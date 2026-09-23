# nitter-cli

[English](README.md) · [简体中文](README.zh-CN.md) · [Documentation](docs/index.md)

<p><a href="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml"><img alt="ci" src="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml/badge.svg"></a> <a href="https://github.com/shitianyaa/nitter-cli/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/shitianyaa/nitter-cli?style=flat-square"></a> <a href="go.mod"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/shitianyaa/nitter-cli?style=flat-square"></a> <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/shitianyaa/nitter-cli?style=flat-square"></a></p>

`nitter` is an unofficial command-line client for **public tweets**. Data comes
from **FxTwitter's public API** (the default fast lane — no account, no
credentials) with **Nitter instances you run yourself** as the private
fallback, the List path, and the fully self-hosted mode. It fetches user
timelines, search results, list timelines, single statuses, conversations,
following lists, profiles, quote tweets and trends, watches sources with
persistent dedup state, and downloads media — a flexible CLI built for agents
and schedulers (cron, systemd timers, Hermes), consumed as NDJSON.

It is also a public Go SDK (`github.com/shitianyaa/nitter-cli/sdk`, package
`nitter`) with a stable, additive-only data model.

## Why nitter-cli?

- **FxTwitter fast lane, Nitter depth** — `fetch_backend` chooses the routing:
  `mix` (default) tries FxTwitter's public API first — no account, no
  credentials — and falls back to your own instances on failure; `nitter`
  keeps every fetch on instances you control; `fx` pins the fast lane. List
  always needs Nitter. Handles and queries go to `api.fxtwitter.com` in
  mix/fx modes — pick `nitter` for a fully self-hosted setup.
- **Flexible instance policy** — point it at any Nitter instance you control:
  configure a rotation set (`[[instances]]`, tried strictly in config order,
  failed entries cool down), override per command with `--instance`, and
  optionally authenticate with host-scoped basic auth (`username` **and**
  `password` set). Credentials can only ever ride requests addressed to their
  own instance — third-party endpoints never see them.
- **Public-tweet retrieval** — `user` (RSS first, HTML user page as fallback),
  `search`, `list`, and single statuses via `get`. `list` is strictly isolated:
  every list fetch always runs on your own instances (FxTwitter has no List
  endpoint). No bundled instances, no login, no bypassing of access controls.
- **Social graph & discovery** — `following` lists who an account follows,
  `profile` prints an account card, `search --type user` finds creators and
  artists by name, `trends` shows what X is talking about right now, and
  `quotes` digs up the quote-tweet derivatives of a status — all credential-free.
- **Conversations & reply trees** — `comments` pulls a status's thread and
  replies (sorted by likes or recency), the fast way to reach an author's
  self-replies with hidden links or to read a serialized thread.
- **Creator circles** — `nitter circle` curates themed rosters in
  `~/.nitter-cli/circles.toml` (`list` / `show` / `suggest` / `add` / `run`):
  `suggest` mines a handle's following list and retweet authors for new
  candidate members, then on-demand discovery and pipeline streaming,
  distinct from scheduled `watch` subscriptions.
- **Composable pipelines** — data commands emit `nitter.pipeline/v1` NDJSON
  automatically whenever stdout is a pipe, so
  `nitter search "..." | nitter download` needs no flags; `--json` extracts
  whole documents and explicit flags always win.
- **Scheduler-ready watch** — `watch --once` runs exactly one deduplicated
  cycle against persistent state and exits; the `--max-new-overflow keep`
  policy re-emits burst overflow on later cycles instead of losing it (宁重勿丢).
- **Media downloads with templates** — a video status converges to its ONE best
  file (bitrate-ranked, remaining candidates as fallbacks), an image-only
  status to every image, `--kind cover` to just the cover; `filename_template`
  / `directory_template` config keys (plus `--filename-template`) name and
  place files via `{id}` `{seq}` `{user}` `{kind}` `{ext}` placeholders.
- **Observable instances** — `instances test` probes RSS / user timeline /
  search / list capabilities with one report line each; the report is the
  product.
- **Manageable state** — `config path/get/set/unset` for the thirteen scalar
  keys, `seen list/clear [--state-dir]` for the watch dedup state; atomic
  writes, a corrupt state file is a hard error (never a silent reset).
- **Honest update checks** — `update --check [--prerelease] [--json]` compares
  against the latest GitHub release by strict semver and performs no
  self-install.
- **Public Go SDK** — typed models (`Tweet`, `Profile`, `Conversation`, `Trend`,
  …) whose JSON keys never disappear, a narrow `Transport` boundary, and
  redacted errors; the CLI's commands consume the same surface.

