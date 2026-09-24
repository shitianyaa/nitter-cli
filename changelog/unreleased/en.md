# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- **`nitter search --sort latest|top`**: `search` gains a result-ordering flag — `latest` (default, the
  newest-first feed, byte-identical to the pre-0.7.4 request) or `top` (the popular-results feed). The
  ordering is handed to BOTH backends (Nitter's `f=` parameter and FxTwitter's `feed`) and carried on every
  pagination page, so a mix-mode fallback never answers with a different ordering than the one requested.
  The value is case- and whitespace-insensitive; any other value exits 2, and `--sort` with `--type user`
  exits 2 as well (profile search has no ordering). Note that `comments --sort likes|recency` is a different
  domain: neither value set is valid for the other command.

## Changed

## Deprecated

## Removed

## Fixed

- **`user --limit 0` and `circle run --limit 0` no longer return empty under Fx/mix backend**: `--limit 0`
  means "all" in the CLI contract, but the underlying FxTwitter client treated `count <= 0` as a request for 0
  items and returned `(nil, "", nil)`. The wiring adapter now maps `limit <= 0` to a sentinel so FxTwitter fetches
  all tweets within the page budget. Additionally, Fx honors unbounded `--max-pages 0` without clamping to 3 pages,
  defaults to 5 pages when unspecified, and adds cursor cycle guards.
- **`get`, `media` and `download`: a positional reference now wins over stdin** — the commands read stdin
  only when they receive no positional reference AND stdin is not a TTY. Previously they read stdin first and
  rejected the input as ambiguous (`status reference given both as an argument and on stdin`, exit 2); on a
  pipe whose writer stayed open that read blocked forever, so `nitter get <REF>` (or `media`/`download` with
  positional refs) inside a pipeline hung instead of running. The ambiguity error and its exit-2 branch are
  gone; passing the input one way is the rule, and having both is no longer an error.

## Security
