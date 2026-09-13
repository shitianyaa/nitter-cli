package media

// ResolveNitter tests: the user's own Nitter instance is the trusted source,
// so this strategy keeps instance-served (possibly plain-http) links that the
// third-party strategies' https-only rule would drop. The fake status page
// mirrors the markup internal/nitter/html parses in production (still-image
// pic proxies, pbs direct links, /video/ placeholders).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// compile-time pin: ResolveNitter produces the sdk (package nitter) contract
// type the command layer renders.

// nitterStatusPage builds a focused Nitter status page whose main item
// carries an instance pic-proxy image, a pbs direct image and a /video/
// placeholder link — the shapes ParseStatus projects into Tweet.Media.
func nitterStatusPage(instanceMedia string) string {
	return `<div class="timeline">` +
		`<div class="timeline-item">` +
		`<a class="tweet-link" href="/nasa/status/2070000000000000100#m"></a>` +
		`<div class="tweet-content">with media</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="attachments">` + instanceMedia + `</div>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
}

// nitterFixture is the page the projection tests parse (a var: the page is
// assembled by nitterStatusPage, not a constant expression).
var nitterFixture = nitterStatusPage(
	`<a class="still-image" href="/pic/orig/media%2FAAA.jpg">img</a>` +
		`<a class="still-image" href="https://pbs.twimg.com/media/BBB.jpg?format=jpg&name=small">orig</a>` +
		`<a class="video-container" href="/video/EXVmp4.mp4">video</a>`)

func TestResolveNitterProjectsInstanceMedia(t *testing.T) {
	r, _ := newTestResolver(map[string]fakeResp{
		"https://nitter.internal:8080/nasa/status/" + id100: {body: []byte(nitterFixture), status: 200},
	})
	res, err := r.ResolveNitter(context.Background(), mustRef(t, statusURL100), Options{NitterBase: "https://nitter.internal:8080", Quality: "low"})
	if err != nil {
		t.Fatalf("ResolveNitter: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("got %d resolutions, want 3: %+v", len(res), res)
	}
	// The instance pic-proxy link stays verbatim (no pbs rewrite applies to a
	// proxy path; the tier is the path segment Nitter rendered). The instance
	// is the user's own trusted host: a plain-http base would be kept too.
	if res[0].Kind != "image" || !strings.HasSuffix(res[0].URL, "/pic/orig/media%2FAAA.jpg") {
		t.Errorf("res[0] = %+v, want the instance pic-proxy image", res[0])
	}
	// The pbs direct link goes through the quality rewrite (low → name=small,
	// already small here — pin it stays a direct pbs URL).
	if res[1].Kind != "image" || !strings.Contains(res[1].URL, "https://pbs.twimg.com/media/BBB.jpg") {
		t.Errorf("res[1] = %+v, want the pbs direct image", res[1])
	}
	// The /video/ placeholder is absolutized against the instance and kept
	// (Nitter carries no variant metadata).
	if res[2].Kind != "video" || res[2].URL != "https://nitter.internal:8080/video/EXVmp4.mp4" {
		t.Errorf("res[2] = %+v, want the instance /video/ link", res[2])
	}
	for i := range res {
		if res[i].Ref != statusURL100 {
			t.Errorf("res[%d].Ref = %q, want %q", i, res[i].Ref, statusURL100)
		}
		if res[i].Source != string(StrategyNitter) {
			t.Errorf("res[%d].Source = %q, want %q", i, res[i].Source, StrategyNitter)
		}
	}
}

func TestResolveNitterPlainHTTPBaseKept(t *testing.T) {
	// A LAN instance on plain http is the normal self-hosted shape: its links
	// must survive (the third-party https-only rule does not apply to the
	// user's own instance).
	page := nitterStatusPage(`<a class="video-container" href="/video/EXVmp4.mp4">video</a>`)
	r, _ := newTestResolver(map[string]fakeResp{
		"http://127.0.0.1:8080/status/" + id100: {body: []byte(page), status: 200},
	})
	res, err := r.ResolveNitter(context.Background(), mustRef(t, id100), Options{NitterBase: "http://127.0.0.1:8080/"})
	if err != nil {
		t.Fatalf("ResolveNitter: %v", err)
	}
	if len(res) != 1 || res[0].URL != "http://127.0.0.1:8080/video/EXVmp4.mp4" {
		t.Fatalf("res = %+v, want the plain-http instance /video/ link", res)
	}
	if res[0].Source != "nitter" {
		t.Errorf("Source = %q, want nitter", res[0].Source)
	}
}

func TestResolveNitterUserlessRouteAndQualityRewrite(t *testing.T) {
	page := nitterStatusPage(`<a class="still-image" href="https://pbs.twimg.com/media/BBB.jpg?format=jpg&name=small">orig</a>`)
	r, fake := newTestResolver(map[string]fakeResp{
		"https://nitter.internal:8080/status/" + id100: {body: []byte(page), status: 200},
	})
	res, err := r.ResolveNitter(context.Background(), mustRef(t, id100), Options{NitterBase: "https://nitter.internal:8080", Quality: "high"})
	if err != nil {
		t.Fatalf("ResolveNitter: %v", err)
	}
	if len(res) != 1 || res[0].URL != "https://pbs.twimg.com/media/BBB.jpg?format=jpg&name=orig" {
		t.Fatalf("res = %+v, want the pbs link rewritten to name=orig", res)
	}
	if got := fake.calls; len(got) != 1 || got[0] != "https://nitter.internal:8080/status/"+id100 {
		t.Errorf("calls = %v, want the user-less route only", got)
	}
}

func TestResolveNitterNoBaseIsLocalState(t *testing.T) {
	r, fake := newTestResolver(nil)
	_, err := r.ResolveNitter(context.Background(), mustRef(t, statusURL100), Options{})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindLocalState {
		t.Fatalf("err = %v, want KindLocalState", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("calls = %v, want none (no base, no fetch)", fake.calls)
	}
	// The auto chain surfaces the reason in its aggregate: nitter is reported
	// with its real name, never silently skipped.
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyNitter, StrategyVx},
		NitterBase: "",
	})
	if err == nil || !strings.Contains(err.Error(), "nitter") || !strings.Contains(err.Error(), "no nitter instance") {
		t.Errorf("ResolveStatus err = %v, want the aggregate to name the nitter failure", err)
	}
	if res != nil {
		t.Errorf("res = %+v, want nil on aggregate failure", res)
	}
}

func TestResolveNitterErrorPageClassified(t *testing.T) {
	r, _ := newTestResolver(map[string]fakeResp{
		"https://nitter.internal:8080/nasa/status/" + id100: {body: []byte(`<div class="error-panel">Wrong</div>`), status: 200},
	})
	_, err := r.ResolveNitter(context.Background(), mustRef(t, statusURL100), Options{NitterBase: "https://nitter.internal:8080"})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindUnavailable {
		t.Fatalf("err = %v, want the error-panel KindUnavailable classification", err)
	}
}

func TestResolveNitterWrongStatusIsMalformed(t *testing.T) {
	r, _ := newTestResolver(map[string]fakeResp{
		// The page served for the requested path carries a DIFFERENT focused
		// status id: the projection must refuse rather than return its media.
		"https://nitter.internal:8080/nasa/status/2070000000000000999": {body: []byte(nitterStatusPage(`<a class="video-container" href="/video/EXVmp4.mp4">video</a>`)), status: 200},
	})
	ref := StatusRef{ID: "2070000000000000999", User: "nasa"}
	_, err := r.ResolveNitter(context.Background(), ref, Options{NitterBase: "https://nitter.internal:8080"})
	var terr *nitter.Error
	if !errors.As(err, &terr) || terr.Kind != nitter.KindMalformed {
		t.Fatalf("err = %v, want KindMalformed", err)
	}
}

func TestResolveNitterEmptyPageIsEmpty(t *testing.T) {
	// A text-only status parses fine and carries no media: empty slice, nil
	// error — the next-strategy semantics of ResolveStatus.
	page := `<div class="timeline"><div class="timeline-item">` +
		`<a class="tweet-link" href="/nasa/status/` + id100 + `#m"></a>` +
		`<div class="tweet-content">text only</div>` +
		`<span class="tweet-date"><a title="Jul 20, 2026 · 2:11 PM UTC">Jul 20</a></span>` +
		`<div class="tweet-stats"><span class="tweet-stat">1</span></div>` +
		`</div></div>`
	r, _ := newTestResolver(map[string]fakeResp{
		"https://nitter.internal:8080/nasa/status/" + id100: {body: []byte(page), status: 200},
	})
	res, err := r.ResolveNitter(context.Background(), mustRef(t, statusURL100), Options{NitterBase: "https://nitter.internal:8080"})
	if err != nil {
		t.Fatalf("ResolveNitter: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("res = %+v, want empty", res)
	}
}

func TestResolveStatusAutoUsesNitterBase(t *testing.T) {
	// The wiring fills Options.NitterBase; the auto-position nitter strategy
	// must see it and win when the earlier strategies come up empty.
	page := nitterStatusPage(`<a class="video-container" href="/video/EXVmp4.mp4">video</a>`)
	r, fake := newTestResolver(map[string]fakeResp{
		fxURL100:  {body: []byte(`{"text":"hi"}`), status: 200},
		vxURL100:  {body: []byte(`{"media_extended":[]}`), status: 200},
		syndURL10: {body: []byte(`{"photos":[]}`), status: 200},
		"https://nitter.internal:8080/nasa/status/" + id100: {body: []byte(page), status: 200},
	})
	res, err := r.ResolveStatus(context.Background(), mustRef(t, statusURL100), Options{
		Strategies: []Strategy{StrategyFx, StrategyVx, StrategySyndication, StrategyNitter, StrategyXdown},
		NitterBase: "https://nitter.internal:8080",
	})
	if err != nil {
		t.Fatalf("ResolveStatus: %v", err)
	}
	if len(res) != 1 || res[0].Source != "nitter" || !strings.HasSuffix(res[0].URL, "/video/EXVmp4.mp4") {
		t.Fatalf("res = %+v, want the nitter /video/ resolution", res)
	}
	last := fake.calls[len(fake.calls)-1]
	if last != "https://nitter.internal:8080/nasa/status/"+id100 {
		t.Errorf("last call = %q, want the nitter status page", last)
	}
	if len(fake.postCalls) != 0 {
		t.Errorf("postCalls = %v, want none (nitter won before xdown)", fake.postCalls)
	}
}

func TestResolveNitterWidthHeightCarried(t *testing.T) {
	// The HTML parser's Media projection carries no dimensions; the
	// resolution must not fabricate any.
	page := nitterStatusPage(`<a class="still-image" href="/pic/orig/media%2FAAA.jpg">img</a>`)
	r, _ := newTestResolver(map[string]fakeResp{
		"https://nitter.internal:8080/nasa/status/" + id100: {body: []byte(page), status: 200},
	})
	res, err := r.ResolveNitter(context.Background(), mustRef(t, statusURL100), Options{NitterBase: "https://nitter.internal:8080"})
	if err != nil {
		t.Fatalf("ResolveNitter: %v", err)
	}
	if len(res) != 1 || res[0].Width != 0 || res[0].Height != 0 {
		t.Fatalf("res = %+v, want one resolution without dimensions", res)
	}
	var _ nitter.MediaResolution = res[0]
}

func TestResolveNitterCoverIsBestEffortEmpty(t *testing.T) {
	// The status page parse yields no video poster metadata, so the
	// projection leaves CoverURL empty — the best-effort capture rule; a
	// cover is never fabricated for the nitter strategy.
	page := nitterStatusPage(`<a class="video-container" href="/video/EXVmp4.mp4">video</a>`)
	r, _ := newTestResolver(map[string]fakeResp{
		"https://nitter.internal:8080/nasa/status/" + id100: {body: []byte(page), status: 200},
	})
	res, err := r.ResolveNitter(context.Background(), mustRef(t, statusURL100), Options{NitterBase: "https://nitter.internal:8080"})
	if err != nil {
		t.Fatalf("ResolveNitter: %v", err)
	}
	if len(res) != 1 || res[0].Kind != "video" || res[0].CoverURL != "" {
		t.Fatalf("res = %+v, want the video resolution with an empty CoverURL", res)
	}
}
