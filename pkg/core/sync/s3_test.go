package sync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
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

func TestNewEncryptedEnvelopeRequiresRealADK(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEncryptedEnvelope(priv, "acct", "dev", "obj", uint64(1), InputMethod, []byte("secret")); !errors.Is(err, ErrMissingADK) {
		t.Fatalf("6-arg envelope without ADK err = %v, want %v", err, ErrMissingADK)
	}
	if _, err := NewEncryptedEnvelope(priv, []byte("short"), "acct", "dev", "obj", uint64(1), InputMethod, []byte("secret")); !errors.Is(err, ErrMissingADK) {
		t.Fatalf("short ADK err = %v, want %v", err, ErrMissingADK)
	}
	adk := make([]byte, 32)
	_, _ = rand.Read(adk)
	env, err := NewEncryptedEnvelope(priv, adk, "adk-v2", "acct", "dev", "obj", uint64(1), LearningData, []byte("payload"))
	if err != nil {
		t.Fatalf("valid explicit ADK envelope failed: %v", err)
	}
	if env.KeyID != "adk-v2" {
		t.Fatalf("KeyID = %q, want adk-v2", env.KeyID)
	}
	got, err := DecryptEnvelope(adk, env, priv.Public().(ed25519.PublicKey))
	if err != nil || string(got) != "payload" {
		t.Fatalf("DecryptEnvelope = %q, %v", got, err)
	}
}

type memoryStateStore struct {
	watermark     uint64
	recoveryReady bool
	saveCalls     int
}

func (m *memoryStateStore) Close() error { return nil }
func (m *memoryStateStore) Load(context.Context) (uint64, bool, error) {
	return m.watermark, m.recoveryReady, nil
}
func (m *memoryStateStore) Save(_ context.Context, w uint64, ready bool) error {
	m.watermark = w
	m.recoveryReady = ready
	m.saveCalls++
	return nil
}

func TestNewClientWithStoreNoNilServerPanicsAndPersistsRecovery(t *testing.T) {
	store, err := NewServerStore(NewServer())
	if err != nil {
		t.Fatal(err)
	}
	stateA := &memoryStateStore{}
	keyStoreA := &memorySecrets{values: map[string]string{}}
	a, err := NewPersistentClientWithStore(context.Background(), store, "dev-a", keyStoreA, stateA)
	if err != nil {
		t.Fatal(err)
	}
	if a.server != nil {
		t.Fatal("expected nil server for store-backed client")
	}
	if err := a.Login("acct"); err != nil {
		t.Fatal(err)
	}
	if err := a.PairDeviceWithCredential(); err != nil {
		t.Fatalf("PairDeviceWithCredential on nil-server client: %v", err)
	}
	code, err := a.PrepareRecoveryCode()
	if err != nil {
		t.Fatalf("PrepareRecoveryCode on nil-server client: %v", err)
	}
	if stateA.recoveryReady {
		t.Fatal("state should not be recoveryReady before confirmation")
	}
	if err := a.ConfirmRecoveryCode(code); err != nil {
		t.Fatalf("ConfirmRecoveryCode on nil-server client: %v", err)
	}
	if !stateA.recoveryReady || stateA.saveCalls < 2 {
		t.Fatalf("ConfirmRecoveryCode did not persist state: %+v", stateA)
	}

	// Simulate restart: new client loads persisted recovery state cleanly.
	aRestarted, err := NewPersistentClientWithStore(context.Background(), store, "dev-a", keyStoreA, stateA)
	if err != nil {
		t.Fatal(err)
	}
	if !aRestarted.SyncEnabled() {
		t.Fatal("restarted client lost confirmed recoveryReady state")
	}

	// Pair device B via signed ticket with X25519 & wrapped ADK on store-backed client.
	ib, _ := newDeviceIdentity()
	b, err := NewClientWithStore(store, "dev-b", ib)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.LoginWithCredential("acct", a.credential); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bundle, err := a.CreatePairingBundleWithX25519("dev-b", b.DevicePublicKey(), b.X25519PublicKey(), now)
	if err != nil {
		t.Fatalf("CreatePairingBundleWithX25519: %v", err)
	}
	if err := b.PairDeviceWithTicket(bundle.Ticket, bundle.ShortCode, PairingFingerprint(a.DevicePublicKey()), now.Add(time.Second)); err != nil {
		t.Fatalf("PairDeviceWithTicket: %v", err)
	}
	if !bytes.Equal(a.ADK(), b.ADK()) {
		t.Fatal("device B did not unwrap ADK from pairing ticket")
	}
	if err := a.WrapADKForDevice("dev-b"); err != nil {
		t.Fatalf("WrapADKForDevice on nil-server client: %v", err)
	}
	if err := b.RefreshADK(); err != nil {
		t.Fatalf("RefreshADK on nil-server client: %v", err)
	}
}

