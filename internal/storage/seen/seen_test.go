package seen_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/storage/seen"
	"github.com/shitianyaa/twitter-cli/sdk"
)

func testPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "seen.json")
}

func fixedNow() time.Time {
	return time.Date(2026, 9, 12, 8, 30, 0, 0, time.UTC)
}

func sampleState() seen.SourceState {
	return seen.SourceState{
		Initialized:  true,
		SeenIDs:      []string{"2081668333762687236", "2081000000000000000"},
		WatermarkIDs: []string{"2081668333762687236", "2081000000000000000"},
	}
}

// Open on a missing file yields an empty store without creating anything:
// the first write stays lazy until the first Put.
func TestOpenMissingFileEmptyStateNoCreation(t *testing.T) {
	path := testPath(t)

	store, err := seen.Open(path, fixedNow)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	state, ok := store.Get("user:NASA")
	if ok {
		t.Fatalf("Get() on empty store reported ok = true")
	}
	if !state.UpdatedAt.IsZero() || len(state.SeenIDs) != 0 || len(state.WatermarkIDs) != 0 || state.Initialized {
		t.Fatalf("Get() zero state = %+v, want zero value", state)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Open() must not create %s (stat err = %v)", path, err)
	}
}

func TestPutGetRoundtripSameInstanceAndReopen(t *testing.T) {
	path := testPath(t)
	now := fixedNow()

	store, err := seen.Open(path, func() time.Time { return now })
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	want := sampleState()
	if err := store.Put("user:NASA", want); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	got, ok := store.Get("user:NASA")
	if !ok {
		t.Fatalf("Get() after Put reported ok = false")
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v (stamped by Put)", got.UpdatedAt, now)
	}
	if got.Initialized != want.Initialized ||
		!equalStrings(got.SeenIDs, want.SeenIDs) ||
		!equalStrings(got.WatermarkIDs, want.WatermarkIDs) {
		t.Fatalf("roundtrip mismatch on same instance: got %+v, want %+v", got, want)
	}

	// Reopen from disk: the state must survive the process boundary.
	reopened, err := seen.Open(path, func() time.Time { return now })
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	again, ok := reopened.Get("user:NASA")
	if !ok {
		t.Fatalf("Get() after reopen reported ok = false")
	}
	if !again.UpdatedAt.Equal(now) ||
		again.Initialized != want.Initialized ||
		!equalStrings(again.SeenIDs, want.SeenIDs) ||
		!equalStrings(again.WatermarkIDs, want.WatermarkIDs) {
		t.Fatalf("roundtrip mismatch after reopen: got %+v, want %+v", again, want)
	}
}

func TestPutIsolatesSources(t *testing.T) {
	path := testPath(t)
	store, err := seen.Open(path, fixedNow)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if err := store.Put("user:NASA", sampleState()); err != nil {
		t.Fatalf("Put(user:NASA) error = %v", err)
	}
	if err := store.Put("tag:%23AI", seen.SourceState{Initialized: true, SeenIDs: []string{"1"}}); err != nil {
		t.Fatalf("Put(tag) error = %v", err)
	}

	nasa, ok := store.Get("user:NASA")
	if !ok || !equalStrings(nasa.SeenIDs, sampleState().SeenIDs) {
		t.Fatalf("user:NASA state changed by other Put: %+v (ok = %v)", nasa, ok)
	}
	if _, ok := store.Get("list:12345"); ok {
		t.Fatalf("Get() on unknown source reported ok = true")
	}
}

func TestPutOverwritesPreviousState(t *testing.T) {
	path := testPath(t)
	store, err := seen.Open(path, fixedNow)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if err := store.Put("user:NASA", sampleState()); err != nil {
		t.Fatalf("first Put() error = %v", err)
	}
	replacement := seen.SourceState{Initialized: true, SeenIDs: []string{"9"}}
	if err := store.Put("user:NASA", replacement); err != nil {
		t.Fatalf("second Put() error = %v", err)
	}

	got, ok := store.Get("user:NASA")
	if !ok {
		t.Fatalf("Get() reported ok = false")
	}
	if !equalStrings(got.SeenIDs, []string{"9"}) || len(got.WatermarkIDs) != 0 {
		t.Fatalf("state after overwrite = %+v, want only SeenIDs [9]", got)
	}
}

