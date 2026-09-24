// Package settings manages the config.toml schema and the env > file >
// default precedence for resolving effective settings.
package settings

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Env keys that override file values (env > file > default precedence).
const (
	EnvDefaultLimit = "NITTER_DEFAULT_LIMIT"
	EnvLogLevel     = "NITTER_LOG_LEVEL"
	EnvLogFormat    = "NITTER_LOG_FORMAT"
	EnvFetchBackend = "NITTER_FETCH_BACKEND"
)

// Settings is the on-disk schema of ~/.nitter-cli/config.toml.
type Settings struct {
	// Instances is the [[instances]] array of tables (user-run Nitter
	// instances); like watch sources it is managed by hand-editing TOML.
	Instances []Instance `toml:"instances"`

	// WatchSources maps the [[watch.sources]] array of tables under the
	// [watch] table. go-toml/v2 struct tags cannot express the dotted
	// watch.sources path, so Load maps this field explicitly and no typed
	// TOML mapping applies.
	WatchSources []WatchSource `toml:"-"`

	DefaultLimit     int    `toml:"default_limit"`
	MaxPages         int    `toml:"max_pages"`
	RequestInterval  string `toml:"request_interval"`
	RetryAttempts    int    `toml:"retry_attempts"`
	RetryDelay       string `toml:"retry_delay"`
	InstanceCooldown string `toml:"instance_cooldown"`
	FetchBackend     string `toml:"fetch_backend"`
	Proxy            string `toml:"proxy"`
	LogLevel         string `toml:"log_level"`
	LogFormat        string `toml:"log_format"`

	// DownloadPath is where `nitter download` writes media; the default is
	// cwd-relative and `nitter download --output DIR` overrides it per
	// invocation. No existence check here: any string is accepted and the
	// download command creates the directory at runtime (mkdir -p).
	DownloadPath string `toml:"download_path"`

	// FilenameTemplate is the download filename template for regular media
	// files, with the placeholders {id}, {seq}, {user}, {kind} and {ext}.
	// The default "{id}-{seq}" reproduces the pre-template naming
	// byte-for-byte; covers always land as <id>-cover.<ext> regardless of
	// the template. `nitter download --filename-template` overrides it per
	// invocation; an empty value means the default. An invalid template is
	// a download-time warning plus fallback to the default, never an error.
	// No env override.
	FilenameTemplate string `toml:"filename_template"`

	// DirectoryTemplate is the download subdirectory template below the
	// output directory, rendered per planned file from {id}, {user} and
	// {kind} ({seq}/{ext} are forbidden in the directory position). Empty =
	// flat in the output directory root (the default). No env override.
	DirectoryTemplate string `toml:"directory_template"`
}

// Instance is one [[instances]] entry: a Nitter instance the user runs.
type Instance struct {
	URL      string `toml:"url"`
	Username string `toml:"username,omitempty"`
	Password string `toml:"password,omitempty"`
}

// WatchSource is one [[watch.sources]] entry.
type WatchSource struct {
	ID string `toml:"id"`
}

