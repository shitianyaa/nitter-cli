# twitter-cli 设计 Spec

> 状态：定稿候选（v1 MVP）
> 日期：2026-09-11
> 模版仓库：`D:\Hermes\pixiv-cli`（进阶模式参考）、`D:\Hermes\javdb-cli`（目录与工程约定主模版）
> 既有资产：`D:\Python\QQBOT\我的插件\Nitter\astrbot_plugin_nitter_tweets`（需求来源与语义真源）

## 1. 背景与目标

用户已有一个成熟的 AstrBot 插件（Python，约 3 万行源码），通过**自建 Nitter 实例**抓取公开推文，实现分组定时推送、seen 去重、多平台投递。现在希望把「推文数据获取 + 去重 + 定时轮询」这层能力抽成一个**独立的 Go CLI**，主要供 **Hermes**（Agent/机器人框架）调用，也可以人工在终端使用。

### 目标（MVP 必须交付）

1. **找推文**：用户时间线、搜索、List 时间线、单条推文（status 链接/ID）。
2. **定时**：`watch` 模式轮询订阅源，产出新推文；支持 `--once`（跑一轮即退出，交给 Hermes/计划任务调度）与常驻循环两种形态。
3. **去重**：跨调用的持久化 seen 状态 + 扫描水位，语义与现有插件一致（首跑只记不推、seen 上限、水位锚定分页边界）。
4. **实例运维**：统一 Nitter 实例列表、RSS 优先/HTML 后备、失败冷却轮换、`instances test` 能力诊断（对应插件 `/镜像测试`）。
5. **Agent 友好**：`--json` / `--ndjson` 机器输出契约、稳定退出码、自带 Agent Skill、（后续）MCP server。

### 非目标（MVP 明确不做）

- **不向 X/Twitter 发帖**。需要写操作就要走 X 官方付费 API，与现有插件「仅获取公开推文」的边界一致。（见 §12 待确认问题 Q1）
- 不做媒体文件下载（只输出媒体 URL；下载复用现有插件/后续再加）。
- 不内置公共 Nitter 实例，不做挑战解算/过盾。
- 不做翻译、不做聊天平台投递（AstrBot 插件与 Hermes 继续负责呈现层）。
- MVP 不做多账户体系（Nitter 后端无账户概念，实例凭证走配置）；多账号凭证池归入 M6 direct 后端（见 §15）。

## 2. 命名与形态

| 项 | 值 |
| --- | --- |
| 仓库名 | `twitter-cli` |
| Go module | `github.com/shitianyaa/twitter-cli`（发布前按实际 GitHub 账号调整） |
| 二进制名 | `twitter` |
| 公开 SDK 包 | 顶层 `sdk/`，`package twitter` |
| 数据目录 | `~/.twitter-cli/`（全平台统一，沿用 javdb-cli 惯例） |
| 配置文件 | `~/.twitter-cli/config.toml` |
| 状态文件 | `~/.twitter-cli/state/seen.json` |
| 输出协议 | NDJSON 信封 `twitter.pipeline/v1`（结构对齐 javdb-cli `javdb.pipeline/v1`） |

命令形态示例：

```bash
twitter user NASA --limit 10 --json
twitter search "#ブルアカ" --ndjson
twitter list 12345
twitter get https://x.com/NASA/status/123...
twitter watch user:NASA tag:%23AI list:12345 --interval 10m --once --ndjson
twitter seen list
twitter instances test
twitter config set default_limit 20
```

## 3. 目录结构（镜像 javdb-cli，已声明同构）

