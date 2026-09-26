# v0.9.0 — 2026-09-26

新增 `followers`、`thread`、`typeahead` 三个命令与 `circle remove`，并修正 FxTwitter 车道契约、`update` 失败保证与产品 skill 的升级路径。

## 新增

- **`nitter followers <HANDLE>`**：`following` 的镜像能力——拉取关注某 handle（1–15 个字母、数字或
  下划线，不带 `@`）的账号，每个粉丝档案一行（`@<handle>  <name>  <followers_count>  <bio>`）。
  `--limit N` 限制档案条数（默认 20，必须 ≥ 1）；`--json`/`--ndjson` 遵循统一输出模式。该路由无论
  `fetch_backend` 怎么设都恒走 `api.fxtwitter.com`；`--instance` 不适用。
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`nitter thread <TWEET_ID>`**：拉取包含某条推文的完整自帖串并从主楼开始输出（每条状态一行：
  `<ID>  <YYYY-MM-DD HH:MM>  @<handle>  <单行文本>`）。ID 必须是纯数字 status ID（1–20 位数字；URL
  等其他引用形态归 `get`）；不属于任何串的推文按 `not_found` 应答（退出 1）。该路由恒走
  `api.fxtwitter.com`；`--instance` 不适用。
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`nitter typeahead <QUERY>`**：经 user-completion 端点做账号名补全。这是补全查询，不是搜索
  （completion, NOT search）：无查询算子，上游最多应答约 10–20 个账号，且不可翻页——真正的查询请用
  `search`。`--limit N` 只限制打印条数（默认 20，必须 ≥ 1；上游可能给得更少）。该路由恒走
  `api.fxtwitter.com`；`--instance` 不适用。
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`nitter circle remove <NAME> <HANDLE>`**：从圈子移除一个 handle，只编辑 `circles.toml` 中该圈子的
  users 数组。档案侧写文件（`~/.nitter-cli/profiles.toml`，含已记录的 `role`/`note`）从不被读取或写入，
  因此移除成员会保留其缓存档案。幂等：移除非成员 handle 时在 stderr 打印明确的
  `not a member; nothing changed` 提示并退出 0。
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))

## 变更

- **`circle add` 现在会拉取新成员的档案事实**：名册写入成功且该成员确实是新增的情况下，该 handle 会被
  发送到 `api.fxtwitter.com` 做一次尽力而为的档案拉取，事实字段（`name`、`bio`、`followers_count`、
  `fetched_at`）合并进侧写文件，`circle show` 无需等待 `circle refresh` 即可渲染新成员。
  判断字段（`role`/`note`/`noted_at`）绝不被触碰。该路径上的任何失败都只是 stderr 一行警告：
  名册写入已成功，命令仍退出 0，事实随下一次 `circle refresh` 到位。
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **其余四个 FxTwitter 车道函数现在与时间线函数对执行同一套参数契约**：`SearchTweets`、`SearchUsers`、
  `FetchUserFollowing` 与 `FetchQuotes` 现在会对空 query/handle 与非正数 count/limit 返回
  `invalid_argument`，而不是静默回退到上游页大小、丢弃 limit 参数或返回空成功。CLI 从未触达过这些路径
  （它会先以退出 2 拒绝 `--limit < 1`），因此这只是对齐车道契约，没有用户可见的行为变化。
  ([#13](https://github.com/shitianyaa/nitter-cli/pull/13))
- **`nitter update` 的失败保证现在按它实际覆盖的范围表述**：帮助文本与 CLI reference 原先写「任何失败都不会改动现有安装」，但最终替换动作并不在这条保证内——Windows 上 `ReplaceFileW` 不是原子的，可能在把旧二进制改名到一旁之后失败。现在措辞把保证限定为替换动作之前的失败，并提示重试前先核实已安装的二进制。行为无变化。
  ([#15](https://github.com/shitianyaa/nitter-cli/pull/15))
- **CLI reference、README 与产品 skill 现在把 `nitter update` 作为受支持的升级路径**：skill 不再
  把 `update` 称作它自己禁止的安装器；升级步骤会在替换二进制之前先落实 Skill 的 tag 与授权；
  二进制与 skill 被写明为**一对**版本。
  ([#15](https://github.com/shitianyaa/nitter-cli/pull/15))

## 修复

- **`circle add` 现在把名册写回实际匹配到的 section key**：当某个圈子的内层 `key` 字段与它的
  section 名不同时，添加成员不再产生第二个 section —— 成员会写进真正被匹配到的那个圈子，
  且重复添加不再触发档案拉取。
  ([#14](https://github.com/shitianyaa/nitter-cli/pull/14))
- **`mix` 不再用 `no instances configured` 覆盖快车道的失败原因**：当 FxTwitter 尝试失败、而实例路径又
  无从应答（未配置实例，或全部在冷却中）时，用户看到的错误只剩选择器的答复。现在选择器错误会携带快车道
  的真因，例如 `chooser: upstream_unavailable: no instances configured (fx attempt:
  fxtwitter.SearchTweets: not_found: resource not found (404): /2/search)`。真实的实例失败仍按原样上报；
  `fetch_backend = fx`（从不回退）不受影响。
  ([#13](https://github.com/shitianyaa/nitter-cli/pull/13))

**Full Changelog**: [v0.8.0...v0.9.0](https://github.com/shitianyaa/nitter-cli/compare/v0.8.0...v0.9.0)
