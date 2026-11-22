# macOS UX P3 验收复核

- 任务：T-clavis-ux-p3
- 依据：`docs/design/mac-ux-spec.md` 第三部分 `[IA-01]`–`[IA-25]`、`[SF-01]`–`[SF-15]`
- 复核范围：当前 `main` 工作树代码；不引入规范之外的新标准。
- 结论规则：每条只有「过」或「不过」；「不过」均给出最小修复建议。
- 测试命令：`go test ./uiapp/...`，实际输出 `ok github.com/yeisme/clavis/uiapp 16.477s`。
- 说明：文中行号以本次复核时工作树为准；旧路线基线 `97261f4` 仅用于 IA-04 的调用点对照。

## 总结

| 类别 | 过 | 不过 |
|---|---:|---:|
| 设置 IA / 行为 IA-01–IA-25 | 3 | 22 |
| 浮层 SF-01–SF-15 | 8 | 7 |
| 合计 | 11 | 29 |

已实现的基础包括：12 页/5 组的导航数据、搜索输入框、草稿切页拦截、空态组件、部分 `SurfaceSpec`/Vibrancy/阴影封装，以及 `go test ./uiapp/...` 通过。但 P1 验收要求的是完整行为和契约，不按「基础已存在」折算通过。

---

## 一、设置页验收

### IA-01 — 不过

**标准：** `settingsPages()` 恰好 12 页、5 组，顺序为输入/效率/智能/系统/关于，key 不重复。

**证据：** 当前没有 `settingsPages()`；实际是 `settingsNavigationSections()`（`uiapp/settings_view.go:77-100`），返回 12 个 section，并由 `settingsGroups()`（`:52-60`）返回 5 组。现有测试 `uiapp/settings_center_test.go:36-56` 验证的是替代实现 `settingsNavigationSections()`，不是规范要求的 `settingsPages()`。

**最小修复建议：** 建立规范命名的唯一 `settingsPages()` 数据源，把现有 12 条数据迁入；让渲染、key 映射和测试全部引用它。

### IA-02 — 不过

**标准：** 全仓不出现 `settingsTabIdx`、`settingsSectionMeta`、`SettingsTabNames`。

**证据：** 当前仍有：

```text
uiapp/controller.go:188       settingsTabIdx
uiapp/controller.go:50        SettingsTabNames
uiapp/settings_view.go:37     settingsSectionMeta
```

并且 `settings_view.go:18-33` 仍保留整数 section 常量。

**最小修复建议：** 将唯一导航状态改为 page key，删除旧 section 类型/下标和 `SettingsTabNames()`；旧 `SettingsTab` 仅保留为外部 API 映射。

### IA-03 — 不过

**标准：** `OpenSettings(tab)` 对 5 个 `SettingsTab` 按 1.3 映射表打开目标页。

**证据：** `uiapp/controller.go:3375-3384` 仍直接执行：

```go
c.uiState.settingsTabIdx = int(tab)
```

而 `settingsNavigationSections()` 中 `SettingsTabGeneral` 对应的是 `triggers`（`settings_view.go:65`），不是“上次停留页/首次快捷键页”的目标映射；没有 `settingsPageForTab`。

**最小修复建议：** 增加 `settingsPageForTab(tab SettingsTab) string`；`OpenSettings` 只写 page key，General 走已确认的上次页策略。

### IA-04 — 不过

**标准：** 1.3 表中 8 个旧调用点源码零改动；已有子命令 case 只增不改。

**证据：** 与基线比较：

```bash
git diff 97261f4 -- main.go uiapp/onboarding_view.go
```

输出：

```text
main.go                  | 261 ++++++++++++++++++++++++++++++++--------
uiapp/onboarding_view.go |  71 +++++++++++++------
```

退出码为 `1`，说明文件发生了改动，不能判定调用点零改动。当前 `main.go:1961-1974` 的 `handleOpenSettings` 仍调用 `mygo.App.Show()`、`win.Show()`、`win.Focus()`；当前也没有对旧调用点做 P1 要求的稳定 page-key 映射。

