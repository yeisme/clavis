# 词库统一架构（B2/B3/B4）

本方案定义 Clavis 的 starter、学习词库、自定义词库与 Unihan 单字库如何共同参与候选生成，以及学习数据在多设备之间的合并与导出边界。市场调研中的 Rime 同步事实见 [`ime-market-research.md`](ime-market-research.md) §2，本文件只记录对实现有约束力的契约。

## 1. 范围与术语

- **starter**：随二进制嵌入的 `data/starter.json`。`hanzi` 是只有顺序、没有读音的频序表，`words` 是短语列表。这个格式保持不变，不把 2370 个单字伪造拼音后塞进 Rime。
- **learned**：SQLite `ime_user_lexicon` 与 `ime_bigrams` 中由用户提交行为积累的词和二元转移。
- **custom**：用户维护的 `.dict.yaml` 或等价 Rime 文本词库，经编译器变成只读 `Lexicon`。
- **builtin**：由 Unihan 读音表生成的单字候选，作为最终兜底。
- **来源优先级**：`custom > learned > starter > builtin`。这是候选来源的语义优先级，不是磁盘写入顺序。

## 2. 数据流

```text
┌──────────────────────────┐
│ starter.json              │  //go:embed，发布时固定 digest
└─────────────┬────────────┘
              │ 启动时读入
              ▼
┌──────────────────────────┐       用户提交/学习      ┌────────────────────┐
│ learned                  │◄────────────────────────│ SQLite              │
│ ime_user_lexicon         │                         │ ime_user_lexicon   │
│ ime_bigrams              │                         │ ime_bigrams        │
└─────────────┬────────────┘                         └────────────────────┘
              │ 加载期合并、排名
              ▼
┌──────────────────────────┐       用户编辑/导入      ┌────────────────────┐
│ custom                   │◄────────────────────────│ *.dict.yaml         │
│ 编译后的 Lexicon          │                         └────────────────────┘
└─────────────┬────────────┘
              │ 无命中时兜底
              ▼
┌──────────────────────────┐
│ builtin                  │
│ Unihan 单字               │
└──────────────────────────┘
              │
              ▼
        候选去重与排序
```

### 2.1 各层写入、读取与失效

| 层 | 谁写 | 谁读 | 何时失效或重建 |
|---|---|---|---|
| starter | 发布构建流程生成 `data/starter.json`，运行时只读 | `StarterDictionary` 与引擎初始化 | 嵌入字节的 SHA-256 digest、数据版本或二进制版本变化时重建。digest 不匹配是构建完整性错误，应拒绝启动。 |
| learned | 输入引擎提交路径写 SQLite，学习导入器写入合并后的快照 | 学习存储热镜像、候选排序器、同步读取器 | 每次提交、导入或删除后更新热镜像。SQLite schema 版本变化时迁移并重载。达到 `MaxLexMirrorEntries` 等容量上限时只淘汰未 pinned 的低分条目。 |
| custom | 用户、设置页或导入操作写 `.dict.yaml` | `CompileRimeDictFiles`、编译缓存和 `NewIMEDictionary` | 文件路径、大小、mtime 或 `dictCompilerVersion` 改变时缓存失效。解析规则变化必须 bump `dictCompilerVersion`。损坏文件不产生部分词库。 |
| builtin | 随引擎发布的 Unihan 数据生成，运行时不写 | `UnihanDictionary` | 仅随 Unihan 数据或引擎版本更新重建。单次查询不做 I/O。 |

`starter` 的单字频序只影响 builtin/单字候选的初始排序，不能被误解释为带有拼音的 Rime 词条。custom、learned 和 starter 的短语记录可以参与同一候选池，最终由来源优先级和同源排序规则裁决。

## 3. 四级优先级与裁决

### 3.1 语义

同一 `(pinyin, word)` 从多个来源出现时，先比较来源等级。等级高者保留，低等级记录只作为审计来源，不重复显示。custom 中的显式词条因此会覆盖 learned、starter 的同词条，即使 learned 已 pinned。learned 覆盖 starter，starter 覆盖 builtin。