// Corrupt state must surface as a KindLocalState error and must never be
// silently reset: the original bytes stay on disk untouched.
func TestOpenCorruptFileErrorsAndKeepsBytes(t *testing.T) {
	cases := map[string]string{
		"truncated json": `{"version":1,"sources":{`,
		"not json":       `this is not json`,
		"empty file":     ``,
		"json array":     `[]`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := testPath(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("seed corrupt file: %v", err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read seeded file: %v", err)
			}

			store, openErr := seen.Open(path, fixedNow)
			if openErr == nil {
				t.Fatalf("Open() on corrupt file succeeded, want error")
			}
			if store != nil {
				t.Fatalf("Open() returned non-nil store with error")
			}
			var sdkErr *twitter.Error
			if !errors.As(openErr, &sdkErr) {
				t.Fatalf("Open() error = %v, want *twitter.Error chain", openErr)
			}
			if sdkErr.Kind != twitter.KindLocalState {
				t.Fatalf("Open() error kind = %q, want %q", sdkErr.Kind, twitter.KindLocalState)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reread corrupt file: %v", err)
			}
			if string(before) != string(after) {
				t.Fatalf("corrupt file was modified: before %q, after %q", before, after)
			}
		})
	}
}

func TestOpenStrictDecodeRejections(t *testing.T) {
	cases := map[string]string{
		"unknown top-level field":     `{"version":1,"sources":{},"extra":true}`,
		"unknown source field":        `{"version":1,"sources":{"user:x":{"initialized":true,"bogus":1}}}`,
		"trailing json document":      `{"version":1,"sources":{}} {"version":1,"sources":{}}`,
		"trailing garbage":            `{"version":1,"sources":{}}garbage`,
		"future version":              `{"version":2,"sources":{}}`,
		"missing version":             `{"sources":{}}`,
		"zero version":                `{"version":0,"sources":{}}`,
		"sources wrong type":          `{"version":1,"sources":[]}`,
		"seen_ids wrong element type": `{"version":1,"sources":{"user:x":{"seen_ids":[1]}}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := testPath(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("seed file: %v", err)
			}

			store, err := seen.Open(path, fixedNow)
			if err == nil {
				t.Fatalf("Open() succeeded, want error")
			}
			if store != nil {
				t.Fatalf("Open() returned non-nil store with error")
			}
			var sdkErr *twitter.Error
			if !errors.As(err, &sdkErr) || sdkErr.Kind != twitter.KindLocalState {
				t.Fatalf("Open() error = %v, want KindLocalState chain", err)
			}
		})
	}
}

// A structurally valid file with the current version must load, including
// a null sources map (normalized to empty) and unknown sources preserved.
func TestOpenAcceptsValidFile(t *testing.T) {
	path := testPath(t)
	content := `{"version":1,"sources":{"user:NASA":{"initialized":true,"seen_ids":["1"],"watermark_ids":["1"],"updated_at":"2026-09-11T15:04:05Z"}}}` + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	store, err := seen.Open(path, fixedNow)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	got, ok := store.Get("user:NASA")
	if !ok {
		t.Fatalf("Get() reported ok = false")
	}
	if !got.Initialized || !equalStrings(got.SeenIDs, []string{"1"}) {
		t.Fatalf("loaded state = %+v, want initialized with seen_ids [1]", got)
	}
	if want := time.Date(2026, 9, 11, 15, 4, 5, 0, time.UTC); !got.UpdatedAt.Equal(want) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, want)
	}
}

// MergeSeen ports the plugin merge_seen_ids: new IDs first (newest-first
// prefix), dedupe, empty IDs dropped, hard cap at SeenLimit.
func TestMergeSeen(t *testing.T) {
	t.Run("new ids keep order and land before old ids", func(t *testing.T) {
		got := seen.MergeSeen([]string{"3", "2", "1"}, []string{"2", "0"})
		want := []string{"3", "2", "1", "0"}
		if !equalStrings(got, want) {
			t.Fatalf("MergeSeen = %v, want %v", got, want)
		}
	})
	t.Run("empty and duplicate ids are dropped", func(t *testing.T) {
		got := seen.MergeSeen([]string{"", "1", "", "1"}, []string{"", "1", "2"})
		want := []string{"1", "2"}
		if !equalStrings(got, want) {
			t.Fatalf("MergeSeen = %v, want %v", got, want)
		}
	})
	t.Run("empty inputs yield empty non-nil slice", func(t *testing.T) {
		got := seen.MergeSeen(nil, nil)
		if got == nil {
			t.Fatalf("MergeSeen(nil, nil) = nil, want empty non-nil slice")
		}
		if len(got) != 0 {
			t.Fatalf("MergeSeen(nil, nil) = %v, want empty", got)
		}
	})
	t.Run("cap 300 drops the 301st id", func(t *testing.T) {
		newIDs := make([]string, 0, seen.SeenLimit+1)
		for i := seen.SeenLimit + 1; i >= 1; i-- { // newest first: 301, 300, ..., 1
			newIDs = append(newIDs, fmt.Sprintf("%d", i))
		}
		got := seen.MergeSeen(newIDs, nil)
		if len(got) != seen.SeenLimit {
			t.Fatalf("len(MergeSeen(301 ids)) = %d, want %d", len(got), seen.SeenLimit)
		}
		if got[0] != "301" {
			t.Fatalf("newest id lost: got[0] = %q, want 301", got[0])
		}
		if got[seen.SeenLimit-1] != "2" {
			t.Fatalf("cap boundary wrong: got[last] = %q, want 2 (301st dropped)", got[seen.SeenLimit-1])
		}
		for _, id := range got {
			if id == "1" {
				t.Fatalf("oldest id 1 survived the cap: %v tail", got[seen.SeenLimit-3:])
			}
		}
	})
	t.Run("old ids fill the remainder up to the cap", func(t *testing.T) {
		var oldIDs []string
		for i := 1; i <= seen.SeenLimit; i++ {
			oldIDs = append(oldIDs, fmt.Sprintf("old-%d", i))
		}
		got := seen.MergeSeen([]string{"n1", "n2"}, oldIDs)
		if len(got) != seen.SeenLimit {
			t.Fatalf("len = %d, want %d", len(got), seen.SeenLimit)
		}
		if got[0] != "n1" || got[1] != "n2" {
			t.Fatalf("new prefix wrong: %v", got[:2])
		}
		if got[2] != "old-1" || got[seen.SeenLimit-1] != "old-298" {
			t.Fatalf("old tail wrong: got[2] = %q, got[last] = %q, want old-1..old-298", got[2], got[seen.SeenLimit-1])
		}
	})
}

// CapWatermark ports the plugin _normalize_scan_baseline_ids: numeric-only,
// order-preserving dedupe, cap 20.
func TestCapWatermark(t *testing.T) {
	t.Run("non-numeric and empty ids filtered, order kept", func(t *testing.T) {
		got := seen.CapWatermark([]string{"12", "abc", "", " 34 ", "12", "-5"})
		want := []string{"12", "34", "-5"}
		if !equalStrings(got, want) {
			t.Fatalf("CapWatermark = %v, want %v", got, want)
		}
	})
	t.Run("cap 20 keeps the first 20", func(t *testing.T) {
		ids := make([]string, 0, seen.WatermarkLimit+5)
		for i := 1; i <= seen.WatermarkLimit+5; i++ {
			ids = append(ids, fmt.Sprintf("%d", i))
		}
		got := seen.CapWatermark(ids)
		if len(got) != seen.WatermarkLimit {
			t.Fatalf("len = %d, want %d", len(got), seen.WatermarkLimit)
		}
		if got[0] != "1" || got[seen.WatermarkLimit-1] != "20" {
			t.Fatalf("cap kept wrong ids: got[0] = %q, got[last] = %q", got[0], got[seen.WatermarkLimit-1])
		}
	})
	t.Run("empty input yields empty non-nil slice", func(t *testing.T) {
		got := seen.CapWatermark(nil)
		if got == nil || len(got) != 0 {
			t.Fatalf("CapWatermark(nil) = %v, want empty non-nil", got)
		}
	})
}

func TestSourceKey(t *testing.T) {
	cases := []struct {
		kind, ref, want string
	}{
		{"user", "NASA", "user:NASA"},
		{"tag", "%23AI", "tag:%23AI"},
		{"list", "12345", "list:12345"},
		{"", "NASA", ""},
		{"user", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := seen.SourceKey(tc.kind, tc.ref); got != tc.want {
			t.Errorf("SourceKey(%q, %q) = %q, want %q", tc.kind, tc.ref, got, tc.want)
		}
	}
}

// Concurrent Put + Get must be race-free and leave every source persisted:
// run with -race; the final reopen sees all writers' sources.
func TestConcurrentPutGet(t *testing.T) {
	path := testPath(t)
	store, err := seen.Open(path, fixedNow)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			source := fmt.Sprintf("user:u%d", i)
			state := seen.SourceState{Initialized: true, SeenIDs: []string{fmt.Sprintf("%d", i)}}
			if err := store.Put(source, state); err != nil {
				t.Errorf("Put(%s) error = %v", source, err)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			store.Get(fmt.Sprintf("user:u%d", (i+1)%workers))
		}(i)
	}
	wg.Wait()

	reopened, err := seen.Open(path, fixedNow)
	if err != nil {
		t.Fatalf("reopen after concurrent writes: %v", err)
	}
	for i := 0; i < workers; i++ {
		source := fmt.Sprintf("user:u%d", i)
		state, ok := reopened.Get(source)
		if !ok {
			t.Fatalf("source %s missing after concurrent puts", source)
		}
		if want := fmt.Sprintf("%d", i); !equalStrings(state.SeenIDs, []string{want}) {
			t.Fatalf("source %s = %v, want seen_ids [%s]", source, state.SeenIDs, want)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "seen.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("state dir entries = %v, want only [seen.json] (no staging leftovers)", names)
	}
}

func TestPutCreatesFileAtMode0600(t *testing.T) {
	path := testPath(t)
	store, err := seen.Open(path, fixedNow)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Put("user:NASA", sampleState()); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if runtime.GOOS == "windows" { // Windows chmod only toggles the read-only bit.
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat seen file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("seen file mode = %v, want 0600", got)
	}
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
