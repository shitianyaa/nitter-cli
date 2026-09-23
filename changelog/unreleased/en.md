# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- **`nitter update` installs releases**: after showing the version comparison it asks for confirmation
  (`--confirm` skips the prompt and is required when stdin is not a terminal, so a pipe is never blocked;
  declining exits 0). The installer downloads only the current platform's archive, verifies its SHA-256
  against the release's `checksums.txt`, checks the staged binary's reported version, and only then replaces
  the executable — any verification failure leaves the existing installation untouched. A `go install`
  installation is refused with the matching `go install` line rather than replaced, so a later install cannot
  silently undo the update. `--proxy`/`config.proxy` now apply to update traffic (they previously did not:
  the check built a proxy-free client).

## Changed

- **`nitter update` no longer only prints guidance**: without `--check` it performs the check and then offers to
  install. `nitter update --check` is unchanged and remains read-only.
- **`nitter download` reports its output directory**: one `note: writing to <dir>` line on stderr carrying the
  resolved absolute path, printed after the directory is created and before any file is written. stdout,
  `--json` and `--ndjson` are unaffected.

## Deprecated

## Removed

## Fixed

## Security
