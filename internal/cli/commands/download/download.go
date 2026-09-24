// Package download implements the `nitter download` command: resolve status
// references into media (the media command's strategies) and stream the
// planned files to disk — the write half the media command deliberately
// leaves out. Data acquisition goes through internal/cli/client and sdk
// models only — per ruling R11 this package never imports internal/media or
// internal/nitter/*.
package download

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// opDownload is the command name stamped into error envelopes.
const opDownload = "download"

// Strategy names accepted by --strategy. auto is the ordered chain
// fx → nitter → xdown (the media command's expansion);
// an explicit name runs only that one.
var validStrategies = map[string]bool{
	"auto":   true,
	"fx":     true,
	"nitter": true,
	"xdown":  true,
}

// autoStrategies is the chain --strategy auto expands to (identical to the
// media command's: nitter is included verbatim and reports a clear
// local-state failure when no instance is configured — never silently
// skipped).
var autoStrategies = []string{"fx", "nitter", "xdown"}

var validQualities = map[string]bool{
	"high":   true,
	"medium": true,
	"low":    true,
}

// --kind values: the media kinds as a pre-filter plus the cover selection
// mode (PlanDownload's contract).
var validKinds = map[string]bool{
	"":      true,
	"image": true,
	"video": true,
	"gif":   true,
	"cover": true,
}

// --on-exists values (default refuse).
var validOnExists = map[string]bool{
	"refuse":    true,
	"skip":      true,
	"overwrite": true,
}

// statusRef is one parsed batch input: the raw reference the user gave (the
// ref every row and envelope echoes) plus client.ParseStatusRef's outputs.
type statusRef struct {
	raw  string
	id   string
	user string
}

// options is the flag set of one download invocation.
type options struct {
	strategy string
	quality  string
	kind     string
	onExists string
	output   string
	// filenameTemplate backs the --filename-template flag; run() resolves it
	// (flag > config > default) before runBatch. directoryTemplate is
	// resolved from the config only (no flag exists). The internal test
	// harness sets both directly.
	filenameTemplate  string
	directoryTemplate string
	asJSON            bool
	asNDJSON          bool
}

// capabilities bundles the three wiring capabilities one download run
// consumes. Production builds it from the wiring; the internal test file
// swaps fakes in (the R11 boundary lives in the client interfaces — no
// internal/media type ever reaches this package).
type capabilities struct {
	resolver   client.MediaResolver
	planner    client.DownloadPlanner
	downloader client.MediaDownloader
}

