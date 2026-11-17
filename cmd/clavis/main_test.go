package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
)

func TestParseGlobalFlagsAllowsFlagsAfterCommand(t *testing.T) {
	dir := t.TempDir()
	gotDir, jsonOutput, command, err := parseGlobalFlags([]string{"workflow", "list", "--config-dir", dir, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != dir || !jsonOutput || strings.Join(command, " ") != "workflow list" {
		t.Fatalf("got dir=%q json=%v command=%v", gotDir, jsonOutput, command)
	}
}

func TestRunWorkflowPlanPrintsDeterministicPlanWithoutExecuting(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	cfg := store.Current()
	cfg.Components.Scripts = append(cfg.Components.Scripts, config.ScriptComponentSpec{ID: "prepare", Command: "touch", Enabled: true})
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "build", Name: "Build", Enabled: true,
		Steps: []config.WorkflowStep{
			{ID: "second", Action: "prepare", DependsOn: []string{"first"}, Enabled: true},
			{ID: "first", Action: "prepare", Enabled: true},
			{ID: "disabled", Action: "prepare", Enabled: false},
		},
	}}
	if err := store.SaveComponents(cfg.Components); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkflows(cfg.Workflows); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"workflow", "plan", "build", "--config-dir", dir}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v; stderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "first\nsecond\n"; got != want {
		t.Fatalf("plan=%q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "workflows.toml")); err != nil {
		t.Fatal(err)
	}
}

func TestRunWorkflowPlanRejectsPromptSourceAndDisabledScript(t *testing.T) {
	cfg := config.ClavisConfig{
		Components: config.ComponentsConfig{Scripts: []config.ScriptComponentSpec{{ID: "disabled", Command: "echo", Enabled: false}}},
		Workflows:  config.WorkflowsConfig{Workflows: []config.Workflow{{ID: "prompt", Enabled: true, Source: config.WorkflowSourcePrompt, Steps: []config.WorkflowStep{{ID: "step", Action: "disabled", Enabled: true}}}}},
	}
	if err := validateWorkflowPlanConfig(cfg.Workflows.Workflows[0]); err == nil || !strings.Contains(err.Error(), "prompt source") {
		t.Fatalf("prompt source validation error = %v", err)
	}
	cfg.Workflows.Workflows[0].Source = config.WorkflowSourceClipboard
	plan, err := automation.BuildExecutionPlanWithRegistry(toAutomationWorkflow(cfg.Workflows.Workflows[0]), actionRegistry(cfg))
	if err == nil || !strings.Contains(err.Error(), "not registered") || plan != nil {
		t.Fatalf("disabled script planning error = %v plan=%v", err, plan)
	}
}

func TestRunWorkflowPlanRejectsUnsupportedModes(t *testing.T) {
	workflow := config.Workflow{ID: "invalid", Execution: config.WorkflowExecutionParallel}
	if err := validateWorkflowPlanConfig(workflow); err == nil || !strings.Contains(err.Error(), "unsupported execution mode") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestRunWorkflowPlanJSON(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	cfg := store.Current()
	cfg.Components.Scripts = append(cfg.Components.Scripts, config.ScriptComponentSpec{ID: "echo", Command: "echo", Enabled: true})
	cfg.Workflows.Workflows = []config.Workflow{{ID: "one", Enabled: true, Steps: []config.WorkflowStep{{ID: "step", Action: "echo", Enabled: true}}}}
	if err := store.SaveComponents(cfg.Components); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkflows(cfg.Workflows); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"workflow", "plan", "one", "--json", "--config-dir", dir}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v; stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "step"`) || strings.Contains(stdout.String(), `"command"`) {
		t.Fatalf("unexpected plan JSON: %s", stdout.String())
	}
}
