// Package seen stores the persistent watch dedup state (seen.json): one
// SourceState per subscribed source, plus the pure merge/cap primitives the
// watch engine uses to advance it.
//
// Semantics ground truth is the AstrBot plugin this CLI ports
// (astrbot_plugin_nitter_tweets): SEEN_LIMIT_PER_USER caps each source's
// seen list, scan watermarks are numeric-only first-page anchors capped at
// 20, and a corrupt state file is a hard error — the store never silently
// resets state, because a silent reset would re-push a whole history.
package seen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/twitter-cli/sdk"
)

const (
	// SeenLimit caps each source's seen ID list, newest-first (plugin
	// SEEN_LIMIT_PER_USER).
	SeenLimit = 300
	// WatermarkLimit caps each source's scan watermark: the most recent
	// numeric status IDs anchoring the page-1 scan boundary (plugin
	// _normalize_scan_baseline_ids).
	WatermarkLimit = 20
	// SchemaVersion is the current seen.json schema version. Files written
	// with any other version are rejected, never migrated or reset.
	SchemaVersion = 1
)

// SourceState is one subscribed source's dedup state. SeenIDs is ordered
// newest-first; WatermarkIDs holds the page-1 scan anchors, newest-first.
type SourceState struct {
	Initialized  bool      `json:"initialized"`
	SeenIDs      []string  `json:"seen_ids"`
	WatermarkIDs []string  `json:"watermark_ids"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// File is the whole seen.json document.
type File struct {
	Version int                    `json:"version"`
	Sources map[string]SourceState `json:"sources"`
}

// Store is the on-disk state holder for one seen.json path. It is safe for
// concurrent use: Get and Put share the store mutex, and Puts are serialized
// process-wide by the atomic-write mutex (Windows rename-over-open-file).
type Store struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
	file File
}

// Open loads the state file at path. A missing file yields an empty store
// (nothing is created on disk — the first write happens on the first Put).
// An existing file is decoded strictly: unknown fields, trailing data and
// any version other than SchemaVersion are errors. A corrupt file returns a
// KindLocalState error and leaves the bytes untouched — no silent reset.
func Open(path string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	file, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, now: now, file: file}, nil
}

func loadFile(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{Version: SchemaVersion, Sources: map[string]SourceState{}}, nil
		}
		return File{}, twitter.Errorf(twitter.KindLocalState, "load seen state",
			"read %s: %w", path, err)
	}
	file, err := decodeStrict(data)
	if err != nil {
		return File{}, twitter.Errorf(twitter.KindLocalState, "load seen state",
			"parse %s: %w", path, err)
	}
	if file.Version != SchemaVersion {
		return File{}, twitter.Errorf(twitter.KindLocalState, "load seen state",
			"unsupported %s schema version %d (want %d); refusing to reset state",
			path, file.Version, SchemaVersion)
	}
	if file.Sources == nil {
		file.Sources = map[string]SourceState{}
	}
	return file, nil
}

// decodeStrict decodes exactly one File JSON document: unknown fields are
// rejected at every level and any trailing non-whitespace bytes are an error.
func decodeStrict(data []byte) (File, error) {
	var file File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return File{}, fmt.Errorf("decode seen file: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return File{}, errors.New("decode seen file: trailing data after JSON document")
		}
		return File{}, fmt.Errorf("decode seen file: trailing data after JSON document: %w", err)
	}
	return file, nil
}

// Get returns the state stored for source and whether it exists. Missing
// sources yield the zero value and false. The returned state is a copy:
// callers cannot mutate the store through it.
func (s *Store) Get(source string) (SourceState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.file.Sources[source]
	if ok {
		state.SeenIDs = slices.Clone(state.SeenIDs)
		state.WatermarkIDs = slices.Clone(state.WatermarkIDs)
	}
	return state, ok
}

// Put stores the state for source and persists the whole file atomically
// (same-directory temp file 0600 → Sync → rename). UpdatedAt is stamped
// from the store clock in UTC; the ID slices are stored as given and cloned
// so later caller mutations cannot leak into the file.
func (s *Store) Put(source string, state SourceState) error {
	if source == "" {
		return twitter.Errorf(twitter.KindLocalState, "save seen state", "empty source key")
	}
	state.UpdatedAt = s.now().UTC()
	state.SeenIDs = slices.Clone(state.SeenIDs)
	state.WatermarkIDs = slices.Clone(state.WatermarkIDs)

	s.mu.Lock()
	defer s.mu.Unlock()

	sources := s.file.Sources
	if sources == nil {
		sources = map[string]SourceState{}
	} else {
		sources = make(map[string]SourceState, len(sources)+1)
		for key, value := range s.file.Sources {
			sources[key] = value
		}
	}
	sources[source] = state

	data, err := jsonx.MarshalLine(File{Version: SchemaVersion, Sources: sources})
	if err != nil {
		return twitter.Errorf(twitter.KindLocalState, "save seen state", "encode: %w", err)
	}
	if err := writeFileAtomic(s.path, data); err != nil {
		return twitter.Errorf(twitter.KindLocalState, "save seen state", "%w", err)
	}
	// Commit to memory only after the file publish succeeded, so a failed
	// write leaves both disk and memory at the previous state.
	s.file.Sources = sources
	return nil
}

// MergeSeen merges new IDs in front of old IDs (newest-first), dropping
// empty strings and duplicates and capping the result at SeenLimit — the
// plugin merge_seen_ids semantics.
func MergeSeen(newIDs, oldIDs []string) []string {
	merged := make([]string, 0, len(newIDs)+len(oldIDs))
	known := make(map[string]struct{}, len(newIDs)+len(oldIDs))
	for _, id := range newIDs {
		if id == "" {
			continue
		}
		if _, dup := known[id]; dup {
			continue
		}
		known[id] = struct{}{}
		merged = append(merged, id)
		if len(merged) >= SeenLimit {
			return merged
		}
	}
	for _, id := range oldIDs {
		if id == "" {
			continue
		}
		if _, dup := known[id]; dup {
			continue
		}
		known[id] = struct{}{}
		merged = append(merged, id)
		if len(merged) >= SeenLimit {
			return merged
		}
	}
	return merged
}

// CapWatermark keeps only pure-numeric status IDs, deduplicates preserving
// order, and caps at WatermarkLimit — the plugin _normalize_scan_baseline_ids
// semantics (a numeric-only automatic baseline).
func CapWatermark(ids []string) []string {
	capped := make([]string, 0, min(len(ids), WatermarkLimit))
	for _, id := range ids {
		value := strings.TrimSpace(id)
		if value == "" {
			continue
		}
		if _, err := strconv.Atoi(value); err != nil {
			continue
		}
		if slices.Contains(capped, value) {
			continue
		}
		capped = append(capped, value)
		if len(capped) >= WatermarkLimit {
			break
		}
	}
	return capped
}

// SourceKey builds the seen map key "<kind>:<ref>" (e.g. "user:NASA",
// "tag:%23AI", "list:12345"). An empty part yields "" so malformed sources
// are never stored.
func SourceKey(kind, ref string) string {
	if kind == "" || ref == "" {
		return ""
	}
	return kind + ":" + ref
}

// writeMu serializes atomic writes within this process. On Windows,
// os.Rename cannot replace a destination file while another goroutine in
// this process still has it open, so concurrent temp-file + rename writers
// would race each other's open handles; the package-level mutex keeps every
// writer's rename safe from the others' staging files (same pattern as
// internal/config/settings).
var writeMu sync.Mutex

// writeFileAtomic writes data to path through a same-directory temp file
// (0600 → Sync → close) and an atomic rename onto the destination.
func writeFileAtomic(path string, data []byte) (resultErr error) {
	writeMu.Lock()
	defer writeMu.Unlock()

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("stage seen state: %w", err)
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
		return fmt.Errorf("stage seen state: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("stage seen state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("stage seen state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("stage seen state: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish seen state: %w", err)
	}
	return nil
}
