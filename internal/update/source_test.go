package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectSourceDevelopment(t *testing.T) {
	for _, version := range []string{"dev", ""} {
		src, err := DetectSource("/usr/local/bin/nitter", version)
		if err != nil {
			t.Fatalf("DetectSource(version=%q): %v", version, err)
		}
		if src != SourceDevelopment {
			t.Errorf("version %q: src = %v, want SourceDevelopment", version, src)
		}
	}
}

func TestDetectSourceGoInstallViaGobin(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "nitter")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("GOBIN", dir)
	src, err := DetectSource(exe, "0.7.0")
	if err != nil {
		t.Fatalf("DetectSource: %v", err)
	}
	if src != SourceGoInstall {
		t.Errorf("src = %v, want SourceGoInstall", src)
	}
}

func TestDetectSourceGoInstallViaGopath(t *testing.T) {
	gopath := t.TempDir()
	bin := filepath.Join(gopath, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	exe := filepath.Join(bin, "nitter")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", gopath)
	src, err := DetectSource(exe, "0.7.0")
	if err != nil {
		t.Fatalf("DetectSource: %v", err)
	}
	if src != SourceGoInstall {
		t.Errorf("src = %v, want SourceGoInstall (GOPATH/bin)", src)
	}
}

func TestDetectSourceArchiveByDefault(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "nitter")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join(dir, "gopath"))
	src, err := DetectSource(exe, "0.7.0")
	if err != nil {
		t.Fatalf("DetectSource: %v", err)
	}
	if src != SourceArchive {
		t.Errorf("src = %v, want SourceArchive", src)
	}
}

// A symlinked launcher (/usr/local/bin/nitter → ~/go/bin/nitter) must be
// classified by its real target: that is the file an update would replace.
func TestDetectSourceResolvesSymlink(t *testing.T) {
	real := t.TempDir()
	target := filepath.Join(real, "nitter")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "nitter")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("GOBIN", real)
	src, err := DetectSource(link, "0.7.0")
	if err != nil {
		t.Fatalf("DetectSource: %v", err)
	}
	if src != SourceGoInstall {
		t.Errorf("src = %v, want SourceGoInstall (symlink must be resolved)", src)
	}
}

func TestDetectSourceUnknownWithoutPath(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	src, err := DetectSource("", "0.7.0")
	if err != nil {
		t.Fatalf("DetectSource: %v", err)
	}
	if src != SourceUnknown {
		t.Errorf("src = %v, want SourceUnknown for an empty executable path", src)
	}
}

func TestSourceString(t *testing.T) {
	cases := map[Source]string{
		SourceArchive:     "archive",
		SourceGoInstall:   "go install",
		SourceDevelopment: "development build",
		SourceUnknown:     "unknown",
	}
	for src, want := range cases {
		if got := src.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", src, got, want)
		}
	}
}

func TestGoBinDirs(t *testing.T) {
	t.Setenv("GOBIN", "/custom/bin")
	t.Setenv("GOPATH", "/gp1"+string(os.PathListSeparator)+"/gp2")
	dirs := GoBinDirs()
	if len(dirs) != 3 {
		t.Fatalf("GoBinDirs = %v, want 3 entries", dirs)
	}
	if dirs[0] != filepath.Clean("/custom/bin") {
		t.Errorf("dirs[0] = %q, want the GOBIN entry first", dirs[0])
	}
	if dirs[1] != filepath.Join(filepath.Clean("/gp1"), "bin") {
		t.Errorf("dirs[1] = %q", dirs[1])
	}
	if dirs[2] != filepath.Join(filepath.Clean("/gp2"), "bin") {
		t.Errorf("dirs[2] = %q", dirs[2])
	}
}

// A directory reachable through two spellings must compare equal: the two
// halves of the classification resolve paths differently (the executable gets
// EvalSymlinks, GOBIN historically did not), and a mismatch would let a
// `go install` binary be replaced. Windows 8.3 short names and symlinks are
// both instances of this, so the test asserts the invariant directly rather
// than relying on a platform-specific path form existing.
func TestSamePathNormalizesBothSides(t *testing.T) {
	real := t.TempDir()
	if canonicalDir(real) != canonicalDir(real) {
		t.Fatalf("canonicalDir is not stable for %q", real)
	}
	// Trailing separators and "." hops must not defeat the match.
	for _, variant := range []string{
		real + string(os.PathSeparator),
		real + string(os.PathSeparator) + ".",
		real + string(os.PathSeparator) + "sub" + string(os.PathSeparator) + "..",
	} {
		if !samePath(real, variant) {
			t.Errorf("samePath(%q, %q) = false, want true", real, variant)
		}
	}
	// Case-insensitivity holds on every platform (Windows paths are
	// case-insensitive; EqualFold is a superset that never breaks a real path).
	if !samePath(real, strings.ToUpper(real)) {
		t.Errorf("samePath must be case-insensitive")
	}
	// Different directories must not match.
	other := t.TempDir()
	if samePath(real, other) {
		t.Errorf("samePath(%q, %q) = true for distinct dirs", real, other)
	}
	// A symlinked spelling of the same directory must match its target: this is
	// the exact discrepancy that made the CI classification wrong.
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Logf("symlinks unavailable (%v); the symlink half is covered on CI", err)
		return
	}
	if !samePath(real, link) {
		t.Errorf("samePath(%q, %q) = false, want true: a symlinked spelling names the same dir", real, link)
	}
}
