# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

## Changed

- **State-store errors name the operation in the same form as every other error**: `watch`, `seen list`
  and `seen clear` reported a corrupt or unreadable state file with a prose prefix (`load seen state: …`)
  instead of the package-qualified operation every other classified error uses. It is now `seen.Load: …`
  (and `seen.Save: …` on the write path). Kinds, exit codes and the state-file semantics are unchanged.

## Deprecated

## Removed

## Fixed

- **`nitter download` reports a local disk failure as a local failure**: a failed write to the target
  file — a full disk, most often — surfaced as `upstream_unavailable` and sent the caller looking at
  the instance. It is now the `local_state_error` every other local filesystem failure on the same
  path already used. Exit codes are unchanged (both are exit 1).
- **The Nitter RSS scan follows the `Min-Id` cursor instead of reading one page**: a timeline whose backlog spanned
  more than one feed page was silently truncated — `--max-new` above one page could not be satisfied, and a first run
  recorded only the first page, so anything older was never emitted. The scan now pages until the feed runs out of
  cursors, the `--max-pages` budget is spent, or a page adds nothing the source has not already seen.
- **Multi-source watches can fetch one merged RSS request per batch**: with `--no-reposts` and two or more `user:`
  sources, the cycle requests `/{u1,u2,...}/rss` once (batched at 250 path characters) and splits the feed per author.
  The merged feed cannot represent a repost (Nitter attributes the item to the original author), which is why this
  only applies when reposts are filtered out; `fetch_backend = "fx"` never merges, and `"mix"` serves those sources
  from Nitter rather than the fast lane. A failed batch falls back to the per-source path.

## Security
