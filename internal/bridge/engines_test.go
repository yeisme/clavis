package bridge

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestDoubleTapDetector_AllSupportedKeysAndRejections(t *testing.T) {
	cases := []struct {
		target string
		press  string
	}{
		{"right_option", "right_alt"},
		{"left_option", "left_opt"},
		{"right_command", "right_win"},
		{"left_command", "left_win"},
		{"right_shift", "rshift"},
		{"left_shift", "lshift"},
		{"option", "left_option"},
		{"command", "right_command"},
		{"shift", "left_shift"},
		{"control", "left_ctrl"},
		{"caps_lock", "capslock"},
	}

	for _, tc := range cases {
		d := NewDoubleTapDetector(tc.target, 300)
		if d.HandleModifier(tc.press, true, false, 100) {
			t.Fatalf("%s: should not trigger on first down", tc.target)
		}
		if d.HandleModifier(tc.press, false, false, 150) {
			t.Fatalf("%s: should not trigger on first up", tc.target)
		}
		if d.HandleModifier(tc.press, true, false, 280) {
			t.Fatalf("%s: should not trigger on second down", tc.target)
		}
		if !d.HandleModifier(tc.press, false, false, 330) {
			t.Fatalf("%s: expected double tap trigger on second up", tc.target)
		}
	}

	// Reject intervening key
	d := NewDoubleTapDetector("right_option", 300)
	d.HandleModifier("right_option", true, false, 100)
	d.HandleModifier("right_option", false, false, 140)
	d.HandleInterveningKey()
	d.HandleModifier("right_option", true, false, 200)
	if d.HandleModifier("right_option", false, false, 230) {
		t.Fatal("expected intervening key to cancel double tap")
	}

	// Reject hold-too-long on first press
	d.Reset()
	d.HandleModifier("right_option", true, false, 100)
	d.HandleModifier("right_option", false, false, 500) // held 400ms > 300ms
	d.HandleModifier("right_option", true, false, 550)
	if d.HandleModifier("right_option", false, false, 600) {
		t.Fatal("expected long hold on first press to be rejected")
	}

	// Reject hold-too-long on second press
	d.Reset()
	d.HandleModifier("right_option", true, false, 100)
	d.HandleModifier("right_option", false, false, 140)
	d.HandleModifier("right_option", true, false, 200)
	if d.HandleModifier("right_option", false, false, 650) {
		t.Fatal("expected long hold on second press to be rejected")
	}

	// Reject opposite-side modifier
	d.Reset()
	d.HandleModifier("right_option", true, false, 100)
	d.HandleModifier("right_option", false, false, 140)
	d.HandleModifier("left_option", true, false, 200)
	d.HandleModifier("left_option", false, false, 230)
	if d.HandleModifier("right_option", true, false, 260) || d.HandleModifier("right_option", false, false, 290) {
		t.Fatal("expected opposite-side modifier to cancel first tap")
	}

	// Reject gap > double_tap_interval_ms (second press starts a fresh tap instead)
	d.Reset()
	d.HandleModifier("right_option", true, false, 100)
	d.HandleModifier("right_option", false, false, 150)
	d.HandleModifier("right_option", true, false, 600) // 450ms gap > 300ms
	if d.HandleModifier("right_option", false, false, 640) {
		t.Fatal("expected expired interval to reject double tap")
	}
	// Follow-up quick tap completes a new double tap
	d.HandleModifier("right_option", true, false, 700)
	if !d.HandleModifier("right_option", false, false, 740) {
		t.Fatal("expected fresh second tap after expired first tap to trigger")
	}
}

