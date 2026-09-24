// Package invocation carries per-run plumbing shared by all commands.
package invocation

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// UsageError marks argument/flag/input-contract failures (exit 2).
// SDK, network and local I/O errors must never be wrapped in it.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func Usagef(format string, args ...any) *UsageError {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// WrapFlagError lets root.SetFlagErrorFunc turn flag-parsing errors into usage
// errors so they exit 2. It is reached only when flag PARSING failed — an
// unknown flag, an unparsable value ("--limit=abc"), or a missing argument —
// and every one of those is an argument-contract failure by the exit-code
// rules, so the wrapping is unconditional. A previous version wrapped only
// *pflag.NotExistError, which left "--limit=abc" exiting 1 while
// "--unknownflag" exited 2. Help and version never come through here (cobra
// resolves them after a successful parse), so they keep exiting 0.
func WrapFlagError(_ *cobra.Command, err error) error {
	return &UsageError{Err: err}
}

func ExitCode(err error) int {
	var ue *UsageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}
