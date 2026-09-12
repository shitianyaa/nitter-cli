// Data model of the public SDK: the structs below are the NDJSON data
// contract. Their JSON keys are frozen — fields may only be added
// additively, never removed, renamed or repurposed. Every field marshals
// unconditionally (no omitempty) so consumers can rely on key presence in
// every line; empty values render as zero JSON values.
//
// The one exception is the media-resolution pair below (MediaVariant,
// MediaResolution): their sparse optional fields use omitempty, so key
// presence is guaranteed only for MediaResolution's ref/source/kind/url.
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

// MediaVariant is one downloadable encoding of a video or GIF media entry.
// Bitrate is the upstream-reported rate in bits per second (0 when the
// source does not carry one, e.g. HLS playlists or Twitter GIF mp4s);
// ContentType is the MIME type ("video/mp4", "application/x-mpegURL") when
// the source carries one.
type MediaVariant struct {
	URL         string `json:"url"`
	Bitrate     int64  `json:"bitrate"`
	ContentType string `json:"content_type,omitempty"`
}

// MediaResolution is one resolved, directly downloadable media entry of a
// status, produced by the media command's resolution strategies (fxtwitter,
// vxtwitter, syndication, nitter, xdown). It extends the NDJSON data
// contract additively; unlike Tweet, its sparse fields are omitted (see the
// package comment), so only Ref, Source, Kind and URL are guaranteed keys.
type MediaResolution struct {
	// Ref is the canonical https://x.com/<user>/status/<id> permalink of the
	// status the entry belongs to (the /i/status/<id> form when the caller's
	// reference carried no user segment).
	Ref string `json:"ref"`
	// Source names the strategy that produced the entry: "fx", "vx",
	// "syndication", "nitter" or "xdown".
	Source string `json:"source"`
	// Kind is one of "image", "video" or "gif".
	Kind string `json:"kind"`
	// URL is the direct https download link. Plain-http upstream links are
	// dropped by contract, never projected.
	URL string `json:"url"`
	// FallbackURL is an alternative link for the same media when the source
	// carries one: the highest-quality variant link when the quality setting
	// reduced a video's main URL below it, or (for xdown) the proxy link
	// standing in for a direct one.
	FallbackURL string `json:"fallback_url,omitempty"`
	// Label is the source's own label for the entry (e.g. an xdown download
	// button caption); empty when the source has none.
	Label string `json:"label,omitempty"`
	// Width and Height are the pixel dimensions when the source carries
	// them.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// DurationSeconds is the video duration in seconds; set from a source
	// that carries one (the xdown token payload or button label) or by the
	// media command's --probe pass.
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	// SizeBytes is the content length in bytes; set only by the media
	// command's --probe pass.
	SizeBytes int64 `json:"size_bytes,omitempty"`
	// Variants lists every downloadable encoding the source offered for a
	// video or GIF entry, in upstream order; empty for images.
	Variants []MediaVariant `json:"variants,omitempty"`
}
