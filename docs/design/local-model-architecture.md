# 本地模型架构（C1/C3，C2 边界）

本方案只定义本地端点探测、请求门控、超时和隐私边界。市场调研中关于逐键本地推理不可行的论证见 [`ime-market-research.md`](ime-market-research.md) §3.2，不在此重复调研。

## 1. 定位与边界

本地模型有两条合法路径：

- **C2 离线批量挖掘是主路径**。用户显式开启后，从本机允许的历史文本或剪贴板快照批量挖掘候选，结果写入本地 learned 词库。它可以长时间运行、暂停和清除。
- **C3 预热后的候选是次路径**。模型已经就绪且完成暖机后，输入法可以把当前拼音和应用名交给本地端点，作为额外候选。

调研已否证“逐键调用通用本地模型”这一方案。冷启动约 5–20 秒，而当前输入体验只有 1.5 秒级别预算，冷启动必然超时。设计因此不把本地模型做成逐键推理器，也不在每个按键上偷偷触发暖机。

C2 只保留边界契约，不规定挖掘算法、模型格式或任务调度器。C2 输出必须进入既有 learned 词库合并和来源优先级流程。

## 2. 端侧异步探测

### 2.1 默认端点与三态

默认探测 `http://127.0.0.1:11434/api/tags`，请求超时为 2 秒 (`2s`)。端点可由本地模型 profile 覆盖，但覆盖地址仍须通过本地地址白名单和显式设置校验。响应成功且能解析出模型名时状态为“就绪（模型名）”。首次探测前或本机从未返回过有效模型名时，连接拒绝、DNS/协议错误或超时记为“未检测到”。曾经就绪后再次探测失败，或端点明确返回错误时，记为“离线”。两种失败状态都不能阻塞启动。

```go
type LocalModelState string

const (
	LocalModelUndetected LocalModelState = "undetected"
	LocalModelReady      LocalModelState = "ready"
	LocalModelOffline    LocalModelState = "offline"
)

type LocalModelStatus struct {
	State    LocalModelState
	Endpoint string
	Model    string
	CheckedAt time.Time
	ErrKind  string // 仅诊断分类，不向模型请求传输错误内容
}

// ProbeLocalModel starts a non-blocking probe. It performs at most one HTTP
// request with a 2s deadline, closes the response body, and sends exactly one
// status to the returned channel. A failed probe is a normal status, not a
// startup error.
func ProbeLocalModel(
	ctx context.Context,
	doer HTTPDoer,
	endpoint string,
	) <-chan LocalModelStatus
```

`HTTPDoer` 复用现有 `pkg/core/ai` 抽象。探测任务在后台 goroutine 或等价异步执行，启动线程只注册状态回调并立即显示“未检测到”。探测不得读取剪贴板、窗口标题或输入缓冲，也不得携带 API key。

### 2.2 为什么必须异步

输入法启动处于全局钩子和候选窗建立的关键路径。同步等待 2 秒会让用户第一次按键失去响应，并把“本机没有安装 ollama”变成启动故障。异步探测允许引擎先进入可用状态，探测成功后再刷新设置页和 C3 能力。失败只影响本地模型状态，不影响本地词典和普通输入。

探测状态是能力提示，不等于模型请求许可。只有 `ready` 且模型名非空时才可能打开 C3 请求门。

本地模型开关位于设置页 `AI > 本地模型`，持久化在 local/Ollama profile 的启用字段。`启用本地候选` 控制 C3 请求门，`启用离线挖掘` 是独立开关，不能用一个开关替代两者。设置页在探测完成前显示未检测到，不能因为用户打开开关就提前发请求。

## 3. profile 级超时分层

目前 `inputengine.AITimeout = 1500 * time.Millisecond` 是所有 profile 共用的全局常量。设计改为每个 profile 解析出请求预算：

| profile | 默认请求超时 | 保护上限 | 说明 |
|---|---:|---:|---|
| 远端 provider | 1.5s | 1.5s | 保持逐键体验，云端慢时应快速降级。 |
| local / Ollama | 4s | 10s | 允许预热后的本地推理完成，但不能被错误配置拖成无限等待。 |

上限是硬截止，包括连接、首字节和流结束。设置页可以降低值，不能超过保护上限。远端不放宽到 4 秒，因为云端慢就是云端慢，放宽只会让打字更卡，并不能修复 provider 延迟。

