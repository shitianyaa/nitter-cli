# nitter CLI 参考

[文档导航](../index.zh-CN.md) · [English](../en/cli-reference.md)

本文是 `nitter` 二进制的公开命令契约。自动化某条命令前先运行
`nitter <command> --help`；当前安装二进制的帮助文本才是其接受 flag 的真源。

## 全局选项

所有命令都接受以下持久选项：

| 选项 | 含义 |
| --- | --- |
| `--proxy URL` | 本次调用的代理（`http`、`https`、`socks5`、`socks5h`）。优先级：flag > 配置 `proxy` > 环境变量（配置为空时走 `HTTPS_PROXY`/`ALL_PROXY`）。 |
| `--instance URL` | 本次调用的 Nitter 实例地址。它会**用这一个 URL 替换整个已配置的实例集**。代理协议或实例 URL 不合法会在任何动作开始前以用法错误退出（退出码 2）。 |

`nitter --version` 输出 `nitter version <版本号>`；裸调用 `nitter` 显示帮助。
未知子命令退出 1（不是 2）。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功——包括空结果、消费端关闭 stdout 管道（EPIPE；Windows 上为尽力而为的检测）、`watch` 收到 SIGINT/SIGTERM 优雅关闭。 |
| `1` | 运行时失败——抓取失败（所有实例都失败）、`watch --once` 有至少一个源失败、状态文件损坏、配置文件读取/解析失败、未知子命令。 |
| `2` | 用法错误——flag/参数不合法、输入契约违规、`--json` 与 `--ndjson` 同给、`watch --json`、配置值不合法。SDK/网络错误绝不会被归类为用法错误。 |

## 输出模式

所有数据命令用同一套规则解析输出模式：`--ndjson` 或 `--json` 优先；不带 flag
时 TTY 得到人类渲染、管道得到相同的 text 渲染（二者都是同一套制表符行，无
ANSI 颜色）。

- **人类 / text**：每条记录一行；空结果在 stdout 不打印任何内容、在 **stderr**
  打印 `(empty)` 提示。推文列表的行形态：
  `<ID>\t<YYYY-MM-DD HH:MM>\t@<handle>\t<单行文本>`——日期为 UTC、精确到分钟，
  文本压平成一行（tab 变空格；其余控制字符会让整个文本单元格切换为带引号的
  转义渲染）。
- **`--json`**：恰好一条记录时是单个 JSON 对象，否则是 JSON 数组，空结果是
  字面量 `[]`。
- **`--ndjson`**：每条记录一个 `nitter.pipeline/v1` 信封：

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800}],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

`meta` 携带溯源信息：`source`（命令输入，形如 `kind:ref` 的键）、`instance`
（产出这批结果的实例 base URL）、`fetched_at`（RFC3339 UTC）。`meta` 的空字段
会被省略。`kind` 枚举在 v1 内只增不改；当前实际输出的 kind 为 `tweet`（数据
命令）、`instance_report`（`instances test --ndjson`）、`media`
（`media --ndjson`）与 `error`（`watch` 的逐源抓取失败、`media` 的逐 REF
失败）：

```json
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"chooser: upstream_unavailable: no instances configured"},"meta":{"input":"user:NASA"}}
```

错误信封的 `data` 为 `{command, stage, code, message}`；`code` 是本次失败的
SDK 错误 Kind（`rate_limited`、`upstream_unavailable`、`challenge_required`、
`not_found`、`malformed_upstream_response` 等），失败不属于任何已分类的 SDK
错误时为 `error`——不要解析 `message` 来做分类。`meta.input` 指明错误对应的
输入。错误消息遵守 SDK 脱敏契约：绝不携带凭证、URL 查询串、请求头或响应体。

**先看退出码，再解析 JSON。** `--json`/`--ndjson` 只描述成功输出；stderr 永远
不是 JSON。

## user / search / list / get 的共同抓取行为

- 按配置顺序轮换实例；抓取失败的实例（429、网络错误）进入冷却（配置
  `instance_cooldown`，默认 60s），期间改试下一个。全部实例失败是运行时失败
  （退出码 1）。
