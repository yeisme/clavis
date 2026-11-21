package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/egoist/mygo/plugins/sqlite"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

// SQLiteStateStore is the platform/storage adapter for sync's standard-library
// StateStore interface. Database details stay out of pkg/core.
type SQLiteStateStore struct{ db *sqlite.DB }

var _ coresync.StateStore = (*SQLiteStateStore)(nil)

func OpenSQLiteStateStore(ctx context.Context, path string) (*SQLiteStateStore, error) {
	if path == "" {
		return nil, errors.New("sync: state database path is required")
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := db.Execute(ctx, `CREATE TABLE IF NOT EXISTS sync_device_state (id INTEGER PRIMARY KEY CHECK (id=1), watermark INTEGER NOT NULL DEFAULT 0, recovery_ready INTEGER NOT NULL DEFAULT 0)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Execute(ctx, `INSERT OR IGNORE INTO sync_device_state(id) VALUES(1)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStateStore{db: db}, nil
}
func (s *SQLiteStateStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
func (s *SQLiteStateStore) Load(ctx context.Context) (uint64, bool, error) {
	if s == nil || s.db == nil {
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
	if s == nil || s.db == nil {
		return coresync.ErrStateStoreNil
	}
	if err := coresync.ValidateWatermark(watermark); err != nil {
		return err
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
