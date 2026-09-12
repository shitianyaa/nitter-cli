package media

// Resolver tests: strategy ordering and the empty/error aggregation contract,
// ref parsing and canonical stamping, request URL shapes and headers, quality
// application, and the https-only / dedup rules. The transport seam is an
// unexported fetch func injected by these tests (the httpx.Options doer hook
// is unexported in package httpx, so out-of-package tests cannot reach it;
// mirroring httpx's own style, the seam lives behind an unexported field).
// HTTP classification itself (retry/pacing/kinds) is httpx's tested contract;
// the fake reproduces only its (body, status, classified-error) surface.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// readFixture loads a committed testdata file (the plugin-shaped payloads).
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// fakeResp is one canned fetch outcome keyed by exact request URL.
type fakeResp struct {
	body   []byte
	status int
	err    error
}

// fakeFetch stands in for the httpx client: it records every requested URL
// and its headers, answers from a route table, and classifies unknown URLs
// as HTTP 404 (the same shape httpx produces for a missing resource). The
// post* fields are the POST counterpart (xdown's ajaxSearch form).
type fakeFetch struct {
	routes map[string]fakeResp
	calls  []string
	sent   []map[string]string

	postRoutes map[string]fakeResp
	postCalls  []string
	postBodies []string
	postSent   []map[string]string
}

func (f *fakeFetch) get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	f.calls = append(f.calls, url)
	f.sent = append(f.sent, headers)
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	r, ok := f.routes[url]
	if !ok {
		return nil, 0, twitter.Errorf(twitter.KindNotFound, "httpx.Get", "instance returned HTTP 404")
	}
	if r.err != nil {
		return nil, 0, r.err
	}
	return r.body, r.status, nil
}

func (f *fakeFetch) post(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, int, error) {
	f.postCalls = append(f.postCalls, url)
	f.postBodies = append(f.postBodies, string(body))
	f.postSent = append(f.postSent, headers)
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	r, ok := f.postRoutes[url]
	if !ok {
		return nil, 0, twitter.Errorf(twitter.KindNotFound, "httpx.Post", "instance returned HTTP 404")
	}
	if r.err != nil {
		return nil, 0, r.err
	}
	return r.body, r.status, nil
}

// newTestResolver builds a Resolver whose fetch is the fake. routes maps
// exact request URLs to canned GET responses, postRoutes to canned POST
// responses; anything else answers 404.
func newTestResolver(routes map[string]fakeResp) (*Resolver, *fakeFetch) {
	fake := &fakeFetch{routes: routes}
	return &Resolver{Now: time.Now, fetch: fake.get, fetchPost: fake.post}, fake
}

func mustRef(t *testing.T, s string) StatusRef {
	t.Helper()
	ref, err := ParseStatusRef(s)
	if err != nil {
		t.Fatalf("ParseStatusRef(%q): %v", s, err)
	}
	return ref
}

const (
	statusURL100 = "https://x.com/nasa/status/2070000000000000100"
	id100        = "2070000000000000100"
)

var (
	fxURL100  = "https://api.fxtwitter.com/nasa/status/" + id100
	vxURL100  = "https://api.vxtwitter.com/nasa/status/" + id100
	syndURL10 = "https://cdn.syndication.twimg.com/tweet-result?id=" + id100 + "&token=x"
)

// TestParseStatusRefDelegatesToAppapi pins the wrapper: it must accept the
// same shapes as the canonical parser and classify rejects as KindInvalidArg
// (no duplicated regex logic, no divergent behavior).
func TestParseStatusRefDelegatesToAppapi(t *testing.T) {
	ref, err := ParseStatusRef("https://x.com/nasa/status/123/video/1")
	if err != nil || ref.ID != "123" || ref.User != "nasa" {
		t.Errorf("ParseStatusRef(url) = (%+v, %v), want {123 nasa}, nil", ref, err)
	}
	ref, err = ParseStatusRef("2070000000000000009")
	if err != nil || ref.ID != "2070000000000000009" || ref.User != "" {
		t.Errorf("ParseStatusRef(bare id) = (%+v, %v), want user-less ref, nil", ref, err)
	}
	_, err = ParseStatusRef("not a ref")
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindInvalidArg {
		t.Errorf("ParseStatusRef(garbage) err = %v, want KindInvalidArg", err)
	}
}

