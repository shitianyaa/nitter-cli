package media

// xdown.app backend: a POST to https://xdown.app/api/ajaxSearch with the
// status URL returns {"status":"ok","data":"<html>"} whose HTML carries the
// download buttons (a.tw-button-dl / a.abutton, either class). Each button's
// kind comes from its label text first (mp4/video, gif, 图片/image/photo)
// with the URL suffix sets as the fallback; entries whose kind stays unknown
// are skipped, never fabricated. snapcdn proxy links carry a JWT-shaped
// token parameter whose middle segment is base64url JSON holding the
// original twimg direct link — an https direct link becomes the main URL
// (images go through the pbs quality rewrite) and the proxy link is kept as
// the fallback; without one the proxy link stays the main URL. Durations
// come from the token payload's duration keys or M:SS / "N seconds" text
// forms across label, URL and payload strings.
//
// Port of the plugin's media_support/xdown.py (XdownMediaParser),
// service.py:397-465 (_resolve_media_candidates) and video_probe.py
// (xdown_token_payload, duration_from_mapping/duration_from_text). One
// deliberate deviation (plan ruling R-M8-5): the plugin's
// extract_video_resolution is NOT ported — the CLI has no consumer for the
// single max-dimension number it extracts (video quality caps are an
// out-of-MVP feature), so MediaResolution.Width/Height stay zero instead of
// fabricating pixel dimensions; the button labels already carry the
// "720p"-style text for display, exactly as the plugin shows them.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/shitianyaa/twitter-cli/sdk"
)

const (
	opXdown        = "media.xdown"
	xdownBaseURL   = "https://xdown.app"
	xdownSearchURL = xdownBaseURL + "/api/ajaxSearch"
)

// ResolveXdown resolves one status via xdown.app's ajaxSearch endpoint and
// returns the parsed media with Ref and Source ("xdown") stamped. A response
// whose status is not "ok" (or whose data HTML carries no recognizable
// media) yields an empty slice with a nil error — the next-strategy
// semantics ResolveStatus applies; transport failures and malformed payloads
// return the classified error.
func (r *Resolver) ResolveXdown(ctx context.Context, ref StatusRef, opts Options) ([]twitter.MediaResolution, error) {
	form := url.Values{"q": {ref.String()}, "lang": {"zh-cn"}}.Encode()
	body, _, err := r.post(ctx, xdownSearchURL, []byte(form), xdownHeaders())
	if err != nil {
		return nil, err
	}
	cands, err := parseXdown(body)
	if err != nil {
		return nil, err
	}
	res := finalize(cands, normalizeQuality(opts.Quality))
	for i := range res {
		res[i].Ref = ref.String()
		res[i].Source = string(StrategyXdown)
	}
	return res, nil
}

// xdownHeaders is the header set the plugin's ajaxSearch POST carries:
// build_request_headers(accept="application/json, text/plain, */*",
// referer="https://xdown.app/", extra={Content-Type, Origin}) — note the
// spaced Accept form, distinct from the JSON backends'.
func xdownHeaders() map[string]string {
	return map[string]string{
		"User-Agent":      mediaUserAgent,
		"Accept-Language": mediaAcceptLanguage,
		"Accept":          "application/json, text/plain, */*",
		"Referer":         xdownBaseURL + "/",
		"Origin":          xdownBaseURL,
		"Content-Type":    "application/x-www-form-urlencoded",
	}
}

