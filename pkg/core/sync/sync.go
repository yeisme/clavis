// Package sync provides privacy-preserving cross-device synchronization.
package sync

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

const (
	ProtocolVersion uint16 = 2
	deleteDomain           = "clavis.sync.delete.v2"
	envelopeDomain         = "clavis.sync.envelope.v2"
	DefaultADKKeyID        = "adk-v1"
	adkKeyID               = DefaultADKKeyID
	wrapInfo               = "clavis.sync.wrap.v2"
	recoveryInfo           = "clavis.sync.recovery.v2"
)

// ADKVersion parses a versioned ADK key identifier such as "adk-v1" or "adk-v2".
func ADKVersion(keyID string) (uint64, bool) {
	if !strings.HasPrefix(keyID, "adk-v") {
		return 0, false
	}
	suffix := strings.TrimPrefix(keyID, "adk-v")
	if suffix == "" || strings.HasPrefix(suffix, "0") {
		return 0, false
	}
	v, err := strconv.ParseUint(suffix, 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return v, true
}

// IsValidADKKeyID reports whether keyID is a valid versioned ADK identifier.
func IsValidADKKeyID(keyID string) bool {
	_, ok := ADKVersion(keyID)
	return ok
}

// FormatADKKeyID formats a positive ADK version as "adk-v<version>".
func FormatADKKeyID(version uint64) string {
	if version == 0 {
		version = 1
	}
	return fmt.Sprintf("adk-v%d", version)
}

func nextADKKeyID(current string) string {
	v, ok := ADKVersion(current)
	if !ok {
		v = 1
	}
	return FormatADKKeyID(v + 1)
}

type DataClass string

const (
	InputMethod    DataClass = "input_method"
	LearningData   DataClass = "learning_data"
	ClipboardText  DataClass = "clipboard_text"
	Media          DataClass = "media"
	ApplicationCtx DataClass = "application_context"
	Templates      DataClass = "templates"
	Snippets       DataClass = "snippets"
	Glossary       DataClass = "glossary"
	ConfigBaseline DataClass = "config_baseline"
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
	Templates      bool `json:"templates"`
	Snippets       bool `json:"snippets"`
	Glossary       bool `json:"glossary"`
	ConfigBaseline bool `json:"configBaseline"`
	SensitiveVault bool `json:"sensitiveVault"`
	Passwords      bool `json:"passwords"`
	KeyManager     bool `json:"keyManager"`
}

func DefaultSyncPlan() SyncPlan {
	return SyncPlan{InputMethod: true, LearningData: true, ClipboardText: true, Media: true, ApplicationCtx: true, Templates: true, Snippets: true, Glossary: true, ConfigBaseline: true}
}

// Phase1SyncPlan returns the strict Phase 1 allowlist: only input_method and
// learning_data are permitted; passwords and key_manager are always forbidden.
func Phase1SyncPlan() SyncPlan {
	return SyncPlan{InputMethod: true, LearningData: true}
}

func (p SyncPlan) Validate() error {
	if p.Passwords || p.KeyManager {
		return ErrClassForbidden
	}
	return nil
}

// Allows reports whether class c is permitted by this sync plan.
func (p SyncPlan) Allows(c DataClass) bool {
	return p.allows(c)
}

// AllowedClasses returns the deterministic list of data classes enabled in p.
func (p SyncPlan) AllowedClasses() []DataClass {
	items := p.Items()
	out := make([]DataClass, 0, len(items))
	for _, item := range items {
		if item.Allowed {
			out = append(out, item.Class)
		}
	}
	return out
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
	case Templates:
		return p.Templates
	case Snippets:
		return p.Snippets
	case Glossary:
		return p.Glossary
	case ConfigBaseline:
		return p.ConfigBaseline
	case SensitiveVault:
		return p.SensitiveVault
	}
	return false
}
func (p SyncPlan) Items() []SyncItem {
	return []SyncItem{
		{Class: InputMethod, Label: "Input method", Allowed: p.InputMethod}, {Class: LearningData, Label: "Learning data", Allowed: p.LearningData}, {Class: ClipboardText, Label: "Clipboard text", Allowed: p.ClipboardText}, {Class: Media, Label: "Media", Allowed: p.Media}, {Class: ApplicationCtx, Label: "Application context", Allowed: p.ApplicationCtx}, {Class: Templates, Label: "Templates", Allowed: p.Templates}, {Class: Snippets, Label: "Snippets", Allowed: p.Snippets}, {Class: Glossary, Label: "Glossary", Allowed: p.Glossary}, {Class: ConfigBaseline, Label: "Config baseline", Allowed: p.ConfigBaseline}, {Class: SensitiveVault, Label: "Sensitive vault", Sensitive: true, Allowed: p.SensitiveVault}, {Class: Passwords, Label: "Passwords (never synchronized)", Sensitive: true}, {Class: KeyManager, Label: "Key manager (never synchronized)", Sensitive: true},
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
	KeyID           string    `json:"keyId"`
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
	b := make([]byte, 0, len(envelopeDomain)+len(e.KeyID)+len(e.AccountID)+len(e.DeviceID)+len(e.ObjectID)+len(e.Class)+32)
	addString := func(value string) {
		b = binary.AppendUvarint(b, uint64(len(value)))
		b = append(b, value...)
	}
	addString(envelopeDomain)
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], e.ProtocolVersion)
	b = append(b, version[:]...)
	addString(e.KeyID)
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
	b := make([]byte, 0, len(envelopeDomain)+len(e.KeyID)+len(e.AccountID)+len(e.DeviceID)+len(e.ObjectID)+len(e.Class)+96)
	addString := func(value string) {
		b = binary.AppendUvarint(b, uint64(len(value)))
		b = append(b, value...)
	}
	addString(envelopeDomain)
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], e.ProtocolVersion)
	b = append(b, version[:]...)
	addString(e.KeyID)
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
func knownClass(c DataClass) bool {
	switch c {
	case InputMethod, LearningData, ClipboardText, Media, ApplicationCtx, Templates, Snippets, Glossary, ConfigBaseline, SensitiveVault:
		return true
	}
	return false
}

