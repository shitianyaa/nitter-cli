# 为 nitter-cli 贡献

[English](CONTRIBUTING.md) | 简体中文

感谢你帮助改进 `nitter-cli`。我们欢迎聚焦的 bug report、文档修复、测试和边界清晰的功能。

## 开始之前

- 先检索已有 issue 和 pull request，避免重复提交。
- 大功能、public API 变更、新依赖，或取数/凭据模型的变更，应在实施前讨论。
- 保持改动聚焦；无关清理更适合单独提交 pull request。

**绝不要包含密钥或私有数据。** Issue、fixture、commit 和 CI 日志都不得携带实例 basic-auth 凭据、代理凭据、`~/.nitter-cli/` 内容（config、circles、seen 状态），或你不希望公开的私有实例 URL。

## 开发环境

受支持的源码构建使用 `go.mod` 声明的 Go 版本与标准 Go toolchain，没有 C 或 native 依赖。

在仓库根目录构建和测试：

```bash
go test ./...
sh scripts/build.sh
./nitter --version
```

默认验证完全离线，不需要任何实例或凭据。离线 e2e 门禁、opt-in 真实检查与平台细节见[开发流程](docs/maintainers/development.md)。

## 架构边界

- `cmd/nitter` 只委托；`internal/cli/root.go` 拥有命令树、流与组装。子命令包不导入 `internal/cli`。
- 命令取数只经顶层 `sdk/`（package `nitter`）；不得从命令包导入 `internal/nitter/*`、`internal/fxtwitter` 或 `internal/media` 的协议细节。
- `internal/cli/client` 是唯一允许导入 `internal/{nitter/*,fxtwitter,media}` 的 CLI 层包（R11）。
- `internal/common` 不接收 `io.Writer`、不含用户文案；`internal/cli/result` 不编码 JSON、不建命令、不调 SDK。
- 文件应聚焦于一个职责或少数紧密相关职责。

修改这些边界前，请阅读[架构说明](docs/maintainers/architecture.md)与仓库 [AGENTS.md](AGENTS.md)。

## 使用测试驱动开发

代码变更采用 red-green-refactor：

1. 添加一个会因目标行为尚未实现而失败的聚焦测试。
2. 实现让它通过的最小完整变更。
3. 在不改变已验证公开行为的前提下重构。
4. 先运行聚焦测试，再运行相关回归。

可行时通过 public boundary 测试公开行为。不得把真实的取数、解析、文件系统或网络失败隐藏为空成功或静默 fallback；不得增加无依据的固定超时、截断、条数上限、重试上限或隐藏降级。

退出码是契约的一部分：未知子命令退出 1；参数、flag 或输入契约值不合法退出 2（经 `invocation.UsageError` 包装）。新增 flag 时同步补上 usage-error（退出码 2）测试。

针对第三方服务（FxTwitter）与自建 Nitter 实例的真实取数均为 opt-in；未经用户授权，不得对其实例或账号运行。

## 文档

修改命令、flag、SDK API、配置键、环境变量、输出契约、退出码行为或已知限制时，在同一 pull request 同步文档。

- 保持 `README.md` 与 `README.zh-CN.md` 的行为语义对应。
- 保持 `docs/en/` 与 `docs/zh-CN/` 下两个语言版本的行为语义对应；不得用未翻译占位内容冒充对应语言。
- `docs/maintainers/` 下的维护者文档只保留一份 canonical 版本。
- SDK 签名或包边界变化时，更新 `docs/{en,zh-CN}/sdk.md` 或 `docs/maintainers/architecture.md`。
- 发布说明位于 `changelog/`，英文与简体中文对应；用户可感知的变化写条目，纯内部整理不写。见 [changelog/README.md](changelog/README.md)。
- CLI 命令、flag 或安全语义变化时检查 `skills/nitter-cli/`。

稳定规则只在一个权威文档中定义，其他位置应链接过去，避免复制大段内容。完整路由表见 [`docs/maintainers/agents/documentation-guidelines.md`](docs/maintainers/agents/documentation-guidelines.md)。

## Pull request checklist

请求 review 前确认：

- [ ] 改动保持聚焦，并说明了用户可感知行为。
- [ ] 新增或修改代码有聚焦测试，并且测试曾先证明失败。
- [ ] `go test ./... -count=1` 通过。
- [ ] `go vet ./...` 通过。
- [ ] `gofmt -l .` 无输出。
- [ ] `sh scripts/build.sh` 通过。
- [ ] 改动涉及 `e2e/` 覆盖的契约时，`bash e2e/run.sh` 通过。
- [ ] `git diff --check` 通过。
- [ ] 需要同步的英文与简体中文文档已对应。
- [ ] 未包含凭据、本地状态或机器相关产物。

Commit message 使用 Conventional Commits——单行、英文小写开头，例如 `feat(circle): add circle suggest` 或 `fix(media): derive file extensions from the decoded URL path`。除非未来规范明确要求，项目不要求 CLA、DCO sign-off 或 signed commit。

## 许可证

提交贡献即表示你同意该贡献可按仓库的 [MIT License](LICENSE) 分发。
