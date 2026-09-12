// Package rss parses Nitter RSS feeds and projects their items into the sdk
// (package twitter) Tweet shape.
//
// Boundary: this is a protocol-internal detail of the Nitter fetch path —
// only sdk models appear in results. Parse consumes raw feed bytes (the HTTP
// transport lives in internal/nitter/protocol/httpx); ItemToTweet projects a
// single feed item onto twitter.Tweet under the models contract's
// no-fabrication rule: facts the feed does not carry (retweet markers, reply
// targets, quoted statuses, media dimensions) stay at their zero value, and
// an unparseable pubDate leaves PublishedAt zero rather than inventing a
// time.
//
// Extraction semantics port the user's reference Python plugin, which
// consumes real Nitter feeds daily: HTML bodies fold via
// internal/nitter/text (the clean_html_text port), description images count
// as media only on Nitter's author-media paths (/pic/media and
// /pic/*_video_thumb, including their percent-encoded forms), and
// pbs.twimg.com/media links are rewritten to their name=orig variant.
package rss

import (
	"bytes"
	"encoding/xml"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/mediaurl"
	"github.com/shitianyaa/twitter-cli/internal/nitter/text"
	"github.com/shitianyaa/twitter-cli/sdk"
)

const (
	opParse       = "rss.Parse"
	opItemToTweet = "rss.ItemToTweet"
)

// Item is one <item> of a Nitter RSS feed, carried as raw strings so the
// projection step (ItemToTweet) owns every interpretation decision.
type Item struct {
	// GUID is the item's <guid> text (Nitter emits the absolute status URL,
	// often with a "#m" fragment). May be empty on non-conforming feeds.
	GUID string
	// Link is the item's <link> text: the absolute status URL on the
	// instance host.
	Link string
	// Title is the item's <title> text (HTML-escaped tweet body).
	Title string
	// Description is the raw HTML of the tweet body (CDATA content).
	Description string
	// PubDate is the item's <pubDate> text (RFC 1123 with numeric zone).
	PubDate string
	// Creator is the item's <dc:creator> text, e.g. "@NASA".
	Creator string
	// MediaURLs lists the item's media sources: every media:content url
	// attribute followed by every author-media <img src> found in the
	// description HTML, in that order. Values are extracted as-is (HTML
	// entities unescaped, trimmed); joining against the instance base and
	// pbs.twimg.com quality rewriting happen in ItemToTweet.
	MediaURLs []string
}

// feedXML mirrors the tolerated feed shapes: a plain <rss><channel><item>
// document (Nitter's real form), plus direct root-level <item> children as a
// fallback for channel-less feeds. Struct tags name local names only:
// encoding/xml then matches dc:creator / media:content regardless of which
// namespace URI the instance declares (Nitter forks vary), matching the
// reference plugin's tolerance of real-world feeds.
type feedXML struct {
	Channel struct {
		Item []itemXML `xml:"item"`
	} `xml:"channel"`
	Item []itemXML `xml:"item"`
}

type itemXML struct {
	GUID        string     `xml:"guid"`
	Link        string     `xml:"link"`
	Title       string     `xml:"title"`
	Description string     `xml:"description"`
	PubDate     string     `xml:"pubDate"`
	Creator     string     `xml:"creator"`
	Media       []mediaXML `xml:"content"`
}

type mediaXML struct {
	URL string `xml:"url,attr"`
}

// statusRe extracts the account and status id from a Nitter status URL
// (guid or link). Nitter hosts and status paths are plain word characters,
// so a leftmost match always lands on the account segment.
var statusRe = regexp.MustCompile(`([A-Za-z0-9_]+)/status(?:es)?/(\d+)`)

