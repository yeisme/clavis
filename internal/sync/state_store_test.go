package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

func TestSQLiteStateStorePersistsWatermark(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := OpenSQLiteStateStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, 17, true); err != nil {
		t.Fatal(err)
	}
	if got, ready, err := store.Load(ctx); err != nil || got != 17 || !ready {
		t.Fatalf("load = %d, %v, %v", got, ready, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	meta := DeviceStatusMetadata{
		AccountID:         "alice@example.com",
		DeviceID:          "macbook-pro",
		SyncDir:           "/tmp/clavis-sync",
		PendingUploads:    1,
		PendingTombstones: 2,
		Conflicts:         3,
		Devices:           2,
		LastSyncAt:        now,
		AllowedClasses:    coresync.Phase1SyncPlan().AllowedClasses(),
		PendingAck:        5,
	}
	if err := store.SaveStatusMeta(ctx, meta); err != nil {
		t.Fatal(err)
	}
	st, err := store.LoadStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.LocalVersion != 17 || st.Conflicts != 3 || st.Devices != 2 || len(st.AllowedClasses) != 2 {
		t.Fatalf("LoadStatus = %+v", st)
	}
	reader := NewDirStatusReader(store, "")
	snap, res, err := reader(ctx)
	if err != nil || res.State != host.StateReady || !snap.LoggedIn || snap.LocalVersion != 17 || snap.Conflicts != 3 || len(snap.AllowedClasses) != 2 {
		t.Fatalf("NewDirStatusReader = %+v, %+v, %v", snap, res, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Read-only inspect works offline without modifying or creating files.
	w, ready, gotMeta, exists, err := InspectStateFileReadOnly(ctx, path)
	if err != nil || !exists || w != 17 || !ready || gotMeta.Conflicts != 3 || gotMeta.PendingAck != 5 {
		t.Fatalf("InspectStateFileReadOnly = %d, %v, %+v, %v, %v", w, ready, gotMeta, exists, err)
	}
	missingPath := filepath.Join(t.TempDir(), "does-not-exist.db")
	_, _, _, missingExists, err := InspectStateFileReadOnly(ctx, missingPath)
	if err != nil || missingExists {
		t.Fatalf("InspectStateFileReadOnly(missing) = exists=%v err=%v", missingExists, err)
	}
	if _, statErr := os.Stat(missingPath); !os.IsNotExist(statErr) {
		t.Fatalf("InspectStateFileReadOnly created missing file: %v", statErr)
	}
	_ = coresync.ErrStateStoreNil
}
