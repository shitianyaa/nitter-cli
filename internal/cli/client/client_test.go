package client_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
	"github.com/shitianyaa/nitter-cli/internal/media"
	"github.com/shitianyaa/nitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// validCfg returns Settings with valid duration strings (as settings.Load
// would produce). Build is deliberately strict about unvalidated settings.
// FetchBackend is the internal instance-only mode: config files can no longer
// set "nitter" (only mix and fx are accepted there), but Build still takes it,
// because that is what --instance selects for one invocation. These are unit
// tests of the dispatcher, so they exercise it directly.
func validCfg() settings.Settings {
	return settings.Settings{RetryDelay: "1s", RequestInterval: "1s", InstanceCooldown: "1s", FetchBackend: "nitter"}
}

func TestBuildWiresParts(t *testing.T) {
	cfg := validCfg()
	cfg.Instances = []settings.Instance{{URL: "http://a", Username: "u", Password: "p"}}
	cfg.RetryAttempts = 1
	now := func() time.Time { return time.Unix(0, 0) }
	w, err := client.Build(&invocation.RootOptions{}, cfg, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := any(w.Transport).(*httpx.Client); !ok {
		t.Fatalf("transport is %T, want *httpx.Client", w.Transport)
	}
	if w.AppAPI == nil || w.AppAPI.HTTP != w.Transport {
		t.Fatal("appapi client is not wired to the built transport")
	}
	if w.Chooser == nil || w.AppAPI.Chooser != w.Chooser {
		t.Fatal("chooser is not shared between the wiring and the appapi client")
	}
	if w.AppAPI.Now().UnixNano() != 0 {
		t.Fatalf("clock was not wired through (Now() = %v)", w.AppAPI.Now())
	}
	if len(w.Instances) != 1 || w.Instances[0].URL != "http://a" || w.Instances[0].Username != "u" || w.Instances[0].Password != "p" {
		t.Fatalf("instances = %+v, want the config projection", w.Instances)
	}
	tester := w.Tester()
	if tester == nil {
		t.Fatal("Tester() returned nil, want the probe capability")
	}
}

func TestBuildInstancesKeepConfigOrderWithoutFlag(t *testing.T) {
	cfg := validCfg()
	cfg.Instances = []settings.Instance{{URL: "http://a"}, {URL: "http://b"}}
	w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(w.Instances) != 2 || w.Instances[0].URL != "http://a" || w.Instances[1].URL != "http://b" {
		t.Fatalf("instances = %+v, want config order preserved", w.Instances)
	}
}

func TestBuildInstanceFlagOverridesConfigInstances(t *testing.T) {
	cfg := validCfg()
	cfg.Instances = []settings.Instance{{URL: "http://a"}, {URL: "http://b"}}
	w, err := client.Build(&invocation.RootOptions{Instance: "http://override"}, cfg, time.Now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(w.Instances) != 1 || w.Instances[0].URL != "http://override" {
		t.Fatalf("instances = %+v, want the --instance override alone", w.Instances)
	}
}

func TestBuildRejectsInvalidProxyScheme(t *testing.T) {
	for _, tc := range []struct {
		name, flagProxy, cfgProxy, wantScheme string
	}{
		{"flag proxy", "ftp://proxy:1", "", "ftp"},
		{"config proxy", "", "socks4://1.2.3.4", "socks4"},
		{"flag wins over config", "socks4://x", "ftp://y", "socks4"},
	} {
		cfg := validCfg()
		cfg.Proxy = tc.cfgProxy
		w, err := client.Build(&invocation.RootOptions{Proxy: tc.flagProxy}, cfg, time.Now)
		if err == nil || w != nil {
			t.Fatalf("%s: Build = (%v, %v), want error and no state", tc.name, w, err)
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
			t.Fatalf("%s: err = %v (%T), want *nitter.Error KindInvalidArg", tc.name, err, err)
		}
		// Naming the scheme proves which source (flag vs config) was used —
		// and the message must not echo the full proxy URL (credentials).
		if !strings.Contains(err.Error(), tc.wantScheme) || strings.Contains(err.Error(), "://") {
			t.Fatalf("%s: err = %v, want the losing scheme named and no URL echoed", tc.name, err)
		}
	}
}

func TestBuildAcceptsSupportedProxySchemes(t *testing.T) {
	for _, proxy := range []string{"", "http://p:1", "https://p:1", "socks5://p:1", "socks5h://p:1"} {
		w, err := client.Build(&invocation.RootOptions{Proxy: proxy}, validCfg(), time.Now)
		if err != nil || w == nil {
			t.Fatalf("proxy %q: Build = (%v, %v), want success", proxy, w, err)
		}
	}
}

func TestBuildInvalidConfigDurationsArePlainErrors(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"retry_delay", "abc"},
		{"request_interval", "xyz"},
		{"instance_cooldown", "soon"},
	} {
		cfg := validCfg()
		switch tc.key {
		case "retry_delay":
			cfg.RetryDelay = tc.value
		case "request_interval":
			cfg.RequestInterval = tc.value
		case "instance_cooldown":
			cfg.InstanceCooldown = tc.value
		}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err == nil || w != nil {
			t.Fatalf("%s: Build = (%v, %v), want error", tc.key, w, err)
		}
		var ue *invocation.UsageError
		var terr *nitter.Error
		if errors.As(err, &ue) || errors.As(err, &terr) {
			t.Fatalf("%s: err = %v (%T), want a plain error (settings.Load already validates these; exit 1 path)", tc.key, err, err)
		}
		if !strings.Contains(err.Error(), tc.key) {
			t.Fatalf("%s: err = %v, want it to name the key", tc.key, err)
		}
	}
}

