package settings_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/config/settings"
)

func envMap(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestDefaults(t *testing.T) {
	want := settings.Settings{
		DefaultLimit:     20,
		MaxPages:         5,
		RequestInterval:  "1s",
		RetryAttempts:    2,
		RetryDelay:       "1s",
		InstanceCooldown: "60s",
		Proxy:            "",
		LogLevel:         "info",
		LogFormat:        "text",
	}
	if got := settings.Defaults(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Defaults() = %+v, want %+v", got, want)
	}
}

const loadFixture = `default_limit     = 7
max_pages         = 2
request_interval  = "2s"
retry_attempts    = 5
retry_delay       = "3s"
instance_cooldown = "90s"
proxy             = "http://127.0.0.1:7890"
log_level         = "debug"
log_format        = "json"

[[instances]]
url = "http://nitter.internal:8080"
username = "alice"
password = "secret"

[[instances]]
url = "https://nitter.example.net"

[[watch.sources]]
id = "user:NASA"

[[watch.sources]]
id = "user:esa"
`

func TestLoad(t *testing.T) {
	t.Run("missing file yields pure defaults and creates nothing", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		got, err := settings.Load(cfgPath, envMap(nil))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if want := settings.Defaults(); !reflect.DeepEqual(got, want) {
			t.Fatalf("Load() = %+v, want %+v", got, want)
		}
		if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
			t.Fatalf("Load() must not create %s (stat err = %v)", cfgPath, err)
		}
	})

	t.Run("parses fixture toml over defaults", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfgPath, []byte(loadFixture), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		got, err := settings.Load(cfgPath, envMap(nil))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		want := settings.Settings{
			Instances: []settings.Instance{
				{URL: "http://nitter.internal:8080", Username: "alice", Password: "secret"},
				{URL: "https://nitter.example.net"},
			},
			WatchSources:     []settings.WatchSource{{ID: "user:NASA"}, {ID: "user:esa"}},
			DefaultLimit:     7,
			MaxPages:         2,
			RequestInterval:  "2s",
			RetryAttempts:    5,
			RetryDelay:       "3s",
			InstanceCooldown: "90s",
			Proxy:            "http://127.0.0.1:7890",
			LogLevel:         "debug",
			LogFormat:        "json",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Load() = %+v, want %+v", got, want)
		}
	})

	t.Run("malformed toml errors", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfgPath, []byte("default_limit = "), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		_, err := settings.Load(cfgPath, envMap(nil))
		if err == nil {
			t.Fatal("Load() error = nil, want parse error")
		}
		if !strings.Contains(err.Error(), "parse config") {
			t.Fatalf("Load() error = %v, want it to name the parse stage", err)
		}
	})

	t.Run("invalid duration errors", func(t *testing.T) {
		for _, key := range []string{"request_interval", "retry_delay", "instance_cooldown"} {
			cfgPath := filepath.Join(t.TempDir(), "config.toml")
			content := key + ` = "abc"` + "\n"
			if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			_, err := settings.Load(cfgPath, envMap(nil))
			if err == nil {
				t.Fatalf("Load() with invalid %s error = nil, want error", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error = %v, want it to name %s", err, key)
			}
		}
	})

	t.Run("env overrides win over file", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		const fixture = "default_limit = 7\nlog_level = \"debug\"\n"
		if err := os.WriteFile(cfgPath, []byte(fixture), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		got, err := settings.Load(cfgPath, envMap(map[string]string{
			"TWITTER_DEFAULT_LIMIT": "9",
			"TWITTER_LOG_FORMAT":    "json",
		}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if got.DefaultLimit != 9 {
			t.Fatalf("DefaultLimit = %d, want 9 (env beats file)", got.DefaultLimit)
		}
		if got.LogLevel != "debug" {
			t.Fatalf("LogLevel = %q, want %q (file beats default)", got.LogLevel, "debug")
		}
		if got.LogFormat != "json" {
			t.Fatalf("LogFormat = %q, want %q (env beats default)", got.LogFormat, "json")
		}
	})

	t.Run("invalid TWITTER_DEFAULT_LIMIT errors", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		_, err := settings.Load(cfgPath, envMap(map[string]string{
			"TWITTER_DEFAULT_LIMIT": "abc",
		}))
		if err == nil {
			t.Fatal("Load() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "TWITTER_DEFAULT_LIMIT") {
			t.Fatalf("Load() error = %v, want it to name TWITTER_DEFAULT_LIMIT", err)
		}
	})
}

func TestLoadValidationError(t *testing.T) {
	t.Run("invalid duration from file carries the key", func(t *testing.T) {
		for _, key := range []string{"request_interval", "retry_delay", "instance_cooldown"} {
			cfgPath := filepath.Join(t.TempDir(), "config.toml")
			content := key + ` = "abc"` + "\n"
			if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			_, err := settings.Load(cfgPath, envMap(nil))
			var verr *settings.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("Load() with invalid %s error = %v, want *settings.ValidationError", key, err)
			}
			if verr.Key != key {
				t.Fatalf("ValidationError.Key = %q, want %q", verr.Key, key)
			}
			if verr.Err == nil {
				t.Fatal("ValidationError.Err = nil, want the wrapped parse error")
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error = %v, want it to name %s", err, key)
			}
		}
	})

	t.Run("invalid TWITTER_DEFAULT_LIMIT env value carries the key", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		_, err := settings.Load(cfgPath, envMap(map[string]string{
			"TWITTER_DEFAULT_LIMIT": "abc",
		}))
		var verr *settings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Load() error = %v, want *settings.ValidationError", err)
		}
		if verr.Key != "TWITTER_DEFAULT_LIMIT" {
			t.Fatalf("ValidationError.Key = %q, want %q", verr.Key, "TWITTER_DEFAULT_LIMIT")
		}
	})

	t.Run("non-validation paths stay plain wrapped errors", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfgPath, []byte("default_limit = "), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		_, err := settings.Load(cfgPath, envMap(nil))
		if err == nil {
			t.Fatal("Load() error = nil, want parse error")
		}
		var verr *settings.ValidationError
		if errors.As(err, &verr) {
			t.Fatalf("Load() error = %v, want no *settings.ValidationError on the parse path", err)
		}
	})
}

