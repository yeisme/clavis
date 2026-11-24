# Security Vault 执行级设计

> 阶段：spec
> 范围：Keychain/SecretRef、user-only 复制、敏感 TTL、审计、Agent 禁止读取正文
> 本轮只产出设计，不修改生产代码。

## 一、问题定义

Clavis 需要保存密码、令牌、恢复码和其他高敏感值，同时让用户可以在目标应用中使用这些值。当前设计必须同时满足四个边界：

1. Secret 正文只能由受信任的本机 Keychain 适配器和用户发起的短时动作接触。
2. Agent、Agent 工具、计划步骤、日志、指标、错误消息和普通剪贴板历史都不能观察 Secret 正文。
3. 用户复制敏感值后必须自动过期，清理动作不能误删用户随后复制的新内容。
4. 每次保存、读取元数据、复制、过期、撤销、失败和降级都可审计，审计记录本身不能成为泄漏通道。

受影响的主体是密码管理用户、Agent 运行时、Host/Clipboard 桥接层和 Keychain 后端。用户需要可验证的复制结果，Agent 需要知道某个 SecretRef 是否可用以及下一步由谁处理，但 Agent 不需要也不应获得正文。

当前能凑合使用的路径是让 AI profile 通过 APIKeyStore 直接解析出 string，再由调用方自行决定是否放入请求、诊断或其他动作。文件层、环境变量和旧 Keychain 迁移已经有边界，但没有 SecretRef、调用者身份、user-only 复制、TTL ownership 或 Agent 禁读的统一契约。安全边界必须在数据契约和调用者身份处固定下来，不能由 UI 文案或调用方自律维持。

## 二、现状证据

- `internal/secrets/secrets.go:27-68` 当前的权威抽象是 `APIKeyStore`，读写接口直接返回 `string`，来源枚举为 `none|keychain|env|file`。`MyGoSecretsStore` 在 `:70-150` 封装 OS Keychain/Credential Manager/Secret Service，`MemoryAPIKeyStore` 是测试 fake store。
- `internal/secrets/file_store.go:15-37,56-105,107-148` 当前生产优先路径是独立的 `0600` `ai-keys.toml`，父目录 `0700`，临时文件原子 rename，权限过宽或解析失败会返回错误而不会静默回退。
- `internal/secrets/layered_store.go:12-25,37-75,122-146` 的 `LayeredStore` 文件优先、写入只落文件，`Migrate` 默认关闭；显式迁移才从旧 Keychain 复制到账户文件。`file_store_test.go:71-88,130-169` 验证禁用迁移时零 Keychain 调用、写入只到文件和迁移幂等。
- `pkg/core/config/main_config.go:325-338,531-540,543-605` 的 `AIProfile` 只有 `keychain_account`、`api_key_env_var` 等引用字段，配置解码明确拒绝 `api_key`、`apiKey`、`secret_key`、`secretKey` 原文。
- `internal/secrets/secrets.go:371-444` 的 profile 解析按账户、显式环境变量和 provider 默认变量处理 keyless。`docs/backend/ai-provider-magpie-env.md:100-124,132-148` 明确文件优先、环境变量 fallback、非 loopback provider 不得无 key 成功。
- `docs/backend/schema-migration.md:20-39,79-94,177-185` 要求每个 SQLite 文件独立版本、迁移前备份、单库事务、未知版本拒绝和失败回滚。`docs/backend/notification-store.md:120-157,206-215` 要求短预览白名单、cursor 事务和原始响应不入库，这些规则同样适用于 Secret 审计。
- 当前代码没有 SecretRef、user-only copy、TTL ownership marker 或 metadata-only audit 的实现。它仍有直接返回 key 正文的 `APIKeyStore` 路径，因此本设计必须把现有 APIKeyStore 作为兼容层，新增安全边界，而不能把当前接口误称为已完成的 Vault。

## 三、方案对比

### 方案 A：Secret 正文进入应用数据库，由 Agent 通过权限读取

**怎么做**

- 在应用数据库中保存 Secret 正文和元数据。
- Agent 通过 secret.read 携带权限令牌读取正文。
- 复制、审计和 UI 都复用同一条读取路径。

**改动面**

- 数据库 schema、备份、迁移、同步、Agent 工具、日志脱敏和所有调试路径都要处理正文。
- 需要新增权限、令牌轮换和泄漏响应机制。

**收益**

