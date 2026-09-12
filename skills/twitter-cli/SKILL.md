---
slug: twitter-cli
version: 0.2.0
displayName: Twitter CLI
summary: Safely operate public-tweet retrieval through the twitter binary and your own Nitter instances, with explicit state changes and scheduler-friendly watch semantics.
license: MIT
homepage: https://github.com/shitianyaa/twitter-cli
tags: [twitter, nitter, cli, agent]
name: twitter-cli
description: 通过 twitter-cli 的 `twitter` 二进制和用户自建的 Nitter 实例检索公开推文（用户时间线、搜索、List、单条推文），并把推文解析成可直接下载的媒体直链（`twitter media`：视频 mp4、图片原图、GIF），用 watch 做去重轮询；仅在用户明确授权时变更本地状态（配置、去重状态）。当用户明确提到 twitter-cli、`twitter` 命令、Nitter 监控/推文抓取、要求解析或下载推文中的视频/图片/GIF，或要求把推文流接入调度/管道时加载；不要用于发推、点赞等任何写操作（本工具没有这些能力）。每次执行前以 `twitter <command> --help` 核对当前可用参数。
---

# twitter-cli Operator

This skill lets an agent operate the `twitter` binary safely and accurately.
命令语义以当前安装二进制的 `--help` 为准（command semantics are always governed
by the `--help` of the installed binary）; this file only provides workflows,
safety boundaries, and semantics traps.

## Precheck

- Probe the environment only with `twitter --version`; the output looks like
  `twitter version <v>` (for example `twitter version 0.1.0`). If the binary is
  missing or not executable, state the blocker; do not guess installation steps
  unless the user explicitly asks for install help.
- Instances come from the user's config (`twitter config path` prints the
  location, typically `~/.twitter-cli/config.toml`). When no instance is
  configured, ask the user for their own Nitter instance address and have them
  add an `[[instances]]` table (or run with `--instance URL`); **never fill in a
  public instance** (nitter.net and friends) on your own.
- Check exit codes before interpreting output: 0 = success, 1 = runtime
  failure, 2 = usage error.

## Hard rules (never violate)

1. Never echo instance credentials (`username`/`password`) from `config.toml`
   into commentary, logs, or code blocks, and do not read the file to "help
   debug".
2. State changes (`seen clear`, `config set`, `config unset`) need consent for
   each individual command; authorization never carries across commands.
3. Do not invent flags; when semantics are unclear, run
   `twitter <command> --help` first.
4. `--json`/`--ndjson` only describe successful output. Check the exit code
   before parsing; stderr is never JSON. Never present a failure as an "empty
   result".
5. `watch` is stateful: by default the first run of a source only initializes
   dedup state and emits nothing (history is never pushed). To emit history,
   the user must explicitly opt in with `--include-existing`.
6. Do not wrap commands in invented timeouts. For long-running work use
   `watch --once` plus scheduler polling; do not keep a foreground loop running
   and waiting.
7. `watch --json` does not exist (it is rejected with exit 2): watch streams
   NDJSON. Do not pass `--json` and `--ndjson` together to any command
   (mutually exclusive, exit 2).

## Command tiers

| Tier | Commands | Agent behavior |
| --- | --- | --- |
| Read-only | `user`, `search`, `list`, `get`, `media`, `instances test`, `seen list`, `config get`, `config path`, `update --check`, `--version` | May run directly when the user's task needs them |
| Local write | `config set`, `config unset`, `seen clear` | Confirm every single time; authorization does not carry over |
| Scheduled / resident | `watch --once` (recommended) / `watch` | Follow the user-given cadence; prefer `--once` driven by a scheduler (cron, systemd timer, Hermes) |
| Software update | `update` (without `--check`) | Prints how to update; never self-installs — do not attempt install steps unless the user asks |

Notes: `instances test` completes even when every probe fails (the report is
the product — exit 0); treat the report, not the exit code, as the diagnostic.
`seen clear` requires `--confirm` and only touches the default state location.
`media` is read-only too, but its auto chain hands the tweet URL to third-party
public resolvers (fx/vx/syndication/xdown) — use it only for public statuses
the user is fine sharing (see trap 16).

## Output and piping

- For humans: the default tab-separated text (same on a TTY and in a pipe).
- For programs: `--ndjson` — one `twitter.pipeline/v1` envelope per line, kind
  `tweet` or `error` (plus `instance_report` for `instances test --ndjson`,
  `media` for `media --ndjson`).
  For single-object extraction: `--json` (one object for one record, an array
  for many, `[]` when empty). Which commands take which flag: `--json` on
  `user` `search` `list` `get` `media` `instances test` `seen list`
  `update --check`; `--ndjson` on `user` `search` `list` `get` `media`
  `instances test` and `watch`; `watch` has no `--json`; `config`/
  `seen clear` have neither.
