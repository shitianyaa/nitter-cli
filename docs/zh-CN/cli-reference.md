# nitter CLI 参考

[文档导航](../index.zh-CN.md) · [English](../en/cli-reference.md)

本文是 `nitter` 二进制的公开命令契约。自动化某条命令前先运行
`nitter <command> --help`；当前安装二进制的帮助文本才是其接受 flag 的真源。

## 全局选项

所有命令都接受以下持久选项：

| 选项 | 含义 |
| --- | --- |
| `--proxy URL` | 本次调用的代理（`http`、`https`、`socks5`、`socks5h`）。优先级：flag > 配置 `proxy`。两者都为空时，环境变量 `HTTPS_PROXY`/`ALL_PROXY` 对 FxTwitter 快车道与 `update` 生效，但**不**作用于 nitter 传输——要给实例流量走代理请用 `--proxy` 或配置 `proxy`。 |
| `--instance URL` | 本次调用的 Nitter 实例地址。它会**用这一个 URL 替换整个已配置的实例集**，并把本次调用钉在实例路径上（跳过 Fx 快车道）——这是验证单个实例的手段。它只对**有实例路径的命令**生效（`user`、`search`、`get`、`list`）；六个 fx-only 能力（`comments`、`following`、`profile`、`quotes`、`trends`、`search --type user`）会忽略它。该覆盖**不携带 basic-auth 凭据**，因此需要认证的实例会返回 401。它不是日常开关：路由由 `fetch_backend` 决定，它只覆盖本次调用的实例集。代理协议或实例 URL 不合法会在任何动作开始前以用法错误退出（退出码 2）。 |

`nitter --version` 输出 `nitter version <版本号>`；裸调用 `nitter` 显示帮助。
未知子命令退出 1（不是 2）。

## 数据流向与隐私

`fetch_backend` 决定由哪个服务应答，也决定了什么会离开你的机器。默认 `mix` 下：

- `user`、`search`、`get` 优先尝试 **`api.fxtwitter.com`**（第三方公共服务）：
  handle、查询串或状态 ID 会发往该服务，**不携带你的任何凭证**；失败时回退到
  你自己的实例。
- `comments`、`following`、`profile`、`quotes`、`trends`、`search --type user`
  与 `circle refresh` 没有 Nitter 等价端点，因此无论 `fetch_backend` 怎么设，
  都会访问 `api.fxtwitter.com`。
- `list` 始终运行在你自己的实例上（FxTwitter 没有 List 端点）。

`fx` 去掉回退（只走快车道，失败即报错）；`--instance URL` 把某一次调用钉在单个
实例上，仅对有实例路径的四个命令有效。basic-auth 凭证在传输层内按主机限定：凭证
只会附着在发往其所属实例的请求上，绝不进入错误、日志或响应。`media`/`download`
使用的第三方媒体解析器只会收到媒体 URL，永远收不到你的凭证。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功——包括空结果、消费端关闭 stdout 管道（EPIPE；Windows 上为尽力而为的检测）、`watch` 收到 SIGINT/SIGTERM 优雅关闭。 |
| `1` | 运行时失败——抓取失败（所有实例都失败）、`watch --once` 有至少一个源失败、状态文件损坏、配置文件读取/解析失败、未知子命令。 |
| `2` | 用法错误——flag/参数不合法、输入契约违规、`--json` 与 `--ndjson` 同给、不带 `--once` 的 `watch --json`、配置值不合法。SDK/网络错误绝不会被归类为用法错误。 |

## 输出模式

所有数据命令用同一套规则解析输出模式：`--ndjson` 或 `--json` 优先；不带 flag
时 TTY 得到人类渲染、stdout 非 TTY（管道/重定向）得到 NDJSON——0.6.0 起的
管道默认（`nitter.pipeline/v1` 信封，每条记录一行）。`watch` 在管道下保持
text 渲染；信封流请传 `--ndjson`。

- **人类 / text**：每条记录一行；空结果在 stdout 不打印任何内容、在 **stderr**
  打印 `(empty)` 提示。推文列表的行形态：
  `<ID>\t<YYYY-MM-DD HH:MM>\t@<handle>\t<单行文本>`——日期为 UTC、精确到分钟，
  文本压平成一行（tab 变空格；其余控制字符会让整个文本单元格切换为带引号的
  转义渲染）。
- **`--json`**：恰好一条记录时是单个 JSON 对象，否则是 JSON 数组，空结果是
  字面量 `[]`。
- **`--ndjson`**：每条记录一个 `nitter.pipeline/v1` 信封：

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2102761519985332442","data":{"id":"2102761519985332442","url":"https://x.com/NASA/status/2102761519985332442","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800}],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
```

`meta` 携带溯源信息：`source`（命令输入，形如 `kind:ref` 的键）、`instance`
（产出这批结果的实例 base URL）、`fetched_at`（RFC3339 UTC）。`meta` 的空字段
会被省略。`kind` 枚举在 v1 内只增不改；当前实际输出的 kind 为 `tweet`（数据
命令）、`instance_report`（`instances test --ndjson`）、`media`
（`media --ndjson`）、`download`（`download --ndjson`）与 `error`（`watch`
的逐源抓取失败，`media` 与 `download` 的逐 REF 失败）：

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
- `--limit` 限制推文条数；不传时应用配置 `default_limit`（默认 20）。
- `--max-pages` 限制分页；不传时应用配置 `max_pages`（默认 5）。
- 两者都是**上限**，显式传值**必须 >= 1**：`0` 与负数都是用法错误（退出码 2）。
  没有「不限制」这个取值——要深挖积压就同时给一个大的 `--limit` 与 `--max-pages`。
- 重试：`retry_attempts`（默认 2）次额外尝试，线性退避 `retry_delay`
  （默认 1s）；429 且带合法 `Retry-After` 时等待一次、重试一次。

## nitter user

```bash
nitter user <HANDLE> [--limit N] [--max-pages N] [--no-reposts] [--media-only] \
  [--with-replies] [--media-type image|video|gif] [--json|--ndjson]
