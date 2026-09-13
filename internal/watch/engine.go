// Package watch implements the dedup selection engine of the watch mode: a
// pure function that decides which fetched tweets to emit for one source in
// one polling round, and how the persistent seen state advances.
//
// Semantics ground truth is the AstrBot plugin this CLI ports, in particular
// its scheduler scan loop (runner.py / runner_seen.py) and the spec §7 rule
// table:
//
//   - First run only records state and never emits history (首跑只记不推),
//     unless the caller explicitly opts in via IncludeExisting.
//   - On an uninitialized source an empty first fetch is kind-dependent:
//     user sources initialize with empty state (plugin runner.py:979-985)
//     so the next round's tweets are pushed, while tag/list sources stay
//     uninitialized (plugin runner.py:948-971) so a transient empty
//     response cannot seal a whole page as history.
//   - An initialized source whose successful fetch came back empty keeps
//     its previous state wholesale, watermark anchors included (plugin
//     runner.py:849-858 返回空结果，保留当前水位; spec §7 保留旧水位).
//   - New tweets are fetched IDs not in the seen list, in timeline order.
//   - MaxNew > 0 caps the emission to the newest N. Following the plugin,
//     the default (drop) still advances seen with ALL new ids — the plugin
//     marks the excess older tweets as seen immediately ("the excess is
//     marked seen ... so the advanced scan watermark cannot silently drop
//     it next round"), so they are intentionally never re-emitted (旧积压
//     可能被跳过). KeepOverflow opts out of that: only the emitted prefix
//     is marked seen, so the excess re-emits on the following cycles under
//     the same cap (宁重勿丢; a burst larger than two caps drains over
//     several cycles).
//   - MaxNew == 0 emits nothing and rebuilds the page-1 baseline: the
//     baseline IDs are merged into seen (plugin _commit_baseline_rebuild →
//     _rebuild_scan_baseline), sealing the current first page. This rule is
//     independent of the overflow policy — the round never reaches emission,
//     so KeepOverflow does not disable the baseline seal.
//   - The watermark is always the capped numeric-only page-1 IDs, replaced
//     — never merged — on every round that rebuilds state.
//   - The engine never fabricates timestamps: Result.State carries
//     UpdatedAt over from prev (zero value for states built from a fresh
//     source) and Store.Put stamps the authoritative UTC time on persist.
//
// The package performs no IO and owns no clock; persistence lives in
// internal/storage/seen.
package watch

