package engine_test

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestPromptTemplateOmniboxRoutingScopingAndActions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Prompts.Items = append(cfg.Prompts.Items, config.PromptTemplate{
		ID:           "xcode-swift",
		Trigger:      "/sw",
		Title:        "Swift Review",
		Category:     "coding",
		Description:  "Xcode only prompt",
		Template:     "Review {{code}} in {{lang:Swift|ObjC}}",
		AppBundleIDs: []string{"com.apple.dt.Xcode"},
	}, config.PromptTemplate{
		ID:          "create-report",
		Trigger:     "/rep",
		Title:       "Create Report",
		Category:    "writing",
		Description: "Create status report",
		Template:    "Report {{topic}}",
	})

	sm := engine.NewStateMachine(cfg)

	// 1. In TextEdit, "/" surfaces global prompts but hides Xcode-scoped "/sw".
	textEditCtx := engine.CapsuleContext{
		BundleID: "com.apple.TextEdit",
		AppName:  "TextEdit",
	}
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "/", Context: &textEditCtx})
	st := sm.State()
	if len(st.OmniboxItems) == 0 {
		t.Fatal("expected prompt templates for '/' in Actions tab")
	}
	for _, it := range st.OmniboxItems {
		if it.Kind != engine.ItemPromptTemplate {
			t.Fatalf("expected only ItemPromptTemplate under '/', got %s (%s)", it.Kind, it.Title)
		}
		if it.PromptTemplate != nil && it.PromptTemplate.ID == "xcode-swift" {
			t.Fatalf("xcode-swift should be filtered out for TextEdit")
		}
	}

	// 2. In Xcode, "/sw" surfaces the Xcode-scoped prompt.
	xcodeCtx := engine.CapsuleContext{
		BundleID: "com.apple.dt.Xcode",
		AppName:  "Xcode",
	}
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "/sw", Context: &xcodeCtx})
	st = sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].PromptTemplate == nil || st.OmniboxItems[0].PromptTemplate.ID != "xcode-swift" {
		t.Fatalf("expected xcode-swift first for '/sw' in Xcode, got %+v", st.OmniboxItems)
	}

	// 3. Typing "/cr" ranks trigger "/cr" (Code Review) above title-prefix "Create Report" (/rep).
	_ = sm.Handle(engine.Event{Type: engine.EventUpdateQuery, Query: "/cr"})
	st = sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].PromptTemplate == nil || st.OmniboxItems[0].PromptTemplate.ID != "code-review" {
		t.Fatalf("expected code-review (/cr) ranked first for '/cr', got %+v", st.OmniboxItems)
	}

	// 4. Primary action emits EffectOpenPromptComposer + EffectClose (not EffectStartAI).
	eff := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(eff) != 2 || eff[0].Type != engine.EffectOpenPromptComposer || eff[1].Type != engine.EffectClose {
		t.Fatalf("expected [EffectOpenPromptComposer, EffectClose], got %+v", eff)
	}
	if eff[0].PromptTemplate == nil || eff[0].PromptTemplate.ID != "code-review" {
		t.Fatalf("unexpected prompt template in effect: %+v", eff[0].PromptTemplate)
	}
	if sm.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected PhaseHidden after opening prompt composer, got %s", sm.State().Phase)
	}

	// 5. Secondary action copies raw template text.
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "/cr", Context: &textEditCtx})
	secEff := sm.Handle(engine.Event{Type: engine.EventConfirmSecondarySelection})
	if len(secEff) != 2 || secEff[0].Type != engine.EffectCopyText || secEff[1].Type != engine.EffectClose {
		t.Fatalf("expected [EffectCopyText, EffectClose] for secondary action, got %+v", secEff)
	}
	if secEff[0].Text == "" {
		t.Fatal("expected non-empty template text copied")
	}
}

func TestInlineOptimizePrefixFlowInEngine(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Prompts.DefaultOptimizerPreset = "concise"
	sm := engine.NewStateMachine(cfg)

	ctx := engine.CapsuleContext{
		AppName:      "Cursor",
		SelectedText: "func foo() {}",
	}
	_ = sm.Handle(engine.Event{
		Type:    engine.EventOpenOmnibox,
		Tab:     config.TabActions,
		Query:   "//write unit tests for retry backoff",
		Context: &ctx,
	})
	st := sm.State()
	if len(st.OmniboxItems) != 5 {
		t.Fatalf("expected 5 optimizer preset items for '//draft', got %d: %+v", len(st.OmniboxItems), st.OmniboxItems)
	}
	first := st.OmniboxItems[0]
	if first.Kind != engine.ItemAIMode || first.AIMode != "optimize" || first.OptimizerPreset != "concise" {
		t.Fatalf("expected default preset 'concise' first, got %+v", first)
	}
	if first.AIPrompt != "write unit tests for retry backoff" {
		t.Fatalf("expected trimmed draft, got %q", first.AIPrompt)
	}

	eff := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(eff) != 1 || eff[0].Type != engine.EffectStartAI {
		t.Fatalf("expected EffectStartAI, got %+v", eff)
	}
	if eff[0].Mode != "optimize" || eff[0].OptimizerPreset != "concise" || eff[0].Prompt != "write unit tests for retry backoff" {
		t.Fatalf("unexpected optimize EffectStartAI: %+v", eff[0])
	}
	if sm.State().Phase != engine.PhaseAIStreaming || sm.State().AIOptimizerPreset != "concise" {
		t.Fatalf("expected PhaseAIStreaming with AIOptimizerPreset=concise, got %+v", sm.State())
	}

	// Follow-up refinement keeps OptimizerPreset.
	_ = sm.Handle(engine.Event{Type: engine.EventAIChunk, Chunk: "Upgraded prompt v1"})
	_ = sm.Handle(engine.Event{Type: engine.EventAICompleted})
	fuEff := sm.Handle(engine.Event{Type: engine.EventFollowUpAI, Prompt: "make it shorter"})
	if len(fuEff) != 2 || fuEff[1].Type != engine.EffectStartAI || fuEff[1].OptimizerPreset != "concise" {
		t.Fatalf("follow-up should preserve OptimizerPreset=concise, got %+v", fuEff)
	}

	// Custom InlineOptimizePrefix works too.
	cfg.Prompts.InlineOptimizePrefix = "!!"
	sm.SetConfig(cfg)
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "!!draft body", Context: &ctx})
	st = sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].AIMode != "optimize" || st.OmniboxItems[0].AIPrompt != "draft body" {
		t.Fatalf("custom InlineOptimizePrefix failed: %+v", st.OmniboxItems)
	}

	// Empty "//" without SelectedText is a safe no-op on confirm.
	emptyCtx := engine.CapsuleContext{AppName: "Notes"}
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "!!", Context: &emptyCtx})
	noOpEff := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(noOpEff) != 0 {
		t.Fatalf("expected empty draft confirm with no selection to be no-op, got %+v", noOpEff)
	}
}
