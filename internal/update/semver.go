// Package update implements the `twitter update --check` machinery: a strict
// semver comparison (semver.go) and a GitHub Releases lookup (release.go).
//
// It deliberately uses plain net/http instead of the internal/nitter
// protocol transport: the Nitter path needs TLS fingerprinting, pacing and
// classified retries against hostile frontends, while the update check is
// one anonymous GET against api.github.com — a small client with a timeout
// is the whole requirement. The dependency boundary (sdk does not import
// internal/nitter) is unaffected: nothing here touches the sdk.
package update

import (
	"strconv"
	"strings"
)

// Compare returns the semver 2.0.0 precedence of a versus b: -1 when a < b,
// 0 when a == b, 1 when a > b.
//
// Convention beyond the spec:
//   - A leading lowercase "v" is accepted on either side (the convention
//     GitHub release tags use).
//   - Build metadata (+...) never affects precedence, per spec §10.
//   - An INVALID version (see IsValid) sorts below every valid version: a
//     caller comparing an unparsable current version against a release
//     therefore reports "outdated", never a silent false negative. Two
//     invalid versions compare equal. Use IsValid to distinguish this case.
func Compare(a, b string) int {
	pa, aOK := parse(a)
	pb, bOK := parse(b)
	switch {
	case !aOK && !bOK:
		return 0
	case !aOK:
		return -1
	case !bOK:
		return 1
	}
	return compareParsed(pa, pb)
}

// IsValid reports whether v is a valid semantic version under this package's
// convention: strict semver 2.0.0 with an optional leading lowercase "v".
func IsValid(v string) bool {
	_, ok := parse(v)
	return ok
}

// version is a parsed semantic version. The build metadata is dropped at
// parse time: it never participates in precedence.
type version struct {
	core          [3]uint64
	identifiers   []string // prerelease identifiers, or nil
	hasPrerelease bool
}

// parse validates and decomposes one version string ("[v]MAJOR.MINOR.PATCH[-prerelease][+build]").
func parse(v string) (version, bool) {
	v = strings.TrimPrefix(v, "v")
	// Build metadata is cut before validation: it may carry leading zeros
	// and never affects precedence. Its shape is still validated (spec §10).
	if i := strings.IndexByte(v, '+'); i >= 0 {
		if !validDotted(v[i+1:], true) {
			return version{}, false
		}
		v = v[:i]
	}

	var pre string
	hasPrerelease := false
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
		hasPrerelease = true
	}

	parsed := version{}
	core := strings.Split(v, ".")
	if len(core) != 3 {
		return version{}, false
	}
	for i, part := range core {
		n, ok := parseNumeric(part)
		if !ok {
			// A '-' or any other non-digit inside the core lands here:
			// split on the FIRST '-' above already isolated the prerelease,
			// so a core like "1.0-0" is simply invalid.
			return version{}, false
		}
		parsed.core[i] = n
	}

	if hasPrerelease {
		// An explicit but empty prerelease ("1.0.0-") is invalid, not
		// "no prerelease" — hence the hasPrerelease flag rather than a
		// non-empty check on pre.
		if !validDotted(pre, false) {
			return version{}, false
		}
		parsed.identifiers = strings.Split(pre, ".")
		parsed.hasPrerelease = true
	}
	return parsed, true
}

// parseNumeric parses one core number: digits only, no sign, no leading
// zeros (a lone "0" is valid), and no uint64 overflow.
func parseNumeric(s string) (uint64, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// validDotted validates a dot-separated identifier list. Numeric identifiers
// must not carry leading zeros unless allowNumericLeadingZeros (build
// metadata allows them, prereleases do not — spec §11 vs §10).
func validDotted(s string, allowNumericLeadingZeros bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for i := 0; i < len(id); i++ {
			c := id[i]
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				numeric = false
			default:
				return false
			}
		}
		if numeric && !allowNumericLeadingZeros && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

// compareParsed implements spec §11 precedence over two parsed versions.
func compareParsed(a, b version) int {
	for i := 0; i < 3; i++ {
		switch {
		case a.core[i] < b.core[i]:
			return -1
		case a.core[i] > b.core[i]:
			return 1
		}
	}

	// A version without a prerelease outranks one with (§11.3).
	switch {
	case !a.hasPrerelease && !b.hasPrerelease:
		return 0
	case !a.hasPrerelease:
		return 1
	case !b.hasPrerelease:
		return -1
	}

	// Identifier-by-identifier: numeric compares numerically, numeric <
	// alphanumeric, alphanumerics compare in ASCII order, and a longer set
	// wins when every preceding identifier is equal (§11.4).
	for i := 0; i < len(a.identifiers) && i < len(b.identifiers); i++ {
		x, y := a.identifiers[i], b.identifiers[i]
		xNum, yNum := isNumericIdentifier(x), isNumericIdentifier(y)
		switch {
		case xNum && yNum:
			xn, _ := strconv.ParseUint(x, 10, 64)
			yn, _ := strconv.ParseUint(y, 10, 64)
			switch {
			case xn < yn:
				return -1
			case xn > yn:
				return 1
			}
		case xNum:
			return -1
		case yNum:
			return 1
		default:
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
		}
	}
	switch {
	case len(a.identifiers) < len(b.identifiers):
		return -1
	case len(a.identifiers) > len(b.identifiers):
		return 1
	}
	return 0
}

// isNumericIdentifier reports whether the (already shape-validated)
// prerelease identifier consists only of digits.
func isNumericIdentifier(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
