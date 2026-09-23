# nitter-cli

[English](README.md) · [简体中文](README.zh-CN.md) · [文档导航](docs/index.zh-CN.md)

<p><a href="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml"><img alt="ci" src="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml/badge.svg"></a> <a href="https://github.com/shitianyaa/nitter-cli/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/shitianyaa/nitter-cli?style=flat-square"></a> <a href="go.mod"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/shitianyaa/nitter-cli?style=flat-square"></a> <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/shitianyaa/nitter-cli?style=flat-square"></a></p>

`nitter` 是一个非官方的**公开推文**命令行客户端。数据来自 **FxTwitter 公共
API**（默认快车道——无需账号、无需凭证），并以**你自己部署的 Nitter 实例**作为
私有回退、List 数据通路与纯自托管模式。它抓取用户时间线、搜索结果、List 时间
线、单条推文、对话评论区、关注列表、博主名片、引用推文与实时热搜，支持带持久化
去重状态的持续监视与媒体下载——一个为 agent 与调度器（cron、systemd timer、
Hermes）打造的灵活 CLI，以 NDJSON 消费。

它同时是一个公开 Go SDK（`github.com/shitianyaa/nitter-cli/sdk`，package
`nitter`），数据模型稳定、只增不改。

## 为什么选择 nitter-cli？

- **FxTwitter 快车道 + Nitter 深度**——`fetch_backend` 决定路由：`mix`
  （默认）优先尝试 FxTwitter 公共 API（无需账号、无需凭证），失败时回退到你
  自建的实例；`nitter` 让所有抓取都留在你自控的实例上；`fx` 固定快车道。
  List 始终需要 Nitter。`mix`/`fx` 模式下 handle 与查询词会发送至
  `api.fxtwitter.com`——需要纯自托管请选 `nitter`。
- **灵活的实例策略**——指向任何一个你自主控制的 Nitter 实例：配置轮换集
  （`[[instances]]`，严格按配置顺序轮换，失败实例进入冷却），单次用
  `--instance` 覆盖，并可用主机级 basic auth 认证（`username` **和**
  `password` 同时设置）。凭证只会附着在发往其所属实例的请求上——第三方
  端点永远看不到它们。
- **公开推文检索**——`user`（RSS 优先，失败或空结果时回退 HTML 用户页）、
  `search`、`list`，以及用 `get` 抓单条推文。`list` 严格隔离：所有 List
  抓取始终走你自己的实例（FxTwitter 没有 List 端点）。不内置实例、不登录、
  不绕过访问控制。
- **社交图谱与发现**——`following` 查看某账号关注了谁，`profile` 输出博主
  名片，`search --type user` 按名字搜创作者/画师，`trends` 看 X 当下在聊
  什么，`quotes` 挖掘某条推文的引用二创——全部无需凭证。
- **对话与回复树**——`comments` 拉取某条推文的对话链与评论区（可按高赞或
  最新排序），是找到博主自评隐藏链接、追更连环长推的最快路径。
- **创作者圈子**——`nitter circle` 在 `~/.nitter-cli/circles.toml` 维护主题
  花名册（`list` / `show` / `suggest` / `add` / `run`）：`suggest` 从某博主的
  关注列表与转推原作者中挖出新候选成员，再按需发现与管道流式拉取，与调度
  型的 `watch` 订阅各司其职。
- **可组合的管道**——stdout 是管道时，数据命令自动输出 `nitter.pipeline/v1`
  NDJSON，`nitter search "..." | nitter download` 无需任何 flag；`--json`
  提取完整文档，显式 flag 永远优先。
- **为调度器而生的 watch**——`watch --once` 对照持久化状态执行恰好一轮去重
  后退出；`--max-new-overflow keep` 策略让突发超限的推文在后续各轮重新推送，
  而不是被丢弃（宁重勿丢）。
- **带模板的媒体下载**——带视频的推文收敛为一个最佳文件（按码率排序，其余
  候选作回退），纯图推文下载全部图片，`--kind cover` 只取封面；
  `filename_template` / `directory_template` 配置键（外加
  `--filename-template`）通过 `{id}` `{seq}` `{user}` `{kind}` `{ext}` 占位符
  命名并放置文件。
