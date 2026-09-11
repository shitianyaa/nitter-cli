# twitter-cli MVP 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个 Go 版推特 CLI（`twitter`），从用户自建 Nitter 实例抓取公开推文，供 Hermes（Agent）完成「找推文、定时轮询、去重」，工程约定完全镜像 `D:\Hermes\javdb-cli` / `D:\Hermes\pixiv-cli`。后端抽象已预留：MVP 为 Nitter 后端，M6 增加内嵌 X GraphQL 客户端与账号池的 direct 后端（spec §15），命令与去重/输出层零改动复用。

**Architecture:** `cmd/twitter` 极薄入口 → `internal/cli` 组合根 + 每命令一目录 → 顶层 `sdk/`（`package twitter`，唯一公开取数面）→ `internal/nitter/{appapi,rss,html,protocol/httpx}` 协议实现 → `internal/{config,storage/seen,watch,update,common,buildinfo}` 支撑层。输出走 `twitter.pipeline/v1` NDJSON 信封 + 人类文本双模；去重状态落 `~/.twitter-cli/state/seen.json`（seen 300 条/源 + 扫描水位 20 条，语义移植自 astrbot_plugin_nitter_tweets）。

**Tech Stack:** Go 1.27.x（`CGO_ENABLED=0`）、cobra、bogdanfinn/tls-client、PuerkitoBio/goquery、pelletier/go-toml/v2、标准库 encoding/xml。无日志库（stderr 诊断）、无颜色库、无 SQLite（状态用原子写 JSON 文件）。

**Spec:** `docs/superpowers/specs/2026-09-11-twitter-cli-spec.md`（本计划从该 spec 论证；执行者两个都要读）

## Global Constraints

- Go 版本：`go 1.27` 起步；全部构建 `CGO_ENABLED=0`，`go build -trimpath`，版本经 `-ldflags "-X .../buildinfo.Version=..."` 注入。
- 边界：`cmd/twitter` 只委托；`internal/cli/commands/*` 不导入 `internal/cli` 包本体，也不导入 `internal/nitter/*`（取数一律经 `sdk`）；`internal/common` 不接收 `io.Writer`、不含用户文案；`internal/cli/result` 不编码 JSON、不建 cobra 命令、不调 SDK。
- `sdk/`（`package twitter`）导出签名是兼容契约：只增不改不删；`sdk/contract_external_test.go` 编译期冻结。
- 错误脱敏铁律：错误链不得包含实例凭证、URL 查询串、请求头、响应体；错误消息用户可读、英文。
- 「No silent fallback」：任何重试/上限/降级都必须来自配置或 flag 并写入文档；状态文件损坏必须报错，不得静默重置。
- 退出码：0 成功；1 运行时失败；2 usage/输入契约错误（`usageError` 只包参数与输入问题）。
- 翻译边界：代码注释按层双语（协议/SDK 英文，CLI/配置/存储中文偏多）；CLI 运行时消息英文；README/docs/changelog/skill 双语。
- 提交：Conventional Commits 单行英文小写 subject（`feat(cli): ...`，不用 `misc`/`wip`）；每任务收尾提交一次；changelog 条目进 `changelog/unreleased/{en,zh-CN}.md`。
- 只抓取用户自建、可信的 Nitter 实例；不内置公共实例、不解算挑战、不做登录态。
- 数据目录统一 `~/.twitter-cli/`，配置与状态文件 0600、原子写。

---

## Phase M0 — 仓库骨架（可构建、可测的 `twitter --version`）

### Task 1: 模块初始化与版本命令

**Files:**
- Create: `go.mod`、`cmd/twitter/main.go`、`internal/buildinfo/buildinfo.go`、`internal/cli/root.go`、`internal/cli/invocation/exit.go`、`internal/cli/root_test.go`
- Create: `.gitignore`

**Interfaces:**
- Consumes: 无（首个任务）
- Produces: `cli.Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int`（全项目唯一入口签名）；`buildinfo.{Version,Commit,BuildDate}`；`invocation.UsageError` + `invocation.ExitCode(err) int`

- [ ] **Step 1: 初始化仓库与模块**

```bash
mkdir -p D:/Hermes/twitter-cli && cd D:/Hermes/twitter-cli
git init
go mod init github.com/shitianyaa/twitter-cli
```

`.gitignore`：

```
/twitter
/twitter-cli
/dist/
*.db
```

- [ ] **Step 2: 写失败测试（版本输出 + 未知命令退出码）**

`internal/cli/root_test.go`：

```go
package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/buildinfo"
	"github.com/shitianyaa/twitter-cli/internal/cli"
)

func TestRunVersion(t *testing.T) {
	buildinfo.Version = "v0.1.0"
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{"--version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "twitter version v0.1.0" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{"nope"}, strings.NewReader(""), &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRunUnknownFlagIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run([]string{"--nope"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/cli/ -v`
Expected: FAIL（包不存在，编译错误）

- [ ] **Step 4: 最小实现**

`internal/buildinfo/buildinfo.go`：

```go
// Package buildinfo holds build metadata injected via -ldflags -X.
package buildinfo

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func IsDevelopment() bool { return Version == "" || Version == "dev" }
```

`internal/cli/invocation/exit.go`：

```go
// Package invocation carries per-run plumbing shared by all commands.
package invocation

import (
	"errors"
	"fmt"

	"github.com/spf13/pflag"
)

// UsageError marks argument/flag/input-contract failures (exit 2).
// SDK, network and local I/O errors must never be wrapped in it.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func Usagef(format string, args ...any) *UsageError {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// WrapFlagError lets root.SetFlagErrorFunc turn unknown-flag errors into
// usage errors so they exit 2.
func WrapFlagError(cmd *cobra.Command, err error) error {
	var nef *pflag.NotExistError
	if errors.As(err, &nef) {
		return &UsageError{Err: err}
	}
	return err
}

func ExitCode(err error) int {
	var ue *UsageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}
```

（`cobra` 引入后 `go get github.com/spf13/cobra@latest` 补依赖；`WrapFlagError` 的 `cmd` 参数用于满足 SetFlagErrorFunc 签名，可命名 `_`。）

`internal/cli/root.go`（v1 骨架，后续任务只加 AddCommand）：

