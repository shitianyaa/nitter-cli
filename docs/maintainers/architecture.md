# 架构说明

本页面向维护者，描述 twitter-cli 的目录结构、包边界与运行期流程。公开契约见
[CLI 参考](../zh-CN/cli-reference.md) 与 [Go SDK](../zh-CN/sdk.md)。

## 总体流程

`cmd/twitter/main.go` 是唯一官方二进制入口，只负责把进程参数和标准流交给
`internal/cli.Run` 并把返回值作为进程退出码。`internal/cli` 根包（`root.go`）
组装 Cobra 命令树、探测 TTY、安装 SIGINT/SIGTERM 信号上下文，并在每次真实命令
前发布基线配置。命令域位于 `internal/cli/commands/*`（每个真实命令一个同名
目录），调用期数据由 `cli/invocation` 提供，取数 wiring 由 `cli/client` 提供，
纯结果投影由 `cli/result` 提供，机器输出协议由 `cli/pipeline` 提供。远程取数
只能经顶层公开 `sdk/`（package twitter）；协议实现细节在 `internal/nitter/*`。

```text
cmd/twitter → internal/cli (root.go → commands/*) → sdk (公开; package twitter)
                         ├── cli/invocation   （RootOptions + Streams + UsageError/退出码）
                         ├── cli/client       （唯一可导入 internal/nitter/* 的 CLI 层包）
                         ├── cli/pipeline     （twitter.pipeline/v1 信封与输出模式）
                         ├── cli/result       （tweet/instance 纯投影）
                         ├── cli/commands/{config,instances,user,search,list,get,watch,seen}
                         └── internal/common/jsonx（NDJSON JSON 编码）
sdk ← internal/nitter/appapi（Client 组合 → timeline/search/list/status/probe）
        └── internal/nitter/protocol/httpx（tls-client 传输：pacing/重试/429/脱敏）
internal/watch（去重选推引擎，纯函数） → internal/storage/seen（seen.json 原子存储）
internal/config/{paths,settings}（~/.twitter-cli 布局与 schema/env 优先级）
internal/buildinfo（版本元数据）
```

## 边界规则（不可违反）

- `cmd/twitter` 只委托：不承载命令逻辑、配置读取或 client 构造。
- `internal/cli/root.go` 拥有命令树、共享流（`Streams`）与进程退出码；子命令包
  **不导入 `internal/cli`**——它们只拿 `*invocation.Streams`（与 `RootOptions`
  同住 invocation，规避导入环）。
- 命令取数只经顶层 `sdk/`（package twitter）；命令包**禁止导入
  `internal/nitter/*`** 协议细节。
- `internal/cli/client` 是**唯一**允许导入 `internal/nitter/{appapi,protocol/
  httpx}` 的 CLI 层包（裁决 R11）。命令消费的能力全部收窄为 client 导出的接口
  （`InstanceTester`、`TimelineSource`、`SearchSource`、`ListSource`、
  `StatusSource`）与类型别名（`TestOptions`）、函数（`ParseStatusRef`）——
  appapi 的类型不越过这堵墙。
- `internal/cli/result` 不编码 JSON、不建命令、不调 SDK 抓取——只把 sdk 类型
  投影为文本（机器输出走 `jsonx`/`pipeline`，由命令自己调用）。
- `internal/cli/pipeline` 不导入 cobra：它只拥有输出模式解析、信封写入与
  EPIPE 判定；对用法的依赖仅限 `invocation.UsageError`。
- `internal/common/jsonx` 不接收 io.Writer、不含用户文案——纯编码语义（关闭
  HTML 转义 + 单行 + 换行符）。
- 任何变更不得引入隐式超时、静默截断、静默降级、静默状态重置：上限必须有配置
  或 flag，默认值写进文档；状态损坏是硬错误（`KindLocalState`，退出码 1），
  绝不静默重置。
