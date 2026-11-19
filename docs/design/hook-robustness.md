# 设计：按键 / 钩子健壮性（Windows）
基线 `c1f0c58`（Wave 0 清账时的 HEAD）。

基线 `458d7fe`。

## 1. 现状（证据）

- 键盘钩子在独立锁线程：`internal/bridge/bridge_windows.go:473` `runKeyboardHook`，安装 @487，失败只在 **启动时** 通过 `ready` 返回（@489）；之后钩子被系统摘除不会有任何感知。
- `TapRecoveryCount`（@337）在 Windows 上恒为 0：没有恢复路径。
- 回调内同步 IME 查询：`bridge_windows.go:584` 每个 keydown 调 `winIsNativeConversionMode()`（@1277），它向前台 IME 窗口发 `SendMessageTimeoutW`（`winime_windows.go`，`imeMsgTimeoutMs=100`）。最坏每次按键 2×100ms。
- macOS 在鼠标按下时重置双击/leader/snippet（`bridge_darwin.go:849`，事件掩码 @783）；Windows 没有鼠标钩子，点击换位置后 snippet 缓冲仍保留旧前缀 → 可能在新位置误展开。
- 无密码框检测：Windows `HandleKeyDownWithIME` 的 `secureInputActive` 恒传 `false`（@585）。

## 2. 钩子摘除 / 超时检测与重装（C1）

系统行为：LL 钩子回调超过 `LowLevelHooksTimeout`（注册表，默认约 300ms–1s；Win7+ 上限 1000ms）后，Windows 7+ 会 **静默摘除** 该钩子，不发任何通知。会话锁屏/UAC 安全桌面期间不投递事件，但不会摘钩。

检测（心跳，2s）：

- 钩子回调每次进入时原子写 `lastHookTick = GetTickCount()`。
- 看门狗读 `GetLastInputInfo().dwTime`（系统最后一次输入，键盘 **或鼠标**）。
- 判定「疑似摘除」：`lastInput - lastHookTick > 2s` 且连续 2 次心跳成立（tick 用 `uint32` 减法处理 49.7 天回绕）。
- 由于 `GetLastInputInfo` 也统计鼠标，必须同时以 `WH_MOUSE_LL`（§3）刷新 `lastHookTick`，否则纯鼠标操作会被误判；鼠标钩子安装失败时，判定门限放宽到 30s，并在诊断里标出 "mouse hook unavailable"。
- 误判代价低：重装 = `UnhookWindowsHookEx` + `SetWindowsHookExW`，必须在钩子线程执行（LL 钩子绑定安装线程的消息泵），通过 `PostThreadMessageW(WM_APP+1)` 请求。

重装失败：记录 `lastHookError` 与次数，在设置 → 诊断经 `PermissionStatus.Details`（`hookReinstalls`、`hookError`、`mouseHook`）展示；不弹窗、不退出。指数退避（2s、4s … 上限 60s）。

纯逻辑：`hookWatchdog`（无 build tag）`Observe(lastInput, lastHook uint32) action`，单测覆盖正常、鼠标活动、回绕、连续两次才触发、退避。

## 3. 是否需要 WH_MOUSE_LL（C2）

成本评估：

| 项 | 评估 |
| --- | --- |
| 性能 | 每次鼠标事件（含 `WM_MOUSEMOVE`，可达 1000Hz）进入回调。只做 `wParam` 比较 + 原子写 + `CallNextHookEx`，μs 级；不加锁、不发消息。 |
| 超时风险 | 回调无阻塞调用，远低于 `LowLevelHooksTimeout`。 |
| 权限 | 与键盘 LL 钩子相同（无需管理员，UIPI 限制相同）。 |
| 隐私 | 不读坐标、不记录。 |

结论：**采用**。只关心 `WM_LBUTTONDOWN(0x201)` / `WM_RBUTTONDOWN(0x204)`（另加 `WM_MBUTTONDOWN` 无害，但按需求只做左右键）；命中后非阻塞投递一个重置信号（带合并的 channel），由 worker 执行：`doubleTapDetector.Reset()`、`leaderDetector.Reset()`、`TriggerSnippetReset`、输入引擎 Reset。回调本身立即 `CallNextHookEx`。

