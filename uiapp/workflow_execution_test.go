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

func TestWorkflowRequiredInputFailsBeforeProviderExecution(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "safe", Label: "Safe", Action: config.CLIAction("printf", "provider-ran")}}
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "required", Enabled: true,
		Inputs: []config.WorkflowInput{{ID: "token", Required: true}},
		Steps:  []config.WorkflowStep{{ID: "one", Action: "safe", Enabled: true}},
	}}
	if err := f.ctrl.ConfigStore.SaveAll(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(cfg)
	if err := f.ctrl.ExecuteWorkflow("required", engine.CapsuleContext{}); err == nil || !strings.Contains(err.Error(), "required workflow input") {
		t.Fatalf("ExecuteWorkflow error = %v", err)
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

func TestCancelWorkflowSuppressesCompletionCallbackAndHUD(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "slow", Label: "Slow", Action: config.CLIAction("sleep", "5")}}
	cfg.Workflows.Workflows = []config.Workflow{{ID: "cancelable", Enabled: true, Steps: []config.WorkflowStep{{ID: "one", Action: "slow", Enabled: true}}}}
	if err := f.ctrl.ConfigStore.SaveAll(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(cfg)
	callback := make(chan struct{}, 1)
	f.ctrl.OnWorkflowResult = func(string, automation.ExecutionResult, error) { callback <- struct{}{} }
	if err := f.ctrl.ExecuteWorkflow("cancelable", engine.CapsuleContext{}); err != nil {
		t.Fatalf("execute workflow: %v", err)
	}
	if !f.ctrl.CancelWorkflow() {
		t.Fatal("CancelWorkflow returned false for active run")
	}
	select {
	case <-callback:
		t.Fatal("canceled workflow invoked completion callback")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWorkflowShutdownSuppressesCompletionCallback(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "slow", Label: "Slow", Action: config.CLIAction("sleep", "5")}}
	cfg.Workflows.Workflows = []config.Workflow{{ID: "shutdown", Enabled: true, Steps: []config.WorkflowStep{{ID: "one", Action: "slow", Enabled: true}}}}
	if err := f.ctrl.ConfigStore.SaveAll(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(cfg)
	callback := make(chan struct{}, 1)
	f.ctrl.OnWorkflowResult = func(string, automation.ExecutionResult, error) { callback <- struct{}{} }
	if err := f.ctrl.ExecuteWorkflow("shutdown", engine.CapsuleContext{}); err != nil {
		t.Fatalf("execute workflow: %v", err)
	}
	f.ctrl.Close()
	select {
	case <-callback:
		t.Fatal("shutdown workflow invoked completion callback")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWorkflowReplaceOutputInjectsIntoActiveTarget(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "safe", Label: "Safe", Action: config.CLIAction("printf", "%s", "replacement-text")}}
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "replace", Name: "Replace", Enabled: true,
		Output: config.WorkflowOutputReplace,
		Steps:  []config.WorkflowStep{{ID: "one", Action: "safe", Enabled: true}},
	}}
	if err := f.ctrl.ConfigStore.SaveAll(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(cfg)

	f.ctrl.OpenOmnibox(config.TabActions, "replace")
	f.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.bridge.mu.Lock()
		injected := append([]injectedCall(nil), f.bridge.injected...)
		f.bridge.mu.Unlock()
		if len(injected) > 0 {
			if injected[0].text != "replacement-text" {
				t.Fatalf("injected text = %q", injected[0].text)
			}
			if want := len([]rune("hello clavis world")); injected[0].delCount != want {
				t.Fatalf("deleteCount = %d, want %d", injected[0].delCount, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workflow replace output was never injected")
}

func TestWorkflowOutputDoesNotInjectAfterForegroundAppSwitch(t *testing.T) {
	f := newInteractionFixture(t, nil)
	result := automation.ExecutionResult{
		Steps:   []automation.StepResult{{StepID: "one", Result: automation.ActionResult{Status: automation.ResultSucceeded, Output: "replacement"}}},
		Outputs: map[string]any{"one": "replacement"},
	}
	target := engine.CapsuleContext{AppName: "TextEdit", BundleID: "com.apple.TextEdit"}
	f.bridge.mu.Lock()
	f.bridge.snapshot.AppName = "Safari"
	f.bridge.snapshot.BundleID = "com.apple.Safari"
	f.bridge.mu.Unlock()
	f.ctrl.applyWorkflowOutputAtTarget(config.WorkflowOutputReplace, target, result, 0)
	time.Sleep(50 * time.Millisecond)
	f.bridge.mu.Lock()
	injected := len(f.bridge.injected)
	f.bridge.mu.Unlock()
	if injected != 0 {
		t.Fatalf("workflow output crossed foreground app switch: %+v", f.bridge.injected)
	}
}

func TestWorkflowOutputDoesNotInjectIntoStaleForegroundTarget(t *testing.T) {
	f := newInteractionFixture(t, nil)
	result := automation.ExecutionResult{
		Steps:   []automation.StepResult{{StepID: "one", Result: automation.ActionResult{Status: automation.ResultSucceeded, Output: "replacement"}}},
		Outputs: map[string]any{"one": "replacement"},
	}
	// Opening a new capsule advances the target generation after this run began.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	f.ctrl.applyWorkflowOutputAtTarget(config.WorkflowOutputReplace, engine.CapsuleContext{SelectedText: "old selection"}, result, 0)
	time.Sleep(50 * time.Millisecond)
	f.bridge.mu.Lock()
	injected := len(f.bridge.injected)
	f.bridge.mu.Unlock()
	if injected != 0 {
		t.Fatalf("stale workflow output was injected: %+v", f.bridge.injected)
	}
	if !strings.Contains(f.ctrl.State().HUDMessage, "replacement") {
		t.Fatalf("stale output was not downgraded to notification: HUD=%q", f.ctrl.State().HUDMessage)
	}
}
