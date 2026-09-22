// Package paths manages the on-disk locations of nitter-cli's config and
// state files under a single data directory in the user home.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AppDirName is the single data directory on every OS (javdb-cli convention).
const AppDirName = ".nitter-cli"

// Paths bundles the locations derived from the user home directory.
type Paths struct {
	Dir         string
	ConfigFile  string
	CirclesFile string
	StateDir    string
	SeenFile    string
}

// New assembles the paths under the user home directory; it creates nothing.
func New() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, AppDirName)
	return Paths{
		Dir:         dir,
		ConfigFile:  filepath.Join(dir, "config.toml"),
		CirclesFile: filepath.Join(dir, "circles.toml"),
		StateDir:    filepath.Join(dir, "state"),
		SeenFile:    filepath.Join(dir, "state", "seen.json"),
	}, nil
}

// EnsureDefaultConfigFile publishes the baseline config at cfgPath without
// ever overwriting: the content is staged in a same-directory temp file
// (0600, synced, closed) and then atomically published with os.Link, which
// fails with EEXIST when the target already exists. Concurrent or repeated
// callers therefore keep the existing winner's complete config; the staging
// file is always removed.
func EnsureDefaultConfigFile(cfgPath, defaultTOML string) (err error) {
	directory := filepath.Dir(cfgPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create app dir: %w", err)
	}
	tmp, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, tmp.Close())
		}
		if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if _, err := tmp.WriteString(defaultTOML); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return fmt.Errorf("stage config: %w", err)
	}
	closed = true
	// Same-directory hard link publishes with no-replace semantics: by the
	// time the target appears, the content is fully written, synced and
	// closed; a concurrent creator that already published keeps its file.
	if err := os.Link(tmpPath, cfgPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("publish config: %w", err)
	}
	return nil
}
