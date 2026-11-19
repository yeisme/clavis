# 设计：Clavis 自建输入引擎（Windows 优先）
基线 `c1f0c58`（Wave 0 清账时的 HEAD）。

基线 `458d7fe`。配置节 `[input_engine]` 早已存在（`pkg/core/config/main_config.go:1044-1138`，默认 `enabled=false`、`mode="inline_hook"`、`candidate_count=7`、`shift_toggle_ascii=true`；校验 `pkg/core/config/validator.go:315-322`，`candidate_count` 3..9）。本设计沿用该节。

## 1. 结论

| 路线 | 结论 |
| --- | --- |
| A. TSF in-proc TIP（C++/COM DLL + Go 边车 IPC） | **本轮不可行**。依据：TIP 必须是被宿主进程加载的 COM in-proc DLL；纯 Go 产出 DLL 需 `-buildmode=c-shared`（依赖 cgo），违反「不引入 CGO」硬约束；且需管理员安装器写 `HKLM\...\CTF\TIP`。列为 V2。 |
| B. 全局 `WH_KEYBOARD_LL` + 自有候选浮窗 + `SendInput(KEYEVENTF_UNICODE)` | **MVP 采用**。钩子/注入/无焦点浮窗/光标定位全部已有；纯 Go；默认关闭零侵入。代价：无内联 preedit（组字串显示在候选窗首行）；提权窗口、独占全屏游戏不可用。 |

因此 P4 交付路线 B 的可用 MVP，而不仅是骨架；TSF 边车写在 §9 作为后续计划。

## 2. 可复用基础设施（证据）

- 键盘钩子：`internal/bridge/bridge_windows.go:473` `runKeyboardHook`（`SetWindowsHookExW` @487，锁线程消息泵 @505-513）。
- 钩子回调：`bridge_windows.go:523` `handleHookProc`：自注入过滤 @534 → keymap @544-565 → 修饰键/双击 @567-573 → `router.HandleKeyDownWithIME` @585。
- 自注入过滤：`bridge_windows.go:534`，`DwExtraInfo == SyntheticEventTag(0x434C5653)` 或 `LLKHF_INJECTED`（@42）。
- 文本注入：`bridge_windows.go:990` `InjectText`（`SendInput` Unicode + tag）。
- 光标锚点：`bridge_windows.go:811` `resolveWinCaretAnchor`（`GetGUIThreadInfo`），回退窗口 @843 / 鼠标 @860。
- 无焦点浮窗：`main.go:290` `imePillPanel`（Panel/Frameless/AlwaysOnTop/SkipTaskbar），`main.go:909` `ShowInactive()` 不抢焦点；`main.go:1193` `computeIMEPillBounds` 多屏夹取。候选窗按同一模式新建 `candidatePanel`。
- IME 状态：`internal/bridge/winime_windows.go`（`SendMessageTimeoutW`，100ms，`SMTO_ABORTIFHUNG`）。
- 拼音数据：`pkg/core/engine/pinyin_table.go`（Unihan U+4E00..U+9FA5 → 音节索引），`pkg/core/engine/pinyin.go:94` `HanziSyllables`。可反转成「音节→字」，零依赖。
- AI：`pkg/core/ai/client.go:387` `StreamCompletion`；profile 选择 `main_config.go:361/380`；超时/取消/gen 防串参照 `uiapp/controller.go` `startAIStream`。

## 3. 路线对比

### 3.1 工作量

| 维度 | A. TSF | B. 钩子 |
| --- | --- | --- |
| 构建 | C++ COM DLL ×(x64/x86/ARM64)，32 位宿主需 32 位 TIP | 纯 Go |
| 注册 | HKLM + `ITfInputProcessorProfileMgr::RegisterProfile`，需管理员 | 无 |
| 进程 | DLL 在每个宿主进程，崩溃带崩宿主；需命名管道 IPC | 单进程 |
| preedit | 有 | 无（候选窗首行显示） |
| 提交 | `ITfInsertAtSelection`，原子可撤销 | `SendInput` |
| 估计 | 4–8 人周（Weasel ≈ 3 万行 C++） | ≈1 人周 |

### 3.2 兼容矩阵（路线 B）

| 目标 | 钩子收键 | SendInput 提交 | 候选窗定位 | 结论 |
| --- | --- | --- | --- | --- |
| 经典 Win32 | 是 | 是 | caret 精确 | 可用 |
| Win11 新记事本 | 是 | 是 | 常无 caret → 窗口锚点 | 可用，位置偏 |
| Electron/Chromium | 是 | 是 | 无 caret → 窗口/鼠标 | 可用，位置偏 |
| UWP | 是 | 通常是 | 窗口锚点 | 基本可用 |
| 提权窗口（Clavis 未提权） | **否**（UIPI） | 否 | — | 不可用；按键原样透传，**失败安全** |
| 独占全屏游戏/反作弊 | 可能 | 常被忽略 | 浮窗不可见 | 不可用；MVP 用切换键关闭，V1 加进程排除名单 |
| 密码框 | — | — | — | 不接管（hook-robustness §4） |
| RDP/VM 客户端 | 是 | 发往客户端 | — | 不建议，同游戏 |