// picMediaSrcRe marks author-media img sources inside description HTML:
// /pic/media/ (photos, also percent-encoded as /pic/media%2F...) and
// /pic/<name>_video_thumb/ (video posters). Port of the plugin's
// _MEDIA_SRC_RE; it keeps link-preview card images (/pic/card_img), emoji
// images (/pic/emoji) and other decoration out of the media list.
var picMediaSrcRe = regexp.MustCompile(`(?i)/pic/(?:media|[a-z0-9_]+_video_thumb)(?:/|%2f)`)

// Parse decodes a Nitter RSS document into its items. Any XML syntax error
// (truncated body, non-XML challenge page) classifies as KindMalformed; a
// well-formed document with zero items is not an error (empty feeds are the
// fetch layer's policy decision, matching the plugin's layered handling).
//
// A body carrying a DTD is rejected outright (before decoding — expansion
// happens inside the parser, so "parse and see" is too late): <!DOCTYPE
// enables entity definitions and with them billion-laughs expansion, which
// no legitimate Nitter feed ships.
func Parse(data []byte) ([]Item, error) {
	if bytes.Contains(data, []byte("<!DOCTYPE")) || bytes.Contains(data, []byte("<!ENTITY")) {
		// XML keywords are case-sensitive, so the exact uppercase tokens are
		// the whole story; a lowercase <!doctype is not valid XML anyway.
		return nil, twitter.Errorf(twitter.KindMalformed, opParse,
			"rejected doctype/entity — potential entity expansion")
	}
	var doc feedXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, twitter.Errorf(twitter.KindMalformed, opParse, "parse RSS XML: %w", err)
	}
	rawItems := doc.Channel.Item
	if len(rawItems) == 0 {
		rawItems = doc.Item
	}
	items := make([]Item, 0, len(rawItems))
	for _, xi := range rawItems {
		it := Item{
			GUID:        strings.TrimSpace(xi.GUID),
			Link:        strings.TrimSpace(xi.Link),
			Title:       strings.TrimSpace(xi.Title),
			Description: strings.TrimSpace(xi.Description),
			PubDate:     strings.TrimSpace(xi.PubDate),
			Creator:     strings.TrimSpace(xi.Creator),
		}
		for _, m := range xi.Media {
			if u := strings.TrimSpace(m.URL); u != "" {
				it.MediaURLs = append(it.MediaURLs, u)
			}
		}
		it.MediaURLs = append(it.MediaURLs, descriptionMedia(xi.Description)...)
		items = append(items, it)
	}
	return items, nil
}

// ItemToTweet projects one feed item onto the sdk Tweet shape.
//
//   - ID/URL: the regexp ([A-Za-z0-9_]+)/status(?:es)?/(\d+) runs on GUID
//     first, falling back to Link; no match → KindMalformed. The URL is
//     canonicalized to https://x.com/<user>/status/<id>.
//   - Text: CleanHTML(Description), falling back to CleanHTML(Title) when
//     the description is empty (plugin semantics: description or title).
//   - PublishedAt: time.Parse(time.RFC1123Z, PubDate), stored in UTC per the
//     models contract. An unparseable value leaves the zero time — errors
//     are reserved for structural breakage, not a missing fact.
//   - Author: Handle from dc:creator with its "@" stripped; Name and
//     AvatarURL stay empty (the feed does not carry them).
//   - Media: media:content and description img URLs; relative paths are
//     joined against the instance host derived from the matched status URL,
//     and pbs.twimg.com/media URLs are rewritten to name=orig (existing
//     name= replaced, otherwise appended) per the plugin's quality rule.
func ItemToTweet(it Item) (twitter.Tweet, error) {
	user, id, source, ok := locateStatus(it)
	if !ok {
		return twitter.Tweet{}, twitter.Errorf(twitter.KindMalformed, opItemToTweet,
			"item guid and link match no <account>/status/<id> URL")
	}
	tw := twitter.Tweet{
		ID:   id,
		URL:  "https://x.com/" + user + "/status/" + id,
		Text: projectText(it),
	}
	if h := strings.TrimPrefix(it.Creator, "@"); h != "" {
		tw.Author.Handle = h
	}
	if t, ok := parsePubDate(it.PubDate); ok {
		tw.PublishedAt = t.UTC()
	}
	tw.Media = projectMedia(it.MediaURLs, instanceBase(source))
	return tw, nil
}

