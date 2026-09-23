// Package client is the CLI wiring layer: it composes, once per invocation,
// everything a command needs to acquire data — through the FxTwitter fast
// lane and/or the configured Nitter instances, per `fetch_backend` — and
// hands capabilities to commands.
//
// Package boundary (ruling R11, frozen): this is the ONLY CLI-layer package
// allowed to import internal/nitter/{appapi,protocol/httpx},
// internal/fxtwitter and internal/media.
// Command packages consume data exclusively through sdk (package nitter)
// models plus the capability types exported here (InstanceTester, TestOptions)
// — they never import internal/nitter/* or internal/media themselves.
package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/config/paths"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
	"github.com/shitianyaa/nitter-cli/internal/media"
	"github.com/shitianyaa/nitter-cli/internal/nitter/appapi"
	"github.com/shitianyaa/nitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// opBuild is the Op stamped on Build's own errors.
const opBuild = "client.Build"

// wiringTimeout is the per-request timeout of the shared transport. The
// request pacing, retries and backoff come from settings.
const wiringTimeout = 20 * time.Second

// UnboundedMaxPages asks the acquisition layer to follow the HTML cursor
// chain without a page budget: pagination stops only when the upstream
// stops serving a cursor (or the context is canceled). `user --max-pages 0`
// passes this sentinel; a positive value stays a hard page cap, and 0 (the
// omitted flag — e.g. watch, or the config's max_pages) keeps the built-in
// default of 5.
const UnboundedMaxPages = -1

// TestOptions re-exports the appapi probe options so command packages can
// name the type without importing internal/nitter/appapi (R11 boundary).
type TestOptions = appapi.TestOptions

// InstanceTester is the instance-probe capability a command consumes. It is
// backed by *appapi.Client (structurally); commands depend on this interface,
// never on appapi.
type InstanceTester interface {
	TestInstance(ctx context.Context, baseURL string, opts TestOptions) (nitter.InstanceReport, error)
}

// TimelineOptions configures timeline acquisition.
type TimelineOptions struct {
	WithReplies bool
	MediaOnly   bool
	// RSSStop ends the Nitter RSS Min-Id scan early: the command layer
	// supplies it so a caught-up source costs one request instead of the
	// whole page budget. The FxTwitter backend has no RSS scan and ignores
	// it.
	RSSStop func(page []nitter.Tweet) bool
}

// TimelineOption sets an option on TimelineOptions.
type TimelineOption func(*TimelineOptions)

// WithReplies includes replies in user timeline when true.
func WithReplies(v bool) TimelineOption {
	return func(o *TimelineOptions) { o.WithReplies = v }
}

// WithMediaOnly requests fast-lane media-only retrieval when true.
func WithMediaOnly(v bool) TimelineOption {
	return func(o *TimelineOptions) { o.MediaOnly = v }
}

// WithRSSStop sets the RSS scan-stop predicate (see TimelineOptions.RSSStop).
func WithRSSStop(fn func(page []nitter.Tweet) bool) TimelineOption {
	return func(o *TimelineOptions) { o.RSSStop = fn }
}

// TimelineSource is the user-timeline acquisition capability a command
// consumes. Parameters are primitives — no appapi type reaches a command
// package (R11). Backed by Wiring through timelineAdapter.
//
// The second return value is the base URL of the instance that produced the
// result ("" on error), or "FxTwitter" when FxTwitter produced the result:
// NDJSON meta.instance needs the provenance.
type TimelineSource interface {
	Timeline(ctx context.Context, handle string, limit, maxPages int, opts ...TimelineOption) ([]nitter.Tweet, string, error)
}

// SearchSource is the search acquisition capability a command consumes
// (same provenance contract as TimelineSource). Backed by *appapi.Client
// and *fxtwitter.Client through searchAdapter.
type SearchSource interface {
	Search(ctx context.Context, query string, limit, maxPages int) ([]nitter.Tweet, string, error)
	SearchUsers(ctx context.Context, query string, count int) ([]nitter.Profile, error)
}

