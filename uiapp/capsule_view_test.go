package uiapp

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestOmniboxFooterTogglesTrackOpenState(t *testing.T) {
	ctrl := NewAppController(Options{})
	defer ctrl.Close()
	cfg := ctrl.Config()
	capsuleUI.reset(ctrl)
	if !ctrl.previewPaneVisible(cfg) {
		t.Fatal("preview pane should start from the appearance default")
	}
	if got := capsuleUI.togglePreview(ctrl, cfg.Main.Appearance.ShowPreviewPane); got {
		t.Fatal("preview toggle should close an initially open pane")
	}
	if ctrl.previewPaneVisible(cfg) {
		t.Fatal("preview pane remained open after toggle")
	}
	if ctrl.omniboxActionMenuOpen(engine.State{OmniboxItems: []engine.OmniboxItem{{Title: "one"}}}) {
		t.Fatal("action menu unexpectedly open")
	}
	ctrl.toggleOmniboxActionMenu()
	if !ctrl.omniboxActionMenuOpen(engine.State{OmniboxItems: []engine.OmniboxItem{{Title: "one"}}}) {
		t.Fatal("action menu did not open")
	}
}

func TestCapsuleHeaderRendersAtMinimumWidth(t *testing.T) {
	ctrl := NewAppController(Options{})
	defer ctrl.Close()
	cfg := ctrl.Config()
	phases := []engine.CapsulePhase{engine.PhaseLeader, engine.PhaseOmnibox, engine.PhaseAIStreaming, engine.PhaseHUD}
	for _, dark := range []bool{false, true} {
		for _, phase := range phases {
			t.Run(map[bool]string{false: "light", true: "dark"}[dark]+"/"+string(phase), func(t *testing.T) {
				state := engine.State{Phase: phase, OmniboxTab: config.TabActions, HUDMessage: "Ready", Context: engine.CapsuleContext{AppName: "TextEdit"}, AIMode: "ask"}
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(map[bool]*ui.Theme{false: ui.LightTheme(), true: ui.DarkTheme()}[dark])
					ctrl.renderCapsuleHeader(c, c.Theme(), cfg, state, DiagnosticsSnapshot{ActiveInputSource: host.InputSourceInfo{ID: "abc", Name: "ABC"}})
				}, 420, int(headerHeight))
				tt.Frame()
				if shades := distinctShades(tt.Image()); shades < 4 {
					t.Fatalf("phase %s dark=%v rendered only %d shades", phase, dark, shades)
				}
			})
		}
	}
}

func TestOmniboxEscapeClearsThenClosesWithRealTester(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	allItems := len(ctrl.State().OmniboxItems)
	w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("window")
	filtered := ctrl.State()
	if filtered.OmniboxQuery != "window" || len(filtered.OmniboxItems) >= allItems {
		t.Fatalf("typing did not filter results: query=%q rows=%d/%d", filtered.OmniboxQuery, len(filtered.OmniboxItems), allItems)
	}
	tt.Key(0, ui.KeyEscape)
	if st := ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxQuery != "" || len(st.OmniboxItems) != allItems {
		t.Fatalf("first Esc did not restore full list: phase=%s query=%q rows=%d/%d", st.Phase, st.OmniboxQuery, len(st.OmniboxItems), allItems)
	}
	if !tt.Focused(i18n.T("omnibox.search_label")) || tt.HasText("window") {
		t.Fatalf("search is not focused and empty after Esc: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if st := ctrl.State(); st.Phase != engine.PhaseHidden {
		t.Fatalf("second Esc did not close panel: phase=%s", st.Phase)
	}
	t.Logf("first Esc: %d → %d rows, empty query, search focused; second Esc: hidden", len(filtered.OmniboxItems), allItems)
}

func TestLeaderEscapeReturnsToParentThenClosesWithRealTester(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader})
	rootCount := len(ctrl.State().LeaderNodes)
	w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Key(0, ui.KeyW)
	if st := ctrl.State(); st.Phase != engine.PhaseLeader || len(st.LeaderPath) != 1 || st.LeaderPath[0] != "w" {
		t.Fatalf("Window submenu not open: phase=%s path=%v", st.Phase, st.LeaderPath)
	}
	tt.Key(0, ui.KeyEscape)
	if st := ctrl.State(); st.Phase != engine.PhaseLeader || len(st.LeaderPath) != 0 || len(st.LeaderNodes) != rootCount {
		t.Fatalf("Esc did not return to Leader root: phase=%s path=%v rows=%d/%d", st.Phase, st.LeaderPath, len(st.LeaderNodes), rootCount)
	}
	tt.Key(0, ui.KeyEscape)
	if st := ctrl.State(); st.Phase != engine.PhaseHidden {
		t.Fatalf("Esc at Leader root did not close: phase=%s", st.Phase)
	}
	t.Log("Esc: Window submenu → Leader root → hidden")
}

