package watch_test

import (
	"encoding/json"
	"errors"
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
	"github.com/shitianyaa/nitter-cli/internal/storage/seen"
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

// runCLIStdout runs the CLI with a custom stdout writer (for output-failure
// paths the always-succeeding strings.Builder harness cannot express) and
// returns the exit code and stderr.
func runCLIStdout(t *testing.T, stdout io.Writer, args ...string) (int, string) {
	t.Helper()
	var errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), stdout, &errOut)
	return code, errOut.String()
}

// failingWriter fails every write with a fixed non-EPIPE error.
type failingWriter struct{ err error }

func (w *failingWriter) Write(p []byte) (int, error) { return 0, w.err }

// fakeNitter serves canned answers keyed by the exact request URI
// (path?query). The answers are swappable between watch runs: a second cycle
// against the same server can serve NEW tweets, which is how the dedup tests
// simulate time passing.
type fakeNitter struct {
	mu      sync.Mutex
	answers map[string]answer
	headers map[string]map[string]string
	addr    string
	srv     *httptest.Server
}

type answer struct {
	status int
	body   string
}

func newFake(t *testing.T, answers map[string]answer) *fakeNitter {
	t.Helper()
	f := &fakeNitter{answers: answers, headers: make(map[string]map[string]string)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		f.mu.Lock()
		a, ok := f.answers[req.URL.RequestURI()]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		hdrs := f.headers[req.URL.RequestURI()]
		f.mu.Unlock()
		for name, value := range hdrs {
			w.Header().Set(name, value)
		}
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.addr = f.srv.URL
	return f
}

// setAnswers swaps the canned answers for the next run.
func (f *fakeNitter) setAnswers(answers map[string]answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = answers
}

// setHeaders makes the fake attach these response headers to one request
// target (the RSS paging tests need Min-Id). It is additive on purpose: the
// answer literals stay positional.
func (f *fakeNitter) setHeaders(target string, headers map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.headers == nil {
		f.headers = make(map[string]map[string]string)
	}
	f.headers[target] = headers
}

// instanceConfig returns the fast config fixture pointing at fake.
func instanceConfig(fake *fakeNitter) string {
	return fastTOML + "[[instances]]\nurl = \"" + fake.addr + "\"\n"
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
// and carrying a load-more cursor when non-empty. An empty ids slice plus an
// empty cursor is the empty-fetch fixture (both layers yield zero tweets).
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

// searchPage builds a Nitter-shaped search result page (the markup tag
// searches share with user timelines and lists).
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

// listPage builds a Nitter-shaped list page.
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

// envelope is the decoded shape of one nitter.pipeline/v1 line: tweet and
// error envelopes share the struct (missing fields stay zero).
type envelope struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Data   struct {
		// Tweet payloads.
		ID   string `json:"id"`
		Text string `json:"text"`
		// TypedErrorEnvelope payloads.
		Command string `json:"command"`
		Stage   string `json:"stage"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"data"`
	Meta *struct {
		Source    string `json:"source"`
		Instance  string `json:"instance"`
		FetchedAt string `json:"fetched_at"`
		Input     string `json:"input"`
	} `json:"meta"`
}

func decodeEnvelopes(t *testing.T, out string) []envelope {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	envs := make([]envelope, 0, len(lines))
	for i, line := range lines {
		var env envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i+1, err, line)
		}
		envs = append(envs, env)
	}
	return envs
}

func readSeenFile(t *testing.T, path string) seen.File {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var file seen.File
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return file
}

// TestWatchFirstOnceRecordsWithoutEmitting: the first --once run only
// records state (只记不推) — stdout stays empty and seen.json carries the
// initialized source with the whole first fetch as seen.
func TestWatchFirstOnceRecordsWithoutEmitting(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want no envelopes on the record-only first run", out)
	}

	seenPath := filepath.Join(stateDir, "seen.json")
	file := readSeenFile(t, seenPath)
	st, ok := file.Sources["user:NASA"]
	if !ok || !st.Initialized {
		t.Fatalf("seen.json sources[user:NASA] = %+v ok=%v, want an initialized entry", st, ok)
	}
	if !slices.Equal(st.SeenIDs, []string{"101", "102"}) {
		t.Errorf("seen_ids = %v, want [101 102]", st.SeenIDs)
	}
}