```
twitter-cli/
├── cmd/twitter/main.go              # 12 行：os.Exit(cli.Run(...))
├── internal/
│   ├── cli/                         # 组合根 root.go（唯一文件 + root_test.go）
│   │   ├── commands/<name>/         # 每命令一目录，New(options, streams) *cobra.Command
│   │   │   ├── user/ search/ list/ get/ watch/ seen/ instances/ config/ update/
│   │   ├── invocation/              # RootOptions / Streams（全局 flag + TTY 探测）
│   │   ├── pipeline/                # 输出模式解析 + NDJSON 信封 + 严格解码
│   │   ├── result/                  # 纯行投影（人类可读输出，不碰 io/JSON/cobra）
│   │   └── client/                  # 代理校验 + sdk 构造（flag>env>config 优先级落点）
│   ├── nitter/
│   │   ├── appapi/                  # 实现层组合根：transport + rss/html 端点
│   │   ├── rss/                     # encoding/xml RSS 解析
│   │   ├── html/                    # goquery HTML 解析（时间线/搜索/List/单条）
│   │   └── protocol/httpx/          # tls-client 传输、重试、节奏、代理
│   ├── config/
│   │   ├── paths/                   # AppDirName、EnsureDefaultConfigFile（原子不覆盖）
│   │   └── settings/                # TOML 结构化树、保留未知键、原子写
│   ├── storage/seen/                # seen.json 状态库（原子写 + 文件锁）
│   ├── common/                      # 纯叶子工具（jsonx、timex、strx）
│   ├── buildinfo/                   # -ldflags 注入 Version/Commit/BuildDate
│   └── update/                      # --check 查询 GitHub Releases（MVP 只查不装）
├── sdk/                             # package twitter —— 唯一公开 Go SDK
│   ├── client.go models.go errors.go page.go pipeline.go
│   └── contract_external_test.go    # 编译期签名冻结
├── skills/twitter-cli/              # 随仓库分发的 Agent Skill（SKILL.md + references/）
├── docs/{en,zh-CN,maintainers}/     # 双语文档 + 维护者架构/开发文档
├── changelog/{unreleased,vX.Y.Z}/   # Keep a Changelog（unreleased 驱动）
├── e2e/run.sh                       # exit 0 过 / 1 挂 / 2 仅 SKIP（凭证缺失）
├── scripts/                         # build.sh、build-release.sh、test-*.sh
├── templates/homebrew/              # 后续发布用
├── .github/workflows/{ci.yml,release.yml,e2e.yml}
├── .pre-commit-config.yaml          # gofmt + go test ./... 本地钩子
├── AGENTS.md                        # CLAUDE.md = "@AGENTS.md" 单一真源
└── go.mod                           # go 1.27.x，CGO_ENABLED=0
```

**边界规则**（与模版一致，写进 AGENTS.md）：

- `cmd/twitter` 只委托；`internal/cli/root.go` 拥有命令树与组装；子命令包不导入 `internal/cli`。
- CLI 命令只经 `sdk`（`package twitter`）取数，绝不导入 `internal/nitter/*`（协议细节）。
- `internal/common` 不接收 `io.Writer`、不含用户文案；`internal/cli/result` 不编码 JSON、不建 cobra 命令。
- 顶层 `sdk/` 的导出签名是兼容契约：只增不改不删，`contract_external_test.go` 冻结。

## 4. 技术栈

| 用途 | 选择 | 理由 |
| --- | --- | --- |
| CLI 框架 | `spf13/cobra` | 两个模版一致 |
| HTTP 传输 | `bogdanfinn/tls-client`（Chrome profile） | 模版一致；自建 Nitter 可能前置 Nginx/WAF，指纹客户端是免费保险 |
| HTML 解析 | `PuerkitoBio/goquery`（基于 `golang.org/x/net/html`） | 插件用正则解析 Nitter DOM，Go 版用 DOM 选择器更稳（引用媒体屏蔽等规则天然可用祖先过滤表达） |
| RSS 解析 | 标准库 `encoding/xml` | Nitter RSS 结构固定，无需 feed 库 |
| 配置 | `pelletier/go-toml/v2` | 与 javdb-cli 一致；结构化树保留未知键 |
| JSON 行 | 标准库 + `SetEscapeHTML(false)` 单行契约 | `internal/common/jsonx` |
| 构建 | `CGO_ENABLED=0`、`-trimpath`、`-ldflags -X` 注入版本 | 与 javdb-cli 一致，纯静态单文件 |

Go 版本基线：1.27.x（对齐 pixiv-cli 2026-09 现状）。

## 5. 数据模型（`sdk/models.go`）

