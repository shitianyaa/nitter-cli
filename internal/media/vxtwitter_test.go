package media

// VxTwitter payload parsing tests: media_extended is primary; the legacy
// mediaURLs / media_urls lists are image-only fallbacks used only when
// media_extended yields nothing.

import (
	"errors"
	"testing"

	"github.com/shitianyaa/twitter-cli/sdk"
)

func TestParseVxFromFixture(t *testing.T) {
	cands, err := parseVx(readFixture(t, "vx_status.json"))
	if err != nil {
		t.Fatalf("parseVx: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	wantURLs := []string{
		"https://pbs.twimg.com/media/Gy1abc.jpg",
		"https://video.twimg.com/ext_tw_video/200/pu/vid/720x1280/2176.mp4",
		"https://video.twimg.com/tweet_video/Vx-d.mp4",
	}
	wantKinds := []string{kindImage, kindVideo, kindGif}
	for i := range wantURLs {
		if cands[i].URL != wantURLs[i] || cands[i].Kind != wantKinds[i] {
			t.Errorf("candidate[%d] = (%q, %q), want (%q, %q)", i, cands[i].Kind, cands[i].URL, wantKinds[i], wantURLs[i])
		}
		if len(cands[i].Variants) != 0 {
			t.Errorf("candidate[%d] carries variants; vx media_extended has none", i)
		}
	}
}

func TestParseVxFallsBackToMediaURLs(t *testing.T) {
	cands, err := parseVx(readFixture(t, "vx_mediaurls_fallback.json"))
	if err != nil {
		t.Fatalf("parseVx: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	for i, c := range cands {
		if c.Kind != kindImage {
			t.Errorf("fallback candidate[%d] kind = %q, want image (legacy lists are image-only)", i, c.Kind)
		}
	}
	if cands[0].URL != "https://pbs.twimg.com/media/Gy2def.jpg" || cands[1].URL != "https://video.twimg.com/ext_tw_video/201/pu/vid/640x360/pl.mp4" {
		t.Errorf("fallback URLs = %q / %q", cands[0].URL, cands[1].URL)
	}
}

func TestParseVxFallsBackToSnakeCaseMediaURLs(t *testing.T) {
	cands, err := parseVx(readFixture(t, "vx_media_urls_fallback.json"))
	if err != nil {
		t.Fatalf("parseVx: %v", err)
	}
	if len(cands) != 1 || cands[0].Kind != kindImage || cands[0].URL != "https://pbs.twimg.com/media/Gy3ghi.jpg" {
		t.Fatalf("candidates = %+v, want the snake_case image", cands)
	}
}

// TestParseVxEmptyExtendedStillFallsBack: an empty media_extended list must
// NOT win over the legacy lists (Python `[] or fallback` semantics).
func TestParseVxEmptyExtendedStillFallsBack(t *testing.T) {
	body := []byte(`{"media_extended": [], "mediaURLs": ["https://pbs.twimg.com/media/Gy6pqr.jpg"]}`)
	cands, err := parseVx(body)
	if err != nil {
		t.Fatalf("parseVx: %v", err)
	}
	if len(cands) != 1 || cands[0].URL != "https://pbs.twimg.com/media/Gy6pqr.jpg" {
		t.Fatalf("candidates = %+v, want the mediaURLs image", cands)
	}
}

// TestParseVxNonStringLegacyEntriesDropped: the legacy lists are taken as
// plain strings; anything else (numbers, objects) is skipped, never fatal.
func TestParseVxNonStringLegacyEntriesDropped(t *testing.T) {
	body := []byte(`{"mediaURLs": ["https://pbs.twimg.com/media/Gy7stu.jpg", 42, {"url": "x"}]}`)
	cands, err := parseVx(body)
	if err != nil {
		t.Fatalf("parseVx: %v", err)
	}
	if len(cands) != 1 || cands[0].URL != "https://pbs.twimg.com/media/Gy7stu.jpg" {
		t.Fatalf("candidates = %+v, want only the string entry", cands)
	}
}

// TestParseVxKindDefaultsToImage: media_extended items without a type are
// images; unknown types too.
func TestParseVxKindDefaultsToImage(t *testing.T) {
	body := []byte(`{"media_extended": [{"url": "https://video.twimg.com/a.mp4"}, {"type": "photo", "url": "https://pbs.twimg.com/media/B.jpg"}]}`)
	cands, err := parseVx(body)
	if err != nil {
		t.Fatalf("parseVx: %v", err)
	}
	if len(cands) != 2 || cands[0].Kind != kindImage || cands[1].Kind != kindImage {
		t.Fatalf("candidates = %+v, want both images", cands)
	}
}

func TestParseVxMalformed(t *testing.T) {
	for _, body := range [][]byte{[]byte("{nope"), []byte("[1,2]"), []byte("null")} {
		_, err := parseVx(body)
		var terr *twitter.Error
		if !errors.As(err, &terr) || terr.Kind != twitter.KindMalformed {
			t.Errorf("parseVx(%q) err = %v, want KindMalformed", body, err)
		}
	}
}
