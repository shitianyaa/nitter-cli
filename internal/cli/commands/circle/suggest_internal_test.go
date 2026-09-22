package circle

import (
	"strings"
	"testing"
	"unicode/utf8"

	nitter "github.com/shitianyaa/nitter-cli/sdk"
)

func prof(handle string, followers int, bio string) nitter.Profile {
	return nitter.Profile{Handle: handle, FollowersCount: followers, Bio: bio}
}

func rt(handle string) nitter.Tweet {
	return nitter.Tweet{ID: "1", IsRetweet: true, Author: nitter.Author{Handle: handle}}
}

func own(handle string) nitter.Tweet {
	return nitter.Tweet{ID: "2", IsRetweet: false, Author: nitter.Author{Handle: handle}}
}

func handles(cs []candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Handle)
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAggregateCandidatesRanking: co-occurrence (following + retweet = 2) ranks
// above single-source candidates; ties break by followers desc, then handle asc.
func TestAggregateCandidatesRanking(t *testing.T) {
	following := []nitter.Profile{
		prof("same", 100, "both-sources"),
		prof("onlyfollow", 999, "follow-only"),
	}
	tweets := []nitter.Tweet{rt("same"), rt("onlyrtlow"), rt("onlyrthigh")}
	got := aggregateCandidates("seed", following, tweets)
	// same: score 2. onlyfollow: score 1, 999 followers. onlyrt*: score 1,
	// 0 followers (unknown) -> tie broken by handle asc.
	want := []string{"same", "onlyfollow", "onlyrthigh", "onlyrtlow"}
	if !equalSlices(handles(got), want) {
		t.Fatalf("order = %v, want %v", handles(got), want)
	}
	if got[0].score() != 2 || got[0].source() != "both" {
		t.Errorf("top = score %d source %q, want 2/both", got[0].score(), got[0].source())
	}
	if got[1].source() != "following" || got[3].source() != "retweet" {
		t.Errorf("sources = %q/%q, want following/retweet", got[1].source(), got[3].source())
	}
}

// TestAggregateCandidatesSeedAndDedup: the seed never appears (case-insensitive),
// a handle seen in both sources merges to one row, and non-retweets contribute
// no candidate.
func TestAggregateCandidatesSeedAndDedup(t *testing.T) {
	following := []nitter.Profile{prof("seed", 10, "x"), prof("dup", 20, "d")}
	tweets := []nitter.Tweet{
		own("seed"),    // own tweet -> ignored
		rt("Seed"),     // seed retweeted -> excluded (case-insensitive)
		rt("dup"),      // merge with following row
		rt("dup"),      // duplicate -> single row
		rt("nobody"),   // retweet-only
		own("someone"), // own tweet by another -> ignored
	}
	got := aggregateCandidates("seed", following, tweets)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (%v)", len(got), handles(got))
	}
	for _, c := range got {
		if strings.EqualFold(c.Handle, "seed") {
			t.Fatalf("seed leaked into candidates: %v", handles(got))
		}
	}
	if got[0].Handle != "dup" || got[0].score() != 2 || got[0].FollowersCount != 20 {
		t.Errorf("dup = %+v, want merged score 2 with following data", got[0])
	}
	if got[1].Handle != "nobody" || !needsEnrichment(got[1]) {
		t.Errorf("nobody = %+v, want retweet-only needing enrichment", got[1])
	}
	if needsEnrichment(got[0]) {
		t.Errorf("dup needs enrichment, want false (came from following)")
	}
}

// TestAggregateCandidatesDeterministic: identical input yields identical order.
func TestAggregateCandidatesDeterministic(t *testing.T) {
	following := []nitter.Profile{prof("bb", 5, ""), prof("aa", 5, ""), prof("cc", 5, "")}
	tweets := []nitter.Tweet{rt("zz"), rt("aa")}
	first := handles(aggregateCandidates("seed", following, tweets))
	for i := 0; i < 5; i++ {
		if again := handles(aggregateCandidates("seed", following, tweets)); !equalSlices(again, first) {
			t.Fatalf("run %d differs: %v vs %v", i, first, again)
		}
	}
	want := []string{"aa", "bb", "cc", "zz"}
	if !equalSlices(first, want) {
		t.Errorf("order = %v, want %v", first, want)
	}
}

// TestShortBio: multi-line/tab bio collapses to one line; long bio truncates
// on a rune boundary and stays valid UTF-8.
func TestShortBio(t *testing.T) {
	if got := shortBio("line1\nline2\ttabbed"); got != "line1 line2 tabbed" {
		t.Errorf("collapse = %q, want %q", got, "line1 line2 tabbed")
	}
	if got := shortBio("  a \r\n\r\n  b  "); got != "a b" {
		t.Errorf("collapse = %q, want %q", got, "a b")
	}
	got := shortBio(strings.Repeat("画", 200))
	if len(got) > 120 {
		t.Errorf("len = %d bytes, want <= 120", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated bio is not valid UTF-8")
	}
}
