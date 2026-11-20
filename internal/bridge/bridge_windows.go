//go:build windows

package bridge

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL = 13

	wmKeyDown                = 0x0100
	wmKeyUp                  = 0x0101
	wmSysKeyDown             = 0x0104
	wmSysKeyUp               = 0x0105
	wmQuit                   = 0x0012
	wmInputLangChangeRequest = 0x0050
	wmIMEControl             = 0x0283

	imcGetConversionMode = 0x0001
	imcSetConversionMode = 0x0002
	imeCmodeNative       = 0x0001

	llkhfExtended = 0x01
	llkhfInjected = 0x10

	vkBack     = 0x08
	vkTab      = 0x09
	vkReturn   = 0x0D
	vkShift    = 0x10
	vkControl  = 0x11
	vkMenu     = 0x12
	vkCapital  = 0x14
	vkEscape   = 0x1B
	vkSpace    = 0x20
	vkPrior    = 0x21
	vkNext     = 0x22
	vkEnd      = 0x23
	vkHome     = 0x24
	vkLeft     = 0x25
	vkUp       = 0x26
	vkRight    = 0x27
	vkDown     = 0x28
	vkDelete   = 0x2E
	vkLWin     = 0x5B
	vkRWin     = 0x5C
	vkF1       = 0x70
	vkF12      = 0x7B
	vkLShift   = 0xA0
	vkRShift   = 0xA1
	vkLControl = 0xA2
	vkRControl = 0xA3
	vkLMenu    = 0xA4
	vkRMenu    = 0xA5

	vkOem1      = 0xBA // ;:
	vkOemPlus   = 0xBB // =+
	vkOemComma  = 0xBC // ,<
	vkOemMinus  = 0xBD // -_
	vkOemPeriod = 0xBE // .>
	vkOem2      = 0xBF // /?
	vkOem3      = 0xC0 // `~
	vkOem4      = 0xDB // [{
	vkOem5      = 0xDC // \|
	vkOem6      = 0xDD // ]}
	vkOem7      = 0xDE // '"

	inputKeyboard    = 1
	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	swRestore     = 9

	dwmwaExtendedFrameBounds = 9
	monitorDefaultToNearest  = 2

	processQueryLimitedInformation = 0x1000
	klfActivate                    = 0x00000001
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	imm32    = windows.NewLazySystemDLL("imm32.dll")

	procSetWindowsHookExW         = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx       = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx            = user32.NewProc("CallNextHookEx")
	procGetMessageW               = user32.NewProc("GetMessageW")
	procTranslateMessage          = user32.NewProc("TranslateMessage")
	procDispatchMessageW          = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW        = user32.NewProc("PostThreadMessageW")
	procGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW            = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	procGetGUIThreadInfo          = user32.NewProc("GetGUIThreadInfo")
	procClientToScreen            = user32.NewProc("ClientToScreen")
	procGetWindowRect             = user32.NewProc("GetWindowRect")
	procSetWindowPos              = user32.NewProc("SetWindowPos")
	procShowWindow                = user32.NewProc("ShowWindow")
	procIsZoomed                  = user32.NewProc("IsZoomed")
	procGetCursorPos              = user32.NewProc("GetCursorPos")
	procEnumDisplayMonitors       = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW           = user32.NewProc("GetMonitorInfoW")
	procSendInput                 = user32.NewProc("SendInput")
	procGetAsyncKeyState          = user32.NewProc("GetAsyncKeyState")
	procGetKeyState               = user32.NewProc("GetKeyState")
	procToUnicodeEx               = user32.NewProc("ToUnicodeEx")
	procMapVirtualKeyW            = user32.NewProc("MapVirtualKeyW")
	procGetKeyboardLayout         = user32.NewProc("GetKeyboardLayout")
	procGetKeyboardLayoutList     = user32.NewProc("GetKeyboardLayoutList")
	procActivateKeyboardLayout    = user32.NewProc("ActivateKeyboardLayout")
	procPostMessageW              = user32.NewProc("PostMessageW")
	procSendMessageW              = user32.NewProc("SendMessageW")
	procOpenClipboard             = user32.NewProc("OpenClipboard")
	procCloseClipboard            = user32.NewProc("CloseClipboard")
	procEmptyClipboard            = user32.NewProc("EmptyClipboard")
	procGetClipboardData          = user32.NewProc("GetClipboardData")
	procSetClipboardData          = user32.NewProc("SetClipboardData")
	procEnumClipboardFormats      = user32.NewProc("EnumClipboardFormats")
	procGetClipboardFormatNameW   = user32.NewProc("GetClipboardFormatNameW")
	procIsClipboardFormatAvail    = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW  = user32.NewProc("RegisterClipboardFormatW")
	procGetClipboardSequenceNum   = user32.NewProc("GetClipboardSequenceNumber")
	procGetCurrentThreadId        = kernel32.NewProc("GetCurrentThreadId")
	procGlobalAlloc               = kernel32.NewProc("GlobalAlloc")
	procGlobalLock                = kernel32.NewProc("GlobalLock")
	procGlobalUnlock              = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                = kernel32.NewProc("GlobalSize")
	procGlobalFree                = kernel32.NewProc("GlobalFree")
	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	procDwmGetWindowAttribute     = dwmapi.NewProc("DwmGetWindowAttribute")
	procShellExecuteW             = shell32.NewProc("ShellExecuteW")
	procImmGetDefaultIMEWnd       = imm32.NewProc("ImmGetDefaultIMEWnd")
)

type winPOINT struct {
	X, Y int32
}

type winRECT struct {
	Left, Top, Right, Bottom int32
}

type winMSG struct {
	Hwnd    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPOINT
	_       uint32
}

type kbdLLHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type guiThreadInfo struct {
	CbSize        uint32
	Flags         uint32
	HwndActive    uintptr
	HwndFocus     uintptr
	HwndCapture   uintptr
	HwndMenuOwner uintptr
	HwndMoveSize  uintptr
	HwndCaret     uintptr
	RcCaret       winRECT
}

type monitorInfo struct {
	CbSize    uint32
	RcMonitor winRECT
	RcWork    winRECT
	DwFlags   uint32
}

type keybdInput struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// winInput matches sizeof(INPUT) == 40 bytes on 64-bit Windows.
type winInput struct {
	Typ uint32
	_   uint32
	Ki  keybdInput
	_   [8]byte
}

var (
	winClipFormatsOnce sync.Once
	fmtExcludeMonitor  uint32
	fmtIncludeHistory  uint32
)

// nativePtr converts an OS-owned memory address (e.g., GlobalLock or hook lParam)
// into an unsafe.Pointer. The memory is non-Go heap memory pinned by the OS.
func nativePtr(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

func initWinClipboardFormats() {
	winClipFormatsOnce.Do(func() {
		p1, _ := windows.UTF16PtrFromString("ExcludeClipboardContentFromMonitorProcessing")
		r1, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p1)))
		fmtExcludeMonitor = uint32(r1)

		p2, _ := windows.UTF16PtrFromString("CanIncludeInClipboardHistory")
		r2, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p2)))
		fmtIncludeHistory = uint32(r2)

		p3, _ := windows.UTF16PtrFromString("PNG")
		r3, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p3)))
		fmtPNG = uint32(r3)
	})
}

const (
	cfDIBFormat   = 8
	cfHDropFormat = 15
	cfDIBV5Format = 17
)

var fmtPNG uint32

func winClipFormatAvailable(f uint32) bool {
	if f == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvail.Call(uintptr(f))
	return r != 0
}

// winMediaClipboardKind classifies a text-less clipboard without opening it.
func winMediaClipboardKind() clipboard.Kind {
	return classifyClipboardFormats(winClipFormats{
		HDrop: winClipFormatAvailable(cfHDropFormat),
		PNG:   winClipFormatAvailable(fmtPNG),
		DIB:   winClipFormatAvailable(cfDIBFormat) || winClipFormatAvailable(cfDIBV5Format),
	})
}

