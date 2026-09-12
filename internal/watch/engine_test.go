package watch_test

import (
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/storage/seen"
	"github.com/shitianyaa/nitter-cli/internal/watch"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// tweets builds fetched tweets in timeline order: the first ID is the newest.
func tweets(ids ...string) []nitter.Tweet {
	out := make([]nitter.Tweet, 0, len(ids))
	for _, id := range ids {
		out = append(out, nitter.Tweet{ID: id})
	}
	return out
}

func onlyIDs(in []nitter.Tweet) []string {
	out := make([]string, 0, len(in))
	for _, tweet := range in {
		out = append(out, tweet.ID)
	}
	return out
}

func equalStrings(got, want []string) bool {
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

func pastTime() time.Time {
	return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
}

// Rule 1: uninitialized source, default options — 首跑只记不推: nothing is
// emitted, but the state is seeded with every fetched ID and the page-1
// watermark, and the source becomes initialized.
func TestSelectRule1FirstRunRecordsWithoutEmitting(t *testing.T) {
	fetched := tweets("1", "2", "3", "4", "5")
	firstPageIDs := []string{"1", "2", "3", "4", "5"}

	result := watch.Select(fetched, firstPageIDs, seen.SourceState{}, watch.Options{})

	if len(result.Tweets) != 0 {
		t.Fatalf("first run emitted %v, want none (只记不推)", onlyIDs(result.Tweets))
	}
	if result.Emitted {
		t.Fatalf("first run Emitted = true, want false")
	}
	state := result.State
	if !state.Initialized {
		t.Fatalf("state not initialized after first run: %+v", state)
	}
	if !equalStrings(state.SeenIDs, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("SeenIDs = %v, want all fetched ids in timeline order", state.SeenIDs)
	}
	if !equalStrings(state.WatermarkIDs, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("WatermarkIDs = %v, want capped first page", state.WatermarkIDs)
	}
	if !state.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt = %v, want zero (the engine must not fabricate timestamps; Store.Put stamps on persist)", state.UpdatedAt)
	}
}

// Rule 2: uninitialized source with IncludeExisting — history is emitted,
// state advancement identical to rule 1.
func TestSelectRule2FirstRunIncludeExistingEmitsHistory(t *testing.T) {
	fetched := tweets("1", "2", "3")
	firstPageIDs := []string{"1", "2", "3"}

	result := watch.Select(fetched, firstPageIDs, seen.SourceState{}, watch.Options{IncludeExisting: true})

	if !result.Emitted {
		t.Fatalf("Emitted = false, want true with IncludeExisting")
	}
	if !equalStrings(onlyIDs(result.Tweets), []string{"1", "2", "3"}) {
		t.Fatalf("Tweets = %v, want all fetched in order", onlyIDs(result.Tweets))
	}
	if !result.State.Initialized || !equalStrings(result.State.SeenIDs, []string{"1", "2", "3"}) {
		t.Fatalf("state = %+v, want seeded with all fetched ids", result.State)
	}
}

// A transient empty first fetch must NOT initialize tag/list sources — the
// plugin's empty/filtered-empty first-run handling (runner.py:948-971) is
// tag/list-specific — so a later real fetch is not swallowed by the
// record-only first run.
func TestSelectFirstRunEmptyFetchStaysUninitializedTagList(t *testing.T) {
	prev := seen.SourceState{UpdatedAt: pastTime()}
	for _, kind := range []string{watch.KindTag, watch.KindList} {
		t.Run(kind, func(t *testing.T) {
			result := watch.Select(nil, nil, prev, watch.Options{Kind: kind, IncludeExisting: true})

			if result.Emitted || len(result.Tweets) != 0 {
				t.Fatalf("empty first fetch emitted, want nothing")
			}
			if result.State.Initialized {
				t.Fatalf("empty first fetch initialized the source: %+v", result.State)
			}
			if !result.State.UpdatedAt.Equal(prev.UpdatedAt) {
				t.Fatalf("state touched on empty first fetch: UpdatedAt %v, want %v",
					result.State.UpdatedAt, prev.UpdatedAt)
			}
		})
	}
	t.Run("tag fetch without any valid id", func(t *testing.T) {
		result := watch.Select(tweets("", ""), nil, prev, watch.Options{Kind: watch.KindTag})
		if result.State.Initialized || result.Emitted {
			t.Fatalf("fetch without valid ids initialized/emitted: %+v", result)
		}
		if !result.State.UpdatedAt.Equal(prev.UpdatedAt) {
			t.Fatalf("state touched: UpdatedAt %v, want %v", result.State.UpdatedAt, prev.UpdatedAt)
		}
	})
}

// A user source's empty first fetch still initializes with empty state
// (plugin runner.py:979-985 seeds an empty seen), so the NEXT round's
// tweets are selected and pushed instead of being silently swallowed by the
// record-only first run — the runner.py:940-947 warning scenario. An empty
// Options.Kind is treated as "user".
func TestSelectFirstRunEmptyFetchInitializesUserSource(t *testing.T) {
	for _, kind := range []string{"", watch.KindUser} {
		t.Run("kind="+kind, func(t *testing.T) {
			first := watch.Select(nil, nil, seen.SourceState{}, watch.Options{Kind: kind})

			if first.Emitted || len(first.Tweets) != 0 {
				t.Fatalf("empty first fetch emitted, want nothing")
			}
			if !first.State.Initialized {
				t.Fatalf("user empty first fetch must initialize: %+v", first.State)
			}
			if len(first.State.SeenIDs) != 0 || len(first.State.WatermarkIDs) != 0 {
				t.Fatalf("seeded state = %+v, want empty seen and watermark", first.State)
			}
			if !first.State.UpdatedAt.IsZero() {
				t.Fatalf("UpdatedAt = %v, want zero (the engine must not fabricate timestamps)", first.State.UpdatedAt)
			}

			// The follow-up round must push — no silent notification loss.
			second := watch.Select(tweets("7", "8"), []string{"7", "8"}, first.State,
				watch.Options{Kind: kind, MaxNew: 10})
			if !second.Emitted || !equalStrings(onlyIDs(second.Tweets), []string{"7", "8"}) {
				t.Fatalf("next round after empty-user-init: Emitted = %v, Tweets = %v, want push [7 8]",
					second.Emitted, onlyIDs(second.Tweets))
			}
		})
	}
	t.Run("user fetch without any valid id", func(t *testing.T) {
		first := watch.Select(tweets("", ""), nil, seen.SourceState{}, watch.Options{Kind: watch.KindUser})
		if !first.State.Initialized || len(first.State.SeenIDs) != 0 {
			t.Fatalf("state = %+v, want initialized with empty seen", first.State)
		}
	})
}

// An initialized source whose successful fetch came back empty keeps its
// previous state wholesale — in particular the watermark anchors are NOT
// rebuilt to empty (plugin runner.py:849-858 返回空结果，保留当前水位;
// spec §7 保留旧水位). This must also short-circuit the MaxNew == 0
// baseline rebuild, which would otherwise blank the watermark.
func TestSelectInitializedEmptyFetchKeepsPreviousState(t *testing.T) {
	prev := seen.SourceState{
		Initialized:  true,
		SeenIDs:      []string{"5"},
		WatermarkIDs: []string{"3", "2"},
		UpdatedAt:    pastTime(),
	}
	for name, opts := range map[string]watch.Options{
		"maxnew 10": {MaxNew: 10},
		"maxnew 0":  {},
	} {
		t.Run(name, func(t *testing.T) {
			result := watch.Select(nil, nil, prev, opts)

			if result.Emitted || len(result.Tweets) != 0 {
				t.Fatalf("empty fetch emitted, want nothing")
			}
			if !equalStrings(result.State.WatermarkIDs, []string{"3", "2"}) {
				t.Fatalf("WatermarkIDs = %v, want previous [3 2] preserved", result.State.WatermarkIDs)
			}
			if !equalStrings(result.State.SeenIDs, []string{"5"}) {
				t.Fatalf("SeenIDs = %v, want previous [5] preserved", result.State.SeenIDs)
			}
			if !result.State.UpdatedAt.Equal(prev.UpdatedAt) {
				t.Fatalf("UpdatedAt = %v, want unchanged %v (nothing happened this round)",
					result.State.UpdatedAt, prev.UpdatedAt)
			}
		})
	}
}

// Rule 3: initialized source — new = fetched − seen in timeline order, seen
// merged with the new IDs in front, cycle completes.
func TestSelectRule3InitializedSelectsUnseenInOrder(t *testing.T) {
	prev := seen.SourceState{
		Initialized: true,
		SeenIDs:     []string{"5", "9"},
		UpdatedAt:   pastTime(),
	}
	fetched := tweets("1", "2", "3", "4", "5")
	firstPageIDs := []string{"1", "2", "3", "4", "5"}

	result := watch.Select(fetched, firstPageIDs, prev, watch.Options{MaxNew: 10})

	if !result.Emitted {
		t.Fatalf("Emitted = false, want true for an initialized cycle")
	}
	if !equalStrings(onlyIDs(result.Tweets), []string{"1", "2", "3", "4"}) {
		t.Fatalf("Tweets = %v, want unseen [1 2 3 4] in timeline order", onlyIDs(result.Tweets))
	}
	if !equalStrings(result.State.SeenIDs, []string{"1", "2", "3", "4", "5", "9"}) {
		t.Fatalf("SeenIDs = %v, want new ids merged in front of old", result.State.SeenIDs)
	}
	if !equalStrings(result.State.WatermarkIDs, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("WatermarkIDs = %v, want rebuilt from first page", result.State.WatermarkIDs)
	}
}

// Rule 4: MaxNew caps the emission but the plugin advances seen with ALL
// new ids — the excess older tweets are marked seen immediately (runner.py:
// "the excess is marked seen ... so the advanced scan watermark cannot
// silently drop it next round"), so they are never re-found. This is the
// plugin semantics ruling; the plan's R16 expectation (remainder re-emitted
// next round) diverges and was overridden.
func TestSelectRule4MaxNewCapsEmittedAndAdvancesSeenWithAllNew(t *testing.T) {
	fetched := tweets("1", "2", "3", "4", "5")
	firstPageIDs := []string{"1", "2", "3", "4", "5"}
	prev := seen.SourceState{Initialized: true, UpdatedAt: pastTime()}

	first := watch.Select(fetched, firstPageIDs, prev, watch.Options{MaxNew: 2})

	if !first.Emitted {
		t.Fatalf("first round Emitted = false, want true")
	}
	if !equalStrings(onlyIDs(first.Tweets), []string{"1", "2"}) {
		t.Fatalf("first round Tweets = %v, want newest 2 [1 2]", onlyIDs(first.Tweets))
	}
	if !equalStrings(first.State.SeenIDs, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("first round SeenIDs = %v, want ALL new ids [1..5] (plugin: excess marked seen)", first.State.SeenIDs)
	}

	// Next round with the same fetched tweets: the unemitted remainder is
	// already seen, so nothing new comes out — the cycle still completes.
	second := watch.Select(fetched, firstPageIDs, first.State, watch.Options{MaxNew: 2})

	if len(second.Tweets) != 0 {
		t.Fatalf("second round Tweets = %v, want empty (excess already seen)", onlyIDs(second.Tweets))
	}
	if !second.Emitted {
		t.Fatalf("second round Emitted = false, want true (completed cycle)")
	}
	if !equalStrings(second.State.SeenIDs, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("second round SeenIDs = %v, want unchanged", second.State.SeenIDs)
	}
}

// Rule 5: MaxNew == 0 — nothing emitted, and the plugin's baseline rebuild
// seals the first page: the baseline IDs are merged into seen (runner.py
// max==0 → _commit_baseline_rebuild → _rebuild_scan_baseline merges the
// baseline into seen; "旧积压可能被跳过"). The watermark is rebuilt either way.
func TestSelectRule5MaxNewZeroSkipsAndRebuildsBaseline(t *testing.T) {
	prev := seen.SourceState{
		Initialized: true,
		SeenIDs:     []string{"9"},
		UpdatedAt:   pastTime(),
	}
	fetched := tweets("1", "2", "3", "4", "5")
	firstPageIDs := []string{"1", "2", "3"}

	result := watch.Select(fetched, firstPageIDs, prev, watch.Options{MaxNew: 0})

	if result.Emitted {
		t.Fatalf("Emitted = true, want false for MaxNew == 0")
	}
	if len(result.Tweets) != 0 {
		t.Fatalf("Tweets = %v, want empty", onlyIDs(result.Tweets))
	}
	if !equalStrings(result.State.SeenIDs, []string{"1", "2", "3", "9"}) {
		t.Fatalf("SeenIDs = %v, want baseline [1 2 3] merged in front of old [9]", result.State.SeenIDs)
	}
	if !equalStrings(result.State.WatermarkIDs, []string{"1", "2", "3"}) {
		t.Fatalf("WatermarkIDs = %v, want rebuilt baseline", result.State.WatermarkIDs)
	}
	if !result.State.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt = %v, want zero (the engine must not fabricate timestamps; Store.Put stamps on persist)", result.State.UpdatedAt)
	}
}