// TestWatchSecondOnceEmitsNewTweetsThirdDedups: run 2 against a fixture with
// a new tweet emits exactly the new tweet; run 3 against the same fixture
// emits nothing (dedup).
func TestWatchSecondOnceEmitsNewTweetsThirdDedups(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	args := []string{"watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir}

	if code, _, errOut := runCLI(t, args...); code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}

	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody("103", "102", "101")},
	})
	code, out, errOut := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("run 2: %d envelopes, want exactly the new tweet:\n%s", len(envs), out)
	}
	env := envs[0]
	if env.Schema != "nitter.pipeline/v1" || env.Kind != "tweet" || env.ID != "103" || env.Data.ID != "103" {
		t.Errorf("run 2 envelope = %+v, want kind tweet / id 103", env)
	}
	if env.Meta == nil || env.Meta.Source != "user:NASA" {
		t.Errorf("run 2 meta.source = %+v, want user:NASA", env.Meta)
	}
	if env.Meta != nil && env.Meta.Instance != fake.addr {
		t.Errorf("run 2 meta.instance = %q, want the serving instance", env.Meta.Instance)
	}
	if env.Meta != nil && !strings.HasSuffix(env.Meta.FetchedAt, "Z") {
		t.Errorf("run 2 meta.fetched_at = %q, want RFC3339 UTC", env.Meta.FetchedAt)
	}

	// Run 3 against the same fixture: everything is seen, nothing emitted.
	code, out, _ = runCLI(t, args...)
	if code != 0 {
		t.Fatalf("run 3: exit = %d, want 0", code)
	}
	if out != "" {
		t.Errorf("run 3 stdout = %q, want nothing (dedup)", out)
	}
}

// TestWatchIncludeExistingEmitsFirstFetch: --include-existing lifts the
// record-only first run and emits the whole first fetch.
func TestWatchIncludeExistingEmitsFirstFetch(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:NASA", "--once", "--ndjson",
		"--include-existing", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 2 || envs[0].ID != "101" || envs[1].ID != "102" {
		t.Fatalf("envelopes = %+v, want ids 101 then 102", envs)
	}
	for _, env := range envs {
		if env.Kind != "tweet" {
			t.Errorf("envelope %s kind = %q, want tweet", env.ID, env.Kind)
		}
	}
}

// TestWatchFailedSourceIsIsolated: a source whose instance 500s gets an
// in-place error envelope (stage fetch, code = the SDK error kind the fetch
// failed with — 500 classifies as upstream_unavailable) while the healthy
// source still emits; the run exits 1 and the failed source's state is
// untouched (no entry in seen.json at all).
func TestWatchFailedSourceIsIsolated(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/Broken/rss": {500, "boom"},
		"/Broken":     {500, "down"},
		"/NASA/rss":   {200, rssBody("201", "202")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:Broken", "user:NASA", "--once", "--ndjson",
		"--include-existing", "--state-dir", stateDir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 3 {
		t.Fatalf("%d envelopes, want error(Broken) + tweets(201,202):\n%s", len(envs), out)
	}
	errEnv, tweet1, tweet2 := envs[0], envs[1], envs[2]
	if errEnv.Kind != "error" || errEnv.Data.Command != "watch" || errEnv.Data.Stage != "fetch" ||
		errEnv.Data.Code != "upstream_unavailable" || errEnv.Data.Message == "" {
		t.Errorf("error envelope = %+v, want command watch / stage fetch / code upstream_unavailable / a message", errEnv)
	}
	if errEnv.Meta == nil || errEnv.Meta.Input != "user:Broken" {
		t.Errorf("error envelope meta.input = %+v, want user:Broken", errEnv.Meta)
	}
	if tweet1.Kind != "tweet" || tweet1.ID != "201" || tweet2.ID != "202" {
		t.Errorf("healthy-source envelopes = %s / %s, want tweets 201 and 202", tweet1.ID, tweet2.ID)
	}

	file := readSeenFile(t, filepath.Join(stateDir, "seen.json"))
	if _, ok := file.Sources["user:Broken"]; ok {
		t.Errorf("seen.json contains user:Broken, want the failed source's state untouched")
	}
	if st, ok := file.Sources["user:NASA"]; !ok || !st.Initialized {
		t.Errorf("seen.json sources[user:NASA] = %+v ok=%v, want initialized", st, ok)
	}
}

// TestWatchFailedSourceHumanMode: in the default mode a source failure is a
// plain stderr line and new tweets are TweetRows on stdout.
func TestWatchFailedSourceHumanMode(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/Broken/rss": {500, "boom"},
		"/Broken":     {500, "down"},
		"/NASA/rss":   {200, rssBody("201")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:Broken", "user:NASA", "--once",
		"--include-existing", "--state-dir", stateDir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "201\t2026-07-05 09:09\t@NASA\trss body 201") {
		t.Errorf("stdout = %q, want the TweetRow for 201", out)
	}
	if !strings.Contains(errOut, "error: user:Broken:") {
		t.Errorf("stderr = %q, want the per-source error line", errOut)
	}
	if strings.Contains(out, "\"kind\"") {
		t.Errorf("stdout = %q, want no envelopes in human mode", out)
	}
}

// TestWatchMaxNewOneCapsEmissionButSeenAdvancesWithAll: --max-new 1 emits
// only the newest new tweet while seen still records every new id (rule 4 —
// the excess is marked seen and never re-emitted).
func TestWatchMaxNewOneCapsEmissionButSeenAdvancesWithAll(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	base := []string{"watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir}

	if code, _, errOut := runCLI(t, base...); code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}

	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody("103", "104", "105")},
	})
	code, out, errOut := runCLI(t, append(base, "--max-new", "1")...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 1 || envs[0].ID != "103" {
		t.Fatalf("run 2 envelopes = %+v, want exactly the newest new tweet 103", envs)
	}

	st := readSeenFile(t, filepath.Join(stateDir, "seen.json")).Sources["user:NASA"]
	for _, id := range []string{"101", "102", "103", "104", "105"} {
		if !slices.Contains(st.SeenIDs, id) {
			t.Errorf("seen_ids missing %q (rule 4: seen advances with ALL new ids): %v", id, st.SeenIDs)
		}
	}
}

