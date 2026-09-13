package search_test

// Streams-level tests for `nitter search`: the command is built over an
// explicit invocation.Streams so the TTY state can be forced (cli.Run probes
// real files only) — this pins the TTY half of the M10 pipe default.

import (
	"context"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/commands/search"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
)

// TestSearchTTYDefaultStaysTextRows pins the TTY half of the M10 pipe
// default: on a TTY (OutIsTTY true) the no-flag default is STILL the human
// text rows — one tab-separated row per tweet, and an empty result gets the
// "(empty)" hint on stderr. Zero change for interactive users.
func TestSearchTTYDefaultStaysTextRows(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=%23artemis": {200, searchPage([]string{"301", "302"}, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{},
	}
	cmd := search.New(s)
	cmd.SetArgs([]string{"#artemis"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = %v, want nil", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want one text row per tweet:\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "301\t2026-07-20 14:11\t@nasa\tsearch body 301") {
		t.Errorf("row 0 = %q, want the ID/date/handle/text projection", lines[0])
	}
	if !strings.HasPrefix(lines[1], "302\t2026-07-20 14:11\t@nasa\tsearch body 302") {
		t.Errorf("row 1 = %q, want the ID/date/handle/text projection", lines[1])
	}
}

// TestSearchTTYEmptyResultPrintsHint pins the TTY-side empty result: the
// "(empty)" hint on stderr, nothing on stdout (the piped NDJSON default is
// fully silent; see search_test.go).
func TestSearchTTYEmptyResultPrintsHint(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/search?f=tweets&q=ghost": {200, searchPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{},
	}
	cmd := search.New(s)
	cmd.SetArgs([]string{"ghost"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = %v, want nil", err)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want nothing", out.String())
	}
	if strings.TrimSpace(errOut.String()) != "(empty)" {
		t.Errorf("stderr = %q, want the (empty) hint", errOut.String())
	}
}