// xdownResponse is the ajaxSearch envelope. Data stays raw so the plugin's
// tolerance survives: a null or absent data field means an empty page, not a
// decode failure (service.py: str(payload.get("data") or "")).
type xdownResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// parseXdown decodes one ajaxSearch response. A status other than "ok" is
// the plugin's `return []`: an empty candidate list, not an error.
func parseXdown(body []byte) ([]mediaCandidate, error) {
	var payload xdownResponse
	if err := decodeJSONObject(opXdown, body, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "ok" {
		return nil, nil
	}
	var html string
	if len(payload.Data) > 0 {
		// A JSON string decodes into html; null/absent leave it empty. Any
		// other JSON shape degrades to an empty page — it can carry no
		// anchors either way.
		_ = json.Unmarshal(payload.Data, &html)
	}
	return xdownCandidates(html), nil
}

// xdownCandidates extracts the media entries from the ajaxSearch data HTML:
// every anchor carrying either download-button class contributes one
// candidate, in document order.
func xdownCandidates(html string) []mediaCandidate {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		// goquery over a string reader cannot fail; kept defensive so the
		// contract stays "no panic, no media".
		return nil
	}
	cands := make([]mediaCandidate, 0)
	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		cls, _ := s.Attr("class")
		if !xdownButtonClass(cls) {
			return
		}
		href, _ := s.Attr("href")
		if href == "" {
			return // plugin: anchors without a link are dropped
		}
		label := strings.TrimSpace(s.Text())
		full := xdownURLJoin(xdownBaseURL, href)
		kind := xdownDetectKind(label, href)
		if kind == "" {
			// The service loop's second chance: the absolute URL may end in
			// a recognizable suffix the raw href did not.
			kind = xdownDetectKind("", full)
		}
		if kind == "" {
			return // unidentifiable kind: skipped, never fabricated
		}
		payload := xdownTokenPayload(full)
		cand := mediaCandidate{
			Kind:            kind,
			Label:           label,
			DurationSeconds: xdownDuration(label, full, payload),
		}
		// The snapcdn proxy URL's token holds the original twimg direct
		// link: prefer it as the main URL (permanent, quality-rewritable)
		// and keep the proxy as the fallback. Only https direct links
		// qualify (the plugin's startswith gate) — the proxy link itself is
		// always https (it was joined against the https base).
		if direct := strings.TrimSpace(xdownJSONText(payload["url"])); strings.HasPrefix(direct, "https://") {
			cand.URL = direct
			cand.FallbackURL = full
		} else {
			cand.URL = full
		}
		cands = append(cands, cand)
	})
	return cands
}

// xdownButtonClass reports whether a class attribute contains either of the
// download-button classes (the plugin's set intersection over the
// whitespace-split class list).
func xdownButtonClass(class string) bool {
	for _, f := range strings.Fields(class) {
		if f == "tw-button-dl" || f == "abutton" {
			return true
		}
	}
	return false
}

// URL suffix sets porting media_support/extensions.py; the suffix check cuts
// the URL at the first '?' or '#'.
var (
	xdownVideoSuffixes = []string{".mp4", ".m4v", ".mov", ".webm", ".mkv", ".avi"}
	xdownGifSuffixes   = []string{".gif"}
	xdownImageSuffixes = []string{".jpg", ".jpeg", ".png", ".webp", ".bmp", ".svg"}
)

// xdownDetectKind ports XdownMediaParser._detect_kind: the label text
// decides first (mp4/video → video, gif → gif, 图片/image/photo → image,
// case-insensitive), then the URL suffix sets act as the fallback. "" means
// unidentifiable — the caller skips the entry.
func xdownDetectKind(text, u string) string {
	lowered := strings.ToLower(text)
	switch {
	case strings.Contains(lowered, "mp4") || strings.Contains(lowered, "video"):
		return kindVideo
	case strings.Contains(lowered, "gif"):
		return kindGif
	case strings.Contains(text, "图片") || strings.Contains(lowered, "image") || strings.Contains(lowered, "photo"):
		return kindImage
	}
	switch {
	case xdownSuffixMatches(u, xdownVideoSuffixes):
		return kindVideo
	case xdownSuffixMatches(u, xdownGifSuffixes):
		return kindGif
	case xdownSuffixMatches(u, xdownImageSuffixes):
		return kindImage
	}
	return ""
}

// xdownSuffixMatches ports suffix_matches: the URL is lowercased and cut at
// the first '?' or '#', then compared by suffix.
func xdownSuffixMatches(u string, suffixes []string) bool {
	normalized := strings.ToLower(u)
	if i := strings.IndexByte(normalized, '?'); i >= 0 {
		normalized = normalized[:i]
	}
	if i := strings.IndexByte(normalized, '#'); i >= 0 {
		normalized = normalized[:i]
	}
	for _, s := range suffixes {
		if strings.HasSuffix(normalized, s) {
			return true
		}
	}
	return false
}

// xdownURLJoin ports urljoin("https://xdown.app", href): absolute URLs pass
// through unchanged, relative paths resolve against the xdown base.
// Unresolvable hrefs come back unchanged (the https rule still applies to
// them downstream).
func xdownURLJoin(base, href string) string {
	b, err := url.Parse(base)
	if err != nil {
		return href
	}
	ref, err := b.Parse(href)
	if err != nil {
		return href
	}
	return ref.String()
}

