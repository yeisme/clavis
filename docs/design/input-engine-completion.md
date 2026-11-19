# 设计：输入法完成（输入法线的收口设计）

Status: `observed` 代码审计 + `inferred` 设计决策。基线 HEAD `21ac2db`。审计方式为只读通读 + `go build ./...` + 三包测试（`pkg/core/inputengine`、`pkg/core/ime`、`internal/bridge`），构建 exit 0、全绿。

前置文档：`own-input-engine.md`（路线 A/B 对比与 Windows MVP，基线 `458d7fe`）仍有效，本文不重复它，只回答一个问题：**这条线怎么算「完成」，还差哪些、按什么顺序。**

## 0. 结论

**引擎已经够用了，不够用的是 macOS 那一层的接线。** 完成输入法 = 补 5 个纯逻辑缺陷 + 4 个真机验证 + 1 个决定（IMK 到底做不做）。

三条判断，说清楚立场：

1. **不要再往引擎里加功能。** 全拼 DP 切分、双拼 5 方案、双向模糊音、词组/单字/简拼/用户词/AI 五类候选、翻页、数字选词、bigram 重排、异步 AI 降级、panic 兜底——都在了，且有单测。现在缺的是「macOS 上打出一个字」这条链的完整性，不是算法。
2. **TSF/IMK 短期不做，且要明确写「不做」的代价。** 代价是：内联 preedit 永远没有（组字串只能显示在候选窗首行）、提权窗口打不了字、独占全屏游戏用不了。这三个代价对「键盘优先的效率工具」是可接受的；对「替代微软拼音/搜狗当系统输入法」是不可接受的。**所以 Clavis 的定位是「第二输入法」，不是系统输入法替代品**——这句话要写进 README，否则用户会按系统输入法的标准验收它，然后失望。
3. **macOS 的 `mode="native_imk"` 是死配置，必须先收敛。** `validator.go:343` 接受它，但引擎只在 `""|inline_hook|dual` 时构建（`input_engine_keys.go:78`）——用户选了它等于关掉输入法，没有任何提示。这是「配置假装支持」的典型坑。

## 1. 现状总账

### 1.1 已实现（引擎与两端共性，`observed`）

| 能力 | 证据 |
| --- | --- |
| 全拼 DP 切分（整音节 1 分 / 末段前缀 2 分 / 单声母 3 分，`'` 强制断开） | `inputengine/engine.go:455-540`；用例 `engine_test.go:27-42` |
| 双拼 5 方案（xiaohe/ziranma/microsoft/sogou/pinyin_plus） | `engine.go:542-573`、`pkg/core/ime/schemes.go:282-291` |
| 模糊音（只扩候选、不参与切分） | `engine.go:693-697`、`787-817`；`ime.ExpandFuzzy` `schemes.go:304-340` |
| 候选五类：词组 exact/prefix、内嵌词表、单字（含 U 变体）、简拼、用户词、AI | `dictionary.go:251-286`、`lexicon.go:20/84-104`、`engine.go:577-587`、`704-716`、`238-247` |
| 提交路径：数字选词 / 空格 / 回车 raw / 退格 / Esc / 翻页 | `engine.go:314-351` |
| Shift 中英切换 | `engine.go:264-279`；darwin `input_engine_keys.go:316-318` + `bridge_darwin.go:911-921`；Win `input_engine_windows.go:83-93` |
| 用户词频 + bigram 重排（真被消费） | `rankTierByUserBoost` `engine.go:878-904`；`prevCommit` `engine.go:399`；单测 `unify_test.go:317-351` |
| 词频异步落库（RAM 镜像先生效） | `main.go:1201-1229` → `internal/storage/user_frequency_adapter.go:14-33` → `LearningWriter.QueueIMECommit` |
| 候选窗（横/竖、AI ✦、无焦点、caret 下方翻转、多屏夹取） | `uiapp/input_candidate_view.go:189-314`、`input_engine_ui.go:67-100` |
| 钩子顺序与自注入过滤 | Win `bridge_windows.go:643/653/680/706`；darwin `bridge_darwin.go:865/959/986/992` |
| 接管资格判定 | Win `input_engine_windows.go:102` + `hook_health_windows.go:258-284`；darwin `bridge_darwin.go:986` |
| AI 异步（250ms 防抖 / 1.5s 超时 / gen 防串 / 失败静默） | `input_engine_ui.go:106-200`、`inputengine/ai.go:13-18`、`uiapp/input_engine_ai.go:15-52` |
| panic 兜底（Reset + 透传） | `input_engine_darwin.go:70-76`、`input_engine_windows.go:75-81` |
| 设置页可改 7 项 | `uiapp/settings_input_engine.go:26-107` |
| 提交注入（Windows SendInput Unicode + tag；darwin 20 单元分块） | `input_engine_windows.go:136-149`、`input_engine_keys.go:59-63/380-390` |

