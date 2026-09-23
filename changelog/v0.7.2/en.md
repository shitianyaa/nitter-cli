# v0.7.2 — 2026-09-23

Reliable RSS pagination, merged multi-source fetches, and the documentation corrections that came with them.

## Added

- **Merged RSS fetching for multi-source watches**: with `--no-reposts` and two or more `user:` sources, a cycle
  now acquires them with ONE RSS request per batch (`/{u1,u2,...}/rss`, each batch kept under 250 path characters)
  and splits the feed per author, instead of one request per source. The merged feed cannot represent a repost
  (Nitter attributes the item to the original author), which is why this only applies when reposts are filtered
  out; `fetch_backend = "fx"` never merges, and `"mix"` serves those sources from Nitter rather than the fast lane.
  A batch whose request fails is not lost — the affected sources fall back to their own fetch.
  ([e0ac167](https://github.com/shitianyaa/nitter-cli/commit/e0ac167))

## Changed

- **State-store errors name the operation in the same form as every other error**: `watch`, `seen list` and
  `seen clear` reported a corrupt or unreadable state file with a prose prefix (`load seen state: …`) instead of
  the package-qualified operation every other classified error uses. It is now `seen.Load: …` (and `seen.Save: …`
  on the write path). Kinds, exit codes and the state-file semantics are unchanged.
  ([cf050f5](https://github.com/shitianyaa/nitter-cli/commit/cf050f5))
- **The documented `mix` fetch routing matches measured behavior**: the FAQ listed `comments`, `following`,
  `profile`, `quotes`, `trends` and `search --type user` among the commands that fall back to your own instances
  on failure, and described `nitter` mode as keeping every fetch self-hosted. Those six commands have no Nitter
  equivalent and always reach `api.fxtwitter.com`, and `fetch_backend` only routes the timeline and status
  surfaces. The text now states that split. ([b526158](https://github.com/shitianyaa/nitter-cli/commit/b526158))

## Fixed

- **The Nitter RSS scan follows the `Min-Id` cursor instead of reading one page**: a timeline whose backlog spanned
  more than one feed page was silently truncated — `--max-new` above one page could not be satisfied, and a first
  run recorded only the first page, so anything older was never emitted. The scan now pages until the feed runs out
  of cursors, the `--max-pages` budget is spent, or a page adds nothing the source has not already seen.
  ([564bf1b](https://github.com/shitianyaa/nitter-cli/commit/564bf1b))
- **`nitter download` reports a local disk failure as a local failure**: a failed write to the target file — a full
  disk, most often — surfaced as `upstream_unavailable` and sent the caller looking at the instance. It is now the
  `local_state_error` every other local filesystem failure on the same path already used. Exit codes are unchanged
  (both are exit 1). ([7b271b2](https://github.com/shitianyaa/nitter-cli/commit/7b271b2))

**Full Changelog**: [v0.7.1...v0.7.2](https://github.com/shitianyaa/nitter-cli/compare/v0.7.1...v0.7.2)
