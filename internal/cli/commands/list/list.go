// Package list implements the `nitter list` command: fetch one Nitter
// list's timeline through the wiring layer and render it in the resolved
// output mode. Data acquisition goes through internal/cli/client and sdk
// models only — per ruling R11 this package never imports internal/nitter/*.
package list

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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

// validListID mirrors the appapi-side list ID contract so bad input exits 2
// before any wiring is built (and before any network): non-empty after
// trimming and free of whitespace, ?, # and / (URL-path-safe). Nitter
// accepts numeric list IDs and some refs — everything else passes through.
// appapi.ListTimeline re-validates as the R11-internal backstop.
func validListID(listID string) bool {
	if strings.TrimSpace(listID) == "" {
		return false
	}
	return !strings.ContainsAny(listID, " \t\n\r\v\f?#/")
}

// New builds the `nitter list <LIST_ID>` command over the shared streams.
//
// Exit codes (repo-wide semantics): success — including an empty timeline —
// exits 0; acquisition failure (every instance failed) exits 1 with the
// classified error message; usage problems (bad list ID, extra arguments,
// negative flags, --json with --ndjson, invalid config values) exit 2.
func New(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag    int
		maxPagesFlag int
		asJSON       bool
		asNDJSON     bool
		filters      tweetfilter.Filters
	)
	cmd := &cobra.Command{
		Use:   "list <LIST_ID>",
		Short: "Fetch a Nitter list's timeline",
		Long: `Fetch the timeline of the list LIST_ID and print one row per tweet:

  <ID>  <YYYY-MM-DD HH:MM>  @<handle>  <single-line text>

LIST_ID is the list's numeric ID (or a ref the instance accepts); it is
passed through to Nitter as the /i/lists/<LIST_ID> path. Instances are
tried in config order; an instance whose fetch fails cools down while the
next one is tried. The pages follow their load-more cursor.

Note: an empty result may mean the list is simply empty — or that it is new
and not yet ingested by this Nitter instance; the two are indistinguishable
from the outside. Neither is an error.

--limit caps the number of tweets; without the flag the config's default_limit
applies. --max-pages caps pagination; without the flag the config's max_pages
applies. Both must be >= 1 when given: there is no "unlimited" value.

--json prints a machine-readable document: one JSON object for a single
tweet, an array otherwise (an empty timeline prints []). --ndjson instead
prints one nitter.pipeline/v1 envelope per tweet (kind tweet, the tweet ID
as id, the Tweet as data, provenance in meta; meta.source is "list:" plus
the list ID). --json and --ndjson are mutually exclusive. An empty result
prints nothing on stdout in NDJSON mode and the (empty) hint on stderr in
the default modes.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter list <LIST_ID>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args[0], limitFlag, maxPagesFlag, asJSON, asNDJSON, filters)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 0,
		"Maximum tweets to fetch (default: config default_limit)")
	cmd.Flags().IntVar(&maxPagesFlag, "max-pages", 0,
		"Maximum list pages (default: config max_pages; built-in default 5)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Print one JSON object for a single tweet, an array otherwise")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false,
		"Print one nitter.pipeline/v1 envelope per tweet (kind tweet)")
	cmd.Flags().BoolVar(&filters.NoReposts, "no-reposts", false,
		"Drop pure retweets (retweet-header detection) from the output")
	cmd.Flags().BoolVar(&filters.MediaOnly, "media-only", false,
		"Drop tweets that carry no media attachments")
	cmd.Flags().StringVar(&filters.MediaType, "media-type", "",
		"Keep only tweets with at least one media entry of this type: image, video or gif")
	return cmd
}

// run executes one list fetch. Flag/config resolution order: an explicit flag
// wins over the config value. Both caps must be >= 1: a flag below 1 is a usage
// error and a config below 1 is rejected at load, so the acquisition layer
// never receives a non-positive limit or page budget. The flag defaults stay 0
// ("not given"), which is why the checks are gated on Changed.
func run(cmd *cobra.Command, s *invocation.Streams, listID string, limitFlag, maxPagesFlag int, asJSON, asNDJSON bool, filters tweetfilter.Filters) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if !validListID(listID) {
		return invocation.Usagef("list: %q is not a valid list ID (non-empty, no whitespace, ?, # or /)", listID)
	}
	if cmd.Flags().Changed("limit") && limitFlag < 1 {
		return invocation.Usagef("list: --limit must be >= 1 (there is no unlimited value)")
	}
	if cmd.Flags().Changed("max-pages") && maxPagesFlag < 1 {
		return invocation.Usagef("list: --max-pages must be >= 1 (there is no unlimited value)")
	}
	if err := filters.Validate(); err != nil {
		return invocation.Usagef("list: %v", err)
	}

	// The root PersistentPreRunE has published the baseline config by now;
	// a bad value in it is a usage error (exit 2).
	cfg, err := client.LoadEffectiveSettings()
	if err != nil {
		return err
	}
	limit, maxPages := cfg.DefaultLimit, cfg.MaxPages
	if cmd.Flags().Changed("limit") {
		limit = limitFlag
	}
	if cmd.Flags().Changed("max-pages") {
		maxPages = maxPagesFlag
	}

	w, err := client.Build(s.RootOptions, cfg, time.Now)
	if err != nil {
		return client.AsUsageError(err)
	}

	ctx := s.CTX
	if ctx == nil {
		ctx = context.Background()
	}
	tweets, instance, err := w.List().ListTimeline(ctx, listID, limit, maxPages)
	if err != nil {
		// Invalid-argument classifications (none expected past the local
		// validation) map to usage errors; acquisition failures exit 1.
		return client.AsUsageError(err)
	}
	// Field filters run post-fetch, pre-output: what the consumer sees is
	// the filtered slice (the fetch itself stays unbounded by them).
	tweets = tweetfilter.Apply(tweets, filters)
	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		err := writeNDJSON(s.Out, tweets, listID, instance, fetchedAt)
		if errors.Is(err, syscall.EPIPE) {
			// The reader hung up mid-stream (head/tail on a pipe): from the
			// command's perspective the run succeeded — pixiv sigpipe
			// policy. Windows may surface broken pipes as a different errno
			// (ERROR_BROKEN_PIPE), so this portable check is best-effort.
			return nil
		}
		return err
	case pipeline.ModeJSON:
		return writeJSON(s.Out, tweets)
	default:
		// ModeHuman and ModeText share the row rendering: it is already the
		// tab-separated, script-friendly line protocol.
		return writeRows(s, tweets)
	}
}

// writeRows prints one row per tweet; an empty timeline prints the (empty)
// hint on stderr and nothing on stdout.
func writeRows(s *invocation.Streams, tweets []nitter.Tweet) error {
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

// writeJSON prints the tweets as one JSON document: a single object when
// exactly one tweet was fetched, an array otherwise — and a literal [] for
// an empty timeline (a nil slice must not become null).
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

// writeNDJSON prints one nitter.pipeline/v1 envelope per tweet: kind tweet,
// the tweet ID as id, the Tweet as data, and provenance in meta (source
// "list:<id>", the instance base URL that produced the batch, and the
// RFC3339 UTC fetch timestamp).
func writeNDJSON(out io.Writer, tweets []nitter.Tweet, listID, instance, fetchedAt string) error {
	for _, tw := range tweets {
		env := pipeline.Envelope{
			Schema: pipeline.Schema,
			Kind:   pipeline.KindTweet,
			ID:     tw.ID,
			Data:   tw,
			Meta: &pipeline.Meta{
				Source:    "list:" + listID,
				Instance:  instance,
				FetchedAt: fetchedAt,
			},
		}
		if err := pipeline.WriteEnvelope(out, env); err != nil {
			return err
		}
	}
	return nil
}
