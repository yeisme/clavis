package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

// A binding must fire from its chord and only its chord, with modifiers matched exactly so
// ctrl+option+return does not also trigger on ctrl+shift+option+return.
func TestBindingForMatchesExactChord(t *testing.T) {
	r := newTriggerRuntime()
	r.setKeyBindings([]config.KeyBinding{
		{Keys: []string{"ctrl+option+return"}, Action: "ai_continue"},
	})

	if action, ok := r.bindingFor("return", []string{"ctrl", "option"}, ""); !ok || action != "ai_continue" {
		t.Fatalf("chord not matched: action=%q ok=%v", action, ok)
	}
	if _, ok := r.bindingFor("return", []string{"ctrl"}, ""); ok {
		t.Fatal("partial modifiers must not match")
	}
	if _, ok := r.bindingFor("return", []string{"ctrl", "option", "shift"}, ""); ok {
		t.Fatal("extra modifiers must not match")
	}
	if _, ok := r.bindingFor("space", []string{"ctrl", "option"}, ""); ok {
		t.Fatal("wrong key must not match")
	}
}

// The per-app restriction is what lets the same chord mean different things in a chat window
// and an editor.
func TestBindingForHonoursAppRestriction(t *testing.T) {
	r := newTriggerRuntime()
	r.setKeyBindings([]config.KeyBinding{
		{Keys: []string{"ctrl+option+r"}, Action: "ai_continue", AppBundleIDs: []string{"com.apple.MobileSMS"}},
	})

	if _, ok := r.bindingFor("r", []string{"ctrl", "option"}, "com.apple.MobileSMS"); !ok {
		t.Fatal("binding should fire in the listed app")
	}
	if _, ok := r.bindingFor("r", []string{"ctrl", "option"}, "com.apple.dt.Xcode"); ok {
		t.Fatal("binding must not fire in an unlisted app")
	}
}

// Bindings are actions, not key remaps, so they keep working with [keymap] enabled=false —
// that switch governs tap/hold remapping, and someone who turned it off may still want the
// AI chords.
func TestBindingsAreIndependentOfKeymapEnableFlag(t *testing.T) {
	r := newTriggerRuntime()
	r.setKeyBindings([]config.KeyBinding{{Keys: []string{"ctrl+option+o"}, Action: "ai_optimize"}})
	if _, ok := r.bindingFor("o", []string{"ctrl", "option"}, ""); !ok {
		t.Fatal("bindings must not depend on the keymap enabled flag")
	}
}

// A malformed chord is skipped rather than breaking every other binding.
func TestInvalidChordIsSkippedNotFatal(t *testing.T) {
	r := newTriggerRuntime()
	r.setKeyBindings([]config.KeyBinding{
		{Keys: []string{""}, Action: "ai_continue"},
		{Keys: []string{"ctrl+option+k"}, Action: "ai_optimize"},
	})
	if action, ok := r.bindingFor("k", []string{"ctrl", "option"}, ""); !ok || action != "ai_optimize" {
		t.Fatalf("valid binding should still work: %q %v", action, ok)
	}
}
