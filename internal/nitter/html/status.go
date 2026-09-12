package html

// Single-status page parsing (the implementation detail behind
// `twitter get`): a Nitter status page (/<user>/status/<id>) is a
// conversation view — the focused status plus thread/reply context and, when
// the status quotes another tweet, a .quote subtree inside the focused item.
// ParseStatus projects the FOCUSED status into the sdk Tweet shape:
//
//   - the focused status is the timeline-item Nitter renders its
//     interaction-stats row (.tweet-stats) on — thread ancestors and
//     replies do not carry one. Fallback for minimal pages without a stats
//     row: exactly ONE timeline-item with a tweet-content (then the main
//     status is unambiguous). Anything else — no items, several candidate
//     items without a stats row — is KindMalformed rather than a guess.
//   - identity (user + status id) comes from the item's own status link
//     (tweetLinkRe); an item without one is KindMalformed — identity is
//     never fabricated.
//   - text, date, media and retweet detection reuse the timeline item
//     extraction on the quote-masked clone, so quoted content never leaks
//     into the main status; the .quote subtree itself is summarized into
//     Tweet.Quote (id/url/text/author only).
//   - interaction counts are NOT extracted: the models contract forbids
//     fabricating data this projection does not carry, so they stay zero
//     even though the markup renders them.
//
// Malformed/no content → KindMalformed; goquery repairs any malformed
// markup, so structural absence (not garbage bytes) is what surfaces here.

import (
	"bytes"

	"github.com/PuerkitoBio/goquery"

	"github.com/shitianyaa/twitter-cli/internal/nitter/text"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// opParseStatus is the Op stamped on ParseStatus's own errors.
const opParseStatus = "html.ParseStatus"

// ParseStatus parses one single-status page against the instance base (used
// to absolutize media URLs, as in ParseTimeline).
func ParseStatus(body []byte, instance string) (twitter.Tweet, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		// Unreachable for in-memory bytes (the HTML parser is error-tolerant
		// and a bytes.Reader cannot fail); kept explicit over silent swallowing.
		return twitter.Tweet{}, twitter.Errorf(twitter.KindMalformed, opParseStatus, "decode page: %w", err)
	}
	main := selectMainItem(doc)
	if main == nil {
		return twitter.Tweet{}, twitter.Errorf(twitter.KindMalformed, opParseStatus,
			"status page carries no identifiable main status")
	}
	clone := maskQuoteSubtrees(main)
	user, id, ok := locateItem(clone)
	if !ok {
		return twitter.Tweet{}, twitter.Errorf(twitter.KindMalformed, opParseStatus,
			"status page carries no status identity")
	}
	tw := buildTweet(clone, user, id, instance)
	tw.Quote = extractQuote(main)
	return tw, nil
}

// selectMainItem finds the focused status of a conversation page: the first
// timeline-item carrying a .tweet-stats row (Nitter renders the stats row
// only on the focused status), falling back to a page whose single
// content-carrying item is unambiguous. nil when the main status cannot be
// identified without guessing.
func selectMainItem(doc *goquery.Document) *goquery.Selection {
	items := doc.Find("div.timeline-item")
	if items.Length() == 0 {
		return nil
	}
	withStats := items.FilterFunction(func(_ int, s *goquery.Selection) bool {
		return s.Find(".tweet-stats").Length() > 0
	})
	if withStats.Length() > 0 {
		return withStats.First()
	}
	withContent := items.FilterFunction(func(_ int, s *goquery.Selection) bool {
		return s.Find(".tweet-content").Length() > 0
	})
	if withContent.Length() == 1 {
		return withContent.First()
	}
	return nil
}

// extractQuote summarizes the main status's .quote subtree (the quoted
// tweet) into the sdk Quoted shape: identity and author handle from the
// quote's own status link (tweetLinkRe, the same never-fabricate rule), text
// from the first .quote-text (Nitter's quoted-body element) with a
// div.tweet-content fallback, folded through text.CleanHTML on a
// nested-quote-masked clone. A quote without an identifiable status link
// leaves Tweet.Quote nil — a partial quote is never invented.
func extractQuote(main *goquery.Selection) *twitter.Quoted {
	q := main.Find(".quote").First()
	if q.Length() == 0 {
		return nil
	}
	user, id, ok := locateItem(q)
	if !ok {
		return nil
	}
	quoted := &twitter.Quoted{
		ID:     id,
		URL:    "https://x.com/" + user + "/status/" + id,
		Author: twitter.Author{Handle: user},
	}
	// Text: Nitter's own quoted-body element (.quote-text), taken from the
	// unmasked subtree — the quote's own body element is the first
	// .quote-text in document order even when the quote nests another quote,
	// and the quote-masker would remove it (its class carries the "quote-"
	// token the masker keys on). Forks that render the quoted body as
	// div.tweet-content instead take the masked-clone fallback, where nested
	// quote containers are removed so their bodies never leak into the
	// summary.
	if body := q.Find(".quote-text").First(); body.Length() > 0 {
		if outer, err := goquery.OuterHtml(body); err == nil {
			quoted.Text = text.CleanHTML(outer)
		}
		return quoted
	}
	qclone := maskQuoteSubtrees(q)
	if body := qclone.Find("div.tweet-content").First(); body.Length() > 0 {
		if outer, err := goquery.OuterHtml(body); err == nil {
			quoted.Text = text.CleanHTML(outer)
		}
	}
	return quoted
}
