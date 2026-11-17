// Package sync provides privacy-preserving cross-device synchronization.
package sync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion uint16 = 1
	deleteDomain           = "clavis.sync.delete.v1"
	envelopeDomain         = "clavis.sync.envelope.v1"
)

type DataClass string

const (
	InputMethod    DataClass = "input_method"
	LearningData   DataClass = "learning_data"
	ClipboardText  DataClass = "clipboard_text"
	Media          DataClass = "media"
	ApplicationCtx DataClass = "application_context"
	SensitiveVault DataClass = "sensitive_vault"
	Passwords      DataClass = "passwords"
	KeyManager     DataClass = "key_manager"
)

type SyncItem struct {
	Class     DataClass `json:"class"`
	Label     string    `json:"label"`
	Sensitive bool      `json:"sensitive"`
	Allowed   bool      `json:"allowed"`
}
type SyncPlan struct {
	InputMethod    bool `json:"inputMethod"`
	LearningData   bool `json:"learningData"`
	ClipboardText  bool `json:"clipboardText"`
	Media          bool `json:"media"`
	ApplicationCtx bool `json:"applicationContext"`
	SensitiveVault bool `json:"sensitiveVault"`
	Passwords      bool `json:"passwords"`
	KeyManager     bool `json:"keyManager"`
}

func DefaultSyncPlan() SyncPlan {
	return SyncPlan{InputMethod: true, LearningData: true, ClipboardText: true, Media: true, ApplicationCtx: true}
}
func (p SyncPlan) Validate() error {
	if p.Passwords || p.KeyManager {
		return errors.New("sync: passwords and key-manager data can never be synchronized")
	}
	return nil
}
func (p SyncPlan) allows(c DataClass) bool {
	switch c {
	case InputMethod:
		return p.InputMethod
	case LearningData:
		return p.LearningData
	case ClipboardText:
		return p.ClipboardText
	case Media:
		return p.Media
	case ApplicationCtx:
		return p.ApplicationCtx
	case SensitiveVault:
		return p.SensitiveVault
	}
	return false
}
func (p SyncPlan) Items() []SyncItem {
	return []SyncItem{
		{Class: InputMethod, Label: "Input method", Allowed: p.InputMethod}, {Class: LearningData, Label: "Learning data", Allowed: p.LearningData}, {Class: ClipboardText, Label: "Clipboard text", Allowed: p.ClipboardText}, {Class: Media, Label: "Media", Allowed: p.Media}, {Class: ApplicationCtx, Label: "Application context", Allowed: p.ApplicationCtx}, {Class: SensitiveVault, Label: "Sensitive vault", Sensitive: true, Allowed: p.SensitiveVault}, {Class: Passwords, Label: "Passwords (never synchronized)", Sensitive: true}, {Class: KeyManager, Label: "Key manager (never synchronized)", Sensitive: true},
	}
}

// Values are deliberately excluded from JSON. They are opaque local values.
type PersonalBaseline struct {
	AccountID string            `json:"accountId"`
	Plan      SyncPlan          `json:"plan"`
	Values    map[string][]byte `json:"-"`
}

func NewPersonalBaseline(account string, plan SyncPlan) (*PersonalBaseline, error) {
	if strings.TrimSpace(account) == "" {
		return nil, errors.New("sync: account id cannot be empty")
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return &PersonalBaseline{AccountID: account, Plan: plan, Values: map[string][]byte{}}, nil
}
func (b *PersonalBaseline) Set(k string, v []byte) error {
	if b == nil || strings.TrimSpace(k) == "" {
		return errors.New("sync: baseline key cannot be empty")
	}
	if b.Values == nil {
		b.Values = map[string][]byte{}
	}
	b.Values[k] = append([]byte(nil), v...)
	return nil
}
func (b *PersonalBaseline) Get(k string) ([]byte, bool) {
	if b == nil {
		return nil, false
	}
	v, ok := b.Values[k]
	return append([]byte(nil), v...), ok
}

type DeviceOverlay struct {
	DeviceID string         `json:"deviceId"`
	Values   map[string]any `json:"-"`
}

func NewDeviceOverlay(id string, values map[string]any) (*DeviceOverlay, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("sync: device id cannot be empty")
	}
	cp := map[string]any{}
	for k, v := range values {
		cp[k] = v
	}
	return &DeviceOverlay{DeviceID: id, Values: cp}, nil
}
func (o *DeviceOverlay) Set(k string, v any) error {
	if o == nil || strings.TrimSpace(k) == "" {
		return errors.New("sync: overlay key cannot be empty")
	}
	if o.Values == nil {
		o.Values = map[string]any{}
	}
	o.Values[k] = v
	return nil
}
func (o *DeviceOverlay) Get(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.Values[k]
	return v, ok
}

