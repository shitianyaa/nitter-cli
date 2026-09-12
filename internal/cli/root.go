// Package cli is the composition root: it owns the command tree, shared
// streams and process exit semantics. Command packages must not import it —
// they take *invocation.Streams, which lives next to RootOptions.
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
	"github.com/shitianyaa/twitter-cli/internal/cli/commands/config"
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/twitter-cli/internal/config/paths"
	"github.com/shitianyaa/twitter-cli/internal/config/settings"
)

// Streams is re-exported from invocation so Run/New keep their historical
// signature; command packages take *invocation.Streams and never import cli.
type Streams = invocation.Streams

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	streams := &Streams{In: stdin, Out: stdout, Err: stderr, CTX: ctx}
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
	root.PersistentFlags().StringVar(&s.RootOptions.Proxy, "proxy", "",
		"Proxy URL (else HTTPS_PROXY/ALL_PROXY/config)")
	root.PersistentFlags().StringVar(&s.RootOptions.Instance, "instance", "",
		"Nitter instance URL override for this invocation (else config)")
	// Baseline config publish on first real run: ensure ~/.twitter-cli/
	// config.toml exists (no-replace). cobra handles --version and
	// --help/-h before the PreRun stage, so they never get here; the
	// auto-generated help subcommand and the config subtree (which manages
	// the file itself and must reject invalid input before any file side
	// effect) are skipped explicitly below.
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "help" || inConfigSubtree(cmd) {
			return nil
		}
		p, err := paths.New()
		if err != nil {
			return err
		}
		return paths.EnsureDefaultConfigFile(p.ConfigFile, settings.DefaultConfigTOML)
	}
	root.AddCommand(config.New(s))
	return root
}

// inConfigSubtree reports whether cmd lives under the config command: config
// get treats a missing file as pure defaults, and config set/unset must not
// create the file when rejecting input, so the baseline publish skips them.
func inConfigSubtree(cmd *cobra.Command) bool {
	for p := cmd; p != nil; p = p.Parent() {
		if p.Name() == "config" {
			return true
		}
	}
	return false
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
