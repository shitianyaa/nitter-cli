package rss_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/rss"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// The fixture is shaped after the documented Nitter RSS format and the
// reference plugin's parser semantics. The plan's curl comparison against a
// real instance cannot run in the offline sandbox; it is carried as a
// user-run smoke step in Task 13 (ruling R12), so field mapping may be
// corrected there under the normal TDD loop.

// truncatedFeed is a response cut off mid-item: valid XML start, no closing
// element. Parse must classify it as KindMalformed.
const truncatedFeed = `<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel><item><title>truncated`

func loadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "user_rss.xml"))
	if err != nil {
		t.Fatalf("read testdata/user_rss.xml: %v", err)
	}
	return b
}

func parseFixture(t *testing.T) []rss.Item {
	t.Helper()
	items, err := rss.Parse(loadFixture(t))
	if err != nil {
		t.Fatalf("Parse(fixture) = error %v", err)
	}
	return items
}

func TestParseExtractsFeedItems(t *testing.T) {
	items := parseFixture(t)
	if len(items) != 4 {
		t.Fatalf("Parse(fixture) = %d items, want 4", len(items))
	}

	first := items[0]
	if first.GUID != "https://nitter.example/NASA/status/2081668333762687236#m" {
		t.Errorf("item 0 GUID = %q", first.GUID)
	}
	if first.Link != first.GUID {
		t.Errorf("item 0 Link = %q, want the guid value", first.Link)
	}
	if first.Creator != "@NASA" {
		t.Errorf("item 0 Creator = %q, want %q", first.Creator, "@NASA")
	}
	if first.PubDate != "Mon, 27 Jul 2026 09:09:40 +0000" {
		t.Errorf("item 0 PubDate = %q", first.PubDate)
	}
	wantTitle := "Engine firing complete & all systems nominal: next launch window opens Monday"
	if first.Title != wantTitle {
		t.Errorf("item 0 Title = %q, want %q", first.Title, wantTitle)
	}
	if want := `<div class="tweet-content media-body" dir="auto">Engine firing complete &amp; all systems nominal.<br/>`; !startsWith(first.Description, want) {
		t.Errorf("item 0 Description = %q, want CDATA HTML starting with %q", first.Description, want)
	}

	// MediaURLs: media:content url first, then description img sources —
	// here one percent-encoded relative /pic/media src.
	wantMedia := []string{
		"https://pbs.twimg.com/media/Fxxx1.jpg?name=small",
		"/pic/media%2FFxxx1.jpg%3Fname%3Dsmall",
	}
	if !reflect.DeepEqual(first.MediaURLs, wantMedia) {
		t.Errorf("item 0 MediaURLs = %#v, want %#v", first.MediaURLs, wantMedia)
	}

	if items[1].Creator != "@esa" {
		t.Errorf("item 1 Creator = %q, want %q", items[1].Creator, "@esa")
	}
	if len(items[1].MediaURLs) != 0 {
		t.Errorf("item 1 MediaURLs = %#v, want none (text-only item)", items[1].MediaURLs)
	}

	// The video-thumb src inside the description counts as media; the
	// /pic/<name>_video_thumb path form comes straight from real Nitter.
	wantVideo := []string{"/pic/ext_tw_video_thumb%2F2081668333762687238%2Fpu%2Fimg%2Fabc123.jpg"}
	if !reflect.DeepEqual(items[2].MediaURLs, wantVideo) {
		t.Errorf("item 2 MediaURLs = %#v, want %#v", items[2].MediaURLs, wantVideo)
	}

	// The GUID-less item carries no guid value; projection must rely on
	// <link> below.
	if items[3].GUID != "" {
		t.Errorf("item 3 GUID = %q, want empty", items[3].GUID)
	}
	if items[3].Link != "https://nitter.example/esa/status/2081668333762687239#m" {
		t.Errorf("item 3 Link = %q", items[3].Link)
	}
}

func TestParseMalformedXMLIsKindMalformed(t *testing.T) {
	items, err := rss.Parse([]byte(truncatedFeed))
	if err == nil {
		t.Fatalf("Parse(truncated) = items %#v, want an error", items)
	}
	if items != nil {
		t.Errorf("Parse(truncated) items = %#v, want nil on error", items)
	}
	var terr *twitter.Error
	if !errors.As(err, &terr) {
		t.Fatalf("Parse(truncated) error = %T (%v), want *twitter.Error", err, err)
	}
	if terr.Kind != twitter.KindMalformed {
		t.Errorf("Kind = %q, want %q", terr.Kind, twitter.KindMalformed)
	}
	if terr.Op != "rss.Parse" {
		t.Errorf("Op = %q, want %q", terr.Op, "rss.Parse")
	}
}

