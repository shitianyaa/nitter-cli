# nitter CLI reference

[Documentation](../index.md) · [简体中文](../zh-CN/cli-reference.md)

This is the public command contract for the `nitter` binary. Run
`nitter <command> --help` before automating a command; the help text of the
installed binary is the source of truth for the exact flags it accepts.

## Global options

Every command accepts these persistent options:

| Option | Meaning |
| --- | --- |
| `--proxy URL` | Proxy for this invocation (`http`, `https`, `socks5`, `socks5h`). Precedence: flag > config `proxy`. When both are empty the environment variables `HTTPS_PROXY`/`ALL_PROXY` apply to the FxTwitter fast lane and to `update`, but **not** to the nitter transport — use `--proxy` or `config proxy` to proxy instance traffic. |
| `--instance URL` | Nitter instance URL for this invocation. It **replaces the whole configured instance set** with this single URL and pins the invocation to the instance path, skipping the Fx fast lane — the way to exercise one instance. It applies only to the commands that have an instance path (`user`, `search`, `get`, `list`); the Fx-only commands (`followers`, `thread`, `typeahead`, `comments`, `following`, `profile`, `quotes`, `trends`, `search --type user`) ignore it. The override carries no basic-auth credentials, so a credential-protected instance will answer 401. It is not a routine flag: `fetch_backend` chooses the routing, and this only overrides the instance set for one call. An invalid proxy scheme or instance URL fails as a usage error (exit 2) before anything runs. |

`nitter --version` prints `nitter version <version>`; a bare `nitter` prints
help. An unknown subcommand exits 1 (not 2).

## Data flow and privacy

`fetch_backend` decides which service answers, and that decides what leaves your
machine. Under the default `mix`:

- `user`, `search` and `get` try **`api.fxtwitter.com`** — a third-party public
  service — first: the handle, query or status ID goes there, with **no
  credentials of yours attached**. On failure they fall back to your own
  instances.
- `followers`, `thread`, `typeahead`, `comments`, `following`, `profile`,
  `quotes`, `trends`, `search --type user` and `circle refresh` have no Nitter
  equivalent, so they always reach `api.fxtwitter.com`, whatever `fetch_backend`
  says. `circle add` sends the newly added member's handle to
  `api.fxtwitter.com` as well, for its best-effort profile-facts fetch (a
  failure there is only a stderr warning).
- `list` always runs on your own instances (FxTwitter has no List endpoint).

`fx` removes the fallback (fast lane only, its failures surface as errors);
`--instance URL` pins one call to a single instance, for the four commands that
have an instance path. Basic-auth credentials are host-scoped inside the
transport: a credential is attached only to requests addressed to its own
configured instance, and never enters errors, logs or responses. The
third-party media resolvers used by `media`/`download` receive the media URL,
never your credentials.

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
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2102761519985332442","data":{"id":"2102761519985332442","url":"https://x.com/NASA/status/2102761519985332442","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800}],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

`meta` carries provenance: `source` (the command input as a `kind:ref` key),
`instance` (the base URL of the instance that produced the batch) and
`fetched_at` (RFC3339 UTC). Empty `meta` fields are omitted. The `kind` enum is
additive-only in v1; the currently emitted kinds are `tweet` (data commands),
`instance_report` (`instances test --ndjson`), `media` (`media --ndjson`),
`download` (`download --ndjson`) and `error` (per-source fetch failures of
`watch`, per-ref failures of `media` and `download`):

