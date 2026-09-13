package download

// Injected-capability tests for the paths the real full chain cannot reach
// over httptest: the third-party strategies drop plain-http links, so the
// fallback loop, the cover filename shape and the Content-Type-derived
// extension are exercised here with faked resolve/plan capabilities. The
// downloader is the REAL wiring capability (client.Build -> Wiring.Downloader,
// the same production adapter the command consumes), so the fetch loop runs
// over the real transport against httptest.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
)

const (
	testRef = "https://x.com/nasa/status/2070000000000000100"
	testID  = "2070000000000000100"
)

// fakeResolver stands in for client.MediaResolver and records the strategy
// list the command passed (the auto-chain expansion contract).
type fakeResolver struct {
	res        []nitter.MediaResolution
	err        error
	mu         sync.Mutex
	strategies []string
}

func (f *fakeResolver) ResolveMedia(ctx context.Context, id, user string, strategies []string, quality string) ([]nitter.MediaResolution, error) {
	f.mu.Lock()
	f.strategies = append([]string(nil), strategies...)
	f.mu.Unlock()
	return f.res, f.err
}

func (f *fakeResolver) ProbeMedia(ctx context.Context, mediaURL string) (float64, int64, error) {
	return 0, 0, nil
}

// fakePlanner stands in for client.DownloadPlanner with a canned plan.
type fakePlanner struct {
	plan []client.PlannedFile
	err  error
}

func (f fakePlanner) PlanDownload(refID string, res []nitter.MediaResolution, quality, kindFilter string) ([]client.PlannedFile, error) {
	return f.plan, f.err
}

// batchTest is one runBatch invocation harness: faked resolver/planner, the
// real wiring downloader, text-mode streams and a fresh output directory.
type batchTest struct {
	t      *testing.T
	outDir string
	opts   options
	caps   capabilities

	out, errOut strings.Builder
}

func newBatchTest(t *testing.T) *batchTest {
	t.Helper()
	// The wiring downloader needs a real transport; retries, backoff and
	// pacing are disabled so httptest stays fast (the negative tuning knobs
	// settings.Load would also accept, fed straight to client.Build).
	cfg := settings.Settings{RetryDelay: "-1s", RequestInterval: "-1s", InstanceCooldown: "1s", RetryAttempts: -1}
	w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
	if err != nil {
		t.Fatalf("client.Build: %v", err)
	}
	return &batchTest{
		t:      t,
		outDir: t.TempDir(),
		opts:   options{strategy: "fx", quality: "high"},
		caps: capabilities{
			resolver:   &fakeResolver{res: []nitter.MediaResolution{{Kind: "video", Source: "fx"}}},
			downloader: w.Downloader(),
		},
	}
}

// run executes runBatch in text mode over one ref with the given plan.
func (bt *batchTest) run(plan []client.PlannedFile) error {
	bt.t.Helper()
	bt.caps.planner = fakePlanner{plan: plan}
	s := &invocation.Streams{
		Out:         &bt.out,
		Err:         &bt.errOut,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{},
	}
	pairs := []statusRef{{raw: testRef, id: testID, user: "nasa"}}
	return runBatch(s, pipeline.ModeText, pairs, &bt.opts, bt.outDir, bt.caps)
}

func (bt *batchTest) stdout() string { return bt.out.String() }
func (bt *batchTest) stderr() string { return bt.errOut.String() }

// wantRow builds the expected single-file text row.
func (bt *batchTest) wantRow(name string, size int64, kind string) string {
	return strings.Join([]string{testRef, filepath.Join(bt.outDir, name), fmt.Sprint(size), kind, "fx"}, "\t")
}

// recordingSrv serves canned answers by exact path and records every request.
type recordingSrv struct {
	mu      sync.Mutex
	seen    []string
	srv     *httptest.Server
	addr    string
	answers map[string]srvAnswer
}

type srvAnswer struct {
	status      int
	body        string
	contentType string
}