总原则：**拿不准就透传**。只有「enabled + 引擎中文态 + 系统 IME 为 ASCII 态 + 非密码框 + 前台非 Clavis + 无 Ctrl/Alt/Win」才消费按键。

### 3.3 Rime / Weasel / Squirrel 参考结论

- Weasel = 薄 TSF TIP + 独立 `WeaselServer.exe`（命名管道），引擎在服务进程。证实 V2 形态：Clavis Go 进程当 server，TIP 只转发 `ITfKeyEventSink`/composition/candidate。
- Squirrel 走 IMK；Clavis 已预留 `imk_socket_path`（`main_config.go:1055`），同为「系统框架薄壳 + 外部引擎」。
- Rime 核心（切分→词图→排序→学习）平台无关。Clavis MVP 取最小子集，放无 build tag 的 `pkg/core/inputengine`，darwin 可单测。

## 4. 组字状态机（`pkg/core/inputengine`，纯逻辑）

```text
Idle ──a-z──▶ Composing(raw, candidates, page, sel)
Composing ──a-z / '──▶ Composing（重切分 + 查词）
Composing ──Backspace──▶ raw 空 ? Idle : Composing
Composing ──1..9 / Space / Enter──▶ 提交 → Idle 或剩余音节继续 Composing
Composing ──Esc──▶ Idle（丢弃）
Composing ──- / =──▶ 翻页
Composing ──Ctrl/Alt/Win 组合、方向键、Tab、鼠标点击──▶ Idle（丢弃，按键透传）
任意 ──Shift 单击（shift_toggle_ascii）──▶ 切换中/英
```

| 键 | Composing | Idle |
| --- | --- | --- |
| a-z | 追加，消费 | 开始组字，消费 |
| `'` | 分隔，消费 | 透传 |
| 1..9 | 提交第 n 项，消费 | 透传 |
| Space | 提交高亮项，消费 | 透传 |
| Enter | 上屏 raw 原文，消费 | 透传 |
| Backspace | 删末字符，消费 | 透传 |
| Esc | 丢弃，消费 | 透传 |
| - / = | 翻页，消费 | 透传 |
| 其他可打印键 | 先提交首候选，再透传该键 | 透传 |

MVP 切分：合法音节表最长匹配 + 回溯；末段允许为合法音节前缀；`'` 强制断开。候选 = 本地词表（整串 / 首字母简拼）∪ 首音节单字（内嵌常用字频序）。部分提交后剩余音节继续组字。

## 5. 候选来源

- 本地：反转 Unihan 表 + 内嵌常用字频序 + 小型常用词表。离线、确定性、可单测。
- AI 异步：UI 层在 raw 停顿 250ms 后用当前 `[ai]` active profile 请求（拼音串 + 应用名，≤5 个候选）。raw 变化即 `cancel()` 并 gen 自增，过期响应丢弃；超时 1.5s；失败/未配置 **静默降级**（不弹 HUD、不记诊断），只显示本地候选。AI 候选排在本地之后并标 ✦，永不在钩子线程执行。不发送剪贴板或周边文本。

## 6. 互斥规则（P4 单测覆盖）

钩子顺序：`自注入过滤 → keymap → 修饰键/双击 → [输入引擎] → 热键 & snippet 路由`。

1. keymap 优先；其合成键带 tag，在 534 行被过滤，不回流引擎。
2. 带 Ctrl/Alt/Win 的组合：引擎 Reset 后透传，capsule/clipboard 热键照常。
3. 修饰键不进引擎，leader/双击检测不受影响；前台为 Clavis 自身（`engines.go:1127` `isSelfClavisApp`）时引擎不接管。
4. snippet：引擎消费的键不进 `HandleKeyDownWithIME`；提交后发 `TriggerSnippetReset`，等同现有 `imeComposing` 分支（`bridge.go` `HandleKeyDownWithIME`）。
5. 系统 IME：仅当系统转换态为 ASCII 时接管，避免与微软拼音双重组字。推荐 en-US 为默认布局，中文交给 Clavis。
6. 鼠标按下（`WH_MOUSE_LL`，P1 C2）→ 引擎 Reset。

## 7. 提交方式

| 方式 | 优 | 劣 | 选 |
| --- | --- | --- | --- |
| `SendInput` Unicode | 走输入队列，Chromium/UWP 都收；可带 tag 自过滤；`InjectText` 已验证 | UIPI 拒提权窗口；非 BMP 需代理对 | MVP |
| `PostMessage(WM_CHAR)` | 不经队列 | Chromium/UWP 不处理；与真实按键乱序 | 否 |
| `ITfInsertAtSelection` | 原子、可撤销 | 必须在 TIP 内 | V2 |

