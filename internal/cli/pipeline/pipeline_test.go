package pipeline_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
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
	t.Run("explicit --ndjson wins over TTY and pipe", func(t *testing.T) {
		if mode, err := pipeline.ResolveOutputMode(true, false, true); err != nil || mode != pipeline.ModeNDJSON {
			t.Fatalf("mode/err = %v/%v, want ModeNDJSON/nil", mode, err)
		}
		if mode, err := pipeline.ResolveOutputMode(true, false, false); err != nil || mode != pipeline.ModeNDJSON {
			t.Fatalf("mode/err = %v/%v, want ModeNDJSON/nil (non-TTY)", mode, err)
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
	t.Run("non-TTY defaults to NDJSON (M10 pipe default)", func(t *testing.T) {
		if mode, err := pipeline.ResolveOutputMode(false, false, false); err != nil || mode != pipeline.ModeNDJSON {
			t.Fatalf("mode/err = %v/%v, want ModeNDJSON/nil", mode, err)
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
	want := `{"schema":"nitter.pipeline/v1","kind":"tweet","id":"1830000000000000001","data":{"text":"a & b < c > d"},"meta":{"source":"rss","instance":"https://nitter.example","fetched_at":"2026-09-12T08:30:00Z"}}` + "\n"
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
	want := `{"schema":"nitter.pipeline/v1","kind":"tweet","data":{"text":"plain"}}` + "\n"
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
	err := pipeline.WriteErrorEnvelope(&buf, "nitter get", "fetch", "instance_unreachable", "lts37200", "all instances failed")
	if err != nil {
		t.Fatalf("WriteErrorEnvelope: %v", err)
	}
	// Shape: {schema,kind:"error",data:{command,stage,code,message},meta:{input}}.
	// The input field is caller-supplied; it must never carry secrets.
	want := `{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"nitter get","stage":"fetch","code":"instance_unreachable","message":"all instances failed"},"meta":{"input":"lts37200"}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("error envelope bytes =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteErrorEnvelopeEmptyInputOmitted(t *testing.T) {
	var buf bytes.Buffer
	if err := pipeline.WriteErrorEnvelope(&buf, "nitter get", "fetch", "no_input", "", "nothing matched"); err != nil {
		t.Fatalf("WriteErrorEnvelope: %v", err)
	}
	want := `{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"nitter get","stage":"fetch","code":"no_input","message":"nothing matched"},"meta":{}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("error envelope bytes =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteDownloadEnvelopeGolden pins the v1 wire shape of the download
// record (M9 Task 4, additive kind): {schema,kind:"download",id,data,meta}
// with id = the absolute on-disk path, the sdk DownloadRecord as data and
// the raw input ref as meta.input. The kind value is frozen — additive-only.
func TestWriteDownloadEnvelopeGolden(t *testing.T) {
	var buf bytes.Buffer
	env := pipeline.Envelope{
		Schema: pipeline.Schema,
		Kind:   pipeline.KindDownload,
		ID:     "/tmp/out/100-1.mp4",
		Data: nitter.DownloadRecord{
			Ref:    "https://x.com/nasa/status/100",
			Path:   "/tmp/out/100-1.mp4",
			Kind:   "video",
			Source: "nitter",
			URL:    "https://cdn.test/x.mp4",
			Bytes:  3,
			SHA256: "ab12cd",
		},
		Meta: &pipeline.Meta{Input: "https://x.com/nasa/status/100"},
	}
	if err := pipeline.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	want := `{"schema":"nitter.pipeline/v1","kind":"download","id":"/tmp/out/100-1.mp4","data":{"ref":"https://x.com/nasa/status/100","path":"/tmp/out/100-1.mp4","kind":"video","source":"nitter","url":"https://cdn.test/x.mp4","bytes":3,"sha256":"ab12cd"},"meta":{"input":"https://x.com/nasa/status/100"}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("download envelope bytes =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteDownloadEnvelopeSkipRow pins the skip row's contract: the sha256
// key is ABSENT (nothing was re-downloaded, no digest is fabricated) while
// bytes carries the existing file's on-disk size.
func TestWriteDownloadEnvelopeSkipRow(t *testing.T) {
	var buf bytes.Buffer
	env := pipeline.Envelope{
		Schema: pipeline.Schema,
		Kind:   pipeline.KindDownload,
		ID:     "/tmp/out/100-1.jpg",
		Data: nitter.DownloadRecord{
			Ref:    "https://x.com/nasa/status/100",
			Path:   "/tmp/out/100-1.jpg",
			Kind:   "image",
			Source: "nitter",
			Bytes:  4096,
		},
		Meta: &pipeline.Meta{Input: "https://x.com/nasa/status/100"},
	}
	if err := pipeline.WriteEnvelope(&buf, env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	want := `{"schema":"nitter.pipeline/v1","kind":"download","id":"/tmp/out/100-1.jpg","data":{"ref":"https://x.com/nasa/status/100","path":"/tmp/out/100-1.jpg","kind":"image","source":"nitter","url":"","bytes":4096},"meta":{"input":"https://x.com/nasa/status/100"}}` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("skip envelope bytes =\n%q\nwant\n%q", got, want)
	}
}
