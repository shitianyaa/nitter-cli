package appapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/nitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// rssBody builds a Nitter-shaped RSS feed listing the given status ids.
func rssBody(ids ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel>`)
	for _, id := range ids {
		b.WriteString(`<item>` +
			`<guid>https://nitter.example/NASA/status/` + id + `#m</guid>` +
			`<link>https://nitter.example/NASA/status/` + id + `</link>` +
			`<dc:creator>@NASA</dc:creator>` +
			`<title>rss ` + id + `</title>` +
			`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate></item>`)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// htmlPage builds a Nitter-shaped user timeline listing the given status ids
// and carrying a load-more cursor when non-empty.
func htmlPage(ids []string, cursor string) string {
	var b strings.Builder
	b.WriteString(`<div class="timeline">`)
	for _, id := range ids {
		b.WriteString(`<div class="timeline-item">` +
			`<a class="tweet-link" href="/NASA/status/` + id + `"></a>` +
			`<div class="tweet-content">html ` + id + `</div>` +
			`<span class="tweet-date"><a title="Jul 5, 2026 · 9:09 AM UTC">Jul 5, 2026</a></span>` +
			`</div>`)
	}
	b.WriteString(`</div>`)
	if cursor != "" {
		b.WriteString(`<div class="show-more"><a href="/NASA?cursor=` + cursor + `">Load more</a></div>`)
	}
	return b.String()
}

// timelineRoute is one canned answer keyed by the exact request URI
// (path?query) the fake receives.
type timelineRoute struct {
	target string
	status int
	body   string
}

// newTimelineFake serves the canned routes; unknown targets answer 404.
func newTimelineFake(t *testing.T, routes ...timelineRoute) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	byTarget := make(map[string]timelineRoute, len(routes))
	for _, r := range routes {
		byTarget[r.target] = r
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		rec.add(req.URL.RequestURI())
		r, ok := byTarget[req.URL.RequestURI()]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(r.status)
		_, _ = io.WriteString(w, r.body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

// newTimelineClient builds an appapi.Client over the real (fast) httpx
// transport rotating across the given base URLs. The cooldown exceeds any
// test's wall time, so a failed instance is genuinely skipped and rotation
// reaches the next candidate.
func newTimelineClient(t *testing.T, baseURLs ...string) *appapi.Client {
	t.Helper()
	hx, err := httpx.New(httpx.Options{RetryAttempts: -1, RetryDelay: -1, MinInterval: -1})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	instances := make([]nitter.Instance, len(baseURLs))
	for i, u := range baseURLs {
		instances[i] = nitter.Instance{URL: u}
	}
	return &appapi.Client{HTTP: hx, Chooser: nitter.NewChooser(instances, time.Minute, nil), Now: time.Now}
}

func TestTimelineRSSPathReturnsTweets(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 200, rssBody("101", "102")},
		timelineRoute{"/NASA", 200, htmlPage([]string{"201"}, "")},
	)
	tweets, instance, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if instance != srv.URL {
		t.Errorf("instance = %q, want the serving instance %q", instance, srv.URL)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2 (RSS is the primary source)", len(tweets))
	}
	if tweets[0].ID != "101" || tweets[1].ID != "102" {
		t.Errorf("ids = %q/%q, want 101/102", tweets[0].ID, tweets[1].ID)
	}
	if tweets[0].Author.Handle != "NASA" {
		t.Errorf("handle = %q, want NASA (dc:creator or feed path)", tweets[0].Author.Handle)
	}
	// The HTML page must never be requested when RSS answers.
	if got := rec.requests(); !slices.Equal(got, []string{"/NASA/rss"}) {
		t.Errorf("requests = %v, want only the RSS fetch", got)
	}
}