func TestOmniboxEscapeLeavesIMEPreeditUntouched(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "window"})
	w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Compose("nihao", 5)
	if !ctrl.uiState.isComposing {
		t.Fatal("search field did not enter IME preedit")
	}
	tt.Key(0, ui.KeyEscape)
	if st := ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxQuery != "window" {
		t.Fatalf("IME preedit Esc changed state: phase=%s query=%q", st.Phase, st.OmniboxQuery)
	}
	tt.Compose("", 0)
	tt.Key(0, ui.KeyEscape)
	if st := ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxQuery != "" {
		t.Fatalf("after preedit ended Esc did not clear query: phase=%s query=%q", st.Phase, st.OmniboxQuery)
	}
	t.Log("Esc during nihao preedit: Omnibox remains open with query window; Esc after composition ends clears query")
}

func TestOmniboxPagingKeysUseVisibleRows(t *testing.T) {
	entries := make([]engine.OmniboxClipboardEntry, 148)
	for i := range entries {
		entries[i] = engine.OmniboxClipboardEntry{ID: fmt.Sprintf("clip-%03d", i), Content: fmt.Sprintf("entry %03d", i)}
	}
	for _, height := range []int{536, 340} {
		t.Run(fmt.Sprintf("height_%d", height), func(t *testing.T) {
			ctrl, _, _, _ := newTestController(t, nil)
			ctrl.Dispatch(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: entries})
			ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
			tt := ui.NewTester(ctrl.CapsuleView, 680, height)
			tt.Frame()
			if got := len(ctrl.State().OmniboxItems); got != 148 {
				t.Fatalf("rows=%d, want 148", got)
			}
			visible := func() int {
				first, last := ctrl.uiState.listState.Visible()
				if last <= first {
					t.Fatalf("expected multiple visible rows, got %d..%d", first, last)
				}
				return last - first + 1
			}
			press := func(name string, mods ui.Modifiers, key ui.Key, want int) {
				t.Helper()
				before := ctrl.State().SelectedIndex
				tt.Key(mods, key)
				if got := ctrl.State().SelectedIndex; got != want {
					t.Fatalf("%s: %d → %d, want %d", name, before, got, want)
				}
				t.Logf("%s: %d → %d (delta %+d)", name, before, want, want-before)
			}
			press("End", 0, ui.KeyEnd, 147)
			press("Home", 0, ui.KeyHome, 0)
			press("Cmd+End", ui.Cmd, ui.KeyEnd, 0)
			press("Ctrl+End", ui.Ctrl, ui.KeyEnd, 0)
			press("PageDown", 0, ui.KeyPageDown, visible())
			press("Ctrl+D", ui.Ctrl, ui.KeyD, ctrl.State().SelectedIndex+visible()/2)
			press("Ctrl+U", ui.Ctrl, ui.KeyU, ctrl.State().SelectedIndex-visible()/2)
			press("PageUp", 0, ui.KeyPageUp, max(0, ctrl.State().SelectedIndex-visible()))
		})
	}
}

