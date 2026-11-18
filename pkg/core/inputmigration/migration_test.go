package inputmigration

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func baseline() PersonalBaseline {
	b := NewPersonalBaseline("acct")
	b.Revision, b.Scheme, b.CandidateCount, b.ShiftToggleASCII = 3, "quanpin", 7, true
	b.FuzzyPinyin = []string{"zh=z", "ch=c", "sh=s"}
	b.Dictionaries = []DictionaryMetadata{{ID: "custom", ContentHash: strings.Repeat("ab", 32), Format: "clavis-dict", EntryCount: 4, SourceDigest: Digest("dict-source")}}
	b.Learning = LearningData{Enabled: true, SummaryOnly: true, Words: []LearningEntry{NewLearningEntry("nihao", "你好", 8, true)}, Bigrams: []BigramEntry{NewBigramEntry("你好", "世界", 3)}, Aggregates: []LearningEntry{NewLearningEntry("nihao", "你好", 11, false)}}
	b.Shortcuts = []ShortcutRule{{Chord: "caps", Action: "toggle-ascii", Explicit: true, RequiresHook: true, Apps: []StableApp{{BundleID: "com.example.editor", Process: "editor"}}}}
	b.AppRules = []AppRule{{ID: "editor-chinese", Apps: []StableApp{{BundleID: "com.example.editor"}}, Action: "scheme:quanpin"}}
	b.FieldRevisions = map[string]uint64{"scheme": 1, "candidateCount": 1, "shiftToggleASCII": 1, "fuzzyPinyin": 1, "learning": 1, "shortcuts": 1, "appRules": 1}
	src := macCaps()
	b.SourceDevice = &src
	return b
}
func macCaps() DeviceCapabilities {
	return DeviceCapabilities{OS: "darwin", Arch: "arm64", KeyboardLayout: "com.apple.keylayout.ABC", InputSourceIDs: []string{"com.apple.inputmethod.SCIM.ITABC"}, NativeIMK: true, Permissions: map[string]PermissionState{"accessibility": PermissionGranted}, Display: DisplayInfo{Width: 2560, Height: 1600, Scale: 2}}
}
func windowsCaps() DeviceCapabilities {
	return DeviceCapabilities{OS: "windows", Arch: "amd64", KeyboardLayout: "00000409", InputSourceIDs: []string{"0x04090409"}, NativeTSF: true, Permissions: map[string]PermissionState{"uiaccess": PermissionDenied}, Display: DisplayInfo{Width: 1920, Height: 1080, Scale: 1}}
}

func TestCanonicalPlatformNamesSelectNativeBackends(t *testing.T) {
	for _, tc := range []struct {
		name    string
		caps    DeviceCapabilities
		backend string
	}{
		{"windows-with-whitespace", func() DeviceCapabilities {
			caps := windowsCaps()
			caps.OS = "  windows  "
			return caps
		}(), "native-tsf"},
		{"macos-with-whitespace", func() DeviceCapabilities {
			caps := macCaps()
			caps.OS = "  macOS  "
			return caps
		}(), "native-imk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.caps.Validate(); err != nil {
				t.Fatalf("canonical platform should validate: %v", err)
			}
			preview, err := GenerateOverlay(baseline(), tc.caps, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Overlay.Backend != tc.backend {
				t.Fatalf("platform %q selected backend %q, want %q", tc.caps.OS, preview.Overlay.Backend, tc.backend)
			}
			if err := preview.Overlay.Validate(); err != nil {
				t.Fatalf("generated overlay failed validation: %v", err)
			}
		})
	}
}

func TestCanonicalPlatformNamesRejectUnknownAndEmpty(t *testing.T) {
	for _, os := range []string{"", "   ", "plan9", "  plan9  "} {
		t.Run("os-"+strings.ReplaceAll(os, " ", "_")+"", func(t *testing.T) {
			caps := windowsCaps()
			caps.OS = os
			if err := caps.Validate(); err == nil {
				t.Fatalf("invalid platform %q was accepted", os)
			}
			if _, err := GenerateOverlay(baseline(), caps, "device"); err == nil {
				t.Fatalf("invalid platform %q reached generation", os)
			}
		})
	}
}

