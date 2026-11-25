# Clavis 上线准备度深度 Review

日期：2026-11-25
仓库：`/Users/yeshugen/workspace/clavis`
分支：`clv/p5-foundation`
目标口径：macOS 自用稳定版 1.0。本文不把 Windows、公开分发、云账号和插件生态混进 1.0 承诺。

## 结论先行

**当前不能发 1.0。** 代码和普通测试已经达到“能继续冲刺”，还没有达到“用户拿到就能稳定使用”。阻断项只有三类，但每一类都是真阻断。

1. `go test -race ./internal/watcher ./uiapp` 当前复现 3 个 UI 失败，涉及 agent refresh settle 和 leader rail layout。
2. 当前真实 macOS 自检显示 `accessibility=false`、`input-monitoring=false`、`granted=false`，输入法 `enabled=false`，AI key 文件不存在，sync `not_configured`。这不是环境噪声，正好覆盖 Clavis 的关键入口。
3. macOS A1–A16 交互签收、Windows 真机签收都仍是路线图中的待办。无头测试不能替代键盘钩子、输入注入、截图权限、Vault 剪贴板归属和 Herdr 控制面的真实验证。

**发布下注**：先发“macOS 本地稳定版”，只承诺 Launcher、剪贴板、Vault、QuickJump、截图标注、AI/OCR、Herdr 只读发现和显式控制。输入法作为显式 opt-in，Windows、同步 GUI、P5 元数据层、Magpie 专页、ACP 和独立 Assets Hub 退出 1.0 的产品承诺。

## 证据边界与状态定义

- **能上线**：有正常入口，有本地测试或构建证据，失败路径有明确状态，剩下的是目标机手测而非功能缺失。
- **半成品**：代码和测试存在，但关键入口、真实环境、权限、外部服务或用户可完成的闭环缺一块。
- **该砍**：没有独立用户价值、没有可证明的运行路径，或者与已有入口重复。底层能力可以保留，产品承诺从 1.0 删除。

设计文档标记为 Ground Truth 只证明设计意图。本文优先采用当前代码、当前命令输出和当前真实自检。

### 本轮实际命令

```text
go test -count=1 ./...
EXIT=0
最后一行：ok  github.com/yeisme/clavis/uiapp  30.209s

 go test -count=3 ./internal/watcher ./uiapp
EXIT=0
internal/watcher 5.104s
uiapp 316.359s

go test -race ./internal/watcher ./uiapp
EXIT=1
TestRefreshAgentSessionsUsesCodexSQLiteReader: codex session cx-sql-1 not loaded via CodexSQLiteReader
TestLeaderDashboardRendersRealDataAndSupportsNumericTabEscSearch: timed out waiting for startup agent refresh settle
TestLeaderRailBlocksNeverOverlap: timed out waiting for startup agent refresh settle

go build ./...
native build: PASS

go vet ./...
native vet: PASS
GOOS=windows GOARCH=amd64 go vet ./...
windows amd64 vet: PASS
GOOS=linux GOARCH=amd64 go vet ./...
linux amd64 vet: PASS
GOOS=windows GOARCH=amd64 go build ./...
windows amd64 build: PASS
GOOS=windows GOARCH=arm64 go build ./...
windows arm64 build: PASS
GOOS=linux GOARCH=amd64 go build ./...
linux amd64 build: PASS

go run . --smoke-check
Clavis smoke-check ok (leader=right_option, os=darwin, arch=arm64)
```

`GOOS=windows GOARCH=amd64 go test -run '^$' ./...` 被 Go 尝试执行 Windows `.test.exe`，在 macOS 上得到 `exec format error`。这不是 Windows 编译失败，CI 使用的正确门禁是 `go test -c`，仓库的 `.github/workflows/ci.yml:34-37` 已采用该方式。

### 当前机器的真实自检

