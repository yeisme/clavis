package config_test

import (
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestLeaderWorkflowActionRoundTrip(t *testing.T) {
	want := config.LeaderConfig{Nodes: []config.LeaderNode{{Key: "r", Label: "Run", Action: config.RunWorkflowAction("daily")}}}
	got, err := config.DecodeLeaderConfig(want.Encode())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Nodes[0].Action.Type != config.ActionRunWorkflow || got.Nodes[0].Action.WorkflowID != "daily" {
		t.Fatalf("round trip = %#v", got.Nodes[0].Action)
	}
	if !strings.Contains(want.Encode(), "workflow_id = \"daily\"") {
		t.Fatalf("encoded workflow id missing: %s", want.Encode())
	}
}

func TestLeaderWorkflowActionDecodeRejectsMissingOrBlankID(t *testing.T) {
	for _, input := range []string{
		"[[nodes]]\nkey = \"r\"\nlabel = \"Run\"\naction = \"run_workflow\"\n",
		"[[nodes]]\nkey = \"r\"\nlabel = \"Run\"\naction = \"run_workflow\"\nworkflow_id = \"   \"\n",
	} {
		if _, err := config.DecodeLeaderConfig(input); err == nil {
			t.Fatalf("expected decode error for %q", input)
		}
	}
}

func TestConfigStoreWorkflowEditsRejectNewlyBrokenLeaderReferences(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	workflow := config.WorkflowsConfig{Workflows: []config.Workflow{{ID: "daily"}}}
	if err := store.SaveWorkflows(workflow); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	leader := config.LeaderConfig{Nodes: []config.LeaderNode{{Key: "r", Label: "Run", Action: config.RunWorkflowAction("daily")}}}
	if err := store.SaveLeader(leader); err != nil {
		t.Fatalf("save leader reference: %v", err)
	}

	if err := store.SaveWorkflows(config.WorkflowsConfig{}); err == nil || !strings.Contains(err.Error(), "references workflow") {
		t.Fatalf("delete referenced workflow error = %v, want a clear reference error", err)
	}
	if got := store.Current().Workflows.Workflows[0].ID; got != "daily" {
		t.Fatalf("failed delete changed workflow config: %q", got)
	}

	if err := store.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{{ID: "renamed"}}}); err == nil || !strings.Contains(err.Error(), "references workflow") {
		t.Fatalf("rename referenced workflow error = %v, want a clear reference error", err)
	}
	if got := store.Current().Workflows.Workflows[0].ID; got != "daily" {
		t.Fatalf("failed rename changed workflow config: %q", got)
	}
}

func TestConfigStoreWorkflowEditRejectsRetainingDanglingLeaderWarning(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	leader := config.LeaderConfig{Nodes: []config.LeaderNode{{Key: "r", Label: "Run", Action: config.RunWorkflowAction("missing")}}}
	if err := store.SaveLeader(leader); err != nil {
		t.Fatalf("save dangling leader reference: %v", err)
	}
	if len(store.LastWarnings()) == 0 {
		t.Fatal("expected dangling leader reference warning")
	}
	if err := store.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{{ID: "new"}}}); err == nil || !strings.Contains(err.Error(), "references workflow") {
		t.Fatalf("workflow save retaining dangling reference error = %v, want a clear reference error", err)
	}
	if len(store.LastWarnings()) == 0 {
		t.Fatal("expected dangling leader reference warning to remain after rejected workflow save")
	}
}

func TestValidateLeaderWorkflowReference(t *testing.T) {
	leader := config.LeaderConfig{Nodes: []config.LeaderNode{{Key: "r", Label: "Run", Action: config.RunWorkflowAction("missing")}}}
	issues := config.ValidateLeader(leader, nil, map[string]struct{}{"known": {}})
	found := false
	for _, issue := range issues {
		if issue.KeyPath == "nodes[0].workflow_id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unknown workflow issue, got %+v", issues)
	}

	cfg := config.DefaultConfig()
	cfg.Leader = leader
	cfg.Workflows.Workflows = []config.Workflow{{ID: "known"}}
	fullIssues := config.Validate(cfg)
	found = false
	for _, issue := range fullIssues {
		if issue.KeyPath == "nodes[0].workflow_id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected full config validation to check workflow reference, got %+v", fullIssues)
	}
}
