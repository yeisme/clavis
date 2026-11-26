package uiapp

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/terminal"
)

func TestAgentItemsConversationAndCommandCompletion(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.Chinese)

	now := time.Now()
	sessions := []agentsession.Session{{ID: "cx-1", Provider: "codex", Title: "输入法验收", Status: agentsession.StatusRunning, LastActivityAt: now, LastMessage: "请运行 go test", PendingReview: true, Workspace: "clavis"}}
	items := agentItems(sessions, nil, "验收")
	if len(items) != 1 || items[0].Title != "输入法验收" || items[0].ID != "agent-session:codex:cx-1" {
		t.Fatalf("conversation completion = %+v", items)
	}
	commands := agentItems(sessions, nil, "/测")
	if len(commands) != 1 || commands[0].UtilityValue != "agent-command:test" {
		t.Fatalf("command completion = %+v", commands)
	}

	i18n.SetLanguage(i18n.English)
	enCmds := agentItems(sessions, nil, "/test")
	if len(enCmds) != 1 || enCmds[0].UtilityValue != "agent-command:test" || enCmds[0].Title != "Run tests & report evidence" {
		t.Fatalf("English command completion = %+v", enCmds)
	}
}

func TestAgentTabRendersCachedSessionAndDiagnostic(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.Chinese)

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.agentMu.Lock()
	ctrl.agentRefreshStarted = true
	ctrl.agentSessions = []agentsession.Session{{ID: "dsh-1", Provider: "dsh", Title: "DSH 会话", Status: agentsession.StatusWaiting, LastActivityAt: time.Now(), PendingReview: true}}
	ctrl.agentDiagnostics = []agentsession.Diagnostic{{Provider: "claude", Code: "unavailable", Message: "projects directory was not found"}}
	ctrl.agentMu.Unlock()
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAgent})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("DSH 会话") || !tt.HasText("DSH") || !tt.HasText("待验收") || !tt.HasText("诊断") {
		t.Fatalf("agent tab did not render cached session and diagnostic: %v", tt.Texts())
	}
	if rect, ok := tt.Find("DSH"); !ok || rect.W <= 0 || rect.H <= 0 {
		t.Fatalf("provider badge has no rendered bounds: %v %v", rect, ok)
	} else {
		t.Logf("Agent provider badge DSH bounds: x=%.1f y=%.1f w=%.1f h=%.1f", rect.X, rect.Y, rect.W, rect.H)
	}
}

func TestAgentTabShowsLoadingStateDuringRefresh(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.agentMu.Lock()
	ctrl.agentRefreshStarted = true
	ctrl.agentMu.Unlock()
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAgent})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("正在加载 Agent 会话…") {
		t.Fatalf("loading state missing: %v", tt.Texts())
	}
}

func TestAgentStatusMessageRendersFailureStateWithTester(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	result := host.OperationResult{State: host.StateFailed, Message: "Codex 数据源不可用", Retryable: true}
	setAgentOperationResult(ctrl, result)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Text(c, agentStatusMessage(result)).Key("agent-refresh-status")
	}, 260, 40)
	tt.Frame()
	if !tt.HasText("Codex 数据源不可用") {
		t.Fatalf("status text missing: %v", tt.Texts())
	}
}

func TestAgentOperationResultStatesAndUnknownDoesNotRetry(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.Chinese)

	for _, tc := range []struct {
		name        string
		sessions    int
		diagnostics int
		hadCache    bool
		want        host.OperationState
	}{
		{"ready", 1, 0, false, host.StateReady},
		{"degraded", 1, 1, true, host.StateDegraded},
		{"failed", 0, 1, false, host.StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := make([]agentsession.Session, tc.sessions)
			diagnostics := make([]agentsession.Diagnostic, tc.diagnostics)
			got := agentOperationResult(sessions, diagnostics, tc.hadCache)
			if got.State != tc.want {
				t.Fatalf("state=%s want %s", got.State, tc.want)
			}
		})
	}
	unknown := host.OperationResult{State: host.StateUnknown, Retryable: false}
	if unknown.Retryable || agentStatusMessage(unknown) != "状态未知" {
		t.Fatalf("unknown result=%+v", unknown)
	}
}