func TestSaveKnown(t *testing.T) {
	const preserveFixture = `default_limit = 7
custom_flag = "keep-me"

[extra_table]
enabled = true

[[instances]]
url = "http://nitter.internal:8080"

[[watch.sources]]
id = "user:NASA"
`

	t.Run("preserves unknown keys and array tables outside the mutated key", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfgPath, []byte(preserveFixture), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		err := settings.SaveKnown(cfgPath, func(tree map[string]any) error {
			tree["default_limit"] = int64(42)
			return nil
		})
		if err != nil {
			t.Fatalf("SaveKnown() error = %v", err)
		}

		got, err := settings.Load(cfgPath, envMap(nil))
		if err != nil {
			t.Fatalf("Load() saved config: %v", err)
		}
		if got.DefaultLimit != 42 {
			t.Fatalf("DefaultLimit = %d, want 42", got.DefaultLimit)
		}
		if len(got.Instances) != 1 || got.Instances[0].URL != "http://nitter.internal:8080" {
			t.Fatalf("Instances = %+v, want the original single instance", got.Instances)
		}
		if len(got.WatchSources) != 1 || got.WatchSources[0].ID != "user:NASA" {
			t.Fatalf("WatchSources = %+v, want the original single source", got.WatchSources)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read saved config: %v", err)
		}
		for _, want := range []string{"keep-me", "user:NASA", "http://nitter.internal:8080", "enabled = true"} {
			if !strings.Contains(string(data), want) {
				t.Fatalf("saved config lost %q:\n%s", want, data)
			}
		}
	})

	t.Run("written file is private 0600", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		err := settings.SaveKnown(cfgPath, func(tree map[string]any) error {
			tree["default_limit"] = int64(42)
			return nil
		})
		if err != nil {
			t.Fatalf("SaveKnown() error = %v", err)
		}
		if runtime.GOOS != "windows" { // Windows chmod only toggles the read-only bit.
			info, err := os.Stat(cfgPath)
			if err != nil {
				t.Fatalf("stat saved config: %v", err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("saved mode = %v, want 0600", got)
			}
		}
	})

	t.Run("missing file creates a fresh config", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		err := settings.SaveKnown(cfgPath, func(tree map[string]any) error {
			tree["default_limit"] = int64(42)
			return nil
		})
		if err != nil {
			t.Fatalf("SaveKnown() error = %v", err)
		}
		got, err := settings.Load(cfgPath, envMap(nil))
		if err != nil {
			t.Fatalf("Load() saved config: %v", err)
		}
		if got.DefaultLimit != 42 {
			t.Fatalf("DefaultLimit = %d, want 42", got.DefaultLimit)
		}
	})

	t.Run("mutator error aborts the write", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfgPath, []byte("default_limit = 7\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		sentinel := errors.New("rejected")

		err := settings.SaveKnown(cfgPath, func(tree map[string]any) error {
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("SaveKnown() error = %v, want it to wrap the mutator error", err)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read config after failed save: %v", err)
		}
		if string(data) != "default_limit = 7\n" {
			t.Fatalf("config changed after failed save: %q", data)
		}
	})
}