// ClipboardSequence implements ClipboardSequencer so the poller can detect a clipboard change
// while it was reading media bytes.
func (b *windowsBridge) ClipboardSequence() int64 { return winClipboardSequenceNumber() }

type windowsBridge struct {
	mu              sync.Mutex
	started         bool
	stopCancel      context.CancelFunc
	hookDone        chan struct{}
	imeDone         chan struct{}
	triggerDone     chan struct{}
	triggerQueue    chan host.TriggerEvent
	hookHandle      uintptr
	hookThreadID    uint32
	hookCallback    uintptr
	onTrigger       func(host.TriggerEvent)
	lastProcMu      sync.RWMutex
	lastProcessName string

	router       *triggerRouter
	keymapEngine *KeymapLayerEngine
	imeEngine    *IMESwitchDecisionEngine
	imeCfg       config.IMEConfig
	windowCalc   *WindowLayoutCalculator
	pbTracker    *selfPasteboardTracker
	pbRestorer   *PasteboardSnapshotRestorer
	onIME        func(host.InputSourceInfo)
	onIMEFail    func(error)
	onLayer      func(string)
	lastLayer    string

	lastIMEFailMsg string
	lastIMEFailAt  time.Time

	// Last non-Clavis foreground app and its input source, for the settings IME card.
	extMu     sync.Mutex
	extApp    AppIdentity
	extSource host.InputSourceInfo
	haveExt   bool

	health   hookHealthState
	triggers *triggerRuntime
	// inputEngineReset, when set, drops any in-flight own-IME composition (mouse click etc.).
	inputEngineReset func()
	// ime hosts the own input engine (input_engine_windows.go); nil engine when disabled.
	ime inputEngineState

	// Inline input-engine interception (hook-level key feed).
	inlineMu      sync.RWMutex
	inlineHandler func(host.InlineKeyEvent) bool
	inlineEnabled atomic.Bool
	shiftDown     bool
	shiftHadKey   bool
}

func (b *windowsBridge) recordExternalIME(app AppIdentity, src host.InputSourceInfo) {
	b.extMu.Lock()
	b.extApp, b.extSource, b.haveExt = app, src, true
	b.extMu.Unlock()
}

// ForegroundIMEStatus implements ForegroundIMEReporter.
func (b *windowsBridge) ForegroundIMEStatus() (ForegroundIMEStatus, bool) {
	b.extMu.Lock()
	app, src, ok := b.extApp, b.extSource, b.haveExt
	b.extMu.Unlock()
	if !ok {
		return ForegroundIMEStatus{}, false
	}
	b.mu.Lock()
	enabled := b.imeCfg.Enabled
	b.mu.Unlock()
	rule, matched := b.imeEngine.MatchedRule(app)
	return ForegroundIMEStatus{App: app, Source: src, Rule: rule, RuleMatched: matched, AutoSwitch: enabled}, true
}

func newPlatformBridge(opts Options) HostService {
	b := &windowsBridge{
		router:       newTriggerRouter(opts.TriggerConfig),
		keymapEngine: NewKeymapLayerEngine(opts.KeymapConfig),
		imeEngine:    NewIMESwitchDecisionEngine(opts.IMEConfig.Enabled, opts.IMERules),
		imeCfg:       opts.IMEConfig,
		windowCalc:   NewWindowLayoutCalculator(),
		pbTracker:    newSelfPasteboardTracker(),
		onIME:        opts.OnInputSourceChanged,
		onIMEFail:    opts.OnIMESwitchFailed,
		onLayer:      opts.OnKeymapLayerChanged,
		triggers:     newTriggerRuntime(),
	}
	b.pbRestorer = NewPasteboardSnapshotRestorer(&windowsPasteboardBackend{bridge: b})
	b.health.resetCh = make(chan struct{}, 1)
	b.health.reinstallRes = make(chan bool, 1)
	b.ime.dirty = make(chan struct{}, 1)
	b.ime.anchorFn = func(ctx context.Context) (host.CursorAnchor, bool) {
		_, hwnd := winForegroundApp()
		if a, ok := resolveWinCaretAnchor(hwnd); ok {
			return a, true
		}
		return resolveWinMouseAnchor()
	}
	b.inputEngineReset = b.resetInputEngine
	return b
}

func (b *windowsBridge) UpdateTriggerConfig(cfg config.TriggerConfig) {
	b.router.UpdateConfig(cfg)
}

func (b *windowsBridge) UpdateKeymapConfig(cfg config.KeymapConfig) {
	if b.keymapEngine != nil {
		b.keymapEngine.UpdateConfig(cfg)
	}
	if b.triggers != nil {
		b.triggers.setKeyBindings(cfg.Bindings)
	}
	b.mu.Lock()
	b.lastLayer = ""
	b.mu.Unlock()
}

// ActiveKeymapLayer reports the currently active keymap hold layer ("nav", "hyper", or "").
func (b *windowsBridge) ActiveKeymapLayer() string {
	if b.keymapEngine == nil {
		return ""
	}
	return b.keymapEngine.ActiveLayer()
}

// notifyLayerIfChanged records layer as the last observed keymap layer and invokes onLayer
// asynchronously (never inside the low-level keyboard hook) whenever it changed.
func (b *windowsBridge) notifyLayerIfChanged(layer string) {
	b.mu.Lock()
	if b.lastLayer == layer {
		b.mu.Unlock()
		return
	}
	b.lastLayer = layer
	fn := b.onLayer
	b.mu.Unlock()
	if fn != nil {
		go fn(layer)
	}
}

func (b *windowsBridge) UpdateIMEConfig(imeCfg config.IMEConfig, rules config.IMERulesConfig) {
	b.mu.Lock()
	b.imeCfg = imeCfg
	b.mu.Unlock()
	b.imeEngine.UpdateRules(imeCfg.Enabled, rules)
}

// TapRecoveryCount reports how many times the hook watchdog reinstalled the LL hooks.
func (b *windowsBridge) TapRecoveryCount() int64 {
	return b.health.reinstalls.Load()
}

func (b *windowsBridge) setCachedProcessName(name string) {
	if name == "" {
		return
	}
	b.lastProcMu.Lock()
	b.lastProcessName = name
	b.lastProcMu.Unlock()
}

func (b *windowsBridge) cachedFrontmostProcessName() string {
	b.lastProcMu.RLock()
	name := b.lastProcessName
	b.lastProcMu.RUnlock()
	return name
}

func (b *windowsBridge) Capabilities() host.Capabilities {
	return host.Capabilities{
		Accessibility:    true,
		InputMonitoring:  true,
		GlobalHotkeys:    true,
		WindowManagement: true,
		IMEControl:       true,
		ClipboardHistory: true,
		Keychain:         true,
	}
}

func (b *windowsBridge) Permissions(ctx context.Context) (host.PermissionStatus, error) {
	if err := ctx.Err(); err != nil {
		return host.PermissionStatus{}, err
	}
	return host.PermissionStatus{
		Accessibility:   true,
		InputMonitoring: true,
		AppleEvents:     true,
		Granted:         true,
		Promptable:      false,
		Details:         b.hookHealthDetails(),
	}, nil
}

func (b *windowsBridge) RefreshPermissions(ctx context.Context) (host.PermissionStatus, error) {
	return b.Permissions(ctx)
}

func (b *windowsBridge) OpenPermissionSettings(string) error {
	return errors.ErrUnsupported
}

func (b *windowsBridge) PromptPermission(string) (host.PermissionStatus, error) {
	return b.Permissions(context.Background())
}

