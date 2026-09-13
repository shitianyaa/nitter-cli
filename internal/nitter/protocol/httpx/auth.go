// Host-scoped basic auth: the credential policy of the httpx transport.
//
// The wiring (internal/cli/client.Build) maps configured instance
// credentials into Options.BasicAuth, keyed by URL host. The transport
// stamps the Authorization header onto requests whose own URL host has a
// complete credential pair configured — on every request path (buffered
// send, streaming download), on every attempt.
//
// Leak safety is structural, not call-site discipline: the decision reads
// the REQUEST's own URL host inside the transport, so a credential can only
// ever ride a request addressed to its own configured host. The third-party
// media endpoints (fxtwitter/vxtwitter/syndication/xdown/twimg) share this
// transport and are structurally unreachable by instance credentials — the
// media package gains nothing and changes nothing.
//
// Redaction: the header value exists only on the request; it never enters
// any error, log or response surface (sdk redaction contract — errors echo
// status codes and sanitized transport errors only).

package httpx

import (
	"encoding/base64"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

// BasicCredentials is one host's basic-auth pair.
type BasicCredentials struct {
	Username string
	Password string
}

// complete reports whether both halves are set. Basic auth is sent only for
// COMPLETE pairs: a username without a password (or vice versa) is treated
// as unconfigured rather than sent with an empty credential half — a
// username-only entry must never become "user:" on the wire.
func (c BasicCredentials) complete() bool { return c.Username != "" && c.Password != "" }

// normalizeBasicAuth copies the configured table into the client's lookup
// map: hosts are lowercased (URL host comparison is case-insensitive) and
// incomplete pairs are dropped, so the send path checks one condition less.
// An empty result returns nil (no policy configured).
func normalizeBasicAuth(in map[string]BasicCredentials) map[string]BasicCredentials {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]BasicCredentials, len(in))
	for host, creds := range in {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || !creds.complete() {
			continue
		}
		out[host] = creds
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyBasicAuth stamps the Authorization header onto req when req's URL
// host has a complete credential pair configured. Callers' headers are
// applied first and the policy decides last for a credentialed host: the
// host policy always wins, deterministically.
func (c *Client) applyBasicAuth(req *fhttp.Request) {
	if c.basicAuth == nil || req.URL == nil {
		return
	}
	creds, ok := c.basicAuth[strings.ToLower(req.URL.Host)]
	if !ok {
		return
	}
	req.Header.Set("Authorization",
		"Basic "+base64.StdEncoding.EncodeToString([]byte(creds.Username+":"+creds.Password)))
}
