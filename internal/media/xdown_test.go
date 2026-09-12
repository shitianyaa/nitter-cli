package media

// xdown.app parsing tests: the ajaxSearch POST shape, download-button HTML
// extraction (kind by label text, then URL suffix), snapcdn token decoding
// (direct link preferred, proxy link as fallback) and duration extraction.
// Fixtures mirror the plugin's parser expectations (media_support/xdown.py,
// service.py:397-465 and video_probe.py's xdown_token_payload); the
// committed xdown_page.html is the single source of the HTML shape and the
// ajaxSearch envelope is wrapped around it here.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/sdk"
)

// xdownSearchRoute is the endpoint the plugin POSTs the status URL to.
const xdownSearchRoute = "https://xdown.app/api/ajaxSearch"

// The fixture page's two snapcdn tokens (their middle base64url segments
// decode to {"url":...,"filename":...[, "length":95]} payload JSON).
const (
	fixtureImageToken = "eyJhbGciOiJIUzI1NiJ9.eyJ1cmwiOiJodHRwczovL3Bicy50d2ltZy5jb20vbWVkaWEvR3gxYWJjLmpwZz9uYW1lPXNtYWxsIiwiZmlsZW5hbWUiOiJHeDFhYmMuanBnIn0.s1gn4tur3"
	fixtureVideoToken = "eyJhbGciOiJIUzI1NiJ9.eyJ1cmwiOiJodHRwczovL3Bicy50d2ltZy5jb20vdmlkLzIwNzAwMDAwMDAwMDAwMDAxMDAveC5tcDQiLCJmaWxlbmFtZSI6IngubXA0IiwibGVuZ3RoIjo5NX0.s1gn4tur3"
)

// xdownOK wraps one HTML page into the ajaxSearch success envelope
// ({status:"ok", data:"<html>"}), keeping xdown_page.html the single source
// of the HTML shape.
func xdownOK(t *testing.T, html string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{"status": "ok", "data": html})
	if err != nil {
		t.Fatalf("build xdown envelope: %v", err)
	}
	return body
}

// xdownTestToken builds a JWT-shaped token (header.payload.sig) whose middle
// segment is the base64url of payload — the shape the fixture's literal
// tokens and the real xdown proxy links use.
func xdownTestToken(t *testing.T, payload string) string {
	t.Helper()
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s1g"
}

// TestResolveXdownFixturePage runs the whole pipeline over the fixture page:
// ordering, kinds, labels, token-derived direct/fallback URL split, duration
// extraction from the label clock and the token payload, the pbs quality
// rewrite on the image direct link, and the skipped unidentifiable entry.
// Resolution extraction is deliberately absent (R-M8-5): Width/Height stay
// zero — the "720p"-style text lives in the label, as in the plugin.
func TestResolveXdownFixturePage(t *testing.T) {
	r, fake := newTestResolver(nil)
	fake.postRoutes = map[string]fakeResp{
		xdownSearchRoute: {body: xdownOK(t, string(readFixture(t, "xdown_page.html"))), status: 200},
	}
	res, err := r.ResolveXdown(context.Background(), mustRef(t, statusURL100), Options{})
	if err != nil {
		t.Fatalf("ResolveXdown: %v", err)
	}
	want := []twitter.MediaResolution{
		{
			Ref: statusURL100, Source: "xdown", Kind: "video",
			URL:             "https://video.twimg.com/ext_tw_video/2070000000000000100/pu/vid/720x1280/a1.mp4",
			Label:           "下载 MP4 720p (1:23)",
			DurationSeconds: 83,
		},
		{
			Ref: statusURL100, Source: "xdown", Kind: "video",
			URL:   "https://video.twimg.com/ext_tw_video/2070000000000000100/pu/vid/1080x1920/b1.mp4",
			Label: "下载 MP4 1080p",
		},
		{
			Ref: statusURL100, Source: "xdown", Kind: "gif",
			URL:   "https://video.twimg.com/tweet_video/c1.gif",
			Label: "下载 GIF 动图",
		},
		{
			Ref: statusURL100, Source: "xdown", Kind: "image",
			URL:         "https://pbs.twimg.com/media/Gx1abc.jpg?name=orig",
			Label:       "下载图片",
			FallbackURL: "https://xdown.app/proxy/img?token=" + fixtureImageToken,
		},
		{
			Ref: statusURL100, Source: "xdown", Kind: "video",
			URL:             "https://pbs.twimg.com/vid/2070000000000000100/x.mp4",
			Label:           "下载 MP4",
			DurationSeconds: 95,
			FallbackURL:     "https://xdown.app/download?token=" + fixtureVideoToken,
		},
	}
	if len(res) != len(want) {
		t.Fatalf("resolutions = %d (%+v), want %d — the unidentifiable entry and non-button anchors must be skipped", len(res), res, len(want))
	}
	for i, w := range want {
		if !reflect.DeepEqual(res[i], w) {
			t.Errorf("res[%d] = %+v, want %+v", i, res[i], w)
		}
	}
	if len(fake.postCalls) != 1 || len(fake.calls) != 0 {
		t.Errorf("requests: %d POST / %d GET, want exactly one POST and no GET", len(fake.postCalls), len(fake.calls))
	}
}

