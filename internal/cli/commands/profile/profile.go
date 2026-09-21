// Package profile implements the `nitter profile` command: fetch one user
// profile through the wiring layer and render it in the resolved output mode.
package profile

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/pipeline"
	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
)

var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

// New builds the `nitter profile <HANDLE>` command over the shared streams.
func New(s *invocation.Streams) *cobra.Command {
	var (
		asJSON   bool
		asNDJSON bool
	)
	cmd := &cobra.Command{
		Use:           "profile <HANDLE>",
		Short:         "Fetch user profile and card for HANDLE",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Fetch user profile for HANDLE (1-15 letters, digits or underscores, without the @)
and print formatted user card:

  @<handle> (<name>)
  Bio:        <bio>
  Followers:  <count>
  Following:  <count>
  Tweets:     <count>
  Media:      <count>
  Avatar:     <url>
  Banner:     <url>

--json prints a JSON profile object.
--ndjson prints one nitter.pipeline/v1 envelope (kind profile).
--json and --ndjson are mutually exclusive.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter profile <HANDLE>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, s, args[0], asJSON, asNDJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON profile object")
	cmd.Flags().BoolVar(&asNDJSON, "ndjson", false, "Print one nitter.pipeline/v1 envelope (kind profile)")
	return cmd
}

func run(cmd *cobra.Command, s *invocation.Streams, rawHandle string, asJSON, asNDJSON bool) error {
	mode, err := pipeline.ResolveOutputMode(asNDJSON, asJSON, s.OutIsTTY)
	if err != nil {
		return err
	}

	cleanHandle := strings.TrimPrefix(strings.TrimSpace(rawHandle), "@")
	if !handleRe.MatchString(cleanHandle) {
		return invocation.Usagef("profile: %q is not a valid handle (1-15 letters, digits or underscores, without the @)", rawHandle)
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

	prof, err := w.Profile().Profile(ctx, cleanHandle)
	if err != nil {
		return client.AsUsageError(err)
	}

	fetchedAt := time.Now().UTC().Format(time.RFC3339)

	switch mode {
	case pipeline.ModeNDJSON:
		env := pipeline.Envelope{
			Schema: pipeline.Schema,
			Kind:   pipeline.KindProfile,
			ID:     prof.Handle,
			Data:   *prof,
			Meta: &pipeline.Meta{
				Source:    "profile:" + cleanHandle,
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
		return nil
	case pipeline.ModeJSON:
		b, err := jsonx.MarshalLine(prof)
		if err != nil {
			return err
		}
		_, err = s.Out.Write(b)
		return err
	default:
		card := result.ProfileCard(*prof)
		fmt.Fprintln(s.Out, card)
		return nil
	}
}
