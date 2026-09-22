package result

import (
	"strconv"
	"strings"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// TrendRow is the human row projection of one trending topic:
//
//	#\tTREND TOPIC\tCONTEXT\tTWEETS
type TrendRow struct {
	Rank       int
	Name       string
	Context    string
	TweetCount int
}

// TrendTableHeader is the tab-separated header line for trends output.
func TrendTableHeader() string {
	return "#\tTREND TOPIC\tCONTEXT\tTWEETS"
}

// Line renders the tab-separated row.
func (r TrendRow) Line() string {
	tweetsStr := "-"
	if r.TweetCount > 0 {
		tweetsStr = strconv.Itoa(r.TweetCount)
	}
	contextStr := r.Context
	if contextStr == "" {
		contextStr = "-"
	}
	cells := []string{
		strconv.Itoa(r.Rank),
		singleLine(r.Name),
		singleLine(contextStr),
		tweetsStr,
	}
	return strings.Join(cells, "\t")
}

// TrendRows projects trends into rows in input order.
func TrendRows(ts []nitter.Trend) []TrendRow {
	rows := make([]TrendRow, 0, len(ts))
	for _, t := range ts {
		rows = append(rows, TrendRow{
			Rank:       t.Rank,
			Name:       t.Name,
			Context:    t.Context,
			TweetCount: t.TweetCount,
		})
	}
	return rows
}
