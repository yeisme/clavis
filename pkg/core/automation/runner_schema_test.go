package automation

import (
	"context"
	"strings"
	"testing"
)

func TestRunnerValidatesOutputSchemaFromRegistry(t *testing.T) {
	workflow := WorkflowDefinition{ID: "schema", Steps: []WorkflowStep{
		{ID: "produce", ActionID: "produce", FailureMode: FailureContinue, OutputMode: OutputCapture},
	}}
	registry := MapActionRegistry{"produce": {
		ID: "produce", OutputSchema: map[string]any{
			"type": "object", "required": []any{"items"},
			"properties": map[string]any{"items": map[string]any{
				"type": "array", "items": map[string]any{"type": "object", "required": []any{"name"},
					"properties": map[string]any{"name": map[string]any{"type": "string"}},
				},
			}},
		},
	}}
	plan, err := BuildExecutionPlanWithRegistry(workflow, registry)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		calls++
		if calls == 1 {
			return ActionResult{Output: map[string]any{"items": []any{map[string]any{"name": "ok"}}}}, nil
		}
		return ActionResult{Output: map[string]any{"items": []any{map[string]any{"name": 7}}}}, nil
	})
	// Execute twice to exercise a valid result followed by an invalid result.
	runner := NewRunner(provider, WithActionRegistry(registry))
	valid, err := runner.Run(context.Background(), plan, nil)
	if err != nil || valid.Steps[0].Result.Status != ResultSucceeded || valid.Outputs["produce"] == nil {
		t.Fatalf("valid result = %#v, err=%v", valid, err)
	}
	invalid, err := runner.Run(context.Background(), plan, nil)
	if err != nil || invalid.Steps[0].Result.Status != ResultFailed {
		t.Fatalf("invalid result = %#v, err=%v", invalid, err)
	}
	failure := invalid.Steps[0].Result
	if failure.ErrorCategory != ErrorCategoryOutputSchema || !strings.Contains(failure.Error, "produce") || !strings.Contains(failure.Error, "$.items[0].name") {
		t.Fatalf("schema failure = %#v", failure)
	}
	if len(invalid.Outputs) != 0 || failure.Output != nil {
		t.Fatalf("invalid output retained: result=%#v outputs=%#v", failure, invalid.Outputs)
	}
}

func TestRunnerOutputSchemaMissingAndInvalidDefinition(t *testing.T) {
	provider := ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		return ActionResult{Output: "value"}, nil
	})
	plan := ExecutionPlan{{ID: "step", ActionID: "step", FailureMode: FailureStop, OutputMode: OutputCapture}}
	noResolver, err := NewRunner(provider).Run(context.Background(), plan, nil)
	if err != nil || noResolver.Steps[0].Result.Status != ResultSucceeded {
		t.Fatalf("no-resolver behavior changed: %#v, %v", noResolver, err)
	}
	invalidSchema := MapActionRegistry{"step": {ID: "step", OutputSchema: map[string]any{"type": "unknown"}}}
	result, err := NewRunner(provider, WithActionRegistry(invalidSchema)).Run(context.Background(), plan, nil)
	if err != nil || result.Steps[0].Result.Status != ResultFailed || result.Steps[0].Result.ErrorCategory != ErrorCategoryOutputSchema {
		t.Fatalf("invalid schema result = %#v, err=%v", result, err)
	}
	if len(result.Outputs) != 0 {
		t.Fatalf("invalid schema output retained: %#v", result.Outputs)
	}
}