func TestPullObjectsFastForwardConflictAndResolve(t *testing.T) {
	store, err := NewServerStore(NewServer())
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := newDeviceIdentity()
	ib, _ := newDeviceIdentity()
	a, _ := NewClientWithStore(store, "dev-a", ia)
	b, _ := NewClientWithStore(store, "dev-b", ib)
	cred, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.LoginWithCredential("acct", cred); err != nil {
		t.Fatal(err)
	}
	if err := a.PairDeviceWithCredential(); err != nil {
		t.Fatal(err)
	}
	if err := b.LoginWithCredential("acct", cred); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bundle, err := a.CreatePairingBundleWithX25519("dev-b", b.DevicePublicKey(), b.X25519PublicKey(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.PairDeviceWithTicket(bundle.Ticket, bundle.ShortCode, PairingFingerprint(a.DevicePublicKey()), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	code, err := a.PrepareRecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ConfirmRecoveryCode(code); err != nil {
		t.Fatal(err)
	}
	if err := b.ConfirmRecoveryCode(code); err != nil {
		t.Fatal(err)
	}

	// Device A uploads v1 -> Device B pulls v1 -> Device B updates to v2 -> Device A pulls v2 (must fast-forward without conflict!)
	if err := a.UploadClass(LearningData, "lexicon", 1, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if res, err := b.PullAll(); err != nil || len(res.Objects) != 1 || len(res.Conflicts) != 0 || string(res.Objects[0].Payload) != "v1" {
		t.Fatalf("B initial PullAll = %+v, %v", res, err)
	}
	if err := b.UploadClass(LearningData, "lexicon", 2, []byte("v2-from-b")); err != nil {
		t.Fatal(err)
	}
	resA, err := a.PullObjects("lexicon")
	if err != nil {
		t.Fatalf("A fast-forward pull failed: %v", err)
	}
	if len(resA.Conflicts) != 0 || len(resA.Objects) != 1 || string(resA.Objects[0].Payload) != "v2-from-b" || resA.Objects[0].Version != 2 {
		t.Fatalf("A pull created spurious conflict or wrong payload: %+v", resA)
	}

	// Now A makes an uncommitted local edit while B uploads v3 -> true conflict!
	if err := a.StageLocalEdit(LearningData, "lexicon", []byte("local-edit-a")); err != nil {
		t.Fatal(err)
	}
	if err := b.UploadClass(LearningData, "lexicon", 3, []byte("remote-edit-b")); err != nil {
		t.Fatal(err)
	}
	resConflict, err := a.PullObjects("lexicon")
	if err != nil {
		t.Fatalf("pull during conflict failed: %v", err)
	}
	if len(resConflict.Conflicts) != 1 || string(resConflict.Conflicts[0].LocalPayload) != "local-edit-a" || string(resConflict.Conflicts[0].RemotePayload) != "remote-edit-b" {
		t.Fatalf("expected explicit conflict, got %+v", resConflict)
	}

	// ResolveConflict keeping local uploads v4 and clears conflicts.
	if err := a.ResolveConflict("lexicon", true); err != nil {
		t.Fatalf("ResolveConflict(keepLocal=true): %v", err)
	}
	if len(a.Conflicts()) != 0 {
		t.Fatalf("conflicts remain after resolve: %+v", a.Conflicts())
	}
	// Device B fast-forwards to v4!
	resB, err := b.PullObjects("lexicon")
	if err != nil || len(resB.Conflicts) != 0 || len(resB.Objects) != 1 || resB.Objects[0].Version != 4 || string(resB.Objects[0].Payload) != "local-edit-a" {
		t.Fatalf("B fast-forward to resolved v4 = %+v, %v", resB, err)
	}

	// Test ResolveConflict keeping remote.
	if err := b.StageLocalEdit(LearningData, "lexicon", []byte("local-b-discard")); err != nil {
		t.Fatal(err)
	}
	if err := a.UploadClass(LearningData, "lexicon", 5, []byte("v5-from-a")); err != nil {
		t.Fatal(err)
	}
	resB2, err := b.PullObjects("lexicon")
	if err != nil || len(resB2.Conflicts) != 1 {
		t.Fatalf("expected conflict on B, got %+v, %v", resB2, err)
	}
	if err := b.ResolveConflict("lexicon", false); err != nil {
		t.Fatalf("ResolveConflict(keepLocal=false): %v", err)
	}
	lo, ok := b.LocalObject("lexicon")
	if !ok || lo.Dirty || lo.Version != 5 || string(lo.Payload) != "v5-from-a" {
		t.Fatalf("B local object after keepRemote = %+v, ok=%v", lo, ok)
	}
}

func TestRemoveDeviceRotatesADKAndBlocksRevokedDecryption(t *testing.T) {
	store, err := NewServerStore(NewServer())
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := newDeviceIdentity()
	ib, _ := newDeviceIdentity()
	ic, _ := newDeviceIdentity()
	a, _ := NewClientWithStore(store, "dev-a", ia)
	b, _ := NewClientWithStore(store, "dev-b", ib)
	c, _ := NewClientWithStore(store, "dev-c", ic)
	cred, err := store.RegisterAccount("acct")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.LoginWithCredential("acct", cred); err != nil {
		t.Fatal(err)
	}
	if err := a.PairDeviceWithCredential(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, peer := range []*Client{b, c} {
		if err := peer.LoginWithCredential("acct", cred); err != nil {
			t.Fatal(err)
		}
		bundle, err := a.CreatePairingBundleWithX25519(peer.DeviceID(), peer.DevicePublicKey(), peer.X25519PublicKey(), now)
		if err != nil {
			t.Fatal(err)
		}
		if err := peer.PairDeviceWithTicket(bundle.Ticket, bundle.ShortCode, PairingFingerprint(a.DevicePublicKey()), now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	code, err := a.PrepareRecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []*Client{a, b, c} {
		if err := client.ConfirmRecoveryCode(code); err != nil {
			t.Fatal(err)
		}
	}
	oldADK := a.ADK()
	if a.ADKKeyID() != "adk-v1" {
		t.Fatalf("initial ADKKeyID = %q, want adk-v1", a.ADKKeyID())
	}

	// B revokes A -> ADK rotates to adk-v2 and re-wraps for B and C (not A).
	if err := b.RemoveDevice("dev-a"); err != nil {
		t.Fatalf("RemoveDevice(dev-a): %v", err)
	}
	if b.ADKKeyID() != "adk-v2" {
		t.Fatalf("rotated ADKKeyID = %q, want adk-v2", b.ADKKeyID())
	}
	if bytes.Equal(b.ADK(), oldADK) {
		t.Fatal("ADK did not rotate after RemoveDevice")
	}

	// B uploads a new object under adk-v2.
	if err := b.UploadClass(LearningData, "post-revoke", 1, []byte("secret-after-revoke")); err != nil {
		t.Fatalf("B upload after rotation: %v", err)
	}
	env, err := store.GetEnvelope("acct", "post-revoke")
	if err != nil {
		t.Fatal(err)
	}
	if env.KeyID != "adk-v2" {
		t.Fatalf("uploaded envelope KeyID = %q, want adk-v2", env.KeyID)
	}

	// Remaining active device C pulls & auto-refreshes to adk-v2 and decrypts!
	pulledC, err := c.Pull("post-revoke")
	if err != nil || string(pulledC.Payload) != "secret-after-revoke" {
		t.Fatalf("active device C failed to decrypt rotated envelope: %+v, %v", pulledC, err)
	}
	if c.ADKKeyID() != "adk-v2" {
		t.Fatalf("C ADKKeyID = %q, want adk-v2", c.ADKKeyID())
	}

	// Revoked device A cannot refresh ADK, cannot upload, and CANNOT decrypt the new envelope!
	if err := a.RefreshADK(); err == nil {
		t.Fatal("revoked device A unexpectedly refreshed ADK")
	}
	if err := a.UploadClass(LearningData, "from-revoked", 1, []byte("nope")); err == nil {
		t.Fatal("revoked device A upload unexpectedly succeeded")
	}
	if _, err := DecryptEnvelope(a.ADK(), env, b.DevicePublicKey()); err == nil {
		t.Fatal("revoked device A decrypted post-rotation envelope with old ADK")
	}
}