- 跨平台读取路径统一。
- 可在没有系统 Keychain 的环境中运行。

**代价**

- 数据库、备份、崩溃转储、同步包和 Agent 都成为 Secret 正文的暴露面。
- Agent 禁止读取正文只能靠运行时权限，无法从接口形状上保证。

**翻车条件**

- 任意一条 SQL、序列化、错误包装或工具调试输出漏出正文。
- 权限令牌被重放，或备份被复制到非用户设备。

**为什么没选**

- 与 user-only 和 secret non-observability 冲突。正文不应进入应用持久层，也不应成为 Agent 的可调用资源。

### 方案 B：Keychain 保存正文，应用只保存 SecretRef，用户复制走专用短时通道

**怎么做**

- 系统 Keychain 保存正文，应用保存不可逆的 SecretRef 元数据。
- 先保留现有 APIKeyStore 作为兼容 adapter，新增不返回正文的 metadata/read boundary；FileStore 和 LayeredStore 迁移为 SecretRef 后再关闭兼容读取。
- Agent 只能读取元数据和状态，不能调用正文读取接口。
- 只有带用户会话和一次性授权的 Host/Clipboard 路径可以解析正文并写入系统剪贴板。
- 清理任务只在剪贴板内容仍匹配本次复制凭据时执行。

**改动面**

- 新增 KeychainStore、SecretRef、user-only copy、TTL 状态机和 metadata-only audit。
- 现有密码条目、复制入口和普通历史需要转为引用模式。
- 需要测试调用者身份、错误降级、重启、并发复制和 Keychain 锁定。

**收益**

- Secret 正文的持久化面和可观测面最小。
- Agent 接口天然只能返回引用与状态。
- 可把系统 Keychain 的锁定、权限和用户确认映射为明确状态。

**代价**

- 跨平台 Keychain 能力不完全一致。
- 当前 FileStore 和 APIKeyStore 会在受信任的 AI 调用路径返回正文，迁移期间必须同时维护旧接口和新边界，不能直接删除旧接口。
- 用户复制是异步且可能处于 unknown，需要明确反馈和重试语义。
- 自动清除无法保证系统剪贴板永远未被外部程序读取，只能保证 Clavis 不继续保留。

**翻车条件**

- 复制清理没有携带本次操作凭据，误清掉用户的新复制内容。
- 调试、错误或审计把 SecretRef 解析结果当成正文记录。
- Agent 被错误地授予 user-only actor。

**为什么选**

- 最符合 user-only、Keychain 权威存储和 Agent 禁止读取正文三个硬约束。失败时可以返回 unknown/degraded，不需要冒充复制成功。

### 方案 C：外部 Secret Provider 托管正文，Clavis 保存 provider 引用

**怎么做**

- 通过 provider URI 和版本保存 SecretRef。
- 每次用户复制时由 provider 解析，Clavis 只在内存中转发到系统剪贴板。
- Agent 只拿 provider 元数据和可用性状态。

**改动面**

- provider 认证、网络重试、租约、超时、缓存、断网和账户切换。
- 审计需要跨进程关联，且不能把 provider 返回正文写入日志。

**收益**

- 不要求本机 Keychain 存储全部正文。
- 适合企业密钥服务和团队共享 Secret。

**代价**

- 网络、远端权限和 provider 可用性增加失败面。
- 用户复制已完成与 provider 已接受请求之间存在 unknown 窗口。
- 远端 provider 可能拥有比本机更广的访问记录。

**翻车条件**

- 自动重试造成重复读取或重复复制。
- provider 返回超时但实际已授权，客户端错误地重放请求。
- provider 元数据中含有 Secret 正文或可逆凭据。

**为什么没选为第一期**

- 依赖外部凭据和网络，无法先证明本机 user-only 边界。可作为第二期 provider 适配器，必须复用方案 B 的 SecretRef 和 Agent 禁读契约。

## 四、定稿方案

### 4.1 数据契约

#### 现有兼容契约

实现阶段必须继续满足 `APIKeyStore` 的现有调用者，直到迁移完成：

- `ReadKey`、`ResolveAPIKey`、`ResolveAPIKeyWithEnv` 只允许在受信任的 provider request boundary 内调用，禁止被 Agent tool、诊断 snapshot、通知投影和普通历史调用。
- `ResolveAPIKeySource` 只返回 `none|keychain|env|file`，不返回正文。`FileStore` 的 `0600`、原子写、错误不降级和 `LayeredStore.Migrate=false` 语义必须保留。
- `MemoryAPIKeyStore` 继续作为 fake store，仅用于测试，不得成为生产默认后端。
- 兼容 adapter 的每次正文读取必须有内部 audit correlation，但不得记录正文、正文摘要或环境变量值。

