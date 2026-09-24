package user_test

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
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
)

// fastTOML disables retries, backoff and pacing so fetches against httptest
// stay fast. Negative values are documented opt-outs in httpx; hand-edited
// config.toml is the user's power tool.
const fastTOML = "retry_attempts = -1\nretry_delay = \"-1s\"\nrequest_interval = \"-1s\"\ninstance_cooldown = \"-1s\"\n"

// tempHome redirects the home directory to a fresh temp dir and neutralizes
// the settings and proxy env overrides.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"NITTER_DEFAULT_LIMIT", "NITTER_LOG_LEVEL", "NITTER_LOG_FORMAT", "NITTER_FETCH_BACKEND",
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

// rssBody builds a Nitter-shaped RSS feed listing the given status ids.
func rssBody(ids ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel>`)
	for _, id := range ids {
		b.WriteString(`<item>` +
			`<guid>https://nitter.example/NASA/status/` + id + `#m</guid>` +
			`<link>https://nitter.example/NASA/status/` + id + `</link>` +
			`<dc:creator>@NASA</dc:creator>` +
			`<title>rss ` + id + `</title>` +
			`<description><![CDATA[<div class="tweet-content">rss body ` + id + `</div>]]></description>` +
			`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate></item>`)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// htmlPage builds a Nitter-shaped user timeline listing the given status ids
// and carrying a load-more cursor when non-empty.
func htmlPage(ids []string, cursor string) string {
	var b strings.Builder
	b.WriteString(`<div class="timeline">`)
	for _, id := range ids {
		b.WriteString(`<div class="timeline-item">` +
			`<a class="tweet-link" href="/NASA/status/` + id + `"></a>` +
			`<div class="tweet-content">html body ` + id + `</div>` +
			`<span class="tweet-date"><a title="Jul 5, 2026 · 9:09 AM UTC">Jul 5, 2026</a></span>` +
			`</div>`)
	}
	b.WriteString(`</div>`)
	if cursor != "" {
		b.WriteString(`<div class="show-more"><a href="/NASA?cursor=` + cursor + `">Load more</a></div>`)
	}
	return b.String()
}

// photoRSS is the R13 money shot: one item carrying the same photo twice —
// a direct pbs.twimg.com media:content URL and the percent-encoded /pic/
// proxy of the description img.
const photoRSS = `<?xml version="1.0"?><rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:media="http://search.yahoo.com/mrss/"><channel>` +
	`<item>` +
	`<guid>https://nitter.example/NASA/status/101#m</guid>` +
	`<link>https://nitter.example/NASA/status/101</link>` +
	`<dc:creator>@NASA</dc:creator>` +
	`<title>photo tweet</title>` +
	`<description><![CDATA[<div class="tweet-content">body text</div><img src="/pic/media%2FFxxx1.jpg%3Fname%3Dsmall"/>]]></description>` +
	`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate>` +
	`<media:content url="https://pbs.twimg.com/media/Fxxx1.jpg?name=small" medium="image"/>` +
	`</item></channel></rss>`

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

// TestUserPipeDefaultEmitsNDJSONEnvelopes pins the M10 pipe default: with
// stdout not a TTY and no output flag given, the RSS path emits one
// nitter.pipeline/v1 tweet envelope per tweet — no flag needed (a TTY keeps
// the human rows; see the internal TTY test).
func TestUserPipeDefaultEmitsNDJSONEnvelopes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
		"/NASA":     {200, htmlPage([]string{"201"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want one per tweet:\n%s", len(envs), out)
	}
	for i, wantID := range []string{"101", "102"} {
		env := envs[i]
		if env["schema"] != "nitter.pipeline/v1" || env["kind"] != "tweet" || env["id"] != wantID {
			t.Errorf("envelope %d = %v, want kind tweet / id %s", i+1, env, wantID)
		}
		data := dataOf(t, env)
		if data["id"] != wantID || data["text"] != "rss body "+wantID {
			t.Errorf("envelope %d data = %v, want the tweet payload", i+1, data)
		}
		meta, ok := env["meta"].(map[string]any)
		if !ok || meta["source"] != "user:NASA" || meta["instance"] != fake.addr {
			t.Errorf("envelope %d meta = %v, want source/instance provenance", i+1, env["meta"])
		}
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/NASA/rss"}) {
		t.Errorf("requests = %v, want only the RSS fetch", got)
	}
}

func TestUserLimitFlagTruncates(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102", "103")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA", "--limit", "2")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Fatalf("got %d rows, want 2:\n%s", n, out)
	}
}

