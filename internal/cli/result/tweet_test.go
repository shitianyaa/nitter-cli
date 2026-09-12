package result_test

import (
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/sdk"
)

func TestTweetRowLine(t *testing.T) {
	t.Run("basic line", func(t *testing.T) {
		row := result.TweetRow{ID: "1830", Date: "2026-09-12 10:30", Handle: "nasa", Text: "hello world"}
		want := "1830\t2026-09-12 10:30\t@nasa\thello world"
		if got := row.Line(); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
	t.Run("clean graphic text is never escaped", func(t *testing.T) {
		// SafeLine discipline: escape only when needed. Quotes, ampersands,
		// angle brackets and non-ASCII graphic runes stay raw.
		row := result.TweetRow{ID: "1", Date: "2026-09-12 10:30", Handle: "nasa", Text: `café "☕" & <3 \ok/`}
		want := "1\t2026-09-12 10:30\t@nasa\t" + `café "☕" & <3 \ok/`
		if got := row.Line(); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
	t.Run("embedded tab becomes a space", func(t *testing.T) {
		row := result.TweetRow{ID: "1", Date: "d", Handle: "n", Text: "a\tb"}
		want := "1\td\t@n\ta b"
		if got := row.Line(); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
	t.Run("control characters quote the whole text field", func(t *testing.T) {
		// A newline is a control char beyond \t, so the WHOLE text field is
		// rendered via strconv.QuoteToGraphic (outer quotes included): the
		// row stays one physical line while the escape stays visible.
		row := result.TweetRow{ID: "1", Date: "d", Handle: "n", Text: "first\nsecond"}
		want := "1\td\t@n\t" + `"first\nsecond"`
		if got := row.Line(); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
	t.Run("tab is spaced before quoting", func(t *testing.T) {
		// Tab AND newline: the tab becomes a space first, then the whole
		// field is quoted for the remaining newline.
		row := result.TweetRow{ID: "1", Date: "d", Handle: "n", Text: "a\tb\nc"}
		want := "1\td\t@n\t" + `"a b\nc"`
		if got := row.Line(); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
	t.Run("escape byte is quoted", func(t *testing.T) {
		row := result.TweetRow{ID: "1", Date: "d", Handle: "n", Text: "x\x1b[31my"}
		want := "1\td\t@n\t" + `"x\x1b[31my"`
		if got := row.Line(); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
	t.Run("empty text renders an empty cell", func(t *testing.T) {
		row := result.TweetRow{ID: "1", Date: "d", Handle: "n", Text: ""}
		want := "1\td\t@n\t"
		if got := row.Line(); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
}

func TestTweetRows(t *testing.T) {
	t.Run("fields and UTC date formatting", func(t *testing.T) {
		published := time.Date(2026, 9, 12, 10, 30, 5, 0, time.UTC)
		rows := result.TweetRows([]nitter.Tweet{{
			ID:          "1830",
			Text:        "hello",
			Author:      nitter.Author{Handle: "nasa", Name: "NASA"},
			PublishedAt: published,
		}})
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		want := result.TweetRow{ID: "1830", Date: "2026-09-12 10:30", Handle: "nasa", Text: "hello"}
		if rows[0] != want {
			t.Fatalf("row = %+v, want %+v", rows[0], want)
		}
	})
	t.Run("non-UTC instants render in UTC", func(t *testing.T) {
		// Producers must store UTC, but the projection formats defensively:
		// the same instant in a +08:00 location shows as its UTC wall clock.
		located := time.Date(2026, 9, 12, 18, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
		rows := result.TweetRows([]nitter.Tweet{{ID: "1", PublishedAt: located}})
		if rows[0].Date != "2026-09-12 10:30" {
			t.Fatalf("date = %q, want the UTC wall clock 2026-09-12 10:30", rows[0].Date)
		}
	})
	t.Run("zero PublishedAt renders an empty date cell", func(t *testing.T) {
		rows := result.TweetRows([]nitter.Tweet{{ID: "1", Text: "no date"}})
		if rows[0].Date != "" {
			t.Fatalf("date = %q, want the empty string", rows[0].Date)
		}
	})
	t.Run("order is preserved", func(t *testing.T) {
		rows := result.TweetRows([]nitter.Tweet{
			{ID: "2", PublishedAt: time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)},
			{ID: "1", PublishedAt: time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)},
		})
		if len(rows) != 2 || rows[0].ID != "2" || rows[1].ID != "1" {
			t.Fatalf("rows = %+v, want input order preserved", rows)
		}
	})
	t.Run("empty input projects to no rows", func(t *testing.T) {
		// The (empty) hint is the caller's job (stderr); the result package
		// stays io-free and returns no rows.
		if rows := result.TweetRows(nil); len(rows) != 0 {
			t.Fatalf("rows = %+v, want none", rows)
		}
	})
}
