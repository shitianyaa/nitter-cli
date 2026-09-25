# 未发布

> 此处是可选的人工草稿区，发布 workflow 不会读取本文件；创建 release-prep PR 前，
> 请将最终双语说明放入目标 `changelog/vX.Y.Z/` 目录。

## 新增

## 变更

- **其余四个 FxTwitter 车道函数现在与时间线函数对执行同一套参数契约**：`SearchTweets`、`SearchUsers`、
  `FetchUserFollowing` 与 `FetchQuotes` 现在会对空 query/handle 与非正数 count/limit 返回
  `invalid_argument`，而不是静默回退到上游页大小、丢弃 limit 参数或返回空成功。CLI 从未触达过这些路径
  （它会先以退出 2 拒绝 `--limit < 1`），因此这只是对齐 SDK 契约，没有用户可见的行为变化。

## 弃用

## 移除

## 修复

- **`mix` 不再用 `no instances configured` 覆盖快车道的失败原因**：当 FxTwitter 尝试失败、而实例路径又
  无从应答（未配置实例，或全部在冷却中）时，用户看到的错误只剩选择器的答复。现在选择器错误会携带快车道
  的真因，例如 `chooser: upstream_unavailable: no instances configured (fx attempt:
  fxtwitter.SearchTweets: not_found: resource not found (404))`。真实的实例失败仍按原样上报；
  `fetch_backend = fx`（从不回退）不受影响。

## 安全
