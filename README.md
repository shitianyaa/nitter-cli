# nitter-cli

[English](README.md) · [简体中文](README.zh-CN.md) · [Documentation](docs/index.md)

`nitter` is an unofficial command-line client for **public tweets**, served through
**Nitter instances you run yourself**. It fetches user timelines, search results,
list timelines and single statuses, and it can watch sources continuously and emit
only genuinely new tweets against a persistent dedup state — designed to be driven
by a scheduler (cron, systemd timers, Hermes) and consumed as NDJSON.

It is also a public Go SDK (`github.com/shitianyaa/nitter-cli/sdk`, package
`nitter`) with a stable, additive-only data model.

## What it can do

- **Fetch public tweets** through your own Nitter instances: `user` (RSS first,
  HTML user page as fallback), `search`, `list`, and single statuses via `get`.
- **Watch sources continuously**: `watch` polls `user:`/`tag:`/`list:` sources,
  deduplicates against `~/.nitter-cli/state/seen.json` and emits only new tweets.
  `--once` runs exactly one cycle — the recommended scheduler form.
- **Download media to disk**: `download` resolves each status ref through the
  `media` strategy chain and writes the planned files to `--output DIR` or the
  `download_path` config key — a video status converges to its ONE best file
  (bitrate-ranked, with the other candidates as fallbacks), an image-only status
  to every image, and `--kind cover` fetches just the video's cover image.
- **Diagnose instances**: `instances test` probes RSS / user timeline / search /
  list capabilities of each configured instance with one report line each.
- **Three output modes for every data command**: human-readable tab-separated rows,
  whole-result `--json`, and one-envelope-per-record `--ndjson`
  (`nitter.pipeline/v1`) — and NDJSON is the automatic default whenever stdout
  is a pipe rather than a terminal.
- **Manage its own configuration and state**: `config path/get/set/unset` for the
  twelve scalar keys, `seen list/clear` for the watch dedup state.
- **Check for updates**: `update --check` compares the installed version against
  the latest GitHub release (strict semver, `--json` for machines). It performs
  no self-install.
- **Rotate and cool down instances**: instances are tried in config order; an
  instance that fails (HTTP 429, network error) cools down (default 60s) while the
  next one is tried. No success weighting — the config order is the policy.

## Quick start

nitter-cli ships without instances: **you point it at a Nitter instance you
control**. Nothing is fetched until you configure one.

