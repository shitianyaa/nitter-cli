# nitter CLI reference

[Documentation](../index.md) · [简体中文](../zh-CN/cli-reference.md)

This is the public command contract for the `nitter` binary. Run
`nitter <command> --help` before automating a command; the help text of the
installed binary is the source of truth for the exact flags it accepts.

## Global options

Every command accepts these persistent options:

| Option | Meaning |
| --- | --- |
| `--proxy URL` | Proxy for this invocation (`http`, `https`, `socks5`, `socks5h`). Precedence: flag > config `proxy` > environment (`HTTPS_PROXY`/`ALL_PROXY` when the config value is empty). |
| `--instance URL` | Nitter instance URL for this invocation. It **replaces the whole configured instance set** with this single URL. An invalid proxy scheme or instance URL fails as a usage error (exit 2) before anything runs. |

`nitter --version` prints `nitter version <version>`; a bare `nitter` prints
help. An unknown subcommand exits 1 (not 2).

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success — including empty results, a consumer closing the stdout pipe (EPIPE, best-effort detection on Windows), and SIGINT/SIGTERM shutdown of `watch`. |
| `1` | Runtime failure — acquisition failure (every instance failed), `watch --once` with at least one failed source, corrupt state file, config file read/parse failure, unknown subcommand. |
| `2` | Usage error — bad flags/arguments, input-contract violations, `--json` with `--ndjson`, `watch --json` without `--once`, invalid config values. SDK/network errors are never classified as usage errors. |

## Output modes

All data commands resolve their output mode the same way: `--ndjson` or `--json`
wins; without flags a TTY gets the human rendering and a non-TTY stdout (pipe or
redirect) gets NDJSON — the 0.6.0 pipe default (`nitter.pipeline/v1` envelopes,
one line per record). `watch` keeps its text rendering in pipes; use `--ndjson`
for its envelope stream.

- **Human / text**: one row per record; an empty result prints nothing on stdout
  and the `(empty)` hint on **stderr**. Row shape for tweet lists:
  `<ID>\t<YYYY-MM-DD HH:MM>\t@<handle>\t<single-line text>` — date in UTC at
  minute precision, text flattened to one line (tabs become spaces; other control
  characters switch the whole text cell to a quoted rendering).
- **`--json`**: one JSON object when exactly one record, a JSON array otherwise,
  a literal `[]` when empty.
- **`--ndjson`**: one `nitter.pipeline/v1` envelope per record:

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800}],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

`meta` carries provenance: `source` (the command input as a `kind:ref` key),
`instance` (the base URL of the instance that produced the batch) and
`fetched_at` (RFC3339 UTC). Empty `meta` fields are omitted. The `kind` enum is
additive-only in v1; the currently emitted kinds are `tweet` (data commands),
`instance_report` (`instances test --ndjson`), `media` (`media --ndjson`),
`download` (`download --ndjson`) and `error` (per-source fetch failures of
`watch`, per-ref failures of `media` and `download`):

```json
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"chooser: upstream_unavailable: no instances configured"},"meta":{"input":"user:NASA"}}
```

Error envelope `data` is `{command, stage, code, message}`; `code` is the SDK
error kind of the failure (`rate_limited`, `upstream_unavailable`,
`challenge_required`, `not_found`, `malformed_upstream_response`, …), or
`error` when the failure is not a classified SDK error — never parse the
`message` to classify. `meta.input` names the input the error refers to.
Error messages obey the SDK redaction contract: never credentials, URL query
strings, request headers or response bodies.

**Check the exit code before parsing JSON.** `--json`/`--ndjson` only describe
successful output; stderr is never JSON.

## Fetch behavior shared by user / search / list / get

- Instances are tried in config order; an instance whose fetch fails (429,
  network error) cools down (config `instance_cooldown`, default 60s) while the
  next is tried. All instances failing is a runtime failure (exit 1).
- `--limit` caps the number of tweets (`0` = all); when omitted the config
  `default_limit` (default 20) applies. A negative flag value is a usage error.
- `--max-pages` caps pagination; when omitted the config `max_pages` (default 5)
  applies. On `user` an **explicit `--max-pages 0` removes the cap**: the HTML
  fallback follows the load-more cursor chain until upstream exhaustion (the
  context bounds a runaway run); the RSS feed is single-page and unaffected. On
  `search`/`list` `0` keeps meaning "use the default". A negative value is a
  usage error.
