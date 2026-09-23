package media

// The M9 fetch half: stream one direct media URL to disk. The resolver
// (resolve.go) turns a status into direct links; the downloader is the
// foundation the M9 command layer builds its per-file fallback loop on —
// FetchToFile takes exactly ONE URL and one target path and leaves
// candidate/fallback selection to that layer.
//
// Semantics:
//   - Atomic write: the body streams into a ".download-*.tmp" file in the
//     TARGET's own directory (same filesystem, so the final rename cannot
//     degrade into a cross-device copy), SHA-256 is computed streaming on
//     the way, and the completed temp file is renamed over the target. A
//     failed or cancelled download never leaves a truncated target — the
//     temp file is removed instead.
//   - Exists policy: an existing finalPath fails the call with an error
//     wrapping ErrFileExists unless force is set; force overwrites through
//     the same temp-then-rename flow (no in-place write ever touches the
//     old file before the new bytes are complete).
//   - Transport: everything rides the shared httpx.Client.Download — full
//     GET, declared-length verification (Content-Length; Content-Range
//     total for a defensive 206), classified *nitter.Error kinds, 429
//     Retry-After honored once, mid-stream cancel stops and cleans up.
//     The downloader does NOT filter http vs https: scheme policy belongs
//     to the resolve layer that produced the URL; whatever URL arrives is
//     downloaded through the transport (proxy included as a transport
//     option).
//   - Error hygiene: transport errors arrive already sanitized (no URL
//     query strings, no response bodies); the downloader's own errors add
//     only local file paths — the user's own machine paths, the same thing
//     the seen store puts in its local-state errors.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/shitianyaa/nitter-cli/internal/nitter/protocol/httpx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

const opMediaDownload = "media.FetchToFile"

// downloadTempPattern is the temp-file name pattern inside the target's
// directory. The leading dot keeps an interrupted download out of the way;
// the .tmp suffix marks it incomplete.
const downloadTempPattern = ".download-*.tmp"

// ErrFileExists is wrapped (kind KindLocalState) when finalPath already
// exists and force is false. The command layer detects it with errors.Is to
// offer --force; no other FetchToFile failure carries it.
var ErrFileExists = errors.New("target file already exists")

// Downloader streams direct media URLs to disk over the shared httpx
// transport. It is safe for concurrent use provided the transport is.
//
// There is deliberately no clock or fetch seam: nothing in FetchToFile
// measures time (the transport owns pacing/retries and takes its clock via
// httpx.Options), and the downloader is tested against real httptest servers
// over the real transport — the same shape the probe tests use.
type Downloader struct {
	// HTTP is the shared transport — ideally the same client the resolver
	// resolves and probes through, so every media request shares one
	// pacing budget. Consulted per call; nil fails the call as
	// KindLocalState (mirroring the resolver's unwired-transport error).
	HTTP *httpx.Client
}

// localWriteGuard records the first write error so a stream failure can be
// told apart from a local sink failure — io.Copy does not distinguish the
// two, and the transport cannot know whether the sink is local disk.
//
// ponytail: the sink is assumed to be local disk; a non-disk sink would need
// this classification revisited.
type localWriteGuard struct {
	w   io.Writer
	err error
}

func (g *localWriteGuard) Write(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	n, err := g.w.Write(p)
	if err != nil {
		g.err = err
	}
	return n, err
}

// streamTo streams url's body into sink and classifies the outcome: a
// failure of sink itself is KindLocalState (the message mirrors the sibling
// create/sync/close temp-file errors), every other error is the transport's
// classification verbatim. The header return is what the auto-extension path
// needs; callers that do not need it ignore it.
func (d *Downloader) streamTo(ctx context.Context, url string, sink io.Writer) (int64, map[string][]string, error) {
	guard := &localWriteGuard{w: sink}
	written, header, err := d.HTTP.DownloadMeta(ctx, url, guard, downloadHeaders(url))
	if err != nil {
		if werr := guard.err; werr != nil {
			return written, nil, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "write temp file: %w", werr)
		}
		return written, nil, err
	}
	return written, header, nil
}

