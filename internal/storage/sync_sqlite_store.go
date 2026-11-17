package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	stdsync "sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

// SQLiteStore persists protocol metadata in a dedicated set of tables. It is
// intentionally independent of the in-memory Server so the latter remains a
// useful test substitute. NewServerStore provides the explicit adapter when a
// caller wants to use the old Server through this contract.
type SQLiteSyncStore struct {
	db     *sqlite.DB
	mu     stdsync.RWMutex
	ownsDB bool
	path   string
}

// SQLiteStore is retained as a package-local compatibility name for callers
// which used the initial storage implementation.
type SQLiteStore = SQLiteSyncStore

var _ coresync.SyncStore = (*SQLiteSyncStore)(nil)

// NewSQLiteSyncStore opens dbPath (or :memory:) and initializes the sync schema.
// A file-backed store is created with a private directory and file mode.
// NewSQLiteStore is the migration-compatible constructor name. SQLite-backed
// storage intentionally lives in internal/storage rather than pkg/core/sync.
func NewSQLiteStore(ctx context.Context, dbPath string) (*SQLiteSyncStore, error) {
	return NewSQLiteSyncStore(ctx, dbPath)
}

func NewSQLiteSyncStore(ctx context.Context, dbPath string) (*SQLiteSyncStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if dbPath == "" {
		return nil, fmt.Errorf("%w: database path is empty", coresync.ErrStoreInvalidArgument)
	}
	file := dbPath != ":memory:"
	if file {
		var err error
		dbPath, err = secureDatabasePath(dbPath)
		if err != nil {
			return nil, fmt.Errorf("sync: unsafe database path: %w", err)
		}
		if err := mkdirPrivate(filepath.Dir(dbPath)); err != nil {
			return nil, fmt.Errorf("sync: create database directory: %w", err)
		}
		if err := rejectDatabaseFiles(dbPath); err != nil {
			return nil, fmt.Errorf("sync: unsafe database path: %w", err)
		}
	}
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{BusyTimeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("sync: open sqlite store: %w", err)
	}
	s := &SQLiteSyncStore{db: db, ownsDB: true}
	if file {
		s.path = dbPath
	}
	if err := s.initSchema(ctx, file); err != nil {
		_ = db.Close()
		return nil, err
	}
	if file {
		if err := protectDatabaseFiles(dbPath); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sync: protect database files: %w", err)
		}
	}
	return s, nil
}

