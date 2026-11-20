# Clavis macOS 交互规格：设置页信息架构 + 浮层统一契约

> 2026-11-20 · T-clavis-ux-p1 · 只是设计，本任务没有改任何 `.go` 文件。
> 行号基线：`97261f4`（当时 main 的 HEAD）。实现前先 `git diff 97261f4 -- <文件>`，确认行号没有漂移。
>
> 审美约束（owner，2026-11-18，不得违反）：
> - Apple 灰阶分层、1px 发丝线、单一系统蓝 `#0A84FF`、Xcode 语法色；
> - 不用 emoji 渐变、3D 霓虹、花哨阴影；
> - 玻璃只用于浮层，正文和代码块不透明。
>
> 保留不动的两条：
> - `settingsEditorState` 草稿模式（`uiapp/settings_editors.go:20-63`）；
> - 删除走行内确认，不弹模态层。
>
> 不新增 token：本文所有数值都来自 `uiapp/tokens.go` 和 `docs/design/visual-language.md`，新增的只有类型和函数。
> 文案只给中文，英文在实现时按同一含义补到 i18n。

## 0. 与任务描述不一致的地方

动笔前先核实了任务里给的事实，有三处和代码对不上。

| # | 任务描述 | 实测 | 证据 |
|---|---|---|---|
| 0-1 | leader / snippets / glossary / workflows 是 4 个「假 tab」 | 在侧栏点它们，会渲染各自的页面，并不是假的。真正的问题是这 4 页**离开侧栏就没有身份**：`SettingsTab` 里没有它们，子命令打不开，元数据里填的 tab 是错的 | 渲染分派：`uiapp/settings_view.go:218-227`；点击：`:157-161` → `setSettingsSection`（`:93-97`）；具体问题见 P-01 ~ P-03 |
| 0-2 | 写盘失败只打日志 | 失败文案会写进 `settingsStatus`，显示在页头的状态药丸里。但药丸固定用 accent 色，和「已保存」外观一样，下一条任何状态都会把它覆盖 | 写入：`uiapp/controller.go:3707`、`:3861`、`:3879`、`:3900`、`:3922`、`uiapp/settings_workflows.go:249`；经 `setSettingsStatus`（`controller.go:3686-3694`）；渲染：`kitStatusPill(c, theme, statusMsg, chipAccent)`（`settings_view.go:199-201`） |
| 0-3 | 6 个窗口 | 7 个，另有输入法候选窗 | `input_engine_ui.go:49-62` |

---

## 第一部分　设置页信息架构

### 1.1 问题清单

#### P-01　同一个字段存两种编号

- 现状：
  - `uiState.settingsTabIdx int`（`uiapp/controller.go:157`）有两个写入方：
    - `OpenSettings` 写入 `int(tab)`，取值 0–4（`controller.go:3177`）；
    - 侧栏点击写入 section 下标，取值 0–8（`settings_view.go:95`）。
  - 两者能对上，全靠一条约定：前 5 个 section 下标恰好等于 `SettingsTab` 的值（`settings_view.go:17-25`）。
- 为什么是问题：只要调整 `SettingsTab` 常量的顺序，或在前 5 页里插入一页，`OpenSettings(SettingsTabAI)` 就会打开别的页面。编译器和现有测试都发现不了。

#### P-02　4 个页面从外部打不开

- 现状：
  - `SettingsTab` 只有 5 个值（`controller.go:39-47`）。
  - 子命令 `settings <x>` 只认这几个：
    - `ai`；
    - `ime` / `keymap` / `ime-keymap`；
    - `permissions`；
    - `configs`。
  - 其余一律落到 General（`controller.go:3161-3171`）。
- 为什么是问题：托盘、引导页、命令行、HUD 报错都无法直达 Leader、片段、术语表和工作流。例如「leader.toml 校验失败」这类提示，没法把人带到 Leader 页。

#### P-03　死字段和错数据

- 现状：
  - `settingsSectionMeta.Settings`（`settings_view.go:39`）全仓没有任何读取。4 个扩展页的这个字段都填成了 `SettingsTabGeneral`（`:47-50`）。
  - `SettingsTabNames()`（`controller.go:49-50`）只被 `uiapp/ui_test.go:754` 引用，列的是 5 个旧名字。
- 为什么是问题：读代码的人会以为 Leader 页属于 General；以后谁照着这个字段修导航，会把 4 页导回 General。

#### P-04　侧栏 9 项平铺，归类靠猜

- 现状：
  - 顺序在 `settings_view.go:43-55` 定义，`:140-162` 一列渲染，没有组标题。
  - 「通用」页（`renderSettingsGeneralSection`，`settings_view.go:240`）只有两张卡，而且性质不同：
    - 「触发」`[trigger]`（`:246`）：键盘入口；
    - 「外观」`[appearance]`（`:288`）：视觉。
  - 剪贴板的设置被拆在两页，而且都不在页名能猜到的地方：
    - 剪贴板卡 `[clipboard]` 挂在术语表页末尾（`settings_editors.go:1858`），和术语表无关；
    - 剪贴板快捷键在「通用 › 触发」里（`settings_view.go:273`）。
- 为什么是问题：用户只能凭页名猜设置在哪一页。9 项平铺时，也看不出哪几页是同一类。

#### P-05　页名和说明挤在一行

- 现状：页头写的是 `meta.Label+" — "+meta.Hint`（`settings_view.go:197`），用 `typeTitle`、`SingleLine()`，右边还挤着状态药丸（`:199-201`）。
- 为什么是问题：
  - 窗口最小宽 760（`main.go:426`），减去侧栏 `railWidth 216` 和左右内边距后，说明文字要和药丸抢同一行。
  - `SingleLine` 放不下就只能截断，而说明是这一页唯一的文字介绍。

#### P-06　状态提示是全局的，也不分成功和失败

- 现状：
  - 整个设置窗口只有一个 `settingsStatus string`（`controller.go:322`）。
  - 直接赋值有 15 处：
    - `controller.go`：`:1178`、`:1594`、`:3614`、`:3640`、`:3688`、`:3719`、`:3807`、`:3844`、`:3867`、`:3888`、`:3910`、`:3928`、`:3997`、`:4001`；
    - `settings_workflows.go:257`。
  - 另有 10 处经 `setSettingsStatus` 写失败文案。
  - 渲染时一律用 `chipAccent`（`settings_view.go:200`）。
  - 切页时不会清空：`setSettingsSection`（`settings_view.go:93-97`）只改页下标。
- 为什么是问题：
  - 失败和成功同色。
  - 提示会串页：在片段页保存后切到权限页，页头还挂着「已保存 N 条片段」。
  - 失败提示会被下一条任意状态覆盖，用户可能还没看到就没了。

#### P-07　即时生效的控件写盘失败后会回弹，原因却显示在别处

- 现状：`UpdateMainConfig`（`controller.go:3698-3722`）先改副本（`:3702-3704`）；`SaveMain` 失败就直接返回（`:3706-3709`）。内存里的配置没变，控件下一帧按旧值重绘。
- 为什么是问题：
  - 用户看到刚改的值自己跳了回去，唯一的解释是页头那颗蓝色药丸（P-06）。
  - 没有重试入口，也没有打开文件位置的入口。

#### P-08　草稿没有「未保存」的概念

- 现状：
  - 各编辑器的草稿字段：
    - Leader：`settings_editors.go:33`；
    - 片段：`:45`；
    - 术语：`:50`；
    - 工作流：`:52-60`，含 `workflowOriginalID`（`:55`）。
  - 设置相关代码里没有任何 dirty 标记。
  - 在列表里点另一条，会直接覆盖草稿：
    - 片段：`:1623-1624`；
    - 术语：`:1821`；
    - 工作流：`selectWorkflowDraft`（`settings_workflows.go:230-243`）。
- 为什么是问题：改到一半去点列表里的另一条，改动就没了，界面上没有任何提示。

#### P-09　输入法规则的草稿放在另一处

- 现状：
  - `imeRule*Draft` 在 `uiState` 里（`controller.go:158-162`），受 `c.mu` 保护。
  - 其他编辑器的草稿在 `settingsEditorState` 里，受 `se.mu` 保护（`settings_editors.go:24-25`）。
- 为什么是问题：
  - 未保存检测和离开拦截都得写两套。
  - 两把锁也增加了嵌套加锁的机会。

#### P-10　删除行为不一致

