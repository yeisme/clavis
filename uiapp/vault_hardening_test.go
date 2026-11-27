package uiapp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type seqMockBridge struct {
	*mockBridge
	seq     atomic.Int64
	mu      sync.Mutex
	ignored []int64
}

func newSeqMockBridge(start int64) *seqMockBridge {
	b := &seqMockBridge{mockBridge: newMockBridge()}
	b.seq.Store(start)
	return b
}

func (b *seqMockBridge) ClipboardSequence() int64 {
	return b.seq.Load()
}

func (b *seqMockBridge) IgnoreSelfPasteboardChange(changeCount int64) {
	b.mu.Lock()
	b.ignored = append(b.ignored, changeCount)
	b.mu.Unlock()
}

func (b *seqMockBridge) bump() int64 {
	return b.seq.Add(1)
}

func (b *seqMockBridge) ignoredCounts() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int64(nil), b.ignored...)
}

var _ bridge.ClipboardSequencer = (*seqMockBridge)(nil)

func TestInitSecretVaultUsesEncryptedStoreAndIsolatesAIKeysToml(t *testing.T) {
	for _, tc := range []struct {
		name      string
		makeStore func(dir string) secrets.APIKeyStore
	}{
		{
			name: "LayeredStore",
			makeStore: func(dir string) secrets.APIKeyStore {
				return &secrets.LayeredStore{
					File: secrets.NewFileStore(secrets.DefaultKeysPath(dir)),
				}
			},
		},
		{
			name: "FileStore",
			makeStore: func(dir string) secrets.APIKeyStore {
				return secrets.NewFileStore(secrets.DefaultKeysPath(dir))
			},
		},
		{
			name:      "NilSecretStoreWithConfigDir",
			makeStore: func(string) secrets.APIKeyStore { return nil },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgStore := config.NewConfigStore(dir)
			if _, err := cfgStore.LoadOrInitialize(); err != nil {
				t.Fatal(err)
			}
			var clipVal string
			ctrl := NewAppController(Options{
				ConfigStore:     cfgStore,
				SecretStore:     tc.makeStore(dir),
				ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
				HerdrExecutor:   disabledHerdrExecutor{},
				ClipboardWriter: func(s string) { clipVal = s },
			})
			t.Cleanup(ctrl.Close)
			if tc.name == "NilSecretStoreWithConfigDir" {
				ctrl.SecretStore = nil
				ctrl.initSecretVault(nil)
			}

			secretVal := "vault-enc-password-42!"
			ref, err := ctrl.VaultPutSecret("ref-enc", "clavis.prod.vault", "Prod Vault", secrets.SecretKindPassword, secretVal)
			if err != nil {
				t.Fatalf("VaultPutSecret: %v", err)
			}
			if ref.Backend != secrets.SecretBackendEncrypted {
				t.Fatalf("ref.Backend = %q, want %q", ref.Backend, secrets.SecretBackendEncrypted)
			}

			// Vault secret must live in encrypted vault_secrets.enc and NEVER in ai-keys.toml.
			if _, err := os.Stat(filepath.Join(dir, "ai-keys.toml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("VaultPutSecret must not write plaintext ai-keys.toml (stat err=%v)", err)
			}
			encRaw, err := os.ReadFile(filepath.Join(dir, "vault_secrets.enc"))
			if err != nil {
				t.Fatalf("expected vault_secrets.enc: %v", err)
			}
			if strings.Contains(string(encRaw), secretVal) {
				t.Fatal("vault_secrets.enc contains plaintext secret")
			}

			// Fresh controller restart decrypts and copies the secret cleanly.
			var clipVal2 string
			ctrl2 := NewAppController(Options{
				ConfigStore:     cfgStore,
				SecretStore:     tc.makeStore(dir),
				ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
				HerdrExecutor:   disabledHerdrExecutor{},
				ClipboardWriter: func(s string) { clipVal2 = s },
			})
			t.Cleanup(ctrl2.Close)
			if tc.name == "NilSecretStoreWithConfigDir" {
				ctrl2.SecretStore = nil
				ctrl2.initSecretVault(nil)
			}

			receipt, err := ctrl2.VaultUserCopy(ref.ID)
			if err != nil || receipt.State != secrets.CopyStateCopied || clipVal2 != secretVal {
				t.Fatalf("restarted VaultUserCopy = receipt=%+v clip=%q err=%v", receipt, clipVal2, err)
			}
			_ = ctrl2.ClearSensitiveClipboardNow()
			_ = clipVal

			// Revoke destroys the backing secret from encrypted storage.
			if err := ctrl2.VaultRevoke(ref.ID); err != nil {
				t.Fatalf("VaultRevoke: %v", err)
			}
			encStore := secrets.NewEncryptedVaultStore(dir, nil)
			if _, err := encStore.ReadKey(ref.RecordKey); !errors.Is(err, secrets.ErrSecretNotFound) {
				t.Fatalf("backing encrypted secret survived VaultRevoke: err=%v", err)
			}
		})
	}
}