**最小修复建议：** 保留调用表达式本身，只在 `OpenSettings` 内做映射；以基线逐行锁定 1.3 所列调用点，并新增只读契约测试防止它们被改写。

### IA-05 — 过

**标准：** 12 个 key 可打开；`ime`、`keymap`、`general` 等旧别名可用；未知参数维持默认行为。

**证据：** `settingsSectionFromKey`（`uiapp/settings_view.go:128-138`）支持 `general` 别名和 `settingsNavigationSections()` 中的 12 个 key；`settings_view.go:247-259` 的搜索/导航会按 key 找 section。现有 `go test ./uiapp/...` 通过，且 `TestSettingsNavigationHasFiveGroupsAndSecondaryPages`（`settings_center_test.go:36`）验证了 12 页 key 可寻址。

**边界：** 这是当前 section-key 路由的通过，不代表 IA-01/IA-02 的 page-model 重构已完成。

### IA-06 — 过

**标准：** 5 个组标题、12 个页行；组标题不可切页。

**证据：** `settings_view.go:259-299` 逐组渲染 `settings-rail-group:<group>` 和 `settings-rail:<page>`；组标题是 `ui.Text`，没有 Clicked 处理；页行才在 `:300-323` 处理 click。`TestSettingsNavigationHasFiveGroupsAndSecondaryPages`（`settings_center_test.go:36-56`）通过。

### IA-07 — 不过

**标准：** 960×500 时侧栏可滚动，搜索框不随滚动。

**证据：** `SettingsView`（`settings_view.go:244-256`）的侧栏是 `ui.Column(...).FillHeight()`，没有侧栏 `ui.Scroll`；只有右侧内容区在 `:251-256` 使用 `ui.Scroll`。搜索框位于同一 Column 内，不存在“搜索框固定、侧栏内容滚动”的结构。

**最小修复建议：** 侧栏改为固定搜索框 + 独立 `ui.Scroll` 包住组/页列表，并在 960×500 帧测试中断言搜索框不移动。

### IA-08 — 不过

**标准：** ⌘1…⌘5 跳到每组第一页。

**证据：** 全仓检索没有设置中心的 ⌘1…⌘5 组导航处理；`settings_view.go:141-157` 只有按 section index 的点击切换，`controller.go:3346-3372` 只有 `settings` 子命令分支。

**最小修复建议：** 在设置窗口级按键路由增加组快捷键，按 `settingsGroups()` 顺序定位每组首个 page key，并加帧断言。

### IA-09 — 不过

**标准：** 从片段页保存后切权限页，页头没有药丸。

**证据：** `settings_view.go:232-238` 每页读取全局 `a.settingsStatus`；`controller.go` 有多处跨页面写入（如 `:3818`、`:4092`、`:4201-4205`）；`renderSettingsPaneHeader`（`settings_view.go:368-399`）无论当前页都渲染 `statusMsg`。没有按页 notice 存储或切页清除。

**最小修复建议：** 引入 `settingsNotice{Text,Tone}` 按 page key 存储；切页只显示目标页 notice，离开页清除普通 notice。

### IA-10 — 不过

**标准：** 滚到底后切页，新页第一张卡在视口内。

**证据：** `SettingsView`（`settings_view.go:251-256`）使用一个无 Key 的共享 `ui.Scroll`；`setSettingsSection`（`:141-146`）只改 index，没有重置滚动或调用 `ScrollIntoView`。

**最小修复建议：** 按 page key 使用 `settings-scroll:<page>`，切页下一帧对首元素执行 `ScrollIntoView(ScrollStart)`。

### IA-11 — 不过

**标准：** 即时控件成功后字段行显示「已保存」1.5 秒，页头不显示药丸。

**证据：** `settings_view.go:502-560` 的即时控件只调用 `UpdateMainConfig`；`controller.go:3923` 写入全局 `settingsStatus`；`settings_view.go:368-399` 将其显示为页头 `kitStatusPill`。没有字段行成功标记、1.5 秒计时或 reduced-motion 分支。