- 现状：有 8 个删除动作一点就写盘，没有确认：

  | 位置 | 删除对象 |
  |---|---|
  | `settings_view.go:513` | 清除 API key |
  | `settings_editors.go:408` | 键位映射行 |
  | `settings_editors.go:729` | 输入法规则行 |
  | `settings_editors.go:804` | AI 配置 |
  | `settings_editors.go:1448` | Leader 节点（`deleteLeaderSelection` `:1542`，写盘 `:1550`） |
  | `settings_editors.go:1704` | 片段（`deleteSnippetDraft` `:1753`，写盘 `:1763`） |
  | `settings_editors.go:1852` | 术语（`deleteGlossaryDraft` `:1902`，写盘 `:1912`） |
  | `settings_editors.go:1978` | 剪贴板排除项 |

- 只有工作流有行内确认（`settings_workflows.go:342-353`、`:608-623`）。但确认文案用的是 `kitErrorBanner`（`:614`），红底加叉号，和写盘失败长得一样。
- 为什么是问题：
  - 风险相同的操作，有的要确认，有的不用。
  - 确认提示长得像报错，用户会以为已经出错了。

#### P-11　权限页用环形仪表显示布尔值，上面印着 true / false

- 现状：
  - `settings_view.go:556-557`、`:580` 把 `strconv.FormatBool(...)` 当读数传给 `TelemetryGauge3D`（`uiapp/spatial3d.go:155-184`）。
  - 画出来是 104×104 的 270° 圆弧，里面用 16pt accent 色写着 `true` 或 `false`。
  - `enable_3d_effects` 默认开（`pkg/core/config/main_config.go:160`），开着时仪表会倾斜（`spatial3d.go:159-161`）。
- 为什么是问题：
  - 仪表表示的是连续量，权限只有两种状态。
  - `true` / `false` 没有本地化。
  - 已授权和未授权同色，只能靠圆弧有没有来分辨。
  - 倾斜出现在文档窗口里，和「不要 3D」可能冲突（见 Q-07）。

#### P-12　没有搜索

- 现状：`SettingsView`（`settings_view.go:100-131`）里没有搜索框；共 9 页、二十多张卡。
- 为什么是问题：知道设置项叫什么、但不知道在哪页的用户，只能一页一页翻。P-04 里的剪贴板就是例子。

#### P-13　关掉设置窗口后，焦点不回原来的应用

- 现状：
  - 打开时调用 `mygo.App.Show()`、`win.Show()`、`win.Focus()`（`main.go:1790-1792`），Clavis 被激活。
  - 关闭时 `closeHides(settingsWin.Hide)`（`main.go:432`）只隐藏窗口，没有任何地方把焦点还给之前的应用。
  - 引导窗口也一样：`onboarding.go:115-117` 打开，`:135-136` 只隐藏。
- 为什么是问题：关窗后 Clavis 仍是活跃应用，却没有可见窗口，键盘输入没有去处，用户得自己点回原来的应用或按 ⌘Tab。
  - 这是按代码推断的结果，由 [SF-11] 手测确认。

#### P-14　设置页里 Esc 只能取消快捷键录制

- 现状：Esc 只在 `settings_view.go:827-828`、`:835` 处理，用来取消快捷键录制。工作流的删除确认不能用 Esc 退出。
- 为什么是问题：纯键盘操作时，进了删除确认就只能去点「取消」。

### 1.2 目标 IA：5 组 × 12 个二级页

#### 侧栏结构（从上到下）

1. 搜索框
   - 位置：替换现在的「设置中心」标题文字（`settings_view.go:139`）。窗口标题已经写了「设置」，这行文字是重复的。
   - 尺寸：高 `controlHeight`。
   - Key：`settings-search`。
2. 5 个组，按顺序排：
   - 组标题：`typeMeta`、600 字重、`theme.TextMuted`，不可点击，Key 为 `settings-rail-group:<group>`。
   - 页行：沿用现在的行组件（`settings_view.go:143-155`），Key 不变，仍是 `settings-rail:<page>`。
3. 侧栏内容放进 `ui.Scroll`，搜索框不在滚动区里。
   - 估算：12 行 × 28 + 5 个组标题 + 间距 ≈ 570。
   - 默认高 640 时放得下；最小高 500（`main.go:427`）时侧栏滚动。

#### 页面归属

| 组 | 二级页（key） | 内容 | 来自 | 归组理由 |
|---|---|---|---|---|
| 输入 | 快捷键 `triggers`（新 key） | `[trigger]` 卡：唤起快捷键、剪贴板快捷键等 | 原「通用」页（`settings_view.go:246`） | 回答的都是「按什么键让 Clavis 出来」，属于键盘输入 |
| 输入 | 输入法与键位 `ime-keymap` | `[keymap]`、`[ime]`、`[input_engine]`、`ime-rules.toml` | 原页不变（`settings_editors.go:668-679`） | 都在改按键或输入法本身的行为 |
| 输入 | Leader 菜单 `leader` | Leader 节点编辑器 | 原页（`settings_editors.go:1182`） | Leader 是一组按键序列，靠键盘触发、靠键盘导航 |
| 效率 | 片段 `snippets` | 片段编辑器 | 原页（`settings_editors.go:1570`） | 存一段文字，之后少打字 |
| 效率 | 术语表 `glossary` | 术语编辑器 | 原页（`settings_editors.go:1778`），剪贴板卡移走 | 存词条和别名，之后复用 |
| 效率 | 剪贴板 `clipboard`（新页） | `[clipboard]` 卡 | 术语表页末尾（`settings_editors.go:1858`） | 剪贴板历史是复用已复制的内容，和片段、术语是一类；单独成页后，页名就是功能名 |
| 效率 | 工作流 `workflows` | 工作流编辑器、步骤 | 原页（`settings_workflows.go:475`） | 把多步操作串成一个动作 |
| 智能 | AI `ai` | AI 配置列表、钥匙串与 API key、新增配置 | 原页（`settings_view.go:365`） | 所有调用模型的设置集中在一处，API key 只出现在这一页 |
| 系统 | 外观 `appearance`（新 key） | `[appearance]` 卡 | 原「通用」页（`settings_view.go:288`） | 作用于所有窗口的视觉，不属于某个功能 |
| 系统 | 权限 `permissions` | 辅助功能、输入监控 | 原页的权限卡（`settings_view.go:553`） | 系统授权，要去 macOS 系统设置里改 |
| 系统 | 配置文件 `configs` | 配置目录、TOML 文件列表、校验结果 | 原页（`settings_view.go:646`、`:656`），校验卡从权限页移来（`:625`） | 都是磁盘上的文件和它们的状态 |
| 关于 | 关于 Clavis `about`（新页） | 版本号（`mygo.App.Version()`，值来自 `mygo.json:5`）、诊断卡 | 诊断卡从权限页移来（`settings_view.go:579`） | 只读的运行信息，报问题时要用 |

补充说明：
- 计数徽标沿用 `settingsSectionBadge`（`settings_view.go:167`），新页不加徽标。
- `general` 不再是页面 key，只作为别名映射到 `triggers`（见 1.3）。

### 1.3 导航状态：从「tab 下标」改成「页面 key」

#### 目标结构

只是示意，本任务不写代码：

```go
// 目标：uiapp/settings_nav.go（新文件）
type settingsPageMeta struct {
	Key   string // 稳定 key，侧栏、深链、搜索锚点、测试共用
	Group string // "input" | "productivity" | "intelligence" | "system" | "about"
	Label, Icon, Hint string
}

func settingsPages() []settingsPageMeta        // 唯一的顺序表
func settingsPageForTab(tab SettingsTab) string // 旧入口 → 页面 key
func (c *AppController) OpenSettingsPage(key string) bool // 新入口；未知 key 返回 false，不改状态
```

#### 状态与代码变更

- `uiState.settingsTabIdx int` 改为 `uiState.settingsPage string`，成为唯一的导航状态。
- 删除：
  - `settingsSection*` 整数常量（`settings_view.go:20-30`）；
  - `settingsSectionMeta.Settings`（`:39`）；
  - `SettingsTabNames()`（`controller.go:49-50`），它的测试（`ui_test.go:754-755`）改为测 `settingsPages()`。
- `SettingsTab` 类型和 5 个常量**保留**，它们是导出 API。`OpenSettings(tab)` 内部只改一行：把 `settingsTabIdx = int(tab)` 换成 `settingsPage = settingsPageForTab(tab)`。
- `handleOpenSettings`（`main.go:1782`）照旧忽略 tab 参数，因为状态已经由 controller 设好了。

