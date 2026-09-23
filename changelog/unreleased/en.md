# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- **Circle member profiles (`nitter circle refresh`)**: member metadata now lives in a machine-readable sidecar
  (`~/.nitter-cli/profiles.toml`), keyed by handle, split into machine-refreshed facts (`name`, `bio`,
  `followers_count`, `fetched_at`) and judgement that refreshes never overwrite (`role`, `note`, `noted_at`).
  `circle refresh <NAME>` fetches each member's profile and merges the fact fields; a failing member is skipped
  with a warning while the others continue, and its previous facts are kept.

## Changed

- **`nitter circle show` is now local-only**: it joins `circles.toml` against the cached sidecar and performs
  zero network requests, so `--min-followers` now filters the cached follower count instead of fetching each
  member's profile. Members with no cache keep their row with `-` placeholders plus a stderr note pointing at
  `circle refresh`; `--json` rows gain `name`, `bio`, `fetched_at`, `role` and `note`. Run
  `nitter circle refresh <NAME>` once to populate the cache.

## Deprecated

## Removed

## Fixed

## Security
