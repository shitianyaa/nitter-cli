package appapi

// User timeline acquisition: the implementation behind `nitter user` (and
// later watch sources). Timeline drives one Nitter instance through the
// layered fetch the reference plugin established (ruling R15): the RSS feed
// first, and — when the feed FAILS or yields NO tweets — the HTML user page
// with cursor pagination. Both layers project into the same sdk Tweet shape
// through internal/nitter/rss and internal/nitter/html, so media/text
// fidelity rules are shared.
//
// Instance rotation reuses the Chooser: an instance whose whole layered
// attempt fails is marked failed (cooldown) and the next configured instance
// is tried; a success is marked successful. The method returns the base URL
// of the instance that produced the result, so callers can stamp provenance
// into NDJSON meta without reaching into the composition root.
//
// Authentication: instance credentials are wired by the CLI layer as a
// host-scoped transport policy (httpx.Options.BasicAuth, see probe.go and
// httpx/auth.go), so fetches against a credentialed instance's host carry
// the Authorization header without any per-call assembly here. Context
// cancellation is honored between and during
// requests: a canceled or deadline-exceeded fetch aborts rotation and
// surfaces the context error instead of moving on to the next instance.

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/shitianyaa/nitter-cli/internal/nitter/html"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// opTimeline is the Op stamped on Timeline's own errors.
const opTimeline = "appapi.Timeline"

// defaultMaxPages is the HTML pagination cap when PageOptions.MaxPages is 0.
const defaultMaxPages = 5

// PageOptions bounds one Timeline fetch.
type PageOptions struct {
	// Limit is the maximum number of tweets to return; 0 (or negative) means
	// all — still bounded by MaxPages. The RSS feed is scanned through the
	// Min-Id cursor chain: its items are collected up to the limit, and the
	// scan stops before requesting a page the limit would discard.
	Limit int
	// MaxPages caps how many HTML timeline pages are fetched on the HTML
	// path (the first page counts). 0 → defaultMaxPages; negative →
	// UNBOUNDED: the cursor chain is followed until the upstream stops
	// serving a cursor (or the context is canceled) — the CLI's `user
	// --max-pages 0` maps to this. Search/List keep the historical
	// "0 = default" semantics at their own entry points.
	MaxPages int
	// Stop is consulted after each RSS page of the Min-Id cursor scan;
	// returning true ends the scan. The watch command uses it to stop once a
	// page adds nothing the source has not already seen. nil never stops
	// early. The HTML path paginates with its own cursor and ignores it.
	Stop func(page []nitter.Tweet) bool
	// Sort selects the result ordering of a SEARCH fetch: "" and "latest"
	// (case-insensitive) both mean the default newest-first feed ("f=tweets"
	// on the wire), "top" asks for the popular-results feed ("f=top"). Any
	// other value is rejected as KindInvalidArg — never silently degraded to
	// the default. Search only; Timeline/List/MergedTimeline ignore it.
	Sort string
}

// handleRe is the X handle contract, enforced before any network: 1-15
// letters, digits or underscores.
var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

// Timeline fetches a user timeline. The handle is validated against the
// X handle contract BEFORE any rotation or network (KindInvalidArg
// otherwise). The returned string is the base URL of the instance that
// produced the result ("" only on error).
func (c *Client) Timeline(ctx context.Context, handle string, opts PageOptions) ([]nitter.Tweet, string, error) {
	if !handleRe.MatchString(handle) {
		return nil, "", nitter.Errorf(nitter.KindInvalidArg, opTimeline,
			"handle must be 1-15 letters, digits or underscores (without the @)")
	}
	if c.HTTP == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opTimeline, "no transport wired into the appapi client")
	}
	if c.Chooser == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opTimeline, "no instance chooser wired into the appapi client")
	}
	// 0 keeps the built-in default; a negative value is the explicit
	// unbounded contract (PageOptions.MaxPages doc) and passes through.
	maxPages := opts.MaxPages
	if maxPages == 0 {
		maxPages = defaultMaxPages
	}

	var tweets []nitter.Tweet
	base, err := c.withInstance(ctx, opTimeline, func(ctx context.Context, base string) error {
		got, err := c.timelineFromInstance(ctx, base, handle, opts.Limit, maxPages, opts.Stop)
		if err != nil {
			return err
		}
		tweets = got
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return tweets, base, nil
}

// withInstance runs fn against each configured instance in rotation order,
// marking success or failure on the chooser, and returns the base URL of the
// instance that succeeded. It is the shared rotation contract of Timeline and
// MergedTimeline: context cancellation aborts instead of rotating on, every
// configured instance is attempted at most once, and the last attempt's error
// is the one reported.
func (c *Client) withInstance(ctx context.Context, op string, fn func(ctx context.Context, base string) error) (string, error) {
	tried := make(map[string]bool)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		base, markSuccess, markFailure, err := c.Chooser.Pick()
		if err != nil {
			// Nothing configured, or every instance is cooling down. If we
			// already attempted someone, their failure is the better report.
			if lastErr != nil {
				return "", lastErr
			}
			return "", err
		}
		if tried[base] {
			// Every configured instance has been attempted (a disabled
			// cooldown makes Pick repeat). A previous attempt must have
			// failed for us to still be looping.
			if lastErr != nil {
				return "", lastErr
			}
			return "", nitter.Errorf(nitter.KindUnavailable, op, "all instances failed")
		}
		tried[base] = true
		if err := fn(ctx, base); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// The caller gave up: abort instead of rotating on.
				return "", err
			}
			markFailure()
			lastErr = err
			continue
		}
		markSuccess()
		return base, nil
	}
}

