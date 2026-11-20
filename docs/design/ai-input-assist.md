# AI 输入增强设计：优化、续写、联想、keymap

2026-11-20。目标：把 AI 从「敲命令才触发」变成**输入流程的一部分**——
写完一句按一个键，让 AI 优化它，或者给出接下来可能想写什么。

## 0. 先说清楚：已有的比想的多，缺的是「接得上」

我把现有能力盘了一遍，避免重复造：

| 能力 | 现状 | 入口 |
|---|---|---|
| 就地优化（改写聚焦字段的草稿） | **已实现** | `option+tab`（`TriggerInPlaceOptimize` → `startInPlaceTransform`） |
| 选中文本 AI 变换 | **已实现** | 双击左 Shift → 优化 prompt / 解释 / 翻译 / 润色 / 复制（`selection bar`） |
| 输入法 AI 候选（✦） | **已实现** | 打字时异步请求，带 `AITimeout=1.5s` |
| prompt 模板 + 槽位 | **已实现** | `PromptTemplate`（trigger/category/slots/optimizerPreset） |
| 撤销 | **已实现** | `snippet_undo`：5 秒内按退格回滚 |
| keymap | **部分** | 只有 CapsLock 点/持 + `remaps`，**没有「键+动作」绑定** |
| **续写 / 推荐后续输入** | **完全没有** | — |
| **联想（句子级）** | **完全没有** | — |

所以这个设计不是从零开始：**已有的 AI 变换能力很强，但只能从一个硬编码的快捷键进入，且不能配置**。

## 1. 核心设计：把 AI 动作变成可绑定的第一类公民

### 1.1 问题

现在 AI 能力散在三处、三套入口、三套触发方式：
1. `inplace_transform.go` 的 `mode`（optimize / rewrite / explain / translate / polish）
2. `SelectionBarConfig.Actions`（字符串列表）
3. `LeaderActionType`（`ai_rewrite` 等）

同一个「润色」在三个地方各写一遍判断。加一个新动作要改三处。

### 1.2 方案：统一 AI 动作注册表

```go
// pkg/core/aiaction/registry.go
type AIAction struct {
    ID          string   // "optimize" / "continue" / "explain" / "translate" / "polish"
    Title       i18n.Key
    Scope       Scope    // ScopeSelection | ScopeField | ScopeContext
    NeedsCtx    int      // 需要多少字符上下文（continue 用）
    Replaces    bool     // true=替换原文，false=插入结果
    Prompt      func(in Input) string
    DefaultKeys []string // 建议绑定
}
```

好处：`option+tab`、selection bar、leader、keymap 全从同一个表取，加一个动作只写一处。

**明确不做**：不做插件化的动作 DSL（用户自定义 prompt 已经由 `PromptTemplate` 覆盖，不要两套）。

## 2. 续写 / 推荐后续输入（你要的核心）

### 2.1 交互

```text
你在聊天框里写完「这个方案我看了，」
        ↓ 按 ctrl+option+回车（可改）
候选窗出现 3 条，每条是一句可能的后续：
  1. 不过预算部分需要再确认一下
  2. 整体方向没问题，下周可以启动
  3. 我今晚整理一份详细的风险清单
        ↓ 按 1/2/3 或 Tab 选择，回车插入
文字插入到光标处，前 5 秒按退格可撤销
```

### 2.2 为什么是显式按键，不是自动

| 方案 | 评价 |
|---|---|
| 边打边续写（自动） | **否**。每次请求 1-2 秒，会拖慢打字；而且会把用户正在写的每句话都发出去 |
| **写完一句按一次（选定）** | 押这个。用户明确说「我要你帮我想下一句」才发，延迟可接受，隐私可控 |
| 光标停顿 3 秒自动触发 | 否。会在地铁上、在输入密码时误触发 |

### 2.3 上下文边界（隐私硬约束）

| 项 | 决定 |
|---|---|
| 取多少 | 光标前 **200 字符**（约 2-3 句），不取全文档 |
| 取什么 | 仅当前聚焦字段的文本；**不取剪贴板、不取其他窗口、不取应用名以外的元信息** |
| 默认状态 | **关闭**。必须在设置里显式开启，开启时告知「会把光标前的文本发送给 AI 服务」 |
| 本地模型时 | 仍然默认关，但提示「当前使用本地模型，文本不出机器」 |
| 发送前预览 | 第一次开启后，第一次触发时显示「将发送：<前 80 字符>…」确认一次 |

这条不能妥协：续写等于把用户的半成品文字发出去，比候选词敏感一个量级。

### 2.4 插入与撤销

- 插入用 `client.insertText`（现有 `captureEditable` 的对称操作）
- 复用 `snippet_undo` 的 5 秒退格窗口，**不新造撤销机制**
- 插入后 HUD 提示「已插入 · 5 秒内按退格撤销」

## 3. 联想（与续写的区别）

「联想」在这个产品里应该分两层，不要混：

| 层 | 触发 | 形态 | 现状 |
|---|---|---|---|
| **词级联想** | 打字时 | 候选窗里的 ✦ 候选 | 已有，需改进 |
| **句级联写** | 显式按键 | 3 条后续句 | 本设计新增 |

