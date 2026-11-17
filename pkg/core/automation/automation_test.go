package automation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type testRegistry map[string]ActionDescriptor

func (r testRegistry) LookupAction(id string) (ActionDescriptor, bool) {
	d, ok := r[id]
	return d, ok
}

func descriptor(id string) ActionDescriptor {
	return ActionDescriptor{ID: id, Provider: ProviderBuiltin, Risk: RiskLow}
}

func validWorkflow() WorkflowDefinition {
	return WorkflowDefinition{
		ID:     "daily_backup",
		Inputs: []WorkflowInput{{ID: "folder", Type: "string", Required: true}},
		Steps: []WorkflowStep{
			{ID: "prepare", ActionID: "prepare", FailureMode: FailureStop, OutputMode: OutputCapture},
			{ID: "save", ActionID: "save", DependsOn: []string{"prepare"}, FailureMode: FailureContinue, OutputMode: OutputReplace},
		},
	}
}

func TestValidateWorkflowAcceptsRegistryMethod(t *testing.T) {
	workflow := validWorkflow()
	issues := ValidateWorkflow(workflow, testRegistry{"prepare": descriptor("prepare"), "save": descriptor("save")})
	if len(issues) != 0 {
		t.Fatalf("expected valid workflow, got %#v", issues)
	}
	if err := ValidateWorkflowE(workflow, testRegistry{"prepare": descriptor("prepare"), "save": descriptor("save")}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWorkflowReportsAllStructuralIssues(t *testing.T) {
	workflow := WorkflowDefinition{
		ID:     "bad id",
		Inputs: []WorkflowInput{{ID: "input", Type: "string"}, {ID: "input", Type: "string"}},
		Steps: []WorkflowStep{
			{ID: "one", ActionID: "missing", Action: "other", DependsOn: []string{"two", "two", "none"}, FailureMode: "later", OutputMode: "wrong"},
			{ID: "two", ActionID: "ok", DependsOn: []string{"one"}, FailureMode: FailureStop, OutputMode: OutputCapture},
		},
	}
	issues := ValidateWorkflow(workflow, testRegistry{"ok": descriptor("ok")})
	codes := make(map[string]int)
	for _, issue := range issues {
		codes[issue.Code]++
	}
	for _, code := range []string{"invalid_id", "duplicate_id", "conflicting_action_id", "unknown_action", "duplicate_dependency", "unknown_step", "invalid_failure_mode", "invalid_output_mode", "dependency_cycle"} {
		if codes[code] == 0 {
			t.Errorf("missing issue code %q in %#v", code, issues)
		}
	}
}

func TestValidateWorkflowRejectsAbortAndDigitLeadingIdentifiers(t *testing.T) {
	workflow := WorkflowDefinition{
		ID:     "1workflow",
		Inputs: []WorkflowInput{{ID: "2input"}},
		Steps:  []WorkflowStep{{ID: "3step", ActionID: "4action", FailureMode: FailureMode("abort"), OutputMode: OutputCapture}},
	}
	issues := ValidateWorkflow(workflow, nil)
	for _, code := range []string{"invalid_id", "invalid_reference", "invalid_failure_mode"} {
		found := false
		for _, issue := range issues {
			if issue.Code == code {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing issue code %q in %#v", code, issues)
		}
	}
}

func TestValidateWorkflowAllowsActionReferenceDotsButNotDigitLeading(t *testing.T) {
	workflow := validWorkflow()
	workflow.Steps[0].ActionID = "script.echo"
	if issues := ValidateWorkflow(workflow, nil); len(issues) != 0 {
		t.Fatalf("dotted action reference should validate, got %#v", issues)
	}
	workflow.Steps[0].ActionID = "1script.echo"
	issues := ValidateWorkflow(workflow, nil)
	for _, issue := range issues {
		if issue.Code == "invalid_reference" {
			return
		}
	}
	t.Fatalf("digit-leading action reference was accepted: %#v", issues)
}

func TestValidateWorkflowAcceptsActionAlias(t *testing.T) {
	workflow := validWorkflow()
	workflow.Steps[0].Action = workflow.Steps[0].ActionID
	issues := ValidateWorkflow(workflow, MapActionRegistry{"prepare": descriptor("prepare"), "save": descriptor("save")})
	if len(issues) != 0 {
		t.Fatalf("expected matching action aliases to pass, got %#v", issues)
	}
}

func TestBuildExecutionPlanIsStableSerialTopologicalOrder(t *testing.T) {
	workflow := WorkflowDefinition{ID: "ordered", Steps: []WorkflowStep{
		{ID: "third", ActionID: "third", FailureMode: FailureStop, OutputMode: OutputCapture, DependsOn: []string{"first"}},
		{ID: "first", ActionID: "first", FailureMode: FailureStop, OutputMode: OutputCapture},
		{ID: "fourth", ActionID: "fourth", FailureMode: FailureStop, OutputMode: OutputCapture, DependsOn: []string{"second", "third"}},
		{ID: "second", ActionID: "second", FailureMode: FailureStop, OutputMode: OutputCapture, DependsOn: []string{"first"}},
	}}
	plan, err := NewExecutionPlanBuilder(testRegistry{
		"first": descriptor("first"), "second": descriptor("second"), "third": descriptor("third"), "fourth": descriptor("fourth"),
	}).Build(workflow)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(plan))
	for i, step := range plan {
		got[i] = step.ID
	}
	want := []string{"first", "third", "second", "fourth"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan order = %v, want %v", got, want)
	}
	second, err := BuildExecutionPlanWithRegistry(workflow, testRegistry{
		"first": descriptor("first"), "second": descriptor("second"), "third": descriptor("third"), "fourth": descriptor("fourth"),
	})
	if err != nil || !reflect.DeepEqual(plan, second) {
		t.Fatalf("plan is not deterministic: first=%v/%v second=%v/%v", plan, err, second, err)
	}
}

func TestBuildExecutionPlanWithoutRegistryStillChecksStructure(t *testing.T) {
	workflow := validWorkflow()
	plan, err := BuildExecutionPlan(workflow)
	if err != nil || len(plan) != 2 {
		t.Fatalf("expected structural plan without registry, got %#v, %v", plan, err)
	}
	workflow.Steps[1].DependsOn = []string{"unknown"}
	if _, err := BuildExecutionPlan(workflow); err == nil {
		t.Fatal("expected unknown dependency error")
	}
}

func TestBuildExecutionPlanRejectsCycle(t *testing.T) {
	workflow := validWorkflow()
	workflow.Steps[0].DependsOn = []string{"save"}
	if _, err := BuildExecutionPlan(workflow); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestJSONRoundTripIsProviderIndependent(t *testing.T) {
	workflow := validWorkflow()
	workflow.Steps[0].Input = map[string]any{"path": "/tmp/x", "enabled": true}
	data, err := json.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkflowDefinition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(workflow, decoded) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", decoded, workflow)
	}
	invocation := ActionInvocation{ActionID: "prepare", Input: map[string]any{"count": 2}}
	result := ActionResult{ActionID: "prepare", Status: ResultSucceeded, Output: map[string]any{"ok": true}}
	if _, err := json.Marshal([]any{invocation, result}); err != nil {
		t.Fatal(err)
	}
}

func TestValidationErrorContainsStableJSONIssueShape(t *testing.T) {
	err := ValidateWorkflowE(WorkflowDefinition{ID: ""}, nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Issues) == 0 || validation.Issues[0].Path != "id" {
		t.Fatalf("unexpected validation error: %#v", err)
	}
	data, marshalErr := json.Marshal(validation)
	if marshalErr != nil || !strings.Contains(string(data), `"code":"invalid_id"`) {
		t.Fatalf("unexpected JSON validation error: %s (%v)", data, marshalErr)
	}
}