// New builds the `nitter download <REF>...` command over the shared streams.
//
// REF is a bare numeric status ID or a status URL (x.com, twitter.com or any
// Nitter instance, shape /<user>/status/<id>; /photo/N and /video/1 suffixes
// are accepted) — the same reference shapes `nitter get` and `nitter media`
// take. Multiple REFs run as a batch. Positional REFs always win; only when
// none is given AND stdin is not a TTY is the input read from stdin:
// envelope mode when the first non-whitespace byte is "{" (strict
// nitter.pipeline/v1 tweet envelopes, each record's data.url used as the
// REF), plain refs one per line otherwise. Stdin is never touched while
// positional REFs are present, so a batch cannot block on an open pipe.
func New(s *invocation.Streams) *cobra.Command {
	opts := &options{}
	cmd := &cobra.Command{
		Use:   "download <REF>...",
		Short: "Resolve statuses and download their media to disk",
		Long: `Resolve each status REF and download its media files to the output
directory. One tab-separated row per downloaded file:

  <ref>  <path>  <bytes>  <kind>  <source>

path is the absolute file path (also the NDJSON envelope's id), kind is
image/video/gif/cover and source names the strategy that resolved the
status. Under --on-exists skip the row reads "<path> (skipped)".

REF is a bare numeric status ID, or a status URL — x.com, twitter.com or any
Nitter instance, shape <user>/status/<id> (/photo/N and /video/1 suffixes
accepted) — the same reference shapes ` + "`nitter get`" + ` and ` + "`nitter media`" + ` take.
Multiple REFs run as a batch. Positional REFs always win; only with no
argument AND a non-TTY stdin is the input read from stdin: when the first
non-whitespace byte is "{" every
non-empty line must be a strict nitter.pipeline/v1 envelope of kind tweet
and each record's data.url is used as the REF (the ` + "`nitter get --ndjson`" + `
and ` + "`nitter watch --ndjson`" + ` streams feed download directly; a malformed
envelope is a usage error), otherwise every non-empty line is a plain REF.
A positional REF never reads stdin, so it cannot block on an open pipe.

Selection (--kind, default: everything) follows the video-wins rule: a
status carrying video or GIF downloads its ONE best video file — ranked by
bitrate (or xdown's p-numbers; empirically xdown serves several bitrate
entries plus a cover image) — and the winner keeps the remaining candidates
as its fallback chain: the first URL that fails falls through the fallbacks
in order, and the row reports whichever candidate succeeded. An image-only
status downloads every image (<id>-1.jpg ... <id>-4.jpg). --kind image|video|
gif pre-filters that; --kind cover plans exactly the video's cover image as
<id>-cover.<ext> (an image-only status has none and reports not_found; a
filter matching nothing yields no files, not an error).

The file extension comes from the resolved URL path when it carries one,
otherwise from the download response's Content-Type (image/jpeg→.jpg,
image/png→.png, image/webp→.webp, image/gif→.gif, video/mp4→.mp4), falling
back to a kind-based default (.jpg for images and covers, .mp4 for
videos/GIFs).

Names follow the filename_template config key (default {id}-{seq}, i.e.
<id>-<seq>.<ext>), overridable per invocation with --filename-template.
Placeholders: {id} the status id, {seq} the file's 1-based position in the
plan, {user} the ref's user segment as given (empty for a bare ID; the
user-less /i/status/<id> route reports i), {kind} image/video/gif/cover,
{ext} the planned extension with its leading dot — empty when the plan
carries none, in which case the response's Content-Type decides at download
time and the extension is appended after the final rendered name exactly as
without a template; a template without {ext} gets the extension appended at
the end. Covers ignore the filename template: they always land as
<id>-cover.<ext>. The directory_template config key (no flag; default empty)
places the files in subdirectories of the output directory, rendered per
file from {id}/{user}/{kind} — {seq} and {ext} are not allowed there, "/"
separates levels, empty levels are skipped, and empty means flat.

Rendered names are sanitized (Windows-illegal characters \ / : * ? " < > |
and control characters become _; "." and ".." directory levels are rejected).
An invalid template — an unknown or malformed placeholder, a forbidden
placeholder in the directory position, a path separator in the filename
position — never fails the run: one stderr warning line names the template
and the default (filenames) or flat (directory) behavior applies instead.
Two planned files of one ref rendering the same name collide: the later one
gets a -2, -3, ... suffix before the extension plus a warning; across refs
the --on-exists semantics apply unchanged. An empty template value means the
default.

Strategies (--strategy, default auto) are the media command's: auto tries
fx → nitter → xdown and the first strategy that yields
media wins (source stamps which one). Trust boundary: fx and
xdown are THIRD-PARTY public services — resolving AND downloading sends the
tweet URL through them, so only use them for public statuses you are fine
sharing; their failures are reported with the real strategy name.
--strategy nitter is the fully private path: resolution and download both
stay on YOUR configured instance ([[instances]] first entry, or --instance),
nothing leaves your facilities; its direct links may be plain http:// links
served by your own instance and are trusted for that. Download requests ride
the configured proxy (--proxy / config proxy) like every other fetch of
this CLI.

The output directory is --output DIR, else the download_path config key
(default ./nitter-media; relative paths resolve against the working
directory); it is created on demand (mkdir -p).

--on-exists decides what happens when a target file is already on disk
(default refuse): refuse reports the entry as an error and the batch
continues; skip keeps the existing file, reports its row with the file's
actual on-disk size and NO sha256 (nothing was re-downloaded, nothing is
fabricated) and is never counted as a failure; overwrite re-downloads
through the same atomic temp-then-rename flow.

--json prints the downloaded files as one JSON document (a single object
when exactly one file, an array otherwise, [] when none); --ndjson instead
prints one nitter.pipeline/v1 envelope per downloaded file (kind download,
id = the absolute path, meta.input = the raw ref) plus one kind:"error"
envelope per failed ref. --json and --ndjson are mutually exclusive. A
consumer closing the stream early (head/tail on a pipe) is a clean stop.

Exit codes (repo-wide semantics): success exits 0 (nothing found renders
"(empty)" on stderr); a per-REF failure gets an in-place error report (a
kind:"error" envelope on the NDJSON stream, a stderr line otherwise) while
the other refs continue, and the command exits 1 with a "download completed
with N of M refs failed" summary when at least one ref failed; usage
problems (unknown --kind/--quality/--strategy/--on-exists, bad or missing
refs, malformed stdin envelopes, --json with --ndjson) exit 2.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.output, "output", "",
		"Output directory (else the download_path config, default ./nitter-media); created on demand")
	cmd.Flags().StringVar(&opts.kind, "kind", "",
		"Download only this media kind: image, video, gif or cover")
	cmd.Flags().StringVar(&opts.quality, "quality", "high",
		"Media quality tier: high, medium or low")
	cmd.Flags().StringVar(&opts.strategy, "strategy", "auto",
		"Resolution strategy: auto (fx→nitter→xdown) or one of fx, nitter, xdown")
	cmd.Flags().StringVar(&opts.onExists, "on-exists", "refuse",
		"When the target file already exists: refuse (error), skip (keep, report with the on-disk size) or overwrite")
	cmd.Flags().StringVar(&opts.filenameTemplate, "filename-template", "",
		"Filename template for planned files with the placeholders {id}, {seq}, {user}, {kind} and {ext} (default: the filename_template config, itself {id}-{seq}); covers always land as <id>-cover.<ext>; an invalid template warns on stderr and falls back to the default")
	cmd.Flags().BoolVar(&opts.asJSON, "json", false,
		"Print the downloaded files as one JSON document (single object when exactly one file, array otherwise)")
	cmd.Flags().BoolVar(&opts.asNDJSON, "ndjson", false,
		"Print one nitter.pipeline/v1 envelope per downloaded file (kind download) and per failed ref (kind error)")
	return cmd
}

// run executes the download batch: resolve the output mode, validate the
// flags, collect the refs (arguments, or stdin in envelope or plain-line
// mode), validate them locally, build the wiring, prepare the output
// directory and run the batch.
func run(cmd *cobra.Command, s *invocation.Streams, args []string, opts *options) error {
	// Flag conflicts are input-contract problems: resolve before anything
	// else runs so --json --ndjson exits 2 up front.
	mode, err := pipeline.ResolveOutputMode(opts.asNDJSON, opts.asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if !validStrategies[opts.strategy] {
		return invocation.Usagef("download: unknown --strategy %q (want auto, fx, nitter or xdown)", opts.strategy)
	}
	if !validQualities[opts.quality] {
		return invocation.Usagef("download: unknown --quality %q (want high, medium or low)", opts.quality)
	}
	if !validKinds[opts.kind] {
		return invocation.Usagef("download: unknown --kind %q (want image, video, gif or cover)", opts.kind)
	}
	if !validOnExists[opts.onExists] {
		return invocation.Usagef("download: unknown --on-exists %q (want refuse, skip or overwrite)", opts.onExists)
	}

	refs, err := collectRefs(s, args)
	if err != nil {
		return err
	}
	pairs := make([]statusRef, 0, len(refs))
	for _, ref := range refs {
		id, user, err := client.ParseStatusRef(ref)
		if err != nil {
			return client.AsUsageError(err)
		}
		pairs = append(pairs, statusRef{raw: ref, id: id, user: user})
	}

	// The root PersistentPreRunE has published the baseline config by now; a
	// bad value in it is a usage error (exit 2).
	cfg, err := client.LoadEffectiveSettings()
	if err != nil {
		return err
	}
	w, err := client.Build(s.RootOptions, cfg, time.Now)
	if err != nil {
		return client.AsUsageError(err)
	}

	// --output overrides the download_path config per invocation; resolve it
	// against the process cwd once so every row, envelope and file shares
	// one absolute path.
	outDir := opts.output
	if outDir == "" {
		outDir = cfg.DownloadPath
	}
	abs, err := filepath.Abs(outDir)
	if err != nil {
		return fmt.Errorf("resolve output directory %s: %w", outDir, err)
	}
	outDir = abs
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outDir, err)
	}
	// Tell the user where files will land. Purely informational: the path is
	// already resolved and the directory now exists, so this is not a gate.
	// stderr keeps stdout a clean machine contract (rows / JSON / NDJSON).
	fmt.Fprintf(s.Err, "note: writing to %s\n", outDir)

	// --filename-template overrides the filename_template config per
	// invocation (flag > config); an empty value at either level IS the
	// default template. directory_template has no flag.
	if opts.filenameTemplate == "" {
		opts.filenameTemplate = cfg.FilenameTemplate
	}
	opts.directoryTemplate = cfg.DirectoryTemplate

	caps := capabilities{
		resolver:   w.Media(),
		planner:    w.Planner(),
		downloader: w.Downloader(),
	}
	return runBatch(s, mode, pairs, opts, outDir, caps)
}

// runBatch executes the download loop: per ref, resolve → plan → fetch every
// planned file, streaming NDJSON envelopes per file as they land (the other
// modes collect) and reporting in-place errors without stopping the batch.
// Every ref failure counts once toward the partial-failure summary; an empty
// plan (a kind filter matching nothing) is not a failure.
func runBatch(s *invocation.Streams, mode pipeline.Mode, pairs []statusRef, opts *options, outDir string, caps capabilities) error {
	ctx := s.CTX
	if ctx == nil {
		ctx = context.Background()
	}
	strategies := autoStrategies
	if opts.strategy != "auto" {
		strategies = []string{opts.strategy}
	}
	// The naming templates are resolved once per run ("" = default, invalid
	// = one-shot warning + fallback inside the engine); collision tracking
	// gets a fresh per-ref scope below.
	eng := newNameEngine(opts.filenameTemplate, opts.directoryTemplate, s.Err)

	type fileRow struct {
		rec     nitter.DownloadRecord
		skipped bool
	}
	rows := make([]fileRow, 0, len(pairs))
	failed := 0
	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := caps.resolver.ResolveMedia(ctx, pair.id, pair.user, strategies, opts.quality)
		if err != nil {
			failed++
			if err := reportRefError(s, mode, pair.raw, "resolve", err); err != nil {
				if pipeline.IsBrokenPipe(err) {
					return nil
				}
				return err
			}
			continue
		}
		plan, err := caps.planner.PlanDownload(pair.id, res, opts.quality, opts.kind)
		if err != nil {
			failed++
			if err := reportRefError(s, mode, pair.raw, "plan", err); err != nil {
				if pipeline.IsBrokenPipe(err) {
					return nil
				}
				return err
			}
			continue
		}
		if len(plan) == 0 {
			// The kind filter matched nothing: visible "nothing found" for
			// the ref on stderr, not a failure (PlanDownload's contract).
			fmt.Fprintf(s.Err, "nothing found for %s\n", pair.raw)
			continue
		}
		source := ""
		if len(res) > 0 {
			source = res[0].Source
		}
		namer := eng.newRefNamer()
		refFailed := false
		for _, pf := range plan {
			tgt, err := namer.target(pf, pair.user, outDir)
			if err != nil {
				refFailed = true
				if err := reportRefError(s, mode, pair.raw, "download", err); err != nil {
					if pipeline.IsBrokenPipe(err) {
						return nil
					}
					return err
				}
				continue
			}
			rec, skipped, err := fetchFile(ctx, caps.downloader, pf, tgt, opts.onExists, pair.raw, source)
			if err != nil {
				refFailed = true
				if err := reportRefError(s, mode, pair.raw, "download", err); err != nil {
					if pipeline.IsBrokenPipe(err) {
						return nil
					}
					return err
				}
				continue
			}
			if mode == pipeline.ModeNDJSON {
				if err := writeDownloadEnvelope(s.Out, rec); err != nil {
					if pipeline.IsBrokenPipe(err) {
						// The reader hung up mid-stream (head/tail on a
						// pipe): from the command's perspective the run
						// succeeded — pixiv sigpipe policy. Windows may
						// surface broken pipes as a different errno
						// (ERROR_BROKEN_PIPE), so this portable check is
						// best-effort.
						return nil
					}
					return err
				}
			}
			rows = append(rows, fileRow{rec: rec, skipped: skipped})
		}
		if refFailed {
			failed++
		}
	}

	switch mode {
	case pipeline.ModeJSON:
		recs := make([]nitter.DownloadRecord, 0, len(rows))
		for _, r := range rows {
			recs = append(recs, r.rec)
		}
		var v any = recs
		if len(recs) == 1 {
			v = recs[0]
		}
		b, err := jsonx.MarshalLine(v)
		if err != nil {
			return err
		}
		if _, err := s.Out.Write(b); err != nil {
			return err
		}
	case pipeline.ModeHuman, pipeline.ModeText:
		for _, r := range rows {
			if _, err := fmt.Fprintln(s.Out, downloadLine(r.rec, r.skipped)); err != nil {
				return err
			}
		}
	}
	if len(rows) == 0 && failed == 0 {
		fmt.Fprintln(s.Err, "(empty)")
	}
	if failed > 0 {
		return fmt.Errorf("download completed with %d of %d refs failed", failed, len(pairs))
	}
	return nil
}

// collectRefs gathers the batch inputs: the positional arguments when any
// are given — stdin is never read then, so a positional batch cannot hang on
// a pipe whose writer stays open — or, with no positional argument and a
// non-TTY stdin, the stdin stream. A stdin payload whose first
// non-whitespace byte is "{" switches to envelope mode (strict
// nitter.pipeline/v1 tweet envelopes; each record's data.url becomes the
// ref, a malformed envelope is a usage error); anything else is plain refs,
// one per non-empty line. Neither source yields a ref is a usage error.
func collectRefs(s *invocation.Streams, args []string) ([]string, error) {
	if len(args) > 0 {
		return append([]string(nil), args...), nil
	}
	var refs []string
	if s.In != nil && !s.InIsTTY {
		data, err := io.ReadAll(s.In)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		trimmed := bytes.TrimLeft(data, " 	\r\n")
		switch {
		case len(trimmed) == 0:
			// Empty stdin: fall through to the no-refs usage error.
		case trimmed[0] == '{':
			return parseEnvelopeRefs(data)
		default:
			refs = nonEmptyLines(bytes.NewReader(data))
		}
	}
	if len(refs) == 0 {
		return nil, invocation.Usagef("usage: nitter download <REF>...")
	}
	return refs, nil
}

// parseEnvelopeRefs extracts one ref per strict nitter.pipeline/v1 tweet
// envelope line: schema and kind must match the protocol exactly and every
// record must carry a non-empty data.url. Any deviation is a usage error
// naming the line (a malformed envelope is a visible error, never skipped).
func parseEnvelopeRefs(data []byte) ([]string, error) {
	var refs []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var env struct {
			Schema string `json:"schema"`
			Kind   string `json:"kind"`
			Data   struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		dec := json.NewDecoder(strings.NewReader(line))
		if err := dec.Decode(&env); err != nil || dec.More() {
			return nil, invocation.Usagef("download: stdin line %d is not a valid %s envelope: %v", lineNo, pipeline.Schema, err)
		}
		if env.Schema != pipeline.Schema {
			return nil, invocation.Usagef("download: stdin line %d carries schema %q, want %q", lineNo, env.Schema, pipeline.Schema)
		}
		if env.Kind != pipeline.KindTweet {
			return nil, invocation.Usagef("download: stdin line %d carries kind %q, want %q", lineNo, env.Kind, pipeline.KindTweet)
		}
		if env.Data.URL == "" {
			return nil, invocation.Usagef("download: stdin line %d carries no data.url to download", lineNo)
		}
		refs = append(refs, env.Data.URL)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return refs, nil
}

// nonEmptyLines reads every line from r, keeping the CR/LF-stripped,
// whitespace-trimmed non-empty ones.
func nonEmptyLines(r io.Reader) []string {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// fetchFile downloads one planned file to its resolved target: the plan's
// URL first, then its fallbacks in order; the row reports whichever candidate
// succeeded. All candidates land on the SAME target name — an existing target
// is NOT a candidate failure — so refuse reports the refusal as the entry's
// error, skip converts it into a skip row (existing file's on-disk size, no
// sha256) and overwrite never sees one (force=true). skipped distinguishes
// the skip row for the text renderer.
func fetchFile(ctx context.Context, dl client.MediaDownloader, pf client.PlannedFile, tgt fileTarget, onExists, ref, source string) (rec nitter.DownloadRecord, skipped bool, err error) {
	var lastErr error
	for _, u := range append([]string{pf.URL}, pf.Fallbacks...) {
		rec, err = fetchOneURL(ctx, dl, u, pf, tgt, onExists == "overwrite")
		if err == nil {
			rec.Ref = ref
			rec.Kind = pf.Kind
			rec.Source = source
			return rec, false, nil
		}
		if errors.Is(err, client.ErrFileExists) {
			if onExists == "skip" {
				rec, err = skipRecord(err, pf, tgt, ref, source)
				if err != nil {
					return nitter.DownloadRecord{}, false, err
				}
				return rec, true, nil
			}
			return nitter.DownloadRecord{}, false, err
		}
		lastErr = err
	}
	return nitter.DownloadRecord{}, false, lastErr
}

// fetchOneURL fetches one candidate URL: FetchToFile when the target name
// carries its extension, FetchToFileAuto (Content-Type-derived extension,
// appended after the final rendered name) otherwise.
func fetchOneURL(ctx context.Context, dl client.MediaDownloader, u string, pf client.PlannedFile, tgt fileTarget, force bool) (nitter.DownloadRecord, error) {
	if tgt.extKnown {
		return dl.FetchToFile(ctx, u, filepath.Join(tgt.dir, tgt.name), force)
	}
	return dl.FetchToFileAuto(ctx, u, filepath.Join(tgt.dir, tgt.name), defaultExtForKind(pf.Kind), force)
}

// defaultExtForKind is the kind-based default extension the auto path falls
// back to when the response's Content-Type maps to nothing (ruling R-M9-4:
// .jpg for images and covers, .mp4 for videos and GIFs).
func defaultExtForKind(kind string) string {
	switch kind {
	case "video", "gif":
		return ".mp4"
	default:
		return ".jpg"
	}
}

// skipRecord builds the skip row for an existence refusal: the existing
// file's actual on-disk size, no URL (nothing was downloaded) and no sha256
// (nothing is fabricated) — the documented skip contract. The path comes
// from the target when the extension was known and from the refusal itself
// on the auto-extension path (only the downloader knows the derived name).
func skipRecord(existsErr error, pf client.PlannedFile, tgt fileTarget, ref, source string) (nitter.DownloadRecord, error) {
	path := ""
	if tgt.extKnown {
		path = filepath.Join(tgt.dir, tgt.name)
	} else if p, ok := client.ExistsPath(existsErr); ok {
		path = p
	}
	if path == "" {
		return nitter.DownloadRecord{}, existsErr
	}
	info, err := os.Stat(path)
	if err != nil {
		return nitter.DownloadRecord{}, err
	}
	return nitter.DownloadRecord{Ref: ref, Path: path, Kind: pf.Kind, Source: source, Bytes: info.Size()}, nil
}

// downloadLine renders one downloaded file as the human-readable,
// tab-separated row:
//
//	ref <TAB> path <TAB> bytes <TAB> kind <TAB> source
//
// A skipped row appends " (skipped)" to the path cell — the file is on disk,
// it was not re-downloaded.
func downloadLine(rec nitter.DownloadRecord, skipped bool) string {
	path := rec.Path
	if skipped {
		path += " (skipped)"
	}
	return strings.Join([]string{
		rec.Ref,
		path,
		strconv.FormatInt(rec.Bytes, 10),
		rec.Kind,
		rec.Source,
	}, "\t")
}

// reportRefError reports one ref's failure: an in-place error envelope on
// the NDJSON stream (command "download", stage = resolve|plan|download,
// code = the SDK error Kind of the failure — "error" as the fallback for
// anything unclassified; meta.input = the raw ref), or a stderr line in the
// default modes. Stderr writes ignore errors like everywhere else.
func reportRefError(s *invocation.Streams, mode pipeline.Mode, ref, stage string, err error) error {
	if mode == pipeline.ModeNDJSON {
		code := "error"
		var terr *nitter.Error
		if errors.As(err, &terr) {
			code = string(terr.Kind)
		}
		return pipeline.WriteErrorEnvelope(s.Out, opDownload, stage, code, ref, err.Error())
	}
	fmt.Fprintf(s.Err, "error: %s: %s\n", ref, err)
	return nil
}

// writeDownloadEnvelope emits one nitter.pipeline/v1 envelope for one
// downloaded file — kind download, the absolute on-disk path as id, the
// DownloadRecord as data, the raw input ref as meta.input. A write error is
// returned for the caller to classify (EPIPE = consumer hung up).
func writeDownloadEnvelope(out io.Writer, rec nitter.DownloadRecord) error {
	return pipeline.WriteEnvelope(out, pipeline.Envelope{
		Schema: pipeline.Schema,
		Kind:   pipeline.KindDownload,
		ID:     rec.Path,
		Data:   rec,
		Meta:   &pipeline.Meta{Input: rec.Ref},
	})
}
