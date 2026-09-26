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

// deadFxBaseURL points the fx backend at an unreachable local port for the
// whole test. `circle add` now best-effort fetches the added member's profile
// after the roster write; tests that do not exercise that fetch wire this so
// the attempt fails instantly and deterministically instead of reaching the
// live FxTwitter API. Tests that DO exercise fetches call
// SetFxBaseURLForTesting(srv.URL) afterwards, which overrides this and
// restores it on cleanup.
func deadFxBaseURL(t *testing.T) {
	t.Helper()
	cleanup := client.SetFxBaseURLForTesting("http://127.0.0.1:1")
	t.Cleanup(cleanup)
}

func TestCircle_AddListShow(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)

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
		cleanup := client.SetFxBaseURLForTesting("http://127.0.0.1:1")
		defer cleanup()

		code, out, _ := runCLI(t, "circle", "show", "dev")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines count = %d, want 2; out=%q", len(lines), out)
		}
		for i, handle := range []string{"@golang", "@github"} {
			cells := strings.Split(lines[i], "\t")
			if len(cells) != 6 || cells[0] != handle {
				t.Errorf("row %d = %v, want 6 columns starting with %s", i, cells, handle)
				continue
			}
			for j := 1; j < 6; j++ {
				if cells[j] != "-" {
					t.Errorf("row %d column %d = %q, want - (no cache)", i, j, cells[j])
				}
			}
		}
	})

	t.Run("show circle json", func(t *testing.T) {
		code, out, _ := runCLI(t, "circle", "show", "dev", "--json")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("unmarshal show json: %v", err)
		}
		if len(rows) != 2 || rows[0]["handle"] != "golang" || rows[1]["handle"] != "github" {
			t.Errorf("unexpected rows: %+v", rows)
		}
		for _, row := range rows {
			if row["fetched_at"] != "" {
				t.Errorf("uncached row must have empty fetched_at: %+v", row)
			}
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
	deadFxBaseURL(t)

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

		// Non-positive limit exits 2 (0 was the "all" spelling and is gone)
		code, _, _ = runCLI(t, "circle", "run", "space", "--limit", "-1")
		if code != 2 {
			t.Errorf("run negative limit code = %d, want 2", code)
		}
		code, _, _ = runCLI(t, "circle", "run", "space", "--limit", "0")
		if code != 2 {
			t.Errorf("run zero limit code = %d, want 2", code)
		}

		// Both json and ndjson exits 2
		code, _, _ = runCLI(t, "circle", "run", "space", "--json", "--ndjson")
		if code != 2 {
			t.Errorf("run both json and ndjson code = %d, want 2", code)
		}
	})
}

// TestCircle_RunPartialAndTotalFailure: a member whose fetch fails is skipped
// with a warning while the rest continue (exit 0); only when every member fails
// does the command exit 1. The semantics mirror what cli-reference documents
// for refresh's per-member loop — its `run` section does not restate them, so
// this test is the only place pinning them.
func TestCircle_RunPartialAndTotalFailure(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home) // fetch_backend = "fx": keeps the 404 on the fast lane
	writeCircles(t, home, "space", []string{"ok", "gone"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		if strings.Contains(path, "/profile/ok/statuses") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"tweets": []map[string]any{
					{
						"id":   "201",
						"text": "ok tweet 1",
						"author": map[string]any{
							"screen_name": "ok",
							"name":        "OK",
						},
						"created_at": "Sun Jul 05 09:09:40 +0000 2026",
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

	// One member fails: a warning names it, and the surviving member's tweet
	// still reaches stdout — the run must not degrade into an empty success.
	// --ndjson pins the output mode so the assertion does not depend on TTY
	// detection.
	code, out, errOut := runCLI(t, "circle", "run", "space", "--ndjson")
	if code != 0 {
		t.Fatalf("partial failure exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "warning: circle run: @gone") {
		t.Errorf("stderr = %q, want a warning naming @gone", errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout lines = %d, want 1 (only the surviving member); out=%q", len(lines), out)
	}
	var env pipeline.Envelope
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (out=%q)", err, out)
	}
	if env.Kind != pipeline.KindTweet || env.ID != "201" {
		t.Errorf("envelope mismatch: kind=%s id=%s", env.Kind, env.ID)
	}
	if env.Meta == nil || env.Meta.Source != "circle:space" {
		t.Errorf("envelope meta mismatch: %+v", env.Meta)
	}

	// Every member fails: the warning names each member, nothing reaches stdout
	// (no silent empty success) and the exit is 1.
	writeCircles(t, home, "dead", []string{"gone"})
	code, out, errOut = runCLI(t, "circle", "run", "dead", "--ndjson")
	if code != 1 {
		t.Fatalf("total failure exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "warning: circle run: @gone") {
		t.Errorf("stderr = %q, want a warning naming @gone", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty (a total failure must not emit tweets)", out)
	}
}

// TestCircle_RunDeterministicOrder: the Fx fast lane sorts its results by
// tweet ID descending (timeline order) before returning them, so the same
// fixture always produces the same ID sequence — two consecutive runs emit
// identical ID lists even when the upstream page order is shuffled.
func TestCircle_RunDeterministicOrder(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)

	runCLI(t, "circle", "add", "ord", "nasa")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		if !strings.Contains(path, "/2/profile/nasa/media") && !strings.Contains(path, "/2/profile/nasa/statuses") {
			http.NotFound(w, r)
			return
		}
		// Deliberately NOT in timeline (ID descending) order.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"results": []map[string]any{
				fxMediaTweet("201", "201 tweet"),
				fxMediaTweet("203", "203 tweet"),
				fxMediaTweet("202", "202 tweet"),
			},
		})
	}))
	defer srv.Close()

	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	var firstIDs []string
	for i := 0; i < 2; i++ {
		code, out, errOut := runCLI(t, "circle", "run", "ord", "--json")
		if code != 0 {
			t.Fatalf("run %d: exit = %d, want 0 (stderr %q)", i, code, errOut)
		}
		var tweets []nitter.Tweet
		if err := json.Unmarshal([]byte(out), &tweets); err != nil {
			t.Fatalf("run %d: unmarshal: %v\n%s", i, err, out)
		}
		var ids []string
		for _, tw := range tweets {
			ids = append(ids, tw.ID)
		}
		if i == 0 {
			firstIDs = ids
			continue
		}
		if strings.Join(ids, ",") != strings.Join(firstIDs, ",") {
			t.Errorf("two runs differ: %v vs %v", firstIDs, ids)
		}
	}
	// Sorted deterministically by ID descending (timeline order).
	if strings.Join(firstIDs, ",") != "203,202,201" {
		t.Errorf("ids = %v, want [203 202 201] (ID descending)", firstIDs)
	}
}

