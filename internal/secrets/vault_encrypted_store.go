package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	encryptedVaultFileName   = "vault_secrets.enc"
	encryptedVaultKeyName    = "vault.key"
	encryptedVaultKeyAccount = "clavis.vault.master_key"
	encryptedVaultVersion    = 1
	encryptedVaultAlgorithm  = "aes-256-gcm"
	encryptedVaultAAD        = "clavis-vault-v1"
	vaultMasterKeyBytes      = 32
)

type encryptedVaultEnvelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// EncryptedVaultStore stores Vault secret values encrypted at rest with AES-256-GCM
// in <dir>/vault_secrets.enc (0600, atomic temp+rename), completely isolated from
// plaintext ai-keys.toml.
//
// Its 32-byte master key is loaded from / saved to an explicit Keychain APIKeyStore
// when supplied and available, seamlessly falling back to a dedicated 0600
// <dir>/vault.key file so headless tests and locally rebuilt macOS binaries never
// break or trigger SecurityAgent authorization prompts.
type EncryptedVaultStore struct {
	dir      string
	keychain APIKeyStore

	mu     sync.Mutex
	cache  map[string]string
	loaded bool
	key    []byte
}

var _ SecretValueStore = (*EncryptedVaultStore)(nil)

// NewEncryptedVaultStore creates an AES-256-GCM encrypted store under dir.
// If keychain is non-nil, the 32-byte master key is stored/loaded there first,
// with automatic fallback to <dir>/vault.key (0600).
func NewEncryptedVaultStore(dir string, keychain APIKeyStore) *EncryptedVaultStore {
	return &EncryptedVaultStore{
		dir:      strings.TrimSpace(dir),
		keychain: keychain,
	}
}

// DataPath returns the encrypted vault data file path (<dir>/vault_secrets.enc).
func (s *EncryptedVaultStore) DataPath() string {
	return filepath.Join(s.dir, encryptedVaultFileName)
}

// KeyPath returns the fallback master key file path (<dir>/vault.key).
func (s *EncryptedVaultStore) KeyPath() string {
	return filepath.Join(s.dir, encryptedVaultKeyName)
}

func (s *EncryptedVaultStore) ReadKey(account string) (string, error) {
	acct, err := validateAccount(account)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return "", err
	}
	if v, ok := s.cache[acct]; ok && v != "" {
		return v, nil
	}
	return "", ErrSecretNotFound
}

func (s *EncryptedVaultStore) SaveKey(account, value string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: value is required", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	key, err := s.ensureKeyLocked()
	if err != nil {
		return err
	}
	if s.cache == nil {
		s.cache = make(map[string]string)
	}
	prevVal, existed := s.cache[acct]
	s.cache[acct] = value
	if err := s.flushLocked(key); err != nil {
		if existed {
			s.cache[acct] = prevVal
		} else {
			delete(s.cache, acct)
		}
		return err
	}
	return nil
}

func (s *EncryptedVaultStore) DeleteKey(account string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	prevVal, existed := s.cache[acct]
	if !existed {
		return nil
	}
	key, err := s.ensureKeyLocked()
	if err != nil {
		return err
	}
	delete(s.cache, acct)
	if err := s.flushLocked(key); err != nil {
		s.cache[acct] = prevVal
		return err
	}
	return nil
}

func (s *EncryptedVaultStore) loadLocked() error {
	if s.loaded {
		return nil
	}
	if s.dir == "" {
		return errors.New("secrets: encrypted vault directory is empty")
	}
	dataPath := s.DataPath()
	info, err := os.Stat(dataPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.cache = make(map[string]string)
			s.loaded = true
			return nil
		}
		return err
	}
	if err := checkStrictFileMode(dataPath, info.Mode().Perm()); err != nil {
		return err
	}
	key, err := s.loadExistingKeyLocked()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		return err
	}
	var env encryptedVaultEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("secrets: parse %s: %w", dataPath, err)
	}
	if env.Version != encryptedVaultVersion || env.Algorithm != encryptedVaultAlgorithm {
		return fmt.Errorf("secrets: unsupported encrypted vault format (version=%d algorithm=%q)", env.Version, env.Algorithm)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return fmt.Errorf("secrets: decode vault nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return fmt.Errorf("secrets: decode vault ciphertext: %w", err)
	}
	gcm, err := newVaultGCM(key)
	if err != nil {
		return err
	}
	if len(nonce) != gcm.NonceSize() {
		return fmt.Errorf("secrets: invalid vault nonce size %d", len(nonce))
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(encryptedVaultAAD))
	if err != nil {
		return fmt.Errorf("secrets: decrypt %s: %w", dataPath, err)
	}
	defer clear(plaintext)

	decoded := make(map[string]string)
	if len(plaintext) > 0 {
		if err := json.Unmarshal(plaintext, &decoded); err != nil {
			return fmt.Errorf("secrets: decode vault payload: %w", err)
		}
	}
	s.cache = decoded
	s.loaded = true
	return nil
}

