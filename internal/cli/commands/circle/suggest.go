package circle

import (
	"sort"
	"strings"
	"unicode/utf8"

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
