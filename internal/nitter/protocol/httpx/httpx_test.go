package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/tls-client/profiles"

	"github.com/shitianyaa/twitter-cli/sdk"
)

// ---------------------------------------------------------------------------
// Test doubles: fake clock, injectable sleep, scripted Doer. No real network
// I/O and no dependence on tls-client behavior — only the constructor smoke
// test touches tls-client construction (which dials nothing).
// ---------------------------------------------------------------------------

// fakeClock is an injectable clock advanced only by fakeSleep.Advance.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// step is one scripted (response, error) entry consumed by fakeDoer in order.
type step struct {
	status  int
	body    string
	headers map[string]string
	err     error // network-level error: Do returns it instead of a response
}

// trackedBody reports closure back to the owning fakeDoer.
type trackedBody struct {
	r       io.Reader
	onClose func()
}

func (b *trackedBody) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *trackedBody) Close() error               { b.onClose(); return nil }

// fakeDoer scripts responses and records request starts (fake-clock time),
// the requests themselves, and per-response body closure.
type fakeDoer struct {
	clock *fakeClock

	mu       sync.Mutex
	script   []step
	calls    int
	starts   []time.Time
	requests []*fhttp.Request
	closed   []bool
}

func (f *fakeDoer) Do(req *fhttp.Request) (*fhttp.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, f.clock.Now())
	f.requests = append(f.requests, req)
	i := f.calls
	f.calls++
	if i >= len(f.script) {
		return nil, fmt.Errorf("fakeDoer: script exhausted at call %d", i+1)
	}
	s := f.script[i]
	if s.err != nil {
		return nil, s.err
	}
	f.closed = append(f.closed, false)
	idx := len(f.closed) - 1
	resp := &fhttp.Response{
		StatusCode: s.status,
		Header:     fhttp.Header{},
		Body: &trackedBody{r: strings.NewReader(s.body), onClose: func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.closed[idx] = true
		}},
	}
	for k, v := range s.headers {
		resp.Header.Set(k, v)
	}
	return resp, nil
}

func (f *fakeDoer) unclosed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.closed {
		if !c {
			n++
		}
	}
	return n
}

// fakeSleep records sleep durations instead of really sleeping, advancing the
// fake clock by the slept duration. failOnCall (1-based) makes that call
// return err instead — used to simulate context cancellation mid-wait, which
// the real (context-aware) sleep would do.
type fakeSleep struct {
	clock      *fakeClock
	mu         sync.Mutex
	durations  []time.Duration
	failOnCall int
	err        error
}

func (s *fakeSleep) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	n := len(s.durations) + 1
	s.durations = append(s.durations, d)
	fail := s.failOnCall == n && s.err != nil
	s.mu.Unlock()
	if fail {
		return s.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.clock.Advance(d)
	return nil
}

func (s *fakeSleep) recorded() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.durations...)
}

// env bundles one test client with its doubles.
type env struct {
	clock *fakeClock
	doer  *fakeDoer
	sleep *fakeSleep
	c     *Client
}

// newTestClient builds a Client whose transport, clock and sleep are fully
// injected. Every env asserts, via t.Cleanup, that all scripted response
// bodies were fully consumed and closed by the end of the test.
func newTestClient(t *testing.T, opts Options, script ...step) *env {
	t.Helper()
	clk := newFakeClock()
	d := &fakeDoer{clock: clk, script: script}
	sl := &fakeSleep{clock: clk}
	opts.doer = d
	opts.sleep = sl.sleep
	if opts.Now == nil {
		opts.Now = clk.Now
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New(%+v): %v", opts, err)
	}
	e := &env{clock: clk, doer: d, sleep: sl, c: c}
	t.Cleanup(func() {
		if n := d.unclosed(); n > 0 {
			t.Errorf("%d scripted response body(s) were never closed", n)
		}
	})
	return e
}