func (b *windowsBridge) Start(ctx context.Context, onTrigger func(host.TriggerEvent)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return errors.New("bridge: already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	triggerQueue := make(chan host.TriggerEvent, 128)
	triggerDone := make(chan struct{})
	b.started = true
	b.stopCancel = cancel
	b.onTrigger = onTrigger
	b.hookDone = make(chan struct{})
	b.imeDone = make(chan struct{})
	b.triggerQueue = triggerQueue
	b.triggerDone = triggerDone
	b.mu.Unlock()

	go b.runTriggerWorker(runCtx, triggerQueue, triggerDone, onTrigger)

	ready := make(chan error, 1)
	go b.runKeyboardHook(runCtx, ready)
	if err := <-ready; err != nil {
		cancel()
		<-b.hookDone
		close(b.imeDone)
		<-triggerDone
		b.mu.Lock()
		b.started = false
		b.triggerQueue = nil
		b.mu.Unlock()
		return err
	}

	go b.runIMEObserver(runCtx)
	go b.runHookWatchdog(runCtx)
	go b.runMouseResetWorker(runCtx)
	return nil
}

func (b *windowsBridge) Stop() error {
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		return nil
	}
	cancel := b.stopCancel
	tid := b.hookThreadID
	hookDone := b.hookDone
	imeDone := b.imeDone
	triggerDone := b.triggerDone
	b.triggerQueue = nil
	b.lastLayer = ""
	b.started = false
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if tid != 0 {
		procPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
	}
	if hookDone != nil {
		<-hookDone
	}
	if imeDone != nil {
		<-imeDone
	}
	if triggerDone != nil {
		<-triggerDone
	}
	return nil
}

func (b *windowsBridge) runKeyboardHook(ctx context.Context, ready chan<- error) {
	defer close(b.hookDone)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	b.mu.Lock()
	if b.hookCallback == 0 {
		b.hookCallback = windows.NewCallback(func(nCode int32, wParam uintptr, lParam uintptr) uintptr {
			return b.handleHookProc(nCode, wParam, lParam)
		})
	}
	cb := b.hookCallback
	b.mu.Unlock()

	hhk, _, callErr := procSetWindowsHookExW.Call(whKeyboardLL, cb, 0, 0)
	if hhk == 0 {
		ready <- fmt.Errorf("windows: SetWindowsHookExW failed: %w", callErr)
		return
	}

	tid, _, _ := procGetCurrentThreadId.Call()
	b.mu.Lock()
	b.hookHandle = hhk
	b.hookThreadID = uint32(tid)
	b.mu.Unlock()

	stopWatch := context.AfterFunc(ctx, func() {
		procPostThreadMessageW.Call(tid, wmQuit, 0, 0)
	})
	defer stopWatch()

	ready <- nil
	b.installMouseHook()

	var msg winMSG
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		if msg.Hwnd == 0 && msg.Message == wmClavisReinstall {
			ok := b.reinstallHooksOnThread()
			select {
			case b.health.reinstallRes <- ok:
			default:
			}
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	b.uninstallMouseHook()
	b.mu.Lock()
	hhk = b.hookHandle
	b.mu.Unlock()
	if hhk != 0 {
		procUnhookWindowsHookEx.Call(hhk)
	}
	b.mu.Lock()
	b.hookHandle = 0
	b.hookThreadID = 0
	b.mu.Unlock()
}

func (b *windowsBridge) handleHookProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	b.mu.Lock()
	hhk := b.hookHandle
	b.mu.Unlock()

	if nCode < 0 || lParam == 0 {
		r, _, _ := procCallNextHookEx.Call(hhk, uintptr(nCode), wParam, lParam)
		return r
	}

	info := (*kbdLLHookStruct)(nativePtr(lParam))
	b.health.lastHookTick.Store(info.Time)
	if info.DwExtraInfo == uintptr(SyntheticEventTag) || (info.Flags&llkhfInjected) != 0 {
		r, _, _ := procCallNextHookEx.Call(hhk, uintptr(nCode), wParam, lParam)
		return r
	}

	nowMs := time.Now().UnixMilli()
	isDown := wParam == wmKeyDown || wParam == wmSysKeyDown
	isUp := wParam == wmKeyUp || wParam == wmSysKeyUp
	vk := uint16(info.VkCode)

	if b.keymapEngine != nil && b.keymapEngine.IsEnabled() && (isDown || isUp) {
		var kmKey string
		if modName, ok := winVKToModifier(vk); ok {
			kmKey = modName
		} else {
			kmKey = winVKToKeyName(vk)
		}
		if kmKey != "" {
			mods := winActiveModifiers()
			outcome := b.keymapEngine.HandleKeyEvent(kmKey, isDown, mods, b.cachedFrontmostProcessName(), nowMs)
			b.notifyLayerIfChanged(outcome.LayerActive)
			for _, act := range outcome.EmitKeys {
				sendWinKeyAction(act)
			}
			if outcome.Consume {
				if isDown {
					b.router.leaderDetector.Reset()
					b.router.doubleTapDetector.Reset()
				}
				return 1
			}
		}
	} else {
		b.notifyLayerIfChanged("")
	}

	if modName, ok := winVKToModifier(vk); ok && (isDown || isUp) {
		// Right shift is reserved for the input-mode toggle (see the darwin side); only
		// left shift drives the double-shift selection bar, so one gesture never has to
		// serve both purposes.
		isShiftKey := vk == vkShift || vk == vkLShift || vk == vkRShift
		isRightShift := vk == vkRShift || modName == "right_shift"
		// Double-tapping the right shift flips forced Chinese takeover — the same gesture as
		// macOS, and the only entry point when the toggle hotkey is left empty.
		if b.triggers != nil && isRightShift && b.triggers.rightShiftDouble(modName, isDown, winOtherModifiersDown(vk), nowMs) {
			b.emitTriggerEvents([]host.TriggerEvent{{Kind: host.TriggerInputEngineToggle, TimestampMs: nowMs}})
			return 1
		}
		if b.triggers != nil && isShiftKey && !isRightShift && b.triggers.doubleShift(modName, isDown, winOtherModifiersDown(vk), nowMs, b.hookIMEComposing(time.Now())) {
			b.emitTriggerEvents([]host.TriggerEvent{selectionEvent(nowMs)})
			return 1
		}
		if isShiftKey {
			b.inputEngineHandleKey(vk, info.ScanCode, isDown, time.Now()) // never consumes
		}
		otherDown := winOtherModifiersDown(vk)
		events := b.router.HandleModifier(modName, isDown, otherDown, nowMs)
		b.emitTriggerEvents(events)
		r, _, _ := procCallNextHookEx.Call(hhk, uintptr(nCode), wParam, lParam)
		return r
	}

	if isDown {
		keyNameForTrigger := winVKToKeyName(vk)
		if b.triggers != nil && keyNameForTrigger != "" {
			if action, ok := b.triggers.bindingFor(keyNameForTrigger, winActiveModifiers(), b.cachedFrontmostProcessName()); ok {
				b.emitTriggerEvents([]host.TriggerEvent{{
					Kind:        host.TriggerKeyBinding,
					Action:      action,
					Key:         keyNameForTrigger,
					TimestampMs: nowMs,
				}})
				return 1
			}
		}
		if b.triggers != nil && b.triggers.shortcutMatches(keyNameForTrigger, winActiveModifiers()) && !b.hookSecureInput(time.Now()) {
			b.emitTriggerEvents([]host.TriggerEvent{optimizeEvent(nowMs)})
			return 1
		}
		cache := &keyDownUnicodeCache{lookup: rawWinVKToUnicode}
		prevCache := activeKeyDownUnicode.Swap(cache)
		defer activeKeyDownUnicode.Store(prevCache)

		var keyName string
		var mods []string
		if dispatchWinKeyDown(vk, info.ScanCode, cache, winKeyDownHooks{
			inputEngine: func() bool {
				return b.inputEngineHandleKey(vk, info.ScanCode, true, time.Now())
			},
			onFallback: func() {
				if b.shiftDown {
					b.shiftHadKey = true
				}
				keyName = winVKToKeyName(vk)
				mods = winActiveModifiers()
			},
			inline: func(unicodeStr string) bool {
				return b.inlineEnabled.Load() && b.callInline(host.InlineKeyEvent{
					KeyName:     keyName,
					Rune:        unicodeStr,
					KeyCode:     vk,
					Modifiers:   mods,
					IsDown:      true,
					TimestampMs: nowMs,
				})
			},
			router: func(unicodeStr string) bool {
				// Under IMM32 a shared HKL keeps native (Chinese) conversion mode active; keystrokes then
				// belong to the IME candidate buffer rather than to committed text.
				now := time.Now()
				imeComposing := b.hookIMEComposing(now)
				events, consume := b.router.HandleKeyDownWithIME(keyName, unicodeStr, mods, vk == vkBack, b.hookSecureInput(now), imeComposing, nowMs)
				b.emitTriggerEvents(events)
				return consume
			},
		}) {
			return 1
		}
	}

	r, _, _ := procCallNextHookEx.Call(hhk, uintptr(nCode), wParam, lParam)
	return r
}

