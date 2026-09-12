package media

// The `--probe` enrichment pass: mp4 duration and Content-Range size probing
// for one direct media URL. Semantics are a faithful port of the plugin's
// media_support/video_probe.py (content_range_total, probe_mp4_duration,
// find_mp4_duration, parse_mvhd_duration) and service.py
// `_probe_remote_media`:
//
//   - ONE ranged GET per media URL (the plugin's exact wire shape:
//     Range bytes=0-1048575) fetches a 1 MiB head window through the shared
//     httpx transport, so pacing, retries and error classification apply.
//     The response's Content-Range total is the size; the received head is
//     walked for the first mp4 movie header (mvhd) to compute the duration.
//   - No ftyp sniff: the plugin walks whatever bytes the response delivered
//     and relies on the box-walk guards (a box smaller than its header or
//     reaching past the window ends the walk) to reject non-mp4 bodies.
//     That also keeps exotic box orders probeable, and it cannot fabricate:
//     garbage input fails the first size check and yields zero.
//   - The two probe parts fail independently into their zero values and no
//     second request is sent (the plugin also walks only what it received):
//     an mvhd past the window (moov at the file end — faststart off) yields
//     no duration; a missing, star-sized or garbage Content-Range total
//     yields no size.

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/shitianyaa/nitter-cli/sdk"
)

const opProbe = "media.Probe"

// probeHeadBytes is the head window one probe request fetches: the plugin's
// exact ranged GET (service.py `_probe_remote_media` sends Range
// bytes=0-1048575 and reads at most 1 MiB). moov sits at the file head on
// the faststart videos the plugin's own download path produces, so 1 MiB is
// what the plugin's production probing sees; an mvhd past the window is
// reported as unknown (no second request, mirroring the plugin).
const probeHeadBytes = 1 << 20

// Probe fetches lightweight metadata for one direct media URL: the video
// duration parsed from the mp4 movie header and the total file size taken
// from the Content-Range response header. It is the media command's
// on-demand `--probe` pass — one extra ranged GET per resolved URL.
//
// Mechanism (ported from media_support/video_probe.py and service.py
// `_probe_remote_media`): one GET with Range bytes=0-1048575 (probeHeadBytes,
// the plugin's 1 MiB head window) through the shared httpx transport carries
// the plugin's media-request headers (fixed browser identity, Accept */* and
// a Referer by host — the twimg CDNs 403 a xdown.app Referer, xdown hosts
// get their own). A 206 response is the probed one: sizeBytes comes from the
// Content-Range total ("bytes 0-N/TOTAL"), durationSeconds from walking the
// received head for the first mvhd box (moov/trak/mdia containers, 32-bit
// version-0 and 64-bit version-1 movie headers).
//
// R-M8-6: probing is best-effort enrichment. The two parts fail
// independently and leave their zero value — an mvhd past the window yields
// no duration, a missing/star/garbage Content-Range total yields no size;
// neither is fabricated, and no second request is sent. A response that is
// not a 206 (a 200 from a CDN that ignores Range, a redirect) gives no size
// and no partial-body semantics: Probe returns zero values and a nil error
// for it. Only a failure of the request itself surfaces as a classified
// *nitter.Error the caller may ignore (404 → KindNotFound; 416 and other
// 4xx → KindUnavailable; 401/403 → KindChallenge; 5xx and transport
// failures → KindUnavailable after the transport's retries; an unparseable
// URL → KindInvalidArg). A 200 that ignores Range may additionally trip the
// transport's response-body cap (the full file would be read, the plugin
// instead caps its read at the window) — that surfaces as KindMalformed.
func (r *Resolver) Probe(ctx context.Context, mediaURL string) (durationSeconds float64, sizeBytes int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, nitter.Errorf(nitter.KindUnavailable, opProbe, "probe not sent: %w", err)
	}
	body, status, header, err := r.getMeta(ctx, mediaURL, probeHeaders(mediaURL))
	if err != nil {
		return 0, 0, err
	}
	if status != http.StatusPartialContent {
		// Range not honored (200 full-body, 3xx, ...): no size, no
		// partial-body semantics — best-effort "cannot probe" (R-M8-6).
		// (The plugin would walk the first MiB of a 200 body, but this
		// transport reads the complete body before Probe sees it: honoring
		// the ruling costs nothing here and never downloads whole videos
		// into a probe.)
		return 0, 0, nil
	}
	sizeBytes = contentRangeTotal(http.Header(header).Get("Content-Range"))
	durationSeconds = probeMP4Duration(body)
	return durationSeconds, sizeBytes, nil
}