**最小修复建议：** 给字段 key 增加短生命周期的 saved 状态，在字段行尾渲染 checkmark/「已保存」，成功提示不再写全局页头药丸。

### IA-12 — 不过

**标准：** 写盘失败时出现页头下方持久横幅，含字段名、文件名、重试、Finder；控件回到旧值；其他字段成功不覆盖横幅。

**证据：** 当前失败只进入 `settingsStatus`，`renderSettingsPaneHeader`（`settings_view.go:387-389`）在页头渲染 `settingsBanner`；没有「重试」和「在 Finder 中显示」按钮，也没有 `settingsFailure{File,Field,Err,Retry}`。现有 `kitErrorBanner` 只有文本（`uiapp/kit_states.go:19-27`）。

**最小修复建议：** 新增按文件持久化 failure banner，复用保存闭包作为 Retry，并将路径拆为文件名行 + typeMeta 完整路径行。

### IA-13 — 不过

**标准：** 失败页侧栏红点跨页保留，重试成功后横幅和红点同时消失。

**证据：** `settings_view.go:300-313` 页行只渲染 badge，没有失败状态点；全仓没有按页 failure map 或 Retry 清除契约。

**最小修复建议：** 在 page/file failure 状态上维护 red-dot，并让成功写入/Retry 原子清除对应 failure。

### IA-14 — 不过

**标准：** 草稿变更时编辑器标题与侧栏有圆点、保存按钮可用；无变更时不可用。

**证据：** `settingsDraftDirty`（`settings_view.go:160-219`）能检测部分片段/术语/工作流/Leader 草稿，但 `settings_view.go:300-313` 侧栏没有 dirty dot；编辑器标题/保存按钮未统一绑定 dirty 状态。当前测试 `TestSettingsDraftDirtyDetectsChangedSnippet` 仅验证布尔检测（`settings_center_test.go:79-90`）。

**最小修复建议：** 为每页输出 `dirty(page)`，在 rail row 和 editor header 使用 `kitStatusDot`，保存按钮用 dirty 作为 enabled 条件。

### IA-15 — 不过

**标准：** 同一编辑器点另一条时不切换，出现保存/放弃/继续编辑；三动作正确，Esc 等于继续编辑。

**证据：** `requestSettingsSection`（`settings_view.go:148-157`）检测 `settingsDraftDirty` 并设置 `leavePrompt`；侧栏在 `:324-345` 渲染继续编辑和放弃按钮。工作流删除/编辑器测试存在于 `settings_center_test.go:1022` 等，`go test ./uiapp/...` 通过。

**限制：** 当前提示的保存动作不是统一的三按钮实现：代码路径显示 `keep_editing` 与 `discard`，保存动作依赖编辑器自身，不是规范要求的统一“保存/放弃/继续编辑”横幅。因此严格按逐条标准判为过？**不应折算**。本条实际为**不过**。

**最小修复建议：** 将 leavePrompt 横幅补齐独立 Save 按钮，并在 Save/Discard 后再切换；Esc 路由统一为 KeepEditing。

### IA-16 — 不过

**标准：** 切页回来草稿保留；关窗再开草稿和圆点都在；关闭按钮有系统未保存圆点。

**证据：** 草稿字段确实保存在 `settingsEditorState`，但 `settingsDraftDirty` 仅支持部分草稿，侧栏无圆点；全仓没有 `SetDocumentEdited` 调用。关闭设置只执行隐藏（`main.go:1961-1974` 通过 `BindFloatingSurface` 的 `Dismiss: settingsWin.Hide`），没有窗口文档编辑状态契约。

**最小修复建议：** 每次 draft 变化调用 `settingsWin.SetDocumentEdited(anyDirty)`；重开复用 editor state，并补关窗再开手测/帧测试。

### IA-17 — 不过

**标准：** 6 类实体删除先行内确认，不弹模态；确认行是 kitInsetWell、取消获得焦点、Esc 取消。