func (b *windowsBridge) emitWindowsSelectionTrigger() {
	// Windows has no selection API in this bridge yet. Keep the probe off-hook;
	// a future UIA provider can replace this without changing hook semantics.
	// Do not fake a selection: only a provider-confirmed non-empty selection fires.
}

func (b *windowsBridge) emitTriggerEvents(events []host.TriggerEvent) {
	if len(events) == 0 {
		return
	}
	b.mu.Lock()
	q := b.triggerQueue
	b.mu.Unlock()
	if q == nil {
		return
	}
	for _, ev := range events {
		select {
		case q <- ev:
		default:
		}
	}
}

func (b *windowsBridge) runTriggerWorker(ctx context.Context, queue <-chan host.TriggerEvent, done chan<- struct{}, cb func(host.TriggerEvent)) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-queue:
			if cb != nil {
				cb(ev)
			}
		}
	}
}

// runIMEObserver polls the foreground app and IME state every 200ms. It applies per-app rules
// and reports every observed change of (HKL, 中/英 conversion mode) through onIME, so the pill
// also reflects manual switches (Win+Space, Shift toggling Microsoft Pinyin).
func (b *windowsBridge) runIMEObserver(ctx context.Context) {
	defer close(b.imeDone)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var (
		prevApp     AppIdentity
		havePrevApp bool
		tracker     imeChangeTracker
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.mu.Lock()
			enabled := b.imeCfg.Enabled
			delayMs := b.imeCfg.SwitchDelayMs
			onIME := b.onIME
			b.mu.Unlock()

			app, fg := winForegroundApp()
			if app.ProcessName != "" {
				b.setCachedProcessName(app.ProcessName)
			}
			if app.ProcessName == "" {
				continue
			}
			b.health.secure.Store(isSecureField(winFocusedSecureSignals(fg, app.ProcessName)), time.Now())

			// A foreground app change moves the insertion point: never let a snippet buffer
			// typed in the previous app match inside the newly focused one.
			if havePrevApp && appIdentityChanged(prevApp, app) && !isSelfClavisApp(app) {
				b.emitTriggerEvents([]host.TriggerEvent{{
					Kind:        host.TriggerSnippetReset,
					TimestampMs: time.Now().UnixMilli(),
				}})
			}
			prevApp = app
			havePrevApp = true

			// Clavis' own windows have their own IME state; observing them would flash the pill
			// whenever the capsule opens.
			if isSelfClavisApp(app) {
				continue
			}
			cur, err := b.CurrentInputSource(ctx)
			if err != nil {
				continue
			}
			b.recordExternalIME(app, cur)
			b.health.native.Store(cur.Mode == host.InputModeNative, time.Now())
			changed := tracker.Observe(cur)

			if enabled {
				decision := b.imeEngine.EvaluateAppActivationWithState(app, cur.ID, cur.Mode == host.InputModeNative)
				if decision.ShouldSwitch && b.applyRuleDecision(ctx, app, decision, delayMs) {
					if updated, err := b.CurrentInputSource(ctx); err == nil {
						b.recordExternalIME(app, updated)
						tracker.Observe(updated)
						if onIME != nil {
							onIME(updated)
						}
					}
					continue
				}
			}
			if changed && onIME != nil {
				onIME(cur)
			}
		}
	}
}

// applyRuleDecision honors the settle delay, then applies decision. Failures are reported.
func (b *windowsBridge) applyRuleDecision(ctx context.Context, app AppIdentity, decision IMEDecision, delayMs int) bool {
	// Honor the configured settle delay so fast alt-tab chains do not flap the layout.
	delayMs = min(max(delayMs, 0), 1000)
	if delayMs > 0 {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Duration(delayMs) * time.Millisecond):
		}
		if latestApp, _ := winForegroundApp(); appIdentityChanged(latestApp, app) {
			return false
		}
	}
	if err := b.applyIMEDecision(ctx, decision); err != nil {
		b.reportIMEFailure(fmt.Errorf("%s: %w", app.ProcessName, err))
		return false
	}
	return true
}

func winForegroundApp() (AppIdentity, uintptr) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return AppIdentity{}, 0
	}
	title := winWindowTitle(hwnd)
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return AppIdentity{WindowTitle: title}, hwnd
	}
	hProc, _, _ := procOpenProcess(processQueryLimitedInformation, false, pid)
	if hProc == 0 {
		return AppIdentity{WindowTitle: title}, hwnd
	}
	defer windows.CloseHandle(windows.Handle(hProc))

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageName.Call(hProc, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 || size == 0 {
		return AppIdentity{WindowTitle: title}, hwnd
	}
	fullPath := windows.UTF16ToString(buf[:size])
	base := filepath.Base(fullPath)
	return AppIdentity{
		ProcessName: base,
		WindowTitle: title,
	}, hwnd
}

func winWindowTitle(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	var buf [512]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	if int(n) > len(buf) {
		n = uintptr(len(buf))
	}
	return truncateUTF8(windows.UTF16ToString(buf[:n]), 512)
}

func procOpenProcess(access uint32, inherit bool, pid uint32) (uintptr, uintptr, error) {
	h, err := windows.OpenProcess(access, inherit, pid)
	if err != nil {
		return 0, 0, err
	}
	return uintptr(h), 0, nil
}

func (b *windowsBridge) SnapshotContext(ctx context.Context) (host.ContextSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return host.ContextSnapshot{}, err
	}

	app, hwnd := winForegroundApp()
	if app.ProcessName != "" {
		b.setCachedProcessName(app.ProcessName)
	}
	snap := host.ContextSnapshot{
		ProcessName: app.ProcessName,
		AppName:     strings.TrimSuffix(app.ProcessName, filepath.Ext(app.ProcessName)),
		WindowTitle: app.WindowTitle,
	}

	if preview, _, err := readWinClipboardUnicodeText(); err == nil && preview != "" {
		snap.ClipboardPreview = truncateUTF8(preview, 512)
	}

	if anchor, ok := resolveWinCaretAnchor(hwnd); ok {
		snap.CursorAnchor = &anchor
	} else if anchor, ok := resolveWinWindowAnchor(hwnd); ok {
		snap.CursorAnchor = &anchor
	} else if anchor, ok := resolveWinMouseAnchor(); ok {
		snap.CursorAnchor = &anchor
	}

	if curIME, err := b.CurrentInputSource(ctx); err == nil && curIME.ID != "" {
		snap.ActiveInputSource = &curIME
	}

	return snap, nil
}

func resolveWinCaretAnchor(hwnd uintptr) (host.CursorAnchor, bool) {
	if hwnd == 0 {
		return host.CursorAnchor{}, false
	}
	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	if tid == 0 {
		return host.CursorAnchor{}, false
	}
	var gti guiThreadInfo
	gti.CbSize = uint32(unsafe.Sizeof(gti))
	r, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti)))
	if r == 0 || gti.HwndCaret == 0 {
		return host.CursorAnchor{}, false
	}
	rc := gti.RcCaret
	if rc.Right <= rc.Left && rc.Bottom <= rc.Top {
		return host.CursorAnchor{}, false
	}
	topLeft := winPOINT{X: rc.Left, Y: rc.Top}
	botRight := winPOINT{X: rc.Right, Y: rc.Bottom}
	procClientToScreen.Call(gti.HwndCaret, uintptr(unsafe.Pointer(&topLeft)))
	procClientToScreen.Call(gti.HwndCaret, uintptr(unsafe.Pointer(&botRight)))

	return host.CursorAnchor{
		X:      float64(topLeft.X),
		Y:      float64(topLeft.Y),
		Width:  float64(max(1, botRight.X-topLeft.X)),
		Height: float64(max(1, botRight.Y-topLeft.Y)),
		Valid:  true,
	}, true
}

