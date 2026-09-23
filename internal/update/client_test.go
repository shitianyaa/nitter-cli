package update

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestValidateProxyScheme(t *testing.T) {
	for _, ok := range []string{
		"",
		"http://127.0.0.1:7890",
		"https://proxy.example:8080",
		"socks5://127.0.0.1:1080",
		"socks5h://127.0.0.1:10808",
	} {
		if err := ValidateProxyScheme(ok); err != nil {
			t.Errorf("ValidateProxyScheme(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"ftp://x", "127.0.0.1:1080", "socks4://x", "http://"} {
		if err := ValidateProxyScheme(bad); err == nil {
			t.Errorf("ValidateProxyScheme(%q) = nil, want an error", bad)
		}
	}
}

// A proxy URL may embed credentials; the error must name the scheme only.
func TestValidateProxySchemeDoesNotEchoCredentials(t *testing.T) {
	err := ValidateProxyScheme("ftp://user:secret@host:1")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "user") {
		t.Errorf("error leaked credentials: %q", err)
	}
}

func TestNewHTTPClientAppliesProxy(t *testing.T) {
	c, err := NewHTTPClient("socks5h://127.0.0.1:10808", time.Second)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	if c.Timeout != time.Second {
		t.Errorf("timeout = %v, want 1s", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("transport without a proxy func: %#v", c.Transport)
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("proxy func: %v", err)
	}
	if u == nil || u.Port() != "10808" {
		t.Errorf("proxy = %v, want port 10808", u)
	}
}

// An empty proxy must disable proxying outright: the CLI resolves the
// effective proxy itself and never relies on the environment.
func TestNewHTTPClientWithoutProxy(t *testing.T) {
	c, err := NewHTTPClient("", time.Second)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	if c.Timeout != time.Second {
		t.Errorf("timeout = %v, want 1s", c.Timeout)
	}
	if c.Transport != nil {
		t.Errorf("transport = %#v, want nil (no proxy)", c.Transport)
	}
}

func TestNewHTTPClientRejectsBadProxy(t *testing.T) {
	if _, err := NewHTTPClient("ftp://nope", time.Second); err == nil {
		t.Error("want an error for an unsupported proxy scheme")
	}
}

// The installer needs the release's asset list; Check must surface it.
func TestCheckParsesAssets(t *testing.T) {
	record := func(tag, name string, assets string) string {
		return fmt.Sprintf(
			`{"tag_name":%q,"name":%q,"html_url":"https://example.com/%s","draft":false,"prerelease":false,"published_at":"2026-09-01T00:00:00Z","assets":%s}`,
			tag, name, tag, assets)
	}
	assets := `[
		{"name":"checksums.txt","browser_download_url":"https://github.com/owner/repo/releases/download/v0.9.0/checksums.txt"},
		{"name":"nitter-0.9.0-linux-amd64.tar.gz","browser_download_url":"https://github.com/owner/repo/releases/download/v0.9.0/nitter-0.9.0-linux-amd64.tar.gz"}
	]`
	srv, _ := fakeAPI(t, "["+record("v0.9.0", "nine", assets)+"]", http.StatusOK)

	got, err := Check(context.Background(), "owner/repo", "0.8.0", Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(got.Assets) != 2 {
		t.Fatalf("Assets = %+v, want 2 entries", got.Assets)
	}
	if got.Assets[0].Name != "checksums.txt" {
		t.Errorf("Assets[0].Name = %q, want checksums.txt", got.Assets[0].Name)
	}
	if got.Assets[1].URL != "https://github.com/owner/repo/releases/download/v0.9.0/nitter-0.9.0-linux-amd64.tar.gz" {
		t.Errorf("Assets[1].URL = %q", got.Assets[1].URL)
	}
}

// A release with no assets must parse cleanly (nil/empty slice), not error:
// the installer reports the missing platform asset itself.
func TestCheckWithoutAssets(t *testing.T) {
	body := "[" + releaseJSON("v0.9.0", "nine", "https://example.com/v0.9.0", false, false) + "]"
	srv, _ := fakeAPI(t, body, http.StatusOK)
	got, err := Check(context.Background(), "owner/repo", "0.8.0", Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(got.Assets) != 0 {
		t.Errorf("Assets = %+v, want none", got.Assets)
	}
}