- Retries: `retry_attempts` (default 2) extra attempts with linear backoff
  `retry_delay` (default 1s); a 429 with a valid `Retry-After` waits once and
  retries once.

## nitter user

```bash
nitter user <HANDLE> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--with-replies] [--media-type image|video|gif] [--json|--ndjson]
```

Fetches the timeline of `HANDLE` — 1–15 letters, digits or underscores, without
the `@` (bad shape exits 2 before any network). When `fetch_backend=mix` (default)
FxTwitter fast-lane is tried first without credentials, falling back smoothly to
configured Nitter instances on failure or when `--instance` is specified.
`--max-pages 0` removes the pagination cap.

- `--with-replies` includes the user's reply tweets.
- `--no-reposts` drops pure retweets.
- `--media-only` drops tweets carrying no media attachments (uses Fx `/media` endpoint when active).
- `--media-type image|video|gif` keeps only tweets with at least one matching media entry.

## nitter following

```bash
nitter following <HANDLE> [--limit N] [--json|--ndjson]
```

Fetches accounts followed by `HANDLE` via FxTwitter API v2.
- Renders as formatted table on TTY: `@<handle>  <name>  <followers>  <bio>`.
- Emits single-line `nitter.pipeline/v1` (`kind: "profile"`) NDJSON envelopes in pipe mode.
- `--json` outputs the array of `Profile` objects.

## nitter comments

```bash
nitter comments <STATUS_ID_OR_URL> [--sort likes|recency] [--limit N] [--json|--ndjson]
```

Fetches the root tweet, context thread chain, and user replies for a status.
- Essential for extracting author self-replies with hidden download links or reading long multi-part threads.
- `--sort` selects reply ordering (`likes` default or `recency`).

## nitter circle

```bash
nitter circle list [--json]
nitter circle show <NAME> [--json]
nitter circle add <NAME> <HANDLE>
nitter circle run <NAME> [--limit N] [--media-only] [--media-type image|video|gif] [--json|--ndjson]
```

Manages and traverses curated creator circles in `~/.nitter-cli/circles.toml`.
- `list`: lists configured circles with user counts.
- `show`: lists handles in a circle.
- `add`: adds handle to a circle (creates file/circle on demand).
- `run`: traverses and streams latest tweets for all creators in the circle. `--media-type image|video|gif` keeps only tweets carrying at least one media entry of that type (an invalid value is a usage error; the semantics match the `user` command's `--media-type`).

## nitter profile

```bash
nitter profile <HANDLE> [--json|--ndjson]
```

Fetches user profile card and metadata for `HANDLE`.
- Renders formatted user card on TTY: handle, name, bio, follower count, following count, tweet count, media count, avatar, banner, and protected status.
- Emits single-line `nitter.pipeline/v1` (`kind: "profile"`) NDJSON envelope in pipe mode.
- `--json` outputs the Profile JSON object.

## nitter quotes

```bash
nitter quotes <REF> [--limit N] [--media-only] [--no-reposts] [--json|--ndjson]
```

Fetches quote tweets for a tweet status ID or URL.
- Renders tweet rows on TTY: `<ID>  <YYYY-MM-DD HH:MM>  @<handle>  <text>`.
- Emits single-line `nitter.pipeline/v1` (`kind: "tweet"`) NDJSON envelopes in pipe mode with `meta.source` set to `quotes:<id>`.
- `--limit`: caps number of quotes to fetch (default: 20, 0 = all).
- `--media-only`: keeps only quotes carrying media attachments.
- `--no-reposts`: filters out retweets.
- `--json`: outputs JSON document.

## nitter trends

```bash
nitter trends [--limit N] [--json|--ndjson]
```

Fetches real-time trending topics on Twitter/X via FxTwitter.
- Renders formatted table on TTY: `#  TREND TOPIC  CONTEXT  TWEETS`.
- Emits single-line `nitter.pipeline/v1` (`kind: "trend"`) NDJSON envelopes in pipe mode.
- `--limit`: caps number of trends to display (default: 0, 0 = all).
- `--json`: outputs array of `Trend` objects.

## nitter search

```bash
nitter search <QUERY> [--type tweet|user] [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--media-type image|video|gif] [--json|--ndjson]
```

Runs `QUERY` against the configured instances or FxTwitter.
When `--type tweet` (default), the query is passed through to
Nitter unchanged (URL-escaped once by the HTTP layer), so Nitter's own query
syntax applies: a leading `#` searches a hashtag, `from:user` a user's posts,
anything else is a plain phrase search. An empty (whitespace-only) query exits 2.
NDJSON `meta.source` is `search:<query as typed>`.

When `--type user`, searches for user profiles, artists, and creators matching the query:
- Renders user table on TTY: `@<handle>  <name>  <followers>  <bio>`.
- Emits `kind: "profile"` NDJSON envelopes in pipe mode.
- `--json` outputs Profile objects.

The same field filters apply as on `user`: `--no-reposts`, `--media-only`,
`--media-type image|video|gif` (applied after the fetch, before output).

## nitter list

```bash
nitter list <LIST_ID> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--media-type image|video|gif] [--json|--ndjson]
```

Fetches the timeline of list `LIST_ID` — the list's numeric ID (or a ref the
instance accepts), non-empty and free of whitespace, `?`, `#` and `/` (else
exit 2). It is passed through as the `/i/lists/<LIST_ID>` path. An empty result
may mean the list is empty or new and not yet ingested by this instance — the
two are indistinguishable from the outside; neither is an error. NDJSON
`meta.source` is `list:<LIST_ID>`.

