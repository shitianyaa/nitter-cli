package settings_test

import (
	"path/filepath"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

func TestCirclesLoadAndSave(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "circles.toml")

	// Missing file returns empty map without error
	circles, err := settings.LoadCircles(path)
	if err != nil {
		t.Fatalf("LoadCircles on missing file: %v", err)
	}
	if len(circles) != 0 {
		t.Fatalf("len(circles) = %d, want 0", len(circles))
	}

	// Save circles
	c1 := settings.Circle{
		Key:         "cosers",
		Name:        "Cosplayers",
		Description: "Anime cosers",
		Users:       []string{"mahina_cos", "enako_cos"},
	}
	circles["cosers"] = c1

	if err := settings.SaveCircles(path, circles); err != nil {
		t.Fatalf("SaveCircles: %v", err)
	}

	// Reload circles
	loaded, err := settings.LoadCircles(path)
	if err != nil {
		t.Fatalf("reload LoadCircles: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("len(loaded) = %d, want 1", len(loaded))
	}
	got, ok := settings.FindCircle(loaded, "cosers")
	if !ok {
		t.Fatalf("FindCircle(loaded, cosers) not found")
	}
	if got.Name != "Cosplayers" || len(got.Users) != 2 {
		t.Fatalf("got = %+v, want Cosplayers with 2 users", got)
	}

	// Find by Name
	gotName, ok := settings.FindCircle(loaded, "Cosplayers")
	if !ok || gotName.Key != "cosers" {
		t.Fatalf("FindCircle by Name failed: %+v, ok=%v", gotName, ok)
	}

	// Find case-insensitive
	gotLower, ok := settings.FindCircle(loaded, "COSERS")
	if !ok || gotLower.Key != "cosers" {
		t.Fatalf("FindCircle case-insensitive failed: %+v, ok=%v", gotLower, ok)
	}
}
