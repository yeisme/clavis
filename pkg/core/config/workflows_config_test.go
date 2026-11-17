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
				Failure: config.WorkflowFailureContinue, Execution: config.WorkflowExecutionSequential},
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

func TestValidateWorkflowsCanonicalIDsRejectWhitespace(t *testing.T) {
	cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID:     " padded ",
		Inputs: []config.WorkflowInput{{ID: " tone "}},
		Steps: []config.WorkflowStep{
			{ID: " lead"},
			{ID: "trail "},
			{ID: "ok", DependsOn: []string{" lead"}},
		},
	}}}
	issues := config.ValidateWorkflows(cfg)
	for _, keyPath := range []string{
		"workflows[0].id",
		"workflows[0].inputs[0].id",
		"workflows[0].steps[0].id",
		"workflows[0].steps[1].id",
		"workflows[0].steps[2].depends_on[0]",
	} {
		if !workflowIssue(issues, keyPath) {
			t.Errorf("expected canonical-id issue at %s; got %+v", keyPath, issues)
		}
	}
}

func TestValidateWorkflowsRejectsPromptSource(t *testing.T) {
	cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "prompt-flow", Source: config.WorkflowSourcePrompt,
		Steps: []config.WorkflowStep{{ID: "ask", Source: config.WorkflowSourcePrompt}},
	}}}
	issues := config.ValidateWorkflows(cfg)
	for _, keyPath := range []string{"workflows[0].source", "workflows[0].steps[0].source"} {
		if !workflowIssue(issues, keyPath) {
			t.Errorf("expected prompt-source issue at %s; got %+v", keyPath, issues)
		}
	}
	for _, issue := range issues {
		if strings.Contains(issue.KeyPath, ".source") && !strings.Contains(issue.Message, `unsupported workflow source mode "prompt"`) {
			t.Errorf("unexpected prompt-source message at %s: %q", issue.KeyPath, issue.Message)
		}
	}
}

func TestDecodeWorkflowsConfigNormalizesLegacyDiscardOutput(t *testing.T) {
	decoded, err := config.DecodeWorkflowsConfig(`[[workflows]]
id = "legacy"
output = "discard"
[[workflows.steps]]
id = "one"
output = "discard"
`)
	if err != nil {
		t.Fatalf("decode legacy discard output: %v", err)
	}
	if got := decoded.Workflows[0].Output; got != config.WorkflowOutputNone {
		t.Fatalf("workflow output = %q, want %q", got, config.WorkflowOutputNone)
	}
	if got := decoded.Workflows[0].Steps[0].Output; got != config.WorkflowOutputNone {
		t.Fatalf("step output = %q, want %q", got, config.WorkflowOutputNone)
	}
	if issues := config.ValidateWorkflows(decoded); len(issues) != 0 {
		t.Fatalf("normalized legacy workflow should validate: %+v", issues)
	}
	encoded := decoded.Encode()
	if strings.Contains(encoded, `output = "discard"`) || !strings.Contains(encoded, `output = "none"`) {
		t.Fatalf("legacy discard output was not re-encoded as none:\n%s", encoded)
	}
}

func TestValidateWorkflowsRejectsAbortFailureAndDigitLeadingIDs(t *testing.T) {
	cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "1workflow", Failure: config.WorkflowFailureMode("abort"),
		Inputs: []config.WorkflowInput{{ID: "2input"}},
		Steps:  []config.WorkflowStep{{ID: "3step", Failure: config.WorkflowFailureMode("abort")}},
	}}}
	issues := config.ValidateWorkflows(cfg)
	for _, path := range []string{"workflows[0].id", "workflows[0].inputs[0].id", "workflows[0].steps[0].id", "workflows[0].failure", "workflows[0].steps[0].failure"} {
		if !workflowIssue(issues, path) {
			t.Errorf("expected validation issue at %s; got %+v", path, issues)
		}
	}
}

func TestValidateWorkflowsAllowsASCIIIdentifierPunctuation(t *testing.T) {
	cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "workflow_1-2", Inputs: []config.WorkflowInput{{ID: "input_1-2"}},
		Steps: []config.WorkflowStep{{ID: "step_1-2"}},
	}}}
	if issues := config.ValidateWorkflows(cfg); len(issues) != 0 {
		t.Fatalf("expected valid identifiers, got %+v", issues)
	}
}

