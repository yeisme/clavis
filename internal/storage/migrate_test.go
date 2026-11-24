package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func TestMigrateAppFreshDB(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	report, err := Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true})
	if err != nil {
		t.Fatalf("Migrate fresh app db: %v", err)
	}
	if report.Kind != DBKindApp || report.From != 0 || report.To != AppSchemaVersion || report.ReadOnly {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Applied) != 2 || report.Applied[0] != "app:v0->v1" || report.Applied[1] != "app:v1->v2" {
		t.Fatalf("unexpected applied steps: %v", report.Applied)
	}
	if report.BackupPath != "" || report.ManifestPath != "" {
		t.Fatalf("fresh empty db should not create backup files: %+v", report)
	}
	if report.SchemaHash == "" {
		t.Fatal("expected non-empty SchemaHash")
	}
	ver, err := CurrentSchemaVersion(ctx, db)
	if err != nil || ver != AppSchemaVersion {
		t.Fatalf("user_version=%d err=%v, want %d", ver, err, AppSchemaVersion)
	}
	if err := ValidateSchema(ctx, db, DBKindApp, AppSchemaVersion); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
}

func TestMigrateAppLegacyV0UpgradeWithBackupManifestAndWAL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "clipboard.db")

	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy v0 app DB running in active WAL mode before media columns existed.
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE clipboard_items (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL UNIQUE,
			source_bundle_id TEXT NOT NULL DEFAULT '',
			source_app_name TEXT NOT NULL DEFAULT '',
			created_at_ns INTEGER NOT NULL,
			updated_at_ns INTEGER NOT NULL,
			is_pinned INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE ime_user_lexicon (pinyin_key TEXT NOT NULL, word TEXT NOT NULL, frequency INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (pinyin_key, word))`,
		`INSERT INTO clipboard_items VALUES ('legacy-1', '你好世界', 'h-zh', 'com.apple.Notes', 'Notes', 100, 200, 1)`,
		`INSERT INTO ime_user_lexicon VALUES ('nihao', '你好', 7, 300, 1)`,
	} {
		if _, err := db.Execute(ctx, stmt); err != nil {
			t.Fatalf("seed legacy v0 db (%s): %v", stmt, err)
		}
	}

	report, err := Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true})
	if err != nil {
		t.Fatalf("Migrate legacy v0 db: %v", err)
	}
	if report.From != 0 || report.To != AppSchemaVersion {
		t.Fatalf("report versions = %d -> %d, want 0 -> %d", report.From, report.To, AppSchemaVersion)
	}
	wantBackup := dbPath + ".pre-migrate-v0-to-v2.bak"
	wantManifest := wantBackup + ".manifest.json"
	assertSameFilePath(t, report.BackupPath, wantBackup)
	assertSameFilePath(t, report.ManifestPath, wantManifest)

	manifest, err := VerifyBackup(report.ManifestPath)
	if err != nil {
		t.Fatalf("VerifyBackup failed: %v", err)
	}
	if manifest.Kind != DBKindApp || manifest.FromVersion != 0 || manifest.ToVersion != AppSchemaVersion || !manifest.WALCheckpointed {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if manifest.SizeBytes <= 0 || len(manifest.SHA256) != 64 || manifest.SchemaHash == "" {
		t.Fatalf("incomplete manifest digests: %+v", manifest)
	}

	// Open the backup file independently to confirm WAL rows were flushed and are readable.
	bakDB, err := sqlite.Open(ctx, manifest.BackupPath, sqlite.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer bakDB.Close()
	bakRows, err := bakDB.Query(ctx, `SELECT content FROM clipboard_items WHERE id='legacy-1'`)
	if err != nil || len(bakRows.Values) != 1 || str(bakRows.Values[0], 0) != "你好世界" {
		t.Fatalf("backup db missing WAL row: rows=%v err=%v", bakRows.Values, err)
	}
	bakVer, err := CurrentSchemaVersion(ctx, bakDB)
	if err != nil || bakVer != 0 {
		t.Fatalf("backup db user_version=%d err=%v, want 0", bakVer, err)
	}

	// Verify upgraded DB preserved rows and backfilled UTF-8 byte_size.
	rows, err := db.Query(ctx, `SELECT content, kind, byte_size FROM clipboard_items WHERE id='legacy-1'`)
	if err != nil || len(rows.Values) != 1 {
		t.Fatalf("query migrated clipboard row: rows=%v err=%v", rows.Values, err)
	}
	if str(rows.Values[0], 0) != "你好世界" || str(rows.Values[0], 1) != "text" || int64val(rows.Values[0], 2) != int64(len([]byte("你好世界"))) {
		t.Fatalf("migrated clipboard row = %v, want content=你好世界 kind=text byte_size=%d", rows.Values[0], len([]byte("你好世界")))
	}
	lexRows, err := db.Query(ctx, `SELECT frequency, pinned FROM ime_user_lexicon WHERE pinyin_key='nihao' AND word='你好'`)
	if err != nil || len(lexRows.Values) != 1 || int64val(lexRows.Values[0], 0) != 7 || int64val(lexRows.Values[0], 1) != 1 {
		t.Fatalf("legacy lexicon row altered: rows=%v err=%v", lexRows.Values, err)
	}
	_ = db.Close()
}

func TestMigrateAppV1ToV2AndIdempotentRerun(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "v1.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Seed a v1 database (all v1 tables, no byte_size column yet, user_version=1).
	if _, err := db.Transaction(ctx, appV0ToV1Statements()); err != nil {
		t.Fatalf("seed v1 db: %v", err)
	}
	if _, err := db.Execute(ctx, `INSERT INTO clipboard_items(id,content,content_hash,source_bundle_id,source_app_name,created_at_ns,updated_at_ns,is_pinned) VALUES('row-1','中文测试','h1','','Notes',10,20,0)`); err != nil {
		t.Fatalf("insert v1 row: %v", err)
	}

	first, err := Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true})
	if err != nil {
		t.Fatalf("v1->v2 Migrate: %v", err)
	}
	if first.From != 1 || first.To != 2 || len(first.Applied) != 1 || first.Applied[0] != "app:v1->v2" {
		t.Fatalf("unexpected v1->v2 report: %+v", first)
	}
	assertSameFilePath(t, first.BackupPath, dbPath+".pre-migrate-v1-to-v2.bak")
	assertSameFilePath(t, first.ManifestPath, dbPath+".pre-migrate-v1-to-v2.bak.manifest.json")
	if _, err := VerifyBackup(first.ManifestPath); err != nil {
		t.Fatalf("VerifyBackup v1->v2: %v", err)
	}

	rows, err := db.Query(ctx, `SELECT byte_size FROM clipboard_items WHERE id='row-1'`)
	if err != nil || len(rows.Values) != 1 || int64val(rows.Values[0], 0) != int64(len([]byte("中文测试"))) {
		t.Fatalf("backfilled byte_size=%v err=%v, want %d", rows.Values, err, len([]byte("中文测试")))
	}

	// Re-running Migrate must be a zero-write, idempotent no-op without duplicate column errors.
	second, err := Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true})
	if err != nil {
		t.Fatalf("idempotent rerun Migrate: %v", err)
	}
	if second.From != 2 || second.To != 2 || len(second.Applied) != 0 || second.BackupPath != "" {
		t.Fatalf("unexpected idempotent rerun report: %+v", second)
	}
	if second.SchemaHash != first.SchemaHash {
		t.Fatalf("schema hash changed across idempotent rerun: %s vs %s", second.SchemaHash, first.SchemaHash)
	}
	cntRows, err := db.Query(ctx, `SELECT COUNT(*) FROM clipboard_items`)
	if err != nil || len(cntRows.Values) != 1 || int64val(cntRows.Values[0], 0) != 1 {
		t.Fatalf("row count changed: %v err=%v", cntRows.Values, err)
	}
}

func TestMigrateSchemaTooNewRejectedReadOnly(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "future.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(ctx, `CREATE TABLE future_table (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(ctx, `PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	beforeHash, beforeSize, err := hashFileSHA256(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Direct Migrate call rejects with ErrSchemaTooNew + ReadOnly=true.
	db2, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Migrate(ctx, db2, DBKindApp, MigrationOptions{AllowCreate: true})
	if !errors.Is(err, ErrSchemaTooNew) || !strings.Contains(err.Error(), "schema_too_new") {
		t.Fatalf("expected ErrSchemaTooNew, got report=%+v err=%v", report, err)
	}
	if !report.ReadOnly || report.From != 99 || report.To != AppSchemaVersion || report.BackupPath != "" {
		t.Fatalf("unexpected schema_too_new report: %+v", report)
	}
	// LearningStore must also reject schema_too_new.
	if _, err := NewLearningStore(db2); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("NewLearningStore expected ErrSchemaTooNew, got %v", err)
	}
	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}

	// SQLiteClipboardStore must also reject schema_too_new without modifying the file.
	if _, err := NewSQLiteClipboardStore(ctx, dbPath, clipboard.NewPrivacyFilter()); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("NewSQLiteClipboardStore expected ErrSchemaTooNew, got %v", err)
	}

	afterHash, afterSize, err := hashFileSHA256(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash != afterHash || beforeSize != afterSize {
		t.Fatalf("schema_too_new modified original db file: before=(%d,%s) after=(%d,%s)", beforeSize, beforeHash, afterSize, afterHash)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dbPath)); len(entries) > 2 {
		// Only future.db and optionally future.db.migrate.lock should exist; no .bak files.
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".bak") || strings.HasSuffix(e.Name(), ".manifest.json") {
				t.Fatalf("schema_too_new unexpectedly created backup artifact %q", e.Name())
			}
		}
	}
}

func TestMigrateFailpointRollbackLeavesPriorVersionIntact(t *testing.T) {
	ctx := context.Background()
	for _, step := range []string{"app.v1_to_v2.before_column", "app.v1_to_v2.after_version"} {
		t.Run(step, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "rollback.db")
			db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			if _, err := db.Transaction(ctx, appV0ToV1Statements()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Execute(ctx, `INSERT INTO clipboard_items(id,content,content_hash,source_bundle_id,source_app_name,created_at_ns,updated_at_ns,is_pinned) VALUES('keep-1','hello','h1','','App',1,2,0)`); err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected failpoint: " + step)
			_, err = Migrate(ctx, db, DBKindApp, MigrationOptions{
				AllowCreate: true,
				Failpoint: func(s string) error {
					if s == step {
						return injected
					}
					return nil
				},
			})
			if !errors.Is(err, ErrMigrationFailed) {
				t.Fatalf("expected ErrMigrationFailed for %s, got %v", step, err)
			}

			// Verify rollback kept user_version at 1, no byte_size column added, and row intact.
			ver, err := CurrentSchemaVersion(ctx, db)
			if err != nil || ver != 1 {
				t.Fatalf("after rollback user_version=%d err=%v, want 1", ver, err)
			}
			cols, err := tableColumns(ctx, db, "clipboard_items")
			if err != nil {
				t.Fatal(err)
			}
			if _, hasByteSize := cols["byte_size"]; hasByteSize {
				t.Fatalf("after rollback byte_size column leaked into v1 schema: %+v", cols)
			}
			rows, err := db.Query(ctx, `SELECT content FROM clipboard_items WHERE id='keep-1'`)
			if err != nil || len(rows.Values) != 1 || str(rows.Values[0], 0) != "hello" {
				t.Fatalf("after rollback row corrupted: %v err=%v", rows.Values, err)
			}

			// Re-running without failpoint succeeds cleanly from v1 to v2.
			report, err := Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true})
			if err != nil {
				t.Fatalf("recovery Migrate after rollback: %v", err)
			}
			if report.From != 1 || report.To != 2 {
				t.Fatalf("unexpected recovery report: %+v", report)
			}
		})
	}

	// Also prove v0->v1 transactional rollback keeps user_version=0 on after_version failpoint.
	t.Run("app.v0_to_v1.after_version", func(t *testing.T) {
		db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		_, err = Migrate(ctx, db, DBKindApp, MigrationOptions{
			AllowCreate: true,
			Failpoint: func(s string) error {
				if s == "app.v0_to_v1.after_version" {
					return errors.New("abort v0->v1")
				}
				return nil
			},
		})
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("expected ErrMigrationFailed, got %v", err)
		}
		ver, err := CurrentSchemaVersion(ctx, db)
		if err != nil || ver != 0 {
			t.Fatalf("after v0 rollback user_version=%d err=%v, want 0", ver, err)
		}
		tables, err := listUserTables(ctx, db)
		if err != nil || len(tables) != 0 {
			t.Fatalf("after v0 rollback tables leaked: %v err=%v", tables, err)
		}
	})
}

func TestMigrateRejectsUnrecognizedV0AndIncompatibleByteSize(t *testing.T) {
	ctx := context.Background()

	t.Run("unrecognized-v0-foreign-db", func(t *testing.T) {
		db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Execute(ctx, `CREATE TABLE codex_threads (id TEXT PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		_, err = Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true})
		if !errors.Is(err, ErrIntegrityFailed) {
			t.Fatalf("expected ErrIntegrityFailed for foreign v0 db, got %v", err)
		}
		ver, _ := CurrentSchemaVersion(ctx, db)
		if ver != 0 {
			t.Fatalf("foreign db user_version mutated to %d", ver)
		}
	})

	t.Run("incompatible-byte-size-column", func(t *testing.T) {
		db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Transaction(ctx, appV0ToV1Statements()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Execute(ctx, `ALTER TABLE clipboard_items ADD COLUMN byte_size TEXT`); err != nil {
			t.Fatal(err)
		}
		_, err = Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true})
		if !errors.Is(err, ErrIntegrityFailed) {
			t.Fatalf("expected ErrIntegrityFailed for wrong byte_size type, got %v", err)
		}
	})
}