#### SecretRef

SecretRef 是唯一对 Agent、UI、审计和普通存储可见的引用对象：

| 字段 | 类型 | 缺省值 | 说明 |
|---|---|---:|---|
| id | string | 必填 | 本机稳定随机 ID，不含正文、路径或可逆编码 |
| backend | enum | layered | 权威后端类型，第一期允许 layered/keychain/env |
| record_key | string | 必填 | 现有 `keychain_account` 或账户文件 key，禁止把正文放入 key |
| label | string | 空 | 用户可见标签，禁止写密码和令牌 |
| kind | enum | password | password/token/recovery_code/other |
| sensitivity | enum | secret | secret/high，不能降为普通文本 |
| version | uint64 | 1 | Keychain 版本，用于并发更新和审计 |
| created_at | timestamp | 必填 | 元数据时间 |
| updated_at | timestamp | 必填 | 元数据时间 |
| state | enum | stored | 当前引用状态，见状态机 |
| revoked_at | timestamp? | 空 | 撤销时间 |
| metadata_revision | uint64 | 1 | 元数据 CAS 版本 |

禁止字段：

- value、plaintext、secret、token、clipboard_text。
- 任何可由 label、record_key 或错误消息反推出正文的字段。
- 正文摘要、正文长度和正文熵等可形成侧信道的派生字段，除非主人单独批准。

权威来源是由 backend + record_key + version 定位的 layered store。layered 的读取优先级沿用现状：`ai-keys.toml` 文件、显式环境变量、provider 默认环境变量，旧 Keychain 只在显式迁移期间可读。迁移完成后，Keychain 或其他后端才可作为正文权威来源。数据库、Agent 状态、UI 行和审计记录都是派生视图。数据库只允许保存 SecretRef 和非敏感元数据。应用重启、同步和备份都不得复制正文。

#### UserCopyRequest

| 字段 | 类型 | 规则 |
|---|---|---|
| request_id | string | 客户端生成的幂等键，重试必须复用 |
| secret_ref_id | string | 只接受本机已存在的 SecretRef |
| actor | enum | 必须是 user_session，Agent/automation/plugin 拒绝 |
| session_id | string | 当前用户会话标识，不能由 Agent 自填 |
| ttl | duration | 默认 30s，上限 5m，超限裁剪并返回明确状态 |
| destination | enum | 第一阶段只允许 system_clipboard |
| reason | enum | user_copy 或 user_paste_prepare |

request_id + secret_ref_id + session_id 是幂等键。相同请求返回同一个 CopyReceipt，不得再次读取正文或创建新的清理任务。

#### CopyReceipt

| 字段 | 类型 | 说明 |
|---|---|---|
| request_id | string | 原请求 ID |
| state | enum | copied/expired/failed/unknown/degraded |
| expires_at | timestamp | 计划清理时间 |
| cleanup_token | opaque | 仅 Host 内部持有，不序列化给 Agent |
| observed_at | timestamp | 状态观察时间 |
| retryable | bool | 是否可安全重试 |

Receipt、Agent 事件和 UI 状态都不得包含正文。Agent 只能看到 state、expires_at、retryable 和不含正文的错误码。

### 4.2 接口与调用边界

以下是目标接口名，不是当前已存在的 Go 接口。当前唯一可复用的存储接口是 `internal/secrets.APIKeyStore`。

```text
UserSession
  -> VaultMetadataService      读取 SecretRef 元数据
  -> UserCopyService           申请一次性复制
  -> HostClipboardAdapter      写入系统剪贴板并按 cleanup_token 清理

Agent
  -> VaultMetadataService      只能读元数据和状态
  -> OperationStatusService    只能读状态、错误码、下一步 actor
  -X UserCopyService.Resolve   禁止调用
  -X KeychainStore.GetValue    不暴露给 Agent runtime
```

抽象接口契约：