```json
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"chooser: upstream_unavailable: no instances configured (fx attempt: fxtwitter.FetchUserTimeline: not_found: resource not found (404): /2/profile/NASA/statuses)"},"meta":{"input":"user:NASA"}}
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
- `--limit` caps the number of tweets; when omitted the config `default_limit`
  (default 20) applies.
- `--max-pages` caps pagination; when omitted the config `max_pages` (default 5)
  applies.
- Both are caps and **must be >= 1 when given**: `0` and negative values are
  usage errors (exit 2). There is no "unlimited" value — for a deep backlog
  state a large `--limit` and a large `--max-pages`.
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
configured Nitter instances on failure. Passing `--instance URL` replaces the
instance set for this call and pins it to the instance path, skipping the fast
lane entirely.

The Nitter RSS feed is read page by page along its `Min-Id` cursor, so a
backlog spanning several feed pages is fetched instead of being cut off at the
first one: `--limit N` keeps paging until N tweets or the `--max-pages` budget
is reached (a feed that advertises no `Min-Id` — a plain RSS proxy — stays a
single page). The budget is **per layer**: the RSS scan and the HTML fallback
each count their own pages, so a scan that spends its budget and still yields
nothing leaves the fallback a fresh budget.

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

## nitter followers

```bash
nitter followers <HANDLE> [--limit N] [--json|--ndjson]
```

Fetches the accounts following `HANDLE` — the follower list, the counterpart of
`following` — via FxTwitter API v2.
- Renders as formatted table on TTY: `@<handle>  <name>  <followers>  <bio>`.
- Emits single-line `nitter.pipeline/v1` (`kind: "profile"`) NDJSON envelopes in pipe mode (`meta.source` is `followers:<handle as typed>`, `meta.instance` is `FxTwitter`).
- `--json` outputs the array of `Profile` objects (`[]` when empty).
- `--limit` caps the number of profiles to fetch (default: 20; must be >= 1 — there is no unlimited value).

This capability always queries the FxTwitter fast lane regardless of
`fetch_backend`; `--instance` does not apply. `HANDLE` is 1–15 letters, digits
or underscores, without the `@` — a bad shape (or any extra argument) exits 2
before any network; a fetch failure exits 1 with the classified error message.

## nitter comments

```bash
nitter comments <STATUS_ID_OR_URL> [--sort likes|recency] [--limit N] [--json|--ndjson]
```

Fetches the root tweet, context thread chain, and user replies for a status.
- Essential for extracting author self-replies with hidden download links or reading long multi-part threads.
- `--sort` selects reply ordering (`likes` default or `recency`).

## nitter thread

```bash
nitter thread <TWEET_ID> [--json|--ndjson]
```

Fetches the full self-thread containing `TWEET_ID` and prints one row per
status, root post first: `<ID>  <YYYY-MM-DD HH:MM>  @<handle>  <single-line text>`.

`TWEET_ID` must be the numeric status ID (1–20 digits); URL and other reference
forms belong to the `get` command — a non-numeric ID (or any extra argument)
exits 2 before any network. A status that is not part of a thread answers
`not_found` and exits 1 with the classified error message. This capability
always queries the FxTwitter fast lane regardless of `fetch_backend`;
`--instance` does not apply.

- Emits single-line `nitter.pipeline/v1` (`kind: "tweet"`) NDJSON envelopes in pipe mode (the status ID as `id`, `meta.source` is `thread:<id>`, `meta.instance` is `FxTwitter`).
- `--json` outputs the array of tweet objects (`[]` when empty).
- An empty thread is a success (exit 0): it prints nothing on stdout in NDJSON mode and the `(empty)` hint on stderr in the default mode.

## nitter circle

```bash
nitter circle list [--json]
nitter circle show <NAME> [--json] [--min-followers N]
nitter circle refresh <NAME>
nitter circle suggest <HANDLE> [--limit N] [--min-followers N] [--json]
nitter circle add <NAME> <HANDLE>
nitter circle remove <NAME> <HANDLE>
nitter circle run <NAME> [--limit N] [--media-only] [--media-type image|video|gif] [--json|--ndjson]
```

Manages and traverses curated creator circles in `~/.nitter-cli/circles.toml`.
- `list`: lists configured circles with user counts.
- `show`: lists members joined with their cached profiles in `~/.nitter-cli/profiles.toml`. **Local only — zero network requests.** Each member renders as `@<handle>\t<followers>\t<role>\t<name>\t<bio>\t<age>` (the bio is flattened to one line and truncated at 120 bytes); absent data renders as `-` rather than dropping the row.
  - **`--min-followers N`**: keeps only members whose **cached** follower count is at least N. A member with no cache has no verified count and is excluded by the filter, but is still counted in the stderr note. `N < 0` is a usage error (exit 2).
  - Members without a cache keep their row with `-` placeholders, and a `note: <N> member(s) have no cached profile; run 'nitter circle refresh <NAME>'` line goes to stderr (whether or not `--min-followers` filtered them out). Run `refresh` once to populate the cache.
  - `--json` emits an array of `{handle, name, bio, followers_count, fetched_at, role, note}` in roster order; as in the human rows, `bio` is flattened to one line and truncated to 120 bytes. An empty `fetched_at` means never fetched; consumers derive freshness from it (`noted_at` and a derived age are deliberately not projected).
- `refresh`: fetches each member's profile and merges the results into the sidecar `~/.nitter-cli/profiles.toml`. It is the **only bulk** refresher of that file and the only command that fetches profiles for a whole circle (`circle add` performs a best-effort single-member fetch at add time — see below; `circle remove` never reads nor writes the file).
  - It writes only the fact fields (`name`, `bio`, `followers_count`, `fetched_at`) and **never** touches `role`, `note` or `noted_at` — judgement recorded there survives every refresh.
  - A member whose fetch fails is skipped with a `warning:` on stderr while the rest continue, and its existing entry keeps its previous facts (no empty overwrite, and no entry is created for it). All members failing exits 1; an unknown circle exits 1; an empty circle prints `(empty)`, exits 0 and creates no file.
  - Prints `refreshed N/M members in circle <key>`. It never prunes: sidecar entries for handles no longer in the circle are left alone.
  - The sidecar is keyed by lowercase handle and is machine-managed — a rewrite drops comments and unlisted fields, so only edit `role`/`note` by hand.
- `suggest`: read-only candidate discovery for building a new circle, merging two existing data lanes — `HANDLE`'s **following list** (static relation) and the **authors of the retweets** in its timeline (behavioral relation). Output is ranked by co-occurrence count (a handle appearing in both lanes ranks first), then by follower count descending, then handle ascending (deterministic).
  - `--limit N` (default 20, **must be >= 1**): per-lane fetch cap. `--limit 0` is a usage error (exit 2), as on every command; here it is doubly so, because the two lanes would give `0` opposite, useless meanings (timeline: empty; following: one server-side page). The following lane ignores `limit`/`count` upstream and returns one page of ~50–67 accounts regardless, so the client truncates; the actual count may therefore be below `N`.
  - `--min-followers N` (default 0): filters only the trailing `top matches` summary section, never the main table or `--json` output.
  - The seed must exist (a `profile` probe runs first; a missing seed exits 1). A lane failure degrades with a stderr warning while the other lane still produces candidates; both lanes failing exits 1. A retweet-only candidate whose profile fetch fails is skipped with a stderr warning.
  - Human output: a stats header, the ranked table (`@<handle>\t<followers>\t<bio one line>\t<source>` where source is `following`, `retweet` or `both`), then a `top matches (>= N followers)` summary. `--json` emits an array of `{handle, followers_count, bio, source}` objects. `suggest` never writes to the circle file — use `circle add` to commit the handles you pick.
- `add`: adds handle to a circle (creates file/circle on demand). After the roster write succeeds it best-effort fetches the new member's profile facts: the handle goes to `api.fxtwitter.com` (that fetch ignores `--instance` and always takes the FxTwitter fast lane, even on an `--instance`-pinned call), and only the fact fields (`name`, `bio`, `followers_count`, `fetched_at`) are merged into the sidecar — judgement (`role`/`note`/`noted_at`) is never touched. Every failure on that path is a `warning:` on stderr only: the roster write stands, the command still exits 0, and the facts arrive with the next `circle refresh`.
- `remove`: removes handle from a circle, editing **only** the circle's users array in `circles.toml`. The profile sidecar `~/.nitter-cli/profiles.toml` — recorded `role`/`note` included — is never read nor written, so removing a member keeps their cached profile. The removal is idempotent: a handle that is not a member prints `circle remove: @<handle> is not a member of <key>; nothing changed` on stderr and exits 0 (the notice is the honesty, never silence). Success prints `removed @<handle> from circle <key>`; an unknown circle exits 1 and a bad handle shape exits 2.
- `run`: traverses and streams latest tweets for all creators in the circle.
  - `--limit N` (default 20; must be >= 1): caps the number of tweets fetched per creator.
  - `--media-type image|video|gif` keeps only tweets carrying at least one media entry of that type (an invalid value is a usage error; the semantics match the `user` command's `--media-type`).
  - **Per-member failures are partial, not fatal**: a member whose timeline fetch fails is skipped with a `warning: circle run: @<handle>: …` on stderr while the rest still emit, so **exit 0 can mean partial output**. All members failing exits 1; an unknown circle exits 1; an empty circle prints `(empty)` on stderr (nothing under `--ndjson`, `[]` under `--json`) and exits 0.
  - **Snapshot semantics**: every run re-fetches each member's latest tweets from scratch with no incremental state — the same circle and limit can return overlapping result sets between runs; use `watch` for incremental tracking of new tweets.
  - **Page budget**: `run` takes no `--max-pages` flag; each member's fetch uses the config `max_pages` (default 5). Raise it in the config if a member's timeline needs more pages (that also raises `watch`'s per-cycle budget).
  - **Deterministic order**: under the Fx fast lane results are sorted by tweet ID descending (timeline order) before `--limit` truncates, so the same input produces the same output sequence even when the upstream page composition fluctuates between runs.
  - **Filter marker**: when `--media-only` or `--media-type` is in effect, NDJSON envelopes carry `meta.filter` (`"media_only"` or the media-type value) so consumers can verify filtering; without a filter the key is absent.
  - **Media-endpoint pagination**: `--media-only` fetches through the Fx media endpoint with a doubled per-page count to compensate for non-media entries — the result set may therefore reach deeper into the member's timeline than the plain fetch (documented behavior, not an error).

## nitter profile

```bash
nitter profile <HANDLE> [--json|--ndjson]
```

Fetches user profile card and metadata for `HANDLE`.
- Renders formatted user card on TTY: handle (with the name in parentheses when present), bio, follower count, following count, tweet count, media count, avatar, banner, and protected status. Followers, following and tweets are always printed; `Bio`, `Media`, `Avatar`, `Banner` and `Protected` appear only when the source carries them.
- `tweets_count` is the source's status count (FxTwitter's `statuses` field); it is `0` when the source carries no count — never fabricated.
- Emits single-line `nitter.pipeline/v1` (`kind: "profile"`) NDJSON envelope in pipe mode.
- `--json` outputs the Profile JSON object.

## nitter quotes

```bash
nitter quotes <REF> [--limit N] [--media-only] [--no-reposts] [--json|--ndjson]
```

Fetches quote tweets for a tweet status ID or URL.

An empty result and an upstream failure are told apart. The quote endpoint answers 404 both for "this tweet has no quotes" and for a real failure, so the command cross-checks the tweet's own quote count: a count of zero prints the empty result and exits 0, while a positive or unreadable count reports `not_found` and exits 1. That failure goes to **stderr with exit 1** — like every fetch failure, it is not wrapped in a `kind: "error"` NDJSON envelope, so an `--ndjson` consumer sees no stdout line at all and must check the exit code.
- Renders tweet rows on TTY: `<ID>  <YYYY-MM-DD HH:MM>  @<handle>  <text>`.
- Emits single-line `nitter.pipeline/v1` (`kind: "tweet"`) NDJSON envelopes in pipe mode with `meta.source` set to `quotes:<id>`.
- `--limit`: caps number of quotes to fetch (default: 20; must be >= 1).
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
- `--limit`: caps number of trends to display (default: 20; must be >= 1).
- `--json`: outputs array of `Trend` objects.

## nitter search

```bash
nitter search <QUERY> [--type tweet|user] [--sort latest|top] [--limit N] [--max-pages N] \
  [--no-reposts] [--media-only] [--media-type image|video|gif] [--json|--ndjson]
