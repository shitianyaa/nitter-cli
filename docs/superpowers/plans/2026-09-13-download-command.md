# M9 媒体下载(`nitter download`)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把下载半边接上,CLI 一体化:`nitter media` 出直链(M8 已有),新命令 `nitter download` 负责**解析 + 落盘**——推文 URL 进,本地文件出。对齐 pixiv-cli/javdb-cli 模板的 download 体验;随 v0.5.0 发布收编积压项(README 下载路线、changelog 归位、dist 产物撤出仓库)。

**Architecture:** 新增 `internal/media/downloader.go`(流式下载器,复用 httpx 传输,绕开 API 10MiB 响应上限)+ 选择层(纯函数:从 `[]MediaResolution` 规划要下的文件)+ `nitter download` 命令(批量/管道骨架复用 media)+ 配置第 10 个标量键 `download_path`。R11 接缝不动:命令层经 `internal/cli/client` 的窄接口拿下载器。

**Tech Stack:** 既有栈(httpx、pipeline、cobra、go-toml);无新依赖。

**Spec 依据:** `docs/superpowers/specs/2026-09-11-nitter-cli-spec.md` §非目标修订(「不做下载」在本版转正,以本文档为准);插件语义真源:`media_support/service.py`(`_normalize_media_candidates` 的视频压图片选择、`_download_media` 重试);模板参照:pixiv-cli `references/download.md`、javdb-cli download(不覆盖已存在文件)。

**实测依据(2026-09-13,11 条 NASA 真实推文):**
- 平台规则(X 官方文档):一条推文 ≤4 张图 **或** 1 个 GIF **或** 1 个视频;2022 起官方 App 支持混合媒体(≤4 项混发),第三方/Nitter 渲染不完整,属罕见 edge。
- fx/vx 干净:视频推恰好返回 1 个 video 条目。
- **xdown 混浊:视频推返回 5 个不同码率的 video 条目 + 1 张封面图**——插件的「有视频就丢图片」正是对冲此伪影,下载选择语义必须沿用。
- Nitter 时间线层把视频推标成 `['image']`(给的是封面缩略图):下载必须走解析器,不能直接吃时间线 media。

## Global Constraints

- `sdk/` 只增不改;新增类型带 JSON tag 与英文 GoDoc;输出协议 `nitter.pipeline/v1` 只增 kind `download`。
- 错误脱敏铁律沿用;第三方解析边界文案沿用(fx/vx/syndication/xdown 会收到推文 URL)。**scheme 政策按来源分流,下载器不做裁判**:第三方策略的产出已在解析层强制 https-only(直连 twimg CDN);**nitter 策略产出实例自身的 `/video/` 代理直链,可能为明文 http(用户自有设施,可信)——下载器以解析层产出的 URL 为准,不得重复执行 scheme 过滤,否则 nitter 私有路径的视频下载会全挂**。`--strategy nitter` = 解析+下载全程不出用户设施,文档/skill 明确这是隐私首选路径。
- 受限网络现实(2026-09-13 实测):**twimg CDN(video/pbs)与 syndication 直连不可达,须经代理**——既有 `--proxy` 与配置 `proxy` 对解析和下载全程生效,skill/README 写明;syndication 策略定性为「代理可用」,auto 链对其失败如实报告并跳过。
- **下载选择语义(实测背书,插件同款)**:同一 ref 的解析结果中,有 video/gif → 收敛为**一个**文件(多条目按 `--quality` 选一:high=最高码率/分辨率,medium=中位,low=最低;主 URL 失败依次落 `fallback_url` → `variants`,全部失败该条目报错);无 video/gif → 下**全部**图片(≤4)。`--kind image|video|gif|cover` 为前置筛选。与 `media` 的「全返回」哲学不冲突:media 是信息视图,download 是动作视图;混合媒体 edge(官方 App 发的)按视频赢处理,文档注明。
- **封面是一等公民**:解析层为 video/gif 条目新增 `CoverURL`(sdk additive 字段),三个源都有现成数据——fx `thumbnail_url`、xdown 的封面图条目、syndication 的 `tweet_video_thumb` 变体。xdown 混进来的封面伪影由此转为资产;`--kind cover` 只下封面(文件名 `<status-id>-cover.<ext>`),纯图推的 CoverURL 为空、`--kind cover` 对其报 not_found 语义错误。
- **从不静默覆盖**:`--on-exists refuse|skip|overwrite`(默认 refuse = javdb 同款安全边界:已存在 → 该条目明确报错;`skip` 供管道幂等重跑;`overwrite` 取代早先的 `--force` 设计,单旗标三态更干净)。
- 文件名 `<status-id>-<seq>.<ext>`:ext 由 URL 路径 / Content-Type 推导实际容器(gif 在 twimg 是 mp4 就叫 `.mp4`);seq 按 ref 内去重后的顺序 1..N。文件名模板(`{author}`/`{date}` 等)**缓办**——解析层不返回 author/date,模板缺数据源,待后续需要再加。
- 落盘原子性:同目录临时文件 + rename;带 Content-Length 则校验,不符 = 失败并删除残留。
- 动作型命令的报告**分歧于 pixiv**(pixiv 成功 stdout 为空):我们延续自家 NDJSON 结果流,每条产出一条结果记录(path/bytes/sha256),因为消费方是 agent,需要程序化结果——文档写明这是有意分歧。
- skill 操作分级新增「本地媒体写入」档:每次确认,授权不延续(pixiv Disk 档同款)。
- 不做:并行下载、断点续传、跨运行去重/缓存、`--on-error`(沿用自家批量语义:条目失败继续、部分失败退 1)。

