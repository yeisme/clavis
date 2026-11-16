package uiapp

import (
	"bytes"
	"context"
	"encoding/json"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/apps"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

// ---- Test Mocks ----

type mockBridge struct {
	mu           sync.Mutex
	caps         host.Capabilities
	snapshot     host.ContextSnapshot
	perms        host.PermissionStatus
	inputSources []host.InputSourceInfo
	currentIME   host.InputSourceInfo

	windowCmds     []host.WindowCommand
	injected       []injectedCall
	openedApps     []string
	selectedIMEs   []string
	prompted       []string
	openedPerms    []string
	snapshotCalls  int
	tapRecoveries  int64
	updatedKeymaps []config.KeymapConfig
	updatedIMERule []config.IMERulesConfig

	// Temporary ASCII lock state for Leader/Capsule sessions.
	lockTemporaryCalls    int
	restoreTemporaryCalls int
	preLockIME            host.InputSourceInfo
	activeKeymapLayer     string
	// Recorded SetIMEMode calls (per-app IME rule application).
	imeModeCalls []config.IMEMode
}

type injectedCall struct {
	text       string
	delCount   int
	backOffset int
}

func newMockBridge() *mockBridge {
	return &mockBridge{
		caps: host.Capabilities{
			Accessibility:    true,
			InputMonitoring:  true,
			GlobalHotkeys:    true,
			WindowManagement: true,
			IMEControl:       true,
			ClipboardHistory: true,
			Keychain:         true,
		},
		snapshot: host.ContextSnapshot{
			BundleID:         "com.apple.TextEdit",
			ProcessName:      "TextEdit",
			AppName:          "TextEdit",
			SelectedText:     "hello clavis world",
			ClipboardPreview: "clipboard sample",
		},
		perms: host.PermissionStatus{
			Accessibility:   true,
			InputMonitoring: true,
			AppleEvents:     true,
			Granted:         true,
			Details:         map[string]string{"secureInput": "false"},
		},
		inputSources: []host.InputSourceInfo{
			{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true},
			{ID: "com.apple.inputmethod.SCIM.ITABC", Name: "Pinyin", IsASCII: false},
		},
		currentIME: host.InputSourceInfo{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true},
	}
}

func (m *mockBridge) Capabilities() host.Capabilities { return m.caps }

func (m *mockBridge) SnapshotContext(context.Context) (host.ContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotCalls++
	return m.snapshot, nil
}

func (m *mockBridge) ExecuteWindowCommand(_ context.Context, cmd host.WindowCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.windowCmds = append(m.windowCmds, cmd)
	return nil
}

func (m *mockBridge) InjectText(_ context.Context, text string, deleteCount int, cursorBackOffset int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.injected = append(m.injected, injectedCall{text: text, delCount: deleteCount, backOffset: cursorBackOffset})
	return nil
}

func (m *mockBridge) OpenApp(_ context.Context, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openedApps = append(m.openedApps, target)
	return nil
}

func (m *mockBridge) InputSources(context.Context) ([]host.InputSourceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]host.InputSourceInfo, len(m.inputSources))
	copy(out, m.inputSources)
	return out, nil
}

func (m *mockBridge) CurrentInputSource(context.Context) (host.InputSourceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentIME, nil
}

func (m *mockBridge) SelectInputSource(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selectedIMEs = append(m.selectedIMEs, id)
	for _, s := range m.inputSources {
		if s.ID == id {
			m.currentIME = s
			break
		}
	}
	return nil
}

func (m *mockBridge) Permissions(context.Context) (host.PermissionStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.perms, nil
}

func (m *mockBridge) Start(context.Context, func(host.TriggerEvent)) error { return nil }
func (m *mockBridge) Stop() error                                          { return nil }

func (m *mockBridge) RefreshPermissions(ctx context.Context) (host.PermissionStatus, error) {
	return m.Permissions(ctx)
}

func (m *mockBridge) OpenPermissionSettings(kind string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openedPerms = append(m.openedPerms, kind)
	return nil
}

func (m *mockBridge) PromptPermission(kind string) (host.PermissionStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompted = append(m.prompted, kind)
	return m.perms, nil
}

func (m *mockBridge) PollClipboardCandidate(context.Context) (*clipboard.CaptureCandidate, int64, error) {
	return nil, 0, nil
}

func (m *mockBridge) IgnoreSelfPasteboardChange(int64) {}

func (m *mockBridge) UpdateKeymapConfig(km config.KeymapConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedKeymaps = append(m.updatedKeymaps, km)
}

func (m *mockBridge) UpdateIMEConfig(_ config.IMEConfig, rules config.IMERulesConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedIMERule = append(m.updatedIMERule, rules)
}

func (m *mockBridge) SetIMEMode(_ context.Context, mode config.IMEMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.imeModeCalls = append(m.imeModeCalls, mode)
	switch mode {
	case config.IMEModeASCII:
		for _, s := range m.inputSources {
			if s.IsASCII {
				m.currentIME = s
				m.selectedIMEs = append(m.selectedIMEs, s.ID)
				break
			}
		}
	case config.IMEModeNative:
		for _, s := range m.inputSources {
			if !s.IsASCII {
				m.currentIME = s
				m.selectedIMEs = append(m.selectedIMEs, s.ID)
				break
			}
		}
	}
	return nil
}

func (m *mockBridge) TapRecoveryCount() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tapRecoveries
}

// LockTemporaryASCII mirrors the host behaviour: switch to the first ASCII source while remembering
// the pre-lock source so RestoreTemporaryIME can put it back.
func (m *mockBridge) LockTemporaryASCII(context.Context) (bool, host.InputSourceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lockTemporaryCalls++
	if m.currentIME.IsASCII {
		return false, m.currentIME, nil
	}
	var target host.InputSourceInfo
	for _, s := range m.inputSources {
		if s.IsASCII {
			target = s
			break
		}
	}
	if target.ID == "" {
		return false, m.currentIME, nil
	}
	m.preLockIME = m.currentIME
	m.currentIME = target
	m.selectedIMEs = append(m.selectedIMEs, target.ID)
	return true, target, nil
}

// RestoreTemporaryIME restores the pre-lock input source recorded by LockTemporaryASCII.
func (m *mockBridge) RestoreTemporaryIME(context.Context) (bool, host.InputSourceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoreTemporaryCalls++
	if m.preLockIME.ID == "" || m.preLockIME.ID == m.currentIME.ID {
		return false, m.currentIME, nil
	}
	restored := m.preLockIME
	m.preLockIME = host.InputSourceInfo{}
	m.currentIME = restored
	m.selectedIMEs = append(m.selectedIMEs, restored.ID)
	return true, restored, nil
}

// ActiveKeymapLayer reports the mock's current keymap hold layer ("" by default).
func (m *mockBridge) ActiveKeymapLayer() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeKeymapLayer
}

func (m *mockBridge) setActiveKeymapLayer(layer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeKeymapLayer = layer
}

func (m *mockBridge) getIMEPillCalls() (lock, restore int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lockTemporaryCalls, m.restoreTemporaryCalls
}

func (m *mockBridge) getSnapshotCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotCalls
}

func (m *mockBridge) getIMEModeCalls() []config.IMEMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]config.IMEMode, len(m.imeModeCalls))
	copy(out, m.imeModeCalls)
	return out
}

func (m *mockBridge) getCurrentIME() host.InputSourceInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentIME
}

func (m *mockBridge) getInjected() []injectedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]injectedCall, len(m.injected))
	copy(out, m.injected)
	return out
}

type mockHTTPDoer struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (d mockHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return d.fn(req)
}

func sseResponse(chunks ...string) *http.Response {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString(`data: {"choices":[{"delta":{"content":"`)
		sb.WriteString(c)
		sb.WriteString(`"}}]}` + "\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sb.String())),
		Header:     make(http.Header),
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func newTestController(t *testing.T, doer ai.HTTPDoer) (*AppController, *mockBridge, *clipboard.MemoryStore, *secrets.MemoryAPIKeyStore) {
	t.Helper()
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("CLAVIS_API_KEY", "")
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}

	clipStore := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	secStore := secrets.NewMemoryAPIKeyStore()
	_ = secStore.SaveKey(cfgStore.Current().Main.AI.KeychainAccount, "sk-test-key")
	br := newMockBridge()

	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		ClipboardStore: clipStore,
		SecretStore:    secStore,
		Bridge:         br,
		HTTPDoer:       doer,
	})
	t.Cleanup(ctrl.Close)
	return ctrl, br, clipStore, secStore
}

// ---- Tests ----

