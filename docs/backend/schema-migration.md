# 应用库 Schema 版本、迁移与回滚设计（P1-C）

- 任务：`T-clavis-backend-design`
- 范围：应用库（当前为共享 SQLite 库，包含学习、bigram、usage_frecency、剪贴板等表）的版本管理、迁移、备份和启动失败语义。
- 非范围：Codex `state_5.sqlite`；它是第三方只读数据库，Clavis 不迁移、不写入、不修改 `user_version`。

## 1. 现状与问题

现状证据：

- `internal/storage/learning_store.go:97–120`：`LearningStore` 直接持有 SQLite DB，构造时调用 `initSchema` 和 `load`。
- `internal/storage/learning_store.go:186–203`：学习相关表使用多条 `CREATE TABLE IF NOT EXISTS` 和 `CREATE INDEX IF NOT EXISTS`，没有统一 schema 版本入口。
- `internal/storage/learning_store.go:188–195`：当前至少包含 `ime_user_lexicon`、`ime_bigrams`、`usage_frecency`、`prompt_history`、`lexicon_hits_daily`、`ai_usage`。
- `internal/storage/learning_writer.go:15–33`：热路径写入先更新 RAM mirror，再由后台 writer 批量持久化；迁移期间不能让 writer 并发打开同一库。
- `internal/storage/sync_sqlite_store.go:409–440`：sync 库已经设置 `PRAGMA user_version = 1`，且有 tombstone 兼容迁移。
- `pkg/core/sync/state.go` / `internal/sync/state_store.go`：设备状态是另一类本地库/状态边界，不能与应用库共享版本号语义。

当前问题是：`CREATE TABLE IF NOT EXISTS` 只能覆盖“没有表”的冷启动，不能安全表达加列、约束、索引重建、数据回填或降级；部分 DDL 成功后失败时，下一次启动也无法判断库处于哪个中间状态。

## 2. 结论：按数据库文件分开版本，不做全局统一号

结论：**每个 SQLite 数据库文件独立维护 `PRAGMA user_version`，不建立跨库全局版本号。**

版本空间固定如下：

| DBKind | 数据库 | `user_version` 所属 | 当前/目标 |
|---|---|---|---|
| `app` | 应用共享库（学习、剪贴板等） | 应用库迁移链 | 当前未纳管按 v0 处理；首个正式迁移目标为 v1 |
| `sync` | sync 服务库 | sync 迁移链 | 已有 v1；由 sync 自己维护 |
| `device_state` | 客户端非机密状态库 | 设备状态迁移链 | 独立于应用库；不得复用应用库的数字含义 |

理由：

1. 迁移事务只覆盖一个 SQLite 文件，跨文件版本无法原子提交。
2. sync 库与应用库的发布节奏不同，统一号会制造“应用库已到 v3 但 sync 只有 v1”的假对应关系。
3. `user_version` 是 SQLite 文件级整数，足以做每个文件的单调版本，不足以表达跨文件事务。
4. 第三方 Codex 库不能被纳入这套版本链。

`user_version=0` 统一表示“未纳管/历史无版本”，**不是**“空库”。迁移器必须先用表、列、索引和数据形状识别 v0 的具体变体，再决定是否可迁移；未知形状直接拒绝。

## 3. 迁移 API 与执行顺序

代码归属建议：迁移实现放 `internal/storage`，`LearningStore` 不再自行创建/变更 schema；它只接收已通过迁移的 DB。

```go
package storage

type DBKind string

const (
    DBKindApp        DBKind = "app"
    DBKindSync       DBKind = "sync"
    DBKindDeviceState DBKind = "device_state"
)

type MigrationReport struct {
    Kind       DBKind
    From       int
    To         int
    Applied    []string
    ReadOnly   bool
    BackupPath string
    SchemaHash string
}

type MigrationOptions struct {
    BackupDir       string
    AllowCreate     bool
    Failpoint       func(step string) error // 仅测试使用
    LockTimeout     time.Duration
}

func Migrate(ctx context.Context, db *sqlite.DB, kind DBKind, opts MigrationOptions) (MigrationReport, error)
func CurrentSchemaVersion(ctx context.Context, db *sqlite.DB) (int, error)
func Backup(ctx context.Context, db *sqlite.DB, kind DBKind, dst string) (BackupManifest, error)
func ValidateSchema(ctx context.Context, db *sqlite.DB, kind DBKind, version int) error
```

推荐启动顺序：