func TestBuildUnvalidatedZeroSettingsIsAPlainError(t *testing.T) {
	// A zero-value Settings never went through settings.Load; Build must not
	// silently substitute defaults for the empty duration strings.
	w, err := client.Build(&invocation.RootOptions{}, settings.Settings{}, time.Now)
	if err == nil || w != nil {
		t.Fatalf("Build(zero Settings) = (%v, %v), want a plain error", w, err)
	}
}

func TestBuildNilArguments(t *testing.T) {
	w, err := client.Build(nil, validCfg(), nil)
	if err != nil {
		t.Fatalf("Build(nil, cfg, nil): %v", err)
	}
	if delta := time.Since(w.AppAPI.Now()); delta > 5*time.Second || delta < -5*time.Second {
		t.Fatalf("nil now did not default to the real clock (drift %v)", delta)
	}
}

func TestAsUsageErrorMapsOnlyInvalidArg(t *testing.T) {
	ue := client.AsUsageError(nitter.Errorf(nitter.KindInvalidArg, "op", "bad input"))
	var got *invocation.UsageError
	if !errors.As(ue, &got) {
		t.Fatalf("AsUsageError(KindInvalidArg) = %v (%T), want *invocation.UsageError", ue, ue)
	}
	down := nitter.Errorf(nitter.KindUnavailable, "op", "down")
	if mapped := client.AsUsageError(down); mapped != down {
		t.Fatalf("AsUsageError(KindUnavailable) rewrote the error: %v", mapped)
	}
	plain := errors.New("boom")
	if mapped := client.AsUsageError(plain); mapped != plain {
		t.Fatalf("AsUsageError(plain) rewrote the error: %v", mapped)
	}
}

// TestTimelineCapability passes through the wiring: the narrow
// TimelineSource is exposed, primitive options flow into the appapi method,
// and errors surface unchanged (an invalid handle is KindInvalidArg; a valid
// one with nothing configured is the chooser's KindUnavailable).
func TestTimelineCapability(t *testing.T) {
	w, err := client.Build(&invocation.RootOptions{}, validCfg(), time.Now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var src client.TimelineSource = w.Timeline()
	if src == nil {
		t.Fatal("Timeline() returned nil")
	}
	_, _, err = src.Timeline(context.Background(), "NASA!", 1, 1)
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
		t.Fatalf("Timeline(NASA!) = %v (%T), want KindInvalidArg", err, err)
	}
	_, _, err = src.Timeline(context.Background(), "NASA", 1, 1)
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("Timeline(NASA) with no instances = %v (%T), want KindUnavailable", err, err)
	}
}