- 退出码语义：未知子命令 → 退出码 1；参数/flag/输入契约值不合法 → 退出码 2
  （经 `invocation.UsageError` 包装；`WrapFlagError` 把未知 flag 也归入 2）。
  SDK/网络/本地 I/O 错误绝不包装成 UsageError。

## 关键组件

### `internal/config/paths` + `settings`

paths 管理 `~/.twitter-cli/` 布局（`config.toml`、`state/seen.json`），
`EnsureDefaultConfigFile` 用临时文件 + `os.Link` 实现 no-replace 原子发布
（并发/重复调用安全）。settings 拥有 config.toml schema：env > file > default
优先级（`TWITTER_DEFAULT_LIMIT`/`TWITTER_LOG_LEVEL`/`TWITTER_LOG_FORMAT`），
duration 字符串在 Load 时校验；`SaveKnown` 读整树 → 改已知键 → 原子写 0600，
保留未知键与数组表，注释不保证保留。

### `internal/nitter/protocol/httpx`

tls-client 传输：全局 pacing（`request_interval`）、网络错误/5xx 线性退避重试、
429 按 `Retry-After` 单次等待重试、20s 单请求超时、10MiB 响应体上限。脱敏铁律：
错误链只含状态码与短原因（`sanitizeTransportErr` 剥离内嵌完整 URL 的
`url.Error`）。

### `internal/nitter/appapi`

真实 Client 组合层：timeline（RSS 优先、HTML 用户页回退带游标翻页）、search、
list、status（多形态 URL 解析）、instances probe（RSS/user/search/list 探测）。
实例轮换复用公开 sdk 的 `Chooser`；解析失败按 Kind 分类，不猜测、不兜底。

### `internal/watch` + `internal/storage/seen`

watch 引擎是纯函数 `Select`（无 IO、无时钟）：首跑只记不推（`IncludeExisting`
放开）、MaxNew 截断但 seen 照常吸收全部新 ID（超出部分绝不补推）、MaxNew=0
重建基准、空抓取按源类别分派（user 空首抓初始化、tag/list 保持未初始化）、
初始化源空结果整组保留旧状态。storage/seen 是 schema v1 的 seen.json 存储：
严格解码（未知字段/尾随数据/版本不符都是硬错误）、每源 seen 上限 300、水位
20、原子写（Windows rename 互斥由进程级 writeMu 串行化）。

### `internal/cli/pipeline`

twitter.pipeline/v1 信封协议：`ResolveOutputMode`（`--json`/`--ndjson` 互斥 →
用法错误；否则 TTY=human、非 TTY=text）、`WriteEnvelope`/`WriteErrorEnvelope`
（单行信封，调用方拥有流）、`IsBrokenPipe`（sigpipe 优雅退出 0，Windows 上
尽力而为）。kind 枚举 v1 只增不改；错误信封 `data={command,stage,code,message}`
、`meta.input` 指明输入，绝不携带密钥/URL 查询串。

## 裁决记录

- **R10（传输边界）**：计划中 `WithHTTPClient(*httpx.Client)` 因 httpx → sdk
  导入环不可行——改为 sdk 定义单方法窄接口 `Transport{Get}`（stdlib 类型），
  `*httpx.Client` 结构性满足；CLI wiring 经 `WithHTTPClient` 注入真实传输。
  依赖方向冻结：`internal/nitter/protocol/httpx` 导入 sdk（共享错误 kind），
  sdk 绝不反向导入。
- **R11（取数边界）**：`internal/cli/client` 是唯一允许导入
  `internal/nitter/{appapi,protocol/httpx}` 的 CLI 层包；命令包只经 client 的
  窄接口与 sdk 模型消费数据，绝不导入 `internal/nitter/*`（`go list` 可机器
  验证）。这是「命令不接触协议细节」的唯一通道，冻结。
- **R18（watch 抓取边界，计划偏差，已记账）**：每轮以标准有界抓取（MaxPages
  预算、Limit 0 = 全量）取数后由 `Select` 去重；插件式「到水位即早停」的分页
  推迟到 MVP 后。正确性等价（不重复、不丢失），差异只在预算内抓取量。