#### 现有调用点：源码零改动，打开的页如下

| 调用点 | 传入 | 现在打开 | 目标打开 |
|---|---|---|---|
| `main.go:601`（托盘权限项） | `SettingsTabPermissions` | 权限 | 系统 › 权限 |
| `main.go:657`（托盘「设置…」） | `SettingsTabGeneral` | 通用 | 本进程内上次停留的页；第一次打开时为 输入 › 快捷键 |
| `onboarding_view.go:483`（引导页「设置」按钮） | `SettingsTabGeneral` | 通用 | 同上（首次运行时就是 输入 › 快捷键） |
| `controller.go:3162`（`settings ai`） | `SettingsTabAI` | AI | 智能 › AI |
| `controller.go:3164`（`settings ime` / `keymap` / `ime-keymap`） | `SettingsTabIMEKeymap` | 输入法与键位 | 输入 › 输入法与键位 |
| `controller.go:3166`（`settings permissions`） | `SettingsTabPermissions` | 权限 | 系统 › 权限 |
| `controller.go:3168`（`settings configs`） | `SettingsTabConfigs` | 配置文件 | 系统 › 配置文件 |
| `controller.go:3170`（default） | `SettingsTabGeneral` | 通用 | 同托盘「设置…」 |

#### 子命令

- 在现有 `case` 之前加一步：参数如果是 12 个页面 key 之一，就调用 `OpenSettingsPage(key)`。
- 已有的 case 一行都不改。
- `general` 作为别名映射到 `triggers`。

#### 每页独立的状态

| 状态 | 现在 | 目标 |
|---|---|---|
| 身份 | int 下标，和 `SettingsTab` 共用编号 | 字符串 key，只有一张表 |
| 普通提示 | 全局 `settingsStatus`（`controller.go:322`），会串页 | 按页存放的 `settingsNotice{Text, Tone}`。只在所属页显示，离开该页即清除。现有写入点按下表归页 |
| 写盘失败 | 混在提示里 | 按页存放的 `settingsFailure{File, Field, Err, Retry}`。显示为横幅，直到解决或手动关闭（见 1.6） |
| 草稿 | `settingsEditorState`；输入法规则的草稿在 `uiState`（P-09） | 全部放进 `settingsEditorState`，每页一个 `dirty(page) bool`，判定规则见 1.4「离开有未保存 draft」 |
| 滚动 | 共用一个没有 Key 的 `ui.Scroll`（`settings_view.go:125`） | `Key("settings-scroll:"+page)`。切页那一帧对页内第一个元素调用 `ScrollIntoView(ScrollStart)`，保证从顶部开始；搜索跳转时改为定位到字段（1.5） |

现有提示写入点归页：

| 写入点 | 归到 |
|---|---|
| `controller.go:1178`、`:3807`、`:3844`、`:3928` | `ime-keymap` |
| `:1594`、`:3614`、`:3640` | `ai` |
| `:3867` | `leader` |
| `:3888` | `snippets` |
| `:3910` | `glossary` |
| `settings_workflows.go:257` | `workflows` |
| `:3997`、`:4001` | `configs` |
| `:3719`（`UpdateMainConfig`） | 由调用方传入页 key；不传时归当前页 |

#### 窗口级的未保存标记

只要任一页 `dirty`，就调用 `settingsWin.SetDocumentEdited(true)`（MyGo `window.go:1126`）。关闭按钮中间会出现系统自带的圆点。这里用的是系统现成的样式，不需要新增视觉元素。

### 1.4 交互矩阵（现状 → 目标）

目标一列只写看得见的行为。

#### 进入

- 现状：
  - 托盘「设置…」调用 `OpenSettings(General)`（`main.go:657`），然后 `App.Show` + `Show` + `Focus`（`main.go:1790-1792`）。
  - `StateKey: "settings"`（`main.go:429`）会恢复窗口位置。
  - 命令行只能打开 5 个页。
  - 页头药丸可能还挂着之前某次操作留下的提示（P-06）。
- 目标：
  - 打开哪一页按 1.3 的映射表；`settings <key>` 能打开全部 12 页。
  - 打开时记下当时的前台应用，用于关窗后归还焦点（2.5）。
  - 页头不显示旧提示；不抢任何文本框的焦点。
  - ⌘F 聚焦搜索框。

#### 切换组

- 现状：没有「组」这一层；切页只能用鼠标点侧栏（`settings_view.go:157-161`）。
- 目标：
  - ⌘1 … ⌘5 跳到对应组的第一页。如果当前页已在该组，就留在原页。
  - 组标题点了没有反应。
  - 其余表现和「切换页」相同。

#### 切换页

- 现状：
  - 只换下标（`setSettingsSection`，`settings_view.go:93-97`）。
  - 页头提示照旧挂着（P-06）。
  - 草稿留在内存里，但界面上看不出来。
  - 滚动容器是共用的（`settings_view.go:125`）。
- 目标：
  - 侧栏选中行和页头一起换。
  - 页头只显示新页自己的提示，没有就不显示药丸。
  - 内容从顶部开始。
  - 离开的那页如果有未保存草稿：草稿保留，侧栏行尾出现 accent 色小圆点（`kitStatusDot`，`kit_states.go:29`），回来时草稿原样还在。

#### 编辑字段

现在有两种保存方式，目标分开写。

即时生效的控件（开关、选择器、快捷键录制）：
- 现状：
  - 调用 `UpdateMainConfig`，立即写盘。
  - 成功时页头药丸显示「已保存」（`controller.go:3719`），离字段很远。
  - 失败时控件回弹，原因在药丸里（P-07）。
- 目标：
  - 成功：字段行末尾显示 `checkmark` 图标 +「已保存」（`typeMeta`、`TextMuted`），1.5 s 后消失；开了「减少动态效果」时直接消失、不淡出。页头不出药丸。
  - 失败：控件回到旧值，同时出现写盘失败横幅（见下面「写盘失败」）。

带「保存」按钮的编辑器（Leader、片段、术语、工作流、输入法规则）：
- 现状：没有未保存标记；保存按钮一直可点。
- 目标：
  - 草稿一有改动，编辑器标题和侧栏行尾就出现 accent 色小圆点。
  - 保存按钮只在有改动时可用。
  - 保存成功后，按钮旁显示「已保存」1.5 s，圆点消失。

#### 删除

- 现状：8 处点了就写盘，只有工作流有确认，而且确认长得像报错（P-10）。
- 目标：分两类处理。
  - 实体删除：Leader 节点、片段、术语、工作流、AI 配置、API key。
    - 删除按钮那一行原地换成确认行：「删除『<名称>』？会立即写入 <文件名>」+「删除」「取消」两个按钮。
    - Leader 节点有子节点时，文案加上「及其 N 个子节点」。
    - 确认行用 `kitInsetWell` 中性底，配 `exclamationmark.triangle` 图标，**不用** `kitErrorBanner`。
    - 出现确认行时，键盘焦点在「取消」上；按 Esc 等于取消。
  - 行内值删除：键位映射、输入法规则、剪贴板排除项。
    - 保持现在的立即删除，不加确认。
  - 两类都不弹模态层。

#### 搜索

- 现状：没有搜索（P-12）。
- 目标：见 1.5。

#### 离开有未保存 draft

dirty 的判定：
- 草稿已加载，并且和已存的那条（按 `originalID` 找）深比较不相等；
- 新建的条目，只要任一字段不是默认值，就算 dirty。

各场景的现状和目标：

| 场景 | 现状 | 目标 |
|---|---|---|
| 同一编辑器里点另一条 | 直接覆盖草稿（P-08） | 不切换。编辑器顶部出现行内横幅：「『<名称>』有未保存的修改」+「保存」「放弃」「继续编辑」。保存：写盘后再切到新条目；放弃：丢掉改动后切换；继续编辑：留在原条目。按 Esc 等于继续编辑 |
| 切页 | 草稿留在内存，看不出来 | 不拦。草稿保留，侧栏有圆点 |
| 关闭窗口 | 窗口只是隐藏（`main.go:432`），草稿还在，但看不出来 | 不拦。草稿保留；关闭按钮有系统未保存圆点（`SetDocumentEdited`）；再打开时侧栏圆点还在 |
| 退出应用 | 草稿丢失 | 维持现状，见 Q-05 |

#### 写盘失败

