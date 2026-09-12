package invocation

import (
	"context"
	"io"
)

// RootOptions holds per-run options threaded through the command tree.
type RootOptions struct {
	CTX      context.Context
	Proxy    string
	Instance string
}

// Streams bundles the three process streams; commands read/write nothing
// else. It lives in invocation (per-run plumbing shared by all commands) so
// command packages can take *Streams without importing the composition root
// (internal/cli), which owns the command tree.
type Streams struct {
	In          io.Reader
	Out, Err    io.Writer
	InIsTTY     bool
	OutIsTTY    bool
	CTX         context.Context
	RootOptions *RootOptions
}
