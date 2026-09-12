# twitter-cli

[English](README.md) · [简体中文](README.zh-CN.md) · [文档导航](docs/index.zh-CN.md)

`twitter` 是一个非官方的**公开推文**命令行客户端，数据来自**你自己部署的 Nitter
实例**。它可以抓取用户时间线、搜索结果、List 时间线和单条推文，也能持续监视
多个来源、对照持久化去重状态只推送真正的新推文——为调度器（cron、systemd
timer、Hermes）驱动、以 NDJSON 消费而设计。

它同时是一个公开 Go SDK（`github.com/shitianyaa/twitter-cli/sdk`，package
`twitter`），数据模型稳定、只增不改。

## 能做什么

- **抓取公开推文**（经你自己的 Nitter 实例）：`user`（RSS 优先，失败或空结果时
  回退 HTML 用户页）、`search`、`list`，以及用 `get` 抓单条推文。
- **持续监视来源**：`watch` 轮询 `user:`/`tag:`/`list:` 源，对照
  `~/.twitter-cli/state/seen.json` 去重，只输出新推文。`--once` 单轮即退——
  这是推荐的调度器形态。
- **诊断实例**：`instances test` 逐实例探测 RSS / 用户时间线 / 搜索 / List
  能力，每个实例一行报告。
- **三种输出模式**（所有数据命令统一）：人类可读的制表符行、整结果 `--json`、
  逐记录 `--ndjson`（`twitter.pipeline/v1` 信封）。
- **管理自身配置与状态**：`config path/get/set/unset` 管理九个标量键，
  `seen list/clear` 管理 watch 去重状态。
- **实例轮换与冷却**：按配置顺序轮换实例；发生 429/网络错误的实例进入冷却
  （默认 60s），期间改试下一个。不做成功率加权——配置顺序就是策略。

## 快速上手

twitter-cli 不内置任何实例：**请指向你自己控制的 Nitter 实例**。未配置实例前
不会发起任何抓取。

1. **构建**（Go 1.27+）：

   ```bash
   sh scripts/build.sh          # 生成 ./twitter
   ./twitter --version          # twitter version 0.1.0（或 dev 版本行）
   ```

2. **配置实例**——编辑 `~/.twitter-cli/config.toml`（路径可用 `twitter config
   path` 查看；首次真实命令会生成带注释示例的基线文件）：

   ```toml
   [[instances]]
   url = "http://nitter.internal:8080"   # <your-instance>：你自己的 Nitter 地址
   # username = ""                       # 可选 basic-auth 凭证
   # password = ""                       # （MVP 传输层暂未使用）
   ```

3. **测试实例**（只探测能力，不抓你的数据）：

   ```bash
   twitter instances test                # 探测全部 [[instances]]
   twitter instances test http://nitter.internal:8080 --full
   ```

   输出示例（单元格为 `ok`、`fail(<原因>)` 或 `-`）：

   ```text
   url	rss	user_html	search	list	latency
   http://nitter.internal:8080	ok	ok	ok	-	212ms
   ```

4. **抓点数据**（示例——实际输出取决于你的实例与数据）：

   ```bash
   twitter user NASA --limit 5
   twitter search "#nitter" --limit 10
   twitter get https://x.com/NASA/status/2081668333762687236
   ```

5. **交给调度器监视**——每次调用单轮，只出新推文，状态跨次运行持久保存：

   ```bash
   # crontab：每 10 分钟一轮，NDJSON 流写入你的消费端
   */10 * * * * twitter watch user:NASA tag:#AI --once --ndjson >> /var/log/twitter-watch.ndjson 2>/tmp/twitter-watch.err
   ```

   来源也可以写在配置的 `[[watch.sources]]` 里；不带参数运行
   `twitter watch --once --ndjson` 即使用它们。

## 配置

`~/.twitter-cli/config.toml`（TOML，权限 0600）。优先级：CLI flag > 环境变量 >
文件 > 内置默认。`twitter config get` 查看生效值；`twitter config set KEY VALUE`
写入（值也可从 stdin 管道读入）；`twitter config unset KEY` 删除。`[[instances]]`
与 `[[watch.sources]]` 数组表只能直接编辑文件——`config set` 拒绝它们。

