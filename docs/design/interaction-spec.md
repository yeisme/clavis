# Clavis 面板与设置交互规格

- 状态：方案草案，供后续实现任务使用
- 范围：首屏搜索框、Tab 键位与下钻、终端 profile、词库下一批、keymap 设置界面
- 边界：只规定行为、数据和验收，不改本轮 Go 实现。

## 1. 现状盘点

| 主题 | 当前实现 | 证据 |
|---|---|---|
| Leader | 自动聚焦容器，按 Leader key 直接分派动作 | uiapp/capsule_view.go:272-404 |
| Omnibox | 搜索框在 Omnibox 页面常驻，query 改变时重建列表 | uiapp/capsule_view.go:456-517、pkg/core/engine/capsule.go:598-602 |
| 入口 | Leader 和 Omnibox 是不同事件，Omnibox 默认进入 actions tab | uiapp/controller.go:3443-3471、pkg/core/engine/capsule.go:442-457 |
| tab | 5 个 tab，鼠标点击和 Tab/Shift+Tab 切换 | uiapp/capsule_view.go:17-23、937-941 |
| 下钻 | Leader 用 LeaderPath 和 breadcrumbs 返回父级 | pkg/core/engine/capsule.go:810-820 |
| keymap | 配置支持 keymap.bindings，设置页当前只有开关、阈值、CapsLock、remaps | pkg/core/config/main_config.go:949-1115、uiapp/settings_editors.go:332-420 |
| CLI | Leader action 有 CLI/profile 字段，执行前有 profile 校验和 dry-run 保护 | pkg/core/config/leader_config.go:126-132、uiapp/controller.go:2894-3025 |
| 词库 | 45921 内嵌词、学习表、bigram 表和自定义词库契约已经存在 | docs/design/backend-roadmap.md:15-19、docs/design/ime-dictionary-architecture.md:47-67 |

## 2. 首屏常驻搜索框

### 2.1 决策

打开 Leader 时直接显示搜索框，下面是动作和最近项列表。原有 Leader 键仍可在搜索为空时触发 mnemonic，搜索框获得文本后由 query 驱动候选。Omnibox 保留完整 tab 视图，作为搜索结果的扩展页。

~~~go
type SurfaceMode string
const ( SurfaceLeader SurfaceMode = "leader"; SurfaceSearch SurfaceMode = "search" )
type SearchFocusState struct {
    Focused bool
    ComposingIME bool
    Query string
    Selected int
    Route string
}
~~~

OpenLeader 创建 SurfaceLeader，焦点落在搜索框。空 query 渲染固定的 6-8 个高频动作，按最近使用排序。输入第一个字符后立即切换 SurfaceSearch，候选列表联动。/ 不再是进入搜索的必要前置，只保留为 route/filter 前缀。

### 2.2 输入法冲突解法

- TextInput 先处理 IME preedit。preedit 存在时，未修改的 Enter、Tab、箭头和 Escape 不由面板抢占，沿用 uiapp/capsule_view.go:422-427 与 :499-512 的边界。
- Cmd/Ctrl 组合键始终由面板处理。
- 打开面板后只自动聚焦一次。点击列表不会重置 query 或强行抢回输入框。
- 中文输入时，列表更新采用 16 ms 合并窗口，避免每个 composition update 都触发完整索引。
- 没有候选时保留空态和当前 query，不能静默丢弃用户输入。

### 2.3 同类依据