---

### Task 1: httpx 流式下载 + `internal/media/downloader.go`

**Files:**
- Modify: `internal/nitter/protocol/httpx/client.go`(新增流式 GET 方法,不动既有 `Get` 的 MaxBodyBytes 语义)、`httpx_test.go`
- Create: `internal/media/downloader.go`、`downloader_test.go`

**Interfaces:**

```go
// httpx 新增(流式,不受 MaxBodyBytes 约束——审查点:两个路径不得混淆):
func (c *Client) Download(ctx context.Context, url string, w io.Writer, headers map[string]string) (written int64, err error)
//   非约 2xx → 分类错误(rate_limited/unavailable,...);429 Retry-After 沿用;上下文取消中途停止并清理
//   契约:完整 GET,200 + Content-Length 校验;防御性兼容 206(取 Content-Range 总长;
//   实测 twimg 对 Range 请求回 206 且 Content-Length=区间长)

// internal/media (package media)
type DownloadResult struct {
    URL    string // 实际成功的那条 URL(可能是 fallback)
    Bytes  int64
    SHA256 string // 流式顺手计算
}
// 下载到 dir 下的临时文件,成功后 rename 为 final;final 已存在 → 返回明确的 exists 错误(命令层决定 --force 行为)
func (d *Downloader) FetchToFile(ctx context.Context, url, finalPath string, force bool) (DownloadResult, error)
```

- [ ] **Step 1:** 失败测试(httptest 假 CDN):分块流式写入、Content-Length 不符报错并清理、非 2xx 分类、429 一次重试、临时文件残留清理、exists 且非 force、force 覆写、上下文取消
- [ ] **Step 2:** 实现 → 全绿 → `gofmt/vet` → Commit `feat(media): streaming downloader with atomic writes`

### Task 2: 选择/规划层(纯函数)

**Files:**
- Modify: `sdk/models.go`(`MediaResolution.CoverURL string \`json:"cover_url,omitempty\"\``——additive)、`internal/media/fxtwitter.go`(暴露 `thumbnail_url`,顺带 item 级 `duration`/`width`/`height` 入库——fx payload 实测自带)、`xdown.go`(视频推的封面图条目捕获为 CoverURL,不再作为 image 条目抛给选择层)、`syndication.go`(**`video.poster` 为封面**(实测字段),旧形状 `tweet_video_thumb` 兜底;`durationMs` → DurationSeconds)、`nitter.go`(status 页若带视频海报则一并捕获)、`resolve.go`(封面字段透传)、对应 `*_test.go`
- Create: `internal/media/selection.go`、`selection_test.go`(fixture 直接采用 2026-09-13 实测形状:xdown 视频推 5 变体 + 封面图、fx 单 video、4 图推)