The same field filters apply as on `user`: `--no-reposts`, `--media-only`,
`--media-type image|video|gif` (applied after the fetch, before output).

## nitter get

```bash
nitter get <REF> [--json|--ndjson]
```

Fetches one single status. In `mix` (default) and `fx` modes, tries FxTwitter fast-lane first, smoothly falling back to Nitter instances on failure.
`REF` is a bare numeric status ID, or a status URL —
`x.com`, `twitter.com` or any Nitter instance, shape `<user>/status/<id>`; the
user segment is optional (Nitter serves `/status/<id>` directly) and `/photo/N`
and `/video/1` suffixes are accepted. With no argument and a non-TTY stdin the
reference is read from one stdin line; giving it both ways is an ambiguity
error (exit 2). There are no pagination flags. The quoted tweet, when present,
is summarized in the `quote` field (visible in `--json`/`--ndjson`); interaction
counts are not reported — none are fabricated. NDJSON `meta.source` is
`status:<numeric ID>`. When served by FxTwitter, `meta.instance` is recorded as `FxTwitter`.

Example (`--json` prints exactly one object for the single status; shape
illustrative):

```json
{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}
```

## nitter media

```bash
nitter media <REF>... [--strategy auto|fx|nitter|xdown] \
  [--quality high|medium|low] [--probe] [--json|--ndjson]
```

Resolves each status REF into directly downloadable media links — video mp4
variants, original images, GIFs. `REF` takes the same shapes as `nitter get`
(bare numeric ID, or a status URL of x.com, twitter.com or any Nitter
instance; `/photo/N` and `/video/1` suffixes accepted). Multiple REFs run as
a batch; with no argument and a non-TTY stdin the references are read from
stdin (one per non-empty line); giving refs both as arguments and on stdin is
an ambiguity error (exit 2). The download itself is the caller's job — the
command resolves links, it does not fetch media.

**Strategies** (`--strategy`, default `auto`): `auto` tries the chain
fx → nitter → xdown in order and returns the FIRST
strategy that yields media (`source` stamps which one won); an explicit name
runs only that one. A strategy whose payload parses but carries no media is
skipped for the next one; when every strategy comes up empty the status
resolves as a `not_found` error — a status without media is a normal
classified outcome, not a crash.

**Trust boundary**: fx and xdown are **third-party public
services** — resolving a status sends its tweet URL to them, so only resolve
public statuses you are fine sharing; their failures are reported with the
real strategy name and are never silently swapped for another source's
success. `nitter` instead reads the status page from **your own** configured
instance (the first `[[instances]]` entry, or `--instance`); with no instance
configured it fails with a clear local-state error. Nitter-served links are
kept as they are — including plain-http LAN instances — and carry no variant
metadata.

**`--quality high|medium|low`** (default `high`): images go through the pbs
tier rewrite (`name=orig|large|small`); for video/GIF the tier selects WHICH
variant becomes the main URL (high = highest bitrate, medium = upper median
non-zero, low = lowest non-zero) while every variant stays in the `variants`
list. Unlike the reference plugin, the CLI returns ALL media entries of a
status — it does not skip images when a video is present; consumers choose.

**`--probe`** adds one ranged GET per video/gif URL (images are never
probed): the duration comes from the mp4 movie header, the size from the
Content-Range total. Probing is best-effort — any failure leaves the values
empty and never fails the run, and a source-reported duration is never
overwritten. Budget one extra request per video entry.

