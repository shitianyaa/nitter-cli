# nitter Go SDK

[文档导航](../index.zh-CN.md) · [English](../en/sdk.md)

公开 SDK 是 Go 包 `github.com/shitianyaa/nitter-cli/sdk`（package 名
`nitter`）。它是本模块唯一的公开能力面：CLI 自身的命令消费它，外部 Go 调用方
同样可以使用。`internal/` 下的一切都是私有实现细节，不属于集成 API。

```bash
go get github.com/shitianyaa/nitter-cli
```

```go
import "github.com/shitianyaa/nitter-cli/sdk"
```

## 稳定性契约

- **只增不改。** 导出标识符、`Kind` 取值与所有模型的 JSON 键只能扩展——绝不
  删除、重命名或改作他用。
- **数据模型不用 `omitempty`。** `Tweet`/`Author`/`Media`/`Quoted`/`Page` 的
  每个字段无条件输出，消费方可以信赖每条 JSON 行的键一定存在；空值输出为零值
  JSON（来源没有媒体时 `media` 为 `null`）。
- **生产方绝不虚构数据。** 来源不携带的字段保持零值。
- **脱敏契约。** `*Error` 结构体以及经 `Unwrap` 可达的任何包装错误，都不得
  包含凭证、URL 查询串、请求头或响应体——只有稳定的 kind、操作名与简短的
  静态上下文。
- 序列化形状由 `sdk/models_test.go` 的 golden-JSON 测试钉死（连同
  `sdk/page.go` 的行为）；`Client`/`Chooser`/`Error` 的行为契约由其余
  `sdk/*_test.go` 钉死。

## Client

```go
client, err := nitter.New(
    nitter.WithInstances([]nitter.Instance{
        {URL: "http://nitter.internal:8080"},
    }),
    nitter.WithHTTPClient(transport), // 实现 Transport
    nitter.WithCooldown(30 * time.Second),
)
```

`New(opts ...Options) (*Client, error)` 组装 Client。Options：

| Option | 默认 | 含义 |
| --- | --- | --- |
| `WithInstances([]Instance)` | 空 | 轮换集合，调用时复制。空集合合法：`New` 成功，但之后的每次抓取都以 `KindUnavailable`（"no instances configured"）失败。 |
| `WithHTTPClient(Transport)` | 无 | 注入传输层。不注入时 `New` 依然成功，抓取在注入前失败。 |
| `WithCooldown(time.Duration)` | `60s` | 实例失败冷却。零或负数表示失败仍被标记但永不阻塞挑选。 |

`Options` 是普通函数；向 `New` 传入 nil 选项是 `KindInvalidArg` 错误。只要注入
的 Transport 并发安全，Client 即并发安全。分页上限为 5 页（内部默认；后续
里程碑新增的抓取方法只会以只增方式扩展公开面）。

### Transport

```go
type Transport interface {
    Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error)
}
```

SDK 的窄 HTTP 边界：恰好是抓取所需，别无其他。真实传输层是
`internal/nitter/protocol/httpx` 的 `*httpx.Client`，以结构化方式满足该接口；
接口定义在 SDK 里是因为 SDK 不得导入 `internal/*`（依赖方向已冻结：`httpx`
为共享错误 kind 导入 SDK）。测试注入假实现。

### 实例（轮换）语义

`nitter.Instance` 为 `{URL string; Username, Password string}`——Nitter 实例的
可选 basic-auth 凭证。凭证会随对象携带，但绝不回显进错误（脱敏契约）。

轮换由 `Chooser` 严格按序执行：按配置顺序挑选实例；请求失败（429、网络错误）
的实例进入冷却，直到 `now + cooldown`，冷却期内被跳过；成功立即复位其冷却。
冷却边界是严格的——实例在其冷却结束后（严格晚于该时刻）才可再次被挑选。所有
实例都在冷却时，`Pick` 以 `KindUnavailable`（"all instances cooling down"）
失败；一个实例都没有时为 `KindUnavailable`（"no instances configured"）。没有
成功率加权：配置顺序就是策略。

## 数据模型

以下结构体都是 NDJSON 数据契约；JSON 键冻结（只增不改），每个字段无条件
输出。

### Tweet

