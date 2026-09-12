# twitter CLI reference

[Documentation](../index.md) · [简体中文](../zh-CN/cli-reference.md)

This is the public command contract for the `twitter` binary. Run
`twitter <command> --help` before automating a command; the help text of the
installed binary is the source of truth for the exact flags it accepts.

## Global options

Every command accepts these persistent options:

| Option | Meaning |
| --- | --- |
| `--proxy URL` | Proxy for this invocation (`http`, `https`, `socks5`, `socks5h`). Precedence: flag > config `proxy` > environment (`HTTPS_PROXY`/`ALL_PROXY` when the config value is empty). |
| `--instance URL` | Nitter instance URL for this invocation. It **replaces the whole configured instance set** with this single URL. An invalid proxy scheme or instance URL fails as a usage error (exit 2) before anything runs. |

`twitter --version` prints `twitter version <version>`; a bare `twitter` prints
help. An unknown subcommand exits 1 (not 2).

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success — including empty results, a consumer closing the stdout pipe (EPIPE, best-effort detection on Windows), and SIGINT/SIGTERM shutdown of `watch`. |
| `1` | Runtime failure — acquisition failure (every instance failed), `watch --once` with at least one failed source, corrupt state file, config file read/parse failure, unknown subcommand. |
| `2` | Usage error — bad flags/arguments, input-contract violations, `--json` with `--ndjson`, `watch --json`, invalid config values. SDK/network errors are never classified as usage errors. |

## Output modes

All data commands resolve their output mode the same way: `--ndjson` or `--json`
wins; without flags a TTY gets the human rendering and a pipe gets the same
text rendering (the two are identical tab-separated lines; no ANSI colors).

- **Human / text**: one row per record; an empty result prints nothing on stdout
  and the `(empty)` hint on **stderr**. Row shape for tweet lists:
  `<ID>\t<YYYY-MM-DD HH:MM>\t@<handle>\t<single-line text>` — date in UTC at
  minute precision, text flattened to one line (tabs become spaces; other control
  characters switch the whole text cell to a quoted rendering).
- **`--json`**: one JSON object when exactly one record, a JSON array otherwise,
  a literal `[]` when empty.
- **`--ndjson`**: one `twitter.pipeline/v1` envelope per record:

