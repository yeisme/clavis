package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/plugins/sqlite"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

type storeFactory struct {
	name string
	open func(t *testing.T) coresync.SyncStore
}

func TestSQLiteSyncStoreContract(t *testing.T) {
	factories := []storeFactory{
		{name: "sqlite-memory", open: func(t *testing.T) coresync.SyncStore {
			s, err := NewSQLiteSyncStore(context.Background(), ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			return s
		}},
	}
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) { runStoreContract(t, factory.open(t)) })
	}
}

func runStoreContract(t *testing.T, store coresync.SyncStore) {
	t.Helper()
	defer store.Close()
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterAccount("acct"); err == nil {
		t.Fatal("duplicate account accepted")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	if err := store.PairDeviceWithCredential("acct", "device", pub, credential); err != nil {
		t.Fatal(err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if err := store.PairDeviceWithCredential("acct", "device", other.Public().(ed25519.PublicKey), credential); err == nil {
		t.Fatalf("device overwrite error = %v", err)
	}
	e, err := testClientEnvelope(priv, "acct", "device", "object", 1, coresync.InputMethod, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UploadEnvelope("acct", "device", "object", 1, e); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEnvelope("acct", "object")
	if err != nil || string(got.Ciphertext) != string(e.Ciphertext) {
		t.Fatalf("envelope = %v, %v", got, err)
	}
	if err := store.UploadEnvelope("acct", "device", "object", 1, e); err == nil {
		t.Fatal("stale upload accepted")
	}

	d := coresync.DeleteInstruction{AccountID: "acct", ObjectID: "object", DeviceID: "device", Version: 2, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)
	tombstone, err := store.SubmitDelete(d)
	if err != nil {
		t.Fatal(err)
	}
	if !store.VerifyTombstone(tombstone) {
		t.Fatal("stored tombstone did not verify")
	}
	tampered := tombstone
	tampered.DeletedAt = tampered.DeletedAt.Add(1)
	if store.VerifyTombstone(tampered) {
		t.Fatal("tombstone with tampered DeletedAt accepted")
	}
	e3, err := testClientEnvelope(priv, "acct", "device", "object", 3, coresync.InputMethod, []byte("resurrection"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UploadEnvelope("acct", "device", "object", 3, e3); err == nil {
		t.Fatal("resurrection accepted")
	}
	batch, err := store.SyncTombstones("acct", "device", ^uint64(0))
	if err != nil || len(batch) != 1 {
		t.Fatalf("sync batch = %+v, %v", batch, err)
	}
	issued, ok, err := store.IssuedBatch("acct", "device")
	if err != nil || !ok || issued.From != tombstone.Sequence || issued.To != tombstone.Sequence {
		t.Fatalf("issued batch = %+v, %v, %v", issued, ok, err)
	}
	if err := store.ConfirmTombstones("acct", "device", tombstone.Sequence); err != nil {
		t.Fatal(err)
	}
	watermark, err := store.Watermark("acct", "device")
	if err != nil || watermark != tombstone.Sequence {
		t.Fatalf("watermark = %d, %v", watermark, err)
	}
	if err := store.ConfirmTombstones("acct", "device", tombstone.Sequence); err == nil {
		t.Fatal("repeated confirmation accepted")
	}
	if events := store.AuditForAccount("acct"); len(events) != 1 || events[0].ObjectDigest == "" {
		t.Fatalf("audit = %+v", events)
	}
	if events := store.AuditForAccount("other"); len(events) != 0 {
		t.Fatal("cross-account audit leaked")
	}
}

func TestSQLiteStoreRestartRestoresProtocolState(t *testing.T) {
	path := provisionSQLiteTestPath(t)
	store, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := store.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	live, err := testClientEnvelope(priv, "acct", "device", "live", 7, coresync.InputMethod, []byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UploadEnvelope("acct", "device", "live", 7, live); err != nil {
		t.Fatal(err)
	}
	d := coresync.DeleteInstruction{AccountID: "acct", ObjectID: "gone", DeviceID: "device", Version: 4, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)
	tombstone, err := store.SubmitDelete(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncTombstones("acct", "device", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.LoginWithCredential("acct", credential); err != nil {
		t.Fatalf("credential not restored: %v", err)
	}
	if version, err := reopened.ObjectVersion("acct", "live"); err != nil || version != 7 {
		t.Fatalf("live object version after restart = %d, %v", version, err)
	}
	if _, err := reopened.GetEnvelope("acct", "live"); err != nil {
		t.Fatalf("live envelope after restart: %v", err)
	}
	if !reopened.VerifyTombstone(tombstone) {
		t.Fatal("tombstone not restored")
	}
	batch, ok, err := reopened.IssuedBatch("acct", "device")
	if err != nil || !ok || batch.To != tombstone.Sequence {
		t.Fatalf("issued batch after restart = %+v, %v, %v", batch, ok, err)
	}
	if err := reopened.ConfirmTombstones("acct", "device", tombstone.Sequence); err != nil {
		t.Fatal(err)
	}
	watermark, err := reopened.Watermark("acct", "device")
	if err != nil || watermark != tombstone.Sequence {
		t.Fatalf("restored watermark = %d, %v", watermark, err)
	}
}

func TestSQLiteStoreRejectsPlaintextAndIsolatesAccounts(t *testing.T) {
	store, err := NewSQLiteSyncStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credA, _ := store.RegisterAccount("a")
	credB, _ := store.RegisterAccount("b")
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	if err := store.PairDeviceWithCredential("a", "device", privA.Public().(ed25519.PublicKey), credA); err != nil {
		t.Fatal(err)
	}
	if err := store.PairDeviceWithCredential("b", "device", privB.Public().(ed25519.PublicKey), credB); err != nil {
		t.Fatal(err)
	}
	if err := store.UploadEnvelope("a", "device", "object", 1, coresync.EncryptedEnvelope{}); err == nil {
		t.Fatal("invalid/plaintext envelope accepted")
	}
	if _, err := store.GetEnvelope("b", "object"); err == nil {
		t.Fatal("cross-account object leaked")
	}
	if events := store.AuditForAccount("b"); len(events) != 0 {
		t.Fatal("cross-account audit leaked")
	}
}

func TestSQLiteDeletePurgesHigherVersionObjectPermanently(t *testing.T) {
	store, err := NewSQLiteSyncStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := store.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	e, err := testClientEnvelope(priv, "acct", "device", "object", 9, coresync.InputMethod, []byte("live"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UploadEnvelope("acct", "device", "object", 9, e); err != nil {
		t.Fatal(err)
	}
	d := coresync.DeleteInstruction{AccountID: "acct", ObjectID: "object", DeviceID: "device", Version: 2, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)
	if _, err := store.SubmitDelete(d); err != nil {
		t.Fatalf("delete below live object version rejected: %v", err)
	}
	if _, err := store.GetEnvelope("acct", "object"); !errors.Is(err, coresync.ErrStoreObjectMissing) {
		t.Fatalf("object after delete = %v, want missing", err)
	}
	resurrection, err := testClientEnvelope(priv, "acct", "device", "object", 10, coresync.InputMethod, []byte("resurrection"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UploadEnvelope("acct", "device", "object", 10, resurrection); err == nil {
		t.Fatal("resurrection accepted after permanent deletion")
	}
	if version, err := store.ObjectVersion("acct", "object"); err != nil || version != 9 {
		t.Fatalf("deletion boundary = %d, %v; want 9", version, err)
	}
	path := provisionSQLiteTestPath(t)
	fileStore, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := fileStore.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	if err := fileStore.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), cred); err != nil {
		t.Fatal(err)
	}
	if err := fileStore.UploadEnvelope("acct", "device", "object", 9, e); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.SubmitDelete(coresync.DeleteInstruction{AccountID: "acct", ObjectID: "object", DeviceID: "device", Version: 2, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)); err != nil {
		t.Fatal(err)
	}
	if err := fileStore.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if version, err := restarted.ObjectVersion("acct", "object"); err != nil || version != 9 {
		t.Fatalf("restarted deletion boundary = %d, %v; want 9", version, err)
	}
}

func TestSQLiteCrossConnectionDeleteIsAtomic(t *testing.T) {
	path := provisionSQLiteTestPath(t)
	one, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	credential, err := one.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := one.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	two, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	d := coresync.DeleteInstruction{AccountID: "acct", ObjectID: "same", DeviceID: "device", Version: 1, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)
	results := make(chan error, 2)
	go func() { _, e := one.SubmitDelete(d); results <- e }()
	go func() { _, e := two.SubmitDelete(d); results <- e }()
	var successes, stale int
	for range 2 {
		e := <-results
		if e == nil {
			successes++
		} else if errors.Is(e, coresync.ErrStoreVersion) {
			stale++
		} else {
			t.Fatalf("cross-connection delete error = %v", e)
		}
	}
	if successes != 1 || stale != 1 {
		t.Fatalf("delete results: successes=%d stale=%d", successes, stale)
	}
	if got, err := one.LatestSequence("acct"); err != nil || got != 1 {
		t.Fatalf("sequence = %d, %v", got, err)
	}
	if got := one.AuditForAccount("acct"); len(got) != 1 {
		t.Fatalf("audit rows = %d, want one", len(got))
	}
}

func TestSQLiteIssuedTombstoneBatchSurvivesNewDeleteAcrossStores(t *testing.T) {
	path := provisionSQLiteTestPath(t)
	first, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	credential, err := first.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	second, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	deleteTombstone := func(object string) coresync.Tombstone {
		t.Helper()
		instruction := coresync.DeleteInstruction{AccountID: "acct", ObjectID: object, DeviceID: "device", Version: 1, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)
		tombstone, err := first.SubmitDelete(instruction)
		if err != nil {
			t.Fatal(err)
		}
		return tombstone
	}
	firstTombstone := deleteTombstone("first")
	firstBatch, err := first.SyncTombstones("acct", "device", 0)
	if err != nil || len(firstBatch) != 1 || firstBatch[0].Sequence != firstTombstone.Sequence {
		t.Fatalf("first batch = %+v, %v", firstBatch, err)
	}
	secondTombstone := deleteTombstone("second")
	retriedBatch, err := second.SyncTombstones("acct", "device", 0)
	if err != nil || len(retriedBatch) != 1 || retriedBatch[0].Sequence != firstTombstone.Sequence {
		t.Fatalf("pending batch was replaced: %+v, %v", retriedBatch, err)
	}
	if err := second.ConfirmTombstones("acct", "device", firstTombstone.Sequence); err != nil {
		t.Fatalf("confirm old batch: %v", err)
	}
	watermark, err := first.Watermark("acct", "device")
	if err != nil || watermark != firstTombstone.Sequence {
		t.Fatalf("watermark after old confirmation = %d, %v", watermark, err)
	}
	nextBatch, err := first.SyncTombstones("acct", "device", 0)
	if err != nil || len(nextBatch) != 1 || nextBatch[0].Sequence != secondTombstone.Sequence {
		t.Fatalf("next batch = %+v, %v", nextBatch, err)
	}
	if err := second.ConfirmTombstones("acct", "device", secondTombstone.Sequence); err != nil {
		t.Fatalf("confirm next batch: %v", err)
	}
}

func TestSQLiteHistoricalSameObjectTombstonesAcrossRestart(t *testing.T) {
	path := provisionSQLiteTestPath(t)
	store, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	first := coresync.DeleteInstruction{AccountID: "acct", ObjectID: "same-object", DeviceID: "device", Version: 1, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)
	firstTombstone, err := store.SubmitDelete(first)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.SyncTombstones("acct", "device", 0)
	if err != nil || len(batch) != 1 || batch[0].Sequence != firstTombstone.Sequence {
		t.Fatalf("first batch = %+v, %v", batch, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second := coresync.DeleteInstruction{AccountID: "acct", ObjectID: "same-object", DeviceID: "device", Version: 2, ProtocolVersion: coresync.ProtocolVersion}.Sign(priv)
	secondTombstone, err := store.SubmitDelete(second)
	if err != nil {
		t.Fatal(err)
	}
	if !store.VerifyTombstone(firstTombstone) {
		t.Fatal("historical tombstone rejected after newer same-object delete")
	}
	if err := store.ConfirmTombstones("acct", "device", firstTombstone.Sequence); err != nil {
		t.Fatalf("confirm historical tombstone: %v", err)
	}
	batch, err = store.SyncTombstones("acct", "device", firstTombstone.Sequence)
	if err != nil || len(batch) != 1 || batch[0].Sequence != secondTombstone.Sequence {
		t.Fatalf("second batch = %+v, %v", batch, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !store.VerifyTombstone(secondTombstone) {
		t.Fatal("new tombstone did not verify after restart")
	}
	if err := store.ConfirmTombstones("acct", "device", secondTombstone.Sequence); err != nil {
		t.Fatalf("confirm new tombstone: %v", err)
	}
	if watermark, err := store.Watermark("acct", "device"); err != nil || watermark != secondTombstone.Sequence {
		t.Fatalf("final watermark = %d, %v", watermark, err)
	}
}

func TestSQLiteExternalDBCloseDoesNotCloseOwner(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteSyncStoreFromDB(db)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(context.Background(), `SELECT 1`); err != nil {
		t.Fatalf("external DB closed by store: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRejectsRelativeAndSymlinkDatabasePaths(t *testing.T) {
	t.Run("missing target", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.db")
		if _, err := NewSQLiteSyncStore(context.Background(), path); err == nil {
			t.Fatal("missing database file accepted")
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("missing database file was created: %v", err)
		}
	})
	t.Run("relative", func(t *testing.T) {
		if _, err := NewSQLiteSyncStore(context.Background(), "state.db"); err == nil {
			t.Fatal("relative database path accepted")
		}
	})
	for _, suffix := range []string{"", "-wal", "-shm"} {
		t.Run("symlink"+suffix, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.db")
			target := filepath.Join(dir, "target"+suffix)
			if err := os.WriteFile(target, []byte("not a database"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path+suffix); err != nil {
				t.Fatal(err)
			}
			if _, err := NewSQLiteSyncStore(context.Background(), path); err == nil {
				t.Fatal("symlink database component accepted")
			}
		})
	}
}

func TestSQLiteCloseRejectsReplacedRegularSidecar(t *testing.T) {
	path := provisionSQLiteTestPath(t)
	store, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	link := path + "-wal"
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("sidecar"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mustFileMode(t, link); got != 0o600 {
		t.Fatalf("regular sidecar mode = %o, want 600", got)
	}
	if got := mustFileMode(t, outside); got != 0o644 {
		t.Fatalf("external file mode changed to %o", got)
	}
}

func provisionSQLiteTestPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sync")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestSQLiteCloseReleasesConnectionWhenProtectionFails(t *testing.T) {
	path := provisionSQLiteTestPath(t)
	store, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	link := path + "-wal"
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	// SQLite may create sidecars lazily; replacing an existing sidecar with a
	// symlink gives Close a deterministic permission-repair failure.
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err == nil {
		t.Fatal("Close unexpectedly succeeded despite symlink sidecar")
	}
	if _, err := store.db.Execute(context.Background(), `SELECT 1`); !errors.Is(err, sqlite.ErrClosed) {
		t.Fatalf("connection remains usable after failed Close: %v", err)
	}
}

func TestSQLiteRejectsWritableExistingParentDirectories(t *testing.T) {
	for _, mode := range []os.FileMode{0o775, 0o777} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			parent := t.TempDir()
			unsafe := filepath.Join(parent, "unsafe")
			if err := os.Mkdir(unsafe, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(unsafe, mode); err != nil {
				t.Fatal(err)
			}
			_, err := NewSQLiteSyncStore(context.Background(), filepath.Join(unsafe, "state.db"))
			if err == nil {
				t.Fatalf("writable parent mode %o accepted", mode)
			}
			info, statErr := os.Stat(unsafe)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if got := info.Mode().Perm(); got != mode {
				t.Fatalf("existing parent mode changed from %o to %o", mode, got)
			}
		})
	}
}

func TestSQLiteRejectsSymlinkParentComponent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSQLiteSyncStore(context.Background(), filepath.Join(link, "nested", "state.db")); err == nil {
		t.Fatal("symlink parent component accepted")
	}
	if _, err := os.Stat(filepath.Join(target, "nested")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was modified: %v", err)
	}
}

func TestSQLiteFileModesProtectNewDatabaseAndSidecars(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "new", "private")
	path := filepath.Join(dir, "state.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteSyncStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := store.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{dir, path} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 && file == dir {
			t.Fatalf("directory mode = %o", got)
		}
		if got := info.Mode().Perm(); got != 0o600 && file == path {
			t.Fatalf("database mode = %o", got)
		}
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("existing parent mode changed to %o", got)
	}
}

func TestSQLiteStoreConcurrentAccess(t *testing.T) {
	store, err := NewSQLiteSyncStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := store.PairDeviceWithCredential("acct", "device", priv.Public().(ed25519.PublicKey), credential); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.Watermark("acct", "device"); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func testClientEnvelope(priv ed25519.PrivateKey, account, device, object string, version uint64, class coresync.DataClass, payload []byte) (coresync.EncryptedEnvelope, error) {
	return coresync.NewEncryptedEnvelope(priv, account, device, object, version, class, payload)
}