### 1.2 五处「macOS 与 Windows 的差集」（这是真正的缺口）

| # | 差集 | 影响 | 类型 |
| --- | --- | --- | --- |
| D1 | **可打印标点在 darwin 被丢弃**：`inputEngineKeysForInline` 对 `, . ? ! ; :` 走 default 返回 `!ok`（`input_engine_keys.go:359-364`） | 组字中打标点，标点先进应用、拼音还没上屏 → 顺序错乱。`engine.go:352-382`「先提交首候选再透传」的兜底在 macOS **不可达** | 纯逻辑 |
| D2 | **darwin 只按键名判类**：`quote` 键打 `"` 仍算 `KeyApostrophe`，`minus` 打 `_` 仍当翻页 | 用户装非美式布局或按 Shift 时行为错 | 纯逻辑 |
| D3 | **darwin 无鼠标/焦点重置**：`resetInputEngine`（`input_engine_darwin.go:51-55`）全仓无调用者；Windows 有鼠标钩子（`hook_health_windows.go:210`） | 点击换位置后残留组字状态 | 纯逻辑 |
| D4 | **darwin 无「第三方中文 IME 是否激活」判定**：只排除自己的 IMK（`ports_unix.go:53-56`） | 用户切到系统中文输入法时可能双重组字（`未验证`，代码未拦） | 纯逻辑 + 真机 |
| D5 | **darwin 无 `TriggerSnippetReset`**：Windows 消费键时发（`input_engine_windows.go:127-130`），darwin 只靠「键不进 router」 | 片段触发的缓冲区可能不会被清 | 纯逻辑 |

另外 4 个两端都有的缺陷（不是差集，但都属于「必做」）：

| # | 缺陷 | 证据 | 影响 |
| --- | --- | --- | --- |
| E1 | **无高亮移动**：`e.sel` 恒为 0；上下方向键在组字中直接丢弃组字 | `engine.go:255/343/349/446/449`、`283-290` | 只能数字/空格选第一项，7 个候选只能靠翻页逐页选首项 |
| E2 | **数字越界吞键**：`n > CandidateCount` 返回 `Consume:true` 但不提交 | `engine.go:334-339` | 组字中按 8/9 静默丢失按键 |
| E3 | **ASCII 态无指示**：`View.ASCII` 无渲染消费者，`SetASCII` 无运行时调用者 | `engine.go:425/224` | 用户不知道现在是中还是英 |
| E4 | **候选窗不可点击**：全文无 `Clicked()` | `uiapp/input_candidate_view.go` | 鼠标用户无法选词 |

还有两个「设计写了但完全没做」的，要在文档里明确降级或删除：

- `mode="native_imk"`：validator 接受、引擎不建（`validator.go:343` vs `input_engine_keys.go:78`）→ **假配置**。
- `cmd/clavis-imk`：24 行 smoke-check，无 controller、无 `.m/.swift`；`imk_install.go:40` 的 Info.plist 却声明 `ClavisIMKController`/`ClavisInput` → 按此装出的 `.app` **没有实现类**，能否被系统加载 `未验证`。这是会真出事的「看起来有、装上没反应」。