type fakeAgentFocuser struct {
	available      bool
	result         host.OperationResult
	mu             sync.Mutex
	availableCalls int
	focusCalls     int
	focusedID      string
}

func (f *fakeAgentFocuser) Name() string { return "fake-herdr" }
func (f *fakeAgentFocuser) Available(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.availableCalls++
	return f.available, nil
}
func (f *fakeAgentFocuser) Focus(_ context.Context, sessionID string) (host.OperationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focusCalls++
	f.focusedID = sessionID
	return f.result, nil
}

func renderAgentToast(t *testing.T, message string) *ui.Tester {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) { ui.Text(c, message).Key("agent-focus-toast") }, 420, 48)
	tester.Frame()
	return tester
}

type fakeTerminalDetector map[string]bool

func (d fakeTerminalDetector) Exists(p config.TerminalProfile) bool { return d[p.Name] }

type fakeTerminalRunner struct {
	mu      sync.Mutex
	calls   int
	program string
	args    []string
}

func (r *fakeTerminalRunner) Run(program string, args ...string) error {
	r.mu.Lock()
	r.calls++
	r.program = program
	r.args = append([]string(nil), args...)
	r.mu.Unlock()
	return nil
}

func TestAgentFocusUnavailableCopiesWithHonestToast(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.Chinese)

	ctrl, _, _, _ := newTestController(t, nil)
	var mu sync.Mutex
	var copied, toast string
	ctrl.mu.Lock()
	ctrl.ClipboardWriter = func(value string) {
		mu.Lock()
		copied = value
		mu.Unlock()
	}
	ctrl.OnShowIMEPill = func(message string, _ *host.CursorAnchor) {
		mu.Lock()
		toast = message
		mu.Unlock()
	}
	ctrl.mu.Unlock()
	restore := setAgentFocuserForTest(&fakeAgentFocuser{available: false})
	defer restore()
	termRunner := &fakeTerminalRunner{}
	restoreTerm := setAgentTerminalResolverForTest(func(cfg config.TerminalConfig) *terminal.Resolver {
		return terminal.NewResolverWith(cfg, fakeTerminalDetector{"ghostty": false, "terminal": true}, termRunner)
	})
	defer restoreTerm()
	if !ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-session:codex:sess-1"}) {
		t.Fatal("session item was not handled")
	}
	// The action needs a matching in-memory session before it can focus/copy.
	ctrl.agentMu.Lock()
	ctrl.agentSessions = []agentsession.Session{{ID: "sess-1", Provider: "codex"}}
	ctrl.agentMu.Unlock()
	ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-session:codex:sess-1"})
	waitUntil(t, time.Second, func() bool {
		mu.Lock()
		gotToast := strings.Contains(toast, "无法直接跳转")
		mu.Unlock()
		termRunner.mu.Lock()
		called := termRunner.calls > 0
		termRunner.mu.Unlock()
		return gotToast && called
	}, "honest unavailable focus toast and terminal resolver fallback")
	mu.Lock()
	gotCopied, gotToast := copied, toast
	mu.Unlock()
	if gotCopied != "sess-1" {
		t.Fatalf("copied=%q", gotCopied)
	}
	if !renderAgentToast(t, gotToast).HasText("无法直接跳转") {
		t.Fatalf("toast=%q", gotToast)
	}
	termRunner.mu.Lock()
	gotProg, gotArgs := termRunner.program, append([]string(nil), termRunner.args...)
	termRunner.mu.Unlock()
	if gotProg != "open" || len(gotArgs) < 2 || gotArgs[0] != "-a" || gotArgs[1] != "Terminal" {
		t.Fatalf("fallback did not use resolved Apple Terminal: program=%q args=%v", gotProg, gotArgs)
	}
}

