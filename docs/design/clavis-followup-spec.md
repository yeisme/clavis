# Clavis 后续能力收口规格

> **Status: Superseded / Updated as of 2026-11-25** — 本文列出的多项后续能力已于 `CLV-P0` 与 `CLV-P1` 落地（Codex SQLite `mode=ro` 与 `terminal.Resolver` 接线、Herdr 聚焦/读输出/受控操作面、剪贴板 120Hz 缓存优化、Bigram 前缀匹配与词典热更新、Security Vault AES-256-GCM 加密、AI 用量遥测/翻译/续写）；尚未实施项（统一 SQLite 迁移、`MergeLearning`、同步 v2 与共享目录传输、可选 `NotificationStore`、Windows Parity）已统一收口至 [`docs/ROADMAP.md`](../ROADMAP.md) 的 `CLV-P3` / `CLV-P4`。下文保留作接口历史参考。

- 任务：T-clavis-followup-spec
- 日期：2026-11-21
- 范围：agent、Omnibox Tab/搜索、剪贴板、keymap/终端、词库后端、同步、原生 DSH/飞书通知
- 本文只改设计范围，不授权发送 agent 消息、执行远程命令或同步密码。

## 0. 结论和状态标记

本规格把已有底层能力收口成可执行的接口和批次。状态使用四种标记。

| 标记 | 含义 |
| --- | --- |
| 已实现 | 当前代码和测试已有该行为 |
| 部分实现 | 数据或 UI 已有，但链路、能力探测或失败契约不完整 |
| 未实现 | 当前代码没有该入口或协议 |
| 计划接口 | 后续实现必须遵循的稳定边界 |

所有跨进程、文件或网络操作都返回可观察状态，不能用布尔值掩盖未知结果。

    type OperationState string
    const (
        StateReady OperationState = "ready"
        StateRefreshing OperationState = "refreshing"
        StateDegraded OperationState = "degraded"
        StateFailed OperationState = "failed"
        StateUnknown OperationState = "unknown"
    )
    type OperationResult struct {
        State OperationState
        Code string
        Message string
        ObservedAt time.Time
        Retryable bool
    }

unknown 表示提交后无法确认结果，不自动重试有副作用的动作。degraded 表示旧快照仍可用。诊断不得包含 prompt、transcript、token、绝对路径或剪贴板正文。

## 1. 现状勘误

| 旧说法 | 当前实现事实 | 收口要求 |
| --- | --- | --- |
| Agent tab 只是占位 | pkg/core/agentsession 已有 DSH、Codex、Claude 发现，uiapp/agent_sessions.go 已有缓存刷新、列表和命令补全 | 接 attach/focus，不重写列表 |
| Codex 没有 state_5.sqlite | internal/agentsession/codex_sqlite.go 使用 file:path?mode=ro，动态检查 threads schema | 禁止 immutable=1，保留 WAL 可见性 |
| 选会话会跳回原窗口 | uiapp/agent_sessions.go:174-221 只复制 ID，再固定打开 com.mitchellh.ghostty | 明确标记为复制 ID 降级，Focused 必须为 false |
| Tab 仍只有五个 | uiapp/capsule_view.go:18-25 已固定六项，Agent 在末尾，Cmd+1..6 已在 :1113-1116 实现 | 保持 1..6 稳定，新增 Tab 不重排 |
| 剪贴板没有持久分栏 | uiapp/ui_state.go:11-110 已持久化 ui-state.json，默认列表 58%，范围 35–75%，500ms debounce | 只补性能和降级，不新造配置字段 |
| bigram 只有表没有生成 | internal/storage/learning_store.go:266-299 与 pkg/core/inputengine/engine.go:840-855 已接入生成 | 后续聚焦 B4 合并、冷启动和质量回归 |
| 词库失败静默回退 | pkg/core/inputengine/dictionary.go:239-367 已有诊断和逐文件回退，已有 dictionary_resolve_test.go | 把热更新结果接到诊断 UI，失败保留旧指针 |
| keymap 没有动作绑定 | pkg/core/config/main_config.go:889-1100 已有 KeyBinding，validator.go:801-851 校验，internal/bridge/triggers.go:58-96 编译 | 后续做录制 UI、冲突范围和终端 profile |
| 同步只有设计 | pkg/core/sync 已有 envelope、tombstone、Client.Sync/Upload/Delete，internal/sync 有 watermark/recovery 持久化 | 补 UI 对象映射、unknown、重启续跑 |
| workflow notify 等于实时通知 | 当前只有 JSON-RPC framing 和 workflow 输出，没有 DSH/Lark event source | 通知单独实现只读订阅、游标和去重 |