```json
{"schema":"twitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800}],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

`meta` carries provenance: `source` (the command input as a `kind:ref` key),
`instance` (the base URL of the instance that produced the batch) and
`fetched_at` (RFC3339 UTC). Empty `meta` fields are omitted. The `kind` enum is
additive-only in v1; the currently emitted kinds are `tweet` (data commands),
`instance_report` (`instances test --ndjson`) and `error` (per-source fetch
failures of `watch`):

```json
{"schema":"twitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"chooser: upstream_unavailable: no instances configured"},"meta":{"input":"user:NASA"}}
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
  applies. **`--max-pages 0` means "use the default", not "unlimited"**; a
  negative value is a usage error.
- Retries: `retry_attempts` (default 2) extra attempts with linear backoff
  `retry_delay` (default 1s); a 429 with a valid `Retry-After` waits once and
  retries once.

## twitter user

```bash
twitter user <HANDLE> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--media-type image|video|gif] [--json|--ndjson]
```

Fetches the timeline of `HANDLE` — 1–15 letters, digits or underscores, without
the `@` (bad shape exits 2 before any network). The RSS feed (`<HANDLE>/rss`) is
tried first; when it fails or yields no tweets the HTML user page is fetched,
following its load-more cursor. NDJSON `meta.source` is `user:<HANDLE>`.

Field filters apply **after the fetch, before output** (the three combine
freely; an invalid `--media-type` value exits 2):

- `--no-reposts` drops pure retweets (the retweet header only exists on the
  HTML parse path; on user timelines the RSS path additionally flags them by
  author mismatch — a retweeted item links the original author, never the
  requested handle. Search/list have no RSS layer, so that signal does not
  extend to them).
- `--media-only` drops tweets that carry no media attachments.
- `--media-type image|video|gif` keeps only tweets carrying at least one media
  entry of that type.

## twitter search

```bash
twitter search <QUERY> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--media-type image|video|gif] [--json|--ndjson]
```

Runs `QUERY` against the configured instances. The query is passed through to
Nitter unchanged (URL-escaped once by the HTTP layer), so Nitter's own query
syntax applies: a leading `#` searches a hashtag, `from:user` a user's posts,
anything else is a plain phrase search. An empty (whitespace-only) query exits 2.
NDJSON `meta.source` is `search:<query as typed>`.

The same field filters apply as on `user`: `--no-reposts`, `--media-only`,
`--media-type image|video|gif` (applied after the fetch, before output).

## twitter list

```bash
twitter list <LIST_ID> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
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

## twitter get

```bash
twitter get <REF> [--json|--ndjson]
```

Fetches one single status. `REF` is a bare numeric status ID, or a status URL —
`x.com`, `twitter.com` or any Nitter instance, shape `<user>/status/<id>`; the
user segment is optional (Nitter serves `/status/<id>` directly) and `/photo/N`
and `/video/1` suffixes are accepted. With no argument and a non-TTY stdin the
reference is read from one stdin line; giving it both ways is an ambiguity
error (exit 2). There are no pagination flags. The quoted tweet, when present,
is summarized in the `quote` field (visible in `--json`/`--ndjson`); interaction
counts are not reported — none are fabricated. NDJSON `meta.source` is
`status:<numeric ID>`.

Example (`--json` prints exactly one object for the single status; shape
illustrative):

```json
{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}
```

## twitter instances test

```bash
twitter instances test [URL] [--full] [--list-id ID] [--user HANDLE] [--json|--ndjson]
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

## twitter config

```bash
twitter config path
twitter config get [KEY]
twitter config set KEY [VALUE]
twitter config unset KEY
```

Manages the nine scalar keys of `~/.twitter-cli/config.toml` (defaults, env
overrides and the array tables are documented in the
[README](../README.md#configuration)):

```text
default_limit, max_pages, request_interval, retry_attempts, retry_delay,
instance_cooldown, proxy, log_level, log_format
```

- `config path` prints the config file path. Takes no arguments (else exit 2).
- `config get` without a key prints all nine keys as `key = value`; with a key
  it prints that one. Unknown keys are rejected (exit 2) before the file is read.
- `config set KEY [VALUE]` validates and coerces the value **before any disk
  write** (integers `>= 0` for `default_limit`/`max_pages`/`retry_attempts`;
  durations `>= 0` for `request_interval`/`retry_delay`/`instance_cooldown`;
  `log_level` is `debug|info`; `log_format` is `text|json`; `proxy` accepts any
  string). Without a VALUE, one line is read from piped stdin (secrets should
  not need argv); on a TTY with no VALUE it is a usage error. Unknown keys are
  rejected with a hint that `[[instances]]`/`[[watch.sources]]` are hand-edited.
- `config unset KEY` removes the key so it falls back to env/default.
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

## twitter watch

```bash
twitter watch [SOURCE...] [--once] [--interval D] [--max-new N] \
  [--max-pages N] [--include-existing] [--state-dir DIR] [--ndjson] \
  [--no-reposts] [--media-only] [--media-type image|video|gif]
```

Polls sources in cycles and prints only tweets that are new against the
persistent dedup state (`~/.twitter-cli/state/seen.json`, or
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
| `--max-pages N` | config `max_pages` (5) | Fetch-page budget per cycle; `0` = use the default. |
| `--include-existing` | off | Emit the whole first fetch on an uninitialized source (default: first run only records state). |
| `--state-dir DIR` | `~/.twitter-cli/state` | Directory holding `seen.json` (created if missing). |
| `--ndjson` | off | One envelope per record: `kind` `tweet` and `kind` `error`. |
| `--json` | — | **Not supported**: watch is a stream of mixed tweets and errors, not a single JSON document; always a usage error. |
| `--no-reposts` | off | Drop pure retweets **before dedup** (the retweet header only exists on the HTML parse path). |
| `--media-only` | off | Drop tweets without media attachments, **before dedup**. |
| `--media-type image\|video\|gif` | — | Keep only tweets carrying at least one media entry of that type, **before dedup**; another value is a usage error. |

Global `--proxy`/`--instance` apply as everywhere.

**First run and emission rules**

- The first cycle of an uninitialized source only RECORDS state — no history is
  emitted (只记不推). `--include-existing` lifts that for the run and bypasses
  `--max-new` for that first fetch.
- Later cycles emit each source's new tweets, at most `--max-new` per source per
  cycle. **Excess new tweets are marked seen immediately and never re-emitted**:
  after a downtime, a burst larger than the cap per source per cycle silently
  loses the tweets beyond the cap — scheduler deployments should set
  `--max-new` explicitly.
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
  `--json`).
- Without `--once` the command loops until SIGINT/SIGTERM (graceful exit 0) or
  an unrecoverable error (state-store failure, non-EPIPE stdout write failure →
  exit 1).
- A closed stdout pipe (EPIPE) counts as the consumer hanging up and exits 0 in
  both modes. On Windows the detection is best-effort (broken pipes may surface
  as `ERROR_BROKEN_PIPE`).

**State** is per source: up to 300 seen IDs (newest-first) plus the 20 most
recent numeric first-page IDs as the scan watermark. Inspect it with
`twitter seen list`; note that `seen` has no `--state-dir` — if you run
`watch --state-dir <dir>`, read that directory's `seen.json` directly.

Example — a scheduler entry consuming the NDJSON stream (illustrative):

```bash
twitter watch user:NASA tag:#AI --once --ndjson --max-new 50
```

```json
{"schema":"twitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{…},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
{"schema":"twitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"…"},"meta":{"input":"tag:#AI"}}
```

## twitter seen

```bash
twitter seen list [--source SOURCE] [--json]
twitter seen clear [--source SOURCE] --confirm
```

Inspects and clears the watch dedup state at the **default** location
`~/.twitter-cli/state/seen.json` — `seen` has no `--state-dir`.

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

## twitter update

```bash
twitter update
twitter update --check [--prerelease] [--json]
```

Reports how to update the binary. **The MVP performs no self-install** — the
guidance form (without `--check`) prints the package-manager / manual-download
instructions and exits 0.

- `--check` compares the installed version against the latest release of
  `github.com/shitianyaa/twitter-cli` via the GitHub Releases API. Drafts are
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