func TestPreferredCapsuleSize(t *testing.T) {
	app := config.DefaultAppearanceConfig()
	w, h := PreferredCapsuleSize(engine.PhaseHidden, app)
	if w != 0 || h != 0 {
		t.Fatalf("PhaseHidden = (%d,%d), want (0,0)", w, h)
	}
	w, h = PreferredCapsuleSize(engine.PhaseLeader, app)
	if w != 680 || h <= 0 {
		t.Fatalf("PhaseLeader = (%d,%d), want width 680", w, h)
	}
	w, h = PreferredCapsuleSize(engine.PhaseOmnibox, app)
	if w != 680 || h != 536 {
		t.Fatalf("PhaseOmnibox = (%d,%d), want (680,536)", w, h)
	}
	w, h = PreferredCapsuleSize(engine.PhaseAIStreaming, app)
	if w != 680 || h != 520 {
		t.Fatalf("PhaseAIStreaming = (%d,%d), want (680,520)", w, h)
	}
	w, h = PreferredCapsuleSizeForState(engine.State{
		Phase:     engine.PhaseAIStreaming,
		AIHistory: []engine.ChatTurn{{Role: "user", Content: "Hi"}},
	}, app)
	if w != 680 || h != 580 {
		t.Fatalf("PreferredCapsuleSizeForState with history = (%d,%d), want (680,580)", w, h)
	}
	w, h = PreferredCapsuleSize(engine.PhaseHUD, app)
	if w > 360 || h != 56 {
		t.Fatalf("PhaseHUD = (%d,%d)", w, h)
	}
}

func TestLeaderUI_NavigationBackspaceAndAutoDowngrade(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenLeader,
		Context: &engine.CapsuleContext{
			AppName:      "Notes",
			SelectedText: "hello world",
		},
	})

	w, h := PreferredCapsuleSize(engine.PhaseLeader, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	// Root nodes visible
	if !tt.HasText("Window") || !tt.HasText("AI Rewrite") || !tt.HasText("Clipboard") {
		t.Fatalf("expected root leader nodes, got %v", tt.Texts())
	}

	// Click "Window" group card -> drills into submenu
	if err := tt.Click("Window"); err != nil {
		t.Fatalf("click Window: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Leader › Window") || !tt.HasText("Left Half") {
		t.Fatalf("expected Window submenu, got %v", tt.Texts())
	}

	// Backspace pops back to root
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if !tt.HasText("AI Rewrite") || tt.HasText("Left Half") {
		t.Fatalf("expected root after Backspace, got %v", tt.Texts())
	}

	// Unmatched key 'z' auto-downgrades to Actions Omnibox preserving 'z'
	tt.Key(0, ui.KeyZ)
	tt.Frame()
	st := ctrl.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabActions || st.OmniboxQuery != "z" {
		t.Fatalf("expected auto-downgrade to Actions Omnibox with query 'z', got phase=%s tab=%s query=%q",
			st.Phase, st.OmniboxTab, st.OmniboxQuery)
	}
}

func TestOmnibox_IMECompositionProtection(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox,
		Tab:  config.TabActions,
		Context: &engine.CapsuleContext{
			AppName:      "Notes",
			SelectedText: "sample text",
		},
	})

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	// Start IME composition
	tt.Compose("nihao", 5)
	if !ctrl.uiState.isComposing {
		t.Fatal("expected isComposing=true after tt.Compose")
	}

	// Keys Enter, Up, Down, Tab, Esc MUST NOT hijack during composition
	tt.Key(0, ui.KeyEnter)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	st := ctrl.State()
	if st.Phase != engine.PhaseOmnibox {
		t.Fatalf("expected phase to remain PhaseOmnibox during IME compose, got %s", st.Phase)
	}
	if st.OmniboxTab != config.TabActions {
		t.Fatalf("expected tab to remain TabActions during IME compose, got %s", st.OmniboxTab)
	}

	// Commit composition text and clear compose state
	tt.Compose("", 0)
	tt.Type("Maximize")
	tt.Frame()

	if ctrl.uiState.isComposing {
		t.Fatal("expected isComposing=false after committing composition")
	}
	st = ctrl.State()
	if st.OmniboxQuery != "Maximize" {
		t.Fatalf("expected OmniboxQuery='Maximize', got %q", st.OmniboxQuery)
	}
	if len(st.OmniboxItems) == 0 || len(st.OmniboxItems[0].MatchedIndices) == 0 {
		t.Fatalf("expected matched indices for 'Maximize', got %+v", st.OmniboxItems)
	}

	// Now Enter DOES confirm selection (executes Maximize and closes)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected PhaseHidden after Enter post-composition, got %s", ctrl.State().Phase)
	}
}

func TestOmnibox_TabsNavigationAndAIStreaming(t *testing.T) {
	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			return sseResponse("Hello ", "from ", "AI!"), nil
		},
	}
	ctrl, br, _, _ := newTestController(t, doer)
	ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox,
		Tab:  config.TabActions,
		Context: &engine.CapsuleContext{
			AppName: "Notes",
		},
	})

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	// Cmd+1..Cmd+5 tab switching
	tt.Key(ui.Cmd, ui.Key2)
	tt.Frame()
	if ctrl.State().OmniboxTab != config.TabClipboard {
		t.Fatalf("Cmd+2 tab = %s, want clipboard", ctrl.State().OmniboxTab)
	}
	tt.Key(ui.Cmd, ui.Key3)
	tt.Frame()
	if ctrl.State().OmniboxTab != config.TabSnippets {
		t.Fatalf("Cmd+3 tab = %s, want snippets", ctrl.State().OmniboxTab)
	}
	tt.Key(ui.Cmd, ui.Key4)
	tt.Frame()
	if ctrl.State().OmniboxTab != config.TabAI {
		t.Fatalf("Cmd+4 tab = %s, want ai", ctrl.State().OmniboxTab)
	}
	tt.Key(ui.Cmd, ui.Key5)
	tt.Frame()
	if ctrl.State().OmniboxTab != config.TabGlossary {
		t.Fatalf("Cmd+5 tab = %s, want glossary", ctrl.State().OmniboxTab)
	}
	tt.Key(ui.Cmd, ui.Key1)
	tt.Frame()
	if ctrl.State().OmniboxTab != config.TabActions {
		t.Fatalf("Cmd+1 tab = %s, want actions", ctrl.State().OmniboxTab)
	}

	// Down / Up navigation
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if ctrl.State().SelectedIndex != 1 {
		t.Fatalf("SelectedIndex after Down = %d, want 1", ctrl.State().SelectedIndex)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if ctrl.State().SelectedIndex != 0 {
		t.Fatalf("SelectedIndex after Up = %d, want 0", ctrl.State().SelectedIndex)
	}

	// Freeform AI prompt submission
	tt.Type("explain go interfaces")
	tt.Frame()
	items := ctrl.State().OmniboxItems
	if len(items) == 0 || items[len(items)-1].Kind != engine.ItemAIMode {
		t.Fatalf("expected adhoc AI item for freeform query, got %+v", items)
	}
	// Move to the adhoc AI item if needed and press Enter
	for range len(items) - 1 - ctrl.State().SelectedIndex {
		tt.Key(0, ui.KeyDown)
	}
	tt.Key(0, ui.KeyEnter)

	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIOutput == "Hello from AI!"
	}, "AI streaming completion")

	tt.Frame()
	if !tt.HasText("Done") {
		t.Fatalf("expected Done status badge in AI view, got %v", tt.Texts())
	}

	// Click "⏎ Replace" -> injects text and hides
	if err := tt.Click("⏎ Replace"); err != nil {
		t.Fatalf("click Replace: %v", err)
	}
	tt.Frame()
	if ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected PhaseHidden after Replace, got %s", ctrl.State().Phase)
	}
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) == 1
	}, "InjectText after Replace")
	if got := br.getInjected()[0].text; got != "Hello from AI!" {
		t.Fatalf("injected text = %q, want %q", got, "Hello from AI!")
	}
}

func TestAIStreaming_CancelOnEscape(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			close(started)
			<-req.Context().Done()
			close(canceled)
			return nil, req.Context().Err()
		},
	}
	ctrl, _, _, _ := newTestController(t, doer)
	ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox,
		Tab:  config.TabAI,
	})
	ctrl.Dispatch(engine.Event{
		Type: engine.EventConfirmSelection,
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("AI stream never started")
	}

	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	if ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected PhaseHidden after Escape, got %s", ctrl.State().Phase)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("AI HTTP request context was not canceled on Escape")
	}
}

