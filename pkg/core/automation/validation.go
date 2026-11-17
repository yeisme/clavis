package automation

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

// ValidationIssue identifies one invalid part of a workflow. Path is a stable
// JSON-like location suitable for displaying to a user or logging.
type ValidationIssue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidationError groups all issues found in one validation pass.
type ValidationError struct {
	Issues []ValidationIssue `json:"issues"`
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "workflow is invalid"
	}
	parts := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		parts[i] = issue.Path + ": " + issue.Message
	}
	return "workflow is invalid: " + strings.Join(parts, "; ")
}

// ValidateWorkflow validates a workflow and returns every issue in deterministic
// traversal order. registry may be a MapActionRegistry or an implementation
// exposing GetAction, LookupAction, Lookup, or FindAction.
func ValidateWorkflow(workflow WorkflowDefinition, registry ActionRegistry) []ValidationIssue {
	var issues []ValidationIssue
	addID := func(path, value string) {
		if !isValidID(value) {
			issues = append(issues, ValidationIssue{Path: path, Code: "invalid_id", Message: "must match " + identifierPattern.String()})
		}
	}

	addID("id", workflow.ID)
	inputIDs := make(map[string]int, len(workflow.Inputs))
	for i, input := range workflow.Inputs {
		path := fmt.Sprintf("inputs[%d].id", i)
		addID(path, input.ID)
		if previous, ok := inputIDs[input.ID]; ok {
			issues = append(issues, ValidationIssue{Path: path, Code: "duplicate_id", Message: fmt.Sprintf("duplicates inputs[%d].id", previous)})
		} else {
			inputIDs[input.ID] = i
		}
	}

	stepIDs := make(map[string]int, len(workflow.Steps))
	for i, step := range workflow.Steps {
		path := fmt.Sprintf("steps[%d].id", i)
		addID(path, step.ID)
		if previous, ok := stepIDs[step.ID]; ok {
			issues = append(issues, ValidationIssue{Path: path, Code: "duplicate_id", Message: fmt.Sprintf("duplicates steps[%d].id", previous)})
		} else {
			stepIDs[step.ID] = i
		}
	}

	for i, step := range workflow.Steps {
		stepPath := fmt.Sprintf("steps[%d]", i)
		actionID, conflict := stepActionID(step)
		if conflict {
			issues = append(issues, ValidationIssue{Path: stepPath, Code: "conflicting_action_id", Message: "actionId and action must name the same action"})
		}
		if !isValidID(actionID) {
			issues = append(issues, ValidationIssue{Path: stepPath + ".actionId", Code: "invalid_reference", Message: "action reference is required"})
		} else if registry != nil {
			if _, ok := lookupAction(registry, actionID); !ok {
				issues = append(issues, ValidationIssue{Path: stepPath + ".actionId", Code: "unknown_action", Message: fmt.Sprintf("action %q is not registered", actionID)})
			}
		}
		if !isFailureMode(step.FailureMode) {
			issues = append(issues, ValidationIssue{Path: stepPath + ".failureMode", Code: "invalid_failure_mode", Message: "must be stop or continue"})
		}
		if !isOutputMode(step.OutputMode) {
			issues = append(issues, ValidationIssue{Path: stepPath + ".outputMode", Code: "invalid_output_mode", Message: "must be discard, capture, replace, or notify"})
		}
		seenDeps := make(map[string]struct{}, len(step.DependsOn))
		for j, dependency := range step.DependsOn {
			depPath := fmt.Sprintf("%s.dependsOn[%d]", stepPath, j)
			if _, duplicate := seenDeps[dependency]; duplicate {
				issues = append(issues, ValidationIssue{Path: depPath, Code: "duplicate_dependency", Message: fmt.Sprintf("dependency %q is repeated", dependency)})
			} else {
				seenDeps[dependency] = struct{}{}
			}
			if _, ok := stepIDs[dependency]; !ok {
				issues = append(issues, ValidationIssue{Path: depPath, Code: "unknown_step", Message: fmt.Sprintf("step %q is not defined", dependency)})
			}
		}
	}
	issues = append(issues, cycleIssues(workflow.Steps, stepIDs)...)
	return issues
}

