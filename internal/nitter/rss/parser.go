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
	"encoding/xml"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

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

// imgSrcRe captures the src attribute of <img> tags in untrusted HTML:
// double-quoted, single-quoted, or unquoted values. The [\s"'] guard keeps
// other attributes (data-src, srcset) from matching as src.
var imgSrcRe = regexp.MustCompile(`(?i)<img\b[^>]*?[\s"']src\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

// pbsNameRe rewrites an existing name= query parameter on pbs media URLs.
var pbsNameRe = regexp.MustCompile(`([?&])name=[^&]*`)

// Parse decodes a Nitter RSS document into its items. Any XML syntax error
// (truncated body, non-XML challenge page) classifies as KindMalformed; a
// well-formed document with zero items is not an error (empty feeds are the
// fetch layer's policy decision, matching the plugin's layered handling).
func Parse(data []byte) ([]Item, error) {
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
	if t, err := time.Parse(time.RFC1123Z, it.PubDate); err == nil {
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
// slice nil (Task 8's pinned null contract); exact duplicates are dropped,
// porting the plugin's seen-set. An unmatched media source type stays the
// "image" default; Nitter video thumbnails (_video_thumb paths) project as
// "video" placeholder links.
func projectMedia(urls []string, base string) []twitter.Media {
	var media []twitter.Media
	seen := make(map[string]bool, len(urls))
	for _, raw := range urls {
		u := rewritePBSOrig(absolutize(raw, base))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		media = append(media, twitter.Media{Type: mediaType(u), URL: u})
	}
	return media
}

// absolutize turns Nitter's media path forms into absolute URLs: protocol-
// relative values get https:, instance-relative values are joined against
// the base derived from the item's own status URL. Anything else passes
// through unchanged — relative input without a usable base is left as-is
// rather than silently dropped (no silent degradation).
func absolutize(raw, base string) string {
	switch {
	case strings.HasPrefix(raw, "//"):
		return "https:" + raw
	case strings.HasPrefix(raw, "/"):
		if base != "" {
			return base + raw
		}
		return raw
	default:
		return raw
	}
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

// rewritePBSOrig ports the plugin's prefer_pbs_quality at the "high" tier:
// pbs.twimg.com/media URLs get their name= parameter replaced with orig, or
// appended when absent; non-pbs URLs pass through unchanged.
func rewritePBSOrig(u string) string {
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

// mediaType classifies a media URL: video thumbnails are the video
// placeholder links of the models contract, everything else is an image.
func mediaType(u string) string {
	if strings.Contains(u, "_video_thumb") {
		return "video"
	}
	return "image"
}

// descriptionMedia extracts author-media <img src> values from the raw
// description HTML: all img tags are scanned, HTML entities in the value are
// unescaped, and only /pic/media and /pic/*_video_thumb sources are kept
// (plugin's author-media rule).
func descriptionMedia(desc string) []string {
	if desc == "" {
		return nil
	}
	var urls []string
	for _, m := range imgSrcRe.FindAllStringSubmatch(desc, -1) {
		v := m[1]
		if v == "" {
			v = m[2]
		}
		if v == "" {
			v = m[3]
		}
		v = strings.TrimSpace(html.UnescapeString(v))
		if v == "" || !picMediaSrcRe.MatchString(v) {
			continue
		}
		urls = append(urls, v)
	}
	return urls
}
