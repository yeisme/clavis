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

func TestWorkflowStepSourceInputIDsAreInjective(t *testing.T) {
	ids := []string{"normal", "with_underscore", "a-b", "a_b", "a_x5f_b"}
	keys := make(map[string]string, len(ids))
	for _, id := range ids {
		key := workflowStepSourceInputID(config.WorkflowStep{ID: id})
		if previous, exists := keys[key]; exists {
			t.Fatalf("step IDs %q and %q share internal source key %q", previous, id, key)
		}
		keys[key] = id
	}
	if got := keys[workflowStepSourceInputID(config.WorkflowStep{ID: "normal"})]; got != "normal" {
		t.Fatalf("normal step ID was not recorded in key map: %q", got)
	}
	if got := workflowStepSourceInputID(config.WorkflowStep{ID: "normal"}); got != "__workflow_step_normal_text" {
		t.Fatalf("normal step source key = %q", got)
	}
	if got := workflowStepSourceInputID(config.WorkflowStep{ID: "a-b"}); got == workflowStepSourceInputID(config.WorkflowStep{ID: "a_b"}) {
		t.Fatalf("colliding step IDs produced the same source key: %q", got)
	}
}

func TestWorkflowStepSourceBindingsResolveCollidingIDs(t *testing.T) {
	workflow := config.Workflow{
		ID: "source-bindings",
		Steps: []config.WorkflowStep{
			{ID: "a-b", Action: "first", Enabled: true, Source: config.WorkflowSourceSelection},
			{ID: "a_b", Action: "second", Enabled: true, Source: config.WorkflowSourceClipboard},
		},
	}
	definition := workflowDefinition(workflow)
	inputs, err := workflowInputs(workflow, engine.CapsuleContext{SelectedText: "selected-value", ClipboardPreview: "clipboard-value"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := automation.BuildExecutionPlan(definition)
	if err != nil {
		t.Fatal(err)
	}
	var bound []string
	provider := automation.ProviderFunc(func(_ context.Context, invocation automation.ActionInvocation) (automation.ActionResult, error) {
		bound = append(bound, invocation.Input["text"].(string))
		return automation.ActionResult{Status: automation.ResultSucceeded, Output: invocation.Input["text"]}, nil
	})
	if _, err := automation.Run(context.Background(), plan, provider, inputs); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(bound, "|"), "selected-value|clipboard-value"; got != want {
		t.Fatalf("source bindings = %q, want %q", got, want)
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
		{"workflow abort", func(w *config.Workflow) { w.Failure = config.WorkflowFailureMode("abort") }, "unsupported failure mode"},
		{"workflow output", func(w *config.Workflow) { w.Output = "unknown" }, "unsupported output mode"},
		{"workflow discard output", func(w *config.Workflow) { w.Output = config.WorkflowOutput("discard") }, "unsupported output mode"},
		{"workflow prompt source", func(w *config.Workflow) { w.Source = config.WorkflowSourcePrompt }, "unsupported source"},
		{"step execution", func(w *config.Workflow) { w.Steps[0].Execution = "parallel" }, "unsupported execution mode"},
		{"step failure", func(w *config.Workflow) { w.Steps[0].Failure = config.WorkflowFailureNotify }, "unsupported failure mode"},
		{"step abort", func(w *config.Workflow) { w.Steps[0].Failure = config.WorkflowFailureMode("abort") }, "unsupported failure mode"},
		{"step output", func(w *config.Workflow) { w.Steps[0].Output = config.WorkflowOutputInsert }, "unsupported output mode"},
		{"step discard output", func(w *config.Workflow) { w.Steps[0].Output = config.WorkflowOutput("discard") }, "unsupported output mode"},
		{"step prompt source", func(w *config.Workflow) { w.Steps[0].Source = config.WorkflowSourcePrompt }, "unsupported source"},
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
	workflow.Output = config.WorkflowOutputNone
	workflow.Steps[0].Output = config.WorkflowOutputNone
	if err := validateWorkflowAdapter(workflow); err != nil {
		t.Fatalf("intentional OutputNone should be runner compatible: %v", err)
	}
}

// TestDecodeLegacyDiscardOutputIsRuntimeCompatible proves the compatibility
// path for the legacy output = "discard" literal: decoding folds it into the
// canonical none value the runner applies, so a legacy file still validates and
// plans instead of silently capturing output.
func TestDecodeLegacyDiscardOutputIsRuntimeCompatible(t *testing.T) {
	decoded, err := config.DecodeWorkflowsConfig(`[[workflows]]
id = "legacy"
output = "discard"
[[workflows.steps]]
id = "one"
output = "discard"
`)
	if err != nil {
		t.Fatalf("decode legacy workflow: %v", err)
	}
	if got := decoded.Workflows[0].Output; got != config.WorkflowOutputNone {
		t.Fatalf("workflow output = %q, want none", got)
	}
	if got := decoded.Workflows[0].Steps[0].Output; got != config.WorkflowOutputNone {
		t.Fatalf("step output = %q, want none", got)
	}
	if err := validateWorkflowAdapter(decoded.Workflows[0]); err != nil {
		t.Fatalf("decoded legacy discard output should be runner compatible: %v", err)
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

// TestValidateWorkflowStepActionsUsesControllerRegistry checks the save-path
// gate consults the same executable registry the controller plans with.
func TestValidateWorkflowStepActionsSkipsDisabledSteps(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	workflow := config.Workflow{ID: "flow", Steps: []config.WorkflowStep{{ID: "disabled", Action: "missing", Enabled: false}}}
	if err := ctrl.validateWorkflowStepActions(workflow); err != nil {
		t.Fatalf("disabled step should not require an action: %v", err)
	}
}

func TestValidateWorkflowStepActionsUsesControllerRegistry(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	seedWorkflowScripts(t, ctrl, "echo")
	workflow := config.Workflow{ID: "flow", Steps: []config.WorkflowStep{{ID: "run", Enabled: true}}}
	if err := ctrl.validateWorkflowStepActions(workflow); err == nil || !strings.Contains(err.Error(), "action ID") {
		t.Fatalf("empty action error = %v, want an inline action-ID error", err)
	}
	workflow.Steps[0].Action = "script.missing"
	if err := ctrl.validateWorkflowStepActions(workflow); err == nil || !strings.Contains(err.Error(), "not available to workflows") {
		t.Fatalf("unknown action error = %v, want a registry lookup error", err)
	}
	workflow.Steps[0].Action = "script.echo"
	if err := ctrl.validateWorkflowStepActions(workflow); err != nil {
		t.Fatalf("registered action rejected: %v", err)
	}
}

func TestWorkflowDefinitionInheritsWorkflowFailurePolicy(t *testing.T) {
	workflow := config.Workflow{ID: "continue", Failure: config.WorkflowFailureContinue, Steps: []config.WorkflowStep{{ID: "first", Action: "run", Enabled: true}}}
	definition := workflowDefinition(workflow)
	plan, err := automation.BuildExecutionPlan(definition)
	if err != nil || definition.FailureMode != automation.FailureContinue || len(plan) != 1 || plan[0].FailureMode != automation.FailureContinue {
		t.Fatalf("definition=%+v plan=%+v err=%v", definition, plan, err)
	}
}
