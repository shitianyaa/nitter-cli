# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- **`nitter search --sort latest|top`**：`search` 新增结果排序 flag——`latest`（默认，最新优先，与 0.7.4
  之前的请求完全一致）或 `top`（热门结果）。该排序会同时下发给两个后端（Nitter 的 `f=` 参数与
  FxTwitter 的 `feed`），并在每一页翻页请求中保持不变，因此 mix 模式降级时绝不会给出与请求不同的排序。
  取值对大小写和首尾空白不敏感；其他值退出 2，`--sort` 与 `--type user` 同时给出同样退出 2（用户搜索
  没有排序概念）。注意 `comments --sort likes|recency` 属于不同域：两套取值互不通用。

## 变更

- **`--limit` 与 `--max-pages` 不再有「不限制」这个取值**：两者都是上限，显式传值必须 `>= 1`；`0` 与负数
  都是用法错误（退出 2）。`0` 过去对 `--limit` 表示「全部」、对 `--max-pages` 表示「取消上限」，而两条
  取数车道对它的理解**相反**——Nitter 侧当作无上限，FxTwitter 侧当作零条——于是同一个 flag 会因为
  最终由哪条车道应答而给出不同结果。配置键 `default_limit` 与 `max_pages` 同规则（均 `>= 1`；
  `retry_attempts` 仍接受 `0`）。**迁移**：把 `--limit 0` 换成一个明确的上限，把 `--max-pages 0` 换成一个
  大的页数预算；要深挖积压就两者都写大。`user --max-pages 0` 的移除也一并去掉了「翻页到上游耗尽」这条
  逃生通道。请优先用逐次传入的 flag，而不是调大全局 `max_pages`：`watch`（每轮）与 `circle run`
  （每个成员）读的是同一个键；而 `search` 在 FxTwitter 车道上忽略 `--max-pages`——那条车道只发一次请求。
- **`trends --limit` 默认值改为 `20`**（原为 `0`，即接口返回的全部热搜）。要看得更多请传更大的值。
- **`fetch_backend` 只剩 `mix` 与 `fx`**：`nitter` 值已移除。它的语义——只用实例、绝不走快车道——在剩下的两种
  模式里没有等价物，而悄悄映射成 `mix` 会加上它原本要排除的快车道，因此现在会被拒绝，并在错误信息里指明改用
  `mix`。要让某一次调用只走自建实例，传 `--instance URL`；该 flag 也仍是验证单个实例的手段。
  **迁移**：把 `fetch_backend = "nitter"` 改成 `"mix"`（或 `"fx"`），需要单次走实例路径时用 `--instance URL`。
- **`fetch_backend = fx` 下 `get` 不再降级到 Nitter**：该降级会在没有配置实例时把真实的 Fx 错误替换成
  `no instances configured`，掩盖了真因。现在 `fx` 模式下快道是唯一允许的来源，错误按原样上报；`mix`
  的回退保持不变。
- **`--instance` 的文档改为写明它实际覆盖的范围**：它只对**有实例路径的命令**生效（`user`、`search`、
  `get`、`list`）；六个 fx-only 能力（`comments`、`following`、`profile`、`quotes`、`trends`、
  `search --type user`）会忽略它，且该覆盖是纯 URL、**不携带** basic-auth 凭证。

## 弃用

## 移除

- **`fetch_backend` 的 `nitter` 值** —— 迁移方式见上方「变更」条目。
- **`user --max-pages 0`** 这种「不设页数上限」的写法。

## 修复

- **`user --limit 0` 与 `circle run --limit 0` 在 FxTwitter 车道上不再返回空结果**：该车道把 `count <= 0`
  当作「零条推文」并返回空成功，于是文档承诺的「取全部」静默变成什么都没有——而 `mix` 还会把它当作成功，
  不再回退到实例。现在该取值是用法错误，车道自身也拒绝非正数 count，而不是返回空。
- **`watch --max-pages 0` 不再静默覆盖配置里的 `max_pages`**：它过去被接受，并让该轮退回内置默认的 5 页，
  完全忽略配置值。现在它是用法错误。
- **重复游标不再烧掉分页预算**：翻页会跟随上游游标链却不记录走过的位置，因此上游若重复给出同一个游标
  （A → B → A），请求会一直发到页数预算耗尽，结果里还可能同一页出现多次。Nitter 的 HTML 路径、Nitter 的
  search/list 路径与 FxTwitter 车道现在一旦遇到重复游标就停止；停滞的链条返回「无后续游标」，而不是编造一个。
- **`get`、`media`、`download`：位置参数优先于标准输入**——仅当未提供位置参数**且** stdin 非 TTY 时
  才读取 stdin。此前命令会先读 stdin，再以歧义错误拒绝输入（`status reference given both as an
  argument and on stdin`，退出 2）；当管道的写端保持打开时，这次读取会永久阻塞，导致管道中的
  `nitter get <REF>`（以及带位置参数的 `media`/`download`）挂起而非执行。歧义报错及其退出 2 分支已彻底
  移除；输入只需给出一种方式，两种同时给出不再报错。显式传入的空位置参数
  （`nitter get ""`）是用法错误（退出 2），绝不回退读取 stdin。
- **`quotes` 与 `search --type user` 不再把上游故障当作空结果**：FxTwitter 的引用与用户搜索路由共用同一条
  上游查询，会间歇性返回 404（实测约 85% 的请求），而这两个命令都把这个 404 变成了空成功——一条有 44 条
  引用的推文会打印 `[]` 并退出 0。现在 404 上报为 `not_found`（退出 1）。`quotes` 会先回查推文自身的引用
  计数，所以确实没有引用的推文仍退出 0 并输出空结果；`search --type user` 则把空列表当作它本来的含义——
  「没有匹配」。
- **错误信息不再携带请求 URL 与上游响应体**：FxTwitter 车道此前把含查询串的完整 URL 拼进传输错误与 404
  错误，导致搜索失败时会把调用者自己的搜索词打印出来，并且会回显上游的 `message` 字段。现在错误只写路由
  路径，符合 `sdk` 的脱敏契约。
- **`user --help` 不再把 RSS 说成单页**：它此前声称 RSS 只有一页、`--max-pages` 只作用于 HTML 回退。
  实际上 RSS 会沿其 `Min-Id` 游标逐页读取、页数预算按层各算一份，因此该帮助文本与实现、以及所有
  描述该扫描的文档相矛盾；此外 `--limit` 满足后就不再请求下一页。仅改帮助文本，行为不变。

## 安全
