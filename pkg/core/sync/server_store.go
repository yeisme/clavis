package sync

import (
	"crypto/ed25519"
	"errors"
)

// ServerStore is the explicit compatibility adapter for the in-memory Server.
// It delegates protocol mutations to Server (rather than duplicating them),
// while exposing read-only store primitives needed by shared store tests and
// by a future persistent Server integration.
type ServerStore struct{ server *Server }

var _ SyncStore = (*ServerStore)(nil)

func NewServerStore(server *Server) (*ServerStore, error) {
	if server == nil {
		return nil, errors.New("sync: nil server")
	}
	return &ServerStore{server: server}, nil
}

func (s *ServerStore) RegisterAccount(account string) ([]byte, error) {
	return s.server.RegisterAccount(account)
}
func (s *ServerStore) LoginWithCredential(account string, credential []byte) error {
	return s.server.LoginWithCredential(account, credential)
}
func (s *ServerStore) PairDeviceWithCredential(account, device string, pub ed25519.PublicKey, credential []byte) error {
	return s.server.PairDeviceWithCredential(account, device, pub, credential)
}
func (s *ServerStore) DevicePublicKey(account, device string) (ed25519.PublicKey, error) {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	pub := s.server.accounts[account][device]
	if len(pub) != ed25519.PublicKeySize {
		return nil, ErrStoreDeviceMissing
	}
	return append(ed25519.PublicKey(nil), pub...), nil
}
func (s *ServerStore) UploadEnvelope(account, device, object string, version uint64, envelope EncryptedEnvelope) error {
	return s.server.UploadEnvelope(account, device, object, version, envelope)
}
func (s *ServerStore) GetEnvelope(account, object string) (EncryptedEnvelope, error) {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	raw, ok := s.server.objects[account][object]
	if !ok {
		return EncryptedEnvelope{}, ErrStoreObjectMissing
	}
	return parseEnvelope(raw)
}
func (s *ServerStore) ObjectVersion(account, object string) (uint64, error) {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	v, ok := s.server.versions[account][object]
	if !ok {
		if t, exists := s.server.tombstones[account][object]; exists {
			return t.Version, nil
		}
		return 0, ErrStoreObjectMissing
	}
	return v, nil
}
func (s *ServerStore) SubmitDelete(instruction DeleteInstruction) (Tombstone, error) {
	return s.server.SubmitDelete(instruction)
}
func (s *ServerStore) SyncTombstones(account, device string, after uint64) ([]Tombstone, error) {
	return s.server.SyncTombstones(account, device, after)
}
func (s *ServerStore) ConfirmTombstones(account, device string, sequence uint64) error {
	return s.server.ConfirmTombstones(account, device, sequence)
}
func (s *ServerStore) VerifyTombstone(tombstone Tombstone) bool {
	return s.server.VerifyTombstone(tombstone)
}
func (s *ServerStore) Watermark(account, device string) (uint64, error) {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	if s.server.accounts[account][device] == nil {
		return 0, ErrStoreDeviceMissing
	}
	return s.server.watermarks[account][device], nil
}
func (s *ServerStore) IssuedBatch(account, device string) (TombstoneBatch, bool, error) {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	if s.server.accounts[account][device] == nil {
		return TombstoneBatch{}, false, ErrStoreDeviceMissing
	}
	b, ok := s.server.issued[account][device]
	return TombstoneBatch{From: b.from, To: b.to}, ok, nil
}
func (s *ServerStore) AuditForAccount(account string) []AuditEvent {
	return s.server.AuditForAccount(account)
}
func (s *ServerStore) LatestSequence(account string) (uint64, error) {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	if _, ok := s.server.credentials[account]; !ok {
		return 0, ErrStoreAccountMissing
	}
	return s.server.accountSequences[account], nil
}
func (s *ServerStore) Close() error { return nil }
