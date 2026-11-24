package sync

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

type memorySecretStore struct {
	values map[string]string
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{values: map[string]string{}}
}
func (m *memorySecretStore) ReadKey(k string) (string, error) {
	v, ok := m.values[k]
	if !ok {
		return "", coresync.ErrSecretNotFound
	}
	return v, nil
}
func (m *memorySecretStore) SaveKey(k, v string) error {
	m.values[k] = v
	return nil
}
func (m *memorySecretStore) DeleteKey(k string) error {
	if _, ok := m.values[k]; !ok {
		return coresync.ErrSecretNotFound
	}
	delete(m.values, k)
	return nil
}

func newDirTestIdentity(t *testing.T) *coresync.DeviceIdentity {
	t.Helper()
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	xPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cred := make([]byte, 32)
	adk := make([]byte, 32)
	_, _ = rand.Read(cred)
	_, _ = rand.Read(adk)
	return &coresync.DeviceIdentity{
		Ed25519Private: edPriv,
		X25519Private:  xPriv,
		Credential:     cred,
		ADK:            adk,
	}
}

// TestSyncDirectoryTwoDevicesEndToEnd validates the complete multi-device
// shared-directory lifecycle:
//   - Device A & B (and C) share ONLY a temp sync dir (each has its own private SQLite DB)
//   - Device A pairs Device B & C via signed tickets with X25519 + wrapped ADK
//   - Device A uploads learning_data -> Device B pulls & decrypts
//   - Device B updates -> Device A fast-forwards cleanly (ZERO false conflict!)
//   - Concurrent edit creates explicit conflict -> ResolveConflict resolves & converges
//   - Permanent delete tombstone propagates & blocks resurrection
//   - RemoveDevice revokes Device B and rotates ADK (adk-v1 -> adk-v2):
//     active Device C decrypts new uploads, while revoked Device B CANNOT!
func TestSyncDirectoryTwoDevicesEndToEnd(t *testing.T) {
	ctx := context.Background()
	sharedDir := filepath.Join(t.TempDir(), "shared-sync-dir")
	privA := filepath.Join(t.TempDir(), "device-a-private")
	privB := filepath.Join(t.TempDir(), "device-b-private")
	privC := filepath.Join(t.TempDir(), "device-c-private")

	secA := newMemorySecretStore()
	secB := newMemorySecretStore()
	secC := newMemorySecretStore()

	txA, err := OpenDirTransport(ctx, DirTransportOptions{
		SyncDir:     sharedDir,
		PrivateDir:  privA,
		AccountID:   "acct-e2e",
		DeviceID:    "dev-a",
		SecretStore: secA,
	})
	if err != nil {
		t.Fatalf("open txA: %v", err)
	}
	defer txA.Close()

	txB, err := OpenDirTransport(ctx, DirTransportOptions{
		SyncDir:     sharedDir,
		PrivateDir:  privB,
		AccountID:   "acct-e2e",
		DeviceID:    "dev-b",
		SecretStore: secB,
	})
	if err != nil {
		t.Fatalf("open txB: %v", err)
	}
	defer txB.Close()

	txC, err := OpenDirTransport(ctx, DirTransportOptions{
		SyncDir:     sharedDir,
		PrivateDir:  privC,
		AccountID:   "acct-e2e",
		DeviceID:    "dev-c",
		SecretStore: secC,
	})
	if err != nil {
		t.Fatalf("open txC: %v", err)
	}
	defer txC.Close()

	// 1. Device A bootstraps the account.
	recoveryCode, err := txA.BootstrapAccount(ctx)
	if err != nil {
		t.Fatalf("BootstrapAccount: %v", err)
	}
	if recoveryCode == "" {
		t.Fatal("empty recovery code")
	}

	// 2. Device A pairs Device B and Device C via signed tickets.
	now := time.Now().UTC()
	bundleB, err := txA.CreatePairingBundle(ctx, "dev-b", txB.DevicePublicKey(), txB.X25519PublicKey(), now)
	if err != nil {
		t.Fatalf("CreatePairingBundle(dev-b): %v", err)
	}
	if err := txB.AcceptPairingTicket(ctx, bundleB.Ticket, bundleB.ShortCode, coresync.PairingFingerprint(txA.DevicePublicKey()), recoveryCode, now.Add(time.Second)); err != nil {
		t.Fatalf("AcceptPairingTicket(dev-b): %v", err)
	}
	bundleC, err := txA.CreatePairingBundle(ctx, "dev-c", txC.DevicePublicKey(), txC.X25519PublicKey(), now)
	if err != nil {
		t.Fatalf("CreatePairingBundle(dev-c): %v", err)
	}
	if err := txC.AcceptPairingTicket(ctx, bundleC.Ticket, bundleC.ShortCode, coresync.PairingFingerprint(txA.DevicePublicKey()), recoveryCode, now.Add(time.Second)); err != nil {
		t.Fatalf("AcceptPairingTicket(dev-c): %v", err)
	}

	// Verify no live SQLite/WAL files exist inside the shared directory!
	entries, err := os.ReadDir(sharedDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".db") || strings.HasSuffix(entry.Name(), "-wal") || strings.HasSuffix(entry.Name(), "-shm") {
			t.Fatalf("live SQLite file leaked into sharedDir: %s", entry.Name())
		}
	}

	// 3. Device A uploads learning_data -> immutable ciphertext file written -> Device B pulls & decrypts!
	if err := txA.Upload(ctx, coresync.LearningData, "user-lexicon", 1, []byte("v1-from-a")); err != nil {
		t.Fatalf("A upload v1: %v", err)
	}
	// Verify ciphertext file on disk does NOT contain plaintext!
	envFiles, err := filepath.Glob(filepath.Join(sharedDir, "envelopes", "learning_data", "user-lexicon.v1.*.json"))
	if err != nil || len(envFiles) != 1 {
		t.Fatalf("expected 1 immutable envelope file, got %v (err=%v)", envFiles, err)
	}
	rawDisk, err := os.ReadFile(envFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawDisk, []byte("v1-from-a")) {
		t.Fatal("plaintext leaked into shared directory envelope file")
	}

	repB, err := txB.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("B SyncOnce v1: %v", err)
	}
	if len(repB.Pulled) != 1 || repB.ConflictCount != 0 || string(repB.Pulled[0].Payload) != "v1-from-a" {
		t.Fatalf("B pull v1 = %+v", repB)
	}

	// 4. Device B updates to v2 -> Device A fast-forwards cleanly (NO false conflict!).
	if err := txB.Upload(ctx, coresync.LearningData, "user-lexicon", 2, []byte("v2-from-b")); err != nil {
		t.Fatalf("B upload v2: %v", err)
	}
	repA, err := txA.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("A SyncOnce v2: %v", err)
	}
	if repA.ConflictCount != 0 || len(repA.Conflicts) != 0 {
		t.Fatalf("A fast-forward produced false conflict: %+v", repA.Conflicts)
	}
	if len(repA.Pulled) != 1 || repA.Pulled[0].Version != 2 || string(repA.Pulled[0].Payload) != "v2-from-b" {
		t.Fatalf("A fast-forward pull = %+v", repA)
	}

	// 5. Concurrent edit creates explicit conflict -> ResolveConflict resolves & converges!
	if err := txA.StageLocalEdit(coresync.LearningData, "user-lexicon", []byte("v3-local-a")); err != nil {
		t.Fatalf("A StageLocalEdit: %v", err)
	}
	if err := txB.Upload(ctx, coresync.LearningData, "user-lexicon", 3, []byte("v3-remote-b")); err != nil {
		t.Fatalf("B upload v3: %v", err)
	}
	repConflict, err := txA.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("A SyncOnce during concurrent edit: %v", err)
	}
	if len(repConflict.Conflicts) != 1 || repConflict.ConflictCount != 1 {
		t.Fatalf("expected 1 explicit conflict, got %+v", repConflict)
	}
	if string(repConflict.Conflicts[0].LocalPayload) != "v3-local-a" || string(repConflict.Conflicts[0].RemotePayload) != "v3-remote-b" {
		t.Fatalf("conflict payloads mismatch: %+v", repConflict.Conflicts[0])
	}

	if err := txA.ResolveConflict(ctx, "user-lexicon", true); err != nil {
		t.Fatalf("A ResolveConflict: %v", err)
	}
	if len(txA.Client().Conflicts()) != 0 {
		t.Fatalf("conflicts remain on A: %+v", txA.Client().Conflicts())
	}
	// Device B fast-forwards to A's resolved v4 cleanly!
	repB2, err := txB.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("B SyncOnce v4: %v", err)
	}
	if repB2.ConflictCount != 0 || len(repB2.Pulled) != 1 || repB2.Pulled[0].Version != 4 || string(repB2.Pulled[0].Payload) != "v3-local-a" {
		t.Fatalf("B fast-forward to v4 = %+v", repB2)
	}

	// 6. Permanent tombstone propagation.
	if err := txA.Upload(ctx, coresync.InputMethod, "temp-profile", 1, []byte("delete-me")); err != nil {
		t.Fatalf("A upload temp-profile: %v", err)
	}
	if _, err := txC.SyncOnce(ctx); err != nil {
		t.Fatalf("C initial sync: %v", err)
	}
	if err := txA.Delete(ctx, "temp-profile"); err != nil {
		t.Fatalf("A delete temp-profile: %v", err)
	}
	tombFiles, err := filepath.Glob(filepath.Join(sharedDir, "tombstones", "*.json"))
	if err != nil || len(tombFiles) == 0 {
		t.Fatalf("expected tombstone file in sharedDir, got %v (err=%v)", tombFiles, err)
	}
	if repC, err := txC.SyncOnce(ctx); err != nil || repC.IngestedTombstones == 0 {
		t.Fatalf("C tombstone sync = %+v, %v", repC, err)
	}
	if _, exists := txC.Client().LocalObject("temp-profile"); exists {
		t.Fatal("deleted object still exists on Device C after tombstone sync")
	}

	// 7. RemoveDevice revokes Device B and rotates ADK to adk-v2!
	oldBADK := txB.Client().ADK()
	if err := txA.RemoveDevice(ctx, "dev-b"); err != nil {
		t.Fatalf("A RemoveDevice(dev-b): %v", err)
	}
	if txA.Client().ADKKeyID() != "adk-v2" {
		t.Fatalf("A ADKKeyID = %q, want adk-v2", txA.Client().ADKKeyID())
	}
	if err := txA.Upload(ctx, coresync.LearningData, "post-revoke-secret", 1, []byte("active-devices-only")); err != nil {
		t.Fatalf("A post-revoke upload: %v", err)
	}

	// Active Device C ingests revocation + wrapped adk-v2 and decrypts cleanly!
	repCAfter, err := txC.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("C SyncOnce after rotation: %v", err)
	}
	if txC.Client().ADKKeyID() != "adk-v2" {
		t.Fatalf("C ADKKeyID = %q, want adk-v2", txC.Client().ADKKeyID())
	}
	foundPostRevoke := false
	for _, obj := range repCAfter.Pulled {
		if obj.ObjectID == "post-revoke-secret" && string(obj.Payload) == "active-devices-only" {
			foundPostRevoke = true
		}
	}
	if !foundPostRevoke {
		t.Fatalf("Device C did not decrypt post-revoke object: %+v", repCAfter.Pulled)
	}

	// Revoked Device B CANNOT decrypt the newly uploaded adk-v2 envelope!
	postFiles, err := filepath.Glob(filepath.Join(sharedDir, "envelopes", "learning_data", "post-revoke-secret.v1.*.json"))
	if err != nil || len(postFiles) != 1 {
		t.Fatalf("post-revoke envelope file missing: %v, %v", postFiles, err)
	}
	rawPost, err := os.ReadFile(postFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	envPost, err := coresync.ParseEncryptedEnvelope(rawPost)
	if err != nil {
		t.Fatal(err)
	}
	if envPost.KeyID != "adk-v2" {
		t.Fatalf("post-revoke envelope KeyID = %q, want adk-v2", envPost.KeyID)
	}
	if _, err := coresync.DecryptEnvelope(oldBADK, envPost, txA.DevicePublicKey()); err == nil {
		t.Fatal("revoked Device B decrypted post-rotation adk-v2 envelope with old ADK")
	}
	if _, err := txB.SyncOnce(ctx); !errors.Is(err, coresync.ErrStoreDeviceRevoked) {
		t.Fatalf("revoked Device B SyncOnce err = %v, want %v", err, coresync.ErrStoreDeviceRevoked)
	}
}