## 2. Agent 对接

### 2.1 代码位置和实现状态

| 能力 | 状态 | 位置 |
| --- | --- | --- |
| DSH JSON/JSONL 发现 | 已实现 | pkg/core/agentsession/dsh.go |
| Codex SQLite 只读发现 | 已实现 | internal/agentsession/codex_sqlite.go |
| Claude JSONL 发现 | 已实现 | pkg/core/agentsession/claude.go |
| 四态状态、脱敏、聚合排序 | 已实现 | model.go、aggregate.go、sanitize.go |
| Agent tab、会话/命令补全 | 已实现 | uiapp/agent_sessions.go、capsule_view.go |
| 缓存分片和刷新状态 | 部分实现 | 当前是 controller 内存快照，尚无持久缓存和 provider 分片状态 |
| 能力探测 | 未实现 | 无 provider capability 接口 |
| attach/focus 原会话 | 未实现 | 当前为复制 ID加打开固定 Ghostty |
| 消息发送 | 未实现 | /继续、/验收、/测试只复制待发模板 |

### 2.2 三 provider 数据源、刷新与缓存

计划接口如下，沿用 pkg/core/agentsession.Session。

    type AgentSource interface {
        Provider() string
        Available(ctx context.Context) (bool, error)
        List(ctx context.Context, limit int) ([]Session, error)
        Capabilities(ctx context.Context) (ProviderCapabilities, error)
    }
    type ProviderCapabilities struct {
        Provider string
        ReadSessions bool
        FocusSession bool
        AttachSession bool
        SendMessage bool
        StopSession bool
        Methods []string
        ObservedAt time.Time
    }
    type AgentCache interface {
        Snapshot(ctx context.Context) ([]Session, []Diagnostic, error)
        Replace(provider string, sessions []Session, observedAt time.Time) error
        Mark(provider string, result OperationResult) error
    }

刷新数据流固定为：打开 Agent tab，50ms 内读旧快照并显示 refreshing，并行调用三 provider，每源 500ms 超时，只替换成功 provider 的分片，发 EventSetAgentSessions 重绘。启动 discovery 上限 2 秒。失败保留旧分片并标记 degraded，连续三次同类解析失败才汇总日志。

- DSH 优先使用已声明的只读 RPC，未知方法回退现有 JSON/JSONL。
- Codex 使用 mode=ro，不能使用 immutable=1。
- Claude 目录存在不能证明 Claude 正在运行。
- 缓存键为 provider 加 session ID。不得把完整 transcript 写入缓存。

### 2.3 状态、能力跳转和复制 ID 降级

    type SessionTarget struct {
        Provider string
        SessionID string
        BundleID string
        WindowID string
        Workspace string
        Method string
    }
    type FocusResult struct {
        OperationResult
        Focused bool
        CreatedNewWindow bool
        CopiedID bool
    }
    type AgentSessionResolver interface {
        Resolve(ctx context.Context, session Session) (SessionTarget, error)
        Focus(ctx context.Context, target SessionTarget) (FocusResult, error)
    }

