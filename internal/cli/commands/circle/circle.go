// Package circle implements the `nitter circle` command family: manage and
// run creator circles (private rosters) stored in ~/.nitter-cli/circles.toml.
package circle

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
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
	cmd.AddCommand(newRemoveCmd(s))
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
		asJSON       bool
		minFollowers int
	)
	cmd := &cobra.Command{
		Use:           "show <NAME>",
		Short:         "Show cached profiles for users in a circle (local, no network)",
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
			if len(circle.Users) == 0 {
				if asJSON {
					_, err := s.Out.Write([]byte("[]\n"))
					return err
				}
				fmt.Fprintln(s.Err, "(empty)")
				return nil
			}
			profiles, err := settings.LoadProfiles(p.ProfilesFile)
			if err != nil {
				return err
			}

			now := time.Now()
			rows := make([]sidecarRow, 0, len(circle.Users))
			lines := make([]string, 0, len(circle.Users))
			missing := 0
			for _, handle := range circle.Users {
				prof, has := settings.FindProfile(profiles, handle)
				if !has || prof.FetchedAt == "" {
					missing++
				}
				if minFollowers > 0 && (!has || prof.FetchedAt == "" || prof.FollowersCount < minFollowers) {
					continue
				}
				rows = append(rows, sidecarRow{
					Handle:         handle,
					Name:           prof.Name,
					Bio:            shortBio(prof.Bio),
					FollowersCount: prof.FollowersCount,
					FetchedAt:      prof.FetchedAt,
					Role:           prof.Role,
					Note:           prof.Note,
				})
				lines = append(lines, renderSidecarLine(handle, prof, has && prof.FetchedAt != "", now))
			}

			if missing > 0 {
				fmt.Fprintf(s.Err, "note: %d member(s) have no cached profile; run 'nitter circle refresh %s'\n", missing, circle.Key)
			}

			if asJSON {
				return writeSidecarJSON(s, rows)
			}
			if len(lines) == 0 {
				fmt.Fprintln(s.Err, "(empty)")
				return nil
			}
			for _, line := range lines {
				fmt.Fprintln(s.Out, line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print members as a JSON array joined with cached profiles")
	cmd.Flags().IntVar(&minFollowers, "min-followers", 0,
		"Keep only members whose cached follower count is at least N (0 = no filter); reads the local cache, no network")
	return cmd
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

			matchedKey, circle, ok := settings.FindCircleKey(circles, name)
			inserted := false
			if !ok {
				circle = settings.Circle{
					Key:   name,
					Name:  name,
					Users: []string{cleanHandle},
				}
				circles[name] = circle
				inserted = true
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
					circles[matchedKey] = circle
					inserted = true
				}
			}

			if err := settings.SaveCircles(p.CirclesFile, circles); err != nil {
				return err
			}

			// Best-effort profile fetch, gated on an actual insertion: a
			// duplicate add is a roster no-op and must not touch the network.
			// The roster write already succeeded, so a failed fetch must not
			// fail the command — a warning keeps the old behavior (facts
			// arrive with circle refresh) visible.
			if inserted {
				fetchProfileFacts(s, p.ProfilesFile, cleanHandle)
			}

			fmt.Fprintf(s.Out, "added @%s to circle %s\n", cleanHandle, name)
			return nil
		},
	}
	return cmd
}

