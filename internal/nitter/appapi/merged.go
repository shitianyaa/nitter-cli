package appapi

// Merged RSS acquisition: one request fetches several user timelines at once
// through the Nitter merged endpoint (/{u1,u2,...}/rss), which answers with a
// single newest-first feed carrying each item's author in dc:creator. The
// feed is bucketed per author so the command layer can keep its per-source
// dedup state (internal/storage/seen) honest, one source at a time.
//
// The endpoint is Nitter-only (FxTwitter has no merged form), and it cannot
// represent a repost: Nitter attributes a reposted item to the ORIGINAL
// author, so a repost of an account outside the batch looks exactly like a
// foreign item and is dropped. That is why the command layer only merges
// when reposts are filtered out — see the watch command's prefetchMerged.

import (
	"context"
	"strings"

	"github.com/shitianyaa/nitter-cli/sdk"
)

const opMergedTimeline = "appapi.MergedTimeline"

// MergedRSSMaxPath is the comma-joined path budget for one merged RSS
// request. Self-hosted Nitter answers 404 past roughly 280 characters
// (observed on production instances), so the batch stays under 250 with
// margin — the same value the reference plugin uses.
const MergedRSSMaxPath = 250

// MergedBatches splits handles into batches whose comma-joined path segment
// stays within MergedRSSMaxPath. Input order is preserved and blank handles
// are dropped; a handle that alone exceeds the budget forms its own batch
// (its request fails and the caller falls back to per-source fetches).
func MergedBatches(handles []string) [][]string {
	var (
		batches [][]string
		current []string
		length  int
	)
	for _, raw := range handles {
		handle := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
		if handle == "" {
			continue
		}
		addition := len(handle)
		if len(current) > 0 {
			addition++ // the joining comma
		}
		if len(current) > 0 && length+addition > MergedRSSMaxPath {
			batches = append(batches, current)
			current = nil
			length = 0
			addition = len(handle)
		}
		current = append(current, handle)
		length += addition
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// MergedTimeline fetches one batch of user timelines through the merged RSS
// endpoint (/{u1,u2,...}/rss). The merged feed is newest-first with each
// item's author in dc:creator, so the result is bucketed per author
// (lower-cased) and the caller's per-source dedup state stays correct.
//
// Items authored by nobody in the batch are dropped: a repost of an outside
// account is exactly that shape — Nitter RSS points a repost's guid/link at
// the ORIGINAL author — which is why the command layer only merges when
// reposts are filtered out. This is also why a repost between two batch
// members collapses into the original author's bucket instead of surfacing
// as the reposter's tweet.
func (c *Client) MergedTimeline(ctx context.Context, handles []string, opts PageOptions) (map[string][]nitter.Tweet, string, error) {
	if len(handles) == 0 {
		return nil, "", nitter.Errorf(nitter.KindInvalidArg, opMergedTimeline, "merged fetch needs at least one handle")
	}
	for _, handle := range handles {
		if !handleRe.MatchString(handle) {
			return nil, "", nitter.Errorf(nitter.KindInvalidArg, opMergedTimeline,
				"handle %q must be 1-15 letters, digits or underscores (without the @)", handle)
		}
	}
	if c.HTTP == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opMergedTimeline, "no transport wired into the appapi client")
	}
	if c.Chooser == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opMergedTimeline, "no instance chooser wired into the appapi client")
	}

	segment := strings.Join(handles, ",")
	var raw []nitter.Tweet
	base, err := c.withInstance(ctx, opMergedTimeline, func(ctx context.Context, base string) error {
		got, err := c.scanRSS(ctx, base, segment, opts.Limit, opts.MaxPages, opts.Stop)
		if err != nil {
			return err
		}
		raw = got
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	members := make(map[string]bool, len(handles))
	for _, handle := range handles {
		members[strings.ToLower(handle)] = true
	}
	buckets := make(map[string][]nitter.Tweet, len(handles))
	for _, tw := range raw {
		key := strings.ToLower(tw.Author.Handle)
		if key == "" || !members[key] {
			// No dc:creator, or an author nobody asked for: the item belongs
			// to a foreign account (the repost-of-an-outsider shape) and has
			// no source to be reported against.
			continue
		}
		buckets[key] = append(buckets[key], tw)
	}
	return buckets, base, nil
}
