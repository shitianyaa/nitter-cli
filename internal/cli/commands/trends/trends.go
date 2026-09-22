// Package trends implements the `nitter trends` command: fetch currently
// trending topics through the wiring layer and render them in the resolved output mode.
package trends

import (
	"context"
	"errors"
	"fmt"
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

// New builds the `nitter trends` command over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag int
		asJSON    bool
		asNDJSON  bool
	)
	cmd := &cobra.Command{
		Use:           "trends",
		Short:         "Fetch currently trending topics on Twitter/X",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Fetch currently trending topics on Twitter/X and print one row per trend:

  #  TREND TOPIC  CONTEXT  TWEETS

--limit caps the number of trends to display (default: 0, 0 = all).
--json prints a JSON array of trend objects ([] when empty).
--ndjson prints one nitter.pipeline/v1 envelope per trend (kind trend).
--json and --ndjson are mutually exclusive.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return invocation.Usagef("usage: nitter trends")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, limitFlag, asJSON, asNDJSON)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 0, "Maximum trends to display (default: 0, 0 = all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print a JSON array of trend objects")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per trend (kind trend)")
	return cmd
}

func run(cmd *cobra.Command, s *invocation.Streams, limitFlag int, asJSON, asNDJSON bool) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if limitFlag < 0 {
		return invocation.Usagef("trends: --limit must be >= 0")
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

	trends, err := w.Trends().Trends(ctx)
	if err != nil {
		return client.AsUsageError(err)
	}

	if limitFlag > 0 && len(trends) > limitFlag {
		trends = trends[:limitFlag]
	}

	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		for _, t := range trends {
			env := pipeline.Envelope{
				Schema: pipeline.Schema,
				Kind:   pipeline.KindTrend,
				ID:     t.Name,
				Data:   t,
				Meta: &pipeline.Meta{
					Source:    "trends",
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
		if trends == nil {
			trends = []nitter.Trend{}
		}
		b, err := jsonx.MarshalLine(trends)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	default:
		rows := result.TrendRows(trends)
		if len(rows) == 0 {
			fmt.Fprintln(s.Err, "(empty)")
			return nil
		}
		fmt.Fprintln(s.Out, result.TrendTableHeader())
		for _, r := range rows {
			fmt.Fprintln(s.Out, r.Line())
		}
		return nil
	}
}