```

Runs `QUERY` against the configured instances or FxTwitter.
When `--type tweet` (default), the query is passed through to
Nitter unchanged (URL-escaped once by the HTTP layer), so Nitter's own query
syntax applies: a leading `#` searches a hashtag, `from:user` a user's posts,
anything else is a plain phrase search. An empty (whitespace-only) query exits 2.
NDJSON `meta.source` is `search:<query as typed>`.
On the FxTwitter lane (`fetch_backend = fx`, or `mix` when the fast lane answers)
the search endpoint is fetched exactly once, so `--max-pages` does not apply
there and a `--limit` above 100 cannot be reached — the single request returns
at most 100 items. The instance path paginates as documented, and there search is
a separate Nitter capability that a given instance may have disabled or serve
slowly — probe it with `nitter instances test --full` before blaming the query.

`--sort latest|top` (default `latest`) selects the result ordering and is
carried by BOTH backends — Nitter's `f=` parameter (`tweets` for `latest`,
`top` for `top`) and FxTwitter's `feed` — so a mix-mode fallback never
answers with a different ordering than the one requested, and every
pagination page keeps it. The value is case- and whitespace-insensitive; any
other value exits 2 (never a silent fallback to the default). `--sort` is
rejected together with `--type user` (exit 2) — profile search has no
ordering.

