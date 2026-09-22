package media

import "strings"

// Third-party endpoint bases. The bases are package-level variables only so
// the media command's wiring-level tests can point the backends at httptest
// servers (the wiring composes the real resolver three packages away, so the
// seam must be reachable from there); production code leaves the overrides
// zero, and an empty override means the documented endpoint below. Never set
// outside _test.go files.
var EndpointOverrides struct {
	// Fx replaces the https://api.fxtwitter.com base of the fx strategy.
	Fx string
}

// baseURL returns the override (trailing slash trimmed) when set, else the
// documented default.
func baseURL(override, def string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return def
}
