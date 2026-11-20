# 后端能力完善路线图

2026-11-20。回答「后端和各项能力支持如何继续完善」。
每条结论都指到代码位置，不凭印象。

## 0. 先纠正一个认知：有些能力已经做好了，缺的是入口

我原本以为同步线要从零做。实际盘点：

| 能力 | 真实状态 | 证据 |
|---|---|---|
| **同步引擎** | **已实现且测试通过**：AES-GCM 信封 + Ed25519 签名、tombstone 删除、游标、并发序列化、Vault、Client/Server | `pkg/core/sync/` 2119 行，**24 个测试全过**；`NewEncryptedEnvelope` / `NewClient` / `NewServer` / `NewVault` |
| **同步的入口** | **完全没有** | `main.go` 未接入（唯一命中的 `sync.New` 是标准库 `sync.NewCond`）；`cmd/clavis` 无 sync 子命令；设置页无同步面板 |
| **性能基准** | 已有 | `BenchmarkEngineKeystrokeQuanpin`、`BenchmarkFrameCandidate`、`BenchmarkComposerKeystroke` |
| **词库** | 45921 词内嵌 + blocklist 22 条 + Rime 编译器 | `data/common.clvdict` 2.77MB |
| **学习数据** | `ime_user_lexicon`（含 pinned）+ `ime_bigrams` + `usage_frecency` | `internal/storage/learning_store.go` |

**结论：后端最大的浪费不是缺功能，而是「做了一半、没有出口」。** 同步引擎 2119 行代码在仓库里躺着，
没有任何用户能用到它。这比从零做一个新功能更亏。

## 1. P0：把已有的能力接上出口（优先级最高）

### 1.1 同步：从库变成功能（估 16h）

现状是「引擎完整、零入口」。要做的不是写同步，是**接线**：

1. **CLI 子命令** `clavis sync init|push|pull|status`——最小可用面，先让人能用
2. **传输**：现有 `NewClient` 需要 host 抽象；先做两种后端
   - 本地目录（含 iCloud/Dropbox 同步盘）——零服务端，学 Rime 的做法
   - HTTP（`NewServer` 已存在，起一个单用户服务）
3. **配对**：设备 ID 生成 + 公钥交换（扫码或粘贴一段文本）
4. **设置页**：「同步」卡片显示 设备 ID / 上次同步时间 / 待上传条数 / 冲突数
5. **MergeLearning**（见 §2.1）——同步真正联通后立刻需要它

**验收**：两台机器（本机 + s101 或第二台 Mac）改同一个词的频率 → sync 后两边合并结果一致。

### 1.2 词库热更新（估 8h）

现在词库是 `//go:embed` 进二进制的，改一个词要重新编译发布整个 app。这是不可持续的。

改法：**用户目录优先，内嵌兜底**
```text
~/.config/clavis/dicts/*.clvdict    ← 优先加载（可热更新、用户可放自己的）
（内嵌 common.clvdict）              ← 兜底
```
- 目录被 watcher 监视（`internal/watcher` 已有）
- 加载失败回退内嵌，并给诊断

**验收**：往目录丢一个 clvdict，不重启就能生效；删掉后回退到内嵌。

### 1.3 Windows 实测（估 8h，含环境）

`internal/bridge/bridge_windows.go` 有 700+ 行，但**一个 `_windows_test` 都没有**。
这不是「测试覆盖不足」，是「这条路径从来没在真机上跑过」。

- 需要一台 Windows（或虚拟机）
- 至少要跑通：按键拦截、候选窗显示、注入上屏、IME 接管

**风险**：如果没这台机器，这条线只能挂着。**我的判断：先不做，如实标注不支持**，别让 UI 假装支持。

## 2. P1：真正缺失的能力

### 2.1 MergeLearning（估 12h）

`pkg/core/inputmigration` 有 `LearningData{Words,Bigrams,Aggregates}`、
`PersonalBaseline`、`DeviceOverlay`，**但没有合并函数**。

契约（已在 `docs/design/ime-dictionary-architecture.md` 定过）：
```go
func MergeLearning(a, b LearningData) LearningData
```
- frequency 求和后**必须压缩**：`min(4096, round(1024*log1p(n)))`，否则一侧历史会永久支配另一侧
- pinned 取 OR，永不压缩、永不淘汰
- 结果按 digest 稳定排序（交换性：`Merge(a,b) == Merge(b,a)` 字节相同）

**验收**：文档里那三个具名测试（交换性 / pinned 保护 / 无重复键）。

### 2.2 bigram 参与候选生成（估 10h）

