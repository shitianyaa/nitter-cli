package media

// FxTwitter payload parsing tests. Fixtures mirror the real api.fxtwitter.com
// shape (tweet.media.all with photo/video/gif entries and per-variant
// bitrates); edge shapes not present in the fixtures are inline payloads.

import (
	"errors"
	"testing"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// TestParseFxFromFixture pins the three media kinds of the fx payload:
// the photo (with dimensions), the video whose variants list is kept whole
// with the item fallbacks, and the gif (its own kind, not "video").
func TestParseFxFromFixture(t *testing.T) {
	cands, err := parseFx(readFixture(t, "fx_status.json"))
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	if cands[0].Kind != kindImage || cands[0].URL != "https://pbs.twimg.com/media/Gx1abc.jpg" || cands[0].Width != 1024 || cands[0].Height != 768 {
		t.Errorf("photo candidate = %+v", cands[0])
	}
	if cands[1].Kind != kindVideo || cands[1].Width != 720 || cands[1].Height != 1280 {
		t.Errorf("video candidate = %+v", cands[1])
	}
	if len(cands[1].Variants) != 3 {
		t.Fatalf("video variants = %d, want 3", len(cands[1].Variants))
	}
	want := []nitter.MediaVariant{
		{URL: "https://video.twimg.com/ext_tw_video/100/pu/pl.m3u8", Bitrate: 0, ContentType: "application/x-mpegURL"},
		{URL: "https://video.twimg.com/ext_tw_video/100/pu/vid/480x848/832.mp4", Bitrate: 832000, ContentType: "video/mp4"},
		{URL: "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4", Bitrate: 2176000, ContentType: "video/mp4"},
	}
	for i, v := range want {
		if cands[1].Variants[i] != v {
			t.Errorf("variant[%d] = %+v, want %+v", i, cands[1].Variants[i], v)
		}
	}
	if cands[2].Kind != kindGif {
		t.Errorf("gif candidate kind = %q, want gif", cands[2].Kind)
	}
	if len(cands[2].Variants) != 2 || cands[2].Variants[1].Bitrate != 512000 {
		t.Errorf("gif variants = %+v", cands[2].Variants)
	}
}

// TestParseFxTextOnlyFixtureIsEmpty: a well-formed tweet without media yields
// zero candidates (the resolve loop then reports the strategy as empty).
func TestParseFxTextOnlyFixtureIsEmpty(t *testing.T) {
	cands, err := parseFx(readFixture(t, "fx_text_only.json"))
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v, want none", cands)
	}
}

// TestParseFxVideoInfoVariantFallback: with an empty item variants list the
// video_info.variants block is used (plugin `or` semantics). A separate
// untyped item confirms that a missing type defaults to photo → image.
func TestParseFxVideoInfoVariantFallback(t *testing.T) {
	body := []byte(`{
	  "tweet": {"text": "x", "media": {"all": [
	    {"type": "video",
	     "url": "https://video.twimg.com/x.mp4",
	     "variants": [],
	     "video_info": {"variants": [
	       {"bitrate": 832000, "content_type": "video/mp4", "url": "https://video.twimg.com/x-832.mp4"},
	       {"bitrate": 2176000, "content_type": "video/mp4", "url": "https://video.twimg.com/x-2176.mp4"}
	     ]}},
	    {"url": "https://pbs.twimg.com/media/GdD.jpg"}
	  ]}}
	}`)
	cands, err := parseFx(body)
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	if cands[0].Kind != kindVideo {
		t.Errorf("video candidate kind = %q", cands[0].Kind)
	}
	if len(cands[0].Variants) != 2 || cands[0].Variants[0].Bitrate != 832000 || cands[0].Variants[0].ContentType != "video/mp4" {
		t.Errorf("variants = %+v, want video_info.variants", cands[0].Variants)
	}
	if cands[1].Kind != kindImage || cands[1].URL != "https://pbs.twimg.com/media/GdD.jpg" {
		t.Errorf("untyped candidate = %+v, want image (missing type defaults to photo)", cands[1])
	}
}

// TestParseFxTopLevelFallback: without a tweet object, a payload carrying
// truthy text is inspected at the top level (plugin tw = data fallback).
func TestParseFxTopLevelFallback(t *testing.T) {
	body := []byte(`{
	  "text": "top level shape",
	  "media": {"all": [{"type": "photo", "url": "https://pbs.twimg.com/media/GbB.jpg"}]}
	}`)
	cands, err := parseFx(body)
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	if len(cands) != 1 || cands[0].Kind != kindImage || cands[0].URL != "https://pbs.twimg.com/media/GbB.jpg" {
		t.Fatalf("candidates = %+v, want the top-level photo", cands)
	}
}

