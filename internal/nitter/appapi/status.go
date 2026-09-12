package appapi

// Single-status acquisition: the implementation behind `twitter get`.
//
// Ref contract (ParseStatusRef, pure and network-free): a bare numeric
// status ID, an https://(x|twitter).com/<user>/status/<id> URL (optional
// /photo/N or /video/1 suffix), or a nitter-style URL of the same path
// shape — where the user segment is optional (stock Nitter also serves the
// user-less /status/<id> route). A scheme-less ref whose first path segment
// looks like a host ("nitter.example/…", "x.com/…") is normalized onto
// https. Everything else is KindInvalidArg.
//
// Fetch strategy: with a known user the page <base>/<user>/status/<id> is
// tried first; on HTTP 404 or an unidentifiable/empty page (or a page whose
// main status is not the requested one) the user-less <base>/status/<id>
// route is retried — Nitter serves both. With an unknown user the user-less
// route is used directly. Any other classification (challenge, 5xx, …)
// fails the attempt instead of falling through.
//
// Instance rotation is identical to the other fetches: the Chooser walks
// the configured instances, an instance whose attempt fails is marked
// failed (cooldown) while the next one is tried, a success is marked
// successful, and context cancellation aborts rotation with the context
// error. The method returns the base URL of the instance that produced the
// tweet so callers can stamp NDJSON provenance (the same deviation
// Timeline established: provenance is only known here).

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/shitianyaa/twitter-cli/internal/nitter/html"
	"github.com/shitianyaa/twitter-cli/sdk"
)

const (
	opStatus         = "appapi.Status"
	opParseStatusRef = "appapi.ParseStatusRef"
)

// statusIDRe is the numeric status-ID shape (bare IDs and URL segments).
var statusIDRe = regexp.MustCompile(`^\d+$`)

// statusPathSegRe matches the status path segment of both the modern
// /status/ and the legacy /statuses/ URL forms.
func isStatusSegment(seg string) bool {
	return strings.EqualFold(seg, "status") || strings.EqualFold(seg, "statuses")
}

// ParseStatusRef parses one status reference into (id, user). user is ""
// for a bare ID or a user-less URL — the caller then fetches the user-less
// route. Errors are KindInvalidArg; parsing never touches the network.
func ParseStatusRef(s string) (id, user string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", twitter.Errorf(twitter.KindInvalidArg, opParseStatusRef, "status reference must not be empty")
	}
	if statusIDRe.MatchString(s) {
		return s, "", nil
	}

	// URL shapes. A scheme-less ref whose first path segment looks like a
	// host (contains "." or ":") gets an https scheme prepended so
	// url.Parse can separate host from path; path-only refs stay hostless
	// and are rejected below (ambiguous).
	if !strings.Contains(s, "://") {
		head := s
		if i := strings.IndexAny(head, "/?#"); i >= 0 {
			head = head[:i]
		}
		if head != "" && strings.ContainsAny(head, ".:") {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", "", twitter.Errorf(twitter.KindInvalidArg, opParseStatusRef,
			"not a status reference: use a numeric ID or a <user>/status/<id> URL")
	}
	var segs []string
	for _, seg := range strings.Split(u.Path, "/") {
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	si := -1
	for i := len(segs) - 1; i >= 0; i-- {
		if isStatusSegment(segs[i]) {
			si = i
			break
		}
	}
	if si < 0 {
		return "", "", twitter.Errorf(twitter.KindInvalidArg, opParseStatusRef,
			"URL carries no /status/<id> path")
	}
	// At most ONE segment may sit between the host and the status segment;
	// more (x.com/i/web/status/…) is a shape this parser does not claim.
	if si > 1 {
		return "", "", twitter.Errorf(twitter.KindInvalidArg, opParseStatusRef,
			"unsupported status URL shape")
	}
	user = ""
	if si == 1 {
		user = segs[0]
		if !handleRe.MatchString(user) {
			return "", "", twitter.Errorf(twitter.KindInvalidArg, opParseStatusRef,
				"URL user segment is not a valid handle")
		}
	}
	if si+1 >= len(segs) || !statusIDRe.MatchString(segs[si+1]) {
		return "", "", twitter.Errorf(twitter.KindInvalidArg, opParseStatusRef,
			"URL carries no numeric status ID after /status/")
	}
	id = segs[si+1]
	// Trailing segments: none, or one of the media-anchor suffixes
	// (/photo/N, /video/1) X attaches to status permalinks.
	rest := segs[si+2:]
	switch {
	case len(rest) == 0:
	case len(rest) == 2 && (rest[0] == "photo" || rest[0] == "video") && statusIDRe.MatchString(rest[1]):
	default:
		return "", "", twitter.Errorf(twitter.KindInvalidArg, opParseStatusRef,
			"unsupported suffix after the status ID")
	}
	return id, user, nil
}