### 标量键（全部九个）

| 键 | 类型 | 默认 | 环境变量覆盖 | 含义 |
| --- | --- | --- | --- | --- |
| `default_limit` | int | `20` | `TWITTER_DEFAULT_LIMIT` | 手动命令未传 `--limit` 时的条数（`0` = 全部） |
| `max_pages` | int | `5` | — | 单次抓取分页上限（命令上传 `--max-pages 0` 也表示这个默认，而非「不限」） |
| `request_interval` | duration | `1s` | — | 相邻请求开始时间的全局最小间隔 |
| `retry_attempts` | int | `2` | — | 首次之外的重试次数（网络错误与 5xx） |
| `retry_delay` | duration | `1s` | — | 线性退避基数：第 n 次重试等待 `retry_delay × n` |
| `instance_cooldown` | duration | `60s` | — | 实例失败（429 / 网络错误）后的冷却时长 |
| `proxy` | string | `""` | — | 代理 URL（`http(s)`、`socks5(h)`）；空 = 走环境代理（`HTTPS_PROXY`/`ALL_PROXY`） |
| `log_level` | 枚举 | `info` | `TWITTER_LOG_LEVEL` | `debug` 或 `info`；诊断只进 stderr，不污染 stdout |
| `log_format` | 枚举 | `text` | `TWITTER_LOG_FORMAT` | `text` 或 `json`（单行） |

环境变量只覆盖这三个键，且优先于文件：`TWITTER_DEFAULT_LIMIT`（整数）、
`TWITTER_LOG_LEVEL`、`TWITTER_LOG_FORMAT`。

### 数组表

```toml
[[instances]]
url = "http://nitter.internal:8080"   # 必填
username = ""                         # 可选 basic auth（见下注）
password = ""

[[watch.sources]]                     # `twitter watch` 无参数时的默认来源
id = "user:NASA"                      # user:<handle> | tag:<query> | list:<id>
```

注意：实例凭证会随配置携带，但 MVP 传输层尚未接入——探测与抓取均以未认证方式
进行。请改为在网络层为实例加访问控制。

## 输出模式

所有数据命令（`user`、`search`、`list`、`get`、`instances test`、`seen list`、
`watch`）用同一套规则解析输出模式：

| 模式 | 触发方式 | 形态 |
| --- | --- | --- |
| 人类 / text | 默认（TTY 与管道下渲染相同） | 每条记录一行制表符分隔；空结果在 **stderr** 打印 `(empty)` |
| JSON | `--json` | 恰好一条记录时是单个 JSON 对象，否则是 JSON 数组，空结果是 `[]` |
| NDJSON | `--ndjson` | 每条记录一个 `twitter.pipeline/v1` 信封，一行一条 |

推文信封示例（示意；`data` 为 SDK 的 `Tweet` 模型）：