func TestHotkeyMatcher(t *testing.T) {
	hk, err := ParseHotkey("option+space")
	if err != nil {
		t.Fatalf("ParseHotkey failed: %v", err)
	}
	if !hk.Matches("space", []string{"alt"}) {
		t.Fatal("expected alt+space to match option+space")
	}
	if !hk.Matches(" ", []string{"right_option"}) {
		t.Fatal("expected right_option+' ' to match option+space")
	}
	if hk.Matches("space", []string{"option", "shift"}) {
		t.Fatal("expected extra shift modifier not to match")
	}

	hk2, err := ParseHotkey("alt+shift+space")
	if err != nil {
		t.Fatal(err)
	}
	if !hk2.Matches("space", []string{"shift", "option"}) {
		t.Fatal("expected shift+option+space to match alt+shift+space")
	}

	hk3, err := ParseHotkey("command+shift+k")
	if err != nil {
		t.Fatal(err)
	}
	if !hk3.Matches("K", []string{"cmd", "shift"}) {
		t.Fatal("expected cmd+shift+K to match command+shift+k")
	}

	hk4, err := ParseHotkey("ctrl+alt+p")
	if err != nil {
		t.Fatal(err)
	}
	if !hk4.Matches("p", []string{"control", "option"}) {
		t.Fatal("expected control+option+p to match ctrl+alt+p")
	}

	for _, bad := range []string{"", "option+", "+space", "option+command", "option+a+b"} {
		if _, err := ParseHotkey(bad); err == nil {
			t.Fatalf("expected ParseHotkey(%q) to fail", bad)
		}
	}
}

func TestKeymapLayerEngine_CapsLockTapHoldNavHyperAndRemaps(t *testing.T) {
	cfg := config.DefaultKeymapConfig()
	cfg.TapHoldThresholdMs = 200
	eng := NewKeymapLayerEngine(cfg)

	// 1. Quick CapsLock tap within TapHoldThresholdMs -> emits Escape on release
	out := eng.HandleKey("caps_lock", true, 100)
	if !out.Consume || out.LayerActive != "nav" || len(out.EmitKeys) != 0 {
		t.Fatalf("expected CapsLock down consumed with nav layer active, got %+v", out)
	}
	out = eng.HandleKey("caps_lock", false, 220) // 120ms <= 200ms
	if !out.Consume || out.LayerActive != "" || len(out.EmitKeys) != 1 || out.EmitKeys[0].KeyName != "escape" {
		t.Fatalf("expected Escape emitted on quick CapsLock release, got %+v", out)
	}

	// 2. CapsLock held longer than TapHoldThresholdMs with no combo -> does NOT emit Escape on release
	eng.HandleKey("caps_lock", true, 300)
	out = eng.HandleKey("caps_lock", false, 600) // 300ms > 200ms
	if !out.Consume || len(out.EmitKeys) != 0 || out.LayerActive != "" {
		t.Fatalf("expected no Escape emitted after long hold, got %+v", out)
	}

	// 3. CapsLock held + all 9 Vim navigation mappings; releasing CapsLock even within threshold does NOT emit Escape
	navCases := []struct {
		in  string
		out string
	}{
		{"h", "left"},
		{"j", "down"},
		{"k", "up"},
		{"l", "right"},
		{"u", "pageup"},
		{"d", "pagedown"},
		{"0", "home"},
		{"4", "end"},
		{"backspace", "delete"},
	}

	eng.HandleKey("caps_lock", true, 1000)
	for i, tc := range navCases {
		ts := int64(1010 + i*10)
		downOut := eng.HandleKey(tc.in, true, ts)
		if !downOut.Consume || downOut.LayerActive != "nav" || len(downOut.EmitKeys) != 1 || downOut.EmitKeys[0].KeyName != tc.out {
			t.Fatalf("nav mapping %s -> %s failed: %+v", tc.in, tc.out, downOut)
		}
		upOut := eng.HandleKey(tc.in, false, ts+5)
		if !upOut.Consume || len(upOut.EmitKeys) != 0 {
			t.Fatalf("nav keyUp %s should be consumed without emitting, got %+v", tc.in, upOut)
		}
	}
	// Release CapsLock within 150ms (< 200ms) after combo keys -> must NOT emit Escape!
	relOut := eng.HandleKey("caps_lock", false, 1150)
	if !relOut.Consume || len(relOut.EmitKeys) != 0 || relOut.LayerActive != "" {
		t.Fatalf("expected no Escape emitted after nav combo, got %+v", relOut)
	}

	// 4. Hyper mode adds ["command", "control", "option", "shift"] to held non-nav keys
	hyperCfg := config.DefaultKeymapConfig()
	hyperCfg.CapsLockHold = "hyper"
	eng.UpdateConfig(hyperCfg)

	out = eng.HandleKey("caps_lock", true, 2000)
	if !out.Consume || out.LayerActive != "hyper" {
		t.Fatalf("expected hyper layer active, got %+v", out)
	}
	out = eng.HandleKey("f", true, 2050)
	wantMods := []string{"command", "control", "option", "shift"}
	if !out.Consume || len(out.EmitKeys) != 1 || out.EmitKeys[0].KeyName != "f" || !reflect.DeepEqual(out.EmitKeys[0].Modifiers, wantMods) {
		t.Fatalf("expected hyper chord on 'f', got %+v", out)
	}
	eng.HandleKey("f", false, 2070)
	out = eng.HandleKey("caps_lock", false, 2100)
	if !out.Consume || len(out.EmitKeys) != 0 {
		t.Fatalf("expected no Escape on release after hyper combo, got %+v", out)
	}

	// 5. Custom cfg.Remaps with AppBundleIDs filtering
	remapCfg := config.DefaultKeymapConfig()
	remapCfg.Remaps = []config.KeyRemapRule{
		{
			From:         "space",
			Tap:          "space",
			Hold:         "nav",
			AppBundleIDs: []string{"com.apple.Terminal"},
		},
	}
	eng.UpdateConfig(remapCfg)

	// Non-matching app passes space through unconsumed
	if out := eng.HandleKeyEvent("space", true, nil, "com.apple.Safari", 3000); out.Consume {
		t.Fatalf("expected space not consumed in Safari, got %+v", out)
	}
	// Matching app activates tap/hold on space
	out = eng.HandleKeyEvent("space", true, nil, "com.apple.Terminal", 3100)
	if !out.Consume || out.LayerActive != "nav" {
		t.Fatalf("expected space hold nav layer in Terminal, got %+v", out)
	}
	out = eng.HandleKeyEvent("space", false, nil, "com.apple.Terminal", 3200)
	if !out.Consume || len(out.EmitKeys) != 1 || out.EmitKeys[0].KeyName != "space" {
		t.Fatalf("expected space emitted on quick tap in Terminal, got %+v", out)
	}
}