// TestStatusRefString pins the canonical permalink used to stamp
// MediaResolution.Ref: the x.com form with the user segment, or the /i/
// route when the ref carried no user.
func TestStatusRefString(t *testing.T) {
	if got := (StatusRef{ID: "123", User: "nasa"}).String(); got != "https://x.com/nasa/status/123" {
		t.Errorf("StatusRef.String() = %q", got)
	}
	if got := (StatusRef{ID: "123"}).String(); got != "https://x.com/i/status/123" {
		t.Errorf("user-less StatusRef.String() = %q", got)
	}
}

// TestSelectVariant pins the quality tiers over video variants: high takes
// the highest bitrate (plugin semantics: ties go to the LATER variant),
// medium the upper median of the non-zero bitrates, low the lowest non-zero;
// zero-bitrate entries never win a medium/low selection, and an all-zero
// list degrades to the high rule so a URL is always selected.
func TestSelectVariant(t *testing.T) {
	v0a := twitter.MediaVariant{URL: "u0a", Bitrate: 0}
	v0b := twitter.MediaVariant{URL: "u0b", Bitrate: 0}
	v100a := twitter.MediaVariant{URL: "u100a", Bitrate: 100}
	v100b := twitter.MediaVariant{URL: "u100b", Bitrate: 100}
	v500 := twitter.MediaVariant{URL: "u500", Bitrate: 500000}
	v832 := twitter.MediaVariant{URL: "u832", Bitrate: 832000}
	v2176 := twitter.MediaVariant{URL: "u2176", Bitrate: 2176000}

	cases := []struct {
		name     string
		variants []twitter.MediaVariant
		quality  string
		wantURL  string
		wantOK   bool
	}{
		{"empty list", nil, "high", "", false},
		{"high picks highest", []twitter.MediaVariant{v0a, v832, v2176}, "high", "u2176", true},
		{"high ties go to the later variant", []twitter.MediaVariant{v100a, v100b}, "high", "u100b", true},
		{"high over all-zero degrades to last", []twitter.MediaVariant{v0a, v0b}, "high", "u0b", true},
		{"medium picks the true median", []twitter.MediaVariant{v500, v832, v2176}, "medium", "u832", true},
		{"medium ignores zero-bitrate", []twitter.MediaVariant{v0a, v832, v2176}, "medium", "u2176", true},
		{"medium picks middle of four", []twitter.MediaVariant{v100a, v500, v832, v2176}, "medium", "u832", true},
		{"medium over all-zero degrades to high", []twitter.MediaVariant{v0a, v0b}, "medium", "u0b", true},
		{"low picks lowest non-zero", []twitter.MediaVariant{v0a, v832, v2176}, "low", "u832", true},
		{"low picks the true minimum", []twitter.MediaVariant{v500, v832, v2176}, "low", "u500", true},
		{"low ties go to the first variant", []twitter.MediaVariant{v100a, v100b}, "low", "u100a", true},
		{"low over all-zero degrades to high", []twitter.MediaVariant{v0a, v0b}, "low", "u0b", true},
		{"unknown quality behaves as high", []twitter.MediaVariant{v0a, v832, v2176}, "bogus", "u2176", true},
		{"empty quality behaves as high", []twitter.MediaVariant{v0a, v832, v2176}, "", "u2176", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := selectVariant(tc.variants, tc.quality)
			if ok != tc.wantOK || got.URL != tc.wantURL {
				t.Errorf("selectVariant(%d variants, %q) = (%q, %v), want (%q, %v)",
					len(tc.variants), tc.quality, got.URL, ok, tc.wantURL, tc.wantOK)
			}
		})
	}
}

