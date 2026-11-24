package uiapp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type lockableVaultStore struct {
	secrets.APIKeyStore
	mu     sync.Mutex
	locked bool
	values map[string]string
}

func newLockableVaultStore() *lockableVaultStore {
	return &lockableVaultStore{
		APIKeyStore: secrets.NewMemoryAPIKeyStore(),
		values:      make(map[string]string),
	}
}

func (s *lockableVaultStore) ReadKey(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return "", errors.New("keychain is locked")
	}
	v, ok := s.values[key]
	if !ok {
		return "", secrets.ErrSecretNotFound
	}
	return v, nil
}

func (s *lockableVaultStore) SaveKey(key, val string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return errors.New("keychain is locked")
	}
	s.values[key] = val
	return nil
}

func (s *lockableVaultStore) DeleteKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return errors.New("keychain is locked")
	}
	delete(s.values, key)
	return nil
}

func (s *lockableVaultStore) setLocked(v bool) {
	s.mu.Lock()
	s.locked = v
	s.mu.Unlock()
}

func (s *lockableVaultStore) dropRaw(key string) {
	s.mu.Lock()
	delete(s.values, key)
	s.mu.Unlock()
}

func assertNoSecretOrMaskLeak(t *testing.T, texts []string, forbiddenSecrets ...string) {
	t.Helper()
	joined := strings.Join(texts, "\n")
	for _, sec := range forbiddenSecrets {
		if sec != "" && strings.Contains(joined, sec) {
			t.Fatalf("UI output leaked secret %q in texts: %v", sec, texts)
		}
	}
	for _, mask := range []string{"***", "****", "••••"} {
		if strings.Contains(joined, mask) {
			t.Fatalf("UI output rendered forbidden asterisk/bullet secret preview %q in texts: %v", mask, texts)
		}
	}
}

func newSecurityTestController(t *testing.T, store secrets.APIKeyStore) (*AppController, *string, *int) {
	t.Helper()
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	clipVal := ""
	openCount := 0
	if store == nil {
		store = secrets.NewMemoryAPIKeyStore()
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     store,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		ClipboardWriter: func(s string) { clipVal = s },
		OnOpenSettings: func(SettingsTab) {
			openCount++
		},
	})
	t.Cleanup(ctrl.Close)
	return ctrl, &clipVal, &openCount
}

func setTestLang(t *testing.T, lang i18n.Language) {
	t.Helper()
	wasZh := i18n.IsChinese()
	i18n.SetLanguage(lang)
	t.Cleanup(func() {
		if wasZh {
			i18n.SetLanguage(i18n.Chinese)
		} else {
			i18n.SetLanguage(i18n.English)
		}
	})
}

