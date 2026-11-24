package sync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	stdsync "sync"
	"time"

	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/host"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

const defaultDirTransportMaxFileBytes = 16 * 1024 * 1024

// DirTransportOptions configures a local-SQLite + shared-directory ciphertext
// sync transport. The shared directory contains ONLY immutable signed
// encrypted envelopes, signed tombstones, and signed public control records —
// never live SQLite/WAL files, plaintext payloads, or private keys.
type DirTransportOptions struct {
	SyncDir      string
	PrivateDir   string
	AccountID    string
	DeviceID     string
	Identity     *coresync.DeviceIdentity
	SecretStore  coresync.SecretStore
	Plan         *coresync.SyncPlan
	MaxFileBytes int64
}

// DirSyncReport summarizes one idempotent DirTransport SyncOnce pass without
// exposing any plaintext payloads or secret material.
type DirSyncReport struct {
	IngestedEnvelopes  int                       `json:"ingestedEnvelopes"`
	IngestedTombstones int                       `json:"ingestedTombstones"`
	Pulled             []coresync.PulledObject   `json:"-"`
	PulledCount        int                       `json:"pulledObjects"`
	Conflicts          []coresync.ConflictEntry  `json:"-"`
	ConflictCount      int                       `json:"conflicts"`
	Watermark          uint64                    `json:"watermark"`
	AllowedClasses     []coresync.DataClass      `json:"allowlist"`
	Diagnostics        []coresync.SyncDiagnostic `json:"diagnostics,omitempty"`
}

// DirInspection holds read-only metadata gathered by inspecting a shared sync
// directory without writing any files or creating directories.
type DirInspection struct {
	Exists         bool `json:"dirExists"`
	EnvelopeFiles  int  `json:"envelopeFiles"`
	TombstoneFiles int  `json:"tombstoneFiles"`
}

type dirDeviceRecord struct {
	AccountID       string `json:"accountId"`
	DeviceID        string `json:"deviceId"`
	Ed25519Public   []byte `json:"ed25519Public"`
	X25519Public    []byte `json:"x25519Public"`
	Bootstrap       bool   `json:"bootstrap,omitempty"`
	PublishedAtUnix int64  `json:"publishedAtUnix"`
	Signature       []byte `json:"signature"`
}

func (r dirDeviceRecord) canonical() []byte {
	cp := r
	cp.Signature = nil
	raw, _ := json.Marshal(cp)
	return append([]byte("clavis.sync.dir-device.v2\x00"), raw...)
}

func (r dirDeviceRecord) verify() bool {
	if len(r.Ed25519Public) != ed25519.PublicKeySize || len(r.X25519Public) != 32 || len(r.Signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(r.Ed25519Public), r.canonical(), r.Signature)
}

type dirWrappedADKRecord struct {
	AccountID string `json:"accountId"`
	Sender    string `json:"sender"`
	Target    string `json:"target"`
	KeyID     string `json:"keyId"`
	Wrapped   []byte `json:"wrapped"`
	Signature []byte `json:"signature"`
}

type dirRevocationRecord struct {
	AccountID     string `json:"accountId"`
	RevokedDevice string `json:"revokedDevice"`
	RevokerDevice string `json:"revokerDevice"`
	NewADKKeyID   string `json:"newAdkKeyId"`
	RevokedAtUnix int64  `json:"revokedAtUnix"`
	Signature     []byte `json:"signature"`
}

func (r dirRevocationRecord) canonical() []byte {
	cp := r
	cp.Signature = nil
	raw, _ := json.Marshal(cp)
	return append([]byte("clavis.sync.dir-revocation.v2\x00"), raw...)
}

type dirRecoveryRecord struct {
	AccountID string `json:"accountId"`
	DeviceID  string `json:"deviceId"`
	Salt      []byte `json:"salt"`
	Wrapped   []byte `json:"wrapped"`
	Signature []byte `json:"signature"`
}

// DirTransport coordinates local encrypted SQLite state with an immutable
// shared directory compatible with iCloud, Syncthing, Dropbox, and OneDrive.
type DirTransport struct {
	mu           stdsync.Mutex
	syncDir      string
	privateDir   string
	accountID    string
	deviceID     string
	plan         coresync.SyncPlan
	maxFileBytes int64
	store        *storage.SQLiteSyncStore
	stateStore   *SQLiteStateStore
	client       *coresync.Client
	localCred    []byte
	ingested     map[string]struct{}
	lastSyncAt   time.Time
}