When `--type user`, searches for user profiles, artists, and creators matching the query:

A query with no matches answers 200 with an empty list (exit 0); a 404 from the upstream user-search endpoint is a failure and is reported as `not_found` (exit 1), not as an empty result. Like every fetch failure it goes to stderr only — `--ndjson` emits no `kind: "error"` envelope, so check the exit code.
- Renders user table on TTY: `@<handle>  <name>  <followers>  <bio>`.
- Emits `kind: "profile"` NDJSON envelopes in pipe mode.
- `--json` outputs Profile objects.

The same field filters apply as on `user`: `--no-reposts`, `--media-only`,
`--media-type image|video|gif` (applied after the fetch, before output).

```bash
nitter search "#AI" --sort top --limit 10 --json   # popular results instead of newest-first
```

## nitter typeahead

```bash
nitter typeahead <QUERY> [--limit N] [--json|--ndjson]
```

Suggests X accounts matching `QUERY` through the user-completion endpoint and
prints one row per profile: `@<handle>  <name>  <followers>  <bio>`.

This is a completion lookup, NOT search: no query operators, the upstream
answers at most ~10-20 accounts and there is no pagination. Use `search` for
real queries. An empty (whitespace-only) query exits 2 before any network.

This capability always queries the FxTwitter fast lane regardless of
`fetch_backend`; `--instance` does not apply.

