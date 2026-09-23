# nitter Go SDK

[Documentation](../index.md) · [简体中文](../zh-CN/sdk.md)

The public SDK is the Go package `github.com/shitianyaa/nitter-cli/sdk`
(package name `nitter`). It is the only public capability surface of this
module: the CLI's own commands consume it, and external Go callers are invited
to do the same. Everything under `internal/` is private implementation detail
and not an integration API.

```bash
go get github.com/shitianyaa/nitter-cli
```

```go
import "github.com/shitianyaa/nitter-cli/sdk"
```

## Stability contract

- **Additive-only.** Exported identifiers, the `Kind` values and the JSON keys
  of every model may only be extended — never removed, renamed or repurposed.
- **No `omitempty` on data models.** Every field of `Tweet`/`Author`/`Media`/
  `Quoted`/`Page`/`Profile`/`Conversation`/`Trend` marshals unconditionally, so
  consumers can rely on key presence in every JSON line; empty values render as
  zero JSON values (`media` is `null` when the source carried no media). The
  media-pipeline types below (`MediaVariant`/`MediaResolution`/`DownloadRecord`)
  are the documented exception: their sparse optional fields use `omitempty`,
  so key presence is guaranteed only for the keys listed with each struct.
- **Producers never fabricate data.** Fields a source does not carry stay at
  their zero value.
- **Redaction contract.** Neither the `*Error` struct nor any wrapped error
  reachable via `Unwrap` may contain credentials, URL query strings, request
  headers or response bodies — only stable kinds, operations and short static
  context.
- The marshaled shapes are pinned by golden-JSON tests in `sdk/models_test.go`
  (and `sdk/page.go` behavior along with it); behavioral contracts of
  `Client`/`Chooser`/`Error` are pinned by the other `sdk/*_test.go` files.

## Client

```go
client, err := nitter.New(
    nitter.WithInstances([]nitter.Instance{
        {URL: "http://nitter.internal:8080"},
    }),
    nitter.WithHTTPClient(transport), // implements Transport
    nitter.WithCooldown(30 * time.Second),
)
```

`New(opts ...Options) (*Client, error)` composes a Client. Options:

| Option | Default | Meaning |
| --- | --- | --- |
| `WithInstances([]Instance)` | empty | The rotation set, copied at call time. Empty is valid: `New` succeeds but every future fetch fails with `KindUnavailable` ("no instances configured"). |
| `WithHTTPClient(Transport)` | none | Injects the transport. Without it, `New` still succeeds and fetches fail until a transport is injected. |
| `WithCooldown(time.Duration)` | `60s` | Instance failure cooldown. Zero or negative means failures are still marked but never block a pick. |

`Options` are plain functions; passing a nil one to `New` is a
`KindInvalidArg` error. The Client is safe for concurrent use provided the
injected Transport is. The pagination cap is 5 pages (internal default; the
fetch methods added in later milestones grow the public surface additively).

### Transport

```go
type Transport interface {
    Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error)
}
```

The SDK's narrow HTTP boundary: exactly what a fetch needs, nothing else. The
real transport is `internal/nitter/protocol/httpx`'s `*httpx.Client`, which
satisfies the interface structurally; the interface lives in the SDK because
the SDK must not import `internal/*` (the dependency direction is frozen:
`httpx` imports the SDK for the shared error kinds). Tests inject fakes.

### Instance (rotation) semantics

`nitter.Instance` is `{URL string; Username, Password string}` — the optional
basic-auth credentials of a Nitter instance. Credentials are carried but never
echoed into errors (redaction contract).

Rotation is strictly ordered by the `Chooser`: instances are picked in config
order; an instance whose request failed (429, network error) enters a cooldown
until `now + cooldown` and is skipped while cooling; a success resets its
cooldown immediately. The cooldown boundary is strict — an instance becomes
pickable again strictly after its cooldown ends. When every instance is
cooling, `Pick` fails with `KindUnavailable` ("all instances cooling down");
with no instances, `KindUnavailable` ("no instances configured"). There is no
success weighting: the config order is the policy.

## Data models

