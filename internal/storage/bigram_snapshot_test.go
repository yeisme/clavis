package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

func TestBigramSnapshotRoundTripAndDeterminism(t *testing.T) {
	created := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	snapA := BigramSnapshot{
		CreatedAt:      created,
		SourceSchema:   AppSchemaVersion,
		Generation:     42,
		FlushWatermark: 7,
		Entries: []BigramSnapshotEntry{
			{PrevWord: "我", Word: "喜欢", Reading: "xihuan", Frequency: 5, LastUsedNS: 200, UsageCount: 3},
			{PrevWord: "我", Word: "们", Reading: "men", Frequency: 12, LastUsedNS: 300, UsageCount: 9},
			{PrevWord: "你", Word: "好", Reading: "hao", Frequency: 5, LastUsedNS: 200, UsageCount: 1},
		},
	}
	// Same logical entries supplied in reverse order must serialize byte-for-byte identically.
	snapB := BigramSnapshot{
		CreatedAt:      created,
		SourceSchema:   AppSchemaVersion,
		Generation:     42,
		FlushWatermark: 7,
		Entries: []BigramSnapshotEntry{
			snapA.Entries[2],
			snapA.Entries[0],
			snapA.Entries[1],
		},
	}

	rawA, err := EncodeBigramSnapshot(snapA)
	if err != nil {
		t.Fatalf("EncodeBigramSnapshot A: %v", err)
	}
	rawB, err := EncodeBigramSnapshot(snapB)
	if err != nil {
		t.Fatalf("EncodeBigramSnapshot B: %v", err)
	}
	if !bytes.Equal(rawA, rawB) {
		t.Fatal("encoding is not deterministic across entry permutations")
	}
	if !bytes.Equal(rawA[:8], []byte("CLVBG01\x00")) {
		t.Fatalf("unexpected magic header: %q", rawA[:8])
	}

	path := filepath.Join(t.TempDir(), BigramSnapshotFilename)
	if err := WriteBigramSnapshot(path, snapA); err != nil {
		t.Fatalf("WriteBigramSnapshot: %v", err)
	}
	got, err := ReadBigramSnapshot(path)
	if err != nil {
		t.Fatalf("ReadBigramSnapshot: %v", err)
	}
	if got.FormatVersion != BigramSnapshotFormatVersion || got.SourceSchema != AppSchemaVersion ||
		got.Generation != 42 || got.FlushWatermark != 7 || !got.CreatedAt.Equal(created) {
		t.Fatalf("unexpected header metadata: %+v", got)
	}
	wantOrder := []BigramSnapshotEntry{
		{PrevWord: "我", Word: "们", Reading: "men", Frequency: 12, LastUsedNS: 300, UsageCount: 9},
		{PrevWord: "你", Word: "好", Reading: "hao", Frequency: 5, LastUsedNS: 200, UsageCount: 1},
		{PrevWord: "我", Word: "喜欢", Reading: "xihuan", Frequency: 5, LastUsedNS: 200, UsageCount: 3},
	}
	if !reflect.DeepEqual(got.Entries, wantOrder) {
		t.Fatalf("round-trip entries = %+v, want %+v", got.Entries, wantOrder)
	}
}

