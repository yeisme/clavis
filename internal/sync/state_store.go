package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	stdsync "sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/host"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

// DeviceStatusMetadata captures non-secret local sync status persisted in the
// per-device SQLite state database. Private keys, ADKs, recovery codes, and
// plaintext payloads never enter this table.
type DeviceStatusMetadata struct {
	AccountID         string
	DeviceID          string
	SyncDir           string
	PendingUploads    int
	PendingTombstones int
	Conflicts         int
	Devices           int
	LastSyncAt        time.Time
	AllowedClasses    []coresync.DataClass
	PendingAck        uint64
}

// SQLiteStateStore is the platform/storage adapter for sync's standard-library
// StateStore and StatusStore interfaces. Database details stay out of pkg/core.
type SQLiteStateStore struct {
	mu   stdsync.Mutex
	db   *sqlite.DB
	path string
}

var (
	_ coresync.StateStore  = (*SQLiteStateStore)(nil)
	_ coresync.StatusStore = (*SQLiteStateStore)(nil)
)

func OpenSQLiteStateStore(ctx context.Context, path string) (*SQLiteStateStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sync: state database path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("sync: create state database dir: %w", err)
		}
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{BusyTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sync_device_state (id INTEGER PRIMARY KEY CHECK (id=1), watermark INTEGER NOT NULL DEFAULT 0, recovery_ready INTEGER NOT NULL DEFAULT 0)`,
		`INSERT OR IGNORE INTO sync_device_state(id) VALUES(1)`,
		`CREATE TABLE IF NOT EXISTS sync_device_status (
			id INTEGER PRIMARY KEY CHECK (id=1),
			account_id TEXT NOT NULL DEFAULT '',
			device_id TEXT NOT NULL DEFAULT '',
			sync_dir TEXT NOT NULL DEFAULT '',
			pending_uploads INTEGER NOT NULL DEFAULT 0,
			pending_tombstones INTEGER NOT NULL DEFAULT 0,
			conflicts INTEGER NOT NULL DEFAULT 0,
			devices INTEGER NOT NULL DEFAULT 0,
			last_sync_at_ns INTEGER NOT NULL DEFAULT 0,
			allowed_classes TEXT NOT NULL DEFAULT 'input_method,learning_data',
			pending_ack INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT OR IGNORE INTO sync_device_status(id) VALUES(1)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Execute(ctx, stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &SQLiteStateStore{db: db, path: path}, nil
}

// InspectStateFileReadOnly reads an existing SQLite state database without
// creating one if it does not yet exist. Suitable for offline status queries.
func InspectStateFileReadOnly(ctx context.Context, path string) (watermark uint64, recoveryReady bool, meta DeviceStatusMetadata, exists bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	info, statErr := os.Lstat(path)
	if os.IsNotExist(statErr) {
		return 0, false, DeviceStatusMetadata{AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses()}, false, nil
	}
	if statErr != nil {
		return 0, false, DeviceStatusMetadata{}, false, statErr
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, false, DeviceStatusMetadata{}, false, errors.New("sync: state database path is not a regular file")
	}
	db, openErr := sqlite.Open(ctx, path, sqlite.OpenOptions{ReadOnly: true, BusyTimeout: 2 * time.Second})
	if openErr != nil {
		return 0, false, DeviceStatusMetadata{}, true, openErr
	}
	defer db.Close()
	tmp := &SQLiteStateStore{db: db, path: path}
	w, ready, loadErr := tmp.Load(ctx)
	if loadErr != nil {
		return 0, false, DeviceStatusMetadata{}, true, loadErr
	}
	m, metaErr := tmp.LoadStatusMeta(ctx)
	if metaErr != nil {
		// Older state DBs before sync_device_status was added remain readable.
		m = DeviceStatusMetadata{AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses()}
	}
	return w, ready, m, true, nil
}

func (s *SQLiteStateStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *SQLiteStateStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *SQLiteStateStore) Load(ctx context.Context) (uint64, bool, error) {
	if s == nil {
		return 0, false, coresync.ErrStateStoreNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, false, coresync.ErrStateStoreNil
	}
	rows, err := s.db.Query(ctx, `SELECT watermark, recovery_ready FROM sync_device_state WHERE id=1`)
	if err != nil {
		return 0, false, err
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 2 {
		return 0, false, errors.New("sync: invalid device state")
	}
	w, ok := rows.Values[0][0].(int64)
	if !ok || w < 0 {
		return 0, false, errors.New("sync: invalid watermark")
	}
	r, ok := rows.Values[0][1].(int64)
	if !ok {
		return 0, false, errors.New("sync: invalid recovery state")
	}
	return uint64(w), r != 0, nil
}

func (s *SQLiteStateStore) Save(ctx context.Context, watermark uint64, recoveryReady bool) error {
	if s == nil {
		return coresync.ErrStateStoreNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := coresync.ValidateWatermark(watermark); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return coresync.ErrStateStoreNil
	}
	ready := int64(0)
	if recoveryReady {
		ready = 1
	}
	_, err := s.db.Execute(ctx, `UPDATE sync_device_state SET watermark=?, recovery_ready=? WHERE id=1`, int64(watermark), ready)
	if err != nil {
		return fmt.Errorf("sync: save device state: %w", err)
	}
	return nil
}

func (s *SQLiteStateStore) LoadStatusMeta(ctx context.Context) (DeviceStatusMetadata, error) {
	if s == nil {
		return DeviceStatusMetadata{}, coresync.ErrStateStoreNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return DeviceStatusMetadata{}, coresync.ErrStateStoreNil
	}
	rows, err := s.db.Query(ctx, `SELECT account_id, device_id, sync_dir, pending_uploads, pending_tombstones, conflicts, devices, last_sync_at_ns, allowed_classes, pending_ack FROM sync_device_status WHERE id=1`)
	if err != nil {
		return DeviceStatusMetadata{}, err
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) < 10 {
		return DeviceStatusMetadata{AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses()}, nil
	}
	row := rows.Values[0]
	var lastSync time.Time
	if ns := stateInt64(row, 7); ns > 0 {
		lastSync = time.Unix(0, ns).UTC()
	}
	allowed := parseAllowedClasses(stateStr(row, 8))
	if len(allowed) == 0 {
		allowed = coresync.Phase1SyncPlan().AllowedClasses()
	}
	pendingAck := uint64(0)
	if pa := stateInt64(row, 9); pa > 0 {
		pendingAck = uint64(pa)
	}
	return DeviceStatusMetadata{
		AccountID:         stateStr(row, 0),
		DeviceID:          stateStr(row, 1),
		SyncDir:           stateStr(row, 2),
		PendingUploads:    int(stateInt64(row, 3)),
		PendingTombstones: int(stateInt64(row, 4)),
		Conflicts:         int(stateInt64(row, 5)),
		Devices:           int(stateInt64(row, 6)),
		LastSyncAt:        lastSync,
		AllowedClasses:    allowed,
		PendingAck:        pendingAck,
	}, nil
}

func (s *SQLiteStateStore) SaveStatusMeta(ctx context.Context, meta DeviceStatusMetadata) error {
	if s == nil {
		return coresync.ErrStateStoreNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := coresync.ValidateWatermark(meta.PendingAck); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return coresync.ErrStateStoreNil
	}
	allowed := meta.AllowedClasses
	if len(allowed) == 0 {
		allowed = coresync.Phase1SyncPlan().AllowedClasses()
	}
	var lastNS int64
	if !meta.LastSyncAt.IsZero() {
		lastNS = meta.LastSyncAt.UTC().UnixNano()
	}
	_, err := s.db.Execute(
		ctx,
		`UPDATE sync_device_status SET account_id=?, device_id=?, sync_dir=?, pending_uploads=?, pending_tombstones=?, conflicts=?, devices=?, last_sync_at_ns=?, allowed_classes=?, pending_ack=? WHERE id=1`,
		strings.TrimSpace(meta.AccountID),
		strings.TrimSpace(meta.DeviceID),
		strings.TrimSpace(meta.SyncDir),
		maxInt(meta.PendingUploads, 0),
		maxInt(meta.PendingTombstones, 0),
		maxInt(meta.Conflicts, 0),
		maxInt(meta.Devices, 0),
		lastNS,
		formatAllowedClasses(allowed),
		int64(meta.PendingAck),
	)
	if err != nil {
		return fmt.Errorf("sync: save device status: %w", err)
	}
	return nil
}

func (s *SQLiteStateStore) SetPendingAck(ctx context.Context, sequence uint64) error {
	if s == nil {
		return coresync.ErrStateStoreNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := coresync.ValidateWatermark(sequence); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return coresync.ErrStateStoreNil
	}
	_, err := s.db.Execute(ctx, `UPDATE sync_device_status SET pending_ack=? WHERE id=1`, int64(sequence))
	return err
}

func (s *SQLiteStateStore) PendingAck(ctx context.Context) (uint64, error) {
	meta, err := s.LoadStatusMeta(ctx)
	if err != nil {
		return 0, err
	}
	return meta.PendingAck, nil
}

// LoadStatus implements coresync.StatusStore using only local persisted state.
func (s *SQLiteStateStore) LoadStatus(ctx context.Context) (coresync.LocalStatus, error) {
	w, _, err := s.Load(ctx)
	if err != nil {
		return coresync.LocalStatus{}, err
	}
	meta, err := s.LoadStatusMeta(ctx)
	if err != nil {
		return coresync.LocalStatus{}, err
	}
	return coresync.LocalStatus{
		LocalVersion:      int64(w),
		PendingUploads:    meta.PendingUploads,
		PendingTombstones: meta.PendingTombstones,
		Conflicts:         meta.Conflicts,
		LastSyncAt:        meta.LastSyncAt,
		Devices:           meta.Devices,
		AllowedClasses:    meta.AllowedClasses,
		SyncDir:           meta.SyncDir,
	}, nil
}

// NewDirStatusReader builds a read-only SyncStatusReader backed by the local
// SQLiteStateStore and an optional configured/default shared sync directory.
// It never performs network I/O or writes to disk.
func NewDirStatusReader(stateStore *SQLiteStateStore, defaultSyncDir string) func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
	return func(ctx context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		res := host.OperationResult{State: host.StateReady, ObservedAt: time.Now().UTC()}
		if stateStore == nil {
			res.State = host.StateUnknown
			res.Code = "state_unavailable"
			return coresync.StatusSnapshot{AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses()}, res, coresync.ErrStateStoreNil
		}
		watermark, recoveryReady, err := stateStore.Load(ctx)
		if err != nil {
			res.State = host.StateUnknown
			res.Code = "state_read_failed"
			return coresync.StatusSnapshot{AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses()}, res, err
		}
		meta, err := stateStore.LoadStatusMeta(ctx)
		if err != nil {
			res.State = host.StateUnknown
			res.Code = "state_status_read_failed"
			return coresync.StatusSnapshot{AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses()}, res, err
		}
		effectiveDir := strings.TrimSpace(meta.SyncDir)
		if effectiveDir == "" && strings.TrimSpace(defaultSyncDir) != "" {
			if info, statErr := os.Lstat(defaultSyncDir); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				effectiveDir = defaultSyncDir
			}
		}
		allowed := meta.AllowedClasses
		if len(allowed) == 0 {
			allowed = coresync.Phase1SyncPlan().AllowedClasses()
		}
		configured := strings.TrimSpace(meta.AccountID) != "" || recoveryReady || effectiveDir != ""
		snap := coresync.StatusSnapshot{
			LoggedIn:          configured,
			RecoveryReady:     recoveryReady,
			LocalVersion:      int64(watermark),
			PendingUploads:    maxInt(meta.PendingUploads, 0),
			PendingTombstones: maxInt(meta.PendingTombstones, 0),
			Conflicts:         maxInt(meta.Conflicts, 0),
			LastSyncAt:        meta.LastSyncAt,
			Devices:           maxInt(meta.Devices, 0),
			AllowedClasses:    allowed,
			SyncDir:           effectiveDir,
		}
		if configured {
			acct := strings.TrimSpace(meta.AccountID)
			if acct == "" {
				acct = "local-dir"
			}
			snap.Account = coresync.RedactIdentifier(acct)
			if strings.TrimSpace(meta.DeviceID) != "" {
				snap.DeviceID = coresync.RedactIdentifier(meta.DeviceID)
			}
		}
		return snap, res, nil
	}
}

func formatAllowedClasses(classes []coresync.DataClass) string {
	parts := make([]string, 0, len(classes))
	for _, c := range classes {
		if c == coresync.Passwords || c == coresync.KeyManager || strings.TrimSpace(string(c)) == "" {
			continue
		}
		parts = append(parts, string(c))
	}
	return strings.Join(parts, ",")
}

func parseAllowedClasses(raw string) []coresync.DataClass {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []coresync.DataClass
	for _, part := range strings.Split(raw, ",") {
		c := coresync.DataClass(strings.TrimSpace(part))
		if c == "" || c == coresync.Passwords || c == coresync.KeyManager {
			continue
		}
		out = append(out, c)
	}
	return out
}

func stateStr(row []any, idx int) string {
	if idx >= len(row) {
		return ""
	}
	if v, ok := row[idx].(string); ok {
		return v
	}
	return ""
}

func stateInt64(row []any, idx int) int64 {
	if idx >= len(row) {
		return 0
	}
	if v, ok := row[idx].(int64); ok {
		return v
	}
	return 0
}

func maxInt(v, floor int) int {
	if v < floor {
		return floor
	}
	return v
}
