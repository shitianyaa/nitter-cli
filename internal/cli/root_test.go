package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/buildinfo"
	"github.com/shitianyaa/twitter-cli/internal/cli"
)

func TestRunVersion(t *testing.T) {
	buildinfo.Version = "v0.1.0"
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{"--version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "twitter version v0.1.0" {
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
