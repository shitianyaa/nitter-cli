// Package media implements the `nitter media` command: resolve one or more
// status references into directly downloadable media links (video mp4
// variants, original images, GIFs) through the wiring layer's resolution
// strategies. Data acquisition goes through internal/cli/client and sdk
// models only — per ruling R11 this package never imports internal/media or
// internal/nitter/*.
package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// opMedia is the command name stamped into error envelopes.
const opMedia = "media"

// Strategy names accepted by --strategy. auto is the ordered chain
// fx → nitter → xdown; an explicit name runs only that one.
var validStrategies = map[string]bool{
	"auto":   true,
	"fx":     true,
	"nitter": true,
	"xdown":  true,
}

// autoStrategies is the chain --strategy auto expands to. nitter is included
// verbatim: it consults the wiring's instance base and reports a clear
// local-state failure in the aggregate when no instance is configured —
// never silently skipped (the third-party failure rule: honest strategy
// reporting).
var autoStrategies = []string{"fx", "nitter", "xdown"}

var validQualities = map[string]bool{
	"high":   true,
	"medium": true,
	"low":    true,
}

// New builds the `nitter media <REF>...` command over the shared streams.
//
// REF is a bare numeric status ID or a status URL (x.com, twitter.com or any
// Nitter instance, shape /<user>/status/<id>; /photo/N and /video/1 suffixes
// are accepted) — the same reference shapes `nitter get` takes. Multiple
// REFs run as a batch. Positional REFs always win; only when none is given
// AND stdin is not a TTY are the references read from stdin (one per
// non-empty line) — stdin is never touched while positional REFs are
// present, so a batch invocation cannot block on a pipe whose writer stays
// open.
//
// Exit codes (repo-wide semantics): success exits 0; a per-REF resolution
// failure gets an in-place error report (kind:"error" envelope on the NDJSON
// stream, a stderr line otherwise) while the other refs continue, and the
// command exits 1 with a "media completed with N of M refs failed" summary
// when at least one ref failed; usage problems (invalid --strategy or
// --quality, bad/missing refs, --json with --ndjson) exit 2.
func New(s *invocation.Streams) *cobra.Command {
	var (
		strategy string
		quality  string
		probe    bool
		asJSON   bool
		asNDJSON bool
	)
	cmd := &cobra.Command{
		Use:   "media <REF>...",
		Short: "Resolve statuses into downloadable media links",
		Long: `Resolve each status REF into directly downloadable media links:
video mp4 variants, original images and GIFs. One tab-separated row per
media entry:

  <ref>  <source>  <kind>  <url>  <label>  <duration>  <size>

source names the strategy that produced the entry, kind is image/video/gif,
and the trailing cells are "-" when absent. The download itself is the
caller's job — the command resolves links, it does not fetch media.

REF is a bare numeric status ID, or a status URL — x.com, twitter.com or any
Nitter instance, shape <user>/status/<id> (/photo/N and /video/1 suffixes
accepted). Multiple REFs run as a batch. Positional REFs always win; only
when none is given AND stdin is not a TTY are the references read from stdin
(one per non-empty line) — a positional REF never reads stdin, so a batch
cannot block on an open pipe.

Strategies (--strategy, default auto): auto tries fx → nitter → xdown in
order and returns the first strategy that yields media
(source stamps which one won); an explicit name runs only that one. fx and
xdown are THIRD-PARTY public services — resolving sends the
tweet URL to them, so only resolve public statuses you are fine sharing;
their failures are reported with the real strategy name. nitter reads the
status page from YOUR configured instance ([[instances]] first entry, or
--instance) and reports a clear failure when none is configured.

--quality high|medium|low (default high) picks the image pbs tier (orig /
large / small) and which video variant becomes the main URL (highest
bitrate / upper median / lowest non-zero); every variant is kept in the
variants list either way. Unlike the reference plugin, the CLI returns ALL
media entries of a status — it does not skip images when a video is
present; consumers choose.

--probe adds one ranged GET per video/gif URL (mp4 duration from the movie
header, size from the Content-Range total). Probing is best-effort: any
failure leaves the values empty and never fails the run. Images are not
probed.

--json prints the resolutions as one JSON document: a single object when
exactly one media entry resolved, an array otherwise, [] when nothing
resolved. --ndjson instead prints one nitter.pipeline/v1 envelope per
media entry (kind media, the download URL as id, meta.input = the raw ref)
and one kind:"error" envelope per failed ref. --json and --ndjson are
mutually exclusive. A status without media resolves as a not_found error
(the resolver contract: every strategy reports empty).`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args, strategy, quality, probe, asJSON, asNDJSON)
		},
	}
	cmd.Flags().StringVar(&strategy, "strategy", "auto",
		"Resolution strategy: auto (fx→nitter→xdown) or one of fx, nitter, xdown")
	cmd.Flags().StringVar(&quality, "quality", "high",
		"Media quality tier: high, medium or low")
	cmd.Flags().BoolVar(&probe, "probe", false,
		"Probe each video/gif URL for duration and size (one extra ranged request per entry; best-effort)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Print the resolutions as one JSON document (single object when exactly one entry, array otherwise)")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false,
		"Print one nitter.pipeline/v1 envelope per media entry (kind media)")
	return cmd
}