同一来源内出现多个记录时，比较顺序固定为：

1. `pinned=true` 优先。
2. `effectiveFrequency` 高者优先。
3. 原始词库中的相对顺序优先，保持用户文件的可预测性。
4. `(pinyin, word)` 的 UTF-8 字节序作为最终稳定 tie-breaker。

custom 的 `weight` 先转换成 `effectiveFrequency`，但不能越过来源等级。learned 的 `frequency` 来自学习合并，starter 的顺序来自 starter 数据，不把无意义的伪拼音权重引入。

### 3.2 Go 接口契约

```go
// DictionarySource identifies the origin of a candidate. The numeric order is
// intentionally not used for comparison outside SourcePriority.
type DictionarySource uint8

const (
	SourceCustom DictionarySource = iota
	SourceLearned
	SourceStarter
	SourceBuiltin
)

// CandidateRecord is the canonical candidate before presentation formatting.
type CandidateRecord struct {
	Pinyin            string
	Word              string
	Source            DictionarySource
	Frequency         uint64
	Pinned            bool
	OriginalOrdinal   uint32
}

// ResolveCandidate applies custom > learned > starter > builtin. For equal
// sources it applies pinned, effective frequency, original ordinal, then UTF-8
// byte order. It emits at most one record for each (Pinyin, Word) pair.
func ResolveCandidate(records []CandidateRecord) []CandidateRecord
```

`ResolveCandidate` 必须在 lookup 阶段完成去重，展示层不得再次按显示文字去重，否则同字不同读音会被错误合并。`OriginalOrdinal` 只在同一来源内有效，跨来源裁决永远先看来源等级。

## 4. MergeLearning 契约（B4）

### 4.1 函数签名与输入键

```go
// MergeLearning returns the deterministic union of two learning snapshots.
// Word keys are (KeyDigest, WordDigest); bigram keys are
// (PreviousDigest, WordDigest). Pinned is merged with logical OR. The result
// contains no duplicate key and is sorted by its stable digest key.
//
// For every unpinned key, the merged score is compressed after summing the two
// input scores. Pinned entries bypass compression and retain the exact checked
// sum. Callers must supply frequencies whose checked sum fits uint64.
func MergeLearning(a, b LearningData) LearningData
```

`Words`、`Bigrams` 和 `Aggregates` 分别按各自的键合并。相同键的 `Pinned` 做 OR，`Bigrams` 没有 pinned 字段。`Enabled` 采用 OR，`SummaryOnly` 采用 AND 只有在两边都是 summary 时才为 true，`RecoveryObjectRef` 只保留双方相同的非空值，否则清空并要求恢复层重新选择对象。

### 4.2 频率压缩

学习存储中的频率用于排序，不是审计账本。对未 pinned 项，先做 `sum = a.Frequency + b.Frequency`，再计算：

```text
score(0) = 0
score(n) = min(4096, round(1024 * log1p(n)))  (n > 0)
```

实现可以用等价的整数查表，不能因浮点平台差异产生不同结果。`Bigrams` 和 `Aggregates` 使用同一规则。`pinned=true` 的项绕过该压缩，`Frequency` 保留 checked sum，且在镜像淘汰、导出截断和容量修剪时不可被截断或淘汰。输入频率的 checked sum 溢出属于上层数据校验错误，不能静默变成一个较小频率。

必须压缩是因为简单求和会让单侧旧历史完全支配另一台设备的新历史。一个设备长期使用得到的大数会让另一侧刚学到的新词永远上不来，继续同步只会放大这种偏置。对数压缩保留相对强弱，又让新词在多轮合并后仍有可见的上升空间。

压缩结果是可再次合并的排名分数，读取期合并必须满足交换性和确定性。幂等性由同步元数据层以快照 ID 去重保证，同一快照不能被重复导入两次。`MergeLearning` 本身不负责识别重复快照，调用方必须先完成去重。

