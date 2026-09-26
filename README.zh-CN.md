# nitter-cli

[English](README.md) · [简体中文](README.zh-CN.md) · [文档导航](docs/index.zh-CN.md)

<p><a href="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml"><img alt="ci" src="https://github.com/shitianyaa/nitter-cli/actions/workflows/ci.yml/badge.svg"></a> <a href="https://github.com/shitianyaa/nitter-cli/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/shitianyaa/nitter-cli?style=flat-square"></a> <a href="go.mod"><img alt="Go" src="https://img.shields.io/github/go-mod/go-version/shitianyaa/nitter-cli?style=flat-square"></a> <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/shitianyaa/nitter-cli?style=flat-square"></a></p>

[安装](#安装) · [快速上手](#60-秒快速上手) · [选择你的接口](#选择你的接口) ·
[文档](#文档)

`nitter` 是一个非官方的**公开推文**命令行客户端。数据来自 **FxTwitter 公共
API**（默认快车道——无需账号、无需凭证），并以**你自己部署的 Nitter 实例**作为
私有回退、List 数据通路，以及（逐次调用、经 `--instance URL`）时间线与单条状态
的实例路径。它抓取用户时间线、搜索结果、List 时间线、单条推文、对话评论区、
自帖串、关注与粉丝列表、博主名片、账号名补全、引用推文与实时热搜，支持带持久化
去重状态的持续监视与媒体
下载——一个为 agent 与调度器（cron、systemd timer、Hermes）打造的灵活 CLI，
以 NDJSON 消费。

它同时是一个公开 Go SDK（`github.com/shitianyaa/nitter-cli/sdk`，package
`nitter`），数据模型稳定、只增不改。

本项目建立在 **[Nitter](https://github.com/zedeus/nitter)**（对接的实例软件；
上游已于 2026 年 9 月归档，因此面向你自行运行的实例）与
**[FxTwitter](https://github.com/FxEmbed/FxEmbed)**（现以 FxEmbed 维护——快车道
背后的公开 API）之上。整体形态——双语文档、仓库内流程 skill、版本化 changelog
与发布矩阵——借鉴自 [FlanChanXwO](https://github.com/FlanChanXwO) 的
**[javdb-cli](https://github.com/FlanChanXwO/javdb-cli)** 与
**[pixiv-cli](https://github.com/FlanChanXwO/pixiv-cli)**。由
[shitianyaa](https://github.com/shitianyaa) 创建与维护。

## 为什么选择 nitter-cli？

- **FxTwitter 快车道 + Nitter 深度**——`fetch_backend` 决定时间线与状态取数的
  路由：`mix`（默认）优先尝试 FxTwitter 公共 API（无需账号、无需凭证），失败时
  回退到你自建的实例；`fx` 固定快车道。想让**某一次**调用只走自建实例，用
  `--instance URL`（测试手段，不是模式；仅 `user`/`search`/`get`/`list`）。
  `list` 始终需要 Nitter；`followers`/`thread`/`typeahead`/`comments`/`profile`/
  `following`/`quotes`/`trends` 与 `circle refresh` 则始终走 `api.fxtwitter.com`
  （无 Nitter 对应端点）。
- **灵活的实例策略**——指向任何一个你自主控制的 Nitter 实例：配置轮换集
  （`[[instances]]`，严格按配置顺序轮换，失败实例进入冷却），单次用
  `--instance` 覆盖，并可用主机级 basic auth 认证（`username` **和**
  `password` 同时设置）。凭证只会附着在发往其所属实例的请求上——第三方
  端点永远看不到它们。
- **公开推文检索**——`user`（RSS 优先，失败或空结果时回退 HTML 用户页）、
  `search`（默认最新优先，`--sort top` 取热门结果）、`list`，以及用 `get` 抓单条推文。
  `list` 严格隔离：所有 List
  抓取始终走你自己的实例（FxTwitter 没有 List 端点）。不内置实例、不登录、
  不绕过访问控制。
- **社交图谱与发现**——`following` 查看某账号关注了谁、`followers` 查看谁关注了它，
  `profile` 输出博主
  名片，`typeahead` 按前缀补全账号（是补全，不是搜索），`search --type user`
  按名字搜创作者/画师，`trends` 看 X 当下在聊
  什么，`quotes` 挖掘某条推文的引用二创——全部无需凭证。
- **对话与回复树**——`comments` 拉取某条推文的对话链与评论区（可按高赞或
  最新排序），`thread` 从主楼开始完整输出一条推文所在的自帖串——是找到博主
  自评隐藏链接、追更连环长推的最快路径。
- **创作者圈子**——`nitter circle` 在 `~/.nitter-cli/circles.toml` 维护主题
  花名册（`list` / `show` / `refresh` / `suggest` / `add` / `remove` / `run`）：`suggest` 从某博主的
  关注列表与转推原作者中挖出新候选成员，再按需发现与管道流式拉取，与调度
  型的 `watch` 订阅各司其职。
- **可组合的管道**——stdout 是管道时，数据命令自动输出 `nitter.pipeline/v1`
  NDJSON，`nitter search "..." | nitter download` 无需任何 flag；`--json`
  提取完整文档，显式 flag 永远优先。
- **为调度器而生的 watch**——`watch --once` 对照持久化状态执行恰好一轮去重
  后退出；`--max-new-overflow keep` 策略让突发超限的推文在后续各轮重新推送，
  而不是被丢弃（宁重勿丢）。
- **带模板的媒体下载**——带视频的推文收敛为一个最佳文件（按码率排序，其余
  候选作回退），纯图推文下载全部图片，`--kind cover` 只取封面；
  `filename_template` / `directory_template` 配置键（外加
  `--filename-template`）通过 `{id}` `{seq}` `{user}` `{kind}` `{ext}` 占位符
  命名并放置文件。
- **可观测的实例**——`instances test` 默认逐实例探测 RSS 与用户时间线能力
  （`--full` 增加搜索探测，`--list-id ID` 增加 List 探测），每个实例一行报告；
  报告本身就是产品。
- **可管理的状态**——`config path/get/set/unset` 管理十三个标量键，
  `seen list/clear [--state-dir]` 管理 watch 去重状态；写入全部原子化，状态
  文件损坏是硬错误（绝不静默重置）。
- **诚实且经验证的更新**——`update --check [--prerelease] [--json]` 按严格 semver
  与 GitHub 最新发布版比较且不写任何东西；`update` 另外会在确认后提供安装，
  `--confirm` 则可跳过询问供脚本使用。`update` 在替换可执行文件前，用发布的
  `checksums.txt` 校验归档、并校验暂存二进制的版本，因此校验失败不会改动现有安装。
  `go install` 安装会被拒绝并给出对应的 `go install` 行；agent 不得在未获用户
  授权时执行 `update`。
- **公开 Go SDK**——类型化模型（`Tweet`、`Profile`、`Conversation`、`Trend`
  等）的 JSON 键永不消失、狭窄的 `Transport` 边界、脱敏的错误；CLI 命令消费
  的就是同一套接口。

## 安装

### Release 压缩包（推荐）

从 [GitHub Releases](https://github.com/shitianyaa/nitter-cli/releases)
下载对应平台的压缩包，并对照**同一发布版**附带的 `checksums.txt` 校验：

```bash
sha256sum -c checksums.txt --ignore-missing   # 或等价工具
```

把 `nitter` 二进制解压到一个已在 `PATH` 上的用户级目录。本项目没有安装脚本；
压缩包就是全部产品。

升级既有安装：先跑 `nitter update --check`，再跑 `nitter update` —— 不需要重新下载压缩包。

### 源码构建

需要 Go 1.27+：

```bash
sh scripts/build.sh          # 生成 ./nitter
./nitter --version           # nitter version <version>（或 dev 版本行）
```

### 让 AI Agent 安装

把下面这一段 prompt 复制给能够操作本机终端的 Codex、Claude Code、Cursor 或
其他 AI Agent：

```text
请为这台机器安装 https://github.com/shitianyaa/nitter-cli 的最新 stable 版本：只从仓库 Releases 页下载与检测到的操作系统、架构对应的官方 GitHub Release 压缩包，替换任何文件之前先对照该发布版附带的 checksums.txt 完成 SHA-256 校验，把 `nitter` 二进制安装到无需管理员或 root 权限的用户级目录，仅当 `nitter` 尚不可达时才把该目录加入当前用户的 PATH（报告每一处 PATH 变更），缺少任何前置工具时先征求同意，安装过程中绝不读取或输出 ~/.nitter-cli/config.toml 或任何 Nitter 凭证，最后运行 `nitter --version` 验证，并报告安装版本、二进制路径以及全部文件和 PATH 变更。

同时安装与该 stable 发布 tag 完全一致的 `nitter-cli` Skill（不要跟随 main）：把该 tag 的 git 树下完整的 skills/nitter-cli/ 目录安装到用户确认的 Agent skills 目录。不要猜测 skills 路径，也不要用 main 上的 skill 内容。
```

## 60 秒快速上手

nitter-cli 不内置实例，默认的 `mix` 模式开箱即用：`user`、`search`、`get`、
`comments`、`following`、`followers`、`thread`、`typeahead`、`profile`、
`quotes`、`trends` 与 `search --type user`
都会直接走 FxTwitter 的公共端点。配置一个你自控的 Nitter 实例可用于：`list`，
以及 Fx 不可用时的回退。还没有实例？用 Docker 自建
一个——参见[上游 wiki](https://github.com/zedeus/nitter/wiki) 或社区
[自建指南](https://github.com/sekai-soft/guide-nitter-self-hosting)；
AI agent 可按 skill 的 [deploy 参考](skills/nitter-cli/references/deploy.md)执行。

```bash
# 0. 配置你的实例——`nitter config path` 打印配置文件路径（首次真实命令会
#    生成带注释示例的文件），加入：
#      [[instances]]
#      url = "http://nitter.internal:8080"
#      # username = ""        # 可选 basic auth——两者都设置才会发送
#      # password = ""
nitter config path

# 诊断实例：能力探测，每实例一行（rss / user_html / search / list / latency；
# 单元格为 ok、fail(<原因>) 或 -）
nitter instances test
nitter instances test http://nitter.internal:8080 --full

# 抓取时间线，输出 JSON 给 Hermes 或任意 agent
nitter user NASA --limit 10 --json

# 发现类能力——无需任何实例或账号
nitter trends --limit 10 --json                   # X 当下在聊什么
nitter following NASA --limit 10                  # 某账号关注了谁
nitter followers NASA --limit 10                  # 谁关注了某账号
nitter profile NASA                               # 博主名片卡
nitter typeahead nas                              # 按前缀补全账号（是补全，不是搜索）
nitter comments 2100031016471818431 --limit 10    # 回复树（博主自评的隐藏链接就在这）
nitter thread <STATUS_ID> --json                  # 从主楼开始完整输出推文所在的自帖串
nitter quotes <STATUS_ID> --media-only | nitter download   # 引用推文衍生素材批量下载
nitter circle run ai_researchers --limit 1            # 流式拉取私人圈子花名册

# 管道输出无需 flag——数据命令自动输出 NDJSON，流可直接喂给下载命令
nitter search "#AI" --limit 20 | nitter download --output ./media

# 按热门排序而非最新优先（两个后端都会拿到该排序）
nitter search "#AI" --sort top --limit 10 --json

# 以最低画质档下载视频
nitter download https://x.com/NASA/status/2102761519985332442 --quality low

# 只取视频封面图
nitter download https://x.com/NASA/status/2102761519985332442 --kind cover

# 交给调度器做去重监视：每次调用一轮，只出新推文，状态跨次运行持久保存
*/10 * * * * nitter watch user:NASA tag:#AI --once --ndjson >> /var/log/nitter-watch.ndjson 2>>/tmp/nitter-watch.err
```

运行 `nitter --help` 或打开[完整 CLI 参考](docs/zh-CN/cli-reference.md)，
查看每条命令、flag、配置键与更新行为。

## 选择你的接口

### CLI

交互时用人类表格；`--json` 输出完整文档，`--ndjson` 输出记录流——或者直接
接管道：非 TTY 的 stdout 默认就是 NDJSON。

```bash
nitter user NASA --limit 10                      # TTY 下输出制表符行
nitter user NASA --limit 10 --json               # 单对象 / 数组
nitter user NASA --limit 10 --ndjson             # 每条推文一个 nitter.pipeline/v1 信封
nitter search "#AI" --sort top --limit 10 --json # 按热门排序
nitter search "#AI" --limit 20 | nitter download # 自动 NDJSON，无需 flag
```

### Go SDK

公开 SDK 是包 `github.com/shitianyaa/nitter-cli/sdk`（package 名 `nitter`）
——CLI 消费的就是同一套接口。它由轮换实例集、狭窄的 `Transport` 接口和实例
冷却时长组合出 `Client`；数据模型（`Tweet`、`Page[T]` 等）无条件序列化
（数据字段不用 `omitempty`），遵循只增不改的稳定性契约，错误经过脱敏
（无凭证、无查询串、无请求头/响应体）。

```go
import "github.com/shitianyaa/nitter-cli/sdk"

client, err := nitter.New(
    nitter.WithInstances([]nitter.Instance{
        {URL: "http://nitter.internal:8080"},
    }),
    nitter.WithHTTPClient(transport), // 实现 Transport 接口
    nitter.WithCooldown(30 * time.Second),
)
```

`nitter.Instance` 携带实例的可选 basic-auth 凭证对；脱敏契约保证它不出现在
任何错误与日志中。[SDK 指南](docs/zh-CN/sdk.md)记录了稳定性契约、模型、
分页与错误类型。

### Agent skill

`skills/nitter-cli/` 是给 AI agent 的操作 skill：命令路由、实例信任边界、需要
用户同意的状态变更命令、发现类配方与错误表。它随发布版一同分发，上面的安装
prompt 会把它装到你 agent 的 skills 目录。

## 配置

`~/.nitter-cli/config.toml`（TOML，权限 0600），首次真实命令时生成并附带注释
示例；Windows 上该目录位于你的用户目录下（`nitter config path` 会打印确切位置）。
优先级：CLI flag > 环境变量 > 文件 > 内置默认。
`nitter config get/set/unset` 读写十三个标量键；`[[instances]]` 与
`[[watch.sources]]` 数组表需手工编辑。[配置表](docs/zh-CN/cli-reference.md#nitter-config)
列出每个键的类型、默认值、环境变量覆盖与实例凭证规则。

```bash
nitter config path                        # 打印配置文件位置
nitter config set default_limit 50        # 状态变更命令：执行前先确认
nitter config get fetch_backend
```

## 文档

| 指南 | 用途 |
| --- | --- |
| [CLI 参考](docs/zh-CN/cli-reference.md) | 每条命令、flag、输出模式、退出码、watch 语义、配置键与常见问题 |
| [Agent 操作 skill](skills/nitter-cli/SKILL.md) | Agent 安全路由、实例信任、状态变更、发现与错误处理 |
| [Go SDK](docs/zh-CN/sdk.md) | 公开客户端、模型与选项 |
| [架构（简体中文）](docs/maintainers/architecture.md) | 包边界与运行时流程 |
| [开发（简体中文）](docs/maintainers/development.md) | 工具链、测试、平台构建、打包与发布 |
| [文档导航](docs/index.zh-CN.md) | 本地化的公开契约与维护者指南 |
| [贡献指南](CONTRIBUTING.zh-CN.md) | 本地质量门禁与贡献规则 |
| [更新日志](changelog/README.md) | 版本化双语发布说明 |

## 贡献

欢迎提交 bug 报告、文档修订、测试与聚焦的功能改动。提 PR 前请先读
[CONTRIBUTING.zh-CN.md](CONTRIBUTING.zh-CN.md)；较大的或涉及兼容性的改动请先
讨论。维护者向的契约（包边界、审查清单、文档路由）在
[docs/maintainers/](docs/maintainers/)。

## 免责声明

1. **仅限公开推文。** nitter-cli 只获取公开可访问的推文——经由公共 FxTwitter
   API（默认 `mix` / `fx` 后端）以及你自己运行的 Nitter 实例（回退路径、所有
   `list` 抓取，以及任何 `--instance` 调用）。它不内置实例、不登录、不提供任何
   绕过访问控制或解算挑战的能力。
2. **只配置你自主控制且信任的实例。** CLI 会跟随实例返回的媒体与重定向 URL。
   请把内部服务（Redis、云 metadata、管理后台）与 nitter-cli 及其实例的网络
   路径隔离。
3. **不绕过访问控制。** 页面需要登录、出现挑战或实例限流时，CLI 只上报分类后的
   错误，不做绕行。请遵守 X 的服务条款与实例承载能力；本项目与 X Corp. 及
   Nitter 项目均无关联。

## 许可证

[MIT](LICENSE) © shitianyaa