**证据：** 只有工作流存在确认路径：`settings_workflows.go:608-623`，且使用 `kitErrorBanner`（`:614`），正好违反“不是 kitErrorBanner”。其他删除调用仍直接执行（由规范现状表及 `settings_editors.go` 对应路径可见）。没有统一 `kitInsetWell`/取消焦点/Esc 契约。

**最小修复建议：** 抽取实体删除确认行组件；替换 workflow 的 `kitErrorBanner`，给 6 类实体统一 focus cancel 和 Escape cancel。

### IA-18 — 过

**标准：** 键位映射、输入法规则、剪贴板排除项删除立即生效、不确认。

**证据：** 当前行内删除实现仍直接调用删除/保存路径，且未发现这些三类被接入实体删除确认横幅。`settings_view.go`/`settings_editors.go` 中对应行操作通过 `go test ./uiapp/...`。

### IA-19 — 不过

**标准：** ⌘F 聚焦搜索框；“剪贴板”结果至少含效率›剪贴板页和触发页剪贴板快捷键字段。

**证据：** 当前有 `ui.TextInput(c.Key("settings-search"))`（`settings_view.go:247`），但全仓没有设置窗口 ⌘F 路由；输入变化会直接把右侧 section 切到第一个匹配项（`settings_view.go:251-258`），不是规范要求的侧栏结果列表。`settingsSectionMatches`（`:329-336`）以 `fmt.Sprintf("%+v", cfg)` 粗粒度扫描配置，不是字段/值索引，也没有结果路径行或 field key。

**最小修复建议：** 增加静态 page/field/value search entries、结果列表和 ⌘F；输入时保持右侧页不动，回车再跳转。

### IA-20 — 不过

**标准：** 搜索回车打开目标页、滚到字段、高亮 1.2 秒；减少动态效果时无动画。

**证据：** 当前没有搜索结果列表、回车跳转、字段 `settings-field:*` key、`ScrollIntoView` 或高亮计时实现。

**最小修复建议：** 在 IA-19 的 search result 上增加 target page/field anchor；跳转后按 reduced-motion 选择动画，并设置 1.2 秒 accent 背景。

### IA-21 — 不过

**标准：** API key 随机串写入后搜索该串结果为空。

**证据：** 当前 `settingsSectionMatches` 对整个 `cfg` 做 `fmt.Sprintf("%+v", cfg)`（`settings_view.go:334`），没有明确排除 secret value。该实现无法证明 API key 不会命中，且搜索本身不是规范结果模型。

**最小修复建议：** 搜索索引只加入 API key 字段名，不加入 key 值；对 API key 具体值加回归测试。

### IA-22 — 不过

**标准：** 搜索框 Esc 清空；空时失焦；永远不关设置窗口。

**证据：** 搜索输入本身存在，但全仓没有设置搜索 Esc 专用处理；Esc 现有处理仍主要是快捷键录制（`settings_view.go:827-828` 及相关路径）。没有空 query 失焦逻辑，也没有“永远不关窗”的测试。

**最小修复建议：** 在搜索控件/设置根处理 Esc：query 非空清空，query 为空仅移除焦点，不向窗口 OnClose 传播。

### IA-23 — 不过

**标准：** 片段、术语、工作流、Leader、输入法规则空态使用同图标/标题/说明/主按钮，点按钮进入新建。

**证据：** `settings_editors.go:697`、`:1637`、`:1813`、`settings_workflows.go:500` 已调用 `kitEmptyState`；`settings_view.go:52-59`/section metadata 提供对应图标。`go test ./uiapp/...` 通过。

**限制：** 当前部分 empty body 传空字符串（例如 `settings_editors.go:697`、`:1637`），严格标准要求一句说明；因此本条按**不过**更准确。

**最小修复建议：** 为每个空态补一条功能说明和唯一主按钮，并用帧测试验证 click 进入新建。

### IA-24 — 不过

**标准：** 权限页不出现 true/false；状态点 + 已授权/未授权；未授权提供系统设置按钮。

**证据：** `settings_view.go:770-771` 仍调用：

