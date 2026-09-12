// Package client is the CLI wiring layer: it composes, once per invocation,
// everything a command needs to acquire data from the configured Nitter
// instances, and hands capabilities to commands.
//
// Package boundary (ruling R11, frozen): this is the ONLY CLI-layer package
// allowed to import internal/nitter/{appapi,protocol/httpx} and internal/media.
// Command packages consume data exclusively through sdk (package twitter)
// models plus the capability types exported here (InstanceTester, TestOptions)
// — they never import internal/nitter/* or internal/media themselves.
package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/twitter-cli/internal/config/paths"
	"github.com/shitianyaa/twitter-cli/internal/config/settings"
	"github.com/shitianyaa/twitter-cli/internal/media"
	"github.com/shitianyaa/twitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/twitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// opBuild is the Op stamped on Build's own errors.
const opBuild = "client.Build"

// wiringTimeout is the per-request timeout of the shared transport. The
// request pacing, retries and backoff come from settings.
const wiringTimeout = 20 * time.Second

// TestOptions re-exports the appapi probe options so command packages can
// name the type without importing internal/nitter/appapi (R11 boundary).
type TestOptions = appapi.TestOptions

// InstanceTester is the instance-probe capability a command consumes. It is
// backed by *appapi.Client (structurally); commands depend on this interface,
// never on appapi.
type InstanceTester interface {
	TestInstance(ctx context.Context, baseURL string, opts TestOptions) (twitter.InstanceReport, error)
}

// TimelineSource is the user-timeline acquisition capability a command
// consumes. Parameters are primitives — no appapi type reaches a command
// package (R11). Backed by *appapi.Client through timelineAdapter, because
// the appapi method's options-struct signature cannot satisfy this
// interface directly.
//
// The second return value is the base URL of the instance that produced the
// result ("" on error): NDJSON meta.instance needs the provenance, and only
// the acquisition layer knows which instance won the rotation.
type TimelineSource interface {
	Timeline(ctx context.Context, handle string, limit, maxPages int) ([]twitter.Tweet, string, error)
}

// SearchSource is the search acquisition capability a command consumes
// (same provenance contract as TimelineSource). Backed by *appapi.Client
// through searchAdapter.
type SearchSource interface {
	Search(ctx context.Context, query string, limit, maxPages int) ([]twitter.Tweet, string, error)
}

// ListSource is the list-timeline acquisition capability a command consumes
// (same provenance contract as TimelineSource). Backed by *appapi.Client
// through listAdapter.
type ListSource interface {
	ListTimeline(ctx context.Context, listID string, limit, maxPages int) ([]twitter.Tweet, string, error)
}

// StatusSource is the single-status acquisition capability a command
// consumes (same provenance contract as TimelineSource). Backed by
// *appapi.Client through statusAdapter.
type StatusSource interface {
	Status(ctx context.Context, ref string) (twitter.Tweet, string, error)
}

// MediaResolver is the media-resolution capability a command consumes. The
// parameters are primitives — no internal/media type reaches a command
// package (R11; same rule as TimelineSource, adapted internally). Backed by
// *media.Resolver through mediaAdapter.
//
//   - id/user are the parsed status reference (client.ParseStatusRef's
//     outputs); strategies are the wire strategy names ("fx", "vx",
//     "syndication", "nitter", "xdown") in try order; quality is
//     "high"|"medium"|"low". The first strategy that yields media wins; an
//     aggregate error naming every attempted strategy otherwise.
//   - ProbeMedia is the best-effort --probe enrichment for one direct media
//     URL (duration from the mp4 head, size from the Content-Range total);
//     every error is a "probe unavailable" signal, never a run failure.
type MediaResolver interface {
	ResolveMedia(ctx context.Context, id, user string, strategies []string, quality string) ([]twitter.MediaResolution, error)
	ProbeMedia(ctx context.Context, mediaURL string) (durationSeconds float64, sizeBytes int64, err error)
}

// ParseStatusRef re-exports the appapi status-reference parser so commands
// can validate a ref locally (exit 2 before any wiring is built, as the
// user command does for handles) without importing internal/nitter/appapi
// (R11 boundary; same re-export precedent as TestOptions).
func ParseStatusRef(s string) (id, user string, err error) {
	return appapi.ParseStatusRef(s)
}