- `LayeredSecretStore` 先包住现有 `APIKeyStore`，暴露 `GetMetadata(ref)`、`ResolveForUser(ref, session, requestID)` 和 `ResolveSource(ref)`；旧的返回 string 方法只在兼容 adapter 内可见。
- KeychainStore.Put(ref, value, expectedVersion)：仅由用户创建/更新入口调用。正文只在调用栈内存中出现，成功后立即释放引用。版本冲突返回 version_conflict，不能覆盖写。
- KeychainStore.GetMetadata(ref)：返回非敏感元数据，可被 Agent 通过 metadata service 调用。
- KeychainStore.ResolveForUser(ref, session, requestID)：仅 user-only actor 可调用。返回内部 opaque value handle，不允许 JSON、日志或 Agent tool result 携带。Keychain 锁定返回 keychain_locked。
- UserCopyService.Copy(request)：校验 actor、session、ref 状态、TTL 和幂等键。将正文写入系统剪贴板后立即释放正文引用。返回 metadata-only CopyReceipt。
- HostClipboardAdapter.ClearIfOwned(cleanupToken)：只有剪贴板仍由本次请求拥有时才清理。内容已被用户或其他程序替换时返回 ownership_lost，不能清理新内容。
- AuditSink.Append(event)：只接收 metadata-only event。审计失败不能阻止用户复制，但必须让复制结果标记 degraded 并触发本地告警计数。

错误码固定为：

| 错误码 | 状态 | retryable | 含义 |
|---|---|---:|---|
| secret_ref_not_found | failed | false | 引用不存在或已删除 |
| secret_revoked | failed | false | 引用已撤销 |
| keychain_locked | degraded | true | 需要用户解锁系统 Keychain |
| user_auth_required | degraded | true | 缺少当前用户确认 |
| agent_forbidden | failed | false | Agent 或自动化主体试图读取/复制正文 |
| ttl_invalid | failed | false | TTL 超出允许范围 |
| clipboard_write_unknown | unknown | true | 写入结果无法确认 |
| cleanup_ownership_lost | degraded | false | 清理时剪贴板已被替换 |
| audit_write_failed | degraded | true | 复制完成但审计落盘失败 |
| version_conflict | failed | true | SecretRef 版本发生并发变化 |

任何未知的 Keychain、Host 或系统剪贴板结果都必须返回 unknown 或 degraded，不得返回 copied。

### 4.3 状态机

状态集合：

- absent：没有可用 SecretRef。next_actor = user
- stored：SecretRef 存在，正文由 Keychain 持有。next_actor = user_or_agent_metadata
- locked：Keychain 暂不可用。next_actor = user
- copy_pending：用户复制请求已受理，结果尚未确认。next_actor = host
- copied：系统剪贴板写入已确认，TTL 清理已排程。next_actor = cleanup_worker
- expired：本次复制的 TTL 已到期且清理成功，SecretRef 仍可复用。next_actor = none
- ownership_lost：清理时发现剪贴板已被替换。next_actor = user
- revoked：SecretRef 不再允许使用。next_actor = user
- degraded：正文动作可能完成，但审计、清理或能力确认不完整。next_actor = user
- unknown：无法确认正文动作结果。next_actor = user
- failed：确定未完成。next_actor = user_or_retry

允许跃迁：

```text
absent -> stored                 user creates or imports SecretRef
stored -> locked                 Keychain reports locked
locked -> stored                 user unlocks and metadata revalidated
stored -> copy_pending           user submits valid UserCopyRequest
copy_pending -> copied           Host confirms clipboard ownership
copy_pending -> unknown          timeout, disconnect, or ambiguous Host result
copy_pending -> failed           deterministic refusal
copied -> expired                cleanup token still owns clipboard at TTL
copied -> ownership_lost         clipboard changed before TTL cleanup
copied -> degraded               cleanup or audit cannot be confirmed
stored -> revoked                user revokes SecretRef
unknown -> copied                explicit reconciliation confirms ownership
unknown -> degraded              reconciliation cannot confirm
```

Agent 只能观察状态和 next_actor，不能触发 stored -> copy_pending，不能触发任何正文解析跃迁。

### 4.4 审计契约

审计事件字段：

- event_id、request_id、secret_ref_id
- actor_class：user_session/agent/automation/host/cleanup_worker
- action：create/update/metadata_read/copy_requested/copy_confirmed/cleanup/revoke/deny
- result_state、error_code、retryable
- session_id_hash、at、app_bundle_id、correlation_id
- ttl_seconds、ref_version

禁止审计字段：

- Secret 正文、正文摘要、剪贴板文本、Keychain 返回值、opaque cleanup token。
- 原始 session ID、provider access token、完整目标窗口内容。