- Raycast 把搜索框定义为窗口顶部唯一入口，Root Search 在输入框下实时显示结果，空 query 仍显示 favorites 和最近项。[Raycast Search Bar](https://manual.raycast.com/search-bar)
- Spotlight 打开即聚焦搜索字段，并用 Command-1 到 Command-4 过滤 Applications、Files、Actions、Clipboard，说明常驻搜索可以和分类过滤共存。[Apple Spotlight Shortcuts](https://support.apple.com/guide/mac-help/spotlight-keyboard-shortcuts-mh26783/mac)
- Alfred 官方把搜索结果数量、结果快捷键和非激活面板作为可配置项，说明焦点和结果密度应是设置而不是隐含状态。[Alfred Appearance](https://www.alfredapp.com/help/appearance/)
- Warp Command Palette 以全局搜索为中心，并用 sessions:、actions: 等前缀过滤类型。[Warp Command Palette](https://docs.warp.dev/terminal/command-palette/)
- Linear 的 Search 文档支持用 / 打开全局搜索、用前缀筛选 issue/project/user/team 等类型，并在空查询时展示最近搜索和最近 issue。这为“常驻搜索 + 可选 route 前缀 + 最近项”提供了直接依据。[Linear Search](https://linear.app/docs/search)
- Notion 的官方快捷键把 Cmd/Ctrl+P 或 Cmd/Ctrl+K 用作搜索或跳转最近页面，Tab/Shift+Tab 用于内容层级缩进和反缩进。它说明 Tab 应在当前编辑上下文有明确优先级，不能同时承担无条件的横向导航。[Notion Keyboard Shortcuts](https://www.notion.com/help/keyboard-shortcuts)

## 3. Tab 键位裁决与下一级

### 3.1 最终语义

| 按键 | 语义 | 生效条件 |
|---|---|---|
| Tab | 接受当前补全 | 搜索框有 inline completion，且没有 IME preedit |
| Shift+Tab | 跳到下一个 tab | 搜索框无 inline completion，且没有 IME preedit |
| Cmd+左/右 | 切换前一个/下一个 tab | 面板获得焦点，任何 query 状态 |
| Enter | 执行主动作或进入下一级 | 结果存在 |
| Esc | 关闭 action panel 或返回上一级 | 不在 IME preedit 中 |

裁决顺序是 IME preedit > inline completion > tab navigation。这样 Tab 在输入框中先完成文本，避免把补全动作误切成 tab。若没有补全，底部提示显示“Shift+Tab 切 tab”，不再显示“Tab 切 tab”。

### 3.2 “跳转下一级”的定义

“下一级”统一定义为 drill-down，即对当前选中项进入其子菜单、详情页或参数表单。它不是横向切换 tab，也不是打开新窗口。

~~~go
type DrillDown struct { ParentID, Title, Query string; Items []OmniboxItem }
func EnterDrillDown(state engine.State, item engine.OmniboxItem) (DrillDown, error)
func ReturnFromDrillDown(state engine.State) engine.State
~~~

- 有 Children 的 Leader 节点：进入子节点并追加 breadcrumb。
- 有参数的命令：进入参数表单，Tab 在字段之间移动，Shift+Tab 返回前一字段。
- Agent 会话：进入会话详情，详情页的发送消息是显式 gated action。
- 没有子内容的普通 action：Enter 直接执行。

Raycast 用 Enter 执行主动作、Cmd-K 打开 Action Panel、Esc 返回上级。VS Code 把命令按 category 分组而不是用网格下钻。该组合支持 Clavis 把下一级保持为纵向层级，把 tab 保持为横向分类。[Raycast Keyboard Shortcuts](https://manual.raycast.com/keyboard-shortcuts)、[VS Code Command Palette UX](https://code.visualstudio.com/api/ux-guidelines/command-palette)

浏览器和 macOS 控件把 Tab/Shift+Tab 保留给焦点移动。Apple 的键盘快捷键说明 Tab 与 Shift-Tab 分别移动到下一个和上一个控件，Chrome 文档也把 Tab/Shift+Tab 用于 tab group 内焦点前后移动。因此 Clavis 只有在当前输入没有可接受补全时才把 Shift+Tab 用作横向 tab 切换。[Apple Keyboard Shortcuts](https://support.apple.com/en-us/102650)、[Chrome Keyboard Shortcuts](https://support.google.com/chrome/answer/157179?hl=en&co=GENIE.Platform%3DDesktop)

### 3.3 迁移与验收

第一阶段同时显示旧快捷键兼容提示，第二阶段移除未修改 Tab 的切 tab 逻辑。验收覆盖：有补全时 Tab 接受文本、无补全时 Tab 不改变 tab、Shift+Tab 切 tab、Cmd 左右切 tab、IME preedit 中四个键都交还输入法、Enter 进入子页后 Esc 返回。

## 4. 终端可配置

### 4.1 配置模型

默认 profile 为 ghostty。profile 不只保存命令名，还保存参数模板和能力。

~~~go
type TerminalProfile struct {
    ID, Label, BundleID, Executable string
    Args []string // 支持 {cwd}, {command}, {title}
    WorkingDirArg string
    ShellCommand string
    SupportsCWD bool
    SupportsCommand bool
    Enabled bool
}
type TerminalConfig struct { DefaultProfile string; Profiles []TerminalProfile; FallbackProfile string }
~~~

模板替换只允许白名单 token。cwd 通过 exec.CommandContext 的 Dir 或应用专用参数传入，禁止把未转义路径拼进 sh -c。profile 失败时按顺序尝试 fallback，再显示错误和复制命令，不静默改用另一个应用。

### 4.2 资料与建议模板

| 终端 | 依据 | Clavis profile 建议 | 验证状态 |
|---|---|---|---|
| Ghostty | 配置文档说明配置 key 可作为 CLI flag，working-directory 接受绝对路径或 ~/ | ghostty --working-directory={cwd}，命令作为受控 argv | CLI flag 语法需本机验证 |
| Terminal.app | Apple 文档确认可从 Finder/Terminal 打开新窗口和 tab，但未给出通用 cwd CLI 参数 | AppleScript do script profile，脚本由参数数组生成 | AppleScript 模板未由本次页面验证 |
| iTerm2 | 官方文档提供 AppleScript window/tab/session 对象模型及 shell integration | AppleScript 创建 tab 并发送 cd -- {cwd}，或用户提供脚本路径 | 需本机验证权限提示 |
| Warp | 官方文档支持 working directory、Tab Configs、Launch Configurations，配置含 cwd、commands | 调用 Warp Launch/Tab Config 文件，profile 传入配置名 | 文档已验证，CLI 入口未验证 |
| kitty | 官方 launch 支持 --cwd、tab/window 类型和 remote control | kitty @ launch --cwd={cwd}，remote control 未启用时回退新进程 | 参数已官方验证，权限需本机验证 |
| Alacritty | 官方配置有 working_directory 和 window 参数 | alacritty --working-directory {cwd} | CLI flag 名需本机验证 |

Ghostty 的工作目录、kitty 的 launch --cwd、Warp 的 cwd/commands 结构均来自官方资料。[Ghostty Option Reference](https://ghostty.org/docs/config/reference)、[kitty launch](https://sw.kovidgoyal.net/kitty/launch/)、[Warp Launch Configurations](https://docs.warp.dev/terminal/sessions/launch-configurations/)、[Warp Working Directory](https://docs.warp.dev/terminal/more-features/working-directory/)

### 4.3 失败回退

1. profile 不存在或 executable 不可执行：标记 profile disabled，尝试 FallbackProfile。
2. cwd 不存在：询问是否用 workspace 父目录，禁止静默用 home。
3. macOS Automation 权限拒绝：显示“需要允许 Clavis 控制终端”，保留“复制命令”和“打开终端应用”动作。
4. 子进程启动后立即退出：保留退出码和 stderr 摘要，避免重复启动。

## 5. 词库下一批优先级

依据 docs/design/backend-roadmap.md:37-89 和 docs/design/ime-dictionary-architecture.md:47-67，下一批按以下顺序做。

### P0：热更新和诊断

先把 ~/.config/clavis/dicts/*.clvdict 的加载、watcher、缓存失效和整文件降级接成设置页可见能力。原因是词库更新目前需要重新发布 app，且架构文档已经规定 custom > learned > starter > builtin 和诊断契约。验收是新增/删除词库无需重启、坏文件不产生部分词库、有稳定诊断码。

### P1：多音字和简拼索引

在 canonical (pinyin, word) 键之外保存所有合法读音组合，并为全拼、首字母简拼建立共享倒排索引。候选生成先按完整音节匹配，再按简拼降权，不能把多音字压成单一读音。验收覆盖“重庆”两个读音、zgr 等简拼和重复导入幂等。

### P2：bigram 进入候选生成

ime_bigrams 目前只参与排序加权，路线图明确要实现 bigramCompose。完成后第二段输入直接把上一词的高频后继加入候选池，冷启动输出保持字节不变。验收覆盖有数据命中排前、无数据行为不变。

### P3：导入体验和可审计性

设置页提供 Rime 导入预览、字段错误定位、来源/权重统计和撤销。导入前显示将写入的 (pinyin,word) 数量、多音字展开数量和冲突数量。导出保持词与拼音字节级 round-trip，沿用架构文档约束。

### P4：同步与体积门槛

完成 MergeLearning 后再接同步入口。词库编译缓存按文件 digest/version 失效，启动只映射索引，避免每次解析原始文本。验收包括启动读取预算、缓存命中率和两设备 pinned/频率合并一致。

明确不先做神经语言模型 rerank，路线图已选择词库加 bigram 的本地路径，先解决数据入口、读音完整性和启动成本。

## 6. keymap 设置界面

### 6.1 页面结构

在设置页 keymap 卡片中增加“动作绑定”表格，和现有 remaps 分开。每行显示动作名称、已绑定按键、作用域、冲突状态和删除/重置按钮。分组顺序为 Global、AI、IME、Leader、应用范围。动作名称使用 Agent:、AI:、IME: 等 category 前缀。

~~~go
type KeyBindingDraft struct {
    Action string
    Keys []string
    AppBundleIDs []string
    Recording bool
    Conflict *KeyBindingConflict
}
type KeyBindingConflict struct {
    Chord string
    ExistingAction string
    Scope string
    Resolution string
}
~~~

### 6.2 录制流程

1. 用户点击“录制快捷键”。
2. 输入框进入 recording 状态，显示按下的 modifier 和 key。
3. 松键后规范化为 cmd+option+...，不接受无修饰普通键。
4. 立即查询当前绑定和 macOS 常见冲突，按全局/应用 scope 分层显示。
5. 用户选择替换、保留现有或取消。保存前再次调用 ValidateKeyBindings，避免并发修改覆盖。

Raycast 的 hotkey recorder 支持录制、冲突检测和从 Action Panel 直接配置。Warp 的设置页提供可搜索动作列表、文件导入和橙色冲突提示。VS Code 用 keybindings.json 保存绑定并要求动作 category 清晰。[Raycast Settings](https://manual.raycast.com/settings)、[Warp Keyboard Shortcuts](https://docs.warp.dev/getting-started/keyboard-shortcuts/)、[VS Code Command Palette UX](https://code.visualstudio.com/api/ux-guidelines/command-palette)

### 6.3 重置、导入、导出

- 行级“恢复默认”只恢复当前 action，保留其他绑定。
- 分组“恢复本组默认”需确认并显示将删除的自定义数量。
- 导出使用稳定 TOML [[keymap.bindings]]，不写机器窗口 ID。
- 导入先解析到 draft，显示新增、修改、冲突、未知 action 四类结果，用户确认后一次性写入。未知 action 拒绝导入，不静默丢弃。
- 设置页保留“打开 TOML”链接作为高级入口，GUI 和 TOML 使用同一 validator。

### 6.4 验收

必须覆盖录制一个 AI action、重复 chord 冲突、应用 scope 不冲突、未知 action 导入被拒绝、行级重置、导出再导入字节稳定，以及当前 uiapp/controller.go:1465-1482 的动作仍可执行。

## 7. 取舍与分批建议

### 选择

选择常驻搜索框加列表联动，因为 Raycast、Spotlight 和 Warp 都把搜索作为首屏入口。选择 Tab 补全优先，因为输入框的完成动作比分类切换更接近用户当前意图。选择 Shift+Tab 横向切 tab、Cmd 左右显式切 tab，因为 Raycast 在表单中把 Tab/Shift+Tab 留给字段移动，而 root navigation 使用独立快捷键。选择 drill-down 作为下一级，因为它能复用现有 LeaderPath，不制造第二套 tab 层级。

### 明确不做

- 不把 Tab 同时作为无条件补全和无条件切 tab。
- 不把终端命令硬编码在 action handler。
- 不把词库所有层编成不可审计的单一文件。
- 不在 keymap UI 中自动覆盖系统快捷键。
- 不把网格卡片用于长列表、agent 会话或 keymap 表格。

### 批次

| 批次 | 内容 | 验收 |
|---|---|---|
| I1 | 常驻搜索框、空 query 最近动作、IME 焦点边界 | 首屏可输入，中文 preedit 不被抢键，query 联动候选 |
| I2 | Tab 裁决、Cmd 左右切 tab、drill-down | 键位矩阵和子页返回测试全部通过 |
| I3 | TerminalProfile、Ghostty 默认和 fake executor | cwd、fallback、权限失败均有可见诊断 |
| I4 | 词库热更新、多音字/简拼索引 | 无重启更新、双读音、简拼、坏文件诊断通过 |
| I5 | keymap 录制、冲突、重置、导入导出 | GUI/TOML round-trip，冲突不静默覆盖 |
| I6 | Agent tab 接入 | 会话发现、跳转和三类补全按 agent-integration-spec.md 验收 |
