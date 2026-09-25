package verificationpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLinePreservesQuotedPipeAndSplitsPipeline(t *testing.T) {
	command, err := ParseLine(`echo "a | b" | nitter get 1234567890`)
	if err != nil {
		t.Fatal(err)
	}
	if len(command.Stages) != 2 {
		t.Fatalf("stages = %d, want 2", len(command.Stages))
	}
	if got := command.Stages[0].Argv[1]; got != "a | b" {
		t.Fatalf("quoted pipe = %q", got)
	}
	if _, err := ParseLine("nitter search nitter && echo nope"); err == nil {
		t.Fatal("expected shell operator rejection")
	}
}

func TestWhitelistDenyAndWorkspaceBoundary(t *testing.T) {
	root := t.TempDir()
	whitelistPath := filepath.Join(root, "whitelist.txt")
	if err := os.WriteFile(whitelistPath, []byte("nitter *\nnitter !config\ncat @\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "image.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := LoadWhitelist(whitelistPath)
	if err != nil {
		t.Fatal(err)
	}
	allowed, _ := ParseLine("cat image.jpg | nitter search nitter")
	if err := w.Validate(allowed, "nitter", root); err != nil {
		t.Fatalf("allowed pipeline rejected: %v", err)
	}
	denied, _ := ParseLine("nitter config set proxy http://127.0.0.1:8080")
	if err := w.Validate(denied, "nitter", root); err == nil {
		t.Fatal("expected config rejection")
	}
	proxy, _ := ParseLine("nitter --proxy=https://example.invalid search nitter")
	if err := w.Validate(proxy, "nitter", root); err == nil {
		t.Fatal("expected inline proxy rejection")
	}
	instance, _ := ParseLine("nitter --instance https://example.invalid timeline nitter")
	if err := w.Validate(instance, "nitter", root); err == nil {
		t.Fatal("expected instance override rejection")
	}
	escape, _ := ParseLine("cat ../outside")
	if err := w.Validate(escape, "nitter", root); err == nil {
		t.Fatal("expected workspace escape rejection")
	}
}

func TestOutputFlagMustStayInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	whitelistPath := filepath.Join(root, "whitelist.txt")
	if err := os.WriteFile(whitelistPath, []byte("nitter *\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := LoadWhitelist(whitelistPath)
	if err != nil {
		t.Fatal(err)
	}
	inside, _ := ParseLine("nitter download 1234567890 --output media")
	if err := w.Validate(inside, "nitter", root); err != nil {
		t.Fatalf("workspace-relative output rejected: %v", err)
	}
	escaping, _ := ParseLine("nitter download 1234567890 --output ../outside")
	if err := w.Validate(escaping, "nitter", root); err == nil {
		t.Fatal("expected output escape rejection")
	}
}
