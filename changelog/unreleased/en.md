# Unreleased

> Optional manual drafting area. The release workflow does not read this file; move finalized bilingual notes
> into the target `changelog/vX.Y.Z/` directory before creating the release-prep PR.

## Added

- `nitter download` naming templates: the `filename_template` config key (default `{id}-{seq}`, byte-identical to the previous behavior) and `directory_template` (default empty = flat) name and place downloaded files via the placeholders `{id}`, `{seq}`, `{user}`, `{kind}`, `{ext}`; `--filename-template` overrides the filename template per invocation. Covers always land as `<id>-cover.<ext>` (the template governs regular files only), rendered names are sanitized (Windows-illegal characters become `_`), a rendered-name collision within one ref gets a `-2`, `-3`, … suffix, and an invalid template warns once on stderr and falls back to the default/flat instead of failing (pixiv semantics). The config command now manages twelve scalar keys.
- `seen list`/`seen clear` gain `--state-dir DIR`: both subcommands can now operate on a `watch --state-dir` directory instead of only the default `~/.nitter-cli/state` location (the documented asymmetry is gone; reads and clears create nothing — a missing directory is simply the empty store).
- `watch --once --json`: prints one JSON document `{"tweets":[...bare Tweet objects...],"errors":[{"ref","code","message"}...]}` for the single cycle — selected tweets plus one entry per failed source (SDK error kind as `code`; the run still exits 1 when a source failed). `--json` without `--once` remains a usage error (exit 2): the resident loop is a stream of cycles — `--ndjson` stays the envelope stream.

## Changed

- `user --max-pages 0` now means UNBOUNDED HTML pagination: the HTML fallback follows the load-more cursor chain until upstream exhaustion (runaway runs are bounded by context cancellation); the RSS feed is single-page and unaffected. The omitted flag still applies the config `max_pages` / built-in default of 5, and `search`/`list` keep the old "0 = default" semantics.

## Deprecated

## Removed

## Fixed

## Security
