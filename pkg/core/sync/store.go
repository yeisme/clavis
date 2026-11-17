package sync

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
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

// SyncStore is the durable boundary for the sync protocol. Implementations
// store only encrypted envelopes; plaintext payloads are not part of this API.
// Methods which mutate protocol state are atomic and preserve the same cursor,
// tombstone, signature, and version invariants as Server.
type SyncStore interface {
	RegisterAccount(account string) ([]byte, error)
	LoginWithCredential(account string, credential []byte) error
	PairDeviceWithCredential(account, device string, pub ed25519.PublicKey, credential []byte) error
	DevicePublicKey(account, device string) (ed25519.PublicKey, error)
	UploadEnvelope(account, device, object string, version uint64, envelope EncryptedEnvelope) error
	GetEnvelope(account, object string) (EncryptedEnvelope, error)
	ObjectVersion(account, object string) (uint64, error)
	SubmitDelete(instruction DeleteInstruction) (Tombstone, error)
	SyncTombstones(account, device string, after uint64) ([]Tombstone, error)
	ConfirmTombstones(account, device string, sequence uint64) error
	VerifyTombstone(tombstone Tombstone) bool
	Watermark(account, device string) (uint64, error)
	IssuedBatch(account, device string) (TombstoneBatch, bool, error)
	AuditForAccount(account string) []AuditEvent
	LatestSequence(account string) (uint64, error)
	Close() error
}

// TombstoneBatch is the exact contiguous batch issued to a device. A batch is
// consumed on confirmation and cannot be forged by supplying a cursor.
type TombstoneBatch struct {
	From uint64
	To   uint64
}

var (
	ErrStoreInvalidArgument = errors.New("sync: invalid store argument")
	ErrStoreAccountExists   = errors.New("sync: account already exists")
	ErrStoreAccountMissing  = errors.New("sync: account not found")
	ErrStoreCredential      = errors.New("sync: invalid account credential")
	ErrStoreDeviceMissing   = errors.New("sync: device is not paired")
	ErrStoreDeviceKey       = errors.New("sync: device key cannot be overwritten")
	ErrStoreObjectMissing   = errors.New("sync: object not found")
	ErrStoreDeleted         = errors.New("sync: object is permanently deleted")
	ErrStoreVersion         = errors.New("sync: upload version is not newer")
	ErrStoreBatch           = errors.New("sync: tombstone batch is not confirmed")
	ErrStoreCursor          = errors.New("sync: invalid tombstone cursor")
	ErrStorePlaintext       = errors.New("sync: plaintext payloads are not accepted")
)

// SQLiteStore persists protocol metadata in a dedicated set of tables. It is
// intentionally independent of the in-memory Server so the latter remains a
// useful test substitute. NewServerStore provides the explicit adapter when a
// caller wants to use the old Server through this contract.
type SQLiteStore struct {
	db *sqlite.DB
	mu sync.RWMutex
}

var _ SyncStore = (*SQLiteStore)(nil)

// NewSQLiteStore opens dbPath (or :memory:) and initializes the sync schema.
// A file-backed store is created with a private directory and file mode.
func NewSQLiteStore(ctx context.Context, dbPath string) (*SQLiteStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if dbPath == "" {
		return nil, fmt.Errorf("%w: database path is empty", ErrStoreInvalidArgument)
	}
	file := dbPath != ":memory:"
	if file {
		dir := filepath.Dir(dbPath)
		if dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("sync: create database directory: %w", err)
			}
			if err := os.Chmod(dir, 0o700); err != nil {
				return nil, fmt.Errorf("sync: protect database directory: %w", err)
			}
		}
	}
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{BusyTimeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("sync: open sqlite store: %w", err)
	}
	s := &SQLiteStore{db: db}
	if err := s.initSchema(ctx, file); err != nil {
		_ = db.Close()
		return nil, err
	}
	if file {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sync: protect database file: %w", err)
		}
	}
	return s, nil
}

