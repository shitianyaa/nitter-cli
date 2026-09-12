package config_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/config"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

// tempHome redirects the home directory to a fresh temp dir (paths.New reads
// it via os.UserHomeDir) and neutralizes the settings env overrides so the
// file and default layers are observable in isolation.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("NITTER_DEFAULT_LIMIT", "")
	t.Setenv("NITTER_LOG_LEVEL", "")
	t.Setenv("NITTER_LOG_FORMAT", "")
	return home
}

func configFile(home string) string {
	return filepath.Join(home, ".nitter-cli", "config.toml")
}

func runCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s must not exist (stat err = %v)", path, err)
	}
}

func TestConfigPathPrintsConfigLocation(t *testing.T) {
	home := tempHome(t)
	code, out, errOut := runCLI(t, "", "config", "path")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if got, want := strings.TrimSpace(out), configFile(home); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestConfigPathPublishesBaselineConfig(t *testing.T) {
	home := tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "path")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	data, err := os.ReadFile(configFile(home))
	if err != nil {
		t.Fatalf("config path must publish the baseline config on first run: %v", err)
	}
	if string(data) != settings.DefaultConfigTOML {
		t.Fatalf("published baseline = %q, want settings.DefaultConfigTOML", data)
	}
	if runtime.GOOS != "windows" { // Windows chmod only toggles the read-only bit.
		info, err := os.Stat(configFile(home))
		if err != nil {
			t.Fatalf("stat published baseline config: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("published mode = %v, want 0600", got)
		}
	}
}

func TestConfigGetPublishesBaselineConfig(t *testing.T) {
	home := tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "get", "log_level")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if _, err := os.Stat(configFile(home)); err != nil {
		t.Fatalf("config get must publish the baseline config on first run (stat err = %v)", err)
	}
}

func TestConfigSetSeedsBaselineOnFreshHome(t *testing.T) {
	home := tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "set", "default_limit", "30")
	if code != 0 {
		t.Fatalf("set exit = %d, want 0 (stderr %q)", code, errOut)
	}
	data, err := os.ReadFile(configFile(home))
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), "default_limit = 30") {
		t.Fatalf("written config = %q, want it to contain default_limit = 30", data)
	}
	// The baseline is seeded first (no-replace), so the file carries the
	// full baseline schema instead of a one-key minimal file. Comments are
	// not preserved across SaveKnown (documented semantics), so the baseline
	// is asserted by key content.
	for _, want := range []string{
		"max_pages", "request_interval", "retry_attempts",
		"retry_delay", "instance_cooldown", "proxy", "log_level", "log_format",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("written config lost baseline key %q:\n%s", want, data)
		}
	}
}

func TestConfigPublishFailureExits1(t *testing.T) {
	home := tempHome(t)
	// Make the app dir path a regular file so MkdirAll inside
	// EnsureDefaultConfigFile fails; a config publish failure is a plain
	// error (exit 1), never a usage error.
	if err := os.WriteFile(filepath.Join(home, ".nitter-cli"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	code, _, errOut := runCLI(t, "", "config", "path")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "create app dir") {
		t.Fatalf("stderr = %q, want the publish failure cause", errOut)
	}
}

func TestConfigSetThenGet(t *testing.T) {
	home := tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "set", "default_limit", "30")
	if code != 0 {
		t.Fatalf("set exit = %d, want 0 (stderr %q)", code, errOut)
	}
	code, out, _ := runCLI(t, "", "config", "get", "default_limit")
	if code != 0 {
		t.Fatalf("get exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != "default_limit = 30" {
		t.Fatalf("get output = %q, want %q", got, "default_limit = 30")
	}
	data, err := os.ReadFile(configFile(home))
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), "default_limit = 30") {
		t.Fatalf("written config = %q, want it to contain default_limit = 30", data)
	}
}

func TestConfigGetAllListsExactlyTheKnownKeys(t *testing.T) {
	tempHome(t)
	code, out, _ := runCLI(t, "", "config", "get")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := []string{
		"default_limit = 20",
		"max_pages = 5",
		"request_interval = 1s",
		"retry_attempts = 2",
		"retry_delay = 1s",
		"instance_cooldown = 60s",
		"proxy = ",
		"log_level = info",
		"log_format = text",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines (%q), want %d", len(lines), out, len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i+1, lines[i], want[i])
		}
	}
}

func TestConfigGetUnknownKeyExits2(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "get", "nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "nope") {
		t.Fatalf("stderr = %q, want it to name the unknown key", errOut)
	}
}

