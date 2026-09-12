package update

import "testing"

// Compare implements semver 2.0.0 precedence with an optional lowercase v
// prefix. The table pins the spec's precedence rules (§11) plus the repo's
// own conventions: build metadata is ignored, an invalid version sorts below
// every valid one (so a caller that skips IsValid sees "outdated", never a
// silent false negative), and two invalid versions compare equal.
func TestCompareTable(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		// Equalities.
		{"identical", "1.0.0", "1.0.0", 0},
		{"v prefix optional", "v1.0.0", "1.0.0", 0},
		{"build metadata ignored", "1.0.0+build.1", "1.0.0+build.2", 0},
		{"build metadata vs none", "1.0.0+abc", "1.0.0", 0},

		// Core precedence.
		{"patch bump", "1.2.3", "1.2.4", -1},
		{"minor beats patch", "1.9.0", "1.10.0", -1},
		{"major beats all", "9.99.99", "10.0.0", -1},
		{"reversed", "2.0.0", "1.0.0", 1},

		// Prerelease: stable wins over any prerelease of the same core.
		{"stable beats prerelease", "1.0.0", "1.0.0-alpha", 1},
		{"prerelease loses to stable", "1.0.0-rc.1", "1.0.0", -1},
		{"long prerelease still loses", "1.0.0-alpha.1.beta.2", "1.0.0", -1},

		// Prerelease identifier rules (spec §11.4).
		{"alpha < beta (ASCII)", "1.0.0-alpha", "1.0.0-beta", -1},
		{"longer set wins when prefix-equal", "1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"numeric < alphanumeric", "1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"numeric compare, not lexicographic", "1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"multi-identifier left to right", "1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"alphanumeric ASCII order", "1.0.0-alpha", "1.0.0-beta.2", -1},
		{"numeric identifier beats fewer-equal set", "1.0.0-1", "1.0.0-a", -1},
		{"prerelease of different patch", "1.0.1-alpha", "1.0.0", 1},

		// Invalid inputs: below everything, deterministically.
		{"invalid current is always outdated", "0.1", "1.0.0", -1},
		{"invalid is below valid regardless of numbers", "99.99", "1.0.0", -1},
		{"valid is above invalid", "1.0.0", "not-a-version", 1},
		{"two invalids compare equal", "1.0", "x.y.z", 0},
		{"empty is invalid", "", "1.0.0", -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Compare(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
			// Antisymmetry: flipping the arguments must flip the sign.
			if flipped := Compare(tc.b, tc.a); flipped != -tc.want {
				t.Fatalf("Compare(%q, %q) = %d, want %d (antisymmetry)", tc.b, tc.a, flipped, -tc.want)
			}
		})
	}
}

// The tag pool a release checker must accept; anything outside is skipped
// client-side (a hostile or odd tag must not crash or win a comparison).
func TestIsValidTable(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"1.0.0", true},
		{"v1.0.0", true},
		{"v0.1.0", true},
		{"1.0.0-alpha.1", true},
		{"1.0.0-0a", true},      // alphanumeric identifiers may start with 0
		{"1.0.0-alpha-1", true}, // hyphens allowed in identifiers
		{"1.0.0+build.5", true}, // build metadata, leading zeros allowed
		{"1.0.0-rc.1+build.5", true},
		{"1.0.0-01", false}, // numeric identifier with leading zero
		{"01.0.0", false},   // core with leading zero
		{"1.0", false},      // incomplete core
		{"1", false},        // incomplete core
		{"1.0.0.0", false},  // overlong core
		{"1.0.0-", false},   // empty prerelease identifier
		{"1.0.0-alpha..1", false},
		{"1.0.0+", false},    // empty build metadata
		{"1.0.0-a_b", false}, // underscore not allowed
		{"V1.0.0", false},    // only the lowercase v prefix is accepted
		{"", false},
		{"release", false},
		{"1.0.0-alpha.01", false},
		{"99999999999999999999.0.0", false}, // must not overflow silently
	}
	for _, tc := range cases {
		if got := IsValid(tc.v); got != tc.want {
			t.Errorf("IsValid(%q) = %t, want %t", tc.v, got, tc.want)
		}
	}
}
