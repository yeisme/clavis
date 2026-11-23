package automation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/jsonschema"
)

// Provider executes an action invocation. Providers own the implementation of
// an action; the runner only supplies a context and JSON-compatible input. In
// particular, the runner never starts a process or interprets input as shell
// text.
type Provider interface {
	Invoke(context.Context, ActionInvocation) (ActionResult, error)
}

// ProviderFunc adapts a function to Provider.
type ProviderFunc func(context.Context, ActionInvocation) (ActionResult, error)

// Invoke implements Provider.
func (f ProviderFunc) Invoke(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	return f(ctx, invocation)
}

// StepResult is the result of one planned step. Results are kept in plan order
// even when a step was skipped, so callers can render a stable execution log.
type StepResult struct {
	StepID     string           `json:"stepId"`
	ActionID   string           `json:"actionId"`
	Status     StepStatus       `json:"status"`
	Invocation ActionInvocation `json:"invocation"`
	Result     ActionResult     `json:"result"`
}

// ExecutionResult contains every step in the plan, in deterministic plan
// order. Outputs contains successful step outputs by step ID. A separate map
// is intentionally not exposed for discarded outputs: discarded output may be
// used by later bindings during this run, but is not a workflow result.
type ExecutionResult struct {
	Goal          string               `json:"goal,omitempty"`
	Status        ExecutionStatus      `json:"status"`
	Steps         []StepResult         `json:"steps"`
	Outputs       map[string]any       `json:"outputs,omitempty"`
	Stopped       bool                 `json:"stopped,omitempty"`
	Canceled      bool                 `json:"canceled,omitempty"`
	CurrentStep   string               `json:"currentStep,omitempty"`
	CurrentAction string               `json:"currentAction,omitempty"`
	ResultLine    string               `json:"resultLine,omitempty"`
	AwaitingHuman bool                 `json:"awaitingHuman,omitempty"`
	Approval      ApprovalState        `json:"approval"`
	Rollback      RollbackState        `json:"rollback"`
	Operation     host.OperationResult `json:"operation"`
}

// Runner executes an ExecutionPlan serially using one provider.
// SchemaResolver supplies an action's output schema to the runner.
type SchemaResolver interface {
	OutputSchema(actionID string) (map[string]any, bool)
}

// SchemaResolverFunc adapts a function to SchemaResolver.
type SchemaResolverFunc func(actionID string) (map[string]any, bool)

func (f SchemaResolverFunc) OutputSchema(actionID string) (map[string]any, bool) {
	return f(actionID)
}

// RunnerOption configures a Runner while preserving the historical constructor.
type RunnerOption func(*Runner)

// RollbackProvider is implemented by providers that can compensate a
// previously successful action. A missing implementation leaves rollback
// explicitly unknown instead of claiming that state was restored.
type RollbackProvider interface {
	Rollback(context.Context, ActionInvocation, ActionResult) (ActionResult, error)
}

type ApprovalDecision string

const (
	ApprovalDecisionPending  ApprovalDecision = "pending"
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionRejected ApprovalDecision = "rejected"
)

type ApprovalRequest struct {
	StepID   string `json:"stepId"`
	ActionID string `json:"actionId"`
	Reason   string `json:"reason,omitempty"`
}

type ApprovalProvider interface {
	RequestApproval(context.Context, ApprovalRequest) (ApprovalDecision, error)
}

// WithSchemaResolver enables runtime output-schema validation.
func WithSchemaResolver(resolver SchemaResolver) RunnerOption {
	return func(r *Runner) { r.SchemaResolver = resolver }
}

// WithActionRegistry enables output-schema validation from action descriptors.
func WithActionRegistry(registry ActionRegistry) RunnerOption {
	return WithSchemaResolver(NewRegistrySchemaResolver(registry))
}

func WithRollbackProvider(provider RollbackProvider) RunnerOption {
	return func(r *Runner) { r.RollbackProvider = provider }
}

func WithApprovalProvider(provider ApprovalProvider) RunnerOption {
	return func(r *Runner) { r.ApprovalProvider = provider }
}

func WithGoal(goal string) RunnerOption {
	return func(r *Runner) { r.Goal = goal }
}

// Runner executes an ExecutionPlan serially using one provider.
type Runner struct {
	Provider         Provider
	SchemaResolver   SchemaResolver
	RollbackProvider RollbackProvider
	ApprovalProvider ApprovalProvider
	Goal             string
}

