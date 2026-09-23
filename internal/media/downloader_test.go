package media

// Downloader tests: FetchToFile's filesystem contract (atomic temp-then-
// rename, ErrFileExists vs --force, temp-file residue cleanup) over the real
// httpx transport against httptest fake CDNs. Transport classification
// itself (retry/pacing/kinds, 206/Content-Length verification) is httpx's
// tested contract; these tests pin what the downloader adds or must not
// break: the file lands whole at finalPath, failures leave neither the
// target nor a temp residue behind, and the classified transport error
// surfaces verbatim.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// newTestDownloader builds a Downloader over the real httpx transport with
// retries, backoff and pacing disabled, so downloads against httptest stay
// fast and deterministic (the newProbeHTTPResolver pattern).
func newTestDownloader(t *testing.T) *Downloader {
	t.Helper()
	c, err := httpx.New(httpx.Options{RetryAttempts: -1, RetryDelay: -1, MinInterval: -1})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	return &Downloader{HTTP: c}
}

// payload builds n deterministic pseudo-random bytes (fixed seed) so file
// contents and hashes are stable across runs.
func payload(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.New(rand.NewSource(1)).Read(b)
	return b
}

// leftoverTemps returns the .download-*.tmp residue paths in dir.
func leftoverTemps(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".download-*.tmp"))
	if err != nil {
		t.Fatalf("glob temp residue: %v", err)
	}
	return matches
}

// wantSHA is the lowercase hex digest the test expects for data.
func wantSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// assertClean asserts that a failed FetchToFile left neither the target nor
// temp residue in dir.
func assertClean(t *testing.T, dir, final string) {
	t.Helper()
	if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("final path must not exist after a failed download, stat = %v", err)
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind: %v", n, leftoverTemps(t, dir))
	}
}

func TestFetchToFileWritesHashesAndRenames(t *testing.T) {
	data := payload(256 << 10) // larger than any single write chunk
	var mu sync.Mutex
	var sawUA, sawAccept, sawReferer, sawRange, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawUA, sawAccept, sawReferer = r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Referer")
		sawRange, sawPath = r.Header.Get("Range"), r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	final := filepath.Join(dir, "video.mp4")
	res, err := newTestDownloader(t).FetchToFile(context.Background(), srv.URL+"/video.mp4", final, false)
	if err != nil {
		t.Fatalf("FetchToFile: %v", err)
	}
	if res.URL != srv.URL+"/video.mp4" {
		t.Errorf("URL = %q, want the downloaded URL", res.URL)
	}
	if res.Bytes != int64(len(data)) {
		t.Errorf("Bytes = %d, want %d", res.Bytes, len(data))
	}
	if res.SHA256 != wantSHA(data) {
		t.Errorf("SHA256 = %q, want the streamed digest", res.SHA256)
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("downloaded file differs: %d bytes, want %d", len(got), len(data))
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if sawPath != "/video.mp4" {
		t.Errorf("request path = %q, want /video.mp4", sawPath)
	}
	// The plugin's media-request headers (probeHeaders minus the Range
	// window — a download is a full GET).
	if sawUA != mediaUserAgent || sawAccept != "*/*" || sawReferer != "https://x.com/" {
		t.Errorf("headers = (UA %q, Accept %q, Referer %q), want the plugin media-request set", sawUA, sawAccept, sawReferer)
	}
	if sawRange != "" {
		t.Errorf("Range = %q, want none (a download is a full GET)", sawRange)
	}
}

func TestFetchToFileContentLengthMismatchCleansUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write(payload(16)) // far short of the declaration
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	final := filepath.Join(dir, "video.mp4")
	_, err := newTestDownloader(t).FetchToFile(context.Background(), srv.URL, final, false)
	// Over a real transport the same server lie surfaces one step earlier
	// than httpx's declared-size check: the truncated body read fails with
	// "unexpected EOF" (kind KindUnavailable). The complete-body-short-of-
	// declaration shape (kind KindMalformed) is pinned at the httpx layer
	// against a scripted doer. Either way: failure, temp file removed.
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("error = %v, want kind %q", err, nitter.KindUnavailable)
	}
	assertClean(t, dir, final)
}