- 现状：
  - 页头药丸用 accent 色显示「保存失败：…」，和成功同色。
  - 下一条提示会把它覆盖。
  - 没有重试入口。
- 目标：
  - 页头下方、滚动区之外出现错误横幅，形式见 1.6。
  - 侧栏里该页行尾显示 `theme.Danger` 色状态点，切到别的页也看得到。
  - 横幅和红点在三种情况下消失：重试成功、手动关闭、之后对同一文件的写入成功。

### 1.5 搜索

**结论：做。** 理由：
- 页面增加到 12 个以后，侧栏扫一眼看不全。
- P-04 那种跨页分散的设置，只靠页名是找不到的。

#### 范围

| 类 | 索引什么 | 显示路径 |
|---|---|---|
| 页 | 组名、页名、页说明（`Hint`） | 组 › 页 |
| 字段 | 卡片标题、字段标签、字段说明（`settingsHint` 的文案）、TOML 键名（如 `capsule_shortcut`）。卡片头本来就显示 `[trigger]` 这类来源，熟悉配置文件的人会按键名找 | 组 › 页 › 卡 › 字段 |
| 值 | 列表型配置里**已保存**的条目（取自 `ConfigStore.Current()`，不含草稿）：Leader 节点的键和标签、片段名、术语和别名、工作流名称和 ID、AI 配置名、输入法规则的目标应用、剪贴板排除项 | 组 › 页 › 条目名 |

不进索引的内容：
- API key 和任何密钥的**值**。搜「API key」只会命中字段名。
- 片段正文：可能很长、多行，命中结果会很杂。

#### 匹配规则

- 不区分大小写；全角和半角统一折叠。
- 中文按子串匹配。
- 用空格分开的多个词必须全部命中。
- 拼音首字母见 Q-03。

#### 索引从哪来

- 页和字段：一张静态描述表 `settingsSearchEntries()`，用渲染时同一批 i18n key，切换界面语言时自动跟着变。
- 值：每次查询时从当前配置现取。条目只有几百个，直接遍历就够了。
- 跳转锚点：每个字段行带稳定 Key `settings-field:<page>:<field>`。
  - 配置字段的 `<field>` 用 TOML 路径，例如 `trigger.capsule_shortcut`。
  - 列表值的 `<field>` 用条目 ID，例如 `item/<id>`。

#### 交互

1. 打开搜索：⌘F，或者直接点搜索框。占位文字为「搜索设置」。
2. 显示结果：搜索框里有字时，侧栏的分组列表换成结果列表。
   - 每条结果分两行：
     - 第一行：命中的名称，命中的子串用现有的 `matchMark` 颜色标出；
     - 第二行：路径，`typeMeta`、`TextMuted`。
   - 最多显示 50 条；超出时末尾写「还有 N 条，继续输入可缩小范围」。
   - 默认选中第一条，↑↓ 移动选中。
   - 打字过程中右侧内容区不动。
3. 跳转：回车或单击结果。
   - 先切到目标页，走「切换页」的规则。
   - 如果命中的是列表值，再选中那一条。当前编辑器有未保存草稿时，先出 1.4 的拦截横幅。
   - 下一帧对字段行调用 `ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollCenter, Animation: &ui.Smooth})`（MyGo `ui/scroll.go:80-94`）。开了「减少动态效果」时不带 `Animation`。
   - 字段行底色变成 `theme.Accent.Alpha(alphaSubtle)`，保持 1.2 s 后去掉。
   - 字段的控件能接受焦点时，把焦点给它。
4. 跳转后：搜索词和结果列表保留，方便接着看下一条。
5. Esc：
   - 搜索框里有字时，清空，侧栏恢复分组列表；
   - 已经空了，搜索框失去焦点；
   - 永远不关窗口。
6. 无结果：侧栏显示「没有匹配『<词>』的设置」（`typeBody`、`TextMuted`），下面一行写「可以搜页面名、设置项名称或已保存的条目」（`typeMeta`）。

### 1.6 空态与错误态

#### 空态的通用规则

- 一律用 `kitEmptyState(c, t, icon, title, body)`（`kit_states.go:9-17`）。
- **不用插画**。图标就用该页在侧栏里的图标（`settingsPageMeta.Icon`），`IconSize24`、`TextMuted`。不加颜色、渐变或 emoji。
- 标题说缺了什么，说明用一句话讲这个功能是干什么的。
- 下方只放一个主按钮（`ui.PrimaryButton`）。这个状态下，它是页面上唯一的 accent 色元素，点了直接进入新建。
- 只有当页面的主体是列表、并且列表为空时，才算整页空态。页内的次级列表为空时，只写一行 `typeMeta` 灰字，不放图标。

#### 各页的空态

| 页 | 什么时候算空 | 标题 | 说明 | 主操作 |
|---|---|---|---|---|
| 快捷键 | 不会空，有默认快捷键 | — | — | — |
| 输入法与键位 | 右栏输入法规则为 0 条（只是右栏空） | 还没有按应用设置的输入法 | 为某个应用指定输入法，切到它时自动切换 | 添加规则：焦点进表单的第一个输入框 |
| Leader 菜单 | 根节点下没有子节点 | Leader 菜单是空的 | 按下 Leader 键后，再按一个键执行对应的动作 | 添加第一个节点 |
| 片段 | 0 条 | 还没有片段 | 保存常用的一段文字，之后直接插入 | 新建片段 |
| 术语表 | 0 条 | 还没有术语 | 记下专有名词和它的别名 | 新建术语 |
| 剪贴板 | 整页是设置，不会空。排除列表为空时写一行灰字「没有排除的应用」 | — | — | — |
| 工作流 | 0 个 | 还没有工作流 | 把几个步骤串成一个动作 | 新建工作流 |
| AI | 不会空：最后一个配置不能删（`settings_editors.go:804` 的 `Disabled`）。当前配置没有 API key 时，在 key 卡里写「这个配置还没有 API key」，并给「填写 API key」按钮（焦点进输入框） | — | — | — |
| 外观、权限、配置文件、关于 | 不会空。权限「未授权」是一种状态，不是空态（见下文） | — | — | — |
| 搜索 | 无结果 | 见 1.5 | — | — |

#### 错误态

写盘失败用**页内横幅**，不用 toast。

横幅的形式：扩展现有的 `kitErrorBanner`（`kit_states.go:19-27`），颜色和图标不变（`t.Danger` + `alphaSubtle` 底，`xmark` 图标）。改动有三处：
- 文字可以换两行，不再用 `SingleLine`（`:25`）：
  - 第一行：「未能保存『<字段或条目名>』：<原因>」，原因里的绝对路径换成文件名；
  - 第二行：完整路径，`typeMeta`。
- 右侧放两个按钮：
  - 「重试」：用同一个值再跑一次同一个保存函数；
  - 「在 Finder 中显示」：定位到那个文件。
- 最右边一个关闭按钮 ×。

横幅的位置和个数：
- 放在页头下方、`ui.Scroll` 之外，页面怎么滚都看得见。
- 同一文件的新失败替换旧的。
- 不同文件最多叠两条，再多的写「还有 N 个文件写入失败」。

不用 toast 的理由：
- 这类失败要给两个动作，还要一直留到用户处理完；toast 会按时自动消失，这正是 P-06 的问题。
- Clavis 唯一的临时提示载体是胶囊的 HUD。它是浮层（`main.go:1559-1561`），会浮到别的应用上面，不该拿来报设置窗口的错。
- 模态弹窗是 owner 禁止的。

其他错误和状态：

| 情况 | 形式 |
|---|---|
| 保存前的校验错误（编辑器内） | 维持现状：编辑器自己的 `kitErrorBanner`，例如 `workflowError`（`settings_workflows.go:604-606`）。它和写盘失败横幅的区别是位置：前者在编辑器内部，后者在页头下方 |
| 配置文件解析失败（启动或重载时） | 「系统 › 配置文件」页出横幅，并显示校验卡（从权限页移来，`settings_view.go:625`）；该页侧栏行有红点。重载失败的 HUD（`controller.go:3155`）照旧 |
| 权限未授权 | 不算错误。每项一行：`kitStatusDot` + 「已授权」或「未授权」；未授权时行尾放「打开系统设置」按钮。不再用仪表（P-11） |

---

## 第二部分　浮层统一契约

### 2.1 现状对照表（7 个窗口）