// timelineFromInstance runs the layered fetch against one instance: RSS
// first; the HTML user page (paginated) when the feed fails or yields zero
// tweets (R15: both conditions trigger the fallback). The fallback's error,
// when it too fails, is the attempt's error — the last stage is the most
// informative. An empty RSS feed followed by an empty HTML page is a success
// with zero tweets, never an error.
func (c *Client) timelineFromInstance(ctx context.Context, base, handle string, limit, maxPages int, stop func(page []nitter.Tweet) bool) ([]nitter.Tweet, error) {
	tweets, err := c.timelineRSS(ctx, base, handle, limit, maxPages, stop)
	if err == nil && len(tweets) > 0 {
		return tweets, nil
	}
	return c.timelineHTML(ctx, base, handle, limit, maxPages)
}

// timelineRSS fetches and projects <base>/<handle>/rss. The feed is scanned
// through the Min-Id cursor chain (scanRSS) so a backlog spanning several
// pages is not silently truncated at the first one. Items that fail
// projection (no <account>/status/<id> URL in guid or link) are skipped,
// mirroring the HTML path's rule that an unidentifiable entry never becomes
// a Tweet. An item authored by a handle other than the requested one (case-
// insensitive) is flagged IsRetweet — see the loop below.
func (c *Client) timelineRSS(ctx context.Context, base, handle string, limit, maxPages int, stop func(page []nitter.Tweet) bool) ([]nitter.Tweet, error) {
	raw, err := c.scanRSS(ctx, base, handle, limit, maxPages, stop)
	if err != nil {
		return nil, err
	}
	var tweets []nitter.Tweet
	for _, tw := range raw {
		// Nitter user RSS carries no repost marker, but a retweeted item's
		// guid/link and dc:creator point at the ORIGINAL author (verified
		// live against a real instance: requesting NASA yields NASAhistory
		// status URLs while every self-authored item links NASA). An author
		// handle differing from the requested handle — case-insensitively —
		// therefore flags the tweet as a repost. User timelines only:
		// search/list mix authors by design, and this heuristic must not
		// apply there. RepostedBy stays empty (the feed names no reposter;
		// that display name remains an HTML-path fact). An empty handle
		// (creator-less item) leaves the flag untouched.
		if tw.Author.Handle != "" && !strings.EqualFold(tw.Author.Handle, handle) {
			tw.IsRetweet = true
		}
		tweets = append(tweets, tw)
		if limit > 0 && len(tweets) >= limit {
			break
		}
	}
	return tweets, nil
}

// timelineHTML fetches <base>/<handle>, classifies it (challenge, error
// panel) and parses the timeline, then follows the load-more cursor while a
// cursor exists, the limit is not met and the page budget lasts. Page URLs
// re-encode the cursor extracted by the parser (it arrives URL-decoded).
// maxPages < 0 removes the budget: pagination runs until the upstream stops
// serving a cursor (upstream exhaustion) or repeats a cursor already followed
// (the same guard the RSS scan applies); a runaway chain is otherwise bounded
// by the caller's context cancellation, honored inside the loop.
func (c *Client) timelineHTML(ctx context.Context, base, handle string, limit, maxPages int) ([]nitter.Tweet, error) {
	body, _, err := c.HTTP.Get(ctx, base+"/"+handle, nil)
	if err != nil {
		return nil, err
	}
	if err := html.ClassifyPage(body); err != nil {
		return nil, err
	}
	page, err := html.ParseTimeline(body, base)
	if err != nil {
		return nil, err
	}
	tweets := page.Tweets
	pages := 1
	cursor := page.NextCursor
	// A repeated cursor ends the scan instead of spinning forever: with the
	// unbounded budget (`--max-pages 0` -> -1) a cycling instance would
	// otherwise be asked for the same page until the context is cancelled.
	followed := make(map[string]bool)
	for cursor != "" && (limit <= 0 || len(tweets) < limit) && (maxPages < 0 || pages < maxPages) {
		if followed[cursor] {
			break
		}
		followed[cursor] = true
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, _, err := c.HTTP.Get(ctx, base+"/"+handle+"?cursor="+url.QueryEscape(cursor), nil)
		if err != nil {
			return nil, err
		}
		if err := html.ClassifyPage(body); err != nil {
			return nil, err
		}
		page, err := html.ParseTimeline(body, base)
		if err != nil {
			return nil, err
		}
		tweets = append(tweets, page.Tweets...)
		pages++
		cursor = page.NextCursor
	}
	// A page can overshoot the limit (its items arrive as a batch); the
	// contract is at most Limit tweets.
	if limit > 0 && len(tweets) > limit {
		tweets = tweets[:limit]
	}
	return tweets, nil
}
