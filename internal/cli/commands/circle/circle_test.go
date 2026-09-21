package circle_test

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
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/circle"
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

func TestCircle_AddListShow(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	// 1. Initial list when no circles exist
	t.Run("list empty text", func(t *testing.T) {
		code, out, errOut := runCLI(t, "circle", "list")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if out != "" {
			t.Errorf("out = %q, want empty", out)
		}
		if strings.TrimSpace(errOut) != "(empty)" {
			t.Errorf("errOut = %q, want (empty)", errOut)
		}
	})

	t.Run("list empty json", func(t *testing.T) {
		code, out, _ := runCLI(t, "circle", "list", "--json")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if strings.TrimSpace(out) != "[]" {
			t.Errorf("out = %q, want []", out)
		}
	})

	// 2. Add handles to circle
	t.Run("add handles", func(t *testing.T) {
		code, out, errOut := runCLI(t, "circle", "add", "dev", "golang")
		if code != 0 {
			t.Fatalf("add 1 code = %d, want 0; err=%s", code, errOut)
		}
		if !strings.Contains(out, "added @golang to circle dev") {
			t.Errorf("unexpected out: %q", out)
		}

		code, out, errOut = runCLI(t, "circle", "add", "dev", "@github")
		if code != 0 {
			t.Fatalf("add 2 code = %d, want 0; err=%s", code, errOut)
		}
		if !strings.Contains(out, "added @github to circle dev") {
			t.Errorf("unexpected out: %q", out)
		}

		// Add duplicate
		code, _, _ = runCLI(t, "circle", "add", "dev", "golang")
		if code != 0 {
			t.Fatalf("add duplicate code = %d, want 0", code)
		}

		// Add another circle
		code, _, _ = runCLI(t, "circle", "add", "space", "NASA")
		if code != 0 {
			t.Fatalf("add space code = %d, want 0", code)
		}
	})

	// 3. List populated
	t.Run("list populated text", func(t *testing.T) {
		code, out, _ := runCLI(t, "circle", "list")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), out)
		}
		// dev should be first (sorted by key), 2 users
		if !strings.HasPrefix(lines[0], "dev\tdev\t\t2") {
			t.Errorf("line 0 = %q, want dev line with count 2", lines[0])
		}
		// space second, 1 user
		if !strings.HasPrefix(lines[1], "space\tspace\t\t1") {
			t.Errorf("line 1 = %q, want space line with count 1", lines[1])
		}
	})

	t.Run("list populated json", func(t *testing.T) {
		code, out, _ := runCLI(t, "circle", "list", "--json")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		var list []map[string]any
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			t.Fatalf("unmarshal json list: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("len(list) = %d, want 2", len(list))
		}
		if list[0]["key"] != "dev" || list[0]["user_count"].(float64) != 2 {
			t.Errorf("list[0] mismatch: %+v", list[0])
		}
	})

	// 4. Show circle
	t.Run("show circle text", func(t *testing.T) {
		code, out, _ := runCLI(t, "circle", "show", "dev")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), out)
		}
		if lines[0] != "@golang" || lines[1] != "@github" {
			t.Errorf("unexpected lines: %v", lines)
		}
	})

	t.Run("show circle json", func(t *testing.T) {
		code, out, _ := runCLI(t, "circle", "show", "dev", "--json")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		var users []string
		if err := json.Unmarshal([]byte(out), &users); err != nil {
			t.Fatalf("unmarshal show json: %v", err)
		}
		if len(users) != 2 || users[0] != "golang" || users[1] != "github" {
			t.Errorf("unexpected users: %v", users)
		}
	})

	// 5. Error cases
	t.Run("error cases", func(t *testing.T) {
		// show nonexistent circle exits 1
		code, _, _ := runCLI(t, "circle", "show", "nonexistent")
		if code != 1 {
			t.Errorf("show nonexistent exit code = %d, want 1", code)
		}

		// show no args exits 2
		code, _, _ = runCLI(t, "circle", "show")
		if code != 2 {
			t.Errorf("show no args exit code = %d, want 2", code)
		}

		// add missing args exits 2
		code, _, _ = runCLI(t, "circle", "add", "dev")
		if code != 2 {
			t.Errorf("add missing arg exit code = %d, want 2", code)
		}

		// add invalid handle exits 2
		code, _, _ = runCLI(t, "circle", "add", "dev", "invalid!handle")
		if code != 2 {
			t.Errorf("add invalid handle exit code = %d, want 2", code)
		}
	})
}

