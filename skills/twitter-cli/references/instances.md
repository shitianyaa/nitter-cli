# Instances: configuration and health

How to point `twitter` at Nitter instances the user controls and how to judge
their health. Command semantics are governed by the installed binary's
`twitter <command> --help`.

## Where instances live

- Config file: `twitter config path` (typically `~/.twitter-cli/config.toml`;
  on Windows, under the user profile). The file is created automatically on the
  first real command, pre-filled with commented examples.
- Instances are `[[instances]]` array tables, hand-edited — `config set` refuses
  them:

  ```toml
  [[instances]]
  url = "http://nitter.internal:8080"   # required
  username = ""                          # optional basic auth
  password = ""                          # optional basic auth
  ```

- Order matters: instances are tried strictly in config order (no success
  weighting), and an entry that fails enters cooldown (default 60s,
  `instance_cooldown`) before the next one is tried.
- Credentials are carried in the config but the MVP transport does not wire
  them in — probes and fetches run unauthenticated. Prefer network-layer access
  control around the instance.
- One-off override: `twitter --instance URL <command>` replaces the whole
  configured set with that single URL for this invocation. `--proxy URL`
  analogously overrides the proxy (flag > config `proxy` > environment
  `HTTPS_PROXY`/`ALL_PROXY`; schemes `http`, `https`, `socks5`, `socks5h`).

## Probing health

```bash
twitter instances test                              # every [[instances]] entry, in order
twitter instances test http://nitter.internal:8080  # one instance
twitter instances test --full                       # + search probe
twitter instances test --full --list-id 12345       # + list probe
twitter instances test --user SOMEONE               # change the probe account (default NASA)
```

Human report — one line per instance:

```text
url	rss	user_html	search	list	latency
http://nitter.internal:8080	ok	ok	ok	-	212ms
```

Cells: `ok`, `fail(<reason>)` (the HTTP status such as `fail(404)`/`fail(429)`
when the status alone is the reason, otherwise a short stable token — `redirect`
for 3xx, `http 401/403` for challenge/login pages, `unreachable` for network
failures and 5xx after retries, `timeout`, `canceled`, `malformed`, or a
content reason such as `not rss`), or `-` (probe did not run). `--json` prints
one object for a single instance, an array for several; `--ndjson` prints one
`instance_report` envelope per instance.

## Reading the report

| Symptom | Meaning | What to do |
| --- | --- | --- |
| `rss ok`, `user_html ok` | Basic retrieval works | Good for `user`/`watch user:` usage |
| `search fail(...)` or `-` | Search disabled or not probed | Run `--full`; if still failing, hashtag/phrase queries via `search` and `tag:` watch sources will not work on this instance |
| `list fail(...)` | List route broken or not probed | Run with `--list-id` to test the actual list |
| everything `fail(timeout)` | Instance unreachable from this machine | Check URL reachability and proxy settings |
| `fail(http 401/403)` | Instance refuses the client (login/challenge page) | The instance may require auth or blocks bots; ask the user — do not retry endlessly |
| latency in seconds | Instance is slow | Expect slow fetches; consider moving it later in the config order |

Exit code: 0 whenever the probes completed — even if every cell failed. 2 = bad
input (empty `--list-id`, `--json --ndjson`, invalid URL/proxy), 1 = wiring
failure. Judge health from the report cells, not the exit code.

## Agent etiquette

- Adding/editing `[[instances]]` is a config-file change: confirm with the
  user, never fill in a public instance, and never echo credentials back.
- Run `instances test` before bulk work (watch setup, backfills) and report the
  table; a probe round costs one request per cell per instance (`--full` and
  `--list-id` each add one).