func TestVaultHostClipboardOwnershipSequenceHashAndSingleClearTimer(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	br := newSeqMockBridge(10)
	var clipMu sync.Mutex
	clipVal := ""
	var clearCalls atomic.Int32

	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:  disabledHerdrExecutor{},
		Bridge:         br,
		ClipboardWriter: func(s string) {
			clipMu.Lock()
			clipVal = s
			clipMu.Unlock()
			br.bump()
		},
	})
	t.Cleanup(ctrl.Close)

	ctrl.SetSensitiveClipboardReadHook(func() (string, bool) {
		clipMu.Lock()
		defer clipMu.Unlock()
		return clipVal, true
	})
	ctrl.SetSensitiveClipboardClearHook(func() error {
		clearCalls.Add(1)
		clipMu.Lock()
		clipVal = ""
		clipMu.Unlock()
		br.bump()
		return nil
	})

	ref, err := ctrl.VaultPutSecret("ref-own", "clavis.own.db", "Own DB", secrets.SecretKindPassword, "vault-own-secret")
	if err != nil {
		t.Fatal(err)
	}

	// 1) Normal Vault copy + direct Cleanup when still owned clears EXACTLY once and marks IgnoreSelfPasteboardChange & history skip.
	receipt, err := ctrl.VaultUserCopy(ref.ID)
	if err != nil || receipt.State != secrets.CopyStateCopied {
		t.Fatalf("VaultUserCopy: %+v, %v", receipt, err)
	}
	if len(br.ignoredCounts()) == 0 {
		t.Fatal("expected hostVaultClipboard.Write to mark IgnoreSelfPasteboardChange")
	}
	if !ctrl.ShouldSkipClipboardHistory(clipboard.CaptureCandidate{Content: "vault-own-secret"}) {
		t.Fatal("expected Vault secret hash to be skipped by clipboard history poller")
	}
	if err := ctrl.ClearSensitiveClipboardNow(); err != nil {
		t.Fatalf("ClearSensitiveClipboardNow: %v", err)
	}
	if got := clearCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 clear call, got %d", got)
	}
	if ctrl.SensitiveClipboardState().Active {
		t.Fatal("expected SensitiveClipboardState inactive after clear")
	}

	// 2) Short-TTL Vault copy where user replaces clipboard BEFORE TTL expires -> MUST NOT clear user's new content.
	clearCalls.Store(0)
	shortReq := secrets.UserCopyRequest{
		RequestID:   "req-short-replaced",
		SecretRefID: ref.ID,
		Actor:       secrets.ActorUserSession,
		SessionID:   "sess-ui",
		TTL:         60 * time.Millisecond,
		Destination: secrets.CopyDestinationClipboard,
	}
	if _, err := ctrl.SecretVault().Copy(shortReq); err != nil {
		t.Fatalf("Vault.Copy short: %v", err)
	}
	// Simulate user copying something else in another app (updates both clipboard text and OS sequence).
	clipMu.Lock()
	clipVal = "user-copied-something-else"
	clipMu.Unlock()
	br.bump()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r, rerr := ctrl.SecretVault().Receipt(shortReq.RequestID, ref.ID, shortReq.SessionID)
		if rerr == nil && r.State == secrets.CopyStateOwnershipLost {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, rerr := ctrl.SecretVault().Receipt(shortReq.RequestID, ref.ID, shortReq.SessionID)
	if rerr != nil || r.State != secrets.CopyStateOwnershipLost {
		t.Fatalf("expected ownership_lost receipt after user replaced clipboard, got %+v err=%v", r, rerr)
	}
	clipMu.Lock()
	afterClip := clipVal
	clipMu.Unlock()
	if afterClip != "user-copied-something-else" {
		t.Fatalf("TTL expiry overwrote user's replacement clipboard: %q", afterClip)
	}
	if clearCalls.Load() != 0 {
		t.Fatalf("expected 0 clear calls after ownership loss, got %d", clearCalls.Load())
	}

	// 3) Short-TTL Vault copy where clipboard is untouched -> single timer clears ONCE.
	shortOwnedReq := secrets.UserCopyRequest{
		RequestID:   "req-short-owned",
		SecretRefID: ref.ID,
		Actor:       secrets.ActorUserSession,
		SessionID:   "sess-ui",
		TTL:         50 * time.Millisecond,
		Destination: secrets.CopyDestinationClipboard,
	}
	if _, err := ctrl.SecretVault().Copy(shortOwnedReq); err != nil {
		t.Fatalf("Vault.Copy owned: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r2, err2 := ctrl.SecretVault().Receipt(shortOwnedReq.RequestID, ref.ID, shortOwnedReq.SessionID)
		if err2 == nil && r2.State == secrets.CopyStateExpired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond) // ensure no duplicate second timer fires
	if got := clearCalls.Load(); got != 1 {
		t.Fatalf("expected single TTL clear call, got %d", got)
	}

	// 4) Hash mismatch alone (e.g. Linux without ClipboardSequencer) ALSO prevents overwrite.
	ctrlNoSeq, clipNoSeq, _ := newSecurityTestController(t, nil)
	var noSeqMu sync.Mutex
	ctrlNoSeq.ClipboardWriter = func(s string) {
		noSeqMu.Lock()
		*clipNoSeq = s
		noSeqMu.Unlock()
	}
	ctrlNoSeq.SetSensitiveClipboardReadHook(func() (string, bool) {
		noSeqMu.Lock()
		defer noSeqMu.Unlock()
		return *clipNoSeq, true
	})
	adapter := &hostVaultClipboard{ctrl: ctrlNoSeq}
	tok, err := adapter.Write("hash-only-secret")
	if err != nil {
		t.Fatal(err)
	}
	noSeqMu.Lock()
	*clipNoSeq = "user-new-text"
	noSeqMu.Unlock()
	if err := adapter.ClearIfOwned(tok); !errors.Is(err, secrets.ErrOwnershipLost) {
		t.Fatalf("ClearIfOwned with hash mismatch = %v, want ErrOwnershipLost", err)
	}
	noSeqMu.Lock()
	gotNoSeq := *clipNoSeq
	noSeqMu.Unlock()
	if gotNoSeq != "user-new-text" {
		t.Fatalf("hash mismatch overwrote clipboard: %q", gotNoSeq)
	}
}

func TestCopySensitiveUserOnlyOwnershipCheckBeforeClear(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	br := newSeqMockBridge(100)
	var mu sync.Mutex
	clipVal := ""
	var clearCalls atomic.Int32

	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:  disabledHerdrExecutor{},
		Bridge:         br,
		ClipboardWriter: func(s string) {
			mu.Lock()
			clipVal = s
			mu.Unlock()
			br.bump()
		},
	})
	t.Cleanup(ctrl.Close)

	ctrl.SetSensitiveClipboardReadHook(func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		return clipVal, true
	})
	ctrl.SetSensitiveClipboardClearHook(func() error {
		clearCalls.Add(1)
		mu.Lock()
		clipVal = ""
		mu.Unlock()
		br.bump()
		return nil
	})

	// 1) User replaces clipboard before TTL expires -> auto-clear must skip and preserve user text.
	_, err := ctrl.ExecuteClipboardAction(ClipboardCopyRequest{
		Origin:      ClipboardCopyOriginUser,
		Action:      config.ClipboardActionCopySensitive,
		Sensitive:   true,
		SecretLabel: "Temp OTP",
		TTL:         60 * time.Millisecond,
		Item:        clipboard.ClipboardItem{ID: "otp-1", Content: "otp-secret-123"},
	})
	if err != nil {
		t.Fatalf("ExecuteClipboardAction sensitive: %v", err)
	}
	mu.Lock()
	clipVal = "user-replacement-text"
	mu.Unlock()
	br.bump()

	waitUntil(t, time.Second, func() bool {
		return !ctrl.SensitiveClipboardState().Active
	}, "sensitive TTL disarms after expiry")
	mu.Lock()
	got := clipVal
	mu.Unlock()
	if got != "user-replacement-text" {
		t.Fatalf("copySensitiveUserOnly overwrote user clipboard: %q", got)
	}
	if clearCalls.Load() != 0 {
		t.Fatalf("expected 0 clears when clipboard changed, got %d", clearCalls.Load())
	}

	// 2) Clipboard unchanged -> auto-clear runs once.
	_, err = ctrl.ExecuteClipboardAction(ClipboardCopyRequest{
		Origin:      ClipboardCopyOriginUser,
		Action:      config.ClipboardActionCopySensitive,
		Sensitive:   true,
		SecretLabel: "Temp OTP 2",
		TTL:         60 * time.Millisecond,
		Item:        clipboard.ClipboardItem{ID: "otp-2", Content: "otp-secret-456"},
	})
	if err != nil {
		t.Fatalf("ExecuteClipboardAction sensitive 2: %v", err)
	}
	waitUntil(t, time.Second, func() bool {
		return !ctrl.SensitiveClipboardState().Active
	}, "sensitive TTL expires and clears")
	if clearCalls.Load() != 1 {
		t.Fatalf("expected 1 clear when clipboard still owned, got %d", clearCalls.Load())
	}
}

