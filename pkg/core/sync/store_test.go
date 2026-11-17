package sync

import (
	"crypto/ed25519"
	"crypto/rand"
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
