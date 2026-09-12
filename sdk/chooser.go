package twitter

import (
	"sync"
	"time"
)

// sdkInstance is one rotation slot: the instance base URL plus the instant
// until which it is cooling down after a failure. coolUntil is a zero Time
// for a healthy instance.
type sdkInstance struct {
	url       string
	coolUntil time.Time
}

// Chooser rotates across configured Nitter instances in configuration order.
// An instance that just failed (HTTP 429, network error — the caller decides
// via markFailure) enters a cooldown for the configured duration: while
// cooling, Pick skips it. A successful request (markSuccess) resets the
// cooldown immediately. Selection is strictly ordered — no success weighting
// (the MVP instance count is small).
//
// A Chooser is safe for concurrent use: the rotation state is guarded by a
// mutex, and the mark functions returned by Pick may be called from any
// goroutine after the request completes.
type Chooser struct {
	mu       sync.Mutex
	order    []sdkInstance
	cooldown time.Duration
	now      func() time.Time
}

// NewChooser builds a Chooser over instances in the given order (the slice
// is copied; later caller mutation cannot leak into the rotation state).
//
// A cooldown of zero or less means failures are still marked but never block
// a pick. A nil now defaults to time.Now.
//
// An empty (or nil) instance list yields a valid Chooser whose every Pick
// fails with a KindUnavailable error ("no instances configured"): the sdk
// Client built without WithInstances behaves exactly this way.
func NewChooser(instances []Instance, cooldown time.Duration, now func() time.Time) *Chooser {
	if now == nil {
		now = time.Now
	}
	c := &Chooser{
		cooldown: cooldown,
		now:      now,
	}
	if len(instances) > 0 {
		c.order = make([]sdkInstance, len(instances))
		for i, in := range instances {
			c.order[i] = sdkInstance{url: in.URL}
		}
	}
	return c
}

// Pick returns the base URL of the first instance, in configuration order,
// that is not cooling down, plus the pair of mark functions that classify the
// upcoming attempt: call markFailure after the request fails (429, network
// error) to put that instance into cooldown until now+cooldown, or
// markSuccess after it succeeds to reset the cooldown immediately. The marks
// always apply to the instance whose URL was returned, and remain callable
// after later picks (state is indexed, not reordered).
//
// When every instance is cooling down Pick returns the error
// "all instances cooling down" (KindUnavailable); when no instance is
// configured at all it returns "no instances configured" (also
// KindUnavailable). On error the returned URL is empty and both mark
// functions are nil.
func (c *Chooser) Pick() (baseURL string, markSuccess, markFailure func(), err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for i := range c.order {
		if c.order[i].coolUntil.After(now) {
			continue // cooling down: strictly after now, so == now is pickable
		}
		idx := i
		return c.order[idx].url,
			func() { c.reset(idx) },
			func() { c.cool(idx) },
			nil
	}
	if len(c.order) == 0 {
		return "", nil, nil, Errorf(KindUnavailable, opChooser, "no instances configured")
	}
	return "", nil, nil, Errorf(KindUnavailable, opChooser, "all instances cooling down")
}

// reset marks instance idx healthy again (markSuccess).
func (c *Chooser) reset(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order[idx].coolUntil = time.Time{}
}

// cool puts instance idx into cooldown until now+cooldown (markFailure). A
// non-positive cooldown stores a value that never blocks a later pick.
func (c *Chooser) cool(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order[idx].coolUntil = c.now().Add(c.cooldown)
}
