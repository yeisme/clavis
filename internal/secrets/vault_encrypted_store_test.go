package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestEncryptedVaultStoreRoundTripEncryptionAndIsolation(t *testing.T) {
	dir := t.TempDir()
	store := NewEncryptedVaultStore(dir, nil)

	// Fresh absent store returns ErrSecretNotFound and touches nothing on disk.
	if _, err := store.ReadKey("clavis.prod.db"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("fresh ReadKey err = %v, want ErrSecretNotFound", err)
	}
	if _, err := os.Stat(store.DataPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh ReadKey should not create %s", store.DataPath())
	}
	if _, err := os.Stat(store.KeyPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh ReadKey should not create %s", store.KeyPath())
	}

	secretVal := "  super-secret-vault-value-with-spaces  "
	if err := store.SaveKey("clavis.prod.db", secretVal); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}
	got, err := store.ReadKey("clavis.prod.db")
	if err != nil || got != secretVal {
		t.Fatalf("ReadKey = %q, %v; want %q", got, err, secretVal)
	}

	// Verify ai-keys.toml was never touched.
	if _, err := os.Stat(filepath.Join(dir, "ai-keys.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("EncryptedVaultStore must never create plaintext ai-keys.toml")
	}

	// Verify file permissions and encrypted envelope contents.
	encInfo, err := os.Stat(store.DataPath())
	if err != nil {
		t.Fatalf("stat %s: %v", store.DataPath(), err)
	}
	keyInfo, err := os.Stat(store.KeyPath())
	if err != nil {
		t.Fatalf("stat %s: %v", store.KeyPath(), err)
	}
	if runtime.GOOS != "windows" {
		if perm := encInfo.Mode().Perm(); perm != 0o600 {
			t.Fatalf("vault_secrets.enc mode = %04o, want 0600", perm)
		}
		if perm := keyInfo.Mode().Perm(); perm != 0o600 {
			t.Fatalf("vault.key mode = %04o, want 0600", perm)
		}
	}

	encRaw, err := os.ReadFile(store.DataPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encRaw), "super-secret-vault-value") || strings.Contains(string(encRaw), "clavis.prod.db") {
		t.Fatalf("vault_secrets.enc leaked plaintext secret or account: %s", string(encRaw))
	}
	var env1 encryptedVaultEnvelope
	if err := json.Unmarshal(encRaw, &env1); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env1.Version != encryptedVaultVersion || env1.Algorithm != encryptedVaultAlgorithm {
		t.Fatalf("unexpected envelope header: %+v", env1)
	}

	keyRaw, err := os.ReadFile(store.KeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(keyRaw), "super-secret-vault-value") {
		t.Fatal("vault.key must never contain plaintext secrets")
	}

	// Writing again uses a fresh random nonce.
	if err := store.SaveKey("clavis.prod.db", secretVal); err != nil {
		t.Fatal(err)
	}
	encRaw2, err := os.ReadFile(store.DataPath())
	if err != nil {
		t.Fatal(err)
	}
	var env2 encryptedVaultEnvelope
	if err := json.Unmarshal(encRaw2, &env2); err != nil {
		t.Fatal(err)
	}
	if env1.Nonce == env2.Nonce || env1.Ciphertext == env2.Ciphertext {
		t.Fatal("expected fresh AES-GCM nonce and ciphertext on each write")
	}

	// Reload in a brand-new instance from disk.
	reloaded := NewEncryptedVaultStore(dir, nil)
	got, err = reloaded.ReadKey("clavis.prod.db")
	if err != nil || got != secretVal {
		t.Fatalf("reloaded ReadKey = %q, %v; want %q", got, err, secretVal)
	}

	// Delete removes the secret persistently.
	if err := reloaded.DeleteKey("clavis.prod.db"); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}
	reloaded2 := NewEncryptedVaultStore(dir, nil)
	if _, err := reloaded2.ReadKey("clavis.prod.db"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("ReadKey after delete = %v, want ErrSecretNotFound", err)
	}
}