func TestFetchToFileExistsWithoutForce(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_, _ = w.Write([]byte("should never be fetched"))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	final := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(final, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	_, err := newTestDownloader(t).FetchToFile(context.Background(), srv.URL, final, false)
	if !errors.Is(err, ErrFileExists) {
		t.Fatalf("error = %v, want it to wrap ErrFileExists", err)
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Errorf("error = %v, want kind %q", err, nitter.KindLocalState)
	}
	if got, rerr := os.ReadFile(final); rerr != nil || string(got) != "keep me" {
		t.Errorf("existing file changed: %q (%v)", got, rerr)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Errorf("requests = %d, want 0 (exists is decided before any network I/O)", requests)
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
}

func TestFetchToFileForceOverwrites(t *testing.T) {
	data := []byte("fresh bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	final := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(final, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	res, err := newTestDownloader(t).FetchToFile(context.Background(), srv.URL, final, true)
	if err != nil {
		t.Fatalf("FetchToFile(force): %v", err)
	}
	if res.Bytes != int64(len(data)) || res.SHA256 != wantSHA(data) {
		t.Errorf("result = (%d, %s), want (%d, %s)", res.Bytes, res.SHA256, len(data), wantSHA(data))
	}
	if got, rerr := os.ReadFile(final); rerr != nil || !bytes.Equal(got, data) {
		t.Errorf("file = %q (%v), want the fresh download", got, rerr)
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
}

func TestFetchToFileSurfacesClassifiedStatuses(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantKind nitter.Kind
	}{
		{"404", http.StatusNotFound, nitter.KindNotFound},
		{"403", http.StatusForbidden, nitter.KindChallenge},
		{"500", http.StatusInternalServerError, nitter.KindUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)

			dir := t.TempDir()
			final := filepath.Join(dir, "x.mp4")
			_, err := newTestDownloader(t).FetchToFile(context.Background(), srv.URL, final, false)
			var terr *nitter.Error
			if !errors.As(err, &terr) || terr.Kind != tc.wantKind {
				t.Fatalf("error = %v, want kind %q (the transport classification verbatim)", err, tc.wantKind)
			}
			assertClean(t, dir, final)
		})
	}
}

func TestFetchToFileChunkedWithoutContentLength(t *testing.T) {
	data := payload(128 << 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		for body := data; len(body) > 0; {
			n := 8192
			if n > len(body) {
				n = len(body)
			}
			_, _ = w.Write(body[:n])
			body = body[n:]
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	final := filepath.Join(dir, "video.mp4")
	res, err := newTestDownloader(t).FetchToFile(context.Background(), srv.URL, final, false)
	if err != nil {
		t.Fatalf("FetchToFile: %v", err)
	}
	if res.Bytes != int64(len(data)) || res.SHA256 != wantSHA(data) {
		t.Errorf("result = (%d, %s), want (%d, %s)", res.Bytes, res.SHA256, len(data), wantSHA(data))
	}
	if got, rerr := os.ReadFile(final); rerr != nil || !bytes.Equal(got, data) {
		t.Errorf("file = %d bytes (%v), want the whole chunked body", len(got), rerr)
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
}

func TestFetchToFileCancelledContextFailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be reached with a cancelled context")
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	final := filepath.Join(dir, "x.mp4")
	_, err := newTestDownloader(t).FetchToFile(ctx, srv.URL, final, false)
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("error = %v, want kind %q", err, nitter.KindUnavailable)
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error %q must mention the context cancellation", err)
	}
	assertClean(t, dir, final)
}

// TestFetchToFileCancelMidStreamCleansUp pins the end-to-end cancellation
// story: a body held open by the fake CDN is aborted by the caller's cancel,
// and the downloader removes its temp file so nothing truncated survives.
func TestFetchToFileCancelMidStreamCleansUp(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done() // hold the body open until the client gives up
	}))
	t.Cleanup(srv.Close)

	d := newTestDownloader(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	dir := t.TempDir()
	final := filepath.Join(dir, "video.mp4")
	go func() {
		_, err := d.FetchToFile(ctx, srv.URL, final, false)
		errCh <- err
	}()
	<-started // the stream has delivered its first bytes and now blocks
	cancel()

	err := <-errCh
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("error = %v, want kind %q", err, nitter.KindUnavailable)
	}
	assertClean(t, dir, final)
}

func TestFetchToFileWithoutTransportIsLocalState(t *testing.T) {
	d := &Downloader{}
	_, err := d.FetchToFile(context.Background(), "https://cdn.test/x.mp4", filepath.Join(t.TempDir(), "x.mp4"), false)
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Fatalf("error = %v, want kind %q", err, nitter.KindLocalState)
	}
}

func TestFetchToFileMissingParentDirIsLocalState(t *testing.T) {
	// No silent directory creation: the command layer owns --output's
	// directory semantics.
	dir := t.TempDir()
	final := filepath.Join(dir, "missing", "x.mp4")
	_, err := newTestDownloader(t).FetchToFile(context.Background(), "https://cdn.test/x.mp4", final, false)
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Fatalf("error = %v, want kind %q", err, nitter.KindLocalState)
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
}

// --- FetchToFileAuto (M9 Task 4, ruling R-M9-4) -----------------------------

