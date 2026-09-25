# nitter-cli

[English](README.md) · [简体中文](README.zh-CN.md) · [Documentation](docs/index.md)

<p><a href="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml"><img alt="ci" src="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml/badge.svg"></a> <a href="https://github.com/shitianyaa/nitter-cli/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/shitianyaa/nitter-cli?style=flat-square"></a> <a href="go.mod"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/shitianyaa/nitter-cli?style=flat-square"></a> <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/shitianyaa/nitter-cli?style=flat-square"></a></p>

[Install](#install) · [Quick start](#60-second-quick-start) ·
[Choose your interface](#choose-your-interface) · [Documentation](#documentation)

`nitter` is an unofficial command-line client for **public tweets**. Data comes
from **FxTwitter's public API** (the default fast lane — no account, no
credentials) with **Nitter instances you run yourself** as the private
fallback, the List path, and — one call at a time, via `--instance URL` — the
instance path for timelines and single statuses. It fetches user timelines,
search results, list timelines, single statuses, conversations, self-threads,
following and follower lists, profiles, account-name completions, quote tweets
and trends, watches sources with persistent dedup
state, and downloads media — a flexible CLI built for agents and schedulers
(cron, systemd timers, Hermes), consumed as NDJSON.

It is also a public Go SDK (`github.com/shitianyaa/nitter-cli/sdk`, package
`nitter`) with a stable, additive-only data model.

Built on **[Nitter](https://github.com/zedeus/nitter)** (the instance software;
upstream was archived in September 2026, so this project targets instances you
run yourself) and **[FxTwitter](https://github.com/FxEmbed/FxEmbed)** (now
maintained as FxEmbed — the public API behind the fast lane). Its shape — the
bilingual docs, the repo-local process skills, the versioned changelog and the
release matrix — follows **[javdb-cli](https://github.com/FlanChanXwO/javdb-cli)**
and **[pixiv-cli](https://github.com/FlanChanXwO/pixiv-cli)** by
[FlanChanXwO](https://github.com/FlanChanXwO). Created and maintained by
[shitianyaa](https://github.com/shitianyaa).

## Why nitter-cli?

- **FxTwitter fast lane, Nitter depth** — `fetch_backend` chooses the routing
  for the timeline and status surfaces: `mix` (default) tries FxTwitter's
  public API first — no account, no credentials — and falls back to your own
  instances on failure; `fx` pins the fast lane. To send ONE call to a single
  instance, pass `--instance URL` (a testing aid, not a mode; `user`, `search`,
  `get` and `list` only). `list` always needs Nitter, while `followers` /
  `thread` / `typeahead` / `comments` / `profile` / `following` / `quotes` /
  `trends` and `circle refresh` always reach `api.fxtwitter.com` instead (they
  have no Nitter equivalent).
- **Flexible instance policy** — point it at any Nitter instance you control:
  configure a rotation set (`[[instances]]`, tried strictly in config order,
  failed entries cool down), override per command with `--instance`, and
  optionally authenticate with host-scoped basic auth (`username` **and**
  `password` set). Credentials can only ever ride requests addressed to their
  own instance — third-party endpoints never see them.
- **Public-tweet retrieval** — `user` (RSS first, falling back to the HTML user
  page when the feed fails or is empty), `search` (newest-first by default,
  `--sort top` for the popular-results feed), `list`, and single statuses
  via `get`. `list` is strictly isolated:
  every list fetch always runs on your own instances (FxTwitter has no List
  endpoint). No bundled instances, no login, no bypassing of access controls.
- **Social graph & discovery** — `following` lists who an account follows and
  `followers` lists who follows it, `profile` prints an account card,
  `typeahead` completes handles from a prefix (completion, not search),
  `search --type user` finds creators and
  artists by name, `trends` shows what X is talking about right now, and
  `quotes` digs up the quote-tweet derivatives of a status — all credential-free.
- **Conversations & reply trees** — `comments` pulls a status's thread and
  replies (sorted by likes or recency) and `thread` prints the full self-thread
  a status belongs to, root first — the fast way to reach an author's
  self-replies with hidden links or to read a serialized thread.
- **Creator circles** — `nitter circle` curates themed rosters in
  `~/.nitter-cli/circles.toml` (`list` / `show` / `refresh` / `suggest` / `add` / `remove` / `run`):
  `suggest` mines a handle's following list and retweet authors for new
  candidate members, then on-demand discovery and pipeline streaming,
  distinct from scheduled `watch` subscriptions.
- **Composable pipelines** — data commands emit `nitter.pipeline/v1` NDJSON
  automatically whenever stdout is a pipe, so
  `nitter search "..." | nitter download` needs no flags; `--json` extracts
  whole documents and explicit flags always win.
- **Scheduler-ready watch** — `watch --once` runs exactly one deduplicated
  cycle against persistent state and exits; the `--max-new-overflow keep`
  policy re-emits burst overflow on later cycles instead of losing it (宁重勿丢).
- **Media downloads with templates** — a video status converges to its ONE best
  file (bitrate-ranked, remaining candidates as fallbacks), an image-only
  status to every image, `--kind cover` to just the cover; `filename_template`
  / `directory_template` config keys (plus `--filename-template`) name and
  place files via `{id}` `{seq}` `{user}` `{kind}` `{ext}` placeholders.
- **Observable instances** — `instances test` probes each instance's RSS and
  user-timeline capabilities by default (`--full` adds the search probe,
  `--list-id ID` the list probe), one report line each; the report is the
  product.
- **Manageable state** — `config path/get/set/unset` for the thirteen scalar
  keys, `seen list/clear [--state-dir]` for the watch dedup state; atomic
  writes, a corrupt state file is a hard error (never a silent reset).
- **Honest, verified updates** — `update --check [--prerelease] [--json]`
  compares against the latest GitHub release by strict semver and writes
  nothing; `update` additionally offers to install after confirmation, and
  `--confirm` skips the prompt for scripts. The installer verifies the archive
  against the release's `checksums.txt` and the staged binary's version before
  replacing the executable, so a failed verification leaves the current
  installation untouched. `go install` installations are refused with the
  matching `go install` line, and agents must not run `update` without the
  user's authorization.
- **Public Go SDK** — typed models (`Tweet`, `Profile`, `Conversation`, `Trend`,
  …) whose JSON keys never disappear, a narrow `Transport` boundary, and
  redacted errors; the CLI's commands consume the same surface.

## Install

### Release archive (recommended)

Download the archive for your platform from
[GitHub Releases](https://github.com/shitianyaa/nitter-cli/releases), then
verify it against the `checksums.txt` attached to the **same release**:

```bash
sha256sum -c checksums.txt --ignore-missing   # or an equivalent tool
```

Extract the `nitter` binary into a per-user directory that is on your `PATH`.
There is no installer script; the archive is the whole product.

### Source build

Requires Go 1.27+:

```bash
sh scripts/build.sh          # produces ./nitter
./nitter --version           # nitter version <version> (or a dev line)
```

### Install with an AI agent

Copy this single prompt into Codex, Claude Code, Cursor, or another local AI
agent with terminal access:

```text
Install the latest stable nitter-cli from https://github.com/shitianyaa/nitter-cli for this machine: download only an official GitHub Release archive for the detected OS and architecture from the repository's Releases page, verify its SHA-256 against the checksums.txt attached to that same release before replacing anything, install the `nitter` binary into a per-user directory without administrator or root privileges, add that directory to the current user's PATH only if `nitter` is not already reachable (state every PATH change), ask before installing any missing prerequisite, never read or output ~/.nitter-cli/config.toml or any Nitter credentials during installation, verify with `nitter --version`, and report the installed version, the binary path, and every file and PATH change.

Also install the `nitter-cli` Skill that matches the same stable release tag (never main): download the full skills/nitter-cli/ directory from that tag's git tree into the agent skills directory the user confirms. Do not guess the skills path and do not follow the main branch for skill content.
```

## 60-second quick start

nitter-cli ships without instances, and the default `mix` mode already works:
`user`, `search`, `get`, `comments`, `following`, `followers`, `thread`,
`typeahead`, `profile`, `quotes`, `trends` and `search --type user` run through
FxTwitter's public endpoint out of the box. `circle refresh` also always uses
FxTwitter, whatever `fetch_backend` says (it sends every member's handle). Configure a Nitter instance you control for
`list` and as the fallback when Fx is unavailable. Don't have one? Deploy your
own with Docker — see the
[upstream wiki](https://github.com/zedeus/nitter/wiki) or the community
[self-hosting guide](https://github.com/sekai-soft/guide-nitter-self-hosting);
an AI agent can follow the skill's [deploy reference](skills/nitter-cli/references/deploy.md).

```bash
# 0. Configure your instance — `nitter config path` prints the config file
#    (created with commented examples on the first real command); add:
#      [[instances]]
#      url = "http://nitter.internal:8080"
#      # username = ""        # optional basic auth — sent only when BOTH are set
#      # password = ""
nitter config path

# Diagnose the instance: capability probes, one line each (rss / user_html /
# search / list / latency; cells are ok, fail(<reason>) or -)
nitter instances test
nitter instances test http://nitter.internal:8080 --full

# Fetch a timeline as JSON for Hermes or any agent
nitter user NASA --limit 10 --json

# Discovery extras — no instance or account needed
nitter trends --limit 10 --json                   # what X is talking about now
nitter following NASA --limit 10                  # who an account follows
nitter followers NASA --limit 10                  # who follows an account
nitter profile NASA                               # account card
nitter typeahead nas                              # complete handles from a prefix (completion, NOT search)
nitter comments 2100031016471818431 --limit 10    # reply tree (author self-replies live here)
nitter thread <STATUS_ID> --json                  # the full self-thread containing a status, root first
nitter quotes <STATUS_ID> --media-only | nitter download   # derivative media from quote tweets
nitter circle run ai_researchers --limit 1            # stream a curated creator roster

# Piped output needs no flags — data commands emit NDJSON on their own,
# so the stream feeds the downloader directly
nitter search "#AI" --limit 20 | nitter download --output ./media

# Popular results instead of newest-first (both backends get the ordering)
nitter search "#AI" --sort top --limit 10 --json

# Download a video at the lowest quality tier
nitter download https://x.com/NASA/status/2102761519985332442 --quality low

# Fetch only the video's cover image
nitter download https://x.com/NASA/status/2102761519985332442 --kind cover

# Set up a deduplicated watch from your scheduler: one cycle per run,
# new tweets only, state persists between runs
*/10 * * * * nitter watch user:NASA tag:#AI --once --ndjson >> /var/log/nitter-watch.ndjson 2>>/tmp/nitter-watch.err
```

Run `nitter --help` or open the [complete CLI reference](docs/en/cli-reference.md)
for every command, flag, configuration key, and update behavior.

## Choose your interface

### CLI

Use the human table interactively; `--json` for whole documents, `--ndjson`
for record streams — or just pipe: a non-TTY stdout is the NDJSON default.

```bash
nitter user NASA --limit 10                      # tab-separated rows on a TTY
nitter user NASA --limit 10 --json               # one object / an array
nitter user NASA --limit 10 --ndjson             # one nitter.pipeline/v1 envelope per tweet
nitter search "#AI" --sort top --limit 10 --json # popular-results ordering
nitter search "#AI" --limit 20 | nitter download # auto-NDJSON, no flags needed
```

### Go SDK

The public SDK is the package `github.com/shitianyaa/nitter-cli/sdk`
(package name `nitter`) — the same surface the CLI consumes. It composes a
`Client` from a rotation set, a narrow `Transport` interface, and an instance
cooldown; data models (`Tweet`, `Page[T]`, …) marshal unconditionally (no
`omitempty` on data fields) under an additive-only stability contract, and
errors are redacted (no credentials, no query strings, no headers/bodies).

```go
import "github.com/shitianyaa/nitter-cli/sdk"

client, err := nitter.New(
    nitter.WithInstances([]nitter.Instance{
        {URL: "http://nitter.internal:8080"},
    }),
    nitter.WithHTTPClient(transport), // implements Transport
    nitter.WithCooldown(30 * time.Second),
)
```

`nitter.Instance` carries the optional basic-auth pair of an instance; the
redaction contract keeps it out of every error and log. The
[SDK guide](docs/en/sdk.md) documents the stability contract, models,
pagination, and error kinds.

### Agent skill

`skills/nitter-cli/` is the operator skill for AI agents: command routing, the
instance trust boundary, state-changing commands that need consent, discovery
recipes, and the error table. It ships with the release and is what the install
prompt above wires into your agent's skills directory.

## Configuration

`~/.nitter-cli/config.toml` (TOML, mode 0600), created with commented examples
on the first real command; on Windows the directory lives under your user
profile (`nitter config path` prints the exact location). Precedence: CLI flag >
environment > file > built-in default. `nitter config get/set/unset` read and write the thirteen scalar keys;
the `[[instances]]` and `[[watch.sources]]` array tables are hand-edited. The
[configuration table](docs/en/cli-reference.md#nitter-config) lists every key
with its type, default, environment override, and the instance credential rules.

```bash
nitter config path                        # print the file location
nitter config set default_limit 50        # a state change: confirm before running
nitter config get fetch_backend
```

## Documentation

| Guide | Use it for |
| --- | --- |
| [CLI reference](docs/en/cli-reference.md) | Every command, flag, output mode, exit code, watch semantics, configuration key, and the FAQ |
| [Agent operator skill](skills/nitter-cli/SKILL.md) | Safe agent routing, instance trust, state changes, discovery, and errors |
| [Go SDK](docs/en/sdk.md) | Public client, models, and options |
| [Architecture (Simplified Chinese)](docs/maintainers/architecture.md) | Package boundaries and runtime flow |
| [Development (Simplified Chinese)](docs/maintainers/development.md) | Toolchain, tests, platform builds, packaging, and releases |
| [Documentation map](docs/index.md) | Localized public contracts and maintainer guides |
| [Contributing](CONTRIBUTING.md) | Local quality gates and contribution rules |
| [Changelog](changelog/README.md) | Versioned bilingual release notes |

## Contributing

Bug reports, documentation fixes, tests, and focused features are welcome.
Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request; discuss
large or compatibility-sensitive changes first. The maintainer-facing contracts
(package boundaries, review checklist, documentation routing) live in
[docs/maintainers/](docs/maintainers/).

## Disclaimer

1. **Public tweets only.** nitter-cli fetches exclusively publicly accessible
   tweets — through the public FxTwitter API (the default `mix` / `fx`
   backends) and through Nitter instances you run yourself (the fallback path,
   every `list` fetch, and any `--instance` call). It bundles no instances,
   performs no login, and provides no capability to bypass access controls or
   solve challenges.
2. **Only use instances you control and trust.** The CLI follows the media and
   redirect URLs returned by the configured instance. Isolate your internal
   services (Redis, cloud metadata endpoints, admin panels) from the network path
   of nitter-cli and its instances.
3. **No bypassing of access controls.** If a page requires login, is a challenge,
   or the instance is rate-limited, the CLI reports the classified error instead of
   working around it. Respect X's terms and your instances' capacity; this project
   is not affiliated with X Corp. or the Nitter project.

## License

[MIT](LICENSE) © shitianyaa