- `--limit` 限制推文条数（`0` = 全部）；不传时应用配置 `default_limit`
  （默认 20）。flag 传负数是用法错误。
- `--max-pages` 限制分页；不传时应用配置 `max_pages`（默认 5）。**`--max-pages
  0` 表示「用默认值」，不是「不限」**；负数是用法错误。
- 重试：`retry_attempts`（默认 2）次额外尝试，线性退避 `retry_delay`
  （默认 1s）；429 且带合法 `Retry-After` 时等待一次、重试一次。

## nitter user

```bash
nitter user <HANDLE> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--media-type image|video|gif] [--json|--ndjson]
```

抓取 `HANDLE` 的时间线——1–15 个字母、数字或下划线，不带 `@`（形状不对时在任何
网络动作前退出 2）。先尝试 RSS 源（`<HANDLE>/rss`）；失败或空结果时改为抓取
HTML 用户页，并跟随其 load-more 游标翻页。NDJSON 的 `meta.source` 为
`user:<HANDLE>`。

字段过滤在**抓取之后、输出之前**应用（三者可自由组合；`--media-type` 值不合法
退出 2）：

- `--no-reposts` 丢弃纯转推（转推标记只存在于 HTML 解析路径；在用户时间线上
  RSS 路径还会按作者不一致识别转推——被转推条目链接的是原作者，绝不会是请求的
  句柄。search/list 没有 RSS 层，该信号不适用于它们）。
- `--media-only` 丢弃不带任何媒体附件的推文。
- `--media-type image|video|gif` 只保留携带至少一个该类型媒体条目的推文。

## nitter search

```bash
nitter search <QUERY> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--media-type image|video|gif] [--json|--ndjson]
```

对配置的实例运行 `QUERY`。查询串原样传给 Nitter（仅由 HTTP 层做一次 URL 转义），
适用 Nitter 自身的查询语法：前导 `#` 搜话题标签，`from:user` 搜某用户的帖子，
其余按普通短语搜索。纯空白查询退出 2。NDJSON 的 `meta.source` 为
`search:<按原样输入的查询>`。

字段过滤与 `user` 一致：`--no-reposts`、`--media-only`、
`--media-type image|video|gif`（抓取之后、输出之前应用）。

## nitter list

```bash
nitter list <LIST_ID> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--media-type image|video|gif] [--json|--ndjson]
```

抓取 List `LIST_ID` 的时间线——List 的数字 ID（或实例接受的 ref），非空且不含
空白、`?`、`#`、`/`（否则退出 2）。它按 `/i/lists/<LIST_ID>` 路径原样传递。空
结果可能意味着 List 本身为空，也可能是新建 List 尚未被该实例收录——二者从外部
无法区分；都不算错误。NDJSON 的 `meta.source` 为 `list:<LIST_ID>`。

字段过滤与 `user` 一致：`--no-reposts`、`--media-only`、
`--media-type image|video|gif`（抓取之后、输出之前应用）。

## nitter get

```bash
nitter get <REF> [--json|--ndjson]
```

抓取单条推文。`REF` 是纯数字 status ID，或推文 URL——`x.com`、`twitter.com` 或
任意 Nitter 实例，形状为 `<user>/status/<id>`；user 段可省略（Nitter 直接提供
`/status/<id>` 路由），`/photo/N` 与 `/video/1` 后缀同样接受。不给位置参数且
stdin 非 TTY 时，从 stdin 读一行作为引用；两种方式同时给出是歧义错误（退出 2）。
没有分页 flag。被引用推文（quote）存在时以 `quote` 字段摘要呈现（`--json`/
`--ndjson` 可见）；互动数不予报告——绝不虚构。NDJSON 的 `meta.source` 为
`status:<数字 ID>`。

示例（`--json` 对单条推文只输出一个对象；形态为示意）：

```json
{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}
```

## nitter media

```bash
nitter media <REF>... [--strategy auto|fx|vx|syndication|nitter|xdown] \
  [--quality high|medium|low] [--probe] [--json|--ndjson]
```