```go
// EffectiveAITimeout selects a profile-specific deadline. Remote providers
// remain at 1500ms, local providers default to 4s, and every profile is
// clamped to a positive value no greater than MaxAIRequestTimeout.
func EffectiveAITimeout(profile config.AIProfile) time.Duration

const (
	DefaultRemoteAITimeout = 1500 * time.Millisecond
	DefaultLocalAITimeout  = 4 * time.Second
	MaxAIRequestTimeout    = 10 * time.Second
)
```

旧配置只有全局 `AITimeout` 时，迁移到远端 profile 的 1.5 秒。local profile 没有显式值时使用 4 秒。profile 级值只影响该 profile 的 HTTP context，不改变探测的 2 秒预算，也不改变离线挖掘任务的调度预算。

## 4. 暖机门与零请求保证

### 4.1 门状态

本地模型请求必须经过 readiness gate。`undetected`、`offline`、模型名为空、探测尚未返回、用户关闭本地模型或离线挖掘开关关闭时，均为零请求。不能“先发一次再等超时”来判断模型是否可用。

```go
// LocalRequestGate reports whether a local model request may be created. It
// is false until the asynchronous probe has returned ready with a non-empty
// model name and the user has enabled the relevant feature.
type LocalRequestGate struct {
	Status  LocalModelStatus
	Enabled bool
}

// CanRequestLocalModel is side-effect free. A false result guarantees that no
// request object is built and HTTPDoer.Do is not called.
func CanRequestLocalModel(gate LocalRequestGate) bool
```

暖机完成的判定由 profile 适配器提供。适配器可以在探测成功后执行一次明确的本地 warm-up，但 warm-up 仍受用户开关、4 秒 local 超时和取消信号约束。warm-up 失败将状态置为 `offline`，后续按键仍保持零请求，直到下一次显式重探测。

### 4.2 fake HTTPDoer 验证

测试注入计数型 fake：

```go
type countingHTTPDoer struct {
	Calls int
}
```

当状态为 `undetected`、`offline` 或 `Enabled=false` 时，调用候选请求入口后断言 `Calls == 0`，并断言没有生成请求体。状态变为 `ready` 且模型名为非空后，才允许 `Calls == 1`。探测请求和候选请求应使用两个计数器，避免把探测本身误判成候选请求。

## 5. C3 请求内容与隐私边界

喂给模型的内容只有：

1. 当前拼音字符串。
2. 应用名，最多 64 个 Unicode 字符。

现有 `AIPrompt` 约束继续有效。不得发送剪贴板、周围文本、窗口标题、URL、文档名、输入历史、候选选择记录或密码字段内容。应用名来自已允许的应用上下文，空值时不填充其他上下文。

```go
// BuildLocalCandidatePrompt creates the only prompt shape allowed for C3. It
// rejects non-pinyin input and truncates appName deterministically. No ambient
// context may be appended by callers.
func BuildLocalCandidatePrompt(pinyin, appName string) (string, error)
```

本地端点即使是 keyless localhost，也必须遵守这条数据边界。localhost 只表示传输不离开本机，不代表可以扩大输入数据范围。日志不得记录完整 prompt、模型回答中的原文或剪贴板内容，只记录状态、耗时、模型名和错误分类。

## 6. C2 离线批量挖掘边界

离线挖掘默认关闭，用户必须在设置中显式开启。设置项至少包括“启用”“暂停”“清除已挖掘词条”和来源选择。

```go
type OfflineMiningPolicy struct {
	Enabled       bool
	Paused        bool
	AllowClipboard bool
	AllowHistory  bool
}

// MineOfflineCandidates is a user-authorized, cancellable local-only job. It
// may read only sources allowed by policy, writes only the local learned store,
// and must expose pause and clear controls. It never performs network I/O.
func MineOfflineCandidates(
	ctx context.Context,
	policy OfflineMiningPolicy,
	sources []LocalTextSource,
	model LocalBatchModel,
	store LearningSink,
	) error
```

暂停必须在下一个可取消检查点生效，清除操作必须删除该任务产生的 learned 条目并保留用户手工 pinned 条目。任务状态、进度和错误可以本地记录，不能上传遥测。C2 的输出不能绕过四级优先级，也不能直接改写 starter 或 custom 文件。