// validClass validates the wire boundary. Unknown non-secret classes are
// intentionally accepted so an older client can retain protocol framing and
// skip a newer class without crashing or persisting it.
func validClass(c DataClass) bool {
	return strings.TrimSpace(string(c)) != "" && c != Passwords && c != KeyManager
}
func marshalEnvelope(e EncryptedEnvelope) ([]byte, error) {
	if e.ProtocolVersion != ProtocolVersion || !IsValidADKKeyID(e.KeyID) || e.AccountID == "" || e.DeviceID == "" || e.ObjectID == "" || e.Version == 0 || !validClass(e.Class) || len(e.Nonce) != 12 || len(e.Ciphertext) < 16 || len(e.Signature) != ed25519.SignatureSize {
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
	mu                sync.Mutex
	accounts          map[string]map[string]ed25519.PublicKey
	x25519Accounts    map[string]map[string][]byte
	wrappedADK        map[string]map[string][]byte
	wrappedADKKeyID   map[string]map[string]string
	accountADKVersion map[string]uint64
	revokedDevices    map[string]map[string]struct{}
	recoverySalt      map[string][]byte
	recoveryWrap      map[string][]byte
	credentials       map[string][32]byte
	accountSequences  map[string]uint64
	tombstones        map[string]map[string]Tombstone
	objects           map[string]map[string][]byte
	versions          map[string]map[string]uint64
	watermarks        map[string]map[string]uint64
	issued            map[string]map[string]tombstoneBatch
	pairingNonces     map[string]map[string]struct{}
	tombstoneLog      map[string]map[uint64]Tombstone
	sequence          uint64
	audit             []AuditEvent
}

func NewServer() *Server {
	return &Server{
		accounts:          map[string]map[string]ed25519.PublicKey{},
		x25519Accounts:    map[string]map[string][]byte{},
		wrappedADK:        map[string]map[string][]byte{},
		wrappedADKKeyID:   map[string]map[string]string{},
		accountADKVersion: map[string]uint64{},
		revokedDevices:    map[string]map[string]struct{}{},
		recoverySalt:      map[string][]byte{},
		recoveryWrap:      map[string][]byte{},
		credentials:       map[string][32]byte{},
		accountSequences:  map[string]uint64{},
		tombstones:        map[string]map[string]Tombstone{},
		tombstoneLog:      map[string]map[uint64]Tombstone{},
		objects:           map[string]map[string][]byte{},
		versions:          map[string]map[string]uint64{},
		watermarks:        map[string]map[string]uint64{},
		issued:            map[string]map[string]tombstoneBatch{},
		pairingNonces:     map[string]map[string]struct{}{},
	}
}
func (s *Server) initAccountLocked(a string) {
	if s.recoverySalt[a] == nil {
		s.recoverySalt[a] = make([]byte, 32)
		_, _ = io.ReadFull(rand.Reader, s.recoverySalt[a])
	}
	if s.accounts[a] == nil {
		s.accounts[a] = map[string]ed25519.PublicKey{}
	}
	if s.x25519Accounts[a] == nil {
		s.x25519Accounts[a] = map[string][]byte{}
	}
	if s.wrappedADK[a] == nil {
		s.wrappedADK[a] = map[string][]byte{}
	}
	if s.wrappedADKKeyID[a] == nil {
		s.wrappedADKKeyID[a] = map[string]string{}
	}
	if s.accountADKVersion[a] == 0 {
		s.accountADKVersion[a] = 1
	}
	if s.revokedDevices[a] == nil {
		s.revokedDevices[a] = map[string]struct{}{}
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
	if s.pairingNonces[a] == nil {
		s.pairingNonces[a] = map[string]struct{}{}
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
	if _, revoked := s.revokedDevices[account][device]; revoked {
		return ErrStoreDeviceRevoked
	}
	if old, exists := s.accounts[account][device]; exists && !hmac.Equal(old, pub) {
		return errors.New("sync: device key cannot be overwritten")
	}
	s.accounts[account][device] = append(ed25519.PublicKey(nil), pub...)
	return nil
}
func (s *Server) PairDeviceWithCredential(account, device string, pub ed25519.PublicKey, credential []byte) error {
	return s.pairDevice(account, device, pub, credential)
}

// PairDeviceWithTicket consumes a signed, time-limited authorization ticket.
// The ticket authorizer must already be paired, and the nonce is one-shot.
func (s *Server) PairDeviceWithTicket(ticket PairingTicket) error {
	if err := ticket.Verify(time.Now().UTC()); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !bytes.Equal(ticket.AuthorizerPubKey, s.accounts[ticket.Account][ticket.Authorizer]) {
		return ErrPairingTicket
	}
	if _, used := s.pairingNonces[ticket.Account][ticket.Nonce]; used {
		return ErrPairingCodeUsed
	}
	if s.accounts[ticket.Account][ticket.Authorizer] == nil {
		return ErrStoreDeviceMissing
	}
	if _, revoked := s.revokedDevices[ticket.Account][ticket.Device]; revoked {
		return ErrStoreDeviceRevoked
	}
	if old, exists := s.accounts[ticket.Account][ticket.Device]; exists && !bytes.Equal(old, ticket.PublicKey) {
		return ErrStoreDeviceKey
	}
	if len(ticket.X25519PublicKey) != 0 && len(ticket.X25519PublicKey) != 32 {
		return ErrPairingTicket
	}
	if len(ticket.WrappedADK) != 0 && len(ticket.WrappedADK) < 60 {
		return ErrPairingTicket
	}
	s.initAccountLocked(ticket.Account)
	s.accounts[ticket.Account][ticket.Device] = append(ed25519.PublicKey(nil), ticket.PublicKey...)
	if len(ticket.X25519PublicKey) == 32 {
		s.x25519Accounts[ticket.Account][ticket.Device] = append([]byte(nil), ticket.X25519PublicKey...)
	}
	if len(ticket.WrappedADK) >= 60 {
		keyID := ticket.ADKKeyID
		if !IsValidADKKeyID(keyID) {
			keyID = DefaultADKKeyID
		}
		s.wrappedADK[ticket.Account][ticket.Device] = append([]byte(nil), ticket.WrappedADK...)
		s.wrappedADKKeyID[ticket.Account][ticket.Device] = keyID
		if ver, ok := ADKVersion(keyID); ok && ver > s.accountADKVersion[ticket.Account] {
			s.accountADKVersion[ticket.Account] = ver
		}
	}
	s.pairingNonces[ticket.Account][ticket.Nonce] = struct{}{}
	return nil
}

// RemoveDevice revokes a paired device after credential validation. It removes
// only server-side public keys and wrapped material; the caller must also call
// DeleteDeviceIdentity locally to prevent reappearance after restart.
func (s *Server) RemoveDevice(account, device string, credential []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.credentials[account]
	if !ok || len(credential) != 32 || !hmac.Equal(c[:], credential) {
		return errors.New("sync: invalid account credential")
	}
	if s.accounts[account][device] == nil {
		return errors.New("sync: device is not paired")
	}
	s.initAccountLocked(account)
	s.revokedDevices[account][device] = struct{}{}
	delete(s.accounts[account], device)
	delete(s.x25519Accounts[account], device)
	delete(s.wrappedADK[account], device)
	delete(s.wrappedADKKeyID[account], device)
	delete(s.watermarks[account], device)
	delete(s.issued[account], device)
	return nil
}

func (s *Server) SetX25519PublicKey(account, device string, pub, credential []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.credentials[account]
	if !ok || len(credential) != 32 || !hmac.Equal(c[:], credential) {
		return errors.New("sync: invalid account credential")
	}
	if len(pub) != 32 || s.accounts[account][device] == nil {
		return errors.New("sync: device is not paired")
	}
	s.initAccountLocked(account)
	s.x25519Accounts[account][device] = append([]byte(nil), pub...)
	return nil
}

func (s *Server) X25519PublicKey(account, device string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, revoked := s.revokedDevices[account][device]; revoked {
		return nil, ErrStoreDeviceRevoked
	}
	pub := s.x25519Accounts[account][device]
	if len(pub) != 32 {
		return nil, errors.New("sync: x25519 key is not registered")
	}
	return append([]byte(nil), pub...), nil
}

func WrappedADKCanonical(account, sender, target string, wrapped []byte) []byte {
	h := sha256.Sum256(wrapped)
	return append([]byte("clavis.sync.wrapped-adk.v2\x00"+account+"\x00"+sender+"\x00"+target+"\x00"), h[:]...)
}
func WrappedADKEntryCanonical(account, sender, target, keyID string, wrapped []byte) []byte {
	h := sha256.Sum256(wrapped)
	return append([]byte("clavis.sync.wrapped-adk-entry.v2\x00"+account+"\x00"+sender+"\x00"+target+"\x00"+keyID+"\x00"), h[:]...)
}
func WrappedADKRequestCanonical(account, device string) []byte {
	return []byte("clavis.sync.get-wrapped-adk.v2\x00" + account + "\x00" + device)
}
func wrappedADKCanonical(account, sender, target string, wrapped []byte) []byte {
	return WrappedADKCanonical(account, sender, target, wrapped)
}
func wrappedADKRequestCanonical(account, device string) []byte {
	return WrappedADKRequestCanonical(account, device)
}
func (s *Server) SetWrappedADK(account, device string, wrapped, signature []byte) error {
	return s.SetWrappedADKForDevice(account, device, device, wrapped, signature)
}
func (s *Server) SetWrappedADKForDevice(account, sender, target string, wrapped, signature []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := s.accounts[account][sender]
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, WrappedADKCanonical(account, sender, target, wrapped), signature) {
		return errors.New("sync: invalid wrapped ADK signature")
	}
	if len(wrapped) < 32 || s.accounts[account][target] == nil {
		return errors.New("sync: wrapped ADK target is not paired")
	}
	s.initAccountLocked(account)
	s.wrappedADK[account][target] = append([]byte(nil), wrapped...)
	if s.wrappedADKKeyID[account][target] == "" {
		s.wrappedADKKeyID[account][target] = DefaultADKKeyID
	}
	return nil
}
func (s *Server) SetWrappedADKEntryForDevice(account, sender, target, keyID string, wrapped, signature []byte) error {
	if !IsValidADKKeyID(keyID) {
		return errors.New("sync: invalid ADK key id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := s.accounts[account][sender]
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, WrappedADKEntryCanonical(account, sender, target, keyID, wrapped), signature) {
		return errors.New("sync: invalid wrapped ADK signature")
	}
	if len(wrapped) < 32 || s.accounts[account][target] == nil {
		return errors.New("sync: wrapped ADK target is not paired")
	}
	s.initAccountLocked(account)
	s.wrappedADK[account][target] = append([]byte(nil), wrapped...)
	s.wrappedADKKeyID[account][target] = keyID
	if ver, ok := ADKVersion(keyID); ok && ver > s.accountADKVersion[account] {
		s.accountADKVersion[account] = ver
	}
	return nil
}
func (s *Server) GetWrappedADK(account, device string, signature []byte) ([]byte, error) {
	entry, err := s.GetWrappedADKEntry(account, device, signature)
	if err != nil {
		return nil, err
	}
	return entry.Wrapped, nil
}
func (s *Server) GetWrappedADKEntry(account, device string, signature []byte) (WrappedADKEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, revoked := s.revokedDevices[account][device]; revoked {
		return WrappedADKEntry{}, ErrStoreDeviceRevoked
	}
	pub := s.accounts[account][device]
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, WrappedADKRequestCanonical(account, device), signature) {
		return WrappedADKEntry{}, errors.New("sync: invalid wrapped ADK request")
	}
	wrapped := s.wrappedADK[account][device]
	if len(wrapped) == 0 {
		return WrappedADKEntry{}, errors.New("sync: wrapped ADK is not available")
	}
	keyID := s.wrappedADKKeyID[account][device]
	if !IsValidADKKeyID(keyID) {
		keyID = DefaultADKKeyID
	}
	return WrappedADKEntry{KeyID: keyID, Wrapped: append([]byte(nil), wrapped...)}, nil
}
func (s *Server) ListActiveDevices(account string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.credentials[account]; !ok {
		return nil, ErrStoreAccountMissing
	}
	out := make([]string, 0, len(s.accounts[account]))
	for device := range s.accounts[account] {
		out = append(out, device)
	}
	sort.Strings(out)
	return out, nil
}
func (s *Server) GetRecoverySalt(account, device string, signature []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := s.accounts[account][device]
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, WrappedADKRequestCanonical(account, device), signature) {
		return nil, errors.New("sync: invalid recovery salt request")
	}
	return append([]byte(nil), s.recoverySalt[account]...), nil
}
func RecoveryWrapCanonical(account, device string, wrapped []byte) []byte {
	h := sha256.Sum256(wrapped)
	return append([]byte("clavis.sync.recovery-wrap.v2\x00"+account+"\x00"+device+"\x00"), h[:]...)
}
func recoveryWrapCanonical(account, device string, wrapped []byte) []byte {
	return RecoveryWrapCanonical(account, device, wrapped)
}
func (s *Server) SetRecoveryWrap(account, device string, wrapped, signature []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := s.accounts[account][device]
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, RecoveryWrapCanonical(account, device, wrapped), signature) {
		return errors.New("sync: invalid recovery wrap signature")
	}
	s.recoveryWrap[account] = append([]byte(nil), wrapped...)
	return nil
}
func (s *Server) GetRecoveryWrap(account, device string, signature []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := s.accounts[account][device]
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, WrappedADKRequestCanonical(account, device), signature) {
		return nil, errors.New("sync: invalid recovery wrap request")
	}
	if len(s.recoveryWrap[account]) == 0 {
		return nil, errors.New("sync: recovery wrap is not available")
	}
	return append([]byte(nil), s.recoveryWrap[account]...), nil
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
	if _, revoked := s.revokedDevices[account][device]; revoked {
		return nil, ErrStoreDeviceRevoked
	}
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
	if _, revoked := s.revokedDevices[account][device]; revoked {
		return ErrStoreDeviceRevoked
	}
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
	// Verify against the append-only log rather than the current object index.
	// The current index intentionally contains only the latest tombstone for an
	// object, while a device may still need to acknowledge an older batch.
	stored, ok := s.tombstoneLog[t.AccountID][t.Sequence]
	if !ok || t.AccountID == "" || t.ObjectID == "" || t.Version == 0 || t.Sequence == 0 {
		return false
	}
	return stored.AccountID == t.AccountID &&
		stored.ObjectID == t.ObjectID &&
		stored.Sequence == t.Sequence &&
		stored.Version == t.Version &&
		stored.DeviceID == t.DeviceID &&
		stored.DeletedAt.UTC().UnixNano() == t.DeletedAt.UTC().UnixNano() &&
		hmac.Equal(stored.Signature, t.Signature) &&
		t.Verify(s.accounts[t.AccountID][t.DeviceID])
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
	if _, revoked := s.revokedDevices[account][device]; revoked {
		return ErrStoreDeviceRevoked
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
		return ErrStoreVersion
	}
	encoded, err := marshalEnvelope(e)
	if err != nil {
		return err
	}
	s.objects[account][object] = encoded
	s.versions[account][object] = version
	return nil
}
func (s *Server) ListObjects(account string) ([]ObjectSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.credentials[account]; !ok {
		return nil, ErrStoreAccountMissing
	}
	out := make([]ObjectSummary, 0, len(s.objects[account]))
	for id, raw := range s.objects[account] {
		env, err := parseEnvelope(raw)
		if err != nil {
			continue
		}
		out = append(out, ObjectSummary{
			ObjectID: id,
			Version:  env.Version,
			Class:    env.Class,
			DeviceID: env.DeviceID,
			KeyID:    env.KeyID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectID < out[j].ObjectID })
	return out, nil
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

type PulledObject struct {
	ObjectID string
	Version  uint64
	Class    DataClass
	Payload  []byte
}

type ConflictEntry struct {
	ObjectID      string
	ConflictID    string
	LocalVersion  uint64
	RemoteVersion uint64
	LocalPayload  []byte
	RemotePayload []byte
}

type PullResult struct {
	Objects   []PulledObject
	Conflicts []ConflictEntry
}

// SyncDiagnostic records a recoverable protocol event that must remain
// observable without turning a whole pull batch into a failure.
type SyncDiagnostic struct {
	Kind     string
	Class    DataClass
	ObjectID string
	Message  string
}

type localObject struct {
	version     uint64
	baseVersion uint64
	basePayload []byte
	payload     []byte
	dirty       bool
	class       DataClass
}

// LocalObjectState exposes a read-only copy of a tracked local object.
type LocalObjectState struct {
	ObjectID    string
	Version     uint64
	BaseVersion uint64
	Class       DataClass
	Payload     []byte
	Dirty       bool
}

type Client struct {
	mu                  sync.Mutex
	syncMu              sync.Mutex
	server              *Server
	store               SyncStore
	accountID, deviceID string
	credential          []byte
	privateKey          ed25519.PrivateKey
	xPrivateKey         *ecdh.PrivateKey
	adk                 []byte
	adkKeyID            string
	adkHistory          map[string][]byte
	deviceSalt          []byte
	recoveryWrap        []byte
	stateStore          StateStore
	keyStore            SecretStore
	cache               *LocalCache
	watermark           uint64
	syncEnabled         bool
	recoveryReady       bool
	plan                SyncPlan
	vault               *Vault
	localObjects        map[string]localObject
	conflicts           []ConflictEntry
	diagnostics         []SyncDiagnostic
}

func newClient(s *Server, device string, identity DeviceIdentity) (*Client, error) {
	if s == nil || strings.TrimSpace(device) == "" {
		return nil, errors.New("sync: server and device id are required")
	}
	if len(identity.Ed25519Private) != ed25519.PrivateKeySize || identity.X25519Private == nil || len(identity.Credential) != 32 || len(identity.ADK) != 32 {
		return nil, errors.New("sync: incomplete device identity")
	}
	v, err := NewVaultWithError("")
	if err != nil {
		return nil, err
	}
	store, err := NewServerStore(s)
	if err != nil {
		return nil, err
	}
	adkCopy := append([]byte(nil), identity.ADK...)
	return &Client{
		server:       s,
		store:        store,
		deviceID:     device,
		privateKey:   append(ed25519.PrivateKey(nil), identity.Ed25519Private...),
		xPrivateKey:  identity.X25519Private,
		adk:          adkCopy,
		adkKeyID:     DefaultADKKeyID,
		adkHistory:   map[string][]byte{DefaultADKKeyID: append([]byte(nil), adkCopy...)},
		credential:   append([]byte(nil), identity.Credential...),
		cache:        NewLocalCache(),
		plan:         DefaultSyncPlan(),
		vault:        v,
		localObjects: make(map[string]localObject),
	}, nil
}

// NewClient retains the in-memory constructor for callers that do not yet have
// an OS keystore. Persistent callers should use NewClientWithIdentity.
func NewClient(s *Server, device string) (*Client, error) {
	identity, err := newDeviceIdentity()
	if err != nil {
		return nil, err
	}
	return newClient(s, device, identity)
}
func NewClientWithIdentity(s *Server, device string, identity DeviceIdentity) (*Client, error) {
	return newClient(s, device, identity)
}

// NewClientWithStore uses the durable encrypted-object store for all sync data
// operations. The store is the protocol boundary; no plaintext is passed to it.
func NewClientWithStore(store SyncStore, device string, identity DeviceIdentity) (*Client, error) {
	if store == nil {
		return nil, errors.New("sync: store is required")
	}
	if strings.TrimSpace(device) == "" {
		return nil, errors.New("sync: device id is required")
	}
	if len(identity.Ed25519Private) != ed25519.PrivateKeySize || identity.X25519Private == nil || len(identity.ADK) != 32 {
		return nil, errors.New("sync: incomplete device identity")
	}
	v, err := NewVaultWithError("")
	if err != nil {
		return nil, err
	}
	adkCopy := append([]byte(nil), identity.ADK...)
	var credCopy []byte
	if len(identity.Credential) == 32 {
		credCopy = append([]byte(nil), identity.Credential...)
	}
	return &Client{
		store:        store,
		deviceID:     device,
		privateKey:   append(ed25519.PrivateKey(nil), identity.Ed25519Private...),
		xPrivateKey:  identity.X25519Private,
		adk:          adkCopy,
		adkKeyID:     DefaultADKKeyID,
		adkHistory:   map[string][]byte{DefaultADKKeyID: append([]byte(nil), adkCopy...)},
		credential:   credCopy,
		cache:        NewLocalCache(),
		plan:         DefaultSyncPlan(),
		vault:        v,
		localObjects: make(map[string]localObject),
	}, nil
}

// NewPersistentClientWithStore loads device identity from keyStore and
// non-secret state from state, backed by any durable SyncStore.
func NewPersistentClientWithStore(ctx context.Context, store SyncStore, device string, keyStore SecretStore, state StateStore) (*Client, error) {
	identity, err := LoadOrCreateDeviceIdentity(keyStore)
	if err != nil {
		return nil, err
	}
	c, err := NewClientWithStore(store, device, identity)
	if err != nil {
		return nil, err
	}
	c.keyStore = keyStore
	if state != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		w, ready, err := state.Load(ctx)
		if err != nil {
			return nil, err
		}
		c.watermark, c.recoveryReady, c.syncEnabled, c.stateStore = w, ready, ready, state
	}
	return c, nil
}

func (c *Client) SetStateStore(ctx context.Context, state StateStore) error {
	if state == nil {
		c.mu.Lock()
		c.stateStore = nil
		c.mu.Unlock()
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w, ready, err := state.Load(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.stateStore = state
	if w > c.watermark {
		c.watermark = w
	}
	if ready {
		c.recoveryReady = true
		c.syncEnabled = true
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) AccountID() string { c.mu.Lock(); defer c.mu.Unlock(); return c.accountID }
func (c *Client) DeviceID() string  { c.mu.Lock(); defer c.mu.Unlock(); return c.deviceID }
func (c *Client) DevicePublicKey() ed25519.PublicKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.privateKey) != ed25519.PrivateKeySize {
		return nil
	}
	return append(ed25519.PublicKey(nil), c.privateKey.Public().(ed25519.PublicKey)...)
}
func (c *Client) X25519PublicKey() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.xPrivateKey == nil {
		return nil
	}
	return append([]byte(nil), c.xPrivateKey.PublicKey().Bytes()...)
}
func (c *Client) ADKKeyID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentADKKeyIDLocked()
}

