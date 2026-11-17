package uiapp

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestWorkflowActionRegistryOnlyExposesSafeCLIAndScripts(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Leader.Nodes = []config.LeaderNode{
		{Key: "safe", Label: "Safe", Action: config.CLIAction("printf", "ok")},
		{Key: "window", Label: "Window", Action: config.WindowAction(config.WindowLeftHalf)},
	}
	cfg.Components.Scripts = []config.ScriptComponentSpec{{ID: "script", Command: "printf", Args: []string{"script"}, Enabled: true}}
	registry := (&AppController{}).workflowActionRegistry(cfg)
	if _, ok := registry.GetAction("leader.safe"); !ok {
		t.Fatal("safe CLI action missing")
	}
	if _, ok := registry.GetAction("safe"); !ok {
		t.Fatal("short CLI action alias missing")
	}
	if _, ok := registry.GetAction("script"); !ok {
		t.Fatal("script action missing")
	}
	if _, ok := registry.GetAction("leader.window"); ok {
		t.Fatal("non-CLI action exposed to workflow")
	}
}

func TestWorkflowProviderInvokesCLIWithoutShellInterpolation(t *testing.T) {
	provider := workflowProvider{
		actionCtx: engine.CapsuleContext{SelectedText: "selected"},
		actions: map[string]workflowAction{
			"safe": {leader: func() *config.LeaderAction {
				a := config.CLIAction("printf", "%s", "literal;not-shell")
				return &a
			}()},
		},
	}
	result, err := provider.Invoke(context.Background(), automation.ActionInvocation{ActionID: "safe"})
	if err != nil || result.Status != automation.ResultSucceeded {
		t.Fatalf("invoke = %#v, %v", result, err)
	}
	if !strings.Contains(result.Output.(string), "literal;not-shell") {
		t.Fatalf("output = %#v", result.Output)
	}
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("printf is unavailable")
	}
}

func TestWorkflowProviderRejectsUnresolvedTemplates(t *testing.T) {
	cli := config.CLIAction("printf", "%s", "{{missing.value}}")
	provider := workflowProvider{actions: map[string]workflowAction{"cli": {leader: &cli}}}
	result, err := provider.Invoke(context.Background(), automation.ActionInvocation{ActionID: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != automation.ResultFailed || !strings.Contains(result.Error, "unresolved template") {
		t.Fatalf("result = %#v", result)
	}
}

func TestWorkflowProviderBindsInputAndStepOutputToCLIAndScript(t *testing.T) {
	cli := config.CLIAction("printf", "%s|%s", "{{input}}", "{{stepOutput}}")
	provider := workflowProvider{
		actions: map[string]workflowAction{
			"cli":    {leader: &cli},
			"script": {script: &config.ScriptComponentSpec{Command: "printf", Args: []string{"%s|%s", "{{input}}", "{{stepOutput}}"}, Enabled: true}},
		},
	}
	invocation := automation.ActionInvocation{
		ActionID: "cli",
		Input:    map[string]any{"input": "prompt-value"},
		Inputs:   map[string]any{"stepOutput": "previous-step-output"},
	}
	result, err := provider.Invoke(context.Background(), invocation)
	if err != nil || result.Status != automation.ResultSucceeded || result.Output != "prompt-value|previous-step-output" {
		t.Fatalf("CLI invoke = %#v, %v", result, err)
	}

	invocation.ActionID = "script"
	result, err = provider.Invoke(context.Background(), invocation)
	if err != nil || result.Status != automation.ResultSucceeded || result.Output != "prompt-value|previous-step-output" {
		t.Fatalf("script invoke = %#v, %v", result, err)
	}
}

func TestApplyWorkflowOutputIgnoresDiscardedStepOutput(t *testing.T) {
	var clipboard string
	controller := &AppController{ClipboardWriter: func(value string) { clipboard = value }}
	controller.applyWorkflowOutput(config.WorkflowOutputClipboard, engine.CapsuleContext{}, automation.ExecutionResult{
		Steps: []automation.StepResult{{
			StepID: "secret", Result: automation.ActionResult{Status: automation.ResultSucceeded, Output: "secret-value"},
		}},
		Outputs: map[string]any{},
	})
	if clipboard != "" {
		t.Fatalf("discarded output reached clipboard: %q", clipboard)
	}
}