// autoSrv serves body under one route with the given Content-Type and
// records every request path. The map is captured by reference so tests can
// fill routes after the server exists.
type autoSrv struct {
	mu        sync.Mutex
	srv       *httptest.Server
	seen      []string
	responses map[string]autoResponse
}

type autoResponse struct {
	body        string
	contentType string
	status      int
}

func (a *autoSrv) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

func newAutoSrv(t *testing.T) *autoSrv {
	t.Helper()
	a := &autoSrv{responses: map[string]autoResponse{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.seen = append(a.seen, r.URL.Path)
		resp, ok := a.responses[r.URL.Path]
		a.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if resp.contentType != "" {
			w.Header().Set("Content-Type", resp.contentType)
		}
		if resp.status != 0 {
			w.WriteHeader(resp.status)
		}
		_, _ = io.WriteString(w, resp.body)
	})
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	return a
}

// existsPathOf extracts the final path a download existence refusal carries
// (the structural accessor the CLI wiring re-exports for the --on-exists
// skip mode).
type existsPathOf interface {
	error
	Path() string
}

func TestFetchToFileAutoDerivesExtensionFromContentType(t *testing.T) {
	// R-M9-4: when the plan carries no extension, the response's
	// Content-Type decides the file extension; the result names the final
	// path it derived.
	for _, tc := range []struct{ contentType, wantExt string }{
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/webp", ".webp"},
		{"image/gif", ".gif"},
		{"video/mp4", ".mp4"},
		{"image/jpeg; charset=binary", ".jpg"}, // parameters stripped
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			srv := newAutoSrv(t)
			srv.responses["/photo"] = autoResponse{body: "bytes", contentType: tc.contentType}
			dir := t.TempDir()
			res, err := newTestDownloader(t).FetchToFileAuto(context.Background(),
				srv.srv.URL+"/photo", filepath.Join(dir, "20700-1"), ".jpg", false)
			if err != nil {
				t.Fatalf("FetchToFileAuto: %v", err)
			}
			wantPath := filepath.Join(dir, "20700-1") + tc.wantExt
			if res.Path != wantPath {
				t.Errorf("Path = %q, want %q", res.Path, wantPath)
			}
			if res.Bytes != 5 || res.SHA256 != wantSHA([]byte("bytes")) || res.URL != srv.srv.URL+"/photo" {
				t.Errorf("result = %+v, want the served bytes hashed", res)
			}
			if got, rerr := os.ReadFile(wantPath); rerr != nil || string(got) != "bytes" {
				t.Errorf("file = %q (%v), want the streamed bytes at the derived path", got, rerr)
			}
			if n := len(leftoverTemps(t, dir)); n != 0 {
				t.Errorf("%d temp file(s) left behind", n)
			}
		})
	}
}

func TestFetchToFileAutoUnknownContentTypeFallsBackToDefault(t *testing.T) {
	// An unlisted Content-Type falls back to the caller's kind-based default
	// (.jpg images/covers, .mp4 videos — the command layer derives it from
	// PlannedFile.Kind). A default without its leading dot is normalized.
	srv := newAutoSrv(t)
	srv.responses["/clip"] = autoResponse{body: "mp4", contentType: "application/octet-stream"}
	dir := t.TempDir()
	res, err := newTestDownloader(t).FetchToFileAuto(context.Background(),
		srv.srv.URL+"/clip", filepath.Join(dir, "20700-1"), ".mp4", false)
	if err != nil {
		t.Fatalf("FetchToFileAuto: %v", err)
	}
	want := filepath.Join(dir, "20700-1.mp4")
	if res.Path != want {
		t.Errorf("Path = %q, want %q", res.Path, want)
	}
	if _, rerr := os.Stat(want); rerr != nil {
		t.Errorf("file missing at the default extension: %v", rerr)
	}

	res, err = newTestDownloader(t).FetchToFileAuto(context.Background(),
		srv.srv.URL+"/clip", filepath.Join(dir, "20700-2"), "mp4", false)
	if err != nil {
		t.Fatalf("FetchToFileAuto (dotless default): %v", err)
	}
	if res.Path != filepath.Join(dir, "20700-2.mp4") {
		t.Errorf("Path = %q, want the dotless default normalized to .mp4", res.Path)
	}
}

func TestFetchToFileAutoNoDefaultExtensionIsLocalState(t *testing.T) {
	// No silent extension-less file: an unknown Content-Type and an empty
	// default cannot name the file, so the call fails.
	srv := newAutoSrv(t)
	srv.responses["/x"] = autoResponse{body: "mystery", contentType: "weird/type"}
	dir := t.TempDir()
	_, err := newTestDownloader(t).FetchToFileAuto(context.Background(),
		srv.srv.URL+"/x", filepath.Join(dir, "20700-1"), "", false)
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Fatalf("error = %v (%T), want kind %q", err, err, nitter.KindLocalState)
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
}

