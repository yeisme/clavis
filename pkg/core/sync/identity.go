package sync

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	deviceEd25519Key     = "clavis.sync.device.ed25519"
	deviceX25519Key      = "clavis.sync.device.x25519"
	accountCredentialKey = "clavis.sync.account.credential"
	deviceADKKey         = "clavis.sync.adk.v1"
)

// SecretStore is the narrow keystore contract needed by the sync core. The
// concrete internal/secrets stores satisfy it without entering pkg/core's
// dependency graph.
type SecretStore interface {
	ReadKey(account string) (string, error)
	SaveKey(account string, value string) error
	DeleteKey(account string) error
}

var ErrSecretNotFound = errors.New("sync: secret not found")

// DeviceIdentity contains only key material. It is intended to be loaded from
// the OS keystore; it must not be serialized to configuration or session JSON.
type DeviceIdentity struct {
	Ed25519Private ed25519.PrivateKey
	X25519Private  *ecdh.PrivateKey
	Credential     []byte
	ADK            []byte
}

func newDeviceIdentity() (DeviceIdentity, error) {
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return DeviceIdentity{}, err
	}
	x, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return DeviceIdentity{}, err
	}
	credential := make([]byte, 32)
	adk := make([]byte, 32)
	if _, err := rand.Read(credential); err != nil {
		return DeviceIdentity{}, err
	}
	if _, err := rand.Read(adk); err != nil {
		return DeviceIdentity{}, err
	}
	return DeviceIdentity{Ed25519Private: ed, X25519Private: x, Credential: credential, ADK: adk}, nil
}

func encodeSecret(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
func decodeSecret(name, value string, size int) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != size {
		return nil, fmt.Errorf("sync: invalid %s secret", name)
	}
	return decoded, nil
}

// LoadOrCreateDeviceIdentity reuses all key material when all keystore entries
// are present. A partial or unreadable set is treated as first-run state and is
// replaced as one unit, preventing half-new identities.
func LoadOrCreateDeviceIdentity(store SecretStore) (DeviceIdentity, error) {
	if store == nil {
		return DeviceIdentity{}, errors.New("sync: identity keystore is required")
	}
	values := make([]string, 4)
	keys := []string{deviceEd25519Key, deviceX25519Key, accountCredentialKey, deviceADKKey}
	complete := true
	for i, key := range keys {
		value, err := store.ReadKey(key)
		if err != nil {
			complete = false
			break
		}
		values[i] = value
	}
	if complete {
		ed, err := decodeSecret(deviceEd25519Key, values[0], ed25519.PrivateKeySize)
		if err == nil {
			x, xerr := decodeSecret(deviceX25519Key, values[1], 32)
			cred, cerr := decodeSecret(accountCredentialKey, values[2], 32)
			adk, aerr := decodeSecret(deviceADKKey, values[3], 32)
			if xerr == nil && cerr == nil && aerr == nil {
				xpriv, xerr := ecdh.X25519().NewPrivateKey(x)
				if xerr == nil {
					return DeviceIdentity{Ed25519Private: ed25519.PrivateKey(ed), X25519Private: xpriv, Credential: cred, ADK: adk}, nil
				}
			}
		}
	}
	identity, err := newDeviceIdentity()
	if err != nil {
		return DeviceIdentity{}, err
	}
	entries := map[string][]byte{
		deviceEd25519Key:     identity.Ed25519Private,
		deviceX25519Key:      identity.X25519Private.Bytes(),
		accountCredentialKey: identity.Credential,
		deviceADKKey:         identity.ADK,
	}
	for _, key := range keys {
		if err := store.SaveKey(key, encodeSecret(entries[key])); err != nil {
			return DeviceIdentity{}, err
		}
	}
	return identity, nil
}

// DeleteDeviceIdentity removes all local identity material after a deliberate
// server-side device revocation.
func DeleteDeviceIdentity(store SecretStore) error {
	if store == nil {
		return errors.New("sync: identity keystore is required")
	}
	for _, key := range []string{deviceEd25519Key, deviceX25519Key, accountCredentialKey, deviceADKKey} {
		if err := store.DeleteKey(key); err != nil && !errors.Is(err, ErrSecretNotFound) {
			return err
		}
	}
	return nil
}
