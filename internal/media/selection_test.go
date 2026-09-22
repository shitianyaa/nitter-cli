package media

// PlanDownload selection tests. Fixtures mirror the shapes recorded live
// 2026-09-13: the xdown video tweet (five 下载 MP4 (Np) entries plus the
// 下载图片 cover entry), the fx video-tweet projection (bitrated variants with
// the m3u8 playlist riding along, cover, duration) and the four-image tweet.
// The selection layer is a pure function over one status's resolutions — the
// winning strategy has already produced them — so no transport is involved
// here except the http-exclusion integration test, which plans the real
// resolve output.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// xdownVideoTweet is the live 2026-09-13 xdown shape: five video entries
// labeled 下载 MP4 (Np) in descending resolution plus the 下载图片 cover entry,
// every video carrying the CoverURL the xdown capture pass now sets. The
// amplify thumb is no pbs /media/ link, so its name=small query survives the
// quality rewrite verbatim — exactly what the resolve layer emits.
func xdownVideoTweet() []nitter.MediaResolution {
	cover := "https://pbs.twimg.com/amplify_video_thumb/2070000000000000100/pu/img/pl.jpg?name=small"
	video := func(p, u string) nitter.MediaResolution {
		return nitter.MediaResolution{
			Ref: statusURL100, Source: "xdown", Kind: kindVideo,
			URL: u, Label: "下载 MP4 (" + p + ")", CoverURL: cover,
		}
	}
	return []nitter.MediaResolution{
		video("3840p", "https://video.twimg.com/ext_tw_video/1/pu/vid/3840x2160/a.mp4"),
		video("1920p", "https://video.twimg.com/ext_tw_video/1/pu/vid/1920x1080/b.mp4"),
		video("1280p", "https://video.twimg.com/ext_tw_video/1/pu/vid/1280x720/c.mp4"),
		video("852p", "https://video.twimg.com/ext_tw_video/1/pu/vid/852x480/d.mp4"),
		video("568p", "https://video.twimg.com/ext_tw_video/1/pu/vid/568x320/e.mp4"),
		{
			Ref: statusURL100, Source: "xdown", Kind: kindImage,
			URL: cover, Label: "下载图片",
			FallbackURL: "https://xdown.app/proxy/img?token=t",
		},
	}
}

// fxVideoProjection is the fx strategy's video-tweet projection: one video
// entry whose variants carry the m3u8 playlist plus the two mp4 encodings,
// the fx cover (thumbnail_url), the payload's duration and dimensions, and —
// as the resolve layer emits it at a below-high tier — the highest variant as
// FallbackURL.
func fxVideoProjection() []nitter.MediaResolution {
	return []nitter.MediaResolution{
		{
			Ref: statusURL100, Source: "fx", Kind: kindVideo,
			URL:             "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4",
			FallbackURL:     "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4",
			CoverURL:        "https://pbs.twimg.com/ext_tw_video_thumb/100/pu/img/pl.jpg",
			Width:           720,
			Height:          1280,
			DurationSeconds: 12.5,
			Variants: []nitter.MediaVariant{
				{URL: "https://video.twimg.com/ext_tw_video/100/pu/pl.m3u8", ContentType: "application/x-mpegURL"},
				{URL: "https://video.twimg.com/ext_tw_video/100/pu/vid/480x848/832.mp4", Bitrate: 832000, ContentType: "video/mp4"},
				{URL: "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4", Bitrate: 2176000, ContentType: "video/mp4"},
			},
		},
	}
}

// gifTweetProjection is the gif-tweet projection: zero-bitrate tweet_video
// variants with the m3u8 playlist riding first (upstream order), no cover.
func gifTweetProjection() []nitter.MediaResolution {
	return []nitter.MediaResolution{
		{
			Ref: statusURL100, Source: "vx", Kind: kindGif,
			URL: "https://video.twimg.com/tweet_video/Gz-d.mp4",
			Variants: []nitter.MediaVariant{
				{URL: "https://video.twimg.com/tweet_video/Gz.pl.m3u8", ContentType: "application/x-mpegURL"},
				{URL: "https://video.twimg.com/tweet_video/Gz-d.mp4"},
			},
		},
	}
}