1. 进程入口取得应用库独占迁移锁；此时不启动 `LearningWriter`、clipboard writer、定时 prune 或同步任务。
2. 校验路径、权限、是否符号链接，并读取 `PRAGMA user_version`。
3. 读取实体清单：表、列、索引、触发器及关键约束；对 `user_version=0` 识别历史形状。
4. 目标版本高于当前时，创建一致性备份并写入 manifest；备份校验失败立即停止，不碰原库。
5. 在同一迁移事务中执行当前版本到目标版本的连续迁移，并在最后写目标 `user_version`。
6. 事务提交后重新读取 schema 和关键数据计数，计算 schema hash；校验失败不得启动写者。
7. 只有迁移成功且校验成功后，才构造 `LearningStore`、`LearningWriter` 和其他会写应用库的服务。
8. 若无需迁移，仍执行轻量 schema 校验；不重复建表、不重复回填。

事务边界：

- 数据 DDL、数据回填、索引、约束和 `PRAGMA user_version = N` 属于同一个迁移事务。
- WAL 模式设置、文件权限修复、备份文件发布属于事务外步骤，但必须在迁移锁内执行。
- 如果当前 SQLite/MyGo 驱动对某条 DDL 不支持事务回滚，不能退化为逐条写原库；改用 staged DB：复制一致性快照到临时库，在临时库完成整条迁移并校验，最后关闭连接后原子替换。

## 4. 具体迁移骨架：给 `clipboard_items` 增加 `byte_size`

假设应用库当前 v1 已有：

```sql
CREATE TABLE clipboard_items (
    id TEXT PRIMARY KEY NOT NULL,
    kind TEXT NOT NULL,
    text TEXT NOT NULL DEFAULT '',
    created_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL
);
```

v2 要求增加 `byte_size INTEGER NOT NULL DEFAULT 0`，并建立按时间查询的索引。不能使用“无条件 `ALTER TABLE ADD COLUMN`”，因为重复执行会报 duplicate column。

```go
const appSchemaVersion = 2

func migrateApp(ctx context.Context, db *sqlite.DB, from, to int, opts MigrationOptions) error {
    if from > to {
        return fmt.Errorf("schema_too_new: app=%d target=%d", from, to)
    }
    for version := from; version < to; version++ {
        switch version {
        case 0:
            if err := migrateAppV0ToV1(ctx, db, opts); err != nil {
                return err
            }
        case 1:
            if err := migrateAppV1ToV2(ctx, db, opts); err != nil {
                return err
            }
        default:
            return fmt.Errorf("unknown app migration from v%d", version)
        }
    }
    return nil
}

func migrateAppV1ToV2(ctx context.Context, db *sqlite.DB, opts MigrationOptions) error {
    return withMigrationTransaction(ctx, db, func(stmts *[]sqlite.Statement) error {
        if err := maybeFail(opts, "app.v1_to_v2.before_column"); err != nil {
            return err
        }

        // 由迁移器预先读取 PRAGMA table_info(clipboard_items)，只有缺列时才加入。
        // 这里的骨架用动态 SQL 表示；生产实现必须用参数化/白名单表名，
        // 不接受外部传入的表名或列名。
        *stmts = append(*stmts,
            sqlite.Statement{SQL: `ALTER TABLE clipboard_items ADD COLUMN byte_size INTEGER NOT NULL DEFAULT 0`},
            sqlite.Statement{SQL: `UPDATE clipboard_items SET byte_size = length(CAST(text AS BLOB)) WHERE byte_size = 0 AND text <> ''`},
            sqlite.Statement{SQL: `CREATE INDEX IF NOT EXISTS idx_clipboard_items_updated ON clipboard_items(updated_at_ns DESC, id DESC)`},
            sqlite.Statement{SQL: `PRAGMA user_version = 2`},
        )
        if err := maybeFail(opts, "app.v1_to_v2.after_version"); err != nil {
            return err
        }
        return nil
    })
}

func withMigrationTransaction(ctx context.Context, db *sqlite.DB, build func(*[]sqlite.Statement) error) error {
    statements := make([]sqlite.Statement, 0, 8)
    if err := build(&statements); err != nil {
        return err
    }
    if _, err := db.Transaction(ctx, statements); err != nil {
        return fmt.Errorf("migration transaction rolled back: %w", err)
    }
    return nil
}

func maybeFail(opts MigrationOptions, step string) error {
    if opts.Failpoint == nil {
        return nil
    }
    return opts.Failpoint(step)
}
```

说明：真实实现需先用 `PRAGMA table_info(clipboard_items)` 判断 `byte_size` 是否已经存在。若列已存在但 `user_version=1`，必须验证其类型、`NOT NULL` 和默认值；验证通过可只补索引并推进版本，验证不通过则返回 `integrity_failed`，不能盲改列。

