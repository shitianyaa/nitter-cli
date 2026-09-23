# v0.7.1 — 2026-09-23

Self-updating installs, cached circle profiles, and the fixes found while verifying both.

## Added

- **`nitter update` installs releases**: after showing the version comparison it asks for confirmation
  (`--confirm` skips the prompt and is required when stdin is not a terminal, so a pipe is never blocked;
  declining exits 0). The installer downloads only the current platform's archive, verifies its SHA-256
  against the release's `checksums.txt`, checks the staged binary's reported version, and only then replaces
  the executable — any verification failure leaves the existing installation untouched. A `go install`
  installation is refused with the matching `go install` line rather than replaced, so a later install cannot
  silently undo the update. On Windows the previous binary is kept as `<name>.exe.old` and removed by the next
  invocation; on Unix the rename is atomic and leaves nothing behind. `update --check` remains read-only.
  ([13f13b4](https://github.com/shitianyaa/nitter-cli/commit/13f13b4))
- **Circle member profiles (`nitter circle refresh`)**: member metadata now lives in a machine-readable sidecar
  (`~/.nitter-cli/profiles.toml`), keyed by lowercase handle, split into machine-refreshed facts (`name`,
  `bio`, `followers_count`, `fetched_at`) and judgement that refreshes never overwrite (`role`, `note`,
  `noted_at`). `circle refresh <NAME>` fetches each member's profile and merges the fact fields; a failing
  member is skipped with a warning while the others continue, and its previous facts are kept.
  ([#2](https://github.com/shitianyaa/nitter-cli/pull/2))

## Changed

- **`nitter update` no longer only prints guidance**: without `--check` it performs the check and then offers to
  install. `--proxy`/`config.proxy` now apply to update traffic, and when both are empty the command honors
  `HTTPS_PROXY`/`ALL_PROXY`.
- **`nitter download` reports its output directory**: one `note: writing to <dir>` line on stderr carrying the
  resolved absolute path, printed after the directory is created and before any file is written. stdout,
  `--json` and `--ndjson` are unaffected. ([7b5080a](https://github.com/shitianyaa/nitter-cli/commit/7b5080a))
- **`nitter circle show` is now local-only**: it joins `circles.toml` against the cached sidecar and performs
  zero network requests, so `--min-followers` now filters the cached follower count instead of fetching each
  member's profile. Members with no cache keep their row with `-` placeholders plus a stderr note pointing at
  `circle refresh`; `--json` rows gain `name`, `bio`, `fetched_at`, `role` and `note`. Run
  `nitter circle refresh <NAME>` once to populate the cache.
  ([08ad3a6](https://github.com/shitianyaa/nitter-cli/commit/08ad3a6))
- **Proxy documentation matches measured behavior**: the environment variables `HTTPS_PROXY`/`ALL_PROXY` apply
  to the FxTwitter fast lane and to `update`, but never to the nitter transport, which needs `--proxy` or
  `config proxy`. The help text previously implied they applied everywhere.
  ([d9629ae](https://github.com/shitianyaa/nitter-cli/commit/d9629ae))

## Fixed

- **`nitter update --check` on large release pages**: the response bound was 1 MiB, so a repository whose
  release list carries asset arrays (github.com/cli/cli returns ~4.7 MB per page) failed with
  `decode response: unexpected EOF`. The bound is now 32 MiB — still a bound, no longer reachable by a real
  page. ([eb924c8](https://github.com/shitianyaa/nitter-cli/commit/eb924c8))
- **`update` install-source detection on Windows**: the executable path was resolved (symlinks and 8.3 short
  names) while the `GOBIN`/`GOPATH` entry was compared verbatim, so a `go install` binary could be
  misclassified as an archive install and be replaced by self-update. Both sides are now canonicalized.
  ([0abcfce](https://github.com/shitianyaa/nitter-cli/commit/0abcfce))

**Full Changelog**: [v0.7.0...v0.7.1](https://github.com/shitianyaa/nitter-cli/compare/v0.7.0...v0.7.1)