// DownloadResult reports one completed FetchToFile or FetchToFileAuto.
type DownloadResult struct {
	// URL is the URL that was downloaded. The command layer's fallback
	// loop stamps whichever candidate actually succeeded.
	URL string
	// Bytes is the streamed size in bytes.
	Bytes int64
	// SHA256 is the lowercase hex digest, computed streaming while writing.
	SHA256 string
	// Path is the final on-disk path the bytes were renamed to. FetchToFile
	// reports its finalPath argument; FetchToFileAuto reports the path with
	// the Content-Type-derived extension appended (ruling R-M9-4) — the only
	// place the derived name exists.
	Path string
}

// existsError marks the refusal FetchToFile and FetchToFileAuto raise when
// the final target already exists and force is false: it Is-matches the
// ErrFileExists sentinel (the errors.Is contract the command layer relies
// on) and carries the final path for callers that must name it — the
// --on-exists skip mode; for the auto-extension path the path exists nowhere
// else, since only the response's Content-Type decided it. The Error() text
// is the sentinel's message plus the path, exactly what the pre-structure
// wrap produced.
type existsError struct {
	path string
}

func (e *existsError) Error() string { return ErrFileExists.Error() + ": " + e.path }
func (e *existsError) Is(target error) bool {
	return target == ErrFileExists
}
func (e *existsError) Path() string { return e.path }

// contentTypeExts maps the Content-Type values the media CDNs serve to the
// file extension the auto path derives (ruling R-M9-4). Anything else falls
// back to the caller's kind-based default.
var contentTypeExts = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
	"video/mp4":  ".mp4",
}

// extFromContentType derives the auto path's extension from a response's
// Content-Type header value (parameters after ';' are stripped, case
// folded); "" when the type is not one the mapping knows.
func extFromContentType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return contentTypeExts[strings.ToLower(strings.TrimSpace(contentType))]
}

