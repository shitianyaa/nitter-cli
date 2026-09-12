# twitter-cli Documentation

`twitter-cli` is an unofficial command-line client and public Go SDK for public
tweets via self-hosted Nitter instances. Public interface documents are
localized; maintainer documents have one canonical version so contributors can
share the same architecture and delivery rules.

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

- [Agent skill](../skills/twitter-cli/SKILL.md): operating rules, command tiers,
  and semantics traps for driving the `twitter` binary from an AI agent.

## Maintainer documentation

- [Architecture](maintainers/architecture.md): package boundaries, runtime flow,
  and ownership.
- [Development](maintainers/development.md): environment, tests, builds, and
  release flow.

## Changelog

Versioned bilingual release notes live in [changelog/](../changelog/README.md);
the current drafting area is [changelog/unreleased/](../changelog/unreleased/en.md).
