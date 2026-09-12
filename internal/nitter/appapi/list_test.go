package appapi_test

// List-timeline acquisition tests. A Nitter list page shares the timeline
// markup and pipeline (ClassifyPage → ParseTimeline → cursor pagination);
// the endpoint is /i/lists/<listID>. The listID is validated as non-empty
// and URL-path-safe (no whitespace, ?, # or /) BEFORE any rotation or
// network; otherwise it is passed through.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/twitter-cli/sdk"
)

func TestListTimelineReturnsTweets(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/i/lists/12345", 200, htmlPage([]string{"401", "402"}, "")},
	)
	tweets, instance, err := newTimelineClient(t, srv.URL).ListTimeline(context.Background(), "12345", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("ListTimeline: %v", err)
	}
	if instance != srv.URL {
		t.Errorf("instance = %q, want the serving instance %q", instance, srv.URL)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2", len(tweets))
	}
	if tweets[0].ID != "401" || tweets[1].ID != "402" {
		t.Errorf("ids = %q/%q, want 401/402", tweets[0].ID, tweets[1].ID)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/i/lists/12345"}) {
		t.Errorf("requests = %v, want the single list fetch", got)
	}
}

func TestListTimelinePaginationBoundedByMaxPages(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/i/lists/12345", 200, htmlPage([]string{"401"}, "c1")},
		timelineRoute{"/i/lists/12345?cursor=c1", 200, htmlPage([]string{"402"}, "c2")},
		timelineRoute{"/i/lists/12345?cursor=c2", 200, htmlPage([]string{"403"}, "c3")},
		timelineRoute{"/i/lists/12345?cursor=c3", 200, htmlPage([]string{"404"}, "")},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).ListTimeline(context.Background(), "12345", appapi.PageOptions{MaxPages: 2})
	if err != nil {
		t.Fatalf("ListTimeline: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2 (two pages worth)", len(tweets))
	}
	fetches := 0
	for _, r := range rec.requests() {
		if strings.HasPrefix(r, "/i/lists/") {
			fetches++
		}
	}
	if fetches != 2 {
		t.Errorf("list fetches = %d, want exactly 2 (bounded by MaxPages)", fetches)
	}
}

func TestListTimelineLimitStopsPaginationEarly(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/i/lists/12345", 200, htmlPage([]string{"401"}, "c1")},
		timelineRoute{"/i/lists/12345?cursor=c1", 200, htmlPage([]string{"402", "403", "404"}, "c2")},
		timelineRoute{"/i/lists/12345?cursor=c2", 200, htmlPage([]string{"405"}, "")},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).ListTimeline(context.Background(), "12345", appapi.PageOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ListTimeline: %v", err)
	}
	if len(tweets) != 3 {
		t.Fatalf("tweets = %d, want 3", len(tweets))
	}
	if got := rec.requests(); len(got) != 2 {
		t.Errorf("requests = %v, want pagination to stop once Limit is met", got)
	}
}

// TestListTimelineEmptyPageYieldsEmptySliceWithoutError pins the empty-list
// semantics: a genuinely empty page is a success with zero tweets — the
// "list not yet ingested" case is indistinguishable server-side and must not
// become an error.
func TestListTimelineEmptyPageYieldsEmptySliceWithoutError(t *testing.T) {
	srv, _ := newTimelineFake(t,
		timelineRoute{"/i/lists/12345", 200, htmlPage(nil, "")},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).ListTimeline(context.Background(), "12345", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("ListTimeline = error %v, want success with an empty slice", err)
	}
	if len(tweets) != 0 {
		t.Fatalf("tweets = %v, want empty", tweets)
	}
}

