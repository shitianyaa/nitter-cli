# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- `nitter download` 命名模板：`filename_template` 配置键（默认 `{id}-{seq}`，与既有行为逐字节一致）与 `directory_template`（默认空 = 平铺）通过占位符 `{id}`、`{seq}`、`{user}`、`{kind}`、`{ext}` 命名并放置下载文件；`--filename-template` 单次覆盖文件名模板。封面始终落盘为 `<id>-cover.<ext>`（模板只作用于普通文件），渲染名做安全清洗（Windows 非法字符替换为 `_`），同一 ref 内的渲染名碰撞获得 `-2`、`-3`… 后缀，非法模板以一条 stderr 警告回退默认/平铺而不使运行失败（pixiv 语义）。config 命令现在管理十二个标量键。
- `seen list`/`seen clear` 新增 `--state-dir DIR`：两个子命令现在可以作用于 `watch --state-dir` 的目录，而不只是默认的 `~/.nitter-cli/state`（原先记录的不对称已消除；读取与清除不创建任何东西——不存在的目录就是空库）。
- `watch --once --json`：为单轮打印一个 JSON 文档 `{"tweets":[...裸 Tweet 对象...],"errors":[{"ref","code","message"}...]}`——选出的推文外加每个失败源一条记录（`code` 为 SDK 错误 Kind；有源失败仍退出 1）。不带 `--once` 的 `--json` 仍是用法错误（退出 2）：常驻循环是逐轮的流——信封流仍用 `--ndjson`。
- `watch --max-new-overflow keep|drop`（默认 `drop`，即此前的静默丢失行为）：`keep` 让超出 `--max-new` 上限的新推文保持未标记，由后续各轮在同一上限下重新推送（宁重勿丢——突发量超过两倍上限时需多轮才能排空）；`keep` 搭配 `--max-new 0` 仍将当前首页封存为新基准。其他取值是用法错误（退出 2）。

## 变更

- 实例 basic 认证：`[[instances]]` 条目**同时**设置 `username` 与 `password` 时，发往该实例的请求现在会携带 HTTP basic 认证（在传输层内按主机限定）。凭证只会附着在发往其所属实例的请求上——第三方媒体端点永远看不到它——也不会进入错误、日志或响应；只设置一半的凭证对视为未配置。单次的 `--instance URL` 覆盖是纯 URL，不携带凭证（已记录的局限）。
- 数据命令在 stdout 为管道时默认输出 NDJSON：`user`、`search`、`list`、`get`、`media`、`download` 与 `instances test` 在 stdout 非 TTY 且未显式给出 `--json`/`--ndjson` 时，现在输出 `nitter.pipeline/v1` 信封，`nitter search "..." | nitter download` 无需任何 flag 即可直连。显式 flag 永远优先；TTY 下默认仍是人类表格；管道下的空结果完全静默（无 `(empty)` 提示）。`watch` 在管道下保持文本默认（信封流请传 `--ndjson`）；`config`、`seen`、`update` 不变。
- `user --max-pages 0` 现在表示**不限页**：HTML 回退路径一直跟随 load-more 游标翻页，直至上游穷尽（失控运行由上下文取消兜底）；RSS 源天然单页，不受影响。不传 flag 仍应用配置 `max_pages` / 内置默认 5，`search`/`list` 保持旧的「0 = 用默认值」语义。

## 弃用

## 移除

## 修复

## 安全
