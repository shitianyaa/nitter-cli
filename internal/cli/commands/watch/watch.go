// Package watch implements the `nitter watch` command: persistent dedup
// polling of one or more sources, the project's core deliverable. Each cycle
// fetches every source, runs the internal/watch selection engine over the
// result against the persistent per-source state (internal/storage/seen) and
// emits only the new tweets — first runs only record state (只记不推) unless
// --include-existing says otherwise. Data acquisition goes through
// internal/cli/client and sdk models only — per ruling R11 this package
// never imports internal/nitter/*.
//
// Fetch boundary (ruling R18, plan deviation, ledgered): each cycle fetches
// with the standard bounded acquisition (MaxPages budget, Limit 0 = all) and
// Select dedups. For user sources the RSS Min-Id scan stops early once a page
// adds nothing the source has not already seen (rssStop), so a caught-up
// source costs one request instead of the whole page budget; tag/list sources
// keep the plain bounded fetch. Correctness is identical (no duplicates, no
// losses); only the fetch volume differs within the bounded budget.
//
// Merged acquisition sits on top of that boundary without changing it: with
// --no-reposts and at least two user sources on a backend that has a merged
// endpoint (anything but "fx"), prefetchMerged covers them with ONE RSS
// request per batch of handles, and any batch it could not serve is left to
// the per-source fetch below. The merged feed is Nitter-only and cannot
// represent a repost — Nitter attributes a reposted item to the original
// author — which is exactly why it requires --no-reposts.
package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/internal/cli/tweetfilter"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/internal/config/paths"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
	"github.com/shitianyaa/nitter-cli/internal/storage/seen"
	watchengine "github.com/shitianyaa/nitter-cli/internal/watch"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// opWatch is the command name stamped into error envelopes.
const opWatch = "watch"

const (
	// defaultIntervalFlag is the --interval flag default: one cycle per
	// 10 minutes in loop mode.
	defaultIntervalFlag = "10m"
	// minInterval is the smallest loop sleep accepted: the ticker must not
	// spin faster than the fetch pacing can sustain.
	minInterval = time.Second
	// defaultMaxNew caps per-round emission per source when --max-new is
	// unset. The contract leaves the default open; 0 would mean "emit
	// nothing, rebuild the baseline" (engine rule 5) and would make the
	// command never push anything, so it must be positive. 10 absorbs
	// ordinary bursts without flooding the stream; the excess older tweets
	// are still marked seen (engine rule 4, 旧积压可能被跳过) unless
	// --max-new-overflow keep says otherwise.
	defaultMaxNew = 10
)

// The --max-new-overflow values: what happens to new tweets beyond the
// --max-new cap in one cycle. drop (the default) marks the excess seen
// immediately — never re-emitted (宁丢勿重, the plugin's original rule 4);
// keep leaves the excess unseen so the next cycles re-emit it under the
// same cap (宁重勿丢).
const (
	overflowDrop = "drop"
	overflowKeep = "keep"
)

