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

// samePath compares two directories, tolerating separators and case.
func samePath(a, b string) bool {
	ca, err := filepath.Abs(a)
	if err != nil {
		ca = a
	}
	cb, err := filepath.Abs(b)
	if err != nil {
		cb = b
	}
	return strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}
