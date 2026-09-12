package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/buildinfo"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
)

// fakeAPI serves a canned GitHub-releases body and points the command's API
// seam at it. It must live in the internal test package: the API base is a
// test seam on the command (apiBaseOverride), and external test files cannot
// reach it — nor may they import internal/cli here (import cycle).
func fakeAPI(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	apiBaseOverride = srv.URL
}

func release(tag string, prerelease bool) string {
	return `{"tag_name":"` + tag + `","name":"` + tag + `","html_url":"https://github.com/shitianyaa/nitter-cli/releases/tag/` + tag + `","draft":false,"prerelease":` + boolText(prerelease) + `,"published_at":"2026-09-01T00:00:00Z"}`
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// setVersion pins the build version for one test (save/restore around it).
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := buildinfo.Version
	buildinfo.Version = v
	t.Cleanup(func() { buildinfo.Version = old })
}

// runUpdate drives the update command directly (cobra, not the composition
// root — internal tests cannot import internal/cli) and maps the error with
// the same ExitCode rule cli.Run applies.
func runUpdate(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{CTX: context.Background()},
	}
	cmd := New(s)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	code := 0
	if err != nil {
		code = invocation.ExitCode(err)
		if code == 1 {
			errOut.WriteString("error: " + err.Error())
		}
	}
	return code, out.String(), errOut.String()
}

// TestUpdateCheckReportsUpdateAvailable: a released build against a fake API
// picks the highest stable release and reports the outdated state (exit 0 —
// outdatedness is reported, not failed).
func TestUpdateCheckReportsUpdateAvailable(t *testing.T) {
	setVersion(t, "0.1.0")
	fakeAPI(t, "["+release("v0.1.0", false)+","+release("v0.2.0", false)+"]", http.StatusOK)

	code, out, errOut := runUpdate(t, "--check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	for _, want := range []string{"0.1.0", "0.2.0", "update available", "releases/tag/v0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestUpdateCheckReportsUpToDate(t *testing.T) {
	setVersion(t, "0.2.0")
	fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)

	code, out, _ := runUpdate(t, "--check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "up to date") {
		t.Errorf("output %q missing the up-to-date notice", out)
	}
}

// Newer-than-latest (e.g. an installed prerelease when only stables are
// published) is not "outdated".
func TestUpdateCheckNewerInstalledIsUpToDate(t *testing.T) {
	setVersion(t, "0.3.0")
	fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)

	code, out, _ := runUpdate(t, "--check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "up to date") {
		t.Errorf("output %q missing the up-to-date notice", out)
	}
}

func TestUpdateCheckPrereleaseFlag(t *testing.T) {
	setVersion(t, "0.2.0")
	fakeAPI(t, "["+release("v0.2.0", false)+","+release("v0.3.0-rc.1", true)+"]", http.StatusOK)

	code, out, _ := runUpdate(t, "--check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "0.2.0") || strings.Contains(out, "0.3.0-rc.1") {
		t.Errorf("without --prerelease the rc must not be selected: %q", out)
	}

	code, out, _ = runUpdate(t, "--check", "--prerelease")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "0.3.0-rc.1") {
		t.Errorf("with --prerelease the rc must be selected: %q", out)
	}
}

// TestUpdateCheckJSONShape pins the machine document of --check --json.
func TestUpdateCheckJSONShape(t *testing.T) {
	setVersion(t, "0.1.0")
	fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)

	code, out, _ := runUpdate(t, "--check", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var doc struct {
		Current    string `json:"current"`
		Latest     string `json:"latest"`
		Outdated   bool   `json:"outdated"`
		Prerelease bool   `json:"prerelease"`
		ReleaseURL string `json:"release_url"`
		DevBuild   bool   `json:"development_build"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if doc.Current != "0.1.0" || doc.Latest != "0.2.0" || !doc.Outdated || doc.Prerelease || doc.DevBuild {
		t.Errorf("document = %+v", doc)
	}
	if doc.ReleaseURL != "https://github.com/shitianyaa/nitter-cli/releases/tag/v0.2.0" {
		t.Errorf("release_url = %q", doc.ReleaseURL)
	}
	// One JSON document on one line.
	if n := strings.Count(strings.TrimSuffix(out, "\n"), "\n"); n != 0 {
		t.Errorf("--json printed %d lines, want one document", n+1)
	}
}

// A GitHub failure is a runtime failure (exit 1), never a usage error.
func TestUpdateCheckAPIFailureExits1(t *testing.T) {
	setVersion(t, "0.1.0")
	fakeAPI(t, `{"message":"nope"}`, http.StatusForbidden)

	code, _, errOut := runUpdate(t, "--check")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if strings.Contains(errOut, "nope") {
		t.Errorf("stderr %q leaks the API response body", errOut)
	}
}

// No usable release (empty repo or all drafts) is also a runtime failure.
func TestUpdateCheckNoReleaseExits1(t *testing.T) {
	setVersion(t, "0.1.0")
	fakeAPI(t, "[]", http.StatusOK)

	if code, _, _ := runUpdate(t, "--check"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}
