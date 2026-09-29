package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatContributorLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"shitianyaa", "- @shitianyaa"},
		{"@shitianyaa", "- @shitianyaa"},
		{"alice-bob", "- @alice-bob"},
		{"John Doe", "- John Doe"},
		{"  user123  ", "- @user123"},
	}
	for _, tc := range tests {
		got := formatContributorLine(tc.input)
		if got != tc.want {
			t.Errorf("formatContributorLine(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestIsBotLogin(t *testing.T) {
	bots := []string{"web-flow", "github-actions[bot]", "dependabot[bot]", "renovate[bot]", "github-actions", "dependabot", "renovate", ""}
	for _, b := range bots {
		if !isBotLogin(b) {
			t.Errorf("isBotLogin(%q) = false, want true", b)
		}
	}

	humans := []string{"shitianyaa", "torvalds", "alice", "bob-builder"}
	for _, h := range humans {
		if isBotLogin(h) {
			t.Errorf("isBotLogin(%q) = true, want false", h)
		}
	}
}

func TestAssembleReleaseNotesSuccess(t *testing.T) {
	repo := t.TempDir()

	if err := runGit(repo, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "config", "user.name", "shitianyaa"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "config", "user.email", "shitianyaa01@gmail.com"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repo, "init.txt"), []byte("v0.8.0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "add", "init.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "tag", "v0.8.0"); err != nil {
		t.Fatal(err)
	}

	// Create changelog files
	clDir := filepath.Join(repo, "changelog", "v0.9.0")
	if err := os.MkdirAll(clDir, 0o755); err != nil {
		t.Fatal(err)
	}
	enContent := "# v0.9.0 — 2026-09-29\n\nEnglish release notes.\n\n## Added\n- Feature A"
	zhContent := "# v0.9.0 — 2026-09-29\n\nChinese release notes.\n\n## Added\n- Feature A"
	if err := os.WriteFile(filepath.Join(clDir, "en.md"), []byte(enContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clDir, "zh-CN.md"), []byte(zhContent), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runGit(repo, "add", "changelog"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "commit", "-m", "add v0.9.0 changelog"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "tag", "v0.9.0"); err != nil {
		t.Fatal(err)
	}

	// Mock GitHub API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/compare/") {
			data := map[string]any{
				"commits": []map[string]any{
					{"author": map[string]any{"login": "shitianyaa"}},
					{"author": map[string]any{"login": "web-flow"}},
					{"author": map[string]any{"login": "github-actions[bot]"}},
					{"author": map[string]any{"login": "alice"}},
				},
			}
			_ = json.NewEncoder(w).Encode(data)
			return
		}
		if strings.Contains(r.URL.Path, "/releases/generate-notes") {
			data := map[string]any{
				"body": "## What\x27s Changed\n* feat by @bob in #12\n* fix by @shitianyaa in #13\n",
			}
			_ = json.NewEncoder(w).Encode(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	notes, err := assembleReleaseNotes(assembleConfig{
		RepoRoot:   repo,
		Tag:        "v0.9.0",
		GitHubRepo: "shitianyaa/nitter-cli",
		APIBase:    server.URL,
		Token:      "mock-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("assembleReleaseNotes error: %v", err)
	}

	if !strings.Contains(notes, enContent) {
		t.Errorf("assembled notes missing enContent: %s", notes)
	}
	if !strings.Contains(notes, zhContent) {
		t.Errorf("assembled notes missing zhContent: %s", notes)
	}
	if !strings.Contains(notes, "---") {
		t.Errorf("assembled notes missing separator: %s", notes)
	}
	if !strings.Contains(notes, "## Contributors / 贡献者") {
		t.Errorf("assembled notes missing Contributors section: %s", notes)
	}
	if !strings.Contains(notes, "- @alice") {
		t.Errorf("assembled notes missing - @alice: %s", notes)
	}
	if !strings.Contains(notes, "- @bob") {
		t.Errorf("assembled notes missing - @bob: %s", notes)
	}
	if !strings.Contains(notes, "- @shitianyaa") {
		t.Errorf("assembled notes missing - @shitianyaa: %s", notes)
	}
	if strings.Contains(notes, "web-flow") || strings.Contains(notes, "github-actions[bot]") {
		t.Errorf("assembled notes should not contain bots: %s", notes)
	}
	if strings.Contains(notes, "Thanks to all contributors who participated in this release:") ||
		strings.Contains(notes, "感谢所有参与本版本贡献的开发者：") {
		t.Errorf("assembled notes must not contain introductory greeting sentences: %s", notes)
	}
}

func TestAssembleReleaseNotesMissingFiles(t *testing.T) {
	repo := t.TempDir()
	_, err := assembleReleaseNotes(assembleConfig{
		RepoRoot: repo,
		Tag:      "v1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for missing changelog files, got nil")
	}

	clDir := filepath.Join(repo, "changelog", "v1.0.0")
	if err := os.MkdirAll(clDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clDir, "en.md"), []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clDir, "zh-CN.md"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = assembleReleaseNotes(assembleConfig{
		RepoRoot: repo,
		Tag:      "v1.0.0",
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty changelog error, got %v", err)
	}
}

func TestAssembleReleaseNotesInvalidTag(t *testing.T) {
	_, err := assembleReleaseNotes(assembleConfig{
		RepoRoot: ".",
		Tag:      "1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for tag without v, got nil")
	}
}
