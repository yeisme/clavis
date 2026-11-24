# Clavis 推进路线（Single Source of Truth）

> **权威基线**：HEAD `79d325b`（2026-11-25）
>
> **状态口径**：本文件是 Clavis 全仓唯一的推进路线与阶段状态真源。历史规划与专项设计见 §5 文档索引；任何旧文档与本文件冲突时，以本文件与当前代码为准。

---

## 0. 一句话结论与 1.x 完成口径

Clavis 的核心功能面（Launcher / 第二输入法 / 媒体剪贴板 / Security Vault / 区域截图标注 / 多协议 AI & Vision OCR / Herdr Agent 控制面）已全部合入 `main`，且 **P0（安全与正确性止血）** 与 **P1（核心引擎、接线与 120Hz 性能收口）** 已全量落地验收。当前处于 **P2（架构减债与可维护性收口）**，随后推进 **P3（统一 Schema、模板平台与多设备同步）** 与 **P4（Windows 实测、CI 打包与可选扩展）**。

| 里程碑 | 目标定义 | 验收门槛 |
| --- | --- | --- |
| **1.0 自用稳定版** | macOS 日常主力启动器 + 第二输入法 + 截图/OCR/AI/Vault/Herdr 全链路可用，无已知安全/竞态/掉帧缺陷 | `CLV-P0` + `CLV-P1` + `CLV-P2` 全绿，单测与 `-race` 零回归 |
| **1.1 跨设备与模板完善** | 统一 SQLite 迁移框架、`MergeLearning` 合并、多语言模板库 UI、共享目录密文同步可用 | `CLV-P3` 全绿 + 双目录/双设备密文同步端到端验证 |
| **1.2 工程与双平台收口** | Windows 桥接测试与真机校验闭环、GitHub Actions CI 与可复现 `ldflags` 打包落地 | `CLV-P4` 核心批次绿 + 三平台 `go vet` & 打包产物校验 |

---

## 1. 四项锁定架构决策（D1–D4）

以下决策已锁定，后续批次严格遵守，不再反复摇摆：

| 编号 | 决策内容 | 落地约束 |
| --- | --- | --- |
| **D1** | **多设备同步采用「共享目录密文同步」优先** | 基于 iCloud Drive / Dropbox / Syncthing 等用户自有同步盘读写加密信封（零服务端运维），密码学升级为 v2 双向可解模型后再视需要扩展 LAN/HTTP 传输；禁止明文落盘敏感数据。 |
| **D2** | **彻底移除 IMK 死残留；冻结 `plugins` / `skills`；MCP 延至 Phase 4** | 删除 `cmd/clavis-imk/` 与零调用的 `internal/bridge/imk_ipc.go`（`imk_install.go` 仅保留至 `selfcheck.go` 解耦完成）；Clavis 坚持「第二输入法（Inline Hook）」定位；`internal/plugins/` 与 `internal/skills/` 进入冻结态不再扩新功能；MCP stdio 集成放入 `CLV-P4` 可选范围。 |
| **D3** | **1.x 坚持自用优先 + CI 与可靠打包** | 聚焦个人多设备效率与工程稳定性（GitHub CI、`ldflags` 版本注入、本地可复现打包），不引入 2.0 面向外部大众分发的签名公证/自动更新器/模板发布市场等纯维护负担。 |
| **D4** | **Security Vault 密文隔离存储，与 `ai-keys.toml` 完全解耦** | Vault 密钥正文独立加密落盘至 `vault_secrets.enc`（AES-256-GCM，32 字节主密钥优先 OS Keychain、回退 `0600` `vault.key`），严禁混入明文 `ai-keys.toml`；Agent 与诊断路径永远只能观测元数据 `SecretRef` 与 `CopyReceipt`。 |

---

## 2. 五阶段统一执行计划（`CLV-P0` – `CLV-P4`）

### Phase 0（`CLV-P0`）：安全与核心正确性止血 — **Completed**

