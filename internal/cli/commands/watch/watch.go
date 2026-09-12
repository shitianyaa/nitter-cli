// Package watch implements the `twitter watch` command: persistent dedup
// polling of one or more sources, the project's core deliverable. Each cycle
// fetches every source, runs the internal/watch selection engine over the
// result against the persistent per-source state (internal/storage/seen) and
// emits only the new tweets — first runs only record state (只记不推) unless
// --include-existing says otherwise. Data acquisition goes through
// internal/cli/client and sdk models only — per ruling R11 this package
// never imports internal/nitter/*.
//
// Fetch boundary (ruling R18, plan deviation, ledgered): each cycle fetches
// with the standard bounded acquisition (MaxPages budget, Limit 0 = all)
// and Select dedups; the plugin-style early-stop paging at the watermark is
// deferred post-MVP. Correctness is identical (no duplicates, no losses);
// only the fetch volume differs within the bounded budget.
package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/twitter-cli/internal/cli/client"
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/twitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/twitter-cli/internal/cli/result"
	"github.com/shitianyaa/twitter-cli/internal/cli/tweetfilter"
	"github.com/shitianyaa/twitter-cli/internal/config/paths"
	"github.com/shitianyaa/twitter-cli/internal/config/settings"
	"github.com/shitianyaa/twitter-cli/internal/storage/seen"
	watchengine "github.com/shitianyaa/twitter-cli/internal/watch"
	"github.com/shitianyaa/twitter-cli/sdk"
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
	// are still marked seen (engine rule 4, 旧积压可能被跳过).
	defaultMaxNew = 10
)