```go
// Tweet 是 SDK 对外推文投影；字段即 NDJSON data 契约，只增不改。
type Tweet struct {
    ID          string    // X 纯数字 status ID（字符串，避免 JS 精度丢失）
    URL         string    // 规范化 https://x.com/<user>/status/<id>
    Text        string    // 纯文本正文（HTML 已折叠：<br>→\n，引用/emoji 图片剔除）
    Author      Author
    PublishedAt time.Time // UTC，RFC3339 序列化
    Media       []Media   // 图片直链（pbs.twimg.com，quality 重写）+ 视频占位链接
    IsRetweet   bool      // 纯转推（retweet-header 判定，同插件语义）
    RepostedBy  string    // 转推者 handle（非空时 IsRetweet 为 true）
    ReplyTo     string    // 被回复者 handle，可为空
    Quote       *Quoted   // 被引用推文摘要（id/url/text/author），可空
}

type Author struct { Handle, Name, AvatarURL string }
type Media  struct { Type string /* image|video|gif */; URL string; Width, Height int }

// Page 是分页结果；NextCursor 为空表示没有更多。
type Page[T any] struct { Items []T; NextCursor string }

// InstanceReport 是 instances test 的逐实例逐能力结果。
type InstanceReport struct {
    URL      string
    RSS      Probe // ok|fail{status,err}
    UserHTML Probe
    Search   Probe
    List     Probe
    Latency  time.Duration
}
```

RSS 与 HTML 两条解析路径统一投影到 `Tweet`（RSS 拿不到的字段留零值，不造假）。

## 6. 抓取策略（从插件 instances-guide 移植）

1. **统一实例列表**：`[[instances]]` 同时服务 RSS、HTML、搜索、List；不设独立池。
2. **用户时间线**：固定 RSS 优先，失败或空结果时同一实例列表回退 HTML 用户页（无开关，与插件一致）。
3. **轮换**：按配置顺序轮换；实例发生 429/网络错误进入冷却（默认 60s，配置 `instance_cooldown`），冷却期跳过；成功即复位。不做成功率加权（YAGNI，插件场景实例≤5 个）。
4. **重试**：`retry_attempts`（默认 2）线性退避 `retry_delay`（默认 1s）；429 且实例返回 `Retry-After` 时按其值等待一次（pixiv-cli 的 parseRetryAfter 语义，含 int64 溢出防护）。
5. **节奏**：`request_interval`（默认 1s）全局最小请求间隔（pixiv-cli pacingRoundTripper 模式）。
6. **分页**：时间线/搜索/List 走 Nitter `cursor=` 分页，`--limit` 达到即停；`--max-pages` 上限（默认 5，防野分页）。
7. **信任边界**（照抄插件）：只允许访问配置里用户自己控制且信任的实例；实例返回的媒体/重定向 URL 会按需输出，因此文档必须警告网络层隔离。无任何挑战解算。

**Nitter DOM/格式事实**（实现解析器时以插件 `media_support/html_backend/parser.py` 与 `client.py` 为语义真源）：

- HTML：`<div class="timeline-item">` 分块；`href="/<user>/status(es)?/<id>"` 定位；正文 `div.tweet-content`（必须剔除 `quote`/`quote-*`/`i/article` 子树内的同名节点）；时间取 `span.tweet-date a[title]`（如 `Jul 27, 2026 · 9:09 AM UTC`，需解析为 UTC）；媒体 = `a.still-image[href]`、`/pic/orig/media`、`/pic/media`（无 orig 时回退）、`pbs.twimg.com/media`（pbs 链接重写 `name=orig`）、视频 = `/video/` 与 `video.twimg.com`；纯转推 = `tweet-content` 之前的 `retweet-header`/`retweeted by`/「转推了」；游标 = `.show-more a[href*=cursor=]`。
- RSS：`<item>` 的 `guid`/`link`（Nitter 站内 `/user/status/id#m`）取 user+id；`title`/`description`（HTML 片段 → 纯文本）；`pubDate`（`Mon, 02 Jan 2006 15:04:05 -0700`）；`media:content` 缩略图。
- 页面分类：真实时间线 / 空页 / 登录·维护·错误页（→ 分类错误），不做更多挑战探测。

## 7. 去重与定时（`watch` + `storage/seen`）

状态文件 `~/.twitter-cli/state/seen.json`，schema v1：

```json
{
  "version": 1,
  "sources": {
    "user:NASA": {
      "initialized": true,
      "seen_ids": ["2081..."],
      "watermark_ids": ["2081..."],
      "updated_at": "2026-09-11T15:04:05Z"
    }
  }
}
```

语义（全部从插件 `storage/seen.py` + `scheduler/runner_seen.py` + architecture.md 移植，单消费方简化版）：

