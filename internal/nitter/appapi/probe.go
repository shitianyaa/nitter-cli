package appapi

// Instance capability probe: the implementation behind `twitter instances
// test`. TestInstance drives one Nitter instance through the capabilities a
// fetch needs — the RSS feed, the user HTML timeline, search and a list —
// and reports each independently, so a diagnostic answers "what works on
// this instance" rather than failing as a whole.
//
// Probe criteria (frozen here so command output stays stable):
//   - RSS: HTTP 200 AND the body parses as XML whose root is <rss> with a
//     <channel> element. encoding/xml with a minimal struct is used instead
//     of string markers: it rejects non-XML 200 responses (error pages served
//     with status 200) and validates well-formedness, while staying cheap —
//     items are NOT required (an account with zero posts still has a valid
//     feed).
//   - user HTML / search / list: HTTP 200 AND the body contains the Nitter
//     timeline markers "timeline-item" or "timeline-end". The end marker is
//     rendered even for an empty timeline, so an empty account is a working
//     capability, not a failure.
//
// Failure recording: per-probe failures live in the returned report, never
// in the error. A probe that got an HTTP status records that status with an
// empty Err (the status is the reason); a probe that failed on content
// records the status plus a short reason ("not rss", "no timeline",
// "redirect"); a probe that failed at transport level records status 0 and a
// short, redacted reason ("timeout", "canceled", "unreachable", …) derived
// from the classified transport error. No URL, query string, header or body
// content ever reaches Probe.Err (sdk redaction contract).
//
// Unauthenticated: instance credentials (Instance.Username/Password) are NOT
// wired to the transport in the MVP — probes run anonymously like every
// other fetch; wiring basic auth is deferred until a real instance needs it.
//
// TestInstance does not touch the Client's Chooser: probes address explicit
// URLs (they are diagnostics, possibly for instances that are not even
// configured) and must not perturb rotation state.

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/shitianyaa/twitter-cli/sdk"
)

// opTestInstance is the Op stamped on TestInstance's own errors.
const opTestInstance = "appapi.TestInstance"

// timelineItem and timelineEnd are the Nitter HTML markers of a timeline
// render (present even when the timeline is empty).
const (
	timelineItem = "timeline-item"
	timelineEnd  = "timeline-end"
)

// TestOptions selects which probes run and against what.
type TestOptions struct {
	// User is the account used for the RSS and user-HTML probes
	// (<base>/<User>/rss and <base>/<User>); it must not be empty. The CLI
	// defaults it to a stable public account.
	User string
	// IncludeSearch enables the search probe
	// (<base>/search?f=tweets&q=twitter).
	IncludeSearch bool
	// ListID enables the list probe (<base>/i/lists/<ListID>) when
	// non-empty.
	ListID string
}

// TestInstance probes one Nitter instance at baseURL and returns the
// per-capability report. The returned error is reserved for invalid input
// (bad baseURL, empty user — KindInvalidArg) and a locally unusable client
// (no transport wired — KindLocalState); every probe outcome, including
// transport failures, is recorded in the report.
//
// baseURL is trimmed of surrounding whitespace and trailing slashes and must
// carry an http or https scheme (and a host). The report's URL is the
// normalized form. Latency is the round trip of the RSS probe (including
// body read), measured with the client clock — a fake clock yields 0.
func (c *Client) TestInstance(ctx context.Context, baseURL string, opts TestOptions) (twitter.InstanceReport, error) {
	base, err := normalizeBaseURL(baseURL)
	if err != nil {
		return twitter.InstanceReport{}, err
	}
	if opts.User == "" {
		return twitter.InstanceReport{}, twitter.Errorf(twitter.KindInvalidArg, opTestInstance, "probe user must not be empty")
	}
	if c.HTTP == nil {
		return twitter.InstanceReport{}, twitter.Errorf(twitter.KindLocalState, opTestInstance, "no transport wired into the appapi client")
	}

	report := twitter.InstanceReport{URL: base}
	report.RSS, report.Latency = c.probeRSS(ctx, base, opts.User)
	report.UserHTML = c.probeHTML(ctx, base, "/"+url.PathEscape(opts.User))
	if opts.IncludeSearch {
		report.Search = c.probeHTML(ctx, base, "/search?f=tweets&q=twitter")
	}
	if opts.ListID != "" {
		report.List = c.probeHTML(ctx, base, "/i/lists/"+url.PathEscape(opts.ListID))
	}
	return report, nil
}

