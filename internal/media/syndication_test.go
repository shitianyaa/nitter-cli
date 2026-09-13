package media

// Syndication payload parsing tests: photos[] (objects or bare URL strings),
// video.variants[] with bitrate selection input, and the two gif markers
// ("tweet_video_thumb" anywhere in the raw video JSON, or a gif video type).

import (
	"errors"
	"testing"

	"github.com/shitianyaa/nitter-cli/sdk"
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
	want := []nitter.MediaVariant{
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
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindMalformed {
			t.Errorf("parseSyndication(%q) err = %v, want KindMalformed", body, err)
		}
	}
}

// TestParseSyndicationCoverAndDuration: the video object's poster field is
// the cover (verified live 2026-09-13) and durationMs becomes DurationSeconds;
// photos never carry a cover and a plain-http poster is dropped.
func TestParseSyndicationCoverAndDuration(t *testing.T) {
	body := []byte(`{
	  "photos": [{"url": "https://pbs.twimg.com/media/Gy4jkl.jpg", "width": 100, "height": 50}],
	  "video": {
	    "poster": "https://pbs.twimg.com/ext_tw_video_thumb/300/pu/img/pl.jpg",
	    "durationMs": 12500,
	    "variants": [
	      {"bitrate": 0, "content_type": "application/x-mpegURL", "src": "https://video.twimg.com/pl.m3u8"},
	      {"bitrate": 2176000, "content_type": "video/mp4", "src": "https://video.twimg.com/2176.mp4"}
	    ]
	  }
	}`)
	cands, err := parseSyndication(body)
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	if cands[0].CoverURL != "" {
		t.Errorf("photo candidate CoverURL = %q, want empty", cands[0].CoverURL)
	}
	if cands[1].CoverURL != "https://pbs.twimg.com/ext_tw_video_thumb/300/pu/img/pl.jpg" {
		t.Errorf("video candidate CoverURL = %q, want the poster", cands[1].CoverURL)
	}
	if cands[1].DurationSeconds != 12.5 {
		t.Errorf("video candidate DurationSeconds = %v, want 12.5 (durationMs/1000)", cands[1].DurationSeconds)
	}

	plainHTTP := []byte(`{"video": {"poster": "http://pbs.twimg.com/ext_tw_video_thumb/300/pu/img/pl.jpg", "variants": [{"bitrate": 0, "content_type": "video/mp4", "src": "https://video.twimg.com/a.mp4"}]}}`)
	if cands, err = parseSyndication(plainHTTP); err != nil || len(cands) != 1 || cands[0].CoverURL != "" {
		t.Errorf("plain-http poster = (%+v, %v), want one candidate with no cover", cands, err)
	}
}

// TestParseSyndicationCoverFromFixture: the committed gif fixture's poster (a
// tweet_video_thumb path) becomes the gif candidate's cover.
func TestParseSyndicationCoverFromFixture(t *testing.T) {
	cands, err := parseSyndication(readFixture(t, "syndication_gif.json"))
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 1 || cands[0].CoverURL != "https://pbs.twimg.com/tweet_video_thumb/Gz2-d.jpg" {
		t.Fatalf("candidates = %+v, want the gif's poster as cover", cands)
	}
}

// TestParseSyndicationCoverLegacyThumbFallback: a payload without a poster
// field still yields a cover when a tweet_video_thumb link rides anywhere in
// the raw video JSON — the same string match the gif detection scans, reused
// as the legacy cover fallback.
func TestParseSyndicationCoverLegacyThumbFallback(t *testing.T) {
	body := []byte(`{"video": {"variants": [
	  {"bitrate": 0, "content_type": "video/mp4", "src": "https://video.twimg.com/tweet_video/H-d.mp4"},
	  {"bitrate": 0, "content_type": "tweet_video_thumb", "src": "https://pbs.twimg.com/tweet_video_thumb/H.jpg"}
	]}}`)
	cands, err := parseSyndication(body)
	if err != nil {
		t.Fatalf("parseSyndication: %v", err)
	}
	if len(cands) != 1 || cands[0].CoverURL != "https://pbs.twimg.com/tweet_video_thumb/H.jpg" {
		t.Fatalf("candidates = %+v, want the tweet_video_thumb link as cover", cands)
	}

	noThumb := []byte(`{"video": {"variants": [{"bitrate": 0, "content_type": "video/mp4", "src": "https://video.twimg.com/a.mp4"}]}}`)
	if cands, err = parseSyndication(noThumb); err != nil || len(cands) != 1 || cands[0].CoverURL != "" {
		t.Errorf("poster-less video = (%+v, %v), want one candidate with no cover", cands, err)
	}
}
