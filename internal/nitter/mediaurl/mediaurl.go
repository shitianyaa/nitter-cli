// Package mediaurl normalizes media URLs extracted from Nitter HTML: it
// resolves instance-relative proxy paths against the instance base and
// rewrites pbs.twimg.com media links to their original-quality variant. It is
// the shared URL-normalization step of the Nitter protocol parsers — the RSS
// projection and the HTML timeline parser — so both producers apply the same
// pbs quality rule and (since the R13 ruling) canonicalize /pic/ media
// proxies onto their direct pbs.twimg.com URLs, letting cross-form duplicates
// of one photo dedup to a single media entry.
//
// RewritePBSOrig is a faithful port of the user's reference Python plugin
// (astrbot_plugin_nitter_tweets, prefer_pbs_quality); Absolutize ports its
// abs_url helper. Both run against real Nitter instances daily, so the ports
// keep the exact semantics rather than reinventing them.
package mediaurl

import (
	"net/url"
	"regexp"
	"strings"
)

// pbsNameRe rewrites an existing name= query parameter on pbs media URLs.
var pbsNameRe = regexp.MustCompile(`([?&])name=[^&]*`)

// schemeRe matches a URL scheme prefix (RFC 3986). Porting the plugin's
// abs_url faithfully means delegating to urljoin semantics: any value that
// carries a scheme passes through untouched, so a hostile scheme like
// javascript: survives normalization and is rejected later by the parsers'
// http(s)-only safety gate instead of being silently joined into the
// instance base.
var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*:`)

// RewritePBSOrig ports the plugin's prefer_pbs_quality at the "high" tier:
// pbs.twimg.com/media URLs get their name= parameter replaced with orig, or
// appended when absent; non-pbs URLs pass through unchanged. Only
// pbs.twimg.com/media thumbnails are affected; other pbs hosts are left as-is.
func RewritePBSOrig(u string) string {
	if !strings.Contains(u, "pbs.twimg.com/media/") {
		return u
	}
	if strings.Contains(u, "name=") {
		return pbsNameRe.ReplaceAllString(u, "${1}name=orig")
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "name=orig"
}

// Absolutize resolves a URL extracted from Nitter HTML against the instance
// base, porting the plugin's abs_url: protocol-relative values get https:,
// absolute http(s) values pass through unchanged, and everything else is
// joined against the base with leading slashes collapsed on both sides. An
// empty value or an empty base leaves the input unchanged (no base to join
// against — no fabricated host).
func Absolutize(base, raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "//") {
		return "https:" + value
	}
	if schemeRe.MatchString(value) {
		return value
	}
	if base == "" {
		return value
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(value, "/")
}

// picPrefix is the proxy path prefix Nitter puts in front of media served
// through the instance (/pic/media/…, /pic/media%2F…, /pic/<x>_video_thumb…).
const picPrefix = "/pic/"

// mediaPathPrefix is the decoded /pic/ target of a photo proxy: everything
// after it is the pbs.twimg.com/media path (plus query) the proxy relays.
const mediaPathPrefix = "media/"

// NormalizePicProxy canonicalizes one media URL extracted from a Nitter feed
// or page (ruling R13): an instance /pic/ proxy whose decoded target is a
// pbs.twimg.com media path maps to that direct pbs URL with the name=orig
// quality rewrite applied, so one photo arriving in two forms — a direct
// media:content URL and the percent-encoded /pic/media%2F… proxy of a
// description img — collapses onto a single dedup key. Direct pbs.twimg.com
// media URLs pass through the same quality rewrite. Any other target (video
// thumbnails, emoji, card images, foreign paths) keeps its absolutized form.
//
// The bool reports whether the returned canonical URL is a direct pbs media
// URL. Callers dedup on the returned string.
func NormalizePicProxy(instance, raw string) (string, bool) {
	abs := Absolutize(instance, raw)
	if abs == "" {
		return "", false
	}
	if strings.Contains(abs, "pbs.twimg.com/media/") {
		return RewritePBSOrig(abs), true
	}
	u, err := url.Parse(abs)
	if err != nil {
		return abs, false
	}
	escaped := u.EscapedPath()
	i := strings.Index(escaped, picPrefix)
	if i < 0 {
		return abs, false
	}
	decoded, err := url.PathUnescape(escaped[i+len(picPrefix):])
	if err != nil {
		return abs, false
	}
	if !strings.HasPrefix(decoded, mediaPathPrefix) || len(decoded) == len(mediaPathPrefix) {
		return abs, false
	}
	target := "https://pbs.twimg.com/media/" + decoded[len(mediaPathPrefix):]
	// A query that rode outside the encoded segment (/pic/media/F.jpg?name=small
	// form) belongs to the target; the fully encoded form already carries its
	// query inside the decoded path.
	if u.RawQuery != "" && !strings.Contains(target, "?") {
		target += "?" + u.RawQuery
	}
	return RewritePBSOrig(target), true
}
