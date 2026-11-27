# DSH / Lark 只读通知 Store 设计（P1-E）

- 任务：`T-clavis-backend-design`
- 目标：把 DSH、飞书等外部事件收敛成可去重、可续传、只读的本地通知流，并让通知点击复用 Agent 会话解析/聚焦能力。
- 硬边界：本设计不发送消息、不停止远端会话、不改远端对象、不把通知回执伪装成 Agent 状态。

## 1. 现状

现状证据：

- `docs/design/clavis-followup-spec.md:357–389`：当前没有 DSH event stream、飞书 webhook 或 Lark API client；现草案只有三个接口和 EventID 去重描述。
- `pkg/core/pluginprotocol/protocol.go:1–4`：JSON-RPC framing/notification 不是 DSH/Lark 事件订阅，也不能作为远端事件源。
- `pkg/core/agentsession/model.go:20–65`：已有 provider-neutral `Session`、`Diagnostic` 和只读 discovery 模型。
- `pkg/core/agentsession/focus.go`：已有 `Focuser`/herdr 真实 focus 能力探测、sessionID 匹配和 `StateUnknown` 降级；通知点击必须复用这一层，不复制 focus 命令。
- `uiapp/agent_sessions.go`：Agent tab 有缓存、诊断和 focus/copy 降级；通知只提供投影和目标引用，不直接修改 Agent session 状态。
- `docs/design/clavis-followup-spec.md:365–387` 的原草案：`Subscribe(ctx, cursor)`、`NotificationEvent` 和 `NotificationStore` 需要补齐 cursor 持久化、近似去重和只读能力边界。

## 2. 结论：Source 负责读取，Store 负责本地事实，UI 只读投影

数据流固定为：

```text
DSH/Lark source
  -> capability probe
  -> read-only event stream
  -> normalize + redact + classify dedup
  -> NotificationStore.Put / cursor commit
  -> Agent/notification snapshot
  -> click: SessionID -> AgentSessionResolver/Focuser
```

Source 不返回未经脱敏的完整 payload 给 UI。Store 不调用外部 API。UI 不持有 source token，也不在点击时直接调用 `send`、`stop` 或任意远端写 API。

## 3. 最终接口签名

接口位于纯 Go core 包（建议 `pkg/core/notification`）；`internal/notification` 只放 DSH/Lark adapter 和本地 SQLite 实现。接口不依赖 MyGo、UI、具体 Lark SDK 或 DSH CLI。

```go
package notification

type NotificationSource interface {
    Name() string
    Available(ctx context.Context) (bool, error)
    // cursor 是本 source 的 opaque cursor；source 不得解释其他 source 的 cursor。
    // 返回事件和 next cursor；事件顺序必须是 source 保证的稳定顺序。
    Read(ctx context.Context, cursor string, limit int) (events []NotificationEvent, nextCursor string, err error)
}

type DedupClass string

const (
    DedupStable  DedupClass = "stable"
    DedupApprox  DedupClass = "approximate"
)

type NotificationEvent struct {
    Source          string
    EventID         string
    DedupClass      DedupClass
    DedupKey        string
    ConversationRef string
    SenderRef       string
    Kind            string
    TextPreview     string
    SessionID       string
    At              time.Time
    SourceCursor    string
    ObservedAt      time.Time
}

type NotificationPage struct {
    Events       []NotificationEvent
    NextCursor   string
    HasMore      bool
    Source       string
}

type NotificationStore interface {
    // Seen 判断稳定/近似去重键是否已经入库；key 已包含 source 前缀。
    Seen(ctx context.Context, source, dedupKey string) (bool, error)
    // Put 只写本地规范化事件；同一 dedupKey 必须幂等。
    Put(ctx context.Context, event NotificationEvent) error
    // CommitCursor 只有 Put 成功后才能调用，表示该 source cursor 已安全消费。
    CommitCursor(ctx context.Context, source, cursor string) error
    Cursor(ctx context.Context, source string) (string, error)
    Recent(ctx context.Context, source string, limit int) (NotificationPage, error)
}
```

### 3.1 相对原草案的收敛

原草案是：

