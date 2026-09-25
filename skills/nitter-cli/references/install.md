# Explicit installation workflow

Use this workflow only when the user explicitly asks to install, upgrade, or
repair the `nitter` binary, or to install the matching Skill. Installation
changes files and may change the user PATH; state the detected platform,
architecture, destination, and the intended PATH action before running
anything.

## Approved sources

- Repository: `https://github.com/shitianyaa/nitter-cli`
- Release assets: `https://github.com/shitianyaa/nitter-cli/releases` — the
  platform archive (`.tar.gz` / `.zip`) for the detected OS and architecture,
  plus the `checksums.txt` attached to the SAME release tag.

There are no install scripts for this project. Never fetch, improvise, or
execute an installer; never substitute a mirror, a package copied from chat,
or any URL that is not an official GitHub Release asset of this repository.

## Upgrading an existing installation

`nitter update` is part of the installed binary — it is NOT one of the
third-party installers this file forbids, and running it is the supported
upgrade path. It reads only the `proxy` key from the config; it never reads
credentials. The manual `Platform flow` (marked fallback) below is for a first
install, and for an install whose source cannot be determined.

1. `nitter update --check` (`--check --json` for scripts) is read-only: it
   reports the current and latest versions and writes nothing. Installing always
   needs the separate authorization in step 2.
2. Get the user's explicit authorization. `update --confirm` is a state change;
   `--confirm` exists for the user's own commands, not for a downloaded script,
   and is not a grant of permission.
3. Back up the binary you are about to replace, and report the backup path to
   the user. Resolve the real target first (`command -v nitter`, then follow
   symlinks) — the updater replaces the resolved file — and keep the backup
   outside every PATH directory. This matters most on Unix, where the updater
   renames over the target and leaves no backup at all. Windows leaves an
   `<exe>.old`, but the next `nitter update` deletes it, so that is not a
   rollback point either.
4. Run `nitter update --confirm`. Every failure leaves the current installation
   untouched.
5. Close-out: `nitter --version` must print `nitter version <v>`.
6. Update the Skill in the same step (see "Skill installation"): the binary and
   the Skill are released as one pair. If the matching tag tree cannot be
   fetched, stop and report — never upgrade only the binary.
7. Summarize the change from the release's curated notes: the
   `changelog/vX.Y.Z/{en,zh-CN}.md` files in the release tag's git tree — the
   same text the published GitHub Release body carries. Take the tag from the
   version `--check` reported. Do not read `changelog/unreleased/`: the release
   workflow never reads it.

`nitter update` refuses a `go install` installation and prints the exact
`go install …@<tag>` line to use instead — follow that line rather than
overwriting a toolchain-managed binary. A development build has nothing to
replace and exits 0.

## Platform flow (fallback)

1. Detect OS and architecture. Do not read, create, or modify
   `~/.nitter-cli/config.toml` or any Nitter credential as part of
   installation — there is nothing to configure yet, and credential hygiene
   starts here (hard rule 1 of SKILL.md).
2. Download the archive AND the `checksums.txt` from the same release tag.
   Verify the archive's SHA-256 against `checksums.txt` before extracting or
   replacing anything; a failed or unverifiable checksum aborts the whole
   install.
3. Extract the `nitter` binary into a per-user directory (for example
   `~/.local/bin` on Linux/macOS, or a directory under the user profile on
   Windows). Never request administrator or root privileges.
4. PATH: if the chosen directory is not already on the user's PATH, add it to
   the CURRENT USER's PATH only, and state the exact change; never edit the
   system PATH. If `nitter` is already reachable, no PATH action is needed.
5. If a prerequisite (`curl`, `tar`, a checksum tool) is missing, stop and ask
   the user before installing it.
6. Close-out: run `nitter --version` and require the line
   `nitter version <v>`. Report the installed version, the binary path, and
   the PATH action (added / already reachable / none needed), plus any
   warning, exactly.
7. Post-install backend briefing: configure NOTHING automatically. Before
   the first data fetch, explain the two `fetch_backend` modes and ask the
   user to pick one; configure only what that mode needs:
   - `mix` (default): the FxTwitter fast-lane commands work out of the box;
     say explicitly that handles and queries are sent to the third-party
     `api.fxtwitter.com` (credentials never are); `list` and the fallback
     need a self-hosted instance.
   - `fx`: fast lane only, no Nitter fallback — when Fx fails the command
     fails. `config set fetch_backend fx` (a state change: consent each
     time) or the `NITTER_FETCH_BACKEND` env override.
   There is no self-hosted-only mode. `--instance URL` sends ONE call to a
   single instance, and it applies only to the commands that have an instance
   path (`user`, `search`, `get`, `list`) — the Fx-only commands ignore it.
   To go fully self-hosted, run a Nitter instance and use `list` (always
   instance-served) plus `--instance` for the rest; an instance is required
   first, so route to deploy.md's "Finding an existing instance" flow.
   Red line (all modes): never fill in a public instance on your own;
   an `[[instances]]` entry is written only after `instances test` passes
   against the user's own instance.

## Skill installation

The Skill versioned for a release lives in that release tag's git tree under
`skills/nitter-cli/`. Install or upgrade it from the SAME release tag as the
binary — never from `main`:

- copy the full `skills/nitter-cli/` directory (`SKILL.md` plus `references/`)
  into the agent skills directory the user confirms;
- do not guess the skills path, and do not mix Skill content across releases;
- command syntax always defers to the installed binary's
  `nitter <cmd> --help`;
- overwrite the installed directory rather than merging into it — a stale file
  left behind is Skill content mixed across releases;
- after the copy, confirm the installed `SKILL.md`'s `version` field equals
  `nitter --version`: the binary and the Skill are one pair, upgraded together
  and never one without the other;

## Refusal conditions

Refuse and report instead of improvising when:

- the checksum does not match, or `checksums.txt` is missing or from a
  different release tag;
- the only available source is a mirror, an unofficial copy, or a
  third-party package;
- the user asks for an install script (none exists — offer the manual
  Release-archive route in `Platform flow` instead);
- the install would require administrator/root privileges or a system-PATH
  edit without the user's explicit consent.