```go
// Package cli is the composition root: it owns the command tree, shared
// streams and process exit semantics. Command packages must not import it.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shitianyaa/twitter-cli/internal/buildinfo"
	"github.com/shitianyaa/twitter-cli/internal/cli/invocation"
)

// Streams bundles the three process streams; commands read/write nothing else.
type Streams struct {
	In           io.Reader
	Out, Err     io.Writer
	InIsTTY      bool
	OutIsTTY     bool
	CTX          context.Context
	RootOptions  *invocation.RootOptions
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	streams := &Streams{In: stdin, Out: stdout, Err: stderr}
	streams.InIsTTY, streams.OutIsTTY = probeTTY(stdin, stdout)
	streams.RootOptions = &invocation.RootOptions{CTX: ctx}

	root := New(streams)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return invocation.ExitCode(err)
	}
	return 0
}

func New(s *Streams) *cobra.Command {
	root := &cobra.Command{
		Use:           "twitter",
		Short:         "Fetch public tweets from your own Nitter instances",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       versionLine(),
	}
	root.SetVersionTemplate("twitter version {{.Version}}\n")
	root.SetFlagErrorFunc(invocation.WrapFlagError)
	return root
}

func versionLine() string {
	if buildinfo.IsDevelopment() {
		return fmt.Sprintf("dev (commit %s, built %s)", buildinfo.Commit, buildinfo.BuildDate)
	}
	return buildinfo.Version
}

func probeTTY(in io.Reader, out io.Writer) (inTTY, outTTY bool) {
	// Windows/Unix TTY 探测：golang.org/x/term.IsTerminal 对 *os.File 判定；
	// 非 *os.File（测试注入）一律按非 TTY 处理。
	f, ok := in.(*os.File)
	if ok {
		inTTY = term.IsTerminal(int(f.Fd()))
	}
	of, ok := out.(*os.File)
	if ok {
		outTTY = term.IsTerminal(int(of.Fd()))
	}
	return inTTY, outTTY
}
```

（`invocation.RootOptions` 随 Task 2 补齐；本任务先放最小结构体 `type RootOptions struct { CTX context.Context }` 于 `internal/cli/invocation/options.go`。）

`cmd/twitter/main.go`：

```go
package main

import (
	"os"

	"github.com/shitianyaa/twitter-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
```

- [ ] **Step 5: 运行测试通过 + 构建冒烟**

Run: `go test ./... && go build ./cmd/twitter && ./twitter --version`
Expected: 全部 PASS；输出 `twitter version dev (commit ..., built ...)`

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(cli): scaffold module, buildinfo and version-only root"
```

### Task 2: 构建脚本、pre-commit、AGENTS.md、changelog 骨架

**Files:**
- Create: `scripts/build.sh`、`.pre-commit-config.yaml`、`AGENTS.md`、`CLAUDE.md`、`changelog/README.md`、`changelog/unreleased/{en,zh-CN}.md`、`internal/cli/invocation/options.go`（补全 RootOptions）

**Interfaces:**
- Produces: `sh scripts/build.sh` 产出 `./twitter`；`invocation.RootOptions{CTX, Proxy, Instance}`（全局 `--proxy`、`--instance` 两个持久 flag 的落点）

- [ ] **Step 1: `scripts/build.sh`**（对齐 javdb-cli）

```bash
#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
VERSION="${VERSION:-dev}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X github.com/shitianyaa/twitter-cli/internal/buildinfo.Version=${VERSION} \
  -X github.com/shitianyaa/twitter-cli/internal/buildinfo.Commit=${COMMIT} \
  -X github.com/shitianyaa/twitter-cli/internal/buildinfo.BuildDate=${BUILD_DATE}" \
  -o twitter ./cmd/twitter
```

- [ ] **Step 2: `.pre-commit-config.yaml`**

```yaml
repos:
  - repo: local
    hooks:
      - id: gofmt
        name: gofmt
        entry: bash -c 'out=$(gofmt -l .); if [ -n "$out" ]; then echo "$out"; exit 1; fi'
        language: system
        pass_filenames: false
      - id: go-test
        name: go test ./...
        entry: go test ./...
        language: system
        pass_filenames: false
```

- [ ] **Step 3: `AGENTS.md`**（CLAUDE.md 内容即一行 `@AGENTS.md`）

```markdown
# AGENTS.md

默认离线验证：`go test ./...` + `sh scripts/build.sh`。

## 架构边界（不可违反）

- `cmd/twitter` 只委托；`internal/cli/root.go` 拥有命令树、流与组装；子命令包不导入 `internal/cli`。
- 命令取数只经顶层 `sdk/`（package twitter）；禁止导入 `internal/nitter/*` 协议细节。
- `internal/common` 不接收 io.Writer、不含用户文案；`internal/cli/result` 不编码 JSON、不建命令、不调 SDK。
- 任何变更不得引入隐式超时、静默截断、静默降级、静默状态重置。

## 变更路由

- CLI 行为/flag/输出语义 → `docs/{en,zh-CN}/cli-reference.md` + 两语 README + `skills/twitter-cli/` + `changelog/unreleased/`
- SDK/模型签名 → `docs/{en,zh-CN}/sdk.md` + `docs/maintainers/architecture.md`
- 构建/发布 → `docs/maintainers/development.md`
- 提交信息：Conventional Commits 单行英文小写 subject。
```

- [ ] **Step 4: `changelog/unreleased/{en,zh-CN}.md`**（Keep a Changelog 空骨架：`# Unreleased` + `## Added` 等空节；`changelog/README.md` 索引表）

- [ ] **Step 5: 验证** — `pre-commit run --all-files`（未装 pre-commit 则手工跑两条钩子命令）；`sh scripts/build.sh && ./twitter --version`

- [ ] **Step 6: Commit** — `git add -A && git commit -m "chore: build script, pre-commit, agents guide and changelog skeleton"`

---

## Phase M1 — 配置、传输层、实例诊断

### Task 3: `internal/config/paths` — 数据目录与默认配置落盘

**Files:**
- Create: `internal/config/paths/paths.go`、`paths_test.go`

**Interfaces:**
- Produces: `paths.AppDirName = ".twitter-cli"`；`paths.New() (Paths, error)` → `Paths{Dir, ConfigFile, StateDir, SeenFile}`；`paths.EnsureDefaultConfigFile(cfgPath, defaultTOML string) error`

- [ ] **Step 1: 失败测试** — `TestNewUnderTempHome`（设置 `HOME`/`USERPROFILE` 到 `t.TempDir()`，断言四个路径拼装正确）；`TestEnsureDefaultConfigFileCreatesOnce`（首次创建 0600 且内容一致；已存在时内容不被覆盖——预写自定义内容再调用，断言保留）

- [ ] **Step 2: 运行确认 FAIL → 实现**

