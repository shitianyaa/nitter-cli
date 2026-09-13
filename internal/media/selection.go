package media

// The M9 selection half: turn one status's resolved media into the download
// plan the command layer executes — a pure function over the []MediaResolution
// a single winning strategy produced, with no I/O of its own. PlanDownload is
// the decision layer between resolve (which links exist) and FetchToFile
// (which fetches exactly one URL): it owns the plugin's video-wins download
// semantics, ranked convergence over the quality tiers, the fallback chain
// and the --kind filter.
//
// Selection semantics (plan M9, plugin-endorsed):
//   - video/gif present → converge to ONE planned file. The candidate pool is
//     the entries' variants (or, for variant-less entries, their direct links
//     ranked by the p-number of an xdown button label) and the winner is
//     ranked with the resolve layer's exact selectVariant semantics — the
//     same quality tiers over the same key scale. The fallback chain is the
//     winner's own fallback link first, then the remaining candidates in
//     descending rank order.
//   - no video/gif → every image is planned in order, Seq 1..N (a tweet
//     carries at most four; the plan never truncates).
//   - --kind is a PRE-filter: the entries are filtered by kind first, then
//     the rules above apply to what is left. A filter matching nothing (a
//     video tweet asked for images from a source that lists none) yields an
//     empty plan without error — the command layer renders that as nothing
//     found for the ref. --kind cover is its own selection mode: it plans
//     exactly the CoverURL file of a status with moving media and refuses
//     with not_found errors otherwise.
//   - Playlists (HLS m3u8 and DASH mpd) are never download candidates: the
//     syndication payload's x-mpegURL variants ride along the M8 resolution
//     output untouched, but the pool and the fallback chain exclude them.
//   - Scheme policy stays with the resolve layer (M8): the selection neither
//     re-filters plain-http links nor can it reintroduce them into a plan
//     built from the resolve output.
//
// Ext is derived from the URL path where one exists and may stay empty — the
// final extension is decided at write time from the response's Content-Type.

import (
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/shitianyaa/nitter-cli/sdk"
)

const opPlan = "media.PlanDownload"

// kindFilterCover is the --kind value that plans only a status's cover image;
// the other filter values reuse the media kinds.
const kindFilterCover = "cover"

// PlannedFile is one file the download command will fetch for a status.
type PlannedFile struct {
	// StatusID is the numeric status ID the file belongs to (the caller's
	// refID — the filename stem is built from it).
	StatusID string
	// Seq is the file's 1-based position within the plan: "1" for the single
	// converged video/gif/cover file, "1".."N" across the images.
	Seq string
	// Kind is the planned file's media kind: "image", "video" or "gif" — or
	// "cover" for a --kind cover plan.
	Kind string
	// Ext is the URL path's file extension with its leading dot (".mp4",
	// ".jpg"); empty when the path carries none. The write layer finalizes
	// the extension from the response's Content-Type.
	Ext string
	// URL is the direct link fetched first.
	URL string
	// Fallbacks lists alternative links tried in order when URL — and each
	// earlier fallback — fails: the winning entry's own fallback link first,
	// then the remaining ranked candidates in descending rank order. nil for
	// candidates without alternatives.
	Fallbacks []string
}

// PlanDownload plans the download of one status: the resolved media of the
// winning strategy converges to one file when the status carries video/gif
// (ranked by quality: high=highest, medium=upper median of non-zero, low=
// lowest — the resolve layer's exact variant semantics, with xdown's
// label p-numbers as the ranking key for variant-less entries) and to every
// image in order otherwise. kindFilter pre-filters the entries ("", "image",
// "video", "gif"); "cover" plans exactly the cover file instead. A filter
// matching nothing yields an empty plan without error; an empty resolution,
// an unknown filter value and a cover request without a coverable status are
// classified errors.
func PlanDownload(refID string, res []nitter.MediaResolution, quality, kindFilter string) ([]PlannedFile, error) {
	switch filter := strings.ToLower(strings.TrimSpace(kindFilter)); filter {
	case "", kindImage, kindVideo, kindGif, kindFilterCover:
		kindFilter = filter
	default:
		return nil, nitter.Errorf(nitter.KindInvalidArg, opPlan, "unknown kind filter %q", kindFilter)
	}
	if len(res) == 0 {
		return nil, nitter.Errorf(nitter.KindNotFound, opPlan, "no media resolved for status %s", refID)
	}
	if kindFilter == kindFilterCover {
		return planCover(refID, res)
	}

	entries := res
	if kindFilter != "" {
		entries = make([]nitter.MediaResolution, 0, len(res))
		for _, m := range res {
			if m.Kind == kindFilter {
				entries = append(entries, m)
			}
		}
		if len(entries) == 0 {
			// The pre-filter matched nothing: an empty plan (the command
			// layer renders it as nothing found for this ref), not an error.
			return []PlannedFile{}, nil
		}
	}

	pool := videoPool(entries)
	if len(pool) == 0 {
		if hasMovingMedia(entries) {
			// The status carries moving media, but every encoding is a
			// playlist: nothing downloadable exists to plan (the plan never
			// falls through to the images of a video tweet — video-wins is
			// the rule, and a refusal beats a silent substitution).
			return nil, nitter.Errorf(nitter.KindNotFound, opPlan, "status %s has no downloadable video candidate (playlists excluded)", refID)
		}
		return planImages(refID, entries), nil
	}
	return []PlannedFile{planConverged(refID, pool, quality)}, nil
}