import (
	"github.com/shitianyaa/nitter-cli/internal/storage/seen"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// Source kinds for Options.Kind. The MVP source set is exactly these three.
const (
	KindUser = "user"
	KindTag  = "tag"
	KindList = "list"
)

// Options tunes one Select round. On the first run only IncludeExisting and
// Kind apply (MaxNew is a per-round emission cap for initialized sources;
// the first run seeds the full fetch either way).
type Options struct {
	// IncludeExisting emits the whole first fetch on an uninitialized
	// source (default false: 只记不推).
	IncludeExisting bool
	// Kind is the source kind: KindUser, KindTag or KindList. It only
	// decides what an uninitialized source does with an empty first fetch:
	// user sources initialize with empty state (plugin runner.py:979-985,
	// so the next round's tweets are pushed), tag/list sources stay
	// uninitialized (plugin runner.py:948-971, so a transient empty
	// response cannot seal a whole page as history). An empty Kind is
	// treated as KindUser — the MVP source set is exactly these three
	// kinds; unknown values behave like tag/list (stay uninitialized).
	Kind string
	// MaxNew caps emitted new tweets for initialized sources: >0 emits at
	// most N (newest first) while seen still advances with every new id;
	// ==0 emits nothing and rebuilds the baseline from the first page;
	// <0 is treated as ==0 (flag-level validation belongs to Task 19).
	MaxNew int
	// KeepOverflow selects the MaxNew overflow policy. false (drop, the
	// default) marks the tweets beyond the cap seen immediately — they are
	// never re-emitted (the plugin's 宁丢勿重). true (keep) marks only the
	// emitted prefix seen, so the excess stays unseen and the next cycles
	// re-fetch and re-emit it under the same cap until it drains (宁重勿丢);
	// a burst larger than twice the cap therefore takes several cycles to
	// drain. The policy only governs capped emission rounds (MaxNew > 0):
	// the MaxNew == 0 baseline rebuild and the first-run seeding are
	// unaffected, and the watermark always keeps tracking what was fetched
	// (the first page), not what was emitted.
	KeepOverflow bool
}

// Result is one round's outcome: the tweets the caller may deliver, the
// state to persist after delivery, and whether the round allows emission at
// all (false = first-run seeding, a MaxNew == 0 stop, or an empty-fetch
// round that changed nothing).
type Result struct {
	Tweets  []nitter.Tweet
	State   seen.SourceState
	Emitted bool
}

// Select picks the tweets to emit for one source and the state to persist
// afterwards. fetched is this round's fetch in timeline order (newest
// first); firstPageIDs are the first-page status IDs the caller sliced off
// the fetch result (watermark rebuild supply). prev is the persisted state
// of the source.
//
// The returned state must only be persisted after the tweets have been
// delivered (准备/抓取失败不落盘): on fetch or delivery failure the caller
// discards Result entirely and keeps the previous state.
func Select(fetched []nitter.Tweet, firstPageIDs []string, prev seen.SourceState, opts Options) Result {
	watermark := seen.CapWatermark(firstPageIDs)

	if !prev.Initialized {
		seedIDs := make([]string, 0, len(fetched))
		for _, tweet := range fetched {
			if tweet.ID != "" {
				seedIDs = append(seedIDs, tweet.ID)
			}
		}
		if len(seedIDs) == 0 {
			if firstRunInitializesEmpty(opts.Kind) {
				// User source: an empty first fetch still initializes with
				// empty state (plugin runner.py:979-985 seeds an empty
				// seen), so the next round's tweets are selected and pushed
				// instead of being silently swallowed by the record-only
				// first run (the runner.py:940-947 warning scenario).
				return Result{
					State: seen.SourceState{
						Initialized:  true,
						SeenIDs:      seen.MergeSeen(nil, prev.SeenIDs),
						WatermarkIDs: watermark,
					},
				}
			}
			// Tag/list: the plugin's empty/filtered-empty first-run
			// handling (runner.py:948-971) is tag/list-specific — a
			// transient empty first response must not initialize the
			// source. Keep prev verbatim; nothing happened.
			return Result{State: prev}
		}
		state := seen.SourceState{
			Initialized:  true,
			SeenIDs:      seen.MergeSeen(seedIDs, prev.SeenIDs),
			WatermarkIDs: watermark,
		}
		if opts.IncludeExisting {
			return Result{Tweets: fetched, State: state, Emitted: true}
		}
		return Result{State: state}
	}

	if len(fetched) == 0 {
		// Empty successful fetch: nothing happened this round — keep the
		// previous state wholesale. The watermark anchors in particular
		// must NOT be rebuilt to empty (plugin runner.py:849-858
		// 返回空结果，保留当前水位; spec §7). This also short-circuits the
		// MaxNew == 0 baseline rebuild below, which would blank the
		// watermark.
		return Result{State: prev}
	}

	if opts.MaxNew <= 0 {
		// No per-round emission: rebuild the baseline and skip this round
		// (plugin max==0 path — the baseline ids are merged into seen so
		// the current first page is sealed; 旧积压可能被跳过).
		return Result{
			State: seen.SourceState{
				Initialized:  true,
				SeenIDs:      seen.MergeSeen(watermark, prev.SeenIDs),
				WatermarkIDs: watermark,
			},
		}
	}

	known := make(map[string]struct{}, len(prev.SeenIDs))
	for _, id := range prev.SeenIDs {
		known[id] = struct{}{}
	}

	var newTweets []nitter.Tweet
	newIDs := make([]string, 0, len(fetched))
	for _, tweet := range fetched {
		if tweet.ID == "" {
			continue
		}
		if _, dup := known[tweet.ID]; dup {
			continue
		}
		newTweets = append(newTweets, tweet)
		newIDs = append(newIDs, tweet.ID)
	}

	emitted := newTweets
	if len(newTweets) > opts.MaxNew {
		emitted = newTweets[:opts.MaxNew]
	}

	// Seen advances with the ids the round commits to. The plugin's default
	// marks ALL new ids seen immediately — the excess older tweets are
	// sealed at discovery time and never re-found (宁丢勿重). KeepOverflow
	// opts out: only the emitted prefix is marked seen, so the excess stays
	// unseen and re-emits on the following cycles (宁重勿丢), draining
	// newest-first under the same cap. The watermark is unaffected either
	// way — it tracks what was fetched (the first page), not what was
	// emitted. (emitted is a prefix of newTweets, so its ids are exactly
	// newIDs[:len(emitted)]; without a cap the two are identical.)
	if opts.KeepOverflow {
		newIDs = newIDs[:len(emitted)]
	}
	return Result{
		Tweets: emitted,
		State: seen.SourceState{
			Initialized:  true,
			SeenIDs:      seen.MergeSeen(newIDs, prev.SeenIDs),
			WatermarkIDs: watermark,
		},
		Emitted: true,
	}
}

// firstRunInitializesEmpty reports whether an uninitialized source of the
// given kind initializes with empty state on an empty first fetch. Only
// user sources do (plugin runner.py:979-985); tag/list — and any unknown
// kind, conservatively — stay uninitialized (plugin runner.py:948-971).
func firstRunInitializesEmpty(kind string) bool {
	return kind == "" || kind == KindUser
}
