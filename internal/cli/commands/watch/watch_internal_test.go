package watch

// Unit pins for the error envelope's `code` semantics (v1 wire contract):
// reportSourceError must stamp the SDK error Kind of the failure — extracted
// with errors.As, so wrapped *nitter.Error values classify too — and fall
// back to "error" for anything not a classified SDK error. The source kind
// ("user"/"tag"/"list") must never appear as the code; it is already visible
// in meta.input. The integration-level happy path (500 → upstream_unavailable)
// is covered by TestWatchFailedSourceIsIsolated in watch_test.go; through the
// real fetch chain every failure is a *nitter.Error, so the fallback is only
// reachable at this unit level.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	watchengine "github.com/shitianyaa/nitter-cli/internal/watch"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
)

// errorCode runs reportSourceError in NDJSON mode against buf and returns the
// decoded envelope's data.code.
func errorCode(t *testing.T, src watchengine.Source, err error) string {
	t.Helper()
	var buf bytes.Buffer
	s := &invocation.Streams{Out: &buf, Err: &bytes.Buffer{}}
	if werr := reportSourceError(s, pipeline.ModeNDJSON, src, src.Key(), err); werr != nil {
		t.Fatalf("reportSourceError: %v", werr)
	}
	var env struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if uerr := json.Unmarshal(buf.Bytes(), &env); uerr != nil {
		t.Fatalf("decode envelope %q: %v", buf.String(), uerr)
	}
	return env.Data.Code
}

// TestReportSourceErrorCodeClassifiesSDKKind.
func TestReportSourceErrorCodeClassifiesSDKKind(t *testing.T) {
	src := watchengine.Source{Kind: watchengine.KindUser, Ref: "NASA"}

	t.Run("direct nitter error", func(t *testing.T) {
		err := nitter.Errorf(nitter.KindRateLimited, "op", "slow down")
		if got := errorCode(t, src, err); got != string(nitter.KindRateLimited) {
			t.Errorf("code = %q, want %q", got, nitter.KindRateLimited)
		}
	})

	t.Run("wrapped nitter error", func(t *testing.T) {
		inner := nitter.Errorf(nitter.KindChallenge, "op", "login required")
		err := fmt.Errorf("fetch user:NASA: %w", inner)
		if got := errorCode(t, src, err); got != string(nitter.KindChallenge) {
			t.Errorf("code = %q, want %q (errors.As through the wrap)", got, nitter.KindChallenge)
		}
	})

	t.Run("unclassified error falls back", func(t *testing.T) {
		if got := errorCode(t, src, errors.New("boom")); got != "error" {
			t.Errorf("code = %q, want the fallback \"error\"", got)
		}
	})
}
