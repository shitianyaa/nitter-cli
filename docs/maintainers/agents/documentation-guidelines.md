# 文档规范

文档按读者、语言和稳定性分层，避免把产品用法、维护者设计和 agent 指令混入同一篇根文档。

## 目录职责

- `README.md`、`README.zh-CN.md`：GitHub 项目入口和安装/快速开始。
- `docs/en/`：英文公开接口契约（canonical）。
- `docs/zh-CN/`：简体中文公开接口文档。
- `docs/maintainers/`：架构、开发流程和协作规则；每篇只保留一个 canonical 版本（中文），因为维护者内部协作只需要一份。
- `docs/maintainers/agents/`：agent 协作细则（详见本目录 `index.md`）。
- `docs/index.md`、`docs/index.zh-CN.md`：用户 locale 与维护者文档总导航。
- `CONTRIBUTING.md` / `CONTRIBUTING.zh-CN.md`：GitHub 可发现的贡献入口。
- `changelog/`：权威的双语版本化发布说明与可选人工草稿区。
- `AGENTS.md`：agent 的短主规则和路由。
- `skills/nitter-cli/`：指导 agent 安全使用已安装 CLI 的产品 skill（面向使用者，随仓库分发）。
- `.agents/skills/`：本仓库自身的 PR / CI / review / docs / commit-message / release-notes 流程 skill。

## Locale 规则

- locale 目录使用 BCP 47 tag：`en`、`zh-CN`。
- 先更新英文 public contract，并在同一变更中更新已有翻译；允许自然改写，不得造成不同命令、flag、安全语义或限制。
- 维护者文档（`docs/maintainers/`）只保留一份 canonical 版本，不做双语镜像；链接必须指向该 canonical 路径。
- 某语言没有真实翻译时，在 `docs/index*.md` 链接到英文；不要把英文内容伪装成翻译。
- README 语言切换与文档总导航必须同步。

## 写作规则

- README 保持安装、能力边界、短示例和文档入口；完整 flag/错误/状态变更契约放 CLI reference。
- SDK 文档只描述公开 `sdk/` 路径（package `nitter`），不把 `internal/` 目录宣称为集成 API。
- 架构文档描述当前包边界与运行流，不记录上游逆向过程、签名推导或凭据细节。
- 影响长期包边界的约束写入 `docs/maintainers/architecture.md`；短期实现细节留在代码注释和测试。
- 产品 skill（`skills/nitter-cli/SKILL.md`）只写流程、安全边界与易混淆语义；命令语义始终以已安装二进制的 `--help` 为准，不复制完整 flag 表。
- 命令、配置、环境变量、输出语义、状态变更、构建或测试流程变化时，同步更新相应文档；用户可感知变化写入 `changelog/`，纯内部整理不新增用户可见条目。

## 变更路由

稳定规则只在一个权威文档中定义，其他位置链接过去，避免复制大段内容。

| 变更类型 | 必须同步 |
| --- | --- |
| CLI 行为 / flag / 输出语义 | `docs/{en,zh-CN}/cli-reference.md`、双语 README、`skills/nitter-cli/`、`changelog/` |
| SDK / 模型签名 | `docs/{en,zh-CN}/sdk.md`、`docs/maintainers/architecture.md` |
| 构建 / 发布 / CI | `docs/maintainers/development.md` |
| 包边界 / 调用边 | `docs/maintainers/architecture.md` |
| 协作规则 / 审查标准 | `docs/maintainers/agents/` |
