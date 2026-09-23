# v0.7.2 — 2026-09-23

RSS 取数可翻页、多源合并为一次请求，以及随之而来的文档修正。

## 新增

- **多源 watch 的合并 RSS 取数**：带 `--no-reposts` 且有两个及以上 `user:` 源时，一轮改为**每批一次**
  RSS 请求取这些源（`/{u1,u2,...}/rss`，每批路径长度控制在 250 字符内），再按作者拆分 feed，而不是
  逐源抓取。合并 feed 无法表示转推（Nitter 会把转推条目归到原作者名下），因此仅在过滤掉转推时启用；
  `fetch_backend = "fx"` 从不合并，`"mix"` 下这些源改由 Nitter 提供而非快车道。某批请求失败不会丢源：
  受影响的源回退到各自的抓取路径。
  ([e0ac167](https://github.com/shitianyaa/nitter-cli/commit/e0ac167))

## 变更

- **状态存储错误与其他错误采用同一种操作名写法**：`watch`、`seen list` 与 `seen clear` 在状态文件
  损坏或不可读时，报错前缀是散文式短语（`load seen state: …`），而其他所有已分类错误都用包限定形式。
  现改为 `seen.Load: …`（写路径为 `seen.Save: …`）。Kind、退出码与状态文件语义均不变。
  ([cf050f5](https://github.com/shitianyaa/nitter-cli/commit/cf050f5))
- **`mix` 取数路由的文档改为与实测一致**：FAQ 此前把 `comments`、`following`、`profile`、`quotes`、
  `trends` 与 `search --type user` 也算进「失败时回退到自建实例」，并把 `nitter` 模式描述为「全部取数
  自托管」。这两条都不成立——这六个命令没有 Nitter 对应端点，始终访问 `api.fxtwitter.com`，而
  `fetch_backend` 只路由时间线与状态两类取数。文档现在直接说明这条分界。
  ([b526158](https://github.com/shitianyaa/nitter-cli/commit/b526158))

## 修复

- **Nitter RSS 取数改为沿 `Min-Id` 游标翻页，不再只读一页**：积压跨越多页的时间线此前会被静默截断——
  超过一页的 `--max-new` 无法满足，且首跑只记录第一页，更早的推文永远不会被推送。现在扫描会一直翻页，
  直到上游不再提供游标、`--max-pages` 预算耗尽，或某一页不再带来该源未见过的新推文。
  ([564bf1b](https://github.com/shitianyaa/nitter-cli/commit/564bf1b))
- **`nitter download` 把本地磁盘写失败报成本地错误**：写入目标文件失败（最典型是磁盘满）此前报
  `upstream_unavailable`，会让人去查实例；现在改为与同一路径上其它本地文件系统失败一致的
  `local_state_error`。退出码不变（两者都是 exit 1）。
  ([7b271b2](https://github.com/shitianyaa/nitter-cli/commit/7b271b2))

**完整变更**：[v0.7.1...v0.7.2](https://github.com/shitianyaa/nitter-cli/compare/v0.7.1...v0.7.2)
