package sync

import (
	"crypto/ed25519"
	"errors"
)

// ObjectSummary describes an encrypted sync object stored for an account
// without exposing any plaintext payload.
type ObjectSummary struct {
	ObjectID string    `json:"objectId"`
	Version  uint64    `json:"version"`
	Class    DataClass `json:"class"`
	DeviceID string    `json:"deviceId"`
	KeyID    string    `json:"keyId"`
}

// WrappedADKEntry pairs a device's wrapped ADK ciphertext with the ADK version
// key identifier (for example "adk-v1", "adk-v2").
type WrappedADKEntry struct {
	KeyID   string `json:"keyId"`
	Wrapped []byte `json:"wrapped"`
}

// SyncStore is the durable boundary for the sync protocol. Implementations
// store only encrypted envelopes; plaintext payloads are not part of this API.
// Methods which mutate protocol state are atomic and preserve the same cursor,
// tombstone, signature, and version invariants as Server.
type SyncStore interface {
	RegisterAccount(account string) ([]byte, error)
	LoginWithCredential(account string, credential []byte) error
	PairDeviceWithCredential(account, device string, pub ed25519.PublicKey, credential []byte) error
	DevicePublicKey(account, device string) (ed25519.PublicKey, error)
	UploadEnvelope(account, device, object string, version uint64, envelope EncryptedEnvelope) error
	GetEnvelope(account, object string) (EncryptedEnvelope, error)
	ListObjects(account string) ([]ObjectSummary, error)
	ObjectVersion(account, object string) (uint64, error)
	SubmitDelete(instruction DeleteInstruction) (Tombstone, error)
	SyncTombstones(account, device string, after uint64) ([]Tombstone, error)
	ConfirmTombstones(account, device string, sequence uint64) error
	VerifyTombstone(tombstone Tombstone) bool
	Watermark(account, device string) (uint64, error)
	IssuedBatch(account, device string) (TombstoneBatch, bool, error)
	AuditForAccount(account string) []AuditEvent
	LatestSequence(account string) (uint64, error)
	Close() error
}

// ADKStore persists per-device X25519 public keys, wrapped ADK entries, and
// active device membership for ADK distribution and rotation on revocation.
type ADKStore interface {
	SetX25519PublicKey(account, device string, pub, credential []byte) error
	X25519PublicKey(account, device string) ([]byte, error)
	SetWrappedADKForDevice(account, sender, target string, wrapped, signature []byte) error
	GetWrappedADK(account, device string, signature []byte) ([]byte, error)
	SetWrappedADKEntryForDevice(account, sender, target, keyID string, wrapped, signature []byte) error
	GetWrappedADKEntry(account, device string, signature []byte) (WrappedADKEntry, error)
	ListActiveDevices(account string) ([]string, error)
}

// RecoveryStore persists per-account recovery salt and recovery-wrapped ADK
// ciphertext. Neither plaintext ADKs nor recovery codes enter this store.
type RecoveryStore interface {
	GetRecoverySalt(account, device string, signature []byte) ([]byte, error)
	SetRecoveryWrap(account, device string, wrapped, signature []byte) error
	GetRecoveryWrap(account, device string, signature []byte) ([]byte, error)
}

// PairingStore combines signed ticket pairing and device revocation.
type PairingStore interface {
	PairingTicketStore
	PairingRevocationStore
}

// TombstoneBatch is the exact contiguous batch issued to a device. A batch is
// consumed on confirmation and cannot be forged by supplying a cursor.
type TombstoneBatch struct {
	From uint64
	To   uint64
}

var (
	ErrStoreInvalidArgument = errors.New("sync: invalid store argument")
	ErrStoreAccountExists   = errors.New("sync: account already exists")
	ErrStoreAccountMissing  = errors.New("sync: account not found")
	ErrStoreCredential      = errors.New("sync: invalid account credential")
	ErrStoreDeviceMissing   = errors.New("sync: device is not paired")
	ErrStoreDeviceKey       = errors.New("sync: device key cannot be overwritten")
	ErrStoreObjectMissing   = errors.New("sync: object not found")
	ErrStoreDeleted         = errors.New("sync: object is permanently deleted")
	ErrStoreVersion         = errors.New("sync: upload version is not newer")
	ErrStoreBatch           = errors.New("sync: tombstone batch is not confirmed")
	ErrStoreCursor          = errors.New("sync: invalid tombstone cursor")
	ErrStorePlaintext       = errors.New("sync: plaintext payloads are not accepted")
	ErrStoreConflict        = errors.New("sync: concurrent object conflict")
	ErrStoreDeviceRevoked   = errors.New("sync: device has been revoked")
	ErrMissingADK           = errors.New("sync: explicit 32-byte ADK is required")
	ErrConflictNotFound     = errors.New("sync: conflict not found")
	ErrClassForbidden       = errors.New("sync: passwords and key-manager data can never be synchronized")
	ErrClassNotAllowed      = errors.New("sync: data class is not enabled in the sync allowlist")
)
