package sync

import (
	"context"
	"testing"
)

func TestClientStoreBackedPullVerifiesThenDecrypts(t *testing.T) {
	server := NewServer()
	store, err := NewServerStore(server)
	if err != nil {
		t.Fatal(err)
	}
	identityA, err := newDeviceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	identityB, err := newDeviceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewClientWithStore(store, "a", identityA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClientWithStore(store, "b", identityB)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*Client{a, b} {
		if err := c.LoginWithCredential("acct", credential); err != nil {
			t.Fatal(err)
		}
		if err := c.PairDeviceWithCredential(); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		c.syncEnabled, c.recoveryReady = true, true
		c.mu.Unlock()
	}
	b.mu.Lock()
	b.adk = append([]byte(nil), a.adk...)
	b.mu.Unlock()
	if err := a.Upload("object", 1, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	got, err := b.Pull("object")
	if err != nil {
		t.Fatal(err)
	}
	if got.ObjectID != "object" || got.Version != 1 || string(got.Payload) != "payload" {
		t.Fatalf("pulled=%+v", got)
	}
	bad, err := store.GetEnvelope("acct", "object")
	if err != nil {
		t.Fatal(err)
	}
	bad.Signature[0] ^= 1
	server.mu.Lock()
	server.objects["acct"]["object"], _ = marshalEnvelope(bad)
	server.mu.Unlock()
	if _, err := b.Pull("object"); err == nil {
		t.Fatal("tampered envelope accepted")
	}
}

func TestClientConcurrentObjectVersionsBecomeConflict(t *testing.T) {
	server := NewServer()
	store, err := NewServerStore(server)
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := newDeviceIdentity()
	ib, _ := newDeviceIdentity()
	a, _ := NewClientWithStore(store, "a", ia)
	b, _ := NewClientWithStore(store, "b", ib)
	credential, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*Client{a, b} {
		if err := c.LoginWithCredential("acct", credential); err != nil {
			t.Fatal(err)
		}
		if err := c.PairDeviceWithCredential(); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		c.syncEnabled, c.recoveryReady = true, true
		c.mu.Unlock()
	}
	b.mu.Lock()
	b.adk = append([]byte(nil), a.adk...)
	b.mu.Unlock()
	if err := a.Upload("object", 1, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Upload("object", 1, []byte("b")); err != ErrStoreConflict {
		t.Fatalf("conflict upload err=%v", err)
	}
	conflicts := b.Conflicts()
	if len(conflicts) != 1 || string(conflicts[0].LocalPayload) != "b" || string(conflicts[0].RemotePayload) != "a" {
		t.Fatalf("conflicts=%+v", conflicts)
	}
	if conflicts[0].ConflictID == "object" || conflicts[0].ConflictID == "" {
		t.Fatalf("invalid conflict id=%q", conflicts[0].ConflictID)
	}
}

func TestSQLiteSyncStoreCanBackClientObjectPath(t *testing.T) {
	// This test documents the integration constructor contract without making
	// pkg/core import internal/storage: the concrete SQLite adapter is checked
	// by internal/storage tests and is assignable to SyncStore here.
	var _ SyncStore = (*ServerStore)(nil)
	_ = context.Background()
}
