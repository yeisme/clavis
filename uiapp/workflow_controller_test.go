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

func TestWorkflowTemplatesResolveBareAndNamedInputs(t *testing.T) {
	cli := config.CLIAction("printf", "%s|%s|%s", "{{text}}", "{{tone}}", "{{input}}")
	provider := workflowProvider{
		actions:        map[string]workflowAction{"cli": {leader: &cli}},
		workflowInputs: map[string]any{"text": "selected", "tone": "concise", "input": map[string]any{"text": "selected", "tone": "concise"}},
	}
	result, err := provider.Invoke(context.Background(), automation.ActionInvocation{ActionID: "cli"})
	if err != nil || result.Status != automation.ResultSucceeded || result.Output != "selected|concise|{\"text\":\"selected\",\"tone\":\"concise\"}" {
		t.Fatalf("invoke = %#v, %v", result, err)
	}
}

func TestWorkflowSourceMapsToDistinctCapsuleValues(t *testing.T) {
	ctx := engine.CapsuleContext{SelectedText: "selection", SurroundingText: "surrounding", ClipboardPreview: "clipboard"}
	for _, tc := range []struct {
		source config.WorkflowSource
		want   string
	}{
		{config.WorkflowSourceSelection, "selection"},
		{config.WorkflowSourceText, "surrounding"},
		{config.WorkflowSourceClipboard, "clipboard"},
	} {
		got, err := workflowSourceValue(tc.source, ctx)
		if err != nil || got != tc.want {
			t.Errorf("source %q = %q, %v; want %q", tc.source, got, err, tc.want)
		}
	}
	if _, err := workflowSourceValue(config.WorkflowSourcePrompt, ctx); err == nil {
		t.Fatal("prompt source without explicit prompt input should fail")
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

func TestValidateWorkflowAdapterRejectsDisabledDependency(t *testing.T) {
	workflow := config.Workflow{
		ID: "in-memory",
		Steps: []config.WorkflowStep{
			{ID: "disabled", Enabled: false},
			{ID: "enabled", Enabled: true, DependsOn: []string{"disabled"}},
		},
	}
	if err := validateWorkflowAdapter(workflow); err == nil || !strings.Contains(err.Error(), "depends on disabled step") {
		t.Fatalf("validateWorkflowAdapter error = %v, want targeted disabled-step dependency error", err)
	}
}

func TestValidateWorkflowAdapterRejectsUnsupportedModes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.Workflow)
		want   string
	}{
		{"workflow execution", func(w *config.Workflow) { w.Execution = "parallel" }, "unsupported execution mode"},
		{"workflow failure", func(w *config.Workflow) { w.Failure = config.WorkflowFailureNotify }, "unsupported failure mode"},
		{"workflow output", func(w *config.Workflow) { w.Output = "unknown" }, "unsupported output mode"},
		{"step execution", func(w *config.Workflow) { w.Steps[0].Execution = "parallel" }, "unsupported execution mode"},
		{"step failure", func(w *config.Workflow) { w.Steps[0].Failure = config.WorkflowFailureNotify }, "unsupported failure mode"},
		{"step output", func(w *config.Workflow) { w.Steps[0].Output = config.WorkflowOutputInsert }, "unsupported output mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflow := config.Workflow{ID: "in-memory", Steps: []config.WorkflowStep{{ID: "one", Enabled: true}}}
			tt.mutate(&workflow)
			if err := validateWorkflowAdapter(workflow); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateWorkflowAdapter error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateWorkflowAdapterAcceptsRunnerSupportedModes(t *testing.T) {
	workflow := config.Workflow{
		ID:        "supported",
		Output:    config.WorkflowOutputNotify,
		Failure:   config.WorkflowFailureStop,
		Execution: config.WorkflowExecutionSequential,
		Steps: []config.WorkflowStep{{
			ID: "one", Enabled: true,
			Output:    config.WorkflowOutputNotify,
			Failure:   config.WorkflowFailureContinue,
			Execution: config.WorkflowExecutionSequential,
		}},
	}
	if err := validateWorkflowAdapter(workflow); err != nil {
		t.Fatalf("validateWorkflowAdapter returned error: %v", err)
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
