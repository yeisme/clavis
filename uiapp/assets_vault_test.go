package uiapp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestVaultMetadataPersistenceUserCopyAndRevoke(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	secStore := secrets.NewMemoryAPIKeyStore()
	var clipboardValue string

	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		SecretStore:    secStore,
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:  disabledHerdrExecutor{},
		ClipboardWriter: func(s string) {
			clipboardValue = s
		},
	})
	t.Cleanup(ctrl.Close)

	// Store secret via Vault wrapper; value never enters vault_refs.json.
	secretVal := "s3cr3t-p@ssphrase-xyz"
	ref, err := ctrl.VaultPutSecret("ref-prod", "clavis.prod.db", "Production DB", secrets.SecretKindPassword, secretVal)
	if err != nil {
		t.Fatalf("VaultPutSecret: %v", err)
	}
	metaRaw, err := os.ReadFile(filepath.Join(dir, "vault_refs.json"))
	if err != nil {
		t.Fatalf("expected vault_refs.json: %v", err)
	}
	if strings.Contains(string(metaRaw), secretVal) {
		t.Fatalf("vault_refs.json leaked secret plaintext!")
	}

	// User-Only TTL copy copies to clipboard and updates Header TTL state.
	receipt, err := ctrl.VaultUserCopy(ref.ID)
	if err != nil {
		t.Fatalf("VaultUserCopy: %v", err)
	}
	if receipt.State != secrets.CopyStateCopied || clipboardValue != secretVal {
		t.Fatalf("unexpected VaultUserCopy receipt=%+v clipboard=%q", receipt, clipboardValue)
	}
	if !ctrl.SensitiveClipboardState().Active {
		t.Fatal("expected SensitiveClipboardState Active after VaultUserCopy")
	}

	// Immediate Clear Now wipes clipboard.
	if err := ctrl.ClearSensitiveClipboardNow(); err != nil {
		t.Fatalf("ClearSensitiveClipboardNow: %v", err)
	}
	if clipboardValue != "" {
		t.Fatalf("expected cleared clipboard, got %q", clipboardValue)
	}

	// Revoke blocks subsequent copy attempts cleanly.
	if err := ctrl.VaultRevoke(ref.ID); err != nil {
		t.Fatalf("VaultRevoke: %v", err)
	}
	if _, err := ctrl.VaultUserCopy(ref.ID); err == nil {
		t.Fatal("expected VaultUserCopy on revoked ref to fail")
	}
	if events := ctrl.VaultAuditEvents(); len(events) < 3 {
		t.Fatalf("expected stored + copied + revoked + copy_failed audit events, got %+v", events)
	}
}

func TestCmd9AssetsHubRemovedAndVaultSettingsUIHidesPlaintext(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     secrets.NewMemoryAPIKeyStore(),
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	if entries := registeredHubEntries(); len(entries) != 2 {
		t.Fatalf("registeredHubEntries len = %d, want 2 (Stats + Manage only)", len(entries))
	}

	if _, err := ctrl.VaultPutSecret("ref-ui-1", "clavis.staging.key", "Staging Key", secrets.SecretKindPassword, "val-hidden-ui"); err != nil {
		t.Fatal(err)
	}
	ctrl.OpenSettingsPage("security")

	tt := ui.NewTester(ctrl.SettingsView, 920, 620)
	tt.Frame()
	if !tt.HasText("Staging Key") {
		t.Fatalf("Vault item label missing in Settings -> Security: %v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "val-hidden-ui") {
			t.Fatalf("Vault UI leaked secret value: %q", txt)
		}
	}
}

func TestCopyMatrixActionsMarkdownPathRevealAndMeta(t *testing.T) {
	var copied, revealed string
	imgPath := filepath.Join(t.TempDir(), "shot.png")
	_ = os.WriteFile(imgPath, makeSolidPNG(12, 12), 0o600)

	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		ClipboardWriter: func(s string) { copied = s },
		RevealPath:      func(p string) error { revealed = p; return nil },
	})
	t.Cleanup(ctrl.Close)

	// Text item -> copy_md wraps fenced code block.
	ctrl.Dispatch(engine.Event{
		Type: engine.EventSetClipboardEntries,
		ClipboardEntries: []engine.OmniboxClipboardEntry{{
			ID:       "t-1",
			Content:  "fmt.Println(42)",
			CopiedAt: time.Now(),
		}},
	})
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	ctrl.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "copy_md"})
	if copied != "```\nfmt.Println(42)\n```" {
		t.Fatalf("copy_md = %q", copied)
	}

	// Media item -> copy_path, reveal, copy_meta.
	ctrl.Dispatch(engine.Event{
		Type: engine.EventSetClipboardEntries,
		ClipboardEntries: []engine.OmniboxClipboardEntry{{
			ID:       "m-1",
			Content:  "shot.png",
			CopiedAt: time.Now(),
			Media:    engine.ClipboardMedia{Kind: "image", Path: imgPath, Mime: "image/png", Width: 12, Height: 12, ByteSize: 100},
		}},
	})
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	ctrl.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "copy_path"})
	if copied != imgPath {
		t.Fatalf("copy_path = %q, want %q", copied, imgPath)
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	ctrl.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "reveal"})
	if revealed != imgPath {
		t.Fatalf("reveal = %q, want %q", revealed, imgPath)
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	ctrl.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "copy_meta"})
	if !strings.Contains(copied, "dimensions: 12x12") || !strings.Contains(copied, "kind: image") {
		t.Fatalf("copy_meta = %q", copied)
	}
}