`length(CAST(text AS BLOB))` 按 UTF-8 字节数回填；历史图片/文件项若没有 `text`，保持 0，由后续媒体元数据迁移单独处理。迁移不得读取或输出剪贴板全文到日志。

## 5. 幂等、失败回滚与降级

### 5.1 重复运行

- `user_version=2` 且 schema hash 正确：直接通过，不执行迁移。
- `user_version=1` 且列已存在：验证后只执行缺失的幂等索引/版本推进。
- `user_version=1` 且列缺失：执行一次 `ALTER TABLE`、回填、索引、版本更新。
- 任何中间失败：事务回滚，版本号保持旧值；下次启动从旧版本重跑，不依赖“上一次执行到第几条”。
- 若驱动在 DDL 后无法回滚，使用 staged DB 方案，原库不变。

### 5.2 新版本库被旧版本打开

结论：**拒绝写入并进入只读受限模式，不自动降级、不删除列、不重置版本。**

- `current > supported` 返回 `schema_too_new`。
- UI/CLI 可读取已经安全读取的非写状态，但不能启动 writer、prune、迁移或覆盖备份。
- 任何尝试写入都返回稳定错误 `schema_too_new`。
- 用户必须升级应用，或使用明确的离线导出/恢复流程；不能让旧版本“尽量工作”造成新字段丢失。

### 5.3 备份与恢复

- 备份在迁移前完成，文件名带 `DBKind`、from/to、时间和内容 digest。
- 备份 manifest 记录数据库文件、WAL/SHM 是否纳入、schema version、schema hash、字节数和 SHA-256。
- 备份校验失败：原库保持关闭，不启动写者。
- 恢复必须关闭所有连接、校验 manifest 和 digest、将恢复库放入同目录临时文件后原子替换；恢复后重新运行 `ValidateSchema`。
- 不把活跃 WAL 数据库直接 `cp` 主文件当备份。

## 6. 上线步骤与回滚

1. 先发布只读 schema inspector 和备份 manifest，不改变写路径。
2. 在临时复制库上跑 v0、v1、损坏 schema、已有部分列、WAL 活跃五类 fixture。
3. 线上启动先只执行备份+校验，迁移开关默认开启但目标版本不变，观察错误码和耗时。
4. 发布 v1 纳管当前应用库；`CREATE IF NOT EXISTS` 仍可作为兼容兜底，但不再是版本推进机制。
5. 发布 v2 的 `clipboard_items.byte_size` 迁移；灰度验证备份 manifest、重启幂等和 writer 启动顺序。
6. 迁移成功后才启用依赖新列的查询/指标。
7. 回滚应用二进制时：若旧版本支持该 schema，正常只读/写；若不支持，按 `schema_too_new` 拒绝写入。不能回滚数据库 schema。
8. 真正恢复旧 schema 只能停写、备份当前库、使用离线恢复副本，再由匹配版本打开。

## 7. 可执行验收

```bash
cd /Users/yeisme/workspace/clavis

go test -count=1 -v ./internal/storage -run 'Test(Migration|SQLiteStoreRestart|SyncStoreContract)'
```

必须有以下具名断言：

- `user_version=0` 历史应用库迁移到 v1，表和数据保留。
- v1 到 v2 后 `clipboard_items.byte_size` 存在，中文文本按 UTF-8 字节数回填。
- 重复运行迁移不报 duplicate column，版本和行数不变。
- 在 `before_column`、`after_version` failpoint 失败后，`user_version`、列和数据均保持迁移前状态。
- 备份 digest 可回验；WAL 活跃时恢复出的行可读。
- 新版本库被旧版本打开返回 `schema_too_new`，且 writer 未启动、原库未被改写。
- 迁移失败不创建空库替换原库。

静态检查：

```bash
grep -R "PRAGMA user_version" -n internal/storage
# 应看到应用库迁移器、sync 迁移器、device_state 迁移器各自的入口；
# 不应看到 Codex reader 修改第三方数据库。
```

## 8. 取舍与风险

取舍结论：押“每库独立版本 + 单库迁移事务 + 迁移前备份 + 新版本拒绝写入”，否掉跨库统一版本、自动降级和直接复制活 WAL 文件。原因是数据完整性比旧版本继续运行更重要，且应用库、sync 库、设备状态库的生命周期不同。

主要风险：MyGo SQLite 驱动对 DDL 事务或在线备份能力的实际限制可能与 SQLite 原生行为不同；若不能证明事务回滚，必须使用 staged DB，不能用多条 `Execute` 假装原子迁移。另一个风险是历史 v0 库形状不唯一，识别不到的库必须拒绝，不得按空库重建。