// Defaults returns the baseline settings.
func Defaults() Settings {
	return Settings{
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
}

// ValidationError marks a value that failed schema/type validation in the
// config layer. The CLI layer maps it to a usage error (exit 2) via
// errors.As, keeping internal/config free of CLI imports.
type ValidationError struct {
	Key string // TOML/env key that failed, e.g. "request_interval"
	Err error
}

func (e *ValidationError) Error() string { return e.Key + ": " + e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// ValidateFetchBackend accepts the two supported fetch modes. The nitter-only
// mode was removed: its meaning (instances only, never the fast lane) has no
// equivalent among what remains, and quietly mapping it onto mix would add the
// fast lane the user asked to avoid — so it fails with a one-edit migration.
// The instance path itself still exists: --instance pins one invocation to a
// single instance, but only for the commands that have an instance path (user,
// search, get, list) — the six Fx-only capabilities ignore it. Exported so
// `nitter config set` rejects exactly the same set.
func ValidateFetchBackend(key, v string) error {
	switch v {
	case "mix", "fx":
		return nil
	case "nitter":
		return fmt.Errorf("invalid %s %q: the nitter-only mode was removed; use \"mix\" (fast lane first, Nitter instances as the fallback). To send one call to your own instances, --instance URL applies to user, search, get and list", key, v)
	}
	return fmt.Errorf("invalid %s %q (allowed: mix, fx)", key, v)
}

// Load resolves settings with env > file > default precedence. A missing
// config file yields pure Defaults() and creates nothing; malformed TOML
// returns a plain wrapped error, while schema/type validation failures
// (durations, the integer env override) return *ValidationError so the CLI
// layer can map them to usage errors via errors.As.
func Load(cfgPath string, env func(string) string) (Settings, error) {
	s := Defaults()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Settings{}, fmt.Errorf("read config: %w", err)
		}
		data = nil
	}
	if data != nil {
		if err := toml.Unmarshal(data, &s); err != nil {
			return Settings{}, fmt.Errorf("parse config: %w", err)
		}
		// Second targeted pass: Settings cannot carry the watch table via a
		// struct tag (dotted path), so map [[watch.sources]] explicitly.
		var watchView struct {
			Watch struct {
				Sources []WatchSource `toml:"sources"`
			} `toml:"watch"`
		}
		if err := toml.Unmarshal(data, &watchView); err != nil {
			return Settings{}, fmt.Errorf("parse config: %w", err)
		}
		s.WatchSources = watchView.Watch.Sources
	}

	// Environment overrides apply after file values, before validation.
	if v := env(EnvDefaultLimit); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Settings{}, &ValidationError{Key: EnvDefaultLimit, Err: fmt.Errorf("parse %s: %w", EnvDefaultLimit, err)}
		}
		if n < 1 {
			return Settings{}, &ValidationError{Key: EnvDefaultLimit, Err: fmt.Errorf("%s: %d is not a valid cap; must be >= 1 (there is no unlimited value)", EnvDefaultLimit, n)}
		}
		s.DefaultLimit = n
	}
	if v := env(EnvLogLevel); v != "" {
		s.LogLevel = v
	}
	if v := env(EnvLogFormat); v != "" {
		s.LogFormat = v
	}
	if v := env(EnvFetchBackend); v != "" {
		if err := ValidateFetchBackend(EnvFetchBackend, v); err != nil {
			return Settings{}, &ValidationError{Key: EnvFetchBackend, Err: err}
		}
		s.FetchBackend = v
	}

	if err := ValidateFetchBackend("fetch_backend", s.FetchBackend); err != nil {
		return Settings{}, &ValidationError{Key: "fetch_backend", Err: err}
	}

	// The two acquisition caps must be positive. 0 used to be the "unlimited"
	// spelling, which the two fetch lanes read in opposite ways; it is gone, so
	// a hand-edited config carrying 0 fails loudly (exit 2) instead of silently
	// asking for nothing.
	for _, cap := range []struct {
		key   string
		value int
	}{
		{"default_limit", s.DefaultLimit},
		{"max_pages", s.MaxPages},
	} {
		if cap.value < 1 {
			return Settings{}, &ValidationError{Key: cap.key, Err: fmt.Errorf("parse config %s: %d is not a valid cap; must be >= 1 (there is no unlimited value)", cap.key, cap.value)}
		}
	}

	// Durations are stored as strings in TOML and validated here so callers
	// never receive values that time.ParseDuration would reject later.
	for _, d := range []struct{ key, value string }{
		{"request_interval", s.RequestInterval},
		{"retry_delay", s.RetryDelay},
		{"instance_cooldown", s.InstanceCooldown},
	} {
		if _, err := time.ParseDuration(d.value); err != nil {
			return Settings{}, &ValidationError{Key: d.key, Err: fmt.Errorf("parse config %s: %w", d.key, err)}
		}
	}
	return s, nil
}
