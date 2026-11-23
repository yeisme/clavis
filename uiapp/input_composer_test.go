package uiapp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestComposerTriggerDetectionAndPathSafety(t *testing.T) {
	if kind, _, _ := detectComposerTrigger("plain text"); kind != composerMenuNone {
		t.Fatalf("plain text triggered menu: %v", kind)
	}
	if kind, start, needle := detectComposerTrigger("check @clip"); kind != composerMenuMention || start != 6 || needle != "clip" {
		t.Fatalf("@mention = (%v, %d, %q)", kind, start, needle)
	}
	if kind, start, needle := detectComposerTrigger("/con"); kind != composerMenuSlash || start != 0 || needle != "con" {
		t.Fatalf("/slash = (%v, %d, %q)", kind, start, needle)
	}
	if kind, _, _ := detectComposerTrigger("check https://example.com/foo"); kind != composerMenuNone {
		t.Fatalf("URL slash must not trigger command menu: %v", kind)
	}
}

func TestComposerMentionAttachesChipAndSlashExpandsCommand(t *testing.T) {
	f := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("Initial response"), nil
	}})
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox, Tab: config.TabActions,
		Context: &engine.CapsuleContext{
			AppName:          "Visual Studio Code",
			SelectedText:     "func hello() {}",
			ClipboardPreview: "copied snippet",
		},
	})
	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?explain")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "initial completion")
	tt.Frame()

	// Focus composer, type @sel -> mention menu opens; press Tab to attach chip.
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Type("@sel")
	tt.Frame()
	if _, ok := tt.Find("composer-mention-menu"); !ok {
		t.Fatalf("@ mention menu did not open: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	chips := f.ctrl.composerChips()
	if len(chips) != 1 || chips[0].Kind != "selection" {
		t.Fatalf("expected selection chip attached, got %+v", chips)
	}
	if _, ok := tt.Find("composer-chips-row"); !ok {
		t.Fatalf("chips row not rendered: %v", tt.Texts())
	}

	// Type /con -> slash menu opens; press Tab to expand command into composer.
	tt.Type("/con")
	tt.Frame()
	if _, ok := tt.Find("composer-slash-menu"); !ok {
		t.Fatalf("/ slash menu did not open: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !strings.Contains(f.ctrl.uiState.followUpQuery, "Rewrite concisely") {
		t.Fatalf("slash command did not expand: %q", f.ctrl.uiState.followUpQuery)
	}
}

func TestComposerIMEPreeditWinsOverMentionAndHistory(t *testing.T) {
	f := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("Answer"), nil
	}})
	f.ctrl.recordComposerHistory("previous question")
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox, Tab: config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes", SelectedText: "alpha"},
	})
	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?hello")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "completion")
	tt.Frame()

	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Compose("zhongwen", 8)
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyEnter)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	if f.ctrl.State().Phase != engine.PhaseAIStreaming {
		t.Fatal("Escape during IME composition closed capsule")
	}
	if f.ctrl.uiState.followUpQuery == "previous question" {
		t.Fatal("Up during IME composition hijacked into input history")
	}
	if len(f.ctrl.State().AIHistory) != 0 {
		t.Fatal("Enter during IME composition fired follow-up")
	}

	// End composition -> Up recalls input history cleanly.
	tt.Compose("", 0)
	tt.Frame()
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if f.ctrl.uiState.followUpQuery != "previous question" {
		t.Fatalf("Up after composition did not recall history: %q", f.ctrl.uiState.followUpQuery)
	}
}