func TestItemToTweetProjectsPhotoItem(t *testing.T) {
	items := parseFixture(t)
	tw, err := rss.ItemToTweet(items[0])
	if err != nil {
		t.Fatalf("ItemToTweet(item 0) = error %v", err)
	}

	if tw.ID != "2081668333762687236" {
		t.Errorf("ID = %q", tw.ID)
	}
	if want := "https://x.com/NASA/status/2081668333762687236"; tw.URL != want {
		t.Errorf("URL = %q, want %q", tw.URL, want)
	}
	// HTML folded: <br/> became a newline, tags are stripped, the &amp;
	// entity is unescaped, and the result is trimmed.
	if want := "Engine firing complete & all systems nominal.\nread more"; tw.Text != want {
		t.Errorf("Text = %q, want %q", tw.Text, want)
	}
	if tw.Author.Handle != "NASA" {
		t.Errorf("Author.Handle = %q, want %q (dc:creator @-stripped)", tw.Author.Handle, "NASA")
	}
	if !tw.PublishedAt.Equal(time.Date(2026, 7, 27, 9, 9, 40, 0, time.UTC)) {
		t.Errorf("PublishedAt = %v, want 2026-07-27T09:09:40Z", tw.PublishedAt)
	}
	if tw.PublishedAt.Location() != time.UTC {
		t.Errorf("PublishedAt location = %v, want UTC (producers store UTC)", tw.PublishedAt.Location())
	}

	// R13 money shot: the item carries the same photo twice — a direct
	// pbs.twimg.com media:content URL and the percent-encoded /pic/ proxy
	// of the description img. Both canonicalize to the pbs name=orig URL,
	// so the projection yields exactly ONE media entry.
	wantMedia := []twitter.Media{
		{Type: "image", URL: "https://pbs.twimg.com/media/Fxxx1.jpg?name=orig"},
	}
	if !reflect.DeepEqual(tw.Media, wantMedia) {
		t.Errorf("Media = %#v, want %#v", tw.Media, wantMedia)
	}

	// The feed carries no retweet/reply/quote facts: they must stay at zero
	// (models contract: never fabricate).
	if tw.IsRetweet || tw.RepostedBy != "" || tw.ReplyTo != "" || tw.Quote != nil {
		t.Errorf("retweet/reply/quote fields = %+v, want zero values", tw)
	}
}

func TestItemToTweetJoinsRelativeVideoThumbMedia(t *testing.T) {
	items := parseFixture(t)
	tw, err := rss.ItemToTweet(items[2])
	if err != nil {
		t.Fatalf("ItemToTweet(item 2) = error %v", err)
	}
	if tw.ID != "2081668333762687238" {
		t.Errorf("ID = %q", tw.ID)
	}
	// The relative /pic/..._video_thumb src is joined against the instance
	// base derived from the item's own status URL, and a video-thumb URL is
	// a video placeholder, not an image.
	wantMedia := []twitter.Media{
		{Type: "video", URL: "https://nitter.example/pic/ext_tw_video_thumb%2F2081668333762687238%2Fpu%2Fimg%2Fabc123.jpg"},
	}
	if !reflect.DeepEqual(tw.Media, wantMedia) {
		t.Errorf("Media = %#v, want %#v", tw.Media, wantMedia)
	}
}

func TestItemToTweetTextOnlyLeavesMediaNil(t *testing.T) {
	items := parseFixture(t)
	tw, err := rss.ItemToTweet(items[1])
	if err != nil {
		t.Fatalf("ItemToTweet(item 1) = error %v", err)
	}
	if tw.ID != "2081668333762687237" {
		t.Errorf("ID = %q", tw.ID)
	}
	// Task 8's pinned null contract: no media → the Media slice stays nil.
	if tw.Media != nil {
		t.Errorf("Media = %#v, want nil", tw.Media)
	}
	if want := "Plain text only status with no attachments at all."; tw.Text != want {
		t.Errorf("Text = %q, want %q", tw.Text, want)
	}
}

func TestItemToTweetGUIDLessFallsBackToLink(t *testing.T) {
	items := parseFixture(t)
	tw, err := rss.ItemToTweet(items[3])
	if err != nil {
		t.Fatalf("ItemToTweet(item 3) = error %v", err)
	}
	if tw.ID != "2081668333762687239" {
		t.Errorf("ID = %q, want the id taken from Link", tw.ID)
	}
	if want := "https://x.com/esa/status/2081668333762687239"; tw.URL != want {
		t.Errorf("URL = %q, want %q", tw.URL, want)
	}
	if tw.Author.Handle != "esa" {
		t.Errorf("Author.Handle = %q, want %q", tw.Author.Handle, "esa")
	}
}

