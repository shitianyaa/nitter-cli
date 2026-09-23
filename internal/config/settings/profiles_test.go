package settings_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/config/paths"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

func TestLoadProfilesMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.toml")
	profiles, err := settings.LoadProfiles(path)
	if err != nil {
		t.Fatalf("LoadProfiles on missing file: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("len(profiles) = %d, want 0", len(profiles))
	}
}

func TestSaveProfilesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.toml")
	profiles := map[string]settings.Profile{
		"doubao23333": {
			Handle:         "doubao23333",
			Name:           "豆包",
			Bio:            "hello",
			FollowersCount: 123500,
			FetchedAt:      "2026-09-23T12:00:00Z",
			Role:           "creator",
			Note:           "alt @doubao_alt",
			NotedAt:        "2026-09-23T13:00:00Z",
		},
	}
	if err := settings.SaveProfiles(path, profiles); err != nil {
		t.Fatalf("SaveProfiles: %v", err)
	}
	loaded, err := settings.LoadProfiles(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := settings.FindProfile(loaded, "doubao23333")
	if !ok {
		t.Fatalf("FindProfile(doubao23333) not found; loaded=%+v", loaded)
	}
	if got != profiles["doubao23333"] {
		t.Fatalf("round-trip mismatch:\n got = %+v\nwant = %+v", got, profiles["doubao23333"])
	}
}

func TestFindProfileCaseInsensitive(t *testing.T) {
	profiles := map[string]settings.Profile{
		"doubao23333": {Handle: "doubao23333"},
	}
	for _, probe := range []string{"doubao23333", "DOUBAO23333", "DouBao23333"} {
		if _, ok := settings.FindProfile(profiles, probe); !ok {
			t.Errorf("FindProfile(%q) not found, want hit", probe)
		}
	}
	if _, ok := settings.FindProfile(profiles, "absent"); ok {
		t.Errorf("FindProfile(absent) found, want miss")
	}
}

// A hand-authored block may use the account's canonical casing as its key.
// LoadProfiles must normalize it, otherwise FindProfile never sees the entry
// and a later refresh adds a second lowercase key, stranding the recorded
// judgement on a key nobody reads.
func TestLoadProfilesNormalizesMixedCaseKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.toml")
	data := "[profiles.Doubao23333]\nhandle = 'Doubao23333'\nrole = 'creator'\nnote = 'hand recorded'\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	profiles, err := settings.LoadProfiles(path)
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	got, ok := settings.FindProfile(profiles, "Doubao23333")
	if !ok {
		t.Fatalf("mixed-case key not found; loaded=%+v", profiles)
	}
	if got.Role != "creator" || got.Note != "hand recorded" {
		t.Errorf("judgement lost: %+v", got)
	}
	if got.Handle != "Doubao23333" {
		t.Errorf("Handle = %q, want canonical casing preserved", got.Handle)
	}
	if _, exists := profiles["Doubao23333"]; exists {
		t.Errorf("mixed-case key must be normalized away: %+v", profiles)
	}

	// Merging facts onto the normalized entry must not create a second key.
	merged := settings.MergeProfileFacts(profiles, "Doubao23333", settings.Profile{
		Handle: "Doubao23333", FollowersCount: 12000,
	}, time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	if len(merged) != 1 {
		t.Fatalf("merged has %d entries, want 1: %+v", len(merged), merged)
	}
	final, _ := settings.FindProfile(merged, "doubao23333")
	if final.Role != "creator" || final.FollowersCount != 12000 {
		t.Errorf("merge lost judgement or facts: %+v", final)
	}
}

