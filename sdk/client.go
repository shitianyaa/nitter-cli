// Instance rotation and client composition for the public SDK.
//
// Dependency boundary (frozen): this package must NOT import
// internal/nitter/*. internal/nitter/protocol/httpx imports this package for
// the shared error kinds, so importing it back would be an import cycle.
// The transport is therefore an interface defined HERE (Transport) and
// satisfied structurally by *httpx.Client; the CLI wiring layer (later
// milestone) injects the real transport through WithHTTPClient.
package nitter

import (
	"context"
	"time"
)

// opChooser and opNew are the Op values stamped on chooser and New errors.
const (
	opChooser = "chooser"
	opNew     = "nitter.New"
)

const (
	// defaultCooldown is the instance failure cooldown (spec: instance
	// cooldown 60s, configurable via WithCooldown).
	defaultCooldown = 60 * time.Second
	// defaultMaxPages caps cursor pagination in later fetch methods (spec:
	// --max-pages default 5).
	defaultMaxPages = 5
)

// Instance is the SDK-side projection of one configured Nitter instance,
// independent of the config layer's representation. Username and Password
// are the optional instance credentials (Nitter basic auth); they are never
// echoed into errors (redaction contract).
type Instance struct {
	URL      string
	Username string
	Password string
}

// Transport is the SDK's narrow HTTP boundary: exactly what fetch methods
// need from a client, nothing else. *httpx.Client (the real tls-client
// transport, internal/nitter/protocol/httpx) satisfies it structurally; the
// interface exists here because the sdk cannot import httpx (httpx imports
// this package for the shared error kinds — see the file comment). Tests
// inject a fake to stay hermetic.
type Transport interface {
	Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error)
}

// Options configures a Client for New. Apply the With* constructors; a nil
// Options passed to New is rejected as an invalid argument.
type Options func(*clientConfig)

// clientConfig accumulates Options before New composes the Client.
type clientConfig struct {
	instances []Instance
	transport Transport
	cooldown  time.Duration
	// cooldownSet distinguishes WithCooldown(0) ("never block") from an
	// unset option (default 60s).
	cooldownSet bool
}

// WithInstances sets the rotation set. The slice is copied; later caller
// mutation cannot leak into the client. An empty list is valid: New
// succeeds, but every future Pick (and therefore every future fetch) fails
// with KindUnavailable "no instances configured".
func WithInstances(in []Instance) Options {
	return func(c *clientConfig) {
		c.instances = make([]Instance, len(in))
		copy(c.instances, in)
	}
}

// WithHTTPClient injects the transport (test seams and CLI wiring). When it
// is not supplied the Client has no transport: New still succeeds, and the
// fetch methods added in later milestones will fail with KindLocalState
// until the wiring injects one. See Transport for why the boundary is an
// interface rather than *httpx.Client.
func WithHTTPClient(t Transport) Options {
	return func(c *clientConfig) {
		c.transport = t
	}
}

// WithCooldown overrides the instance failure cooldown (default 60s). Zero
// or negative means failures are still marked but never block a pick.
func WithCooldown(d time.Duration) Options {
	return func(c *clientConfig) {
		c.cooldown = d
		c.cooldownSet = true
	}
}

// Client is the public composition root of the SDK: an instance Chooser plus
// the HTTP transport the fetch methods (added in later milestones) run
// against. It is safe for concurrent use provided the injected Transport is.
type Client struct {
	chooser   *Chooser
	transport Transport
	maxPages  int
}

// New composes a Client.
//
// Defaults: instance cooldown 60s (override with WithCooldown), pagination
// cap 5 pages. With no Options the Client has no instances and no transport:
// New succeeds, but every future Pick fails with KindUnavailable "no
// instances configured", and every future fetch fails until the CLI wiring
// injects instances and a Transport.
func New(opts ...Options) (*Client, error) {
	var cfg clientConfig
	for _, opt := range opts {
		if opt == nil {
			return nil, Errorf(KindInvalidArg, opNew, "nil Options")
		}
		opt(&cfg)
	}

	cooldown := defaultCooldown
	if cfg.cooldownSet {
		cooldown = cfg.cooldown
	}

	return &Client{
		chooser:   NewChooser(cfg.instances, cooldown, time.Now),
		transport: cfg.transport,
		maxPages:  defaultMaxPages,
	}, nil
}
