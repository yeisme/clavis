# 词库统一架构（B2 / B3 / B4）

状态：设计稿，供实现批次使用。本文不改变 `pkg/core/inputengine/lexicon.go` 中已经锁定的 starter 格式，也不把实现细节伪装成已经落地的能力。市场背景引用 [`ime-market-research.md`](ime-market-research.md) §2；本文只写实现契约、边界和验收。

## 1. 目标、范围与不变量

本方案统一四个候选来源：

1. `custom`：用户维护的 `.dict.yaml`，经 Rime 编译器生成只读 `Lexicon`。
2. `learned`：SQLite 中的 `ime_user_lexicon` 与 `ime_bigrams`，由用户提交和学习流程写入。
3. `starter`：随二进制嵌入的 `data/starter.json`，目前由 `//go:embed` 加载并由 digest/fingerprint 锁定。
4. `builtin`：Unihan 读音表生成的单字兜底。

必须保持以下不变量：

- lookup 只访问已经构建好的内存索引；按键热路径不做磁盘 I/O、SQLite I/O 或编译。
- `starter.json` 的 `hanzi` 只有频序、没有逐字读音；不得为 2370 个单字制造假拼音后导入 Rime。
- 同一个 `(pinyin, word)` 最终只产生一个展示候选，但来源与裁决原因可供诊断和审计。
- 任一 custom 文件失败都不能让输入法不可用；降级必须可观察，且不能使用失败文件的部分结果。
- 多设备学习数据磁盘上各自独占文件，读取期才合并；禁止共享一个可并发写的总文件。

## 2. 数据流

```text
发布构建：data/starter.json --embed + sha256/fingerprint
                         │ 启动时一次性构建内存索引
                         ▼
custom .dict.yaml ── CompileRimeDictFiles ──► custom Lexicon ─┐
                                                              │
用户提交/学习 ──► SQLite ime_user_lexicon + ime_bigrams ──────►│ learned
                                                              │
starter 索引 ────────────────────────────────────────────────►│ starter
                                                              │
Unihan 读音表 ───────────────────────────────────────────────►│ builtin
                                                              ▼
                         ResolveCandidate / 去重 / 排序
                                      │
                                      ▼
                              候选页（内存快照）
```

### 2.1 四层的写入、读取与失效

| 层 | 谁写 | 谁读 | 何时失效或重建 |
|---|---|---|---|
| `starter` | 发布构建流程生成 `data/starter.json`；运行时只读 | `StarterDictionary`、引擎启动构建 | 二进制/嵌入字节、digest 或 starter 数据版本改变时重建。digest 不匹配是构建完整性错误，不是用户可修复的运行时降级。 |
| `learned` | 输入引擎提交路径和学习存储写入 SQLite；导入流程写入合并快照 | 学习热镜像、候选排序器、同步读取器 | 每次提交、导入、删除后更新内存镜像；schema 迁移后重载。达到 `MaxLexMirrorEntries` 时只淘汰未 pinned 的低分项。 |
| `custom` | 用户/设置页/导入操作写 `.dict.yaml`；编译器写缓存 `.clvdict` | `CompileRimeDictFiles`、缓存加载器、`NewIMEDictionary` | 路径、size、mtime 或 `dictCompilerVersion` 变化时失效；解析逻辑变化必须 bump `dictCompilerVersion`。文件级失败不产生部分 Lexicon。 |
| `builtin` | 随引擎版本生成；运行时不写 | `UnihanDictionary` | Unihan 数据或引擎版本变化时重建；lookup 永不读文件。 |

`starter` 的单字频序只影响单字候选排序，不等于可导入 Rime 的 `(word, pinyin)` 记录。`custom`、`learned`、`starter` 的短语可进入同一个候选解析阶段，但来源等级先于频率。

### 2.2 加载时序

启动或显式 reload 时，顺序是：读取/校验 custom → 读取 learned 快照 → 复用或构建 starter → 构建 builtin → 生成不可变候选链。custom 文件监听只触发后台重建和原子替换；旧链在新链成功前继续服务。读取失败的 custom 不阻断启动，而是写入诊断并使用剩余层。

## 3. 四级优先级与同词裁决

来源优先级固定为：

```text
custom > learned > starter > builtin
```

这是语义等级，不是单纯的分数比较。相同 `(pinyin, word)` 多来源时只保留等级最高者；低等级记录不展示，但可以保留 `shadowedBy` 等诊断元数据。即使 learned 项 pinned，显式 custom 仍优先；pinned 的意义是保护 learned 项不被学习镜像的容量修剪，而不是跨越 custom 的来源等级。

同一来源内按以下顺序排序：

1. `pinned=true` 优先；
2. `effectiveFrequency` 高者优先；
3. `OriginalOrdinal` 小者优先，保持用户文件/导入快照的相对顺序；
4. `(pinyin, word)` 的 UTF-8 字节序作为稳定 tie-breaker。

