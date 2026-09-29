package download

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// baseTime is the fixed "now" every case compares against.
var baseTime = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// writeAged writes content to path (creating parents) and back-dates its
// mtime to now-mtimeAgo.
func writeAged(t *testing.T, path, content string, now time.Time, mtimeAgo time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	mt := now.Add(-mtimeAgo)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func TestCleanDirectoryRemovesOldKeepsFresh(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.jpg")
	fresh := filepath.Join(dir, "fresh.jpg")
	writeAged(t, old, "old", baseTime, 200*time.Hour)
	writeAged(t, fresh, "fresh", baseTime, time.Hour)

	stats, err := cleanDirectory(dir, 168*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats.Removed != 1 || stats.Bytes != 3 || stats.Skipped != 0 {
		t.Fatalf("stats = %+v, want {Removed:1 Bytes:3 Skipped:0}", stats)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old file still present (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh file was removed: %v", err)
	}
}

func TestCleanDirectoryRemovesAnyFileNameShape(t *testing.T) {
	dir := t.TempDir()
	// A custom filename_template can produce arbitrary names; cleaning must
	// not depend on the default <id>-<seq> shape.
	odd := filepath.Join(dir, "NASA - 123_image.jpg")
	writeAged(t, odd, "x", baseTime, 200*time.Hour)

	stats, err := cleanDirectory(dir, 168*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats.Removed != 1 {
		t.Fatalf("Removed = %d, want 1", stats.Removed)
	}
}

func TestCleanDirectoryPrunesEmptiedSubdirsButKeepsRoot(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "temp", "nasa")
	writeAged(t, filepath.Join(sub, "a.jpg"), "a", baseTime, 200*time.Hour)
	// A sibling that stays non-empty must survive.
	keep := filepath.Join(dir, "temp", "kept")
	writeAged(t, filepath.Join(keep, "live.jpg"), "l", baseTime, time.Hour)

	stats, err := cleanDirectory(dir, 168*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats.Removed != 1 {
		t.Fatalf("Removed = %d, want 1", stats.Removed)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("emptied subdir survived (err=%v)", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("non-empty sibling removed: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("root dir was removed: %v", err)
	}
}

func TestCleanDirectoryMissingDirIsNoop(t *testing.T) {
	stats, err := cleanDirectory(filepath.Join(t.TempDir(), "nope"), 168*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats != (cleanStats{}) {
		t.Fatalf("stats = %+v, want zero", stats)
	}
}

func TestCleanDirectoryRejectsFileAndSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "afile")
	writeAged(t, file, "x", baseTime, 200*time.Hour)
	if _, err := cleanDirectory(file, 168*time.Hour, baseTime); err == nil {
		t.Error("cleanDirectory(file) = nil error, want an error")
	}

	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}
	if _, err := cleanDirectory(link, 168*time.Hour, baseTime); err == nil {
		t.Error("cleanDirectory(symlink) = nil error, want an error")
	}
}

func TestCleanDirectorySkipsSymlinkEntriesWithoutFollowing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scratch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The link target is an old file OUTSIDE dir: it must survive.
	outside := filepath.Join(root, "outside.jpg")
	writeAged(t, outside, "keep", baseTime, 200*time.Hour)
	link := filepath.Join(dir, "linked.jpg")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}
	writeAged(t, filepath.Join(dir, "real.jpg"), "gone", baseTime, 200*time.Hour)

	stats, err := cleanDirectory(dir, 168*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats.Removed != 1 || stats.Skipped != 1 {
		t.Fatalf("stats = %+v, want {Removed:1 Skipped:1}", stats)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("symlink target outside dir was deleted: %v", err)
	}
}

func TestCleanDirectoryKeepsFileAtExactCutoff(t *testing.T) {
	dir := t.TempDir()
	// mtime exactly == cutoff: Before() is false, so the file stays.
	writeAged(t, filepath.Join(dir, "edge.jpg"), "e", baseTime, 168*time.Hour)

	stats, err := cleanDirectory(dir, 168*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats.Removed != 0 {
		t.Fatalf("Removed = %d, want 0 (cutoff is exclusive)", stats.Removed)
	}
}

func TestCleanDirectoryZeroOlderThanRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	writeAged(t, filepath.Join(dir, "a.jpg"), "a", baseTime, time.Second)
	writeAged(t, filepath.Join(dir, "b.jpg"), "b", baseTime, time.Hour)

	stats, err := cleanDirectory(dir, 0, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats.Removed != 2 {
		t.Fatalf("Removed = %d, want 2", stats.Removed)
	}
}

func TestCleanDirectoryNonEmptySubdirIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	// A subdirectory that still holds a fresh file cannot be pruned. That is
	// the expected outcome, so it must NOT be counted as a failure.
	sub := filepath.Join(dir, "keepme")
	writeAged(t, filepath.Join(sub, "old.jpg"), "o", baseTime, 200*time.Hour)
	writeAged(t, filepath.Join(sub, "live.jpg"), "l", baseTime, time.Hour)

	stats, err := cleanDirectory(dir, 168*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("cleanDirectory: %v", err)
	}
	if stats.Removed != 1 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want {Removed:1 Failed:0}", stats)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Errorf("non-empty subdir was removed: %v", err)
	}
}
