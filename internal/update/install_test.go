package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// releaseServer serves a fake release: checksums.txt plus one archive. When
// tamper is true the archive bytes are replaced after the checksum is
// computed, so the checksum verification must fail.
func releaseServer(t *testing.T, tag, archiveName string, archive []byte, tamper bool) (*httptest.Server, Release) {
	t.Helper()
	sum := sha256.Sum256(archive)
	body := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), archiveName)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "checksums.txt":
			_, _ = w.Write([]byte(body))
		case archiveName:
			if tamper {
				_, _ = w.Write([]byte("tampered archive contents"))
				return
			}
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	rel := Release{Tag: tag, Version: tag[1:], Assets: []Asset{
		{Name: "checksums.txt", URL: srv.URL + "/" + tag + "/checksums.txt"},
		{Name: archiveName, URL: srv.URL + "/" + tag + "/" + archiveName},
	}}
	return srv, rel
}

// withAssetPrefix points the trusted-URL prefix at a test server for the
// duration of one test.
func withAssetPrefix(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := assetURLPrefix
	assetURLPrefix = srv.URL + "/"
	t.Cleanup(func() { assetURLPrefix = old })
}

func TestInstallerReplacesBinary(t *testing.T) {
	payload := []byte("new binary bytes")
	archive := tarGz(t, "nitter", payload)
	srv, rel := releaseServer(t, "v0.8.0", "nitter-0.8.0-linux-amd64.tar.gz", archive, false)
	withAssetPrefix(t, srv)

	target := filepath.Join(t.TempDir(), "nitter")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	var replacedDst string
	var stagedContent []byte
	inst := NewInstaller(InstallOptions{
		GOOS: "linux", GOARCH: "amd64",
		ExecutablePath: func() (string, error) { return target, nil },
		// Read the staged file inside the stub: Install removes it as soon as
		// it returns, so the content is only observable here.
		Replace: func(src, dst string) error {
			replacedDst = dst
			data, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			stagedContent = data
			return nil
		},
		VerifyVersion: func(string) func(string) error { return func(string) error { return nil } },
	})
	if err := inst.Install(context.Background(), rel); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if replacedDst != target {
		t.Errorf("replaced dst = %q, want %q", replacedDst, target)
	}
	if string(stagedContent) != string(payload) {
		t.Errorf("staged content = %q, want %q", stagedContent, payload)
	}
	// The target directory holds only the (stub-replaced) target: no staging
	// leftovers survive a successful install.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("staging leftovers after a successful install: %v", names)
	}
}

// The central promise: a checksum mismatch must leave the target untouched.
func TestInstallerLeavesTargetUnchangedOnTamperedArchive(t *testing.T) {
	archive := tarGz(t, "nitter", []byte("good"))
	srv, rel := releaseServer(t, "v0.8.0", "nitter-0.8.0-linux-amd64.tar.gz", archive, true)
	withAssetPrefix(t, srv)

	target := filepath.Join(t.TempDir(), "nitter")
	original := []byte("old binary")
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	inst := NewInstaller(InstallOptions{
		GOOS: "linux", GOARCH: "amd64",
		ExecutablePath: func() (string, error) { return target, nil },
		Replace:        func(src, dst string) error { t.Fatal("replace must not be called"); return nil },
		VerifyVersion:  func(string) func(string) error { return func(string) error { return nil } },
	})
	if err := inst.Install(context.Background(), rel); err == nil {
		t.Fatal("want a checksum verification error")
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("target was modified: %q", after)
	}
	// No staging leftovers either.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("staging leftovers in target dir: %v", names)
	}
}

func TestInstallerRejectsVersionMismatch(t *testing.T) {
	archive := tarGz(t, "nitter", []byte("binary"))
	srv, rel := releaseServer(t, "v0.8.0", "nitter-0.8.0-linux-amd64.tar.gz", archive, false)
	withAssetPrefix(t, srv)

	target := filepath.Join(t.TempDir(), "nitter")
	original := []byte("old")
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	inst := NewInstaller(InstallOptions{
		GOOS: "linux", GOARCH: "amd64",
		ExecutablePath: func() (string, error) { return target, nil },
		Replace:        func(src, dst string) error { t.Fatal("replace must not be called"); return nil },
		VerifyVersion: func(string) func(string) error {
			return func(string) error { return fmt.Errorf("staged binary reports version %q", "0.0.1") }
		},
	})
	if err := inst.Install(context.Background(), rel); err == nil {
		t.Fatal("want a version verification error")
	}
	after, _ := os.ReadFile(target)
	if string(after) != string(original) {
		t.Errorf("target modified despite verification failure: %q", after)
	}
}

func TestInstallerRejectsUntrustedAssetURL(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nitter")
	inst := NewInstaller(InstallOptions{
		GOOS: "linux", GOARCH: "amd64",
		ExecutablePath: func() (string, error) { return target, nil },
		Replace:        func(src, dst string) error { t.Fatal("replace must not be called"); return nil },
		VerifyVersion:  func(string) func(string) error { return func(string) error { return nil } },
	})
	for _, rel := range []Release{
		{Tag: "v0.8.0", Version: "0.8.0", Assets: []Asset{
			{Name: "nitter-0.8.0-linux-amd64.tar.gz", URL: "https://evil.example/nitter-0.8.0-linux-amd64.tar.gz"},
			{Name: "checksums.txt", URL: "https://evil.example/checksums.txt"},
		}},
		{Tag: "v0.8.0", Version: "0.8.0", Assets: []Asset{
			{Name: "nitter-0.8.0-linux-amd64.tar.gz", URL: assetURLPrefix + "v0.8.0/nitter-0.8.0-linux-amd64.tar.gz"},
			{Name: "checksums.txt", URL: "https://evil.example/checksums.txt"},
		}},
	} {
		if err := inst.Install(context.Background(), rel); err == nil {
			t.Error("want an untrusted-URL error")
		}
	}
}