| 窗口 | 构造 | 类型 / 材质 | 圆角来源 | 尺寸 | 显示方式 | Esc | 失焦 | 关闭（OnClose） | 焦点归还 |
|---|---|---|---|---|---|---|---|---|---|
| IME 指示胶囊 | `main.go:381-399` | Panel / `VibrancyGlass`（`:390`） | 写死 `15`（`:391`），等于高 30（`pillShortHeight`，`layout.go:27`）的一半；内容根用 `radiusPill`（`pill_view.go:79`） | 创建时 `PreferredIMEPillSize()`（`layout.go:148`），68×30；显示时按标签重算（`PreferredPillSizeForLabel`，`main.go:1408`） | `ShowInactive()`（`main.go:1418`）；自动隐藏（`scheduleIMEPillAutoHide`，`:1424`） | 无 | 无 | 隐藏（`:399`） | 不拿焦点，无需归还 |
| 候选窗 | `input_engine_ui.go:48-62` | Panel / `VibrancyGlass`（`:54`） | `CandidatePanelCornerRadius()`（`candidate_style.go:50`），取自 `[input_engine.style]`；创建时定死，改了要重启（注释 `input_engine_ui.go:52-53`） | `PreferredCandidatePanelSize`（`:48`） | `ShowInactive()`（`:82`） | 不处理，Esc 归输入法引擎 | 无 | 隐藏（`:62`） | 不拿焦点 |
| 选区工具条 | `main.go:401-420` | Panel / `VibrancyGlass`（`:410`） | 写死 `17`（`:411`），等于高 34（`selectionBarHeight`，`selection_bar_view.go:39`）的一半；内容根用 `radiusPill`（`:81`） | 宽 `PreferredSelectionBarWidth` × 34；显示时 `SelectionBarSize`（`selection_bar_view.go:69-71`） | `ShowInactive()`（`main.go:1906`），注释写明「never takes focus」（`:1881`） | 无 | 无 | 隐藏（`:420`） | 不拿焦点 |
| Prompt 编辑器 | `main.go:355-379` | Panel / `VibrancyGlass`（`:364`） | `NormalizeCornerRadius(appearance)`（`:365`；`layout.go:207-216`，默认 16，上限 40）；内容根同源（`prompt_composer.go:294`、`:320`） | `PreferredPromptComposerPanelSize(2)`（`:355`）；显示时按槽位数重算（`:1755`） | `Show()` + `Focus()`（`:1761-1762`），之前先 `markPresentTransition` 350 ms 和 180 ms（`:1735`、`:1754`） | 关闭（`prompt_composer.go:402`、`:494`、`:590`）；组字中不处理（`:398`） | 关闭（`main.go:372-379`），过渡期内忽略 | `ClosePromptComposer`（`:371`） | Panel 语义：隐藏后键盘回到原应用（MyGo `window.go:196-198`） |
| 胶囊 | `main.go:325-353` | Panel / `VibrancyGlass`（`:334`） | `NormalizeCornerRadius`（`:335`）；内容根同源（`capsule_view.go:35`、`:58`） | `PreferredCapsuleSize(PhaseLeader)`（`:325`）；随阶段重算（`PreferredCapsuleSizeForState`，`:1550`） | HUD 阶段 `ShowInactive()`（`:1561`）；其他阶段 `Show()` + `Focus()`（`:1578-1579`），带延迟显形（`capsuleRevealDelay`，`:1569`） | 逐层：动作菜单先关（`capsule_view.go:445-451`、`capsule_preview.go:221-223`），其余交给引擎 `EventEscape`（`:368-371`、`:928-930`、`:937-942`、`:1181-1183`） | `EventDismiss`（`main.go:344-353`），过渡期、Hidden、HUD 阶段除外 | `EventDismiss`（`:341-343`） | Panel 语义；注入前等 35 ms（`focusRestoreDelay`，`main.go:1716-1726`） |
| 设置 | `main.go:422-432` | 普通窗口 / 不透明 | 系统窗口 | 960×640，最小 760×500，`StateKey: "settings"` | `App.Show()` + `Show()` + `Focus()`（`:1790-1792`） | 只取消快捷键录制（`settings_view.go:827-828`） | 不关 | 隐藏（`:432`） | **无**：关窗后 Clavis 仍是活跃应用（P-13） |
| 引导 | `main.go:434-445` | 普通窗口 / 不透明 | 系统窗口 | 600×740，最小 520×520，无 `StateKey` | `App.Show()` + `Show()` + `Focus()`（`onboarding.go:115-117`） | 无 | 不关 | `finishOnboarding`（`:445`），里面只隐藏（`onboarding.go:135-136`） | **无** |

从表里能看出的问题：

1. 圆角来源有 4 种：
   - 其中两个是字面量（15、17），而且窗口遮罩和内容根用的是两套数。高度一改，两边就对不上。
   - `TestStyleLiteralGuard`（`uiapp/style_guard_test.go:111`）只拦 `Radius(n)`，拦不住 `CornerRadius: 15`。
2. MyGo v0.9.0 没有 `SetCornerRadius`（`window.go` 里的 Set 系列只有 `SetVibrancy`、`SetHasShadow`、`SetOpacity` 等），窗口遮罩在创建时就定死了。
   - 只有候选窗写了注释说明这一点。
   - 在 `[appearance]` 里改圆角后，胶囊和 Prompt 的窗口边缘要重启才会变，设置页没有任何提示。
3. 7 个窗口都没有显式设置阴影：全仓没有 `SetHasShadow` 调用。`elevationShadow` 只用在窗口内的元素上：
   - `kit_surface.go:22`、`:37`：elev2；
   - `onboarding_view.go:428`：elev1；
   - `settings_view.go:147`：elev1。
4. 失焦关闭时「是否在过渡期」的判断写了两遍（`main.go:345`、`:373`）。
5. Esc 的处理分散在 3 个文件、10 处，每处各自判断是否在组字。
6. MyGo 没有窗口层级 API，只有 `AlwaysOnTop bool`（`window.go:135`、`:1275`）。5 个浮层处于同一层，谁在上面取决于谁后显示。

### 2.2 SurfaceSpec：只有 5 个字段

只是示意，本任务不写代码：

```go
// 目标：uiapp/surface.go（新文件）
type SurfaceSpec struct {
	Elevation    int             // elevNone..elev4（tokens.go:98）
	Radius       SurfaceRadius   // 圆角从哪来，不写像素值
	Blur         SurfaceMaterial // MaterialOpaque | MaterialGlass
	Dismiss      DismissPolicy   // 位集：DismissEscape | DismissBlur | DismissExplicit
	FocusRestore FocusRestore    // FocusNever | FocusPanel | FocusPreviousApp
}

type SurfaceRadius int

const (
	RadiusSystem     SurfaceRadius = iota // 不设 CornerRadius，交给系统窗口
	RadiusAppearance                      // NormalizeCornerRadius(cfg.Main.Appearance)
	RadiusInputStyle                      // CandidatePanelCornerRadius()
	RadiusHalfHeight                      // 创建高度 / 2
)
```

#### 字段语义与默认值

零值就是默认值。

| 字段 | 含义 | 默认 | 作用 |
|---|---|---|---|
| `Elevation` | 层级 | `elevNone` | 决定上下顺序（2.3）。对 Panel 还决定原生阴影：`elevNone` 关阴影，其余开（`SetHasShadow`，`window.go:1304`）。原生阴影只有开和关，`tokens.go` 里的偏移、模糊值仍只用于窗口内的卡片。普通窗口的阴影交给系统 |
| `Radius` | 圆角从哪来 | `RadiusSystem` | 窗口遮罩（`WindowOptions.CornerRadius`）和视图根节点的 `.Radius()` 用同一个函数 `surfaceRadius(spec, h, cfg)` 求值，不再各写一份。运行时值变了：内容层马上跟着变，窗口遮罩要重启才变，设置页要在对应字段旁注明（[SF-15]） |
| `Blur` | 材质 | `MaterialOpaque` | `Glass` = `Transparent` + `Frameless` + `VibrancyGlass`；`Opaque` = 不设 Vibrancy。owner 规则：只有浮层能用 `Glass`。开了「减少透明度」时，`Glass` 也画成不透明。visual-language.md 的无障碍一节已经这样规定了内容层，这里补上窗口层 |
| `Dismiss` | 哪些方式能关掉它 | `DismissExplicit` | 见 2.4 |
| `FocusRestore` | 关掉后键盘回到哪 | `FocusNever` | 见 2.5。窗口类型也由它推出：`FocusPreviousApp` 是普通窗口；其余是 Panel + `Frameless` + `AlwaysOnTop` + `SkipTaskbar` |

