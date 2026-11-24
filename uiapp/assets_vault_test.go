package uiapp

import (
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

func TestAssetsHubCmd9ThreeTabsAndVaultUI(t *testing.T) {
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

	if _, err := ctrl.VaultPutSecret("ref-ui-1", "clavis.staging.key", "Staging Key", secrets.SecretKindPassword, "val-hidden-ui"); err != nil {
		t.Fatal(err)
	}
	ctrl.Dispatch(engine.Event{
		Type: engine.EventSetClipboardEntries,
		ClipboardEntries: []engine.OmniboxClipboardEntry{{
			ID:            "pin-1",
			Content:       "pinned snippet text",
			IsPinned:      true,
			SourceAppName: "Notes",
			CopiedAt:      time.Now(),
		}},
	})
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.ToggleHubSurface(HubSurfaceAssets)

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	// Switch to Pinned & Templates sub-tab.
	pinnedLbl := "Pinned"
	if _, ok := tt.Find("置顶与模板"); ok {
		pinnedLbl = "置顶与模板"
	}
	if err := tt.Click(pinnedLbl); err != nil {
		t.Fatalf("click Pinned sub-tab: %v (%v)", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("pinned snippet text") {
		t.Fatalf("pinned item missing on Pinned sub-tab: %v", tt.Texts())
	}

	// Switch to Vault sub-tab and verify secret plaintext is never rendered.
	vaultLbl := "Vault"
	if _, ok := tt.Find("密钥库"); ok {
		vaultLbl = "密钥库"
	}
	if err := tt.Click(vaultLbl); err != nil {
		t.Fatalf("click Vault sub-tab: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Staging Key") {
		t.Fatalf("Vault item label missing: %v", tt.Texts())
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