把每条 status REF 解析成可直接下载的媒体直链——视频 mp4 变体、图片原图、
GIF。`REF` 的形态与 `nitter get` 相同（纯数字 ID，或 x.com / twitter.com /
任意 Nitter 实例的推文 URL；接受 `/photo/N` 与 `/video/1` 后缀）。多个 REF
按批次运行；不给位置参数且 stdin 非 TTY 时，从 stdin 读取引用（每行一个，
空行忽略）；位置参数与 stdin 同时给出是歧义错误（退出 2）。下载动作本身由
调用方完成——本命令只解析直链，不抓取媒体。

**策略**（`--strategy`，默认 `auto`）：`auto` 按链路 fx → vx → syndication
→ nitter → xdown 依次尝试，返回**第一个**产出媒体的策略（`source` 标明胜出
者）；显式指定名称则只运行该策略。payload 能解析但不含媒体的策略会被跳过、
继续下一个；所有策略都为空时按 `not_found` 错误解析——「推文没有媒体」是
正常的分类结果，不是崩溃。

**信任边界**：fx、vx、syndication、xdown 是**第三方公共服务**——解析请求会
把推文 URL 发送给它们，因此只解析你愿意分享的公开推文；它们的失败会以真实
策略名上报，绝不静默换成其他来源的成功结果。`nitter` 则从**你自己的**配置
实例读取 status 页（`[[instances]]` 的第一个条目，或 `--instance`）；未配置
实例时以明确的 local-state 错误失败。Nitter 服务的链接按原样保留——包括
纯 http 的局域网实例——且不携带变体元数据。

**`--quality high|medium|low`**（默认 `high`）：图片走 pbs 档位重写
（`name=orig|large|small`）；视频/GIF 由档位决定哪个变体成为主 URL
（high = 最高码率，medium = 非零码率的上中位，low = 最低非零），全部变体
仍保留在 `variants` 列表中。与参考插件不同，CLI 返回一条推文的**全部**媒体
条目——不会在有视频时跳过图片，由消费方自行取舍。

**`--probe`** 对每个 video/gif URL 追加一次 Range 请求（图片永不探测）：
时长来自 mp4 movie header，大小来自 Content-Range total。探测是尽力而为的
——任何失败都让取值留空、绝不导致运行失败；来源已带有时长的条目不会被覆盖。
每条视频条目多一次请求成本。

人类 / text 输出为每条媒体一行制表符行：

```text
https://x.com/NASA/status/2081668333762687236	fx	video	https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4	-	3.2	24000
```

列为 `ref source kind url label duration size`——ref 原样回显输入引用；尾部
缺省单元格为 `-`。`--json` 在整个调用恰好解析出一条媒体时输出单个 JSON
对象，否则为数组，没有结果时为 `[]`。`--ndjson` 按引用顺序输出：每条媒体
一个信封（`kind` `media`，下载 URL 作为 `id`，MediaResolution 作为 `data`，
`meta.input` = 原始 ref），每个失败的 REF 一个 `kind:"error"` 信封：

```json
{"schema":"nitter.pipeline/v1","kind":"media","id":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","data":{"ref":"https://x.com/NASA/status/2081668333762687236","source":"fx","kind":"video","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","variants":[{"url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","bitrate":2176000}]},"meta":{"input":"https://x.com/NASA/status/2081668333762687236"}}
```

退出码：成功为 0（探测失败不计）；单个 REF 解析失败会得到就地错误报告
（NDJSON 流上为 error 信封，其他模式为 stderr 的 `error: <ref>: <message>`），
其余 REF 继续运行；至少一个 REF 失败时以 `media completed with N of M refs
failed` 摘要退出 1；用法问题（`--json` 与 `--ndjson` 同给、`--strategy`/
`--quality` 不合法、引用缺失或不合法、位置参数与 stdin 同时给出）退出 2。

## nitter instances test

```bash
nitter instances test [URL] [--full] [--list-id ID] [--user HANDLE] [--json|--ndjson]
```

