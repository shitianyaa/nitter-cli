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

	"github.com/shitianyaa/nitter-cli/internal/buildinfo"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/config"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/download"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/get"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/instances"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/list"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/media"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/search"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/seen"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/update"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/user"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/watch"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/config/paths"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
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
		Use:           "nitter",
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
	root.SetVersionTemplate("nitter version {{.Version}}\n")
	root.SetFlagErrorFunc(invocation.WrapFlagError)
	root.PersistentFlags().StringVar(&s.RootOptions.Proxy, "proxy", "",
		"Proxy URL (else HTTPS_PROXY/ALL_PROXY/config)")
	root.PersistentFlags().StringVar(&s.RootOptions.Instance, "instance", "",
		"Nitter instance URL override for this invocation (else config)")
	// Baseline config publish on first real run: ensure ~/.nitter-cli/
	// config.toml exists (no-replace). cobra handles --version and
	// --help/-h before the PreRun stage, so they never get here; the
	// auto-generated help subcommand is skipped explicitly below. The
	// config mutation commands (set/unset) are skipped as well: they
	// manage the file themselves and must reject invalid input before any
	// file side effect (they seed the baseline themselves after
	// validation). Everything else — including the read-only config
	// path/get — publishes the baseline.
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "help" || isConfigMutationCommand(cmd) {
			return nil
		}
		p, err := paths.New()
		if err != nil {
			return err
		}
		return paths.EnsureDefaultConfigFile(p.ConfigFile, settings.DefaultConfigTOML)
	}
	root.AddCommand(config.New(s))
	root.AddCommand(instances.New(s))
	root.AddCommand(user.New(s))
	root.AddCommand(search.New(s))
	root.AddCommand(list.New(s))
	root.AddCommand(get.New(s))
	root.AddCommand(media.New(s))
	root.AddCommand(download.New(s))
	root.AddCommand(watch.New(s))
	root.AddCommand(seen.New(s))
	root.AddCommand(update.New(s))
	return root
}

// isConfigMutationCommand reports whether cmd is `config set`/`config unset`:
// they manage config.toml themselves and must reject invalid input before any
// file side effect, so the baseline publish skips them. The read-only config
// commands (path/get) are not exempt and publish like every other command.
func isConfigMutationCommand(cmd *cobra.Command) bool {
	if cmd.Name() != "set" && cmd.Name() != "unset" {
		return false
	}
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