func TestScreenGeometryClamp(t *testing.T) {
	// Quartz <-> AppKit roundtrip
	primaryHeight := 1080.0
	quartz := Rect{X: 100, Y: 200, Width: 680, Height: 420}
	appkit := QuartzToAppKitRect(quartz, primaryHeight)
	if appkit.Y != 460 {
		t.Fatalf("expected AppKit Y=460, got %v", appkit.Y)
	}
	back := AppKitToQuartzRect(appkit, primaryHeight)
	if math.Abs(back.Y-quartz.Y) > 1e-9 || back != quartz {
		t.Fatalf("expected roundtrip %+v, got %+v", quartz, back)
	}

	displays := []DisplayInfo{
		{
			ID:       1,
			Bounds:   Rect{X: 0, Y: 0, Width: 1920, Height: 1080},
			WorkArea: Rect{X: 0, Y: 25, Width: 1920, Height: 1005},
			Primary:  true,
		},
		{
			ID:       2,
			Bounds:   Rect{X: 1920, Y: 0, Width: 1440, Height: 900},
			WorkArea: Rect{X: 1920, Y: 25, Width: 1440, Height: 875},
		},
	}

	// Normal below-caret placement
	popSize := Size{Width: 680, Height: 300}
	p1 := PlacePopover(Rect{X: 200, Y: 100, Width: 2, Height: 20}, popSize, displays)
	if p1.X != 200 || p1.Y != 126 {
		t.Fatalf("unexpected normal placement: %+v", p1)
	}

	// Bottom overflow flips above caret
	p2 := PlacePopover(Rect{X: 300, Y: 850, Width: 2, Height: 20}, popSize, displays)
	if p2.Y != 850-300-PopoverAnchorGap {
		t.Fatalf("expected flip above caret at Y=%v, got %+v", 850-300-PopoverAnchorGap, p2)
	}

	// Right-edge clamp on second monitor with 12pt margin
	p3 := PlacePopover(Rect{X: 3200, Y: 200, Width: 2, Height: 20}, popSize, displays)
	expectedMaxX := 1920.0 + 1440.0 - 680.0 - PopoverScreenMargin
	if p3.X != expectedMaxX {
		t.Fatalf("expected clamped X=%v on display 2, got %+v", expectedMaxX, p3)
	}
}

