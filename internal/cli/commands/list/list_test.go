package list_test

// End-to-end tests for `nitter list`: httptest fake instances, temp-HOME
// config fixtures and cli.Run-level exit code assertions, mirroring the
// user/search command test style.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
)

// fastTOML disables retries, backoff and pacing so fetches against httptest
// stay fast.
const fastTOML = "retry_attempts = -1\nretry_delay = \"-1s\"\nrequest_interval = \"-1s\"\ninstance_cooldown = \"-1s\"\n"

// tempHome redirects the home directory to a fresh temp dir and neutralizes
// the settings and proxy env overrides.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"NITTER_DEFAULT_LIMIT", "NITTER_LOG_LEVEL", "NITTER_LOG_FORMAT",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, "")
	}
	return home
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".nitter-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// fakeNitter serves canned answers keyed by the exact request URI
// (path?query) and records every request target.
type fakeNitter struct {
	rec  *recorder
	mux  *http.ServeMux
	srv  *httptest.Server
	addr string
}

type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) add(target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, target)
}

func (r *recorder) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func newFake(t *testing.T, answers map[string]answer) *fakeNitter {
	t.Helper()
	rec := &recorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		rec.add(req.URL.RequestURI())
		a, ok := answers[req.URL.RequestURI()]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fakeNitter{rec: rec, mux: mux, srv: srv, addr: srv.URL}
}

type answer struct {
	status int
	body   string
}

// listPage builds a Nitter-shaped list page (the timeline-item markup lists
// share with user timelines and search) listing the given status ids and
// carrying a load-more cursor when non-empty.
func listPage(ids []string, cursor string) string {
	var b strings.Builder
	b.WriteString(`<div class="timeline">`)
	for _, id := range ids {
		b.WriteString(`<div class="timeline-item">` +
			`<a class="tweet-link" href="/nasa/status/` + id + `"></a>` +
			`<div class="tweet-content">list body ` + id + `</div>` +
			`<span class="tweet-date"><a title="Jul 21, 2026 · 8:00 AM UTC">Jul 21</a></span>` +
			`</div>`)
	}
	b.WriteString(`</div>`)
	if cursor != "" {
		b.WriteString(`<div class="show-more"><a href="/i/lists/12345?cursor=` + cursor + `">Load more</a></div>`)
	}
	return b.String()
}

// parseEnvelopes splits the stdout into NDJSON envelope lines (the piped
// default since M10) and decodes each into a generic object.
func parseEnvelopes(t *testing.T, out string) []map[string]any {
	t.Helper()
	if out == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	envs := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d is not one JSON envelope: %v\n%s", i+1, err, line)
		}
		envs = append(envs, env)
	}
	return envs
}

// dataOf returns the envelope's data object.
func dataOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("envelope carries no data object: %v", env)
	}
	return data
}

// TestListPipeDefaultEmitsNDJSONEnvelopes pins the M10 pipe default: with
// stdout not a TTY and no output flag given, the list timeline emits one
// nitter.pipeline/v1 tweet envelope per tweet — no flag needed (a TTY keeps
// the human rows; see the internal TTY test).
func TestListPipeDefaultEmitsNDJSONEnvelopes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/i/lists/12345": {200, listPage([]string{"401", "402"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "list", "12345")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want one per tweet:\n%s", len(envs), out)
	}
	for i, wantID := range []string{"401", "402"} {
		env := envs[i]
		if env["schema"] != "nitter.pipeline/v1" || env["kind"] != "tweet" || env["id"] != wantID {
			t.Errorf("envelope %d = %v, want kind tweet / id %s", i+1, env, wantID)
		}
		data := dataOf(t, env)
		if data["id"] != wantID || data["text"] != "list body "+wantID {
			t.Errorf("envelope %d data = %v, want the tweet payload", i+1, data)
		}
		meta, ok := env["meta"].(map[string]any)
		if !ok || meta["source"] != "list:12345" || meta["instance"] != fake.addr {
			t.Errorf("envelope %d meta = %v, want source/instance provenance", i+1, env["meta"])
		}
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/i/lists/12345"}) {
		t.Errorf("requests = %v, want the single list fetch", got)
	}
}

func TestListPaginationBoundedByMaxPages(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/i/lists/12345":           {200, listPage([]string{"401"}, "c1")},
		"/i/lists/12345?cursor=c1": {200, listPage([]string{"402"}, "c2")},
		"/i/lists/12345?cursor=c2": {200, listPage([]string{"403"}, "c3")},
		"/i/lists/12345?cursor=c3": {200, listPage([]string{"404"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "list", "12345", "--limit", "0", "--max-pages", "2")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Fatalf("got %d rows, want 2 (two pages worth):\n%s", n, out)
	}
	fetches := 0
	for _, r := range fake.rec.requests() {
		if strings.HasPrefix(r, "/i/lists/") {
			fetches++
		}
	}
	if fetches != 2 {
		t.Errorf("list fetches = %d, want exactly 2 (bounded by --max-pages)", fetches)
	}
}

func TestListInvalidIDIsUsageErrorBeforeNetwork(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	for _, listID := range []string{"", "   ", "12 345", "12?345", "12#345", "12/345"} {
		code, out, errOut := runCLI(t, "list", listID)
		if code != 2 {
			t.Fatalf("list %q: exit = %d, want 2 (stderr %q)", listID, code, errOut)
		}
		if out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
	}
	if got := fake.rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (validation precedes any network)", got)
	}
}

