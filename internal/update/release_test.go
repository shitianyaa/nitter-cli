package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// releaseJSON renders one GitHub Releases API record; the fake API in these
// tests serves arrays of them.
func releaseJSON(tag, name, url string, draft, prerelease bool) string {
	return fmt.Sprintf(`{"tag_name":%q,"name":%q,"html_url":%q,"draft":%t,"prerelease":%t,"published_at":"2026-09-01T00:00:00Z"}`,
		tag, name, url, draft, prerelease)
}

// fakeAPI starts an httptest server answering with the given raw JSON body
// and recording the request path.
func fakeAPI(t *testing.T, body string, status int) (*httptest.Server, *string) {
	t.Helper()
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &path
}

func TestCheckSelectsHighestStable(t *testing.T) {
	body := "[" + strings.Join([]string{
		releaseJSON("v0.1.0", "first", "https://example.com/v0.1.0", false, false),
		releaseJSON("v0.10.0", "tenth", "https://example.com/v0.10.0", false, false),
		releaseJSON("v0.2.0", "second", "https://example.com/v0.2.0", false, false),
		releaseJSON("v0.3.0", "draft", "https://example.com/v0.3.0", true, false),
		releaseJSON("release-2024", "bad tag", "https://example.com/bad", false, false),
	}, ",") + "]"
	srv, path := fakeAPI(t, body, http.StatusOK)

	got, err := Check(context.Background(), "owner/repo", "0.1.0", Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Check = error %v", err)
	}
	if got.Tag != "v0.10.0" {
		t.Errorf("Tag = %q, want v0.10.0 (numeric compare, drafts and invalid tags excluded)", got.Tag)
	}
	if got.Version != "0.10.0" {
		t.Errorf("Version = %q, want 0.10.0 (tag without v)", got.Version)
	}
	if got.URL != "https://example.com/v0.10.0" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Prerelease {
		t.Errorf("Prerelease = true, want false")
	}
	if *path != "/repos/owner/repo/releases?per_page=100" {
		t.Errorf("request URI = %q", *path)
	}
}

func TestCheckPrereleaseFlag(t *testing.T) {
	body := "[" + strings.Join([]string{
		releaseJSON("v0.2.0", "stable", "https://example.com/v0.2.0", false, false),
		releaseJSON("v0.3.0-rc.1", "rc", "https://example.com/v0.3.0-rc.1", false, true),
	}, ",") + "]"
	srv, _ := fakeAPI(t, body, http.StatusOK)

	got, err := Check(context.Background(), "owner/repo", "0.2.0", Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Check = error %v", err)
	}
	if got.Tag != "v0.2.0" {
		t.Errorf("without flag: Tag = %q, want v0.2.0", got.Tag)
	}

	got, err = Check(context.Background(), "owner/repo", "0.2.0", Options{BaseURL: srv.URL, IncludePrerelease: true})
	if err != nil {
		t.Fatalf("Check(prerelease) = error %v", err)
	}
	if got.Tag != "v0.3.0-rc.1" {
		t.Errorf("with flag: Tag = %q, want v0.3.0-rc.1", got.Tag)
	}
	if !got.Prerelease {
		t.Errorf("with flag: Prerelease = false, want true")
	}
}

func TestCheckNoRelease(t *testing.T) {
	srv, _ := fakeAPI(t, "[]", http.StatusOK)
	if _, err := Check(context.Background(), "owner/repo", "0.1.0", Options{BaseURL: srv.URL}); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("Check([]) = %v, want ErrNoRelease", err)
	}

	// Only drafts/invalid tags: equally no release.
	body := "[" + releaseJSON("v0.2.0", "draft", "https://example.com/x", true, false) + "]"
	srv2, _ := fakeAPI(t, body, http.StatusOK)
	if _, err := Check(context.Background(), "owner/repo", "0.1.0", Options{BaseURL: srv2.URL}); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("Check(draft only) = %v, want ErrNoRelease", err)
	}
}

func TestCheckHTTPStatusErrors(t *testing.T) {
	for _, status := range []int{403, 404, 500} {
		srv, _ := fakeAPI(t, `{"message":"secret detail"}`, status)
		_, err := Check(context.Background(), "owner/repo", "0.1.0", Options{BaseURL: srv.URL})
		if err == nil {
			t.Fatalf("Check(HTTP %d) = nil error", status)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
			t.Errorf("Check(HTTP %d) error %q does not name the status", status, err)
		}
		// Redaction: the response body must never be echoed into the error.
		if strings.Contains(err.Error(), "secret detail") {
			t.Errorf("Check(HTTP %d) error %q leaks the response body", status, err)
		}
	}
}

func TestCheckRejectsBadRepoShape(t *testing.T) {
	srv, path := fakeAPI(t, "[]", http.StatusOK)
	for _, repo := range []string{"", "owner", "owner/", "/repo", "a/b/c", "owner/repo?x=1", "owner/repo/../.."} {
		if _, err := Check(context.Background(), repo, "0.1.0", Options{BaseURL: srv.URL}); err == nil {
			t.Errorf("Check(%q) = nil error, want repo-shape rejection", repo)
		}
	}
	if *path != "" {
		t.Errorf("bad repo shapes must not reach the network, got request %q", *path)
	}
}

func TestCheckTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // connections refused from here on
	if _, err := Check(context.Background(), "owner/repo", "0.1.0", Options{BaseURL: srv.URL}); err == nil {
		t.Fatalf("Check(closed server) = nil error, want transport failure")
	}
}
