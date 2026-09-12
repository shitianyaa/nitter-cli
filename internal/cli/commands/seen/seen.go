// Package seen implements the `twitter seen` commands: inspect (list) and
// clear the persistent watch dedup state (seen.json). The commands operate
// on the default state location (paths.SeenFile) — the same file the watch
// command reads and writes by default. Source references are parsed with
// watch.ParseSource, so `--source` takes the same "user:NASA" form watch
// takes. State changes are gated behind --confirm (状态变更需显式授权) and
// persist through the store's atomic writes.
package seen

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/twitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/twitter-cli/internal/config/paths"
	seenstore "github.com/shitianyaa/twitter-cli/internal/storage/seen"
	"github.com/shitianyaa/twitter-cli/internal/watch"
)

// New builds the `twitter seen` command group over the shared streams.
// Bare `twitter seen` shows the help (cobra convention for command groups).
func New(s *invocation.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seen",
		Short: "Inspect and clear the persistent watch dedup state (seen.json)",
		Long: `Inspect and clear the persistent watch dedup state that twitter watch
keeps in ~/.twitter-cli/state/seen.json.

  twitter seen list [--source SOURCE] [--json]
      One summary line per source (or a JSON array with --json).

  twitter seen clear [--source SOURCE] --confirm
      Delete one source's entry, or every entry without --source. State
      changes need explicit authorization: --confirm is required.

A corrupt state file is a hard error (exit 1) — the store never silently
resets state, because a silent reset would re-push a whole watch history.`,
	}
	cmd.AddCommand(newList(s), newClear(s))
	return cmd
}

// newList builds `twitter seen list`.
//
// Exit codes: success (including an empty store) exits 0; usage problems
// (a bad --source value) exit 2; a corrupt state file exits 1.
func newList(s *invocation.Streams) *cobra.Command {
	var (
		sourceFlag string
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the stored dedup state per source",
		Long: `Print one summary line per source, sorted by source key:

  <key>  initialized=<bool>  seen=<n>  watermark=<n>  <updated_at RFC3339 or ->

(separated by tabs; key is "<kind>:<ref>" as watch stores it). --source
narrows the listing to one source, parsed exactly like a watch source
("user:NASA", "tag:%23AI", "list:12345"). --json prints a JSON array of
{source, initialized, seen_count, watermark_count, updated_at} — always an
array, even for a single source: it is a listing, the shape is stable. An
empty store prints the (empty) hint on stderr and nothing on stdout; with
--json it prints [] on stdout instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(s, sourceFlag, asJSON)
		},
	}
	cmd.Flags().StringVar(&sourceFlag, "source", "",
		"Only this source (same form as watch sources, e.g. user:NASA)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"Print a JSON array of {source, initialized, seen_count, watermark_count, updated_at}")
	return cmd
}

// sourceSummary is the projected per-source listing record (the --json
// shape). UpdatedAt is an RFC3339 UTC string, "" when the entry carries no
// stamp.
type sourceSummary struct {
	Source         string `json:"source"`
	Initialized    bool   `json:"initialized"`
	SeenCount      int    `json:"seen_count"`
	WatermarkCount int    `json:"watermark_count"`
	UpdatedAt      string `json:"updated_at"`
}

// runList lists the stored source states (all, or one with --source).
func runList(s *invocation.Streams, sourceFlag string, asJSON bool) error {
	filter, err := parseSourceFlag(sourceFlag)
	if err != nil {
		return err
	}
	store, err := openStore()
	if err != nil {
		return err
	}

	states := store.All()
	if filter != "" {
		state, ok := states[filter]
		if !ok {
			// A filter matching nothing is the same empty-listing contract
			// as an empty store (exit 0, idempotent reads).
			return writeEmpty(s, asJSON)
		}
		states = map[string]seenstore.SourceState{filter: state}
	}
	if len(states) == 0 {
		return writeEmpty(s, asJSON)
	}

	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	if asJSON {
		summaries := make([]sourceSummary, 0, len(keys))
		for _, key := range keys {
			summaries = append(summaries, summarize(key, states[key]))
		}
		b, err := jsonx.MarshalLine(summaries)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	}
	for _, key := range keys {
		sum := summarize(key, states[key])
		fmt.Fprintf(s.Out, "%s\tinitialized=%t\tseen=%d\twatermark=%d\t%s\n",
			sum.Source, sum.Initialized, sum.SeenCount, sum.WatermarkCount, formatUpdatedAt(sum.UpdatedAt))
	}
	return nil
}

// summarize projects one stored state into the listing record.
func summarize(key string, state seenstore.SourceState) sourceSummary {
	updatedAt := ""
	if !state.UpdatedAt.IsZero() {
		updatedAt = state.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return sourceSummary{
		Source:         key,
		Initialized:    state.Initialized,
		SeenCount:      len(state.SeenIDs),
		WatermarkCount: len(state.WatermarkIDs),
		UpdatedAt:      updatedAt,
	}
}

// formatUpdatedAt renders the human updated_at cell: the RFC3339 timestamp,
// or a dash when the entry carries no stamp (never fabricated).
func formatUpdatedAt(rfc3339 string) string {
	if rfc3339 == "" {
		return "-"
	}
	return rfc3339
}

// writeEmpty reports an empty listing: the (empty) hint on stderr in the
// default modes, a literal [] on stdout in --json mode (machine consumers
// get machine output, mirroring the data commands' JSON mode).
func writeEmpty(s *invocation.Streams, asJSON bool) error {
	if !asJSON {
		fmt.Fprintln(s.Err, "(empty)")
		return nil
	}
	if _, err := fmt.Fprintln(s.Out, "[]"); err != nil {
		return err
	}
	return nil
}

// newClear builds `twitter seen clear`.
//
// Exit codes: success exits 0 (including an absent --source target —
// clearing what is not there is idempotent and reports "not found" on
// stderr); the missing --confirm gate and a bad --source value exit 2; a
// corrupt state file exits 1.
func newClear(s *invocation.Streams) *cobra.Command {
	var (
		sourceFlag string
		confirm    bool
	)
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear the stored dedup state (all sources, or one with --source)",
		Long: `Delete the stored dedup state: every source's entry without --source, or
exactly one source's entry with --source (same form as watch sources). The
schema version of the file is kept.

State changes need explicit authorization (状态变更需显式授权): --confirm
is required, every time — authorization does not carry across commands.
Clearing a source that is not stored succeeds with a "not found" hint
(idempotent). Writes are atomic: the file is staged and renamed, so a
failure never leaves a half-cleared state behind.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClear(s, sourceFlag, confirm)
		},
	}
	cmd.Flags().StringVar(&sourceFlag, "source", "",
		"Only this source (same form as watch sources, e.g. user:NASA); without it every source is cleared")
	cmd.Flags().BoolVar(&confirm, "confirm", false,
		"Required: state changes need explicit authorization (状态变更需显式授权)")
	return cmd
}

