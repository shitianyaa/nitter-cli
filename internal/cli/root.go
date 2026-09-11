// Package cli is the composition root: it owns the command tree, shared
// streams and process exit semantics. Command packages must not import it.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shitianyaa/twitter-cli/internal/buildinfo"
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
)

// Streams bundles the three process streams; commands read/write nothing else.
type Streams struct {
	In          io.Reader
	Out, Err    io.Writer
	InIsTTY     bool
	OutIsTTY    bool
	CTX         context.Context
	RootOptions *invocation.RootOptions
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	streams := &Streams{In: stdin, Out: stdout, Err: stderr}
	streams.InIsTTY, streams.OutIsTTY = probeTTY(stdin, stdout)
	streams.RootOptions = &invocation.RootOptions{CTX: ctx}

	root := New(streams)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return invocation.ExitCode(err)
	}
	return 0
}

func New(s *Streams) *cobra.Command {
	root := &cobra.Command{
		Use:           "twitter",
		Short:         "Fetch public tweets from your own Nitter instances",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       versionLine(),
		// 无子命令时 cobra 会把位置参数当作根命令参数静默接受；这里显式拒绝，
		// 保持与有子命令后 legacyArgs 相同的 "unknown command" 语义。裸调用显示帮助。
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	root.SetVersionTemplate("twitter version {{.Version}}\n")
	root.SetFlagErrorFunc(invocation.WrapFlagError)
	return root
}

func versionLine() string {
	if buildinfo.IsDevelopment() {
		return fmt.Sprintf("dev (commit %s, built %s)", buildinfo.Commit, buildinfo.BuildDate)
	}
	return buildinfo.Version
}

func probeTTY(in io.Reader, out io.Writer) (inTTY, outTTY bool) {
	// Windows/Unix TTY 探测：golang.org/x/term.IsTerminal 对 *os.File 判定；
	// 非 *os.File（测试注入）一律按非 TTY 处理。
	f, ok := in.(*os.File)
	if ok {
		inTTY = term.IsTerminal(int(f.Fd()))
	}
	of, ok := out.(*os.File)
	if ok {
		outTTY = term.IsTerminal(int(of.Fd()))
	}
	return inTTY, outTTY
}