探测实例能力，每个实例一行输出：

```text
url	rss	user_html	search	list	latency
http://nitter.internal:8080	ok	ok	fail(404)	-	212ms
```

单元格为 `ok`、`fail(<原因>)`（HTTP 状态码，或 `timeout`、`not rss` 之类的简短
原因），未执行的探测为 `-`。latency 是 RSS 探测的往返耗时，按人类精度渲染。

- 不带 URL 时按顺序探测 `[[instances]]` 的全部实例；带 URL 时只探测该实例。
  既无已配置实例又无 URL：退出 2。
- RSS 与 user 探测抓取 `<user>/rss` 和 `<user>`；`--user` 覆盖账号（默认
  `NASA`）。`--full` 追加搜索探测；`--list-id ID` 追加 List 探测
  （`/i/lists/<id>`；给出时不得为空）。两者默认关闭——每个额外探测都要实例付
  一次请求成本。
- 探测使用与真实抓取相同的传输与设置（重试、节奏、代理）。
- **只要探测完成就退出 0，即使全部探测失败**——报告本身就是产物。退出 2 表示
  输入不合法（空 `--list-id`、`--json --ndjson`、URL/代理不合法）；退出 1 表示
  wiring/传输构建失败。
- `--json` 对单个实例输出一个 JSON 对象，多实例输出数组。`--ndjson` 每实例输出
  一个信封（`kind` 为 `instance_report`，实例 URL 作为 `id`，报告作为 `data`，
  无 `meta`）。

## nitter config

```bash
nitter config path
nitter config get [KEY]
nitter config set KEY [VALUE]
nitter config unset KEY
```

