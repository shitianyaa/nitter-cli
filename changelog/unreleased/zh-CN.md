# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

- `nitter` CLI：经你自己的 Nitter 实例抓取公开推文——`nitter user`（RSS 优先、
  HTML 回退）、`nitter search`、`nitter list` 与 `nitter get`（按 ID 或 URL
  取单条），带实例轮换、冷却与请求节奏。
- `nitter watch`：对 `user:`/`tag:`/`list:` 源做持久化去重轮询，支持 `--once`
  调度器模式、每源去重状态（300 条已见 ID + 水位 20）、`--max-new` 输出上限与
  逐源错误报告。
- `nitter instances test`：逐实例探测能力（RSS / 用户 HTML / 搜索 / List），
  提供人类、`--json` 与 `--ndjson` 三种报告。
- `nitter config path/get/set/unset`：管理九个标量配置键，支持环境变量覆盖
  （`NITTER_DEFAULT_LIMIT`、`NITTER_LOG_LEVEL`、`NITTER_LOG_FORMAT`）、0600
  原子写入与首跑自动生成基线配置。
- `nitter seen list/clear`：检视与清除 watch 去重状态（`seen list --json` 恒为
  数组；`seen clear` 每次都需要 `--confirm`）。
- 所有数据命令的 `--json` / `--ndjson` 输出模式，基于稳定的
  `nitter.pipeline/v1` 信封协议（kind `tweet` / `instance_report` / `error`），
  包括 `watch` 的逐源就地错误信封与优雅 EPIPE 处理（stdout 管道被关闭按退出码
  0 处理）。
- 公开 Go SDK `github.com/shitianyaa/nitter-cli/sdk`（package `nitter`）：
  带实例轮换与冷却的 `Client`、窄 `Transport` 接口、分类错误 kind
  （`KindChallenge`、携带 `RetryAfter` 的 `KindRateLimited`、`KindUnavailable`
  等）与只增不改的数据模型（`Tweet`、`Author`、`Media`、`Quoted`、`Page`、
  `InstanceReport`）。
- 双语文档（English + 简体中文）：`docs/` 下的 README、CLI 参考、Go SDK 指南与
  维护者架构/开发指南。
- 随仓库分发的 Agent Skill（`skills/nitter-cli/`）：面向 AI agent 的操作规则、
  命令分级、速查表与故障排查。
- `nitter update` 支持 `--check [--prerelease] [--json]`：以严格 semver 将
  当前版本与 GitHub 最新发布版比较（`--prerelease` 感知预发布，恒排除草稿）；
  不带 `--check` 时打印包管理器 / 手动下载指引。MVP 不做自替换安装，开发构建
  直接跳过检查。
- 数据命令的字段级输出过滤：`user`/`search`/`list`/`watch` 新增
  `--no-reposts`、`--media-only` 与 `--media-type image|video|gif`（可自由组合；
  `--media-type` 值不合法为用法错误）。过滤在抓取之后应用——`watch` 中位于去重
  之前，被过滤的推文不会被记为已见，每轮重新抓取但不重复输出，`--max-new` 只
  统计通过过滤的推文。
- `nitter media <REF>...`：把推文解析成可直接下载的媒体直链（视频 mp4 变体、
  图片原图、GIF），走参考插件验证过的策略链——`--strategy auto`（fx → vx →
  syndication → nitter → xdown；首个产出媒体的策略胜出并在 `source` 中标明）
  或显式指定单一策略。`--quality high|medium|low` 对图片走 pbs 档位重写、决定
  主视频变体（保留全部变体）；`--probe` 对每条视频/GIF 追加一次尽力而为的
  Range 请求（mp4 时长 + Content-Range 大小；任何失败都让取值留空、绝不导致
  运行失败）。多 REF 批次输出就地错误信封，部分失败退出 1；`--json` 在恰好
  一条媒体时输出单个对象、否则为数组；`--ndjson` 以新增的 `media` kind（只增）
  流式输出。Nitter 读取用户自己的配置实例（其纯 http 链接按原样保留）；
  fx/vx/syndication/xdown 是会收到推文 URL 的第三方公共服务——已写入文档的
  信任边界，仅用于公开推文。

## 变更

- `watch` 与 `seen list` 的帮助文本现在使用 tag 源的原始写法（`tag:#AI`、
  `tag:from:nasa`）；预转义形式 `tag:%23AI` 会在网络上被二次转义，文档已明确
  其不合法。

## 弃用

## 移除

## 修复

## 安全
