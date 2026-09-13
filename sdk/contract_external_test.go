// External contract-freeze test: this file pins the ENTIRE exported surface
// of the sdk at compile time. It is the executable form of the "additive-only"
// v1 contract — any rename, removal, type change or signature change of a
// frozen identifier breaks this build, while additive extensions compile
// cleanly.
//
// Two mechanisms:
//  1. Signature assertions: function values (and method expressions) assigned
//     to typed vars — these freeze exact signatures, not just existence.
//  2. Composite-literal assignments with field names — these freeze struct
//     field names and types (additive new fields still compile, per the
//     contract).
//
// The JSON encodings themselves (zero and filled Tweet, Page, InstanceReport)
// are golden-pinned in models_test.go and NOT duplicated here; this file adds
// the standalone sub-struct goldens (Author, Media, Quoted, Probe) that the
// embedded Tweet golden does not isolate.
package nitter_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// --- Client / New / Options -------------------------------------------------

var (
	_ func(...nitter.Options) (*nitter.Client, error) = nitter.New
	_ func([]nitter.Instance) nitter.Options          = nitter.WithInstances
	_ func(nitter.Transport) nitter.Options           = nitter.WithHTTPClient
	_ func(time.Duration) nitter.Options              = nitter.WithCooldown

	// The concrete types behind the option values.
	_ *nitter.Client   = (*nitter.Client)(nil)
	_ nitter.Options   = nil
	_ []nitter.Options = nil
	_ nitter.Transport = (nitter.Transport)(nil)
	_ nitter.Instance  = nitter.Instance{URL: "", Username: "", Password: ""}
)

// --- Chooser -----------------------------------------------------------------

var (
	_ func([]nitter.Instance, time.Duration, func() time.Time) *nitter.Chooser = nitter.NewChooser
	// Method expression: freezes Pick's exact signature on *Chooser.
	_ func(*nitter.Chooser) (string, func(), func(), error) = (*nitter.Chooser).Pick
	_ *nitter.Chooser                                       = (*nitter.Chooser)(nil)
)

// --- Errors ------------------------------------------------------------------

var (
	// Errorf's full signature: kind, op, format, args.
	_ func(nitter.Kind, string, string, ...any) *nitter.Error = nitter.Errorf

	// The exported *Error struct shape (Kind/Op/Err/RetryAfter).
	_ *nitter.Error = &nitter.Error{
		Kind:       nitter.KindInvalidArg,
		Op:         "",
		Err:        nil,
		RetryAfter: (*time.Duration)(nil),
	}

	// The error/unwrap methods, as satisfied by *Error.
	_ func(*nitter.Error) string = (*nitter.Error).Error
	_ func(*nitter.Error) error  = (*nitter.Error).Unwrap

	// All seven v1 Kind constants — names, type AND wire values.
	_ map[nitter.Kind]string = map[nitter.Kind]string{
		nitter.KindChallenge:   "challenge_required",
		nitter.KindRateLimited: "rate_limited",
		nitter.KindNotFound:    "not_found",
		nitter.KindUnavailable: "upstream_unavailable",
		nitter.KindMalformed:   "malformed_upstream_response",
		nitter.KindInvalidArg:  "invalid_argument",
		nitter.KindLocalState:  "local_state_error",
	}
)

// TestKindWireValuesAreFrozen pins the string values behind the Kind
// constants: the map above freezes their names and type at compile time, but
// only an equality check can freeze the wire values themselves (they reach
// NDJSON consumers through error envelopes).
func TestKindWireValuesAreFrozen(t *testing.T) {
	want := map[nitter.Kind]string{
		nitter.KindChallenge:   "challenge_required",
		nitter.KindRateLimited: "rate_limited",
		nitter.KindNotFound:    "not_found",
		nitter.KindUnavailable: "upstream_unavailable",
		nitter.KindMalformed:   "malformed_upstream_response",
		nitter.KindInvalidArg:  "invalid_argument",
		nitter.KindLocalState:  "local_state_error",
	}
	for kind, value := range want {
		if string(kind) != value {
			t.Errorf("Kind %q = %q, wire value drifted", value, string(kind))
		}
	}
}

// --- Data models (field shapes via composite literals) -----------------------

var (
	_ nitter.Tweet = nitter.Tweet{
		ID:   "",
		URL:  "",
		Text: "",
		Author: nitter.Author{
			Handle:    "",
			Name:      "",
			AvatarURL: "",
		},
		PublishedAt: time.Time{},
		Media:       []nitter.Media{{Type: "", URL: "", Width: 0, Height: 0}},
		IsRetweet:   false,
		RepostedBy:  "",
		ReplyTo:     "",
		Quote: &nitter.Quoted{
			ID:     "",
			URL:    "",
			Text:   "",
			Author: nitter.Author{},
		},
	}
	_ nitter.Quoted = nitter.Quoted{}
	_ nitter.Author = nitter.Author{}
	_ nitter.Media  = nitter.Media{}

	_ nitter.Probe          = nitter.Probe{OK: false, Status: 0, Err: ""}
	_ nitter.InstanceReport = nitter.InstanceReport{
		URL:      "",
		RSS:      nitter.Probe{},
		UserHTML: nitter.Probe{},
		Search:   nitter.Probe{},
		List:     nitter.Probe{},
		Latency:  0,
	}

	// Media resolution (additive M8 extension, cover_url additive M9): the
	// field-name/type freeze via composite literals.
	_ nitter.MediaVariant    = nitter.MediaVariant{URL: "", Bitrate: 0, ContentType: ""}
	_ nitter.MediaResolution = nitter.MediaResolution{
		Ref:             "",
		Source:          "",
		Kind:            "",
		URL:             "",
		FallbackURL:     "",
		CoverURL:        "",
		Label:           "",
		Width:           0,
		Height:          0,
		DurationSeconds: 0,
		SizeBytes:       0,
		Variants:        []nitter.MediaVariant{{URL: "", Bitrate: 0, ContentType: ""}},
	}

	// The generic pagination envelope instantiated at the frozen type.
	_ nitter.Page[nitter.Tweet]          = nitter.Page[nitter.Tweet]{Items: nil, NextCursor: ""}
	_ nitter.Page[nitter.InstanceReport] = nitter.Page[nitter.InstanceReport]{Items: nil, NextCursor: ""}
)

