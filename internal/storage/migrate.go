package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

// DBKind identifies an independent SQLite schema version namespace.
// Each database file maintains its own monotonic PRAGMA user_version.
type DBKind string

const (
	DBKindApp         DBKind = "app"
	DBKindSync        DBKind = "sync"
	DBKindDeviceState DBKind = "device_state"
)

const (
	// AppSchemaVersion is the current PRAGMA user_version for the shared app database
	// (clipboard_items + FTS5 + learning/frecency/prompt/usage tables).
	AppSchemaVersion = 2
	// SyncSchemaVersion is the current PRAGMA user_version for the sync database.
	SyncSchemaVersion = 1
	// DeviceStateSchemaVersion is the current PRAGMA user_version for the non-secret device state DB.
	DeviceStateSchemaVersion = 1

	defaultMigrationLockTimeout = 5 * time.Second
	migrationLockPollInterval   = 10 * time.Millisecond
)

var (
	// ErrSchemaTooNew indicates the database user_version exceeds the binary's supported schema.
	// Callers must reject writes and never downgrade or overwrite the database.
	ErrSchemaTooNew = errors.New("schema_too_new")
	// ErrBackupFailed indicates pre-migration backup creation or verification failed.
	ErrBackupFailed = errors.New("backup_failed")
	// ErrMigrationBusy indicates the exclusive migration lock could not be acquired before timeout.
	ErrMigrationBusy = errors.New("migration_busy")
	// ErrMigrationFailed indicates a migration transaction failed and rolled back.
	ErrMigrationFailed = errors.New("migration_failed")
	// ErrIntegrityFailed indicates schema validation or legacy v0 recognition failed.
	ErrIntegrityFailed = errors.New("integrity_failed")
	// ErrUnknownDBKind indicates an unrecognized DBKind was supplied.
	ErrUnknownDBKind = errors.New("unknown_db_kind")
)

// MigrationReport describes the outcome of a Migrate call.
type MigrationReport struct {
	Kind         DBKind
	From         int
	To           int
	Applied      []string
	ReadOnly     bool
	BackupPath   string
	ManifestPath string
	SchemaHash   string
}

// MigrationOptions configures Migrate behavior.
type MigrationOptions struct {
	// DBPath overrides automatic PRAGMA database_list path discovery when non-empty.
	DBPath string
	// BackupDir overrides the directory where pre-migration backup files and manifests are written.
	// When empty, backups are placed alongside the database file.
	BackupDir string
	// AllowCreate explicitly permits initializing an empty v0 database.
	AllowCreate bool
	// DisallowCreate rejects empty v0 databases instead of creating tables.
	DisallowCreate bool
	// Failpoint is a test-only fault injection hook keyed by migration step name.
	Failpoint func(step string) error
	// LockTimeout bounds how long Migrate waits for the exclusive migration file lock.
	// Zero defaults to 5s; a negative value fails immediately if the lock is busy.
	LockTimeout time.Duration
}

// BackupManifest records verifiable metadata for a pre-migration database backup.
type BackupManifest struct {
	Kind            DBKind    `json:"kind"`
	SourcePath      string    `json:"sourcePath"`
	BackupPath      string    `json:"backupPath"`
	FromVersion     int       `json:"fromVersion"`
	ToVersion       int       `json:"toVersion"`
	SchemaVersion   int       `json:"schemaVersion"`
	SchemaHash      string    `json:"schemaHash"`
	SizeBytes       int64     `json:"sizeBytes"`
	SHA256          string    `json:"sha256"`
	WALCheckpointed bool      `json:"walCheckpointed"`
	CreatedAt       time.Time `json:"createdAt"`
}

// TargetSchemaVersion returns the current supported schema version for kind.
func TargetSchemaVersion(kind DBKind) (int, error) {
	switch kind {
	case DBKindApp:
		return AppSchemaVersion, nil
	case DBKindSync:
		return SyncSchemaVersion, nil
	case DBKindDeviceState:
		return DeviceStateSchemaVersion, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownDBKind, kind)
	}
}

// CurrentSchemaVersion reads PRAGMA user_version from db.
func CurrentSchemaVersion(ctx context.Context, db *sqlite.DB) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return 0, fmt.Errorf("%w: nil sqlite database", ErrIntegrityFailed)
	}
	rows, err := db.Query(ctx, `PRAGMA user_version`)
	if err != nil {
		return 0, fmt.Errorf("read PRAGMA user_version: %w", err)
	}
	if len(rows.Values) == 0 || len(rows.Values[0]) == 0 {
		return 0, fmt.Errorf("%w: empty PRAGMA user_version result", ErrIntegrityFailed)
	}
	return int(int64val(rows.Values[0], 0)), nil
}

