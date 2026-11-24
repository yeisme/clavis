package secrets

import (
	"encoding/json"
	"errors"
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
