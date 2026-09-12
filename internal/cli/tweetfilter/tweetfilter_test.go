package tweetfilter_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
	"github.com/shitianyaa/nitter-cli/internal/cli/tweetfilter"
	nitter "github.com/shitianyaa/nitter-cli/sdk"
)

// media builders keep the fixture table readable.
func img() nitter.Media {
	return nitter.Media{Type: "image", URL: "https://pbs.twimg.com/media/x.jpg"}
}
func vid() nitter.Media { return nitter.Media{Type: "video", URL: "https://x.com/i/video/1"} }
func gif() nitter.Media {
	return nitter.Media{Type: "gif", URL: "https://pbs.twimg.com/tweet_video/x"}
}

// tw builds one tweet with the given id, retweet marker and media list.
func tw(id string, isRetweet bool, media ...nitter.Media) nitter.Tweet {
	return nitter.Tweet{ID: id, IsRetweet: isRetweet, Media: media}
}

// ids projects a tweet slice onto its IDs.
func ids(ts []nitter.Tweet) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

// fixture: the whole filter matrix in one timeline-ordered slice.
var fixture = []nitter.Tweet{
	tw("101", false),        // plain, no media
	tw("102", true),         // pure retweet, no media
	tw("103", false, img()), // photo
	tw("104", false, vid()), // video
	tw("105", false, gif()), // gif
	tw("106", true, img()),  // retweet carrying a photo
}

// TestApplyTable pins the filter matrix: each flag alone, every relevant
// combination, order preservation and the empty-input case.
func TestApplyTable(t *testing.T) {
	tests := []struct {
		name string
		f    tweetfilter.Filters
		in   []nitter.Tweet
		want []string
	}{
		{
			name: "zero filters keep everything in order",
			f:    tweetfilter.Filters{},
			in:   fixture,
			want: []string{"101", "102", "103", "104", "105", "106"},
		},
		{
			name: "no-reposts drops pure retweets",
			f:    tweetfilter.Filters{NoReposts: true},
			in:   fixture,
			want: []string{"101", "103", "104", "105"},
		},
		{
			name: "media-only drops tweets without media",
			f:    tweetfilter.Filters{MediaOnly: true},
			in:   fixture,
			want: []string{"103", "104", "105", "106"},
		},
		{
			name: "media-type image keeps only image carriers",
			f:    tweetfilter.Filters{MediaType: "image"},
			in:   fixture,
			want: []string{"103", "106"},
		},
		{
			name: "media-type video keeps only video carriers",
			f:    tweetfilter.Filters{MediaType: "video"},
			in:   fixture,
			want: []string{"104"},
		},
		{
			name: "media-type gif keeps only gif carriers",
			f:    tweetfilter.Filters{MediaType: "gif"},
			in:   fixture,
			want: []string{"105"},
		},
		{
			name: "media-only combined with media-type",
			f:    tweetfilter.Filters{MediaOnly: true, MediaType: "image"},
			in:   fixture,
			want: []string{"103", "106"},
		},
		{
			name: "no-reposts combined with media-only",
			f:    tweetfilter.Filters{NoReposts: true, MediaOnly: true},
			in:   fixture,
			want: []string{"103", "104", "105"},
		},
		{
			name: "no-reposts combined with media-type",
			f:    tweetfilter.Filters{NoReposts: true, MediaType: "image"},
			in:   fixture,
			want: []string{"103"},
		},
		{
			name: "all three flags",
			f:    tweetfilter.Filters{NoReposts: true, MediaOnly: true, MediaType: "video"},
			in:   fixture,
			want: []string{"104"},
		},
		{
			name: "empty input stays empty",
			f:    tweetfilter.Filters{NoReposts: true, MediaOnly: true, MediaType: "image"},
			in:   nil,
			want: []string{},
		},
		{
			name: "no filters on empty input",
			f:    tweetfilter.Filters{},
			in:   []nitter.Tweet{},
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tweetfilter.Apply(tc.in, tc.f)
			if !reflect.DeepEqual(ids(got), tc.want) {
				t.Errorf("Apply ids = %v, want %v", ids(got), tc.want)
			}
		})
	}
}

// TestApplyOrderPreservedOnReversedInput: the filtered slice keeps the input
// order even when it is not the canonical newest-first shape.
func TestApplyOrderPreservedOnReversedInput(t *testing.T) {
	in := []nitter.Tweet{tw("303", false, img()), tw("302", true), tw("301", false, img())}
	got := tweetfilter.Apply(in, tweetfilter.Filters{NoReposts: true})
	if !reflect.DeepEqual(ids(got), []string{"303", "301"}) {
		t.Errorf("Apply ids = %v, want [303 301] (input order)", ids(got))
	}
}

// TestApplyDoesNotMutateInput: the input slice and its tweets are untouched.
func TestApplyDoesNotMutateInput(t *testing.T) {
	in := fixture
	before := append([]nitter.Tweet(nil), in...)
	_ = tweetfilter.Apply(in, tweetfilter.Filters{NoReposts: true, MediaType: "image"})
	if !reflect.DeepEqual(in, before) {
		t.Errorf("Apply mutated the input slice")
	}
}

// TestApplyNoFiltersReturnsInputUnchanged: with no filters set the input
// slice comes back as-is (a nil input stays nil — the --json [] contract and
// the watch empty-fetch skip rely on len, and the nil case on identity).
func TestApplyNoFiltersReturnsInputUnchanged(t *testing.T) {
	if got := tweetfilter.Apply(nil, tweetfilter.Filters{}); got != nil {
		t.Errorf("Apply(nil, zero Filters) = %#v, want nil", got)
	}
	in := fixture
	if got := tweetfilter.Apply(in, tweetfilter.Filters{}); !reflect.DeepEqual(got, in) {
		t.Errorf("Apply(zero Filters) altered the input")
	}
}

// TestValidate pins the --media-type value contract: empty or one of the
// three exact values passes; anything else is a usage error (exit 2).
func TestValidate(t *testing.T) {
	for _, ok := range []string{"", "image", "video", "gif"} {
		if err := (tweetfilter.Filters{MediaType: ok}).Validate(); err != nil {
			t.Errorf("Validate(media-type %q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"photo", "IMAGE", "image "} {
		err := (tweetfilter.Filters{MediaType: bad}).Validate()
		if err == nil {
			t.Errorf("Validate(media-type %q) = nil, want a usage error", bad)
			continue
		}
		var ue *invocation.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("Validate(media-type %q) = %T, want *invocation.UsageError", bad, err)
		}
	}
}
