// Package html parses Nitter HTML timelines (user pages, search pages and
// lists share the same markup) and projects their items into the sdk
// (package twitter) Tweet shape.
//
// Boundary: this is a protocol-internal detail of the Nitter fetch path —
// only sdk models appear in results. ParseTimeline consumes raw page bytes
// (the HTTP transport lives in internal/nitter/protocol/httpx) and never
// fabricates data a page does not carry: an unparseable tweet date leaves
// PublishedAt zero, an unidentifiable item is skipped, and quoted tweets are
// masked out of their quoting item rather than becoming separate Tweets.
//
// Extraction semantics port the user's reference Python plugin
// (astrbot_plugin_nitter_tweets, media_support/html_backend/parser.py), which
// runs against real Nitter instances daily. HTML folding goes through
// internal/nitter/text (the clean_html_text port) and URL normalization
// through internal/nitter/mediaurl (abs_url and prefer_pbs_quality ports).
package html

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"github.com/shitianyaa/twitter-cli/internal/nitter/mediaurl"
	"github.com/shitianyaa/twitter-cli/internal/nitter/text"
	"github.com/shitianyaa/twitter-cli/sdk"
)

const (
	opParseTimeline = "html.ParseTimeline"
	opClassifyPage  = "html.ClassifyPage"
)

// Page is one parsed timeline: the projected tweets plus the load-more
// cursor for the next page.
type Page struct {
	Tweets     []twitter.Tweet
	NextCursor string
}

// tweetLinkRe locates user and status id in a Nitter status href
// (/account/status/…), anchored at the start of the attribute value — the
// plugin's tweet-link pattern. Nitter status paths are plain word characters,
// so a leftmost match lands on the author segment.
var tweetLinkRe = regexp.MustCompile(`^/([A-Za-z0-9_]+)/status(?:es)?/(\d+)`)

// articleHrefRe detects Nitter article-card links (/i/article/…), whose
// subtrees are masked like quotes (plugin's nested-media rule).
var articleHrefRe = regexp.MustCompile(`(?i)(?:^|/)/?i/article(?:/|[?#]|$)`)

// controlRe is the plugin's unsafe-URL character gate: control characters,
// spaces and backslashes never appear in legitimate media URLs.
var controlRe = regexp.MustCompile(`[\x00-\x20\x7f\\]`)

// tweetDateLayout parses a Nitter tweet-date title such as
// "Jul 27, 2026 · 9:09 AM UTC". The MST token matches the trailing zone name
// literally; Nitter always emits UTC, so Go resolves it to the UTC location.
const tweetDateLayout = "Jan 2, 2006 · 3:04 PM MST"

// ParseTimeline parses any page carrying div.timeline-item chunks (user
// timeline, search results, list) into a Page. It is the goquery port of the
// plugin's parse_timeline_html:
//
//   - Items are the div.timeline-item elements; user and status id come from
//     the first a.tweet-link (fallback: first anchor) whose href matches
//     tweetLinkRe. Items without such a link are skipped — identity is never
//     fabricated.
//   - Text, media, date and retweet detection all run on a clone of the item
//     whose quote and article-card subtrees have been removed, so a quoted
//     tweet's text and media never leak into the quoting item (and quoted
//     content never becomes a Tweet of its own).
//   - Text is the first div.tweet-content of the masked clone, folded through
//     text.CleanHTML; a missing tweet-content leaves the text empty.
//   - Media follows the plugin's pass order and seen-set (see itemMedia).
//   - PublishedAt parses the span.tweet-date title with tweetDateLayout into
//     UTC; unparseable or missing dates leave the zero time.
//
// The returned error is reserved for structural breakage the fetch layer must
// surface; goquery repairs any malformed markup, so garbage bytes degrade to
// an empty Page with a nil error. Challenge/error/empty classification of a
// page is ClassifyPage's job — callers apply it before parsing.
func ParseTimeline(body []byte, instance string) (Page, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		// Unreachable for in-memory bytes (the HTML parser is error-tolerant
		// and a bytes.Reader cannot fail); kept explicit over silent swallowing.
		return Page{}, twitter.Errorf(twitter.KindMalformed, opParseTimeline, "decode page: %w", err)
	}
	var page Page
	seen := make(map[string]bool)
	doc.Find("div.timeline-item").EachWithBreak(func(_ int, item *goquery.Selection) bool {
		clone := maskQuoteSubtrees(item)
		user, id, ok := locateItem(clone)
		if !ok {
			return true
		}
		key := user + ":" + id
		if seen[key] {
			return true
		}
		seen[key] = true
		page.Tweets = append(page.Tweets, buildTweet(clone, user, id, instance))
		return true
	})
	page.NextCursor = extractCursor(doc)
	return page, nil
}

