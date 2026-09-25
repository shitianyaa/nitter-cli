package circle_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

// writeCircles seeds ~/.nitter-cli/circles.toml through the real SaveCircles
// round-trip so the fixture's TOML shape is always authentic.
func writeCircles(t *testing.T, home, name string, users []string) {
	t.Helper()
	circles := map[string]settings.Circle{
		name: {Key: name, Name: name, Users: users},
	}
	if err := settings.SaveCircles(filepath.Join(home, ".nitter-cli", "circles.toml"), circles); err != nil {
		t.Fatalf("seed circles.toml: %v", err)
	}
}

// readCircles loads ~/.nitter-cli/circles.toml through the real LoadCircles.
func readCircles(t *testing.T, home string) map[string]settings.Circle {
	t.Helper()
	circles, err := settings.LoadCircles(filepath.Join(home, ".nitter-cli", "circles.toml"))
	if err != nil {
		t.Fatalf("load circles.toml: %v", err)
	}
	return circles
}

func TestCircleRemoveRemovesMember(t *testing.T) {
	home := tempHome(t)
	writeCircles(t, home, "coser", []string{"alice", "bob"})
	code, out, errOut := runCLI(t, "circle", "remove", "coser", "@alice")
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "removed @alice from circle coser") {
		t.Errorf("out = %q", out)
	}
	users := readCircles(t, home)["coser"].Users
	if len(users) != 1 || users[0] != "bob" {
		t.Fatalf("users = %v, want [bob]", users)
	}
}

func TestCircleRemoveIdempotentForNonMember(t *testing.T) {
	home := tempHome(t)
	writeCircles(t, home, "coser", []string{"alice"})
	code, _, errOut := runCLI(t, "circle", "remove", "coser", "@ghost")
	if code != 0 {
		t.Fatalf("non-member remove must be exit 0 (idempotent), got %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "not a member") {
		t.Errorf("stderr = %q, want an explicit not-a-member notice", errOut)
	}
	users := readCircles(t, home)["coser"].Users
	if len(users) != 1 || users[0] != "alice" {
		t.Errorf("users = %v, want [alice] (nothing changed)", users)
	}
}

func TestCircleRemoveUnknownCircleFails(t *testing.T) {
	home := tempHome(t)
	writeCircles(t, home, "coser", []string{"alice"})
	if code, _, errOut := runCLI(t, "circle", "remove", "nope", "@alice"); code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
}

// TestCircleRemoveLeavesProfileSidecarAlone: remove edits only the circles
// roster; the profile sidecar (role/note judgement included) must not be read
// or rewritten by remove.
func TestCircleRemoveLeavesProfileSidecarAlone(t *testing.T) {
	home := tempHome(t)
	writeCircles(t, home, "coser", []string{"alice", "bob"})
	dir := filepath.Join(home, ".nitter-cli")
	seed := "[profiles.alice]\nhandle = 'alice'\nrole = 'creator'\nnote = 'keep me'\nnoted_at = '2026-01-01T00:00:00Z'\n"
	if err := os.WriteFile(filepath.Join(dir, "profiles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}

	if code, _, errOut := runCLI(t, "circle", "remove", "coser", "@alice"); code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles.toml"))
	if err != nil {
		t.Fatalf("read profiles.toml: %v", err)
	}
	if string(data) != seed {
		t.Errorf("profiles.toml changed:\n%s", data)
	}
}

func TestCircleRemoveRejectsWrongArgCount(t *testing.T) {
	home := tempHome(t)
	writeCircles(t, home, "coser", []string{"alice"})
	if code, _, _ := runCLI(t, "circle", "remove", "coser"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// TestCircleAddFetchesProfileFacts: after a successful roster write, add
// best-effort fetches the new member's profile facts and merges them into the
// sidecar — the machine fields only (handle/name/bio/followers_count), exactly
// as circle refresh does.
func TestCircleAddFetchesProfileFacts(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/profile/NASA" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"profile":{"screen_name":"NASA","name":"NASA","description":"space","followers_count":70000000}}`))
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, _, errOut := runCLI(t, "circle", "add", "coser", "@NASA")
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	profiles, err := settings.LoadProfiles(filepath.Join(home, ".nitter-cli", "profiles.toml"))
	if err != nil {
		t.Fatalf("load profiles.toml: %v", err)
	}
	p, ok := profiles["nasa"]
	if !ok {
		t.Fatalf("nasa not written to profiles.toml: %+v", profiles)
	}
	if p.Name != "NASA" || p.Bio != "space" || p.FollowersCount != 70000000 {
		t.Fatalf("profile facts not merged: %+v", p)
	}
}

// TestCircleAddSurvivesProfileFetchFailure: the roster write happens first and
// must never be lost — a failed profile fetch is only a stderr warning and the
// command stays exit 0.
func TestCircleAddSurvivesProfileFetchFailure(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404}`))
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, _, errOut := runCLI(t, "circle", "add", "coser", "@NASA")
	if code != 0 {
		t.Fatalf("roster write must succeed even when the profile fetch fails; exit = %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "warning") {
		t.Errorf("stderr = %q, want a warning", errOut)
	}
	users := readCircles(t, home)["coser"].Users
	if len(users) != 1 || users[0] != "NASA" {
		t.Fatalf("roster write was lost: %v", users)
	}
}
