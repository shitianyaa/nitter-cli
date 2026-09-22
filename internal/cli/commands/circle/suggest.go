package circle

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
)

// candidate is one aggregated circle candidate: a handle the seed either
// follows (static), retweets (behavioral), or both.
type candidate struct {
	Handle         string `json:"handle"`
	FollowersCount int    `json:"followers_count"`
	Bio            string `json:"bio"`
	FromFollowing  bool   `json:"-"`
	FromRetweet    bool   `json:"-"`
}

// source names where the candidate came from: "following", "retweet" or
// "both" (the co-occurrence case that ranks first).
func (c candidate) source() string {
	switch {
	case c.FromFollowing && c.FromRetweet:
		return "both"
	case c.FromFollowing:
		return "following"
	default:
		return "retweet"
	}
}

// score is the co-occurrence count: 0–2, one point per contributing source.
func (c candidate) score() int {
	n := 0
	if c.FromFollowing {
		n++
	}
	if c.FromRetweet {
		n++
	}
	return n
}

// needsEnrichment reports whether the candidate still lacks profile data and
// must be resolved with a Profile() fetch (retweet-only candidates).
func needsEnrichment(c candidate) bool { return !c.FromFollowing }

// aggregateCandidates merges the static following list with the authors of
// retweets in the timeline into a deterministic ranking. The seed itself is
// excluded (case-insensitive) and each handle yields exactly one row.
// Candidates sourced from following carry Bio/FollowersCount; retweet-only
// candidates leave them zero for the caller to enrich.
func aggregateCandidates(seed string, following []nitter.Profile, tweets []nitter.Tweet) []candidate {
	byHandle := make(map[string]*candidate)
	order := make([]string, 0, len(following)) // first-seen key order

	add := func(handle string) *candidate {
		key := strings.ToLower(handle)
		if c, ok := byHandle[key]; ok {
			return c
		}
		c := &candidate{Handle: handle}
		byHandle[key] = c
		order = append(order, key)
		return c
	}

	seedKey := strings.ToLower(seed)
	for _, p := range following {
		if strings.ToLower(p.Handle) == seedKey {
			continue
		}
		c := add(p.Handle)
		c.FromFollowing = true
		if c.FollowersCount == 0 {
			c.FollowersCount = p.FollowersCount
		}
		if c.Bio == "" {
			c.Bio = p.Bio
		}
	}
	for _, t := range tweets {
		if !t.IsRetweet {
			continue
		}
		h := t.Author.Handle
		if h == "" || strings.ToLower(h) == seedKey {
			continue
		}
		add(h).FromRetweet = true
	}

	out := make([]candidate, 0, len(order))
	for _, key := range order {
		out = append(out, *byHandle[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].score(), out[j].score(); a != b {
			return a > b
		}
		if a, b := out[i].FollowersCount, out[j].FollowersCount; a != b {
			return a > b
		}
		return out[i].Handle < out[j].Handle
	})
	return out
}

