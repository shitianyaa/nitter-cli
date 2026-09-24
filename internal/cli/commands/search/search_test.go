package search_test

// End-to-end tests for `nitter search`: httptest fake instances, temp-HOME
// config fixtures and cli.Run-level exit code assertions, mirroring the user
// command's test style.

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
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/search"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
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
	// fetch_backend has only mix and fx now, and these tests exercise the
	// instance path: point the Fx fast lane at a stub that answers 404 so mix
	// fails over to the configured fake instance (and a fixture with no
	// instances still reaches the chooser's "no instances configured" error).
	// Without this the fast lane would reach the real network.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(stub.Close)
	prevFxBase := fxtwitter.EndpointOverrides.BaseURL
	fxtwitter.EndpointOverrides.BaseURL = stub.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = prevFxBase })
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

// searchPage builds a Nitter-shaped search result page listing the given
// status ids and carrying a load-more cursor when non-empty.
func searchPage(ids []string, cursor string) string {
	var b strings.Builder
	b.WriteString(`<div class="timeline">`)
	for _, id := range ids {
		b.WriteString(`<div class="timeline-item">` +
			`<a class="tweet-link" href="/nasa/status/` + id + `"></a>` +
			`<div class="tweet-content">search body ` + id + `</div>` +
			`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
			`</div>`)
	}
	b.WriteString(`</div>`)
	if cursor != "" {
		b.WriteString(`<div class="show-more"><a href="?cursor=` + cursor + `">Load more</a></div>`)
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

// TestSearchPipeDefaultEmitsNDJSONEnvelopes pins the M10 pipe default: with
// stdout not a TTY and no output flag given, the hashtag search emits one
// nitter.pipeline/v1 tweet envelope per match — no flag needed (a TTY keeps
// the human rows; see the internal TTY test).
func TestSearchPipeDefaultEmitsNDJSONEnvelopes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=%23artemis": {200, searchPage([]string{"301", "302"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "#artemis")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want one per tweet:\n%s", len(envs), out)
	}
	for i, wantID := range []string{"301", "302"} {
		env := envs[i]
		if env["schema"] != "nitter.pipeline/v1" || env["kind"] != "tweet" || env["id"] != wantID {
			t.Errorf("envelope %d = %v, want kind tweet / id %s", i+1, env, wantID)
		}
		data := dataOf(t, env)
		if data["id"] != wantID || data["text"] != "search body "+wantID {
			t.Errorf("envelope %d data = %v, want the tweet payload", i+1, data)
		}
		meta, ok := env["meta"].(map[string]any)
		if !ok || meta["source"] != "search:#artemis" || meta["instance"] != fake.addr {
			t.Errorf("envelope %d meta = %v, want source/instance provenance", i+1, env["meta"])
		}
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/search?f=tweets&q=%23artemis"}) {
		t.Errorf("requests = %v, want the escaped search fetch", got)
	}
}

func TestSearchFromUserQueryPassesThrough(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=from%3Anasa": {200, searchPage([]string{"303"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "from:nasa")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1:\n%s", len(envs), out)
	}
	if data := dataOf(t, envs[0]); data["id"] != "303" || data["text"] != "search body 303" {
		t.Fatalf("data = %v, want the fetched tweet", envs[0])
	}
}

func TestSearchPaginationBoundedByMaxPages(t *testing.T) {
	home := tempHome(t)
	q := "/search?f=tweets&q=%23artemis"
	fake := newFake(t, map[string]answer{
		q:                {200, searchPage([]string{"301"}, "c1")},
		q + "&cursor=c1": {200, searchPage([]string{"302"}, "c2")},
		q + "&cursor=c2": {200, searchPage([]string{"303"}, "c3")},
		q + "&cursor=c3": {200, searchPage([]string{"304"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	// A limit larger than the four tweets on offer, so --max-pages is what
	// bounds the fetch (there is no "unlimited" limit any more).
	code, out, errOut := runCLI(t, "search", "#artemis", "--limit", "100", "--max-pages", "2")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Fatalf("got %d rows, want 2 (two pages worth):\n%s", n, out)
	}
	fetches := 0
	for _, r := range fake.rec.requests() {
		if strings.HasPrefix(r, "/search?") {
			fetches++
		}
	}
	if fetches != 2 {
		t.Errorf("search fetches = %d, want exactly 2 (bounded by --max-pages)", fetches)
	}
}

func TestSearchLimitFlagTruncates(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=moon": {200, searchPage([]string{"301", "302", "303"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "moon", "--limit", "2")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Fatalf("got %d rows, want 2:\n%s", n, out)
	}
}

func TestSearchEmptyQueryIsUsageErrorBeforeNetwork(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	for _, query := range []string{"", "   "} {
		code, out, errOut := runCLI(t, "search", query)
		if code != 2 {
			t.Fatalf("search %q: exit = %d, want 2 (stderr %q)", query, code, errOut)
		}
		if out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
	}
	if got := fake.rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (validation precedes any network)", got)
	}
}

// TestSearchEmptyPipeDefaultEmitsNothing: under the piped NDJSON default an
// empty result prints NOTHING on stdout or stderr (the "(empty)" hint is the
// TTY default's; see the internal TTY test).
func TestSearchEmptyPipeDefaultEmitsNothing(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=ghost": {200, searchPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "ghost")
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

func TestSearchAllInstancesFailExitsOne(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=x": {503, "down"},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "x")
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

func TestSearchJSONModes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=moon": {200, searchPage([]string{"301"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "moon", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if obj["id"] != "301" || obj["url"] != "https://x.com/nasa/status/301" {
		t.Errorf("json = %v, want the projected tweet", obj)
	}

	// Two tweets → an array; zero tweets → [].
	fake2 := newFake(t, map[string]answer{
		"/search?f=tweets&q=moon":  {200, searchPage([]string{"301", "302"}, "")},
		"/search?f=tweets&q=ghost": {200, searchPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake2.addr+"\"\n")
	code, out, _ = runCLI(t, "search", "moon", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("output = %q (%v), want a 2-element array", out, err)
	}
	code, out, _ = runCLI(t, "search", "ghost", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("output = %q, want []", out)
	}
}

func TestSearchNDJSONEnvelopes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=%23artemis": {200, searchPage([]string{"301", "302"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "#artemis", "--ndjson")
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
				Text   string         `json:"text"`
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
		wantID := "301"
		if i == 1 {
			wantID = "302"
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
		// meta.source carries the RAW (unescaped) query.
		if env.Meta.Source != "search:#artemis" {
			t.Errorf("line %d meta.source = %q, want %q", i+1, env.Meta.Source, "search:#artemis")
		}
		if env.Meta.Instance != fake.addr {
			t.Errorf("line %d meta.instance = %q, want the serving instance", i+1, env.Meta.Instance)
		}
		if !strings.HasSuffix(env.Meta.FetchedAt, "Z") {
			t.Errorf("line %d meta.fetched_at = %q, want RFC3339 UTC", i+1, env.Meta.FetchedAt)
		}
	}
}

func TestSearchInstanceFlagNeedsNoConfiguredInstance(t *testing.T) {
	tempHome(t) // baseline config with zero instances
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=moon": {200, searchPage([]string{"301"}, "")},
	})

	code, out, errOut := runCLI(t, "search", "moon", "--instance", fake.addr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1:\n%s", len(envs), out)
	}
	if data := dataOf(t, envs[0]); data["id"] != "301" {
		t.Fatalf("data = %v, want the fetched tweet", envs[0])
	}
}

func TestSearchNoInstancesExitsOne(t *testing.T) {
	home := tempHome(t) // zero instances, no --instance
	writeConfig(t, home, fastTOML)
	code, _, errOut := runCLI(t, "search", "moon")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "no instances configured") {
		t.Fatalf("stderr = %q, want the chooser's message", errOut)
	}
}

func TestSearchJSONAndNDJSONAreMutuallyExclusive(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "search", "moon", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--json") || !strings.Contains(errOut, "--ndjson") {
		t.Fatalf("stderr = %q, want it to name both flags", errOut)
	}
}

func TestSearchExtraArgsAreUsageError(t *testing.T) {
	tempHome(t)
	code, _, _ := runCLI(t, "search", "moon", "landing")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestSearchNonPositiveCapsAreUsageErrors(t *testing.T) {
	tempHome(t)
	for _, flag := range []string{"--limit=-5", "--limit=0", "--max-pages=0"} {
		code, _, errOut := runCLI(t, "search", "moon", flag)
		if code != 2 {
			t.Fatalf("%s: exit = %d, want 2 (stderr %q)", flag, code, errOut)
		}
		if !strings.Contains(errOut, "must be >= 1") {
			t.Fatalf("%s: stderr = %q, want it to state the >= 1 floor", flag, errOut)
		}
		if name, _, _ := strings.Cut(flag, "="); !strings.Contains(errOut, name) {
			t.Fatalf("%s: stderr = %q, want it to name %s", flag, errOut, name)
		}
	}
}

func TestSearchInvalidTypeIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "search", "query", "--type=unknown")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--type") {
		t.Fatalf("stderr = %q, want it to name --type", errOut)
	}
}

func TestSearchTypeUser(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/2/search/users" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"users": []map[string]any{
					{
						"screen_name":     "nasa",
						"name":            "NASA",
						"description":     "Space exploration agency",
						"followers_count": 80000000,
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	t.Run("TTY table output", func(t *testing.T) {
		var out, errOut strings.Builder
		s := &invocation.Streams{
			In:          strings.NewReader(""),
			Out:         &out,
			Err:         &errOut,
			OutIsTTY:    true,
			RootOptions: &invocation.RootOptions{},
		}
		cmd := search.New(s)
		cmd.SetArgs([]string{"nasa", "--type", "user"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if !strings.Contains(out.String(), "@nasa	NASA	80000000	Space exploration agency") {
			t.Errorf("out = %q, want profile row", out.String())
		}
	})

	t.Run("NDJSON output", func(t *testing.T) {
		code, out, _ := runCLI(t, "search", "nasa", "--type", "user", "--ndjson")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"kind":"profile"`) || !strings.Contains(out, `"handle":"nasa"`) {
			t.Errorf("out = %q, want profile NDJSON envelope", out)
		}
	})

	t.Run("JSON output", func(t *testing.T) {
		code, out, _ := runCLI(t, "search", "nasa", "--type", "user", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"handle":"nasa"`) {
			t.Errorf("out = %q, want profile JSON", out)
		}
	})
}

// The pair below pins the --type user contract: a query with no matches answers
// 200 + an empty user list, while a 404 is an upstream failure (verified live
// 2026-09-24). The 404 used to be swallowed into an empty success.
func searchUsersServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/2/search/users" {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchTypeUserNotFoundExitsOne(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")
	srv := searchUsersServer(t, http.StatusNotFound, `{"code":404,"results":[],"cursor":{"top":null,"bottom":null}}`)
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = "" })

	code, out, errOut := runCLI(t, "search", "NASA", "--type", "user", "--json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 — a 404 is a failure, not an empty result; stderr = %q", code, errOut)
	}
	if !strings.Contains(errOut, "not_found") {
		t.Errorf("stderr = %q, want the classified not_found kind", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing on the failure path", out)
	}
}

func TestSearchTypeUserEmptyStaysEmpty(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")
	srv := searchUsersServer(t, http.StatusOK, `{"code":200,"users":[]}`)
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = "" })

	code, out, errOut := runCLI(t, "search", "no-such-person-anywhere", "--type", "user", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — no matches is a success; stderr = %q", code, errOut)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("stdout = %q, want []", out)
	}
}