// OpenDirTransport opens or initializes a DirTransport whose private SQLite
// coordinator and state DB live inside opts.PrivateDir while immutable signed
// ciphertexts are exchanged through opts.SyncDir.
func OpenDirTransport(ctx context.Context, opts DirTransportOptions) (*DirTransport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(opts.SyncDir) == "" {
		return nil, fmt.Errorf("%w: sync dir is required", coresync.ErrStoreInvalidArgument)
	}
	if strings.TrimSpace(opts.PrivateDir) == "" {
		return nil, fmt.Errorf("%w: private dir is required", coresync.ErrStoreInvalidArgument)
	}
	if strings.TrimSpace(opts.AccountID) == "" || strings.TrimSpace(opts.DeviceID) == "" {
		return nil, fmt.Errorf("%w: account id and device id are required", coresync.ErrStoreInvalidArgument)
	}
	if err := safePathSegment(opts.DeviceID); err != nil {
		return nil, fmt.Errorf("sync: invalid device id: %w", err)
	}
	plan := coresync.Phase1SyncPlan()
	if opts.Plan != nil {
		plan = *opts.Plan
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	maxBytes := opts.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = defaultDirTransportMaxFileBytes
	}

	absSyncDir, err := filepath.Abs(opts.SyncDir)
	if err != nil {
		return nil, err
	}
	if err := ensureSafeDir(absSyncDir); err != nil {
		return nil, fmt.Errorf("sync: unsafe sync dir: %w", err)
	}
	absPrivateDir, err := filepath.Abs(opts.PrivateDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absPrivateDir, 0o700); err != nil {
		return nil, fmt.Errorf("sync: create private dir: %w", err)
	}

	syncStoreDir := filepath.Join(absPrivateDir, "sync")
	if err := os.MkdirAll(syncStoreDir, 0o700); err != nil {
		return nil, fmt.Errorf("sync: create sync store dir: %w", err)
	}
	coordPath := filepath.Join(syncStoreDir, "coordinator.db")
	if err := provisionPrivateFile(coordPath); err != nil {
		return nil, fmt.Errorf("sync: provision coordinator db: %w", err)
	}
	store, err := storage.NewSQLiteSyncStore(ctx, coordPath)
	if err != nil {
		return nil, err
	}

	statePath := filepath.Join(absPrivateDir, "sync-state.db")
	stateStore, err := OpenSQLiteStateStore(ctx, statePath)
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	var client *coresync.Client
	switch {
	case opts.Identity != nil:
		client, err = coresync.NewClientWithStore(store, opts.DeviceID, *opts.Identity)
		if err == nil {
			err = client.SetStateStore(ctx, stateStore)
		}
	case opts.SecretStore != nil:
		client, err = coresync.NewPersistentClientWithStore(ctx, store, opts.DeviceID, opts.SecretStore, stateStore)
	default:
		_ = stateStore.Close()
		_ = store.Close()
		return nil, errors.New("sync: either Identity or SecretStore is required")
	}
	if err != nil {
		_ = stateStore.Close()
		_ = store.Close()
		return nil, err
	}
	if err := client.ConfigurePlan(plan); err != nil {
		_ = stateStore.Close()
		_ = store.Close()
		return nil, err
	}

	t := &DirTransport{
		syncDir:      absSyncDir,
		privateDir:   absPrivateDir,
		accountID:    opts.AccountID,
		deviceID:     opts.DeviceID,
		plan:         plan,
		maxFileBytes: maxBytes,
		store:        store,
		stateStore:   stateStore,
		client:       client,
		ingested:     make(map[string]struct{}),
	}

	// Resume persisted account credential if this local store already holds the account.
	if err := t.resumeExistingAccount(ctx, opts); err != nil {
		_ = stateStore.Close()
		_ = store.Close()
		return nil, err
	}
	return t, nil
}

func (t *DirTransport) resumeExistingAccount(ctx context.Context, opts DirTransportOptions) error {
	if opts.SecretStore != nil {
		if err := t.client.LoginWithStoredCredential(t.accountID); err == nil {
			t.localCred = readCredentialFromSecretStore(opts.SecretStore)
			_ = t.client.RefreshADK()
			return nil
		}
	}
	if opts.Identity != nil && len(opts.Identity.Credential) == 32 {
		if err := t.client.LoginWithCredential(t.accountID, opts.Identity.Credential); err == nil {
			t.localCred = append([]byte(nil), opts.Identity.Credential...)
			_ = t.client.RefreshADK()
			return nil
		}
	}
	return nil
}

func readCredentialFromSecretStore(store coresync.SecretStore) []byte {
	if store == nil {
		return nil
	}
	// Reload via LoadOrCreateDeviceIdentity would reuse existing secrets cleanly.
	id, err := coresync.LoadOrCreateDeviceIdentity(store)
	if err != nil || len(id.Credential) != 32 {
		return nil
	}
	return id.Credential
}

// Close closes the local SQLite stores owned by DirTransport.
func (t *DirTransport) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []error
	if t.stateStore != nil {
		errs = append(errs, t.stateStore.Close())
		t.stateStore = nil
	}
	if t.store != nil {
		errs = append(errs, t.store.Close())
		t.store = nil
	}
	return errors.Join(errs...)
}

func (t *DirTransport) Client() *coresync.Client        { return t.client }
func (t *DirTransport) Store() *storage.SQLiteSyncStore { return t.store }
func (t *DirTransport) StateStore() *SQLiteStateStore   { return t.stateStore }
func (t *DirTransport) SyncDir() string                 { return t.syncDir }
func (t *DirTransport) Plan() coresync.SyncPlan         { return t.plan }

func (t *DirTransport) DevicePublicKey() ed25519.PublicKey { return t.client.DevicePublicKey() }
func (t *DirTransport) X25519PublicKey() []byte            { return t.client.X25519PublicKey() }

// BootstrapAccount initializes this device as the first/coordinator device for
// the account, confirms a recovery code, and publishes public device metadata
// and wrapped ADK/recovery blobs (never secrets or plaintext) into SyncDir.
func (t *DirTransport) BootstrapAccount(ctx context.Context) (recoveryCode string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	cred, err := t.store.RegisterAccount(t.accountID)
	if err != nil {
		return "", err
	}
	t.localCred = append([]byte(nil), cred...)
	if err := t.client.LoginWithCredential(t.accountID, cred); err != nil {
		return "", err
	}
	if err := t.client.PairDeviceWithCredential(); err != nil {
		return "", err
	}
	code, err := t.client.PrepareRecoveryCode()
	if err != nil {
		return "", err
	}
	if err := t.client.ConfirmRecoveryCode(code); err != nil {
		return "", err
	}
	if err := t.publishDeviceRecordLocked(true); err != nil {
		return "", err
	}
	if err := t.publishWrappedADKForTargetLocked(t.deviceID); err != nil {
		return "", err
	}
	if err := t.publishRecoveryRecordLocked(); err != nil {
		return "", err
	}
	if err := t.persistStatusLocked(ctx); err != nil {
		return "", err
	}
	return code, nil
}