// shortBio flattens a bio to a single line and truncates it on a rune
// boundary (never splitting a UTF-8 sequence).
func shortBio(bio string) string {
	flat := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(bio)
	flat = strings.Join(strings.Fields(flat), " ")
	const max = 120
	if len(flat) <= max {
		return flat
	}
	cut := flat[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// newSuggestCmd builds `nitter circle suggest <HANDLE>`: read-only candidate
// discovery over the seed's following list and the authors it retweets.
func newSuggestCmd(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag        int
		minFollowersFlag int
		asJSON           bool
	)
	cmd := &cobra.Command{
		Use:           "suggest <HANDLE>",
		Short:         "Suggest circle candidates from a handle's following list and retweets",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter circle suggest <HANDLE>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			handle := strings.TrimPrefix(strings.TrimSpace(args[0]), "@")
			if !handleRe.MatchString(handle) {
				return invocation.Usagef("circle suggest: %q is not a valid handle (1-15 letters, digits or underscores, without the @)", args[0])
			}
			// --limit 0 is rejected on purpose: the two lanes give it opposite,
			// useless meanings (timeline: empty; following: one server-side
			// page). See the design doc.
			if limitFlag < 1 {
				return invocation.Usagef("circle suggest: --limit must be >= 1")
			}
			if minFollowersFlag < 0 {
				return invocation.Usagef("circle suggest: --min-followers must be >= 0")
			}
			return runSuggest(s, handle, limitFlag, minFollowersFlag, asJSON)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum accounts to fetch per source (following and timeline); must be >= 1")
	cmd.Flags().IntVar(&minFollowersFlag, "min-followers", 0,
		"Only list candidates with at least N followers in the trailing top-matches summary (0 = no filter)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print candidates as a JSON array")
	return cmd
}

// suggestRow is the JSON projection of one candidate (SDK contract style:
// every field marshals unconditionally).
type suggestRow struct {
	Handle         string `json:"handle"`
	FollowersCount int    `json:"followers_count"`
	Bio            string `json:"bio"`
	Source         string `json:"source"`
}

func runSuggest(s *invocation.Streams, handle string, limit, minFollowers int, asJSON bool) error {
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

	// Seed probe (profile-first discovery): the handle must exist; a failure
	// here is fatal, never a silent "no candidates".
	if _, err := w.Profile().Profile(ctx, handle); err != nil {
		return client.AsUsageError(err)
	}

	followingProfiles, _, followingErr := w.Following().Following(ctx, handle, limit, "")
	if followingErr != nil {
		fmt.Fprintf(s.Err, "warning: circle suggest: following lane: %v\n", followingErr)
	}
	tweets, _, timelineErr := w.Timeline().Timeline(ctx, handle, limit, cfg.MaxPages)
	if timelineErr != nil {
		fmt.Fprintf(s.Err, "warning: circle suggest: timeline lane: %v\n", timelineErr)
	}
	if followingErr != nil && timelineErr != nil {
		return client.AsUsageError(followingErr)
	}

	candidates := aggregateCandidates(handle, followingProfiles, tweets)

	// Enrich retweet-only candidates; a failed profile is skipped with a
	// warning (partial data beats a hard error), never silently dropped.
	enriched := make([]candidate, 0, len(candidates))
	for _, c := range candidates {
		if needsEnrichment(c) {
			p, err := w.Profile().Profile(ctx, c.Handle)
			if err != nil {
				fmt.Fprintf(s.Err, "warning: circle suggest: @%s: %v\n", c.Handle, err)
				continue
			}
			if p != nil {
				c.FollowersCount = p.FollowersCount
				c.Bio = p.Bio
			}
		}
		enriched = append(enriched, c)
	}
	candidates = enriched

	// Enrichment changed follower counts, so re-rank before output.
	sort.SliceStable(candidates, func(i, j int) bool {
		if a, b := candidates[i].score(), candidates[j].score(); a != b {
			return a > b
		}
		if a, b := candidates[i].FollowersCount, candidates[j].FollowersCount; a != b {
			return a > b
		}
		return candidates[i].Handle < candidates[j].Handle
	})

	if asJSON {
		rows := make([]suggestRow, 0, len(candidates))
		for _, c := range candidates {
			rows = append(rows, suggestRow{
				Handle:         c.Handle,
				FollowersCount: c.FollowersCount,
				Bio:            shortBio(c.Bio),
				Source:         c.source(),
			})
		}
		b, err := jsonx.MarshalLine(rows)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	}

	if len(candidates) == 0 {
		fmt.Fprintln(s.Err, "(empty)")
		return nil
	}

	followingCount, retweetCount, overlap := 0, 0, 0
	for _, c := range candidates {
		if c.FromFollowing {
			followingCount++
		}
		if c.FromRetweet {
			retweetCount++
		}
		if c.FromFollowing && c.FromRetweet {
			overlap++
		}
	}
	fmt.Fprintf(s.Out, "candidates for @%s (following: %d, retweet authors: %d, overlap: %d)\n",
		handle, followingCount, retweetCount, overlap)
	for _, c := range candidates {
		fmt.Fprintf(s.Out, "@%s\t%d\t%s\t%s\n", c.Handle, c.FollowersCount, shortBio(c.Bio), c.source())
	}

	fmt.Fprintf(s.Out, "\ntop matches (>= %d followers):\n", minFollowers)
	matched := 0
	for _, c := range candidates {
		if c.FollowersCount < minFollowers {
			continue
		}
		fmt.Fprintf(s.Out, "@%s\t%d\t%s\t%s\n", c.Handle, c.FollowersCount, shortBio(c.Bio), c.source())
		matched++
	}
	if matched == 0 {
		fmt.Fprintln(s.Err, "(no matches)")
	}
	return nil
}
