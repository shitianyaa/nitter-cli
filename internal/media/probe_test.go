package media

// Probe tests: the mp4 box walk over programmatically built fixture boxes,
// the Content-Range total table, and Probe's behavior over both the injected
// header-bearing fetch seam and the real httpx transport against httptest
// servers. HTTP classification itself (retry/pacing/kinds) is httpx's tested
// contract; the seam reproduces only GetMeta's (body, status, response
// headers, classified-error) surface — canned statuses arrive without a
// classification, so canned failures carry their classified error in `err`.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/twitter-cli/sdk"
)

// ---------------------------------------------------------------------------
// mp4 fixture builders
// ---------------------------------------------------------------------------

// mp4Box builds one ISO-BMFF box: a 4-byte big-endian size (header included),
// the 4-byte type and the payload.
func mp4Box(boxType string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[:4], uint32(len(b)))
	copy(b[4:8], boxType)
	copy(b[8:], payload)
	return b
}

// mp4Box64 builds a size==1 box: the 32-bit size field is 1 and the real
// size rides in the following 8 bytes (largesize).
func mp4Box64(boxType string, payload []byte) []byte {
	b := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(b[:4], 1)
	copy(b[4:8], boxType)
	binary.BigEndian.PutUint64(b[8:16], uint64(len(b)))
	copy(b[16:], payload)
	return b
}

// mp4BoxZeroSize builds a trailing box whose 32-bit size is 0 (the box
// extends to the end of the window).
func mp4BoxZeroSize(boxType string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[:4], 0)
	copy(b[4:8], boxType)
	copy(b[8:], payload)
	return b
}

const (
	// fixtureTimescale / fixtureDurationUnits yield the exact float 10.5 s.
	fixtureTimescale     = 100
	fixtureDurationUnits = 1050
	wantFixtureDuration  = 10.5
)

// mvhdBodyV0 builds a full version-0 movie header body (100 bytes, like a
// real mvhd): version/flags, creation/modification, timescale at 12:16,
// duration at 16:20, then rate/volume/reserved padding.
func mvhdBodyV0(timescale, duration uint32) []byte {
	b := make([]byte, 100)
	binary.BigEndian.PutUint32(b[12:16], timescale)
	binary.BigEndian.PutUint32(b[16:20], duration)
	return b
}

// mvhdBodyV1 builds a full version-1 movie header body (112 bytes): 64-bit
// creation/modification, 32-bit timescale at 20:24, 64-bit duration at
// 24:32, then padding.
func mvhdBodyV1(timescale uint32, duration uint64) []byte {
	b := make([]byte, 112)
	b[0] = 1 // version 1
	binary.BigEndian.PutUint32(b[20:24], timescale)
	binary.BigEndian.PutUint64(b[24:32], duration)
	return b
}

// probeMP4Head assembles the head a faststart mp4 serves: ftyp, then moov
// carrying the mvhd, then the start of mdat.
func probeMP4Head() []byte {
	ftyp := mp4Box("ftyp", []byte("isomiso2avc1mp41"))
	moov := mp4Box("moov", mp4Box("mvhd", mvhdBodyV0(fixtureTimescale, fixtureDurationUnits)))
	mdat := mp4Box("mdat", bytes.Repeat([]byte{0xAB}, 64))
	out := make([]byte, 0, len(ftyp)+len(moov)+len(mdat))
	out = append(out, ftyp...)
	out = append(out, moov...)
	out = append(out, mdat...)
	return out
}

// ---------------------------------------------------------------------------
// box walk
// ---------------------------------------------------------------------------

