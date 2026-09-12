package user_test

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/cli/commands/user"
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
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