// fourImageTweet is the four-image tweet projection (pbs orig links, as the
// fx strategy emits them at the high tier).
func fourImageTweet() []nitter.MediaResolution {
	return []nitter.MediaResolution{
		{Ref: statusURL100, Source: "fx", Kind: kindImage, URL: "https://pbs.twimg.com/media/Gx1abc.jpg?name=orig"},
		{Ref: statusURL100, Source: "fx", Kind: kindImage, URL: "https://pbs.twimg.com/media/Gx2abc.jpg?name=orig"},
		{Ref: statusURL100, Source: "fx", Kind: kindImage, URL: "https://pbs.twimg.com/media/Gx3abc.jpg?name=orig"},
		{Ref: statusURL100, Source: "fx", Kind: kindImage, URL: "https://pbs.twimg.com/media/Gx4abc.jpg?name=orig"},
	}
}

// TestPlanDownloadXdownConvergesPerQuality: the five xdown entries converge
// to ONE planned file, ranked by the p-number in the labels with the resolve
// layer's exact quality semantics — high the highest, medium the upper
// median, low the lowest — and the fallback chain lists the remaining
// entries in descending rank order.
func TestPlanDownloadXdownConvergesPerQuality(t *testing.T) {
	const (
		a = "https://video.twimg.com/ext_tw_video/1/pu/vid/3840x2160/a.mp4"
		b = "https://video.twimg.com/ext_tw_video/1/pu/vid/1920x1080/b.mp4"
		c = "https://video.twimg.com/ext_tw_video/1/pu/vid/1280x720/c.mp4"
		d = "https://video.twimg.com/ext_tw_video/1/pu/vid/852x480/d.mp4"
		e = "https://video.twimg.com/ext_tw_video/1/pu/vid/568x320/e.mp4"
	)
	cases := []struct {
		quality string
		wantURL string
		wantFB  []string
	}{
		{"high", a, []string{b, c, d, e}},
		{"medium", c, []string{a, b, d, e}},
		{"low", e, []string{a, b, c, d}},
		{"", a, []string{b, c, d, e}}, // unset quality behaves as high
	}
	for _, tc := range cases {
		t.Run("quality="+tc.quality, func(t *testing.T) {
			plan, err := PlanDownload(id100, xdownVideoTweet(), tc.quality, "")
			if err != nil {
				t.Fatalf("PlanDownload: %v", err)
			}
			if len(plan) != 1 {
				t.Fatalf("plan = %d files (%+v), want exactly one converged video", len(plan), plan)
			}
			f := plan[0]
			if f.StatusID != id100 || f.Seq != "1" || f.Kind != kindVideo || f.Ext != ".mp4" {
				t.Errorf("file header = %+v, want StatusID %s, Seq 1, kind video, ext .mp4", f, id100)
			}
			if f.URL != tc.wantURL {
				t.Errorf("URL = %q, want %q", f.URL, tc.wantURL)
			}
			if len(f.Fallbacks) != len(tc.wantFB) {
				t.Fatalf("Fallbacks = %v, want %v", f.Fallbacks, tc.wantFB)
			}
			for i, u := range tc.wantFB {
				if f.Fallbacks[i] != u {
					t.Errorf("Fallbacks[%d] = %q, want %q", i, f.Fallbacks[i], u)
				}
			}
		})
	}
}

// TestPlanDownloadHighTiesGoToLaterEntry: equal p-numbers rank-tie and the
// high tier picks the LATER one — the selectVariant semantics the selection
// layer reuses verbatim.
func TestPlanDownloadHighTiesGoToLaterEntry(t *testing.T) {
	res := []nitter.MediaResolution{
		{Ref: statusURL100, Source: "xdown", Kind: kindVideo, URL: "https://v.twimg.com/first.mp4", Label: "下载 MP4 (720p)"},
		{Ref: statusURL100, Source: "xdown", Kind: kindVideo, URL: "https://v.twimg.com/second.mp4", Label: "下载 MP4 (720p)"},
	}
	plan, err := PlanDownload(id100, res, "high", "")
	if err != nil || len(plan) != 1 {
		t.Fatalf("PlanDownload = (%+v, %v)", plan, err)
	}
	if plan[0].URL != "https://v.twimg.com/second.mp4" {
		t.Errorf("URL = %q, want the later tied entry", plan[0].URL)
	}
	if len(plan[0].Fallbacks) != 1 || plan[0].Fallbacks[0] != "https://v.twimg.com/first.mp4" {
		t.Errorf("Fallbacks = %v, want the earlier tied entry", plan[0].Fallbacks)
	}
}

