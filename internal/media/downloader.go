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

// DownloadResult reports one completed FetchToFile.
type DownloadResult struct {
	// URL is the URL that was downloaded. The command layer's fallback
	// loop stamps whichever candidate actually succeeded.
	URL string
	// Bytes is the streamed size in bytes.
	Bytes int64
	// SHA256 is the lowercase hex digest, computed streaming while writing.
	SHA256 string
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
// declared-size mismatch → KindMalformed); local filesystem failures are
// KindLocalState. Every failure removes the temp file and leaves finalPath
// untouched. No directory is created implicitly: a missing parent fails as
// KindLocalState, directory semantics belong to the command layer.
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
			return DownloadResult{}, nitter.Errorf(nitter.KindLocalState, opMediaDownload, "%w: %s", ErrFileExists, finalPath)
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
	written, err := d.HTTP.Download(ctx, url, io.MultiWriter(tmp, hash), downloadHeaders(url))
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
	return DownloadResult{URL: url, Bytes: written, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
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