时序：钩子线程在 `return 1` 前同步 `SendInput`，保证提交文本排在被消费键之后、后续真实键之前。

## 8. 自我触发过滤

提交文本带 `SyntheticEventTag` 且系统置 `LLKHF_INJECTED`，在 `bridge_windows.go:534` 直接透传，不回流引擎/snippet。

## 9. 里程碑

| 阶段 | 内容 |
| --- | --- |
| MVP（P4） | 路线 B；全拼；单字 + 小词表；候选窗 ≤9；数字/空格/回车/退格/Esc/翻页；Shift 切中英；AI 联想静默降级；密码框不接管。`enabled=false`（默认）时不创建引擎对象，钩子内仅一次 nil 判断。 |
| V1 | 用户词频学习、`custom_dict_paths`、双拼 `scheme`、`fuzzy_pinyin`、进程排除名单、全角标点。 |
| V2 | TSF 薄 TIP（独立 C++ 仓库 + 签名安装器）↔ Clavis 命名管道 server；内联 preedit、提权窗口、`ITfInsertAtSelection`；路线 B 作为未装 TIP 时回退，新增 `mode="tsf"`。 |

## 10. 风险与回退

| 风险 | 缓解 / 回退 |
| --- | --- |
| 钩子回调超时被系统静默摘除 | 内存查表；IME 状态读缓存（hook-robustness §5）；AI 不在钩子线程；看门狗重装（P1 C1）。 |
| 吞键 | 条件严格；引擎 panic → recover + Reset + 透传；`enabled=false` 一键回退。 |
| 候选窗抢焦点 | `ShowInactive`（`main.go:909` 已验证）。 |
| 定位不准 | 窗口/鼠标锚点回退，同 pill。 |
| macOS 回归 | 逻辑在纯 Go 包，接线只在 `*_windows.go`；`bridge_darwin.go` 不动。macOS 上 `enabled=true` 暂不生效（后续走 IMK）。 |
| 词频学习写库阻塞钩子（Windows 原生 SQLite 往返约 12.6ms/次，自动提交另含 fsync） | 钩子线程、提交路径、UI 线程只调用 `storage.LearningWriter` 的 `Queue*`：同步更新 RAM 镜像（`LookupBoost` 立即生效），持久化由后台按 750ms / 256 条合并为一个事务（计数按增量 UPSERT）；**禁止**在这些路径直接调用 `LearningStore.RecordIMECommit` / `RecordItemUse`。失败重试 3 次后丢弃并计入 `Stats().Dropped`。 |

## 11. 技术债：`mixed_english` / `auto_pair_punctuation`（2026-11-15 记账）

两个键从未接到 `pkg/core/inputengine`：引擎只收 `candidate_count`、`shift_toggle_ascii`、`scheme`、`fuzzy_pinyin`、`custom_dict_paths`、`user_learning`；读 `AutoPairPunctuation` 的只有 `pkg/core/ime.Composer`，而它没有运行时调用方。为不让用户改一个无效开关，2026-11-15 起这两个键不再写入默认模板、不再读取、不再写回（老配置里残留的键会被忽略，下次保存时清除）。`InputEngineConfig` 的两个字段暂留并标 Deprecated，因为 `internal/bridge/input_engine_keys.go` 的 `inputEngineConfigEqual` 仍在比较它们（该文件当前由 Windows 副本冻结）。

接入估时超过 2 小时，需要：

1. 引擎：中文模式下的全角标点映射；成对符号（`（）`、`【】`、`“”`、`《》`）插入后光标回到中间，并在随后输入闭合符号时跳过已补的那一个（type-over），导航键、点击、回车时清空待闭合栈；数字后的 `.` `,` `:` 保持半角（`3.14`、`1,000`）；`Result` 增加光标移动字段。
2. 桥接：上屏后按 `Result` 移动光标。darwin 用 `postTaggedKey` 发左右方向键；Windows 用 `sendWinKeyTap(vkLeft/vkRight)`（Windows 设备负责）。
3. **已知缺陷（darwin）**：`inputEngineKeysForInline` 对 `, . ? ! ; :` 这类可打印标点返回 `!ok`，按键直接放行给应用。组字中打标点时，标点会先于尚未上屏的拼音进入应用。需要补一个「可打印字符 → `KeyOther`」分支，与 Windows 的 `engineKeyForVK` 对齐。另外 darwin 按键名分类：`quote` 键打出 `"` 仍归为 `KeyApostrophe`，`minus` 键打出 `_` 仍归为翻页键，需要在引擎侧按实际字符归一化。
4. `mixed_english`：输入串没有任何词典候选覆盖时，把原始字母作为首选（否则放在末位）；纯 ASCII 词不进学习库（在 `main.go` 的提交回调里过滤）。
5. 接通后恢复这两个配置键和设置页开关，再同时删掉 Deprecated 字段及 `inputEngineConfigEqual` 里的两行比较。
