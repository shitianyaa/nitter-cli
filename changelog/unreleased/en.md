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
- **`nitter circle run --media-type`**: keep only tweets carrying at least one media entry of the given type (`image|video|gif`) when traversing a circle — flag parity with the `user` command; an invalid value is a usage error (exit 2) before any network.
- **`nitter circle show --min-followers`**: filter circle members by follower count (`--min-followers N` keeps only members with ≥ N followers, printing `@<handle>\t<followers>` per row, or JSON `{handle, followers_count}` objects with `--json`); a failed member profile is skipped with a stderr warning while others continue, all failing exits 1, and a negative N is a usage error (exit 2) before any network.
- **`nitter circle suggest`**: read-only circle-candidate discovery that merges a handle's following list (static) with the authors of the retweets in its timeline (behavioral), ranking candidates by co-occurrence then follower count; human output is a stats header, a ranked table (`@handle\tfollowers\tbio\tsource`) and a `--min-followers`-filtered `top matches` summary, with `--json` for `{handle, followers_count, bio, source}` arrays. `--limit` caps each lane and must be ≥ 1 (0 is a usage error, since the two lanes give 0 opposite, useless meanings); a missing seed exits 1, a single lane failure degrades with a stderr warning, and `suggest` never writes the circle file.

## Changed

- **`circle run` deterministic results and filter provenance**: Fx fast-lane results are sorted by tweet ID descending before `--limit` truncates (same input, same output — upstream page-composition fluctuations no longer change the result set); NDJSON envelopes carry `meta.filter` (`"media_only"` or the media-type value) when a media filter is in effect; snapshot semantics and the doubled media-endpoint pagination under `--media-only` are documented in the help text and CLI reference.

- Thirteen scalar keys (added `fetch_backend` with `NITTER_FETCH_BACKEND` env override).
- `--instance` explicitly forces Nitter; List subscriptions remain strictly isolated to Nitter.

## Deprecated

## Removed

- **`vx` and `syndication` media strategies**: removed from the `media`/`download` resolve chain (both directions verified dead on two environments — vx always 403 challenge_required, syndication empty/not_found). `--strategy auto` is now `fx → nitter → xdown`; explicit `vx`/`syndication` are usage errors (exit 2).

## Fixed

- **Nitter user-less refs 404ed (`get`, and `media`/`download` with `--strategy nitter`)**: a bare numeric status ID resolves to a user-less reference, and the instance was asked for its `/status/<id>` route — the deployed Nitter answers 404 there, so those runs failed with `not_found` (the auto chain reported `all media strategies failed`). User-less refs now use `/i/status/<id>`, the same route `StatusRef.String` renders and the one Nitter actually serves.
- **Nitter-strategy downloads got a polluted file extension**: the nitter media link wraps the real twimg URL inside its own path (`/video/<token>/<percent-encoded URL>`); `url.Parse` decodes that path, so the inner URL's query (`?tag=29`) landed in it and the extension came out as `.mp4?tag=29` — files landed as `<id>-1.mp4_tag=29`. The path is now cut at the first `?`/`#` and only a plausible extension (1–8 alphanumerics) is accepted, otherwise the Content-Type decides.
- **watch silently fetched nothing under the fx backend**: the user/tag/list source fetches passed `limit 0` (= all) and the Fx fast lane treats `count <= 0` as ZERO tweets with a nil error — every cycle recorded an empty baseline (`seen=0`) and emitted nothing. Source fetches now send a positive all-tweets sentinel (nitter semantics unchanged; the Fx client's eager allocation is clamped so an unbounded count no longer reserves gigabytes).
## Security