func TestClipboardTab_PinAndDeleteWithFIFOPersistence(t *testing.T) {
	ctrl, _, clipStore, _ := newTestController(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	_, _, _ = clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "first-entry", SourceAppName: "Notes", Timestamp: now})
	_, _, _ = clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "second-entry", SourceAppName: "Terminal", Timestamp: now.Add(time.Second)})

	ctrl.syncClipboardEntries(ctx)
	ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox,
		Tab:  config.TabClipboard,
	})

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	if !tt.HasText("second-entry") || !tt.HasText("first-entry") {
		t.Fatalf("expected clipboard items in view, got %v", tt.Texts())
	}

	// Move down to "first-entry" (index 1) and pin it with Cmd+P
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if got := ctrl.State().OmniboxItems[ctrl.State().SelectedIndex].ClipboardContent; got != "first-entry" {
		t.Fatalf("expected selected 'first-entry', got %q", got)
	}

	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()

	// Wait for FIFO persistence and check selection preserved on "first-entry"
	waitUntil(t, time.Second, func() bool {
		recent, _ := clipStore.Recent(ctx, 10)
		for _, r := range recent {
			if r.Content == "first-entry" && r.IsPinned {
				return true
			}
		}
		return false
	}, "first-entry pinned in store")

	st := ctrl.State()
	if st.OmniboxItems[st.SelectedIndex].ClipboardContent != "first-entry" {
		t.Fatalf("selection should follow 'first-entry' across pin sort, got %q",
			st.OmniboxItems[st.SelectedIndex].ClipboardContent)
	}

	// Delete "first-entry" with Cmd+Backspace
	tt.Key(ui.Cmd, ui.KeyBackspace)
	tt.Frame()

	waitUntil(t, time.Second, func() bool {
		recent, _ := clipStore.Recent(ctx, 10)
		return len(recent) == 1 && recent[0].Content == "second-entry"
	}, "first-entry deleted from store")
}

func TestSettingsView_AllFourTabsKeychainAndValidationErrors(t *testing.T) {
	var reqMu sync.Mutex
	var requestedURLs []string
	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			reqMu.Lock()
			requestedURLs = append(requestedURLs, req.URL.String())
			reqMu.Unlock()
			return sseResponse("OK"), nil
		},
	}
	var openedPaths []string
	ctrl, br, _, secStore := newTestController(t, doer)
	ctrl.OpenPath = func(path string) error {
		openedPaths = append(openedPaths, path)
		return nil
	}
	// Clear pre-seeded key so we test saving & clearing from scratch
	_ = secStore.DeleteKey(ctrl.Config().Main.AI.KeychainAccount)
	ctrl.APIKeyStatus()

	if len(SettingsTabNames()) != 5 {
		t.Fatalf("SettingsTabNames() len = %d, want 5", len(SettingsTabNames()))
	}

	tt := ui.NewTester(ctrl.SettingsView, 760, 560)
	tt.Frame()

	// Tab 1: General & Triggers
	if !tt.HasText("right_option") || !tt.HasText("option+space") {
		t.Fatalf("expected trigger fields on General tab, got %v", tt.Texts())
	}

	// Switch to Tab 2: AI & Keychain
	if err := tt.Click("AI & Keychain"); err != nil {
		t.Fatalf("click AI & Keychain: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Not configured") || !tt.HasText("deepseek-chat") {
		t.Fatalf("expected 'Not configured' key status and deepseek-chat model, got %v", tt.Texts())
	}

	// Switch AI profile to keyless local "ollama" -> should show "Local / Keyless (optional)"
	if err := tt.Click("ollama"); err != nil {
		t.Fatalf("click ollama profile pill: %v", err)
	}
	tt.Frame()
	if ctrl.ActiveAIProfile().ID != "ollama" {
		t.Fatalf("ActiveAIProfile().ID = %q, want ollama", ctrl.ActiveAIProfile().ID)
	}
	if !tt.HasText("Local / Keyless (optional)") || !tt.HasText("qwen2.5:7b") {
		t.Fatalf("expected keyless Ollama status and qwen2.5:7b model, got %v", tt.Texts())
	}

	// Test Connection on keyless Ollama
	if err := tt.Click("Test Connection"); err != nil {
		t.Fatalf("click Test Connection on ollama: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		ctrl.mu.Lock()
		st := ctrl.aiTestStatus
		ctrl.mu.Unlock()
		return st == AITestSuccess
	}, "TestAIConnection ollama success")
	tt.Frame()

	// Switch back to "deepseek" profile
	if err := tt.Click("deepseek"); err != nil {
		t.Fatalf("click deepseek profile pill: %v", err)
	}
	tt.Frame()
	if ctrl.ActiveAIProfile().ID != "deepseek" || !tt.HasText("Not configured") {
		t.Fatalf("expected deepseek profile with 'Not configured', got %v", tt.Texts())
	}

	// Enter API key in password input and save
	if err := tt.Click("API Key"); err != nil {
		t.Fatalf("click API Key field: %v", err)
	}
	secretVal := "sk-super-secret-value-999"
	tt.Type(secretVal)
	tt.Frame()
	if err := tt.Click("Save Key"); err != nil {
		t.Fatalf("click Save Key: %v", err)
	}
	tt.Frame()

	// Verify raw key is NEVER exposed in UI text
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, secretVal) {
			t.Fatalf("raw API key leaked into UI text: %q", txt)
		}
	}
	if !tt.HasText("Configured in Keychain") {
		t.Fatalf("expected Keychain configured badge, got %v", tt.Texts())
	}

	// Test Connection with mock HTTP transport
	if err := tt.Click("Test Connection"); err != nil {
		t.Fatalf("click Test Connection: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		ctrl.mu.Lock()
		st := ctrl.aiTestStatus
		ctrl.mu.Unlock()
		return st == AITestSuccess
	}, "TestAIConnection success")
	tt.Frame()
	if !tt.HasText("Connected: OK") {
		t.Fatalf("expected 'Connected: OK' on AI tab, got %v", tt.Texts())
	}

	// Clear Key
	if err := tt.Click("Clear Key"); err != nil {
		t.Fatalf("click Clear Key: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Not configured") {
		t.Fatalf("expected 'Not configured' after Clear Key, got %v", tt.Texts())
	}

	// Switch to Tab 3: IME & Keymap
	if err := tt.Click("IME & Keymap"); err != nil {
		t.Fatalf("click IME & Keymap: %v", err)
	}
	tt.Frame()
	if !tt.HasText("tap_hold_threshold_ms: 200 ms") || !tt.HasText("com.apple.Terminal") {
		t.Fatalf("expected keymap fields and Terminal IME rule on IME & Keymap tab, got %v", tt.Texts())
	}

	// Toggle "Nav Layer" switch and verify persistence + bridge update
	if err := tt.Click("Nav Layer"); err != nil {
		t.Fatalf("click Nav Layer switch: %v", err)
	}
	tt.Frame()
	if ctrl.Config().Main.Keymap.NavLayerEnabled {
		t.Fatal("expected NavLayerEnabled=false after toggling switch")
	}

	// Add a new IME rule via inline form
	if err := tt.Click("Rule Target"); err != nil {
		t.Fatalf("click Rule Target input: %v", err)
	}
	tt.Type("com.apple.Safari")
	tt.Frame()
	if err := tt.Click("URL Pattern"); err != nil {
		t.Fatalf("click URL Pattern input: %v", err)
	}
	tt.Type("github.com")
	tt.Frame()
	if err := tt.Click("Add Rule"); err != nil {
		t.Fatalf("click Add Rule: %v", err)
	}
	tt.Frame()

	if len(ctrl.Config().IMERules.Rules) != 3 || !tt.HasText("com.apple.Safari") || !tt.HasText("url=github.com") {
		t.Fatalf("expected newly added com.apple.Safari IME rule, got rules=%+v texts=%v",
			ctrl.Config().IMERules.Rules, tt.Texts())
	}
	br.mu.Lock()
	imeUpdates := len(br.updatedIMERule)
	br.mu.Unlock()
	if imeUpdates == 0 {
		t.Fatal("expected bridge.UpdateIMEConfig to be called on Add Rule")
	}

	// Delete the newly added IME rule
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete on IME rule: %v", err)
	}
	tt.Frame()
	if len(ctrl.Config().IMERules.Rules) != 2 || tt.HasText("com.apple.Safari") {
		t.Fatalf("expected com.apple.Safari rule deleted, got rules=%+v", ctrl.Config().IMERules.Rules)
	}

	// Introduce invalid config.toml and reload -> validation error should display on Permissions & Diagnostics tab
	badCfgPath := filepath.Join(ctrl.ConfigStore.Directory(), string(config.FileMain))
	if err := os.WriteFile(badCfgPath, []byte("invalid toml [[["), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	if ctrl.ReloadConfig() {
		t.Fatal("expected ReloadConfig to fail on invalid TOML")
	}

	// Switch to Tab 4: Permissions & Diagnostics
	if err := tt.Click("Permissions & Diagnostics"); err != nil {
		t.Fatalf("click Permissions & Diagnostics: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Accessibility") || !tt.HasText("Input Monitoring") || !tt.HasText("ERROR:") {
		t.Fatalf("expected permissions and config validation error, got %v", tt.Texts())
	}

	// Switch to Tab 5: Configs
	if err := tt.Click("Configs"); err != nil {
		t.Fatalf("click Configs: %v", err)
	}
	tt.Frame()
	for _, f := range config.AllConfigFileKinds() {
		if !tt.HasText(string(f)) {
			t.Fatalf("expected %s on Configs tab, got %v", f, tt.Texts())
		}
	}
	if err := tt.Click("Open Config Folder"); err != nil {
		t.Fatalf("click Open Config Folder: %v", err)
	}
	if len(openedPaths) != 1 || openedPaths[0] != ctrl.ConfigStore.Directory() {
		t.Fatalf("openedPaths = %v, want [%s]", openedPaths, ctrl.ConfigStore.Directory())
	}
}

func TestAIStreaming_MultiTurnFollowUpAndProfileSwitch(t *testing.T) {
	var reqMu sync.Mutex
	var capturedBodies []map[string]any
	var capturedURLs []string
	turnCount := 0

	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)

			reqMu.Lock()
			capturedBodies = append(capturedBodies, payload)
			capturedURLs = append(capturedURLs, req.URL.String())
			turnCount++
			curTurn := turnCount
			reqMu.Unlock()

			if curTurn == 1 {
				return sseResponse("First answer"), nil
			}
			return sseResponse("Refined second answer"), nil
		},
	}

	ctrl, _, _, _ := newTestController(t, doer)
	ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox,
		Tab:  config.TabAI,
		Context: &engine.CapsuleContext{
			AppName:      "Notes",
			SelectedText: "draft paragraph",
		},
	})
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})

	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIOutput == "First answer"
	}, "first AI turn completion")

	w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	// Verify active profile pill is shown and clicking it cycles to ollama
	if !tt.HasText("deepseek / deepseek-chat") {
		t.Fatalf("expected profile pill 'deepseek / deepseek-chat', got %v", tt.Texts())
	}
	if err := tt.Click("deepseek / deepseek-chat"); err != nil {
		t.Fatalf("click profile pill: %v", err)
	}
	tt.Frame()
	if !tt.HasText("ollama / qwen2.5:7b") || ctrl.ActiveAIProfile().ID != "ollama" {
		t.Fatalf("expected switched profile 'ollama / qwen2.5:7b', got %v", tt.Texts())
	}

	// Press Tab to focus the follow-up input, type a follow-up prompt, and press Enter
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Type("Make it more concise")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)

	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIOutput == "Refined second answer"
	}, "second AI turn completion")

	tt.Frame()
	// History pill from first turn should be rendered above current response
	if !tt.HasText("First answer") || !tt.HasText("Refined second answer") {
		t.Fatalf("expected both history and refined answer in view, got %v", tt.Texts())
	}

	reqMu.Lock()
	defer reqMu.Unlock()
	if len(capturedURLs) != 2 {
		t.Fatalf("capturedURLs len = %d, want 2", len(capturedURLs))
	}
	if !strings.Contains(capturedURLs[0], "api.deepseek.com") {
		t.Fatalf("turn 1 URL = %q, want deepseek endpoint", capturedURLs[0])
	}
	if !strings.Contains(capturedURLs[1], "127.0.0.1:11434") {
		t.Fatalf("turn 2 URL = %q, want ollama endpoint", capturedURLs[1])
	}
	msgs, _ := capturedBodies[1]["messages"].([]any)
	if len(msgs) < 4 {
		t.Fatalf("expected multi-turn messages (system + user1 + assistant1 + user2), got %v", capturedBodies[1]["messages"])
	}
}