跳转顺序是 provider 已声明且返回同一 SessionID 的 focus/attach，已有 bundle/window 的激活，用户明确允许后按 workspace 新建终端，最后复制 ID。复制 ID 必须 CopiedID=true、Focused=false，文案只能是“已复制会话 ID”，不能说“已打开会话”。

### 2.4 三种补全边界

    type CommandCompletion struct { ID, Label, Description, Action, Source string; Score float64 }
    type ConversationCompletion struct { SessionID, Provider, Title, Workspace string; Score float64 }
    type MessageCompletion struct { ID, Label, BodyTemplate string; RequiresWrite bool; ProviderScopes []string }
    func CompleteCommands(ctx context.Context, query, activeApp string) []CommandCompletion
    func CompleteConversations(ctx context.Context, query string) []ConversationCompletion
    func CompleteMessages(ctx context.Context, session Session, query string) []MessageCompletion

命令补全只读 action registry、Leader 节点、终端 profile 和命令首词。对话补全只选择 session。消息补全只给本地模板和预览，SendMessage 另设 gated action，要求 SendMessage 能力、用户确认和 session ID 匹配。

Agent 验收：

- go test ./pkg/core/agentsession ./internal/agentsession：三源 fixture、schema 变化、脱敏。
- go test ./uiapp -run 'TestAgent'：旧快照先显示，单个 provider 失败不清空其他源。
- fake resolver 覆盖同 ID 成功、窗口不存在、复制 ID 降级，断言不得伪称跳转。
- fake provider 对未知 method 零调用，对已声明 method 才调用。
- 未确认、无写能力、目标不匹配的 SendMessage 不产生写调用。

## 3. Tab 和搜索交互

### 3.1 现状

uiapp/capsule_view.go:18-25 的顺序是 Actions、Clipboard、Snippets、AI、Glossary、Agent。搜索框在 :512-523 自动聚焦。:1065-1083 已实现 IME preedit > inline completion > tab navigation。:1113-1116 已绑定 Cmd+1..6，查询在横向切换时保留。

### 3.2 状态机和事件接口

    SearchIdle --输入--> SearchQuerying
    SearchQuerying --IME preedit--> IMEPreedit
    SearchQuerying --有 completion + Tab--> InlineCompletion
    SearchQuerying --无 completion + Shift+Tab--> TabNavigation
    SearchQuerying --Cmd+1..6--> FixedTabSwitch
    SearchQuerying --Cmd+Left/Right--> RelativeTabSwitch
    IMEPreedit --四个裁决键--> IME处理，不由面板消费
    InlineCompletion --Tab--> 接受 completion，仍留在当前 Tab
    InlineCompletion --Shift+Tab--> 不接受、不切换

引擎边界沿用现有事件，不在 UI 再造 Tab 状态源。

    type EventType string
    const (
        EventUpdateQuery EventType = "update_query"
        EventSwitchTab EventType = "switch_tab"
        EventOpenOmnibox EventType = "open_omnibox"
    )
    type Event struct {
        Type EventType
        Query string
        Tab config.CapsuleTab
    }
    func (m *StateMachine) Handle(event Event) []Effect

错误契约是：IME preedit 时面板不发 `EventSwitchTab`，inline completion 被接受时只发 `EventUpdateQuery`，刷新失败保留上一份 `OmniboxItems` 并通过 UI 诊断标记 degraded。

| 按键 | 无 preedit、无 completion | 有 completion | 有 IME preedit |
| --- | --- | --- | --- |
| Tab | 保持当前状态 | 接受 completion，Tab 不变 | 交给 IME |
| Shift+Tab | 切换下一个 Tab | 不接受、不切换 | 交给 IME |
| Cmd+Left/Right | 上/下一个 Tab | 上/下一个 Tab | 交给 IME |
| Cmd+1..6 | 固定 Actions、Clipboard、Snippets、AI、Glossary、Agent | 同左 | 不抢占 |
| Enter | 执行当前项 | 执行当前项 | 交给 IME |