// hasMovingMedia reports whether any entry is a video/gif.
func hasMovingMedia(entries []nitter.MediaResolution) bool {
	for _, m := range entries {
		if m.Kind == kindVideo || m.Kind == kindGif {
			return true
		}
	}
	return false
}

// planCover plans exactly the cover image of a status with moving media: the
// first non-empty CoverURL of its video/gif entries. An image-only status has
// no cover to take and a video without a captured cover is refused — both
// not_found.
func planCover(refID string, res []nitter.MediaResolution) ([]PlannedFile, error) {
	cover := ""
	hasMoving := false
	for _, m := range res {
		if m.Kind != kindVideo && m.Kind != kindGif {
			continue
		}
		hasMoving = true
		if cover == "" {
			cover = m.CoverURL
		}
	}
	if !hasMoving {
		return nil, nitter.Errorf(nitter.KindNotFound, opPlan, "status %s has no video or gif: no cover to plan", refID)
	}
	if cover == "" {
		return nil, nitter.Errorf(nitter.KindNotFound, opPlan, "status %s carries no cover image for its video", refID)
	}
	return []PlannedFile{{
		StatusID: refID,
		Seq:      "1",
		Kind:     kindFilterCover,
		Ext:      extFromURL(cover),
		URL:      cover,
	}}, nil
}

// planImages plans every image entry in order: one PlannedFile each with Seq
// 1..N, the entry's own fallback link (the xdown proxy standing in for a
// direct link) carried along when present.
func planImages(refID string, entries []nitter.MediaResolution) []PlannedFile {
	files := make([]PlannedFile, 0, len(entries))
	for _, m := range entries {
		if m.Kind != kindImage {
			continue
		}
		files = append(files, PlannedFile{
			StatusID:  refID,
			Seq:       strconv.Itoa(len(files) + 1),
			Kind:      m.Kind,
			Ext:       extFromURL(m.URL),
			URL:       m.URL,
			Fallbacks: fallbackChain(m.URL, m.FallbackURL),
		})
	}
	return files
}

// videoCandidate is one ranked download candidate of a status's moving
// media: a video/gif entry's variant or its variant-less direct link. key is
// the quality rank — a variant's bitrate, or the p-number of an xdown label —
// so one scale feeds the resolve layer's ranking semantics.
type videoCandidate struct {
	url      string
	key      int64
	kind     string
	fallback string
}