func TestOmnibox_CalculatorEmojiAndInstalledAppsWithIcons(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)

	var copied []string
	ctrl.ClipboardWriter = func(text string) {
		copied = append(copied, text)
	}

	mockApps := apps.NewMockAppProvider(
		engine.InstalledAppEntry{
			Name:     "Visual Studio Code",
			BundleID: "com.microsoft.VSCode",
			Path:     "/Applications/Visual Studio Code.app",
		},
	)
	mockApps.SetIcon("/Applications/Visual Studio Code.app", GenerateTrayIconPNG(32))
	ctrl.AppProvider = mockApps
	ctrl.SyncInstalledApps(context.Background())

	ctrl.OpenOmnibox(config.TabActions, "")
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	// 1. Calculator utility query: "(12 + 8) * 3" -> "Result = 60"
	tt.Type("(12 + 8) * 3")
	tt.Frame()
	if !tt.HasText("Result = 60") {
		t.Fatalf("expected calculator result 'Result = 60', got %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if len(copied) != 1 || copied[0] != "60" {
		t.Fatalf("expected calculator copy '60', got %v", copied)
	}

	// 2. Emoji query: ":rocket" -> "🚀  Rocket" -> injects "🚀"
	ctrl.OpenOmnibox(config.TabActions, ":rocket")
	tt.Frame()
	if !tt.HasText("Rocket") || !tt.HasText("🚀") {
		t.Fatalf("expected Rocket emoji in Omnibox, got %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) >= 1
	}, "emoji injected")
	if got := br.getInjected()[0].text; got != "🚀" {
		t.Fatalf("injected emoji = %q, want 🚀", got)
	}

	// 3. Installed App query: "Visual Studio Code" -> renders app icon bitmap & launches app on Enter
	ctrl.OpenOmnibox(config.TabActions, "Visual Studio Code")
	tt.Frame()
	if !tt.HasText("Visual Studio Code") {
		t.Fatalf("expected Visual Studio Code in Omnibox, got %v", tt.Texts())
	}
	if bmp := ctrl.AppIconBitmap("/Applications/Visual Studio Code.app"); bmp == nil {
		t.Fatal("expected non-nil decoded AppIconBitmap for Visual Studio Code")
	}
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, time.Second, func() bool {
		br.mu.Lock()
		defer br.mu.Unlock()
		return len(br.openedApps) == 1 && br.openedApps[0] == "com.microsoft.VSCode"
	}, "OpenApp com.microsoft.VSCode")
}

func TestLeader_LockASCIIOnLeaderAndRestoreOnDismissOrSkipOnToggle(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)

	var pillLabels []string
	ctrl.OnShowIMEPill = func(label string, _ *host.CursorAnchor) {
		pillLabels = append(pillLabels, label)
	}

	// Start with non-ASCII Pinyin active
	br.mu.Lock()
	br.currentIME = host.InputSourceInfo{ID: "com.apple.inputmethod.SCIM.ITABC", Name: "Pinyin", IsASCII: false}
	br.selectedIMEs = nil
	br.mu.Unlock()

	// 1. Opening Leader temporarily locks IME to default ASCII ("com.apple.keylayout.ABC")
	ctrl.OpenLeader()
	br.mu.Lock()
	curAfterOpen := br.currentIME
	br.mu.Unlock()
	if !curAfterOpen.IsASCII || curAfterOpen.ID != "com.apple.keylayout.ABC" {
		t.Fatalf("expected ASCII lock on OpenLeader, got %+v", curAfterOpen)
	}
	if len(pillLabels) == 0 || pillLabels[0] != "EN" {
		t.Fatalf("expected OnShowIMEPill('EN') on Leader ASCII lock, got %v", pillLabels)
	}

	// Dismissing Leader restores previous non-ASCII Pinyin IME
	ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
	br.mu.Lock()
	curAfterDismiss := br.currentIME
	br.mu.Unlock()
	if curAfterDismiss.ID != "com.apple.inputmethod.SCIM.ITABC" {
		t.Fatalf("expected Pinyin restored on Leader dismiss, got %+v", curAfterDismiss)
	}

	// 2. If user triggers Toggle IME ('i') inside Leader, EffectClose must NOT overwrite the intentional toggle
	ctrl.OpenLeader()
	ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "i"})
	waitUntil(t, time.Second, func() bool {
		return ctrl.State().Phase == engine.PhaseHUD
	}, "Toggle IME HUD")
	br.mu.Lock()
	curAfterToggle := br.currentIME
	br.mu.Unlock()
	if curAfterToggle.ID != "com.apple.inputmethod.SCIM.ITABC" {
		t.Fatalf("expected Toggle IME to switch to Pinyin without restore conflict, got %+v", curAfterToggle)
	}
}

