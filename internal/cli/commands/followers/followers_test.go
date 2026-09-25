package followers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/followers"
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

func TestFollowersHappyPathTable(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"fx\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/profile/NASA/followers" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"followers":[{"screen_name":"a","name":"A","followers":10}]}`))
	}))
	defer srv.Close()
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = "" })

	// TTY: one human table row per follower profile (same contract as
	// following_test.go; the @a row only exists in the human mode).
	var outBuf, errBuf strings.Builder
	s := &invocation.Streams{
		Out:      &outBuf,
		Err:      &errBuf,
		OutIsTTY: true,
		CTX:      context.Background(),
	}
	cmd := followers.New(s)
	cmd.SetArgs([]string{"NASA"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(outBuf.String(), "@a") {
		t.Errorf("out = %q, want the @a row", outBuf.String())
	}

	// Non-TTY default is NDJSON, same as following: one profile envelope
	// with source "followers:<handle>" and instance "FxTwitter".
	code, out, errOut := runCLI(t, "followers", "NASA")
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, `"handle":"a"`) || !strings.Contains(out, `"source":"followers:NASA"`) {
		t.Errorf("out = %q, want the followers profile envelope", out)
	}
}

func TestFollowersUsageErrors(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "")

	cases := []struct {
		name string
		args []string
	}{
		{"no args", []string{"followers"}},
		{"bad handle", []string{"followers", "bad handle!"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := runCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
			}
		})
	}

	code, _, errOut := runCLI(t, "followers", "NASA", "--limit", "0")
	if code != 2 || !strings.Contains(errOut, "--limit must be >= 1") {
		t.Fatalf("exit = %d stderr = %q, want limit usage error", code, errOut)
	}
}
