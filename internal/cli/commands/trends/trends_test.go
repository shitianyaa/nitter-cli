package trends_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/trends"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
)

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"NITTER_DEFAULT_LIMIT", "NITTER_LOG_LEVEL", "NITTER_LOG_FORMAT",
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
		t.Fatalf("write config fixture: %v", err)
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestTrendsValidation(t *testing.T) {
	tempHome(t)

	// Extra args -> exit 2
	code, _, errOut := runCLI(t, "trends", "unexpected")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}

	// Non-positive limit -> exit 2 (0 was the "all" spelling and is gone)
	code, _, errOut = runCLI(t, "trends", "--limit=-1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	code, _, errOut = runCLI(t, "trends", "--limit=0")
	if code != 2 {
		t.Fatalf("--limit=0: exit = %d, want 2 (stderr %q)", code, errOut)
	}

	// --json and --ndjson together -> exit 2
	code, _, errOut = runCLI(t, "trends", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
}

func TestTrendsSuccessAndModes(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/2/trends" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"trends": []map[string]any{
					{
						"name":           "#AI",
						"rank":           1,
						"context":        "Technology · Trending",
						"tweet_count":    42000,
						"grouped_topics": []string{"AI", "Tech"},
					},
					{
						"name":        "SpaceX",
						"rank":        2,
						"context":     "Science · Trending",
						"tweet_count": 15000,
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	t.Run("TTY table output with header", func(t *testing.T) {
		var out, errOut strings.Builder
		s := &invocation.Streams{
			In:          strings.NewReader(""),
			Out:         &out,
			Err:         &errOut,
			OutIsTTY:    true,
			RootOptions: &invocation.RootOptions{},
		}
		cmd := trends.New(s)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		outStr := out.String()
		if !strings.Contains(outStr, "#\tTREND TOPIC\tCONTEXT\tTWEETS") {
			t.Errorf("expected header, got:\n%s", outStr)
		}
		if !strings.Contains(outStr, "1\t#AI\tTechnology · Trending\t42000") {
			t.Errorf("expected row 1, got:\n%s", outStr)
		}
		if !strings.Contains(outStr, "2\tSpaceX\tScience · Trending\t15000") {
			t.Errorf("expected row 2, got:\n%s", outStr)
		}
	})

	t.Run("NDJSON output envelope", func(t *testing.T) {
		code, out, _ := runCLI(t, "trends", "--ndjson")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"kind":"trend"`) || !strings.Contains(out, `"name":"#AI"`) {
			t.Errorf("out = %q, want trend NDJSON", out)
		}
	})

	t.Run("JSON array output", func(t *testing.T) {
		code, out, _ := runCLI(t, "trends", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.HasPrefix(strings.TrimSpace(out), "[") {
			t.Errorf("out = %q, want JSON array", out)
		}
	})

	t.Run("Limit flag caps output", func(t *testing.T) {
		code, out, _ := runCLI(t, "trends", "--limit=1", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		var list []any
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("len(list) = %d, want 1", len(list))
		}
	})
}

func TestTrendsEmpty(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":   200,
			"trends": []map[string]any{},
		})
	}))
	defer srv.Close()

	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		RootOptions: &invocation.RootOptions{},
	}
	cmd := trends.New(s)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(errOut.String(), "(empty)") {
		t.Errorf("errOut = %q, want (empty)", errOut.String())
	}
}
