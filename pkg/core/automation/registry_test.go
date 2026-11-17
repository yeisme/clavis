package automation

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestRegistryRegistersKindsAndListsByID(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterBuiltin("zeta", config.CLIAction("echo", "z")); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterScript(config.ScriptComponentSpec{ID: "alpha", Title: "Alpha", Args: []string{"one"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterWorkflow(WorkflowDescriptor{ID: "middle", Name: "Middle", Workflow: struct{ Enabled bool }{true}}); err != nil {
		t.Fatal(err)
	}

	got := r.List()
	ids := make([]string, len(got))
	for i, descriptor := range got {
		ids[i] = descriptor.ID
	}
	want := []string{"alpha", "middle", "zeta"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("IDs = %v, want %v", ids, want)
	}
	if got[0].Kind != KindScript || got[1].Kind != KindWorkflow || got[2].Kind != KindBuiltin {
		t.Fatalf("unexpected kinds: %#v", got)
	}
}

func TestRegistryRejectsDuplicateIDsAcrossKinds(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterBuiltin("same", config.ToggleIMEAction()); err != nil {
		t.Fatal(err)
	}
	err := r.RegisterScript(config.ScriptComponentSpec{ID: "same"})
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("error = %v, want duplicate ID error", err)
	}
}

func TestRegistryTracksProviderAvailability(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterScript(config.ScriptComponentSpec{ID: "script"}); err != nil {
		t.Fatal(err)
	}
	status := ProviderAvailability{Available: false, Reason: "shell permission denied"}
	if err := r.SetProviderAvailability("script", status); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("script")
	if !ok {
		t.Fatal("script descriptor was not registered")
	}
	if got.Availability != status {
		t.Fatalf("availability = %#v, want %#v", got.Availability, status)
	}
	if got := r.ProviderAvailability("new-provider"); !got.Available {
		t.Fatalf("unknown provider should default available: %#v", got)
	}
	if err := r.SetProviderAvailability("script", ProviderAvailability{Available: true, Reason: "ignored"}); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get("script")
	if got.Availability.Reason != "" {
		t.Fatalf("available provider retained reason %q", got.Availability.Reason)
	}
}

func TestRegistryCopiesConfigValues(t *testing.T) {
	r := NewRegistry()
	action := config.CLIAction("echo", "one")
	if err := r.RegisterBuiltin("builtin", action); err != nil {
		t.Fatal(err)
	}
	spec := config.ScriptComponentSpec{ID: "script", Args: []string{"one"}}
	if err := r.RegisterScript(spec); err != nil {
		t.Fatal(err)
	}
	action.Args[0] = "changed"
	spec.Args[0] = "changed"

	gotAction, _ := r.Get("builtin")
	gotScript, _ := r.Get("script")
	if gotAction.Action == nil || gotAction.Action.Args[0] != "one" {
		t.Fatalf("action was aliased: %#v", gotAction.Action)
	}
	if gotScript.Script == nil || gotScript.Script.Args[0] != "one" {
		t.Fatalf("script was aliased: %#v", gotScript.Script)
	}

	listed := r.List()
	listed[0].Script = nil
	if descriptor, ok := r.Get("script"); !ok || descriptor.Script == nil {
		t.Fatal("List exposed registry's descriptor storage")
	}
}

func TestRegistryRejectsMissingIDs(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Descriptor{}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Register error = %v, want invalid ID", err)
	}
	if err := r.RegisterWorkflow(WorkflowDescriptor{}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("RegisterWorkflow error = %v, want invalid ID", err)
	}
}
