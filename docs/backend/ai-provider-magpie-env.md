# Magpie AI Provider 环境配置设计

- 任务：`T-clavis-magpie-env-design`
- 目标：明确 Clavis 中自定义 `magpie` AI provider 的 profile 配置、API key 环境变量、文件存储和解析优先级，使本地/LAN Magpie endpoint 可复现、可诊断且不泄露秘密。
- 非目标：不新增 Magpie provider 实现、不新增协议、不把真实 API key 写入仓库或文档、不改变现有 secrets/config 代码。
- 硬边界：本文只描述配置契约和实现现状；示例中的 key 一律为占位符，不能复制为真实凭据。本文中的 Magpie 指 AI profile/provider，不是 Agent 面板或其他同名模型调度能力。

## 1. 现状与范围

现状证据（行号随代码变动时以符号复核）：

- `pkg/core/config/main_config.go:281–339` 定义了 `openai_compatible`、`anthropic`、`gemini` 三种协议，以及 `AIProfile` 的 `provider`、`base_url`、`model`、`keychain_account`、`api_key_env_var` 等字段。
- `pkg/core/config/main_config.go:342–397` 的内置 profile 只有 DeepSeek、Ollama、Claude、Gemini；`magpie` 不是 shipped default，必须作为用户自定义 profile 配置。
- `pkg/core/config/reasoning_effort_test.go:21–27` 已有 Magpie 形状的 fixture：provider 为 `magpie`，协议为 OpenAI-compatible，endpoint 为 `http://10.0.0.1:3425/v1`，账户为 `clavis.ai.magpie`。这只是测试/契约证据，不表示运行时默认注册了 Magpie。
- `pkg/core/config/main_config.go:531–541,543–653` 禁止在 `[ai]` 或 `[[ai.profiles]]` 中写 `api_key`、`apiKey`、`secret_key`、`secretKey`；profile 的 `keychain_account` 若省略，会按 `clavis.ai.<id>` 生成。
- `pkg/core/config/validator.go:243–303` 校验 profile 的 id、provider、协议、HTTP(S) base URL、model、keychain account、temperature 和 timeout；`api_key_env_var` 是可选字段，空值表示使用 provider 默认映射。
- `internal/secrets/secrets.go:40–56` 将 provider 映射到环境变量：未知 provider（包括 `magpie`）落到通用 `CLAVIS_API_KEY`，并不会自动尝试一个名为 `MAGPIE_API_KEY` 的变量。
- `internal/secrets/secrets.go:371–389,395–423` 定义了 profile 级 keyless 判断和 key 解析；非本机 loopback 的 Magpie endpoint 不能按 keyless 处理。
- `main.go:335–343` 运行时组合了 `ai-keys.toml` 文件存储和 OS keychain；`LayeredStore.Migrate` 默认关闭，因此正常 AI 路径不会主动触碰旧 keychain。

本设计的核心结论是：**Magpie 必须显式指定 `api_key_env_var = "MAGPIE_API_KEY"`，或者明确接受未知 provider 的通用回退 `CLAVIS_API_KEY`；推荐前者。**

## 2. 推荐 profile 配置

Magpie 使用 OpenAI-compatible wire protocol 时，建议在 `config.toml` 中加入以下配置。`base_url`、`model` 和 endpoint 的具体值按部署环境替换；示例不包含秘密：

```toml
[ai]
active_profile_id = "magpie"

[[ai.profiles]]
id = "magpie"
name = "Magpie"
provider = "magpie"
protocol = "openai_compatible"
base_url = "http://10.0.0.1:3425/v1"
model = "huoshan/deepseek-v4-1-flash-261017"
temperature = 0.2
timeout_seconds = 60
keychain_account = "clavis.ai.magpie"
api_key_env_var = "MAGPIE_API_KEY"
reasoning_effort = "low"
```

### 2.1 字段契约

| 字段 | Magpie 约束 | 说明 |
|---|---|---|
| `id` | `magpie` | profile 选择 ID；`active_profile_id` 必须精确指向一个已声明 profile。 |
| `provider` | `magpie` | provider 名参与环境变量默认映射；它不会自动注册 provider。 |
| `protocol` | `openai_compatible` | 请求使用 OpenAI-compatible body、`Authorization: Bearer` 和 SSE 语义。 |
| `base_url` | 带 host 的 `http://` 或 `https://` URL | `http://10.0.0.1:3425/v1` 是 LAN 示例；生产环境按 endpoint 的 TLS 和网络策略配置。 |
| `model` | 非空 | 发送给 Magpie 服务的模型标识。 |
| `keychain_account` | 推荐显式写 `clavis.ai.magpie` | 省略时 decoder 会按 profile ID 默认生成同名账户；显式写出可降低迁移和诊断歧义。 |
| `api_key_env_var` | 推荐显式写 `MAGPIE_API_KEY` | 非空时覆盖 provider 默认映射；变量名本身当前没有格式校验。 |
| `timeout_seconds` | 大于 0 | 超时策略由 AI client 执行；Magpie fixture 使用 60 秒只是示例。 |
| `reasoning_effort` | 按服务能力填写或留空 | 不是认证字段，只有 Magpie endpoint 支持时才启用。 |

