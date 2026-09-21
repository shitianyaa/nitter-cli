package result_test

import (
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/sdk"
)

func TestTrendRowAndHeader(t *testing.T) {
	header := result.TrendTableHeader()
	if header != "#\tTREND TOPIC\tCONTEXT\tTWEETS" {
		t.Errorf("header = %q, want #\\tTREND TOPIC\\tCONTEXT\\tTWEETS", header)
	}

	trend := nitter.Trend{
		Name:          "#AI",
		Rank:          1,
		Context:       "Tech",
		TweetCount:    50000,
		GroupedTopics: []string{"Tech"},
	}

	rows := result.TrendRows([]nitter.Trend{trend})
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}

	line := rows[0].Line()
	want := "1\t#AI\tTech\t50000"
	if line != want {
		t.Errorf("line = %q, want %q", line, want)
	}

	// Empty tweet count and context renders "-"
	emptyTrend := nitter.Trend{
		Name: "Topic",
		Rank: 2,
	}
	emptyRows := result.TrendRows([]nitter.Trend{emptyTrend})
	emptyLine := emptyRows[0].Line()
	wantEmpty := "2\tTopic\t-\t-"
	if emptyLine != wantEmpty {
		t.Errorf("emptyLine = %q, want %q", emptyLine, wantEmpty)
	}
}
