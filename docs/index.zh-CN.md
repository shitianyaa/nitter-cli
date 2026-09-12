# nitter-cli 文档导航

`nitter-cli` 是一个非官方的命令行客户端与公开 Go SDK，经自建 Nitter 实例获取
公开推文。公开接口文档提供双语；维护者文档只有一个权威版本，便于贡献者共享
同一套架构与交付规则。

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

## 变更日志

版本化双语发布说明位于 [changelog/](../changelog/README.md)；当前草稿区在
[changelog/unreleased/](../changelog/unreleased/zh-CN.md)。
