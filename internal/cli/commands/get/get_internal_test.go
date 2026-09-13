package get_test

// Streams-level tests for `nitter get`: the command is built over an
// explicit invocation.Streams so the TTY state can be forced (cli.Run probes
// real files only) — this pins the TTY half of the M10 pipe default.

import (
	"context"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/commands/get"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
)

// TestGetTTYDefaultStaysTextRow pins the TTY half of the M10 pipe default:
// on a TTY (OutIsTTY true) the no-flag default is STILL the human text row —
// one tab-separated line. Zero change for interactive users.
func TestGetTTYDefaultStaysTextRow(t *testing.T) {
	home := tempHome(t)
	fake := newFake(t, map[string]answer{
		"/status/101": {200, statusPage("nasa", "101")},
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
	cmd := get.New(s)
	cmd.SetArgs([]string{"101"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = %v, want nil", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "101\t2026-07-20 14:11\t@nasa\tget body 101") {
		t.Fatalf("output = %q, want the ID/date/handle/text projection", got)
	}
}