### 4.3 读取期合并

磁盘不写单一全局词库。每台设备在共享 `sync_dir` 中写自己的 `<installation_id>.userdb.txt` 文件，例如：

```text
mini.userdb.txt
windows.userdb.txt
```

设备标识必须是用户可读且稳定的安装标识，推荐 `mini`、`windows` 这样的名称，允许用户在设置页修改。每台设备只原子写自己的文件，不覆盖其他设备。读取时扫描已知设备文件，解析成 `LearningData`，调用 `MergeLearning` 生成内存视图。写回时只写本设备的新快照，避免两台设备同时写同一文件造成冲突。

该形态与 Rime 已确认的共享目录行为一致。同步目录只承载用户学习快照，starter、Unihan 和词库定义仍由各版本二进制或用户配置管理。来源设备可保留在读取期元数据中供设置页审计，但不参与候选排序。

## 5. Rime 导出与导入的 round-trip 不变量

导出格式采用 UTF-8 文本，每条记录为：

```text
word<TAB>pinyin<TAB>weight<LF>
```

### 5.1 必须逐字节保持

- `word` 字段的 UTF-8 字节序列必须逐字节相同，不能做 Unicode 归一化、大小写变换或去空白。
- `pinyin` 字段的 UTF-8 字节序列必须逐字节相同。规范化只允许发生在导出前的 canonicalization 阶段，canonical 输入再次导入后必须原样导出。
- 记录的相对顺序必须保持。排序器可以在候选展示时使用权重，但导出器不能按权重重排用户文件。
- 空词、缺读音或无法表示为规范字段的记录不进入导出，并产生诊断，不得用假拼音补齐。

### 5.2 允许归一化

`weight` 可以从浮点、百分号或整数形式归一化为规范十进制整数。权重的精确文本形式不属于 round-trip 不变量，只要求数值映射稳定并可用于同源排序。编译规则或字段解析改变时必须递增 `dictCompilerVersion`，防止旧缓存伪装成新解析结果。

## 6. 自定义词库失败模式与诊断

当前 `LoadDictionary` 在自定义文件损坏、缺字段、不可读或编译失败时静默回退 `defaultDictionary`。设计改为可观察的降级：

```go
// DictionaryDiagnostic explains why one custom source was not used. Kind is a
// stable machine-readable value such as unreadable, malformed, missing_field,
// compiler_error, or empty. Diagnostics contain no file contents.
type DictionaryDiagnostic struct {
	Path     string
	Kind     string
	Message  string
	Fallback string // starter+builtin
}

// LoadDictionaryWithDiagnostics returns a complete dictionary and all custom
// source diagnostics. A bad custom file is skipped as a whole, then the
// starter+builtin chain remains available. It never returns a partial custom
// dictionary and never blocks normal startup on a user file.
func LoadDictionaryWithDiagnostics(
	customPaths []string,
	cacheDir string,
	report func(DictionaryDiagnostic),
) (Dictionary, error)
```

`report` 为空时仍保留内部日志或诊断缓存，设置页可以在下一次打开时显示“已回退到 starter + builtin”。不可读文件、YAML 语法错误、缺 `word`/`pinyin` 字段、空词库和缓存损坏分别给稳定 `Kind`。一份文件失败不会阻止其他自定义文件加载，但同一文件不允许只加载成功的前半部分。所有 custom 都失败时，结果仍为 `ChainDictionaries(StarterDictionary(), UnihanDictionary())`。

## 7. 测试策略

每条不变量都必须有独立测试名和可断言结果。测试不依赖真实用户目录，使用临时目录和内存 fake 文件系统。

