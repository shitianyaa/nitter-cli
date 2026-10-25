package paths_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/config/paths"
)

func TestNewUnderTempHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	p, err := paths.New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	dir := filepath.Join(home, paths.AppDirName)
	want := paths.Paths{
		Dir:          dir,
		ConfigFile:   filepath.Join(dir, "config.toml"),
		CirclesFile:  filepath.Join(dir, "circles.toml"),
		ProfilesFile: filepath.Join(dir, "profiles.toml"),
		StateDir:     filepath.Join(dir, "state"),
		SeenFile:     filepath.Join(dir, "state", "seen.json"),
	}
	if p != want {
		t.Fatalf("New() = %+v, want %+v", p, want)
	}
	// New() only assembles paths; it must not create anything on disk.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("New() must not create %s (stat err = %v)", dir, err)
	}
}

func TestEnsureDefaultConfigFileReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory chmod does not enforce write denial on Windows")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	const custom = "default_limit = 99\n"
	if err := os.WriteFile(cfgPath, []byte(custom), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	probe, err := os.CreateTemp(dir, ".write-probe-*")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("current user can write despite directory permissions")
	}
	if !os.IsPermission(err) {
		t.Fatalf("write probe failed for an unexpected reason: %v", err)
	}
	if err := paths.EnsureDefaultConfigFile(cfgPath, "default_limit = 20\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil || string(data) != custom {
		t.Fatalf("existing config = %q, error = %v", data, err)
	}
	if err := paths.EnsureDefaultConfigFile(filepath.Join(dir, "missing.toml"), custom); err == nil {
		t.Fatal("initializing a missing config in a read-only directory must fail")
	}
}

func TestEnsureDefaultConfigFileConcurrentCreators(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	candidates := make(map[string]bool, callers)
	for i := 0; i < callers; i++ {
		content := fmt.Sprintf("# creator %d\n%s", i, strings.Repeat("default_limit = 20\n", 1024))
		candidates[content] = true
		go func() {
			<-start
			results <- paths.EnsureDefaultConfigFile(cfgPath, content)
		}()
	}
	close(start)
	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Errorf("creator failed: %v", err)
		}
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !candidates[string(data)] {
		t.Fatal("published config is not one complete candidate")
	}
	entries, err := os.ReadDir(filepath.Dir(cfgPath))
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("unexpected directory contents: %v, error = %v", entries, err)
	}
}

func TestEnsureDefaultConfigFileConcurrentCallersPreserveWinner(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	const callers = 16
	const winner = "# published winner\ndefault_limit = 99\n"
	ready := make(chan struct{}, callers)
	start := make(chan struct{})
	results := make(chan error, callers)
	// Keep contenders active but gated until the winner has been published
	// and recorded. This avoids relying on scheduler timing to observe it.
	for i := 0; i < callers; i++ {
		content := fmt.Sprintf("# contender %d\ndefault_limit = 20\n", i)
		go func() {
			ready <- struct{}{}
			<-start
			results <- paths.EnsureDefaultConfigFile(cfgPath, content)
		}()
	}
	for i := 0; i < callers; i++ {
		<-ready
	}
	publishErr := paths.EnsureDefaultConfigFile(cfgPath, winner)
	winnerInfo, statErr := os.Stat(cfgPath)
	// Release and join every contender before reporting setup failures.
	close(start)
	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Errorf("contender failed: %v", err)
		}
	}
	if publishErr != nil {
		t.Fatal(publishErr)
	}
	if statErr != nil {
		t.Fatal(statErr)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil || string(data) != winner {
		t.Fatalf("published winner changed: content = %q, error = %v", data, err)
	}
	finalInfo, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(winnerInfo, finalInfo) {
		t.Fatal("published winner was replaced")
	}
}

func TestEnsureDefaultConfigFilePreservesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing.toml")
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, cfgPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := paths.EnsureDefaultConfigFile(cfgPath, "default_limit = 20\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(cfgPath); err != nil || got != target {
		t.Fatalf("symlink = %q, error = %v", got, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("symlink target must remain absent, error = %v", err)
	}
}

func TestEnsureDefaultConfigFileCreatesOnce(t *testing.T) {
	const defaultTOML = "# nitter-cli configuration\ndefault_limit = 20\n"

	t.Run("first call creates the file with identical content", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), paths.AppDirName, "config.toml")

		if err := paths.EnsureDefaultConfigFile(cfgPath, defaultTOML); err != nil {
			t.Fatalf("EnsureDefaultConfigFile() error = %v", err)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read published config: %v", err)
		}
		if string(data) != defaultTOML {
			t.Fatalf("published content = %q, want %q", data, defaultTOML)
		}
		if runtime.GOOS != "windows" { // Windows chmod only toggles the read-only bit.
			info, err := os.Stat(cfgPath)
			if err != nil {
				t.Fatalf("stat published config: %v", err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("published mode = %v, want 0600", got)
			}
		}
	})

	t.Run("existing file is never overwritten", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		const custom = "custom_limit = 99\n"

		if err := os.WriteFile(cfgPath, []byte(custom), 0o600); err != nil {
			t.Fatalf("write pre-existing config: %v", err)
		}
		if err := paths.EnsureDefaultConfigFile(cfgPath, defaultTOML); err != nil {
			t.Fatalf("EnsureDefaultConfigFile() error = %v", err)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read existing config: %v", err)
		}
		if string(data) != custom {
			t.Fatalf("existing config overwritten: got %q, want %q", data, custom)
		}
	})

	t.Run("no staging files left behind", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), paths.AppDirName, "config.toml")

		for i := 0; i < 2; i++ {
			if err := paths.EnsureDefaultConfigFile(cfgPath, defaultTOML); err != nil {
				t.Fatalf("EnsureDefaultConfigFile() call %d error = %v", i+1, err)
			}
		}
		entries, err := os.ReadDir(filepath.Dir(cfgPath))
		if err != nil {
			t.Fatalf("read app dir: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "config.toml" {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("app dir entries = %v, want only [config.toml]", names)
		}
	})
}
