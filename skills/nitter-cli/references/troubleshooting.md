# Troubleshooting

Common errors, what they mean, and what to do. Command semantics are governed
by the installed binary's `nitter <command> --help`; error messages themselves
are redacted (no credentials, no URL query strings, no headers or bodies).

First rule: **check the exit code before parsing anything.** 0 = success,
1 = runtime failure, 2 = usage error. `--json`/`--ndjson` only describe
successful output; **stderr is never JSON**.

## Error table

| Symptom (stderr) | Exit | Meaning | Fix |
| --- | --- | --- | --- |
| `instances test: no instances configured; add an [[instances]] table to config.toml or pass a URL` | 2 | No instance in config and no URL argument | Have the user add their own Nitter instance (`[[instances]]`) or pass `--instance URL`; never substitute a public instance |
| `chooser: upstream_unavailable: no instances configured` | 1 | A Nitter-path fetch ran with an empty instance set — `list`/`watch`, an explicit `--instance`-less `nitter` backend fetch, or a mix-mode fallback after FxTwitter failed | Same as above: configure an instance first. Note that mix mode (default) serves `user`/`search`/`get`/`comments`/`following`/`profile`/`quotes`/`trends` without any instance |
| `chooser: upstream_unavailable: all instances cooling down` | 1 | Every instance recently failed (429/network) and is cooling | Wait out the cooldown (default 60s), fix instance health, or add a healthy instance; `markSuccess` resets on the next successful fetch |
| `... rate_limited` (429) | 1 | Instance rate-limited the client; a `Retry-After` is honored once automatically | Slow down (raise `request_interval`), reduce poll frequency or `--limit`; do not hammer retries |
| `... challenge_required` (login/maintenance/challenge page) | 1 | The instance served a login or challenge page instead of content | Check the instance in a browser; redeploy or replace it — the CLI never solves challenges |
| `fail(timeout)` / `unreachable` in `instances test`, or transport errors on fetch | 1 | Instance unreachable (network, proxy, wrong URL) | Verify URL reachability, `--proxy`/config proxy; test with `nitter instances test <url>` |
| `... local_state_error: write temp file: ...` on `download` | 1 | The **local** write of the downloaded file failed (full disk, permissions, read-only target directory) — not an instance or network problem | Free space, or point `--output`/`download_path` at a writable directory. Retrying against the instance cannot help; every other local filesystem failure on this path reports the same kind |
| RSS probes `ok` but `search` empty or `fail(...)` | 0/1 | Search is a separate Nitter capability, disabled or slow on this instance | Run `nitter instances test --full`; use an instance with search enabled |
| `search "#AI"` returns nothing though the tag exists | 0 | Two candidates: instance search disabled, or the query was pre-escaped | Prefer the raw form (`nitter search "#AI"`); the pre-escaped `%23` form double-escapes and must not be used anywhere (`watch tag:` sources included) |
| `watch: no sources given and no [[watch.sources]] configured` | 2 | Neither argv sources nor config sources | Pass sources (`user:NASA tag:#AI list:12345`) or add `[[watch.sources]]` |
| `watch: --json requires --once; ...` | 2 | `--json` passed to the resident watch loop (no `--once`) | The loop is a stream, not one document: use `--ndjson`, or add `--once` to get the `{"tweets","errors"}` document of that single cycle |
| `--json and --ndjson are mutually exclusive` | 2 | Both flags on one command | Pick one; `--json` for single-document extraction, `--ndjson` for streams |
| `watch: --interval must be >= 1s, got ...` | 2 | Interval below the 1s floor or not a duration | Use e.g. `10m`; the value is validated even with `--once` |
| `seen clear: state changes need explicit authorization ... pass --confirm` | 2 | `seen clear` without `--confirm` | Get user consent, then add `--confirm`; consent is per-command, never carried over |
| `not found: <key>` on `seen clear` | 0 | Cleared a source that had no state | Idempotent success; nothing to do |
| `user: "..." is not a valid handle (1-15 letters, digits or underscores, without the @)` | 2 | Bad handle shape | Strip the `@`, check for illegal characters |
| `list: "..." is not a valid list ID (non-empty, no whitespace, ?, # or /)` | 2 | Bad list ID | Use the numeric ID (or a ref the instance accepts) |
| `get: status reference given both as an argument and on stdin` | 2 | Ambiguous ref input | Pass the ref one way only |
| `appapi.ParseStatusRef: invalid_argument: not a status reference ...` | 2 | Unparseable `get` ref | Use a bare numeric ID or a `<user>/status/<id>` URL (`/photo/N`, `/video/1` suffixes allowed) |
| `config set: unknown key "..."` (+ array-table hint) | 2 | Not a scalar key | The thirteen scalar keys only; `[[instances]]`/`[[watch.sources]]` are hand-edited TOML (creator circles live in `circles.toml`) |
| `config set <key>: "..." is not an integer / not a duration / is invalid` | 2 | Value failed schema validation | Follow the documented shapes: ints >= 0, durations >= 0 (`500ms`, `2s`), `log_level` debug|info, `log_format` text|json |
| `watch: invalid config [[watch.sources]] entry ...` | 2 | Malformed source id in config | Fix the entry to `user:<handle>`, `tag:<query>` (raw query), or `list:<id>` |
| `unsupported .../seen.json schema version N (want 1); refusing to reset state` or a parse error on state | 1 | Corrupt or foreign state file | Hard error by design (never a silent reset). With user consent, move the file away or start from a fresh `--state-dir`; report it as a bug if it corrupted on its own |
| `error: unknown command "..." for "nitter"` | 1 | Typo'd subcommand | Check `nitter --help`; note this exits 1, not 2 |
| `unknown flag: --...` | 2 | Invented or misspelled flag | Run `nitter <command> --help`; never invent flags |
| A watch burst delivered only some new tweets | 0 | `--max-new` cap (default 10); the excess was marked seen and will not re-emit | Raise `--max-new` or poll more often; see references/watch.md |
| Watch emitted nothing on its first run | 0 | First run of an uninitialized source records state only (只记不推) | Expected; use `--include-existing` for the run where history is wanted |

## Diagnosis order

1. Exit code (0/1/2) — then look at stderr (human text) or the `kind:"error"`
   envelopes on the NDJSON stream (watch per-source failures land on stdout).
2. For fetch problems: `nitter instances test [--full]` and read the cells.
3. For watch problems: `nitter seen list --state-dir <dir>` (or the default
   location without the flag) and references/watch.md's exit-code matrix.
4. For anything state-changing (clear, set, unset): fresh user consent, every
   time.