func TestAgentFocusSuccessAndUnknownUseThreeHonestBranches(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.Chinese)

	for _, tc := range []struct {
		name   string
		result host.OperationResult
		want   string
	}{
		{name: "success", result: host.OperationResult{State: host.StateReady}, want: "已跳转到该会话"},
		{name: "unknown", result: host.OperationResult{State: host.StateUnknown, Retryable: false}, want: "未找到该会话的面板"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl, _, _, _ := newTestController(t, nil)
			var mu sync.Mutex
			var toast string
			ctrl.mu.Lock()
			ctrl.OnShowIMEPill = func(message string, _ *host.CursorAnchor) {
				mu.Lock()
				toast = message
				mu.Unlock()
			}
			ctrl.mu.Unlock()
			fake := &fakeAgentFocuser{available: true, result: tc.result}
			restore := setAgentFocuserForTest(fake)
			defer restore()
			ctrl.agentMu.Lock()
			ctrl.agentSessions = []agentsession.Session{{ID: "sess-2", Provider: "codex"}}
			ctrl.agentMu.Unlock()
			ctrl.ClipboardWriter = func(string) {}
			ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-session:codex:sess-2"})
			waitUntil(t, time.Second, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return strings.Contains(toast, tc.want)
			}, "focus branch toast")
			mu.Lock()
			gotToast := toast
			mu.Unlock()
			if !renderAgentToast(t, gotToast).HasText(tc.want) {
				t.Fatalf("toast=%q", gotToast)
			}
			fake.mu.Lock()
			if fake.focusCalls != 1 || fake.focusedID != "sess-2" {
				t.Fatalf("focusCalls=%d focusedID=%q", fake.focusCalls, fake.focusedID)
			}
			fake.mu.Unlock()
		})
	}
}

func TestAgentDiagnosticTextIsRedactedBeforeUI(t *testing.T) {
	items := agentItems(nil, []agentsession.Diagnostic{{Provider: "codex", Code: "read_failed", Message: "token=sk-abcdefghijklmnopqrstuvwxyz /Users/alice/private"}}, "")
	if len(items) < 1 || strings.Contains(items[len(items)-1].Subtitle, "sk-abcdefghijklmnopqrstuvwxyz") || strings.Contains(items[len(items)-1].Subtitle, "/Users/alice") {
		t.Fatalf("diagnostic leaked secret/path: %+v", items)
	}
}

func TestAgentSwitchTabTriggersRefreshAndRendersPreview(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	// Let NewAppController's startup refresh settle first so we prove
	// EventSwitchTab itself triggers a fresh refresh.
	waitUntil(t, 3*time.Second, func() bool {
		res := ctrl.agentRecordedOperationResult()
		return !ctrl.agentRefreshing() && res.State != "" && res.State != host.StateRefreshing
	}, "startup agent refresh should settle")
	initialObserved := ctrl.agentRecordedOperationResult().ObservedAt

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAgent})
	waitUntil(t, 3*time.Second, func() bool {
		res := ctrl.agentRecordedOperationResult()
		return !ctrl.agentRefreshing() && res.ObservedAt.After(initialObserved)
	}, "switching to Agent tab should trigger and finish a fresh session refresh")

	ctrl.agentMu.Lock()
	ctrl.agentSessions = []agentsession.Session{{
		ID:             "cx-preview-1",
		Provider:       "codex",
		Title:          "Frontend Polish",
		Status:         agentsession.StatusRunning,
		LastActivityAt: time.Now(),
		Workspace:      "/workspace/clavis",
		LastMessage:    "Preview pane renders session details",
		PendingReview:  true,
	}}
	ctrl.agentDiagnostics = nil
	setAgentOperationResult(ctrl, host.OperationResult{State: host.StateReady, ObservedAt: time.Now()})
	ctrl.agentMu.Unlock()

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("Preview pane renders session details") {
		t.Fatalf("agent preview body missing last message: %v", tt.Texts())
	}
	if !tt.HasText("ID: cx-preview-1") {
		t.Fatalf("agent preview body missing session ID: %v", tt.Texts())
	}
	if !tt.HasText("R") {
		t.Fatalf("agent footer missing refresh shortcut hint: %v", tt.Texts())
	}
}