- `--limit` caps the number of profiles to print (default: 20; must be >= 1 — the upstream may answer fewer).
- Emits single-line `nitter.pipeline/v1` (`kind: "profile"`) NDJSON envelopes in pipe mode (`meta.source` is `typeahead:<query as typed>`, `meta.instance` is `FxTwitter`).
- `--json` outputs the array of `Profile` objects (`[]` when empty).

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

Fetches one single status. In `mix` (default) the FxTwitter fast lane is tried first and falls back smoothly to Nitter instances on failure; in `fx` mode the fast lane is the only permitted source, so its error surfaces as it is — there is no fallback to mask it.
`REF` is a bare numeric status ID, or a status URL —
`x.com`, `twitter.com` or any Nitter instance, shape `<user>/status/<id>`; the
user segment is optional (Nitter serves `/status/<id>` directly) and `/photo/N`
and `/video/1` suffixes are accepted. A positional `REF` always wins; only
with no argument AND a non-TTY stdin is the reference read from one stdin
line. A positional `REF` never reads stdin, so `nitter get <REF>` cannot
block on a pipe whose writer stays open. There are no pagination flags. The
quoted tweet, when present,
is summarized in the `quote` field (visible in `--json`/`--ndjson`); interaction
counts are not reported — none are fabricated. NDJSON `meta.source` is
`status:<numeric ID>`. When served by FxTwitter, `meta.instance` is recorded as `FxTwitter`.

Example (`--json` prints exactly one object for the single status; shape
illustrative):

```json
{"id":"2102761519985332442","url":"https://x.com/NASA/status/2102761519985332442","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}
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
a batch; positional REFs always win — only with no argument AND a non-TTY
stdin are the references read from stdin (one per non-empty line). A
positional REF never reads stdin, so a batch cannot block on a pipe whose
writer stays open. The download itself is the caller's job — the
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
https://x.com/NASA/status/2102761519985332442	fx	video	https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4	-	3.2	24000
```

Columns: `ref source kind url label duration size` — ref echoes the input
reference; the trailing cells are `-` when absent. `--json` prints a single
JSON object when exactly one media entry resolved across the whole
invocation, an array otherwise, `[]` when nothing resolved. `--ndjson`
prints one envelope per media entry (`kind` `media`, the download URL as
`id`, the MediaResolution as `data`, `meta.input` = the raw ref) and one
`kind:"error"` envelope per failed ref, in ref order:

```json
{"schema":"nitter.pipeline/v1","kind":"media","id":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","data":{"ref":"https://x.com/NASA/status/2102761519985332442","source":"fx","kind":"video","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","variants":[{"url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","bitrate":2176000}]},"meta":{"input":"https://x.com/NASA/status/2102761519985332442"}}
```