// rssBodyMixed builds a Nitter-shaped RSS feed mixing self-authored items
// with author/creator overrides — the author-mismatch fixture.
func rssBodyMixed(items ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel>`)
	for _, it := range items {
		b.WriteString(it)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// rssMixedItem builds one RSS item whose status URL carries the given account
// and whose dc:creator carries the given creator ("@" optional; empty creator
// omits the element).
func rssMixedItem(account, creator, id string) string {
	c := ""
	if creator != "" {
		c = `<dc:creator>@` + creator + `</dc:creator>`
	}
	return `<item>` +
		`<guid>https://nitter.example/` + account + `/status/` + id + `#m</guid>` +
		`<link>https://nitter.example/` + account + `/status/` + id + `</link>` +
		c +
		`<title>rss ` + id + `</title>` +
		`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate></item>`
}

// TestTimelineRSSFlagsRepostsByAuthorMismatch: Nitter user RSS carries no
// repost marker, but a retweeted item's guid/link and dc:creator point at the
// ORIGINAL author (verified live: requesting NASA yields NASAhistory status
// URLs). An author handle differing from the requested handle — case-
// insensitively — therefore flags IsRetweet on the user-timeline RSS path
// only; a matching or absent handle leaves the flag untouched, and RepostedBy
// stays empty (the feed names no reposter).
func TestTimelineRSSFlagsRepostsByAuthorMismatch(t *testing.T) {
	feed := rssBodyMixed(
		rssMixedItem("NASA", "NASA", "101"),               // self-authored
		rssMixedItem("NASA", "nasa", "102"),               // case-insensitive self match
		rssMixedItem("NASAhistory", "NASAhistory", "103"), // retweeted: original author
		rssMixedItem("NASA", "", "104"),                   // creator-less: flag untouched
	)
	srv, _ := newTimelineFake(t, timelineRoute{"/NASA/rss", 200, feed})
	tweets, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(tweets) != 4 {
		t.Fatalf("tweets = %d, want 4", len(tweets))
	}
	if tweets[0].ID != "101" || tweets[0].IsRetweet {
		t.Errorf("tweet 0 = %v, want self-authored 101 with IsRetweet false", tweets[0])
	}
	if tweets[1].ID != "102" || tweets[1].IsRetweet {
		t.Errorf("tweet 1 = %v, want the case-insensitive self match 102 with IsRetweet false", tweets[1])
	}
	if tweets[2].ID != "103" || !tweets[2].IsRetweet {
		t.Errorf("tweet 2 = %v, want the foreign-author item 103 flagged IsRetweet", tweets[2])
	}
	if tweets[2].Author.Handle != "NASAhistory" {
		t.Errorf("tweet 2 handle = %q, want the original author NASAhistory", tweets[2].Author.Handle)
	}
	if tweets[2].RepostedBy != "" {
		t.Errorf("tweet 2 repostedBy = %q, want empty (RSS names no reposter)", tweets[2].RepostedBy)
	}
	if tweets[3].ID != "104" || tweets[3].IsRetweet {
		t.Errorf("tweet 3 = %v, want the creator-less item 104 with IsRetweet untouched (false)", tweets[3])
	}
}

func TestTimelineLimitTruncatesRSS(t *testing.T) {
	srv, _ := newTimelineFake(t, timelineRoute{"/NASA/rss", 200, rssBody("101", "102", "103")})
	tweets, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2", len(tweets))
	}
}

func TestTimelineValidHandles(t *testing.T) {
	handles := []string{"a", "NASA", "nasa_1", strings.Repeat("x", 15)}
	var routes []timelineRoute
	for _, h := range handles {
		routes = append(routes, timelineRoute{"/" + h + "/rss", 200, rssBody("101")})
	}
	srv, _ := newTimelineFake(t, routes...)
	for _, handle := range handles {
		if _, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), handle, appapi.PageOptions{}); err != nil {
			t.Errorf("Timeline(%q) = error %v, want success", handle, err)
		}
	}
}