`config.toml` 只保存 profile 元数据和 key 的引用，不保存 key 内容。不要添加如下字段或等价变体：

```toml
# 错误：原始 API key 不得出现在 config.toml
api_key = "sk-..."
secret_key = "..."
```

## 3. 环境变量解析契约

### 3.1 provider 默认映射

`internal/secrets.DefaultEnvVarForProvider` 的结果如下。它是**变量名选择**，不是多个变量按顺序尝试的 fallback 链：

| provider（不区分大小写） | 默认环境变量 | 备注 |
|---|---|---|
| `deepseek` | `DEEPSEEK_API_KEY` | 内置默认 provider。 |
| `anthropic`、`claude` | `ANTHROPIC_API_KEY` | `claude` 是 provider alias。 |
| `gemini`、`google` | `GEMINI_API_KEY` | `google` 是 provider alias。 |
| `ollama` | 空 | 仅在 keyless 条件满足时不需要 key。 |
| `openai` | `OPENAI_API_KEY` | OpenAI-compatible 并不等于 provider 名为 `openai`。 |
| 其他 provider（包括 `magpie`） | `CLAVIS_API_KEY` | 通用回退；不会再自动尝试 `MAGPIE_API_KEY`。 |

因此，以下两种配置的行为不同：

```toml
# 推荐：Magpie 使用专用变量，避免与其他自定义 provider 共用凭据
api_key_env_var = "MAGPIE_API_KEY"
```

```toml
# 可工作但不推荐：api_key_env_var 为空时，magpie 使用 CLAVIS_API_KEY
api_key_env_var = ""
```

配置了专用变量后，启动进程前设置它（不要写入 `config.toml`）：

```bash
export MAGPIE_API_KEY='<redacted>'
```

### 3.2 解析优先级

对一个 profile，`ResolveProfileAPIKey` 的逻辑可归纳为：

1. 以 `keychain_account` 读取持久化 secret store；在当前生产组合中，`ai-keys.toml` 是首选文件层。
2. 若文件层没有该账户，则读取 profile 的 `api_key_env_var`；为空时使用 `DefaultEnvVarForProvider(provider)`。
3. 若仍没有 key，只有 keyless profile 才返回无 key 成功；否则返回 `ErrSecretNotFound`，请求侧显示缺失 key 诊断。

在当前 `main.go` 组合下，实际推荐/正常路径是：

```text
ai-keys.toml 中的 clavis.ai.magpie
        ↓ 没有文件值时
MAGPIE_API_KEY（若显式配置 api_key_env_var）
        ↓ 未显式配置时
CLAVIS_API_KEY（magpie 的 provider 默认映射）
        ↓ 仍没有值且 endpoint 不是 loopback
missing_api_key / ErrSecretNotFound
```

文件优先于环境变量，因此同一个账户同时存在文件值和 `MAGPIE_API_KEY` 时，以文件值为准。环境变量和值都会 trim 空白；空字符串不算已配置。文件解析或权限错误不会被环境变量静默掩盖。

### 3.3 Magpie 不是 keyless provider

`internal/secrets.isKeylessProfile` 允许 Ollama 或 `localhost`、`127.0.0.1`、`::1` endpoint 在没有 key 时继续运行。`10.0.0.1`、普通 LAN hostname 和公网 hostname 不在这个 loopback allowlist 内。

所以：

- `http://10.0.0.1:3425/v1` 的 Magpie profile 必须提供文件 key 或环境变量。
- 即使协议是 `openai_compatible`，也不会因此自动变成 keyless。
- 如果将 Magpie 服务代理到 `http://localhost:...`，当前代码会按 loopback keyless 规则处理；这是 endpoint 规则，不是 Magpie provider 注册规则，部署时应确认代理本身的访问控制。

## 4. `ai-keys.toml` 文件存储

### 4.1 文件位置和权限

`internal/secrets.DefaultKeysPath(configDir)` 使用：

