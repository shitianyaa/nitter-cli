# v0.9.1 — 2026-10-25

支持在只读配置目录中读取已有配置；操作 Skill 澄清实例维护授权、下载输入与限流处理指引。

## 变更

- **操作 Skill 澄清**：经用户明确授权的实例维护只读改任务所需的最小配置；`config get`/`config set` 仅管理标量键（`[[instances]]` 仍为手工编辑的 TOML）；`/pic/` 链接与图片/视频直链不是下载输入；仅在确有实例健康判定需要时探测，HTTP 429 属限流而非配置错误。
  ([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

## 修复

- **只读配置目录下已有配置可用**：配置文件已存在时，初始化直接返回、不再创建暂存临时文件，`config path`/`config get` 在只读目录下正常工作。首次初始化仍为原子操作且绝不覆盖已有配置；写命令仍需目录可写，暂存失败会明确报告。
  ([#23](https://github.com/shitianyaa/nitter-cli/pull/23))

**Full Changelog**: [v0.9.0...v0.9.1](https://github.com/shitianyaa/nitter-cli/compare/v0.9.0...v0.9.1)