// Rule 6: everything already seen — the cycle still completes (Emitted true)
// and the state is refreshed without changing the seen content.
func TestSelectRule6AllSeenStillCompletesCycle(t *testing.T) {
	prev := seen.SourceState{
		Initialized:  true,
		SeenIDs:      []string{"1", "2", "3"},
		WatermarkIDs: []string{"1", "2", "3"},
		UpdatedAt:    pastTime(),
	}
	fetched := tweets("1", "2", "3")
	firstPageIDs := []string{"1", "2", "3"}

	result := watch.Select(fetched, firstPageIDs, prev, watch.Options{MaxNew: 5})

	if len(result.Tweets) != 0 {
		t.Fatalf("Tweets = %v, want empty", onlyIDs(result.Tweets))
	}
	if !result.Emitted {
		t.Fatalf("Emitted = false, want true (completed cycle)")
	}
	if !equalStrings(result.State.SeenIDs, []string{"1", "2", "3"}) {
		t.Fatalf("SeenIDs = %v, want unchanged", result.State.SeenIDs)
	}
	if !equalStrings(result.State.WatermarkIDs, []string{"1", "2", "3"}) {
		t.Fatalf("WatermarkIDs = %v, want rebuilt", result.State.WatermarkIDs)
	}
	if !result.State.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt = %v, want zero (the engine must not fabricate timestamps; Store.Put stamps on persist)", result.State.UpdatedAt)
	}
}

