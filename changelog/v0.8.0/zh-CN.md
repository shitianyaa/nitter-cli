# v0.8.0 — 2026-09-24

搜索结果排序、更严格的限制与页数预算契约，以及让长串与管道回归正常的翻页与输入修复。

## 新增

- **`nitter search --sort latest|top`**：`search` 新增结果排序 flag——`latest`（默认，最新优先，与上一版
  请求完全一致）或 `top`（热门结果）。该排序会同时下发给两个后端（Nitter 的 `f=` 参数与 FxTwitter 的
  `feed`），并在每一页翻页请求中保持不变，因此 mix 模式降级时绝不会给出与请求不同的排序。取值对大小写
  和首尾空白不敏感；其他值退出 2，`--sort` 与 `--type user` 同时给出同样退出 2（用户搜索没有排序概念）。
  注意 `comments --sort likes|recency` 属于不同域：两套取值互不通用。
  ([#6](https://github.com/shitianyaa/nitter-cli/pull/6))

## 变更

- **`--limit` 与 `--max-pages` 不再有「不限制」这个取值**：两者都是上限，显式传值必须 `>= 1`；`0` 与负数
  都是用法错误（退出 2）。`0` 过去对 `--limit` 表示「全部」、对 `--max-pages` 表示「取消上限」，而两条
  取数车道对它的理解**相反**——Nitter 侧当作无上限，FxTwitter 侧当作零条——于是同一个 flag 会因为最终由
  哪条车道应答而给出不同结果。配置键 `default_limit` 与 `max_pages` 同规则（均 `>= 1`；`retry_attempts`
  仍接受 `0`）。**迁移**：把 `--limit 0` 换成一个明确的上限，把 `--max-pages 0` 换成一个大的页数预算；
  要深挖积压就两者都写大。`user --max-pages 0` 的移除也一并去掉了「翻页到上游耗尽」这条逃生通道。请优先
  用逐次传入的 flag，而不是调大全局 `max_pages`：`watch`（每轮）与 `circle run`（每个成员）读的是同一个键；
  而 `search` 在 FxTwitter 车道上忽略 `--max-pages`——那条车道只发一次请求。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **`trends --limit` 默认值改为 `20`**（原为 `0`，即接口返回的全部热搜）。要看得更多请传更大的值。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **`fetch_backend` 只剩 `mix` 与 `fx`**：`nitter` 值已移除。它的语义——只用实例、绝不走快车道——在剩下的两种
  模式里没有等价物，而悄悄映射成 `mix` 会加上它原本要排除的快车道，因此现在会被拒绝，并在错误信息里指明
  改用 `mix`。要让某一次调用只走自建实例，传 `--instance URL`；该 flag 也仍是验证单个实例的手段。
  **迁移**：把 `fetch_backend = "nitter"` 改成 `"mix"`（或 `"fx"`），需要单次走实例路径时用 `--instance URL`。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **`fetch_backend = fx` 下 `get` 不再降级到 Nitter**：该降级会在没有配置实例时把真实的 Fx 错误替换成
  `no instances configured`，掩盖了真因。现在 `fx` 模式下快道是唯一允许的来源，错误按原样上报；`mix`
  的回退保持不变。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **`--instance` 的文档改为写明它实际覆盖的范围**：它只对**有实例路径的命令**生效（`user`、`search`、
  `get`、`list`）；六个 fx-only 能力（`comments`、`following`、`profile`、`quotes`、`trends`、
  `search --type user`）会忽略它，且该覆盖是纯 URL、**不携带** basic-auth 凭证。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))

## 移除

- **`fetch_backend` 的 `nitter` 值** —— 迁移方式见上方「变更」条目。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **`user --max-pages 0`** 这种「不设页数上限」的写法。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))

## 修复