// NewSQLiteSyncStoreFromDB attaches the sync tables to an existing MyGo SQLite DB.
// The owner of db remains responsible for closing it.
func NewSQLiteSyncStoreFromDB(db *sqlite.DB) (*SQLiteSyncStore, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil sqlite database", coresync.ErrStoreInvalidArgument)
	}
	s := &SQLiteSyncStore{db: db, ownsDB: false}
	if err := s.initSchema(context.Background(), false); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SQLiteSyncStore) initSchema(ctx context.Context, wal bool) error {
	if err := s.migrateLegacyTombstones(ctx); err != nil {
		return err
	}
	if wal {
		if _, err := s.db.Execute(ctx, `PRAGMA journal_mode=WAL`); err != nil {
			return fmt.Errorf("sync: enable WAL: %w", err)
		}
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sync_accounts (account_id TEXT PRIMARY KEY, credential BLOB NOT NULL, sequence INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS sync_devices (account_id TEXT NOT NULL, device_id TEXT NOT NULL, public_key BLOB NOT NULL, watermark INTEGER NOT NULL DEFAULT 0, issued INTEGER NOT NULL DEFAULT 0, issued_from INTEGER NOT NULL DEFAULT 0, issued_to INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(account_id, device_id), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`,
		`CREATE TABLE IF NOT EXISTS sync_objects (account_id TEXT NOT NULL, object_id TEXT NOT NULL, version INTEGER NOT NULL, envelope BLOB NOT NULL, PRIMARY KEY(account_id, object_id), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`,
		// The log is append-only: one object may receive multiple higher-version
		// deletes. sync_current_tombstones is the permanent object boundary.
		`CREATE TABLE IF NOT EXISTS sync_tombstones (account_id TEXT NOT NULL, object_id TEXT NOT NULL, device_id TEXT NOT NULL, version INTEGER NOT NULL, sequence INTEGER NOT NULL, issued_at_ns INTEGER NOT NULL, deleted_at_ns INTEGER NOT NULL, protocol_version INTEGER NOT NULL, signature BLOB NOT NULL, PRIMARY KEY(account_id, sequence), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`,
		`CREATE TABLE IF NOT EXISTS sync_current_tombstones (account_id TEXT NOT NULL, object_id TEXT NOT NULL, device_id TEXT NOT NULL, version INTEGER NOT NULL, sequence INTEGER NOT NULL, issued_at_ns INTEGER NOT NULL, deleted_at_ns INTEGER NOT NULL, protocol_version INTEGER NOT NULL, signature BLOB NOT NULL, PRIMARY KEY(account_id, object_id), UNIQUE(account_id, sequence), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`,
		`CREATE TABLE IF NOT EXISTS sync_object_boundaries (account_id TEXT NOT NULL, object_id TEXT NOT NULL, version INTEGER NOT NULL, PRIMARY KEY(account_id, object_id), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`,
		`INSERT INTO sync_object_boundaries(account_id,object_id,version) SELECT account_id,object_id,version FROM sync_current_tombstones WHERE 1 ON CONFLICT(account_id,object_id) DO UPDATE SET version=MAX(version,excluded.version)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_tombstones_cursor ON sync_tombstones(account_id, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_tombstones_object ON sync_tombstones(account_id, object_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS sync_audit (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id TEXT NOT NULL, object_digest TEXT NOT NULL, device_id TEXT NOT NULL, at_ns INTEGER NOT NULL, action TEXT NOT NULL, FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`,
		`CREATE INDEX IF NOT EXISTS idx_sync_audit_account ON sync_audit(account_id, at_ns, id)`,
		`PRAGMA user_version = 1`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Execute(ctx, stmt); err != nil {
			return fmt.Errorf("sync: initialize schema: %w", err)
		}
	}
	return nil
}

// migrateLegacyTombstones upgrades the pre-persistent prototype schema, whose
// tombstone table used (account_id, object_id) as its primary key. The old
// rows are copied into the append-only log and current-boundary table in one
// transaction; the renamed table is retained as a rollback copy until an
// operator explicitly removes it.
func (s *SQLiteSyncStore) migrateLegacyTombstones(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `PRAGMA table_info(sync_tombstones)`)
	if err != nil {
		return fmt.Errorf("sync: inspect tombstone schema: %w", err)
	}
	if len(rows.Values) == 0 {
		return nil
	}
	nameIndex, pkIndex := -1, -1
	for i, name := range rows.Columns {
		if name == "name" {
			nameIndex = i
		}
		if name == "pk" {
			pkIndex = i
		}
	}
	legacyObjectPK, currentSequencePK := false, false
	for _, row := range rows.Values {
		if nameIndex >= 0 && pkIndex >= 0 && nameIndex < len(row) && pkIndex < len(row) {
			name, nameOK := row[nameIndex].(string)
			pk, pkOK := row[pkIndex].(int64)
			if !nameOK || !pkOK {
				continue
			}
			if name == "object_id" && pk != 0 {
				legacyObjectPK = true
			}
			if name == "sequence" && pk != 0 {
				currentSequencePK = true
			}
		}
	}
	legacy := legacyObjectPK && !currentSequencePK
	if !legacy {
		return nil
	}
	stmts := []sqlite.Statement{
		{SQL: `ALTER TABLE sync_tombstones RENAME TO sync_tombstones_legacy_v1`},
		{SQL: `CREATE TABLE sync_tombstones (account_id TEXT NOT NULL, object_id TEXT NOT NULL, device_id TEXT NOT NULL, version INTEGER NOT NULL, sequence INTEGER NOT NULL, issued_at_ns INTEGER NOT NULL, deleted_at_ns INTEGER NOT NULL, protocol_version INTEGER NOT NULL, signature BLOB NOT NULL, PRIMARY KEY(account_id, sequence), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `INSERT INTO sync_tombstones(account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature) SELECT account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature FROM sync_tombstones_legacy_v1`},
		{SQL: `CREATE TABLE IF NOT EXISTS sync_current_tombstones (account_id TEXT NOT NULL, object_id TEXT NOT NULL, device_id TEXT NOT NULL, version INTEGER NOT NULL, sequence INTEGER NOT NULL, issued_at_ns INTEGER NOT NULL, deleted_at_ns INTEGER NOT NULL, protocol_version INTEGER NOT NULL, signature BLOB NOT NULL, PRIMARY KEY(account_id, object_id), UNIQUE(account_id, sequence), FOREIGN KEY(account_id) REFERENCES sync_accounts(account_id))`},
		{SQL: `INSERT INTO sync_current_tombstones(account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature) SELECT account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature FROM sync_tombstones_legacy_v1`},
	}
	if _, err := s.db.Transaction(ctx, stmts); err != nil {
		return fmt.Errorf("sync: migrate tombstone schema (rollback: restore sync_tombstones_legacy_v1 as sync_tombstones): %w", err)
	}
	return nil
}

func (s *SQLiteSyncStore) Close() error {
	if s == nil || s.db == nil || !s.ownsDB {
		return nil
	}
	var protectErr error
	if s.path != "" {
		protectErr = protectDatabaseFiles(s.path)
	}
	// Always release the connection even when a permission repair failed. An
	// externally supplied DB is deliberately excluded above.
	closeErr := s.db.Close()
	return errors.Join(protectErr, closeErr)
}

func secureDatabasePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("relative database paths are rejected; use an absolute path")
	}
	if path == ":memory:" || strings.ContainsRune(path, 0) {
		return "", errors.New("invalid database path")
	}
	path = filepath.Clean(path)
	// macOS exposes /var as a system compatibility symlink. Canonicalize only
	// that fixed system component; caller-controlled components are still
	// checked and rejected below rather than resolved.
	if runtime.GOOS == "darwin" && strings.HasPrefix(path, "/var/") {
		path = "/private" + path
	}
	return path, nil
}

// mkdirPrivate creates only missing components. Existing parent permissions are
// intentionally never changed. Every component is checked with Lstat, so a
// symlink cannot redirect the database into an unrelated directory, and an
// existing group/world-writable directory is rejected rather than chmodded.
func mkdirPrivate(dir string) error {
	dir = filepath.Clean(dir)
	missing := make([]string, 0)
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if err := validateDatabaseDirectory(current, info); err != nil {
				return err
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("cannot resolve parent of %s", current)
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(missing[i])
		if err != nil {
			return err
		}
		if err := validateDatabaseDirectory(missing[i], info); err != nil {
			return err
		}
	}
	// The first existing directory above the newly-created chain was checked
	// while walking upward; validate the complete path again to catch a changed
	// component and to make the invariant explicit at the point of opening.
	return validatePrivateDirectory(dir)
}

func validateDatabaseDirectory(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is group/world-writable (mode %o)", path, info.Mode().Perm())
	}
	return nil
}

func validatePrivateDirectory(dir string) error {
	dir = filepath.Clean(dir)
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect database directory %s: %w", current, err)
		}
		if err := validateDatabaseDirectory(current, info); err != nil {
			return err
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

// rejectDatabaseFiles performs the pre-open path checks. protectDatabaseFiles
// repeats them after SQLite has opened the database because path checks alone
// cannot establish a safe file identity across an intervening rename.
func rejectDatabaseFiles(path string) error {
	if err := validatePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Lstat(name)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s is a symlink", name)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s is not a regular file", name)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func protectDatabaseFiles(path string) error {
	if err := rejectDatabaseFiles(path); err != nil {
		return err
	}
	var errs []error
	if err := protectDatabaseFile(path, true); err != nil {
		errs = append(errs, err)
	}
	for _, name := range []string{path + "-wal", path + "-shm"} {
		if err := protectDatabaseFile(name, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// protectDatabaseFile opens the file before changing its mode. Chmod is then
// performed through the opened descriptor, not the pathname. If a pathname is
// replaced between Lstat and Open, SameFile detects the replacement and the
// descriptor is closed without changing the (possibly external) target.
func protectDatabaseFile(name string, required bool) error {
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		if required {
			return fmt.Errorf("%s disappeared while protecting it", name)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", name)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	file, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("open %s for protection: %w", name, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened %s: %w", name, err)
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("%s changed while protecting it", name)
	}
	if !opened.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	// A replacement after Open is safe for the descriptor-based chmod, but it
	// is still a security failure for this operation and must be surfaced.
	current, err := os.Lstat(name)
	if err != nil {
		return fmt.Errorf("recheck %s after protection: %w", name, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, current) {
		return fmt.Errorf("%s changed during protection", name)
	}
	return nil
}

func (s *SQLiteSyncStore) secureOwnedFiles() error {
	if s == nil || !s.ownsDB || s.path == "" {
		return nil
	}
	return protectDatabaseFiles(s.path)
}

func validID(v string) bool { return strings.TrimSpace(v) != "" }

func (s *SQLiteSyncStore) RegisterAccount(account string) (credentialOut []byte, err error) {
	defer func() {
		if protectErr := s.secureOwnedFiles(); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("sync: protect database files: %w", protectErr))
		}
	}()
	if !validID(account) {
		return nil, fmt.Errorf("%w: account id", coresync.ErrStoreInvalidArgument)
	}
	credential := make([]byte, 32)
	if _, err := rand.Read(credential); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Execute(context.Background(), `INSERT INTO sync_accounts(account_id,credential) VALUES(?,?)`, account, credential)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, coresync.ErrStoreAccountExists
		}
		return nil, fmt.Errorf("sync: register account: %w", err)
	}
	return append([]byte(nil), credential...), nil
}

func (s *SQLiteSyncStore) LoginWithCredential(account string, credential []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT credential FROM sync_accounts WHERE account_id=?`, account)
	if err != nil {
		return fmt.Errorf("sync: query account credential: %w", err)
	}
	if len(rows.Values) == 0 {
		return coresync.ErrStoreCredential
	}
	stored, ok := blob(rows.Values[0], 0)
	if !ok || !hmac.Equal(stored, credential) {
		return coresync.ErrStoreCredential
	}
	return nil
}

func (s *SQLiteSyncStore) PairDeviceWithCredential(account, device string, pub ed25519.PublicKey, credential []byte) (err error) {
	defer func() {
		if protectErr := s.secureOwnedFiles(); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("sync: protect database files: %w", protectErr))
		}
	}()
	if !validID(account) || !validID(device) || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: device pairing", coresync.ErrStoreInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkCredentialLocked(account, credential); err != nil {
		return err
	}
	rows, err := s.db.Query(context.Background(), `SELECT public_key FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil {
		return err
	}
	if len(rows.Values) > 0 {
		old, _ := blob(rows.Values[0], 0)
		if !hmac.Equal(old, pub) {
			return coresync.ErrStoreDeviceKey
		}
		return nil
	}
	_, err = s.db.Execute(context.Background(), `INSERT INTO sync_devices(account_id,device_id,public_key) VALUES(?,?,?)`, account, device, append([]byte(nil), pub...))
	return err
}

func (s *SQLiteSyncStore) checkCredentialLocked(account string, credential []byte) error {
	rows, err := s.db.Query(context.Background(), `SELECT credential FROM sync_accounts WHERE account_id=?`, account)
	if err != nil {
		return fmt.Errorf("sync: query account credential: %w", err)
	}
	if len(rows.Values) == 0 {
		return coresync.ErrStoreCredential
	}
	stored, ok := blob(rows.Values[0], 0)
	if !ok || !hmac.Equal(stored, credential) {
		return coresync.ErrStoreCredential
	}
	return nil
}

func (s *SQLiteSyncStore) DevicePublicKey(account, device string) (ed25519.PublicKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT public_key FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil {
		return nil, err
	}
	if len(rows.Values) == 0 {
		return nil, coresync.ErrStoreDeviceMissing
	}
	key, ok := blob(rows.Values[0], 0)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("sync: invalid stored device key")
	}
	return ed25519.PublicKey(key), nil
}

func (s *SQLiteSyncStore) UploadEnvelope(account, device, object string, version uint64, e coresync.EncryptedEnvelope) (err error) {
	defer func() {
		if protectErr := s.secureOwnedFiles(); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("sync: protect database files: %w", protectErr))
		}
	}()
	if !validID(account) || !validID(device) || !validID(object) || version == 0 {
		return fmt.Errorf("%w: envelope identity", coresync.ErrStoreInvalidArgument)
	}
	encoded, err := coresync.MarshalEncryptedEnvelope(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pub, err := s.deviceKeyLocked(account, device)
	if err != nil {
		return err
	}
	if e.AccountID != account || e.DeviceID != device || e.ObjectID != object || e.Version != version || !e.Verify(pub) {
		return errors.New("sync: invalid envelope signature or scope")
	}
	results, err := s.db.Transaction(context.Background(), []sqlite.Statement{
		{SQL: `UPDATE sync_objects SET version=?, envelope=?
			WHERE account_id=? AND object_id=? AND version<?
			  AND NOT EXISTS (SELECT 1 FROM sync_object_boundaries WHERE account_id=? AND object_id=?)
			  AND EXISTS (SELECT 1 FROM sync_accounts a JOIN sync_devices d ON d.account_id=a.account_id
			              WHERE a.account_id=? AND d.device_id=? AND d.watermark >= a.sequence)`, Args: []any{version, encoded, account, object, version, account, object, account, device}},
		{SQL: `INSERT INTO sync_objects(account_id,object_id,version,envelope)
			SELECT ?,?,?,?
			WHERE NOT EXISTS (SELECT 1 FROM sync_objects WHERE account_id=? AND object_id=?)
			  AND NOT EXISTS (SELECT 1 FROM sync_object_boundaries WHERE account_id=? AND object_id=?)
			  AND EXISTS (SELECT 1 FROM sync_accounts a JOIN sync_devices d ON d.account_id=a.account_id
			              WHERE a.account_id=? AND d.device_id=? AND d.watermark >= a.sequence)`, Args: []any{account, object, version, encoded, account, object, account, object, account, device}},
	})
	if err != nil {
		return fmt.Errorf("sync: upload envelope transaction: %w", err)
	}
	if len(results) == 2 && (results[0].Changes > 0 || results[1].Changes > 0) {
		return nil
	}
	// Classify a predicate rejection after the atomic transaction. The mutation
	// itself is never performed outside that transaction.
	rows, qerr := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, account)
	if qerr != nil || len(rows.Values) == 0 {
		return coresync.ErrStoreAccountMissing
	}
	sequence := syncInt64val(rows.Values[0], 0)
	rows, qerr = s.db.Query(context.Background(), `SELECT watermark FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if qerr != nil || len(rows.Values) == 0 {
		return coresync.ErrStoreDeviceMissing
	}
	if syncInt64val(rows.Values[0], 0) < sequence {
		return errors.New("sync: tombstones must be synchronized before upload")
	}
	rows, qerr = s.db.Query(context.Background(), `SELECT 1 FROM sync_current_tombstones WHERE account_id=? AND object_id=?`, account, object)
	if qerr == nil && len(rows.Values) > 0 {
		return coresync.ErrStoreDeleted
	}
	return coresync.ErrStoreVersion
}

func (s *SQLiteSyncStore) GetEnvelope(account, object string) (coresync.EncryptedEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT envelope FROM sync_objects WHERE account_id=? AND object_id=?`, account, object)
	if err != nil {
		return coresync.EncryptedEnvelope{}, err
	}
	if len(rows.Values) == 0 {
		return coresync.EncryptedEnvelope{}, coresync.ErrStoreObjectMissing
	}
	raw, ok := blob(rows.Values[0], 0)
	if !ok {
		return coresync.EncryptedEnvelope{}, coresync.ErrStorePlaintext
	}
	return coresync.ParseEncryptedEnvelope(raw)
}

func (s *SQLiteSyncStore) ObjectVersion(account, object string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT version FROM sync_objects WHERE account_id=? AND object_id=?`, account, object)
	if err != nil {
		return 0, err
	}
	if len(rows.Values) == 0 {
		rows, err = s.db.Query(context.Background(), `SELECT version FROM sync_object_boundaries WHERE account_id=? AND object_id=?`, account, object)
	}
	if err != nil || len(rows.Values) == 0 {
		return 0, coresync.ErrStoreObjectMissing
	}
	return uint64(syncInt64val(rows.Values[0], 0)), nil
}

func (s *SQLiteSyncStore) SubmitDelete(d coresync.DeleteInstruction) (t coresync.Tombstone, err error) {
	defer func() {
		if protectErr := s.secureOwnedFiles(); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("sync: protect database files: %w", protectErr))
		}
	}()
	if !validID(d.AccountID) || !validID(d.ObjectID) || !validID(d.DeviceID) || d.Version == 0 || d.ProtocolVersion != coresync.ProtocolVersion {
		return coresync.Tombstone{}, fmt.Errorf("%w: delete instruction", coresync.ErrStoreInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pub, err := s.deviceKeyLocked(d.AccountID, d.DeviceID)
	if err != nil || !d.Verify(pub) {
		return coresync.Tombstone{}, errors.New("sync: invalid delete signature")
	}
	rows, err := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, d.AccountID)
	if err != nil || len(rows.Values) == 0 {
		return coresync.Tombstone{}, coresync.ErrStoreAccountMissing
	}
	sequence := uint64(syncInt64val(rows.Values[0], 0) + 1)
	now := time.Now().UTC()
	t = coresync.Tombstone{AccountID: d.AccountID, ObjectID: d.ObjectID, DeviceID: d.DeviceID, Version: d.Version, Sequence: sequence, IssuedAt: d.IssuedAt, DeletedAt: now, ProtocolVersion: coresync.ProtocolVersion, Signature: append([]byte(nil), d.Signature...)}
	digest := sha256.Sum256([]byte(d.ObjectID))
	// Sequence allocation, append log, permanent boundary, object purge and
	// audit are one SQLite transaction. A newer delete supersedes an older
	// tombstone; it is not blocked by a live object with a higher upload version.
	stmts := []sqlite.Statement{
		{SQL: `UPDATE sync_accounts SET sequence=? WHERE account_id=? AND sequence=? AND NOT EXISTS (SELECT 1 FROM sync_current_tombstones WHERE account_id=? AND object_id=? AND version>=?)`, Args: []any{sequence, d.AccountID, sequence - 1, d.AccountID, d.ObjectID, d.Version}},
		// Force a rollback when the guarded sequence allocation did not happen.
		// SQLite's statement batch API commits successful no-op statements, so a
		// NOT NULL violation is used as the transaction-level guard.
		{SQL: `UPDATE sync_accounts SET sequence=NULL WHERE account_id=? AND sequence<>?`, Args: []any{d.AccountID, sequence}},
		{SQL: `INSERT INTO sync_object_boundaries(account_id,object_id,version) VALUES(?,?,MAX(?,COALESCE((SELECT version FROM sync_objects WHERE account_id=? AND object_id=?),0))) ON CONFLICT(account_id,object_id) DO UPDATE SET version=MAX(sync_object_boundaries.version,excluded.version)`, Args: []any{d.AccountID, d.ObjectID, d.Version, d.AccountID, d.ObjectID}},
		{SQL: `INSERT INTO sync_tombstones(account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature) VALUES(?,?,?,?,?,?,?,?,?)`, Args: []any{t.AccountID, t.ObjectID, t.DeviceID, t.Version, t.Sequence, t.IssuedAt.UTC().UnixNano(), t.DeletedAt.UnixNano(), t.ProtocolVersion, t.Signature}},
		{SQL: `INSERT INTO sync_current_tombstones(account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,object_id) DO UPDATE SET device_id=excluded.device_id,version=excluded.version,sequence=excluded.sequence,issued_at_ns=excluded.issued_at_ns,deleted_at_ns=excluded.deleted_at_ns,protocol_version=excluded.protocol_version,signature=excluded.signature WHERE excluded.version > sync_current_tombstones.version`, Args: []any{t.AccountID, t.ObjectID, t.DeviceID, t.Version, t.Sequence, t.IssuedAt.UTC().UnixNano(), t.DeletedAt.UnixNano(), t.ProtocolVersion, t.Signature}},
		{SQL: `DELETE FROM sync_objects WHERE account_id=? AND object_id=?`, Args: []any{d.AccountID, d.ObjectID}},
		{SQL: `INSERT INTO sync_audit(account_id,object_digest,device_id,at_ns,action) VALUES(?,?,?,?,?)`, Args: []any{d.AccountID, hex.EncodeToString(digest[:]), d.DeviceID, t.DeletedAt.UnixNano(), "delete"}},
	}
	results, err := s.db.Transaction(context.Background(), stmts)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not null") || strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return coresync.Tombstone{}, coresync.ErrStoreVersion
		}
		return coresync.Tombstone{}, fmt.Errorf("sync: submit tombstone: %w", err)
	}
	if len(results) < 1 || results[0].Changes != 1 {
		return coresync.Tombstone{}, coresync.ErrStoreVersion
	}
	return t, nil
}

func (s *SQLiteSyncStore) SyncTombstones(account, device string, _ uint64) (out []coresync.Tombstone, err error) {
	defer func() {
		if protectErr := s.secureOwnedFiles(); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("sync: protect database files: %w", protectErr))
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Read the candidate batch first, then publish exactly that range in a
	// transaction whose predicates re-check both cursors. A concurrent SQLite
	// connection therefore cannot publish a range for a stale watermark.
	rows, err := s.db.Query(context.Background(), `SELECT d.watermark,a.sequence,d.issued,d.issued_from,d.issued_to FROM sync_devices d JOIN sync_accounts a ON a.account_id=d.account_id WHERE d.account_id=? AND d.device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return nil, coresync.ErrStoreDeviceMissing
	}
	row := rows.Values[0]
	from, latest := uint64(syncInt64val(row, 0)), uint64(syncInt64val(row, 1))
	if syncInt64val(row, 2) != 0 {
		issuedFrom, issuedTo := uint64(syncInt64val(row, 3)), uint64(syncInt64val(row, 4))
		if issuedFrom != from+1 || issuedTo < issuedFrom {
			return nil, coresync.ErrStoreBatch
		}
		return s.loadTombstoneRangeLocked(account, issuedFrom, issuedTo)
	}
	rows, err = s.db.Query(context.Background(), `SELECT object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature FROM sync_tombstones WHERE account_id=? AND sequence>? AND sequence<=? ORDER BY sequence`, account, from, latest)
	if err != nil {
		return nil, err
	}
	out = make([]coresync.Tombstone, 0, len(rows.Values))
	for _, r := range rows.Values {
		t := rowTombstone(account, r)
		if t.Sequence != from+uint64(len(out))+1 {
			return nil, errors.New("sync: tombstone sequence is not contiguous")
		}
		out = append(out, t)
	}
	var issuedFrom, issuedTo int64
	if len(out) > 0 {
		issuedFrom, issuedTo = int64(out[0].Sequence), int64(out[len(out)-1].Sequence)
	}
	results, err := s.db.Transaction(context.Background(), []sqlite.Statement{{SQL: `UPDATE sync_devices SET issued=?,issued_from=?,issued_to=? WHERE account_id=? AND device_id=? AND watermark=? AND issued=0 AND EXISTS (SELECT 1 FROM sync_accounts WHERE account_id=? AND sequence=?)`, Args: []any{syncBoolInt(len(out) > 0), issuedFrom, issuedTo, account, device, from, account, latest}}})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 || results[0].Changes != 1 {
		// Another store instance may have published the same candidate batch
		// between our read and conditional update. Return that durable batch so
		// its confirmation remains valid; never expose the unrecorded candidate.
		pending, queryErr := s.db.Query(context.Background(), `SELECT watermark,issued,issued_from,issued_to FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
		if queryErr == nil && len(pending.Values) > 0 && syncInt64val(pending.Values[0], 1) != 0 {
			pendingRow := pending.Values[0]
			pendingFrom, pendingTo := uint64(syncInt64val(pendingRow, 2)), uint64(syncInt64val(pendingRow, 3))
			if pendingFrom == uint64(syncInt64val(pendingRow, 0))+1 && pendingTo >= pendingFrom {
				return s.loadTombstoneRangeLocked(account, pendingFrom, pendingTo)
			}
		}
		return nil, coresync.ErrStoreBatch
	}
	return out, nil
}

func (s *SQLiteSyncStore) loadTombstoneRangeLocked(account string, from, to uint64) ([]coresync.Tombstone, error) {
	rows, err := s.db.Query(context.Background(), `SELECT object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature FROM sync_tombstones WHERE account_id=? AND sequence>=? AND sequence<=? ORDER BY sequence`, account, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]coresync.Tombstone, 0, len(rows.Values))
	for _, r := range rows.Values {
		t := rowTombstone(account, r)
		if t.Sequence != from+uint64(len(out)) {
			return nil, errors.New("sync: tombstone sequence is not contiguous")
		}
		out = append(out, t)
	}
	if len(out) == 0 || out[len(out)-1].Sequence != to {
		return nil, errors.New("sync: tombstone sequence is not contiguous")
	}
	return out, nil
}

func (s *SQLiteSyncStore) ConfirmTombstones(account, device string, sequence uint64) (err error) {
	defer func() {
		if protectErr := s.secureOwnedFiles(); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("sync: protect database files: %w", protectErr))
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(context.Background(), `SELECT watermark,issued,issued_from,issued_to FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return coresync.ErrStoreDeviceMissing
	}
	r := rows.Values[0]
	confirmed, issued := uint64(syncInt64val(r, 0)), syncInt64val(r, 1) != 0
	latestRows, err := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, account)
	if err != nil || len(latestRows.Values) == 0 {
		return coresync.ErrStoreAccountMissing
	}
	latest := uint64(syncInt64val(latestRows.Values[0], 0))
	if sequence < confirmed {
		return errors.New("sync: confirmation moved backwards")
	}
	if sequence > latest {
		return errors.New("sync: confirmation is ahead of server")
	}
	if sequence == confirmed && sequence == 0 && latest == 0 && !issued {
		return nil
	}
	if sequence == confirmed || !issued || uint64(syncInt64val(r, 2)) != confirmed+1 || uint64(syncInt64val(r, 3)) != sequence {
		return coresync.ErrStoreBatch
	}
	results, err := s.db.Transaction(context.Background(), []sqlite.Statement{{SQL: `UPDATE sync_devices SET watermark=?,issued=0,issued_from=0,issued_to=0 WHERE account_id=? AND device_id=? AND watermark=? AND issued=1 AND issued_from=? AND issued_to=? AND EXISTS (SELECT 1 FROM sync_accounts WHERE account_id=? AND sequence>=?)`, Args: []any{sequence, account, device, confirmed, confirmed + 1, sequence, account, sequence}}})
	if err != nil {
		return err
	}
	if len(results) == 0 || results[0].Changes != 1 {
		return coresync.ErrStoreBatch
	}
	return nil
}

func (s *SQLiteSyncStore) VerifyTombstone(t coresync.Tombstone) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t.AccountID == "" || t.ObjectID == "" || t.Version == 0 || t.Sequence == 0 {
		return false
	}
	// The current tombstone index is only the latest object boundary. A device
	// can still be acknowledging an older issued batch, so validation must use
	// the append-only sequence log instead.
	rows, err := s.db.Query(context.Background(), `SELECT object_id,device_id,version,signature FROM sync_tombstones WHERE account_id=? AND sequence=?`, t.AccountID, t.Sequence)
	if err != nil || len(rows.Values) == 0 {
		return false
	}
	r := rows.Values[0]
	storedSignature, ok := blob(r, 3)
	if !ok || syncStr(r, 0) != t.ObjectID || syncStr(r, 1) != t.DeviceID || uint64(syncInt64val(r, 2)) != t.Version || !hmac.Equal(storedSignature, t.Signature) {
		return false
	}
	pub, err := s.deviceKeyLocked(t.AccountID, t.DeviceID)
	return err == nil && t.Verify(pub)
}

func (s *SQLiteSyncStore) Watermark(account, device string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT watermark FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return 0, coresync.ErrStoreDeviceMissing
	}
	return uint64(syncInt64val(rows.Values[0], 0)), nil
}