```

抓取 `HANDLE` 的时间线——1–15 个字母、数字或下划线，不带 `@`（形状不对时在任何
网络动作前退出 2）。默认 `fetch_backend=mix` 时优先通过 FxTwitter 极速免登通道拉取，
遇到故障时平滑走 Nitter 实例。传 `--instance URL` 会用该 URL 替换本次调用的实例集，并把本次调用
钉在实例路径上、完全跳过快车道。

Nitter RSS 源会按其 `Min-Id` 游标逐页读取，因此积压跨越多页时不会被截断在
第一页：`--limit N` 会一直翻页到凑满 N 条或耗尽 `--max-pages` 预算为止
（不携带 `Min-Id` 的普通 RSS 代理仍是单页）。

- `--with-replies` 包含用户自身发布的回复推文。
- `--no-reposts` 丢弃纯转推。
- `--media-only` 丢弃不带任何媒体附件的推文（Fx 激活时走 `/media` 高速专线）。
- `--media-type image|video|gif` 只保留携带至少一个该类型媒体条目的推文。

## nitter following

```bash
nitter following <HANDLE> [--limit N] [--json|--ndjson]
```

通过 FxTwitter 接口拉取 `HANDLE` 关注的用户列表（扩列与找同好）。
- TTY 默认渲染排版表格：`@<handle>  <name>  <followers>  <bio>`。
- 管道模式（`!isatty`）自动输出 `nitter.pipeline/v1`（`kind: "profile"`）单行 NDJSON 信封。
- `--json` 输出完整 `Profile` 数组。

## nitter comments

```bash
nitter comments <STATUS_ID_OR_URL> [--sort likes|recency] [--limit N] [--json|--ndjson]
```

获取某条推文的主楼、上下文对话链（`thread`）以及评论区回复（`replies`）。
- 典型场景：提取博主首条自评隐藏链接/网盘、追更 1/N 连环长推/漫画串。
- `--sort`：可选 `likes`（默认高赞排序）或 `recency`（最新回复排序）。

## nitter circle

```bash
nitter circle list [--json]
nitter circle show <NAME> [--json] [--min-followers N]
nitter circle refresh <NAME>
nitter circle suggest <HANDLE> [--limit N] [--min-followers N] [--json]
nitter circle add <NAME> <HANDLE>
nitter circle run <NAME> [--limit N] [--media-only] [--media-type image|video|gif] [--json|--ndjson]
```

管理与遍历保存在 `~/.nitter-cli/circles.toml` 的私人精选创作者圈子名单。
- `list`：列出所有圈子名称、描述及博主数。
- `show`：查看指定圈子的成员，并与 `~/.nitter-cli/profiles.toml` 中缓存的侧写合并展示。**纯本地，零网络请求。** 每个成员输出 `@<handle>\t<粉丝数>\t<role>\t<昵称>\t<bio>\t<数据年龄>`（bio 展平为单行并截断到 120 字节）；无数据的列输出 `-`，不会丢行。
  - **`--min-followers N`**：只保留**缓存**粉丝数 ≥ N 的成员。无缓存的成员没有已验证数字，会被该过滤排除，但仍计入 stderr 提示。`N < 0` 是 usage error（退出 2）。
  - 无缓存的成员保留行内 `-` 占位，同时在 stderr 输出 `note: <N> member(s) have no cached profile; run 'nitter circle refresh <NAME>'`（无论是否被 `--min-followers` 过滤掉）。先跑一次 `refresh` 填充缓存。
  - `--json` 按名单顺序输出 `{handle, name, bio, followers_count, fetched_at, role, note}` 对象数组；与 human 行一致，`bio` 会展平为单行并截断到 120 字节。`fetched_at` 为空串表示从未拉取；数据新鲜度由消费方自行派生（`noted_at` 与派生的 age 刻意不投影）。
- `refresh`：拉取每个成员的 profile 并把结果合并进侧写文件 `~/.nitter-cli/profiles.toml`。它是该文件**唯一**的联网写入路径，也是唯一为圈子拉取 profile 的命令。
  - 只写事实字段（`name`、`bio`、`followers_count`、`fetched_at`），**绝不触碰** `role`、`note`、`noted_at`——记在这些字段里的判断在每次刷新中都会存活。
  - 单个成员拉取失败时 stderr 一行 `warning:` 并继续，其已有条目保留原事实（不以空值覆盖，也不会为它新建条目）。全部成员失败退出 1；圈子不存在退出 1；空圈子输出 `(empty)`、退出 0 且不创建文件。
  - 成功时输出 `refreshed N/M members in circle <key>`。从不 prune：已不在圈子中的 handle 其侧写条目原样保留。
  - 侧写文件以 handle 小写为键且由机器管理——重写会丢弃注释与未声明字段，手改请只动 `role`/`note`。
- `suggest`：只读的圈子候选发现，为新建圈子服务；聚合两路现成数据——`HANDLE` 的**关注列表**（静态关系）与其时间线中**被转推的原作者**（行为关系）。输出按同现次数降序（两路都命中的排最前），再按粉丝数降序，再按 handle 字母序（确定性）。
  - `--limit N`（默认 20，**必须 ≥ 1**）：每路取数上限。`--limit 0` 是 usage error（退出 2），与所有命令一致；在这里更是如此——两路对 `0` 的语义相反且都无用（timeline：空；following：服务端单页）。following 路在上游忽略 `limit`/`count`，恒定返回一页约 50–67 个账号，因此由客户端截断；实际条数可能少于 `N`。
  - `--min-followers N`（默认 0）：只筛末尾 `top matches` 小结段，不影响主表与 `--json` 输出。
  - 种子必须存在（先做一次 `profile` 探测，种子不存在退出 1）。单路失败时 stderr 警告并降级，另一路继续产出候选；两路全失败退出 1。仅来自转推的候选若 profile 拉取失败，stderr 警告并跳过。
  - 人类输出：统计行、排序主表（`@<handle>\t<粉丝数>\t<bio 单行>\t<来源>`，来源为 `following`、`retweet` 或 `both`），末尾 `top matches (>= N followers)` 小结。`--json` 输出 `{handle, followers_count, bio, source}` 对象数组。`suggest` 从不写圈子文件——用 `circle add` 落库你选中的 handle。
- `add`：向圈子添加博主（支持自动创建圈子并原子存盘）。
- `run`：按序遍历圈子中所有博主并拉取最新推文流，天然支持管道传输给 `nitter download`。
  - `--limit N`（默认 20，必须 ≥ 1）：每个博主抓取的推文上限。
  - `--media-type image|video|gif` 只保留携带至少一个该类型 media 的推文（非法值为 usage error；语义与 `user` 命令的 `--media-type` 一致）。
  - **快照语义**：每次 run 都从头重新拉取每个成员的最近推文，无增量状态——同圈子同 limit 多次运行可能返回重叠结果集；需要增量追踪新推文用 `watch`。
  - **页数预算**：`run` 没有 `--max-pages` flag；每个成员的抓取使用配置 `max_pages`（默认 5）。需要更深时请改配置（这同时会提高 `watch` 每轮的预算）。
  - **确定性顺序**：Fx 快车道下结果按推文 ID 降序（时间线序）排序后再按 `--limit` 截断，即使上游翻页组成在多次运行间波动，同输入也产生同输出序列。
  - **过滤标注**：`--media-only` 或 `--media-type` 生效时，NDJSON 信封携带 `meta.filter`（`"media_only"` 或媒体类型值），消费者可验证过滤；无过滤时无该字段。
  - **媒体端点翻页**：`--media-only` 走 Fx media 端点，每页数量翻倍以补偿非媒体推文——结果集可能比普通拉取探得更深（文档化行为，非错误）。

## nitter profile

```bash
nitter profile <HANDLE> [--json|--ndjson]
```

获取博主个人名片卡与详细元数据。
- TTY 默认渲染排版名片卡：包含 Handle（有昵称时以括号附上）、Bio、关注数、粉丝数、发推数、媒体数、头像/背景横幅直链及受保护状态。关注数、粉丝数与发推数始终打印；`Bio`、`Media`、`Avatar`、`Banner`、`Protected` 仅在数据源提供时出现。
- `tweets_count` 取数据源的状态数（FxTwitter 的 `statuses` 字段）；数据源不提供时为 `0`——绝不编造。
- 管道模式（`!isatty`）自动输出 `nitter.pipeline/v1`（`kind: "profile"`）单行 NDJSON 信封。
- `--json`：输出单条 Profile JSON 对象。

## nitter quotes

```bash
nitter quotes <REF> [--limit N] [--media-only] [--no-reposts] [--json|--ndjson]
```

挖掘指定推文（ID 或 URL）的引用推文（Quotes，二创及转发点评）。

空结果与上游故障会被区分。该接口对「这条推文确实没有引用」和「真实故障」都返回 404，因此命令会回查推文自身的引用计数：计数为 0 时输出空结果并退出 0，计数为正或不可读则上报 `not_found` 并退出 1。该故障只写 **stderr 并退出 1**——与所有取数失败一样，它**不会**被包成 `kind: "error"` 的 NDJSON 信封，因此 `--ndjson` 的消费方在 stdout 上看不到任何行，必须靠退出码判断。
- TTY 默认渲染推文行：`<ID>  <YYYY-MM-DD HH:MM>  @<handle>  <text>`。
- 管道模式（`!isatty`）自动输出 `kind: "tweet"` 单行 NDJSON 信封，`meta.source` 标记为 `quotes:<id>`。
- `--limit`：限制条数（默认 20，必须 ≥ 1）。
- `--media-only`：仅保留带媒体附件的引用推文。
- `--no-reposts`：过滤纯转推。
- `--json`：输出 JSON 文档。

## nitter trends

```bash
nitter trends [--limit N] [--json|--ndjson]
```

获取实时 Twitter/X 热搜榜单趋势。
- TTY 默认渲染排版表格：`#  TREND TOPIC  CONTEXT  TWEETS`。
- 管道模式（`!isatty`）自动输出 `nitter.pipeline/v1`（`kind: "trend"`）单行 NDJSON 信封。
- `--limit`：限制展示条数（默认 20，必须 ≥ 1）。
- `--json`：输出完整 `Trend` 数组。

