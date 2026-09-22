package media_test

// End-to-end tests for `nitter media`: httptest fakes for the fx/vx
// backends and the Nitter instance, temp-HOME config fixtures and cli.Run-
// level exit code assertions, mirroring the get command test style.
//
// The third-party backends are redirected to the fakes through the
// internal/media EndpointOverrides seam. This test FILE is the only place a
// media-command artifact touches internal/media — the command package itself
// stays behind the R11 wiring boundary (client.MediaResolver only).

import (
	"encoding/binary"
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
	"github.com/shitianyaa/nitter-cli/internal/media"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
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
	return runCLIStdin(t, "", args...)
}

func runCLIStdin(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// overrideEndpoints points the fx backend at the fake base and restores the
// seam after the test. The base carries a path prefix so ONE httptest server
// can play the fx backend (the resolver builds <base>/<user>/status/<id>, so
// the prefix keeps the routes distinct).
func overrideEndpoints(t *testing.T, fx, _ string) {
	t.Helper()
	media.EndpointOverrides.Fx = fx
	t.Cleanup(func() {
		media.EndpointOverrides.Fx = ""
	})
}

// fakeBackend serves canned answers keyed by the exact request URI
// (path?query) and records every request target.
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
		_, _ = io.WriteString(w, a.body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.addr = f.srv.URL
	return f
}

const (
	ref100     = "https://x.com/nasa/status/2070000000000000100"
	id100      = "2070000000000000100"
	fxRoute100 = "/fx/nasa/status/" + id100
)

// fxTwoMedia is an fx payload with one photo and one three-variant video
// (bitrates 1e5 / 5e5 / 1e6): the quality tiers map to lo/mid/hi.
const fxTwoMedia = `{"tweet":{"text":"media dump","media":{"all":[` +
	`{"type":"photo","url":"https://pbs.twimg.com/media/Gx1abc.jpg","width":1024,"height":768},` +
	`{"type":"video","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/pl.mp4",` +
	`"variants":[{"url":"https://video.twimg.com/ext_tw_video/100/pu/vid/lo.mp4","bitrate":100000},` +
	`{"url":"https://video.twimg.com/ext_tw_video/100/pu/vid/mid.mp4","bitrate":500000},` +
	`{"url":"https://video.twimg.com/ext_tw_video/100/pu/vid/hi.mp4","bitrate":1000000}]` +
	`}]}}}`

// fxVideo is a photo-less fx payload whose single video resolves to the fake
// server's own /video/exv.mp4 (the --probe tests' target).
func fxVideo(base string) string {
	return `{"tweet":{"text":"one video","media":{"all":[` +
		`{"type":"video","url":"` + base + `/video/exv.mp4",` +
		`"variants":[{"url":"` + base + `/video/exv.mp4","bitrate":500000}]` +
		`}]}}}`
}

// mp4Box assembles one ISO-BMFF box (32-bit size) for the probe head window.
func mp4Box(boxType string, payload []byte) []byte {
	buf := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], uint32(8+len(payload)))
	copy(buf[4:8], boxType)
	return append(buf, payload...)
}

// probeHead builds the bytes the fake /video/exv.mp4 serves: a moov>mvhd
// version-0 movie header with timescale 600 and duration 1920 units → 3.2s.
func probeHead() []byte {
	mvhd := make([]byte, 20)
	mvhd[0] = 0 // version 0
	binary.BigEndian.PutUint32(mvhd[12:16], 600)
	binary.BigEndian.PutUint32(mvhd[16:20], 1920)
	return mp4Box("moov", mp4Box("mvhd", mvhd))
}

func TestMediaAutoChainFallsThroughToNitter(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		fxRoute100: {status: 500, body: "boom"},
		"/nasa/status/" + id100: {status: 200, body: nitterVideoPage(id100)},
	})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "media", ref100, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj nitter.MediaResolution
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if obj.Source != "nitter" || obj.Kind != "video" {
		t.Errorf("resolution = %+v, want the nitter-served video", obj)
	}
	// Ref echoes the raw input (Task 1 review minor #5, Task 4 ruling).
	if obj.Ref != ref100 {
		t.Errorf("Ref = %q, want the raw input ref", obj.Ref)
	}
	// Request order pinned: fx first, then the nitter status page.
	if got := fake.requests(); !slices.Equal(got, []string{fxRoute100, "/nasa/status/" + id100}) {
		t.Errorf("requests = %v, want fx then the nitter status page", got)
	}
}