// Rule 7: the watermark is always CapWatermark(firstPageIDs) — rebuilt by
// replacement, never merged with the previous watermark, regardless of seen
// overlap.
func TestSelectRule7WatermarkReplacedNotMerged(t *testing.T) {
	prev := seen.SourceState{
		Initialized:  true,
		SeenIDs:      []string{"5"},
		WatermarkIDs: []string{"777", "5"},
		UpdatedAt:    pastTime(),
	}
	fetched := tweets("7", "6", "5")
	// Duplicates, non-numeric and empty entries must be filtered; the cap
	// keeps the first 20 numeric unique ids.
	firstPageIDs := []string{"9", "abc", "", "9", "8"}

	result := watch.Select(fetched, firstPageIDs, prev, watch.Options{MaxNew: 10})

	if !equalStrings(result.State.WatermarkIDs, []string{"9", "8"}) {
		t.Fatalf("WatermarkIDs = %v, want [9 8] (deduped, numeric-only, replaced)", result.State.WatermarkIDs)
	}
	if len(result.State.WatermarkIDs) > seen.WatermarkLimit {
		t.Fatalf("watermark cap violated: %d entries", len(result.State.WatermarkIDs))
	}
}

// The watermark cap applies on every initialized path: more than 20 first
// page ids are truncated to the newest 20.
func TestSelectWatermarkCappedAt20(t *testing.T) {
	prev := seen.SourceState{Initialized: true, UpdatedAt: pastTime()}
	fetched := make([]nitter.Tweet, 0, 30)
	firstPageIDs := make([]string, 0, 30)
	for i := 30; i >= 1; i-- { // newest first
		id := ""
		for _, part := range []string{string(rune('0' + i/10)), string(rune('0' + i%10))} {
			id += part
		}
		fetched = append(fetched, nitter.Tweet{ID: id})
		firstPageIDs = append(firstPageIDs, id)
	}

	result := watch.Select(fetched, firstPageIDs, prev, watch.Options{})

	if len(result.State.WatermarkIDs) != seen.WatermarkLimit {
		t.Fatalf("watermark len = %d, want %d", len(result.State.WatermarkIDs), seen.WatermarkLimit)
	}
	if result.State.WatermarkIDs[0] != "30" || result.State.WatermarkIDs[19] != "11" {
		t.Fatalf("watermark kept wrong ids: first %q, last %q, want 30..11",
			result.State.WatermarkIDs[0], result.State.WatermarkIDs[19])
	}
}

