// Package config implements the `nitter config` command family: path, get,
// set and unset over ~/.nitter-cli/config.toml. Only the twelve scalar keys
// listed in knownKeys are managed; the [[instances]] / [[watch.sources]]
// array tables are hand-edited TOML and rejected here. All input-contract
// violations are validated before any file read/write.
package config

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/config/paths"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

// knownKeys is the exact, ordered set of scalar keys this command manages;
// `config get` without a key prints them in this order.
var knownKeys = []string{
	"default_limit",
	"max_pages",
	"request_interval",
	"retry_attempts",
	"retry_delay",
	"instance_cooldown",
	"proxy",
	"log_level",
	"log_format",
	"download_path",
	"filename_template",
	"directory_template",
}

// getters maps each known key to its effective value (env > file > default).
var getters = map[string]func(settings.Settings) string{
	"default_limit":      func(s settings.Settings) string { return strconv.Itoa(s.DefaultLimit) },
	"max_pages":          func(s settings.Settings) string { return strconv.Itoa(s.MaxPages) },
	"request_interval":   func(s settings.Settings) string { return s.RequestInterval },
	"retry_attempts":     func(s settings.Settings) string { return strconv.Itoa(s.RetryAttempts) },
	"retry_delay":        func(s settings.Settings) string { return s.RetryDelay },
	"instance_cooldown":  func(s settings.Settings) string { return s.InstanceCooldown },
	"proxy":              func(s settings.Settings) string { return s.Proxy },
	"log_level":          func(s settings.Settings) string { return s.LogLevel },
	"log_format":         func(s settings.Settings) string { return s.LogFormat },
	"download_path":      func(s settings.Settings) string { return s.DownloadPath },
	"filename_template":  func(s settings.Settings) string { return s.FilenameTemplate },
	"directory_template": func(s settings.Settings) string { return s.DirectoryTemplate },
}

// arrayTableHint is appended when set/unset targets something outside the
// managed scalar keys: array tables are hand-edited TOML.
const arrayTableHint = "; [[instances]] and [[watch.sources]] array tables are managed by editing config.toml directly"

func isKnownKey(key string) bool {
	_, ok := getters[key]
	return ok
}

// New builds the `nitter config` command tree over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and manage the nitter-cli configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Keep the repo-wide exit semantics: an unknown subcommand under
			// config exits 1 (root does the same for the top level).
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newPathCommand(s),
		newGetCommand(s),
		newSetCommand(s),
		newUnsetCommand(s),
	)
	return cmd
}

func newPathCommand(s *invocation.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return invocation.Usagef("usage: config path (takes no arguments)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := configPath()
			if err != nil {
				return err
			}
			fmt.Fprintln(s.Out, cfgPath)
			return nil
		},
	}
}

func newGetCommand(s *invocation.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "get [KEY]",
		Short: "Print the effective value of KEY, or all known keys without one",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return invocation.Usagef("usage: config get [KEY]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Unknown keys are rejected before the config file is even read.
			if len(args) == 1 && !isKnownKey(args[0]) {
				return invocation.Usagef("config get: unknown key %q (known keys: %s)", args[0], strings.Join(knownKeys, ", "))
			}
			// The single effective-settings loader of the wiring layer (env >
			// file > default; a *settings.ValidationError surfaces as a usage
			// error, exit 2). One implementation for every command.
			cfg, err := client.LoadEffectiveSettings()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				for _, key := range knownKeys {
					fmt.Fprintf(s.Out, "%s = %s\n", key, getters[key](cfg))
				}
				return nil
			}
			fmt.Fprintf(s.Out, "%s = %s\n", args[0], getters[args[0]](cfg))
			return nil
		},
	}
}