审计查询只返回事件元数据。审计写入失败必须可观测，但错误信息不能拼接底层 Keychain 错误正文。

### 4.5 复制与清理竞态

每次复制创建内部 cleanup_token，其中包含随机 nonce、request_id 和期望的剪贴板 ownership marker。Marker 只用于 Host 判断仍是本次写入，不能是正文或正文哈希。

清理规则：

1. 到期时读取 Host ownership marker。
2. Marker 匹配才清空。
3. Marker 不匹配返回 cleanup_ownership_lost，保留用户新内容。
4. 清理任务重复执行必须幂等。
5. 应用重启后未完成的清理任务进入 unknown，不直接清空系统剪贴板。

### 4.6 迁移顺序与 commit 粒度

每一步都必须能独立编译、测试、回滚。后一步不能假设前一步已经把正文写入数据库：

1. `docs/security-vault-contract`：冻结 SecretRef、actor、错误码、禁止字段和审计白名单，只改契约测试与文档。
2. `secrets/metadata-adapter`：在 `internal/secrets` 增加 metadata-only adapter 和 fake store，保留现有 APIKeyStore 行为。失败时只回滚 adapter，不改变 FileStore/LayeredStore。
3. `secrets/user-copy`：新增 user-only copy、TTL、ownership marker 和清理状态机。Keychain 锁定、Host unknown 和审计失败先由 fake store 覆盖。
4. `secrets/agent-deny`：在 Agent 调用边界拒绝正文读取和复制请求，返回 `agent_forbidden`。这一提交必须独立验证 Agent snapshot、diagnostics 和 tool result 无正文。
5. `secrets/migrate-ref`：把现有 `keychain_account`、FileStore account 和 env source 映射成 SecretRef。迁移只新增引用和审计元数据，不把正文复制进应用 SQLite。
6. `secrets/retire-raw`：在所有受信任调用者迁移并通过兼容测试后，收窄旧 APIKeyStore 的可见范围。若任一调用者未迁移，停止在此步，不删除旧实现。

迁移使用独立的 SecretRef ledger 版本，不修改应用 SQLite、sync SQLite 或 device state 的 `user_version`。迁移前生成私有权限备份，失败保留旧 FileStore/Keychain 条目并返回明确错误，不生成空库覆盖原数据。

## 五、分期与验收

### P0：契约与不可观测边界

交付：

- 新增 SecretRef、UserCopyRequest、CopyReceipt、审计事件和状态机契约文档。
- 明确 Agent metadata-only 接口和 user-only copy 接口。
- 明确正文不能进入数据库、JSON、日志、指标、错误和 Agent tool result。

机器断言：

- `go test -count=1 -v ./internal/secrets ./pkg/core/config -run 'Test(DefaultEnvVarForProvider|LayeredStore|FileStore|.*ReasoningEffort.*)'` 必须通过，且输出包含现有 FileStore 权限、文件优先和禁用迁移测试。
- `grep -RInE 'api_key|apiKey|secret_key|secretKey' pkg/core/config` 只能命中禁止字段解码和对应测试，不能出现 raw secret 字段定义。
- 新增 SecretRef 序列化测试中，Agent-facing JSON 不得包含 value/plaintext/secret/token 字段；当前 APIKeyStore 的正文返回只能在受信任 adapter 测试中出现。
- Agent 工具测试中，所有返回 JSON 不包含 value/plaintext/secret/token 字段。
- 以 Agent actor 调用复制或正文解析接口必须得到 agent_forbidden 和 failed。
- `go test -race -count=1 ./internal/secrets ./pkg/core/config` 通过，且测试只使用 MemoryAPIKeyStore、临时目录和显式环境变量。

### P1：Keychain 与 user-only 复制

交付：

- KeychainStore 第一版适配器。
- UserCopyService 身份校验、幂等、TTL、ownership marker 和清理 worker。
- Keychain 锁定、Host unknown、审计失败和重启恢复路径。

机器断言：

- `go test -count=1 -v ./internal/secrets -run 'Test(LayeredStore|FileStore|.*Migration.*)'` 必须显示 `=== RUN` 和 `PASS`，不得以匹配零用例的 `ok` 作为迁移完成证据。
- 相同 request_id + secret_ref_id + session_id 重试只产生一个复制事件和一个清理任务。
- TTL 只接受默认值和上限范围，越界返回 ttl_invalid。
- 用户替换剪贴板后，清理返回 cleanup_ownership_lost，新内容保持不变。
- Host 写入未知时返回 unknown，不能返回 copied。
- Keychain 正文不会出现在测试日志、错误字符串、JSON 快照和审计表。