```go
type NotificationSource interface {
    Name() string
    Available(ctx context.Context) (bool, error)
    Subscribe(ctx context.Context, cursor string) (<-chan NotificationEvent, error)
}
type NotificationStore interface {
    Seen(source, eventID string) (bool, error)
    Put(event NotificationEvent) error
    Recent(limit int) ([]NotificationEvent, error)
}
```

本设计的改动是确定性的：

1. `Subscribe` 改为 `Read(...)(events,nextCursor,error)`：Source 不能把不可控的 goroutine/channel 生命周期泄露给 Store/UI；批量页更容易实现断线续传、deadline、背压和测试。
2. `EventID` 不再是唯一去重入口，增加 `DedupClass`、`DedupKey`：拿不到稳定 EventID 时必须显式标记 approximate。
3. `NotificationEvent` 增加 `ConversationRef`、`SenderRef`、`SourceCursor`、`ObservedAt`，并统一字段名；只存引用和短预览，不存原始 payload。
4. Store 的方法全部带 `context.Context`，避免 IO 永久卡住；`Recent` 按 source 分片，避免不同 source 的 cursor 混用。
5. 增加 `CommitCursor`/`Cursor`：事件落库和 cursor 提交必须分开但在同一 Store 事务中由实现组合，防止先推进 cursor 后丢事件。
6. `Seen` 只读判断，`Put` 必须幂等；不能要求调用方用“先 Seen 再 Put”实现去重，因为并发 source 会产生竞态。

## 4. 标准化、脱敏与三种状态

事件进入 Store 前必须经过 normalize：

- `Source` 使用固定白名单值，如 `dsh`、`lark`；未知 source 直接拒绝。
- `EventID` 稳定时取 source 官方/协议 ID，trim 后限制长度；不把整个 payload hash 当 EventID。
- `TextPreview` 只保留短预览（建议最多 240 个 Unicode rune），执行 `agentsession.SanitizeDiagnosticText` 同等规则：去 token、绝对路径、代码块和控制字符。完整消息、附件、富文本原文不入库。
- `ConversationRef`、`SenderRef` 是 opaque 引用或脱敏标识，不存手机号、邮箱、access token 或完整 URL query。
- `SessionID` 只有 source 明确能绑定时才填；不能从标题/正文猜 UUID。
- `At` 用 source 时间；缺失时使用 `ObservedAt` 并在诊断中标时间不可靠。
- `SourceCursor` 只作为 source opaque string 持久化，限制长度和字符集；不交叉解释。

### 4.1 稳定 EventID 去重

有稳定 EventID 时：

```text
DedupClass = stable
DedupKey   = source + "\x00stable\x00" + EventID
```

数据库对 `(source, dedup_key)` 建唯一约束。重复 Put 返回成功/`already_seen` 等价语义，不产生第二条 Recent 事件，也不推进错误的 cursor。

### 4.2 近似去重

拿不到稳定 EventID 时，构造规范化近似键：

```text
preview_digest = SHA-256(normalized_text_preview)
DedupKey = source + "\x00approx\x00" +
           conversation_ref + "\x00" + sender_ref + "\x00" +
           at_bucket + "\x00" + preview_digest
```

- `at_bucket` 初版按 60 秒 UTC bucket；source 若只能给日期级时间，使用更宽 bucket 并把结果标 approximate。
- 近似键不是事件身份，只用于有限窗口抑制重复；建议保存 `dedup_expires_at`，默认 7 天后允许同样内容再次作为新事件。
- `DedupClass=approximate` 必须进入诊断和数据模型，UI 可以显示“可能重复”，不能声称绝对去重。
- 同一 conversation/sender/preview 在时间桶内重复时只保留第一条；不同 cursor 的重复不覆盖更早的 source cursor，cursor commit 仍按读取页推进。

## 5. Cursor 持久化和断线续传

结论：**cursor 存应用 SQLite 的独立 notification 表，不存普通文件。** 理由是事件、去重键、cursor 需要事务性更新；单独文件容易出现“事件已写但 cursor 未写”或反向情况。迁移由应用库 schema 版本管理（见 `schema-migration.md`），通知表使用独立版本号/迁移步骤，不修改 sync 库。

