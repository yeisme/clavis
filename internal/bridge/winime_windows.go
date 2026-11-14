//go:build windows

package bridge

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 IME plumbing. Limitations (documented in docs/windows-parity-audit.md):
//   - The conversion mode is read via IMM32 (WM_IME_CONTROL / ImmGetContext). Modern TSF IMEs
//     (Microsoft Pinyin on Win10+) expose it through the IMM compatibility layer; TSF-only
//     third-party IMEs may not answer, in which case ConvKnown is false and the mode is unknown.
//   - ImmGetContext only works for windows of the calling thread, so the cross-process context
//     fallback usually fails for other apps; the HKL switch is the final fallback.

const (
	imcGetOpenStatus = 0x0005
	imcSetOpenStatus = 0x0006
	smtoAbortIfHung  = 0x0002
	imeMsgTimeoutMs  = 100

	localeSLocalizedDisplayName = 0x00000002
)

var (
	procSendMessageTimeoutW    = user32.NewProc("SendMessageTimeoutW")
	procImmGetContext          = imm32.NewProc("ImmGetContext")
	procImmReleaseContext      = imm32.NewProc("ImmReleaseContext")
	procImmGetConversionStatus = imm32.NewProc("ImmGetConversionStatus")
	procImmSetConversionStatus = imm32.NewProc("ImmSetConversionStatus")
	procImmGetOpenStatus       = imm32.NewProc("ImmGetOpenStatus")
	procImmSetOpenStatus       = imm32.NewProc("ImmSetOpenStatus")
	procImmGetDescriptionW     = imm32.NewProc("ImmGetDescriptionW")
	procGetLocaleInfoW         = kernel32.NewProc("GetLocaleInfoW")

	hklNameCache sync.Map // uint64 -> string
)

// winIMEControl sends WM_IME_CONTROL with a timeout so a hung foreground app cannot block us.
func winIMEControl(imeWnd uintptr, cmd uintptr, arg uintptr) (uintptr, bool) {
	var result uintptr
	r, _, _ := procSendMessageTimeoutW.Call(imeWnd, wmIMEControl, cmd, arg,
		smtoAbortIfHung, imeMsgTimeoutMs, uintptr(unsafe.Pointer(&result)))
	return result, r != 0
}

// winForegroundIMEWnd returns the foreground window and its default IME window (0 when none).
func winForegroundIMEWnd() (hwnd, imeWnd uintptr) {
	hwnd, _, _ = procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0, 0
	}
	imeWnd, _, _ = procImmGetDefaultIMEWnd.Call(hwnd)
	return hwnd, imeWnd
}

// winForegroundHKL returns the keyboard layout of the foreground window's thread.
func winForegroundHKL() uint64 {
	hwnd, _, _ := procGetForegroundWindow.Call()
	var tid uintptr
	if hwnd != 0 {
		tid, _, _ = procGetWindowThreadProcessId.Call(hwnd, 0)
	}
	hkl, _, _ := procGetKeyboardLayout.Call(tid)
	return uint64(hkl)
}

// winReadIMEState reads the foreground HKL and IMM open/conversion state.
func winReadIMEState() winIMEState {
	st := winIMEState{HKL: winForegroundHKL()}
	_, imeWnd := winForegroundIMEWnd()
	if imeWnd == 0 {
		return st
	}
	open, ok1 := winIMEControl(imeWnd, imcGetOpenStatus, 0)
	conv, ok2 := winIMEControl(imeWnd, imcGetConversionMode, 0)
	if !ok1 || !ok2 {
		return st
	}
	st.ConvKnown = true
	st.Open = open != 0
	st.Native = conv&uintptr(imeCmodeNative) != 0
	return st
}

