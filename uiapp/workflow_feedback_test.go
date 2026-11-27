package uiapp

import (
	"context"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestWorkflowFeedbackReportsFailureAndTimeout(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{
		{Key: "fail", Label: "Fail", Action: config.CLIAction("false")},
		{Key: "wait", Label: "Wait", Action: config.CLIAction("sleep", "2")},
	}
	cfg.Workflows.Workflows = []config.Workflow{
		{ID: "failure", Enabled: true, Steps: []config.WorkflowStep{{ID: "one", Action: "fail", Enabled: true}}},
		{ID: "timeout", Enabled: true, Steps: []config.WorkflowStep{{ID: "one", Action: "wait", Enabled: true}}},
	}
	if err := f.ctrl.ConfigStore.SaveAll(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	f.ctrl.SetConfig(cfg)
	results := make(chan automation.ExecutionResult, 2)
	f.ctrl.OnWorkflowResult = func(_ string, result automation.ExecutionResult, _ error) { results <- result }
	if err := f.ctrl.ExecuteWorkflow("failure", engine.CapsuleContext{}); err != nil {
		t.Fatalf("failure plan: %v", err)
	}
	select {
	case result := <-results:
		if len(result.Steps) != 1 || result.Steps[0].Result.Status != automation.ResultFailed {
			t.Fatalf("failure result=%+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failure result timeout")
	}
	provider := workflowProvider{controller: f.ctrl, actions: f.ctrl.workflowActionRegistry(cfg).actions}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := provider.Invoke(ctx, automation.ActionInvocation{ActionID: "wait"})
	if err == nil && result.Status != automation.ResultFailed {
		t.Fatalf("timeout result=%+v err=%v", result, err)
	}
}