func TestMigrateDeviceStateAndSyncNamespaces(t *testing.T) {
	ctx := context.Background()

	// DeviceState: v0 -> v1, idempotent rerun, failpoint rollback, and independent schema_too_new.
	dsPath := filepath.Join(t.TempDir(), "device_state.db")
	db, err := sqlite.Open(ctx, dsPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Prove failpoint rollback on device_state v0->v1.
	if _, err := Migrate(ctx, db, DBKindDeviceState, MigrationOptions{
		AllowCreate: true,
		Failpoint: func(step string) error {
			if step == "device_state.v0_to_v1.after_version" {
				return errors.New("abort device_state")
			}
			return nil
		},
	}); !errors.Is(err, ErrMigrationFailed) {
		t.Fatalf("expected ErrMigrationFailed, got %v", err)
	}
	if ver, _ := CurrentSchemaVersion(ctx, db); ver != 0 {
		t.Fatalf("device_state user_version after rollback = %d, want 0", ver)
	}

	report, err := Migrate(ctx, db, DBKindDeviceState, MigrationOptions{AllowCreate: true})
	if err != nil {
		t.Fatalf("Migrate device_state: %v", err)
	}
	if report.From != 0 || report.To != DeviceStateSchemaVersion || len(report.Applied) != 1 {
		t.Fatalf("unexpected device_state report: %+v", report)
	}
	rows, err := db.Query(ctx, `SELECT watermark, recovery_ready FROM sync_device_state WHERE id=1`)
	if err != nil || len(rows.Values) != 1 || int64val(rows.Values[0], 0) != 0 {
		t.Fatalf("sync_device_state row=%v err=%v", rows.Values, err)
	}

	// Idempotent rerun.
	rerun, err := Migrate(ctx, db, DBKindDeviceState, MigrationOptions{AllowCreate: true})
	if err != nil || len(rerun.Applied) != 0 || rerun.From != 1 || rerun.To != 1 {
		t.Fatalf("device_state rerun=%+v err=%v", rerun, err)
	}

	// Version 2 is valid for App, but schema_too_new for DeviceState (independent version space!).
	if _, err := db.Execute(ctx, `PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	tooNew, err := Migrate(ctx, db, DBKindDeviceState, MigrationOptions{})
	if !errors.Is(err, ErrSchemaTooNew) || !tooNew.ReadOnly {
		t.Fatalf("expected ErrSchemaTooNew for device_state v2, got report=%+v err=%v", tooNew, err)
	}

	// Sync namespace target & schema_too_new check.
	syncDB, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer syncDB.Close()
	syncRep, err := Migrate(ctx, syncDB, DBKindSync, MigrationOptions{AllowCreate: true})
	if err != nil || syncRep.To != SyncSchemaVersion {
		t.Fatalf("Migrate sync: report=%+v err=%v", syncRep, err)
	}
	if _, err := syncDB.Execute(ctx, `PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, syncDB, DBKindSync, MigrationOptions{}); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("expected ErrSchemaTooNew for sync v5, got %v", err)
	}
}

func TestMigrateLockBusy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "busy.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	lock, err := acquireDatabaseOSLock(dbPath + ".migrate.lock")
	if err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	defer func() { _ = releaseDatabaseOSLock(lock) }()

	_, err = Migrate(ctx, db, DBKindApp, MigrationOptions{
		AllowCreate: true,
		LockTimeout: -1,
	})
	if !errors.Is(err, ErrMigrationBusy) {
		t.Fatalf("expected ErrMigrationBusy when lock is held, got %v", err)
	}
}

func assertSameFilePath(t *testing.T, got, want string) {
	t.Helper()
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat got path %q: %v", got, err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat want path %q: %v", want, err)
	}
	if !os.SameFile(gotInfo, wantInfo) || filepath.Base(got) != filepath.Base(want) {
		t.Fatalf("path mismatch: got=%q want=%q", got, want)
	}
}
