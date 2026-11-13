package uiapp

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/apps"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// interactionFixture wires an AppController with a mock bridge, an in-memory
// clipboard, a mock app provider, and a capturing clipboard writer so the
// CapsuleView interaction tests can observe every dispatch.
type interactionFixture struct {
	ctrl   *AppController
	bridge *mockBridge
	clips  *clipboard.MemoryStore
	apps   *apps.MockAppProvider

	mu     sync.Mutex
	copied []string
}

func newInteractionFixture(t *testing.T, doer ai.HTTPDoer) *interactionFixture {
	t.Helper()
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("CLAVIS_API_KEY", "")

	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}

	clipStore := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	secStore := secrets.NewMemoryAPIKeyStore()
	_ = secStore.SaveKey(cfgStore.Current().Main.AI.KeychainAccount, "sk-test-key")
	br := newMockBridge()
	mockApps := apps.NewMockAppProvider()

	f := &interactionFixture{bridge: br, clips: clipStore, apps: mockApps}
	f.ctrl = NewAppController(Options{
		ConfigStore:    cfgStore,
		ClipboardStore: clipStore,
		SecretStore:    secStore,
		Bridge:         br,
		AppProvider:    mockApps,
		HTTPDoer:       doer,
		ClipboardWriter: func(text string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.copied = append(f.copied, text)
		},
	})
	t.Cleanup(f.ctrl.Close)
	return f
}

func (f *interactionFixture) copiedTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.copied))
	copy(out, f.copied)
	return out
}

func (f *interactionFixture) record(t *testing.T, contents ...string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for i, content := range contents {
		if _, _, err := f.clips.Record(ctx, clipboard.CaptureCandidate{
			Content:       content,
			SourceAppName: "Notes",
			Timestamp:     now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("record %q: %v", content, err)
		}
	}
}

func (f *interactionFixture) omniboxTester(t *testing.T) *ui.Tester {
	t.Helper()
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	return tt
}

func testPNG16(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for x := range 16 {
		for y := range 16 {
			img.Set(x, y, color.NRGBA{R: uint8(x * 12), G: uint8(y * 12), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode 16x16 PNG: %v", err)
	}
	return buf.Bytes()
}

// TestLeaderSlashJumpsToCleanOmnibox verifies the new "/" instant-search key.
func TestLeaderSlashJumpsToCleanOmnibox(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenLeader,
		Context: &engine.CapsuleContext{
			AppName:      "Notes",
			SelectedText: "hello world",
		},
	})

	w, h := PreferredCapsuleSize(engine.PhaseLeader, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()

	if !tt.HasText("/ Search") {
		t.Fatalf("expected leader footer '/ Search' hint, got %v", tt.Texts())
	}

	tt.Key(0, ui.KeySlash)
	tt.Frame()

	st := f.ctrl.State()
	if st.Phase != engine.PhaseOmnibox {
		t.Fatalf("expected PhaseOmnibox after '/', got %s", st.Phase)
	}
	if st.OmniboxTab != config.TabActions {
		t.Fatalf("expected TabActions after '/', got %s", st.OmniboxTab)
	}
	if st.OmniboxQuery != "" {
		t.Fatalf("expected clean OmniboxQuery after '/', got %q", st.OmniboxQuery)
	}
}

// TestOmniboxQuickIndexAndSecondaryShortcuts covers ⌥1..⌥9 and ⌘↵ / Ctrl+↵.
func TestOmniboxQuickIndexAndSecondaryShortcuts(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "alpha", "beta", "gamma")

	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)

	items := f.ctrl.State().OmniboxItems
	if len(items) < 2 {
		t.Fatalf("expected clipboard items, got %+v", items)
	}
	secondRow := items[1].ClipboardContent

	// ⌥2 / Alt+2 runs the primary action of the second row directly.
	tt.Key(ui.Alt, ui.Key2)
	waitUntil(t, 2*time.Second, func() bool {
		return len(f.bridge.getInjected()) >= 1
	}, "Alt+2 quick-select injection")
	if got := f.bridge.getInjected()[0].text; got != secondRow {
		t.Fatalf("Alt+2 injected %q, want %q", got, secondRow)
	}

	// ⌘↵ / Ctrl+↵ runs the secondary action of the selected row (copy).
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt.Frame()
	selected := f.ctrl.State().OmniboxItems[f.ctrl.State().SelectedIndex].ClipboardContent
	tt.Key(ui.Cmd, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		return len(f.copiedTexts()) >= 1
	}, "secondary selection copy")
	if got := f.copiedTexts()[0]; got != selected {
		t.Fatalf("secondary action copied %q, want %q", got, selected)
	}
}