func (s *EncryptedVaultStore) loadExistingKeyLocked() ([]byte, error) {
	if len(s.key) == vaultMasterKeyBytes {
		return s.key, nil
	}
	var kcErr error
	if s.keychain != nil {
		rawHex, err := s.keychain.ReadKey(encryptedVaultKeyAccount)
		if err == nil {
			k, derr := decodeMasterKeyHex(rawHex)
			if derr != nil {
				return nil, fmt.Errorf("secrets: invalid keychain vault master key: %w", derr)
			}
			s.key = k
			return s.key, nil
		}
		if !errors.Is(err, ErrSecretNotFound) {
			kcErr = err
		}
	}
	keyPath := s.KeyPath()
	info, err := os.Stat(keyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if kcErr != nil {
				return nil, fmt.Errorf("secrets: vault master key unavailable: %w", kcErr)
			}
			return nil, errors.New("secrets: vault master key missing")
		}
		return nil, err
	}
	if err := checkStrictFileMode(keyPath, info.Mode().Perm()); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	k, err := decodeMasterKeyHex(string(raw))
	if err != nil {
		return nil, fmt.Errorf("secrets: parse %s: %w", keyPath, err)
	}
	s.key = k
	return s.key, nil
}

func (s *EncryptedVaultStore) ensureKeyLocked() ([]byte, error) {
	if k, err := s.loadExistingKeyLocked(); err == nil {
		return k, nil
	} else {
		// If a key file already exists but is malformed or has bad permissions, fail loudly
		// instead of overwriting an existing key.
		if _, statErr := os.Stat(s.KeyPath()); statErr == nil {
			return nil, err
		}
		// If vault_secrets.enc already exists on disk, never silently rotate the key and
		// make existing entries undecryptable.
		if _, statErr := os.Stat(s.DataPath()); statErr == nil {
			return nil, err
		}
	}

	key := make([]byte, vaultMasterKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("secrets: generate vault master key: %w", err)
	}
	encoded := hex.EncodeToString(key)

	if s.keychain != nil {
		if err := s.keychain.SaveKey(encryptedVaultKeyAccount, encoded); err == nil {
			s.key = key
			return s.key, nil
		}
	}
	if err := writeAtomicSecretFile(s.KeyPath(), []byte(encoded+"\n"), ".vault-key-*.tmp"); err != nil {
		clear(key)
		return nil, err
	}
	s.key = key
	return s.key, nil
}

func (s *EncryptedVaultStore) flushLocked(key []byte) error {
	if s.dir == "" {
		return errors.New("secrets: encrypted vault directory is empty")
	}
	plaintext, err := json.Marshal(s.cache)
	if err != nil {
		return err
	}
	defer clear(plaintext)

	gcm, err := newVaultGCM(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("secrets: generate vault nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(encryptedVaultAAD))
	env := encryptedVaultEnvelope{
		Version:    encryptedVaultVersion,
		Algorithm:  encryptedVaultAlgorithm,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writeAtomicSecretFile(s.DataPath(), encoded, ".vault-secrets-*.tmp")
}

func newVaultGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != vaultMasterKeyBytes {
		return nil, fmt.Errorf("secrets: invalid vault key size %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decodeMasterKeyHex(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("empty key")
	}
	key, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, err
	}
	if len(key) != vaultMasterKeyBytes {
		return nil, fmt.Errorf("got %d bytes, want %d", len(key), vaultMasterKeyBytes)
	}
	return key, nil
}

func checkStrictFileMode(path string, perm os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if perm&0o077 != 0 {
		return fmt.Errorf("secrets: %s is mode %04o, want 0600 (run: chmod 600 %s)", path, perm, path)
	}
	return nil
}

func writeAtomicSecretFile(path string, data []byte, tmpPattern string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(keysFileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
