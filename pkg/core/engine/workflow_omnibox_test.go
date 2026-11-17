package engine

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestWorkflowItemsSearchAndEnterEmitsRunWorkflow(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "daily", Name: "Daily Report", Description: "Build a safe report", Enabled: true,
		Steps: []config.WorkflowStep{{ID: "report", Name: "Report", Action: "safe", Enabled: true}},
	}}
	m := NewStateMachine(cfg)
	effects := m.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions, Query: "daily"})
	if len(effects) == 0 || m.State().Phase != PhaseOmnibox {
		t.Fatalf("open effects=%v state=%+v", effects, m.State())
	}
	st := m.State()
	workflowIndex := -1
	for i, item := range st.OmniboxItems {
		if item.Kind == ItemWorkflow {
			workflowIndex = i
			if item.Workflow == nil || item.Workflow.Description != "Build a safe report" {
				t.Fatalf("workflow preview data missing: %+v", item)
			}
		}
	}
	if workflowIndex < 0 {
		t.Fatalf("workflow search items=%+v", st.OmniboxItems)
	}
	effects = m.Handle(Event{Type: EventSelectAndConfirmIndex, Index: workflowIndex})
	if len(effects) != 2 || effects[0].Type != EffectRunWorkflow || effects[0].WorkflowID != "daily" {
		t.Fatalf("enter effects=%v", effects)
	}
}

func TestWorkflowSearchExcludesDisabledWorkflows(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Workflows.Workflows = []config.Workflow{{ID: "disabled", Name: "Disabled", Enabled: false}}
	m := NewStateMachine(cfg)
	m.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions, Query: "disabled"})
	for _, item := range m.State().OmniboxItems {
		if item.Kind == ItemWorkflow {
			t.Fatalf("disabled workflow appeared: %+v", item)
		}
	}
}
