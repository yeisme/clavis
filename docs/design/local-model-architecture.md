# 本地模型架构（C1 / C3，C2 边界）

状态：设计稿，供后续实现批次使用。本轮只定义探测、门控、超时、隐私和离线挖掘边界，不实现本地模型客户端、不改 UI。市场背景引用 [`ime-market-research.md`](ime-market-research.md) §3.2，不重复调研。

## 1. 定位与否证结论

本地模型有两条合法路径：

- **C2 离线批量挖掘是主路径**：用户显式开启后，从本机允许的历史文本或剪贴板快照批量挖候选，结果写入本地 learned 词库。任务可以运行较长时间，必须可暂停、取消和清除。
- **C3 预热后的在线候选是次路径**：本地端点已经探测为 ready，模型已经具备响应能力时，输入法才把当前拼音和应用名交给模型，作为额外候选来源。

调研 §3.2 已否证“逐键调用通用本地模型”：通用模型冷启动约 5–20 秒，而输入体验和当前 `inputengine.AITimeout = 1500ms` 只有 1.5 秒级预算。逐键本地推理在冷启动时必然超时，也会把模型进程管理、网络连接和磁盘 I/O 引入按键热路径。因此本设计明确写成：**本地模型做离线批量挖掘（C2 主路径）+ 预热后的候选（C3 次路径）**，不设计逐键冷启动推理。

C2 本轮只规定边界：不规定模型格式、挖掘算法或调度器；其输出必须进入 learned 层和词库四级优先级，不得直接修改 starter 或 custom。

## 2. C1：端侧异步探测

### 2.1 默认端点与三态

默认探测：

```text
http://127.0.0.1:11434/api/tags
```

单次探测硬超时 `2s`。端点可由 local/Ollama profile 覆盖，但覆盖地址必须仍通过 localhost 白名单和用户显式设置校验。探测只发送无敏感内容的 tags 请求，不携带 API key，不携带剪贴板、窗口标题或输入缓冲。

```go
// LocalModelState is the user-visible readiness state of a local endpoint.
type LocalModelState string

const (
    LocalModelUndetected LocalModelState = "undetected"
    LocalModelReady      LocalModelState = "ready"
    LocalModelOffline    LocalModelState = "offline"
)

// LocalModelStatus contains only capability metadata; ErrKind is a stable
// diagnostic class and never contains response bodies or credentials.
type LocalModelStatus struct {
    State     LocalModelState
    Endpoint  string
    Model     string
    CheckedAt time.Time
    ErrKind   string
}

// HTTPDoer is the existing injectable HTTP seam. It is intentionally narrow so
// probes and candidate requests can use fake transports in tests.
type HTTPDoer interface {
    Do(*http.Request) (*http.Response, error)
}

// ProbeLocalModel starts an asynchronous, bounded probe. It returns promptly,
// sends exactly one status, closes the response body, and treats failure as a
// normal state rather than a startup error.
func ProbeLocalModel(
    ctx context.Context,
    doer HTTPDoer,
    endpoint string,
) <-chan LocalModelStatus
```

状态转换：

| 事实 | 状态 |
|---|---|
| 从未得到过有效探测结果；连接拒绝、DNS/协议错误或首次超时 | `undetected` |
| HTTP 成功且能解析出至少一个模型名 | `ready`，并携带模型名 |
| 曾经 ready 后再次探测失败，或服务明确返回错误 | `offline` |

“文件存在”或“端口可连接”不等于模型 ready；必须解析出模型名。`offline` 不能阻塞启动，也不能自动打开 C3 请求门。

### 2.2 为什么必须异步

输入法启动处于全局钩子和候选窗建立的关键路径。同步等待 2 秒会让首次按键失去响应，并把“没有安装 ollama”变成启动故障。探测应在后台 goroutine 或等价异步任务运行，启动线程只注册状态回调并立即继续初始化。探测失败只影响 local model 能力，不影响普通输入、词典和远端 profile。

探测状态是能力提示，不是请求许可。只有 `ready`、模型名非空、用户打开 C3 开关且暖机门通过，才可创建 C3 请求。

设置层分成两个独立开关：

- `启用本地候选`：控制 C3；
- `启用离线挖掘`：控制 C2。

不能用一个总开关替代两者。探测尚未返回时，设置页显示“未检测到”，不能因为用户打开开关就提前发请求。

## 3. 超时分层与 profile 契约

当前 `inputengine.AITimeout = 1500ms` 是所有 profile 共用的全局常量。设计改为 profile 级请求预算：

