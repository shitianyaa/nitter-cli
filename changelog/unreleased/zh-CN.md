# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

## 变更

- 澄清操作 Skill 中的实例维护授权、配置命令仅支持标量键，以及媒体下载需传入
  推文引用的限制。([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

## 弃用

## 移除

## 修复

- 读取只读目录中的已有配置时不再创建配置临时文件；首次初始化仍以原子方式
  创建配置，且不覆盖已有配置。([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

## 安全