```go
TelemetryGauge3D(... strconv.FormatBool(diag.Permissions.Accessibility) ...)
TelemetryGauge3D(... strconv.FormatBool(diag.Permissions.InputMonitoring) ...)
```

且 `renderPermRow` 仍以布尔值渲染仪表；不符合状态行标准。

**最小修复建议：** 删除权限布尔仪表，改为 `kitStatusDot` + 本地化状态文本和打开系统设置按钮；保留其他连续诊断指标。

### IA-25 — 不过

**标准：** 中英文、宽 760 时页名不截断，说明换第二行。

**证据：** 页头确实使用两行 `ui.Column`（`settings_view.go:385-386`），但页名仍 `.SingleLine()`，说明也 `.SingleLine()`（`:385-386`）；窗口最小宽在 `main.go:509-511` 为 760。没有宽 760 的中英文帧断言，不能验收“没有截断”。

**最小修复建议：** 按规范在 760 宽加入页头帧测试；页名保留可见宽度，说明允许第二行/换行，不再对说明使用 `SingleLine`。

---

## 二、浮层验收

### SF-01 — 过

**标准：** 7 个窗口不使用 `CornerRadius` 数字字面量，统一由 `surfaceRadius` 求值。

**证据：** 当前窗口构造均经过 `uiapp.FloatingSurface(...)`（`main.go:411`、`:439`、`:461`、`:483`、`:506`、`:522`），圆角由 `SurfaceRadius.resolve`/`FloatingSurfaceSpecWithElevation`（`uiapp/kit_surface.go:37-63`、`:162-176`）计算。`rg 'CornerRadius:'` 未发现窗口数字字面量。

### SF-02 — 过

**标准：** 指示胶囊/选区工具条遮罩圆角、根节点圆角、高度一半一致。

**证据：** `main.go:460-461` 为 pill 高度 `pillH`，`:482-483` 为选择条高 `sbH=34`；`FloatingSurfaceSpecWithElevation`（`kit_surface.go:165-169`）对高度不超过 40 使用 `HalfHeightSurfaceRadius`；`SurfaceRadius.resolve`（`:50-55`）返回 `height/2`。已有 `TestRadiusScaleIsMonotonic` 和 `go test ./uiapp/...` 通过。

### SF-03 — 过

**标准：** FocusNever 显示路径不调用 Focus/Show/App.Show/App.Focus。

**证据：** `main.go:460-501` 的 pill/selection 是 FocusNever；显示路径在 `main.go:1420`、`:1906` 使用 `ShowInactive()`。`input_engine_ui.go:82` 也使用 `ShowInactive()`。`go test ./uiapp/...` 通过。

### SF-04 — 过

**标准：** FocusPanel 显示路径不调用 App.Show/App.Focus。

**证据：** 胶囊非 HUD 在 `main.go:1757-1758` 使用 `panel.Show()` + `panel.Focus()`；Prompt 显示路径同样只操作 panel。`App.Show()` 当前出现在设置窗口 `handleOpenSettings`（`:1969`），未出现在这两个 FocusPanel 路径。

### SF-05 — 不过

**标准：** IME 指示胶囊可见时唤出胶囊/Prompt，指示胶囊仍置顶且焦点在操作窗口。

**证据：** 虽有 `SurfaceElevationIndicator=elev4`（`kit_surface.go:87`）和 `AlwaysOnTop`，没有规范要求的统一 `raiseAbove`/重新置前函数；全仓没有 `raiseAbove`。因此无法证明同层/后显示时的置前顺序。

**最小修复建议：** 在一个 `raiseAbove(spec)` 中集中管理可见浮层的 `ShowInactive` 置前顺序，并补 SF-05 手测记录。

### SF-06 — 过

**标准：** 胶囊输入框组字时候选窗压在胶囊上面。

**证据：** 候选窗使用 `AlwaysOnTop`/Panel 和 `ShowInactive()`（`input_engine_ui.go:48-82`）；候选窗在输入引擎路径独立显示。当前没有 `raiseAbove` 代码契约，但现有窗口属性和显示时机满足已实现路径的层级行为。按代码证据判过。

### SF-07 — 过

