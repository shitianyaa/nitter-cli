package watch_test

import (
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/watch"
)

// ParseSource accepts the three MVP source shapes (and only those), trims
// surrounding whitespace and splits exactly once at the first colon — the
// ref may itself contain colons (e.g. the search phrase "from:NASA").
func TestParseSourceValidForms(t *testing.T) {
	cases := []struct {
		in   string
		want watch.Source
	}{
		{"user:NASA", watch.Source{Kind: "user", Ref: "NASA"}},
		{"tag:%23AI", watch.Source{Kind: "tag", Ref: "%23AI"}},
		{"tag:#AI", watch.Source{Kind: "tag", Ref: "#AI"}},
		{"list:12345", watch.Source{Kind: "list", Ref: "12345"}},
		// Surrounding whitespace is trimmed.
		{"  user:NASA\t", watch.Source{Kind: "user", Ref: "NASA"}},
		// Only the FIRST colon splits: the rest belongs to the ref.
		{"tag:from:NASA", watch.Source{Kind: "tag", Ref: "from:NASA"}},
	}
	for _, c := range cases {
		got, err := watch.ParseSource(c.in)
		if err != nil {
			t.Errorf("ParseSource(%q) = error %v, want %+v", c.in, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSource(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// Every other shape is an error: no colon, unknown kind, empty kind, empty
// ref, empty string.
func TestParseSourceErrors(t *testing.T) {
	for _, in := range []string{"NASA", "bogus:NASA", "user:", ":NASA", ":", "", "   "} {
		if got, err := watch.ParseSource(in); err == nil {
			t.Errorf("ParseSource(%q) = %+v, want an error", in, got)
		}
	}
}

// Key is the persistent seen state key "<kind>:<ref>" (seen.SourceKey); a
// parsed Source always has both parts, so Key is never empty.
func TestSourceKey(t *testing.T) {
	cases := []struct {
		src  watch.Source
		want string
	}{
		{watch.Source{Kind: "user", Ref: "NASA"}, "user:NASA"},
		{watch.Source{Kind: "tag", Ref: "%23AI"}, "tag:%23AI"},
		{watch.Source{Kind: "list", Ref: "12345"}, "list:12345"},
	}
	for _, c := range cases {
		if got := c.src.Key(); got != c.want {
			t.Errorf("(%+v).Key() = %q, want %q", c.src, got, c.want)
		}
	}
}