type AuditEvent struct {
	AccountID    string    `json:"accountId"`
	ObjectDigest string    `json:"objectDigest"`
	DeviceID     string    `json:"deviceId"`
	At           time.Time `json:"at"`
	Action       string    `json:"action"`
}

type DeleteInstruction struct {
	AccountID       string    `json:"accountId"`
	ObjectID        string    `json:"objectId"`
	DeviceID        string    `json:"deviceId"`
	Version         uint64    `json:"version"`
	IssuedAt        time.Time `json:"issuedAt"`
	ProtocolVersion uint16    `json:"protocolVersion"`
	Signature       []byte    `json:"signature"`
}

func (d DeleteInstruction) canonical() []byte {
	var b []byte
	add := func(s string) { b = binary.AppendUvarint(b, uint64(len(s))); b = append(b, s...) }
	add(deleteDomain)
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], d.ProtocolVersion)
	b = append(b, p[:]...)
	add(d.AccountID)
	add(d.ObjectID)
	add(d.DeviceID)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], d.Version)
	b = append(b, n[:]...)
	binary.BigEndian.PutUint64(n[:], uint64(d.IssuedAt.UTC().UnixNano()))
	return append(b, n[:]...)
}
func (d DeleteInstruction) Verify(pub ed25519.PublicKey) bool {
	return d.ProtocolVersion == ProtocolVersion && len(d.Signature) == ed25519.SignatureSize && ed25519.Verify(pub, d.canonical(), d.Signature)
}

// Sign returns a copy of the instruction signed by priv.
func (d DeleteInstruction) Sign(priv ed25519.PrivateKey) DeleteInstruction {
	d.Signature = ed25519.Sign(priv, d.canonical())
	return d
}

type Tombstone struct {
	AccountID       string    `json:"accountId"`
	ObjectID        string    `json:"objectId"`
	DeviceID        string    `json:"deviceId"`
	Version         uint64    `json:"version"`
	Sequence        uint64    `json:"sequence"`
	IssuedAt        time.Time `json:"issuedAt"`
	DeletedAt       time.Time `json:"deletedAt"`
	ProtocolVersion uint16    `json:"protocolVersion"`
	Signature       []byte    `json:"signature"`
}

func (t Tombstone) canonical() []byte {
	return (DeleteInstruction{AccountID: t.AccountID, ObjectID: t.ObjectID, DeviceID: t.DeviceID, Version: t.Version, IssuedAt: t.IssuedAt, ProtocolVersion: t.ProtocolVersion}).canonical()
}
func (t Tombstone) Verify(pub ed25519.PublicKey) bool {
	return t.ProtocolVersion == ProtocolVersion && len(t.Signature) == ed25519.SignatureSize && ed25519.Verify(pub, t.canonical(), t.Signature)
}

type EncryptedEnvelope struct {
	ProtocolVersion uint16    `json:"protocolVersion"`
	AccountID       string    `json:"accountId"`
	DeviceID        string    `json:"deviceId"`
	ObjectID        string    `json:"objectId"`
	Version         uint64    `json:"version"`
	Class           DataClass `json:"class"`
	Nonce           []byte    `json:"nonce"`
	Ciphertext      []byte    `json:"ciphertext"`
	Signature       []byte    `json:"signature"`
}