// Wiring carries everything a command needs to acquire data, built once from
// persistent flags + settings. Transport, Chooser and AppAPI share one
// composition: the appapi client wraps the same transport and clock, and its
// chooser is the wiring chooser. The media resolver shares the transport too,
// so the third-party media requests ride the same pacing, retries and proxy.
type Wiring struct {
	AppAPI    *appapi.Client
	Transport *httpx.Client
	Chooser   *twitter.Chooser
	Instances []twitter.Instance

	// now and nitterBase feed the lazily built media resolver (Media()):
	// the resolver clock, and the instance base the nitter strategy fetches
	// status pages from (the first configured instance — --instance replaces
	// the whole set, so instances[0] already reflects it). Empty when no
	// instance is configured; the nitter strategy then reports local state.
	now        func() time.Time
	nitterBase string
}

// Tester returns the instance-probe capability as the narrow interface
// commands consume (R11: commands never import appapi).
func (w *Wiring) Tester() InstanceTester { return w.AppAPI }

// timelineAdapter bridges the primitive-parameter TimelineSource to the
// appapi method's PageOptions signature.
type timelineAdapter struct{ app *appapi.Client }

func (a timelineAdapter) Timeline(ctx context.Context, handle string, limit, maxPages int) ([]twitter.Tweet, string, error) {
	return a.app.Timeline(ctx, handle, appapi.PageOptions{Limit: limit, MaxPages: maxPages})
}

// Timeline returns the user-timeline acquisition capability as the narrow
// interface commands consume (R11: commands never import appapi).
func (w *Wiring) Timeline() TimelineSource { return timelineAdapter{w.AppAPI} }

// searchAdapter bridges the primitive-parameter SearchSource to the appapi
// method's PageOptions signature.
type searchAdapter struct{ app *appapi.Client }

func (a searchAdapter) Search(ctx context.Context, query string, limit, maxPages int) ([]twitter.Tweet, string, error) {
	return a.app.Search(ctx, query, appapi.PageOptions{Limit: limit, MaxPages: maxPages})
}

// Search returns the search acquisition capability as the narrow interface
// commands consume (R11: commands never import appapi).
func (w *Wiring) Search() SearchSource { return searchAdapter{w.AppAPI} }

// listAdapter bridges the primitive-parameter ListSource to the appapi
// method's PageOptions signature.
type listAdapter struct{ app *appapi.Client }

func (a listAdapter) ListTimeline(ctx context.Context, listID string, limit, maxPages int) ([]twitter.Tweet, string, error) {
	return a.app.ListTimeline(ctx, listID, appapi.PageOptions{Limit: limit, MaxPages: maxPages})
}

// List returns the list-timeline acquisition capability as the narrow
// interface commands consume (R11: commands never import appapi).
func (w *Wiring) List() ListSource { return listAdapter{w.AppAPI} }

// statusAdapter bridges the primitive-parameter StatusSource to the appapi
// method (same shape — kept for symmetry with the other adapters).
type statusAdapter struct{ app *appapi.Client }

func (a statusAdapter) Status(ctx context.Context, ref string) (twitter.Tweet, string, error) {
	return a.app.Status(ctx, ref)
}

// Status returns the single-status acquisition capability as the narrow
// interface commands consume (R11: commands never import appapi).
func (w *Wiring) Status() StatusSource { return statusAdapter{w.AppAPI} }

// mediaAdapter bridges the primitive-parameter MediaResolver to the
// internal/media resolver's typed Options signature.
type mediaAdapter struct {
	res        *media.Resolver
	nitterBase string
}

func (a mediaAdapter) ResolveMedia(ctx context.Context, id, user string, strategies []string, quality string) ([]twitter.MediaResolution, error) {
	sts := make([]media.Strategy, len(strategies))
	for i, s := range strategies {
		sts[i] = media.Strategy(s)
	}
	return a.res.ResolveStatus(ctx, media.StatusRef{ID: id, User: user}, media.Options{
		Strategies: sts,
		Quality:    quality,
		NitterBase: a.nitterBase,
	})
}

func (a mediaAdapter) ProbeMedia(ctx context.Context, mediaURL string) (float64, int64, error) {
	return a.res.Probe(ctx, mediaURL)
}

// Media returns the media-resolution capability as the narrow interface
// commands consume (R11: commands never import internal/media). The resolver
// shares the wiring's transport; nil now is defaulted (a wiring built by
// Build always carries a clock).
func (w *Wiring) Media() MediaResolver {
	now := w.now
	if now == nil {
		now = time.Now
	}
	return mediaAdapter{res: &media.Resolver{HTTP: w.Transport, Now: now}, nitterBase: w.nitterBase}
}

