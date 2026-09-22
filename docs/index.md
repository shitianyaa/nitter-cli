# nitter-cli Documentation

`nitter-cli` is an unofficial command-line client and public Go SDK for public
tweets — through the public FxTwitter API (the default fast lane) and through
self-hosted Nitter instances. Public interface documents are localized;
maintainer documents have one canonical version so contributors can share the
same architecture and delivery rules.

## User documentation

| Interface | English | 简体中文 |
| --- | --- | --- |
| Project overview | [README](../README.md) | [README](../README.zh-CN.md) |
| CLI reference | [English](en/cli-reference.md) | [简体中文](zh-CN/cli-reference.md) |
| Go SDK | [English](en/sdk.md) | [简体中文](zh-CN/sdk.md) |

English is the canonical public contract. Translations must preserve command,
flag, state-change, and error semantics; they should not be word-for-word
copies.

## Agent documentation

- [Agent skill](../skills/nitter-cli/SKILL.md): operating rules, command tiers,
  and semantics traps for driving the `nitter` binary from an AI agent.

## Maintainer documentation

- [Architecture](maintainers/architecture.md): package boundaries, runtime flow,
  and ownership.
- [Development](maintainers/development.md): environment, tests, builds, and
  release flow.
- [Agent collaboration rules](maintainers/agents/index.md): review checklist and
  documentation routing for repository work.

## For automation

Read [`AGENTS.md`](../AGENTS.md) at the repository root first. Then enter the
matching skill for the task:

| Task | Skill |
| --- | --- |
| Preparing a PR | [`.agents/skills/nitter-cli-pr/`](../.agents/skills/nitter-cli-pr/SKILL.md) |
| Diagnosing CI | [`.agents/skills/nitter-cli-ci/`](../.agents/skills/nitter-cli-ci/SKILL.md) |
| Reviewing changes | [`.agents/skills/nitter-cli-review/`](../.agents/skills/nitter-cli-review/SKILL.md) |
| Maintaining docs | [`.agents/skills/nitter-cli-docs/`](../.agents/skills/nitter-cli-docs/SKILL.md) |
| Commit messages | [`.agents/skills/nitter-cli-commit-message/`](../.agents/skills/nitter-cli-commit-message/SKILL.md) |
| Release notes and tags | [`.agents/skills/nitter-cli-release-notes/`](../.agents/skills/nitter-cli-release-notes/SKILL.md) |

`CLAUDE.md` only references `AGENTS.md`. Long-term rules are not duplicated into
each tool's config.

## Changelog

Versioned bilingual release notes live in [changelog/](../changelog/README.md);
the current drafting area is [changelog/unreleased/](../changelog/unreleased/en.md).
