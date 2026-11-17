// Package sync provides the privacy boundaries and deletion protocol for
// Clavis' cross-device data.  It deliberately deals in opaque payloads: the
// server can route encrypted objects and retain deletion metadata, but the
// deletion metadata never contains content, hashes, or keys.
package sync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// DataClass is a category that can be selected independently during setup.
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

// SyncItem is the safe-to-display description of a setup choice.  It contains
// no sample data or secret values.
type SyncItem struct {
	Class     DataClass `json:"class"`
	Label     string    `json:"label"`
	Sensitive bool      `json:"sensitive"`
	Allowed   bool      `json:"allowed"`
}

// SyncPlan is the account-wide selection made during onboarding. Device
// overlay is intentionally not part of this structure.
type SyncPlan struct {
	InputMethod    bool `json:"inputMethod"`
	LearningData   bool `json:"learningData"`
	ClipboardText  bool `json:"clipboardText"`
	Media          bool `json:"media"`
	ApplicationCtx bool `json:"applicationContext"`
	SensitiveVault bool `json:"sensitiveVault"`
	// These fields are retained to make an attempted unsafe selection visible
	// to callers. Validate rejects them; they can never be synced.
	Passwords  bool `json:"passwords"`
	KeyManager bool `json:"keyManager"`
}

// DefaultSyncPlan is conservative: vault data requires a separate approval.
func DefaultSyncPlan() SyncPlan {
	return SyncPlan{InputMethod: true, LearningData: true, ClipboardText: true, Media: true, ApplicationCtx: true}
}

// Validate enforces the hard boundary around password and key-manager data.
func (p SyncPlan) Validate() error {
	if p.Passwords || p.KeyManager {
		return errors.New("sync: passwords and key-manager data can never be synchronized")
	}
	return nil
}

// Items returns the exact categories suitable for a consent screen.
func (p SyncPlan) allows(class DataClass) bool {
	switch class {
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
	default:
		return false
	}
}

func (p SyncPlan) Items() []SyncItem {
	return []SyncItem{
		{Class: InputMethod, Label: "Input method", Allowed: p.InputMethod},
		{Class: LearningData, Label: "Learning data", Allowed: p.LearningData},
		{Class: ClipboardText, Label: "Clipboard text", Allowed: p.ClipboardText},
		{Class: Media, Label: "Media", Allowed: p.Media},
		{Class: ApplicationCtx, Label: "Application context", Allowed: p.ApplicationCtx},
		{Class: SensitiveVault, Label: "Sensitive vault", Sensitive: true, Allowed: p.SensitiveVault},
		{Class: Passwords, Label: "Passwords (never synchronized)", Sensitive: true, Allowed: false},
		{Class: KeyManager, Label: "Key manager (never synchronized)", Sensitive: true, Allowed: false},
	}
}

// PersonalBaseline is the account-wide preference set. Values are opaque to
// the sync server; callers should encrypt them before constructing an upload.
type PersonalBaseline struct {
	AccountID string            `json:"accountId"`
	Plan      SyncPlan          `json:"plan"`
	Values    map[string][]byte `json:"values"`
}

func NewPersonalBaseline(accountID string, plan SyncPlan) (*PersonalBaseline, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, errors.New("sync: account id cannot be empty")
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return &PersonalBaseline{AccountID: accountID, Plan: plan, Values: map[string][]byte{}}, nil
}

func (b *PersonalBaseline) Set(key string, value []byte) error {
	if b == nil || strings.TrimSpace(key) == "" {
		return errors.New("sync: baseline key cannot be empty")
	}
	if b.Values == nil {
		b.Values = map[string][]byte{}
	}
	b.Values[key] = append([]byte(nil), value...)
	return nil
}

func (b *PersonalBaseline) Get(key string) ([]byte, bool) {
	if b == nil {
		return nil, false
	}
	v, ok := b.Values[key]
	return append([]byte(nil), v...), ok
}

// DeviceOverlay is local-only state. It has no serialization or upload path.
type DeviceOverlay struct {
	DeviceID string         `json:"deviceId"`
	Values   map[string]any `json:"values"`
}

// NewDeviceOverlay creates a local overlay and copies the provided values.
func NewDeviceOverlay(deviceID string, values map[string]any) (*DeviceOverlay, error) {
	if strings.TrimSpace(deviceID) == "" {
		return nil, errors.New("sync: device id cannot be empty")
	}
	copyValues := make(map[string]any, len(values))
	for k, v := range values {
		copyValues[k] = v
	}
	return &DeviceOverlay{DeviceID: deviceID, Values: copyValues}, nil
}