// kindOf recovers the *twitter.Error from err.
func kindOf(t *testing.T, err error) *twitter.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var te *twitter.Error
	if !errors.As(err, &te) {
		t.Fatalf("error %v does not carry a *twitter.Error", err)
	}
	return te
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewDefaultConstructsTLSClientWithoutDialing(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New(Options{}) = %v, want success", err)
	}
	if c == nil {
		t.Fatal("New(Options{}) returned a nil client")
	}
	if c.doer == nil {
		t.Fatal("New(Options{}) did not install a transport")
	}
}

func TestNewDefaultsApplied(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New(Options{}): %v", err)
	}
	if c.timeout != 20*time.Second {
		t.Errorf("timeout = %v, want 20s", c.timeout)
	}
	if c.retryAttempts != 2 {
		t.Errorf("retryAttempts = %d, want 2", c.retryAttempts)
	}
	if c.retryDelay != time.Second {
		t.Errorf("retryDelay = %v, want 1s", c.retryDelay)
	}
	if c.minInterval != time.Second {
		t.Errorf("minInterval = %v, want 1s", c.minInterval)
	}
	if c.now == nil || c.sleep == nil {
		t.Error("clock and sleep helpers must be initialized")
	}
}

func TestNewExplicitProfileAndNegativeRetryAttempts(t *testing.T) {
	if _, err := New(Options{Profile: profiles.Chrome_120, RetryAttempts: -1}); err != nil {
		t.Fatalf("New with explicit profile: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Success paths
// ---------------------------------------------------------------------------

func TestGetReturnsFullBodyAndStatus(t *testing.T) {
	big := strings.Repeat("x", 64<<10) // larger than any single Read chunk
	env := newTestClient(t, Options{}, step{status: 200, body: big})

	body, status, err := env.c.Get(context.Background(), "https://instance.test/user/rss",
		map[string]string{"Accept": "application/rss+xml"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != big {
		t.Errorf("body truncated: got %d bytes, want %d", len(body), len(big))
	}
	if env.doer.calls != 1 {
		t.Errorf("calls = %d, want 1", env.doer.calls)
	}
	if len(env.sleep.recorded()) != 0 {
		t.Errorf("sleeps = %v, want none", env.sleep.recorded())
	}

	req := env.doer.requests[0]
	if req.Method != fhttp.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if got := req.Header.Get("Accept"); got != "application/rss+xml" {
		t.Errorf("Accept header = %q, want the map value", got)
	}
	if req.Header.Get("User-Agent") != "" {
		t.Error("Get must not invent default headers; only the map is merged")
	}
}

func TestGetReturns3xxWithoutFollowing(t *testing.T) {
	env := newTestClient(t, Options{}, step{status: 302, body: "moved", headers: map[string]string{"Location": "https://elsewhere.test/x"}})

	body, status, err := env.c.Get(context.Background(), "https://instance.test/a", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status != 302 || string(body) != "moved" {
		t.Errorf("got (%q, %d), want (%q, 302)", body, status, "moved")
	}
}

// ---------------------------------------------------------------------------
// Retry: network errors and 5xx, linear backoff
// ---------------------------------------------------------------------------

func TestGetRetries5xxWithLinearBackoffThenSucceeds(t *testing.T) {
	env := newTestClient(t, Options{RetryAttempts: 2, RetryDelay: 100 * time.Millisecond},
		step{status: 500, body: "boom"},
		step{status: 503, body: "unavailable"},
		step{status: 200, body: "third"})

	body, status, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status != 200 || string(body) != "third" {
		t.Errorf("got (%q, %d), want (%q, 200)", body, status, "third")
	}
	if env.doer.calls != 3 {
		t.Errorf("calls = %d, want 3 (two retries)", env.doer.calls)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if got := env.sleep.recorded(); !equalDurations(got, want) {
		t.Errorf("backoff sleeps = %v, want %v (linear growth)", got, want)
	}
}

func TestGetRetriesNetworkErrorsThenSucceeds(t *testing.T) {
	boom := errors.New("dial tcp: connection refused")
	env := newTestClient(t, Options{RetryAttempts: 2, RetryDelay: 50 * time.Millisecond},
		step{err: boom},
		step{status: 200, body: "ok"})

	body, status, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status != 200 || string(body) != "ok" {
		t.Errorf("got (%q, %d), want (%q, 200)", body, status, "ok")
	}
	if env.doer.calls != 2 {
		t.Errorf("calls = %d, want 2", env.doer.calls)
	}
	if got := env.sleep.recorded(); !equalDurations(got, []time.Duration{50 * time.Millisecond}) {
		t.Errorf("backoff sleeps = %v, want [50ms]", got)
	}
}

func TestGet5xxExhaustedRetriesClassifiedUnavailable(t *testing.T) {
	env := newTestClient(t, Options{RetryAttempts: 2, RetryDelay: 10 * time.Millisecond},
		step{status: 500}, step{status: 500}, step{status: 500})

	body, status, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	if err == nil {
		t.Fatal("Get: nil error, want KindUnavailable")
	}
	te := kindOf(t, err)
	if te.Kind != twitter.KindUnavailable {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindUnavailable)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q must carry the status code", err)
	}
	if body != nil || status != 0 {
		t.Errorf("on error body/status must be nil/0, got (%v, %d)", body, status)
	}
	if env.doer.calls != 3 {
		t.Errorf("calls = %d, want 3", env.doer.calls)
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	if got := env.sleep.recorded(); !equalDurations(got, want) {
		t.Errorf("backoff sleeps = %v, want %v", got, want)
	}
}

func TestGetNetworkErrorExhaustedRetriesReachRootCause(t *testing.T) {
	boom := errors.New("connection reset by peer")
	env := newTestClient(t, Options{RetryAttempts: 1, RetryDelay: time.Millisecond},
		step{err: boom}, step{err: boom})

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindUnavailable {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindUnavailable)
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v must wrap the last network error", err)
	}
	if env.doer.calls != 2 {
		t.Errorf("calls = %d, want 2", env.doer.calls)
	}
}

// TestGetRedactsURLFromTransportError pins the sdk redaction contract on the
// network-exhaustion path: real transports wrap failures in *url.Error whose
// Error() embeds the full request URL including its query string, so the
// wrapper itself must never be carried into the *twitter.Error chain.
func TestGetRedactsURLFromTransportError(t *testing.T) {
	boom := errors.New("dial tcp: refused")
	leak := &url.Error{Op: "Get", URL: "https://instance.test/user?token=secret", Err: boom}
	env := newTestClient(t, Options{RetryAttempts: 1, RetryDelay: time.Millisecond},
		step{err: leak}, step{err: leak})

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindUnavailable {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindUnavailable)
	}
	msg := err.Error()
	if strings.Contains(msg, "token=secret") {
		t.Errorf("error %q leaks the URL query string", msg)
	}
	if strings.Contains(msg, "instance.test/user") {
		t.Errorf("error %q leaks the request URL", msg)
	}
	if !strings.Contains(msg, "dial tcp: refused") {
		t.Errorf("root cause text must survive sanitization, got %q", msg)
	}
	if !errors.Is(err, boom) {
		t.Errorf("sanitized chain must still reach the root cause via errors.Is")
	}
}

// TestGetRedactsURLFromRequestBuildError pins the same contract on the
// request-construction path: url.ParseRequestURI failures also embed the raw
// URL (query string included) in their message.
func TestGetRedactsURLFromRequestBuildError(t *testing.T) {
	env := newTestClient(t, Options{})

	_, _, err := env.c.Get(context.Background(), "http://exa mple.test/x?token=secret", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindInvalidArg {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindInvalidArg)
	}
	msg := err.Error()
	if strings.Contains(msg, "token=secret") {
		t.Errorf("error %q leaks the URL query string", msg)
	}
	if strings.Contains(msg, "http://") || strings.Contains(msg, "exa mple") {
		t.Errorf("error %q leaks the raw URL", msg)
	}
	if env.doer.calls != 0 {
		t.Errorf("calls = %d, want 0", env.doer.calls)
	}
}

// ---------------------------------------------------------------------------
// Status classification (the brief's table)
// ---------------------------------------------------------------------------

func TestGetClassifiesStatuses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantKind  twitter.Kind
		wantInMsg string // status code must appear in the message for non-404 4xx
	}{
		{"404 not found", 404, twitter.KindNotFound, ""},
		{"401 challenge", 401, twitter.KindChallenge, "401"},
		{"403 challenge", 403, twitter.KindChallenge, "403"},
		{"409 other 4xx", 409, twitter.KindUnavailable, "409"},
		{"451 other 4xx", 451, twitter.KindUnavailable, "451"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestClient(t, Options{}, step{status: tc.status, body: "nope"})

			body, status, err := env.c.Get(context.Background(), "https://instance.test/x", nil)
			te := kindOf(t, err)
			if te.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", te.Kind, tc.wantKind)
			}
			if body != nil || status != 0 {
				t.Errorf("on error body/status must be nil/0, got (%v, %d)", body, status)
			}
			if env.doer.calls != 1 {
				t.Errorf("calls = %d, want 1 (4xx is never retried)", env.doer.calls)
			}
			if tc.wantInMsg != "" && !strings.Contains(err.Error(), tc.wantInMsg) {
				t.Errorf("error %q must carry the status code", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 429 / Retry-After
// ---------------------------------------------------------------------------

func TestGet429WithoutRetryAfterIsRateLimited(t *testing.T) {
	env := newTestClient(t, Options{}, step{status: 429, body: "slow down"})

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindRateLimited {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindRateLimited)
	}
	if te.RetryAfter != nil {
		t.Errorf("RetryAfter = %v, want nil (nothing usable came from the response)", *te.RetryAfter)
	}
	if env.doer.calls != 1 {
		t.Errorf("calls = %d, want 1", env.doer.calls)
	}
	if len(env.sleep.recorded()) != 0 {
		t.Errorf("sleeps = %v, want none", env.sleep.recorded())
	}
}

func TestGet429GarbageRetryAfterIsRateLimitedWithoutRetry(t *testing.T) {
	env := newTestClient(t, Options{},
		step{status: 429, headers: map[string]string{"Retry-After": "abc"}})

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindRateLimited {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindRateLimited)
	}
	if te.RetryAfter != nil {
		t.Errorf("RetryAfter = %v, want nil for unparseable value", *te.RetryAfter)
	}
	if env.doer.calls != 1 {
		t.Errorf("calls = %d, want 1 (invalid Retry-After: no wait, no retry)", env.doer.calls)
	}
}

func TestGet429AbsurdRetryAfterIsTreatedInvalid(t *testing.T) {
	// Overflows int64 seconds; must never become a (negative) Duration.
	env := newTestClient(t, Options{},
		step{status: 429, headers: map[string]string{"Retry-After": "99999999999999999999"}})

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindRateLimited {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindRateLimited)
	}
	if te.RetryAfter != nil {
		t.Errorf("RetryAfter = %v, want nil for absurd value", *te.RetryAfter)
	}
	if env.doer.calls != 1 {
		t.Errorf("calls = %d, want 1", env.doer.calls)
	}
}

func TestGet429ValidRetryAfterWaitsOnceThenRetries(t *testing.T) {
	env := newTestClient(t, Options{},
		step{status: 429, headers: map[string]string{"Retry-After": "2"}},
		step{status: 200, body: "recovered"})

	body, status, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status != 200 || string(body) != "recovered" {
		t.Errorf("got (%q, %d), want (%q, 200)", body, status, "recovered")
	}
	if env.doer.calls != 2 {
		t.Errorf("calls = %d, want 2 (one Retry-After retry)", env.doer.calls)
	}
	if got := env.sleep.recorded(); !equalDurations(got, []time.Duration{2 * time.Second}) {
		t.Errorf("sleeps = %v, want exactly [2s] (the Retry-After value)", got)
	}
	if d := env.doer.starts[1].Sub(env.doer.starts[0]); d != 2*time.Second {
		t.Errorf("second request started %v after the first, want exactly 2s on the injected clock", d)
	}
}

func TestGet429HTTPDateRetryAfterWaitsThenRetries(t *testing.T) {
	when := env0Clock().Add(2 * time.Second)
	env := newTestClient(t, Options{},
		step{status: 429, headers: map[string]string{"Retry-After": when.UTC().Format(fhttp.TimeFormat)}},
		step{status: 200, body: "ok"})

	if _, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := env.sleep.recorded(); !equalDurations(got, []time.Duration{2 * time.Second}) {
		t.Errorf("sleeps = %v, want [2s] parsed from the HTTP-date", got)
	}
}

func TestGet429RetryStill429ReturnsRateLimitedWithResponseRetryAfter(t *testing.T) {
	env := newTestClient(t, Options{},
		step{status: 429, headers: map[string]string{"Retry-After": "2"}},
		step{status: 429, headers: map[string]string{"Retry-After": "5"}})

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindRateLimited {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindRateLimited)
	}
	// The wait-then-retry was already spent; the error passes through the
	// Retry-After the final 429 response provided (never fabricated).
	if te.RetryAfter == nil || *te.RetryAfter != 5*time.Second {
		t.Errorf("RetryAfter = %v, want 5s from the final response", te.RetryAfter)
	}
	if env.doer.calls != 2 {
		t.Errorf("calls = %d, want 2 (429 retries at most once)", env.doer.calls)
	}
	if got := env.sleep.recorded(); !equalDurations(got, []time.Duration{2 * time.Second}) {
		t.Errorf("sleeps = %v, want [2s] with no backoff after the Retry-After wait", got)
	}
}

// ---------------------------------------------------------------------------
// Pacing
// ---------------------------------------------------------------------------

func TestPacingDefersSecondGetUntilMinInterval(t *testing.T) {
	env := newTestClient(t, Options{MinInterval: time.Second},
		step{status: 200, body: "a"}, step{status: 200, body: "b"})

	if _, _, err := env.c.Get(context.Background(), "https://instance.test/a", nil); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if _, _, err := env.c.Get(context.Background(), "https://instance.test/b", nil); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got := env.sleep.recorded(); !equalDurations(got, []time.Duration{time.Second}) {
		t.Errorf("sleeps = %v, want [1s] pacing wait before the second Get", got)
	}
	if d := env.doer.starts[1].Sub(env.doer.starts[0]); d < time.Second {
		t.Errorf("second request started %v after the first, want >= MinInterval (1s)", d)
	}
}

func TestPacingSkipsWaitWhenIntervalAlreadyElapsed(t *testing.T) {
	env := newTestClient(t, Options{MinInterval: time.Second},
		step{status: 200, body: "a"}, step{status: 200, body: "b"})

	if _, _, err := env.c.Get(context.Background(), "https://instance.test/a", nil); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	env.clock.Advance(2 * time.Second)
	if _, _, err := env.c.Get(context.Background(), "https://instance.test/b", nil); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got := env.sleep.recorded(); len(got) != 0 {
		t.Errorf("sleeps = %v, want none (interval already elapsed)", got)
	}
}

// ---------------------------------------------------------------------------
// Context handling
// ---------------------------------------------------------------------------

func TestGetAbortsWhenContextCancelledDuringBackoff(t *testing.T) {
	env := newTestClient(t, Options{RetryAttempts: 2, RetryDelay: 10 * time.Millisecond},
		step{status: 500}, step{status: 500}, step{status: 500})
	env.sleep.failOnCall = 1
	env.sleep.err = context.Canceled

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user", nil)
	if err == nil {
		t.Fatal("Get: nil error, want cancellation")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error %q must mention the context cancellation", err)
	}
	if te := kindOf(t, err); te.Kind != twitter.KindUnavailable {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindUnavailable)
	}
	if env.doer.calls != 1 {
		t.Errorf("calls = %d, want 1 (aborted during the first backoff wait)", env.doer.calls)
	}
}

func TestGetWithCancelledContextFailsFast(t *testing.T) {
	env := newTestClient(t, Options{}, step{status: 200, body: "never"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := env.c.Get(ctx, "https://instance.test/x", nil)
	if err == nil {
		t.Fatal("Get: nil error, want cancellation")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error %q must mention the context cancellation", err)
	}
	if env.doer.calls != 0 {
		t.Errorf("calls = %d, want 0 (nothing sent)", env.doer.calls)
	}
}

func TestGetInvalidURLIsInvalidArg(t *testing.T) {
	env := newTestClient(t, Options{}, step{status: 200, body: "never"})

	_, _, err := env.c.Get(context.Background(), "http://exa mple.test/x", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindInvalidArg {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindInvalidArg)
	}
	if env.doer.calls != 0 {
		t.Errorf("calls = %d, want 0", env.doer.calls)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// env0Clock is a fresh fake clock used only to compute an HTTP-date header
// value 2s ahead of the reference time the matching env's clock starts at.
func env0Clock() time.Time { return newFakeClock().Now() }

// ---------------------------------------------------------------------------
// Response body size cap (Options.MaxBodyBytes)
// ---------------------------------------------------------------------------

func TestNewDefaultMaxBodyBytes(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New(Options{}): %v", err)
	}
	if c.maxBodyBytes != 10<<20 {
		t.Errorf("maxBodyBytes = %d, want the documented 10 MiB default", c.maxBodyBytes)
	}
}

func TestGetBodyOverCapIsKindMalformedWithoutRetry(t *testing.T) {
	env := newTestClient(t, Options{MaxBodyBytes: 16, RetryAttempts: 3, RetryDelay: -1},
		step{status: 200, body: "0123456789abcdefG"}) // 17 bytes: one over the cap

	body, status, err := env.c.Get(context.Background(), "https://instance.test/user/rss", nil)
	if err == nil {
		t.Fatalf("Get = (%q, %d, nil), want an error (no partial body may be returned)", body, status)
	}
	if body != nil || status != 0 {
		t.Errorf("got (%q, %d), want (nil, 0) — no partial body on the error path", body, status)
	}
	te := kindOf(t, err)
	if te.Kind != twitter.KindMalformed {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindMalformed)
	}
	if te.Op != opGet {
		t.Errorf("Op = %q, want %q", te.Op, opGet)
	}
	if !strings.Contains(err.Error(), "16 bytes") {
		t.Errorf("err = %v, want it to name the cap", err)
	}
	// An oversized body is a terminal response shape: retrying cannot help,
	// so the budget must stay untouched.
	if env.doer.calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry for a classified size failure)", env.doer.calls)
	}
	if n := env.doer.unclosed(); n != 0 {
		t.Errorf("%d response body(s) left unclosed", n)
	}
}

func TestGetBodyExactlyAtCapSucceeds(t *testing.T) {
	exact := "0123456789abcdef" // 16 bytes
	env := newTestClient(t, Options{MaxBodyBytes: 16}, step{status: 200, body: exact})

	body, _, err := env.c.Get(context.Background(), "https://instance.test/user/rss", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != exact {
		t.Errorf("body = %q, want the full %d-byte body", body, len(exact))
	}
}

func TestGetNegativeMaxBodyBytesDisablesCap(t *testing.T) {
	// Larger than the 10 MiB default, so only the explicit opt-out passes.
	huge := strings.Repeat("x", 10<<20+1)
	env := newTestClient(t, Options{MaxBodyBytes: -1}, step{status: 200, body: huge})

	body, _, err := env.c.Get(context.Background(), "https://instance.test/user/rss", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(body) != len(huge) {
		t.Errorf("body = %d bytes, want %d", len(body), len(huge))
	}
}

func TestGetDefaultCapRejectsOversizeWithoutOption(t *testing.T) {
	huge := strings.Repeat("x", 10<<20+1)
	env := newTestClient(t, Options{RetryAttempts: -1}, step{status: 200, body: huge})

	_, _, err := env.c.Get(context.Background(), "https://instance.test/user/rss", nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindMalformed {
		t.Errorf("Kind = %v, want %v (10 MiB default applies when the option is zero)", te.Kind, twitter.KindMalformed)
	}
}

// ---------------------------------------------------------------------------
// Post (the media resolver's form posts share the transport contract)
// ---------------------------------------------------------------------------

// TestPostSendsMethodBodyAndHeaders pins the POST wire shape: the method, the
// exact body bytes, the caller's headers (Content-Type included — the caller
// owns it, mirroring the plugin which builds it into its header map), and the
// classified (body, status) response.
func TestPostSendsMethodBodyAndHeaders(t *testing.T) {
	env := newTestClient(t, Options{MinInterval: -1, RetryAttempts: -1},
		step{status: 200, body: `{"status":"ok"}`})
	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Referer":      "https://xdown.app/",
	}
	body, status, err := env.c.Post(context.Background(), "https://xdown.app/api/ajaxSearch",
		[]byte("lang=zh-cn&q=https%3A%2F%2Fx.com%2Fnasa%2Fstatus%2F123"), headers)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if status != 200 || string(body) != `{"status":"ok"}` {
		t.Errorf("Post = (%q, %d), want the scripted body and 200", body, status)
	}
	env.doer.mu.Lock()
	defer env.doer.mu.Unlock()
	if len(env.doer.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(env.doer.requests))
	}
	req := env.doer.requests[0]
	if req.Method != fhttp.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://xdown.app/api/ajaxSearch" {
		t.Errorf("url = %q", req.URL.String())
	}
	sent, rerr := io.ReadAll(req.Body)
	if rerr != nil {
		t.Fatalf("read recorded request body: %v", rerr)
	}
	if string(sent) != "lang=zh-cn&q=https%3A%2F%2Fx.com%2Fnasa%2Fstatus%2F123" {
		t.Errorf("request body = %q, want the exact form payload", sent)
	}
	for _, k := range []string{"Content-Type", "Referer"} {
		if req.Header.Get(k) != headers[k] {
			t.Errorf("header %s = %q, want %q", k, req.Header.Get(k), headers[k])
		}
	}
}

// TestPostRetriesLikeGet: a 5xx is retried with the same linear backoff and
// the request body is resent intact on every attempt (a one-shot body reader
// would resend empty bytes).
func TestPostRetriesLikeGet(t *testing.T) {
	env := newTestClient(t, Options{MinInterval: -1, RetryDelay: -1},
		step{status: 500, body: "boom"},
		step{status: 500, body: "boom"},
		step{status: 200, body: `{"status":"ok","data":""}`})
	body, status, err := env.c.Post(context.Background(), "https://xdown.app/api/ajaxSearch",
		[]byte("lang=zh-cn&q=x"), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if status != 200 || string(body) != `{"status":"ok","data":""}` {
		t.Errorf("Post = (%q, %d), want success after retries", body, status)
	}
	if got := env.sleep.recorded(); len(got) != 2 {
		t.Errorf("backoff waits = %v, want two linear waits", got)
	}
	env.doer.mu.Lock()
	defer env.doer.mu.Unlock()
	if len(env.doer.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(env.doer.requests))
	}
	for i, req := range env.doer.requests {
		if req.Method != fhttp.MethodPost {
			t.Errorf("request[%d] method = %q, want POST", i, req.Method)
		}
		sent, rerr := io.ReadAll(req.Body)
		if rerr != nil {
			t.Fatalf("read request[%d] body: %v", i, rerr)
		}
		if string(sent) != "lang=zh-cn&q=x" {
			t.Errorf("request[%d] body = %q, want the payload resent on every attempt", i, sent)
		}
	}
}

// TestPostClassifiesStatuses: POST responses classify exactly like GETs.
func TestPostClassifiesStatuses(t *testing.T) {
	env := newTestClient(t, Options{MinInterval: -1, RetryAttempts: -1}, step{status: 404, body: "nope"})
	_, _, err := env.c.Post(context.Background(), "https://xdown.app/api/ajaxSearch", []byte("q=x"), nil)
	te := kindOf(t, err)
	if te.Kind != twitter.KindNotFound {
		t.Errorf("Kind = %v, want %v", te.Kind, twitter.KindNotFound)
	}
	if te.Op != "httpx.Post" {
		t.Errorf("Op = %q, want httpx.Post", te.Op)
	}
}
