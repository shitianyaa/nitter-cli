// Package media resolves a status reference into directly downloadable media
// links (video mp4 variants, original images, GIFs) using the third-party
// public services the user's reference plugin proved in daily use:
// fxtwitter, vxtwitter and Twitter's syndication endpoint. It is the
// implementation layer behind the `twitter media` command; the command layer
// owns strategy orchestration over the sdk projection produced here.
//
// Trust boundary: fx/vx/syndication are THIRD-PARTY public services (unlike
// the self-hosted Nitter instances). Resolving a status sends its URL to
// them; docs and skill state this, and failures are reported with the real
// strategy name — never silently swapped for another source's success.
//
// Semantics are a faithful port of the plugin's media_support/status_resolve.py:
//   - fx  media.all[]: photo→image, video→video, gif/animated_gif→gif; video
//     URLs come from variants (or video_info.variants) by bitrate.
//   - vx  media_extended[] direct links; legacy mediaURLs/media_urls lists
//     as an image-only fallback used only when media_extended yields nothing.
//   - syndication photos[] images; video.variants by bitrate, gif detected
//     via the "tweet_video_thumb" marker or a gif video type.
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

	"github.com/shitianyaa/twitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/twitter-cli/internal/nitter/mediaurl"
	"github.com/shitianyaa/twitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/twitter-cli/sdk"
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
	StrategyXdown       Strategy = "xdown"  // parsed by internal/media in M8 Task 2
)

// Media kinds, matching twitter.Media's wire values.
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
		return nil, 0, twitter.Errorf(twitter.KindLocalState, opResolve, "no transport wired into the media resolver")
	}
	return r.HTTP.Get(ctx, url, headers)
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
	// Variants lists every encoding the source offered (upstream order);
	// empty when the entry is a plain direct link.
	Variants []twitter.MediaVariant
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
func (r *Resolver) ResolveStatus(ctx context.Context, ref StatusRef, opts Options) ([]twitter.MediaResolution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(opts.Strategies) == 0 {
		return nil, twitter.Errorf(twitter.KindInvalidArg, opResolve, "no media strategies requested")
	}
	quality := normalizeQuality(opts.Quality)

	var parts []string
	var lastErr error
	for _, s := range opts.Strategies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := r.resolveOne(ctx, s, ref, quality)
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
		kind := twitter.KindUnavailable
		var terr *twitter.Error
		if errors.As(lastErr, &terr) && terr.Kind != "" {
			kind = terr.Kind
		}
		return nil, &twitter.Error{
			Kind: kind,
			Op:   opResolve,
			Err:  fmt.Errorf("all media strategies failed (%s): %w", joined, lastErr),
		}
	}
	return nil, twitter.Errorf(twitter.KindNotFound, opResolve, "no media found (%s)", joined)
}

// resolveOne runs one strategy: build URL, fetch JSON, extract media, apply
// the common quality/dedup rules. Zero resolutions mean "empty" (the caller
// moves to the next strategy); an error is the strategy's classified failure.
func (r *Resolver) resolveOne(ctx context.Context, s Strategy, ref StatusRef, quality string) ([]twitter.MediaResolution, error) {
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
	default:
		return nil, twitter.Errorf(twitter.KindLocalState, opResolve, "media strategy %q is not implemented by this package", string(s))
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
func finalize(cands []mediaCandidate, quality string) []twitter.MediaResolution {
	out := make([]twitter.MediaResolution, 0, len(cands))
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
func project(c mediaCandidate, quality string) (twitter.MediaResolution, bool) {
	res := twitter.MediaResolution{Kind: c.Kind, Width: c.Width, Height: c.Height}
	if c.Kind == kindImage {
		u := mediaurl.RewritePBSTier(c.URL, quality)
		if !isHTTPS(u) {
			return twitter.MediaResolution{}, false
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
		return twitter.MediaResolution{}, false
	}
	res.URL = c.URL
	return res, true
}

// httpsVariants filters the variant list to plain-http-free, https-only
// entries (upstream order preserved): a variant is itself a downloadable
// link, so the no-plain-http rule applies to the whole list.
func httpsVariants(variants []twitter.MediaVariant) []twitter.MediaVariant {
	out := make([]twitter.MediaVariant, 0, len(variants))
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
func selectVariant(variants []twitter.MediaVariant, quality string) (twitter.MediaVariant, bool) {
	if len(variants) == 0 {
		return twitter.MediaVariant{}, false
	}
	highest := func() twitter.MediaVariant {
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
		var nonzero []twitter.MediaVariant
		for _, v := range variants {
			if v.Bitrate > 0 {
				nonzero = append(nonzero, v)
			}
		}
		if len(nonzero) == 0 {
			return highest(), true
		}
		sorted := make([]twitter.MediaVariant, len(nonzero))
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
		var best twitter.MediaVariant
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
func sortByBitrate(v []twitter.MediaVariant) {
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
	var terr *twitter.Error
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
		return twitter.Errorf(twitter.KindMalformed, op, "response is not a JSON object")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return twitter.Errorf(twitter.KindMalformed, op, "decode response fields: %v", err)
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
