package html_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/html"
	sdk "github.com/shitianyaa/twitter-cli/sdk"
)

// Fixtures are shaped after documented Nitter HTML timeline markup and the
// reference plugin's parser semantics (media_support/html_backend/parser.py,
// which runs against real Nitter instances daily). Real-instance comparison
// is carried as a user-run smoke step in Task 13/14, matching R12.

const instance = "https://nitter.example"

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return b
}

// parse is a helper that runs ParseTimeline and fails the test on error.
func parse(t *testing.T, body []byte) html.Page {
	t.Helper()
	page, err := html.ParseTimeline(body, instance)
	if err != nil {
		t.Fatalf("ParseTimeline = error %v", err)
	}
	return page
}

// wantTweet is the per-position expectation of one projected timeline item.
type wantTweet struct {
	id         string
	user       string
	text       string
	published  time.Time
	media      []sdk.Media
	isRetweet  bool
	repostedBy string
}

func checkTweet(t *testing.T, label string, got sdk.Tweet, w wantTweet) {
	t.Helper()
	if got.ID != w.id {
		t.Errorf("%s ID = %q, want %q", label, got.ID, w.id)
	}
	if want := "https://x.com/" + w.user + "/status/" + w.id; got.URL != want {
		t.Errorf("%s URL = %q, want %q", label, got.URL, want)
	}
	if got.Text != w.text {
		t.Errorf("%s Text = %q, want %q", label, got.Text, w.text)
	}
	if got.Author.Handle != w.user {
		t.Errorf("%s Author.Handle = %q, want %q", label, got.Author.Handle, w.user)
	}
	if got.Author.Name != "" || got.Author.AvatarURL != "" {
		t.Errorf("%s Author.Name/AvatarURL = %q/%q, want empty (source does not carry them)", label, got.Author.Name, got.Author.AvatarURL)
	}
	if !got.PublishedAt.Equal(w.published) {
		t.Errorf("%s PublishedAt = %v, want %v", label, got.PublishedAt, w.published)
	}
	if got.PublishedAt.Location() != time.UTC {
		t.Errorf("%s PublishedAt location = %v, want UTC", label, got.PublishedAt.Location())
	}
	if !reflect.DeepEqual(got.Media, w.media) {
		t.Errorf("%s Media = %#v, want %#v", label, got.Media, w.media)
	}
	if got.IsRetweet != w.isRetweet {
		t.Errorf("%s IsRetweet = %v, want %v", label, got.IsRetweet, w.isRetweet)
	}
	if got.RepostedBy != w.repostedBy {
		t.Errorf("%s RepostedBy = %q, want %q", label, got.RepostedBy, w.repostedBy)
	}
	if got.ReplyTo != "" || got.Quote != nil {
		t.Errorf("%s ReplyTo/Quote = %q/%v, want zero (not carried by timeline items)", label, got.ReplyTo, got.Quote)
	}
}

// TestParseTimelineExtractsTweets covers the basic extraction table: three
// timeline items with id/user/text/date/media/repost markers.
func TestParseTimelineExtractsTweets(t *testing.T) {
	page := parse(t, loadFixture(t, "timeline.html"))

	want := []wantTweet{
		{
			id:   "1620000000000000001",
			user: "SpaceExplored",
			text: "Engine firing complete & all systems nominal.\nNext window opens Monday #Artemis",
			published: func() time.Time {
				ts, err := time.Parse("Jan 2, 2006 · 3:04 PM MST", "Jul 27, 2026 · 9:09 AM UTC")
				if err != nil {
					t.Fatalf("parse want date: %v", err)
				}
				return ts.UTC()
			}(),
			media: []sdk.Media{{Type: "image", URL: "https://nitter.example/pic/media%2FFphoto1.jpg%3Fname%3Dsmall"}},
		},
		{
			id:         "1620000000000000002",
			user:       "rocketgirl",
			text:       "Static fire of the full stack is done. Beautiful plume.",
			published:  time.Date(2026, 7, 26, 20, 15, 0, 0, time.UTC),
			media:      []sdk.Media{{Type: "image", URL: "https://pbs.twimg.com/media/GzT4KzXwAAU7.jpg?name=orig"}},
			isRetweet:  true,
			repostedBy: "SpaceExplored",
		},
		{
			id:   "1620000000000000003",
			user: "SpaceExplored",
			text: "Our full interview with the booster team is up.",
			published: func() time.Time {
				ts, err := time.Parse("Jan 2, 2006 · 3:04 PM MST", "Jul 25, 2026 · 6:40 AM UTC")
				if err != nil {
					t.Fatalf("parse want date: %v", err)
				}
				return ts.UTC()
			}(),
			media: []sdk.Media{
				{Type: "image", URL: "https://nitter.example/pic/orig/media/OwnPic.jpg?name=small"},
				{Type: "video", URL: "https://nitter.example/video/1620000000000000003"},
			},
		},
	}

	if len(page.Tweets) != len(want) {
		t.Fatalf("ParseTimeline(fixture) = %d tweets, want %d", len(page.Tweets), len(want))
	}
	for i, w := range want {
		checkTweet(t, fmt.Sprintf("tweet %d", i), page.Tweets[i], w)
	}
}