// AuditEvent is intentionally limited to non-content metadata.
type AuditEvent struct {
	ObjectID string    `json:"objectId"`
	DeviceID string    `json:"deviceId"`
	At       time.Time `json:"at"`
	Action   string    `json:"action"`
}

// DeleteInstruction is the signed, client-originated deletion command. The
// canonical form includes no payload, content hash, or key.
type DeleteInstruction struct {
	AccountID string    `json:"accountId"`
	ObjectID  string    `json:"objectId"`
	DeviceID  string    `json:"deviceId"`
	Version   uint64    `json:"version"`
	IssuedAt  time.Time `json:"issuedAt"`
	Signature []byte    `json:"signature"`
}

func (d DeleteInstruction) canonical() []byte {
	// Avoid JSON's representation of a signature and make the signed bytes
	// stable across Go versions.
	var b []byte
	appendString := func(s string) { b = binary.AppendUvarint(b, uint64(len(s))); b = append(b, s...) }
	appendString(d.AccountID)
	appendString(d.ObjectID)
	appendString(d.DeviceID)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], d.Version)
	b = append(b, n[:]...)
	binary.BigEndian.PutUint64(n[:], uint64(d.IssuedAt.UTC().UnixNano()))
	return append(b, n[:]...)
}

// Verify checks the Ed25519 signature without exposing any deletion content.
func (d DeleteInstruction) Verify(publicKey ed25519.PublicKey) bool {
	return len(d.Signature) == ed25519.SignatureSize && ed25519.Verify(publicKey, d.canonical(), d.Signature)
}

// Tombstone is the only object information returned by the server after a
// deletion. Version is monotonic for the account/object pair.
type Tombstone struct {
	AccountID string    `json:"accountId"`
	ObjectID  string    `json:"objectId"`
	Version   uint64    `json:"version"`
	Sequence  uint64    `json:"sequence"`
	DeletedAt time.Time `json:"deletedAt"`
}

// LocalCache models all local copies that must be purged on receipt of a
// tombstone. Backups and exports are removed as whole artifacts if they refer
// to a deleted object, preventing restoration from an old package.
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

func (c *LocalCache) PutCleartext(id string, data []byte)  { c.put(c.Cleartext, id, data) }
func (c *LocalCache) PutCiphertext(id string, data []byte) { c.put(c.Ciphertext, id, data) }
func (c *LocalCache) PutMedia(id string, data []byte)      { c.put(c.Media, id, data) }
func (c *LocalCache) put(dst map[string][]byte, id string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dst == nil {
		return
	}
	dst[id] = append([]byte(nil), data...)
}
func (c *LocalCache) PutBackup(name string, objectIDs []string, data []byte) {
	c.putArtifact(c.Backups, name, objectIDs, data)
}
func (c *LocalCache) PutExport(name string, objectIDs []string, data []byte) {
	c.putArtifact(c.Exports, name, objectIDs, data)
}
func (c *LocalCache) putArtifact(dst map[string]LocalArtifact, name string, ids []string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dst == nil {
		return
	}
	dst[name] = LocalArtifact{ObjectIDs: append([]string(nil), ids...), Data: append([]byte(nil), data...)}
}

// CleanupReport makes deletion fan-out observable without reporting content.
type CleanupReport struct{ Cleartext, Ciphertext, Media, Backups, Exports int }