func TestGenerateAppIconAndTrayIconPNG_AndSemanticIcons(t *testing.T) {
	// Verify all core semantic icons parse cleanly and return non-nil *ui.SVG
	for _, name := range CoreSemanticIconNames() {
		if svg := SemanticIconSVG(name); svg == nil {
			t.Fatalf("SemanticIconSVG(%q) returned nil", name)
		}
	}

	// Verify 512x512 App Icon generation and resources/icon.png
	appPNG := GenerateAppIconPNG(512)
	if len(appPNG) == 0 {
		t.Fatal("GenerateAppIconPNG(512) returned empty bytes")
	}
	img, err := png.Decode(bytes.NewReader(appPNG))
	if err != nil {
		t.Fatalf("png.Decode(GenerateAppIconPNG(512)) failed: %v", err)
	}
	if img.Bounds().Dx() != 512 || img.Bounds().Dy() != 512 {
		t.Fatalf("AppIcon bounds = %v, want 512x512", img.Bounds())
	}
	// Center pixel must be opaque, corner (0,0) must be transparent (squircle mask)
	centerC := color.NRGBAModel.Convert(img.At(256, 256)).(color.NRGBA)
	cornerC := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
	if centerC.A != 255 {
		t.Fatalf("expected opaque center pixel in AppIcon, got alpha=%d", centerC.A)
	}
	if cornerC.A != 0 {
		t.Fatalf("expected transparent corner pixel (0,0) in squircle AppIcon, got alpha=%d", cornerC.A)
	}

	// Ensure resources/icon.png is updated and matches the 512x512 generated icon
	iconPath := filepath.Join("..", "resources", "icon.png")
	if existing, err := os.ReadFile(iconPath); err != nil || !bytes.Equal(existing, appPNG) {
		if err := os.WriteFile(iconPath, appPNG, 0o644); err != nil {
			t.Fatalf("write %s: %v", iconPath, err)
		}
	}
	diskBytes, err := os.ReadFile(iconPath)
	if err != nil {
		t.Fatalf("read %s: %v", iconPath, err)
	}
	diskImg, err := png.Decode(bytes.NewReader(diskBytes))
	if err != nil || diskImg.Bounds().Dx() != 512 || diskImg.Bounds().Dy() != 512 {
		t.Fatalf("resources/icon.png invalid or not 512x512: err=%v bounds=%v", err, diskImg.Bounds())
	}

	// Verify 32x32 Tray Icon generation
	trayPNG := GenerateTrayIconPNG(32)
	trayImg, err := png.Decode(bytes.NewReader(trayPNG))
	if err != nil {
		t.Fatalf("png.Decode(GenerateTrayIconPNG(32)) failed: %v", err)
	}
	if trayImg.Bounds().Dx() != 32 || trayImg.Bounds().Dy() != 32 {
		t.Fatalf("TrayIcon bounds = %v, want 32x32", trayImg.Bounds())
	}

	// Verify IMEPillView renders cleanly in headless Tester
	w, h := PreferredIMEPillSize()
	tt := ui.NewTester(func(c *ui.Context) {
		IMEPillView(c, "中")
	}, w, h)
	tt.Frame()
	if !tt.HasText("中") {
		t.Fatalf("expected IMEPillView to render '中', got %v", tt.Texts())
	}
}

func TestController_TriggersAndActionEffects(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)

	var copied []string
	ctrl.ClipboardWriter = func(text string) {
		copied = append(copied, text)
	}

	// TriggerLeaderKey opens Leader
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	if ctrl.State().Phase != engine.PhaseLeader {
		t.Fatalf("expected PhaseLeader, got %s", ctrl.State().Phase)
	}

	// TriggerCapsuleToggle dismisses when open, then opens Omnibox when hidden
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerCapsuleToggle})
	if ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected PhaseHidden on toggle, got %s", ctrl.State().Phase)
	}
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerCapsuleToggle})
	if ctrl.State().Phase != engine.PhaseOmnibox {
		t.Fatalf("expected PhaseOmnibox on second toggle, got %s", ctrl.State().Phase)
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})

	// Inline snippet expansion via TriggerSnippetKey / TriggerSnippetBack
	for _, r := range ";shx" {
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetKey, Rune: string(r)})
	}
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
	for _, r := range "rug" {
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetKey, Rune: string(r)})
	}
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) >= 1
	}, "snippet expansion InjectText")
	inj := br.getInjected()[0]
	if inj.text != `¯\_(ツ)_/¯` || inj.delCount != len(";shrug") {
		t.Fatalf("unexpected snippet injection: %+v", inj)
	}

	// NormalizeGlossary action replaces selected text
	ctrl.executeEffect(engine.Effect{
		Type:    engine.EffectRunAction,
		Action:  ptrAction(config.NormalizeGlossaryAction()),
		Context: &engine.CapsuleContext{SelectedText: "using clavis daily"},
	}, ctrl.State())
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) >= 2
	}, "glossary normalize InjectText")
	if got := br.getInjected()[1].text; got != "using Clavis daily" {
		t.Fatalf("normalized text = %q, want %q", got, "using Clavis daily")
	}

	// ToggleIME switches to non-ASCII input source
	ctrl.executeEffect(engine.Effect{
		Type:   engine.EffectRunAction,
		Action: ptrAction(config.ToggleIMEAction()),
	}, ctrl.State())
	waitUntil(t, time.Second, func() bool {
		br.mu.Lock()
		defer br.mu.Unlock()
		return len(br.selectedIMEs) == 1
	}, "SelectInputSource called")

	// EffectCopyText copies and presents HUD
	ctrl.executeEffect(engine.Effect{
		Type: engine.EffectCopyText,
		Text: "copied-text",
	}, ctrl.State())
	if len(copied) != 1 || copied[0] != "copied-text" {
		t.Fatalf("copied = %v, want [copied-text]", copied)
	}
	if ctrl.State().Phase != engine.PhaseHUD {
		t.Fatalf("expected PhaseHUD after copy, got %s", ctrl.State().Phase)
	}
}

func ptrAction(a config.LeaderAction) *config.LeaderAction {
	return &a
}

func TestLeaderUI_TypeKeyDoesNotDoubleFireAndReduceTransparency(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader})

	w, h := PreferredCapsuleSize(engine.PhaseLeader, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.SetDark(true)
	tt.SetPreferences(ui.Preferences{ReduceTransparency: true, HighContrast: true})
	tt.Frame()

	// TypeKey sends KeyPressed + TextInput + KeyReleased; must drill into Window ONCE without downgrading
	tt.TypeKey(0, ui.KeyW, "w")
	tt.Frame()
	st := ctrl.State()
	if st.Phase != engine.PhaseLeader || len(st.LeaderPath) != 1 || st.LeaderPath[0] != "w" {
		t.Fatalf("expected LeaderPath=[w] after TypeKey('w'), got phase=%s path=%v", st.Phase, st.LeaderPath)
	}
}

func TestHUD_AutoDismissAndInternalCLISettings(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)

	var openedTab SettingsTab
	var settingsOpened bool
	ctrl.OnOpenSettings = func(tab SettingsTab) {
		settingsOpened = true
		openedTab = tab
	}

	// Set short HUD duration in config.toml and reload
	cfg := ctrl.Config()
	cfg.Main.Appearance.HUDDurationMs = 50
	if err := os.WriteFile(filepath.Join(ctrl.ConfigStore.Directory(), string(config.FileMain)), []byte(cfg.Main.Encode()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if !ctrl.ReloadConfig() {
		t.Fatal("ReloadConfig failed")
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: "Quick HUD"})
	if ctrl.State().Phase != engine.PhaseHUD {
		t.Fatalf("expected PhaseHUD, got %s", ctrl.State().Phase)
	}
	waitUntil(t, time.Second, func() bool {
		return ctrl.State().Phase == engine.PhaseHidden
	}, "HUD auto-dismiss")

	// clavis-setting ai opens SettingsTabAI
	ctrl.executeEffect(engine.Effect{
		Type:   engine.EffectRunAction,
		Action: ptrAction(config.CLIAction("clavis-setting", "ai")),
	}, ctrl.State())
	if !settingsOpened || openedTab != SettingsTabAI {
		t.Fatalf("settingsOpened=%v tab=%v, want SettingsTabAI", settingsOpened, openedTab)
	}

	// Window, OpenApp, and Snippet actions
	ctrl.executeEffect(engine.Effect{
		Type:   engine.EffectRunAction,
		Action: ptrAction(config.WindowAction(config.WindowMaximize)),
	}, ctrl.State())
	ctrl.executeEffect(engine.Effect{
		Type:   engine.EffectRunAction,
		Action: ptrAction(config.OpenAppAction("com.apple.Terminal")),
	}, ctrl.State())
	ctrl.executeEffect(engine.Effect{
		Type:    engine.EffectRunAction,
		Action:  ptrAction(config.SnippetAction("email-signoff")),
		Context: &engine.CapsuleContext{AppName: "Mail"},
	}, ctrl.State())

	waitUntil(t, time.Second, func() bool {
		br.mu.Lock()
		defer br.mu.Unlock()
		return len(br.windowCmds) == 1 && len(br.openedApps) == 1 && len(br.injected) == 1
	}, "Window, OpenApp, Snippet effects")
}

