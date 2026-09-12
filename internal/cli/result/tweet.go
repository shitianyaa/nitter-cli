package result

import (
	"strconv"
	"strings"
	"time"

	"github.com/shitianyaa/twitter-cli/sdk"
)

// tweetDateLayout is the human date cell precision: minutes, UTC.
const tweetDateLayout = "2006-01-02 15:04"

// TweetRow is the human row projection of one status for the table commands:
//
//	<ID>\t<YYYY-MM-DD HH:MM>\t@<handle>\t<single-line text>
//
// Date is pre-formatted (see TweetRows); Handle is the bare author handle
// (no leading @ — Line adds it); Text may still contain control characters,
// Line flattens it (see singleLine) so one row is always one physical line.
type TweetRow struct {
	ID     string
	Date   string
	Handle string
	Text   string
}

// Line renders the tab-separated row. Only the text cell is sanitized: an
// embedded tab becomes a space, and if the text contains any control
// character beyond that, the WHOLE text cell is rendered via
// strconv.QuoteToGraphic (outer quotes included) so the row cannot break the
// one-record-per-line protocol while the escape stays visible — the pixiv
// SafeLine discipline of escaping only when needed.
func (r TweetRow) Line() string {
	cells := []string{r.ID, r.Date, "@" + r.Handle, singleLine(r.Text)}
	return strings.Join(cells, "\t")
}

// TweetRows projects statuses into rows in input order. The date cell is
// formatted here (the caller does not repeat the policy): PublishedAt in UTC
// at minute precision per tweetDateLayout; a zero PublishedAt renders an
// empty cell (the source did not carry a date — never fabricated). Text is
// passed through unmodified; sanitization happens at render time in Line.
//
// An empty input projects to no rows: nothing goes to stdout, and the
// caller prints the (empty) hint to stderr — this package stays io-free.
func TweetRows(ts []twitter.Tweet) []TweetRow {
	rows := make([]TweetRow, 0, len(ts))
	for _, t := range ts {
		rows = append(rows, TweetRow{
			ID:     t.ID,
			Date:   formatTweetDate(t.PublishedAt),
			Handle: t.Author.Handle,
			Text:   t.Text,
		})
	}
	return rows
}

// singleLine flattens a tweet body for one-row-per-line terminal output:
// tabs become spaces; any other control (non-graphic) rune switches the
// whole field to strconv.QuoteToGraphic, which escapes newlines, ESC and
// friends while keeping graphic Unicode (including the outer text's quotes
// and backslashes on clean input) readable.
func singleLine(text string) string {
	s := strings.ReplaceAll(text, "\t", " ")
	for _, r := range s {
		if !strconv.IsGraphic(r) {
			return strconv.QuoteToGraphic(s)
		}
	}
	return s
}

// formatTweetDate renders the date cell: UTC wall clock at minute precision,
// empty for a zero time.
func formatTweetDate(published time.Time) string {
	if published.IsZero() {
		return ""
	}
	return published.UTC().Format(tweetDateLayout)
}