func (s *SQLiteSyncStore) IssuedBatch(account, device string) (coresync.TombstoneBatch, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT issued,issued_from,issued_to FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return coresync.TombstoneBatch{}, false, coresync.ErrStoreDeviceMissing
	}
	if syncInt64val(rows.Values[0], 0) == 0 {
		return coresync.TombstoneBatch{}, false, nil
	}
	return coresync.TombstoneBatch{From: uint64(syncInt64val(rows.Values[0], 1)), To: uint64(syncInt64val(rows.Values[0], 2))}, true, nil
}

func (s *SQLiteSyncStore) AuditForAccount(account string) []coresync.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT object_digest,device_id,at_ns,action FROM sync_audit WHERE account_id=? ORDER BY id`, account)
	if err != nil {
		return nil
	}
	out := make([]coresync.AuditEvent, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, coresync.AuditEvent{AccountID: account, ObjectDigest: syncStr(r, 0), DeviceID: syncStr(r, 1), At: time.Unix(0, syncInt64val(r, 2)).UTC(), Action: syncStr(r, 3)})
	}
	return out
}

func (s *SQLiteSyncStore) LatestSequence(account string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, account)
	if err != nil || len(rows.Values) == 0 {
		return 0, coresync.ErrStoreAccountMissing
	}
	return uint64(syncInt64val(rows.Values[0], 0)), nil
}

func (s *SQLiteSyncStore) deviceKeyLocked(account, device string) (ed25519.PublicKey, error) {
	rows, err := s.db.Query(context.Background(), `SELECT public_key FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return nil, coresync.ErrStoreDeviceMissing
	}
	key, ok := blob(rows.Values[0], 0)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, coresync.ErrStoreDeviceMissing
	}
	return ed25519.PublicKey(key), nil
}

func rowTombstone(account string, r []any) coresync.Tombstone {
	sig, _ := blob(r, 7)
	return coresync.Tombstone{AccountID: account, ObjectID: syncStr(r, 0), DeviceID: syncStr(r, 1), Version: uint64(syncInt64val(r, 2)), Sequence: uint64(syncInt64val(r, 3)), IssuedAt: time.Unix(0, syncInt64val(r, 4)).UTC(), DeletedAt: time.Unix(0, syncInt64val(r, 5)).UTC(), ProtocolVersion: uint16(syncInt64val(r, 6)), Signature: append([]byte(nil), sig...)}
}

func blob(row []any, index int) ([]byte, bool) {
	if index >= len(row) {
		return nil, false
	}
	switch v := row[index].(type) {
	case []byte:
		return append([]byte(nil), v...), true
	case string:
		return []byte(v), true
	default:
		return nil, false
	}
}
func syncStr(row []any, index int) string {
	if index >= len(row) {
		return ""
	}
	if v, ok := row[index].(string); ok {
		return v
	}
	return ""
}
func syncInt64val(row []any, index int) int64 {
	if index >= len(row) {
		return 0
	}
	if v, ok := row[index].(int64); ok {
		return v
	}
	return 0
}
func syncBoolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