// ComputeSchemaHash returns a canonical SHA-256 hex digest of db's non-internal sqlite_master definitions.
func ComputeSchemaHash(ctx context.Context, db *sqlite.DB) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return "", fmt.Errorf("%w: nil sqlite database", ErrIntegrityFailed)
	}
	rows, err := db.Query(ctx, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type ASC, name ASC`)
	if err != nil {
		return "", fmt.Errorf("query sqlite_master for schema hash: %w", err)
	}
	h := sha256.New()
	for _, r := range rows.Values {
		if len(r) < 4 {
			continue
		}
		_, _ = io.WriteString(h, str(r, 0))
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, str(r, 1))
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, str(r, 2))
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, strings.TrimSpace(str(r, 3)))
		_, _ = io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Migrate brings db to the current schema version for kind.
//
// Guarantees:
//   - user_version > target returns ErrSchemaTooNew with ReadOnly=true without modifying db.
//   - Existing file-backed databases receive a verified atomic pre-migration backup and JSON manifest.
//   - All DDL, backfills, indexes, and PRAGMA user_version updates execute atomically in a transaction;
//     any failure (including failpoints) rolls back cleanly to the prior version.
//   - Already up-to-date databases run lightweight schema validation with zero writes.
func Migrate(ctx context.Context, db *sqlite.DB, kind DBKind, opts MigrationOptions) (MigrationReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MigrationReport{}, err
	}
	if db == nil {
		return MigrationReport{}, fmt.Errorf("%w: nil sqlite database", ErrIntegrityFailed)
	}
	target, err := TargetSchemaVersion(kind)
	if err != nil {
		return MigrationReport{}, err
	}

	dbPath, err := resolveMigrationDBPath(ctx, db, opts.DBPath)
	if err != nil {
		return MigrationReport{}, err
	}
	isFileBacked := dbPath != "" && dbPath != ":memory:"
	if isFileBacked {
		if fi, statErr := os.Lstat(dbPath); statErr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return MigrationReport{}, fmt.Errorf("%w: database path %q must not be a symlink", ErrIntegrityFailed, dbPath)
		}
		lock, lockErr := acquireMigrationLock(ctx, dbPath+".migrate.lock", opts.LockTimeout)
		if lockErr != nil {
			return MigrationReport{}, lockErr
		}
		defer func() { _ = releaseDatabaseOSLock(lock) }()
	}

	from, err := CurrentSchemaVersion(ctx, db)
	if err != nil {
		return MigrationReport{}, err
	}
	if from < 0 {
		return MigrationReport{}, fmt.Errorf("%w: negative user_version %d", ErrIntegrityFailed, from)
	}
	if from > target {
		return MigrationReport{
			Kind:     kind,
			From:     from,
			To:       target,
			ReadOnly: true,
		}, fmt.Errorf("%w: %s=%d target=%d", ErrSchemaTooNew, kind, from, target)
	}

	report := MigrationReport{
		Kind: kind,
		From: from,
		To:   target,
	}

	if from == target {
		if err := ValidateSchema(ctx, db, kind, target); err != nil {
			return report, err
		}
		hash, err := ComputeSchemaHash(ctx, db)
		if err != nil {
			return report, err
		}
		report.SchemaHash = hash
		return report, nil
	}

	tables, err := listUserTables(ctx, db)
	if err != nil {
		return report, err
	}
	if from == 0 {
		if len(tables) == 0 && opts.DisallowCreate {
			return report, fmt.Errorf("%w: refusing to initialize empty %s database", ErrIntegrityFailed, kind)
		}
		if len(tables) > 0 && !recognizesV0Schema(kind, tables) {
			return report, fmt.Errorf("%w: unrecognized v0 %s schema (tables=%v)", ErrIntegrityFailed, kind, tables)
		}
	}

	hasExistingData := from > 0 || len(tables) > 0
	if isFileBacked && hasExistingData {
		if err := maybeFail(opts, "backup.before"); err != nil {
			return report, fmt.Errorf("%w: %v", ErrBackupFailed, err)
		}
		backupPath := preMigrateBackupPath(dbPath, opts.BackupDir, from, target)
		manifest, manifestPath, bErr := backupDatabase(ctx, db, kind, dbPath, backupPath, from, target)
		if bErr != nil {
			return report, fmt.Errorf("%w: %v", ErrBackupFailed, bErr)
		}
		report.BackupPath = manifest.BackupPath
		report.ManifestPath = manifestPath
	}

	applied, err := runMigrationChain(ctx, db, kind, from, target, opts)
	if err != nil {
		return report, err
	}
	report.Applied = applied

	if err := ValidateSchema(ctx, db, kind, target); err != nil {
		return report, err
	}
	hash, err := ComputeSchemaHash(ctx, db)
	if err != nil {
		return report, err
	}
	report.SchemaHash = hash
	return report, nil
}

// Backup writes a consistent atomic backup of db to dst along with a JSON manifest at dst+".manifest.json".
func Backup(ctx context.Context, db *sqlite.DB, kind DBKind, dst string) (BackupManifest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return BackupManifest{}, err
	}
	if db == nil {
		return BackupManifest{}, fmt.Errorf("%w: nil sqlite database", ErrBackupFailed)
	}
	target, err := TargetSchemaVersion(kind)
	if err != nil {
		return BackupManifest{}, err
	}
	srcPath, err := resolveMigrationDBPath(ctx, db, "")
	if err != nil {
		return BackupManifest{}, fmt.Errorf("%w: %v", ErrBackupFailed, err)
	}
	if srcPath == "" || srcPath == ":memory:" {
		return BackupManifest{}, fmt.Errorf("%w: cannot back up in-memory database", ErrBackupFailed)
	}
	from, err := CurrentSchemaVersion(ctx, db)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("%w: %v", ErrBackupFailed, err)
	}
	manifest, _, err := backupDatabase(ctx, db, kind, srcPath, dst, from, target)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("%w: %v", ErrBackupFailed, err)
	}
	return manifest, nil
}

// VerifyBackup reads a backup manifest JSON file and verifies that the backup file exists
// and matches the manifest's recorded byte size and SHA-256 digest.
func VerifyBackup(manifestPath string) (BackupManifest, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("%w: read manifest: %v", ErrBackupFailed, err)
	}
	var m BackupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return BackupManifest{}, fmt.Errorf("%w: decode manifest: %v", ErrBackupFailed, err)
	}
	if strings.TrimSpace(m.BackupPath) == "" || len(m.SHA256) != sha256.Size*2 {
		return BackupManifest{}, fmt.Errorf("%w: incomplete manifest", ErrBackupFailed)
	}
	sum, size, err := hashFileSHA256(m.BackupPath)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("%w: read backup file: %v", ErrBackupFailed, err)
	}
	if size != m.SizeBytes || !strings.EqualFold(sum, m.SHA256) {
		return BackupManifest{}, fmt.Errorf("%w: backup digest or size mismatch (got size=%d sha256=%s, want size=%d sha256=%s)",
			ErrBackupFailed, size, sum, m.SizeBytes, m.SHA256)
	}
	return m, nil
}

// ValidateSchema checks that db matches kind's expected user_version and required tables/columns.
func ValidateSchema(ctx context.Context, db *sqlite.DB, kind DBKind, version int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return fmt.Errorf("%w: nil sqlite database", ErrIntegrityFailed)
	}
	actual, err := CurrentSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if actual != version {
		return fmt.Errorf("%w: %s user_version=%d want=%d", ErrIntegrityFailed, kind, actual, version)
	}
	tables, err := listUserTables(ctx, db)
	if err != nil {
		return err
	}
	tableSet := make(map[string]struct{}, len(tables))
	for _, t := range tables {
		tableSet[t] = struct{}{}
	}

	switch kind {
	case DBKindApp:
		requiredTables := []string{
			"clipboard_items",
			"clipboard_fts",
			"ime_user_lexicon",
			"ime_bigrams",
			"usage_frecency",
			"prompt_history",
			"lexicon_hits_daily",
			"ai_usage",
		}
		for _, req := range requiredTables {
			if _, ok := tableSet[req]; !ok {
				return fmt.Errorf("%w: missing app table %q", ErrIntegrityFailed, req)
			}
		}
		if version >= 2 {
			cols, err := tableColumns(ctx, db, "clipboard_items")
			if err != nil {
				return err
			}
			for _, mc := range mediaColumns {
				if _, ok := cols[mc[0]]; !ok {
					return fmt.Errorf("%w: clipboard_items missing column %q for v%d", ErrIntegrityFailed, mc[0], version)
				}
			}
			if err := validateByteSizeColumn(cols["byte_size"]); err != nil {
				return err
			}
		}
		return nil

	case DBKindDeviceState:
		if _, ok := tableSet["sync_device_state"]; !ok {
			return fmt.Errorf("%w: missing device_state table %q", ErrIntegrityFailed, "sync_device_state")
		}
		cols, err := tableColumns(ctx, db, "sync_device_state")
		if err != nil {
			return err
		}
		for _, col := range []string{"id", "watermark", "recovery_ready"} {
			if _, ok := cols[col]; !ok {
				return fmt.Errorf("%w: sync_device_state missing column %q", ErrIntegrityFailed, col)
			}
		}
		return nil

	case DBKindSync:
		for _, req := range []string{
			"sync_accounts",
			"sync_devices",
			"sync_objects",
			"sync_tombstones",
			"sync_current_tombstones",
			"sync_object_boundaries",
			"sync_audit",
		} {
			if _, ok := tableSet[req]; !ok {
				return fmt.Errorf("%w: missing sync table %q", ErrIntegrityFailed, req)
			}
		}
		return nil

	default:
		return fmt.Errorf("%w: %q", ErrUnknownDBKind, kind)
	}
}

func runMigrationChain(ctx context.Context, db *sqlite.DB, kind DBKind, from, to int, opts MigrationOptions) ([]string, error) {
	var (
		stmts   []sqlite.Statement
		applied []string
		postErr error
	)

	switch kind {
	case DBKindApp:
		for v := from; v < to; v++ {
			switch v {
			case 0:
				if err := maybeFail(opts, "app.v0_to_v1.before_ddl"); err != nil {
					return nil, fmt.Errorf("%w: %v", ErrMigrationFailed, err)
				}
				stmts = append(stmts, appV0ToV1Statements()...)
				applied = append(applied, "app:v0->v1")
				if err := maybeFail(opts, "app.v0_to_v1.after_version"); err != nil {
					postErr = err
					stmts = append(stmts, failpointAbortStatement())
				}
			case 1:
				if err := maybeFail(opts, "app.v1_to_v2.before_column"); err != nil {
					return nil, fmt.Errorf("%w: %v", ErrMigrationFailed, err)
				}
				v2Stmts, err := appV1ToV2Statements(ctx, db, from == 0)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, v2Stmts...)
				applied = append(applied, "app:v1->v2")
				if err := maybeFail(opts, "app.v1_to_v2.after_version"); err != nil {
					postErr = err
					stmts = append(stmts, failpointAbortStatement())
				}
			default:
				return nil, fmt.Errorf("%w: unknown app migration from v%d", ErrMigrationFailed, v)
			}
			if postErr != nil {
				break
			}
		}

	case DBKindDeviceState:
		for v := from; v < to; v++ {
			switch v {
			case 0:
				if err := maybeFail(opts, "device_state.v0_to_v1.before_ddl"); err != nil {
					return nil, fmt.Errorf("%w: %v", ErrMigrationFailed, err)
				}
				stmts = append(stmts,
					sqlite.Statement{SQL: `CREATE TABLE IF NOT EXISTS sync_device_state (id INTEGER PRIMARY KEY CHECK (id=1), watermark INTEGER NOT NULL DEFAULT 0, recovery_ready INTEGER NOT NULL DEFAULT 0)`},
					sqlite.Statement{SQL: `INSERT OR IGNORE INTO sync_device_state(id) VALUES(1)`},
					sqlite.Statement{SQL: `PRAGMA user_version = 1`},
				)
				applied = append(applied, "device_state:v0->v1")
				if err := maybeFail(opts, "device_state.v0_to_v1.after_version"); err != nil {
					postErr = err
					stmts = append(stmts, failpointAbortStatement())
				}
			default:
				return nil, fmt.Errorf("%w: unknown device_state migration from v%d", ErrMigrationFailed, v)
			}
			if postErr != nil {
				break
			}
		}

	case DBKindSync:
		for v := from; v < to; v++ {
			switch v {
			case 0:
				if err := maybeFail(opts, "sync.v0_to_v1.before_ddl"); err != nil {
					return nil, fmt.Errorf("%w: %v", ErrMigrationFailed, err)
				}
				stmts = append(stmts, syncV0ToV1Statements()...)
				applied = append(applied, "sync:v0->v1")
				if err := maybeFail(opts, "sync.v0_to_v1.after_version"); err != nil {
					postErr = err
					stmts = append(stmts, failpointAbortStatement())
				}
			default:
				return nil, fmt.Errorf("%w: unknown sync migration from v%d", ErrMigrationFailed, v)
			}
			if postErr != nil {
				break
			}
		}

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownDBKind, kind)
	}

	if len(stmts) == 0 {
		return applied, nil
	}
	if _, err := db.Transaction(ctx, stmts); err != nil {
		if postErr != nil {
			return nil, fmt.Errorf("%w: failpoint rollback: %v", ErrMigrationFailed, postErr)
		}
		return nil, fmt.Errorf("%w: transaction rolled back: %v", ErrMigrationFailed, err)
	}
	if postErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrMigrationFailed, postErr)
	}
	return applied, nil
}

// failpointAbortStatement executes a guaranteed SQLite statement error inside an active
// transaction after DDL and PRAGMA user_version have executed, proving real transactional rollback.
func failpointAbortStatement() sqlite.Statement {
	return sqlite.Statement{SQL: `INSERT INTO "__clavis_migration_failpoint_abort__" VALUES (1)`}
}

func appV0ToV1Statements() []sqlite.Statement {
	return []sqlite.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS clipboard_items (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL UNIQUE,
			source_bundle_id TEXT NOT NULL DEFAULT '',
			source_app_name TEXT NOT NULL DEFAULT '',
			created_at_ns INTEGER NOT NULL,
			updated_at_ns INTEGER NOT NULL,
			is_pinned INTEGER NOT NULL DEFAULT 0
		)`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_clipboard_items_order ON clipboard_items(is_pinned DESC, updated_at_ns DESC, id ASC)`},
		{SQL: `CREATE VIRTUAL TABLE IF NOT EXISTS clipboard_fts USING fts5(
			content,
			source_app_name,
			content='clipboard_items',
			content_rowid='rowid'
		)`},
		{SQL: clipboardFTSInsertTrigger},
		{SQL: clipboardFTSDeleteTrigger},
		{SQL: clipboardFTSUpdateTrigger},
		{SQL: `CREATE TABLE IF NOT EXISTS ime_user_lexicon (pinyin_key TEXT NOT NULL, word TEXT NOT NULL, frequency INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (pinyin_key, word))`},
		{SQL: `CREATE TABLE IF NOT EXISTS ime_bigrams (prev_word TEXT NOT NULL, word TEXT NOT NULL, transition_count INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (prev_word, word))`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_ime_lexicon_prefix ON ime_user_lexicon(pinyin_key)`},
		{SQL: `CREATE TABLE IF NOT EXISTS usage_frecency (item_id TEXT PRIMARY KEY, use_count INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0)`},
		{SQL: `CREATE TABLE IF NOT EXISTS prompt_history (id INTEGER PRIMARY KEY AUTOINCREMENT, prompt_id TEXT NOT NULL, trigger TEXT NOT NULL, raw_draft TEXT NOT NULL, final_output TEXT NOT NULL, preset TEXT NOT NULL, created_at_ns INTEGER NOT NULL)`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_prompt_history_recent ON prompt_history(prompt_id, created_at_ns DESC, id DESC)`},
		{SQL: `CREATE TABLE IF NOT EXISTS lexicon_hits_daily (day TEXT NOT NULL, hits INTEGER NOT NULL DEFAULT 0, misses INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(day))`},
		{SQL: `CREATE TABLE IF NOT EXISTS ai_usage (id INTEGER PRIMARY KEY AUTOINCREMENT, at_ns INTEGER NOT NULL, profile TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, action TEXT NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, cached_tokens INTEGER NOT NULL, reported INTEGER NOT NULL, latency_ms INTEGER NOT NULL)`},
		{SQL: `CREATE INDEX IF NOT EXISTS ai_usage_at ON ai_usage(at_ns)`},
		{SQL: `PRAGMA user_version = 1`},
	}
}

func appV1ToV2Statements(ctx context.Context, db *sqlite.DB, creatingFromV0 bool) ([]sqlite.Statement, error) {
	cols, err := tableColumns(ctx, db, "clipboard_items")
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 && !creatingFromV0 {
		return nil, fmt.Errorf("%w: clipboard_items table missing at app v1", ErrIntegrityFailed)
	}
	if existing, ok := cols["byte_size"]; ok {
		if err := validateByteSizeColumn(existing); err != nil {
			return nil, err
		}
	}

	stmts := make([]sqlite.Statement, 0, len(mediaColumns)+6)
	for _, col := range mediaColumns {
		if _, exists := cols[col[0]]; exists {
			continue
		}
		stmts = append(stmts, sqlite.Statement{
			SQL: `ALTER TABLE clipboard_items ADD COLUMN ` + col[0] + ` ` + col[1],
		})
	}
	stmts = append(stmts,
		sqlite.Statement{SQL: `DROP TRIGGER IF EXISTS clipboard_items_au`},
		sqlite.Statement{SQL: clipboardFTSUpdateTrigger},
		sqlite.Statement{SQL: `UPDATE clipboard_items SET kind='text', byte_size=length(CAST(content AS BLOB))
			WHERE kind='' OR (kind='text' AND byte_size=0 AND content<>'')`},
		sqlite.Statement{SQL: `CREATE INDEX IF NOT EXISTS idx_clipboard_items_updated ON clipboard_items(updated_at_ns DESC, id DESC)`},
		sqlite.Statement{SQL: `INSERT INTO clipboard_fts(clipboard_fts) VALUES ('rebuild')`},
		sqlite.Statement{SQL: `PRAGMA user_version = 2`},
	)
	return stmts, nil
}

func syncV0ToV1Statements() []sqlite.Statement {
	return []sqlite.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS sync_accounts (account_id TEXT PRIMARY KEY, credential BLOB NOT NULL, sequence INTEGER NOT NULL DEFAULT 0)`},
		{SQL: `CREATE TABLE IF NOT EXISTS sync_devices (account_id TEXT NOT NULL, device_id TEXT NOT NULL, public_key BLOB NOT NULL, watermark INTEGER NOT NULL DEFAULT 0, issued INTEGER NOT NULL DEFAULT 0, issued_from INTEGER NOT NULL DEFAULT 0, issued_to INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(account_id, device_id), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `CREATE TABLE IF NOT EXISTS sync_objects (account_id TEXT NOT NULL, object_id TEXT NOT NULL, version INTEGER NOT NULL, envelope BLOB NOT NULL, PRIMARY KEY(account_id, object_id), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `CREATE TABLE IF NOT EXISTS sync_tombstones (account_id TEXT NOT NULL, object_id TEXT NOT NULL, device_id TEXT NOT NULL, version INTEGER NOT NULL, sequence INTEGER NOT NULL, issued_at_ns INTEGER NOT NULL, deleted_at_ns INTEGER NOT NULL, protocol_version INTEGER NOT NULL, signature BLOB NOT NULL, PRIMARY KEY(account_id, sequence), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `CREATE TABLE IF NOT EXISTS sync_current_tombstones (account_id TEXT NOT NULL, object_id TEXT NOT NULL, device_id TEXT NOT NULL, version INTEGER NOT NULL, sequence INTEGER NOT NULL, issued_at_ns INTEGER NOT NULL, deleted_at_ns INTEGER NOT NULL, protocol_version INTEGER NOT NULL, signature BLOB NOT NULL, PRIMARY KEY(account_id, object_id), UNIQUE(account_id, sequence), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `CREATE TABLE IF NOT EXISTS sync_object_boundaries (account_id TEXT NOT NULL, object_id TEXT NOT NULL, version INTEGER NOT NULL, PRIMARY KEY(account_id, object_id), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_sync_tombstones_cursor ON sync_tombstones(account_id, sequence)`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_sync_tombstones_object ON sync_tombstones(account_id, object_id, sequence)`},
		{SQL: `CREATE TABLE IF NOT EXISTS sync_audit (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id TEXT NOT NULL, object_digest TEXT NOT NULL, device_id TEXT NOT NULL, at_ns INTEGER NOT NULL, action TEXT NOT NULL, FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_sync_audit_account ON sync_audit(account_id, at_ns, id)`},
		{SQL: `PRAGMA user_version = 1`},
	}
}

type columnInfo struct {
	name      string
	colType   string
	notNull   bool
	dfltValue string
	hasDflt   bool
}

func tableColumns(ctx context.Context, db *sqlite.DB, table string) (map[string]columnInfo, error) {
	// Strict whitelist prevents any SQL injection through PRAGMA table_info.
	switch table {
	case "clipboard_items", "sync_device_state":
	default:
		return nil, fmt.Errorf("%w: unexpected table inspection %q", ErrIntegrityFailed, table)
	}
	rows, err := db.Query(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	nameIdx := slices.Index(rows.Columns, "name")
	typeIdx := slices.Index(rows.Columns, "type")
	notNullIdx := slices.Index(rows.Columns, "notnull")
	dfltIdx := slices.Index(rows.Columns, "dflt_value")
	out := make(map[string]columnInfo, len(rows.Values))
	for _, r := range rows.Values {
		if nameIdx < 0 || nameIdx >= len(r) {
			continue
		}
		name := str(r, nameIdx)
		if name == "" {
			continue
		}
		info := columnInfo{name: name}
		if typeIdx >= 0 && typeIdx < len(r) {
			info.colType = str(r, typeIdx)
		}
		if notNullIdx >= 0 && notNullIdx < len(r) {
			info.notNull = int64val(r, notNullIdx) != 0
		}
		if dfltIdx >= 0 && dfltIdx < len(r) && r[dfltIdx] != nil {
			info.hasDflt = true
			info.dfltValue = strings.TrimSpace(fmt.Sprint(r[dfltIdx]))
		}
		out[name] = info
	}
	return out, nil
}

func validateByteSizeColumn(col columnInfo) error {
	if !strings.EqualFold(strings.TrimSpace(col.colType), "INTEGER") || !col.notNull || !col.hasDflt || col.dfltValue != "0" {
		return fmt.Errorf("%w: clipboard_items.byte_size has incompatible definition (type=%q notnull=%v dflt=%q)",
			ErrIntegrityFailed, col.colType, col.notNull, col.dfltValue)
	}
	return nil
}

func listUserTables(ctx context.Context, db *sqlite.DB) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("list user tables: %w", err)
	}
	tables := make([]string, 0, len(rows.Values))
	for _, r := range rows.Values {
		if n := str(r, 0); n != "" {
			tables = append(tables, n)
		}
	}
	return tables, nil
}

func recognizesV0Schema(kind DBKind, tables []string) bool {
	var known map[string]struct{}
	switch kind {
	case DBKindApp:
		known = map[string]struct{}{
			"clipboard_items":    {},
			"clipboard_fts":      {},
			"ime_user_lexicon":   {},
			"ime_bigrams":        {},
			"usage_frecency":     {},
			"prompt_history":     {},
			"lexicon_hits_daily": {},
			"ai_usage":           {},
		}
	case DBKindDeviceState:
		known = map[string]struct{}{
			"sync_device_state": {},
		}
	case DBKindSync:
		known = map[string]struct{}{
			"sync_accounts":           {},
			"sync_devices":            {},
			"sync_objects":            {},
			"sync_tombstones":         {},
			"sync_current_tombstones": {},
			"sync_object_boundaries":  {},
			"sync_audit":              {},
		}
	default:
		return false
	}
	for _, t := range tables {
		if _, ok := known[t]; ok {
			return true
		}
	}
	return false
}

func resolveMigrationDBPath(ctx context.Context, db *sqlite.DB, explicit string) (string, error) {
	if explicit == ":memory:" {
		return ":memory:", nil
	}
	if strings.TrimSpace(explicit) != "" {
		return filepath.Clean(explicit), nil
	}
	rows, err := db.Query(ctx, `PRAGMA database_list`)
	if err != nil {
		return "", fmt.Errorf("inspect database path: %w", err)
	}
	nameIdx := slices.Index(rows.Columns, "name")
	fileIdx := slices.Index(rows.Columns, "file")
	for _, r := range rows.Values {
		if nameIdx >= 0 && fileIdx >= 0 && str(r, nameIdx) == "main" {
			file := strings.TrimSpace(str(r, fileIdx))
			if file == "" {
				return ":memory:", nil
			}
			return filepath.Clean(file), nil
		}
	}
	return ":memory:", nil
}

func preMigrateBackupPath(dbPath, backupDir string, from, to int) string {
	fileName := fmt.Sprintf("%s.pre-migrate-v%d-to-v%d.bak", filepath.Base(dbPath), from, to)
	if strings.TrimSpace(backupDir) != "" {
		return filepath.Join(backupDir, fileName)
	}
	return filepath.Join(filepath.Dir(dbPath), fileName)
}

func backupDatabase(ctx context.Context, db *sqlite.DB, kind DBKind, srcPath, dstPath string, from, to int) (BackupManifest, string, error) {
	walCheckpointed, err := checkpointWALForBackup(ctx, db)
	if err != nil {
		return BackupManifest{}, "", err
	}
	schemaHash, err := ComputeSchemaHash(ctx, db)
	if err != nil {
		return BackupManifest{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return BackupManifest{}, "", fmt.Errorf("create backup dir: %w", err)
	}

	sum, size, err := atomicCopyFileWithSHA256(srcPath, dstPath)
	if err != nil {
		return BackupManifest{}, "", err
	}

	// Re-read the published backup file to verify integrity before trusting it.
	verifySum, verifySize, err := hashFileSHA256(dstPath)
	if err != nil {
		return BackupManifest{}, "", fmt.Errorf("verify backup file: %w", err)
	}
	if verifySize != size || verifySum != sum {
		return BackupManifest{}, "", fmt.Errorf("backup verification mismatch: size=%d/%d", verifySize, size)
	}

	manifest := BackupManifest{
		Kind:            kind,
		SourcePath:      srcPath,
		BackupPath:      dstPath,
		FromVersion:     from,
		ToVersion:       to,
		SchemaVersion:   from,
		SchemaHash:      schemaHash,
		SizeBytes:       size,
		SHA256:          sum,
		WALCheckpointed: walCheckpointed,
		CreatedAt:       time.Now().UTC(),
	}
	manifestPath := dstPath + ".manifest.json"
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupManifest{}, "", fmt.Errorf("marshal backup manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := atomicWriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		return BackupManifest{}, "", fmt.Errorf("write backup manifest: %w", err)
	}
	return manifest, manifestPath, nil
}

func checkpointWALForBackup(ctx context.Context, db *sqlite.DB) (bool, error) {
	rows, err := db.Query(ctx, `PRAGMA journal_mode`)
	if err != nil {
		return false, fmt.Errorf("query journal_mode: %w", err)
	}
	isWAL := len(rows.Values) > 0 && strings.EqualFold(str(rows.Values[0], 0), "wal")
	if !isWAL {
		return false, nil
	}
	cpRows, err := db.Query(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err != nil {
		return false, fmt.Errorf("checkpoint WAL before backup: %w", err)
	}
	if len(cpRows.Values) > 0 && len(cpRows.Values[0]) > 0 && int64val(cpRows.Values[0], 0) != 0 {
		return false, fmt.Errorf("WAL checkpoint busy: active reader or writer prevented consistent backup")
	}
	return true, nil
}

func atomicCopyFileWithSHA256(srcPath, dstPath string) (string, int64, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", 0, fmt.Errorf("open source db for backup: %w", err)
	}
	defer src.Close()

	dir := filepath.Dir(dstPath)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dstPath)+".tmp.*")
	if err != nil {
		return "", 0, fmt.Errorf("create backup temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return "", 0, fmt.Errorf("chmod backup temp file: %w", err)
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err != nil {
		cleanup()
		return "", 0, fmt.Errorf("copy backup bytes: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", 0, fmt.Errorf("sync backup file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("close backup file: %w", err)
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("rename backup file: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func atomicWriteFile(dstPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dstPath)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func hashFileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func acquireMigrationLock(ctx context.Context, lockPath string, timeout time.Duration) (*databaseOSLock, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("%w: create lock dir: %v", ErrMigrationBusy, err)
	}
	var deadline time.Time
	switch {
	case timeout < 0:
		deadline = time.Now()
	case timeout == 0:
		deadline = time.Now().Add(defaultMigrationLockTimeout)
	default:
		deadline = time.Now().Add(timeout)
	}
	for {
		lock, err := acquireDatabaseOSLock(lockPath)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, errDatabaseLockBusy) {
			return nil, fmt.Errorf("%w: %v", ErrMigrationBusy, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: timed out acquiring %s", ErrMigrationBusy, lockPath)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrMigrationBusy, ctx.Err())
		case <-time.After(migrationLockPollInterval):
		}
	}
}

func maybeFail(opts MigrationOptions, step string) error {
	if opts.Failpoint == nil {
		return nil
	}
	return opts.Failpoint(step)
}