func TestLoadEffectiveSettings(t *testing.T) {
	t.Run("fresh home yields defaults", func(t *testing.T) {
		neutralizeEnv(t)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("USERPROFILE", os.Getenv("HOME"))
		cfg, err := client.LoadEffectiveSettings()
		if err != nil {
			t.Fatalf("LoadEffectiveSettings: %v", err)
		}
		if cfg.DefaultLimit != 20 || cfg.RequestInterval != "1s" || cfg.FetchBackend != "mix" {
			t.Fatalf("cfg = %+v, want defaults", cfg)
		}
	})
	t.Run("validation error maps to usage error", func(t *testing.T) {
		neutralizeEnv(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		dir := filepath.Join(home, ".nitter-cli")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("request_interval = \"abc\"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		_, err := client.LoadEffectiveSettings()
		var ue *invocation.UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("err = %v (%T), want *invocation.UsageError", err, err)
		}
		if !strings.Contains(err.Error(), "request_interval") {
			t.Fatalf("err = %v, want it to name the offending key", err)
		}
	})
	t.Run("invalid fetch_backend maps to usage error", func(t *testing.T) {
		neutralizeEnv(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		dir := filepath.Join(home, ".nitter-cli")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("fetch_backend = \"bogus\"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		_, err := client.LoadEffectiveSettings()
		var ue *invocation.UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("err = %v (%T), want *invocation.UsageError", err, err)
		}
		if !strings.Contains(err.Error(), "fetch_backend") {
			t.Fatalf("err = %v, want it to name the offending key", err)
		}
	})
}

// neutralizeEnv clears the settings and proxy env overrides so the file and
// default layers are observable in isolation.
func neutralizeEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"NITTER_DEFAULT_LIMIT", "NITTER_LOG_LEVEL", "NITTER_LOG_FORMAT", "NITTER_FETCH_BACKEND",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, "")
	}
}

// TestDownloadCapability passes the download capabilities through the wiring:
// MediaDownloader streams one URL to disk over the shared transport and
// reports the final path, and DownloadPlanner projects a resolution into its
// plan (R11: the command packages consume both through these interfaces
// only, so the tests exercise them exactly as the commands will).
func TestDownloadCapability(t *testing.T) {
	body := "video-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	w, err := client.Build(&invocation.RootOptions{}, validCfg(), time.Now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	final := filepath.Join(t.TempDir(), "100-1.mp4")
	rec, err := w.Downloader().FetchToFile(context.Background(), srv.URL+"/x.mp4", final, false)
	if err != nil {
		t.Fatalf("Downloader().FetchToFile: %v", err)
	}
	if rec.Path != final || rec.URL != srv.URL+"/x.mp4" || rec.Bytes != int64(len(body)) {
		t.Errorf("record = %+v, want the final path, the served URL and %d bytes", rec, len(body))
	}
	sum := sha256.Sum256([]byte(body))
	if rec.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %q, want the streamed digest", rec.SHA256)
	}
	if got, rerr := os.ReadFile(final); rerr != nil || string(got) != body {
		t.Errorf("file = %q (%v), want the streamed bytes", got, rerr)
	}

	plan, err := w.Planner().PlanDownload("100", []nitter.MediaResolution{
		{Kind: "video", URL: "https://video.twimg.com/x.mp4"},
	}, "high", "")
	if err != nil {
		t.Fatalf("Planner().PlanDownload: %v", err)
	}
	if len(plan) != 1 || plan[0].StatusID != "100" || plan[0].Seq != "1" ||
		plan[0].Kind != "video" || plan[0].Ext != ".mp4" || plan[0].URL != "https://video.twimg.com/x.mp4" {
		t.Errorf("plan = %+v, want the single converged video file", plan)
	}
}