func TestOmniboxPagingKeysSelectFilteredClipboardRows(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	entries := []engine.OmniboxClipboardEntry{
		{ID: "text1", Content: "First text"},
		{ID: "file1", Content: "First file", Media: engine.ClipboardMedia{Kind: "file"}},
		{ID: "text2", Content: "Second text"},
		{ID: "file2", Content: "Second file", Media: engine.ClipboardMedia{Kind: "file"}},
		{ID: "text3", Content: "Third text"},
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: entries})
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	ctrl.clipboardStatsMu.Lock()
	ctrl.clipboardStats.filter = "file"
	ctrl.clipboardStatsMu.Unlock()
	tt := ui.NewTester(ctrl.CapsuleView, 680, 536)
	for _, tc := range []struct {
		key  ui.Key
		want string
	}{{ui.KeyEnd, "file2"}, {ui.KeyHome, "file1"}} {
		tt.Key(0, tc.key)
		item, ok := selectedOmniboxItem(ctrl.State())
		if !ok || item.ClipboardID != tc.want {
			t.Fatalf("key %v selected clipboard ID %q, want %q", tc.key, item.ClipboardID, tc.want)
		}
	}
}

func TestAgentEmptyStateUsesEnglishTranslation(t *testing.T) {
	defer i18n.SetLanguage(i18n.Current())
	for _, tc := range []struct {
		language i18n.Language
		want     string
	}{
		{i18n.English, "No agent sessions found"},
		{i18n.Chinese, "没有发现 Agent 会话"},
	} {
		i18n.SetLanguage(tc.language)
		ctrl := &AppController{}
		cfg := config.DefaultConfig()
		state := engine.State{Phase: engine.PhaseOmnibox, OmniboxTab: config.TabAgent, OmniboxQuery: "unmatched"}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(ui.LightTheme())
			ctrl.renderOmniboxView(c, c.Theme(), cfg, state)
		}, 680, 536)
		tt.Frame()
		if !tt.HasText(tc.want) {
			t.Fatalf("missing %q: %v", tc.want, tt.Texts())
		}
		t.Logf("%s: %s", tc.language, tc.want)
	}
}