```text
<configDir>/ai-keys.toml
```

默认 config directory 由应用启动逻辑解析；不要把该文件放入仓库或与普通 `config.toml` 混用。`FileStore` 的约束是：

- 文件缺失表示尚未配置，不是错误；
- 文件和临时文件使用 `0600`，父目录使用 `0700`；
- 文件内容按稳定顺序写入，并通过临时文件加原子 rename 发布；
- 文件权限过宽（任一 group/other 权限）会返回错误，不能悄悄回退到环境变量；
- secret store 实现不得记录 key 值或把它写入普通 TOML 配置。

### 4.2 dotted account 必须保持为一个账户

`clavis.ai.magpie` 是一个完整账户名，不是 `clavis`、`ai`、`magpie` 三层配置。写 TOML 时应将 dotted key 加引号：

```toml
[keys]
"clavis.ai.magpie" = "<redacted>"
```

代码对历史嵌套 table 有兼容的重新拼接逻辑（`internal/secrets/file_store.go:150–178`），但新写入应使用上面的单一 quoted key，避免不同 TOML 解析器和人工编辑产生歧义。正常使用优先调用 secret store 的 `SaveKey`，不要手工把真实 key 粘贴进文件。

## 5. 配置与诊断流程

### 5.1 最小安全配置流程

1. 在 `config.toml` 写入 Magpie profile 元数据，显式设置 `keychain_account` 和 `api_key_env_var = "MAGPIE_API_KEY"`。
2. 在启动 Clavis 的同一进程环境中设置 `MAGPIE_API_KEY`，或通过 secret store 保存账户 `clavis.ai.magpie`。
3. 确认 `base_url` 能被配置 validator 接受，并确认服务端支持 OpenAI-compatible `/chat/completions` 和流式响应语义。
4. 用隔离的配置目录执行 self-check/诊断，确认 key source 为 `env` 或 `file`；不要在诊断输出中打印 key 值。
5. 发起一次最小请求，按 HTTP 状态、协议错误和 `missing_api_key` 分类；不要把 key 放入命令行参数、日志或 profile 的 `system_prompt`。

### 5.2 失败分类

| 现象 | 可能原因 | 处理 |
|---|---|---|
| `missing_api_key` / `ErrSecretNotFound` | 文件无账户、环境变量未设置或变量名写错 | 核对 `keychain_account`、`api_key_env_var` 和启动进程环境；不要先改协议。 |
| 配置加载拒绝 raw API key | `config.toml` 中出现 `api_key`/`secret_key` | 删除秘密字段，改用 `ai-keys.toml` 或环境变量。 |
| keys 文件权限错误 | `ai-keys.toml` 可被 group/other 读取 | `chmod 600 <configDir>/ai-keys.toml`，修复后重试；不能依赖 env 覆盖这个错误。 |
| 变量存在但仍缺 key | `api_key_env_var` 为空而只设置了 `MAGPIE_API_KEY` | 显式写 `api_key_env_var = "MAGPIE_API_KEY"`；否则代码只看 `CLAVIS_API_KEY`。 |
| URL/protocol validation error | base URL 无 host、不是 HTTP(S)，或 protocol 不在三种支持值内 | 修正 URL/protocol；Magpie 的 OpenAI-compatible endpoint 使用 `openai_compatible`。 |
| 连接成功但请求失败 | 服务端模型名、路径、认证格式或协议兼容性不符 | 检查 Magpie server contract 和 `/v1` 前缀；不要通过记录更多秘密来诊断。 |

## 6. 上线、迁移与回滚

### 6.1 上线步骤

1. 先只提交 `config.toml` 的 profile 元数据，不提交任何 key。
2. 默认采用 `MAGPIE_API_KEY` 专用变量；只有明确需要共享通用变量时才使用 `CLAVIS_API_KEY`。
3. 使用临时 config directory 和非生产测试 key 验证文件优先级、env fallback、缺 key 和错误权限四种路径。
4. 如需从旧 OS keychain 导入，由显式迁移流程调用 `MigrateFromKeychain`；`LayeredStore.Migrate` 默认关闭，不能把正常启动隐式变成 keychain 访问。
5. 保存 key 后重新运行 self-check，确认只显示来源类别和账户标识，不显示 secret 内容。
6. 在 endpoint、model、认证和流式响应都通过后，再把 profile 设为 active；配置加载失败时保留上一份有效 config，不用空 profile 覆盖。

### 6.2 回滚

