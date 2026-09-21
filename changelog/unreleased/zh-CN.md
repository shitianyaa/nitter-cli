# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- **FxTwitter API v2 混合抓取引擎**：支持 `fetch_backend` 策略（`mix` 默认 / `nitter` / `fx`），博主时间线与推文搜索默认优先走 Fx 极速免登通道，遇到网络异常或 SafeSearch 404 平滑回退自建 Nitter 实例池。
- **博主关注列表命令（`nitter following`）**：一键拉取指定用户关注账号画像（支持 TTY 格式化表格与管道模式 NDJSON `kind: "profile"` 信封）。
- **推文评论区与对话链命令（`nitter comments`）**：提取推文楼中楼与上下文 thread（支持 `--sort likes|recency`），便于提取博主自评隐藏链接、网盘车牌与追更连环长推/漫画串。
- **私人精选圈子管理（`nitter circle`）**：支持 `list`、`show`、`add` 与流式拉取整圈推文的 `run` 子命令，配置文件保存在 `~/.nitter-cli/circles.toml`。
- **`nitter user --with-replies`**：支持在时间线中包含用户自身发布的回复推文。
- **SDK 模型扩充**：`sdk` 包新增 `Profile` 与 `Conversation` 标准模型。

## 变更

- 标量配置键由 12 个扩充为 13 个，新增 `fetch_backend`，支持环境变量 `NITTER_FETCH_BACKEND`。
- 指定 `--instance` 时自动强制走 Nitter 实例，List 订阅严格物理隔离锁定自建 Nitter。

## 弃用

## 移除

## 修复

## 安全