func TestAgentVisibleItemsIsolatesControllerResults(t *testing.T) {
	ctrlA := &AppController{}
	ctrlB := &AppController{}
	setAgentOperationResult(ctrlA, host.OperationResult{State: host.StateReady, ObservedAt: time.Now()})
	setAgentOperationResult(ctrlB, host.OperationResult{State: host.StateFailed, Message: "Codex 数据源不可用", Retryable: true})
	itemsA := ctrlA.agentVisibleItems("")
	if len(itemsA) != 0 {
		t.Fatalf("ctrlA leaked ctrlB failure result: %+v", itemsA)
	}
	itemsB := ctrlB.agentVisibleItems("")
	if len(itemsB) != 1 || itemsB[0].UtilityValue != "agent-refresh-status" {
		t.Fatalf("ctrlB missing its own failure status row: %+v", itemsB)
	}
}

func TestRefreshAgentSessionsUsesCodexSQLiteReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_5.sqlite")
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatalf("open temp codex db: %v", err)
	}
	_, err = db.Execute(context.Background(), `CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		title TEXT,
		cwd TEXT,
		updated_at TEXT,
		status TEXT,
		source TEXT,
		last_message TEXT
	)`)
	if err != nil {
		t.Fatalf("create threads: %v", err)
	}
	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Execute(context.Background(),
		`INSERT INTO threads VALUES (?,?,?,?,?,?,?)`,
		"cx-sql-1", "SQLite Wired", "/tmp/clavis-repo", nowStr, "completed", "cli-wrong-source", "all green",
	)
	if err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	_ = db.Close()

	restoreDB := setAgentCodexDBPathForTest(path)
	defer restoreDB()

	ctrl, _, _, _ := newTestController(t, nil)
	waitUntil(t, 3*time.Second, func() bool {
		res := ctrl.agentRecordedOperationResult()
		return !ctrl.agentRefreshing() && res.State != "" && res.State != host.StateRefreshing
	}, "initial agent refresh settled")

	ctrl.refreshAgentSessions()
	sessions, _ := ctrl.cachedAgentData()
	var found *agentsession.Session
	for i := range sessions {
		if sessions[i].Provider == "codex" && sessions[i].ID == "cx-sql-1" {
			found = &sessions[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("codex session cx-sql-1 not loaded via CodexSQLiteReader: %+v", sessions)
	}
	if found.Title != "SQLite Wired" || found.LastMessage != "all green" {
		t.Fatalf("unexpected codex session fields: %+v", *found)
	}
	// Completed status must map to StatusStopped (not mis-read from source column).
	if found.Status != agentsession.StatusStopped {
		t.Fatalf("codex status = %q, want %q", found.Status, agentsession.StatusStopped)
	}
}

func TestAgentFallbackUsesConfiguredTerminalProfileAndWorkspace(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Terminal = config.TerminalConfig{
			Default: "wezterm",
			Profiles: []config.TerminalProfile{{
				Name:        "wezterm",
				Command:     "/usr/local/bin/wezterm",
				ArgTemplate: "start --cwd {dir}",
			}},
		}
	}); err != nil {
		t.Fatalf("update terminal config: %v", err)
	}

	runner := &fakeTerminalRunner{}
	var capturedCfg config.TerminalConfig
	restoreTerm := setAgentTerminalResolverForTest(func(cfg config.TerminalConfig) *terminal.Resolver {
		capturedCfg = cfg
		return terminal.NewResolverWith(cfg, fakeTerminalDetector{"wezterm": true}, runner)
	})
	defer restoreTerm()

	ctrl.openTerminalForAgentFallback(agentsession.Session{
		ID:        "sess-term",
		Provider:  "codex",
		Workspace: "/tmp/my-workspace",
	})
	if capturedCfg.Default != "wezterm" {
		t.Fatalf("resolver did not receive configured Main.Terminal: %+v", capturedCfg)
	}
	if runner.calls != 1 || runner.program != "/usr/local/bin/wezterm" || !slices.Equal(runner.args, []string{"start", "--cwd", "/tmp/my-workspace"}) {
		t.Fatalf("unexpected resolved command: program=%q args=%v calls=%d", runner.program, runner.args, runner.calls)
	}
}