// TestResolveStatusStopsAtFirstSuccessWithStrategiesInOrder: fx returns a
// text-only payload (empty) so the resolver moves on to vx, which succeeds;
// syndication must never be requested and every entry carries source "vx".
func TestResolveStatusStopsAtFirstSuccessWithStrategiesInOrder(t *testing.T) {
	routes := map[string]fakeResp{
		fxURL100: {body: readFixture(t, "fx_text_only.json"), status: 200},
		vxURL100: {body: readFixture(t, "vx_status.json"), status: 200},
	}
	r, fake := newTestResolver(routes)
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyFx, StrategyVx, StrategySyndication},
	})
	if err != nil {
		t.Fatalf("ResolveStatus: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("resolutions = %d, want 3 (image, video, gif)", len(res))
	}
	for _, m := range res {
		if m.Source != "vx" {
			t.Errorf("source = %q, want vx", m.Source)
		}
		if m.Ref != statusURL100 {
			t.Errorf("ref = %q, want %q", m.Ref, statusURL100)
		}
	}
	wantKinds := []string{"image", "video", "gif"}
	for i, kind := range wantKinds {
		if res[i].Kind != kind {
			t.Errorf("res[%d].kind = %q, want %q", i, res[i].Kind, kind)
		}
	}
	if res[0].URL != "https://pbs.twimg.com/media/Gy1abc.jpg?name=orig" {
		t.Errorf("image URL = %q, want pbs orig rewrite", res[0].URL)
	}
	if res[1].URL != "https://video.twimg.com/ext_tw_video/200/pu/vid/720x1280/2176.mp4" {
		t.Errorf("video URL = %q, want the direct mp4", res[1].URL)
	}
	if len(fake.calls) != 2 {
		t.Errorf("requests = %v, want fx then vx only (syndication never reached)", fake.calls)
	}
}

// TestResolveStatusRequestURLShapesAndHeaders pins the backend endpoints and
// the JSON request headers, including the /i/ user segment for user-less refs.
func TestResolveStatusRequestURLShapesAndHeaders(t *testing.T) {
	r, fake := newTestResolver(nil) // every backend answers 404
	_, err := r.ResolveStatus(context.Background(), mustRef(t, "2070000000000000100"), Options{
		Strategies: []Strategy{StrategyFx, StrategyVx, StrategySyndication},
	})
	if err == nil {
		t.Fatalf("ResolveStatus: want an aggregate error")
	}
	want := []string{
		"https://api.fxtwitter.com/i/status/" + id100,
		"https://api.vxtwitter.com/i/status/" + id100,
		"https://cdn.syndication.twimg.com/tweet-result?id=" + id100 + "&token=x",
	}
	if len(fake.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", fake.calls, want)
	}
	for i, u := range want {
		if fake.calls[i] != u {
			t.Errorf("call[%d] = %q, want %q", i, fake.calls[i], u)
		}
	}
	h := fake.sent[0]
	if h["Accept"] != "application/json,text/plain,*/*" {
		t.Errorf("Accept = %q", h["Accept"])
	}
	if h["User-Agent"] == "" || h["Accept-Language"] == "" {
		t.Errorf("browser identity headers missing: UA=%q AL=%q", h["User-Agent"], h["Accept-Language"])
	}
}

// TestResolveStatusAllFailedNamesEveryStrategy: when every strategy errors the
// aggregate message lists each strategy with a short redacted reason, and no
// upstream URL ever leaks into the message.
func TestResolveStatusAllFailedNamesEveryStrategy(t *testing.T) {
	r, _ := newTestResolver(nil)
	_, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyFx, StrategyVx, StrategySyndication},
	})
	var terr *twitter.Error
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v (%T), want *twitter.Error", err, err)
	}
	msg := err.Error()
	for _, part := range []string{"fx: not_found", "vx: not_found", "syndication: not_found"} {
		if !strings.Contains(msg, part) {
			t.Errorf("message %q misses %q", msg, part)
		}
	}
	for _, leaked := range []string{"api.fxtwitter.com", "api.vxtwitter.com", "syndication.twimg.com", "token=x"} {
		if strings.Contains(msg, leaked) {
			t.Errorf("message %q leaks %q", msg, leaked)
		}
	}
}