## 2. 完成定义（DoD）

分三档，只有 A 档全绿才允许在 README 里写「输入法可用」。

### A 档：日常主力（必须）

在每个目标应用里，用 Clavis 输入法**连续输入一段中文而不出错**，且能随时退回系统输入法。

| # | 验收断言 | 验证方式 |
| --- | --- | --- |
| A1 | Word / Chrome / VSCode / 飞书 四类 app 输入中文整句，无丢字、无乱序 | 真机手测（macOS + Windows 各一次） |
| A2 | 组字中打 `, . ? !` 顺序正确（标点在上屏文字之后） | 单测（纯逻辑）+ 真机 |
| A3 | 组字中按方向键/点击别处，组字被明确取消而不是残留 | 单测 + 真机 |
| A4 | 中/英状态有可见指示（候选窗或胶囊上的角标） | `ui.NewTester` 帧断言 |
| A5 | 候选可用数字/翻页任意选择，越界按键不被吞 | 单测 |
| A6 | 密码框不接管（macOS 网页 `type=password`、Windows 浏览器各一次） | 真机 |
| A7 | 一键退回：关掉 `enabled` 后立即恢复系统输入法行为，无需重启 | 真机 |
| A8 | 崩溃/异常不吞键：引擎 panic 后按键照常到达应用 | 单测（已有 recover 路径，补断言） |

### B 档：该做（不阻塞「可用」，但影响口碑）

| # | 内容 |
| --- | --- |
| B1 | darwin 鼠标/焦点重置（D3） |
| B2 | darwin `TriggerSnippetReset`（D5） |
| B3 | 全角标点 + 成对符号 type-over + 数字后半角 + `Result` 增加光标位移字段 |
| B4 | 进程排除名单（游戏、远程桌面、IDE 终端） |
| B5 | `prevWord` 单源化（引擎 `prevCommit` 与 tracker `prevWord` 双份维护，`input_engine_keys.go:121-133`） |
| B6 | 词频学习跨重启生效的真机验证 |
| B7 | 长提交分块注入时序验证（darwin 20 单元分块） |

### C 档：可选（做之前先想清楚代价）

| # | 内容 | 代价 |
| --- | --- | --- |
| C1 | TSF 薄 TIP（V2，独立 C++ 仓库 + 签名安装器） | 4–8 人周；换来内联 preedit、提权窗口、独占全屏 |
| C2 | IMK 真宿主（Go 写 controller 或引入 Swift 壳） | 需 system input method 权限 + 签名；换来 macOS 原生 preedit |
| C3 | 候选窗鼠标点选 | 小工作量 |
| C4 | `mixed_english` / `auto_pair_punctuation` 接线或彻底删除 | 现在两个字段 Deprecated 还在 `inputEngineConfigEqual` 比较（`input_engine_keys.go:31-32`），改它们会触发无意义引擎重建 |

**对 C1/C2 的立场**：本轮不做，且要把理由写进 README 而不是留在设计文档里。做之前先回答一个问题——「用户是否愿意为了内联 preedit 去装一个需要系统权限的输入法？」以 Clavis 的用户画像（键盘重度用户、已经有 Squirrel/Rime），答案大概率是否。等 A 档全绿、有人真的抱怨「组字串看不到」再动手。

## 3. 关键设计决策

### 3.1 定位：第二输入法，不是系统输入法替代品

写进 README 与设置页文案：

> Clavis 输入法工作在「系统输入法为英文」的前提下。中文由 Clavis 接管；切到系统中文输入法时 Clavis 自动让位。它不提供内联组字、不支持提权窗口，因此不适合作为唯一中文输入法。

这句话直接决定了三件事：

1. 接管条件必须检查「系统 IME 当前是 ASCII 态」（Windows 已有 `hookIMEComposing`；macOS 缺 D4 → 必须补）。
2. 必须有可见的中/英指示（E3），否则用户不知道自己现在归谁管。
3. 必须能一键退回（A7）。

