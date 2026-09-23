# Review Checklist

审查本仓库改动时按下面的顺序核对。每项都要能落到具体文件与行号，不能确认的列为 Open Questions。

## 架构边界（AGENTS.md 红线）

- `cmd/nitter` 是否仍只委托？命令树、Streams 与组装是否仍只在 `internal/cli/root.go`？
- 子命令包是否仍不导入 `internal/cli`（`invocation`/`pipeline`/`result`/`client` 为既有许可）？
- 命令取数是否只经顶层 `sdk/`（package `nitter`）？是否有人从命令包直连 `internal/nitter/*`、`internal/fxtwitter` 或 `internal/media`？
- `internal/cli/client` 是否仍是唯一导入 `internal/{nitter/*,fxtwitter,media}` 的 CLI 层包（R11）？
- `internal/common` 是否仍不接收 `io.Writer`、不含用户文案？`internal/cli/result` 是否仍不编码 JSON、不建命令、不调 SDK？

## 行为与错误语义

- 是否引入了隐式超时、静默截断、静默降级或静默状态重置？
- 部分失败是否可见（stderr 警告 / error 信封），而不是被伪装成空成功？
- 退出码是否守约：未知子命令 → 1；参数/flag/输入契约值不合法 → 2（经 `invocation.UsageError` 包装）；运行失败 → 1；空结果 → 0？
- 输入校验是否先于任何网络（usage 错误必须在建 wiring 之前返回）？
- 上游端点缺失或不可用（例如容器版 Nitter 不带 search/list）是否诚实报告，而不是猜测？
- 新增错误的 `Op` 遵循 `<包>.<函数或策略>`（库层，如 `media.FetchToFile`）或裸命令名（命令层，如 `watch`）；
  不新增带空格的散文短语。`chooser` 是既有公开契约，不改名。

## 公开契约兼容性

- CLI flag、JSON 字段、NDJSON 信封与文本列是否保持既有脚本/agent 的兼容性？
- `sdk/` 模型是否仍遵守 additive-only 契约（只增字段，不移除、不重命名、不改变用途）？
- NDJSON 是否走 `jsonx` 语义（`SetEscapeHTML(false)`），而不是裸 `json.Marshal`？
- 新 flag 是否补齐了 usage-error（退出码 2）测试？

## 凭据与本地状态

- 是否避免打印、记录或测试夹带实例 basic-auth 凭据、代理凭据、`~/.nitter-cli/` 内容？
- `config set`、`seen clear`、`circle add` 等状态变更是否仍需显式授权，且未被静默绕过？
- 代理与实例错误是否只暴露脱敏信息（scheme / host，不含内嵌凭据）？

## 测试与文档

- 行为变更是否补了聚焦测试，且测试曾先证明失败？
- 是否运行了 `go test ./...`、`go vet ./...`、`gofmt -l .`、`sh scripts/build.sh`，以及改到 `e2e/` 覆盖范围时的 `bash e2e/run.sh`？
- 按 [文档路由](documentation-guidelines.md) 核对：CLI 行为/flag/输出语义 → 两个 locale 的 CLI reference + 双语 README + `skills/nitter-cli/` + `changelog/`；SDK/模型签名 → `docs/{en,zh-CN}/sdk.md` + `docs/maintainers/architecture.md`；构建/发布 → `docs/maintainers/development.md`。
- 新文档链接是否指向 `docs/<locale>/`、`docs/maintainers/` 或 `skills/nitter-cli/` 的权威路径？
- 提交信息是否为 Conventional Commits 单行英文小写 subject，且没有 AI 署名？