// probeHeaders is the header set of a probe GET: the plugin's
// `_media_request_headers` — its fixed browser identity, Accept */* and a
// Referer by host (the twimg CDNs 403 a xdown.app Referer; xdown hosts get
// their own) — plus the Range window.
func probeHeaders(mediaURL string) map[string]string {
	referer := "https://x.com/"
	if u, err := url.Parse(mediaURL); err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), "xdown.app") {
		referer = "https://xdown.app/"
	}
	return map[string]string{
		"User-Agent":      mediaUserAgent,
		"Accept-Language": mediaAcceptLanguage,
		"Accept":          "*/*",
		"Referer":         referer,
		"Range":           "bytes=0-" + strconv.FormatInt(probeHeadBytes-1, 10),
	}
}

// contentRangeTotal ports video_probe.content_range_total: the total after
// the last "/" of a "bytes 0-N/TOTAL" Content-Range value; 0 (unknown) when
// the header is absent, carries no "/", ends in "*" or nothing, or is not an
// integer. Two documented hardenings over Python's arbitrary-precision
// int(): a negative or beyond-int64 total is garbage and reports unknown
// rather than propagating a nonsense size.
func contentRangeTotal(v string) int64 {
	if v == "" {
		return 0
	}
	i := strings.LastIndex(v, "/")
	if i < 0 {
		return 0
	}
	tail := strings.TrimSpace(v[i+1:])
	if tail == "" || tail == "*" {
		return 0
	}
	n, err := strconv.ParseInt(tail, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// The ISO-BMFF box types the walk cares about (video_probe.py's
// container_types plus the movie header).
var (
	boxMvhd = []byte("mvhd")
	boxMoov = []byte("moov")
	boxTrak = []byte("trak")
	boxMdia = []byte("mdia")
)

// probeMP4Duration ports video_probe.probe_mp4_duration: walk the whole
// received window. There is no ftyp sniff — the plugin walks whatever bytes
// arrived and the box-walk guards reject non-mp4 bodies (their first "box"
// fails the size/header check and yields zero); that also keeps exotic box
// orders probeable and cannot fabricate a duration.
func probeMP4Duration(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	return findMP4Duration(data, 0, len(data))
}

// findMP4Duration ports video_probe.find_mp4_duration: a depth-first walk of
// the ISO-BMFF box structure within data[start:end] that returns the first
// mvhd duration in seconds, or 0 when the window holds none. A box smaller
// than its header or reaching past the window ends that level's walk (the
// parent continues after the box, the plugin's break semantics); each
// recursion covers a strictly smaller range, so hostile input terminates.
// size==1 boxes carry a 64-bit largesize (an implausibly large one aborts —
// Python's big ints merely fail the range check, so this is the same
// outcome without the int-width hazard); size==0 boxes extend to the window
// end. The bounds are written as size > end-offset (algebraically the
// plugin's offset+size > end) so no addition can overflow.
func findMP4Duration(data []byte, start, end int) float64 {
	offset := start
	for offset+8 <= end {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := data[offset+4 : offset+8]
		headerSize := 8
		if size == 1 && offset+16 <= end {
			wide := binary.BigEndian.Uint64(data[offset+8 : offset+16])
			headerSize = 16
			if wide > math.MaxInt {
				break
			}
			size = int(wide)
		} else if size == 0 {
			size = end - offset
		}
		if size < headerSize || size > end-offset {
			break
		}
		if bytes.Equal(boxType, boxMvhd) {
			// The plugin returns whatever parse_mvhd_duration yields — an
			// unparsable mvhd ends the search, it does not keep looking.
			return parseMVHDDuration(data[offset+headerSize : offset+size])
		}
		if bytes.Equal(boxType, boxMoov) || bytes.Equal(boxType, boxTrak) || bytes.Equal(boxType, boxMdia) {
			if d := findMP4Duration(data, offset+headerSize, offset+size); d > 0 {
				return d
			}
		}
		offset += size
	}
	return 0
}

// parseMVHDDuration ports video_probe.parse_mvhd_duration: the movie header
// payload's version 0 (32-bit timescale at 12:16, 32-bit duration at 16:20)
// or version 1 (32-bit timescale at 20:24, 64-bit duration at 24:32) gives
// seconds = duration/timescale. A short payload, an unknown version byte,
// a zero timescale or a zero duration yield 0 (unknown — the plugin returns
// None and the CLI reports no duration rather than fabricating one).
func parseMVHDDuration(payload []byte) float64 {
	if len(payload) < 20 {
		return 0
	}
	var timescale uint32
	var duration uint64
	switch payload[0] {
	case 0:
		timescale = binary.BigEndian.Uint32(payload[12:16])
		duration = uint64(binary.BigEndian.Uint32(payload[16:20]))
	case 1:
		if len(payload) < 32 {
			return 0
		}
		timescale = binary.BigEndian.Uint32(payload[20:24])
		duration = binary.BigEndian.Uint64(payload[24:32])
	default:
		return 0
	}
	if timescale == 0 || duration == 0 {
		return 0
	}
	return float64(duration) / float64(timescale)
}
