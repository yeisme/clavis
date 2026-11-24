package uiapp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type mockHerdrManageCLI struct {
	mu      sync.Mutex
	calls   []string
	agents  string
	workspc string
	sendErr error
}

func (m *mockHerdrManageCLI) LookPath(string) (string, error) { return "/fake/herdr", nil }
func (m *mockHerdrManageCLI) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	m.mu.Lock()
	m.calls = append(m.calls, joined)
	m.mu.Unlock()
	if len(args) >= 2 && args[0] == "agent" && args[1] == "list" {
		return []byte(m.agents), nil
	}
	if len(args) >= 2 && args[0] == "workspace" && args[1] == "list" {
		return []byte(m.workspc), nil
	}
	if len(args) >= 2 && args[0] == "agent" && args[1] == "send" && m.sendErr != nil {
		return nil, m.sendErr
	}
	return []byte("{}"), nil
}

func TestQuickJumpHashUIAndRoutingEffects(t *testing.T) {
	var openedURL, openedDir string
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	if err := cfgStore.SaveQuickJump(config.QuickJumpConfig{
		Bookmarks: []config.QuickJumpBookmark{
			{ID: "docs", Name: "Clavis Docs", Kind: config.QuickJumpURL, Target: "https://example.com/search?q={query}", Tags: []string{"doc"}},
			{ID: "repo", Name: "Work Dir", Kind: config.QuickJumpDir, Target: "/tmp/work", Tags: []string{"work"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		OpenExternal:    func(u string) error { openedURL = u; return nil },
		OpenPath:        func(p string) error { openedDir = p; return nil },
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "＃docs test"})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("Quick Jump") && !tt.HasText("快速跳转") {
		t.Fatalf("expected Quick Jump mode badge, got %v", tt.Texts())
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	if openedURL != "https://example.com/search?q=test" {
		t.Fatalf("openedURL = %q, want expanded query URL", openedURL)
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "#repo"})
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	if openedDir != "/tmp/work" {
		t.Fatalf("openedDir = %q, want /tmp/work", openedDir)
	}
}

func TestAgentManageGatedControlsAndManagement2x3Hub(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mockCLI := &mockHerdrManageCLI{
		workspc: `{"result":{"workspaces":[{"workspace_id":"w4","label":"clavis","number":4,"agent_status":"working","focused":true}]}}`,
		agents:  `{"result":{"agents":[{"agent":"codex","agent_session":{"value":"s-1"},"agent_status":"working","name":"coder","pane_id":"w4:p1","workspace_id":"w4","focused":true}]}}`,
	}
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   mockCLI,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	waitUntil(t, 5*time.Second, func() bool {
		sessions, _ := ctrl.cachedAgentData()
		return len(sessions) > 0 && len(ctrl.cachedAgentWorkspaces()) > 0
	}, "live herdr session & workspace loaded")

	// Unconfirmed SendHerdrPrompt fails safely; confirmed succeeds.
	if _, err := ctrl.SendHerdrPrompt("w4:p1", "continue", false); err == nil {
		t.Fatal("expected unconfirmed SendHerdrPrompt to fail")
	}
	if _, err := ctrl.SendHerdrPrompt("w4:p1", "continue", true); err != nil {
		t.Fatalf("confirmed SendHerdrPrompt failed: %v", err)
	}

	// Ambiguous failure sets honest warning and never retries.
	mockCLI.sendErr = errors.New("eof")
	res, err := ctrl.SendHerdrPrompt("w4:p1", "again", true)
	if err == nil || res.Retryable {
		t.Fatalf("expected non-retryable unknown failure, got res=%+v err=%v", res, err)
	}

	// Allowlisted StartAgent executes cleanly.
	if _, err := ctrl.StartHerdrAgent("w4", "codex", "helper"); err != nil {
		t.Fatalf("StartHerdrAgent failed: %v", err)
	}

	// Render TabAgent Workbench & Management Hub 2x3 grid + Leader Quick Tools.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAgent})
	waitUntil(t, 2*time.Second, func() bool { return !ctrl.agentRefreshing() }, "tab agent settled")
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("clavis") || (!tt.HasText("Send Prompt") && !tt.HasText("发送指令")) {
		t.Fatalf("agent manage controls missing: %v", tt.Texts())
	}

	// Switch to Management Hub (⌘8) and confirm all 6 cards render.
	ctrl.ToggleHubSurface(HubSurfaceManage)
	tt.Frame()
	if !tt.HasText("AI & Vision") && !tt.HasText("AI 与 Vision") {
		t.Fatalf("Management 2x3 AI card missing: %v", tt.Texts())
	}
	if !tt.HasText("Vault & Quick Jump") && !tt.HasText("Vault 与快跳") {
		t.Fatalf("Management 2x3 Vault card missing: %v", tt.Texts())
	}

	// Leader Dashboard shows Quick Tools strip cleanly.
	ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader})
	lw, lh := PreferredCapsuleSize(engine.PhaseLeader, ctrl.Config().Main.Appearance)
	ltt := ui.NewTester(ctrl.CapsuleView, lw, lh)
	ltt.Frame()
	if !ltt.HasText("OCR") || (!ltt.HasText("Capture") && !ltt.HasText("截图")) {
		t.Fatalf("Leader Quick Tools missing: %v", ltt.Texts())
	}
}

func TestAgentManageWorkspaceFilterFocusRenameAndWatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mockCLI := &mockHerdrManageCLI{
		workspc: `{"result":{"workspaces":[{"workspace_id":"w4","label":"clavis","number":4},{"workspace_id":"w9","label":"novels","number":9}]}}`,
		agents:  `{"result":{"agents":[{"agent":"codex","agent_session":{"value":"s-1"},"agent_status":"working","name":"coder","pane_id":"w4:p1","workspace_id":"w4"}]}}`,
	}
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   mockCLI,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	// SetAgentWorkspaceFilter sets, resets agentSelected, and toggles off on repeat call.
	ctrl.SetAgentWorkspaceFilter("w4")
	if got := ctrl.agentWorkspaceFilter(); got != "w4" {
		t.Fatalf("workspace filter = %q, want w4", got)
	}
	ctrl.SetAgentWorkspaceFilter("w4")
	if got := ctrl.agentWorkspaceFilter(); got != "" {
		t.Fatalf("repeat SetAgentWorkspaceFilter should clear, got %q", got)
	}

	// FilterAgentItemsByWorkspace matches WorkspaceID or WorkspaceLabel and preserves controls/refresh items.
	sessions := []agentsession.Session{
		{ID: "s1", Provider: "codex", PaneID: "w4:p1", WorkspaceID: "w4", WorkspaceLabel: "clavis", HerdrManaged: true},
		{ID: "s2", Provider: "agy", PaneID: "w9:p1", WorkspaceID: "w9", WorkspaceLabel: "novels", HerdrManaged: true},
	}
	allItems := []engine.OmniboxItem{
		{ID: "agent:s1", Kind: engine.ItemUtility, UtilityValue: "agent-session:codex:s1"},
		{ID: "agent:s2", Kind: engine.ItemUtility, UtilityValue: "agent-session:agy:s2"},
		{ID: "cmd:1", Kind: engine.ItemUtility, UtilityValue: "agent-command:refresh"},
		{ID: "cmd:2", Kind: engine.ItemUtility, UtilityValue: "agent-refresh-status"},
	}
	if unfiltered := FilterAgentItemsByWorkspace(allItems, sessions, ""); len(unfiltered) != 4 {
		t.Fatalf("empty filter returned %d items", len(unfiltered))
	}
	byID := FilterAgentItemsByWorkspace(allItems, sessions, "w4")
	if len(byID) != 3 || byID[0].ID != "agent:s1" {
		t.Fatalf("FilterAgentItemsByWorkspace by ID = %+v", byID)
	}
	byLabel := FilterAgentItemsByWorkspace(allItems, sessions, "NOVELS")
	if len(byLabel) != 3 || byLabel[0].ID != "agent:s2" {
		t.Fatalf("FilterAgentItemsByWorkspace by case-insensitive label = %+v", byLabel)
	}

	// FocusHerdrWorkspace: empty workspace sets error status, valid workspace calls `workspace focus w9`.
	if _, err := ctrl.FocusHerdrWorkspace(""); err == nil || !agentManageStateFor(ctrl).statusIsErr {
		t.Fatal("expected empty workspace FocusHerdrWorkspace to fail and set statusIsErr")
	}
	if _, err := ctrl.FocusHerdrWorkspace("w9"); err != nil || agentManageStateFor(ctrl).statusIsErr {
		t.Fatalf("FocusHerdrWorkspace w9 failed: err=%v statusIsErr=%v", err, agentManageStateFor(ctrl).statusIsErr)
	}

	// RenameHerdrAgent: >40 runes fails; valid rename clears draft & invokes `agent rename`.
	st := agentManageStateFor(ctrl)
	st.mu.Lock()
	st.renameOpen = true
	st.renameDraft = "draft"
	st.mu.Unlock()
	if _, err := ctrl.RenameHerdrAgent("w4:p1", strings.Repeat("x", 41)); err == nil || !st.statusIsErr {
		t.Fatal("expected >40 rune rename failure")
	}
	if _, err := ctrl.RenameHerdrAgent("w4:p1", "builder"); err != nil {
		t.Fatalf("RenameHerdrAgent valid failed: %v", err)
	}
	st.mu.Lock()
	openAfter, draftAfter := st.renameOpen, st.renameDraft
	st.mu.Unlock()
	if openAfter || draftAfter != "" {
		t.Fatalf("expected rename state cleared, got open=%v draft=%q", openAfter, draftAfter)
	}

	// WatchHerdrAgentCompletion: empty target is a no-op; valid target invokes wait once & clears watching.
	ctrl.WatchHerdrAgentCompletion("  ", "ignored")
	ctrl.WatchHerdrAgentCompletion("w4:p1", "coder")
	ctrl.WatchHerdrAgentCompletion("w4:p1", "coder") // dedup while active or sequential
	waitUntil(t, 2*time.Second, func() bool {
		mockCLI.mu.Lock()
		hasWait := slices.Contains(mockCLI.calls, "agent wait w4:p1 --state idle --timeout 45")
		mockCLI.mu.Unlock()
		st.mu.Lock()
		watching := st.watching["w4:p1"]
		st.mu.Unlock()
		return hasWait && !watching
	}, "WatchHerdrAgentCompletion executes wait and clears active flag")
}
