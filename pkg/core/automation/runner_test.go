package automation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type recordingProvider struct {
	calls []ActionInvocation
	fn    func(context.Context, ActionInvocation) (ActionResult, error)
}

func (p *recordingProvider) Invoke(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	p.calls = append(p.calls, invocation)
	return p.fn(ctx, invocation)
}

func TestRunnerSerialBindingsAndDeterministicResults(t *testing.T) {
	provider := &recordingProvider{fn: func(_ context.Context, invocation ActionInvocation) (ActionResult, error) {
		return ActionResult{Status: ResultSucceeded, Output: map[string]any{"value": invocation.Input["value"]}}, nil
	}}
	plan := ExecutionPlan{
		{ID: "first", ActionID: "produce", FailureMode: FailureStop, OutputMode: OutputCapture,
			Input: map[string]any{"value": "{{input.name}}", "nested": map[string]any{"raw": "{{input.object.x}}"}}},
		{ID: "second", ActionID: "consume", FailureMode: FailureStop, OutputMode: OutputCapture,
			Input: map[string]any{"value": "prefix-{{step.first.output.value}}", "whole": "{{step.first.output}}"}},
	}
	got, err := NewRunner(provider).Run(context.Background(), plan, map[string]any{
		"name": "Ada", "object": map[string]any{"x": 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Stopped || got.Canceled || len(got.Steps) != 2 {
		t.Fatalf("unexpected execution state: %#v", got)
	}
	if got.Steps[0].Invocation.Input["value"] != "Ada" || got.Steps[0].Invocation.Input["nested"].(map[string]any)["raw"] != 7 {
		t.Fatalf("input binding = %#v", got.Steps[0].Invocation.Input)
	}
	second := got.Steps[1].Invocation.Input
	if second["value"] != "prefix-Ada" {
		t.Fatalf("unexpected bound output: %#v", second)
	}
	if !reflect.DeepEqual(second["whole"], map[string]any{"value": "Ada"}) {
		t.Fatalf("structured output binding = %#v", second["whole"])
	}
	if !reflect.DeepEqual(got.Outputs, map[string]any{"first": map[string]any{"value": "Ada"}, "second": map[string]any{"value": "prefix-Ada"}}) {
		t.Fatalf("outputs = %#v", got.Outputs)
	}
}

func TestRunnerDiscardedOutputStaysInternalAndIsRedactedFromResult(t *testing.T) {
	provider := ProviderFunc(func(_ context.Context, invocation ActionInvocation) (ActionResult, error) {
		if invocation.ActionID == "produce" {
			return ActionResult{Status: ResultSucceeded, Output: "discarded-secret"}, nil
		}
		return ActionResult{Status: ResultSucceeded, Output: map[string]any{"echo": invocation.Input["value"]}}, nil
	})
	plan := ExecutionPlan{
		{ID: "produce", ActionID: "produce", FailureMode: FailureStop, OutputMode: OutputDiscard},
		{ID: "consume", ActionID: "consume", FailureMode: FailureStop, OutputMode: OutputCapture,
			Input: map[string]any{"value": "prefix-{{step.produce.output}}"}},
	}
	got, err := Run(context.Background(), plan, provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[0].Result.Output != nil {
		t.Fatalf("discarded step result output = %#v", got.Steps[0].Result.Output)
	}
	if got.Steps[1].Invocation.Input["value"] != "prefix-[REDACTED]" {
		t.Fatalf("discarded value leaked through invocation: %#v", got.Steps[1].Invocation.Input)
	}
	serialized, marshalErr := json.Marshal(got)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(serialized), "discarded-secret") {
		t.Fatalf("discarded value leaked through execution result: %s", serialized)
	}
}

func TestRunnerRedactsTypedCollectionsAndPreservesInternalBindings(t *testing.T) {
	type typedRecord struct {
		Secret string   `json:"secret"`
		Nested []string `json:"nested"`
	}
	type typedPayload struct {
		Records []typedRecord          `json:"records"`
		Lookup  map[string]typedRecord `json:"lookup"`
		Array   [2]string              `json:"array"`
	}
	secret := typedPayload{
		Records: []typedRecord{{Secret: "typed-secret", Nested: []string{"nested-secret"}}},
		Lookup:  map[string]typedRecord{"one": {Secret: "map-secret"}},
		Array:   [2]string{"array-secret", "safe"},
	}
	var consumed any
	provider := ProviderFunc(func(_ context.Context, invocation ActionInvocation) (ActionResult, error) {
		if invocation.ActionID == "produce" {
			return ActionResult{Status: ResultSucceeded, Output: secret}, nil
		}
		consumed = invocation.Input["whole"]
		return ActionResult{Status: ResultSucceeded, Output: invocation.Input}, nil
	})
	plan := ExecutionPlan{
		{ID: "produce", ActionID: "produce", FailureMode: FailureStop, OutputMode: OutputDiscard},
		{ID: "consume", ActionID: "consume", FailureMode: FailureStop, OutputMode: OutputCapture,
			Input: map[string]any{"whole": "{{step.produce.output}}"}},
	}
	got, err := Run(context.Background(), plan, provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := consumed.(typedPayload)
	if !ok || bound.Records[0].Secret != "typed-secret" || bound.Lookup["one"].Secret != "map-secret" {
		t.Fatalf("internal typed binding was changed: %#v", consumed)
	}
	serialized, marshalErr := json.Marshal(got)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, leaked := range []string{"typed-secret", "nested-secret", "map-secret", "array-secret"} {
		if strings.Contains(string(serialized), leaked) {
			t.Fatalf("typed discarded value leaked through execution result: %s", serialized)
		}
	}
	if !strings.Contains(string(serialized), redactedOutput) {
		t.Fatalf("redaction marker missing from execution result: %s", serialized)
	}
}

func TestRunnerRedactionDoesNotCorruptShortTokensOrNumericValues(t *testing.T) {
	provider := ProviderFunc(func(_ context.Context, invocation ActionInvocation) (ActionResult, error) {
		if invocation.ActionID == "produce" {
			return ActionResult{Status: ResultSucceeded, Output: map[int]string{12: "long-secret"}}, nil
		}
		return ActionResult{Status: ResultSucceeded, Output: map[string]any{
			"number": 120, "key": invocation.Input["whole"], "text": "120 long-secret safe",
		}}, nil
	})
	plan := ExecutionPlan{
		{ID: "produce", ActionID: "produce", FailureMode: FailureStop, OutputMode: OutputDiscard},
		{ID: "consume", ActionID: "consume", FailureMode: FailureStop, OutputMode: OutputCapture, Input: map[string]any{"whole": "{{step.produce.output}}"}},
	}
	got, err := Run(context.Background(), plan, provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "long-secret") {
		t.Fatalf("secret leaked: %s", encoded)
	}
	if !strings.Contains(string(encoded), "120") {
		t.Fatalf("numeric value was broadly corrupted: %s", encoded)
	}
}

func TestBuildExecutionPlanCarriesRequiredInputsToEveryStep(t *testing.T) {
	plan, err := BuildExecutionPlan(WorkflowDefinition{
		ID: "workflow", Inputs: []WorkflowInput{{ID: "token", Required: true}},
		Steps: []WorkflowStep{{ID: "step", ActionID: "run", FailureMode: FailureStop, OutputMode: OutputCapture}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan[0].RequiredInputs, []string{"token"}) {
		t.Fatalf("required inputs = %#v", plan[0].RequiredInputs)
	}
}

func TestRunnerEnforcesRequiredInputsInPlan(t *testing.T) {
	calls := 0
	provider := ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		calls++
		return ActionResult{Status: ResultSucceeded}, nil
	})
	plan := ExecutionPlan{{ID: "step", ActionID: "run", FailureMode: FailureStop, OutputMode: OutputCapture, RequiredInputs: []string{"token"}}}
	result, err := Run(context.Background(), plan, provider, map[string]any{"token": "  "})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || result.Steps[0].Result.Status != ResultFailed || !strings.Contains(result.Steps[0].Result.Error, "required workflow input") {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}

func TestRunnerFailureContinueAndStopSkipUnstartedSteps(t *testing.T) {
	var calls []string
	provider := ProviderFunc(func(_ context.Context, invocation ActionInvocation) (ActionResult, error) {
		calls = append(calls, invocation.ActionID)
		if invocation.ActionID == "fail" {
			return ActionResult{}, errors.New("boom")
		}
		return ActionResult{Status: ResultSucceeded, Output: invocation.ActionID}, nil
	})
	plan := ExecutionPlan{
		{ID: "continue", ActionID: "fail", FailureMode: FailureContinue, OutputMode: OutputCapture},
		{ID: "after-continue", ActionID: "ok", FailureMode: FailureStop, OutputMode: OutputCapture},
		{ID: "stop", ActionID: "fail", FailureMode: FailureStop, OutputMode: OutputCapture},
		{ID: "never-started", ActionID: "never", FailureMode: FailureContinue, OutputMode: OutputCapture},
	}
	got, err := Run(context.Background(), plan, provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"fail", "ok", "fail"}) {
		t.Fatalf("provider calls = %v", calls)
	}
	if !got.Stopped || len(got.Steps) != len(plan) {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Steps[0].Result.Status != ResultFailed || got.Steps[1].Result.Status != ResultSucceeded || got.Steps[2].Result.Status != ResultFailed || got.Steps[3].Result.Status != ResultSkipped {
		t.Fatalf("statuses = %#v", got.Steps)
	}
	if got.Steps[3].Result.Error == "" {
		t.Fatal("skipped step should explain why it was skipped")
	}
}

func TestRunnerCancellationSkipsRemainingStepsAndPassesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := ProviderFunc(func(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
		if invocation.ActionID == "cancel" {
			cancel()
		}
		return ActionResult{Status: ResultSucceeded}, nil
	})
	plan := ExecutionPlan{
		{ID: "cancel", ActionID: "cancel", FailureMode: FailureStop, OutputMode: OutputCapture},
		{ID: "later", ActionID: "later", FailureMode: FailureStop, OutputMode: OutputCapture},
	}
	got, err := NewRunner(provider).Run(ctx, plan, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context cancellation", err)
	}
	if !got.Canceled || got.Steps[1].Result.Status != ResultSkipped {
		t.Fatalf("unexpected cancellation result: %#v", got)
	}
}

func TestRunnerTemplateErrorsRespectFailureMode(t *testing.T) {
	calls := 0
	provider := ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		calls++
		return ActionResult{Status: ResultSucceeded}, nil
	})
	plan := ExecutionPlan{
		{ID: "bad", ActionID: "bad", Input: map[string]any{"x": "{{missing.x}}"}, FailureMode: FailureContinue, OutputMode: OutputCapture},
		{ID: "good", ActionID: "good", FailureMode: FailureStop, OutputMode: OutputCapture},
	}
	got, err := NewRunner(provider).Run(context.Background(), plan, nil)
	if err != nil || calls != 1 {
		t.Fatalf("template failure execution: err=%v calls=%d", err, calls)
	}
	if got.Steps[0].Result.Status != ResultFailed || !strings.Contains(got.Steps[0].Result.Error, "unsupported template") {
		t.Fatalf("template result = %#v", got.Steps[0].Result)
	}
}

func TestRunnerRejectsNilProvider(t *testing.T) {
	_, err := NewRunner(nil).Run(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "provider is nil") {
		t.Fatalf("error = %v", err)
	}
}
