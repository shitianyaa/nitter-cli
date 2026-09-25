# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

## Changed

- **The four remaining FxTwitter lane functions enforce the same argument contract as the
  timeline pair**: `SearchTweets`, `SearchUsers`, `FetchUserFollowing` and `FetchQuotes` now
  reject an empty query/handle and a non-positive count/limit with `invalid_argument` instead of
  silently defaulting to the upstream page size, dropping the limit parameter, or returning an
  empty success. The CLI has never been able to reach these paths (it rejects `--limit < 1` with
  exit 2 first), so this aligns the SDK contract without a user-visible behavior change.

## Deprecated

## Removed

## Fixed

- **`mix` no longer replaces the fast lane's failure reason with `no instances configured`**: when
  the FxTwitter attempt failed and the instance path had nothing to answer with (no instances
  configured, or all cooling down), the error the user saw was only the chooser's answer. The
  chooser error now carries the fast lane's cause, e.g. `chooser: upstream_unavailable: no
  instances configured (fx attempt: fxtwitter.SearchTweets: not_found: resource not found (404))`.
  A real instance failure is reported unchanged, and `fetch_backend = fx` (which never falls back)
  is unaffected.

## Security