// NewRunner constructs a provider-independent workflow runner.
func NewRunner(provider Provider, options ...RunnerOption) *Runner {
	runner := &Runner{Provider: provider}
	for _, option := range options {
		if option != nil {
			option(runner)
		}
	}
	return runner
}

// Run executes plan in order. The returned error is reserved for runner-level
// failures (missing provider and context cancellation). Provider and template
// failures are represented by failed StepResults, allowing FailureContinue to
// do its job without losing per-step diagnostics.
func (r *Runner) Run(ctx context.Context, plan ExecutionPlan, inputs map[string]any) (ExecutionResult, error) {
	return r.run(ctx, plan, inputs)
}

// Execute is an alias for Run for callers which use execution terminology.
func (r *Runner) Execute(ctx context.Context, plan ExecutionPlan, inputs map[string]any) (ExecutionResult, error) {
	return r.run(ctx, plan, inputs)
}

// Run executes a plan with provider. It is a convenience for callers that do
// not need to retain a Runner.
func Run(ctx context.Context, plan ExecutionPlan, provider Provider, inputs map[string]any) (ExecutionResult, error) {
	return NewRunner(provider).Run(ctx, plan, inputs)
}

func (r *Runner) run(ctx context.Context, plan ExecutionPlan, inputs map[string]any) (result ExecutionResult, runErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.Provider == nil {
		return ExecutionResult{}, errors.New("automation: provider is nil")
	}

	result = ExecutionResult{
		Goal:      r.Goal,
		Status:    ExecutionPending,
		Steps:     make([]StepResult, 0, len(plan)),
		Outputs:   make(map[string]any),
		Approval:  ApprovalState{Status: ApprovalNotRequired},
		Rollback:  RollbackState{Status: RollbackNotRequested},
		Operation: host.OperationResult{State: host.StateRefreshing, Code: "execution_pending"},
	}
	// internalOutputs includes discarded values because output bindings are an
	// execution concern. Values are copied before being retained to prevent a
	// provider from mutating the data observed by later steps.
	internalOutputs := make(map[string]any, len(plan))
	// Discarded output remains available to internal bindings, but it must never
	// be observable through the returned result (including a callback or JSON
	// log). Keep the values only for the duration of this run so the public
	// result can be redacted wherever a later step echoed one.
	var discardedValues []any
	defer func() {
		redactExecutionResult(&result, discardedValues)
	}()

	for index, step := range plan {
		result.Status = ExecutionRunning
		result.CurrentStep = step.ID
		result.CurrentAction = stepAction(step)
		if err := ctx.Err(); err != nil {
			result.Canceled = true
			result.Status = ExecutionCanceled
			result.Operation = host.OperationResult{State: host.StateUnknown, Code: "cancelled", Message: "execution was cancelled", Retryable: false}
			appendSkipped(&result, plan[index:], err)
			return result, err
		}

		actionID := stepAction(step)
		if step.RequiresApproval {
			request := ApprovalRequest{StepID: step.ID, ActionID: actionID, Reason: "approval required"}
			decision := ApprovalDecisionPending
			var approvalErr error
			if r.ApprovalProvider != nil {
				decision, approvalErr = r.ApprovalProvider.RequestApproval(ctx, request)
			}
			if approvalErr != nil {
				result.Status = ExecutionUnknown
				result.Approval = ApprovalState{Status: ApprovalUnknown, StepID: step.ID, Reason: approvalErr.Error(), RequestedAt: time.Now().UTC()}
				result.Operation = host.OperationResult{State: host.StateUnknown, Code: "approval_unknown", Message: approvalErr.Error(), Retryable: false, ObservedAt: time.Now().UTC()}
				result.Steps = append(result.Steps, StepResult{StepID: step.ID, ActionID: actionID, Status: StepUnknown, Invocation: ActionInvocation{ActionID: actionID}, Result: ActionResult{ActionID: actionID, Status: ResultUnknown, Error: approvalErr.Error()}})
				return result, nil
			}
			if decision == ApprovalDecisionRejected {
				result.Status = ExecutionFailed
				result.Approval = ApprovalState{Status: ApprovalRejected, StepID: step.ID, Reason: "approval rejected", RequestedAt: time.Now().UTC(), DecidedAt: time.Now().UTC()}
				result.Operation = host.OperationResult{State: host.StateFailed, Code: "approval_rejected", Message: "approval was rejected", Retryable: false, ObservedAt: time.Now().UTC()}
				result.Steps = append(result.Steps, StepResult{StepID: step.ID, ActionID: actionID, Status: StepFailed, Invocation: ActionInvocation{ActionID: actionID}, Result: ActionResult{ActionID: actionID, Status: ResultFailed, Error: "approval was rejected"}})
				return result, nil
			}
			if decision != ApprovalDecisionApproved {
				result.Status = ExecutionAwaitingHuman
				result.AwaitingHuman = true
				result.Approval = ApprovalState{Status: ApprovalPending, StepID: step.ID, Reason: "approval required", RequestedAt: time.Now().UTC()}
				result.Operation = host.OperationResult{State: host.StateDegraded, Code: "awaiting_human", Message: "approval is required before execution", Retryable: false, ObservedAt: time.Now().UTC()}
				result.ResultLine = "Awaiting human approval"
				result.Steps = append(result.Steps, StepResult{StepID: step.ID, ActionID: actionID, Status: StepAwaitingHuman, Invocation: ActionInvocation{ActionID: actionID}, Result: ActionResult{ActionID: actionID, Status: ResultAwaitingHuman, Error: "approval required"}})
				return result, nil
			}
			result.Approval = ApprovalState{Status: ApprovalApproved, StepID: step.ID, Reason: "approved", RequestedAt: time.Now().UTC(), DecidedAt: time.Now().UTC()}
		}
		rawInput := step.Input
		if rawInput == nil {
			rawInput = step.Inputs
		}
		var err error
		for _, required := range step.RequiredInputs {
			if isEmptyWorkflowInput(inputs[required]) {
				err = fmt.Errorf("required workflow input %q is missing", required)
				break
			}
		}
		var boundInput any
		if err == nil {
			boundInput, err = bindValue(rawInput, inputs, internalOutputs)
		}
		invocation := ActionInvocation{ActionID: actionID, Input: asMap(boundInput)}
		if step.Inputs != nil {
			invocation.Inputs = invocation.Input
		}

		if err != nil {
			stepResult := failedStep(step, invocation, err)
			result.Steps = append(result.Steps, stepResult)
			if step.FailureMode == FailureStop {
				result.Stopped = true
				result.Status = ExecutionFailed
				result.Operation = host.OperationResult{State: host.StateFailed, Code: "step_failed", Message: err.Error(), Retryable: false, ObservedAt: time.Now().UTC()}
				appendSkipped(&result, plan[index+1:], errStepStop)
				return result, nil
			}
			continue
		}

		actionResult, invokeErr := r.Provider.Invoke(ctx, invocation)
		if actionResult.ActionID == "" {
			actionResult.ActionID = actionID
		}
		if invokeErr != nil {
			actionResult.Status = ResultFailed
			if actionResult.Error == "" {
				actionResult.Error = invokeErr.Error()
			}
			if actionResult.ErrorCategory == "" {
				actionResult.ErrorCategory = ErrorCategory(invokeErr)
			}
		} else if actionResult.Status == "" {
			actionResult.Status = ResultSucceeded
		}
		if actionResult.Status == ResultFailed && actionResult.ErrorCategory == "" && actionResult.Error != "" {
			actionResult.ErrorCategory = ErrorCategory(errors.New(actionResult.Error))
		}
		if actionResult.Status != ResultSucceeded && actionResult.Status != ResultFailed && actionResult.Status != ResultSkipped && actionResult.Status != ResultAwaitingHuman && actionResult.Status != ResultUnknown && actionResult.Status != ResultRolledBack {
			actionResult.Status = ResultUnknown
			actionResult.ErrorCategory = "unknown_result"
			if actionResult.Error == "" {
				actionResult.Error = "provider returned an unknown result status"
			}
		}
		if actionResult.Status == ResultSucceeded && r.SchemaResolver != nil {
			if schema, found := r.SchemaResolver.OutputSchema(actionID); found {
				if err := jsonschema.ValidateDefinition(schema); err != nil {
					actionResult.Status = ResultFailed
					actionResult.ErrorCategory = ErrorCategoryOutputSchema
					actionResult.Error = fmt.Sprintf("automation: action %q output schema definition invalid: %v", actionID, err)
					actionResult.Output = nil
				} else if err := jsonschema.Validate(actionResult.Output, schema); err != nil {
					actionResult.Status = ResultFailed
					actionResult.ErrorCategory = ErrorCategoryOutputSchema
					actionResult.Error = fmt.Sprintf("automation: action %q output schema validation failed: %v", actionID, err)
					actionResult.Output = nil
				}
			}
		}
		stepResult := StepResult{StepID: step.ID, ActionID: actionID, Status: stepStatusForResult(actionResult.Status), Invocation: invocation, Result: cloneActionResult(actionResult)}
		if actionResult.Status == ResultSucceeded && step.OutputMode == OutputDiscard {
			discardedValues = append(discardedValues, cloneValue(actionResult.Output))
			// Keep discarded output only in internalOutputs. The deferred
			// redaction also covers later invocations/results that echo it.
			stepResult.Result.Output = nil
		}
		result.Steps = append(result.Steps, stepResult)
		if actionResult.Status == ResultAwaitingHuman {
			result.Status = ExecutionAwaitingHuman
			result.AwaitingHuman = true
			result.Approval = ApprovalState{Status: ApprovalPending, StepID: step.ID, Reason: actionResult.Error, RequestedAt: time.Now().UTC()}
			result.Operation = host.OperationResult{State: host.StateDegraded, Code: "awaiting_human", Message: actionResult.Error, Retryable: false, ObservedAt: time.Now().UTC()}
			result.ResultLine = "Awaiting human approval"
			return result, nil
		}

		if actionResult.Status == ResultSucceeded {
			internalOutputs[step.ID] = cloneValue(actionResult.Output)
			if step.OutputMode != OutputDiscard {
				result.Outputs[step.ID] = cloneValue(actionResult.Output)
			}
		}
		if err := ctx.Err(); err != nil {
			result.Canceled = true
			result.Status = ExecutionCanceled
			result.Operation = host.OperationResult{State: host.StateUnknown, Code: "cancelled", Message: "execution was cancelled", Retryable: false, ObservedAt: time.Now().UTC()}
			appendSkipped(&result, plan[index+1:], err)
			return result, err
		}
		if (actionResult.Status == ResultFailed || actionResult.Status == ResultUnknown) && step.FailureMode == FailureStop {
			result.Stopped = true
			if actionResult.Status == ResultUnknown {
				result.Status = ExecutionUnknown
				result.Operation = host.OperationResult{State: host.StateUnknown, Code: "unknown_result", Message: actionResult.Error, Retryable: false, ObservedAt: time.Now().UTC()}
			} else {
				result.Status = ExecutionFailed
				result.Operation = host.OperationResult{State: host.StateFailed, Code: "step_failed", Message: actionResult.Error, Retryable: false, ObservedAt: time.Now().UTC()}
			}
			r.applyRollback(ctx, &result, plan[:index])
			appendSkipped(&result, plan[index+1:], errStepStop)
			return result, nil
		}
	}
	result.Status = ExecutionSucceeded
	result.Operation = host.OperationResult{State: host.StateReady, Code: "execution_succeeded", Message: "execution completed", Retryable: false, ObservedAt: time.Now().UTC()}
	for _, step := range result.Steps {
		switch step.Result.Status {
		case ResultUnknown:
			result.Status = ExecutionUnknown
			result.Operation = host.OperationResult{State: host.StateUnknown, Code: "unknown_result", Message: step.Result.Error, Retryable: false, ObservedAt: time.Now().UTC()}
		case ResultFailed:
			if result.Status == ExecutionSucceeded {
				result.Status = ExecutionFailed
				result.Operation = host.OperationResult{State: host.StateFailed, Code: "step_failed", Message: step.Result.Error, Retryable: false, ObservedAt: time.Now().UTC()}
			}
		}
	}
	switch result.Status {
	case ExecutionSucceeded:
		result.ResultLine = "Execution completed"
	case ExecutionFailed:
		result.ResultLine = "Execution completed with failures"
	case ExecutionUnknown:
		result.ResultLine = "Execution outcome is unknown"
	}
	return result, nil
}

var templatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

var errStepStop = errors.New("skipped because a previous step failed")

func stepAction(step WorkflowStep) string {
	if step.ActionID != "" {
		return step.ActionID
	}
	return step.Action
}

func stepStatusForResult(status ResultStatus) StepStatus {
	switch status {
	case ResultSucceeded:
		return StepSucceeded
	case ResultFailed:
		return StepFailed
	case ResultSkipped:
		return StepSkipped
	case ResultAwaitingHuman:
		return StepAwaitingHuman
	case ResultRolledBack:
		return StepRolledBack
	default:
		return StepUnknown
	}
}

func (r *Runner) applyRollback(ctx context.Context, result *ExecutionResult, plan ExecutionPlan) {
	var candidates []struct {
		step   WorkflowStep
		result ActionResult
	}
	for i := len(plan) - 1; i >= 0; i-- {
		step := plan[i]
		if step.RollbackActionID == "" {
			continue
		}
		for _, completed := range result.Steps {
			if completed.StepID == step.ID && completed.Result.Status == ResultSucceeded {
				candidates = append(candidates, struct {
					step   WorkflowStep
					result ActionResult
				}{step: step, result: completed.Result})
				break
			}
		}
	}
	if len(candidates) == 0 {
		return
	}
	result.Rollback = RollbackState{Status: RollbackRunning}
	if r.RollbackProvider == nil {
		result.Rollback.Status = RollbackUnknown
		result.Rollback.Error = "rollback provider is not configured"
		result.Status = ExecutionUnknown
		result.Operation = host.OperationResult{State: host.StateUnknown, Code: "rollback_unknown", Message: result.Rollback.Error, Retryable: false, ObservedAt: time.Now().UTC()}
		return
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			result.Rollback.Status = RollbackUnknown
			result.Rollback.Error = err.Error()
			result.Status = ExecutionUnknown
			result.Operation = host.OperationResult{State: host.StateUnknown, Code: "rollback_cancelled", Message: err.Error(), Retryable: false, ObservedAt: time.Now().UTC()}
			return
		}
		invocation := ActionInvocation{ActionID: candidate.step.RollbackActionID, Metadata: map[string]any{"rollbackOf": candidate.step.ID}}
		actionResult, err := r.RollbackProvider.Rollback(ctx, invocation, candidate.result)
		if actionResult.ActionID == "" {
			actionResult.ActionID = candidate.step.RollbackActionID
		}
		if err != nil {
			actionResult.Status = ResultFailed
			if actionResult.Error == "" {
				actionResult.Error = err.Error()
			}
		}
		if actionResult.Status == "" {
			actionResult.Status = ResultSucceeded
		}
		stepResult := StepResult{StepID: "rollback:" + candidate.step.ID, ActionID: candidate.step.RollbackActionID, Status: stepStatusForResult(actionResult.Status), Invocation: invocation, Result: cloneActionResult(actionResult)}
		result.Rollback.Steps = append(result.Rollback.Steps, stepResult)
		if actionResult.Status != ResultSucceeded {
			result.Rollback.Status = RollbackFailed
			result.Rollback.Error = actionResult.Error
			result.Status = ExecutionFailed
			result.Operation = host.OperationResult{State: host.StateDegraded, Code: "rollback_failed", Message: actionResult.Error, Retryable: true, ObservedAt: time.Now().UTC()}
			return
		}
	}
	result.Rollback.Status = RollbackSucceeded
	result.Status = ExecutionRolledBack
	result.Operation = host.OperationResult{State: host.StateDegraded, Code: "rolled_back", Message: "execution failed and rollback completed", Retryable: false, ObservedAt: time.Now().UTC()}
	result.ResultLine = "Execution failed and was rolled back"
}

