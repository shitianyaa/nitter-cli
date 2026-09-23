# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- **圈子成员侧写（`nitter circle refresh`）**：成员元数据改存机器可读的侧写文件
  （`~/.nitter-cli/profiles.toml`），以 handle 为键，分为机器刷新的事实字段
  （`name`、`bio`、`followers_count`、`fetched_at`）与刷新永不覆盖的判断字段
  （`role`、`note`、`noted_at`）。`circle refresh <NAME>` 拉取每个成员的 profile 并合并事实字段；
  单个成员失败时 stderr 警告并继续，其原有事实保留不变。

## 变更

- **`nitter circle show` 改为纯本地**：与缓存的侧写文件 join，零网络请求；因此 `--min-followers`
  改为过滤**缓存**的粉丝数，不再逐个成员拉取 profile。无缓存的成员保留行并以 `-` 占位，
  同时在 stderr 给出一条指向 `circle refresh` 的提示；`--json` 行新增 `name`、`bio`、`fetched_at`、
  `role`、`note`。先跑一次 `nitter circle refresh <NAME>` 填充缓存。

## 弃用

## 移除

## 修复

## 安全