func TestConfigGetPrefersEnvOverFile(t *testing.T) {
	home := tempHome(t)
	if err := os.MkdirAll(filepath.Dir(configFile(home)), 0o700); err != nil {
		t.Fatalf("create app dir: %v", err)
	}
	if err := os.WriteFile(configFile(home), []byte("default_limit = 7\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("NITTER_DEFAULT_LIMIT", "9")

	code, out, _ := runCLI(t, "", "config", "get", "default_limit")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != "default_limit = 9" {
		t.Fatalf("get output = %q, want the env override to win", got)
	}
}

func TestConfigGetMapsValidationErrorToUsageError(t *testing.T) {
	home := tempHome(t)
	if err := os.MkdirAll(filepath.Dir(configFile(home)), 0o700); err != nil {
		t.Fatalf("create app dir: %v", err)
	}
	if err := os.WriteFile(configFile(home), []byte("request_interval = \"abc\"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	code, _, errOut := runCLI(t, "", "config", "get", "default_limit")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (bad config value is a usage error)", code)
	}
	if !strings.Contains(errOut, "request_interval") {
		t.Fatalf("stderr = %q, want it to name the offending key", errOut)
	}
}

func TestConfigSetUnknownKeyExits2WithoutCreatingFile(t *testing.T) {
	home := tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "set", "nope", "1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "nope") {
		t.Fatalf("stderr = %q, want it to name the unknown key", errOut)
	}
	mustNotExist(t, filepath.Join(home, ".nitter-cli"))
}

func TestConfigSetInvalidValuesAreUsageErrorsBeforeAnyWrite(t *testing.T) {
	home := tempHome(t)
	// Negative values must be passed after the `--` separator: pflag would
	// otherwise claim "-5"/"-1s" as (unknown) shorthand flags.
	for _, tc := range []struct{ args []string }{
		{[]string{"config", "set", "default_limit", "abc"}},
		{[]string{"config", "set", "default_limit", "--", "-5"}},
		{[]string{"config", "set", "default_limit", "3.5"}},
		{[]string{"config", "set", "max_pages", "xyz"}},
		{[]string{"config", "set", "max_pages", "--", "-1"}},
		{[]string{"config", "set", "retry_attempts", "--", "-2"}},
		{[]string{"config", "set", "request_interval", "abc"}},
		{[]string{"config", "set", "request_interval", "--", "-1s"}},
		{[]string{"config", "set", "retry_delay", "5"}},
		{[]string{"config", "set", "instance_cooldown", "--", "-5m"}},
		{[]string{"config", "set", "log_level", "bogus"}},
		{[]string{"config", "set", "log_level", "warn"}},
		{[]string{"config", "set", "log_format", "xml"}},
	} {
		code, _, errOut := runCLI(t, "", tc.args...)
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2", tc.args, code)
		}
		key := tc.args[2]
		if !strings.Contains(errOut, key) {
			t.Fatalf("%v: stderr = %q, want it to name the key", tc.args, errOut)
		}
	}
	// Validation precedes any file side effect: nothing was written.
	mustNotExist(t, configFile(home))
}

func TestConfigSetBareNegativeValueIsRejectedByFlagParsing(t *testing.T) {
	home := tempHome(t)
	// Without the `--` separator pflag claims "-5" as an unknown shorthand
	// flag; WrapFlagError still turns that into a usage error (exit 2) and
	// nothing is written.
	code, _, errOut := runCLI(t, "", "config", "set", "default_limit", "-5")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown shorthand flag") {
		t.Fatalf("stderr = %q, want the flag-parse error", errOut)
	}
	mustNotExist(t, configFile(home))
}