| 批次 | 状态 | 合并提交（实现提交） | 核心交付 |
| --- | --- | --- | --- |
| `CLV-P0-1` | ✅ Completed | `b5c086d` (`c161c7f`) | Security Vault `vault_secrets.enc` AES-256-GCM 加密与 `ai-keys.toml` 隔离（D4）、撤销即删底层密文、`FileStore` 并发竞态修复、安全剪贴板 TTL 归属校验清理 |
| `CLV-P0-2` | ✅ Completed | `0468f29` (`d378f8c`) | Agent 会话控制态按会话隔离（防跨面板串态）、Herdr 参数防选项注入与统一密钥拦截、过滤态 Capsule 导航索引修复 |
| `CLV-P0-3` | ✅ Completed | `9dff5b0` (`6ffd7fa`) | 标注文字 CJK 字体回退与 MediaStore 入库、`NSScreenCaptureUsageDescription` 补齐、Shader 时间/Alpha 修复、翻译换源自动重置方向、Hub 导航与设置页滚动修复 |
| `CLV-P0-4` | ✅ Completed | `5cde130` (`f507087`) | 桌面端剪贴板隐私过滤与 AI 用量遥测接线、翻译/AI 续写（rune 安全切片 + 5s 退格撤销 + 安全输入门禁）/划词悬浮条修复、Annotate 窗口生命周期收口 |

---

### Phase 1（`CLV-P1`）：核心引擎、接线与 120Hz 性能闭环 — **Completed**

| 批次 | 状态 | 合并提交（实现提交） | 核心交付 |
| --- | --- | --- | --- |
| `CLV-P1-1` | ✅ Completed | `021e44c` (`398861b`) | 输入法 Bigram 前缀匹配修复、词典热更新 `DictionaryWatcher` stat/故障恢复加固、starter 词典摘要校验 |
| `CLV-P1-2` | ✅ Completed | `621b980` (`be4118c`) | macOS IME 钩子重置与自应用防递归守卫、输入引擎动态开关候选 UI、用户黑名单与词库接线、`--self-check` 严格只读化 |
| `CLV-P1-3` | ✅ Completed | `da85969` (`b869603`) | Codex `state_5.sqlite`（`mode=ro`，禁 `immutable=1`）与 `pkg/core/terminal` Resolver 接入 Agent 会话、设置页 IPC 缓存、去重设置卡片 |
| `CLV-P1-4` | ✅ Completed | `79d325b` (`f8c7b58`) | 剪贴板卡顿治理 Commit 4：COW Capsule 状态快照（`sealSlice`）、行元数据与预览缓存、60 行首屏预览上限，达成 120Hz 帧预算基准 |

---

### Phase 2（`CLV-P2`）：架构减债、守护生命周期与文档真源 — **In Progress**

| 批次 | 状态 | 范围与目标 |
| --- | --- | --- |
| `CLV-P2-1` | 🔄 In Progress | `uiapp` 架构重构：拆分超大视图与控制器职责，收敛状态同步与事件分发边界 |
| `CLV-P2-2` | 🔄 In Progress | i18n 守卫与 UX 抛光：消除残留硬编码文案，补齐中英双语测试守卫与交互细节 |
| `CLV-P2-3` | 🔄 In Progress | 守护进程生命周期收口：`internal/daemon` 启动/优雅退出/信号/Watcher 生命周期与幂等恢复 |
| `CLV-P2-4` | 🔄 In Progress | IMK 死残留清理（删除 `cmd/clavis-imk/` 与 `internal/bridge/imk_ipc.go`）、确立 `docs/ROADMAP.md` 唯一真源、刷新 `README.md`、补齐 11-23~24 新特性三篇事实规格 |

---

### Phase 3（`CLV-P3`）：数据一致性、模板平台与多设备密文同步 — **Planned**

| 批次 | 状态 | 范围与目标 |
| --- | --- | --- |
| `CLV-P3-1` | ⏳ Planned | 统一 SQLite Schema 迁移框架：按库独立版本号、迁移前自动备份、单库事务与未知版本 fail-closed（参见 `docs/backend/schema-migration.md`） |
| `CLV-P3-2` | ⏳ Planned | Bigram 与学习数据快照：`MergeLearning` 可交换合并、`log1p` 频次压缩与 pinned 永久保护（参见 `docs/design/ime-dictionary-architecture.md`） |
| `CLV-P3-3` | ⏳ Planned | Template Platform T5：多语言模板目录激活、`promptrepo` 目录集成与设置页模板库管理 UI（参见 `docs/design/template-platform.md`） |
| `CLV-P3-4` | ⏳ Planned | 多设备同步加固与目录传输（D1）：密码学 v2 双向可解信封（ADK/ECDH）、共享目录密文传输后端、冲突可见性与同步审计 |

---

### Phase 4（`CLV-P4`）：跨平台校验、CI/打包与可选扩展 — **Planned**

