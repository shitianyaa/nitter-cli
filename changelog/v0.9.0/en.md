# v0.9.0 — 2026-09-26

Adds the `followers`, `thread` and `typeahead` commands and `circle remove`, and corrects the FxTwitter lane contract, the `update` failure guarantee and the operator skill's upgrade path.

## Added

- **`nitter followers <HANDLE>`**: the follower-list counterpart of `following` — fetches the accounts
  following a handle (1–15 letters, digits or underscores, without the `@`) and prints one row per follower
  profile (`@<handle>  <name>  <followers_count>  <bio>`). `--limit N` caps the number of profiles
  (default 20, must be >= 1); `--json`/`--ndjson` follow the shared output modes. The route always queries
  `api.fxtwitter.com` regardless of `fetch_backend`; `--instance` does not apply.
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`nitter thread <TWEET_ID>`**: fetches the full self-thread containing a status and prints it root first
  (one row per status: `<ID>  <YYYY-MM-DD HH:MM>  @<handle>  <single-line text>`). The ID must be the
  numeric status ID (1–20 digits; URL and other reference forms belong to `get`); a status that is not part
  of a thread answers `not_found` (exit 1). The route always queries `api.fxtwitter.com`; `--instance` does
  not apply.
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`nitter typeahead <QUERY>`**: account-name completion through the user-completion endpoint. This is a
  completion lookup, NOT search: no query operators, the upstream answers at most ~10-20 accounts and there
  is no pagination — use `search` for real queries. `--limit N` caps what is printed (default 20, must be
  >= 1; the upstream may answer fewer). The route always queries `api.fxtwitter.com`; `--instance` does not
  apply.
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`nitter circle remove <NAME> <HANDLE>`**: removes a handle from a circle, editing only the circle's
  users array in `circles.toml`. The profile sidecar (`~/.nitter-cli/profiles.toml`, recorded `role`/`note`
  included) is never read nor written, so removing a member keeps their cached profile. Idempotent:
  removing a handle that is not a member prints an explicit `not a member; nothing changed` notice on
  stderr and exits 0.
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))

## Changed

- **`circle add` now fetches the new member's profile facts**: after the roster write succeeds and a new
  member was actually inserted, the added handle is sent to `api.fxtwitter.com` for a best-effort profile
  fetch whose fact fields (`name`, `bio`, `followers_count`, `fetched_at`) are merged into the sidecar, so
  `circle show` can render a fresh member without waiting for `circle refresh`. The judgement fields
  (`role`/`note`/`noted_at`) are never touched. Every failure on that path is only a warning on stderr: the
  roster write already succeeded, so the command still exits 0 and the facts simply arrive with the next
  `circle refresh`.
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **The four remaining FxTwitter lane functions enforce the same argument contract as the
  timeline pair**: `SearchTweets`, `SearchUsers`, `FetchUserFollowing` and `FetchQuotes` now
  reject an empty query/handle and a non-positive count/limit with `invalid_argument` instead of
  silently defaulting to the upstream page size, dropping the limit parameter, or returning an
  empty success. The CLI has never been able to reach these paths (it rejects `--limit < 1` with
  exit 2 first), so this aligns the lane contract without a user-visible behavior change.
  ([#13](https://github.com/shitianyaa/nitter-cli/pull/13))
- **`nitter update`'s failure guarantee is now stated for what it covers**: the help text and
  the CLI reference said every failure leaves the installation untouched, but the final
  replacement is not covered — on Windows `ReplaceFileW` is not atomic and can fail after
  moving the old binary aside. The wording now limits the guarantee to failures before the
  replacement and says to verify the installed binary before retrying. No behavior change.
  ([#15](https://github.com/shitianyaa/nitter-cli/pull/15))
- **The CLI reference, README and operator skill now present `nitter update` as the supported
  upgrade path**: the Skill no longer calls `update` an installer it forbids, the upgrade steps
  settle the Skill tag and its authorization before replacing the binary, and the binary and the
  Skill are documented as one versioned pair.
  ([#15](https://github.com/shitianyaa/nitter-cli/pull/15))

## Fixed

- **`circle add` writes the roster back under the matched section key**: a circle whose inner `key`
  field differs from its section name no longer gets a second section on add — the member lands in
  the circle that was actually matched, and a duplicate add no longer triggers a profile fetch.
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`mix` no longer replaces the fast lane's failure reason with `no instances configured`**: when
  the FxTwitter attempt failed and the instance path had nothing to answer with (no instances
  configured, or all cooling down), the error the user saw was only the chooser's answer. The
  chooser error now carries the fast lane's cause, e.g. `chooser: upstream_unavailable: no
  instances configured (fx attempt: fxtwitter.SearchTweets: not_found: resource not found
  (404): /2/search)`.
  A real instance failure is reported unchanged, and `fetch_backend = fx` (which never falls back)
  is unaffected.
  ([#13](https://github.com/shitianyaa/nitter-cli/pull/13))

**Full Changelog**: [v0.8.0...v0.9.0](https://github.com/shitianyaa/nitter-cli/compare/v0.8.0...v0.9.0)