// ListSource is the list-timeline acquisition capability a command consumes
// (same provenance contract as TimelineSource). Backed by *appapi.Client
// through listAdapter.
type ListSource interface {
	ListTimeline(ctx context.Context, listID string, limit, maxPages int) ([]nitter.Tweet, string, error)
}

// StatusSource is the single-status acquisition capability a command
// consumes (same provenance contract as TimelineSource). Backed by
// *fxtwitter.Client and *appapi.Client through statusAdapter.
type StatusSource interface {
	Status(ctx context.Context, ref string) (nitter.Tweet, string, error)
}

// QuotesSource is the quote-tweets acquisition capability a command consumes.
// Backed by *fxtwitter.Client through quotesAdapter.
type QuotesSource interface {
	Quotes(ctx context.Context, statusID string, count int, cursor string) ([]nitter.Tweet, string, error)
}

// TrendsSource is the trending-topics acquisition capability a command consumes.
// Backed by *fxtwitter.Client through trendsAdapter.
type TrendsSource interface {
	Trends(ctx context.Context) ([]nitter.Trend, error)
}

// ProfileSource is the user-profile acquisition capability a command consumes.
// Backed by *fxtwitter.Client through profileAdapter.
type ProfileSource interface {
	Profile(ctx context.Context, handle string) (*nitter.Profile, error)
}

// FollowingSource is the user-following acquisition capability a command
// consumes. Backed by *fxtwitter.Client through followingAdapter.
type FollowingSource interface {
	Following(ctx context.Context, handle string, limit int, cursor string) ([]nitter.Profile, string, error)
}

// ConversationSource is the conversation acquisition capability a command
// consumes. Backed by *fxtwitter.Client through conversationAdapter.
type ConversationSource interface {
	Conversation(ctx context.Context, statusID string, rankingMode string, cursor string) (*nitter.Conversation, error)
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
	ResolveMedia(ctx context.Context, id, user string, strategies []string, quality string) ([]nitter.MediaResolution, error)
	ProbeMedia(ctx context.Context, mediaURL string) (durationSeconds float64, sizeBytes int64, err error)
}

// ParseStatusRef re-exports the appapi status-reference parser so commands
// can validate a ref locally (exit 2 before any wiring is built, as the
// user command does for handles) without importing internal/nitter/appapi
// (R11 boundary; same re-export precedent as TestOptions).
func ParseStatusRef(s string) (id, user string, err error) {
	return appapi.ParseStatusRef(s)
}

// PlannedFile re-exports media.PlannedFile — one file a download plan will
// fetch — so the download command can consume DownloadPlanner's output
// without importing internal/media (R11 boundary; same re-export precedent
// as TestOptions).
type PlannedFile = media.PlannedFile

// ErrFileExists re-exports media.ErrFileExists: the download command detects
// an existing target with errors.Is to apply --on-exists refuse/skip without
// importing internal/media (R11 boundary; same re-export precedent).
var ErrFileExists = media.ErrFileExists

// MediaDownloader is the media-download capability a command consumes: one
// URL, one target file, streaming over the shared transport. Parameters are
// primitives and the result is the sdk DownloadRecord — no internal/media
// type reaches a command package (R11; the adapter converts). Backed by
// *media.Downloader through downloaderAdapter.
//
//   - FetchToFile writes finalPath (the extension is the caller's: the plan
//     carried one). An existing finalPath fails with an error wrapping
//     ErrFileExists unless force is set; force overwrites through the same
//     atomic temp-then-rename flow.
//   - FetchToFileAuto is the extension-derivation path for planned files
//     whose URL carries none (ruling R-M9-4): the extension comes from the
//     response's Content-Type, falling back to defaultExt (the caller's
//     kind-based default), and the record's Path carries the derived final
//     path — the only place it exists.
type MediaDownloader interface {
	FetchToFile(ctx context.Context, url, finalPath string, force bool) (nitter.DownloadRecord, error)
	FetchToFileAuto(ctx context.Context, url, basePath, defaultExt string, force bool) (nitter.DownloadRecord, error)
}