1. **Install** — two routes:

   a. **Download a release binary** (recommended): pick the archive for your
   platform from
   [GitHub Releases](https://github.com/shitianyaa/nitter-cli/releases) and
   verify it against the attached `checksums.txt`.

   b. **Build from source** (Go 1.27+):

   ```bash
   sh scripts/build.sh          # produces ./nitter
   ./nitter --version          # nitter version 0.5.0 (or a dev line)
   ```

2. **Configure your instance** — edit `~/.nitter-cli/config.toml` (the path is
   printed by `nitter config path`; the file with commented examples is created
   on the first real command):

   ```toml
   [[instances]]
   url = "http://nitter.internal:8080"   # <your-instance>: your own Nitter URL
   # username = ""                       # optional basic-auth credentials
   # password = ""                       # (carried but unused in the MVP transport)
   ```

3. **Test the instance** (no live fetch of your data yet, just capability probes):

   ```bash
   nitter instances test                # probes every [[instances]] entry
   nitter instances test http://nitter.internal:8080 --full
   ```

   Example output (cells are `ok`, `fail(<reason>)` or `-`):

   ```text
   url	rss	user_html	search	list	latency
   http://nitter.internal:8080	ok	ok	ok	-	212ms
   ```

4. **Fetch something** (examples — output depends on your instance and the data):

   ```bash
   nitter user NASA --limit 5
   nitter search "#nitter" --limit 10
   nitter get https://x.com/NASA/status/2081668333762687236
   ```

5. **Watch sources from your scheduler** — one cycle per invocation, new tweets
   only, state persists between runs:

   ```bash
   # crontab: every 10 minutes, NDJSON stream into your consumer
   */10 * * * * nitter watch user:NASA tag:#AI --once --ndjson >> /var/log/nitter-watch.ndjson 2>/tmp/nitter-watch.err
   ```

   Sources can also live in config under `[[watch.sources]]`; run
   `nitter watch --once --ndjson` with no arguments to use them.

## Configuration

`~/.nitter-cli/config.toml` (TOML, mode 0600). Precedence: CLI flag > environment
> file > built-in default. `nitter config get` prints effective values;
`nitter config set KEY VALUE` writes (value may also be piped on stdin);
`nitter config unset KEY` removes a key. The `[[instances]]` and
`[[watch.sources]]` array tables are managed by editing the file directly —
`config set` refuses them.

### Scalar keys (all twelve)

| Key | Type | Default | Env override | Meaning |
| --- | --- | --- | --- | --- |
| `default_limit` | int | `20` | `NITTER_DEFAULT_LIMIT` | Tweets per manual command when `--limit` is not given (`0` = all) |
| `max_pages` | int | `5` | — | Pagination cap per fetch (on `user`, an explicit `--max-pages 0` instead removes the cap) |
| `request_interval` | duration | `1s` | — | Global minimum interval between the starts of consecutive requests |
| `retry_attempts` | int | `2` | — | Extra attempts after the first, for network errors and 5xx |
| `retry_delay` | duration | `1s` | — | Linear backoff base: the n-th retry waits `retry_delay × n` |
| `instance_cooldown` | duration | `60s` | — | How long an instance is skipped after a failure (429 / network error) |
| `proxy` | string | `""` | — | Proxy URL (`http(s)`, `socks5(h)`); empty = environment proxies (`HTTPS_PROXY`/`ALL_PROXY`) |
| `log_level` | enum | `info` | `NITTER_LOG_LEVEL` | `debug` or `info`; diagnostics go to stderr, never stdout |
| `log_format` | enum | `text` | `NITTER_LOG_FORMAT` | `text` or `json` (single-line) |
| `download_path` | string | `./nitter-media` | — | Where `nitter download` writes media (cwd-relative; created on demand; `download --output DIR` overrides it per invocation) |
| `filename_template` | string | `{id}-{seq}` | — | Download filename for regular media, placeholders `{id}` `{seq}` `{user}` `{kind}` `{ext}` (default = the pre-template `<id>-<seq>.<ext>` naming; covers always `<id>-cover.<ext>`; `download --filename-template` overrides per invocation; invalid templates warn and fall back) |
| `directory_template` | string | `""` | — | Download subdirectory below `download_path`, placeholders `{id}` `{user}` `{kind}` (`/` separates levels; `{seq}`/`{ext}` forbidden; empty = flat) |

Environment overrides apply on top of the file for exactly these three keys:
`NITTER_DEFAULT_LIMIT` (integer), `NITTER_LOG_LEVEL`, `NITTER_LOG_FORMAT`.

### Array tables

```toml
[[instances]]
url = "http://nitter.internal:8080"   # required
username = ""                         # optional basic auth (see note)
password = ""

[[watch.sources]]                     # default sources for `nitter watch`
id = "user:NASA"                      # user:<handle> | tag:<query> | list:<id>
```

Note: instance credentials are carried in the config but the MVP transport does
not wire them in — probes and fetches run unauthenticated. Put the instance behind
your own network-layer access control instead.

## Output modes

Every data command (`user`, `search`, `list`, `get`, `media`, `download`,
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
no change. `watch` keeps its text default in pipes (pass `--ndjson` for its
envelope stream); `config`, `seen` and `update` are unchanged.

Example tweet envelope (illustrative; `data` is the `Tweet` model of the SDK):

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

In-place error envelopes (currently emitted by `watch` per failed source):

```json
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"chooser: upstream_unavailable: no instances configured"},"meta":{"input":"user:NASA"}}
```

**Exit codes — check the exit code before parsing any JSON.** `--json`/`--ndjson`
only describe successful output; **stderr is never JSON**.

- `0` — success (including empty results; a consumer closing the stdout pipe, and
  SIGINT/SIGTERM shutdown of `watch`, also exit 0);
- `1` — runtime failure (every instance failed; `watch --once` with at least one
  failed source; a corrupt `seen.json`; config file read/parse failures);
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
  all succeeded.
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
`~/.nitter-cli/config.toml` (configuration) and `~/.nitter-cli/state/seen.json`
(watch dedup state). On Windows both live under your user profile directory
(`nitter config path` prints the exact location). Writes are atomic; a corrupt
state file is a hard error (exit 1), never a silent reset.

**How do I use a different instance for one command?**
`nitter --instance http://nitter.internal:8080 user NASA` replaces the configured
instance set for this invocation only. `--proxy` analogously overrides the proxy
(flag > config > environment).

**Can I get media URLs?**
Yes — `media` entries in the `Tweet` model (visible via `--json`/`--ndjson`)
carry direct image/video links as the instance served them.

## Disclaimer

1. **Public tweets only.** nitter-cli fetches exclusively publicly accessible
   tweets through Nitter. It bundles no instances, performs no login, and provides
   no capability to bypass access controls or solve challenges.
2. **Only use instances you control and trust.** The CLI follows the media and
   redirect URLs returned by the configured instance. Isolate your internal
   services (Redis, cloud metadata endpoints, admin panels) from the network path
   of nitter-cli and its instances.
3. **No bypassing of access controls.** If a page requires login, is a challenge,
   or the instance is rate-limited, the CLI reports the classified error instead of
   working around it. Respect X's terms and your instances' capacity; this project
   is not affiliated with X Corp. or the Nitter project.

## License

MIT.