```go
package paths

import "path/filepath"

// AppDirName is the single data directory on every OS (javdb-cli convention).
const AppDirName = ".twitter-cli"

type Paths struct {
	Dir        string
	ConfigFile string
	StateDir   string
	SeenFile   string
}

func New() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, AppDirName)
	return Paths{
		Dir:        dir,
		ConfigFile: filepath.Join(dir, "config.toml"),
		StateDir:   filepath.Join(dir, "state"),
		SeenFile:   filepath.Join(dir, "state", "seen.json"),
	}, nil
}

// EnsureDefaultConfigFile publishes the baseline config without ever
// overwriting: temp file + os.Link (fails with EEXIST if the target exists).
func EnsureDefaultConfigFile(cfgPath, defaultTOML string) error {
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return fmt.Errorf("create app dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(cfgPath), ".config-*.tmp")
	if err != nil { return fmt.Errorf("stage config: %w", err) }
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(defaultTOML); err != nil { tmp.Close(); return err }
	if err := tmp.Chmod(0o600); err != nil { tmp.Close(); return err }
	if err := tmp.Sync(); err != nil { tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	if err := os.Link(tmp.Name(), cfgPath); err != nil {
		if os.IsExist(err) { return nil } // 并发/重复调用：已存在即成功
		return fmt.Errorf("publish config: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: PASS → Commit** — `feat(config): data paths and no-replace default config`

### Task 4: `internal/config/settings` — TOML 结构 + 未知键保留写回 + env 覆盖

**Files:**
- Create: `internal/config/settings/settings.go`、`settings_test.go`
- Create: `internal/config/settings/document.go`

**Interfaces:**
- Produces:
  - `type Settings struct { Instances []Instance; WatchSources []WatchSource; DefaultLimit int; MaxPages int; RequestInterval, RetryDelay, InstanceCooldown string; RetryAttempts int; Proxy, LogLevel, LogFormat string }`
  - `type Instance struct { URL string; Username, Password string }`、`type WatchSource struct { ID string }`
  - `settings.Defaults() Settings`（default_limit=20、max_pages=5、request_interval="1s"、retry_attempts=2、retry_delay="1s"、instance_cooldown="60s"、log_level="info"、log_format="text"）
  - `settings.Load(cfgPath string, env func(string) string) (Settings, error)`（优先级 env > file > default；env 键 `TWITTER_DEFAULT_LIMIT`、`TWITTER_LOG_LEVEL`、`TWITTER_LOG_FORMAT`；duration 字段用 `time.ParseDuration` 校验，非法 → `*invocation.UsageError` 语义错误）
  - `settings.SaveKnown(path string, mut func(map[string]any) error) error`（读整树为 `map[string]any` → mut 只改已知顶层键 → 0600 原子写；未知键天然保留。注释不保证保留——javdb 同款语义，注明）

- [ ] **Step 1: 失败测试**（表驱动）：defaults 值；Load 读 fixture TOML；env 覆盖生效；非法 duration 报错；`SaveKnown` 保留未知键 `[[instances]]` 之外的任意表；写出文件 0600。
- [ ] **Step 2: FAIL → 实现（结构体 + `go-toml/v2` Unmarshal 到 Settings 与 map 双通道 + 原子写复用 paths 的 temp+rename 模式；rename 前后加包级 `sync.Mutex` 注释说明 Windows rename-over-open-file 串行化）**
- [ ] **Step 3: PASS → Commit** — `feat(config): settings schema, env precedence and unknown-key-preserving writes`

### Task 5: `twitter config` 命令

**Files:**
- Create: `internal/cli/commands/config/config.go`、`config_test.go`
- Modify: `internal/cli/root.go`（`root.AddCommand(configcmd.New(s))`）

**Interfaces:**
- Consumes: Task 3/4
- Produces: `config path`（打印路径）、`config get [KEY]`（无 KEY 打印全部已知键；未知键 exit 2）、`config set KEY VALUE`、`config unset KEY`；已知键集合 = `default_limit, max_pages, request_interval, retry_attempts, retry_delay, instance_cooldown, proxy, log_level, log_format`（instances/watch 属于数组表，只能手编 TOML，`set` 拒绝并提示）。`set` 对 `default_limit`/`max_pages`/`retry_attempts` 做 int 校验，duration 键做 ParseDuration 校验，校验先于任何落盘。

- [ ] **Step 1: 失败测试** — `path` 输出包含 `.twitter-cli`；`set default_limit 30` 后 `get default_limit` 回读 30；`set nope 1` exit 2 且不创建文件；`set default_limit abc` exit 2。
- [ ] **Step 2: FAIL → 实现**（无值 `set KEY` 时：TTY 则提示从参数或 stdin 读，非 TTY 读 stdin 一行——对齐 pixiv-cli 敏感值不入 argv；MVP 只有 proxy 将来算敏感，先按此实现）
- [ ] **Step 3: PASS → Commit** — `feat(cli): twitter config path/get/set/unset`

### Task 6: `sdk/errors.go` — 分类错误模型

**Files:**
- Create: `sdk/errors.go`、`errors_test.go`

**Interfaces:**
- Produces:

```go
package twitter

// Kind is a stable, machine-readable error class. Values may only be added,
// never removed, renamed or reused (v1 contract).
type Kind string

const (
	KindChallenge   Kind = "challenge_required"
	KindRateLimited Kind = "rate_limited"
	KindNotFound    Kind = "not_found"
	KindUnavailable Kind = "upstream_unavailable"
	KindMalformed   Kind = "malformed_upstream_response"
	KindInvalidArg  Kind = "invalid_argument"
	KindLocalState  Kind = "local_state_error"
)

// Error carries the redaction contract: neither this struct nor its wrapped
// chain may contain credentials, URL query strings, request headers or
// response bodies.
type Error struct {
	Kind       Kind
	Op         string
	Err        error
	RetryAfter *time.Duration // 仅 KindRateLimited 且来自实例响应头
}

func (e *Error) Error() string
func (e *Error) Unwrap() error
func Errorf(kind Kind, op string, format string, args ...any) *Error
```

- [ ] **Step 1: 失败测试** — `errors.As` 命中 `*Error` 取出 Kind/RetryAfter；`Errorf` 消息含格式化内容；嵌套链 `Unwrap` 可达根因。
- [ ] **Step 2: FAIL → 实现 → PASS → Commit** — `feat(sdk): classified error model with redaction contract`

### Task 7: `internal/nitter/protocol/httpx` — 传输：tls-client、重试、节奏、429

**Files:**
- Create: `internal/nitter/protocol/httpx/client.go`、`httpx_test.go`、`internal/common/jsonx/jsonx.go`（顺手建：`MarshalLine`，`SetEscapeHTML(false)` 单行 + `\n`）

**Interfaces:**
- Consumes: Task 6（错误分类）
- Produces:

```go
package httpx

type Doer interface { Do(*fhttp.Request) (*fhttp.Response, error) }

type Options struct {
	Proxy         string        // "" = 不设置（继承环境）
	Timeout       time.Duration // 默认 20s
	Profile       tls_client.ClientProfile
	RetryAttempts int           // 额外重试次数，默认 2
	RetryDelay    time.Duration // 线性退避 delay*(attempt+1)
	MinInterval   time.Duration // 全局节奏（默认 1s，由调用方从 config 注入）
	Now           func() time.Time
}