// DownloadPlanner is the download-planning capability a command consumes:
// media.PlanDownload behind the interface (R11 — the command package never
// imports internal/media). Backed by planAdapter.
type DownloadPlanner interface {
	PlanDownload(refID string, res []nitter.MediaResolution, quality, kindFilter string) ([]PlannedFile, error)
}

// ExistsPath extracts the already-existing final path a download existence
// refusal carries, reporting whether the error is one (errors.Is
// ErrFileExists) AND names its path. The --on-exists skip mode needs the
// path to stat the on-disk size: for the auto-extension path the derived
// path exists nowhere else, since only the response's Content-Type decided
// it.
func ExistsPath(err error) (string, bool) {
	if !errors.Is(err, ErrFileExists) {
		return "", false
	}
	var pe interface {
		error
		Path() string
	}
	if errors.As(err, &pe) {
		return pe.Path(), true
	}
	return "", false
}

// Wiring carries everything a command needs to acquire data, built once from
// persistent flags + settings. Transport, Chooser and AppAPI share one
// composition: the appapi client wraps the same transport and clock, and its
// chooser is the wiring chooser. The media resolver shares the transport too,
// so the third-party media requests ride the same pacing, retries and proxy.
type Wiring struct {
	AppAPI       *appapi.Client
	Fx           *fxtwitter.Client
	FetchBackend string
	Transport    *httpx.Client
	Chooser      *nitter.Chooser
	Instances    []nitter.Instance

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

var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

// timelineAdapter bridges TimelineSource to the hybrid dispatcher.
type timelineAdapter struct{ w *Wiring }

func (a timelineAdapter) Timeline(ctx context.Context, handle string, limit, maxPages int, opts ...TimelineOption) ([]nitter.Tweet, string, error) {
	if !handleRe.MatchString(handle) {
		return nil, "", nitter.Errorf(nitter.KindInvalidArg, "client.Timeline", "handle must be 1-15 letters, digits or underscores (without the @)")
	}

	var opt TimelineOptions
	for _, fn := range opts {
		fn(&opt)
	}

	backend := a.w.FetchBackend
	if backend == "" {
		backend = "mix"
	}

	switch backend {
	case "nitter":
		return a.timelineNitter(ctx, handle, limit, maxPages, opt)
	case "fx":
		return a.timelineFx(ctx, handle, limit, maxPages, opt)
	case "mix":
		tweets, instance, err := a.timelineFx(ctx, handle, limit, maxPages, opt)
		if err == nil {
			return tweets, instance, nil
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, "", err
		}
		return a.timelineNitter(ctx, handle, limit, maxPages, opt)
	default:
		return a.timelineNitter(ctx, handle, limit, maxPages, opt)
	}
}

func (a timelineAdapter) timelineNitter(ctx context.Context, handle string, limit, maxPages int, opt TimelineOptions) ([]nitter.Tweet, string, error) {
	return a.w.AppAPI.Timeline(ctx, handle, appapi.PageOptions{Limit: limit, MaxPages: maxPages, Stop: opt.RSSStop})
}

func (a timelineAdapter) timelineFx(ctx context.Context, handle string, limit, maxPages int, opt TimelineOptions) ([]nitter.Tweet, string, error) {
	if a.w.Fx == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, "client.Timeline", "no fxtwitter client wired")
	}

	var tweets []nitter.Tweet
	var err error

	if opt.MediaOnly {
		tweets, _, err = a.w.Fx.FetchUserMedia(ctx, handle, limit, "", maxPages)
	} else {
		tweets, _, err = a.w.Fx.FetchUserTimeline(ctx, handle, limit, "", maxPages, false, false)
	}
	if err != nil {
		return nil, "", err
	}

	if !opt.WithReplies {
		filtered := make([]nitter.Tweet, 0, len(tweets))
		for _, tw := range tweets {
			if tw.ReplyTo == "" {
				filtered = append(filtered, tw)
			}
		}
		tweets = filtered
	}

	// Deterministic order: sort by tweet ID descending (timeline order) so
	// the same input always produces the same output sequence — the Fx media
	// endpoint's page composition may fluctuate between runs, but the result
	// set is stable once ordered. Truncate to limit afterwards (limit 0 =
	// all). Purely client-side and documented in the CLI reference.
	sort.SliceStable(tweets, func(i, j int) bool {
		a, b := tweetIDNum(tweets[i].ID), tweetIDNum(tweets[j].ID)
		if a != b {
			return a > b
		}
		return tweets[i].ID > tweets[j].ID
	})
	if limit > 0 && len(tweets) > limit {
		tweets = tweets[:limit]
	}

	return tweets, "FxTwitter", nil
}