func TestClipboard_RapidMutationsFIFOAndGenerationGuard(t *testing.T) {
	ctrl, _, clipStore, _ := newTestController(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	_, _, _ = clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "item-a", SourceAppName: "Notes", Timestamp: now})
	_, _, _ = clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "item-b", SourceAppName: "Notes", Timestamp: now.Add(time.Second)})
	_, _, _ = clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "item-c", SourceAppName: "Notes", Timestamp: now.Add(2 * time.Second)})

	ctrl.syncClipboardEntries(ctx)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})

	// Rapidly pin item-c, then pin item-b, then delete item-b
	ctrl.Dispatch(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem}) // pins item-c
	ctrl.Dispatch(engine.Event{Type: engine.EventSelectNext})                     // moves to item-b
	ctrl.Dispatch(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem}) // pins item-b
	ctrl.Dispatch(engine.Event{Type: engine.EventDeleteSelectedClipboardItem})    // deletes item-b

	waitUntil(t, 2*time.Second, func() bool {
		recent, _ := clipStore.Recent(ctx, 10)
		if len(recent) != 2 {
			return false
		}
		var hasPinnedC, hasA bool
		for _, r := range recent {
			if r.Content == "item-c" && r.IsPinned {
				hasPinnedC = true
			}
			if r.Content == "item-a" && !r.IsPinned {
				hasA = true
			}
		}
		return hasPinnedC && hasA
	}, "FIFO serialized rapid pin + delete mutations")
}

func TestDispatch_HideBeforeHUDAndTimerSurvives(t *testing.T) {
	setShortHUD := func(t *testing.T, ctrl *AppController) {
		t.Helper()
		cfg := ctrl.Config()
		// 500ms: long enough that the headless -race frames between dispatch
		// and the PhaseHUD assertion cannot outlast the timer, yet still short
		// enough to auto-dismiss well inside the 1s waitUntil below.
		cfg.Main.Appearance.HUDDurationMs = 500
		if err := os.WriteFile(filepath.Join(ctrl.ConfigStore.Directory(), string(config.FileMain)), []byte(cfg.Main.Encode()), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if !ctrl.ReloadConfig() {
			t.Fatal("ReloadConfig failed")
		}
	}

	t.Run("EventCopyAIOutput via Cmd+C shortcut", func(t *testing.T) {
		doer := mockHTTPDoer{
			fn: func(req *http.Request) (*http.Response, error) {
				return sseResponse("AI answer"), nil
			},
		}
		ctrl, _, _, _ := newTestController(t, doer)
		setShortHUD(t, ctrl)

		var copied []string
		ctrl.ClipboardWriter = func(text string) {
			copied = append(copied, text)
		}

		var seqMu sync.Mutex
		var lifecycle []string
		ctrl.OnHide = func() {
			seqMu.Lock()
			lifecycle = append(lifecycle, "hide")
			seqMu.Unlock()
		}
		ctrl.OnPresentPhase = func(phase engine.CapsulePhase) {
			seqMu.Lock()
			lifecycle = append(lifecycle, "present:"+string(phase))
			seqMu.Unlock()
		}

		ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAI})
		ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
		waitUntil(t, 2*time.Second, func() bool {
			st := ctrl.State()
			return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIOutput == "AI answer"
		}, "AI stream complete")

		seqMu.Lock()
		lifecycle = nil
		seqMu.Unlock()

		w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, ctrl.Config().Main.Appearance)
		tt := ui.NewTester(ctrl.CapsuleView, w, h)
		tt.Frame()

		// Trigger Cmd+C / Ctrl+C shortcut
		tt.Key(primaryMod(), ui.KeyC)
		tt.Frame()

		if len(copied) != 1 || copied[0] != "AI answer" {
			t.Fatalf("copied = %v, want [AI answer]", copied)
		}
		if ctrl.State().Phase != engine.PhaseHUD {
			t.Fatalf("expected PhaseHUD after Cmd+C, got %s", ctrl.State().Phase)
		}

		seqMu.Lock()
		gotOrder := strings.Join(lifecycle, ",")
		seqMu.Unlock()
		if gotOrder != "hide,present:hud" {
			t.Fatalf("lifecycle order = %q, want %q", gotOrder, "hide,present:hud")
		}

		// Auto-dismiss timer must survive and transition to PhaseHidden
		waitUntil(t, time.Second, func() bool {
			return ctrl.State().Phase == engine.PhaseHidden
		}, "PhaseHUD auto-dismiss after EventCopyAIOutput")
	})

	t.Run("Glossary EffectCopyText", func(t *testing.T) {
		ctrl, _, _, _ := newTestController(t, nil)
		setShortHUD(t, ctrl)

		var copied []string
		ctrl.ClipboardWriter = func(text string) { copied = append(copied, text) }

		var seqMu sync.Mutex
		var lifecycle []string
		ctrl.OnHide = func() {
			seqMu.Lock()
			lifecycle = append(lifecycle, "hide")
			seqMu.Unlock()
		}
		ctrl.OnPresentPhase = func(phase engine.CapsulePhase) {
			seqMu.Lock()
			lifecycle = append(lifecycle, "present:"+string(phase))
			seqMu.Unlock()
		}

		ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabGlossary})
		ctrl.Dispatch(engine.Event{Type: engine.EventSelectNext})
		seqMu.Lock()
		lifecycle = nil
		seqMu.Unlock()

		ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
		if ctrl.State().Phase != engine.PhaseHUD {
			t.Fatalf("expected PhaseHUD after glossary copy, got %s", ctrl.State().Phase)
		}
		if len(copied) != 1 {
			t.Fatalf("expected 1 copied glossary term, got %v", copied)
		}

		seqMu.Lock()
		gotOrder := strings.Join(lifecycle, ",")
		seqMu.Unlock()
		if gotOrder != "hide,present:hud" {
			t.Fatalf("lifecycle order = %q, want %q", gotOrder, "hide,present:hud")
		}

		waitUntil(t, time.Second, func() bool {
			return ctrl.State().Phase == engine.PhaseHidden
		}, "PhaseHUD auto-dismiss after glossary copy")
	})

	t.Run("normalize_glossary action", func(t *testing.T) {
		ctrl, br, _, _ := newTestController(t, nil)
		setShortHUD(t, ctrl)

		var seqMu sync.Mutex
		var lifecycle []string
		ctrl.OnHide = func() {
			seqMu.Lock()
			lifecycle = append(lifecycle, "hide")
			seqMu.Unlock()
		}
		ctrl.OnPresentPhase = func(phase engine.CapsulePhase) {
			seqMu.Lock()
			lifecycle = append(lifecycle, "present:"+string(phase))
			seqMu.Unlock()
		}

		ctrl.Dispatch(engine.Event{
			Type: engine.EventOpenOmnibox,
			Tab:  config.TabActions,
			Context: &engine.CapsuleContext{
				SelectedText: "using clavis daily",
			},
		})
		ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "Normalize"})
		seqMu.Lock()
		lifecycle = nil
		seqMu.Unlock()

		ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
		if ctrl.State().Phase != engine.PhaseHUD {
			t.Fatalf("expected PhaseHUD after normalize_glossary, got %s", ctrl.State().Phase)
		}

		seqMu.Lock()
		gotOrder := strings.Join(lifecycle, ",")
		seqMu.Unlock()
		if gotOrder != "hide,present:hud" {
			t.Fatalf("lifecycle order = %q, want %q", gotOrder, "hide,present:hud")
		}

		waitUntil(t, time.Second, func() bool {
			return len(br.getInjected()) == 1 && br.getInjected()[0].text == "using Clavis daily"
		}, "normalize_glossary InjectText")

		waitUntil(t, time.Second, func() bool {
			return ctrl.State().Phase == engine.PhaseHidden
		}, "PhaseHUD auto-dismiss after normalize_glossary")
	})
}

func TestSnippetKey_SkipsSnapshotContextUnlessNeeded(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)

	cfg := ctrl.Config()
	cfg.Snippets.Items = append(cfg.Snippets.Items, config.SnippetItem{
		ID:      "clip-snip",
		Trigger: ";clip",
		Content: "Clip={{clipboard}} App={{app}}",
	})
	if err := os.WriteFile(filepath.Join(ctrl.ConfigStore.Directory(), string(config.FileSnippets)), []byte(cfg.Snippets.Encode()), 0o600); err != nil {
		t.Fatalf("write snippets: %v", err)
	}
	if !ctrl.ReloadConfig() {
		t.Fatal("ReloadConfig failed")
	}

	// Non-matching keystrokes + normal snippet (;shrug) must NOT call SnapshotContext
	for _, r := range "hello ;shrug" {
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetKey, Rune: string(r)})
	}
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) == 1
	}, "shrug snippet injected")
	if got := br.getSnapshotCalls(); got != 0 {
		t.Fatalf("SnapshotContext calls = %d, want 0 for non-clipboard/non-app snippet", got)
	}

	// Matching ;clip lazily calls SnapshotContext once
	for _, r := range ";clip" {
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetKey, Rune: string(r)})
	}
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) == 2
	}, "clip snippet injected")
	if got := br.getSnapshotCalls(); got != 1 {
		t.Fatalf("SnapshotContext calls = %d, want 1 after ;clip match", got)
	}
	if got := br.getInjected()[1].text; got != "Clip=clipboard sample App=TextEdit" {
		t.Fatalf("injected clip snippet = %q, want %q", got, "Clip=clipboard sample App=TextEdit")
	}
}