type Client struct{ /* 持有 Doer、节奏状态、重试参数 */ }

func New(opts Options) (*Client, error) // 构造 tls-client（Chrome profile、NoopLogger、CookieJar）

// Do 执行 GET，返回 body 字节与最终 status。分类：
//   - 网络错误/5xx：按 RetryAttempts 线性退避重试，耗尽 → KindUnavailable
//   - 429：解析 Retry-After（秒或 HTTP-date，int64 溢出防护）；有效 → 等待一次后重试一次；无效 → KindRateLimited
//   - 404 → KindNotFound；401/403 → KindChallenge；其余 4xx → KindUnavailable（含状态码）
//   - 2xx/3xx：返回（3xx 不跟随重定向——tls-client WithNotFollowRedirects，由调用方决定）
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error)
```

- [ ] **Step 1: 失败测试**（用自造 `fakeDoer` 注入脚本化响应；不依赖 tls-client 网络行为）：
  - 200 返回 body；500→500→200 两次重试后成功；404 → `KindNotFound`；429 + `Retry-After: 2` → 等待后重试成功（用注入时钟断言 sleep 时长）；429 无 Retry-After → `KindRateLimited`；节奏：两次 Get 间隔 < MinInterval 时第二次被推迟（注入时钟记录时刻）。
- [ ] **Step 2: FAIL → 实现 → PASS → Commit** — `feat(protocol): tls-client transport with retry, pacing and retry-after`

### Task 8: `sdk/client.go` — 实例轮换选择器 + Client 组合根

**Files:**
- Create: `sdk/client.go`、`chooser.go`、`chooser_test.go`、`sdk/page.go`、`sdk/models.go`（Tweet/Author/Media/Page/InstanceReport，字段按 spec §5）
- Create: `internal/nitter/appapi/client.go`（组合根，嵌 `*httpx.Client` 与 `*Chooser`）

**Interfaces:**
- Consumes: Task 6/7
- Produces:

```go
package twitter

type Instance struct{ URL, Username, Password string } // sdk 侧独立于 config 的投影

type Options func(*clientConfig)
func WithInstances(in []Instance) Options
func WithHTTPClient(h *httpx.Client) Options // 测试注入；内部默认自建
func WithCooldown(d time.Duration) Options

type Chooser struct{ /* 顺序表 + coolUntil + now func */ }
func (c *Chooser) Pick() (baseURL string, markSuccess, markFailure func(), err error)
// 全部冷却中 → KindUnavailable "all instances cooling down"

type Client struct { /* Chooser + httpx.Client + 分页上限 */ }
func New(opts ...Options) (*Client, error)
```

- [ ] **Step 1: 失败测试（chooser 纯逻辑表驱动）**：顺序取第一个未冷却实例；冷却中的被跳过；markFailure 进入冷却（注入时钟）；markSuccess 复位；全冷却报错。
- [ ] **Step 2: FAIL → 实现 → PASS → Commit** — `feat(sdk): client composition with instance rotation and cooldown`

### Task 9: `twitter instances test` — 实例能力诊断

**Files:**
- Create: `internal/nitter/appapi/probe.go`、`internal/cli/commands/instances/instances.go`、`instances_test.go`、`internal/cli/result/instance.go`

**Interfaces:**
- Consumes: Task 7/8、Task 4（instances 列表）、Task 12 的 pipeline（若已就绪；未就绪则本任务先实现 `--json`，Task 12 完成后回归补 NDJSON——任务顺序允许时把本任务排在 Task 12 之后）
- Produces: `sdk (Client).TestInstance(ctx, baseURL string, opts TestOptions) InstanceReport`（探测 `/<probe user>/rss`、`/<probe user>`、`/search?q=twitter&f=tweets`、可选 `/i/lists/<id>`；判定 = HTTP 200 且可解析/含 `timeline-item` 或 RSS `<item>`）；命令 `twitter instances test [URL] [--full]`，无 URL 时测配置全部实例。

- [ ] **Step 1: 失败测试** — `httptest` 起假 Nitter（RSS OK、HTML OK、search 404）：Report 各 Probe 状态正确；URL 参数优先于配置；`--json` 输出含 `url/rss/user_html/search/list` 键。
- [ ] **Step 2: FAIL → 实现**（人类输出制表行：`URL  RSS  USER_HTML  SEARCH  LIST  LATENCY`；失败格 `fail(404)`）
- [ ] **Step 3: PASS → 真实实例冒烟（用户手填 `--instance` 验一次）→ Commit** — `feat(cli): twitter instances test capability probe`

---

## Phase M2 — RSS/HTML 解析、pipeline、`twitter user`

### Task 10: `sdk/models.go` 定稿 + RSS 解析器

**Files:**
- Create: `internal/nitter/rss/parser.go`、`parser_test.go`、`testdata/user_rss.xml`
- Modify: `sdk/models.go`（如 Task 8 未定型则此步定稿）

**Interfaces:**
- Produces:

```go
package rss

type Item struct {
	GUID, Link, Title, Description, PubDate, Creator string
	MediaURLs []string // media:content url + description 内 <img src>
}

func Parse(data []byte) ([]Item, error) // encoding/xml；channel 命名空间与 dc/mrss 前缀显式声明

func ItemToTweet(it Item) (twitter.Tweet, error)
// - user+id: regexp `([A-Za-z0-9_]+)/status(?:es)?/(\d+)` 作用于 GUID，回退 Link；不匹配 → KindMalformed
// - URL 规范化为 https://x.com/<user>/status/<id>
// - Text: cleanHTML(Description)，空则回退 cleanHTML(Title)；复用 plugin clean_html_text 语义（<br>→\n、剥标签、unescape、收敛空行）
// - PublishedAt: time.Parse(time.RFC1123Z, PubDate)
// - Media: MediaURLs 相对路径经 instance base 拼接，pbs.twimg.com/media 重写 name=orig
```

- [ ] **Step 1: 写 fixture**（`testdata/user_rss.xml`：Nitter RSS 真实结构样例——`<item><guid isPermaLink="true">https://nitter.example/NASA/status/123#m</guid><title>…</title><dc:creator>…</dc:creator><description><![CDATA[ XHTML 含 <img src="/pic/media/…"> ]]></description><pubDate>Mon, 27 Jul 2026 09:09:40 +0000</pubDate><media:content url="https://pbs.twimg.com/media/xxx.jpg?name=small"/></item>` ×3，含一条无媒体、一条视频缩略）
  **注意**：fixture 必须先用 `curl <实例>/NASA/rss` 与真实输出比对一次；字段与真实样例有出入时，以真实样例为准修正 fixture 与解析器（本步骤内的修正属于正常 TDD 循环，不属于计划变更）。