func TestMediaSingleStrategyRunsOnlyThatOne(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		fxRoute100: {status: 200, body: fxTwoMedia},
	})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	code, out, errOut := runCLI(t, "media", ref100, "--strategy", "fx", "--quality", "medium", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var list []nitter.MediaResolution
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out)
	}
	if len(list) != 2 {
		t.Fatalf("got %d resolutions, want 2 (image + video): %s", len(list), out)
	}
	if list[0].Kind != "image" || !strings.Contains(list[0].URL, "name=") {
		t.Errorf("res[0] = %+v, want the quality-rewritten image", list[0])
	}
	// medium video: the upper-median bitrate becomes the main URL, the
	// highest stays as the fallback, every variant is kept.
	if list[1].URL != "https://video.twimg.com/ext_tw_video/100/pu/vid/mid.mp4" {
		t.Errorf("video URL = %q, want the medium variant", list[1].URL)
	}
	if list[1].FallbackURL != "https://video.twimg.com/ext_tw_video/100/pu/vid/hi.mp4" {
		t.Errorf("fallback = %q, want the highest variant", list[1].FallbackURL)
	}
	if len(list[1].Variants) != 3 {
		t.Errorf("variants = %d, want all three", len(list[1].Variants))
	}
	for _, row := range list {
		if row.Source != "fx" {
			t.Errorf("source = %q, want fx", row.Source)
		}
	}
	if got := fake.requests(); !slices.Equal(got, []string{fxRoute100}) {
		t.Errorf("requests = %v, want the fx route only", got)
	}
}

// nitterVideoPage is a status page whose focused item carries one /video/
// placeholder: the nitter strategy keeps the (plain-http) instance link, and
// that link is the --probe tests' target.
func nitterVideoPage(id string) string {
	return `<div class="timeline"><div class="timeline-item">` +
		`<a class="tweet-link" href="/nasa/status/` + id + `#m"></a>` +
		`<div class="tweet-content">with video</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="attachments"><a class="video-container" href="/video/exv.mp4">video</a></div>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
}

func TestMediaProbeFillsVideoMetadata(t *testing.T) {
	home := tempHome(t)
	// The map is captured by reference, so it can be filled after the server
	// exists (the config needs the instance address first).
	answers := map[string]answer{}
	fake := newFakeBackend(t, answers)
	answers["/nasa/status/"+id100] = answer{status: 200, body: nitterVideoPage(id100)}
	answers["/video/exv.mp4"] = answer{
		status:  http.StatusPartialContent,
		body:    string(probeHead()),
		headers: map[string]string{"Content-Range": "bytes 0-1048575/24000"},
	}
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "media", ref100, "--strategy", "nitter", "--probe", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj nitter.MediaResolution
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if obj.DurationSeconds != 3.2 {
		t.Errorf("duration = %v, want 3.2 from the mvhd probe", obj.DurationSeconds)
	}
	if obj.SizeBytes != 24000 {
		t.Errorf("size = %d, want 24000 from the Content-Range total", obj.SizeBytes)
	}
	if got := fake.requests(); !slices.Equal(got, []string{"/nasa/status/" + id100, "/video/exv.mp4"}) {
		t.Errorf("requests = %v, want the status page then the probe", got)
	}
}

func TestMediaProbeFailureKeepsZerosAndSucceeds(t *testing.T) {
	home := tempHome(t)
	answers := map[string]answer{}
	fake := newFakeBackend(t, answers)
	answers["/nasa/status/"+id100] = answer{status: 200, body: nitterVideoPage(id100)}
	// The probe target is gone: the enrichment fails, the command must still
	// emit the resolution with zero duration/size and exit 0.
	answers["/video/exv.mp4"] = answer{status: 404, body: "gone"}
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "media", ref100, "--strategy", "nitter", "--probe", "--ndjson")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			DurationSeconds float64 `json:"duration_seconds"`
			SizeBytes       int64   `json:"size_bytes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, out)
	}
	if env.Kind != "media" {
		t.Fatalf("kind = %q, want media", env.Kind)
	}
	if env.Data.DurationSeconds != 0 || env.Data.SizeBytes != 0 {
		t.Errorf("probe failure leaked values: %+v", env.Data)
	}
}