// tweetIDNum parses a numeric status ID as an unsigned integer; unparsable
// IDs sort as zero (ties then fall back to the string comparison).
func tweetIDNum(id string) uint64 {
	v, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// Timeline returns the user-timeline acquisition capability as the narrow
// interface commands consume (R11: commands never import appapi).
func (w *Wiring) Timeline() TimelineSource { return timelineAdapter{w: w} }

// searchAdapter bridges SearchSource to the hybrid dispatcher.
type searchAdapter struct{ w *Wiring }

func (a searchAdapter) Search(ctx context.Context, query string, limit, maxPages int) ([]nitter.Tweet, string, error) {
	if strings.TrimSpace(query) == "" {
		return nil, "", nitter.Errorf(nitter.KindInvalidArg, "client.Search", "query cannot be empty")
	}

	backend := a.w.FetchBackend
	if backend == "" {
		backend = "mix"
	}

	switch backend {
	case "nitter":
		return a.searchNitter(ctx, query, limit, maxPages)
	case "fx":
		return a.searchFx(ctx, query, limit)
	case "mix":
		tweets, instance, err := a.searchFx(ctx, query, limit)
		if err == nil {
			return tweets, instance, nil
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, "", err
		}
		return a.searchNitter(ctx, query, limit, maxPages)
	default:
		return a.searchNitter(ctx, query, limit, maxPages)
	}
}

func (a searchAdapter) searchNitter(ctx context.Context, query string, limit, maxPages int) ([]nitter.Tweet, string, error) {
	return a.w.AppAPI.Search(ctx, query, appapi.PageOptions{Limit: limit, MaxPages: maxPages})
}

func (a searchAdapter) searchFx(ctx context.Context, query string, limit int) ([]nitter.Tweet, string, error) {
	if a.w.Fx == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, "client.Search", "no fxtwitter client wired")
	}
	tweets, _, err := a.w.Fx.SearchTweets(ctx, query, limit, "", "latest")
	if err != nil {
		return nil, "", err
	}
	return tweets, "FxTwitter", nil
}

func (a searchAdapter) SearchUsers(ctx context.Context, query string, count int) ([]nitter.Profile, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nitter.Errorf(nitter.KindInvalidArg, "client.SearchUsers", "query cannot be empty")
	}
	if a.w.Fx == nil {
		return nil, nitter.Errorf(nitter.KindLocalState, "client.SearchUsers", "no fxtwitter client wired")
	}
	return a.w.Fx.SearchUsers(ctx, query, count)
}

// Search returns the search acquisition capability as the narrow interface
// commands consume (R11: commands never import appapi).
func (w *Wiring) Search() SearchSource { return searchAdapter{w: w} }