// A block with no explicit handle backfills it from the key.
func TestLoadProfilesBackfillsHandleFromKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.toml")
	if err := os.WriteFile(path, []byte("[profiles.abc]\nrole = 'creator'\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	profiles, err := settings.LoadProfiles(path)
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	got, ok := settings.FindProfile(profiles, "abc")
	if !ok {
		t.Fatalf("entry missing")
	}
	if got.Handle != "abc" {
		t.Errorf("Handle = %q, want backfilled from key", got.Handle)
	}
}

// MergeProfileFacts is the load-bearing invariant of the whole design:
// refresh overwrites facts and must never disturb judgement.
func TestMergeProfileFactsPreservesJudgement(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	existing := map[string]settings.Profile{
		"doubao23333": {
			Handle:         "doubao23333",
			Name:           "old name",
			Bio:            "old bio",
			FollowersCount: 1,
			FetchedAt:      "2026-01-01T00:00:00Z",
			Role:           "creator",
			Note:           "keep me",
			NotedAt:        "2026-01-02T00:00:00Z",
		},
	}
	fresh := settings.Profile{
		Handle:         "Doubao23333", // server casing differs from the key
		Name:           "new name",
		Bio:            "new bio",
		FollowersCount: 123500,
	}
	merged := settings.MergeProfileFacts(existing, "Doubao23333", fresh, now)
	got, ok := settings.FindProfile(merged, "doubao23333")
	if !ok {
		t.Fatalf("merged entry missing")
	}
	// Facts overwritten.
	if got.Name != "new name" || got.Bio != "new bio" || got.FollowersCount != 123500 {
		t.Errorf("facts not overwritten: %+v", got)
	}
	if got.Handle != "Doubao23333" {
		t.Errorf("handle = %q, want canonical server casing %q", got.Handle, "Doubao23333")
	}
	if got.FetchedAt != "2026-09-23T12:00:00Z" {
		t.Errorf("FetchedAt = %q, want the merge timestamp", got.FetchedAt)
	}
	// Judgement untouched.
	if got.Role != "creator" || got.Note != "keep me" || got.NotedAt != "2026-01-02T00:00:00Z" {
		t.Errorf("judgement disturbed: role=%q note=%q notedAt=%q", got.Role, got.Note, got.NotedAt)
	}
}

func TestMergeProfileFactsCreatesEntryWithEmptyJudgement(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	merged := settings.MergeProfileFacts(nil, "newbie", settings.Profile{
		Handle:         "newbie",
		Name:           "New",
		FollowersCount: 5,
	}, now)
	got, ok := settings.FindProfile(merged, "newbie")
	if !ok {
		t.Fatalf("new entry missing")
	}
	if got.Role != "" || got.Note != "" || got.NotedAt != "" {
		t.Errorf("new entry must start with empty judgement, got %+v", got)
	}
	if got.FetchedAt != "2026-09-23T12:00:00Z" {
		t.Errorf("FetchedAt = %q", got.FetchedAt)
	}
}

func TestMergeProfileFactsDoesNotPruneOthers(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	existing := map[string]settings.Profile{
		"other": {Handle: "other", Role: "fanwork"},
	}
	merged := settings.MergeProfileFacts(existing, "newbie", settings.Profile{Handle: "newbie"}, now)
	if _, ok := settings.FindProfile(merged, "other"); !ok {
		t.Fatalf("unrelated entry was pruned")
	}
}

func TestProfileAge(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		fetchedAt string
		want      string
	}{
		{"", "-"},                       // never fetched
		{"2026-09-23T11:59:15Z", "45s"}, // < 60s
		{"2026-09-23T11:48:00Z", "12m"}, // < 60m
		{"2026-09-23T07:00:00Z", "5h"},  // < 24h
		{"2026-09-20T12:00:00Z", "3d"},  // < 14d
		{"2026-09-09T12:00:00Z", "2w"},  // 14d -> weeks
		{"2026-09-23T12:00:01Z", "0s"},  // future skew clamps to 0
		{"not-a-timestamp", "-"},        // unparseable is missing, not a crash
	}
	for _, tc := range cases {
		if got := settings.ProfileAge(tc.fetchedAt, now); got != tc.want {
			t.Errorf("ProfileAge(%q) = %q, want %q", tc.fetchedAt, got, tc.want)
		}
	}
}

func TestPathsProfilesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := paths.New()
	if err != nil {
		t.Fatalf("paths.New: %v", err)
	}
	want := filepath.Join(home, ".nitter-cli", "profiles.toml")
	if p.ProfilesFile != want {
		t.Errorf("ProfilesFile = %q, want %q", p.ProfilesFile, want)
	}
}
