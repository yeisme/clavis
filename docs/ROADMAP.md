# Clavis 推进路线（怎么把这条线做完）

基线 HEAD `21ac2db`（2026-11-19）。三条设计线：
[模板平台](design/template-platform.md) · [输入法完成](design/input-engine-completion.md) · [多设备](design/multi-device.md)。

## 0. 一句话结论

**功能已经够了，缺的是三条线各自的「最后一公里」加一堆没人关的账。**

- 引擎、模板、同步协议都不是从零开始——每一块都有可用的代码骨架（有的比预期完整得多，见各设计文档的事实基线）。
- 真正阻断的是三件事：输入法在 macOS 上的 5 处差集、模板根本没有远端导入能力、同步的加密模型是单向的。
- 另有 13 个 `.pi/goals` 里 8 个是 0 token 空占位、`docs/` 多篇基线停在 `e7c54fe`/`458d7fe`——**账不清，每轮盘点都会被假未完成项污染**。

所以路线第一波不是写功能，是**先止血再推进**（详见 §2 Wave 0）。

## 1. 「完成」的定义

不同的人对 Clavis「完成」的定义差很远，先把三档写死，避免永远做不完：

| 档 | 定义 | 可验收 |
| --- | --- | --- |
| **可用（1.0）** | 主人在 mini 上把它当日常启动器 + 中文输入主力（第二输入法），模板能从官方目录导入并一键填槽，Windows 侧不是主力但能拉起来跑 | 本文件 §3 的 Wave 0–2 全部机器断言绿 + 一次两平台手测记录 |
| **好用（1.1）** | 多设备真的能同步（片段/词库/模板清单）、冲突有界面、Windows parity 🟡 收完 | Wave 3 绿 |
| **能做产品（2.0）** | 签名安装包、自动更新、外部用户能自己配起来 | 不在本路线内（见 §6） |

**本路线只承诺到「好用」。** 2.0 的三件事（签名、更新器、引导）与当前自用定位不匹配，现在做等于给自己造维护负担。

## 2. 四波推进

### Wave 0：止血（约 6h，必做，不可并行）

| 任务 | 内容 | 验收 |
| --- | --- | --- |
| W0-1 | `.pi/goals` 清账：13 个文件里 8 个是 0 token 空占位、1 个 paused 带 3.3M token | `ls .pi/goals/active_goal_*.md \| wc -l` 降到「真实在办数」；每个保留项 `revision>0` |
| W0-2 | `docs/` 基线刷新：`own-input-engine.md`（458d7fe）、`windows-parity-audit.md`（e7c54fe）、`hook-robustness.md`、`media-preview.md` 全部落后 HEAD 10+ 个提交 | 每篇顶部写「基线 <sha>」，且审计结论能 grep 到符号 |
| W0-3 | README 补缺：目录树缺 `cmd/clavis-imk/`、`internal/daemon/`；`[input_engine]` 段缺失；输入法定位一句话（第二输入法，见 input-engine-completion §3.1） | `grep -q input_engine README.md` 等 3 条全命中 |
| W0-4 | 删假配置：`cmd/clavis-imk` 的 Info.plist 声明 `ClavisIMKController` 但无实现类 | 打包清单不再声明 IMK；或 binary 摘出 |
| W0-5 | herdr 台账对账：`T-clavis-ai-select` 自 11-15 挂 working（实际已合入）、`T-clavis-windows-beat` 卡 owner | 两个任务关闭或重新定状态，不留僵尸 |

W0 不并行：它要改同一批文件（README/docs/goals），并行只会制造 P1 冲突。

### Wave 1：输入法可用 + 模板能吃（约 40h，两线并行）

| 线 | 任务 | 估时 | 归属 |
| --- | --- | --- | --- |
| A | I1 键语义下沉 + darwin 标点通路 + 数字越界不吞 | 10h | pi（backend） |
| A | I2 高亮移动 + 中英指示 + 一键退回断言 | 8h | pi（backend，接 I1） |
| A | I3 收敛假配置（`mode` 白名单 / 混合英文接线） | 6h | pi（backend） |
| B | T1 catalog 读取 + digest 复核 + CAS | 6h | pi（backend，另一面板） |
| B | T2 合并视图 + 冲突告警 + 诊断 | 5h | pi（backend，接 T1） |
| B | T3 合同 → 槽位 + 校验器 | 8h | pi（backend，接 T2） |

并行约束（血换来的，见 herdr `COORDINATION.md` P1/P2/P4）：**同一 git 工作树同时只允许 1 个会话处于提交状态，活跃写会话 ≤2**。所以 A/B 两线必须**路径不重叠**：

- 线 A 只碰：`pkg/core/inputengine/`、`internal/bridge/input_engine*`、`uiapp/input_*`、`uiapp/settings_input_engine.go`
- 线 B 只碰：`pkg/core/template/`（新建）、`pkg/core/config/prompts_config.go`、`pkg/core/prompt/`

