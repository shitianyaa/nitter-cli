package appapi

// User timeline acquisition: the implementation behind `twitter user` (and
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
// Unauthenticated: instance credentials are not wired to the transport in the
// MVP (see probe.go). Context cancellation is honored between and during
// requests: a canceled or deadline-exceeded fetch aborts rotation and
// surfaces the context error instead of moving on to the next instance.

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/shitianyaa/twitter-cli/internal/nitter/html"
	"github.com/shitianyaa/twitter-cli/internal/nitter/rss"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// opTimeline is the Op stamped on Timeline's own errors.
const opTimeline = "appapi.Timeline"

// defaultMaxPages is the HTML pagination cap when PageOptions.MaxPages is 0.
const defaultMaxPages = 5

// PageOptions bounds one Timeline fetch.
type PageOptions struct {
	// Limit is the maximum number of tweets to return; 0 (or negative) means
	// all — still bounded by MaxPages pages of HTML output. The RSS feed is
	// single-page: its items are collected up to the limit.
	Limit int
	// MaxPages caps how many HTML timeline pages are fetched on the HTML
	// path (the first page counts). 0 (or negative) → defaultMaxPages.
	MaxPages int
}

// handleRe is the X handle contract, enforced before any network: 1-15
// letters, digits or underscores.
var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

// Timeline fetches a user timeline. The handle is validated against the
// X handle contract BEFORE any rotation or network (KindInvalidArg
// otherwise). The returned string is the base URL of the instance that
// produced the result ("" only on error).
func (c *Client) Timeline(ctx context.Context, handle string, opts PageOptions) ([]twitter.Tweet, string, error) {
	if !handleRe.MatchString(handle) {
		return nil, "", twitter.Errorf(twitter.KindInvalidArg, opTimeline,
			"handle must be 1-15 letters, digits or underscores (without the @)")
	}
	if c.HTTP == nil {
		return nil, "", twitter.Errorf(twitter.KindLocalState, opTimeline, "no transport wired into the appapi client")
	}
	if c.Chooser == nil {
		return nil, "", twitter.Errorf(twitter.KindLocalState, opTimeline, "no instance chooser wired into the appapi client")
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = defaultMaxPages
	}

	tried := make(map[string]bool)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		base, markSuccess, markFailure, err := c.Chooser.Pick()
		if err != nil {
			// Nothing configured, or every instance is cooling down. If we
			// already attempted someone, their failure is the better report;
			// otherwise pass the chooser's answer through.
			if lastErr != nil {
				return nil, "", lastErr
			}
			return nil, "", err
		}
		if tried[base] {
			// Every configured instance has been attempted (a disabled
			// cooldown makes Pick repeat). A previous attempt must have
			// failed for us to still be looping; the guard keeps the
			// contract exact even if that invariant ever breaks.
			if lastErr != nil {
				return nil, "", lastErr
			}
			return nil, "", twitter.Errorf(twitter.KindUnavailable, opTimeline, "all instances failed")
		}
		tried[base] = true
		tweets, err := c.timelineFromInstance(ctx, base, handle, opts.Limit, maxPages)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// The caller gave up: abort instead of rotating on.
				return nil, "", err
			}
			markFailure()
			lastErr = err
			continue
		}
		markSuccess()
		return tweets, base, nil
	}
}

// timelineFromInstance runs the layered fetch against one instance: RSS
// first; the HTML user page (paginated) when the feed fails or yields zero
// tweets (R15: both conditions trigger the fallback). The fallback's error,
// when it too fails, is the attempt's error — the last stage is the most
// informative. An empty RSS feed followed by an empty HTML page is a success
// with zero tweets, never an error.
func (c *Client) timelineFromInstance(ctx context.Context, base, handle string, limit, maxPages int) ([]twitter.Tweet, error) {
	tweets, err := c.timelineRSS(ctx, base, handle, limit)
	if err == nil && len(tweets) > 0 {
		return tweets, nil
	}
	return c.timelineHTML(ctx, base, handle, limit, maxPages)
}

// timelineRSS fetches and projects <base>/<handle>/rss. The feed is
// single-page: items are collected up to the limit. Items that fail
// projection (no <account>/status/<id> URL in guid or link) are skipped,
// mirroring the HTML path's rule that an unidentifiable entry never becomes
// a Tweet. An item authored by a handle other than the requested one (case-
// insensitive) is flagged IsRetweet — see the loop below.
func (c *Client) timelineRSS(ctx context.Context, base, handle string, limit int) ([]twitter.Tweet, error) {
	body, _, err := c.HTTP.Get(ctx, base+"/"+handle+"/rss", nil)
	if err != nil {
		return nil, err
	}
	items, err := rss.Parse(body)
	if err != nil {
		return nil, err
	}
	var tweets []twitter.Tweet
	for _, item := range items {
		tw, err := rss.ItemToTweet(item)
		if err != nil {
			continue
		}
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
func (c *Client) timelineHTML(ctx context.Context, base, handle string, limit, maxPages int) ([]twitter.Tweet, error) {
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
	for cursor != "" && (limit <= 0 || len(tweets) < limit) && pages < maxPages {
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