func TestBeforeInject_OpenOmnibox_And_TapRecoveryDiagnostics(t *testing.T) {
	ctrl, br, clipStore, _ := newTestController(t, nil)
	ctx := context.Background()
	_, _, _ = clipStore.Record(ctx, clipboard.CaptureCandidate{
		Content:       "tray-synced-clip",
		SourceAppName: "Notes",
		Timestamp:     time.Now().UTC(),
	})

	br.mu.Lock()
	br.tapRecoveries = 3
	br.mu.Unlock()

	if _, err := ctrl.RefreshPermissions(ctx); err != nil {
		t.Fatalf("RefreshPermissions: %v", err)
	}
	if diag := ctrl.Diagnostics(); diag.RecoveryCount != 3 {
		t.Fatalf("Diagnostics().RecoveryCount = %d, want 3", diag.RecoveryCount)
	}

	var orderMu sync.Mutex
	var order []string
	ctrl.BeforeInject = func(context.Context) {
		orderMu.Lock()
		order = append(order, "before_inject")
		orderMu.Unlock()
	}

	ctrl.OpenOmnibox(config.TabClipboard, "")
	st := ctrl.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard {
		t.Fatalf("expected PhaseOmnibox TabClipboard, got phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}
	if st.Context.AppName != "TextEdit" || len(st.OmniboxItems) != 1 || st.OmniboxItems[0].ClipboardContent != "tray-synced-clip" {
		t.Fatalf("expected synced context and clipboard item, got ctx=%+v items=%+v", st.Context, st.OmniboxItems)
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) == 1
	}, "clipboard item injected")

	orderMu.Lock()
	gotOrder := append([]string(nil), order...)
	orderMu.Unlock()
	if len(gotOrder) != 1 || gotOrder[0] != "before_inject" {
		t.Fatalf("expected BeforeInject to run before InjectText, got %v", gotOrder)
	}
}

// ---- T4: Smart Leader IME lock/restore, non-destructive HUD, IME rule binding, config CRUD ----

type pillRecorder struct {
	mu     sync.Mutex
	labels []string
}

func (p *pillRecorder) cb(label string, _ *host.CursorAnchor) {
	p.mu.Lock()
	p.labels = append(p.labels, label)
	p.mu.Unlock()
}

func (p *pillRecorder) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.labels...)
}

func (p *pillRecorder) last() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.labels) == 0 {
		return ""
	}
	return p.labels[len(p.labels)-1]
}

func TestPreferredPillSizeForLabel(t *testing.T) {
	for _, short := range []string{"EN", "中", "ABC"} {
		w, h := PreferredPillSizeForLabel(short)
		if w != 68 || h != 30 {
			t.Fatalf("PreferredPillSizeForLabel(%q) = (%d,%d), want (68,30)", short, w, h)
		}
	}
	w, h := PreferredPillSizeForLabel("LAYER • NAV")
	if h != 32 || w <= 68 || w > 340 {
		t.Fatalf("PreferredPillSizeForLabel(LAYER • NAV) = (%d,%d), want width in (68,340] and height 32", w, h)
	}
	longW, longH := PreferredPillSizeForLabel(strings.Repeat("x", 200))
	if longW != 340 || longH != 32 {
		t.Fatalf("long label size = (%d,%d), want capped (340,32)", longW, longH)
	}
	if w2, _ := PreferredPillSizeForLabel("Bound Notes → ASCII"); w2 <= w {
		t.Fatalf("longer label width %d should exceed %d", w2, w)
	}
}

func TestLeader_TemporaryASCIILockAndRestoreOnOmniboxDowngrade(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)

	pills := &pillRecorder{}
	ctrl.OnShowIMEPill = pills.cb

	// Chinese (Pinyin) IME active before opening Leader.
	br.mu.Lock()
	br.currentIME = host.InputSourceInfo{ID: "com.apple.inputmethod.SCIM.ITABC", Name: "Pinyin", IsASCII: false}
	br.mu.Unlock()

	ctrl.OpenLeader()
	if ctrl.State().Phase != engine.PhaseLeader {
		t.Fatalf("expected PhaseLeader, got %s", ctrl.State().Phase)
	}

	lockCalls, _ := br.getIMEPillCalls()
	afterLock := br.getCurrentIME()
	if lockCalls != 1 {
		t.Fatalf("LockTemporaryASCII calls = %d, want 1", lockCalls)
	}
	if !afterLock.IsASCII || afterLock.ID != "com.apple.keylayout.ABC" {
		t.Fatalf("expected temporary ASCII lock on Leader open, got %+v", afterLock)
	}
	if got := pills.all(); len(got) == 0 || got[0] != "EN" {
		t.Fatalf("expected OnShowIMEPill('EN') on ASCII lock, got %v", got)
	}

	// Pressing "/" downgrades Leader -> Actions Omnibox and must immediately restore the native IME.
	w, h := PreferredCapsuleSize(engine.PhaseLeader, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("/")
	tt.Frame()

	st := ctrl.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabActions || st.OmniboxQuery != "" {
		t.Fatalf("expected clean Actions Omnibox after '/', got phase=%s tab=%s query=%q", st.Phase, st.OmniboxTab, st.OmniboxQuery)
	}

	_, restoreCalls := br.getIMEPillCalls()
	afterDowngrade := br.getCurrentIME()
	if restoreCalls != 1 {
		t.Fatalf("RestoreTemporaryIME calls = %d, want 1 after Leader -> Omnibox", restoreCalls)
	}
	if afterDowngrade.ID != "com.apple.inputmethod.SCIM.ITABC" {
		t.Fatalf("expected Chinese IME restored for Omnibox search, got %+v", afterDowngrade)
	}
}

func TestShowHUDWhileOmniboxOpenKeepsSessionIntact(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	pills := &pillRecorder{}
	ctrl.OnShowIMEPill = pills.cb

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "git"})
	if st := ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxQuery != "git" {
		t.Fatalf("setup failed: phase=%s query=%q", st.Phase, st.OmniboxQuery)
	}
	itemsBefore := len(ctrl.State().OmniboxItems)

	ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: "Copied to clipboard"})

	st := ctrl.State()
	if st.Phase != engine.PhaseOmnibox {
		t.Fatalf("EventShowHUD must not clobber an open Omnibox, got phase=%s", st.Phase)
	}
	if st.OmniboxQuery != "git" || len(st.OmniboxItems) != itemsBefore || itemsBefore == 0 {
		t.Fatalf("EventShowHUD cleared Omnibox state: query=%q items=%d (want %d)", st.OmniboxQuery, len(st.OmniboxItems), itemsBefore)
	}
	if got := pills.last(); got != "Copied to clipboard" {
		t.Fatalf("expected floating pill 'Copied to clipboard', got %v", pills.all())
	}

	// ShowToastOrHUD keeps the session open too.
	ctrl.ShowToastOrHUD("Bound WeChat → NATIVE IME")
	if st := ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxQuery != "git" {
		t.Fatalf("ShowToastOrHUD must not clobber an open Omnibox, got phase=%s query=%q", st.Phase, st.OmniboxQuery)
	}
	if got := pills.last(); got != "Bound WeChat → NATIVE IME" {
		t.Fatalf("expected pill 'Bound WeChat → NATIVE IME', got %v", pills.all())
	}

	// With no pill sink wired, HUD dispatch still reaches PhaseHUD (headless behaviour preserved).
	ctrl.OnShowIMEPill = nil
	ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
	ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: "plain hud"})
	if ctrl.State().Phase != engine.PhaseHUD {
		t.Fatalf("expected PhaseHUD without pill sink, got %s", ctrl.State().Phase)
	}
}