func newSetCommand(s *invocation.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "set KEY [VALUE]",
		Short: "Set KEY to VALUE; without VALUE the value is read from piped stdin",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 || len(args) > 2 {
				return invocation.Usagef("usage: config set KEY [VALUE]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !isKnownKey(key) {
				return invocation.Usagef("config set: unknown key %q (known keys: %s)%s", key, strings.Join(knownKeys, ", "), arrayTableHint)
			}
			raw, err := valueInput(s, key, args)
			if err != nil {
				return err
			}
			typed, err := validateAndCoerce(key, raw)
			if err != nil {
				return err
			}
			cfgPath, err := configPath()
			if err != nil {
				return err
			}
			// Seed the self-documenting baseline first (no-replace: a no-op
			// when the file already exists), so a fresh install ends up with
			// the full schema instead of a one-key file. Validation already
			// passed, so "reject before any file side effect" still holds.
			if err := paths.EnsureDefaultConfigFile(cfgPath, settings.DefaultConfigTOML); err != nil {
				return err
			}
			// SaveKnown preserves unknown keys and the array tables and
			// publishes atomically at 0600.
			return settings.SaveKnown(cfgPath, func(tree map[string]any) error {
				tree[key] = typed
				return nil
			})
		},
	}
}

func newUnsetCommand(s *invocation.Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "unset KEY",
		Short: "Remove KEY so it falls back to env/default",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: config unset KEY")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !isKnownKey(key) {
				return invocation.Usagef("config unset: unknown key %q (known keys: %s)%s", key, strings.Join(knownKeys, ", "), arrayTableHint)
			}
			cfgPath, err := configPath()
			if err != nil {
				return err
			}
			// Seed the baseline on a fresh install (no-replace) before the
			// delete, same as `config set` does before its write.
			if err := paths.EnsureDefaultConfigFile(cfgPath, settings.DefaultConfigTOML); err != nil {
				return err
			}
			return settings.SaveKnown(cfgPath, func(tree map[string]any) error {
				delete(tree, key)
				return nil
			})
		},
	}
}

// valueInput resolves the value for `config set KEY [VALUE]`: an explicit
// VALUE argument wins; otherwise one line is read from stdin when it is
// piped (secrets and credential-bearing proxy URLs must not need argv —
// pixiv-cli convention). On a TTY there is nothing to read, so guide the
// user to either form instead of blocking.
func valueInput(s *invocation.Streams, key string, args []string) (string, error) {
	if len(args) == 2 {
		return args[1], nil
	}
	if s.InIsTTY {
		return "", invocation.Usagef("config set %s: missing VALUE; pass it as an argument or pipe it on stdin (e.g. echo 30 | nitter config set %s)", key, key)
	}
	sc := bufio.NewScanner(s.In)
	sc.Scan()
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read stdin value for %s: %w", key, err)
	}
	return sc.Text(), nil
}

// validateAndCoerce checks raw against the key's schema and returns the typed
// value to store (int for integer keys, string otherwise). It runs before any
// file read/write so a rejected value never touches the disk. Integer and
// duration keys accept 0 (its semantics are defined downstream) and reject
// negatives; log_level/log_format are enums; proxy, download_path and the two
// download naming templates accept any string (download_path is checked at
// download runtime with a mkdir -p; an invalid template is a download-time
// warning plus fallback to the default, never a set-time rejection).
func validateAndCoerce(key, raw string) (any, error) {
	switch key {
	case "default_limit", "max_pages", "retry_attempts":
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, invocation.Usagef("config set %s: %q is not an integer", key, raw)
		}
		if n < 0 {
			return nil, invocation.Usagef("config set %s: %d is negative; must be >= 0", key, n)
		}
		return n, nil
	case "request_interval", "retry_delay", "instance_cooldown":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, invocation.Usagef("config set %s: %q is not a duration (e.g. 500ms, 2s)", key, raw)
		}
		if d < 0 {
			return nil, invocation.Usagef("config set %s: %q is negative; must be >= 0", key, raw)
		}
		return raw, nil
	case "log_level":
		if raw != "debug" && raw != "info" {
			return nil, invocation.Usagef("config set log_level: %q is invalid; must be debug or info", raw)
		}
		return raw, nil
	case "log_format":
		if raw != "text" && raw != "json" {
			return nil, invocation.Usagef("config set log_format: %q is invalid; must be text or json", raw)
		}
		return raw, nil
	default: // "proxy", "download_path" — any string is accepted
		return raw, nil
	}
}

// configPath resolves the config file location from the user home.
func configPath() (string, error) {
	p, err := paths.New()
	if err != nil {
		return "", fmt.Errorf("resolve config location: %w", err)
	}
	return p.ConfigFile, nil
}
