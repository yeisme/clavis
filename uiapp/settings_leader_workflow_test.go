package uiapp

import (
	"reflect"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestLeaderWorkflowIDsAreStableAndExcludeBlankIDs(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Workflows.Workflows = []config.Workflow{{ID: " first "}, {ID: ""}, {ID: "second"}}
	if got, want := leaderWorkflowIDs(cfg), []string{" first ", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow IDs = %#v, want %#v", got, want)
	}
}

func TestLeaderWorkflowOptionsPreserveMissingID(t *testing.T) {
	options, valid := leaderWorkflowOptions([]string{"daily", "weekly"}, "missing")
	if valid || !reflect.DeepEqual(options, []string{"missing", "daily", "weekly"}) {
		t.Fatalf("options=%v valid=%v, want stale ID preserved and invalid", options, valid)
	}
}

func TestExecuteWorkflowRejectsMissingAndDisabledRuntimeWorkflows(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.ExecuteWorkflow("missing", engine.CapsuleContext{}); err == nil {
		t.Fatal("missing workflow should fail safely")
	}
	cfg := ctrl.Config()
	cfg.Workflows.Workflows = []config.Workflow{{ID: "disabled", Enabled: false}}
	if err := ctrl.ConfigStore.SaveWorkflows(cfg.Workflows); err != nil {
		t.Fatalf("save workflows: %v", err)
	}
	if err := ctrl.ExecuteWorkflow("disabled", engine.CapsuleContext{}); err == nil {
		t.Fatal("disabled workflow should fail safely")
	}
}

func TestLeaderWorkflowActionCanBeSaved(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	cfg := ctrl.Config()
	cfg.Workflows.Workflows = []config.Workflow{{ID: "daily", Enabled: true}}
	if err := ctrl.ConfigStore.SaveWorkflows(cfg.Workflows); err != nil {
		t.Fatalf("save workflows: %v", err)
	}
	cfg = ctrl.Config()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "r", Label: "Run", Action: config.RunWorkflowAction("daily")}}
	if err := ctrl.SaveLeaderConfig(cfg.Leader); err != nil {
		t.Fatalf("save leader workflow action: %v", err)
	}
	if got := ctrl.Config().Leader.Nodes[0].Action.WorkflowID; got != "daily" {
		t.Fatalf("saved workflow id = %q", got)
	}
}
