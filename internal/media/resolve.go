// Package media resolves a status reference into directly downloadable media
// links (video mp4 variants, original images, GIFs) using the third-party
// public services the user's reference plugin proved in daily use:
// fxtwitter, vxtwitter, Twitter's syndication endpoint and xdown.app. It is
// the implementation layer behind the `nitter media` command; the command
// layer owns strategy orchestration over the sdk projection produced here.
//
// Trust boundary: fx/vx/syndication/xdown are THIRD-PARTY public services
// (unlike the self-hosted Nitter instances). Resolving a status sends its
// URL to them; docs and skill state this, and failures are reported with the
// real strategy name — never silently swapped for another source's success.
//
// Semantics are a faithful port of the plugin's media_support/status_resolve.py:
//   - fx  media.all[]: photo→image, video→video, gif/animated_gif→gif; video
//     URLs come from variants (or video_info.variants) by bitrate.
//   - vx  media_extended[] direct links; legacy mediaURLs/media_urls lists
//     as an image-only fallback used only when media_extended yields nothing.
//   - syndication photos[] images; video.variants by bitrate, gif detected
//     via the "tweet_video_thumb" marker or a gif video type.
//   - xdown ajaxSearch page: download-button anchors, kind by label text
//     then URL suffix (xdown.py); snapcdn token payloads yield the direct
//     twimg link with the proxy link kept as the fallback.
//
// Probe (probe.go) is the optional `--probe` enrichment pass the media
// command runs per resolved URL: one ranged GET fetches a 1 MiB head window,
// whose Content-Range total is the file size and whose body is walked for
// the first mp4 movie header (mvhd) to get the duration — a faithful port
// of media_support/video_probe.py. Probing is best-effort: the two parts
// fail independently into their zero values and only a failed request
// surfaces as a classified error.
//
// Common rules (plan M8): image URLs go through the pbs quality rewrite
// (name=orig|large|small); video quality selects WHICH variant becomes the
// main URL (high=highest bitrate, medium=upper median non-zero, low=lowest
// non-zero) while every variant is kept; results dedupe by final URL; plain
// http links are dropped. A strategy whose payload carries no media is
// reported "empty" and the next strategy is tried; only when all strategies
// fail does ResolveStatus return an aggregate error naming each strategy and
// a short redacted reason.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/nitter-cli/internal/nitter/mediaurl"
	"github.com/shitianyaa/nitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

const opResolve = "media.ResolveStatus"

// Strategy names one media resolution backend. The values are the
// MediaResolution.Source wire values (NDJSON contract).
type Strategy string

const (
	StrategyFx          Strategy = "fx"
	StrategyVx          Strategy = "vx"
	StrategySyndication Strategy = "syndication"
	StrategyNitter      Strategy = "nitter" // resolved by the command layer (M8 Task 4)
	StrategyXdown       Strategy = "xdown"  // parsed from xdown.app's ajaxSearch page (xdown.go)
)

// Media kinds, matching nitter.Media's wire values.
const (
	kindImage = "image"
	kindVideo = "video"
	kindGif   = "gif"
)

// Options configures one ResolveStatus call.
type Options struct {
	// Strategies is the ordered backend list to try. Empty is a
	// KindInvalidArg error; the command layer supplies the auto order
	// fx → vx → syndication → nitter → xdown.
	Strategies []Strategy
	// Quality selects the image pbs tier (high=orig, medium=large, low=small)
	// and which video variant becomes the main URL. "" or an unknown value
	// behaves as high (the plugin default).
	Quality string
	// NitterBase is the base URL of the user's own Nitter instance that the
	// nitter strategy fetches its status page from (<base>/<user>/status/<id>).
	// The third-party strategies never consult it; an empty value makes the
	// nitter strategy fail with a local-state error, which the auto chain
	// reports honestly in its aggregate (never silently skipped). The CLI
	// wiring fills it from the configured instances (config [[instances]] or
	// the --instance override).
	NitterBase string
}

// StatusRef is a parsed status reference: the numeric status ID and the
// user segment of the URL ("" for bare IDs and user-less routes).
type StatusRef struct {
	ID   string
	User string
}

