package httpx

// Host-scoped basic auth: Options.BasicAuth attaches an Authorization header
// to requests whose URL host has credentials configured. These tests pin the
// contract that makes the feature leak-proof BY CONSTRUCTION: the decision is
// made inside the transport from the request's own URL host, so a credential
// can only ever ride a request addressed to its own configured host — never
// to a third-party host fetched through the same client (the media resolvers
// share this transport and gain nothing: their hosts are never in the map).
//
// Credentials are sent only for COMPLETE pairs: a username without a password
// (or vice versa, or an entirely empty pair) is treated as unconfigured.

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// basicHeaderValue is the exact Authorization value for the given pair.
func basicHeaderValue(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestBasicAuthSentToConfiguredHost(t *testing.T) {
	e := newTestClient(t, Options{
		BasicAuth: map[string]BasicCredentials{
			"inst.example": {Username: "user", Password: "p@ss:word"},
		},
	}, step{status: 200, body: "ok"})
	_, _, err := e.c.Get(context.Background(), "http://inst.example/user/rss", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := basicHeaderValue("user", "p@ss:word")
	if got := e.doer.requests[0].Header.Get("Authorization"); got != want {
		t.Errorf("Authorization = %q, want %q (base64 of user:password)", got, want)
	}
}

// TestBasicAuthCoversEveryRequestPath: Get, Post and GetMeta share the
// buffered send pipeline, but Download builds its requests in its own
// streaming loop — all four must carry the header, or a media download from
// the instance host would silently run unauthenticated (and, worse, the
// streaming path would be the one place a credential could diverge from the
// host-scoped rule).
func TestBasicAuthCoversEveryRequestPath(t *testing.T) {
	e := newTestClient(t, Options{
		BasicAuth: map[string]BasicCredentials{
			"inst.example": {Username: "u", Password: "p"},
		},
	},
		step{status: 200, body: "get"},
		step{status: 200, body: "post"},
		step{status: 200, body: "getmeta"},
		step{status: 200, body: "download"},
	)
	ctx := context.Background()
	want := basicHeaderValue("u", "p")

	if _, _, err := e.c.Get(ctx, "http://inst.example/a", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, _, err := e.c.Post(ctx, "http://inst.example/b", []byte("body"), nil); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if _, _, _, err := e.c.GetMeta(ctx, "http://inst.example/c", nil); err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	var sb strings.Builder
	if _, err := e.c.Download(ctx, "http://inst.example/d", &sb, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}

	for i, req := range e.doer.requests {
		if got := req.Header.Get("Authorization"); got != want {
			t.Errorf("request %d (%s %s) Authorization = %q, want %q", i, req.Method, req.URL, got, want)
		}
	}
}

// TestBasicAuthNotSentToOtherHosts is the leak-proof pin: the SAME client
// carries credentials for the instance host, yet requests addressed to any
// other host — the third-party media endpoints (fx/vx/syndication/xdown/
// twimg) — go out WITHOUT the header, on every request path. Caller-supplied
// headers coexist untouched.
func TestBasicAuthNotSentToOtherHosts(t *testing.T) {
	e := newTestClient(t, Options{
		BasicAuth: map[string]BasicCredentials{
			"inst.example": {Username: "u", Password: "p"},
		},
	},
		step{status: 200, body: "get"},
		step{status: 200, body: "post"},
		step{status: 200, body: "getmeta"},
		step{status: 200, body: "download"},
	)
	ctx := context.Background()
	headers := map[string]string{"User-Agent": "nitter-cli"}

	if _, _, err := e.c.Get(ctx, "http://cdn.thirdparty.example/x.jpg", headers); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, _, err := e.c.Post(ctx, "http://down.thirdparty.example/watch", []byte("body"), headers); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if _, _, _, err := e.c.GetMeta(ctx, "http://probe.thirdparty.example/v", headers); err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	var sb strings.Builder
	if _, err := e.c.Download(ctx, "http://video.thirdparty.example/v.mp4", &sb, headers); err != nil {
		t.Fatalf("Download: %v", err)
	}

	for i, req := range e.doer.requests {
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("request %d (%s %s) carried Authorization %q, want none", i, req.Method, req.URL, got)
		}
		if got := req.Header.Get("User-Agent"); got != "nitter-cli" {
			t.Errorf("request %d User-Agent = %q, want the caller's header untouched", i, got)
		}
	}
}

// TestBasicAuthIncompleteCredentialsNotSent: only a COMPLETE user+password
// pair authenticates. Username-only, password-only and empty entries are
// treated as unconfigured (documented decision — an empty credential half is
// never sent).
func TestBasicAuthIncompleteCredentialsNotSent(t *testing.T) {
	e := newTestClient(t, Options{
		BasicAuth: map[string]BasicCredentials{
			"user-only.example":  {Username: "u"},
			"pass-only.example":  {Password: "p"},
			"empty-pair.example": {},
		},
	},
		step{status: 200, body: "1"},
		step{status: 200, body: "2"},
		step{status: 200, body: "3"},
	)
	ctx := context.Background()
	for _, host := range []string{"user-only.example", "pass-only.example", "empty-pair.example"} {
		if _, _, err := e.c.Get(ctx, "http://"+host+"/rss", nil); err != nil {
			t.Fatalf("Get %s: %v", host, err)
		}
	}
	for i, req := range e.doer.requests {
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("request %d (%s) carried Authorization %q, want none (incomplete pair)", i, req.URL, got)
		}
	}
}

// TestBasicAuthHostMatchingPinsHostAndPort: the map key is the URL host
// (port included when present), matched case-insensitively; a different port
// is a different host and must not receive the credential.
func TestBasicAuthHostMatchingPinsHostAndPort(t *testing.T) {
	e := newTestClient(t, Options{
		BasicAuth: map[string]BasicCredentials{
			"InsT.Example:8443": {Username: "u", Password: "p"},
		},
	},
		step{status: 200, body: "match"},
		step{status: 200, body: "other-port"},
	)
	ctx := context.Background()
	if _, _, err := e.c.Get(ctx, "http://inst.example:8443/rss", nil); err != nil {
		t.Fatalf("Get with-port host: %v", err)
	}
	if _, _, err := e.c.Get(ctx, "http://inst.example/rss", nil); err != nil {
		t.Fatalf("Get default-port host: %v", err)
	}
	if got := e.doer.requests[0].Header.Get("Authorization"); got != basicHeaderValue("u", "p") {
		t.Errorf("port-qualified host Authorization = %q, want %q", got, basicHeaderValue("u", "p"))
	}
	if got := e.doer.requests[1].Header.Get("Authorization"); got != "" {
		t.Errorf("different port carried Authorization %q, want none", got)
	}
}

// TestBasicAuthPolicyWinsOverCallerHeader: if a caller ever passes its own
// Authorization header for a credentialed host, the host policy decides —
// deterministic, not call-order dependent.
func TestBasicAuthPolicyWinsOverCallerHeader(t *testing.T) {
	e := newTestClient(t, Options{
		BasicAuth: map[string]BasicCredentials{
			"inst.example": {Username: "u", Password: "p"},
		},
	}, step{status: 200, body: "ok"})
	_, _, err := e.c.Get(context.Background(), "http://inst.example/rss",
		map[string]string{"Authorization": "Basic stale"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := e.doer.requests[0].Header.Get("Authorization"); got != basicHeaderValue("u", "p") {
		t.Errorf("Authorization = %q, want the host policy value %q", got, basicHeaderValue("u", "p"))
	}
}