// TestWatchMaxNewZeroRebuildsBaseline: --max-new 0 emits nothing and seals
// the current first page into seen + watermark (rule 5).
func TestWatchMaxNewZeroRebuildsBaseline(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	base := []string{"watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir}

	if code, _, errOut := runCLI(t, base...); code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}

	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody("103", "104")},
	})
	code, out, errOut := runCLI(t, append(base, "--max-new", "0")...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("run 2 stdout = %q, want nothing (max-new 0 emits nothing)", out)
	}
	st := readSeenFile(t, filepath.Join(stateDir, "seen.json")).Sources["user:NASA"]
	if !slices.Contains(st.SeenIDs, "103") || !slices.Contains(st.SeenIDs, "104") {
		t.Errorf("seen_ids = %v, want the baseline 103/104 merged in (rule 5)", st.SeenIDs)
	}
	if !slices.Equal(st.WatermarkIDs, []string{"103", "104"}) {
		t.Errorf("watermark_ids = %v, want [103 104] (baseline rebuilt)", st.WatermarkIDs)
	}
}

// TestWatchMaxNewOverflowKeepReEmitsBacklogNextCycles: --max-new-overflow
// keep does NOT mark the tweets beyond the cap seen — the following cycles
// re-emit them (newest first) until the queue drains, without repeating
// already-delivered tweets (the 宁重勿丢 counterpart of rule 4).
func TestWatchMaxNewOverflowKeepReEmitsBacklogNextCycles(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	base := []string{"watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir}

	if code, _, errOut := runCLI(t, base...); code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}

	// Burst of three new tweets (105 newest) against --max-new 1 keep.
	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody("105", "104", "103", "102", "101")},
	})
	keep := append(slices.Clone(base), "--max-new", "1", "--max-new-overflow", "keep")

	code, out, errOut := runCLI(t, keep...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 1 || envs[0].ID != "105" {
		t.Fatalf("run 2 envelopes = %+v, want exactly the newest new tweet 105", envs)
	}
	st := readSeenFile(t, filepath.Join(stateDir, "seen.json")).Sources["user:NASA"]
	if !slices.Contains(st.SeenIDs, "105") {
		t.Errorf("run 2 seen_ids = %v, want the emitted 105 recorded", st.SeenIDs)
	}
	if slices.Contains(st.SeenIDs, "103") || slices.Contains(st.SeenIDs, "104") {
		t.Errorf("run 2 seen_ids = %v, want the kept overflow 103/104 NOT recorded", st.SeenIDs)
	}

	// Cycle 3 re-emits the kept backlog's newest tweet; nothing repeats.
	code, out, _ = runCLI(t, keep...)
	if code != 0 {
		t.Fatalf("run 3: exit = %d, want 0", code)
	}
	envs = decodeEnvelopes(t, out)
	if len(envs) != 1 || envs[0].ID != "104" {
		t.Fatalf("run 3 envelopes = %+v, want exactly the kept 104 (no duplicates)", envs)
	}

	// Cycle 4 drains the last backlog tweet.
	code, out, _ = runCLI(t, keep...)
	if code != 0 {
		t.Fatalf("run 4: exit = %d, want 0", code)
	}
	envs = decodeEnvelopes(t, out)
	if len(envs) != 1 || envs[0].ID != "103" {
		t.Fatalf("run 4 envelopes = %+v, want exactly the kept 103", envs)
	}

	// Cycle 5: queue drained, dedup back to silence.
	code, out, _ = runCLI(t, keep...)
	if code != 0 {
		t.Fatalf("run 5: exit = %d, want 0", code)
	}
	if out != "" {
		t.Errorf("run 5 stdout = %q, want nothing (queue drained)", out)
	}
}