**标准：** 设置/引导不设 Vibrancy；减少透明度时所有浮层不透明。

**证据：** `main.go:504-533` 的 settings/onboarding `surfaceSpec` 使用 `mygo.VibrancyNone` 且普通 WindowOptions；`FloatingSurface`（`kit_surface.go:97-108`）在 None 时清除 Transparent/Vibrancy。`GlassSurface`（`theme_glass.go`）按外观偏好处理 reduced transparency。设置/引导本身无玻璃。

### SF-08 — 不过

**标准：** OnBlur/OnClose 只在 bindSurface 注册；OnBlur 中 transition 判断只调用一次。

**证据：** 当前统一函数名是 `BindFloatingSurface`，且 `kit_surface.go:139-155` 集中注册 OnClose/OnBlur；但当前 `main.go` 只传 `CanDismiss`，每个 caller 自己判断 transition，例如 `main.go:428-430`，并非规范要求的 bindSurface OnBlur 单一判断函数。另有 `closeHides` 旧实现（`main.go:683-695`）仍存在。严格契约不满足。

**最小修复建议：** 将 transition 判断移入统一 bind 函数/单一 `isInPresentTransition` 调用，删除或不再保留旧 close 注册路径。

### SF-09 — 不过

**标准：** Prompt 组字时 Esc 只取消组字，不关窗；非组字 Esc 关窗。

**证据：** 规范所列 Prompt Esc 处理仍分散在 `prompt_composer.go` 多个聚焦元素路径；没有 `surfaceEscape(kind)` 单一实现，也没有当前 P3 手测证据能证明系统拼音组字时两阶段行为。`go test ./uiapp/...` 只覆盖 controller/UI 状态，不等于真实系统输入法手测。

**最小修复建议：** 增加统一 `surfaceEscape`，让三处 Prompt 输入路径调用同一函数；补真实系统拼音组字手测。

### SF-10 — 过

**标准：** 胶囊动作菜单开着时 Esc 只关菜单，再按一次后退/关闭。

**证据：** `uiapp/capsule_interaction_test.go:263-274` 明确断言 Esc 只关闭 action menu、不关闭 Capsule；`capsule_view.go:445-451` 先处理菜单，`capsule_preview.go:221-223` 处理预览层。测试通过。

### SF-11 — 不过

**标准：** Safari 输入框打开设置再关闭，Safari 回前台且光标回原输入框。

**证据：** 当前 `handleOpenSettings`（`main.go:1961-1974`）直接 `mygo.App.Show()`、`win.Show()`、`win.Focus()`；关闭路径只隐藏并走 `restorePreviousAppFocus`，其实现（`main.go:699-715`）只在没有 Clavis 窗口时调用 `mygo.App.Hide()`，没有记录/激活 Safari 或恢复具体输入框。规范要求的 `returnTo`/外部 bundle 记录不存在。

**最小修复建议：** 记录最近外部前台应用及输入锚点，关闭设置后按 bundle 激活；失败时仅在无浮层时 App.Hide；补 Safari 手测。

### SF-12 — 过

**标准：** 原前台应用退出后关闭设置，Clavis 不再活跃；可见浮层不能被藏掉。

**证据：** `restorePreviousAppFocus`（`main.go:699-715`）遍历 capsule/prompt/pill/selection/settings/onboarding；发现任何可见窗口就直接 return，否则 `mygo.App.Hide()`。这正是规范要求的“有浮层不调用 App.Hide”的代码契约；无目标 app 时通过隐藏 Clavis 兜底。

### SF-13 — 不过

**标准：** 设置和引导同时开，关闭其中一个焦点留在 Clavis；关闭最后一个才归还。

**证据：** `BindFloatingSurface` 给 settings 和 onboarding 都配置 `FocusPreviousApp`（`main.go:504-540`），每个 dismiss 都调用 `RestoreFocus`；`restorePreviousAppFocus` 会因另一个窗口 visible 而 return，但没有对“另一个 FocusPreviousApp 窗口”单独建模，也没有设置/引导同时打开的测试或焦点证据。由于当前 handler 同时会直接 `mygo.App.Show()`（settings），不能证明焦点契约完整通过。

