package sync

import (
	"context"
	"errors"
	"math"
)

// StateStore persists non-secret sync state. Implementations belong outside
// pkg/core/sync when they need platform/database dependencies. Key material
// never enters this store; it belongs in a SecretStore.
type StateStore interface {
	Close() error
	Load(ctx context.Context) (watermark uint64, recoveryReady bool, err error)
	Save(ctx context.Context, watermark uint64, recoveryReady bool) error
}

var ErrStateStoreNil = errors.New("sync: nil state store")

// ValidateWatermark keeps SQLite-backed implementations from accepting a
// uint64 value that cannot be represented by SQLite's signed INTEGER type.
func ValidateWatermark(watermark uint64) error {
	if watermark > math.MaxInt64 {
		return errors.New("sync: watermark exceeds SQLite range")
	}
	return nil
}
