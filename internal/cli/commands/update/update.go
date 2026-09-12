// Package update implements the `nitter update` command: report how to
// update the binary, and (with --check) compare the installed version
// against the latest GitHub release. The MVP performs no self-install —
// `update/install` is future work — so the command is read-only in every
// mode and never touches the filesystem beyond the baseline config publish.
package update

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/buildinfo"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/internal/update"
)

// apiBaseOverride is the test seam for the GitHub API endpoint (internal
// tests point it at httptest; always empty in production builds).
var apiBaseOverride string

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
// Exit codes: the guidance form (without --check) and every successful
// --check exit 0 — outdatedness is a reported result, not a failure. A
// failed release check (network, GitHub error, no usable release) is a
// runtime failure (exit 1). Flag misuse (--json or --prerelease without
// --check, unknown flags) is a usage error (exit 2).
func New(s *invocation.Streams) *cobra.Command {
	var (
		checkFlag      bool
		prereleaseFlag bool
		asJSON         bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Show how to update, or check for a newer release with --check",
		Long: `Reports how to update the nitter binary and — with --check — compares the
installed version against the latest release on GitHub
(` + buildinfo.Repo + `):

  nitter update              How to update (package manager / manual download).
  nitter update --check      Compare the installed version with the latest
                              release; exits 0 and prints "up to date" or
                              "update available" with the release URL.
  nitter update --check --json
                              One JSON document: {current, latest, outdated,
                              prerelease, release_url}.

Flags: --prerelease admits prereleases into the "latest" selection.
--json and --prerelease are only valid together with --check (else usage
error). --check never modifies anything: the MVP performs no self-install;
update via your package manager or by downloading a release.

Development builds (compiled without version metadata) skip the release
check and say so: there is no release to compare "dev" against.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
				return runGuidance(s)
			}
			return runCheck(s, checkOptions{prerelease: prereleaseFlag, asJSON: asJSON})
		},
	}
	cmd.Flags().BoolVar(&checkFlag, "check", false,
		"Compare the installed version against the latest GitHub release")
	cmd.Flags().BoolVar(&prereleaseFlag, "prerelease", false,
		"Consider prereleases when picking the latest (only with --check)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Print one JSON document (only with --check)")
	return cmd
}

// runGuidance prints the update guidance (no self-install in the MVP) and
// exits 0.
func runGuidance(s *invocation.Streams) error {
	fmt.Fprintf(s.Out, `nitter update performs no self-install in this version.

To update:
  - reinstall with your package manager, or
  - download the latest release from https://github.com/%s/releases

To see whether a newer release exists: nitter update --check
`, buildinfo.Repo)
	return nil
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

	ctx := s.CTX
	if ctx == nil {
		ctx = context.Background()
	}
	latest, err := update.Check(ctx, buildinfo.Repo, buildinfo.Version, update.Options{
		IncludePrerelease: opts.prerelease,
		BaseURL:           apiBaseOverride,
	})
	if err != nil {
		if errors.Is(err, update.ErrNoRelease) {
			return fmt.Errorf("update: %w", err)
		}
		return err
	}

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
