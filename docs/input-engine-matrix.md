# macOS 输入法测试矩阵

基线：`6a69bbb`（含 I1 判类修复）。被测二进制：`build/darwin-arm64/Clavis.app`（自签名 `Clavis Local Signing`，重建不丢授权）。
本文是 `docs/ROADMAP.md` I5 批次的执行清单。**A 档不过，README 里不许写「输入法可用」。**

## 0. 当前实测状态（2026-11-19 12:55）

```text
input engine: enabled=false mode="inline_hook" scheme="quanpin" candidates=7
input engine probe: typing "nihao" -> 你好 / 你 / 尼     ← 引擎逻辑正常
permissions: accessibility=false input-monitoring=false granted=false   ← 硬阻塞
native input method: installed=false
```

两个结论：

1. **引擎本身没问题**：self-check 的离屏探针能正确出「你好 / 你 / 尼」，说明 I1 之后的切分与候选链路在纯逻辑层是通的。
2. **一切交互类功能都还没被真实使用过**：Accessibility 与 Input Monitoring 都没授权 → CGEventTap 装不上 → 钩子不工作 → Leader 键、输入法、片段在真机上等于没接线。这不是 bug，是权限。

## 1. 前置：授权（只能人手做）

1. 打开 `build/darwin-arm64/Clavis.app`（首次会走 onboarding 的权限引导页）。
2. 系统设置 → 隐私与安全性 → **辅助功能** → 勾选 Clavis。
3. 系统设置 → 隐私与安全性 → **输入监控** → 勾选 Clavis。
4. 退出并重开 Clavis，然后自检：

```bash
./build/darwin-arm64/Clavis.app/Contents/MacOS/Clavis --self-check | grep permissions
```

**期望**：`accessibility=true input-monitoring=true granted=true`。

若重开后仍为 false：`tccutil reset Accessibility com.yeisme.clavis` 后重新授权（自签名证书不变则只需一次）。

## 2. 启用输入法

```bash
scripts/ime-test-prep.sh on      # 备份 config.toml 并置 [input_engine] enabled = true
```

然后重启 Clavis（配置热重载对 input_engine 会重建引擎并清空组字，但开关本身建议重启一次确认）。

自检确认：

```bash
./build/darwin-arm64/Clavis.app/Contents/MacOS/Clavis --self-check | grep "input engine:"
```

**期望**：`enabled=true mode="inline_hook" scheme="quanpin" candidates=7`。

**验收后必须还原**：`scripts/ime-test-prep.sh off`（默认值就是 off，不要让测试状态留在机器上）。

## 3. 测试用例（逐条打勾，失败项直接记下来）

前置约定：系统输入法切到**英文（ABC）**，下面所有输入都在这个前提下进行。

| # | 用例 | 操作 | 期望 | 结果 |
| --- | --- | --- | --- | --- |
| A1 | 基本中文输入 | TextEdit 里打 `nihao` | 出候选窗，首候选「你好」，空格上屏「你好」 | ☐ |
| A2 | 数字选词 | 打 `nihao` 后按 `3` | 上屏第 3 候选（不是「你好」） | ☐ |
| A3 | 翻页 | 打 `zhongguo` 后按 `=` | 候选翻到第 2 页，页码变化；`-` 翻回 | ☐ |
| A4 | **组字中打标点**（I1 修复项） | 打 `nihao` 后按 `,` | 先上屏「你好」再输出「,」；**不能出现标点先到、拼音后到或拼音变英文** | ☐ |
| A5 | 大写字母 | 打 `nihao` 后按 `Shift+A` | 输出「你好A」；**不能变成小写 a**（I1 删了 unicode.ToLower） | ☐ |
| A6 | **上下键移高亮**（I2 新增） | 打 `nihao` 后按 `↓` 两次再空格 | 高亮从第 1 项移到第 3 项，空格上屏的是第 3 项 | ☐ |
| A7 | **中英角标**（I2 新增） | 打 `nihao` | 候选窗出现「中」角标；按 Shift 切到英文后变「英」 | ☐ |
| A8 | 数字越界不吞键（I1 修复项） | 打 `nihao` 后按 `9`（候选只有 7 个） | 键应透传给应用（打出 `9`），不能静默丢失 | ☐ |
| A9 | Esc 丢弃 | 打 `nihao` 后按 Esc | 组字消失，不上屏任何字 | ☐ |
| A10 | 退格 | 打 `nihao` 后按 3 次退格 | 组字串变 `ni`，候选随之更新 | ☐ |
| A11 | Shift 切中英 | 打 `zhong` 后按 Shift，再打 `wen` | 切换后 `wen` 按英文字面上屏；再按 Shift 能切回中文 | ☐ |
| A12 | 四类应用连通 | Chrome / VSCode / Word / 飞书 各打一句「今天天气很好」 | 四次都正确上屏，无丢字、无乱序 | ☐ |
| A13 | 密码框不接管 | Chrome 打开任意登录页，点密码框打 `nihao` | 输出英文字面 `nihao`，不弹候选窗 | ☐ |
| A14 | 第三方中文输入法让位 | 切到系统「简体拼音」，打 `nihao` | Clavis **不接管**，由系统输入法出候选（不能双重组字） | ☐ |
| A15 | 一键退回 | `scripts/ime-test-prep.sh off` + 重启 | 打字恢复系统默认行为，无残留拦截 | ☐ |
| A16 | 候选窗定位 | 在屏幕四角与第二块屏（若接）分别呼出 | 候选窗贴光标下方，不出屏、不翻转异常 | ☐ |

## 4. 失败项的处置

每条失败都要能回答三件事，否则无法转成任务：

1. **复现步骤**（哪个应用、打了什么键）
2. **实际 vs 期望**（贴一句实际输出）
3. **是否与权限有关**（先看 `--self-check` 的 permissions 行）

失败项统一登记为 herdr 任务，标题前缀 `T-clavis-ime-`，并在 `docs/design/input-engine-completion.md` 的 DoD 表里标红。

## 5. 已知的、不算 bug 的行为（别误判）

| 现象 | 原因 | 定性 |
| --- | --- | --- |
| 组字串只显示在候选窗首行，不能在文本框内内联显示 | 路线 B 无 preedit，见 `own-input-engine.md` §1 | 设计边界 |
| 提权（管理员）窗口里打不出中文 | UIPI 阻止 SendInput | 设计边界 |
| 独占全屏游戏里无效 | 同上 + 浮窗不可见 | 设计边界 |
| 系统输入法切到中文时 Clavis 让位后打不出字 | 由第三方 IME 接管，正常 | 正常 |

## 6. 自动化能覆盖的部分（不需要权限，已在 CI 跑）

| 项 | 覆盖方式 |
| --- | --- |
| 切分/双拼/模糊音/候选排序 | `go test ./pkg/core/inputengine/...` |
| 判类（Rune 优先、标点走 KeyOther、跨平台一致） | `go test ./internal/bridge/...`（I1 新增表驱动 + 跨平台一致性用例） |
| 高亮移动/越界夹取/翻页归零 | `go test ./pkg/core/inputengine/...`（I2 新增） |
| 中英角标渲染与面板尺寸 | `go test ./uiapp/...`（I2 新增 sizer 用例） |
| 引擎离屏探针 | `Clavis --self-check` 的 `input engine probe` 行 |

**真正只能真机验的**：A4（标点顺序在真实应用里的表现）、A13（密码框）、A14（第三方 IME 仲裁）、A16（多屏定位）——这四条是 I5 存在的理由。