func TestConfigSetZeroAndBoundaryValues(t *testing.T) {
	tempHome(t)
	for _, tc := range []struct{ key, value, want string }{
		{"default_limit", "0", "default_limit = 0"},
		{"request_interval", "0s", "request_interval = 0s"},
		{"log_level", "debug", "log_level = debug"},
		{"log_format", "json", "log_format = json"},
	} {
		code, _, errOut := runCLI(t, "", "config", "set", tc.key, tc.value)
		if code != 0 {
			t.Fatalf("set %s %s: exit = %d, want 0 (stderr %q)", tc.key, tc.value, code, errOut)
		}
		code, out, _ := runCLI(t, "", "config", "get", tc.key)
		if code != 0 {
			t.Fatalf("get %s exit = %d, want 0", tc.key, code)
		}
		if got := strings.TrimSpace(out); got != tc.want {
			t.Fatalf("get %s = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestConfigSetValueFromStdin(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "2s\n", "config", "set", "request_interval")
	if code != 0 {
		t.Fatalf("set exit = %d, want 0 (stderr %q)", code, errOut)
	}
	code, out, _ := runCLI(t, "", "config", "get", "request_interval")
	if code != 0 {
		t.Fatalf("get exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != "request_interval = 2s" {
		t.Fatalf("get output = %q, want %q", got, "request_interval = 2s")
	}

	// A CRLF line ending is stripped from the piped value.
	code, _, errOut = runCLI(t, "45s\r\n", "config", "set", "instance_cooldown")
	if code != 0 {
		t.Fatalf("set (CRLF) exit = %d, want 0 (stderr %q)", code, errOut)
	}
	code, out, _ = runCLI(t, "", "config", "get", "instance_cooldown")
	if code != 0 {
		t.Fatalf("get exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != "instance_cooldown = 45s" {
		t.Fatalf("get output = %q, want %q (CRLF must be stripped)", got, "instance_cooldown = 45s")
	}
}

func TestConfigSetMissingValueOnTTYIsUsageError(t *testing.T) {
	home := tempHome(t)
	var out, errOut bytes.Buffer
	cmd := config.New(&invocation.Streams{
		In: strings.NewReader(""), Out: &out, Err: &errOut, InIsTTY: true,
	})
	cmd.SetArgs([]string{"set", "proxy"})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want a usage error")
	}
	var ue *invocation.UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v (%T), want *invocation.UsageError", err, err)
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("error = %v, want it to name the key", err)
	}
	mustNotExist(t, configFile(home))
}

func TestConfigUnsetRoundtripFallsBackToDefault(t *testing.T) {
	tempHome(t)
	if code, _, errOut := runCLI(t, "", "config", "set", "default_limit", "30"); code != 0 {
		t.Fatalf("set exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if code, _, errOut := runCLI(t, "", "config", "unset", "default_limit"); code != 0 {
		t.Fatalf("unset exit = %d, want 0 (stderr %q)", code, errOut)
	}
	code, out, _ := runCLI(t, "", "config", "get", "default_limit")
	if code != 0 {
		t.Fatalf("get exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != "default_limit = 20" {
		t.Fatalf("get output = %q, want the default %q after unset", got, "default_limit = 20")
	}
}

func TestConfigUnsetPreservesOtherKeys(t *testing.T) {
	tempHome(t)
	if code, _, errOut := runCLI(t, "", "config", "set", "default_limit", "30"); code != 0 {
		t.Fatalf("set default_limit exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if code, _, errOut := runCLI(t, "", "config", "set", "max_pages", "2"); code != 0 {
		t.Fatalf("set max_pages exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if code, _, errOut := runCLI(t, "", "config", "unset", "default_limit"); code != 0 {
		t.Fatalf("unset exit = %d, want 0 (stderr %q)", code, errOut)
	}
	code, out, _ := runCLI(t, "", "config", "get", "max_pages")
	if code != 0 {
		t.Fatalf("get exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out); got != "max_pages = 2" {
		t.Fatalf("get output = %q, want %q (other keys must survive unset)", got, "max_pages = 2")
	}
}

func TestConfigUnsetUnknownKeyExits2(t *testing.T) {
	home := tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "unset", "nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "nope") {
		t.Fatalf("stderr = %q, want it to name the unknown key", errOut)
	}
	mustNotExist(t, filepath.Join(home, ".nitter-cli"))
}

func TestConfigSetUnsetRejectsArrayTables(t *testing.T) {
	tempHome(t)
	for _, args := range [][]string{
		{"config", "set", "instances", "x"},
		{"config", "set", "watch", "x"},
		{"config", "unset", "instances"},
		{"config", "unset", "watch"},
	} {
		code, _, errOut := runCLI(t, "", args...)
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2", args, code)
		}
		if !strings.Contains(errOut, "config.toml") {
			t.Fatalf("%v: stderr = %q, want guidance to edit config.toml directly", args, errOut)
		}
	}
}

func TestConfigArgCountViolationsAreUsageErrors(t *testing.T) {
	tempHome(t)
	for _, args := range [][]string{
		{"config", "path", "extra"},
		{"config", "get", "a", "b"},
		{"config", "set"},
		{"config", "set", "onlykey", "v", "extra"},
		{"config", "unset"},
		{"config", "unset", "a", "b"},
	} {
		code, _, errOut := runCLI(t, "", args...)
		if code != 2 {
			t.Fatalf("%v: exit = %d, want 2 (stderr %q)", args, code, errOut)
		}
	}
}

func TestConfigUnknownSubcommandExits1(t *testing.T) {
	tempHome(t)
	code, _, errOut := runCLI(t, "", "config", "frobnicate")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Fatalf("stderr = %q, want the unknown-command error", errOut)
	}
}
