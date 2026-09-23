package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source classifies how the running binary was installed, which decides
// whether it may replace itself at all.
type Source int

const (
	// SourceUnknown means detection could not classify the installation.
	SourceUnknown Source = iota
	// SourceArchive is a release-archive (or manual) install: self-update works.
	SourceArchive
	// SourceGoInstall is a `go install` build: update it with go install
	// instead, so a later `go install` cannot silently undo the update.
	SourceGoInstall
	// SourceDevelopment is a build without release metadata.
	SourceDevelopment
)

func (s Source) String() string {
	switch s {
	case SourceArchive:
		return "archive"
	case SourceGoInstall:
		return "go install"
	case SourceDevelopment:
		return "development build"
	default:
		return "unknown"
	}
}

// GoBinDirs returns the directories `go install` writes binaries into:
// GOBIN when set, plus each GOPATH entry's bin directory.
func GoBinDirs() []string {
	var dirs []string
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		dirs = append(dirs, filepath.Clean(gobin))
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	for _, entry := range filepath.SplitList(gopath) {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		dirs = append(dirs, filepath.Join(filepath.Clean(entry), "bin"))
	}
	return dirs
}

// DetectSource classifies the installation owning executable. A development
// build is reported before any path inspection: there is no release to
// replace it with.
func DetectSource(executable, version string) (Source, error) {
	if version == "" || version == "dev" {
		return SourceDevelopment, nil
	}
	if strings.TrimSpace(executable) == "" {
		return SourceUnknown, nil
	}
	resolved, err := filepath.Abs(executable)
	if err != nil {
		return SourceUnknown, fmt.Errorf("resolve executable %q: %w", executable, err)
	}
	// A symlinked launcher must be classified by its real target: that is the
	// file an update would replace.
	if link, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = link
	}
	dir := filepath.Dir(resolved)
	for _, gobin := range GoBinDirs() {
		if samePath(dir, gobin) {
			return SourceGoInstall, nil
		}
	}
	return SourceArchive, nil
}

// samePath reports whether two directories name the same location. Both sides
// go through the same canonicalization: comparing a resolved path against a raw
// one silently fails whenever the two spellings differ, which on Windows is the
// normal case — an 8.3 short name (“C:\Users\RUNNER~1\...“) and its long
// form name the same directory, and filepath.Abs/Clean keep them distinct while
// EvalSymlinks folds them together. Misclassifying that way would let a
// `go install` binary be replaced by self-update, so the comparison has to be
// spelling-agnostic rather than merely case-insensitive.
func samePath(a, b string) bool {
	return strings.EqualFold(canonicalDir(a), canonicalDir(b))
}

// canonicalDir resolves a path to one spelling: absolute, cleaned, and
// symlink/8.3-normalized where the platform supports it. A path that cannot be
// resolved keeps its absolute+cleaned form, so a missing directory still
// compares by name instead of failing open.
func canonicalDir(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)
	// EvalSymlinks also normalizes Windows 8.3 short names to their long form,
	// which is what makes the two spellings of one directory compare equal.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
