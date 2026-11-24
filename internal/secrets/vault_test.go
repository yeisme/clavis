package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeClipboard struct {
	mu     sync.Mutex
	value  string
	token  string
	writes int
}

func (c *fakeClipboard) Write(value string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = value
	c.writes++
	c.token = "ownership-" + value
	return c.token, nil
}
func (c *fakeClipboard) ClearIfOwned(token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if token != c.token {
		return ErrOwnershipLost
	}
	c.value = ""
	return nil
}

func (c *fakeClipboard) replace(value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = value
	c.token = "other-owner"
}

func TestSecretRefAndReceiptAreMetadataOnly(t *testing.T) {
	ref := NewSecretRef("ref-1", "account-1", "personal")
	receipt := CopyReceipt{RequestID: "req-1", State: CopyStateCopied, ErrorCode: CopyErrorAuditFailed, cleanupToken: "secret-must-not-escape"}
	for _, value := range []any{ref, receipt} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"value", "plaintext", "secret", "token", "clipboard_text"} {
			if _, present := fields[forbidden]; present {
				t.Fatalf("metadata JSON contains forbidden field %q: %s", forbidden, encoded)
			}
		}
	}
}

func TestVaultUserOnlyCopyTTLAndIdempotency(t *testing.T) {
	store := NewFakeSecretStore()
	clipboard := &fakeClipboard{}
	audit := &MemoryAuditSink{}
	vault := NewVault(NewLayeredSecretStore(store), clipboard, audit)
	ref, err := vault.Put(NewSecretRef("ref-1", "acct-1", "personal"), "top-secret")
	if err != nil {
		t.Fatal(err)
	}

	req := UserCopyRequest{RequestID: "req-1", SecretRefID: ref.ID, Actor: ActorUserSession, SessionID: "session-1", TTL: 10 * time.Millisecond}
	first, err := vault.Copy(req)
	if err != nil || first.State != CopyStateCopied {
		t.Fatalf("copy = %+v, %v", first, err)
	}
	second, err := vault.Copy(req)
	if err != nil || second != first {
		t.Fatalf("idempotent copy = %+v, %v, want %+v", second, err, first)
	}
	clipboard.mu.Lock()
	writes := clipboard.writes
	value := clipboard.value
	clipboard.mu.Unlock()
	if writes != 1 || value != "top-secret" {
		t.Fatalf("clipboard writes=%d value=%q", writes, value)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		clipboard.mu.Lock()
		current := clipboard.value
		clipboard.mu.Unlock()
		if current == "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	clipboard.mu.Lock()
	current := clipboard.value
	clipboard.mu.Unlock()
	if current != "" {
		t.Fatalf("TTL cleanup left clipboard value %q", current)
	}
	for time.Now().Before(deadline) && len(audit.Snapshot()) < 3 {
		time.Sleep(time.Millisecond)
	}
	if len(audit.Snapshot()) < 3 {
		t.Fatalf("audit events=%d, want store/copy/expired", len(audit.Snapshot()))
	}
}

func TestVaultConcurrentRetryWritesClipboardOnce(t *testing.T) {
	store := NewFakeSecretStore()
	clipboard := &fakeClipboard{}
	vault := NewVault(store, clipboard, nil)
	ref, err := vault.Put(NewSecretRef("ref-concurrent", "acct-concurrent", "personal"), "top-secret")
	if err != nil {
		t.Fatal(err)
	}
	req := UserCopyRequest{RequestID: "req-concurrent", SecretRefID: ref.ID, Actor: ActorUserSession, SessionID: "session-1", TTL: MaxSecretCopyTTL}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, copyErr := vault.Copy(req)
			if copyErr != nil || receipt.State != CopyStateCopied {
				t.Errorf("copy = %+v, %v", receipt, copyErr)
			}
		}()
	}
	wg.Wait()
	clipboard.mu.Lock()
	writes := clipboard.writes
	clipboard.mu.Unlock()
	if writes != 1 {
		t.Fatalf("concurrent retries wrote clipboard %d times", writes)
	}
}

