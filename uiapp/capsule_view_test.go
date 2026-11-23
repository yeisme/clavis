package uiapp

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
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