- Shrink first with `--limit` before reaching for `jq`; do not add limits,
  pages, timeouts, or retries the user did not ask for.
- In an error envelope, `code` is the SDK error kind (`rate_limited`,
  `upstream_unavailable`, `challenge_required`, `not_found`,
  `malformed_upstream_response`, `local_state_error`, `invalid_argument`, or
  `error` for unclassified) — classify by `code`, never by parsing `message`.
- `watch --once --ndjson` stdout is the delivery stream: exit 0 = every source
  succeeded, exit 1 = at least one source failed (its details are in the
  `kind:"error"` envelope on stdout), exit 2 = usage error. A consumer closing
  the stream early (EPIPE) is a graceful exit 0, not a failure.

## Command quick reference

Verify flags with `--help` before each use; these examples are navigation, not
a stable API contract.

```text
twitter --version
twitter config path                                      # prints config location; creates baseline if missing
twitter config get default_limit                         # read one effective setting (env > file > default)
twitter config set max_pages 5                           # state change: consent each time
twitter config unset proxy                               # state change: consent each time

twitter instances test                                   # probe every [[instances]] entry
twitter instances test http://nitter.internal:8080 --full # +search probe; --list-id ID adds list probe; --user NAME overrides probe account
twitter instances test URL --ndjson                      # one instance_report envelope per instance

twitter user NASA --limit 5                              # timeline, RSS first, HTML fallback
twitter user NASA --limit 20 --json                      # array of tweet objects (single object when exactly one)
twitter user NASA --no-reposts --media-only --json       # field filters: drop retweets, keep only tweets with media
twitter user NASA --media-type image --json              # keep only tweets carrying an image entry (video|gif likewise)
twitter user NASA --limit 0 --max-pages 3                # 0 = all, bounded by max pages (RSS yields ~20/page)
twitter user NASA --instance http://127.0.0.1:8080       # per-invocation instance override (never persisted)
twitter user NASA --proxy socks5://127.0.0.1:10808       # per-invocation proxy (http/https/socks5/socks5h)

twitter search "#AI" --limit 10 --json                   # hashtag: pass raw, escaping happens once
twitter search "from:nasa" --limit 10 --ndjson           # user search form
twitter search "moon landing" --limit 10                 # plain phrase

twitter list 12345 --limit 10 --json                     # list timeline by numeric ID; new lists may look empty
twitter get https://x.com/NASA/status/2081668333762687236 --json
twitter get 2081668333762687236 --json                   # bare numeric ID also works
echo https://x.com/NASA/status/2081668333762687236 | twitter get   # one ref from non-TTY stdin

twitter media https://x.com/NASA/status/2081668333762687236 --json   # resolve downloadable media (image originals + video mp4)
twitter media <ref> --strategy xdown --json                # force one resolver (auto = fx→vx→syndication→nitter→xdown)
twitter media <ref> --quality medium --ndjson              # video bitrate / image pbs tier
twitter media <ref> --probe --json                         # + duration/size (extra ranged requests; best-effort)

twitter watch user:NASA --once --ndjson                  # recommended Hermes form (scheduler-driven)
twitter watch user:NASA tag:#AI list:12345 --once --ndjson   # mixed sources; failed source = error envelope, others continue
twitter watch --once --ndjson                            # sources from [[watch.sources]]
twitter watch user:NASA --once --include-existing --ndjson   # first run emits history (explicit opt-in)
twitter watch user:NASA --once --max-new 50 --ndjson     # raise the per-source emission cap (default 10)
twitter watch user:NASA tag:#AI --once --ndjson --no-reposts   # field filter before dedup: reposts re-fetched each cycle, never emitted
twitter watch user:NASA --interval 5m                    # resident loop; SIGINT/SIGTERM exits gracefully
twitter watch user:NASA --once --state-dir D:/tmp/state --ndjson   # isolated state (seen list cannot see it)

twitter seen list                                        # inspect dedup state (default location)
twitter seen list --json
twitter seen clear --source user:NASA --confirm          # state change: consent each time
twitter seen clear --confirm                             # clear ALL sources: consent each time

twitter update --check                                   # read-only release comparison
twitter update --check --json                            # {current, latest, outdated, url}
```

## Key semantics and traps

1. **Fetch layering**: user timelines try RSS first and fall back to the HTML
   user page on failure or empty results — same instance list, no switch to
   disable. `search`/`list`/`get` are HTML-only.
2. **RSS is single-page.** A user timeline returns at most ~20 tweets per
   fetch even with a larger `--limit`; `--limit 40` legitimately stops at the
   RSS page. Deeper scans: use `watch` cycles, or rely on the HTML fallback —
   which only triggers when RSS fails or is empty.
3. **Instance cooldown**: an instance failing with 429 or a network error cools
   down (default 60s, config `instance_cooldown`) and is skipped while cooling;
   success resets immediately. Rotation is strictly in config order — no
   success weighting.
