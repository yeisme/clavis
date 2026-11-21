package uiapp

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// The arbitration table of docs/design/interaction-spec.md §3.1, key by key:
// IME preedit > inline completion > tab navigation.
func TestArbitrateSearchKey_Table(t *testing.T) {
	cmd := primaryMod()
	cases := []struct {
		name       string
		key        ui.Key
		mods       ui.Modifiers
		composing  bool
		completing bool
		want       searchKeyRole
	}{
		{"Tab completes", ui.KeyTab, 0, false, true, searchKeyAccept},
		{"Tab without completion does nothing", ui.KeyTab, 0, false, false, searchKeyInert},
		{"Shift+Tab switches tab", ui.KeyTab, ui.Shift, false, false, searchKeyNextTab},
		{"Shift+Tab leaves a completion alone", ui.KeyTab, ui.Shift, false, true, searchKeyInert},
		{"Cmd+Right next tab", ui.KeyRight, cmd, false, false, searchKeyNextTab},
		{"Cmd+Right next tab with completion", ui.KeyRight, cmd, false, true, searchKeyNextTab},
		{"Cmd+Left previous tab", ui.KeyLeft, cmd, false, true, searchKeyPrevTab},
		{"preedit keeps Tab", ui.KeyTab, 0, true, true, searchKeyIME},
		{"preedit keeps Shift+Tab", ui.KeyTab, ui.Shift, true, false, searchKeyIME},
		{"preedit keeps Cmd+Left", ui.KeyLeft, cmd, true, false, searchKeyIME},
		{"preedit keeps Cmd+Right", ui.KeyRight, cmd, true, false, searchKeyIME},
		{"plain Right is not arbitrated", ui.KeyRight, 0, false, true, searchKeyOther},
		{"Enter is not arbitrated", ui.KeyEnter, 0, false, true, searchKeyOther},
		{"Ctrl+Tab is not arbitrated", ui.KeyTab, ui.Ctrl, false, true, searchKeyOther},
	}
	for _, tc := range cases {
		if got := arbitrateSearchKey(tc.key, tc.mods, tc.composing, tc.completing); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestInlineCompletion(t *testing.T) {
	cases := []struct {
		typed, title, want string
		ok                 bool
	}{
		{"maxi", "Maximize", "maximize", true},
		{"Maxi", "Maximize", "Maximize", true},
		{"截", "截图", "截图", true},
		{"Maximize", "Maximize", "", false},
		{"mize", "Maximize", "", false},
		{"", "Maximize", "", false},
		{"maximizer", "Maximize", "", false},
	}
	for _, tc := range cases {
		got, ok := inlineCompletion(tc.typed, tc.title)
		if got != tc.want || ok != tc.ok {
			t.Errorf("inlineCompletion(%q, %q) = %q, %v; want %q, %v", tc.typed, tc.title, got, ok, tc.want, tc.ok)
		}
	}
}

func newTabArbitrationTester(t *testing.T) (*AppController, *ui.Tester) {
	t.Helper()
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{
		Type:    engine.EventOpenOmnibox,
		Tab:     config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes"},
	})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	return ctrl, tt
}

// typeCompletable types a query whose chosen row completes it.
func typeCompletable(t *testing.T, ctrl *AppController, tt *ui.Tester) string {
	t.Helper()
	tt.Type("maxi")
	tt.Frame()
	full, ok := omniboxCompletion(ctrl.State())
	if !ok {
		st := ctrl.State()
		t.Fatalf("expected a completion for %q, chosen row %d of %d", st.OmniboxQuery, st.SelectedIndex, len(st.OmniboxItems))
	}
	return full
}

func TestTabArbitration_TabAcceptsCompletionAndKeepsTab(t *testing.T) {
	ctrl, tt := newTabArbitrationTester(t)
	full := typeCompletable(t, ctrl, tt)
	if !tt.HasText("Complete") || tt.HasText("Switch tab") {
		t.Fatalf("footer should offer Tab Complete while completing, got %v", tt.Texts())
	}

	tt.Key(0, ui.KeyTab)
	tt.Frame()
	st := ctrl.State()
	if st.OmniboxQuery != full {
		t.Fatalf("Tab should accept the completion %q, query is %q", full, st.OmniboxQuery)
	}
	if st.OmniboxTab != config.TabActions || st.Phase != engine.PhaseOmnibox {
		t.Fatalf("Tab must not switch tabs while completing: phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}
	// The caret ends after the completed word: typing goes on from there.
	tt.Type("!")
	tt.Frame()
	if got := ctrl.State().OmniboxQuery; got != full+"!" {
		t.Fatalf("typing after the completion should append, query is %q (completion %q)", got, full)
	}
}

func TestTabArbitration_TabWithoutCompletionDoesNothing(t *testing.T) {
	ctrl, tt := newTabArbitrationTester(t)
	tt.Type("zzzz-no-such-row")
	tt.Frame()
	if _, ok := omniboxCompletion(ctrl.State()); ok {
		t.Fatal("expected no completion")
	}
	if !tt.HasText("Switch tab") || tt.HasText("Complete") {
		t.Fatalf("footer should offer %s Switch tab without a completion, got %v", shiftTabLabel(), tt.Texts())
	}

	tt.Key(0, ui.KeyTab)
	tt.Frame()
	st := ctrl.State()
	if st.OmniboxTab != config.TabActions || st.OmniboxQuery != "zzzz-no-such-row" {
		t.Fatalf("Tab without a completion must change nothing: tab=%s query=%q", st.OmniboxTab, st.OmniboxQuery)
	}
	// Tab did not move the focus out of the field either: typing still lands.
	tt.Type("x")
	tt.Frame()
	if got := ctrl.State().OmniboxQuery; got != "zzzz-no-such-rowx" {
		t.Fatalf("focus left the search field after Tab, query is %q", got)
	}
}

func TestTabArbitration_ShiftTabSwitchesToNextTab(t *testing.T) {
	ctrl, tt := newTabArbitrationTester(t)
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if got := ctrl.State().OmniboxTab; got != config.TabClipboard {
		t.Fatalf("Shift+Tab should go to the next tab (Clipboard), got %s", got)
	}

	// While a completion shows, Shift+Tab leaves both the text and the tab.
	ctrl2, tt2 := newTabArbitrationTester(t)
	full := typeCompletable(t, ctrl2, tt2)
	tt2.Key(ui.Shift, ui.KeyTab)
	tt2.Frame()
	st := ctrl2.State()
	if st.OmniboxTab != config.TabActions || st.OmniboxQuery == full {
		t.Fatalf("Shift+Tab with a completion must neither switch nor complete: tab=%s query=%q", st.OmniboxTab, st.OmniboxQuery)
	}
}

func TestTabArbitration_CmdArrowsSwitchTabs(t *testing.T) {
	ctrl, tt := newTabArbitrationTester(t)
	// Any query state: with a completion showing too.
	typeCompletable(t, ctrl, tt)

	tt.Key(primaryMod(), ui.KeyRight)
	tt.Frame()
	if got := ctrl.State().OmniboxTab; got != config.TabClipboard {
		t.Fatalf("Cmd+Right should go to the next tab (Clipboard), got %s", got)
	}
	if got := ctrl.State().OmniboxQuery; got != "maxi" {
		t.Fatalf("switching tabs keeps the query, got %q", got)
	}
	tt.Key(primaryMod(), ui.KeyLeft)
	tt.Frame()
	if got := ctrl.State().OmniboxTab; got != config.TabActions {
		t.Fatalf("Cmd+Left should go back to Actions, got %s", got)
	}
	tt.Key(primaryMod(), ui.KeyLeft)
	tt.Frame()
	if got := ctrl.State().OmniboxTab; got != config.TabAgent {
		t.Fatalf("Cmd+Left from the first tab should wrap to the last (Agent), got %s", got)
	}
}

func TestTabArbitration_IMEPreeditKeepsAllFourKeys(t *testing.T) {
	// With a completion showing, and without one: there Shift+Tab would
	// otherwise switch tabs, here Tab would otherwise complete.
	for _, completing := range []bool{true, false} {
		ctrl, tt := newTabArbitrationTester(t)
		full := "\x00"
		if completing {
			full = typeCompletable(t, ctrl, tt)
		} else {
			tt.Type("zzzz-no-such-row")
			tt.Frame()
		}
		assertPreeditKeepsKeys(t, ctrl, tt, full)
	}
}

func assertPreeditKeepsKeys(t *testing.T, ctrl *AppController, tt *ui.Tester, full string) {
	t.Helper()
	for _, k := range []struct {
		mods ui.Modifiers
		key  ui.Key
		name string
	}{
		{0, ui.KeyTab, "Tab"},
		{ui.Shift, ui.KeyTab, "Shift+Tab"},
		{primaryMod(), ui.KeyLeft, "Cmd+Left"},
		{primaryMod(), ui.KeyRight, "Cmd+Right"},
	} {
		// Each key meets a preedit of its own: the tester's editor may end
		// one on a key the panel left it, as Cmd+Left moving the caret.
		tt.Compose("nihao", 5)
		tt.Frame()
		if !ctrl.uiState.isComposing {
			t.Fatalf("expected an IME preedit before %s", k.name)
		}
		tt.Key(k.mods, k.key)
		tt.Frame()
		st := ctrl.State()
		if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabActions {
			t.Fatalf("%s during preedit reached the panel: phase=%s tab=%s", k.name, st.Phase, st.OmniboxTab)
		}
		if st.OmniboxQuery == full {
			t.Fatalf("%s during preedit accepted the completion %q", k.name, full)
		}
		if strings.Contains(st.OmniboxQuery, "\t") {
			t.Fatalf("%s during preedit typed a tab into the query %q", k.name, st.OmniboxQuery)
		}
	}
}
