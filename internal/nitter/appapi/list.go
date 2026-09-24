package appapi

// List-timeline acquisition: the implementation behind `nitter list`. A
// Nitter list page shares the timeline markup (div.timeline-item plus a
// load-more cursor), so the fetch pipeline is the search page pipeline with
// a different URL shape — no RSS layer is involved:
//
//	GET <base>/i/lists/<listID>[?cursor=<escaped>]
//
// listID semantics: Nitter accepts numeric list IDs and some refs. This
// layer validates only that the ID is non-empty and URL-path-safe (no
// whitespace, ?, # or / — those would break the path or smuggle a query)
// and passes it through unchanged otherwise. A genuinely empty list page is
// a success with zero tweets: the "list not yet ingested by this Nitter"
// case is indistinguishable server-side and must not be invented into an
// error here.
//
// Instance rotation is identical to Timeline and Search: the Chooser walks
// the configured instances in order, an instance whose whole fetch fails is
// marked failed (cooldown) while the next one is tried, a success is marked
// successful, and context cancellation aborts rotation with the context
// error. The method returns the base URL of the instance that produced the
// result so callers can stamp NDJSON provenance (the same deviation
// Timeline established: provenance is only known here).

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// opListTimeline is the Op stamped on ListTimeline's own errors.
const opListTimeline = "appapi.ListTimeline"

// validListID reports whether a list ID is non-empty and URL-path-safe:
// whitespace, "?", "#" and "/" are rejected — they would corrupt the
// request path or smuggle query/fragment components. Everything else
// (numeric IDs, Nitter list refs) passes through.
func validListID(listID string) bool {
	if strings.TrimSpace(listID) == "" {
		return false
	}
	return !strings.ContainsAny(listID, " \t\n\r\v\f?#/")
}

// ListTimeline fetches one list's timeline. The list ID must be non-empty
// and URL-path-safe (KindInvalidArg otherwise) — the check happens BEFORE
// any rotation or network. The returned string is the base URL of the
// instance that produced the result ("" only on error).
func (c *Client) ListTimeline(ctx context.Context, listID string, opts PageOptions) ([]nitter.Tweet, string, error) {
	if !validListID(listID) {
		return nil, "", nitter.Errorf(nitter.KindInvalidArg, opListTimeline,
			"list ID must be non-empty and free of whitespace, ?, # and /")
	}
	if c.HTTP == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opListTimeline, "no transport wired into the appapi client")
	}
	if c.Chooser == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opListTimeline, "no instance chooser wired into the appapi client")
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
			return nil, "", nitter.Errorf(nitter.KindUnavailable, opListTimeline, "all instances failed")
		}
		tried[base] = true
		tweets, err := c.listFromInstance(ctx, base, listID, opts.Limit, maxPages)
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

// listFromInstance runs the paginated list fetch against one instance: the
// first page <base>/i/lists/<listID>, then the cursor pages while a cursor
// exists, the limit is not met and the page budget lasts — the same bounds
// math as the HTML timeline path. listID is path-escaped defensively even
// though validation already rejected the unsafe characters.
func (c *Client) listFromInstance(ctx context.Context, base, listID string, limit, maxPages int) ([]nitter.Tweet, error) {
	first := base + "/i/lists/" + url.PathEscape(listID)
	body, _, err := c.HTTP.Get(ctx, first, nil)
	if err != nil {
		return nil, err
	}
	tweets, cursor, err := searchParsePage(body, base)
	if err != nil {
		return nil, err
	}
	pages := 1
	// A repeated cursor ends the scan instead of re-requesting the same page
	// until the page budget runs out — the guard the RSS scan and the HTML
	// timeline path apply.
	followed := make(map[string]bool)
	for cursor != "" && (limit <= 0 || len(tweets) < limit) && pages < maxPages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if followed[cursor] {
			break
		}
		followed[cursor] = true
		body, _, err := c.HTTP.Get(ctx, first+"?cursor="+url.QueryEscape(cursor), nil)
		if err != nil {
			return nil, err
		}
		pageTweets, next, err := searchParsePage(body, base)
		if err != nil {
			return nil, err
		}
		tweets = append(tweets, pageTweets...)
		pages++
		cursor = next
	}
	// A page can overshoot the limit (its items arrive as a batch); the
	// contract is at most Limit tweets.
	if limit > 0 && len(tweets) > limit {
		tweets = tweets[:limit]
	}
	return tweets, nil
}
