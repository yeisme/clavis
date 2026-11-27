# Bigram 冷启动快照设计（P1-C / P1-D 交接）

- 任务：`T-clavis-backend-design`
- 目标：让 IME 在冷启动时拥有最近的双词转移排序，同时不把快照变成第二个学习真相源，也不阻塞启动和按键路径。
- 非目标：修改候选排序算法、把完整学习库同步到远端、在启动时同步网络。

## 1. 现状

现状证据：

- `internal/storage/learning_store.go:97–120`：`LearningStore` 持有 `bigrams` RAM mirror，并在构造时执行 `initSchema`、`load`。
- `internal/storage/learning_store.go:188–195`：`ime_bigrams(prev_word, word, transition_count, last_used_ns)` 与 `usage_frecency` 同在应用库。
- `internal/storage/learning_store.go:206–240`：启动从 SQLite 读取有界的 unigram、bigram、usage mirror；这是当前唯一可持久化学习来源。
- `internal/storage/learning_store.go:266–299`：学习写入先更新 RAM mirror，再写 `ime_bigrams` 等表。
- `internal/storage/learning_writer.go:15–33,168–217`：后台 writer 批量、延迟 flush；内存中的新学习可能暂时尚未落盘。
- `internal/storage/learning_writer.go:483–491`：bigram 和 `usage_frecency` 使用 upsert 累加/更新。
- `pkg/core/inputengine/engine.go:773–860`：候选生成会读取 user frequency/bigram，并将 bigram 影响排序；`pkg/core/inputengine/unify_test.go` 已覆盖生成及上下文行为。

缺口：当前冷启动依赖 SQLite 查询完成后才能填充 mirror；没有一个带版本/digest/水位的紧凑快照，也没有“引擎先可用、后台补齐”的明确发布协议。若启动磁盘慢或库暂时 busy，双词热度会缺失，但不能为了它阻塞首帧或在 hook 线程做 I/O。

## 2. 真相源与快照定位

结论：**`ime_bigrams` 与 `usage_frecency` 是唯一真相源；bigram snapshot 是可丢弃、可重建、带水位的缓存副本。**

- `ime_bigrams`：保存有方向的 `(prev_word, word)` 转移计数和最后使用时间，是 bigram 事实来源。
- `usage_frecency`：保存对象级使用次数/最近使用时间，用于 frecency 和首页整体排序；它不是 bigram 的替代表。
- snapshot：只保存已经从上述两张表读取并排序后的有限热集，不参与原始学习计数的合并，不作为同步输入，不覆盖数据库。
- LearningWriter RAM mirror：是“未持久化但已观察”的短期事实；snapshot 生成前必须先取得 flush 水位，不能把未 flush 的镜像误称为持久快照。

冲突规则：

1. 快照加载后，如果 SQLite 可读，数据库值优先。
2. 快照比数据库新（例如上次进程在 flush 后生成快照但数据库 checkpoint 尚未完成）时，只能在记录的 `source_watermark` 可验证且快照 digest 合法时作为临时补充；后台数据库扫描完成后以数据库为准。
3. 快照比数据库旧：丢弃旧条目，不能把旧计数写回数据库。
4. 快照损坏、版本不支持、digest 不匹配、来源水位不可解释：整份丢弃并从数据库重建，不影响引擎启动。
5. 快照与在线学习同时发生：在线 RAM mirror 通过 generation 合并到下一份发布；旧快照不能覆盖较新的 RAM mirror。

## 3. 快照格式和存储位置

快照存放在应用私有数据目录，与应用 SQLite 同一受保护目录，但不是 SQLite 的 WAL 或主数据库文件。例如：

```text
<app-data>/learning/bigram-snapshot-v1.bin
<app-data>/learning/bigram-snapshot-v1.bin.tmp
<app-data>/learning/bigram-snapshot-v1.manifest.json
```

不放在共享词库目录、不放云盘、不放用户可直接编辑的 config.toml。文件权限沿用应用数据库私有权限；写入使用临时文件、`fsync`、`rename`，失败保留旧快照。

