package appapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/nitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

const (
	rssFeedBody = `<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel><title>NASA</title><item><title>One</title></item></channel></rss>`
	// A valid feed with a channel but zero items: an account with no posts
	// still serves a working feed, so the RSS criterion must accept it.
	rssFeedEmpty = `<rss version="2.0"><channel><title>NASA</title></channel></rss>`
	// An RSS-shaped root without a channel is not a usable feed.
	rssNoChannel = `<rss version="2.0"></rss>`
	timelineBody = `<div class="timeline"><div class="timeline-item">a post</div></div><div class="timeline-end">end</div>`
	// An empty user timeline still renders the timeline-end marker.
	timelineEmptyBody = `<div class="timeline"><div class="timeline-end">No more posts.</div></div>`
	nonFeedBody       = `<!doctype html><html><body>instance front page</body></html>`
)

// route is one fake-instance endpoint: requests to path answer with status
// and body.
type route struct {
	path   string
	status int
	body   string
}

// recorder records request targets (path?query) as the fake receives them.
type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) add(target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, target)
}

func (r *recorder) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func newFakeNitter(t *testing.T, routes ...route) (*httptest.Server, *recorder) {
	t.Helper()
	return newSlowFakeNitter(t, 0, routes...)
}

// newSlowFakeNitter is newFakeNitter with every route answering after delay.
// It exists for the latency-asserting test: Windows resolves time.Now to
// ~0.5ms, so an un-slept loopback roundtrip can quantize to exactly 0 on fast
// CI hardware; a server-side delay keeps the measured latency deterministically
// above the clock granularity.
func newSlowFakeNitter(t *testing.T, delay time.Duration, routes ...route) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.HandleFunc(rt.path, func(w http.ResponseWriter, req *http.Request) {
			if delay > 0 {
				time.Sleep(delay)
			}
			rec.add(req.URL.RequestURI())
			w.WriteHeader(rt.status)
			_, _ = io.WriteString(w, rt.body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

// newProbeClient builds an appapi.Client over the real httpx transport with
// retries, backoff and pacing disabled, so probes against httptest stay fast
// and deterministic.
func newProbeClient(t *testing.T) *appapi.Client {
	t.Helper()
	hx, err := httpx.New(httpx.Options{RetryAttempts: -1, RetryDelay: -1, MinInterval: -1})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	return &appapi.Client{HTTP: hx, Now: time.Now}
}

func TestTestInstanceAllProbesOK(t *testing.T) {
	// The latency assertion below needs a roundtrip measurably above the OS
	// clock granularity, so the fake instance answers ~2ms late (~4 ticks of
	// the ~0.5ms Windows monotonic clock) without weakening the > 0 contract.
	const probeDelay = 2 * time.Millisecond
	srv, rec := newSlowFakeNitter(t, probeDelay,
		route{"/NASA/rss", 200, rssFeedBody},
		route{"/NASA", 200, timelineBody},
		route{"/search", 200, timelineBody},
		route{"/i/lists/", 200, timelineBody},
	)
	report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL,
		appapi.TestOptions{User: "NASA", IncludeSearch: true, ListID: "123"})
	if err != nil {
		t.Fatalf("TestInstance: %v", err)
	}
	if report.URL != srv.URL {
		t.Errorf("report.URL = %q, want the normalized base %q", report.URL, srv.URL)
	}
	for name, p := range map[string]nitter.Probe{
		"rss":       report.RSS,
		"user_html": report.UserHTML,
		"search":    report.Search,
		"list":      report.List,
	} {
		if !p.OK || p.Status != 200 || p.Err != "" {
			t.Errorf("%s probe = %+v, want ok/200/no err", name, p)
		}
	}
	if report.Latency <= 0 {
		t.Errorf("latency = %v, want > 0", report.Latency)
	}
	// Probes hit the documented endpoints, in order, with the pinned search
	// query (f before q).
	want := []string{"/NASA/rss", "/NASA", "/search?f=tweets&q=nitter", "/i/lists/123"}
	if got := rec.requests(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestTestInstanceProbeCriteria(t *testing.T) {
	t.Run("rss 200 with channel but no items is ok", func(t *testing.T) {
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedEmpty}, route{"/NASA", 200, timelineBody})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if !report.RSS.OK || report.RSS.Status != 200 {
			t.Errorf("rss probe = %+v, want ok/200 (empty feed is still a feed)", report.RSS)
		}
	})
	t.Run("rss 200 non-feed body fails as not rss", func(t *testing.T) {
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, nonFeedBody}, route{"/NASA", 200, timelineBody})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if report.RSS.OK || report.RSS.Status != 200 || report.RSS.Err != "not rss" {
			t.Errorf("rss probe = %+v, want failed/200/\"not rss\"", report.RSS)
		}
	})
	t.Run("rss 200 without channel fails", func(t *testing.T) {
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssNoChannel}, route{"/NASA", 200, timelineBody})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if report.RSS.OK || report.RSS.Err != "not rss" {
			t.Errorf("rss probe = %+v, want failed with \"not rss\"", report.RSS)
		}
	})
	t.Run("user html empty timeline is ok via timeline-end", func(t *testing.T) {
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedBody}, route{"/NASA", 200, timelineEmptyBody})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if !report.UserHTML.OK || report.UserHTML.Status != 200 {
			t.Errorf("user_html probe = %+v, want ok/200 (empty timeline is still a timeline)", report.UserHTML)
		}
	})
	t.Run("user html 200 without timeline markers fails", func(t *testing.T) {
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedBody}, route{"/NASA", 200, nonFeedBody})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if report.UserHTML.OK || report.UserHTML.Status != 200 || report.UserHTML.Err != "no timeline" {
			t.Errorf("user_html probe = %+v, want failed/200/\"no timeline\"", report.UserHTML)
		}
	})
	t.Run("search 404 fails with status only", func(t *testing.T) {
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedBody}, route{"/NASA", 200, timelineBody}, route{"/search", 404, ""})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA", IncludeSearch: true})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if report.Search.OK || report.Search.Status != 404 || report.Search.Err != "" {
			t.Errorf("search probe = %+v, want failed/404/empty err", report.Search)
		}
	})
	t.Run("list 404 fails with status only", func(t *testing.T) {
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedBody}, route{"/NASA", 200, timelineBody}, route{"/i/lists/", 404, ""})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA", ListID: "42"})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if report.List.OK || report.List.Status != 404 || report.List.Err != "" {
			t.Errorf("list probe = %+v, want failed/404/empty err", report.List)
		}
	})
	t.Run("redirect is not ok", func(t *testing.T) {
		// httpx does not follow redirects: a 3xx means the base URL is
		// misconfigured (e.g. http vs https), which deserves its own reason.
		srv, _ := newFakeNitter(t, route{"/NASA/rss", 301, ""}, route{"/NASA", 200, timelineBody})
		report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
		if err != nil {
			t.Fatalf("TestInstance: %v", err)
		}
		if report.RSS.OK || report.RSS.Status != 301 || report.RSS.Err != "redirect" {
			t.Errorf("rss probe = %+v, want failed/301/\"redirect\"", report.RSS)
		}
	})
}

