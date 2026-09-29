package download

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// cleanStats summarizes one cleanDirectory run. Bytes counts the logical size
// of the files that were removed (not the space reclaimed on disk).
type cleanStats struct {
	Removed int
	Bytes   int64
	// Skipped counts deliberate skips: symlink/junction entries and
	// non-regular files. These are a normal part of walking a directory the
	// user may have pointed at anything, so they are visible but not a
	// failure.
	Skipped int
	// Failed counts real failures: an entry that could not be removed, an
	// entry whose metadata could not be read, or a directory that was
	// confirmed empty and still could not be pruned. A non-zero Failed means
	// the run did not do everything it set out to do and must not be
	// reported as a clean success.
	Failed int
}

// cleanDirectory removes every regular file under dir whose mtime is older
// than olderThan (i.e. strictly before now-olderThan), then prunes the
// subdirectories that became empty as a result.
//
// Contract:
//   - dir does not exist: zero stats, nil error (idempotent).
//   - dir itself is a symlink/junction: error, nothing is removed.
//   - dir is not a directory: error.
//   - symlink entries inside the tree are skipped and counted (never
//     followed, never removed, and their targets are never touched).
//   - only regular files are removed; dir itself is never removed.
//   - a file that cannot be removed increments Failed and the walk
//     continues (visible, never silent); a deliberate skip (a symlink
//     entry, a non-regular file) increments Skipped instead.
func cleanDirectory(dir string, olderThan time.Duration, now time.Time) (cleanStats, error) {
	var stats cleanStats

	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return stats, nil
		}
		return stats, fmt.Errorf("clean-temp: stat %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return stats, fmt.Errorf("clean-temp: %s is a symbolic link or junction; refusing to manage it", dir)
	}
	if !info.IsDir() {
		return stats, fmt.Errorf("clean-temp: %s is not a directory", dir)
	}

	cutoff := now.Add(-olderThan)
	// dirty collects the directories that lost a file, so the pruning pass
	// only ever considers directories this run emptied — a directory the user
	// happened to leave empty is left alone.
	dirty := make(map[string]bool)

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			stats.Failed++
			return nil
		}
		if path == dir {
			return nil
		}
		// WalkDir does not follow symlinks, so a link to a directory is a
		// leaf here: skip it without descending.
		if d.Type()&os.ModeSymlink != 0 {
			stats.Skipped++
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			stats.Skipped++
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			stats.Failed++
			return nil
		}
		if !fi.ModTime().Before(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			stats.Failed++
			return nil
		}
		stats.Removed++
		stats.Bytes += fi.Size()
		dirty[filepath.Dir(path)] = true
		return nil
	})
	if walkErr != nil {
		return stats, fmt.Errorf("clean-temp: walk %s: %w", dir, walkErr)
	}

	// Prune bottom-up: deepest paths first.
	seen := make(map[string]bool)
	var dirs []string
	for d := range dirty {
		for d != dir && d != "." && strings.HasPrefix(d, dir) {
			if !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
			d = filepath.Dir(d)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		if err := os.Remove(d); err != nil {
			// A non-empty directory is the intended "keep it" signal, so
			// re-check emptiness: still populated means expected, anything
			// else means the removal genuinely failed and must be visible.
			if entries, readErr := os.ReadDir(d); readErr == nil && len(entries) > 0 {
				continue
			}
			stats.Failed++
		}
	}
	return stats, nil
}