// TestCircle_RunMetaFilter: the NDJSON envelope meta carries a filter marker
// only when a media filter is in effect: --media-only marks "media_only",
// --media-type image marks "image", and without any filter the meta has no
// filter key at all.
func TestCircle_RunMetaFilter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantFilter string // empty = key must be absent
	}{
		{name: "media-only", args: []string{"--media-only"}, wantFilter: "media_only"},
		{name: "media-type image", args: []string{"--media-type", "image"}, wantFilter: "image"},
		{name: "no filter", args: nil, wantFilter: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := tempHome(t)
			writeConfig(t, home)
			deadFxBaseURL(t)
			runCLI(t, "circle", "add", "mf", "nasa")

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				path := strings.ToLower(r.URL.Path)
				if !strings.Contains(path, "/2/profile/nasa/media") && !strings.Contains(path, "/2/profile/nasa/statuses") {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    200,
					"results": []map[string]any{fxMediaTweet("301", "photo tweet")},
				})
			}))
			defer srv.Close()
			cleanup := client.SetFxBaseURLForTesting(srv.URL)
			defer cleanup()

			args := append([]string{"circle", "run", "mf", "--ndjson"}, tc.args...)
			code, out, errOut := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
			}
			var env pipeline.Envelope
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
				t.Fatalf("unmarshal envelope: %v\n%s", err, out)
			}
			if env.Meta == nil {
				t.Fatalf("meta is nil")
			}
			if tc.wantFilter == "" {
				if env.Meta.Filter != "" {
					t.Errorf("meta.filter = %q, want key absent", env.Meta.Filter)
				}
				return
			}
			if env.Meta.Filter != tc.wantFilter {
				t.Errorf("meta.filter = %q, want %q", env.Meta.Filter, tc.wantFilter)
			}
		})
	}
}

// fxMediaTweet builds one fx media-endpoint result entry with a photo.
func fxMediaTweet(id, text string) map[string]any {
	return map[string]any{
		"id":   id,
		"text": text,
		"author": map[string]any{
			"screen_name": "nasa",
			"name":        "NASA",
		},
		"created_at": "Sun Jul 05 09:09:40 +0000 2026",
		"media": map[string]any{
			"all": []map[string]any{
				{"type": "photo", "url": "https://pbs.twimg.com/media/" + id + ".jpg"},
			},
		},
	}
}

// TestCircle_RunMediaType: --media-type image keeps only tweets carrying an
// image, video on an image-only circle yields [] (filtered-empty, success),
// and a bogus value is a usage error (exit 2) naming the flag before any
// network — the same semantics as the user command's --media-type.
func TestCircle_RunMediaType(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)

	runCLI(t, "circle", "add", "media", "nasa")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		if !strings.Contains(path, "/2/profile/nasa/media") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"results": []map[string]any{
				{
					"id":   "101",
					"text": "Photo tweet",
					"author": map[string]any{
						"screen_name": "nasa",
						"name":        "NASA",
					},
					"created_at": "Sun Jul 05 09:09:40 +0000 2026",
					"media": map[string]any{
						"all": []map[string]any{
							{"type": "photo", "url": "https://pbs.twimg.com/media/101.jpg"},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	// --media-type image: only the photo tweet (101) survives.
	code, out, errOut := runCLI(t, "circle", "run", "media", "--media-type", "image", "--json")
	if code != 0 {
		t.Fatalf("--media-type image: exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var tweets []nitter.Tweet
	if err := json.Unmarshal([]byte(out), &tweets); err != nil {
		t.Fatalf("--media-type image: unmarshal: %v\n%s", err, out)
	}
	if len(tweets) != 1 || tweets[0].ID != "101" {
		t.Errorf("--media-type image: tweets = %+v, want only 101", tweets)
	}

	// --media-type video: no tweet has video — filtered-empty is [] and a
	// success.
	code, out, _ = runCLI(t, "circle", "run", "media", "--media-type", "video", "--json")
	if code != 0 {
		t.Fatalf("--media-type video: exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("--media-type video: output = %q, want []", out)
	}

	// --media-type bogus: usage error naming the flag, before any network.
	// No fake server is wired here (fresh home, fx backend, no instance), so
	// any network attempt would fail differently — exit 2 proves validation
	// ran first.
	home2 := tempHome(t)
	writeConfig(t, home2)
	runCLI(t, "circle", "add", "media", "nasa")
	code, _, errOut = runCLI(t, "circle", "run", "media", "--media-type", "bogus")
	if code != 2 {
		t.Fatalf("--media-type bogus: exit = %d, want 2 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "--media-type") {
		t.Errorf("--media-type bogus: stderr = %q, want it to name --media-type", errOut)
	}
}