// NewSQLiteStoreFromDB attaches the sync tables to an existing MyGo SQLite DB.
// The owner of db remains responsible for closing it.
func NewSQLiteStoreFromDB(db *sqlite.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil sqlite database", ErrStoreInvalidArgument)
	}
	s := &SQLiteStore{db: db}
	if err := s.initSchema(context.Background(), false); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) initSchema(ctx context.Context, wal bool) error {
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
func (s *SQLiteStore) migrateLegacyTombstones(ctx context.Context) error {
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

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func validID(v string) bool { return strings.TrimSpace(v) != "" }

func (s *SQLiteStore) RegisterAccount(account string) ([]byte, error) {
	if !validID(account) {
		return nil, fmt.Errorf("%w: account id", ErrStoreInvalidArgument)
	}
	credential := make([]byte, 32)
	if _, err := rand.Read(credential); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Execute(context.Background(), `INSERT INTO sync_accounts(account_id,credential) VALUES(?,?)`, account, credential)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrStoreAccountExists
		}
		return nil, fmt.Errorf("sync: register account: %w", err)
	}
	return append([]byte(nil), credential...), nil
}

func (s *SQLiteStore) LoginWithCredential(account string, credential []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT credential FROM sync_accounts WHERE account_id=?`, account)
	if err != nil {
		return fmt.Errorf("sync: query account credential: %w", err)
	}
	if len(rows.Values) == 0 {
		return ErrStoreCredential
	}
	stored, ok := blob(rows.Values[0], 0)
	if !ok || !hmac.Equal(stored, credential) {
		return ErrStoreCredential
	}
	return nil
}

func (s *SQLiteStore) PairDeviceWithCredential(account, device string, pub ed25519.PublicKey, credential []byte) error {
	if !validID(account) || !validID(device) || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: device pairing", ErrStoreInvalidArgument)
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
			return ErrStoreDeviceKey
		}
		return nil
	}
	_, err = s.db.Execute(context.Background(), `INSERT INTO sync_devices(account_id,device_id,public_key) VALUES(?,?,?)`, account, device, append([]byte(nil), pub...))
	return err
}

func (s *SQLiteStore) checkCredentialLocked(account string, credential []byte) error {
	rows, err := s.db.Query(context.Background(), `SELECT credential FROM sync_accounts WHERE account_id=?`, account)
	if err != nil {
		return fmt.Errorf("sync: query account credential: %w", err)
	}
	if len(rows.Values) == 0 {
		return ErrStoreCredential
	}
	stored, ok := blob(rows.Values[0], 0)
	if !ok || !hmac.Equal(stored, credential) {
		return ErrStoreCredential
	}
	return nil
}

func (s *SQLiteStore) DevicePublicKey(account, device string) (ed25519.PublicKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT public_key FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil {
		return nil, err
	}
	if len(rows.Values) == 0 {
		return nil, ErrStoreDeviceMissing
	}
	key, ok := blob(rows.Values[0], 0)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("sync: invalid stored device key")
	}
	return ed25519.PublicKey(key), nil
}

func (s *SQLiteStore) UploadEnvelope(account, device, object string, version uint64, e EncryptedEnvelope) error {
	if !validID(account) || !validID(device) || !validID(object) || version == 0 {
		return fmt.Errorf("%w: envelope identity", ErrStoreInvalidArgument)
	}
	encoded, err := marshalEnvelope(e)
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
	var sequence, boundary uint64
	rows, err := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, account)
	if err != nil || len(rows.Values) == 0 {
		return ErrStoreAccountMissing
	}
	sequence = uint64(int64val(rows.Values[0], 0))
	rows, err = s.db.Query(context.Background(), `SELECT watermark FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return ErrStoreDeviceMissing
	}
	if uint64(int64val(rows.Values[0], 0)) < sequence {
		return errors.New("sync: tombstones must be synchronized before upload")
	}
	rows, err = s.db.Query(context.Background(), `SELECT version FROM sync_objects WHERE account_id=? AND object_id=?`, account, object)
	if err != nil {
		return err
	}
	if len(rows.Values) > 0 {
		boundary = uint64(int64val(rows.Values[0], 0))
	}
	rows, err = s.db.Query(context.Background(), `SELECT version FROM sync_current_tombstones WHERE account_id=? AND object_id=?`, account, object)
	if err != nil {
		return err
	}
	if len(rows.Values) > 0 {
		return ErrStoreDeleted
	}
	if version <= boundary {
		return ErrStoreVersion
	}
	_, err = s.db.Execute(context.Background(), `INSERT INTO sync_objects(account_id,object_id,version,envelope) VALUES(?,?,?,?) ON CONFLICT(account_id,object_id) DO UPDATE SET version=excluded.version,envelope=excluded.envelope`, account, object, version, encoded)
	return err
}