// ClassifyPage distinguishes Nitter's own login and maintenance pages from
// its structured error panels and from any other response:
//
//   - an element with class "error-panel" (Nitter's own error container —
//     structural markup that cannot occur inside tweet bodies) reports
//     KindUnavailable with the token-stable message "error panel";
//   - a login form (form action containing /login), a .login-container or a
//     .maintenance marker reports KindChallenge;
//   - anything else — including an empty timeline and unparseable bytes — is
//     nil: emptiness is the fetch layer's policy decision, not a parse error.
//
// Messages are static tokens; page content is never echoed into the error
// chain (sdk redaction contract). This follows the plugin's structural-marker
// approach in html_backend/modes.py while dropping its <title> phrase
// matching: generic English phrases in titles were the plugin's own
// documented source of false rotations.
func ClassifyPage(body []byte) error {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return twitter.Errorf(twitter.KindMalformed, opClassifyPage, "decode page: %w", err)
	}
	if doc.Find(".error-panel").Length() > 0 {
		return twitter.Errorf(twitter.KindUnavailable, opClassifyPage, "error panel")
	}
	if doc.Find(`form[action*="/login"], .login-container, .maintenance`).Length() > 0 {
		return twitter.Errorf(twitter.KindChallenge, opClassifyPage, "login or maintenance page")
	}
	return nil
}

// buildTweet projects one identified timeline item onto the sdk Tweet shape.
// Everything runs on the quote-masked clone.
func buildTweet(clone *goquery.Selection, user, id, instance string) twitter.Tweet {
	tw := twitter.Tweet{
		ID:   id,
		URL:  "https://x.com/" + user + "/status/" + id,
		Text: itemText(clone),
	}
	tw.Author.Handle = user
	tw.PublishedAt = itemDate(clone)
	tw.Media = itemMedia(clone, instance)
	tw.IsRetweet, tw.RepostedBy = retweetInfo(clone)
	return tw
}

// locateItem finds the item's user/id pair: the first a.tweet-link whose href
// matches tweetLinkRe, falling back to the first matching anchor of any kind
// (plugin semantics: leftmost status href wins — in Nitter markup that is the
// item's own tweet-link, quote links come later).
func locateItem(clone *goquery.Selection) (user, id string, ok bool) {
	scan := func(sel string) bool {
		found := false
		clone.Find(sel).EachWithBreak(func(_ int, s *goquery.Selection) bool {
			href, exists := s.Attr("href")
			if !exists {
				return true
			}
			if m := tweetLinkRe.FindStringSubmatch(href); m != nil {
				user, id, found = m[1], m[2], true
				return false
			}
			return true
		})
		return found
	}
	if scan("a.tweet-link[href]") {
		return user, id, true
	}
	if scan("a[href]") {
		return user, id, true
	}
	return "", "", false
}

// maskQuoteSubtrees deep-copies the item and removes quote and article-card
// subtrees, porting the plugin's _is_nested_media_section_start rule: an
// element whose class carries a "quote" token or a "quote-…" token, and an
// element whose href points at /i/article, opens a masked subtree. Quoted
// tweets' text and media must never leak into the quoting item. The original
// selection is untouched.
func maskQuoteSubtrees(item *goquery.Selection) *goquery.Selection {
	clone := item.Clone()
	var roots []*goquery.Selection
	clone.Find("*").Each(func(_ int, s *goquery.Selection) {
		if cls, exists := s.Attr("class"); exists && hasQuoteClass(cls) {
			roots = append(roots, s)
			return
		}
		if href, exists := s.Attr("href"); exists && articleHrefRe.MatchString(href) {
			roots = append(roots, s)
		}
	})
	for _, root := range roots {
		root.Remove()
	}
	return clone
}

// hasQuoteClass ports the plugin's class rule: any whitespace-separated
// class token equal to "quote" or starting with "quote-" (case-insensitive)
// marks a quote container.
func hasQuoteClass(classAttr string) bool {
	for _, name := range strings.Fields(classAttr) {
		name = strings.ToLower(name)
		if name == "quote" || strings.HasPrefix(name, "quote-") {
			return true
		}
	}
	return false
}

// itemText extracts the author's tweet body: the first div.tweet-content of
// the masked clone, serialized as outer HTML and folded through the shared
// text.CleanHTML port. A missing tweet-content leaves the text empty (zero
// value — no placeholder text is fabricated).
func itemText(clone *goquery.Selection) string {
	content := clone.Find("div.tweet-content").First()
	if content.Length() == 0 {
		return ""
	}
	outer, err := goquery.OuterHtml(content)
	if err != nil {
		// Only reachable for an empty selection; guarded above.
		return ""
	}
	return text.CleanHTML(outer)
}