func TestPlatformOverlaysAndExplainableDiffs(t *testing.T) {
	b := baseline()
	for _, tc := range []struct {
		name, backend, layout string
		caps                  DeviceCapabilities
	}{{"macos", "native-imk", "com.apple.keylayout.ABC", macCaps()}, {"windows", "native-tsf", "00000409", windowsCaps()}} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := GenerateOverlay(b, tc.caps, "device-"+tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if p.Overlay.Backend != tc.backend || p.Overlay.KeyboardLayout != tc.layout || p.Overlay.InputSourceID != tc.caps.InputSourceIDs[0] {
				t.Fatalf("overlay not capability-specific: %+v", p.Overlay)
			}
			if len(p.Diffs) == 0 {
				t.Fatal("expected explainable differences")
			}
			for _, d := range p.Diffs {
				if d.Path == "" || d.Category == "" || d.Reason == "" || d.SourceValue == "com.apple.inputmethod.SCIM.ITABC" || strings.Contains(d.SourceValue+d.TargetValue, "你好") {
					t.Fatalf("unsafe or incomplete diff: %+v", d)
				}
			}
		})
	}
}
func TestRoundTripValidationAndCopyIsolation(t *testing.T) {
	b := baseline()
	raw, err := MarshalBaseline(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalBaseline(raw)
	if err != nil || got.Scheme != "quanpin" || got.CandidateCount != 7 || len(got.FuzzyPinyin) != 3 || got.Learning.Words[0].Frequency != 8 || got.Learning.Aggregates[0].Frequency != 11 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	b.Scheme = "shuangpin"
	again, _ := UnmarshalBaseline(raw)
	if again.Scheme != "quanpin" {
		t.Fatal("unmarshal shares mutable state")
	}
	b.FuzzyPinyin[0] = "changed"
	if got.FuzzyPinyin[0] == "changed" {
		t.Fatal("marshal did not isolate input")
	}
	b.SchemaVersion = 99
	if _, err := MarshalBaseline(b); err == nil {
		t.Fatal("unknown version accepted")
	}
	b = baseline()
	bad := macCaps()
	bad.OS = "plan9"
	if _, err := GenerateOverlay(b, bad, "x"); err == nil {
		t.Fatal("invalid platform accepted")
	}
	if strings.Contains(string(raw), "你好") || strings.Contains(string(raw), "nihao") {
		t.Fatal("raw learning text exported")
	}
}
func TestUnsupportedRulesAreDeferredAndPreviewIsRedacted(t *testing.T) {
	b := baseline()
	caps := windowsCaps()
	caps.NativeTSF, caps.HookCapability = false, false
	p, err := GenerateOverlay(b, caps, "limited")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Overlay.Shortcuts) != 1 || len(p.Overlay.DeferredRules) != 1 || p.Overlay.DeferredRules[0].State != "unsupported" {
		t.Fatalf("rule silently changed: %+v", p.Overlay)
	}
	raw, _ := MarshalOverlay(p.Overlay)
	if strings.Contains(string(raw), "http://") || strings.Contains(string(raw), "password") {
		t.Fatal("unexpected context")
	}
}