// TestWatchMaxNewOverflowDropMarksExcessSeen: the explicit default `drop`
// keeps today's behavior byte-identical — the tweets beyond the cap are
// marked seen immediately and never re-emitted.
func TestWatchMaxNewOverflowDropMarksExcessSeen(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	base := []string{"watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir}

	if code, _, errOut := runCLI(t, base...); code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}

	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody("105", "104", "103", "102", "101")},
	})
	drop := append(slices.Clone(base), "--max-new", "1", "--max-new-overflow", "drop")

	code, out, errOut := runCLI(t, drop...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 1 || envs[0].ID != "105" {
		t.Fatalf("run 2 envelopes = %+v, want exactly the newest new tweet 105", envs)
	}
	st := readSeenFile(t, filepath.Join(stateDir, "seen.json")).Sources["user:NASA"]
	for _, id := range []string{"103", "104", "105"} {
		if !slices.Contains(st.SeenIDs, id) {
			t.Errorf("run 2 seen_ids missing %q (drop: the excess is sealed at discovery): %v", id, st.SeenIDs)
		}
	}

	// Run 3 against the same fixture: the excess is already seen, nothing
	// re-emits.
	code, out, _ = runCLI(t, drop...)
	if code != 0 {
		t.Fatalf("run 3: exit = %d, want 0", code)
	}
	if out != "" {
		t.Errorf("run 3 stdout = %q, want nothing (drop never re-emits)", out)
	}
}

// TestWatchMaxNewOverflowKeepWithZeroMaxNewStillSealsBaseline pins the
// documented interaction: the overflow policy only governs capped emission
// rounds — --max-new 0 is the baseline rebuild and seals the current first
// page under both policies.
func TestWatchMaxNewOverflowKeepWithZeroMaxNewStillSealsBaseline(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	base := []string{"watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir}

	if code, _, errOut := runCLI(t, base...); code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}

	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody("103", "104")},
	})
	code, out, errOut := runCLI(t, append(slices.Clone(base),
		"--max-new", "0", "--max-new-overflow", "keep")...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("run 2 stdout = %q, want nothing (max-new 0 emits nothing under keep too)", out)
	}
	st := readSeenFile(t, filepath.Join(stateDir, "seen.json")).Sources["user:NASA"]
	if !slices.Contains(st.SeenIDs, "103") || !slices.Contains(st.SeenIDs, "104") {
		t.Errorf("run 2 seen_ids = %v, want the baseline 103/104 sealed in (keep must not disable rule 5)", st.SeenIDs)
	}
	if !slices.Equal(st.WatermarkIDs, []string{"103", "104"}) {
		t.Errorf("run 2 watermark_ids = %v, want [103 104] (baseline rebuilt)", st.WatermarkIDs)
	}

	// Run 3: the sealed baseline stays deduped.
	code, out, _ = runCLI(t, append(slices.Clone(base),
		"--max-new", "0", "--max-new-overflow", "keep")...)
	if code != 0 {
		t.Fatalf("run 3: exit = %d, want 0", code)
	}
	if out != "" {
		t.Errorf("run 3 stdout = %q, want nothing (baseline sealed)", out)
	}
}

// TestWatchMaxNewOverflowInvalidValueIsUsageError: only drop|keep are
// accepted; anything else exits 2 naming the flag.
func TestWatchMaxNewOverflowInvalidValueIsUsageError(t *testing.T) {
	tempHome(t)
	for _, value := range []string{"bogus", "Keep", "keep ", ""} {
		code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--max-new-overflow", value)
		if code != 2 {
			t.Errorf("--max-new-overflow %q: exit = %d, want 2 (stderr %q)", value, code, errOut)
		}
		if !strings.Contains(errOut, "--max-new-overflow") {
			t.Errorf("--max-new-overflow %q: stderr = %q, want it to name the flag", value, errOut)
		}
	}
}

// TestWatchTagAndListSources: the kind dispatches tag to Search and list to
// ListTimeline, with meta.source carrying the source key.
func TestWatchTagAndListSources(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=%23AI": {200, searchPage([]string{"301"}, "")},
		"/i/lists/12345":           {200, listPage([]string{"401"}, "")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "tag:#AI", "list:12345", "--once", "--ndjson",
		"--include-existing", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 2 || envs[0].ID != "301" || envs[1].ID != "401" {
		t.Fatalf("envelopes = %+v, want 301 (tag) then 401 (list)", envs)
	}
	if envs[0].Meta == nil || envs[0].Meta.Source != "tag:#AI" {
		t.Errorf("tag meta.source = %+v, want tag:#AI", envs[0].Meta)
	}
	if envs[1].Meta == nil || envs[1].Meta.Source != "list:12345" {
		t.Errorf("list meta.source = %+v, want list:12345", envs[1].Meta)
	}
}

// TestWatchInvalidSourceIsUsageError: malformed source strings exit 2 before
// any fetch.
func TestWatchInvalidSourceIsUsageError(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, instanceConfig(fake))
	for _, source := range []string{"NASA", "bogus:NASA", "user:", ":NASA"} {
		code, _, errOut := runCLI(t, "watch", source, "--once", "--state-dir", t.TempDir())
		if code != 2 {
			t.Errorf("watch %q: exit = %d, want 2 (stderr %q)", source, code, errOut)
		}
	}
}

