package get_test

// End-to-end tests for `nitter get`: httptest fake instances, temp-HOME
// config fixtures and cli.Run-level exit code assertions, mirroring the
// user/search/list command test style.

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
// stay fast.
const fastTOML = "retry_attempts = -1\nretry_delay = \"-1s\"\nrequest_interval = \"-1s\"\ninstance_cooldown = \"-1s\"\nfetch_backend = \"nitter\"\n"

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
	return runCLIStdin(t, "", args...)
}

// runCLIStdin runs the CLI with the given stdin content (used for the
// stdin-ref contract).
func runCLIStdin(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(stdin), &out, &errOut)
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

// statusPage builds a Nitter-shaped conversation page whose focused status
// (the timeline-item with the stats row) is user/id.
func statusPage(user, id string) string {
	return `<div class="timeline">` +
		`<div class="timeline-item">` +
		`<a class="tweet-link" href="/` + user + `/status/` + id + `#m"></a>` +
		`<div class="tweet-content">get body ` + id + `</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
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

// TestGetPipeDefaultEmitsNDJSONEnvelope pins the M10 pipe default: with
// stdout not a TTY and no output flag given, `get` emits exactly ONE
// nitter.pipeline/v1 tweet envelope — no flag needed (a TTY keeps the human
// row; see the internal TTY test).
func TestGetPipeDefaultEmitsNDJSONEnvelope(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/status/101": {200, statusPage("nasa", "101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "get", "101")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want exactly one (the tweet, no media listing):\n%s", len(envs), out)
	}
	env := envs[0]
	if env["schema"] != "nitter.pipeline/v1" || env["kind"] != "tweet" || env["id"] != "101" {
		t.Errorf("envelope = %v, want kind tweet / id 101", env)
	}
	if data := dataOf(t, env); data["id"] != "101" || data["text"] != "get body 101" {
		t.Errorf("data = %v, want the tweet payload", data)
	}
	meta, ok := env["meta"].(map[string]any)
	if !ok || meta["source"] != "status:101" || meta["instance"] != fake.addr {
		t.Errorf("meta = %v, want source/instance provenance", env["meta"])
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/status/101"}) {
		t.Errorf("requests = %v, want the single user-less fetch", got)
	}
}

func TestGetURLRefUsesUserRoute(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/nasa/status/101": {200, statusPage("nasa", "101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "get", "https://x.com/nasa/status/101")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 || dataOf(t, envs[0])["id"] != "101" {
		t.Fatalf("envelopes = %v, want the fetched tweet", envs)
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/nasa/status/101"}) {
		t.Errorf("requests = %v, want the user route only", got)
	}
}

func TestGetURLRef404FallsBackToUserless(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/nasa/status/101": {404, "gone"},
		"/status/101":      {200, statusPage("nasa", "101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "get", "https://x.com/nasa/status/101")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 || dataOf(t, envs[0])["id"] != "101" {
		t.Fatalf("envelopes = %v, want the fetched tweet", envs)
	}
	if got := fake.rec.requests(); !slices.Equal(got, []string{"/nasa/status/101", "/status/101"}) {
		t.Errorf("requests = %v, want the user route then the user-less fallback", got)
	}
}

func TestGetJSONSingleObject(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/status/101": {200, statusPage("nasa", "101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "get", "101", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if obj["id"] != "101" || obj["url"] != "https://x.com/nasa/status/101" {
		t.Errorf("json = %v, want the projected tweet", obj)
	}
}

func TestGetNDJSONSingleEnvelope(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/status/101": {200, statusPage("nasa", "101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "get", "101", "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want exactly one envelope:\n%s", len(lines), out)
	}
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
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("line is not one JSON object: %v\n%s", err, lines[0])
	}
	if env.Schema != "nitter.pipeline/v1" || env.Kind != "tweet" || env.ID != "101" {
		t.Errorf("envelope = %s, want kind tweet / id 101", lines[0])
	}
	if env.Data.ID != "101" || env.Data.Author["handle"] != "nasa" {
		t.Errorf("data = %s", lines[0])
	}
	if env.Meta == nil {
		t.Fatalf("envelope carries no meta: %s", lines[0])
	}
	// meta.source carries the numeric status ID, not the raw ref.
	if env.Meta.Source != "status:101" {
		t.Errorf("meta.source = %q, want %q", env.Meta.Source, "status:101")
	}
	if env.Meta.Instance != fake.addr {
		t.Errorf("meta.instance = %q, want the serving instance", env.Meta.Instance)
	}
	if !strings.HasSuffix(env.Meta.FetchedAt, "Z") {
		t.Errorf("meta.fetched_at = %q, want RFC3339 UTC", env.Meta.FetchedAt)
	}
}

func TestGetStdinRef(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/status/101": {200, statusPage("nasa", "101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLIStdin(t, "101\n", "get")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 || dataOf(t, envs[0])["id"] != "101" {
		t.Fatalf("envelopes = %v, want the fetched tweet", envs)
	}

	// CRLF line endings are stripped, and URL refs work from stdin too.
	code, out, _ = runCLIStdin(t, "https://x.com/nasa/status/101\r\n", "get")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	envs = parseEnvelopes(t, out)
	if len(envs) != 1 || dataOf(t, envs[0])["id"] != "101" {
		t.Fatalf("envelopes = %v, want the fetched tweet", envs)
	}
}

func TestGetRefBothArgAndStdinIsUsageError(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, _ := runCLIStdin(t, "999\n", "get", "101")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if got := fake.rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (ambiguity is rejected before any network)", got)
	}
}

// TestGetEmptyStdinLineWithArgUsesArg: an empty stdin line carries no ref —
// it must not trigger the ambiguity error when an argument is given.
func TestGetEmptyStdinLineWithArgUsesArg(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/status/101": {200, statusPage("nasa", "101")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLIStdin(t, "\n", "get", "101")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 || dataOf(t, envs[0])["id"] != "101" {
		t.Fatalf("envelopes = %v, want the fetched tweet", envs)
	}
}

func TestGetNoRefIsUsageError(t *testing.T) {
	tempHome(t)
	for _, stdin := range []string{"", "\n", "   \n"} {
		code, _, _ := runCLIStdin(t, stdin, "get")
		if code != 2 {
			t.Fatalf("get with stdin %q: exit = %d, want 2", stdin, code)
		}
	}
}

func TestGetInvalidRefIsUsageErrorBeforeNetwork(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	for _, ref := range []string{"abc", "https://x.com/i/web/status/123", "user/status/123"} {
		code, out, errOut := runCLI(t, "get", ref)
		if code != 2 {
			t.Fatalf("get %q: exit = %d, want 2 (stderr %q)", ref, code, errOut)
		}
		if out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
	}
	if got := fake.rec.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (ref parsing precedes any network)", got)
	}
}

func TestGetAllInstancesFailExitsOne(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/status/101": {503, "down"},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "get", "101")
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

func TestGetNoInstancesExitsOne(t *testing.T) {
	home := tempHome(t) // zero instances, no --instance
	writeConfig(t, home, fastTOML)
	code, _, errOut := runCLI(t, "get", "101")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "no instances configured") {
		t.Fatalf("stderr = %q, want the chooser's message", errOut)
	}
}

func TestGetJSONAndNDJSONAreMutuallyExclusive(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "get", "101", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--json") || !strings.Contains(errOut, "--ndjson") {
		t.Fatalf("stderr = %q, want it to name both flags", errOut)
	}
}

func TestGetExtraArgsAreUsageError(t *testing.T) {
	tempHome(t)
	code, _, _ := runCLI(t, "get", "101", "102")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestGetInstanceFlagNeedsNoConfiguredInstance(t *testing.T) {
	tempHome(t) // baseline config with zero instances
	fake := newFake(t, map[string]answer{
		"/status/101": {200, statusPage("nasa", "101")},
	})

	code, out, errOut := runCLI(t, "get", "101", "--instance", fake.addr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	envs := parseEnvelopes(t, out)
	if len(envs) != 1 || dataOf(t, envs[0])["id"] != "101" {
		t.Fatalf("envelopes = %v, want the fetched tweet", envs)
	}
}

func TestGetFxFastLane(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/2/status/555") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"tweet": map[string]any{
					"id":   "555",
					"text": "fast lane status",
					"author": map[string]any{
						"screen_name": "space",
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

	code, out, _ := runCLI(t, "get", "555", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, `"id":"555"`) || !strings.Contains(out, "fast lane status") {
		t.Errorf("out = %q, want tweet from Fx fast-lane", out)
	}
}
