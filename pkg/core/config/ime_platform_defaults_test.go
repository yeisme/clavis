package config

import (
	"strings"
	"testing"
)

func TestIMEDefaultsPerGOOS(t *testing.T) {
	if got := DefaultIMEConfigFor("darwin").DefaultInputSourceID; got != "com.apple.keylayout.ABC" {
		t.Fatalf("darwin default = %q", got)
	}
	if got := DefaultIMEConfigFor("windows").DefaultInputSourceID; got != "0x04090409" {
		t.Fatalf("windows default = %q", got)
	}
	for _, r := range DefaultIMERulesConfigFor("windows").Rules {
		if r.BundleID != "" || !strings.HasSuffix(r.ProcessName, ".exe") || r.InputSourceID != "" {
			t.Fatalf("windows rule must be process_name + mode only: %+v", r)
		}
	}
	if r := DefaultIMERulesConfigFor("darwin").Rules[0]; r.BundleID != "com.apple.Terminal" {
		t.Fatalf("darwin rules changed: %+v", r)
	}
}

func TestDefaultContentForWindowsDecodes(t *testing.T) {
	rules, err := DecodeIMERulesConfig(DefaultContentFor(FileIMERules, "windows"))
	if err != nil {
		t.Fatalf("decode windows ime-rules: %v", err)
	}
	if len(rules.Rules) != 2 || rules.Rules[0].ProcessName != "WindowsTerminal.exe" || rules.Rules[1].ProcessName != "Code.exe" {
		t.Fatalf("windows rules = %+v", rules.Rules)
	}
	mainTOML := DefaultContentFor(FileMain, "windows")
	if !strings.Contains(mainTOML, `default_input_source_id = "0x04090409"`) || strings.Contains(mainTOML, "com.apple.keylayout") {
		t.Fatalf("windows main default IME not replaced")
	}
	if _, err := DecodeMainConfig(mainTOML); err != nil {
		t.Fatalf("decode windows main: %v", err)
	}
	if DefaultContentFor(FileMain, "darwin") != defaultMainTOML {
		t.Fatalf("darwin main content must be unchanged")
	}
}

func TestNormalizeInputSourceIDFor(t *testing.T) {
	if got := NormalizeInputSourceIDFor("windows", "com.apple.keylayout.ABC"); got != WindowsDefaultInputSourceID {
		t.Fatalf("windows mac id = %q", got)
	}
	if got := NormalizeInputSourceIDFor("windows", "0x08040804"); got != "0x08040804" {
		t.Fatalf("windows hkl = %q", got)
	}
	if got := NormalizeInputSourceIDFor("darwin", "com.apple.keylayout.ABC"); got != "com.apple.keylayout.ABC" {
		t.Fatalf("darwin = %q", got)
	}
}