func resolveWinWindowAnchor(hwnd uintptr) (host.CursorAnchor, bool) {
	if hwnd == 0 {
		return host.CursorAnchor{}, false
	}
	rc, ok := getWinWindowBounds(hwnd)
	if !ok {
		return host.CursorAnchor{}, false
	}
	return host.CursorAnchor{
		X:      rc.X + rc.Width/2,
		Y:      rc.Y + rc.Height/3,
		Width:  1,
		Height: 18,
		Valid:  true,
	}, true
}

func resolveWinMouseAnchor() (host.CursorAnchor, bool) {
	var pt winPOINT
	r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	if r == 0 {
		return host.CursorAnchor{}, false
	}
	return host.CursorAnchor{
		X:      float64(pt.X),
		Y:      float64(pt.Y),
		Width:  1,
		Height: 18,
		Valid:  true,
	}, true
}

func getWinWindowBounds(hwnd uintptr) (Rect, bool) {
	var wr winRECT
	hr, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaExtendedFrameBounds, uintptr(unsafe.Pointer(&wr)), unsafe.Sizeof(wr))
	if hr != 0 {
		r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
		if r == 0 {
			return Rect{}, false
		}
	}
	return Rect{
		X:      float64(wr.Left),
		Y:      float64(wr.Top),
		Width:  float64(wr.Right - wr.Left),
		Height: float64(wr.Bottom - wr.Top),
	}, true
}

func (b *windowsBridge) ExecuteWindowCommand(ctx context.Context, cmd host.WindowCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return errors.New("windows: no foreground window")
	}

	curRect, ok := getWinWindowBounds(hwnd)
	if !ok {
		return errors.New("windows: failed to read foreground window bounds")
	}

	displays := winDisplays()
	winID := fmt.Sprintf("hwnd:%x", hwnd)
	targetRect, applied, err := b.windowCalc.ComputeTargetRect(winID, curRect, cmd, displays)
	if err != nil || !applied {
		return err
	}

	if zoomed, _, _ := procIsZoomed.Call(hwnd); zoomed != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}

	// Compensate for invisible DWM extended frame borders if DWM differs from GetWindowRect.
	var rawRect, dwmRect winRECT
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rawRect))); r != 0 {
		if hr, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaExtendedFrameBounds, uintptr(unsafe.Pointer(&dwmRect)), unsafe.Sizeof(dwmRect)); hr == 0 {
			padLeft := dwmRect.Left - rawRect.Left
			padTop := dwmRect.Top - rawRect.Top
			padRight := rawRect.Right - dwmRect.Right
			padBottom := rawRect.Bottom - dwmRect.Bottom
			targetRect.X -= float64(padLeft)
			targetRect.Y -= float64(padTop)
			targetRect.Width += float64(padLeft + padRight)
			targetRect.Height += float64(padTop + padBottom)
		}
	}

	r, _, callErr := procSetWindowPos.Call(
		hwnd, 0,
		uintptr(int32(targetRect.X)),
		uintptr(int32(targetRect.Y)),
		uintptr(int32(targetRect.Width)),
		uintptr(int32(targetRect.Height)),
		swpNoZOrder|swpNoActivate,
	)
	if r == 0 {
		return fmt.Errorf("windows: SetWindowPos failed: %w", callErr)
	}
	return nil
}

var (
	enumMonitorsOnce sync.Once
	enumMonitorsCB   uintptr
	enumDisplaysMu   sync.Mutex
	enumDisplaysBuf  []DisplayInfo
)

func enumMonitorsProc(hMonitor uintptr, hdc uintptr, lprc uintptr, dwData uintptr) uintptr {
	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi))); r != 0 {
		enumDisplaysBuf = append(enumDisplaysBuf, DisplayInfo{
			ID: int64(hMonitor),
			Bounds: Rect{
				X:      float64(mi.RcMonitor.Left),
				Y:      float64(mi.RcMonitor.Top),
				Width:  float64(mi.RcMonitor.Right - mi.RcMonitor.Left),
				Height: float64(mi.RcMonitor.Bottom - mi.RcMonitor.Top),
			},
			WorkArea: Rect{
				X:      float64(mi.RcWork.Left),
				Y:      float64(mi.RcWork.Top),
				Width:  float64(mi.RcWork.Right - mi.RcWork.Left),
				Height: float64(mi.RcWork.Bottom - mi.RcWork.Top),
			},
			Primary: (mi.DwFlags & 1) != 0,
		})
	}
	return 1
}

func winDisplays() []DisplayInfo {
	enumMonitorsOnce.Do(func() {
		enumMonitorsCB = windows.NewCallback(enumMonitorsProc)
	})
	enumDisplaysMu.Lock()
	defer enumDisplaysMu.Unlock()
	enumDisplaysBuf = enumDisplaysBuf[:0]
	procEnumDisplayMonitors.Call(0, 0, enumMonitorsCB, 0)
	out := make([]DisplayInfo, len(enumDisplaysBuf))
	copy(out, enumDisplaysBuf)
	return out
}

func (b *windowsBridge) InjectText(ctx context.Context, text string, deleteCount int, cursorBackOffset int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if utf8.RuneCountInString(text) <= 256 && !strings.ContainsAny(text, "\r\n\t") {
		inputs := make([]winInput, 0, deleteCount*2+cursorBackOffset*2+len([]rune(text))*2)
		for i := 0; i < deleteCount; i++ {
			inputs = append(inputs, winInput{Typ: inputKeyboard, Ki: keybdInput{WVk: vkBack, DwExtraInfo: uintptr(SyntheticEventTag)}}, winInput{Typ: inputKeyboard, Ki: keybdInput{WVk: vkBack, DwFlags: keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)}})
		}
		for _, u := range utf16.Encode([]rune(text)) {
			inputs = append(inputs, winInput{Typ: inputKeyboard, Ki: keybdInput{WScan: u, DwFlags: keyeventfUnicode, DwExtraInfo: uintptr(SyntheticEventTag)}}, winInput{Typ: inputKeyboard, Ki: keybdInput{WScan: u, DwFlags: keyeventfUnicode | keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)}})
		}
		for i := 0; i < cursorBackOffset; i++ {
			inputs = append(inputs, winInput{Typ: inputKeyboard, Ki: keybdInput{WVk: vkLeft, DwExtraInfo: uintptr(SyntheticEventTag)}}, winInput{Typ: inputKeyboard, Ki: keybdInput{WVk: vkLeft, DwFlags: keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)}})
		}
		if len(inputs) > 0 {
			procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
		}
		return nil
	}

	snap, snapErr := b.pbRestorer.Capture()

	for i := 0; i < deleteCount; i++ {
		sendWinKeyTap(vkBack)
	}

	var writtenSeq int64
	if text != "" {
		seq, err := writeWinClipboardUnicodeText(text)
		if err != nil {
			return err
		}
		writtenSeq = seq
		b.IgnoreSelfPasteboardChange(writtenSeq)
		sendWinCombo(vkControl, 'V')
		time.Sleep(45 * time.Millisecond)
	}

	for i := 0; i < cursorBackOffset; i++ {
		sendWinKeyTap(vkLeft)
	}

	if snapErr == nil && text != "" {
		_, _ = b.pbRestorer.RestoreIfUnmodified(snap, writtenSeq)
	}
	return nil
}