### 3.2 先收敛假配置，再做体验

顺序不能反。理由：`mode="native_imk"` 这种「配置接受了但不生效」的坑，比缺功能更伤——用户以为开了，实际是关的。

| 配置 | 现状 | 决定 |
| --- | --- | --- |
| `mode` | 接受 `inline_hook`/`native_imk`/`dual`/`""`，但只有 `""\|inline_hook\|dual` 会建引擎 | **收敛为 `inline_hook`（唯一有效值）+ `off`**；`native_imk`/`dual` 在 validator 里报错并提示「本轮不支持」；迁移时降级为 `inline_hook` 并写诊断 |
| `mixed_english` / `auto_pair_punctuation` | Deprecated，不读，但仍在 `inputEngineConfigEqual` 比较 | 二选一：接线（B3）或彻底删字段 + 删比较。**选接线**，因为 `mixed_english` 对中英混输是真实需求 |
| `imk_socket_path` | 预留，无消费者 | 保留但标注「未接线」；C2 落地前不动 |
| `cmd/clavis-imk` | 只有 smoke-check，Info.plist 却声明了 controller | **把 Info.plist 的声明删掉**，或把 binary 从打包清单里摘掉。不能让用户装一个假输入法 |

### 3.3 组字中「其他键」的处置规则（D1/E1/E2 的统一答案）

现在三处各自为政：标点走「先提交再透传」（但 darwin 不可达）、方向键丢组字、数字越界吞键。统一成一张表：

| 键 | 组字中 | 空闲 |
| --- | --- | --- |
| 可打印字符（含标点） | 提交首候选 → **透传该字符**（不消费） | 透传 |
| `1..9` 且 ≤ 候选数 | 提交第 n 项 | 透传 |
| `1..9` 且 > 候选数 | **透传**（不再吞） | 透传 |
| ↑ / ↓ | 组字中移动高亮（不翻页） | 透传 |
| ← / → | 提交首候选 → 透传 | 透传 |
| Tab | 提交首候选 → 透传 | 透传 |
| 鼠标按下 | 取消组字（不提交）→ 透传 | — |
| Ctrl/Alt/Win 组合 | 取消组字 → 透传 | 透传 |
| Esc | 丢弃组字 | 透传 |

这张表要成为 `pkg/core/inputengine` 里的表驱动测试（每行一个 case），两端桥接只负责把物理按键翻译成这张表的输入——**判类逻辑不再分散在 darwin/windows 两套 keycode 映射里**。这是解决 D1/D2 的根本办法：把「字符 → 语义」的归一化放进引擎，桥接只给 `Key{Code, Rune, Mods}`。

### 3.4 学习数据的单源

`prevWord` 现在两处维护（`engine.go:399` 的 `prevCommit` 与 `input_engine_keys.go:121-133` 的 tracker），`未验证` 是否漂移。决定：**引擎是唯一真源**，tracker 只读引擎暴露的 `LastCommit()`，删掉自己的副本。理由：bigram 的质量直接取决于 prev 链的准确性，两份维护迟早分叉，而且是那种「偶尔排序不对、没人能复现」的 bug。

### 3.5 候选窗交互

- 高亮可移动（↑/↓）+ 数字直选 + 翻页（- / =）三件套齐全后，鼠标点选（C3）优先级降到最低：键盘用户不会去点。
- 候选窗必须显示：组字串（首行）、候选列表、页码、AI 候选项带 ✦、当前中/英角标。
- 定位：caret → 窗口 → 鼠标三级回退已有；补「屏幕边缘翻转」的真机验证（多屏/竖屏）。

## 4. 批次与验收

