package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
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
