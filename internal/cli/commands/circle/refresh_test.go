package circle_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
)

// fxProfileHandler serves the Fx /2/profile/<handle> endpoint. The map decides
// each handle's response: nil means 404.
func fxProfileHandler(t *testing.T, users map[string]map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		for handle, user := range users {
			if !strings.HasSuffix(path, "/profile/"+strings.ToLower(handle)) {
				continue
			}
			if user == nil {
				http.Error(w, `{"code": 404, "message": "User not found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "user": user})
			return
		}
		http.NotFound(w, r)
	}
}

// NOTE: go-toml unmarshals integers into map[string]any as int64, not
// float64 — assert with int64 on the TOML path. (The JSON path in
// TestCircleShowJSONShape still uses float64, since encoding/json does that.)
func readProfiles(t *testing.T, home, handle string) (map[string]any, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".nitter-cli", "profiles.toml"))
	if err != nil {
		return nil, false
	}
	var tree map[string]map[string]map[string]any
	if err := toml.Unmarshal(data, &tree); err != nil {
		t.Fatalf("parse profiles.toml: %v\n%s", err, data)
	}
	entry, ok := tree["profiles"][handle]
	return entry, ok
}

func TestCircleRefreshWritesFacts(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)
	runCLI(t, "circle", "add", "dev", "big")
	runCLI(t, "circle", "add", "dev", "small")

	srv := httptest.NewServer(fxProfileHandler(t, map[string]map[string]any{
		"big":   {"screen_name": "big", "name": "Big", "followers": 12000, "description": "bio big"},
		"small": {"screen_name": "small", "name": "Small", "followers": 300, "description": "bio small"},
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, out, errOut := runCLI(t, "circle", "refresh", "dev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "refreshed 2/2 members in circle dev") {
		t.Errorf("out = %q, want the refresh summary", out)
	}
	big, ok := readProfiles(t, home, "big")
	if !ok {
		t.Fatalf("big not written to profiles.toml")
	}
	if big["followers_count"] != int64(12000) || big["bio"] != "bio big" {
		t.Errorf("big = %+v", big)
	}
	if got, _ := big["fetched_at"].(string); got == "" {
		t.Errorf("fetched_at empty, want a timestamp")
	}
}

func TestCircleRefreshPreservesJudgement(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)
	runCLI(t, "circle", "add", "dev", "big")

	dir := filepath.Join(home, ".nitter-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := "[profiles.big]\nhandle = 'big'\nrole = 'creator'\nnote = 'alt @big2'\nnoted_at = '2026-01-01T00:00:00Z'\n"
	if err := os.WriteFile(filepath.Join(dir, "profiles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := httptest.NewServer(fxProfileHandler(t, map[string]map[string]any{
		"big": {"screen_name": "big", "followers": 12000},
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, _, errOut := runCLI(t, "circle", "refresh", "dev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	big, ok := readProfiles(t, home, "big")
	if !ok {
		t.Fatalf("big missing after refresh")
	}
	if big["role"] != "creator" || big["note"] != "alt @big2" || big["noted_at"] != "2026-01-01T00:00:00Z" {
		t.Errorf("judgement lost: %+v", big)
	}
	if big["followers_count"] != int64(12000) {
		t.Errorf("facts not refreshed: %+v", big)
	}
}

func TestCircleRefreshPartialFailureWarnsAndContinues(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)
	runCLI(t, "circle", "add", "dev", "big")
	runCLI(t, "circle", "add", "dev", "gone")

	srv := httptest.NewServer(fxProfileHandler(t, map[string]map[string]any{
		"big":  {"screen_name": "big", "followers": 12000},
		"gone": nil,
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, out, errOut := runCLI(t, "circle", "refresh", "dev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "warning: circle refresh: @gone") {
		t.Errorf("stderr = %q, want a warning naming @gone", errOut)
	}
	if !strings.Contains(out, "refreshed 1/2 members in circle dev") {
		t.Errorf("out = %q, want 1/2", out)
	}
	if _, ok := readProfiles(t, home, "gone"); ok {
		t.Errorf("failed member must not be written")
	}
}

func TestCircleRefreshAllFailuresExit1(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)
	runCLI(t, "circle", "add", "dev", "gone")

	srv := httptest.NewServer(fxProfileHandler(t, map[string]map[string]any{"gone": nil}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, _, _ := runCLI(t, "circle", "refresh", "dev")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestCircleRefreshUnknownCircleExit1(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	_ = home
	code, _, errOut := runCLI(t, "circle", "refresh", "nosuch")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "not found") {
		t.Errorf("stderr = %q, want not-found message", errOut)
	}
}

func TestCircleRefreshEmptyCircleWritesNothing(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	dir := filepath.Join(home, ".nitter-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := "[circles.empty]\nkey = 'empty'\nname = 'empty'\nusers = []\n"
	if err := os.WriteFile(filepath.Join(dir, "circles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed circles: %v", err)
	}

	code, _, errOut := runCLI(t, "circle", "refresh", "empty")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "(empty)") {
		t.Errorf("stderr = %q, want (empty)", errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.toml")); !os.IsNotExist(err) {
		t.Errorf("profiles.toml must not be created for an empty circle (err=%v)", err)
	}
}

func TestCircleRefreshRejectsExtraArgs(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	_ = home
	code, _, _ := runCLI(t, "circle", "refresh", "dev", "extra")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