All structs below are the NDJSON data contract; their JSON keys are frozen
(additive-only) and every field marshals unconditionally — the
media-pipeline pair at the end of this section is the documented exception
(its sparse fields use `omitempty`).

### Tweet

```go
type Tweet struct {
    ID          string    // pure-numeric X status ID, as a string (no JS precision loss)
    URL         string    // canonical https://x.com/<user>/status/<id>
    Text        string    // plain text: HTML folded, quote and emoji image nodes removed
    Author      Author
    PublishedAt time.Time // UTC; marshals as RFC3339 UTC ("2026-07-27T09:09:40Z")
    Media       []Media   // possibly nil when the status has no media
    IsRetweet   bool      // pure retweet (retweet-header detection)
    RepostedBy  string    // reposter display name (not a handle); non-empty only when IsRetweet
    ReplyTo     string    // replied-to handle; empty when not a reply
    Quote       *Quoted   // quoted-status summary; nil when there is none
}

type Author struct{ Handle, Name, AvatarURL string }

type Media struct {
    Type         string // "image", "video" or "gif"
    URL          string // direct link as the instance served it
    Width, Height int   // 0 when the source does not carry them
}

type Quoted struct {
    ID, URL, Text string
    Author        Author
}
```

Pinned marshaled shape (from `sdk/models_test.go`):

```json
{"id":"2102761519985332442","url":"https://x.com/NASA/status/2102761519985332442","text":"line one\nline two","author":{"handle":"NASA","name":"NASA","avatar_url":"https://pbs.twimg.com/profile_images/x_normal.jpg"},"published_at":"2026-07-27T09:09:40Z","media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800},{"type":"video","url":"https://video.twimg.com/vid/abc.mp4","width":0,"height":0}],"is_retweet":true,"reposted_by":"nasa","reply_to":"esa","quote":{"id":"123","url":"https://x.com/esa/status/123","text":"quoted text","author":{"handle":"esa","name":"ESA","avatar_url":""}}}
```

The zero value marshals with every key present (`"media":null`,
`"quote":null`, `"published_at":"0001-01-01T00:00:00Z"`).

### Page

```go
type Page[T any] struct {
    Items      []T    `json:"items"`
    NextCursor string `json:"next_cursor"`
}
```

One page of results. `NextCursor` is the opaque cursor for the following page;
empty means no more results. RSS feeds have no cursor: their pages carry an
empty `NextCursor` and a single `Page`.

### InstanceReport (instances test)

```go
type InstanceReport struct {
    URL      string        `json:"url"`
    RSS      Probe         `json:"rss"`
    UserHTML Probe         `json:"user_html"`
    Search   Probe         `json:"search"`
    List     Probe         `json:"list"`
    Latency  time.Duration `json:"latency"` // round trip of the RSS probe
}

type Probe struct {
    OK     bool   `json:"ok"`
    Status int    `json:"status"` // HTTP status on a status failure
    Err    string `json:"err"`    // short redacted reason on content/transport failure
}
```

One instance's per-capability result: RSS feed, user HTML timeline, search,
list. `Latency` marshals as a Go duration string (for example `"212ms"`).

### Profile (following / profile / search --type user)

```go
type Profile struct {
    ID             string // pure-numeric user ID carried as a string
    Handle         string // without the @
    Name           string
    Bio            string
    FollowersCount int
    FollowingCount int
    TweetsCount    int    // 0 when the source does not carry it
    MediaCount     int
    AvatarURL      string
    BannerURL      string
    IsProtected    bool
}
```

A creator's profile card. The same model backs `nitter following`
(`kind: "profile"` envelopes, `id` is the handle), `nitter profile` and
`nitter search --type user`.

### Conversation (comments)

```go
type Conversation struct {
    Status  Tweet   // the root status
    Thread  []Tweet // ancestor chain above it (possibly nil)
    Replies []Tweet // the reply tree below it
    Cursor  string  // pagination cursor for more replies; empty = no more
}
```

The full conversation projection of `nitter comments`: root + ancestors +
replies.

### Trend (trends)

