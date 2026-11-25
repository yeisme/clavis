# Clavis 界面美化（T-clavis-visual-polish-claude）

- 任务：T-clavis-visual-polish-claude（2026-11-25，claude 接替 agy 施工）
- 分支：`clv/p5-foundation`
- 依据：主人定的视觉硬指标 —— **现代 macOS 灰阶分层 + 1px 发丝线 + 系统蓝 `#0A84FF` + Xcode 语法配色；液态玻璃只用于浮层，正文不透明**；禁止渐变 / 霓虹 / 紫蓝混合；钱与数据界面必须有真图表与成体系图标。数值锚点沿用 `docs/design/clavis-visual-spec.md` §0。
- 证据口径：全部截图来自 MyGo 无头 `ui.Tester` 的真实光栅化像素（不是设计稿、不是占位图），命令见第 5 节。数据类截图里的数字是测试夹具经控制器自己的数据通道注入的固定样本（与异步读取器写入的位置相同）；界面本身只读真实快照，不编造任何数字。

## 1. 结论一览

| 硬指标 | 改前（`3246b1d`） | 改后 | 证据 |
|---|---|---|---|
| macOS 灰阶分层 | 直接用 MyGo 默认主题：Tailwind zinc 灰（`#18181b` / `#27272a` / `#71717a`） | Clavis 自有 macOS 调色板：内容层 / 抬升层 / 悬停 / 按下 / 分隔线五级灰阶 | `uiapp/theme_macos.go`；`TestMacThemeHardMetrics` |
| 单一系统蓝 `#0A84FF` | Tailwind 蓝 `#2563eb`（浅）/ `#3b82f6`（深），且跟随桌面强调色 | 两种外观都固定 `#0A84FF`；桌面强调色换成紫色也不改色 | `TestMacThemeHardMetrics`（注入紫色系统强调色仍得 `#0A84FF`） |
| 玻璃只用于浮层、正文不透明 | Capsule 整张卡片是玻璃，正文只靠 92–94% 着色压暗，**正文是半透明的** | 只有浮层外壳 / 标题栏是玻璃（`#F5F5F7`@82% / `#1E1E20`@80%）；列表、预览、AI 回答、对话框正文一律铺 100% 不透明的 `contentSurface` | `TestCapsuleBodyIsOpaque`：品红背景下旧实现正文有 **287,718** 个像素透出背景，新实现 **0** 个 |
| 禁止渐变 / 霓虹 / 紫蓝混合 | AI 页眉着色器光晕（运行蓝光、出错粉光）+ 着色器「液态球」；新手引导显示靛蓝→钴蓝渐变图标 | 运行态改为扁平的系统蓝脉冲点；新手引导改为中性 ⌘ 标记 | 对比图 `cmp-06c`、`cmp-18` |
| 布局缺陷 | Leader 右栏「最近活动」第二张卡画在「快捷工具」上（所有 Leader 截图可见）；设置表单行写死 28pt 却放两行文字，说明文字溢进下一行 | 右栏各块定高并按真实面板高度装行；设置行最小 40pt | `TestLeaderRailBlocksNeverOverlap`、`TestLeaderRailActivityRowsFit` |

## 2. 实际落地的 Token

| 角色 | Light | Dark | 用途 |
|---|---|---|---|
| content（`Background`） | `#FFFFFF` | `#1C1C1E` | 一切阅读区，100% 不透明（`contentSurface`） |
| elevated（`Surface`） | `#F5F5F7` | `#262628` | 卡片、井、控件面、设置侧栏 |
| hover / pressed | `#EDEDF0` / `#E3E3E8` | `#2F2F32` / `#38383B` | 指针悬停 / 按下 |
| separator（`Border`） | `#D1D1D6` | `#48484A` | 控件描边；`hairline()` 取其 60% |
| text / secondary | `#1D1D1F` / `#6E6E73` | `#F5F5F7` / `#A1A1A6` | 主文字 ≥ 12:1，次要文字 ≥ 4.5:1（测试钉住） |
| accent | `#0A84FF` | `#0A84FF` | 只用于选中、焦点、主操作、运行中 |
| 浮层玻璃着色 | `#F5F5F7`@82% | `#1E1E20`@80% | 只着在浮层外壳（标题栏、边缘） |
| 语义色 | `#FF3B30` / `#FF9500` / `#34C759` | `#FF453A` / `#FF9F0A` / `#30D158` | 图标与圆点；当文字用时经 `statusInk` 提到 4.5:1 |

## 3. 改动清单

