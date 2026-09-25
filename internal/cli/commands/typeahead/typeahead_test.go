package typeahead_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/typeahead"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
)

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"NITTER_DEFAULT_LIMIT", "NITTER_LOG_LEVEL", "NITTER_LOG_FORMAT", "NITTER_FETCH_BACKEND",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, "")
	}
	return home
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".nitter-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestTypeaheadHappyPath(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"fx\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/typeahead" || r.URL.Query().Get("q") != "nas" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"num_results":1,"users":[{"screen_name":"nasa","name":"NASA"}]}`))
	}))
	defer srv.Close()
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = "" })

	// TTY: one human table row per profile (same output-mode contract as
	// followers_test.go; the @nasa row only exists in the human mode).
	var outBuf, errBuf strings.Builder
	s := &invocation.Streams{
		Out:      &outBuf,
		Err:      &errBuf,
		OutIsTTY: true,
		CTX:      context.Background(),
	}
	cmd := typeahead.New(s)
	cmd.SetArgs([]string{"nas"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(outBuf.String(), "@nasa") {
		t.Errorf("out = %q, want the @nasa row", outBuf.String())
	}

	// Non-TTY default is NDJSON, same as followers: one profile envelope
	// with source "typeahead:<query>" and instance "FxTwitter".
	code, out, errOut := runCLI(t, "typeahead", "nas")
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, `"handle":"nasa"`) || !strings.Contains(out, `"source":"typeahead:nas"`) {
		t.Errorf("out = %q, want the typeahead profile envelope", out)
	}
}

func TestTypeaheadUsageErrors(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "")

	if code, _, errOut := runCLI(t, "typeahead", "   "); code != 2 {
		t.Fatalf("blank query: exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if code, _, errOut := runCLI(t, "typeahead", "nas", "--limit", "0"); code != 2 ||
		!strings.Contains(errOut, "--limit must be >= 1") {
		t.Fatalf("limit 0: exit = %d stderr = %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "typeahead"); code != 2 {
		t.Fatalf("no args: exit = %d, want 2 (stderr %q)", code, errOut)
	}
}