func failedStep(step WorkflowStep, invocation ActionInvocation, err error) StepResult {
	return StepResult{
		StepID: step.ID, ActionID: invocation.ActionID, Status: StepFailed, Invocation: invocation,
		Result: ActionResult{ActionID: invocation.ActionID, Status: ResultFailed, Error: err.Error(), ErrorCategory: ErrorCategory(err)},
	}
}

func appendSkipped(result *ExecutionResult, steps []WorkflowStep, cause error) {
	for _, step := range steps {
		actionID := step.ActionID
		if actionID == "" {
			actionID = step.Action
		}
		errorText := cause.Error()
		result.Steps = append(result.Steps, StepResult{
			StepID: step.ID, ActionID: actionID,
			Status:     StepSkipped,
			Invocation: ActionInvocation{ActionID: actionID},
			Result:     ActionResult{ActionID: actionID, Status: ResultSkipped, Error: errorText},
		})
	}
}

func isEmptyWorkflowInput(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return nil
}

func bindValue(value any, inputs map[string]any, outputs map[string]any) (any, error) {
	switch value := value.(type) {
	case string:
		return bindString(value, inputs, outputs)
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		bound := make(map[string]any, len(value))
		for _, key := range keys {
			item, err := bindValue(value[key], inputs, outputs)
			if err != nil {
				return nil, err
			}
			bound[key] = item
		}
		return bound, nil
	case []any:
		bound := make([]any, len(value))
		for i, item := range value {
			var err error
			bound[i], err = bindValue(item, inputs, outputs)
			if err != nil {
				return nil, err
			}
		}
		return bound, nil
	default:
		return cloneValue(value), nil
	}
}

