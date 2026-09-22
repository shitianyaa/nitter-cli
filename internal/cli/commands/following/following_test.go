package following_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/following"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/sdk"
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

func writeConfig(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".nitter-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := "retry_attempts = -1\nretry_delay = \"-1s\"\nrequest_interval = \"-1s\"\ninstance_cooldown = \"-1s\"\nfetch_backend = \"fx\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestFollowing_ArgsValidation(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	cases := []struct {
		name string
		args []string
	}{
		{"no args", []string{"following"}},
		{"too many args", []string{"following", "user1", "user2"}},
		{"with @ prefix", []string{"following", "@NASA"}},
		{"invalid chars", []string{"following", "user-name"}},
		{"handle too long", []string{"following", "toolonghandle1234567890"}},
		{"negative limit", []string{"following", "NASA", "--limit", "-1"}},
		{"both json and ndjson", []string{"following", "NASA", "--json", "--ndjson"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("runCLI(%v) exit code = %d, want 2. out=%q, errOut=%q", tc.args, code, out, errOut)
			}
		})
	}
}

func TestFollowing_MockServer(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/profile/empty/following"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":  200,
				"users": []any{},
			})
		case strings.Contains(r.URL.Path, "/profile/nasa/following"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"users": []map[string]any{
					{
						"id":              "1",
						"username":        "ESA",
						"name":            "European Space Agency",
						"bio":             "Europe's gateway to space.",
						"followers_count": 2000000,
					},
					{
						"id":              "2",
						"username":        "JAXA",
						"name":            "JAXA Official",
						"bio":             "Japan Aerospace Exploration Agency",
						"followers_count": 1000000,
					},
				},
			})
		case strings.Contains(r.URL.Path, "/profile/erroruser/following"):
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    500,
				"message": "upstream server error",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	t.Run("human output formatted table rows on TTY", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: true,
			CTX:      context.Background(),
		}
		cmd := following.New(s)
		cmd.SetArgs([]string{"nasa"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), outBuf.String())
		}
		wantESA := "@ESA	European Space Agency	2000000	Europe's gateway to space."
		if lines[0] != wantESA {
			t.Errorf("line 0 = %q, want %q", lines[0], wantESA)
		}
	})

	t.Run("empty results on TTY prints (empty) to stderr", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: true,
			CTX:      context.Background(),
		}
		cmd := following.New(s)
		cmd.SetArgs([]string{"empty"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if outBuf.String() != "" {
			t.Errorf("out = %q, want empty", outBuf.String())
		}
		if strings.TrimSpace(errBuf.String()) != "(empty)" {
			t.Errorf("err = %q, want (empty)", errBuf.String())
		}
	})

	t.Run("non-TTY default is ndjson", func(t *testing.T) {
		code, out, errOut := runCLI(t, "following", "nasa")
		if code != 0 {
			t.Fatalf("code = %d, want 0; errOut=%s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), out)
		}
		var env pipeline.Envelope
		if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
			t.Fatalf("unmarshal line 0: %v", err)
		}
		if env.Kind != pipeline.KindProfile {
			t.Errorf("env.Kind = %q, want %q", env.Kind, pipeline.KindProfile)
		}
		if env.ID != "ESA" {
			t.Errorf("env.ID = %q, want ESA", env.ID)
		}
		if env.Meta == nil || env.Meta.Source != "following:nasa" || env.Meta.Instance != "FxTwitter" {
			t.Errorf("env.Meta = %+v, want source=following:nasa, instance=FxTwitter", env.Meta)
		}
	})

	t.Run("explicit ndjson flag", func(t *testing.T) {
		code, out, errOut := runCLI(t, "following", "nasa", "--ndjson")
		if code != 0 {
			t.Fatalf("code = %d, want 0; errOut=%s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), out)
		}
	})

	t.Run("explicit json flag", func(t *testing.T) {
		code, out, errOut := runCLI(t, "following", "nasa", "--json")
		if code != 0 {
			t.Fatalf("code = %d, want 0; errOut=%s", code, errOut)
		}
		var profiles []nitter.Profile
		if err := json.Unmarshal([]byte(out), &profiles); err != nil {
			t.Fatalf("unmarshal json profiles: %v", err)
		}
		if len(profiles) != 2 {
			t.Fatalf("len(profiles) = %d, want 2", len(profiles))
		}
		if profiles[0].Handle != "ESA" || profiles[1].Handle != "JAXA" {
			t.Errorf("unexpected profiles: %+v", profiles)
		}
	})

	t.Run("empty results json", func(t *testing.T) {
		code, out, errOut := runCLI(t, "following", "empty", "--json")
		if code != 0 {
			t.Fatalf("code = %d, want 0; errOut=%s", code, errOut)
		}
		if strings.TrimSpace(out) != "[]" {
			t.Errorf("out = %q, want []", out)
		}
	})

	t.Run("empty results ndjson", func(t *testing.T) {
		code, out, errOut := runCLI(t, "following", "empty", "--ndjson")
		if code != 0 {
			t.Fatalf("code = %d, want 0; errOut=%s", code, errOut)
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("out = %q, want empty", out)
		}
	})

	t.Run("upstream error exits 1", func(t *testing.T) {
		code, _, _ := runCLI(t, "following", "erroruser")
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
	})
}