Agent 永远在第 6 位。验收命令：go test ./uiapp -run 'Test(ArbitrateSearchKey|TabArbitration|.*Cmd.*Tab|Agent)'。

### 3.3 回归矩阵

| 场景 | 输入 | 断言 |
| --- | --- | --- |
| 空查询 | Cmd+1..6 | 六个固定 Tab 逐一可达 |
| 横向导航 | query=abc 后 Cmd+Right | Tab 改变，query 仍为 abc |
| inline completion | 有补全文本后 Tab | query 完整，Tab 不变 |
| IME preedit | 拼音组合中 Tab、Shift+Tab、Cmd+箭头 | 面板不消费四键 |
| Agent 末尾 | Agent 后 Shift+Tab | 回到 Actions，循环不漏项 |
| 刷新超时 | Clipboard 搜索超时 | 旧结果保留，状态 degraded |

## 4. 粘贴板

### 4.1 当前接口和性能事实

核心接口在 pkg/core/clipboard/models.go:117-132。

    type Store interface {
        Record(ctx context.Context, candidate CaptureCandidate) (*ClipboardItem, bool, error)
        Search(ctx context.Context, query string, limit int) ([]ClipboardItem, error)
        Recent(ctx context.Context, limit int) ([]ClipboardItem, error)
        Pin(ctx context.Context, id string, pinned bool) error
        Delete(ctx context.Context, id string) error
        Clear(ctx context.Context) error
        PruneExpired(ctx context.Context, retentionDays, maxItems int) (int, error)
    }

文本、图片、文件、视频统一为 ClipboardItem。图片 MediaStore 已限制 40 MiB、8000 边、3200 万像素，缩略图最长边 256。uiapp/ui_state.go 已把分栏写入 ui-state.json，默认列表 58%，范围 35–75%，500ms debounce。capsule_preview.go 已让文本滚动、媒体画布让出 footer 空间。

### 4.2 计划契约和数据流

    type ClipboardLayoutState struct { ListPercent float32 }
    type ClipboardPreviewBudget struct {
        MaxTextLines int
        MaxFrameTime time.Duration
        MaxThumbEdge int
    }

采集到预览的数据流为 `bridge 捕获 -> PrivacyFilter -> Store.Record -> Search/Recent -> engine OmniboxItem -> Capsule preview`。`Store.Record` 返回 `(item, recorded, error)`，`recorded=false` 表示隐私过滤拒绝，不能在 UI 显示成持久化成功。激活条目时先写入目标剪贴板，再执行 paste，写入失败不得继续发送 paste；图片和文件沿用 `ClipboardMedia` 元数据，原始文件路径永不由 Clavis 删除。

保持 ui-state.json，不写 config.toml。加载失败回默认 58%，超范围夹到 35–75%，临时文件加 rename。单帧目标 <=16ms，连续 100 次上下移动不得产生线性磁盘读取。UI 只读已存缩略图，不解码原始 40 MiB 图像。文本最多渲染 200 行，代码/SQL/URL 用等宽 well 并显示隐藏行数。

| 类型 | 正常 | 降级 |
| --- | --- | --- |
| 文本 | 来源、字符数、行数、可滚动正文 | 空内容显示 — |
| 图片 | 256px 缩略图、尺寸、MIME、大小 | 只显示元数据和预览不可用 |
| 链接 | 原始 URL 和来源，不自动联网 | 保持原 URL，复制不受影响 |
| 代码/命令 | 等宽、换行、缩进 | 超预算截断并显示隐藏行数 |
| 文件/视频 | 名称、数量、大小、时长、存在性 | 原路径失效仍保留历史，复制返回错误 |

验收命令：go test ./pkg/core/clipboard ./internal/storage ./uiapp -run 'Test(Clipboard|Media|Preview|.*Split)'。性能基线：go test ./uiapp -run '^$' -bench BenchmarkClipboardSwitch -count=1。

## 5. keymap 和终端