func TestCircle_Run(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	// Add users to circle
	runCLI(t, "circle", "add", "space", "NASA")
	runCLI(t, "circle", "add", "space", "ESA")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		switch {
		case strings.Contains(path, "/profile/nasa/statuses"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"tweets": []map[string]any{
					{
						"id":   "101",
						"text": "NASA tweet 1",
						"author": map[string]any{
							"screen_name": "NASA",
							"name":        "NASA Official",
						},
						"created_at": "Sun Jul 05 09:09:40 +0000 2026",
					},
				},
			})
		case strings.Contains(path, "/profile/esa/statuses"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"tweets": []map[string]any{
					{
						"id":   "102",
						"text": "ESA tweet 1",
						"author": map[string]any{
							"screen_name": "ESA",
							"name":        "European Space Agency",
						},
						"created_at": "Sun Jul 05 09:10:00 +0000 2026",
					},
				},
			})
		case strings.Contains(path, "/profile/empty/statuses"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":   200,
				"tweets": []any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	t.Run("run circle json mode", func(t *testing.T) {
		code, out, errOut := runCLI(t, "circle", "run", "space", "--json")
		if code != 0 {
			t.Fatalf("code = %d, want 0; err=%s", code, errOut)
		}
		var tweets []nitter.Tweet
		if err := json.Unmarshal([]byte(out), &tweets); err != nil {
			t.Fatalf("unmarshal tweets: %v", err)
		}
		if len(tweets) != 2 {
			t.Fatalf("len(tweets) = %d, want 2", len(tweets))
		}
		if tweets[0].ID != "101" || tweets[1].ID != "102" {
			t.Errorf("unexpected tweets: %+v", tweets)
		}
	})

	t.Run("run circle ndjson mode", func(t *testing.T) {
		code, out, errOut := runCLI(t, "circle", "run", "space", "--ndjson")
		if code != 0 {
			t.Fatalf("code = %d, want 0; err=%s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), out)
		}
		var env1, env2 pipeline.Envelope
		if err := json.Unmarshal([]byte(lines[0]), &env1); err != nil {
			t.Fatalf("unmarshal env1: %v", err)
		}
		if err := json.Unmarshal([]byte(lines[1]), &env2); err != nil {
			t.Fatalf("unmarshal env2: %v", err)
		}
		if env1.Kind != pipeline.KindTweet || env1.ID != "101" {
			t.Errorf("env1 mismatch: kind=%s id=%s", env1.Kind, env1.ID)
		}
		if env2.Kind != pipeline.KindTweet || env2.ID != "102" {
			t.Errorf("env2 mismatch: kind=%s id=%s", env2.Kind, env2.ID)
		}
		if env1.Meta.Source != "circle:space" || env1.Meta.Instance != "FxTwitter" {
			t.Errorf("env1.Meta mismatch: %+v", env1.Meta)
		}
	})

	t.Run("run circle human mode on TTY", func(t *testing.T) {
		var outBuf, errBuf strings.Builder
		s := &invocation.Streams{
			Out:      &outBuf,
			Err:      &errBuf,
			OutIsTTY: true,
			CTX:      context.Background(),
		}
		cmd := circle.New(s)
		cmd.SetArgs([]string{"run", "space"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), outBuf.String())
		}
		if !strings.Contains(lines[0], "@NASA\tNASA tweet 1") {
			t.Errorf("line 0 mismatch: %q", lines[0])
		}
		if !strings.Contains(lines[1], "@ESA\tESA tweet 1") {
			t.Errorf("line 1 mismatch: %q", lines[1])
		}
	})

	t.Run("run errors", func(t *testing.T) {
		// Nonexistent circle exits 1
		code, _, _ := runCLI(t, "circle", "run", "nonexistent")
		if code != 1 {
			t.Errorf("run nonexistent code = %d, want 1", code)
		}

		// Missing name exits 2
		code, _, _ = runCLI(t, "circle", "run")
		if code != 2 {
			t.Errorf("run missing name code = %d, want 2", code)
		}

		// Negative limit exits 2
		code, _, _ = runCLI(t, "circle", "run", "space", "--limit", "-1")
		if code != 2 {
			t.Errorf("run negative limit code = %d, want 2", code)
		}

		// Both json and ndjson exits 2
		code, _, _ = runCLI(t, "circle", "run", "space", "--json", "--ndjson")
		if code != 2 {
			t.Errorf("run both json and ndjson code = %d, want 2", code)
		}
	})
}