### 3.1 词级联想的改进

现有 ✦ 候选的问题：`ShouldQueryAI` 对任何 2 字母输入都触发，等于每打一个词发一次请求。

改法：
1. **节流**：同一组字态只请求一次（已有 debounce，确认生效）
2. **长度门槛**：从 `>= 2` 提到 `>= 4` 字母（2-3 字母的候选词典已经够用，AI 帮不上）
3. **本地优先**：本地模型就绪时走本地（C1/C3 已规划），远端仅在显式开启时用
4. **失败静默**：保持现状（超时即放弃，不打扰）

### 3.2 推荐（recency 型）

「推荐后续输入」还有另一种理解：**根据你最近常打的内容推荐**。这个更便宜、更隐私友好：

- 从 `ime_user_lexicon` + `ime_bigrams`（已有表）取当前上下文之后最常跟的词
- 零网络、零延迟，直接在候选窗里排前面
- 这是 `T-clavis-ime-*` 里 bigram 参与生成那条的正解，**优先做这个**，AI 续写作为补充

我的判断：**推荐（本地统计）优先于 AI 续写**。前者每次都准、不花钱、不发数据；后者是「锦上添花但要知道自己在发什么」。

## 4. keymap 设计

### 4.1 现状缺口

`KeymapConfig` 只有：`caps_lock_tap`、`caps_lock_hold`、`nav_layer`、`remaps`。
**没有任何「键位 → 动作」的通用绑定**，所以 AI 动作只能绑死在代码里的 `option+tab`。

### 4.2 目标结构

```toml
[keymap]
enabled = true
tap_hold_threshold_ms = 200
caps_lock_tap = "escape"
caps_lock_hold = "nav"

# 新增：绑定表
[[keymap.bindings]]
keys = ["ctrl+option+return"]
action = "ai_continue"

[[keymap.bindings]]
keys = ["ctrl+option+o"]
action = "ai_optimize"

[[keymap.bindings]]
keys = ["ctrl+option+e"]
action = "ai_explain"

# 条件绑定：只在某些应用里生效
[[keymap.bindings]]
keys = ["ctrl+option+r"]
action = "ai_continue"
apps = ["com.apple.MobileSMS", "com.tinyspeck.slackmacgap"]
```

### 4.3 设计约束

| 约束 | 理由 |
|---|---|
| 键位必须含至少一个修饰键 | 裸键会抢走正常输入 |
| 冲突检测在加载时做，报出具体冲突的两条 | 静默覆盖是最难查的 bug |
| 动作找不到 → 加载失败并指名 | 拼错 action 名不该静默失效 |
| `apps` 条件可选，为空=全局 | 聊天和写代码该有不同的续写键 |
| 键位显示用用户习惯的符号（⌃⌥⏎） | 配置里写英文，UI 显示符号 |

### 4.4 与现有 leader / selection bar 的关系

- leader（右 Option）：**保留**，它是「命令面板」语义
- selection bar（双击左 Shift）：**保留**，它是「对选中文字做事」语义
- keymap bindings：**新增**，它是「对当前输入流做事」语义

三者语义不同，不合并。但**动作表是同一个**（§1.2），所以「润色」可以从三处都能触发。

## 5. 批次

| 批 | 内容 | 估时 | 依赖 |
|---|---|---|---|
| A1 | AI 动作注册表（统一 optimize/explain/translate/polish，从表取） | 8h | — |
| A2 | keymap bindings（含冲突检测、条件 apps、符号显示） | 10h | A1 |
| A3 | 本地推荐（bigram 驱动的「下一词」排序，零网络） | 8h | — |
| A4 | 续写动作（显式触发 + 隐私开关 + 首次确认 + 插入/撤销） | 12h | A1、A2 |
| A5 | 词级联想改进（长度门槛、节流、本地优先） | 5h | A3 |

合计 43h。**A3 优先于 A4**：每个字都受益，且不涉及隐私。

## 6. 风险

| 风险 | 缓解 |
|---|---|
| 续写把半成品文字发给云端 | 默认关 + 首次确认 + 明示发送内容；本地模型时额外提示 |
| keymap 绑定与系统快捷键冲突 | 加载时检查已知系统快捷键表；冲突时警告但仍允许（用户可能知道自己在做什么） |
| AI 动作表抽过头，把 prompt 模板也塞进去 | 边界：动作表只管「触发什么」，prompt 内容仍归 `PromptTemplate` |
| 续写结果质量差反而干扰 | 只给 3 条，不猜第 4 条；插入前可预览，不满意按退格撤销 |
| 词级联想门槛提高后觉得「变笨了」 | 保留配置项 `ai_min_letters`，默认 4，可调回 2 |

## 7. 明确不做

- **不做自动续写**（停顿触发）——会误触发、会持续上传文本
- **不做跨应用上下文采集**（不读剪贴板历史、不读其他窗口）
- **不把 AI 动作做成脚本 DSL**（prompt 模板已覆盖）
- **不改 leader / selection bar 的既有语义**
- **不在输入热路径上同步等 AI**（现有 `AITimeout` 铁律继续有效）