// locateStatus finds the account/id pair on GUID first, then Link, and
// reports which source matched (the base for joining relative media URLs).
func locateStatus(it Item) (user, id, source string, ok bool) {
	for _, candidate := range []string{it.GUID, it.Link} {
		if m := statusRe.FindStringSubmatch(candidate); m != nil {
			return m[1], m[2], candidate, true
		}
	}
	return "", "", "", false
}

// pubDateDayRe matches a weekday-comma prefix followed by a single-digit day
// of month ("Sun, 5 Jul …"), the shape RFC1123Z's zero-padded "02" rejects.
var pubDateDayRe = regexp.MustCompile(`^([A-Za-z]{3},) (\d)(\s.+)$`)

// parsePubDate parses one pubDate with the tolerance real Nitter feeds need
// (Task 10 carry): whitespace runs are collapsed to single spaces (Nitter
// sometimes emits "Sun,  5 Jul …" with a doubled space after the comma), the
// result is tried against RFC1123Z, and a single-digit day of month is
// zero-padded for a second attempt (RFC1123Z's "02" requires two digits;
// Nitter emits "Sun, 5 Jul …" during the first nine days of a month). The
// brief's "try RFC1123 (named zone) as a second layout" degenerates here:
// Nitter always emits a numeric zone (+0000), so the only real-world gap is
// the day padding, and the retry stays on RFC1123Z. Unparseable input
// reports false — the caller leaves PublishedAt zero rather than fabricating.
func parsePubDate(raw string) (time.Time, bool) {
	s := strings.Join(strings.Fields(raw), " ")
	if t, err := time.Parse(time.RFC1123Z, s); err == nil {
		return t, true
	}
	if m := pubDateDayRe.FindStringSubmatch(s); m != nil {
		if t, err := time.Parse(time.RFC1123Z, m[1]+" 0"+m[2]+m[3]); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// projectText cleans the description, falling back to the title when the
// description is empty (plugin semantics: clean_text(description or title)).
func projectText(it Item) string {
	src := it.Description
	if src == "" {
		src = it.Title
	}
	return text.CleanHTML(src)
}

// projectMedia maps extracted URLs onto twitter.Media. Empty input leaves the
// slice nil (Task 8's pinned null contract). Dedup runs on the canonical URL
// string (ruling R13): an instance /pic/ proxy pointing at a pbs.twimg.com
// media path canonicalizes onto the direct pbs URL (quality rewritten to
// name=orig), so an item carrying the same photo both as a media:content URL
// and as a description img proxy yields ONE media entry. Proxy targets that
// are not pbs media paths (video thumbnails) keep their absolutized proxy
// form and dedup by string, as before. Nitter video thumbnails project as
// "video" placeholder links; everything else is an image.
func projectMedia(urls []string, base string) []twitter.Media {
	var media []twitter.Media
	seen := make(map[string]bool, len(urls))
	for _, raw := range urls {
		u, _ := mediaurl.NormalizePicProxy(base, raw)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		media = append(media, twitter.Media{Type: mediaType(u), URL: u})
	}
	return media
}

// instanceBase derives scheme://host from the item's matched status URL; ""
// when the URL carries no absolute form.
func instanceBase(source string) string {
	u, err := url.Parse(source)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// mediaType classifies a media URL: video thumbnails are the video
// placeholder links of the models contract, everything else is an image.
func mediaType(u string) string {
	if strings.Contains(u, "_video_thumb") {
		return "video"
	}
	return "image"
}

// descriptionMedia extracts author-media <img src> values from the raw
// description HTML in document order: HTML entities in the value are
// unescaped, and only /pic/media and /pic/*_video_thumb sources are kept
// (the plugin's author-media rule). Quote containers and article-card links
// are masked (the plugin's nested-media rule, shared with the HTML parser):
// images inside a subtree rooted at a tag whose class carries a "quote"/
// "quote-…" token or whose href points at /i/article belong to the quoted
// tweet, never to the quoting item.
func descriptionMedia(desc string) []string {
	if desc == "" {
		return nil
	}
	var urls []string
	skipName := "" // tag name of the masked subtree root; "" when not masked
	depth := 0
	for _, loc := range tagRe.FindAllStringSubmatchIndex(desc, -1) {
		tag := desc[loc[0]:loc[1]]
		name := strings.ToLower(desc[loc[4]:loc[5]])
		closing := desc[loc[2]:loc[3]] == "/"
		selfClosed := strings.HasSuffix(tag, "/>")
		if skipName != "" {
			// Inside a masked subtree: track the root tag's nesting depth and
			// drop everything until it closes. Void and self-closed tags
			// never change the depth.
			if name != skipName || isVoidTag(name) || selfClosed {
				continue
			}
			if closing {
				depth--
				if depth == 0 {
					skipName = ""
				}
			} else {
				depth++
			}
			continue
		}
		switch {
		case name == "img" && !closing:
			if v, ok := imgSrcValue(tag); ok {
				v = strings.TrimSpace(html.UnescapeString(v))
				if v != "" && picMediaSrcRe.MatchString(v) {
					urls = append(urls, v)
				}
			}
		case !closing && !selfClosed && !isVoidTag(name) && opensMaskedSubtree(tag):
			skipName = name
			depth = 1
		}
	}
	return urls
}

// tagRe iterates the tags of a machine-generated Nitter HTML fragment: group
// 1 is the optional closing slash, group 2 the tag name. The [^>]* attribute
// soup relies on Nitter never emitting a raw ">" inside attribute values.
var tagRe = regexp.MustCompile(`(?i)<(/?)([a-zA-Z][a-zA-Z0-9]*)\b[^>]*>`)

// voidTags are the HTML void elements: they have no closing tag and never
// open a subtree the mask could track.
var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"source": true, "track": true, "wbr": true,
}

func isVoidTag(name string) bool { return voidTags[name] }

// opensMaskedSubtree reports whether an opening tag starts a masked subtree:
// its class attribute carries a "quote"/"quote-…" token (the HTML parser's
// hasQuoteClass port) or its href points at an article card (/i/article).
func opensMaskedSubtree(tag string) bool {
	if m := tagAttrRe("class").FindStringSubmatch(tag); m != nil {
		for _, name := range strings.Fields(m[1] + m[2] + m[3]) {
			n := strings.ToLower(name)
			if n == "quote" || strings.HasPrefix(n, "quote-") {
				return true
			}
		}
	}
	if m := tagAttrRe("href").FindStringSubmatch(tag); m != nil {
		if articleCardRe.MatchString(m[1] + m[2] + m[3]) {
			return true
		}
	}
	return false
}

// tagAttrRe builds a matcher for one attribute's value: double-quoted,
// single-quoted or unquoted. The value lands in exactly one of the three
// capture groups (1, 2, 3); the others stay empty.
func tagAttrRe(attr string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b` + attr + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
}

// articleCardRe detects Nitter article-card links (/i/article/…), mirroring
// the HTML parser's articleHrefRe.
var articleCardRe = regexp.MustCompile(`(?i)(?:^|/)/?i/article(?:/|[?#]|$)`)

// imgSrcValue extracts the src attribute of one <img> tag text: double-
// quoted, single-quoted or unquoted. The [\s"'] guard keeps other attributes
// (data-src, srcset) from matching as src.
var imgSrcTagRe = regexp.MustCompile(`(?i)[\s"']src\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

func imgSrcValue(tag string) (string, bool) {
	m := imgSrcTagRe.FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	v := m[1]
	if v == "" {
		v = m[2]
	}
	if v == "" {
		v = m[3]
	}
	return v, v != ""
}
