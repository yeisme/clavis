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
	"github.com/yeisme/clavis/pkg/core/host"
	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
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
	cs := st.sessionLocked("w4:p1", true)
	cs.renameOpen = true
	cs.renameDraft = "draft"
	st.mu.Unlock()
	if _, err := ctrl.RenameHerdrAgent("w4:p1", strings.Repeat("x", 41)); err == nil {
		t.Fatal("expected >40 rune rename failure")
	}
	st.mu.Lock()
	renameErr := st.sessionLocked("w4:p1", false) != nil && st.sessionLocked("w4:p1", false).statusIsErr
	st.mu.Unlock()
	if !renameErr {
		t.Fatal("expected session statusIsErr set on rename failure")
	}
	if _, err := ctrl.RenameHerdrAgent("w4:p1", "builder"); err != nil {
		t.Fatalf("RenameHerdrAgent valid failed: %v", err)
	}
	st.mu.Lock()
	var openAfter bool
	var draftAfter string
	if cur := st.sessionLocked("w4:p1", false); cur != nil {
		openAfter, draftAfter = cur.renameOpen, cur.renameDraft
	}
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

func TestAgentManageControlsIsolateStatePerSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mockCLI := &mockHerdrManageCLI{
		workspc: `{"result":{"workspaces":[{"workspace_id":"w4","label":"clavis","number":4}]}}`,
		agents: `{"result":{"agents":[` +
			`{"agent":"codex","agent_session":{"value":"s-1"},"agent_status":"working","name":"coder-a","pane_id":"w4:p1","workspace_id":"w4"},` +
			`{"agent":"claude","agent_session":{"value":"s-2"},"agent_status":"idle","name":"reviewer-b","pane_id":"w4:p2","workspace_id":"w4"}` +
			`]}}`,
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

	now := time.Now()
	settleAgentTabForTest(t, ctrl, []agentsession.Session{
		{ID: "s-1", Provider: "codex", Title: "Coder A", AgentName: "coder-a", PaneID: "w4:p1", WorkspaceID: "w4", WorkspaceLabel: "clavis", Status: agentsession.StatusRunning, LastActivityAt: now, HerdrManaged: true},
		{ID: "s-2", Provider: "claude", Title: "Reviewer B", AgentName: "reviewer-b", PaneID: "w4:p2", WorkspaceID: "w4", WorkspaceLabel: "clavis", Status: agentsession.StatusWaiting, LastActivityAt: now.Add(-time.Minute), HerdrManaged: true},
	}, nil, host.OperationResult{State: host.StateReady, ObservedAt: now})

	// Arm a prompt + rename draft + status message on Session A (w4:p1).
	st := agentManageStateFor(ctrl)
	st.mu.Lock()
	csA := st.sessionLocked("w4:p1", true)
	csA.promptOpen = true
	csA.promptDraft = "delete temp cache on A"
	csA.confirmArmed = true
	csA.renameOpen = true
	csA.renameDraft = "renamed-a"
	csA.statusMsg = "session-a-warning"
	csA.statusIsErr = true
	st.mu.Unlock()

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("Confirm Send") && !tt.HasText("确认发送到终端") {
		t.Fatalf("expected Session A to show Confirm Send when armed, got %v", tt.Texts())
	}
	if !tt.HasText("session-a-warning") {
		t.Fatalf("expected Session A status message visible, got %v", tt.Texts())
	}

	// Switch to Session B (w4:p2): MUST NOT inherit confirmArmed, promptDraft, renameDraft, or statusMsg.
	ctrl.mu.Lock()
	ctrl.uiState.agentSelected = 1
	ctrl.mu.Unlock()
	tt.Frame()
	if tt.HasText("Confirm Send") || tt.HasText("确认发送到终端") {
		t.Fatalf("Session B inherited Session A confirmArmed state: %v", tt.Texts())
	}
	if tt.HasText("delete temp cache on A") || tt.HasText("renamed-a") || tt.HasText("session-a-warning") {
		t.Fatalf("Session B leaked Session A draft/status: %v", tt.Texts())
	}
	st.mu.Lock()
	if csB := st.sessionLocked("w4:p2", false); csB != nil && (csB.confirmArmed || csB.promptDraft != "" || csB.renameDraft != "" || csB.statusMsg != "") {
		st.mu.Unlock()
		t.Fatalf("Session B unexpectedly populated control state: %+v", csB)
	}
	st.mu.Unlock()

	// Sending on Session A clears only Session A and leaves Session B untouched.
	if _, err := ctrl.SendHerdrPrompt("w4:p1", "safe prompt", true); err != nil {
		t.Fatalf("SendHerdrPrompt on Session A failed: %v", err)
	}
	st.mu.Lock()
	if curA := st.sessionLocked("w4:p1", false); curA == nil || curA.confirmArmed || curA.promptOpen || curA.promptDraft != "" {
		st.mu.Unlock()
		t.Fatalf("Session A prompt state not cleared after send: %+v", curA)
	}
	if curB := st.sessionLocked("w4:p2", false); curB != nil && curB.statusMsg != "" {
		st.mu.Unlock()
		t.Fatalf("Session A status bled into Session B: %+v", curB)
	}
	// Bounded cleanup prevents unbounded session map growth.
	for i := 0; i < maxAgentSessionControlStates+25; i++ {
		cs := st.sessionLocked("pane-"+string(rune('A'+(i%26)))+"-"+time.Duration(i).String(), true)
		cs.promptDraft = "draft"
	}
	count := len(st.sessions)
	st.mu.Unlock()
	if count > maxAgentSessionControlStates {
		t.Fatalf("session control state map exceeded cap: got %d, max %d", count, maxAgentSessionControlStates)
	}
}

