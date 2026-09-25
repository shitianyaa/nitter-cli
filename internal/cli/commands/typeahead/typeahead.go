// Package typeahead implements the `nitter typeahead` command: suggest
// accounts matching a query through the wiring layer and render them in the
// resolved output mode.
package typeahead

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// New builds the `nitter typeahead <QUERY>` command over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag int
		asJSON    bool
		asNDJSON  bool
	)
	cmd := &cobra.Command{
		Use:           "typeahead <QUERY>",
		Short:         "Suggest accounts matching a query (completion, not search)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Suggest X accounts matching QUERY through the user-completion endpoint and
print one row per profile:

  @<handle>  <name>  <followers_count>  <bio>

This is a completion lookup, NOT search: no query operators, the upstream answers at most ~10-20 accounts and there is no pagination. Use ` + "`search`" + ` for real queries. Always queries the FxTwitter fast lane regardless of fetch_backend; --instance does not apply.
--limit caps the number of profiles to print (default: 20; must be >= 1;
upstream may answer fewer). --json prints a JSON array of profile objects
([] when empty). --ndjson prints one nitter.pipeline/v1 envelope per profile
(kind profile). --json and --ndjson are mutually exclusive.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter typeahead <QUERY>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args[0], limitFlag, asJSON, asNDJSON)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum profiles to print")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print a JSON array of profile objects")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per profile (kind profile)")
	return cmd
}

func run(cmd *cobra.Command, s *invocation.Streams, query string, limitFlag int, asJSON, asNDJSON bool) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return invocation.Usagef("typeahead: query cannot be empty")
	}
	if limitFlag < 1 {
		return invocation.Usagef("typeahead: --limit must be >= 1 (there is no unlimited value)")
	}

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

	profiles, err := w.Typeahead().Typeahead(ctx, q, limitFlag)
	if err != nil {
		return client.AsUsageError(err)
	}

	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		for _, p := range profiles {
			env := pipeline.Envelope{
				Schema: pipeline.Schema,
				Kind:   pipeline.KindProfile,
				ID:     p.Handle,
				Data:   p,
				Meta: &pipeline.Meta{
					Source:    "typeahead:" + query,
					Instance:  "FxTwitter",
					FetchedAt: fetchedAt,
				},
			}
			if err := pipeline.WriteEnvelope(s.Out, env); err != nil {
				if errors.Is(err, syscall.EPIPE) {
					return nil
				}
				return err
			}
		}
		return nil
	case pipeline.ModeJSON:
		if profiles == nil {
			profiles = []nitter.Profile{}
		}
		b, err := jsonx.MarshalLine(profiles)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	default:
		rows := result.ProfileRows(profiles)
		if len(rows) == 0 {
			fmt.Fprintln(s.Err, "(empty)")
			return nil
		}
		for _, r := range rows {
			fmt.Fprintln(s.Out, r.Line())
		}
		return nil
	}
}