// SignControlPayload signs a transport control record using the device's
// Ed25519 private key without exposing key material to callers.
func (c *Client) SignControlPayload(canonical []byte) ([]byte, error) {
	c.mu.Lock()
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	c.mu.Unlock()
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("sync: signing key is not initialized")
	}
	return ed25519.Sign(priv, canonical), nil
}

// BuildSignedWrappedADKEntry wraps the client's current ADK for targetDevice
// using ECDH with targetX25519Pub and signs the entry with the sender's Ed25519 key.
func (c *Client) BuildSignedWrappedADKEntry(targetDevice string, targetX25519Pub []byte) (keyID string, wrapped, signature []byte, err error) {
	c.mu.Lock()
	account := c.accountID
	sender := c.deviceID
	adk := append([]byte(nil), c.adk...)
	keyID = c.currentADKKeyIDLocked()
	xpriv := c.xPrivateKey
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	c.mu.Unlock()
	if account == "" || sender == "" || targetDevice == "" {
		return "", nil, nil, errors.New("sync: account, sender, and target device are required")
	}
	if len(adk) != 32 || xpriv == nil || len(priv) != ed25519.PrivateKeySize {
		return "", nil, nil, errors.New("sync: client keys are not initialized")
	}
	pub, err := ecdh.X25519().NewPublicKey(targetX25519Pub)
	if err != nil {
		return "", nil, nil, err
	}
	shared, err := xpriv.ECDH(pub)
	if err != nil {
		return "", nil, nil, err
	}
	sealed, err := wrapWithSharedKey(shared, adk)
	if err != nil {
		return "", nil, nil, err
	}
	wrapped = append(append([]byte(nil), xpriv.PublicKey().Bytes()...), sealed...)
	signature = ed25519.Sign(priv, WrappedADKEntryCanonical(account, sender, targetDevice, keyID, wrapped))
	return keyID, wrapped, signature, nil
}

