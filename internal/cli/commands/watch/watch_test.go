package watch_test

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

	"github.com/shitianyaa/twitter-cli/internal/cli"
	"github.com/shitianyaa/twitter-cli/internal/storage/seen"
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
		"TWITTER_DEFAULT_LIMIT", "TWITTER_LOG_LEVEL", "TWITTER_LOG_FORMAT",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, "")
	}
	return home
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".twitter-cli")
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
// (path?query). The answers are swappable between watch runs: a second cycle
// against the same server can serve NEW tweets, which is how the dedup tests
// simulate time passing.
type fakeNitter struct {
	mu      sync.Mutex
	answers map[string]answer
	addr    string
	srv     *httptest.Server
}

type answer struct {
	status int
	body   string
}

func newFake(t *testing.T, answers map[string]answer) *fakeNitter {
	t.Helper()
	f := &fakeNitter{answers: answers}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		f.mu.Lock()
		a, ok := f.answers[req.URL.RequestURI()]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
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

// envelope is the decoded shape of one twitter.pipeline/v1 line: tweet and
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
	if env.Schema != "twitter.pipeline/v1" || env.Kind != "tweet" || env.ID != "103" || env.Data.ID != "103" {
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

// TestWatchJSONFlagRejected: watch is a stream — --json is a usage error
// pointing at --ndjson.
func TestWatchJSONFlagRejected(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "watch", "user:NASA", "--once", "--json")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--ndjson") {
		t.Errorf("stderr = %q, want guidance toward --ndjson", errOut)
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
	seenPath := filepath.Join(home, ".twitter-cli", "state", "seen.json")
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