### 3.1 主题与表面（`7caab1b`）
- `uiapp/theme_macos.go`（新）：`macTheme(dark, prefs)` 跟随系统的高对比度、字号缩放，但不跟随桌面强调色；`statusInk` 让语义色文字达到 4.5:1。
- `uiapp/capsule_view.go`：`resolveAppearanceTheme` 统一走 `macTheme`，所有窗口（Capsule、设置、提示词编辑器、选区工具条、新手引导、标注、候选窗）一起换色；`capsuleBody` 把标题栏以下的阅读区铺成不透明内容层；Omnibox 标签改为一个分段控件（中性轨道 + 唯一的蓝色胶囊）；AI 页眉去掉着色器光晕与液态球，改为扁平蓝色运行点。
- `uiapp/spatial_wire.go`：新增 `shellGlassTint` / `contentSurface`；Capsule 卡片的玻璃只着外壳色；删除不再使用的页眉光晕绘制。
- `uiapp/kit_dialog.go`：对话框正文（`kitDialogBody`）不透明，外壳保持玻璃。
- `uiapp/pill_view.go`：IME 浮动胶囊此前没设主题（沿用 MyGo 默认），现在也走 Clavis 调色板。
- `uiapp/kit_header.go`：开关类底栏按钮（⌘I 预览）开启时显示为「按下的中性井」，不再是第二个蓝色按钮。
- `uiapp/kit_controls.go`：选中行只保留柔和蓝底，描边只在「增强对比度」时出现。
- `uiapp/settings_view.go`、`settings_rows.go`、`tokens.go`：设置侧栏成为不透明的抬升灰层；侧栏选中项去描边去阴影；分组卡片改为平面（抬升灰 + 发丝线，无投影）；表单行最小高度 `settingRowHeight = 40`。
- `uiapp/prompt_composer.go`、`settings_templates.go`：语义色文字改经 `statusInk`。

### 3.2 布局缺陷（`2bc584f`）
- `uiapp/leader_dashboard_view.go`：右栏每块定高，`leaderRailActivityRows` 按面板真实高度算「最近活动」能放几行（放不下就整块隐藏），右栏裁剪兜底；活动条目改为原生列表行（悬停高亮 + 状态点），「快捷工具」标题与按钮同一行。
- `uiapp/agent_manage_controls.go`：Agent 工作区标签超过 6 个时折叠为「+N」（当前筛选的工作区始终可见），不再铺成一片标签云。

### 3.3 新手引导与列表选中（`949961d`）
- `uiapp/onboarding_view.go`：引导页不再显示渐变 Dock 图标，改为与 Leader 标题栏一致的中性 ⌘ 标记。
- `uiapp/settings_rows.go`：设置主列表（Leader 树、片段、术语表、工作流）的选中行与侧栏一致，只有柔和蓝底。

### 3.4 管理页用量条（`c7ba370`）
- `uiapp/kit_meter.go`（新）+ `uiapp/management_view.go`：管理页（⌘8）「媒体与剪贴板」卡此前只写「配额 512 MB」「历史上限 200 条」，看不出用了多少。现在用缓存的剪贴板统计画两条真用量条：图片字节 / 媒体配额（`QuotaEvictions` 只统计图片）、条目数 / 历史上限。中性灰填充 + 发丝线轨道，≥ 90% 转系统橙并换警示图标（不只靠颜色），读数用等宽数字；没有新增文案，所以不需要新 i18n key。

### 3.5 统计页真图表（`2c78096`，子代理在独立 worktree 完成，cherry-pick 合入）
统计页（⌘7）原来四张卡各只有一个装饰性迷你图（96×28pt 七根细柱、两条 sparkline）。现在基于 MyGo 自带的无头图表包 `ui/chart`（`Plot` / `Axes` / `NewBand` / `NewLinear().Nice()` / `Select`）画真图表，所有数字都来自现有的异步快照（AI 用量、剪贴板统计、词库命中、Top 词、Agent 会话），一个都不编：
- **AI 用量（钱的界面）**：7 日堆叠柱（输入 + 结果 Token），圆整刻度、发丝线网格、更实的基线、段间 2pt 缝；历史柱两级中性灰，**今天**这根用 `#0A84FF`；图例；指针 / 键盘选中带提示（日期 · 调用次数 · 输入 · 结果）；KPI 行（今日 Token、7 日累计、调用次数）。
- **剪贴板**：按类型的 100% 构成条（当前筛选的那段变蓝）+ 带类型图标的图例行（条数、体积，等宽数字）+ 7 日捕获柱。
- **词库**：14 日命中率折线（0/50/100%），没有事件的日子是断点而不是 0；只有最新点用 8pt 蓝点；Top-5 词横条，值标在条尾。
- **Agent**：分道分布条（运行中 = 蓝，待审批 = 系统橙 + 图标，已完成 = 灰），块内标数、图例带百分比。
- 四张卡等高（178pt）、不透明，在 Omnibox 尺寸的 Capsule 里中英文都完整放下，小窗口里保持卡高并可滚动；加载 / 出错（可重试）/ 空态保留。
- 文件：`uiapp/kit_charts.go`（新，调色板 = 灰阶 + 一个强调色、几何常量、纯函数：刻度、标签抽稀、band 槽、最大余数法百分比、段宽、标签适配）、`stats_view.go`、`ai_usage_view.go`、`clipboard_stats_view.go`、`lexicon_stats_view.go`；`docs/design/ai-workbench-spec.md` 同步更新 AI 用量的展示位置说明。
- 测试：`kit_charts_test.go` 39 个测试函数（纯函数 + 真渲染的基线对齐、堆叠、指针 / 键盘提示、断点 vs 零、筛选高亮、分道颜色、各状态、不出现货币、卡片不透明、小窗滚动）；`visual_snapshot_hubs_test.go` 渲染统计 / 管理 / 资产三个 hub 的中英 × 深浅截图。

