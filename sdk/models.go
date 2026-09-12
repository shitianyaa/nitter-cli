// Data model of the public SDK: the structs below are the NDJSON data
// contract. Their JSON keys are frozen — fields may only be added
// additively, never removed, renamed or repurposed. Every field marshals
// unconditionally (no omitempty) so consumers can rely on key presence in
// every line; empty values render as zero JSON values.
//
// Producers filling these structs must not fabricate data a source does not
// carry: leave fields at their zero value instead (the RSS and HTML parse
// paths project into the same Tweet shape).
package twitter

import "time"

// Tweet is the SDK's outward projection of one status. Fields are the NDJSON
// data contract (additive-only).
type Tweet struct {
	// ID is the pure-numeric X status ID, kept as a string to avoid JS
	// precision loss on 64-bit IDs.
	ID string `json:"id"`
	// URL is the canonical https://x.com/<user>/status/<id> form.
	URL string `json:"url"`
	// Text is the plain-text body: HTML folded (<br> to newline), quote and
	// emoji image nodes removed.
	Text string `json:"text"`
	// Author is the status author.
	Author Author `json:"author"`
	// PublishedAt is the status time in UTC; it marshals as an RFC3339 UTC
	// string. Producers must store UTC.
	PublishedAt time.Time `json:"published_at"`
	// Media lists direct media links: image URLs (pbs.twimg.com, quality
	// rewritten) and video placeholder links. Empty (possibly nil) when the
	// status has no media.
	Media []Media `json:"media"`
	// IsRetweet reports a pure retweet (retweet-header detection, matching
	// the plugin semantics).
	IsRetweet bool `json:"is_retweet"`
	// RepostedBy is the reposter's DISPLAY NAME as rendered in the retweet
	// header ("NASA retweeted"), not a handle; non-empty only when IsRetweet
	// is true.
	RepostedBy string `json:"reposted_by"`
	// ReplyTo is the replied-to handle; empty when not a reply.
	ReplyTo string `json:"reply_to"`
	// Quote is the quoted-status summary; nil when there is none.
	Quote *Quoted `json:"quote"`
}

// Author identifies the account behind a Tweet or Quoted status.
type Author struct {
	Handle    string `json:"handle"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// Media is one media attachment of a Tweet. Type is one of "image",
// "video" or "gif"; Width and Height are 0 when the source does not carry
// them.
type Media struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Quoted is the summary of a quoted status embedded in a Tweet
// (id/url/text/author only — the full body stays behind URL).
type Quoted struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Text   string `json:"text"`
	Author Author `json:"author"`
}

// Probe is one capability check of an InstanceReport: OK for success, or the
// HTTP status and a short redacted error description on failure.
type Probe struct {
	OK     bool   `json:"ok"`
	Status int    `json:"status"`
	Err    string `json:"err"`
}

// InstanceReport is the per-instance, per-capability result of the instances
// test command. Each Probe corresponds to one Nitter capability: RSS feed,
// user HTML timeline, search, and list. Latency is the round trip of the
// first (RSS) probe.
type InstanceReport struct {
	URL      string        `json:"url"`
	RSS      Probe         `json:"rss"`
	UserHTML Probe         `json:"user_html"`
	Search   Probe         `json:"search"`
	List     Probe         `json:"list"`
	Latency  time.Duration `json:"latency"`
}