// itemDate parses the span.tweet-date anchor title ("Jul 27, 2026 · 9:09 AM
// UTC") into UTC. Unparseable or missing dates leave the zero time — errors
// are reserved for structural breakage, not a missing fact. Attribute values
// arrive entity-decoded from the HTML parser, so no extra unquoting applies.
func itemDate(clone *goquery.Selection) time.Time {
	title, exists := clone.Find(".tweet-date a[title]").First().Attr("title")
	if !exists {
		return time.Time{}
	}
	t, err := time.Parse(tweetDateLayout, title)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// repostMarkers are the retweet-header text markers real instances emit
// after the reposter's display name (2026-09 real-instance smoke round):
// the English stock form and the two known Chinese localized forms. The
// name is the header text before the earliest marker found.
var repostMarkers = []string{" retweeted", "转推了", "转推自"}

// repostNameTrimcut strips whitespace and stray separators from the extracted
// display name (real headers render as e.g. "NASA retweeted", with the icon
// leaving leading space, and localized variants may leave "; " before the
// marker).
const repostNameTrimcut = " \t\r\n;；"

// retweetInfo detects a pure retweet and its reposter. Port of the plugin's
// is_pure_retweet_chunk: Nitter renders the retweet-header inside tweet-body
// before tweet-content, so the rule is a .retweet-header element appearing
// before the item's first .tweet-content in document order (the plugin cuts
// the chunk at tweet-content and searches only the head; the DOM order
// comparison replaces its 2500-byte head window). RepostedBy is the header's
// DISPLAY NAME, extracted from its text content via repostedByName — stock
// Nitter renders it as plain text ("… icon … NASA retweeted"), not an anchor.
func retweetInfo(clone *goquery.Selection) (isRetweet bool, repostedBy string) {
	header := clone.Find(".retweet-header").First()
	if header.Length() == 0 {
		return false, ""
	}
	content := clone.Find(".tweet-content").First()
	if content.Length() > 0 {
		root := clone.Nodes[0]
		hi, hok := docIndex(root, header.Nodes[0])
		ci, cok := docIndex(root, content.Nodes[0])
		if !hok || !cok || hi > ci {
			return false, ""
		}
	}
	return true, repostedByName(header.Text())
}

// repostedByName extracts the reposter's display name from the retweet
// header's text: the portion before the earliest known marker (" retweeted",
// "转推了", "转推自"), trimmed of whitespace and stray separators. Text
// matching no marker yields "" — the name is never fabricated from an
// unknown header shape.
func repostedByName(headerText string) string {
	s := strings.TrimSpace(headerText)
	cut := -1
	for _, marker := range repostMarkers {
		if i := strings.Index(s, marker); i >= 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	if cut < 0 {
		return ""
	}
	return strings.Trim(s[:cut], repostNameTrimcut)
}

// docIndex reports the pre-order index of target within the subtree rooted at
// root, for document-order comparisons between two elements of one item.
func docIndex(root, target *html.Node) (int, bool) {
	idx := 0
	var walk func(n *html.Node) bool
	walk = func(n *html.Node) bool {
		if n == target {
			return true
		}
		idx++
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	if walk(root) {
		return idx, true
	}
	return 0, false
}

// itemMedia extracts the author's media from the masked clone, porting the
// plugin's pass order over _extract_media:
//
//  1. a.still-image[href] thumbnails;
//  2. href/src values starting with /pic/orig/media (original-quality proxy);
//  3. href/src values starting with /pic/media, only when passes 1–2 found no
//     image at all (the plugin's gate: an orig-form link suppresses the
//     generic fallback for the whole item);
//  4. https://pbs.twimg.com/media/ hrefs inside the first attachments wrapper
//     (the plugin's class="attachments window, taken as a DOM subtree);
//  5. href/src values starting with /video/ or https://video.twimg.com/ (the
//     video placeholder links of the models contract).
//
// Every accepted URL is absolutized against the instance base, gated by
// safeMediaURL, stripped of profile images/banners (all kinds) and of video
// thumbnails/emoji (images only), and pbs.twimg.com/media links are rewritten
// to name=orig. Dedup runs on the final URL string, the plugin's seen-set.
// Nothing found leaves the slice nil (the models' null contract).
func itemMedia(clone *goquery.Selection, instance string) []twitter.Media {
	acc := &mediaAccum{seen: map[string]bool{}}
	clone.Find("a.still-image[href]").Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok {
			acc.add("image", href, instance)
		}
	})
	scanAttrPrefixed(clone, "/pic/orig/media", "image", acc, instance)
	if !acc.hasImage() {
		scanAttrPrefixed(clone, "/pic/media", "image", acc, instance)
	}
	if wrapper := firstAttachments(clone); wrapper != nil {
		wrapper.Find(`a[href^="https://pbs.twimg.com/media/"]`).Each(func(_ int, s *goquery.Selection) {
			if href, ok := s.Attr("href"); ok {
				acc.add("image", href, instance)
			}
		})
	}
	scanAttrPrefixed(clone, "/video/", "video", acc, instance)
	scanAttrPrefixed(clone, "https://video.twimg.com/", "video", acc, instance)
	return acc.media
}

// scanAttrPrefixed walks the scope in document order and feeds every href or
// src attribute value that starts with prefix to add — the DOM equivalent of
// the plugin's per-pass (?:href|src)="…" regexes.
func scanAttrPrefixed(scope *goquery.Selection, prefix, kind string, acc *mediaAccum, instance string) {
	scope.Find("[href], [src]").Each(func(_ int, s *goquery.Selection) {
		for _, attr := range []string{"href", "src"} {
			if v, ok := s.Attr(attr); ok && strings.HasPrefix(v, prefix) {
				acc.add(kind, v, instance)
			}
		}
	})
}

// firstAttachments finds the first element whose class tokens start with
// "attachments" (the plugin's class="attachments substring match), or nil.
func firstAttachments(clone *goquery.Selection) *goquery.Selection {
	var found *goquery.Selection
	clone.Find("[class]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		cls, _ := s.Attr("class")
		for _, name := range strings.Fields(cls) {
			if strings.HasPrefix(name, "attachments") {
				found = s
				return false
			}
		}
		return true
	})
	return found
}