// TestDownloaderExistsErrorCarriesPath pins the skip mode's seam: the
// existence refusal wraps the re-exported ErrFileExists and carries the
// already-existing final path (the auto-extension path derives it from the
// response's Content-Type, so only the downloader knows it).
func TestDownloaderExistsErrorCarriesPath(t *testing.T) {
	w, err := client.Build(&invocation.RootOptions{}, validCfg(), time.Now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	final := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(final, []byte("keep"), 0o600); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	_, err = w.Downloader().FetchToFile(context.Background(), "https://cdn.test/x.jpg", final, false)
	if !errors.Is(err, client.ErrFileExists) {
		t.Fatalf("error = %v, want it to wrap client.ErrFileExists", err)
	}
	if p, ok := client.ExistsPath(err); !ok || p != final {
		t.Errorf("ExistsPath = (%q, %v), want (%q, true)", p, ok, final)
	}
}

// ---------------------------------------------------------------------------
// Instance basic auth (host-scoped transport policy). The wiring maps the
// [[instances]] username/password settings into the shared transport
// (httpx.Options.BasicAuth, keyed by URL host): instance-host fetches carry
// the Authorization header, third-party media endpoints fetched through the
// SAME transport structurally cannot. See httpx/auth.go for the contract.
// ---------------------------------------------------------------------------

// basicAuthRSSFeed is a Nitter-shaped RSS feed whose single item projects
// into one tweet for the handle "user" (keeps Timeline on the RSS path).
const basicAuthRSSFeed = `<?xml version="1.0"?><rss version="2.0"><channel>` +
	`<item><guid>https://nitter.example/user/status/100#m</guid>` +
	`<link>https://nitter.example/user/status/100</link>` +
	`<dc:creator>@user</dc:creator><title>one</title>` +
	`<pubDate>Sun, 05 Jul 2026 09:09:40 +0000</pubDate></item></channel></rss>`

// fxOnePhotoPayload is a minimal fxtwitter envelope with one photo entry.
const fxOnePhotoPayload = `{"tweet":{"media":{"all":[` +
	`{"type":"photo","url":"https://pbs.twimg.com/media/x.jpg","width":1,"height":1}]}}}`

// newRecordingServer serves one handler and records the Authorization header
// of every request it receives.
func newRecordingServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request), auth *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*auth = append(*auth, r.Header.Get("Authorization"))
		handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fastCfg returns settings like validCfg but with pacing disabled: the
// basic-auth tests drive several requests through one transport and need no
// 1s spacing between them.
func fastCfg() settings.Settings {
	cfg := validCfg()
	cfg.RequestInterval = "-1s"
	return cfg
}

// TestBuildWiresInstanceBasicAuth: with credentials configured for the
// instance, the instance-host fetch (Timeline) and the instance probe
// (TestInstance) carry the correctly encoded Authorization header, while the
// fxtwitter strategy resolved through the SAME transport never does.
func TestBuildWiresInstanceBasicAuth(t *testing.T) {
	var instAuth, fxAuth []string
	inst := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rss") {
			_, _ = io.WriteString(w, basicAuthRSSFeed)
			return
		}
		http.NotFound(w, r)
	}, &instAuth)
	fx := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fxOnePhotoPayload)
	}, &fxAuth)

	media.EndpointOverrides.Fx = fx.URL
	t.Cleanup(func() { media.EndpointOverrides.Fx = "" })

	cfg := fastCfg()
	cfg.Instances = []settings.Instance{{URL: inst.URL, Username: "user", Password: "p"}}
	w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:p"))
	ctx := context.Background()

	tweets, _, err := w.Timeline().Timeline(ctx, "user", 5, 5)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("tweets = %d, want 1 (RSS path)", len(tweets))
	}

	report, err := w.Tester().TestInstance(ctx, inst.URL, client.TestOptions{User: "user"})
	if err != nil {
		t.Fatalf("TestInstance: %v", err)
	}
	if !report.RSS.OK {
		t.Fatalf("probe RSS = %+v, want OK", report.RSS)
	}

	res, err := w.Media().ResolveMedia(ctx, "100", "user", []string{"fx"}, "high")
	if err != nil {
		t.Fatalf("ResolveMedia: %v", err)
	}
	if len(res) != 1 || res[0].URL != "https://pbs.twimg.com/media/x.jpg?name=orig" {
		t.Fatalf("resolutions = %+v, want the single fx photo", res)
	}

	if len(instAuth) != 3 {
		t.Fatalf("instance fake saw %d request(s) with Authorization recorded %v, want 3 (timeline rss + probe rss + probe html)", len(instAuth), instAuth)
	}
	for i, got := range instAuth {
		if got != want {
			t.Errorf("instance request %d Authorization = %q, want %q", i, got, want)
		}
	}
	if len(fxAuth) == 0 {
		t.Fatal("fxtwitter fake saw no request")
	}
	for i, got := range fxAuth {
		if got != "" {
			t.Errorf("fxtwitter request %d carried Authorization %q, want none (third-party host)", i, got)
		}
	}
}

