// Package quotes implements the `nitter quotes` command: fetch quote tweets
// for a given status ID or URL through the wiring layer and render them in the resolved output mode.
package quotes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/internal/cli/tweetfilter"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// New builds the `nitter quotes <REF>` command over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag int
		asJSON    bool
		asNDJSON  bool
		filters   tweetfilter.Filters
	)
	cmd := &cobra.Command{
		Use:           "quotes <REF>",
		Short:         "Fetch quote tweets for a tweet status ID or URL",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Fetch quote tweets for a tweet status ID or URL:

  <ID>  <YYYY-MM-DD HH:MM>  @<handle>  <text>

--limit caps the number of quotes to fetch (default: 20; must be >= 1).
--media-only drops quotes that carry no media attachments.
--no-reposts drops reposted quotes.
--json prints a machine-readable document: one JSON object for a single quote, an array otherwise ([] when empty).
--ndjson prints one nitter.pipeline/v1 envelope per quote (kind tweet).
--json and --ndjson are mutually exclusive.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter quotes <REF>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args[0], limitFlag, asJSON, asNDJSON, filters)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum quotes to fetch")
	cmd.Flags().BoolVar(&filters.MediaOnly, "media-only", false, "Drop quotes that carry no media attachments")
	cmd.Flags().BoolVar(&filters.NoReposts, "no-reposts", false, "Drop pure reposts from the output")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON document")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per quote (kind tweet)")
	return cmd
}

func run(cmd *cobra.Command, s *invocation.Streams, ref string, limitFlag int, asJSON, asNDJSON bool, filters tweetfilter.Filters) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if limitFlag < 1 {
		return invocation.Usagef("quotes: --limit must be >= 1 (there is no unlimited value)")
	}

	statusID, _, err := client.ParseStatusRef(ref)
	if err != nil {
		return client.AsUsageError(err)
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

	tweets, _, err := w.Quotes().Quotes(ctx, statusID, limitFlag, "")
	if err != nil {
		return client.AsUsageError(err)
	}

	tweets = tweetfilter.Apply(tweets, filters)
	if limitFlag > 0 && len(tweets) > limitFlag {
		tweets = tweets[:limitFlag]
	}

	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		for _, tw := range tweets {
			env := pipeline.Envelope{
				Schema: pipeline.Schema,
				Kind:   pipeline.KindTweet,
				ID:     tw.ID,
				Data:   tw,
				Meta: &pipeline.Meta{
					Source:    "quotes:" + statusID,
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
		return writeJSON(s.Out, tweets)
	default:
		rows := result.TweetRows(tweets)
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

func writeJSON(out io.Writer, tweets []nitter.Tweet) error {
	if tweets == nil {
		tweets = []nitter.Tweet{}
	}
	var v any = tweets
	if len(tweets) == 1 {
		v = tweets[0]
	}
	b, err := jsonx.MarshalLine(v)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}