// New builds the `nitter watch [SOURCE...]` command over the shared streams.
//
// Exit codes (repo-wide semantics): a --once cycle with every source healthy
// exits 0 (also: SIGINT/SIGTERM graceful exit, EPIPE on stdout); a --once
// cycle in which at least one source failed exits 1; usage problems (bad
// source string, empty source set, --interval < 1s, negative flags, --json
// without --once, --json with --ndjson) exit 2. Loop mode runs until the
// context is canceled (signals → exit 0) or an unrecoverable error (state
// store, non-EPIPE write failure) exits 1.
func New(s *invocation.Streams) *cobra.Command {
	var (
		intervalFlag       string
		onceFlag           bool
		includeExisting    bool
		maxNewFlag         int
		maxNewOverflowFlag string
		maxPagesFlag       int
		stateDirFlag       string
		asJSON             bool
		asNDJSON           bool
		filters            tweetfilter.Filters
	)
	cmd := &cobra.Command{
		Use:   "watch [SOURCE...]",
		Short: "Poll sources persistently and emit only new tweets (dedup state on disk)",
		Long: `Poll one or more sources in cycles and print only tweets that are new
against the persistent dedup state (~/.nitter-cli/state/seen.json, or
<--state-dir>/seen.json):

  <SOURCE>... is any list of user:<handle>, tag:<query> and list:<id>
  (e.g. user:NASA, tag:#AI, tag:from:nasa, list:12345). The ref after the
  first colon is passed through verbatim to the fetch (handle / search
  query / list ID) and forms the seen key "<kind>:<ref>" — write tag queries
  raw (tag:#AI); the URL-escaped form (tag:%23AI) would be double-escaped on
  the wire and is not valid. With no SOURCE arguments the config's
  [[watch.sources]] entries are used instead; when both are empty this is a
  usage error.

The first cycle of an uninitialized source only RECORDS state — no history
is emitted (只记不推); --include-existing lifts that for a run. Later cycles
emit each source's new tweets: at most --max-new per source per cycle
(newest first). By default the excess beyond the cap is marked seen
immediately and never re-emitted (宁丢勿重); --max-new-overflow keep leaves
it unseen so the next cycles re-emit it under the same cap (宁重勿丢).
--max-new 0 emits nothing and seals the current first page as the new
baseline, regardless of --max-new-overflow. A source whose fetch fails
gets an in-place error report while the other sources continue; its state is
left untouched.

--once runs exactly one cycle and exits: any failed source exits 1
(otherwise 0) — the recommended Hermes form, with the stdout stream
consumed directly. Without --once the command loops, sleeping --interval
(default 10m, at least 1s) between cycles until SIGINT/SIGTERM (graceful
exit 0). A closed stdout pipe (EPIPE) counts as the consumer hanging up and
exits 0 in both modes: when tweets were delivered but the state write was
cut short, the next round re-pushes them (宁重勿丢 — prefer duplicates over
losses).

--ndjson prints one nitter.pipeline/v1 envelope per record: kind tweet
(the tweet as data, provenance meta.source/meta.instance/meta.fetched_at)
and kind error for per-source fetch failures. --json (only with --once)
prints one JSON document instead: {"tweets":[<bare Tweet objects>],
"errors":[{"ref","code","message"}...]} — the cycle's selected tweets and
its per-source fetch failures. Without --once --json is rejected: the
resident loop is a stream of cycles, not one document (use --ndjson).

Field filters (--no-reposts, --media-only, --media-type image|video|gif)
run after the fetch but BEFORE selection/dedup: filtered tweets are not
recorded as seen — they are re-fetched (and re-filtered) every cycle but
never emitted, so filtering cannot grow the state or re-push old tweets.
--max-new counts only tweets that pass the filters. The watermark anchors
the filtered first page. Note the repost marker is only carried by the
HTML parse path, so --no-reposts acts on HTML-sourced tweets.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args, options{
				interval:        intervalFlag,
				once:            onceFlag,
				includeExisting: includeExisting,
				maxNew:          maxNewFlag,
				maxNewOverflow:  maxNewOverflowFlag,
				maxPages:        maxPagesFlag,
				stateDir:        stateDirFlag,
				asJSON:          asJSON,
				asNDJSON:        asNDJSON,
				filters:         filters,
			})
		},
	}
	cmd.Flags().StringVar(&intervalFlag, "interval", defaultIntervalFlag,
		"Loop sleep between cycles, at least 1s (default 10m)")
	cmd.Flags().BoolVar(&onceFlag, "once", false,
		"Run one polling cycle and exit")
	cmd.Flags().BoolVar(&includeExisting, "include-existing", false,
		"Emit the whole first fetch on an uninitialized source (default: first run only records state)")
	cmd.Flags().IntVar(&maxNewFlag, "max-new", defaultMaxNew,
		"Emit at most N new tweets per source per cycle; 0 emits nothing and rebuilds the baseline")
	cmd.Flags().StringVar(&maxNewOverflowFlag, "max-new-overflow", overflowDrop,
		"What happens to new tweets beyond --max-new in a burst: \"drop\" marks the excess seen immediately, never re-emitted (default); \"keep\" leaves it unseen so the next cycles re-emit it under the same cap")
	cmd.Flags().IntVar(&maxPagesFlag, "max-pages", 0,
		"Fetch-page budget per cycle (default: config max_pages; built-in default 5)")
	cmd.Flags().StringVar(&stateDirFlag, "state-dir", "",
		"State directory holding seen.json (default: ~/.nitter-cli/state)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"With --once: print one JSON document {\"tweets\":[...],\"errors\":[{ref,code,message}...]}; rejected without --once (use --ndjson for the envelope stream)")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false,
		"Print one nitter.pipeline/v1 envelope per record (tweets and errors)")
	cmd.Flags().BoolVar(&filters.NoReposts, "no-reposts", false,
		"Drop pure retweets (retweet-header detection) before dedup")
	cmd.Flags().BoolVar(&filters.MediaOnly, "media-only", false,
		"Drop tweets that carry no media attachments, before dedup")
	cmd.Flags().StringVar(&filters.MediaType, "media-type", "",
		"Keep only tweets with at least one media entry of this type: image, video or gif")
	return cmd
}

// options bundles the parsed flag values of one invocation.
type options struct {
	interval        string
	once            bool
	includeExisting bool
	maxNew          int
	maxNewOverflow  string
	maxPages        int
	stateDir        string
	asJSON          bool
	asNDJSON        bool
	filters         tweetfilter.Filters
}

// jsonErrorEntry is one failed source in the --once --json document:
// ref is the source key (meta.input's counterpart), code the SDK error kind
// of the failure (same classification as the NDJSON error envelope) and
// message the redacted error text.
type jsonErrorEntry struct {
	Ref     string `json:"ref"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// jsonDocument is the `watch --once --json` output document (ruling
// R-M10-2): the cycle's selected tweets as bare Tweet objects plus one
// entry per failed source. Both fields are initialized as empty slices so
// an empty cycle marshals as [], never null.
type jsonDocument struct {
	Tweets []nitter.Tweet   `json:"tweets"`
	Errors []jsonErrorEntry `json:"errors"`
}

// pendingPut is one deferred state advance of --once --json mode: buffered
// during the cycle and applied only after the document write succeeded, so
// a failed or interrupted delivery leaves the state unadvanced and the next
// round re-pushes (produce-then-persist, 宁重勿丢).
type pendingPut struct {
	key   string
	state seen.SourceState
}

// onceJSON carries the --once --json accumulation: doc is the output
// document (ruling R-M10-2 — bare Tweet objects plus one {ref, code,
// message} entry per failed source; both slices initialized so an empty
// cycle marshals as [], never null), pending the deferred per-source state
// writes in cycle order.
type onceJSON struct {
	doc     jsonDocument
	pending []pendingPut
}

// run executes the watch command: validate flags, resolve sources (argv,
// else config), open the state store, build the wiring, then run one cycle
// (--once) or the ticker loop.
func run(cmd *cobra.Command, s *invocation.Streams, args []string, opts options) error {
	// --json is defined for --once only: one cycle IS one document. The
	// resident loop is a stream of cycles — a "document" would be ambiguous
	// (one per cycle? the whole run?) — so it stays rejected there.
	var acc *onceJSON
	if opts.asJSON {
		if opts.asNDJSON {
			return invocation.Usagef("watch: --json and --ndjson are mutually exclusive")
		}
		if !opts.once {
			return invocation.Usagef("watch: --json requires --once; without it watch is a stream of cycles, not one JSON document (use --ndjson for the envelope stream)")
		}
		acc = &onceJSON{doc: jsonDocument{Tweets: []nitter.Tweet{}, Errors: []jsonErrorEntry{}}}
	}
	// watch keeps the pre-auto-NDJSON decision table: the M10 pipe default
	// change is declared for the data commands only, so watch's default stays
	// the text rows on a TTY AND in a pipe — --ndjson selects the envelope
	// stream. ModeHuman and ModeText share the row rendering, so one literal
	// covers both non-NDJSON modes. In --once --json mode the collector (acc)
	// routes the records into the document and defers the state writes
	// instead of any stdout stream.
	mode := pipeline.ModeHuman
	if opts.asNDJSON {
		mode = pipeline.ModeNDJSON
	}
	interval, err := parseInterval(opts.interval)
	if err != nil {
		return err
	}
	if opts.maxNew < 0 {
		return invocation.Usagef("watch: --max-new must be >= 0 (0 rebuilds the baseline without emitting)")
	}
	if opts.maxNewOverflow != overflowDrop && opts.maxNewOverflow != overflowKeep {
		return invocation.Usagef("watch: --max-new-overflow must be %q or %q, got %q", overflowDrop, overflowKeep, opts.maxNewOverflow)
	}
	if opts.maxPages < 0 {
		return invocation.Usagef("watch: --max-pages must be >= 0")
	}
	if err := opts.filters.Validate(); err != nil {
		return invocation.Usagef("watch: %v", err)
	}

	// The root PersistentPreRunE has published the baseline config by now;
	// a bad value in it is a usage error (exit 2).
	cfg, err := client.LoadEffectiveSettings()
	if err != nil {
		return err
	}
	sources, err := resolveSources(args, cfg)
	if err != nil {
		return err
	}
	maxPages := cfg.MaxPages
	if cmd.Flags().Changed("max-pages") {
		maxPages = opts.maxPages
	}

	// The state directory must exist before seen.Open: Open itself creates
	// nothing (the first Put is lazy), and the contract requires the dir
	// upfront.
	stateDir := opts.stateDir
	if stateDir == "" {
		p, err := paths.New()
		if err != nil {
			return err
		}
		stateDir = p.StateDir
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	store, err := seen.Open(filepath.Join(stateDir, "seen.json"), time.Now)
	if err != nil {
		return err
	}

	w, err := client.Build(s.RootOptions, cfg, time.Now)
	if err != nil {
		return client.AsUsageError(err)
	}

	ctx := s.CTX
	if ctx == nil {
		ctx = context.Background()
	}
	cycle := cycleOptions{
		includeExisting: opts.includeExisting,
		maxNew:          opts.maxNew,
		keepOverflow:    opts.maxNewOverflow == overflowKeep,
		maxPages:        maxPages,
		mode:            mode,
		collect:         acc,
		filters:         opts.filters,
	}

	if opts.once {
		failed, err := runCycle(ctx, s, store, w, sources, cycle)
		if err != nil {
			return graceful(err)
		}
		if ctx.Err() != nil {
			// Canceled mid-cycle (SIGINT): graceful exit wins over the
			// failed-source summary (and over a partial document). The
			// buffered state writes are dropped with the document — nothing
			// was delivered, so the next round re-pushes (宁重勿丢).
			return nil
		}
		if acc != nil {
			if err := writeJSONDocument(s.Out, &acc.doc); err != nil {
				return graceful(err)
			}
			// The document landed — only NOW advance the state
			// (produce-then-persist): a state-write failure here is a real
			// error (exit 1) whose tweets were delivered and whose
			// un-advanced sources re-push next round, and the failed
			// document write above left the state wholly untouched.
			for _, p := range acc.pending {
				if err := store.Put(p.key, p.state); err != nil {
					return err
				}
			}
		}
		if failed > 0 {
			return fmt.Errorf("watch completed with %d of %d sources failed", failed, len(sources))
		}
		return nil
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := runCycle(ctx, s, store, w, sources, cycle); err != nil {
			return graceful(err)
		}
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			// SIGINT/SIGTERM: graceful exit 0.
			return nil
		case <-ticker.C:
		}
	}
}

// parseInterval parses --interval: a duration of at least one second
// (validated in both modes — the flag's value must be well-formed
// regardless of --once).
func parseInterval(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, invocation.Usagef("watch: --interval must be a duration (e.g. 10m), got %q", raw)
	}
	if d < minInterval {
		return 0, invocation.Usagef("watch: --interval must be >= 1s, got %s", d)
	}
	return d, nil
}

// resolveSources resolves the cycle's source list: argv sources win; with no
// argv sources the config's [[watch.sources]] entries are parsed instead
// (an invalid entry is a usage error naming the entry); when both are empty
// it is a usage error.
func resolveSources(args []string, cfg settings.Settings) ([]watchengine.Source, error) {
	if len(args) > 0 {
		sources := make([]watchengine.Source, 0, len(args))
		for _, arg := range args {
			src, err := watchengine.ParseSource(arg)
			if err != nil {
				return nil, invocation.Usagef("watch: %v", err)
			}
			sources = append(sources, src)
		}
		return sources, nil
	}
	sources := make([]watchengine.Source, 0, len(cfg.WatchSources))
	for _, entry := range cfg.WatchSources {
		src, err := watchengine.ParseSource(entry.ID)
		if err != nil {
			return nil, invocation.Usagef("watch: invalid config [[watch.sources]] entry %q: %v", entry.ID, err)
		}
		sources = append(sources, src)
	}
	if len(sources) == 0 {
		return nil, invocation.Usagef("watch: no sources given and no [[watch.sources]] configured")
	}
	return sources, nil
}

// cycleOptions carries the per-cycle knobs (flag-derived, constant across
// cycles).
type cycleOptions struct {
	includeExisting bool
	maxNew          int
	// keepOverflow is the --max-new-overflow keep policy: tweets beyond the
	// maxNew cap stay unseen so the next cycles re-emit them (engine
	// KeepOverflow).
	keepOverflow bool
	maxPages     int
	mode         pipeline.Mode
	// collect routes the cycle's records into a --once --json document and
	// defers the per-source state writes until the document has landed
	// (nil in every other mode).
	collect *onceJSON
	filters tweetfilter.Filters
}

// runCycle processes every source once, in the given order. It returns the
// number of sources whose fetch failed. Per-source fetch failures never
// abort the cycle — they are reported (a {ref, code, message} entry in the
// --once --json document, an error envelope in NDJSON mode, an
// "error: <key>: <message>" stderr line otherwise) and the source's state is
// left untouched. A returned error is fatal for the whole watch run: in the
// streaming modes a state-store failure or a non-EPIPE stdout write failure
// (--once --json mode buffers both the document and the state writes, so it
// cannot fail here). EPIPE is returned as-is; the caller converts it to a
// graceful exit (pipeline.IsBrokenPipe).
func runCycle(ctx context.Context, s *invocation.Streams, store *seen.Store, w *client.Wiring, sources []watchengine.Source, opts cycleOptions) (int, error) {
	failed := 0
	// One merged RSS request per eligible batch covers the cycle's user
	// sources; a source the prefetch could not serve falls through to its
	// own fetch below, so a failed batch never loses a source.
	prefetched := prefetchMerged(ctx, w, store, sources, opts.filters.NoReposts, opts.maxPages)
	for _, src := range sources {
		if ctx.Err() != nil {
			// Graceful shutdown: stop mid-cycle without writing error
			// reports for sources that never ran.
			return failed, nil
		}
		key := src.Key()
		prev, _ := store.Get(key)
		if opts.collect != nil {
			// A duplicate argv source's earlier occurrence has its state
			// buffered, not yet applied: the buffered state is the effective
			// previous state, so duplicate sources dedup exactly like in the
			// immediate-Put modes.
			for _, p := range opts.collect.pending {
				if p.key == key {
					prev = p.state
				}
			}
		}

		var (
			tweets   []nitter.Tweet
			instance string
			err      error
		)
		if hit, ok := prefetched[key]; ok {
			tweets, instance = hit.tweets, hit.instance
		} else {
			tweets, instance, err = fetchSource(ctx, w, src, opts.maxPages, prev)
		}
		if err != nil {
			if ctx.Err() != nil {
				// The fetch died because the caller gave up (signal during
				// instance rotation): graceful exit, not a source failure.
				return failed, nil
			}
			failed++
			if werr := reportSourceError(s, opts, src, key, err); werr != nil {
				return failed, werr
			}
			continue
		}
		fetchedAt := time.Now().UTC().Format(time.RFC3339)
		// Field filters run after the fetch but BEFORE Select/dedup (the
		// plugin's placement: filters at the fetch stage, before the seen
		// diff). Filtered tweets are never marked seen — each cycle
		// re-fetches and re-filters them without emitting, so filtering
		// cannot grow the state or re-push old tweets. The watermark
		// anchors the FILTERED first page (page-1 of what the pipeline
		// considers), and MaxNew counts only filtered-through tweets.
		tweets = tweetfilter.Apply(tweets, opts.filters)
		res := watchengine.Select(tweets, firstPageIDs(tweets), prev, watchengine.Options{
			Kind:            src.Kind,
			IncludeExisting: opts.includeExisting,
			MaxNew:          opts.maxNew,
			KeepOverflow:    opts.keepOverflow,
		})

		// Produce first, persist after (先产出后落盘): in the streaming modes a
		// write failure below returns before the Put; in --once --json mode
		// the Put is buffered and applied only after the document has
		// landed. Either way a delivery or state-write failure leaves the
		// state unadvanced, so the next round re-pushes this round's tweets
		// — 宁重勿丢.
		if len(res.Tweets) > 0 {
			if werr := emitTweets(s, opts, res.Tweets, key, instance, fetchedAt); werr != nil {
				return failed, werr
			}
		}
		// Persist only when the state actually changed: empty-fetch rounds
		// (Result.State == prev) must not touch the file, so UpdatedAt
		// stays an honest record of the last real advance.
		if stateChanged(res.State, prev) {
			if opts.collect != nil {
				opts.collect.pending = append(opts.collect.pending, pendingPut{key: key, state: res.State})
			} else if err := store.Put(key, res.State); err != nil {
				return failed, err
			}
		}
	}
	return failed, nil
}

// fetchSource dispatches one fetch by source kind. Limit is the
// allTweetsSentinel (= all): the cycle fetches everything within the
// MaxPages budget and lets Select dedup (ruling R18). The sentinel matters:
// the Fx fast lane treats count <= 0 as ZERO tweets with a nil error
// (fxtwitter.FetchUserTimeline's count guard), so the historical limit-0
// call silently fetched nothing and watch recorded an empty baseline.
// allTweetsSentinel must stay far below math.MaxInt: the Fx client uses the
// count as a slice capacity (make([]sdk.Tweet, 0, count)) and a MaxInt cap
// overflows the allocator; it must also stay above the Fx per-page cap
// (100) so the accumulated[:count] truncation never bites, and appapi
// treats limit <= 0 as unbounded, so any positive sentinel keeps the
// nitter backend's semantics unchanged. The returned string is the base
// URL of the instance that produced the result (NDJSON meta.instance
// provenance).
const allTweetsSentinel = math.MaxInt

func fetchSource(ctx context.Context, w *client.Wiring, src watchengine.Source, maxPages int, prev seen.SourceState) ([]nitter.Tweet, string, error) {
	switch src.Kind {
	case watchengine.KindUser:
		return w.Timeline().Timeline(ctx, src.Ref, allTweetsSentinel, maxPages, client.WithRSSStop(rssStop(prev)))
	case watchengine.KindTag:
		return w.Search().Search(ctx, src.Ref, allTweetsSentinel, maxPages)
	case watchengine.KindList:
		return w.List().ListTimeline(ctx, src.Ref, allTweetsSentinel, maxPages)
	default:
		// Unreachable through this command (ParseSource validates the kind
		// at the flag level); kept as the engine contract's backstop.
		return nil, "", nitter.Errorf(nitter.KindInvalidArg, opWatch, "unknown source kind %q", src.Kind)
	}
}

// rssStop builds the RSS scan-stop predicate for one source: the scan ends
// once a page adds no tweet the source has not already seen. An
// uninitialized source has nothing seen, so its scan runs to the page budget
// — the first run records as much history as the budget allows instead of
// silently dropping everything past the first feed page.
func rssStop(prev seen.SourceState) func(page []nitter.Tweet) bool {
	known := make(map[string]bool, len(prev.SeenIDs))
	for _, id := range prev.SeenIDs {
		known[id] = true
	}
	return func(page []nitter.Tweet) bool {
		for _, tw := range page {
			if tw.ID != "" && !known[tw.ID] {
				return false
			}
		}
		return true
	}
}

// mergedPrefetch is one source's slice of a successful merged fetch.
type mergedPrefetch struct {
	tweets   []nitter.Tweet
	instance string
}

// mergedStop builds the batch-level RSS scan-stop predicate: the scan ends
// once a page adds no tweet that ANY member of the batch has not already
// seen. It is the batch-scoped form of the plugin's scan-boundary rule — a
// per-member watermark would stop at the freshest member's and silently
// truncate the others. As with the per-source predicate, a page holding
// nothing new cannot be distinguished from "nothing older is coming"; that
// is the same 宁丢勿重 trade the --max-new cap already makes.
func mergedStop(store *seen.Store, batch []string, byHandle map[string]watchengine.Source) func(page []nitter.Tweet) bool {
	known := make(map[string]map[string]bool, len(batch))
	pending := false
	for _, handle := range batch {
		src, ok := byHandle[strings.ToLower(handle)]
		if !ok {
			continue
		}
		prev, _ := store.Get(src.Key())
		ids := make(map[string]bool, len(prev.SeenIDs))
		for _, id := range prev.SeenIDs {
			ids[id] = true
		}
		known[strings.ToLower(handle)] = ids
		if !prev.Initialized {
			// A member that has never been initialized needs its baseline built
			// from as many pages as the budget allows. Stopping because the OTHER
			// members are caught up would truncate the new member's history — the
			// single-source scan has the same rule (nothing seen ⇒ never stop
			// early), and a batch must not weaken it.
			pending = true
		}
	}
	return func(page []nitter.Tweet) bool {
		if pending {
			return false
		}
		for _, tw := range page {
			ids, ok := known[strings.ToLower(tw.Author.Handle)]
			if !ok || tw.ID == "" {
				continue
			}
			if !ids[tw.ID] {
				return false
			}
		}
		return true
	}
}

// prefetchMerged fetches the cycle's eligible user sources through the merged
// RSS endpoint and returns their tweets keyed by seen key. It returns an
// empty map when merging is not eligible, and simply omits a batch whose
// fetch failed — the caller's per-source fetch covers it, so a merged
// failure never loses a source.
//
// Eligibility: --no-reposts (the merged feed cannot represent a repost — the
// item is attributed to the original author) and at least two user sources.
// The FxTwitter backend has no merged endpoint, so fetch_backend "fx" never
// merges; "mix" does, which means those sources are served by Nitter instead
// of the fast lane (a batch failure falls back to the per-source path, where
// mix tries fx first again).
func prefetchMerged(ctx context.Context, w *client.Wiring, store *seen.Store, sources []watchengine.Source, noReposts bool, maxPages int) map[string]mergedPrefetch {
	out := make(map[string]mergedPrefetch)
	if !noReposts || w.FetchBackend == "fx" {
		return out
	}
	byHandle := make(map[string]watchengine.Source)
	handles := make([]string, 0, len(sources))
	for _, src := range sources {
		if src.Kind != watchengine.KindUser {
			continue
		}
		// Normalize exactly like MergedBatches does. Without this the batch
		// carries the bare handle while byHandle keeps the raw ref, so a
		// "user:@NASA" source would look up "@nasa" and be dropped from the
		// prefetch (it then falls back to the per-source path, which rejects the
		// leading "@" — a wasted merged request and a confusing report).
		handle := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src.Ref), "@"))
		if handle == "" {
			continue
		}
		handles = append(handles, handle)
		byHandle[strings.ToLower(handle)] = src
	}
	if len(handles) < 2 {
		return out
	}
	for _, batch := range client.MergedBatches(handles) {
		if ctx.Err() != nil {
			return out
		}
		buckets, instance, err := w.MergedTimeline().MergedTimeline(ctx, batch, maxPages, mergedStop(store, batch, byHandle))
		if err != nil {
			continue
		}
		for _, handle := range batch {
			src, ok := byHandle[strings.ToLower(handle)]
			if !ok {
				continue
			}
			tweets := buckets[strings.ToLower(handle)]
			if len(tweets) == 0 {
				continue
			}
			out[src.Key()] = mergedPrefetch{tweets: tweets, instance: instance}
		}
	}
	return out
}

