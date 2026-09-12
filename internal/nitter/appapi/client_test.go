package appapi

import (
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/protocol/httpx"
)

func TestNewClientDefaults(t *testing.T) {
	c, err := newClient(nil, nil, nil)
	if err != nil {
		t.Fatalf("newClient(nil, nil, nil) = error %v, want success", err)
	}
	if c == nil {
		t.Fatal("newClient returned a nil client")
	}
	// The composition root owns the default transport: httpx built with zero
	// Options, whose zero value selects the documented defaults (Timeout 20s,
	// RetryAttempts 2, RetryDelay 1s, MinInterval 1s) — the values themselves
	// are pinned by httpx's TestNewDefaultsApplied.
	if c.HTTP == nil {
		t.Fatal("newClient did not build a default HTTP transport")
	}
	if _, ok := any(c.HTTP).(*httpx.Client); !ok {
		t.Fatalf("default transport is a %T, want *httpx.Client", c.HTTP)
	}
	if c.Now == nil {
		t.Fatal("newClient left Now nil, want a time.Now default")
	}
	if delta := time.Since(c.Now()); delta > 5*time.Second || delta < -5*time.Second {
		t.Errorf("Now() = %v, drifts %v from the real clock", c.Now(), delta)
	}
}

func TestNewClientStoresProvidedParts(t *testing.T) {
	hx, err := httpx.New(httpx.Options{})
	if err != nil {
		t.Fatalf("httpx.New = error %v", err)
	}
	fakeNow := func() time.Time { return time.Unix(0, 0) }
	c, err := newClient(hx, nil, fakeNow)
	if err != nil {
		t.Fatalf("newClient = error %v", err)
	}
	if c.HTTP != hx {
		t.Error("newClient did not store the provided transport")
	}
	if c.Now == nil {
		t.Fatal("injected clock lost")
	}
	if c.Now().UnixNano() != 0 {
		t.Errorf("Now() = %v, want the injected fake clock", c.Now())
	}
	// The chooser is stored as-is (nil allowed for this milestone; the
	// endpoint tasks fill the composition root).
	if c.Chooser != nil {
		t.Error("chooser should be stored as passed (nil here)")
	}
}