func TestUserLimitDefaultsFromConfig(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102", "103")},
	})
	writeConfig(t, home, fastTOML+"default_limit = 1\n[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, _ := runCLI(t, "user", "NASA")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("got %d rows, want 1 (config default_limit):\n%s", n, out)
	}

	// An explicit --limit overrides the config default. 100 exceeds the three
	// tweets the fixture serves, so the whole timeline comes back; there is no
	// "unlimited" spelling any more, so a cap is always stated. Pinned to the
	// instance path because the assertion is about the RSS/HTML pagination
	// chain rather than the Fx fast lane.
	code, out, _ = runCLI(t, "--instance", fake.addr, "user", "NASA", "--limit", "100")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if n := strings.Count(out, "\n"); n != 3 {
		t.Fatalf("got %d rows, want 3 (the whole fixture timeline):\n%s", n, out)
	}
}

func TestUserRSSFailureFallsBackToHTML(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {500, "boom"},
		"/NASA":     {200, htmlPage([]string{"201"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1:\n%s", len(envs), out)
	}
	if data := dataOf(t, envs[0]); data["id"] != "201" || data["text"] != "html body 201" {
		t.Fatalf("data = %v, want the HTML-fallback tweet", envs[0])
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/NASA/rss", "/NASA"}) {
		t.Errorf("requests = %v, want the RSS attempt then the HTML fallback", got)
	}
}

func TestUserEmptyRSSTriggersHTMLFallback(t *testing.T) {
	// R15: a feed that answers but yields nothing ALSO falls back to the
	// HTML user page.
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody()},
		"/NASA":     {200, htmlPage([]string{"201"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1:\n%s", len(envs), out)
	}
	if data := dataOf(t, envs[0]); data["id"] != "201" {
		t.Fatalf("data = %v, want the HTML-fallback tweet", envs[0])
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/NASA/rss", "/NASA"}) {
		t.Errorf("requests = %v, want the RSS attempt then the HTML fallback", got)
	}
}

func TestUserBothFailExitsOneWithClassifiedError(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {500, "boom"},
		"/NASA":     {503, "down"},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA")
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

func TestUserLargeLimitBoundedByMaxPages(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss":       {500, "boom"},
		"/NASA":           {200, htmlPage([]string{"201"}, "c1")},
		"/NASA?cursor=c1": {200, htmlPage([]string{"202"}, "c2")},
		"/NASA?cursor=c2": {200, htmlPage([]string{"203"}, "c3")},
		"/NASA?cursor=c3": {200, htmlPage([]string{"204"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	// A limit larger than the four tweets the chain serves, so MaxPages is the
	// only stop signal: exactly 2 HTML pages may be fetched. Pinned to the
	// instance path — the assertion is about the RSS/HTML paging chain.
	code, out, errOut := runCLI(t, "--instance", fake.addr, "user", "NASA", "--limit", "100", "--max-pages", "2")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Fatalf("got %d rows, want 2 (two pages worth):\n%s", n, out)
	}
	htmlFetches := 0
	for _, r := range fake.rec.requests() {
		if r == "/NASA" || strings.HasPrefix(r, "/NASA?cursor=") {
			htmlFetches++
		}
	}
	if htmlFetches != 2 {
		t.Errorf("html fetches = %d, want exactly 2 (bounded by --max-pages)", htmlFetches)
	}
}

// TestUserRejectsNonPositiveCaps: both flags are caps, so 0 and negatives are
// usage errors. 0 used to mean "all" for --limit and "lift the page cap" for
// --max-pages; the two fetch lanes read it in opposite ways, so the spelling is
// gone and the rejection happens before any network. The omitted flags still
// resolve from the config.
func TestUserRejectsNonPositiveCaps(t *testing.T) {
	answers := map[string]answer{
		"/NASA/rss":       {500, "boom"},
		"/NASA":           {200, htmlPage([]string{"201"}, "c1")},
		"/NASA?cursor=c1": {200, htmlPage([]string{"202"}, "c2")},
		"/NASA?cursor=c2": {200, htmlPage([]string{"203"}, "c3")},
		"/NASA?cursor=c3": {200, htmlPage([]string{"204"}, "c4")},
		"/NASA?cursor=c4": {200, htmlPage([]string{"205"}, "c5")},
		"/NASA?cursor=c5": {200, htmlPage([]string{"206"}, "c6")},
		"/NASA?cursor=c6": {200, htmlPage([]string{"207"}, "")},
	}

	home := tempHome(t)
	fake := newFake(t, answers)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	for _, flag := range []string{"--limit=0", "--limit=-1", "--max-pages=0", "--max-pages=-1"} {
		code, out, errOut := runCLI(t, "--instance", fake.addr, "user", "NASA", flag)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stderr %q)", flag, code, errOut)
		}
		if !strings.Contains(errOut, "must be >= 1") {
			t.Errorf("%s: stderr = %q, want it to state the >= 1 floor", flag, errOut)
		}
		if out != "" {
			t.Errorf("%s: stdout = %q, want nothing", flag, out)
		}
	}
	if got := fake.rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none: the rejection must precede any network", got)
	}

	// Omitted flags: the config's max_pages still caps (2 of the 7 pages).
	home2 := tempHome(t)
	fake2 := newFake(t, answers)
	writeConfig(t, home2, fastTOML+"max_pages = 2\n[[instances]]\nurl = \""+fake2.addr+"\"\n")
	code, out, errOut := runCLI(t, "--instance", fake2.addr, "user", "NASA")
	if code != 0 {
		t.Fatalf("omitted flags: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Errorf("omitted flags: got %d rows, want 2 (config max_pages cap):\n%s", n, out)
	}
}

func TestUserInvalidHandleIsUsageErrorBeforeNetwork(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA!")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "handle") {
		t.Fatalf("stderr = %q, want it to name the handle problem", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if got := fake.rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (validation precedes any network)", got)
	}
}

func TestUserJSONSingleObjectArrayAndEmpty(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if obj["id"] != "101" || obj["url"] != "https://x.com/NASA/status/101" {
		t.Errorf("json = %v, want the projected tweet", obj)
	}

	// Two tweets → an array.
	fake2 := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake2.addr+"\"\n")
	code, out, _ = runCLI(t, "user", "NASA", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("output = %q (%v), want a 2-element array", out, err)
	}

	// Zero tweets → a literal empty array.
	fake3 := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody()},
		"/NASA":     {200, htmlPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake3.addr+"\"\n")
	code, out, _ = runCLI(t, "user", "NASA", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("output = %q, want []", out)
	}
}

func TestUserNDJSONEnvelopes(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA", "--ndjson")
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
				ID     string           `json:"id"`
				Text   string           `json:"text"`
				Author map[string]any   `json:"author"`
				Media  []map[string]any `json:"media"`
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
		wantID := "101"
		if i == 1 {
			wantID = "102"
		}
		if env.Schema != "nitter.pipeline/v1" || env.Kind != "tweet" || env.ID != wantID {
			t.Errorf("line %d envelope = %s, want kind tweet / id %s", i+1, line, wantID)
		}
		if env.Data.ID != wantID || env.Data.Author["handle"] != "NASA" {
			t.Errorf("line %d data = %s", i+1, line)
		}
		if env.Meta == nil {
			t.Fatalf("line %d carries no meta: %s", i+1, line)
		}
		if env.Meta.Source != "user:NASA" {
			t.Errorf("line %d meta.source = %q, want %q", i+1, env.Meta.Source, "user:NASA")
		}
		if env.Meta.Instance != fake.addr {
			t.Errorf("line %d meta.instance = %q, want the serving instance", i+1, env.Meta.Instance)
		}
		if !strings.HasSuffix(env.Meta.FetchedAt, "Z") {
			t.Errorf("line %d meta.fetched_at = %q, want RFC3339 UTC", i+1, env.Meta.FetchedAt)
		}
	}
}

// TestUserEmptyPipeDefaultEmitsNothing: under the piped NDJSON default an
// empty timeline prints NOTHING — no stdout envelopes, no stderr hint (the
// "(empty)" hint is the TTY default's; see the internal TTY test).
func TestUserEmptyPipeDefaultEmitsNothing(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody()},
		"/NASA":     {200, htmlPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA")
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

func TestUserInstanceFlagNeedsNoConfiguredInstance(t *testing.T) {
	tempHome(t) // baseline config with zero instances
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})

	code, out, errOut := runCLI(t, "user", "NASA", "--instance", fake.addr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1:\n%s", len(envs), out)
	}
	if data := dataOf(t, envs[0]); data["id"] != "101" {
		t.Fatalf("data = %v, want the fetched tweet", envs[0])
	}
}

