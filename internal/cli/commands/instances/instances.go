// Package instances implements the `twitter instances` command family:
// diagnostics over the Nitter instances configured in config.toml. Data
// acquisition goes through the wiring layer (internal/cli/client) and sdk
// models only — per ruling R11 this package never imports
// internal/nitter/*.
package instances

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/twitter-cli/internal/cli/client"
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/twitter-cli/internal/cli/result"
	"github.com/shitianyaa/twitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// defaultProbeUser is the account the RSS/user-HTML probes fetch when the
// user does not choose one: a long-lived, high-volume public account, so the
// probe exercises a populated timeline without depending on anyone's own
// follow graph.
const defaultProbeUser = "NASA"

// New builds the `twitter instances` command family over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instances",
		Short: "Diagnose the configured Nitter instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Keep the repo-wide exit semantics: an unknown subcommand exits
			// 1 (root does the same for the top level).
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newTestCommand(s))
	return cmd
}

// newTestCommand builds `twitter instances test [URL]`.
//
// Exit codes (repo-wide semantics, task contract): completed probes exit 0
// even when every probe fails — the report is the product, diagnostics are
// not a failure. Usage problems (invalid proxy scheme, bad URL, empty
// --list-id, extra arguments) exit 2; wiring/transport build failures exit 1.
func newTestCommand(s *invocation.Streams) *cobra.Command {
	var (
		full   bool
		listID string
		user   string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "test [URL]",
		Short: "Probe instance capabilities: RSS, user timeline, search, list",
		Long: `Probe what a Nitter instance can serve and print one line per instance:

  url  rss  user_html  search  list  latency

Cells are "ok", "fail(<reason>)" (the HTTP status, or a short reason such as
"timeout" or "not rss"), or "-" for probes that did not run.

Without a URL every instance from [[instances]] in config.toml is probed in
order; with a URL only that instance is probed. The probes use the same
transport and settings (retry, pacing, proxy) as real fetches.

The RSS and user probes fetch <user>/rss and <user>; --user overrides the
account (default ` + defaultProbeUser + `, a stable public account). --full adds the
search probe (/search?f=tweets&q=twitter); --list-id adds a list probe
(/i/lists/<id>). Both default off: each extra probe costs the instance a
request.

--json prints the machine-readable report: one JSON object when exactly one
instance is probed, an array of objects otherwise. Exit status is 0 whenever
the probes completed, even if they all failed; 2 marks invalid input, 1
wiring failures.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return invocation.Usagef("usage: instances test [URL]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Input-contract validation before anything else runs.
			if cmd.Flags().Changed("list-id") && listID == "" {
				return invocation.Usagef("instances test: --list-id must not be empty")
			}

			// The root PersistentPreRunE has published the baseline config by
			// now; a bad value in it is a usage error (exit 2).
			cfg, err := client.LoadEffectiveSettings()
			if err != nil {
				return err
			}
			w, err := client.Build(s.RootOptions, cfg, time.Now)
			if err != nil {
				return client.AsUsageError(err)
			}

			ctx := s.CTX
			if ctx == nil {
				ctx = context.Background()
			}
			targets := []string{}
			if len(args) == 1 {
				targets = append(targets, args[0])
			} else {
				if len(w.Instances) == 0 {
					return invocation.Usagef("instances test: no instances configured; add an [[instances]] table to config.toml or pass a URL")
				}
				for _, in := range w.Instances {
					targets = append(targets, in.URL)
				}
			}

			tester := w.Tester()
			opts := client.TestOptions{User: user, IncludeSearch: full, ListID: listID}
			reports := make([]twitter.InstanceReport, 0, len(targets))
			for _, target := range targets {
				if err := ctx.Err(); err != nil {
					return err
				}
				report, err := tester.TestInstance(ctx, target, opts)
				if err != nil {
					// Invalid instance URLs are usage errors wherever they
					// came from; probe failures never reach this path.
					return client.AsUsageError(err)
				}
				reports = append(reports, report)
			}

			if asJSON {
				return writeJSON(s.Out, reports)
			}
			fmt.Fprintln(s.Out, result.InstanceReportHeader())
			for _, report := range reports {
				fmt.Fprintln(s.Out, result.InstanceReportLine(report))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "Also probe search (GET /search?f=tweets&q=twitter)")
	cmd.Flags().StringVar(&listID, "list-id", "", "Also probe the list with this ID (GET /i/lists/<id>)")
	cmd.Flags().StringVar(&user, "user", defaultProbeUser, "Account for the RSS and user-timeline probes")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the JSON report: one object for a single instance, an array for several")
	return cmd
}

// writeJSON prints the reports as one JSON document: a single object when
// exactly one instance was probed, an array otherwise. NDJSON is
// deliberately out of scope here — the watch pipeline (task 12) retrofits it
// per ruling R5/R7.
func writeJSON(out io.Writer, reports []twitter.InstanceReport) error {
	var v any = reports
	if len(reports) == 1 {
		v = reports[0]
	}
	b, err := jsonx.MarshalLine(v)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}