func bindString(value string, inputs map[string]any, outputs map[string]any) (any, error) {
	matches := templatePattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, nil
	}
	// Preserve the source type when the whole value is one binding, which makes
	// {{input.options}} useful for structured JSON-compatible input.
	if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(value) {
		resolved, err := resolveTemplate(strings.TrimSpace(value[matches[0][2]:matches[0][3]]), inputs, outputs)
		if err != nil {
			return nil, err
		}
		return cloneValue(resolved), nil
	}
	var builder strings.Builder
	last := 0
	for _, match := range matches {
		builder.WriteString(value[last:match[0]])
		resolved, err := resolveTemplate(strings.TrimSpace(value[match[2]:match[3]]), inputs, outputs)
		if err != nil {
			return nil, err
		}
		builder.WriteString(fmt.Sprint(resolved))
		last = match[1]
	}
	builder.WriteString(value[last:])
	return builder.String(), nil
}

func resolveTemplate(expression string, inputs map[string]any, outputs map[string]any) (any, error) {
	parts := strings.Split(expression, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("automation: unsupported template %q", expression)
	}
	var value any
	switch parts[0] {
	case "input":
		var ok bool
		value, ok = inputs[parts[1]]
		if !ok {
			return nil, fmt.Errorf("automation: input %q is not defined", parts[1])
		}
		parts = parts[2:]
	case "step":
		if len(parts) < 3 || parts[2] != "output" {
			return nil, fmt.Errorf("automation: unsupported template %q", expression)
		}
		var ok bool
		value, ok = outputs[parts[1]]
		if !ok {
			return nil, fmt.Errorf("automation: output for step %q is not defined", parts[1])
		}
		parts = parts[3:]
	default:
		return nil, fmt.Errorf("automation: unsupported template %q", expression)
	}
	for _, part := range parts {
		var ok bool
		value, ok = lookupPath(value, part)
		if !ok {
			return nil, fmt.Errorf("automation: value %q is not defined in template %q", part, expression)
		}
	}
	return value, nil
}

