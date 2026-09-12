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

func TestSearchOutputsRowsForHashtagQuery(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=%23artemis": {200, searchPage([]string{"301", "302"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "search", "#artemis")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want one row per tweet:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "301\t2026-07-20 14:11\t@nasa\tsearch body 301") {
		t.Errorf("row 0 = %q, want the ID/date/handle/text projection", lines[0])
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
	if !strings.Contains(out, "303\t2026-07-20 14:11\t@nasa\tsearch body 303") {
		t.Fatalf("output = %q, want the fetched row", out)
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

	code, out, errOut := runCLI(t, "search", "#artemis", "--limit", "0", "--max-pages", "2")
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

func TestSearchEmptyResultPrintsHintAndExitsZero(t *testing.T) {
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
	if strings.TrimSpace(errOut) != "(empty)" {
		t.Errorf("stderr = %q, want the (empty) hint", errOut)
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
	if !strings.HasPrefix(out, "301\t") {
		t.Fatalf("output = %q, want the fetched tweet", out)
	}
}

func TestSearchNoInstancesExitsOne(t *testing.T) {
	tempHome(t) // zero instances, no --instance
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

func TestSearchNegativeLimitIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "search", "moon", "--limit=-5")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--limit") {
		t.Fatalf("stderr = %q, want it to name the flag", errOut)
	}
}
