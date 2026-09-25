// Package thread implements the `nitter thread` command: fetch the full
// self-thread containing one status through the wiring layer and render it
// root first in the resolved output mode.
package thread

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// statusIDRe is the numeric status ID contract: 1-20 digits. URL and other
// reference forms belong to the get command.
var statusIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)

// New builds the `nitter thread <TWEET_ID>` command over the shared streams.
//
// Exit codes (repo-wide semantics): success — including an empty thread —
// exits 0; acquisition failure (a status that is not part of a thread
// answers not_found) exits 1 with the classified error message; usage
// problems (a non-numeric ID, extra arguments, --json with --ndjson,
// invalid config values) exit 2.
func New(s *invocation.Streams) *cobra.Command {
	var (
		asJSON   bool
		asNDJSON bool
	)
	cmd := &cobra.Command{
		Use:           "thread <TWEET_ID>",
		Short:         "Fetch the full self-thread containing a status, root first",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Fetch the full self-thread containing TWEET_ID and print one row per
status, root post first:

  <ID>  <YYYY-MM-DD HH:MM>  @<handle>  <single-line text>

TWEET_ID must be the numeric status ID (1-20 digits); URL and other
reference forms belong to the get command.

This capability always queries the FxTwitter fast lane regardless of
fetch_backend; --instance does not apply. A status that is not part of a
thread answers not_found (exit 1).

--json prints a JSON array of tweet objects ([] when empty).
--ndjson prints one nitter.pipeline/v1 envelope per status (kind tweet,
the status ID as id, provenance in meta). --json and --ndjson are
mutually exclusive. An empty result prints nothing on stdout in NDJSON
mode and the (empty) hint on stderr in the default mode.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter thread <TWEET_ID>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(s, args[0], asJSON, asNDJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print a JSON array of tweet objects")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per status (kind tweet)")
	return cmd
}

func run(s *invocation.Streams, rawID string, asJSON, asNDJSON bool) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(rawID)
	if !statusIDRe.MatchString(id) {
		return invocation.Usagef("thread: %q is not a numeric status ID", id)
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

	tweets, err := w.Thread().Thread(ctx, id)
	if err != nil {
		return client.AsUsageError(err)
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
					Source:    "thread:" + id,
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
		if tweets == nil {
			tweets = []nitter.Tweet{}
		}
		b, err := jsonx.MarshalLine(tweets)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
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
