# Watch: scheduling and dedup details

`nitter watch` is the stateful core: poll sources in cycles, dedup against
`~/.nitter-cli/state/seen.json`, and emit only new tweets. Command semantics
are governed by the installed binary's `nitter watch --help`.

## Recommended form: `--once` + scheduler (Hermes)

The recommended deployment is one process per cycle, driven by a scheduler;
the process boundary is the state boundary and stdout is consumed directly:

```bash
# crontab: every 10 minutes
*/10 * * * * nitter watch user:NASA tag:#AI --once --ndjson >> /var/log/nitter-watch.ndjson 2>> /var/log/nitter-watch.err
```

- Sources can be argv (`user:NASA tag:#AI list:12345`) or config
  `[[watch.sources]]` entries (`id = "user:NASA"`) used when argv is empty;
  when both are empty the run exits 2 before fetching.
- The resident loop (`watch` without `--once`, sleeping `--interval`, default
  10m, minimum 1s) is the fallback form; it exits 0 on SIGINT/SIGTERM. Prefer
  the scheduler form.

## First run: record-only (只记不推)

The first cycle of an uninitialized source only seeds the dedup state — no
history is emitted. This is by design: the stream starts at "now".

- To emit the whole first fetch once, pass `--include-existing` for that run
  (it also bypasses `--max-new` for that first fetch).
- How deep that first fetch reaches is decided by `--max-pages`: a user
  source's RSS scan pages along the feed's `Min-Id` cursor up to the page
  budget, so the recorded baseline covers several feed pages, not one.
- After the first run, each cycle emits the source's new tweets (IDs not in the
  seen list, newest first).

## `--max-new` semantics and the overflow policy

- Default 10: at most 10 new tweets per source per cycle are emitted, newest
  first.
- Default overflow policy `--max-new-overflow drop`: excess new tweets are
  **marked seen immediately and never re-emitted** — after downtime, a burst
  larger than the cap per source per cycle silently loses the tweets beyond the
  cap (宁丢勿重; the plugin's original semantics).
- `--max-new-overflow keep` (宁重勿丢) leaves the excess unseen: the next
  cycles re-fetch and re-emit it under the same cap, without repeating
  already-delivered tweets, until the backlog drains. A burst larger than twice
  the cap therefore takes several cycles to drain. Only the seen-marking of the
  excess changes; the first-page watermark always keeps tracking what was
  fetched.
- `--max-new 0` emits nothing and seals the current first page as the new
  baseline under BOTH policies — the overflow policy only governs capped
  emission rounds, so `keep` does not disable this escape hatch. Negative
  `--max-new` and any `--max-new-overflow` value other than `drop`/`keep` are
  usage errors (exit 2).
- Scheduler deployments should set `--max-new` explicitly to cover their
  sources' quiet-period bursts (for example `--max-new 50`), or pass
  `--max-new-overflow keep` when a silent loss is unacceptable.
- An initialized source whose fetch succeeds but comes back empty keeps its
  previous state wholesale — nothing is sealed by a transient empty response.

## Merged fetching (multi-source + `--no-reposts`)

With `--no-reposts` and two or more `user:` sources, a cycle fetches those
sources with ONE RSS request per batch instead of one request per source:
`/{u1,u2,...}/rss`, batched so each request path stays under 250 characters.
The merged feed is split per author, so the per-source dedup state is
unaffected.

- **Eligibility**: `--no-reposts` plus at least two `user:` sources. `tag:` and
  `list:` sources are never merged.
- **Repost trade-off**: the merged feed cannot represent a repost — Nitter
  attributes a reposted item to the ORIGINAL author — so merging with reposts
  kept would misattribute them. That is why merging is tied to `--no-reposts`;
  items authored by an account outside the batch (the shape a repost of an
  outsider takes) are dropped.
- **Backend**: `fetch_backend = "fx"` never merges (the fast lane has no merged
  endpoint). `"mix"` does, which means those sources are served by Nitter
  instead of the fast lane for that cycle.
- **Failure**: a batch whose request fails is not lost — the affected sources
  fall back to their own fetch.
- A batch holding a source whose dedup state is not initialized yet keeps
  scanning the full `--max-pages` budget: merging must not cut a new source's
  baseline short because the other batch members are already caught up.

## Tag sources: raw query form

Write the query exactly as Nitter should receive it: `tag:#AI`,
`tag:from:nasa`. The ref after the first colon passes through verbatim; the
HTTP layer URL-escapes it exactly once. The pre-escaped form `tag:%23AI`
double-escapes on the wire and returns wrong or empty results — it is not
valid anywhere (watch sources, `[[watch.sources]]`, `seen --source`).

## State: `--state-dir` and the seen store

- Default state location: `~/.nitter-cli/state/seen.json` (schema v1: per
  source `initialized`, up to 300 `seen_ids` newest-first, up to 20 numeric
  first-page `watermark_ids`, `updated_at`).
- `--state-dir DIR` points watch at another directory (created if missing) —
  use it for tests or isolated deployments.
- `seen list`/`seen clear` accept the same `--state-dir DIR` and operate on
  `<dir>/seen.json`; without the flag they use the default location.
- Tweets are produced first, state persisted after: a delivery or state-write
  failure keeps the old state, so the next round re-pushes the same tweets
  (prefer duplicates over losses). A consumer closing the stdout pipe is a
  graceful exit 0; if the pipe breaks mid-state-write, the next round re-pushes.
- A corrupt state file is a hard error (exit 1) — never a silent reset.

## Multi-category subscriptions: one `--state-dir` per category

A subscription *category* is a group of sources that share a purpose, a cadence,
or a push channel — for example `ai-artists` polled every 10 minutes into
Telegram, and `news` posted hourly into a digest channel. Give every category
its OWN state directory:

```bash
# crontab
*/10 * * * * nitter watch user:A user:B --once --state-dir ~/.nitter-cli/state/ai-artists --ndjson >> /var/log/nitter-ai.ndjson
0 * * * *    nitter watch tag:#AI --once --state-dir ~/.nitter-cli/state/news --ndjson >> /var/log/nitter-news.ndjson
```

- One directory per category, under a common root
  (`~/.nitter-cli/state/<category>`); the directory is created on demand.
- Every scheduler line of a category must pass the SAME `--state-dir`. A line
  that omits the flag silently falls back to the default
  `~/.nitter-cli/state` — that is how one missing flag re-couples two
  categories.
- A brand-new (empty) directory makes the category's next cycle a first run:
  record-only (只记不推), nothing is emitted until the cycle after that, unless
  that run passes `--include-existing`.

**Why a shared `seen.json` silently loses tweets (静默漏推).** Dedup state is
keyed by source key (`user:NASA`, `tag:#AI`) inside one `seen.json` per
directory. Two categories that subscribe to the same account use the SAME key
`user:NASA`:

- The category whose cycle runs FIRST emits the new tweet and marks its ID
  seen. The other category's cycle then finds the ID already seen and emits
  nothing — that channel never receives the tweet. There is no error and no
  warning: the second run exits 0 with an empty stream, indistinguishable from
  a quiet source.
- The collision is per source key, so it hits exactly the accounts that belong
  to more than one category. Different `--max-new` values or cadences do not
  help; only separate state directories do.
- `seen clear --source <key>` and `seen list` operate on one directory too: with
  a shared directory, resetting a category's backlog also rewrites every other
  category's view of that account, and a diagnosis mixes categories together.
  Keep the reset scoped by passing the category's `--state-dir` (consent still
  required for `clear`).

