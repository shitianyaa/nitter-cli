package appapi

// Search acquisition: the implementation behind `nitter search`. The Nitter
// search page reuses the timeline markup (div.timeline-item plus a load-more
// cursor), so the fetch pipeline is Timeline's HTML branch with a different
// URL shape — no RSS layer is involved:
//
//	GET <base>/search?f=<mode>&q=<url-escaped query>[&cursor=<escaped>]
//
// <mode> comes from PageOptions.Sort: "top" selects the popular-results feed
// (f=top), everything else — "" and "latest" — the default newest-first feed
// (f=tweets). The mode is carried on every page request, so pagination never
// drifts back to the default ordering.
//
// Query semantics are pass-through (the reference plugin's query_kind
// semantics — "#" prefix = tag, "@"/"from:" = user search, else phrase —
// belong to Nitter's own query parser): this layer validates only that the
// query is non-empty after trimming and URL-escapes it unchanged.
//
// Instance rotation is identical to Timeline: the Chooser walks the
// configured instances in order, an instance whose whole fetch fails is
// marked failed (cooldown) while the next one is tried, a success is marked
// successful, and context cancellation aborts rotation with the context
// error. The method returns the base URL of the instance that produced the
// result so callers can stamp NDJSON provenance (the same deviation Timeline
// established: provenance is only known here).

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/shitianyaa/nitter-cli/internal/nitter/html"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// opSearch is the Op stamped on Search's own errors.
const opSearch = "appapi.Search"

// Search fetches one search result timeline. The query must be non-empty
// after trimming (KindInvalidArg otherwise) — the check happens BEFORE any
// rotation or network. opts.Sort selects the feed ("latest"/"" = newest
// first, "top" = popular results) and is validated here as well, so a bad
// value fails as KindInvalidArg instead of reaching an instance (and never
// marks one failed). The returned string is the base URL of the instance
// that produced the result ("" only on error).
func (c *Client) Search(ctx context.Context, query string, opts PageOptions) ([]nitter.Tweet, string, error) {
	if strings.TrimSpace(query) == "" {
		return nil, "", nitter.Errorf(nitter.KindInvalidArg, opSearch, "query must not be empty")
	}
	// Validate the sort BEFORE rotation: a bad value must not be reported as
	// an instance failure (which would cool the instance down). The
	// per-fetch call in searchFromInstance is the same pure check, kept as
	// the backstop for direct callers.
	if _, err := searchSortMode(opts.Sort); err != nil {
		return nil, "", err
	}
	if c.HTTP == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opSearch, "no transport wired into the appapi client")
	}
	if c.Chooser == nil {
		return nil, "", nitter.Errorf(nitter.KindLocalState, opSearch, "no instance chooser wired into the appapi client")
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
			return nil, "", nitter.Errorf(nitter.KindUnavailable, opSearch, "all instances failed")
		}
		tried[base] = true
		tweets, err := c.searchFromInstance(ctx, base, query, opts, maxPages)
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

// searchFromInstance runs the paginated search fetch against one instance:
// the first page <base>/search?f=<mode>&q=<escaped query>, then the cursor
// pages (<…>&cursor=<escaped cursor>) while a cursor exists, the limit is
// not met and the page budget lasts — the same bounds math as the HTML
// timeline path. <mode> comes from opts.Sort and is re-validated here (the
// pure searchSortMode check), so the fetch never degrades silently to a
// different ordering.
func (c *Client) searchFromInstance(ctx context.Context, base, query string, opts PageOptions, maxPages int) ([]nitter.Tweet, error) {
	mode, err := searchSortMode(opts.Sort)
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	first := base + "/search?f=" + mode + "&q=" + url.QueryEscape(query)
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
		body, _, err := c.HTTP.Get(ctx, first+"&cursor="+url.QueryEscape(cursor), nil)
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

// searchSortMode maps PageOptions.Sort onto Nitter's `f=` parameter: "" and
// "latest" (case-insensitive) are the default newest-first feed ("tweets"),
// "top" is the popular-results feed. Every other value is KindInvalidArg
// naming the offending value — the ordering is never silently changed to a
// default.
func searchSortMode(sort string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "", "latest":
		return "tweets", nil
	case "top":
		return "top", nil
	default:
		return "", nitter.Errorf(nitter.KindInvalidArg, opSearch, "invalid sort %q; must be latest or top", sort)
	}
}

// searchParsePage classifies and parses one search page: ClassifyPage first
// (challenge/error panels), then the shared ParseTimeline (the search page
// carries the same timeline-item markup as user and list pages).
func searchParsePage(body []byte, base string) ([]nitter.Tweet, string, error) {
	if err := html.ClassifyPage(body); err != nil {
		return nil, "", err
	}
	page, err := html.ParseTimeline(body, base)
	if err != nil {
		return nil, "", err
	}
	return page.Tweets, page.NextCursor, nil
}