// String renders the canonical x.com permalink used to stamp
// MediaResolution.Ref. User-less references use the /i/status/<id> route
// (the same segment the fx/vx endpoints receive).
func (r StatusRef) String() string {
	return "https://x.com/" + userSegment(r) + "/status/" + r.ID
}

// userSegment is the URL user segment the fx/vx endpoints expect: the handle
// when known, the /i/ placeholder otherwise (plugin: link.username or "i").
func userSegment(r StatusRef) string {
	if r.User == "" {
		return "i"
	}
	return r.User
}

// ParseStatusRef parses one status reference (a bare numeric ID or an
// x.com/twitter.com/nitter-style /status/<id> URL, optional /photo/N or
// /video/1 suffix) into a StatusRef. It delegates to the canonical appapi
// parser — no ref-shape logic is duplicated here; errors are KindInvalidArg.
func ParseStatusRef(s string) (StatusRef, error) {
	id, user, err := appapi.ParseStatusRef(s)
	if err != nil {
		return StatusRef{}, err
	}
	return StatusRef{ID: id, User: user}, nil
}

// Resolver resolves statuses into downloadable media over the shared httpx
// transport (pacing, retries and error classification are its tested
// contract). It is safe for concurrent use.
type Resolver struct {
	// HTTP is the shared transport; it is consulted lazily so a Resolver
	// built for tests can inject a fetch func instead.
	HTTP *httpx.Client
	// Now is the injectable clock (reserved for the probe pass); nil is
	// defaulted to time.Now by NewResolver.
	Now func() time.Time

	// fetch, when set (tests in this package only), replaces HTTP entirely:
	// it reproduces httpx.Client.Get's (body, status, classified error)
	// surface. Unexported, because httpx.Options' own doer hook is
	// unexported and out-of-package callers always get the real transport.
	fetch func(ctx context.Context, url string, headers map[string]string) ([]byte, int, error)
	// fetchPost is the POST counterpart of fetch (httpx.Client.Post's
	// surface, body and headers verbatim); the xdown backend sends its
	// ajaxSearch form through it.
	fetchPost func(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, int, error)
	// fetchMeta is the probe's seam (tests in this package only): it
	// reproduces httpx.Client.GetMeta's (body, status, response headers,
	// classified error) surface — the plain fetch seam cannot carry response
	// headers, and the probe reads Content-Range from one.
	fetchMeta func(ctx context.Context, url string, headers map[string]string) ([]byte, int, map[string][]string, error)
}

// NewResolver builds a Resolver over a freshly constructed httpx transport.
// No network I/O happens here.
func NewResolver(opts httpx.Options, now func() time.Time) (*Resolver, error) {
	c, err := httpx.New(opts)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Resolver{HTTP: c, Now: now}, nil
}

// get routes one GET through the injected fetch when present (tests), the
// shared transport otherwise.
func (r *Resolver) get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	if r.fetch != nil {
		return r.fetch(ctx, url, headers)
	}
	if r.HTTP == nil {
		return nil, 0, nitter.Errorf(nitter.KindLocalState, opResolve, "no transport wired into the media resolver")
	}
	return r.HTTP.Get(ctx, url, headers)
}

// post routes one POST through the injected fetchPost when present (tests),
// the shared transport otherwise. The body travels verbatim; the caller owns
// Content-Type in the header map (mirrors the plugin, which builds it into
// its own header mapping).
func (r *Resolver) post(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, int, error) {
	if r.fetchPost != nil {
		return r.fetchPost(ctx, url, body, headers)
	}
	if r.HTTP == nil {
		return nil, 0, nitter.Errorf(nitter.KindLocalState, opResolve, "no transport wired into the media resolver")
	}
	return r.HTTP.Post(ctx, url, body, headers)
}

// getMeta routes one header-bearing GET through the injected fetchMeta when
// present (tests), the shared transport otherwise. Only the probe uses it.
func (r *Resolver) getMeta(ctx context.Context, url string, headers map[string]string) ([]byte, int, map[string][]string, error) {
	if r.fetchMeta != nil {
		return r.fetchMeta(ctx, url, headers)
	}
	if r.HTTP == nil {
		return nil, 0, nil, nitter.Errorf(nitter.KindLocalState, opProbe, "no transport wired into the media resolver")
	}
	return r.HTTP.GetMeta(ctx, url, headers)
}

