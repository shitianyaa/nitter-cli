# v0.9.1 — 2026-10-25

Reads existing configuration from read-only config directories and clarifies the operator skill's guidance for authorized instance maintenance, download inputs, and rate-limit handling.

## Changed

- **Operator skill clarifications**: instance maintenance under explicit user authorization reads and edits only what the task requires; `config get`/`config set` manage scalar keys only (`[[instances]]` stays hand-edited TOML); `/pic/` links and direct image/video URLs are not download inputs; probe instances only for a real health decision, and treat HTTP 429 as rate limiting rather than a configuration error.
  ([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

## Fixed

- **Existing configuration works in read-only directories**: when the config file already exists, initialization returns early without creating staging files, so `config path`/`config get` succeed on a read-only config directory. First-run initialization remains atomic and never overwrites an existing config; write commands still require a writable directory and report the staging failure explicitly.
  ([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

**Full Changelog**: [v0.9.0...v0.9.1](https://github.com/shitianyaa/nitter-cli/compare/v0.9.0...v0.9.1)
