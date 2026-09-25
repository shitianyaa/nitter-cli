package thread_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/thread"
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

func TestThreadHappyPathTable(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"fx\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/thread/101" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"thread":[{"id_str":"101","author":{"screen_name":"n"},"text":"root"},{"id_str":"102","author":{"screen_name":"n"},"text":"two"}]}`))
	}))
	defer srv.Close()
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = "" })

	// TTY: one human table row per status, root first (same contract as
	// followers_test.go / following_test.go; the text cells only exist in
	// the human mode).
	var outBuf, errBuf strings.Builder
	s := &invocation.Streams{
		Out:      &outBuf,
		Err:      &errBuf,
		OutIsTTY: true,
		CTX:      context.Background(),
	}
	cmd := thread.New(s)
	cmd.SetArgs([]string{"101"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(outBuf.String(), "root") || !strings.Contains(outBuf.String(), "two") {
		t.Errorf("out = %q, want both tweet rows", outBuf.String())
	}

	// Non-TTY default is NDJSON, same as followers: one tweet envelope per
	// status with kind "tweet", the status ID as id, and source
	// "thread:<id>" with the FxTwitter provenance.
	code, out, errOut := runCLI(t, "thread", "101")
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, `"kind":"tweet"`) || !strings.Contains(out, `"id":"101"`) || !strings.Contains(out, `"source":"thread:101"`) {
		t.Errorf("out = %q, want the thread tweet envelope", out)
	}
}

func TestThreadUsageErrors(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "")

	cases := []struct {
		name string
		args []string
	}{
		{"no args", []string{"thread"}},
		{"non-numeric id", []string{"thread", "abc"}},
		{"two positional args", []string{"thread", "1", "2"}},
		{"json and ndjson", []string{"thread", "101", "--json", "--ndjson"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := runCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
			}
		})
	}
}