### 5.1 当前接口

pkg/core/config/main_config.go:889-1100 已有 KeyBinding 和 KeymapConfig，validator.go:801-851 已校验动作白名单、至少一个 command/option/control、同 chord 冲突。internal/bridge/triggers.go:58-110 已编译并按应用过滤。

    type KeyBinding struct {
        Keys []string
        Action string
        AppBundleIDs []string
    }
    func ValidateKeyBindings(bindings []KeyBinding) []error
    func (r *triggerRuntime) setKeyBindings(bindings []config.KeyBinding)
    func (r *triggerRuntime) bindingFor(key string, modifiers []string, appBundleID string) (string, bool)

### 5.2 用户录制和冲突

    type KeyChordConflict struct { Chord, Existing, Incoming, Scope string }
    type KeymapRecorder interface {
        Begin(ctx context.Context, action, appBundleID string) error
        Cancel(ctx context.Context) error
        Commit(ctx context.Context, chord string) ([]KeyChordConflict, error)
    }

状态为 idle -> listening -> conflict -> committed 或 cancelled。录制期间不写配置。先检查同作用域，再检查 remap、IME 专用快捷键和系统保留键。冲突不静默覆盖，用户明确替换才提交。

### 5.3 终端 profile 和失败回退

    type TerminalProfile struct {
        ID string
        AppBundle string
        Executable string
        Args []string
        WorkingDir string
        SupportsFocus bool
    }
    type TerminalResolver interface {
        Profiles(ctx context.Context) ([]TerminalProfile, error)
        Resolve(ctx context.Context, requested string) (TerminalProfile, error)
        Open(ctx context.Context, profile TerminalProfile, workspace string) (FocusResult, error)
    }

默认终端为 Ghostty。模板只保存公开启动参数，不保存历史、环境变量和凭据。

| 终端 | 标识 | 启动策略 | 失败回退 |
| --- | --- | --- | --- |
| Ghostty | com.mitchellh.ghostty | 激活已有 app，workspace 只在新窗口作为 cwd | open -a 失败则复制 workspace |
| Terminal | com.apple.Terminal | 激活系统 app | 用户允许时回退 Ghostty |
| iTerm2 | com.googlecode.iterm2 | 已验证窗口优先，否则新建 | 未找到窗口则明确提示 |
| Warp | dev.warp.Warp-Stable | 仅调用已验证能力 | 无能力时复制 workspace |
| kitty | net.kovidgoyal.kitty | 需可执行文件和窗口协议 | 回退已安装默认终端 |

验收命令：go test ./pkg/core/config ./internal/bridge ./uiapp -run 'Test(KeyBinding|Keymap|.*Terminal|.*OpenApp)'。覆盖成功、未安装、窗口不存在、未知 profile。

## 6. 词库和后端

### 6.1 当前状态

| 能力 | 状态 | 位置 |
| --- | --- | --- |
| custom/starter/builtin/learned 分层和诊断 | 已实现 | pkg/core/inputengine/dictionary.go:21-367 |
| 用户词库 watcher | 已实现 | internal/watcher/dictionary_watcher.go |
| B3 候选与 bigram 生成 | 已实现 | pkg/core/inputengine/engine.go:830-870 |
| ime_bigrams 记录和内存镜像 | 已实现 | internal/storage/learning_store.go:99-205 |
| B4 多设备学习合并 | 部分实现 | 设计有 MergeLearning，统一接入和来源保留尚未完成 |
| learning schema 迁移 | 部分实现 | 各 SQLite store 有局部 migration，学习快照版本化未统一 |
| 同步入口 | 部分实现 | Client 已上传/删除/同步，UI 对象选择和 unknown 展示未完成 |

### 6.2 热更新诊断、数据流和原子替换

    type DictionaryReloadResult struct {
        OperationResult
        LoadedSources []string
        Diagnostics []DictionaryDiagnostic
        EntryCount int
    }
    type DictionaryReloader interface {
        Reload(ctx context.Context) (DictionaryReloadResult, error)
    }

