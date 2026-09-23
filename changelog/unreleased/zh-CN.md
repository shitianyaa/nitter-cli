# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

## 变更

- **状态存储错误与其他错误采用同一种操作名写法**：`watch`、`seen list` 与 `seen clear` 在状态文件
  损坏或不可读时，报错前缀是散文式短语（`load seen state: …`），而其他所有已分类错误都用包限定形式。
  现改为 `seen.Load: …`（写路径为 `seen.Save: …`）。Kind、退出码与状态文件语义均不变。

## 弃用

## 移除

## 修复

## 安全