展示层不得按文字再次去重，否则同一个汉字的不同读音会被误合并。

### 3.1 Go 接口契约

```go
// DictionarySource identifies the origin of a candidate. The constants are
// semantic levels; callers must use SourcePriority rather than numeric order.
type DictionarySource uint8

const (
    SourceCustom DictionarySource = iota
    SourceLearned
    SourceStarter
    SourceBuiltin
)

// CandidateRecord is a canonical candidate before pagination or UI formatting.
type CandidateRecord struct {
    Pinyin          string
    Word            string
    Source          DictionarySource
    Frequency       uint64
    Pinned          bool
    OriginalOrdinal uint32
}

// ResolveCandidate applies source priority and same-source tie breaks. It emits
// at most one record for each exact (Pinyin, Word) key and never performs I/O.
func ResolveCandidate(records []CandidateRecord) []CandidateRecord
```

伪代码：

```text
for record in records:
    key = (record.Pinyin, record.Word)
    current = best[key]
    if current absent or
       priority(record.Source) > priority(current.Source) or
       same source and sameSourceRank(record) > sameSourceRank(current):
        best[key] = record
return stableSort(best.values)
```

## 4. MergeLearning 契约（B4）

### 4.1 数据与函数签名

```go
// LearningData is a device-local snapshot. Words and Bigrams use stable keys;
// Aggregates carries counters that are not directly displayed as candidates.
type LearningData struct {
    Words      []LearnedWord
    Bigrams    []LearnedBigram
    Aggregates []LearningAggregate
    Enabled    bool
    SummaryOnly bool
}

// MergeLearning returns the deterministic union of two snapshots. Equal keys
// are merged, Pinned is OR'ed, and the result is sorted by its stable key.
// The operation is read-only and does not mutate either input.
func MergeLearning(a, b LearningData) LearningData
```

实现必须为 `Words`、`Bigrams`、`Aggregates` 分别定义稳定键；不能依赖数组位置。相同词项的 `Pinned` 做逻辑 OR；如果两个快照都带相同的恢复对象引用才保留，否则清空并让恢复层重新选择。`Enabled` 为 OR；`SummaryOnly` 只有两边都为 true 才为 true。

### 4.2 频率求和与压缩

未 pinned 项的合并规则：

```text
sum = checked_add(a.frequency, b.frequency)
score = 0                                  if sum == 0
score = min(4096, round(1024 * log1p(sum))) otherwise
```

`Bigrams` 和 `Aggregates` 使用相同的压缩规则。实现可以使用预计算整数表，但不同平台必须得到相同整数结果。pinned 项绕过压缩并保留 checked sum；如果 sum 溢出，返回上层数据校验错误，禁止静默回绕。pinned 项在镜像容量修剪、导出截断和低频淘汰中都不可删除。

必须压缩的理由：如果只求和，一台设备积累多年的历史会完全支配另一台设备刚学到的新词，新的词即使在另一侧频繁使用也永远上不来。对数压缩保留强弱关系，同时把历史规模的影响压平；封顶保证计数不会无限扩大，pinned 则保留明确的人类意图。

该分数是排名分数，不是审计计数。`MergeLearning` 必须满足交换性、确定性和输入不变性。幂等去重属于同步快照层：同一个 snapshot ID 在进入 MergeLearning 前只允许导入一次。

### 4.3 多设备分发形态

调研已确认 Rime 的形态：共享 `sync_dir` 内每台设备各写一份 `<installation_id>.userdb.txt`，而不是共同写一个总文件。Clavis 沿用该取舍：

```text
sync_dir/
  mini.userdb.txt
  windows.userdb.txt
```

设备标识必须稳定且可读，默认推荐 `mini`、`windows`，允许用户修改。设备只原子替换自己的文件，不删除或覆盖其他设备文件；读取时扫描已知快照、解析成 `LearningData`，在内存中调用 `MergeLearning`。来源设备可供审计，但不参与来源优先级排序。starter、builtin 和 custom 定义不写入 userdb 快照。

## 5. Rime 导出/导入 round-trip 不变量

规范文本记录为：

```text
word<TAB>pinyin<TAB>weight<LF>
```

必须逐字节保持：

- `word` 的 UTF-8 bytes：不做 Unicode normalization、大小写变换或自动去空白；
- `pinyin` 的 UTF-8 bytes：只允许在进入 canonical 导出管线前做一次明确归一化，canonical 输入再次导入再导出必须相同；
- 记录相对顺序：导出器不得按权重重排用户顺序；
- 可表示记录的存在性：空 word、缺 pinyin 或无法表示的记录不得用假拼音补齐，必须产生诊断并被整条拒绝。

允许归一化的只有 `weight` 的表示：整数、浮点或百分号可以映射到统一的十进制整数，只要求数值排序稳定。解析规则变化必须递增 `dictCompilerVersion`，旧缓存不能被新解析器复用。

