# nitter-cli 文档导航

`nitter-cli` 是一个非官方的命令行客户端与公开 Go SDK，获取公开推文——
经由公共 FxTwitter API（默认快车道）与自建 Nitter 实例。公开接口文档提供
双语；维护者文档只有一个权威版本，便于贡献者共享同一套架构与交付规则。

## 用户文档

| 接口 | English | 简体中文 |
| --- | --- | --- |
| 项目总览 | [README](../README.md) | [README](../README.zh-CN.md) |
| CLI 参考 | [English](en/cli-reference.md) | [简体中文](zh-CN/cli-reference.md) |
| Go SDK | [English](en/sdk.md) | [简体中文](zh-CN/sdk.md) |

英文版是公开契约的权威版本。翻译必须保留命令、flag、状态变更与错误语义，不必
逐字对照。

## Agent 文档

- [Agent Skill](../skills/nitter-cli/SKILL.md)：AI agent 驱动 `nitter`
  二进制的操作规则、命令分级与语义陷阱。

## 维护者文档

- [架构说明](maintainers/architecture.md)：包边界、运行期流程与职责归属。
- [开发指南](maintainers/development.md)：环境、测试、构建与发布流程。
- [Agent 协作细则](maintainers/agents/index.md)：审查清单与文档变更路由。

## 面向自动化

先读仓库根目录的 [`AGENTS.md`](../AGENTS.md)，再按任务进入对应 skill：

| 任务 | Skill |
| --- | --- |
| 准备 PR | [`.agents/skills/nitter-cli-pr/`](../.agents/skills/nitter-cli-pr/SKILL.md) |
| 诊断 CI | [`.agents/skills/nitter-cli-ci/`](../.agents/skills/nitter-cli-ci/SKILL.md) |
| 审查改动 | [`.agents/skills/nitter-cli-review/`](../.agents/skills/nitter-cli-review/SKILL.md) |
| 维护文档 | [`.agents/skills/nitter-cli-docs/`](../.agents/skills/nitter-cli-docs/SKILL.md) |
| commit message | [`.agents/skills/nitter-cli-commit-message/`](../.agents/skills/nitter-cli-commit-message/SKILL.md) |
| 发布说明与打 tag | [`.agents/skills/nitter-cli-release-notes/`](../.agents/skills/nitter-cli-release-notes/SKILL.md) |

`CLAUDE.md` 只引用 `AGENTS.md`，不维护第二份规则；长期规则不复制进各工具配置。

## 变更日志

版本化双语发布说明位于 [changelog/](../changelog/README.md)；当前草稿区在
[changelog/unreleased/](../changelog/unreleased/zh-CN.md)。
