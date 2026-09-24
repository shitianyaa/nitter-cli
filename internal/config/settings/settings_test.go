package settings_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

func envMap(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestDefaults(t *testing.T) {
	want := settings.Settings{
		DefaultLimit:      20,
		MaxPages:          5,
		RequestInterval:   "1s",
		RetryAttempts:     2,
		RetryDelay:        "1s",
		InstanceCooldown:  "60s",
		FetchBackend:      "mix",
		Proxy:             "",
		LogLevel:          "info",
		LogFormat:         "text",
		DownloadPath:      "./nitter-media",
		FilenameTemplate:  "{id}-{seq}",
		DirectoryTemplate: "",
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
download_path     = "/srv/nitter-media"

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
			FetchBackend:     "mix",
			Proxy:            "http://127.0.0.1:7890",
			LogLevel:         "debug",
			LogFormat:        "json",
			DownloadPath:     "/srv/nitter-media",
			// The fixture names no template keys: the defaults merge over.
			FilenameTemplate:  "{id}-{seq}",
			DirectoryTemplate: "",
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
		const fixture = "default_limit = 7\nlog_level = \"debug\"\nfetch_backend = \"mix\"\n"
		if err := os.WriteFile(cfgPath, []byte(fixture), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		got, err := settings.Load(cfgPath, envMap(map[string]string{
			"NITTER_DEFAULT_LIMIT": "9",
			"NITTER_LOG_FORMAT":    "json",
			"NITTER_FETCH_BACKEND": "fx",
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
		if got.FetchBackend != "fx" {
			t.Fatalf("FetchBackend = %q, want %q (env beats file)", got.FetchBackend, "fx")
		}
	})

	t.Run("invalid NITTER_FETCH_BACKEND errors", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		_, err := settings.Load(cfgPath, envMap(map[string]string{
			"NITTER_FETCH_BACKEND": "bogus",
		}))
		if err == nil {
			t.Fatal("Load() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "NITTER_FETCH_BACKEND") {
			t.Fatalf("Load() error = %v, want it to name NITTER_FETCH_BACKEND", err)
		}
	})

	t.Run("invalid NITTER_DEFAULT_LIMIT errors", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		_, err := settings.Load(cfgPath, envMap(map[string]string{
			"NITTER_DEFAULT_LIMIT": "abc",
		}))
		if err == nil {
			t.Fatal("Load() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "NITTER_DEFAULT_LIMIT") {
			t.Fatalf("Load() error = %v, want it to name NITTER_DEFAULT_LIMIT", err)
		}
	})

	t.Run("download_path has no env override", func(t *testing.T) {
		// Only default_limit, log_level and log_format have env layers; a
		// NITTER_DOWNLOAD_PATH variable must be ignored, not honored.
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		got, err := settings.Load(cfgPath, envMap(map[string]string{
			"NITTER_DOWNLOAD_PATH": "/tmp/from-env",
		}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if want := settings.Defaults().DownloadPath; got.DownloadPath != want {
			t.Fatalf("DownloadPath = %q, want the default %q (no env override exists)", got.DownloadPath, want)
		}
	})

	t.Run("download templates have no env override", func(t *testing.T) {
		// The env set stays at three keys (default_limit, log_level,
		// log_format); template variables must be ignored, not honored.
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		got, err := settings.Load(cfgPath, envMap(map[string]string{
			"NITTER_FILENAME_TEMPLATE":  "{x}",
			"NITTER_DIRECTORY_TEMPLATE": "y",
		}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if want := settings.Defaults(); got.FilenameTemplate != want.FilenameTemplate || got.DirectoryTemplate != want.DirectoryTemplate {
			t.Fatalf("templates = %q/%q, want the defaults %q/%q (no env override exists)",
				got.FilenameTemplate, got.DirectoryTemplate, want.FilenameTemplate, want.DirectoryTemplate)
		}
	})

	t.Run("download_path accepts any string without validation", func(t *testing.T) {
		for _, value := range []string{"", " ", "~/pictures", "C:\\Users\\me\\media", "relative/dir"} {
			cfgPath := filepath.Join(t.TempDir(), "config.toml")
			content := "download_path = " + strconv.Quote(value) + "\n"
			if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			got, err := settings.Load(cfgPath, envMap(nil))
			if err != nil {
				t.Fatalf("Load() with download_path %q error = %v, want any string accepted", value, err)
			}
			if got.DownloadPath != value {
				t.Fatalf("DownloadPath = %q, want %q (any string accepted)", got.DownloadPath, value)
			}
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

	t.Run("invalid NITTER_DEFAULT_LIMIT env value carries the key", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		_, err := settings.Load(cfgPath, envMap(map[string]string{
			"NITTER_DEFAULT_LIMIT": "abc",
		}))
		var verr *settings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Load() error = %v, want *settings.ValidationError", err)
		}
		if verr.Key != "NITTER_DEFAULT_LIMIT" {
			t.Fatalf("ValidationError.Key = %q, want %q", verr.Key, "NITTER_DEFAULT_LIMIT")
		}
	})

	t.Run("invalid fetch_backend from file carries the key", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		content := "fetch_backend = \"invalid\"\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		_, err := settings.Load(cfgPath, envMap(nil))
		var verr *settings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Load() error = %v, want *settings.ValidationError", err)
		}
		if verr.Key != "fetch_backend" {
			t.Fatalf("ValidationError.Key = %q, want %q", verr.Key, "fetch_backend")
		}
	})

	t.Run("the removed nitter mode is rejected with a migration hint", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfgPath, []byte("fetch_backend = \"nitter\"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		_, err := settings.Load(cfgPath, envMap(nil))
		var verr *settings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Load() error = %v, want *settings.ValidationError", err)
		}
		if verr.Key != "fetch_backend" {
			t.Fatalf("ValidationError.Key = %q, want %q", verr.Key, "fetch_backend")
		}
		// Assert the nitter-specific arm, not just "mix": the generic
		// rejection (allowed: mix, fx) also contains "mix", so a weaker
		// assertion would still pass with the migration arm deleted.
		if !strings.Contains(verr.Error(), "nitter-only mode was removed") {
			t.Errorf("error = %q, want the nitter-specific migration hint", verr.Error())
		}
	})

	t.Run("invalid NITTER_FETCH_BACKEND env value carries the key", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		_, err := settings.Load(cfgPath, envMap(map[string]string{
			"NITTER_FETCH_BACKEND": "invalid",
		}))
		var verr *settings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Load() error = %v, want *settings.ValidationError", err)
		}
		if verr.Key != "NITTER_FETCH_BACKEND" {
			t.Fatalf("ValidationError.Key = %q, want %q", verr.Key, "NITTER_FETCH_BACKEND")
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

func TestDefaultConfigTOML(t *testing.T) {
	t.Run("parses to pure defaults", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfgPath, []byte(settings.DefaultConfigTOML), 0o600); err != nil {
			t.Fatalf("write baseline: %v", err)
		}

		got, err := settings.Load(cfgPath, envMap(nil))
		if err != nil {
			t.Fatalf("Load() baseline error = %v", err)
		}
		if want := settings.Defaults(); !reflect.DeepEqual(got, want) {
			t.Fatalf("Load() baseline = %+v, want defaults %+v", got, want)
		}
	})

	t.Run("no enabled instances or watch sources", func(t *testing.T) {
		for _, line := range strings.Split(settings.DefaultConfigTOML, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[[instances]]") || strings.HasPrefix(trimmed, "[[watch.sources]]") {
				t.Fatalf("baseline has active array-table header %q; examples must stay commented out", trimmed)
			}
		}
	})

	t.Run("documents download_path with default and --output override", func(t *testing.T) {
		if !strings.Contains(settings.DefaultConfigTOML, `download_path     = "./nitter-media"`) {
			t.Fatalf("baseline must carry the active download_path default:\n%s", settings.DefaultConfigTOML)
		}
		if !strings.Contains(settings.DefaultConfigTOML, "--output") {
			t.Fatalf("baseline comment must note that `nitter download --output` overrides download_path per call:\n%s", settings.DefaultConfigTOML)
		}
	})

	t.Run("documents the download naming templates", func(t *testing.T) {
		if !strings.Contains(settings.DefaultConfigTOML, `filename_template  = "{id}-{seq}"`) {
			t.Fatalf("baseline must carry the active filename_template default:\n%s", settings.DefaultConfigTOML)
		}
		if !strings.Contains(settings.DefaultConfigTOML, `directory_template = ""`) {
			t.Fatalf("baseline must carry the empty directory_template default (flat):\n%s", settings.DefaultConfigTOML)
		}
	})

	// The baseline is what every fresh install reads, so the value set its
	// comment advertises must be exactly the set ValidateFetchBackend accepts:
	// a stale `# mix|nitter|fx` would send users to a value that now exits 2.
	t.Run("fetch_backend comment advertises exactly the accepted values", func(t *testing.T) {
		var advertised []string
		for _, line := range strings.Split(settings.DefaultConfigTOML, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "fetch_backend") {
				continue
			}
			_, comment, ok := strings.Cut(line, "#")
			if !ok {
				t.Fatalf("baseline fetch_backend line carries no value comment: %q", line)
			}
			advertised = strings.Split(strings.TrimSpace(comment), "|")
		}
		if len(advertised) == 0 {
			t.Fatalf("baseline carries no fetch_backend line:\n%s", settings.DefaultConfigTOML)
		}
		for _, v := range advertised {
			if err := settings.ValidateFetchBackend("fetch_backend", v); err != nil {
				t.Errorf("baseline advertises fetch_backend %q, which ValidateFetchBackend rejects: %v", v, err)
			}
		}
		if want := []string{"mix", "fx"}; !reflect.DeepEqual(advertised, want) {
			t.Errorf("baseline advertises %v, want %v", advertised, want)
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

	t.Run("download_path set then unset round-trips to the default", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.toml")

		if err := settings.SaveKnown(cfgPath, func(tree map[string]any) error {
			tree["download_path"] = "./somewhere else"
			return nil
		}); err != nil {
			t.Fatalf("SaveKnown() set error = %v", err)
		}
		got, err := settings.Load(cfgPath, envMap(nil))
		if err != nil {
			t.Fatalf("Load() after set: %v", err)
		}
		if got.DownloadPath != "./somewhere else" {
			t.Fatalf("DownloadPath = %q, want %q after set", got.DownloadPath, "./somewhere else")
		}

		if err := settings.SaveKnown(cfgPath, func(tree map[string]any) error {
			delete(tree, "download_path")
			return nil
		}); err != nil {
			t.Fatalf("SaveKnown() unset error = %v", err)
		}
		got, err = settings.Load(cfgPath, envMap(nil))
		if err != nil {
			t.Fatalf("Load() after unset: %v", err)
		}
		if want := settings.Defaults().DownloadPath; got.DownloadPath != want {
			t.Fatalf("DownloadPath = %q, want the default %q after unset", got.DownloadPath, want)
		}
	})
}