func TestFetchToFileAutoRefusesExistingWithoutForce(t *testing.T) {
	// The derived final path exists and force is false: the refusal wraps
	// ErrFileExists (kind local_state) and CARRIES the derived path — the
	// only place it exists, since the Content-Type decided it. The temp
	// residue is removed and the existing file is untouched. Per R-M9-4 the
	// extension is only knowable from the response, so the download itself
	// has happened by then.
	srv := newAutoSrv(t)
	srv.responses["/photo"] = autoResponse{body: "fresh", contentType: "image/jpeg"}
	dir := t.TempDir()
	base := filepath.Join(dir, "20700-1")
	existing := base + ".jpg"
	if err := os.WriteFile(existing, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	_, err := newTestDownloader(t).FetchToFileAuto(context.Background(), srv.srv.URL+"/photo", base, ".jpg", false)
	if !errors.Is(err, ErrFileExists) {
		t.Fatalf("error = %v, want it to wrap ErrFileExists", err)
	}
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Errorf("error = %v, want kind %q", err, nitter.KindLocalState)
	}
	var pe existsPathOf
	if !errors.As(err, &pe) || pe.Path() != existing {
		t.Errorf("error = %v, want it to carry the derived path %q", err, existing)
	}
	if got, rerr := os.ReadFile(existing); rerr != nil || string(got) != "keep me" {
		t.Errorf("existing file changed: %q (%v)", got, rerr)
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
	if got := srv.requests(); len(got) != 1 {
		t.Errorf("requests = %v, want exactly the one download (the extension needs the response)", got)
	}
}

func TestFetchToFileAutoForceOverwrites(t *testing.T) {
	srv := newAutoSrv(t)
	srv.responses["/photo"] = autoResponse{body: "fresh bytes", contentType: "image/png"}
	dir := t.TempDir()
	final := filepath.Join(dir, "20700-1.png")
	if err := os.WriteFile(final, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	res, err := newTestDownloader(t).FetchToFileAuto(context.Background(), srv.srv.URL+"/photo", filepath.Join(dir, "20700-1"), ".png", true)
	if err != nil {
		t.Fatalf("FetchToFileAuto(force): %v", err)
	}
	if got, rerr := os.ReadFile(final); rerr != nil || string(got) != "fresh bytes" {
		t.Errorf("file = %q (%v), want the overwrite", got, rerr)
	}
	if res.Bytes != int64(len("fresh bytes")) {
		t.Errorf("Bytes = %d, want %d", res.Bytes, len("fresh bytes"))
	}
	if n := len(leftoverTemps(t, dir)); n != 0 {
		t.Errorf("%d temp file(s) left behind", n)
	}
}

func TestFetchToFileExistsErrorCarriesPath(t *testing.T) {
	// The plain FetchToFile existence refusal upgrades to the path-carrying
	// error too (same wire text, structured path): the skip mode needs it
	// without parsing error strings.
	srv := newAutoSrv(t)
	dir := t.TempDir()
	final := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(final, []byte("keep"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := newTestDownloader(t).FetchToFile(context.Background(), srv.srv.URL+"/x", final, false)
	if !errors.Is(err, ErrFileExists) {
		t.Fatalf("error = %v, want ErrFileExists", err)
	}
	var pe existsPathOf
	if !errors.As(err, &pe) || pe.Path() != final {
		t.Errorf("error = %v, want it to carry path %q", err, final)
	}
	if n := len(srv.requests()); n != 0 {
		t.Errorf("requests = %d, want 0 (exists is decided before any network I/O)", n)
	}
}

// failWriter always fails its write, standing in for a full disk.
type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

// A sink write failure must be reclassified as a local-state error and must
// keep the sink's cause: io.Copy cannot tell a write failure from a read
// failure, so the guard is what makes the distinction (T1-7).
func TestStreamToClassifiesSinkWriteFailureAsLocalState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload(4096))
	}))
	t.Cleanup(srv.Close)

	sinkErr := errors.New("no space left on device")
	_, _, err := newTestDownloader(t).streamTo(context.Background(), srv.URL, failWriter{err: sinkErr})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Fatalf("error = %v, want kind %q", err, nitter.KindLocalState)
	}
	if !errors.Is(err, sinkErr) {
		t.Errorf("error must keep the sink cause, got %v", err)
	}
}

// The transport's own classification must survive the guard untouched.
func TestStreamToKeepsTransportClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, _, err := newTestDownloader(t).streamTo(context.Background(), srv.URL, io.Discard)
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("error = %v, want kind %q", err, nitter.KindUnavailable)
	}
}
