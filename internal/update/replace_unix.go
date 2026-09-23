//go:build !windows

package update

import "os"

// replaceExecutable performs an atomic same-filesystem replacement. The
// staged file is created in the target's own directory, so the rename is
// atomic and a running process keeps its inode.
func replaceExecutable(src, dst string) error {
	return os.Rename(src, dst)
}

// CleanupPendingUpdate has no work outside Windows: the rename already
// released the previous file.
func CleanupPendingUpdate() error { return nil }
