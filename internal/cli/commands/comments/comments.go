// Package comments implements the `nitter comments` command: fetch a status's
// conversation thread and replies through the wiring layer and render it in
// the resolved output mode.
package comments

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
	nitter "github.com/shitianyaa/nitter-cli/sdk"
)

// New builds the `nitter comments <STATUS_ID_OR_URL>` command over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag int
		sortFlag  string
		asJSON    bool
		asNDJSON  bool
	)
	cmd := &cobra.Command{
		Use:           "comments <STATUS_ID_OR_URL>",
		Short:         "Fetch comments and replies for a tweet",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Fetch conversation replies and comments for a tweet status ID or URL:

  <ID>  <YYYY-MM-DD HH:MM>  @<handle>  <text>
    <ID>  <YYYY-MM-DD HH:MM>  @<handle>  <reply_text>

--limit caps the number of replies to fetch (default: 20; must be >= 1).
--sort specifies reply ordering ("likes" or "recency", default: "likes").
--json prints the full sdk.Conversation object.
--ndjson prints one nitter.pipeline/v1 envelope per reply (kind tweet).
--json and --ndjson are mutually exclusive.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter comments <STATUS_ID_OR_URL>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args[0], limitFlag, sortFlag, asJSON, asNDJSON)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum replies to fetch")
	cmd.Flags().StringVar(&sortFlag, "sort", "likes", "Sort replies by \"likes\" or \"recency\" (default: likes)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print sdk.Conversation JSON object")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per reply (kind tweet)")
	return cmd
}

func run(cmd *cobra.Command, s *invocation.Streams, ref string, limitFlag int, sortFlag string, asJSON, asNDJSON bool) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if limitFlag < 1 {
		return invocation.Usagef("comments: --limit must be >= 1 (there is no unlimited value)")
	}
	if sortFlag != "likes" && sortFlag != "recency" {
		return invocation.Usagef("comments: invalid sort %q (allowed: likes, recency)", sortFlag)
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

	conv, err := w.Conversation().Conversation(ctx, statusID, sortFlag, "")
	if err != nil {
		return client.AsUsageError(err)
	}

	if limitFlag > 0 && len(conv.Replies) > limitFlag {
		conv.Replies = conv.Replies[:limitFlag]
	}

	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		for _, reply := range conv.Replies {
			env := pipeline.Envelope{
				Schema: pipeline.Schema,
				Kind:   pipeline.KindTweet,
				ID:     reply.ID,
				Data:   reply,
				Meta: &pipeline.Meta{
					Source:    "comments:" + statusID,
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
		b, err := jsonx.MarshalLine(conv)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	default:
		rootRows := result.TweetRows([]nitter.Tweet{conv.Status})
		if len(rootRows) > 0 {
			fmt.Fprintln(s.Out, rootRows[0].Line())
		}
		replyRows := result.TweetRows(conv.Replies)
		for _, r := range replyRows {
			fmt.Fprintln(s.Out, "  "+r.Line())
		}
		return nil
	}
}