| 测试名 | 断言要点 |
|---|---|
| `TestDictionaryPriorityCustomLearnedStarterBuiltin` | 同一 `(pinyin,word)` 同时来自四层时只发出 custom，依次移除后依次得到 learned、starter、builtin。 |
| `TestDictionaryTieBreakPinnedFrequencyOrdinalBytes` | 同源记录按 pinned、频率、原始顺序、UTF-8 字节序稳定排序，重复 lookup 结果相同。 |
| `TestMergeLearningSumsAndCompressesUnpinned` | 相同键先求和再按 `min(4096, round(1024*log1p(n)))` 得到分数，两个输入顺序交换结果字节相同。 |
| `TestMergeLearningPinnedNeverCompressedOrEvicted` | pinned 项保留 checked sum，超过镜像容量时仍存在，未 pinned 低分项才淘汰。 |
| `TestMergeLearningUnionHasNoDuplicateKeys` | Words、Bigrams、Aggregates 各自无重复键，`Pinned` 是 OR，结果按 digest 排序。 |
| `TestLearningReadMergeKeepsPerDeviceFiles` | `mini.userdb.txt` 与 `windows.userdb.txt` 并存，各自写入不覆盖另一份，读取期结果等于 `MergeLearning`。 |
| `TestRimeRoundTripPreservesWordPinyinAndOrderBytes` | canonical `.dict.yaml` 导出再导入，`word`、`pinyin` 和相对顺序的 UTF-8 bytes 完全相等。 |
| `TestRimeRoundTripNormalizesWeightOnly` | `12.0%`、`12` 等权重形式得到同一数值排序，除 weight 外字段不变。 |
| `TestRimeCompilerCacheInvalidatesOnVersionChange` | 只修改 `dictCompilerVersion` 就不能命中旧 `.clvdict`，并使用新解析规则。 |
| `TestLoadDictionaryDiagnosticsFallsBackWithoutPartialCustom` | 损坏、缺字段、不可读和空文件分别产生稳定 Kind，结果不含该文件的半成品且包含 starter+builtin。 |
| `TestLoadDictionaryReportsAllFailedCustomSources` | 多个 custom 同时失败时收到每个路径的诊断，启动仍返回可查询字典。 |
| `TestStarterFormatAndDigestRemainUnchanged` | 继续通过现有 `TestStarterDataMatchesRecordedFingerprint`，不把单字伪拼音写入 starter。 |

## 8. 关键取舍与否掉的替代方案

- 保留 `starter.json` 原格式，因为 `hanzi` 没有逐字读音，伪造 Rime 拼音会让单字数据失真。
- 选择“每设备一份、读取期合并”，因为共享文件的并发写入会产生冲突，Rime 的已确认形态也避免了这一点。
- 选择来源优先级先于频率，因为用户明确维护的 custom 词条必须能覆盖旧学习结果。
- 选择对数压缩，因为纯求和会让单侧旧历史长期支配另一侧的新词。
- 选择 pinned 绕过压缩和淘汰，因为显式固定是用户意图，不能被自动统计策略撤销。
- 选择完整文件失败后整文件降级，因为部分导入会制造不可审计的半个词库。
- 否掉单一 `merged.userdb.txt`，因为两设备同写必然冲突。
- 否掉把 starter、learned、custom 全部编成一个不可分层 `.clvdict`，因为无法保留来源优先级、诊断和用户可审计性。
- 否掉把 weight 当跨来源绝对分数，因为任意 custom 权重会错误压过 pinned learned 或明确来源等级。

## 9. 遗留待拍板问题

1. 设备文件名是否由用户手工命名，还是首次启动自动生成 `mini`、`windows` 并允许之后修改。
2. 同一设备重装后是否沿用旧设备标识，还是生成新标识并由用户选择合并。
3. 导出文件是否包含来源设备注释。当前建议不写入词条正文，只放 sidecar 元数据。
4. pinned 频率的最大可表示值和同步快照 ID 的去重存储位置。
5. 诊断应显示在设置页、日志还是两者同时显示，以及错误消息的本地化词表。
6. custom 中同一词不同 pinyin 是否允许同时导出，当前建议允许并按完整 `(pinyin,word)` 键区分。
7. 读取期发现未知设备文件时，是忽略、提示用户，还是放入待批准设备列表。