func TestVaultClipboardOwnershipAndAssetsHubEdgePaths(t *testing.T) {
	dir := t.TempDir()
	// Corrupted vault_refs.json on disk must be ignored cleanly during startup without panicking.
	if err := os.WriteFile(filepath.Join(dir, "vault_refs.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	secStore := secrets.NewMemoryAPIKeyStore()
	clipValue := ""
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     secStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		ClipboardWriter: func(s string) { clipValue = s },
	})
	t.Cleanup(ctrl.Close)

	// hostVaultClipboard ownership isolation + nil guard + clearHook override.
	var nilAdapter *hostVaultClipboard
	if _, err := nilAdapter.Write("v"); err == nil {
		t.Fatal("nil hostVaultClipboard Write should fail")
	}
	if err := nilAdapter.ClearIfOwned("tok"); err == nil {
		t.Fatal("nil hostVaultClipboard ClearIfOwned should fail")
	}

	adapter := &hostVaultClipboard{ctrl: ctrl}
	tok1, err := adapter.Write("first-secret")
	if err != nil || clipValue != "first-secret" {
		t.Fatalf("Write tok1 failed: err=%v clip=%q", err, clipValue)
	}
	tok2, err := adapter.Write("second-secret")
	if err != nil || clipValue != "second-secret" {
		t.Fatalf("Write tok2 failed: err=%v clip=%q", err, clipValue)
	}
	// Stale token MUST fail ownership check and NOT clear second-secret.
	if err := adapter.ClearIfOwned(tok1); !errors.Is(err, secrets.ErrOwnershipLost) {
		t.Fatalf("expected ErrOwnershipLost for stale token, got %v", err)
	}
	if clipValue != "second-secret" {
		t.Fatalf("stale token overwrote clipboard: %q", clipValue)
	}
	// Active token clears cleanly, honoring custom clearHook when configured.
	hookCalls := 0
	ctrl.clipboardKeyManager().mu.Lock()
	ctrl.clipboardKeyManager().clearHook = func() error { hookCalls++; clipValue = "hook-cleared"; return nil }
	ctrl.clipboardKeyManager().mu.Unlock()
	if err := adapter.ClearIfOwned(tok2); err != nil || hookCalls != 1 || clipValue != "hook-cleared" {
		t.Fatalf("ClearIfOwned active token with clearHook failed: err=%v hookCalls=%d clip=%q", err, hookCalls, clipValue)
	}

	// VaultPutSecret errors, version bump on update, VaultRevoke missing error, and VaultSnapshot sort order.
	if _, err := ctrl.VaultPutSecret("bad-empty", "clavis.empty.val", "Empty", secrets.SecretKindPassword, "   "); err == nil {
		t.Fatal("expected VaultPutSecret blank value to fail")
	}
	r1, err := ctrl.VaultPutSecret("ref-a", "clavis.service.alpha", "Alpha", secrets.SecretKindPassword, "secret-alpha-v1")
	if err != nil || r1.Version != 1 {
		t.Fatalf("VaultPutSecret v1 failed: ref=%+v err=%v", r1, err)
	}
	r1Upd, err := ctrl.VaultPutSecret("ref-a", "clavis.service.alpha", "Alpha", secrets.SecretKindPassword, "secret-alpha-v2")
	if err != nil || r1Upd.Version != 2 {
		t.Fatalf("VaultPutSecret update should bump Version=2, got ref=%+v err=%v", r1Upd, err)
	}
	if _, err := ctrl.VaultPutSecret("ref-b", "clavis.service.beta", "Beta", secrets.SecretKindPassword, "secret-beta-v1"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.VaultRevoke("missing-ref"); err == nil {
		t.Fatal("expected VaultRevoke missing ref error")
	}
	if err := ctrl.VaultRevoke("ref-a"); err != nil {
		t.Fatalf("VaultRevoke ref-a failed: %v", err)
	}
	snap := ctrl.VaultSnapshot()
	if len(snap.SecretRefs) != 2 || snap.SecretRefs[0].ID != "ref-b" || snap.SecretRefs[1].ID != "ref-a" {
		t.Fatalf("expected active ref-b sorted before revoked ref-a, got %+v", snap.SecretRefs)
	}
}