建议表：

```sql
CREATE TABLE notification_events (
    source TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    dedup_class TEXT NOT NULL,
    event_id TEXT NOT NULL DEFAULT '',
    conversation_ref TEXT NOT NULL DEFAULT '',
    sender_ref TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    text_preview TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    at_ns INTEGER NOT NULL,
    source_cursor TEXT NOT NULL DEFAULT '',
    observed_at_ns INTEGER NOT NULL,
    dedup_expires_at_ns INTEGER,
    PRIMARY KEY (source, dedup_key)
);
CREATE INDEX idx_notification_recent ON notification_events(source, at_ns DESC, dedup_key);

CREATE TABLE notification_cursors (
    source TEXT PRIMARY KEY,
    cursor TEXT NOT NULL,
    committed_at_ns INTEGER NOT NULL
);
```

消费事务语义：

```text
Read(source, cursor, limit)
  -> normalize each event
  -> BEGIN
       for each event: INSERT ... ON CONFLICT(source,dedup_key) DO NOTHING
       if all inserts/duplicate checks succeed:
           UPDATE notification_cursors SET cursor = nextCursor
     COMMIT
```

Store 的 `Put` 和 `CommitCursor` 是公开接口以便测试/组合，但生产 batch consumer 必须使用实现提供的原子 `PutPageAndCommitCursor`（或把二者放在同一 Store transaction）避免跨调用崩溃窗口。若保留最小接口，不允许 source 自己先写 cursor。

断线语义：

1. 读取使用上次已提交 cursor；未提交的页下次重复读取。
2. 重复事件由稳定/近似 dedup key 幂等过滤。
3. 只有整页规范化、写事件和 cursor 更新成功后才提交 next cursor。
4. source 返回 cursor 过期/无效时，不把它静默当空；返回 `cursor_expired`，保留旧 cursor，并根据 source 能力执行明确的 bounded resync（例如最近 N 分钟/页），同时将可能重复事件交给 Store 去重。
5. source 断线、权限失效、schema 变化返回诊断，不删除旧通知；下次手动/定时刷新可重试。
6. 不支持 cursor 的 source 用空 cursor + bounded watermark/approx key；必须标记其至少一次语义，不能宣称 exactly-once。

## 6. 只读边界

`NotificationSource` 实现必须满足：

- 只允许 `list/read/subscribe/get history` 等读取动作。
- DSH adapter 禁止调用 `send`、`stop`、远端命令、任意 shell；禁止把事件内容拼成命令参数。
- Lark adapter 只能使用已授权的只读事件/历史 API；禁止发送消息、回复、撤回、审批操作、任务更新或会议控制。
- 不提供 `Send`、`Stop`、`Mutate`、`AckRemote` 方法；远端 webhook 回执不能冒充消息已送达。
- Store 是本地持久化，不是远端 outbox；`Put` 不产生任何远端副作用。
- 事件里拿不到稳定 EventID 时，`EventID` 置空、`DedupClass=approximate`，不能把近似 digest 填回 `EventID` 欺骗下游。
- 任何 source 返回的未知字段只丢弃，不把原始 JSON 保存到诊断或 Store。

Source 能力探测：

```go
type SourceCapabilities struct {
    ReadEvents bool
    StableEventID bool
    Cursor bool
    History bool
    ObservedAt time.Time
}

func (s Source) Capabilities(ctx context.Context) (SourceCapabilities, error)
```

能力探测失败表示 source unavailable/degraded，不降级到写 API，也不把“能调用某个写接口”当作读能力。

## 7. 与 Agent tab 的关系

通知点击有两条路径：

### 7.1 有 SessionID

1. 读取事件的 `SessionID` 和 `Source`。
2. 通过 `AgentSessionResolver.Resolve(ctx, Session{Provider: source, ID: sessionID, ...})` 验证目标是否仍存在；不能仅凭通知字段直接 focus。
3. resolver 返回 target 后调用已存在的 `Focuser.Focus`/provider focus seam。
4. 成功显示“已跳转到该会话”。
5. resolver 未找到返回 `StateUnknown, Retryable=false`，显示“已复制会话 ID（未找到该会话的面板）”。
6. herdr 不可用时走现有复制 ID 降级，显示“已复制会话 ID（未检测到 herdr，无法直接跳转）”。