```go
type Tweet struct {
    ID          string    // 纯数字 X status ID，以字符串承载（避免 JS 精度丢失）
    URL         string    // 规范形式 https://x.com/<user>/status/<id>
    Text        string    // 纯文本：HTML 折叠，quote 与 emoji 图片节点剔除
    Author      Author
    PublishedAt time.Time // UTC；序列化为 RFC3339 UTC（"2026-07-27T09:09:40Z"）
    Media       []Media   // 无媒体时可能为 nil
    IsRetweet   bool      // 纯转推（retweet-header 判定）
    RepostedBy  string    // 转推者显示名（非 handle）；仅 IsRetweet 时非空
    ReplyTo     string    // 被回复者 handle；非回复时为空
    Quote       *Quoted   // 被引用推文摘要；无则为 nil
}

type Author struct{ Handle, Name, AvatarURL string }

type Media struct {
    Type         string // "image"、"video" 或 "gif"
    URL          string // 实例返回的直链
    Width, Height int   // 来源未携带时为 0
}

type Quoted struct {
    ID, URL, Text string
    Author        Author
}
```

钉死的序列化形状（来自 `sdk/models_test.go`）：

```json
{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236","text":"line one\nline two","author":{"handle":"NASA","name":"NASA","avatar_url":"https://pbs.twimg.com/profile_images/x_normal.jpg"},"published_at":"2026-07-27T09:09:40Z","media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800},{"type":"video","url":"https://video.twimg.com/vid/abc.mp4","width":0,"height":0}],"is_retweet":true,"reposted_by":"nasa","reply_to":"esa","quote":{"id":"123","url":"https://x.com/esa/status/123","text":"quoted text","author":{"handle":"esa","name":"ESA","avatar_url":""}}}
```

零值序列化时每个键都存在（`"media":null`、`"quote":null`、
`"published_at":"0001-01-01T00:00:00Z"`）。

### Page

```go
type Page[T any] struct {
    Items      []T    `json:"items"`
    NextCursor string `json:"next_cursor"`
}
```

一页结果。`NextCursor` 是请求下一页的不透明游标；为空表示没有更多结果。RSS
源没有游标：其页携带空 `NextCursor` 且只有单个 `Page`。

### InstanceReport（instances test）

```go
type InstanceReport struct {
    URL      string        `json:"url"`
    RSS      Probe         `json:"rss"`
    UserHTML Probe         `json:"user_html"`
    Search   Probe         `json:"search"`
    List     Probe         `json:"list"`
    Latency  time.Duration `json:"latency"` // RSS 探测的往返耗时
}

type Probe struct {
    OK     bool   `json:"ok"`
    Status int    `json:"status"` // 状态失败时的 HTTP 状态码
    Err    string `json:"err"`    // 内容/传输失败时的简短脱敏原因
}
```

单个实例的分能力结果：RSS 源、用户 HTML 时间线、搜索、List。`Latency` 序列化
为 Go duration 字符串（例如 `"212ms"`）。

## 错误

```go
type Kind string

const (
    KindChallenge   Kind = "challenge_required"         // 登录/维护/挑战页
    KindRateLimited Kind = "rate_limited"               // 429；有 Retry-After 时填充 RetryAfter
    KindNotFound    Kind = "not_found"
    KindUnavailable Kind = "upstream_unavailable"       // 不可达/5xx、无实例、全部冷却中
    KindMalformed   Kind = "malformed_upstream_response"
    KindInvalidArg  Kind = "invalid_argument"
    KindLocalState  Kind = "local_state_error"          // 状态文件损坏、缺少传输层
)

type Error struct {
    Kind       Kind
    Op         string
    Err        error          // 包装链；Unwrap 暴露
    RetryAfter *time.Duration // 仅 KindRateLimited，取自响应头
}
```

- `Error()` 渲染稳定且脱敏的消息 `"<op>: <kind>: <chain>"`，空部分省略。
- 用 `nitter.Errorf(kind, op, format, args...)` 构造错误；format 中用 `%w`
  挂载根因。调用方以 `errors.As(*nitter.Error)` 判型并按 `Kind` 分派。
- `Kind` 集合在 v1 内稳定：取值只能增加，绝不删除、重命名或复用。
