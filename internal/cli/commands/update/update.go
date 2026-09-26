// Package update implements the `nitter update` command: check for a newer
// release and, with consent, install it by replacing the running binary.
// The install path verifies the archive against the release's checksums.txt
// and the staged binary's reported version before anything is written, so
// every failure before the replacement leaves the current installation
// untouched.
package update

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/buildinfo"
	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/internal/update"
)

// apiBaseOverride is the test seam for the GitHub API endpoint (internal
// tests point it at httptest; always empty in production builds).
var apiBaseOverride string

// osExecutable is a test seam for locating the running binary (internal
// tests point it at a temp file; production uses os.Executable).
var osExecutable = os.Executable

// networkTimeout bounds the release lookup, mirroring the client the update
// package would build on its own.
const networkTimeout = 15 * time.Second

// devBuildJSON is the --check --json document of a development build: no
// release metadata, nothing compared.
type devBuildJSON struct {
	Current          string `json:"current"`
	DevelopmentBuild bool   `json:"development_build"`
}

// checkJSON is the --check --json document of a release build. Every key is
// always present (no omitempty): consumers rely on key presence.
type checkJSON struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Outdated   bool   `json:"outdated"`
	Prerelease bool   `json:"prerelease"`
	ReleaseURL string `json:"release_url"`
}

