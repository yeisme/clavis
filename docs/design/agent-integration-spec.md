# Agent 会话打通规格

- 状态：方案草案，供后续实现任务使用
- 范围：DSH、Codex app-server、Claude Code 会话的只读发现、会话跳转、命令/对话/消息补全
- 边界：本文件不授权发送消息、改变 agent 状态或执行任意远程命令。写操作必须经过显式动作和独立权限检查。

## 1. 现状盘点

| 区域 | 当前事实 | 实现位置 |
|---|---|---|
| Leader | 进入 Leader 时复制配置节点并呈现键位树 | uiapp/capsule_view.go:272-289，pkg/core/engine/capsule.go:432-440 |
| Omnibox tabs | 当前顺序是 actions、clipboard、snippets、ai、glossary | uiapp/capsule_view.go:17-23 |
| Omnibox 打开 | 打开前同步剪贴板、应用列表和触发上下文 | uiapp/controller.go:3443-3457 |
| 搜索输入 | 文本框自动获得焦点，输入变化发出 EventUpdateQuery | uiapp/capsule_view.go:456-517，pkg/core/engine/capsule.go:598-602 |
| Tab 当前语义 | 未修改的 Tab 和 Shift+Tab 都调用 EventSwitchTab | uiapp/capsule_view.go:937-941 |
| 横向切 tab | 引擎保留当前 query，重建目标 tab 候选 | pkg/core/engine/capsule.go:604-620 |
| drill-down 当前基础 | Leader 有 path、breadcrumbs、父级返回状态 | pkg/core/engine/capsule.go:810-820 |
| keymap 动作 | keymap.bindings 由动作注册表分派，当前动作集合是 AI 改写和 IME 切换 | uiapp/controller.go:1465-1482，pkg/core/config/validator.go:801-851 |
| AI 历史状态 | 引擎已有 ChatTurn、AIHistory，但没有 agent 会话实体 | pkg/core/engine/capsule.go:90-105、195-203 |

### 本机 DSH 取证