| profile | 默认请求超时 | 硬保护上限 | 取舍 |
|---|---:|---:|---|
| 远端 provider | 1.5s | 1.5s | 保持逐键体验；云端慢时快速降级。 |
| local / Ollama | 4s | 10s | 给预热后本地推理更宽预算，但防止错误配置无限等待。 |

上限包括连接、首字节和流结束。设置可以降低值，不能超过保护上限。远端不能统一放宽到 4 秒：云端慢就是云端慢，放宽只会让打字更卡，不能修复 provider 延迟。

```go
const (
    DefaultRemoteAITimeout = 1500 * time.Millisecond
    DefaultLocalAITimeout  = 4 * time.Second
    MaxAIRequestTimeout    = 10 * time.Second
)

// EffectiveAITimeout returns the profile-specific deadline. Remote profiles
// stay at 1500ms; local profiles default to 4s; all values are clamped to a
// positive value no greater than MaxAIRequestTimeout.
func EffectiveAITimeout(profile config.AIProfile) time.Duration
```

迁移规则：旧配置只有全局 timeout 时，远端 profile 继续使用 1.5 秒；local profile 没有显式值时使用 4 秒。该 timeout 只作用于 profile 的 HTTP context，不改变 C1 的 2 秒探测预算，也不改变 C2 批处理调度预算。

## 4. 暖机门：未就绪时零请求

### 4.1 门状态与契约

local 请求必须先通过 readiness gate。以下任一条件不满足时，C3 请求入口必须返回“暂不可用”而不构造请求：

- 状态不是 `ready`；
- 模型名为空；
- 用户关闭本地候选；
- 暖机未完成或 warm-up 失败；
- context 已取消。

```go
// LocalRequestGate is a side-effect-free snapshot of the C3 request gate.
type LocalRequestGate struct {
    Status   LocalModelStatus
    Enabled  bool
    Warmed   bool
}

// CanRequestLocalModel is a pure predicate. False guarantees that no request
// body is built and HTTPDoer.Do is not called.
func CanRequestLocalModel(gate LocalRequestGate) bool
```

暖机可以在探测成功后由 profile adapter 发起一次明确的最小 warm-up，但必须受 local profile 的 4 秒默认 timeout、10 秒上限、取消信号和用户开关约束。warm-up 失败将状态变为 `offline` 并关闭门；后续按键仍保持零请求，只有显式重探测并再次 ready 才能恢复。

### 4.2 fake HTTPDoer 验收

测试使用计数型 fake：

```go
type countingHTTPDoer struct { Calls int }
```

必须分开统计探测请求和候选请求：

- `undetected`、`offline`、模型名为空、`Enabled=false` 或 `Warmed=false` 时，调用 C3 入口后候选 fake 的 `Calls == 0`，并且没有生成请求体；
- `ready + model != "" + Enabled + Warmed` 时，候选 `Calls == 1`；
- 探测本身的 call 不得被误计为候选请求。

## 5. C3 请求内容与隐私边界

喂给模型的输入只有两项：

1. 当前拼音字符串；
2. 应用名，最多 64 个 Unicode 字符。

```go
// BuildLocalCandidatePrompt creates the only allowed C3 prompt shape. It
// rejects non-pinyin input, truncates appName deterministically, and never
// appends ambient context.
func BuildLocalCandidatePrompt(pinyin, appName string) (string, error)
```

禁止发送：剪贴板、周围文本、窗口标题、URL、文档名、输入历史、候选选择记录、密码字段或任意未显式列出的上下文。应用名来自已经允许的应用上下文；为空时保持为空，不用其他字段补齐。

localhost 只表示传输不出机器，不表示可以扩大数据范围。日志不得记录完整 prompt、模型回答原文或剪贴板内容，只记录状态、耗时、模型名和稳定错误分类。

## 6. C2：离线批量挖掘边界

离线挖掘默认关闭，必须由用户显式开启；设置必须提供暂停和清除入口，并能区分任务生成的 learned 条目与用户手工 pinned 条目。