func TestInstallerRejectsMissingPlatformAsset(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nitter")
	inst := NewInstaller(InstallOptions{
		GOOS: "linux", GOARCH: "amd64",
		ExecutablePath: func() (string, error) { return target, nil },
		Replace:        func(src, dst string) error { t.Fatal("replace must not be called"); return nil },
		VerifyVersion:  func(string) func(string) error { return func(string) error { return nil } },
	})
	rel := Release{Tag: "v0.8.0", Version: "0.8.0", Assets: []Asset{
		{Name: "checksums.txt", URL: assetURLPrefix + "v0.8.0/checksums.txt"},
	}}
	if err := inst.Install(context.Background(), rel); err == nil {
		t.Error("want an error for a release without this platform's archive")
	}
}

// A release whose checksums.txt omits the platform archive must fail rather
// than install unverified bytes.
func TestInstallerRejectsMissingChecksumEntry(t *testing.T) {
	archive := tarGz(t, "nitter", []byte("binary"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "checksums.txt":
			_, _ = w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  something-else.tar.gz\n"))
		default:
			_, _ = w.Write(archive)
		}
	}))
	t.Cleanup(srv.Close)
	withAssetPrefix(t, srv)

	target := filepath.Join(t.TempDir(), "nitter")
	inst := NewInstaller(InstallOptions{
		GOOS: "linux", GOARCH: "amd64",
		ExecutablePath: func() (string, error) { return target, nil },
		Replace:        func(src, dst string) error { t.Fatal("replace must not be called"); return nil },
		VerifyVersion:  func(string) func(string) error { return func(string) error { return nil } },
	})
	rel := Release{Tag: "v0.8.0", Version: "0.8.0", Assets: []Asset{
		{Name: "checksums.txt", URL: srv.URL + "/v0.8.0/checksums.txt"},
		{Name: "nitter-0.8.0-linux-amd64.tar.gz", URL: srv.URL + "/v0.8.0/nitter-0.8.0-linux-amd64.tar.gz"},
	}}
	if err := inst.Install(context.Background(), rel); err == nil {
		t.Error("want an error when checksums.txt has no entry for the archive")
	}
}

func TestInstallerSurfacesHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	withAssetPrefix(t, srv)

	target := filepath.Join(t.TempDir(), "nitter")
	inst := NewInstaller(InstallOptions{
		GOOS: "linux", GOARCH: "amd64",
		ExecutablePath: func() (string, error) { return target, nil },
		Replace:        func(src, dst string) error { t.Fatal("replace must not be called"); return nil },
		VerifyVersion:  func(string) func(string) error { return func(string) error { return nil } },
	})
	rel := Release{Tag: "v0.8.0", Version: "0.8.0", Assets: []Asset{
		{Name: "checksums.txt", URL: srv.URL + "/v0.8.0/checksums.txt"},
		{Name: "nitter-0.8.0-linux-amd64.tar.gz", URL: srv.URL + "/v0.8.0/nitter-0.8.0-linux-amd64.tar.gz"},
	}}
	if err := inst.Install(context.Background(), rel); err == nil {
		t.Error("want an HTTP failure to surface")
	}
}

func TestValidateAssetURL(t *testing.T) {
	good := assetURLPrefix + "v0.8.0/checksums.txt"
	if err := ValidateAssetURL(good, "v0.8.0", "checksums.txt"); err != nil {
		t.Errorf("ValidateAssetURL(%q) = %v, want nil", good, err)
	}
	for _, bad := range []string{
		"https://github.com/other/repo/releases/download/v0.8.0/checksums.txt",
		assetURLPrefix + "v9.9.9/checksums.txt",
		assetURLPrefix + "v0.8.0/other.txt",
	} {
		if err := ValidateAssetURL(bad, "v0.8.0", "checksums.txt"); err == nil {
			t.Errorf("ValidateAssetURL(%q) = nil, want an error", bad)
		}
	}
}

func TestParseVersionOutput(t *testing.T) {
	cases := map[string]string{
		"nitter version 0.7.0\n":         "0.7.0",
		"nitter version 0.7.0":           "0.7.0",
		"  nitter version dev (a, b, c)": "dev",
		"":                               "",
		"unexpected output":              "",
		"version":                        "",
	}
	for in, want := range cases {
		if got := ParseVersionOutput(in); got != want {
			t.Errorf("ParseVersionOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

// VerifyStagedVersion must reject a binary reporting the wrong version, and
// accept one reporting the expected version (with the leading v tolerated).
func TestVerifyStagedVersionAgainstRealScript(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("the fake binary is a shell script; covered on Unix CI")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, []byte("#!/bin/sh\necho 'nitter version 0.8.0'\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\necho 'nitter version 0.1.0'\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := VerifyStagedVersion("v0.8.0")(good); err != nil {
		t.Errorf("expected version accepted: %v", err)
	}
	if err := VerifyStagedVersion("v0.8.0")(bad); err == nil {
		t.Error("wrong version must be rejected")
	}
}