func lookupPath(value any, key string) (any, bool) {
	if object, ok := value.(map[string]any); ok {
		item, found := object[key]
		return item, found
	}
	valueOf := reflect.ValueOf(value)
	if valueOf.IsValid() && valueOf.Kind() == reflect.Map && valueOf.Type().Key().Kind() == reflect.String {
		item := valueOf.MapIndex(reflect.ValueOf(key).Convert(valueOf.Type().Key()))
		if item.IsValid() {
			return item.Interface(), true
		}
	}
	return nil, false
}

func cloneActionResult(result ActionResult) ActionResult {
	result.Output = cloneValue(result.Output)
	if result.Metadata != nil {
		result.Metadata = cloneValue(result.Metadata).(map[string]any)
	}
	return result
}

const redactedOutput = "[REDACTED]"

func redactExecutionResult(result *ExecutionResult, discarded []any) {
	if result == nil || len(discarded) == 0 {
		return
	}
	for i := range result.Steps {
		result.Steps[i].Invocation = redactInvocation(result.Steps[i].Invocation, discarded)
		result.Steps[i].Result = redactActionResult(result.Steps[i].Result, discarded)
	}
	for key, value := range result.Outputs {
		result.Outputs[key] = redactValue(value, discarded)
	}
}

func redactInvocation(invocation ActionInvocation, discarded []any) ActionInvocation {
	invocation.Input = redactMap(invocation.Input, discarded)
	invocation.Inputs = redactMap(invocation.Inputs, discarded)
	invocation.Metadata = redactMap(invocation.Metadata, discarded)
	return invocation
}

