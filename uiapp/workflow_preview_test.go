package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestWorkflowPreviewIsSafeAndNonSensitive(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "release", Name: "Release Notes", Description: "Prepare release notes", Enabled: true,
		Steps: []config.WorkflowStep{{ID: "notes", Name: "Prepare notes", Action: "safe-action", Command: "secret-command", Input: "{{selection}}", Enabled: true}},
	}}
	f.ctrl.StateMachine.SetConfig(cfg)
	f.ctrl.OpenOmnibox(config.TabActions, "release")
	tt := f.omniboxTester(t)
	if !tt.HasText("Release Notes") || !tt.HasText("Prepare release notes") || !tt.HasText("Prepare notes") {
		t.Fatalf("workflow preview text missing: %v", tt.Texts())
	}
	if tt.HasText("secret-command") {
		t.Fatalf("workflow preview exposed a command: %v", tt.Texts())
	}
	if item, ok := selectedOmniboxItem(f.ctrl.State()); !ok || item.Kind != engine.ItemWorkflow {
		t.Fatalf("selected item=%+v ok=%v", item, ok)
	}
}

var _ = ui.KeyEnter