```text
go run . --self-check
config: loaded
input engine: enabled=false mode="inline_hook" scheme="quanpin" candidates=7 learning=true
input engine: OFF
input engine probe: typing "nihao" -> 你好 / 您好 / 你
permissions: accessibility=false input-monitoring=false apple-events=true granted=false
permissions: NOT granted — grant Accessibility + Input Monitoring in System Settings
keep-alive agent: installed=true
ai key source: none (.../ai-keys.toml missing)
input method mode: inline-hook (second-IME; optional legacy IMK bundle installed=false)
self-check ok

go run ./cmd/clavis sync status --json
{"code":"not_configured","configured":false,"enabled":false,"recoveryReady":false,"watermark":0,"pendingUploads":0,"pendingTombstones":0,"conflicts":0,"devices":0,"dirExists":false,"envelopeFiles":0,"tombstoneFiles":0,"allowlist":["input_method","learning_data"],"blockedClasses":["passwords","key_manager"]}
```

仓库当前存在并行工作树改动和未跟踪文件。本文没有触碰它们，测试证据代表当前 checkout，不代表一个干净发布 commit。

## 全量功能盘点

### 用户入口和主要能力

| 功能面 | 当前实现证据 | 结论 | 上线取舍 |
| --- | --- | --- | --- |
| Leader 与 Omnibox 全能搜索 | `uiapp/capsule_view.go:185-192` 有 Actions、Clipboard、Snippets、AI、Glossary、Agent 六 tab，支持快捷键、路由前缀、预览和 action menu。`uiapp` 有 98 个测试文件、456 个 `Test` 函数。 | **能上线** | 这是主入口，保留。先修 race 下 agent refresh，再做 A1–A16。产品宣称“全能搜索”仍缺通用文件索引，不能把 Actions 中的应用发现说成 Spotlight/Raycast 级文件搜索。 |
| 剪贴板历史 | `internal/storage/sqlite_store.go:21-40,374-397` 使用 SQLite + FTS5，`pkg/core/clipboard` 有文本、图片、文件和隐私过滤测试，P1-4 已做 COW 快照和 60 行首屏上限。 | **能上线** | 这是最可靠的本地价值。保留 pin、delete、FTS、图片缩略图和隐私过滤。首次授权和前台粘贴仍需手测。 |
| key 管理与安全剪贴板 | `internal/secrets/file_store.go`、`vault_encrypted_store.go`、`vault.go` 覆盖 AES-256-GCM、OS Keychain/`0600 vault.key` 回退、TTL、剪贴板所有权、Revoke 删除。Vault 测试覆盖并发、篡改、权限和 Agent 禁止复制。 | **能上线** | 以“本地 Vault + 用户显式复制”为承诺。不能承诺云同步密码，`sync plan` 明确把 `passwords`、`key_manager` 列为 forbidden。 |
| QuickJump 快跳 | `pkg/core/engine/quickjump.go:45-160` 合并 Herdr workspace/pane、`quickjump.toml`、URL `{query}` 模板和目录/工作树；`quickjump_test.go`、`uiapp/quickjump_agent_manage_test.go` 有覆盖。 | **能上线** | 保留 `#` 书签和本地目录/URL。Herdr 不可用时必须显示 terminal fallback 状态，不得把 fallback 伪装成 Herdr。 |
| 截图标注 Annotate | `internal/capture` 有 macOS `screencapture`、Linux CLI、Windows GDI；`pkg/core/annotate` 有 Rect/Arrow/Mosaic/Text、Undo/Redo、PNG flatten；`uiapp/annotate_view.go` 接 MediaStore 和 OCR。Windows 当前只承诺 full-screen，区域选择仍是 Phase 4 gap。 | **半成品** | macOS 自用可作为重点，但必须完成 Screen Recording + A1 手测。Windows 1.0 删除“交互区域截图已平齐”的承诺。 |
| OCR 与双语翻译 | `pkg/core/ai/multimodal.go` 有 Vision profile 选择和三协议 payload，`uiapp/ocr_translate_view.go` 有 2048px 缩放、方向检测、OCR/Translate UI，`uiapp/ocr_translate_test.go` 和 `pkg/core/ai/multimodal_test.go` 使用 mock HTTP。 | **半成品** | OCR/翻译保留为 provider-backed capability。发布前必须用至少一个真实 Vision profile 和一个真实文本 profile 做 smoke，缺 key 时必须零阻塞、明确报错。 |
| 媒体搜索与预览 | `pkg/core/clipboard/media.go` 有 SHA-256 content-addressed blob、256px thumbnail、40MiB/8000px/3200万像素防爆、512MiB quota GC；`uiapp/media_search.go`、`media_preview.go` 有异步搜索和预览测试。 | **能上线** | 本地媒体和剪贴板预览是可交付能力。远端“媒体搜索”不应被误解为联网素材库，1.0 只承诺本地历史。 |
| Assets Hub | `uiapp/assets_hub_view.go` 230 行、`vault_assets_view.go` 627 行、`assets_vault_test.go` 314 行，覆盖媒体、Pinned/Templates、Vault 三类入口。功能主要重复 Omnibox、Manage 和 Settings。 | **该砍** | 保留底层 MediaStore、Vault、Templates，砍独立 `⌘9 Assets` surface，省掉至少 1,171 行直接 UI/测试维护和一套子 tab 状态。功能回收到 Omnibox 和 Settings。 |
| AI 中心 | `uiapp/ai_center_view.go`、`controller_ai.go`、`pkg/core/ai/client.go` 支持 pending/running/awaiting_human/done/failed、流式输出、diff gate、Anthropic/Gemini/OpenAI-compatible、token telemetry。默认 profile 有 DeepSeek/Ollama/Claude/Gemini，测试均为 fake HTTP。 | **半成品** | UI 和协议层保留，不能把“mock HTTP 通过”当作 provider 可用。当前机器没有 `ai-keys.toml`，发布验收必须有真实 key、错误 key、超时和 localhost Ollama 四条 smoke。 |
| Agent 工作台 | `pkg/core/agentsession` 合并 DSH、Codex SQLite、Claude JSONL、Herdr；`herdr_control.go` 有确认门、live target re-check、secret/option injection 拦截、StateUnknown；`uiapp/agent_sessions.go` 有缓存和 QuickJump 接线。 | **半成品** | 只读发现和显式控制保留。Race 下 agent refresh settle 失败，且本机没有 Herdr/Codex/Claude live fixture 证明。修复 race 并做真实空、缺二进制、live agent 三态 smoke 后再发。 |
| Herdr 对接 | `pkg/core/agentsession/herdr_control.go` 和 `focus.go` 对真实 `herdr` CLI 有参数边界和不可重试 unknown contract，测试用 fake executor。 | **半成品** | Herdr 是可选外部依赖。缺 binary 时必须降级到终端并显示诊断。1.0 不承诺 Herdr daemon 一定存在，只承诺安全降级和安装后可控操作。 |
| ACP 对接 | `git grep -n -i -E '(^|[^a-z])acp([^a-z]|$)' -- '*.go' '*.md'` 无结果。仓库实际有 `internal/mcp`，路线图把 MCP 列为 Phase 4 可选扩展。 | **该砍** | ACP 没有实现、没有入口、没有验收。不要为了名词完整而新增协议层。保留 Herdr 控制面，MCP 继续冻结为可选扩展。 |
| P5 annotation store | `internal/storage/annotation_store.go:57-86` 可迁移到 App schema v3，支持 tags/favorite/props/notes；`annotation_store_test.go` 覆盖持久化、排序、D4 secret rejection 和并发。仓库内没有 `NewAnnotationStore` 的 runtime caller，只有测试。 | **半成品** | 不阻塞 1.0。保留 schema 和测试，等 Omnibox/Template/Vault 真正消费 annotation 后再接 UI。 |
| P5 identities | `pkg/core/config/identities_config.go:9-75` 只有 `active_persona_id` 和 `active_env_profile_id` 两个 ID，`ValidateIdentities` 直接返回 nil，当前只有 ConfigStore/self-check 读写，没有 persona/env profile 数据模型和编辑器。 | **半成品** | 不阻塞 1.0。把“身份中心”从 README 和 Settings 入口拿掉，先保留兼容配置读写。 |
| P5 taxonomy | `pkg/core/taxonomy/query.go`、`tags.go`、`attributes.go` 测试完整，支持 `#tag`、`-#tag`、`is:fav` 和 facet，但没有 runtime consumer 接到 Omnibox 或 Settings。 | **半成品** | 不阻塞 1.0。保留纯函数和安全校验，暂不承诺统一标签搜索。 |
| P5 i18n kit | `pkg/core/i18n/table_p5_kit.go` 注册表和 `register_table_test.go` 已接入，重复 key/空翻译会 panic。 | **能上线** | 这是低风险基础设施，保留。它不等于 P5 元数据层已经完成。 |
| Magpie/env 诊断 | `docs/backend/ai-provider-magpie-env.md` 明确写的是“不新增 Magpie provider 实现”；代码只有通用 `AIProfile` 和 `api_key_env_var`，Magpie 只在 fixture/文档出现。`--self-check` 只报告当前 profile key source，不报告 Magpie endpoint 探测。 | **该砍** | 砍 Magpie 专页和“环境诊断页”产品承诺。保留通用 profile、env fallback 和 self-check 的稳定错误码。 |
| 自研输入法引擎 | `pkg/core/inputengine` 有 embedded 47,787 词、自定义 `.clvdict` 热更新、Bigram、学习和 blocklist，42 个测试函数；macOS hook 在 `internal/bridge/bridge_darwin.go`。当前真实 config `enabled=false`，权限未授予。 | **半成品** | 作为 opt-in beta，不作为默认启动承诺。完成 AX/Input Monitoring/真实输入注入手测后再把默认开关改成产品决定。 |
| Windows parity | `docs/windows-parity-audit.md:19-35` 标出大部分桥接为 Unit/Cross-compiled，但多项写明 Needs Windows HW，Snippets 和 OpenApp 仍是 partial。当前 amd64/arm64 build、vet 通过，未有真机结果。 | **半成品** | 1.0 不宣称 Windows ready。Windows 支持保留为 1.2 目标，真机签收前在下载页和 README 明确“未签收”。 |
| 同步 | `internal/sync/dir_transport.go` 已有共享目录密文 envelope、ADK wrapping、revocation、tombstone、conflict；`cmd/clavis/main.go:248-486` 有 `sync status/plan/run`，CLI 测试有双目录端到端。当前真实状态是 `not_configured`，没有 GUI init/pairing 完整闭环。 | **半成品** | 引擎保留，1.0 不把同步写成默认可用。发布后优先补初始化/配对/冲突 UI，或把 CLI 明确标为高级实验功能。 |
| 存储迁移 | `internal/storage/migrate.go` 目前 App schema v3，v0→v1→v2→v3、备份、WAL、failpoint rollback、unknown version fail-closed 均有测试，`go test ./internal/storage` 在全量测试中通过。 | **能上线** | 这是上线硬依赖，保留。发布包必须用临时旧库做一次升级与回滚演练，不能只依赖 fresh DB。 |
| Snippets、Glossary、Templates、Workflows | `pkg/core/config` 有 TOML round-trip/validation，`uiapp/settings_templates.go` 有 CAS import/update 和 Composer 接线，Snippet/Glossary 有安全输入和 secure-field guard。 | **能上线** | 保留本地能力。模板 catalog/远端发布市场不进入 1.0。 |
| Keymap、窗口管理、Selection Bar | `internal/bridge` 有 tap-hold、window commands、selection probe，`uiapp` 有对应设置和 UI tests。 | **能上线** | 作为本地 macOS 功能随 A1–A16 验收。UIPI/跨平台差异在 Windows 说明中保留。 |
| 首次启动与权限引导 | `uiapp/onboarding_view.go:90-118,230-299` 有 Accessibility/Input Monitoring 引导和 relaunch；`mygo.json:11-16` 还声明 Apple Events 与 Screen Recording。当前自检两个键盘权限均 false，Screen Recording 没有同等可见的状态行。 | **半成品** | 发布前必须把 Screen Recording 加入诊断/引导，或者明确 Annotate/OCR 在未授权时的阻断提示。当前不能把 onboarding 说成完整首启闭环。 |