## nitter search

```bash
nitter search <QUERY> [--type tweet|user] [--sort latest|top] [--limit N] [--max-pages N] \
  [--no-reposts] [--media-only] [--media-type image|video|gif] [--json|--ndjson]
```

对配置的实例或 FxTwitter 运行 `QUERY`。当 `--type tweet`（默认）时，查询串原样传给 Nitter（仅由 HTTP 层做一次 URL 转义），
适用 Nitter 自身的查询语法：前导 `#` 搜话题标签，`from:user` 搜某用户的帖子，
其余按普通短语搜索。纯空白查询退出 2。NDJSON 的 `meta.source` 为
`search:<按原样输入的查询>`。
在 FxTwitter 车道上（`fetch_backend = fx`，或 `mix` 下由快车道应答时）搜索接口
**只请求一次**，因此 `--max-pages` 在那里不生效，`--limit` 超过 100 也拿不到——
单次请求最多返回 100 条。实例路径按文档正常翻页；在实例路径上，搜索是 Nitter 的
一项独立能力，某个实例可能禁用或响应很慢——先用 `nitter instances test --full`
探测，再怀疑查询串。

`--sort latest|top`（默认 `latest`）选择结果排序，并同时下发给两个后端——Nitter 的
`f=` 参数（`latest` 对应 `tweets`，`top` 对应 `top`）与 FxTwitter 的 `feed`——因此
mix 模式降级时绝不会给出与请求不同的排序，翻页的每一页也保持该排序。取值对大小写
和首尾空白不敏感；其他值退出 2（绝不静默回退到默认排序）。`--sort` 与 `--type user`
同时给出退出 2——用户搜索没有排序概念。