func (c *LocalCache) ApplyTombstones(ts []Tombstone) CleanupReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make(map[string]struct{}, len(ts))
	for _, t := range ts {
		ids[t.ObjectID] = struct{}{}
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
	for name, a := range c.Backups {
		if artifactContains(a, ids) {
			delete(c.Backups, name)
			r.Backups++
		}
	}
	for name, a := range c.Exports {
		if artifactContains(a, ids) {
			delete(c.Exports, name)
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

// Server is an in-memory reference implementation of the server-side
// protocol. Payloads are treated as opaque bytes and are not included in
// audit records or tombstones.
type Server struct {
	mu         sync.Mutex
	accounts   map[string]map[string]ed25519.PublicKey
	tombstones map[string]map[string]Tombstone
	objects    map[string]map[string][]byte
	watermarks map[string]map[string]uint64
	sequence   uint64
	audit      []AuditEvent
}

func NewServer() *Server {
	return &Server{accounts: map[string]map[string]ed25519.PublicKey{}, tombstones: map[string]map[string]Tombstone{}, objects: map[string]map[string][]byte{}, watermarks: map[string]map[string]uint64{}}
}

func (s *Server) Login(accountID string) error {
	if strings.TrimSpace(accountID) == "" {
		return errors.New("sync: account id cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts[accountID] == nil {
		s.accounts[accountID] = map[string]ed25519.PublicKey{}
	}
	if s.tombstones[accountID] == nil {
		s.tombstones[accountID] = map[string]Tombstone{}
	}
	if s.objects[accountID] == nil {
		s.objects[accountID] = map[string][]byte{}
	}
	if s.watermarks[accountID] == nil {
		s.watermarks[accountID] = map[string]uint64{}
	}
	return nil
}
func (s *Server) PairDevice(accountID, deviceID string, publicKey ed25519.PublicKey) error {
	if err := s.Login(accountID); err != nil {
		return err
	}
	if strings.TrimSpace(deviceID) == "" || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("sync: invalid device pairing")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[accountID][deviceID] = append(ed25519.PublicKey(nil), publicKey...)
	return nil
}

func (s *Server) SubmitDelete(d DeleteInstruction) (Tombstone, error) {
	if d.AccountID == "" || d.ObjectID == "" || d.DeviceID == "" || d.Version == 0 {
		return Tombstone{}, errors.New("sync: incomplete delete instruction")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.accounts[d.AccountID][d.DeviceID]
	if len(key) != ed25519.PublicKeySize || !d.Verify(key) {
		return Tombstone{}, errors.New("sync: invalid delete signature")
	}
	old, exists := s.tombstones[d.AccountID][d.ObjectID]
	if exists && d.Version <= old.Version {
		return old, fmt.Errorf("sync: stale delete version %d", d.Version)
	}
	s.sequence++
	t := Tombstone{AccountID: d.AccountID, ObjectID: d.ObjectID, Version: d.Version, Sequence: s.sequence, DeletedAt: time.Now().UTC()}
	s.tombstones[d.AccountID][d.ObjectID] = t
	delete(s.objects[d.AccountID], d.ObjectID)
	s.audit = append(s.audit, AuditEvent{ObjectID: d.ObjectID, DeviceID: d.DeviceID, At: t.DeletedAt, Action: "delete"})
	return t, nil
}

// SyncTombstones must be called before upload. It advances the device
// watermark even when no tombstones are returned.
func (s *Server) SyncTombstones(accountID, deviceID string, afterSequence uint64) ([]Tombstone, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts[accountID][deviceID] == nil {
		return nil, errors.New("sync: device is not paired")
	}
	out := make([]Tombstone, 0)
	for _, t := range s.tombstones[accountID] {
		if t.Sequence > afterSequence {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	max := afterSequence
	for _, t := range out {
		if t.Sequence > max {
			max = t.Sequence
		}
	}
	s.watermarks[accountID][deviceID] = max
	return out, nil
}

// Upload rejects a device whose tombstone watermark is behind the server.
// The payload is opaque and is never copied to audit data.
func (s *Server) Upload(accountID, deviceID, objectID string, version uint64, payload []byte) error {
	if objectID == "" || version == 0 {
		return errors.New("sync: invalid upload")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts[accountID][deviceID] == nil {
		return errors.New("sync: device is not paired")
	}
	seen := s.watermarks[accountID][deviceID]
	for _, t := range s.tombstones[accountID] {
		if t.Sequence > seen {
			return errors.New("sync: tombstones must be synchronized before upload")
		}
		if t.ObjectID == objectID && version <= t.Version {
			return errors.New("sync: upload is deleted by tombstone")
		}
	}
	if s.objects[accountID] == nil {
		s.objects[accountID] = map[string][]byte{}
	}
	s.objects[accountID][objectID] = append([]byte(nil), payload...)
	return nil
}
func (s *Server) Audit() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditEvent(nil), s.audit...)
}
func (s *Server) LatestSequence() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.sequence }

// Client owns the signing key and local deletion fan-out.
type Client struct {
	mu            sync.Mutex
	server        *Server
	accountID     string
	deviceID      string
	privateKey    ed25519.PrivateKey
	cache         *LocalCache
	watermark     uint64
	syncEnabled   bool
	recoveryHash  [32]byte
	recoveryReady bool
	plan          SyncPlan
	vault         *Vault
}

func NewClient(server *Server, deviceID string) (*Client, error) {
	if server == nil || strings.TrimSpace(deviceID) == "" {
		return nil, errors.New("sync: server and device id are required")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Client{server: server, deviceID: deviceID, privateKey: priv, cache: NewLocalCache(), plan: DefaultSyncPlan(), vault: NewVault("")}, nil
}
func (c *Client) Login(accountID string) error {
	if err := c.server.Login(accountID); err != nil {
		return err
	}
	c.mu.Lock()
	c.accountID = accountID
	c.mu.Unlock()
	return nil
}
func (c *Client) PairDevice() error {
	c.mu.Lock()
	account, device := c.accountID, c.deviceID
	pub := append(ed25519.PublicKey(nil), c.privateKey.Public().(ed25519.PublicKey)...)
	c.mu.Unlock()
	if account == "" {
		return errors.New("sync: login is required")
	}
	return c.server.PairDevice(account, device, pub)
}
func (c *Client) Cache() *LocalCache { return c.cache }
func (c *Client) Vault() *Vault      { return c.vault }

// ConfigurePlan records account-wide consent choices. Passwords and key
// manager data are rejected before they can reach any upload API.
func (c *Client) ConfigurePlan(plan SyncPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	c.plan = plan
	c.mu.Unlock()
	return nil
}

func (c *Client) Plan() SyncPlan {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plan
}
func (c *Client) GenerateDeleteInstruction(objectID string) (DeleteInstruction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accountID == "" {
		return DeleteInstruction{}, errors.New("sync: login is required")
	}
	if strings.TrimSpace(objectID) == "" {
		return DeleteInstruction{}, errors.New("sync: object id cannot be empty")
	}
	t := time.Now().UTC()
	d := DeleteInstruction{AccountID: c.accountID, ObjectID: objectID, DeviceID: c.deviceID, Version: c.watermark + 1, IssuedAt: t}
	d.Signature = ed25519.Sign(c.privateKey, d.canonical())
	return d, nil
}
func (c *Client) Delete(objectID string) error {
	d, err := c.GenerateDeleteInstruction(objectID)
	if err != nil {
		return err
	}
	t, err := c.server.SubmitDelete(d)
	if err != nil {
		return err
	}
	c.cache.ApplyTombstones([]Tombstone{t})
	c.vault.deleteForTombstone(t.ObjectID)
	c.mu.Lock()
	if t.Sequence > c.watermark {
		c.watermark = t.Sequence
	}
	c.mu.Unlock()
	return nil
}
func (c *Client) Sync() ([]Tombstone, CleanupReport, error) {
	c.mu.Lock()
	account, device, after := c.accountID, c.deviceID, c.watermark
	c.mu.Unlock()
	if account == "" {
		return nil, CleanupReport{}, errors.New("sync: login is required")
	}
	ts, err := c.server.SyncTombstones(account, device, after)
	if err != nil {
		return nil, CleanupReport{}, err
	}
	r := c.cache.ApplyTombstones(ts)
	for _, t := range ts {
		c.vault.deleteForTombstone(t.ObjectID)
	}
	c.mu.Lock()
	for _, t := range ts {
		if t.Sequence > c.watermark {
			c.watermark = t.Sequence
		}
	}
	c.mu.Unlock()
	return ts, r, nil
}
func (c *Client) Upload(objectID string, version uint64, payload []byte) error {
	return c.UploadClass(InputMethod, objectID, version, payload)
}

// UploadClass applies the selected plan and the independent vault gate before
// handing opaque bytes to the server.
func (c *Client) UploadClass(class DataClass, objectID string, version uint64, payload []byte) error {
	c.mu.Lock()
	account, device, enabled, plan := c.accountID, c.deviceID, c.syncEnabled, c.plan
	c.mu.Unlock()
	if !enabled {
		return errors.New("sync: recovery code must be saved and confirmed before sync is enabled")
	}
	if account == "" {
		return errors.New("sync: login is required")
	}
	if class == Passwords || class == KeyManager {
		return errors.New("sync: passwords and key-manager data can never be synchronized")
	}
	if !plan.allows(class) {
		return fmt.Errorf("sync: %s is not enabled in the sync plan", class)
	}
	if class == SensitiveVault && !c.vault.Authorized() {
		return errors.New("sync: vault authorization required")
	}
	return c.server.Upload(account, device, objectID, version, payload)
}

// PrepareRecoveryCode returns a one-time-display code. Sync remains disabled
// until ConfirmRecoveryCode is called with the code after the user saves it.
func (c *Client) PrepareRecoveryCode() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code := strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), "=")
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
func equalHash(expected [32]byte, data []byte) bool {
	got := sha256.Sum256(data)
	var diff byte
	for i := range got {
		diff |= got[i] ^ expected[i]
	}
	return diff == 0
}
func (c *Client) SyncEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.syncEnabled && c.recoveryReady
}
func (c *Client) Watermark() uint64 { c.mu.Lock(); defer c.mu.Unlock(); return c.watermark }

// Vault stores encrypted values behind an authorization independent of account
// login. An empty authorization token is valid only for tests/local callers;
// production callers should provide a non-empty token.
type Vault struct {
	mu         sync.RWMutex
	authHash   [32]byte
	key        []byte
	authorized bool
	objects    map[string][]byte
}

func NewVault(authorization string) *Vault {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	// An empty authorization is never accepted. For a client created before
	// vault setup, use an unguessable placeholder until the user configures a
	// real independent approval factor.
	if strings.TrimSpace(authorization) == "" {
		randomAuth := make([]byte, 32)
		_, _ = rand.Read(randomAuth)
		authorization = hex.EncodeToString(randomAuth)
	}
	return &Vault{authHash: sha256.Sum256([]byte(authorization)), key: key, objects: map[string][]byte{}}
}

// ConfigureAuthorization installs the independent vault approval factor once.
// It cannot silently replace an already configured or authorized factor.
func (v *Vault) ConfigureAuthorization(authorization string) error {
	if strings.TrimSpace(authorization) == "" {
		return errors.New("sync: vault authorization cannot be empty")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.authorized {
		return errors.New("sync: revoke vault before changing authorization")
	}
	v.authHash = sha256.Sum256([]byte(authorization))
	return nil
}
func (v *Vault) Authorize(authorization string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !equalHash(v.authHash, []byte(authorization)) {
		return errors.New("sync: vault authorization denied")
	}
	v.authorized = true
	return nil
}
func (v *Vault) Revoke()          { v.mu.Lock(); v.authorized = false; v.mu.Unlock() }
func (v *Vault) Authorized() bool { v.mu.RLock(); defer v.mu.RUnlock(); return v.authorized }
func (v *Vault) Put(objectID string, plaintext []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.authorized {
		return errors.New("sync: vault authorization required")
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	v.objects[objectID] = gcm.Seal(nonce, nonce, plaintext, nil)
	return nil
}
func (v *Vault) Get(objectID string) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.authorized {
		return nil, errors.New("sync: vault authorization required")
	}
	raw, ok := v.objects[objectID]
	if !ok {
		return nil, errors.New("sync: secret not found")
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	n := gcm.NonceSize()
	if len(raw) < n {
		return nil, errors.New("sync: invalid encrypted secret")
	}
	return gcm.Open(nil, raw[:n], raw[n:], nil)
}
func (v *Vault) Ciphertext(objectID string) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	raw, ok := v.objects[objectID]
	if !ok {
		return nil, errors.New("sync: secret not found")
	}
	return append([]byte(nil), raw...), nil
}
func (v *Vault) deleteForTombstone(objectID string) {
	v.mu.Lock()
	delete(v.objects, objectID)
	v.mu.Unlock()
}

func (v *Vault) Delete(objectID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.authorized {
		return errors.New("sync: vault authorization required")
	}
	delete(v.objects, objectID)
	return nil
}

// MarshalDeleteInstruction is useful to transport a signed command without
// accidentally marshaling a payload. It is intentionally a small helper.
func MarshalDeleteInstruction(d DeleteInstruction) ([]byte, error) { return json.Marshal(d) }
func ParseDeleteInstruction(data []byte) (DeleteInstruction, error) {
	var d DeleteInstruction
	err := json.Unmarshal(data, &d)
	return d, err
}

// Fingerprint is for device display only; it is not part of audit or tombstone
// data and must never be used as a content identifier.
func (c *Client) Fingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	sum := sha256.Sum256(c.privateKey.Public().(ed25519.PublicKey))
	return hex.EncodeToString(sum[:8])
}