- **可观测的实例**——`instances test` 逐实例探测 RSS / 用户时间线 / 搜索 /
  List 能力，每个实例一行报告；报告本身就是产品。
- **可管理的状态**——`config path/get/set/unset` 管理十三个标量键，
  `seen list/clear [--state-dir]` 管理 watch 去重状态；写入全部原子化，状态
  文件损坏是硬错误（绝不静默重置）。
- **诚实的更新检查**——`update --check [--prerelease] [--json]` 按严格 semver
  与 GitHub 最新发布版比较，不做自替换安装。
- **公开 Go SDK**——类型化模型（`Tweet`、`Profile`、`Conversation`、`Trend`
  等）的 JSON 键永不消失、狭窄的 `Transport` 边界、脱敏的错误；CLI 命令消费
  的就是同一套接口。

## 安装

### Release 压缩包（推荐）

从 [GitHub Releases](https://github.com/shitianyaa/nitter-cli/releases)
下载对应平台的压缩包，并对照**同一发布版**附带的 `checksums.txt` 校验：

```bash
sha256sum -c checksums.txt --ignore-missing   # 或等价工具
```

把 `nitter` 二进制解压到一个已在 `PATH` 上的用户级目录。本项目没有安装脚本；
压缩包就是全部产品。

### 源码构建

需要 Go 1.27+：

```bash
sh scripts/build.sh          # 生成 ./nitter
./nitter --version           # nitter version 0.7.0（或 dev 版本行）
```

### 让 AI Agent 安装

把下面这一段 prompt 复制给能够操作本机终端的 Codex、Claude Code、Cursor 或
其他 AI Agent：

```text
请为这台机器安装 https://github.com/shitianyaa/nitter-cli 的最新 stable 版本：只从仓库 Releases 页下载与检测到的操作系统、架构对应的官方 GitHub Release 压缩包，替换任何文件之前先对照该发布版附带的 checksums.txt 完成 SHA-256 校验，把 `nitter` 二进制安装到无需管理员或 root 权限的用户级目录，仅当 `nitter` 尚不可达时才把该目录加入当前用户的 PATH（报告每一处 PATH 变更），缺少任何前置工具时先征求同意，安装过程中绝不读取或输出 ~/.nitter-cli/config.toml 或任何 Nitter 凭证，最后运行 `nitter --version` 验证，并报告安装版本、二进制路径以及全部文件和 PATH 变更。

同时安装与该 stable 发布 tag 完全一致的 `nitter-cli` Skill（不要跟随 main）：把该 tag 的 git 树下完整的 skills/nitter-cli/ 目录安装到用户确认的 Agent skills 目录。不要猜测 skills 路径，也不要用 main 上的 skill 内容。
```

## 60 秒快速上手

nitter-cli 不内置实例，默认的 `mix` 模式开箱即用：`user`、`search`、`get`、
`comments`、`following`、`profile`、`quotes`、`trends` 与 `search --type user`
都会直接走 FxTwitter 的公共端点。配置一个你自控的 Nitter 实例可用于：`list`、
纯自托管的 `nitter` 模式，以及 Fx 不可用时的回退。还没有实例？用 Docker 自建
一个——参见[上游 wiki](https://github.com/zedeus/nitter/wiki) 或社区
[自建指南](https://github.com/sekai-soft/guide-nitter-self-hosting)；
AI agent 可按 skill 的 [deploy 参考](skills/nitter-cli/references/deploy.md)执行。

```bash
# 0. 配置你的实例——`nitter config path` 打印配置文件路径（首次真实命令会
#    生成带注释示例的文件），加入：
#      [[instances]]
#      url = "http://nitter.internal:8080"
#      # username = ""        # 可选 basic auth——两者都设置才会发送
#      # password = ""
nitter config path

# 诊断实例：能力探测，每实例一行（rss / user_html / search / list / latency；
# 单元格为 ok、fail(<原因>) 或 -）
nitter instances test
nitter instances test http://nitter.internal:8080 --full

# 抓取时间线，输出 JSON 给 Hermes 或任意 agent
nitter user NASA --limit 10 --json

# 发现类能力——无需任何实例或账号
nitter trends --limit 10 --json                   # X 当下在聊什么
nitter following NASA --limit 10                  # 某账号关注了谁
nitter profile NASA                               # 博主名片卡
nitter comments 2100031016471818431 --limit 10    # 回复树（博主自评的隐藏链接就在这）
nitter quotes <STATUS_ID> --media-only | nitter download   # 引用推文衍生素材批量下载
nitter circle run ai_researchers --limit 1            # 流式拉取私人圈子花名册

# 管道输出无需 flag——数据命令自动输出 NDJSON，流可直接喂给下载命令
nitter search "#AI" --limit 20 | nitter download --output ./media

# 以最低画质档下载视频
nitter download https://x.com/NASA/status/2081668333762687236 --quality low

# 只取视频封面图
nitter download https://x.com/NASA/status/2081668333762687236 --kind cover

# 交给调度器做去重监视：每次调用一轮，只出新推文，状态跨次运行持久保存
*/10 * * * * nitter watch user:NASA tag:#AI --once --ndjson >> /var/log/nitter-watch.ndjson 2>>/tmp/nitter-watch.err
```

运行 `nitter --help` 或打开[完整 CLI 参考](docs/zh-CN/cli-reference.md)，
查看每条命令、flag、配置键与更新行为。

## 选择你的接口

### CLI

交互时用人类表格；`--json` 输出完整文档，`--ndjson` 输出记录流——或者直接
接管道：非 TTY 的 stdout 默认就是 NDJSON。

```bash
nitter user NASA --limit 10                      # TTY 下输出制表符行
nitter user NASA --limit 10 --json               # 单对象 / 数组
nitter user NASA --limit 10 --ndjson             # 每条推文一个 nitter.pipeline/v1 信封
nitter search "#AI" --limit 20 | nitter download # 自动 NDJSON，无需 flag
```

### Go SDK

公开 SDK 是包 `github.com/shitianyaa/nitter-cli/sdk`（package 名 `nitter`）
——CLI 消费的就是同一套接口。它由轮换实例集、狭窄的 `Transport` 接口和实例
冷却时长组合出 `Client`；数据模型（`Tweet`、`Page[T]` 等）无条件序列化
（数据字段不用 `omitempty`），遵循只增不改的稳定性契约，错误经过脱敏
（无凭证、无查询串、无请求头/响应体）。

```go
import "github.com/shitianyaa/nitter-cli/sdk"

client, err := nitter.New(
    nitter.WithInstances([]nitter.Instance{
        {URL: "http://nitter.internal:8080"},
    }),
    nitter.WithHTTPClient(transport), // 实现 Transport 接口
    nitter.WithCooldown(30 * time.Second),
)
```

`nitter.Instance` 携带实例的可选 basic-auth 凭证对；脱敏契约保证它不出现在
任何错误与日志中。[SDK 指南](docs/zh-CN/sdk.md)记录了稳定性契约、模型、
分页与错误类型。

## 配置

`~/.nitter-cli/config.toml`（TOML，权限 0600）。优先级：CLI flag > 环境变量 >
文件 > 内置默认。`nitter config get` 查看生效值；`nitter config set KEY VALUE`
写入（值也可从 stdin 管道读入）；`nitter config unset KEY` 删除。`[[instances]]`
与 `[[watch.sources]]` 数组表只能直接编辑文件——`config set` 拒绝它们。创作者
圈子（`nitter circle` 花名册）存放在独立文件 `~/.nitter-cli/circles.toml`，由
`circle` 子命令管理。

### 标量键（全部十三个）

| 键 | 类型 | 默认 | 环境变量覆盖 | 含义 |
| --- | --- | --- | --- | --- |
| `default_limit` | int | `20` | `NITTER_DEFAULT_LIMIT` | 手动命令未传 `--limit` 时的条数（`0` = 全部） |
| `max_pages` | int | `5` | — | 单次抓取分页上限（在 `user` 上，显式传 `--max-pages 0` 则取消上限） |
| `request_interval` | duration | `1s` | — | 相邻请求开始时间的全局最小间隔 |
| `retry_attempts` | int | `2` | — | 首次之外的重试次数（网络错误与 5xx） |
| `retry_delay` | duration | `1s` | — | 线性退避基数：第 n 次重试等待 `retry_delay × n` |
| `instance_cooldown` | duration | `60s` | — | 实例失败（429 / 网络错误）后的冷却时长 |
| `fetch_backend` | 枚举 | `mix` | `NITTER_FETCH_BACKEND` | 抓取后端策略：`mix`（默认优先 FxTwitter，故障回退自建 Nitter）、`nitter`（纯 Nitter 实例）、`fx`（纯 FxTwitter） |
| `proxy` | string | `""` | — | 代理 URL（`http(s)`、`socks5(h)`）；空 = 走环境代理（`HTTPS_PROXY`/`ALL_PROXY`） |
| `log_level` | 枚举 | `info` | `NITTER_LOG_LEVEL` | `debug` 或 `info`；诊断只进 stderr，不污染 stdout |
| `log_format` | 枚举 | `text` | `NITTER_LOG_FORMAT` | `text` 或 `json`（单行） |
| `download_path` | string | `./nitter-media` | — | `nitter download` 写入媒体的位置（相对当前工作目录；按需创建；`download --output DIR` 单次覆盖） |
| `filename_template` | string | `{id}-{seq}` | — | 普通媒体的下载文件名，占位符 `{id}` `{seq}` `{user}` `{kind}` `{ext}`（默认 = 模板出现前的 `<id>-<seq>.<ext>` 命名；封面始终 `<id>-cover.<ext>`；`download --filename-template` 单次覆盖；非法模板告警并回退默认） |
| `directory_template` | string | `""` | — | `download_path` 之下的下载子目录，占位符 `{id}` `{user}` `{kind}`（`/` 分隔层级；禁用 `{seq}`/`{ext}`；空 = 平铺） |

环境变量优先于文件：`NITTER_DEFAULT_LIMIT`（整数）、
`NITTER_LOG_LEVEL`、`NITTER_LOG_FORMAT`、`NITTER_FETCH_BACKEND`。

### 数组表

```toml
[[instances]]
url = "http://nitter.internal:8080"   # 必填
username = ""                         # 可选 basic auth（见下注）
password = ""

[[watch.sources]]                     # `nitter watch` 无参数时的默认来源
id = "user:NASA"                      # user:<handle> | tag:<query> | list:<id>
```

注意：当 `[[instances]]` 条目**同时**设置 `username` 与 `password` 时，发往该
实例的请求会携带 HTTP basic auth。凭证策略在传输层内按主机限定——凭证只会
附着在发往其所属实例的请求上，第三方媒体端点（`media`/`download` 的解析器，
如 fx/xdown 与 twimg）永远收不到它；凭证也不会进入错误、日志
或响应。只设置一半的凭证对视为未配置。单次的 `--instance URL` 覆盖是纯 URL，
**不携带**凭证——需要认证的实例请使用配置条目。无法配置凭证的实例，请在网络
层为其加访问控制。

## 输出模式

数据命令（`user`、`search`、`list`、`get`、`media`、`download`、`following`、
`comments`、`trends`、`quotes`、`profile`、`circle run`、`instances test`）用
同一套规则解析输出模式：

| 模式 | 触发方式 | 形态 |
| --- | --- | --- |
| 人类 / text | **TTY** 下的默认 | 每条记录一行制表符分隔；空结果在 **stderr** 打印 `(empty)` |
| NDJSON | **stdout 非 TTY 时（管道/重定向）为默认**，无需 flag；也可显式 `--ndjson` | 每条记录一个 `nitter.pipeline/v1` 信封，一行一条 |
| JSON | `--json` | 恰好一条记录时是单个 JSON 对象，否则是 JSON 数组，空结果是 `[]` |

**stdout 为管道时默认输出 NDJSON（0.6.0 行为变更）。** stdout 是管道或文件、
且未显式给出 `--json`/`--ndjson` 时，数据命令输出 `nitter.pipeline/v1` 信封
而非文本——因此 `nitter search "..." | nitter download` 无需任何 flag 即可
直连（信封的 `data.url` 供下载命令的 stdin 信封模式使用）。显式 flag 永远
优先；TTY 下默认仍是人类表格——交互用户零变化。管道下的空结果完全静默
（stdout 与 stderr 都不打印 `(empty)` 提示）。`watch` 在管道下保持文本
默认（信封流请传 `--ndjson`）；`config`、`seen`、`update` 不变。

推文信封示例（示意；`data` 为 SDK 的 `Tweet` 模型）：

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

`meta.instance` 记录抓取的来源：快车道应答时为 `"FxTwitter"`，否则是应答
实例的 URL。`meta.source` 标注操作及其输入（`user:NASA`、`search:#AI`、
`following:NASA`、`comments:<id>`、`quotes:<id>`、`trends`、`circle:<name>` 等）。

就地错误信封（当前由 `watch` 对每个失败源输出）：

```json
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"chooser: upstream_unavailable: no instances configured"},"meta":{"input":"user:NASA"}}
```

**退出码——先看退出码，再解析 JSON。** `--json`/`--ndjson` 只描述成功输出；
**stderr 永远不是 JSON**。

- `0` — 成功（含空结果；消费端关闭 stdout 管道、`watch` 收到 SIGINT/SIGTERM
  优雅关闭，同样退出 0）；
- `1` — 运行时失败（所有实例都失败；`watch --once` 有至少一个源失败；
  `seen.json` 损坏；配置文件读取/解析失败）；
- `2` — 用法错误（flag 或参数不合法、输入契约违规、`--json --ndjson` 同时给出、
  不带 `--once` 的 `watch --json`、配置值不合法；未知**子命令**名退出 1）。

## watch 语义（自动化前必读）

- **首跑只记不推**：未初始化源的第一轮只建立去重状态、**不输出任何内容**——
  历史不入推。需要历史时对该次运行显式传 `--include-existing`（该轮同时不受
  `--max-new` 限制）。
- **状态规模**：每源最多 300 条已见推文 ID，外加最近 20 个首页纯数字 status ID
  组成的扫描水位。用 `nitter seen list` 检视，用
  `nitter seen clear [--source user:NASA] --confirm` 删除。
- **`--max-new`（默认 10）**限制每源每轮的输出条数（从新到旧）。默认
  （`--max-new-overflow drop`）下，超出上限的新推文会被**立即标记为已见、之后
  绝不补推**：停机恢复后，若单源单轮的新推文超过上限，超出部分会被静默跳过。
  调度器部署应显式设置 `--max-new`，覆盖你各源停机期的突发量；也可以传
  `--max-new-overflow keep`，让超出部分保持未标记，由后续各轮在同一上限下
  重新推送（宁重勿丢；突发量超过两倍上限时需多轮才能排空）。
  `--max-new 0` 不输出任何内容并按当前首页重建基准（两种策略下均如此）。
- **tag 源写原始查询**：`tag:#AI`、`tag:from:nasa`。第一个冒号后的 ref 原样传给
  抓取层（URL 转义由 HTTP 层做且只做一次）。预转义形式 `tag:%23AI` 会被二次转义，
  **不是合法写法**。
- **单源失败不中断整轮**：失败源收到就地错误报告（`--ndjson` 下为错误信封），
  其状态保持不动，其余源继续。`watch --once` 只要有源失败即退出 1，全部成功
  才退出 0。`watch --once --json` 为该轮打印一个文档
  `{"tweets":[...],"errors":[{ref,code,message}...]}`。
- **stdout 管道被关闭按优雅退出 0 处理**（消费端挂断，例如 `head`）。已交付的
  推文之后照常落盘；若落盘本身被截断，下一轮会重推同一批推文（宁重勿丢）。
  Windows 上管道断裂可能以不同 errno（`ERROR_BROKEN_PIPE`）出现，EPIPE → 0 的
  判定在 Windows 上是尽力而为。
- **`seen` 跟随 `--state-dir`**：`nitter seen list/clear` 作用于默认的
  `~/.nitter-cli/state/seen.json`，或传 `--state-dir <dir>` 时作用于
  `<dir>/seen.json`——与 `watch --state-dir` 使用同一个目录。

## 常见问题

**为什么 watch 第一次运行什么都不推？**
设计如此：第一轮先建立去重基线，让流从「现在」开始。确实要历史时，用
`--include-existing` 跑一次。

**为什么一轮突发新推文只到了一部分？**
被 `--max-new`（默认 10）截断；默认溢出策略（`drop`）下其余已标记为已见、
不再补推（见 watch 语义）。调高 `--max-new`、缩短轮询间隔，或传
`--max-new-overflow keep`，让后续各轮重新推送积压部分。

**RSS 正常但 `search` 没有结果。**
搜索是 Nitter 的独立能力，你的实例可能未启用或较慢；用
`nitter instances test --full` 探测。同时检查查询写法：tag 查询必须写原始形式
（watch 里 `tag:#AI`，search 里 `nitter search "#AI"`）——`%23` 会被二次转义。

**`nitter list 12345` 结果为空——是坏了吗？**
空结果可能是 List 本身为空，也可能是新建的 List 尚未被你的实例收录；二者从
外部无法区分。都不算错误。

**数据都存在哪里？**
`~/.nitter-cli/config.toml`（配置）、`~/.nitter-cli/circles.toml`（创作者圈子
花名册）和 `~/.nitter-cli/state/seen.json`（watch 去重状态）。Windows 上都在
用户主目录下（`nitter config path` 打印确切位置）。写入全部原子化；状态文件
损坏是硬错误（退出 1），绝不静默重置。

**默认的 `mix` 模式会把什么发到哪里？**
`user`、`search`、`get`、`comments`、`following`、`profile`、`quotes`、
`trends` 与 `search --type user` 会优先尝试 FxTwitter 的公共端点
（`api.fxtwitter.com`）——handle 或查询词会发送给这个第三方服务，不会附带你
的任何凭证；失败时回退到你自己配置的实例。`list` 始终走你的实例（FxTwitter
没有 List 端点），单次 `--instance URL` 会强制该命令走 Nitter 路径，而
`fetch_backend = "nitter"` 则让所有抓取都留在你自控的基础设施上。

**只想对一条命令换实例怎么办？**
`nitter --instance http://nitter.internal:8080 user NASA` 只在本次调用中替换
整个实例集——注意该覆盖不携带 basic auth 凭证。`--proxy` 同理（flag > 配置 >
环境变量）。

**能拿到媒体链接吗？**
能——`Tweet` 模型的 `media` 字段（`--json`/`--ndjson` 可见）携带实例返回的
图片/视频直链。`nitter download` 直接落盘，`nitter media` 只解析链接不下载。

## 免责声明

1. **仅限公开推文。** nitter-cli 只获取公开可访问的推文——经由公共 FxTwitter
   API（默认 `mix` / `fx` 后端）以及你自己运行的 Nitter 实例（`nitter` 后端、
   回退路径与所有 `list` 抓取）。它不内置实例、不登录、不提供任何绕过访问控制
   或解算挑战的能力。
2. **只配置你自主控制且信任的实例。** CLI 会跟随实例返回的媒体与重定向 URL。
   请把内部服务（Redis、云 metadata、管理后台）与 nitter-cli 及其实例的网络
   路径隔离。
3. **不绕过访问控制。** 页面需要登录、出现挑战或实例限流时，CLI 只上报分类后的
   错误，不做绕行。请遵守 X 的服务条款与实例承载能力；本项目与 X Corp. 及
   Nitter 项目均无关联。

## 许可证

[MIT](LICENSE)。