## Install

### Release archive (recommended)

Download the archive for your platform from
[GitHub Releases](https://github.com/shitianyaa/nitter-cli/releases), then
verify it against the `checksums.txt` attached to the **same release**:

```bash
sha256sum -c checksums.txt --ignore-missing   # or an equivalent tool
```

Extract the `nitter` binary into a per-user directory that is on your `PATH`.
There is no installer script; the archive is the whole product.

### Source build

Requires Go 1.27+:

```bash
sh scripts/build.sh          # produces ./nitter
./nitter --version           # nitter version 0.7.0 (or a dev line)
```

### Install with an AI agent

Copy this single prompt into Codex, Claude Code, Cursor, or another local AI
agent with terminal access:

```text
Install the latest stable nitter-cli from https://github.com/shitianyaa/nitter-cli for this machine: download only an official GitHub Release archive for the detected OS and architecture from the repository's Releases page, verify its SHA-256 against the checksums.txt attached to that same release before replacing anything, install the `nitter` binary into a per-user directory without administrator or root privileges, add that directory to the current user's PATH only if `nitter` is not already reachable (state every PATH change), ask before installing any missing prerequisite, never read or output ~/.nitter-cli/config.toml or any Nitter credentials during installation, verify with `nitter --version`, and report the installed version, the binary path, and every file and PATH change.

Also install the `nitter-cli` Skill that matches the same stable release tag (never main): download the full skills/nitter-cli/ directory from that tag's git tree into the agent skills directory the user confirms. Do not guess the skills path and do not follow the main branch for skill content.
```

## 60-second quick start

nitter-cli ships without instances, and the default `mix` mode already works:
`user`, `search`, `get`, `comments`, `following`, `profile`, `quotes`, `trends`
and `search --type user` run through FxTwitter's public endpoint out of the
box. Configure a Nitter instance you control for `list`, for the fully
self-hosted `nitter` mode, and as the fallback when Fx is unavailable. Don't
have one? Deploy your own with Docker — see the
[upstream wiki](https://github.com/zedeus/nitter/wiki) or the community
[self-hosting guide](https://github.com/sekai-soft/guide-nitter-self-hosting);
an AI agent can follow the skill's [deploy reference](skills/nitter-cli/references/deploy.md).

```bash
# 0. Configure your instance — `nitter config path` prints the config file
#    (created with commented examples on the first real command); add:
#      [[instances]]
#      url = "http://nitter.internal:8080"
#      # username = ""        # optional basic auth — sent only when BOTH are set
#      # password = ""
nitter config path

# Diagnose the instance: capability probes, one line each (rss / user_html /
# search / list / latency; cells are ok, fail(<reason>) or -)
nitter instances test
nitter instances test http://nitter.internal:8080 --full

# Fetch a timeline as JSON for Hermes or any agent
nitter user NASA --limit 10 --json

# Discovery extras — no instance or account needed
nitter trends --limit 10 --json                   # what X is talking about now
nitter following NASA --limit 10                  # who an account follows
nitter profile NASA                               # account card
nitter comments 2100031016471818431 --limit 10    # reply tree (author self-replies live here)
nitter quotes <STATUS_ID> --media-only | nitter download   # derivative media from quote tweets
nitter circle run coser_acgn --limit 1            # stream a curated creator roster

# Piped output needs no flags — data commands emit NDJSON on their own,
# so the stream feeds the downloader directly
nitter search "#AI" --limit 20 | nitter download --output ./media

# Download a video at the lowest quality tier
nitter download https://x.com/NASA/status/2081668333762687236 --quality low

# Fetch only the video's cover image
nitter download https://x.com/NASA/status/2081668333762687236 --kind cover

# Set up a deduplicated watch from your scheduler: one cycle per run,
# new tweets only, state persists between runs
*/10 * * * * nitter watch user:NASA tag:#AI --once --ndjson >> /var/log/nitter-watch.ndjson 2>>/tmp/nitter-watch.err
```

Run `nitter --help` or open the [complete CLI reference](docs/en/cli-reference.md)
for every command, flag, configuration key, and update behavior.

## Choose your interface

### CLI

Use the human table interactively; `--json` for whole documents, `--ndjson`
for record streams — or just pipe: a non-TTY stdout is the NDJSON default.

```bash
nitter user NASA --limit 10                      # tab-separated rows on a TTY
nitter user NASA --limit 10 --json               # one object / an array
nitter user NASA --limit 10 --ndjson             # one nitter.pipeline/v1 envelope per tweet
nitter search "#AI" --limit 20 | nitter download # auto-NDJSON, no flags needed
```

### Go SDK

The public SDK is the package `github.com/shitianyaa/nitter-cli/sdk`
(package name `nitter`) — the same surface the CLI consumes. It composes a
`Client` from a rotation set, a narrow `Transport` interface, and an instance
cooldown; data models (`Tweet`, `Page[T]`, …) marshal unconditionally (no
`omitempty` on data fields) under an additive-only stability contract, and
errors are redacted (no credentials, no query strings, no headers/bodies).

```go
import "github.com/shitianyaa/nitter-cli/sdk"

client, err := nitter.New(
    nitter.WithInstances([]nitter.Instance{
        {URL: "http://nitter.internal:8080"},
    }),
    nitter.WithHTTPClient(transport), // implements Transport
    nitter.WithCooldown(30 * time.Second),
)
```

`nitter.Instance` carries the optional basic-auth pair of an instance; the
redaction contract keeps it out of every error and log. The
[SDK guide](docs/en/sdk.md) documents the stability contract, models,
pagination, and error kinds.

## Configuration

`~/.nitter-cli/config.toml` (TOML, mode 0600). Precedence: CLI flag > environment
> file > built-in default. `nitter config get` prints effective values;
`nitter config set KEY VALUE` writes (value may also be piped on stdin);
`nitter config unset KEY` removes a key. The `[[instances]]` and
`[[watch.sources]]` array tables are managed by editing the file directly —
`config set` refuses them. Creator circles (the `nitter circle` rosters) live
in a separate file, `~/.nitter-cli/circles.toml`, managed by the `circle`
subcommands.

### Scalar keys (all thirteen)

| Key | Type | Default | Env override | Meaning |
| --- | --- | --- | --- | --- |
| `default_limit` | int | `20` | `NITTER_DEFAULT_LIMIT` | Item cap when a command receives no `--limit` (`0` = all) |
| `max_pages` | int | `5` | — | Upper bound on pagination (on `user`, `--max-pages 0` lifts the cap) |
| `request_interval` | duration | `1s` | — | Global floor on delay between request start times |
| `retry_attempts` | int | `2` | — | Extra attempts on network errors and 5xx |
| `retry_delay` | duration | `1s` | — | Linear backoff base: attempt n waits `retry_delay × n` |
| `instance_cooldown` | duration | `60s` | — | How long an instance is skipped after a failure (429 / network error) |
| `fetch_backend` | enum | `mix` | `NITTER_FETCH_BACKEND` | Fetch backend strategy: `mix` (default, FxTwitter fast-lane with Nitter fallback), `nitter` (pure Nitter), `fx` (pure FxTwitter) |
| `proxy` | string | `""` | — | Proxy URL (`http(s)`, `socks5(h)`); empty = fall back to environment (`HTTPS_PROXY`/`ALL_PROXY`) |
| `log_level` | enum | `info` | `NITTER_LOG_LEVEL` | `debug` or `info`; diagnostics go to stderr only, stdout stays pure data |
| `log_format` | enum | `text` | `NITTER_LOG_FORMAT` | `text` or single-line `json` |
| `download_path` | string | `./nitter-media` | — | Where `nitter download` writes media files (cwd-relative; created on demand; overridden per call by `download --output DIR`) |
| `filename_template` | string | `{id}-{seq}` | — | Filename template for non-cover media, placeholders `{id}` `{seq}` `{user}` `{kind}` `{ext}` (default = legacy `<id>-<seq>.<ext>` naming; covers are always `<id>-cover.<ext>`; overridden per call by `download --filename-template`; invalid template warns and falls back to default) |
| `directory_template` | string | `""` | — | Subdirectory under `download_path`, placeholders `{id}` `{user}` `{kind}` (`/` separates nesting; `{seq}`/`{ext}` forbidden; empty = flat) |

Environment variables win over the file: `NITTER_DEFAULT_LIMIT` (integer),
`NITTER_LOG_LEVEL`, `NITTER_LOG_FORMAT`, `NITTER_FETCH_BACKEND`.

### Array tables

```toml
[[instances]]
url = "http://nitter.internal:8080"   # required
username = ""                         # optional basic auth (see note)
password = ""

[[watch.sources]]                     # default sources for `nitter watch`
id = "user:NASA"                      # user:<handle> | tag:<query> | list:<id>
```

Note: when an `[[instances]]` entry sets **both** `username` and `password`,
requests to that instance carry HTTP basic auth. The credential policy is
host-scoped inside the transport — a credential is only ever attached to a
request addressed to its own configured instance, so the third-party media
endpoints (`media`/`download` resolvers such as fx/xdown and
twimg) can never receive it; credentials also never enter errors, logs, or
responses. An incomplete pair (only one half set) is treated as unconfigured.
A one-off `--instance URL` override is a plain URL and carries **no**
credentials — for a credentialed instance, use the config entry. Put instances
you cannot credential behind your own network-layer access control instead.

## Output modes

Every data command (`user`, `search`, `list`, `get`, `media`, `download`,
`following`, `comments`, `trends`, `quotes`, `profile`, `circle run`,
`instances test`) resolves its output mode the same way:

| Mode | How | Shape |
| --- | --- | --- |
| Human / text | default on a **TTY** | one tab-separated row per record; empty result prints `(empty)` on **stderr** |
| NDJSON | **default when stdout is not a TTY** (pipe or redirect, no flag needed); explicitly via `--ndjson` | one `nitter.pipeline/v1` envelope per record, one line each |
| JSON | `--json` | one JSON object when exactly one record, a JSON array otherwise, `[]` when empty |

**Piped stdout defaults to NDJSON (0.6.0 behavior change).** When stdout is a
pipe or a file and neither `--json` nor `--ndjson` is given, the data commands
emit `nitter.pipeline/v1` envelopes instead of text — so
`nitter search "..." | nitter download` works without flags (each envelope's
`data.url` feeds the downloader's stdin envelope mode). Explicit flags always
win, and on a TTY the default stays the human table — interactive users see
no change. An empty result in a pipe is fully silent (no `(empty)` hint on
stdout or stderr). `watch` keeps its text default in pipes (pass `--ndjson`
for its envelope stream); `config`, `seen` and `update` are unchanged.

Example tweet envelope (illustrative; `data` is the `Tweet` model of the SDK):

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

`meta.instance` records the provenance of the fetch: `"FxTwitter"` when the
fast lane answered, otherwise the URL of the instance that did. `meta.source`
names the operation and its input (`user:NASA`, `search:#AI`, `following:NASA`,
`comments:<id>`, `quotes:<id>`, `trends`, `circle:<name>`, …).

In-place error envelopes (currently emitted by `watch` per failed source):

```json
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"chooser: upstream_unavailable: no instances configured"},"meta":{"input":"user:NASA"}}
```

**Exit codes — check the exit code before parsing any JSON.** `--json`/`--ndjson`
only describe successful output; **stderr is never JSON**.

- `0` — success (including empty results; a consumer closing the stdout pipe, and
  SIGINT/SIGTERM shutdown of `watch`, also exit 0);
- `1` — runtime failure (every instance failed; `watch --once` with at least one
  failed source; a corrupt `seen.json` or `profiles.toml`; config file read/parse failures);
- `2` — usage error (bad flags or arguments, input-contract violations,
  `--json --ndjson` together, `watch --json` without `--once`, invalid config
  values; unknown *subcommand* names exit 1).

## Watch semantics (read before automating)

- **First run records only (只记不推)**: the first cycle of an uninitialized source
  initializes the dedup state and emits **nothing** — history is never pushed.
  Pass `--include-existing` to emit the whole first fetch once (this also bypasses
  `--max-new` on that run).
- **State size**: per source, up to 300 seen tweet IDs plus a scan watermark of the
  20 most recent first-page status IDs. Inspect with `nitter seen list`, delete
  with `nitter seen clear [--source user:NASA] --confirm`.
- **`--max-new` (default 10)** caps emission per source per cycle (newest first).
  By default (`--max-new-overflow drop`) excess new tweets are **marked seen
  immediately and never re-emitted**: after a downtime, a burst larger than the
  cap per source per cycle silently loses the tweets beyond the cap. Scheduler
  deployments should set `--max-new` explicitly to a value that covers your
  sources' quiet-period bursts — or pass `--max-new-overflow keep` to leave the
  excess unseen so the next cycles re-emit it under the same cap (宁重勿丢; a
  burst larger than twice the cap drains over several cycles).
  `--max-new 0` emits nothing and rebuilds the baseline from the first page
  (under both policies).
- **Tag sources use the raw query**: `tag:#AI`, `tag:from:nasa`. The ref after the
  first colon is passed through verbatim (the HTTP layer URL-escapes it exactly
  once). The pre-escaped form `tag:%23AI` would be double-escaped on the wire and
  is **not valid**.
- **Per-source failures never abort a cycle**: the failed source gets an in-place
  error report (error envelope in `--ndjson`), its state is left untouched, other
  sources continue. `watch --once` exits 1 when at least one source failed, 0 when
  all succeeded. `watch --once --json` prints one document
  `{"tweets":[...],"errors":[{ref,code,message}...]}` for that cycle.
- **A closed stdout pipe is a graceful exit 0** (the consumer hung up, e.g.
  `head`). Tweets already delivered are still followed by the state write when the
  pipe survives; if the write itself is cut short, the next round re-pushes the
  same tweets (宁重勿丢 — prefer duplicates over losses). On Windows, broken pipes
  may surface as a different errno (`ERROR_BROKEN_PIPE`), so the EPIPE → 0
  detection is best-effort there.
- **`seen` follows `--state-dir`**: `nitter seen list/clear` operate on the
  default `~/.nitter-cli/state/seen.json`, or on `<dir>/seen.json` when given
  `--state-dir <dir>` — the same directory `watch --state-dir` uses.

## FAQ

**Why is nothing pushed the first time I run watch?**
By design: the first cycle seeds the dedup state so that your stream starts at
"now". Use `--include-existing` once if you actually want the history.

**Why did a burst of new tweets only partially arrive?**
`--max-new` (default 10) capped the emission; with the default overflow policy
(`drop`) the rest was marked seen and never re-emitted (see Watch semantics).
Raise `--max-new`, shorten the polling interval, or pass `--max-new-overflow
keep` so the next cycles re-emit the backlog.

**RSS works but `search` returns nothing.**
Search is a separate Nitter capability and may be disabled or slow on your
instance; probe it with `nitter instances test --full`. Also check the query
form: hashtag queries must be written raw (`tag:#AI` in watch, `nitter search
"#AI"` in search) — `%23` double-escapes.

**`nitter list 12345` is empty — is it broken?**
An empty result may mean the list is empty, or that it is new and not yet ingested
by your instance; the two are indistinguishable from the outside. Neither is an
error.

**Where is everything stored?**
`~/.nitter-cli/config.toml` (configuration), `~/.nitter-cli/circles.toml`
(creator-circle rosters), `~/.nitter-cli/profiles.toml` (cached circle-member
profiles, keyed by lowercase handle) and `~/.nitter-cli/state/seen.json` (watch
dedup state). On Windows these live under your user profile directory
(`nitter config path` prints the exact location). Writes are atomic; a corrupt
state file is a hard error (exit 1), never a silent reset.

The profiles sidecar is filled by `nitter circle refresh <NAME>` and read by
`nitter circle show`. It holds machine-refreshed facts (`name`, `bio`,
`followers_count`, `fetched_at`) plus judgement you record yourself (`role`,
`note`, `noted_at`) — a refresh overwrites the facts and never touches the
judgement. It is machine-managed, so a rewrite drops comments; hand-edit only
`role`/`note`.

**What does the default `mix` mode send where?**
`user`, `search`, `get`, `comments`, `following`, `profile`, `quotes`, `trends`
and `search --type user` try FxTwitter's public endpoint
(`api.fxtwitter.com`) first — the handle or query goes to that third-party
service, with no credentials of yours attached. On failure the request falls
back to your own instances. `list` always runs on your instances (FxTwitter
has no List endpoint), `--instance URL` forces the Nitter path for a command,
and `fetch_backend = "nitter"` keeps every fetch on infrastructure you
control.

**How do I use a different instance for one command?**
`nitter --instance http://nitter.internal:8080 user NASA` replaces the configured
instance set for this invocation only — note that this override carries no basic
auth credentials. `--proxy` analogously overrides the proxy
(flag > config > environment).

**Can I get media URLs?**
Yes — `media` entries in the `Tweet` model (visible via `--json`/`--ndjson`)
carry direct image/video links as the instance served them. `nitter download`
writes them to disk, and `nitter media` resolves links without downloading.

## Disclaimer

1. **Public tweets only.** nitter-cli fetches exclusively publicly accessible
   tweets — through the public FxTwitter API (the default `mix` / `fx`
   backends) and through Nitter instances you run yourself (the `nitter`
   backend, the fallback path, and every `list` fetch). It bundles no
   instances, performs no login, and provides no capability to bypass access
   controls or solve challenges.
2. **Only use instances you control and trust.** The CLI follows the media and
   redirect URLs returned by the configured instance. Isolate your internal
   services (Redis, cloud metadata endpoints, admin panels) from the network path
   of nitter-cli and its instances.
3. **No bypassing of access controls.** If a page requires login, is a challenge,
   or the instance is rate-limited, the CLI reports the classified error instead of
   working around it. Respect X's terms and your instances' capacity; this project
   is not affiliated with X Corp. or the Nitter project.

## License

[MIT](LICENSE).