func TestVaultAgentCannotCopyAndUserReplacementIsPreserved(t *testing.T) {
	store := NewFakeSecretStore()
	clipboard := &fakeClipboard{}
	audit := &MemoryAuditSink{}
	vault := NewVault(NewKeychainSecretStore(store), clipboard, audit)
	ref, err := vault.Put(NewSecretRef("ref-2", "acct-2", "personal"), "top-secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = vault.Copy(UserCopyRequest{RequestID: "agent-1", SecretRefID: ref.ID, Actor: ActorAgent, SessionID: "session-1"})
	if !errors.Is(err, ErrAgentForbidden) {
		t.Fatalf("agent copy error=%v, want agent_forbidden", err)
	}
	clipboard.mu.Lock()
	writes := clipboard.writes
	clipboard.mu.Unlock()
	if writes != 0 {
		t.Fatalf("agent copy wrote clipboard %d times", writes)
	}

	receipt, err := vault.Copy(UserCopyRequest{RequestID: "user-2", SecretRefID: ref.ID, Actor: ActorUserSession, SessionID: "session-1", TTL: MaxSecretCopyTTL})
	if err != nil {
		t.Fatal(err)
	}
	clipboard.replace("user's new content")
	if err := vault.Cleanup(receipt.RequestID, ref.ID, "session-1"); !errors.Is(err, ErrOwnershipLost) {
		t.Fatalf("cleanup error=%v, want ownership loss", err)
	}
	clipboard.mu.Lock()
	value := clipboard.value
	clipboard.mu.Unlock()
	if value != "user's new content" {
		t.Fatalf("replacement clipboard was cleared: %q", value)
	}
}

func TestVaultRejectsInvalidTTLAndForbiddenLabel(t *testing.T) {
	store := NewFakeSecretStore()
	clipboard := &fakeClipboard{}
	vault := NewVault(store, clipboard, nil)
	if _, err := vault.Put(NewSecretRef("ref-3", "acct-3", "password backup"), "secret"); err == nil {
		t.Fatal("secret-bearing label accepted")
	}
	ref, err := vault.Put(NewSecretRef("ref-3", "acct-3", "backup"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = vault.Copy(UserCopyRequest{RequestID: "bad-ttl", SecretRefID: ref.ID, Actor: ActorUserSession, SessionID: "s", TTL: MaxSecretCopyTTL + time.Second})
	if !errors.Is(err, ErrTTLInvalid) {
		t.Fatalf("TTL error=%v, want ttl_invalid", err)
	}
}

func TestVaultPutUsesDefaultsAndRejectsVersionConflict(t *testing.T) {
	vault := NewVault(NewFakeSecretStore(), &fakeClipboard{}, nil)
	ref, err := vault.Put(SecretRef{ID: "ref-version", RecordKey: "acct-version"}, "first")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Backend != SecretBackendLayered || ref.Kind != SecretKindPassword || ref.Sensitivity != SecretSensitivity || ref.Version != 1 {
		t.Fatalf("defaults = %+v", ref)
	}
	stale := ref
	if _, err := vault.Put(stale, "second"); err != nil {
		t.Fatalf("matching version update: %v", err)
	}
	if _, err := vault.Put(stale, "third"); err == nil || err.Error() != CopyErrorVersionConflict {
		t.Fatalf("stale version accepted: %v", err)
	}
}

type failingDeleteStore struct {
	*FakeSecretStore
	deleteErr error
}

func (s *failingDeleteStore) DeleteKey(account string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.FakeSecretStore.DeleteKey(account)
}

func TestVaultRevokeDeletesBackingSecretAndPropagatesStorageError(t *testing.T) {
	base := NewFakeSecretStore()
	store := &failingDeleteStore{FakeSecretStore: base}
	vault := NewVault(store, &fakeClipboard{}, &MemoryAuditSink{})

	ref, err := vault.Put(NewSecretRef("ref-del", "acct.del", "Primary DB"), "db-pass-1")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if v, err := base.ReadKey(ref.RecordKey); err != nil || v != "db-pass-1" {
		t.Fatalf("backing store missing secret before revoke: %q, %v", v, err)
	}

	// Non-not-found storage failure must propagate and keep ref active.
	store.deleteErr = errors.New("keychain is locked")
	if err := vault.Revoke(ref.ID); err == nil || err.Error() != "keychain is locked" {
		t.Fatalf("Revoke with storage error = %v, want keychain is locked", err)
	}
	if meta, err := vault.Metadata(ref.ID); err != nil || meta.State != SecretStateStored {
		t.Fatalf("failed Revoke mutated ref state: meta=%+v err=%v", meta, err)
	}

	// Once unlocked, Revoke deletes backing secret and marks ref revoked.
	store.deleteErr = nil
	if err := vault.Revoke(ref.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := base.ReadKey(ref.RecordKey); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("backing secret survived Revoke: err=%v", err)
	}
	if meta, err := vault.Metadata(ref.ID); err != nil || meta.State != SecretStateRevoked {
		t.Fatalf("post-revoke meta = %+v, %v", meta, err)
	}

	// ErrSecretNotFound during Revoke is treated as already deleted.
	ref2, err := vault.Put(NewSecretRef("ref-gone", "acct.gone", "Secondary DB"), "db-pass-2")
	if err != nil {
		t.Fatal(err)
	}
	store.deleteErr = ErrSecretNotFound
	if err := vault.Revoke(ref2.ID); err != nil {
		t.Fatalf("Revoke when backing secret already missing should succeed, got %v", err)
	}

	// Re-revoking an already-revoked ref must NOT delete a newer active ref reusing the same RecordKey.
	store.deleteErr = nil
	ref3, err := vault.Put(NewSecretRef("ref-reused", "acct.del", "New Primary DB"), "db-pass-3")
	if err != nil {
		t.Fatalf("Put reusing revoked RecordKey: %v", err)
	}
	if err := vault.Revoke(ref.ID); err != nil {
		t.Fatalf("idempotent second Revoke: %v", err)
	}
	if v, err := base.ReadKey(ref3.RecordKey); err != nil || v != "db-pass-3" {
		t.Fatalf("re-revoking old ref deleted new active ref secret: v=%q err=%v", v, err)
	}
}

func TestVaultPreventsDuplicateActiveRecordKeyAndDistinguishesInvalidInput(t *testing.T) {
	store := NewFakeSecretStore()
	vault := NewVault(store, &fakeClipboard{}, nil)

	ref1, err := vault.Put(NewSecretRef("ref-1", "clavis.prod.db", "Production DB"), "original-secret")
	if err != nil {
		t.Fatal(err)
	}

	// Different ref ID using the same active RecordKey must be rejected without clobbering secret.
	_, err = vault.Put(NewSecretRef("ref-2", "clavis.prod.db", "Duplicate DB"), "clobber-secret")
	if !errors.Is(err, ErrInvalidInput) || !errors.Is(err, ErrDuplicateRecordKey) {
		t.Fatalf("duplicate RecordKey error = %v, want ErrInvalidInput + ErrDuplicateRecordKey", err)
	}
	if errors.Is(err, ErrAgentForbidden) {
		t.Fatalf("duplicate RecordKey error must not match ErrAgentForbidden: %v", err)
	}
	if got, rerr := store.ReadKey("clavis.prod.db"); rerr != nil || got != "original-secret" {
		t.Fatalf("duplicate Put clobbered backing secret: got=%q err=%v", got, rerr)
	}

	// Invalid label and empty fields return ErrInvalidInput, never ErrAgentForbidden.
	for _, tc := range []struct {
		name string
		ref  SecretRef
		val  string
	}{
		{"forbidden-label", NewSecretRef("ref-lbl", "acct.lbl", "My Password"), "val"},
		{"empty-record-key", SecretRef{ID: "ref-nokey", RecordKey: ""}, "val"},
		{"empty-value", NewSecretRef("ref-noval", "acct.noval", "Service"), "   "},
	} {
		_, putErr := vault.Put(tc.ref, tc.val)
		if !errors.Is(putErr, ErrInvalidInput) {
			t.Fatalf("%s: err=%v, want ErrInvalidInput", tc.name, putErr)
		}
		if errors.Is(putErr, ErrAgentForbidden) {
			t.Fatalf("%s: err=%v must not match ErrAgentForbidden", tc.name, putErr)
		}
	}

	// Updating the same ref ID with matching version still works.
	if _, err := vault.Put(ref1, "updated-secret"); err != nil {
		t.Fatalf("same-ID update failed: %v", err)
	}
	if got, _ := store.ReadKey("clavis.prod.db"); got != "updated-secret" {
		t.Fatalf("same-ID update value = %q, want updated-secret", got)
	}
}

func TestVaultBoundedAuditAndReceipts(t *testing.T) {
	audit := &MemoryAuditSink{}
	for i := 0; i < DefaultMaxAuditEvents+125; i++ {
		if err := audit.Append(AuditEvent{
			At:        time.Unix(int64(i), 0).UTC(),
			Action:    "test",
			RequestID: fmt.Sprintf("req-%d", i),
			State:     CopyStateCopied,
		}); err != nil {
			t.Fatal(err)
		}
	}
	snap := audit.Snapshot()
	if len(snap) != DefaultMaxAuditEvents {
		t.Fatalf("audit len = %d, want %d", len(snap), DefaultMaxAuditEvents)
	}
	if snap[0].RequestID != "req-125" || snap[len(snap)-1].RequestID != fmt.Sprintf("req-%d", DefaultMaxAuditEvents+124) {
		t.Fatalf("audit ring kept wrong window: first=%q last=%q", snap[0].RequestID, snap[len(snap)-1].RequestID)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewFakeSecretStore()
	vault := NewVault(store, &fakeClipboard{}, audit)
	vault.now = func() time.Time { return now }
	ref, err := vault.Put(NewSecretRef("ref-cap", "acct.cap", "Cap Test"), "secret-cap")
	if err != nil {
		t.Fatal(err)
	}

	// Add one active unexpired receipt, then > DefaultMaxReceipts failed/expired receipts.
	activeReceipt, err := vault.Copy(UserCopyRequest{
		RequestID:   "req-active",
		SecretRefID: ref.ID,
		Actor:       ActorUserSession,
		SessionID:   "sess-1",
		TTL:         MaxSecretCopyTTL,
	})
	if err != nil || activeReceipt.State != CopyStateCopied {
		t.Fatalf("active copy = %+v, %v", activeReceipt, err)
	}
	for i := 0; i < DefaultMaxReceipts+50; i++ {
		now = now.Add(time.Millisecond)
		_, _ = vault.Copy(UserCopyRequest{
			RequestID:   fmt.Sprintf("req-fail-%d", i),
			SecretRefID: ref.ID,
			Actor:       ActorAgent,
			SessionID:   "sess-1",
		})
	}
	vSnap := vault.Snapshot()
	if len(vSnap.Receipts) > DefaultMaxReceipts {
		t.Fatalf("vault receipts len = %d, want <= %d", len(vSnap.Receipts), DefaultMaxReceipts)
	}
	if _, err := vault.Receipt("req-active", ref.ID, "sess-1"); err != nil {
		t.Fatalf("active unexpired receipt was pruned before expired/failed receipts: %v", err)
	}
	// Clean up timer for req-active so no timer lingers.
	_ = vault.Cleanup("req-active", ref.ID, "sess-1")
}