// videoPool collects the downloadable candidates of a resolution list's
// video/gif entries. An entry with variants feeds the pool from its variants
// alone (playlists excluded); a variant-less entry contributes its direct
// link, ranked by the p-number in its label ("下载 MP4 (1920p)") when one is
// present. URLs dedupe onto the first occurrence, upstream order preserved.
func videoPool(entries []nitter.MediaResolution) []videoCandidate {
	pool := make([]videoCandidate, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, m := range entries {
		if m.Kind != kindVideo && m.Kind != kindGif {
			continue
		}
		if len(m.Variants) > 0 {
			for _, v := range m.Variants {
				if v.URL == "" || seen[v.URL] || isPlaylistVariant(v) {
					continue
				}
				seen[v.URL] = true
				pool = append(pool, videoCandidate{url: v.URL, key: v.Bitrate, kind: m.Kind, fallback: m.FallbackURL})
			}
			continue
		}
		if m.URL == "" || seen[m.URL] || isPlaylistURL(m.URL) {
			continue
		}
		seen[m.URL] = true
		pool = append(pool, videoCandidate{
			url:      m.URL,
			key:      pNumberFromLabel(m.Label),
			kind:     m.Kind,
			fallback: m.FallbackURL,
		})
	}
	return pool
}

// planConverged ranks the candidate pool with the resolve layer's exact
// variant semantics (selectVariant over the bitrate/p-number keys) and
// converges the winner into ONE planned file whose fallback chain is the
// winner's own fallback link followed by the remaining candidates in
// descending rank order.
func planConverged(refID string, pool []videoCandidate, quality string) PlannedFile {
	variants := make([]nitter.MediaVariant, len(pool))
	for i, c := range pool {
		variants[i] = nitter.MediaVariant{URL: c.url, Bitrate: c.key}
	}
	win, _ := selectVariant(variants, quality)
	winner := pool[0]
	for _, c := range pool {
		if c.url == win.URL {
			winner = c
			break
		}
	}

	chain := []string{winner.url}
	if fb := winner.fallback; fb != "" && fb != winner.url && !isPlaylistURL(fb) {
		chain = append(chain, fb)
	}
	for _, c := range descendingByRank(pool) {
		if c.url == winner.url || containsURL(chain, c.url) {
			continue
		}
		chain = append(chain, c.url)
	}

	var fallbacks []string
	if len(chain) > 1 {
		fallbacks = chain[1:]
	}
	return PlannedFile{
		StatusID:  refID,
		Seq:       "1",
		Kind:      winner.kind,
		Ext:       extFromURL(winner.url),
		URL:       winner.url,
		Fallbacks: fallbacks,
	}
}

// fallbackChain renders one source-provided alternative link as a planned
// file's fallback slice: [fallback] when it is a real, non-playlist link
// distinct from the main URL; nil otherwise.
func fallbackChain(main, fallback string) []string {
	if fallback == "" || fallback == main || isPlaylistURL(fallback) {
		return nil
	}
	return []string{fallback}
}

// descendingByRank returns the pool ordered by descending quality key
// (stable: equal keys keep upstream order) — the fallback chain's tail order.
func descendingByRank(pool []videoCandidate) []videoCandidate {
	out := make([]videoCandidate, len(pool))
	copy(out, pool)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].key > out[j-1].key; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// containsURL reports whether the chain already carries u (the fallback
// chain never repeats a link).
func containsURL(chain []string, u string) bool {
	for _, c := range chain {
		if c == u {
			return true
		}
	}
	return false
}

// pNumberRe extracts the resolution p-number an xdown button label carries
// ("下载 MP4 (1920p)"). The video_probe-style resolution helper the plan
// names is absent from xdown.go (R-M8-5 skipped extract_video_resolution),
// so the label text is scanned directly.
var pNumberRe = regexp.MustCompile(`(\d{3,4})p`)

// pNumberFromLabel extracts a source label's p-number as the ranking key of a
// variant-less entry; 0 when the label carries none.
func pNumberFromLabel(label string) int64 {
	m := pNumberRe.FindStringSubmatch(label)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// isPlaylistVariant reports whether a variant is an HLS/DASH playlist rather
// than a downloadable file: a playlist content type (x-mpegURL and friends)
// or a playlist path suffix. Playlists never enter the download pool.
func isPlaylistVariant(v nitter.MediaVariant) bool {
	ct := strings.ToLower(v.ContentType)
	if strings.Contains(ct, "mpegurl") || strings.Contains(ct, "dash+xml") {
		return true
	}
	return isPlaylistURL(v.URL)
}

// isPlaylistURL reports whether u's path ends in a playlist extension (.m3u8
// HLS, .mpd DASH).
func isPlaylistURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	switch strings.ToLower(path.Ext(parsed.Path)) {
	case ".m3u8", ".mpd":
		return true
	}
	return false
}

// extFromURL derives a planned file's extension from the URL path (".mp4",
// ".jpg"); empty when the path carries none — the write layer finalizes the
// extension from the response's Content-Type.
func extFromURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	if ext := path.Ext(parsed.Path); len(ext) > 1 {
		return strings.ToLower(ext)
	}
	return ""
}
