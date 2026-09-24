// Package search implements the `nitter search` command: run one query
// against the configured Nitter instances and render the result timeline in
// the resolved output mode. Data acquisition goes through internal/cli/client
// and sdk models only — per ruling R11 this package never imports
// internal/nitter/*.
package search

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

// New builds the `nitter search <QUERY>` command over the shared streams.
//
// Exit codes (repo-wide semantics): success — including an empty result —
// exits 0; acquisition failure (every instance failed) exits 1 with the
// classified error message; usage problems (empty query, extra arguments,
// negative flags, --json with --ndjson, invalid config values) exit 2.
func New(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag    int
		maxPagesFlag int
		typeFlag     string
		asJSON       bool
		asNDJSON     bool
		filters      tweetfilter.Filters
	)
	cmd := &cobra.Command{
		Use:   "search <QUERY>",
		Short: "Search tweets on the configured Nitter instances",
		Long: `Run QUERY against the configured Nitter instances and print one row per
matching tweet (or matching user profiles when --type user):

  <ID>  <YYYY-MM-DD HH:MM>  @<handle>  <single-line text>

When --type user:

  @<handle>  <name>  <followers_count>  <bio>

The query is passed through to Nitter unchanged (URL-escaped only), so its
own query syntax applies: a leading # searches a hashtag, "from:user" a
user's posts, anything else is a plain phrase search.

Instances are tried in config order; an instance whose fetch fails cools
down while the next one is tried. The result pages follow their load-more
cursor.

--type specifies search target: "tweet" (default) or "user".
--limit caps the number of items; without the flag the config's default_limit
applies. --max-pages caps pagination; without the flag the config's max_pages
applies. Both must be >= 1 when given: there is no "unlimited" value.

--json prints a machine-readable document: one JSON object for a single
item, an array otherwise (an empty result prints []). --ndjson instead
prints one nitter.pipeline/v1 envelope per item (kind tweet or profile,
the item ID as id, the item as data, provenance in meta; meta.source is
"search:" plus the query as typed). --json and --ndjson are mutually exclusive.
An empty result prints nothing on stdout in NDJSON mode and the (empty) hint on
stderr in the default modes.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter search <QUERY>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args[0], limitFlag, maxPagesFlag, typeFlag, asJSON, asNDJSON, filters)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 0,
		"Maximum items to fetch (default: config default_limit)")
	cmd.Flags().IntVar(&maxPagesFlag, "max-pages", 0,
		"Maximum result pages (default: config max_pages; built-in default 5)")
	cmd.Flags().StringVar(&typeFlag, "type", "tweet",
		"Search target type: tweet or user (default: tweet)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Print one JSON object for a single item, an array otherwise")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false,
		"Print one nitter.pipeline/v1 envelope per item (kind tweet or profile)")
	cmd.Flags().BoolVar(&filters.NoReposts, "no-reposts", false,
		"Drop pure retweets (retweet-header detection) from the output")
	cmd.Flags().BoolVar(&filters.MediaOnly, "media-only", false,
		"Drop tweets that carry no media attachments")
	cmd.Flags().StringVar(&filters.MediaType, "media-type", "",
		"Keep only tweets with at least one media entry of this type: image, video or gif")
	return cmd
}

// run executes one search. Flag/config resolution order: an explicit flag
// wins over the config value; a negative flag is a usage error while a
// negative config value keeps its documented "0 semantics = all" pixiv
// heritage (appapi treats limit <= 0 as unbounded).
func run(cmd *cobra.Command, s *invocation.Streams, query string, limitFlag, maxPagesFlag int, typeFlag string, asJSON, asNDJSON bool, filters tweetfilter.Filters) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if strings.TrimSpace(query) == "" {
		return invocation.Usagef("search: the query must not be empty")
	}
	typeFlag = strings.ToLower(strings.TrimSpace(typeFlag))
	if typeFlag != "tweet" && typeFlag != "user" {
		return invocation.Usagef("search: invalid --type %q (allowed: tweet, user)", typeFlag)
	}
	if cmd.Flags().Changed("limit") && limitFlag < 1 {
		return invocation.Usagef("search: --limit must be >= 1 (there is no unlimited value)")
	}
	if cmd.Flags().Changed("max-pages") && maxPagesFlag < 1 {
		return invocation.Usagef("search: --max-pages must be >= 1 (there is no unlimited value)")
	}
	if err := filters.Validate(); err != nil {
		return invocation.Usagef("search: %v", err)
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

	if typeFlag == "user" {
		profiles, err := w.Search().SearchUsers(ctx, query, limit)
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
						Source:    "search:" + query,
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
			var v any = profiles
			if len(profiles) == 1 {
				v = profiles[0]
			}
			b, err := jsonx.MarshalLine(v)
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

	tweets, instance, err := w.Search().Search(ctx, query, limit, maxPages)
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
		err := writeNDJSON(s.Out, tweets, query, instance, fetchedAt)
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

// writeRows prints one row per tweet; an empty result prints the (empty)
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
// an empty result (a nil slice must not become null).
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
// "search:<query>" — the query as typed, unescaped —, the instance base URL
// that produced the batch, and the RFC3339 UTC fetch timestamp).
func writeNDJSON(out io.Writer, tweets []nitter.Tweet, query, instance, fetchedAt string) error {
	for _, tw := range tweets {
		env := pipeline.Envelope{
			Schema: pipeline.Schema,
			Kind:   pipeline.KindTweet,
			ID:     tw.ID,
			Data:   tw,
			Meta: &pipeline.Meta{
				Source:    "search:" + query,
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