// TestWatchIntervalBelowOneSecondIsUsageError.
func TestWatchIntervalBelowOneSecondIsUsageError(t *testing.T) {
	tempHome(t)
	for _, interval := range []string{"500ms", "nonsense"} {
		code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--interval", interval)
		if code != 2 {
			t.Errorf("--interval %s: exit = %d, want 2 (stderr %q)", interval, code, errOut)
		}
		if !strings.Contains(errOut, "--interval") {
			t.Errorf("--interval %s: stderr = %q, want it to name the flag", interval, errOut)
		}
	}
}

// TestWatchNegativeMaxNewIsUsageError.
func TestWatchNegativeMaxNewIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--max-new=-1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--max-new") {
		t.Errorf("stderr = %q, want it to name the flag", errOut)
	}
}

// TestWatchNonPositiveMaxPagesIsUsageError: --max-pages is a cap, so 0 (which
// used to slip through as "one page") and negatives are usage errors.
func TestWatchNonPositiveMaxPagesIsUsageError(t *testing.T) {
	tempHome(t)
	for _, flag := range []string{"--max-pages=0", "--max-pages=-1"} {
		code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", flag)
		if code != 2 {
			t.Fatalf("%s: exit = %d, want 2 (stderr %q)", flag, code, errOut)
		}
		if !strings.Contains(errOut, "--max-pages") {
			t.Errorf("%s: stderr = %q, want it to name the flag", flag, errOut)
		}
	}
}

// TestWatchJSONRequiresOnce: the resident loop is a stream of cycles, not
// one JSON document — --json without --once is a usage error naming the
// --once requirement.
func TestWatchJSONRequiresOnce(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "watch", "user:NASA", "--json")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--once") {
		t.Errorf("stderr = %q, want it to name the --once requirement", errOut)
	}
}

// TestWatchJSONAndNDJSONAreMutuallyExclusive: even in once mode the two
// flags do not combine (repo-wide flag contract).
func TestWatchJSONAndNDJSONAreMutuallyExclusive(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--json") || !strings.Contains(errOut, "--ndjson") {
		t.Errorf("stderr = %q, want it to name both flags", errOut)
	}
}

// TestWatchOnceJSONDocumentShape: `watch --once --json` prints ONE JSON
// document {"tweets":[...],"errors":[...]} — the cycle's selected tweets as
// bare Tweet objects and the per-source failures as {ref, code, message}
// entries. The record-only first run yields both arrays empty.
func TestWatchOnceJSONDocumentShape(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	args := []string{"watch", "user:NASA", "--once", "--json", "--state-dir", stateDir}

	code, out, errOut := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("run 1: output is not one JSON document: %v\n%s", err, out)
	}
	if len(doc) != 2 {
		t.Errorf("run 1: document keys = %v, want exactly tweets and errors", doc)
	}
	var tweets []map[string]any
	if err := json.Unmarshal(doc["tweets"], &tweets); err != nil || tweets == nil || len(tweets) != 0 {
		t.Errorf("run 1: tweets = %q (%v), want a literal empty array", doc["tweets"], err)
	}
	var errs []map[string]any
	if err := json.Unmarshal(doc["errors"], &errs); err != nil || errs == nil || len(errs) != 0 {
		t.Errorf("run 1: errors = %q (%v), want a literal empty array", doc["errors"], err)
	}

	// Run 2: one new tweet — the document carries it as a bare Tweet object
	// (no envelope wrapper: no schema/kind keys).
	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody("103", "102", "101")},
	})
	code, out, errOut = runCLI(t, args...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	doc = nil
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("run 2: output is not one JSON document: %v\n%s", err, out)
	}
	if err := json.Unmarshal(doc["tweets"], &tweets); err != nil {
		t.Fatalf("run 2: decode tweets: %v\n%s", err, out)
	}
	if len(tweets) != 1 || tweets[0]["id"] != "103" || tweets[0]["text"] != "rss body 103" {
		t.Errorf("run 2: tweets = %v, want exactly the bare new tweet 103", tweets)
	}
	if _, ok := tweets[0]["schema"]; ok {
		t.Errorf("run 2: tweets[0] carries a schema key — envelopes, not bare Tweet objects: %v", tweets[0])
	}
	if err := json.Unmarshal(doc["errors"], &errs); err != nil || len(errs) != 0 {
		t.Errorf("run 2: errors = %q (%v), want an empty array", doc["errors"], err)
	}
	if strings.Contains(out, "nitter.pipeline/v1") {
		t.Errorf("run 2: stdout = %q, want one plain document (no envelopes)", out)
	}
}