**最小修复建议：** 将 FocusPreviousApp 窗口集合显式分组并加并行打开/关闭测试；只在最后一个文档窗口关闭时恢复外部 app。

### SF-14 — 不过

**标准：** AI 确认前切换到别的应用：不注入，复制到剪贴板并 HUD 提示。

**证据：** 当前 `beforeInject` 有隐藏/延迟与目标上下文保护，但未见按 session 开始时和注入前比较 `darwinFrontmostAppIdentity()` 的实现；全仓没有该验收所需的“结果已复制”分支契约。规范自身也写明现有注入路径没有这种比较（Q-08）。

**最小修复建议：** 为 AI 注入保存会话起始 app identity；注入前不匹配则跳过注入、写剪贴板、发 HUD，并补手测。

### SF-15 — 不过

**标准：** 设置改圆角后，字段旁出现“窗口边缘重启后生效”。

**证据：** `settings_view.go:534-536` 只更新 `appearance.CornerRadius`；没有“重启后生效”字段级提示或 `SurfaceRadius` 变更提示。

**最小修复建议：** 对 `appearance.corner_radius` 保存成功后的字段行增加规范要求文案；仅提示，不改变现有实现。

---

## 三、重点三类结论

### 视觉

- **elevation 层级：不过。** `SurfaceSpec.Elevation` 和 `DisableShadow` 已存在（`uiapp/kit_surface.go:71-112`），但没有统一置前 `raiseAbove`，无法完成 P1 的跨窗口层级验收；已有窗口内 `elevationShadow` 测试不等于窗口层级验收。
- **玻璃只用于浮层：过。** 设置/引导使用 `VibrancyNone`（`main.go:504-533`）；浮层使用 `VibrancyGlass`。
- **强调色只有 `#0A84FF`：不过。** 代码测试明确存在其他强调/选择色，例如 `uiapp/candidate_style_test.go:337` 的 `#FFD60A`、`#30D158`，且设置 AI active profile 直接使用 `theme.Accent`（`settings_view.go:608-624`）。按规范“单一系统蓝”不能判过。

### 行为

- **ESC/点外/显式关闭统一：不过。** 部分窗口已通过 `BindFloatingSurface`，但 Prompt/胶囊内容级 Esc 仍分散；设置 Esc 仍只处理录制；没有统一 `surfaceEscape`。
- **关闭后焦点回原应用：不过。** 设置/引导只用 `App.Hide` 兜底，没有按 bundle 记录/激活；当前代码无法证明 Safari 输入框恢复。

### 兼容

- **旧 OpenSettings 调用点零改动：不过。** `git diff 97261f4 -- main.go uiapp/onboarding_view.go` 非空，且当前仍是旧 index 状态。虽然调用形式仍存在，但 P1 的“源码零改动 + 通过 page mapping”契约没有完整实现。
- **既有 settings 测试全绿：过。** 命令：

```bash
go test ./uiapp/...
```

关键输出：

```text
ok   github.com/yeisme/clavis/uiapp 16.477s
```

---

## 四、最小修复批次建议

1. **P3-A：导航/搜索契约**：`settingsPages` 唯一数据源、page-key 状态、侧栏独立滚动、⌘1…⌘5、真正结果索引、字段锚点；先补 IA-01–08、IA-19–22、IA-25。
2. **P3-B：设置状态/草稿**：按页 notice/failure、saved 行反馈、failure retry/red dot、SetDocumentEdited、统一删除确认；补 IA-09–18、IA-23–24。
3. **P3-C：浮层契约**：统一 `bindSurface`/`surfaceEscape`、raiseAbove、FocusPreviousApp 外部 bundle 记录、AI 注入前 identity；补 SF-05、SF-08、SF-09、SF-11、SF-13、SF-14、SF-15。
4. 每批只改实现，不把本 review 中的“不足”标成过；每项完成后逐条运行规范指定的帧断言、代码契约和真实手测。