// fetchProfileFacts best-effort fetches a newly added member's profile facts
// and merges them into the sidecar. Every failure here is only a warning on
// stderr: the roster write has already succeeded, so the command stays exit 0.
// Like circle refresh, only the machine fields (Handle/Name/Bio/
// FollowersCount) are written via settings.MergeProfileFacts — the judgement
// fields (role/note/noted_at) are never touched here.
func fetchProfileFacts(s *invocation.Streams, profilesFile, handle string) {
	fetchCtx := s.CTX
	if fetchCtx == nil {
		fetchCtx = context.Background()
	}
	cfg, err := client.LoadEffectiveSettings()
	if err != nil {
		fmt.Fprintf(s.Err, "warning: circle add: load settings: %v\n", err)
		return
	}
	w, err := client.Build(s.RootOptions, cfg, time.Now)
	if err != nil {
		fmt.Fprintf(s.Err, "warning: circle add: build client: %v\n", err)
		return
	}
	prof, err := w.Profile().Profile(fetchCtx, handle)
	if err != nil {
		fmt.Fprintf(s.Err, "warning: circle add: fetch profile facts: %v\n", err)
		return
	}
	profiles, err := settings.LoadProfiles(profilesFile)
	if err != nil {
		// A sidecar we cannot read must never be rewritten from an empty map —
		// that would drop every other member's judgement. Warn and leave it.
		fmt.Fprintf(s.Err, "warning: circle add: load profile sidecar: %v\n", err)
		return
	}
	profiles = settings.MergeProfileFacts(profiles, handle, settings.Profile{
		Handle:         prof.Handle,
		Name:           prof.Name,
		Bio:            prof.Bio,
		FollowersCount: prof.FollowersCount,
	}, time.Now())
	if err := settings.SaveProfiles(profilesFile, profiles); err != nil {
		fmt.Fprintf(s.Err, "warning: circle add: save profile facts: %v\n", err)
	}
}

// newRemoveCmd builds `nitter circle remove <NAME> <HANDLE>`: it edits ONLY
// the circle's users array in circles.toml. The profile sidecar
// (~/.nitter-cli/profiles.toml, role/note judgement included) is never read
// nor written here. A non-member handle is idempotent: an explicit notice on
// stderr, exit 0 — the notice is the honesty, never silence.
func newRemoveCmd(s *invocation.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "remove <NAME> <HANDLE>",
		Short:         "Remove a user handle from a circle (the profile sidecar is untouched)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return invocation.Usagef("usage: nitter circle remove <NAME> <HANDLE>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			cleanHandle := strings.TrimPrefix(strings.TrimSpace(args[1]), "@")
			if name == "" {
				return invocation.Usagef("circle: circle name cannot be empty")
			}
			if !handleRe.MatchString(cleanHandle) {
				return invocation.Usagef("circle: %q is not a valid handle (1-15 letters, digits or underscores, without the @)", args[1])
			}

			p, err := paths.New()
			if err != nil {
				return err
			}
			circles, err := settings.LoadCircles(p.CirclesFile)
			if err != nil {
				return err
			}
			matchedKey, circle, ok := settings.FindCircleKey(circles, name)
			if !ok {
				return fmt.Errorf("circle %q not found", name)
			}

			kept := circle.Users[:0:0]
			removed := false
			for _, u := range circle.Users {
				if strings.EqualFold(u, cleanHandle) {
					removed = true
					continue
				}
				kept = append(kept, u)
			}
			if !removed {
				fmt.Fprintf(s.Err, "circle remove: @%s is not a member of %s; nothing changed\n", cleanHandle, matchedKey)
				return nil
			}
			circle.Users = kept
			circles[matchedKey] = circle
			if err := settings.SaveCircles(p.CirclesFile, circles); err != nil {
				return err
			}
			fmt.Fprintf(s.Out, "removed @%s from circle %s\n", cleanHandle, matchedKey)
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
			if limitFlag < 1 {
				return invocation.Usagef("circle run: --limit must be >= 1 (there is no unlimited value)")
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
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum tweets to fetch per user")
	cmd.Flags().BoolVar(&mediaOnlyFlag, "media-only", false,
		"Fetch only tweets with media attachments (uses the Fx media endpoint; snapshot semantics: every run re-fetches each member's latest tweets from scratch, no incremental state — use watch for incremental tracking)")
	cmd.Flags().StringVar(&mediaTypeFlag, "media-type", "",
		"Keep only tweets with at least one media entry of this type: image, video or gif (applied after fetching, so the result may be shorter than --limit)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print tweets as a JSON array")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per tweet (kind tweet)")
	return cmd
}