```go
type Trend struct {
    Name          string   // topic name
    Rank          int      // 1-based rank; defensively numbered when the source omits it
    Context       string   // e.g. "Gaming · Trending", "Trending in United States"
    TweetCount    int      // 0 when the source does not carry it
    GroupedTopics []string // grouped topics (possibly nil)
}
```

One real-time trending topic from `nitter trends`; envelopes carry
`kind: "trend"` with the topic name as `id`.

### MediaResolution and DownloadRecord (media / download commands)

The media pipeline extends the data contract additively with two sparse
types: unlike the models above they use `omitempty` on their optional
fields, so key presence is guaranteed only for `MediaResolution`'s
`ref`/`source`/`kind`/`url` and `DownloadRecord`'s
`ref`/`path`/`kind`/`source`/`url`/`bytes`.

```go
type MediaVariant struct {
    URL         string // direct link of one downloadable encoding
    Bitrate     int64  // upstream-reported bits per second (0 when absent)
    ContentType string // "video/mp4", "application/x-mpegURL", … (omitempty)
}

type MediaResolution struct {
    Ref             string         // canonical https://x.com/<user>/status/<id>
    Source          string         // strategy: "fx", "nitter" or "xdown"
    Kind            string         // "image", "video" or "gif"
    URL             string         // direct download link — always https for third-party strategies; plain http only ever from the user's own nitter instance
    FallbackURL     string         // alternative link for the same media (omitempty)
    CoverURL        string         // poster/thumbnail link of a video or GIF (omitempty)
    Label           string         // the source's own label (omitempty)
    Width, Height   int            // pixel dimensions (omitempty)
    DurationSeconds float64        // source-carried, or measured by the media command's --probe (omitempty)
    SizeBytes       int64          // the media command's --probe only (omitempty)
    Variants        []MediaVariant // every encoding the source offered, upstream order (omitempty)
}

type DownloadRecord struct {
    Ref    string // the raw input reference the file belongs to
    Path   string // absolute on-disk path (the download envelope's id)
    Kind   string // "image", "video", "gif" or "cover"
    Source string // strategy that resolved the status
    URL    string // the direct link that was downloaded (empty on a skip row)
    Bytes  int64  // streamed size (a skip row: the existing file's on-disk size)
    SHA256 string // lowercase hex digest of the streamed bytes (omitempty)
}
```

`CoverURL` (added for the download command, additive) carries the
poster/thumbnail of a video or GIF entry — fx's `thumbnail_url`,
the xdown cover-image entry; empty for images
and whenever the source carries no cover. It is an https link or empty (the
no-plain-http rule applies).

`DownloadRecord` is one file `nitter download` wrote to disk (or found
already on disk under `--on-exists skip`). Its one deliberate sparse
omission is `SHA256`: a skip row reports no digest for a file the command
did not download — the omitted `sha256` key is the documented skip marker;
nothing is fabricated.

## Errors

```go
type Kind string

const (
    KindChallenge   Kind = "challenge_required"         // login/maintenance/challenge page
    KindRateLimited Kind = "rate_limited"               // 429; RetryAfter set when present
    KindNotFound    Kind = "not_found"
    KindUnavailable Kind = "upstream_unavailable"       // unreachable/5xx, no instances, all cooling
    KindMalformed   Kind = "malformed_upstream_response"
    KindInvalidArg  Kind = "invalid_argument"
    KindLocalState  Kind = "local_state_error"          // corrupt state file, missing transport, failed local write
)

type Error struct {
    Kind       Kind
    Op         string
    Err        error          // wrapped chain; Unwrap exposes it
    RetryAfter *time.Duration // only for KindRateLimited, from response headers
}
```

- `Error()` renders the stable, redacted message `"<op>: <kind>: <chain>"`,
  omitting empty parts.
- Build errors with `nitter.Errorf(kind, op, format, args...)`; use `%w` in
  the format to attach a cause. Callers classify with `errors.As(*nitter.Error)`
  and switch on `Kind`.
- The `Kind` set is v1-stable: values may only be added, never removed, renamed
  or reused.
