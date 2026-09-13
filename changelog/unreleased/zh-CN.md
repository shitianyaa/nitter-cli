# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- `seen list`/`seen clear` 新增 `--state-dir DIR`：两个子命令现在可以作用于 `watch --state-dir` 的目录，而不只是默认的 `~/.nitter-cli/state`（原先记录的不对称已消除；读取与清除不创建任何东西——不存在的目录就是空库）。
- `watch --once --json`：为单轮打印一个 JSON 文档 `{"tweets":[...裸 Tweet 对象...],"errors":[{"ref","code","message"}...]}`——选出的推文外加每个失败源一条记录（`code` 为 SDK 错误 Kind；有源失败仍退出 1）。不带 `--once` 的 `--json` 仍是用法错误（退出 2）：常驻循环是逐轮的流——信封流仍用 `--ndjson`。

## 变更

- `user --max-pages 0` 现在表示**不限页**：HTML 回退路径一直跟随 load-more 游标翻页，直至上游穷尽（失控运行由上下文取消兜底）；RSS 源天然单页，不受影响。不传 flag 仍应用配置 `max_pages` / 内置默认 5，`search`/`list` 保持旧的「0 = 用默认值」语义。

## 弃用

## 移除

## 修复

## 安全