// The third-party services receive the plugin's fixed browser identity:
// headers are exactly what build_request_headers sends (media_support/network.py).
const (
	mediaUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	mediaAcceptLanguage = "en-US,en;q=0.9,zh-CN;q=0.8"
	mediaAccept         = "application/json,text/plain,*/*"
)

// jsonHeaders is the header set every JSON backend request carries.
func jsonHeaders() map[string]string {
	return map[string]string{
		"User-Agent":      mediaUserAgent,
		"Accept-Language": mediaAcceptLanguage,
		"Accept":          mediaAccept,
	}
}

// mediaCandidate is one media entry extracted from a backend payload,
// before quality rewriting, variant selection and dedup.
type mediaCandidate struct {
	// Kind is one of kindImage / kindVideo / kindGif.
	Kind string
	// URL is the entry's own direct link: the image URL (pbs tier rewrite
	// applies) or the video link used when the source offers no variants.
	URL string
	// Label is the source's own display caption for the entry (the xdown
	// button text); empty when the source has none.
	Label string
	// FallbackURL is a source-provided alternative link for the same media
	// (the xdown snapcdn proxy standing in for an extracted direct link).
	FallbackURL string
	// DurationSeconds is a duration the source itself reported for the entry
	// (xdown token payload keys or label clock text); 0 when unknown.
	DurationSeconds float64
	// Variants lists every encoding the source offered (upstream order);
	// empty when the entry is a plain direct link.
	Variants []nitter.MediaVariant
	// Width and Height are the source's dimensions when present.
	Width  int
	Height int
}

// ResolveStatus tries the requested strategies in order and returns the
// media of the first one that yields any, each stamped with the canonical
// Ref and the winning Source. A strategy whose payload parses but carries no
// media is skipped as "empty"; a strategy whose fetch or parse fails records
// a short redacted reason. When no strategy produces media the returned
// error names every attempt: KindNotFound when they all reported empty (the
// status has no downloadable media), otherwise the last strategy failure's
// kind with every reason in the message. Parsing never fabricates entries:
// plain-http links are dropped, duplicates collapse onto the first entry.
func (r *Resolver) ResolveStatus(ctx context.Context, ref StatusRef, opts Options) ([]nitter.MediaResolution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(opts.Strategies) == 0 {
		return nil, nitter.Errorf(nitter.KindInvalidArg, opResolve, "no media strategies requested")
	}
	var parts []string
	var lastErr error
	for _, s := range opts.Strategies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := r.resolveOne(ctx, s, ref, opts)
		if err != nil {
			parts = append(parts, string(s)+": "+shortReason(err))
			lastErr = err
			continue
		}
		if len(res) == 0 {
			parts = append(parts, string(s)+": empty")
			continue
		}
		for i := range res {
			res[i].Ref = ref.String()
			res[i].Source = string(s)
		}
		return res, nil
	}

	joined := strings.Join(parts, "; ")
	if lastErr != nil {
		kind := nitter.KindUnavailable
		var terr *nitter.Error
		if errors.As(lastErr, &terr) && terr.Kind != "" {
			kind = terr.Kind
		}
		return nil, &nitter.Error{
			Kind: kind,
			Op:   opResolve,
			Err:  fmt.Errorf("all media strategies failed (%s): %w", joined, lastErr),
		}
	}
	return nil, nitter.Errorf(nitter.KindNotFound, opResolve, "no media found (%s)", joined)
}

