# v0.9.0 — 2026-09-29

新增 `download --clean-temp` 维护模式，安全清理过期临时下载文件并修剪空子目录；完善创作者圈子参考文档与故障排查指引，并固化圈子错误退出码契约。

## 新增

- **`nitter download --clean-temp <DIR> [--older-than DURATION]`**：显式维护模式，删除指定目录中超过时间窗口的普通文件（默认 `168h`；`0s` 表示全部），修剪因此变空的子目录并在 stderr 输出清理统计。该模式不下载、不读 stdin、不创建目录；对根目录（如 `/`、`C:\`、UNC 共享根）及符号链接/Junction 根进行拦截防护（分别返回退出码 2 与 1），遍历过程中安全跳过子树内的符号链接且不引发失败；禁止与 REF 或下载专属 flag 混用（exit 2）。
  ([#20](https://github.com/shitianyaa/nitter-cli/pull/20))

## 变更

- **圈子文档路由与排障指引**：新增独立的创作者圈子参考手册（`skills/nitter-cli/references/circle.md`），在故障排查表中增补圈子告警与恢复说明，并固化圈子非法 handle 与执行失败的退出码契约测试。
  ([#19](https://github.com/shitianyaa/nitter-cli/pull/19))

**Full Changelog**: [v0.8.2...v0.9.0](https://github.com/shitianyaa/nitter-cli/compare/v0.8.2...v0.9.0)
