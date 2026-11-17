package automation

import (
	"context"
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