// TestResolveStatusMalformedPayloadIsKindMalformed: a strategy response that
// is not a JSON object classifies as KindMalformed; a single-strategy run
// surfaces that kind on the aggregate error.
func TestResolveStatusMalformedPayloadIsKindMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"truncated json": `{"tweet": {"text": "x"`,
		"json array":     `[]`,
		"json null":      `null`,
		"plain text":     `gateway timeout`,
	} {
		t.Run(name, func(t *testing.T) {
			routes := map[string]fakeResp{fxURL100: {body: []byte(body), status: 200}}
			r, _ := newTestResolver(routes)
			_, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{Strategies: []Strategy{StrategyFx}})
			var terr *twitter.Error
			if !errors.As(err, &terr) || terr.Kind != twitter.KindMalformed {
				t.Fatalf("err = %v, want KindMalformed", err)
			}
			if !strings.Contains(err.Error(), "fx:") {
				t.Errorf("message %q does not name the fx strategy", err.Error())
			}
		})
	}
}

// TestResolveStatusEmptyEverywhereIsNotFound: when every strategy responds
// fine but carries no media the aggregate is KindNotFound (the status has no
// downloadable media), with each strategy reported as empty.
func TestResolveStatusEmptyEverywhereIsNotFound(t *testing.T) {
	textOnly := []byte(`{"code": 200, "tweet": {"text": "no media"}}`)
	routes := map[string]fakeResp{
		fxURL100:  {body: readFixture(t, "fx_text_only.json"), status: 200},
		vxURL100:  {body: textOnly, status: 200},
		syndURL10: {body: []byte(`{"text": "no media"}`), status: 200},
	}
	r, _ := newTestResolver(routes)
	_, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyFx, StrategyVx, StrategySyndication},
	})
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindNotFound {
		t.Fatalf("err = %v, want KindNotFound", err)
	}
	for _, part := range []string{"fx: empty", "vx: empty", "syndication: empty"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("message %q misses %q", err.Error(), part)
		}
	}
}

// TestResolveStatusObjectWithoutMediaIsEmpty: a well-formed payload that
// simply carries none of the media fields is "empty", not malformed.
func TestResolveStatusObjectWithoutMediaIsEmpty(t *testing.T) {
	routes := map[string]fakeResp{fxURL100: {body: []byte(`{"code": 200, "message": "OK"}`), status: 200}}
	r, _ := newTestResolver(routes)
	_, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{Strategies: []Strategy{StrategyFx}})
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindNotFound || !strings.Contains(err.Error(), "fx: empty") {
		t.Fatalf("err = %v, want KindNotFound naming fx: empty", err)
	}
}

// TestResolveStatusNoStrategiesIsInvalidArg.
func TestResolveStatusNoStrategiesIsInvalidArg(t *testing.T) {
	r, _ := newTestResolver(nil)
	_, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{})
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindInvalidArg {
		t.Fatalf("err = %v, want KindInvalidArg", err)
	}
}

// TestResolveStatusUnimplementedStrategyIsLocalState: naming a strategy this
// package does not implement (nitter stays command-layer work, M8 Task 4) is
// a wiring bug, reported per strategy.
func TestResolveStatusUnimplementedStrategyIsLocalState(t *testing.T) {
	r, _ := newTestResolver(nil)
	_, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyNitter},
	})
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindLocalState {
		t.Fatalf("err = %v, want KindLocalState", err)
	}
	if !strings.Contains(err.Error(), "nitter:") {
		t.Errorf("message %q misses %q", err.Error(), "nitter:")
	}
}

// TestResolveStatusAppliesQuality: quality=low selects the lowest non-zero
// video variant, keeps the highest as fallback, rewrites images to name=small.
func TestResolveStatusAppliesQuality(t *testing.T) {
	routes := map[string]fakeResp{fxURL100: {body: readFixture(t, "fx_status.json"), status: 200}}
	r, _ := newTestResolver(routes)
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyFx},
		Quality:    "low",
	})
	if err != nil {
		t.Fatalf("ResolveStatus: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("resolutions = %d, want 3", len(res))
	}
	if res[0].URL != "https://pbs.twimg.com/media/Gx1abc.jpg?name=small" {
		t.Errorf("image URL = %q, want name=small tier", res[0].URL)
	}
	if res[1].URL != "https://video.twimg.com/ext_tw_video/100/pu/vid/480x848/832.mp4" {
		t.Errorf("video URL = %q, want lowest non-zero variant", res[1].URL)
	}
	if res[1].FallbackURL != "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4" {
		t.Errorf("video fallback = %q, want the highest variant", res[1].FallbackURL)
	}
}

