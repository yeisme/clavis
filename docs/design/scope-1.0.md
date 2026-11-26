# Clavis 1.0 范围真相

日期：2026-11-26
目标：macOS 自用稳定版 1.0
状态：范围冻结，发布仍受运行时验收阻断

这份文档定义用户在 README、Settings 和 ROADMAP 里能看到的 1.0 承诺。代码里已经存在的基础库、测试 fixture 和跨平台构建目标继续保留，但没有通过真实环境验收的能力不写成 ready。

## 1.0 承诺

| 能力 | 1.0 口径 |
| --- | --- |
| Launcher / Omnibox | macOS 键盘启动器，支持 Actions、Clipboard、Snippets、AI、Glossary、Agent 六个 tab，以及 Stats、Manage 两个运营面板。 |
| 剪贴板与媒体 | 本地 SQLite/FTS5 剪贴板历史、图片与文件元数据、pin/delete、隐私过滤、MediaStore 缩略图和预览。 |
| Security Vault | 本地 AES-256-GCM 密文存储、Keychain/`0600 vault.key` 回退、metadata-only SecretRef、用户显式 TTL 复制和撤销删除。 |
| QuickJump | `#` 书签、目录、工作树、URL 模板和 Herdr 不可用时的终端降级。 |
| 截图与标注 | macOS 交互截图、Annotate 矢量工具、PNG 保存/复制和 AI OCR/Ask AI 入口。必须先完成 Screen Recording 和 A1–A16 手测。 |
| AI 基础流 | 已配置 profile 时提供文本 AI、Vision OCR、翻译、续写和 diff review。没有 key 或 provider 不可用时显示稳定诊断，不阻塞启动。 |
| Herdr | 只读发现、显式确认的 focus/rename/send/wait 控制和无 binary 降级。Herdr daemon 本身不是 1.0 安装前提。 |
| Snippets / Glossary / Templates / Workflows | 本地 TOML 配置、校验、Composer 接线和 secure-input 防护。远端模板市场不属于 1.0。 |
| 输入法引擎 | 作为 opt-in second-IME 能力保留。默认配置不把它当作已完成的系统输入法替代品。 |

## 不属于 1.0 承诺

| 能力 | 处理方式 |
| --- | --- |
| Windows parity | 保留代码、CI vet/build 和测试。Windows 真机键钩子、IME、UIPI、区域截图尚未签收，1.0 不写 Windows ready。 |
| 多设备同步 | 保留 v2 crypto、DirTransport、CLI 和只读 Settings 状态。初始化、配对、恢复码和冲突 UI 未闭环，1.0 不写“同步可用”。 |
| P5 annotation store / taxonomy | 保留 schema、纯函数和测试。当前没有 runtime consumer，不写成统一标签系统或已交付注释中心。 |
| P5 identities | `identities.toml` 只作为兼容配置保留。当前没有 persona/env profile 数据模型、编辑器或运行时身份管理器，不提供身份中心。 |
| Magpie 专页 | 不新增 provider 专用页面。保留通用 AI profile、`api_key_env_var` 和 self-check 的 key source 诊断。 |
| ACP | 当前仓库没有 ACP 实现、入口或验收，不新增协议层。Herdr 控制面和冻结的 MCP 内部包各自保持现状。 |
| 独立 Assets Hub | 不作为 1.0 产品入口承诺。MediaStore、Vault、Pinned 和 Templates 通过 Omnibox、Manage 和 Settings 访问。 |
| 空间 3D 默认路线 | 保留稳定的 classic/liquid glass 和 Reduce Motion。spatial 3D 不作为 1.0 价值承诺。 |
| 公共分发链路 | 1.0 目标是 macOS 自用。Developer ID 签名和干净安装验证仍是发布门槛，公证、自动更新、模板市场、团队云账号和 CRDT 协作延期。 |

## 发布前阻断

以下条件完成前，版本只能标为“范围已冻结，发布未验收”。

1. `go test -race ./internal/watcher ./uiapp` 连续通过，修复 agent refresh settle、Codex fixture 和 leader rail 的竞态失败。
2. macOS A1–A16 GUI 手测完成，覆盖 Capsule、Clipboard、输入法 opt-in、Selection Bar、Annotate、Vault TTL 复制和 Herdr fallback。
3. Onboarding/Diagnostics 明确显示 Accessibility、Input Monitoring、Screen Recording 三类权限及受影响功能。
4. 至少一个真实文本 provider、一个真实 Vision provider、缺 key、超时和无 Herdr 五条 smoke 路径完成。
5. 旧 App DB v0/v1/v2 到 v3 的迁移、WAL、备份回验和 failpoint rollback 完成。
6. macOS arm64 包完成签名、干净目录安装、`--smoke-check`、`--self-check` 和版本校验。

## 证据基线

- `go test -count=1 ./...` 已通过，普通测试不等于真实桌面签收。
- `go build ./...`、Windows amd64/arm64 build、Linux amd64 build 和三平台 vet 已通过。
- 当前真实 self-check 曾报告 `accessibility=false`、`input-monitoring=false`、`input engine: enabled=false`、`ai key source: none`。
- 当前真实 sync status 曾报告 `code=not_configured`。
- 详细功能、竞品、技术债和取舍见 [`launch-readiness-2026-11-25.md`](launch-readiness-2026-11-25.md)。
