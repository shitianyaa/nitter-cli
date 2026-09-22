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
- **实时热搜趋势命令（`nitter trends`）**：获取实时 Twitter/X 热门话题榜单（支持排名、话题名、上下文分类及发推数统计；TTY 格式化表格，管道模式输出 `kind: "trend"` NDJSON 信封）。
- **推文引用挖掘命令（`nitter quotes`）**：挖掘单条推文的二次创作与带图引用（支持 `--media-only`、`--no-reposts` 过滤，管道模式输出 `kind: "tweet"` NDJSON 信封）。
- **博主名片卡命令（`nitter profile`）**：获取博主完整个人主页资料卡（含 Handle、名称、Bio、关注/粉丝/推文/媒体计数及头像横幅直链；管道输出 `kind: "profile"` NDJSON 信封）。
- **推主与创作者搜索（`nitter search --type user`）**：支持在 `search` 命令中通过 `--type user` 搜索推主、画师及创作者账号画像。
- **单推抓取（`nitter get`）接入 Fx 快道**：在 `mix` 与 `fx` 模式下优先走 FxTwitter 极速通道，遇故障平滑回退 Nitter 实例。
- **SDK `Trend` 模型与 Protocol `KindTrend`**：新增 `Trend` 结构体及 `KindTrend = "trend"` 协议常量。
- **`nitter circle run --media-type`**：遍历圈子时按媒体类型过滤（`image|video|gif`，只保留携带至少一个该类型 media 的推文）——与 `user` 命令的 flag 对齐；非法值为用法错误（退出 2），先于任何网络。

## 变更

- 标量配置键由 12 个扩充为 13 个，新增 `fetch_backend`，支持环境变量 `NITTER_FETCH_BACKEND`。
- 指定 `--instance` 时自动强制走 Nitter 实例，List 订阅严格物理隔离锁定自建 Nitter。

## 弃用

## 移除

- **`vx` 与 `syndication` 媒体策略**：从 `media`/`download` 解析链中移除（双环境实测均不可用——vx 恒 403 challenge_required，syndication empty/not_found）。`--strategy auto` 链路改为 `fx → nitter → xdown`；显式指定 `vx`/`syndication` 为用法错误（退出 2）。

## 修复

## 安全