Human/text output is one tab-separated row per media entry:

```text
https://x.com/NASA/status/2081668333762687236	fx	video	https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4	-	3.2	24000
```

Columns: `ref source kind url label duration size` — ref echoes the input
reference; the trailing cells are `-` when absent. `--json` prints a single
JSON object when exactly one media entry resolved across the whole
invocation, an array otherwise, `[]` when nothing resolved. `--ndjson`
prints one envelope per media entry (`kind` `media`, the download URL as
`id`, the MediaResolution as `data`, `meta.input` = the raw ref) and one
`kind:"error"` envelope per failed ref, in ref order:

```json
{"schema":"nitter.pipeline/v1","kind":"media","id":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","data":{"ref":"https://x.com/NASA/status/2081668333762687236","source":"fx","kind":"video","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","variants":[{"url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","bitrate":2176000}]},"meta":{"input":"https://x.com/NASA/status/2081668333762687236"}}
```

Exit codes: success 0 (probing failures do not count); a per-ref resolution
failure gets an in-place error report (error envelope on the NDJSON stream,
`error: <ref>: <message>` on stderr otherwise) while the other refs continue,
and the run exits 1 with a `media completed with N of M refs failed` summary
when at least one ref failed; usage problems (`--json` with `--ndjson`,
invalid `--strategy` or `--quality`, bad/missing refs, refs given both as
arguments and on stdin) exit 2.

## nitter download

```bash
nitter download <REF>... [--output DIR] [--kind image|video|gif|cover] \
  [--quality high|medium|low] [--strategy auto|fx|nitter|xdown] \
  [--on-exists refuse|skip|overwrite] [--filename-template TEMPLATE] \
  [--json|--ndjson]
```

Resolves each status REF with the media command's strategies and downloads
the planned media files to the output directory. `REF` takes the same shapes
as `nitter get` and `nitter media` (bare numeric ID, or a status URL of
x.com, twitter.com or any Nitter instance; `/photo/N` and `/video/1`
suffixes accepted). Multiple REFs run as a batch; with no argument and a
non-TTY stdin the input is read from stdin — when the first non-whitespace
byte is `{`, every non-empty line must be a strict `nitter.pipeline/v1` tweet
envelope and each record's `data.url` is used as the REF (the
`nitter get --ndjson` and `nitter watch --ndjson` streams feed download
directly; a malformed envelope is a usage error), otherwise every non-empty
line is a plain REF. Giving refs both as arguments and on stdin is an
ambiguity error (exit 2).

**Selection** (`--kind`, default: everything) follows the video-wins rule: a
status carrying video or GIF downloads its ONE best video file — ranked by
bitrate (or xdown's p-numbers; empirically xdown serves several bitrate
entries plus a cover image for one video tweet, which is why the plan
converges) — and the winner keeps the remaining candidates as its fallback
chain: the first URL that fails falls through the fallbacks in order, and
the row reports whichever candidate succeeded. An image-only status
downloads every image (`<id>-1.jpg` ... `<id>-4.jpg`). `--kind
image|video|gif` pre-filters that; `--kind cover` plans exactly the video's
cover image as `<id>-cover.<ext>` — an image-only status has none and
reports `not_found`. A filter matching nothing yields no files, not an error
(a `nothing found for <ref>` line on stderr). Playlists (HLS `.m3u8`, DASH
`.mpd`) are never download candidates.

The file extension comes from the resolved URL path when it carries one,
otherwise from the download response's Content-Type (`image/jpeg`→`.jpg`,
`image/png`→`.png`, `image/webp`→`.webp`, `image/gif`→`.gif`,
`video/mp4`→`.mp4`), falling back to a kind-based default (`.jpg` for images
and covers, `.mp4` for videos/GIFs).

