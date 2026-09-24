package appapi_test

// Search acquisition tests. The search endpoint shares the timeline pipeline
// (ClassifyPage → ParseTimeline → cursor pagination) but hits
// /search?f=<mode>&q=<query> and never involves RSS. Query semantics are the
// pass-through contract: the CLI does not transform the query, appapi only
// URL-escapes it (#tag, from:user and plain phrases all go over the wire
// as-is). PageOptions.Sort picks the feed — "" / "latest" → f=tweets,
// "top" → f=top — and every other value is a KindInvalidArg.

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// searchRouteTarget is the exact request URI of the first search page for
// one query — the fake answers are keyed by these.
func searchRouteTarget(query string) string {
	return "/search?f=tweets&q=" + url.QueryEscape(query)
}

func TestSearchReturnsTweetsWithEscapedQuery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		query     string
		wireQuery string // the URL-escaped form the fake must receive
	}{
		{name: "hashtag passes through escaped", query: "#artemis", wireQuery: "%23artemis"},
		{name: "from:user passes through escaped", query: "from:nasa", wireQuery: "from%3Anasa"},
		{name: "plain phrase", query: "moon landing", wireQuery: "moon+landing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := newTimelineFake(t,
				timelineRoute{"/search?f=tweets&q=" + tc.wireQuery, 200, htmlPage([]string{"301", "302"}, ""), nil},
			)
			tweets, instance, err := newTimelineClient(t, srv.URL).Search(context.Background(), tc.query, appapi.PageOptions{})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if instance != srv.URL {
				t.Errorf("instance = %q, want the serving instance %q", instance, srv.URL)
			}
			if len(tweets) != 2 {
				t.Fatalf("tweets = %d, want 2", len(tweets))
			}
			if tweets[0].ID != "301" || tweets[1].ID != "302" {
				t.Errorf("ids = %q/%q, want 301/302", tweets[0].ID, tweets[1].ID)
			}
			want := []string{"/search?f=tweets&q=" + tc.wireQuery}
			if got := rec.requests(); !slices.Equal(got, want) {
				t.Errorf("requests = %v, want the single escaped search fetch %v", got, want)
			}
		})
	}
}

