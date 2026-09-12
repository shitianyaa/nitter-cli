// Package httpx is the HTTP transport for Nitter instances: a tls-client
// backed GET/POST with global pacing (a minimum interval between the starts of
// consecutive requests), linear-backoff retries for network errors and 5xx,
// and single-shot Retry-After honoring for 429s. Failures are classified
// into the shared sdk (package nitter) error kinds.
//
// Package boundary: this is a protocol detail of the Nitter fetch path.
// Only packages under internal/nitter/*, the media resolution package
// internal/media (M8: it fetches third-party status JSON over GET and sends
// xdown form posts over POST with the same paced, retried transport), and
// the sdk may import it.
//
// Redaction: errors produced here obey the sdk contract — neither the
// *nitter.Error nor its wrapped chain contains credentials, URL query
// strings, request headers or response bodies. Status codes and transport
// error strings are the only upstream facts echoed back. To that end,
// sanitizeTransportErr strips net/url wrappers (whose Error() text embeds
// the full request URL) before any transport error is wrapped.
package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"github.com/shitianyaa/nitter-cli/sdk"
)

const (
	opGet  = "httpx.Get"
	opPost = "httpx.Post"
	opNew  = "httpx.New"
)

const (
	// defaultTimeout bounds a single request/round trip.
	defaultTimeout = 20 * time.Second
	// defaultRetryAttempts is the number of EXTRA attempts after the first
	// one for network errors and 5xx (Options zero value → this default).
	defaultRetryAttempts = 2
	// defaultRetryDelay is the linear backoff base: the n-th retry
	// (attempt+1, 1-based) waits retryDelay*(attempt+1).
	defaultRetryDelay = 1 * time.Second
	// defaultMinInterval spaces the starts of consecutive Get calls.
	defaultMinInterval = 1 * time.Second
	// defaultMaxBodyBytes caps one response body read (Options zero value →
	// this default). A Nitter RSS feed or timeline page is orders of
	// magnitude smaller; the cap bounds hostile or misbehaving instances.
	defaultMaxBodyBytes = int64(10 << 20)
)

// maxRetryAfterSeconds caps Retry-After seconds so that converting to
// time.Duration (nanoseconds) cannot overflow int64; anything above is
// treated as invalid rather than becoming a bogus (possibly negative) wait.
const maxRetryAfterSeconds = math.MaxInt64 / int64(time.Second)

// Doer is the transport boundary of this package: the Get method drives any
// implementation. tls-client's HttpClient satisfies it; tests inject a
// scripted fake instead.
type Doer interface {
	Do(*fhttp.Request) (*fhttp.Response, error)
}

// Options configures a Client. Every field is optional; zero values select
// the documented defaults (Timeout 20s, RetryAttempts 2, RetryDelay 1s,
// MinInterval 1s, newest Chrome profile pinned below). Negative values
// explicitly disable the corresponding feature (retries / backoff / pacing).
type Options struct {
	// Proxy is the proxy URL (e.g. "http://host:port", socks5://...).
	// "" means no WithProxyUrl option is passed (environment inheritance is
	// left to the transport).
	Proxy string
	// Timeout bounds a single request; 0 → defaultTimeout.
	Timeout time.Duration
	// Profile pins the TLS fingerprint. Zero value → defaultProfile below.
	Profile profiles.ClientProfile
	// RetryAttempts is the number of extra attempts for network errors and
	// 5xx; 0 → defaultRetryAttempts, negative → no retries.
	RetryAttempts int
	// RetryDelay is the linear backoff base: the n-th retry waits
	// retryDelay*(attempt+1); 0 → defaultRetryDelay, negative → no backoff
	// wait between retries.
	RetryDelay time.Duration
	// MinInterval is the global minimum interval between the STARTS of
	// consecutive Get calls on one client; 0 → defaultMinInterval,
	// negative → no pacing.
	MinInterval time.Duration
	// MaxBodyBytes caps one response body read. 0 → defaultMaxBodyBytes
	// (10 MiB); negative → explicitly unlimited. A body over the cap fails
	// the request as KindMalformed with no partial body returned and no
	// retry (the response shape cannot improve by repeating the request).
	MaxBodyBytes int64
	// Now is the injectable clock; nil → time.Now.
	Now func() time.Time

	// doer, when set (tests in this package only), replaces the constructed
	// tls-client transport entirely: no real client is built and Proxy,
	// Timeout and Profile are ignored. Unexported so external callers always
	// get the real transport.
	doer Doer
	// sleep overrides the internal context-aware sleep (tests record
	// durations against a fake clock instead of really sleeping).
	sleep func(ctx context.Context, d time.Duration) error
}

