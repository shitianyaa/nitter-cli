// Package appapi is the implementation-layer composition root of the Nitter
// fetch path: it owns the real transport (httpx) and the instance rotation
// chooser, and hosts the endpoint implementations (RSS/HTML timeline,
// search, list, status) added in later milestones.
//
// Package boundary (frozen): appapi may import the sdk (package twitter) and
// httpx; the sdk never imports appapi — it defines the contract surface and
// the CLI layer wires the two. This file therefore only composes plain
// structs; the endpoint tasks fill the root in.
package appapi

import (
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// Client is the implementation-layer composition root: the real transport,
// the rotation chooser and the clock, ready for the endpoint methods to
// attach to. The zero value is not usable — build it through newClient (or
// fill the fields at the wiring site).
type Client struct {
	// HTTP is the paced, retried, classified GET transport.
	HTTP *httpx.Client
	// Chooser rotates instances in configuration order; nil until the wiring
	// task installs one.
	Chooser *twitter.Chooser
	// Now is the injectable clock; never nil after newClient.
	Now func() time.Time
}

// newClient composes a Client.
//
// A nil httpx.Client selects the default transport: httpx built with zero
// Options, whose zero value selects the documented defaults (Timeout 20s,
// RetryAttempts 2, RetryDelay 1s, MinInterval 1s — pinned by httpx's own
// tests). A nil now defaults to time.Now. The chooser is stored as passed.
func newClient(h *httpx.Client, ch *twitter.Chooser, now func() time.Time) (*Client, error) {
	if h == nil {
		var err error
		if h, err = httpx.New(httpx.Options{}); err != nil {
			return nil, twitter.Errorf(twitter.KindLocalState, opNewClient, "build default transport: %w", err)
		}
	}
	if now == nil {
		now = time.Now
	}
	return &Client{HTTP: h, Chooser: ch, Now: now}, nil
}

// opNewClient is the Op stamped on newClient's own errors.
const opNewClient = "appapi.newClient"