// ValidateWorkflowDefinition is a descriptive alias for ValidateWorkflow.
func ValidateWorkflowDefinition(workflow WorkflowDefinition, registry ActionRegistry) []ValidationIssue {
	return ValidateWorkflow(workflow, registry)
}

// Validate returns the same issues as ValidateWorkflow for callers that use
// the package-level validation convention.
func Validate(workflow WorkflowDefinition, registry ActionRegistry) []ValidationIssue {
	return ValidateWorkflow(workflow, registry)
}

// ValidateWorkflowE is the error-returning form of ValidateWorkflow.
func ValidateWorkflowE(workflow WorkflowDefinition, registry ActionRegistry) error {
	issues := ValidateWorkflow(workflow, registry)
	if len(issues) != 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

// IsValidID reports whether id is a non-empty, stable workflow identifier.
// IDs start with a letter and may contain letters, digits, underscores, dots,
// and hyphens.
func IsValidID(id string) bool {
	return identifierPattern.MatchString(id)
}

// ValidateID is an alias for IsValidID for callers that prefer validator naming.
func ValidateID(id string) bool { return IsValidID(id) }

func isValidID(id string) bool { return IsValidID(id) }

func isFailureMode(mode FailureMode) bool {
	return mode == FailureStop || mode == FailureContinue
}

func isOutputMode(mode OutputMode) bool {
	return mode == OutputDiscard || mode == OutputCapture || mode == OutputReplace || mode == OutputNotify
}

func stepActionID(step WorkflowStep) (string, bool) {
	if step.ActionID != "" && step.Action != "" && step.ActionID != step.Action {
		return step.ActionID, true
	}
	if step.ActionID != "" {
		return step.ActionID, false
	}
	return step.Action, false
}

func cycleIssues(steps []WorkflowStep, ids map[string]int) []ValidationIssue {
	state := make([]uint8, len(steps))
	var issues []ValidationIssue
	var visit func(int)
	visit = func(index int) {
		if state[index] == 2 {
			return
		}
		if state[index] == 1 {
			issues = append(issues, ValidationIssue{Path: fmt.Sprintf("steps[%d].dependsOn", index), Code: "dependency_cycle", Message: "dependency graph contains a cycle"})
			return
		}
		state[index] = 1
		for _, dependency := range steps[index].DependsOn {
			if depIndex, ok := ids[dependency]; ok {
				visit(depIndex)
			}
		}
		state[index] = 2
	}
	for i := range steps {
		visit(i)
	}
	return issues
}

// lookupAction supports the small set of conventional registry method names
// without making this model package depend on a provider implementation.
func lookupAction(registry ActionRegistry, id string) (ActionDescriptor, bool) {
	if registry == nil {
		return ActionDescriptor{}, false
	}
	switch r := registry.(type) {
	case MapActionRegistry:
		return r.GetAction(id)
	case map[string]ActionDescriptor:
		d, ok := r[id]
		return d, ok
	case interface {
		GetAction(string) (ActionDescriptor, bool)
	}:
		return r.GetAction(id)
	case interface {
		LookupAction(string) (ActionDescriptor, bool)
	}:
		return r.LookupAction(id)
	case interface {
		Lookup(string) (ActionDescriptor, bool)
	}:
		return r.Lookup(id)
	case interface {
		Get(string) (ActionDescriptor, bool)
	}:
		return r.Get(id)
	case interface {
		FindAction(string) (ActionDescriptor, bool)
	}:
		return r.FindAction(id)
	case interface {
		Descriptor(string) (ActionDescriptor, bool)
	}:
		return r.Descriptor(id)
	}
	// A pointer to a map alias is occasionally convenient to callers.
	value := reflect.ValueOf(registry)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return ActionDescriptor{}, false
		}
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Map && value.Type().Key().Kind() == reflect.String {
		entry := value.MapIndex(reflect.ValueOf(id).Convert(value.Type().Key()))
		if entry.IsValid() && entry.Type() == reflect.TypeOf(ActionDescriptor{}) {
			return entry.Interface().(ActionDescriptor), true
		}
	}
	return ActionDescriptor{}, false
}
