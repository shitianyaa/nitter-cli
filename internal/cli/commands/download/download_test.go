package download_test

// End-to-end tests for `nitter download`: a fake Nitter instance plays both
// the status pages (the nitter strategy is the private resolve+download path,
// whose links the command fetches) and the media files, with temp-HOME config
// fixtures and cli.Run-level exit code assertions, mirroring the media
// command test style.
//
// This test FILE, like media_test.go, is full-chain over the real wiring: the
// resolution strategies, the planner and the downloader all run for real
// against httptest. The paths the real chain cannot reach (fallback chains,
// covers, Content-Type-derived extensions — the third-party strategies drop
// plain-http links) are covered in download_internal_test.go with injected
// capabilities; the command package itself stays behind the R11 boundary.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/download"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
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
		"NITTER_DEFAULT_LIMIT", "NITTER_LOG_LEVEL", "NITTER_LOG_FORMAT", "NITTER_FETCH_BACKEND",
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

// instanceConfig is fastTOML plus the fake instance as the configured
// [[instances]] entry (the nitter strategy's resolve+download base).
func instanceConfig(addr string) string {
	return fastTOML + "[[instances]]\nurl = \"" + addr + "\"\n"
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runCLIStdin(t, "", args...)
}

func runCLIStdin(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// fakeBackend serves canned answers keyed by the exact request URI and
// records every request target.
type fakeBackend struct {
	mu   sync.Mutex
	seen []string
	srv  *httptest.Server
	addr string
}

type answer struct {
	status  int
	body    string
	headers map[string]string
}

func (f *fakeBackend) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func (f *fakeBackend) hits(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.seen {
		if p == path {
			n++
		}
	}
	return n
}

func newFakeBackend(t *testing.T, answers map[string]answer) *fakeBackend {
	t.Helper()
	f := &fakeBackend{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, req.URL.RequestURI())
		f.mu.Unlock()
		a, ok := answers[req.URL.RequestURI()]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		for k, v := range a.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(a.status)
		_, _ = fmt.Fprint(w, a.body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.addr = f.srv.URL
	return f
}

const (
	ref100 = "https://x.com/nasa/status/2070000000000000100"
	id100  = "2070000000000000100"
)

// nitterVideoPage is a status page whose focused item carries one /video/
// placeholder: the nitter strategy keeps the (plain-http) instance link, and
// that link is the download target.
func nitterVideoPage(id string) string {
	return nitterVideoPageLink(id, "/video/exv.mp4")
}

// nitterVideoPageLink is the video page variant with a caller-chosen
// /video/ href (the partial-failure fixture points at a missing file).
func nitterVideoPageLink(id, videoHref string) string {
	return `<div class="timeline"><div class="timeline-item">` +
		`<a class="tweet-link" href="/nasa/status/` + id + `#m"></a>` +
		`<div class="tweet-content">with video</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="attachments"><a class="video-container" href="` + videoHref + `">video</a></div>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
}

// nitterImagesPage is an image-only status page carrying len(names)
// /pic/ proxy image links.
func nitterImagesPage(id string, names ...string) string {
	var atts strings.Builder
	for _, n := range names {
		atts.WriteString(`<a class="still-image" href="/pic/orig/` + n + `.jpg">img</a>`)
	}
	return `<div class="timeline"><div class="timeline-item">` +
		`<a class="tweet-link" href="/nasa/status/` + id + `#m"></a>` +
		`<div class="tweet-content">with images</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="attachments">` + atts.String() + `</div>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
}

// tweetEnvelope renders the get/watch NDJSON record download's stdin
// envelope mode consumes (only schema/kind/data.url matter to the parser).
func tweetEnvelope(url string) string {
	return `{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2070000000000000100","data":{"id":"2070000000000000100","url":"` + url + `"},"meta":{}}`
}

// wantSHA is the expected lowercase hex digest of a fixed body.
func wantSHA(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// downloadEnvelope decodes one nitter.pipeline/v1 download envelope line.
func downloadEnvelope(t *testing.T, line string) (schema, kind, id string, data struct {
	Ref    string `json:"ref"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	URL    string `json:"url"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}, metaInput string) {
	t.Helper()
	var env struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Data   struct {
			Ref    string `json:"ref"`
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Source string `json:"source"`
			URL    string `json:"url"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"data"`
		Meta *struct {
			Input string `json:"input"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, line)
	}
	if env.Meta != nil {
		metaInput = env.Meta.Input
	}
	return env.Schema, env.Kind, env.ID, env.Data, metaInput
}

// TestDownloadPipeDefaultEmitsNDJSONEnvelope pins the M10 pipe default: with
// stdout not a TTY and no output flag given, the downloaded file is reported
// as one nitter.pipeline/v1 download envelope — no flag needed (a TTY keeps
// the tab row; see the internal TTY test).
func TestDownloadPipeDefaultEmitsNDJSONEnvelope(t *testing.T) {
	home := tempHome(t)
	video := "fake-mp4-bytes"
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: video},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want exactly one envelope:\n%s", len(lines), out)
	}
	schema, kind, id, data, metaInput := downloadEnvelope(t, lines[0])
	if schema != "nitter.pipeline/v1" || kind != "download" {
		t.Errorf("schema/kind = %q/%q, want nitter.pipeline/v1/download", schema, kind)
	}
	wantPath := filepath.Join(outDir, id100+"-1.mp4")
	if id != wantPath || data.Path != wantPath {
		t.Errorf("id/path = %q/%q, want %q", id, data.Path, wantPath)
	}
	if data.Ref != ref100 || data.Bytes != int64(len(video)) || data.Kind != "video" || data.Source != "nitter" {
		t.Errorf("data = %+v, want the streamed video record", data)
	}
	if metaInput != ref100 {
		t.Errorf("meta.input = %q, want the raw ref", metaInput)
	}
	if got, err := os.ReadFile(wantPath); err != nil || string(got) != video {
		t.Errorf("file = %q (%v), want the streamed bytes at %s", got, err, wantPath)
	}
	if got := fake.requests(); len(got) != 2 || got[0] != "/nasa/status/"+id100 || got[1] != "/video/exv.mp4" {
		t.Errorf("requests = %v, want the status page then the video", got)
	}
}

func TestDownloadFourImagesWritesFourFiles(t *testing.T) {
	home := tempHome(t)
	images := []string{"image-one", "image-two", "image-three", "image-four"}
	answers := map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterImagesPage(id100, "AAA1", "AAA2", "AAA3", "AAA4")},
	}
	for i, name := range []string{"AAA1", "AAA2", "AAA3", "AAA4"} {
		answers["/pic/orig/"+name+".jpg"] = answer{status: 200, body: images[i]}
	}
	fake := newFakeBackend(t, answers)
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d envelopes, want 4:\n%s", len(lines), out)
	}
	for i := range []string{"AAA1", "AAA2", "AAA3", "AAA4"} {
		wantPath := filepath.Join(outDir, fmt.Sprintf("%s-%d.jpg", id100, i+1))
		_, kind, id, data, _ := downloadEnvelope(t, lines[i])
		if kind != "download" || id != wantPath {
			t.Errorf("envelope %d id = %q/%q, want the download record at %s", i+1, kind, id, wantPath)
		}
		if data.Bytes != int64(len(images[i])) {
			t.Errorf("envelope %d bytes = %d, want %d", i+1, data.Bytes, len(images[i]))
		}
		if got, err := os.ReadFile(wantPath); err != nil || string(got) != images[i] {
			t.Errorf("file %s = %q (%v), want %q", wantPath, got, err, images[i])
		}
	}
}

func TestDownloadCoverOnVideoTweetWithoutCoverIsNotFound(t *testing.T) {
	// The nitter page parse carries no cover metadata, so a video status
	// asked for --kind cover is a not_found plan failure (the machine shape
	// of "nothing to download for this ref").
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--kind", "cover", "--output", outDir, "--ndjson")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Command string `json:"command"`
			Stage   string `json:"stage"`
			Code    string `json:"code"`
		} `json:"data"`
		Meta *struct {
			Input string `json:"input"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("stdout line is not JSON: %v\n%s", err, out)
	}
	if env.Kind != "error" || env.Data.Command != "download" || env.Data.Stage != "plan" || env.Data.Code != "not_found" {
		t.Errorf("envelope = %s, want the plan-stage not_found error", strings.TrimSpace(out))
	}
	if env.Meta == nil || env.Meta.Input != ref100 {
		t.Errorf("meta.input = %+v, want the raw ref", env.Meta)
	}
	if !strings.Contains(errOut, "download completed with 1 of 1 refs failed") {
		t.Errorf("stderr = %q, want the batch summary", errOut)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) != 0 {
		t.Errorf("output dir must stay empty, got %v (%v)", entries, err)
	}
}

func TestDownloadKindImageOnVideoTweetIsEmpty(t *testing.T) {
	// A kind filter matching nothing yields an empty plan — not a failure:
	// nothing on stdout, a "(empty)" hint on stderr, exit 0.
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--kind", "image", "--output", outDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "(empty)") {
		t.Errorf("stderr = %q, want the empty hint", errOut)
	}
	if got := fake.requests(); len(got) != 1 {
		t.Errorf("requests = %v, want the status page only (no downloads)", got)
	}
}

func TestDownloadOnExistsModes(t *testing.T) {
	home := tempHome(t)
	// A counter serves a fresh body per download so overwrite is observable.
	var mu sync.Mutex
	fetches := 0
	var pages, downloads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.URL.Path == "/nasa/status/"+id100 {
			pages = append(pages, req.URL.Path)
			_, _ = fmt.Fprint(w, nitterVideoPage(id100))
			return
		}
		if req.URL.Path == "/video/exv.mp4" {
			fetches++
			downloads = append(downloads, req.URL.Path)
			_, _ = fmt.Fprintf(w, "video-content-%d", fetches)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	writeConfig(t, home, instanceConfig(srv.URL))
	outDir := t.TempDir()
	final := filepath.Join(outDir, id100+"-1.mp4")

	// First run downloads video-content-1 (piped NDJSON default envelope).
	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("run 1: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	_, kind, _, data, _ := downloadEnvelope(t, strings.TrimSpace(out))
	if kind != "download" || data.Bytes != 15 {
		t.Fatalf("run 1: envelope = %s, want the download record", strings.TrimSpace(out))
	}
	first, err := os.ReadFile(final)
	if err != nil || string(first) != "video-content-1" {
		t.Fatalf("run 1: file = %q (%v), want the first body", first, err)
	}

	// refuse (the default): the second run reports the entry as an in-place
	// error envelope and the batch continues — exit 1, no re-download, file
	// untouched.
	code, out, errOut = runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 1 {
		t.Fatalf("run 2 (refuse): exit = %d, want 1 (stderr %q)", code, errOut)
	}
	var refuse struct {
		Kind string `json:"kind"`
		Data struct {
			Command string `json:"command"`
			Stage   string `json:"stage"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"data"`
		Meta *struct {
			Input string `json:"input"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &refuse); err != nil {
		t.Fatalf("run 2 (refuse): stdout is not one envelope: %v\n%s", err, out)
	}
	if refuse.Kind != "error" || refuse.Data.Command != "download" || refuse.Data.Stage != "download" || refuse.Data.Code != "local_state_error" {
		t.Errorf("run 2 (refuse): envelope = %s, want the download-stage existence error", strings.TrimSpace(out))
	}
	if !strings.Contains(refuse.Data.Message, "already exists") {
		t.Errorf("run 2 (refuse): message = %q, want the existence problem", refuse.Data.Message)
	}
	if refuse.Meta == nil || refuse.Meta.Input != ref100 {
		t.Errorf("run 2 (refuse): meta.input = %+v, want the raw ref", refuse.Meta)
	}
	if !strings.Contains(errOut, "download completed with 1 of 1 refs failed") {
		t.Errorf("run 2 (refuse): stderr = %q, want the batch summary", errOut)
	}
	if got, _ := os.ReadFile(final); string(got) != "video-content-1" {
		t.Errorf("run 2 (refuse): file = %q, want it untouched", got)
	}

	// skip: the existing file is kept and reported — envelope with the
	// on-disk size, no failure, no re-download.
	code, out, errOut = runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir, "--on-exists", "skip")
	if code != 0 {
		t.Fatalf("run 3 (skip): exit = %d, want 0 (stderr %q)", code, errOut)
	}
	_, kind, id, data, _ := downloadEnvelope(t, strings.TrimSpace(out))
	if kind != "download" || id != final || data.Path != final || data.Bytes != 15 {
		t.Errorf("run 3 (skip): envelope = %s, want the skip record with the on-disk size", strings.TrimSpace(out))
	}
	if got, _ := os.ReadFile(final); string(got) != "video-content-1" {
		t.Errorf("run 3 (skip): file = %q, want it untouched", got)
	}

	// overwrite: the file is re-downloaded through the same atomic flow.
	code, out, errOut = runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir, "--on-exists", "overwrite")
	if code != 0 {
		t.Fatalf("run 4 (overwrite): exit = %d, want 0 (stderr %q)", code, errOut)
	}
	_, kind, _, data, _ = downloadEnvelope(t, strings.TrimSpace(out))
	if kind != "download" || data.Bytes != 15 || data.SHA256 != wantSHA("video-content-2") {
		t.Errorf("run 4 (overwrite): envelope = %s, want the re-downloaded record", strings.TrimSpace(out))
	}
	if got, _ := os.ReadFile(final); string(got) != "video-content-2" {
		t.Errorf("run 4 (overwrite): file = %q, want the re-downloaded body", got)
	}

	// skip in NDJSON mode: the row carries the on-disk size and NO sha256
	// key (nothing was re-downloaded, nothing is fabricated).
	code, out, _ = runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir, "--on-exists", "skip", "--ndjson")
	if code != 0 {
		t.Fatalf("run 5 (skip ndjson): exit = %d, want 0", code)
	}
	var env struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Data struct {
			Path   string `json:"path"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("run 5: line is not JSON: %v\n%s", err, out)
	}
	if env.Kind != "download" || env.ID != final || env.Data.Path != final || env.Data.Bytes != 15 {
		t.Errorf("run 5: envelope = %s, want the skip row with the on-disk size", strings.TrimSpace(out))
	}
	if strings.Contains(out, "sha256") {
		t.Errorf("run 5: skip row must not carry a sha256 key: %s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if fetches != 2 {
		t.Errorf("download fetches = %d, want 2 (refuse and skip never re-download)", fetches)
	}
}

func TestDownloadNdjsonEnvelopeShape(t *testing.T) {
	home := tempHome(t)
	video := "ndjson-video-bytes"
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: video},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir, "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var env struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Data   struct {
			Ref    string `json:"ref"`
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Source string `json:"source"`
			URL    string `json:"url"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"data"`
		Meta *struct {
			Input string `json:"input"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, out)
	}
	wantPath := filepath.Join(outDir, id100+"-1.mp4")
	if env.Schema != "nitter.pipeline/v1" || env.Kind != "download" {
		t.Errorf("schema/kind = %q/%q, want nitter.pipeline/v1/download", env.Schema, env.Kind)
	}
	if env.ID != wantPath || env.Data.Path != wantPath {
		t.Errorf("id/path = %q/%q, want the absolute on-disk path %q", env.ID, env.Data.Path, wantPath)
	}
	if env.Data.Ref != ref100 || env.Data.Kind != "video" || env.Data.Source != "nitter" {
		t.Errorf("data = %+v, want ref/kind/source stamped", env.Data)
	}
	if env.Data.URL != fake.addr+"/video/exv.mp4" || env.Data.Bytes != int64(len(video)) {
		t.Errorf("data = %+v, want the served URL and %d bytes", env.Data, len(video))
	}
	if env.Data.SHA256 != wantSHA(video) {
		t.Errorf("sha256 = %q, want %q", env.Data.SHA256, wantSHA(video))
	}
	if env.Meta == nil || env.Meta.Input != ref100 {
		t.Errorf("meta.input = %+v, want the raw ref", env.Meta)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("the envelope's id names no file: %v", err)
	}
}

func TestDownloadJSONCardinality(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	// Exactly one file: a single object.
	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("single-file --json is not one object: %v\n%s", err, out)
	}
	if obj.Kind != "video" || obj.Path != filepath.Join(outDir, id100+"-1.mp4") {
		t.Errorf("object = %+v, want the video record", obj)
	}

	// Four files: an array.
	images := map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterImagesPage(id100, "AAA1", "AAA2", "AAA3", "AAA4")},
	}
	for _, name := range []string{"AAA1", "AAA2", "AAA3", "AAA4"} {
		images["/pic/orig/"+name+".jpg"] = answer{status: 200, body: "img"}
	}
	fake2 := newFakeBackend(t, images)
	writeConfig(t, home, instanceConfig(fake2.addr))
	code, out, errOut = runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir, "--json")
	if code != 0 {
		t.Fatalf("four-image exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var list []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("four-file --json is not an array: %v\n%s", err, out)
	}
	if len(list) != 4 {
		t.Fatalf("got %d records, want 4: %s", len(list), out)
	}

	// Nothing downloaded: exactly []. (--kind video filters the image-only
	// page away — no downloads, no conflict with the files above.)
	code, out, errOut = runCLI(t, "download", ref100, "--strategy", "nitter", "--kind", "video", "--output", outDir, "--json")
	if code != 0 {
		t.Fatalf("empty exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty --json = %q, want []", out)
	}
}

func TestDownloadPartialFailureEmitsErrorEnvelopeAndExitsOne(t *testing.T) {
	home := tempHome(t)
	refB := "https://x.com/nasa/status/2070000000000000200"
	idB := "2070000000000000200"
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
		// refB's page points at the missing file: the ref fails in place.
		"/nasa/status/" + idB: {status: 200, body: nitterVideoPageLink(idB, "/video/missing.mp4")},
		"/video/missing.mp4":  {status: 404, body: "gone"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, refB, "--strategy", "nitter", "--output", outDir, "--ndjson")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d envelope lines, want 2 (download then error):\n%s", len(lines), out)
	}
	var dl struct {
		Kind string `json:"kind"`
		Data struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &dl); err != nil {
		t.Fatalf("line 1 is not JSON: %v", err)
	}
	if dl.Kind != "download" || dl.Data.Path != filepath.Join(outDir, id100+"-1.mp4") {
		t.Errorf("line 1 = %s, want ref100's download envelope", lines[0])
	}
	var errEnv struct {
		Kind string `json:"kind"`
		Data struct {
			Command string `json:"command"`
			Stage   string `json:"stage"`
			Code    string `json:"code"`
		} `json:"data"`
		Meta *struct {
			Input string `json:"input"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &errEnv); err != nil {
		t.Fatalf("line 2 is not JSON: %v", err)
	}
	if errEnv.Kind != "error" || errEnv.Data.Command != "download" || errEnv.Data.Stage != "download" || errEnv.Data.Code != "not_found" {
		t.Errorf("line 2 = %s, want the download-stage not_found error envelope", lines[1])
	}
	if errEnv.Meta == nil || errEnv.Meta.Input != refB {
		t.Errorf("meta.input = %+v, want ref B", errEnv.Meta)
	}
	if !strings.Contains(errOut, "download completed with 1 of 2 refs failed") {
		t.Errorf("stderr = %q, want the batch summary", errOut)
	}
}

func TestDownloadStdinEnvelopeMode(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLIStdin(t, tweetEnvelope(ref100)+"\n", "download", "--strategy", "nitter", "--output", outDir, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj struct {
		Ref  string `json:"ref"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("--json is not one object: %v\n%s", err, out)
	}
	if obj.Ref != ref100 || obj.Path != filepath.Join(outDir, id100+"-1.mp4") {
		t.Errorf("record = %+v, want the envelope's data.url downloaded", obj)
	}
}

func TestDownloadStdinEnvelopeIsStrict(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	for _, tc := range []struct{ name, line string }{
		{"truncated json", `{"schema":"nitter.pipeline/v1","kind":"tweet",`},
		{"wrong schema", `{"schema":"twitter.pipeline/v1","kind":"tweet","data":{"url":"` + ref100 + `"}}`},
		{"wrong kind", `{"schema":"nitter.pipeline/v1","kind":"media","data":{"url":"` + ref100 + `"}}`},
		{"missing url", `{"schema":"nitter.pipeline/v1","kind":"tweet","data":{}}`},
		{"data not an object", `{[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runCLIStdin(t, tc.line+"\n", "download", "--strategy", "nitter", "--output", outDir)
			if code != 2 {
				t.Fatalf("%s: exit = %d, want 2 (stderr %q)", tc.name, code, errOut)
			}
			if out != "" {
				t.Errorf("%s: stdout = %q, want nothing", tc.name, out)
			}
			if got := fake.requests(); len(got) != 0 {
				t.Errorf("%s: requests = %v, want none (malformed input is rejected before any network)", tc.name, got)
			}
		})
	}
}

func TestDownloadStdinPlainLines(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, _, errOut := runCLIStdin(t, "  "+ref100+"  \n", "download", "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(outDir, id100+"-1.mp4")); err != nil {
		t.Errorf("plain-line ref not downloaded: %v", err)
	}
}

func TestDownloadUsageErrorsExit2BeforeNetwork(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	for _, tc := range [][]string{
		{"--strategy", "bogus"},
		{"--strategy", "vx"},
		{"--strategy", "syndication"},
		{"--quality", "ultra"},
		{"--kind", "audio"},
		{"--on-exists", "replace"},
		{"--json", "--ndjson"},
	} {
		// The bad flag comes last on purpose: pflag's last Set wins, so the
		// invalid value is what the command sees.
		args := append([]string{"download", ref100, "--strategy", "nitter", "--output", outDir}, tc...)
		code, out, errOut := runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", tc, code, errOut)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q, want nothing", tc, out)
		}
	}

	// vx and syndication are removed strategies: the usage error names the
	// remaining ones (fx/nitter/xdown).
	for _, name := range []string{"vx", "syndication"} {
		code, _, errOut := runCLI(t, "download", ref100, "--strategy", name, "--output", outDir)
		if code != 2 {
			t.Errorf("--strategy %s: exit = %d, want 2 (stderr %q)", name, code, errOut)
		}
		for _, want := range []string{"fx", "nitter", "xdown"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("--strategy %s: stderr %q, want it to name %q", name, errOut, want)
			}
		}
	}
	for _, args := range [][]string{
		{"download", "not-a-ref"},
		{"download"},
	} {
		code, out, errOut := runCLI(t, append(args, "--output", outDir, "--strategy", "nitter")...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", args, code, errOut)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q, want nothing", args, out)
		}
	}
	if got := fake.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (usage errors precede any network)", got)
	}
}

// TestDownloadArgTakesPrecedenceOverStdin: positional REFs win outright — the
// stdin payload is ignored (not read, not parsed), so only the argument's
// status is downloaded. The stdin line here is a valid tweet envelope for a
// DIFFERENT status, which must leave no trace.
func TestDownloadArgTakesPrecedenceOverStdin(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	other := "https://x.com/nasa/status/2070000000000000999"
	code, out, errOut := runCLIStdin(t, tweetEnvelope(other)+"\n", "download", ref100, "--strategy", "nitter", "--output", outDir, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj struct {
		Ref  string `json:"ref"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("--json is not one object: %v\n%s", err, out)
	}
	if obj.Ref != ref100 || obj.Path != filepath.Join(outDir, id100+"-1.mp4") {
		t.Errorf("record = %+v, want the argument's file", obj)
	}
	if got := fake.requests(); len(got) != 2 || !strings.HasSuffix(got[0], "/nasa/status/"+id100) {
		t.Errorf("requests = %v, want only the argument's resolve + download", got)
	}
	if _, err := os.Stat(filepath.Join(outDir, "2070000000000000999-1.mp4")); err == nil {
		t.Error("the stdin ref was downloaded; positional REFs must win outright")
	}
}

// TestDownloadArgDoesNotBlockOnOpenStdin is the pipe-hang regression: with a
// positional REF the command must never read stdin. io.Pipe's read end blocks
// until its write end produces data or closes, so a stdin-first
// implementation hangs here forever.
func TestDownloadArgDoesNotBlockOnOpenStdin(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	done := make(chan int, 1)
	go func() {
		var out, errOut strings.Builder
		done <- cli.Run([]string{"download", ref100, "--strategy", "nitter", "--output", outDir}, pr, &out, &errOut)
	}()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("download with a positional REF blocked on stdin: the never-closed pipe reader was read")
	}
	if _, err := os.Stat(filepath.Join(outDir, id100+"-1.mp4")); err != nil {
		t.Errorf("the argument's file was not downloaded: %v", err)
	}
}

func TestDownloadOutputFlagOverridesConfigAndDirIsCreated(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	configDir := t.TempDir()
	// Forward slashes: TOML basic strings treat a backslash as an escape,
	// and the Windows temp path carries plenty of them.
	configDirTOML := strings.ReplaceAll(configDir, "\\", "/")
	writeConfig(t, home, fastTOML+
		"download_path = \""+configDirTOML+"\"\n"+
		"[[instances]]\nurl = \""+fake.addr+"\"\n")

	// Without --output: the config's download_path is used.
	code, _, errOut := runCLI(t, "download", ref100, "--strategy", "nitter")
	if code != 0 {
		t.Fatalf("config-path run: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(configDir, id100+"-1.mp4")); err != nil {
		t.Errorf("config download_path not used: %v", err)
	}

	// --output overrides per invocation and is created on demand (mkdir -p).
	flagDir := filepath.Join(t.TempDir(), "made", "on", "demand")
	code, _, errOut = runCLI(t, "download", ref100, "--strategy", "nitter", "--output", flagDir)
	if code != 0 {
		t.Fatalf("flag run: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(flagDir, id100+"-1.mp4")); err != nil {
		t.Errorf("--output dir not created/used: %v", err)
	}
}

// epipeWriter fails every write with syscall.EPIPE, the portable shape of a
// reader that hung up on a pipe.
type epipeWriter struct{}

func (epipeWriter) Write(p []byte) (int, error) { return 0, syscall.EPIPE }

func TestDownloadEPipeIsGracefulExitZero(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	var errOut strings.Builder
	code := cli.Run([]string{"download", ref100, "--strategy", "nitter", "--output", outDir, "--ndjson"},
		strings.NewReader(""), epipeWriter{}, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (EPIPE on stdout is a clean stop; stderr %q)", code, errOut.String())
	}
}

// TestDownloadTTYDefaultStaysTextRows pins the TTY half of the M10 pipe
// default: on a TTY (OutIsTTY true) the no-flag default is STILL the
// tab-separated row — the envelope stream is the pipe default only. The
// command is built over explicit streams because cli.Run probes real files
// only and can never fake a TTY.
func TestDownloadTTYDefaultStaysTextRows(t *testing.T) {
	home := tempHome(t)
	video := "tty-video-bytes"
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: video},
	})
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{},
	}
	cmd := download.New(s)
	cmd.SetArgs([]string{ref100, "--strategy", "nitter", "--output", outDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = %v, want nil", err)
	}
	wantPath := filepath.Join(outDir, id100+"-1.mp4")
	wantRow := strings.Join([]string{ref100, wantPath, fmt.Sprint(len(video)), "video", "nitter"}, "\t")
	if out.String() != wantRow+"\n" {
		t.Errorf("stdout = %q, want %q", out.String(), wantRow+"\n")
	}
	if got, err := os.ReadFile(wantPath); err != nil || string(got) != video {
		t.Errorf("file = %q (%v), want the streamed bytes at %s", got, err, wantPath)
	}
}

// TestDownloadFilenameTemplateFlagOverridesConfig pins the precedence: the
// config filename_template applies alone; --filename-template overrides it
// per invocation.
func TestDownloadFilenameTemplateFlagOverridesConfig(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, fastTOML+
		"filename_template = \"cfg-{id}\"\n"+
		"[[instances]]\nurl = \""+fake.addr+"\"\n")
	outDir := t.TempDir()

	// Config alone: cfg-<id>.mp4.
	code, _, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("config run: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(outDir, "cfg-"+id100+".mp4")); err != nil {
		t.Errorf("config template not applied: %v", err)
	}

	// The flag wins over the config.
	code, _, errOut = runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir,
		"--filename-template", "flag-{kind}-{seq}")
	if code != 0 {
		t.Fatalf("flag run: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(outDir, "flag-video-1.mp4")); err != nil {
		t.Errorf("flag template not applied: %v", err)
	}
}

// TestDownloadDirectoryTemplatePlacesFilesInSubdirectories pins the config
// directory_template end to end: the nitter-strategy video lands in the
// rendered subdirectory of the output directory.
func TestDownloadDirectoryTemplatePlacesFilesInSubdirectories(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, fastTOML+
		"directory_template = \"media/{kind}\"\n"+
		"[[instances]]\nurl = \""+fake.addr+"\"\n")
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	wantPath := filepath.Join(outDir, "media", "video", id100+"-1.mp4")
	_, kind, id, _, _ := downloadEnvelope(t, strings.TrimSpace(out))
	if kind != "download" || id != wantPath {
		t.Errorf("envelope = %s, want the download record at %s", strings.TrimSpace(out), wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("subdirectory file missing: %v", err)
	}
}

// TestDownloadInvalidConfigTemplateWarnsAndFallsBack pins the pixiv
// semantics end to end: an invalid filename_template in the config never
// fails the run — one stderr warning, default naming.
func TestDownloadInvalidConfigTemplateWarnsAndFallsBack(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
		"/video/exv.mp4":        {status: 200, body: "video"},
	})
	writeConfig(t, home, fastTOML+
		"filename_template = \"{bogus}-{id}\"\n"+
		"[[instances]]\nurl = \""+fake.addr+"\"\n")
	outDir := t.TempDir()

	code, _, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (invalid template warns, never fails; stderr %q)", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(outDir, id100+"-1.mp4")); err != nil {
		t.Errorf("default-named file missing: %v", err)
	}
	// Count only the warning lines: stderr also carries the informational
	// "note: writing to <dir>" line, which is not a warning.
	warnings := make([]string, 0, 2)
	for _, line := range strings.Split(strings.TrimRight(errOut, "\n"), "\n") {
		if strings.HasPrefix(line, "warning:") {
			warnings = append(warnings, line)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "filename_template") {
		t.Errorf("stderr = %q, want exactly one filename_template warning", errOut)
	}
}

// The resolved output directory is reported on stderr before anything is
// written: the path is already absolute and the directory already exists by
// then, so this is informational, never a gate. stdout stays a clean machine
// contract.
func TestDownloadNotesResolvedOutputDirectory(t *testing.T) {
	home := tempHome(t)
	answers := map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterImagesPage(id100, "AAA1")},
	}
	answers["/pic/orig/AAA1.jpg"] = answer{status: 200, body: "one"}
	fake := newFakeBackend(t, answers)
	writeConfig(t, home, instanceConfig(fake.addr))
	outDir := t.TempDir()

	code, out, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", outDir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "note: writing to "+outDir) {
		t.Errorf("stderr = %q, want a note naming the resolved directory %q", errOut, outDir)
	}
	if strings.Contains(out, "writing to") {
		t.Errorf("stdout must not carry the note: %q", out)
	}
	if n := strings.Count(errOut, "note: writing to"); n != 1 {
		t.Errorf("note appeared %d times, want exactly 1", n)
	}
}

// A relative --output is resolved against the cwd and reported absolutely.
func TestDownloadNotesAbsolutePathForRelativeOutput(t *testing.T) {
	home := tempHome(t)
	answers := map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: nitterImagesPage(id100, "AAA1")},
	}
	answers["/pic/orig/AAA1.jpg"] = answer{status: 200, body: "one"}
	fake := newFakeBackend(t, answers)
	writeConfig(t, home, instanceConfig(fake.addr))

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	rel := filepath.ToSlash(filepath.Join("testdata-notes", "out"))
	code, _, errOut := runCLI(t, "download", ref100, "--strategy", "nitter", "--output", rel)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	want := filepath.Join(cwd, "testdata-notes", "out")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(cwd, "testdata-notes")) })
	if !strings.Contains(errOut, want) {
		t.Errorf("stderr = %q, want the absolute path %q", errOut, want)
	}
}