#### 不进 SurfaceSpec、留给各窗口自己管的

- 尺寸和最小尺寸；
- 位置与锚点（`computeCapsuleBounds` / `computeIMEPillBounds`）；
- 显示时机：延迟显形、过渡期、自动隐藏计时；
- `StateKey`、标题、内容视图；
- 胶囊在不同阶段的判断（Hidden / HUD）。

#### 各窗口的取值

| 窗口 | Elevation | Radius | Blur | Dismiss | FocusRestore |
|---|---|---|---|---|---|
| IME 指示胶囊 | `elev4` | `RadiusHalfHeight` | Glass | `DismissExplicit`（只由程序隐藏） | `FocusNever` |
| 候选窗 | `elev3` | `RadiusInputStyle` | Glass | `DismissExplicit` | `FocusNever` |
| 胶囊（非 HUD 阶段） | `elev3` | `RadiusAppearance` | Glass | `Escape \| Blur \| Explicit` | `FocusPanel` |
| 胶囊（HUD 阶段） | `elev3` | `RadiusAppearance` | Glass | `DismissExplicit`（自动隐藏） | `FocusNever` |
| Prompt 编辑器 | `elev3` | `RadiusAppearance` | Glass | `Escape \| Blur \| Explicit` | `FocusPanel` |
| 选区工具条 | `elev2` | `RadiusHalfHeight` | Glass | `DismissExplicit` | `FocusNever` |
| 设置 | `elevNone` | `RadiusSystem` | Opaque | `DismissExplicit` | `FocusPreviousApp` |
| 引导 | `elevNone` | `RadiusSystem` | Opaque | `DismissExplicit` | `FocusPreviousApp` |

胶囊是同一个窗口，按阶段取两组值（`capsuleSurface(phase)`）。两组只有 `Dismiss` 和 `FocusRestore` 不同；在创建时就定死的三个字段完全一样，所以不会出现矛盾。

### 2.3 视觉层级

| 层 | Elevation | 窗口 | 为什么在这层 |
|---|---|---|---|
| L4 | `elev4` | IME 指示胶囊 | owner 定为最高。它只报告当前是中文还是英文，尺寸小（68×30 起），会自动隐藏，盖住别的浮层的时间很短。反过来，如果它被盖住，用户切换输入法后看不到结果 |
| L3 | `elev3` | 胶囊、Prompt 编辑器、候选窗 | 用户正在操作的浮层。同层时后显示的在上。候选窗总是在它服务的输入框出现之后才显示，所以会压在胶囊或 Prompt 上面，符合「组字优先」（visual-language.md 无障碍一节） |
| L2 | `elev2` | 选区工具条 | 被动出现的建议，不拿焦点；任何 L3 出现时都要压在它上面 |
| L0 | `elevNone` | 设置、引导 | 普通文档窗口，在系统普通层级，低于所有浮层 |

`elev1` 不分配给窗口，它是窗口内行级元素用的（`settings_view.go:147`、`onboarding_view.go:428`）。

#### 排序规则

- 层级高的永远在上；同层的，后显示的在上。

#### 唯一的实现点

- MyGo 没有层级 API，所以用「重新置前」来实现。
- 任何浮层 `Show` 或 `ShowInactive` 之后，把当前可见、`Elevation` 更高的浮层按从低到高的顺序各 `ShowInactive()` 一次。
- 这件事只放在 `main.go` 的一个函数里（`a.raiseAbove(spec)`），所有浮层的显示路径都调用它。
- `ShowInactive` 不改变键盘焦点，所以把 L4 重新置前不会抢走 L3 的焦点。对已经可见的窗口再调一次 `ShowInactive` 会不会重新排序，要靠 [SF-05] 手测确认。

### 2.4 Esc / 点外部 / 显式关闭

#### 各窗口适用哪种关闭方式

| 窗口 | Esc | 点外部（失焦） | 显式关闭 |
|---|---|---|---|
| IME 指示胶囊 | 不适用：它不拿焦点，Esc 到不了 | 不适用：它不会成为 key window，没有失焦事件 | 由程序隐藏：自动隐藏计时（`main.go:1424`） |
| 候选窗 | 不适用：Esc 归输入法引擎，用来取消组字 | 不适用 | 由程序隐藏：组字结束（`input_engine_ui.go:75`） |
| 选区工具条 | 不适用 | 不适用 | 由程序隐藏：选区消失（`main.go:1911-1923`）；点了工具条上的按钮后，由对应动作决定 |
| 胶囊 | 逐层后退：组字中不拦截 → 关动作菜单 → 交给引擎的 `EventEscape`（退一级或关闭） | 关闭 | OnClose（如 ⌘W）→ `EventDismiss` |
| Prompt 编辑器 | 组字中不拦截 → 关闭 | 关闭 | OnClose → `ClosePromptComposer` |
| 设置 | 逐层后退，**永远不关窗**：快捷键录制 → 删除确认 → 未保存横幅（等于「继续编辑」）→ 搜索框（有字就清空，空了就失焦） | 不关，它是文档窗口 | 红灯或 ⌘W → 隐藏，并归还焦点 |
| 引导 | 不处理 | 不关 | 红灯 → `finishOnboarding`，并归还焦点 |

#### 唯一的实现点

1. 窗口级（`main.go`）：`a.bindSurface(win, spec, dismiss func(reason DismissReason))`。这 7 个窗口的 `OnClose` 和 `OnBlur` 只在这里注册。
   - `OnClose`：沿用 `closeHides`（`main.go:579-587`），包住 `dismiss(Explicit)`。
   - `OnBlur`：只有 `spec.Dismiss` 含 `DismissBlur` 时才注册。先判断一次 `isInPresentTransition()`（`main.go:1821`），再调用 `dismiss(Blur)`。这样就去掉了 `:345` 和 `:373` 两处重复。
   - 窗口自己的判断留在各自的 `dismiss` 里：胶囊的 Hidden / HUD 阶段判断（`:348-351`）、Prompt 的 `PromptComposerOpen`（`:376`）。
2. 内容级 Esc（`uiapp`）：每种浮层一个函数 `a.surfaceEscape(kind) bool`。所有表示「退出 / 关闭」的 Esc 都调用它。
   - 组字判断只在这个函数开头写一次：组字中返回 false，不消费。现在这个判断写在三处：`capsule_view.go:445-447`、`prompt_composer.go:398`、`capsule_view.go:938-939`。
   - 内层自己先消费 Esc 并返回 true：动作菜单、快捷键录制、删除确认、未保存横幅、搜索框。
   - 胶囊现有的 4 处都只是转发 `EventEscape`，引擎本身就是唯一的决策点，这里不用动。
   - Prompt 的 3 处（`:402`、`:494`、`:590`）挂在不同的聚焦元素上。MyGo 的按键事件会不会从聚焦的输入框冒泡到根节点，还没有核实：
     - 会冒泡：合并成根节点上的一个处理；
     - 不会冒泡：3 处位置不变，但都调用同一个函数。这时「唯一实现点」指的是同一个函数，不是同一处注册。

### 2.5 焦点归还契约

按 `FocusRestore` 分三类。

#### FocusNever

适用：指示胶囊、选区工具条、候选窗、HUD 阶段的胶囊。

- 只能用 `ShowInactive()`。不得调用 `Show()`、`Focus()`、`App.Show()`、`App.Focus()`。
- 现状已经符合：`main.go:1418`、`:1906`、`input_engine_ui.go:82`、`main.go:1561`。
- 焦点从来没离开原应用，所以没有失败的情况。

#### FocusPanel

适用：胶囊（非 HUD 阶段）、Prompt 编辑器。

- 显示：只用 `Show()` + `Focus()`（`main.go:1578-1579`、`:1761-1762`）。不得调用 `App.Show()` 或 `App.Focus()`，否则 Clavis 会被激活，Panel 归还焦点的机制就失效了。
- 归还：靠 MyGo 的 Panel 语义（`window.go:191-199`）。隐藏后键盘自动回到原来的活跃应用，Clavis 不需要写代码。
- 隐藏后往目标应用写文字的路径，只能经过 `beforeInject`（`main.go:1695`）。它会等到最后一次隐藏浮层之后 35 ms 才继续（`:1716-1726`）。以后新增的注入路径也必须走这里。
- 失败的情况（新增行为，见 Q-08）：
  - 什么算失败：注入时的前台应用和会话开始时的不是同一个。比如用户中途切走了，或者目标应用已经退出。
  - 怎么处理：不注入；把结果放进剪贴板，并用 HUD 提示「目标应用已不在前台，结果已复制」。
  - 怎么判断：会话开始时用 `darwinFrontmostAppIdentity()`（`internal/bridge/bridge_darwin.go:1209`）记下前台应用，注入前再取一次比较。现在的注入路径里没有这种比较。

