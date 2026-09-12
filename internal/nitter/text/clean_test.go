package text

import "testing"

// CleanHTML is the port of the reference plugin's clean_html_text; these
// cases pin each pipeline step in the exact order the plugin uses.
func TestCleanHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"br variants fold to newlines", `a<br>b<BR/>c<br />d`, "a\nb\nc\nd"},
		{"tags stripped", `<div class="tweet-content">hello <a href="https://x.com">world</a></div>`, "hello world"},
		{"entities unescaped", `at &amp; t &lt;3 &quot;q&quot;`, `at & t <3 "q"`},
		{"spaces before newline removed", "line one \t\nline two", "line one\nline two"},
		{"runs of newlines collapse to two", "a\n\n\n\n\nb", "a\n\nb"},
		{"trimmed", "  \nplain words\n  ", "plain words"},
		// Step order matters: <br> folds first, so the produced newlines then
		// take part in the collapse step (3 br → 1 blank line, not 2).
		{"br runs collapse too", "a<br/><br/><br/>b", "a\n\nb"},
		// Only whitespace BEFORE a newline is removed; a space after a folded
		// newline survives, matching the plugin.
		{"space after newline kept", "one<br/> two", "one\n two"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanHTML(tc.in); got != tc.want {
				t.Errorf("CleanHTML(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