数据流为 `DictionaryWatcher -> reload callback -> LoadDictionaryWithDiagnostics -> ChainDictionaries -> inputengine`。lookup 热路径只读已构建 dictionary，不做文件 I/O 或 YAML 解析。

watcher 在 debounce 后只触发一次 reload。新 dictionary 完整构建成功前不替换旧指针。构建失败保留旧快照并返回 degraded。诊断只含路径和稳定 Kind，不含词条正文。reload 完成后必须运行 40 词首页和 yijing 黑名单回归。

### 6.3 B4、bigram 和 schema

    type LearnedWord struct { PinyinKey, Word string; Frequency int64; Pinned bool; LastUsed time.Time }
    type LearningSnapshot struct {
        DeviceID string
        Schema int
        SnapshotID string
        Words []LearnedWord
        Bigrams []BigramRecord
    }
    func MergeLearning(snapshots ...LearningSnapshot) (LearningSnapshot, error)
    func MigrateLearningSchema(ctx context.Context, from, to int) error

每台设备独立写快照，读取期合并。相同完整 pinyinKey 加 word 合并频率，bigram 按 prevWord 加 word 合并 transition count。pinned 不压缩、不淘汰。snapshotID 幂等去重。schema 变化使旧缓存失效，迁移失败保留旧表。bigram 达到最小次数门槛才进入候选。

### 6.4 同步入口和顺序

| DataClass | 默认 | 规则 |
| --- | --- | --- |
| input_method | 开 | 词库定义版本和黑名单元数据，不含秘密 |
| learning_data | 开 | 每设备学习快照、合并诊断 |
| clipboard_text | 开 | 仅用户明确选择的文本 |
| media | 开 | 元数据或用户选择的 blob |
| application_context | 开 | 非敏感应用标识 |
| sensitive_vault | 关 | 单独授权 |
| passwords/key_manager | 永不允许 | SyncPlan.Validate 必须拒绝 |

现有接口：pkg/core/sync.Client 的 ConfigurePlan、UploadClass、Delete、Sync、SyncEnabled、Watermark；internal/sync.SQLiteStateStore 的 Load、Save。数据流为 `本地对象 -> SyncPlan.allows -> Client.UploadClass -> encrypted envelope -> server -> Client.Sync -> tombstone cleanup -> StateStore.Save`。UI 验收顺序是读计划和设备状态，上传 learning snapshot，处理 clipboard/media，逐对象显示 submitted/accepted/failed/unknown，应用 tombstone 后更新 watermark，重启后继续且不重复删除。

验收命令：go test ./pkg/core/sync ./internal/sync ./internal/storage -run 'Test(Sync|SQLiteState|Learning|Migration)'。

## 7. 原生 DSH / 飞书通知

### 7.1 当前状态

当前没有 DSH event stream、飞书 webhook 或 Lark API client。pkg/core/pluginprotocol 只提供 JSON-RPC framing/notification，pkg/core/automation 的 notify 是 workflow 输出。因此本主题未实现。通知回执不能冒充实时 RPC，也不能直接改变 AgentSession 状态。

### 7.2 只读事件接口

    type NotificationSource interface {
        Name() string
        Available(ctx context.Context) (bool, error)
        Subscribe(ctx context.Context, cursor string) (<-chan NotificationEvent, error)
    }
    type NotificationEvent struct {
        Source string
        EventID string
        Conversation string
        Sender string
        Kind string
        TextPreview string
        SessionID string
        At time.Time
        Cursor string
    }
    type NotificationStore interface {
        Seen(source, eventID string) (bool, error)
        Put(event NotificationEvent) error
        Recent(limit int) ([]NotificationEvent, error)
    }

