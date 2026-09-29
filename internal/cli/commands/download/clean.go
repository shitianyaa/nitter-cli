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
	Skipped int
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
//   - a file that cannot be removed is counted as skipped and the walk
//     continues (visible via Skipped, never silent).
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
			stats.Skipped++
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
			stats.Skipped++
			return nil
		}
		if !fi.ModTime().Before(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			stats.Skipped++
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

	// Prune bottom-up: deepest paths first. os.Remove fails on a non-empty
	// directory (ENOTEMPTY on Unix, ERROR_DIR_NOT_EMPTY on Windows) and that
	// failure is the intended "keep it" signal — ignored on purpose.
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
		_ = os.Remove(d)
	}
	return stats, nil
}