// runClear deletes one or all source entries through the store's atomic
// writes. Success prints nothing (the exit code is the signal); an absent
// single-source target prints a "not found" hint on stderr.
func runClear(s *invocation.Streams, sourceFlag string, confirm bool) error {
	if !confirm {
		return invocation.Usagef("seen clear: state changes need explicit authorization (状态变更需显式授权) — pass --confirm to proceed")
	}
	filter, err := parseSourceFlag(sourceFlag)
	if err != nil {
		return err
	}
	store, err := openStore()
	if err != nil {
		return err
	}
	if filter != "" {
		deleted, err := store.Delete(filter)
		if err != nil {
			return err
		}
		if !deleted {
			fmt.Fprintf(s.Err, "not found: %s\n", filter)
		}
		return nil
	}
	return store.Clear()
}

// parseSourceFlag parses the shared --source flag value: empty means "no
// filter", anything else must parse as a watch source (same "user:NASA"
// contract as twitter watch).
func parseSourceFlag(sourceFlag string) (string, error) {
	if strings.TrimSpace(sourceFlag) == "" {
		return "", nil
	}
	src, err := watch.ParseSource(sourceFlag)
	if err != nil {
		return "", invocation.Usagef("seen: %v", err)
	}
	return src.Key(), nil
}

// openStore opens the default seen.json location. A corrupt file is a hard
// error (KindLocalState, exit 1) — never a silent reset.
func openStore() (*seenstore.Store, error) {
	p, err := paths.New()
	if err != nil {
		return nil, err
	}
	return seenstore.Open(p.SeenFile, time.Now)
}
