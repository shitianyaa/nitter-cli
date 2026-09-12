package nitter_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// fakeClock is an injectable clock: tests advance it explicitly so cooldown
// boundaries are deterministic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// coolPrefix marks the first n instances of the chooser as failed, in config
// order. Because a fresh chooser always picks the first non-cooling instance,
// repeatedly picking and marking failures walks the prefix one by one.
func coolPrefix(t *testing.T, c *nitter.Chooser, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, _, markFailure, err := c.Pick()
		if err != nil {
			t.Fatalf("coolPrefix: Pick() %d = error %v, want an instance", i+1, err)
		}
		if markFailure == nil {
			t.Fatalf("coolPrefix: Pick() %d returned nil markFailure", i+1)
		}
		markFailure()
	}
}

func TestChooserPickReturnsFirstNonCoolingInstanceInOrder(t *testing.T) {
	tests := []struct {
		name     string
		entries  []nitter.Instance
		cool     int // number of leading instances to mark failed
		advance  time.Duration
		wantURL  string
		wantFail bool
	}{
		{
			name:    "none cooling returns first in config order",
			entries: []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}, {URL: "http://c"}},
			wantURL: "http://a",
		},
		{
			name:    "single instance returned",
			entries: []nitter.Instance{{URL: "http://only"}},
			wantURL: "http://only",
		},
		{
			name:    "cooling first is skipped",
			entries: []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}},
			cool:    1,
			wantURL: "http://b",
		},
		{
			name:    "two cooling instances are skipped",
			entries: []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}, {URL: "http://c"}},
			cool:    2,
			wantURL: "http://c",
		},
		{
			name:     "all cooling errors",
			entries:  []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}},
			cool:     2,
			wantFail: true,
		},
		{
			name:    "cooldown expired exactly is pickable again",
			entries: []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}},
			cool:    1,
			// coolUntil == now+cooldown; a Pick at exactly that instant must
			// not treat the instance as cooling (boundary: strictly-after).
			advance: 60 * time.Second,
			wantURL: "http://a",
		},
		{
			name:    "cooldown expired is pickable again",
			entries: []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}},
			cool:    1,
			advance: 60*time.Second + time.Nanosecond,
			wantURL: "http://a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock()
			c := nitter.NewChooser(tc.entries, 60*time.Second, clock.Now)
			coolPrefix(t, c, tc.cool)
			clock.Advance(tc.advance)

			url, markSuccess, markFailure, err := c.Pick()
			if tc.wantFail {
				if err == nil {
					t.Fatalf("Pick() = %q, want an error (all instances cooling)", url)
				}
				if markSuccess != nil || markFailure != nil {
					t.Error("Pick() must return nil mark functions on error")
				}
				var sdkErr *nitter.Error
				if !errors.As(err, &sdkErr) {
					t.Fatalf("Pick() error %v is not *nitter.Error", err)
				}
				if sdkErr.Kind != nitter.KindUnavailable {
					t.Errorf("Kind = %v, want %v", sdkErr.Kind, nitter.KindUnavailable)
				}
				if got := sdkErr.Error(); got != "chooser: upstream_unavailable: all instances cooling down" {
					t.Errorf("Error() = %q, want the stable all-cooling message", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pick() = error %v, want %q", err, tc.wantURL)
			}
			if url != tc.wantURL {
				t.Errorf("Pick() = %q, want %q", url, tc.wantURL)
			}
			if markSuccess == nil || markFailure == nil {
				t.Fatal("Pick() must return non-nil mark functions on success")
			}
			// The marks are exercised against the handed-out instance in the
			// dedicated markFailure/markSuccess tests below.
		})
	}
}

func TestChooserMarkFailureEntersCooldown(t *testing.T) {
	clock := newFakeClock()
	c := nitter.NewChooser([]nitter.Instance{{URL: "http://a"}, {URL: "http://b"}}, 60*time.Second, clock.Now)

	first, _, markFailure, err := c.Pick()
	if err != nil {
		t.Fatalf("first Pick() = error %v", err)
	}
	if first != "http://a" {
		t.Fatalf("first Pick() = %q, want http://a", first)
	}
	markFailure()

	// Same clock instant: a is cooling until now+60s, so b is next.
	second, _, _, err := c.Pick()
	if err != nil {
		t.Fatalf("second Pick() = error %v", err)
	}
	if second != "http://b" {
		t.Errorf("second Pick() = %q, want http://b (a cooling)", second)
	}

	// Just before the cooldown ends a is still out.
	clock.Advance(59 * time.Second)
	third, _, _, err := c.Pick()
	if err != nil {
		t.Fatalf("third Pick() = error %v", err)
	}
	if third != "http://b" {
		t.Errorf("Pick() before cooldown end = %q, want http://b", third)
	}

	// After the cooldown a rotates back in.
	clock.Advance(1 * time.Second)
	fourth, _, _, err := c.Pick()
	if err != nil {
		t.Fatalf("fourth Pick() = error %v", err)
	}
	if fourth != "http://a" {
		t.Errorf("Pick() after cooldown end = %q, want http://a", fourth)
	}
}