// TestParseTimelineMasksQuotes pins the quote-masking money-shot: item 3 of
// the fixture carries a quoted tweet (its own tweet-content and /pic/media
// image) followed by the author's own attachments. Quoted text and media must
// not leak into the author's tweet, the article-card link text is masked, and
// the quote must not surface as a separate Tweet.
func TestParseTimelineMasksQuotes(t *testing.T) {
	page := parse(t, loadFixture(t, "timeline.html"))

	if len(page.Tweets) != 3 {
		t.Fatalf("quoted tweet leaked as a separate item: %d tweets, want 3", len(page.Tweets))
	}
	author := page.Tweets[2]
	if author.Author.Handle != "SpaceExplored" {
		t.Fatalf("tweet 3 author = %q, want SpaceExplored (quote link must not win)", author.Author.Handle)
	}
	for _, banned := range []string{"Quoted:", "engine test", "Full story", "EngineerLuca"} {
		if strings.Contains(author.Text, banned) {
			t.Errorf("tweet 3 Text = %q, must not contain %q", author.Text, banned)
		}
	}
	for _, m := range author.Media {
		if strings.Contains(m.URL, "QuotedPic") {
			t.Errorf("tweet 3 Media contains quoted image %q", m.URL)
		}
	}
	if !strings.Contains(author.Text, "Our full interview") {
		t.Errorf("tweet 3 Text = %q, author text lost", author.Text)
	}
	if !strings.Contains(author.Media[0].URL, "OwnPic.jpg") {
		t.Errorf("tweet 3 Media[0] = %q, author image lost", author.Media[0].URL)
	}
}

// TestParseTimelineCursorPicksOlderShowMore covers cursor extraction: both a
// newer and a load-more show-more carry cursor params, the LAST one in
// document order (the older link) wins, and the value is URL-decoded.
func TestParseTimelineCursorPicksOlderShowMore(t *testing.T) {
	page := parse(t, loadFixture(t, "timeline.html"))
	if page.NextCursor != "older/page2" {
		t.Errorf("NextCursor = %q, want %q", page.NextCursor, "older/page2")
	}
}

// TestParseTimelineSearchFixture smokes the search page shell that Task 14
// consumes: two items plus a show-more cursor.
func TestParseTimelineSearchFixture(t *testing.T) {
	page := parse(t, loadFixture(t, "search.html"))

	want := []wantTweet{
		{
			id:        "2070000000000000010",
			user:      "nasa",
			text:      "Artemis II crew walkthrough from the pad.",
			published: time.Date(2026, 7, 20, 14, 11, 0, 0, time.UTC),
			media:     []sdk.Media{{Type: "image", URL: "https://nitter.example/pic/media%2FNasaImg.jpg"}},
		},
		{
			id:        "2070000000000000011",
			user:      "esa",
			text:      "Juice gravity assist complete, all instruments nominal.",
			published: time.Date(2026, 7, 19, 10, 2, 0, 0, time.UTC),
			media:     nil,
		},
	}
	if len(page.Tweets) != len(want) {
		t.Fatalf("ParseTimeline(search) = %d tweets, want %d", len(page.Tweets), len(want))
	}
	for i, w := range want {
		checkTweet(t, fmt.Sprintf("search tweet %d", i), page.Tweets[i], w)
	}
	if page.NextCursor != `"sCursor/2"` {
		t.Errorf("search NextCursor = %q, want %q", page.NextCursor, `"sCursor/2"`)
	}
}