### 3.6 Xcode 语法配色（`3113fcf`，子代理在独立 worktree 完成，cherry-pick 合入）
代码此前在剪贴板预览里是单色，在 AI 回答里用的是 MyGo 默认的 GitHub 配色。现在两处都走 `xcodeCodeTheme`（`uiapp/syntax_xcode.go`，新），用 Xcode 12+ 「Default (Light) / (Dark)」配色：
- 关键字（浅 `#9B2393` / 深 `#FC5FA3`，半粗）、字符串（`#C41A16` / `#FC6A5D`）、数字与字符（`#1C00CF` / `#D0BF69`）、类型（`#3900A0` / `#D0A8FF`）、函数与属性（`#326D74` / `#67B7A4`）、注释（`#5D6C79` / 深色下 `#6C7986` 向文字色混 10%，因为原值在 `#1C1C1E` 上只有约 3.8:1）、预处理（`#643820` / `#FD8F3F`）、属性（`#815F03` / `#BF8555`）、链接（`#0E0EFF` / `#5482FF`）。每个 token 色在代码井上都 ≥ 4.5:1，由测试钉住。
- AI 回答的 Markdown 代码块：`CodeColors` 改为 Xcode 主题，代码井改为不透明。
- 剪贴板代码预览：原来围栏不带语言，所以一直是纯文本；现在 `detectCodeLanguage` 按前几行判断语言（sql、go、swift、rust、python、js/ts、shell、json、yaml、toml、html/xml、css、diff、c/c++、java），判断不出就保持纯文本，宁缺勿错。
- 性能：按（主题, 文本）缓存 span，命中时逐行校验、容量有上限；超过 32 KiB 的文本或超过 2000 字节的单行不高亮，256 KiB 的日志不会每帧重新分词。
- 测试：`syntax_xcode_test.go` 22 个测试函数（Xcode 色板钉住、对比度、样式、缓存、语言表、预览 / Markdown 两条路径的像素检查、深浅切换）；`visual_snapshot_code_test.go` 渲染 SQL / Go / AI 回答 / Shell / JSON / YAML / 原始模式 / 片段 8 组截图。

## 4. 前后对比（真渲染，左 = 改前 `3246b1d`，右 = 改后）

全部图片在 `docs/design/visual-polish/`，由 MyGo 无头渲染器以 2x 光栅化后缩到 1x 并排拼接。

| 图 | 看什么 |
|---|---|
| `cmp-01-leader-zh-light.png` | 灰阶 + 系统蓝；右栏「最近活动」不再压住「快捷工具」 |
| `cmp-02-omnibox-zh-dark.png` | 标签变一个分段控件；⌘I 预览开启时是中性按下态；选中行柔和蓝底 + 2pt 蓝条 |
| `cmp-05-clipboard-zh-light.png` | 剪贴板：玻璃只在标题栏，列表与预览是不透明内容层 |
| `cmp-06a-ai-streaming-zh-light.png` | AI 运行中：着色器液态球 → 扁平蓝色运行点 |
| `cmp-06c-ai-error-zh-light.png` | AI 出错：页眉粉色着色器光晕去掉 |
| `cmp-09-settings-general-zh-light.png` | 设置：表单行不再挤压溢出；侧栏成为抬升灰层；分组卡片变平 |
| `cmp-15-settings-ai-zh-dark.png` | 设置深色：侧栏选中去描边去阴影 |
| `cmp-18-onboarding-fresh-zh-dark.png` | 新手引导：渐变图标 → 中性 ⌘ 标记 |
| `cmp-20-prompt-composer-zh-light.png` | 对话框：玻璃外壳 + 不透明正文 |
| `cmp-30-hub-stats-zh-light.png`、`cmp-30-hub-stats-zh-dark.png` | 统计页：迷你图 → 带坐标轴 / 图例 / 提示的真图表 |
| `cmp-31-hub-manage-zh-dark.png` | 管理页：媒体配额与历史上限两条真用量条 |
| `cmp-40-code-sql-zh-light.png`、`cmp-41-code-go-zh-dark.png`、`cmp-42-code-ai-zh-light.png` | 剪贴板 SQL / Go 预览与 AI 回答代码块：单色 → Xcode 配色 |