**Why separate directories also protect concurrent runs.** The store
serializes writes only WITHIN one process (a package mutex) and persists
through a temp-file + rename; there is no cross-process file lock. Two
scheduler entries that fire in the same second against the same `seen.json`
read-modify-write the same file and the last writer wins, discarding the other
run's seen marks (re-pushing already delivered tweets) or its baseline.
Distinct directories make concurrent categories independent writers, so no
ordering assumption between jobs is needed.

Operations per category:

```bash
nitter seen list  --state-dir ~/.nitter-cli/state/ai-artists
nitter seen clear --source user:NASA --state-dir ~/.nitter-cli/state/ai-artists --confirm
```

With `--state-dir`, the default location is ignored entirely (reads create
nothing); without it, both commands operate on `~/.nitter-cli/state/seen.json`.

## `--once --json`: one document per cycle

With `--once` (and only then), `--json` replaces the text rows with ONE JSON
document for the whole cycle:

```json
{"tweets":[{"id":"103","url":"…","text":"…","author":{…},"published_at":"…","media":null,"is_retweet":false}],"errors":[{"ref":"user:Broken","code":"upstream_unavailable","message":"…"}]}
```

- `tweets` holds the cycle's selected tweets as bare Tweet objects (the same
  shape as the data commands' `--json` payloads) — on a record-only first run
  it is a literal `[]`.
- `errors` holds one `{ref, code, message}` entry per failed source (`ref` =
  the source key, `code` = the SDK error kind, same classification as the
  NDJSON error envelope). Healthy sources still deliver; the run still exits 1
  when at least one entry is present.
- Without `--once`, `--json` is a usage error (exit 2): the resident loop is a
  stream of cycles, not one document — use `--ndjson`. `--json` and `--ndjson`
  are mutually exclusive.

## Exit-code matrix (`--once`)

| Exit code | When | What you get |
| --- | --- | --- |
| 0 | Every source fetched and emitted successfully | Tweet envelopes (`kind:"tweet"`) on stdout; nothing on stderr |
| 0 | Consumer closed stdout early (EPIPE) | Possibly truncated stream; state may lag — next round re-pushes |
| 0 | SIGINT/SIGTERM (also in loop mode) | Graceful shutdown; sources not yet run in the cycle are simply skipped |
| 1 | At least one source failed | Failed sources produce `kind:"error"` envelopes on stdout (or `error: <key>: <message>` lines on stderr without `--ndjson`); healthy sources' tweets still stream; failed sources' state is untouched |
| 2 | Usage error | No fetch: bad source string, empty source set, `--interval < 1s`, negative `--max-new`, `--max-pages < 1`, invalid `--max-new-overflow`, `--json` without `--once`, `--json --ndjson` together, invalid config values |

Loop mode: exit 1 only for unrecoverable errors (state-store failure,
non-EPIPE write failure); SIGINT/SIGTERM exit 0.

## Agent etiquette

- Watch setup is stateful configuration: agree on sources, cadence,
  `--max-new` and the category's `--state-dir` with the user before writing a
  scheduler entry (see "Multi-category subscriptions" above when the user runs
  more than one category).
- Diagnose a suspicious watch run in this order: exit code → stderr lines /
  error envelopes → `nitter seen list --state-dir <dir>` (or the default
  location without the flag) → `nitter instances test` for instance health.
- To re-emit a source's history deliberately: clear its state
  (`nitter seen clear --source <key> --confirm`, consent required) or point
  watch at a fresh `--state-dir`, then run with `--include-existing`.
