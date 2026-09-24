// Package get implements the `nitter get` command: fetch one single status
// through the wiring layer and render it in the resolved output mode. Data
// acquisition goes through internal/cli/client and sdk models only — per
// ruling R11 this package never imports internal/nitter/*.
package get

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/sdk"
)

// New builds the `nitter get <REF>` command over the shared streams.
//
// REF is a bare numeric status ID or a status URL (x.com, twitter.com or any
// Nitter instance, shape /<user>/status/<id>; the user segment is optional
// on Nitter's /status/<id> route; /photo/N and /video/1 suffixes are
// accepted). A positional REF always wins; only when none is given AND stdin
// is not a TTY is the reference read from stdin (one line; a CR/LF-stripped,
// trimmed line) — stdin is never touched while a positional REF is present,
// so `nitter get <REF>` cannot block on a pipe whose writer stays open.
// There are no pagination flags — a single status is fetched.
//
// Exit codes (repo-wide semantics): success exits 0; acquisition failure
// (every instance failed) exits 1 with the classified error message; usage
// problems (bad/missing ref, extra arguments, --json with --ndjson, invalid
// config values) exit 2.
func New(s *invocation.Streams) *cobra.Command {
	var (
		asJSON   bool
		asNDJSON bool
	)
	cmd := &cobra.Command{
		Use:   "get <REF>",
		Short: "Fetch one single status by ID or URL",
		Long: `Fetch the single status REF and print one row:

  <ID>  <YYYY-MM-DD HH:MM>  @<handle>  <single-line text>

REF is a bare numeric status ID, or a status URL — x.com, twitter.com or
any Nitter instance, with the shape <user>/status/<id>; the user segment is
optional (Nitter also serves /status/<id> directly), and the /photo/N and
/video/1 suffixes of status permalinks are accepted. A positional REF always
wins; only with no argument AND a non-TTY stdin is the reference read from
stdin (one line). A positional REF never reads stdin, so it cannot block on
an open pipe.

The page's quoted tweet, when present, is summarized (visible in --json
/--ndjson output); interaction counts are not reported — the source
projection does not carry them reliably and none are fabricated. Instances
are tried in config order; an instance whose fetch fails cools down while
the next one is tried.

--json prints the tweet as one JSON object. --ndjson instead prints one
nitter.pipeline/v1 envelope (kind tweet, the tweet ID as id, the Tweet as
data, provenance in meta; meta.source is "status:" plus the numeric ID).
--json and --ndjson are mutually exclusive.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return invocation.Usagef("usage: nitter get <REF>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args, asJSON, asNDJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Print the tweet as one JSON object")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false,
		"Print one nitter.pipeline/v1 envelope (kind tweet)")
	return cmd
}

// run executes one single-status fetch. The ref comes from the positional
// argument; only when none is given and stdin is not a TTY is it read from
// one stdin line. A positional ref short-circuits the stdin branch entirely
// — stdin is never read, so the command cannot hang on a pipe whose writer
// stays open (the pre-0.7.4 code read stdin first and reported an ambiguity
// error, which blocked forever on `nitter get <REF> |`-style pipelines).
func run(cmd *cobra.Command, s *invocation.Streams, args []string, asJSON, asNDJSON bool) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	ref := ""
	if len(args) == 1 {
		ref = args[0]
	}
	if len(args) == 0 && s.In != nil && !s.InIsTTY {
		// Only a non-empty stdin line counts as "given on stdin": an empty
		// payload (e.g. `echo "" | nitter get`) carries no ref and falls
		// through to the usage error below. An explicitly supplied empty
		// argument is a positional argument too, so it never reaches here.
		if line, ok := firstLine(s.In); ok && line != "" {
			ref = line
		}
	}
	if ref == "" {
		return invocation.Usagef("usage: nitter get <REF>")
	}
	// Local ref parsing so bad input exits 2 before any wiring is built
	// (and before any network) — the same local-validation pattern the user
	// command applies to handles; appapi.Status re-parses as the
	// R11-internal backstop.
	if _, _, err := client.ParseStatusRef(ref); err != nil {
		return client.AsUsageError(err)
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
	tw, instance, err := w.Status().Status(ctx, ref)
	if err != nil {
		// Invalid-argument classifications (none expected past the local
		// validation) map to usage errors; acquisition failures exit 1.
		return client.AsUsageError(err)
	}
	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		err := writeNDJSON(s.Out, tw, instance, fetchedAt)
		if errors.Is(err, syscall.EPIPE) {
			// The reader hung up mid-stream (head/tail on a pipe): from the
			// command's perspective the run succeeded — pixiv sigpipe
			// policy. Windows may surface broken pipes as a different errno
			// (ERROR_BROKEN_PIPE), so this portable check is best-effort.
			return nil
		}
		return err
	case pipeline.ModeJSON:
		return writeJSON(s.Out, tw)
	default:
		// ModeHuman and ModeText share the row rendering: the tweet's text
		// row only — no media listing (the row protocol is one line).
		row := result.TweetRows([]nitter.Tweet{tw})[0]
		fmt.Fprintln(s.Out, row.Line())
		return nil
	}
}

// firstLine reads one line from r: scanner text with surrounding whitespace
// trimmed (ScanLines already strips the trailing CR/LF). ok is false at EOF
// or on a scan error.
func firstLine(r io.Reader) (line string, ok bool) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() {
		return "", false
	}
	line = strings.TrimSpace(sc.Text())
	return line, true
}

// writeJSON prints the tweet as one JSON object (a single status never
// marshals as an array).
func writeJSON(out io.Writer, tw nitter.Tweet) error {
	b, err := jsonx.MarshalLine(tw)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}

// writeNDJSON prints exactly one nitter.pipeline/v1 envelope: kind tweet,
// the tweet ID as id, the Tweet as data, and provenance in meta (source
// "status:<id>" — the numeric ID, not the raw ref —, the instance base URL
// that produced the tweet, and the RFC3339 UTC fetch timestamp).
func writeNDJSON(out io.Writer, tw nitter.Tweet, instance, fetchedAt string) error {
	env := pipeline.Envelope{
		Schema: pipeline.Schema,
		Kind:   pipeline.KindTweet,
		ID:     tw.ID,
		Data:   tw,
		Meta: &pipeline.Meta{
			Source:    "status:" + tw.ID,
			Instance:  instance,
			FetchedAt: fetchedAt,
		},
	}
	return pipeline.WriteEnvelope(out, env)
}
