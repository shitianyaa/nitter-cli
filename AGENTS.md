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

- CLI 行为/flag/输出语义 → `docs/{en,zh-CN}/cli-reference.md` + 两语 README + `skills/nitter-cli/` + `changelog/unreleased/`
- SDK/模型签名 → `docs/{en,zh-CN}/sdk.md` + `docs/maintainers/architecture.md`
- 构建/发布 → `docs/maintainers/development.md`
- 提交信息：Conventional Commits 单行英文小写 subject。