// SetRecoveryMaterial caches verified recovery salt and recovery-wrapped ADK
// ciphertext fetched from a transport before ConfirmRecoveryCode is invoked.
func (c *Client) SetRecoveryMaterial(salt, wrapped []byte) {
	if len(salt) != 32 || len(wrapped) == 0 {
		return
	}
	c.mu.Lock()
	c.deviceSalt = append([]byte(nil), salt...)
	c.recoveryWrap = append([]byte(nil), wrapped...)
	c.mu.Unlock()
}

// DecryptVerifiedEnvelope decrypts an envelope whose Ed25519 signature has
// already been verified using the client's ADK keyring.
func (c *Client) DecryptVerifiedEnvelope(envelope EncryptedEnvelope, pub ed25519.PublicKey) ([]byte, error) {
	return c.decryptEnvelopeWithKeyring(envelope, pub)
}
func (c *Client) currentADKKeyIDLocked() string {
	if !IsValidADKKeyID(c.adkKeyID) {
		return DefaultADKKeyID
	}
	return c.adkKeyID
}
func (c *Client) adkForKeyIDLocked(keyID string) ([]byte, bool) {
	if keyID == c.currentADKKeyIDLocked() && len(c.adk) == 32 {
		return append([]byte(nil), c.adk...), true
	}
	if key, ok := c.adkHistory[keyID]; ok && len(key) == 32 {
		return append([]byte(nil), key...), true
	}
	return nil, false
}

func (c *Client) syncStore() (SyncStore, error) {
	c.mu.Lock()
	store := c.store
	c.mu.Unlock()
	if store == nil {
		return nil, errors.New("sync: store is not configured")
	}
	return store, nil
}

// NewPersistentClient loads identity from the keystore and non-secret state
// from SQLite. It does not pair or write any secret to SQLite.
func NewPersistentClient(ctx context.Context, s *Server, device string, keyStore SecretStore, state StateStore) (*Client, error) {
	identity, err := LoadOrCreateDeviceIdentity(keyStore)
	if err != nil {
		return nil, err
	}
	c, err := newClient(s, device, identity)
	if err != nil {
		return nil, err
	}
	if state != nil {
		c.keyStore = keyStore
		w, ready, err := state.Load(ctx)
		if err != nil {
			return nil, err
		}
		c.watermark, c.recoveryReady, c.syncEnabled, c.stateStore = w, ready, ready, state
	}
	return c, nil
}

// LoginWithStoredCredential resumes an account using the credential loaded in
// DeviceIdentity, without generating a new device identity or requiring a
// second pairing operation.
func (c *Client) LoginWithStoredCredential(account string) error {
	c.mu.Lock()
	credential := append([]byte(nil), c.credential...)
	c.mu.Unlock()
	return c.LoginWithCredential(account, credential)
}

func (c *Client) saveState() error {
	c.mu.Lock()
	state, watermark, ready := c.stateStore, c.watermark, c.recoveryReady
	c.mu.Unlock()
	if state == nil {
		return nil
	}
	return state.Save(context.Background(), watermark, ready)
}
func (c *Client) Login(account string) error {
	store, err := c.syncStore()
	if err != nil {
		return err
	}
	// Login may register a new account, but deliberately cannot recover an
	// existing account's credential from the store.
	cred, err := store.RegisterAccount(account)
	if err != nil {
		return err
	}
	return c.loginWithCredential(account, cred)
}
func (c *Client) LoginWithCredential(account string, credential []byte) error {
	store, err := c.syncStore()
	if err != nil {
		return err
	}
	if err := store.LoginWithCredential(account, credential); err != nil {
		return err
	}
	return c.loginWithCredential(account, credential)
}
func (c *Client) loginWithCredential(account string, credential []byte) error {
	c.mu.Lock()
	c.accountID = account
	c.credential = append([]byte(nil), credential...)
	store := c.keyStore
	c.mu.Unlock()
	if store != nil {
		if err := store.SaveKey(accountCredentialKey, encodeSecret(credential)); err != nil {
			return err
		}
	}
	return nil
}
func hkdfKey(secret, salt []byte, info string) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, salt, info, 32)
}

func wrapWithSharedKey(shared, plaintext []byte) ([]byte, error) {
	key, err := hkdfKey(shared, nil, wrapInfo)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}
