package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func workflowIssue(issues []config.ValidationIssue, keyPath string) bool {
	for _, issue := range issues {
		if issue.KeyPath == keyPath && issue.Severity == config.SeverityError {
			return true
		}
	}
	return false
}

func TestWorkflowsConfigRoundTrip(t *testing.T) {
	want := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "polish", Name: "Polish text", Description: "A test workflow", Enabled: true,
		Source: config.WorkflowSourceSelection, Output: config.WorkflowOutputReplace,
		Failure: config.WorkflowFailureStop, Execution: config.WorkflowExecutionSequential,
		Inputs: []config.WorkflowInput{{ID: "tone", Name: "Tone", Type: "text", Default: "concise", Required: true}},
		Steps: []config.WorkflowStep{
			{ID: "draft", Name: "Draft", Action: "prompt", Prompt: "Draft {{text}}", DependsOn: []string{}, Enabled: true,
				Source: config.WorkflowSourceSelection, Output: config.WorkflowOutputInsert,
				Failure: config.WorkflowFailureStop, Execution: config.WorkflowExecutionSequential},
			{ID: "copy", Name: "Copy", Action: "copy", DependsOn: []string{"draft"}, Enabled: false,
				Source: config.WorkflowSourceSelection, Output: config.WorkflowOutputClipboard,
				Failure: config.WorkflowFailureContinue, Execution: config.WorkflowExecutionParallel},
		},
	}}}

	encoded := want.Encode()
	if !strings.Contains(encoded, "[[workflows]]") || !strings.Contains(encoded, "depends_on = [\"draft\"]") {
		t.Fatalf("encoded workflow omitted expected TOML fields:\n%s", encoded)
	}
	got, err := config.DecodeWorkflowsConfig(encoded)
	if err != nil {
		t.Fatalf("decode encoded workflow: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow round trip mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if issues := config.ValidateWorkflows(got); len(issues) != 0 {
		t.Fatalf("round-tripped workflow should validate: %+v", issues)
	}
}

func TestWorkflowsConfigSafeDefaults(t *testing.T) {
	got, err := config.DecodeWorkflowsConfig(`[[workflows]]
id = "minimal"
[[workflows.steps]]
id = "step"
`)
	if err != nil {
		t.Fatalf("decode minimal workflow: %v", err)
	}
	if len(got.Workflows) != 1 || len(got.Workflows[0].Steps) != 1 {
		t.Fatalf("unexpected decoded workflow: %#v", got)
	}
	workflow := got.Workflows[0]
	if !workflow.Enabled || workflow.Source != config.DefaultWorkflowSource || workflow.Output != config.DefaultWorkflowOutput || workflow.Failure != config.DefaultWorkflowFailure || workflow.Execution != config.DefaultWorkflowExecution {
		t.Fatalf("unsafe or unexpected workflow defaults: %#v", workflow)
	}
	step := workflow.Steps[0]
	if !step.Enabled || step.Source != config.DefaultWorkflowSource || step.Output != config.DefaultWorkflowOutput || step.Failure != config.DefaultWorkflowFailure || step.Execution != config.DefaultWorkflowExecution {
		t.Fatalf("unsafe or unexpected step defaults: %#v", step)
	}
	if issues := config.ValidateWorkflows(got); len(issues) != 0 {
		t.Fatalf("safe defaults should validate: %+v", issues)
	}
}

func TestValidateWorkflowsIDsModesDependenciesAndCycles(t *testing.T) {
	bad := config.WorkflowsConfig{Workflows: []config.Workflow{
		{ID: "bad id", Source: "network", Output: "stream", Failure: "explode", Execution: "random"},
		{ID: "same", Steps: []config.WorkflowStep{
			{ID: "dup"}, {ID: "dup"}, {ID: "cycle-a", DependsOn: []string{"cycle-b"}},
			{ID: "cycle-b", DependsOn: []string{"cycle-a"}}, {ID: "missing", DependsOn: []string{"not-a-step"}},
		}},
		{ID: "same"},
	}}
	issues := config.ValidateWorkflows(bad)
	for _, keyPath := range []string{
		"workflows[0].id", "workflows[0].source", "workflows[0].output", "workflows[0].failure", "workflows[0].execution",
		"workflows[1].steps[1].id", "workflows[1].steps[4].depends_on[0]", "workflows[1].steps",
		"workflows[2].id",
	} {
		if !workflowIssue(issues, keyPath) {
			t.Errorf("expected validation issue at %s; got %+v", keyPath, issues)
		}
	}
}

func TestDecodeWorkflowsConfigRejectsWrongTypes(t *testing.T) {
	if _, err := config.DecodeWorkflowsConfig(`[[workflows]]
id = "x"
steps = ["not-a-table"]
`); err == nil {
		t.Fatal("expected wrong workflow step type to fail decoding")
	}
}