func TestTestInstanceStatusReasonMapping(t *testing.T) {
	// Pins the transportProbe mapping: unambiguous statuses survive as
	// numbers, ambiguous ones degrade to stable short tokens.
	for _, tc := range []struct {
		name       string
		status     int
		wantStatus int
		wantErr    string
	}{
		{"403 is a challenge", 403, 0, "http 401/403"},
		{"429 keeps its status", 429, 429, ""},
		{"500 is unreachable", 500, 0, "unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newFakeNitter(t, route{"/NASA/rss", tc.status, ""}, route{"/NASA", 200, timelineBody})
			report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
			if err != nil {
				t.Fatalf("TestInstance: %v", err)
			}
			if report.RSS.Status != tc.wantStatus || report.RSS.Err != tc.wantErr || report.RSS.OK {
				t.Errorf("rss probe = %+v, want failed status %d err %q", report.RSS, tc.wantStatus, tc.wantErr)
			}
		})
	}
}

func TestTestInstanceSkipsDisabledProbes(t *testing.T) {
	srv, rec := newFakeNitter(t,
		route{"/NASA/rss", 200, rssFeedBody},
		route{"/NASA", 200, timelineBody},
		route{"/search", 404, ""},
		route{"/i/lists/", 404, ""},
	)
	report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL, appapi.TestOptions{User: "NASA"})
	if err != nil {
		t.Fatalf("TestInstance: %v", err)
	}
	if report.Search != (nitter.Probe{}) || report.List != (nitter.Probe{}) {
		t.Errorf("search/list = %+v/%+v, want zero probes when disabled", report.Search, report.List)
	}
	want := []string{"/NASA/rss", "/NASA"}
	if got := rec.requests(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v (no search/list requests)", got, want)
	}
}