管理 `~/.nitter-cli/config.toml` 的十个标量键（默认值、环境变量覆盖与数组表见
[README](../README.zh-CN.md#配置)）：

```text
default_limit, max_pages, request_interval, retry_attempts, retry_delay,
instance_cooldown, proxy, log_level, log_format, download_path
```

- `config path` 打印配置文件路径。不接受参数（否则退出 2）。
- `config get` 不带键时按 `key = value` 打印全部十个键；带键时只打印该键。
  未知键在读取文件之前即被拒绝（退出 2）。
- `config set KEY [VALUE]` 在**任何磁盘写入之前**校验并转型（`default_limit`/
  `max_pages`/`retry_attempts` 为 `>= 0` 的整数；
  `request_interval`/`retry_delay`/`instance_cooldown` 为 `>= 0` 的时长；
  `log_level` 取 `debug|info`；`log_format` 取 `text|json`；`proxy` 与
  `download_path` 接受任意字符串）。不给 VALUE 时从管道 stdin 读一行（敏感值
  不该进 argv）；TTY 下既无 VALUE 也不可读 stdin 是用法错误。未知键被拒绝，
  并提示 `[[instances]]`/`[[watch.sources]]` 需直接编辑文件。
- `config unset KEY` 删除该键，使其回落到环境变量/默认值。
- `download_path`（默认 `./nitter-media`，相对当前工作目录）是 `nitter
  download` 写入媒体的位置。它没有环境变量覆盖；`nitter download --output
  DIR` 在每次调用时覆盖它，目录在下载时创建——`config set` 不做存在性检查。
- 写入保留未知键与数组表，且为原子写（临时文件落盘，权限 0600）。**config.toml
  中的注释不保证在 `config set`/`config unset` 后保留。**
- 全新安装时，第一条真实命令（`--help`/`-h`、`--version`、`help` 子命令以及
  `config set`/`config unset` 之外的任何命令）会发布一份自带注释的基线配置；
  `config set`/`config unset` 在校验通过后自行播种。任何操作都不会覆盖已有
  配置。

退出码：成功 0；键/值/参数个数不合法 2。配置文件无法读取或解析（非法 TOML）
以退出码 1 失败；值未通过 schema 校验（如非法时长）是用法错误，退出 2。

## nitter watch

```bash
nitter watch [SOURCE...] [--once] [--interval D] [--max-new N] \
  [--max-pages N] [--include-existing] [--state-dir DIR] [--ndjson] \
  [--no-reposts] [--media-only] [--media-type image|video|gif]
```

按轮询周期抓取各来源，对照持久化去重状态（`~/.nitter-cli/state/seen.json`，或
`<--state-dir>/seen.json`）只输出新推文。

**来源**为任意一组 `user:<handle>`、`tag:<query>`、`list:<id>`，例如
`user:NASA`、`tag:#AI`、`tag:from:nasa`、`list:12345`。第一个冒号后的 ref 原样
传给抓取层，并构成 seen 键 `<kind>:<ref>`——**tag 查询写原始形式（`tag:#AI`）；
URL 预转义形式（`tag:%23AI`）会在网络上被二次转义，不是合法写法。** 不带
SOURCE 参数时使用配置 `[[watch.sources]]`；两者都为空：退出 2。

**flag**

| flag | 默认 | 含义 |
| --- | --- | --- |
| `--once` | 关 | 只跑一轮即退出——推荐的调度器形态。 |
| `--interval D` | `10m` | 不带 `--once` 时轮次间的睡眠时长；必须是 `>= 1s` 的 duration（两种模式下都会校验）。 |
| `--max-new N` | `10` | 每源每轮最多输出的新推文数（从新到旧）。`0` 不输出任何内容，并把当前首页封存为新基准；负数是用法错误。 |
| `--max-pages N` | 配置 `max_pages`（5） | 每轮抓取页数预算；`0` = 用默认值。 |
| `--include-existing` | 关 | 未初始化源的首轮输出整个首抓结果（默认：首跑只记录状态）。 |
| `--state-dir DIR` | `~/.nitter-cli/state` | 存放 `seen.json` 的目录（不存在则创建）。 |
| `--ndjson` | 关 | 每条记录一个信封：`kind` 为 `tweet` 与 `error`。 |
| `--json` | — | **不支持**：watch 是推文与错误混合的流，不是单个 JSON 文档；恒为用法错误。 |
| `--no-reposts` | 关 | 在**去重之前**丢弃纯转推（转推标记只存在于 HTML 解析路径）。 |
| `--media-only` | 关 | 在**去重之前**丢弃不带媒体附件的推文。 |
| `--media-type image\|video\|gif` | — | 在**去重之前**只保留携带至少一个该类型媒体条目的推文；其他值是用法错误。 |

全局 `--proxy`/`--instance` 与其他命令一致。

**首跑与输出规则**

- 未初始化源的第一轮只**记录**状态——不输出任何历史（只记不推）。
  `--include-existing` 对该次运行解除此限制，且该轮首抓不受 `--max-new` 限制。
- 之后的每轮输出各源的新推文，每源每轮最多 `--max-new` 条。**超出上限的新推文
  会被立即标记为已见、之后绝不补推**：停机恢复后，单源单轮突发超过上限的部分
  会被静默跳过——调度器部署应显式设置 `--max-new`。
- 已初始化源的抓取成功但结果为空时，整组保留旧状态（不封存任何东西）。
- **字段过滤在去重之前运行**：被 `--no-reposts`、`--media-only` 或
  `--media-type` 过滤掉的推文不会被记为已见——每轮都会重新抓取、重新过滤但不
  输出，因此过滤不会让状态增长，也不会重复推送旧推文。`--max-new` 只统计通过
  过滤的推文（上限作用于消费端实际收到的内容），水位锚定过滤后的首页。
- 推文先产出、状态后落盘（先产出后落盘）：交付或落盘失败时保留旧状态，下一轮
  重推（宁重勿丢）。

**失败与退出码**

- 抓取失败的源收到就地错误报告（`--ndjson` 流上是错误信封；其他模式是 stderr 的
  `error: <key>: <message>` 行），其余源继续；该源状态保持不动。`--once` 只要有
  源失败即退出 1，全部成功退出 0。用法问题退出 2（来源字符串不合法、来源集为
  空、`--interval < 1s`、flag 为负、`--json`）。
- 不带 `--once` 时命令持续循环，直到 SIGINT/SIGTERM（优雅退出 0）或不可恢复
  错误（状态存储失败、非 EPIPE 的 stdout 写失败 → 退出 1）。
- stdout 管道被关闭（EPIPE）视为消费端挂断，两种模式下都退出 0。Windows 上该
  检测是尽力而为（管道断裂可能以 `ERROR_BROKEN_PIPE` 呈现）。

**状态**按源存储：最多 300 条已见 ID（从新到旧），外加最近 20 个首页纯数字 ID
作为扫描水位。用 `nitter seen list` 检视；注意 `seen` 没有 `--state-dir`——
如果你用 `watch --state-dir <dir>`，请直接读取该目录下的 `seen.json`。

示例——调度器消费 NDJSON 流（示意）：

```bash
nitter watch user:NASA tag:#AI --once --ndjson --max-new 50
```

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2081668333762687236","data":{…},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"…"},"meta":{"input":"tag:#AI"}}
```

## nitter seen

```bash
nitter seen list [--source SOURCE] [--json]
nitter seen clear [--source SOURCE] --confirm
```

检视与清除 **默认位置** `~/.nitter-cli/state/seen.json` 的 watch 去重状态——
`seen` 没有 `--state-dir`。

- `seen list` 按键排序，每源一行制表符分隔：

  ```text
  user:NASA	initialized=true	seen=142	watermark=20	2026-09-12T08:00:00Z
  ```

  `updated_at` 为 RFC3339 UTC，无时间戳时为 `-`。`--source` 收窄到单个源，解析
  方式与 watch 来源完全一致（`user:NASA`、`tag:#AI`、`list:12345`；过滤不到任何
  条目与空库是同一种空清单）。`--json` 输出
  `{source, initialized, seen_count, watermark_count, updated_at}` 的 JSON 数组
  ——即使只有单源也恒为数组。空库在 stderr 打印 `(empty)`（stdout 无输出）；带
  `--json` 时在 stdout 打印 `[]`。以上情况均退出 0；`--source` 不合法退出 2。
- `seen clear` 带 `--source` 删除该源的条目，不带则删除全部。**每次都需要
  `--confirm`**——状态变更需显式授权；缺 `--confirm` 退出 2。清除不存在的源
  幂等成功，stderr 提示 `not found`。成功时不打印任何内容（退出码就是信号）。
  文件的 schema 版本保留；写入原子化。
- 状态文件损坏是硬错误（退出 1）——存储层绝不静默重置状态，因为静默重置会把
  整个 watch 历史重新推送一遍。

## nitter update

```bash
nitter update
nitter update --check [--prerelease] [--json]
```

报告二进制的更新方式。**MVP 不做自替换安装**——不带 `--check` 的形式只打印
「请用包管理器重装 / 手动下载」的指引，退出 0。

- `--check` 经 GitHub Releases API 将当前版本与
  `github.com/shitianyaa/nitter-cli` 的最新发布版比较。草稿版恒被排除；
  `--prerelease` 允许预发布版参与"最新版"遴选。遴选按严格 semver 优先级
  （`v` 前缀可选、忽略构建元数据、遵循 semver 预发布排序）在 API 首页内
  进行，而非按发布时间。所有成功的检查均退出 0——过时是报告结果而非失败：
  `update available: <version> (<release URL>)` 或 `up to date`。当前版本
  比最新发布版还新（例如装了预发布版）视为最新。
- `--json`（仅可与 `--check` 同用）打印一个键齐全的 JSON 文档：
  `{"current":"0.1.0","latest":"0.2.0","outdated":true,"prerelease":false,"release_url":"…"}`。
  开发构建输出：`{"current":"dev","development_build":true}`。
- 开发构建（编译时未注入版本元数据）完全跳过检查：没有可比较的发布版。
- 检查失败——网络错误、GitHub 错误（仅报 HTTP 状态码；绝不回显响应体）、
  无可用发布版——是运行时失败（退出 1）。
- `--json` 与 `--prerelease` 仅可与 `--check` 同用（否则用法错误，退出 2）。
