package automation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
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
	Invocation ActionInvocation `json:"invocation"`
	Result     ActionResult     `json:"result"`
}

// ExecutionResult contains every step in the plan, in deterministic plan
// order. Outputs contains successful step outputs by step ID. A separate map
// is intentionally not exposed for discarded outputs: discarded output may be
// used by later bindings during this run, but is not a workflow result.
type ExecutionResult struct {
	Steps    []StepResult   `json:"steps"`
	Outputs  map[string]any `json:"outputs,omitempty"`
	Stopped  bool           `json:"stopped,omitempty"`
	Canceled bool           `json:"canceled,omitempty"`
}

// Runner executes an ExecutionPlan serially using one provider.
type Runner struct {
	Provider Provider
}

// NewRunner constructs a provider-independent workflow runner.
func NewRunner(provider Provider) *Runner { return &Runner{Provider: provider} }

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
		Steps:   make([]StepResult, 0, len(plan)),
		Outputs: make(map[string]any),
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
		if err := ctx.Err(); err != nil {
			result.Canceled = true
			appendSkipped(&result, plan[index:], err)
			return result, err
		}

		actionID := step.ActionID
		if actionID == "" {
			actionID = step.Action
		}
		rawInput := step.Input
		if rawInput == nil {
			rawInput = step.Inputs
		}
		boundInput, err := bindValue(rawInput, inputs, internalOutputs)
		invocation := ActionInvocation{ActionID: actionID, Input: asMap(boundInput)}
		if step.Inputs != nil {
			invocation.Inputs = invocation.Input
		}

		if err != nil {
			stepResult := failedStep(step, invocation, err)
			result.Steps = append(result.Steps, stepResult)
			if step.FailureMode == FailureStop {
				result.Stopped = true
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
		} else if actionResult.Status == "" {
			actionResult.Status = ResultSucceeded
		}
		if actionResult.Status != ResultSucceeded && actionResult.Status != ResultFailed && actionResult.Status != ResultSkipped {
			actionResult.Status = ResultFailed
			if actionResult.Error == "" {
				actionResult.Error = "provider returned an invalid result status"
			}
		}
		stepResult := StepResult{StepID: step.ID, ActionID: actionID, Invocation: invocation, Result: cloneActionResult(actionResult)}
		if actionResult.Status == ResultSucceeded && step.OutputMode == OutputDiscard {
			discardedValues = append(discardedValues, cloneValue(actionResult.Output))
			// Keep discarded output only in internalOutputs. The deferred
			// redaction also covers later invocations/results that echo it.
			stepResult.Result.Output = nil
		}
		result.Steps = append(result.Steps, stepResult)

		if actionResult.Status == ResultSucceeded {
			internalOutputs[step.ID] = cloneValue(actionResult.Output)
			if step.OutputMode != OutputDiscard {
				result.Outputs[step.ID] = cloneValue(actionResult.Output)
			}
		}
		if err := ctx.Err(); err != nil {
			result.Canceled = true
			appendSkipped(&result, plan[index+1:], err)
			return result, err
		}
		if actionResult.Status == ResultFailed && step.FailureMode == FailureStop {
			result.Stopped = true
			appendSkipped(&result, plan[index+1:], errStepStop)
			return result, nil
		}
	}
	return result, nil
}

var templatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

var errStepStop = errors.New("skipped because a previous step failed")

func failedStep(step WorkflowStep, invocation ActionInvocation, err error) StepResult {
	return StepResult{
		StepID: step.ID, ActionID: invocation.ActionID, Invocation: invocation,
		Result: ActionResult{ActionID: invocation.ActionID, Status: ResultFailed, Error: err.Error()},
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
			Invocation: ActionInvocation{ActionID: actionID},
			Result:     ActionResult{ActionID: actionID, Status: ResultSkipped, Error: errorText},
		})
	}
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
			key := iter.Key()
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

func redactionTokens(value any) []string {
	if value == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var tokens []string
	add := func(token string) {
		if token == "" {
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
		if item.CanInterface() {
			add(fmt.Sprint(item.Interface()))
		}
		switch item.Kind() {
		case reflect.Map:
			if item.IsNil() {
				return
			}
			iter := item.MapRange()
			for iter.Next() {
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