#### FocusPreviousApp

适用：设置、引导。

1. 打开时：记下 `returnTo`，即「最近一个不是 Clavis 的前台应用」。
   - bridge 已经每 200 ms 轮询一次前台应用（`bridge_darwin.go:1086`、`:1105`）。
   - 要补的是：遇到 Clavis 自身时跳过（判断函数 `isSelfClavisApp` 已有，见 `:1115`），另外存一份 `lastExternalApp`。
   - 记完之后，照现在的方式 `App.Show()` + `Show()` + `Focus()`。
2. 关闭时（OnClose、⌘W、`finishOnboarding`）：
   - 先隐藏窗口；
   - 如果另一个 `FocusPreviousApp` 窗口还开着（例如从引导页点「设置」，`onboarding_view.go:483`），什么都不做；
   - 否则按 bundle ID 激活 `returnTo`。bridge 现在没有激活应用的函数，要新增一个。
3. 失败时（`returnTo` 为空、应用已退出、激活失败）：
   - 调 `mygo.App.Hide()`（MyGo `app.go:499-502`），让 macOS 把下一个应用切到前台。
   - 但 `App.Hide` 会把本应用的所有窗口都藏起来，包括浮层。所以只在没有任何浮层可见时才调用；有浮层可见就什么都不做。

### 2.6 迁移顺序

原则：从没有状态的窗口开始。每一步单独提交，出了问题可以单独回退。

| 步 | 窗口 | 改什么 | 为什么排在这里 | 验收 |
|---|---|---|---|---|
| 1 | IME 指示胶囊 | 引入 `SurfaceSpec` 和 `surfaceRadius`；字面量 15 改为 `RadiusHalfHeight`；`elev4`；建好 `raiseAbove` | 不拿焦点，没有 Esc 和失焦；出问题只会影响圆角和层级，回退成本最低 | SF-01、SF-02、SF-03 |
| 2 | 选区工具条 | 字面量 17 改为 `RadiusHalfHeight`；`elev2` | 同样没有状态；顺便验证第 1 步的 `surfaceRadius` 能直接复用 | SF-01、SF-02 |
| 3 | 候选窗 | 套上 spec 和 `raiseAbove`；圆角已经有来源，不改 | 没有状态，但它在输入法的关键路径上，所以放在前两步验证过之后 | SF-06 |
| 4 | Prompt 编辑器 | 改用 `bindSurface` 和 `surfaceEscape`；去掉 `:373` 的重复判断 | 第一个会拿焦点的窗口，只有一个阶段 | SF-04、SF-08、SF-09 |
| 5 | 胶囊 | 改用 `bindSurface`；按阶段 `capsuleSurface(phase)`；Esc 仍由引擎决定 | 阶段最多（HUD、延迟显形、过渡期），放在 Prompt 验证过之后 | SF-05、SF-10 |
| 6 | 设置、引导 | `FocusPreviousApp`：bridge 增加 `lastExternalApp` 和按 bundle ID 激活的函数 | 唯一要改 bridge 的一步；兜底 `App.Hide()` 会连带藏掉浮层，要用 SF-12 手测确认「有浮层可见就不调用」的判断。SF-14（注入前检查）依赖 Q-08，不在这一步 | SF-07、SF-11、SF-12、SF-13 |

第一部分的设置页 IA 改造和上表互不依赖，可以并行。

---

## 第三部分　验收标准

格式：`[编号] 断言 —— 验证方式`。

验证方式有三种：
- **帧断言**：用 `ui.NewTester(view, w, h)` 无头渲染，写法同 `uiapp/visual_snapshot_test.go`，再按元素 Key 查询、推进时钟、注入按键。
- **代码契约**：单测，或对源码的 `grep` 计数。
- **手测**：在真实应用里操作。

### 设置页

- [IA-01] `settingsPages()` 恰好 12 页、5 组，组顺序为 输入 / 效率 / 智能 / 系统 / 关于，页 key 不重复 —— 代码契约（单测）
- [IA-02] 全仓不再出现 `settingsTabIdx`、`settingsSectionMeta`、`SettingsTabNames` —— 代码契约（`grep -rn` 结果为空）
- [IA-03] `OpenSettings(tab)` 对 5 个 `SettingsTab` 值打开的页，与 1.3 映射表一致 —— 代码契约（表驱动单测）
- [IA-04] 1.3 表里 8 个调用点的源码没有改动；`controller.go` 子命令的已有 case 只增不改 —— 代码契约（`git diff 97261f4 -- main.go uiapp/onboarding_view.go` 不涉及这些行）
- [IA-05] `settings <key>` 对 12 个 key 都能打开对应页；`ime`、`keymap`、`general` 等旧别名照常可用；未知参数的行为和现在的 default 一样 —— 代码契约
- [IA-06] 侧栏渲染出 5 个组标题和 12 个页行，点组标题不会切页 —— 帧断言（按 `settings-rail-group:*` 和 `settings-rail:*` 计数）
- [IA-07] 窗口 960×500 时侧栏可以滚动，搜索框不跟着滚 —— 帧断言
- [IA-08] ⌘1 … ⌘5 各自切到对应组的第一页 —— 帧断言
- [IA-09] 在片段页保存后切到权限页，页头没有药丸 —— 帧断言
- [IA-10] 在某页滚到底后切页，新页的第一张卡在视口内 —— 帧断言
- [IA-11] 即时控件保存成功：字段行末尾出现「已保存」，推进 1.5 s 后消失；页头不出药丸 —— 帧断言（推进时钟）
- [IA-12] 写盘失败：页头下方出现横幅，包含字段名、文件名、「重试」「在 Finder 中显示」；控件显示旧值；之后另一个字段保存成功，横幅仍在 —— 帧断言（注入一个 `SaveMain` 必返回错误的 ConfigStore）
- [IA-13] 写盘失败的页在侧栏行尾有红色状态点，切到别的页仍可见；重试成功后横幅和红点同时消失 —— 帧断言
- [IA-14] 草稿有改动时，编辑器标题和侧栏行出现圆点，保存按钮可用；没有改动时保存按钮不可用 —— 帧断言
- [IA-15] 草稿未保存时在同一编辑器里点另一条：不切换，出现「保存 / 放弃 / 继续编辑」横幅；三个按钮分别导致写盘后切换、丢弃后切换、留在原条目；Esc 等于继续编辑 —— 帧断言
- [IA-16] 草稿未保存时切页再回来，草稿原样还在；关窗再打开，草稿和圆点都在，关闭按钮有系统未保存圆点 —— 帧断言（切页）+ 手测（关窗再开）
- [IA-17] 实体删除（Leader 节点、片段、术语、工作流、AI 配置、API key）先出行内确认，不出模态层；确认行不是 `kitErrorBanner`；焦点默认在「取消」；Esc 等于取消 —— 帧断言
- [IA-18] 行内值删除（键位映射、输入法规则、剪贴板排除项）点了就生效，不出确认 —— 帧断言
- [IA-19] ⌘F 让搜索框获得焦点；输入「剪贴板」，结果里至少有「效率 › 剪贴板」页和「输入 › 快捷键」里的剪贴板快捷键字段 —— 帧断言
- [IA-20] 在搜索结果上按回车：目标页打开，目标字段行在视口内，并高亮一次；开了「减少动态效果」时没有滚动动画 —— 帧断言
- [IA-21] 在 API key 字段填一个随机串后搜索这个串，结果为空 —— 帧断言
- [IA-22] 搜索框里按 Esc：有字时清空，空时失焦；无论按多少次 Esc，设置窗口都不会关 —— 帧断言
- [IA-23] 片段、术语、工作流、Leader 为空，以及输入法规则为空时，显示和侧栏同一个图标 + 标题 + 一句说明 + 一个主按钮；点主按钮进入新建 —— 帧断言
- [IA-24] 权限页不再出现 `true` / `false` 文字；每项是状态点 +「已授权」或「未授权」，未授权时有「打开系统设置」按钮 —— 帧断言
- [IA-25] 中文和英文界面下，窗口宽 760 时页头的页名没有被截断（说明文字已经移到第二行） —— 帧断言