// defaultProfile is the newest stable Chrome profile shipped by
// tls-client v1.16.0 (the Chrome_152_PSK variant only adds the post-quantum
// key share used by QUIC handshakes, which is not the target here). Pinned
// explicitly instead of profiles.DefaultClientProfile so a dependency
// upgrade cannot silently change the fingerprint.
var defaultProfile = profiles.Chrome_152

// Client performs paced, retried, classified GETs and POSTs. It is safe for
// concurrent use: pacing state is mutex-guarded and the transport is
// tls-client's goroutine-safe HttpClient.
type Client struct {
	doer          Doer
	now           func() time.Time
	sleep         func(ctx context.Context, d time.Duration) error
	timeout       time.Duration
	retryAttempts int
	retryDelay    time.Duration
	minInterval   time.Duration
	// maxBodyBytes is the effective body cap; <= 0 means unlimited
	// (only reachable via an explicit negative Options value).
	maxBodyBytes int64

	paceMu    sync.Mutex
	lastStart time.Time
}

// New builds a Client. Unless Options.doer is injected (tests), it
// constructs a real tls-client transport with the chosen Chrome profile, a
// no-op logger, a cookie jar and redirects disabled (3xx responses are
// returned to the caller, matching the classification contract). No
// network I/O happens here.
func New(opts Options) (*Client, error) {
	c := &Client{
		now:           opts.Now,
		sleep:         opts.sleep,
		timeout:       defaultTimeout,
		retryAttempts: defaultRetryAttempts,
		retryDelay:    defaultRetryDelay,
		minInterval:   defaultMinInterval,
		maxBodyBytes:  defaultMaxBodyBytes,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = sleepContext
	}
	if opts.Timeout > 0 {
		c.timeout = opts.Timeout
	}
	switch {
	case opts.RetryAttempts > 0:
		c.retryAttempts = opts.RetryAttempts
	case opts.RetryAttempts < 0:
		c.retryAttempts = 0
	}
	if opts.RetryDelay > 0 {
		c.retryDelay = opts.RetryDelay
	} else if opts.RetryDelay < 0 {
		c.retryDelay = 0
	}
	if opts.MinInterval > 0 {
		c.minInterval = opts.MinInterval
	} else if opts.MinInterval < 0 {
		c.minInterval = 0
	}
	switch {
	case opts.MaxBodyBytes > 0:
		c.maxBodyBytes = opts.MaxBodyBytes
	case opts.MaxBodyBytes < 0:
		c.maxBodyBytes = 0 // explicit opt-out: unlimited body reads
	}

	if opts.doer != nil {
		c.doer = opts.doer
		return c, nil
	}

	profile := opts.Profile
	// ClientProfile has only unexported fields, so its zero value is
	// detected via the underlying utls ClientHelloID. Note the inversion:
	// utls's IsSet() confusingly returns true for the ZERO id (u_common.go:
	// `Client == "" && Version == ""`), and feeding a zero profile into
	// tls-client panics while building.
	hello := profile.GetClientHelloId()
	if hello.IsSet() {
		profile = defaultProfile
	}

	tlsOpts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(c.timeout / time.Second)),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
		tls_client.WithClientProfile(profile),
	}
	if opts.Proxy != "" {
		tlsOpts = append(tlsOpts, tls_client.WithProxyUrl(opts.Proxy))
	}
	tc, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), tlsOpts...)
	if err != nil {
		return nil, nitter.Errorf(nitter.KindLocalState, opNew, "build tls-client transport: %w", err)
	}
	c.doer = tc
	return c, nil
}

// Get performs a GET against url with exactly the given headers merged onto
// the request (no defaults are added; the User-Agent baseline is decided by
// a later layer) and returns the response body and status. 2xx/3xx succeed;
// 3xx is returned as-is because redirects are not followed.
//
// Classification (see Post and the package comment for retry/pacing details):
//   - network error / unreadable body / 5xx: retried retryAttempts times with
//     linear backoff retryDelay*(attempt); exhausted → KindUnavailable.
//   - body over the effective MaxBodyBytes cap: KindMalformed immediately —
//     no partial body, no retry.
//   - 429 with a valid Retry-After (seconds or HTTP-date): wait once, retry
//     once; if that response is 429 again → KindRateLimited carrying the
//     final response's Retry-After when present. Invalid or absent
//     Retry-After → KindRateLimited with no wait and no retry, RetryAfter nil.
//   - 404 → KindNotFound; 401/403 → KindChallenge; other 4xx →
//     KindUnavailable (status code in the message).
//
// On any error the returned body is nil and the status 0; inspect the
// *nitter.Error (errors.As) for Kind and RetryAfter.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	body, status, _, err := c.send(ctx, opGet, fhttp.MethodGet, url, nil, headers)
	return body, status, err
}