func TestItemToTweetNonStatusURLIsKindMalformed(t *testing.T) {
	it := rss.Item{
		GUID: "https://nitter.example/NASA/home",
		Link: "https://nitter.example/NASA",
	}
	tw, err := rss.ItemToTweet(it)
	if err == nil {
		t.Fatalf("ItemToTweet(non-status) = %+v, want an error", tw)
	}
	var terr *twitter.Error
	if !errors.As(err, &terr) {
		t.Fatalf("error = %T (%v), want *twitter.Error", err, err)
	}
	if terr.Kind != twitter.KindMalformed {
		t.Errorf("Kind = %q, want %q", terr.Kind, twitter.KindMalformed)
	}
	if terr.Op != "rss.ItemToTweet" {
		t.Errorf("Op = %q, want %q", terr.Op, "rss.ItemToTweet")
	}
}

func TestItemToTweetTitleFallbackAndUnparseableDate(t *testing.T) {
	it := rss.Item{
		GUID:    "https://nitter.example/NASA/status/42#m",
		Link:    "https://nitter.example/NASA/status/42#m",
		Title:   "plain <b>title &amp; <br/>more</b>",
		PubDate: "not a date",
	}
	tw, err := rss.ItemToTweet(it)
	if err != nil {
		t.Fatalf("ItemToTweet(title-only) = error %v, want nil (bad pubDate is not fatal)", err)
	}
	// Description empty → fall back to the cleaned title.
	if want := "plain title &\nmore"; tw.Text != want {
		t.Errorf("Text = %q, want %q", tw.Text, want)
	}
	if !tw.PublishedAt.IsZero() {
		t.Errorf("PublishedAt = %v, want the zero time (never fabricate)", tw.PublishedAt)
	}
	if tw.Media != nil {
		t.Errorf("Media = %#v, want nil", tw.Media)
	}
}

// TestParseRejectsDoctypeEntity pins the fetch-layer hardening carried from
// Task 10: an RSS body carrying a DTD (and with it, entity definitions) is
// rejected before XML decoding — billion-laughs expansion must never reach
// encoding/xml.
func TestParseRejectsDoctypeEntity(t *testing.T) {
	for _, body := range []string{
		`<?xml version="1.0"?><!DOCTYPE rss [<!ENTITY xxe "boom">]><rss version="2.0"><channel><item><title>&xxe;</title></item></channel></rss>`,
		`<rss version="2.0"><!ENTITY gone "later"><channel></channel></rss>`,
	} {
		items, err := rss.Parse([]byte(body))
		if err == nil {
			t.Fatalf("Parse(DTD body) = %#v, want an error", items)
		}
		if items != nil {
			t.Errorf("Parse(DTD body) items = %#v, want nil on error", items)
		}
		var terr *twitter.Error
		if !errors.As(err, &terr) {
			t.Fatalf("Parse(DTD body) error = %T (%v), want *twitter.Error", err, err)
		}
		if terr.Kind != twitter.KindMalformed {
			t.Errorf("Kind = %q, want %q", terr.Kind, twitter.KindMalformed)
		}
		if terr.Op != "rss.Parse" {
			t.Errorf("Op = %q, want %q", terr.Op, "rss.Parse")
		}
		if !strings.Contains(terr.Error(), "doctype/entity") {
			t.Errorf("err = %v, want the entity-expansion rejection message", terr)
		}
	}
}

// TestParseAcceptsFeedWithoutDoctype guards the rejection against
// false positives: the plain fixture (no DTD) must keep parsing.
func TestParseAcceptsFeedWithoutDoctype(t *testing.T) {
	if _, err := rss.Parse(loadFixture(t)); err != nil {
		t.Fatalf("Parse(fixture) = error %v, want success", err)
	}
}

