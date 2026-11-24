package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SecretRef is the metadata-only handle exposed to UI, agents and persistence.
// It intentionally has no field that can carry a secret or a secret-derived value.
type SecretRef struct {
	ID               string    `json:"id"`
	Backend          string    `json:"backend"`
	RecordKey        string    `json:"record_key"`
	Label            string    `json:"label,omitempty"`
	Kind             string    `json:"kind"`
	Sensitivity      string    `json:"sensitivity"`
	Version          uint64    `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	State            string    `json:"state"`
	RevokedAt        time.Time `json:"revoked_at,omitempty"`
	MetadataRevision uint64    `json:"metadata_revision"`
}

const (
	SecretBackendKeychain = "keychain"
	SecretBackendLayered  = "layered"
	SecretBackendEnv      = "env"
	SecretKindPassword    = "password"
	SecretSensitivity     = "secret"
	SecretStateStored     = "stored"
	SecretStateRevoked    = "revoked"

	ActorUserSession = "user_session"
	ActorAgent       = "agent"
	ActorAutomation  = "automation"

	CopyStateCopied          = "copied"
	CopyStateExpired         = "expired"
	CopyStateFailed          = "failed"
	CopyStateUnknown         = "unknown"
	CopyStateDegraded        = "degraded"
	CopyStateOwnershipLost   = "ownership_lost"
	CopyErrorRefNotFound     = "secret_ref_not_found"
	CopyErrorRevoked         = "secret_revoked"
	CopyErrorKeychainLocked  = "keychain_locked"
	CopyErrorUserAuth        = "user_auth_required"
	CopyErrorAgentForbidden  = "agent_forbidden"
	CopyErrorTTLInvalid      = "ttl_invalid"
	CopyErrorCleanupLost     = "cleanup_ownership_lost"
	CopyErrorAuditFailed     = "audit_write_failed"
	CopyErrorVersionConflict = "version_conflict"
	CopyDestinationClipboard = "system_clipboard"
)

var (
	ErrAgentForbidden = errors.New(CopyErrorAgentForbidden)
	ErrTTLInvalid     = errors.New(CopyErrorTTLInvalid)
	ErrOwnershipLost  = errors.New(CopyErrorCleanupLost)
)

const (
	DefaultSecretCopyTTL = 30 * time.Second
	MaxSecretCopyTTL     = 5 * time.Minute
)

// UserCopyRequest is accepted only from a current user session.
type UserCopyRequest struct {
	RequestID   string        `json:"request_id"`
	SecretRefID string        `json:"secret_ref_id"`
	Actor       string        `json:"actor"`
	SessionID   string        `json:"session_id"`
	TTL         time.Duration `json:"ttl"`
	Destination string        `json:"destination"`
	Reason      string        `json:"reason"`
}

// CopyReceipt is safe to serialize to an agent or a diagnostic snapshot.
// cleanupToken is deliberately private and never leaves the host boundary.
type CopyReceipt struct {
	RequestID    string    `json:"request_id"`
	State        string    `json:"state"`
	ExpiresAt    time.Time `json:"expires_at"`
	ObservedAt   time.Time `json:"observed_at"`
	Retryable    bool      `json:"retryable"`
	ErrorCode    string    `json:"error_code,omitempty"`
	cleanupToken string
}

// AuditEvent contains metadata only. Implementations must not add a value, digest,
// length or entropy field because those become secret side channels.
type AuditEvent struct {
	At          time.Time `json:"at"`
	Action      string    `json:"action"`
	Actor       string    `json:"actor"`
	RequestID   string    `json:"request_id,omitempty"`
	SecretRefID string    `json:"secret_ref_id,omitempty"`
	State       string    `json:"state"`
	ErrorCode   string    `json:"error_code,omitempty"`
}

type AuditSink interface{ Append(AuditEvent) error }

// ClipboardAdapter is the only boundary that receives a secret value.
type ClipboardAdapter interface {
	Write(value string) (string, error)
	ClearIfOwned(token string) error
}

// SecretValueStore is the narrow trusted adapter used by Vault. APIKeyStore remains
// available for existing provider callers, while this interface keeps raw values out
// of metadata and agent-facing APIs.
type SecretValueStore interface {
	ReadKey(account string) (string, error)
	SaveKey(account, value string) error
	DeleteKey(account string) error
}

type KeychainSecretStore struct{ Store SecretValueStore }

func NewKeychainSecretStore(store SecretValueStore) *KeychainSecretStore {
	return &KeychainSecretStore{Store: store}
}
func (s *KeychainSecretStore) ReadKey(account string) (string, error) {
	return s.Store.ReadKey(account)
}
func (s *KeychainSecretStore) SaveKey(account, value string) error {
	return s.Store.SaveKey(account, value)
}
func (s *KeychainSecretStore) DeleteKey(account string) error { return s.Store.DeleteKey(account) }

// LayeredSecretStore is the trusted adapter for the existing layered value store.
// It does not add another persistence layer and therefore preserves FileStore's
// file-first and migration-off semantics.
type LayeredSecretStore struct{ Store SecretValueStore }

func NewLayeredSecretStore(store SecretValueStore) *LayeredSecretStore {
	return &LayeredSecretStore{Store: store}
}
func (s *LayeredSecretStore) ReadKey(account string) (string, error) { return s.Store.ReadKey(account) }
func (s *LayeredSecretStore) SaveKey(account, value string) error {
	return s.Store.SaveKey(account, value)
}
func (s *LayeredSecretStore) DeleteKey(account string) error { return s.Store.DeleteKey(account) }

// FakeSecretStore is a concurrency-safe keychain fake for tests. It is never a
// production default and exposes values only through the trusted adapter method.
type FakeSecretStore struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewFakeSecretStore() *FakeSecretStore { return &FakeSecretStore{values: make(map[string]string)} }
func (s *FakeSecretStore) ReadKey(account string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[account]
	if !ok || v == "" {
		return "", ErrSecretNotFound
	}
	return v, nil
}
func (s *FakeSecretStore) SaveKey(account, value string) error {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(value) == "" {
		return errors.New("secrets: account and value are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[account] = value
	return nil
}
func (s *FakeSecretStore) DeleteKey(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, account)
	return nil
}

type MemoryAuditSink struct {
	mu     sync.Mutex
	Events []AuditEvent
	Err    error
}

func (s *MemoryAuditSink) Append(e AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	s.Events = append(s.Events, e)
	return nil
}

func (s *MemoryAuditSink) Snapshot() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEvent, len(s.Events))
	copy(out, s.Events)
	return out
}

// AgentVaultSnapshot is safe to expose to an agent or diagnostic renderer.
// It contains references and receipts only, never values or ownership tokens.
type AgentVaultSnapshot struct {
	SecretRefs []SecretRef   `json:"secret_refs"`
	Receipts   []CopyReceipt `json:"receipts"`
}

// Vault enforces metadata-only access and user-only copy. Values are held only
// between ResolveForUser and ClipboardAdapter.Write.
type Vault struct {
	mu        sync.Mutex
	store     SecretValueStore
	clipboard ClipboardAdapter
	audit     AuditSink
	now       func() time.Time
	refs      map[string]SecretRef
	receipts  map[string]CopyReceipt
}

func NewVault(store SecretValueStore, clipboard ClipboardAdapter, audit AuditSink) *Vault {
	return &Vault{store: store, clipboard: clipboard, audit: audit, now: time.Now, refs: make(map[string]SecretRef), receipts: make(map[string]CopyReceipt)}
}

func (v *Vault) Put(ref SecretRef, value string) (SecretRef, error) {
	if v.store == nil {
		return SecretRef{}, errors.New("secrets: value store is required")
	}
	if err := validateSecretRef(ref); err != nil {
		return SecretRef{}, err
	}
	if strings.TrimSpace(value) == "" {
		return SecretRef{}, errors.New("secrets: value is required")
	}
	if ref.Backend == "" {
		ref.Backend = SecretBackendLayered
	}
	if ref.Kind == "" {
		ref.Kind = SecretKindPassword
	}
	if ref.Sensitivity == "" {
		ref.Sensitivity = SecretSensitivity
	}
	if ref.Version == 0 {
		ref.Version = 1
	}
	if ref.MetadataRevision == 0 {
		ref.MetadataRevision = 1
	}
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = v.now().UTC()
	}
	ref.UpdatedAt = v.now().UTC()
	ref.State = SecretStateStored
	v.mu.Lock()
	if old, exists := v.refs[ref.ID]; exists {
		if ref.Version != old.Version {
			v.mu.Unlock()
			return SecretRef{}, errors.New(CopyErrorVersionConflict)
		}
		ref.Version = old.Version + 1
		ref.MetadataRevision = old.MetadataRevision + 1
		ref.CreatedAt = old.CreatedAt
	}
	if err := v.store.SaveKey(ref.RecordKey, value); err != nil {
		v.mu.Unlock()
		return SecretRef{}, err
	}
	v.refs[ref.ID] = ref
	v.mu.Unlock()
	return ref, v.appendAudit(AuditEvent{At: ref.UpdatedAt, Action: "stored", SecretRefID: ref.ID, State: ref.State})
}

func (v *Vault) Metadata(id string) (SecretRef, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	ref, ok := v.refs[id]
	if !ok {
		return SecretRef{}, errors.New(CopyErrorRefNotFound)
	}
	return ref, nil
}

func (v *Vault) AgentMetadata(id string) (SecretRef, error) { return v.Metadata(id) }

func (v *Vault) Receipt(requestID, refID, sessionID string) (CopyReceipt, error) {
	key := requestID + "\x00" + refID + "\x00" + sessionID
	v.mu.Lock()
	defer v.mu.Unlock()
	receipt, ok := v.receipts[key]
	if !ok {
		return CopyReceipt{}, errors.New(CopyErrorRefNotFound)
	}
	return receipt, nil
}

func (v *Vault) AgentReceipt(requestID, refID, sessionID string) (CopyReceipt, error) {
	return v.Receipt(requestID, refID, sessionID)
}

func (v *Vault) Snapshot() AgentVaultSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	snapshot := AgentVaultSnapshot{SecretRefs: make([]SecretRef, 0, len(v.refs)), Receipts: make([]CopyReceipt, 0, len(v.receipts))}
	for _, ref := range v.refs {
		snapshot.SecretRefs = append(snapshot.SecretRefs, ref)
	}
	for _, receipt := range v.receipts {
		receipt.cleanupToken = ""
		snapshot.Receipts = append(snapshot.Receipts, receipt)
	}
	return snapshot
}

// ResolveForUser is the only raw-value read exposed by Vault. It requires the
// user actor and a live session, so agent and automation callers cannot reach it.
func (v *Vault) ResolveForUser(refID, actor, sessionID, requestID string) (string, error) {
	if actor != ActorUserSession {
		return "", ErrAgentForbidden
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(requestID) == "" {
		return "", errors.New(CopyErrorUserAuth)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	ref, ok := v.refs[refID]
	if !ok {
		return "", errors.New(CopyErrorRefNotFound)
	}
	if ref.State == SecretStateRevoked {
		return "", errors.New(CopyErrorRevoked)
	}
	return v.store.ReadKey(ref.RecordKey)
}

func (v *Vault) Copy(req UserCopyRequest) (CopyReceipt, error) {
	now := v.now().UTC()
	key := req.RequestID + "\x00" + req.SecretRefID + "\x00" + req.SessionID
	v.mu.Lock()
	if old, ok := v.receipts[key]; ok {
		v.mu.Unlock()
		return old, nil
	}
	v.mu.Unlock()
	if req.Actor != ActorUserSession {
		return v.failReceipt(req, key, now, CopyErrorAgentForbidden, false, ErrAgentForbidden)
	}
	if strings.TrimSpace(req.RequestID) == "" || strings.TrimSpace(req.SecretRefID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return v.failReceipt(req, key, now, CopyErrorUserAuth, true, errors.New(CopyErrorUserAuth))
	}
	if req.Destination != "" && req.Destination != CopyDestinationClipboard {
		return v.failReceipt(req, key, now, "destination_not_supported", false, errors.New("destination_not_supported"))
	}
	if req.TTL == 0 {
		req.TTL = DefaultSecretCopyTTL
	}
	if req.TTL < 0 || req.TTL > MaxSecretCopyTTL {
		return v.failReceipt(req, key, now, CopyErrorTTLInvalid, false, ErrTTLInvalid)
	}
	if v.clipboard == nil || v.store == nil {
		return v.failReceipt(req, key, now, "clipboard_write_unknown", true, errors.New("clipboard unavailable"))
	}
	v.mu.Lock()
	if old, ok := v.receipts[key]; ok {
		v.mu.Unlock()
		return old, nil
	}
	ref, ok := v.refs[req.SecretRefID]
	if !ok {
		v.mu.Unlock()
		return v.failReceipt(req, key, now, CopyErrorRefNotFound, false, errors.New(CopyErrorRefNotFound))
	}
	if ref.State == SecretStateRevoked {
		v.mu.Unlock()
		return v.failReceipt(req, key, now, CopyErrorRevoked, false, errors.New(CopyErrorRevoked))
	}
	value, err := v.store.ReadKey(ref.RecordKey)
	if err != nil {
		v.mu.Unlock()
		return v.failReceipt(req, key, now, CopyErrorKeychainLocked, true, err)
	}
	token, err := v.clipboard.Write(value)
	if err != nil {
		v.mu.Unlock()
		return v.failReceipt(req, key, now, "clipboard_write_unknown", true, err)
	}
	receipt := CopyReceipt{RequestID: req.RequestID, State: CopyStateCopied, ExpiresAt: now.Add(req.TTL), ObservedAt: now, Retryable: false, cleanupToken: token}
	v.receipts[key] = receipt
	v.mu.Unlock()
	if err := v.appendAudit(AuditEvent{At: now, Action: "copied", Actor: req.Actor, RequestID: req.RequestID, SecretRefID: ref.ID, State: receipt.State}); err != nil {
		receipt.State, receipt.ErrorCode, receipt.Retryable = CopyStateDegraded, CopyErrorAuditFailed, true
		v.mu.Lock()
		v.receipts[key] = receipt
		v.mu.Unlock()
	}
	time.AfterFunc(req.TTL, func() { _ = v.Cleanup(req.RequestID, req.SecretRefID, req.SessionID) })
	return receipt, nil
}

func (v *Vault) Cleanup(requestID, refID, sessionID string) error {
	key := requestID + "\x00" + refID + "\x00" + sessionID
	v.mu.Lock()
	receipt, ok := v.receipts[key]
	if !ok {
		v.mu.Unlock()
		return errors.New(CopyErrorRefNotFound)
	}
	if receipt.State == CopyStateExpired {
		v.mu.Unlock()
		return nil
	}
	if receipt.State == CopyStateOwnershipLost {
		v.mu.Unlock()
		return ErrOwnershipLost
	}
	err := v.clipboard.ClearIfOwned(receipt.cleanupToken)
	now := v.now().UTC()
	if err != nil {
		receipt.State, receipt.ErrorCode, receipt.ObservedAt = CopyStateOwnershipLost, CopyErrorCleanupLost, now
		v.receipts[key] = receipt
		v.mu.Unlock()
		_ = v.appendAudit(AuditEvent{At: now, Action: "cleanup", RequestID: requestID, SecretRefID: refID, State: receipt.State, ErrorCode: receipt.ErrorCode})
		return ErrOwnershipLost
	}
	receipt.State, receipt.ObservedAt = CopyStateExpired, now
	v.receipts[key] = receipt
	v.mu.Unlock()
	return v.appendAudit(AuditEvent{At: now, Action: "expired", RequestID: requestID, SecretRefID: refID, State: receipt.State})
}

func (v *Vault) Revoke(id string) error {
	v.mu.Lock()
	ref, ok := v.refs[id]
	if ok {
		ref.State, ref.RevokedAt, ref.UpdatedAt = SecretStateRevoked, v.now().UTC(), v.now().UTC()
		ref.MetadataRevision++
		v.refs[id] = ref
	}
	v.mu.Unlock()
	if !ok {
		return errors.New(CopyErrorRefNotFound)
	}
	return v.appendAudit(AuditEvent{At: ref.UpdatedAt, Action: "revoked", SecretRefID: id, State: ref.State})
}

func (v *Vault) failReceipt(req UserCopyRequest, key string, now time.Time, code string, retryable bool, err error) (CopyReceipt, error) {
	r := CopyReceipt{RequestID: req.RequestID, State: CopyStateFailed, ObservedAt: now, Retryable: retryable, ErrorCode: code}
	v.mu.Lock()
	v.receipts[key] = r
	v.mu.Unlock()
	_ = v.appendAudit(AuditEvent{At: now, Action: "copy_failed", Actor: req.Actor, RequestID: req.RequestID, SecretRefID: req.SecretRefID, State: r.State, ErrorCode: code})
	return r, err
}

func (v *Vault) appendAudit(e AuditEvent) error {
	if v.audit == nil {
		return nil
	}
	return v.audit.Append(e)
}

func validateSecretRef(ref SecretRef) error {
	if strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.RecordKey) == "" {
		return errors.New("secrets: id and record_key are required")
	}
	if strings.TrimSpace(ref.Label) != "" {
		lower := strings.ToLower(ref.Label)
		if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			return errors.New("secrets: label may not identify secret material")
		}
	}
	if ref.Backend != "" && ref.Backend != SecretBackendLayered && ref.Backend != SecretBackendKeychain && ref.Backend != SecretBackendEnv {
		return errors.New("secrets: unsupported backend")
	}
	return nil
}

func NewSecretRef(id, recordKey, label string) SecretRef {
	if id == "" {
		id = randomID()
	}
	now := time.Now().UTC()
	return SecretRef{ID: id, Backend: SecretBackendLayered, RecordKey: recordKey, Label: label, Kind: SecretKindPassword, Sensitivity: SecretSensitivity, Version: 1, CreatedAt: now, UpdatedAt: now, State: SecretStateStored, MetadataRevision: 1}
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ref-%d", time.Now().UnixNano())
	}
	return "ref-" + hex.EncodeToString(b[:])
}
