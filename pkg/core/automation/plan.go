package automation

import "sort"

// BuildExecutionPlan returns a stable topological ordering of workflow steps.
// Independent steps retain their declaration order, making the result
// deterministic while still respecting every dependency. It performs no
// execution and does not inspect action providers.
func BuildExecutionPlan(workflow WorkflowDefinition, registries ...ActionRegistry) (ExecutionPlan, error) {
	var registry ActionRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	return buildExecutionPlan(workflow, registry)
}

// BuildExecutionPlanWithRegistry validates action references using registry
// before producing the same deterministic serial plan.
func BuildExecutionPlanWithRegistry(workflow WorkflowDefinition, registry ActionRegistry) (ExecutionPlan, error) {
	return buildExecutionPlan(workflow, registry)
}

// ExecutionPlanBuilder builds plans with an optional action registry.
type ExecutionPlanBuilder struct {
	Registry ActionRegistry
}

// NewExecutionPlanBuilder returns a builder which checks action references when
// registry is non-nil.
func NewExecutionPlanBuilder(registry ActionRegistry) ExecutionPlanBuilder {
	return ExecutionPlanBuilder{Registry: registry}
}

// Build creates a deterministic serial plan.
func (b ExecutionPlanBuilder) Build(workflow WorkflowDefinition) (ExecutionPlan, error) {
	return buildExecutionPlan(workflow, b.Registry)
}

func buildExecutionPlan(workflow WorkflowDefinition, registry ActionRegistry) (ExecutionPlan, error) {
	issues := ValidateWorkflow(workflow, registry)
	if registry == nil {
		// A registry is optional for structural planning; no action can be
		// unknown when the caller intentionally did not supply one.
		filtered := issues[:0]
		for _, issue := range issues {
			if issue.Code != "unknown_action" {
				filtered = append(filtered, issue)
			}
		}
		issues = filtered
	}
	for _, input := range workflow.Inputs {
		if !input.Required || !isEmptyWorkflowInput(input.Default) {
			continue
		}
		// The runner receives values at execution time, but the requirement is
		// carried by the executable plan so non-UI callers cannot accidentally
		// bypass it.
		for stepIndex := range workflow.Steps {
			workflow.Steps[stepIndex].RequiredInputs = append(workflow.Steps[stepIndex].RequiredInputs, input.ID)
		}
	}
	if len(issues) != 0 {
		return nil, &ValidationError{Issues: issues}
	}

	indexByID := make(map[string]int, len(workflow.Steps))
	indegree := make([]int, len(workflow.Steps))
	dependents := make([][]int, len(workflow.Steps))
	for i, step := range workflow.Steps {
		indexByID[step.ID] = i
	}
	for i, step := range workflow.Steps {
		indegree[i] = len(step.DependsOn)
		for _, dependency := range step.DependsOn {
			dependents[indexByID[dependency]] = append(dependents[indexByID[dependency]], i)
		}
	}

	// A sorted ready queue is declaration-index ordered. Inserting by index
	// rather than relying on a map is what makes ties deterministic.
	ready := make([]int, 0, len(workflow.Steps))
	for i, degree := range indegree {
		if degree == 0 {
			ready = append(ready, i)
		}
	}
	plan := make(ExecutionPlan, 0, len(workflow.Steps))
	for len(ready) > 0 {
		index := ready[0]
		ready = ready[1:]
		plan = append(plan, workflow.Steps[index])
		for _, dependent := range dependents[index] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
		sort.SliceStable(ready, func(i, j int) bool { return ready[i] < ready[j] })
	}
	if len(plan) != len(workflow.Steps) {
		return nil, &ValidationError{Issues: []ValidationIssue{{Path: "steps", Code: "dependency_cycle", Message: "dependency graph contains a cycle"}}}
	}
	return plan, nil
}
