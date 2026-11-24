package bridge

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestIsWinHGlobalClipboardFormat(t *testing.T) {
	disallowed := []uint32{
		0,
		winCFBitmap,     // CF_BITMAP = 2
		winCFMetafile,   // CF_METAFILEPICT = 3
		winCFPalette,    // CF_PALETTE = 9
		winCFEnhMeta,    // CF_ENHMETAFILE = 14
		winCFOwnerDisp,  // CF_OWNERDISPLAY = 0x0080
		winCFDSPText,    // CF_DSPTEXT = 0x0081
		winCFDSPBitmap,  // CF_DSPBITMAP = 0x0082
		winCFDSPMeta,    // CF_DSPMETAFILEPICT = 0x0083
		winCFDSPEnhMeta, // CF_DSPENHMETAFILE = 0x008E
		0x0200,          // CF_PRIVATEFIRST
		0x02FF,          // CF_PRIVATELAST
		0x0300,          // CF_GDIOBJFIRST
		0x03FF,          // CF_GDIOBJLAST
	}
	for _, id := range disallowed {
		if isWinHGlobalClipboardFormat(id) {
			t.Fatalf("format 0x%X (%d) must be rejected as non-HGLOBAL", id, id)
		}
	}

	allowed := []uint32{
		1,      // CF_TEXT
		7,      // CF_OEMTEXT
		8,      // CF_DIB
		13,     // CF_UNICODETEXT
		15,     // CF_HDROP
		16,     // CF_LOCALE
		17,     // CF_DIBV5
		0xC000, // registered format min
		0xC123, // registered format (e.g. HTML Format / PNG / vscode-editor-data)
		0xFFFF, // registered format max
	}
	for _, id := range allowed {
		if !isWinHGlobalClipboardFormat(id) {
			t.Fatalf("format 0x%X (%d) must be allowed as HGLOBAL", id, id)
		}
	}
}

func TestWinShiftTrackerTransitions(t *testing.T) {
	t.Run("standalone left and right shift taps toggle", func(t *testing.T) {
		var tr winShiftTracker
		tr.OnShiftDown(winVKLShift, false)
		if !tr.Armed() {
			t.Fatal("expected armed after lone LShift down")
		}
		if !tr.OnShiftUp(winVKLShift, false) {
			t.Fatal("expected lone LShift up to toggle")
		}
		if tr.Armed() {
			t.Fatal("must not stay armed after up")
		}

		tr.OnShiftDown(winVKRShift, false)
		if !tr.OnShiftUp(winVKRShift, false) {
			t.Fatal("expected lone RShift up to toggle")
		}
	})

	t.Run("Shift+Alt and Ctrl+Shift never toggle", func(t *testing.T) {
		var tr winShiftTracker
		// Shift pressed first, then Alt pressed/released (Windows Alt+Shift language switch).
		tr.OnShiftDown(winVKLShift, false)
		tr.OnNonShiftModifier()
		if tr.Armed() {
			t.Fatal("non-shift modifier must disarm shift tap")
		}
		tr.OnNonShiftModifier() // Alt up
		if tr.OnShiftUp(winVKLShift, false) {
			t.Fatal("Shift+Alt must not toggle ASCII")
		}

		// Ctrl pressed first, then Shift down while otherModDown=true, then Ctrl up, then Shift up.
		tr.OnNonShiftModifier()
		tr.OnShiftDown(winVKLShift, true)
		tr.OnNonShiftModifier()
		if tr.OnShiftUp(winVKLShift, false) {
			t.Fatal("Ctrl+Shift must not toggle ASCII")
		}
	})

	t.Run("Shift+key and key auto-repeat stay tainted", func(t *testing.T) {
		var tr winShiftTracker
		tr.OnShiftDown(winVKLShift, false)
		tr.OnInterveningKey()
		tr.OnShiftDown(winVKLShift, false) // OS key repeat while Shift stays held
		if tr.Armed() {
			t.Fatal("auto-repeat after intervening key must not re-arm")
		}
		if tr.OnShiftUp(winVKLShift, false) {
			t.Fatal("Shift+A must not toggle ASCII")
		}
	})

	t.Run("holding left shift and tapping right shift does not toggle", func(t *testing.T) {
		var tr winShiftTracker
		tr.OnShiftDown(winVKLShift, false)
		tr.OnShiftDown(winVKRShift, true)
		if tr.OnShiftUp(winVKRShift, true) {
			t.Fatal("LShift+RShift must not toggle on RShift up")
		}
		if tr.OnShiftUp(winVKLShift, false) {
			t.Fatal("LShift+RShift must not toggle on LShift up")
		}
	})

	t.Run("double-tap Reset leaves no stale shiftAlone state", func(t *testing.T) {
		var tr winShiftTracker
		tr.OnShiftDown(winVKRShift, false)
		tr.Reset() // double-tap consumed the gesture
		if tr.Armed() {
			t.Fatal("Reset must disarm tracker")
		}
		if tr.OnShiftUp(winVKRShift, false) {
			t.Fatal("stale shift up after Reset must not toggle")
		}
	})
}

