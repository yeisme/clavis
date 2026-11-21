package sync

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

type memorySecrets struct{ values map[string]string }

func (m *memorySecrets) ReadKey(k string) (string, error) {
	v, ok := m.values[k]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}
func (m *memorySecrets) SaveKey(k, v string) error { m.values[k] = v; return nil }
func (m *memorySecrets) DeleteKey(k string) error {
	if _, ok := m.values[k]; !ok {
		return ErrSecretNotFound
	}
	delete(m.values, k)
	return nil
}

func TestLoadOrCreateDeviceIdentityReusesBase64Secrets(t *testing.T) {
	store := &memorySecrets{values: map[string]string{}}
	first, err := LoadOrCreateDeviceIdentity(store)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateDeviceIdentity(store)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatalf("fingerprint changed: %s != %s", first.Fingerprint(), second.Fingerprint())
	}
	for _, key := range []string{deviceEd25519Key, deviceX25519Key, accountCredentialKey, deviceADKKey} {
		value := store.values[key]
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(decoded) == 0 {
			t.Fatalf("%s is not base64: %v", key, err)
		}
	}
}

func (i DeviceIdentity) Fingerprint() string {
	pub := i.Ed25519Private.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}
