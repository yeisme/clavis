package sync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"sync"
	"testing"
)

type storeFactory struct {
	name string
	open func(t *testing.T) SyncStore
}

func TestSyncStoreContract(t *testing.T) {
	factories := []storeFactory{
		{name: "memory-server", open: func(t *testing.T) SyncStore {
			s, err := NewServerStore(NewServer())
			if err != nil {
				t.Fatal(err)
			}
			return s
		}},
		{name: "sqlite-memory", open: func(t *testing.T) SyncStore {
			s, err := NewSQLiteStore(context.Background(), ":memory:")
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

func runStoreContract(t *testing.T, store SyncStore) {
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
	e, err := clientEnvelope(priv, "acct", "device", "object", 1, InputMethod, []byte("secret"))
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

	d := DeleteInstruction{AccountID: "acct", ObjectID: "object", DeviceID: "device", Version: 2, ProtocolVersion: ProtocolVersion}
	d.Signature = ed25519.Sign(priv, d.canonical())
	tombstone, err := store.SubmitDelete(d)
	if err != nil {
		t.Fatal(err)
	}
	if !store.VerifyTombstone(tombstone) {
		t.Fatal("stored tombstone did not verify")
	}
	e3, err := clientEnvelope(priv, "acct", "device", "object", 3, InputMethod, []byte("resurrection"))
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
	path := filepath.Join(t.TempDir(), "sync", "state.db")
	store, err := NewSQLiteStore(context.Background(), path)
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
	live, err := clientEnvelope(priv, "acct", "device", "live", 7, InputMethod, []byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UploadEnvelope("acct", "device", "live", 7, live); err != nil {
		t.Fatal(err)
	}
	d := DeleteInstruction{AccountID: "acct", ObjectID: "gone", DeviceID: "device", Version: 4, ProtocolVersion: ProtocolVersion}
	d.Signature = ed25519.Sign(priv, d.canonical())
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

	reopened, err := NewSQLiteStore(context.Background(), path)
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
	store, err := NewSQLiteStore(context.Background(), ":memory:")
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
	if err := store.UploadEnvelope("a", "device", "object", 1, EncryptedEnvelope{}); err == nil {
		t.Fatal("invalid/plaintext envelope accepted")
	}
	if _, err := store.GetEnvelope("b", "object"); err == nil {
		t.Fatal("cross-account object leaked")
	}
	if events := store.AuditForAccount("b"); len(events) != 0 {
		t.Fatal("cross-account audit leaked")
	}
}

func TestSQLiteStoreConcurrentAccess(t *testing.T) {
	store, err := NewSQLiteStore(context.Background(), ":memory:")
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