// CreatePairingBundle creates a signed QR/short-code pairing bundle for a new
// peer device and publishes the signed ticket into SyncDir so the peer can
// join using only the shared directory.
func (t *DirTransport) CreatePairingBundle(ctx context.Context, peerDevice string, peerEdPub ed25519.PublicKey, peerXPub []byte, now time.Time) (coresync.PairingBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := safePathSegment(peerDevice); err != nil {
		return coresync.PairingBundle{}, fmt.Errorf("sync: invalid peer device id: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	bundle, err := t.client.CreatePairingBundleWithX25519(peerDevice, peerEdPub, peerXPub, now)
	if err != nil {
		return coresync.PairingBundle{}, err
	}
	if err := t.store.PairDeviceWithTicket(bundle.Ticket); err != nil {
		return coresync.PairingBundle{}, err
	}
	raw, err := json.Marshal(bundle.Ticket)
	if err != nil {
		return coresync.PairingBundle{}, err
	}
	ticketPath := filepath.Join(t.syncDir, "control", "tickets", bundle.Ticket.Nonce+".json")
	if err := t.writeImmutableFile(ticketPath, raw); err != nil {
		return coresync.PairingBundle{}, err
	}
	if err := t.publishWrappedADKForTargetLocked(peerDevice); err != nil {
		return coresync.PairingBundle{}, err
	}
	if err := t.persistStatusLocked(ctx); err != nil {
		return coresync.PairingBundle{}, err
	}
	return bundle, nil
}

// AcceptPairingTicket verifies a signed pairing ticket from an already-paired
// authorizer, joins the account locally in this device's private SQLite store,
// unwraps the shared ADK, and optionally confirms the recovery code so sync is
// enabled immediately.
func (t *DirTransport) AcceptPairingTicket(ctx context.Context, ticket coresync.PairingTicket, shortCode, authorizerFingerprint, recoveryCode string, now time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if shortCode != ticket.ShortCode || authorizerFingerprint == "" || authorizerFingerprint != coresync.PairingFingerprint(ed25519.PublicKey(ticket.AuthorizerPubKey)) {
		return coresync.ErrPairingFingerprint
	}
	if err := ticket.Verify(now); err != nil {
		return err
	}
	if ticket.Account != t.accountID || ticket.Device != t.deviceID {
		return coresync.ErrPairingTicket
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// Ensure this device's local private SQLite store has the account initialized.
	if len(t.localCred) != 32 {
		cred, err := t.store.RegisterAccount(t.accountID)
		if err != nil {
			return err
		}
		t.localCred = append([]byte(nil), cred...)
	}
	if err := t.client.LoginWithCredential(t.accountID, t.localCred); err != nil {
		return err
	}
	// Pin the verified authorizer public key in this device's local store.
	if err := t.store.PairDeviceWithCredential(t.accountID, ticket.Authorizer, ed25519.PublicKey(ticket.AuthorizerPubKey), t.localCred); err != nil && !errors.Is(err, coresync.ErrStoreDeviceKey) {
		return err
	}
	// Also load authorizer's X25519 public key from SyncDir control record if present.
	_ = t.ingestControlDevicesLocked()
	if err := t.client.PairDeviceWithTicket(ticket, shortCode, authorizerFingerprint, now); err != nil {
		return err
	}
	if err := t.publishDeviceRecordLocked(false); err != nil {
		return err
	}
	// Ingest recovery wrap from SyncDir if available, then confirm recovery code.
	_ = t.ingestRecoveryRecordsLocked()
	if strings.TrimSpace(recoveryCode) != "" {
		if err := t.client.ConfirmRecoveryCode(recoveryCode); err != nil {
			return err
		}
	}
	return t.persistStatusLocked(ctx)
}

// StageLocalEdit marks an uncommitted local change so SyncOnce detects true
// concurrent divergence instead of silently overwriting local state.
func (t *DirTransport) StageLocalEdit(class coresync.DataClass, objectID string, payload []byte) error {
	if err := t.validateClass(class); err != nil {
		return err
	}
	if err := safePathSegment(objectID); err != nil {
		return fmt.Errorf("sync: invalid object id: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.client.StageLocalEdit(class, objectID, payload)
}

// Upload enforces the strict Phase 1 allowlist, encrypts and signs the payload
// via the core Client, commits it to the local SQLite store, and atomically
// writes an immutable envelope file into SyncDir.
func (t *DirTransport) Upload(ctx context.Context, class coresync.DataClass, objectID string, version uint64, payload []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := t.validateClass(class); err != nil {
		return err
	}
	if err := safePathSegment(objectID); err != nil {
		return fmt.Errorf("sync: invalid object id: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.client.UploadClass(class, objectID, version, payload); err != nil {
		return err
	}
	env, err := t.store.GetEnvelope(t.accountID, objectID)
	if err != nil {
		return err
	}
	if err := t.writeEnvelopeFileLocked(env); err != nil {
		return err
	}
	t.lastSyncAt = time.Now().UTC()
	return t.persistStatusLocked(ctx)
}

// Delete signs and records a permanent tombstone locally and publishes it
// atomically into SyncDir/tombstones/<seq>.json.
func (t *DirTransport) Delete(ctx context.Context, objectID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := safePathSegment(objectID); err != nil {
		return fmt.Errorf("sync: invalid object id: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.client.Delete(objectID); err != nil {
		return err
	}
	latestSeq, err := t.store.LatestSequence(t.accountID)
	if err != nil {
		return err
	}
	// Pull/confirm our own delete batch cleanly so local watermark catches up.
	ts, _, syncErr := t.client.Sync()
	if syncErr != nil {
		return syncErr
	}
	for _, tomb := range ts {
		if err := t.writeTombstoneFileLocked(tomb); err != nil {
			return err
		}
	}
	// If already confirmed via watermark bump, still ensure latest tombstone is written.
	if len(ts) == 0 && latestSeq > 0 {
		if batch, err := t.store.SyncTombstones(t.accountID, t.deviceID, 0); err == nil {
			for _, tomb := range batch {
				if err := t.writeTombstoneFileLocked(tomb); err != nil {
					return err
				}
			}
		}
	}
	t.lastSyncAt = time.Now().UTC()
	return t.persistStatusLocked(ctx)
}

// ResolveConflict resolves an explicit conflict for objectID. When keepLocal is
// true, the new winning encrypted envelope is also published to SyncDir.
func (t *DirTransport) ResolveConflict(ctx context.Context, objectID string, keepLocal bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.client.ResolveConflict(objectID, keepLocal); err != nil {
		return err
	}
	if keepLocal {
		env, err := t.store.GetEnvelope(t.accountID, objectID)
		if err != nil {
			return err
		}
		if err := t.writeEnvelopeFileLocked(env); err != nil {
			return err
		}
	}
	t.lastSyncAt = time.Now().UTC()
	return t.persistStatusLocked(ctx)
}

// RemoveDevice revokes targetDevice, rotates the ADK to the next version
// (adk-v2, adk-v3, ...), re-wraps the rotated ADK for all remaining active
// devices (excluding targetDevice), and publishes signed revocation + wrapped
// ADK records into SyncDir.
func (t *DirTransport) RemoveDevice(ctx context.Context, targetDevice string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := safePathSegment(targetDevice); err != nil {
		return fmt.Errorf("sync: invalid target device id: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.client.RemoveDevice(targetDevice); err != nil {
		return err
	}
	newKeyID := t.client.ADKKeyID()
	if err := t.publishRevocationRecordLocked(targetDevice, newKeyID); err != nil {
		return err
	}
	activeDevices, err := t.store.ListActiveDevices(t.accountID)
	if err != nil {
		return err
	}
	for _, active := range activeDevices {
		if active == targetDevice {
			continue
		}
		_ = t.publishWrappedADKForTargetLocked(active)
	}
	t.lastSyncAt = time.Now().UTC()
	return t.persistStatusLocked(ctx)
}

// SyncOnce performs one crash-safe, idempotent synchronization pass against
// SyncDir:
//  1. Ingest verified control records (devices, tickets, revocations, wrapped ADKs).
//  2. Reconcile any crash-interrupted pending tombstone ack.
//  3. Ingest and verify remote tombstones BEFORE envelopes (deletion boundary wins).
//  4. Ingest, verify Ed25519 signatures, and decrypt allowlisted remote envelopes.
//  5. Persist updated non-secret status metadata to SQLiteStateStore.
func (t *DirTransport) SyncOnce(ctx context.Context) (DirSyncReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	report := DirSyncReport{
		AllowedClasses: t.plan.AllowedClasses(),
	}

	// 1. Control records (order matters: devices -> tickets -> revocations -> wrapped ADKs -> recovery).
	if err := t.ingestControlDevicesLocked(); err != nil {
		return report, err
	}
	if err := t.ingestControlTicketsLocked(); err != nil {
		return report, err
	}
	if err := t.ingestControlRevocationsLocked(); err != nil {
		return report, err
	}
	if err := t.ingestControlWrappedADKsLocked(); err != nil {
		return report, err
	}
	_ = t.ingestRecoveryRecordsLocked()
	// Refresh local ADK if a newer wrapped ADK was published for this device.
	_ = t.client.RefreshADK()

	// 2. Crash-safe pending tombstone ack recovery.
	if err := t.reconcilePendingAckLocked(ctx); err != nil {
		return report, err
	}

	// 3. Tombstones FIRST (never resurrect deleted objects).
	ingestedTombstones, err := t.ingestTombstonesLocked(ctx)
	if err != nil {
		return report, err
	}
	report.IngestedTombstones = ingestedTombstones

	// Persist pending_ack around client.Sync() so a crash mid-confirm recovers cleanly.
	if batch, ok, bErr := t.store.IssuedBatch(t.accountID, t.deviceID); bErr == nil && ok {
		_ = t.stateStore.SetPendingAck(ctx, batch.To)
	}
	if _, _, err := t.client.Sync(); err != nil {
		// If this device was revoked, stop cleanly with revocation error.
		if errors.Is(err, coresync.ErrStoreDeviceRevoked) || errors.Is(err, coresync.ErrStoreDeviceMissing) {
			return report, err
		}
		return report, err
	}
	_ = t.stateStore.SetPendingAck(ctx, 0)

	// 4. Ingest immutable encrypted envelopes strictly within allowlist.
	ingestedEnvs, touchedObjects, syncConflicts, err := t.ingestEnvelopesLocked()
	if err != nil {
		return report, err
	}
	report.IngestedEnvelopes = ingestedEnvs

	if len(touchedObjects) > 0 {
		sort.Strings(touchedObjects)
		pullRes, pullErr := t.client.PullObjects(touchedObjects...)
		if pullErr != nil {
			return report, pullErr
		}
		report.Pulled = pullRes.Objects
		report.PulledCount = len(pullRes.Objects)
		report.Conflicts = append(report.Conflicts, pullRes.Conflicts...)
	}
	for _, c := range syncConflicts {
		report.Conflicts = append(report.Conflicts, c)
	}
	report.ConflictCount = len(t.client.Conflicts())
	report.Watermark = t.client.Watermark()
	report.Diagnostics = t.client.Diagnostics()

	t.lastSyncAt = time.Now().UTC()
	if err := t.persistStatusLocked(ctx); err != nil {
		return report, err
	}
	return report, nil
}

// StatusSnapshot returns a local-only read-only status snapshot.
func (t *DirTransport) StatusSnapshot(ctx context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
	return NewDirStatusReader(t.stateStore, t.syncDir)(ctx)
}

// InspectDirReadOnly inspects a shared sync directory without writing anything.
func InspectDirReadOnly(syncDir string) (DirInspection, error) {
	if strings.TrimSpace(syncDir) == "" {
		return DirInspection{}, nil
	}
	info, err := os.Lstat(syncDir)
	if os.IsNotExist(err) {
		return DirInspection{Exists: false}, nil
	}
	if err != nil {
		return DirInspection{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return DirInspection{}, errors.New("sync: sync dir is not a safe directory")
	}
	out := DirInspection{Exists: true}
	for _, class := range coresync.Phase1SyncPlan().AllowedClasses() {
		entries, readErr := os.ReadDir(filepath.Join(syncDir, "envelopes", string(class)))
		if readErr != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && isJSONArtifact(entry.Name()) {
				out.EnvelopeFiles++
			}
		}
	}
	if entries, readErr := os.ReadDir(filepath.Join(syncDir, "tombstones")); readErr == nil {
		for _, entry := range entries {
			if !entry.IsDir() && isJSONArtifact(entry.Name()) {
				out.TombstoneFiles++
			}
		}
	}
	return out, nil
}

func (t *DirTransport) validateClass(class coresync.DataClass) error {
	if class == coresync.Passwords || class == coresync.KeyManager {
		return coresync.ErrClassForbidden
	}
	if !t.plan.Allows(class) {
		return fmt.Errorf("%w: %s", coresync.ErrClassNotAllowed, class)
	}
	return nil
}

func (t *DirTransport) writeEnvelopeFileLocked(env coresync.EncryptedEnvelope) error {
	if err := t.validateClass(env.Class); err != nil {
		return err
	}
	if err := safePathSegment(string(env.Class)); err != nil {
		return err
	}
	if err := safePathSegment(env.ObjectID); err != nil {
		return err
	}
	encoded, err := coresync.MarshalEncryptedEnvelope(env)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(encoded)
	sha8 := hex.EncodeToString(sum[:4])
	name := fmt.Sprintf("%s.v%d.%s.json", env.ObjectID, env.Version, sha8)
	path := filepath.Join(t.syncDir, "envelopes", string(env.Class), name)
	if err := t.writeImmutableFile(path, encoded); err != nil {
		return err
	}
	t.ingested[filepath.ToSlash(filepath.Join("envelopes", string(env.Class), name))] = struct{}{}
	return nil
}

func (t *DirTransport) writeTombstoneFileLocked(tomb coresync.Tombstone) error {
	encoded, err := json.Marshal(tomb)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d.json", tomb.Sequence)
	path := filepath.Join(t.syncDir, "tombstones", name)
	if err := t.writeImmutableFile(path, encoded); err != nil {
		return err
	}
	t.ingested[filepath.ToSlash(filepath.Join("tombstones", name))] = struct{}{}
	return nil
}

func (t *DirTransport) publishDeviceRecordLocked(bootstrap bool) error {
	rec := dirDeviceRecord{
		AccountID:       t.accountID,
		DeviceID:        t.deviceID,
		Ed25519Public:   t.client.DevicePublicKey(),
		X25519Public:    t.client.X25519PublicKey(),
		Bootstrap:       bootstrap,
		PublishedAtUnix: time.Now().UTC().UnixNano(),
	}
	// Sign using client private key? Wait: client private key is unexported, so sign via Delete/Envelope or add Sign helper?
	// Let's use the already-exported client public keys + signed ticket/ADK proof!
	// Wait: we can sign dirDeviceRecord and dirRevocationRecord cleanly via a small SignPayload method on Client, OR sign directly!
	sig, err := t.client.SignControlPayload(rec.canonical())
	if err != nil {
		return err
	}
	rec.Signature = sig
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := filepath.Join(t.syncDir, "control", "devices", t.deviceID+".pub.json")
	return t.writeImmutableFile(path, raw)
}

func (t *DirTransport) publishWrappedADKForTargetLocked(target string) error {
	if err := safePathSegment(target); err != nil {
		return err
	}
	// Read the target's wrapped ADK entry directly from local SQLite using target? No, we can re-wrap or query via WrapADKForDevice?
	// Wait: WrapADKForDevice wraps with this device's X25519 key for target and stores in t.store.
	// Better: call BuildSignedWrappedADKForDevice on Client! Let's add Client.BuildWrappedADKRecord or read from store?
	// Reading from store requires target's Ed25519 signature (since GetWrappedADKEntry checks target's sig).
	// Building a fresh signed wrapped ADK package directly from Client (using target's X25519 pub from store!) is clean, authenticated by sender's Ed25519 key, and verified by SQLiteSyncStore.SetWrappedADKEntryForDevice on every peer!
	targetXPub, err := t.store.X25519PublicKey(t.accountID, target)
	if err != nil {
		return err
	}
	keyID, wrapped, sig, err := t.client.BuildSignedWrappedADKEntry(target, targetXPub)
	if err != nil {
		return err
	}
	rec := dirWrappedADKRecord{
		AccountID: t.accountID,
		Sender:    t.deviceID,
		Target:    target,
		KeyID:     keyID,
		Wrapped:   wrapped,
		Signature: sig,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	name := fmt.Sprintf("%s.%s.%s.json", target, keyID, hex.EncodeToString(sum[:4]))
	path := filepath.Join(t.syncDir, "control", "wrapped-adk", name)
	return t.writeImmutableFile(path, raw)
}

func (t *DirTransport) publishRevocationRecordLocked(targetDevice, newKeyID string) error {
	rec := dirRevocationRecord{
		AccountID:     t.accountID,
		RevokedDevice: targetDevice,
		RevokerDevice: t.deviceID,
		NewADKKeyID:   newKeyID,
		RevokedAtUnix: time.Now().UTC().UnixNano(),
	}
	sig, err := t.client.SignControlPayload(rec.canonical())
	if err != nil {
		return err
	}
	rec.Signature = sig
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	name := fmt.Sprintf("%s.%s.json", targetDevice, hex.EncodeToString(sum[:4]))
	path := filepath.Join(t.syncDir, "control", "revocations", name)
	return t.writeImmutableFile(path, raw)
}

func (t *DirTransport) publishRecoveryRecordLocked() error {
	sigReq, err := t.client.SignControlPayload(coresync.WrappedADKRequestCanonical(t.accountID, t.deviceID))
	if err != nil {
		return err
	}
	salt, err := t.store.GetRecoverySalt(t.accountID, t.deviceID, sigReq)
	if err != nil {
		return err
	}
	wrapped, err := t.store.GetRecoveryWrap(t.accountID, t.deviceID, sigReq)
	if err != nil {
		return err
	}
	wrapSig, err := t.client.SignControlPayload(coresync.RecoveryWrapCanonical(t.accountID, t.deviceID, wrapped))
	if err != nil {
		return err
	}
	rec := dirRecoveryRecord{
		AccountID: t.accountID,
		DeviceID:  t.deviceID,
		Salt:      salt,
		Wrapped:   wrapped,
		Signature: wrapSig,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	name := fmt.Sprintf("recovery.%s.json", hex.EncodeToString(sum[:4]))
	path := filepath.Join(t.syncDir, "control", "recovery", name)
	return t.writeImmutableFile(path, raw)
}

func (t *DirTransport) ingestControlDevicesLocked() error {
	dir := filepath.Join(t.syncDir, "control", "devices")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !isJSONArtifact(entry.Name()) {
			continue
		}
		raw, err := readRegularFileLimited(filepath.Join(dir, entry.Name()), t.maxFileBytes)
		if err != nil {
			continue
		}
		var rec dirDeviceRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.AccountID != t.accountID || !rec.verify() {
			continue
		}
		// Only attach X25519 key for devices already paired in our trusted local store!
		pub, err := t.store.DevicePublicKey(t.accountID, rec.DeviceID)
		if err != nil || !bytes.Equal(pub, rec.Ed25519Public) {
			continue
		}
		if len(t.localCred) == 32 {
			_ = t.store.SetX25519PublicKey(t.accountID, rec.DeviceID, rec.X25519Public, t.localCred)
		}
	}
	return nil
}

func (t *DirTransport) ingestControlTicketsLocked() error {
	dir := filepath.Join(t.syncDir, "control", "tickets")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !isJSONArtifact(entry.Name()) {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("control", "tickets", entry.Name()))
		if _, seen := t.ingested[rel]; seen {
			continue
		}
		raw, err := readRegularFileLimited(filepath.Join(dir, entry.Name()), t.maxFileBytes)
		if err != nil {
			continue
		}
		var ticket coresync.PairingTicket
		if err := json.Unmarshal(raw, &ticket); err != nil {
			continue
		}
		if ticket.Account != t.accountID {
			continue
		}
		err = t.store.PairDeviceWithTicket(ticket)
		if err == nil || errors.Is(err, coresync.ErrPairingCodeUsed) || errors.Is(err, coresync.ErrStoreDeviceRevoked) {
			t.ingested[rel] = struct{}{}
		}
	}
	return nil
}

func (t *DirTransport) ingestControlRevocationsLocked() error {
	dir := filepath.Join(t.syncDir, "control", "revocations")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !isJSONArtifact(entry.Name()) {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("control", "revocations", entry.Name()))
		if _, seen := t.ingested[rel]; seen {
			continue
		}
		raw, err := readRegularFileLimited(filepath.Join(dir, entry.Name()), t.maxFileBytes)
		if err != nil {
			continue
		}
		var rec dirRevocationRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.AccountID != t.accountID || len(rec.Signature) != ed25519.SignatureSize {
			continue
		}
		revokerPub, err := t.store.DevicePublicKey(t.accountID, rec.RevokerDevice)
		if err != nil {
			continue
		}
		if !ed25519.Verify(revokerPub, rec.canonical(), rec.Signature) {
			continue
		}
		if len(t.localCred) == 32 {
			err := t.store.RemoveDevice(t.accountID, rec.RevokedDevice, t.localCred)
			if err == nil || errors.Is(err, coresync.ErrStoreDeviceMissing) || errors.Is(err, coresync.ErrStoreDeviceRevoked) {
				t.ingested[rel] = struct{}{}
			}
		}
	}
	return nil
}

func (t *DirTransport) ingestControlWrappedADKsLocked() error {
	dir := filepath.Join(t.syncDir, "control", "wrapped-adk")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	type candidate struct {
		rel string
		rec dirWrappedADKRecord
		ver uint64
	}
	var list []candidate
	for _, entry := range entries {
		if entry.IsDir() || !isJSONArtifact(entry.Name()) {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("control", "wrapped-adk", entry.Name()))
		if _, seen := t.ingested[rel]; seen {
			continue
		}
		raw, err := readRegularFileLimited(filepath.Join(dir, entry.Name()), t.maxFileBytes)
		if err != nil {
			continue
		}
		var rec dirWrappedADKRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.AccountID != t.accountID {
			continue
		}
		ver, ok := coresync.ADKVersion(rec.KeyID)
		if !ok {
			continue
		}
		list = append(list, candidate{rel: rel, rec: rec, ver: ver})
	}
	// Keep only the highest ADK version per target so older files never downgrade rotated keys.
	bestByTarget := make(map[string]candidate)
	for _, item := range list {
		if cur, exists := bestByTarget[item.rec.Target]; !exists || item.ver > cur.ver || (item.ver == cur.ver && item.rel > cur.rel) {
			bestByTarget[item.rec.Target] = item
		}
	}
	targets := make([]string, 0, len(bestByTarget))
	for target := range bestByTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		item := bestByTarget[target]
		err := t.store.SetWrappedADKEntryForDevice(item.rec.AccountID, item.rec.Sender, item.rec.Target, item.rec.KeyID, item.rec.Wrapped, item.rec.Signature)
		if err == nil {
			t.ingested[item.rel] = struct{}{}
		}
	}
	return nil
}

func (t *DirTransport) ingestRecoveryRecordsLocked() error {
	dir := filepath.Join(t.syncDir, "control", "recovery")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !isJSONArtifact(entry.Name()) {
			continue
		}
		raw, err := readRegularFileLimited(filepath.Join(dir, entry.Name()), t.maxFileBytes)
		if err != nil {
			continue
		}
		var rec dirRecoveryRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.AccountID != t.accountID || len(rec.Salt) != 32 || len(rec.Wrapped) == 0 {
			continue
		}
		pub, err := t.store.DevicePublicKey(t.accountID, rec.DeviceID)
		if err != nil {
			continue
		}
		if !ed25519.Verify(pub, coresync.RecoveryWrapCanonical(rec.AccountID, rec.DeviceID, rec.Wrapped), rec.Signature) {
			continue
		}
		t.client.SetRecoveryMaterial(rec.Salt, rec.Wrapped)
		_ = t.store.SetRecoveryWrap(rec.AccountID, rec.DeviceID, rec.Wrapped, rec.Signature)
	}
	return nil
}

func (t *DirTransport) reconcilePendingAckLocked(ctx context.Context) error {
	pending, err := t.stateStore.PendingAck(ctx)
	if err != nil || pending == 0 {
		return nil
	}
	confirmed, wErr := t.store.Watermark(t.accountID, t.deviceID)
	if wErr != nil {
		return nil
	}
	if confirmed >= pending {
		// Remote store already confirmed this batch before crash; advance local state cleanly.
		_, ready, _ := t.stateStore.Load(ctx)
		_ = t.stateStore.Save(ctx, confirmed, ready)
		return t.stateStore.SetPendingAck(ctx, 0)
	}
	if err := t.store.ConfirmTombstones(t.accountID, t.deviceID, pending); err == nil {
		_, ready, _ := t.stateStore.Load(ctx)
		_ = t.stateStore.Save(ctx, pending, ready)
		return t.stateStore.SetPendingAck(ctx, 0)
	}
	return nil
}

func (t *DirTransport) ingestTombstonesLocked(ctx context.Context) (int, error) {
	_ = ctx
	dir := filepath.Join(t.syncDir, "tombstones")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	type tombItem struct {
		rel  string
		seq  uint64
		tomb coresync.Tombstone
	}
	var items []tombItem
	for _, entry := range entries {
		if entry.IsDir() || !isJSONArtifact(entry.Name()) {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("tombstones", entry.Name()))
		if _, seen := t.ingested[rel]; seen {
			continue
		}
		raw, err := readRegularFileLimited(filepath.Join(dir, entry.Name()), t.maxFileBytes)
		if err != nil {
			return 0, err
		}
		var tomb coresync.Tombstone
		if err := json.Unmarshal(raw, &tomb); err != nil {
			return 0, fmt.Errorf("sync: parse tombstone %s: %w", entry.Name(), err)
		}
		if tomb.AccountID != t.accountID {
			continue
		}
		seq := tomb.Sequence
		if seq == 0 {
			if parsed, pErr := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".json"), 10, 64); pErr == nil {
				seq = parsed
			}
		}
		items = append(items, tombItem{rel: rel, seq: seq, tomb: tomb})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].seq != items[j].seq {
			return items[i].seq < items[j].seq
		}
		return items[i].rel < items[j].rel
	})
	ingestedCount := 0
	for _, item := range items {
		pub, err := t.store.DevicePublicKey(t.accountID, item.tomb.DeviceID)
		if err != nil {
			continue
		}
		if !item.tomb.Verify(pub) {
			return ingestedCount, fmt.Errorf("sync: invalid tombstone signature in %s", item.rel)
		}
		instr := coresync.DeleteInstruction{
			AccountID:       item.tomb.AccountID,
			ObjectID:        item.tomb.ObjectID,
			DeviceID:        item.tomb.DeviceID,
			Version:         item.tomb.Version,
			IssuedAt:        item.tomb.IssuedAt,
			ProtocolVersion: item.tomb.ProtocolVersion,
			Signature:       item.tomb.Signature,
		}
		_, err = t.store.SubmitDelete(instr)
		if err != nil && !errors.Is(err, coresync.ErrStoreVersion) {
			return ingestedCount, err
		}
		t.ingested[item.rel] = struct{}{}
		ingestedCount++
	}
	return ingestedCount, nil
}

func (t *DirTransport) ingestEnvelopesLocked() (int, []string, []coresync.ConflictEntry, error) {
	// Hard-reject any forbidden class directories containing files.
	for _, forbidden := range []coresync.DataClass{coresync.Passwords, coresync.KeyManager} {
		entries, err := os.ReadDir(filepath.Join(t.syncDir, "envelopes", string(forbidden)))
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && isJSONArtifact(entry.Name()) {
					return 0, nil, nil, coresync.ErrClassForbidden
				}
			}
		}
	}

	type envItem struct {
		rel string
		env coresync.EncryptedEnvelope
	}
	var items []envItem
	for _, class := range t.plan.AllowedClasses() {
		dir := filepath.Join(t.syncDir, "envelopes", string(class))
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, nil, nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !isJSONArtifact(entry.Name()) {
				continue
			}
			rel := filepath.ToSlash(filepath.Join("envelopes", string(class), entry.Name()))
			if _, seen := t.ingested[rel]; seen {
				continue
			}
			raw, err := readRegularFileLimited(filepath.Join(dir, entry.Name()), t.maxFileBytes)
			if err != nil {
				return 0, nil, nil, err
			}
			env, err := coresync.ParseEncryptedEnvelope(raw)
			if err != nil {
				return 0, nil, nil, fmt.Errorf("sync: invalid envelope %s: %w", rel, err)
			}
			if env.Class == coresync.Passwords || env.Class == coresync.KeyManager {
				return 0, nil, nil, coresync.ErrClassForbidden
			}
			if env.Class != class || !t.plan.Allows(env.Class) {
				continue
			}
			if env.AccountID != t.accountID {
				continue
			}
			items = append(items, envItem{rel: rel, env: env})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].env.ObjectID != items[j].env.ObjectID {
			return items[i].env.ObjectID < items[j].env.ObjectID
		}
		if items[i].env.Version != items[j].env.Version {
			return items[i].env.Version < items[j].env.Version
		}
		if items[i].env.DeviceID != items[j].env.DeviceID {
			return items[i].env.DeviceID < items[j].env.DeviceID
		}
		return items[i].rel < items[j].rel
	})

	ingestedCount := 0
	touchedSet := make(map[string]struct{})
	var conflicts []coresync.ConflictEntry
	for _, item := range items {
		pub, err := t.store.DevicePublicKey(t.accountID, item.env.DeviceID)
		if err != nil {
			if errors.Is(err, coresync.ErrStoreDeviceRevoked) {
				// Ignore envelopes signed by revoked devices.
				t.ingested[item.rel] = struct{}{}
				continue
			}
			continue
		}
		if !item.env.Verify(pub) {
			return ingestedCount, nil, conflicts, fmt.Errorf("sync: invalid envelope signature in %s", item.rel)
		}
		// Ensure sender's local watermark in our replica permits accepting verified remote envelopes.
		if err := t.ensureReplicaDeviceWatermarkLocked(item.env.DeviceID); err != nil {
			return ingestedCount, nil, conflicts, err
		}
		err = t.store.UploadEnvelope(item.env.AccountID, item.env.DeviceID, item.env.ObjectID, item.env.Version, item.env)
		if err != nil {
			switch {
			case errors.Is(err, coresync.ErrStoreDeleted):
				// Permanent deletion boundary wins; never resurrect.
				t.ingested[item.rel] = struct{}{}
				continue
			case errors.Is(err, coresync.ErrStoreVersion):
				// Either duplicate or concurrent same-version edit from another device!
				existing, gErr := t.store.GetEnvelope(t.accountID, item.env.ObjectID)
				if gErr == nil && existing.Version == item.env.Version && !bytes.Equal(existing.Ciphertext, item.env.Ciphertext) {
					remotePayload, decErr := t.client.DecryptVerifiedEnvelope(item.env, pub)
					if decErr != nil {
						return ingestedCount, nil, conflicts, decErr
					}
					if lo, hasLocal := t.client.LocalObject(item.env.ObjectID); hasLocal && !bytes.Equal(lo.Payload, remotePayload) {
						c := t.client.RecordRemoteConflict(item.env, remotePayload)
						conflicts = append(conflicts, c)
					}
				}
				t.ingested[item.rel] = struct{}{}
				continue
			default:
				return ingestedCount, nil, conflicts, err
			}
		}
		t.ingested[item.rel] = struct{}{}
		ingestedCount++
		touchedSet[item.env.ObjectID] = struct{}{}
	}
	touched := make([]string, 0, len(touchedSet))
	for id := range touchedSet {
		touched = append(touched, id)
	}
	return ingestedCount, touched, conflicts, nil
}