func TestAppRuleInputSourcesAreCapabilityAware(t *testing.T) {
	macWithExtra := macCaps()
	macWithExtra.InputSourceIDs = append(macWithExtra.InputSourceIDs, "com.apple.inputmethod.Kotoeri")
	windowsWithMacSource := windowsCaps()
	windowsWithMacSource.InputSourceIDs = append(windowsWithMacSource.InputSourceIDs, macCaps().InputSourceIDs[0])
	cases := []struct {
		name         string
		caps         DeviceCapabilities
		ruleSource   string
		wantRules    int
		wantDeferred int
	}{
		{"macos-present", macCaps(), macCaps().InputSourceIDs[0], 1, 0},
		{"macos-extra-source", macWithExtra, macCaps().InputSourceIDs[0], 1, 0},
		{"windows-missing", windowsCaps(), macCaps().InputSourceIDs[0], 0, 1},
		{"windows-present", windowsWithMacSource, macCaps().InputSourceIDs[0], 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := baseline()
			b.AppRules[0].InputSourceID = tc.ruleSource
			p, err := GenerateOverlay(b, tc.caps, "device")
			if err != nil {
				t.Fatal(err)
			}
			appDeferred := 0
			for _, deferred := range p.Overlay.DeferredRules {
				if deferred.RuleID == b.AppRules[0].ID {
					appDeferred++
				}
			}
			if len(p.Overlay.AppRules) != tc.wantRules || appDeferred != tc.wantDeferred {
				t.Fatalf("app rule was not capability-aware: %+v", p.Overlay)
			}
			if tc.wantDeferred == 1 {
				rule := p.Overlay.DeferredRules[0]
				if rule.RuleID != b.AppRules[0].ID || rule.State != "unsupported" || !strings.Contains(rule.Reason, "input source") {
					t.Fatalf("missing auditable deferral: %+v", rule)
				}
				foundDiff := false
				for _, diff := range p.Diffs {
					if strings.Contains(diff.Path, "/app-rules/") {
						foundDiff = true
						break
					}
				}
				if !foundDiff {
					t.Fatalf("missing app-rule preview diff: %+v", p.Diffs)
				}
			}
		})
	}
}