## 6. custom 失败模式与诊断

当前 `LoadDictionary` 失败时静默回退 `defaultDictionary`，设计改为可观察且可部分降级：

```go
// DictionaryDiagnostic explains why one custom source was not used. Message
// must not contain file contents, prompts or secrets.
type DictionaryDiagnostic struct {
    Path     string
    Kind     string // unreadable, malformed, missing_field, compiler_error, empty
    Message  string
    Fallback string // e.g. "starter+builtin"
}

// LoadDictionaryWithDiagnostics loads each custom file as an all-or-nothing
// unit. Failed files are skipped, valid files remain, and the fallback chain
// is always available. It performs I/O only during load/reload, never lookup.
func LoadDictionaryWithDiagnostics(
    customPaths []string,
    cacheDir string,
    report func(DictionaryDiagnostic),
) (Dictionary, error)
```

逐文件处理：不可读、YAML 语法错误、缺 `word`/`pinyin`、空词库、缓存损坏分别给稳定 `Kind`。同批其他 custom 仍可加载；一份文件不能只使用解析成功的前半段。全部 custom 失败时返回 `StarterDictionary + UnihanDictionary`，并报告每个失败路径及总回退诊断。诊断可进入日志和 self-check，但不能含正文。

## 7. 测试策略

| 测试名 | 断言要点 |
|---|---|
| `TestDictionaryPriorityCustomLearnedStarterBuiltin` | 同一 key 同时来自四层时只输出 custom；逐层移除后依次得到 learned、starter、builtin。 |
| `TestDictionaryTieBreakPinnedFrequencyOrdinalBytes` | 同源按 pinned、frequency、ordinal、UTF-8 bytes 稳定排序。 |
| `TestMergeLearningSumsAndCompressesUnpinned` | 先 checked sum，再按 log 压缩；交换输入结果相同。 |
| `TestMergeLearningPinnedNeverCompressedOrEvicted` | pinned 保留 checked sum，容量修剪不删除。 |
| `TestMergeLearningUnionHasNoDuplicateKeys` | 三类集合均无重复 key，pinned 为 OR，结果按稳定 key 排序。 |
| `TestLearningReadMergeKeepsPerDeviceFiles` | `mini` 与 `windows` 文件同时存在，读取期等于 MergeLearning，写一侧不覆盖另一侧。 |
| `TestRimeRoundTripPreservesWordPinyinAndOrderBytes` | word/pinyin/相对顺序 bytes 完全相同。 |
| `TestRimeRoundTripNormalizesWeightOnly` | weight 文本可变但数值顺序稳定，其他字段不变。 |
| `TestRimeCompilerCacheInvalidatesOnVersionChange` | compiler version 改变后旧 cache 不命中。 |
| `TestLoadDictionaryDiagnosticsFallsBackWithoutPartialCustom` | 损坏、缺字段、不可读、空文件各有稳定 Kind，结果仍含 starter+builtin。 |
| `TestLoadDictionaryReportsAllFailedCustomSources` | 多文件失败分别报告，不影响启动。 |
| `TestStarterFormatAndDigestRemainUnchanged` | 现有 `TestStarterDataMatchesRecordedFingerprint` 继续通过，starter 不被转成假拼音。 |

## 8. 关键取舍与否掉的替代方案

- 保留 starter 原格式：它表达的是汉字频序而不是逐字读音，伪造拼音会制造错误语义。
- 选择来源等级先于频率：用户明确维护的 custom 必须能覆盖旧 learned。
- 选择对数压缩而非简单求和：避免单侧旧历史永久支配新词。
- 选择 pinned 绕过压缩和淘汰：固定是用户意图，不应被自动统计撤销。
- 选择每设备一份、读取期合并：避免共享总文件的并发写冲突，且可追溯来源。
- 选择整文件失败：部分导入无法审计，会产生半个词库。
- 否掉单一 `merged.userdb.txt`：两设备同时写必然冲突。
- 否掉把四层预先编成一个 `.clvdict`：会丢来源等级、诊断和可审计性。
- 否掉跨来源绝对 weight：任意 custom weight 不应绕过来源语义。

## 9. 遗留待拍板问题

1. 设备标识是首次启动自动生成 `mini`/`windows`，还是完全由用户命名。
2. 重装设备是否沿用旧标识，还是生成新标识并由用户选择合并。
3. 导出是否写 sidecar 来源元数据；建议不把来源注释写进词条正文。
4. pinned 频率上限、snapshot ID 去重存储位置和保留期。
5. 诊断只显示在设置页、只写日志，还是两者都做。
6. 同词不同 pinyin 是否允许同时导出；建议按完整 `(pinyin, word)` 区分并允许。
7. 发现未知设备文件时是忽略、提示，还是进入待批准设备列表。
