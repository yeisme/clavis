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