func TestTestInstanceNormalizesTrailingSlash(t *testing.T) {
	srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedBody}, route{"/NASA", 200, timelineBody})
	report, err := newProbeClient(t).TestInstance(context.Background(), srv.URL+"/", appapi.TestOptions{User: "NASA"})
	if err != nil {
		t.Fatalf("TestInstance: %v", err)
	}
	if report.URL != srv.URL {
		t.Errorf("report.URL = %q, want trailing slash trimmed to %q", report.URL, srv.URL)
	}
	if !report.RSS.OK {
		t.Errorf("rss probe = %+v, want ok (request used the normalized path)", report.RSS)
	}
}

func TestTestInstanceInvalidBaseURLIsKindInvalidArg(t *testing.T) {
	c := newProbeClient(t)
	for _, raw := range []string{"", "   ", "example.com", "ftp://example.com", "http://exa\x7fample.com"} {
		report, err := c.TestInstance(context.Background(), raw, appapi.TestOptions{User: "NASA"})
		if err == nil {
			t.Fatalf("TestInstance(%q) = %+v, want error", raw, report)
		}
		if report != (nitter.InstanceReport{}) {
			t.Errorf("TestInstance(%q) report = %+v, want zero value on error", raw, report)
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
			t.Fatalf("TestInstance(%q) err = %v (%T), want *nitter.Error KindInvalidArg", raw, err, err)
		}
		// Redaction contract: the error must not echo the rejected input.
		if probe := strings.TrimSpace(raw); probe != "" && strings.Contains(err.Error(), probe) {
			t.Errorf("TestInstance(%q) err = %v, want it to not echo the URL", raw, err)
		}
	}
}

func TestTestInstanceEmptyUserIsKindInvalidArg(t *testing.T) {
	_, err := newProbeClient(t).TestInstance(context.Background(), "http://example.com", appapi.TestOptions{User: ""})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
		t.Fatalf("err = %v (%T), want *nitter.Error KindInvalidArg", err, err)
	}
}

func TestTestInstanceWithoutTransportIsKindLocalState(t *testing.T) {
	c := &appapi.Client{Now: time.Now}
	_, err := c.TestInstance(context.Background(), "http://example.com", appapi.TestOptions{User: "NASA"})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Fatalf("err = %v (%T), want *nitter.Error KindLocalState", err, err)
	}
}

func TestTestInstanceTransportFailureFailsProbesWithoutError(t *testing.T) {
	srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedBody}, route{"/NASA", 200, timelineBody})
	url := srv.URL
	srv.Close() // the instance is now unreachable

	report, err := newProbeClient(t).TestInstance(context.Background(), url, appapi.TestOptions{User: "NASA", IncludeSearch: true})
	if err != nil {
		t.Fatalf("TestInstance = error %v, want nil error (probe failures live in the report)", err)
	}
	for name, p := range map[string]nitter.Probe{
		"rss":       report.RSS,
		"user_html": report.UserHTML,
		"search":    report.Search,
	} {
		if p.OK || p.Status != 0 || p.Err == "" {
			t.Errorf("%s probe = %+v, want failed/status 0/short err", name, p)
		}
	}
}

func TestTestInstanceCanceledContextFailsProbesAsCanceled(t *testing.T) {
	srv, _ := newFakeNitter(t, route{"/NASA/rss", 200, rssFeedBody}, route{"/NASA", 200, timelineBody})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := newProbeClient(t).TestInstance(ctx, srv.URL, appapi.TestOptions{User: "NASA"})
	if err != nil {
		t.Fatalf("TestInstance = error %v, want nil error", err)
	}
	if report.RSS.Err != "canceled" || report.UserHTML.Err != "canceled" {
		t.Errorf("rss/user_html err = %q/%q, want \"canceled\"", report.RSS.Err, report.UserHTML.Err)
	}
}