// firstPageIDs slices the watermark supply off the fetch result: the first
// min(20, len) tweet IDs (the fetch arrives newest-first), then filtered to
// pure-numeric IDs, deduplicated and capped by seen.CapWatermark.
func firstPageIDs(fetched []nitter.Tweet) []string {
	first := fetched
	if len(first) > seen.WatermarkLimit {
		first = first[:seen.WatermarkLimit]
	}
	ids := make([]string, 0, len(first))
	for _, tw := range first {
		ids = append(ids, tw.ID)
	}
	return seen.CapWatermark(ids)
}

// reportSourceError reports one source's fetch failure: in --once --json
// mode an {ref, code, message} entry appended to the document; an in-place
// error envelope on the NDJSON stream (command "watch", stage "fetch",
// code = the SDK error Kind of the failure — extracted with errors.As, so
// wrapped *nitter.Error values classify too; "error" as the fallback for
// anything not a classified SDK error — meta.input = the source key); or a
// plain stderr line in the default modes. The source kind ("user"/"tag"/
// "list") is never the code: it is already visible in ref / meta.input.
// Stderr writes ignore errors like everywhere else.
func reportSourceError(s *invocation.Streams, opts cycleOptions, src watchengine.Source, key string, err error) error {
	if opts.collect != nil {
		opts.collect.doc.Errors = append(opts.collect.doc.Errors, jsonErrorEntry{
			Ref:     key,
			Code:    errorCodeOf(err),
			Message: err.Error(),
		})
		return nil
	}
	if opts.mode == pipeline.ModeNDJSON {
		return pipeline.WriteErrorEnvelope(s.Out, opWatch, "fetch", errorCodeOf(err), key, err.Error())
	}
	fmt.Fprintf(s.Err, "error: %s: %s\n", key, err)
	return nil
}