func redactActionResult(result ActionResult, discarded []any) ActionResult {
	result.Output = redactValue(result.Output, discarded)
	result.Error = redactString(result.Error, discarded)
	result.Metadata = redactMap(result.Metadata, discarded)
	return result
}

func redactMap(value map[string]any, discarded []any) map[string]any {
	if value == nil {
		return nil
	}
	redacted := make(map[string]any, len(value))
	for key, item := range value {
		redacted[key] = redactValue(item, discarded)
	}
	return redacted
}

func redactValue(value any, discarded []any) any {
	if value == nil {
		return nil
	}
	for _, secret := range discarded {
		if reflect.DeepEqual(value, secret) {
			return redactedOutput
		}
	}
	return redactReflect(reflect.ValueOf(value), discarded).Interface()
}

// redactReflect walks values recursively rather than only handling the
// untyped map/slice shapes produced by encoding/json. Providers may return
// typed maps, arrays, slices, and JSON-compatible structs; all of them can be
// echoed through a later invocation and must be scrubbed from the public
// result. A replacement is allowed to change the containing value to a string
// when the original type cannot represent the redaction marker.
func redactReflect(value reflect.Value, discarded []any) reflect.Value {
	if !value.IsValid() {
		return value
	}
	// Check the complete value before walking it. This matters for typed
	// numbers, []byte, arrays, and structs: their elements cannot be replaced
	// with the marker without either leaking the value or changing its type.
	if value.CanInterface() && matchesRedactionAtom(value.Interface(), discarded) {
		return replacementValue(value.Type())
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		return valueSafeValue(redactReflect(value.Elem(), discarded), value.Type())
	}
	if value.CanInterface() && isRedactionScalar(value.Kind()) {
		for _, secret := range discarded {
			if reflect.DeepEqual(value.Interface(), secret) {
				return replacementValue(value.Type())
			}
		}
	}
	switch value.Kind() {
	case reflect.String:
		redacted := redactString(value.String(), discarded)
		return reflect.ValueOf(redacted).Convert(value.Type())
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key := redactReflect(iter.Key(), discarded)
			key = valueSafeValue(key, value.Type().Key())
			item := redactReflect(iter.Value(), discarded)
			if !item.IsValid() || !item.Type().AssignableTo(value.Type().Elem()) {
				item = valueSafeValue(item, value.Type().Elem())
			}
			result.SetMapIndex(key, item)
		}
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(valueSafeValue(redactReflect(value.Index(i), discarded), value.Type().Elem()))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(valueSafeValue(redactReflect(value.Index(i), discarded), value.Type().Elem()))
		}
		return result
	case reflect.Struct:
		result := reflect.New(value.Type()).Elem()
		result.Set(value)
		for i := 0; i < value.NumField(); i++ {
			if result.Field(i).CanSet() && value.Field(i).CanInterface() {
				result.Field(i).Set(valueSafeValue(redactReflect(value.Field(i), discarded), value.Type().Field(i).Type))
			}
		}
		return result
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(valueSafeValue(redactReflect(value.Elem(), discarded), value.Type().Elem()))
		return result
	default:
		return value
	}
}

func isRedactionScalar(kind reflect.Kind) bool {
	switch kind {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String:
		return true
	default:
		return false
	}
}

func replacementValue(typ reflect.Type) reflect.Value {
	replacement := reflect.ValueOf(redactedOutput)
	if replacement.Type().AssignableTo(typ) {
		return replacement
	}
	if replacement.Type().ConvertibleTo(typ) {
		return replacement.Convert(typ)
	}
	return reflect.Zero(typ)
}

func valueSafeValue(value reflect.Value, typ reflect.Type) reflect.Value {
	if !value.IsValid() {
		return reflect.Zero(typ)
	}
	if typ.Kind() == reflect.Interface {
		if value.Type().AssignableTo(typ) {
			wrapped := reflect.New(typ).Elem()
			wrapped.Set(value)
			return wrapped
		}
		return reflect.Zero(typ)
	}
	if value.Type().AssignableTo(typ) {
		return value
	}
	if value.Type().ConvertibleTo(typ) {
		return value.Convert(typ)
	}
	return reflect.Zero(typ)
}

func redactString(value string, discarded []any) string {
	for _, secret := range discarded {
		for _, token := range redactionTokens(secret) {
			value = strings.ReplaceAll(value, token, redactedOutput)
		}
	}
	return value
}

