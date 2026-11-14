package uiapp

import (
	"strings"
	"testing"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// "Bind current app" on Windows has no bundle ID: it must store process_name, and re-binding
// with different case must replace (not duplicate) the existing rule.
func TestBuildAppIMERulesWindowsProcessName(t *testing.T) {
	existing := []config.IMERule{
		{ProcessName: "code.exe", PreferredMode: config.IMEModeNative},
		{ProcessName: "WindowsTerminal.exe", PreferredMode: config.IMEModeASCII},
		{ProcessName: "Code.exe", TitlePattern: "vim", PreferredMode: config.IMEModeASCII},
	}
	got := buildAppIMERules(existing, "", "Code.exe", "Code", config.IMEModeASCII, config.WindowsDefaultInputSourceID)
	if len(got) != 3 {
		t.Fatalf("want 3 rules (replace, keep terminal, keep title rule), got %+v", got)
	}
	last := got[len(got)-1]
	if last.ProcessName != "Code.exe" || last.BundleID != "" || last.PreferredMode != config.IMEModeASCII ||
		!last.RestoreOnLeave || last.InputSourceID != config.WindowsDefaultInputSourceID {
		t.Fatalf("new rule = %+v", last)
	}
	if got = buildAppIMERules(got, "", "CODE", "", "", ""); len(got) != 2 {
		t.Fatalf("unbind should drop the app rule only: %+v", got)
	}
	if !isClavisProcess("", "Clavis.exe") || isClavisProcess("", "Code.exe") {
		t.Fatal("isClavisProcess")
	}
}

func TestForegroundIMERows(t *testing.T) {
	st := bridge.ForegroundIMEStatus{
		App:         bridge.AppIdentity{ProcessName: "Code.exe"},
		Source:      host.InputSourceInfo{ID: "0x08040804", Name: "Microsoft Pinyin", Mode: host.InputModeNative},
		Rule:        config.IMERule{ProcessName: "Code.exe", PreferredMode: config.IMEModeASCII},
		RuleMatched: true,
		AutoSwitch:  false,
	}
	rows := foregroundIMERows(st)
	if len(rows) != 4 || rows[0][1] != "Code.exe" {
		t.Fatalf("rows = %v", rows)
	}
	if !strings.Contains(rows[3][1], "Code.exe") || !strings.Contains(rows[3][1], "ascii") {
		t.Fatalf("rule row = %q", rows[3][1])
	}
	st.RuleMatched = false
	if rows = foregroundIMERows(st); strings.Contains(rows[3][1], "Code.exe") {
		t.Fatalf("unmatched rule row = %q", rows[3][1])
	}
}
