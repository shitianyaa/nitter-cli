package media

// Syndication payload parsing tests: photos[] (objects or bare URL strings),
// video.variants[] with bitrate selection input, and the two gif markers
// ("tweet_video_thumb" anywhere in the raw video JSON, or a gif video type).

import (
	"errors"
	"testing"

	"github.com/shitianyaa/twitter-cli/sdk"
)

func TestParseSyndicationFromFixture(t *testing.T) {
	cands, err := parseSyndication(readFixture(t, "syndication_status.json"))
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2 (photo, video)", len(cands))
	}
	if cands[0].Kind != kindImage || cands[0].URL != "https://pbs.twimg.com/media/Gy4jkl.jpg" || cands[0].Width != 1024 || cands[0].Height != 512 {
		t.Errorf("photo candidate = %+v", cands[0])
	}
	if cands[1].Kind != kindVideo {
		t.Errorf("video candidate kind = %q, want video", cands[1].Kind)
	}
	if len(cands[1].Variants) != 3 {
		t.Fatalf("video variants = %d, want 3", len(cands[1].Variants))
	}
	want := []twitter.MediaVariant{
		{URL: "https://video.twimg.com/ext_tw_video/300/pu/pl.m3u8", Bitrate: 0, ContentType: "application/x-mpegURL"},
		{URL: "https://video.twimg.com/ext_tw_video/300/pu/vid/480x848/832.mp4", Bitrate: 832000, ContentType: "video/mp4"},
		{URL: "https://video.twimg.com/ext_tw_video/300/pu/vid/720x1280/2176.mp4", Bitrate: 2176000, ContentType: "video/mp4"},
	}
	for i, v := range want {
		if cands[1].Variants[i] != v {
			t.Errorf("variant[%d] = %+v, want %+v", i, cands[1].Variants[i], v)
		}
	}
}

// TestParseSyndicationGifViaVideoThumbMarker: the "tweet_video_thumb"
// content type anywhere in the raw video JSON marks a gif, and the all-zero
// bitrate selection follows the high rule (last variant wins).
func TestParseSyndicationGifViaVideoThumbMarker(t *testing.T) {
	cands, err := parseSyndication(readFixture(t, "syndication_gif.json"))
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1", len(cands))
	}
	if cands[0].Kind != kindGif {
		t.Errorf("kind = %q, want gif (tweet_video_thumb marker)", cands[0].Kind)
	}
	if cands[0].Variants[1].URL != "https://video.twimg.com/tweet_video/Gz2-d.mp4" {
		t.Errorf("variant[1] = %+v", cands[0].Variants[1])
	}
}

// TestParseSyndicationGifViaVideoType: a gif video_type is the second marker.
func TestParseSyndicationGifViaVideoType(t *testing.T) {
	body := []byte(`{
	  "video": {
	    "video_type": "animated_gif",
	    "variants": [{"bitrate": 0, "content_type": "video/mp4", "src": "https://video.twimg.com/tweet_video/H-d.mp4"}]
	  }
	}`)
	cands, err := parseSyndication(body)
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 1 || cands[0].Kind != kindGif {
		t.Fatalf("candidates = %+v, want one gif", cands)
	}
}

// TestParseSyndicationBareStringPhoto: photos[] entries that are plain URL
// strings become images (plugin str(item) path).
func TestParseSyndicationBareStringPhoto(t *testing.T) {
	body := []byte(`{"photos": ["https://pbs.twimg.com/media/Gy5mno.jpg", 7, {"src": "https://pbs.twimg.com/media/Gy8vwx.jpg"}]}`)
	cands, err := parseSyndication(body)
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2 (string and src-object; number dropped)", len(cands))
	}
	if cands[0].URL != "https://pbs.twimg.com/media/Gy5mno.jpg" || cands[0].Kind != kindImage {
		t.Errorf("candidate[0] = %+v", cands[0])
	}
	if cands[1].URL != "https://pbs.twimg.com/media/Gy8vwx.jpg" || cands[1].Kind != kindImage {
		t.Errorf("candidate[1] = %+v", cands[1])
	}
}

// TestParseSyndicationPhotoSrcFallback: photo objects prefer url over src.
func TestParseSyndicationPhotoSrcFallback(t *testing.T) {
	body := []byte(`{"photos": [{"src": "https://pbs.twimg.com/media/Gy9yza.jpg"}]}`)
	cands, err := parseSyndication(body)
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 1 || cands[0].URL != "https://pbs.twimg.com/media/Gy9yza.jpg" {
		t.Fatalf("candidates = %+v", cands)
	}
}

// TestParseSyndicationVideoWithoutUsableVariants: a video object whose
// variants yield no URL contributes nothing (plugin `if best:` guard), and a
// non-object video is skipped entirely.
func TestParseSyndicationVideoWithoutUsableVariants(t *testing.T) {
	body := []byte(`{"photos": [], "video": {"variants": [{"bitrate": 832000}]}}`)
	cands, err := parseSyndication(body)
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v, want none", cands)
	}
	body = []byte(`{"video": "not an object"}`)
	if cands, err = parseSyndication(body); err != nil || len(cands) != 0 {
		t.Fatalf("non-object video: (%+v, %v), want none, nil", cands, err)
	}
}

// TestParseSyndicationEmptyPayload: an object without photos or video has no
// media (empty, not malformed).
func TestParseSyndicationEmptyPayload(t *testing.T) {
	cands, err := parseSyndication([]byte(`{"text": "only text"}`))
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v, want none", cands)
	}
}

func TestParseSyndicationMalformed(t *testing.T) {
	for _, body := range [][]byte{[]byte("{nope"), []byte("[{}]"), []byte("null")} {
		_, err := parseSyndication(body)
		var terr *twitter.Error
		if !errors.As(err, &terr) || terr.Kind != twitter.KindMalformed {
			t.Errorf("parseSyndication(%q) err = %v, want KindMalformed", body, err)
		}
	}
}