// TestParseFxSkipsNonDictItems: non-object entries inside media.all are
// skipped, not fatal (plugin isinstance guard).
func TestParseFxSkipsNonDictItems(t *testing.T) {
	body := []byte(`{
	  "tweet": {"text": "x", "media": {"all": [
	    "garbage string",
	    {"type": "photo", "url": "https://pbs.twimg.com/media/GcC.jpg"}
	  ]}}
	}`)
	cands, err := parseFx(body)
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	if len(cands) != 1 || cands[0].URL != "https://pbs.twimg.com/media/GcC.jpg" {
		t.Fatalf("candidates = %+v, want only the object entry", cands)
	}
}

// TestParseFxKindMapping pins the type → kind table: video is video, the gif
// aliases (gif/animated_gif/dynamic) are gif, anything else is image.
func TestParseFxKindMapping(t *testing.T) {
	body := []byte(`{
	  "tweet": {"text": "x", "media": {"all": [
	    {"type": "video", "url": "https://video.twimg.com/a.mp4"},
	    {"type": "animated_gif", "url": "https://video.twimg.com/b.mp4"},
	    {"type": "dynamic", "url": "https://video.twimg.com/c.mp4"},
	    {"type": "photo", "url": "https://pbs.twimg.com/media/D.jpg"},
	    {"url": "https://pbs.twimg.com/media/E.jpg"}
	  ]}}
	}`)
	cands, err := parseFx(body)
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	want := []string{kindVideo, kindGif, kindGif, kindImage, kindImage}
	for i, k := range want {
		if cands[i].Kind != k {
			t.Errorf("candidate[%d] kind = %q, want %q", i, cands[i].Kind, k)
		}
	}
}

// TestParseFxMalformed: non-JSON bodies classify as KindMalformed.
func TestParseFxMalformed(t *testing.T) {
	for _, body := range [][]byte{[]byte("{nope"), []byte("[]"), []byte("null"), nil} {
		_, err := parseFx(body)
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindMalformed {
			t.Errorf("parseFx(%q) err = %v, want KindMalformed", body, err)
		}
	}
}

// TestParseFxCoverAndDuration: the moving-media items' thumbnail_url becomes
// the candidate cover and the item-level duration (seconds) rides along; a
// photo never carries a cover.
func TestParseFxCoverAndDuration(t *testing.T) {
	cands, err := parseFx(readFixture(t, "fx_status.json"))
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	if cands[0].CoverURL != "" || cands[0].DurationSeconds != 0 {
		t.Errorf("photo candidate = %+v, want no cover, no duration", cands[0])
	}
	if cands[1].CoverURL != "https://pbs.twimg.com/ext_tw_video_thumb/100/pu/img/pl.jpg" {
		t.Errorf("video candidate CoverURL = %q, want the item thumbnail_url", cands[1].CoverURL)
	}
	if cands[1].DurationSeconds != 12.5 {
		t.Errorf("video candidate DurationSeconds = %v, want the item-level 12.5", cands[1].DurationSeconds)
	}
	if cands[2].CoverURL != "https://pbs.twimg.com/tweet_video_thumb/Gz-d.jpg" {
		t.Errorf("gif candidate CoverURL = %q, want the item thumbnail_url", cands[2].CoverURL)
	}
}

// TestParseFxCoverGates: a plain-http thumbnail yields no cover (the
// no-plain-http rule applies) and a video item without a thumbnail leaves it
// empty — nothing is fabricated; a missing duration stays zero.
func TestParseFxCoverGates(t *testing.T) {
	body := []byte(`{
	  "tweet": {"text": "x", "media": {"all": [
	    {"type": "video", "url": "https://video.twimg.com/a.mp4", "thumbnail_url": "http://pbs.twimg.com/a.jpg", "duration": 3},
	    {"type": "video", "url": "https://video.twimg.com/b.mp4"}
	  ]}}
	}`)
	cands, err := parseFx(body)
	if err != nil {
		t.Fatalf("parseFx: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	if cands[0].CoverURL != "" || cands[0].DurationSeconds != 3 {
		t.Errorf("plain-http thumbnail candidate = %+v, want empty cover with duration kept", cands[0])
	}
	if cands[1].CoverURL != "" || cands[1].DurationSeconds != 0 {
		t.Errorf("thumbnail-less candidate = %+v, want no cover, zero duration", cands[1])
	}
}