func (s *SQLiteStore) GetEnvelope(account, object string) (EncryptedEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT envelope FROM sync_objects WHERE account_id=? AND object_id=?`, account, object)
	if err != nil {
		return EncryptedEnvelope{}, err
	}
	if len(rows.Values) == 0 {
		return EncryptedEnvelope{}, ErrStoreObjectMissing
	}
	raw, ok := blob(rows.Values[0], 0)
	if !ok {
		return EncryptedEnvelope{}, ErrStorePlaintext
	}
	return parseEnvelope(raw)
}

func (s *SQLiteStore) ObjectVersion(account, object string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT version FROM sync_objects WHERE account_id=? AND object_id=?`, account, object)
	if err != nil {
		return 0, err
	}
	if len(rows.Values) == 0 {
		rows, err = s.db.Query(context.Background(), `SELECT version FROM sync_current_tombstones WHERE account_id=? AND object_id=?`, account, object)
	}
	if err != nil || len(rows.Values) == 0 {
		return 0, ErrStoreObjectMissing
	}
	return uint64(int64val(rows.Values[0], 0)), nil
}

func (s *SQLiteStore) SubmitDelete(d DeleteInstruction) (Tombstone, error) {
	if !validID(d.AccountID) || !validID(d.ObjectID) || !validID(d.DeviceID) || d.Version == 0 || d.ProtocolVersion != ProtocolVersion {
		return Tombstone{}, fmt.Errorf("%w: delete instruction", ErrStoreInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pub, err := s.deviceKeyLocked(d.AccountID, d.DeviceID)
	if err != nil || !d.Verify(pub) {
		return Tombstone{}, errors.New("sync: invalid delete signature")
	}
	rows, err := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, d.AccountID)
	if err != nil || len(rows.Values) == 0 {
		return Tombstone{}, ErrStoreAccountMissing
	}
	sequence := uint64(int64val(rows.Values[0], 0))
	rows, err = s.db.Query(context.Background(), `SELECT version FROM sync_current_tombstones WHERE account_id=? AND object_id=?`, d.AccountID, d.ObjectID)
	if err != nil {
		return Tombstone{}, err
	}
	if len(rows.Values) > 0 && d.Version <= uint64(int64val(rows.Values[0], 0)) {
		return Tombstone{}, fmt.Errorf("%w: stale delete version %d", ErrStoreVersion, d.Version)
	}
	rows, err = s.db.Query(context.Background(), `SELECT version FROM sync_objects WHERE account_id=? AND object_id=?`, d.AccountID, d.ObjectID)
	if err != nil {
		return Tombstone{}, err
	}
	if len(rows.Values) > 0 && d.Version <= uint64(int64val(rows.Values[0], 0)) {
		return Tombstone{}, fmt.Errorf("%w: stale delete version %d", ErrStoreVersion, d.Version)
	}
	sequence++
	now := time.Now().UTC()
	t := Tombstone{AccountID: d.AccountID, ObjectID: d.ObjectID, DeviceID: d.DeviceID, Version: d.Version, Sequence: sequence, IssuedAt: d.IssuedAt, DeletedAt: now, ProtocolVersion: ProtocolVersion, Signature: append([]byte(nil), d.Signature...)}
	digest := sha256.Sum256([]byte(d.ObjectID))
	stmts := []sqlite.Statement{
		{SQL: `UPDATE sync_accounts SET sequence=? WHERE account_id=?`, Args: []any{sequence, d.AccountID}},
		{SQL: `INSERT INTO sync_tombstones(account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature) VALUES(?,?,?,?,?,?,?,?,?)`, Args: []any{t.AccountID, t.ObjectID, t.DeviceID, t.Version, t.Sequence, t.IssuedAt.UTC().UnixNano(), t.DeletedAt.UnixNano(), t.ProtocolVersion, t.Signature}},
		{SQL: `INSERT INTO sync_current_tombstones(account_id,object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,object_id) DO UPDATE SET device_id=excluded.device_id,version=excluded.version,sequence=excluded.sequence,issued_at_ns=excluded.issued_at_ns,deleted_at_ns=excluded.deleted_at_ns,protocol_version=excluded.protocol_version,signature=excluded.signature`, Args: []any{t.AccountID, t.ObjectID, t.DeviceID, t.Version, t.Sequence, t.IssuedAt.UTC().UnixNano(), t.DeletedAt.UnixNano(), t.ProtocolVersion, t.Signature}},
		{SQL: `DELETE FROM sync_objects WHERE account_id=? AND object_id=?`, Args: []any{d.AccountID, d.ObjectID}},
		{SQL: `INSERT INTO sync_audit(account_id,object_digest,device_id,at_ns,action) VALUES(?,?,?,?,?)`, Args: []any{d.AccountID, hex.EncodeToString(digest[:]), d.DeviceID, t.DeletedAt.UnixNano(), "delete"}},
	}
	if _, err := s.db.Transaction(context.Background(), stmts); err != nil {
		return Tombstone{}, fmt.Errorf("sync: submit tombstone: %w", err)
	}
	return t, nil
}

func (s *SQLiteStore) SyncTombstones(account, device string, _ uint64) ([]Tombstone, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.deviceKeyLocked(account, device); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(context.Background(), `SELECT watermark FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return nil, ErrStoreDeviceMissing
	}
	from := uint64(int64val(rows.Values[0], 0))
	rows, err = s.db.Query(context.Background(), `SELECT object_id,device_id,version,sequence,issued_at_ns,deleted_at_ns,protocol_version,signature FROM sync_tombstones WHERE account_id=? AND sequence>? ORDER BY sequence`, account, from)
	if err != nil {
		return nil, err
	}
	out := make([]Tombstone, 0, len(rows.Values))
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
	if _, err := s.db.Execute(context.Background(), `UPDATE sync_devices SET issued=?,issued_from=?,issued_to=? WHERE account_id=? AND device_id=?`, boolInt(len(out) > 0), issuedFrom, issuedTo, account, device); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteStore) ConfirmTombstones(account, device string, sequence uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(context.Background(), `SELECT watermark,issued,issued_from,issued_to FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return ErrStoreDeviceMissing
	}
	r := rows.Values[0]
	confirmed, issued := uint64(int64val(r, 0)), int64val(r, 1) != 0
	latestRows, err := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, account)
	if err != nil || len(latestRows.Values) == 0 {
		return ErrStoreAccountMissing
	}
	latest := uint64(int64val(latestRows.Values[0], 0))
	if sequence < confirmed {
		return errors.New("sync: confirmation moved backwards")
	}
	if sequence > latest {
		return errors.New("sync: confirmation is ahead of server")
	}
	if sequence == confirmed && sequence == 0 && latest == 0 && !issued {
		return nil
	}
	if sequence == confirmed || !issued || uint64(int64val(r, 2)) != confirmed+1 || uint64(int64val(r, 3)) != sequence {
		return ErrStoreBatch
	}
	_, err = s.db.Execute(context.Background(), `UPDATE sync_devices SET watermark=?,issued=0,issued_from=0,issued_to=0 WHERE account_id=? AND device_id=?`, sequence, account, device)
	return err
}