// run executes the media resolution batch: collect refs (arguments or
// stdin), validate the flags, resolve each ref through the wiring's
// MediaResolver, apply the optional --probe pass, and render in the resolved
// output mode.
func run(cmd *cobra.Command, s *invocation.Streams, args []string, strategy, quality string, probe, asJSON, asNDJSON bool) error {
	// Flag conflicts are input-contract problems: resolve before anything
	// else runs so --json --ndjson exits 2 up front.
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if !validStrategies[strategy] {
		return invocation.Usagef("media: unknown --strategy %q (want auto, fx, nitter or xdown)", strategy)
	}
	if !validQualities[quality] {
		return invocation.Usagef("media: unknown --quality %q (want high, medium or low)", quality)
	}

	refs, err := collectRefs(s, args)
	if err != nil {
		return err
	}
	// Local ref parsing so bad input exits 2 before any wiring is built (and
	// before any network) — the same local-validation pattern `get` applies;
	// the resolver re-parses as the R11-internal backstop.
	type refPair struct {
		raw  string
		id   string
		user string
	}
	pairs := make([]refPair, 0, len(refs))
	for _, ref := range refs {
		id, user, err := client.ParseStatusRef(ref)
		if err != nil {
			return client.AsUsageError(err)
		}
		pairs = append(pairs, refPair{raw: ref, id: id, user: user})
	}

	// The root PersistentPreRunE has published the baseline config by now;
	// a bad value in it is a usage error (exit 2).
	cfg, err := client.LoadEffectiveSettings()
	if err != nil {
		return err
	}
	w, err := client.Build(s.RootOptions, cfg, time.Now)
	if err != nil {
		return client.AsUsageError(err)
	}

	ctx := s.CTX
	if ctx == nil {
		ctx = context.Background()
	}
	strategies := autoStrategies
	if strategy != "auto" {
		strategies = []string{strategy}
	}

	resolver := w.Media()
	emitted := make([]nitter.MediaResolution, 0, len(pairs))
	failed := 0
	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := resolver.ResolveMedia(ctx, pair.id, pair.user, strategies, quality)
		if err != nil {
			failed++
			if err := reportRefError(s, mode, pair.raw, err); err != nil {
				return err
			}
			continue
		}
		// Ref echoes the raw input ref (Task 1 review minor #5, Task 4
		// ruling): the consumer sees what it passed, not a canonicalized URL.
		for i := range res {
			res[i].Ref = pair.raw
		}
		if probe {
			applyProbe(ctx, resolver, res)
		}
		// NDJSON streams per-ref and in ref order (javdb batch semantics):
		// each ref's envelopes — media, or the in-place error — land on the
		// stream as soon as that ref is settled. The other modes collect.
		if mode == pipeline.ModeNDJSON {
			if err := writeNDJSON(s.Out, res); err != nil {
				if pipeline.IsBrokenPipe(err) {
					// The reader hung up mid-stream (head/tail on a pipe):
					// from the command's perspective the run succeeded —
					// pixiv sigpipe policy. Windows may surface broken pipes
					// as a different errno (ERROR_BROKEN_PIPE), so this
					// portable check is best-effort.
					return nil
				}
				return err
			}
		}
		emitted = append(emitted, res...)
	}

	switch mode {
	case pipeline.ModeJSON:
		var v any = emitted
		if len(emitted) == 1 {
			v = emitted[0]
		}
		b, err := jsonx.MarshalLine(v)
		if err != nil {
			return err
		}
		if _, err := s.Out.Write(b); err != nil {
			return err
		}
	case pipeline.ModeHuman, pipeline.ModeText:
		// The shared row rendering (already tab-separated and
		// script-friendly). NDJSON wrote its envelopes inline above.
		for _, res := range emitted {
			if _, err := fmt.Fprintln(s.Out, result.MediaLine(res)); err != nil {
				return err
			}
		}
	}
	if len(emitted) == 0 && failed == 0 {
		fmt.Fprintln(s.Err, "(empty)")
	}
	if failed > 0 {
		return fmt.Errorf("media completed with %d of %d refs failed", failed, len(pairs))
	}
	return nil
}