// TestTimelineInvalidHandleIsInvalidArgBeforeNetwork pins the validation
// order: a zero-instance chooser would answer "no instances configured", so
// a KindInvalidArg proves the handle was rejected before any rotation.
func TestTimelineInvalidHandleIsInvalidArgBeforeNetwork(t *testing.T) {
	c := newTimelineClient(t) // no instances at all
	for _, handle := range []string{"", "NASA!", "na sa", "@nasa", strings.Repeat("x", 16)} {
		tweets, instance, err := c.Timeline(context.Background(), handle, appapi.PageOptions{})
		if err == nil {
			t.Fatalf("Timeline(%q) = (%v, %q), want an error", handle, tweets, instance)
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) {
			t.Fatalf("Timeline(%q) error = %T (%v), want *nitter.Error", handle, err, err)
		}
		if terr.Kind != nitter.KindInvalidArg {
			t.Errorf("Timeline(%q) Kind = %v, want %v (before any network)", handle, terr.Kind, nitter.KindInvalidArg)
		}
	}
}

func TestTimelineRSSFailureFallsBackToHTML(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 500, "boom"},
		timelineRoute{"/NASA", 200, htmlPage([]string{"201"}, "")},
	)
	tweets, instance, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if instance != srv.URL || len(tweets) != 1 || tweets[0].ID != "201" {
		t.Fatalf("got (%v, %q, %v), want the HTML tweet from the same instance", tweets, instance, err)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/NASA/rss", "/NASA"}) {
		t.Errorf("requests = %v, want the RSS attempt then the HTML fallback", got)
	}
}

func TestTimelineEmptyRSSTriggersHTMLFallback(t *testing.T) {
	// R15: an RSS feed that succeeds but yields nothing ALSO triggers the
	// HTML user-page fallback.
	srv, rec := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 200, rssBody()},
		timelineRoute{"/NASA", 200, htmlPage([]string{"201"}, "")},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(tweets) != 1 || tweets[0].ID != "201" {
		t.Fatalf("tweets = %v, want the HTML tweet", tweets)
	}
	if got := rec.requests(); !slices.Equal(got, []string{"/NASA/rss", "/NASA"}) {
		t.Errorf("requests = %v, want the RSS attempt then the HTML fallback", got)
	}
}

func TestTimelineBothEmptyYieldsEmptySliceWithoutError(t *testing.T) {
	srv, _ := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 200, rssBody()},
		timelineRoute{"/NASA", 200, htmlPage(nil, "")},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Timeline = error %v, want success with an empty slice", err)
	}
	if len(tweets) != 0 {
		t.Fatalf("tweets = %v, want empty", tweets)
	}
}

func TestTimelineBothStagesFailReturnsClassifiedError(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 500, "boom"},
		timelineRoute{"/NASA", 503, "down"},
	)
	tweets, instance, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err == nil {
		t.Fatalf("Timeline = (%v, %q), want an error", tweets, instance)
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("err = %v (%T), want KindUnavailable", err, err)
	}
	// Retries are disabled: exactly one RSS attempt and one HTML attempt.
	if got := rec.requests(); !slices.Equal(got, []string{"/NASA/rss", "/NASA"}) {
		t.Errorf("requests = %v, want one attempt per stage", got)
	}
}

func TestTimelineChallengePropagates(t *testing.T) {
	login := `<form action="/login"><input name="username"/></form>`
	srv, _ := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 500, "boom"},
		timelineRoute{"/NASA", 200, login},
	)
	_, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindChallenge {
		t.Fatalf("err = %v (%T), want KindChallenge from ClassifyPage", err, err)
	}
}

func TestTimelinePaginationBoundByMaxPages(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 500, "boom"},
		timelineRoute{"/NASA", 200, htmlPage([]string{"201"}, "c1")},
		timelineRoute{"/NASA?cursor=c1", 200, htmlPage([]string{"202"}, "c2")},
		timelineRoute{"/NASA?cursor=c2", 200, htmlPage([]string{"203"}, "c3")},
		timelineRoute{"/NASA?cursor=c3", 200, htmlPage([]string{"204"}, "")},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{MaxPages: 2})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2 (two pages worth)", len(tweets))
	}
	htmlFetches := 0
	for _, r := range rec.requests() {
		if strings.HasPrefix(r, "/NASA") && !strings.HasSuffix(r, "/rss") {
			htmlFetches++
		}
	}
	if htmlFetches != 2 {
		t.Errorf("html fetches = %d, want exactly 2 (bounded by MaxPages)", htmlFetches)
	}
}