- [ ] **Step 2: 失败测试** — 解析 3 条；ID/user/URL 投影正确；HTML 正文转纯文本（`<br>` 成换行）；pubDate → UTC；media 含 pbs 链接且 `name=orig`；缺 GUID 回退 Link；损坏 XML → KindMalformed。
- [ ] **Step 3: FAIL → 实现 → PASS → Commit** — `feat(nitter): rss feed parser and tweet projection`

### Task 11: HTML 时间线解析器

**Files:**
- Create: `internal/nitter/html/timeline.go`、`timeline_test.go`、`testdata/timeline.html`、`testdata/search.html`（本任务只建文件，search 解析 Task 14 用）

**Interfaces:**
- Consumes: Task 10（`cleanHTML`、pbs 质量重写放 `internal/nitter/html/text.go` 共用）
- Produces:

```go
package html

type Page struct {
	Tweets     []twitter.Tweet
	NextCursor string
}

// ParseTimeline 解析任意承载 timeline-item 的页面（用户页/搜索/List 共用）。
// 语义真源：astrbot_plugin_nitter_tweets media_support/html_backend/parser.py
//   - 分块 div.timeline-item；href="/<user>/status(es)?/<id>" 定位
//   - 正文 div.tweet-content：取 OuterHtml 后 cleanHTML；必须在剔除 .quote、
//     div[class^='quote']、a[href*='/i/article'] 子树后的克隆节点上取（引用媒体不算作者媒体）
//   - 媒体（同样在克隆上取）：a.still-image[href]、[src|href*="/pic/orig/media"]、
//     [src|href*="/pic/media"]（无 orig 回退）、href^="/video/"、video.twimg.com；
//     排除 profile_images/profile_banners/video_thumb/amplify_video_thumb/emoji
//   - 纯转推：tweet-content 之前存在 .retweet-header（或 retweeted by / 转推了）
//   - 时间：.tweet-date a[title]（"Jul 27, 2026 · 9:09 AM UTC" → layout "Jan 2, 2006 · 3:04 PM MST"）；
//     解析失败 → PublishedAt 零值并继续（不造数据）
//   - 游标：a.show-more[href] 的 cursor= 参数（url.ParseQuery 后 unquote）
func ParseTimeline(body []byte, instance string) (Page, error)

// ClassifyPage 区分 登录/维护页（KindChallenge）、错误页（KindUnavailable）、空时间线（空 Page）。
func ClassifyPage(body []byte) error
```

- [ ] **Step 1: 失败测试**（fixture 覆盖）：
  - 基本抽取：3 条推文的 id/user/text/date/media/repost 标记
  - 引用屏蔽：item 含 `.quote`（quote 内有 `/pic/media` 与 tweet-content）→ 作者 text 不含引用文本、media 不含引用图
  - 质量重写：pbs 链接 → `name=orig`
  - 游标：`show-more` → cursor 提取
  - 分类：登录页 → KindChallenge；错误页 → KindUnavailable；空 timeline-end → 空 Page 无错误
- [ ] **Step 2: FAIL → 实现（goquery；克隆节点 `Remove()` 屏蔽引用子树）→ PASS → Commit** — `feat(nitter): goquery timeline parser with quote masking and paging`

### Task 12: `internal/cli/pipeline` + `invocation` 输出机制

**Files:**
- Create: `internal/cli/pipeline/pipeline.go`、`pipeline_test.go`、`internal/cli/result/tweet.go`、`result_test.go`
- Modify: `internal/cli/invocation/options.go`（补 `RootOptions{CTX, Proxy, Instance string}`）

**Interfaces:**
- Consumes: `internal/common/jsonx`
- Produces:

```go
package pipeline

type Mode int
const ( ModeHuman Mode = iota; ModeText; ModeJSON; ModeNDJSON )

// ResolveOutputMode：--json/--ndjson 互斥（UsageError）；显式 flag 优先；
// 无 flag：TTY → ModeHuman，非 TTY → ModeText。
func ResolveOutputMode(ndjson, json bool, outIsTTY bool) (Mode, error)

type Envelope struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Data   any    `json:"data"`
	Meta   *Meta  `json:"meta,omitempty"`
}
const Schema = "twitter.pipeline/v1"

type Meta struct { Source, Instance, FetchedAt string }

func WriteEnvelope(w io.Writer, env Envelope) error           // jsonx.MarshalLine
func WriteErrorEnvelope(w io.Writer, command, stage, code, input, message string) error
```

人类行投影 `result.TweetRow.Line()`：`<id>\t<YYYY-MM-DD HH:MM>\t@<handle>\t<单行文本>`；文本单行化时剥离控制字符并保留图形字符（借 pixiv SafeLine 思路，`strconv.QuoteToGraphic` 仅对含控制字符的文本启用）；空列表提示 `(empty)` 写 stderr。

- [ ] **Step 1: 失败测试** — 互斥报错；TTY/非 TTY 默认；信封 golden（schema/kind/id/data/meta 键序与单行 `\n`）；错误信封无 secrets；TweetRow 控制字符清洗。
- [ ] **Step 2: FAIL → 实现 → PASS → Commit** — `feat(cli): output mode resolution and twitter.pipeline/v1 envelopes`

### Task 13: `twitter user` — 用户时间线（RSS 优先 + HTML 后备 + 分页）

**Files:**
- Create: `internal/nitter/appapi/timeline.go`、`internal/cli/commands/user/user.go`、`user_test.go`、`internal/cli/client/client.go`（sdk 构造落点：proxy/instance 解析，校验 `--proxy` scheme ∈ http/https/socks5/socks5h）

**Interfaces:**
- Consumes: Task 8 Chooser/Client、Task 10 RSS、Task 11 HTML、Task 12 pipeline
- Produces:

```go
// appapi
func (c *Client) Timeline(ctx context.Context, handle string, opts twitter.PageOptions) ([]twitter.Tweet, error)
// PageOptions{ Limit int; MaxPages int }
// 每个候选实例：GET /<handle>/rss → rss.Parse（≤limit 即停，RSS 无游标，一页到顶）；
// 失败或 0 条 → GET /<handle> → html.ParseTimeline → 按 cursor 续页（≤MaxPages）；
// ClassifyPage 错误按 Kind 上抛；实例成功 markSuccess / 失败 markFailure 后换下一个。
// handle 校验：^[A-Za-z0-9_]{1,15}$ → 否则 usage error（先于任何网络/文件副作用）
```

命令：`twitter user <HANDLE> [--limit N] [--max-pages N] [--reposts|--no-reposts 过滤为后续]`（MVP 不做过滤开关）、全局 `--proxy/--instance`、`--json/--ndjson`。