// resolveOne runs one strategy: build URL, fetch, extract media, apply the
// common quality/dedup rules. Zero resolutions mean "empty" (the caller
// moves to the next strategy); an error is the strategy's classified failure.
func (r *Resolver) resolveOne(ctx context.Context, s Strategy, ref StatusRef, opts Options) ([]nitter.MediaResolution, error) {
	quality := normalizeQuality(opts.Quality)
	var (
		cands []mediaCandidate
		err   error
	)
	switch s {
	case StrategyFx:
		var body []byte
		if body, _, err = r.get(ctx, fxStatusURL(ref), jsonHeaders()); err != nil {
			return nil, err
		}
		cands, err = parseFx(body)
	case StrategyVx:
		var body []byte
		if body, _, err = r.get(ctx, vxStatusURL(ref), jsonHeaders()); err != nil {
			return nil, err
		}
		cands, err = parseVx(body)
	case StrategySyndication:
		var body []byte
		if body, _, err = r.get(ctx, syndicationURL(ref.ID), jsonHeaders()); err != nil {
			return nil, err
		}
		cands, err = parseSyndication(body)
	case StrategyNitter:
		// ResolveNitter fetches and finalizes itself: the page's media keeps
		// instance-served (possibly plain-http) links the shared https-only
		// tail below would wrongly drop, and the parser already applied its
		// own dedup and pbs rules.
		return r.ResolveNitter(ctx, ref, opts)
	case StrategyXdown:
		// ResolveXdown fetches and finalizes itself (its candidates carry
		// labels, fallback URLs and durations the shared tail below would
		// only re-apply).
		return r.ResolveXdown(ctx, ref, opts)
	default:
		return nil, nitter.Errorf(nitter.KindLocalState, opResolve, "media strategy %q is not implemented by this package", string(s))
	}
	if err != nil {
		return nil, err
	}
	return finalize(cands, quality), nil
}

// Quality tiers (plugin media_quality; `_PBS_QUALITY_NAME` for images).
const (
	qualityHigh   = "high"
	qualityMedium = "medium"
	qualityLow    = "low"
)

// normalizeQuality validates the quality option: anything but the three
// known tiers (case-insensitive) behaves as high.
func normalizeQuality(q string) string {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case qualityMedium:
		return qualityMedium
	case qualityLow:
		return qualityLow
	default:
		return qualityHigh
	}
}

// finalize applies the common post-parse rules in order: https-only
// filtering, image pbs quality rewrite, video variant selection (main URL by
// quality, highest variant as fallback), then dedup by final URL keeping the
// first occurrence.
func finalize(cands []mediaCandidate, quality string) []nitter.MediaResolution {
	out := make([]nitter.MediaResolution, 0, len(cands))
	seen := make(map[string]bool, len(cands))
	for _, c := range cands {
		res, ok := project(c, quality)
		if !ok || seen[res.URL] {
			continue
		}
		seen[res.URL] = true
		out = append(out, res)
	}
	return out
}

// project turns one candidate into its final resolution; ok is false when
// the candidate has no https link to offer (dropped, never projected).
func project(c mediaCandidate, quality string) (nitter.MediaResolution, bool) {
	res := nitter.MediaResolution{
		Kind:            c.Kind,
		Label:           c.Label,
		FallbackURL:     c.FallbackURL,
		DurationSeconds: c.DurationSeconds,
		Width:           c.Width,
		Height:          c.Height,
	}
	if c.Kind == kindImage {
		u := mediaurl.RewritePBSTier(c.URL, quality)
		if !isHTTPS(u) {
			return nitter.MediaResolution{}, false
		}
		res.URL = u
		return res, true
	}

	// Video or GIF: quality selects the main variant; the highest-bitrate
	// variant (what the high tier — and the plugin — would pick) is kept as
	// the fallback link when it differs.
	pool := httpsVariants(c.Variants)
	if len(pool) > 0 {
		main, _ := selectVariant(pool, quality)
		top, _ := selectVariant(pool, qualityHigh)
		res.URL = main.URL
		if top.URL != main.URL {
			res.FallbackURL = top.URL
		}
		res.Variants = pool
		return res, true
	}
	if !isHTTPS(c.URL) {
		return nitter.MediaResolution{}, false
	}
	res.URL = c.URL
	return res, true
}

// httpsVariants filters the variant list to plain-http-free, https-only
// entries (upstream order preserved): a variant is itself a downloadable
// link, so the no-plain-http rule applies to the whole list.
func httpsVariants(variants []nitter.MediaVariant) []nitter.MediaVariant {
	out := make([]nitter.MediaVariant, 0, len(variants))
	for _, v := range variants {
		if isHTTPS(v.URL) {
			out = append(out, v)
		}
	}
	return out
}

