# Deploy: standing up a Nitter instance

The flow for a user who has no Nitter instance of their own. Command
semantics are governed by the installed binary's `nitter <command> --help`.

> Context first: with the default `fetch_backend = mix`, `user` / `search` /
> `get` / `comments` / `following` / `profile` / `quotes` / `trends` all work
> **without any instance** through the FxTwitter fast lane. A Nitter instance
> is needed for `list`, for the Fx fallback, and for `--instance` (which pins
> ONE call to a single instance, on `user` / `search` / `get` / `list` only) —
> say this before proposing a deployment, and let the user decide whether they
> want one.

## Ask first

- Ask the user whether they already run their own Nitter instance. Yes →
  find it (next section). No → ask whether they want one deployed now, and
  deploy only on an explicit yes. Never "solve" a missing instance by
  filling in a public one (nitter.net, xcancel, …) — that boundary holds
  here too.
- Say what a deployment means before starting: two Docker containers
  (`nitter` + `nitter-redis`) in a working directory of the user's choosing,
  and — stated plainly — that Nitter fetches through a **real X account's
  session token**; there is no token-free mode. If Docker is missing,
  installing it is a prerequisite the user consents to first.

## Finding an existing instance

1. Ask for the instance URL first; when the user knows it, that is the
   whole step.
2. When they don't, look for deployment traces on the machine they name:
   `docker ps --format '{{.Names}} {{.Ports}}' | grep -i nitter`,
   `systemctl list-units --all | grep -i nitter`, or a Nitter listening on
   its default port 8080. Probe only hosts the user pointed at — no
   port-scanning beyond that.
3. Every candidate URL must pass `nitter instances test <URL> --full`
   (`rss`/`user_html` cells `ok`) before it enters `[[instances]]`; a probe,
   not a guess, is the only write path into config.

## Docker deployment (default)

Nitter ships an official Compose file (nitter + redis, port bound to
`127.0.0.1:8080:8080` by default — localhost-only is the upstream default,
keep it). Upstream is archived since 2026-09 (frozen, not deleted), so the
linked files are stable:

```bash
mkdir nitter && cd nitter
curl -fsSLO https://raw.githubusercontent.com/zedeus/nitter/master/compose.yml
curl -fsSLO https://raw.githubusercontent.com/zedeus/nitter/master/nitter.example.conf
mv nitter.example.conf nitter.conf
# nitter.conf: set redisHost = "nitter-redis"   (the only required change)
```

Compose mounts `./nitter.conf` and `./sessions.jsonl` into the container —
if a mounted file is missing, Docker silently creates a directory in its
place and the container fails, so both files must exist **before**
`docker compose up -d`:

1. **`sessions.jsonl` — the hard prerequisite.** Since X killed guest
   accounts (2024-02), Nitter works only through real account session
   tokens; the
   [Creating-session-tokens wiki page](https://github.com/zedeus/nitter/wiki/Creating-session-tokens)
   plus the scripts in the upstream `tools/` directory produce the file.
   Advise a burner account without 2FA, never the user's main one. The file
   is a credential: place it on the user's instruction, but never read,
   echo, or transmit its contents. If the user will not provide tokens,
   stop here and say why — a tokenless instance boots but fetches nothing;
   that is not a bug and not something to work around.
2. **`docker compose up -d`** (compose ships `restart: unless-stopped` and
   a healthcheck). Host port 8080 already taken? Change only the host side
   of the mapping (e.g. `127.0.0.1:8081:8080`) and use that port below.
3. **Verify, then wire in** (a config edit is a state change: consent):
   `nitter instances test http://127.0.0.1:8080 --full`, and on `ok` cells
   add `[[instances]] url = "http://127.0.0.1:8080"` to the config file.

A community walkthrough with the same shape (compose + nitter.conf +
credentials + up): the
[self-hosting guide](https://github.com/sekai-soft/guide-nitter-self-hosting)
— link it when the user wants to read instead of delegate.

## Operational notes

- **Localhost is the feature, not a limitation.** `127.0.0.1:8080:8080`
  keeps the instance off the network. When the CLI runs on a *different*
  machine (a VPS deployment), the default binding is unreachable from
  there — bridge with an SSH tunnel or an authenticating reverse proxy
  instead of widening the bind; the reverse-proxy case is also where
  `[[instances]]` `username`/`password` applies (host-scoped; see
  [instances.md](instances.md)).
- **Say the risk once, plainly.** Real-account scraping can get the account
  rate-limited or banned, and X Corp is actively targeting Nitter
  (cease-and-desist 2026-08, upstream repository archived 2026-09).
  Self-hosting is the user's informed choice; state it and move on.
- Updates: `docker compose pull && docker compose up -d` in the compose
  directory. Upstream being frozen, the prebuilt image may eventually stop
  being published — a local build from the archived repository (its
  Dockerfile) is the fallback.
- Diagnose a down or tokenless instance with `nitter instances test`
  (`unreachable`/`timeout` on every cell usually means the container is
  down or the token file is missing/invalid); look at the instance's own
  logs (`docker compose logs nitter` in the compose directory), never into
  credential files.

## Agent etiquette

- The deploy chain is a sequence of state changes on the user's machine
  (prerequisite installs, files, containers, config edits): confirm each
  step with the user, exactly like every other Local-write/Disk-write tier.
- Verify before wiring in: an instance that was never probed with
  `instances test` does not go into config, no matter how confident the
  deployment looked.
