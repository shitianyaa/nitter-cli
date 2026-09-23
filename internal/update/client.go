package update

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// proxySchemes are the schemes the CLI accepts for --proxy — the same set the
// data-fetch wiring supports. Kept here so internal/update stays free of
// internal/cli imports.
var proxySchemes = map[string]bool{
	"http":    true,
	"https":   true,
	"socks5":  true,
	"socks5h": true,
}

// ValidateProxyScheme reports whether proxy is empty (disabled) or uses a
// supported scheme. The error names the scheme only: a proxy URL may embed
// credentials, which must never reach an error message.
func ValidateProxyScheme(proxy string) error {
	if proxy == "" {
		return nil
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return fmt.Errorf("proxy: not a valid URL")
	}
	if !proxySchemes[u.Scheme] {
		return fmt.Errorf("proxy: unsupported scheme %q (allowed: http, https, socks5, socks5h)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("proxy: missing host")
	}
	return nil
}

// NewHTTPClient builds the client used for both the release lookup and the
// asset downloads. An empty proxy disables proxying entirely (no environment
// lookup: the CLI resolves the effective proxy itself, so honoring
// environment variables here would silently override a deliberate choice).
func NewHTTPClient(proxy string, timeout time.Duration) (*http.Client, error) {
	if err := ValidateProxyScheme(proxy); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("proxy: not a valid URL")
		}
		client.Transport = &http.Transport{Proxy: http.ProxyURL(parsed)}
	}
	return client, nil
}