// TestResolveXdownRequestShape pins the ajaxSearch wire shape: POST to
// /api/ajaxSearch with the urlencoded q/lang body (the ref uses the /i/
// segment when the caller has no user) and the plugin's header set.
func TestResolveXdownRequestShape(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		wantBody string
	}{
		{
			"user-less ref uses /i/",
			"2070000000000000100",
			"lang=zh-cn&q=https%3A%2F%2Fx.com%2Fi%2Fstatus%2F2070000000000000100",
		},
		{
			"user ref keeps the handle",
			statusURL100,
			"lang=zh-cn&q=https%3A%2F%2Fx.com%2Fnasa%2Fstatus%2F2070000000000000100",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, fake := newTestResolver(nil)
			fake.postRoutes = map[string]fakeResp{xdownSearchRoute: {body: xdownOK(t, ""), status: 200}}
			res, err := r.ResolveXdown(context.Background(), mustRef(t, tc.ref), Options{})
			if err != nil {
				t.Fatalf("ResolveXdown: %v", err)
			}
			if len(res) != 0 {
				t.Fatalf("resolutions = %+v, want none for an empty page", res)
			}
			if len(fake.postCalls) != 1 || fake.postCalls[0] != xdownSearchRoute {
				t.Fatalf("post calls = %v, want [%s]", fake.postCalls, xdownSearchRoute)
			}
			if fake.postBodies[0] != tc.wantBody {
				t.Errorf("body = %q, want %q", fake.postBodies[0], tc.wantBody)
			}
			h := fake.postSent[0]
			if h["Content-Type"] != "application/x-www-form-urlencoded" {
				t.Errorf("Content-Type = %q", h["Content-Type"])
			}
			if h["Referer"] != "https://xdown.app/" {
				t.Errorf("Referer = %q", h["Referer"])
			}
			if h["Origin"] != "https://xdown.app" {
				t.Errorf("Origin = %q", h["Origin"])
			}
			if h["Accept"] != "application/json, text/plain, */*" {
				t.Errorf("Accept = %q", h["Accept"])
			}
			if h["User-Agent"] == "" || h["Accept-Language"] == "" {
				t.Errorf("browser identity headers missing: UA=%q AL=%q", h["User-Agent"], h["Accept-Language"])
			}
		})
	}
}