**Naming templates**: the `filename_template` config key (default
`{id}-{seq}`, i.e. `<id>-<seq>.<ext>`) renders every regular file's name;
`--filename-template TEMPLATE` overrides it per invocation (flag > config).
Placeholders: `{id}` the status id, `{seq}` the file's 1-based position in
the plan, `{user}` the ref's user segment as given (empty for a bare ID; the
user-less `/i/status/<id>` route reports `i`), `{kind}`
image/video/gif/cover, `{ext}` the planned extension with its leading dot —
empty when the plan carries none, in which case the response's Content-Type
decides at download time and the extension is appended after the final
rendered name exactly as without a template; a template without `{ext}`
gets the extension appended at the end. Covers ignore the filename template:
they always land as `<id>-cover.<ext>`. The `directory_template` config key
(no flag; default empty = flat) places the files in subdirectories of the
output directory, rendered per file from `{id}`/`{user}`/`{kind}` — `{seq}`
and `{ext}` are not allowed there, `/` separates levels, empty levels are
skipped. Rendered names are sanitized (Windows-illegal characters `\ / : *
? " < > |` and control characters become `_`; `.` and `..` directory levels
are rejected). An invalid template — an unknown or malformed placeholder, a
forbidden placeholder in the directory position, a path separator in the
filename position — never fails the run: one stderr warning line names the
template and the default (filenames) or flat (directory) behavior applies
instead. Two planned files of one ref rendering the same name collide: the
later one gets a `-2`, `-3`, ... suffix before the extension plus a warning;
across refs the `--on-exists` semantics apply unchanged. An empty template
value means the default.

**Strategies and trust boundary** (`--strategy`, default `auto`): the media
command's chain — `auto` tries fx → nitter → xdown and
the first strategy that yields media wins (`source` stamps which one); an
explicit name runs only that one. For download the boundary is stricter than
for `media`, because the fetch happens too: fx and xdown
are **third-party public services** — resolving AND downloading sends the
tweet URL through them, so only use them for public statuses you are fine
sharing. `--strategy nitter` is the fully private path: resolution and
download both stay on **your own** configured instance (the first
`[[instances]]` entry, or `--instance`); its direct links may be plain-http
links served by your own instance and are trusted for that. Download
requests ride the configured proxy (`--proxy` / config `proxy`) like every
other fetch of this CLI.

The output directory is `--output DIR`, else the `download_path` config key
(default `./nitter-media`; relative paths resolve against the working
directory); it is created on demand (`mkdir -p`).

**`--on-exists`** (default `refuse`) decides what happens when a target file
is already on disk: `refuse` reports the entry as an error and the batch
continues; `skip` keeps the existing file, reports its row with the file's
actual on-disk size and NO sha256 (nothing was re-downloaded, nothing is
fabricated) and is never counted as a failure; `overwrite` re-downloads
through the same atomic temp-then-rename flow. Duplicate refs in one batch
meet the same filenames: under `refuse` the second occurrence fails with the
exists error while the batch continues.

Human/text output is one tab-separated row per downloaded file:

```text
https://x.com/NASA/status/2081668333762687236	/home/you/nitter-media/2081668333762687236-1.mp4	24000000	video	fx
```