// ensureReplicaDeviceWatermarkLocked advances a remote peer's watermark in THIS
// device's local replica SQLite store when ingesting remote envelopes from the
// shared directory (our own device's watermark is governed strictly by Client.Sync).
func (t *DirTransport) ensureReplicaDeviceWatermarkLocked(peerDevice string) error {
	if peerDevice == t.deviceID {
		return nil
	}
	latest, err := t.store.LatestSequence(t.accountID)
	if err != nil || latest == 0 {
		return nil
	}
	wm, err := t.store.Watermark(t.accountID, peerDevice)
	if err != nil || wm >= latest {
		return nil
	}
	batch, err := t.store.SyncTombstones(t.accountID, peerDevice, wm)
	if err != nil || len(batch) == 0 {
		return err
	}
	return t.store.ConfirmTombstones(t.accountID, peerDevice, batch[len(batch)-1].Sequence)
}

func (t *DirTransport) persistStatusLocked(ctx context.Context) error {
	devices := 0
	if list, err := t.store.ListActiveDevices(t.accountID); err == nil {
		devices = len(list)
	}
	latestSeq, _ := t.store.LatestSequence(t.accountID)
	wm := t.client.Watermark()
	pendingTombstones := 0
	if latestSeq > wm {
		pendingTombstones = int(latestSeq - wm)
	}
	pendingAck, _ := t.stateStore.PendingAck(ctx)
	return t.stateStore.SaveStatusMeta(ctx, DeviceStatusMetadata{
		AccountID:         t.accountID,
		DeviceID:          t.deviceID,
		SyncDir:           t.syncDir,
		PendingUploads:    0,
		PendingTombstones: pendingTombstones,
		Conflicts:         len(t.client.Conflicts()),
		Devices:           devices,
		LastSyncAt:        t.lastSyncAt,
		AllowedClasses:    t.plan.AllowedClasses(),
		PendingAck:        pendingAck,
	})
}