// isHTTPS reports whether u is an absolute https URL — the only link shape
// this package projects (plan rule: plain-http direct links are dropped).
func isHTTPS(u string) bool {
	parsed, err := url.Parse(u)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

// selectVariant picks the variant the quality tier asks for:
//
//	high   — highest bitrate, ties to the LATER variant (plugin-exact
//	         `bitrate >= best_bitrate` semantics; zero-bitrate entries take
//	         part and win only when every entry is zero).
//	medium — upper median of the NON-ZERO bitrates (first variant in
//	         upstream order on ties); with an even count the better half
//	         wins, so a two-tier video behaves like high.
//	low    — lowest NON-ZERO bitrate, first variant in upstream order on
//	         ties.
//
// medium/low degrade to the high rule over all-zero lists so a URL is always
// selected; ok is false only for an empty variant list.
func selectVariant(variants []nitter.MediaVariant, quality string) (nitter.MediaVariant, bool) {
	if len(variants) == 0 {
		return nitter.MediaVariant{}, false
	}
	highest := func() nitter.MediaVariant {
		best := variants[0]
		for _, v := range variants[1:] {
			if v.Bitrate >= best.Bitrate {
				best = v
			}
		}
		return best
	}
	switch normalizeQuality(quality) {
	case qualityMedium:
		var nonzero []nitter.MediaVariant
		for _, v := range variants {
			if v.Bitrate > 0 {
				nonzero = append(nonzero, v)
			}
		}
		if len(nonzero) == 0 {
			return highest(), true
		}
		sorted := make([]nitter.MediaVariant, len(nonzero))
		copy(sorted, nonzero)
		sortByBitrate(sorted)
		median := sorted[len(sorted)/2].Bitrate
		for _, v := range nonzero {
			if v.Bitrate == median {
				return v, true
			}
		}
		return nonzero[0], true
	case qualityLow:
		var best nitter.MediaVariant
		found := false
		for _, v := range variants {
			if v.Bitrate <= 0 {
				continue
			}
			if !found || v.Bitrate < best.Bitrate {
				best = v
				found = true
			}
		}
		if !found {
			return highest(), true
		}
		return best, true
	default:
		return highest(), true
	}
}

// sortByBitrate orders variants by ascending bitrate (stable on equal
// rates); insertion sort keeps the tiny variant lists allocation-free.
func sortByBitrate(v []nitter.MediaVariant) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j].Bitrate < v[j-1].Bitrate; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// shortReason renders one strategy failure as a short redacted reason: the
// stable kind plus the already-redacted cause chain, without the op prefix
// (the strategy name in front of it plays that role in the aggregate).
func shortReason(err error) string {
	var terr *nitter.Error
	if errors.As(err, &terr) {
		if terr.Err != nil {
			return string(terr.Kind) + ": " + terr.Err.Error()
		}
		return string(terr.Kind)
	}
	return err.Error()
}

// kindFromType ports the plugin's _kind_from_type: video → video; the gif
// aliases gif/animated_gif/dynamic → gif; everything else (photo, unknown,
// empty) → image.
func kindFromType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "video":
		return kindVideo
	case "gif", "animated_gif", "dynamic":
		return kindGif
	default:
		return kindImage
	}
}

// decodeJSONObject decodes one backend response: the body must be a single
// JSON object (the plugin's "invalid json object" gate) — arrays, scalars,
// null and garbage classify as KindMalformed.
func decodeJSONObject(op string, body []byte, out any) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil || probe == nil {
		return nitter.Errorf(nitter.KindMalformed, op, "response is not a JSON object")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nitter.Errorf(nitter.KindMalformed, op, "decode response fields: %v", err)
	}
	return nil
}

// unmarshalItems decodes each element of a raw JSON array independently and
// skips elements that do not decode into T — the port of the plugin's
// isinstance guards, which drop malformed entries instead of failing the
// whole payload.
func unmarshalItems[T any](items []json.RawMessage) []T {
	out := make([]T, 0, len(items))
	for _, raw := range items {
		if v, ok := decodeAs[T](raw); ok {
			out = append(out, v)
		}
	}
	return out
}

// decodeAs decodes raw into T, reporting failure instead of erroring — the
// shape-probing companion to unmarshalItems.
func decodeAs[T any](raw json.RawMessage) (T, bool) {
	var v T
	if json.Unmarshal(raw, &v) != nil {
		return v, false
	}
	return v, true
}
