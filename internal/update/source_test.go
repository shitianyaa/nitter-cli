package update

import (
	"os"
	"path/filepath"
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
