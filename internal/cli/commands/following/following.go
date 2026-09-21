// Package following implements the `nitter following` command: fetch one
// account's following list through the wiring layer and render it in the
// resolved output mode.
package following

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// handleRe is the X handle contract: 1-15 letters, digits or underscores.
var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

// New builds the `nitter following <HANDLE>` command over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	var (
		limitFlag int
		asJSON    bool
		asNDJSON  bool
	)
	cmd := &cobra.Command{
		Use:           "following <HANDLE>",
		Short:         "Fetch accounts followed by HANDLE",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Fetch accounts followed by HANDLE (1-15 letters, digits or underscores,
without the @) and print one row per followed profile:

  @<handle>  <name>  <followers_count>  <bio>

--limit caps the number of profiles to fetch (default: 20, 0 = all).
--json prints a JSON array of profile objects ([] when empty).
--ndjson prints one nitter.pipeline/v1 envelope per profile (kind profile).
--json and --ndjson are mutually exclusive.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter following <HANDLE>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args[0], limitFlag, asJSON, asNDJSON)
		},
	}
	cmd.Flags().IntVar(&limitFlag, "limit", 20, "Maximum profiles to fetch (default: 20, 0 = all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print a JSON array of profile objects")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope per profile (kind profile)")
	return cmd
}

func run(cmd *cobra.Command, s *invocation.Streams, handle string, limitFlag int, asJSON, asNDJSON bool) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}
	if !handleRe.MatchString(handle) {
		return invocation.Usagef("following: %q is not a valid handle (1-15 letters, digits or underscores, without the @)", handle)
	}
	if limitFlag < 0 {
		return invocation.Usagef("following: --limit must be >= 0 (0 means all)")
	}

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

	profiles, _, err := w.Following().Following(ctx, handle, limitFlag, "")
	if err != nil {
		return client.AsUsageError(err)
	}

	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		for _, p := range profiles {
			env := pipeline.Envelope{
				Schema: pipeline.Schema,
				Kind:   pipeline.KindProfile,
				ID:     p.Handle,
				Data:   p,
				Meta: &pipeline.Meta{
					Source:    "following:" + handle,
					Instance:  "FxTwitter",
					FetchedAt: fetchedAt,
				},
			}
			if err := pipeline.WriteEnvelope(s.Out, env); err != nil {
				if errors.Is(err, syscall.EPIPE) {
					return nil
				}
				return err
			}
		}
		return nil
	case pipeline.ModeJSON:
		if profiles == nil {
			profiles = []nitter.Profile{}
		}
		b, err := jsonx.MarshalLine(profiles)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	default:
		rows := result.ProfileRows(profiles)
		if len(rows) == 0 {
			fmt.Fprintln(s.Err, "(empty)")
			return nil
		}
		for _, r := range rows {
			fmt.Fprintln(s.Out, r.Line())
		}
		return nil
	}
}