- [ ] **Step 1: 失败测试**（httptest 假实例：RSS 挂 → HTML 兜底出 3 条；`--limit 2` 截断；`--json` 数组信封；`--ndjson` 逐行信封且 schema 正确；`user NASA!` exit 2；`--limit 0` = 全部）
- [ ] **Step 2: FAIL → 实现 → PASS**
- [ ] **Step 3: 真实实例冒烟**（用户配置实例跑一次 `twitter user <已知账号> --limit 5` 人工确认中文/emoji/媒体字段）
- [ ] **Step 4: Commit** — `feat(cli): twitter user timeline with rss-first html fallback`

---

## Phase M3 — 搜索、List、单条（任务结构与 Task 13 同构）

### Task 14: `twitter search`

**Files:** Create `internal/nitter/appapi/search.go`、`internal/cli/commands/search/search.go`、`search_test.go`、修改 `testdata/search.html`

**Interfaces:** `(c *Client).Search(ctx, query string, opts PageOptions) ([]twitter.Tweet, error)` — GET `/search?f=tweets&q=<urlquery>`；goquery 解析同 `ParseTimeline`（搜索页复用 timeline-item 结构 + `#search-results`）；游标续页。查询先 `query_kind` 语义校验（`#tag` 原样、`from:`/`@` 前缀透传——对齐插件 `html_backend/query.py`；非法空查 usage error）。

- [ ] 步骤：fixture 失败测试（含 `#` 标签查询 URL 编码断言、分页）→ 实现 → PASS → 真实冒烟 → Commit `feat(cli): twitter search`

### Task 15: `twitter list`

**Files:** Create `internal/nitter/appapi/list.go`、`internal/cli/commands/list/list.go`、`list_test.go`、`testdata/list.html`

**Interfaces:** `(c *Client).ListTimeline(ctx, listID string, opts PageOptions) ([]twitter.Tweet, error)` — GET `/i/lists/<listID>`（listID 校验为纯数字或 Nitter 列表路径 ref）；解析同 timeline。空结果与「List 尚未被 Nitter 收录」区分：空 Page + 明确提示（不造错误）。

- [ ] 步骤：同 Task 14 模式 → Commit `feat(cli): twitter list timeline`

### Task 16: `twitter get` — 单条推文

**Files:** Create `internal/nitter/html/status.go`、`status_test.go`、`internal/nitter/appapi/status.go`、`internal/cli/commands/get/get.go`、`get_test.go`、`testdata/status.html`

**Interfaces:**

```go
// ref 解析（usage error 先于网络）：纯数字 ID | https://(x|twitter).com/<user>/status/<id>(/photo/N)? | nitter URL 同构
func ParseStatusRef(s string) (id string, user string, err error)
func (c *Client) Status(ctx context.Context, ref string) (twitter.Tweet, error)
// GET /<user>/status/<id>（user 未知时 nitter 支持仅 ID 的 /status/<id> 路由，实现按此顺序回退）
// status 页比 timeline-item 多正文全文与引用摘要：Quote{ID,URL,Text,Author}
```

- [ ] 步骤：ref 解析表驱动测试 → status 页 fixture 测试（含 quote 摘要）→ 命令三输出模式测试 → Commit `feat(cli): twitter get single status`

---

## Phase M4 — seen 状态与 watch（本项目的差异化核心）

### Task 17: `internal/storage/seen` — 状态文件库

**Files:**
- Create: `internal/storage/seen/seen.go`、`seen_test.go`

**Interfaces:**
- Produces:

```go
package seen

const (
	SeenLimit      = 300 // plugin SEEN_LIMIT_PER_USER
	WatermarkLimit = 20  // plugin _normalize_scan_baseline_ids 上限
	SchemaVersion  = 1
)

type SourceState struct {
	Initialized  bool      `json:"initialized"`
	SeenIDs      []string  `json:"seen_ids"`
	WatermarkIDs []string  `json:"watermark_ids"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type File struct {
	Version int                    `json:"version"`
	Sources map[string]SourceState `json:"sources"`
}

type Store struct{ /* path + sync.Mutex + now func */ }

func Open(path string, now func() time.Time) (*Store, error)
// 加载：文件缺失 → 空 File{Version:1}；存在 → 严格解码（DisallowUnknownFields、
// 拒绝尾随数据）；损坏 → KindLocalState 错误，绝不静默重置。

func (s *Store) Get(source string) SourceState        // 不存在返回零值 + false（第二返回值）
func (s *Store) Put(source string, st SourceState) error // 触碰 UpdatedAt；临时文件 0600 → Sync → rename（Windows rename-over-open 用包级 Mutex 串行化）

func MergeSeen(newIDs, oldIDs []string) []string // 新前插、去重、cap SeenLimit（plugin merge_seen_ids 语义）
func CapWatermark(ids []string) []string         // 仅保留纯数字、去重、cap WatermarkLimit
func SourceKey(kind, ref string) string          // "user:NASA" / "tag:%23AI" / "list:123"
```

- [ ] **Step 1: 失败测试**：空路径首次 Get 零值；Put→重开→回读一致；损坏 JSON 报错且原文不动；MergeSeen 顺序/去重/300 截断；CapWatermark 过滤非数字；并发 Put 无损坏（goroutine + 最终一致性）。
- [ ] **Step 2: FAIL → 实现 → PASS → Commit** — `feat(storage): seen state store with atomic writes`

### Task 18: `internal/watch` — 去重选取引擎（纯函数）

**Files:**
- Create: `internal/watch/engine.go`、`engine_test.go`

**Interfaces:**
- Consumes: Task 17 常量与合并函数
- Produces:

```go
package watch

type Options struct {
	IncludeExisting bool // 首跑是否产出历史（默认 false：只记不推）
	MaxNew          int  // >0：本轮最多产出 N 条并重建基准；==0：不产出并重建基准（仅 initialized 源）
}

type Result struct {
	Tweets  []twitter.Tweet
	State   seen.SourceState
	Emitted bool // 本轮是否允许产出（false = 首跑初始化或 MaxNew==0 截停）
}

