// Package jsonx holds small JSON encoding helpers shared across internal
// packages. It deliberately works on bytes only: callers own their output
// streams (and the user copy that goes with them).
package jsonx

import (
	"bytes"
	"encoding/json"
)

// MarshalLine encodes v as a single JSON value on one line with exactly one
// trailing newline: the byte contract is "<json>\n", where <json> is compact
// (no indentation, string newlines escaped) and contains no HTML escaping —
// <, > and & are emitted literally. The result is suitable as one NDJSON
// record, or as the payload of a file that must end in a single newline.
func MarshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