// New builds the `nitter update` command over the shared streams.
//
// Exit codes: a successful check, an up-to-date result, a declined prompt and
// a development build all exit 0 — none of them is a failure. A failed
// release check (network, GitHub error, no usable release), any verification
// failure, a `go install` installation and an unwritable target directory are
// runtime failures (exit 1). Flag misuse (--json or --prerelease without
// --check, --confirm with --check, an invalid --proxy) is a usage error
// (exit 2).
func New(s *invocation.Streams) *cobra.Command {
	var (
		checkFlag      bool
		prereleaseFlag bool
		asJSON         bool
		confirmFlag    bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Install the latest release, or check for one with --check",
		Long: `Checks the latest release on GitHub (` + buildinfo.Repo + `) and, when a
newer one exists, offers to install it:

  nitter update              Check, then install after confirmation.
  nitter update --confirm    Install without asking (required when stdin is
                              not a terminal — the prompt is never sent to a
                              pipe, so scripts must opt in).
  nitter update --check      Compare versions only; never writes anything.
  nitter update --check --json
                              One JSON document: {current, latest, outdated,
                              prerelease, release_url}.

The installer downloads only this platform's archive, verifies its SHA-256
against the release's checksums.txt, checks the staged binary's version, and
only then replaces the executable. Any failure before that replacement leaves
the current installation unchanged; if the replacement itself fails, verify the
installed binary before retrying.

Flags: --prerelease admits prereleases into the "latest" selection (only with
--check). --json is only valid with --check. --proxy / config.proxy apply to
every request this command makes.

A binary installed with 'go install' is never replaced: the command reports
the matching install line instead, so a later install cannot silently undo
the update. Development builds (no version metadata) report and exit 0.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A leftover "<exe>.old" from a previous Windows update is removed
			// here: by now the process that held it has exited. Best-effort.
			_ = update.CleanupPendingUpdate()
			// --json / --prerelease are modifiers of --check; alone they are
			// silently-ignored-flag bugs, so they fail as usage errors
			// (javdb rule: a flag with no effect in this mode is an error).
			if !checkFlag {
				if asJSON {
					return invocation.Usagef("update: --json is only valid together with --check")
				}
				if prereleaseFlag {
					return invocation.Usagef("update: --prerelease is only valid together with --check")
				}
				return runInstall(cmd, s, confirmFlag)
			}
			if confirmFlag {
				return invocation.Usagef("update: --confirm only applies when installing (not with --check)")
			}
			return runCheck(s, checkOptions{prerelease: prereleaseFlag, asJSON: asJSON})
		},
	}
	cmd.Flags().BoolVar(&checkFlag, "check", false,
		"Compare the installed version against the latest GitHub release without installing")
	cmd.Flags().BoolVar(&prereleaseFlag, "prerelease", false,
		"Consider prereleases when picking the latest (only with --check)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Print one JSON document (only with --check)")
	cmd.Flags().BoolVar(&confirmFlag, "confirm", false,
		"Install without asking (required when stdin is not a terminal)")
	return cmd
}

// checkOptions carries the parsed flags of one --check run.
type checkOptions struct {
	prerelease bool
	asJSON     bool
}

// runCheck compares the installed version against the latest release. A dev
// build skips the query entirely (nothing to compare "dev" against) and
// exits 0; release builds query the GitHub API — failures are runtime
// errors (exit 1), outdatedness is a reported result (exit 0).
func runCheck(s *invocation.Streams, opts checkOptions) error {
	if buildinfo.IsDevelopment() {
		if opts.asJSON {
			b, err := jsonx.MarshalLine(devBuildJSON{Current: buildinfo.Version, DevelopmentBuild: true})
			if err != nil {
				return err
			}
			if _, err := s.Out.Write(b); err != nil {
				return err
			}
			return nil
		}
		fmt.Fprintf(s.Out, "development build (version %q) — skipping the release check\n", buildinfo.Version)
		fmt.Fprintln(s.Out, "install a release build to enable update checks")
		return nil
	}

	ctx, latest, err := lookupRelease(s, opts.prerelease)
	if err != nil {
		return err
	}
	_ = ctx

	outdated := update.Compare(latest.Tag, buildinfo.Version) > 0
	if opts.asJSON {
		b, err := jsonx.MarshalLine(checkJSON{
			Current:    buildinfo.Version,
			Latest:     latest.Version,
			Outdated:   outdated,
			Prerelease: latest.Prerelease,
			ReleaseURL: latest.URL,
		})
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	}

	fmt.Fprintf(s.Out, "current version: %s\n", buildinfo.Version)
	fmt.Fprintf(s.Out, "latest release:  %s\n", latest.Version)
	if outdated {
		fmt.Fprintf(s.Out, "update available: %s (%s)\n", latest.Version, latest.URL)
	} else {
		fmt.Fprintln(s.Out, "up to date")
	}
	return nil
}

// runInstall checks for a newer release and, with consent, replaces the
// running binary. Consent is either --confirm or an interactive "y" on a
// TTY; a non-TTY without --confirm reports and exits 0 WITHOUT reading
// stdin, because blocking a pipe would be worse than not installing.
func runInstall(cmd *cobra.Command, s *invocation.Streams, confirm bool) error {
	if buildinfo.IsDevelopment() {
		fmt.Fprintf(s.Out, "development build (version %q) — self-update is unavailable\n", buildinfo.Version)
		fmt.Fprintln(s.Out, "install a release build to enable updates")
		return nil
	}

	ctx, latest, err := lookupRelease(s, false)
	if err != nil {
		return err
	}
	outdated := update.Compare(latest.Tag, buildinfo.Version) > 0

	fmt.Fprintf(s.Out, "current version: %s\n", buildinfo.Version)
	fmt.Fprintf(s.Out, "latest release:  %s\n", latest.Version)
	if !outdated {
		fmt.Fprintln(s.Out, "up to date")
		return nil
	}
	fmt.Fprintf(s.Out, "update available: %s (%s)\n", latest.Version, latest.URL)

	executable, err := osExecutable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	source, err := update.DetectSource(executable, buildinfo.Version)
	if err != nil {
		return err
	}
	switch source {
	case update.SourceGoInstall:
		return fmt.Errorf("this binary was installed with 'go install'; update it with:\n  go install github.com/%s/cmd/nitter@%s",
			buildinfo.Repo, latest.Tag)
	case update.SourceUnknown:
		return fmt.Errorf("cannot determine how this binary was installed; download %s manually", latest.URL)
	}

	if !confirm {
		allowed, err := askToInstall(s)
		if err != nil {
			return err
		}
		if !allowed {
			fmt.Fprintln(s.Out, "not installed")
			return nil
		}
	}

	proxy, err := resolveProxy(s)
	if err != nil {
		return err
	}
	httpClient, err := update.NewHTTPClient(proxy, update.DownloadTimeout())
	if err != nil {
		return invocation.Usagef("update: %v", err)
	}
	inst := update.NewInstaller(update.InstallOptions{
		HTTPClient:      httpClient,
		ExpectedVersion: latest.Version,
	})
	fmt.Fprintf(s.Out, "downloading %s\n", update.ArchiveName(latest.Version, runtime.GOOS, runtime.GOARCH))
	if err := inst.Install(ctx, latest); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "installed %s → %s\n", latest.Version, executable)
	return nil
}

// askToInstall prompts on a TTY. A non-TTY returns (false, nil) without
// reading: the caller reports it and exits 0.
func askToInstall(s *invocation.Streams) (bool, error) {
	if !s.InIsTTY {
		fmt.Fprintln(s.Out, "not installed: stdin is not a terminal — re-run with --confirm to install")
		return false, nil
	}
	fmt.Fprint(s.Out, "install now? [y/N] ")
	sc := bufio.NewScanner(s.In)
	sc.Scan()
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// lookupRelease resolves the effective proxy and queries the latest release.
func lookupRelease(s *invocation.Streams, includePrerelease bool) (context.Context, update.Release, error) {
	proxy, err := resolveProxy(s)
	if err != nil {
		return nil, update.Release{}, err
	}
	httpClient, err := update.NewHTTPClient(proxy, networkTimeout)
	if err != nil {
		return nil, update.Release{}, invocation.Usagef("update: %v", err)
	}
	ctx := s.CTX
	if ctx == nil {
		ctx = context.Background()
	}
	latest, err := update.Check(ctx, buildinfo.Repo, buildinfo.Version, update.Options{
		IncludePrerelease: includePrerelease,
		BaseURL:           apiBaseOverride,
		HTTPClient:        httpClient,
	})
	if err != nil {
		if errors.Is(err, update.ErrNoRelease) {
			return nil, update.Release{}, fmt.Errorf("update: %w", err)
		}
		return nil, update.Release{}, err
	}
	return ctx, latest, nil
}

// resolveProxy returns the effective proxy: --proxy wins over config.proxy,
// the same precedence the data-fetch wiring uses.
func resolveProxy(s *invocation.Streams) (string, error) {
	cfg, err := client.LoadEffectiveSettings()
	if err != nil {
		return "", err
	}
	proxy := cfg.Proxy
	if s.RootOptions != nil && s.RootOptions.Proxy != "" {
		proxy = s.RootOptions.Proxy
	}
	if err := update.ValidateProxyScheme(proxy); err != nil {
		return "", invocation.Usagef("update: %v", err)
	}
	return proxy, nil
}