func (e EncryptedEnvelope) aad() []byte {
	// Encode variable-length fields with their lengths rather than separators.
	// This makes the representation unambiguous even when an identifier contains
	// a colon (or any other delimiter-looking byte).
	b := make([]byte, 0, len(envelopeDomain)+len(e.AccountID)+len(e.DeviceID)+len(e.ObjectID)+len(e.Class)+32)
	addString := func(value string) {
		b = binary.AppendUvarint(b, uint64(len(value)))
		b = append(b, value...)
	}
	addString(envelopeDomain)
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], e.ProtocolVersion)
	b = append(b, version[:]...)
	addString(e.AccountID)
	addString(e.DeviceID)
	addString(e.ObjectID)
	var objectVersion [8]byte
	binary.BigEndian.PutUint64(objectVersion[:], e.Version)
	b = append(b, objectVersion[:]...)
	addString(string(e.Class))
	return b
}
func (e EncryptedEnvelope) canonical() []byte {
	// Sign the complete envelope scope and authenticated ciphertext. Length
	// prefixes keep identifiers unambiguous, while the digests avoid signing
	// large payloads twice and bind both encryption context and ciphertext.
	b := make([]byte, 0, len(envelopeDomain)+len(e.AccountID)+len(e.DeviceID)+len(e.ObjectID)+len(e.Class)+96)
	addString := func(value string) {
		b = binary.AppendUvarint(b, uint64(len(value)))
		b = append(b, value...)
	}
	addString(envelopeDomain)
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], e.ProtocolVersion)
	b = append(b, version[:]...)
	addString(e.AccountID)
	addString(e.DeviceID)
	addString(e.ObjectID)
	var objectVersion [8]byte
	binary.BigEndian.PutUint64(objectVersion[:], e.Version)
	b = append(b, objectVersion[:]...)
	addString(string(e.Class))
	nonceDigest := sha256.Sum256(e.Nonce)
	ciphertextDigest := sha256.Sum256(e.Ciphertext)
	aadDigest := sha256.Sum256(e.aad())
	b = append(b, nonceDigest[:]...)
	b = append(b, ciphertextDigest[:]...)
	return append(b, aadDigest[:]...)
}
func (e EncryptedEnvelope) Verify(pub ed25519.PublicKey) bool {
	return e.ProtocolVersion == ProtocolVersion && len(e.Signature) == ed25519.SignatureSize && ed25519.Verify(pub, e.canonical(), e.Signature)
}
func validClass(c DataClass) bool {
	switch c {
	case InputMethod, LearningData, ClipboardText, Media, ApplicationCtx, SensitiveVault:
		return true
	}
	return false
}
func marshalEnvelope(e EncryptedEnvelope) ([]byte, error) {
	if e.ProtocolVersion != ProtocolVersion || e.AccountID == "" || e.DeviceID == "" || e.ObjectID == "" || e.Version == 0 || !validClass(e.Class) || len(e.Nonce) != 12 || len(e.Ciphertext) < 16 || len(e.Signature) != ed25519.SignatureSize {
		return nil, errors.New("sync: invalid encrypted envelope")
	}
	return json.Marshal(e)
}
func parseEnvelope(b []byte) (EncryptedEnvelope, error) {
	var e EncryptedEnvelope
	if err := json.Unmarshal(b, &e); err != nil {
		return e, errors.New("sync: upload must be an encrypted envelope")
	}
	if _, err := marshalEnvelope(e); err != nil {
		return e, err
	}
	return e, nil
}
func MarshalEncryptedEnvelope(e EncryptedEnvelope) ([]byte, error) { return marshalEnvelope(e) }
func ParseEncryptedEnvelope(b []byte) (EncryptedEnvelope, error)   { return parseEnvelope(b) }

// LocalCache contains every local copy which must be removed by a delete.
type LocalCache struct {
	mu         sync.RWMutex
	Cleartext  map[string][]byte
	Ciphertext map[string][]byte
	Media      map[string][]byte
	Backups    map[string]LocalArtifact
	Exports    map[string]LocalArtifact
}
type LocalArtifact struct {
	ObjectIDs []string
	Data      []byte
}

func NewLocalCache() *LocalCache {
	return &LocalCache{Cleartext: map[string][]byte{}, Ciphertext: map[string][]byte{}, Media: map[string][]byte{}, Backups: map[string]LocalArtifact{}, Exports: map[string]LocalArtifact{}}
}
func (c *LocalCache) put(m map[string][]byte, id string, d []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m != nil {
		m[id] = append([]byte(nil), d...)
	}
}
func (c *LocalCache) PutCleartext(id string, d []byte)  { c.put(c.Cleartext, id, d) }
func (c *LocalCache) PutCiphertext(id string, d []byte) { c.put(c.Ciphertext, id, d) }
func (c *LocalCache) PutMedia(id string, d []byte)      { c.put(c.Media, id, d) }
func (c *LocalCache) putArtifact(m map[string]LocalArtifact, n string, ids []string, d []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m != nil {
		m[n] = LocalArtifact{ObjectIDs: append([]string(nil), ids...), Data: append([]byte(nil), d...)}
	}
}
func (c *LocalCache) PutBackup(n string, ids []string, d []byte) { c.putArtifact(c.Backups, n, ids, d) }
func (c *LocalCache) PutExport(n string, ids []string, d []byte) { c.putArtifact(c.Exports, n, ids, d) }

type CleanupReport struct{ Cleartext, Ciphertext, Media, Backups, Exports int }

func (c *LocalCache) ApplyTombstones(ts []Tombstone) CleanupReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := map[string]struct{}{}
	for _, t := range ts {
		if t.ObjectID != "" {
			ids[t.ObjectID] = struct{}{}
		}
	}
	var r CleanupReport
	for id := range ids {
		if _, ok := c.Cleartext[id]; ok {
			delete(c.Cleartext, id)
			r.Cleartext++
		}
		if _, ok := c.Ciphertext[id]; ok {
			delete(c.Ciphertext, id)
			r.Ciphertext++
		}
		if _, ok := c.Media[id]; ok {
			delete(c.Media, id)
			r.Media++
		}
	}
	for n, a := range c.Backups {
		if artifactContains(a, ids) {
			delete(c.Backups, n)
			r.Backups++
		}
	}
	for n, a := range c.Exports {
		if artifactContains(a, ids) {
			delete(c.Exports, n)
			r.Exports++
		}
	}
	return r
}
func artifactContains(a LocalArtifact, ids map[string]struct{}) bool {
	for _, id := range a.ObjectIDs {
		if _, ok := ids[id]; ok {
			return true
		}
	}
	return false
}