// Post performs a POST against url with the given request body and exactly
// the given headers merged onto the request — the caller owns Content-Type,
// mirroring the plugin, which builds it into its header map. It shares Get's
// pacing, retry and classification contract verbatim; the body is resent
// intact on every retry attempt.
func (c *Client) Post(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, int, error) {
	b, status, _, err := c.send(ctx, opPost, fhttp.MethodPost, url, body, headers)
	return b, status, err
}

// GetMeta performs a GET with Get's exact pacing, retry and classification
// contract and additionally surfaces the response headers (as a plain
// map[string][]string) for callers that must read one — the media probe
// reads Content-Range from a ranged GET. On any error the body is nil, the
// status 0 and the headers nil; the *nitter.Error surface is identical to
// Get's (op httpx.Get).
func (c *Client) GetMeta(ctx context.Context, url string, headers map[string]string) ([]byte, int, map[string][]string, error) {
	return c.send(ctx, opGet, fhttp.MethodGet, url, nil, headers)
}

// send is the shared GET/POST pipeline: pacing, the retry/classification loop
// and body reading. The request is rebuilt per attempt so a POST body (a
// one-shot reader once consumed) is resent whole on retries. The response's
// header map rides along so GetMeta can surface it; Get and Post drop it.
func (c *Client) send(ctx context.Context, op, method, url string, body []byte, headers map[string]string) ([]byte, int, map[string][]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, nil, nitter.Errorf(nitter.KindUnavailable, op, "request not sent: %w", err)
	}
	if err := c.pace(ctx); err != nil {
		return nil, 0, nil, err
	}

	// retryAfterUsed guards the single 429 wait-and-retry; it is independent
	// of the network/5xx retry budget counted by attempt below.
	retryAfterUsed := false
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := fhttp.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			// Parse failures carry the raw URL in their message — sanitize
			// before wrapping (sdk redaction contract).
			return nil, 0, nil, nitter.Errorf(nitter.KindInvalidArg, op, "build request: %w", sanitizeTransportErr(err))
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, respBody, err := c.doOnce(req)
		if err != nil {
			// A body over the size cap arrives already classified (its
			// KindMalformed carries no *url.Error to sanitize): terminal —
			// the response shape cannot improve by retrying.
			var terr *nitter.Error
			if errors.As(err, &terr) {
				return nil, 0, nil, err
			}
			// Transport-level failure (network or unreadable body). The
			// cause is sanitized before wrapping: net/url's *url.Error
			// embeds the full request URL in its message.
			if attempt < c.retryAttempts {
				if werr := c.wait(ctx, c.retryDelay*time.Duration(attempt+1), "backoff"); werr != nil {
					return nil, 0, nil, werr
				}
				continue
			}
			return nil, 0, nil, nitter.Errorf(nitter.KindUnavailable, op,
				"request failed after %d attempt(s): %w", attempt+1, sanitizeTransportErr(err))
		}

		status := resp.StatusCode
		switch {
		case status >= 200 && status < 400:
			return respBody, status, map[string][]string(resp.Header), nil

		case status == fhttp.StatusTooManyRequests:
			delay, valid := parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
			if retryAfterUsed {
				e := &nitter.Error{
					Kind: nitter.KindRateLimited,
					Op:   op,
					Err:  errors.New("instance returned HTTP 429 again after honoring Retry-After"),
				}
				if valid {
					e.RetryAfter = &delay
				}
				return nil, 0, nil, e
			}
			if !valid {
				return nil, 0, nil, &nitter.Error{
					Kind: nitter.KindRateLimited,
					Op:   op,
					Err:  errors.New("instance returned HTTP 429 without a usable Retry-After"),
				}
			}
			retryAfterUsed = true
			if werr := c.wait(ctx, delay, "retry-after"); werr != nil {
				return nil, 0, nil, werr
			}
			continue

		case status == fhttp.StatusNotFound:
			return nil, 0, nil, nitter.Errorf(nitter.KindNotFound, op, "instance returned HTTP 404")

		case status == fhttp.StatusUnauthorized || status == fhttp.StatusForbidden:
			return nil, 0, nil, nitter.Errorf(nitter.KindChallenge, op, "instance returned HTTP %d", status)

		case status >= 400 && status < 500:
			return nil, 0, nil, nitter.Errorf(nitter.KindUnavailable, op, "instance returned HTTP %d", status)

		default:
			// 5xx (and unexpected 1xx): retryable upstream failure.
			if attempt < c.retryAttempts {
				if werr := c.wait(ctx, c.retryDelay*time.Duration(attempt+1), "backoff"); werr != nil {
					return nil, 0, nil, werr
				}
				continue
			}
			return nil, 0, nil, nitter.Errorf(nitter.KindUnavailable, op,
				"instance returned HTTP %d after %d attempt(s)", status, attempt+1)
		}
	}
}