`uiapp/settings_view.go` 是**已知撞车点**（B 的模板库页与 C 的设备页都要加 tab）——Wave 1 里谁都不许动它，留到 Wave 3 串行处理。

### Wave 2：模板可用 + 输入法收口 + 真机（约 48h）

| 线 | 任务 | 估时 | 归属 |
| --- | --- | --- | --- |
| A | I4 darwin 重置/snippet 对齐 + `prevWord` 单源 | 6h | pi |
| A | I5 真机验证批次（四类 app × 两平台） | 8h | **owner 真机时间**（不可外包给面板） |
| B | T4 fail-closed 渲染 + 权限门 | 6h | pi |
| B | T5 设置页「模板库」（含 `settings_view.go` 改造） | 12h | codex（frontend） |
| D | windows-parity 🟡 收口（ProbeSelection / ActiveURL / hover / secure-input 守卫） | 12h | pi |
| D | 打包脚本双包 + 体积预算 | 4h | codex |

Wave 2 的关键：**I5 必须单独排期**。它是唯一不能靠单测替代的批次，混进代码批次就会永远被「等有 Windows 机器时再说」拖住。

### Wave 3：多设备（约 56h，串行为主）

| 批 | 任务 | 估时 | 依赖 |
| --- | --- | --- | --- |
| S1 | 密码学 v2：ADK + ECDH 包裹 + 恢复码封装 ADK | 12h | 无（可早开工，但无可见收益，放这里） |
| S2 | 身份落盘（keystore：`internal/secrets` 已有 `SaveKey/ReadKey`） | 6h | S1 |
| S3 | `Client` 改吃 `SyncStore` 接口 + `Pull` + rebase 冲突 | 10h | S2 |
| S4 | 传输：TCP/TLS + 指纹 pin + UDP beacon | 14h | S3 |
| S5 | 配对：短码 + 二维码 + 授权票 + `RemoveDevice` | 10h | S4 |
| S6 | DataClass 扩展 + 协议 v2 迁移 + 隐私过滤 | 8h | S3 |
| S7 | 设置页「设备」+ 冲突 UI + 审计 + i18n | 12h | S5/S6 + 与 T5 串行改 `settings_view.go` |

**S1 必须在 S4 之前**，这是本路线最重要的排序：现协议用「发送方私钥」当 AES 密钥（`sync.go:910`），接收方物理上解不开。先做传输等于白做。

另外 S7 与 T5 都改 `uapp/settings_view.go`：**串行**，不并行。

## 3. 关键路径与并行度

```text
W0（6h，独占）
 └─ Wave 1  A: I1→I2→I3        B: T1→T2→T3          （两线并行，路径隔离）
     └─ Wave 2  A: I4→I5(真机)  B: T4→T5(codex)  D: parity+打包
         └─ Wave 3  S1→S2→S3→S4→S5→S6→S7（基本串行，S7 与 T5 互斥）
```

可并行的只有 Wave 1 的两条线，以及 Wave 2 的 A/B/D 三条线（D 只碰 `internal/bridge` 的 Windows 侧与 `scripts/`，与 A 的 darwin 侧文件不重叠，但同属 `internal/bridge` 包——**必须按文件分配，不能按目录分配**）。

合计约 **150h**。按「两个面板并行、每天 6 有效小时」算 ≈ 13 个工作日；按一个面板算 ≈ 25 天。

## 4. herdr 任务表（可直接 seed）

派发前必须满足：`accept` 有机器断言（P14）；同仓无锁（P1）；目标面板用 magpie 模型（cpa 通道 2026-11-19 已 403，见记忆）。前端/文档走 codex，后端走 pi。