`ime_bigrams` 表有数据、`LearningWriter` 在记录，但**只用于排序加权**（`rankTierByUserBoost`），
不参与生成。这是「上下文关联」的正解——零网络、零隐私代价。

```go
// 上一词之后高频出现的词，直接进候选池
func (e *Engine) bigramCompose(prevWord string, segs []string) []Candidate
```
冷启动（无数据）行为与今天完全一致，不猜。

**验收**：连续输入「我」→「的」这类高频搭配时，第二段候选里出现 bigram 命中的词且排前列；
无 bigram 数据时输出逐字节不变。

### 2.3 词库内容质量（估 6h）

现状：`blocklist.txt` 只有 22 条（纯性/解剖词），`yijing` 仍可能出「遗精」类词。
权重区间是启发式，不是语义筛选。

三层补齐：
1. **构建期黑名单扩展**：扫一遍 `[20000,600000)` 区间内的词，补同类词
2. **用户屏蔽持久化**：候选上能「屏蔽这个词」，存本地，优先级最高
3. **首页质量回归扩到 200 词**：现在只有 40 词（门槛 39/40）

**验收**：200 词首页命中 ≥180；屏蔽词重启后仍生效。

### 2.4 AI 续写补完（估 8h）

现在只插入第 1 条，另 2 条只在 HUD 里报个数——等于做了一半。

- 三条列出来让用户选（复用候选窗或 omnibox 列表）
- 选中后插入，5 秒退格撤销（`snippet_undo` 已有）
- 记住上次选择，同类场景优先那条

### 2.5 本地模型路由（估 10h）

`ai.IsKeylessAllowed` 已允许 localhost 无 key，但没有：
- 启动探测（`/api/tags`，2s 超时）
- profile 级超时（远端 1.5s / 本地 4s，上限 10s）
- 未就绪时零请求（不是发了再超时）

**验收**：未装 Ollama 时零报错零阻塞；装了之后本地 profile 生效且不发远端请求。

## 3. P2：架构债（不紧急但会越来越贵）

| 债 | 现状 | 建议 |
|---|---|---|
| SQLite 无 schema 版本 | `learning_store.go` 建表用 `CREATE TABLE IF NOT EXISTS`，改结构无迁移 | 加 `PRAGMA user_version` + 迁移函数 |
| 学习数据无备份/导出 | 只在 App Support 里 | 导出 JSON/Rime；设置页「导出我的数据」 |
| 候选质量无埋点 | 不知道首页命中率 | 记录「首个候选被选中 / 翻页后选中」计数，本地统计 |
| 诊断信息分散 | `--self-check` + 设置页零散 | 一个诊断页：引擎状态/词库版本/学习条数/同步状态/延迟 |
| 输入延迟无回归门槛 | 有 benchmark 但无断言 | CI 式断言：候选窗帧时 ≤ 主界面 ×1.2 |

## 4. 批次与依赖

| 批 | 内容 | 估时 | 依赖 |
|---|---|---|---|
| B1 | 词库热更新 | 8h | — |
| B2 | MergeLearning | 12h | — |
| B3 | 同步 CLI（本地目录后端） | 12h | B2 |
| B4 | 同步设置页 + 配对 | 8h | B3 |
| B5 | bigram 生成 | 10h | — |
| B6 | 词库质量三层 | 6h | — |
| B7 | AI 续写三选一 | 8h | — |
| B8 | 本地模型路由 | 10h | — |
| B9 | 架构债（schema 版本 + 导出 + 埋点 + 诊断页） | 14h | — |

**并行分组**（文件不重叠）：
- 线 A：B1 + B6（词库相关）
- 线 B：B2 + B3 + B4（同步/学习，串行）
- 线 C：B5 + B7 + B8（引擎与 AI）
- 线 D：B9（存储与诊断）

## 5. 我的判断：先做哪三个

1. **B1 词库热更新**——现在改一个词要发一版 app，这是最影响迭代速度的债
2. **B2 MergeLearning**——12h 换来「同步能用」，且它是同步的前置（没有它，两台机器的词频无法合）
3. **B5 bigram 生成**——零网络、零隐私代价，直接提升「打一半就能猜到下一词」的体验

**不押**：B4 配对 UI（要服务端和跨机测试，投入产出比暂时低）、Windows 实测（没机器）。

## 6. 明确不做

- 不做语言模型 rerank（n-gram LM / 神经网络）：超出范围，词库 + bigram 够用
- 不做云输入法联想（与本地优先定位冲突）
- 不在钩子线程做任何 I/O（铁律）
- 不让同步阻塞输入（后台线程 + 失败静默）
- 不做多用户服务端（单人自用，`NewServer` 够）
