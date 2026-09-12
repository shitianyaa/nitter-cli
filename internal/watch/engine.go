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
//   - A genuinely empty first fetch does not initialize the source, so a
//     transient empty response cannot seal a whole page as history.
//   - New tweets are fetched IDs not in the seen list, in timeline order.
//   - MaxNew > 0 caps the emission to the newest N; following the plugin,
//     seen still advances with ALL new ids — the plugin marks the excess
//     older tweets as seen immediately ("the excess is marked seen ... so
//     the advanced scan watermark cannot silently drop it next round"), so
//     they are intentionally never re-emitted (旧积压可能被跳过).
//   - MaxNew == 0 emits nothing and rebuilds the page-1 baseline: the
//     baseline IDs are merged into seen (plugin _commit_baseline_rebuild →
//     _rebuild_scan_baseline), sealing the current first page.
//   - The watermark is always the capped numeric-only page-1 IDs, replaced
//     — never merged — on every completed round.
//
// The package performs no IO and owns no clock beyond stamping state
// updates; persistence lives in internal/storage/seen.
package watch

import (
	"time"

	"github.com/shitianyaa/twitter-cli/internal/storage/seen"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// Options tunes one Select round for an initialized source. On the first
// run only IncludeExisting applies (MaxNew is a per-round emission cap for
// initialized sources; the first run seeds the full fetch either way).
type Options struct {
	// IncludeExisting emits the whole first fetch on an uninitialized
	// source (default false: 只记不推).
	IncludeExisting bool
	// MaxNew caps emitted new tweets for initialized sources: >0 emits at
	// most N (newest first) while seen still advances with every new id;
	// ==0 emits nothing and rebuilds the baseline from the first page.
	MaxNew int
}

// Result is one round's outcome: the tweets the caller may deliver, the
// state to persist after delivery, and whether the round allows emission at
// all (false = first-run seeding or MaxNew == 0 stop).
type Result struct {
	Tweets  []twitter.Tweet
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
func Select(fetched []twitter.Tweet, firstPageIDs []string, prev seen.SourceState, opts Options) Result {
	watermark := seen.CapWatermark(firstPageIDs)

	if !prev.Initialized {
		seedIDs := make([]string, 0, len(fetched))
		for _, tweet := range fetched {
			if tweet.ID != "" {
				seedIDs = append(seedIDs, tweet.ID)
			}
		}
		if len(seedIDs) == 0 {
			// Empty first fetch: leave the source uninitialized so a
			// transient empty response does not seal history (plugin:
			// 首次抓取为空，未建立订阅源基线).
			return Result{State: prev}
		}
		state := seen.SourceState{
			Initialized:  true,
			SeenIDs:      seen.MergeSeen(seedIDs, prev.SeenIDs),
			WatermarkIDs: watermark,
			UpdatedAt:    time.Now().UTC(),
		}
		if opts.IncludeExisting {
			return Result{Tweets: fetched, State: state, Emitted: true}
		}
		return Result{State: state}
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
				UpdatedAt:    time.Now().UTC(),
			},
		}
	}

	known := make(map[string]struct{}, len(prev.SeenIDs))
	for _, id := range prev.SeenIDs {
		known[id] = struct{}{}
	}

	var newTweets []twitter.Tweet
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

	// Seen advances with ALL new ids: the plugin merges the emitted subset
	// after delivery and marks the excess older tweets seen immediately at
	// discovery time, so they are permanently skipped rather than re-found
	// next round. Merging the timeline-ordered new ids (emitted are their
	// newest prefix) reproduces the plugin's final order exactly.
	return Result{
		Tweets: emitted,
		State: seen.SourceState{
			Initialized:  true,
			SeenIDs:      seen.MergeSeen(newIDs, prev.SeenIDs),
			WatermarkIDs: watermark,
			UpdatedAt:    time.Now().UTC(),
		},
		Emitted: true,
	}
}