func sendWinKeyTap(vk uint16) {
	inputs := [2]winInput{
		{Typ: inputKeyboard, Ki: keybdInput{WVk: vk, DwExtraInfo: uintptr(SyntheticEventTag)}},
		{Typ: inputKeyboard, Ki: keybdInput{WVk: vk, DwFlags: keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)}},
	}
	procSendInput.Call(2, uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
}

func sendWinCombo(modVK, keyVK uint16) {
	inputs := [4]winInput{
		{Typ: inputKeyboard, Ki: keybdInput{WVk: modVK, DwExtraInfo: uintptr(SyntheticEventTag)}},
		{Typ: inputKeyboard, Ki: keybdInput{WVk: keyVK, DwExtraInfo: uintptr(SyntheticEventTag)}},
		{Typ: inputKeyboard, Ki: keybdInput{WVk: keyVK, DwFlags: keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)}},
		{Typ: inputKeyboard, Ki: keybdInput{WVk: modVK, DwFlags: keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)}},
	}
	procSendInput.Call(4, uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
}

func sendWinKeyAction(act KeyAction) {
	keyVK, ok := winKeyNameToVK(act.KeyName)
	if !ok {
		return
	}
	var modVKs []uint16
	for _, m := range act.Modifiers {
		if mvk, ok := winModifierNameToVK(m); ok {
			modVKs = append(modVKs, mvk)
		}
	}
	inputs := make([]winInput, 0, len(modVKs)*2+2)
	for _, mvk := range modVKs {
		inputs = append(inputs, winInput{
			Typ: inputKeyboard,
			Ki:  keybdInput{WVk: mvk, DwExtraInfo: uintptr(SyntheticEventTag)},
		})
	}
	inputs = append(inputs,
		winInput{
			Typ: inputKeyboard,
			Ki:  keybdInput{WVk: keyVK, DwExtraInfo: uintptr(SyntheticEventTag)},
		},
		winInput{
			Typ: inputKeyboard,
			Ki:  keybdInput{WVk: keyVK, DwFlags: keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)},
		},
	)
	for i := len(modVKs) - 1; i >= 0; i-- {
		inputs = append(inputs, winInput{
			Typ: inputKeyboard,
			Ki:  keybdInput{WVk: modVKs[i], DwFlags: keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)},
		})
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
}

func winModifierNameToVK(mod string) (uint16, bool) {
	norm, ok := NormalizeShortcutModifier(mod)
	if !ok {
		norm = strings.ToLower(strings.TrimSpace(mod))
	}
	switch norm {
	case "command":
		return vkLWin, true
	case "control":
		return vkControl, true
	case "option":
		return vkMenu, true
	case "shift":
		return vkShift, true
	default:
		return 0, false
	}
}

func winKeyNameToVK(name string) (uint16, bool) {
	s := strings.ToLower(strings.TrimSpace(name))
	if len(s) == 1 {
		if s[0] >= 'a' && s[0] <= 'z' {
			return uint16(s[0] - 'a' + 'A'), true
		}
		if s[0] >= '0' && s[0] <= '9' {
			return uint16(s[0]), true
		}
	}
	switch s {
	case "space":
		return vkSpace, true
	case "return", "enter":
		return vkReturn, true
	case "tab":
		return vkTab, true
	case "escape", "esc":
		return vkEscape, true
	case "backspace":
		return vkBack, true
	case "delete", "forwarddelete":
		return vkDelete, true
	case "left":
		return vkLeft, true
	case "right":
		return vkRight, true
	case "up":
		return vkUp, true
	case "down":
		return vkDown, true
	case "home":
		return vkHome, true
	case "end":
		return vkEnd, true
	case "pageup":
		return vkPrior, true
	case "pagedown":
		return vkNext, true
	case "minus", "-":
		return vkOemMinus, true
	case "equal", "=":
		return vkOemPlus, true
	case "leftbracket", "[":
		return vkOem4, true
	case "rightbracket", "]":
		return vkOem6, true
	case "semicolon", ";":
		return vkOem1, true
	case "quote", "'":
		return vkOem7, true
	case "comma", ",":
		return vkOemComma, true
	case "period", ".":
		return vkOemPeriod, true
	case "slash", "/":
		return vkOem2, true
	case "backslash", `\`:
		return vkOem5, true
	case "grave", "backtick", "`":
		return vkOem3, true
	}
	if strings.HasPrefix(s, "f") && len(s) >= 2 {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 1 && n <= 12 {
			return uint16(vkF1 + n - 1), true
		}
	}
	return 0, false
}

func (b *windowsBridge) OpenApp(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return errors.New("windows: empty app target")
	}
	verb, _ := windows.UTF16PtrFromString("open")
	filePtr, err := windows.UTF16PtrFromString(trimmed)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(filePtr)), 0, 0, 1)
	if r <= 32 {
		return exec.CommandContext(ctx, "cmd", "/C", "start", "", trimmed).Run()
	}
	return nil
}

func (b *windowsBridge) InputSources(ctx context.Context) ([]host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hkls := winInstalledHKLs()
	out := make([]host.InputSourceInfo, 0, len(hkls))
	for _, h := range hkls {
		out = append(out, winInputSourceInfo(h, winHKLFriendlyName(h), winIMEState{HKL: h}))
	}
	return out, nil
}

// CurrentInputSource reports the foreground HKL plus, for CJK IMEs, the live 中/英 conversion
// mode (Mode/IsASCII), so Microsoft Pinyin in English mode is labelled EN.
func (b *windowsBridge) CurrentInputSource(ctx context.Context) (host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return host.InputSourceInfo{}, err
	}
	st := winReadIMEState()
	return winInputSourceInfo(st.HKL, winHKLFriendlyName(st.HKL), st), nil
}

// SelectInputSource switches the foreground window to an installed HKL (full id or bare LANGID).
// It fails when the layout is not installed or the switch is not observed, so callers can fall
// back to a conversion-mode switch instead of silently doing nothing.
func (b *windowsBridge) SelectInputSource(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hkl, err := resolveInstalledHKL(winInstalledHKLs(), id)
	if err != nil {
		return err
	}
	return winSelectHKL(hkl)
}

// SetIMEMode switches the foreground IME between native (中) and ASCII (英) and verifies the
// result. Strategies, in order: WM_IME_CONTROL on the default IME window, ImmGetContext +
// ImmSetConversionStatus, then an HKL switch to an installed English / CJK layout. The returned
// error lists every attempt so callers can surface it.
func (b *windowsBridge) SetIMEMode(ctx context.Context, mode config.IMEMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode == config.IMEModeKeep || mode == "" {
		return nil
	}
	if mode != config.IMEModeASCII && mode != config.IMEModeNative {
		return fmt.Errorf("windows: unsupported IME mode %q", mode)
	}
	wantNative := mode == config.IMEModeNative
	if winIMEModeSatisfied(winReadIMEState(), wantNative) {
		return nil
	}

	var attempts []string
	for _, step := range []struct {
		name string
		fn   func(bool) error
	}{{"WM_IME_CONTROL", winSetModeViaIMEWnd}, {"ImmSetConversionStatus", winSetModeViaContext}} {
		if err := step.fn(wantNative); err != nil {
			attempts = append(attempts, step.name+": "+err.Error())
			continue
		}
		if winIMEModeSatisfied(winReadIMEState(), wantNative) {
			return nil
		}
		attempts = append(attempts, step.name+": state unchanged after set")
	}

	b.mu.Lock()
	preferred := strings.TrimSpace(b.imeCfg.DefaultInputSourceID)
	b.mu.Unlock()
	if hkl, ok := winTargetHKLForMode(winInstalledHKLs(), wantNative, preferred); ok {
		if err := winSelectHKL(hkl); err != nil {
			attempts = append(attempts, "HKL switch: "+err.Error())
		} else if st := winReadIMEState(); !wantNative || !st.ConvKnown || winIMEModeSatisfied(st, true) {
			// An English layout is ASCII by definition; a CJK layout with unknown conversion
			// state is the best we can do.
			return nil
		} else {
			attempts = append(attempts, "HKL switch: IME still in English mode")
		}
	} else {
		attempts = append(attempts, "HKL switch: no matching input source installed")
	}
	return fmt.Errorf("windows: could not switch IME to %s (%s)", mode, strings.Join(attempts, "; "))
}