// TestWatchOnceJSONReportsSourceErrors: a failed source becomes an errors
// entry {ref, code, message} in the document while the healthy sources'
// tweets still arrive; the run exits 1 (the failure summary is unchanged).
func TestWatchOnceJSONReportsSourceErrors(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/Broken/rss": {500, "boom"},
		"/Broken":     {500, "down"},
		"/NASA/rss":   {200, rssBody("201", "202")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:Broken", "user:NASA", "--once", "--json",
		"--include-existing", "--state-dir", stateDir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	var doc struct {
		Tweets []map[string]any `json:"tweets"`
		Errors []struct {
			Ref     string `json:"ref"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not one JSON document: %v\n%s", err, out)
	}
	if len(doc.Tweets) != 2 || doc.Tweets[0]["id"] != "201" || doc.Tweets[1]["id"] != "202" {
		t.Errorf("tweets = %v, want the healthy source's 201 and 202", doc.Tweets)
	}
	if len(doc.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly the failed source", doc.Errors)
	}
	if doc.Errors[0].Ref != "user:Broken" || doc.Errors[0].Code != "upstream_unavailable" || doc.Errors[0].Message == "" {
		t.Errorf("error entry = %+v, want ref user:Broken / code upstream_unavailable / a message", doc.Errors[0])
	}
}

// TestWatchOnceJSONPersistsOnlyAfterTheDocumentLands pins the corrected
// produce-then-persist ordering in --once --json mode: the per-source state
// writes are buffered during the cycle and applied only after the document
// write succeeded. A failing document write therefore leaves the state
// wholly unadvanced (the next round re-pushes the cycle, 宁重勿丢), while a
// successful run advances it.
func TestWatchOnceJSONPersistsOnlyAfterTheDocumentLands(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	seenPath := filepath.Join(stateDir, "seen.json")
	args := []string{"watch", "user:NASA", "--once", "--json", "--include-existing", "--state-dir", stateDir}

	// The document write fails (a non-EPIPE stdout failure exits 1): the
	// tweets were NOT delivered, so the state must stay untouched — the next
	// round re-pushes them.
	code, errOut := runCLIStdout(t, &failingWriter{err: errors.New("stdout write failed")}, args...)
	if code != 1 {
		t.Fatalf("failing document write: exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "stdout write failed") {
		t.Errorf("failing document write: stderr = %q, want the write failure reported", errOut)
	}
	if _, err := os.Stat(seenPath); !os.IsNotExist(err) {
		t.Errorf("failing document write advanced the state (stat err = %v), want the store untouched", err)
	}

	// Control: the same run with a working stdout lands the document AND
	// then advances the state.
	code, out, errOut := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("control: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var doc struct {
		Tweets []map[string]any `json:"tweets"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("control: output is not one JSON document: %v\n%s", err, out)
	}
	if len(doc.Tweets) != 2 || doc.Tweets[0]["id"] != "101" || doc.Tweets[1]["id"] != "102" {
		t.Errorf("control: tweets = %v, want the whole first fetch (include-existing)", doc.Tweets)
	}
	st := readSeenFile(t, seenPath).Sources["user:NASA"]
	if !st.Initialized || !slices.Contains(st.SeenIDs, "101") || !slices.Contains(st.SeenIDs, "102") {
		t.Errorf("control: seen state = %+v, want initialized with ids 101 and 102 (state advanced after the document landed)", st)
	}
}

// TestWatchNoSourcesAnywhereIsUsageError: no argv sources and no
// [[watch.sources]] in the config.
func TestWatchNoSourcesAnywhereIsUsageError(t *testing.T) {
	tempHome(t) // baseline config: zero instances, zero watch sources
	code, _, errOut := runCLI(t, "watch", "--once")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "source") {
		t.Errorf("stderr = %q, want it to name the missing sources", errOut)
	}
}

// TestWatchConfigSourcesFallback: argv sources fall back to the config's
// [[watch.sources]] entries.
func TestWatchConfigSourcesFallback(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})
	writeConfig(t, home, instanceConfig(fake)+"\n[[watch.sources]]\nid = \"user:NASA\"\n")

	// No --state-dir: the seen file lands in the default state dir under
	// the (temp) home — the default layout check rides along here.
	code, out, errOut := runCLI(t, "watch", "--once", "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want no envelopes on the record-only first run", out)
	}
	seenPath := filepath.Join(home, ".nitter-cli", "state", "seen.json")
	if _, ok := readSeenFile(t, seenPath).Sources["user:NASA"]; !ok {
		t.Errorf("%s has no user:NASA entry", seenPath)
	}
}

// TestWatchConfigInvalidSourceNamesEntry: an invalid [[watch.sources]] entry
// is a usage error naming the offending entry.
func TestWatchConfigInvalidSourceNamesEntry(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, instanceConfig(fake)+"\n[[watch.sources]]\nid = \"garbage\"\n")

	code, _, errOut := runCLI(t, "watch", "--once")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "garbage") {
		t.Errorf("stderr = %q, want it to name the invalid entry", errOut)
	}
}

