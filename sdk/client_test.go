package twitter

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewDefaultsSucceed(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New() = error %v, want success", err)
	}
	if c == nil {
		t.Fatal("New() returned a nil client")
	}
	if c.chooser == nil {
		t.Fatal("New() did not install a chooser")
	}
	if got := c.chooser.cooldown; got != defaultCooldown {
		t.Errorf("default cooldown = %v, want %v", got, defaultCooldown)
	}
	if len(c.chooser.order) != 0 {
		t.Errorf("default chooser holds %d instances, want 0", len(c.chooser.order))
	}
	if c.maxPages != defaultMaxPages {
		t.Errorf("default maxPages = %d, want %d", c.maxPages, defaultMaxPages)
	}
	// Documented contract: without WithHTTPClient the client has no
	// transport; the CLI wiring (later milestone) injects the real one.
	if c.transport != nil {
		t.Errorf("default transport = %v, want nil (must be injected by wiring)", c.transport)
	}
	// Every future Pick on the empty chooser fails with the stable
	// no-instances error.
	_, _, _, err = c.chooser.Pick()
	if err == nil {
		t.Fatal("Pick() on the default chooser = nil error, want no-instances error")
	}
	var sdkErr *Error
	if !errors.As(err, &sdkErr) || sdkErr.Kind != KindUnavailable {
		t.Errorf("Pick() error = %v, want Kind %v", err, KindUnavailable)
	}
}

func TestNewRejectsNilOption(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("New(nil) = nil error, want an invalid-argument error")
	}
	var sdkErr *Error
	if !errors.As(err, &sdkErr) || sdkErr.Kind != KindInvalidArg {
		t.Errorf("New(nil) error = %v, want Kind %v", err, KindInvalidArg)
	}
}

func TestWithInstancesStoresInstancesInOrder(t *testing.T) {
	in := []Instance{
		{URL: "http://one", Username: "u1", Password: "p1"},
		{URL: "http://two"},
		{URL: "http://three"},
	}
	c, err := New(WithInstances(in))
	if err != nil {
		t.Fatalf("New(WithInstances) = error %v", err)
	}
	if len(c.chooser.order) != 3 {
		t.Fatalf("chooser holds %d instances, want 3", len(c.chooser.order))
	}
	for i, want := range []string{"http://one", "http://two", "http://three"} {
		if c.chooser.order[i].url != want {
			t.Errorf("order[%d].url = %q, want %q", i, c.chooser.order[i].url, want)
		}
		if !c.chooser.order[i].coolUntil.IsZero() {
			t.Errorf("order[%d] starts cooling, want a zero coolUntil", i)
		}
	}
	// The chooser must hold its own copy: later caller mutation of the input
	// slice must not leak into the rotation state.
	in[0].URL = "http://mutated"
	if c.chooser.order[0].url != "http://one" {
		t.Errorf("order[0].url = %q after caller mutation, want http://one (defensive copy)", c.chooser.order[0].url)
	}
}

// fakeTransport records a Get call; it only exists to prove injection.
type fakeTransport struct {
	called bool
}

func (f *fakeTransport) Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	f.called = true
	return nil, 0, errors.New("not used in this test")
}

func TestWithHTTPClientInjectsTransport(t *testing.T) {
	ft := &fakeTransport{}
	c, err := New(WithHTTPClient(ft))
	if err != nil {
		t.Fatalf("New(WithHTTPClient) = error %v", err)
	}
	if c.transport != Transport(ft) {
		t.Fatalf("transport was not injected: got %v", c.transport)
	}
}

func TestWithCooldownOverridesDefault(t *testing.T) {
	tests := []struct {
		name string
		opt  Options
		want time.Duration
	}{
		{"default stays 60s", nil, 60 * time.Second},
		{"override to 5s", WithCooldown(5 * time.Second), 5 * time.Second},
		{"explicit zero never blocks", WithCooldown(0), 0},
		{"negative means never block", WithCooldown(-time.Second), -time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c *Client
			var err error
			if tc.opt == nil {
				c, err = New()
			} else {
				c, err = New(tc.opt)
			}
			if err != nil {
				t.Fatalf("New() = error %v", err)
			}
			if got := c.chooser.cooldown; got != tc.want {
				t.Errorf("cooldown = %v, want %v", got, tc.want)
			}
		})
	}
}