func TestOmniboxFilteredTabAgentKeyboardNavigationAndActivation(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	var copyMu sync.Mutex
	var copied []string
	ctrl.mu.Lock()
	ctrl.ClipboardWriter = func(s string) {
		copyMu.Lock()
		copied = append(copied, s)
		copyMu.Unlock()
	}
	ctrl.OnShowIMEPill = func(string, *host.CursorAnchor) {}
	ctrl.mu.Unlock()
	restore := setAgentFocuserForTest(&fakeAgentFocuser{available: true, result: host.OperationResult{State: host.StateReady}})
	defer restore()

	now := time.Now()
	sessions := []agentsession.Session{
		{ID: "s-run", Provider: "codex", Title: "Running Task", Status: agentsession.StatusRunning, WorkspaceID: "w1", WorkspaceLabel: "alpha", LastActivityAt: now},
		{ID: "s-app-1", Provider: "claude", Title: "Approval One", Status: agentsession.StatusWaiting, PendingReview: true, WorkspaceID: "w2", WorkspaceLabel: "beta", LastActivityAt: now.Add(-time.Minute)},
		{ID: "s-app-2", Provider: "dsh", Title: "Approval Two", Status: agentsession.StatusWaiting, PendingReview: true, WorkspaceID: "w2", WorkspaceLabel: "beta", LastActivityAt: now.Add(-2 * time.Minute)},
		{ID: "s-done", Provider: "codex", Title: "Done Task", Status: agentsession.StatusStopped, WorkspaceID: "w1", WorkspaceLabel: "alpha", LastActivityAt: now.Add(-3 * time.Minute)},
	}
	settleAgentTabForTest(t, ctrl, sessions, nil, host.OperationResult{State: host.StateReady, ObservedAt: now})

	// Activate Approval lane filter -> only [s-app-1, s-app-2] are visible.
	capsuleUI.setLaneFilter(ctrl, string(agentLaneApproval))
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	lastCopied := func() string {
		copyMu.Lock()
		defer copyMu.Unlock()
		if len(copied) == 0 {
			return ""
		}
		return copied[len(copied)-1]
	}
	copiedLen := func() int {
		copyMu.Lock()
		defer copyMu.Unlock()
		return len(copied)
	}

	// Enter immediately activates first filtered session (s-app-1), NOT unfiltered s-run.
	tt.Key(0, ui.KeyEnter)
	if got := lastCopied(); got != "s-app-1" {
		t.Fatalf("filtered TabAgent Enter activated %q, want s-app-1", got)
	}

	// Down moves to second filtered session (s-app-2); Enter activates s-app-2.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	if got := lastCopied(); got != "s-app-2" {
		t.Fatalf("filtered TabAgent Down+Enter activated %q, want s-app-2", got)
	}

	// Ctrl+N wraps within the 2 filtered rows back to s-app-1; Ctrl+P goes back to s-app-2; Up returns to s-app-1.
	tt.Key(ui.Ctrl, ui.KeyN)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	if got := lastCopied(); got != "s-app-1" {
		t.Fatalf("filtered TabAgent Ctrl+N wrap activated %q, want s-app-1", got)
	}
	tt.Key(ui.Ctrl, ui.KeyP)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	if got := lastCopied(); got != "s-app-2" {
		t.Fatalf("filtered TabAgent Ctrl+P activated %q, want s-app-2", got)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	tt.Key(primaryMod(), ui.KeyEnter)
	if got := lastCopied(); got != "s-app-1" {
		t.Fatalf("filtered TabAgent Up+Cmd+Enter activated %q, want s-app-1", got)
	}

	// Alt+2 activates second visible filtered row (s-app-2), while Alt+3 is out of filtered bounds and must NOT activate s-done.
	tt.Key(ui.Alt, ui.Key2)
	if got := lastCopied(); got != "s-app-2" {
		t.Fatalf("filtered TabAgent Alt+2 activated %q, want s-app-2", got)
	}
	beforeOutOfRange := copiedLen()
	tt.Key(ui.Alt, ui.Key3)
	if copiedLen() != beforeOutOfRange {
		t.Fatalf("filtered TabAgent Alt+3 activated out-of-range hidden item: %v", copied)
	}

	// Clear lane filter and apply workspace filter ("w2") -> visible [s-app-1, s-app-2].
	capsuleUI.setLaneFilter(ctrl, "")
	ctrl.SetAgentWorkspaceFilter("w2")
	tt.Frame()
	tt.Key(ui.Alt, ui.Key1)
	if got := lastCopied(); got != "s-app-1" {
		t.Fatalf("workspace-filtered TabAgent Alt+1 activated %q, want s-app-1", got)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	if got := lastCopied(); got != "s-app-2" {
		t.Fatalf("workspace-filtered TabAgent Down+Enter activated %q, want s-app-2", got)
	}
}

func TestOmniboxFilteredTabClipboardKeyboardNavigationAndActivation(t *testing.T) {
	f := newInteractionFixture(t, nil)
	entries := []engine.OmniboxClipboardEntry{
		{ID: "file1", Content: "First file", Media: engine.ClipboardMedia{Kind: "file"}},
		{ID: "text1", Content: "First text"},
		{ID: "file2", Content: "Second file", Media: engine.ClipboardMedia{Kind: "file"}},
		{ID: "text2", Content: "Second text"},
		{ID: "file3", Content: "Third file", Media: engine.ClipboardMedia{Kind: "file"}},
	}
	f.ctrl.Dispatch(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: entries})
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	f.ctrl.clipboardStatsMu.Lock()
	f.ctrl.clipboardStats.filter = "text"
	f.ctrl.clipboardStatsMu.Unlock()

	tt := f.omniboxTester(t)

	// SelectedIndex starts at 0 (file1, hidden by filter), yet visible row 0 is text1 (raw index 1).
	// Enter must activate text1, NOT hidden file1.
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool { return len(f.copiedTexts()) >= 1 }, "filtered clipboard Enter copies text1")
	if got := f.copiedTexts()[0]; got != "First text" {
		t.Fatalf("filtered Enter copied %q, want %q", got, "First text")
	}

	// Reopen with same filter: Down moves directly to text2 (raw index 3), Ctrl+N wraps to text1 (raw index 1),
	// Ctrl+P moves back to text2 (raw index 3), Up returns to text1, Down returns to text2.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	if got := f.ctrl.State().SelectedIndex; got != 3 {
		t.Fatalf("filtered Down SelectedIndex = %d, want 3 (text2)", got)
	}
	tt.Key(ui.Ctrl, ui.KeyN)
	if got := f.ctrl.State().SelectedIndex; got != 1 {
		t.Fatalf("filtered Ctrl+N wrap SelectedIndex = %d, want 1 (text1)", got)
	}
	tt.Key(ui.Ctrl, ui.KeyP)
	if got := f.ctrl.State().SelectedIndex; got != 3 {
		t.Fatalf("filtered Ctrl+P SelectedIndex = %d, want 3 (text2)", got)
	}
	tt.Key(0, ui.KeyUp)
	if got := f.ctrl.State().SelectedIndex; got != 1 {
		t.Fatalf("filtered Up SelectedIndex = %d, want 1 (text1)", got)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool { return len(f.copiedTexts()) >= 2 }, "filtered clipboard Down+Enter copies text2")
	if got := f.copiedTexts()[1]; got != "Second text" {
		t.Fatalf("filtered text2 Enter copied %q, want %q", got, "Second text")
	}

	// Cmd+Enter (secondary paste) on visible row 0 pastes text1.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	tt.Frame()
	tt.Key(primaryMod(), ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool { return f.bridge.getPasteClipboardCalls() >= 1 }, "filtered Cmd+Enter pastes text1")
	if got := f.copiedTexts()[len(f.copiedTexts())-1]; got != "First text" {
		t.Fatalf("filtered Cmd+Enter copied %q, want %q", got, "First text")
	}

	// Alt+2 copies text2; Alt+Cmd+1 pastes text1; Alt+3 is out of range for the 2 visible filtered items and must NOT activate hidden file2.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	tt.Frame()
	tt.Key(ui.Alt, ui.Key2)
	waitUntil(t, 2*time.Second, func() bool { return len(f.copiedTexts()) >= 4 }, "filtered Alt+2 copies text2")
	if got := f.copiedTexts()[3]; got != "Second text" {
		t.Fatalf("filtered Alt+2 copied %q, want %q", got, "Second text")
	}

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	tt.Frame()
	tt.Key(ui.Alt|primaryMod(), ui.Key1)
	waitUntil(t, 2*time.Second, func() bool { return f.bridge.getPasteClipboardCalls() >= 2 }, "filtered Alt+Cmd+1 pastes text1")
	if got := f.copiedTexts()[len(f.copiedTexts())-1]; got != "First text" {
		t.Fatalf("filtered Alt+Cmd+1 copied %q, want %q", got, "First text")
	}

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	tt.Frame()
	beforeAlt3 := len(f.copiedTexts())
	tt.Key(ui.Alt, ui.Key3)
	tt.Frame()
	if len(f.copiedTexts()) != beforeAlt3 || f.ctrl.State().Phase != engine.PhaseOmnibox {
		t.Fatalf("filtered Alt+3 activated out-of-range hidden entry: copied=%v phase=%s", f.copiedTexts(), f.ctrl.State().Phase)
	}

	// Also verify media-kind filter ("file") steps strictly across file rows [0, 2, 4] and skips hidden text rows [1, 3].
	f.ctrl.clipboardStatsMu.Lock()
	f.ctrl.clipboardStats.filter = "file"
	f.ctrl.clipboardStatsMu.Unlock()
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	if got := f.ctrl.State().SelectedIndex; got != 2 {
		t.Fatalf("file-filtered Down SelectedIndex = %d, want 2 (file2)", got)
	}
	tt.Key(0, ui.KeyDown)
	if got := f.ctrl.State().SelectedIndex; got != 4 {
		t.Fatalf("file-filtered second Down SelectedIndex = %d, want 4 (file3)", got)
	}
	tt.Key(0, ui.KeyUp)
	if got := f.ctrl.State().SelectedIndex; got != 2 {
		t.Fatalf("file-filtered Up SelectedIndex = %d, want 2 (file2)", got)
	}
}

