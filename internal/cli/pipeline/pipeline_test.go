package pipeline_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/twitter-cli/internal/cli/pipeline"
)

func TestResolveOutputMode(t *testing.T) {
	t.Run("both flags are a usage error", func(t *testing.T) {
		mode, err := pipeline.ResolveOutputMode(true, true, true)
		if err == nil {
			t.Fatalf("ResolveOutputMode(true, true, true) = %v, want a mutual-exclusion error", mode)
		}
		var ue *invocation.UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("err = %v, want *invocation.UsageError (exit 2)", err)
		}
	})
	t.Run("explicit --ndjson wins over TTY", func(t *testing.T) {
		if mode, err := pipeline.ResolveOutputMode(true, false, true); err != nil || mode != pipeline.ModeNDJSON {
			t.Fatalf("mode/err = %v/%v, want ModeNDJSON/nil", mode, err)
		}
	})
	t.Run("explicit --json wins over TTY", func(t *testing.T) {
		if mode, err := pipeline.ResolveOutputMode(false, true, true); err != nil || mode != pipeline.ModeJSON {
			t.Fatalf("mode/err = %v/%v, want ModeJSON/nil", mode, err)
		}
		if mode, err := pipeline.ResolveOutputMode(false, true, false); err != nil || mode != pipeline.ModeJSON {
			t.Fatalf("mode/err = %v/%v, want ModeJSON/nil (non-TTY)", mode, err)
		}
	})
	t.Run("TTY defaults to human", func(t *testing.T) {
		if mode, err := pipeline.ResolveOutputMode(false, false, true); err != nil || mode != pipeline.ModeHuman {
			t.Fatalf("mode/err = %v/%v, want ModeHuman/nil", mode, err)
		}
	})
	t.Run("non-TTY defaults to text", func(t *testing.T) {
		if mode, err := pipeline.ResolveOutputMode(false, false, false); err != nil || mode != pipeline.ModeText {
			t.Fatalf("mode/err = %v/%v, want ModeText/nil", mode, err)
		}
	})
}

// sampleData is a fixed-shape data payload so the golden bytes are stable
// (map[string]any would sort keys and obscure the marshaling order).
type sampleData struct {
	Text string `json:"text"`
}

func TestWriteEnvelopeGolden(t *testing.T) {
	var buf bytes.Buffer
	env := pipeline.Envelope{
		Schema: pipeline.Schema,
		Kind:   pipeline.KindTweet,
		ID:     "1830000000000000001",
		Data:   sampleData{Text: "a & b < c > d"},
		Meta: &pipeline.Meta{
			Source:    "rss",
			Instance:  "https://nitter.example",
			FetchedAt: "2026-09-12T08:30:00Z",
		},
	}
	if err := pipeline.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	// Exact key order (schema,kind,id,data,meta), single line, one trailing
	// \n, and no HTML escaping (& < > stay raw — jsonx contract).
	want := `{"schema":"twitter.pipeline/v1","kind":"tweet","id":"1830000000000000001","data":{"text":"a & b < c > d"},"meta":{"source":"rss","instance":"https://nitter.example","fetched_at":"2026-09-12T08:30:00Z"}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("envelope bytes =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteEnvelopeOmitempty(t *testing.T) {
	var buf bytes.Buffer
	env := pipeline.Envelope{
		Schema: pipeline.Schema,
		Kind:   pipeline.KindTweet,
		Data:   sampleData{Text: "plain"},
	}
	if err := pipeline.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	// Empty ID and nil Meta must vanish, key order otherwise unchanged.
	want := `{"schema":"twitter.pipeline/v1","kind":"tweet","data":{"text":"plain"}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("envelope bytes =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteEnvelopeSingleLine(t *testing.T) {
	var buf bytes.Buffer
	env := pipeline.Envelope{
		Schema: pipeline.Schema,
		Kind:   pipeline.KindTweet,
		ID:     "1",
		Data:   sampleData{Text: "line1\nline2"},
	}
	if err := pipeline.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	got := buf.String()
	if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("envelope must be exactly one line with one trailing \\n, got %q", got)
	}
	if !strings.Contains(got, `line1\nline2`) {
		t.Fatalf("interior newline must be JSON-escaped, got %q", got)
	}
}

func TestWriteErrorEnvelopeGolden(t *testing.T) {
	var buf bytes.Buffer
	err := pipeline.WriteErrorEnvelope(&buf, "twitter get", "fetch", "instance_unreachable", "lts37200", "all instances failed")
	if err != nil {
		t.Fatalf("WriteErrorEnvelope: %v", err)
	}
	// Shape: {schema,kind:"error",data:{command,stage,code,message},meta:{input}}.
	// The input field is caller-supplied; it must never carry secrets.
	want := `{"schema":"twitter.pipeline/v1","kind":"error","data":{"command":"twitter get","stage":"fetch","code":"instance_unreachable","message":"all instances failed"},"meta":{"input":"lts37200"}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("error envelope bytes =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteErrorEnvelopeEmptyInputOmitted(t *testing.T) {
	var buf bytes.Buffer
	if err := pipeline.WriteErrorEnvelope(&buf, "twitter get", "fetch", "no_input", "", "nothing matched"); err != nil {
		t.Fatalf("WriteErrorEnvelope: %v", err)
	}
	want := `{"schema":"twitter.pipeline/v1","kind":"error","data":{"command":"twitter get","stage":"fetch","code":"no_input","message":"nothing matched"},"meta":{}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("error envelope bytes =\n%q\nwant\n%q", got, want)
	}
}