// listAdapter bridges the primitive-parameter ListSource to the appapi
// method's PageOptions signature.
type listAdapter struct{ w *Wiring }

func (a listAdapter) ListTimeline(ctx context.Context, listID string, limit, maxPages int) ([]nitter.Tweet, string, error) {
	return a.w.AppAPI.ListTimeline(ctx, listID, appapi.PageOptions{Limit: limit, MaxPages: maxPages})
}

// List returns the list-timeline acquisition capability as the narrow
// interface commands consume (R11: commands never import appapi).
func (w *Wiring) List() ListSource { return listAdapter{w: w} }

// statusAdapter bridges the primitive-parameter StatusSource to the hybrid dispatcher.
type statusAdapter struct{ w *Wiring }

func (a statusAdapter) Status(ctx context.Context, ref string) (nitter.Tweet, string, error) {
	backend := a.w.FetchBackend
	if backend == "" {
		backend = "mix"
	}

	if backend == "mix" || backend == "fx" {
		statusID, _, err := ParseStatusRef(ref)
		if err != nil {
			return nitter.Tweet{}, "", err
		}
		if a.w.Fx != nil {
			tw, err := a.w.Fx.FetchStatus(ctx, statusID)
			if err == nil && tw != nil {
				return *tw, "FxTwitter", nil
			}
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nitter.Tweet{}, "", err
			}
		}
		return a.w.AppAPI.Status(ctx, ref)
	}

	return a.w.AppAPI.Status(ctx, ref)
}

// Status returns the single-status acquisition capability as the narrow
// interface commands consume (R11: commands never import appapi).
func (w *Wiring) Status() StatusSource { return statusAdapter{w: w} }

type quotesAdapter struct{ w *Wiring }

func (a quotesAdapter) Quotes(ctx context.Context, statusID string, count int, cursor string) ([]nitter.Tweet, string, error) {
	if a.w.Fx == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, "client.Quotes", "no fxtwitter client wired")
	}
	return a.w.Fx.FetchQuotes(ctx, statusID, count, cursor)
}

// Quotes returns the quotes acquisition capability.
func (w *Wiring) Quotes() QuotesSource { return quotesAdapter{w: w} }

type trendsAdapter struct{ w *Wiring }

func (a trendsAdapter) Trends(ctx context.Context) ([]nitter.Trend, error) {
	if a.w.Fx == nil {
		return nil, nitter.Errorf(nitter.KindLocalState, "client.Trends", "no fxtwitter client wired")
	}
	return a.w.Fx.FetchTrends(ctx)
}

// Trends returns the trends acquisition capability.
func (w *Wiring) Trends() TrendsSource { return trendsAdapter{w: w} }

type profileAdapter struct{ w *Wiring }

func (a profileAdapter) Profile(ctx context.Context, handle string) (*nitter.Profile, error) {
	if a.w.Fx == nil {
		return nil, nitter.Errorf(nitter.KindLocalState, "client.Profile", "no fxtwitter client wired")
	}
	return a.w.Fx.FetchUserProfile(ctx, handle)
}

// Profile returns the profile acquisition capability.
func (w *Wiring) Profile() ProfileSource { return profileAdapter{w: w} }

type followingAdapter struct{ w *Wiring }

func (a followingAdapter) Following(ctx context.Context, handle string, limit int, cursor string) ([]nitter.Profile, string, error) {
	if a.w.Fx == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, "client.Following", "no fxtwitter client wired")
	}
	return a.w.Fx.FetchUserFollowing(ctx, handle, limit, cursor)
}

// Following returns the following acquisition capability.
func (w *Wiring) Following() FollowingSource { return followingAdapter{w: w} }

type conversationAdapter struct{ w *Wiring }

func (a conversationAdapter) Conversation(ctx context.Context, statusID string, rankingMode string, cursor string) (*nitter.Conversation, error) {
	if a.w.Fx == nil {
		return nil, nitter.Errorf(nitter.KindLocalState, "client.Conversation", "no fxtwitter client wired")
	}
	return a.w.Fx.FetchConversation(ctx, statusID, rankingMode, cursor)
}