// Server stores only encrypted payloads and deletion metadata.
type tombstoneBatch struct {
	from uint64
	to   uint64
}

type Server struct {
	mu               sync.Mutex
	accounts         map[string]map[string]ed25519.PublicKey
	credentials      map[string][32]byte
	accountSequences map[string]uint64
	tombstones       map[string]map[string]Tombstone
	objects          map[string]map[string][]byte
	versions         map[string]map[string]uint64
	watermarks       map[string]map[string]uint64
	issued           map[string]map[string]tombstoneBatch
	tombstoneLog     map[string]map[uint64]Tombstone
	sequence         uint64
	audit            []AuditEvent
}

func NewServer() *Server {
	return &Server{accounts: map[string]map[string]ed25519.PublicKey{}, credentials: map[string][32]byte{}, accountSequences: map[string]uint64{}, tombstones: map[string]map[string]Tombstone{}, tombstoneLog: map[string]map[uint64]Tombstone{}, objects: map[string]map[string][]byte{}, versions: map[string]map[string]uint64{}, watermarks: map[string]map[string]uint64{}, issued: map[string]map[string]tombstoneBatch{}}
}
func (s *Server) initAccountLocked(a string) {
	if s.accounts[a] == nil {
		s.accounts[a] = map[string]ed25519.PublicKey{}
	}
	if s.tombstones[a] == nil {
		s.tombstones[a] = map[string]Tombstone{}
	}
	if s.objects[a] == nil {
		s.objects[a] = map[string][]byte{}
	}
	if s.versions[a] == nil {
		s.versions[a] = map[string]uint64{}
	}
	if s.watermarks[a] == nil {
		s.watermarks[a] = map[string]uint64{}
	}
	if s.issued[a] == nil {
		s.issued[a] = map[string]tombstoneBatch{}
	}
	if s.tombstoneLog[a] == nil {
		s.tombstoneLog[a] = map[uint64]Tombstone{}
	}
}