// xdownTokenPayload decodes the JWT-shaped token query parameter of a
// snapcdn proxy URL: the middle dot-segment is base64url JSON (port of
// video_probe.xdown_token_payload). Anything absent, truncated or
// undecodable yields nil — the candidate simply loses its direct link, never
// its snapcdn one.
func xdownTokenPayload(rawURL string) map[string]any {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	token := u.Query().Get("token")
	if token == "" {
		return nil
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	segment := parts[1]
	if pad := len(segment) % 4; pad != 0 {
		segment += strings.Repeat("=", 4-pad)
	}
	decoded, err := base64.URLEncoding.DecodeString(segment)
	if err != nil {
		// The plugin's urlsafe_b64decode also tolerates the standard base64
		// alphabet; try it before giving up.
		if decoded, err = base64.StdEncoding.DecodeString(segment); err != nil {
			return nil
		}
	}
	var payload map[string]any
	if json.Unmarshal(decoded, &payload) != nil || payload == nil {
		return nil
	}
	return payload
}

// xdownJSONText renders one decoded token-payload value the way the plugin's
// str(...) feeds its branch decisions and text scanners (missing/None → "").
func xdownJSONText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// xdownDuration ports extract_video_duration: the token payload's duration
// keys win, then the clock and "N seconds/minutes" text forms across label,
// URL and payload strings. 0 means "no duration found" (the field is
// omitted on the wire).
func xdownDuration(label, fullURL string, payload map[string]any) float64 {
	if d, ok := durationFromMapping(payload); ok {
		return d
	}
	text := strings.Join([]string{
		label,
		fullURL,
		xdownJSONText(payload["filename"]),
		xdownJSONText(payload["url"]),
	}, " ")
	d, _ := durationFromText(text)
	return d
}

// durationFromMapping ports duration_from_mapping: the first key that yields
// a positive duration wins.
func durationFromMapping(payload map[string]any) (float64, bool) {
	for _, key := range []string{"duration", "duration_seconds", "durationSeconds", "length", "length_seconds"} {
		if d, ok := coerceDurationSeconds(payload[key]); ok {
			return d, true
		}
	}
	return 0, false
}

// coerceDurationSeconds ports coerce_duration_seconds: positive numbers
// pass, numeric strings parse, anything else goes through the text scanner.
func coerceDurationSeconds(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		if t > 0 {
			return t, true
		}
		return 0, false
	case string:
		text := strings.TrimSpace(t)
		if text == "" {
			return 0, false
		}
		if n, err := strconv.ParseFloat(text, 64); err == nil {
			if n > 0 {
				return n, true
			}
			return 0, false
		}
		return durationFromText(text)
	default:
		return durationFromText(strings.TrimSpace(xdownJSONText(t)))
	}
}

// The two duration text patterns port duration_from_text's regex table. RE2
// has no lookaround, so the clock pattern is anchored (it only ever matches
// at the scan position) and its digit-boundary guards are enforced by hand
// in clockDuration.
var (
	xdownClockRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?`)
	xdownUnitRe  = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(seconds?|secs?|s|minutes?|mins?|m)\b`)
)

// durationFromText ports duration_from_text: the first clock run (M:SS or
// H:MM:SS, not touching neighboring digits) wins, then the first
// "N seconds/minutes" form.
func durationFromText(text string) (float64, bool) {
	if d, ok := clockDuration(text); ok {
		return d, true
	}
	m := xdownUnitRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	if strings.HasPrefix(strings.ToLower(m[2]), "m") {
		return value * 60, true
	}
	return value, true
}

// clockDuration finds the first M:SS / H:MM:SS run whose edges do not touch
// other digits — the hand-rolled emulation of the plugin's (?<!\d) and
// (?!\d) guards, scanning every start position like re.finditer does.
func clockDuration(text string) (float64, bool) {
	for i := 0; i < len(text); i++ {
		if i > 0 && isASCIIDigit(text[i-1]) {
			continue // (?<!\d): a run glued to a leading digit never matches
		}
		m := xdownClockRe.FindStringSubmatch(text[i:])
		if m == nil {
			continue
		}
		end := i + len(m[0])
		if end < len(text) && isASCIIDigit(text[end]) {
			continue // (?!\d): a run glued to a trailing digit never matches
		}
		first, _ := strconv.Atoi(m[1])
		second, _ := strconv.Atoi(m[2])
		if m[3] == "" {
			return float64(first*60 + second), true
		}
		third, _ := strconv.Atoi(m[3])
		return float64(first*3600 + second*60 + third), true
	}
	return 0, false
}

// isASCIIDigit is the digit test the boundary guards need (multibyte UTF-8
// label text never produces ASCII digit bytes mid-rune).
func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