Columns: `ref path bytes kind source` — `path` is the absolute file path
(also the NDJSON envelope's `id`); under `--on-exists skip` the path cell
reads `<path> (skipped)`. `--json` prints the downloaded files as one JSON
document (a single object when exactly one file, an array otherwise, `[]`
when none). `--ndjson` prints one envelope per downloaded file (`kind`
`download`, the absolute path as `id`, the DownloadRecord as `data`,
`meta.input` = the raw ref) and one `kind:"error"` envelope per failed ref
(`data.command` is `download`, `data.stage` is `resolve`, `plan` or
`download`), in ref order:

```json
{"schema":"nitter.pipeline/v1","kind":"download","id":"/home/you/nitter-media/2081668333762687236-1.mp4","data":{"ref":"https://x.com/NASA/status/2081668333762687236","path":"/home/you/nitter-media/2081668333762687236-1.mp4","kind":"video","source":"fx","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","bytes":24000000,"sha256":"…"},"meta":{"input":"https://x.com/NASA/status/2081668333762687236"}}
```

Exit codes: success 0 (an invocation that plans and downloads nothing prints
`(empty)` on stderr; a consumer closing the stdout pipe early is a clean
stop); a per-ref failure gets an in-place error report (error envelope on
the NDJSON stream, `error: <ref>: <message>` on stderr otherwise) while the
other refs continue, and the run exits 1 with a
`download completed with N of M refs failed` summary when at least one ref
failed; usage problems (unknown `--kind`/`--quality`/`--strategy`/
`--on-exists`, bad or missing refs, refs given both as arguments and on
stdin, malformed stdin envelopes, `--json` with `--ndjson`) exit 2.

## nitter instances test

```bash
nitter instances test [URL] [--full] [--list-id ID] [--user HANDLE] [--json|--ndjson]
```

Probes instance capabilities and prints one line per instance:

```text
url	rss	user_html	search	list	latency
http://nitter.internal:8080	ok	ok	fail(404)	-	212ms
```

Cells are `ok`, `fail(<reason>)` (the HTTP status, or a short reason such as
`timeout` or `not rss`), or `-` for probes that did not run. Latency is the
round trip of the RSS probe, rendered at human precision.

- Without a URL, every instance from `[[instances]]` is probed in order; with a
  URL, only that one. With no instances configured and no URL: exit 2.
- The RSS and user probes fetch `<user>/rss` and `<user>`; `--user` overrides
  the account (default `NASA`). `--full` adds the search probe; `--list-id ID`
  adds the list probe (`/i/lists/<id>`; must be non-empty when given). Both
  default off — each extra probe costs the instance a request.
- Probes use the same transport and settings (retry, pacing, proxy) as real
  fetches.
- **Exit status is 0 whenever the probes completed, even if they all failed** —
  the report is the product. Exit 2 marks invalid input (empty `--list-id`,
  `--json --ndjson`, invalid URL/proxy); exit 1 wiring/transport build failures.
- `--json` prints one JSON object for a single instance, an array for several.
  `--ndjson` prints one envelope per instance (`kind` `instance_report`, the
  instance URL as `id`, the report as `data`, no `meta`).

## nitter config

```bash
nitter config path
nitter config get [KEY]
nitter config set KEY [VALUE]
nitter config unset KEY
```

Manages the thirteen scalar keys of `~/.nitter-cli/config.toml` (defaults, env
overrides and the array tables are documented in the
[README](../../README.md#configuration)):

```text
default_limit, max_pages, request_interval, retry_attempts, retry_delay,
instance_cooldown, fetch_backend, proxy, log_level, log_format, download_path,
filename_template, directory_template
```

- `config path` prints the config file path. Takes no arguments (else exit 2).
- `config get` without a key prints all thirteen keys as `key = value`; with a
  key it prints that one. Unknown keys are rejected (exit 2) before the file
  is read.
- `config set KEY [VALUE]` validates and coerces the value **before any disk
  write** (integers `>= 0` for `default_limit`/`max_pages`/`retry_attempts`;
  durations `>= 0` for `request_interval`/`retry_delay`/`instance_cooldown`;
  `fetch_backend` is `mix|nitter|fx`;
  `log_level` is `debug|info`; `log_format` is `text|json`; `proxy`,
  `download_path` and the two naming templates accept any string). Without a
  VALUE, one line is read from piped stdin (secrets should not need argv); on
  a TTY with no VALUE it is a usage error. Unknown keys are rejected with a
  hint that `[[instances]]`/`[[watch.sources]]` are hand-edited.
- `config unset KEY` removes the key so it falls back to env/default.
- `download_path` (default `./nitter-media`, relative to the working
  directory) is where `nitter download` writes media. It has no env override;
  `nitter download --output DIR` overrides it per invocation, and the
  directory is created at download time — `config set` performs no existence
  check.
- `filename_template` (default `{id}-{seq}`) and `directory_template`
  (default empty = flat) are `nitter download`'s naming templates; neither
  has an env override. `--filename-template` overrides the filename one per
  invocation. Any string is accepted at `config set` time — an invalid
  template warns on stderr and falls back to the default/flat at download
  time (see the download section).
- Writes preserve unknown keys and the array tables and are atomic (staged file,
  mode 0600). **Comments in config.toml are not guaranteed to survive a
  `config set`/`config unset`.**
- On a fresh install the first real command (anything except `--help`/`-h`,
  `--version`, the `help` subcommand, and `config set`/`config unset`) publishes
  a self-documenting baseline config; `config set`/`config unset` seed it after
  validation. Nothing is ever overwritten.

Exit codes: success 0; bad key/value/arity 2. A config file that cannot be read
or parsed (invalid TOML) fails with exit 1; a value failing schema validation
(such as a malformed duration) is a usage error, exit 2.

## nitter watch

```bash
nitter watch [SOURCE...] [--once] [--interval D] [--max-new N] \
  [--max-new-overflow drop|keep] [--max-pages N] [--include-existing] \
  [--state-dir DIR] [--ndjson] [--json] [--no-reposts] [--media-only] \
  [--media-type image|video|gif]
```

Polls sources in cycles and prints only tweets that are new against the
persistent dedup state (`~/.nitter-cli/state/seen.json`, or
`<--state-dir>/seen.json`).

**Sources** are any list of `user:<handle>`, `tag:<query>` and `list:<id>`,
e.g. `user:NASA`, `tag:#AI`, `tag:from:nasa`, `list:12345`. The ref after the
first colon is passed through verbatim to the fetch and forms the seen key
`<kind>:<ref>` — **write tag queries raw (`tag:#AI`); the URL-escaped form
(`tag:%23AI`) would be double-escaped on the wire and is not valid.** With no
SOURCE arguments the config's `[[watch.sources]]` entries are used; when both
are empty: exit 2.

**Flags**

| Flag | Default | Meaning |
| --- | --- | --- |
| `--once` | off | Run exactly one cycle and exit — the recommended scheduler form. |
| `--interval D` | `10m` | Loop sleep between cycles without `--once`; must be a duration `>= 1s` (validated in both modes). |
| `--max-new N` | `10` | Emit at most N new tweets per source per cycle (newest first). `0` emits nothing and seals the current first page as the new baseline; negative is a usage error. |
| `--max-new-overflow drop\|keep` | `drop` | What happens to new tweets beyond the `--max-new` cap in one cycle. `drop` marks the excess seen immediately — never re-emitted (宁丢勿重). `keep` leaves it unseen so the next cycles re-emit it under the same cap (宁重勿丢; a burst larger than twice the cap drains over several cycles). Another value is a usage error; `--max-new 0` always rebuilds the baseline regardless. |
| `--max-pages N` | config `max_pages` (5) | Fetch-page budget per cycle; `0` = use the default. |
| `--include-existing` | off | Emit the whole first fetch on an uninitialized source (default: first run only records state). |
| `--state-dir DIR` | `~/.nitter-cli/state` | Directory holding `seen.json` (created if missing). |
| `--ndjson` | off | One envelope per record: `kind` `tweet` and `kind` `error`. |
| `--json` | — | Only with `--once`: prints ONE JSON document `{"tweets":[…bare Tweet objects…],"errors":[{"ref","code","message"}…]}` — the cycle's selected tweets and its per-source fetch failures (both arrays literal `[]` when empty; the failed-source summary still exits 1). Without `--once` it is a usage error: the resident loop is a stream of cycles, not one document. |
| `--no-reposts` | off | Drop pure retweets **before dedup** (the retweet header only exists on the HTML parse path). |
| `--media-only` | off | Drop tweets without media attachments, **before dedup**. |
| `--media-type image\|video\|gif` | — | Keep only tweets carrying at least one media entry of that type, **before dedup**; another value is a usage error. |

Global `--proxy`/`--instance` apply as everywhere.

**First run and emission rules**

- The first cycle of an uninitialized source only RECORDS state — no history is
  emitted (只记不推). `--include-existing` lifts that for the run and bypasses
  `--max-new` for that first fetch.
- Later cycles emit each source's new tweets, at most `--max-new` per source per
  cycle. By default (`--max-new-overflow drop`) **excess new tweets are marked
  seen immediately and never re-emitted**: after a downtime, a burst larger than
  the cap per source per cycle silently loses the tweets beyond the cap —
  scheduler deployments should set `--max-new` explicitly.
  `--max-new-overflow keep` instead leaves the excess unseen, so the next
  cycles re-emit it under the same cap (宁重勿丢) until the backlog drains; a
  burst larger than twice the cap therefore takes several cycles.
- An initialized source whose fetch succeeds but comes back empty keeps its
  previous state wholesale (nothing is sealed).
- **Field filters run before dedup**: tweets dropped by `--no-reposts`,
  `--media-only` or `--media-type` are not recorded as seen — each cycle
  re-fetches and re-filters them without emitting them, so filtering never
  grows the state or re-pushes old tweets. `--max-new` counts only tweets
  that pass the filters (the cap applies to what the consumer receives), and
  the watermark anchors the filtered first page.
- Tweets are produced first and the state persisted after (produce-then-persist):
  a delivery or state-write failure leaves the old state, so the next round
  re-pushes (宁重勿丢).

**Failures and exit codes**

- A source whose fetch fails gets an in-place error report (error envelope on
  the `--ndjson` stream; `error: <key>: <message>` on stderr otherwise) while
  the other sources continue; its state is left untouched. `--once` exits 1 when
  at least one source failed, 0 when all succeeded. Exit 2 for usage problems
  (bad source string, empty source set, `--interval < 1s`, negative flags,
  invalid `--max-new-overflow`, `--json` without `--once`, `--json` with
  `--ndjson`).
- Without `--once` the command loops until SIGINT/SIGTERM (graceful exit 0) or
  an unrecoverable error (state-store failure, non-EPIPE stdout write failure →
  exit 1).
- A closed stdout pipe (EPIPE) counts as the consumer hanging up and exits 0 in
  both modes. On Windows the detection is best-effort (broken pipes may surface
  as `ERROR_BROKEN_PIPE`).

**State** is per source: up to 300 seen IDs (newest-first) plus the 20 most
recent numeric first-page IDs as the scan watermark. Inspect it with
`nitter seen list --state-dir <dir>` (or, without the flag, the default
location); `seen clear` takes the same flag.

Example — a scheduler entry consuming the NDJSON stream (illustrative):

```bash
nitter watch user:NASA tag:#AI --once --ndjson --max-new 50
```

To prefer re-delivery over loss on bursts, add `--max-new-overflow keep`:

```bash
nitter watch user:NASA tag:#AI --once --ndjson --max-new 50 --max-new-overflow keep
```

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{…},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"…"},"meta":{"input":"tag:#AI"}}
```

`--once --json` prints the whole cycle as one document instead (illustrative):

```json
{"tweets":[{"id":"2081668333762687236","url":"…","text":"…","author":{…},"published_at":"2026-09-12T08:00:00Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}],"errors":[{"ref":"tag:#AI","code":"upstream_unavailable","message":"…"}]}
```

## nitter seen

```bash
nitter seen list [--source SOURCE] [--json] [--state-dir DIR]
nitter seen clear [--source SOURCE] [--state-dir DIR] --confirm
```

Inspects and clears the watch dedup state — at the **default** location
`~/.nitter-cli/state/seen.json`, or at `<--state-dir>/seen.json` (the same
directory `watch --state-dir` uses). Nothing is created: a state directory
without a `seen.json` is simply the empty store.

- `seen list` prints one tab-separated line per source, sorted by key:

  ```text
  user:NASA	initialized=true	seen=142	watermark=20	2026-09-12T08:00:00Z
  ```

  `updated_at` is RFC3339 UTC, or `-` when the entry carries no stamp. `--source`
  narrows to one source, parsed exactly like a watch source (`user:NASA`,
  `tag:#AI`, `list:12345`; a filter matching nothing is the same empty listing).
  `--json` prints a JSON array of
  `{source, initialized, seen_count, watermark_count, updated_at}` — always an
  array, even for a single source. An empty store prints `(empty)` on stderr
  (nothing on stdout); with `--json` it prints `[]` on stdout. Exit 0 in all
  these cases; bad `--source` exits 2.
- `seen clear` deletes one source's entry (with `--source`) or every entry
  (without). **`--confirm` is required every time** — state changes need explicit
  authorization; missing `--confirm` exits 2. Clearing a source that is not
  stored succeeds idempotently with a `not found` hint on stderr. Success prints
  nothing (the exit code is the signal). The file schema version is kept; writes
  are atomic.
- A corrupt state file is a hard error (exit 1) — the store never silently resets
  state, because a silent reset would re-push a whole watch history.

## nitter update

```bash
nitter update
nitter update --check [--prerelease] [--json]
```

Reports how to update the binary. **`update` never self-installs** — the
guidance form (without `--check`) prints the package-manager / manual-download
instructions and exits 0.

- `--check` compares the installed version against the latest release of
  `github.com/shitianyaa/nitter-cli` via the GitHub Releases API. Drafts are
  always excluded; `--prerelease` admits prereleases into the "latest"
  selection. Selection is by strict semver precedence (optional `v` prefix,
  build metadata ignored, spec prerelease ordering) over the first API page,
  not by recency. Exit 0 on every successful check — outdatedness is a
  reported result, not a failure:
  `update available: <version> (<release URL>)` versus `up to date`. The
  installed version being newer than the latest release (e.g. an installed
  prerelease) counts as up to date.
- `--json` (only with `--check`) prints one JSON document with every key
  present: `{"current":"0.1.0","latest":"0.2.0","outdated":true,"prerelease":false,"release_url":"…"}`.
  On a development build: `{"current":"dev","development_build":true}`.
- Development builds (compiled without version metadata) skip the check
  entirely: there is no release to compare `dev` against.
- A failed check — network failure, GitHub error (HTTP status only; response
  bodies are never echoed), or no usable release — is a runtime failure
  (exit 1).
- `--json` and `--prerelease` are only valid together with `--check`
  (otherwise a usage error, exit 2).