### P2：迁移、外部 Provider 与运营审计

交付：

- 旧密码条目迁移为 SecretRef。
- 可选外部 Secret Provider，复用同一状态和 Agent 禁读契约。
- 审计查询、保留期、导出和故障诊断。

机器断言：

- 旧条目迁移后数据库只存在 SecretRef 元数据，正文列不存在或为空。
- 迁移可重复执行，重复运行不增加 SecretRef 或审计事件。
- provider 超时、重试和断网测试均返回 unknown/degraded，不自动重放未知正文动作。
- 审计导出通过字段白名单检查，不含正文、摘要和 cleanup token。

## 六、风险与回滚

| 风险 | 尽早发现 | 回滚与退化 |
|---|---|---|
| Agent 通过错误接口拿到正文 | schema 黑名单、工具集成测试、运行时 actor 拒绝 | 立即禁用正文动作路由，保留 metadata-only；撤销相关权限 |
| TTL 清理误删新剪贴板内容 | ownership marker 竞态测试和双写模拟 | 停止 cleanup worker，只保留用户手动清理；不得无条件清空 |
| Keychain 锁定被误报为失败 | 锁定/解锁集成测试，检查 error code | 返回 locked/degraded，用户解锁后重试，不删除 SecretRef |
| Host 写入结果未知却显示成功 | 注入断连、超时和进程重启 | 状态固定为 unknown，禁止自动重试写入，等待用户显式重试 |
| 审计失败导致安全动作不可追踪 | 审计 sink 故障注入、计数器和本地告警 | 复制结果标记 degraded，不写正文到备用日志 |
| 现有 0600 FileStore 被误当作不可观测 Keychain | `APIKeyStore` adapter 的字段/调用图检查，确认正文读取只在受信任边界 | 保留 FileStore 兼容读取但禁止 Agent 和诊断调用，停止 SecretRef 迁移，不删除旧凭据 |
| 迁移把正文复制到普通数据库 | migration schema 检查、备份扫描、回滚演练 | 停止迁移，回到旧只读视图；保留 Keychain 原条目 |
| 多用户环境 actor 绑定错误 | session、UID、桌面会话隔离测试 | 拒绝复制并返回 user_auth_required，不进入 Keychain |

回滚原则：

- SecretRef 元数据迁移和 Keychain 写入分离，每个 commit 可独立回滚。
- 任何回滚都不得从 Keychain 反向导出正文到数据库。
- 回滚期间允许用户看到 degraded、locked 或 unknown，不显示“复制成功”。
- 清理失败不能通过扩大权限或无条件清空剪贴板来补救。

## 七、待主人拍板

1. **默认建议：第一期只支持本机 Keychain，外部 Provider 放到 P2。** 影响是企业远端密码库要等待 P2。若不选，P1 需要同时承担网络认证、重试和 unknown reconciliation，首期泄漏面和验证面明显扩大。
2. **默认建议：敏感复制默认 TTL 30 秒，上限 5 分钟。** 影响是长流程用户需要再次复制。若不选，需要定义按 Secret kind、目标 App 和会话的分级 TTL，并增加更多竞态测试。
3. **默认建议：Agent 永久禁止正文读取和复制，只能请求用户可见的 metadata/action。** 影响是 Agent 无法代替用户填写密码。若不选，必须新增高风险授权、交互确认、权限审计和泄漏响应流程，P0 无法按当前 non-observability 目标验收。

## 八、本轮明确不做什么

- 不实现 Keychain 适配器、数据库迁移、Host/Clipboard 代码、Agent 工具或 UI。
- 不读取、复制、打印、哈希或验证任何真实 Secret 正文。
- 不把 Secret 正文纳入同步、备份、崩溃报告、遥测、搜索索引或普通剪贴板历史。
- 不实现自动填充、网页注入、跨设备 Secret 同步或外部 Provider。
- 不承诺系统剪贴板被其他程序读取后的可控性，只保证 Clavis 的持有窗口、TTL 和 ownership-safe 清理。
- 不用“复制请求已提交”冒充“复制已确认”。未知结果统一走 unknown/degraded。
- 不在本轮修改除 docs/design/security-vault-plan.md 之外的任何路径。
