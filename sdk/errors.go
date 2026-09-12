// Package nitter is the public SDK for nitter-cli. It is the only public
// capability surface of this module: CLI commands consume it, while the
// packages under internal/ remain private implementation details.
//
// Redaction contract: values flowing through this package's error chain —
// the *Error struct itself and every wrapped error reachable via Unwrap —
// must never contain credentials, URL query strings, request headers or
// response bodies. Producers classify and describe failures with stable
// kinds, operations and short static context only; anything sensitive stays
// in logs owned by the transport layer, never in errors handed to callers.
//
// This file freezes the v1 error contract: exported identifiers may only be
// extended additively (see Kind).
package nitter

import (
	"fmt"
	"strings"
	"time"
)

// Kind is a stable, machine-readable error class. Values may only be added,
// never removed, renamed or reused (v1 contract).
type Kind string

const (
	KindChallenge   Kind = "challenge_required"
	KindRateLimited Kind = "rate_limited"
	KindNotFound    Kind = "not_found"
	KindUnavailable Kind = "upstream_unavailable"
	KindMalformed   Kind = "malformed_upstream_response"
	KindInvalidArg  Kind = "invalid_argument"
	KindLocalState  Kind = "local_state_error"
)

// Error carries the redaction contract: neither this struct nor its wrapped
// chain may contain credentials, URL query strings, request headers or
// response bodies.
type Error struct {
	Kind Kind
	Op   string
	Err  error

	// RetryAfter is set only for KindRateLimited, sourced from the
	// instance response headers.
	RetryAfter *time.Duration
}

// Error returns a stable, redacted message of the form
// "<op>: <kind>: <chain>", omitting empty parts. It adds no context of its
// own, so its only free-text input is the wrapped chain — which producers
// must keep free of credentials, query strings, headers and response bodies.
func (e *Error) Error() string {
	parts := make([]string, 0, 3)
	if e.Op != "" {
		parts = append(parts, e.Op)
	}
	if e.Kind != "" {
		parts = append(parts, string(e.Kind))
	}
	if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	if len(parts) == 0 {
		return "nitter: unknown error"
	}
	return strings.Join(parts, ": ")
}

// Unwrap exposes the wrapped chain so errors.Is, errors.As and callers can
// reach the root cause. It is part of the redaction contract: every error
// reachable from here must obey the same rules as *Error itself.
func (e *Error) Unwrap() error {
	return e.Err
}

// Errorf builds a *nitter.Error whose chain starts with the formatted
// message. The format string and arguments must satisfy the redaction
// contract: never pass credentials, URL query strings, request headers or
// response bodies as arguments. Use %w in format to attach a cause error.
func Errorf(kind Kind, op string, format string, args ...any) *Error {
	return &Error{
		Kind: kind,
		Op:   op,
		Err:  fmt.Errorf(format, args...),
	}
}
