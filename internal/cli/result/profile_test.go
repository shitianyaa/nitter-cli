package result_test

import (
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/result"
	"github.com/shitianyaa/nitter-cli/sdk"
)

func TestProfileRow(t *testing.T) {
	p := nitter.Profile{
		Handle:         "NASA",
		Name:           "NASA Official",
		FollowersCount: 1234567,
		Bio:            "Exploring the\nsecrets of the universe.\tFollow us!",
	}

	rows := result.ProfileRows([]nitter.Profile{p})
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	line := rows[0].Line()
	wantPrefix := "@NASA	NASA Official	1234567	"
	if len(line) < len(wantPrefix) || line[:len(wantPrefix)] != wantPrefix {
		t.Errorf("line = %q, want prefix %q", line, wantPrefix)
	}

	card := result.ProfileCard(p)
	if card == "" {
		t.Error("ProfileCard returned empty string")
	}
}