// TestPlanDownloadFxVideoConvergence: the fx single-video projection
// converges on its variants with the exact selectVariant semantics, the
// winner's own FallbackURL takes the second chain slot, and the m3u8
// playlist NEVER appears anywhere in the plan.
func TestPlanDownloadFxVideoConvergence(t *testing.T) {
	cases := []struct {
		quality string
		wantURL string
		wantFB  []string
	}{
		{"high", "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4",
			[]string{"https://video.twimg.com/ext_tw_video/100/pu/vid/480x848/832.mp4"}},
		{"medium", "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4",
			[]string{"https://video.twimg.com/ext_tw_video/100/pu/vid/480x848/832.mp4"}},
		{"low", "https://video.twimg.com/ext_tw_video/100/pu/vid/480x848/832.mp4",
			[]string{"https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4"}},
	}
	for _, tc := range cases {
		t.Run("quality="+tc.quality, func(t *testing.T) {
			plan, err := PlanDownload(id100, fxVideoProjection(), tc.quality, "")
			if err != nil {
				t.Fatalf("PlanDownload: %v", err)
			}
			if len(plan) != 1 {
				t.Fatalf("plan = %d files (%+v), want one", len(plan), plan)
			}
			f := plan[0]
			if f.URL != tc.wantURL {
				t.Errorf("URL = %q, want %q", f.URL, tc.wantURL)
			}
			if strings.Join(f.Fallbacks, "|") != strings.Join(tc.wantFB, "|") {
				t.Errorf("Fallbacks = %v, want %v", f.Fallbacks, tc.wantFB)
			}
			if f.Kind != kindVideo || f.Ext != ".mp4" {
				t.Errorf("file = %+v, want kind video, ext .mp4", f)
			}
			for _, u := range append([]string{f.URL}, f.Fallbacks...) {
				if strings.Contains(u, ".m3u8") {
					t.Errorf("playlist link %q leaked into the plan", u)
				}
			}
		})
	}
}

// TestPlanDownloadWinnerFallbackTakesSecondSlot: the winning entry's own
// fallback link (the xdown proxy standing in for a direct link) precedes the
// remaining ranked entries in the chain.
func TestPlanDownloadWinnerFallbackTakesSecondSlot(t *testing.T) {
	res := []nitter.MediaResolution{
		{Ref: statusURL100, Source: "xdown", Kind: kindVideo, URL: "https://v.twimg.com/hi.mp4",
			Label: "下载 MP4 (1080p)", FallbackURL: "https://xdown.app/dl?token=t"},
		{Ref: statusURL100, Source: "xdown", Kind: kindVideo, URL: "https://v.twimg.com/lo.mp4",
			Label: "下载 MP4 (720p)"},
	}
	plan, err := PlanDownload(id100, res, "high", "")
	if err != nil || len(plan) != 1 {
		t.Fatalf("PlanDownload = (%+v, %v)", plan, err)
	}
	want := []string{"https://xdown.app/dl?token=t", "https://v.twimg.com/lo.mp4"}
	if strings.Join(plan[0].Fallbacks, "|") != strings.Join(want, "|") {
		t.Errorf("Fallbacks = %v, want %v", plan[0].Fallbacks, want)
	}
}

// TestPlanDownloadGifTweetConverges: a gif tweet plans ONE gif file (the
// zero-bitrate pool's mp4; the m3u8 playlist excluded), --kind gif keeps it
// and --kind video — whose pre-filter matches no entry — yields an empty
// plan without error.
func TestPlanDownloadGifTweetConverges(t *testing.T) {
	plan, err := PlanDownload(id100, gifTweetProjection(), "high", "")
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	if len(plan) != 1 || plan[0].Kind != kindGif || plan[0].URL != "https://video.twimg.com/tweet_video/Gz-d.mp4" || plan[0].Ext != ".mp4" {
		t.Fatalf("plan = %+v, want one gif file for the tweet_video mp4", plan)
	}
	if len(plan[0].Fallbacks) != 0 {
		t.Errorf("Fallbacks = %v, want none (the m3u8 playlist is not a fallback)", plan[0].Fallbacks)
	}

	plan, err = PlanDownload(id100, gifTweetProjection(), "", "gif")
	if err != nil || len(plan) != 1 || plan[0].Kind != kindGif {
		t.Errorf("--kind gif = (%+v, %v), want the same single gif file", plan, err)
	}

	plan, err = PlanDownload(id100, gifTweetProjection(), "", "video")
	if err != nil {
		t.Fatalf("--kind video error: %v", err)
	}
	if len(plan) != 0 {
		t.Errorf("--kind video plan = %+v, want an empty plan (filter matched nothing)", plan)
	}
}