// New builds the `twitter watch [SOURCE...]` command over the shared streams.
//
// Exit codes (repo-wide semantics): a --once cycle with every source healthy
// exits 0 (also: SIGINT/SIGTERM graceful exit, EPIPE on stdout); a --once
// cycle in which at least one source failed exits 1; usage problems (bad
// source string, empty source set, --interval < 1s, negative flags, --json)
// exit 2. Loop mode runs until the context is canceled (signals → exit 0) or
// an unrecoverable error (state store, non-EPIPE write failure) exits 1.
func New(s *invocation.Streams) *cobra.Command {
	var (
		intervalFlag    string
		onceFlag        bool
		includeExisting bool
		maxNewFlag      int
		maxPagesFlag    int
		stateDirFlag    string
		asJSON          bool
		asNDJSON        bool
		filters         tweetfilter.Filters
	)
	cmd := &cobra.Command{
		Use:   "watch [SOURCE...]",
		Short: "Poll sources persistently and emit only new tweets (dedup state on disk)",
		Long: `Poll one or more sources in cycles and print only tweets that are new
against the persistent dedup state (~/.twitter-cli/state/seen.json, or
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
(newest first; seen still advances with every new id), 0 emits nothing and
seals the current first page as the new baseline. A source whose fetch fails
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

--ndjson prints one twitter.pipeline/v1 envelope per record: kind tweet
(the tweet as data, provenance meta.source/meta.instance/meta.fetched_at)
and kind error for per-source fetch failures. --json is rejected: watch is
a stream of mixed tweets and errors, which is not a single JSON document.

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
	cmd.Flags().IntVar(&maxPagesFlag, "max-pages", 0,
		"Fetch-page budget per cycle (default: config max_pages; built-in default 5)")
	cmd.Flags().StringVar(&stateDirFlag, "state-dir", "",
		"State directory holding seen.json (default: ~/.twitter-cli/state)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Not supported: watch streams NDJSON (usage error; use --ndjson)")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false,
		"Print one twitter.pipeline/v1 envelope per record (tweets and errors)")
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
	maxPages        int
	stateDir        string
	asJSON          bool
	asNDJSON        bool
	filters         tweetfilter.Filters
}

// run executes the watch command: validate flags, resolve sources (argv,
// else config), open the state store, build the wiring, then run one cycle
// (--once) or the ticker loop.
func run(cmd *cobra.Command, s *invocation.Streams, args []string, opts options) error {
	// watch is a stream of mixed tweets and errors — never one JSON
	// document. --json is registered precisely to fail with guidance.
	if opts.asJSON {
		return invocation.Usagef("watch: --json is not supported; watch streams NDJSON (use --ndjson)")
	}
	mode, err := pipeline.ResolveOutputMode(opts.asNDJSON, false, s.OutIsTTY)
	if err != nil {
		return err
	}
	interval, err := parseInterval(opts.interval)
	if err != nil {
		return err
	}
	if opts.maxNew < 0 {
		return invocation.Usagef("watch: --max-new must be >= 0 (0 rebuilds the baseline without emitting)")
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
		maxPages:        maxPages,
		mode:            mode,
		filters:         opts.filters,
	}

	if opts.once {
		failed, err := runCycle(ctx, s, store, w, sources, cycle)
		if err != nil {
			return graceful(err)
		}
		if ctx.Err() != nil {
			// Canceled mid-cycle (SIGINT): graceful exit wins over the
			// failed-source summary.
			return nil
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
	maxPages        int
	mode            pipeline.Mode
	filters         tweetfilter.Filters
}

// runCycle processes every source once, in the given order. It returns the
// number of sources whose fetch failed. Per-source fetch failures never
// abort the cycle — they are reported (error envelope in NDJSON mode, an
// "error: <key>: <message>" stderr line otherwise) and the source's state is
// left untouched. A returned error is fatal for the whole watch run: a
// state-store failure or a non-EPIPE stdout write failure. EPIPE is returned
// as-is; the caller converts it to a graceful exit (pipeline.IsBrokenPipe).
func runCycle(ctx context.Context, s *invocation.Streams, store *seen.Store, w *client.Wiring, sources []watchengine.Source, opts cycleOptions) (int, error) {
	failed := 0
	for _, src := range sources {
		if ctx.Err() != nil {
			// Graceful shutdown: stop mid-cycle without writing error
			// reports for sources that never ran.
			return failed, nil
		}
		key := src.Key()
		prev, _ := store.Get(key)

		tweets, instance, err := fetchSource(ctx, w, src, opts.maxPages)
		if err != nil {
			if ctx.Err() != nil {
				// The fetch died because the caller gave up (signal during
				// instance rotation): graceful exit, not a source failure.
				return failed, nil
			}
			failed++
			if werr := reportSourceError(s, opts.mode, src, key, err); werr != nil {
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
		})

		// Produce first, persist after (先产出后落盘): a write failure below
		// returns before the Put, so the next round re-pushes this round's
		// tweets — 宁重勿丢.
		if len(res.Tweets) > 0 {
			if werr := emitTweets(s, opts.mode, res.Tweets, key, instance, fetchedAt); werr != nil {
				return failed, werr
			}
		}
		// Persist only when the state actually changed: empty-fetch rounds
		// (Result.State == prev) must not touch the file, so UpdatedAt
		// stays an honest record of the last real advance.
		if stateChanged(res.State, prev) {
			if err := store.Put(key, res.State); err != nil {
				return failed, err
			}
		}
	}
	return failed, nil
}

// fetchSource dispatches one fetch by source kind. Limit is 0 (= all): the
// cycle fetches everything within the MaxPages budget and lets Select dedup
// (ruling R18). The returned string is the base URL of the instance that
// produced the result (NDJSON meta.instance provenance).
func fetchSource(ctx context.Context, w *client.Wiring, src watchengine.Source, maxPages int) ([]twitter.Tweet, string, error) {
	switch src.Kind {
	case watchengine.KindUser:
		return w.Timeline().Timeline(ctx, src.Ref, 0, maxPages)
	case watchengine.KindTag:
		return w.Search().Search(ctx, src.Ref, 0, maxPages)
	case watchengine.KindList:
		return w.List().ListTimeline(ctx, src.Ref, 0, maxPages)
	default:
		// Unreachable through this command (ParseSource validates the kind
		// at the flag level); kept as the engine contract's backstop.
		return nil, "", twitter.Errorf(twitter.KindInvalidArg, opWatch, "unknown source kind %q", src.Kind)
	}
}

// firstPageIDs slices the watermark supply off the fetch result: the first
// min(20, len) tweet IDs (the fetch arrives newest-first), then filtered to
// pure-numeric IDs, deduplicated and capped by seen.CapWatermark.
func firstPageIDs(fetched []twitter.Tweet) []string {
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

// reportSourceError reports one source's fetch failure: an in-place error
// envelope on the NDJSON stream (command "watch", stage "fetch", code = the
// SDK error Kind of the failure — extracted with errors.As, so wrapped
// *twitter.Error values classify too; "error" as the fallback for anything
// not a classified SDK error — meta.input = the source key), or a plain
// stderr line in the default modes. The source kind ("user"/"tag"/"list")
// is never the code: it is already visible in meta.input. Stderr writes
// ignore errors like everywhere else.
func reportSourceError(s *invocation.Streams, mode pipeline.Mode, src watchengine.Source, key string, err error) error {
	if mode == pipeline.ModeNDJSON {
		code := "error"
		var terr *twitter.Error
		if errors.As(err, &terr) {
			code = string(terr.Kind)
		}
		return pipeline.WriteErrorEnvelope(s.Out, opWatch, "fetch", code, key, err.Error())
	}
	fmt.Fprintf(s.Err, "error: %s: %s\n", key, err)
	return nil
}

// emitTweets delivers one round's selected tweets: one twitter.pipeline/v1
// tweet envelope each in NDJSON mode (meta.source = the source key,
// meta.instance = the producing instance, meta.fetched_at = RFC3339 UTC), or
// one TweetRow line each in the default modes.
func emitTweets(s *invocation.Streams, mode pipeline.Mode, tweets []twitter.Tweet, key, instance, fetchedAt string) error {
	if mode == pipeline.ModeNDJSON {
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