func TestIMESwitchDecisionEngine(t *testing.T) {
	rules := config.IMERulesConfig{
		Rules: []config.IMERule{
			{
				BundleID:       "com.apple.Terminal",
				ProcessName:    "Terminal",
				InputSourceID:  "com.apple.keylayout.ABC",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
			{
				ProcessName:    "WindowsTerminal.exe",
				InputSourceID:  "0x0409",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
		},
	}

	eng := NewIMESwitchDecisionEngine(true, rules)

	// Start in Notes with Chinese IME
	d := eng.EvaluateAppActivation(AppIdentity{BundleID: "com.apple.Notes", ProcessName: "Notes"}, "com.apple.inputmethod.SCIM.ITABC")
	if d.ShouldSwitch {
		t.Fatalf("unexpected switch on non-rule app: %+v", d)
	}

	// Enter Terminal -> switch to ABC and save Chinese IME
	d = eng.EvaluateAppActivation(AppIdentity{BundleID: "com.apple.Terminal", ProcessName: "Terminal"}, "com.apple.inputmethod.SCIM.ITABC")
	if !d.ShouldSwitch || d.TargetInputSourceID != "com.apple.keylayout.ABC" {
		t.Fatalf("expected switch to ABC, got %+v", d)
	}

	// Consecutive rule-matched app (matched by process_name without .exe!) -> preserve pre-rule Chinese IME!
	d = eng.EvaluateAppActivation(AppIdentity{ProcessName: "WindowsTerminal"}, "com.apple.keylayout.ABC")
	if !d.ShouldSwitch || d.TargetInputSourceID != "0x0409" {
		t.Fatalf("expected switch to 0x0409, got %+v", d)
	}

	// Leave to non-rule app -> restore original pre-rule Chinese IME
	d = eng.EvaluateAppActivation(AppIdentity{BundleID: "com.apple.TextEdit", ProcessName: "TextEdit"}, "0x0409")
	if !d.ShouldSwitch || !d.Restored || d.TargetInputSourceID != "com.apple.inputmethod.SCIM.ITABC" {
		t.Fatalf("expected restore to SCIM.ITABC, got %+v", d)
	}
}

func TestIMESwitchDecisionEngine_URLTitleSpecificityAndTemporaryLock(t *testing.T) {
	// Configure rules in mixed order to verify specificity priority:
	// 1. URLPattern > 2. TitlePattern > 3. BundleID > 4. ProcessName
	rules := config.IMERulesConfig{
		Rules: []config.IMERule{
			// Tier 4: ProcessName only
			{
				ProcessName:    "Safari",
				InputSourceID:  "ime.proc.default",
				PreferredMode:  config.IMEModeNative,
				RestoreOnLeave: true,
			},
			// Tier 3: BundleID only
			{
				BundleID:       "com.apple.Safari",
				InputSourceID:  "ime.bundle.default",
				PreferredMode:  config.IMEModeNative,
				RestoreOnLeave: true,
			},
			// Tier 2: TitlePattern regex
			{
				BundleID:       "com.apple.Safari",
				TitlePattern:   `(?i)\b(vim|neovim| pull request)\b`,
				InputSourceID:  "ime.title.ascii",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
			// Tier 1a: URLPattern host match
			{
				BundleID:       "com.apple.Safari",
				URLPattern:     "github.com",
				InputSourceID:  "ime.url.github",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
			// Tier 1b: URLPattern wildcard domain match
			{
				BundleID:       "com.apple.Safari",
				URLPattern:     "*.docs.rs",
				InputSourceID:  "ime.url.docsrs",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
		},
	}

	eng := NewIMESwitchDecisionEngine(true, rules)

	// 1. URLPattern wins over TitlePattern, BundleID, and ProcessName
	d := eng.EvaluateAppActivation(AppIdentity{
		BundleID:    "com.apple.Safari",
		ProcessName: "Safari",
		WindowTitle: "Edit in Vim Mode",
		ActiveURL:   "https://github.com/yeisme/clavis",
	}, "ime.initial")
	if !d.ShouldSwitch || d.TargetInputSourceID != "ime.url.github" {
		t.Fatalf("expected URLPattern github.com to win, got %+v", d)
	}

	// Wildcard *.docs.rs matches subdomain
	d = eng.EvaluateAppActivation(AppIdentity{
		BundleID:    "com.apple.Safari",
		ProcessName: "Safari",
		WindowTitle: "tokio - Rust",
		ActiveURL:   "https://tokio.docs.rs/tokio/index.html",
	}, "ime.url.github")
	if !d.ShouldSwitch || d.TargetInputSourceID != "ime.url.docsrs" {
		t.Fatalf("expected URLPattern *.docs.rs to match, got %+v", d)
	}

	// 2. TitlePattern wins over BundleID and ProcessName when URL does not match
	d = eng.EvaluateAppActivation(AppIdentity{
		BundleID:    "com.apple.Safari",
		ProcessName: "Safari",
		WindowTitle: "Editing Buffer in Neovim",
		ActiveURL:   "https://example.org/editor",
	}, "ime.url.docsrs")
	if !d.ShouldSwitch || d.TargetInputSourceID != "ime.title.ascii" {
		t.Fatalf("expected TitlePattern to win over BundleID, got %+v", d)
	}

	// 3. BundleID wins over ProcessName when neither URL nor Title matches
	d = eng.EvaluateAppActivation(AppIdentity{
		BundleID:    "com.apple.Safari",
		ProcessName: "Safari",
		WindowTitle: "News Feed",
		ActiveURL:   "https://news.example.com",
	}, "ime.title.ascii")
	if !d.ShouldSwitch || d.TargetInputSourceID != "ime.bundle.default" {
		t.Fatalf("expected BundleID to win over ProcessName, got %+v", d)
	}

	// 4. ProcessName matches when BundleID is absent
	d = eng.EvaluateAppActivation(AppIdentity{
		ProcessName: "Safari.exe",
		WindowTitle: "News Feed",
	}, "ime.bundle.default")
	if !d.ShouldSwitch || d.TargetInputSourceID != "ime.proc.default" {
		t.Fatalf("expected ProcessName fallback to match, got %+v", d)
	}

	// 5. TemporaryLockASCII / RestoreTemporaryLock is independent of app-rule stack
	lockDec := eng.TemporaryLockASCII("com.apple.keylayout.ABC", "ime.proc.default")
	if !lockDec.ShouldSwitch || lockDec.TargetInputSourceID != "com.apple.keylayout.ABC" {
		t.Fatalf("expected TemporaryLockASCII to switch to ABC, got %+v", lockDec)
	}
	restoreTemp := eng.RestoreTemporaryLock("com.apple.keylayout.ABC")
	if !restoreTemp.ShouldSwitch || !restoreTemp.Restored || restoreTemp.TargetInputSourceID != "ime.proc.default" {
		t.Fatalf("expected RestoreTemporaryLock to restore ime.proc.default, got %+v", restoreTemp)
	}

	// Leaving the rule session still restores the original pre-rule "ime.initial"
	leaveDec := eng.EvaluateAppActivation(AppIdentity{
		BundleID:    "com.apple.Notes",
		ProcessName: "Notes",
	}, "ime.proc.default")
	if !leaveDec.ShouldSwitch || !leaveDec.Restored || leaveDec.TargetInputSourceID != "ime.initial" {
		t.Fatalf("expected app-rule leave to restore ime.initial, got %+v", leaveDec)
	}
}

func TestWindowLayoutCalculator(t *testing.T) {
	displays := []DisplayInfo{
		{ID: 1, Bounds: Rect{0, 0, 1920, 1080}, WorkArea: Rect{0, 25, 1920, 1005}, Primary: true},
		{ID: 2, Bounds: Rect{1920, 0, 1280, 720}, WorkArea: Rect{1920, 0, 1280, 720}},
	}
	calc := NewWindowLayoutCalculator()
	orig := Rect{X: 120, Y: 140, Width: 800, Height: 600}

	checkCmd := func(cmd config.WindowCommand, cur Rect) Rect {
		t.Helper()
		r, ok, err := calc.ComputeTargetRect("win-1", cur, cmd, displays)
		if err != nil || !ok {
			t.Fatalf("ComputeTargetRect(%s) failed: ok=%v err=%v", cmd, ok, err)
		}
		return r
	}

	left := checkCmd(config.WindowLeftHalf, orig)
	if left != (Rect{0, 25, 960, 1005}) {
		t.Fatalf("left_half = %+v", left)
	}
	right := checkCmd(config.WindowRightHalf, left)
	if right != (Rect{960, 25, 960, 1005}) {
		t.Fatalf("right_half = %+v", right)
	}
	top := checkCmd(config.WindowTopHalf, right)
	if top != (Rect{0, 25, 1920, 502}) {
		t.Fatalf("top_half = %+v", top)
	}
	bottom := checkCmd(config.WindowBottomHalf, top)
	if bottom != (Rect{0, 527, 1920, 503}) {
		t.Fatalf("bottom_half = %+v", bottom)
	}
	maxRect := checkCmd(config.WindowMaximize, bottom)
	if maxRect != (Rect{0, 25, 1920, 1005}) {
		t.Fatalf("maximize = %+v", maxRect)
	}
	centered := checkCmd(config.WindowCenter, orig)
	if centered.Width != 800 || centered.Height != 600 || centered.X != 560 {
		t.Fatalf("center = %+v", centered)
	}
	nextDisp := checkCmd(config.WindowNextDisplay, Rect{100, 100, 800, 600})
	if nextDisp.X < 1920 || nextDisp.MaxX() > 3200 {
		t.Fatalf("next_display = %+v", nextDisp)
	}
	prevDisp := checkCmd(config.WindowPreviousDisplay, nextDisp)
	if prevDisp.X < 0 || prevDisp.MaxX() > 1920 {
		t.Fatalf("previous_display = %+v", prevDisp)
	}

	// Restore returns original pre-tiling frame even after multiple tiling commands
	restored := checkCmd(config.WindowRestore, prevDisp)
	if restored != orig {
		t.Fatalf("expected restore to %+v, got %+v", orig, restored)
	}
	// Second restore is a no-op
	if _, ok, err := calc.ComputeTargetRect("win-1", restored, config.WindowRestore, displays); err != nil || ok {
		t.Fatalf("expected second restore to be no-op, got ok=%v err=%v", ok, err)
	}
}

type fakePasteboardBackend struct {
	changeCount int64
	snap        PasteboardSnapshot
}

func (f *fakePasteboardBackend) ChangeCount() int64 { return f.changeCount }
func (f *fakePasteboardBackend) Capture() (PasteboardSnapshot, error) {
	return f.snap, nil
}
func (f *fakePasteboardBackend) Restore(snap PasteboardSnapshot) error {
	f.changeCount++
	f.snap = snap
	return nil
}

func TestPasteboardSnapshotRestorer(t *testing.T) {
	backend := &fakePasteboardBackend{
		changeCount: 10,
		snap: PasteboardSnapshot{
			ChangeCount: 10,
			Items: []PasteboardItemSnapshot{
				{
					Representations: []PasteboardRepresentation{
						{Type: "public.utf8-plain-text", Data: []byte("hello")},
						{Type: "public.html", Data: []byte("<b>hello</b>")},
					},
				},
				{
					Representations: []PasteboardRepresentation{
						{Type: "public.png", Data: []byte{1, 2, 3}},
					},
				},
			},
		},
	}

	restorer := NewPasteboardSnapshotRestorer(backend)
	captured, err := restorer.Capture()
	if err != nil {
		t.Fatal(err)
	}

	// Simulate self write advancing changeCount to 11
	backend.changeCount = 11
	backend.snap = PasteboardSnapshot{ChangeCount: 11, Items: []PasteboardItemSnapshot{{Representations: []PasteboardRepresentation{{Type: "public.utf8-plain-text", Data: []byte("injected")}}}}}

	restored, err := restorer.RestoreIfUnmodified(captured, 11)
	if err != nil || !restored {
		t.Fatalf("expected restore when changeCount matches 11, got restored=%v err=%v", restored, err)
	}
	if len(backend.snap.Items) != 2 || !bytes.Equal(backend.snap.Items[0].Representations[1].Data, []byte("<b>hello</b>")) {
		t.Fatalf("multi-item multi-format snapshot not restored accurately: %+v", backend.snap)
	}

	// Simulate external clipboard change advancing changeCount to 13
	backend.changeCount = 13
	restored, err = restorer.RestoreIfUnmodified(captured, 12)
	if err != nil || restored {
		t.Fatalf("expected restore to be skipped when external changeCount advanced, got restored=%v err=%v", restored, err)
	}
}

func TestIMESwitchDecisionEngine_ModeOnlyRulesAndSelfAppIgnore(t *testing.T) {
	rules := config.IMERulesConfig{
		Rules: []config.IMERule{
			// Mode-only rule: no InputSourceID, only a preferred conversion mode.
			{
				BundleID:       "com.apple.Terminal",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
			{
				BundleID:       "com.apple.Notes",
				PreferredMode:  config.IMEModeNative,
				RestoreOnLeave: true,
			},
		},
	}

	eng := NewIMESwitchDecisionEngine(true, rules)

	// Mode-only ASCII rule switches the conversion mode without naming an input source.
	d := eng.EvaluateAppActivation(
		AppIdentity{BundleID: "com.apple.Terminal", ProcessName: "Terminal"},
		"com.apple.inputmethod.SCIM.ITABC",
	)
	if !d.ShouldSwitch || d.TargetMode != config.IMEModeASCII || d.TargetInputSourceID != "" {
		t.Fatalf("expected mode-only ASCII switch, got %+v", d)
	}

	// Clavis itself must never trigger auto-switching (self-activation would fight the user).
	for _, self := range []AppIdentity{
		{BundleID: "com.yeisme.clavis", ProcessName: "Clavis"},
		{BundleID: "com.yeisme.clavis", ProcessName: "clavis.exe"},
		{ProcessName: "Clavis"},
	} {
		if d := eng.EvaluateAppActivation(self, "com.apple.keylayout.ABC"); d.ShouldSwitch {
			t.Fatalf("expected self app %+v to be ignored, got %+v", self, d)
		}
	}

	// A mode-only native rule switches back via TargetMode only.
	d = eng.EvaluateAppActivation(
		AppIdentity{BundleID: "com.apple.Notes", ProcessName: "Notes"},
		"com.apple.keylayout.ABC",
	)
	if !d.ShouldSwitch || d.TargetMode != config.IMEModeNative || d.TargetInputSourceID != "" {
		t.Fatalf("expected mode-only native switch, got %+v", d)
	}

	// A "keep" preference never switches.
	keepEng := NewIMESwitchDecisionEngine(true, config.IMERulesConfig{
		Rules: []config.IMERule{
			{BundleID: "com.apple.Terminal", PreferredMode: config.IMEModeKeep},
		},
	})
	if d := keepEng.EvaluateAppActivation(AppIdentity{BundleID: "com.apple.Terminal"}, "com.apple.keylayout.ABC"); d.ShouldSwitch {
		t.Fatalf("expected PreferredMode keep to never switch, got %+v", d)
	}
}

func TestIMESwitchDecisionEngine_TemporaryLockPreservesNativeAndAppRuleStack(t *testing.T) {
	eng := NewIMESwitchDecisionEngine(true, config.IMERulesConfig{
		Rules: []config.IMERule{
			{
				BundleID:       "com.apple.Terminal",
				InputSourceID:  "com.apple.keylayout.ABC",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
		},
	})

	// Windows-style shared HKL: native conversion mode active under the SAME input source ID,
	// so the lock must be expressible as a mode-only switch.
	const sharedHKL = "0x08040804"
	lock := eng.TemporaryLockASCIIWithState(sharedHKL, sharedHKL, true)
	if !lock.ShouldSwitch || lock.TargetInputSourceID != "" || lock.TargetMode != config.IMEModeASCII {
		t.Fatalf("expected mode-only ASCII temporary lock, got %+v", lock)
	}
	if !eng.IsTemporaryLocked() {
		t.Fatal("expected temporary lock to be active")
	}
	// Locking twice must keep the ORIGINAL saved state (no overwrite churn).
	if again := eng.TemporaryLockASCIIWithState(sharedHKL, sharedHKL, false); again.ShouldSwitch {
		t.Fatalf("expected second lock to be a no-op, got %+v", again)
	}

	restore := eng.RestoreTemporaryLock(sharedHKL)
	if !restore.ShouldSwitch || !restore.Restored || restore.TargetMode != config.IMEModeNative || restore.TargetInputSourceID != "" {
		t.Fatalf("expected native mode restore, got %+v", restore)
	}
	if eng.IsTemporaryLocked() {
		t.Fatal("expected temporary lock to be cleared after restore")
	}
	if extra := eng.RestoreTemporaryLock(sharedHKL); extra.ShouldSwitch {
		t.Fatalf("expected restore to be idempotent, got %+v", extra)
	}

	// Distinct ASCII source: lock remembers the CJK source and restores it later.
	eng2 := NewIMESwitchDecisionEngine(true, config.IMERulesConfig{
		Rules: []config.IMERule{
			{
				BundleID:       "com.apple.Terminal",
				InputSourceID:  "com.apple.keylayout.ABC",
				PreferredMode:  config.IMEModeASCII,
				RestoreOnLeave: true,
			},
		},
	})
	const cjk = "com.apple.inputmethod.SCIM.ITABC"
	const abc = "com.apple.keylayout.ABC"

	lock2 := eng2.TemporaryLockASCIIWithState(abc, cjk, true)
	if !lock2.ShouldSwitch || lock2.TargetInputSourceID != abc {
		t.Fatalf("expected temporary lock to ABC, got %+v", lock2)
	}

	// While the temporary lock is active the app-rule stack keeps the user's real pre-lock source:
	// the rule target is already the current source, so no extra switch is emitted, but the session
	// is entered with the pre-lock CJK source remembered.
	if d := eng2.EvaluateAppActivation(AppIdentity{BundleID: "com.apple.Terminal", ProcessName: "Terminal"}, abc); d.ShouldSwitch {
		t.Fatalf("expected no redundant switch while already ASCII, got %+v", d)
	}

	restore2 := eng2.RestoreTemporaryLock(abc)
	if !restore2.ShouldSwitch || !restore2.Restored || restore2.TargetInputSourceID != cjk || restore2.TargetMode != config.IMEModeNative {
		t.Fatalf("expected temporary restore to CJK, got %+v", restore2)
	}

	// Leaving the rule app still restores the true pre-rule source, proving the lock never
	// corrupted the app-rule restore stack.
	leave := eng2.EvaluateAppActivation(AppIdentity{BundleID: "com.apple.Notes", ProcessName: "Notes"}, abc)
	if !leave.ShouldSwitch || !leave.Restored || leave.TargetInputSourceID != cjk {
		t.Fatalf("expected app-rule leave to restore CJK, got %+v", leave)
	}
}
