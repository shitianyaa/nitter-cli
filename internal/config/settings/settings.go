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
	Proxy            string `toml:"proxy"`
	LogLevel         string `toml:"log_level"`
	LogFormat        string `toml:"log_format"`
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
		s.DefaultLimit = n
	}
	if v := env(EnvLogLevel); v != "" {
		s.LogLevel = v
	}
	if v := env(EnvLogFormat); v != "" {
		s.LogFormat = v
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
