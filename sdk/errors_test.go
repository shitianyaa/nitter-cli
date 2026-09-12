package nitter_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/sdk"
)

func TestKindValuesAreStableStrings(t *testing.T) {
	tests := []struct {
		kind nitter.Kind
		want string
	}{
		{nitter.KindChallenge, "challenge_required"},
		{nitter.KindRateLimited, "rate_limited"},
		{nitter.KindNotFound, "not_found"},
		{nitter.KindUnavailable, "upstream_unavailable"},
		{nitter.KindMalformed, "malformed_upstream_response"},
		{nitter.KindInvalidArg, "invalid_argument"},
		{nitter.KindLocalState, "local_state_error"},
	}
	for _, tc := range tests {
		if got := string(tc.kind); got != tc.want {
			t.Errorf("kind %v = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestErrorfMessageContainsFormattedContent(t *testing.T) {
	err := nitter.Errorf(nitter.KindNotFound, "FetchUser", "user %q not found", "NASA")
	if err.Kind != nitter.KindNotFound {
		t.Errorf("Kind = %v, want %v", err.Kind, nitter.KindNotFound)
	}
	if err.Op != "FetchUser" {
		t.Errorf("Op = %q, want %q", err.Op, "FetchUser")
	}
	msg := err.Error()
	if !strings.Contains(msg, `user "NASA" not found`) {
		t.Errorf("Error() = %q, want it to contain %q", msg, `user "NASA" not found`)
	}
	if !strings.Contains(msg, "FetchUser") {
		t.Errorf("Error() = %q, want it to contain op %q", msg, "FetchUser")
	}
	if !strings.Contains(msg, string(nitter.KindNotFound)) {
		t.Errorf("Error() = %q, want it to contain kind %q", msg, nitter.KindNotFound)
	}
}

func TestErrorsAsRecoversKindAndRetryAfter(t *testing.T) {
	retry := 30 * time.Second
	sdkErr := &nitter.Error{
		Kind:       nitter.KindRateLimited,
		Op:         "FetchTimeline",
		Err:        errors.New("instance returned HTTP 429"),
		RetryAfter: &retry,
	}
	// Two wrapping layers: errors.As must traverse the whole chain.
	wrapped := fmt.Errorf("watch loop: %w", fmt.Errorf("get timeline: %w", sdkErr))

	var target *nitter.Error
	if !errors.As(wrapped, &target) {
		t.Fatalf("errors.As did not recover *nitter.Error from %v", wrapped)
	}
	if target.Kind != nitter.KindRateLimited {
		t.Errorf("Kind = %v, want %v", target.Kind, nitter.KindRateLimited)
	}
	if target.Op != "FetchTimeline" {
		t.Errorf("Op = %q, want %q", target.Op, "FetchTimeline")
	}
	if target.RetryAfter == nil || *target.RetryAfter != retry {
		t.Errorf("RetryAfter = %v, want %v", target.RetryAfter, retry)
	}
}

func TestUnwrapReachesRootCause(t *testing.T) {
	root := errors.New("connection reset by peer")
	err := nitter.Errorf(nitter.KindUnavailable, "FetchUser", "request failed: %w", root)

	first := errors.Unwrap(err)
	if first == nil {
		t.Fatal("Unwrap() returned nil for a constructed *nitter.Error")
	}
	if !errors.Is(first, root) {
		t.Errorf("inner chain lost the root cause: %v", first)
	}
	if !errors.Is(err, root) {
		t.Errorf("errors.Is did not reach the root cause through the chain: %v", err)
	}
}

func TestErrorMessageStableWithoutChain(t *testing.T) {
	err := &nitter.Error{Kind: nitter.KindInvalidArg, Op: "SetConfig"}
	if got, want := err.Error(), "SetConfig: invalid_argument"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	empty := &nitter.Error{}
	if got := empty.Error(); got == "" {
		t.Error("zero-value Error() must not return an empty string")
	}

	nilChain := nitter.Errorf(nitter.KindLocalState, "RunWatch", "watcher already running")
	if nilChain.Err == nil {
		t.Fatal("Errorf must populate Err from the formatted message")
	}
	if got, want := nilChain.Error(), "RunWatch: local_state_error: watcher already running"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
