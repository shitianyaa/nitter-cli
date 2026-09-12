# M8 媒体解析（twitter media）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `twitter` CLI 增加媒体解析能力——把一条推文解析成**可直接下载的媒体直链**（视频 mp4 变体、图片原链、GIF），复刻插件 `media_support` 已验证的三种解析方式；下载动作本身留给 Hermes（与整体设计一致：CLI 出数据，Agent 出动作）。

**Architecture:** 新增 `internal/media`（解析实现层：fx/vx/syndication JSON 客户端、xdown 页面解析、mp4/大小探测）+ `sdk` 只增类型（`MediaVariant`/`MediaResolution`）+ `twitter media` 命令（策略编排 + 输出模式）。策略顺序与插件一致：fx → vx → syndication → nitter → xdown。

**Tech Stack:** 既有栈（tls-client/httpx、goquery、pipeline、tweetfilter 模式）；无新依赖。

**Spec 依据:** `docs/superpowers/specs/2026-09-11-twitter-cli-spec.md` §1 非目标修订（媒体解析入列）+ 本文档；插件语义真源：`media_support/status_resolve.py`、`service.py:397-505`、`xdown.py`、`video_probe.py`。

## Global Constraints

- `sdk/` 只增不改；新增类型带 JSON tag（NDJSON 契约）与英文 GoDoc。
- 错误脱敏铁律沿用；第三方服务（fxtwitter/vxtwitter/syndication/xdown）的失败要**如实报告策略名**，不得静默换源伪装成功。
- 输出协议沿用 `twitter.pipeline/v1`；新增 kind `media`（只增）。
- 质量档位：`--quality high|medium|low`（视频按码率排名选变体：high=最高码率、medium=中位、low=最低非零；图片走 pbs `name=orig|large|small` 重写）。默认 high。
- 信任边界：fx/vx/syndication/xdown 是**第三方公共服务**（与自建 Nitter 不同），文档与 skill 必须写明「解析请求会把推文 URL 发给该服务」。
- 不做下载、不做缓存持久化（MVP）；`--probe` 的探测请求按需发起。

---

### Task 1: sdk 只增类型 + fx/vx/syndication JSON 客户端

**Files:**
- Modify: `sdk/models.go`（追加 `MediaVariant{URL string; Bitrate int64; ContentType string}`、`MediaResolution{Ref, Source, Kind, URL, FallbackURL, Label string; Width, Height int; DurationSeconds, SizeBytes float64; Variants []MediaVariant}`——字段名与 JSON tag 实现时按既有模型风格定稿）
- Create: `internal/media/fxtwitter.go`、`vxtwitter.go`、`syndication.go`、`resolve.go`、`testdata/*.json`
- Create: 对应 `*_test.go`

**Interfaces:**
- Produces:

```go
// internal/media (package media)
type Strategy string // "fx" | "vx" | "syndication" | "nitter" | "xdown"

type Resolver struct{ HTTP *httpx.Client; Now func() time.Time } // 复用既有传输（重试/节奏/代理）

// Resolve 按 strategy 拉取并投影；source 列表逐一尝试，全部失败返回最后一个分类错误（错误消息含各策略名，脱敏）。
func (r *Resolver) Resolve(ctx context.Context, ref twitter.StatusRef, opts Options) ([]twitter.MediaResolution, error)
// Options{ Strategies []Strategy; Quality string }

// 各 backend 投影要点（语义真源 status_resolve.py）：
// fx:  media.all[] → kind(photo/video/gif)；video 取 variants/video_info.variants 中 bitrate 最高者为主 URL
// vx:  media_extended[] → 直链；回退 mediaURLs/media_urls（按 image）
// syndication: photos[] → image；video.variants → 最高码率（"tweet_video_thumb"/gif 判定 dynamic）
// 共性: 图片 URL 过 pbs 质量重写（复用 mediaurl.RewritePBSOrig 的 name 参数变体）；同 URL 去重；http 明文直链丢弃
```

- [ ] **Step 1:** 从 `status_resolve.py` 抄三份真实形状 fixture（fx 带 video variants 多码率、vx media_extended、syndication photos+video）
- [ ] **Step 2:** 失败测试：各 backend 的 kind 判定、最高码率选择、质量重写、空 media/text 的 empty 语义、非法 payload → KindMalformed
- [ ] **Step 3:** 实现 → 全绿 → `gofmt/vet` → Commit `feat(media): fxtwitter vxtwitter syndication resolvers`

### Task 2: xdown 解析器

**Files:**
- Create: `internal/media/xdown.go`、`xdown_test.go`、`testdata/xdown_page.html`

**Interfaces:**
- Produces:

