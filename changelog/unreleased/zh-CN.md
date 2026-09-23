# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- **`nitter update` 可安装新版本**：显示版本对比后询问确认（`--confirm` 跳过询问；stdin 非终端时
  必须给出，因此绝不会阻塞管道；拒绝安装退出 0）。安装器只下载本平台归档，用发布的
  `checksums.txt` 校验 SHA-256，再校验暂存二进制报告的版本，全部通过后才替换可执行文件——
  任何校验失败都不会改动现有安装。`go install` 安装会被拒绝并给出对应的 `go install` 行而不做
  替换，避免后续安装静默抹掉更新。`--proxy`/`config.proxy` 现在对 update 的网络请求生效
  （此前不生效：检查用的是无代理的 client）。

## 变更

- **`nitter update` 不再只打印指引**：不带 `--check` 时它执行检查并提供安装。`nitter update --check`
  行为不变，仍是只读。
- **`nitter download` 报告输出目录**：在 stderr 输出一行 `note: writing to <dir>`（解析后的绝对路径），
  时机在目录创建之后、写入任何文件之前。stdout、`--json` 与 `--ndjson` 不受影响。

## 弃用

## 移除

## 修复

## 安全
