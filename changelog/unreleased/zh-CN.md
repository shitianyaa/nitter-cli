# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

## 变更

- **watch 文档建议每个订阅类别使用各自的 `--state-dir`**：watch 去重状态在一个 `seen.json` 内以源键为键，
  因此两个类别订阅同一账号时会共用 `user:HANDLE`——先跑的那一轮把推文标记为已见，另一个类别就静默丢弃它
  （不报错，`--once` 仍退出 0）。CLI 参考与两份 README 现在建议给每个类别各自的
  `--state-dir ~/.nitter-cli/state/<category>`，并在该类别的每一行调度命令里都传同一个 flag，
  同时说明了并发写入的情形。
- **文档中的 NASA 示例推文现在确实属于 NASA**：两份 README、CLI 参考与 SDK 指南共用的示例 status ID
  实际解析到另一个账号的推文，照抄 `nitter get`/`nitter media`/`nitter download` 示例取到的并不是正文
  所称的 NASA 推文。所有示例现已改用真实存在的 NASA 推文。

## 弃用

## 移除

## 修复

- **`profile`、`following` 与 `search --type user` 报告真实发推数**：`tweets_count` 此前恒为 `0`，因为
  FxTwitter 的 profile 与 author 对象把该计数放在 `statuses` 键下，而解码器从未读取该键（只映射了遗留的
  `tweets_count`/`statuses_count` 写法）。解码器现在优先读取 `statuses`，因此名片卡的 `Tweets:` 行与所有
  `profile` 信封的 `tweets_count` 字段都携带数据源的真实计数。

## 安全