// TestResolveXdownStatusNotOKIsEmpty: a non-ok status (and an ok payload
// without data) yield an empty result with no error — the next-strategy
// semantics ResolveStatus applies.
func TestResolveXdownStatusNotOKIsEmpty(t *testing.T) {
	for name, body := range map[string]string{
		"error status": `{"status":"error","data":"<a class=\"tw-button-dl\" href=\"https://v/x.mp4\">MP4</a>"}`,
		"ok, no data":  `{"status":"ok"}`,
		"null data":    `{"status":"ok","data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			r, fake := newTestResolver(nil)
			fake.postRoutes = map[string]fakeResp{xdownSearchRoute: {body: []byte(body), status: 200}}
			res, err := r.ResolveXdown(context.Background(), mustRef(t, statusURL100), Options{})
			if err != nil {
				t.Fatalf("ResolveXdown: %v", err)
			}
			if len(res) != 0 {
				t.Fatalf("resolutions = %+v, want none", res)
			}
		})
	}
}

// TestResolveXdownMalformedIsKindMalformed: bodies that are not JSON objects
// classify as KindMalformed.
func TestResolveXdownMalformedIsKindMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"garbage":    `not json`,
		"json array": `[]`,
		"json null":  `null`,
	} {
		t.Run(name, func(t *testing.T) {
			r, fake := newTestResolver(nil)
			fake.postRoutes = map[string]fakeResp{xdownSearchRoute: {body: []byte(body), status: 200}}
			_, err := r.ResolveXdown(context.Background(), mustRef(t, statusURL100), Options{})
			var terr *twitter.Error
			if !errors.As(err, &terr) || terr.Kind != twitter.KindMalformed {
				t.Fatalf("err = %v, want KindMalformed", err)
			}
			if terr.Op != "media.xdown" {
				t.Errorf("Op = %q, want media.xdown", terr.Op)
			}
		})
	}
}

// TestResolveStatusXdownFailureNamesStrategy: a transport failure inside the
// resolve loop reports the xdown strategy with its classified kind (the fake
// classifies missing routes as HTTP 404), never another strategy's name.
func TestResolveStatusXdownFailureNamesStrategy(t *testing.T) {
	r, _ := newTestResolver(nil) // no routes at all: xdown POST answers 404
	_, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyXdown},
	})
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindNotFound {
		t.Fatalf("err = %v, want KindNotFound", err)
	}
	if !strings.Contains(err.Error(), "xdown:") {
		t.Errorf("message %q misses %q", err.Error(), "xdown:")
	}
}

// TestResolveXdownQualityRewritesImageDirect: the quality tier rewrites the
// DIRECT image link (the token payload's pbs URL), the video link stays
// untouched, and the snapcdn fallback keeps standing in for the direct link.
func TestResolveXdownQualityRewritesImageDirect(t *testing.T) {
	tok := xdownTestToken(t, `{"url":"https://pbs.twimg.com/media/Gx1abc.jpg?name=orig","filename":"Gx1abc.jpg"}`)
	html := `<a href="/proxy/img?token=` + tok + `" class="abutton">下载图片</a>` +
		`<a href="https://video.twimg.com/v.mp4" class="tw-button-dl">下载 MP4</a>`
	r, fake := newTestResolver(nil)
	fake.postRoutes = map[string]fakeResp{xdownSearchRoute: {body: xdownOK(t, html), status: 200}}
	res, err := r.ResolveXdown(context.Background(), mustRef(t, statusURL100), Options{Quality: "medium"})
	if err != nil {
		t.Fatalf("ResolveXdown: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("resolutions = %+v, want image then video", res)
	}
	if res[0].Kind != "image" || res[0].URL != "https://pbs.twimg.com/media/Gx1abc.jpg?name=large" {
		t.Errorf("image = %+v, want the direct link rewritten to name=large", res[0])
	}
	if res[0].FallbackURL != "https://xdown.app/proxy/img?token="+tok {
		t.Errorf("image fallback = %q, want the snapcdn proxy link", res[0].FallbackURL)
	}
	if res[1].Kind != "video" || res[1].URL != "https://video.twimg.com/v.mp4" {
		t.Errorf("video = %+v, want the untouched direct mp4", res[1])
	}
}

// TestResolveXdownKindDetection pins the two-stage kind decision (label text
// first, URL suffix fallback) and what gets skipped: unknown kinds, empty
// hrefs and non-button anchors.
func TestResolveXdownKindDetection(t *testing.T) {
	html := `<a href="https://v.twimg.com/1.mp4" class="tw-button-dl">Download VIDEO 480p</a>` +
		`<a href="https://v.twimg.com/2.mp4" class="abutton">MP4 DOWNLOAD</a>` +
		`<a href="https://v.twimg.com/3.webm" class="tw-button-dl">下载</a>` +
		`<a href="https://v.twimg.com/4.gif" class="tw-button-dl">动图</a>` +
		`<a href="https://v.twimg.com/5.png" class="tw-button-dl">截图</a>` +
		`<a href="https://v.twimg.com/6.mov?dl=1" class="tw-button-dl">下载</a>` +
		`<a href="https://v.twimg.com/7.jpg" class="tw-button-dl abutton">下载</a>` +
		`<a href="/dl/abc" class="tw-button-dl">下载文件</a>` +
		`<a href="" class="tw-button-dl">下载 MP4</a>` +
		`<a href="/page">普通链接 MP4</a>`
	r, fake := newTestResolver(nil)
	fake.postRoutes = map[string]fakeResp{xdownSearchRoute: {body: xdownOK(t, html), status: 200}}
	res, err := r.ResolveXdown(context.Background(), mustRef(t, statusURL100), Options{})
	if err != nil {
		t.Fatalf("ResolveXdown: %v", err)
	}
	if len(res) != 7 {
		t.Fatalf("resolutions = %d (%+v), want 7", len(res), res)
	}
	wantKinds := []string{kindVideo, kindVideo, kindVideo, kindGif, kindImage, kindVideo, kindImage}
	wantURLs := []string{
		"https://v.twimg.com/1.mp4", "https://v.twimg.com/2.mp4", "https://v.twimg.com/3.webm",
		"https://v.twimg.com/4.gif", "https://v.twimg.com/5.png", "https://v.twimg.com/6.mov?dl=1",
		"https://v.twimg.com/7.jpg",
	}
	for i := range wantKinds {
		if res[i].Kind != wantKinds[i] {
			t.Errorf("res[%d].kind = %q, want %q", i, res[i].Kind, wantKinds[i])
		}
		if res[i].URL != wantURLs[i] {
			t.Errorf("res[%d].url = %q, want %q", i, res[i].URL, wantURLs[i])
		}
	}
}

// TestResolveXdownTokenDirectVsProxy: only an https direct link in the token
// payload becomes the main URL (with the proxy as fallback); plain-http
// payloads, payloads without a url and undecodable tokens keep the snapcdn
// link as the main URL with no fallback.
func TestResolveXdownTokenDirectVsProxy(t *testing.T) {
	tokHTTP := xdownTestToken(t, `{"url":"http://pbs.twimg.com/media/H.jpg","filename":"H.jpg"}`)
	tokNoURL := xdownTestToken(t, `{"filename":"n.jpg"}`)
	html := `<a href="/h?token=` + tokHTTP + `" class="tw-button-dl">下载图片</a>` +
		`<a href="/n?token=` + tokNoURL + `" class="tw-button-dl">下载图片</a>` +
		`<a href="/d?notoken=1" class="tw-button-dl">下载 MP4</a>` +
		`<a href="/x?token=notajwt" class="tw-button-dl">下载 MP4</a>`
	r, fake := newTestResolver(nil)
	fake.postRoutes = map[string]fakeResp{xdownSearchRoute: {body: xdownOK(t, html), status: 200}}
	res, err := r.ResolveXdown(context.Background(), mustRef(t, statusURL100), Options{})
	if err != nil {
		t.Fatalf("ResolveXdown: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("resolutions = %+v, want 4", res)
	}
	for i, wantURL := range []string{
		"https://xdown.app/h?token=" + tokHTTP,
		"https://xdown.app/n?token=" + tokNoURL,
		"https://xdown.app/d?notoken=1",
		"https://xdown.app/x?token=notajwt",
	} {
		if res[i].URL != wantURL {
			t.Errorf("res[%d].url = %q, want the snapcdn link %q", i, res[i].URL, wantURL)
		}
		if res[i].FallbackURL != "" {
			t.Errorf("res[%d].fallback = %q, want empty without an https direct link", i, res[i].FallbackURL)
		}
	}
}

// TestResolveXdownDedupesByFinalURL: two buttons whose tokens carry the same
// direct link collapse onto the first entry, document order preserved.
func TestResolveXdownDedupesByFinalURL(t *testing.T) {
	tok1 := xdownTestToken(t, `{"url":"https://pbs.twimg.com/media/Dup.jpg?name=small"}`)
	tok2 := xdownTestToken(t, `{"url":"https://pbs.twimg.com/media/Dup.jpg?name=large"}`)
	html := `<a href="/a?token=` + tok1 + `" class="tw-button-dl">下载图片</a>` +
		`<a href="/b?token=` + tok2 + `" class="abutton">下载图片</a>`
	r, fake := newTestResolver(nil)
	fake.postRoutes = map[string]fakeResp{xdownSearchRoute: {body: xdownOK(t, html), status: 200}}
	res, err := r.ResolveXdown(context.Background(), mustRef(t, statusURL100), Options{})
	if err != nil {
		t.Fatalf("ResolveXdown: %v", err)
	}
	if len(res) != 1 || res[0].URL != "https://pbs.twimg.com/media/Dup.jpg?name=orig" {
		t.Fatalf("resolutions = %+v, want one entry deduped on the rewritten URL", res)
	}
	if res[0].FallbackURL != "https://xdown.app/a?token="+tok1 {
		t.Errorf("fallback = %q, want the FIRST entry's proxy link", res[0].FallbackURL)
	}
}

// TestXdownDurationExtraction ports video_probe's duration_from_text and
// duration_from_mapping tables: clock forms with digit-boundary guards,
// "N seconds/minutes" text, the payload key precedence and value coercion.
func TestXdownDurationExtraction(t *testing.T) {
	t.Run("text forms", func(t *testing.T) {
		cases := []struct {
			name string
			text string
			want float64
			ok   bool
		}{
			{"m:ss", "1:23", 83, true},
			{"h:mm:ss", "1:02:03", 3723, true},
			{"zero minutes", "0:59", 59, true},
			{"seconds word", "90 seconds", 90, true},
			{"secs", "45 secs", 45, true},
			{"minutes", "2 minutes", 120, true},
			{"fractional minutes", "1.5 minutes", 90, true},
			{"trailing digits block the clock", "12:345", 0, false},
			{"leading digits block the clock", "912:34", 0, false},
			{"no duration", "下载 MP4", 0, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := durationFromText(tc.text)
				if ok != tc.ok || got != tc.want {
					t.Errorf("durationFromText(%q) = (%v, %v), want (%v, %v)", tc.text, got, ok, tc.want, tc.ok)
				}
			})
		}
	})
	t.Run("payload mapping", func(t *testing.T) {
		cases := []struct {
			name    string
			payload map[string]any
			want    float64
			ok      bool
		}{
			{"length key", map[string]any{"length": 95.0}, 95, true},
			{"duration key first", map[string]any{"duration": 61.0, "length": 95.0}, 61, true},
			{"zero falls through", map[string]any{"duration": 0.0, "length_seconds": 12.5}, 12.5, true},
			{"string number", map[string]any{"length": "12.5"}, 12.5, true},
			{"text value", map[string]any{"duration": "1:30", "length": 95.0}, 90, true},
			{"non-positive ignored", map[string]any{"length": -3.0}, 0, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := durationFromMapping(tc.payload)
				if ok != tc.ok || got != tc.want {
					t.Errorf("durationFromMapping = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.ok)
				}
			})
		}
	})
	t.Run("xdownDuration precedence", func(t *testing.T) {
		// The payload's own duration keys win over the label's clock text.
		if d := xdownDuration("1:23", "https://xdown.app/d?token=x", map[string]any{"length": 95.0}); d != 95 {
			t.Errorf("payload length = %v, want 95", d)
		}
		if d := xdownDuration("1:23", "https://xdown.app/d", nil); d != 83 {
			t.Errorf("label clock = %v, want 83", d)
		}
	})
}

// TestResolveStatusUsesXdownAfterFxEmpty: the resolve loop reaches xdown when
// fx reports empty, and stamps the winning source and canonical ref.
func TestResolveStatusUsesXdownAfterFxEmpty(t *testing.T) {
	r, fake := newTestResolver(map[string]fakeResp{
		fxURL100: {body: readFixture(t, "fx_text_only.json"), status: 200},
	})
	fake.postRoutes = map[string]fakeResp{
		xdownSearchRoute: {body: xdownOK(t, string(readFixture(t, "xdown_page.html"))), status: 200},
	}
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyFx, StrategyXdown},
	})
	if err != nil {
		t.Fatalf("ResolveStatus: %v", err)
	}
	if len(res) != 5 {
		t.Fatalf("resolutions = %d, want the fixture's 5 entries", len(res))
	}
	for _, m := range res {
		if m.Source != "xdown" {
			t.Errorf("source = %q, want xdown", m.Source)
		}
		if m.Ref != statusURL100 {
			t.Errorf("ref = %q, want %q", m.Ref, statusURL100)
		}
	}
	if len(fake.calls) != 1 || len(fake.postCalls) != 1 {
		t.Errorf("requests = %d GET / %d POST, want one of each (vx/syndication never reached)", len(fake.calls), len(fake.postCalls))
	}
}
