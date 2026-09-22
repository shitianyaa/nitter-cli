---
name: nitter-cli-docs
description: Maintain nitter-cli documentation; locale and routing live in docs/maintainers/agents/documentation-guidelines.md.
---

# nitter-cli Docs

新增、修改或审查本仓库文档。文件职责与变更路由以
`docs/maintainers/agents/documentation-guidelines.md` 为准；本文件只定义流程，不复制路由表。

## 流程

1. 读取文档规范，确定内容应落在 locale（`docs/{en,zh-CN}/`）、维护者文档（`docs/maintainers/`）、`changelog/`、产品 skill（`skills/nitter-cli/`）还是 repo-local skill（`.agents/skills/`）。
2. 先更新英文 public contract，再在同一变更中更新简体中文对应内容；命令、路径、包名和 code-id 保持英文。
3. 修改已翻译的 public contract 时保持行为语义对应；允许自然调整句式，不得让不同语言出现不同命令、flag、安全语义或限制。
4. 维护者文档只保留一份 canonical 版本（中文），不做双语镜像；新增文件时同步更新 `docs/maintainers/agents/index.md` 的地图。
5. 产品 skill 只写流程、安全边界与易混淆语义，不复制完整 flag 表——命令语义以已安装二进制的 `--help` 为准。
6. 发布说明只写入 `changelog/`：用户可感知的变化写双语条目，纯内部整理不写。
7. 构建、发布、workflow 或门禁文档变化时，同步检查 `docs/maintainers/development.md` 与 README；不要只更新用户 locale 文档。
8. 完成后检查链接、locale 导航与 `git diff --check`。

## 约束

- 不把长篇架构或发布说明写回 `AGENTS.md`。
- 不为纯内部整理新增用户可见 changelog 条目。
- 不宣称本工具具备不存在的写能力（发推、点赞、关注等）或未实现的分发渠道。
