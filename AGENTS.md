# AGENTS.md

默认离线验证：`go test ./...` + `sh scripts/build.sh`。

> **本文件是通用、可公开的规则层**；绑定本机环境、代理、私有实例与账号的个性化
> 规则在仓库根目录的 `AGENTS.local.md`（仅本地，见 `.gitignore`，不入库），
> 两者是同一套约束的两层，不是可选补充。
> **任何写或执行动作之前，必须先 `read` 一次 `AGENTS.local.md`** —— 不凭记忆、
> 不假设本次会话已读过；其余触发时机见其开头的「读取时机」。冲突时以它为准
> （三层优先级见其 §0.6）。子代理简报只给路径，不复制其内容。
> 该文件不存在时按本文件的通用规则行事，并向用户确认环境细节。

## 通用协作规则

- **代理**：访问外网前先探测连通性，不通就问用户。不自填公共代理、不换未授权中继；
  代理需逐工具显式传（git / curl / gh 互不相通）。本机代理地址与传法见 `AGENTS.local.md`。
- **计划与草稿不入库**：计划、设计规范、调研草稿与事故记录一律不提交；引用时用
  相对路径，不复制进受跟踪文件。本地目录：`Progress/`、`docs/superpowers/`、
  `Testignore/`、`.superpowers/`、`AGENTS.local.md`。
- **子代理**：只用于审核计划与执行计划；代码审查、实现、设计、调研在当前会话完成。
  子代理不得再派子代理或自 spawn reviewer；派发参数与简要约束见 `AGENTS.local.md`。
  不可逆操作、安全敏感操作、工作区之外的副作用（merge / push 共享分支 / 发布）
  一律停下问用户。
- **Git**：不用 `git add -A`；`git add --renormalize .` 是批量操作，不能当定向 add 用。

## 门禁与 PR 流程

门禁体系移植自 FlanChanXwO/javdb-cli（MIT）。main 分支不跑任何 push 触发的 workflow——所有变更经 PR 进入并过门。

- **变更范围分类**：`.github/workflows/ci.yml` 先跑 `scripts/classify-change-scope.sh`，按 `.github/ci-change-scope.gitignore` 分档：纯文档改动（docs、changelog、skills、README 等）跳过 runner 重的检查；workflow/工具自身改动只跑质量门；代码改动跑质量门 + Windows parity。分类器从 base 提交 checkout 运行，PR 无法改写评判自己的规则。
- **PR 元数据门**（`pr-metadata.yml`）：PR 正文必须保留模板三段（变更点 / 验证步骤 / 检查清单）且清单全部勾选，检查结果以 `PR template gate` 与 `PR commands gate` 两个稳定 status 发布。正文失效 7 天未改的 PR 会被 `pr-invalid-close.yml` 自动关闭。
- **验证命令声明**（可选）：PR 正文的验证步骤里可声明恰好一个 ```commands fenced block，作为 CI 代跑的默认验证命令；命令必须落在 `tools/verification/command-whitelist.txt` 白名单内，不会执行 shell。
- **`/test` 评论验证门**（`pr-verification.yml`）：在 PR 里评论 `/test`（首个非空行、精确匹配）触发；触发者须是 PR 作者或有写权限。评论生命周期用 reaction 表达：👀 受理 → 🎉 通过 / 👎 失败，结果沉淀在一条带状态 JSON 的常驻评论里。执行身份绑定 `PR + HEAD SHA + 命令哈希`，重复触发去重、新提交自动取代旧 run。trusted runner 从 main tip checkout 工具链到 `_trusted/`，PR 代码只被构建成二进制执行，且全程不接触仓库 secrets；评论中的 commands block 会完全覆盖 PR 声明，声明无效一律 fail closed。单条命令默认 10 分钟超时。
- **triage**：`pr-triage.yml` 按路径打 `area: *` 标签并指派维护者；外部 PR 由 `auto-assign.yml` 自动请求 review。
- **发布信任链**（`release.yml`）：tag 必须解析到默认分支祖先链上的 commit（`tools/release verify-source`），双语 changelog 缺失即失败；构建后须经 `release-approval` environment 人工审批，发布 job 运行在独立的 `release` environment，产物保持 draft，公开由维护者手动完成。

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
| 本机环境、代理、踩坑记录（仅本地） | `AGENTS.local.md`（不入库，无超链接） |

## Repo-local skills

本仓库自身的流程 skill 位于 [`.agents/skills/`](.agents/skills/)。**执行下表动作前先读对应 skill，不凭记忆**：

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
- 状态变更类命令（`config set`、`seen clear`、`circle add`）需用户显式授权，授权不跨命令沿用。
- 不把代理凭据、私有实例 URL 或下载内容写进 commit、PR、issue、日志或最终报告。