当 `--type user` 时，按关键词搜索推主、画师、KOL 账号：

无匹配时上游返回 200 + 空列表（退出 0）；上游用户搜索接口返回 404 属故障，按 `not_found` 上报（退出 1），而不是当作空结果。与所有取数失败一样，它只写 stderr——`--ndjson` **不会**输出 `kind: "error"` 信封，请靠退出码判断。
- TTY 渲染用户表格：`@<handle>  <name>  <followers>  <bio>`。
- 管道模式（`!isatty`）自动输出 `kind: "profile"` NDJSON 信封。
- `--json` 输出 Profile 对象或数组。

字段过滤与 `user` 一致：`--no-reposts`、`--media-only`、
`--media-type image|video|gif`（抓取之后、输出之前应用）。

```bash
nitter search "#AI" --sort top --limit 10 --json   # 按热门排序而非最新优先
```

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

抓取单条推文。默认 `fetch_backend=mix` 下优先走 FxTwitter 快道，遇故障或未命中时平滑降级至 Nitter 实例；`fx` 模式下快道是唯一允许的来源，错误按原样上报，不再有降级掩盖真因。
`REF` 是纯数字 status ID，或推文 URL——`x.com`、`twitter.com` 或
任意 Nitter 实例，形状为 `<user>/status/<id>`；user 段可省略（Nitter 直接提供
`/status/<id>` 路由），`/photo/N` 与 `/video/1` 后缀同样接受。位置参数 `REF`
始终优先；仅在未提供位置参数且 stdin 非 TTY 时，才从 stdin 读一行作为引用。
给出位置参数时绝不读取 stdin，因此 `nitter get <REF>` 不会卡在写端保持打开的管道上。
没有分页 flag。被引用推文（quote）存在时以 `quote` 字段摘要呈现（`--json`/
`--ndjson` 可见）；互动数不予报告——绝不虚构。NDJSON 的 `meta.source` 为
`status:<数字 ID>`。当由 FxTwitter 提供时，NDJSON 的 `meta.instance` 标为 `FxTwitter`。

示例（`--json` 对单条推文只输出一个对象；形态为示意）：

```json
{"id":"2102761519985332442","url":"https://x.com/NASA/status/2102761519985332442","text":"…","author":{"handle":"NASA","name":"NASA","avatar_url":"…"},"published_at":"2026-07-27T09:09:40Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}
```

## nitter media

```bash
nitter media <REF>... [--strategy auto|fx|nitter|xdown] \
  [--quality high|medium|low] [--probe] [--json|--ndjson]
```

把每条 status REF 解析成可直接下载的媒体直链——视频 mp4 变体、图片原图、
GIF。`REF` 的形态与 `nitter get` 相同（纯数字 ID，或 x.com / twitter.com /
任意 Nitter 实例的推文 URL；接受 `/photo/N` 与 `/video/1` 后缀）。多个 REF
按批次运行；位置参数始终优先——仅在未提供位置参数且 stdin 非 TTY 时，才从 stdin
读取引用（每行一个，空行忽略）。给出位置参数时绝不读取 stdin，因此批次不会卡在
写端保持打开的管道上。下载动作本身由
调用方完成——本命令只解析直链，不抓取媒体。

**策略**（`--strategy`，默认 `auto`）：`auto` 按链路 fx → nitter → xdown 依次尝试，
返回**第一个**产出媒体的策略（`source` 标明胜出者）；显式指定名称则只运行该策略。payload 能解析但不含媒体的策略会被跳过、
继续下一个；所有策略都为空时按 `not_found` 错误解析——「推文没有媒体」是
正常的分类结果，不是崩溃。

