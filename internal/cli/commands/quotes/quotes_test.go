package quotes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/quotes"
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

func TestQuotesValidation(t *testing.T) {
	tempHome(t)

	// Missing ref -> exit 2
	code, _, _ := runCLI(t, "quotes")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}

	// Invalid ref -> exit 2
	code, _, errOut := runCLI(t, "quotes", "not-a-ref")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}

	// Non-positive limit -> exit 2 (0 was the "all" spelling and is gone)
	code, _, errOut = runCLI(t, "quotes", "12345", "--limit=-1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
	code, _, errOut = runCLI(t, "quotes", "12345", "--limit=0")
	if code != 2 {
		t.Fatalf("--limit=0: exit = %d, want 2 (stderr %q)", code, errOut)
	}

	// --json and --ndjson together -> exit 2
	code, _, errOut = runCLI(t, "quotes", "12345", "--json", "--ndjson")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %q)", code, errOut)
	}
}

func TestQuotesSuccessAndModes(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/2/status/12345/quotes") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"quotes": []map[string]any{
					{
						"id":         "901",
						"text":       "Quote with media",
						"created_at": "2026-07-20 14:11:00",
						"author": map[string]any{
							"screen_name": "quoter1",
						},
						"media": map[string]any{
							"all": []map[string]any{
								{"type": "photo", "url": "https://pbs.twimg.com/media/q.jpg"},
							},
						},
					},
					{
						"id":         "902",
						"text":       "Quote text only",
						"created_at": "2026-07-20 14:12:00",
						"author": map[string]any{
							"screen_name": "quoter2",
						},
					},
				},
				"cursor": "next_cur",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	t.Run("TTY table output", func(t *testing.T) {
		var out, errOut strings.Builder
		s := &invocation.Streams{
			In:          strings.NewReader(""),
			Out:         &out,
			Err:         &errOut,
			OutIsTTY:    true,
			RootOptions: &invocation.RootOptions{},
		}
		cmd := quotes.New(s)
		cmd.SetArgs([]string{"12345"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		outStr := out.String()
		if !strings.Contains(outStr, "901") || !strings.Contains(outStr, "@quoter1") {
			t.Errorf("out = %q, want tweet row 901", outStr)
		}
		if !strings.Contains(outStr, "902") || !strings.Contains(outStr, "@quoter2") {
			t.Errorf("out = %q, want tweet row 902", outStr)
		}
	})

	t.Run("NDJSON output envelope", func(t *testing.T) {
		code, out, _ := runCLI(t, "quotes", "12345", "--ndjson")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"kind":"tweet"`) || !strings.Contains(out, `"quotes:12345"`) {
			t.Errorf("out = %q, want quotes NDJSON envelope", out)
		}
	})

	t.Run("JSON output document", func(t *testing.T) {
		code, out, _ := runCLI(t, "quotes", "12345", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"id":"901"`) {
			t.Errorf("out = %q, want JSON with quote", out)
		}
	})

	t.Run("Media-only filter", func(t *testing.T) {
		code, out, _ := runCLI(t, "quotes", "12345", "--media-only", "--json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		var tw map[string]any
		if err := json.Unmarshal([]byte(out), &tw); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if tw["id"] != "901" {
			t.Errorf("got id = %v, want 901", tw["id"])
		}
	})
}

func TestQuotesEmpty(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":   200,
			"quotes": []map[string]any{},
		})
	}))
	defer srv.Close()

	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() {
		fxtwitter.EndpointOverrides.BaseURL = ""
	})

	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		RootOptions: &invocation.RootOptions{},
	}
	cmd := quotes.New(s)
	cmd.SetArgs([]string{"https://x.com/user/status/12345"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(errOut.String(), "(empty)") {
		t.Errorf("errOut = %q, want (empty)", errOut.String())
	}
}

// The pair below is the user-visible half of the 404 fix: the quotes endpoint
// answers 404 both for "this tweet has no quotes" and for an upstream failure
// (verified live 2026-09-24), so the command must tell the two apart instead of
// printing an empty list for both.

// emptyQuotesBody is the verbatim body FxTwitter returns with those 404s.
const emptyQuotesBody = `{"code":404,"results":[],"cursor":{"top":null,"bottom":null}}`

func quotesServer(t *testing.T, quoteCount int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/2/status/12345/quotes":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(emptyQuotesBody))
		case "/2/status/12345":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":   200,
				"status": map[string]any{"id": "12345", "quotes": quoteCount},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestQuotesNotFoundWithZeroQuotesIsEmpty(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")
	srv := quotesServer(t, 0)
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = "" })

	code, out, errOut := runCLI(t, "quotes", "12345", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the tweet really has no quotes; stderr = %q", code, errOut)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("stdout = %q, want []", out)
	}
}

func TestQuotesNotFoundWithQuotesExitsOne(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home, "fetch_backend = \"mix\"\n")
	srv := quotesServer(t, 3)
	fxtwitter.EndpointOverrides.BaseURL = srv.URL
	t.Cleanup(func() { fxtwitter.EndpointOverrides.BaseURL = "" })

	code, out, errOut := runCLI(t, "quotes", "12345", "--json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 — the tweet has 3 quotes, so an empty success would be a lie; stderr = %q", code, errOut)
	}
	if !strings.Contains(errOut, "not_found") {
		t.Errorf("stderr = %q, want the classified not_found kind", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing on the failure path", out)
	}
}