// collectRefs gathers the batch inputs: the positional arguments when any
// are given — stdin is never read then, so a positional batch cannot hang on
// a pipe whose writer stays open — or, with no positional argument and a
// non-TTY stdin, every non-empty stdin line. Neither source yields a ref is
// a usage error.
func collectRefs(s *invocation.Streams, args []string) ([]string, error) {
	if len(args) > 0 {
		return append([]string(nil), args...), nil
	}
	var refs []string
	if s.In != nil && !s.InIsTTY {
		refs = nonEmptyLines(s.In)
	}
	if len(refs) == 0 {
		return nil, invocation.Usagef("usage: nitter media <REF>...")
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

// applyProbe runs the best-effort --probe enrichment over one ref's
// resolutions: each video/gif URL gets one ranged probe; on success the zero
// values are filled (a source-reported duration is never overwritten), on
// ANY error the resolution keeps its values and the batch continues —
// probing is never fatal (Task 3 review mandate). Images are not probed.
func applyProbe(ctx context.Context, resolver client.MediaResolver, res []nitter.MediaResolution) {
	for i := range res {
		if res[i].Kind != "video" && res[i].Kind != "gif" {
			continue
		}
		duration, size, err := resolver.ProbeMedia(ctx, res[i].URL)
		if err != nil {
			continue // probe unavailable: keep zeros, never fatal
		}
		if res[i].DurationSeconds == 0 {
			res[i].DurationSeconds = duration
		}
		if res[i].SizeBytes == 0 {
			res[i].SizeBytes = size
		}
	}
}

// reportRefError reports one ref's resolution failure: an in-place error
// envelope on the NDJSON stream (command "media", stage "resolve", code =
// the SDK error Kind of the failure — "error" as the fallback for anything
// unclassified; meta.input = the raw ref), or a stderr line in the default
// modes. Stderr writes ignore errors like everywhere else.
func reportRefError(s *invocation.Streams, mode pipeline.Mode, ref string, err error) error {
	if mode == pipeline.ModeNDJSON {
		code := "error"
		var terr *nitter.Error
		if errors.As(err, &terr) {
			code = string(terr.Kind)
		}
		return pipeline.WriteErrorEnvelope(s.Out, opMedia, "resolve", code, ref, err.Error())
	}
	fmt.Fprintf(s.Err, "error: %s: %s\n", ref, err)
	return nil
}

// writeNDJSON emits one nitter.pipeline/v1 envelope per media entry —
// kind media, the download URL as id, the MediaResolution as data, the raw
// input ref as meta.input — in resolution order. It writes exactly one
// ref's share per call (the caller streams per-ref, in ref order); a write
// error is returned for the caller to classify (EPIPE = consumer hung up).
func writeNDJSON(out io.Writer, res []nitter.MediaResolution) error {
	for _, r := range res {
		env := pipeline.Envelope{
			Schema: pipeline.Schema,
			Kind:   pipeline.KindMedia,
			ID:     r.URL,
			Data:   r,
			Meta:   &pipeline.Meta{Input: r.Ref},
		}
		if err := pipeline.WriteEnvelope(out, env); err != nil {
			return err
		}
	}
	return nil
}
