package html_test

// ParseStatus tests: the single-status page (/<user>/status/<id>). The
// conversation view carries thread/reply context as further timeline-items;
// the focused (main) status is the one Nitter renders its interaction-stats
// row on. Counts themselves are NOT extracted (never fabricated) — the row
// is only the structural main-status marker.

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/html"
	sdk "github.com/shitianyaa/twitter-cli/sdk"
)

// statusDate is the main fixture tweet's date.
func statusDate(t *testing.T) time.Time {
	t.Helper()
	ts, err := time.Parse("Jan 2, 2006 · 3:04 PM MST", "Jul 20, 2026 · 2:11 PM UTC")
	if err != nil {
		t.Fatalf("parse want date: %v", err)
	}
	return ts.UTC()
}

func TestParseStatusExtractsMainTweetWithQuoteAndMedia(t *testing.T) {
	tw, err := html.ParseStatus(loadFixture(t, "status.html"), instance)
	if err != nil {
		t.Fatalf("ParseStatus = error %v", err)
	}
	if tw.ID != "2070000000000000010" {
		t.Errorf("ID = %q, want the focused status (not the thread context …009 or the reply …012)", tw.ID)
	}
	if tw.Author.Handle != "nasa" {
		t.Errorf("Author.Handle = %q, want nasa", tw.Author.Handle)
	}
	if want := "https://x.com/nasa/status/2070000000000000010"; tw.URL != want {
		t.Errorf("URL = %q, want %q", tw.URL, want)
	}
	wantText := "Artemis II crew walkthrough from the pad.\nThread below."
	if tw.Text != wantText {
		t.Errorf("Text = %q, want %q", tw.Text, wantText)
	}
	if !tw.PublishedAt.Equal(statusDate(t)) || tw.PublishedAt.Location() != time.UTC {
		t.Errorf("PublishedAt = %v, want the fixture date in UTC", tw.PublishedAt)
	}
	wantMedia := []sdk.Media{{Type: "image", URL: "https://pbs.twimg.com/media/GzT4KzXwAAU7.jpg?name=orig"}}
	if !reflect.DeepEqual(tw.Media, wantMedia) {
		t.Errorf("Media = %#v, want %#v", tw.Media, wantMedia)
	}
	// Quote summary: identity, URL and text from the .quote subtree; the
	// quoted text must NOT leak into the main tweet's text (checked above).
	if tw.Quote == nil {
		t.Fatalf("Quote = nil, want the quoted-status summary")
	}
	if tw.Quote.ID != "2070000000000000011" {
		t.Errorf("Quote.ID = %q, want 2070000000000000011", tw.Quote.ID)
	}
	if want := "https://x.com/esa/status/2070000000000000011"; tw.Quote.URL != want {
		t.Errorf("Quote.URL = %q, want %q", tw.Quote.URL, want)
	}
	if want := "Juice gravity assist complete, all instruments nominal."; tw.Quote.Text != want {
		t.Errorf("Quote.Text = %q, want %q", tw.Quote.Text, want)
	}
	if tw.Quote.Author.Handle != "esa" {
		t.Errorf("Quote.Author.Handle = %q, want esa", tw.Quote.Author.Handle)
	}
	if tw.IsRetweet || tw.RepostedBy != "" || tw.ReplyTo != "" {
		t.Errorf("IsRetweet/RepostedBy/ReplyTo = %v/%q/%q, want zero on the fixture", tw.IsRetweet, tw.RepostedBy, tw.ReplyTo)
	}
}

// minimalStatusPage builds a single-item status page: one timeline-item with
// identity, content and (optionally) the stats row.
func minimalStatusPage(stats bool) string {
	page := `<div class="timeline"><div class="timeline-item">` +
		`<a class="tweet-link" href="/nasa/status/2070000000000000010#m"></a>` +
		`<div class="tweet-content">body text</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>`
	if stats {
		page += `<div class="tweet-stats"><span class="tweet-stat">1</span></div>`
	}
	return page + `</div></div>`
}

// TestParseStatusSingleItemWithoutStats pins the fallback: a page with
// exactly ONE timeline-item carrying content has an unambiguous main status
// even without the stats row.
func TestParseStatusSingleItemWithoutStats(t *testing.T) {
	tw, err := html.ParseStatus([]byte(minimalStatusPage(false)), instance)
	if err != nil {
		t.Fatalf("ParseStatus = error %v", err)
	}
	if tw.ID != "2070000000000000010" || tw.Author.Handle != "nasa" || tw.Text != "body text" {
		t.Errorf("tweet = %+v, want the single item's status", tw)
	}
}

func TestParseStatusMalformed(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "no timeline items", body: `<html><body><p>nothing here</p></body></html>`},
		{name: "item without content", body: `<div class="timeline"><div class="timeline-item"></div></div>`},
		// Two content items and no stats row: the main status cannot be
		// identified without guessing — malformed, never a fabricated pick.
		{name: "ambiguous items without stats", body: `<div class="timeline">` +
			`<div class="timeline-item"><a class="tweet-link" href="/a/status/1#m"></a><div class="tweet-content">one</div></div>` +
			`<div class="timeline-item"><a class="tweet-link" href="/b/status/2#m"></a><div class="tweet-content">two</div></div>` +
			`</div>`},
		// The selected main item must carry a status link: identity is never
		// fabricated.
		{name: "main item without status link", body: `<div class="timeline">` +
			`<div class="timeline-item"><div class="tweet-content">body</div><div class="tweet-stats"><span>1</span></div></div>` +
			`</div>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := html.ParseStatus([]byte(tc.body), instance)
			if err == nil {
				t.Fatalf("ParseStatus = nil error, want KindMalformed")
			}
			var terr *sdk.Error
			if !errors.As(err, &terr) || terr.Kind != sdk.KindMalformed {
				t.Fatalf("err = %v (%T), want KindMalformed", err, err)
			}
		})
	}
}