func TestWinInputEngineEligibleRespectsForcedAndGuards(t *testing.T) {
	cases := []struct {
		name      string
		forced    bool
		nativeIME bool
		secure    bool
		selfApp   bool
		want      bool
	}{
		{"ascii normal external", false, false, false, false, true},
		{"native IME blocked when unforced", false, true, false, false, false},
		{"native IME allowed when forced", true, true, false, false, true},
		{"secure field always blocked even when forced", true, false, true, false, false},
		{"clavis self app always blocked even when forced", true, false, false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := winInputEngineEligible(tc.forced, tc.nativeIME, tc.secure, tc.selfApp); got != tc.want {
				t.Fatalf("winInputEngineEligible(%v,%v,%v,%v) = %v, want %v",
					tc.forced, tc.nativeIME, tc.secure, tc.selfApp, got, tc.want)
			}
		})
	}
}

func TestValidateWinOpenAppTarget(t *testing.T) {
	valid := []string{
		"notepad.exe",
		"Code",
		`C:\Program Files (x86)\App\tool.exe`,
		`shell:AppsFolder\Microsoft.WindowsTerminal_8wekyb3d8bbwe!App`,
		"https://example.com/search?a=1&b=2",
		"ms-settings:keyboard",
	}
	for _, target := range valid {
		got, err := validateWinOpenAppTarget("  " + target + "  ")
		if err != nil || got != target {
			t.Fatalf("validateWinOpenAppTarget(%q) = (%q, %v), want (%q, nil)", target, got, err, target)
		}
	}

	invalid := []string{
		"",
		"   ",
		"notepad & calc.exe",
		"notepad && calc.exe",
		"notepad | calc.exe",
		"notepad;calc.exe",
		"notepad > out.txt",
		"notepad < in.txt",
		"notepad\ncalc.exe",
		"notepad\x00calc.exe",
		`"C:\Windows\notepad.exe"`,
		"notepad ^& calc.exe",
		"notepad`calc`",
	}
	for _, target := range invalid {
		if got, err := validateWinOpenAppTarget(target); err == nil {
			t.Fatalf("validateWinOpenAppTarget(%q) succeeded with %q, want error", target, got)
		}
	}

	// Batch/PowerShell scripts and URIs must not be returned as direct executable fallback candidates.
	if cands := winOpenAppExecCandidates("https://example.com"); len(cands) != 0 {
		t.Fatalf("URI must not produce exec candidates: %v", cands)
	}
	if cands := winOpenAppExecCandidates("script.bat"); len(cands) != 0 {
		t.Fatalf(".bat must not produce exec candidates: %v", cands)
	}
	if cands := winOpenAppExecCandidates("notepad"); !slices.Equal(cands, []string{"notepad.exe", "notepad"}) {
		t.Fatalf("bare name candidates = %v", cands)
	}
	if !isWinDirectExecutablePath(`C:\Windows\System32\notepad.exe`) || isWinDirectExecutablePath(`C:\temp\evil.cmd`) {
		t.Fatal("direct executable filter mismatch")
	}
}

func TestIsWinCopyProbeUnsafe(t *testing.T) {
	cases := []struct {
		name   string
		proc   string
		cls    string
		secure bool
		self   bool
		want   bool
	}{
		{"normal editor safe", "Code.exe", "Chrome_WidgetWin_1", false, false, false},
		{"notepad safe", "notepad.exe", "Notepad", false, false, false},
		{"secure field unsafe", "notepad.exe", "Edit", true, false, true},
		{"self app unsafe", "clavis.exe", "Window", false, true, true},
		{"empty process unsafe", "", "", false, false, true},
		{"windows terminal unsafe", "WindowsTerminal.exe", "CASCADIA_HOSTING_WINDOW_CLASS", false, false, true},
		{"conhost unsafe", "conhost.exe", "ConsoleWindowClass", false, false, true},
		{"cmd unsafe", "CMD.EXE", "", false, false, true},
		{"powershell unsafe", "pwsh.exe", "", false, false, true},
		{"console class overrides process", "custom.exe", "ConsoleWindowClass", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWinCopyProbeUnsafe(tc.proc, tc.cls, tc.secure, tc.self); got != tc.want {
				t.Fatalf("isWinCopyProbeUnsafe(%q,%q,%v,%v) = %v, want %v",
					tc.proc, tc.cls, tc.secure, tc.self, got, tc.want)
			}
		})
	}
}