// mediaAccum accumulates accepted media with the plugin's seen-set semantics:
// membership is keyed by the final (absolutized, rewritten) URL string.
type mediaAccum struct {
	media []twitter.Media
	seen  map[string]bool
}

// hasImage reports whether an image-kind entry was already accepted (the
// plugin's fallback gate for the generic /pic/media pass).
func (a *mediaAccum) hasImage() bool {
	for _, m := range a.media {
		if m.Type == "image" {
			return true
		}
	}
	return false
}

// add gates and stores one candidate media URL, porting the plugin's add():
// absolutize, safety and exclusion gates, then the pbs quality rewrite for
// images, then seen-set membership on the final URL.
func (a *mediaAccum) add(kind, raw, instance string) {
	u := mediaurl.Absolutize(instance, raw)
	if u == "" || a.seen[u] || !safeMediaURL(u) {
		return
	}
	if strings.Contains(u, "profile_images") || strings.Contains(u, "profile_banners") {
		return
	}
	if kind == "image" && (strings.Contains(u, "video_thumb") || strings.Contains(u, "emoji")) {
		return
	}
	if kind == "image" {
		u = mediaurl.RewritePBSOrig(u)
	}
	if a.seen[u] {
		// Two different input forms can rewrite onto the same final URL;
		// dedup is defined on the final URL string.
		return
	}
	a.seen[u] = true
	a.media = append(a.media, twitter.Media{Type: kind, URL: u})
}

// safeMediaURL ports the plugin's is_safe_http_url gate: only absolute
// http(s) URLs with a host, no userinfo, no control characters/spaces and a
// sane port are accepted. Media values are absolutized before this check, so
// relative /pic paths arrive with the instance host attached; a relative
// value left without a usable base is rejected rather than emitted.
func safeMediaURL(u string) bool {
	if controlRe.MatchString(u) {
		return false
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if scheme := parsed.Scheme; scheme != "http" && scheme != "https" {
		return false
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if p := parsed.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return false
		}
	}
	return true
}

// extractCursor returns the load-more cursor: the URL-decoded cursor query
// parameter of the LAST show-more anchor carrying one in document order.
// Nitter renders a "Load newest" link above the timeline and a "Load more"
// link below; the older link is the paging cursor, so when both exist the
// last one wins, and anchors without a cursor parameter never qualify. The
// port of the plugin's extract_next_cursor keeps its unquote step, expressed
// as url.ParseQuery on the anchor's query string.
func extractCursor(doc *goquery.Document) string {
	cursor := ""
	doc.Find(".show-more a[href], a.show-more[href]").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		if v := cursorParam(href); v != "" {
			cursor = v
		}
	})
	return cursor
}

// cursorParam extracts the cursor query parameter of one anchor href, or ""
// when it carries none (or an empty one — the plugin's pattern requires at
// least one value character). Parse errors on malformed escapes leave the
// successfully parsed pairs in place: extraction is best-effort.
func cursorParam(href string) string {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	q, _ := url.ParseQuery(u.RawQuery)
	return q.Get("cursor")
}
