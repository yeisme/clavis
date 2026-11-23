package sync

import (
	"crypto/ed25519"
	"errors"
)

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
)