### 7.2 没有 SessionID

- 只显示短预览、source、conversation 引用。
- 若 source 能用稳定 conversation→session 映射，必须经过 resolver 生成 `SessionID` 后再 focus；不从正文正则猜。
- 没有映射时点击只做复制引用/打开本地 Agent 列表，文案明确“未关联 Agent 会话”，不得伪称聚焦成功。
- 通知点击不改变 Agent tab 的 session 状态；下一次 Agent refresh 仍由各 provider 只读发现决定。

## 8. 上线步骤与回滚

1. 先实现纯 Go normalization、stable/approx dedup 和 fake source/store conformance，不接真实 DSH/Lark。
2. 引入应用库 notification 表和迁移；旧数据库不含表时创建，未知 schema 版本拒绝写入。
3. 接 DSH 只读 source：先 capability probe，再 bounded `Read`；记录 cursor 过期、权限、schema 错误。
4. 接 Lark 只读 source：限定授权域和事件类型，拒绝写 API；用 fixture 验证 event schema 变化。
5. 将通知投影接入 Agent/通知列表，默认只显示短预览和来源；点击复用 resolver/focus，不新造跳转。
6. 灰度开启持久化，观测 dedup hit、approx 比例、cursor lag、cursor_expired、权限失败和最近事件数量。
7. 回滚 source adapter 时保留本地事件和 cursor，但停止该 source 消费；恢复后从已提交 cursor 继续。
8. 回滚 schema 时按 `schema_too_new` 进入通知只读/隐藏写入状态，不能删除 notification 表或重置 cursor；必须用匹配版本迁移。

## 9. 可执行验收

纯 Go conformance：

```bash
cd <clavis checkout>
go test -count=1 -race -v ./pkg/core/notification ./internal/notification -run 'Test(Notification|Event|Dedup|Cursor|ReadOnly)'
```

必须具名覆盖：

- 相同 stable EventID Put 两次只有一条事件。
- 相同 approximate key 在时间窗口内只有一条，并且 `DedupClass=approximate`。
- 不同 source 的相同 EventID 不互相去重。
- `Put` 成功、cursor 未提交时重启会重复读但不会重复存储。
- 事件和 cursor 在同一事务中提交；注入写失败时二者都不前进。
- cursor 过期返回 `cursor_expired`，不静默清空旧事件。
- 事件预览中的 token、绝对路径、代码块、完整 payload 不出现在 Store 或 UI 投影。
- source fake 没有 `send/stop/mutate` 调用入口；静态接口检查不出现这些方法。
- schema 变化和权限失败进入稳定诊断，旧事件仍可读。

Agent/UI 接线：

```bash
go test -count=1 -race ./pkg/core/agentsession ./uiapp -run 'Test(Notification|Agent.*Session|Focus|Dedup)'
```

断言有 SessionID 成功复用 resolver/focus；无 SessionID 不产生 focus 调用；herdr 不可用、未匹配和成功三档文案与 P1-A 一致。

## 10. 取舍与风险

取舍结论：押“source 只读批量分页 + SQLite 事件/cursor 事务 + stable/approx 双去重 + Agent resolver 复用”，否掉 channel-only Subscribe、文件 cursor、把近似 digest 冒充 EventID 和通知直连远端写 API。

最大翻车风险是 source cursor 与事件写入不在同一事务，导致断线后丢事件；实现必须用 page transaction 或 `PutPageAndCommitCursor`，不能让调用方手工先 `Seen`、再 `Put`、最后改 cursor。其次是 Lark/DSH schema 变化可能把正文/附件意外落盘，必须在 adapter 边界做字段白名单、长度上限和脱敏，原始响应不可进入 Store。第三是 conversation→Agent session 的映射不稳定，找不到时必须返回 unknown/复制引用，不能为“看起来像跳转”而猜 ID。
