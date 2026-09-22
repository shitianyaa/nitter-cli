---
slug: nitter-cli
version: 0.7.0
displayName: Nitter CLI
summary: Safely operate public-tweet retrieval through the nitter binary, FxTwitter fast-lane, and your own Nitter instances, with explicit state changes and scheduler-friendly watch semantics.
license: MIT
homepage: https://github.com/shitianyaa/nitter-cli
tags: [nitter, cli, agent]
name: nitter-cli
description: 通过 nitter-cli 的 `nitter` 二进制和用户自建的 Nitter 实例检索公开推文（用户时间线、搜索、List、单条推文），并把推文解析成可直接下载的媒体直链（`nitter media`：视频 mp4、图片原图、GIF），用 watch 做去重轮询；仅在用户明确授权时变更本地状态（配置、去重状态）。当用户明确提到 nitter-cli、`nitter` 命令、Nitter 监控/推文抓取、部署或自建 Nitter 实例、要求解析或下载推文中的视频/图片/GIF，或要求把推文流接入调度/管道时加载；不要用于发推、点赞等任何写操作（本工具没有这些能力）。每次执行前以 `nitter <command> --help` 核对当前可用参数。
---

# nitter-cli Operator

This skill lets an agent operate the `nitter` binary safely and accurately.
命令语义以当前安装二进制的 `--help` 为准（command semantics are always governed
by the `--help` of the installed binary）; this file only provides workflows,
safety boundaries, and semantics traps.

## Precheck

- Probe the environment only with `nitter --version`; the output looks like
  `nitter version <v>` (for example `nitter version 0.7.0`). If the binary is
  missing or not executable, state the blocker. Install only when the user
  explicitly asked for installation; then read
  [references/install.md](references/install.md) and follow its approved
  sources. Otherwise do not install or guess installation steps.