// Login is retained for first-account registration. Existing accounts require
// LoginWithCredential and therefore cannot be silently taken over.
func (s *Server) Login(account string) error {
	if strings.TrimSpace(account) == "" {
		return errors.New("sync: account id cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.credentials[account]; exists {
		return errors.New("sync: account already exists; credential required")
	}
	s.initAccountLocked(account)
	var c [32]byte
	if _, err := io.ReadFull(rand.Reader, c[:]); err != nil {
		return err
	}
	s.credentials[account] = c
	return nil
}
func (s *Server) RegisterAccount(account string) ([]byte, error) {
	if strings.TrimSpace(account) == "" {
		return nil, errors.New("sync: account id cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.credentials[account]; exists {
		return nil, errors.New("sync: account already exists")
	}
	s.initAccountLocked(account)
	var c [32]byte
	if _, err := io.ReadFull(rand.Reader, c[:]); err != nil {
		return nil, err
	}
	s.credentials[account] = c
	return append([]byte(nil), c[:]...), nil
}
func (s *Server) LoginWithCredential(account string, credential []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.credentials[account]
	if !ok || len(credential) != 32 || !hmac.Equal(c[:], credential) {
		return errors.New("sync: invalid account credential")
	}
	s.initAccountLocked(account)
	return nil
}
func (s *Server) PairDevice(account, device string, pub ed25519.PublicKey) error {
	// The legacy entry point is retained, but unauthenticated pairing is no
	// longer permitted. Call PairDeviceWithCredential for existing accounts.
	return s.pairDevice(account, device, pub, []byte{})
}
func (s *Server) pairDevice(account, device string, pub ed25519.PublicKey, credential []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.credentials[account]
	if !ok || len(credential) != 32 || !hmac.Equal(c[:], credential) {
		return errors.New("sync: invalid account credential")
	}
	if strings.TrimSpace(device) == "" || len(pub) != ed25519.PublicKeySize {
		return errors.New("sync: invalid device pairing")
	}
	s.initAccountLocked(account)
	if old, exists := s.accounts[account][device]; exists && !hmac.Equal(old, pub) {
		return errors.New("sync: device key cannot be overwritten")
	}
	s.accounts[account][device] = append(ed25519.PublicKey(nil), pub...)
	return nil
}
func (s *Server) PairDeviceWithCredential(account, device string, pub ed25519.PublicKey, credential []byte) error {
	return s.pairDevice(account, device, pub, credential)
}

func (s *Server) SubmitDelete(d DeleteInstruction) (Tombstone, error) {
	if d.AccountID == "" || d.ObjectID == "" || d.DeviceID == "" || d.Version == 0 || d.ProtocolVersion != ProtocolVersion {
		return Tombstone{}, errors.New("sync: incomplete delete instruction")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := s.accounts[d.AccountID][d.DeviceID]
	if len(pub) != ed25519.PublicKeySize || !d.Verify(pub) {
		return Tombstone{}, errors.New("sync: invalid delete signature")
	}
	if old, ok := s.tombstones[d.AccountID][d.ObjectID]; ok && d.Version <= old.Version {
		return old, fmt.Errorf("sync: stale delete version %d", d.Version)
	}
	boundary := s.versions[d.AccountID][d.ObjectID]
	if d.Version > boundary {
		boundary = d.Version
	}
	s.versions[d.AccountID][d.ObjectID] = boundary
	s.sequence++
	s.accountSequences[d.AccountID]++
	t := Tombstone{AccountID: d.AccountID, ObjectID: d.ObjectID, DeviceID: d.DeviceID, Version: d.Version, Sequence: s.accountSequences[d.AccountID], IssuedAt: d.IssuedAt, DeletedAt: time.Now().UTC(), ProtocolVersion: ProtocolVersion, Signature: append([]byte(nil), d.Signature...)}
	s.tombstones[d.AccountID][d.ObjectID] = t
	s.tombstoneLog[d.AccountID][t.Sequence] = t
	delete(s.objects[d.AccountID], d.ObjectID)
	digest := sha256.Sum256([]byte(d.ObjectID))
	s.audit = append(s.audit, AuditEvent{AccountID: d.AccountID, ObjectDigest: hex.EncodeToString(digest[:]), DeviceID: d.DeviceID, At: t.DeletedAt, Action: "delete"})
	return t, nil
}

// SyncTombstones ignores afterSequence for compatibility with the original
// API. The server uses the device's confirmed watermark as the authoritative
// cursor and records exactly the contiguous batch it returned. Only that
// recorded batch may subsequently be confirmed. While a batch is pending,
// repeated calls return that same batch rather than replacing it with a newer
// range.
func (s *Server) SyncTombstones(account, device string, after uint64) ([]Tombstone, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts[account][device] == nil {
		return nil, errors.New("sync: device is not paired")
	}
	if batch, pending := s.issued[account][device]; pending {
		return s.tombstoneBatchLocked(account, batch)
	}
	confirmed := s.watermarks[account][device]
	out := make([]Tombstone, 0)
	for sequence := confirmed + 1; sequence <= s.accountSequences[account]; sequence++ {
		t, ok := s.tombstoneLog[account][sequence]
		if !ok {
			return nil, errors.New("sync: tombstone sequence is not contiguous")
		}
		out = append(out, t)
	}
	if len(out) > 0 {
		s.issued[account][device] = tombstoneBatch{from: out[0].Sequence, to: out[len(out)-1].Sequence}
	} else {
		delete(s.issued[account], device)
	}
	return out, nil
}

func (s *Server) tombstoneBatchLocked(account string, batch tombstoneBatch) ([]Tombstone, error) {
	if batch.from == 0 || batch.to < batch.from {
		return nil, ErrStoreBatch
	}
	out := make([]Tombstone, 0, batch.to-batch.from+1)
	for sequence := batch.from; sequence <= batch.to; sequence++ {
		t, ok := s.tombstoneLog[account][sequence]
		if !ok {
			return nil, errors.New("sync: tombstone sequence is not contiguous")
		}
		out = append(out, t)
	}
	return out, nil
}
func (s *Server) ConfirmTombstones(account, device string, sequence uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts[account][device] == nil {
		return errors.New("sync: device is not paired")
	}
	confirmed := s.watermarks[account][device]
	if sequence < confirmed {
		return errors.New("sync: confirmation moved backwards")
	}
	latest := s.accountSequences[account]
	if sequence > latest {
		return errors.New("sync: confirmation is ahead of server")
	}
	if sequence == confirmed {
		// A watermark match is a bootstrap no-op only before this account has
		// ever issued a tombstone. Once the account sequence advances, zero is
		// not confirmation merely because this device has not fetched yet.
		if sequence == 0 && latest == 0 {
			if _, pending := s.issued[account][device]; !pending {
				return nil
			}
		}
		return errors.New("sync: tombstone batch is not confirmed")
	}
	batch, ok := s.issued[account][device]
	if !ok || batch.from != confirmed+1 || batch.to != sequence || batch.to > latest {
		return errors.New("sync: confirmation does not match issued tombstone batch")
	}
	s.watermarks[account][device] = sequence
	delete(s.issued[account], device)
	return nil
}
func (s *Server) VerifyTombstone(t Tombstone) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.tombstones[t.AccountID][t.ObjectID]
	return ok && stored.Sequence == t.Sequence && stored.Version == t.Version && stored.DeviceID == t.DeviceID && t.AccountID != "" && t.ObjectID != "" && t.Version != 0 && t.Sequence != 0 && t.Sequence <= s.accountSequences[t.AccountID] && t.Verify(s.accounts[t.AccountID][t.DeviceID])
}

// Upload accepts only a JSON EncryptedEnvelope. The legacy signature remains,
// but its payload must now be that envelope (use UploadEnvelope for clarity).
func (s *Server) Upload(account, device, object string, version uint64, payload []byte) error {
	e, err := parseEnvelope(payload)
	if err != nil {
		return err
	}
	return s.UploadEnvelope(account, device, object, version, e)
}
func (s *Server) UploadEnvelope(account, device, object string, version uint64, e EncryptedEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.AccountID != account || e.DeviceID != device || e.ObjectID != object || e.Version != version || e.ProtocolVersion != ProtocolVersion || !validClass(e.Class) || len(e.Nonce) != 12 || len(e.Ciphertext) < 16 {
		return errors.New("sync: envelope does not match upload")
	}
	pub := s.accounts[account][device]
	if len(pub) != ed25519.PublicKeySize || !e.Verify(pub) {
		return errors.New("sync: invalid envelope signature")
	}
	if s.accounts[account][device] == nil {
		return errors.New("sync: device is not paired")
	}
	if s.watermarks[account][device] < s.accountSequences[account] {
		return errors.New("sync: tombstones must be synchronized before upload")
	}
	if _, deleted := s.tombstones[account][object]; deleted {
		return errors.New("sync: object is permanently deleted")
	}
	if version <= s.versions[account][object] {
		return errors.New("sync: upload version is not newer")
	}
	encoded, err := marshalEnvelope(e)
	if err != nil {
		return err
	}
	s.objects[account][object] = encoded
	s.versions[account][object] = version
	return nil
}
func (s *Server) Audit(accounts ...string) []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(accounts) == 0 {
		return append([]AuditEvent(nil), s.audit...)
	}
	out := make([]AuditEvent, 0)
	for _, event := range s.audit {
		if event.AccountID == accounts[0] {
			out = append(out, event)
		}
	}
	return out
}
func (s *Server) AuditForAccount(account string) []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []AuditEvent{}
	for _, e := range s.audit {
		if e.AccountID == account {
			out = append(out, e)
		}
	}
	return out
}
func (s *Server) LatestSequence() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.sequence }