// TestPlanDownloadImageTweetPlansAllImages: no moving media — every image is
// planned in order with Seq 1..N; --kind image plans the same set; --kind
// video/gif yield an empty plan without error; --kind cover refuses with a
// not_found error.
func TestPlanDownloadImageTweetPlansAllImages(t *testing.T) {
	cases := []struct {
		name       string
		kindFilter string
		wantFiles  int
		wantErr    bool
	}{
		{"no filter", "", 4, false},
		{"kind image", "image", 4, false},
		{"kind video", "video", 0, false},
		{"kind gif", "gif", 0, false},
		{"kind cover", "cover", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanDownload(id100, fourImageTweet(), "high", tc.kindFilter)
			if tc.wantErr {
				var terr *nitter.Error
				if !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
					t.Fatalf("err = %v, want KindNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("PlanDownload: %v", err)
			}
			if len(plan) != tc.wantFiles {
				t.Fatalf("plan = %d files (%+v), want %d", len(plan), plan, tc.wantFiles)
			}
			for i, f := range plan {
				if f.Kind != kindImage || f.Ext != ".jpg" || f.StatusID != id100 {
					t.Errorf("file[%d] = %+v, want an image of status %s with ext .jpg", i, f, id100)
				}
				if f.Seq != string(rune('1'+i)) {
					t.Errorf("file[%d].Seq = %q, want %q", i, f.Seq, string(rune('1'+i)))
				}
				if len(f.Fallbacks) != 0 {
					t.Errorf("file[%d].Fallbacks = %v, want none", i, f.Fallbacks)
				}
			}
		})
	}
}

// TestPlanDownloadKindIsAPreFilter: --kind filters the entries first, then
// the convergence rules apply to what is left. On a mixed fx tweet the
// explicit --kind image plans the real photo (the video-wins rule is the
// default path, not an override of an explicit filter); without a filter the
// same tweet converges on its video.
func TestPlanDownloadKindIsAPreFilter(t *testing.T) {
	mixed := []nitter.MediaResolution{
		{Ref: statusURL100, Source: "fx", Kind: kindImage, URL: "https://pbs.twimg.com/media/Gm1.jpg?name=orig"},
		{
			Ref: statusURL100, Source: "fx", Kind: kindVideo, URL: "https://video.twimg.com/hi.mp4",
			Variants: []nitter.MediaVariant{{URL: "https://video.twimg.com/hi.mp4", Bitrate: 2176000}},
		},
	}
	plan, err := PlanDownload(id100, mixed, "high", "image")
	if err != nil || len(plan) != 1 || plan[0].Kind != kindImage || plan[0].URL != "https://pbs.twimg.com/media/Gm1.jpg?name=orig" {
		t.Fatalf("--kind image = (%+v, %v), want the photo planned", plan, err)
	}
	plan, err = PlanDownload(id100, mixed, "high", "")
	if err != nil || len(plan) != 1 || plan[0].Kind != kindVideo || plan[0].URL != "https://video.twimg.com/hi.mp4" {
		t.Fatalf("no filter = (%+v, %v), want the video to win", plan, err)
	}
	plan, err = PlanDownload(id100, mixed, "high", "gif")
	if err != nil || len(plan) != 0 {
		t.Errorf("--kind gif = (%+v, %v), want an empty plan, nil error", plan, err)
	}
}

// TestPlanDownloadKindImagePlansTheXdownCoverEntry: the cover-image entry
// R-M9-1 keeps in the resolution output is a legitimate image entry — an
// explicit --kind image on the xdown video tweet plans it, proxy fallback
// carried along.
func TestPlanDownloadKindImagePlansTheXdownCoverEntry(t *testing.T) {
	plan, err := PlanDownload(id100, xdownVideoTweet(), "high", "image")
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	if len(plan) != 1 || plan[0].Kind != kindImage {
		t.Fatalf("plan = %+v, want the single image entry", plan)
	}
	f := plan[0]
	if f.URL != "https://pbs.twimg.com/amplify_video_thumb/2070000000000000100/pu/img/pl.jpg?name=small" || f.Ext != ".jpg" || f.Seq != "1" {
		t.Errorf("file = %+v, want the rewritten cover entry", f)
	}
	if len(f.Fallbacks) != 1 || f.Fallbacks[0] != "https://xdown.app/proxy/img?token=t" {
		t.Errorf("Fallbacks = %v, want the entry's proxy link", f.Fallbacks)
	}
}

