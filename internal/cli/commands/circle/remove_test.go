package circle_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
