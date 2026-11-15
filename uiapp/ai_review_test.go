package uiapp

import (
	"net/http"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// runRewrite drives a freeform AI ask over a selection to completion with a
// canned answer and returns a Tester on the finished AI view.
func runRewrite(t *testing.T, selection, answer string) (*interactionFixture, *ui.Tester) {
	t.Helper()
	f := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse(answer), nil
	}})
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox, Tab: config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes", SelectedText: selection},
	})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?fix the grammar")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI completion")
	if got := f.ctrl.State().AISourceText; got != selection {
		t.Fatalf("AISourceText = %q, want the selection", got)
	}
	tt.Frame()
	return f, tt
}

// TestAIReviewOffersChangesForEdits: a grammar fix shows Result | Changes
// and opens on the changes; ⌘D switches back to the plain result.
func TestAIReviewOffersChangesForEdits(t *testing.T) {
	f, tt := runRewrite(t, "I has a apple and its very tasty", "I have an apple and it's very tasty")
	if !tt.HasText("Result") || !tt.HasText("Changes") {
		t.Fatalf("review toggle missing: %v", tt.Texts())
	}
	d, ok := f.ctrl.aiReview(f.ctrl.State())
	if !ok || !f.ctrl.aiReviewShowsChanges(d) {
		t.Fatalf("a close edit should open on the changes view (similarity %.2f)", d.Similarity)
	}
	if d.Deleted == 0 || d.Inserted == 0 {
		t.Fatalf("diff counts = -%d +%d", d.Deleted, d.Inserted)
	}

	tt.Key(primaryMod(), ui.KeyD)
	tt.Frame()
	if f.ctrl.aiReviewShowsChanges(d) {
		t.Fatal("⌘D should switch to the result view")
	}
	tt.Key(primaryMod(), ui.KeyD)
	tt.Frame()
	if !f.ctrl.aiReviewShowsChanges(d) {
		t.Fatal("⌘D again should switch back to the changes")
	}
}

// TestAIReviewHiddenForTranslations: a translation shares nothing with its
// source, so there are no changes worth showing.
func TestAIReviewHiddenForTranslations(t *testing.T) {
	f, tt := runRewrite(t, "这段代码的作用是读取配置文件。", "This code reads the configuration file.")
	if tt.HasText("Changes") {
		t.Fatalf("a translation should not offer the changes view: %v", tt.Texts())
	}
	if _, ok := f.ctrl.aiReview(f.ctrl.State()); ok {
		t.Fatal("aiReview reported a reviewable diff for a translation")
	}
}

func TestAIReviewChoiceResetsForNewResult(t *testing.T) {
	ctrl := &AppController{}
	st := engine.State{AISourceText: "colour of the sky", AIOutput: "color of the sky", AIComplete: true}
	d, ok := ctrl.aiReview(st)
	if !ok {
		t.Fatal("expected a review")
	}
	ctrl.toggleAIReview(d) // user prefers the plain result
	if ctrl.aiReviewShowsChanges(d) {
		t.Fatal("toggle had no effect")
	}
	st.AIOutput = "colour of the summer sky"
	d2, _ := ctrl.aiReview(st)
	if !ctrl.aiReviewShowsChanges(d2) {
		t.Fatal("a new result should start from the automatic choice again")
	}
	if _, ok := ctrl.aiReview(engine.State{AISourceText: "x", AIOutput: "x", AIComplete: false}); ok {
		t.Fatal("no review while the answer is still streaming")
	}
}
