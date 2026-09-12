# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- `twitter` CLI: fetch public tweets through your own Nitter instances — `twitter user` (RSS first, HTML fallback), `twitter search`, `twitter list` and `twitter get` (single status by ID or URL), with instance rotation, cooldown and pacing.
- `twitter watch`: persistent dedup polling of `user:`/`tag:`/`list:` sources with `--once` scheduler mode, per-source seen state (300 IDs + watermark 20), `--max-new` emission cap and per-source error reports.
- `twitter instances test`: capability probes (RSS / user HTML / search / list) per configured instance with human, `--json` and `--ndjson` reports.
- `twitter config path/get/set/unset`: manage the nine scalar config keys with env overrides (`TWITTER_DEFAULT_LIMIT`, `TWITTER_LOG_LEVEL`, `TWITTER_LOG_FORMAT`), atomic 0600 writes and automatic baseline config creation on first run.
- `twitter seen list/clear`: inspect and clear the watch dedup state (`seen list --json` always returns an array; `seen clear` requires `--confirm` every time).
- `--json` / `--ndjson` output modes for all data commands, backed by the stable `twitter.pipeline/v1` envelope protocol (kind `tweet` / `instance_report` / `error`), including in-place per-source error envelopes for `watch` and graceful EPIPE handling (exit 0 on a closed stdout pipe).
- Public Go SDK `github.com/shitianyaa/twitter-cli/sdk` (package `twitter`): `Client` with instance rotation and cooldown, narrow `Transport` interface, classified error kinds (`KindChallenge`, `KindRateLimited` with `RetryAfter`, `KindUnavailable`, …) and additive-only data models (`Tweet`, `Author`, `Media`, `Quoted`, `Page`, `InstanceReport`).
- Bilingual documentation (English + 简体中文): README, CLI reference, Go SDK guide and maintainers' architecture/development guides under `docs/`.
- Agent skill shipped with the repository (`skills/twitter-cli/`): operating rules, command tiers, quick reference and troubleshooting for driving the binary from an AI agent.
- `twitter update` with `--check [--prerelease] [--json]`: compares the installed version against the latest GitHub release by strict semver (prerelease-aware with `--prerelease`, drafts always excluded); without `--check` it prints package-manager / manual-download guidance. No self-install in the MVP; development builds skip the check.
- Field-level output filters for the data commands: `--no-reposts`, `--media-only` and `--media-type image|video|gif` on `user`/`search`/`list`/`watch` (freely combinable; an invalid `--media-type` value is a usage error). Filters apply after the fetch — in `watch` before dedup, so filtered tweets are never marked seen, are re-fetched each cycle without being re-emitted, and `--max-new` counts only filtered-through tweets.

## Changed

- `watch` help and `seen list` help now advertise the raw tag source form (`tag:#AI`, `tag:from:nasa`); the pre-escaped `tag:%23AI` form double-escapes on the wire and is documented as invalid.

## Deprecated

## Removed

## Fixed

## Security
