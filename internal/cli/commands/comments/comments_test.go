package comments_test

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
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/comments"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
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

func TestComments_ArgsValidation(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	cases := []struct {
		name string
		args []string
	}{
		{"no args", []string{"comments"}},
		{"too many args", []string{"comments", "123", "456"}},
		{"invalid status ref", []string{"comments", "invalid_ref_shape"}},
		{"negative limit", []string{"comments", "123456789", "--limit", "-1"}},
		{"invalid sort", []string{"comments", "123456789", "--sort", "alphabetical"}},
		{"both json and ndjson", []string{"comments", "123456789", "--json", "--ndjson"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, _ := runCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("runCLI(%v) exit code = %d, want 2", tc.args, code)
			}
		})
	}
}

func TestComments_MockServer(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	var lastRankingMode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastRankingMode = r.URL.Query().Get("ranking_mode")
		if lastRankingMode == "" {
			lastRankingMode = r.URL.Query().Get("rankingMode")
		}

		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/conversation/200") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"tweet": map[string]any{
					"id":   "200",
					"text": "Root tweet text",
					"author": map[string]any{
						"screen_name": "NASA",
						"name":        "NASA Official",
					},
					"created_at": "Sun Jul 05 09:09:40 +0000 2026",
				},
				"thread": []any{},
				"replies": []map[string]any{
					{
						"id":   "201",
						"text": "First reply text",
						"author": map[string]any{
							"screen_name": "astronomer",
							"name":        "Astronomer",
						},
						"created_at": "Sun Jul 05 09:15:00 +0000 2026",
					},
					{
						"id":   "202",
						"text": "Second reply text",
						"author": map[string]any{
							"screen_name": "physicist",
							"name":        "Physicist",
						},
						"created_at": "Sun Jul 05 09:20:00 +0000 2026",
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	t.Run("human output on TTY shows root status and indented replies", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: true,
			CTX:      context.Background(),
		}
		cmd := comments.New(s)
		cmd.SetArgs([]string{"200"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
		if len(lines) != 3 {
			t.Fatalf("lines count = %d, want 3; out=%q", len(lines), outBuf.String())
		}
		// Line 0 is root status
		if !strings.HasPrefix(lines[0], "200\t") || !strings.Contains(lines[0], "@NASA\tRoot tweet text") {
			t.Errorf("line 0 mismatch: %q", lines[0])
		}
		// Line 1 is indented reply 201
		if !strings.HasPrefix(lines[1], "  201\t") || !strings.Contains(lines[1], "@astronomer\tFirst reply text") {
			t.Errorf("line 1 mismatch: %q", lines[1])
		}
		// Line 2 is indented reply 202
		if !strings.HasPrefix(lines[2], "  202\t") || !strings.Contains(lines[2], "@physicist\tSecond reply text") {
			t.Errorf("line 2 mismatch: %q", lines[2])
		}
	})

	t.Run("status URL ref parsing", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: true,
			CTX:      context.Background(),
		}
		cmd := comments.New(s)
		cmd.SetArgs([]string{"https://x.com/NASA/status/200"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(outBuf.String(), "Root tweet text") {
			t.Errorf("expected root tweet in out: %q", outBuf.String())
		}
	})

	t.Run("sort recency passed to upstream", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: true,
			CTX:      context.Background(),
		}
		cmd := comments.New(s)
		cmd.SetArgs([]string{"200", "--sort", "recency"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if lastRankingMode != "recency" {
			t.Errorf("lastRankingMode = %q, want recency", lastRankingMode)
		}
	})

	t.Run("limit flag truncates replies", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: true,
			CTX:      context.Background(),
		}
		cmd := comments.New(s)
		cmd.SetArgs([]string{"200", "--limit", "1"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 1 root + 1 reply = 2 lines, got %d; out=%q", len(lines), outBuf.String())
		}
	})

	t.Run("json mode output", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: false,
			CTX:      context.Background(),
		}
		cmd := comments.New(s)
		cmd.SetArgs([]string{"200", "--json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		var conv nitter.Conversation
		if err := json.Unmarshal([]byte(outBuf.String()), &conv); err != nil {
			t.Fatalf("unmarshal json conversation: %v", err)
		}
		if conv.Status.ID != "200" {
			t.Errorf("conv.Status.ID = %q, want 200", conv.Status.ID)
		}
		if len(conv.Replies) != 2 {
			t.Errorf("len(conv.Replies) = %d, want 2", len(conv.Replies))
		}
	})

	t.Run("ndjson mode output", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: false,
			CTX:      context.Background(),
		}
		cmd := comments.New(s)
		cmd.SetArgs([]string{"200", "--ndjson"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("ndjson lines count = %d, want 2; out=%q", len(lines), outBuf.String())
		}
		var env1, env2 pipeline.Envelope
		if err := json.Unmarshal([]byte(lines[0]), &env1); err != nil {
			t.Fatalf("unmarshal env1: %v", err)
		}
		if err := json.Unmarshal([]byte(lines[1]), &env2); err != nil {
			t.Fatalf("unmarshal env2: %v", err)
		}
		if env1.Kind != pipeline.KindTweet || env1.ID != "201" {
			t.Errorf("env1 mismatch: kind=%s id=%s", env1.Kind, env1.ID)
		}
		if env2.Kind != pipeline.KindTweet || env2.ID != "202" {
			t.Errorf("env2 mismatch: kind=%s id=%s", env2.Kind, env2.ID)
		}
		if env1.Meta.Source != "comments:200" || env1.Meta.Instance != "FxTwitter" {
			t.Errorf("env1.Meta mismatch: %+v", env1.Meta)
		}
	})
}