func TestValidateWorkflowsRejectsRuntimeIncompatibleDiscardLiteral(t *testing.T) {
	cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "in-memory", Output: config.WorkflowOutput("discard"),
		Steps: []config.WorkflowStep{{ID: "one", Output: config.WorkflowOutput("discard")}},
	}}}
	issues := config.ValidateWorkflows(cfg)
	for _, keyPath := range []string{"workflows[0].output", "workflows[0].steps[0].output"} {
		if !workflowIssue(issues, keyPath) {
			t.Errorf("expected unnormalized discard issue at %s; got %+v", keyPath, issues)
		}
	}
}

func TestValidateWorkflowsAcceptsOutputNone(t *testing.T) {
	cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "quiet", Output: config.WorkflowOutputDiscard,
		Steps: []config.WorkflowStep{{ID: "one", Output: config.WorkflowOutputNone}},
	}}}
	if issues := config.ValidateWorkflows(cfg); len(issues) != 0 {
		t.Fatalf("intentional OutputNone should validate: %+v", issues)
	}
}

func TestValidateWorkflowsSeparatesWorkflowAndStepOutputs(t *testing.T) {
	for _, output := range []config.WorkflowOutput{config.WorkflowOutputInsert, config.WorkflowOutputClipboard} {
		t.Run("workflow "+string(output), func(t *testing.T) {
			cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
				ID: "workflow", Output: output,
				Steps: []config.WorkflowStep{{ID: "step", Output: config.WorkflowOutputNone}},
			}}}
			if issues := config.ValidateWorkflows(cfg); len(issues) != 0 {
				t.Fatalf("workflow output %q should validate: %+v", output, issues)
			}
		})

		t.Run("step "+string(output), func(t *testing.T) {
			cfg := config.WorkflowsConfig{Workflows: []config.Workflow{{
				ID: "workflow", Output: config.WorkflowOutputNone,
				Steps: []config.WorkflowStep{{ID: "step", Output: output}},
			}}}
			issues := config.ValidateWorkflows(cfg)
			issuePath := "workflows[0].steps[0].output"
			if !workflowIssue(issues, issuePath) {
				t.Fatalf("step output %q should be rejected at %s: %+v", output, issuePath, issues)
			}
			for _, issue := range issues {
				if issue.KeyPath == issuePath && !strings.Contains(issue.Message, "unsupported workflow step output mode") {
					t.Fatalf("unexpected step output error: %q", issue.Message)
				}
			}
		})
	}
}

func TestValidateWorkflowsRejectsUnsupportedRuntimeModes(t *testing.T) {
	workflow := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "unsupported", Failure: config.WorkflowFailureNotify, Execution: config.WorkflowExecutionParallel,
		Steps: []config.WorkflowStep{{ID: "step", Action: "noop", Failure: "retry", Execution: "concurrent"}},
	}}}
	issues := config.ValidateWorkflows(workflow)
	for _, keyPath := range []string{
		"workflows[0].failure", "workflows[0].execution",
		"workflows[0].steps[0].failure", "workflows[0].steps[0].execution",
	} {
		if !workflowIssue(issues, keyPath) {
			t.Errorf("expected unsupported runtime mode at %s; got %+v", keyPath, issues)
		}
	}
	for _, issue := range issues {
		if issue.KeyPath == "workflows[0].failure" && !strings.Contains(issue.Message, `unsupported workflow failure mode "notify"`) {
			t.Fatalf("unexpected failure mode message: %q", issue.Message)
		}
	}
}

func TestValidateWorkflowsRejectsEnabledDependencyOnDisabledStep(t *testing.T) {
	workflow := config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "disabled-dependency",
		Steps: []config.WorkflowStep{
			{ID: "disabled", Enabled: false},
			{ID: "enabled", Enabled: true, DependsOn: []string{"disabled"}},
		},
	}}}
	issues := config.ValidateWorkflows(workflow)
	if !workflowIssue(issues, "workflows[0].steps[1].depends_on[0]") {
		t.Fatalf("expected disabled dependency validation issue; got %+v", issues)
	}
	for _, issue := range issues {
		if issue.KeyPath == "workflows[0].steps[1].depends_on[0]" && issue.Message != `enabled workflow step "enabled" depends on disabled workflow step "disabled"` {
			t.Fatalf("unexpected disabled dependency message: %q", issue.Message)
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
