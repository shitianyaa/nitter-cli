package instances_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/cli"
)

const (
	rssXML = `<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel><title>NASA</title><item><title>One</title></item></channel></rss>`
	// The two markers the HTML probes accept; a fake instance serving this
	// body passes the user/search/list criteria.
	timelineHTML = `<div class="timeline"><div class="timeline-item">a post</div></div><div class="timeline-end">end</div>`
	// Latency cell shape in the human output (any duration unit).
	latencyCell = `^[0-9.]+(µs|ms|s)$`
)

// fastTOML disables retries, backoff and pacing so probes against httptest
// and unreachable addresses stay fast. Negative values are documented
// opt-outs in httpx; hand-edited config.toml is the user's power tool.
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

// recorder records request targets (path?query) as the fake receives them.
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

// newFakeNitter builds the brief's fake instance: RSS ok, user HTML ok,
// search 404, lists ok. searchStatus overrides the search answer.
func newFakeNitter(t *testing.T, searchStatus int) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	say := func(body string, status int) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			rec.add(req.URL.RequestURI())
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/NASA/rss", say(rssXML, 200))
	mux.HandleFunc("/otheruser/rss", say(rssXML, 200))
	mux.HandleFunc("/NASA", say(timelineHTML, 200))
	mux.HandleFunc("/otheruser", say(timelineHTML, 200))
	mux.HandleFunc("/i/lists/", say(timelineHTML, 200))
	mux.HandleFunc("/search", say("", searchStatus))
	mux.HandleFunc("/", say("", 404))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// instanceLines returns the output lines after the header, split into cells.
func instanceLines(t *testing.T, out string) [][]string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 1 || lines[0] != "url\trss\tuser_html\tsearch\tlist\tlatency" {
		t.Fatalf("output must start with the header line, got %q", out)
	}
	lines = lines[1:]
	if len(lines) == 0 {
		t.Fatalf("no instance lines in %q", out)
	}
	var parsed [][]string
	for _, line := range lines {
		parsed = append(parsed, strings.Split(line, "\t"))
	}
	return parsed
}

func TestInstancesTestProbesConfiguredInstance(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, out, errOut := runCLI(t, "instances", "test")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := instanceLines(t, out)
	if len(lines) != 1 {
		t.Fatalf("got %d instance lines, want 1", len(lines))
	}
	cells := lines[0]
	if cells[0] != srv.URL {
		t.Errorf("url cell = %q, want %q", cells[0], srv.URL)
	}
	if cells[1] != "ok" || cells[2] != "ok" {
		t.Errorf("rss/user_html cells = %q/%q, want ok/ok", cells[1], cells[2])
	}
	if cells[3] != "-" || cells[4] != "-" {
		t.Errorf("search/list cells = %q/%q, want -/- (probes disabled)", cells[3], cells[4])
	}
	if !regexp.MustCompile(latencyCell).MatchString(cells[5]) {
		t.Errorf("latency cell = %q, want a duration like 42ms", cells[5])
	}
}

func TestInstancesTestURLParameterOverridesConfig(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	// The configured instance is unreachable; the URL argument must win.
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \"http://127.0.0.1:1\"\n")

	code, out, errOut := runCLI(t, "instances", "test", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := instanceLines(t, out)
	if len(lines) != 1 || lines[0][0] != srv.URL || lines[0][1] != "ok" {
		t.Fatalf("lines = %v, want exactly the probed URL with rss ok", lines)
	}
	if strings.Contains(out, "http://127.0.0.1:1\t") {
		// Match the exact configured URL token: a bare "127.0.0.1:1" check
		// also matches the ephemeral fake-server URL (e.g. 127.0.0.1:1183).
		t.Errorf("output = %q, want the configured instance ignored", out)
	}
}

func TestInstancesTestJSONSingleInstanceIsOneObject(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	// Exactly one instance probed → a single JSON object, not an array.
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	var arr []any
	if json.Unmarshal([]byte(out), &arr) == nil {
		t.Fatalf("output is an array, want a single object for one instance:\n%s", out)
	}
	for _, key := range []string{"url", "rss", "user_html", "search", "list", "latency"} {
		if _, ok := obj[key]; !ok {
			t.Errorf("JSON is missing the %q key: %v", key, obj)
		}
	}
	if obj["url"] != srv.URL {
		t.Errorf("url = %v, want %q", obj["url"], srv.URL)
	}
	rss, ok := obj["rss"].(map[string]any)
	if !ok || rss["ok"] != true || rss["status"] != float64(200) {
		t.Errorf("rss = %v, want ok true/status 200", obj["rss"])
	}
	// Not-probed probes are zero objects with all keys present.
	search, ok := obj["search"].(map[string]any)
	if !ok || search["ok"] != false || search["status"] != float64(0) || search["err"] != "" {
		t.Errorf("search = %v, want the zero probe (not probed)", obj["search"])
	}
	if latency, ok := obj["latency"].(float64); !ok || latency < 0 {
		t.Errorf("latency = %v, want a non-negative number", obj["latency"])
	}
}