Exit codes: success 0 (probing failures do not count); a per-ref resolution
failure gets an in-place error report (error envelope on the NDJSON stream,
`error: <ref>: <message>` on stderr otherwise) while the other refs continue,
and the run exits 1 with a `media completed with N of M refs failed` summary
when at least one ref failed; usage problems (`--json` with `--ndjson`,
invalid `--strategy` or `--quality`, bad/missing refs) exit 2.

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
suffixes accepted). Multiple REFs run as a batch. Positional REFs always win;
only with no argument AND a non-TTY stdin is the input read from stdin — when
the first non-whitespace byte is `{`, every non-empty line must be a strict
`nitter.pipeline/v1` tweet envelope and each record's `data.url` is used as
the REF (the `nitter get --ndjson` and `nitter watch --ndjson` streams feed
download directly; a malformed envelope is a usage error), otherwise every
non-empty line is a plain REF. A positional REF never reads stdin, so a batch
cannot block on a pipe whose writer stays open.

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
directory); it is created on demand (`mkdir -p`). The resolved **absolute**
path is reported once on stderr before anything is written
(`note: writing to <dir>`) — informational, never a gate, and it never
appears on stdout (which stays a clean machine contract for `--json` and
`--ndjson`).

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
https://x.com/NASA/status/2102761519985332442	/home/you/nitter-media/2102761519985332442-1.mp4	24000000	video	fx
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
{"schema":"nitter.pipeline/v1","kind":"download","id":"/home/you/nitter-media/2102761519985332442-1.mp4","data":{"ref":"https://x.com/NASA/status/2102761519985332442","path":"/home/you/nitter-media/2102761519985332442-1.mp4","kind":"video","source":"fx","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","bytes":24000000,"sha256":"…"},"meta":{"input":"https://x.com/NASA/status/2102761519985332442"}}
```

Exit codes: success 0 (an invocation that plans and downloads nothing prints
`(empty)` on stderr; a consumer closing the stdout pipe early is a clean
stop); a per-ref failure gets an in-place error report (error envelope on
the NDJSON stream, `error: <ref>: <message>` on stderr otherwise) while the
other refs continue, and the run exits 1 with a
`download completed with N of M refs failed` summary when at least one ref
failed; usage problems (unknown `--kind`/`--quality`/`--strategy`/
`--on-exists`, bad or missing refs, malformed stdin envelopes, `--json` with
`--ndjson`) exit 2.

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

Manages the thirteen scalar keys of `~/.nitter-cli/config.toml` (TOML, mode
0600). Precedence: CLI flag > environment > file > built-in default. The
`[[instances]]` and `[[watch.sources]]` array tables are managed by editing the
file directly — `config set` refuses them. Creator circles (the `nitter circle`
rosters) live in a separate file, `~/.nitter-cli/circles.toml`, managed by the
`circle` subcommands.

| Key | Type | Default | Env override | Meaning |
| --- | --- | --- | --- | --- |
| `default_limit` | int | `20` | `NITTER_DEFAULT_LIMIT` | Item cap when a command receives no `--limit` |
| `max_pages` | int | `5` | — | Upper bound on pagination |
| `request_interval` | duration | `1s` | — | Global floor on delay between request start times |
| `retry_attempts` | int | `2` | — | Extra attempts on network errors and 5xx |
| `retry_delay` | duration | `1s` | — | Linear backoff base: attempt n waits `retry_delay × n` |
| `instance_cooldown` | duration | `60s` | — | How long an instance is skipped after a failure (429 / network error) |
| `fetch_backend` | enum | `mix` | `NITTER_FETCH_BACKEND` | `mix` (default, FxTwitter fast lane with Nitter fallback) or `fx` (pure FxTwitter). The removed `nitter` value is rejected; to send one call to your own instances, `--instance URL` (on `user`, `search`, `get`, `list`) |
| `proxy` | string | `""` | — | Proxy URL (`http(s)`, `socks5(h)`); empty = no configured proxy — `HTTPS_PROXY`/`ALL_PROXY` apply to the FxTwitter fast lane and `update` only, **not** to the nitter transport |
| `log_level` | enum | `info` | `NITTER_LOG_LEVEL` | `debug` or `info`; diagnostics go to stderr only, stdout stays pure data |
| `log_format` | enum | `text` | `NITTER_LOG_FORMAT` | `text` or single-line `json` |
| `download_path` | string | `./nitter-media` | — | Where `nitter download` writes media files (cwd-relative; created on demand; overridden per call by `download --output DIR`) |
| `filename_template` | string | `{id}-{seq}` | — | Filename template for non-cover media, placeholders `{id}` `{seq}` `{user}` `{kind}` `{ext}` (default = legacy `<id>-<seq>.<ext>` naming; covers are always `<id>-cover.<ext>`; overridden per call by `download --filename-template`; an invalid template warns and falls back to the default) |
| `directory_template` | string | `""` | — | Subdirectory under `download_path`, placeholders `{id}` `{user}` `{kind}` (`/` separates nesting; `{seq}`/`{ext}` forbidden; empty = flat) |

`default_limit` and `max_pages` are caps and must be `>= 1`: `0` is not a
spelling for "unlimited". Environment variables win over the file:
`NITTER_DEFAULT_LIMIT` (integer), `NITTER_LOG_LEVEL`, `NITTER_LOG_FORMAT`,
`NITTER_FETCH_BACKEND`.

Array tables (hand-edited):

```toml
[[instances]]
url = "http://nitter.internal:8080"   # required
username = ""                         # optional basic auth (see note)
password = ""