// FetchToFile downloads url to finalPath atomically: the response body
// streams into a temp file in finalPath's directory — hashed with SHA-256
// on the way — and the completed temp file is renamed over finalPath. The
// request carries the plugin's media-request headers (probeHeaders minus
// the Range window: a download is a full GET).
//
// If finalPath already exists the call fails with an error wrapping
// ErrFileExists (kind KindLocalState) unless force is set; force overwrites
// through the same temp-then-rename flow.
//
// Errors are the transport's classified *nitter.Error verbatim (404 →
// KindNotFound, 401/403 → KindChallenge, 429 → KindRateLimited after the
// single Retry-After retry, 5xx/transport → KindUnavailable after retries,
// declared-size mismatch → KindMalformed) — except a failure of the target
// file's own writes, which is KindLocalState like every other local
// filesystem failure. Every failure removes the temp file and leaves
// finalPath untouched. No directory is created implicitly: a missing parent
// fails as KindLocalState, directory semantics belong to the command layer.
func (d *Downloader) FetchToFile(ctx context.Context, url, finalPath string, force bool) (DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return DownloadResult{}, nitter.Errorf(nitter.KindUnavailable, opMediaDownload, "download not sent: %w", err)
	}
	if d.HTTP == nil {
		return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "no transport wired into the media downloader")
	}
	if !force {
		switch _, err := os.Stat(finalPath); {
		case err == nil:
			return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "%w", &existsError{path: finalPath})
		case !errors.Is(err, os.ErrNotExist):
			return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "stat %s: %w", finalPath, err)
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(finalPath), downloadTempPattern)
	if err != nil {
		return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// discard removes the temp file and returns err unchanged: every failure
	// past CreateTemp leaves no residue behind.
	discard := func(err error) (DownloadResult, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return DownloadResult{}, err
	}

	hash := sha256.New()
	written, _, err := d.streamTo(ctx, url, io.MultiWriter(tmp, hash))
	if err != nil {
		return discard(err)
	}
	if err := tmp.Sync(); err != nil {
		return discard(nitter.Errorf(nitter.KindLocalState, opMediaDownload, "sync temp file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return discard(nitter.Errorf(nitter.KindLocalState, opMediaDownload, "close temp file: %w", err))
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "rename to %s: %w", finalPath, err)
	}
	return DownloadResult{URL: url, Bytes: written, SHA256: hex.EncodeToString(hash.Sum(nil)), Path: finalPath}, nil
}

// FetchToFileAuto downloads url to a path it derives itself — the write half
// of ruling R-M9-4, for planned files whose URL carries no extension. The
// body streams into a temp file in basePath's directory (hashed on the way,
// exactly like FetchToFile), the file extension is derived from the
// response's Content-Type (image/jpeg→.jpg, image/png→.png, image/webp→.webp,
// image/gif→.gif, video/mp4→.mp4; anything else falls back to defaultExt,
// the caller's kind-based default — .jpg for images and covers, .mp4 for
// videos — which is normalized to carry its leading dot), and the completed
// temp file is renamed to basePath+ext. The result reports the derived final
// path in Path: the command layer cannot recompute it.
//
// The extension is only knowable from the response, so — unlike FetchToFile —
// the download happens before the exists check: an existing basePath+ext
// still fails with an error wrapping ErrFileExists (kind KindLocalState,
// carrying the derived path) unless force is set, and the temp file is
// removed either way. Everything else mirrors FetchToFile: transport
// classification verbatim, no directory creation, failures leave no residue.
// An unknown Content-Type together with an empty defaultExt cannot name the
// file and fails as KindLocalState rather than silently writing an
// extension-less file.
func (d *Downloader) FetchToFileAuto(ctx context.Context, url, basePath, defaultExt string, force bool) (DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return DownloadResult{}, nitter.Errorf(nitter.KindUnavailable, opMediaDownload, "download not sent: %w", err)
	}
	if d.HTTP == nil {
		return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "no transport wired into the media downloader")
	}

	tmp, err := os.CreateTemp(filepath.Dir(basePath), downloadTempPattern)
	if err != nil {
		return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// discard removes the temp file and returns err unchanged: every failure
	// past CreateTemp leaves no residue behind.
	discard := func(err error) (DownloadResult, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return DownloadResult{}, err
	}

	hash := sha256.New()
	written, header, err := d.streamTo(ctx, url, io.MultiWriter(tmp, hash))
	if err != nil {
		return discard(err)
	}
	ext := extFromContentType(http.Header(header).Get("Content-Type"))
	if ext == "" {
		ext = strings.TrimSpace(defaultExt)
		if ext == "" {
			return discard(nitter.Errorf(nitter.KindLocalState, opMediaDownload,
				"no extension derivable from the response's Content-Type and no default extension given for %s", basePath))
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
	}
	finalPath := basePath + ext

	if !force {
		switch _, err := os.Stat(finalPath); {
		case err == nil:
			return discard(nitter.Errorf(nitter.KindLocalState, opMediaDownload, "%w", &existsError{path: finalPath}))
		case !errors.Is(err, os.ErrNotExist):
			return discard(nitter.Errorf(nitter.KindLocalState, opMediaDownload, "stat %s: %w", finalPath, err))
		}
	}
	if err := tmp.Sync(); err != nil {
		return discard(nitter.Errorf(nitter.KindLocalState, opMediaDownload, "sync temp file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return discard(nitter.Errorf(nitter.KindLocalState, opMediaDownload, "close temp file: %w", err))
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "rename to %s: %w", finalPath, err)
	}
	return DownloadResult{URL: url, Bytes: written, SHA256: hex.EncodeToString(hash.Sum(nil)), Path: finalPath}, nil
}

// downloadHeaders is the header set of a media download GET: the plugin's
// `_media_request_headers` — its fixed browser identity, Accept */* and a
// Referer by host (the twimg CDNs 403 a xdown.app Referer; xdown hosts get
// their own) — matching probeHeaders minus the Range window (a download is
// a full GET).
func downloadHeaders(mediaURL string) map[string]string {
	referer := "https://x.com/"
	if u, err := url.Parse(mediaURL); err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), "xdown.app") {
		referer = "https://xdown.app/"
	}
	return map[string]string{
		"User-Agent":      mediaUserAgent,
		"Accept-Language": mediaAcceptLanguage,
		"Accept":          "*/*",
		"Referer":         referer,
	}
}
