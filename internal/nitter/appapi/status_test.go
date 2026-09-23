package appapi_test

// Status acquisition tests: ParseStatusRef (the ref-shape contract, pure)
// and Status (single-instance-pass fetch with the user → user-less route
// fallback, instance rotation identical to the other fetches).

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// statusPage builds a Nitter-shaped conversation page whose focused status
// (the timeline-item with the stats row) is user/id.
func statusPage(user, id string) string {
	return `<div class="timeline">` +
		`<div class="timeline-item">` +
		`<a class="tweet-link" href="/` + user + `/status/` + id + `#m"></a>` +
		`<div class="tweet-content">status body ` + id + `</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
}

// TestParseStatusRefTable pins the accepted ref shapes: bare numeric ID;
// https://(x|nitter).com/<user>/status/<id> with optional /photo/N or
// /video/1 suffix; nitter-style URLs of the same path shape (user optional
// on the /status/<id> route); scheme-less host URLs are normalized.
func TestParseStatusRefTable(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		id   string
		user string
	}{
		{ref: "2070000000000000010", id: "2070000000000000010", user: ""},
		{ref: "https://x.com/nasa/status/2070000000000000010", id: "2070000000000000010", user: "nasa"},
		{ref: "https://twitter.com/nasa/status/2070000000000000010", id: "2070000000000000010", user: "nasa"},
		{ref: "https://x.com/nasa/status/2070000000000000010/photo/1", id: "2070000000000000010", user: "nasa"},
		{ref: "https://x.com/nasa/status/2070000000000000010/video/1", id: "2070000000000000010", user: "nasa"},
		{ref: "https://x.com/nasa/statuses/2070000000000000010", id: "2070000000000000010", user: "nasa"},
		{ref: "https://nitter.example/nasa/status/2070000000000000010", id: "2070000000000000010", user: "nasa"},
		// The stock Nitter user-less route.
		{ref: "https://nitter.example/status/2070000000000000010", id: "2070000000000000010", user: ""},
		// Scheme-less host forms are normalized onto https.
		{ref: "nitter.example/nasa/status/2070000000000000010", id: "2070000000000000010", user: "nasa"},
		{ref: "x.com/nasa/status/123", id: "123", user: "nasa"},
	} {
		id, user, err := appapi.ParseStatusRef(tc.ref)
		if err != nil || id != tc.id || user != tc.user {
			t.Errorf("ParseStatusRef(%q) = (%q, %q, %v), want (%q, %q, nil)", tc.ref, id, user, err, tc.id, tc.user)
		}
	}
}

func TestParseStatusRefInvalid(t *testing.T) {
	for _, ref := range []string{
		"", "   ", "abc", "x.com", "12 345",
		"https://x.com/nasa",
		"https://x.com/nasa/status/abc",
		"https://x.com/i/web/status/123", // unknown multi-segment shape
		"https://x.com/nasa/likes/123",
		"https://x.com/nasa/status/123/photo/x",
		"https://x.com/nasa/status/123/photo",
		"https://x.com/nasa/status/123/favorites",
		"user/status/123", // no host, no scheme: ambiguous path-only ref
	} {
		id, user, err := appapi.ParseStatusRef(ref)
		if err == nil {
			t.Errorf("ParseStatusRef(%q) = (%q, %q), want an error", ref, id, user)
			continue
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
			t.Errorf("ParseStatusRef(%q) err = %v (%T), want KindInvalidArg", ref, err, err)
		}
	}
}

// TestStatusBareIDFetchesUserlessRouteDirectly: a bare numeric ID has no
// user, so the /i/status/<id> route is used directly — exactly one request.
func TestStatusBareIDFetchesUserlessRouteDirectly(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/i/status/2070000000000000010", 200, statusPage("nasa", "2070000000000000010"), nil},
	)
	tw, instance, err := newTimelineClient(t, srv.URL).Status(context.Background(), "2070000000000000010")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if instance != srv.URL {
		t.Errorf("instance = %q, want the serving instance", instance)
	}
	if tw.ID != "2070000000000000010" || tw.Author.Handle != "nasa" || tw.Text != "status body 2070000000000000010" {
		t.Errorf("tweet = %+v, want the fixture status", tw)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/i/status/2070000000000000010"}) {
		t.Errorf("requests = %v, want the single /i/ user-less fetch", got)
	}
}

func TestStatusUserRouteServesDirectly(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/nasa/status/2070000000000000010", 200, statusPage("nasa", "2070000000000000010"), nil},
	)
	tw, _, err := newTimelineClient(t, srv.URL).Status(context.Background(), "https://x.com/nasa/status/2070000000000000010")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if tw.ID != "2070000000000000010" {
		t.Errorf("ID = %q, want the requested status", tw.ID)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/nasa/status/2070000000000000010"}) {
		t.Errorf("requests = %v, want exactly the user route (no fallback)", got)
	}
}

func TestStatusUserRoute404FallsBackToUserless(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/nasa/status/2070000000000000010", 404, "gone", nil},
		timelineRoute{"/i/status/2070000000000000010", 200, statusPage("nasa", "2070000000000000010"), nil},
	)
	tw, _, err := newTimelineClient(t, srv.URL).Status(context.Background(), "https://x.com/nasa/status/2070000000000000010")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if tw.ID != "2070000000000000010" {
		t.Errorf("ID = %q, want the requested status", tw.ID)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/nasa/status/2070000000000000010", "/i/status/2070000000000000010"}) {
		t.Errorf("requests = %v, want the user route then the /i/ user-less fallback", got)
	}
}

// TestStatusUserRouteEmptyPageFallsBackToUserless covers the "empty result"
// trigger: a 200 page that yields no identifiable main status retries on the
// user-less route.
func TestStatusUserRouteEmptyPageFallsBackToUserless(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/nasa/status/2070000000000000010", 200, `<div class="timeline"></div>`, nil},
		timelineRoute{"/i/status/2070000000000000010", 200, statusPage("nasa", "2070000000000000010"), nil},
	)
	tw, _, err := newTimelineClient(t, srv.URL).Status(context.Background(), "https://x.com/nasa/status/2070000000000000010")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if tw.ID != "2070000000000000010" {
		t.Errorf("ID = %q, want the requested status", tw.ID)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/nasa/status/2070000000000000010", "/i/status/2070000000000000010"}) {
		t.Errorf("requests = %v, want the user route then the /i/ user-less fallback", got)
	}
}

// TestStatusUserRouteOtherErrorsDoNotFallback pins the retry boundary: a
// challenge or server error on the user route fails the attempt (and
// rotates instances) — only 404 and unidentifiable content fall through.
func TestStatusUserRouteOtherErrorsDoNotFallback(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/nasa/status/2070000000000000010", 503, "down", nil},
	)
	_, _, err := newTimelineClient(t, srv.URL).Status(context.Background(), "https://x.com/nasa/status/2070000000000000010")
	if err == nil {
		t.Fatal("Status = nil error, want the 503 to fail the attempt")
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("err = %v (%T), want KindUnavailable", err, err)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/nasa/status/2070000000000000010"}) {
		t.Errorf("requests = %v, want no user-less retry on a 5xx", got)
	}
}

func TestStatusAllInstancesExhaustedReportsLastError(t *testing.T) {
	srv1, _ := newTimelineFake(t, timelineRoute{"/nasa/status/2070000000000000010", 503, "down", nil})
	srv2, _ := newTimelineFake(t, timelineRoute{"/nasa/status/2070000000000000010", 404, "gone", nil})
	_, _, err := newTimelineClient(t, srv1.URL, srv2.URL).Status(context.Background(), "https://x.com/nasa/status/2070000000000000010")
	if err == nil {
		t.Fatal("Status = nil error, want the last instance failure")
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
		t.Fatalf("err = %v (%T), want the last instance's classification (404 / KindNotFound)", err, err)
	}
}

// TestStatusInvalidRefIsInvalidArgBeforeNetwork: a zero-instance chooser
// would answer "no instances configured" — KindInvalidArg proves the ref was
// rejected before any rotation.
func TestStatusInvalidRefIsInvalidArgBeforeNetwork(t *testing.T) {
	c := newTimelineClient(t) // no instances at all
	for _, ref := range []string{"", "abc", "https://x.com/nasa/status/abc"} {
		tw, instance, err := c.Status(context.Background(), ref)
		if err == nil {
			t.Fatalf("Status(%q) = (%+v, %q), want an error", ref, tw, instance)
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
			t.Errorf("Status(%q) err = %v (%T), want KindInvalidArg before any network", ref, err, err)
		}
	}
}

func TestStatusNoInstancesConfigured(t *testing.T) {
	c := newTimelineClient(t)
	tw, instance, err := c.Status(context.Background(), "123")
	if err == nil {
		t.Fatalf("Status = (%+v, %q), want an error", tw, instance)
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("err = %v (%T), want KindUnavailable", err, err)
	}
	if !strings.Contains(err.Error(), "no instances configured") {
		t.Errorf("err = %v, want the chooser's no-instances message", err)
	}
}

func TestStatusRespectsContextCancellation(t *testing.T) {
	srv, _ := newTimelineFake(t, timelineRoute{"/nasa/status/123", 503, "down", nil})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := newTimelineClient(t, srv.URL, srv.URL).Status(ctx, "123")
	if err == nil {
		t.Fatal("Status = nil error, want the cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
}
