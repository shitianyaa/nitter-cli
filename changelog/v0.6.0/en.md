# v0.6.0 — 2026-09-13

## Added

- `nitter download` naming templates: the `filename_template` config key (default `{id}-{seq}`, byte-identical to the previous behavior) and `directory_template` (default empty = flat) name and place downloaded files via the placeholders `{id}`, `{seq}`, `{user}`, `{kind}`, `{ext}`; `--filename-template` overrides the filename template per invocation. Covers always land as `<id>-cover.<ext>` (the template governs regular files only), rendered names are sanitized (Windows-illegal characters become `_`), a rendered-name collision within one ref gets a `-2`, `-3`, … suffix, and an invalid template warns once on stderr and falls back to the default/flat instead of failing (pixiv semantics). The config command now manages twelve scalar keys.
- `seen list`/`seen clear` gain `--state-dir DIR`: both subcommands can now operate on a `watch --state-dir` directory instead of only the default `~/.nitter-cli/state` location (the documented asymmetry is gone; reads and clears create nothing — a missing directory is simply the empty store).
- `watch --once --json`: prints one JSON document `{"tweets":[...bare Tweet objects...],"errors":[{"ref","code","message"}...]}` for the single cycle — selected tweets plus one entry per failed source (SDK error kind as `code`; the run still exits 1 when a source failed). `--json` without `--once` remains a usage error (exit 2): the resident loop is a stream of cycles — `--ndjson` stays the envelope stream.
- `watch --max-new-overflow keep|drop` (default `drop`, the previous silent-loss behavior): `keep` leaves tweets beyond the `--max-new` cap unseen so the next cycles re-emit them under the same cap (宁重勿丢 — a burst larger than twice the cap drains over several cycles); `keep` together with `--max-new 0` still seals the current first page as the baseline. Any other value is a usage error (exit 2).

## Changed

- Instance basic authentication: an `[[instances]]` entry with BOTH `username` and `password` now authenticates requests to that instance (HTTP basic auth, host-scoped inside the transport). Credentials can only ever ride requests addressed to their own instance — the third-party media endpoints never see them — and they never enter errors, logs, or responses; an incomplete pair is treated as unconfigured. A one-off `--instance URL` override is a plain URL and carries no credentials (documented limitation).
- Data commands default to NDJSON when stdout is a pipe: `user`, `search`, `list`, `get`, `media`, `download` and `instances test` now emit `nitter.pipeline/v1` envelopes when stdout is not a TTY and no explicit `--json`/`--ndjson` is given, so `nitter search "..." | nitter download` works without flags. Explicit flags win; on a TTY the default stays the human table; an empty result in a pipe prints nothing (no `(empty)` hint). `watch` keeps its text default in pipes (pass `--ndjson` for the envelope stream); `config`, `seen` and `update` are unchanged.
- `user --max-pages 0` now means UNBOUNDED HTML pagination: the HTML fallback follows the load-more cursor chain until upstream exhaustion (runaway runs are bounded by context cancellation); the RSS feed is single-page and unaffected. The omitted flag still applies the config `max_pages` / built-in default of 5, and `search`/`list` keep the old "0 = default" semantics.

## Deprecated

## Removed

## Fixed

## Security