```json
{"schema":"twitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

就地错误信封（当前由 `watch` 对每个失败源输出）：

```json
{"schema":"twitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"user","message":"chooser: upstream_unavailable: no instances configured"},"meta":{"input":"user:NASA"}}
```

**退出码——先看退出码，再解析 JSON。** `--json`/`--ndjson` 只描述成功输出；
**stderr 永远不是 JSON**。

- `0` — 成功（含空结果；消费端关闭 stdout 管道、`watch` 收到 SIGINT/SIGTERM
  优雅关闭，同样退出 0）；
- `1` — 运行时失败（所有实例都失败；`watch --once` 有至少一个源失败；
  `seen.json` 损坏；配置文件读取/解析失败）；
- `2` — 用法错误（flag 或参数不合法、输入契约违规、`--json --ndjson` 同时给出、
  `watch --json`、配置值不合法；未知**子命令**名退出 1）。

## watch 语义（自动化前必读）

- **首跑只记不推**：未初始化源的第一轮只建立去重状态、**不输出任何内容**——
  历史不入推。需要历史时对该次运行显式传 `--include-existing`（该轮同时不受
  `--max-new` 限制）。
- **状态规模**：每源最多 300 条已见推文 ID，外加最近 20 个首页纯数字 status ID
  组成的扫描水位。用 `twitter seen list` 检视，用
  `twitter seen clear [--source user:NASA] --confirm` 删除。
- **`--max-new`（默认 10）**限制每源每轮的输出条数（从新到旧）。超出上限的新推文
  会被**立即标记为已见、之后绝不补推**：停机恢复后，若单源单轮的新推文超过上限，
  超出部分会被静默跳过。调度器部署应显式设置 `--max-new`，覆盖你各源停机期的
  突发量。`--max-new 0` 不输出任何内容并按当前首页重建基准。
- **tag 源写原始查询**：`tag:#AI`、`tag:from:nasa`。第一个冒号后的 ref 原样传给
  抓取层（URL 转义由 HTTP 层做且只做一次）。预转义形式 `tag:%23AI` 会被二次转义，
  **不是合法写法**。
- **单源失败不中断整轮**：失败源收到就地错误报告（`--ndjson` 下为错误信封），
  其状态保持不动，其余源继续。`watch --once` 只要有源失败即退出 1，全部成功
  才退出 0。
- **stdout 管道被关闭按优雅退出 0 处理**（消费端挂断，例如 `head`）。已交付的
  推文之后照常落盘；若落盘本身被截断，下一轮会重推同一批推文（宁重勿丢）。
  Windows 上管道断裂可能以不同 errno（`ERROR_BROKEN_PIPE`）出现，EPIPE → 0 的
  判定在 Windows 上是尽力而为。
- **`seen` 没有 `--state-dir`**：`twitter seen list/clear` 只作用于默认的
  `~/.twitter-cli/state/seen.json`。如果你用 `watch --state-dir <dir>`，请直接
  读取该目录下的 `seen.json`（JSON，schema v1）。

## FAQ

**为什么 watch 第一次运行什么都不推？**
设计如此：第一轮先建立去重基线，让流从「现在」开始。确实要历史时，用
`--include-existing` 跑一次。

**为什么一轮突发新推文只到了一部分？**
被 `--max-new`（默认 10）截断，其余已标记为已见（见 watch 语义）。调高
`--max-new` 或缩短轮询间隔。

**RSS 正常但 `search` 没有结果。**
搜索是 Nitter 的独立能力，你的实例可能未启用或较慢；用
`twitter instances test --full` 探测。同时检查查询写法：tag 查询必须写原始形式
（watch 里 `tag:#AI`，search 里 `twitter search "#AI"`）——`%23` 会被二次转义。

**`twitter list 12345` 结果为空——是坏了吗？**
空结果可能是 List 本身为空，也可能是新建的 List 尚未被你的实例收录；二者从
外部无法区分。都不算错误。

**数据都存在哪里？**
`~/.twitter-cli/config.toml`（配置）和 `~/.twitter-cli/state/seen.json`（watch
去重状态）。Windows 上都在用户主目录下（`twitter config path` 打印确切位置）。
写入全部原子化；状态文件损坏是硬错误（退出 1），绝不静默重置。

**只想对一条命令换实例怎么办？**
`twitter --instance http://nitter.internal:8080 user NASA` 只在本次调用中替换
整个实例集。`--proxy` 同理（flag > 配置 > 环境变量）。

**能拿到媒体链接吗？**
能——`Tweet` 模型的 `media` 字段（`--json`/`--ndjson` 可见）携带实例返回的
图片/视频直链。

## 免责声明

1. **仅限公开推文。** twitter-cli 只经 Nitter 获取公开可访问的推文。它不内置
   实例、不登录、不提供任何绕过访问控制或解算挑战的能力。
2. **只配置你自主控制且信任的实例。** CLI 会跟随实例返回的媒体与重定向 URL。
   请把内部服务（Redis、云 metadata、管理后台）与 twitter-cli 及其实例的网络
   路径隔离。
3. **不绕过访问控制。** 页面需要登录、出现挑战或实例限流时，CLI 只上报分类后的
   错误，不做绕行。请遵守 X 的服务条款与实例承载能力；本项目与 X Corp. 及
   Nitter 项目均无关联。

## 许可证

MIT。
