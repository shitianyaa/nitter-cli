package appapi

import (
	"context"
	"net/url"
	"strings"

	"github.com/shitianyaa/nitter-cli/internal/nitter/rss"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// rssPage is one RSS response: the projected tweets plus the cursor the
// instance offers for the next page.
type rssPage struct {
	tweets     []nitter.Tweet
	nextCursor string
}

// fetchRSSPage reads one RSS page for a path segment ("NASA", "a,b,c",
// "i/lists/123"). The next-page cursor is Nitter's Min-Id response header;
// an absent header means the feed is exhausted, which is also how every RSS
// source without Min-Id behaves — the scan then matches the historical
// single-page read exactly.
func (c *Client) fetchRSSPage(ctx context.Context, base, segment, cursor string) (rssPage, error) {
	target := base + "/" + segment + "/rss"
	if cursor != "" {
		target += "?cursor=" + url.QueryEscape(cursor)
	}
	body, _, headers, err := c.HTTP.GetMeta(ctx, target, nil)
	if err != nil {
		return rssPage{}, err
	}
	items, err := rss.Parse(body)
	if err != nil {
		return rssPage{}, err
	}
	page := rssPage{nextCursor: headerValue(headers, "Min-Id")}
	for _, item := range items {
		tw, err := rss.ItemToTweet(item)
		if err != nil {
			// Unidentifiable items never become tweets (the HTML path's
			// rule); skipping them is not an error.
			continue
		}
		page.tweets = append(page.tweets, tw)
	}
	return page, nil
}

// headerValue returns the first value of a case-insensitive response header.
func headerValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

// scanRSS follows the Min-Id cursor chain for one path segment and returns
// every page's tweets in fetch order (newest page first). It stops at the
// first of: the limit being satisfied, a page without a cursor, a cursor
// already followed (a loop guard — a misbehaving proxy must not spin
// forever), the page budget, or a page for which stop returns true. limit > 0
// is checked BEFORE the next page is requested, so a bounded fetch
// (`nitter user NASA --limit 5`) never spends the page budget. maxPages 0
// means defaultMaxPages; negative is the unbounded contract (the cursor chain
// is then the only bound).
func (c *Client) scanRSS(ctx context.Context, base, segment string, limit, maxPages int, stop func(page []nitter.Tweet) bool) ([]nitter.Tweet, error) {
	if maxPages == 0 {
		maxPages = defaultMaxPages
	}
	var (
		all      []nitter.Tweet
		cursor   string
		followed = make(map[string]bool)
	)
	for pages := 0; maxPages < 0 || pages < maxPages; pages++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := c.fetchRSSPage(ctx, base, segment, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.tweets...)
		if limit > 0 && len(all) >= limit {
			// The caller has enough: requesting the next page would spend a
			// request on items it is about to discard.
			break
		}
		if page.nextCursor == "" || followed[page.nextCursor] {
			break
		}
		followed[page.nextCursor] = true
		cursor = page.nextCursor
		if stop != nil && stop(page.tweets) {
			break
		}
	}
	return all, nil
}
