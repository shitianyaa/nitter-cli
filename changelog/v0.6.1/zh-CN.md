# v0.6.1 — 2026-09-13

文档与 agent 指引版本：无任何二进制行为变更。

## 新增

- agent 部署指引：新增 skill 参考（`skills/nitter-cli/references/deploy.md`），覆盖先问后动的流程——已有实例：按部署痕迹查找，并以 `instances test` 验证后才写入配置；没有实例：经用户明确同意后提供 Docker 部署——以及默认绑定 `127.0.0.1:8080` 的 Nitter Docker Compose 部署（上游 compose 文件、`redisHost` 一行改动、以及必需的真实账号 `sessions.jsonl`，含小号建议与凭证处理纪律），与运维注意（同机绑定、远程 CLI 的 SSH 隧道/反向代理桥接、账号封禁与停止函风险告知）。README 快速上手为无 agent 的读者链至上游 wiki 与社区自建指南。

## 修复

- 发布说明卫生：版本化 changelog 文件不再携带「未发布」草稿区标题，changelog 索引表补齐已发布版本行。GitHub Release 正文按本索引一贯的声明改为携带双语说明（英文在前、简体中文在后），此前两个版本的正文已同步补齐。
