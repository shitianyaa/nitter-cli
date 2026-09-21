# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- **FxTwitter API v2 hybrid fetch engine**: `fetch_backend` setting (`mix` default, `nitter`, `fx`). Timelines and searches try FxTwitter fast-lane without credentials first, falling back smoothly to Nitter instances on network error, 429, or SafeSearch 404.
- **Following list command (`nitter following`)**: fetch followed account profiles for any handle (table on TTY, NDJSON `kind: "profile"` in pipe mode).
- **Comments and conversation tree command (`nitter comments`)**: fetch thread context and user replies (sorted by `likes` or `recency`), ideal for extracting author self-replies with hidden download links or reading serialized manga threads.
- **Curated creator circles (`nitter circle`)**: manage and stream private rosters in `~/.nitter-cli/circles.toml` (`list`, `show`, `add`, `run`).
- **`nitter user --with-replies`**: include the user's reply tweets in timeline.
- **SDK model expansions**: added `Profile` and `Conversation` models to `sdk`.
- **Trends command (`nitter trends`)**: fetch real-time Twitter/X trending topics with ranks, names, contexts, and tweet counts (formatted table on TTY, `kind: "trend"` NDJSON in pipe mode).
- **Quote tweets command (`nitter quotes`)**: fetch quote tweets and derivative creations for a status (supports `--media-only` and `--no-reposts`, emitting `kind: "tweet"` NDJSON in pipe mode).
- **Profile card command (`nitter profile`)**: fetch creator profile card with bio, counts, and media URLs (`kind: "profile"` NDJSON in pipe mode).
- **User search (`nitter search --type user`)**: search creators, artists, and profiles by keywords or bio.
- **Fast-lane dispatch for `nitter get`**: `get` connects through FxTwitter first in `mix` and `fx` modes, smoothly falling back to Nitter instances.
- **SDK `Trend` model and `KindTrend` protocol constant**: added `Trend` struct and `KindTrend = "trend"`.

## Changed

- Thirteen scalar keys (added `fetch_backend` with `NITTER_FETCH_BACKEND` env override).
- `--instance` explicitly forces Nitter; List subscriptions remain strictly isolated to Nitter.

## Deprecated

## Removed

## Fixed

## Security