[[watch.sources]]                     # default sources for `nitter watch`
id = "user:NASA"                      # user:<handle> | tag:<query> | list:<id>
```

When an `[[instances]]` entry sets **both** `username` and `password`, requests
to that instance carry HTTP basic auth. The credential policy is host-scoped
inside the transport — a credential is only ever attached to a request addressed
to its own configured instance, so the third-party media endpoints
(`media`/`download` resolvers such as fx/xdown and twimg) can never receive it;
credentials also never enter errors, logs, or responses. An incomplete pair
(only one half set) is treated as unconfigured. A one-off `--instance URL`
override is a plain URL and carries **no** credentials — for a credentialed
instance, use the config entry. Put instances you cannot credential behind your
own network-layer access control instead.

- `config path` prints the config file path. Takes no arguments (else exit 2).
- `config get` without a key prints all thirteen keys as `key = value`; with a
  key it prints that one. Unknown keys are rejected (exit 2) before the file
  is read.
- `config set KEY [VALUE]` validates and coerces the value **before any disk
  write** (integers `>= 1` for `default_limit`/`max_pages` — they are caps, and
  `0` is not a spelling for "unlimited"; `retry_attempts` is `>= 0`;
  durations `>= 0` for `request_interval`/`retry_delay`/`instance_cooldown`;
  `fetch_backend` is `mix|fx` (`nitter` was removed — the instance path is
  selected per call with `--instance`, on the commands that have one);
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

**Merged fetching** — with `--no-reposts` and two or more `user:` sources,
one cycle acquires those sources with ONE RSS request per batch
(`/{u1,u2,...}/rss`, each batch kept under 250 path characters) and splits the
feed per author, instead of fetching every source separately. The merged feed
is Nitter-only and cannot represent a repost — Nitter attributes a reposted
item to the original author — which is exactly why merging requires
`--no-reposts`; `fetch_backend = "fx"` never merges, and `"mix"` serves those
sources from Nitter rather than the fast lane. A batch whose request fails is
not lost: the affected sources fall back to their own fetch.

**Flags**

| Flag | Default | Meaning |
| --- | --- | --- |
| `--once` | off | Run exactly one cycle and exit — the recommended scheduler form. |
| `--interval D` | `10m` | Loop sleep between cycles without `--once`; must be a duration `>= 1s` (validated in both modes). |
| `--max-new N` | `10` | Emit at most N new tweets per source per cycle (newest first). `0` emits nothing and seals the current first page as the new baseline; negative is a usage error. |
| `--max-new-overflow drop\|keep` | `drop` | What happens to new tweets beyond the `--max-new` cap in one cycle. `drop` marks the excess seen immediately — never re-emitted (宁丢勿重). `keep` leaves it unseen so the next cycles re-emit it under the same cap (宁重勿丢; a burst larger than twice the cap drains over several cycles). Another value is a usage error; `--max-new 0` always rebuilds the baseline regardless. |
| `--max-pages N` | config `max_pages` (5) | Fetch-page budget per cycle; when given it must be >= 1 (`0` and negatives are usage errors), and omitting it applies the config value. |
| `--include-existing` | off | Emit the whole first fetch on an uninitialized source (default: first run only records state). |
| `--state-dir DIR` | `~/.nitter-cli/state` | Directory holding `seen.json` (created if missing). Give each subscription category its own directory — see "Multi-category subscriptions" below. |
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
- An uninitialized `user:` source initializes even when its first fetch comes
  back empty, so the next cycle emits its new tweets; `tag:` and `list:` sources
  stay uninitialized until a cycle that returns at least one tweet, so a
  transient empty first response is never mistaken for "caught up".
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

**Multi-category subscriptions — one `--state-dir` per category.** A
subscription *category* is a group of sources that share a purpose, a cadence
or a push channel. Give each category its own
`--state-dir ~/.nitter-cli/state/<category>` and pass that same flag on every
scheduler line of the category; a line that omits it falls back to the default
`~/.nitter-cli/state`, which re-couples the categories. State is keyed by
source key inside one `seen.json`, so two categories subscribed to the same
account share the key `user:HANDLE`: the category whose cycle runs first marks
the tweet seen and the other one drops it silently — no error, `--once` still
exits 0. Separate directories also make concurrent runs independent: the store
serializes writes only within one process, so two jobs firing against the same
`seen.json` read-modify-write the same file and the last writer wins. Scope
`seen list`/`seen clear` with the same flag to inspect or reset one category.

Example — a scheduler entry consuming the NDJSON stream (illustrative):

```bash
nitter watch user:NASA tag:#AI --once --ndjson --max-new 50
```

To prefer re-delivery over loss on bursts, add `--max-new-overflow keep`:

```bash
nitter watch user:NASA tag:#AI --once --ndjson --max-new 50 --max-new-overflow keep
```

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2102761519985332442","data":{…},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"…"},"meta":{"input":"tag:#AI"}}
```