func TestBigramSnapshotRejectsCorruptionTruncationVersionAndChecksum(t *testing.T) {
	valid := BigramSnapshot{
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
		SourceSchema:   AppSchemaVersion,
		Generation:     99,
		FlushWatermark: 3,
		Entries: []BigramSnapshotEntry{
			{PrevWord: "我", Word: "们", Reading: "men", Frequency: 4, LastUsedNS: 100},
		},
	}
	raw, err := EncodeBigramSnapshot(valid)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		mutate  func([]byte) []byte
		wantErr error
	}{
		{
			name:    "truncated-header",
			mutate:  func(b []byte) []byte { return b[:20] },
			wantErr: ErrBigramSnapshotInvalid,
		},
		{
			name:    "truncated-body",
			mutate:  func(b []byte) []byte { return b[:len(b)-5] },
			wantErr: ErrBigramSnapshotChecksum,
		},
		{
			name: "bad-magic",
			mutate: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				cp[0] = 'X'
				return cp
			},
			wantErr: ErrBigramSnapshotInvalid,
		},
		{
			name: "wrong-version",
			mutate: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				binary.LittleEndian.PutUint16(cp[8:10], 99)
				return cp
			},
			wantErr: ErrBigramSnapshotVersion,
		},
		{
			name: "flipped-body-bit",
			mutate: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				cp[bigramSnapshotHeaderBytes+2] ^= 0x01
				return cp
			},
			wantErr: ErrBigramSnapshotChecksum,
		},
		{
			name: "corrupted-trailing-checksum",
			mutate: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				cp[len(cp)-1] ^= 0xff
				return cp
			},
			wantErr: ErrBigramSnapshotChecksum,
		},
		{
			name: "tampered-source-digest-with-valid-trailer",
			mutate: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				cp[40] ^= 0x55
				payloadEnd := len(cp) - bigramSnapshotTrailerBytes
				sum := sha256.Sum256(cp[:payloadEnd])
				copy(cp[payloadEnd:], sum[:])
				return cp
			},
			wantErr: ErrBigramSnapshotChecksum,
		},
		{
			name: "oversized-payload",
			mutate: func(_ []byte) []byte {
				return make([]byte, MaxBigramSnapshotBytes+1)
			},
			wantErr: ErrBigramSnapshotTooLarge,
		},
	}

	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".bin")
			if err := os.WriteFile(path, tc.mutate(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ReadBigramSnapshot(path)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ReadBigramSnapshot(%s) err = %v, want errors.Is(%v)", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestLearningStoreBigramSnapshotColdStartFallbackAndInvalidation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	snapPath := filepath.Join(dir, "learning", BigramSnapshotFilename)

	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewLearningStoreWithOptions(ctx, db, LearningStoreOptions{
		DBPath:             dbPath,
		BigramSnapshotPath: snapPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.LoadedBigramFromSnapshot() {
		t.Fatal("expected cold start without existing snapshot to fall back to SQLite")
	}

	for i := 0; i < 3; i++ {
		if err := store.RecordIMECommit(ctx, "men", "们", "我", false); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := store.RecordIMECommit(ctx, "xihuan", "喜欢", "我", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(ctx); err != nil {
		t.Fatalf("store.Close snapshot persist: %v", err)
	}
	_ = db.Close()

	// 1. Valid matching snapshot: startup loads bigrams from binary snapshot fast-path.
	db2, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewLearningStoreWithOptions(ctx, db2, LearningStoreOptions{
		DBPath:             dbPath,
		BigramSnapshotPath: snapPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.LoadedBigramFromSnapshot() {
		t.Fatalf("expected valid matching snapshot fast-path, lastErr=%q", reloaded.LastBigramSnapshotError())
	}
	if got := reloaded.LookupBoost("men", "们", "我"); got != 3+3*2 {
		t.Fatalf("boost from snapshot = %d, want 9", got)
	}
	cands := reloaded.BigramWordsForPrefix("我", "m")
	if len(cands) != 1 || cands[0].Word != "们" || cands[0].Frequency != 3 {
		t.Fatalf("BigramWordsForPrefix from snapshot = %+v", cands)
	}
	_ = db2.Close()

	// 2. Corrupted snapshot: seamlessly falls back to SQLite without failing startup.
	if err := os.WriteFile(snapPath, []byte("corrupted-garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	db3, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := NewLearningStoreWithOptions(ctx, db3, LearningStoreOptions{
		DBPath:             dbPath,
		BigramSnapshotPath: snapPath,
	})
	if err != nil {
		t.Fatalf("corrupted snapshot must not fail startup: %v", err)
	}
	if fallback.LoadedBigramFromSnapshot() {
		t.Fatal("corrupted snapshot should not report snapshot hit")
	}
	if fallback.LastBigramSnapshotError() == "" {
		t.Fatal("expected diagnostic error recorded for corrupted snapshot")
	}
	if got := fallback.LookupBoost("men", "们", "我"); got != 9 {
		t.Fatalf("SQLite fallback boost = %d, want 9", got)
	}
	// Re-save valid snapshot, then mutate SQLite directly to trigger generation mismatch invalidation.
	if err := fallback.FlushBigramSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db3.Execute(ctx, `INSERT INTO ime_bigrams(prev_word,word,transition_count,last_used_ns) VALUES('你','好',5,999999)`); err != nil {
		t.Fatal(err)
	}
	_ = db3.Close()

	// 3. Stale generation mismatch: snapshot is invalidated and SQLite's newer bigram is loaded.
	db4, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db4.Close()
	invalidated, err := NewLearningStoreWithOptions(ctx, db4, LearningStoreOptions{
		DBPath:             dbPath,
		BigramSnapshotPath: snapPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if invalidated.LoadedBigramFromSnapshot() {
		t.Fatal("stale snapshot generation should have been invalidated")
	}
	if !strings.Contains(invalidated.LastBigramSnapshotError(), ErrBigramSnapshotStale.Error()) {
		t.Fatalf("expected stale diagnostic, got %q", invalidated.LastBigramSnapshotError())
	}
	if got := invalidated.LookupBoost("hao", "好", "你"); got != 10 {
		t.Fatalf("SQLite row missing after generation invalidation: boost=%d, want 10", got)
	}
}

func TestLearningStoreApplyBigramSnapshotRejectsStaleOnlineGeneration(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}

	observedGen := store.BigramGeneration()
	// Online learning happens while a background snapshot load is in flight.
	if err := store.RecordIMECommit(ctx, "hao", "好", "你", false); err != nil {
		t.Fatal(err)
	}
	staleSnap := BigramSnapshot{
		Entries: []BigramSnapshotEntry{
			{PrevWord: "老", Word: "词", Reading: "ci", Frequency: 50, LastUsedNS: 1},
		},
	}
	if store.ApplyBigramSnapshotIfCurrent(staleSnap, observedGen) {
		t.Fatal("stale snapshot overwrote newer online learning")
	}
	if got := store.LookupBoost("hao", "好", "你"); got != 3 {
		t.Fatalf("online learning lost: boost=%d", got)
	}
	if got := store.LookupBoost("ci", "词", "老"); got != 0 {
		t.Fatalf("stale snapshot leaked into mirror: boost=%d", got)
	}

	// Applying with the current generation succeeds.
	curGen := store.BigramGeneration()
	if !store.ApplyBigramSnapshotIfCurrent(staleSnap, curGen) {
		t.Fatal("current-generation snapshot replacement was rejected")
	}
	if got := store.LookupBoost("ci", "词", "老"); got != 100 {
		t.Fatalf("replaced snapshot boost = %d, want 100", got)
	}
}

func TestLearningStoreBigramSnapshotConcurrentRace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "race.db")
	snapPath := filepath.Join(dir, BigramSnapshotFilename)

	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewLearningStoreWithOptions(ctx, db, LearningStoreOptions{
		DBPath:             dbPath,
		BigramSnapshotPath: snapPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	// Writer goroutines recording commits.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = store.RecordIMECommit(ctx, fmt.Sprintf("py%d", i%5), fmt.Sprintf("词%d", i%5), fmt.Sprintf("前%d", id), false)
			}
		}(g)
	}
	// Snapshot publishers running concurrently.
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_ = store.FlushBigramSnapshot(ctx)
			}
		}()
	}
	// Concurrent hot-path readers + snapshot file readers.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_ = store.LookupBoost("py0", "词0", fmt.Sprintf("前%d", id))
				_ = store.BigramWordsForPrefix(fmt.Sprintf("前%d", id), "py")
				if snap, err := ReadBigramSnapshot(snapPath); err == nil {
					if snap.FormatVersion != BigramSnapshotFormatVersion {
						t.Errorf("invalid format version under concurrent read: %d", snap.FormatVersion)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("atomic snapshot read failed under concurrency: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	if err := store.Close(ctx); err != nil {
		t.Fatalf("final Close: %v", err)
	}
	snap, err := ReadBigramSnapshot(snapPath)
	if err != nil {
		t.Fatalf("final ReadBigramSnapshot: %v", err)
	}
	if len(snap.Entries) == 0 {
		t.Fatal("expected non-empty snapshot after concurrent commits")
	}
}