// TestListEmptyPipeDefaultEmitsNothing: under the piped NDJSON default an
// empty result prints NOTHING on stdout or stderr (the "(empty)" hint is the
// TTY default's; see the internal TTY test).
func TestListEmptyPipeDefaultEmitsNothing(t *testing.T) {
	// A genuinely empty page is a success, not an error — but an empty list
	// may also mean the list is new and not yet ingested (indistinguishable
	// server-side); the help documents that, the command must not invent an
	// error.
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/i/lists/12345": {200, listPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "list", "12345")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing in the NDJSON default", errOut)
	}
}

func TestListAllInstancesFailExitsOne(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/i/lists/12345": {503, "down"},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "list", "12345")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "upstream_unavailable") {
		t.Fatalf("stderr = %q, want the classified kind", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing on the failure path", out)
	}
}

func TestListJSONModes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/i/lists/12345": {200, listPage([]string{"401"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "list", "12345", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if obj["id"] != "401" || obj["url"] != "https://x.com/nasa/status/401" {
		t.Errorf("json = %v, want the projected tweet", obj)
	}

	// Two tweets → an array; zero tweets → [].
	fake2 := newFake(t, map[string]answer{
		"/i/lists/12345": {200, listPage([]string{"401", "402"}, "")},
		"/i/lists/99999": {200, listPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake2.addr+"\"\n")
	code, out, _ = runCLI(t, "list", "12345", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("output = %q (%v), want a 2-element array", out, err)
	}
	code, out, _ = runCLI(t, "list", "99999", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("output = %q, want []", out)
	}
}

func TestListNDJSONEnvelopes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/i/lists/12345": {200, listPage([]string{"401", "402"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "list", "12345", "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want one envelope per tweet:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var env struct {
			Schema string `json:"schema"`
			Kind   string `json:"kind"`
			ID     string `json:"id"`
			Data   struct {
				ID     string         `json:"id"`
				Author map[string]any `json:"author"`
			} `json:"data"`
			Meta *struct {
				Source    string `json:"source"`
				Instance  string `json:"instance"`
				FetchedAt string `json:"fetched_at"`
			} `json:"meta"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i+1, err, line)
		}
		wantID := "401"
		if i == 1 {
			wantID = "402"
		}
		if env.Schema != "nitter.pipeline/v1" || env.Kind != "tweet" || env.ID != wantID {
			t.Errorf("line %d envelope = %s, want kind tweet / id %s", i+1, line, wantID)
		}
		if env.Data.Author["handle"] != "nasa" {
			t.Errorf("line %d data = %s", i+1, line)
		}
		if env.Meta == nil {
			t.Fatalf("line %d carries no meta: %s", i+1, line)
		}
		if env.Meta.Source != "list:12345" {
			t.Errorf("line %d meta.source = %q, want %q", i+1, env.Meta.Source, "list:12345")
		}
		if env.Meta.Instance != fake.addr {
			t.Errorf("line %d meta.instance = %q, want the serving instance", i+1, env.Meta.Instance)
		}
		if !strings.HasSuffix(env.Meta.FetchedAt, "Z") {
			t.Errorf("line %d meta.fetched_at = %q, want RFC3339 UTC", i+1, env.Meta.FetchedAt)
		}
	}
}

func TestListInstanceFlagNeedsNoConfiguredInstance(t *testing.T) {
	tempHome(t) // baseline config with zero instances
	fake := newFake(t, map[string]answer{
		"/i/lists/12345": {200, listPage([]string{"401"}, "")},
	})

	code, out, errOut := runCLI(t, "list", "12345", "--instance", fake.addr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1:\n%s", len(envs), out)
	}
	if data := dataOf(t, envs[0]); data["id"] != "401" {
		t.Fatalf("data = %v, want the fetched tweet", envs[0])
	}
}

func TestListNoInstancesExitsOne(t *testing.T) {
	tempHome(t) // zero instances, no --instance
	code, _, errOut := runCLI(t, "list", "12345")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "no instances configured") {
		t.Fatalf("stderr = %q, want the chooser's message", errOut)
	}
}

func TestListJSONAndNDJSONAreMutuallyExclusive(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "list", "12345", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--json") || !strings.Contains(errOut, "--ndjson") {
		t.Fatalf("stderr = %q, want it to name both flags", errOut)
	}
}

func TestListExtraArgsAreUsageError(t *testing.T) {
	tempHome(t)
	code, _, _ := runCLI(t, "list", "12345", "extra")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestListNegativeLimitIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "list", "12345", "--limit=-5")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--limit") {
		t.Fatalf("stderr = %q, want it to name the flag", errOut)
	}
}