// winSetModeViaIMEWnd toggles IME_CMODE_NATIVE (and opens the IME for native) on the default IME window.
func winSetModeViaIMEWnd(wantNative bool) error {
	_, imeWnd := winForegroundIMEWnd()
	if imeWnd == 0 {
		return errors.New("no default IME window")
	}
	conv, ok := winIMEControl(imeWnd, imcGetConversionMode, 0)
	if !ok {
		return errors.New("WM_IME_CONTROL timed out")
	}
	if wantNative {
		winIMEControl(imeWnd, imcSetOpenStatus, 1)
		winIMEControl(imeWnd, imcSetConversionMode, conv|uintptr(imeCmodeNative))
	} else {
		winIMEControl(imeWnd, imcSetConversionMode, conv&^uintptr(imeCmodeNative))
	}
	return nil
}

// winSetModeViaContext uses ImmGetContext + ImmSetConversionStatus/ImmSetOpenStatus.
func winSetModeViaContext(wantNative bool) error {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return errors.New("no foreground window")
	}
	himc, _, _ := procImmGetContext.Call(hwnd)
	if himc == 0 {
		return errors.New("ImmGetContext returned no context (cross-process window)")
	}
	defer procImmReleaseContext.Call(hwnd, himc)
	var conv, sent uint32
	if r, _, _ := procImmGetConversionStatus.Call(himc, uintptr(unsafe.Pointer(&conv)), uintptr(unsafe.Pointer(&sent))); r == 0 {
		return errors.New("ImmGetConversionStatus failed")
	}
	if wantNative {
		procImmSetOpenStatus.Call(himc, 1)
		conv |= imeCmodeNative
	} else {
		conv &^= imeCmodeNative
	}
	if r, _, _ := procImmSetConversionStatus.Call(himc, uintptr(conv), uintptr(sent)); r == 0 {
		return errors.New("ImmSetConversionStatus failed")
	}
	return nil
}

// winInstalledHKLs lists the installed keyboard layouts.
func winInstalledHKLs() []uint64 {
	cnt, _, _ := procGetKeyboardLayoutList.Call(0, 0)
	if cnt == 0 {
		return nil
	}
	hkls := make([]uintptr, cnt)
	got, _, _ := procGetKeyboardLayoutList.Call(cnt, uintptr(unsafe.Pointer(&hkls[0])))
	out := make([]uint64, 0, got)
	for i := uintptr(0); i < got && int(i) < len(hkls); i++ {
		out = append(out, uint64(hkls[i]))
	}
	return out
}

// winHKLFriendlyName resolves a readable name: the IMM IME description, else the localized
// locale display name (e.g. "中文(简体，中国)"). TSF IMEs such as Microsoft Pinyin have no IMM
// description, so they show the locale name; the 中/英 state is reported separately via Mode.
func winHKLFriendlyName(hkl uint64) string {
	if v, ok := hklNameCache.Load(hkl); ok {
		return v.(string)
	}
	name := ""
	var buf [256]uint16
	if n, _, _ := procImmGetDescriptionW.Call(uintptr(hkl), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); n > 0 && int(n) <= len(buf) {
		name = strings.TrimSpace(windows.UTF16ToString(buf[:n]))
	}
	if name == "" {
		lcid := uintptr(hklLangID(hkl))
		if n, _, _ := procGetLocaleInfoW.Call(lcid, localeSLocalizedDisplayName, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); n > 0 && int(n) <= len(buf) {
			name = strings.TrimSpace(windows.UTF16ToString(buf[:n]))
		}
	}
	hklNameCache.Store(hkl, name)
	return name
}

// winSelectHKL posts WM_INPUTLANGCHANGEREQUEST to the foreground window (or activates the layout
// for our own thread) and waits briefly for the foreground HKL to change.
func winSelectHKL(hkl uint64) error {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		if r, _, callErr := procActivateKeyboardLayout.Call(uintptr(hkl), klfActivate); r == 0 {
			return fmt.Errorf("windows: ActivateKeyboardLayout failed: %w", callErr)
		}
		return nil
	}
	procPostMessageW.Call(hwnd, wmInputLangChangeRequest, 0, uintptr(hkl))
	for range 6 {
		if winForegroundHKL() == hkl {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	if winForegroundHKL() == hkl {
		return nil
	}
	return fmt.Errorf("windows: foreground window did not switch to input source %s", FormatHKLID(hkl))
}