| 规则 | 值/行为 |
| --- | --- |
| seen 键 | `<type>:<ref>`：`user:NASA`、`tag:%23ブルアカ`、`list:12345` |
| seen 上限 | 每源 300 条（插件 `SEEN_LIMIT_PER_USER`），新 ID 前插、超限裁尾 |
| 扫描水位 | 每源最近 20 个 status ID（插件 `_normalize_scan_baseline_ids`），用于分页边界与「首页是否有效」判定 |
| 首跑 | `initialized=false` → 记录 seen + 水位、**不产出任何推文**（插件铁律「首次只记 seen，不推历史」）；`--include-existing` 可显式放开 |
| 选取 | 新推文 = 本轮抓取结果中 ID ∉ seen 的推文 |
| 写入时机 | 所有产出交付完成后合并 seen、替换水位；**准备/抓取失败不写状态**（保留旧水位，下轮重试，与插件「transient_failure 不写 seen」一致） |
| 抓取边界 | 逐页抓取直到：命中 seen 已知 ID（水位边界）或 `--max-pages` 耗尽；首页有有效 ID 但扫描不完整时按 `--max-new`（0=不产出并重建基准，>0=最多推 N 条后重建）——完全对齐插件规则 |
| 单源失败 | 写入 `kind:"error"` 信封、该源状态不动、其余源继续（javdb 批次信封模式）；收尾汇总 `N of M failed` → exit 1 |

`watch` 运行形态：

- `twitter watch <source>... [--interval 10m]`：常驻循环，逐轮输出新推文 NDJSON，SIGINT/SIGTERM 优雅退出。
- `--once`：单轮即退。**这是给 Hermes 的推荐形态**：由 Hermes/计划任务定时拉起，stdout 直接被消费，进程边界即状态边界。
- 源可以走 argv，也可以走配置 `[[watch.sources]]`（argv 为空时的默认）。
- `--state-dir` 可覆盖状态目录（测试/多机隔离）。

`twitter seen list [--json]`、`twitter seen clear [--source S] [--confirm]`：状态可检视、可清（clear 默认要求 `--confirm`，对齐「状态变更需显式授权」的 skill 规则）。

## 8. 输出协议（`internal/cli/pipeline`）

对齐 javdb-cli：

- `pipeline.ResolveOutputMode(ndjsonFlag, jsonFlag, stdoutIsTTY)`：`--json`/`--ndjson` 互斥（usage error）；TTY 默认人类文本，非 TTY 默认稳定行文本。
- **人类文本**：纯制表符行 + 空列表提示到 stderr；无 ANSI 颜色（两模版均无，保持）；`internal/cli/result` 纯投影。
- **`--json`**：单条 = 命令自身形状；多条 = 信封数组（cardinality 规则）。
- **`--ndjson`**：`twitter.pipeline/v1` 信封：

```json
{"schema":"twitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{…Tweet…},"meta":{"source":"user:ErroR_eroi","instance":"http://nitter:8080","fetched_at":"2026-09-11T15:04:05Z"}}
```

  kind 枚举 v1：`tweet | user | list | instance_report | seen_entry | error`。错误信封 `data={command,stage,code,message}`，永不携带密钥/URL 查询串/实例凭证。
- **stdin 管道**：`twitter get` 等 ref 类命令支持 stdin 传入 URL/ID；NDJSON 流可经 `--on-error skip|fail-fast` 逐行消费（pixiv-cli pipeline 模式，MVP 只做 ref 单值，流式消费列为后续）。
- **退出码**：0 成功；1 运行时失败（含批次部分失败）；2 usage/flag/输入契约错误。`usageError` 只包参数与输入问题，SDK/网络错误一律不包（pixiv-cli 铁律）。NDJSON 下游 EPIPE = 成功退出（pixiv-cli sigpipe 政策）。
- **诊断**：stderr 输出，`log_level=debug` 时启用；`log_format=json` 单行 JSON；正常模式无日志污染 stdout。

## 9. 错误模型（`sdk/errors.go`）

```go
type Kind string // 稳定枚举，只增不改
const (
    KindChallenge    Kind = "challenge_required"     // 登录/维护/挑战页
    KindRateLimited  Kind = "rate_limited"           // 429（携带 RetryAfter *time.Duration）
    KindNotFound     Kind = "not_found"
    KindUnavailable  Kind = "upstream_unavailable"   // 实例不可达/5xx
    KindMalformed    Kind = "malformed_upstream_response"
    KindInvalidArg   Kind = "invalid_argument"
    KindLocalState   Kind = "local_state_error"      // 状态文件损坏等
)

type Error struct { Kind Kind; Op string; Err error; RetryAfter *time.Duration }
```