## 竞品对标与取舍

### 竞品各自强项

| 产品 | 官方能力事实 | Clavis 需要吸收的基线 |
| --- | --- | --- |
| Raycast | [Quickstart](https://manual.raycast.com/quickstart) 把 app、file、folder、window、link、command、AI、snippet、clipboard 放在同一个 Root Search，另有扩展商店、Calculator 和 Window Management。 | 单一入口、应用/文件搜索、Quicklink、Clipboard、Snippet、Window Management 是用户预期。Clavis 当前六 tab 有入口统一，但没有明确的通用文件索引。 |
| Alfred | [Overview](https://www.alfredapp.com/help/overview/) 的强项是本地快速搜索、Workflow、Universal Actions、Clipboard History、Snippets、Web Search 和 Preferences Sync。[Workflow 文档](https://www.alfredapp.com/help/workflows/) 允许脚本、变量、UI 和导入分享。 | Alfred 证明本地、可组合、可脚本化比“功能列表很长”更重要。Clavis 的 TOML workflow 有基础，但没有 Alfred 级生态和分享链路，1.0 不应假装有。 |
| Warp | [Agents overview](https://docs.warp.dev/agents/) 把真实终端、代码审查、Agent 管理和后台自动化放在同一产品，支持 Claude Code、Codex、Gemini CLI 等。 | Warp 强在终端上下文和多步执行闭环。Clavis 的 Herdr 是发现、预览、显式控制面，不应和 Warp 争“全自动 Agent IDE”。 |
| Spotlight | [Apple Support](https://support.apple.com/en-euro/guide/mac-help/mchl4d69efd3/mac) 的基线是系统级 app、文件、位置和类别筛选，零安装、零额外权限。 | Clavis 如果继续叫 Launcher，至少要补一个低成本的 app/file open 路径，或者把定位收缩为“键盘工作台”。不能用六个业务 tab 替代系统文件搜索。 |
| Clipboard 工具 | [Maccy](https://maccy.app/) 明确只做“把 copy history 放在手边”，强调本地、快速、少功能。[Paste](https://pasteapp.io/updates) 已把历史、暂停收集和 AI 写作结合。 | Clipboard 的用户期待是毫秒级打开、搜索、过滤、隐私边界和可靠粘贴。Clavis 的 FTS5、COW、MediaStore 和 privacy filter 已具备差异化工程基础，必须优先保证它。 |

### “别人有我没有，所以必须有”

1. **App/file open 的最低闭环**。Raycast、Alfred、Spotlight 都有。Clavis 当前有应用发现和 Actions，但没有一个清楚的通用文件搜索入口。1.0 要么补一个直接的文件/目录搜索 action，要么删掉“全能 Launcher”宣称。
2. **Clipboard History 的稳定 paste**。Clavis 已有实现，必须把真实权限、前台注入和隐私过滤验收完，不再扩展更多媒体花活。
3. **Snippets 和 Quicklinks**。Clavis 已有 snippets、`#` QuickJump 和 `{query}` URL。保证 round-trip、secure field guard 和 fallback，比再加 provider 更重要。
4. **权限可解释、失败可恢复**。竞品可以靠系统授权和成熟分发降低摩擦。Clavis 需要把 Accessibility、Input Monitoring、Screen Recording 三类状态讲清楚，缺任何一个都显示具体受影响功能。
5. **本地稳定性和可卸载性**。本地 SQLite、可读 self-check、配置备份、无 key 时不阻塞，属于上线底线，不是高级卖点。

### “别人也没有，但砍了不影响上线”

- **第二输入法**是 Clavis 的真正差异化，但必须作为 opt-in。Raycast/Alfred/Spotlight 不替代中文输入法，Clavis 可以在这里形成自己的位置，不能把它作为所有用户的默认门槛。
- **Security Vault + Agent 禁止 plaintext** 是有实际差异的安全边界。保留，但不扩展团队 Vault 或云账号。
- **Herdr 控制面**是面向已有终端工作流的差异化。保留发现、聚焦、重命名、确认发送和 unknown contract，砍掉通用多步编排。
- **3D spatial visual preset**不是用户选择 Clavis 的理由。它没有解决入口、粘贴、输入和权限问题，应从 1.0 默认路线砍掉，只保留稳定的 classic/liquid glass。
- **六 tab + 三 Hub**是信息架构，不是价值本身。Assets Hub 独立 surface 应砍，Stats/Manage 只保留必要诊断卡片。
- **P5 taxonomy、annotation、identities**在没有 runtime consumer 前是架构自嗨。保留纯库和迁移，不把它们包装成已交付用户功能。
- **Magpie 专页、ACP 名称、扩展商店**都没有当前闭环。用户不会因为文档里多一个协议名而得到价值。

## 技术债与上线硬门槛

| 项目 | 现状证据 | 缺口 | 补齐成本估计 | 判定 |
| --- | --- | --- | --- | --- |
| 单测覆盖盲区 | `uiapp` 98 个测试文件/456 个函数，但 `pkg/core/annotate` 只有 1 个测试函数，`internal/capture` 只有 4 个，绝大多数 OS 交互靠 fake 或编译。 | 缺真实键盘钩子、前台 paste、Screen Recording、Keychain、Herdr、真实 provider 的端到端矩阵。 | 2–3 个 macOS 工作日建立 A1–A16 和最小 smoke 清单。 | **必须补** |
| Flaky / race | `go test -count=3 ./internal/watcher ./uiapp` 通过但耗时 316.359s；`go test -race` 失败 3 个，两个 startup agent refresh settle timeout，一个 Codex reader fixture 空结果。 | race 下的共享启动刷新、fixture 生命周期和 UI layout 等待条件仍不稳定。 | 1–2 个工作日定位 refresh barrier、fixture isolation 和 timeout，再连续跑 `-race` 3 次。 | **不能发** |
| 跨平台构建 | native/windows amd64/windows arm64/linux amd64 build 和三平台 vet 均通过。CI 用 Windows runner、Linux runner、macOS runner，Windows bridge/capture 用 `go test -c`。 | 没有 Windows 真机输入法、hook recovery、UIPI、区域截图和 elevated window 签收。 | 1 个有真实 Windows 设备的人日，含 `device-beat.ps1` 和手测记录。 | **Windows 延后** |
| 签名与分发 | `mygo.json:8` 使用 `Clavis Local Signing`；`scripts/package.sh:58-66` 和 `package-all.sh:64-71` 只有设置 `CLAVIS_SIGN_IDENTITY` 才传 `-sign`，macOS 只在本机执行 `codesign --verify`。 | 当前没有 Developer ID 证据、notarization ticket、公证 staple、安装升级实测。 | 自用版 0.5–1 天完成稳定签名身份和安装验证。公开下载再加 Apple Developer 账号、公证和 DMG 复验。 | **自用必须签名，公开分发延期** |
| 崩溃上报 | 仓库没有 Sentry、Crashpad、OSLog 上传或 crash dump 入口。`internal/plugins` 的 crash limit 只是插件 supervisor 自我熔断。 | 用户崩溃无回溯，无法判断真实设备失败率。 | 1–2 天接入本地 crash log 和用户主动导出，远端上报另算。 | **1.0 后补** |
| 更新机制 | 仓库没有 updater、Sparkle、版本检查或迁移提示。`scripts/version.sh` 只生成版本号。 | 用户只能手动替换 app，无法可靠获知修复版本。 | 3–5 天做 Sparkle/自有更新链路，含签名、回滚和权限测试。 | **1.0 后补** |
| 首次授权 | `onboarding_view.go` 能引导 Accessibility/Input Monitoring，`mygo.json` 声明四类 usage description；真实 self-check 两个键盘权限均 false，Screen Recording 没有状态卡。 | 首启用户不知道“哪个功能因哪个权限不可用”，Annotate/OCR 的 Screen Recording 缺口尤其明显。 | 0.5–1 天补 Screen Recording 状态、受影响功能、重新检测和 relaunch 文案，再做 A1–A16。 | **不能发** |
| AI provider 运行时 | 协议/stream/OCR tests 使用 mock HTTP；当前机器 `ai-keys.toml` 不存在。 | 没有真实 provider smoke，超时、错误 JSON、限流和模型不支持 Vision 的用户体验仍未知。 | 0.5–1 天准备非生产 key，跑 DeepSeek/Anthropic 或 Gemini 其中一条文本、一条 Vision、一条错误路径。 | **不能把 AI 说成已验收** |
| Herdr/Agent 运行时 | fake executor 覆盖安全边界，真实机器未报告 Herdr、Codex、Claude live 发现结果；race 失败集中在 agent refresh。 | 缺“无 binary / 空列表 / stale target / unknown outcome / live pane”矩阵。 | 0.5–1 天用临时 fixture 和真实 Herdr（若可用）跑发现与控制闭环。 | **不能发 Agent 控制面** |
| 同步与迁移 | Storage migration 已有 v3、WAL、备份、rollback 测试。Sync CLI/DirTransport 有端到端测试，但真实状态是 `not_configured`，没有 GUI init/pairing。 | 新用户不能从 Settings 完成配对、恢复码确认和冲突处理。 | 2–3 天做最小 init/pair/status/conflict UI，或把 sync 明确降为高级 CLI。 | **同步延后，迁移可发** |

## 上线必做，按依赖排序

| 顺序 | 必做 | 依赖 | 完成门槛 |
| --- | --- | --- | --- |
| 1 | 锁定 1.0 范围为 macOS 自用版，删除 README/Settings 中 ACP、Magpie 专页、Windows ready、P5 已完成等过度承诺 | 无 | 文档和启动 UI 不再暗示未验收能力 |
| 2 | 修复 race 下三个 UI 失败，并把 agent refresh fixture 做成确定性 barrier | 1 | `go test -race ./internal/watcher ./uiapp` 连续 3 次全绿，单次不超过 2 分钟 |
| 3 | 把 Screen Recording 加入权限诊断和 onboarding，明确 Annotate/OCR 受影响范围 | 1 | 无权限、只给 AX、只给 Input Monitoring、三者全给四种状态都有可见结果 |
| 4 | 完成 macOS A1–A16 手测 | 2、3 | Capsule、Clipboard、IME opt-in、Selection Bar、Annotate、Vault copy/expiry、Herdr fallback 每项留真实输出 |
| 5 | 做 provider 和 Agent 最小 runtime smoke | 2、3 | 文本 provider、Vision provider、缺 key、超时、无 Herdr 五条路径有稳定错误码，日志不泄密 |
| 6 | 做旧 App DB v0/v1/v2 → v3 升级、WAL、备份回验和 failpoint rollback | 2 | 使用临时旧库执行 `go test -count=1 ./internal/storage`，并保存 manifest/digest 结果 |
| 7 | 生成签名 macOS arm64 包，安装到干净用户目录，执行 `--smoke-check`、`--self-check` 和版本校验 | 4、5、6 | `codesign --verify` 通过，包内版本正确，未授权时 UI 给出可恢复提示 |
| 8 | 用干净 release worktree 做最终全量门禁 | 1–7 | `go test -count=1 ./...`、native/Windows/Linux build、三平台 vet、脚本语法和 manifest 校验全绿 |

## 上线后补，不阻塞 macOS 1.0

| 优先级 | 项目 | 先补什么 |
| --- | --- | --- |
| P1 | 同步用户闭环 | Settings init、设备配对、恢复码、冲突列表和两目录 E2E。当前 DirTransport 和 crypto 先保留。 |
| P1 | Windows parity | 真机键钩子、IME、UIPI、OpenApp、区域截图和 elevated window 签收。 |
| P1 | 崩溃诊断 | 先做本地崩溃日志和用户导出，再决定是否远端上报。 |
| P1 | 输入法正式化 | 把 opt-in 测试矩阵跑完，补候选质量和真实应用兼容性，再决定是否默认开启。 |
| P2 | P5 runtime wiring | 先让 annotation store 有真实 caller，再做 taxonomy 过滤 UI，最后才做 identities persona/env profile 模型。 |
| P2 | AI resilience | provider capability probe、限流/重试边界、模型不支持 Vision 的明确替代路径。 |
| P2 | 更新机制 | 签名更新、回滚、版本迁移和 changelog。 |
| P2 | 模板 catalog | 只在本地 CAS/import/update 可靠后再考虑远端发布市场。 |
| P3 | MCP/插件扩展 | 只有确定用户场景、权限模型和可撤销审计后再解冻。 |

## 建议砍掉

| 砍掉的东西 | 保留的底层能力 | 直接省下的维护面 |
| --- | --- | --- |
| ACP 产品承诺 | Herdr 控制面、已存在的 MCP 内部包 | 不新增一套协议、认证、session、错误模型和文档验收 |
| Magpie 专用诊断页 | 通用 AI profile、`api_key_env_var`、self-check key source | 不维护 provider 专属 UI、探测协议和环境变量特例 |
| 独立 Assets Hub `⌘9` | MediaStore、Vault、Templates、Pinned | 至少 1,171 行 Assets/Vault Hub 直接 UI/测试，加上 Hub 子状态和导航分支 |
| 默认 spatial 3D 视觉路线 | classic/liquid glass、Reduce Motion、稳定的 Capsule | `uiapp/spatial3d.go`、`spatial_wire.go` 及关联测试约 1,000 行视觉维护面 |
| P5“身份中心/统一标签系统”1.0 宣称 | v3 schema、annotation/taxonomy/i18n 纯库 | 不维护没有消费者的 config/UI 契约，避免把基础层伪装成已交付功能 |
| 通用多步 Agent 编排 | Herdr 单步/受控发送、等待、确认和 unknown contract | 不引入执行 DAG、重试语义、审批状态和更大的安全边界 |
| Windows 1.0 ready 宣称 | Windows 代码和 CI 编译门禁 | 省下未签收平台的支持承诺，待真机通过后再开 |

## 最终下注

1. **1.0 只发 macOS 自用稳定版**，重点是 Omnibox、Clipboard、Vault、QuickJump、Annotate、AI 基础流和安全的 Herdr 降级。
2. **race、权限引导和真实环境 smoke 是发版硬门槛**。普通测试绿不能覆盖它们。
3. **砍表里的入口全部从产品承诺移除，不删除已经稳定的底层库**。先降低用户看到的功能面，再把真实可用的主线做硬。
4. **同步、输入法、Windows、P5 和更新机制都不阻塞这次 macOS 1.0，但必须在路线图中标成半成品或延后**，不能继续使用“已完成”措辞。
