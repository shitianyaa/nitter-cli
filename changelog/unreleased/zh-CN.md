# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- 新增 `nitter download --clean-temp <DIR> [--older-than DURATION]`：显式维护模式，
  删除指定目录中超过时间窗口的文件（默认 `168h`；`0s` 表示全部），剪掉因此变空的
  子目录并在 stderr 报告。它不下载、也不会自动运行。

## 变更

## 弃用

## 移除

## 修复

## 安全