// doOnce sends the request once, reads the response body fully (capped by the
// effective body size), and closes it on every path. The returned response
// carries StatusCode and Header only (Body has been consumed). Transport-level
// failures come back as unclassified errors; a body over the cap comes back
// already classified (KindMalformed) so Get can skip its retry budget.
func (c *Client) doOnce(req *fhttp.Request) (*fhttp.Response, []byte, error) {
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.Body == nil {
		return resp, nil, nil
	}
	var body []byte
	var readErr error
	if c.maxBodyBytes > 0 {
		// One byte over the cap distinguishes "exactly at the cap" from
		// "there was more" without reading the rest of the body.
		body, readErr = io.ReadAll(io.LimitReader(resp.Body, c.maxBodyBytes+1))
	} else {
		body, readErr = io.ReadAll(resp.Body)
	}
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, nil, &readBodyError{readErr}
	}
	if closeErr != nil {
		return nil, nil, &readBodyError{closeErr}
	}
	if c.maxBodyBytes > 0 && int64(len(body)) > c.maxBodyBytes {
		return nil, nil, nitter.Errorf(nitter.KindMalformed, opGet,
			"response exceeds %d bytes", c.maxBodyBytes)
	}
	return resp, body, nil
}

// readBodyError marks body read/close failures so callers' errors stay
// transport-level (they are retried like network errors) without leaking
// response content into error text.
type readBodyError struct{ err error }

func (e *readBodyError) Error() string { return "read response body: " + e.err.Error() }
func (e *readBodyError) Unwrap() error { return e.err }

// pace enforces Options.MinInterval between the STARTS of consecutive Get/Post
// calls across all goroutines sharing this client: it sleeps (context-aware)
// for the remainder of the interval, then records this request's start time.
func (c *Client) pace(ctx context.Context) error {
	if c.minInterval <= 0 {
		return nil
	}
	c.paceMu.Lock()
	defer c.paceMu.Unlock()
	start := c.now()
	if !c.lastStart.IsZero() {
		if wait := c.minInterval - start.Sub(c.lastStart); wait > 0 {
			if err := c.sleep(ctx, wait); err != nil {
				return nitter.Errorf(nitter.KindUnavailable, opGet, "pacing wait aborted: %w", err)
			}
			start = c.now()
		}
	}
	c.lastStart = start
	return nil
}

// wait sleeps via the injectable sleep and converts an abort (context
// cancellation) into a classified error mentioning the context.
func (c *Client) wait(ctx context.Context, d time.Duration, what string) error {
	if err := c.sleep(ctx, d); err != nil {
		return nitter.Errorf(nitter.KindUnavailable, opGet, "%s wait aborted: %w", what, err)
	}
	return nil
}

// sleepContext is the default context-aware sleep: it returns early with the
// context error when the caller gives up mid-wait.
func sleepContext(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// parseRetryAfter parses a Retry-After header value: a delay in seconds or
// an HTTP-date. Absent, unparseable, non-positive, or overflow-sized values
// are reported as invalid (ok == false) — absurd input must never become a
// wait, let alone a negative one.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs <= 0 || secs > maxRetryAfterSeconds {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := fhttp.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// sanitizeTransportErr serves the sdk/errors.go redaction contract: net/url
// wrappers render their full request URL — query string included — into
// Error() text ("Get \"https://host/p?token=…\": dial tcp: …"), so they must
// never reach the *nitter.Error chain. Since Go 1.27 the old
// *url.ParseError is unified into *url.Error (Op "parse"), so one
// type-assertion covers both the transport and the request-build paths.
// The wrapper is replaced by its cause, keeping errors.Is/errors.As able to
// reach the real root (dial errors, invalid-character reasons, …); a nil
// cause degrades to a fixed static description.
func sanitizeTransportErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Err != nil {
			return ue.Err
		}
		return errors.New("transport failure")
	}
	return err
}