func TestOmniboxWindowShortcutsCmd1To6AndIMEPreeditPriority(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	mods := primaryMod()

	// Open a hub surface (⌘8 Manage) so the search field is unmounted and window-level
	// shortcuts must handle ⌘1..6 cleanly and close the hub surface.
	for _, tc := range []struct {
		hubKey ui.Key
		tabKey ui.Key
		want   config.CapsuleTab
	}{
		{ui.Key8, ui.Key2, config.TabClipboard},
		{ui.Key7, ui.Key3, config.TabSnippets},
		{ui.Key9, ui.Key4, config.TabAI},
		{ui.Key8, ui.Key5, config.TabGlossary},
		{ui.Key7, ui.Key6, config.TabAgent},
		{ui.Key8, ui.Key1, config.TabActions},
	} {
		tt.Key(mods, tc.hubKey)
		tt.Frame()
		if ctrl.ActiveHubSurface() == HubSurfaceNone {
			t.Fatalf("hub shortcut %v did not open hub surface", tc.hubKey)
		}
		tt.Key(mods, tc.tabKey)
		tt.Frame()
		if ctrl.ActiveHubSurface() != HubSurfaceNone {
			t.Fatalf("tab shortcut %v did not close hub surface (still %s)", tc.tabKey, ctrl.ActiveHubSurface())
		}
		if got := ctrl.State().OmniboxTab; got != tc.want {
			t.Fatalf("tab shortcut %v switched to %s, want %s", tc.tabKey, got, tc.want)
		}
	}

	// IME preedit strictly retains priority: ⌘2 / Ctrl+2 and unmodified 2 must NOT switch tabs while composing.
	tt.Compose("nihao", 5)
	tt.Frame()
	if !ctrl.uiState.isComposing {
		t.Fatal("expected search field to enter IME preedit")
	}
	tt.Key(mods, ui.Key2)
	tt.Key(0, ui.Key2)
	tt.Frame()
	if got := ctrl.State().OmniboxTab; got != config.TabActions {
		t.Fatalf("digit/shortcut during IME preedit switched tab to %s, want actions", got)
	}

	// After IME composition ends, ⌘2 switches to Clipboard as expected.
	tt.Compose("", 0)
	tt.Frame()
	tt.Key(mods, ui.Key2)
	tt.Frame()
	if got := ctrl.State().OmniboxTab; got != config.TabClipboard {
		t.Fatalf("after IME preedit ended, %v+2 switched to %s, want clipboard", mods, got)
	}
}

func TestCapsuleViewPhaseHiddenDoesNotTriggerOnRepaintLoop(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	var mu sync.Mutex
	repaints := 0
	ctrl.mu.Lock()
	ctrl.OnRepaint = func() {
		mu.Lock()
		repaints++
		mu.Unlock()
	}
	ctrl.mu.Unlock()

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.ToggleHubSurface(HubSurfaceManage)
	if ctrl.ActiveHubSurface() != HubSurfaceManage {
		t.Fatalf("expected HubSurfaceManage active, got %s", ctrl.ActiveHubSurface())
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
	if ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected PhaseHidden after dismiss, got %s", ctrl.State().Phase)
	}

	mu.Lock()
	repaints = 0
	mu.Unlock()

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Frame()

	if ctrl.ActiveHubSurface() != HubSurfaceNone {
		t.Fatalf("expected PhaseHidden render to clear hub surface silently, still %s", ctrl.ActiveHubSurface())
	}
	mu.Lock()
	gotRepaints := repaints
	mu.Unlock()
	if gotRepaints != 0 {
		t.Fatalf("PhaseHidden CapsuleView triggered %d synchronous OnRepaint calls (expected 0)", gotRepaints)
	}
}