// errorCodeOf classifies a fetch failure for machine output: the SDK error
// Kind when the error is (or wraps) a *nitter.Error, "error" as the
// fallback for anything unclassified.
func errorCodeOf(err error) string {
	code := "error"
	var terr *nitter.Error
	if errors.As(err, &terr) {
		code = string(terr.Kind)
	}
	return code
}

// writeJSONDocument marshals the --once --json document as exactly one JSON
// line (jsonx semantics: one trailing newline) and writes it.
func writeJSONDocument(w io.Writer, doc *jsonDocument) error {
	b, err := jsonx.MarshalLine(doc)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// emitTweets delivers one round's selected tweets: in --once --json mode
// they are appended to the document as bare Tweet objects; in NDJSON mode
// one nitter.pipeline/v1 tweet envelope each (meta.source = the source key,
// meta.instance = the producing instance, meta.fetched_at = RFC3339 UTC);
// otherwise one TweetRow line each in the default modes.
func emitTweets(s *invocation.Streams, opts cycleOptions, tweets []nitter.Tweet, key, instance, fetchedAt string) error {
	if opts.collect != nil {
		opts.collect.doc.Tweets = append(opts.collect.doc.Tweets, tweets...)
		return nil
	}
	if opts.mode == pipeline.ModeNDJSON {
		for _, tw := range tweets {
			env := pipeline.Envelope{
				Schema: pipeline.Schema,
				Kind:   pipeline.KindTweet,
				ID:     tw.ID,
				Data:   tw,
				Meta: &pipeline.Meta{
					Source:    key,
					Instance:  instance,
					FetchedAt: fetchedAt,
				},
			}
			if err := pipeline.WriteEnvelope(s.Out, env); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range result.TweetRows(tweets) {
		if _, err := fmt.Fprintln(s.Out, r.Line()); err != nil {
			return err
		}
	}
	return nil
}

// stateChanged shallow-compares the engine's returned state against prev
// (Initialized/SeenIDs/WatermarkIDs; UpdatedAt is stamped by Store.Put and
// never compared). True exactly when the round advanced or initialized the
// source — empty-fetch rounds compare equal and skip the persist.
func stateChanged(next, prev seen.SourceState) bool {
	if next.Initialized != prev.Initialized {
		return true
	}
	if !slices.Equal(next.SeenIDs, prev.SeenIDs) {
		return true
	}
	if !slices.Equal(next.WatermarkIDs, prev.WatermarkIDs) {
		return true
	}
	return false
}

// graceful maps a run-cycle error to the command's exit semantics: a broken
// stdout pipe is the consumer hanging up (pixiv sigpipe policy — exit 0),
// everything else is a real failure.
func graceful(err error) error {
	if pipeline.IsBrokenPipe(err) {
		return nil
	}
	return err
}