替代方案（若钩子安装失败）：看门狗每 2s 用 `GetAsyncKeyState(VK_LBUTTON/VK_RBUTTON)` 采样——只能抓到采样时刻正按着的键，漏检率高，仅作降级并在诊断中显示。

## 4. 密码框保护（C3）

保守实现，**任一信号命中即视为密码框**：

1. `GetGUIThreadInfo` 取前台线程焦点 `hwndFocus`；`GetClassNameW`：`Edit`/`RichEdit*` 且 `GetWindowLongPtrW(GWL_STYLE) & ES_PASSWORD(0x20)`。
2. 对 `Edit` 类 `SendMessageTimeoutW(EM_GETPASSWORDCHAR)` 非 0（50ms，`SMTO_ABORTIFHUNG`）。
3. 前台进程名命中内置名单（大小写不敏感）：`credentialuibroker.exe`、`consent.exe`、`logonui.exe`、`1password.exe`、`keepass.exe`、`keepassxc.exe`、`bitwarden.exe`、`lockapp.exe`。
4. UIA `IsPassword`（Chromium/Electron/WinUI 的 `<input type=password>` 只能这样识别）：需要 COM `IUIAutomation::GetFocusedElement` + `CurrentIsPassword`。**本轮不做**：依据——在钩子回调中做 UIA 跨进程调用可能耗时数十至数百 ms，会触发钩子超时；放到观察线程又需要稳定的焦点变化事件（`SetWinEventHook(EVENT_OBJECT_FOCUS)`）。替代：V1 用 `SetWinEventHook` 在后台线程异步刷新 `IsPassword` 缓存。

执行位置：判定在 **IME 观察线程**（200ms 周期，`bridge_windows.go:631`）计算并写入原子缓存；钩子回调只读缓存（ns 级）。命中时：snippet 路由收到 `secureInputActive=true`（不记录、不展开），输入引擎不接管、隐藏候选。纯函数 `isSecureFieldSignals(class, style, pwChar, process)` 单测覆盖。

已知漏检：浏览器/Electron 网页密码框（需 UIA，见上）。用户可通过 snippet 只在空闲时匹配的既有设计降低风险；文档与诊断页会注明。

## 5. 回调内 IME 查询延迟

问题：`bridge_windows.go:584` 每个 keydown 同步查询 IME；前台应用挂起时 `SMTO_ABORTIFHUNG` 立即返回，但「忙而未挂起」的应用会吃满 100ms ×2，累积有被摘钩风险。

改造：观察线程（200ms）已经调用 `CurrentInputSource`，顺带把「native 转换态」写入原子缓存（带时间戳）；钩子回调读缓存，缓存陈旧（>500ms）才回退同步查询。代价：用户切换中/英后最长 200ms 内的按键可能按旧态处理 snippet（只影响 snippet 是否被 reset，不影响输入本身）。

压测口径（真机）：

- 指标：钩子回调耗时 p50/p99/max（`QueryPerformanceCounter` 进出差值，仅在 `CLAVIS_HOOK_TRACE=1` 时采样到环形缓冲，诊断页显示）。
- 场景：(a) 记事本连续输入 2000 键；(b) 前台为人为挂起的进程（调试器暂停）；(c) 前台为高负载 Electron（VS Code 大文件）；(d) 微软拼音中文态 vs 英文态。
- 通过标准：p99 < 5ms，max < 150ms，30 分钟内看门狗重装次数 = 0（非休眠场景）。

## 6. 风险与回退

| 风险 | 回退 |
| --- | --- |
| 看门狗误判频繁重装 | 重装幂等；退避；诊断可见次数。 |
| 鼠标钩子影响系统流畅 | 回调无锁无阻塞；安装失败降级为 `GetAsyncKeyState` 采样。 |
| 密码框误判（普通输入被当密码框） | 只关闭 snippet/引擎，不影响正常输入；失败安全。 |
| macOS | 全部改动在 `*_windows.go` 或无 tag 纯逻辑文件；`bridge_darwin.go` 不动。 |