func TestAgentSessionsEnglishLocalization(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	assertNoHan := func(label, s string) {
		t.Helper()
		for _, r := range s {
			if unicode.Is(unicode.Han, r) {
				t.Fatalf("%s leaked Chinese rune in English mode: %q", label, s)
			}
		}
	}

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	for _, msg := range []string{
		agentStatusMessage(host.OperationResult{State: host.StateRefreshing}),
		agentStatusMessage(host.OperationResult{State: host.StateDegraded}),
		agentStatusMessage(host.OperationResult{State: host.StateDegraded, Message: "Codex source unavailable"}),
		agentStatusMessage(host.OperationResult{State: host.StateFailed}),
		agentStatusMessage(host.OperationResult{State: host.StateUnknown}),
		agentStatusSubtitle(host.OperationResult{State: host.StateFailed, Retryable: true}),
		agentStatusSubtitle(host.OperationResult{State: host.StateUnknown}),
		agentDiagnosticSummary(nil),
		agentDiagnosticSummary([]agentsession.Diagnostic{{Provider: "codex"}}),
		agentRelativeTime(time.Time{}, now),
		agentRelativeTime(now.Add(-10*time.Second), now),
		agentRelativeTime(now.Add(-5*time.Minute), now),
		agentRelativeTime(now.Add(-2*time.Hour), now),
		agentRelativeTime(now.Add(-3*24*time.Hour), now),
		agentStatusLabel(agentsession.StatusRunning),
		agentStatusLabel(agentsession.StatusStopped),
		agentStatusLabel(agentsession.StatusWaiting),
		agentStatusLabel(agentsession.Status("")),
	} {
		assertNoHan("helper", msg)
	}

	items := agentItemsForResult(
		[]agentsession.Session{{ID: "s1", Provider: "codex", Title: "Refactor", Status: agentsession.StatusRunning, LastActivityAt: now.Add(-2 * time.Minute), PendingReview: true}},
		[]agentsession.Diagnostic{{Provider: "claude", Code: "missing", Message: "not found"}},
		"",
		host.OperationResult{State: host.StateDegraded, Message: "Claude source unavailable", Retryable: true},
	)
	for _, it := range items {
		assertNoHan("item.Title", it.Title)
		assertNoHan("item.Subtitle", it.Subtitle)
	}
	for _, cmd := range agentItems(nil, nil, "/") {
		assertNoHan("cmd.Title", cmd.Title)
		assertNoHan("cmd.Subtitle", cmd.Subtitle)
	}

	ctrl, _, _, _ := newTestController(t, nil)
	var mu sync.Mutex
	var toasts []string
	ctrl.mu.Lock()
	ctrl.ClipboardWriter = func(string) {}
	ctrl.OnShowIMEPill = func(message string, _ *host.CursorAnchor) {
		mu.Lock()
		toasts = append(toasts, message)
		mu.Unlock()
	}
	ctrl.mu.Unlock()

	ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-command:test"})
	mu.Lock()
	gotCmdToast := len(toasts) > 0 && strings.Contains(toasts[0], "Copied draft command")
	mu.Unlock()
	if !gotCmdToast {
		t.Fatalf("expected English command copy toast, got %v", toasts)
	}

	restore := setAgentFocuserForTest(&fakeAgentFocuser{available: true, result: host.OperationResult{State: host.StateReady}})
	defer restore()
	ctrl.agentMu.Lock()
	ctrl.agentSessions = []agentsession.Session{{ID: "en-1", Provider: "codex"}}
	ctrl.agentMu.Unlock()
	ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-session:codex:en-1"})
	waitUntil(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range toasts {
			if m == "Focused agent session" {
				return true
			}
		}
		return false
	}, "English focus success toast")
}