**信任边界**：fx、xdown 是**第三方公共服务**——解析请求会
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
https://x.com/NASA/status/2102761519985332442	fx	video	https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4	-	3.2	24000
```

列为 `ref source kind url label duration size`——ref 原样回显输入引用；尾部
缺省单元格为 `-`。`--json` 在整个调用恰好解析出一条媒体时输出单个 JSON
对象，否则为数组，没有结果时为 `[]`。`--ndjson` 按引用顺序输出：每条媒体
一个信封（`kind` `media`，下载 URL 作为 `id`，MediaResolution 作为 `data`，
`meta.input` = 原始 ref），每个失败的 REF 一个 `kind:"error"` 信封：

```json
{"schema":"nitter.pipeline/v1","kind":"media","id":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","data":{"ref":"https://x.com/NASA/status/2102761519985332442","source":"fx","kind":"video","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","variants":[{"url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","bitrate":2176000}]},"meta":{"input":"https://x.com/NASA/status/2102761519985332442"}}
```

退出码：成功为 0（探测失败不计）；单个 REF 解析失败会得到就地错误报告
（NDJSON 流上为 error 信封，其他模式为 stderr 的 `error: <ref>: <message>`），
其余 REF 继续运行；至少一个 REF 失败时以 `media completed with N of M refs
failed` 摘要退出 1；用法问题（`--json` 与 `--ndjson` 同给、`--strategy`/
`--quality` 不合法、引用缺失或不合法）退出 2。

## nitter download

```bash
nitter download <REF>... [--output DIR] [--kind image|video|gif|cover] \
  [--quality high|medium|low] [--strategy auto|fx|nitter|xdown] \
  [--on-exists refuse|skip|overwrite] [--filename-template TEMPLATE] \
  [--json|--ndjson]
```

用 `media` 命令的策略链解析每条 status REF，并把计划好的媒体文件下载到输出
目录。`REF` 的形态与 `nitter get`、`nitter media` 相同（纯数字 ID，或
x.com / twitter.com / 任意 Nitter 实例的推文 URL；接受 `/photo/N` 与
`/video/1` 后缀）。多个 REF 按批次运行。位置参数始终优先；仅在未提供位置参数
且 stdin 非 TTY 时从 stdin 读取输入——首个非空白字节为 `{` 时，每个非空行都必须是严格的
`nitter.pipeline/v1` tweet 信封，取每条记录的 `data.url` 作为 REF（`nitter get
--ndjson` 与 `nitter watch --ndjson` 的流可直接喂给 download；信封格式错误是用法
错误），否则每个非空行都是一个普通 REF。给出位置参数时绝不读取 stdin，因此批次
不会卡在写端保持打开的管道上。

**选择**（`--kind`，默认：全部）遵循 video-wins 规则：带视频或 GIF 的推文
只下载唯一一个最佳视频文件——按码率（或 xdown 的 p 值）排序；实测 xdown
对一条视频推文会返回多个码率条目外加一张封面图，这正是计划层要收敛的原因——
胜出者把其余候选保留为回退链：第一个 URL 失败时按序落到回退项，行内报告的是
实际成功的那个候选。纯图推文下载全部图片（`<id>-1.jpg` ... `<id>-4.jpg`）。
`--kind image|video|gif` 是其前置过滤；`--kind cover` 只计划视频的封面图，
写为 `<id>-cover.<ext>`——纯图推文没有封面，按 `not_found` 报告。过滤器匹配
不到任何文件不算错误（stderr 打印一行 `nothing found for <ref>`）。播放列表
（HLS `.m3u8`、DASH `.mpd`）永远不会成为下载候选。

文件扩展名优先取自解析出的 URL 路径；路径不带扩展名时取下载响应的
Content-Type（`image/jpeg`→`.jpg`、`image/png`→`.png`、`image/webp`→
`.webp`、`image/gif`→`.gif`、`video/mp4`→`.mp4`），再退回按 kind 的默认值
（图片与封面 `.jpg`，视频与 GIF `.mp4`）。

**命名模板**：`filename_template` 配置键（默认 `{id}-{seq}`，即
`<id>-<seq>.<ext>`）渲染每个普通文件的文件名；`--filename-template TEMPLATE`
单次覆盖（flag > config）。占位符：`{id}` status id，`{seq}` 文件在计划中的
1-based 序号，`{user}` ref 的 user 段原样（裸 ID 为空；无 user 的
`/i/status/<id>` 路由报告 `i`），`{kind}` image/video/gif/cover，`{ext}`
计划扩展名（带前导点）——计划不带扩展名时为空，此时扩展名由下载响应的
Content-Type 在下载时决定，并追加在**最终渲染名**之后，与无模板时完全一致；
模板不含 `{ext}` 时扩展名追加在末尾。封面忽略文件名模板：始终落盘为
`<id>-cover.<ext>`。`directory_template` 配置键（无 flag；默认空 = 平铺）把
文件放进输出目录的子目录，按文件从 `{id}`/`{user}`/`{kind}` 渲染——该位置
禁用 `{seq}` 与 `{ext}`，`/` 分隔层级，空层级跳过。渲染结果做安全清洗
（Windows 非法字符 `\ / : * ? " < > |` 与控制字符替换为 `_`；`.` 与 `..`
目录层级被拒绝）。非法模板——未知或畸形占位符、目录位置出现禁用占位符、
文件名位置出现路径分隔符——绝不使运行失败：打印一条 stderr 警告（命名该
模板），文件名回退默认模板、目录回退平铺。同一 ref 的两个计划文件渲染出
相同名字时发生碰撞：后到者获得扩展名之前的 `-2`、`-3`… 数字后缀并告警；
跨 ref 沿用原有 `--on-exists` 语义。空模板值即默认值。

**策略与信任边界**（`--strategy`，默认 `auto`）：与 `media` 命令相同的链路
——`auto` 依次尝试 fx → nitter → xdown，首个产出媒体的
策略胜出（`source` 标明）；显式指定名称则只运行该策略。对 download 而言边界
比 `media` 更严格，因为抓取本身也会发生：fx、xdown 是
**第三方公共服务**——解析与下载都会把推文 URL 发送给它们，因此只用于你愿意
分享的公开推文。`--strategy nitter` 是完全私有路径：解析与下载都留在**你自己**
配置的实例上（`[[instances]]` 的第一个条目，或 `--instance`）；其直链可能是
你自己实例提供的纯 http 链接，对此予以信任。下载请求与其他一切抓取一样走
配置的代理（`--proxy` / 配置 `proxy`）。

输出目录为 `--output DIR`，否则为 `download_path` 配置键（默认
`./nitter-media`；相对路径按工作目录解析）；目录按需创建（`mkdir -p`）。
解析后的**绝对路径**会在写入任何文件之前于 stderr 报告一行
（`note: writing to <dir>`）——仅供参考、从不作为门禁，且绝不会出现在 stdout
（stdout 对 `--json` / `--ndjson` 保持纯净的机器契约）。

**`--on-exists`**（默认 `refuse`）决定目标文件已存在于磁盘时的行为：
`refuse` 把该条目报告为错误，批次继续；`skip` 保留已有文件，行内以文件实际
的磁盘大小报告、且不含 sha256（没有重新下载，也不虚构数据），绝不计为失败；
`overwrite` 经同样的「临时文件落盘再原子重命名」流程重新下载。同一批次内的
重复 ref 会命中相同文件名：`refuse` 下第二次出现按文件已存在报错，批次继续。

人类 / text 输出为每个下载文件一行制表符行：

```text
https://x.com/NASA/status/2102761519985332442	/home/you/nitter-media/2102761519985332442-1.mp4	24000000	video	fx
```

列为 `ref path bytes kind source`——`path` 是绝对文件路径（也是 NDJSON 信封
的 `id`）；`--on-exists skip` 下 path 单元格为 `<path> (skipped)`。`--json`
把下载的文件输出为一个 JSON 文档（恰好一个文件时是单个对象，否则为数组，
没有文件时为 `[]`）。`--ndjson` 按引用顺序输出：每个下载文件一个信封
（`kind` `download`，绝对路径作为 `id`，DownloadRecord 作为 `data`，
`meta.input` = 原始 ref），每个失败的 REF 一个 `kind:"error"` 信封
（`data.command` 为 `download`，`data.stage` 为 `resolve`、`plan` 或
`download`）：

```json
{"schema":"nitter.pipeline/v1","kind":"download","id":"/home/you/nitter-media/2102761519985332442-1.mp4","data":{"ref":"https://x.com/NASA/status/2102761519985332442","path":"/home/you/nitter-media/2102761519985332442-1.mp4","kind":"video","source":"fx","url":"https://video.twimg.com/ext_tw_video/100/pu/vid/pl.mp4","bytes":24000000,"sha256":"…"},"meta":{"input":"https://x.com/NASA/status/2102761519985332442"}}
```

退出码：成功为 0（没有计划、没有下载时在 stderr 打印 `(empty)`；消费端提前
关闭 stdout 管道是干净停止）；单个 REF 失败会得到就地错误报告（NDJSON 流上
为 error 信封，其他模式为 stderr 的 `error: <ref>: <message>`），其余 REF
继续运行；至少一个 REF 失败时以 `download completed with N of M refs
failed` 摘要退出 1；用法问题（`--kind`/`--quality`/`--strategy`/
`--on-exists` 不合法、引用缺失或不合法、stdin
信封格式错误、`--json` 与 `--ndjson` 同给）退出 2。

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

管理 `~/.nitter-cli/config.toml`（TOML，权限 0600）的十三个标量键。优先级：
命令行 flag > 环境变量 > 文件 > 内置默认。`[[instances]]` 与 `[[watch.sources]]`
数组表需直接编辑文件——`config set` 拒绝它们。创作者圈层（`nitter circle` 名册）
存放在另一个文件 `~/.nitter-cli/circles.toml`，由 `circle` 子命令管理。

| 键 | 类型 | 默认 | 环境变量覆盖 | 含义 |
| --- | --- | --- | --- | --- |
| `default_limit` | int | `20` | `NITTER_DEFAULT_LIMIT` | 命令未传 `--limit` 时的条数上限 |
| `max_pages` | int | `5` | — | 单次抓取分页上限 |
| `request_interval` | duration | `1s` | — | 相邻请求开始时间的全局最小间隔 |
| `retry_attempts` | int | `2` | — | 首次之外的重试次数（网络错误与 5xx） |
| `retry_delay` | duration | `1s` | — | 线性退避基数：第 n 次重试等待 `retry_delay × n` |
| `instance_cooldown` | duration | `60s` | — | 实例失败（429 / 网络错误）后的冷却时长 |
| `fetch_backend` | 枚举 | `mix` | `NITTER_FETCH_BACKEND` | `mix`（默认优先 FxTwitter，故障回退自建 Nitter）或 `fx`（纯 FxTwitter）。已移除的 `nitter` 值会被拒绝；要让某次调用只走自建实例，用 `--instance URL`（仅 `user`/`search`/`get`/`list`） |
| `proxy` | string | `""` | — | 代理 URL（`http(s)`、`socks5(h)`）；空 = 未配置代理——`HTTPS_PROXY`/`ALL_PROXY` 只对 FxTwitter 快车道与 `update` 生效，**不**作用于 nitter 传输 |
| `log_level` | 枚举 | `info` | `NITTER_LOG_LEVEL` | `debug` 或 `info`；诊断只进 stderr，不污染 stdout |
| `log_format` | 枚举 | `text` | `NITTER_LOG_FORMAT` | `text` 或 `json`（单行） |
| `download_path` | string | `./nitter-media` | — | `nitter download` 写入媒体的位置（相对当前工作目录；按需创建；`download --output DIR` 单次覆盖） |
| `filename_template` | string | `{id}-{seq}` | — | 普通媒体的下载文件名，占位符 `{id}` `{seq}` `{user}` `{kind}` `{ext}`（默认 = 模板出现前的 `<id>-<seq>.<ext>` 命名；封面始终 `<id>-cover.<ext>`；`download --filename-template` 单次覆盖；非法模板告警并回退默认） |
| `directory_template` | string | `""` | — | `download_path` 之下的下载子目录，占位符 `{id}` `{user}` `{kind}`（`/` 分隔层级；禁用 `{seq}`/`{ext}`；空 = 平铺） |

`default_limit` 与 `max_pages` 是上限，必须 `>= 1`：`0` 不是「不限制」的写法。
环境变量优先于文件：`NITTER_DEFAULT_LIMIT`（整数）、`NITTER_LOG_LEVEL`、
`NITTER_LOG_FORMAT`、`NITTER_FETCH_BACKEND`。

数组表（手工编辑）：

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

- `config path` 打印配置文件路径。不接受参数（否则退出 2）。
- `config get` 不带键时按 `key = value` 打印全部十三个键；带键时只打印该键。
  未知键在读取文件之前即被拒绝（退出 2）。
- `config set KEY [VALUE]` 在**任何磁盘写入之前**校验并转型（`default_limit`/
  `max_pages` 为 `>= 1` 的整数——它们是上限，`0` 不是「不限制」的写法；
  `retry_attempts` 为 `>= 0`；
  `request_interval`/`retry_delay`/`instance_cooldown` 为 `>= 0` 的时长；
  `fetch_backend` 取 `mix|fx`（`nitter` 已移除——实例路径改为用 `--instance` 在有实例路径的命令上逐次选择）；
  `log_level` 取 `debug|info`；`log_format` 取 `text|json`；`proxy`、
  `download_path` 与两个命名模板接受任意字符串）。不给 VALUE 时从管道 stdin
  读一行（敏感值不该进 argv）；TTY 下既无 VALUE 也不可读 stdin 是用法错误。
  未知键被拒绝，并提示 `[[instances]]`/`[[watch.sources]]` 需直接编辑文件。
- `config unset KEY` 删除该键，使其回落到环境变量/默认值。
- `download_path`（默认 `./nitter-media`，相对当前工作目录）是 `nitter
  download` 写入媒体的位置。它没有环境变量覆盖；`nitter download --output
  DIR` 在每次调用时覆盖它，目录在下载时创建——`config set` 不做存在性检查。
- `filename_template`（默认 `{id}-{seq}`）与 `directory_template`（默认空 =
  平铺）是 `nitter download` 的命名模板；两者都没有环境变量覆盖。
  `--filename-template` 在每次调用时覆盖文件名模板。`config set` 接受任意
  字符串——非法模板在下载时以 stderr 警告回退默认/平铺（见 download 一节）。
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
  [--max-new-overflow drop|keep] [--max-pages N] [--include-existing] \
  [--state-dir DIR] [--ndjson] [--json] [--no-reposts] [--media-only] \
  [--media-type image|video|gif]
```

按轮询周期抓取各来源，对照持久化去重状态（`~/.nitter-cli/state/seen.json`，或
`<--state-dir>/seen.json`）只输出新推文。

**来源**为任意一组 `user:<handle>`、`tag:<query>`、`list:<id>`，例如
`user:NASA`、`tag:#AI`、`tag:from:nasa`、`list:12345`。第一个冒号后的 ref 原样
传给抓取层，并构成 seen 键 `<kind>:<ref>`——**tag 查询写原始形式（`tag:#AI`）；
URL 预转义形式（`tag:%23AI`）会在网络上被二次转义，不是合法写法。** 不带
SOURCE 参数时使用配置 `[[watch.sources]]`；两者都为空：退出 2。

**合并取数**——带 `--no-reposts` 且有两个及以上 `user:` 源时，一轮改用
**每批一次** RSS 请求取这些源（`/{u1,u2,...}/rss`，每批路径长度控制在 250
字符内），再按作者拆分 feed，而不是逐源抓取。合并 feed 只有 Nitter 提供，
且无法表示转推——Nitter 会把转推条目归到原作者名下——这正是合并要求
`--no-reposts` 的原因；`fetch_backend = "fx"` 从不合并，`"mix"` 下这些源改由
Nitter 提供而非快车道。某批请求失败不会丢源：受影响的源回退到各自的抓取
路径。

**flag**

| flag | 默认 | 含义 |
| --- | --- | --- |
| `--once` | 关 | 只跑一轮即退出——推荐的调度器形态。 |
| `--interval D` | `10m` | 不带 `--once` 时轮次间的睡眠时长；必须是 `>= 1s` 的 duration（两种模式下都会校验）。 |
| `--max-new N` | `10` | 每源每轮最多输出的新推文数（从新到旧）。`0` 不输出任何内容，并把当前首页封存为新基准；负数是用法错误。 |
| `--max-new-overflow drop\|keep` | `drop` | 单轮突发超出 `--max-new` 上限的部分如何处理。`drop` 立即将超出部分标记为已见——之后绝不补推（宁丢勿重）。`keep` 不将其标记为已见，后续各轮会在同一上限下重新推送（宁重勿丢；突发量超过两倍上限时需多轮才能排空）。其他值是用法错误；`--max-new 0` 始终按规则重建基准，不受本项影响。 |
| `--max-pages N` | 配置 `max_pages`（5） | 每轮抓取页数预算；显式传值必须 ≥ 1（`0` 与负数都是用法错误），不传时应用配置值。 |
| `--include-existing` | 关 | 未初始化源的首轮输出整个首抓结果（默认：首跑只记录状态）。 |
| `--state-dir DIR` | `~/.nitter-cli/state` | 存放 `seen.json` 的目录（不存在则创建）。每个订阅类别使用各自的目录——见下文「多类别订阅」。 |
| `--ndjson` | 关 | 每条记录一个信封：`kind` 为 `tweet` 与 `error`。 |
| `--json` | — | 仅可与 `--once` 同用：打印**一个** JSON 文档 `{"tweets":[…裸 Tweet 对象…],"errors":[{"ref","code","message"}…]}`——本轮选出的推文与逐源抓取失败（两个数组为空时是字面量 `[]`；有源失败仍退出 1）。不带 `--once` 时是用法错误：常驻循环是逐轮的流，不是单个文档。 |
| `--no-reposts` | 关 | 在**去重之前**丢弃纯转推（转推标记只存在于 HTML 解析路径）。 |
| `--media-only` | 关 | 在**去重之前**丢弃不带媒体附件的推文。 |
| `--media-type image\|video\|gif` | — | 在**去重之前**只保留携带至少一个该类型媒体条目的推文；其他值是用法错误。 |

全局 `--proxy`/`--instance` 与其他命令一致。

**首跑与输出规则**

- 未初始化源的第一轮只**记录**状态——不输出任何历史（只记不推）。
  `--include-existing` 对该次运行解除此限制，且该轮首抓不受 `--max-new` 限制。
- 之后的每轮输出各源的新推文，每源每轮最多 `--max-new` 条。默认
  （`--max-new-overflow drop`）下**超出上限的新推文会被立即标记为已见、之后
  绝不补推**：停机恢复后，单源单轮突发超过上限的部分会被静默跳过——调度器
  部署应显式设置 `--max-new`。`--max-new-overflow keep` 则不将超出部分标记为
  已见，后续各轮会在同一上限下重新推送（宁重勿丢），直至积压排空；突发量
  超过两倍上限时需要多轮。
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
  空、`--interval < 1s`、flag 为负、`--max-new-overflow` 不合法、不带 `--once`
  的 `--json`、`--json` 与 `--ndjson` 同给）。
- 不带 `--once` 时命令持续循环，直到 SIGINT/SIGTERM（优雅退出 0）或不可恢复
  错误（状态存储失败、非 EPIPE 的 stdout 写失败 → 退出 1）。
- stdout 管道被关闭（EPIPE）视为消费端挂断，两种模式下都退出 0。Windows 上该
  检测是尽力而为（管道断裂可能以 `ERROR_BROKEN_PIPE` 呈现）。

**状态**按源存储：最多 300 条已见 ID（从新到旧），外加最近 20 个首页纯数字 ID
作为扫描水位。用 `nitter seen list --state-dir <dir>` 检视（不带 flag 则查看
默认位置）；`seen clear` 接受同一个 flag。

**多类别订阅——每个类别一个 `--state-dir`。** 订阅*类别*指用途、节奏或推送渠道
相同的一组源。给每个类别各自的 `--state-dir ~/.nitter-cli/state/<category>`，
并在该类别的每一行调度命令里都传同一个 flag；漏传该 flag 的行会回落到默认的
`~/.nitter-cli/state`，从而把两个类别重新耦合在一起。状态在一个 `seen.json` 内
以源键为键，因此两个类别订阅同一账号时会共用 `user:HANDLE`：先跑的那一轮把推文
标记为已见，另一个类别就静默丢弃它——不报错，`--once` 仍退出 0。分目录也能让
并发运行彼此独立：状态存储只在单个进程内串行化写入，因此两个任务同时对同一个
`seen.json` 读-改-写时，后写者会覆盖先写者的标记。用同一个 flag 限定
`seen list`/`seen clear`，即可只检视或重置某一个类别。

示例——调度器消费 NDJSON 流（示意）：

```bash
nitter watch user:NASA tag:#AI --once --ndjson --max-new 50
```

突发时宁愿重推也不丢推，加 `--max-new-overflow keep`：

```bash
nitter watch user:NASA tag:#AI --once --ndjson --max-new 50 --max-new-overflow keep
```

```json
{"schema":"nitter.pipeline/v1","kind":"tweet","id":"2102761519985332442","data":{…},"meta":{"source":"user:NASA","instance":"http://nitter.internal:8080","fetched_at":"2026-09-12T08:00:00Z"}}
{"schema":"nitter.pipeline/v1","kind":"error","data":{"command":"watch","stage":"fetch","code":"upstream_unavailable","message":"…"},"meta":{"input":"tag:#AI"}}
```

`--once --json` 则把整轮输出为一个文档（示意）：

```json
{"tweets":[{"id":"2102761519985332442","url":"…","text":"…","author":{…},"published_at":"2026-09-12T08:00:00Z","media":[],"is_retweet":false,"reposted_by":"","reply_to":"","quote":null}],"errors":[{"ref":"tag:#AI","code":"upstream_unavailable","message":"…"}]}
```

## nitter seen

```bash
nitter seen list [--source SOURCE] [--json] [--state-dir DIR]
nitter seen clear [--source SOURCE] [--state-dir DIR] --confirm
```

检视与清除 watch 去重状态——**默认位置**为 `~/.nitter-cli/state/seen.json`，
也可用 `<--state-dir>/seen.json`（即 `watch --state-dir` 使用的同一目录）。
命令不创建任何东西：没有 `seen.json` 的状态目录就是空库。

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
nitter update [--confirm] [--proxy URL]
nitter update --check [--prerelease] [--json] [--proxy URL]
```

报告二进制的更新方式。当存在更新版本时会**提供安装**：安装路径只下载**本平台的归档**，用发布的
`checksums.txt` 校验其 SHA-256，检查暂存二进制报告的版本，全部通过后才替换可执行文件。
**任何失败都不会改动现有安装**——所有校验通过前不会碰目标文件。

- 不带 flag 的 `nitter update` 先比对版本，然后在终端询问
  `install now? [y/N]`（默认 No）。无 `--confirm` 且无终端时，它打印比对结果加
  `not installed: stdin is not a terminal — re-run with --confirm` 并退出 0
  ——**绝不阻塞读取管道**，且拒绝安装**不是**错误。报告为 `current version:` /
  `latest release:` / `up to date`（或 `update available:` / `installed <version> → <path>`）。
- `--confirm` 不询问直接安装；stdin 非终端时必须给出（脚本显式 opt-in）。
  与 `--check` 同用是用法错误（退出 2）——两种模式互斥。
- `--check` 经 GitHub Releases API 将当前版本与
  `github.com/shitianyaa/nitter-cli` 的最新发布版比较，**绝不写入任何东西**。草稿版恒被排除；
  `--prerelease` 允许预发布版参与"最新版"遴选。遴选按严格 semver 优先级
  （`v` 前缀可选、忽略构建元数据、遵循 semver 预发布排序）在 API 首页内
  进行，而非按发布时间。所有成功的检查均退出 0——过时是报告结果而非失败：
  `update available: <version> (<release URL>)` 或 `up to date`。当前版本
  比最新发布版还新（例如装了预发布版）视为最新。
- `--json`（仅可与 `--check` 同用）打印一个键齐全的 JSON 文档：
  `{"current":"0.1.0","latest":"0.2.0","outdated":true,"prerelease":false,"release_url":"…"}`。
  开发构建输出：`{"current":"dev","development_build":true}`。
- **`go install` 安装的二进制会被拒绝**，绝不替换：后续的 `go install` 会静默
  抹掉更新，因此命令退出 1 并给出对应的
  `go install github.com/shitianyaa/nitter-cli/cmd/nitter@<tag>` 行。无法判定
  安装来源的安装退出 1，并指向发布页面。
- 开发构建（编译时未注入版本元数据）完全跳过检查并退出 0：没有可比较的发布版，
  也没有可替换的对象。
- **代理**：`--proxy`（或 `config.proxy`）对本命令的**每个请求**生效——发布查询与
  资产下载。不支持的代理 scheme 在任何网络调用之前就是用法错误（退出 2）。
  `--proxy` 与 `config proxy` 都为空时，`update` 会读取环境变量
  `HTTPS_PROXY`/`ALL_PROXY`。
- 检查失败——网络错误、GitHub 错误（仅报 HTTP 状态码；绝不回显响应体）、
  无可用发布版——是运行时失败（退出 1）。校验失败（checksum 不符、归档格式错误，
  或暂存二进制报告的版本不对）同样是退出 1，且现有安装保持不变。
- `--json` 与 `--prerelease` 仅可与 `--check` 同用（否则用法错误，退出 2）。
- **Agent 不得在未获用户授权时执行 `nitter update`。** `--confirm` 是为用户自己的
  脚本提供的机制，不构成授权。
