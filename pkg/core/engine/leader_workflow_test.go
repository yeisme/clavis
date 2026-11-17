package engine

import (
	"reflect"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestLeaderRunWorkflowEmitsEffectWithCapsuleContext(t *testing.T) {
	cfg := config.DefaultConfig()
	ctx := CapsuleContext{AppName: "Editor", SelectedText: "selected"}
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "r", Label: "Run", Action: config.RunWorkflowAction("daily")}}
	sm := NewStateMachine(cfg)
	sm.Handle(Event{Type: EventOpenLeader, Context: &ctx})
	effects := sm.Handle(Event{Type: EventLeaderKeyPress, Key: "r"})
	if len(effects) != 2 || effects[0].Type != EffectRunWorkflow || effects[0].WorkflowID != "daily" || effects[1].Type != EffectClose {
		t.Fatalf("effects = %#v", effects)
	}
	if effects[0].Context == nil || !reflect.DeepEqual(*effects[0].Context, ctx) {
		t.Fatalf("context = %#v, want %#v", effects[0].Context, ctx)
	}
	if sm.State().Phase != PhaseHidden {
		t.Fatalf("phase = %v, want hidden", sm.State().Phase)
	}
}

func TestLeaderRunWorkflowBlankIDNoOps(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Leader.Nodes = []config.LeaderNode{{Key: "r", Label: "Run", Action: config.RunWorkflowAction(" ")}}
	sm := NewStateMachine(cfg)
	sm.Handle(Event{Type: EventOpenLeader, Context: &CapsuleContext{AppName: "Editor"}})
	if effects := sm.Handle(Event{Type: EventLeaderKeyPress, Key: "r"}); len(effects) != 0 {
		t.Fatalf("effects = %#v, want no-op", effects)
	}
}