type blockingHerdrWaitCLI struct {
	started   chan string
	cancelled chan string
}

func (b *blockingHerdrWaitCLI) LookPath(string) (string, error) { return "/fake/herdr", nil }
func (b *blockingHerdrWaitCLI) Output(ctx context.Context, _ string, args ...string) ([]byte, error) {
	if len(args) >= 3 && args[0] == "agent" && args[1] == "wait" {
		select {
		case b.started <- args[2]:
		default:
		}
		<-ctx.Done()
		select {
		case b.cancelled <- args[2]:
		default:
		}
		return nil, ctx.Err()
	}
	return []byte("{}"), nil
}

func TestWatchHerdrAgentCompletionCancelledByStopAllAndClose(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	waitCLI := &blockingHerdrWaitCLI{
		started:   make(chan string, 4),
		cancelled: make(chan string, 4),
	}
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	var toastMu sync.Mutex
	var toasts []string
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   waitCLI,
		ClipboardWriter: func(string) {},
	})
	ctrl.mu.Lock()
	ctrl.OnShowIMEPill = func(msg string, _ *host.CursorAnchor) {
		toastMu.Lock()
		toasts = append(toasts, msg)
		toastMu.Unlock()
	}
	ctrl.mu.Unlock()
	t.Cleanup(ctrl.Close)

	// 1. StopAll cancels an active WatchHerdrAgentCompletion via runtimecoord.AgentWait.
	ctrl.WatchHerdrAgentCompletion("w4:p1", "coder")
	select {
	case got := <-waitCLI.started:
		if got != "w4:p1" {
			t.Fatalf("wait started for %q, want w4:p1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchHerdrAgentCompletion did not start WaitState")
	}

	report := ctrl.StopAll("user")
	foundAgentWait := false
	for _, op := range report.Operations {
		if op.Kind == runtimecoord.AgentWait && op.WasCancelled {
			foundAgentWait = true
			break
		}
	}
	if !foundAgentWait {
		t.Fatalf("expected runtimecoord.AgentWait in StopAll report, got %+v", report.Operations)
	}
	select {
	case got := <-waitCLI.cancelled:
		if got != "w4:p1" {
			t.Fatalf("cancelled target = %q, want w4:p1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StopAll did not cancel in-flight WaitState context")
	}
	st := agentManageStateFor(ctrl)
	waitUntil(t, 2*time.Second, func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.watching["w4:p1"]
	}, "watching flag cleared after StopAll cancellation")

	toastMu.Lock()
	gotToasts := append([]string(nil), toasts...)
	toastMu.Unlock()
	if len(gotToasts) != 0 {
		t.Fatalf("cancelled watch should not emit spurious error toast, got %v", gotToasts)
	}

	// 2. Close() cancels an active WatchHerdrAgentCompletion cleanly.
	ctrl.WatchHerdrAgentCompletion("w4:p2", "reviewer")
	select {
	case got := <-waitCLI.started:
		if got != "w4:p2" {
			t.Fatalf("second wait started for %q, want w4:p2", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second WatchHerdrAgentCompletion did not start")
	}
	ctrl.Close()
	select {
	case got := <-waitCLI.cancelled:
		if got != "w4:p2" {
			t.Fatalf("Close cancelled target = %q, want w4:p2", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not cancel in-flight WatchHerdrAgentCompletion")
	}
}