- `%w` 包裹 + 短阶段前缀（`"parse timeline: %w"`）；`errors.As` 判型。
- 错误链永不包含：实例凭证、完整 URL 查询串、请求头、响应体（与 pixiv-cli 脱敏铁律一致）。
- CLI 侧按 Kind 映射人类文案与退出策略；`KindChallenge` 的文案指引自检实例部署（照插件常见错误表）。

## 10. 配置（`~/.twitter-cli/config.toml`）

```toml
default_limit     = 20        # 手动命令默认条数
max_pages         = 5         # 单次抓取分页上限
request_interval  = "1s"      # 全局最小请求间隔
retry_attempts    = 2
retry_delay       = "1s"
instance_cooldown = "60s"     # 实例失败冷却
proxy             = ""        # 空 = 走 HTTPS_PROXY/ALL_PROXY 环境变量
log_level         = "info"    # debug|info
log_format        = "text"    # text|json

[[instances]]
url = "http://nitter:8080"
# username = ""              # 预留：反代基础认证
# password = ""

[[watch.sources]]
id = "user:NASA"             # watch 无 argv 源时的默认
```

- 优先级：CLI flag > env > file > 默认（javdb-cli 语义）。Env：`TWITTER_LOG_LEVEL`、`TWITTER_LOG_FORMAT`、`TWITTER_DEFAULT_LIMIT`、`HTTPS_PROXY`。
- `config get/set/unset/path`：未知键先拒绝再落盘；写入用「读整树→改已知键→原子写」保留未知键（注释不保证保留，javdb-cli 同款语义；若将来需要保留注释再引入 `creachadair/tomledit`）；`0600`。
- 敏感值（未来实例凭证）在 `config get` 中输出 `<redacted>`。
- 首次运行 `EnsureDefaultConfigFile` 生成基线文件：临时文件 + `os.Link` 不覆盖语义，并发安全。

## 11. 测试与工程化

| 层 | 做法 |
| --- | --- |
| 单元测试 | 每包邻接 `*_test.go`；解析器用 `testdata/*.html`/`*.xml` fixture（从插件测试样例与真实页面裁剪）；外部测试包优先 |
| SDK 契约 | `sdk/contract_external_test.go` 编译期 `var _` 冻结全部导出签名 |
| 纯函数测试重点 | `watch` 选推逻辑（首跑/上限裁剪/水位推进/失败不落盘）表驱动全覆盖 |
| e2e | `e2e/run.sh`：离线契约（版本格式、config 落盘、`--help`、NDJSON 信封 schema）；实网只读探针 `TWITTER_CLI_E2E_INSTANCE=...` env 门控；exit 2 = 仅 SKIP 视为软通过 |
| pre-commit | `gofmt -l` 必须为空 + `go test ./...`（两模版一致） |
| CI | ci.yml：gofmt / vet / `go test ./... -count=1` / `-race` / `scripts/build.sh`；e2e.yml 读 evn 门控；release.yml 后置（见 §13） |
| 提交 | Conventional Commits 单行英文小写 subject（`feat(cli): ...`）；changelog/unreleased 双语条目带 PR 链接 |
| 文档义务 | CLI 行为/配置键/输出语义变更必须同步：双语 cli-reference、README、skills/twitter-cli、changelog |

## 12. 安全与合规边界（写进 SKILL.md 与 README）

1. 仅获取公开推文；不提供绕过访问控制的能力；不内置公共实例、不解算挑战。
2. 只应配置用户自己控制且信任的 Nitter 实例；CLI 会跟随实例返回的媒体/重定向 URL，须在网络层隔离内部服务（Redis、云 metadata、管理面）。
3. Agent 铁律：不回显配置中的凭证；状态变更（`seen clear`、`config set`）逐次授权、授权不延续；不发明 flag；先看退出码再解析 JSON；`--json` 只描述成功输出。
4. 不加隐式超时/截断/静默降级：任何上限都必须有配置或 flag，且默认值写进文档（两模版共同的「No silent fallback」工程价值）。

## 13. 里程碑划分