// Status fetches one single status. The ref is parsed BEFORE any rotation
// or network (KindInvalidArg otherwise). The returned string is the base
// URL of the instance that produced the tweet ("" only on error).
func (c *Client) Status(ctx context.Context, ref string) (twitter.Tweet, string, error) {
	id, user, err := ParseStatusRef(ref)
	if err != nil {
		return twitter.Tweet{}, "", err
	}
	if c.HTTP == nil {
		return twitter.Tweet{}, "", twitter.Errorf(twitter.KindLocalState, opStatus, "no transport wired into the appapi client")
	}
	if c.Chooser == nil {
		return twitter.Tweet{}, "", twitter.Errorf(twitter.KindLocalState, opStatus, "no instance chooser wired into the appapi client")
	}

	tried := make(map[string]bool)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return twitter.Tweet{}, "", err
		}
		base, markSuccess, markFailure, err := c.Chooser.Pick()
		if err != nil {
			// Nothing configured, or every instance is cooling down. If we
			// already attempted someone, their failure is the better report;
			// otherwise pass the chooser's answer through.
			if lastErr != nil {
				return twitter.Tweet{}, "", lastErr
			}
			return twitter.Tweet{}, "", err
		}
		if tried[base] {
			// Every configured instance has been attempted (a disabled
			// cooldown makes Pick repeat). A previous attempt must have
			// failed for us to still be looping; the guard keeps the
			// contract exact even if that invariant ever breaks.
			if lastErr != nil {
				return twitter.Tweet{}, "", lastErr
			}
			return twitter.Tweet{}, "", twitter.Errorf(twitter.KindUnavailable, opStatus, "all instances failed")
		}
		tried[base] = true
		tw, err := c.statusFromInstance(ctx, base, id, user)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// The caller gave up: abort instead of rotating on.
				return twitter.Tweet{}, "", err
			}
			markFailure()
			lastErr = err
			continue
		}
		markSuccess()
		return tw, base, nil
	}
}

// statusFromInstance runs the route strategy against one instance: the user
// route first when the user is known, retrying the user-less route on 404
// or unidentifiable content; the user-less route directly otherwise. The
// parsed page must identify the REQUESTED status — anything else is
// KindMalformed (never a fabricated pick of some other tweet).
func (c *Client) statusFromInstance(ctx context.Context, base, id, user string) (twitter.Tweet, error) {
	if user != "" {
		tw, err := c.fetchStatusPage(ctx, base, "/"+user+"/status/"+id)
		if err == nil && tw.ID == id {
			return tw, nil
		}
		if err == nil {
			// The page parsed but carries a different main status: same
			// retry class as an empty page.
			err = twitter.Errorf(twitter.KindMalformed, opStatus,
				"status page did not contain the requested status")
		}
		var terr *twitter.Error
		if !errors.As(err, &terr) || (terr.Kind != twitter.KindNotFound && terr.Kind != twitter.KindMalformed) {
			return twitter.Tweet{}, err
		}
		// Fall through to the user-less route.
	}
	tw, err := c.fetchStatusPage(ctx, base, "/status/"+id)
	if err != nil {
		return twitter.Tweet{}, err
	}
	if tw.ID != id {
		return twitter.Tweet{}, twitter.Errorf(twitter.KindMalformed, opStatus,
			"status page did not contain the requested status")
	}
	return tw, nil
}

// fetchStatusPage fetches, classifies and parses one status page.
func (c *Client) fetchStatusPage(ctx context.Context, base, path string) (twitter.Tweet, error) {
	body, _, err := c.HTTP.Get(ctx, base+path, nil)
	if err != nil {
		return twitter.Tweet{}, err
	}
	if err := html.ClassifyPage(body); err != nil {
		return twitter.Tweet{}, err
	}
	return html.ParseStatus(body, base)
}