`--once --json` prints the whole cycle as one document instead (illustrative):

```json
{"tweets":[{"id":"2102761519985332442","url":"…","text":"…","author":{…},"published_at":"2026-09-12T08:00:00Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}],"errors":[{"ref":"tag:#AI","code":"upstream_unavailable","message":"…"}]}
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
nitter update [--confirm] [--proxy URL]
nitter update --check [--prerelease] [--json] [--proxy URL]
```

Checks the latest release on GitHub and, when a newer one exists, offers to
install it. The install path downloads **only this platform's archive**,
verifies its SHA-256 against the release's `checksums.txt`, checks that the
staged binary reports the expected version, and only then replaces the
executable. **Every failure before that replacement leaves the current
installation untouched** — nothing touches the target until all checks pass.
The replacement itself is not covered by that guarantee: if it fails, verify
the installed binary (on Windows also look for `<exe>.old`) before retrying.

- Bare `nitter update` compares versions and then asks
  `install now? [y/N]` (default No) on a terminal. Without `--confirm` and
  without a terminal it prints the comparison plus
  `not installed: stdin is not a terminal — re-run with --confirm` and exits 0
  — it never blocks reading a pipe, and declining is **not** an error. The
  report is `current version:` / `latest release:` / `up to date` (or
  `update available:` / `installed <version> → <path>`).
- `--confirm` installs without asking. It is required whenever stdin is not a
  terminal, so scripts opt in explicitly. Passing it with `--check` is a usage
  error (exit 2): the two modes are mutually exclusive.
- `--check` compares the installed version against the latest release of
  `github.com/shitianyaa/nitter-cli` via the GitHub Releases API and **never
  writes anything**. Drafts are always excluded; `--prerelease` admits
  prereleases into the "latest" selection. Selection is by strict semver
  precedence (optional `v` prefix, build metadata ignored, spec prerelease
  ordering) over the first API page, not by recency. Exit 0 on every
  successful check — outdatedness is a reported result, not a failure:
  `update available: <version> (<release URL>)` versus `up to date`. The
  installed version being newer than the latest release (e.g. an installed
  prerelease) counts as up to date.
- `--json` (only with `--check`) prints one JSON document with every key
  present: `{"current":"0.1.0","latest":"0.2.0","outdated":true,"prerelease":false,"release_url":"…"}`.
  On a development build: `{"current":"dev","development_build":true}`.
- **`go install` installations are refused**, never replaced: a later
  `go install` would silently undo the update, so the command exits 1 with the
  matching `go install github.com/shitianyaa/nitter-cli/cmd/nitter@<tag>` line.
  An installation whose source cannot be determined exits 1 with a pointer to
  the release page.
- Development builds (compiled without version metadata) skip the check
  entirely and exit 0: there is no release to compare `dev` against, and
  nothing to replace.
- **Proxy**: `--proxy` (or `config.proxy`) applies to every request this
  command makes — the release lookup and the asset downloads. An unsupported
  proxy scheme is a usage error (exit 2) before any network call. When
  `--proxy` and `config proxy` are both empty, `update` honors the
  `HTTPS_PROXY`/`ALL_PROXY` environment variables.
- A failed check — network failure, GitHub error (HTTP status only; response
  bodies are never echoed), or no usable release — is a runtime failure
  (exit 1). A verification failure (checksum mismatch, malformed archive, or a
  staged binary reporting the wrong version) is also exit 1, with the existing
  installation unchanged.
- `--json` and `--prerelease` are only valid together with `--check`
  (otherwise a usage error, exit 2).
- **Agents must not run `nitter update` without the user's authorization.**
  `--confirm` is a mechanism for the user's own scripts, not a grant of
  permission.
