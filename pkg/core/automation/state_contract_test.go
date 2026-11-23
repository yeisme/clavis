package automation

import (
	"context"
	"errors"
	"testing"

	"github.com/yeisme/clavis/pkg/core/host"
)

func TestRunnerPublishesUnifiedSuccessStateAndOperation(t *testing.T) {
	plan := ExecutionPlan{{ID: "step", ActionID: "build", FailureMode: FailureStop, OutputMode: OutputCapture}}
	result, err := NewRunner(ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		return ActionResult{Status: ResultSucceeded, Output: "ok"}, nil
	})).Run(context.Background(), plan, nil)
	if err != nil || result.Status != ExecutionSucceeded || result.Steps[0].Status != StepSucceeded {
		t.Fatalf("success state=%+v err=%v", result, err)
	}
	if result.Operation.State != host.StateReady || result.Operation.Code != "execution_succeeded" {
		t.Fatalf("success operation=%+v", result.Operation)
	}
}

func TestRunnerStopsBeforeProviderWhenApprovalIsRequired(t *testing.T) {
	calls := 0
	plan := ExecutionPlan{{ID: "deploy", ActionID: "deploy", RequiresApproval: true, FailureMode: FailureStop, OutputMode: OutputCapture}}
	result, err := NewRunner(ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		calls++
		return ActionResult{Status: ResultSucceeded}, nil
	})).Run(context.Background(), plan, nil)
	if err != nil || calls != 0 {
		t.Fatalf("approval invoked provider: calls=%d err=%v", calls, err)
	}
	if result.Status != ExecutionAwaitingHuman || !result.AwaitingHuman || result.Approval.Status != ApprovalPending || result.Steps[0].Status != StepAwaitingHuman {
		t.Fatalf("approval state=%+v", result)
	}
	if result.Operation.State != host.StateDegraded || result.Operation.Code != "awaiting_human" {
		t.Fatalf("approval operation=%+v", result.Operation)
	}
}

type approvalTestProvider struct {
	decision ApprovalDecision
}

func (p approvalTestProvider) RequestApproval(context.Context, ApprovalRequest) (ApprovalDecision, error) {
	return p.decision, nil
}

func TestRunnerExecutesAfterApprovalProviderAccepts(t *testing.T) {
	calls := 0
	plan := ExecutionPlan{{ID: "deploy", ActionID: "deploy", RequiresApproval: true, FailureMode: FailureStop, OutputMode: OutputCapture}}
	runner := NewRunner(ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		calls++
		return ActionResult{Status: ResultSucceeded}, nil
	}), WithApprovalProvider(approvalTestProvider{decision: ApprovalDecisionApproved}), WithGoal("deploy safely"))
	result, err := runner.Run(context.Background(), plan, nil)
	if err != nil || calls != 1 || result.Status != ExecutionSucceeded || result.Goal != "deploy safely" || result.Approval.Status != ApprovalApproved {
		t.Fatalf("approved execution=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestExecutionPlanCarriesRegistryApprovalAndRollbackMetadata(t *testing.T) {
	workflow := WorkflowDefinition{ID: "deploy", Steps: []WorkflowStep{{ID: "step", ActionID: "deploy", FailureMode: FailureStop, OutputMode: OutputCapture}}}
	plan, err := BuildExecutionPlanWithRegistry(workflow, MapActionRegistry{"deploy": {
		ID: "deploy", RequiresApproval: true, RollbackActionID: "undo",
	}})
	if err != nil || !plan[0].RequiresApproval || plan[0].RollbackActionID != "undo" {
		t.Fatalf("plan metadata=%+v err=%v", plan, err)
	}
}

type rollbackTestProvider struct {
	invoked     []string
	rollbackErr error
}

func (p *rollbackTestProvider) Invoke(_ context.Context, invocation ActionInvocation) (ActionResult, error) {
	p.invoked = append(p.invoked, invocation.ActionID)
	if invocation.ActionID == "fail" {
		return ActionResult{}, errors.New("boom")
	}
	return ActionResult{Status: ResultSucceeded, Output: "changed"}, nil
}

func (p *rollbackTestProvider) Rollback(_ context.Context, invocation ActionInvocation, _ ActionResult) (ActionResult, error) {
	p.invoked = append(p.invoked, invocation.ActionID)
	if p.rollbackErr != nil {
		return ActionResult{}, p.rollbackErr
	}
	return ActionResult{Status: ResultSucceeded}, nil
}

func TestRunnerReportsRollbackCompletionAndOperationDegraded(t *testing.T) {
	provider := &rollbackTestProvider{}
	plan := ExecutionPlan{
		{ID: "write", ActionID: "write", RollbackActionID: "undo", FailureMode: FailureStop, OutputMode: OutputCapture},
		{ID: "fail", ActionID: "fail", FailureMode: FailureStop, OutputMode: OutputCapture},
	}
	result, err := NewRunner(provider, WithRollbackProvider(provider)).Run(context.Background(), plan, nil)
	if err != nil || result.Status != ExecutionRolledBack || result.Rollback.Status != RollbackSucceeded {
		t.Fatalf("rollback state=%+v err=%v", result, err)
	}
	if result.Operation.State != host.StateDegraded || result.Operation.Code != "rolled_back" {
		t.Fatalf("rollback operation=%+v", result.Operation)
	}
	if len(result.Rollback.Steps) != 1 || result.Rollback.Steps[0].Status != StepSucceeded {
		t.Fatalf("rollback steps=%+v", result.Rollback.Steps)
	}
}

func TestRunnerDoesNotClaimRollbackWhenProviderIsUnavailable(t *testing.T) {
	provider := &rollbackTestProvider{}
	plan := ExecutionPlan{
		{ID: "write", ActionID: "write", RollbackActionID: "undo", FailureMode: FailureStop, OutputMode: OutputCapture},
		{ID: "fail", ActionID: "fail", FailureMode: FailureStop, OutputMode: OutputCapture},
	}
	result, err := NewRunner(provider).Run(context.Background(), plan, nil)
	if err != nil || result.Status != ExecutionUnknown || result.Rollback.Status != RollbackUnknown {
		t.Fatalf("missing rollback state=%+v err=%v", result, err)
	}
	if result.Operation.State != host.StateUnknown || result.Operation.Code != "rollback_unknown" {
		t.Fatalf("missing rollback operation=%+v", result.Operation)
	}
}

func TestRunnerPreservesUnknownProviderStatus(t *testing.T) {
	plan := ExecutionPlan{{ID: "step", ActionID: "tool", FailureMode: FailureStop, OutputMode: OutputCapture}}
	result, err := NewRunner(ProviderFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		return ActionResult{Status: ResultStatus("provider_pending")}, nil
	})).Run(context.Background(), plan, nil)
	if err != nil || result.Status != ExecutionUnknown || result.Steps[0].Result.Status != ResultUnknown {
		t.Fatalf("unknown provider result=%+v err=%v", result, err)
	}
	if result.Operation.State != host.StateUnknown {
		t.Fatalf("unknown provider operation=%+v", result.Operation)
	}
}