| 里程碑 | 内容 | 交付判据 |
| --- | --- | --- |
| M0 骨架 | 仓库脚手架、root、版本、构建脚本、AGENTS.md | `twitter --version` 可构建可测 |
| M1 配置与传输 | config 子系统、httpx、分类错误、instances test | `twitter instances test` 对真实自建实例出报告 |
| M2 时间线 | RSS/HTML 解析、sdk.UserTimeline、pipeline、`twitter user` | 三种输出模式 + 契约测试 |
| M3 搜索/List/单条 | search、list、get | 三命令三输出模式 |
| M4 watch + 去重 | seen 存储、watch 引擎、seen 命令 | `--once` 全语义表驱动验证 |
| M5 文档与发布 | 双语文档、Skill、e2e、update --check、release workflow | skill 可被 Agent 直接使用 |
| M6 direct X 后端（已确认立项） | 双后端一体化：`internal/auth` 会话导入 + `internal/x` GraphQL 协议层 + 账号池（SQLite/CAS/429 冷却轮换）；命令、pipeline、seen/watch 层零改动复用（设计见 §15） | 同一命令集在 `backend=direct` 下全部通过；账号轮换/冷却表驱动验证 |
| M7+ | MCP server、媒体下载命令、AstrBot 插件对接、发布链增强 | 另行立计划 |

## 14. 假设与待确认问题

- **Q1「定时发推文」释义**：本 spec 按「定时**推送**抓取到的新推文给 Hermes/下游」设计（watch 模式）。若实际含义是「定时**发帖到 X**」，需要 X 官方付费 API，属于完全不同的能力包，请确认后再立补充 spec。当前按前者推进。
- **Q2 二进制名**：默认 `twitter`（repo `twitter-cli`）。若介意与 X 商标重名或与本机命令冲突，可在 M0 前改名（改 `AppDirName`、module、cobra `Use` 共 3 处 + 文档）。
- **Q3 SDK module 路径**：`github.com/shitianyaa/twitter-cli` 按插件 repo 作者推断，发布前确认。
- **Q4 Hermes 对接形态**：MVP 以「计划任务拉起 `watch --once` + stdout NDJSON」为准；若 Hermes 支持 MCP，M7 的 MCP server 是更优通道（pixiv-cli 已验证该模式）。

## 15. 双后端决策记录（2026-09-12）

**决策**：采用双后端一体化架构，Nitter 后端先行（本计划 M0–M5），direct X 后端随后（M6）。

- 动机：消除「单独部署 Nitter 服务」的依赖，单二进制直连 X——与 pixiv-cli（内嵌 Pixiv App API）/javdb-cli（内嵌签名 API）的模板模式一致。
- 抽象边界：`sdk.Tweet/Page/Client` 是唯一上层契约；后端差异只存在于取数实现层（`internal/nitter` ↔ `internal/x`），config 增 `backend = "nitter" | "direct"` 键选择。命令、pipeline、seen/watch、输出协议全部复用，零改动。
- direct 后端设计要点：
  - **认证**：仅会话 cookie 导入（`auth_token` + `ct0`），支持 devtools 粘贴与浏览器解密读取（pixiv-cli `internal/browsercookies` 模式）；**不做密码登录**（X 登录流有强 bot 防护）。
  - **多账号**：账号池是一等公民——`twitter auth import/list/use/remove/check` + 429 冷却轮换 + CAS 落盘（SQLite modernc，纯 Go），完整复刻 pixiv-cli pool 模式。
  - **协议**：`x.com/i/api/graphql/<queryId>/<Op>`，公开 web Bearer 常量 + `x-csrf-token` 头；queryId 运行时从 x.com JS bundle 动态提取并本地缓存（twikit 式），**不移植 Nitter 源码**（AGPL 传染风险）；提取失败明确报错，无静默兜底。
  - **所需操作仅 5 个**：UserByScreenName、UserTweets、SearchTimeline、ListLatestTweetsTimeline、TweetDetail；响应投影到 `sdk.Tweet`（插件 `output/status_*.json` 即该 JSON 形状的现成样例）。
- 风险（须写入 README 与 Skill）：GraphQL 端点/字段随 X 迭代可能失效（动态提取缓解，非免疫）；真实账号高频抓取有限速与封号风险，需节奏控制 + 账号冷却（与自建 Nitter 挂账号同性质）；`auth_token` 因登出/换浏览器失效，需重新导入。