4. **Dedup state size**: per source up to 300 seen tweet IDs plus a scan
   watermark of the 20 most recent first-page status IDs
   (`~/.twitter-cli/state/seen.json`, schema v1).
5. **First run records only (只记不推)**: an uninitialized source's first cycle
   seeds the state and emits nothing; `--include-existing` lifts that for the
   run. An empty first cycle on a `user:` source still initializes (next cycle
   emits); `tag:`/`list:` sources stay uninitialized until a non-empty cycle.
6. **`--max-new` (default 10)**: caps emission per source per cycle (newest
   first). Excess new tweets are marked seen immediately and never re-emitted —
   after downtime, a burst larger than the cap per source per cycle silently
   loses the tweets beyond the cap. Scheduler deployments should set
   `--max-new` explicitly. `--max-new 0` emits nothing and seals the current
   first page as the new baseline.
7. **Tag sources take the raw query**: `tag:#AI`, `tag:from:nasa`. The ref
   after the first colon passes through verbatim (URL-escaping happens exactly
   once in the HTTP layer); the pre-escaped `tag:%23AI` form double-escapes and
   is **not valid**.
8. **Author fields depend on the path**: on the RSS path (the normal one)
   `author` carries only `handle` — `name`/`avatar_url` are empty; the HTML
   fallback path fills what the page provides. `reposted_by` is a display
   name, not a handle, and is only populated on the HTML path (RSS carries no
   reposter identity). On user timelines the RSS path still flags retweets by
   author mismatch (a retweeted item's handle is the original author's, never
   the requested handle); search/list, which fetch no RSS, get no such flag.
   Do not treat an empty `reposted_by` on RSS data as "not a retweet".
9. **Empty JSON fields are contract, not bugs**: `published_at` is always UTC
   RFC3339; a tweet without media marshals `"media": null` (not `[]`);
   `--max-pages 0` means the built-in default (5).
10. **New lists may look empty**: a freshly created list can be empty until the
    Nitter instance ingests it — indistinguishable from a truly empty list;
    neither is an error.
11. **429 handling**: a `Retry-After` is honored once (wait, retry once);
    a final 429 is classified `rate_limited`. In watch, a failed source gets an
    in-place error envelope, its state is untouched, other sources continue, and
    `--once` exits 1.
12. **Exit codes**: 0 success (including empty results and EPIPE), 1 runtime
    failure (all instances failed / partial watch failure / partial `media`
    batch failure / corrupt state file / unknown subcommand), 2 usage error.
    Check the exit code before parsing any JSON; stderr is never JSON.
13. **`--state-dir` isolates watch state** (testing, multi-instance setups), but
    `seen list`/`seen clear` have no `--state-dir` and always operate on the
    default location — with a custom `--state-dir`, read that directory's
    `seen.json` directly.
14. **Instances are trust boundaries**: only the user's own instances belong in
    config; the CLI follows media/redirect URLs an instance returns, so the
    network around the instance must isolate internal services.
15. **Field filters run before dedup in watch**: `--no-reposts`, `--media-only`
    and `--media-type image|video|gif` (on `user`/`search`/`list`/`watch`)
    apply right after the fetch, before selection/dedup — filtered tweets are
    not marked seen and are re-fetched (never re-emitted) each cycle, and
    `--max-new` counts only filtered-through tweets. An invalid
    `--media-type` value exits 2. Note `--no-reposts` acts on the HTML
    retweet header only: RSS-sourced data carries no repost marker.
16. **`media` strategies and the privacy boundary**: `--strategy auto` tries
    fx → vx → syndication → nitter → xdown and returns the FIRST strategy
    that yields media (`source` stamps the winner). fx/vx/syndication/xdown
    are THIRD-PARTY public services that receive the tweet URL — only resolve
    public statuses the user is fine sharing, and never feed them private or
    sensitive links; `nitter` instead reads the status page from the user's
    own configured instance (its plain-http links are kept as-is). A status
    without media resolves as a `not_found` error (exit 1), not an empty
    success — that is the resolver contract, not a malfunction.
17. **`media` returns ALL entries — a deliberate CLI divergence**: the
    reference plugin skips image candidates when a status also has video/GIF;
    the CLI does not — every media entry of the winning strategy comes back
    (images and video together) and the CONSUMER chooses. `--quality
    high|medium|low` picks the image pbs tier and the main video variant
    (every variant stays in `variants`); `--probe` adds duration/size via
    extra ranged requests and is best-effort — a probe failure keeps the
    values empty and never fails the run.

## Routing

- [references/instances.md](references/instances.md) — configuring instances
  and diagnosing their health.
- [references/watch.md](references/watch.md) — scheduling and dedup details:
  `--once` cron mode, first-run record-only, `--max-new` rule, tag form,
  `--state-dir`, exit-code matrix.
- [references/troubleshooting.md](references/troubleshooting.md) — common error
  table and fixes.