// Select 语义真源：plugin scheduler/runner_seen.py + architecture.md 后台检查链路。
// fetched 为本轮抓到的全部推文（按时间线顺序）；firstPageIDs 为首页前 20 个 status ID
//（由调用方从抓取结果切出，供水位重建）。
func Select(fetched []twitter.Tweet, firstPageIDs []string, prev seen.SourceState, opts Options) Result
```

- [ ] **Step 1: 失败测试（表驱动，全部对照插件语义）**：
  1. 未初始化 + 默认 → Tweets 空、State.Initialized=true、Seen=全部（cap 300）、Watermark=firstPage 前 20、Emitted=false
  2. 未初始化 + IncludeExisting → Tweets=全部、Emitted=true
  3. 已初始化 → 新推文 = fetched − seen（按序）；旧 seen 保持前插合并
  4. 已初始化 + MaxNew=2 → 只出 2 条、State 正常推进（基准重建）
  5. 已初始化 + MaxNew=0 → Emitted=false、State 仍推进（对齐插件「上限 0 时不推送并自动重建第一页基准」）
  6. 全部已 seen → Tweets 空、State 仍刷新 UpdatedAt
  7. 水位 = firstPageIDs（与 seen 是否重叠无关），cap 20
- [ ] **Step 2: FAIL → 实现 → PASS → Commit** — `feat(watch): dedup selection engine mirroring plugin seen semantics`

### Task 19: `twitter watch` 命令

**Files:**
- Create: `internal/cli/commands/watch/watch.go`、`watch_test.go`、`internal/watch/source.go`（`ParseSource("user:NASA") → {Kind,Ref}`，非法 → usage error）

**Interfaces:**
- Consumes: Task 13–16 的 appapi 能力、Task 17 Store、Task 18 Select、Task 12 pipeline
- Produces:

```
twitter watch [SOURCE...] [--interval 10m] [--once] [--include-existing] [--max-new N] [--max-pages N] [--state-dir DIR] [--ndjson]
SOURCE 形如 user:<handle> | tag:<query> | list:<id>；argv 为空时读配置 [[watch.sources]]；两者皆空 → usage error。
```

行为：
1. 逐源处理，源级失败写 `kind:"error"` 信封（`data={command:"watch",stage:"fetch",code:<Kind>,message}` + `meta.source`），状态不动，继续下一源。
2. 抓取边界（对齐插件）：逐页抓取直到「本页全部 ID ∈ seen」（水位命中）或 `--max-pages` 耗尽；`firstPageIDs` 取首页前 20。
3. 每轮结束：Select → 产出 `kind:"tweet"` 信封（meta.source/instance/fetched_at）→ `state.Put`（**先产出后落盘**；产出通道写失败则不落盘，下轮重推，宁重勿丢）。
4. `--once`：单轮结束；有失败源 → `fmt.Errorf("watch completed with %d of %d sources failed", ...)` exit 1，否则 0。
5. 无 `--once`：`--interval`（≥1s，默认 10m）ticker 循环直至 CTX 取消（SIGINT/SIGTERM → 优雅退出 0）；stdout 写入遇 EPIPE → 视为下游关闭，退出 0（pixiv sigpipe 政策）。

- [ ] **Step 1: 失败测试**（httptest 假实例 + `--state-dir t.TempDir()`）：
  - 首轮 `--once`：stdout 无 tweet 信封、seen.json 有 initialized 源
  - 二轮（fixture 换新推文）→ 出新推文信封且旧推文不重复
  - 同 fixture 三轮 → 无输出（去重）
  - `--include-existing` 首轮全出
  - 源 A 实例 500、源 B 正常 → B 出信封、A error 信封、exit 1、A 状态未动（seen.json 无 A 的 seen 推进）
  - `--max-new 1` → 单条
  - 非法 source → exit 2
- [ ] **Step 2: FAIL → 实现 → PASS**
- [ ] **Step 3: 端到端手验**：对本机真实实例连续两轮 `--once --ndjson`，确认首轮只记不推、次轮增量。
- [ ] **Step 4: Commit** — `feat(cli): twitter watch with persistent dedup state`

### Task 20: `twitter seen` 命令

**Files:** Create `internal/cli/commands/seen/seen.go`、`seen_test.go`

**Interfaces:** `seen list [--source S] [--json]`（输出 SourceState 摘要：kind:id、计数、水位首条、updated_at）；`seen clear [--source S] --confirm`（无 `--confirm` → usage error 并提示；无 `--source` 清全部）。删除对应源条目后原子落盘。

- [ ] 步骤：测试（list/clear/确认门/未知源提示）→ 实现 → Commit `feat(cli): twitter seen inspect and clear`

---

## Phase M5 — 文档、Skill、e2e、更新检查、发布

### Task 21: 双语文档

**Files:** Create `README.md`、`README.zh-CN.md`、`docs/en/cli-reference.md`、`docs/zh-CN/cli-reference.md`、`docs/en/sdk.md`、`docs/zh-CN/sdk.md`、`docs/maintainers/architecture.md`、`docs/maintainers/development.md`、`docs/index.md`、`docs/index.zh-CN.md`

**Interfaces:** 内容真源 = spec §6–§12 + 已实现命令 `--help` 实测输出；README 结构对齐插件 README（能做什么/上手/配置/FAQ/免责声明——免责声明必须含「仅公开推文、只用可信自建实例、网络层隔离内部服务」三条）。cli-reference 逐命令：用法、flag、退出码、输出模式示例（含 NDJSON 信封样例）。

- [ ] 步骤：写作 → 对照实测输出核校每个 flag/示例 → 双语行为一致检查 → Commit `docs: bilingual readme, cli reference, sdk and maintainers docs`

### Task 22: Agent Skill（随仓库分发）

**Files:** Create `skills/twitter-cli/SKILL.md`、`skills/twitter-cli/references/{instances.md,watch.md,troubleshooting.md}`

**Interfaces:** SKILL.md 结构对齐 javdb-cli（frontmatter: slug/version/displayName/summary/license/homepage/tags/name + 中文 description 触发条件）。内容必须包含：

```markdown
# twitter-cli

（frontmatter 后）

## 预检
- 仅以 `twitter --version` 探测环境；输出形如 `twitter version <v>`。
- 实例来自配置 `~/.twitter-cli/config.toml`；未配置实例时先请用户填写自建 Nitter 地址，
  绝不代填公共实例（nitter.net 等）。

## 不可违反的规则
1. 不回显 config.toml 中的实例凭证（username/password）。
2. 状态变更（`seen clear`、`config set`、`config unset`）逐次征得同意，授权不跨命令延续。
3. 不发明 flag；不确定语义先跑 `twitter <cmd> --help`。
4. `--json/--ndjson` 只描述成功输出；先看退出码再解析，stderr 永远不是 JSON。
5. watch 是有状态的：默认首跑只初始化不产出（历史不入推）；要产出历史必须显式 `--include-existing`。
6. 不给命令加自造超时；长任务用 `--once` + 调度器轮询，不要常驻前台等待。

## 操作分级
| 级别 | 命令 | Agent 行为 |
| --- | --- | --- |
| 只读 | user/search/list/get/instances test/seen list/config get/config path/update --check | 可直接执行 |
| 本地写 | config set/unset、seen clear | 每次确认 |
| 常驻/调度 | watch --once（推荐）/ watch | 按用户给定节奏；--once 优先 |