func unwrapWithSharedKey(shared, wrapped []byte) ([]byte, error) {
	key, err := hkdfKey(shared, nil, wrapInfo)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(wrapped) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("sync: invalid wrapped ADK")
	}
	return gcm.Open(nil, wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():], nil)
}

type wrappedADKReader interface {
	GetWrappedADK(account, device string, signature []byte) ([]byte, error)
}

func (c *Client) ensureADK() error {
	c.mu.Lock()
	ready := len(c.adk) == 32
	c.mu.Unlock()
	if ready {
		return nil
	}
	return c.RefreshADK()
}

func (c *Client) publishWrappedADK() error {
	c.mu.Lock()
	a, d := c.accountID, c.deviceID
	adk := append([]byte(nil), c.adk...)
	keyID := c.currentADKKeyIDLocked()
	xpriv := c.xPrivateKey
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	store := c.store
	c.mu.Unlock()
	if store == nil {
		return errors.New("sync: store is not configured")
	}
	adkStore, ok := store.(ADKStore)
	if !ok {
		return errors.New("sync: wrapped ADK is unsupported by this store")
	}
	if a == "" || d == "" || xpriv == nil || len(priv) != ed25519.PrivateKeySize || len(adk) != 32 {
		return errors.New("sync: paired device and ADK are required to publish wrapped ADK")
	}
	shared, err := xpriv.ECDH(xpriv.PublicKey())
	if err != nil {
		return err
	}
	wrapped, err := wrapWithSharedKey(shared, adk)
	if err != nil {
		return err
	}
	wrapped = append(append([]byte(nil), xpriv.PublicKey().Bytes()...), wrapped...)
	return adkStore.SetWrappedADKEntryForDevice(a, d, d, keyID, wrapped, ed25519.Sign(priv, WrappedADKEntryCanonical(a, d, d, keyID, wrapped)))
}

// RefreshADK fetches the device-specific wrapped account key and replaces the
// in-memory ADK only after authentication and AEAD verification succeed.
func (c *Client) RefreshADK() error {
	c.mu.Lock()
	account, device, store := c.accountID, c.deviceID, c.store
	xpriv, priv := c.xPrivateKey, c.privateKey
	keyStore := c.keyStore
	c.mu.Unlock()
	if account == "" || device == "" || xpriv == nil || len(priv) != ed25519.PrivateKeySize {
		return errors.New("sync: a paired device is required to refresh the ADK")
	}
	sig := ed25519.Sign(priv, WrappedADKRequestCanonical(account, device))
	var entry WrappedADKEntry
	if adkStore, ok := store.(ADKStore); ok {
		var err error
		entry, err = adkStore.GetWrappedADKEntry(account, device, sig)
		if err != nil {
			return err
		}
	} else if reader, ok := store.(wrappedADKReader); ok {
		wrapped, err := reader.GetWrappedADK(account, device, sig)
		if err != nil {
			return err
		}
		entry = WrappedADKEntry{KeyID: DefaultADKKeyID, Wrapped: wrapped}
	} else {
		return errors.New("sync: wrapped ADK is unsupported by this store")
	}
	if !IsValidADKKeyID(entry.KeyID) {
		entry.KeyID = DefaultADKKeyID
	}
	wrapped := entry.Wrapped
	if len(wrapped) < 32 {
		return errors.New("sync: wrapped ADK is incomplete")
	}
	publicKey, err := ecdh.X25519().NewPublicKey(wrapped[:32])
	if err != nil {
		return err
	}
	shared, err := xpriv.ECDH(publicKey)
	if err != nil {
		return err
	}
	adk, err := unwrapWithSharedKey(shared, wrapped[32:])
	if err != nil {
		return err
	}
	if len(adk) != 32 {
		return errors.New("sync: wrapped ADK has invalid key length")
	}
	c.mu.Lock()
	if c.accountID != account {
		c.mu.Unlock()
		return errors.New("sync: account changed during ADK refresh")
	}
	if c.adkHistory == nil {
		c.adkHistory = make(map[string][]byte)
	}
	if len(c.adk) == 32 {
		c.adkHistory[c.currentADKKeyIDLocked()] = append([]byte(nil), c.adk...)
	}
	c.adk = append([]byte(nil), adk...)
	c.adkKeyID = entry.KeyID
	c.adkHistory[entry.KeyID] = append([]byte(nil), adk...)
	c.mu.Unlock()
	if keyStore != nil {
		if err := keyStore.SaveKey(deviceADKKey, encodeSecret(adk)); err != nil {
			return err
		}
	}
	return nil
}
func (c *Client) ADK() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.adk...)
}

// WrapADKForDevice creates an authenticated-device-key package for another
// paired device. The server receives only this ciphertext.
func (c *Client) ShareADKWithDevice(device string) error { return c.WrapADKForDevice(device) }
func (c *Client) WrapADKForDevice(device string) error {
	c.mu.Lock()
	a, d := c.accountID, c.deviceID
	adk := append([]byte(nil), c.adk...)
	keyID := c.currentADKKeyIDLocked()
	xpriv := c.xPrivateKey
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	store := c.store
	c.mu.Unlock()
	if len(adk) != 32 {
		return errors.New("sync: ADK is not initialized")
	}
	if store == nil {
		return errors.New("sync: store is not configured")
	}
	adkStore, ok := store.(ADKStore)
	if !ok {
		return errors.New("sync: wrapped ADK is unsupported by this store")
	}
	pubBytes, err := adkStore.X25519PublicKey(a, device)
	if err != nil {
		return err
	}
	pub, err := ecdh.X25519().NewPublicKey(pubBytes)
	if err != nil {
		return err
	}
	shared, err := xpriv.ECDH(pub)
	if err != nil {
		return err
	}
	wrapped, err := wrapWithSharedKey(shared, adk)
	if err != nil {
		return err
	}
	wrapped = append(append([]byte(nil), xpriv.PublicKey().Bytes()...), wrapped...)
	return adkStore.SetWrappedADKEntryForDevice(a, d, device, keyID, wrapped, ed25519.Sign(priv, WrappedADKEntryCanonical(a, d, device, keyID, wrapped)))
}

func (c *Client) PairDevice(tickets ...PairingTicket) error {
	if len(tickets) == 0 {
		return ErrPairingTicket
	}
	if len(tickets) != 1 {
		return ErrPairingTicket
	}
	return c.PairDeviceWithTicket(tickets[0], tickets[0].ShortCode, PairingFingerprint(ed25519.PublicKey(tickets[0].AuthorizerPubKey)), time.Now().UTC())
}

// PairDeviceWithCredential is retained only for explicit first-device/bootstrap
// migration. New-device pairing must use PairDevice with a signed ticket.
func (c *Client) PairDeviceWithCredential() error {
	c.mu.Lock()
	a, d := c.accountID, c.deviceID
	pub := append(ed25519.PublicKey(nil), c.privateKey.Public().(ed25519.PublicKey)...)
	xpub := append([]byte(nil), c.xPrivateKey.PublicKey().Bytes()...)
	cred := append([]byte(nil), c.credential...)
	store := c.store
	c.mu.Unlock()
	if a == "" {
		return errors.New("sync: login is required")
	}
	if store == nil {
		return errors.New("sync: store is not configured")
	}
	if err := store.PairDeviceWithCredential(a, d, pub, cred); err != nil {
		return err
	}
	adkStore, ok := store.(ADKStore)
	if !ok {
		if len(c.ADK()) != 32 {
			return errors.New("sync: ADK is not initialized")
		}
		return nil
	}
	if err := adkStore.SetX25519PublicKey(a, d, xpub, cred); err != nil {
		return err
	}
	if err := c.ensureADK(); err != nil {
		return err
	}
	return c.publishWrappedADK()
}
func conflictObjectID(object, device string, version uint64) string {
	sum := sha256.Sum256([]byte(object + "\x00" + device + "\x00" + fmt.Sprint(version)))
	return object + ".conflict." + hex.EncodeToString(sum[:8])
}

func (c *Client) recordConflict(conflict ConflictEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conflicts = append(c.conflicts, ConflictEntry{ObjectID: conflict.ObjectID, ConflictID: conflict.ConflictID, LocalVersion: conflict.LocalVersion, RemoteVersion: conflict.RemoteVersion, LocalPayload: append([]byte(nil), conflict.LocalPayload...), RemotePayload: append([]byte(nil), conflict.RemotePayload...)})
}

func (c *Client) recordDiagnostic(diagnostic SyncDiagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.diagnostics = append(c.diagnostics, diagnostic)
}

// Diagnostics returns a copy of recoverable sync diagnostics, including
// unsupported classes skipped during pull. It never includes payload contents.
func (c *Client) Diagnostics() []SyncDiagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SyncDiagnostic(nil), c.diagnostics...)
}