func TestBindAppIMERule_PersistsAppliesAndUnbinds(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)
	pills := &pillRecorder{}
	ctrl.OnShowIMEPill = pills.cb

	appCtx := engine.CapsuleContext{
		BundleID:    "com.tencent.xinWeChat",
		ProcessName: "WeChat",
		AppName:     "WeChat",
	}
	rulesPath := filepath.Join(ctrl.ConfigStore.Directory(), string(config.FileIMERules))

	// 1. Bind WeChat to native (Chinese) IME.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: ">native", Context: &appCtx})
	items := ctrl.State().OmniboxItems
	idx := -1
	for i, it := range items {
		if it.Kind == engine.ItemAppIMERule && it.IMEMode == config.IMEModeNative {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("expected a native IME bind item, got %+v", items)
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: idx})

	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read %s: %v", rulesPath, err)
	}
	if !strings.Contains(string(data), "com.tencent.xinWeChat") || !strings.Contains(string(data), "native") {
		t.Fatalf("ime-rules.toml missing WeChat native rule:\n%s", data)
	}

	var bound *config.IMERule
	for i, r := range ctrl.Config().IMERules.Rules {
		if r.BundleID == "com.tencent.xinWeChat" {
			bound = &ctrl.Config().IMERules.Rules[i]
		}
	}
	if bound == nil || bound.PreferredMode != config.IMEModeNative || !bound.RestoreOnLeave {
		t.Fatalf("expected persisted native rule, got %+v", bound)
	}
	if bound.InputSourceID != "" {
		t.Fatalf("native rule must leave InputSourceID empty, got %q", bound.InputSourceID)
	}

	modes := br.getIMEModeCalls()
	if len(modes) == 0 || modes[len(modes)-1] != config.IMEModeNative {
		t.Fatalf("expected SetIMEMode(native), got %v", modes)
	}
	if ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected capsule to close after binding, got %s", ctrl.State().Phase)
	}
	if got := pills.last(); !strings.Contains(got, "NATIVE") {
		t.Fatalf("expected confirmation pill mentioning NATIVE, got %v", pills.all())
	}

	// 2. Unbind WeChat (IMEMode == "").
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: ">unbind", Context: &appCtx})
	items = ctrl.State().OmniboxItems
	unbindIdx := -1
	for i, it := range items {
		if it.Kind == engine.ItemAppIMERule && it.IMEMode == "" {
			unbindIdx = i
			break
		}
	}
	if unbindIdx < 0 {
		t.Fatalf("expected an unbind IME item, got %+v", items)
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: unbindIdx})

	for _, r := range ctrl.Config().IMERules.Rules {
		if r.BundleID == "com.tencent.xinWeChat" {
			t.Fatalf("expected WeChat rule removed, still present: %+v", r)
		}
	}
	data, err = os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read %s after unbind: %v", rulesPath, err)
	}
	if strings.Contains(string(data), "com.tencent.xinWeChat") {
		t.Fatalf("ime-rules.toml still contains WeChat after unbind:\n%s", data)
	}

	// 3. No active app -> warning instead of a panic/persisted rule.
	before := len(ctrl.Config().IMERules.Rules)
	ctrl.executeEffect(engine.Effect{
		Type:    engine.EffectBindAppIMERule,
		IMEMode: config.IMEModeASCII,
		Context: &engine.CapsuleContext{},
	}, ctrl.State())
	if got := len(ctrl.Config().IMERules.Rules); got != before {
		t.Fatalf("empty context must not add IME rules, got %d want %d", got, before)
	}
	if got := pills.last(); got != "No active app detected" {
		t.Fatalf("expected 'No active app detected' notice, got %v", pills.all())
	}

}

func TestConfigCRUDHelpersPersistAndUpdateLiveEngines(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)
	pills := &pillRecorder{}
	ctrl.OnShowIMEPill = pills.cb

	readFile := func(kind config.ConfigFileKind) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(ctrl.ConfigStore.Directory(), string(kind)))
		if err != nil {
			t.Fatalf("read %s: %v", kind, err)
		}
		return string(data)
	}

	// ---- Leader ----
	leaderCfg := ctrl.Config().Leader
	leaderCfg.Nodes = append(leaderCfg.Nodes, config.LeaderNode{
		Key:    "z",
		Label:  "Zed Notes",
		Action: config.OpenAppAction("com.zed.notes"),
	})
	if err := ctrl.SaveLeaderConfig(leaderCfg); err != nil {
		t.Fatalf("SaveLeaderConfig: %v", err)
	}
	if !strings.Contains(readFile(config.FileLeader), "Zed Notes") {
		t.Fatalf("leader.toml missing new node:\n%s", readFile(config.FileLeader))
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader})
	found := false
	for _, n := range ctrl.State().LeaderNodes {
		if n.Label == "Zed Notes" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected StateMachine to pick up the saved leader node")
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})

	// ---- Snippets ----
	snipCfg := ctrl.Config().Snippets
	snipCfg.Items = append(snipCfg.Items, config.SnippetItem{ID: "zz-t4", Trigger: ";zzt4", Content: "ZZ-T4-EXPAND"})
	if err := ctrl.SaveSnippetsConfig(snipCfg); err != nil {
		t.Fatalf("SaveSnippetsConfig: %v", err)
	}
	if !strings.Contains(readFile(config.FileSnippets), ";zzt4") {
		t.Fatalf("snippets.toml missing new trigger:\n%s", readFile(config.FileSnippets))
	}
	for _, r := range ";zzt4" {
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetKey, Rune: string(r)})
	}
	waitUntil(t, time.Second, func() bool {
		return len(br.getInjected()) == 1
	}, "snippet engine live-reload expansion")
	if got := br.getInjected()[0].text; got != "ZZ-T4-EXPAND" {
		t.Fatalf("injected snippet = %q, want ZZ-T4-EXPAND", got)
	}

	// ---- Glossary ----
	glossCfg := ctrl.Config().Glossary
	glossCfg.Terms = append(glossCfg.Terms, config.GlossaryTerm{Term: "Kubernetes", Aliases: []string{"k8s"}})
	if err := ctrl.SaveGlossaryConfig(glossCfg); err != nil {
		t.Fatalf("SaveGlossaryConfig: %v", err)
	}
	if !strings.Contains(readFile(config.FileGlossary), "Kubernetes") {
		t.Fatalf("glossary.toml missing new term:\n%s", readFile(config.FileGlossary))
	}
	if !hasGlossaryTerm(ctrl.Glossary.Terms, "Kubernetes") {
		t.Fatal("ctrl.Glossary not live-reloaded")
	}
	if ctrl.AIClient == nil || !hasGlossaryTerm(ctrl.AIClient.GlossaryEngine.Terms, "Kubernetes") {
		t.Fatal("AIClient.GlossaryEngine not live-reloaded")
	}

	// ---- IME rules ----
	rulesBefore := len(ctrl.Config().IMERules.Rules)
	imeCfg := ctrl.Config().IMERules
	imeCfg.Rules = append(imeCfg.Rules, config.IMERule{
		BundleID:       "com.apple.Safari",
		AppName:        "Safari",
		PreferredMode:  config.IMEModeASCII,
		InputSourceID:  "com.apple.keylayout.ABC",
		RestoreOnLeave: true,
	})
	if err := ctrl.SaveIMERulesConfig(imeCfg); err != nil {
		t.Fatalf("SaveIMERulesConfig: %v", err)
	}
	if !strings.Contains(readFile(config.FileIMERules), "com.apple.Safari") {
		t.Fatalf("ime-rules.toml missing Safari rule:\n%s", readFile(config.FileIMERules))
	}
	if got := len(ctrl.Config().IMERules.Rules); got != rulesBefore+1 {
		t.Fatalf("rules len = %d, want %d", got, rulesBefore+1)
	}
	br.mu.Lock()
	updatedRules := br.updatedIMERule
	br.mu.Unlock()
	if len(updatedRules) == 0 || len(updatedRules[len(updatedRules)-1].Rules) != rulesBefore+1 {
		t.Fatalf("expected bridge.UpdateIMEConfig with %d rules, got %+v", rulesBefore+1, updatedRules)
	}

	// ---- AvailableInputSources + keymap layer diagnostics ----
	if srcs := ctrl.AvailableInputSources(context.Background()); len(srcs) != 2 {
		t.Fatalf("AvailableInputSources() = %d sources, want 2", len(srcs))
	}
	ctrl.NotifyKeymapLayer("nav")
	if got := pills.last(); got != "LAYER • NAV" {
		t.Fatalf("expected 'LAYER • NAV' pill, got %v", pills.all())
	}
	if got := ctrl.Diagnostics().ActiveKeymapLayer; got != "nav" {
		t.Fatalf("Diagnostics().ActiveKeymapLayer = %q, want nav", got)
	}
	ctrl.NotifyKeymapLayer("")
	if got := ctrl.Diagnostics().ActiveKeymapLayer; got != "" {
		t.Fatalf("Diagnostics().ActiveKeymapLayer = %q after exit, want empty", got)
	}
	// Diagnostics falls back to the live bridge layer when the controller has none cached.
	br.setActiveKeymapLayer("hyper")
	if got := ctrl.Diagnostics().ActiveKeymapLayer; got != "hyper" {
		t.Fatalf("Diagnostics().ActiveKeymapLayer = %q, want hyper from bridge", got)
	}
}

func hasGlossaryTerm(terms []config.GlossaryTerm, want string) bool {
	for _, term := range terms {
		if term.Term == want {
			return true
		}
	}
	return false
}