func TestUserNoInstancesExitsOne(t *testing.T) {
	home := tempHome(t) // zero instances, no --instance
	writeConfig(t, home, fastTOML)
	code, _, errOut := runCLI(t, "user", "NASA")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "no instances configured") {
		t.Fatalf("stderr = %q, want the chooser's message", errOut)
	}
}

func TestUserR13SamePhotoInTwoFormsIsOneMediaEntry(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, photoRSS},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj struct {
		Media []map[string]any `json:"media"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if len(obj.Media) != 1 {
		t.Fatalf("media = %v, want exactly ONE entry for the same photo", obj.Media)
	}
	if obj.Media[0]["url"] != "https://pbs.twimg.com/media/Fxxx1.jpg?name=orig" {
		t.Errorf("media url = %v, want the canonical pbs name=orig URL", obj.Media[0]["url"])
	}
	if obj.Media[0]["type"] != "image" {
		t.Errorf("media type = %v, want image", obj.Media[0]["type"])
	}
}

func TestUserJSONAndNDJSONAreMutuallyExclusive(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "user", "NASA", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--json") || !strings.Contains(errOut, "--ndjson") {
		t.Fatalf("stderr = %q, want it to name both flags", errOut)
	}
}

func TestUserExtraArgsAreUsageError(t *testing.T) {
	tempHome(t)
	code, _, _ := runCLI(t, "user", "NASA", "extra")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestUserNegativeLimitIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "user", "NASA", "--limit=-5")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--limit") {
		t.Fatalf("stderr = %q, want it to name the flag", errOut)
	}
}

func TestUserConfiguredInstanceIsUsedWithoutFlag(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA", "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "\"instance\":\""+fake.addr+"\"") {
		t.Fatalf("output = %q, want meta.instance to carry the configured instance", out)
	}
}

// htmlPageWithRetweet builds a Nitter-shaped user timeline with one pure
// retweet (stock anchor-less retweet-header markup, status 555 by another
// account) followed by one normal tweet (201) — the --no-reposts fixture.
func htmlPageWithRetweet() string {
	return `<div class="timeline">` +
		`<div class="timeline-item">` +
		`<a class="tweet-link" href="/repolygon/status/555"></a>` +
		`<div class="tweet-body">` +
		`<div class="retweet-header"><span><div class="icon-container"><span class="icon-retweet" title=""></span> NASA retweeted</div></span></div>` +
		`<div class="tweet-content">reposted body 555</div>` +
		`<span class="tweet-date"><a title="Jul 5, 2026 · 9:09 AM UTC">Jul 5, 2026</a></span>` +
		`</div></div>` +
		`<div class="timeline-item">` +
		`<a class="tweet-link" href="/NASA/status/201"></a>` +
		`<div class="tweet-content">html body 201</div>` +
		`<span class="tweet-date"><a title="Jul 5, 2026 · 9:09 AM UTC">Jul 5, 2026</a></span>` +
		`</div></div>`
}

// TestUserNoRepostsDropsRetweets: --no-reposts removes pure retweets after
// the fetch; without the flag the fixture's retweet is part of the output.
// The retweet header only exists on the HTML path, so the RSS feed serves
// empty and the HTML user page carries both tweets.
func TestUserNoRepostsDropsRetweets(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody()},
		"/NASA":     {200, htmlPageWithRetweet()},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	// Control: unfiltered --json prints a 2-element array led by the
	// retweet (proving the fixture has one).
	code, out, errOut := runCLI(t, "user", "NASA", "--json")
	if code != 0 {
		t.Fatalf("control: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var all []map[string]any
	if err := json.Unmarshal([]byte(out), &all); err != nil || len(all) != 2 {
		t.Fatalf("control: output = %q (%v), want a 2-element array", out, err)
	}
	if all[0]["id"] != "555" || all[0]["is_retweet"] != true {
		t.Errorf("control: tweet 0 = %v, want the pure retweet 555", all[0])
	}

	// Filtered: only the normal tweet survives; --json prints one object.
	code, out, errOut = runCLI(t, "user", "NASA", "--no-reposts", "--json")
	if code != 0 {
		t.Fatalf("filtered: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var one map[string]any
	if err := json.Unmarshal([]byte(out), &one); err != nil {
		t.Fatalf("filtered: output is not one JSON object: %v\n%s", err, out)
	}
	if one["id"] != "201" || one["is_retweet"] != false {
		t.Errorf("filtered: object = %v, want the normal tweet 201", one)
	}
}

// retweetRSS is one self-authored item (101) and one retweeted item whose
// guid/link and dc:creator point at the ORIGINAL author NASAhistory (103) —
// real Nitter user RSS shapes a retweet as the original tweet, so the author
// mismatch is the only repost signal the feed carries.
const retweetRSS = `<?xml version="1.0"?><rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel>` +
	`<item>` +
	`<guid>https://nitter.example/NASA/status/101#m</guid>` +
	`<link>https://nitter.example/NASA/status/101</link>` +
	`<dc:creator>@NASA</dc:creator>` +
	`<title>rss 101</title>` +
	`<description><![CDATA[<div class="tweet-content">rss body 101</div>]]></description>` +
	`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate></item>` +
	`<item>` +
	`<guid>https://nitter.example/NASAhistory/status/103#m</guid>` +
	`<link>https://nitter.example/NASAhistory/status/103</link>` +
	`<dc:creator>@NASAhistory</dc:creator>` +
	`<title>rss 103</title>` +
	`<description><![CDATA[<div class="tweet-content">rss body 103</div>]]></description>` +
	`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate></item>` +
	`</channel></rss>`

// TestUserRSSRepostsFlaggedByAuthorMismatch: on the user RSS path a retweet
// surfaces as an item authored by someone else — the command output must flag
// it is_retweet and --no-reposts must drop it, keeping the self-authored tweet.
func TestUserRSSRepostsFlaggedByAuthorMismatch(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, retweetRSS},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	// Control: --json prints both, with only the foreign-author item flagged.
	code, out, errOut := runCLI(t, "user", "NASA", "--json")
	if code != 0 {
		t.Fatalf("control: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var all []map[string]any
	if err := json.Unmarshal([]byte(out), &all); err != nil || len(all) != 2 {
		t.Fatalf("control: output = %q (%v), want a 2-element array", out, err)
	}
	if all[0]["id"] != "101" || all[0]["is_retweet"] != false {
		t.Errorf("control: tweet 0 = %v, want the self-authored 101 unflagged", all[0])
	}
	if all[1]["id"] != "103" || all[1]["is_retweet"] != true {
		t.Errorf("control: tweet 1 = %v, want the foreign-author 103 flagged", all[1])
	}

	// Filtered: only the self-authored tweet survives; --json prints one object.
	code, out, errOut = runCLI(t, "user", "NASA", "--no-reposts", "--json")
	if code != 0 {
		t.Fatalf("filtered: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var one map[string]any
	if err := json.Unmarshal([]byte(out), &one); err != nil {
		t.Fatalf("filtered: output is not one JSON object: %v\n%s", err, out)
	}
	if one["id"] != "101" || one["is_retweet"] != false {
		t.Errorf("filtered: object = %v, want the self-authored tweet 101", one)
	}
}

// photoItem102 is one RSS item carrying a media:content photo (status 102).
const photoItem102 = `<item>` +
	`<guid>https://nitter.example/NASA/status/102#m</guid>` +
	`<link>https://nitter.example/NASA/status/102</link>` +
	`<dc:creator>@NASA</dc:creator>` +
	`<title>photo tweet</title>` +
	`<description><![CDATA[<div class="tweet-content">rss body 102</div>]]></description>` +
	`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate>` +
	`<media:content url="https://pbs.twimg.com/media/Fxxx2.jpg?name=small" medium="image"/>` +
	`</item>`

// rssBodyWithPhoto builds an RSS feed with one media-less tweet (101) and
// one photo tweet (102) — the media-filter fixture.
func rssBodyWithPhoto() string {
	return strings.Replace(rssBody("101"), "</channel></rss>", photoItem102+"</channel></rss>", 1)
}

// TestUserMediaFilters: --media-only keeps only media-carrying tweets,
// --media-type narrows further to one media kind, and an invalid --media-type
// value is a usage error (exit 2) before any network.
func TestUserMediaFilters(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBodyWithPhoto()},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	// --media-only: only the photo tweet (102) survives; --json prints one
	// object.
	code, out, errOut := runCLI(t, "user", "NASA", "--media-only", "--json")
	if code != 0 {
		t.Fatalf("--media-only: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var one map[string]any
	if err := json.Unmarshal([]byte(out), &one); err != nil {
		t.Fatalf("--media-only: output is not one JSON object: %v\n%s", err, out)
	}
	if one["id"] != "102" {
		t.Errorf("--media-only: object = %v, want the photo tweet 102", one)
	}

	// --media-type image: same survivor.
	code, out, _ = runCLI(t, "user", "NASA", "--media-type", "image", "--json")
	if code != 0 {
		t.Fatalf("--media-type image: exit = %d, want 0", code)
	}
	one = nil
	if err := json.Unmarshal([]byte(out), &one); err != nil || one["id"] != "102" {
		t.Errorf("--media-type image: output = %q (%v), want tweet 102", out, err)
	}

	// --media-type video: no tweet has video — a filtered-empty result is a
	// literal [] and still a success.
	code, out, _ = runCLI(t, "user", "NASA", "--media-type", "video", "--json")
	if code != 0 {
		t.Fatalf("--media-type video: exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("--media-type video: output = %q, want []", out)
	}

	// --media-type bogus: usage error naming the flag, before any network.
	bad := newFake(t, map[string]answer{})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+bad.addr+"\"\n")
	code, _, errOut = runCLI(t, "user", "NASA", "--media-type", "bogus")
	if code != 2 {
		t.Fatalf("--media-type bogus: exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--media-type") {
		t.Errorf("stderr = %q, want it to name --media-type", errOut)
	}
	if got := bad.rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (validation precedes any network)", got)
	}
}

func TestUserWithRepliesFlag(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "user", "NASA", "--with-replies", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "101") {
		t.Errorf("output = %q, want tweet 101", out)
	}
}

// TestUserLimitZeroUnderFxBackend was removed with the "0 = unlimited" limit
// semantics: --limit 0 is now a usage error on every backend (pinned by
// TestUserRejectsNonPositiveCaps above).