func (s *SQLiteStore) VerifyTombstone(t Tombstone) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT device_id,version,sequence FROM sync_current_tombstones WHERE account_id=? AND object_id=?`, t.AccountID, t.ObjectID)
	if err != nil || len(rows.Values) == 0 {
		return false
	}
	r := rows.Values[0]
	if str(r, 0) != t.DeviceID || uint64(int64val(r, 1)) != t.Version || uint64(int64val(r, 2)) != t.Sequence || t.Sequence == 0 {
		return false
	}
	pub, err := s.deviceKeyLocked(t.AccountID, t.DeviceID)
	return err == nil && t.Verify(pub)
}

func (s *SQLiteStore) Watermark(account, device string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT watermark FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return 0, ErrStoreDeviceMissing
	}
	return uint64(int64val(rows.Values[0], 0)), nil
}

func (s *SQLiteStore) IssuedBatch(account, device string) (TombstoneBatch, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT issued,issued_from,issued_to FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return TombstoneBatch{}, false, ErrStoreDeviceMissing
	}
	if int64val(rows.Values[0], 0) == 0 {
		return TombstoneBatch{}, false, nil
	}
	return TombstoneBatch{From: uint64(int64val(rows.Values[0], 1)), To: uint64(int64val(rows.Values[0], 2))}, true, nil
}

func (s *SQLiteStore) AuditForAccount(account string) []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT object_digest,device_id,at_ns,action FROM sync_audit WHERE account_id=? ORDER BY id`, account)
	if err != nil {
		return nil
	}
	out := make([]AuditEvent, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, AuditEvent{AccountID: account, ObjectDigest: str(r, 0), DeviceID: str(r, 1), At: time.Unix(0, int64val(r, 2)).UTC(), Action: str(r, 3)})
	}
	return out
}