// TestListTimelineInvalidIDIsInvalidArgBeforeNetwork pins the validation
// order: a zero-instance chooser would answer "no instances configured", so
// a KindInvalidArg proves the list ID was rejected before any rotation.
// Whitespace, ?, # and / are rejected as URL-path-unsafe; everything else
// (numeric IDs and Nitter list refs) passes through.
func TestListTimelineInvalidIDIsInvalidArgBeforeNetwork(t *testing.T) {
	c := newTimelineClient(t) // no instances at all
	for _, listID := range []string{
		"", "   ", "12 345", "12?345", "12#345", "12/345", "\t12345\n",
	} {
		tweets, instance, err := c.ListTimeline(context.Background(), listID, appapi.PageOptions{})
		if err == nil {
			t.Fatalf("ListTimeline(%q) = (%v, %q), want an error", listID, tweets, instance)
		}
		var terr *twitter.Error
		if !errors.As(err, &terr) {
			t.Fatalf("ListTimeline(%q) error = %T (%v), want *twitter.Error", listID, err, err)
		}
		if terr.Kind != twitter.KindInvalidArg {
			t.Errorf("ListTimeline(%q) Kind = %v, want %v (before any network)", listID, terr.Kind, twitter.KindInvalidArg)
		}
	}
}

func TestListTimelineValidNonNumericRefsPassThrough(t *testing.T) {
	// Nitter accepts numeric IDs and some refs — non-numeric path-safe ids
	// are passed through unchanged.
	srv, rec := newTimelineFake(t,
		timelineRoute{"/i/lists/custom-ref_1", 200, htmlPage([]string{"401"}, "")},
	)
	if _, _, err := newTimelineClient(t, srv.URL).ListTimeline(context.Background(), "custom-ref_1", appapi.PageOptions{}); err != nil {
		t.Fatalf("ListTimeline: %v", err)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/i/lists/custom-ref_1"}) {
		t.Errorf("requests = %v, want the raw ref passed through", got)
	}
}

func TestListTimelineChallengePropagates(t *testing.T) {
	login := `<form action="/login"><input name="username"/></form>`
	srv, _ := newTimelineFake(t,
		timelineRoute{"/i/lists/12345", 200, login},
	)
	_, _, err := newTimelineClient(t, srv.URL).ListTimeline(context.Background(), "12345", appapi.PageOptions{})
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindChallenge {
		t.Fatalf("err = %v (%T), want KindChallenge from ClassifyPage", err, err)
	}
}

func TestListTimelineAllInstancesExhaustedReportsLastError(t *testing.T) {
	srv1, _ := newTimelineFake(t, timelineRoute{"/i/lists/12345", 503, "down"})
	srv2, _ := newTimelineFake(t, timelineRoute{"/i/lists/12345", 404, "gone"})
	_, _, err := newTimelineClient(t, srv1.URL, srv2.URL).ListTimeline(context.Background(), "12345", appapi.PageOptions{})
	if err == nil {
		t.Fatal("ListTimeline = nil error, want the last instance failure")
	}
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindNotFound {
		t.Fatalf("err = %v (%T), want the last instance's classification (404 / KindNotFound)", err, err)
	}
}

func TestListTimelineRespectsContextCancellation(t *testing.T) {
	srv, _ := newTimelineFake(t, timelineRoute{"/i/lists/12345", 503, "down"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the fetch must notice before the second instance attempt
	_, _, err := newTimelineClient(t, srv.URL, srv.URL).ListTimeline(ctx, "12345", appapi.PageOptions{})
	if err == nil {
		t.Fatal("ListTimeline = nil error, want the cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
}

func TestListTimelineNoInstancesConfigured(t *testing.T) {
	c := newTimelineClient(t)
	tweets, instance, err := c.ListTimeline(context.Background(), "12345", appapi.PageOptions{})
	if err == nil {
		t.Fatalf("ListTimeline = (%v, %q), want an error", tweets, instance)
	}
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindUnavailable {
		t.Fatalf("err = %v (%T), want KindUnavailable", err, err)
	}
	if !strings.Contains(err.Error(), "no instances configured") {
		t.Errorf("err = %v, want the chooser's no-instances message", err)
	}
}
