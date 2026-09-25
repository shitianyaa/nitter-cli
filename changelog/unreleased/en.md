# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- **`nitter followers <HANDLE>`**: the follower-list counterpart of `following` — fetches the accounts
  following a handle (1–15 letters, digits or underscores, without the `@`) and prints one row per follower
  profile (`@<handle>  <name>  <followers_count>  <bio>`). `--limit N` caps the number of profiles
  (default 20, must be >= 1); `--json`/`--ndjson` follow the shared output modes. The route always queries
  `api.fxtwitter.com` regardless of `fetch_backend`; `--instance` does not apply.
- **`nitter thread <TWEET_ID>`**: fetches the full self-thread containing a status and prints it root first
  (one row per status: `<ID>  <YYYY-MM-DD HH:MM>  @<handle>  <single-line text>`). The ID must be the
  numeric status ID (1–20 digits; URL and other reference forms belong to `get`); a status that is not part
  of a thread answers `not_found` (exit 1). The route always queries `api.fxtwitter.com`; `--instance` does
  not apply.
- **`nitter typeahead <QUERY>`**: account-name completion through the user-completion endpoint. This is a
  completion lookup, NOT search: no query operators, the upstream answers at most ~10-20 accounts and there
  is no pagination — use `search` for real queries. `--limit N` caps what is printed (default 20, must be
  >= 1; the upstream may answer fewer). The route always queries `api.fxtwitter.com`; `--instance` does not
  apply.
- **`nitter circle remove <NAME> <HANDLE>`**: removes a handle from a circle, editing only the circle's
  users array in `circles.toml`. The profile sidecar (`~/.nitter-cli/profiles.toml`, recorded `role`/`note`
  included) is never read nor written, so removing a member keeps their cached profile. Idempotent:
  removing a handle that is not a member prints an explicit `not a member; nothing changed` notice on
  stderr and exits 0.

## Changed

- **`circle add` now fetches the new member's profile facts**: after the roster write succeeds, the added
  handle is sent to `api.fxtwitter.com` for a best-effort profile fetch whose fact fields (`name`, `bio`,
  `followers_count`, `fetched_at`) are merged into the sidecar, so `circle show` can render a fresh member
  without waiting for `circle refresh`. The judgement fields (`role`/`note`/`noted_at`) are never touched.
  Every failure on that path is only a warning on stderr: the roster write already succeeded, so the
  command still exits 0 and the facts simply arrive with the next `circle refresh`.

- **The four remaining FxTwitter lane functions enforce the same argument contract as the
  timeline pair**: `SearchTweets`, `SearchUsers`, `FetchUserFollowing` and `FetchQuotes` now
  reject an empty query/handle and a non-positive count/limit with `invalid_argument` instead of
  silently defaulting to the upstream page size, dropping the limit parameter, or returning an
  empty success. The CLI has never been able to reach these paths (it rejects `--limit < 1` with
  exit 2 first), so this aligns the lane contract without a user-visible behavior change.

## Deprecated

## Removed

## Fixed

- **`mix` no longer replaces the fast lane's failure reason with `no instances configured`**: when
  the FxTwitter attempt failed and the instance path had nothing to answer with (no instances
  configured, or all cooling down), the error the user saw was only the chooser's answer. The
  chooser error now carries the fast lane's cause, e.g. `chooser: upstream_unavailable: no
  instances configured (fx attempt: fxtwitter.SearchTweets: not_found: resource not found
  (404): /2/search)`.
  A real instance failure is reported unchanged, and `fetch_backend = fx` (which never falls back)
  is unaffected.

## Security
