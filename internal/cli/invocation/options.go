// Package invocation carries per-run plumbing shared by all commands.
package invocation

import "context"

// RootOptions holds per-run options threaded through the command tree.
type RootOptions struct {
	CTX      context.Context
	Proxy    string
	Instance string
}