// TestWatchEmptyFetchDoesNotRewriteSeenFile: an initialized source whose
// fetch comes back empty keeps its state wholesale — the persist-skip means
// seen.json is byte-identical after the empty round (no UpdatedAt churn).
func TestWatchEmptyFetchDoesNotRewriteSeenFile(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()
	seenPath := filepath.Join(stateDir, "seen.json")
	args := []string{"watch", "user:NASA", "--once", "--ndjson", "--state-dir", stateDir}

	if code, _, errOut := runCLI(t, args...); code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	before, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen.json after run 1: %v", err)
	}

	// Empty fetch: RSS answers with an empty feed AND the HTML page has no
	// tweets (both layers yield zero).
	fake.setAnswers(map[string]answer{
		"/NASA/rss": {200, rssBody()},
		"/NASA":     {200, htmlPage(nil, "")},
	})
	code, out, errOut := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("run 2 stdout = %q, want nothing", out)
	}
	after, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen.json after run 2: %v", err)
	}
	if !slices.Equal(before, after) {
		t.Errorf("empty-fetch round rewrote seen.json:\nbefore %s\nafter  %s", before, after)
	}
}

// htmlPageWithRetweet builds a Nitter-shaped user timeline with one pure
// retweet (stock anchor-less retweet-header markup, status 555) followed by
// one normal tweet (201) — the --no-reposts fixture. Retweet headers only
// exist on the HTML path, so the fetch must be driven there (the RSS answer
// 500s; the fetch falls back to the HTML user page).
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

// TestWatchNoRepostsFiltersBeforeDedup pins the filter placement ruling:
// field filters run after the fetch but BEFORE selection/dedup, so filtered
// tweets are never recorded as seen — each cycle re-fetches (and re-filters)
// them without ever emitting them, and the seen state never grows.
func TestWatchNoRepostsFiltersBeforeDedup(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {500, "boom"},
		"/NASA":     {200, htmlPageWithRetweet()},
	})
	writeConfig(t, home, instanceConfig(fake))

	// Control (own state dir): without the filter the fixture emits the
	// retweet 555 first — proving the fixture really carries one.
	controlDir := t.TempDir()
	code, out, errOut := runCLI(t, "watch", "user:NASA", "--once", "--ndjson",
		"--include-existing", "--state-dir", controlDir)
	if code != 0 {
		t.Fatalf("control: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 2 || envs[0].ID != "555" || envs[1].ID != "201" {
		t.Fatalf("control: envelopes = %+v, want the retweet 555 then 201", envs)
	}
	controlSeen := readSeenFile(t, filepath.Join(controlDir, "seen.json")).Sources["user:NASA"]
	if !slices.Contains(controlSeen.SeenIDs, "555") {
		t.Errorf("control: seen_ids = %v, want the unfiltered 555 recorded", controlSeen.SeenIDs)
	}

	// Filtered first cycle: the record-only run seeds ONLY the
	// filtered-through tweet; the repost id never enters seen_ids and the
	// watermark anchors the filtered first page.
	stateDir := t.TempDir()
	seenPath := filepath.Join(stateDir, "seen.json")
	args := []string{"watch", "user:NASA", "--once", "--ndjson", "--no-reposts", "--state-dir", stateDir}

	code, out, errOut = runCLI(t, args...)
	if code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("run 1: stdout = %q, want nothing (record-only first run)", out)
	}
	st := readSeenFile(t, seenPath).Sources["user:NASA"]
	if slices.Contains(st.SeenIDs, "555") {
		t.Errorf("run 1: seen_ids = %v, want the filtered-out repost NOT recorded", st.SeenIDs)
	}
	if !slices.Equal(st.SeenIDs, []string{"201"}) {
		t.Errorf("run 1: seen_ids = %v, want [201] (only the filtered-through tweet)", st.SeenIDs)
	}
	if !slices.Equal(st.WatermarkIDs, []string{"201"}) {
		t.Errorf("run 1: watermark_ids = %v, want [201] (filtered first page)", st.WatermarkIDs)
	}
	before, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen.json after run 1: %v", err)
	}

	// Next cycle against the same fixture: the repost is re-fetched and
	// re-filtered — nothing emitted, no dupes, no state growth.
	code, out, errOut = runCLI(t, args...)
	if code != 0 {
		t.Fatalf("run 2: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("run 2: stdout = %q, want nothing (the repost is filtered, 201 is seen)", out)
	}
	after, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen.json after run 2: %v", err)
	}
	if !slices.Equal(before, after) {
		t.Errorf("run 2 rewrote seen.json (state grew):\nbefore %s\nafter  %s", before, after)
	}
}

// TestWatchInvalidMediaTypeIsUsageError: a --media-type value outside
// image|video|gif exits 2 before any fetch.
func TestWatchInvalidMediaTypeIsUsageError(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, instanceConfig(fake))

	code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--media-type", "bogus",
		"--state-dir", t.TempDir())
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--media-type") {
		t.Errorf("stderr = %q, want it to name --media-type", errOut)
	}
}

// TestWatchRSSPagesPastTheFirstFeedPage: a source whose backlog spans more
// than one RSS page is fully fetched, so nothing past the first page is
// silently dropped.
func TestWatchRSSPagesPastTheFirstFeedPage(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss":            {200, rssBody("103", "102")},
		"/NASA/rss?cursor=102": {200, rssBody("101")},
	})
	fake.setHeaders("/NASA/rss", map[string]string{"Min-Id": "102"})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:NASA", "--once", "--ndjson",
		"--include-existing", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	var ids []string
	for _, env := range envs {
		ids = append(ids, env.ID)
	}
	if want := []string{"103", "102", "101"}; !slices.Equal(ids, want) {
		t.Fatalf("emitted ids = %v, want %v (the second RSS page must be fetched)", ids, want)
	}
}

