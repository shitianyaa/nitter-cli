// Package pipeline implements the nitter.pipeline/v1 machine protocol:
// typed NDJSON envelopes, output-mode resolution and in-place error
// envelopes. It is the shared machine-output path for all data commands.
//
// The protocol mirrors javdb-cli's javdb.pipeline/v1: every machine-readable
// record is one Envelope on one line, batches may replace individual records
// with error envelopes instead of aborting, and kind values are v1-stable —
// additive-only, never renamed or repurposed.
//
// Callers own the streams: WriteEnvelope never touches anything but the
// io.Writer it is given, and the (empty) hint for empty result sets is the
// caller's job (stderr), keeping this package free of user-copy policy.
package pipeline

import (
	"errors"
	"io"
	"syscall"

	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
)

// Mode is the resolved output mode of one command run.
type Mode int

const (
	// ModeHuman is the interactive table rendering (default on a TTY).
	ModeHuman Mode = iota
	// ModeText is the script-friendly text rendering. ResolveOutputMode no
	// longer resolves it (the pipe default became ModeNDJSON in M10), but it
	// stays a valid mode value for direct batch callers.
	ModeText
	// ModeJSON is the whole-result JSON document (--json).
	ModeJSON
	// ModeNDJSON is one nitter.pipeline/v1 envelope per record (--ndjson).
	ModeNDJSON
)

// Schema is the fixed schema constant of every envelope line.
const Schema = "nitter.pipeline/v1"

// Kind values of the v1 protocol (additive-only).
const (
	KindTweet          = "tweet"
	KindUser           = "user"
	KindProfile        = "profile"
	KindList           = "list"
	KindInstanceReport = "instance_report"
	KindSeenEntry      = "seen_entry"
	KindMedia          = "media"
	KindDownload       = "download"
	KindTrend          = "trend"
	KindError          = "error"
)

// ResolveOutputMode resolves the output mode from the --ndjson/--json flags
// and the stdout TTY state. The flags are mutually exclusive (returned as a
// *invocation.UsageError, so exit 2); an explicit flag always wins; without
// flags the decision is the M10 pipe default: a TTY gets ModeHuman (the human
// text table, unchanged), while a non-TTY stdout — a pipe or a redirect —
// gets ModeNDJSON, so piped pipelines receive nitter.pipeline/v1 envelopes
// without any flag (the declared 0.6.0 behavior change; watch opts out at
// its call site).
//
// ModeText is no longer resolved here; it remains a valid mode value for
// direct batch callers that render the text rows themselves.
func ResolveOutputMode(ndjson, json bool, outIsTTY bool) (Mode, error) {
	switch {
	case ndjson && json:
		return ModeHuman, invocation.Usagef("--json and --ndjson are mutually exclusive")
	case ndjson:
		return ModeNDJSON, nil
	case json:
		return ModeJSON, nil
	case outIsTTY:
		return ModeHuman, nil
	default:
		return ModeNDJSON, nil
	}
}

// Meta carries per-record provenance. Empty fields are omitted; FetchedAt is
// an RFC3339 UTC timestamp. Input holds the batch input a record (typically
// an error envelope) refers to. Filter names the media filter in effect when
// the record was fetched ("media_only" or a --media-type value); the key is
// omitted when no media filter applied.
type Meta struct {
	Source    string `json:"source,omitempty"`
	Instance  string `json:"instance,omitempty"`
	FetchedAt string `json:"fetched_at,omitempty"`
	Filter    string `json:"filter,omitempty"`
	Input     string `json:"input,omitempty"`
}

// Envelope is one nitter.pipeline/v1 record. Schema must be set to Schema
// by the caller (WriteEnvelope does not silently fix it up); ID is the
// record's natural identity (tweet id, instance URL, …) and Data the typed
// payload. A nil Meta omits the meta key.
type Envelope struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Data   any    `json:"data"`
	Meta   *Meta  `json:"meta,omitempty"`
}

// WriteEnvelope marshals env as exactly one JSON line (jsonx semantics: no
// HTML escaping, one trailing newline) and writes it with a single Write.
func WriteEnvelope(w io.Writer, env Envelope) error {
	b, err := jsonx.MarshalLine(env)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// WriteErrorEnvelope writes the in-place error envelope for one batch
// record: {schema,kind:"error",data:{command,stage,code,message},meta:{input}}.
//
// The input value and message end up in machine-readable output — the caller
// is responsible for never passing secrets (tokens, proxy credentials, …)
// and for redacting URLs per the repo's redaction rules.
func WriteErrorEnvelope(w io.Writer, command, stage, code, input, message string) error {
	return WriteEnvelope(w, Envelope{
		Schema: Schema,
		Kind:   KindError,
		Data: TypedErrorEnvelope{
			Command: command,
			Stage:   stage,
			Code:    code,
			Message: message,
		},
		Meta: &Meta{Input: input},
	})
}

// IsBrokenPipe reports whether err is a broken pipe on stdout: the reader
// hung up mid-stream (head/tail on a pipe). Per the pixiv sigpipe policy the
// consuming side closing the stream is not a run failure — the caller treats
// it as a graceful exit 0 (and, for the watch command's produce-then-persist
// discipline, skips the state write so the next round re-pushes; 宁重勿丢).
// Windows may surface broken pipes as a different errno (ERROR_BROKEN_PIPE),
// so this portable check is best-effort.
func IsBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}
