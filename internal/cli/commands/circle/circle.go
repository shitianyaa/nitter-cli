// Package circle implements the `nitter circle` command family: manage and
// run creator circles (private rosters) stored in ~/.nitter-cli/circles.toml.
package circle

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
	"github.com/shitianyaa/nitter-cli/internal/config/paths"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
)

var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

type circleSummary struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	UserCount   int    `json:"user_count"`
}

// New builds the `nitter circle` parent command over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "circle",
		Short:         "Manage creator circles (rosters of accounts)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newListCmd(s))
	cmd.AddCommand(newShowCmd(s))
	cmd.AddCommand(newRefreshCmd(s))
	cmd.AddCommand(newSuggestCmd(s))
	cmd.AddCommand(newAddCmd(s))
	cmd.AddCommand(newRunCmd(s))
	return cmd
}

func newListCmd(s *invocation.Streams) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List all configured circles",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return invocation.Usagef("usage: nitter circle list")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := paths.New()
			if err != nil {
				return err
			}
			circles, err := settings.LoadCircles(p.CirclesFile)
			if err != nil {
				return err
			}

			keys := make([]string, 0, len(circles))
			for k := range circles {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			if asJSON {
				summaries := make([]circleSummary, 0, len(keys))
				for _, k := range keys {
					c := circles[k]
					summaries = append(summaries, circleSummary{
						Key:         c.Key,
						Name:        c.Name,
						Description: c.Description,
						UserCount:   len(c.Users),
					})
				}
				b, err := jsonx.MarshalLine(summaries)
				if err != nil {
					return err
				}
				_, err = s.Out.Write(b)
				return err
			}

			if len(keys) == 0 {
				fmt.Fprintln(s.Err, "(empty)")
				return nil
			}
			for _, k := range keys {
				c := circles[k]
				fmt.Fprintf(s.Out, "%s\t%s\t%s\t%d\n", c.Key, c.Name, c.Description, len(c.Users))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print circles as a JSON array")
	return cmd
}

func newShowCmd(s *invocation.Streams) *cobra.Command {
	var (
		asJSON          bool
		minFollowers    int
		minFollowersSet bool
	)
	cmd := &cobra.Command{
		Use:           "show <NAME>",
		Short:         "Show users in a specific circle",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter circle show <NAME>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if minFollowers < 0 {
				return invocation.Usagef("circle show: --min-followers must be >= 0")
			}
			p, err := paths.New()
			if err != nil {
				return err
			}
			circles, err := settings.LoadCircles(p.CirclesFile)
			if err != nil {
				return err
			}
			circle, ok := settings.FindCircle(circles, name)
			if !ok {
				return fmt.Errorf("circle %q not found", name)
			}

			if minFollowersSet {
				return showFiltered(cmd, s, circle, minFollowers, asJSON)
			}

			if asJSON {
				users := circle.Users
				if users == nil {
					users = []string{}
				}
				b, err := jsonx.MarshalLine(users)
				if err != nil {
					return err
				}
				_, err = s.Out.Write(b)
				return err
			}

			if len(circle.Users) == 0 {
				fmt.Fprintln(s.Err, "(empty)")
				return nil
			}
			for _, u := range circle.Users {
				fmt.Fprintf(s.Out, "@%s\n", u)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print users as a JSON array")
	cmd.Flags().Var(&minFollowersFlag{&minFollowers, &minFollowersSet}, "min-followers",
		"Show only members with at least N followers (fetches each member's profile; prints @<handle>\t<followers>, or JSON objects with --json)")
	return cmd
}

// minFollowersFlag is an int flag value that records whether the flag was
// set, so the default (absent) state performs no network requests.
type minFollowersFlag struct {
	ptr  *int
	seen *bool
}

func (f *minFollowersFlag) String() string {
	if f.ptr == nil {
		return "0"
	}
	return strconv.Itoa(*f.ptr)
}

func (f *minFollowersFlag) Set(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("--min-followers must be an integer")
	}
	*f.ptr = n
	*f.seen = true
	return nil
}

func (f *minFollowersFlag) Type() string { return "int" }

// showFiltered resolves each member's profile through the client wiring and
// keeps only members with at least min followers. A member whose profile
// fetch fails is skipped with a one-line stderr warning while the others
// continue; when every member fails, the last error is returned as a
// runtime failure (exit 1).
func showFiltered(cmd *cobra.Command, s *invocation.Streams, circle settings.Circle, min int, asJSON bool) error {
	if len(circle.Users) == 0 {
		if asJSON {
			_, err := s.Out.Write([]byte("[]\n"))
			return err
		}
		fmt.Fprintln(s.Err, "(empty)")
		return nil
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

	type member struct {
		Handle         string `json:"handle"`
		FollowersCount int    `json:"followers_count"`
	}

	var members []member
	var anySuccess bool
	var lastErr error
	for _, u := range circle.Users {
		p, err := w.Profile().Profile(ctx, u)
		if err != nil {
			lastErr = err
			fmt.Fprintf(s.Err, "warning: circle show: @%s: %v\n", u, err)
			continue
		}
		anySuccess = true
		if p.FollowersCount < min {
			continue
		}
		members = append(members, member{Handle: p.Handle, FollowersCount: p.FollowersCount})
	}

	if !anySuccess && lastErr != nil {
		return client.AsUsageError(lastErr)
	}

	if asJSON {
		if members == nil {
			members = []member{}
		}
		b, err := jsonx.MarshalLine(members)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	}
	for _, m := range members {
		fmt.Fprintf(s.Out, "@%s\t%d\n", m.Handle, m.FollowersCount)
	}
	return nil
}

func newAddCmd(s *invocation.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "add <NAME> <HANDLE>",
		Short:         "Add a user handle to a circle",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return invocation.Usagef("usage: nitter circle add <NAME> <HANDLE>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			handle := strings.TrimSpace(args[1])
			cleanHandle := strings.TrimPrefix(handle, "@")

			if name == "" {
				return invocation.Usagef("circle: circle name cannot be empty")
			}
			if !handleRe.MatchString(cleanHandle) {
				return invocation.Usagef("circle: %q is not a valid handle (1-15 letters, digits or underscores, without the @)", handle)
			}

			p, err := paths.New()
			if err != nil {
				return err
			}
			circles, err := settings.LoadCircles(p.CirclesFile)
			if err != nil {
				return err
			}

			circle, ok := settings.FindCircle(circles, name)
			if !ok {
				circle = settings.Circle{
					Key:   name,
					Name:  name,
					Users: []string{cleanHandle},
				}
				circles[name] = circle
			} else {
				exists := false
				for _, u := range circle.Users {
					if strings.EqualFold(u, cleanHandle) {
						exists = true
						break
					}
				}
				if !exists {
					circle.Users = append(circle.Users, cleanHandle)
					circles[circle.Key] = circle
				}
			}

			if err := settings.SaveCircles(p.CirclesFile, circles); err != nil {
				return err
			}

			fmt.Fprintf(s.Out, "added @%s to circle %s\n", cleanHandle, name)
			return nil
		},
	}
	return cmd
}

func newRunCmd(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag     int
		mediaOnlyFlag bool
		mediaTypeFlag string
		asJSON        bool
		asNDJSON      bool
	)
	cmd := &cobra.Command{
		Use:           "run <NAME>",
		Short:         "Traverse users in a circle and fetch their latest tweets",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter circle run <NAME>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
			if err != nil {
				return err
			}
			if limitFlag < 0 {
				return invocation.Usagef("circle run: --limit must be >= 0")
			}
			// --media-type validation precedes any network (exit 2), the same
			// placement as the user command's filter validation.
			filters := tweetfilter.Filters{MediaType: mediaTypeFlag}
			if err := filters.Validate(); err != nil {
				return invocation.Usagef("circle run: %v", err)
			}

			p, err := paths.New()
			if err != nil {
				return err
			}
			circles, err := settings.LoadCircles(p.CirclesFile)
			if err != nil {
				return err
			}
			circle, ok := settings.FindCircle(circles, name)
			if !ok {
				return fmt.Errorf("circle %q not found", name)
			}

			if len(circle.Users) == 0 {
				switch mode {
				case pipeline.ModeJSON:
					_, err = s.Out.Write([]byte("[]\n"))
					return err
				case pipeline.ModeNDJSON:
					return nil
				default:
					fmt.Fprintln(s.Err, "(empty)")
					return nil
				}
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

			opts := []client.TimelineOption{
				client.WithMediaOnly(mediaOnlyFlag || mediaTypeFlag != ""),
			}
			// meta.filter marks the media filter in effect on NDJSON
			// envelopes ("media_only" or the --media-type value); omitted
			// without any filter so consumers can verify filtering.
			metaFilter := ""
			if mediaOnlyFlag {
				metaFilter = "media_only"
			} else if mediaTypeFlag != "" {
				metaFilter = mediaTypeFlag
			}

			var allTweets []nitter.Tweet
			var anySuccess bool
			var lastErr error
			fetchedAt := time.Now().UTC().Format(time.RFC3339)

			for _, u := range circle.Users {
				tweets, instance, err := w.Timeline().Timeline(ctx, u, limitFlag, cfg.MaxPages, opts...)
				if err != nil {
					lastErr = err
					fmt.Fprintf(s.Err, "warning: circle run: @%s: %v\n", u, err)
					continue
				}
				anySuccess = true
				if mediaTypeFlag != "" {
					tweets = tweetfilter.Apply(tweets, filters)
				}
				if mode == pipeline.ModeNDJSON {
					for _, tw := range tweets {
						env := pipeline.Envelope{
							Schema: pipeline.Schema,
							Kind:   pipeline.KindTweet,
							ID:     tw.ID,
							Data:   tw,
							Meta: &pipeline.Meta{
								Source:    "circle:" + circle.Key,
								Instance:  instance,
								FetchedAt: fetchedAt,
								Filter:    metaFilter,
							},
						}
						if err := pipeline.WriteEnvelope(s.Out, env); err != nil {
							if errors.Is(err, syscall.EPIPE) {
								return nil
							}
							return err
						}
					}
				} else {
					allTweets = append(allTweets, tweets...)
				}
			}

			if !anySuccess && lastErr != nil {
				return client.AsUsageError(lastErr)
			}

			switch mode {
			case pipeline.ModeNDJSON:
				return nil
			case pipeline.ModeJSON:
				if allTweets == nil {
					allTweets = []nitter.Tweet{}
				}
				b, err := jsonx.MarshalLine(allTweets)
				if err != nil {
					return err
				}
				_, err = s.Out.Write(b)
				return err
			default:
				rows := result.TweetRows(allTweets)
				if len(rows) == 0 {
					fmt.Fprintln(s.Err, "(empty)")
					return nil
				}
				for _, r := range rows {
					fmt.Fprintln(s.Out, r.Line())
				}
				return nil
			}
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum tweets to fetch per user (default: 20, 0 = all)")
	cmd.Flags().BoolVar(&mediaOnlyFlag, "media-only", false,
		"Fetch only tweets with media attachments (uses the Fx media endpoint; snapshot semantics: every run re-fetches each member's latest tweets from scratch, no incremental state — use watch for incremental tracking)")
	cmd.Flags().StringVar(&mediaTypeFlag, "media-type", "",
		"Keep only tweets with at least one media entry of this type: image, video or gif (applied after fetching, so the result may be shorter than --limit)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print tweets as a JSON array")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per tweet (kind tweet)")
	return cmd
}