## 5. 怎么验（命令与本次真实输出）

```sh
cd /Users/yeshugen/workspace/clavis && export PATH=/opt/homebrew/bin:$PATH && go test ./...
```
本次输出：退出码 0，40 个包全部 `ok`，末尾为
```
ok  	github.com/yeisme/clavis/pkg/core/toml	3.718s
ok  	github.com/yeisme/clavis/pkg/mobile	3.917s
ok  	github.com/yeisme/clavis/uiapp	31.181s
```

真渲染截图（全部 opt-in，不设环境变量时跳过）：
```sh
# 主矩阵：3 种视觉预设 × 2 种动效 × 中英 × 深浅，末尾自动校验每张 PNG 非空、非纯色
CLAVIS_SNAPSHOT_DIR=/tmp/clavis-shots go test -count=1 -run 'TestVisualSnapshots$' -v ./uiapp/
#   visual_snapshot_test.go:65: validated 556 PNGs in /tmp/clavis-polish/final-matrix
#   --- PASS: TestVisualSnapshots (147.09s)
CLAVIS_SNAPSHOT_DIR=/tmp/clavis-hubs go test -count=1 -run TestVisualSnapshotsHubs ./uiapp/   # 统计 / 管理 / 资产 hub
CLAVIS_SNAPSHOT_DIR=/tmp/clavis-code go test -count=1 -run TestVisualSnapshotsCode ./uiapp/   # 代码高亮
```

本次新增的关键测试（`go test -count=1 -v -run '...' ./uiapp/`，均 PASS）：`TestMacThemeHardMetrics`、`TestCapsuleBodyIsOpaque`、`TestDialogBodyIsOpaque`、`TestLeaderRailBlocksNeverOverlap`、`TestLeaderRailActivityRowsFit`、`TestAgentWorkspaceStripFolds`、`TestQuotaMeterMath`；仓库原有守卫 `TestStyleLiteralGuard`（禁止裸样式数字）与 `TestI18n*`（i18n key 存在且中英齐全、硬编码中文只减不增）同样通过。

「正文不透明」两个测试的反证：把正文的不透明底色临时去掉后，`TestCapsuleBodyIsOpaque` 报告 287,718 个正文像素透出背景，`TestDialogBodyIsOpaque` 报告 116,669 个；恢复后均为 0。

## 6. 边界与没做的

- **Dock / Finder 应用图标没改**：`uiapp/appicon_gen.go` 生成的图标是靛蓝→钴蓝渐变加琥珀 / 青色星芒，本身不符合「禁止渐变、紫蓝混合」。但 `go test` 会把生成结果回写 `resources/icon.png` 与 `resources/icon.ico`（`uiapp/ui_test.go:1215`、`uiapp/icons_iconset_test.go:77`），而本轮只允许提交 `uiapp/` 与 `docs/`；改生成器会让验收跑测试时工作区被改脏。界面内唯一显示它的地方（新手引导）已换成中性 ⌘ 标记。Dock 图标需要另开任务，连同 `resources/` 一起改。
- **不显示金额（¥ / $）**：数据层刻意不存成本（`internal/storage/learning_store.go:63` 「Cost is intentionally not stored」），本轮又不能改 `internal/` 与 `pkg/`，所以「钱」的界面以 Token 作为花费度量画真图，不编价格。要显示金额，需要先在数据层加价目表。
- **新文案只能复用已有 i18n key**：key 表在 `pkg/core/i18n`（不在允许范围），所以图表图例只用现有 key（如「输入 / 结果」= `settings.field.input` / `ai.view.result`）或数字与单位；用量条也没有新增文字。
- **Agent 工作台的数据密度**：工作区标签已折叠为「+N」，但会话列表本身的信息结构没有重做。
- **子代理报告的已知小问题（未处理）**：带 `- key: value` 列表项的 YAML 被既有的 Markdown 判定当成列表，不会进入高亮；MyGo 的 YAML 词法器会把值里的 `scheme:` 着成键色；没有语言标注的 AI 代码围栏保持纯文本；代码预览没有加行号栏。
