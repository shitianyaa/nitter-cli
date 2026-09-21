package result

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// ProfileRow is the human row projection of one profile:
//
//	@<handle>\t<name>\t<followers_count>\t<bio>
type ProfileRow struct {
	Handle         string
	Name           string
	FollowersCount int
	Bio            string
}

// Line renders the tab-separated row.
func (r ProfileRow) Line() string {
	cells := []string{"@" + r.Handle, singleLine(r.Name), strconv.Itoa(r.FollowersCount), singleLine(r.Bio)}
	return strings.Join(cells, "\t")
}

// ProfileRows projects profiles into rows in input order.
func ProfileRows(ps []nitter.Profile) []ProfileRow {
	rows := make([]ProfileRow, 0, len(ps))
	for _, p := range ps {
		rows = append(rows, ProfileRow{
			Handle:         p.Handle,
			Name:           p.Name,
			FollowersCount: p.FollowersCount,
			Bio:            p.Bio,
		})
	}
	return rows
}

// ProfileCard renders a profile as a formatted user card.
func ProfileCard(p nitter.Profile) string {
	var sb strings.Builder
	handle := "@" + strings.TrimPrefix(p.Handle, "@")
	if p.Name != "" {
		fmt.Fprintf(&sb, "%s (%s)\n", handle, p.Name)
	} else {
		fmt.Fprintf(&sb, "%s\n", handle)
	}
	if p.Bio != "" {
		fmt.Fprintf(&sb, "Bio:        %s\n", p.Bio)
	}
	fmt.Fprintf(&sb, "Followers:  %d\n", p.FollowersCount)
	fmt.Fprintf(&sb, "Following:  %d\n", p.FollowingCount)
	fmt.Fprintf(&sb, "Tweets:     %d\n", p.TweetsCount)
	if p.MediaCount > 0 {
		fmt.Fprintf(&sb, "Media:      %d\n", p.MediaCount)
	}
	if p.AvatarURL != "" {
		fmt.Fprintf(&sb, "Avatar:     %s\n", p.AvatarURL)
	}
	if p.BannerURL != "" {
		fmt.Fprintf(&sb, "Banner:     %s\n", p.BannerURL)
	}
	if p.IsProtected {
		sb.WriteString("Protected:  true\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