| 批 | 内容 | 估时 | 验收 |
| --- | --- | --- | --- |
| I1 | 键语义归一化下沉到引擎（`Key{Code,Rune,Mods}`）+ §3.3 表驱动测试 + darwin 标点通路（D1/D2）+ 数字越界不吞（E2） | 10h | 表内每个 case 单测绿；darwin 组字中打 `,` 后应用只收到 1 个字符且拼音已上屏 |
| I2 | 高亮移动（E1）+ 中英指示（E3）+ 一键退回断言（A7） | 8h | `ui.NewTester` 帧断言：高亮位置随 ↑/↓ 变化；角标随 ASCII 切换；`enabled=false` 后钩子零接管 |
| I3 | 收敛假配置（`mode` 白名单、删 Info.plist 假声明、`mixed_english` 接线） | 6h | `native_imk` 必须报错；旧配置迁移后 `mode=inline_hook` 且有诊断；`inputEngineConfigEqual` 不再比较 Deprecated 字段 |
| I4 | darwin 重置与 snippet 对齐（D3/D5）+ 双源 `prevWord` 收敛（B5） | 6h | 点击换位置后组字被取消；消费键后 snippet 缓冲区被清；引擎与 tracker 的 prev 一致（单测） |
| I5 | 真机验证批次（A1/A6/A7 + D4 第三方 IME 抢键 + 多屏定位） | 8h | 四个 app × 两平台的手测记录落 `docs/input-engine-matrix.md`，失败项建 herdr 任务 |
| I6（可选） | 全角标点 / 成对符号 / 进程排除名单（B3/B4） | 10h | 标点映射表单测；排除名单命中即透传 |

合计约 38h（不含可选的 I6 与 C 档）。

**顺序理由**：I1 是纯逻辑且解决最大差集，必须先做；I2 是「用户能不能用」的可见部分；I3 是止血（假配置）；I4 收尾；I5 是唯一不能靠单测替代的部分，必须单独作为一个批次排真机时间，否则永远被「等有 Windows 机器时再说」拖住。

## 5. 风险与回退

| 风险 | 缓解 / 回退 |
| --- | --- |
| 键语义下沉改动面大（两端桥接 + 引擎） | 保留旧映射作 fallback 一个版本；新旧路径用 feature flag 切换，出问题关 flag |
| darwin 标点通路改动碰到 snippet 触发 | 组字中不触发 snippet（已有 `imeComposing` 语义），I1 补断言 |
| 收敛 `mode` 让既有用户配置报错 | 迁移而非报错：读到 `native_imk`/`dual` 自动降级为 `inline_hook` + 写一条诊断，validator 只在**新写入**时拒绝 |
| 删 Info.plist 声明后有人已经在用假 IMK | 输出一条「IMK 宿主未实现，已从打包中移除」的诊断；不静默 |
| 真机验证一直排不上 | I5 单独建 herdr 任务并标注「需 Windows 真机 / 需 macOS 真机」，不合并进代码批次 |
| `enabled=false` 默认值让功能无人验证 | 设置页把它做成 onboarding 的一步（「要不要试用 Clavis 输入法」），而不是埋在二级页面 |

## 6. 明确不做（本轮）

- TSF TIP（C1）、IMK 真宿主（C2）——理由见 §2。
- 内联 preedit——同上。
- 提权窗口输入、独占全屏游戏——路线 B 的固有边界，写进 README。
- 云端词库/联网语言模型——本地词表 + 用户频率 + AI 候选已够；引入联网词库会把隐私模型搞复杂。
- 候选窗鼠标点选（C3）——键盘用户不需要，等有人提。
- 双拼之外的形码方案（五笔/郑码）——不做。

## 7. 与另外两条线的关系

- **模板线**：输入法只消费模板的「触发词」概念（`;` 片段式），不参与模板渲染。两条线唯一的耦合点是「组字中不触发片段」这条互斥规则，已在 I1 的断言里。
- **多设备线**：`learning_data`（词频 + bigram）是输入法最值钱的可同步资产，Windows 与 macOS 共用同一份引擎，同步后两平台候选排序一致。这要求 B5（prevWord 单源）先做，否则同步的是有噪声的数据。