// Tweets without an ID cannot be tracked and are never selected as new
// (plugin: "if not status_id ... continue").
func TestSelectSkipsEmptyIDTweets(t *testing.T) {
	prev := seen.SourceState{Initialized: true, SeenIDs: []string{"5"}, UpdatedAt: pastTime()}
	fetched := tweets("", "1", "", "2")
	firstPageIDs := []string{"1", "2"}

	result := watch.Select(fetched, firstPageIDs, prev, watch.Options{MaxNew: 10})

	if !equalStrings(onlyIDs(result.Tweets), []string{"1", "2"}) {
		t.Fatalf("Tweets = %v, want only the ID-carrying tweets [1 2]", onlyIDs(result.Tweets))
	}
	if !equalStrings(result.State.SeenIDs, []string{"1", "2", "5"}) {
		t.Fatalf("SeenIDs = %v, want [1 2 5]", result.State.SeenIDs)
	}
}

// MaxNew caps emission but never under-produces: when fewer new tweets than
// the cap arrive, all of them are emitted.
func TestSelectMaxNewAboveNewCountEmitsAll(t *testing.T) {
	prev := seen.SourceState{Initialized: true, SeenIDs: []string{"4"}, UpdatedAt: pastTime()}
	fetched := tweets("1", "2", "3", "4")

	result := watch.Select(fetched, []string{"1", "2", "3", "4"}, prev, watch.Options{MaxNew: 100})

	if !equalStrings(onlyIDs(result.Tweets), []string{"1", "2", "3"}) {
		t.Fatalf("Tweets = %v, want all new [1 2 3]", onlyIDs(result.Tweets))
	}
}