func (c *Client) Conflicts() []ConflictEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ConflictEntry, len(c.conflicts))
	for i := range c.conflicts {
		out[i] = c.conflicts[i]
		out[i].LocalPayload = append([]byte(nil), c.conflicts[i].LocalPayload...)
		out[i].RemotePayload = append([]byte(nil), c.conflicts[i].RemotePayload...)
	}
	return out
}

// StageLocalEdit records an uncommitted local change on object so a subsequent
// PullObjects detects true concurrent divergence instead of overwriting it.
func (c *Client) StageLocalEdit(class DataClass, object string, payload []byte) error {
	if strings.TrimSpace(object) == "" {
		return errors.New("sync: object id cannot be empty")
	}
	if class == Passwords || class == KeyManager {
		return ErrClassForbidden
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.localObjects == nil {
		c.localObjects = make(map[string]localObject)
	}
	existing, hasExisting := c.localObjects[object]
	if hasExisting {
		nextVer := existing.version
		if existing.baseVersion >= nextVer {
			nextVer = existing.baseVersion + 1
		}
		c.localObjects[object] = localObject{
			version:     nextVer,
			baseVersion: existing.baseVersion,
			basePayload: bytes.Clone(existing.basePayload),
			payload:     bytes.Clone(payload),
			dirty:       !bytes.Equal(payload, existing.basePayload),
			class:       class,
		}
		return nil
	}
	c.localObjects[object] = localObject{
		version:     1,
		baseVersion: 0,
		payload:     bytes.Clone(payload),
		dirty:       true,
		class:       class,
	}
	return nil
}

// LocalObject returns a snapshot of the client's tracked local object state.
func (c *Client) LocalObject(object string) (LocalObjectState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lo, ok := c.localObjects[object]
	if !ok {
		return LocalObjectState{}, false
	}
	return LocalObjectState{
		ObjectID:    object,
		Version:     lo.version,
		BaseVersion: lo.baseVersion,
		Class:       lo.class,
		Payload:     bytes.Clone(lo.payload),
		Dirty:       lo.dirty,
	}, true
}

// RecordRemoteConflict records an explicit conflict observed by an external
// transport (for example two devices writing the same object version into a
// shared sync directory) without losing local payload state.
func (c *Client) RecordRemoteConflict(remote EncryptedEnvelope, remotePayload []byte) ConflictEntry {
	c.mu.Lock()
	local := c.localObjects[remote.ObjectID]
	localVer := local.version
	localPayload := bytes.Clone(local.payload)
	if c.localObjects == nil {
		c.localObjects = make(map[string]localObject)
	}
	c.localObjects[remote.ObjectID] = localObject{
		version:     localVer,
		baseVersion: remote.Version,
		basePayload: bytes.Clone(remotePayload),
		payload:     bytes.Clone(localPayload),
		dirty:       true,
		class:       remote.Class,
	}
	conflictID := conflictObjectID(remote.ObjectID, remote.DeviceID, remote.Version)
	conflict := ConflictEntry{
		ObjectID:      remote.ObjectID,
		ConflictID:    conflictID,
		LocalVersion:  localVer,
		RemoteVersion: remote.Version,
		LocalPayload:  localPayload,
		RemotePayload: bytes.Clone(remotePayload),
	}
	for _, existing := range c.conflicts {
		if existing.ObjectID == conflict.ObjectID && existing.ConflictID == conflict.ConflictID {
			c.mu.Unlock()
			return existing
		}
	}
	c.conflicts = append(c.conflicts, ConflictEntry{
		ObjectID:      conflict.ObjectID,
		ConflictID:    conflict.ConflictID,
		LocalVersion:  conflict.LocalVersion,
		RemoteVersion: conflict.RemoteVersion,
		LocalPayload:  bytes.Clone(conflict.LocalPayload),
		RemotePayload: bytes.Clone(conflict.RemotePayload),
	})
	c.mu.Unlock()
	return conflict
}

// ListObjects returns metadata summaries for all live encrypted objects stored
// for the logged-in account.
func (c *Client) ListObjects() ([]ObjectSummary, error) {
	store, err := c.syncStore()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	account := c.accountID
	c.mu.Unlock()
	if account == "" {
		return nil, errors.New("sync: login is required")
	}
	return store.ListObjects(account)
}

// PullAll enumerates all live objects in the store and pulls each one.
func (c *Client) PullAll() (PullResult, error) {
	summaries, err := c.ListObjects()
	if err != nil {
		return PullResult{}, err
	}
	if len(summaries) == 0 {
		return PullResult{}, nil
	}
	ids := make([]string, 0, len(summaries))
	for _, s := range summaries {
		ids = append(ids, s.ObjectID)
	}
	return c.PullObjects(ids...)
}

// ResolveConflict resolves a recorded conflict for objectID by either
// uploading the local payload as a new winning version (keepLocal=true) or
// adopting the remote payload (keepLocal=false).
func (c *Client) ResolveConflict(objectID string, keepLocal bool) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	store, err := c.syncStore()
	if err != nil {
		return err
	}
	c.mu.Lock()
	account := c.accountID
	device := c.deviceID
	enabled := c.syncEnabled
	plan := c.plan
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	conflictIdx := -1
	var entry ConflictEntry
	for i, candidate := range c.conflicts {
		if candidate.ObjectID == objectID || candidate.ConflictID == objectID {
			conflictIdx = i
			entry = candidate
			break
		}
	}
	var class DataClass
	if conflictIdx >= 0 {
		class = c.localObjects[entry.ObjectID].class
	}
	c.mu.Unlock()
	if conflictIdx < 0 {
		return ErrConflictNotFound
	}
	if account == "" {
		return errors.New("sync: login is required")
	}
	if class == "" {
		if remoteEnv, getErr := store.GetEnvelope(account, entry.ObjectID); getErr == nil {
			class = remoteEnv.Class
		} else {
			class = InputMethod
		}
	}

	if keepLocal {
		if !enabled {
			return errors.New("sync: recovery code must be saved and confirmed before sync is enabled")
		}
		if class == Passwords || class == KeyManager {
			return ErrClassForbidden
		}
		if !plan.allows(class) {
			return fmt.Errorf("%w: %s", ErrClassNotAllowed, class)
		}
		if class == SensitiveVault && !c.vault.Authorized() {
			return errors.New("sync: vault authorization required")
		}
		remoteVer := entry.RemoteVersion
		if v, vErr := store.ObjectVersion(account, entry.ObjectID); vErr == nil && v > remoteVer {
			remoteVer = v
		}
		nextVer := remoteVer
		if entry.LocalVersion > nextVer {
			nextVer = entry.LocalVersion
		}
		nextVer++
		if err := c.ensureADK(); err != nil {
			return err
		}
		c.mu.Lock()
		adk := append([]byte(nil), c.adk...)
		keyID := c.currentADKKeyIDLocked()
		c.mu.Unlock()
		env, err := NewEncryptedEnvelopeWithKeyID(priv, adk, keyID, account, device, entry.ObjectID, nextVer, class, entry.LocalPayload)
		if err != nil {
			return err
		}
		if err := store.UploadEnvelope(account, device, entry.ObjectID, nextVer, env); err != nil {
			return err
		}
		c.mu.Lock()
		if c.localObjects == nil {
			c.localObjects = make(map[string]localObject)
		}
		c.localObjects[entry.ObjectID] = localObject{
			version:     nextVer,
			baseVersion: nextVer,
			basePayload: bytes.Clone(entry.LocalPayload),
			payload:     bytes.Clone(entry.LocalPayload),
			dirty:       false,
			class:       class,
		}
		c.conflicts = slices.Delete(c.conflicts, conflictIdx, conflictIdx+1)
		c.mu.Unlock()
		return nil
	}

	c.mu.Lock()
	if c.localObjects == nil {
		c.localObjects = make(map[string]localObject)
	}
	c.localObjects[entry.ObjectID] = localObject{
		version:     entry.RemoteVersion,
		baseVersion: entry.RemoteVersion,
		basePayload: bytes.Clone(entry.RemotePayload),
		payload:     bytes.Clone(entry.RemotePayload),
		dirty:       false,
		class:       class,
	}
	c.conflicts = slices.Delete(c.conflicts, conflictIdx, conflictIdx+1)
	c.mu.Unlock()
	return nil
}

