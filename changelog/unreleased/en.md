# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

## Changed

- Clarified authorized instance maintenance, scalar-only config commands, and
  tweet-reference inputs for media downloads in the operator skill. ([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

## Deprecated

## Removed

## Fixed

- Existing configuration can be read from a read-only directory without creating
  temporary config files. First-run initialization remains atomic and does not
  overwrite existing configuration. ([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

## Security
