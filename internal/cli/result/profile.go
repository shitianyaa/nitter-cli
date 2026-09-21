package result

import (
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
