package user_test

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/commands/user"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
)

// epipeWriter fails every write with syscall.EPIPE, the portable shape of a
// reader that hung up on a pipe.
type epipeWriter struct{}

func (epipeWriter) Write(p []byte) (int, error) { return 0, syscall.EPIPE }

// swallowWriter records writes; anything else is discarded.
type swallowWriter struct{ buf strings.Builder }

func (w *swallowWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

// TestRunEPIPEOnNDJSONExitsZero pins the sigpipe policy: when stdout's
// reader hangs up mid-NDJSON (head/tail on a pipe), the run is a success —
// no error surfaces (exit 0), instead of a spurious failure. Windows note:
// broken pipes there may surface as ERROR_BROKEN_PIPE rather than EPIPE, so
// the production check is best-effort; this test pins the portable path.
func TestRunEPIPEOnNDJSONExitsZero(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})

	s := &invocation.Streams{
		Out:         epipeWriter{},
		Err:         &swallowWriter{},
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{Instance: fake.addr},
	}
	cmd := user.New(s)
	cmd.SetArgs([]string{"NASA", "--ndjson"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = error %v, want nil (EPIPE on stdout is a clean stop)", err)
	}
}

// TestRunOtherWriteErrorsSurface keeps the policy honest: only EPIPE is
// absorbed; a different write failure is a real error.
func TestRunOtherWriteErrorsSurface(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome)

	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101")},
	})
	boom := errors.New("boom")

	s := &invocation.Streams{
		Out:         failingWriter{err: boom},
		Err:         &swallowWriter{},
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{Instance: fake.addr},
	}
	cmd := user.New(s)
	cmd.SetArgs([]string{"NASA", "--ndjson"})
	if err := cmd.Execute(); !errors.Is(err, boom) {
		t.Fatalf("Execute = %v, want the write failure to surface", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write(p []byte) (int, error) { return 0, w.err }

// TestUserTTYDefaultStaysTextRows pins the other half of the M10 pipe
// default: on a TTY (OutIsTTY true) the no-flag default is STILL the human
// text rows — one tab-separated row per tweet, and an empty timeline gets
// the "(empty)" hint on stderr. Zero change for interactive users.
func TestUserTTYDefaultStaysTextRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody("101", "102")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	var out, errOut swallowWriter
	s := &invocation.Streams{
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{Instance: fake.addr},
	}
	cmd := user.New(s)
	cmd.SetArgs([]string{"NASA"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = %v, want nil", err)
	}
	lines := strings.Split(strings.TrimRight(out.buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want one text row per tweet:\n%s", len(lines), out.buf.String())
	}
	if !strings.HasPrefix(lines[0], "101\t2026-07-05 09:09\t@NASA\trss body 101") {
		t.Errorf("row 0 = %q, want the ID/date/handle/text projection", lines[0])
	}
	if !strings.HasPrefix(lines[1], "102\t2026-07-05 09:09\t@NASA\trss body 102") {
		t.Errorf("row 1 = %q, want the ID/date/handle/text projection", lines[1])
	}
}

// TestUserTTYEmptyResultPrintsHint pins the TTY-side empty result: the
// "(empty)" hint on stderr, nothing on stdout (the piped NDJSON default is
// fully silent; see user_test.go).
func TestUserTTYEmptyResultPrintsHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	fake := newFake(t, map[string]answer{
		"/NASA/rss": {200, rssBody()},
		"/NASA":     {200, htmlPage(nil, "")},
	})
	writeConfig(t, home, fastTOML+"[[instances]]\nurl = \""+fake.addr+"\"\n")

	var out, errOut swallowWriter
	s := &invocation.Streams{
		Out:         &out,
		Err:         &errOut,
		OutIsTTY:    true,
		CTX:         context.Background(),
		RootOptions: &invocation.RootOptions{Instance: fake.addr},
	}
	cmd := user.New(s)
	cmd.SetArgs([]string{"NASA"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute = %v, want nil", err)
	}
	if out.buf.String() != "" {
		t.Errorf("stdout = %q, want nothing", out.buf.String())
	}
	if strings.TrimSpace(errOut.buf.String()) != "(empty)" {
		t.Errorf("stderr = %q, want the (empty) hint", errOut.buf.String())
	}
}