// winIsNativeConversionMode reports whether the foreground IME is open in native conversion mode.
func winIsNativeConversionMode() bool {
	st := winReadIMEState()
	return st.ConvKnown && st.Open && st.Native
}

// asciiInputSourceIDLocked picks the ASCII input source to lock to during a Leader/Capsule
// session: the configured default when installed, else the first ASCII-capable layout.
func (b *windowsBridge) asciiInputSourceIDLocked(ctx context.Context) string {
	b.mu.Lock()
	defaultID := strings.TrimSpace(b.imeCfg.DefaultInputSourceID)
	b.mu.Unlock()

	sources, err := b.InputSources(ctx)
	if err != nil {
		return defaultID
	}
	if defaultID != "" {
		for _, s := range sources {
			if strings.EqualFold(s.ID, defaultID) {
				return s.ID
			}
		}
	}
	for _, s := range sources {
		if s.IsASCII {
			return s.ID
		}
	}
	return ""
}

// LockTemporaryASCII temporarily switches to ASCII for a Leader/Capsule session, remembering both
// the pre-lock HKL and its native conversion mode so RestoreTemporaryIME can undo it precisely.
func (b *windowsBridge) LockTemporaryASCII(ctx context.Context) (bool, host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return false, host.InputSourceInfo{}, err
	}
	cur, err := b.CurrentInputSource(ctx)
	if err != nil {
		return false, host.InputSourceInfo{}, err
	}

	asciiID := b.asciiInputSourceIDLocked(ctx)
	wasNative := !cur.IsASCII || winIsNativeConversionMode()
	dec := b.imeEngine.TemporaryLockASCIIWithState(asciiID, cur.ID, wasNative)
	if !dec.ShouldSwitch {
		return false, cur, nil
	}

	if err := b.applyIMEDecision(ctx, dec); err != nil {
		b.reportIMEFailure(err)
		return false, cur, nil
	}

	if updated, err := b.CurrentInputSource(ctx); err == nil && updated.ID != "" {
		return true, updated, nil
	}
	return true, cur, nil
}

// RestoreTemporaryIME restores the HKL (and native conversion mode) active before the most
// recent LockTemporaryASCII call.
func (b *windowsBridge) RestoreTemporaryIME(ctx context.Context) (bool, host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return false, host.InputSourceInfo{}, err
	}
	cur, _ := b.CurrentInputSource(ctx)
	dec := b.imeEngine.RestoreTemporaryLock(cur.ID)
	if !dec.ShouldSwitch {
		return false, cur, nil
	}

	if err := b.applyIMEDecision(ctx, dec); err != nil {
		b.reportIMEFailure(err)
		return false, cur, nil
	}

	if updated, err := b.CurrentInputSource(ctx); err == nil && updated.ID != "" {
		return true, updated, nil
	}
	return true, cur, nil
}

// applyIMEDecision applies a switch decision, preferring an explicit input source and falling
// back to a mode-only switch. A restored native decision also re-opens native conversion after the
// HKL switch. The returned error describes every failed strategy.
func (b *windowsBridge) applyIMEDecision(ctx context.Context, decision IMEDecision) error {
	var selErr error
	if decision.TargetInputSourceID != "" {
		if selErr = b.SelectInputSource(ctx, decision.TargetInputSourceID); selErr == nil {
			if decision.TargetMode == config.IMEModeNative {
				return b.SetIMEMode(ctx, decision.TargetMode)
			}
			return nil
		}
	}
	if decision.TargetMode == "" {
		if selErr == nil {
			selErr = errors.New("windows: empty IME decision")
		}
		return selErr
	}
	if err := b.SetIMEMode(ctx, decision.TargetMode); err != nil {
		return errors.Join(selErr, err)
	}
	return nil
}

// reportIMEFailure surfaces an IME switch failure through OnIMESwitchFailed, throttling repeats
// of the same message so a flapping foreground window cannot spam the HUD.
func (b *windowsBridge) reportIMEFailure(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	now := time.Now()
	b.mu.Lock()
	cb := b.onIMEFail
	if msg == b.lastIMEFailMsg && now.Sub(b.lastIMEFailAt) < 5*time.Second {
		cb = nil
	} else {
		b.lastIMEFailMsg, b.lastIMEFailAt = msg, now
	}
	b.mu.Unlock()
	if cb != nil {
		cb(err)
	}
}

func (b *windowsBridge) IgnoreSelfPasteboardChange(changeCount int64) {
	b.pbTracker.Ignore(changeCount)
}

func (b *windowsBridge) PollClipboardCandidate(ctx context.Context) (*clipboard.CaptureCandidate, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	initWinClipboardFormats()

	seq := winClipboardSequenceNumber()
	if !b.pbTracker.ShouldProcess(seq) {
		return nil, seq, nil
	}

	text, types, err := readWinClipboardUnicodeText()
	if err != nil {
		return nil, seq, err
	}
	var kind clipboard.Kind
	if text == "" {
		// Media: only the kind is decided here; bytes are read by the poller after the privacy
		// filter accepted the candidate (docs/design/media-preview.md §5).
		if kind = winMediaClipboardKind(); kind == "" {
			return nil, seq, nil
		}
	}
	app, _ := winForegroundApp()
	return &clipboard.CaptureCandidate{
		Media:           clipboard.Media{Kind: kind},
		Content:         text,
		SourceAppName:   strings.TrimSuffix(app.ProcessName, filepath.Ext(app.ProcessName)),
		SourceBundleID:  app.ProcessName,
		PasteboardTypes: types,
		Timestamp:       time.Now().UTC(),
	}, seq, nil
}

// PasteClipboard implements MediaPaster: the caller already wrote the clipboard.
func (b *windowsBridge) PasteClipboard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.IgnoreSelfPasteboardChange(winClipboardSequenceNumber())
	sendWinCombo(vkControl, 'V')
	return nil
}

func winClipboardSequenceNumber() int64 {
	r, _, _ := procGetClipboardSequenceNum.Call()
	return int64(uint32(r))
}

func readWinClipboardUnicodeText() (string, []string, error) {
	initWinClipboardFormats()
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return "", nil, errors.New("windows: OpenClipboard failed")
	}
	defer procCloseClipboard.Call()

	var types []string
	if fmtExcludeMonitor != 0 {
		if avail, _, _ := procIsClipboardFormatAvail.Call(uintptr(fmtExcludeMonitor)); avail != 0 {
			types = append(types, "ExcludeClipboardContentFromMonitorProcessing")
		}
	}
	if fmtIncludeHistory != 0 {
		if avail, _, _ := procIsClipboardFormatAvail.Call(uintptr(fmtIncludeHistory)); avail != 0 {
			h, _, _ := procGetClipboardData.Call(uintptr(fmtIncludeHistory))
			if h != 0 {
				ptr, _, _ := procGlobalLock.Call(h)
				if ptr != 0 {
					sz, _, _ := procGlobalSize.Call(h)
					if sz >= 4 {
						val := binary.LittleEndian.Uint32(unsafe.Slice((*byte)(nativePtr(ptr)), 4))
						if val == 0 {
							types = append(types, "CanIncludeInClipboardHistory")
						}
					}
					procGlobalUnlock.Call(h)
				}
			}
		}
	}

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", types, nil
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", types, nil
	}
	defer procGlobalUnlock.Call(h)

	sz, _, _ := procGlobalSize.Call(h)
	if sz < 2 {
		return "", types, nil
	}
	u16 := unsafe.Slice((*uint16)(nativePtr(ptr)), sz/2)
	return windows.UTF16ToString(u16), types, nil
}

