# Instances: configuration and health

How to point `nitter` at Nitter instances the user controls and how to judge
their health. Command semantics are governed by the installed binary's
`nitter <command> --help`.

## Where instances live

- Config file: `nitter config path` (typically `~/.nitter-cli/config.toml`;
  on Windows, under the user profile). The file is created automatically on the
  first real command, pre-filled with commented examples.
- Instances are `[[instances]]` array tables, hand-edited — `config set` refuses
  them:

  ```toml
  [[instances]]
  url = "http://nitter.internal:8080"   # required
  username = ""                          # optional basic auth — sent only when BOTH are set
  password = ""                          # optional basic auth
  ```

- Order matters: instances are tried strictly in config order (no success
  weighting), and an entry that fails enters cooldown (default 60s,
  `instance_cooldown`) before the next one is tried.
- Basic auth: when an entry sets **both** `username` and `password`, requests
  to that instance (probes and fetches alike) carry HTTP basic auth. The
  credential policy is host-scoped inside the transport — a credential is
  only ever attached to a request addressed to its own configured instance,
  so the third-party media resolvers (fx/vx/syndication/xdown, twimg) can
  never receive it, and it never enters errors or logs. An incomplete pair
  (only one half set) is treated as unconfigured. If you cannot credential
  the instance, put network-layer access control around it instead.
- One-off override: `nitter --instance URL <command>` replaces the whole
  configured set with that single URL for this invocation — it is a plain
  URL and carries **no** credentials, so a credentialed instance must be
  used through its config entry. `--proxy URL`
  analogously overrides the proxy (flag > config `proxy` > environment
  `HTTPS_PROXY`/`ALL_PROXY`; schemes `http`, `https`, `socks5`, `socks5h`).

## No instance yet?

If the user has none, do not improvise: ask whether to deploy one and
follow [deploy.md](deploy.md). When they say they have one but cannot name
the URL, deploy.md's "Finding an existing instance" section is the search
order (ask → docker/systemctl traces → verify with `instances test` before
any config edit).

## Probing health

```bash
nitter instances test                              # every [[instances]] entry, in order
nitter instances test http://nitter.internal:8080  # one instance
nitter instances test --full                       # + search probe
nitter instances test --full --list-id 12345       # + list probe
nitter instances test --user SOMEONE               # change the probe account (default NASA)
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
