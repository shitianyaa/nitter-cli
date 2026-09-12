package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/buildinfo"
	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

func TestRunVersion(t *testing.T) {
	buildinfo.Version = "v0.1.0"
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{"--version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "nitter version v0.1.0" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{"nope"}, strings.NewReader(""), &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRunUnknownFlagIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{"--nope"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// TestRunVersionAndHelpNeverCreateConfig pins the PersistentPreRunE contract:
// --version and --help/-h are handled by cobra before the PreRun stage, and
// the auto-generated help subcommand is skipped by the hook itself, so none of
// them may publish (or otherwise create) the baseline config.
func TestRunVersionAndHelpNeverCreateConfig(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-h"}, {"--help"}, {"help"}, {"help", "config"}} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		var out, errOut bytes.Buffer
		if code := cli.Run(args, strings.NewReader(""), &out, &errOut); code != 0 {
			t.Fatalf("run %v: exit = %d, want 0 (stderr %q)", args, code, errOut.String())
		}
		if _, err := os.Stat(filepath.Join(home, ".nitter-cli")); !os.IsNotExist(err) {
			t.Fatalf("run %v must not create the app dir (stat err = %v)", args, err)
		}
	}
}

func TestRunBareInvocationPublishesBaselineConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut.String())
	}
	data, err := os.ReadFile(filepath.Join(home, ".nitter-cli", "config.toml"))
	if err != nil {
		t.Fatalf("read published baseline config: %v", err)
	}
	if string(data) != settings.DefaultConfigTOML {
		t.Fatalf("published baseline = %q, want settings.DefaultConfigTOML", data)
	}
}
