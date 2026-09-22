package media_test

// Streams-level tests for `nitter media`: the command is built over an
// explicit invocation.Streams so the TTY state can be forced (cli.Run probes
// real files only) — this pins the TTY half of the M10 pipe default.

import (
	"context"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/commands/media"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
)

// TestMediaTTYDefaultStaysTextRows pins the TTY half of the M10 pipe
// default: on a TTY (OutIsTTY true) the no-flag default is STILL the human
// text rows — one tab-separated row per media entry — and a per-ref failure
// is reported as a stderr line, never as an envelope. Zero change for
// interactive users.
func TestMediaTTYDefaultStaysTextRows(t *testing.T) {
	home := tempHome(t)
	refB := "https://x.com/nasa/status/2070000000000000200"
	fake := newFakeBackend(t, map[string]answer{
		fxRoute100:                            {status: 200, body: fxVideo("https://video.twimg.com/x.mp4")},
		"/fx/nasa/status/2070000000000000200": {status: 500, body: "boom"},
	})
	overrideEndpoints(t, fake.addr+"/fx", fake.addr)
	writeConfig(t, home, fastTOML)

	var out, errOut strings.Builder
	s := &invocation.Streams{
		In:          strings.NewReader(""),
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{},
	}
	cmd := media.New(s)
	cmd.SetArgs([]string{ref100, refB, "--strategy", "fx"})
	// The exit-1 semantics are cli.Run's; at the command level the partial
	// failure surfaces as the returned summary error.
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "media completed with 1 of 2 refs failed") {
		t.Fatalf("Execute = %v, want the batch summary error", err)
	}
	if !strings.HasPrefix(out.String(), ref100+"\tfx\tvideo\thttps://video.twimg.com/x.mp4") {
		t.Errorf("stdout = %q, want the tab row", out.String())
	}
	if strings.Contains(out.String(), "nitter.pipeline/v1") {
		t.Errorf("stdout = %q, want no envelope on the TTY default", out.String())
	}
	if !strings.Contains(errOut.String(), "error: "+refB) {
		t.Errorf("stderr = %q, want the in-place error line", errOut.String())
	}
}
