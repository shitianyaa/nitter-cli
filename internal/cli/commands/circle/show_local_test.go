package circle_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
)

// The tests in this file cover `circle show`'s local (zero-network) behavior:
// joining circles.toml against the profiles.toml sidecar. They live in their
// own file so the legacy AddListShow/Run suites are not disturbed.

// countingFxServer serves nothing and counts every request. `show` must never
// touch it — asserting on the *count* (rather than on an unreachable address)
// is what actually proves the command performs no network I/O: an unreachable
// URL only proves that a failed request does not surface as an error.
func countingFxServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	return srv, &requests
}

func TestCircleShowIsLocalAndJoinsSidecar(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	runCLI(t, "circle", "add", "dev", "cached")
	runCLI(t, "circle", "add", "dev", "uncached")

	dir := filepath.Join(home, ".nitter-cli")
	seed := "[profiles.cached]\nhandle = 'cached'\nname = 'Cached'\nbio = 'a bio'\n" +
		"followers_count = 12000\nfetched_at = '2026-09-20T12:00:00Z'\nrole = 'creator'\n"
	if err := os.WriteFile(filepath.Join(dir, "profiles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	srv, requests := countingFxServer(t)
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, out, errOut := runCLI(t, "circle", "show", "dev")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if *requests != 0 {
		t.Errorf("requests = %d, want 0 (show must be local-only)", *requests)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want 2 rows", lines)
	}
	// Row order follows the roster; columns are
	// @handle <followers> <role> <name> <bio> <age>.
	first := strings.Split(lines[0], "\t")
	if len(first) != 6 || first[0] != "@cached" || first[1] != "12000" ||
		first[2] != "creator" || first[3] != "Cached" || first[4] != "a bio" {
		t.Errorf("first row = %v", first)
	}
	if first[5] == "-" || first[5] == "" {
		t.Errorf("age column = %q, want a rendered age", first[5])
	}
	second := strings.Split(lines[1], "\t")
	if len(second) != 6 || second[0] != "@uncached" {
		t.Fatalf("second row = %v", second)
	}
	for i := 1; i < 6; i++ {
		if second[i] != "-" {
			t.Errorf("uncached column %d = %q, want -", i, second[i])
		}
	}
	if !strings.Contains(errOut, "1 member(s) have no cached profile") {
		t.Errorf("stderr = %q, want the missing-cache note", errOut)
	}
}

func TestCircleShowJSONShape(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	runCLI(t, "circle", "add", "dev", "cached")
	runCLI(t, "circle", "add", "dev", "uncached")

	dir := filepath.Join(home, ".nitter-cli")
	seed := "[profiles.cached]\nhandle = 'cached'\nbio = 'b'\nfollowers_count = 12000\n" +
		"fetched_at = '2026-09-20T12:00:00Z'\nrole = 'creator'\nnote = 'n'\nnoted_at = '2026-09-21T00:00:00Z'\n"
	if err := os.WriteFile(filepath.Join(dir, "profiles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	srv, requests := countingFxServer(t)
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, out, errOut := runCLI(t, "circle", "show", "dev", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if *requests != 0 {
		t.Errorf("requests = %d, want 0 (show must be local-only)", *requests)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	if rows[0]["handle"] != "cached" || rows[0]["followers_count"].(float64) != 12000 ||
		rows[0]["role"] != "creator" || rows[0]["fetched_at"] != "2026-09-20T12:00:00Z" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if _, present := rows[0]["noted_at"]; present {
		t.Errorf("noted_at must not be projected into JSON: %+v", rows[0])
	}
	if _, present := rows[0]["age_seconds"]; present {
		t.Errorf("age_seconds must not exist; consumers derive age: %+v", rows[0])
	}
	if rows[1]["handle"] != "uncached" || rows[1]["fetched_at"] != "" {
		t.Errorf("row 1 = %+v, want uncached with empty fetched_at", rows[1])
	}
}

func TestCircleShowMinFollowersFiltersCache(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	runCLI(t, "circle", "add", "dev", "big")
	runCLI(t, "circle", "add", "dev", "small")
	runCLI(t, "circle", "add", "dev", "uncached")

	dir := filepath.Join(home, ".nitter-cli")
	seed := "[profiles.big]\nhandle = 'big'\nfollowers_count = 12000\nfetched_at = '2026-09-20T12:00:00Z'\n" +
		"[profiles.small]\nhandle = 'small'\nfollowers_count = 300\nfetched_at = '2026-09-20T12:00:00Z'\n"
	if err := os.WriteFile(filepath.Join(dir, "profiles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	srv, requests := countingFxServer(t)
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, out, errOut := runCLI(t, "circle", "show", "dev", "--min-followers", "5000")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if *requests != 0 {
		t.Errorf("requests = %d, want 0 (show must be local-only)", *requests)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "@big\t") {
		t.Errorf("lines = %v, want only @big", lines)
	}
	// The uncached member is filtered out but must still be reported.
	if !strings.Contains(errOut, "1 member(s) have no cached profile") {
		t.Errorf("stderr = %q, want the missing-cache note even when filtered", errOut)
	}

	// A negative threshold is a usage error before any IO.
	code, _, _ = runCLI(t, "circle", "show", "dev", "--min-followers", "-1")
	if code != 2 {
		t.Errorf("negative --min-followers exit = %d, want 2", code)
	}
	if *requests != 0 {
		t.Errorf("requests = %d, want 0 after a usage error", *requests)
	}
}

func TestCircleShowEmptyAndUnknown(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	dir := filepath.Join(home, ".nitter-cli")
	seed := "[circles.empty]\nkey = 'empty'\nname = 'empty'\nusers = []\n"
	if err := os.WriteFile(filepath.Join(dir, "circles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed circles: %v", err)
	}

	code, out, errOut := runCLI(t, "circle", "show", "empty")
	if code != 0 {
		t.Fatalf("empty exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("empty out = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "(empty)") {
		t.Errorf("empty stderr = %q, want (empty)", errOut)
	}

	code, out, _ = runCLI(t, "circle", "show", "empty", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("empty --json: code=%d out=%q, want 0 and []", code, out)
	}

	code, _, errOut = runCLI(t, "circle", "show", "nosuch")
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("unknown circle: code=%d stderr=%q, want 1 and not-found", code, errOut)
	}
}