func TestChooserMarkSuccessResetsCooldown(t *testing.T) {
	clock := newFakeClock()
	c := nitter.NewChooser([]nitter.Instance{{URL: "http://a"}, {URL: "http://b"}}, time.Hour, clock.Now)

	// Pick a and drive it into cooldown, keeping its markSuccess closure.
	_, markSuccessA, markFailureA, err := c.Pick()
	if err != nil {
		t.Fatalf("first Pick() = error %v", err)
	}
	markFailureA()

	// b is the only healthy instance now; drive it into cooldown too, keeping
	// its markSuccess closure.
	_, markSuccessB, markFailureB, err := c.Pick()
	if err != nil {
		t.Fatalf("second Pick() = error %v, want http://b", err)
	}
	markFailureB()

	// Both cooling: Pick must fail until a mark resets one of them.
	if _, _, _, err := c.Pick(); err == nil {
		t.Fatal("Pick() with all instances cooling = nil error, want an error")
	}

	// Recovery without waiting out the one-hour cooldown: markSuccess on b
	// resets its coolUntil to zero, so b is immediately pickable again.
	markSuccessB()
	urlB, _, markFailureB2, err := c.Pick()
	if err != nil || urlB != "http://b" {
		t.Fatalf("Pick() after markSuccess = (%q, %v), want http://b reset", urlB, err)
	}

	// a is still cooling (the reset only touched b): re-failing b puts the
	// whole set back into cooldown.
	markFailureB2()
	if _, _, _, err := c.Pick(); err == nil {
		t.Error("Pick() with all instances cooling again = nil error, want an error")
	}

	// A success mark on a still-cooling instance also resets it.
	clock.Advance(30 * time.Minute)
	markSuccessA()
	if url, _, _, err := c.Pick(); err != nil || url != "http://a" {
		t.Errorf("Pick() after markSuccess on cooling a = (%q, %v), want http://a", url, err)
	}
}

func TestChooserMarkSuccessOnHealthyInstanceIsNoop(t *testing.T) {
	clock := newFakeClock()
	c := nitter.NewChooser([]nitter.Instance{{URL: "http://a"}}, time.Hour, clock.Now)

	_, markSuccess, _, err := c.Pick()
	if err != nil {
		t.Fatalf("Pick() = error %v", err)
	}
	markSuccess()
	if url, _, _, err := c.Pick(); err != nil || url != "http://a" {
		t.Errorf("Pick() after markSuccess = (%q, %v), want http://a", url, err)
	}
}

func TestChooserZeroCooldownNeverBlocks(t *testing.T) {
	clock := newFakeClock()
	c := nitter.NewChooser([]nitter.Instance{{URL: "http://a"}}, 0, clock.Now)

	_, _, markFailure, err := c.Pick()
	if err != nil {
		t.Fatalf("first Pick() = error %v", err)
	}
	if markFailure == nil {
		t.Fatal("first Pick() returned nil markFailure")
	}
	markFailure()

	// Zero cooldown: the failure is still marked (coolUntil = now+0), but a
	// pick at the same instant must not treat the instance as cooling.
	if url, _, _, err := c.Pick(); err != nil || url != "http://a" {
		t.Errorf("Pick() with zero cooldown = (%q, %v), want http://a", url, err)
	}
}

func TestChooserNoInstancesConfiguredFails(t *testing.T) {
	clock := newFakeClock()
	c := nitter.NewChooser(nil, 60*time.Second, clock.Now)

	url, markSuccess, markFailure, err := c.Pick()
	if err == nil {
		t.Fatalf("Pick() on empty chooser = %q, want an error", url)
	}
	if markSuccess != nil || markFailure != nil {
		t.Error("Pick() must return nil mark functions on error")
	}
	var sdkErr *nitter.Error
	if !errors.As(err, &sdkErr) {
		t.Fatalf("Pick() error %v is not *nitter.Error", err)
	}
	if sdkErr.Kind != nitter.KindUnavailable {
		t.Errorf("Kind = %v, want %v", sdkErr.Kind, nitter.KindUnavailable)
	}
	if got := sdkErr.Error(); got != "chooser: upstream_unavailable: no instances configured" {
		t.Errorf("Error() = %q, want the stable no-instances message", got)
	}
}

func TestChooserNilClockDefaultsToTimeNow(t *testing.T) {
	c := nitter.NewChooser([]nitter.Instance{{URL: "http://a"}}, time.Minute, nil)
	if url, _, _, err := c.Pick(); err != nil || url != "http://a" {
		t.Errorf("Pick() with nil clock = (%q, %v), want http://a", url, err)
	}
}

func TestChooserDefensiveCopyOfInstances(t *testing.T) {
	entries := []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}}
	c := nitter.NewChooser(entries, time.Minute, newFakeClock().Now)

	entries[0].URL = "http://mutated"
	if url, _, _, err := c.Pick(); err != nil || url != "http://a" {
		t.Errorf("Pick() after caller mutation = (%q, %v), want http://a (chooser must copy)", url, err)
	}
}

func TestChooserConcurrentPickAndMarks(t *testing.T) {
	// Concurrency smoke: real race detection is unavailable on this machine
	// (no cgo-capable toolchain), so this only proves the mutex design does
	// not deadlock or panic under contention. Correctness is by construction:
	// every access to the rotation state is guarded by Chooser.mu.
	entries := []nitter.Instance{{URL: "http://a"}, {URL: "http://b"}, {URL: "http://c"}}
	c := nitter.NewChooser(entries, time.Millisecond, time.Now)

	const workers = 8
	const iterations = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_, markSuccess, markFailure, err := c.Pick()
				if err != nil {
					continue // everything cooling: legitimate under contention
				}
				if (seed+i)%2 == 0 {
					markFailure()
				} else {
					markSuccess()
				}
			}
		}(w)
	}
	wg.Wait()
}