推荐二进制结构（逻辑字段，具体编码固定为 little-endian/长度前缀，禁止依赖 Go `gob`）：

```go
type BigramSnapshot struct {
    FormatVersion uint16
    CreatedAt     time.Time
    SourceSchema  int
    SourceDigest  [32]byte // ime_bigrams + usage_frecency 选定行的规范摘要
    FlushWatermark uint64  // LearningWriter 已持久化水位
    Entries       []BigramEntry
}

type BigramEntry struct {
    PrevWord       string
    Word           string
    TransitionCount uint64
    LastUsedNS     int64
    UsageCount     uint64
}
```

字段约束：

- `PrevWord`、`Word` 必须是已脱敏的汉字/受允许的候选键，不写完整剪贴板、prompt 或绝对路径。
- 只保存前 N 个有用转移；N 使用固定配置（初版 4096），与 `MaxBigramMirrorEntries` 对齐。
- 排序为 `TransitionCount DESC, LastUsedNS DESC, PrevWord bytes ASC, Word bytes ASC`；同输入生成的快照字节必须一致。
- `SourceDigest` 覆盖来源 schema、排序后的 key/count/time 和 `FlushWatermark`，防止半写或错配。
- 快照总大小设置硬上限（初版 2 MiB）；超过上限按稳定排序截断并记录 `truncated=true`，不分片。

快照不保存完整 `usage_frecency` 表，只保存 bigram entries 需要的 usage 辅助字段；首页 frecency 仍从 `usage_frecency` mirror/查询获得。这样避免快照成为第二个完整学习数据库。

## 4. 加载时序：启动不阻塞，后台补齐

```text
进程启动
  │
  ├─ 读取配置/检查快照路径（只做路径与 manifest 轻校验）
  │
  ├─ 后台 goroutine 读取 snapshot
  │      ├─ 成功且格式/digest 可接受：发布只读 BigramSnapshot mirror
  │      └─ 失败：记录 snapshot_invalid / snapshot_missing，继续
  │
  ├─ 立即创建基础 input engine
  │      ├─ 内嵌词库可用
  │      ├─ 无 snapshot 也可生成普通候选
  │      └─ hook/候选路径不读文件、不查 SQLite
  │
  ├─ 后台打开应用库并读取 ime_bigrams + usage_frecency
  │      ├─ 取得 LearningWriter flush 水位
  │      ├─ 读取有界热集，计算 SourceDigest
  │      └─ 以 DB mirror 原子替换 snapshot mirror
  │
  ├─ 引擎可用后后台生成下一份 snapshot
  │      ├─ 写 tmp
  │      ├─ fsync + manifest
  │      └─ rename 发布
  │
  └─ 后续学习：RAM mirror → LearningWriter flush → generation 增加 → 下次后台快照
```

时间边界：

- 启动主线程不等待 snapshot/SQLite 学习读取；快照读取有单独 deadline，建议 100ms，超时即跳过。
- 引擎首帧不等待快照；快照只影响后续候选排序。
- 文件读取、SQLite 查询、压缩/序列化都在后台 worker；hook 线程不能调用 `os.ReadFile`、SQLite、JSON 解码或 snapshot swap 的长锁。
- 发布使用不可变对象指针/短原子交换；在线学习 generation 不匹配时，旧 worker 结果不得覆盖新 mirror。

## 5. 生成、刷新和一致性协议

建议接口：

```go
type BigramSnapshotStore interface {
    Load(ctx context.Context) (BigramSnapshot, error)
    Publish(ctx context.Context, snapshot BigramSnapshot) error
}

type BigramSnapshotBuilder interface {
    Build(ctx context.Context, flushWatermark uint64) (BigramSnapshot, error)
}

type BigramRuntime interface {
    Snapshot() *BigramSnapshot
    Replace(snapshot BigramSnapshot) bool // generation 较新才替换
    Generation() uint64
}
```

刷新步骤：

