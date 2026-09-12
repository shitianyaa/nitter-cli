// Package text normalizes Nitter HTML fragments into plain text. It is the
// shared text-folding step of the Nitter protocol parsers — the RSS
// description bodies today, the user HTML timeline later — so every producer
// projects status bodies with the same rules.
//
// The single export, CleanHTML, is a faithful port of the user's reference
// Python plugin (astrbot_plugin_nitter_tweets): its clean_html_text /
// clean_text pipeline runs against real Nitter instances daily, so the port
// keeps the exact step order and semantics rather than reinventing them.
package text

import (
	"html"
	"regexp"
	"strings"
)

var (
	brRe    = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	preNLRe = regexp.MustCompile(`[ \t]+\n`)
	runsRe  = regexp.MustCompile(`\n{3,}`)
)

// CleanHTML folds a Nitter HTML fragment (a tweet body as embedded in RSS
// description CDATA or timeline HTML) into plain text, porting the plugin's
// clean_html_text semantics exactly:
//
//  1. <br> variants (any case, optional slash) become newlines;
//  2. all remaining tags are stripped;
//  3. HTML entities are unescaped;
//  4. spaces/tabs directly before a newline are removed;
//  5. runs of 3+ newlines collapse to two;
//  6. the result is trimmed.
//
// It is deliberately not a general HTML renderer: Nitter fragments are
// bounded and simple, and this pipeline is what the reference plugin has
// battle-tested in production.
func CleanHTML(raw string) string {
	s := brRe.ReplaceAllString(raw, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = preNLRe.ReplaceAllString(s, "\n")
	s = runsRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
