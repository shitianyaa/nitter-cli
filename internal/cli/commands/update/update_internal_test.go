package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// runUpdateWith drives the command with caller-supplied streams, so the
// confirm prompt can be exercised (InIsTTY + a stdin reader) without a real
// terminal.
func runUpdateWith(t *testing.T, s *invocation.Streams, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	s.Out = &out
	s.Err = &errOut
	if s.CTX == nil {
		s.CTX = context.Background()
	}
	if s.RootOptions == nil {
		s.RootOptions = &invocation.RootOptions{CTX: s.CTX}
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

// panicReader fails the test if anything reads stdin — proving the non-TTY
// path never blocks on a pipe.
type panicReader struct{ t *testing.T }

func (p panicReader) Read([]byte) (int, error) {
	p.t.Error("stdin must not be read when it is not a terminal")
	return 0, nil
}

// Declining the prompt (an empty line, i.e. accepting the [y/N] default)
// exits 0 and reports that nothing was installed.
func TestUpdateConfirmDeclinedExitsZeroWithoutInstalling(t *testing.T) {
	setVersion(t, "0.1.0")
	fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)
	s := &invocation.Streams{In: strings.NewReader("\n"), InIsTTY: true}

	code, out, errOut := runUpdateWith(t, s)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "not installed") {
		t.Errorf("out = %q, want a 'not installed' line", out)
	}
	if strings.Contains(out, "downloading") {
		t.Errorf("a declined prompt must not download: %q", out)
	}
}

func TestUpdateConfirmRejectsNonYesAnswers(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "\n", "maybe\n"} {
		setVersion(t, "0.1.0")
		fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)
		s := &invocation.Streams{In: strings.NewReader(answer), InIsTTY: true}
		code, out, _ := runUpdateWith(t, s)
		if code != 0 {
			t.Errorf("answer %q: exit = %d, want 0", answer, code)
		}
		if !strings.Contains(out, "not installed") {
			t.Errorf("answer %q: out = %q, want 'not installed'", answer, out)
		}
	}
}

// A non-TTY without --confirm reports and exits 0 without ever reading stdin.
func TestUpdateNonTTYWithoutConfirmDoesNotReadStdin(t *testing.T) {
	setVersion(t, "0.1.0")
	fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)
	s := &invocation.Streams{In: panicReader{t: t}, InIsTTY: false}

	code, out, errOut := runUpdateWith(t, s)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "--confirm") {
		t.Errorf("out = %q, want a pointer to --confirm", out)
	}
	if !strings.Contains(out, "not installed") {
		t.Errorf("out = %q, want an explicit 'not installed'", out)
	}
}

// Up to date in install mode never prompts and never downloads.
func TestUpdateInstallUpToDateDoesNotPrompt(t *testing.T) {
	setVersion(t, "0.2.0")
	fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)
	s := &invocation.Streams{In: panicReader{t: t}, InIsTTY: true}

	code, out, errOut := runUpdateWith(t, s)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "up to date") {
		t.Errorf("out = %q, want 'up to date'", out)
	}
}

// A go install installation is refused with the matching install line: an
// update would be silently undone by the next `go install`.
func TestUpdateInstallRefusesGoInstallSource(t *testing.T) {
	setVersion(t, "0.1.0")
	fakeAPI(t, "["+release("v0.2.0", false)+"]", http.StatusOK)

	// Put the "running binary" inside a fake GOBIN so DetectSource sees it.
	gobin := t.TempDir()
	exe := filepath.Join(gobin, "nitter")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("GOBIN", gobin)
	oldExe := osExecutable
	osExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { osExecutable = oldExe })

	s := &invocation.Streams{In: strings.NewReader("y\n"), InIsTTY: true}
	code, _, errOut := runUpdateWith(t, s)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "go install") {
		t.Errorf("stderr = %q, want the go install guidance", errOut)
	}
}

// --confirm with --check is a flag combination with no meaning: usage error.
func TestUpdateConfirmWithCheckIsUsageError(t *testing.T) {
	setVersion(t, "0.1.0")
	code, _, _ := runUpdate(t, "--check", "--confirm")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

// An invalid --proxy scheme is a usage error before any network call.
func TestUpdateInvalidProxyIsUsageError(t *testing.T) {
	setVersion(t, "0.1.0")
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		RootOptions: &invocation.RootOptions{Proxy: "ftp://nope"},
	}
	code, _, _ := runUpdateWith(t, s, "--check")
	if code != 2 {
		t.Errorf("exit = %d, want 2 for an invalid proxy scheme", code)
	}
}