func TestCaptureWinEditableFieldWithOps(t *testing.T) {
	ctx := context.Background()

	t.Run("active selection avoids Ctrl+A and preserves selection", func(t *testing.T) {
		selectAllCalled := false
		snap, err := captureWinEditableFieldWithOps(ctx, winCaptureFieldOps{
			isSecure:       func() bool { return false },
			isUnsafeTarget: func() bool { return false },
			probeSelection: func(context.Context, time.Duration) (string, error) { return "selected span", nil },
			selectAll:      func(context.Context) error { selectAllCalled = true; return nil },
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if selectAllCalled {
			t.Fatal("selectAll must not be called when text is already selected")
		}
		if snap.SelectedText != "selected span" || snap.Value != "selected span" {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
	})

	t.Run("no selection falls back to selectAll + probe for whole field", func(t *testing.T) {
		var steps []string
		probes := 0
		snap, err := captureWinEditableFieldWithOps(ctx, winCaptureFieldOps{
			isSecure:       func() bool { return false },
			isUnsafeTarget: func() bool { return false },
			probeSelection: func(context.Context, time.Duration) (string, error) {
				probes++
				steps = append(steps, "probe")
				if probes == 1 {
					return "", nil
				}
				return "entire field text", nil
			},
			selectAll: func(context.Context) error {
				steps = append(steps, "select_all")
				return nil
			},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !slices.Equal(steps, []string{"probe", "select_all", "probe"}) {
			t.Fatalf("steps = %v", steps)
		}
		if snap.Value != "entire field text" || snap.SelectedText != "" {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
	})

	t.Run("secure input aborts before probing", func(t *testing.T) {
		probed := false
		_, err := captureWinEditableFieldWithOps(ctx, winCaptureFieldOps{
			isSecure:       func() bool { return true },
			probeSelection: func(context.Context, time.Duration) (string, error) { probed = true; return "secret", nil },
		})
		if err == nil || probed {
			t.Fatalf("expected secure input rejection, err=%v probed=%v", err, probed)
		}
	})

	t.Run("cancelled context aborts immediately", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := captureWinEditableFieldWithOps(cctx, winCaptureFieldOps{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestReplaceWinEditableFieldWithOps(t *testing.T) {
	ctx := context.Background()

	t.Run("selection only skips Ctrl+A and pastes", func(t *testing.T) {
		var calls []string
		err := replaceWinEditableFieldWithOps(ctx, "hello", true, winReplaceFieldOps{
			isSecure:     func() bool { return false },
			selectAll:    func(context.Context) error { calls = append(calls, "select_all"); return nil },
			pasteReplace: func(_ context.Context, s string) error { calls = append(calls, "paste:"+s); return nil },
		})
		if err != nil || !slices.Equal(calls, []string{"paste:hello"}) {
			t.Fatalf("err=%v calls=%v", err, calls)
		}
	})

	t.Run("whole field selects all then pastes", func(t *testing.T) {
		var calls []string
		err := replaceWinEditableFieldWithOps(ctx, "rewritten", false, winReplaceFieldOps{
			isSecure:     func() bool { return false },
			selectAll:    func(context.Context) error { calls = append(calls, "select_all"); return nil },
			pasteReplace: func(_ context.Context, s string) error { calls = append(calls, "paste:"+s); return nil },
		})
		if err != nil || !slices.Equal(calls, []string{"select_all", "paste:rewritten"}) {
			t.Fatalf("err=%v calls=%v", err, calls)
		}
	})

	t.Run("empty replacement deletes selected content", func(t *testing.T) {
		var calls []string
		err := replaceWinEditableFieldWithOps(ctx, "", false, winReplaceFieldOps{
			isSecure:       func() bool { return false },
			selectAll:      func(context.Context) error { calls = append(calls, "select_all"); return nil },
			deleteSelected: func(context.Context) error { calls = append(calls, "delete"); return nil },
		})
		if err != nil || !slices.Equal(calls, []string{"select_all", "delete"}) {
			t.Fatalf("err=%v calls=%v", err, calls)
		}
	})

	t.Run("secure input blocks replacement", func(t *testing.T) {
		pasted := false
		err := replaceWinEditableFieldWithOps(ctx, "x", true, winReplaceFieldOps{
			isSecure:     func() bool { return true },
			pasteReplace: func(context.Context, string) error { pasted = true; return nil },
		})
		if err == nil || pasted {
			t.Fatalf("expected secure input block, err=%v pasted=%v", err, pasted)
		}
	})
}