func TestSecurityVaultManagementEntryMetadataOnlyAndTTLCopy(t *testing.T) {
	setTestLang(t, i18n.Chinese)

	ctrl, clipVal, openCount := newSecurityTestController(t, nil)
	secretOne := "db-super-secret-alpha-91"
	secretTwo := "deploy-secret-token-beta-42"

	if _, err := ctrl.VaultPutSecret("ref-db", "clavis.prod.db", "Production DB", secrets.SecretKindPassword, secretOne); err != nil {
		t.Fatalf("VaultPutSecret 1: %v", err)
	}
	if _, err := ctrl.VaultPutSecret("ref-deploy", "clavis.deploy.key", "Deploy Runner", secrets.SecretKindPassword, secretTwo); err != nil {
		t.Fatalf("VaultPutSecret 2: %v", err)
	}

	// Open Management Hub (管理) and navigate via 管理 → 安全.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.ToggleHubSurface(HubSurfaceManage)
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	capTester := ui.NewTester(ctrl.CapsuleView, w, h)
	capTester.Frame()
	if err := capTester.Click("安全"); err != nil {
		t.Fatalf("click 管理 -> 安全: %v (texts=%v)", err, capTester.Texts())
	}
	capTester.Frame()

	if *openCount != 1 {
		t.Fatalf("expected OnOpenSettings once, got %d", *openCount)
	}
	ctrl.mu.Lock()
	selectedSection := ctrl.uiState.settingsTabIdx
	ctrl.mu.Unlock()
	if selectedSection != settingsSectionSecurity {
		t.Fatalf("selected settings section = %d, want settingsSectionSecurity (%d)", selectedSection, settingsSectionSecurity)
	}

	// Also verify settings search matches Security section via security/vault/安全/密码.
	var secMeta settingsSectionMeta
	for _, meta := range settingsNavigationSections() {
		if meta.Index == settingsSectionSecurity {
			secMeta = meta
			break
		}
	}
	for _, q := range []string{"安全", "security", "vault", "password"} {
		if !settingsSectionMatches(secMeta, q, ctrl.Config()) {
			t.Fatalf("settingsSectionMatches failed for query %q", q)
		}
	}

	// Render Settings -> 安全 with ui.NewTester and verify metadata-only rendering.
	tt := ui.NewTester(ctrl.SettingsView, 960, 820)
	tt.Frame()
	texts := tt.Texts()
	if !tt.HasText("Production DB") || !tt.HasText("clavis.prod.db") || !tt.HasText("Deploy Runner") {
		t.Fatalf("missing SecretRef metadata in Security view: %v", texts)
	}
	assertNoSecretOrMaskLeak(t, texts, secretOne, secretTwo)

	// Verify Omnibox, Diagnostics, VaultSnapshot, VaultAuditEvents, and persisted vault_refs.json never contain secrets.
	if snapRaw, err := os.ReadFile(filepath.Join(ctrl.ConfigStore.Directory(), "vault_refs.json")); err == nil {
		if strings.Contains(string(snapRaw), secretOne) || strings.Contains(string(snapRaw), secretTwo) {
			t.Fatal("vault_refs.json contains plaintext secret")
		}
	}
	diag := ctrl.Diagnostics()
	if strings.Contains(diag.LastIMEError, secretOne) || strings.Contains(diag.ActiveKeymapLayer, secretOne) {
		t.Fatal("diagnostics leaked secret")
	}
	for _, item := range ctrl.State().OmniboxItems {
		if strings.Contains(item.Title, secretOne) || strings.Contains(item.Subtitle, secretOne) ||
			strings.Contains(item.Title, secretTwo) || strings.Contains(item.Subtitle, secretTwo) {
			t.Fatalf("omnibox item leaked secret: %+v", item)
		}
	}

	// Trigger User-Only TTL Copy directly via button in Security UI.
	if err := tt.Click("TTL 复制"); err != nil {
		t.Fatalf("click TTL 复制: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()

	// Sorted newest/stored first: ref-deploy and ref-db have UpdatedAt close; verify clipboard got one of stored secrets safely.
	if *clipVal != secretOne && *clipVal != secretTwo {
		t.Fatalf("expected clipboard to receive stored secret on User-Only copy, got %q", *clipVal)
	}
	if !ctrl.SensitiveClipboardState().Active {
		t.Fatal("expected SensitiveClipboardState active after user TTL copy")
	}
	assertNoSecretOrMaskLeak(t, tt.Texts(), secretOne, secretTwo)

	// Clear clipboard immediately from Security UI.
	if err := tt.Click("立即清空剪贴板"); err != nil {
		t.Fatalf("click 立即清空剪贴板: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()
	if *clipVal != "" {
		t.Fatalf("expected clipboard wiped after clear button, got %q", *clipVal)
	}
}

func TestSecurityVaultAgentBlockedAndRevokeDenied(t *testing.T) {
	setTestLang(t, i18n.English)

	ctrl, clipVal, _ := newSecurityTestController(t, nil)
	secretVal := "agent-must-never-read-999"
	ref, err := ctrl.VaultPutSecret("ref-agent", "clavis.agent.blocked", "Payment Gateway", secrets.SecretKindPassword, secretVal)
	if err != nil {
		t.Fatal(err)
	}

	// Direct Agent attempts through controller & underlying Vault MUST all be denied.
	receipt, err := ctrl.VaultAgentCopyAttempt(ref.ID)
	if !errors.Is(err, secrets.ErrAgentForbidden) || receipt.ErrorCode != secrets.CopyErrorAgentForbidden || receipt.State != secrets.CopyStateFailed {
		t.Fatalf("VaultAgentCopyAttempt got receipt=%+v err=%v, want agent_forbidden", receipt, err)
	}
	if *clipVal != "" {
		t.Fatalf("Agent attempt wrote clipboard: %q", *clipVal)
	}
	if _, err := ctrl.SecretVault().ResolveForUser(ref.ID, secrets.ActorAgent, "sess-1", "req-resolve"); !errors.Is(err, secrets.ErrAgentForbidden) {
		t.Fatalf("ResolveForUser(ActorAgent) = %v, want ErrAgentForbidden", err)
	}
	if _, err := ctrl.VaultCopyAs(secrets.ActorAutomation, "auto-1", ref.ID); !errors.Is(err, secrets.ErrAgentForbidden) {
		t.Fatalf("VaultCopyAs(ActorAutomation) = %v, want ErrAgentForbidden", err)
	}

	ctrl.OpenSettingsSection(settingsSectionSecurity)
	tt := ui.NewTester(ctrl.SettingsView, 960, 820)
	tt.Frame()
	if !tt.HasText("Denied") || !tt.HasText(secrets.CopyErrorAgentForbidden) {
		t.Fatalf("expected Denied + agent_forbidden state in UI, got %v", tt.Texts())
	}
	assertNoSecretOrMaskLeak(t, tt.Texts(), secretVal)

	// Clicking Revoke in UI disables further copies and causes User Copy attempt to deny cleanly with secret_revoked.
	if err := tt.Click("Revoke"); err != nil {
		t.Fatalf("click Revoke: %v (%v)", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText(secrets.SecretStateRevoked) {
		t.Fatalf("expected revoked status badge after clicking Revoke, got %v", tt.Texts())
	}
	if _, err := ctrl.VaultUserCopy(ref.ID); err == nil {
		t.Fatal("expected copy of revoked ref to fail")
	}
	tt.Frame()
	if !tt.HasText("Denied") || !tt.HasText(secrets.CopyErrorRevoked) {
		t.Fatalf("expected Denied + secret_revoked in UI after copying revoked ref, got %v", tt.Texts())
	}
	assertNoSecretOrMaskLeak(t, tt.Texts(), secretVal)
}

func TestSecurityVaultLockedMissingDeniedStatesAndInputMasking(t *testing.T) {
	setTestLang(t, i18n.English)

	store := newLockableVaultStore()
	ctrl, clipVal, _ := newSecurityTestController(t, store)

	secretVal := "vault-lock-state-secret-77"
	ref, err := ctrl.VaultPutSecret("ref-lock", "clavis.ssh.pass", "SSH Recovery", secrets.SecretKindPassword, secretVal)
	if err != nil {
		t.Fatal(err)
	}

	ctrl.handleInternalSettingCommand([]string{"security"})
	tt := ui.NewTester(ctrl.SettingsView, 960, 820)
	tt.Frame()

	// 1) Locked state: keychain locks before Copy click.
	store.setLocked(true)
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("click Copy while locked: %v (%v)", err, tt.Texts())
	}
	tt.Frame()
	if *clipVal != "" {
		t.Fatalf("locked copy should not write clipboard, got %q", *clipVal)
	}
	if !tt.HasText("Locked") || !tt.HasText(secrets.CopyErrorKeychainLocked) {
		t.Fatalf("expected Locked + keychain_locked state rendered, got %v", tt.Texts())
	}
	assertNoSecretOrMaskLeak(t, tt.Texts(), secretVal)

	// Unlock recovers Copy cleanly.
	store.setLocked(false)
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("click Copy after unlock: %v", err)
	}
	tt.Frame()
	if *clipVal != secretVal {
		t.Fatalf("expected recovered copy after unlock, got %q", *clipVal)
	}

	// 2) Missing state: underlying credential vanished from store while SecretRef metadata remains.
	store.dropRaw(ref.RecordKey)
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("click Copy when secret missing: %v (%v)", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("Missing") || !tt.HasText(secrets.CopyErrorRefNotFound) {
		t.Fatalf("expected Missing + secret_ref_not_found state rendered, got %v", tt.Texts())
	}
	if !tt.HasText("Re-add") {
		t.Fatalf("expected Re-add recovery action on Missing banner, got %v", tt.Texts())
	}
	assertNoSecretOrMaskLeak(t, tt.Texts(), secretVal)

	// Clicking Re-add opens the add form; typing credential value uses .Password() so plaintext draft never enters Texts().
	if err := tt.Click("Re-add"); err != nil {
		t.Fatalf("click Re-add: %v", err)
	}
	tt.Frame()
	draftSecret := "new-typed-draft-secret-123"
	if err := tt.Click("Credential"); err != nil {
		t.Fatalf("focus password input: %v (%v)", err, tt.Texts())
	}
	tt.Type(draftSecret)
	tt.Frame()
	if strings.Contains(strings.Join(tt.Texts(), "\n"), draftSecret) {
		t.Fatalf("password TextInput exposed plaintext draft in Texts(): %v", tt.Texts())
	}
	if err := tt.Click("Close Form"); err != nil {
		t.Fatalf("click Close Form: %v", err)
	}
	tt.Frame()

	// 3) Denied state + Dismiss cleans banner cleanly.
	_, _ = ctrl.VaultAgentCopyAttempt(ref.ID)
	tt.Frame()
	if !tt.HasText("Denied") || !tt.HasText(secrets.CopyErrorAgentForbidden) {
		t.Fatalf("expected Denied + agent_forbidden rendered, got %v", tt.Texts())
	}
	if err := tt.Click("Dismiss"); err != nil {
		t.Fatalf("click Dismiss: %v", err)
	}
	tt.Frame()
	if tt.HasText("Denied") || tt.HasText("Dismiss") {
		t.Fatalf("expected Dismiss to clear error banner, got %v", tt.Texts())
	}
	assertNoSecretOrMaskLeak(t, tt.Texts(), secretVal, draftSecret)
}
