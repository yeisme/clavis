package uiapp

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestWorkflowActionRegistryOnlyExposesSafeCLIAndScripts(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Leader.Nodes = []config.LeaderNode{
		{Key: "safe", Label: "Safe", Action: config.CLIAction("printf", "ok")},
		{Key: "window", Label: "Window", Action: config.WindowAction(config.WindowLeftHalf)},
	}
	cfg.Components.Scripts = []config.ScriptComponentSpec{{ID: "script", Command: "printf", Args: []string{"script"}, Enabled: true}}
	registry := (&AppController{}).workflowActionRegistry(cfg)
	if _, ok := registry.GetAction("leader.safe"); !ok {
		t.Fatal("safe CLI action missing")
	}
	if _, ok := registry.GetAction("safe"); !ok {
		t.Fatal("short CLI action alias missing")
	}
	if _, ok := registry.GetAction("script"); !ok {
		t.Fatal("script action missing")
	}
	if _, ok := registry.GetAction("leader.window"); ok {
		t.Fatal("non-CLI action exposed to workflow")
	}
}

func TestWorkflowProviderInvokesCLIWithoutShellInterpolation(t *testing.T) {
	provider := workflowProvider{
		actionCtx: engine.CapsuleContext{SelectedText: "selected"},
		actions: map[string]workflowAction{
			"safe": {leader: func() *config.LeaderAction {
				a := config.CLIAction("printf", "%s", "literal;not-shell")
				return &a
			}()},
		},
	}
	result, err := provider.Invoke(context.Background(), automation.ActionInvocation{ActionID: "safe"})
	if err != nil || result.Status != automation.ResultSucceeded {
		t.Fatalf("invoke = %#v, %v", result, err)
	}
	if !strings.Contains(result.Output.(string), "literal;not-shell") {
		t.Fatalf("output = %#v", result.Output)
	}
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("printf is unavailable")
	}
}
