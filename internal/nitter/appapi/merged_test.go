package appapi_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/nitter/appapi"
)

// Batches keep the comma-joined path within the budget and preserve order.
func TestMergedBatchesRespectsThePathBudget(t *testing.T) {
	// 15-char handles: 250/16 ≈ 15 per batch, so 20 handles force a second.
	handles := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		handles = append(handles, strings.Repeat("a", 14)+string(rune('a'+i%26)))
	}
	batches := appapi.MergedBatches(handles)
	if len(batches) < 2 {
		t.Fatalf("batches = %d, want at least 2 for 20 long handles", len(batches))
	}
	var flat []string
	for _, batch := range batches {
		joined := strings.Join(batch, ",")
		if len(joined) > appapi.MergedRSSMaxPath {
			t.Fatalf("batch path %q is %d chars, over the %d budget", joined, len(joined), appapi.MergedRSSMaxPath)
		}
		flat = append(flat, batch...)
	}
	if !slices.Equal(flat, handles) {
		t.Fatalf("flattened batches = %v, want the input order %v", flat, handles)
	}
}

// Empty handles are dropped; a single over-long handle still forms a batch.
func TestMergedBatchesEdgeCases(t *testing.T) {
	got := appapi.MergedBatches([]string{"", "NASA", "  ", "@ESA"})
	if len(got) != 1 || !slices.Equal(got[0], []string{"NASA", "ESA"}) {
		t.Fatalf("batches = %v, want [[NASA ESA]]", got)
	}
	long := strings.Repeat("a", appapi.MergedRSSMaxPath+10)
	if got := appapi.MergedBatches([]string{long}); len(got) != 1 || len(got[0]) != 1 {
		t.Fatalf("batches = %v, want the over-long handle alone in one batch", got)
	}
}

// The merged feed is split per author; items from authors outside the batch
// (the shape a repost of an outside account takes) are dropped.
func TestMergedTimelineSplitsPerAuthorAndDropsOutsiders(t *testing.T) {
	srv, _ := newTimelineFake(t,
		timelineRoute{target: "/NASA,ESA/rss", status: 200, body: rssBodyMixed(
			rssMixedItem("NASA", "NASA", "101"),
			rssMixedItem("ESA", "ESA", "201"),
			rssMixedItem("OUTSIDER", "OUTSIDER", "999"),
		)},
	)
	buckets, _, err := newTimelineClient(t, srv.URL).MergedTimeline(context.Background(),
		[]string{"NASA", "ESA"}, appapi.PageOptions{})
	if err != nil {
		t.Fatalf("MergedTimeline() error = %v", err)
	}
	if got := buckets["nasa"]; len(got) != 1 || got[0].ID != "101" {
		t.Fatalf("nasa bucket = %+v, want one tweet 101", got)
	}
	if got := buckets["esa"]; len(got) != 1 || got[0].ID != "201" {
		t.Fatalf("esa bucket = %+v, want one tweet 201", got)
	}
	if _, ok := buckets["outsider"]; ok {
		t.Fatalf("buckets = %v, want no bucket for the outside author", buckets)
	}
}

// An invalid handle is a usage-classified error before any network call.
func TestMergedTimelineRejectsBadHandles(t *testing.T) {
	srv, rec := newTimelineFake(t)
	_, _, err := newTimelineClient(t, srv.URL).MergedTimeline(context.Background(),
		[]string{"NASA", "bad handle"}, appapi.PageOptions{})
	if err == nil {
		t.Fatal("MergedTimeline() error = nil, want an invalid-argument error")
	}
	if len(rec.requests()) != 0 {
		t.Fatalf("requests = %v, want none before validation fails", rec.requests())
	}
}