- 回滚 profile 配置时，删除/停用 `active_profile_id = "magpie"` 即可；不要为了回滚把 key 复制到 `config.toml`。
- 如果只是 Magpie endpoint 或模型变更失败，保留 secret store 中的 key，回滚 `base_url`/`model`，避免不必要地重新导入凭据。
- 如果 `MAGPIE_API_KEY` 泄露或需要轮换，先在 Magpie 服务端撤销旧 key，再更新环境注入或 `ai-keys.toml` 中的账户值；日志、shell history、CI artifacts 中的旧值按同一事件处理。
- 不把 `CLAVIS_API_KEY` 当成 Magpie 专用撤销开关；它可能被其他未知 provider 共用。迁移到专用变量后，清理旧的共享变量和对应测试环境。
- 若 keys 文件损坏或权限不合规，先保留现场副本并修复/恢复经过校验的 `0600` 文件，不创建空文件覆盖原配置，也不把错误降级成 env 成功。

## 7. 可执行验收

所有测试使用临时目录、占位 key 和显式环境变量；不得读取开发机真实 keychain 或环境秘密。仓库根目录执行：

```bash
go test -count=1 -v ./internal/secrets ./pkg/core/config \
  -run 'Test(DefaultEnvVarForProvider|LayeredStore|FileStore|.*ReasoningEffort.*)'
```

至少应保留/验证以下具名行为：

- `DefaultEnvVarForProvider("magpie") == "CLAVIS_API_KEY"`，并证明这不是 `DEEPSEEK_API_KEY` 后置 fallback。
- 显式 `api_key_env_var = "MAGPIE_API_KEY"` 时，profile 只使用该变量名；环境值 trim 后可用。
- file value 优先于 env value；文件缺失时才使用 env；两者都缺失时非 loopback Magpie 返回 missing key。
- `clavis.ai.magpie` 作为 quoted dotted key 保存、重新打开后仍是一个账户；历史 nested-table 形式不会丢失账户名。
- `Migrate=false` 时缺失 file entry 不触碰 OS keychain；保存和删除默认只影响 file store。
- `ai-keys.toml` 缺失可恢复；权限不是 `0600` 时返回明确错误，且不会用 env 静默掩盖。
- `config.toml` 中出现 raw `api_key`/`secret_key` 被拒绝；合法 profile 能通过 URL、协议、model 和 account 校验。
- `http://10.0.0.1:3425/v1` 不被当作 keyless；只有 loopback endpoint 才进入当前 keyless 规则。

如需手工隔离环境，使用临时 `configDir`、清空 `MAGPIE_API_KEY`/`CLAVIS_API_KEY` 后再逐项设置；验收输出只记录 `file|env|none|keychain` 等来源类别和稳定错误码，不记录值。

## 8. 取舍与风险

取舍结论：**profile 中显式绑定专用 `MAGPIE_API_KEY`，secret store 文件优先，环境变量作为可控 fallback，keychain 仅保留显式迁移路径。** 这样避免未知 provider 共用 `CLAVIS_API_KEY` 带来的误用，也符合现有 config/raw-secret 边界；代价是部署系统必须额外注入一个专用变量。

拒绝以下方案：

- 拒绝把 key 放进 `config.toml`：配置文件会被导出、复制或提交，且 decoder 已明确禁止 raw key 字段。
- 拒绝把 `MAGPIE_API_KEY` 自动加入 provider 全局映射：当前 provider 注册不是环境变量注册，新增隐式映射会改变未知 provider 的兼容行为；本设计用 profile 字段表达意图。
- 拒绝把 `CLAVIS_API_KEY` 解释为“所有 provider 的第二次尝试”：代码把它作为未知 provider 的默认变量名，而不是 `DEEPSEEK_API_KEY` 失败后的链式 fallback。
- 拒绝将 LAN endpoint 一律视为 keyless：只有明确的 loopback host 在当前规则中免 key，LAN 地址仍需认证。
- 拒绝让正常启动默认读取 OS keychain：当前 `LayeredStore.Migrate` 关闭可避免系统弹窗和不可预期的外部凭据访问。

主要风险：`api_key_env_var` 当前不做格式校验，拼写错误只能在请求/诊断阶段暴露；provider 默认表、配置生成模板和 UI preset 存在多处维护点，未来若新增 Magpie 专用默认映射必须同步测试、生成器和本文。若后续给 `ai-keys.toml` 增加版本化迁移，应遵守 [应用库 Schema 版本、迁移与回滚设计](schema-migration.md) 的“按存储边界独立版本”原则，不把 secret 文件版本号混入应用 SQLite 的 `user_version`。