数据流是 source capability probe，脱敏规范化，source 加 EventID 去重，本地 store，通知/Agent tab 投影，点击后复用 AgentSessionResolver。DSH 只读订阅，不调用 send、stop 或远程命令。飞书先限定用户已授权的消息、任务/审批和会议事件。无稳定 EventID 时使用 conversation、sender、时间和预览 digest，并标记近似去重。只保存短预览和引用，不缓存完整消息、附件或 token。

通知验收包括重复 EventID 只留一条，断线 cursor 续传，无权限和 schema 变化稳定诊断，以及有/无 SessionID 的跳转分支。计划命令：go test ./pkg/core/notification ./internal/notification ./uiapp -run 'Test(Notification|Event|Dedup|Session)'。当前这些包不存在，实施前必须先创建纯 Go 包和 fake 测试，不能把该命令当作本轮通过证据。

## 8. P0 / P1 / P2、依赖图和 scope

### P0：已有入口诚实可用

| 批次 | 内容 | 依赖 |
| --- | --- | --- |
| P0-A | Agent cache 刷新状态、三源诊断、复制 ID 文案 | agentsession |
| P0-B | Tab/搜索回归和 Agent 末尾契约 | engine/uiapp |
| P0-C | 剪贴板预算、缺失媒体降级、分栏恢复 | clipboard/ui_state |
| P0-D | dictionary 原子 reload、40词和黑名单回归 | watcher/inputengine |
| P0-E | 同步只读状态页 | Client/StateStore |

### P1：受控动作和数据闭环

| 批次 | 内容 | 依赖 |
| --- | --- | --- |
| P1-A | capability probe、resolver、真实 focus、复制 ID 降级 | P0-A |
| P1-B | keymap recorder、冲突 UI、terminal resolver | P0-B |
| P1-C | B4 合并、schema migration、bigram 冷启动快照 | P0-D |
| P1-D | 同步对象映射、幂等上传、unknown | P0-E、P1-C |
| P1-E | DSH/Lark 只读事件 store、cursor、去重 | P1-A |

### P2：增强能力

| 批次 | 内容 | 依赖 |
| --- | --- | --- |
| P2-A | 消息模板预览和显式发送 gated action | P1-A、P1-E |
| P2-B | 多终端窗口定位和 provider attach | P1-A、P1-B |
| P2-C | 通知聚合、跨设备 cursor、媒体同步优化 | P1-D、P1-E |
| P2-D | 端侧句级联想和离线批量挖掘 | P1-C |

依赖关系：现有三源发现 -> P0-A -> P1-A -> P1-E -> P2-C；现有 Tab/IME -> P0-B -> P1-B；现有剪贴板 -> P0-C；watcher/B3 -> P0-D -> P1-C -> P1-D；现有 keymap -> P1-B。每批使用独立包、接口和 fake，避免整仓锁。

## 9. 统一最终验收顺序

1. git diff --check。
2. go test ./pkg/core/agentsession ./internal/agentsession ./pkg/core/clipboard ./internal/storage ./pkg/core/inputengine ./pkg/core/sync ./internal/sync。
3. go test ./uiapp/...，覆盖 Agent、Tab、IME preedit、剪贴板和预览。
4. go test ./...。
5. P1 实现后运行 provider fake、resolver、keymap recorder、sync unknown、notification dedup 专项测试。
6. 生产验收分别记录 discovered、submitted、accepted、target reached 和 artifact/回执。缓存、fixture、复制 ID 和本地预览不能替代后续门。

## 10. 未实现和明确边界

- 没有真实 DSH attach/focus RPC，Agent tab 的 Ghostty 打开不是会话跳转。
- 没有 DSH/Lark 原生通知订阅，通知专项包和 fake 尚未存在。
- B4 学习合并、统一 schema 迁移和 UI 同步入口尚未闭环。
- keymap 已能加载和执行绑定，但无用户录制器和终端能力探测。
- 剪贴板支持文本、图片、文件、视频。链接不自动联网，失败时保持原始 URL。
