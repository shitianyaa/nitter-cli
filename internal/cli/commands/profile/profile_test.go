package profile_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/profile"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
)

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"NITTER_DEFAULT_LIMIT", "NITTER_LOG_LEVEL", "NITTER_LOG_FORMAT",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, "")
	}
	return home
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".nitter-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestProfileValidation(t *testing.T) {
	tempHome(t)

	// Missing handle -> exit 2
	code, _, _ := runCLI(t, "profile")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}

	// Invalid handle -> exit 2
	code, _, errOut := runCLI(t, "profile", "invalid-handle-with-dashes!")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}

	// --json and --ndjson together -> exit 2
	code, _, errOut = runCLI(t, "profile", "nasa", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
}

func TestProfileSuccessAndModes(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/2/profile/NASA") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"user": map[string]any{
					"screen_name":     "NASA",
					"name":            "NASA Official",
					"description":     "Exploring the cosmos.",
					"followers_count": 83000000,
					"following_count": 250,
					"statuses_count":  74000,
					"media_count":     12000,
					"avatar_url":      "https://pbs.twimg.com/avatar.jpg",
				},
			})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/2/profile/notfound") {
			http.Error(w, `{"code":404,"message":"not found"}`, http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	t.Run("TTY user card output", func(t *testing.T) {
		var out, errOut strings.Builder
		s := &invocation.Streams{
			In:          strings.NewReader(""),
			Out:         &out,
			Err:         &errOut,
			OutIsTTY:    true,
			RootOptions: &invocation.RootOptions{},
		}
		cmd := profile.New(s)
		cmd.SetArgs([]string{"NASA"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		outStr := out.String()
		if !strings.Contains(outStr, "@NASA (NASA Official)") {
			t.Errorf("out = %q, want card header", outStr)
		}
		if !strings.Contains(outStr, "Followers:  83000000") {
			t.Errorf("out = %q, want followers count", outStr)
		}
	})

	t.Run("NDJSON output envelope", func(t *testing.T) {
		code, out, _ := runCLI(t, "profile", "NASA", "--ndjson")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"kind":"profile"`) || !strings.Contains(out, `"handle":"NASA"`) {
			t.Errorf("out = %q, want profile NDJSON envelope", out)
		}
	})

	t.Run("JSON output", func(t *testing.T) {
		code, out, _ := runCLI(t, "profile", "NASA", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"handle":"NASA"`) {
			t.Errorf("out = %q, want profile JSON", out)
		}
	})

	t.Run("User not found exits 1", func(t *testing.T) {
		code, _, errOut := runCLI(t, "profile", "notfound")
		if code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(errOut, "not found") {
			t.Errorf("errOut = %q, want not found message", errOut)
		}
	})
}