本机没有找到 state_5.sqlite，也没有在 ~/.dsh 找到 SQLite 文件。可读的会话缓存是 ~/.dsh/storages/session_projcache/sessions/*.json，每个文件含 record.identity.createdAt、record.identity.cwd、record.rows.title、record.rows.sessionStats、record.rows.modelSelection、record.rows.workingDirectory、record.rows.turnOutline 等字段。另有 ~/.dsh/threads/pi/*.jsonl，首行含 type=session、id、timestamp、cwd，后续 type=message 记录角色和消息内容。以上是本机现状证据，不代表所有 DSH 版本都使用同一路径。

state_5.sqlite 的 schema、会话目录的完整 schema、DSH app-server 的会话列举 RPC 和将窗口拉到前台的 RPC，在本机或公开官方页面中未验证。实现必须通过能力探测确认，禁止把推测的方法名写死。

## 2. 目标与非目标

打开 Clavis 后，在一个列表中看见近期 agent 会话，按状态和最后活动时间排序，能跳到原会话，并提供三种不同语义的补全。

- 不扫描或展示完整提示词、密钥、环境变量和工具输出。
- 不在每次按键时启动 agent、app-server 或终端进程。
- 不把文件存在当作 agent 正在运行的证明。
- 不在没有能力声明和用户确认时发送消息、停止运行或修改会话。

## 3. 统一会话模型

~~~go
type AgentSessionStatus string
const (
    AgentRunning AgentSessionStatus = "running"
    AgentWaiting AgentSessionStatus = "waiting"
    AgentStopped AgentSessionStatus = "stopped"
    AgentUnknown AgentSessionStatus = "unknown"
)

type AgentSession struct {
    ID             string
    Provider       string
    Title          string
    Status         AgentSessionStatus
    LastActivityAt time.Time
    LastMessage    string
    PendingReview  bool
    Workspace      string
    TargetRef      string
    Source         string
    ObservedAt     time.Time
}

type AgentSessionStore interface {
    List(ctx context.Context, filter AgentSessionFilter) ([]AgentSession, error)
    Refresh(ctx context.Context, ids []string) ([]AgentSession, error)
    ResolveTarget(ctx context.Context, session AgentSession) (SessionTarget, error)
}

type AgentSessionFilter struct { Provider, Query string; Limit int }
type SessionTarget struct {
    Kind, BundleID, WindowID, SessionID, ReadOnlyURL string
}
~~~

LastMessage 由最后一条用户或 agent 文本截取 160 个 Unicode 字符，去掉代码块、绝对路径、疑似 token 和换行。PendingReview 只在结构化状态明确包含待确认、待验收或 action request 时为 true。仅凭最后一步未结束不能置 true。

| 来源事实 | Clavis 状态 | 备注 |
|---|---|---|
| 进程或 turn 明确 active，最近活动小于 2 分钟 | running | 时间窗可配置 |
| turn 已完成但会话可继续，或最近活动超过 2 分钟且无终态 | waiting | 表示等待输入 |
| 文件或 RPC 明确 completed、stopped、closed | stopped | 仍可跳转历史 |
| 只有文件、无终态字段或解析错误 | unknown | 不猜测运行中 |

## 4. 数据源适配器与刷新

优先使用已声明的 DSH 只读 RPC。其次读取 ~/.dsh/storages/session_projcache/sessions/*.json，再读取 ~/.dsh/threads/pi/*.jsonl。SQLite 适配器只有在实际发现 schema 和只读连接方式后启用。本机未发现 state_5.sqlite，第一版不得依赖它。

~~~go
type AgentSource interface {
    Name() string
    Available(ctx context.Context) bool
    List(ctx context.Context, filter AgentSessionFilter) ([]AgentSession, error)
}
func NewAgentSessionStore(sources []AgentSource, cache Cache) AgentSessionStore
~~~

启动时做一次 2 秒上限的 discovery。面板打开时读取 1 秒内的缓存，后台刷新并通过 EventSetAgentSessions 更新 UI。文件适配器用 mtime、size 和 inode 组成增量键，单次最多读取每个 JSONL 末尾 64 KiB。

若 daemon 未运行或 RPC 不可用，不阻断 Leader。状态卡片显示“仅本地缓存”，并保留文件适配器结果。连续三次解析失败才记录一条汇总诊断，原始内容不写日志。

## 5. 会话发现与三种补全

### 命令补全

解决“我要运行什么动作”。来源是 Clavis action registry、Leader 节点、终端 profile、shell 历史命令名索引，不读取 agent 正文。

~~~go
type CommandCompletion struct {
    ID, Label, Description, Action, Source string
    Score float64
}
func CompleteCommands(ctx context.Context, query string, activeApp string) []CommandCompletion
~~~

索引键为规范化 label、alias、action ID 和 command basename。shell 历史只收集命令首词和最近使用时间，默认不上传、不执行。

### 对话补全

解决“我要切到哪个会话”。来源是 AgentSessionStore.List，索引键是 title、provider、workspace basename、session ID 后 8 位和摘要。输入 @ 或在 Agent tab 直接输入时进入此模式，选择结果只执行受控跳转。

### 消息补全

解决“给当前会话写什么”。第一版只提供本地模板和当前 Clavis 上下文，不自动发送。模板包括“继续上一任务”“总结待验收项”“运行测试并回报证据”，每条模板带 provider 能力要求。

~~~go
type MessageCompletion struct {
    ID, Label, BodyTemplate string
    RequiresWrite bool
    ProviderScopes []string
}
func CompleteMessages(ctx context.Context, session AgentSession, query string) []MessageCompletion
func PreviewMessage(session AgentSession, completion MessageCompletion) (string, error)
func SendMessage(ctx context.Context, session AgentSession, text string, confirm SendConfirmation) error
~~~

SendMessage 是后续 gated action。没有 provider 写能力、目标 session 不匹配、摘要脱敏失败或用户确认时，函数必须返回错误并保持会话不变。

## 6. 跳转策略

首选受控 resolver 激活现有终端。若 TargetRef 能解析到 bundle ID 和窗口标识，先调用 macOS 激活 API或 open -a bundle，再通过窗口标题、工作目录或 DSH session ID 完成定位。失败时显示“已找到会话，但无法定位窗口”，不创建新终端。

次选是 DSH 只读 RPC 提供的 focus、attach 或 open session 能力。只有能力描述明确声明且返回目标 session ID 时才调用。当前本机没有验证该 RPC，不能作为第一版硬依赖。

最终回退是用户明确触发“在目录中打开”动作，调用终端 profile 并把 workspace 作为 cwd，动作标记为“新窗口”。

~~~go
func FocusAgentSession(ctx context.Context, s AgentSession) (FocusResult, error)
type FocusResult struct { Focused, CreatedNewWindow bool; Method, Diagnostic string }
~~~

## 7. UI 与事件接口

引擎新增 AgentSessions []AgentSession，事件新增 EventSetAgentSessions、EventOpenAgentSession、EventFocusAgentSession。EventSetAgentSessions 只替换内存快照。EventOpenAgentSession 进入会话详情页，EventFocusAgentSession 产生 EffectFocusAgentSession，由 controller 调用 resolver。Agent tab 复用 Omnibox 的搜索输入和候选列表，不增加第二套输入状态。

## 8. 同类产品依据

- Raycast 官方 Search Bar 把搜索框作为窗口顶部的唯一入口，Root Search 在下方实时显示结果，支持空 query 的 favorites 和 compact mode。[Raycast Search Bar](https://manual.raycast.com/search-bar)
- Raycast 官方快捷键把 Enter 定义为主动作，Cmd-K 定义为 Action Panel，Action Panel 内可继续输入筛选，Esc 返回上级。[Raycast Keyboard Shortcuts](https://manual.raycast.com/keyboard-shortcuts)、[Raycast Action Panel](https://manual.raycast.com/action-panel)
- Raycast AI Chat 提供聊天搜索、前十个会话的 Cmd-1 到 Cmd-0 跳转、Cmd-J 从 Quick AI 转入完整会话。[Raycast AI Chat](https://manual.raycast.com/ai/ai-chat)
- Warp Command Palette 把 workflows、prompts、files、actions、sessions 作为可筛选类型，并用 sessions: 前缀筛活动 session。[Warp Command Palette](https://docs.warp.dev/terminal/command-palette/)
- VS Code 要求命令名称清晰、带 category 前缀，并明确不覆盖现有快捷键。Clavis agent 命令应使用 Agent: 前缀。[VS Code Command Palette UX](https://code.visualstudio.com/api/ux-guidelines/command-palette)
- 本机 DSH 的 Codex 子代理封装显示 app-server 采用 initialize、initialized、thread/start 和 turn/completed。它只能证明协议形态，不能证明 DSH 已暴露持久会话列表。证据来自本机 /Users/yeisme/.dsh/profiles/web/node_modules/@deepseek-ai/dsh-subagent-codex/README.md 与 src/wire.ts，版本需随依赖升级重新核对。

## 9. 隐私、缓存和失败处理

默认只缓存 ID、标题、状态、时间、workspace basename 和脱敏摘要。provider 读取超时为 1 秒，启动 discovery 为 2 秒。超时保留旧快照并标记 ObservedAt，不清空已显示会话。JSON 结构未知时记录 source 和稳定诊断码，不记录原文。发送结果分为 submitted、accepted、failed、unknown，unknown 不自动重试。

## 10. 取舍与明确不做

选择本地文件快照加可选 RPC 的混合方案，因为本机已有可读缓存，且不会把 DSH daemon 变成 Clavis 启动依赖。选择激活现有窗口优先，因为创建新终端会破坏会话连续性。选择三种补全分开索引，因为命令、会话和消息的排序字段与权限不同。

暂不做完整 transcript 全文索引、远程多机聚合、自动发送消息、跨 provider 统一继续执行、根据摘要推断待验收状态。

## 11. 分批实现与验收

| 批次 | 内容 | 独立验收 |
|---|---|---|
| A1 | AgentSession、文件适配器、缓存和 Agent tab 只读列表 | fixture JSON/JSONL 显示 title、状态、时间、摘要，解析失败不阻断 |
| A2 | 命令、对话、消息三套索引和补全 | 同一 query 分别得到 action、session、template，排序稳定 |
| A3 | 终端 resolver 与窗口激活 | fake resolver 覆盖窗口已找到、窗口不存在、数据源超时 |
| A4 | DSH RPC 能力探测 | 只有 capability 声明存在时发 RPC，未知方法零调用 |
| A5 | gated 消息发送 | 未确认不发送，成功、失败、unknown 可观察，unknown 不重试 |
| A6 | 隐私和性能回归 | 10000 个 JSONL 文件增量刷新不阻塞 UI，日志无正文和凭据 |
