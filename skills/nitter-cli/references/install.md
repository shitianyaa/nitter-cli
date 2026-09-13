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

## Platform flow

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

## Skill installation

The Skill versioned for a release lives in that release tag's git tree under
`skills/nitter-cli/`. Install or upgrade it from the SAME release tag as the
binary — never from `main`:

- copy the full `skills/nitter-cli/` directory (`SKILL.md` plus `references/`)
  into the agent skills directory the user confirms;
- do not guess the skills path, and do not mix Skill content across releases;
- command syntax always defers to the installed binary's
  `nitter <cmd> --help`.

## Refusal conditions

Refuse and report instead of improvising when:

- the checksum does not match, or `checksums.txt` is missing or from a
  different release tag;
- the only available source is a mirror, an unofficial copy, or a
  third-party package;
- the user asks for an install script (none exists — offer the manual
  Release-archive route above instead);
- the install would require administrator/root privileges or a system-PATH
  edit without the user's explicit consent.