type Client struct {
	mu                  sync.Mutex
	syncMu              sync.Mutex
	server              *Server
	accountID, deviceID string
	credential          []byte
	privateKey          ed25519.PrivateKey
	cache               *LocalCache
	watermark           uint64
	syncEnabled         bool
	recoveryHash        [32]byte
	recoveryReady       bool
	plan                SyncPlan
	vault               *Vault
}

func NewClient(s *Server, device string) (*Client, error) {
	if s == nil || strings.TrimSpace(device) == "" {
		return nil, errors.New("sync: server and device id are required")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	v, err := NewVaultWithError("")
	if err != nil {
		return nil, err
	}
	return &Client{server: s, deviceID: device, privateKey: priv, cache: NewLocalCache(), plan: DefaultSyncPlan(), vault: v}, nil
}
func (c *Client) Login(account string) error {
	// Login may register a new account, but deliberately cannot recover an
	// existing account's credential from the Server object.
	cred, err := c.server.RegisterAccount(account)
	if err != nil {
		return err
	}
	return c.loginWithCredential(account, cred)
}
func (c *Client) LoginWithCredential(account string, credential []byte) error {
	if err := c.server.LoginWithCredential(account, credential); err != nil {
		return err
	}
	return c.loginWithCredential(account, credential)
}
func (c *Client) loginWithCredential(account string, credential []byte) error {
	c.mu.Lock()
	c.accountID = account
	c.credential = append([]byte(nil), credential...)
	c.mu.Unlock()
	return nil
}
func (c *Client) PairDevice() error {
	c.mu.Lock()
	a, d := c.accountID, c.deviceID
	pub := append(ed25519.PublicKey(nil), c.privateKey.Public().(ed25519.PublicKey)...)
	cred := append([]byte(nil), c.credential...)
	c.mu.Unlock()
	if a == "" {
		return errors.New("sync: login is required")
	}
	return c.server.PairDeviceWithCredential(a, d, pub, cred)
}
func (c *Client) Cache() *LocalCache { return c.cache }
func (c *Client) Vault() *Vault      { return c.vault }
func (c *Client) ConfigurePlan(p SyncPlan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	c.plan = p
	c.mu.Unlock()
	return nil
}
func (c *Client) Plan() SyncPlan { c.mu.Lock(); defer c.mu.Unlock(); return c.plan }
func (c *Client) GenerateDeleteInstruction(object string) (DeleteInstruction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accountID == "" {
		return DeleteInstruction{}, errors.New("sync: login is required")
	}
	if strings.TrimSpace(object) == "" {
		return DeleteInstruction{}, errors.New("sync: object id cannot be empty")
	}
	d := DeleteInstruction{AccountID: c.accountID, ObjectID: object, DeviceID: c.deviceID, Version: c.watermark + 1, IssuedAt: time.Now().UTC(), ProtocolVersion: ProtocolVersion}
	d.Signature = ed25519.Sign(c.privateKey, d.canonical())
	return d, nil
}
func (c *Client) applyTombstone(t Tombstone) CleanupReport {
	r := c.cache.ApplyTombstones([]Tombstone{t})
	_ = c.vault.Delete(t.ObjectID)
	c.vault.deleteForTombstone(t.ObjectID)
	return r
}
func (c *Client) Delete(object string) error {
	d, err := c.GenerateDeleteInstruction(object)
	if err != nil {
		return err
	}
	t, err := c.server.SubmitDelete(d)
	if t.ObjectID != "" {
		c.applyTombstone(t)
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	if t.Sequence > c.watermark {
		c.watermark = t.Sequence
	}
	c.mu.Unlock()
	return nil
}
func (c *Client) Sync() ([]Tombstone, CleanupReport, error) {
	// The server records one issued tombstone batch per device. Serialize the
	// complete fetch/apply/confirm sequence so concurrent calls cannot replace
	// one another's issued batch.
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.mu.Lock()
	a, d, after := c.accountID, c.deviceID, c.watermark
	c.mu.Unlock()
	if a == "" {
		return nil, CleanupReport{}, errors.New("sync: login is required")
	}
	ts, err := c.server.SyncTombstones(a, d, after)
	if err != nil {
		return nil, CleanupReport{}, err
	}
	var r CleanupReport
	for _, t := range ts {
		if t.AccountID != a || t.ObjectID == "" || t.Version == 0 || !c.server.VerifyTombstone(t) {
			return nil, CleanupReport{}, errors.New("sync: invalid tombstone")
		}
		x := c.applyTombstone(t)
		r.Cleartext += x.Cleartext
		r.Ciphertext += x.Ciphertext
		r.Media += x.Media
		r.Backups += x.Backups
		r.Exports += x.Exports
	}
	confirm := after
	for _, t := range ts {
		if t.Sequence > confirm {
			confirm = t.Sequence
		}
	}
	if len(ts) > 0 {
		if err := c.server.ConfirmTombstones(a, d, confirm); err != nil {
			return nil, CleanupReport{}, err
		}
	}
	c.mu.Lock()
	if confirm > c.watermark {
		c.watermark = confirm
	}
	c.mu.Unlock()
	return ts, r, nil
}
func (c *Client) Upload(object string, version uint64, payload []byte) error {
	return c.UploadClass(InputMethod, object, version, payload)
}
func (c *Client) UploadClass(class DataClass, object string, version uint64, payload []byte) error {
	c.mu.Lock()
	a, d, enabled, p := c.accountID, c.deviceID, c.syncEnabled, c.plan
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	c.mu.Unlock()
	if !enabled {
		return errors.New("sync: recovery code must be saved and confirmed before sync is enabled")
	}
	if a == "" {
		return errors.New("sync: login is required")
	}
	if class == Passwords || class == KeyManager {
		return errors.New("sync: passwords and key-manager data can never be synchronized")
	}
	if !p.allows(class) {
		return fmt.Errorf("sync: %s is not enabled in the sync plan", class)
	}
	if class == SensitiveVault && !c.vault.Authorized() {
		return errors.New("sync: vault authorization required")
	}
	if object == "" || version == 0 {
		return errors.New("sync: invalid upload")
	}
	e, err := clientEnvelope(priv, a, d, object, version, class, payload)
	if err != nil {
		return err
	}
	return c.server.UploadEnvelope(a, d, object, version, e)
}

// NewEncryptedEnvelope seals an opaque payload for an authenticated sync upload.
// The payload is encrypted before it enters any SyncStore implementation.
func NewEncryptedEnvelope(priv ed25519.PrivateKey, a, d, o string, v uint64, class DataClass, payload []byte) (EncryptedEnvelope, error) {
	e := EncryptedEnvelope{ProtocolVersion: ProtocolVersion, AccountID: a, DeviceID: d, ObjectID: o, Version: v, Class: class, Nonce: make([]byte, 12)}
	if _, err := io.ReadFull(rand.Reader, e.Nonce); err != nil {
		return e, err
	}
	seed := sha256.Sum256(priv)
	block, err := aes.NewCipher(seed[:])
	if err != nil {
		return e, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return e, err
	}
	e.Ciphertext = gcm.Seal(nil, e.Nonce, payload, e.aad())
	e.Signature = ed25519.Sign(priv, e.canonical())
	return e, nil
}

func clientEnvelope(priv ed25519.PrivateKey, a, d, o string, v uint64, class DataClass, payload []byte) (EncryptedEnvelope, error) {
	return NewEncryptedEnvelope(priv, a, d, o, v, class, payload)
}
func (c *Client) PrepareRecoveryCode() (string, error) {
	b := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	code := strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), "=")
	c.mu.Lock()
	c.recoveryHash = sha256.Sum256([]byte(code))
	c.recoveryReady = false
	c.syncEnabled = false
	c.mu.Unlock()
	return code, nil
}
func (c *Client) ConfirmRecoveryCode(code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recoveryHash == ([32]byte{}) || !equalHash(c.recoveryHash, []byte(code)) {
		return errors.New("sync: recovery code does not match")
	}
	c.recoveryReady = true
	c.syncEnabled = true
	return nil
}
func equalHash(exp [32]byte, b []byte) bool {
	got := sha256.Sum256(b)
	var d byte
	for i := range got {
		d |= got[i] ^ exp[i]
	}
	return d == 0
}
func (c *Client) SyncEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.syncEnabled && c.recoveryReady
}
func (c *Client) Watermark() uint64 { c.mu.Lock(); defer c.mu.Unlock(); return c.watermark }