// TestParseTimelineMediaExclusions pins the plugin's exclusion gates: profile
// images/banners, video thumbnails and emoji URLs never become author media,
// and non-http schemes are dropped. One valid image survives.
func TestParseTimelineMediaExclusions(t *testing.T) {
	body := []byte(fmt.Sprintf(`
<div class="timeline">
 <div class="timeline-item">
  <a class="tweet-link" href="/mediabot/status/1111111111111111111#m"></a>
  <div class="tweet-body">
   <div class="tweet-content media-body">media soup</div>
   <div class="attachments">
    <a class="still-image" href="/pic/profile_images%%2FAvatar_normal.jpg">avatar</a>
    <a class="still-image" href="/pic/profile_banners%%2FBanner.jpg">banner</a>
    <a class="still-image" href="/pic/ext_tw_video_thumb%%2F111%%2Fpu%%2Fimg%%2Fa.jpg">thumb</a>
    <a class="still-image" href="/pic/emoji%%2F1f680.png">emoji</a>
    <a class="still-image" href="javascript:alert(1)">xss</a>
    <a class="still-image" href="/pic/media%%2FRealOne.jpg">real</a>
   </div>
  </div>
 </div>
</div>`))

	page := parse(t, body)
	if len(page.Tweets) != 1 {
		t.Fatalf("ParseTimeline = %d tweets, want 1", len(page.Tweets))
	}
	want := []sdk.Media{{Type: "image", URL: "https://nitter.example/pic/media%2FRealOne.jpg"}}
	if !reflect.DeepEqual(page.Tweets[0].Media, want) {
		t.Errorf("Media = %#v, want %#v", page.Tweets[0].Media, want)
	}
}

// TestParseTimelineDeduplicatesSameStatus covers the seen-set: the same
// user+id appearing twice in one page keeps the first occurrence only.
func TestParseTimelineDeduplicatesSameStatus(t *testing.T) {
	item := func(text string) string {
		return fmt.Sprintf(`<div class="timeline-item">`+
			`<a class="tweet-link" href="/dup/status/2222222222222222222#m"></a>`+
			`<div class="tweet-content media-body">%s</div></div>`, text)
	}
	body := []byte(item("first") + item("second"))

	page := parse(t, body)
	if len(page.Tweets) != 1 {
		t.Fatalf("ParseTimeline = %d tweets, want 1", len(page.Tweets))
	}
	if page.Tweets[0].Text != "first" {
		t.Errorf("tweet Text = %q, want the first occurrence %q", page.Tweets[0].Text, "first")
	}
}

// TestParseTimelineUnparseableDateZeroesPublishedAt covers the no-fabrication
// rule: a broken tweet-date title leaves PublishedAt zero and the item is
// still extracted.
func TestParseTimelineUnparseableDateZeroesPublishedAt(t *testing.T) {
	body := []byte(`<div class="timeline-item">` +
		`<a class="tweet-link" href="/datetest/status/3333333333333333333#m"></a>` +
		`<span class="tweet-date"><a href="/datetest/status/3333333333333333333#m" title="not a date">?</a></span>` +
		`<div class="tweet-content media-body">body survives</div></div>`)

	page := parse(t, body)
	if len(page.Tweets) != 1 {
		t.Fatalf("ParseTimeline = %d tweets, want 1", len(page.Tweets))
	}
	got := page.Tweets[0]
	if !got.PublishedAt.IsZero() {
		t.Errorf("PublishedAt = %v, want zero time", got.PublishedAt)
	}
	if got.ID != "3333333333333333333" || got.Text != "body survives" {
		t.Errorf("ID/Text = %q/%q, item must still be extracted", got.ID, got.Text)
	}
}

