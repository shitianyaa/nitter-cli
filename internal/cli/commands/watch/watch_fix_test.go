package watch_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
)

// fxStatusesBody builds an FxTwitter /2/profile/:user/statuses payload
// listing the given tweet ids (the fast-lane fixture the fx backend serves).
func fxStatusesBody(ids ...string) string {
	var b strings.Builder
	b.WriteString(`{"code":200,"results":[`)
	for i, id := range ids {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"` + id + `","id_str":"` + id + `","text":"fx body ` + id + `",` +
			`"author":{"screen_name":"NASA"},"created_at":"Sun Jul 05 09:09:40 +0000 2026"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// newFakeFx serves canned FxTwitter answers for the /2/profile/NASA/statuses
// endpoint and points the Fx base URL override at itself (the fx backend
// consults fxtwitter.EndpointOverrides, not [[instances]]). Requests are
// recorded so tests can assert the limit the fetch actually sent.
func newFakeFx(t *testing.T, ids ...string) (*fakeNitter, *[]string) {
	t.Helper()
	body := fxStatusesBody(ids...)
	var mu sync.Mutex
	var requests []string
	mux := http.NewServeMux()
	mux.HandleFunc("/2/profile/NASA/statuses", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		requests = append(requests, req.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	prev := fxtwitter.EndpointOverrides.BaseURL
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = prev })
	_ = client.SetFxBaseURLForTesting(srv.URL)

	fake := &fakeNitter{answers: map[string]answer{}, addr: srv.URL, srv: srv}
	return fake, &requests
}

// fxBackendConfig is the fast config fixture pinned to the fx backend (the
// [[instances]] entry keeps the nitter fallback wiring neutral; the fx
// backend never consults it).
func fxBackendConfig(fake *fakeNitter) string {
	return strings.Replace(instanceConfig(fake), `fetch_backend = "nitter"`, `fetch_backend = "fx"`, 1)
}

// TestWatchUserSourceFetchesTweetsFxBackend: watch's user source fetches the
// same tweets as the user command under the fx backend — the known "watch
// always 0 rows" regression. fetchSource passes limit 0 (= all) and the Fx
// fast lane treats count <= 0 as ZERO tweets with a nil error, so the cycle
// silently records an empty baseline. With the fix the fetch reaches the
// statuses endpoint and --include-existing emits the rows.
func TestWatchUserSourceFetchesTweetsFxBackend(t *testing.T) {
	home := tempHome(t)
	fake, _ := newFakeFx(t, "101", "102")
	writeConfig(t, home, fxBackendConfig(fake))

	code, out, errOut := runCLI(t, "watch", "user:NASA", "--once", "--include-existing",
		"--state-dir", filepath.Join(t.TempDir(), "state"), "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 2 || envs[0].ID != "101" || envs[1].ID != "102" {
		t.Fatalf("envelopes = %d (%v), want tweets 101 and 102 — the fx fetch must not silently come back empty", len(envs), out)
	}
}

// TestWatchUserSourceFxNoSilentEmptyBaseline: without --include-existing the
// first fx run still seeds the fetched ids into seen.json (seen_count > 0) —
// never the 0-tweet baseline the regression produced.
func TestWatchUserSourceFxNoSilentEmptyBaseline(t *testing.T) {
	home := tempHome(t)
	fake, _ := newFakeFx(t, "101", "102")
	writeConfig(t, home, fxBackendConfig(fake))
	stateDir := filepath.Join(t.TempDir(), "state")

	if code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--state-dir", stateDir); code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "seen.json"))
	if err != nil {
		t.Fatalf("read seen.json: %v", err)
	}
	var state struct {
		Sources map[string]struct {
			SeenIDs []string `json:"seen_ids"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode seen.json: %v", err)
	}
	src, ok := state.Sources["user:NASA"]
	if !ok {
		t.Fatalf("seen.json = %s, want a user:NASA source entry", data)
	}
	if len(src.SeenIDs) != 2 {
		t.Fatalf("seen_ids = %v, want [101 102] — an empty baseline is the regression", src.SeenIDs)
	}
}

// TestWatchUserSourceFxSendsPositiveLimit: the user source's fetch must send
// a positive count (limit 0 = all) — the regression sent limit=0 and the Fx
// fast lane short-circuited on count <= 0 before any request.
func TestWatchUserSourceFxSendsPositiveLimit(t *testing.T) {
	home := tempHome(t)
	fake, requests := newFakeFx(t, "101")
	writeConfig(t, home, fxBackendConfig(fake))

	if code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--state-dir", filepath.Join(t.TempDir(), "state")); code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	reqs := *requests
	if len(reqs) == 0 {
		t.Fatalf("requests = [], want at least one /2/profile/NASA/statuses fetch")
	}
	for _, q := range reqs {
		if strings.Contains(q, "limit=0") || strings.Contains(q, "count=0") {
			t.Errorf("query %q carries a zero limit — the fx fast lane short-circuits on count <= 0", q)
		}
	}
}
