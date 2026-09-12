package media

// The nitter strategy: the user's OWN Nitter instance is the trusted media
// source. The strategy fetches the status page
// (<base>/<user>/status/<id>, or the user-less /status/<id> route when the
// reference carries no user segment), classifies it through the shared page
// gate, and parses it with the shared HTML status parser
// (internal/nitter/html — internal-to-internal reuse, the same way this
// package already reuses the httpx transport). The page's media is projected
// onto MediaResolution:
//
//   - image entries arrive as direct pbs.twimg.com/media links (rewritten to
//     name=orig by the parser) or as instance /pic/ proxy links whose tier is
//     the path segment Nitter rendered; pbs links go through the quality
//     rewrite here, proxy links pass through verbatim (they are already
//     tier-shaped and rewriting would be a no-op).
//   - video entries are the instance's /video/ proxy links, absolutized by
//     the parser. Nitter carries no variant metadata, so the link is kept
//     verbatim as the download URL — no variants, no fallback.
//
// The third-party strategies' plain-https drop rule does NOT apply to this
// strategy: the instance is the user's own trusted host and commonly serves
// plain http on a LAN — exactly like every other Nitter fetch of this CLI.
// Trust follows the config, not the scheme.
//
// An empty NitterBase (no instance configured) fails the strategy with a
// local-state error so the auto chain reports it honestly in its aggregate —
// never silently skipped.

import (
	"context"
	"strings"

	"github.com/shitianyaa/twitter-cli/internal/nitter/html"
	"github.com/shitianyaa/twitter-cli/internal/nitter/mediaurl"
	"github.com/shitianyaa/twitter-cli/sdk"
)

const opNitter = "media.nitter"

// ResolveNitter resolves one status through the user's own Nitter instance
// and returns the page's media with Ref and Source ("nitter") stamped. A
// page that parses but carries no media yields an empty slice with a nil
// error (the next-strategy semantics ResolveStatus applies); transport
// failures and classified page errors return the classified error.
func (r *Resolver) ResolveNitter(ctx context.Context, ref StatusRef, opts Options) ([]twitter.MediaResolution, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.NitterBase), "/")
	if base == "" {
		return nil, twitter.Errorf(twitter.KindLocalState, opNitter, "no nitter instance configured for the nitter strategy")
	}
	path := "/status/" + ref.ID
	if ref.User != "" {
		path = "/" + ref.User + path
	}
	body, _, err := r.get(ctx, base+path, nil)
	if err != nil {
		return nil, err
	}
	// The same page gate the appapi status fetch applies: an instance error
	// panel or login wall is a classified failure, never a parse of its HTML.
	if err := html.ClassifyPage(body); err != nil {
		return nil, err
	}
	tw, err := html.ParseStatus(body, base)
	if err != nil {
		return nil, err
	}
	if tw.ID != ref.ID {
		return nil, twitter.Errorf(twitter.KindMalformed, opNitter, "status page did not contain the requested status")
	}
	quality := normalizeQuality(opts.Quality)
	res := make([]twitter.MediaResolution, 0, len(tw.Media))
	seen := make(map[string]bool, len(tw.Media))
	for _, m := range tw.Media {
		u := m.URL
		if m.Type == kindImage {
			u = mediaurl.RewritePBSTier(u, quality)
		}
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		res = append(res, twitter.MediaResolution{
			Kind:   m.Type,
			URL:    u,
			Width:  m.Width,
			Height: m.Height,
		})
	}
	for i := range res {
		res[i].Ref = ref.String()
		res[i].Source = string(StrategyNitter)
	}
	return res, nil
}