func (c *Client) decryptEnvelopeWithKeyring(envelope EncryptedEnvelope, pub ed25519.PublicKey) ([]byte, error) {
	c.mu.Lock()
	key, ok := c.adkForKeyIDLocked(envelope.KeyID)
	fallback := append([]byte(nil), c.adk...)
	c.mu.Unlock()
	if ok {
		if payload, err := DecryptEnvelope(key, envelope, pub); err == nil {
			return payload, nil
		}
	}
	if err := c.RefreshADK(); err == nil {
		c.mu.Lock()
		refreshed, found := c.adkForKeyIDLocked(envelope.KeyID)
		if !found {
			refreshed = append([]byte(nil), c.adk...)
		}
		c.mu.Unlock()
		if len(refreshed) == 32 {
			return DecryptEnvelope(refreshed, envelope, pub)
		}
	}
	if len(fallback) == 32 {
		return DecryptEnvelope(fallback, envelope, pub)
	}
	return nil, errors.New("sync: no matching ADK available for envelope")
}

// Pull fetches one encrypted object, verifies its sender signature, and only
// then decrypts it. A verification failure never reaches the AEAD operation.
func (c *Client) Pull(object string) (PulledObject, error) {
	result, err := c.PullObjects(object)
	if err != nil {
		return PulledObject{}, err
	}
	if len(result.Objects) == 0 {
		return PulledObject{}, ErrStoreObjectMissing
	}
	return result.Objects[0], nil
}

func (c *Client) PullObjects(objects ...string) (PullResult, error) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	store, err := c.syncStore()
	if err != nil {
		return PullResult{}, err
	}
	c.mu.Lock()
	account := c.accountID
	hasADK := len(c.adk) == 32
	c.mu.Unlock()
	if account == "" {
		return PullResult{}, errors.New("sync: login is required")
	}
	if !hasADK {
		if err := c.ensureADK(); err != nil {
			return PullResult{}, err
		}
	}
	out := PullResult{}
	for _, object := range objects {
		envelope, err := store.GetEnvelope(account, object)
		if err != nil {
			return out, err
		}
		pub, err := store.DevicePublicKey(account, envelope.DeviceID)
		if err != nil {
			return out, err
		}
		if !envelope.Verify(pub) {
			return out, errors.New("sync: invalid envelope signature")
		}
		if !knownClass(envelope.Class) {
			c.recordDiagnostic(SyncDiagnostic{Kind: "unsupported_class", Class: envelope.Class, ObjectID: envelope.ObjectID, Message: "unknown data class skipped without local persistence"})
			continue
		}
		payload, err := c.decryptEnvelopeWithKeyring(envelope, pub)
		if err != nil {
			return out, err
		}
		pulled := PulledObject{ObjectID: envelope.ObjectID, Version: envelope.Version, Class: envelope.Class, Payload: append([]byte(nil), payload...)}
		c.mu.Lock()
		local, hasLocal := c.localObjects[object]
		c.mu.Unlock()
		if hasLocal {
			localModified := local.dirty && !bytes.Equal(local.payload, local.basePayload)
			if bytes.Equal(local.payload, payload) {
				// Payloads already match: converge metadata cleanly.
			} else if !localModified {
				// Clean local copy (not dirty, or matches base): fast-forward without conflict.
				if envelope.Version < local.baseVersion {
					continue
				}
			} else if envelope.Version != local.baseVersion {
				// Local was modified concurrently while remote diverged from base.
				conflictID := conflictObjectID(object, envelope.DeviceID, envelope.Version)
				conflict := ConflictEntry{ObjectID: object, ConflictID: conflictID, LocalVersion: local.version, RemoteVersion: envelope.Version, LocalPayload: bytes.Clone(local.payload), RemotePayload: bytes.Clone(payload)}
				c.recordConflict(conflict)
				out.Conflicts = append(out.Conflicts, conflict)
				continue
			} else {
				// Remote is still at our baseVersion; preserve our uncommitted local edit.
				continue
			}
		}
		c.mu.Lock()
		if c.localObjects == nil {
			c.localObjects = make(map[string]localObject)
		}
		c.localObjects[object] = localObject{
			version:     envelope.Version,
			baseVersion: envelope.Version,
			basePayload: append([]byte(nil), payload...),
			payload:     append([]byte(nil), payload...),
			dirty:       false,
			class:       envelope.Class,
		}
		c.mu.Unlock()
		out.Objects = append(out.Objects, pulled)
	}
	return out, nil
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
	c.vault.deleteForTombstone(t.ObjectID)
	c.mu.Lock()
	delete(c.localObjects, t.ObjectID)
	c.conflicts = slices.DeleteFunc(c.conflicts, func(conflict ConflictEntry) bool {
		return conflict.ObjectID == t.ObjectID || conflict.ConflictID == t.ObjectID
	})
	c.mu.Unlock()
	return r
}
func (c *Client) Delete(object string) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	d, err := c.GenerateDeleteInstruction(object)
	if err != nil {
		return err
	}
	store, storeErr := c.syncStore()
	if storeErr != nil {
		return storeErr
	}
	t, err := store.SubmitDelete(d)
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
	return c.saveState()
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
	store, storeErr := c.syncStore()
	if storeErr != nil {
		return nil, CleanupReport{}, storeErr
	}
	ts, err := store.SyncTombstones(a, d, after)
	if err != nil {
		return nil, CleanupReport{}, err
	}
	var r CleanupReport
	for _, t := range ts {
		if t.AccountID != a || t.ObjectID == "" || t.Version == 0 || !store.VerifyTombstone(t) {
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
		if err := store.ConfirmTombstones(a, d, confirm); err != nil {
			return nil, CleanupReport{}, err
		}
	}
	c.mu.Lock()
	if confirm > c.watermark {
		c.watermark = confirm
	}
	c.mu.Unlock()
	if err := c.saveState(); err != nil {
		return nil, CleanupReport{}, err
	}
	return ts, r, nil
}
func (c *Client) Upload(object string, version uint64, payload []byte) error {
	return c.UploadClass(InputMethod, object, version, payload)
}

var ErrClipboardPrivacy = errors.New("sync: clipboard item rejected by privacy filter")

// ClipboardCandidateAllowed centralizes the existing clipboard privacy policy
// at the sync boundary, so sensitive pasteboard types cannot become encrypted
// sync objects even when a caller bypasses clipboard history persistence.
func ClipboardCandidateAllowed(candidate clipboard.CaptureCandidate) bool {
	return clipboard.NewPrivacyFilter().ShouldRecord(candidate)
}

// UploadClipboard applies clipboard privacy filtering before encrypting or
// storing any payload. The payload is caller-provided opaque data and is never
// inspected by the filter.
func (c *Client) UploadClipboard(object string, version uint64, candidate clipboard.CaptureCandidate, payload []byte) error {
	if !ClipboardCandidateAllowed(candidate) {
		return ErrClipboardPrivacy
	}
	return c.UploadClass(ClipboardText, object, version, payload)
}

func (c *Client) UploadClass(class DataClass, object string, version uint64, payload []byte) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
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
		return ErrClassForbidden
	}
	if !p.allows(class) {
		return fmt.Errorf("%w: %s", ErrClassNotAllowed, class)
	}
	if class == SensitiveVault && !c.vault.Authorized() {
		return errors.New("sync: vault authorization required")
	}
	if object == "" || version == 0 {
		return errors.New("sync: invalid upload")
	}
	c.mu.Lock()
	adk := append([]byte(nil), c.adk...)
	keyID := c.currentADKKeyIDLocked()
	c.mu.Unlock()
	if len(adk) != 32 {
		if err := c.ensureADK(); err != nil {
			return err
		}
		c.mu.Lock()
		adk = append([]byte(nil), c.adk...)
		keyID = c.currentADKKeyIDLocked()
		c.mu.Unlock()
	}
	e, err := NewEncryptedEnvelopeWithKeyID(priv, adk, keyID, a, d, object, version, class, payload)
	if err != nil {
		return err
	}
	store, storeErr := c.syncStore()
	if storeErr != nil {
		return storeErr
	}
	if err := store.UploadEnvelope(a, d, object, version, e); err != nil {
		if !errors.Is(err, ErrStoreVersion) {
			return err
		}
		// Another device won the same object/version. Preserve this local
		// payload under a deterministic conflict object instead of dropping it.
		remote, getErr := store.GetEnvelope(a, object)
		if getErr != nil {
			return err
		}
		pub, keyErr := store.DevicePublicKey(a, remote.DeviceID)
		if keyErr != nil {
			return err
		}
		remotePayload, decErr := c.decryptEnvelopeWithKeyring(remote, pub)
		if decErr != nil {
			return decErr
		}
		conflictID := conflictObjectID(object, d, version)
		conflictEnvelope, envErr := NewEncryptedEnvelopeWithKeyID(priv, adk, keyID, a, d, conflictID, version, class, payload)
		if envErr != nil {
			return envErr
		}
		if uploadErr := store.UploadEnvelope(a, d, conflictID, version, conflictEnvelope); uploadErr != nil {
			return uploadErr
		}
		c.mu.Lock()
		if c.localObjects == nil {
			c.localObjects = make(map[string]localObject)
		}
		c.localObjects[object] = localObject{
			version:     version,
			baseVersion: remote.Version,
			basePayload: bytes.Clone(remotePayload),
			payload:     bytes.Clone(payload),
			dirty:       true,
			class:       class,
		}
		c.mu.Unlock()
		c.recordConflict(ConflictEntry{ObjectID: object, ConflictID: conflictID, LocalVersion: version, RemoteVersion: remote.Version, LocalPayload: append([]byte(nil), payload...), RemotePayload: remotePayload})
		return ErrStoreConflict
	}
	c.mu.Lock()
	if c.localObjects == nil {
		c.localObjects = make(map[string]localObject)
	}
	c.localObjects[object] = localObject{
		version:     version,
		baseVersion: version,
		basePayload: append([]byte(nil), payload...),
		payload:     append([]byte(nil), payload...),
		dirty:       false,
		class:       class,
	}
	c.mu.Unlock()
	return nil
}