// TestOmniboxActionMenuOpensClosesAndExecutes covers the ⌘K / Ctrl+K palette.
func TestOmniboxActionMenuOpensClosesAndExecutes(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "menu-target")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)

	actionsLabel := modPrefix() + "K Actions"
	if err := tt.Click(actionsLabel); err != nil {
		t.Fatalf("click footer %q: %v", actionsLabel, err)
	}
	tt.Frame()
	if !f.ctrl.omniboxActionMenuOpen(f.ctrl.State()) {
		t.Fatalf("expected action menu open after clicking %q, got %v", actionsLabel, tt.Texts())
	}
	for _, want := range []string{"Paste to Active App", "Copy to Clipboard", "↑↓ Select"} {
		if !tt.HasText(want) {
			t.Fatalf("expected action menu entry %q, got %v", want, tt.Texts())
		}
	}

	// Escape closes only the menu, never the Capsule.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if f.ctrl.omniboxActionMenuOpen(f.ctrl.State()) {
		t.Fatal("expected action menu closed after Escape")
	}
	st := f.ctrl.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard {
		t.Fatalf("Escape closed the Capsule: phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}
	if tt.HasText("↑↓ Select") {
		t.Fatalf("expected action menu gone after Escape, got %v", tt.Texts())
	}

	// Reopen with ⌘K and click a menu entry to execute it.
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !f.ctrl.omniboxActionMenuOpen(f.ctrl.State()) {
		t.Fatalf("expected action menu open after ⌘K, got %v", tt.Texts())
	}
	if err := tt.Click("Copy to Clipboard"); err != nil {
		t.Fatalf("click 'Copy to Clipboard': %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return len(f.copiedTexts()) >= 1
	}, "action menu copy execution")
	if got := f.copiedTexts()[0]; got != "menu-target" {
		t.Fatalf("action menu copied %q, want %q", got, "menu-target")
	}

	// Palette keyboard navigation: ↓ moves selection to "pin", Enter executes it.
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		recent, _ := f.clips.Recent(context.Background(), 10)
		for _, r := range recent {
			if r.Content == "menu-target" && r.IsPinned {
				return true
			}
		}
		return false
	}, "action menu pin execution")

	// Pin works through '@' prefix routing in the Actions tab too, since the
	// chord keys off the selected item kind rather than the active tab.
	f.record(t, "at-target")
	f.ctrl.OpenOmnibox(config.TabActions, "@at-target")
	tt.Frame()
	st = f.ctrl.State()
	if st.OmniboxTab != config.TabActions || len(st.OmniboxItems) == 0 {
		t.Fatalf("expected '@' routed clipboard items, got tab=%s items=%+v", st.OmniboxTab, st.OmniboxItems)
	}
	if got := st.OmniboxItems[st.SelectedIndex].Kind; got != engine.ItemClipboardEntry {
		t.Fatalf("expected clipboard entry under '@' routing, got %s", got)
	}
	tt.Key(ui.Cmd, ui.KeyP)
	waitUntil(t, 2*time.Second, func() bool {
		recent, _ := f.clips.Recent(context.Background(), 10)
		for _, r := range recent {
			if r.Content == "at-target" && r.IsPinned {
				return true
			}
		}
		return false
	}, "'@' routed clipboard pin")
}

