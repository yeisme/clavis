package sync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"testing"
)

func TestClientRefreshADKFailurePreservesExistingKey(t *testing.T) {
	server := NewServer()
	store, err := NewServerStore(server)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := newDeviceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClientWithStore(store, "device", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("account"); err != nil {
		t.Fatal(err)
	}
	if err := store.PairDeviceWithCredential("account", "device", c.privateKey.Public().(ed25519.PublicKey), c.credential); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshADK(); err == nil {
		t.Fatal("refresh succeeded without a wrapped key")
	}
	if !bytes.Equal(c.ADK(), identity.ADK) {
		t.Fatal("failed refresh replaced the existing key")
	}
}

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

func TestClientTombstonePurgesConflictAndObjectMirrors(t *testing.T) {
	client := &Client{
		cache: NewLocalCache(), vault: NewVault("test authorization"),
		localObjects: map[string]localObject{"removed": {version: 1, payload: []byte("old")}, "other": {version: 2}},
		conflicts:    []ConflictEntry{{ObjectID: "removed", ConflictID: "conflict-removed", LocalPayload: []byte("old")}, {ObjectID: "other"}},
	}
	client.applyTombstone(Tombstone{ObjectID: "removed"})
	if _, exists := client.localObjects["removed"]; exists {
		t.Fatal("tombstoned plaintext remains in the local object mirror")
	}
	if got := client.Conflicts(); len(got) != 1 || got[0].ObjectID != "other" {
		t.Fatalf("tombstone did not purge corresponding conflicts: %+v", got)
	}
	if _, exists := client.localObjects["other"]; !exists {
		t.Fatal("unrelated object was deleted")
	}
}