## 输出与管道
- 人类阅读：默认文本；程序消费：`--ndjson`（twitter.pipeline/v1，kind=tweet|error）；
  单对象提取：`--json`。先 `--limit` 缩量再考虑 jq。
- watch `--once --ndjson` 的 stdout 就是交付流；exit 0 全成、1 部分源失败（error 信封内含明细）、2 用法错。

## 命令速查
（~15 行带注释示例，与 cli-reference 一致）

## 关键语义与陷阱
（10 条：RSS 优先 HTML 后备、实例冷却、seen 300/水位 20、首跑不推、MaxNew 语义、
tag 源需 URL 编码、list 新建需等 Nitter 收录、429 处理、退出码、--state-dir 隔离测试）

## 路由
references/instances.md（配置与健康诊断）、references/watch.md（调度与去重细节）、
references/troubleshooting.md（常见错误表，对齐插件 instances-guide）
```

- [ ] 步骤：撰写 → 与 `--help` 实测逐条核对（skill 明文「命令语义以当前安装二进制的 --help 为准」）→ Commit `feat(skill): ship agent skill for twitter-cli`

### Task 23: e2e 与 CI

**Files:** Create `e2e/run.sh`、`.github/workflows/ci.yml`、`.github/workflows/e2e.yml`、`.github/workflows/release.yml`（骨架）

**Interfaces:** e2e 退出码契约 = javdb（0 过 / 1 挂 / 2 仅 SKIP 软通过）：

```sh
#!/usr/bin/env bash
set -u
pass=0; fail=0; skip=0
check() { # label, cmd... ; 断言 stdout 含子串 }
# 离线契约（必跑）：
#   ./twitter --version          匹配 ^twitter version
#   ./twitter config path        输出含 .twitter-cli，且首次运行创建 0600 文件
#   ./twitter user --help        exit 0
#   ./twitter watch --once --ndjson --state-dir "$tmp"（无实例配置）→ exit 2（usage）
#   NDJSON 信封 python3 校验 schema 字段（喂 fixture 假实例：python3 -m http.server 起静态 RSS）
# 实网只读探针（env 门控）：TWITTER_CLI_E2E_INSTANCE、TWITTER_CLI_E2E_USER 设置时跑
#   user/search/get 三命令 --json 关键字段存在性断言；未设置 → skip
echo "summary: pass=$pass fail=$fail skip=$skip"
[ "$fail" -gt 0 ] && exit 1
[ "$pass" -eq 0 ] && [ "$skip" -gt 0 ] && exit 2
exit 0
```

ci.yml：gofmt / `go vet ./...` / `go test ./... -count=1` / `go test -race ./...` / `sh scripts/build.sh` / e2e 离线段。release.yml：tag 触发双语 changelog 校验 + 6 平台构建矩阵（沿用 javdb 矩阵：macos-15-intel/macos-15/ubuntu-22.04/-arm/windows-2025/windows-11-arm）+ GitHub Release 草稿（Ed25519 签名清单列为后续增强，MVP 先 checksums.txt）。

- [ ] 步骤：e2e 本地跑通三态退出码 → workflow 提交 → Commit `ci: offline e2e gate, quality workflow and release skeleton`

### Task 24: `twitter update --check` 与收尾

**Files:** Create `internal/update/semver.go`、`release.go`、`internal/cli/commands/update/update.go`、`update_test.go`；Modify `changelog/unreleased/*`（整理本里程碑条目）

**Interfaces:** `update --check [--prerelease] [--json]`：GitHub Releases API 查 `github.com/shitianyaa/twitter-cli` 最新稳定版，`semver` 比较（自写 `Compare(a, b string) int` + 表驱动测试，含 prerelease 规则）；dev 构建提示「development build」；无 `--check` 时输出「请用包管理器/重新下载安装」（MVP 不做自替换安装，`update/install` 留待后续计划）。`--json` 仅与 `--check` 同用，否则 usage error（javdb 同款）。

- [ ] 步骤：semver 表驱动 TDD → release 查询测试（httptest 假 API）→ 命令测试 → changelog 整理 → Commit `feat(cli): update check and milestone changelog`

---

## 后续里程碑（MVP 完成后按独立计划执行）

1. **M6 — direct X 后端（已确认立项，双后端一体化）**：新增 `internal/auth`（会话 cookie 导入：devtools 粘贴 + 浏览器解密读取，pixiv-cli browsercookies 模式；**不做密码登录**）、`internal/x/{appapi,graphql}`（GraphQL 协议层：UserByScreenName / UserTweets / SearchTimeline / ListLatestTweetsTimeline / TweetDetail；queryId 运行时从 x.com JS bundle 动态提取并本地缓存，不移植 Nitter 源码以避 AGPL；提取失败明确报错）、账号池（多账号 + 429 冷却轮换 + CAS，SQLite modernc 存储，pixiv-cli pool 模式，`twitter auth import/list/use/remove/check`）。命令、pipeline、seen/watch 层零改动，仅新增取数实现；config 增加 `backend = "nitter" | "direct"`。风险与设计见 spec §15。**M5 完成后将本里程碑细化为同规格任务计划。**
2. **M7 — MCP server**（pixiv-cli 模式：`internal/mcpserver` + modelcontextprotocol/go-sdk，工具 `user_timeline / search_tweets / list_timeline / get_status / watch_once / instance_report`，输出走 records 投影）。
3. **M8 — 媒体下载**：`twitter media download <tweet-ref>`（图片直链，视频走 xdown 评估）。
4. **M9 — AstrBot 插件对接**：插件侧以 subprocess 调 `twitter watch --once --ndjson` 替换自研抓取层的适配器 + 灰度开关。
5. **M10 — 发布链增强**：Ed25519 签名清单、Homebrew tap、Docker 镜像、skill 发布（ClawHub，对齐两模板的 publish-clawhub 工作流）。

## Self-Review 结论（已自检）

- Spec 覆盖：§5 数据模型→Task 8/10；§6 抓取策略→Task 7/9/13；§7 去重定时→Task 17–19；§8 输出协议→Task 12；§9 错误模型→Task 6；§10 配置→Task 3–5；§11 工程化→Task 2/23；§12 安全边界→Task 22 + README（Task 21）。无缺口。
- 占位符扫描：无 TBD/TODO；Task 10/11 的 fixture 允许「与真实实例比对后修正」被显式定义为 TDD 循环内修正而非延后决策。
- 类型一致性：`twitter.Tweet/Page/InstanceReport`、`pipeline.Envelope/Schema`、`seen.SourceState/MergeSeen`、`watch.Select` 签名在各任务 Consumes/Produces 与代码块一致；`Select` 的 `firstPageIDs` 参数在 Task 18 定义、Task 19 消费，命名一致。
