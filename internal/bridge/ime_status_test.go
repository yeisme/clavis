package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

// A1: the Windows default rules and a "bind current app" rule target process_name, so a
// foreground AppIdentity{ProcessName:"Code.exe"} (what winForegroundApp reports) must match,
// switch to ASCII, and restore native (中) when leaving.
func TestWindowsCodeExeRuleMatchAndRestore(t *testing.T) {
	rules := config.DefaultIMERulesConfigFor("windows")
	for _, r := range rules.Rules {
		if r.BundleID != "" || r.ProcessName == "" {
			t.Fatalf("windows default rule must target process_name only: %+v", r)
		}
	}
	eng := NewIMESwitchDecisionEngine(true, rules)
	code := AppIdentity{ProcessName: "Code.exe", WindowTitle: "main.go - clavis - Visual Studio Code"}

	if r, ok := eng.MatchedRule(code); !ok || r.ProcessName != "Code.exe" || r.PreferredMode != config.IMEModeASCII {
		t.Fatalf("MatchedRule(Code.exe) = %+v, %v", r, ok)
	}
	// MatchedRule is read-only: the first activation still switches.
	d := eng.EvaluateAppActivationWithState(code, "0x08040804", true)
	if !d.ShouldSwitch || d.TargetMode != config.IMEModeASCII {
		t.Fatalf("entering Code.exe in 中 should switch to EN: %+v", d)
	}
	d = eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "WINWORD.EXE"}, "0x08040804", false)
	if !d.ShouldSwitch || !d.Restored || d.TargetMode != config.IMEModeNative {
		t.Fatalf("leaving Code.exe should restore 中: %+v", d)
	}
}

// A rule written by "bind current app" on Windows (process_name only + en-US HKL) behaves the same.
func TestWindowsBoundProcessRuleWithHKL(t *testing.T) {
	eng := NewIMESwitchDecisionEngine(true, config.IMERulesConfig{Rules: []config.IMERule{{
		ProcessName: "Code.exe", AppName: "Code", InputSourceID: config.WindowsDefaultInputSourceID,
		PreferredMode: config.IMEModeASCII, RestoreOnLeave: true,
	}}})
	d := eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "code.EXE"}, "0x08040804", true)
	if !d.ShouldSwitch || d.TargetInputSourceID != config.WindowsDefaultInputSourceID {
		t.Fatalf("bound rule should target en-US HKL: %+v", d)
	}
	d = eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "notepad.exe"}, config.WindowsDefaultInputSourceID, false)
	if !d.ShouldSwitch || !d.Restored || d.TargetInputSourceID != "0x08040804" {
		t.Fatalf("leaving should restore the Pinyin HKL: %+v", d)
	}
}

func TestMatchedRuleIgnoresClavisAndUnmatched(t *testing.T) {
	eng := NewIMESwitchDecisionEngine(true, config.DefaultIMERulesConfigFor("windows"))
	if _, ok := eng.MatchedRule(AppIdentity{ProcessName: "Clavis.exe"}); ok {
		t.Fatal("Clavis itself must never match")
	}
	if _, ok := eng.MatchedRule(AppIdentity{ProcessName: "notepad.exe"}); ok {
		t.Fatal("notepad has no rule")
	}
	if got := DescribeIMERuleTarget(config.IMERule{TitlePattern: "vim"}); got != "title~vim" {
		t.Fatalf("describe title rule = %q", got)
	}
}