func TestVaultInvalidInputAndDuplicateKeyNotClassifiedAsDenied(t *testing.T) {
	setTestLang(t, i18n.English)

	ctrl, _, _ := newSecurityTestController(t, nil)
	if _, err := ctrl.VaultPutSecret("ref-orig", "clavis.prod.db", "Production DB", secrets.SecretKindPassword, "orig-secret-val"); err != nil {
		t.Fatal(err)
	}

	// Invalid label error must classify as vaultStatusInvalid, NEVER vaultStatusDenied.
	_, labelErr := ctrl.VaultPutSecret("", "clavis.bad.label", "My Password", secrets.SecretKindPassword, "some-val")
	if labelErr == nil {
		t.Fatal("expected forbidden label to fail")
	}
	kind, code, detail := classifyVaultFailure(secrets.CopyReceipt{}, labelErr, false)
	if kind == vaultStatusDenied || kind != vaultStatusInvalid {
		t.Fatalf("forbidden label classified as kind=%q code=%q detail=%q; want vaultStatusInvalid (not denied)", kind, code, detail)
	}
	if strings.Contains(strings.ToLower(detail), "active user session") {
		t.Fatalf("forbidden label detail misreported as session denied: %q", detail)
	}

	// Duplicate RecordKey on new entry (id == "") must fail cleanly and NOT overwrite original secret.
	_, dupErr := ctrl.VaultPutSecret("", "clavis.prod.db", "Another Alias", secrets.SecretKindPassword, "clobber-attempt")
	if !errors.Is(dupErr, secrets.ErrDuplicateRecordKey) {
		t.Fatalf("duplicate RecordKey err = %v, want ErrDuplicateRecordKey", dupErr)
	}
	dupKind, dupCode, dupDetail := classifyVaultFailure(secrets.CopyReceipt{}, dupErr, false)
	if dupKind != vaultStatusInvalid || dupCode != secrets.CopyErrorInvalidInput {
		t.Fatalf("duplicate RecordKey classified as (%q, %q, %q), want (invalid, invalid_input)", dupKind, dupCode, dupDetail)
	}

	// Render status banner in Security UI and confirm it displays Invalid Input, not Denied.
	uiSt := vaultUIState(ctrl)
	uiSt.mu.Lock()
	uiSt.setStatusLocked(kind, code, detail)
	uiSt.mu.Unlock()
	ctrl.OpenSettingsPage("security")
	tt := ui.NewTester(ctrl.SettingsView, 960, 820)
	tt.Frame()
	if !tt.HasText("Invalid Input") {
		t.Fatalf("expected 'Invalid Input' status badge, got %v", tt.Texts())
	}
	if tt.HasText("Denied") {
		t.Fatalf("invalid label must not render 'Denied' badge: %v", tt.Texts())
	}
}