```go
type OfflineMiningPolicy struct {
    Enabled        bool
    Paused         bool
    AllowClipboard bool
    AllowHistory   bool
}

// LocalTextSource yields only policy-approved local text. It must not perform
// network I/O and must support cancellation between records.
type LocalTextSource interface {
    Next(context.Context) (string, bool, error)
}

// LocalBatchModel is a local-only batch interface. Its implementation may be
// slow, but it must honor context cancellation and never call a remote service.
type LocalBatchModel interface {
    Extract(context.Context, string) ([]LearnedCandidate, error)
}

// LearningSink writes only to the local learned store. It must preserve user
// pinned entries when ClearMiningRun is called.
type LearningSink interface {
    PutMiningRun(context.Context, string, []LearnedCandidate) error
    ClearMiningRun(context.Context, string) error
}

// MineOfflineCandidates is an explicitly authorized, cancellable local-only
// job. It may read only policy-approved sources, writes only the local learned
// store, and never changes starter/custom files or performs network I/O.
func MineOfflineCandidates(
    context.Context,
    OfflineMiningPolicy,
    []LocalTextSource,
    LocalBatchModel,
    LearningSink,
) error
```

暂停必须在下一个取消检查点生效；清除只删除该任务的 learned 条目，不能删除用户 pinned 项。任务状态、进度和错误可本地记录，但不能上传遥测。C2 输出必须进入 learned 层和词库四级优先级。

## 7. 测试策略

| 测试名 | 断言要点 |
|---|---|
| `TestProbeLocalModelIsAsyncAndUsesTwoSecondDeadline` | 调用立即返回；fake endpoint 收到一次请求；deadline 不超过 2 秒；结果通道只发一个状态。 |
| `TestProbeLocalModelThreeStates` | 首次拒绝/超时为 undetected；曾 ready 后失败为 offline；成功且有模型名为 ready。 |
| `TestProbeLocalModelNeverSendsAmbientContext` | 探测请求不含 key、剪贴板、窗口标题或输入缓冲。 |
| `TestEffectiveAITimeoutByProfile` | remote=1500ms，local=4s，任何配置不超过 10s，非法值回到默认。 |
| `TestRemoteTimeoutIsNotWidened` | 即使旧全局 timeout 很大，remote 仍为 1.5s。 |
| `TestLocalWarmupGateZeroRequestsBeforeReady` | undetected/offline/空模型/关闭开关/未暖机时 C3 fake `Calls==0`。 |
| `TestLocalWarmupGateAllowsRequestOnlyWhenReady` | ready、有模型、开启且 warmed 时 C3 `Calls==1`。 |
| `TestWarmupFailureClosesGate` | warm-up 超时后 offline，下一次按键不创建请求；显式重探测后才可恢复。 |
| `TestLocalPromptContainsOnlyPinyinAndAppName` | prompt 只有拼音和截断应用名，不出现 clipboard、URL、window title 或历史。 |
| `TestOfflineMiningRequiresExplicitOptIn` | 未启用或暂停时没有模型调用、词库写入和网络调用。 |
| `TestOfflineMiningPauseAndClear` | 暂停在取消点生效，清除只删该 run 的 learned，pinned 保留。 |
| `TestOfflineMiningNeverLeavesMachine` | fake transport 没有外发调用，输出只进入本地 LearningSink。 |

## 8. 关键取舍与否掉的替代方案

- 选择异步 2 秒探测：启动路径不能被未安装的 Ollama 阻塞。
- 选择三态而不是布尔值：未检测到和曾就绪后离线需要不同提示与重试语义。
- 选择 profile 级 timeout：local 默认 4 秒，remote 固定 1.5 秒。
- 选择暖机门前置：未就绪时必须零请求，不能用一次超时请求代替探测。
- 选择 C2 批量挖掘为主路径：冷启动延迟与逐键预算不相容。
- 选择最小 prompt：localhost 不等于可以读取最高敏感的周围文本。
- 否掉同步探测：会让启动和第一次按键承担 2 秒 I/O。
- 否掉统一放宽到 4 秒：远端慢只会让打字更卡。
- 否掉“每键先请求、超时再降级”：违反零请求门契约。
- 否掉把剪贴板/周围文本送入 C3：超出现有隐私约束。
- 否掉逐键通用本地模型：冷启动 5–20 秒，无法满足输入延迟。

## 9. 遗留待拍板问题

1. local readiness 是只信 `/api/tags`，还是要求最小 warm-up 也成功后才显示 ready。
2. undetected 与 offline 是否显示不同文案，自动重探测间隔取多少。
3. local timeout 是否允许低于 4 秒；当前建议允许降低，但不得超过 10 秒。
4. C2 历史文本来源、保留期限和清除操作是否需要可审计 run ID。
5. 多个 local 模型同时 ready 时，使用固定 profile 顺序还是最近成功模型。
6. C3 失败是否保留本地诊断历史；建议只存错误分类和耗时。
7. 离线挖掘输出默认是否 unpinned，以及用户提升为 pinned 的入口。