**Interfaces:**

```go
type PlannedFile struct { StatusID, Seq string; Kind, Ext, URL string; Fallbacks []string }
// 输入:某 ref 的 []MediaResolution(单一 winning strategy 已定)+ quality + kindFilter
// 规则:video/gif 存在 → 按 quality 从候选(主 URL + 其余变体/其他条目按码率/分辨率标签)收敛 1 个,
//       备选链 [main, fallback_url, variants..., 其他条目按档位];无 video → 全部 image 依序 Seq=1..N
// kindFilter=cover → 视频/GIF 推计划 CoverURL 单文件(无 CoverURL → 明确错误);纯图推 → not_found 语义错误
// 非直链变体过滤:syndication variants 实测含 m3u8(HLS 播放列表)——播放列表类不进入候选,只留 mp4/图片直链
// ext 推导:URL 路径后缀优先,缺省按 kind(image→jpg 兜底?否——Content-Type 运行时定,计划期 ext 允许为空,落盘时以响应头终判)
func PlanDownload(refID string, res []twitter.MediaResolution, quality, kindFilter string) ([]PlannedFile, error)
```

- [ ] **Step 1:** 失败测试:三源封面提取(fx/xdown/syndication fixture 各一)、视频推收敛(变体排序、kindFilter=video/gif/image/cover、纯图推全下、cover 对纯图推报错、空解析 → error、http 链剔除沿用 M8 规则)
- [ ] **Step 2:** 实现 → 全绿 → Commit `feat(media): cover extraction and download selection`

### Task 3: 配置键 `download_path`(第 10 个标量键)

**Files:**
- Modify: `internal/config/settings/settings.go`、`baseline.go`、`document.go`、`settings_test.go`;`internal/cli/commands/config/config.go`(键表与 help);`docs/{en,zh-CN}/cli-reference.md` 配置节
- 语义:默认 `./nitter-media`(cwd 相对,与 pixiv 同款并文档写明);`--output DIR` 是 per-invocation 覆盖(flag > config,沿用既有 precedence,不做 pixiv 的「两者必须一致」——我们只有一个拼法,无歧义);值不做存在性检查,运行时 mkdir -p。

- [ ] **Step 1:** 失败测试(默认值、set/unset、effective get、baseline 注释含示例)
- [ ] **Step 2:** 实现 → 全绿 → Commit `feat(config): download_path key`

### Task 4: `nitter download` 命令

**Files:**
- Modify: `internal/cli/client/client.go`(窄接口 `MediaDownloader` + wiring)、`client_test.go`;`internal/cli/pipeline/pipeline.go`(additive kind `download`)
- Create: `internal/cli/commands/download/download.go`、`download_test.go`

**Interfaces:**

```text
nitter download <REF>... [--output DIR] [--kind image|video|gif|cover] [--quality high|medium|low]
                         [--strategy auto|fx|vx|syndication|nitter|xdown]
                         [--on-exists refuse|skip|overwrite] [--json|--ndjson]
```

- REF 形状同 `get`/`media`(裸 ID / x.com / twitter.com / nitter URL,含 /photo/N、/video/1 后缀);批量;stdin 无位置参数且非 TTY 时读入:**首字节 `{` 按严格 NDJSON tweet 信封解析取其 ref(记录模式,pixiv 同构),否则按纯行 refs**;两种都给 = 歧义错误退 2。
- 每条 ref:解析(auto 链语义与 media 相同,`source` 记入结果)→ PlanDownload → 逐文件 FetchToFile。
- 输出:默认文本一行一条 `<ref> <path> <bytes> <kind> <source>`;`--ndjson` 每文件一条 `kind:"download"` 信封(`id`=落盘绝对路径,`meta.input`=raw ref)+ 每失败 ref 一条就地 error 信封;`--json` 单条对象/数组/`[]`。**部分失败 exit 1**(汇总行 `download completed with N of M refs failed`),EPIPE 退 0,坏 flag/歧义/坏 ref 退 2。
- 信任边界文案进 Long help(第三方解析 + 只下公开推文;`--strategy nitter` 是解析+下载全程不出用户设施的隐私路径,其直链可能为实例的明文 http,属可信来源)。

