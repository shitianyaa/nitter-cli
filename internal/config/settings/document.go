package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// SaveKnown reads the whole TOML tree at path, lets mut change only the
// known top-level keys, and marshals the tree back atomically at 0600.
// Untouched keys — including unknown ones and the [[instances]] /
// [[watch.sources]] array tables — survive verbatim because they were never
// modified. Comments are NOT guaranteed to be preserved (javdb-cli
// semantics; reintroduce a TOML editor library if that is ever needed).
func SaveKnown(path string, mut func(map[string]any) error) error {
	if mut == nil {
		return errors.New("mutate config: nil mutator")
	}
	tree, err := loadTree(path)
	if err != nil {
		return err
	}
	if err := mut(tree); err != nil {
		return fmt.Errorf("mutate config: %w", err)
	}
	data, err := toml.Marshal(tree)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// loadTree reads the whole TOML document at path into a generic tree; a
// missing file yields an empty tree so callers can seed a fresh config.
func loadTree(path string) (map[string]any, error) {
	tree := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tree, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := toml.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return tree, nil
}

// writeMu serializes atomic writes within this process. On Windows,
// os.Rename cannot replace a destination file while another goroutine in
// this process still has it open, so concurrent temp-file + rename writers
// would race each other's open handles; the package-level mutex keeps every
// writer's rename safe from the others' staging files.
var writeMu sync.Mutex

// writeFileAtomic writes data to path through a same-directory temp file
// (0600 → Sync → close) and an atomic rename onto the destination.
func writeFileAtomic(path string, data []byte) (resultErr error) {
	writeMu.Lock()
	defer writeMu.Unlock()

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create app dir: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, temporary.Close())
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, removeErr)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("stage config: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish config: %w", err)
	}
	return nil
}