// Conversation returns the conversation acquisition capability.
func (w *Wiring) Conversation() ConversationSource { return conversationAdapter{w: w} }

// mediaAdapter bridges the primitive-parameter MediaResolver to the
// internal/media resolver's typed Options signature.
type mediaAdapter struct {
	res        *media.Resolver
	nitterBase string
}

func (a mediaAdapter) ResolveMedia(ctx context.Context, id, user string, strategies []string, quality string) ([]nitter.MediaResolution, error) {
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

// downloaderAdapter bridges the record-returning MediaDownloader to the
// internal/media downloader's DownloadResult (a four-field conversion; no
// media type crosses to a command package).
type downloaderAdapter struct {
	d *media.Downloader
}

func (a downloaderAdapter) FetchToFile(ctx context.Context, url, finalPath string, force bool) (nitter.DownloadRecord, error) {
	res, err := a.d.FetchToFile(ctx, url, finalPath, force)
	return downloadRecord(res), err
}

func (a downloaderAdapter) FetchToFileAuto(ctx context.Context, url, basePath, defaultExt string, force bool) (nitter.DownloadRecord, error) {
	res, err := a.d.FetchToFileAuto(ctx, url, basePath, defaultExt, force)
	return downloadRecord(res), err
}

func downloadRecord(res media.DownloadResult) nitter.DownloadRecord {
	return nitter.DownloadRecord{Path: res.Path, URL: res.URL, Bytes: res.Bytes, SHA256: res.SHA256}
}

// Downloader returns the media-download capability as the narrow interface
// commands consume (R11: commands never import internal/media). The
// downloader shares the wiring's transport, so downloads ride the same
// pacing budget and proxy as resolution and probes.
func (w *Wiring) Downloader() MediaDownloader {
	return downloaderAdapter{d: &media.Downloader{HTTP: w.Transport}}
}

// planAdapter backs DownloadPlanner with media.PlanDownload (a pure
// function, so the adapter is stateless).
type planAdapter struct{}

func (planAdapter) PlanDownload(refID string, res []nitter.MediaResolution, quality, kindFilter string) ([]PlannedFile, error) {
	return media.PlanDownload(refID, res, quality, kindFilter)
}

// Planner returns the download-planning capability as the narrow interface
// commands consume (R11: commands never import internal/media).
func (w *Wiring) Planner() DownloadPlanner {
	return planAdapter{}
}

// Build composes the wiring.
//
//   - Proxy: rootOpts.Proxy when set, else cfg.Proxy; "" disables it. The
//     scheme is validated up front (http, https, socks5, socks5h) — invalid
//     input fails as *nitter.Error KindInvalidArg before anything is
//     constructed, so bad input never creates state.
//   - Durations: parsed here from the settings strings. settings.Load
//     validates and defaults them, so a parse failure (including an empty
//     string, i.e. a zero-value Settings that never went through Load) means
//     Build received unvalidated settings and is reported as a plain error —
//     no silent fallback that could diverge from the documented config
//     defaults (the CLI maps non-usage build failures to exit 1).
//   - Instances: the cfg projection, or the single rootOpts.Instance URL when
//     the --instance override is set (its documented meaning: the instance
//     set for this invocation). Complete credentials become a host-scoped
//     basic-auth policy on the shared transport (see the basicAuth block
//     below): instance-host fetches authenticate, third-party endpoints
//     never see the credential. The first instance doubles as the media
//     resolver's nitter strategy base.
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

	instances := make([]nitter.Instance, len(cfg.Instances))
	for i, in := range cfg.Instances {
		instances[i] = nitter.Instance{URL: in.URL, Username: in.Username, Password: in.Password}
	}
	if rootOpts.Instance != "" {
		instances = []nitter.Instance{{URL: rootOpts.Instance}}
	}

	// Instance basic auth: configured credentials become a HOST-SCOPED
	// transport policy (httpx.Options.BasicAuth) — requests whose URL host
	// matches the instance's own host carry the Authorization header; every
	// other host sharing this transport (the third-party media endpoints)
	// structurally cannot receive the credential (httpx/auth.go). A complete
	// user+password pair is required: a username or password alone is
	// treated as unconfigured (an empty credential half is never sent). The
	// --instance flag override replaces the whole instance set and carries
	// no credentials (no flag exists for them), so overridden runs stay
	// unauthenticated. Duplicate hosts: the last configured entry wins.
	basicAuth := make(map[string]httpx.BasicCredentials, len(instances))
	for _, in := range instances {
		if in.Username == "" || in.Password == "" {
			continue
		}
		u, err := url.Parse(in.URL)
		if err != nil || u.Host == "" {
			// No host to key credentials on; the bad URL fails at
			// request-build time with the usual classified error.
			continue
		}
		basicAuth[strings.ToLower(u.Host)] = httpx.BasicCredentials{Username: in.Username, Password: in.Password}
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
		BasicAuth:     basicAuth,
		Now:           now,
	})
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}
	chooser := nitter.NewChooser(instances, cooldown, now)
	nitterBase := ""
	if len(instances) > 0 {
		nitterBase = instances[0].URL
	}

	fetchBackend := cfg.FetchBackend
	if fetchBackend == "" {
		fetchBackend = "mix"
	}
	if rootOpts != nil && rootOpts.Instance != "" {
		fetchBackend = "nitter"
	}
	switch fetchBackend {
	case "mix", "nitter", "fx":
	default:
		return nil, fmt.Errorf("invalid fetch_backend %q (allowed: mix, nitter, fx)", fetchBackend)
	}

	var fxOpts []fxtwitter.Option
	fxOpts = append(fxOpts, fxtwitter.WithTimeout(wiringTimeout))
	if fxtwitter.EndpointOverrides.BaseURL != "" {
		fxOpts = append(fxOpts, fxtwitter.WithBaseURL(fxtwitter.EndpointOverrides.BaseURL))
	}
	fxHTTPClient := &http.Client{
		Timeout: wiringTimeout,
	}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			fxHTTPClient.Transport = &http.Transport{
				Proxy: http.ProxyURL(u),
			}
		}
	}
	fxOpts = append(fxOpts, fxtwitter.WithHTTPClient(fxHTTPClient))
	fxClient := fxtwitter.NewClient(fxOpts...)

	return &Wiring{
		AppAPI:       &appapi.Client{HTTP: transport, Chooser: chooser, Now: now},
		Fx:           fxClient,
		FetchBackend: fetchBackend,
		Transport:    transport,
		Chooser:      chooser,
		Instances:    instances,
		now:          now,
		nitterBase:   nitterBase,
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
		return nitter.Errorf(nitter.KindInvalidArg, opBuild, "invalid proxy URL: scheme must be http, https, socks5 or socks5h")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return nitter.Errorf(nitter.KindInvalidArg, opBuild, "unsupported proxy scheme %q (must be http, https, socks5 or socks5h)", u.Scheme)
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
	var terr *nitter.Error
	if errors.As(err, &terr) && terr.Kind == nitter.KindInvalidArg {
		return &invocation.UsageError{Err: err}
	}
	return err
}

// SetFxBaseURLForTesting sets the FxTwitter base URL override for unit tests.
// The returned cleanup function restores the previous value.
func SetFxBaseURLForTesting(rawURL string) func() {
	prev := fxtwitter.EndpointOverrides.BaseURL
	fxtwitter.EndpointOverrides.BaseURL = rawURL
	return func() {
		fxtwitter.EndpointOverrides.BaseURL = prev
	}
}
