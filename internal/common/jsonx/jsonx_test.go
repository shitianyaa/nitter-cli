package jsonx_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/common/jsonx"
)

func TestMarshalLineNoHTMLEscapingSingleTrailingNewline(t *testing.T) {
	type doc struct {
		HTML string    `json:"html"`
		N    int       `json:"n"`
		When time.Time `json:"when"`
	}
	in := doc{
		HTML: `<a href="/x">&amp;"quoted"</a>`,
		N:    3,
		When: time.Date(2026, 9, 12, 8, 30, 0, 0, time.UTC),
	}

	got, err := jsonx.MarshalLine(in)
	if err != nil {
		t.Fatalf("MarshalLine: %v", err)
	}

	want := `{"html":"<a href=\"/x\">&amp;\"quoted\"</a>","n":3,"when":"2026-09-12T08:30:00Z"}` + "\n"
	if string(got) != want {
		t.Errorf("MarshalLine output mismatch:\n got  %q\n want %q", got, want)
	}

	// Byte contract: exactly one trailing newline, no embedded newlines.
	if c := bytes.Count(got, []byte("\n")); c != 1 {
		t.Errorf("newline count = %d, want exactly 1", c)
	}
	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Error("output must end with a newline")
	}

	// The value must still decode to the original document.
	var back doc
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if back.HTML != in.HTML || back.N != in.N || !back.When.Equal(in.When) {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", back, in)
	}
}

func TestMarshalLineMultiLineValueStaysSingleLine(t *testing.T) {
	// A multi-line string value must stay on one encoded line (escaped),
	// with the only literal newline being the trailing one.
	got, err := jsonx.MarshalLine(map[string]string{"text": "line1\nline2"})
	if err != nil {
		t.Fatalf("MarshalLine: %v", err)
	}
	if c := bytes.Count(got, []byte("\n")); c != 1 {
		t.Errorf("newline count = %d, want exactly 1 (output %q)", c, got)
	}
	if !bytes.Contains(got, []byte(`line1\nline2`)) {
		t.Errorf("embedded newline must be escaped, got %q", got)
	}
}
