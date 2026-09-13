package instances_test

// Streams-level test for `nitter instances test`: the command is built over
// an explicit invocation.Streams so the TTY state can be forced (cli.Run
// probes real files only) — this pins the TTY half of the M10 pipe default:
// the human table with its header line and ok/fail(-)/"-" cells.

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/commands/instances"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
)

// TestInstancesTestTTYDefaultStaysHumanTable pins the TTY half of the M10
// pipe default: on a TTY (OutIsTTY true) the no-flag default is STILL the
// human table — header line, one tab-separated row per instance with
// ok/fail(<reason>)/"-" cells. Zero change for interactive users.
func TestInstancesTestTTYDefaultStaysHumanTable(t *testing.T) {
	home := tempHome(t)
	srv, _ := newFakeNitter(t, 404)
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+srv.URL+"\"\n")

	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{},
	}
	cmd := instances.New(s)
	cmd.SetArgs([]string{"test", "--full"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = %v, want nil", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want the header plus one row:\n%s", len(lines), out.String())
	}
	if lines[0] != "url\trss\tuser_html\tsearch\tlist\tlatency" {
		t.Errorf("header = %q, want the table header", lines[0])
	}
	cells := strings.Split(lines[1], "\t")
	if len(cells) != 6 {
		t.Fatalf("row = %q, want six tab-separated cells", lines[1])
	}
	if cells[0] != srv.URL {
		t.Errorf("url cell = %q, want %q", cells[0], srv.URL)
	}
	if cells[1] != "ok" || cells[2] != "ok" {
		t.Errorf("rss/user_html cells = %q/%q, want ok/ok", cells[1], cells[2])
	}
	if cells[3] != "fail(404)" {
		t.Errorf("search cell = %q, want fail(404)", cells[3])
	}
	if cells[4] != "-" {
		t.Errorf("list cell = %q, want - (probe disabled)", cells[4])
	}
	if !regexp.MustCompile(latencyCell).MatchString(cells[5]) {
		t.Errorf("latency cell = %q, want a duration like 42ms", cells[5])
	}
}