| 任务 ID | lane | 目标 | 机器验收 |
| --- | --- | --- | --- |
| `T-clavis-w0-ledger` | docs | 清 `.pi/goals` + 刷新 4 篇文档基线 + README 补缺 | `gi t_sha` + `cmd: test $(ls .pi/goals/active_goal_*.md \| wc -l) -lt 13` + `cmd: grep -q input_engine README.md` |
| `T-clavis-ime-i1` | backend | 键语义下沉 + darwin 标点 + 数字越界 | `git_sha` + `cmd: go test ./pkg/core/inputengine/... ./internal/bridge/...` |
| `T-clavis-ime-i2` | backend | 高亮移动 + 中英指示 | `git_sha` + `cmd: go test ./uiapp/...` |
| `T-clavis-ime-i3` | backend | 收敛 `mode` + 删假 IMK 声明 | `git_sha` + `cmd: go test ./...` |
| `T-clavis-tpl-t1` | backend | catalog + digest + CAS | `git_sha` + `cmd: go test ./pkg/core/template/...` |
| `T-clavis-tpl-t2` | backend | 合并视图 + 诊断 | 同上 |
| `T-clavis-tpl-t3` | backend | 合同映射 + 校验器 | 同上 |
| `T-clavis-tpl-t4` | backend | fail-closed + 权限门 | `cmd: go test ./pkg/core/template/... ./pkg/core/prompt/...` |
| `T-clavis-tpl-t5` | frontend | 设置页模板库 | `git_sha` + `cmd: go test ./uiapp/...` + `screenshot` |
| `T-clavis-sync-s1` | backend | 密码学 v2 | `cmd: go test ./pkg/core/sync/...` 且新增「双设备互解」用例 |
| `T-clavis-sync-s2` | backend | 身份落盘 | `cmd: go test ./pkg/core/sync/... ./internal/secrets/...` |
| `T-clavis-sync-s3` | backend | Pull + rebase | 同上 + 冲突用例 |
| `T-clavis-sync-s4` | backend | 传输层 | `cmd: go test ./pkg/core/sync/...` + 两进程互转集成测试 |
| `T-clavis-parity` | backend | Windows 🟡 收口 | `cmd: go vet ./... && GOOS=windows go vet ./...` + 真机截图 |

统一铁律（沿用 herdr `build_prompt`）：只逐路径 `git add`、禁 `git add -A`、提交后立刻回报 sha 并让出工作树、不碰别人未提交的改动。

## 5. 需要主人拍板的三件事（不拍板就卡住）

| # | 事项 | 我的建议 | 不拍板的后果 |
| --- | --- | --- | --- |
| G-A | **Windows 真机时间**：I5 与 parity 的验收都要 Windows 手测 | 每周固定一个 2h 窗口，主人开 Windows 机器跑一遍清单，我出脚本与记录表 | I5 永远挂起，输入法只能在 macOS 自称可用 |
| G-B | **IMK / TSF 做不做**（C1/C2，4–8 人周） | **本轮不做**，把「第二输入法」定位写进 README。理由见 input-engine-completion §2 | 不做则内联组字永远没有；做则这条线多两个月 |
| G-C | **多设备值不值 80h** | **押注：值，但排在模板之后**。理由：词库与片段跨设备是真实痛点（Windows 与 mac 各学一遍词很蠢）；但它的可见收益在 S4 之后，先做会有一段时间「烧钱看不到东西」 | 不做则 Windows 与 mac 的学习数据永远各自为政 |

已定不需要再问的：模板线方向（消费 promptrepo 目录，不自建市场）、输入法定位（第二输入法）、同步冲突策略（都保留而非 LWW）——这三条已写进设计文档，主人有异议再改。

## 6. 明确不做（避免范围蔓延）

- **签名安装包 / 自动更新 / 外部用户引导**（2.0）：自用定位下是纯维护负担。真要发，先解决签名证书与 notarization 的账号问题，那是另一条线。
- **模板市场 / 发布端**：发布留在 `template-registry` + git，Clavis 只消费。
- **通用 agent 编排**：recipe >3 步交给 herdr（见 template-platform §6）。
- **IMK / TSF 宿主**：本轮不做（G-B）。
- **团队共享 / 云账号**：多设备只做单人多设备（multi-device §12）。
- **CRDT / 实时协同**：不做（multi-device §12）。

## 7. 风险

| 风险 | 判断 |
| --- | --- |
| **最大的风险不是技术，是没人关账** | 13 个 goal 里 8 个空占位、parity 文档落后 10+ 提交，这不是偶然，是流程问题。W0 之后必须建立「任务完成即改状态」的纪律，否则 Wave 1 结束又攒一批假未完成 |
| 两线并行撞同一文件 | `uiapp/settings_view.go` 是已知撞车点，Wave 1 禁改；Wave 2/3 串行。按目录分配会失败，必须按文件分配 |
| 多设备 S1 把既有测试打红 | `sync_test.go` 23 个用例全部围绕 v1 语义，S1 会大面积改签名与密钥派生 → 预留一天专门修测试，不要指望「顺手绿」 |
| 真机验证被无限推迟 | I5/parity 单独成任务、单独排期、指派到 owner 而不是面板 |
| 成本 | 150h × 面板单价，按 herdr P6「单面板 >$50 必须回报」，每波结束对一次账；Wave 3 是最大不确定项（80h 里 12h 是重写密码学） |

## 8. 下一步动作（现在就能做的三件）

1. **seed herdr 任务**（§4 前 9 条），`open` 状态不派发，等主人拍 G-A/G-C。
2. **跑 W0**（6h，纯收尾，不需要拍板）——这是唯一可以立刻开工且不花钱的批次。
3. **把三份设计文档与这份路线提交并 push**（P10：不 push 等于 Windows 看不见）。