// rssItemFor renders one Nitter-shaped RSS item for one handle.
func rssItemFor(handle, id string) string {
	return `<item>` +
		`<guid>https://nitter.example/` + handle + `/status/` + id + `#m</guid>` +
		`<link>https://nitter.example/` + handle + `/status/` + id + `</link>` +
		`<dc:creator>@` + handle + `</dc:creator>` +
		`<title>rss ` + id + `</title>` +
		`<description><![CDATA[<div class="tweet-content">rss body ` + id + `</div>]]></description>` +
		`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate></item>`
}

// rssFeedOf wraps explicit items into one feed.
func rssFeedOf(items ...string) string {
	return `<?xml version="1.0"?><rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel>` +
		strings.Join(items, "") + `</channel></rss>`
}

// TestWatchMergesUserSourcesWithNoReposts: with --no-reposts and two user
// sources the cycle issues ONE merged request instead of two.
func TestWatchMergesUserSourcesWithNoReposts(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA,ESA/rss": {200, rssFeedOf(rssItemFor("NASA", "101"), rssItemFor("ESA", "201"))},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:NASA", "user:ESA", "--once", "--ndjson",
		"--no-reposts", "--include-existing", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 2 {
		t.Fatalf("envelopes = %d, want 2 (one per author): %s", len(envs), out)
	}
	file := readSeenFile(t, filepath.Join(stateDir, "seen.json"))
	for _, key := range []string{"user:NASA", "user:ESA"} {
		if _, ok := file.Sources[key]; !ok {
			t.Errorf("seen.json missing %q: %+v", key, file.Sources)
		}
	}
}

// TestWatchDoesNotMergeWithoutNoReposts: the default (reposts kept) stays on
// the per-source path, because the merged feed cannot represent a repost.
func TestWatchDoesNotMergeWithoutNoReposts(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
		"/ESA/rss":  {200, rssBody("201")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:NASA", "user:ESA", "--once", "--ndjson",
		"--include-existing", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if envs := decodeEnvelopes(t, out); len(envs) != 2 {
		t.Fatalf("envelopes = %d, want 2 from the per-source path", len(envs))
	}
}

// TestWatchMergedBatchFailureFallsBackPerSource: a failed merged request must
// not lose the sources — the per-source path covers them.
//
// ESA's fallback answer is author-correct (rssBody hardcodes NASA in guid and
// dc:creator, which the RSS repost heuristic would flag — and --no-reposts
// would then drop — under the ESA source).
func TestWatchMergedBatchFailureFallsBackPerSource(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA,ESA/rss": {404, ""},
		"/NASA/rss":     {200, rssBody("101")},
		"/ESA/rss":      {200, rssFeedOf(rssItemFor("ESA", "201"))},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	code, out, errOut := runCLI(t, "watch", "user:NASA", "user:ESA", "--once", "--ndjson",
		"--no-reposts", "--include-existing", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if envs := decodeEnvelopes(t, out); len(envs) != 2 {
		t.Fatalf("envelopes = %d, want 2 (fallback must cover both sources)", len(envs))
	}
}

// TestWatchMergedDoesNotEarlyStopForAnUninitializedMember: page 1 holding only
// an initialized member's already-seen tweets must not cut off a new member's
// baseline — the scan has to reach the page that carries it.
func TestWatchMergedDoesNotEarlyStopForAnUninitializedMember(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})
	writeConfig(t, home, instanceConfig(fake))
	stateDir := t.TempDir()

	// Run 1 initializes NASA only.
	if code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--state-dir", stateDir); code != 0 {
		t.Fatalf("seed run exit = %d, want 0 (stderr %q)", code, errOut)
	}

	// Run 2 adds ESA. Page 1 is entirely NASA's already-seen tweet; ESA's only
	// tweet sits on page 2.
	fake.setAnswers(map[string]answer{
		"/NASA,ESA/rss":           {200, rssFeedOf(rssItemFor("NASA", "101"))},
		"/NASA,ESA/rss?cursor=c1": {200, rssFeedOf(rssItemFor("ESA", "201"))},
	})
	fake.setHeaders("/NASA,ESA/rss", map[string]string{"Min-Id": "c1"})

	code, out, errOut := runCLI(t, "watch", "user:NASA", "user:ESA", "--once", "--ndjson",
		"--no-reposts", "--include-existing", "--state-dir", stateDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := decodeEnvelopes(t, out)
	if len(envs) != 1 || envs[0].ID != "201" {
		t.Fatalf("envelopes = %+v, want exactly ESA's 201 (an early stop would have truncated it)", envs)
	}
}