### 浮层

- [SF-01] 7 个窗口的 `WindowOptions` 里都没有 `CornerRadius` 数字字面量，全部由 `surfaceRadius` 求值 —— 代码契约（把 `TestStyleLiteralGuard` 扩展到 `CornerRadius:`）
- [SF-02] IME 指示胶囊和选区工具条的窗口遮罩圆角、视图根节点圆角、窗口高度的一半三者相等（高 30 对应 15，高 34 对应 17） —— 代码契约（`surfaceRadius` 单测）
- [SF-03] `FocusNever` 窗口的显示路径里没有 `.Focus()`、`.Show()`、`App.Show()`；显示之后，前台应用输入框里的光标继续闪 —— 代码契约 + 手测
- [SF-04] `FocusPanel` 窗口的显示路径里没有 `App.Show()` 和 `App.Focus()` —— 代码契约
- [SF-05] 指示胶囊可见时唤出胶囊或 Prompt：指示胶囊仍在最上面，键盘焦点在胶囊或 Prompt 上 —— 手测
- [SF-06] 在胶囊的输入框里组字时，候选窗压在胶囊上面 —— 手测
- [SF-07] 设置和引导窗口都不设 Vibrancy；打开「减少透明度」后，所有浮层都画成不透明 —— 代码契约 + 手测
- [SF-08] `OnBlur` 和 `OnClose` 只在 `bindSurface` 里注册；OnBlur 路径里 `isInPresentTransition` 只调用一次 —— 代码契约（`grep -c` 计数）
- [SF-09] Prompt 编辑器：用系统拼音组字时按 Esc，只取消组字、不关窗；不在组字时按 Esc 关窗 —— 手测
- [SF-10] 胶囊：动作菜单开着时按 Esc 只关菜单，再按一次才后退或关闭 —— 帧断言（写法同 `uiapp/capsule_interaction_test.go`）
- [SF-11] 从 Safari 的输入框打开设置再关掉：Safari 回到前台，光标回到原来的输入框 —— 手测
- [SF-12] 打开设置后退出原来的前台应用，再关掉设置：Clavis 不再是活跃应用（`mygo.App.IsActive()` 为 false，`app.go:494`）；当时可见的浮层没有被藏掉 —— 手测 + 代码契约（只在没有浮层可见时调用 `App.Hide`）
- [SF-13] 设置和引导同时开着，关掉其中一个，焦点留在 Clavis；关掉最后一个才归还 —— 手测
- [SF-14] 在胶囊里确认 AI 结果之前，先 ⌘Tab 切到别的应用：结果不注入，已在剪贴板里，HUD 显示提示 —— 手测
- [SF-15] 在设置里改 `[appearance]` 的圆角后，字段旁出现「窗口边缘重启后生效」 —— 帧断言

---

## 附录 A　关键取舍

每条后面写了被否掉的替代方案。

1. **页面身份用字符串 key，不扩充 `SettingsTab` 枚举**。key 同时要用在侧栏、深链、搜索锚点和测试里，整数下标做不到。
   - 否掉：给 `SettingsTab` 再加 4 个常量。那样仍是两套编号，P-01 没有解决。
2. **`SettingsTab` 保留，由 `OpenSettings` 在内部做映射**。这是满足「调用点零改动」的唯一做法。
   - 否掉：把 8 个调用点都改成 `OpenSettingsPage`。违反本任务的约束，收益也只是少一层映射。
3. **「通用」拆成「输入 › 快捷键」和「系统 › 外观」**。5 个组里没有「通用」，而这两张卡本来就不是一类。
   - 否掉：把「通用」作为第 6 组保留。不符合 5 组的目标，「通用」也说不清里面放什么。
4. **剪贴板单独成页**。这样页名就是功能名，不用搜索也找得到。
   - 否掉：把 `[clipboard]` 卡并进片段页。还是要靠猜。
5. **`SettingsTabGeneral` 打开「上次停留的页」，第一次打开时是快捷键页**。托盘「设置…」是个泛入口，回到上次的位置最省步骤。
   - 否掉：固定打开快捷键页。每次都要重新找。
6. **写盘失败用页内横幅**。失败要给「重试」「在 Finder 中显示」两个动作，而且要一直留到处理完。
   - 否掉：toast 或 HUD（会自动消失；HUD 还会浮在别的应用上）；模态弹窗（owner 禁止）；继续用页头药丸（就是 P-06）。
7. **日常的成功提示改为字段旁 1.5 s 的「已保存」**。提示出现在用户刚操作的地方。
   - 否掉：页头药丸。离操作的位置远，还会串到别的页。
8. **切页不拦未保存草稿，只拦同一编辑器里换条目**。草稿本来就存在内存里，切页不会丢；只有换条目会覆盖它。
   - 否掉：切页也拦。打断的次数多，换来的保护却是零。
9. **删除分两类：实体删除走行内确认，行内值删除立即生效**。前者删掉的是用户写的内容，后者一步就能加回来。
   - 否掉：全部都确认（行内值删得频繁，每次确认是负担）；全部不确认、改为撤销（需要撤销栈，不在本期范围）。
10. **搜索结果显示在侧栏，按回车才跳转**。打字过程中内容区不会跳。
    - 否掉：边打字边预览第一条结果。每打一个字，内容区就换一次页。
11. **SurfaceSpec 里的圆角存来源，不存像素值**。窗口遮罩和内容根节点用同一个函数求值，就不会再出现 15 和 999 两套数。
    - 否掉：存像素值。窗口高度一改，又会和遮罩对不上。
12. **层级靠「显示后把更高层重新置前」实现**。MyGo 没有窗口层级 API。
    - 否掉：
      - 给 MyGo 加 `Level` 选项：要改依赖，超出范围。
      - 用 `Parent` 子窗口：这些窗口之间没有父子关系，而且父窗口一隐藏，子窗口也会跟着隐藏（`window.go:175-183`）。
13. **焦点归还失败时调 `App.Hide()`，但有浮层可见时不调**。首先保证 Clavis 不会在没有窗口的情况下留在前台；`App.Hide` 会把浮层一起藏掉，所以加了这个条件。
    - 否掉：失败时什么都不做。那就是 P-13 的现状。
14. **迁移从无状态的浮层开始**。指示胶囊没有焦点、没有 Esc、没有失焦，出了问题只影响圆角和层级。
    - 否掉：先迁胶囊。它的阶段最多，出问题时很难判断是 spec 的问题还是阶段逻辑的问题。

## 附录 B　待 owner 拍板

- **Q-01** 是否同意把「通用」拆成「输入 › 快捷键」和「系统 › 外观」，以及让 `SettingsTabGeneral` 打开「上次停留的页」？
- **Q-02** 实体删除的「删除」按钮要不要用系统红（`t.Danger`）？
  - 现在红色只出现在错误横幅里。
  - 本文默认不用红色按钮，以守住「单一系统蓝」。
  - macOS 的惯例是破坏性按钮用红色。
- **Q-03** 搜索要不要支持拼音首字母，例如用 `jtb` 搜到「剪贴板」？要的话需要一张汉字到拼音的表，可以考虑复用输入法词库。
- **Q-04** L2 和 L3 要不要互斥？即胶囊或 Prompt 出现时，是否直接隐藏选区工具条。
- **Q-05** 退出应用时还有未保存的草稿：维持现状直接丢弃，还是写到临时文件、下次打开时恢复？
- **Q-06** 「智能」「关于」各只有一页，是否接受单页组？本文的处理是照常显示组标题，⌘N 的编号不变。
- **Q-07** 权限页换成状态行后，`TelemetryGauge3D` 在产品代码里就没有引用了，`TestSpatialComponentsAreWiredIntoProductionSurfaces`（`uiapp/spatial3d_test.go:36`）会失败。
  - 二选一：把它从测试名单里去掉；或者给它找一个真正的连续量来显示。
  - 相关背景：
    - `spatial_3d` 是默认预设（`docs/design/visual-language.md:12`）；
    - `enable_3d_effects` 默认开启（`pkg/core/config/main_config.go:160`）。
    - 这两项和「不要 3D 霓虹」是什么关系，需要 owner 定。
- **Q-08** 注入前发现目标应用已不在前台时，按本文的方案放进剪贴板并用 HUD 提示，还是直接放弃？这是新增行为，现在的注入路径不做这个检查。
