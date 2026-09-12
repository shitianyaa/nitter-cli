// Package tweetfilter implements the emission-side field filters shared by
// the data commands: dropping pure retweets (--no-reposts), tweets without
// media (--media-only) and narrowing to one media kind (--media-type).
//
// The package is pure: it performs no IO, owns no clock and knows nothing
// about cobra. Flag registration and usage-error plumbing live in the
// command packages (per-command flag definitions, mirroring the template
// CLIs); this package only classifies and filters tweet slices.
package tweetfilter

import (
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
	twitter "github.com/shitianyaa/twitter-cli/sdk"
)

// mediaTypes are the exact --media-type values. The models contract defines
// exactly these three kinds; matching is case-sensitive (an unknown or
// differently-cased value is a usage error).
var mediaTypes = map[string]bool{"image": true, "video": true, "gif": true}

// Filters carries the emission-side field filters shared by the data
// commands. The zero value filters nothing.
type Filters struct {
	// NoReposts drops pure retweets (IsRetweet true). Note the repost
	// marker is only carried by the HTML parse path (twitter.Tweet.IsRetweet).
	NoReposts bool
	// MediaOnly drops tweets without any media entries.
	MediaOnly bool
	// MediaType keeps only tweets carrying at least one media entry of this
	// type: "" (unset) | image | video | gif.
	MediaType string
}

// Validate checks the flag combination: a MediaType outside the exact
// image|video|gif set is a *invocation.UsageError (exit 2). Everything else
// is valid — the three filters are freely combinable.
func (f Filters) Validate() error {
	if f.MediaType == "" || mediaTypes[f.MediaType] {
		return nil
	}
	return invocation.Usagef("--media-type must be image, video or gif (got %q)", f.MediaType)
}

// Apply returns the filtered slice: order preserved, input untouched, no
// mutation. With no filter set the input slice comes back as-is (a nil
// input stays nil). Filtering runs before output in the one-shot commands
// and before selection/dedup in watch — callers own the placement.
func Apply(ts []twitter.Tweet, f Filters) []twitter.Tweet {
	if !f.NoReposts && !f.MediaOnly && f.MediaType == "" {
		return ts
	}
	out := make([]twitter.Tweet, 0, len(ts))
	for _, tw := range ts {
		if f.keeps(tw) {
			out = append(out, tw)
		}
	}
	return out
}

// keeps reports whether one tweet passes the filter set. --media-type
// subsumes --media-only (a tweet carrying the wanted kind has media), so a
// set MediaType short-circuits the media-only check.
func (f Filters) keeps(tw twitter.Tweet) bool {
	if f.NoReposts && tw.IsRetweet {
		return false
	}
	if f.MediaType != "" {
		return hasMediaType(tw.Media, f.MediaType)
	}
	if f.MediaOnly && len(tw.Media) == 0 {
		return false
	}
	return true
}

// hasMediaType reports whether the media list carries at least one entry of
// the wanted type.
func hasMediaType(ms []twitter.Media, want string) bool {
	for _, m := range ms {
		if m.Type == want {
			return true
		}
	}
	return false
}