func TestMediaBatchPartialFailureEmitsErrorEnvelopeAndExitsOne(t *testing.T) {
	home := tempHome(t)
	refB := "https://x.com/nasa/status/2070000000000000200"
	fake := newFakeBackend(t, map[string]answer{
		fxRoute100:                            {status: 200, body: fxVideo("https://video.twimg.com/x.mp4")},
		"/fx/nasa/status/2070000000000000200": {status: 404, body: "gone"},
	})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	code, out, errOut := runCLI(t, "media", ref100, refB, "--strategy", "fx", "--ndjson")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d envelope lines, want 2 (media then error):\n%s", len(lines), out)
	}
	var mediaEnv struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Data struct {
			Source string `json:"source"`
			URL    string `json:"url"`
		} `json:"data"`
		Meta *struct {
			Input string `json:"input"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &mediaEnv); err != nil {
		t.Fatalf("line 1 is not JSON: %v", err)
	}
	if mediaEnv.Kind != "media" || mediaEnv.Data.Source != "fx" || !strings.HasSuffix(mediaEnv.ID, ".mp4") {
		t.Errorf("line 1 = %s, want the fx media envelope", lines[0])
	}
	if mediaEnv.Meta == nil || mediaEnv.Meta.Input != ref100 {
		t.Errorf("meta.input = %+v, want the raw ref", mediaEnv.Meta)
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
	if errEnv.Kind != "error" || errEnv.Data.Command != "media" || errEnv.Data.Stage != "resolve" {
		t.Errorf("line 2 = %s, want the media resolve error envelope", lines[1])
	}
	if errEnv.Data.Code != "not_found" {
		t.Errorf("code = %q, want the SDK kind", errEnv.Data.Code)
	}
	if errEnv.Meta == nil || errEnv.Meta.Input != refB {
		t.Errorf("meta.input = %+v, want ref B", errEnv.Meta)
	}
	if !strings.Contains(errOut, "media completed with 1 of 2 refs failed") {
		t.Errorf("stderr = %q, want the batch summary", errOut)
	}
}

// TestMediaBatchPipeDefaultStreamsEnvelopes pins the M10 pipe default: with
// stdout not a TTY and no output flag given, the batch streams per-ref
// envelopes (the media record, then the in-place error envelope for the
// failed ref) while the summary stays on stderr. The text-mode rendering of
// this scenario is the TTY default's — see the internal TTY test.
func TestMediaBatchPipeDefaultStreamsEnvelopes(t *testing.T) {
	home := tempHome(t)
	refB := "https://x.com/nasa/status/2070000000000000200"
	fake := newFakeBackend(t, map[string]answer{
		fxRoute100:                            {status: 200, body: fxVideo("https://video.twimg.com/x.mp4")},
		"/fx/nasa/status/2070000000000000200": {status: 500, body: "boom"},
	})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	code, out, errOut := runCLI(t, "media", ref100, refB, "--strategy", "fx")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d envelope lines, want 2 (media then error):\n%s", len(lines), out)
	}
	var mediaEnv struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Data   struct {
			Source string `json:"source"`
			URL    string `json:"url"`
		} `json:"data"`
		Meta *struct {
			Input string `json:"input"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &mediaEnv); err != nil {
		t.Fatalf("line 1 is not JSON: %v", err)
	}
	if mediaEnv.Schema != "nitter.pipeline/v1" || mediaEnv.Kind != "media" || mediaEnv.Data.Source != "fx" || !strings.HasSuffix(mediaEnv.ID, ".mp4") {
		t.Errorf("line 1 = %s, want the fx media envelope", lines[0])
	}
	if mediaEnv.Meta == nil || mediaEnv.Meta.Input != ref100 {
		t.Errorf("meta.input = %+v, want the raw ref", mediaEnv.Meta)
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
	if errEnv.Kind != "error" || errEnv.Data.Command != "media" || errEnv.Data.Stage != "resolve" {
		t.Errorf("line 2 = %s, want the media resolve error envelope", lines[1])
	}
	if !strings.Contains(errOut, "media completed with 1 of 2 refs failed") {
		t.Errorf("stderr = %q, want the batch summary", errOut)
	}
}

