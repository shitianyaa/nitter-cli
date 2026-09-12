---
slug: twitter-cli
version: 0.1.0
displayName: Twitter CLI
summary: Safely operate public-tweet retrieval through the twitter binary and your own Nitter instances, with explicit state changes and scheduler-friendly watch semantics.
license: MIT
homepage: https://github.com/shitianyaa/twitter-cli
tags: [twitter, nitter, cli, agent]
name: twitter-cli
description: 通过 twitter-cli 的 `twitter` 二进制和用户自建的 Nitter 实例检索公开推文（用户时间线、搜索、List、单条推文），并用 watch 做去重轮询；仅在用户明确授权时变更本地状态（配置、去重状态）。当用户明确提到 twitter-cli、`twitter` 命令、Nitter 监控/推文抓取，或要求把推文流接入调度/管道时加载；不要用于发推、点赞等任何写操作（本工具没有这些能力）。每次执行前以 `twitter <command> --help` 核对当前可用参数。
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
   NDJSON. Do not pass `--json` to `watch`, and never pass `--json` and
   `--ndjson` together to any command (mutually exclusive, exit 2).

## Command tiers

| Tier | Commands | Agent behavior |
| --- | --- | --- |
| Read-only | `user`, `search`, `list`, `get`, `instances test`, `seen list`, `config get`, `config path`, `--version` | May run directly when the user's task needs them |
| Local write | `config set`, `config unset`, `seen clear` | Confirm every single time; authorization does not carry over |
| Scheduled / resident | `watch --once` (recommended) / `watch` | Follow the user-given cadence; prefer `--once` driven by a scheduler (cron, systemd timer, Hermes) |

Notes: `instances test` completes even when every probe fails (the report is
the product — exit 0); treat the report, not the exit code, as the diagnostic.
`seen clear` requires `--confirm` and only touches the default state location.

## Output and piping

- For humans: the default tab-separated text (same on a TTY and in a pipe).
- For programs: `--ndjson` — one `twitter.pipeline/v1` envelope per line, kind
  `tweet` or `error` (plus `instance_report` for `instances test --ndjson`).
  For single-object extraction: `--json` (one object for one record, an array
  for many, `[]` when empty).
- Shrink first with `--limit` before reaching for `jq`; pass `--ndjson` when the
  consumer is a program or a log file.
- `watch --once --ndjson` stdout is the delivery stream: exit 0 = every source
  succeeded, exit 1 = at least one source failed (its details are in the
  `kind:"error"` envelope on stdout), exit 2 = usage error. A consumer closing
  the stream early (EPIPE) is a graceful exit 0, not a failure.

## Command quick reference

Verify flags with `--help` before each use; these examples are navigation, not
a stable API contract.

```text
twitter --version
twitter config path
twitter config get default_limit
twitter instances test                                  # probe all [[instances]]
twitter instances test http://nitter.internal:8080 --full
twitter user NASA --limit 5                             # timeline, RSS first
twitter search "#AI" --limit 10                         # query passes through to Nitter
twitter list 12345                                      # list timeline by numeric ID
twitter get https://x.com/NASA/status/2081668333762687236
twitter watch user:NASA tag:#AI --once --ndjson         # recommended Hermes form
twitter watch --once --ndjson                           # sources from [[watch.sources]]
twitter seen list
twitter seen clear --source user:NASA --confirm         # state change: consent each time
twitter config set max_pages 5                          # state change: consent each time
```

## Key semantics and traps

1. **Fetch layering**: user timelines try RSS first and fall back to the HTML
   user page on failure or empty results — same instance list, no switch to
   disable.
2. **Instance cooldown**: an instance failing with 429 or a network error cools
   down (default 60s, config `instance_cooldown`) and is skipped while cooling;
   success resets immediately. Rotation is strictly in config order — no
   success weighting.
3. **Dedup state size**: per source up to 300 seen tweet IDs plus a scan
   watermark of the 20 most recent first-page status IDs
   (`~/.twitter-cli/state/seen.json`, schema v1).
4. **First run records only (只记不推)**: an uninitialized source's first cycle
   seeds the state and emits nothing; `--include-existing` lifts that for the
   run.
5. **`--max-new` (default 10)**: caps emission per source per cycle (newest
   first). Excess new tweets are marked seen immediately and never re-emitted —
   after downtime, a burst larger than the cap per source per cycle silently
   loses the tweets beyond the cap. Scheduler deployments should set
   `--max-new` explicitly. `--max-new 0` emits nothing and rebuilds the
   baseline from the first page.
6. **Tag sources take the raw query**: `tag:#AI`, `tag:from:nasa`. The ref
   after the first colon passes through verbatim (URL-escaping happens exactly
   once in the HTTP layer); the pre-escaped `tag:%23AI` form double-escapes and
   is **not valid**.
7. **New lists may look empty**: a freshly created list can be empty until the
   Nitter instance ingests it — indistinguishable from a truly empty list;
   neither is an error.
8. **429 handling**: a `Retry-After` is honored once (wait, retry once);
   a final 429 is classified `rate_limited`. In watch, a failed source gets an
   in-place error envelope, its state is untouched, other sources continue, and
   `--once` exits 1.
9. **Exit codes**: 0 success (including empty results and EPIPE), 1 runtime
   failure (all instances failed / partial watch failure / corrupt state file /
   unknown subcommand), 2 usage error. Check the exit code before parsing any
   JSON; stderr is never JSON.
10. **`--state-dir` isolates watch state** (testing, multi-instance setups), but
    `seen list`/`seen clear` have no `--state-dir` and always operate on the
    default location — with a custom `--state-dir`, read that directory's
    `seen.json` directly.

## Routing

- [references/instances.md](references/instances.md) — configuring instances
  and diagnosing their health.
- [references/watch.md](references/watch.md) — scheduling and dedup details:
  `--once` cron mode, first-run record-only, `--max-new` rule, tag form,
  `--state-dir`, exit-code matrix.
- [references/troubleshooting.md](references/troubleshooting.md) — common error
  table and fixes.
