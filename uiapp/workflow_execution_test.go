package uiapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestWorkflowEnterRunsConfiguredSafeActionAndReportsResult(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "safe", Label: "Safe", Action: config.CLIAction("printf", "%s", "workflow-ok")}}
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "run", Name: "Run", Description: "safe", Enabled: true,
		Output: config.WorkflowOutputNotify,
		Steps:  []config.WorkflowStep{{ID: "one", Action: "safe", Enabled: true}},
	}}
	if err := f.ctrl.ConfigStore.SaveAll(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(cfg)
	var got automation.ExecutionResult
	done := make(chan struct{}, 1)
	f.ctrl.OnWorkflowResult = func(_ string, result automation.ExecutionResult, err error) {
		if err == nil {
			got = result
		}
		select {
		case done <- struct{}{}:
		default:
		}
	}
	f.ctrl.OpenOmnibox(config.TabActions, "run")
	f.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("workflow did not report result")
	}
	if len(got.Steps) != 1 || got.Steps[0].Result.Status != automation.ResultSucceeded {
		t.Fatalf("result=%+v", got)
	}
	if !strings.Contains(f.ctrl.State().HUDMessage, "workflow-ok") {
		t.Fatalf("HUD=%q", f.ctrl.State().HUDMessage)
	}
}

func TestWorkflowContextCancellationReportsCanceledResult(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "safe", Label: "Safe", Action: config.CLIAction("printf", "ok")}}
	cfg.Workflows.Workflows = []config.Workflow{{ID: "cancel", Enabled: true, Steps: []config.WorkflowStep{{ID: "one", Action: "safe", Enabled: true}}}}
	if err := f.ctrl.ConfigStore.SaveAll(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.ctrl.RunWorkflow(ctx, "cancel", engine.CapsuleContext{}); err != nil {
		t.Fatalf("run workflow: %v", err)
	}
}