func TestMediaNitterStrategyUsesConfiguredInstance(t *testing.T) {
	home := tempHome(t)
	page := `<div class="timeline"><div class="timeline-item">` +
		`<a class="tweet-link" href="/nasa/status/` + id100 + `#m"></a>` +
		`<div class="tweet-content">with media</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="attachments">` +
		`<a class="still-image" href="/pic/orig/media%2FAAA.jpg">img</a>` +
		`<a class="video-container" href="/video/EXVmp4.mp4">video</a>` +
		`</div>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
	fake := newFakeBackend(t, map[string]answer{
		"/nasa/status/" + id100: {status: 200, body: page},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	code, out, errOut := runCLI(t, "media", ref100, "--strategy", "nitter", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var list []nitter.MediaResolution
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out)
	}
	if len(list) != 2 {
		t.Fatalf("got %d resolutions, want 2: %s", len(list), out)
	}
	if list[0].Source != "nitter" || !strings.HasSuffix(list[0].URL, "/pic/orig/media%2FAAA.jpg") {
		t.Errorf("res[0] = %+v, want the instance pic-proxy image", list[0])
	}
	if list[1].Kind != "video" || list[1].URL != fake.addr+"/video/EXVmp4.mp4" {
		t.Errorf("res[1] = %+v, want the instance /video/ link", list[1])
	}
	if got := fake.requests(); !slices.Equal(got, []string{"/nasa/status/" + id100}) {
		t.Errorf("requests = %v, want the status page only", got)
	}
}

// TestMediaInvalidStrategyAndQualityExit2: an unknown --strategy (vx and
// syndication removed, fx/nitter/xdown remain) and an invalid --quality are
// usage errors (exit 2) before any network.
func TestMediaInvalidStrategyAndQualityExit2(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	for _, args := range [][]string{
		{"media", ref100, "--strategy", "bogus"},
		{"media", ref100, "--quality", "ultra"},
	} {
		code, out, errOut := runCLI(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2 (stderr %q)", args, code, errOut)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q, want nothing", args, out)
		}
	}
	if got := fake.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (validation precedes any network)", got)
	}
}

// TestMediaStrategyRemoved: vx and syndication are no longer valid --strategy
// values — a usage error (exit 2) naming the remaining strategies.
func TestMediaStrategyRemoved(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	for _, name := range []string{"vx", "syndication"} {
		code, _, errOut := runCLI(t, "media", ref100, "--strategy", name)
		if code != 2 {
			t.Errorf("--strategy %s: exit = %d, want 2 (stderr %q)", name, code, errOut)
		}
		for _, want := range []string{"fx", "nitter", "xdown"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("--strategy %s: stderr %q, want it to name %q", name, errOut, want)
			}
		}
	}
	if got := fake.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (validation precedes any network)", got)
	}
}

func TestMediaInvalidRefExit2BeforeNetwork(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	code, out, errOut := runCLI(t, "media", "not-a-ref")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if got := fake.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestMediaStdinRef(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{
		fxRoute100: {status: 200, body: fxVideo("https://video.twimg.com/x.mp4")},
	})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	code, out, errOut := runCLIStdin(t, ref100+"\n", "media", "--strategy", "fx", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj nitter.MediaResolution
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if obj.Ref != ref100 || obj.Source != "fx" {
		t.Errorf("resolution = %+v, want the fx video with the raw ref", obj)
	}
}

func TestMediaRefBothArgAndStdinIsUsageError(t *testing.T) {
	home := tempHome(t)
	fake := newFakeBackend(t, map[string]answer{})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	code, out, _ := runCLIStdin(t, ref100+"\n", "media", id100)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if got := fake.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (ambiguity is rejected before any network)", got)
	}
}

func TestMediaNoRefIsUsageError(t *testing.T) {
	tempHome(t)
	for _, stdin := range []string{"", "\n", "   \n"} {
		code, _, _ := runCLIStdin(t, stdin, "media")
		if code != 2 {
			t.Fatalf("media with stdin %q: exit = %d, want 2", stdin, code)
		}
	}
}

func TestMediaJSONAndNDJSONAreMutuallyExclusive(t *testing.T) {
	tempHome(t)
	code, _, _ := runCLI(t, "media", ref100, "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestMediaJSONCardinality(t *testing.T) {
	home := tempHome(t)
	answers := map[string]answer{}
	fake := newFakeBackend(t, answers)
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	// Two media entries from one ref: an array.
	answers[fxRoute100] = answer{status: 200, body: fxTwoMedia}
	code, out, errOut := runCLI(t, "media", ref100, "--strategy", "fx", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var list []nitter.MediaResolution
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("two-media --json is not an array: %v\n%s", err, out)
	}
	if len(list) != 2 {
		t.Fatalf("got %d entries, want 2", len(list))
	}

	// Exactly one media entry across the whole invocation: a single object.
	answers[fxRoute100] = answer{status: 200, body: fxVideo("https://video.twimg.com/x.mp4")}
	code, out, errOut = runCLI(t, "media", ref100, "--strategy", "fx", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var obj nitter.MediaResolution
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("single-media --json is not one object: %v\n%s", err, out)
	}
	if obj.Source != "fx" || obj.Kind != "video" {
		t.Errorf("object = %+v, want the fx video", obj)
	}
}