func TestFindMP4Duration(t *testing.T) {
	t.Run("mvhd v0 inside moov after ftyp", func(t *testing.T) {
		data := probeMP4Head()
		if got := findMP4Duration(data, 0, len(data)); got != wantFixtureDuration {
			t.Errorf("duration = %v, want %v", got, wantFixtureDuration)
		}
	})

	t.Run("mvhd v1 reads the 64-bit duration", func(t *testing.T) {
		// 5e9 units / 1e6 per second = 5000 s — beyond any 32-bit duration.
		moov := mp4Box("moov", mp4Box("mvhd", mvhdBodyV1(1_000_000, 5_000_000_000)))
		data := append(mp4Box("ftyp", []byte("isom")), moov...)
		if got := findMP4Duration(data, 0, len(data)); got != 5000 {
			t.Errorf("duration = %v, want 5000", got)
		}
	})

	t.Run("top-level mvhd without a moov container", func(t *testing.T) {
		data := mp4Box("mvhd", mvhdBodyV0(fixtureTimescale, fixtureDurationUnits))
		if got := findMP4Duration(data, 0, len(data)); got != wantFixtureDuration {
			t.Errorf("duration = %v, want %v", got, wantFixtureDuration)
		}
	})

	t.Run("recurses into trak and mdia containers", func(t *testing.T) {
		inner := mp4Box("mdia", mp4Box("mvhd", mvhdBodyV0(fixtureTimescale, fixtureDurationUnits)))
		trak := mp4Box("trak", inner)
		moov := mp4Box("moov", trak)
		if got := findMP4Duration(moov, 0, len(moov)); got != wantFixtureDuration {
			t.Errorf("duration = %v, want %v", got, wantFixtureDuration)
		}
	})

	t.Run("moov without mvhd yields zero", func(t *testing.T) {
		moov := mp4Box("moov", mp4Box("trak", mp4Box("mdia", bytes.Repeat([]byte{0}, 32))))
		if got := findMP4Duration(moov, 0, len(moov)); got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})

	t.Run("moov at the file end missing from the window yields zero", func(t *testing.T) {
		// faststart off: mdat spans everything the window cuts away, the
		// mvhd sits past the window. The walk must stop, not fabricate.
		ftyp := mp4Box("ftyp", []byte("isom"))
		mdat := mp4Box("mdat", bytes.Repeat([]byte{0xCD}, 2<<20))
		moov := mp4Box("moov", mp4Box("mvhd", mvhdBodyV0(fixtureTimescale, fixtureDurationUnits)))
		full := append(append(append([]byte{}, ftyp...), mdat...), moov...)
		window := full[:len(ftyp)+8+16] // ftyp + the mdat header and a sliver
		if got := findMP4Duration(window, 0, len(window)); got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})

	t.Run("truncated moov declaring more than the window yields zero", func(t *testing.T) {
		ftyp := mp4Box("ftyp", []byte("isom"))
		moov := mp4Box("moov", mp4Box("mvhd", mvhdBodyV0(fixtureTimescale, fixtureDurationUnits)))
		full := append(ftyp, moov...)
		window := full[:len(ftyp)+8+10] // cut inside the moov body
		if got := findMP4Duration(window, 0, len(window)); got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})

	t.Run("size==1 64-bit box walk", func(t *testing.T) {
		moov := mp4Box64("moov", mp4Box("mvhd", mvhdBodyV0(fixtureTimescale, fixtureDurationUnits)))
		data := append(mp4Box("ftyp", []byte("isom")), moov...)
		if got := findMP4Duration(data, 0, len(data)); got != wantFixtureDuration {
			t.Errorf("duration = %v, want %v", got, wantFixtureDuration)
		}
	})

	t.Run("size==0 final box extends to the window end", func(t *testing.T) {
		moov := mp4BoxZeroSize("moov", mp4Box("mvhd", mvhdBodyV0(fixtureTimescale, fixtureDurationUnits)))
		if got := findMP4Duration(moov, 0, len(moov)); got != wantFixtureDuration {
			t.Errorf("duration = %v, want %v", got, wantFixtureDuration)
		}
	})

	t.Run("implausible 64-bit size aborts without panicking", func(t *testing.T) {
		for _, large := range []uint64{1 << 63, 1<<63 - 1} {
			bad := make([]byte, 16)
			binary.BigEndian.PutUint32(bad[:4], 1)
			copy(bad[4:8], "moov")
			binary.BigEndian.PutUint64(bad[8:16], large)
			if got := findMP4Duration(bad, 0, len(bad)); got != 0 {
				t.Errorf("largesize %d: duration = %v, want 0", large, got)
			}
		}
	})

	t.Run("size under the header size aborts", func(t *testing.T) {
		bad := make([]byte, 16)
		binary.BigEndian.PutUint32(bad[:4], 4) // smaller than the 8-byte header
		copy(bad[4:8], "free")
		if got := findMP4Duration(bad, 0, len(bad)); got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})

	t.Run("garbage body yields zero", func(t *testing.T) {
		data := []byte("this is definitely not an mp4 file, just text.....")
		if got := findMP4Duration(data, 0, len(data)); got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})

	t.Run("empty body yields zero", func(t *testing.T) {
		if got := probeMP4Duration(nil); got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})
}