1. 从 writer 取得一个单调 flush watermark；若 writer 有 pending，先请求后台 flush，但 flush 有 deadline，不能阻塞 UI。
2. 在同一读事务/一致性读取窗口中读取 `ime_bigrams` 和 `usage_frecency` 的有界数据。
3. 规范化排序、生成 digest、绑定 schema version 和 flush watermark。
4. 若生成 generation 仍是当前 generation，原子发布到 runtime；否则丢弃旧结果。
5. 先发布 runtime，再异步持久化文件；持久化失败只标诊断，不撤销当前可用 mirror。
6. 新进程读取 snapshot 后必须继续执行数据库校验；snapshot 不是永久离线授权。

应用学习写入与 snapshot 的取舍：不做每次按键都生成 snapshot；由 flush 成功、定时（建议 30 秒）或关闭前触发，关闭前只给有限 deadline，失败不阻塞退出。

## 6. 取舍

押的方案：**数据库真相 + 有界二进制快照 + 后台双阶段加载。**

否掉的方案：

- 否掉“启动必须等数据库读完”：会把磁盘抖动直接传到首帧和输入体验。
- 否掉“快照作为第二真相源”：双写计数会产生重复累加、远端合并歧义和不可解释冲突。
- 否掉“每次学习立即重写快照”：会产生 fsync/rename 风暴，并与 LearningWriter 的批量语义冲突。
- 否掉“把快照纳入跨设备同步”：snapshot 是本地排序缓存，跨设备传它会把设备局部排序误当学习贡献；同步应传原始学习快照/贡献，另行聚合。
- 否掉“启动时读完整表”：只加载有界热集，完整重建放后台，避免大库和低端盘拖延。

## 7. 上线步骤与回滚

1. 先加入只读 snapshot loader 和指标，不生成文件；验证坏文件、截断文件、版本不支持都可启动。
2. 加入 runtime mirror 原子发布，但默认只影响诊断，不改变候选排序；记录“快照排序 vs DB 排序”的差异计数。
3. 打开后台从数据库构建 snapshot，写入独立目录；验证文件权限、临时文件清理、重启一致性。
4. 在 fixture 上冻结字节级 digest、候选排序和 p95 加载时间；再启用 snapshot 对候选排序的影响。
5. 观察一段周期后再启用关闭前 best-effort publish；关闭 deadline 到期直接放弃。
6. 回滚应用版本时：旧版本读到未知 `FormatVersion` 只跳过快照，继续使用 SQLite/内嵌词库；不能删除数据库。
7. 回滚快照格式时只删除/隔离快照文件，重新从数据库构建；旧快照不迁移成新格式。

## 8. 可执行验收

```bash
cd <clavis checkout>

go test -count=1 -v ./internal/storage ./pkg/core/inputengine -run 'Test(Bigram|Learning|EngineConcurrent|.*ColdStart)'
```

必须具名验证：

- `ime_bigrams` 写入后重启，快照和数据库排序一致。
- 只有 snapshot、数据库暂时 busy 时，engine 仍在 deadline 内可用；不触发 hook I/O。
- snapshot 截断、错误 digest、未知格式、超过大小均回退到数据库/内嵌，不 panic。
- 在线学习 generation 较新时，迟到的旧 snapshot 不覆盖新 mirror。
- 同一 fixture 生成两次 snapshot 字节和 digest 一致。
- LearningWriter flush 失败时不能把未持久化数据宣称为 `FlushWatermark`；诊断包含失败原因但输入仍可用。

性能/并发：

```bash
go test -race -count=1 ./internal/storage ./pkg/core/inputengine
```

另加启动 fixture 断言：主 goroutine 在 snapshot/DB worker 延迟 500ms 时仍能创建 engine；worker 完成后只发生一次可见 mirror 替换。

## 9. 风险

最大风险是把“RAM 已学习”错误写进快照并在重启时当作已持久化事实，导致计数重复或丢失；因此必须绑定 writer flush watermark。其次是 SQLite 读取窗口和后台 snapshot 生成跨过一次在线写入，必须用 generation 和 source digest 拒绝旧结果。最后是快照大小截断改变排序，必须固定排序键、硬上限和诊断字段，不能静默截断。
