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

## Security