func writeWinClipboardUnicodeText(text string) (int64, error) {
	u16 := utf16.Encode([]rune(text))
	u16 = append(u16, 0)
	byteLen := len(u16) * 2

	hMem, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(byteLen))
	if hMem == 0 {
		return 0, fmt.Errorf("windows: GlobalAlloc failed: %w", err)
	}
	ptr, _, err := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("windows: GlobalLock failed: %w", err)
	}
	dst := unsafe.Slice((*uint16)(nativePtr(ptr)), len(u16))
	copy(dst, u16)
	procGlobalUnlock.Call(hMem)

	if r, _, openErr := procOpenClipboard.Call(0); r == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("windows: OpenClipboard failed: %w", openErr)
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	if res, _, setErr := procSetClipboardData.Call(cfUnicodeText, hMem); res == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("windows: SetClipboardData failed: %w", setErr)
	}
	return winClipboardSequenceNumber(), nil
}

type windowsPasteboardBackend struct {
	bridge *windowsBridge
}

func (p *windowsPasteboardBackend) ChangeCount() int64 {
	return winClipboardSequenceNumber()
}

func (p *windowsPasteboardBackend) Capture() (PasteboardSnapshot, error) {
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return PasteboardSnapshot{}, fmt.Errorf("windows: OpenClipboard failed: %w", err)
	}
	defer procCloseClipboard.Call()

	snap := PasteboardSnapshot{ChangeCount: winClipboardSequenceNumber()}
	var item PasteboardItemSnapshot
	var fmtID uintptr
	for {
		nextFmt, _, _ := procEnumClipboardFormats.Call(fmtID)
		if nextFmt == 0 {
			break
		}
		fmtID = nextFmt
		h, _, _ := procGetClipboardData.Call(fmtID)
		if h == 0 {
			continue
		}
		ptr, _, _ := procGlobalLock.Call(h)
		if ptr == 0 {
			continue
		}
		sz, _, _ := procGlobalSize.Call(h)
		raw := make([]byte, sz)
		if sz > 0 {
			copy(raw, unsafe.Slice((*byte)(nativePtr(ptr)), sz))
		}
		procGlobalUnlock.Call(h)

		item.Representations = append(item.Representations, PasteboardRepresentation{
			Type: strconv.FormatUint(uint64(fmtID), 10),
			Data: raw,
		})
	}
	if len(item.Representations) > 0 {
		snap.Items = append(snap.Items, item)
	}
	return snap, nil
}

func (p *windowsPasteboardBackend) Restore(snap PasteboardSnapshot) error {
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return fmt.Errorf("windows: OpenClipboard failed: %w", err)
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	for _, item := range snap.Items {
		for _, rep := range item.Representations {
			fmtID, err := strconv.ParseUint(rep.Type, 10, 32)
			if err != nil {
				continue
			}
			allocSize := max(1, len(rep.Data))
			hMem, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(allocSize))
			if hMem == 0 {
				continue
			}
			ptr, _, _ := procGlobalLock.Call(hMem)
			if ptr == 0 {
				procGlobalFree.Call(hMem)
				continue
			}
			if len(rep.Data) > 0 {
				copy(unsafe.Slice((*byte)(nativePtr(ptr)), len(rep.Data)), rep.Data)
			}
			procGlobalUnlock.Call(hMem)
			if res, _, _ := procSetClipboardData.Call(uintptr(fmtID), hMem); res == 0 {
				procGlobalFree.Call(hMem)
			}
		}
	}
	p.bridge.IgnoreSelfPasteboardChange(winClipboardSequenceNumber())
	return nil
}

func winVKToModifier(vk uint16) (string, bool) {
	switch vk {
	case vkRMenu:
		return "right_option", true
	case vkLMenu, vkMenu:
		return "left_option", true
	case vkRWin:
		return "right_command", true
	case vkLWin:
		return "left_command", true
	case vkRShift:
		return "right_shift", true
	case vkLShift, vkShift:
		return "left_shift", true
	case vkRControl:
		return "right_control", true
	case vkLControl, vkControl:
		return "left_control", true
	case vkCapital:
		return "caps_lock", true
	default:
		return "", false
	}
}

func winVKDown(vk uint16) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return (uint16(r) & 0x8000) != 0
}

func winOtherModifiersDown(targetVK uint16) bool {
	checks := []uint16{vkLShift, vkRShift, vkLControl, vkRControl, vkLMenu, vkRMenu, vkLWin, vkRWin}
	for _, vk := range checks {
		if vk == targetVK {
			continue
		}
		if winVKDown(vk) {
			return true
		}
	}
	return false
}

func winActiveModifiers() []string {
	var mods []string
	if winVKDown(vkLWin) || winVKDown(vkRWin) {
		mods = append(mods, "command")
	}
	if winVKDown(vkLMenu) || winVKDown(vkRMenu) || winVKDown(vkMenu) {
		mods = append(mods, "option")
	}
	if winVKDown(vkLShift) || winVKDown(vkRShift) || winVKDown(vkShift) {
		mods = append(mods, "shift")
	}
	if winVKDown(vkLControl) || winVKDown(vkRControl) || winVKDown(vkControl) {
		mods = append(mods, "control")
	}
	return mods
}

var activeKeyDownUnicode atomic.Pointer[keyDownUnicodeCache]

func winVKToUnicode(vk uint16, scanCode uint32) string {
	if c := activeKeyDownUnicode.Load(); c != nil {
		return c.Resolve(vk, scanCode)
	}
	return rawWinVKToUnicode(vk, scanCode)
}

func rawWinVKToUnicode(vk uint16, scanCode uint32) string {
	var keyState [256]byte
	if winVKDown(vkLShift) || winVKDown(vkRShift) || winVKDown(vkShift) {
		keyState[vkShift] = 0x80
	}
	if capState, _, _ := procGetKeyState.Call(uintptr(vkCapital)); (uint16(capState) & 0x0001) != 0 {
		keyState[vkCapital] = 0x01
	}

	hwnd, _, _ := procGetForegroundWindow.Call()
	var tid uintptr
	if hwnd != 0 {
		tid, _, _ = procGetWindowThreadProcessId.Call(hwnd, 0)
	}
	hkl, _, _ := procGetKeyboardLayout.Call(tid)

	var buf [8]uint16
	r, _, _ := procToUnicodeEx.Call(
		uintptr(vk),
		uintptr(scanCode),
		uintptr(unsafe.Pointer(&keyState[0])),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0,
		hkl,
	)
	n := int32(r)
	if n <= 0 {
		return ""
	}
	if n > int32(len(buf)) {
		n = int32(len(buf))
	}
	var sb strings.Builder
	for _, r := range utf16.Decode(buf[:n]) {
		if r < 0x20 || r == 0x7f {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func winVKToKeyName(vk uint16) string {
	if vk >= 'A' && vk <= 'Z' {
		return string(rune(vk - 'A' + 'a'))
	}
	if vk >= '0' && vk <= '9' {
		return string(rune(vk))
	}
	if vk >= vkF1 && vk <= vkF12 {
		return fmt.Sprintf("f%d", vk-vkF1+1)
	}
	switch vk {
	case vkSpace:
		return "space"
	case vkReturn:
		return "return"
	case vkTab:
		return "tab"
	case vkEscape:
		return "escape"
	case vkBack:
		return "backspace"
	case vkDelete:
		return "forwarddelete"
	case vkLeft:
		return "left"
	case vkRight:
		return "right"
	case vkUp:
		return "up"
	case vkDown:
		return "down"
	case vkHome:
		return "home"
	case vkEnd:
		return "end"
	case vkPrior:
		return "pageup"
	case vkNext:
		return "pagedown"
	case vkOemMinus:
		return "minus"
	case vkOemPlus:
		return "equal"
	case vkOem4:
		return "leftbracket"
	case vkOem6:
		return "rightbracket"
	case vkOem1:
		return "semicolon"
	case vkOem7:
		return "quote"
	case vkOemComma:
		return "comma"
	case vkOemPeriod:
		return "period"
	case vkOem2:
		return "slash"
	case vkOem5:
		return "backslash"
	case vkOem3:
		return "grave"
	default:
		return ""
	}
}
