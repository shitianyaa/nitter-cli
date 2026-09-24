# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

## Changed

- **`--limit` and `--max-pages` have no "unlimited" value**: both are caps and must be `>= 1` when given; `0`
  and negative values are usage errors (exit 2). `0` used to mean "all" for `--limit` and "lift the page cap"
  for `--max-pages`, and the two fetch lanes read it in opposite ways — the Nitter lane as unbounded, the
  FxTwitter lane as zero — so one flag produced different results depending on which lane answered. The
  `default_limit` and `max_pages` config keys follow the same rule (both `>= 1`; `retry_attempts` still
  accepts `0`). **Migration**: replace `--limit 0` with an explicit cap, and `--max-pages 0` with a large
  page budget; for a deep backlog state both. The removed `user --max-pages 0` also drops the
  "paginate until upstream exhaustion" escape hatch. Prefer the per-call flags over raising the global
  `max_pages`: `watch` (per cycle) and `circle run` (per member) read that same key, and `search` ignores
  `--max-pages` on the FxTwitter lane, whose endpoint is fetched in a single request.
- **`trends --limit` now defaults to `20`** (it was `0`, i.e. every trend the endpoint returned). Pass a
  larger value to see more.
- **`fetch_backend` has only `mix` and `fx`**: the `nitter` value was removed. Its meaning — instances only, never
  the fast lane — has no equivalent among the two remaining modes, and quietly mapping it onto `mix` would add the
  fast lane the setting used to exclude, so it is now rejected with a message naming `mix`. To pin one invocation
  to your own instances, pass `--instance URL`; that flag also remains the way to exercise a single instance.
  **Migration**: replace `fetch_backend = "nitter"` with `fetch_backend = "mix"` (or `"fx"`), and use
  `--instance URL` where you want the instance path for a single call.
- **`fetch_backend = fx` no longer falls back to Nitter for `get`**: that fallback replaced the real Fx error with
  `no instances configured` whenever no instance was configured, hiding the cause. Under `fx` the fast lane is the
  only permitted source and its error surfaces as it is; `mix` keeps its fallback unchanged.
- **`--instance` is now documented by what it actually covers**: it applies to the commands that have an instance
  path (`user`, `search`, `get`, `list`). The Fx-only capabilities (`comments`, `following`, `profile`, `quotes`,
  `trends`, `search --type user`) ignore it, and the override is a plain URL that carries no basic-auth
  credentials.

## Deprecated

## Removed

- **The `nitter` value of `fetch_backend`** — see the `Changed` entry above for the migration.
- **`user --max-pages 0`** as the spelling for "no page cap".

## Fixed

- **`user --limit 0` and `circle run --limit 0` no longer return an empty result on the FxTwitter lane**: the lane
  treated `count <= 0` as "zero tweets" and answered with an empty success, so a documented "fetch everything"
  silently produced nothing — and `mix` accepted that as a success instead of falling back to the instances. The
  value is now a usage error, and the lane itself rejects a non-positive count instead of returning empty.
- **`watch --max-pages 0` no longer silently overrides the configured `max_pages`**: it was accepted and made
  the cycle fall back to the built-in default of 5 pages, ignoring the config value entirely. It is now a
  usage error.
- **`quotes` and `search --type user` no longer report an upstream failure as an empty result**: the FxTwitter
  quote and user-search routes share one upstream query that answers 404 intermittently (measured ≈85% of
  requests), and both commands turned that 404 into an empty success — a tweet with 44 quotes printed `[]` and
  exited 0. The 404 now surfaces as `not_found` (exit 1). `quotes` cross-checks the tweet's own quote count first,
  so a tweet that genuinely has no quotes still exits 0 with an empty result; `search --type user` treats an empty
  list as the "no matches" answer it is.
- **Error messages no longer carry the request URL or the upstream body**: the FxTwitter lane interpolated the
  full request URL — query string included — into transport and 404 errors, so a failed search printed the
  caller's own search terms, and it echoed the upstream `message` field. Errors now name the route only, as the
  `sdk` redaction contract requires.

## Security
