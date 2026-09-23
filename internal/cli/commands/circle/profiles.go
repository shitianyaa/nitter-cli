package circle

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/common/jsonx"
	"github.com/shitianyaa/nitter-cli/internal/config/paths"
	"github.com/shitianyaa/nitter-cli/internal/config/settings"
)

// newRefreshCmd builds `nitter circle refresh <NAME>`: the only networked
// writer of the profile sidecar. It fetches each member's profile and merges
// the fact fields into ~/.nitter-cli/profiles.toml, leaving judgement fields
// (role/note/noted_at) untouched.
func newRefreshCmd(s *invocation.Streams) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "refresh <NAME>",
		Short:         "Refresh cached profiles for a circle's members",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invocation.Usagef("usage: nitter circle refresh <NAME>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRefresh(cmd, s, args[0])
		},
	}
	return cmd
}

func runRefresh(cmd *cobra.Command, s *invocation.Streams, name string) error {
	p, err := paths.New()
	if err != nil {
		return err
	}
	circles, err := settings.LoadCircles(p.CirclesFile)
	if err != nil {
		return err
	}
	circle, ok := settings.FindCircle(circles, name)
	if !ok {
		return fmt.Errorf("circle %q not found", name)
	}
	if len(circle.Users) == 0 {
		fmt.Fprintln(s.Err, "(empty)")
		return nil
	}

	profiles, err := settings.LoadProfiles(p.ProfilesFile)
	if err != nil {
		return err
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

	now := time.Now()
	refreshed := 0
	var lastErr error
	for _, handle := range circle.Users {
		prof, err := w.Profile().Profile(ctx, handle)
		if err != nil {
			lastErr = err
			fmt.Fprintf(s.Err, "warning: circle refresh: @%s: %v\n", handle, err)
			continue
		}
		profiles = settings.MergeProfileFacts(profiles, handle, settings.Profile{
			Handle:         prof.Handle,
			Name:           prof.Name,
			Bio:            prof.Bio,
			FollowersCount: prof.FollowersCount,
		}, now)
		refreshed++
	}

	if refreshed == 0 && lastErr != nil {
		return client.AsUsageError(lastErr)
	}
	if err := settings.SaveProfiles(p.ProfilesFile, profiles); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "refreshed %d/%d members in circle %s\n", refreshed, len(circle.Users), circle.Key)
	return nil
}

// sidecarRow is the JSON projection of one roster member joined with its
// sidecar record. An absent record yields empty facts (fetched_at == "").
type sidecarRow struct {
	Handle         string `json:"handle"`
	Name           string `json:"name"`
	Bio            string `json:"bio"`
	FollowersCount int    `json:"followers_count"`
	FetchedAt      string `json:"fetched_at"`
	Role           string `json:"role"`
	Note           string `json:"note"`
}

// writeSidecarJSON writes the joined rows as a JSON array in roster order.
func writeSidecarJSON(s *invocation.Streams, rows []sidecarRow) error {
	if rows == nil {
		rows = []sidecarRow{}
	}
	b, err := jsonx.MarshalLine(rows)
	if err != nil {
		return err
	}
	_, err = s.Out.Write(b)
	return err
}

// renderSidecarLine renders one human row:
//
//	@<handle>\t<followers>\t<role>\t<name>\t<bio>\t<age>
//
// Fact columns with no data render as "-" so a member is never silently
// dropped. `role` is hand-recorded judgement rather than a fetched fact, so it
// is shown even for a member with no cached facts yet — a recorded call must
// never look lost merely because `circle refresh` has not run.
func renderSidecarLine(handle string, prof settings.Profile, hasCache bool, now time.Time) string {
	role := dashIfEmpty(prof.Role)
	if !hasCache {
		return strings.Join([]string{"@" + handle, "-", role, "-", "-", "-"}, "\t")
	}
	followers := strconv.Itoa(prof.FollowersCount)
	name := dashIfEmpty(shortBio(prof.Name))
	bio := dashIfEmpty(shortBio(prof.Bio))
	age := settings.ProfileAge(prof.FetchedAt, now)
	return strings.Join([]string{"@" + handle, followers, role, name, bio, age}, "\t")
}

func dashIfEmpty(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
