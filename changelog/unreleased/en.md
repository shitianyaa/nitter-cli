# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- Add `nitter download --clean-temp <DIR> [--older-than DURATION]`: an explicit
  maintenance mode that removes files older than the window from a named directory
  (default `168h`; `0s` removes everything), prunes the subdirectories it emptied and
  reports on stderr. It never downloads and never runs automatically.

## Changed

## Deprecated

## Removed

## Fixed

## Security