// NewEncryptedEnvelopeWithKeyID seals an opaque payload for an authenticated
// sync upload using an explicit 32-byte ADK and versioned ADK key ID.
func NewEncryptedEnvelopeWithKeyID(priv ed25519.PrivateKey, adk []byte, keyID, account, device, object string, version uint64, class DataClass, payload []byte) (EncryptedEnvelope, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return EncryptedEnvelope{}, errors.New("sync: invalid signing key")
	}
	if len(adk) != 32 {
		return EncryptedEnvelope{}, ErrMissingADK
	}
	if !IsValidADKKeyID(keyID) {
		return EncryptedEnvelope{}, errors.New("sync: invalid ADK key id")
	}
	e := EncryptedEnvelope{ProtocolVersion: ProtocolVersion, KeyID: keyID, AccountID: account, DeviceID: device, ObjectID: object, Version: version, Class: class, Nonce: make([]byte, 12)}
	if _, err := io.ReadFull(rand.Reader, e.Nonce); err != nil {
		return e, err
	}
	block, err := aes.NewCipher(adk)
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

// NewEncryptedEnvelope seals an opaque payload for an authenticated sync upload.
// Callers MUST provide a real 32-byte ADK:
//   - 7 args: (adk, account, device, object, version, class, payload) -> uses DefaultADKKeyID
//   - 8 args: (adk, keyID, account, device, object, version, class, payload)
func NewEncryptedEnvelope(priv ed25519.PrivateKey, args ...any) (EncryptedEnvelope, error) {
	var adk []byte
	var keyID string
	var a, d, o string
	var v uint64
	var class DataClass
	var payload []byte
	switch len(args) {
	case 8:
		adk, _ = args[0].([]byte)
		keyID, _ = args[1].(string)
		a, _ = args[2].(string)
		d, _ = args[3].(string)
		o, _ = args[4].(string)
		v = envelopeVersion(args[5])
		class, _ = args[6].(DataClass)
		payload, _ = args[7].([]byte)
	case 7:
		adk, _ = args[0].([]byte)
		keyID = DefaultADKKeyID
		a, _ = args[1].(string)
		d, _ = args[2].(string)
		o, _ = args[3].(string)
		v = envelopeVersion(args[4])
		class, _ = args[5].(DataClass)
		payload, _ = args[6].([]byte)
	case 6:
		return EncryptedEnvelope{}, ErrMissingADK
	default:
		return EncryptedEnvelope{}, errors.New("sync: invalid envelope arguments")
	}
	return NewEncryptedEnvelopeWithKeyID(priv, adk, keyID, a, d, o, v, class, payload)
}

func envelopeVersion(value any) uint64 {
	switch v := value.(type) {
	case uint64:
		return v
	case int:
		if v >= 0 {
			return uint64(v)
		}
	case int64:
		if v >= 0 {
			return uint64(v)
		}
	case uint:
		return uint64(v)
	}
	return 0
}

func clientEnvelopeWithADK(priv ed25519.PrivateKey, adk []byte, a, d, o string, v uint64, class DataClass, payload []byte) (EncryptedEnvelope, error) {
	return NewEncryptedEnvelopeWithKeyID(priv, adk, DefaultADKKeyID, a, d, o, v, class, payload)
}
func DecryptEnvelope(adk []byte, e EncryptedEnvelope, pub ed25519.PublicKey) ([]byte, error) {
	if len(adk) != 32 || !IsValidADKKeyID(e.KeyID) || !e.Verify(pub) {
		return nil, errors.New("sync: invalid envelope authentication")
	}
	block, err := aes.NewCipher(adk)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(e.Nonce) != gcm.NonceSize() {
		return nil, errors.New("sync: invalid envelope nonce")
	}
	return gcm.Open(nil, e.Nonce, e.Ciphertext, e.aad())
}
func recoveryKEK(code string, salt []byte) ([]byte, error) {
	return hkdfKey([]byte(code), salt, recoveryInfo)
}
func sealRecovery(code string, salt, adk []byte) ([]byte, error) {
	key, err := recoveryKEK(code, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, adk, nil), nil
}
func openRecovery(code string, salt, wrapped []byte) ([]byte, error) {
	key, err := recoveryKEK(code, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(wrapped) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("sync: invalid recovery wrap")
	}
	return gcm.Open(nil, wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():], nil)
}
func (c *Client) PrepareRecoveryCode() (string, error) {
	b := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	code := strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), "=")
	c.mu.Lock()
	a, d := c.accountID, c.deviceID
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	adk := append([]byte(nil), c.adk...)
	store := c.store
	c.mu.Unlock()
	if a == "" || d == "" {
		return "", errors.New("sync: login is required")
	}
	if store == nil {
		return "", errors.New("sync: store is not configured")
	}
	recoveryStore, ok := store.(RecoveryStore)
	if !ok {
		return "", errors.New("sync: recovery storage is unsupported by this store")
	}
	if len(adk) != 32 {
		if err := c.ensureADK(); err != nil {
			return "", err
		}
		c.mu.Lock()
		adk = append([]byte(nil), c.adk...)
		c.mu.Unlock()
	}
	salt, err := recoveryStore.GetRecoverySalt(a, d, ed25519.Sign(priv, WrappedADKRequestCanonical(a, d)))
	if err != nil {
		return "", err
	}
	wrapped, err := sealRecovery(code, salt, adk)
	if err != nil {
		return "", err
	}
	if err := recoveryStore.SetRecoveryWrap(a, d, wrapped, ed25519.Sign(priv, RecoveryWrapCanonical(a, d, wrapped))); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.recoveryWrap = append([]byte(nil), wrapped...)
	c.deviceSalt = append([]byte(nil), salt...)
	c.recoveryReady = false
	c.syncEnabled = false
	c.mu.Unlock()
	if err := c.saveState(); err != nil {
		return "", err
	}
	return code, nil
}
func (c *Client) ConfirmRecoveryCode(code string) error {
	c.mu.Lock()
	a, d := c.accountID, c.deviceID
	priv := append(ed25519.PrivateKey(nil), c.privateKey...)
	salt := append([]byte(nil), c.deviceSalt...)
	wrapped := append([]byte(nil), c.recoveryWrap...)
	store := c.store
	keyStore := c.keyStore
	c.mu.Unlock()
	if len(salt) != 32 || len(wrapped) == 0 {
		if store == nil {
			return errors.New("sync: store is not configured")
		}
		recoveryStore, ok := store.(RecoveryStore)
		if !ok {
			return errors.New("sync: recovery storage is unsupported by this store")
		}
		if len(salt) != 32 {
			var err error
			salt, err = recoveryStore.GetRecoverySalt(a, d, ed25519.Sign(priv, WrappedADKRequestCanonical(a, d)))
			if err != nil {
				return err
			}
		}
		if len(wrapped) == 0 {
			var err error
			wrapped, err = recoveryStore.GetRecoveryWrap(a, d, ed25519.Sign(priv, WrappedADKRequestCanonical(a, d)))
			if err != nil {
				return err
			}
		}
	}
	adk, err := openRecovery(code, salt, wrapped)
	if err != nil {
		return errors.New("sync: recovery code does not match")
	}
	c.mu.Lock()
	c.adk = append([]byte(nil), adk...)
	if c.adkHistory == nil {
		c.adkHistory = make(map[string][]byte)
	}
	c.adkHistory[c.currentADKKeyIDLocked()] = append([]byte(nil), adk...)
	c.recoveryReady = true
	c.syncEnabled = true
	c.mu.Unlock()
	if keyStore != nil {
		if err := keyStore.SaveKey(deviceADKKey, encodeSecret(adk)); err != nil {
			return err
		}
	}
	return c.saveState()
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