func matchesRedactionAtom(value any, discarded []any) bool {
	for _, secret := range discarded {
		var matched bool
		visitRedactionAtoms(secret, func(atom any) {
			if reflect.DeepEqual(value, atom) {
				matched = true
			}
		})
		if matched {
			return true
		}
	}
	return false
}

// visitRedactionAtoms collects exact values for typed scalar/byte values. It
// deliberately does not stringify numbers: substring replacement of a short
// value such as 1 would corrupt unrelated values such as 100.
func visitRedactionAtoms(value any, visit func(any)) {
	var walk func(reflect.Value)
	walk = func(item reflect.Value) {
		if !item.IsValid() {
			return
		}
		if item.Kind() == reflect.Interface {
			if !item.IsNil() {
				walk(item.Elem())
			}
			return
		}
		if item.CanInterface() && isRedactionScalar(item.Kind()) {
			visit(item.Interface())
			return
		}
		if item.Kind() == reflect.Slice && item.Type().Elem().Kind() == reflect.Uint8 {
			if item.CanInterface() {
				visit(item.Interface())
			}
			return
		}
		switch item.Kind() {
		case reflect.Map:
			if item.IsNil() {
				return
			}
			iter := item.MapRange()
			for iter.Next() {
				walk(iter.Key())
				walk(iter.Value())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < item.Len(); i++ {
				walk(item.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < item.NumField(); i++ {
				walk(item.Field(i))
			}
		case reflect.Pointer:
			if !item.IsNil() {
				walk(item.Elem())
			}
		}
	}
	walk(reflect.ValueOf(value))
}

func redactionTokens(value any) []string {
	if value == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var tokens []string
	add := func(token string) {
		// Numeric and byte values are redacted by exact DeepEqual checks. Never
		// turn them into substring tokens ("1" would corrupt "100"). Short
		// strings are likewise too ambiguous to safely redact in free-form text.
		if len([]rune(token)) < 4 {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	var visit func(reflect.Value)
	visit = func(item reflect.Value) {
		if !item.IsValid() {
			return
		}
		if item.Kind() == reflect.Interface {
			if item.IsNil() {
				return
			}
			visit(item.Elem())
			return
		}
		if item.Kind() == reflect.String {
			add(item.String())
			return
		}
		// []byte is a common typed output for encoded secrets. It is safe to
		// use its textual form only when it is long enough to be unambiguous;
		// individual numeric bytes must never become redaction tokens.
		if item.Kind() == reflect.Slice && item.Type().Elem().Kind() == reflect.Uint8 && !item.IsNil() {
			if item.CanInterface() {
				add(string(item.Bytes()))
			}
			return
		}
		switch item.Kind() {
		case reflect.Map:
			if item.IsNil() {
				return
			}
			iter := item.MapRange()
			for iter.Next() {
				// Keys can carry discarded identifiers too; numeric keys are
				// intentionally ignored by add to avoid substring corruption.
				visit(iter.Key())
				visit(iter.Value())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < item.Len(); i++ {
				visit(item.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < item.NumField(); i++ {
				visit(item.Field(i))
			}
		case reflect.Pointer:
			if !item.IsNil() {
				visit(item.Elem())
			}
		}
	}
	visit(reflect.ValueOf(value))
	return tokens
}

func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	return cloneReflect(reflect.ValueOf(value)).Interface()
}

func cloneReflect(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		return valueSafeValue(cloneReflect(value.Elem()), value.Type())
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			result.SetMapIndex(iter.Key(), valueSafeValue(cloneReflect(iter.Value()), value.Type().Elem()))
		}
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(valueSafeValue(cloneReflect(value.Index(i)), value.Type().Elem()))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(valueSafeValue(cloneReflect(value.Index(i)), value.Type().Elem()))
		}
		return result
	case reflect.Struct:
		result := reflect.New(value.Type()).Elem()
		result.Set(value)
		for i := 0; i < value.NumField(); i++ {
			if result.Field(i).CanSet() && value.Field(i).CanInterface() {
				result.Field(i).Set(valueSafeValue(cloneReflect(value.Field(i)), value.Type().Field(i).Type))
			}
		}
		return result
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(valueSafeValue(cloneReflect(value.Elem()), value.Type().Elem()))
		return result
	default:
		return value
	}
}