// --- Standalone sub-struct JSON goldens ---------------------------------------
//
// The zero and filled Tweet/Page/InstanceReport goldens live in models_test.go
// (single source of truth). These pin the standalone encodings of the
// sub-structures that only appear embedded there.

func TestAuthorJSONShapeIsTheDataContract(t *testing.T) {
	b, err := json.Marshal(nitter.Author{Handle: "nasa", Name: "NASA", AvatarURL: "https://pbs.twimg.com/a.jpg"})
	if err != nil {
		t.Fatalf("Marshal(Author) = error %v", err)
	}
	want := `{"handle":"nasa","name":"NASA","avatar_url":"https://pbs.twimg.com/a.jpg"}`
	if string(b) != want {
		t.Errorf("Author JSON mismatch:\n got %s\nwant %s", b, want)
	}
}

func TestMediaJSONShapeIsTheDataContract(t *testing.T) {
	b, err := json.Marshal(nitter.Media{Type: "gif", URL: "https://video.twimg.com/x.mp4", Width: 640, Height: 360})
	if err != nil {
		t.Fatalf("Marshal(Media) = error %v", err)
	}
	want := `{"type":"gif","url":"https://video.twimg.com/x.mp4","width":640,"height":360}`
	if string(b) != want {
		t.Errorf("Media JSON mismatch:\n got %s\nwant %s", b, want)
	}
}

func TestQuotedJSONShapeIsTheDataContract(t *testing.T) {
	b, err := json.Marshal(nitter.Quoted{ID: "7", URL: "https://x.com/esa/status/7", Text: "q"})
	if err != nil {
		t.Fatalf("Marshal(Quoted) = error %v", err)
	}
	want := `{"id":"7","url":"https://x.com/esa/status/7","text":"q","author":{"handle":"","name":"","avatar_url":""}}`
	if string(b) != want {
		t.Errorf("Quoted JSON mismatch:\n got %s\nwant %s", b, want)
	}
}

func TestProbeJSONShapeIsTheDataContract(t *testing.T) {
	b, err := json.Marshal(nitter.Probe{OK: false, Status: 429, Err: "instance returned HTTP 429"})
	if err != nil {
		t.Fatalf("Marshal(Probe) = error %v", err)
	}
	want := `{"ok":false,"status":429,"err":"instance returned HTTP 429"}`
	if string(b) != want {
		t.Errorf("Probe JSON mismatch:\n got %s\nwant %s", b, want)
	}
}

// TestMediaResolutionJSONShapeIsTheDataContract pins the M8 media-resolution
// encoding: the identity keys (ref/source/kind/url) marshal unconditionally,
// every sparse field is omitempty, and a zero value carries only those four.
func TestMediaResolutionJSONShapeIsTheDataContract(t *testing.T) {
	b, err := json.Marshal(nitter.MediaResolution{
		Ref:    "https://x.com/nasa/status/7",
		Source: "fx",
		Kind:   "video",
		URL:    "https://video.twimg.com/x.mp4",
		Width:  720,
		Height: 1280,
		Variants: []nitter.MediaVariant{
			{URL: "https://video.twimg.com/x.mp4", Bitrate: 2176000, ContentType: "video/mp4"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal(MediaResolution) = error %v", err)
	}
	want := `{"ref":"https://x.com/nasa/status/7","source":"fx","kind":"video","url":"https://video.twimg.com/x.mp4","width":720,"height":1280,` +
		`"variants":[{"url":"https://video.twimg.com/x.mp4","bitrate":2176000,"content_type":"video/mp4"}]}`
	if string(b) != want {
		t.Errorf("MediaResolution JSON mismatch:\n got %s\nwant %s", b, want)
	}

	zero, err := json.Marshal(nitter.MediaResolution{})
	if err != nil {
		t.Fatalf("Marshal(zero MediaResolution) = error %v", err)
	}
	wantZero := `{"ref":"","source":"","kind":"","url":""}`
	if string(zero) != wantZero {
		t.Errorf("zero MediaResolution JSON mismatch:\n got %s\nwant %s", zero, wantZero)
	}

	// cover_url (additive M9): a sparse field like its siblings — present
	// with its key when filled, absent from the zero encoding above.
	withCover, err := json.Marshal(nitter.MediaResolution{
		Ref: "https://x.com/nasa/status/7", Source: "fx", Kind: "video", URL: "https://video.twimg.com/x.mp4",
		CoverURL: "https://pbs.twimg.com/ext_tw_video_thumb/7/pu/img/pl.jpg",
	})
	if err != nil {
		t.Fatalf("Marshal(MediaResolution with cover) = error %v", err)
	}
	wantCover := `{"ref":"https://x.com/nasa/status/7","source":"fx","kind":"video","url":"https://video.twimg.com/x.mp4","cover_url":"https://pbs.twimg.com/ext_tw_video_thumb/7/pu/img/pl.jpg"}`
	if string(withCover) != wantCover {
		t.Errorf("MediaResolution cover JSON mismatch:\n got %s\nwant %s", withCover, wantCover)
	}
}