func TestTimelineLimitStopsPaginationEarly(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 500, "boom"},
		timelineRoute{"/NASA", 200, htmlPage([]string{"201"}, "c1")},
		timelineRoute{"/NASA?cursor=c1", 200, htmlPage([]string{"202"}, "c2")},
		timelineRoute{"/NASA?cursor=c2", 200, htmlPage([]string{"203"}, "")},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2", len(tweets))
	}
	if got := rec.requests(); len(got) != 3 { // rss + 2 html pages
		t.Errorf("requests = %v, want the pagination to stop once Limit is met", got)
	}
}

func TestTimelineNoInstancesConfigured(t *testing.T) {
	c := newTimelineClient(t)
	tweets, instance, err := c.Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err == nil {
		t.Fatalf("Timeline = (%v, %q), want an error", tweets, instance)
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("err = %v (%T), want KindUnavailable", err, err)
	}
	if !strings.Contains(err.Error(), "no instances configured") {
		t.Errorf("err = %v, want the chooser's no-instances message", err)
	}
}

func TestTimelineAllInstancesExhaustedReportsLastError(t *testing.T) {
	srv1, rec1 := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 500, "boom"},
		timelineRoute{"/NASA", 503, "down"},
	)
	srv2, rec2 := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 404, "gone"},
		timelineRoute{"/NASA", 404, "gone"},
	)
	_, _, err := newTimelineClient(t, srv1.URL, srv2.URL).Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err == nil {
		t.Fatal("Timeline = nil error, want the last instance failure")
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v (%T), want *nitter.Error", err, err)
	}
	for _, rec := range []*recorder{rec1, rec2} {
		if got := rec.requests(); !slices.Equal(got, []string{"/NASA/rss", "/NASA"}) {
			t.Errorf("requests = %v, want both stages on every instance", got)
		}
	}
	// The last error is the last instance's classification: HTTP 404 →
	// KindNotFound (srv1's 503 stays behind).
	if terr.Kind != nitter.KindNotFound || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want the last instance's classification (404 / KindNotFound)", err)
	}
}

// TestTimelineOversizeRSSBodyFallsBackToHTML proves the fetch-layer body cap
// composes with the fallback: a giant RSS body fails as KindMalformed, which
// is a plain RSS failure for Timeline — the HTML page still answers.
func TestTimelineOversizeRSSBodyFallsBackToHTML(t *testing.T) {
	rec := &recorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("/NASA/rss", func(w http.ResponseWriter, req *http.Request) {
		rec.add(req.URL.RequestURI())
		w.WriteHeader(200)
		_, _ = io.WriteString(w, strings.Repeat("x", 4096)) // far over the tiny cap
	})
	mux.HandleFunc("/NASA", func(w http.ResponseWriter, req *http.Request) {
		rec.add(req.URL.RequestURI())
		w.WriteHeader(200)
		_, _ = io.WriteString(w, htmlPage([]string{"201"}, ""))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	hx, err := httpx.New(httpx.Options{MaxBodyBytes: 1024, RetryAttempts: -1, RetryDelay: -1, MinInterval: -1})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	c := &appapi.Client{HTTP: hx, Chooser: nitter.NewChooser([]nitter.Instance{{URL: srv.URL}}, 0, nil), Now: time.Now}
	tweets, _, err := c.Timeline(context.Background(), "NASA", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(tweets) != 1 || tweets[0].ID != "201" {
		t.Fatalf("tweets = %v, want the HTML fallback tweet", tweets)
	}
}

func TestTimelineRespectsContextCancellation(t *testing.T) {
	srv, _ := newTimelineFake(t,
		timelineRoute{"/NASA/rss", 500, "boom"},
		timelineRoute{"/NASA", 503, "down"},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the fetch must notice before the second instance attempt
	_, _, err := newTimelineClient(t, srv.URL, srv.URL).Timeline(ctx, "NASA", appapi.PageOptions{})
	if err == nil {
		t.Fatal("Timeline = nil error, want the cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
}
