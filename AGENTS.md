# AGENTS.md

默认离线验证：`go test ./...` + `sh scripts/build.sh`。

## 架构边界（不可违反）

- `cmd/nitter` 只委托；`internal/cli/root.go` 拥有命令树、流与组装；子命令包不导入 `internal/cli`。
- 命令取数只经顶层 `sdk/`（package nitter）；禁止导入 `internal/nitter/*` 或 `internal/fxtwitter` 协议细节。
- `internal/cli/client` 是唯一允许导入 `internal/{nitter/*,fxtwitter,media}` 的 CLI 层包（R11）。
- `internal/common` 不接收 io.Writer、不含用户文案；`internal/cli/result` 不编码 JSON、不建命令、不调 SDK。
- 任何变更不得引入隐式超时、静默截断、静默降级、静默状态重置。
- 退出码语义：未知子命令 → 退出码 1；参数/flag/输入契约值不合法 → 退出码 2（经 `invocation.UsageError` 包装）。

## 变更路由

- CLI 行为/flag/输出语义 → `docs/{en,zh-CN}/cli-reference.md` + 两语 README + `skills/nitter-cli/` + `changelog/`
- SDK/模型签名 → `docs/{en,zh-CN}/sdk.md` + `docs/maintainers/architecture.md`
- 构建/发布/CI → `docs/maintainers/development.md`
- 提交信息：Conventional Commits 单行英文小写 subject，不加任何 AI 署名。

完整路由表见 [`docs/maintainers/agents/documentation-guidelines.md`](docs/maintainers/agents/documentation-guidelines.md)。

## 文档路由

| 任务 | 权威文档 / 入口 |
| --- | --- |
| 架构与包边界 | [`docs/maintainers/architecture.md`](docs/maintainers/architecture.md) |
| 环境、测试、构建、发布 | [`docs/maintainers/development.md`](docs/maintainers/development.md) |
| 协作细则总入口 | [`docs/maintainers/agents/index.md`](docs/maintainers/agents/index.md) |
| 审查标准 | [`docs/maintainers/agents/review-checklist.md`](docs/maintainers/agents/review-checklist.md) |
| 文档职责与变更路由 | [`docs/maintainers/agents/documentation-guidelines.md`](docs/maintainers/agents/documentation-guidelines.md) |
| 面向使用者的产品 skill | [`skills/nitter-cli/SKILL.md`](skills/nitter-cli/SKILL.md) |
| 贡献入口 | [`CONTRIBUTING.md`](CONTRIBUTING.md) / [`CONTRIBUTING.zh-CN.md`](CONTRIBUTING.zh-CN.md) |
| 发布说明 | [`changelog/README.md`](changelog/README.md) |

## Repo-local skills

本仓库自身的流程 skill 位于 [`.agents/skills/`](.agents/skills/)；只在对应任务中读取：

| 任务 | Skill |
| --- | --- |
| 准备/创建/维护 PR | [`.agents/skills/nitter-cli-pr/`](.agents/skills/nitter-cli-pr/SKILL.md) |
| 诊断 CI 与本地门禁 | [`.agents/skills/nitter-cli-ci/`](.agents/skills/nitter-cli-ci/SKILL.md) |
| 审查改动 | [`.agents/skills/nitter-cli-review/`](.agents/skills/nitter-cli-review/SKILL.md) |
| 维护文档 | [`.agents/skills/nitter-cli-docs/`](.agents/skills/nitter-cli-docs/SKILL.md) |
| 生成 commit message | [`.agents/skills/nitter-cli-commit-message/`](.agents/skills/nitter-cli-commit-message/SKILL.md) |
| 发布说明与打 tag | [`.agents/skills/nitter-cli-release-notes/`](.agents/skills/nitter-cli-release-notes/SKILL.md) |

`CLAUDE.md` 只引用本文件，不维护第二份规则。产品 skill（`skills/nitter-cli/`）面向分发，
不是仓库协作规则的替代品。

## 红线

- 不提交实例 basic-auth 凭据、代理凭据、`~/.nitter-cli/` 本地状态、构建产物或测试产物。
- `Progress/`、`docs/superpowers/`、`Testignore/` 都是本地目录，永不提交。
- 状态变更类命令（`config set`、`seen clear`、`circle add`）需用户显式授权，授权不跨命令沿用。