func TestOverlayValidationRejectsUnsafeCrossDeviceRules(t *testing.T) {
	p, err := GenerateOverlay(baseline(), windowsCaps(), "device")
	if err != nil {
		t.Fatal(err)
	}
	bad := p.Overlay
	bad.InputSourceID = "not-installed"
	if err := bad.Validate(); err == nil {
		t.Fatal("overlay accepted unavailable selected input source")
	}
	bad = p.Overlay
	bad.Backend = "made-up"
	if err := bad.Validate(); err == nil {
		t.Fatal("overlay accepted unknown backend")
	}
	bad = p.Overlay
	bad.DeferredRules = []DeferredRule{{RuleID: strings.Repeat("r", maxMetadataSize+1), State: "deferred", Reason: "explain"}}
	if err := bad.Validate(); err == nil {
		t.Fatal("overlay accepted oversized deferred rule")
	}
	bad = p.Overlay
	bad.AppRules = []AppRule{{ID: strings.Repeat("r", maxMetadataSize+1), InputSourceID: bad.InputSourceID, Mode: "keep"}}
	if err := bad.Validate(); err == nil {
		t.Fatal("overlay accepted oversized app rule id")
	}
	bad = p.Overlay
	bad.AppRules = []AppRule{{ID: "rule", InputSourceID: bad.InputSourceID, Action: strings.Repeat("a", maxMetadataSize+1)}}
	if err := bad.Validate(); err == nil {
		t.Fatal("overlay accepted oversized app rule field")
	}
	bad = p.Overlay
	bad.Permissions = make(map[string]PermissionState, maxMetadataSize+1)
	for i := 0; i <= maxMetadataSize; i++ {
		bad.Permissions["permission-"+string(rune('a'+i%26))+string(rune(i/26))] = PermissionGranted
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("overlay accepted too many permissions")
	}
}
func TestCapabilityAwareOverlayImport(t *testing.T) {
	windowsTarget := windowsCaps()
	darwinTarget := macCaps()
	// Keep source IDs compatible while isolating backend/OS capability checks.
	darwinTarget.InputSourceIDs = append([]string(nil), windowsTarget.InputSourceIDs...)
	windowsTarget.InputSourceIDs = append([]string(nil), darwinTarget.InputSourceIDs...)

	windowsOverlay, err := GenerateOverlay(baseline(), windowsCaps(), "windows-device")
	if err != nil {
		t.Fatal(err)
	}
	darwinOverlay, err := GenerateOverlay(baseline(), macCaps(), "darwin-device")
	if err != nil {
		t.Fatal(err)
	}
	deferredCaps := windowsCaps()
	deferredCaps.NativeTSF = false
	deferredCaps.HookCapability = false
	deferredOverlay, err := GenerateOverlay(baseline(), deferredCaps, "deferred-device")
	if err != nil {
		t.Fatal(err)
	}
	appRuleSourceOverlay := cloneOverlay(windowsOverlay.Overlay)
	appRuleSourceOverlay.InputSourceIDs = append(appRuleSourceOverlay.InputSourceIDs, "target-missing-source")
	appRuleSourceOverlay.AppRules[0].InputSourceID = "target-missing-source"

	tests := []struct {
		name    string
		overlay DeviceOverlay
		target  DeviceCapabilities
		wantErr bool
		isErr   error
	}{
		{"windows native-tsf on Windows", windowsOverlay.Overlay, windowsCaps(), false, nil},
		{"Windows native-tsf on darwin", windowsOverlay.Overlay, darwinTarget, true, nil},
		{"darwin native-imk on Windows", darwinOverlay.Overlay, windowsTarget, true, nil},
		{"hook capability mismatch", func() DeviceOverlay {
			caps := windowsCaps()
			caps.NativeTSF, caps.HookCapability = false, true
			p, err := GenerateOverlay(baseline(), caps, "hook-device")
			if err != nil {
				t.Fatalf("generate hook overlay: %v", err)
			}
			return p.Overlay
		}(), func() DeviceCapabilities {
			caps := windowsCaps()
			caps.NativeTSF, caps.HookCapability = false, false
			return caps
		}(), true, nil},
		{"missing selected source", windowsOverlay.Overlay, func() DeviceCapabilities {
			caps := windowsCaps()
			caps.InputSourceIDs = []string{"different-source"}
			return caps
		}(), true, nil},
		{"missing app-rule source", appRuleSourceOverlay, windowsCaps(), true, nil},
		{"deferred backend is explicit", deferredOverlay.Overlay, deferredCaps, true, ErrDeferredOverlay},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.overlay.ValidateForCapabilities(tc.target); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateForCapabilities() error=%v, wantErr=%v", err, tc.wantErr)
			} else if tc.isErr != nil && !errors.Is(err, tc.isErr) {
				t.Fatalf("error=%v, want errors.Is(%v)", err, tc.isErr)
			}
		})
	}

	rawOverlay, err := MarshalOverlay(windowsOverlay.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	gotOverlay, err := UnmarshalOverlayForCapabilities(rawOverlay, windowsCaps())
	if err != nil || gotOverlay.Backend != "native-tsf" {
		t.Fatalf("overlay JSON round trip: backend=%q err=%v", gotOverlay.Backend, err)
	}
	export, err := NewExport(baseline(), &windowsOverlay.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	rawExport, err := MarshalExport(export)
	if err != nil {
		t.Fatal(err)
	}
	gotExport, err := UnmarshalExportForCapabilities(rawExport, windowsCaps())
	if err != nil || gotExport.Overlay == nil || gotExport.Overlay.Backend != "native-tsf" {
		t.Fatalf("export JSON round trip: overlay=%+v err=%v", gotExport.Overlay, err)
	}
	if _, err := UnmarshalOverlayForCapabilities(rawOverlay, darwinTarget); err == nil {
		t.Fatal("capability-incompatible overlay accepted during unmarshal")
	}
}

func TestOverlayBackendAndDisplayValidation(t *testing.T) {
	validCases := []struct {
		name    string
		caps    DeviceCapabilities
		backend string
	}{
		{"native-imk", macCaps(), "native-imk"},
		{"native-tsf", windowsCaps(), "native-tsf"},
		{"hook", func() DeviceCapabilities {
			caps := windowsCaps()
			caps.NativeTSF, caps.HookCapability = false, true
			return caps
		}(), "hook"},
		{"deferred", func() DeviceCapabilities {
			caps := windowsCaps()
			caps.NativeTSF, caps.HookCapability = false, false
			return caps
		}(), "deferred"},
	}
	for _, tc := range validCases {
		t.Run("valid-"+tc.name, func(t *testing.T) {
			p, err := GenerateOverlay(baseline(), tc.caps, "device")
			if err != nil {
				t.Fatal(err)
			}
			if p.Overlay.Backend != tc.backend || p.Overlay.HookEnabled != (tc.backend == "hook") {
				t.Fatalf("overlay backend contract not derived from capabilities: %+v", p.Overlay)
			}
			if err := p.Overlay.Validate(); err != nil {
				t.Fatalf("generated overlay invalid: %v", err)
			}
		})
	}

	invalidCases := []struct {
		name        string
		backend     string
		hookEnabled bool
	}{
		{"native-imk-with-hook", "native-imk", true},
		{"native-tsf-with-hook", "native-tsf", true},
		{"deferred-with-hook", "deferred", true},
		{"hook-without-hook", "hook", false},
	}
	for _, tc := range invalidCases {
		t.Run("invalid-"+tc.name, func(t *testing.T) {
			p, err := GenerateOverlay(baseline(), windowsCaps(), "device")
			if err != nil {
				t.Fatal(err)
			}
			bad := p.Overlay
			bad.Backend = tc.backend
			bad.HookEnabled = tc.hookEnabled
			if err := bad.Validate(); err == nil {
				t.Fatal("invalid hook/backend combination accepted")
			}
		})
	}

	displayCases := []struct {
		name    string
		display DisplayInfo
	}{
		{"negative-width", DisplayInfo{Width: -1, Height: 1, Scale: 1}},
		{"negative-height", DisplayInfo{Width: 1, Height: -1, Scale: 1}},
		{"zero-scale", DisplayInfo{Width: 1, Height: 1, Scale: 0}},
		{"negative-scale", DisplayInfo{Width: 1, Height: 1, Scale: -1}},
		{"nan-scale", DisplayInfo{Width: 1, Height: 1, Scale: math.NaN()}},
		{"positive-infinity-scale", DisplayInfo{Width: 1, Height: 1, Scale: math.Inf(1)}},
		{"negative-infinity-scale", DisplayInfo{Width: 1, Height: 1, Scale: math.Inf(-1)}},
	}
	for _, tc := range displayCases {
		t.Run("capabilities-"+tc.name, func(t *testing.T) {
			caps := windowsCaps()
			caps.Display = tc.display
			if err := caps.Validate(); err == nil {
				t.Fatal("invalid display accepted by capabilities")
			}
		})
		t.Run("overlay-"+tc.name, func(t *testing.T) {
			p, err := GenerateOverlay(baseline(), windowsCaps(), "device")
			if err != nil {
				t.Fatal(err)
			}
			p.Overlay.Display = tc.display
			if err := p.Overlay.Validate(); err == nil {
				t.Fatal("invalid display accepted by overlay")
			}
		})
	}
	valid := windowsCaps()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid display rejected: %v", err)
	}
}