func TestInstancesTestJSONMultipleInstancesIsArray(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+
		"[[instances]]\nurl = \""+srv.URL+"\"\n"+
		"[[instances]]\nurl = \"http://127.0.0.1:1\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out)
	}
	if len(arr) != 2 {
		t.Fatalf("array length = %d, want 2", len(arr))
	}
	if arr[0]["url"] != srv.URL || arr[1]["url"] != "http://127.0.0.1:1" {
		t.Fatalf("urls = %v/%v, want config order preserved", arr[0]["url"], arr[1]["url"])
	}
	if first, _ := arr[0]["rss"].(map[string]any); first == nil || first["ok"] != true {
		t.Errorf("first rss = %v, want ok", arr[0]["rss"])
	}
	if second, _ := arr[1]["rss"].(map[string]any); second == nil || second["ok"] != false {
		t.Errorf("second rss = %v, want failed", arr[1]["rss"])
	}
}

func TestInstancesTestFullFlagEnablesSearchProbe(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--full")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	cells := instanceLines(t, out)[0]
	if cells[3] != "fail(404)" {
		t.Errorf("search cell = %q, want fail(404)", cells[3])
	}

	code, out, _ = runCLI(t, "instances", "test", "--full", "--json")
	if code != 0 {
		t.Fatalf("json exit = %d, want 0", code)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	search, _ := obj["search"].(map[string]any)
	if search == nil || search["ok"] != false || search["status"] != float64(404) {
		t.Errorf("json search = %v, want failed/404", obj["search"])
	}
}

func TestInstancesTestListIDFlag(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--list-id", "42")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	cells := instanceLines(t, out)[0]
	if cells[4] != "ok" {
		t.Errorf("list cell = %q, want ok", cells[4])
	}
}

func TestInstancesTestUserFlag(t *testing.T) {
	home := tempHome(t)
	srv, rec := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--user", "otheruser")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	cells := instanceLines(t, out)[0]
	if cells[1] != "ok" || cells[2] != "ok" {
		t.Errorf("rss/user_html cells = %q/%q, want ok/ok for the overridden user", cells[1], cells[2])
	}
	for _, want := range []string{"/otheruser/rss", "/otheruser"} {
		found := false
		for _, req := range rec.requests() {
			if req == want {
				found = true
			}
		}
		if !found {
			t.Errorf("requests %v miss %q", rec.requests(), want)
		}
	}
}

func TestInstancesTestListIDEmptyValueIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "instances", "test", "--list-id=")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "list-id") {
		t.Fatalf("stderr = %q, want it to mention --list-id", errOut)
	}
}

func TestInstancesTestNoInstancesNoURLIsUsageError(t *testing.T) {
	tempHome(t) // baseline config is published with zero instances
	code, _, errOut := runCLI(t, "instances", "test")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "instances") {
		t.Fatalf("stderr = %q, want guidance about configured instances", errOut)
	}
}

func TestInstancesTestAllProbesFailStillExitZero(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \"http://127.0.0.1:1\"\n")

	code, out, errOut := runCLI(t, "instances", "test")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 even when every probe fails (stderr %q)", code, errOut)
	}
	cells := instanceLines(t, out)[0]
	if !strings.HasPrefix(cells[1], "fail(") || !strings.HasPrefix(cells[2], "fail(") {
		t.Errorf("rss/user_html cells = %q/%q, want fail(...) cells", cells[1], cells[2])
	}
	if cells[3] != "-" || cells[4] != "-" {
		t.Errorf("search/list cells = %q/%q, want -/-", cells[3], cells[4])
	}

	code, out, _ = runCLI(t, "instances", "test", "--json")
	if code != 0 {
		t.Fatalf("json exit = %d, want 0", code)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	rss, _ := obj["rss"].(map[string]any)
	if rss == nil || rss["ok"] != false || rss["status"] != float64(0) || rss["err"] == "" {
		t.Errorf("json rss = %v, want failed with a short err and status 0", obj["rss"])
	}
}

func TestInstancesTestInvalidProxySchemeIsUsageErrorBeforeNetwork(t *testing.T) {
	home := tempHome(t)
	srv, rec := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, _, errOut := runCLI(t, "instances", "test", "--proxy", "ftp://x")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "proxy") {
		t.Fatalf("stderr = %q, want it to mention the proxy", errOut)
	}
	if got := rec.requests(); len(got) != 0 {
		t.Fatalf("requests = %v, want none (validation must precede any network)", got)
	}
}

func TestInstancesTestConfigProxyInvalidSchemeIsUsageError(t *testing.T) {
	home := tempHome(t)
	srv, rec := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"proxy = \"ftp://config\"\n[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, _, errOut := runCLI(t, "instances", "test")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if got := rec.requests(); len(got) != 0 {
		t.Fatalf("requests = %v, want none", got)
	}
}

func TestInstancesTestInstanceFlagOverridesConfig(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \"http://127.0.0.1:1\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--instance", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	cells := instanceLines(t, out)[0]
	if cells[0] != srv.URL || cells[1] != "ok" {
		t.Fatalf("cells = %v, want the --instance override probed with rss ok", cells)
	}
}

func TestInstancesTestBadURLArgumentIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "instances", "test", "ftp://example.com")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "scheme") {
		t.Fatalf("stderr = %q, want it to name the scheme problem", errOut)
	}
}

func TestInstancesTestInvalidConfigValueIsUsageError(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, "request_interval = \"abc\"\n[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, _, errOut := runCLI(t, "instances", "test")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "request_interval") {
		t.Fatalf("stderr = %q, want it to name the offending key", errOut)
	}
}

func TestInstancesTestUnknownSubcommandExits1(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "instances", "frobnicate")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Fatalf("stderr = %q, want the unknown-command error", errOut)
	}
}

func TestInstancesTestExtraArgIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, _ := runCLI(t, "instances", "test", "http://a", "http://b")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