// TestPlanDownloadCover: --kind cover plans exactly the CoverURL file of a
// status with moving media — for the xdown shape (cover captured from the
// cover entry) and the fx shape (thumbnail_url) alike; a video without a
// cover and a pure image tweet refuse with not_found errors.
func TestPlanDownloadCover(t *testing.T) {
	plan, err := PlanDownload(id100, xdownVideoTweet(), "high", "cover")
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	if len(plan) != 1 {
		t.Fatalf("plan = %+v, want exactly the cover file", plan)
	}
	if plan[0].StatusID != id100 || plan[0].Seq != "1" || plan[0].Kind != "cover" ||
		plan[0].URL != "https://pbs.twimg.com/amplify_video_thumb/2070000000000000100/pu/img/pl.jpg?name=small" ||
		plan[0].Ext != ".jpg" || len(plan[0].Fallbacks) != 0 {
		t.Errorf("cover file = %+v, want Kind cover, the cover URL, ext .jpg, no fallbacks", plan[0])
	}

	plan, err = PlanDownload(id100, fxVideoProjection(), "high", "cover")
	if err != nil || len(plan) != 1 || plan[0].Kind != "cover" ||
		plan[0].URL != "https://pbs.twimg.com/ext_tw_video_thumb/100/pu/img/pl.jpg" {
		t.Errorf("fx cover plan = (%+v, %v), want the fx thumbnail as the cover file", plan, err)
	}

	noCover := []nitter.MediaResolution{
		{Ref: statusURL100, Source: "vx", Kind: kindVideo, URL: "https://video.twimg.com/v.mp4"},
	}
	_, err = PlanDownload(id100, noCover, "high", "cover")
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
		t.Errorf("cover without CoverURL err = %v, want KindNotFound", err)
	}

	_, err = PlanDownload(id100, fourImageTweet(), "high", "cover")
	if !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
		t.Errorf("cover on image tweet err = %v, want KindNotFound", err)
	}
}

// TestPlanDownloadPlaylistOnlyVideoIsNotFound: a video whose only encodings
// are HLS playlists offers no downloadable candidate — the plan refuses with
// a not_found error instead of planning a playlist.
func TestPlanDownloadPlaylistOnlyVideoIsNotFound(t *testing.T) {
	m3u8Only := []nitter.MediaResolution{
		{
			Ref: statusURL100, Source: "syndication", Kind: kindVideo,
			URL: "https://video.twimg.com/pl.m3u8",
			Variants: []nitter.MediaVariant{
				{URL: "https://video.twimg.com/pl.m3u8", ContentType: "application/x-mpegURL"},
			},
		},
	}
	_, err := PlanDownload(id100, m3u8Only, "high", "")
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
		t.Fatalf("err = %v, want KindNotFound", err)
	}
	if _, err := PlanDownload(id100, m3u8Only, "high", "video"); !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
		t.Errorf("--kind video err = %v, want KindNotFound", err)
	}
}

// TestPlanDownloadVariantlessVideoWithoutLabel: a bare direct video link (the
// vx/nitter shapes — no variants, no label) plans as-is with no fallbacks.
func TestPlanDownloadVariantlessVideoWithoutLabel(t *testing.T) {
	res := []nitter.MediaResolution{
		{Ref: statusURL100, Source: "nitter", Kind: kindVideo, URL: "https://nitter.internal:8080/video/EXVmp4.mp4"},
	}
	plan, err := PlanDownload(id100, res, "high", "")
	if err != nil || len(plan) != 1 {
		t.Fatalf("PlanDownload = (%+v, %v)", plan, err)
	}
	if plan[0].URL != "https://nitter.internal:8080/video/EXVmp4.mp4" || plan[0].Kind != kindVideo || plan[0].Ext != ".mp4" || len(plan[0].Fallbacks) != 0 {
		t.Errorf("file = %+v, want the instance link planned verbatim", plan[0])
	}
}