func TestDirTransportEnforcesPhase1AllowlistAndSecurityBoundaries(t *testing.T) {
	ctx := context.Background()
	sharedDir := filepath.Join(t.TempDir(), "shared")
	privDir := filepath.Join(t.TempDir(), "private")

	tx, err := OpenDirTransport(ctx, DirTransportOptions{
		SyncDir:     sharedDir,
		PrivateDir:  privDir,
		AccountID:   "acct-policy",
		DeviceID:    "dev-a",
		SecretStore: newMemorySecretStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if _, err := tx.BootstrapAccount(ctx); err != nil {
		t.Fatal(err)
	}

	// Passwords and KeyManager are hard-rejected.
	if err := tx.Upload(ctx, coresync.Passwords, "pw1", 1, []byte("nope")); !errors.Is(err, coresync.ErrClassForbidden) {
		t.Fatalf("passwords upload err = %v, want %v", err, coresync.ErrClassForbidden)
	}
	if err := tx.Upload(ctx, coresync.KeyManager, "km1", 1, []byte("nope")); !errors.Is(err, coresync.ErrClassForbidden) {
		t.Fatalf("key_manager upload err = %v, want %v", err, coresync.ErrClassForbidden)
	}
	// Non-Phase1 classes (clipboard_text, media, sensitive_vault) are rejected by default Phase 1 allowlist.
	for _, disallowed := range []coresync.DataClass{coresync.ClipboardText, coresync.Media, coresync.ApplicationCtx, coresync.SensitiveVault} {
		if err := tx.Upload(ctx, disallowed, "obj", 1, []byte("nope")); !errors.Is(err, coresync.ErrClassNotAllowed) {
			t.Fatalf("class %s upload err = %v, want %v", disallowed, err, coresync.ErrClassNotAllowed)
		}
	}
	// Path traversal in object ID is rejected.
	if err := tx.Upload(ctx, coresync.LearningData, "../escape", 1, []byte("nope")); err == nil {
		t.Fatal("path traversal objectID accepted")
	}
	// Allowed classes succeed.
	if err := tx.Upload(ctx, coresync.InputMethod, "ime-base", 1, []byte("ok-ime")); err != nil {
		t.Fatalf("input_method upload failed: %v", err)
	}
	if err := tx.Upload(ctx, coresync.LearningData, "learn-base", 1, []byte("ok-learn")); err != nil {
		t.Fatalf("learning_data upload failed: %v", err)
	}

	// Symlink inside envelopes dir is rejected during SyncOnce.
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(sharedDir, "envelopes", "learning_data", "symlink.v1.deadbeef.json")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.SyncOnce(ctx); err == nil {
		t.Fatal("symlink envelope file accepted")
	}
	_ = os.Remove(linkPath)

	// Crash-safe restart: close and reopen on same PrivateDir & SecretStore.
	sec := newMemorySecretStore()
	shared2 := filepath.Join(t.TempDir(), "shared2")
	priv2 := filepath.Join(t.TempDir(), "priv2")
	tx1, err := OpenDirTransport(ctx, DirTransportOptions{SyncDir: shared2, PrivateDir: priv2, AccountID: "acct-r", DeviceID: "dev-r", SecretStore: sec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx1.BootstrapAccount(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Upload(ctx, coresync.LearningData, "persisted", 1, []byte("survives-restart")); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Close(); err != nil {
		t.Fatal(err)
	}
	tx2, err := OpenDirTransport(ctx, DirTransportOptions{SyncDir: shared2, PrivateDir: priv2, AccountID: "acct-r", DeviceID: "dev-r", SecretStore: sec})
	if err != nil {
		t.Fatalf("reopen DirTransport: %v", err)
	}
	defer tx2.Close()
	if !tx2.Client().SyncEnabled() {
		t.Fatal("reopened DirTransport lost recoveryReady/SyncEnabled state")
	}
	if _, err := tx2.SyncOnce(ctx); err != nil {
		t.Fatalf("reopened SyncOnce: %v", err)
	}
	if err := tx2.Upload(ctx, coresync.LearningData, "persisted", 2, []byte("after-restart")); err != nil {
		t.Fatalf("upload after restart: %v", err)
	}
}