// Vault derives its AES key from both a random root and the independent
// authorization factor. Revoke rotates the root, invalidating old ciphertext.
type Vault struct {
	mu         sync.RWMutex
	authHash   [32]byte
	authSecret string
	root       []byte
	key        []byte
	authorized bool
	objects    map[string][]byte
}

func NewVaultWithError(auth string) (*Vault, error) {
	root := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, root); err != nil {
		return nil, fmt.Errorf("sync: vault key generation: %w", err)
	}
	v := &Vault{root: root, authHash: sha256.Sum256([]byte(auth)), authSecret: auth, objects: map[string][]byte{}}
	v.key = deriveVaultKey(root, auth)
	return v, nil
}
func NewVault(auth string) *Vault { v, _ := NewVaultWithError(auth); return v }
func deriveVaultKey(root []byte, auth string) []byte {
	m := hmac.New(sha256.New, root)
	m.Write([]byte("clavis.vault.hkdf.v1"))
	m.Write([]byte{0})
	m.Write([]byte(auth))
	return m.Sum(nil)
}
func (v *Vault) ConfigureAuthorization(auth string) error {
	if strings.TrimSpace(auth) == "" {
		return errors.New("sync: vault authorization cannot be empty")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.authorized {
		return errors.New("sync: revoke vault before changing authorization")
	}
	v.authHash = sha256.Sum256([]byte(auth))
	v.authSecret = auth
	v.key = deriveVaultKey(v.root, auth)
	return nil
}
func (v *Vault) Authorize(auth string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if strings.TrimSpace(auth) == "" || !equalHash(v.authHash, []byte(auth)) {
		return errors.New("sync: vault authorization denied")
	}
	v.authorized = true
	return nil
}
func (v *Vault) RevokeWithError() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	root := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, root); err != nil {
		return fmt.Errorf("sync: vault key rotation: %w", err)
	}
	v.root = root
	v.key = deriveVaultKey(root, v.authSecret)
	v.authorized = false
	return nil
}
func (v *Vault) Revoke() error    { return v.RevokeWithError() }
func (v *Vault) Authorized() bool { v.mu.RLock(); defer v.mu.RUnlock(); return v.authorized }
func (v *Vault) Put(id string, p []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.authorized {
		return errors.New("sync: vault authorization required")
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	v.objects[id] = g.Seal(nonce, nonce, p, nil)
	return nil
}
func (v *Vault) Get(id string) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.authorized {
		return nil, errors.New("sync: vault authorization required")
	}
	raw, ok := v.objects[id]
	if !ok {
		return nil, errors.New("sync: secret not found")
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < g.NonceSize() {
		return nil, errors.New("sync: invalid encrypted secret")
	}
	return g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
}
func (v *Vault) Ciphertext(id string) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.authorized {
		return nil, errors.New("sync: vault authorization required")
	}
	raw, ok := v.objects[id]
	if !ok {
		return nil, errors.New("sync: secret not found")
	}
	return append([]byte(nil), raw...), nil
}
func (v *Vault) deleteForTombstone(id string) { v.mu.Lock(); delete(v.objects, id); v.mu.Unlock() }
func (v *Vault) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.authorized {
		return errors.New("sync: vault authorization required")
	}
	delete(v.objects, id)
	return nil
}
func MarshalDeleteInstruction(d DeleteInstruction) ([]byte, error) { return json.Marshal(d) }
func ParseDeleteInstruction(b []byte) (DeleteInstruction, error) {
	var d DeleteInstruction
	if err := json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if d.AccountID == "" || d.ObjectID == "" || d.DeviceID == "" || d.Version == 0 || d.ProtocolVersion != ProtocolVersion || len(d.Signature) != ed25519.SignatureSize {
		return d, errors.New("sync: invalid delete instruction")
	}
	return d, nil
}
func (c *Client) Fingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	sum := sha256.Sum256(c.privateKey.Public().(ed25519.PublicKey))
	return hex.EncodeToString(sum[:8])
}