// TestOmniboxSplitPreviewPane verifies the multi-line clipboard preview, live
// snippet rendering, and the ⌘I / Ctrl+I toggle.
func TestOmniboxSplitPreviewPane(t *testing.T) {
	f := newInteractionFixture(t, nil)
	content := "line one\nline two\nline three"
	f.record(t, content)

	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)

	if !tt.HasText("line three") {
		t.Fatalf("expected full multi-line clipboard content in preview, got %v", tt.Texts())
	}
	stats := fmt.Sprintf("%d chars • %d lines", utf8.RuneCountInString(content), 3)
	if !tt.HasText(stats) {
		t.Fatalf("expected preview stats %q, got %v", stats, tt.Texts())
	}

	// The footer Preview button hides the pane; ⌘I toggles it back and forth.
	previewLabel := modPrefix() + "I Preview"
	if err := tt.Click(previewLabel); err != nil {
		t.Fatalf("click footer %q: %v", previewLabel, err)
	}
	tt.Frame()
	if tt.HasText("line three") {
		t.Fatalf("expected preview hidden after footer toggle, got %v", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyI)
	tt.Frame()
	if !tt.HasText("line three") {
		t.Fatalf("expected preview back after ⌘I, got %v", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyI)
	tt.Frame()
	if tt.HasText("line three") {
		t.Fatalf("expected preview hidden after second ⌘I, got %v", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyI)
	tt.Frame()
	if !tt.HasText("line three") {
		t.Fatalf("expected preview back after third ⌘I, got %v", tt.Texts())
	}

	// Snippet preview renders the raw template and the live rendered output.
	cfg := f.ctrl.Config()
	cfg.Snippets.Items = append(cfg.Snippets.Items, config.SnippetItem{
		ID:           "greeting",
		Trigger:      ";greet",
		Content:      "Greeting for {{app}}!",
		Description:  "Greeting snippet",
		WordBoundary: true,
		AppBundleIDs: []string{"com.apple.TextEdit"},
	})
	if err := f.ctrl.ConfigStore.SaveSnippets(cfg.Snippets); err != nil {
		t.Fatalf("SaveSnippets: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(f.ctrl.ConfigStore.Current())

	f.ctrl.OpenOmnibox(config.TabSnippets, "greet")
	tt.Frame()
	if !tt.HasText(";greet") {
		t.Fatalf("expected snippet trigger badge, got %v", tt.Texts())
	}
	if !tt.HasText("Greeting for TextEdit!") {
		t.Fatalf("expected live rendered snippet output, got %v", tt.Texts())
	}
	if !tt.HasText("Template") || !tt.HasText("Rendered") {
		t.Fatalf("expected template/rendered preview sections, got %v", tt.Texts())
	}
}

// TestOmniboxInstalledAppPreviewIcon verifies decoded PNG icons in the preview.
func TestOmniboxInstalledAppPreviewIcon(t *testing.T) {
	f := newInteractionFixture(t, nil)
	appPath := "/Applications/Zebrario.app"
	f.apps.Apps = []engine.InstalledAppEntry{
		{Name: "Zebrario", BundleID: "com.example.zebrario", Path: appPath},
	}
	f.apps.SetIcon(appPath, testPNG16(t))
	f.ctrl.AppProvider = f.apps
	f.ctrl.SyncInstalledApps(context.Background())

	f.ctrl.OpenOmnibox(config.TabActions, "Zebrario")
	tt := f.omniboxTester(t)

	if !tt.HasText("Zebrario") {
		t.Fatalf("expected installed app row, got %v", tt.Texts())
	}
	bmp := f.ctrl.AppIconBitmap(appPath)
	if bmp == nil {
		t.Fatal("expected decoded Retina PNG icon bitmap for installed app")
	}
	if bw, bh := bmp.Size(); bw != 16 || bh != 16 {
		t.Fatalf("decoded icon = %dx%d, want 16x16", bw, bh)
	}
	if !tt.HasText("com.example.zebrario") {
		t.Fatalf("expected app bundle id in preview, got %v", tt.Texts())
	}
}

// TestAIStreamingFollowUpIMECompositionSafety verifies IME candidate keys never
// send a follow-up or close the AI view mid-composition.
func TestAIStreamingFollowUpIMECompositionSafety(t *testing.T) {
	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			return sseResponse("Hello ", "from ", "AI!"), nil
		},
	}
	f := newInteractionFixture(t, doer)
	f.ctrl.OpenOmnibox(config.TabAI, "explain go interfaces")

	items := f.ctrl.State().OmniboxItems
	for i := range len(items) - 1 - f.ctrl.State().SelectedIndex {
		f.ctrl.Dispatch(engine.Event{Type: engine.EventSelectNext})
		_ = i
	}
	f.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIOutput == "Hello from AI!"
	}, "AI streaming completion")

	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()

	// Focus the follow-up input and start composing Chinese Pinyin.
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if _, noIME := tt.TextInputActive(); noIME {
		t.Fatal("expected the follow-up input to accept input methods")
	}
	tt.Compose("nihao", 5)
	if !f.ctrl.followUpComposing() {
		t.Fatalf("expected follow-up composition flag set, got %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	st := f.ctrl.State()
	if st.Phase != engine.PhaseAIStreaming {
		t.Fatalf("Enter/Escape during composition closed the AI view: phase=%s", st.Phase)
	}
	if len(st.AIHistory) != 0 || st.AIPrompt != "explain go interfaces" {
		t.Fatalf("follow-up fired during composition: history=%+v prompt=%q", st.AIHistory, st.AIPrompt)
	}

	// Commit text and press Enter: now the follow-up DOES fire.
	tt.Compose("", 0)
	tt.Type("hello")
	tt.Frame()
	if f.ctrl.followUpComposing() {
		t.Fatal("expected composition flag cleared after commit")
	}
	if f.ctrl.uiState.followUpQuery != "hello" {
		t.Fatalf("followUpQuery = %q, want %q", f.ctrl.uiState.followUpQuery, "hello")
	}
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && len(st.AIHistory) == 2
	}, "follow-up AI turn")
	st = f.ctrl.State()
	if st.AIPrompt != "hello" {
		t.Fatalf("follow-up prompt = %q, want %q", st.AIPrompt, "hello")
	}
	if st.AIHistory[0].Role != "user" || st.AIHistory[1].Role != "assistant" {
		t.Fatalf("unexpected follow-up history: %+v", st.AIHistory)
	}
}

// TestAIStreamingProfileCycleShortcut verifies ⌘⇧M / Ctrl+Shift+M cycles profiles.
func TestAIStreamingProfileCycleShortcut(t *testing.T) {
	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			return sseResponse("Done"), nil
		},
	}
	f := newInteractionFixture(t, doer)
	f.ctrl.OpenOmnibox(config.TabAI, "summarize")
	f.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI completion for profile cycle")

	before := f.ctrl.ActiveAIProfile().ID
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()

	tt.Key(primaryMod()|ui.Shift, ui.KeyM)
	tt.Frame()
	after := f.ctrl.ActiveAIProfile().ID
	if after == before {
		t.Fatalf("expected profile to cycle with %s⇧M, still %q", modPrefix(), before)
	}
	if after != "ollama" {
		t.Fatalf("expected cycling deepseek -> ollama, got %q", after)
	}
}
