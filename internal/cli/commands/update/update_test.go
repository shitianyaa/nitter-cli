package update_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/cli"
)

// tempHome redirects the home directory to a fresh temp dir (the root
// PersistentPreRunE publishes the baseline config there) and neutralizes
// proxy env overrides.
func tempHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestUpdateWithoutCheckPrintsGuidance: bare `twitter update` performs no
// network call and prints the package-manager/manual-download guidance
// (MVP does not self-install), exit 0.
func TestUpdateWithoutCheckPrintsGuidance(t *testing.T) {
	tempHome(t)
	code, out, errOut := runCLI(t, "update")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	for _, want := range []string{"package manager", "releases", "update --check"} {
		if !strings.Contains(out, want) {
			t.Errorf("guidance output %q missing %q", out, want)
		}
	}
}

// TestUpdateDevBuildCheckSkipsNetwork: a dev build has no version metadata;
// --check reports the development-build notice instead of querying, exit 0.
func TestUpdateDevBuildCheckSkipsNetwork(t *testing.T) {
	tempHome(t)
	code, out, errOut := runCLI(t, "update", "--check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "development build") {
		t.Errorf("output %q missing the development-build notice", out)
	}

	code, out, _ = runCLI(t, "update", "--check", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var doc struct {
		Current          string `json:"current"`
		DevelopmentBuild bool   `json:"development_build"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json output is not one JSON document: %v (%q)", err, out)
	}
	if doc.Current != "dev" || !doc.DevelopmentBuild {
		t.Errorf("dev --json = %+v, want current=dev development_build=true", doc)
	}
}

// TestUpdateFlagCombinationsAreUsageErrors: --json (and --prerelease) only
// combine with --check; anything else is the javdb-style usage error (exit 2).
func TestUpdateFlagCombinationsAreUsageErrors(t *testing.T) {
	tempHome(t)
	for _, args := range [][]string{
		{"update", "--json"},
		{"update", "--prerelease"},
		{"update", "--json", "--prerelease"},
		{"update", "--no-such-flag"},
	} {
		code, _, errOut := runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", args, code, errOut)
		}
	}
}