func TestAppRuleActionAndModeValidation(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		mode    string
		scheme  string
		wantErr bool
	}{
		{"legacy-scheme", "scheme:quanpin", "", "", false},
		{"legacy-scheme-with-native-mode", "scheme:quanpin", "native", "quanpin", false},
		{"matching-action-and-mode", "native", "native", "", false},
		{"matching-ascii-action-and-mode", "ascii", "ascii", "", false},
		{"matching-keep-action-and-mode", "keep", "keep", "", false},
		{"conflicting-action-and-mode", "ascii", "native", "", true},
		{"conflicting-legacy-scheme-and-mode", "scheme:quanpin", "ascii", "", true},
		{"conflicting-legacy-scheme-and-scheme", "scheme:quanpin", "native", "xiaohe", true},
		{"invalid-action-with-mode", "not-a-mode", "native", "", true},
		{"invalid-legacy-scheme", "scheme:not-a-scheme", "native", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := baseline()
			b.AppRules[0].Action, b.AppRules[0].Mode, b.AppRules[0].Scheme = tc.action, tc.mode, tc.scheme
			if err := b.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestMergeKeepsNewerBaselineWithoutTouchingDeviceState(t *testing.T) {
	source, target := baseline(), baseline()
	source.FieldRevisions["scheme"] = 5
	source.Scheme = "xiaohe"
	target.CandidateCount = 9
	merged, conflicts, err := MergeBaselines(source, target)
	if err != nil || merged.Scheme != "xiaohe" || merged.CandidateCount != 9 || len(conflicts) == 0 {
		t.Fatalf("%+v %+v %v", merged, conflicts, err)
	}
	target.Scheme = "quanpin"
	if merged.Scheme != "xiaohe" {
		t.Fatal("merge result aliases target")
	}
}

func TestMergeRevisionsConvergeAndDictionaryConflictsAreExplicit(t *testing.T) {
	cases := []struct {
		name                           string
		sourceRevision, targetRevision uint64
		wantRevision                   uint64
		wantScheme                     string
	}{
		{"equal", 2, 2, 3, "quanpin"},
		{"source-newer", 5, 2, 5, "xiaohe"},
		{"target-newer", 2, 5, 5, "quanpin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, d := baseline(), baseline()
			s.FieldRevisions["scheme"], d.FieldRevisions["scheme"] = tc.sourceRevision, tc.targetRevision
			s.Scheme, d.Scheme = "xiaohe", "quanpin"
			got, _, err := MergeBaselines(s, d)
			if err != nil || got.FieldRevisions["scheme"] != tc.wantRevision || got.Scheme != tc.wantScheme {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			replay, _, err := MergeBaselines(got, d)
			if err != nil || replay.FieldRevisions["scheme"] != got.FieldRevisions["scheme"] || replay.Scheme != got.Scheme {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
		})
	}
	s, d := baseline(), baseline()
	s.Dictionaries = []DictionaryMetadata{{ID: "same", ContentHash: Digest("one"), Revision: 4}}
	d.Dictionaries = []DictionaryMetadata{{ID: "same", ContentHash: Digest("two"), Revision: 4}, {ID: "other", ContentHash: Digest("one")}}
	got, conflicts, err := MergeBaselines(s, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) == 0 || conflicts[0].Winner != "target" {
		t.Fatalf("missing dictionary conflict: %+v", conflicts)
	}
	if len(got.Dictionaries) != 2 {
		t.Fatalf("dictionary merge lost distinct content: %+v", got.Dictionaries)
	}
	foundTwo := false
	for _, d := range got.Dictionaries {
		if d.ContentHash == Digest("two") {
			foundTwo = true
		}
	}
	if !foundTwo {
		t.Fatalf("dictionary merge lost winner: %+v", got.Dictionaries)
	}
}

func TestNestedRulesAreCopiedAndAppRuleContractIsStrict(t *testing.T) {
	b := baseline()
	p, err := GenerateOverlay(b, windowsCaps(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	p.Overlay.Shortcuts[0].Apps[0].BundleID = "changed"
	p.Overlay.AppRules[0].Apps[0].BundleID = "changed"
	if b.Shortcuts[0].Apps[0].BundleID == "changed" || b.AppRules[0].Apps[0].BundleID == "changed" {
		t.Fatal("overlay aliases baseline apps")
	}
	bad := b
	bad.AppRules[0].Action = "not-a-mode"
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown app mode accepted")
	}
	bad = b
	bad.AppRules[0].Action = "scheme:not-a-scheme"
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown scheme accepted")
	}
}

func TestStrictPayloadAndPreviewPrivacy(t *testing.T) {
	b := baseline()
	if _, err := UnmarshalBaseline([]byte(`{"schemaVersion":1} garbage`)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	tooLarge := strings.Repeat(" ", 1<<20+1)
	if _, err := UnmarshalBaseline([]byte(tooLarge)); err == nil {
		t.Fatal("oversized payload accepted")
	}
	caps := macCaps()
	caps.OS = "macos"
	p, err := GenerateOverlay(b, caps, "device/with-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range p.Diffs {
		if strings.Contains(d.Path, "device/with-secret") || strings.Contains(d.Path, "caps") {
			t.Fatalf("diff path leaked input: %+v", d)
		}
	}
}