// TestParseTimelineSkipsItemsWithoutStatusLink: a timeline-item without a
// /<user>/status/<id> link cannot be identified and is skipped (no
// fabricated identity).
func TestParseTimelineSkipsItemsWithoutStatusLink(t *testing.T) {
	body := []byte(`<div class="timeline">` +
		`<div class="timeline-item"><div class="tweet-content media-body">no link</div></div>` +
		`<div class="timeline-item"><a class="tweet-link" href="/ok/status/4444444444444444444#m"></a>` +
		`<div class="tweet-content media-body">linked</div></div>` +
		`</div>`)

	page := parse(t, body)
	if len(page.Tweets) != 1 {
		t.Fatalf("ParseTimeline = %d tweets, want 1", len(page.Tweets))
	}
	if page.Tweets[0].Text != "linked" {
		t.Errorf("tweet Text = %q, want %q", page.Tweets[0].Text, "linked")
	}
}

// TestParseTimelineEmptyTimeline: a well-formed page without timeline items
// (the empty state) is not an error — empty Page, nil error.
func TestParseTimelineEmptyTimeline(t *testing.T) {
	body := []byte(`<!DOCTYPE html><html><head><title>NASA / Nitter</title></head>` +
		`<body><div class="timeline"><div class="timeline-end">No items found</div></div></body></html>`)

	page := parse(t, body)
	if len(page.Tweets) != 0 {
		t.Errorf("ParseTimeline = %d tweets, want 0", len(page.Tweets))
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty", page.NextCursor)
	}
	if err := html.ClassifyPage(body); err != nil {
		t.Errorf("ClassifyPage(empty timeline) = %v, want nil", err)
	}
}

// TestParseTimelineGarbageTolerated: goquery never errors on broken HTML —
// garbage bytes degrade to an empty Page with nil error.
func TestParseTimelineGarbageTolerated(t *testing.T) {
	garbage := []byte{0x00, 0x01, 0xFF, 0xFE, '}', '<', '>', 0x80, '<', 'd', 'i', 'v'}
	page := parse(t, garbage)
	if len(page.Tweets) != 0 || page.NextCursor != "" {
		t.Errorf("ParseTimeline(garbage) = %d tweets, cursor %q; want empty Page", len(page.Tweets), page.NextCursor)
	}
	if err := html.ClassifyPage(garbage); err != nil {
		t.Errorf("ClassifyPage(garbage) = %v, want nil", err)
	}
}

// TestClassifyPage pins the page classification: Nitter's login form and
// maintenance marker are challenges, the structured error panel is
// upstream-unavailable with a token-stable message, anything else is nil.
func TestClassifyPage(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    sdk.Kind
		wantMsg string
	}{
		{
			name:    "login form is a challenge",
			body:    `<html><body><div class="login-container"><form action="/login" method="post"><input name="username"></form></div></body></html>`,
			want:    sdk.KindChallenge,
			wantMsg: "login or maintenance page",
		},
		{
			name:    "maintenance marker is a challenge",
			body:    `<html><body><div class="maintenance">Down for maintenance</div></body></html>`,
			want:    sdk.KindChallenge,
			wantMsg: "login or maintenance page",
		},
		{
			name:    "error panel is unavailable",
			body:    `<html><body><div class="error-panel">Instance has been rate limited. Try again later.</div></body></html>`,
			want:    sdk.KindUnavailable,
			wantMsg: "error panel",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := html.ClassifyPage([]byte(tc.body))
			if err == nil {
				t.Fatalf("ClassifyPage = nil, want kind %s", tc.want)
			}
			var terr *sdk.Error
			if !errors.As(err, &terr) {
				t.Fatalf("ClassifyPage error %v is not *sdk.Error", err)
			}
			if terr.Kind != tc.want {
				t.Errorf("kind = %s, want %s", terr.Kind, tc.want)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("message %q does not contain token-stable text %q", err.Error(), tc.wantMsg)
			}
		})
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "healthy timeline fixture is nil", body: string(loadFixture(t, "timeline.html"))},
		{name: "search fixture is nil", body: string(loadFixture(t, "search.html"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := html.ClassifyPage([]byte(tc.body)); err != nil {
				t.Errorf("ClassifyPage = %v, want nil", err)
			}
		})
	}
}
