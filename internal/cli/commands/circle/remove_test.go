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

// TestCircleRemoveInvalidHandleExit2: the handle-shape check runs before the
// circle lookup, so a bad handle is a usage error (exit 2) even when the named
// circle exists. Documented in docs/en/cli-reference.md (remove: a bad handle
// shape exits 2).
func TestCircleRemoveInvalidHandleExit2(t *testing.T) {
	home := tempHome(t)
	writeCircles(t, home, "coser", []string{"alice"})
	code, _, errOut := runCLI(t, "circle", "remove", "coser", "bad!handle")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	users := readCircles(t, home)["coser"].Users
	if len(users) != 1 || users[0] != "alice" {
		t.Errorf("users = %v, want [alice] (a usage error must not mutate the roster)", users)
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

// TestCircleRemoveWritesBackUnderMatchedKey: a circle's struct Key can
// diverge from the section key it is stored under — the inner key field wins
// over the section name on load — so the edit must land on the matched
// section, never fork the circle under c.Key.
func TestCircleRemoveWritesBackUnderMatchedKey(t *testing.T) {
	home := tempHome(t)
	dir := filepath.Join(home, ".nitter-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir .nitter-cli: %v", err)
	}
	seed := "[circles.coser]\nkey = 'renamed'\nname = 'Coser'\nusers = ['alice', 'bob']\n"
	if err := os.WriteFile(filepath.Join(dir, "circles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed circles.toml: %v", err)
	}

	code, out, errOut := runCLI(t, "circle", "remove", "coser", "@alice")
	if code != 0 {
		t.Fatalf("exit = %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "removed @alice from circle") {
		t.Errorf("out = %q", out)
	}
	circles := readCircles(t, home)
	if _, forked := circles["renamed"]; forked {
		t.Fatal("remove forked the circle under the divergent inner key instead of editing the matched section")
	}
	users := circles["coser"].Users
	if len(users) != 1 || users[0] != "bob" {
		t.Fatalf("users under the matched section = %v, want [bob]", users)
	}
}

// TestCircleAddDuplicateSkipsProfileFetch: re-adding an existing member is a
// roster no-op — no profile fetch may fire (a dead upstream would otherwise
// surface a warning). The first add still fetches.
func TestCircleAddDuplicateSkipsProfileFetch(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	deadFxBaseURL(t)

	code, _, errOut := runCLI(t, "circle", "add", "coser", "@NASA")
	if code != 0 {
		t.Fatalf("first add: exit = %d (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "warning") {
		t.Fatalf("first add must attempt the profile fetch (dead upstream → warning); stderr = %q", errOut)
	}

	code, out, errOut := runCLI(t, "circle", "add", "coser", "@NASA")
	if code != 0 {
		t.Fatalf("duplicate add: exit = %d (stderr %q)", code, errOut)
	}
	if strings.Contains(errOut, "warning") {
		t.Errorf("duplicate add must skip the profile fetch; stderr = %q", errOut)
	}
	if !strings.Contains(out, "added @NASA to circle coser") {
		t.Errorf("out = %q, want the unchanged duplicate-add message", out)
	}
}

// TestCircleAddFetchesProfileFacts: after a successful roster write, add
// best-effort fetches the new member's profile facts and merges them into the
// sidecar — the machine fields only (handle/name/bio/followers_count), exactly
// as circle refresh does. A pre-seeded judgement (role/note/noted_at) must
// survive the merge untouched: the auto-fetch never clobbers it.
func TestCircleAddFetchesProfileFacts(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	dir := filepath.Join(home, ".nitter-cli")
	seed := "[profiles.nasa]\nhandle = 'nasa'\nrole = 'creator'\nnote = 'hand recorded'\nnoted_at = '2026-01-01T00:00:00Z'\n"
	if err := os.WriteFile(filepath.Join(dir, "profiles.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
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
	profiles, err := settings.LoadProfiles(filepath.Join(dir, "profiles.toml"))
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
	if p.Role != "creator" || p.Note != "hand recorded" || p.NotedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("judgement clobbered by the add-time fetch: %+v", p)
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