func TestSearchPaginationBoundedByMaxPages(t *testing.T) {
	q := searchRouteTarget("#artemis")
	srv, rec := newTimelineFake(t,
		timelineRoute{q, 200, htmlPage([]string{"301"}, "c1"), nil},
		timelineRoute{q + "&cursor=c1", 200, htmlPage([]string{"302"}, "c2"), nil},
		timelineRoute{q + "&cursor=c2", 200, htmlPage([]string{"303"}, "c3"), nil},
		timelineRoute{q + "&cursor=c3", 200, htmlPage([]string{"304"}, ""), nil},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "#artemis", appapi.PageOptions{MaxPages: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2 (two pages worth)", len(tweets))
	}
	fetches := 0
	for _, r := range rec.requests() {
		if strings.HasPrefix(r, "/search?") {
			fetches++
		}
	}
	if fetches != 2 {
		t.Errorf("search fetches = %d, want exactly 2 (bounded by MaxPages)", fetches)
	}
}

// A repeated search cursor ends the scan instead of re-requesting the same
// page until the page budget runs out.
func TestSearchCursorLoopIsBounded(t *testing.T) {
	q := searchRouteTarget("#artemis")
	srv, rec := newTimelineFake(t,
		timelineRoute{q, 200, htmlPage([]string{"301"}, "same"), nil},
		timelineRoute{q + "&cursor=same", 200, htmlPage([]string{"302"}, "same"), nil},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "#artemis", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2 (two pages, then the repeated cursor stops the scan)", len(tweets))
	}
	fetches := 0
	for _, r := range rec.requests() {
		if strings.HasPrefix(r, "/search?") {
			fetches++
		}
	}
	if fetches != 2 {
		t.Errorf("search fetches = %d, want exactly 2 (the loop guard must stop the repeated cursor)", fetches)
	}
}

func TestSearchLimitStopsPaginationEarly(t *testing.T) {
	q := searchRouteTarget("moon")
	srv, rec := newTimelineFake(t,
		timelineRoute{q, 200, htmlPage([]string{"301"}, "c1"), nil},
		timelineRoute{q + "&cursor=c1", 200, htmlPage([]string{"302", "303", "304"}, "c2"), nil},
		timelineRoute{q + "&cursor=c2", 200, htmlPage([]string{"305"}, ""), nil},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "moon", appapi.PageOptions{Limit: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tweets) != 3 {
		t.Fatalf("tweets = %d, want 3", len(tweets))
	}
	if got := rec.requests(); len(got) != 2 {
		t.Errorf("requests = %v, want pagination to stop once Limit is met", got)
	}
}

// searchTopTarget is the exact request URI of the first page of a TOP-sorted
// search (PageOptions.Sort = "top" → f=top on the wire).
func searchTopTarget(query string) string {
	return "/search?f=top&q=" + url.QueryEscape(query)
}

// TestSearchSortTopRequestsTopFeed pins the --sort top wire shape: the feed
// parameter becomes f=top instead of f=tweets, and the query escaping is
// unchanged.
func TestSearchSortTopRequestsTopFeed(t *testing.T) {
	srv, rec := newTimelineFake(t,
		timelineRoute{searchTopTarget("#artemis"), 200, htmlPage([]string{"301", "302"}, ""), nil},
	)
	tweets, instance, err := newTimelineClient(t, srv.URL).Search(context.Background(), "#artemis", appapi.PageOptions{Sort: "top"})
	if err != nil {
		t.Fatalf("Search(Sort: top): %v", err)
	}
	if instance != srv.URL {
		t.Errorf("instance = %q, want the serving instance %q", instance, srv.URL)
	}
	if len(tweets) != 2 {
		t.Fatalf("tweets = %d, want 2", len(tweets))
	}
	want := []string{"/search?f=top&q=%23artemis"}
	if got := rec.requests(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want the top-feed search fetch %v", got, want)
	}
}

// TestSearchSortTopPaginationKeepsTopFeed pins that the chosen ordering
// survives pagination: every cursor page repeats f=top rather than falling
// back to the default feed.
func TestSearchSortTopPaginationKeepsTopFeed(t *testing.T) {
	q := searchTopTarget("moon")
	srv, rec := newTimelineFake(t,
		timelineRoute{q, 200, htmlPage([]string{"301"}, "c1"), nil},
		timelineRoute{q + "&cursor=c1", 200, htmlPage([]string{"302"}, "c2"), nil},
		timelineRoute{q + "&cursor=c2", 200, htmlPage([]string{"303"}, ""), nil},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "moon", appapi.PageOptions{Sort: "top"})
	if err != nil {
		t.Fatalf("Search(Sort: top): %v", err)
	}
	if len(tweets) != 3 {
		t.Fatalf("tweets = %d, want 3 (three pages)", len(tweets))
	}
	want := []string{
		q,
		q + "&cursor=c1",
		q + "&cursor=c2",
	}
	if got := rec.requests(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want every page to keep f=top (%v)", got, want)
	}
}

// TestSearchSortLatestMatchesDefault pins the compatibility rule: an explicit
// "latest" (in any case, with surrounding whitespace) is byte-identical to
// the omitted Sort — f=tweets.
func TestSearchSortLatestMatchesDefault(t *testing.T) {
	for _, sort := range []string{"", "latest", "LATEST", " Latest "} {
		srv, rec := newTimelineFake(t,
			timelineRoute{searchRouteTarget("moon"), 200, htmlPage([]string{"301"}, ""), nil},
		)
		if _, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "moon", appapi.PageOptions{Sort: sort}); err != nil {
			t.Fatalf("Search(Sort: %q): %v", sort, err)
		}
		want := []string{"/search?f=tweets&q=moon"}
		if got := rec.requests(); !slices.Equal(got, want) {
			t.Errorf("Sort %q: requests = %v, want %v", sort, got, want)
		}
	}
}

// TestSearchInvalidSortIsInvalidArgBeforeNetwork pins both halves of the
// rejection: the value is a KindInvalidArg naming the offending value, and it
// fails before any rotation or request — a zero-instance client would answer
// "no instances configured", so KindInvalidArg proves the sort was checked
// first (and no instance was marked failed).
func TestSearchInvalidSortIsInvalidArgBeforeNetwork(t *testing.T) {
	for _, sort := range []string{"bogus", "hot", "tweets", "Top-1"} {
		t.Run(sort, func(t *testing.T) {
			c := newTimelineClient(t) // no instances at all
			tweets, instance, err := c.Search(context.Background(), "moon", appapi.PageOptions{Sort: sort})
			if err == nil {
				t.Fatalf("Search(Sort: %q) = (%v, %q), want an error", sort, tweets, instance)
			}
			var terr *nitter.Error
			if !errors.As(err, &terr) {
				t.Fatalf("Search(Sort: %q) error = %T (%v), want *nitter.Error", sort, err, err)
			}
			if terr.Kind != nitter.KindInvalidArg {
				t.Errorf("Search(Sort: %q) Kind = %v, want %v", sort, terr.Kind, nitter.KindInvalidArg)
			}
			if !strings.Contains(err.Error(), "invalid sort") || !strings.Contains(err.Error(), "latest or top") {
				t.Errorf("Search(Sort: %q) error = %q, want it to name the accepted values", sort, err.Error())
			}
		})
	}

	// The same rejection with an instance configured: nothing is fetched.
	srv, rec := newTimelineFake(t,
		timelineRoute{searchRouteTarget("moon"), 200, htmlPage([]string{"301"}, ""), nil},
	)
	if _, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "moon", appapi.PageOptions{Sort: "bogus"}); err == nil {
		t.Fatal("Search(Sort: bogus) = nil error, want KindInvalidArg")
	}
	if got := rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (the sort is validated before any network)", got)
	}
}

