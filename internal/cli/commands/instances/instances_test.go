package instances_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
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

// instanceRecords parses the NDJSON envelope stream (the piped default since
// M10) into the per-instance data payloads, validating the envelope shape.
func instanceRecords(t *testing.T, out string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("no instance_report envelopes in %q", out)
	}
	var records []map[string]any
	for i, line := range lines {
		var env struct {
			Schema string         `json:"schema"`
			Kind   string         `json:"kind"`
			ID     string         `json:"id"`
			Data   map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d is not one JSON envelope: %v\n%s", i+1, err, line)
		}
		if env.Schema != "nitter.pipeline/v1" || env.Kind != "instance_report" {
			t.Fatalf("line %d = %s, want an nitter.pipeline/v1 instance_report envelope", i+1, line)
		}
		if url, _ := env.Data["url"].(string); url == "" || env.ID != url {
			t.Fatalf("line %d id = %q, want the instance data.url", i+1, env.ID)
		}
		records = append(records, env.Data)
	}
	return records
}

// probeOf returns one probe object from an instance report payload.
func probeOf(t *testing.T, data map[string]any, key string) map[string]any {
	t.Helper()
	p, ok := data[key].(map[string]any)
	if !ok {
		t.Fatalf("data is missing the %s probe: %v", key, data)
	}
	return p
}

// notProbed reports whether a probe object is the zero probe (the "-" cell
// of the human table: the probe did not run).
func notProbed(p map[string]any) bool {
	ok, _ := p["ok"].(bool)
	status, _ := p["status"].(float64)
	return !ok && status == 0
}

func TestInstancesTestProbesConfiguredInstance(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	code, out, errOut := runCLI(t, "instances", "test")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	records := instanceRecords(t, out)
	if len(records) != 1 {
		t.Fatalf("got %d instance records, want 1", len(records))
	}
	data := records[0]
	if data["url"] != srv.URL {
		t.Errorf("url = %v, want %q", data["url"], srv.URL)
	}
	if rss := probeOf(t, data, "rss"); rss["ok"] != true || rss["status"] != float64(200) {
		t.Errorf("rss = %v, want ok/status 200", rss)
	}
	if userHTML := probeOf(t, data, "user_html"); userHTML["ok"] != true {
		t.Errorf("user_html = %v, want ok", userHTML)
	}
	if !notProbed(probeOf(t, data, "search")) || !notProbed(probeOf(t, data, "list")) {
		t.Errorf("search/list = %v/%v, want the zero probes (not probed)", data["search"], data["list"])
	}
	if latency, ok := data["latency"].(float64); !ok || latency < 0 {
		t.Errorf("latency = %v, want a non-negative number", data["latency"])
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
	records := instanceRecords(t, out)
	if len(records) != 1 || records[0]["url"] != srv.URL || probeOf(t, records[0], "rss")["ok"] != true {
		t.Fatalf("records = %v, want exactly the probed URL with rss ok", records)
	}
	if strings.Contains(out, "\"http://127.0.0.1:1\"") {
		// Match the exact configured URL token (quote-delimited in the JSON
		// envelope): a bare "127.0.0.1:1" check would also match the
		// ephemeral fake-server URL (e.g. 127.0.0.1:1183).
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
	search := probeOf(t, instanceRecords(t, out)[0], "search")
	if search["ok"] != false || search["status"] != float64(404) {
		t.Errorf("search = %v, want failed/404", search)
	}

	code, out, _ = runCLI(t, "instances", "test", "--full", "--json")
	if code != 0 {
		t.Fatalf("json exit = %d, want 0", code)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	search, _ = obj["search"].(map[string]any)
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
	if list := probeOf(t, instanceRecords(t, out)[0], "list"); list["ok"] != true {
		t.Errorf("list = %v, want ok", list)
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
	data := instanceRecords(t, out)[0]
	if probeOf(t, data, "rss")["ok"] != true || probeOf(t, data, "user_html")["ok"] != true {
		t.Errorf("rss/user_html = %v/%v, want ok/ok for the overridden user", data["rss"], data["user_html"])
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
	data := instanceRecords(t, out)[0]
	for _, key := range []string{"rss", "user_html"} {
		p := probeOf(t, data, key)
		if p["ok"] != false || p["status"] != float64(0) || p["err"] == "" {
			t.Errorf("%s = %v, want failed with a short err and status 0", key, p)
		}
	}
	if !notProbed(probeOf(t, data, "search")) || !notProbed(probeOf(t, data, "list")) {
		t.Errorf("search/list = %v/%v, want the zero probes", data["search"], data["list"])
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
	data := instanceRecords(t, out)[0]
	if data["url"] != srv.URL || probeOf(t, data, "rss")["ok"] != true {
		t.Fatalf("record = %v, want the --instance override probed with rss ok", data)
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

func TestInstancesTestNDJSONOneEnvelopePerInstance(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+
		"[[instances]]\nurl = \""+srv.URL+"\"\n"+
		"[[instances]]\nurl = \"http://127.0.0.1:1\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d output lines, want one envelope per instance:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var env struct {
			Schema string         `json:"schema"`
			Kind   string         `json:"kind"`
			ID     string         `json:"id"`
			Data   map[string]any `json:"data"`
			Meta   any            `json:"meta"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i+1, err, line)
		}
		if env.Schema != "nitter.pipeline/v1" {
			t.Errorf("line %d schema = %q, want nitter.pipeline/v1", i+1, env.Schema)
		}
		if env.Kind != "instance_report" {
			t.Errorf("line %d kind = %q, want instance_report", i+1, env.Kind)
		}
		if env.Data == nil {
			t.Fatalf("line %d carries no data: %s", i+1, line)
		}
		url, _ := env.Data["url"].(string)
		if url == "" || env.ID != url {
			t.Errorf("line %d id = %q, want the instance url %q", i+1, env.ID, url)
		}
		if _, ok := env.Data["rss"].(map[string]any); !ok {
			t.Errorf("line %d data is missing the rss report: %s", i+1, line)
		}
		if _, ok := env.Data["latency"].(float64); !ok {
			t.Errorf("line %d data is missing the latency number: %s", i+1, line)
		}
		if env.Meta != nil {
			t.Errorf("line %d meta = %v, want the key absent (meta nil)", i+1, env.Meta)
		}
	}
}

func TestInstancesTestNDJSONURLArgumentSingleEnvelope(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \"http://127.0.0.1:1\"\n")

	code, out, errOut := runCLI(t, "instances", "test", "--ndjson", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("got %d lines, want exactly one envelope:\n%s", n, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if env["kind"] != "instance_report" || env["id"] != srv.URL {
		t.Fatalf("envelope = %v, want kind instance_report and id %q", env, srv.URL)
	}
}

func TestInstancesTestNDJSONAndJSONAreMutuallyExclusive(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "instances", "test", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--ndjson") || !strings.Contains(errOut, "--json") {
		t.Fatalf("stderr = %q, want it to name both conflicting flags", errOut)
	}
}