// TestBuildWithoutCredentialsSendsNoAuth: with no credentials at all, or an
// incomplete pair (username only, password only — both halves are required,
// an empty credential half is never sent), instance fetches run
// unauthenticated exactly as before.
func TestBuildWithoutCredentialsSendsNoAuth(t *testing.T) {
	cases := []settings.Instance{
		{},
		{Username: "user"},
		{Password: "p"},
	}
	for i, in := range cases {
		var auth []string
		inst := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, basicAuthRSSFeed)
		}, &auth)

		in.URL = inst.URL
		cfg := fastCfg()
		cfg.Instances = []settings.Instance{in}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("case %d Build: %v", i, err)
		}
		tweets, _, err := w.Timeline().Timeline(context.Background(), "user", 5, 5)
		if err != nil {
			t.Fatalf("case %d Timeline: %v", i, err)
		}
		if len(tweets) != 1 {
			t.Fatalf("case %d tweets = %d, want 1", i, len(tweets))
		}
		if len(auth) != 1 || auth[0] != "" {
			t.Errorf("case %d instance Authorization = %v, want one request with no header", i, auth)
		}
		inst.Close()
	}
}

func TestHybridTimelineAndSearchDispatch(t *testing.T) {
	fxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/statuses"):
			if strings.Contains(r.URL.Path, "failuser") {
				http.Error(w, `{"code":404}`, http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"code":200,"results":[{"id":"111","url":"https://x.com/user/status/111","text":"from fx","author":{"screen_name":"user"}}]}`)
		case strings.Contains(r.URL.Path, "/search"):
			if strings.Contains(r.URL.RawQuery, "failquery") {
				http.Error(w, `{"code":404}`, http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"code":200,"results":[{"id":"222","url":"https://x.com/user/status/222","text":"search fx","author":{"screen_name":"user"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fxSrv.Close()

	nitterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/rss"):
			_, _ = io.WriteString(w, basicAuthRSSFeed)
		case strings.Contains(r.URL.Path, "/search"):
			_, _ = io.WriteString(w, `<div class="timeline"><div class="timeline-item"><a class="tweet-link" href="/user/status/333"></a><div class="tweet-content">from nitter search</div></div></div>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer nitterSrv.Close()

	fxtwitter.EndpointOverrides.BaseURL = fxSrv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	t.Run("mix mode uses Fx first", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		tweets, inst, err := w.Timeline().Timeline(context.Background(), "user", 5, 5)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if inst != "FxTwitter" || len(tweets) != 1 || tweets[0].ID != "111" {
			t.Errorf("got inst=%q tweets=%+v, want FxTwitter and tweet 111", inst, tweets)
		}
	})

	// There is no limit-zero case here any more: 0 is a usage error at the flag
	// layer and the Fx client rejects a non-positive count, so the wiring can
	// never be asked to translate it.

	t.Run("mix mode falls back to Nitter on Fx error", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		tweets, inst, err := w.Timeline().Timeline(context.Background(), "failuser", 5, 5)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if inst != nitterSrv.URL || len(tweets) != 1 {
			t.Errorf("got inst=%q, want nitter fallback URL", inst)
		}
	})

	// --instance is the only way to reach the instance path now (fetch_backend
	// has just mix and fx), and it must keep Fx out of the way entirely.
	t.Run("instance pinning ignores Fx", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{Instance: nitterSrv.URL}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		tweets, inst, err := w.Timeline().Timeline(context.Background(), "user", 5, 5)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if inst != nitterSrv.URL {
			t.Errorf("got inst=%q, want nitter instance %q", inst, nitterSrv.URL)
		}
		_ = tweets
	})

	// The documented precedence: --instance selects the instance path even when
	// the config explicitly asks for the fx-only lane.
	t.Run("instance pinning overrides an explicit fx backend", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "fx"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{Instance: nitterSrv.URL}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		_, inst, err := w.Timeline().Timeline(context.Background(), "user", 5, 5)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if inst != nitterSrv.URL {
			t.Errorf("got inst=%q, want the pinned instance %q — --instance must outrank fetch_backend=fx", inst, nitterSrv.URL)
		}
	})

	t.Run("following and conversation sources wire through Fx", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if w.Following() == nil {
			t.Error("Following() is nil")
		}
		if w.Conversation() == nil {
			t.Error("Conversation() is nil")
		}
		if w.Quotes() == nil {
			t.Error("Quotes() is nil")
		}
		if w.Trends() == nil {
			t.Error("Trends() is nil")
		}
		if w.Profile() == nil {
			t.Error("Profile() is nil")
		}
	})
}