## 7. 测试策略

| 测试名 | 断言要点 |
|---|---|
| `TestProbeLocalModelIsAsyncAndUsesTwoSecondDeadline` | 调用探测后启动函数立即返回，fake endpoint 收到一次请求，context deadline 不超过 2 秒，通道只发送一个状态。 |
| `TestProbeLocalModelThreeStates` | 连接拒绝或超时得到 undetected/offline，200 响应且模型名非空得到 ready，HTTP 错误不阻塞启动。 |
| `TestProbeLocalModelNeverSendsAmbientContext` | 探测请求不含 API key、剪贴板、窗口标题或输入缓冲。 |
| `TestEffectiveAITimeoutByProfile` | 远端默认严格为 1500ms，local 默认 4s，任意配置不能超过 10s，非法值回到 profile 默认值。 |
| `TestRemoteTimeoutIsNotWidened` | 将全局旧 timeout 设置为大值后，远端 profile 仍按 1.5s 截止。 |
| `TestLocalWarmupGateZeroRequestsBeforeReady` | undetected/offline/空模型名/关闭开关时调用 C3 入口，候选 fake `HTTPDoer` 的 Calls 始终为 0。 |
| `TestLocalWarmupGateAllowsRequestOnlyWhenReady` | ready 且模型名非空、功能开启时 Calls 恰为 1，其他状态仍为 0。 |
| `TestWarmupFailureClosesGate` | warm-up 超时后状态变 offline，下一次按键不创建请求，显式重探测后才可恢复。 |
| `TestLocalPromptContainsOnlyPinyinAndAppName` | prompt 只出现拼音和截断后的应用名，不出现 clipboard、surrounding text、URL、window title 或历史。 |
| `TestOfflineMiningRequiresExplicitOptIn` | Enabled=false 或 Paused=true 时没有模型调用、没有词库写入、没有网络请求。 |
| `TestOfflineMiningPauseAndClear` | 暂停在取消点生效，清除只删除该任务的 learned 条目，用户 pinned 条目保留。 |
| `TestOfflineMiningNeverLeavesMachine` | fake transport 没有外发调用，所有输出只进入本地 LearningSink。 |

## 8. 关键取舍与否掉的替代方案

- 选择异步 2 秒探测，因为启动路径不能被本机未安装 ollama 阻塞。
- 选择三态而不是单一布尔值，因为“尚未检测到”和“探测后离线”需要不同的用户反馈与重试行为。
- 选择 profile 级超时，因为 local 需要 4 秒预算，而远端仍须保持 1.5 秒快速降级。
- 选择暖机门前置，因为等待超时不是能力探测，且会直接污染逐键延迟。
- 选择 C2 批量挖掘为主路径，因为冷启动延迟与逐键预算不相容。
- 选择 C3 只发送拼音和应用名，因为 localhost 传输不出机不能替代最小数据边界。
- 否掉同步探测，因为它会让输入法启动和第一次按键承担 2 秒 I/O。
- 否掉统一放宽到 4 秒的全局 timeout，因为远端慢时只会让打字更卡。
- 否掉“每次按键先请求、超时后降级”，因为未就绪时必须保证零请求。
- 否掉把剪贴板和周围文本送进 C3，因为现有隐私约束明确只允许拼音与应用名。
- 否掉逐键通用本地模型推理，因为冷启动 5–20 秒超过输入法可接受延迟。

## 9. 遗留待拍板问题

1. local profile 的 readiness 是否只信 `/api/tags`，还是要求一次最小 warm-up 也成功后才显示“就绪”。
2. `undetected` 与 `offline` 在设置页是否显示不同文案，以及自动重探测间隔。
3. local profile 的可配置超时是否允许小于 4 秒，当前建议允许但不允许超过 10 秒。
4. C2 历史文本的具体来源、保留期限和“清除”是否需要可审计的任务 ID。
5. 多个本地模型同时 ready 时，模型选择由用户固定、按 profile 顺序，还是按最近一次成功结果。
6. C3 候选失败是否写入本地诊断历史，当前建议只保留错误分类和耗时，不保留 prompt/回答。
7. 离线挖掘生成的词条是否默认 unpinned，以及用户手工提升为 pinned 的入口。
