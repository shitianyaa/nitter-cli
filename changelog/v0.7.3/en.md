# v0.7.3 — 2026-09-23

Accurate tweet-count mapping for FxTwitter profiles, multi-category subscription isolation guidance, and documentation sample corrections.

## Changed

- **Watch documentation recommends one `--state-dir` per subscription category**: `watch` dedup state is keyed
  by source key inside one `seen.json`, so two categories watching the same account shared the key `user:HANDLE`
  — the category whose cycle ran first marked the tweet seen and the other one dropped it silently (no error,
  `--once` still exited 0). The CLI reference and both READMEs now tell you to give each category its own
  `--state-dir ~/.nitter-cli/state/<category>` and to pass that same flag on every scheduler line of that
  category, and they explain the concurrent-write case as well.
  ([#4](https://github.com/shitianyaa/nitter-cli/pull/4))
- **The NASA example status in the documentation now belongs to NASA**: the example status ID shared by both
  READMEs, the CLI reference and the SDK guide resolved to a different account's tweet, so a copied `nitter get`
  / `nitter media` / `nitter download` line fetched something other than the NASA status the surrounding text
  claimed. Every example now uses a real NASA status.
  ([#4](https://github.com/shitianyaa/nitter-cli/pull/4))

## Fixed

- **`profile`, `following` and `search --type user` report the real tweet count**: `tweets_count` was always `0`
  because the FxTwitter profile and author objects carry the count under `statuses`, a key the decoder never read
  (it only mapped the legacy `tweets_count`/`statuses_count` spellings). The decoder now reads `statuses` first,
  so the `Tweets:` line of the profile card and the `tweets_count` field of every `profile` envelope carry the
  source's real count.
  ([#4](https://github.com/shitianyaa/nitter-cli/pull/4))

**Full Changelog**: [v0.7.2...v0.7.3](https://github.com/shitianyaa/nitter-cli/compare/v0.7.2...v0.7.3)
