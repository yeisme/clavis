# Clavis Leader 面板视觉调研与改版依据

研究日期为 2026-11-20。本文只记录可追溯的官方资料、官方截图中的可观察模式，以及针对 Clavis 的落地建议。产品没有公开的内部像素值会明确标为“未验证”，建议值属于 Clavis 的设计决策。

## 证据等级

| 标记 | 含义 |
| --- | --- |
| 官方文字 | 官方文档或设计指南直接说明 |
| 官方截图 | 官方页面提供截图，可观察结构或状态，无法从页面确认精确像素 |
| 未验证 | 没有找到官方数值，不能当作产品规格 |
| Clavis 建议 | 为本项目提出的可执行目标，不声称是竞品原值 |

## 资料要点

- Raycast 把搜索栏放在窗口中心，并主动增大搜索栏和结果图标。其 Action Bar 固定承载导航标题、toast、操作和快捷键，Compact Mode 用来降低其他元素的视觉权重。官方还说明图标采用统一的描边粗细和圆角规则。 [Raycast A fresh look and feel](https://www.raycast.com/blog/a-fresh-look-and-feel)
- Raycast 的官方快捷键文档把 Root Search、Action Panel 和列表导航分层，Action Panel 以 `⌘K` 打开，操作项显示主操作、次操作和三级操作的快捷键。 [Raycast Keyboard Shortcuts](https://manual.raycast.com/keyboard-shortcuts)
- Alfred 允许主题分别调整文字、图标、padding、border、颜色、opacity、blur 和字体。它支持结果右侧数字快捷键、结果数量和滚动条开关。默认窗口是 non-activating panel，显示器位置可选默认屏、鼠标所在屏或活动屏。 [Alfred Appearance](https://www.alfredapp.com/help/appearance/)
- Alfred UI Toolkit 的外部窗口默认尺寸是 800×600。嵌入 Alfred 时，宽度跟随当前主题，高度按内容最小需求计算。这一数值属于 UI Toolkit 外部窗口，不应直接当作 Alfred 搜索面板尺寸。 [Alfred UI Toolkit Window](https://www.alfredapp.com/help/alfredui/window/)
- Spotlight 的官方支持页展示了搜索结果和 Quick Actions，并说明窗口可以拖动调整位置和尺寸。页面没有公布圆角、间距、毛玻璃半径或不透明度。 [Apple Spotlight guide](https://support.apple.com/en-euro/guide/imac/apd10f8d1038/mac)
- Linear 将命令菜单和上下文菜单作为键盘优先入口，并在菜单中提醒用户快捷键。其设计文章明确提到菜单要支持高频键盘用户，同时避免子菜单鼠标路径过长。 [Linear Invisible details](https://linear.app/now/invisible-details)
- Notion 官方快捷键文档把 `/` 命令菜单定义为可插入内容块的完整菜单，并用 `Esc` 关闭。官方没有给出该菜单的尺寸、模糊、颜色或键帽数值。 [Notion keyboard shortcuts](https://www.notion.com/en-gb/help/keyboard-shortcuts)
- Arc 官方帮助页将 Command Bar 作为可搜索和执行 Arc 操作的入口。页面没有公布面板尺寸、圆角、透明度或字号规范。 [Arc Command Bar Actions](https://start.arc.net/command-bar-actions)
- VS Code 的官方 Command Palette 指南要求命令名称清晰、适当显示键盘快捷键、按类别组织命令，并明确不使用 emoji 作为命令名称。官方示例是纵向 Quick Pick 列表，而不是 2×2 卡片网格。 [VS Code Command Palette UX Guidelines](https://code.visualstudio.com/api/ux-guidelines/command-palette)
- Apple HIG 将 macOS 正文基准列为 13 pt，并将较小的辅助文字列为 10 pt。Apple 的 Materials 指南说明 blur、vibrancy 和 blending mode 用于建立层级，macOS 支持 behind-window 和 within-window 两类背景混合。Apple 的 Images 指南要求分层背景本身保持不透明，避免底层内容直接穿透文字区域。 [Apple Typography](https://developer.apple.com/design/human-interface-guidelines/typography) · [Apple Materials](https://developer.apple.com/design/human-interface-guidelines/materials) · [Apple Images](https://developer.apple.com/design/human-interface-guidelines/images)

## 各产品规格对照表

“未验证”表示官方资料未公布该项，不表示产品一定没有该能力。

| 产品 | 面板尺寸策略 | 圆角、内边距、间距 | 玻璃、背景和遮罩 | 字号层级 | 键帽和快捷键 | 锚定和交互 |
| --- | --- | --- | --- | --- | --- | --- |
| Raycast | 内容驱动的搜索和结果窗口。官方设置页说明改变窗口尺寸时，文字、按钮和字段会一起缩放。精确默认宽高未验证。 | 精确数值未验证。官方强调更大的搜索栏、较大的结果图标和底部 Action Bar。 | 使用方式和内部 blur 数值未验证。官方截图可见独立浮层和清晰结果区。 | 精确字号未验证。搜索栏、结果、底部 Action Bar 有明显层级。 | Action Panel 以 `⌘K` 打开。主、次、三级动作分别绑定回车、`⌘↵`、`⌥↵`。官方未规定键帽像素。 | 键盘优先。列表上下移动，Action Panel 是上下文操作区。全局快捷键默认 `⌘Space` 或 `AltSpace`。 |
| Alfred | 搜索窗口按主题宽度和内容高度调整。UI Toolkit 外部窗口默认 800×600，搜索面板精确尺寸未验证。 | 主题可调 text、icon、padding、border 和 corner roundness。默认值未验证。 | 主题支持 opacity 和 Visual Effects Light/Dark blur。blur 半径未验证。非 opaque 主题可启用 blur。 | 主题可自定义字体和尺寸。默认字号未验证。 | 可显示结果右侧数字快捷键，也可关闭。数字键帽的尺寸和填充未验证。 | non-activating panel。可选择默认屏、鼠标屏或活动屏，也可选择屏幕位置。 |
| Spotlight | 可拖动和调整大小。官方页面未给出默认宽高或最大宽高。 | 精确圆角、内边距和行距未验证。官方截图可观察为单列结果和独立 Quick Actions 区。 | 原生系统材质。官方页面未公布具体 blur 半径和 opacity。 | Apple HIG 的 macOS 正文基准为 13 pt，辅助文字常用 10 pt。Spotlight 自身字号未验证。 | Quick Actions 置于结果下方或结果上下文中，精确键帽规则未验证。 | 系统搜索浮层，位置和尺寸可调整。 |
| Linear | 命令菜单按内容显示，精确默认宽高未验证。 | 精确数值未验证。命令菜单和上下文菜单强调简短、可扫描的列表。 | 玻璃、opacity、blur 未验证。 | 精确字号未验证。菜单项与快捷键有明确主次。 | 菜单显示可用快捷键，帮助用户记住高频动作。 | `⌘K` 命令菜单和右键上下文菜单并存。子菜单交互减少不必要的鼠标折线路径。 |
| Notion | `/` 打开的完整命令菜单按内容展示，精确宽高未验证。 | 精确数值未验证。官方只描述命令列表和 `Esc` 关闭。 | 玻璃和 blur 未验证。 | 精确字号未验证。 | 命令菜单以列表和搜索为主，没有公开键帽像素规范。 | 贴近编辑上下文出现，依赖键盘输入和 `Esc` 退出。 |
| Arc | Command Bar 以可搜索操作列表为主，尺寸和最大宽度未验证。 | 未验证。 | 未验证。 | 未验证。 | 未验证。 | 使用 `⌘T` 打开 Command Bar Actions，按动作列表导航。 |
| VS Code | Command Palette 是纵向 Quick Pick，内容增长时滚动，精确默认宽高未验证。 | 未验证。官方示例不是网格卡片。 | 未验证。 | Apple 基准可作为 macOS 宿主参考，VS Code 自有字号未验证。 | 适当时显示快捷键。命令名称要清晰并按类别组织。 | `⇧⌘P` 或对应平台快捷键打开，支持上下移动和回车执行。 |

## 面板骨架结论

### 尺寸策略

- 竞品共同采用内容驱动或可调尺寸。Raycast 和 Alfred 都把搜索、结果和操作区作为可缩放的整体，Spotlight 允许拖动调整。没有足够官方证据支持 Clavis 使用固定高度。
- Clavis 建议将 Leader 面板设为内容驱动高度，宽度以 520 pt 为起点，最小宽度 440 pt，最大宽度 640 pt。4 个动作时高度只容纳标题、两行列表和底部提示，动作超过 8 个后进入滚动区。以上是 Clavis 建议值。

### 列表和网格

- 列表适合命令数不确定、需要搜索、需要显示类别或快捷键的入口。Raycast、Linear、Notion、VS Code 的官方资料都把命令入口描述为搜索或纵向列表。
- 网格适合固定数量且每项需要大图标的工具入口。当前 Clavis 只有 4 个动作，但每项包含键位、图标、标题和组计数，2×2 卡片会把四种层级放在同一视觉重量上。
- Clavis 建议 Leader 首屏使用单列列表。4 个动作保持 4 行，超过 8 行启用滚动。若未来出现 6 个以上同类图标工具，另行评估 2 列紧凑网格。

### 锚定

- Alfred 公开了默认屏、鼠标屏和活动屏选择，Spotlight 允许调整位置。Raycast 的全局快捷键入口强调从任意位置打开，但官方没有公开锚点像素规则。
- Clavis Leader 建议继续居中于活动显示器，不跟随光标。Leader 是全局命令入口，跟随光标会让键盘用户在多屏和高 DPI 场景下产生位置漂移。

## 玻璃、背景和可读性

| 项目 | 竞品证据 | Clavis 建议 |
| --- | --- | --- |
| 玻璃用途 | Apple 将 blur、vibrancy 和 blending mode 定义为建立层级的材料能力。Alfred 允许非 opaque 主题使用 Light/Dark Visual Effects blur。具体竞品半径未验证。 | 玻璃只用于浮层边界和外轮廓。正文区域使用不透明灰阶面板，避免终端文字穿过正文。 |
| 背景透过 | Apple HIG 说明背景层应保持不透明。Spotlight 和 Alfred 的官方资料没有公布遮罩参数。 | 面板正文底色使用不透明系统灰阶。若保留外层材质，建议背景不透明度 0.90–0.94，叠加 1px 发丝线。该范围是 Clavis 建议，不是竞品实测值。 |
| 模糊半径 | Alfred 只公开 blur 类型，未公开半径。Apple 只说明材料会模糊和混合，未规定单一半径。 | 不在文档中声称竞品半径。Clavis 视觉试验可从 24–32 px 的外层 blur 起步，正文仍保持不透明。 |
| 文字可读性 | Apple HIG 将层级和 secondary label 分开。官方竞品页没有给出统一对比度数字。 | 正文和选中态文字最低达到 WCAG AA 普通文字 4.5:1，对大字号至少 3:1。次要文字不能低于 4.5:1 的目标，避免底层内容干扰。对比度应使用最终合成后的面板背景计算。 |

## 颜色与层级

Clavis 已确定的视觉约束是 Apple 灰阶分层、1px 发丝线和单一系统蓝 `#0A84FF`。竞品证据支持将色彩集中在选中态和动作反馈，而不是让每个键帽都成为高饱和色块。Raycast 将 Action Bar 作为动作承载区，VS Code 要求清晰命令名和合适的快捷键，Apple HIG 将 accent color 用于按钮、选中和侧栏图标。 [Raycast A fresh look and feel](https://www.raycast.com/blog/a-fresh-look-and-feel) · [VS Code Command Palette](https://code.visualstudio.com/api/ux-guidelines/command-palette) · [Apple Color](https://developer.apple.com/design/human-interface-guidelines/color)

| 层级 | Clavis 建议 |
| --- | --- |
| L0 面板底 | 深色模式使用近黑灰阶，浅色模式使用近白灰阶，正文背景不透明。 |
| L1 行容器 | 比面板底亮或暗一个灰阶层，使用 1px 发丝线区分，不再为每个动作使用彩色大卡片。 |
| L2 选中态 | 只使用 `#0A84FF` 的低透明度填充或 1px 描边，文字保持高对比度。hover 只提升亮度和描边，不另加紫色或蓝色渐变。 |
| L3 系统状态 | “通用控制 / ABC”和“已锁定英文”收进同一状态行，使用小号次要文字和单一图标。状态徽章不与标题竞争。 |
| 强调色用量 | 键帽不使用高饱和蓝底。键帽采用灰阶底、1px 描边，只有按下或选中时使用系统蓝。 |

## 键盘提示与键帽

- Raycast 的 Action Panel 将快捷键直接放在动作上下文中，Alfred 支持结果右侧数字快捷键，VS Code 在命令项适当显示快捷键。三者都把快捷键当作辅助信息，未把键帽做成主视觉。
- Clavis 建议底部提示条只保留 `a–z`、`/`、`⌫`、`esc` 四组提示，使用 11 pt、次要灰阶、12 pt 组间距。该字号和间距是 Clavis 建议。
- 键帽建议 22×20 pt，圆角 5 pt，灰阶填充，1px 发丝线，文字 11 pt、中等字重。键帽与图标之间保持 8 pt，图标与主标题之间保持 10 pt。具体竞品键帽尺寸未验证。
- 快捷键提示条和动作列表之间使用 1px 分隔线。提示条不再包含第二组徽章，状态放到标题右侧或提示条最右端。

## 问题 → 建议 → 产品依据

| Clavis 当前问题 | 建议 | 产品依据 |
| --- | --- | --- |
| 玻璃透明度过高，终端文字穿透 | 正文改为不透明灰阶面板，外层只保留轻量材质和 1px 发丝线。可从 0.90–0.94 背景不透明度与 24–32 px 外层 blur 试验。 | Apple HIG 要求背景层保持不透明。Alfred 允许 opacity 与 blur 分开调整，说明两者应成为独立参数。 [Apple Images](https://developer.apple.com/design/human-interface-guidelines/images) · [Alfred Appearance](https://www.alfredapp.com/help/appearance/) |
| 淡紫、淡蓝造成品牌色漂移 | 全面移除紫色和多色渐变，只保留 Apple 灰阶与 `#0A84FF`。蓝色只用于选中、焦点和可执行反馈。 | Apple HIG 将 accent color 用于按钮、选中和侧栏图标。Raycast 的改版把层级集中到搜索栏和 Action Bar，而不是多色卡片。 [Apple Color](https://developer.apple.com/design/human-interface-guidelines/color) · [Raycast A fresh look and feel](https://www.raycast.com/blog/a-fresh-look-and-feel) |
| 2×2 大卡片松散，键帽和图标挤在一起 | 4 个动作改为单列 4 行，每行高度 44 pt，左右内边距 12 pt，键帽、图标、标题、组计数按固定列对齐。 | Raycast、Linear、Notion、VS Code 的命令入口都以纵向可扫描列表为主。VS Code 明确把命令按类别组织，官方示例不是网格。 [VS Code Command Palette](https://code.visualstudio.com/api/ux-guidelines/command-palette) · [Raycast Search Bar](https://manual.raycast.com/search-bar) |
| 标题、卡片、底部提示、右侧徽章权重接近 | 标题 15 pt/semibold，动作标题 13 pt/medium，次要说明 11 pt，底部提示 11 pt。状态徽章降到次要灰阶并合并成一行。 | Apple HIG 给出 macOS 13 pt 正文和 10 pt 小字号基准。Raycast 将导航标题、toast 和操作统一放入 Action Bar，减少并列层级。 [Apple Typography](https://developer.apple.com/design/human-interface-guidelines/typography) · [Raycast A fresh look and feel](https://www.raycast.com/blog/a-fresh-look-and-feel) |
| 高饱和蓝键帽抢过正文 | 键帽改为灰阶填充和 1px 描边，选中时才使用 `#0A84FF` 低透明度填充或蓝色描边。 | Raycast、Alfred、VS Code 的官方资料都把快捷键放在动作项的辅助位置。Alfred 可关闭结果快捷键，说明快捷键不应成为内容主角。 [Raycast Keyboard Shortcuts](https://manual.raycast.com/keyboard-shortcuts) · [Alfred Appearance](https://www.alfredapp.com/help/appearance/) · [VS Code Command Palette](https://code.visualstudio.com/api/ux-guidelines/command-palette) |
| 面板锚定和动作数量缺少明确规则 | Leader 继续居中于活动屏，内容高度驱动，4 行首屏，超过 8 行滚动。不要跟随光标。 | Alfred 公开默认屏、鼠标屏和活动屏选择。Spotlight 支持移动和调整，但两者都没有要求命令面板跟随光标。 [Alfred Appearance](https://www.alfredapp.com/help/appearance/) · [Apple Spotlight guide](https://support.apple.com/en-euro/guide/imac/apd10f8d1038/mac) |
| 底部提示条和状态徽章重复占用注意力 | 底部只保留四个输入提示，状态徽章移到标题右侧并使用 10–11 pt 次要灰阶，或在无状态时完全隐藏。 | Raycast 将上下文操作和快捷键集中在一个 Action Bar。Linear 通过上下文菜单提醒快捷键，避免额外常驻说明块。 [Raycast A fresh look and feel](https://www.raycast.com/blog/a-fresh-look-and-feel) · [Linear Invisible details](https://linear.app/now/invisible-details) |

## 最小改动清单

如果只改 5 个地方，按以下顺序落地。

1. **把 2×2 卡片换成单列动作列表**。4 个动作各占一行，行高 44 pt，行间距 4 pt，列表内左右 padding 12 pt。超过 8 行后滚动。依据是 Raycast、Linear、Notion、VS Code 的命令入口均以列表扫描为核心。
2. **把正文改成不透明灰阶底**。正文背景不透明，外层保留轻量玻璃和 1px 发丝线。先用 0.90–0.94 的外层背景不透明度和 24–32 px blur 做视觉校准。依据是 Apple HIG 对不透明背景层的要求，以及 Alfred 对 opacity 和 blur 的分离控制。
3. **收敛为一个强调色**。移除淡紫、淡蓝和渐变，只保留 `#0A84FF` 用于选中行、焦点描边和可执行反馈。依据是 Apple accent color 用途和 Raycast 将视觉重心放在搜索栏、图标与 Action Bar 的做法。
4. **降低键帽权重并固定列对齐**。键帽 22×20 pt、圆角 5 pt、灰阶填充、1px 描边、11 pt 字号，选中时才出现系统蓝。键帽与图标间 8 pt，图标与标题间 10 pt。依据是 Raycast、Alfred、VS Code 将快捷键作为辅助信息的模式。
5. **重排层级和底部提示**。标题 15 pt/semibold，动作标题 13 pt/medium，次要文字 11 pt，底部提示 11 pt。状态徽章并入标题右侧或提示条末端，底部只留 `a–z`、`/`、`⌫`、`esc`。依据是 Apple 13 pt/10 pt 基准、Raycast Action Bar 的合并层级和 Linear 的上下文快捷键提示。

## 不能从公开资料确认的项目

- Raycast、Alfred、Spotlight、Linear、Notion、Arc、VS Code 的 Leader/命令面板精确默认宽高。
- 各产品的圆角、内边距、行距、键帽宽高和具体字号。
- 各产品的 blur 半径、opacity 数值和最终合成后的对比度。
- Spotlight、Linear、Notion、Arc、VS Code 的内部玻璃材质参数。

这些项目在后续实现中应通过 Clavis 截图和可读性检查验收，不应回填为未经产品官方确认的竞品数值。