func TestEncryptedVaultStoreDetectsTamperingAndBadPermissions(t *testing.T) {
	dir := t.TempDir()
	store := NewEncryptedVaultStore(dir, nil)
	if err := store.SaveKey("acct.tamper", "sensitive-value"); err != nil {
		t.Fatal(err)
	}

	// Tamper with ciphertext.
	raw, err := os.ReadFile(store.DataPath())
	if err != nil {
		t.Fatal(err)
	}
	var env encryptedVaultEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Ciphertext[0] == 'A' {
		env.Ciphertext = "B" + env.Ciphertext[1:]
	} else {
		env.Ciphertext = "A" + env.Ciphertext[1:]
	}
	tampered, _ := json.Marshal(env)
	if err := os.WriteFile(store.DataPath(), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEncryptedVaultStore(dir, nil).ReadKey("acct.tamper"); err == nil || errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected GCM decryption error on tampered file, got %v", err)
	}

	if runtime.GOOS != "windows" {
		// Restore valid file then relax permissions to 0644.
		if err := os.WriteFile(store.DataPath(), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.DataPath(), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewEncryptedVaultStore(dir, nil).ReadKey("acct.tamper"); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("expected 0600 mode error on vault_secrets.enc, got %v", err)
		}
		_ = os.Chmod(store.DataPath(), 0o600)
		_ = os.Chmod(store.KeyPath(), 0o644)
		if _, err := NewEncryptedVaultStore(dir, nil).ReadKey("acct.tamper"); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("expected 0600 mode error on vault.key, got %v", err)
		}
	}
}

type failingKeychainStore struct {
	APIKeyStore
	err error
}

func (f *failingKeychainStore) ReadKey(string) (string, error) { return "", f.err }
func (f *failingKeychainStore) SaveKey(string, string) error   { return f.err }

func TestEncryptedVaultStoreKeychainPrimaryAndFileFallback(t *testing.T) {
	// 1) Working Keychain stores master key in Keychain without creating vault.key.
	dir1 := t.TempDir()
	kc := NewMemoryAPIKeyStore()
	s1 := NewEncryptedVaultStore(dir1, kc)
	if err := s1.SaveKey("acct.kc", "kc-protected-secret"); err != nil {
		t.Fatalf("SaveKey with keychain: %v", err)
	}
	if _, err := os.Stat(s1.KeyPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("vault.key should not be written when keychain SaveKey succeeded")
	}
	if _, err := kc.ReadKey(encryptedVaultKeyAccount); err != nil {
		t.Fatalf("expected master key stored in keychain: %v", err)
	}
	s1Reload := NewEncryptedVaultStore(dir1, kc)
	if got, err := s1Reload.ReadKey("acct.kc"); err != nil || got != "kc-protected-secret" {
		t.Fatalf("keychain reload = %q, %v", got, err)
	}

	// 2) Failing Keychain falls back seamlessly to 0600 vault.key.
	dir2 := t.TempDir()
	brokenKC := &failingKeychainStore{APIKeyStore: NewMemoryAPIKeyStore(), err: errors.New("keychain unavailable")}
	s2 := NewEncryptedVaultStore(dir2, brokenKC)
	if err := s2.SaveKey("acct.fb", "fallback-protected-secret"); err != nil {
		t.Fatalf("SaveKey with failing keychain: %v", err)
	}
	if _, err := os.Stat(s2.KeyPath()); err != nil {
		t.Fatalf("expected fallback vault.key to exist: %v", err)
	}
	s2Reload := NewEncryptedVaultStore(dir2, brokenKC)
	if got, err := s2Reload.ReadKey("acct.fb"); err != nil || got != "fallback-protected-secret" {
		t.Fatalf("fallback reload = %q, %v", got, err)
	}
}

func TestEncryptedVaultStoreConcurrentRace(t *testing.T) {
	dir := t.TempDir()
	store := NewEncryptedVaultStore(dir, nil)
	if err := store.SaveKey("seed", "keep-me"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			acct := fmt.Sprintf("vault.acct.%d", id%3)
			for i := 0; i < 15; i++ {
				switch (id + i) % 3 {
				case 0:
					if err := store.SaveKey(acct, fmt.Sprintf("secret-%d-%d", id, i)); err != nil {
						t.Errorf("SaveKey: %v", err)
					}
				case 1:
					if _, err := store.ReadKey(acct); err != nil && !errors.Is(err, ErrSecretNotFound) {
						t.Errorf("ReadKey: %v", err)
					}
				case 2:
					if err := store.DeleteKey(acct); err != nil {
						t.Errorf("DeleteKey: %v", err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if got, err := NewEncryptedVaultStore(dir, nil).ReadKey("seed"); err != nil || got != "keep-me" {
		t.Fatalf("post-race seed = %q, %v", got, err)
	}
}
