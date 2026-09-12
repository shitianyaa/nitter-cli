package client_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/cli/client"
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/twitter-cli/internal/config/settings"
	"github.com/shitianyaa/twitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// validCfg returns Settings with valid duration strings (as settings.Load
// would produce). Build is deliberately strict about unvalidated settings.
func validCfg() settings.Settings {
	return settings.Settings{RetryDelay: "1s", RequestInterval: "1s", InstanceCooldown: "1s"}
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
		var terr *twitter.Error
		if !errors.As(err, &terr) || terr.Kind != twitter.KindInvalidArg {
			t.Fatalf("%s: err = %v (%T), want *twitter.Error KindInvalidArg", tc.name, err, err)
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
		var terr *twitter.Error
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
	ue := client.AsUsageError(twitter.Errorf(twitter.KindInvalidArg, "op", "bad input"))
	var got *invocation.UsageError
	if !errors.As(ue, &got) {
		t.Fatalf("AsUsageError(KindInvalidArg) = %v (%T), want *invocation.UsageError", ue, ue)
	}
	down := twitter.Errorf(twitter.KindUnavailable, "op", "down")
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
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindInvalidArg {
		t.Fatalf("Timeline(NASA!) = %v (%T), want KindInvalidArg", err, err)
	}
	_, _, err = src.Timeline(context.Background(), "NASA", 1, 1)
	if !errors.As(err, &terr) || terr.Kind != twitter.KindUnavailable {
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
		if cfg.DefaultLimit != 20 || cfg.RequestInterval != "1s" {
			t.Fatalf("cfg = %+v, want defaults", cfg)
		}
	})
	t.Run("validation error maps to usage error", func(t *testing.T) {
		neutralizeEnv(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		dir := filepath.Join(home, ".twitter-cli")
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
}

// neutralizeEnv clears the settings and proxy env overrides so the file and
// default layers are observable in isolation.
func neutralizeEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TWITTER_DEFAULT_LIMIT", "TWITTER_LOG_LEVEL", "TWITTER_LOG_FORMAT",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, "")
	}
}