func TestParseMVHDDuration(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		want    float64
	}{
		{"v0 timescale/duration", mvhdBodyV0(100, 1050), 10.5},
		{"v1 64-bit duration", mvhdBodyV1(1_000_000, 5_000_000_000), 5000},
		{"v0 timescale zero", mvhdBodyV0(0, 1050), 0},
		{"v0 duration zero", mvhdBodyV0(100, 0), 0},
		{"unknown version", func() []byte {
			b := mvhdBodyV0(100, 1050)
			b[0] = 2
			return b
		}(), 0},
		{"v0 body too short", mvhdBodyV0(100, 1050)[:19], 0},
		{"v1 body too short", mvhdBodyV1(1_000_000, 5_000_000_000)[:31], 0},
		{"empty payload", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseMVHDDuration(tc.payload); got != tc.want {
				t.Errorf("parseMVHDDuration = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Content-Range total
// ---------------------------------------------------------------------------

func TestContentRangeTotal(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"bytes 0-0/12345", 12345},
		{"bytes 0-1048575/24690112", 24690112},
		{"bytes */123", 123},
		{"bytes 0-0/ 42 ", 42},
		{"bytes 0-0/*", 0},
		{"", 0},
		{"nonsense", 0},
		{"bytes 0-0/", 0},
		{"bytes 0-0/abc", 0},
		{"bytes 0-0/-5", 0},                      // negative garbage: unknown, never a negative size
		{"bytes 0-0/99999999999999999999999", 0}, // beyond int64: unknown
	}
	for _, tc := range cases {
		if got := contentRangeTotal(tc.in); got != tc.want {
			t.Errorf("contentRangeTotal(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Probe over the injected header-bearing seam
// ---------------------------------------------------------------------------

// probeResp is one canned header-bearing GET outcome.
type probeResp struct {
	body         []byte
	status       int
	contentRange string
	err          error
}

// probeTransport stands in for the httpx client on the probe path only: it
// reproduces GetMeta's (body, status, response headers, classified error)
// surface, records the URL and headers of every call, and answers anything
// unrouted with the same 404 shape httpx produces.
type probeTransport struct {
	routes map[string]probeResp
	calls  []string
	sent   []map[string]string
}

func (p *probeTransport) getMeta(ctx context.Context, url string, headers map[string]string) ([]byte, int, map[string][]string, error) {
	p.calls = append(p.calls, url)
	p.sent = append(p.sent, headers)
	if err := ctx.Err(); err != nil {
		return nil, 0, nil, err
	}
	r, ok := p.routes[url]
	if !ok {
		return nil, 0, nil, twitter.Errorf(twitter.KindNotFound, "httpx.Get", "instance returned HTTP 404")
	}
	if r.err != nil {
		return nil, 0, nil, r.err
	}
	var header map[string][]string
	if r.contentRange != "" {
		header = map[string][]string{"Content-Range": {r.contentRange}}
	}
	return r.body, r.status, header, nil
}

// newProbeResolver builds a Resolver whose header-bearing fetch is the fake.
func newProbeResolver(routes map[string]probeResp) (*Resolver, *probeTransport) {
	p := &probeTransport{routes: routes}
	return &Resolver{Now: time.Now, fetchMeta: p.getMeta}, p
}

const (
	probeMediaURL  = "https://video.twimg.com/tweet_video/v.mp4"
	probeXdownURL  = "https://xdown.app/api/proxy?token=x.y.z"
	probeSizeTotal = int64(24690112)
)

func TestProbeFetchesDurationAndSize(t *testing.T) {
	res, p := newProbeResolver(map[string]probeResp{
		probeMediaURL: {body: probeMP4Head(), status: http.StatusPartialContent, contentRange: "bytes 0-1048575/24690112"},
	})
	d, size, err := res.Probe(context.Background(), probeMediaURL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if d != wantFixtureDuration {
		t.Errorf("duration = %v, want %v", d, wantFixtureDuration)
	}
	if size != probeSizeTotal {
		t.Errorf("size = %d, want %d", size, probeSizeTotal)
	}
	if len(p.calls) != 1 || p.calls[0] != probeMediaURL {
		t.Errorf("calls = %v, want exactly one request to %q", p.calls, probeMediaURL)
	}

	// Request shape: the plugin's media-request headers plus the exact
	// 1 MiB window (service.py `_probe_remote_media`).
	want := map[string]string{
		"User-Agent":      mediaUserAgent,
		"Accept-Language": mediaAcceptLanguage,
		"Accept":          "*/*",
		"Referer":         "https://x.com/",
		"Range":           "bytes=0-1048575",
	}
	if !maps.Equal(p.sent[0], want) {
		t.Errorf("headers = %v, want %v", p.sent[0], want)
	}
}

func TestProbeRefererFollowsTheHost(t *testing.T) {
	res, p := newProbeResolver(map[string]probeResp{
		probeXdownURL: {body: probeMP4Head(), status: http.StatusPartialContent, contentRange: "bytes 0-1048575/1000"},
	})
	if _, _, err := res.Probe(context.Background(), probeXdownURL); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := p.sent[0]["Referer"]; got != "https://xdown.app/" {
		t.Errorf("Referer = %q, want the xdown host's own %q", got, "https://xdown.app/")
	}
}

func TestProbeOutcomes(t *testing.T) {
	head := probeMP4Head()
	cases := []struct {
		name     string
		route    probeResp
		wantD    float64
		wantSize int64
		wantKind twitter.Kind // "" wants a nil error
	}{
		{
			name:     "206 with content-range",
			route:    probeResp{body: head, status: http.StatusPartialContent, contentRange: "bytes 0-1048575/52428800"},
			wantD:    wantFixtureDuration,
			wantSize: 52428800,
		},
		{
			name:  "206 without content-range leaves size zero",
			route: probeResp{body: head, status: http.StatusPartialContent},
			wantD: wantFixtureDuration,
		},
		{
			name:  "206 star total leaves size zero",
			route: probeResp{body: head, status: http.StatusPartialContent, contentRange: "bytes 0-1048575/*"},
			wantD: wantFixtureDuration,
		},
		{
			// R-M8-6: a 200 without Range support gives no size and no
			// partial-body semantics — the body must not even be walked
			// (its mvhd would otherwise fabricate a duration).
			name:  "200 without range support is not probed",
			route: probeResp{body: head, status: http.StatusOK},
		},
		{
			name:  "302 redirect is not probed",
			route: probeResp{body: []byte("moved"), status: http.StatusFound},
		},
		{
			name:     "404 is classified",
			route:    probeResp{status: http.StatusNotFound, err: twitter.Errorf(twitter.KindNotFound, "httpx.Get", "instance returned HTTP 404")},
			wantKind: twitter.KindNotFound,
		},
		{
			name:     "transport failure is classified",
			route:    probeResp{err: twitter.Errorf(twitter.KindUnavailable, "httpx.Get", "request failed after 3 attempt(s): connection refused")},
			wantKind: twitter.KindUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := newProbeResolver(map[string]probeResp{probeMediaURL: tc.route})
			d, size, err := res.Probe(context.Background(), probeMediaURL)
			if d != tc.wantD || size != tc.wantSize {
				t.Errorf("Probe = (%v, %d), want (%v, %d)", d, size, tc.wantD, tc.wantSize)
			}
			if tc.wantKind == "" {
				if err != nil {
					t.Errorf("error = %v, want nil", err)
				}
				return
			}
			var terr *twitter.Error
			if !errors.As(err, &terr) || terr.Kind != tc.wantKind {
				t.Errorf("error = %v, want kind %q", err, tc.wantKind)
			}
		})
	}
}

func TestProbeCancelledContextFailsFast(t *testing.T) {
	res, _ := newProbeResolver(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := res.Probe(ctx, probeMediaURL)
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindUnavailable {
		t.Errorf("error = %v, want kind %q", err, twitter.KindUnavailable)
	}
}

func TestProbeWithoutTransportIsLocalState(t *testing.T) {
	res := &Resolver{Now: time.Now}
	_, _, err := res.Probe(context.Background(), probeMediaURL)
	var terr *twitter.Error
	if !errors.As(err, &terr) || terr.Kind != twitter.KindLocalState {
		t.Errorf("error = %v, want kind %q", err, twitter.KindLocalState)
	}
}

// ---------------------------------------------------------------------------
// Probe over the real httpx transport (httptest)
// ---------------------------------------------------------------------------

// newProbeHTTPResolver builds a Resolver over the real httpx transport with
// retries, backoff and pacing disabled, so probes against httptest stay fast
// and deterministic.
func newProbeHTTPResolver(t *testing.T) *Resolver {
	t.Helper()
	res, err := NewResolver(httpx.Options{RetryAttempts: -1, RetryDelay: -1, MinInterval: -1}, nil)
	if err != nil {
		t.Fatalf("build resolver: %v", err)
	}
	return res
}

func TestProbeOverRealTransport206(t *testing.T) {
	head := probeMP4Head()
	var mu sync.Mutex
	var sawRange, sawReferer, sawAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawRange, sawReferer, sawAccept = r.Header.Get("Range"), r.Header.Get("Referer"), r.Header.Get("Accept")
		mu.Unlock()
		w.Header().Set("Content-Range", "bytes 0-1048575/52428800")
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(head)
	}))
	t.Cleanup(srv.Close)

	d, size, err := newProbeHTTPResolver(t).Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if d != wantFixtureDuration || size != 52428800 {
		t.Errorf("Probe = (%v, %d), want (%v, 52428800)", d, size, wantFixtureDuration)
	}
	mu.Lock()
	defer mu.Unlock()
	if sawRange != "bytes=0-1048575" || sawReferer != "https://x.com/" || sawAccept != "*/*" {
		t.Errorf("server saw Range=%q Referer=%q Accept=%q", sawRange, sawReferer, sawAccept)
	}
}

func TestProbeOverRealTransportRangeIgnored(t *testing.T) {
	head := probeMP4Head()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK) // ignores Range, no Content-Range
		_, _ = w.Write(head)
	}))
	t.Cleanup(srv.Close)

	d, size, err := newProbeHTTPResolver(t).Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if d != 0 || size != 0 {
		t.Errorf("Probe = (%v, %d), want zeros: a 200 gives no partial-body semantics", d, size)
	}
}

func TestProbeOverRealTransportStarTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-1048575/*")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(probeMP4Head())
	}))
	t.Cleanup(srv.Close)

	d, size, err := newProbeHTTPResolver(t).Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if d != wantFixtureDuration || size != 0 {
		t.Errorf("Probe = (%v, %d), want (%v, 0)", d, size, wantFixtureDuration)
	}
}

func TestProbeOverRealTransportClassifiesStatuses(t *testing.T) {
	cases := []struct {
		status   int
		wantKind twitter.Kind
	}{
		{http.StatusNotFound, twitter.KindNotFound},
		{http.StatusRequestedRangeNotSatisfiable, twitter.KindUnavailable},
		{http.StatusForbidden, twitter.KindChallenge},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)

			_, _, err := newProbeHTTPResolver(t).Probe(context.Background(), srv.URL)
			var terr *twitter.Error
			if !errors.As(err, &terr) || terr.Kind != tc.wantKind {
				t.Errorf("Probe error = %v, want kind %q", err, tc.wantKind)
			}
		})
	}
}

// TestProbeWindowConstantPinsThePluginRange pins the wire value the plugin
// sends (service.py: Range bytes=0-1048575); a changed constant must be a
// conscious semantic decision.
func TestProbeWindowConstantPinsThePluginRange(t *testing.T) {
	if probeHeadBytes != 1<<20 {
		t.Errorf("probeHeadBytes = %d, want the plugin's 1 MiB (1048576)", probeHeadBytes)
	}
	if !strings.Contains(probeHeaders(probeMediaURL)["Range"], "1048575") {
		t.Errorf("Range header = %q, want the bytes=0-1048575 window", probeHeaders(probeMediaURL)["Range"])
	}
}