func TestSearchEmptyPageYieldsEmptySliceWithoutError(t *testing.T) {
	srv, _ := newTimelineFake(t,
		timelineRoute{searchRouteTarget("ghost"), 200, htmlPage(nil, ""), nil},
	)
	tweets, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "ghost", appapi.PageOptions{})
	if err != nil {
		t.Fatalf("Search = error %v, want success with an empty slice", err)
	}
	if len(tweets) != 0 {
		t.Fatalf("tweets = %v, want empty", tweets)
	}
}

// TestSearchEmptyQueryIsInvalidArgBeforeNetwork pins the validation order:
// a zero-instance chooser would answer "no instances configured", so a
// KindInvalidArg proves the query was rejected before any rotation.
func TestSearchEmptyQueryIsInvalidArgBeforeNetwork(t *testing.T) {
	c := newTimelineClient(t) // no instances at all
	for _, query := range []string{"", "   ", "\t\n"} {
		tweets, instance, err := c.Search(context.Background(), query, appapi.PageOptions{})
		if err == nil {
			t.Fatalf("Search(%q) = (%v, %q), want an error", query, tweets, instance)
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) {
			t.Fatalf("Search(%q) error = %T (%v), want *nitter.Error", query, err, err)
		}
		if terr.Kind != nitter.KindInvalidArg {
			t.Errorf("Search(%q) Kind = %v, want %v (before any network)", query, terr.Kind, nitter.KindInvalidArg)
		}
	}
}

func TestSearchChallengePropagates(t *testing.T) {
	login := `<form action="/login"><input name="username"/></form>`
	srv, _ := newTimelineFake(t,
		timelineRoute{searchRouteTarget("x"), 200, login, nil},
	)
	_, _, err := newTimelineClient(t, srv.URL).Search(context.Background(), "x", appapi.PageOptions{})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindChallenge {
		t.Fatalf("err = %v (%T), want KindChallenge from ClassifyPage", err, err)
	}
}

func TestSearchAllInstancesExhaustedReportsLastError(t *testing.T) {
	q := searchRouteTarget("x")
	srv1, _ := newTimelineFake(t, timelineRoute{q, 503, "down", nil})
	srv2, _ := newTimelineFake(t, timelineRoute{q, 404, "gone", nil})
	_, _, err := newTimelineClient(t, srv1.URL, srv2.URL).Search(context.Background(), "x", appapi.PageOptions{})
	if err == nil {
		t.Fatal("Search = nil error, want the last instance failure")
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v (%T), want *nitter.Error", err, err)
	}
	// The last error is the last instance's classification: HTTP 404 →
	// KindNotFound (srv1's 503 stays behind).
	if terr.Kind != nitter.KindNotFound {
		t.Errorf("err = %v, want the last instance's classification (404 / KindNotFound)", err)
	}
}

func TestSearchRespectsContextCancellation(t *testing.T) {
	srv, _ := newTimelineFake(t, timelineRoute{searchRouteTarget("x"), 503, "down", nil})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the fetch must notice before the second instance attempt
	_, _, err := newTimelineClient(t, srv.URL, srv.URL).Search(ctx, "x", appapi.PageOptions{})
	if err == nil {
		t.Fatal("Search = nil error, want the cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
}

func TestSearchNoInstancesConfigured(t *testing.T) {
	c := newTimelineClient(t)
	tweets, instance, err := c.Search(context.Background(), "x", appapi.PageOptions{})
	if err == nil {
		t.Fatalf("Search = (%v, %q), want an error", tweets, instance)
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("err = %v (%T), want KindUnavailable", err, err)
	}
	if !strings.Contains(err.Error(), "no instances configured") {
		t.Errorf("err = %v, want the chooser's no-instances message", err)
	}
}