// TestItemToTweetPubDateTolerance pins the pubDate normalization carried
// from Task 10: Nitter sometimes emits single-digit days (RFC1123Z's "02"
// wants two digits) and doubled spaces after the weekday comma. Both must
// parse to the same UTC instant the padded form yields.
func TestItemToTweetPubDateTolerance(t *testing.T) {
	want := time.Date(2026, 7, 5, 9, 9, 40, 0, time.UTC)
	for _, tc := range []struct {
		name, pubDate string
	}{
		{"zero-padded baseline", "Sun, 05 Jul 2026 09:09:40 +0000"},
		{"single-digit day, single space", "Sun, 5 Jul 2026 09:09:40 +0000"},
		{"single-digit day, double space", "Sun,  5 Jul 2026 09:09:40 +0000"},
		{"two-digit day, double space", "Sun, 05  Jul 2026 09:09:40 +0000"},
		{"leading/trailing whitespace", "  Sun, 5 Jul 2026 09:09:40 +0000  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tw, err := rss.ItemToTweet(rss.Item{
				GUID:    "https://nitter.example/NASA/status/42#m",
				Link:    "https://nitter.example/NASA/status/42#m",
				PubDate: tc.pubDate,
			})
			if err != nil {
				t.Fatalf("ItemToTweet = error %v", err)
			}
			if !tw.PublishedAt.Equal(want) {
				t.Errorf("PublishedAt = %v, want %v", tw.PublishedAt, want)
			}
			if tw.PublishedAt.Location() != time.UTC {
				t.Errorf("PublishedAt location = %v, want UTC", tw.PublishedAt.Location())
			}
		})
	}
}

// TestItemToTweetUnparseablePubDateStaysZero keeps the no-fabrication rule
// for input the normalization cannot rescue.
func TestItemToTweetUnparseablePubDateStaysZero(t *testing.T) {
	for _, pubDate := range []string{"not a date", "Sun, Jul 2026 09:09:40 +0000", "32 Jul 2026"} {
		tw, err := rss.ItemToTweet(rss.Item{
			GUID:    "https://nitter.example/NASA/status/42#m",
			Link:    "https://nitter.example/NASA/status/42#m",
			PubDate: pubDate,
		})
		if err != nil {
			t.Fatalf("ItemToTweet(%q) = error %v", pubDate, err)
		}
		if !tw.PublishedAt.IsZero() {
			t.Errorf("PubDate %q: PublishedAt = %v, want the zero time", pubDate, tw.PublishedAt)
		}
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// feedFor wraps a description-bearing item body into a minimal feed so tests
// can exercise Parse's description-media extraction end to end (the scan
// lives in Parse, not in ItemToTweet).
func feedFor(description string) []byte {
	return []byte(`<?xml version="1.0"?><rss version="2.0"><channel><item>` +
		`<guid>https://nitter.example/NASA/status/100#m</guid>` +
		`<link>https://nitter.example/NASA/status/100#m</link>` +
		`<description><![CDATA[` + description + `]]></description>` +
		`</item></channel></rss>`)
}

// TestParseQuoteAndArticleMediaMasked pins the Task 10 carry: images inside
// quote containers or article-card links in the description belong to the
// quoted tweet, never to the quoting item — the plugin's nested-media rule
// the HTML parser already applies.
func TestParseQuoteAndArticleMediaMasked(t *testing.T) {
	items, err := rss.Parse(feedFor(
		`<div class="tweet-content media-body" dir="auto">own text</div>` +
			`<img src="/pic/media%2FOWN.jpg"/>` +
			`<div class="quote"><div class="quoted-tweet"><img src="/pic/media%2FQUOTED.jpg"/></div></div>` +
			`<a href="/i/article/12345"><img src="/pic/media%2FARTICLE.jpg"/></a>` +
			`<div class="quote-header"><img src="/pic/media%2FHEADER.jpg"/></div>`))
	if err != nil {
		t.Fatalf("Parse = error %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Parse = %d items, want 1", len(items))
	}
	wantMedia := []string{"/pic/media%2FOWN.jpg"}
	if !reflect.DeepEqual(items[0].MediaURLs, wantMedia) {
		t.Errorf("MediaURLs = %#v, want %#v (quote/article images masked)", items[0].MediaURLs, wantMedia)
	}
}

// TestParseMediaOutsideMaskedSubtreesSurvives checks the mask does not
// over-reach: content after a closed quote container is kept, and nesting
// depth inside the masked subtree is tracked.
func TestParseMediaOutsideMaskedSubtreesSurvives(t *testing.T) {
	items, err := rss.Parse(feedFor(
		`<div class="quote"><div><img src="/pic/media%2FQUOTED.jpg"/></div></div>` +
			`<img src="/pic/media%2FAFTER.jpg"/>` +
			`<div class="tweet-content"><img src="/pic/media%2FBR%20tag.jpg"/></div>`))
	if err != nil {
		t.Fatalf("Parse = error %v", err)
	}
	wantMedia := []string{"/pic/media%2FAFTER.jpg", "/pic/media%2FBR%20tag.jpg"}
	if !reflect.DeepEqual(items[0].MediaURLs, wantMedia) {
		t.Errorf("MediaURLs = %#v, want %#v", items[0].MediaURLs, wantMedia)
	}
}