func (s *SQLiteStore) LatestSequence(account string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(context.Background(), `SELECT sequence FROM sync_accounts WHERE account_id=?`, account)
	if err != nil || len(rows.Values) == 0 {
		return 0, ErrStoreAccountMissing
	}
	return uint64(int64val(rows.Values[0], 0)), nil
}

func (s *SQLiteStore) deviceKeyLocked(account, device string) (ed25519.PublicKey, error) {
	rows, err := s.db.Query(context.Background(), `SELECT public_key FROM sync_devices WHERE account_id=? AND device_id=?`, account, device)
	if err != nil || len(rows.Values) == 0 {
		return nil, ErrStoreDeviceMissing
	}
	key, ok := blob(rows.Values[0], 0)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, ErrStoreDeviceMissing
	}
	return ed25519.PublicKey(key), nil
}

func rowTombstone(account string, r []any) Tombstone {
	sig, _ := blob(r, 7)
	return Tombstone{AccountID: account, ObjectID: str(r, 0), DeviceID: str(r, 1), Version: uint64(int64val(r, 2)), Sequence: uint64(int64val(r, 3)), IssuedAt: time.Unix(0, int64val(r, 4)).UTC(), DeletedAt: time.Unix(0, int64val(r, 5)).UTC(), ProtocolVersion: uint16(int64val(r, 6)), Signature: append([]byte(nil), sig...)}
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
func str(row []any, index int) string {
	if index >= len(row) {
		return ""
	}
	if v, ok := row[index].(string); ok {
		return v
	}
	return ""
}
func int64val(row []any, index int) int64 {
	if index >= len(row) {
		return 0
	}
	if v, ok := row[index].(int64); ok {
		return v
	}
	return 0
}
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