// Build composes the wiring.
//
//   - Proxy: rootOpts.Proxy when set, else cfg.Proxy; "" disables it. The
//     scheme is validated up front (http, https, socks5, socks5h) — invalid
//     input fails as *twitter.Error KindInvalidArg before anything is
//     constructed, so bad input never creates state.
//   - Durations: parsed here from the settings strings. settings.Load
//     validates and defaults them, so a parse failure (including an empty
//     string, i.e. a zero-value Settings that never went through Load) means
//     Build received unvalidated settings and is reported as a plain error —
//     no silent fallback that could diverge from the documented config
//     defaults (the CLI maps non-usage build failures to exit 1).
//   - Instances: the cfg projection, or the single rootOpts.Instance URL when
//     the --instance override is set (its documented meaning: the instance
//     set for this invocation). Credentials are carried but, in the MVP, not
//     wired to the transport — probes and fetches run unauthenticated. The
//     first instance doubles as the media resolver's nitter strategy base.
//   - A nil now defaults to time.Now; a nil rootOpts is treated as unset
//     flags (tests and programmatic callers).
func Build(rootOpts *invocation.RootOptions, cfg settings.Settings, now func() time.Time) (*Wiring, error) {
	if rootOpts == nil {
		rootOpts = &invocation.RootOptions{}
	}
	proxy := cfg.Proxy
	if rootOpts.Proxy != "" {
		proxy = rootOpts.Proxy
	}
	if err := validateProxyScheme(proxy); err != nil {
		return nil, err
	}
	retryDelay, err := parseDuration("retry_delay", cfg.RetryDelay)
	if err != nil {
		return nil, err
	}
	minInterval, err := parseDuration("request_interval", cfg.RequestInterval)
	if err != nil {
		return nil, err
	}
	cooldown, err := parseDuration("instance_cooldown", cfg.InstanceCooldown)
	if err != nil {
		return nil, err
	}

	instances := make([]twitter.Instance, len(cfg.Instances))
	for i, in := range cfg.Instances {
		instances[i] = twitter.Instance{URL: in.URL, Username: in.Username, Password: in.Password}
	}
	if rootOpts.Instance != "" {
		instances = []twitter.Instance{{URL: rootOpts.Instance}}
	}

	if now == nil {
		now = time.Now
	}
	transport, err := httpx.New(httpx.Options{
		Proxy:         proxy,
		Timeout:       wiringTimeout,
		RetryAttempts: cfg.RetryAttempts,
		RetryDelay:    retryDelay,
		MinInterval:   minInterval,
		Now:           now,
	})
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}
	chooser := twitter.NewChooser(instances, cooldown, now)
	nitterBase := ""
	if len(instances) > 0 {
		nitterBase = instances[0].URL
	}
	return &Wiring{
		AppAPI:     &appapi.Client{HTTP: transport, Chooser: chooser, Now: now},
		Transport:  transport,
		Chooser:    chooser,
		Instances:  instances,
		now:        now,
		nitterBase: nitterBase,
	}, nil
}

// validateProxyScheme accepts the proxy schemes the tls-client transport
// supports; "" means disabled. Errors name the scheme only — a proxy URL may
// embed credentials, which must never reach an error message.
func validateProxyScheme(proxy string) error {
	if proxy == "" {
		return nil
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return twitter.Errorf(twitter.KindInvalidArg, opBuild, "invalid proxy URL: scheme must be http, https, socks5 or socks5h")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return twitter.Errorf(twitter.KindInvalidArg, opBuild, "unsupported proxy scheme %q (must be http, https, socks5 or socks5h)", u.Scheme)
	}
}

// parseDuration parses one of the duration-valued settings keys.
func parseDuration(key, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse config %s: %w", key, err)
	}
	return d, nil
}

// LoadEffectiveSettings resolves the config file location and loads settings
// with env > file > default precedence. A *settings.ValidationError (bad
// values in file or env) is mapped to invocation.UsageError so it exits 2;
// read/parse failures stay plain errors (exit 1).
func LoadEffectiveSettings() (settings.Settings, error) {
	p, err := paths.New()
	if err != nil {
		return settings.Settings{}, err
	}
	cfg, err := settings.Load(p.ConfigFile, os.Getenv)
	if err != nil {
		var verr *settings.ValidationError
		if errors.As(err, &verr) {
			return settings.Settings{}, &invocation.UsageError{Err: err}
		}
		return settings.Settings{}, err
	}
	return cfg, nil
}

// AsUsageError maps sdk invalid-argument errors (invalid proxy scheme,
// invalid instance URL, empty probe user) to invocation.UsageError so they
// exit 2; every other error — transport build failures, local-state errors —
// passes through unchanged (exit 1).
func AsUsageError(err error) error {
	var terr *twitter.Error
	if errors.As(err, &terr) && terr.Kind == twitter.KindInvalidArg {
		return &invocation.UsageError{Err: err}
	}
	return err
}
