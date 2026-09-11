// Package invocation carries per-run plumbing shared by all commands.
package invocation

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// UsageError marks argument/flag/input-contract failures (exit 2).
// SDK, network and local I/O errors must never be wrapped in it.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func Usagef(format string, args ...any) *UsageError {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// WrapFlagError lets root.SetFlagErrorFunc turn unknown-flag errors into
// usage errors so they exit 2.
func WrapFlagError(cmd *cobra.Command, err error) error {
	var nef *pflag.NotExistError
	if errors.As(err, &nef) {
		return &UsageError{Err: err}
	}
	return err
}

func ExitCode(err error) int {
	var ue *UsageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}
