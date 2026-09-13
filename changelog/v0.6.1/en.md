# v0.6.1 — 2026-09-13

Documentation and agent-guidance release: no binary behavior changes.

## Added

- Agent deployment guidance: a new skill reference
  (`skills/nitter-cli/references/deploy.md`) covering the ask-first flow —
  an existing instance is found by its deployment traces and verified with
  `instances test` before any config edit; when the user has none, the agent
  offers a Docker deployment and proceeds only on explicit consent — plus
  the default Docker
  Compose deployment of Nitter bound to `127.0.0.1:8080` (the upstream
  compose file, the one-line `redisHost` change, and the required
  real-account `sessions.jsonl` with burner-account and credential-handling
  guidance), and the operational notes (same-machine binding, SSH-tunnel /
  reverse-proxy bridging for remote CLIs, the account-ban and cease-and-desist
  risk disclosure). The README quick start links the upstream wiki and the
  community self-hosting guide for readers without an agent.

## Fixed

- Release-notes hygiene: versioned changelog files no longer carry the
  `Unreleased` drafting header, and the changelog index table lists the
  released versions. GitHub Release bodies now carry the bilingual notes
  (English first, Simplified Chinese second) as this index has always
  claimed; the bodies of the two previous releases were backfilled
  accordingly.