// TestPlanDownloadEmptyResolutionErrors: planning against nothing resolved
// is a not_found error, never an empty success.
func TestPlanDownloadEmptyResolutionErrors(t *testing.T) {
	for name, res := range map[string][]nitter.MediaResolution{"nil": nil, "empty": {}} {
		_, err := PlanDownload(id100, res, "high", "")
		var terr *nitter.Error
		if !errors.As(err, &terr) || terr.Kind != nitter.KindNotFound {
			t.Errorf("%s resolutions: err = %v, want KindNotFound", name, err)
		}
	}
}

// TestPlanDownloadUnknownKindFilterIsInvalidArg: a kind filter outside the
// contract values is a caller bug, classified invalid_argument.
func TestPlanDownloadUnknownKindFilterIsInvalidArg(t *testing.T) {
	_, err := PlanDownload(id100, fourImageTweet(), "high", "audio")
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindInvalidArg {
		t.Fatalf("err = %v, want KindInvalidArg", err)
	}
}

// TestPlanDownloadHTTPPolicyIsTheResolveLayer: selection neither re-filters
// (a hand-fed plain-http entry passes through verbatim — scheme policy
// belongs to the resolve layer that produced the input) nor reintroduces
// (planning the real resolve output of a payload carrying a plain-http photo
// keeps every planned link https).
func TestPlanDownloadHTTPPolicyIsTheResolveLayer(t *testing.T) {
	handFed := []nitter.MediaResolution{
		{Ref: statusURL100, Source: "vx", Kind: kindImage, URL: "http://pbs.twimg.com/media/GaB.jpg"},
	}
	plan, err := PlanDownload(id100, handFed, "high", "")
	if err != nil || len(plan) != 1 || plan[0].URL != "http://pbs.twimg.com/media/GaB.jpg" {
		t.Errorf("hand-fed plain-http entry = (%+v, %v), want it passed through verbatim (no re-filtering)", plan, err)
	}

	body := []byte(`{
	  "tweet": {"text": "x", "media": {"all": [
	    {"type": "photo", "url": "http://pbs.twimg.com/media/GaB.jpg"},
	    {"type": "video", "url": "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/pl.mp4",
	     "thumbnail_url": "http://pbs.twimg.com/ext_tw_video_thumb/100/pu/img/pl.jpg",
	     "variants": [
	       {"bitrate": 832000, "content_type": "video/mp4", "url": "https://video.twimg.com/ext_tw_video/100/pu/vid/480x848/832.mp4"},
	       {"bitrate": 2176000, "content_type": "video/mp4", "url": "https://video.twimg.com/ext_tw_video/100/pu/vid/720x1280/2176.mp4"}
	     ]}
	  ]}}
	}`)
	routes := map[string]fakeResp{fxURL100: {body: body, status: 200}}
	r, _ := newTestResolver(routes)
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyFx},
		Quality:    "low",
	})
	if err != nil {
		t.Fatalf("ResolveStatus: %v", err)
	}
	plan, err = PlanDownload(id100, res, "low", "")
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	if len(plan) != 1 || len(plan[0].Fallbacks) == 0 {
		t.Fatalf("plan = %+v, want the converged video with variant fallbacks", plan)
	}
	for _, u := range append([]string{plan[0].URL}, plan[0].Fallbacks...) {
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("planned link %q is not https — the M8 exclusion was reintroduced", u)
		}
	}
}

// TestExtFromURL: the extension comes from the URL path, percent-decoded, and
// only a plausible extension survives — the nitter strategy's wrapped link
// (/video/<token>/<encoded inner URL>) must not leak "?tag=29" into the
// filename.
func TestExtFromURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{"plain mp4", "https://video.twimg.com/a/b/x.mp4", ".mp4"},
		{"plain jpg", "https://pbs.twimg.com/media/x.jpg", ".jpg"},
		{"query tag ignored", "https://video.twimg.com/a/b/x.mp4?tag=29", ".mp4"},
		{
			"nitter wrapped link",
			"http://127.0.0.1:8080/video/DB49775EE674B/" +
				"https%3A%2F%2Fvideo.twimg.com%2Famplify_video%2F1%2Fvid%2Favc1%2F720x1280%2FZNXx.mp4%3Ftag%3D29",
			".mp4",
		},
		{"no extension", "https://example.com/path/noext", ""},
		{"trailing encoded query only", "https://example.com/x%3Ftag%3D29", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := extFromURL(tc.url); got != tc.want {
				t.Errorf("extFromURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