- **`user --limit 0` 与 `circle run --limit 0` 在 FxTwitter 车道上不再返回空结果**：该车道把 `count <= 0`
  当作「零条推文」并返回空成功，于是文档承诺的「取全部」静默变成什么都没有——而 `mix` 还会把它当作成功，
  不再回退到实例。现在该取值是用法错误，车道自身也拒绝非正数 count。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **`watch --max-pages 0` 不再静默覆盖配置里的 `max_pages`**：它过去被接受，并让该轮退回内置默认的 5 页，
  完全忽略配置值。现在它是用法错误。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **重复游标不再烧掉分页预算**：翻页会跟随上游游标链却不记录走过的位置，因此上游若重复给出同一个游标
  （A → B → A），请求会一直发到页数预算耗尽，结果里还可能同一页出现多次。Nitter 的 HTML 路径、Nitter 的
  search/list 路径与 FxTwitter 车道现在一旦遇到重复游标就停止；停滞的链条返回「无后续游标」，而不是编造一个。
  ([#6](https://github.com/shitianyaa/nitter-cli/pull/6))
- **`get`、`media`、`download`：位置参数优先于标准输入**——仅当未提供位置参数**且** stdin 非 TTY 时才读取
  stdin。此前命令会先读 stdin，再以歧义错误拒绝输入（`status reference given both as an argument and on
  stdin`，退出 2）；当管道的写端保持打开时，这次读取会永久阻塞，导致管道中的 `nitter get <REF>`（以及带
  位置参数的 `media`/`download`）挂起而非执行。两种同时给出不再报错；显式传入的空位置参数
  （`nitter get ""`）是用法错误（退出 2），绝不回退读取 stdin。
  ([#6](https://github.com/shitianyaa/nitter-cli/pull/6))
- **`quotes` 与 `search --type user` 不再把上游故障当作空结果**：FxTwitter 的引用与用户搜索路由共用同一条
  上游查询，会间歇性返回 404（分窗口：2026-09-24 那个采样窗口实测约 85% 失败），而这两个命令都把这个 404
  变成了空成功——一条有 44 条引用的推文会打印 `[]` 并退出 0。现在 404 上报为 `not_found`（退出 1）。
  `quotes` 会先回查推文自身的引用计数，所以确实没有引用的推文仍退出 0 并输出空结果；`search --type user`
  则把空列表当作它本来的含义——「没有匹配」。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **错误信息不再携带请求 URL 与上游响应体**：FxTwitter 车道此前把含查询串的完整 URL 拼进传输错误与 404
  错误，导致搜索失败时会把调用者自己的搜索词打印出来，并且会回显上游的 `message` 字段。现在错误只写路由
  路径，符合 `sdk` 的脱敏契约。
  ([f768b5e](https://github.com/shitianyaa/nitter-cli/commit/f768b5e))
- **`user --help` 不再把 RSS 说成单页**：它此前声称 RSS 只有一页、`--max-pages` 只作用于 HTML 回退。实际上
  RSS 会沿其 `Min-Id` 游标逐页读取、页数预算按层各算一份，因此该帮助文本与实现、以及所有描述该扫描的文档
  相矛盾；此外 `--limit` 满足后就不再请求下一页。仅改帮助文本，行为不变。
  ([#6](https://github.com/shitianyaa/nitter-cli/pull/6))
- **操作 skill 与排障文档订正**：agent skill 曾把 `HTTPS_PROXY`/`ALL_PROXY` 环境变量写成实例流量的回退
  （它们只作用于 FxTwitter 快车道与 `update`）、把 `config set` 的取值区间写成 `ints >= 0`（只有
  `retry_attempts` 接受 `0`；`default_limit`/`max_pages` 必须 `>= 1`）、把 `following --limit` 描述成限制
  页数而非 profile 条数；两份 CLI 参考现在也写明 `watch` 的 `user:` 源空首抓会完成初始化，而 `tag:`/
  `list:` 源要等某轮返回至少一条推文。
  ([#6](https://github.com/shitianyaa/nitter-cli/pull/6))

**Full Changelog**: [v0.7.3...v0.8.0](https://github.com/shitianyaa/nitter-cli/compare/v0.7.3...v0.8.0)
