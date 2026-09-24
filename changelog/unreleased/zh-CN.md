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

## 弃用

## 移除

## 修复

- **`user --limit 0` 与 `circle run --limit 0` 在 Fx/mix 后端下不再静默返回空**：CLI 契约中 `--limit 0`
  代表全量（all），但底层 FxTwitter 客户端此前把 `count <= 0` 视作获取 0 条推文并返回成功空结果。
  现于 wiring 适配层将 `limit <= 0` 映射为全量哨兵，使 FxTwitter 能按分页预算正常拉取全部推文。同时 Fx
  真正支持 `--max-pages 0` 无界翻页（未指定时默认对齐 5 页上限）并补充游标防回环守卫。
- **`get`、`media`、`download`：位置参数优先于标准输入**——仅当未提供位置参数**且** stdin 非 TTY 时
  才读取 stdin。此前命令会先读 stdin，再以歧义错误拒绝输入（`status reference given both as an
  argument and on stdin`，退出 2）；当管道的写端保持打开时，这次读取会永久阻塞，导致管道中的
  `nitter get <REF>`（以及带位置参数的 `media`/`download`）挂起而非执行。歧义报错及其退出 2 分支已彻底
  移除；输入只需给出一种方式，两种同时给出不再报错。显式传入的空位置参数
  （`nitter get ""`）是用法错误（退出 2），绝不回退读取 stdin。

## 安全