- [ ] **Step 1:** 失败测试(httptest 全链):单视频推(含 fallback 链)、四图推、`--kind cover` 单文件与纯图推报错、`--on-exists` 三态(refuse/skip/overwrite)、stdin 信封模式与纯行模式、部分失败退出码、ndjson 信封形状(只增契约测试进 `sdk/contract_external_test.go` 相邻的 pipeline 测试)
- [ ] **Step 2:** 实现 → 全绿 → `gofmt/vet` → Commit `feat(cli): nitter download command`

### Task 5: skill + 文档

**Files:**
- Modify: `skills/nitter-cli/SKILL.md`(分级表加「本地媒体写入」档=每次确认;硬规则加一条;速查表加 download 行;版本 → 0.5.0)、`skills/nitter-cli/references/media.md`(交付段改为指向 download.md)
- Create: `skills/nitter-cli/references/download.md`(仿 pixiv:预下载清单、语义表、选择规则与实测依据、管道示例 `nitter watch user:X --once --ndjson | nitter download --ndjson`、报告方式)
- Modify: `README.md`/`README.zh-CN.md`(Quick start 第 1 步改为**Releases 下载为推荐路线 + 源码构建备选**,版本示例 → 0.5.0,特性清单加 download);`docs/{en,zh-CN}/cli-reference.md`(download 章节);`docs/en+zh sdk.md`(DownloadResult);`changelog/unreleased/{en,zh-CN}.md`(M9 条目)

- [ ] **Step 1:** 文档落地 → skill 交叉自检(tier 表/速查/references 三处一致) → Commit `docs: download command in skill, reference and readme`

### Task 6: 发布 v0.5.0(收编积压项)

- [ ] **Step 1:** `git rm` dist/ 两文件(产物不入库,改走 Release);changelog/unreleased → `changelog/v0.5.0/{en,zh-CN}.md`,unreleased 恢复空模板;Commit `chore(release): prepare v0.5.0`
- [ ] **Step 2:** 推 main + 打 tag `v0.5.0` → release workflow 六平台构建出 draft(不要手工抢先建 release,CI 的 `gh release create --draft` 会撞车)
- [ ] **Step 3:** CI 绿后验资(`gh release view`,六平台 archive + 合并 checksums.txt)→ `gh release edit v0.5.0 --draft=false` 发布
- [ ] **Step 4:** 实测冒烟(本地或 VPS 新版本):四图推 → 4 文件、视频推 → 1 文件(xdown 路径验证封面不入盘)、存在重跑 → 拒绝、`--force` 覆写;GIF 样本遇有则补(至今未采样,M8 起挂账)
- [ ] **Step 5:** 台账收尾 + 测试文件报备单(目标:空——全程管道与 t.TempDir,仓库/VPS 不留文件)

## Self-Review 结论

- 与 M8 一致性:选择层输入就是 M8 的 `MediaResolution`,无新解析逻辑;批量/退出码/信封语义与 media 完全同构。
- 与 pixiv 对齐项:download_path、预下载清单、stdin 记录模式(首字节 `{` 判定)、独立 download reference、「下载别掐表」;有意分歧:成功也出结果流(agent 消费)、无 --on-error、无文件名模板(数据源缺失,已注明)。
- 与 javdb 对齐项:从不覆盖 + --force、本地写入档逐次授权。
- 风险:私有库 Release 资产下载需凭据(Hermes 用 gh auth 或 token curl,安装话术已含);xdown 对同一视频的条目数/标签措辞可能随站点改版漂移——选择层以「码率/分辨率标签 + 变体排序」收敛,不写死条目数。