func TestStatusDispatchAndCapabilityWiring(t *testing.T) {
	fxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/2/status/100"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"tweet": map[string]any{
					"id":   "100",
					"text": "Fx status 100",
					"author": map[string]any{
						"screen_name": "fxuser",
					},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/2/status/200/quotes"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"quotes": []map[string]any{
					{"id": "201", "text": "quote 1"},
				},
				"cursor": "cur_quotes",
			})
		case r.URL.Path == "/2/trends":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"trends": []map[string]any{
					{"name": "#Trending", "rank": 1, "context": "News"},
				},
			})
		case r.URL.Path == "/2/search/users":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"users": []map[string]any{
					{"screen_name": "searcheduser", "name": "Searched User"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/2/profile/profuser"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"user": map[string]any{
					"screen_name": "profuser",
					"name":        "Profile User",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fxSrv.Close()

	nitterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/status/"):
			// Return valid status HTML
			id := "999"
			if strings.Contains(r.URL.Path, "100") {
				id = "100"
			}
			_, _ = io.WriteString(w, `<div class="conversation"><div class="main-tweet"><div class="timeline-item">`+
				`<a class="tweet-link" href="/nitteruser/status/`+id+`#m"></a>`+
				`<div class="tweet-content">status body `+id+`</div>`+
				`</div></div></div>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer nitterSrv.Close()

	fxtwitter.EndpointOverrides.BaseURL = fxSrv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	t.Run("Status mix mode hits Fx first", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		tw, inst, err := w.Status().Status(context.Background(), "100")
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if inst != "FxTwitter" || tw.ID != "100" || tw.Text != "Fx status 100" {
			t.Errorf("got inst=%q tw=%+v, want FxTwitter and status 100", inst, tw)
		}
	})

	t.Run("Status mix mode falls back to Nitter on Fx 404", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		// status 999 404s on Fx, should fall back to Nitter
		tw, inst, err := w.Status().Status(context.Background(), "999")
		if err != nil {
			t.Fatalf("Status fallback failed: %v", err)
		}
		if inst != nitterSrv.URL || tw.ID != "999" {
			t.Errorf("got inst=%q tw=%+v, want nitter instance and status 999", inst, tw)
		}
	})

	t.Run("Status fx mode does not fall back to Nitter", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "fx"
		cfg.Instances = nil // fx is the only permitted source
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		// status 999 404s on Fx: its error must surface rather than being
		// replaced by the chooser's "no instances configured".
		_, _, err = w.Status().Status(context.Background(), "999")
		if err == nil {
			t.Fatal("expected the Fx error, got nil")
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
			t.Errorf("want KindNotFound, got %v", err)
		}
		if strings.Contains(err.Error(), "no instances configured") {
			t.Errorf("the Nitter fallback masked the real cause: %v", err)
		}
	})

	t.Run("Status instance pinning ignores Fx", func(t *testing.T) {
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{Instance: nitterSrv.URL}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		tw, inst, err := w.Status().Status(context.Background(), "100")
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if inst != nitterSrv.URL {
			t.Errorf("got inst=%q, want nitter instance %q", inst, nitterSrv.URL)
		}
		_ = tw
	})

	t.Run("Quotes wires to Fx", func(t *testing.T) {
		cfg := fastCfg()
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		quotes, cur, err := w.Quotes().Quotes(context.Background(), "200", 10, "")
		if err != nil {
			t.Fatalf("Quotes failed: %v", err)
		}
		if len(quotes) != 1 || quotes[0].ID != "201" || cur != "cur_quotes" {
			t.Errorf("got quotes=%+v cur=%q", quotes, cur)
		}
	})

	t.Run("Trends wires to Fx", func(t *testing.T) {
		cfg := fastCfg()
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		trends, err := w.Trends().Trends(context.Background())
		if err != nil {
			t.Fatalf("Trends failed: %v", err)
		}
		if len(trends) != 1 || trends[0].Name != "#Trending" {
			t.Errorf("got trends=%+v", trends)
		}
	})

	t.Run("SearchUsers wires to Fx", func(t *testing.T) {
		cfg := fastCfg()
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		users, err := w.Search().SearchUsers(context.Background(), "query", 10)
		if err != nil {
			t.Fatalf("SearchUsers failed: %v", err)
		}
		if len(users) != 1 || users[0].Handle != "searcheduser" {
			t.Errorf("got users=%+v", users)
		}

		// Empty query returns error
		_, err = w.Search().SearchUsers(context.Background(), "", 10)
		if err == nil {
			t.Error("expected error on empty query, got nil")
		}
	})

	t.Run("Profile wires to Fx", func(t *testing.T) {
		cfg := fastCfg()
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		prof, err := w.Profile().Profile(context.Background(), "profuser")
		if err != nil {
			t.Fatalf("Profile failed: %v", err)
		}
		if prof.Handle != "profuser" || prof.Name != "Profile User" {
			t.Errorf("got prof=%+v", prof)
		}
	})
}

// TestSearchSortPropagatesToBothBackends pins the no-silent-degradation rule
// of --sort: the SAME ordering reaches FxTwitter (as `feed`) and Nitter (as
// `f=`), in every backend mode. A mix-mode fallback must answer with the
// ordering that was asked for, never with the default one.
func TestSearchSortPropagatesToBothBackends(t *testing.T) {
	var mu sync.Mutex
	var fxQueries, nitterTargets []string

	fxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fxQueries = append(fxQueries, r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"results":[{"id":"222","url":"https://x.com/user/status/222","text":"search fx","author":{"screen_name":"user"}}]}`)
	}))
	defer fxSrv.Close()

	nitterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		nitterTargets = append(nitterTargets, r.URL.RequestURI())
		mu.Unlock()
		_, _ = io.WriteString(w, `<div class="timeline"><div class="timeline-item"><a class="tweet-link" href="/user/status/333"></a><div class="tweet-content">from nitter search</div></div></div>`)
	}))
	defer nitterSrv.Close()

	fxtwitter.EndpointOverrides.BaseURL = fxSrv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	snapshot := func() (fx []string, nitter []string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), fxQueries...), append([]string(nil), nitterTargets...)
	}

	t.Run("top reaches both backends", func(t *testing.T) {
		mu.Lock()
		fxQueries, nitterTargets = nil, nil
		mu.Unlock()

		// mix mode: Fx answers, and it must carry feed=top.
		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if _, inst, err := w.Search().Search(context.Background(), "moon", 5, 5, client.WithSearchSort("top")); err != nil {
			t.Fatalf("Search(mix, top): %v", err)
		} else if inst != "FxTwitter" {
			t.Errorf("instance = %q, want the Fx fast lane", inst)
		}
		fx, _ := snapshot()
		if len(fx) != 1 || !strings.Contains(fx[0], "feed=top") {
			t.Errorf("fx queries = %v, want exactly one carrying feed=top", fx)
		}
		if strings.Contains(fx[0], "feed=latest") {
			t.Errorf("fx query = %q, want the requested ordering, not the default", fx[0])
		}

		// nitter mode: the same ordering becomes f=top.
		mu.Lock()
		fxQueries, nitterTargets = nil, nil
		mu.Unlock()
		cfg2 := fastCfg()
		cfg2.FetchBackend = "nitter"
		cfg2.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w2, err := client.Build(&invocation.RootOptions{}, cfg2, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if _, _, err := w2.Search().Search(context.Background(), "moon", 5, 5, client.WithSearchSort("top")); err != nil {
			t.Fatalf("Search(nitter, top): %v", err)
		}
		_, nitter := snapshot()
		if len(nitter) != 1 || nitter[0] != "/search?f=top&q=moon" {
			t.Errorf("nitter targets = %v, want the f=top fetch", nitter)
		}
	})

	t.Run("mix fallback keeps the requested ordering", func(t *testing.T) {
		mu.Lock()
		fxQueries, nitterTargets = nil, nil
		mu.Unlock()

		// fxSrv answers everything, so point the fast lane at a dead
		// endpoint to force the Nitter fallback: the fallback must still be
		// f=top, not the default feed.
		fxtwitter.EndpointOverrides.BaseURL = "http://127.0.0.1:1"
		t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = fxSrv.URL })

		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if _, inst, err := w.Search().Search(context.Background(), "moon", 5, 5, client.WithSearchSort("top")); err != nil {
			t.Fatalf("Search(mix fallback, top): %v", err)
		} else if inst != nitterSrv.URL {
			t.Errorf("instance = %q, want the Nitter fallback %q", inst, nitterSrv.URL)
		}
		_, nitter := snapshot()
		if len(nitter) != 1 || nitter[0] != "/search?f=top&q=moon" {
			t.Errorf("nitter targets = %v, want the fallback to keep f=top", nitter)
		}
	})

	t.Run("default and latest keep the historical f=tweets/feed=latest", func(t *testing.T) {
		for _, opts := range [][]client.SearchOption{
			nil,
			{client.WithSearchSort("latest")},
			{client.WithSearchSort("")},
		} {
			mu.Lock()
			fxQueries, nitterTargets = nil, nil
			mu.Unlock()

			cfg := fastCfg()
			cfg.FetchBackend = "mix"
			cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
			w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if _, _, err := w.Search().Search(context.Background(), "moon", 5, 5, opts...); err != nil {
				t.Fatalf("Search(%v): %v", opts, err)
			}
			fx, _ := snapshot()
			if len(fx) != 1 || !strings.Contains(fx[0], "feed=latest") {
				t.Errorf("opts %v: fx queries = %v, want feed=latest", opts, fx)
			}
		}
	})

	t.Run("invalid sort is rejected before any backend", func(t *testing.T) {
		mu.Lock()
		fxQueries, nitterTargets = nil, nil
		mu.Unlock()

		cfg := fastCfg()
		cfg.FetchBackend = "mix"
		cfg.Instances = []settings.Instance{{URL: nitterSrv.URL}}
		w, err := client.Build(&invocation.RootOptions{}, cfg, time.Now)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		_, _, err = w.Search().Search(context.Background(), "moon", 5, 5, client.WithSearchSort("bogus"))
		if err == nil {
			t.Fatal("Search(bogus sort) = nil error, want invalid_argument")
		}
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
			t.Errorf("err = %v (%T), want KindInvalidArg", err, err)
		}
		fx, nitter := snapshot()
		if len(fx) != 0 || len(nitter) != 0 {
			t.Errorf("requests = fx %v / nitter %v, want none", fx, nitter)
		}
	})
}