func (t *DirTransport) writeImmutableFile(path string, data []byte) error {
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(t.syncDir, cleanPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return errors.New("sync: path escapes sync directory")
	}
	dir := filepath.Dir(cleanPath)
	if err := ensureSafeDir(dir); err != nil {
		return err
	}
	if existing, err := os.Lstat(cleanPath); err == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			return fmt.Errorf("sync: destination %s is not a regular file", rel)
		}
		cur, readErr := os.ReadFile(cleanPath)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(cur, data) {
			return nil
		}
		return fmt.Errorf("sync: immutable artifact %s already exists with different content", rel)
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".clavis-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, cleanPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func ensureSafeDir(dir string) error {
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", dir)
	}
	return nil
}

func provisionPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

func readRegularFileLimited(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("sync: symlink rejected: %s", filepath.Base(path))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("sync: non-regular file rejected: %s", filepath.Base(path))
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("sync: file exceeds size limit: %s", filepath.Base(path))
	}
	return os.ReadFile(path)
}

func safePathSegment(seg string) error {
	if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
		return errors.New("empty or dot-prefixed segment")
	}
	if strings.ContainsAny(seg, "/\\\x00") || strings.Contains(seg, "..") {
		return errors.New("path traversal rejected")
	}
	for _, r := range seg {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == '@' {
			continue
		}
		return fmt.Errorf("unsupported character %q", r)
	}
	return nil
}

func isJSONArtifact(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp") {
		return false
	}
	return strings.HasSuffix(name, ".json")
}