- Instances come from the user's config (`nitter config path` prints the
  location, typically `~/.nitter-cli/config.toml`). The default
  `fetch_backend = mix` works without any instance for `user`, `search`, `get`,
  `comments`, `following`, `profile`, `quotes`, `trends` and `search --type
  user` (FxTwitter fast lane); instances are required for `list`, for the
  fully self-hosted `nitter` mode, and as the fallback when Fx is unavailable.
  When the user wants those, ask whether they run their own Nitter instance:
  yes → find and verify its URL
  ([references/deploy.md](references/deploy.md), "Finding an existing
  instance"); no → offer the Docker deployment from
  [references/deploy.md](references/deploy.md) on an explicit yes. In either
  case **never fill in a public instance** (nitter.net and friends) on your
  own.
- Check exit codes before interpreting output: 0 = success, 1 = runtime
  failure, 2 = usage error.

## Hard rules (never violate)

1. Never echo instance credentials (`username`/`password`) from `config.toml`
   into commentary, logs, or code blocks, and do not read the file to "help
   debug". Inspect configuration through the CLI instead: `nitter config path`
   and `nitter config get <key>` (instances are not readable this way — by
   design; diagnose them with `instances test`, see
   [references/instances.md](references/instances.md)).
2. State changes (`seen clear`, `config set`, `config unset`) need consent for
   each individual command; authorization never carries across commands.
3. Do not invent flags; when semantics are unclear, run
   `nitter <command> --help` first.
4. `--json`/`--ndjson` only describe successful output. Check the exit code
   before parsing; stderr is never JSON. Never present a failure as an "empty
   result" — on a non-zero exit, report the stderr error and follow
   [references/troubleshooting.md](references/troubleshooting.md).
5. `watch` is stateful: by default the first run of a source only initializes
   dedup state and emits nothing (history is never pushed). To emit history,
   the user must explicitly opt in with `--include-existing`.
6. Do not wrap commands in invented timeouts. For long-running work use
   `watch --once` plus scheduler polling; do not keep a foreground loop running
   and waiting.
7. `watch --json` is only valid with `--once`: it prints ONE JSON document
   `{"tweets":[...bare Tweet objects...],"errors":[{"ref","code","message"}...]}`
   for that single cycle. Without `--once` it is rejected (exit 2) — the
   resident loop is a stream of cycles; use `--ndjson` there. Do not pass
   `--json` and `--ndjson` together to any command (mutually exclusive, exit 2).
8. `download` writes files to disk (the `download_path` config key, default
   `./nitter-media`, or `--output DIR`): state the target directory and the
   exact refs to the user before each invocation; consent never carries over.
   The default `--on-exists refuse` never replaces an existing file — only
   pass `overwrite` or `skip` when the user asked for that.
9. Never overclaim completeness. RSS yields about 20 tweets per fetch, so a
   user-timeline result is one page, never "the timeline": when fewer tweets
   came back than the user may have expected, say exactly what was fetched
   (e.g. "the most recent 8 tweets available via RSS") and never tell the user
   "the latest 20 tweets are complete". `--limit N` is a cap on output, not
   proof that N exist or that nothing older remains (see also trap 2).
10. No ritual probes. Do not run `instances test` before every command — it
    costs the instance real requests. Probe only when an instance-health
    decision actually needs it (setup, diagnosing failures, comparing
    candidates); for everything else the fetch's own classified error tells
    you what is wrong (see [references/troubleshooting.md](references/troubleshooting.md)).

## Command tiers

| Tier | Commands | Agent behavior |
| --- | --- | --- |
| Read-only | `user`, `search`, `list`, `get`, `media`, `following`, `comments`, `circle list`, `circle show`, `circle run`, `trends`, `quotes`, `profile`, `instances test`, `seen list`, `config get`, `config path`, `update --check`, `--version` | May run directly when the user's task needs them |
| Local write | `config set`, `config unset`, `seen clear`, `circle add` | Confirm every single time; authorization does not carry over |
| Disk write (本地媒体写入) | `download` | Writes media files to disk: state the target directory (`--output DIR`, else the `download_path` config key, default `./nitter-media`) and the exact refs before EACH invocation; authorization never carries over |
| Scheduled / resident | `watch --once` (recommended) / `watch` | Follow the user-given cadence; prefer `--once` driven by a scheduler (cron, systemd timer, Hermes) |
| Software update | `update` (without `--check`) | Prints how to update; never self-installs — do not attempt install steps unless the user asks |

Notes: `instances test` completes even when every probe fails (the report is
the product — exit 0); treat the report, not the exit code, as the diagnostic.
`seen clear` requires `--confirm` and operates on the state location it is
given (default `~/.nitter-cli/state`, or `--state-dir DIR`).
`media` is read-only too, but its auto chain hands the tweet URL to third-party
public resolvers (fx/xdown) — use it only for public statuses
the user is fine sharing (see trap 16).

## Output and piping

- For humans on a TTY: the default tab-separated text.
- For programs: when stdout is NOT a TTY (a pipe or a redirect) the data
  commands (`user` `search` `list` `get` `media` `download` `instances test`
  `following` `comments` `trends` `quotes` `profile` `circle run`)
  emit NDJSON by DEFAULT — one `nitter.pipeline/v1` envelope per line, no flag
  needed; `--ndjson` selects the same stream explicitly (also on a TTY).
  An empty result in a pipe prints nothing at all — no `(empty)` hint; do not
  read silence as failure, check the exit code.
  Envelope kinds: `tweet` or `error` (plus `instance_report` for
  `instances test`, `media` for `media`, `download` for `download`,
  `profile` for `following`/`profile`/`search --type user`, `trend` for `trends`).
  `watch` keeps its text default in pipes — pass `--ndjson` for its envelope
  stream. `seen list`, `config`, `update` are unchanged.
  For single-object extraction: `--json` (one object for one record, an array
  for many, `[]` when empty). Which commands take which flag: `--json` on
  `user` `search` `list` `get` `media` `download` `instances test` `seen list`
  `update --check` `following` `comments` `trends` `quotes` `profile`; `--ndjson` on `user` `search` `list` `get` `media`
  `download` `instances test` `following` `comments` `trends` `quotes` `profile` and `watch`; `watch --json` only with `--once`
  (one `{"tweets","errors"}` document); `config`/`seen clear` have neither.
- Shrink first with `--limit` before reaching for `jq`; do not add limits,
  pages, timeouts, or retries the user did not ask for. If `jq` is present,
  prefer `--json` + `jq` for field extraction; if absent, fall back to the
  plain output silently — never ask the user to install anything.
- Stdin is read by four commands only, and only when they receive no
  positional value and stdin is not a TTY: `get` (one ref), `media` (one ref
  per non-empty line), `download` (refs one per non-empty line, or — when the
  first non-whitespace byte is `{` — strict `nitter.pipeline/v1` tweet
  envelopes whose `data.url` becomes the ref, so any piped data command
  (auto-NDJSON) and `watch --ndjson` feed it directly, no flags needed),
  `config set KEY` (the value, one line — this keeps
  secrets such as a credential-bearing `proxy` URL out of argv). Passing the
  value both as an argument and on stdin is an ambiguity error (exit 2).
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
nitter --version
nitter config path                                      # prints config location; creates baseline if missing
nitter config get default_limit                         # read one effective setting (env > file > default)
nitter config set max_pages 5                           # state change: consent each time
nitter config unset proxy                               # state change: consent each time

nitter instances test                                   # probe every [[instances]] entry
nitter instances test http://nitter.internal:8080 --full # +search probe; --list-id ID adds list probe; --user NAME overrides probe account
nitter instances test URL --ndjson                      # one instance_report envelope per instance

nitter user NASA --limit 5                              # timeline, RSS first, HTML fallback
nitter user NASA --with-replies --limit 5                # include user replies and interactions
nitter user NASA --limit 20 --json                      # array of tweet objects (single object when exactly one)
nitter user NASA --no-reposts --media-only --json       # field filters: drop retweets, keep only tweets with media
nitter user NASA --media-type image --json              # keep only tweets carrying an image entry (video|gif likewise)
nitter user NASA --limit 0 --max-pages 3                # 0 = all, bounded by max pages (RSS yields ~20/page)
nitter user NASA --limit 0 --max-pages 0                # max-pages 0 = unbounded HTML pagination, until upstream exhaustion
nitter user NASA --instance http://127.0.0.1:8080       # per-invocation instance override (never persisted)
nitter user NASA --proxy socks5://127.0.0.1:10808       # per-invocation proxy (http/https/socks5/socks5h)

nitter following NASA --limit 10                        # fetch accounts followed by handle (profile table or NDJSON)
nitter profile NASA                                     # display creator profile card (bio, follower count, stats)
nitter profile NASA --json                              # profile object JSON
nitter comments 2100031016471818431 --limit 10          # fetch replies/thread for tweet (extract hidden links/threads)
nitter quotes 2100031016471818431 --limit 10            # fetch quote tweets / second-creation mining for status
nitter quotes 2100031016471818431 --media-only --json   # quote tweets with media attachments
nitter trends --limit 10                                # fetch real-time Twitter/X trends (#, topic, context, tweet count)
nitter trends --json                                    # array of trend objects
nitter circle list                                      # list configured creator circles
nitter circle show shaoluo                              # show handles in circle
nitter circle show shaoluo --min-followers 5000          # only members with ≥5k followers (@handle\tfollowers per row; --json for objects)
nitter circle run shaoluo --limit 2                     # stream latest tweets for all creators in circle
nitter circle run shaoluo --media-type image --ndjson   # keep only tweets carrying an image entry (video|gif likewise)
nitter circle add shaoluo NewCreator                    # add handle to circle

nitter search "#AI" --limit 10 --json                   # hashtag: pass raw, escaping happens once
nitter search "from:nasa" --limit 10 --ndjson           # user search form
nitter search "digital art" --type user --limit 10      # search user profiles / illustrators by name/bio
nitter search "moon landing" --limit 10                 # plain phrase

nitter list 12345 --limit 10 --json                     # list timeline by numeric ID; new lists may look empty
nitter get https://x.com/NASA/status/2081668333762687236 --json
nitter get 2081668333762687236 --json                   # bare numeric ID also works
echo https://x.com/NASA/status/2081668333762687236 | nitter get   # one ref from non-TTY stdin

nitter media https://x.com/NASA/status/2081668333762687236 --json   # resolve downloadable media (image originals + video mp4)
nitter media <ref> --strategy xdown --json                # force one resolver (auto = fx→nitter→xdown)
nitter media <ref> --quality medium --ndjson              # video bitrate / image pbs tier
nitter media <ref> --probe --json                         # + duration/size (extra ranged requests; best-effort)

nitter download <ref> --output D:/media --ndjson          # resolve + write media files to disk (dir created on demand)
nitter download <ref> --kind cover                        # only the video's cover image (<id>-cover.<ext>)
nitter download <ref> --quality low --output D:/media      # quality defaults to high — say 'low'/'medium' when a smaller file is wanted
nitter download <ref> --filename-template "{kind}-{id}{ext}"         # per-call filename template (config filename_template is the default; covers ignore it; no path separators — subdirectories come from directory_template)
nitter download <ref> --strategy nitter                   # resolve + download both stay on the user's own instance
nitter download <ref> --on-exists skip                    # keep existing files: row marked (skipped), on-disk size, no sha256
nitter watch user:NASA --once --ndjson | nitter download --ndjson   # feed the watch stream straight into downloads (Disk write: consent)

nitter watch user:NASA --once --ndjson                  # recommended Hermes form (scheduler-driven)
nitter watch user:NASA tag:#AI list:12345 --once --ndjson   # mixed sources; failed source = error envelope, others continue
nitter watch user:NASA --once --json                    # one {"tweets":[...],"errors":[{ref,code,message}...]} document for the cycle (once mode only)
nitter watch --once --ndjson                            # sources from [[watch.sources]]
nitter watch user:NASA --once --include-existing --ndjson   # first run emits history (explicit opt-in)
nitter watch user:NASA --once --max-new 50 --ndjson     # raise the per-source emission cap (default 10)
nitter watch user:NASA --once --max-new 10 --max-new-overflow keep --ndjson   # bursts beyond the cap re-emit on the next cycles instead of being lost
nitter watch user:NASA tag:#AI --once --ndjson --no-reposts   # field filter before dedup: reposts re-fetched each cycle, never emitted
nitter watch user:NASA --interval 5m                    # resident loop; SIGINT/SIGTERM exits gracefully
nitter watch user:NASA --once --state-dir D:/tmp/state --ndjson   # isolated state; inspect with seen list --state-dir D:/tmp/state

nitter seen list                                        # inspect dedup state (default location)
nitter seen list --json
nitter seen list --state-dir D:/tmp/state               # inspect the state of a watch --state-dir run
nitter seen clear --source user:NASA --confirm          # state change: consent each time
nitter seen clear --confirm                             # clear ALL sources: consent each time

nitter update --check                                   # read-only release comparison
nitter update --check --json                            # {current, latest, outdated, prerelease, release_url}
nitter update --check --prerelease                      # admit prereleases into the "latest" pick (only with --check)
```

## Config keys

Thirteen scalar keys in `~/.nitter-cli/config.toml`, managed with
`config set`/`config unset` (precedence env > file > default; baseline
default in parentheses): `default_limit` (20), `max_pages` (5),
`request_interval` (1s), `retry_attempts` (2), `retry_delay` (1s),
`instance_cooldown` (60s), `fetch_backend` (`mix` — mix|nitter|fx), `proxy` (empty), `log_level` (info), `log_format`
(text), `download_path` (`./nitter-media`, cwd-relative — where `download`
writes media; `download --output` overrides it per call), `filename_template`
(`{id}-{seq}` — the download filename, placeholders
`{id}`/`{seq}`/`{user}`/`{kind}`/`{ext}`; covers always `<id>-cover`;
`--filename-template` overrides per call), `directory_template` (empty =
flat; download subdirectory from `{id}`/`{user}`/`{kind}`). An invalid
template warns on stderr and falls back to the default at download time. Env
overrides exist for four keys: `NITTER_DEFAULT_LIMIT`,
`NITTER_LOG_LEVEL`, `NITTER_LOG_FORMAT`, `NITTER_FETCH_BACKEND`. Two array tables are hand-edited
TOML, not `config set` targets: `[[instances]]` (`url`, optional
`username`/`password` — credentials, hard rule 1 applies) and
`[[watch.sources]]` (`id = "user:NASA"`; see references/watch.md). Creator circles are managed in `~/.nitter-cli/circles.toml` via `nitter circle` commands.

## Key semantics and traps

1. **Fetch layering & hybrid dispatch (`fetch_backend`)**: `fetch_backend`
   controls user timeline and search routing (`mix` default, `nitter`, `fx`).
   In `mix` mode, FxTwitter fast-lane is tried first without credentials; on
   failure (network error, rate-limit 429, or SafeSearch 404) or when an
   explicit `--instance` flag is provided, it falls back smoothly to the
   configured Nitter instance pool (RSS first, then HTML user page). `list`
   timeline is strictly isolated and ALWAYS fetches from Nitter instances
   (Fx has no List endpoint).
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
   (`~/.nitter-cli/state/seen.json`, schema v1).
5. **First run records only (只记不推)**: an uninitialized source's first cycle
   seeds the state and emits nothing; `--include-existing` lifts that for the
   run. An empty first cycle on a `user:` source still initializes (next cycle
   emits); `tag:`/`list:` sources stay uninitialized until a non-empty cycle.
6. **`--max-new` (default 10)**: caps emission per source per cycle (newest
   first). By default (`--max-new-overflow drop`) excess new tweets are marked
   seen immediately and never re-emitted — after downtime, a burst larger than
   the cap per source per cycle silently loses the tweets beyond the cap.
   Scheduler deployments should set `--max-new` explicitly.
   `--max-new-overflow keep` instead leaves the excess unseen, so the next
   cycles re-emit it under the same cap (宁重勿丢; a burst larger than twice
   the cap drains over several cycles). `--max-new 0` emits nothing and seals
   the current first page as the new baseline, under both policies.
   Another `--max-new-overflow` value is a usage error (exit 2).
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
   `--max-pages 0` on `user` means UNBOUNDED (the HTML fallback paginates until
   upstream exhaustion; the RSS feed is single-page ~20 anyway) — on
   `search`/`list` it still means the built-in default (5).
10. **New lists may look empty**: a freshly created list can be empty until the
    Nitter instance ingests it — indistinguishable from a truly empty list;
    neither is an error.
11. **429 handling**: a `Retry-After` is honored once (wait, retry once);
    a final 429 is classified `rate_limited`. In watch, a failed source gets an
    in-place error envelope, its state is untouched, other sources continue, and
    `--once` exits 1.
12. **Exit codes**: 0 success (including empty results and EPIPE), 1 runtime
    failure (all instances failed / partial watch failure / partial `media`
    batch failure / partial `download` batch failure / corrupt state file /
    unknown subcommand), 2 usage error.
    Check the exit code before parsing any JSON; stderr is never JSON.
13. **`--state-dir` isolates watch state** (testing, multi-instance setups);
    `seen list`/`seen clear` accept the same `--state-dir DIR` to operate on
    that directory's `seen.json` — without the flag they use the default
    location (`~/.nitter-cli/state`).
14. **Instances are trust boundaries**: only the user's own instances belong in
    config; the CLI follows media/redirect URLs an instance returns, so the
    network around the instance must isolate internal services. Instance
    basic auth (an `[[instances]]` entry with **both** `username` and
    `password`) is host-scoped inside the transport: those credentials ride
    only requests addressed to their own instance and never reach the
    third-party media resolvers; an incomplete pair is treated as
    unconfigured, and a `--instance URL` override carries no credentials
    (use the config entry for a credentialed instance).
15. **Field filters run before dedup in watch**: `--no-reposts`, `--media-only`
    and `--media-type image|video|gif` (on `user`/`search`/`list`/`watch`, and
    `--media-type` on `circle run`)
    apply right after the fetch, before selection/dedup — filtered tweets are
    not marked seen and are re-fetched (never re-emitted) each cycle, and
    `--max-new` counts only filtered-through tweets. An invalid
    `--media-type` value exits 2. Note `--no-reposts` acts on the HTML
    retweet header only: RSS-sourced data carries no repost marker.
16. **`media` strategies and the privacy boundary**: `--strategy auto` tries
    fx → nitter → xdown and returns the FIRST strategy
    that yields media (`source` stamps the winner). fx/xdown
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
    values empty and never fails the run. Full details and download
    guidance: references/media.md. To have the CLI write the files to disk
    itself (video-wins selection, `--on-exists`, the `watch` pipeline), see
    references/download.md.
18. **`download`/`media` default to `--quality high`** — the highest-bitrate
    video variant and the original image tier. When the user asks for a
    smaller file, pass `--quality low` or `medium` explicitly (the default
    will not); state the chosen quality when it matters, and use `--probe`
    for real sizes. Every variant stays in `variants` either way, so a
    consumer can still pick another tier from `media` output.
19. **`following` is FxTwitter-powered**: fetches accounts followed by `HANDLE`
    with avatar, bio, and follower/following counts. In non-TTY pipes it emits
    `kind: "profile"` NDJSON envelopes; `--limit 0` fetches all available pages.
20. **`comments` extracts conversation trees & hidden author links**: returns
    the root status, parent thread ancestors, and replies (sorted by `--sort likes`
    or `recency`). This is the primary mechanism for discovering author self-replies
    containing full-res download links, Fantia/Gumroad passwords, or reading
    serialized manga threads.
21. **`circle` manages curated creator rosters (`~/.nitter-cli/circles.toml`)**:
    distinct from resident `watch` polling, circles categorize favorite creators
    by theme/style for on-demand discovery (`circle show`, `--min-followers N`
    filters members by follower count — one profile fetch per member, a failed
    member is skipped with a warning, all failing exits 1) and pipeline streaming
    (`circle run <name> | nitter download -o DIR`).
22. **`trends` retrieves real-time Twitter/X trending topics**: FxTwitter-powered;
    returns trending topic rank, name, context category, tweet count, and grouped topics.
    Emits tab-separated table on TTY or `kind: "trend"` NDJSON in pipes.
23. **`quotes` uncovers quote tweets and second-creations**: status quote tweets retrieval;
    supports `--media-only` and `--no-reposts` filters, emitting tweet rows on TTY or
    `kind: "tweet"` NDJSON in pipes.
24. **`profile` and `search --type user` for creator discovery**: `profile <HANDLE>`
    displays a structured user card on TTY or `kind: "profile"` NDJSON in pipes.
    `search <QUERY> --type user` discovers creators, artists, and topic influencers
    matching keyword/bio.
25. **`get` fast-lane dispatch**: `nitter get` uses FxTwitter fast-lane first under `mix`
    (default) and `fx` modes, falling back to configured Nitter instances on failure.
26. **Profile-first discovery, search as the fallback**: the FxTwitter search
    endpoints are unreliable — `from:` tweet search returns 404 for any query
    and name-based `search --type user` can return empty even for existing
    accounts — while the handle-based endpoints (`profile`, `user`, `get`)
    work reliably. When a task needs a specific account, resolve the handle
    first (from the user, a mention, a profile URL, or a followed account) and
    go straight to `profile <HANDLE>`; only fall back to `search --type user`
    when no handle can be established, and treat an empty search result as
    "unknown", not as proof the account does not exist.

## Media delivery for agents

`nitter media` resolves links; the download is the agent's job (full
details: references/media.md). To resolve and download in one step, the CLI
has `nitter download` — a Disk write command: agree on the target directory
and the exact refs with the user before each invocation (full details:
references/download.md).

- Resolved URLs are direct links — fetch them with a plain GET (curl, wget,
  or the host's HTTP client); no cookies or sign-in involved. fx/
  xdown serve https; the `nitter` strategy may serve plain
  http:// links from the user's own instance.
- If the main URL fails, retry `fallback_url`, then the other `variants`.
- Deliver downloaded files through the host attachment API; if the host
  cannot attach files, share the resolved URL only and never claim the
  media itself was sent.

## Routing

| Task | Read |
| --- | --- |
| Install or upgrade the `nitter` binary, or install the matching Skill | [references/install.md](references/install.md) |
| Fetch public tweets (timelines, search, lists, single statuses) | the quick reference in this file; on errors [references/troubleshooting.md](references/troubleshooting.md) |
| Configure instances (incl. basic auth), override one command, diagnose instance health | [references/instances.md](references/instances.md) |
| The user has no Nitter instance (deploy with Docker, default localhost) or its URL is unknown | [references/deploy.md](references/deploy.md) |
| Resolve media links (strategies, trust boundary, quality, probe, delivery) | [references/media.md](references/media.md) |
| Write media to disk (download, templates, `--on-exists`, stdin pipelines) | [references/download.md](references/download.md) |
| Schedule monitoring (`watch` cycles, dedup, `--max-new` and the overflow policy, state dirs) | [references/watch.md](references/watch.md) |
| Errors: failure messages, exit codes, and fixes | [references/troubleshooting.md](references/troubleshooting.md) |