func (r *recordingSrv) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func newRecordingSrv(t *testing.T, answers map[string]srvAnswer) *recordingSrv {
	t.Helper()
	r := &recordingSrv{answers: answers}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.seen = append(r.seen, req.URL.Path)
		a, ok := r.answers[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if a.contentType != "" {
			w.Header().Set("Content-Type", a.contentType)
		}
		w.WriteHeader(a.status)
		_, _ = fmt.Fprint(w, a.body)
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	r.addr = r.srv.URL
	return r
}

// TestRunBatchFallsThroughToFallbacks pins the fallback loop: the plan's URL
// 404s, the fallbacks are tried in order, the first success lands on the
// planned filename and the row reports the winning URL's bytes.
func TestRunBatchFallsThroughToFallbacks(t *testing.T) {
	srv := newRecordingSrv(t, map[string]srvAnswer{
		"/a.mp4": {status: 404, body: "gone"},
		"/b.mp4": {status: 200, body: "bbb", contentType: "video/mp4"},
	})
	bt := newBatchTest(t)
	plan := []client.PlannedFile{{
		StatusID:  testID,
		Seq:       "1",
		Kind:      "video",
		Ext:       ".mp4",
		URL:       srv.addr + "/a.mp4",
		Fallbacks: []string{srv.addr + "/b.mp4"},
	}}
	if err := bt.run(plan); err != nil {
		t.Fatalf("runBatch: %v", err)
	}
	wantPath := filepath.Join(bt.outDir, testID+"-1.mp4")
	if got, rerr := os.ReadFile(wantPath); rerr != nil || string(got) != "bbb" {
		t.Errorf("file = %q (%v), want the fallback's bytes at %s", got, rerr, wantPath)
	}
	if want := bt.wantRow(testID+"-1.mp4", 3, "video") + "\n"; bt.stdout() != want {
		t.Errorf("stdout = %q, want %q", bt.stdout(), want)
	}
	if got := srv.requests(); len(got) != 2 || got[0] != "/a.mp4" || got[1] != "/b.mp4" {
		t.Errorf("requests = %v, want /a.mp4 then the /b.mp4 fallback", got)
	}
}

// TestRunBatchAllCandidatesFailCountsRef pins the partial-failure shape at
// the batch level: every candidate failing is the ref's in-place error, the
// summary names it, and the error is a plain one (exit 1, not usage).
func TestRunBatchAllCandidatesFailCountsRef(t *testing.T) {
	srv := newRecordingSrv(t, map[string]srvAnswer{
		"/a.mp4": {status: 404, body: "gone"},
	})
	bt := newBatchTest(t)
	plan := []client.PlannedFile{{
		StatusID: testID,
		Seq:      "1",
		Kind:     "video",
		Ext:      ".mp4",
		URL:      srv.addr + "/a.mp4",
	}}
	err := bt.run(plan)
	if err == nil {
		t.Fatal("runBatch = nil error, want the ref failure summary")
	}
	var ue *invocation.UsageError
	if errors.As(err, &ue) {
		t.Fatalf("err = %v (%T), want a plain error (exit 1)", err, err)
	}
	if !strings.Contains(err.Error(), "download completed with 1 of 1 refs failed") {
		t.Errorf("err = %v, want the batch summary", err)
	}
	if !strings.Contains(bt.stderr(), "error: "+testRef) {
		t.Errorf("stderr = %q, want the in-place error line", bt.stderr())
	}
	if _, serr := os.Stat(filepath.Join(bt.outDir, testID+"-1.mp4")); !os.IsNotExist(serr) {
		t.Errorf("failed download must leave no file, stat = %v", serr)
	}
}

// TestRunBatchNamesCoverFile pins the cover filename shape
// <status-id>-cover.<ext> (no seq segment).
func TestRunBatchNamesCoverFile(t *testing.T) {
	srv := newRecordingSrv(t, map[string]srvAnswer{
		"/cover.jpg": {status: 200, body: "cover", contentType: "image/jpeg"},
	})
	bt := newBatchTest(t)
	bt.opts.strategy = "xdown"
	bt.caps.resolver = &fakeResolver{res: []nitter.MediaResolution{{Kind: "video", Source: "xdown"}}}
	plan := []client.PlannedFile{{
		StatusID: testID,
		Seq:      "1",
		Kind:     "cover",
		Ext:      ".jpg",
		URL:      srv.addr + "/cover.jpg",
	}}
	if err := bt.run(plan); err != nil {
		t.Fatalf("runBatch: %v", err)
	}
	wantPath := filepath.Join(bt.outDir, testID+"-cover.jpg")
	if got, rerr := os.ReadFile(wantPath); rerr != nil || string(got) != "cover" {
		t.Errorf("file = %q (%v), want the cover bytes at %s", got, rerr, wantPath)
	}
	want := strings.Join([]string{testRef, wantPath, "5", "cover", "xdown"}, "\t") + "\n"
	if bt.stdout() != want {
		t.Errorf("stdout = %q, want %q", bt.stdout(), want)
	}
}

// TestRunBatchDerivesExtensionFromContentType pins the auto-extension path:
// a plan without Ext lands on the response's Content-Type-derived extension.
func TestRunBatchDerivesExtensionFromContentType(t *testing.T) {
	srv := newRecordingSrv(t, map[string]srvAnswer{
		"/photo": {status: 200, body: "bytes", contentType: "image/png"},
	})
	bt := newBatchTest(t)
	plan := []client.PlannedFile{{
		StatusID: testID,
		Seq:      "1",
		Kind:     "image",
		Ext:      "", // the URL carries no extension
		URL:      srv.addr + "/photo",
	}}
	if err := bt.run(plan); err != nil {
		t.Fatalf("runBatch: %v", err)
	}
	wantPath := filepath.Join(bt.outDir, testID+"-1.png")
	if got, rerr := os.ReadFile(wantPath); rerr != nil || string(got) != "bytes" {
		t.Errorf("file = %q (%v), want the bytes at the Content-Type-derived %s", got, rerr, wantPath)
	}
}

// TestRunBatchSkipKeepsFileAndReportsDiskSize pins the skip mode's batch
// shape: the existing file is kept, its row reports the on-disk size with
// the (skipped) marker, and no fetch happens for it.
func TestRunBatchSkipKeepsFileAndReportsDiskSize(t *testing.T) {
	srv := newRecordingSrv(t, map[string]srvAnswer{
		"/never.jpg": {status: 500, body: "boom"},
	})
	bt := newBatchTest(t)
	bt.opts.onExists = "skip"
	bt.caps.resolver = &fakeResolver{res: []nitter.MediaResolution{{Kind: "image", Source: "fx"}}}
	existing := filepath.Join(bt.outDir, testID+"-1.jpg")
	if err := os.WriteFile(existing, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	plan := []client.PlannedFile{{
		StatusID: testID,
		Seq:      "1",
		Kind:     "image",
		Ext:      ".jpg",
		URL:      srv.addr + "/never.jpg",
	}}
	if err := bt.run(plan); err != nil {
		t.Fatalf("runBatch: %v", err)
	}
	want := strings.Join([]string{testRef, existing + " (skipped)", "7", "image", "fx"}, "\t") + "\n"
	if bt.stdout() != want {
		t.Errorf("stdout = %q, want %q", bt.stdout(), want)
	}
	if got, rerr := os.ReadFile(existing); rerr != nil || string(got) != "keep me" {
		t.Errorf("file = %q (%v), want it untouched", got, rerr)
	}
	if got := srv.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none (skip never re-downloads)", got)
	}
}

// TestRunBatchPassesStrategyChain pins the --strategy to strategy-list
// expansion the command performs (auto is the media command's chain).
func TestRunBatchPassesStrategyChain(t *testing.T) {
	srv := newRecordingSrv(t, map[string]srvAnswer{
		"/x.mp4": {status: 200, body: "v", contentType: "video/mp4"},
	})
	plan := []client.PlannedFile{{
		StatusID: testID,
		Seq:      "1",
		Kind:     "video",
		Ext:      ".mp4",
		URL:      srv.addr + "/x.mp4",
	}}

	bt := newBatchTest(t)
	bt.opts.strategy = "nitter"
	if err := bt.run(plan); err != nil {
		t.Fatalf("runBatch(nitter): %v", err)
	}
	res := bt.caps.resolver.(*fakeResolver)
	res.mu.Lock()
	got := res.strategies
	res.mu.Unlock()
	if len(got) != 1 || got[0] != "nitter" {
		t.Errorf("explicit strategy: strategies = %v, want [nitter]", got)
	}

	// A fresh harness: the first run's file would refuse a second download.
	bt2 := newBatchTest(t)
	bt2.opts.strategy = "auto"
	if err := bt2.run(plan); err != nil {
		t.Fatalf("runBatch(auto): %v", err)
	}
	res2 := bt2.caps.resolver.(*fakeResolver)
	res2.mu.Lock()
	got = res2.strategies
	res2.mu.Unlock()
	want := []string{"fx", "vx", "syndication", "nitter", "xdown"}
	if len(got) != len(want) {
		t.Fatalf("auto: strategies = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("auto: strategies = %v, want %v", got, want)
		}
	}
}