| 批次 | 状态 | 范围与目标 |
| --- | --- | --- |
| `CLV-P4-1` | ⏳ Planned | Windows Parity 收口与桥接测试：补齐 `internal/bridge/*_windows_test.go`、`internal/capture` 区域框选、真机手测回归记录（参见 `docs/windows-parity-audit.md`） |
| `CLV-P4-2` | ⏳ Planned | GitHub Actions CI 与可复现打包：三平台 `go vet`、`go test -race`、构建期 `ldflags` 版本/commit 注入与打包体积检查（D3） |
| `CLV-P4-3` | ⏳ Planned | 可选扩展（按需排期）：`NotificationStore`（只读 DSH/飞书游标通知，参见 `docs/backend/notification-store.md`）、本地模型 C1 路由（参见 `docs/design/local-model-architecture.md`）、MCP stdio 集成（D2） |

---

## 3. 明确不做与冻结范围

- **原生 IMK / TSF 输入法宿主**：永久否决（D2）。Clavis 定位为英文系统输入法之上的「第二输入法」，不追求内联组字或提权窗口接管。
- **`internal/plugins` 与 `internal/skills` 扩展**：代码保持编译通过与冻结状态（D2），不再新增宿主扩展面。
- **2.0 外部商业分发链路**：苹果公证、自动更新器、模板发布市场、团队云账号与实时 CRDT 协同不在 1.x 路线内（D3）。
- **通用多步 Agent 编排**：超过单步/受控提示的复杂编排交由 `herdr` 独立处理，Clavis 仅提供发现、预览与显式确认控制面。

---

## 4. 风险与协作纪律

1. **禁止未关账的旧文档继续误导**：每个阶段合入后须同步更新本文件状态与基线 SHA，避免重复盘点已落地项。
2. **并行 worktree 文件归属隔离**：同一目录不同职责必须按文件显式划分（尤其是 `uiapp/` 与 `internal/bridge/`），严禁使用 `git add -A`。
3. **跨平台编译门禁常绿**：任何合入前必须通过 `go vet ./...`、`GOOS=windows GOARCH=amd64 go vet ./...`、`GOOS=linux GOARCH=amd64 go vet ./...` 与相关包 `-count=1` 单测。

---

## 5. 设计文档索引

### 当前事实规格（Ground Truth）
- **截图标注与媒体存储**：[`docs/design/capture-annotate-spec.md`](design/capture-annotate-spec.md)
- **Herdr 控制面与 Agent 会话**：[`docs/design/herdr-control-plane-spec.md`](design/herdr-control-plane-spec.md)
- **AI 工作台、OCR/翻译与续写**：[`docs/design/ai-workbench-spec.md`](design/ai-workbench-spec.md)
- **视觉语言与空间 3D 规范**：[`docs/design/visual-language.md`](design/visual-language.md)
- **模板平台（Phase 3 实施依据）**：[`docs/design/template-platform.md`](design/template-platform.md)
- **多设备同步（Phase 3 结合 D1）**：[`docs/design/multi-device.md`](design/multi-device.md)
- **SQLite 迁移契约（Phase 3）**：[`docs/backend/schema-migration.md`](backend/schema-migration.md)
- **Windows 能力对账（Phase 4）**：[`docs/windows-parity-audit.md`](windows-parity-audit.md)

### 已取代 / 历史归档文档（顶部已标注状态横幅）
- [`docs/design/security-vault-plan.md`](design/security-vault-plan.md)（已实现并由 `CLV-P0-1` 升级为 `vault_secrets.enc` AES-256-GCM）
- [`docs/design/clipboard-jank-review.md`](design/clipboard-jank-review.md)（方案 B 全部 4 个 commit 已于 `CLV-P1-4` 收口）
- [`docs/design/agent-integration-spec.md`](design/agent-integration-spec.md)（已由 `herdr-control-plane-spec.md` 与 `CLV-P0-2`/`CLV-P1-3` 取代）
- [`docs/design/clavis-followup-spec.md`](design/clavis-followup-spec.md)（已拆分收口至 `CLV-P0`–`CLV-P4`）
- [`docs/design/backend-roadmap.md`](design/backend-roadmap.md)（2026-11-20 盘点，剩余项并入 `CLV-P3`/`CLV-P4`）
- [`docs/design/unmerged-branches.md`](design/unmerged-branches.md)（2026-11-22 分支盘点历史记录）