// TestResolveStatusHighQualityIsThePluginDefault: quality unset behaves as
// high (name=orig images, highest-bitrate variant, no fallback URL).
func TestResolveStatusHighQualityIsThePluginDefault(t *testing.T) {
	routes := map[string]fakeResp{fxURL100: {body: readFixture(t, "fx_status.json"), status: 200}}
	r, _ := newTestResolver(routes)
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{Strategies: []Strategy{StrategyFx}})
	if err != nil {
		t.Fatalf("ResolveStatus: %v", err)
	}
	if res[0].URL != "https://pbs.twimg.com/media/Gx1abc.jpg?name=orig" {
		t.Errorf("image URL = %q, want name=orig", res[0].URL)
	}
	if res[1].URL != "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4" {
		t.Errorf("video URL = %q, want highest-bitrate variant", res[1].URL)
	}
	if res[1].FallbackURL != "" {
		t.Errorf("video fallback = %q, want empty at the high tier", res[1].FallbackURL)
	}
	if res[1].Width != 720 || res[1].Height != 1280 {
		t.Errorf("video dims = %dx%d, want 720x1280", res[1].Width, res[1].Height)
	}
	if len(res[1].Variants) != 3 {
		t.Fatalf("video variants = %d, want all 3 kept in upstream order", len(res[1].Variants))
	}
	wantBitrates := []int64{0, 832000, 2176000}
	for i, b := range wantBitrates {
		if res[1].Variants[i].Bitrate != b {
			t.Errorf("variant[%d].bitrate = %d, want %d", i, res[1].Variants[i].Bitrate, b)
		}
	}
}

// TestResolveStatusDedupesAndDropsPlainHTTP: duplicate final URLs collapse to
// the first entry and plain-http links are dropped entirely.
func TestResolveStatusDedupesAndDropsPlainHTTP(t *testing.T) {
	body := []byte(`{
	  "tweet": {
	    "text": "duplicates",
	    "media": {"all": [
	      {"type": "photo", "url": "https://pbs.twimg.com/media/GaA.jpg"},
	      {"type": "photo", "url": "https://pbs.twimg.com/media/GaA.jpg"},
	      {"type": "photo", "url": "http://pbs.twimg.com/media/GaB.jpg"}
	    ]}
	  }
	}`)
	routes := map[string]fakeResp{fxURL100: {body: body, status: 200}}
	r, _ := newTestResolver(routes)
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{Strategies: []Strategy{StrategyFx}})
	if err != nil {
		t.Fatalf("ResolveStatus: %v", err)
	}
	if len(res) != 1 || res[0].URL != "https://pbs.twimg.com/media/GaA.jpg?name=orig" {
		t.Fatalf("resolutions = %+v, want exactly the deduped https image", res)
	}
}

// TestNewResolverWiresTransportAndClock: the constructor builds the shared
// transport (no network) and defaults the clock.
func TestNewResolverWiresTransportAndClock(t *testing.T) {
	r, err := NewResolver(httpx.Options{RetryAttempts: -1, RetryDelay: -1, MinInterval: -1}, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.HTTP == nil {
		t.Fatal("Resolver.HTTP is nil")
	}
	if r.Now == nil {
		t.Fatal("Resolver.Now not defaulted")
	}
	r2, err := NewResolver(httpx.Options{RetryAttempts: -1, RetryDelay: -1, MinInterval: -1}, func() time.Time { return time.Unix(0, 0) })
	if err != nil || r2.HTTP == nil || r2.Now() != time.Unix(0, 0) {
		t.Errorf("NewResolver with explicit clock: (%v, %v)", r2.HTTP != nil, err)
	}
}