// normalizeBaseURL validates and normalizes an instance base URL: trimmed,
// absolute, http/https only, trailing slashes removed. Error messages never
// echo the input (redaction contract — a URL may carry credentials or query
// strings).
func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	switch {
	case err != nil, u == nil, u.Scheme == "", u.Host == "":
		return "", twitter.Errorf(twitter.KindInvalidArg, opTestInstance, "instance URL must be an absolute http or https URL")
	case u.Scheme != "http" && u.Scheme != "https":
		return "", twitter.Errorf(twitter.KindInvalidArg, opTestInstance, "instance URL scheme must be http or https")
	}
	return strings.TrimRight(raw, "/"), nil
}

// probeRSS runs the RSS probe and measures its latency.
func (c *Client) probeRSS(ctx context.Context, base, user string) (twitter.Probe, time.Duration) {
	start := c.nowFunc()
	body, status, err := c.HTTP.Get(ctx, base+"/"+url.PathEscape(user)+"/rss", nil)
	latency := c.nowFunc().Sub(start)
	if err != nil {
		return transportProbe(err), latency
	}
	return contentProbe(status, isRSSFeed(body), "not rss"), latency
}

// probeHTML runs one of the HTML probes (user timeline, search, list) against
// base+path.
func (c *Client) probeHTML(ctx context.Context, base, path string) twitter.Probe {
	body, status, err := c.HTTP.Get(ctx, base+path, nil)
	if err != nil {
		return transportProbe(err)
	}
	hasTimeline := bytes.Contains(body, []byte(timelineItem)) || bytes.Contains(body, []byte(timelineEnd))
	return contentProbe(status, hasTimeline, "no timeline")
}

// contentProbe turns a completed request into a probe result: 3xx is its own
// reason (httpx does not follow redirects — the base URL is likely
// misconfigured); 2xx fails on content with the caller's short reason when
// the criterion does not hold.
func contentProbe(status int, ok bool, contentErr string) twitter.Probe {
	switch {
	case status >= 300:
		return twitter.Probe{Status: status, Err: "redirect"}
	case !ok:
		return twitter.Probe{Status: status, Err: contentErr}
	default:
		return twitter.Probe{OK: true, Status: status}
	}
}

// transportProbe reduces a classified transport error to the failure shape of
// a probe that produced no usable HTTP status: status 0 plus a short,
// redacted reason. HTTP-status reasons stay recoverable from the kinds that
// carry an unambiguous status (404, 429); everything else degrades to stable
// tokens — never the wrapped transport text, which can embed instance host
// details.
func transportProbe(err error) twitter.Probe {
	switch {
	case errors.Is(err, context.Canceled):
		return twitter.Probe{Err: "canceled"}
	case errors.Is(err, context.DeadlineExceeded):
		return twitter.Probe{Err: "timeout"}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return twitter.Probe{Err: "timeout"}
	}
	var terr *twitter.Error
	if !errors.As(err, &terr) {
		return twitter.Probe{Err: "unavailable"}
	}
	switch terr.Kind {
	case twitter.KindNotFound:
		return twitter.Probe{Status: 404}
	case twitter.KindRateLimited:
		return twitter.Probe{Status: 429}
	case twitter.KindChallenge:
		// 401 and 403 are not distinguishable through the classified error.
		return twitter.Probe{Err: "http 401/403"}
	case twitter.KindUnavailable:
		// Network failure, other 4xx, or a 5xx after retries — the probe
		// cannot tell them apart without the response.
		return twitter.Probe{Err: "unreachable"}
	case twitter.KindMalformed:
		return twitter.Probe{Err: "malformed"}
	case twitter.KindInvalidArg:
		return twitter.Probe{Err: "invalid request"}
	default:
		return twitter.Probe{Err: "unavailable"}
	}
}

// rssFeed is the minimal RSS shape the probe validates: the root element must
// be <rss> (any namespace) and a <channel> child must be present. The channel
// pointer distinguishes "absent" from "present" — decoding into a value field
// cannot.
type rssFeed struct {
	XMLName xml.Name  `xml:"rss"`
	Channel *struct{} `xml:"channel"`
}

// isRSSFeed reports whether body decodes as an RSS document with a channel.
func isRSSFeed(body []byte) bool {
	var feed rssFeed
	return xml.Unmarshal(body, &feed) == nil && feed.Channel != nil
}

// nowFunc returns the client clock, defaulting to time.Now for struct
// literals that did not set Now (newClient always does).
func (c *Client) nowFunc() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
