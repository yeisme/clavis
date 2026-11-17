package inputmigration

import (
	"strings"
	"testing"
)

func baseline() PersonalBaseline {
	b := NewPersonalBaseline("acct")
	b.Revision, b.Scheme, b.CandidateCount, b.ShiftToggleASCII = 3, "quanpin", 7, true
	b.FuzzyPinyin = []string{"zh=z", "ch=c", "sh=s"}
	b.Dictionaries = []DictionaryMetadata{{ID: "custom", ContentHash: strings.Repeat("ab", 32), Format: "clavis-dict", EntryCount: 4, SourceDigest: Digest("dict-source")}}
	b.Learning = LearningData{Enabled: true, Words: []LearningEntry{NewLearningEntry("nihao", "你好", 8, true)}, Bigrams: []BigramEntry{NewBigramEntry("你好", "世界", 3)}, Aggregates: []LearningEntry{NewLearningEntry("nihao", "你好", 11, false)}}
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
func TestMergeKeepsNewerBaselineWithoutTouchingDeviceState(t *testing.T) {
	source, target := baseline(), baseline()
	source.FieldRevisions["scheme"] = 5
	source.Scheme = "shuangpin"
	target.CandidateCount = 9
	merged, conflicts, err := MergeBaselines(source, target)
	if err != nil || merged.Scheme != "shuangpin" || merged.CandidateCount != 9 || len(conflicts) == 0 {
		t.Fatalf("%+v %+v %v", merged, conflicts, err)
	}
	target.Scheme = "quanpin"
	if merged.Scheme != "shuangpin" {
		t.Fatal("merge result aliases target")
	}
}