```go
// POST https://xdown.app/api/ajaxSearch  body: q=<status url>&lang=zh-cn
// headers: Referer/Origin xdown.app, Content-Type: application/x-www-form-urlencoded
// 响应 {status:"ok", data:"<html>"} → goquery 解析 a.tw-button-dl / a.abutton：
//   kind 由锚文本（mp4/video/gif/图片|image|photo）与 URL 后缀兜底判定（port XdownMediaParser._detect_kind）
//   snapcdn 代理 URL 的 token 参数 = base64url JSON → 解出 url 字段即 twimg 直链：
//     直链 https 开头 → 主 URL=直链（图片再过质量重写），fallback=snapcdn 全链
//     否则主 URL=snapcdn 全链
//   分辨率/时长从 label+URL+token payload 提取（port video_probe.extract_video_resolution/duration）
```

- [ ] **Step 1:** fixture（含 mp4 多清晰度按钮、gif、图片按钮、snapcdn token 链接、无法识别类型条目）
- [ ] **Step 2:** 失败测试 → 实现 → 全绿 → Commit `feat(media): xdown ajaxsearch parser with token extraction`

### Task 3: mp4 时长 / 大小探测（`--probe`）

**Files:**
- Create: `internal/media/probe.go`、`probe_test.go`

**Interfaces:**
- Produces: `Probe(ctx, url string) (DurationSeconds float64, SizeBytes int64, err error)`——时长：Range 请求抓 mp4 头部若干 KB，解析 mvhd box（port video_probe.find_mp4_duration/parse_mvhd_duration）；大小：`Content-Range: bytes 0-0/TOTAL`。两个探测各自失败互不影响（留零值）。仅 `--probe` 时调用（每条媒体多 1-2 个请求，文档写明）。
- [ ] TDD → Commit `feat(media): mp4 duration and content-range size probes`

### Task 4: `twitter media` 命令

**Files:**
- Create: `internal/cli/commands/media/media.go`、`media_test.go`、`internal/cli/result/media.go`
- Modify: `internal/cli/client/client.go`（Wiring 增加 media Resolver 构造；`--proxy` 沿用）、`internal/cli/pipeline/pipeline.go`（kind 增加 `media`）、`internal/cli/client/client.go` 或 commands 内复用 `ParseStatusRef`
- Modify: `docs/{en,zh-CN}/cli-reference.md`、`skills/twitter-cli/SKILL.md`（速查表 + 陷阱：第三方解析服务的隐私边界、视频优先于图片的选取规则）、skill 版本 →0.2.0、`changelog/unreleased/*`

**Interfaces:**

```
twitter media <REF>... [--strategy auto|fx|vx|syndication|nitter|xdown] [--quality high|medium|low] [--probe] [--json|--ndjson]
# auto 顺序: fx → vx → syndication → nitter(取 Nitter status 页 /video/ 链接) → xdown
# 输出: 每条媒体一个记录——人类=制表行；--json=单对象/数组；--ndjson=kind:"media" 信封
#       data: MediaResolution{ref, source(策略名), kind, url, fallback_url, label, width/height?, duration?, size?, variants?}
# 插件选取规则: 同一推文有视频/GIF 时跳过图片候选（日志说明 skipped 图片数）
# 退出码: 沿用 0/1/2；部分 REF 失败 → error 信封 + exit 1（javdb 批次模式）
```

- [ ] **Step 1:** 失败测试（httptest 假 fx 服务 + 假 Nitter status 页：auto 顺序、quality 三档变体选择、视频跳过图片、strategy 显式指定、批次部分失败 exit 1、`--probe` 触发探测请求）
- [ ] **Step 2:** 实现 → 全绿 → Commit `feat(cli): twitter media resolution command`
- [ ] **Step 3:** Skill/cli-reference/changelog 同步 → Commit `docs: media command in reference and skill`

### Task 5: 真实实例冒烟（用户配合）

- [ ] 用真实视频推文/GIF/多图推文各一条跑 `twitter media <url> --json`：确认 fx 直链可下载（curl -I 200）、`--probe` 时长/大小合理、xdown 兜底路径可达
- [ ] 对照插件同推文的解析结果（media_quality 三档各一次）
- [ ] 结论写回台账；skill 增补真实陷阱（如有）

## Self-Review 结论

- 插件三种方式全覆盖：fx/vx/syndication（Task 1）、xdown（Task 2）、Nitter /video/（Task 4 auto 链）；探测（Task 3）。
- 语义保真点：最高码率变体、视频跳过同推文图片、snapcdn token 提直链、质量三档、空 text+无 media 判 empty、全部失败报策略名清单。
- 与 MVP 架构一致：无新依赖、pipeline 只增 kind、sdk 只增类型、命令走 Wiring、边界规则不破。
