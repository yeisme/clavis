//go:build darwin

package bridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/permissionflow"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// CoreGraphics / Carbon / Accessibility constants.
const (
	kCFStringEncodingUTF8 = 0x08000100

	kAXValueCGPointType int32 = 1
	kAXValueCGSizeType  int32 = 2
	kAXValueCGRectType  int32 = 3
	kAXValueCFRangeType int32 = 4

	kCGHIDEventTap           uint32 = 0
	kCGSessionEventTap       uint32 = 1
	kCGHeadInsertEventTap    uint32 = 0
	kCGEventTapOptionDefault uint32 = 0

	kCGEventLeftMouseDown          uint32 = 1
	kCGEventLeftMouseUp            uint32 = 2
	kCGEventRightMouseDown         uint32 = 3
	kCGEventKeyDown                uint32 = 10
	kCGEventKeyUp                  uint32 = 11
	kCGEventFlagsChanged           uint32 = 12
	kCGEventTapDisabledByTimeout   uint32 = 0xFFFFFFFE
	kCGEventTapDisabledByUserInput uint32 = 0xFFFFFFFF

	kCGKeyboardEventKeycode uint32 = 9
	kCGEventSourceUserData  uint32 = 42

	kCGEventFlagMaskAlphaShift uint64 = 0x00010000
	kCGEventFlagMaskShift      uint64 = 0x00020000
	kCGEventFlagMaskControl    uint64 = 0x00040000
	kCGEventFlagMaskAlternate  uint64 = 0x00080000
	kCGEventFlagMaskCommand    uint64 = 0x00100000
	kCGEventFlagMaskSecondary  uint64 = 0x00800000

	// Device-dependent left/right modifier masks from IOKit/hidsystem/IOLLEvent.h
	nxDeviceLCtlMask   uint64 = 0x00000001
	nxDeviceLShiftMask uint64 = 0x00000002
	nxDeviceRShiftMask uint64 = 0x00000004
	nxDeviceLCmdMask   uint64 = 0x00000008
	nxDeviceRCmdMask   uint64 = 0x00000010
	nxDeviceLAltMask   uint64 = 0x00000020
	nxDeviceRAltMask   uint64 = 0x00000040
	nxDeviceRCtlMask   uint64 = 0x00002000

	// macOS virtual keycodes
	kVK_ANSI_A        uint16 = 0x00
	kVK_ANSI_S        uint16 = 0x01
	kVK_ANSI_D        uint16 = 0x02
	kVK_ANSI_F        uint16 = 0x03
	kVK_ANSI_H        uint16 = 0x04
	kVK_ANSI_G        uint16 = 0x05
	kVK_ANSI_Z        uint16 = 0x06
	kVK_ANSI_X        uint16 = 0x07
	kVK_ANSI_C        uint16 = 0x08
	kVK_ANSI_V        uint16 = 0x09
	kVK_ANSI_B        uint16 = 0x0B
	kVK_ANSI_Q        uint16 = 0x0C
	kVK_ANSI_W        uint16 = 0x0D
	kVK_ANSI_E        uint16 = 0x0E
	kVK_ANSI_R        uint16 = 0x0F
	kVK_ANSI_Y        uint16 = 0x10
	kVK_ANSI_T        uint16 = 0x11
	kVK_ANSI_1        uint16 = 0x12
	kVK_ANSI_2        uint16 = 0x13
	kVK_ANSI_3        uint16 = 0x14
	kVK_ANSI_4        uint16 = 0x15
	kVK_ANSI_6        uint16 = 0x16
	kVK_ANSI_5        uint16 = 0x17
	kVK_ANSI_Equal    uint16 = 0x18
	kVK_ANSI_9        uint16 = 0x19
	kVK_ANSI_7        uint16 = 0x1A
	kVK_ANSI_Minus    uint16 = 0x1B
	kVK_ANSI_8        uint16 = 0x1C
	kVK_ANSI_0        uint16 = 0x1D
	kVK_ANSI_RightBrk uint16 = 0x1E
	kVK_ANSI_O        uint16 = 0x1F
	kVK_ANSI_U        uint16 = 0x20
	kVK_ANSI_LeftBrk  uint16 = 0x21
	kVK_ANSI_I        uint16 = 0x22
	kVK_ANSI_P        uint16 = 0x23
	kVK_Return        uint16 = 0x24
	kVK_ANSI_L        uint16 = 0x25
	kVK_ANSI_J        uint16 = 0x26
	kVK_ANSI_Quote    uint16 = 0x27
	kVK_ANSI_K        uint16 = 0x28
	kVK_ANSI_Semi     uint16 = 0x29
	kVK_ANSI_Backsl   uint16 = 0x2A
	kVK_ANSI_Comma    uint16 = 0x2B
	kVK_ANSI_Slash    uint16 = 0x2C
	kVK_ANSI_N        uint16 = 0x2D
	kVK_ANSI_M        uint16 = 0x2E
	kVK_ANSI_Period   uint16 = 0x2F
	kVK_Tab           uint16 = 0x30
	kVK_Space         uint16 = 0x31
	kVK_ANSI_Grave    uint16 = 0x32
	kVK_Delete        uint16 = 0x33
	kVK_Escape        uint16 = 0x35
	kVK_RightCommand  uint16 = 0x36
	kVK_Command       uint16 = 0x37
	kVK_Shift         uint16 = 0x38
	kVK_CapsLock      uint16 = 0x39
	kVK_Option        uint16 = 0x3A
	kVK_Control       uint16 = 0x3B
	kVK_RightShift    uint16 = 0x3C
	kVK_RightOption   uint16 = 0x3D
	kVK_RightControl  uint16 = 0x3E
	kVK_F5            uint16 = 0x60
	kVK_F6            uint16 = 0x61
	kVK_F7            uint16 = 0x62
	kVK_F3            uint16 = 0x63
	kVK_F8            uint16 = 0x64
	kVK_F9            uint16 = 0x65
	kVK_F11           uint16 = 0x67
	kVK_F10           uint16 = 0x6D
	kVK_F12           uint16 = 0x6F
	kVK_Home          uint16 = 0x73
	kVK_PageUp        uint16 = 0x74
	kVK_ForwardDelete uint16 = 0x75
	kVK_F4            uint16 = 0x76
	kVK_End           uint16 = 0x77
	kVK_F2            uint16 = 0x78
	kVK_PageDown      uint16 = 0x79
	kVK_F1            uint16 = 0x7A
	kVK_LeftArrow     uint16 = 0x7B
	kVK_RightArrow    uint16 = 0x7C
	kVK_DownArrow     uint16 = 0x7D
	kVK_UpArrow       uint16 = 0x7E
)

type cgPoint struct {
	X, Y float64
}

type cgSize struct {
	Width, Height float64
}

type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

type cfRange struct {
	Location int64
	Length   int64
}

var (
	darwinInitOnce sync.Once
	darwinInitErr  error

	libObjC, libCF, libCG, libAppKit, libAX, libCarbon uintptr

	// ObjC helpers
	poolPushFn uintptr
	poolPopFn  uintptr

	// CoreFoundation
	cfRelease                     func(cf uintptr)
	cfRetain                      func(cf uintptr) uintptr
	cfGetTypeID                   func(cf uintptr) uintptr
	cfStringGetTypeID             func() uintptr
	cfURLGetTypeID                func() uintptr
	cfURLGetString                func(anURL uintptr) uintptr
	cfStringCreateWithCString     func(alloc uintptr, cStr *byte, encoding uint32) uintptr
	cfStringGetCString            func(str uintptr, buf *byte, bufSize int, encoding uint32) bool
	cfStringGetLength             func(str uintptr) int
	cfArrayGetCount               func(arr uintptr) int
	cfArrayGetValueAtIndex        func(arr uintptr, idx int) uintptr
	cfBooleanGetValue             func(b uintptr) bool
	cfDictionaryCreate            func(alloc uintptr, keys *uintptr, vals *uintptr, numValues int, keyCB uintptr, valCB uintptr) uintptr
	cfMachPortCreateRunLoopSource func(alloc uintptr, port uintptr, order int) uintptr
	cfRunLoopGetCurrent           func() uintptr
	cfRunLoopAddSource            func(rl uintptr, src uintptr, mode uintptr)
	cfRunLoopRemoveSource         func(rl uintptr, src uintptr, mode uintptr)
	cfRunLoopRun                  func()
	cfRunLoopStop                 func(rl uintptr)

	kCFBooleanTrue                  uintptr
	kCFRunLoopCommonModes           uintptr
	kCFTypeDictionaryKeyCallbacks   uintptr
	kCFTypeDictionaryValueCallbacks uintptr

	// Accessibility / ApplicationServices
	axIsProcessTrusted                 func() bool
	axIsProcessTrustedWithOptions      func(options uintptr) bool
	axUIElementCreateSystemWide        func() uintptr
	axUIElementCreateApplication       func(pid int32) uintptr
	axUIElementCopyAttributeValue      func(el uintptr, attr uintptr, val *uintptr) int32
	axUIElementCopyParameterizedAttr   func(el uintptr, attr uintptr, param uintptr, res *uintptr) int32
	axUIElementSetAttributeValue       func(el uintptr, attr uintptr, val uintptr) int32
	axUIElementIsAttributeSettable     func(el uintptr, attr uintptr, settable *bool) int32
	axValueCreate                      func(theType int32, valPtr unsafe.Pointer) uintptr
	axValueGetValue                    func(val uintptr, theType int32, valPtr unsafe.Pointer) bool
	kAXTrustedCheckOptionPrompt        uintptr
	nsPasteboardTypeString             objc.ID
	nsWorkspaceDidActivateNotification objc.ID

	// CoreGraphics
	cgPreflightListenEventAccess func() bool
	cgRequestListenEventAccess   func() bool
	cgEventTapCreate             func(tap uint32, place uint32, opts uint32, eventsOfInterest uint64, callback uintptr, userInfo uintptr) uintptr
	cgEventTapEnable             func(tap uintptr, enable bool)
	cgEventCreate                func(source uintptr) uintptr
	cgEventGetFlags              func(event uintptr) uint64
	cgEventSetFlags              func(event uintptr, flags uint64)
	cgEventGetIntegerValueField  func(event uintptr, field uint32) int64
	cgEventSetIntegerValueField  func(event uintptr, field uint32, value int64)
	cgEventGetLocation           func(event uintptr) cgPoint
	cgEventCreateKeyboardEvent   func(source uintptr, virtualKey uint16, keyDown bool) uintptr
	cgEventKeyboardGetUnicodeStr func(event uintptr, maxLen uint, actualLen *uint, unicodeStr *uint16)
	cgEventKeyboardSetUnicodeStr func(event uintptr, length uint, unicodeStr *uint16)
	cgEventPost                  func(tap uint32, event uintptr)
	cgMainDisplayID              func() uint32
	cgDisplayBounds              func(display uint32) cgRect
	cgGetActiveDisplayList       func(maxDisplays uint32, activeDisplays *uint32, displayCount *uint32) int32

	// Carbon (Secure Input + Text Input Sources)
	isSecureEventInputEnabled         func() bool
	tisCreateInputSourceList          func(properties uintptr, includeAllInstalled bool) uintptr
	tisCopyCurrentKeyboardInputSource func() uintptr
	tisSelectInputSource              func(source uintptr) int32
	tisGetInputSourceProperty         func(source uintptr, key uintptr) uintptr
	pthreadMainNP                     func() int32 // libSystem: non-zero on the main thread
	kTISPropertyInputSourceID         uintptr
	kTISPropertyLocalizedName         uintptr
	kTISPropertyInputSourceLanguages  uintptr
	kTISPropertyInputSourceCategory   uintptr
	kTISCategoryKeyboardInputSource   uintptr
	kTISPropertyInputSourceIsSelect   uintptr
	kTISPropertyInputSourceIsEnable   uintptr
	kTISPropertyInputSourceIsASCIICap uintptr

	// Cached CFStrings for AX attribute names
	axAttrMu    sync.Mutex
	axAttrCache = make(map[string]uintptr)
)

func initDarwin() error {
	darwinInitOnce.Do(func() {
		var err error
		openLib := func(path string) uintptr {
			if err != nil {
				return 0
			}
			h, e := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
			if e != nil {
				err = fmt.Errorf("dlopen %s: %w", path, e)
				return 0
			}
			return h
		}
		libObjC = openLib("/usr/lib/libobjc.A.dylib")
		libCF = openLib("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		libCG = openLib("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		libAppKit = openLib("/System/Library/Frameworks/AppKit.framework/AppKit")
		libAX = openLib("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices")
		libCarbon = openLib("/System/Library/Frameworks/Carbon.framework/Carbon")
		libSystem := openLib("/usr/lib/libSystem.B.dylib")
		if err != nil {
			darwinInitErr = err
			return
		}

		reg := func(fn any, lib uintptr, name string) {
			if err != nil {
				return
			}
			sym, e := purego.Dlsym(lib, name)
			if e != nil {
				err = fmt.Errorf("dlsym %s: %w", name, e)
				return
			}
			purego.RegisterFunc(fn, sym)
		}
		symPtr := func(lib uintptr, name string) uintptr {
			if err != nil {
				return 0
			}
			p, e := purego.Dlsym(lib, name)
			if e != nil {
				err = fmt.Errorf("dlsym %s: %w", name, e)
				return 0
			}
			return **(**uintptr)(unsafe.Pointer(&p))
		}
		symAddr := func(lib uintptr, name string) uintptr {
			if err != nil {
				return 0
			}
			p, e := purego.Dlsym(lib, name)
			if e != nil {
				err = fmt.Errorf("dlsym %s: %w", name, e)
				return 0
			}
			return p
		}

		poolPushFn = symAddr(libObjC, "objc_autoreleasePoolPush")
		poolPopFn = symAddr(libObjC, "objc_autoreleasePoolPop")

		// CoreFoundation
		reg(&cfRelease, libCF, "CFRelease")
		reg(&cfRetain, libCF, "CFRetain")
		reg(&cfGetTypeID, libCF, "CFGetTypeID")
		reg(&cfStringGetTypeID, libCF, "CFStringGetTypeID")
		reg(&cfURLGetTypeID, libCF, "CFURLGetTypeID")
		reg(&cfURLGetString, libCF, "CFURLGetString")
		reg(&cfStringCreateWithCString, libCF, "CFStringCreateWithCString")
		reg(&cfStringGetCString, libCF, "CFStringGetCString")
		reg(&cfStringGetLength, libCF, "CFStringGetLength")
		reg(&cfArrayGetCount, libCF, "CFArrayGetCount")
		reg(&cfArrayGetValueAtIndex, libCF, "CFArrayGetValueAtIndex")
		reg(&cfBooleanGetValue, libCF, "CFBooleanGetValue")
		reg(&cfDictionaryCreate, libCF, "CFDictionaryCreate")
		reg(&cfMachPortCreateRunLoopSource, libCF, "CFMachPortCreateRunLoopSource")
		reg(&cfRunLoopGetCurrent, libCF, "CFRunLoopGetCurrent")
		reg(&cfRunLoopAddSource, libCF, "CFRunLoopAddSource")
		reg(&cfRunLoopRemoveSource, libCF, "CFRunLoopRemoveSource")
		reg(&cfRunLoopRun, libCF, "CFRunLoopRun")
		reg(&cfRunLoopStop, libCF, "CFRunLoopStop")

		kCFBooleanTrue = symPtr(libCF, "kCFBooleanTrue")
		kCFRunLoopCommonModes = symPtr(libCF, "kCFRunLoopCommonModes")
		kCFTypeDictionaryKeyCallbacks = symAddr(libCF, "kCFTypeDictionaryKeyCallBacks")
		kCFTypeDictionaryValueCallbacks = symAddr(libCF, "kCFTypeDictionaryValueCallBacks")

		// ApplicationServices / AX
		reg(&axIsProcessTrusted, libAX, "AXIsProcessTrusted")
		reg(&axIsProcessTrustedWithOptions, libAX, "AXIsProcessTrustedWithOptions")
		reg(&axUIElementCreateSystemWide, libAX, "AXUIElementCreateSystemWide")
		reg(&axUIElementCreateApplication, libAX, "AXUIElementCreateApplication")
		reg(&axUIElementCopyAttributeValue, libAX, "AXUIElementCopyAttributeValue")
		reg(&axUIElementCopyParameterizedAttr, libAX, "AXUIElementCopyParameterizedAttributeValue")
		reg(&axUIElementSetAttributeValue, libAX, "AXUIElementSetAttributeValue")
		reg(&axUIElementIsAttributeSettable, libAX, "AXUIElementIsAttributeSettable")
		reg(&axValueCreate, libAX, "AXValueCreate")
		reg(&axValueGetValue, libAX, "AXValueGetValue")
		kAXTrustedCheckOptionPrompt = symPtr(libAX, "kAXTrustedCheckOptionPrompt")

		// AppKit constants
		nsPasteboardTypeString = objc.ID(symPtr(libAppKit, "NSPasteboardTypeString"))
		nsWorkspaceDidActivateNotification = objc.ID(symPtr(libAppKit, "NSWorkspaceDidActivateApplicationNotification"))

		// CoreGraphics
		reg(&cgPreflightListenEventAccess, libCG, "CGPreflightListenEventAccess")
		reg(&cgRequestListenEventAccess, libCG, "CGRequestListenEventAccess")
		reg(&cgEventTapCreate, libCG, "CGEventTapCreate")
		reg(&cgEventTapEnable, libCG, "CGEventTapEnable")
		reg(&cgEventCreate, libCG, "CGEventCreate")
		reg(&cgEventGetFlags, libCG, "CGEventGetFlags")
		reg(&cgEventSetFlags, libCG, "CGEventSetFlags")
		reg(&cgEventGetIntegerValueField, libCG, "CGEventGetIntegerValueField")
		reg(&cgEventSetIntegerValueField, libCG, "CGEventSetIntegerValueField")
		reg(&cgEventGetLocation, libCG, "CGEventGetLocation")
		reg(&cgEventCreateKeyboardEvent, libCG, "CGEventCreateKeyboardEvent")
		reg(&cgEventKeyboardGetUnicodeStr, libCG, "CGEventKeyboardGetUnicodeString")
		reg(&cgEventKeyboardSetUnicodeStr, libCG, "CGEventKeyboardSetUnicodeString")
		reg(&cgEventPost, libCG, "CGEventPost")
		reg(&cgMainDisplayID, libCG, "CGMainDisplayID")
		reg(&cgDisplayBounds, libCG, "CGDisplayBounds")
		reg(&cgGetActiveDisplayList, libCG, "CGGetActiveDisplayList")

		// Carbon
		reg(&isSecureEventInputEnabled, libCarbon, "IsSecureEventInputEnabled")
		reg(&tisCreateInputSourceList, libCarbon, "TISCreateInputSourceList")
		reg(&tisCopyCurrentKeyboardInputSource, libCarbon, "TISCopyCurrentKeyboardInputSource")
		reg(&tisSelectInputSource, libCarbon, "TISSelectInputSource")
		reg(&tisGetInputSourceProperty, libCarbon, "TISGetInputSourceProperty")
		reg(&pthreadMainNP, libSystem, "pthread_main_np")
		kTISPropertyInputSourceID = symPtr(libCarbon, "kTISPropertyInputSourceID")
		kTISPropertyLocalizedName = symPtr(libCarbon, "kTISPropertyLocalizedName")
		kTISPropertyInputSourceLanguages = symPtr(libCarbon, "kTISPropertyInputSourceLanguages")
		kTISPropertyInputSourceCategory = symPtr(libCarbon, "kTISPropertyInputSourceCategory")
		kTISCategoryKeyboardInputSource = symPtr(libCarbon, "kTISCategoryKeyboardInputSource")
		kTISPropertyInputSourceIsSelect = symPtr(libCarbon, "kTISPropertyInputSourceIsSelectCapable")
		kTISPropertyInputSourceIsEnable = symPtr(libCarbon, "kTISPropertyInputSourceIsEnableCapable")
		kTISPropertyInputSourceIsASCIICap = symPtr(libCarbon, "kTISPropertyInputSourceIsASCIICapable")

		darwinInitErr = err
	})
	return darwinInitErr
}

func withAutoreleasePool(fn func()) {
	if poolPushFn == 0 || poolPopFn == 0 {
		fn()
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p, _, _ := purego.SyscallN(poolPushFn)
	defer purego.SyscallN(poolPopFn, p)
	fn()
}

func sel(name string) objc.SEL { return objc.RegisterName(name) }
func cls(name string) objc.ID  { return objc.ID(objc.GetClass(name)) }

func cstr(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}

func nsString(s string) objc.ID {
	return cls("NSString").Send(sel("stringWithUTF8String:"), cstr(s))
}

func goStringFromNS(id objc.ID) string {
	if id == 0 {
		return ""
	}
	p := objc.Send[unsafe.Pointer](id, sel("UTF8String"))
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

func cfString(s string) uintptr {
	return cfStringCreateWithCString(0, cstr(s), kCFStringEncodingUTF8)
}

func axAttr(name string) uintptr {
	axAttrMu.Lock()
	defer axAttrMu.Unlock()
	if v, ok := axAttrCache[name]; ok {
		return v
	}
	v := cfString(name)
	axAttrCache[name] = v
	return v
}

func goStringFromCF(cf uintptr) string {
	if cf == 0 {
		return ""
	}
	tid := cfGetTypeID(cf)
	if cfURLGetTypeID != nil && cfURLGetString != nil && tid == cfURLGetTypeID() {
		cf = cfURLGetString(cf)
		if cf == 0 {
			return ""
		}
		tid = cfGetTypeID(cf)
	}
	if tid != cfStringGetTypeID() {
		return ""
	}
	n := cfStringGetLength(cf)
	if n == 0 {
		return ""
	}
	buf := make([]byte, n*4+1)
	if !cfStringGetCString(cf, &buf[0], len(buf), kCFStringEncodingUTF8) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

type darwinBridge struct {
	mu           sync.Mutex
	started      bool
	stopCancel   context.CancelFunc
	tapDone      chan struct{}
	imeDone      chan struct{}
	triggerDone  chan struct{}
	triggerQueue chan host.TriggerEvent
	tapPort      uintptr
	tapSource    uintptr
	tapRunLoop   uintptr
	tapCallback  uintptr
	onTrigger    func(host.TriggerEvent)

	tapRecoveries  atomic.Int64
	triggers       *triggerRuntime
	lastBundleIDMu sync.RWMutex
	lastBundleID   string

	router        *triggerRouter
	keymapEngine  *KeymapLayerEngine
	imeEngine     *IMESwitchDecisionEngine
	imeCfg        config.IMEConfig
	windowCalc    *WindowLayoutCalculator
	pbTracker     *selfPasteboardTracker
	pbRestorer    *PasteboardSnapshotRestorer
	onIME         func(host.InputSourceInfo)
	onLayer       func(string)
	lastLayer     string
	inlineMu      sync.RWMutex
	inlineHandler func(host.InlineKeyEvent) bool
	inlineEnabled atomic.Bool
	// inlineForced bypasses the system-input-source gate: the user explicitly asked
	// the own engine to take over Chinese typing while another IME stays active.
	inlineForced atomic.Bool
	ime           inputEngineState
	shiftDown     bool
	shiftHadKey   bool
	// shiftToggle, when non-nil, is the pending bare-Shift ASCII toggle. It is
	// deferred while the selection bar's double-shift is armed so that one
	// double tap cannot both flip 中/英 and open the selection bar.
	shiftMu     sync.Mutex
	shiftToggle *time.Timer
}

func newPlatformBridge(opts Options) HostService {
	b := &darwinBridge{
		router:       newTriggerRouter(opts.TriggerConfig),
		keymapEngine: NewKeymapLayerEngine(opts.KeymapConfig),
		imeEngine:    NewIMESwitchDecisionEngine(opts.IMEConfig.Enabled, opts.IMERules),
		imeCfg:       opts.IMEConfig,
		windowCalc:   NewWindowLayoutCalculator(),
		pbTracker:    newSelfPasteboardTracker(),
		onIME:        opts.OnInputSourceChanged,
		onLayer:      opts.OnKeymapLayerChanged,
		triggers:     newTriggerRuntime(),
	}
	b.pbRestorer = NewPasteboardSnapshotRestorer(&darwinPasteboardBackend{bridge: b})
	b.ime.dirty = make(chan struct{}, 1)
	b.ime.anchorFn = func(ctx context.Context) (host.CursorAnchor, bool) {
		a, err := b.FastCaretAnchor(ctx)
		return a, err == nil
	}
	return b
}

// UpdateTriggerConfig updates DoubleTapDetector and HotkeyMatcher settings dynamically.
func (b *darwinBridge) UpdateTriggerConfig(cfg config.TriggerConfig) {
	b.router.UpdateConfig(cfg)
}

// UpdateKeymapConfig updates KeymapLayerEngine tap/hold and navigation layer settings dynamically.
func (b *darwinBridge) UpdateKeymapConfig(cfg config.KeymapConfig) {
	if b.keymapEngine != nil {
		b.keymapEngine.UpdateConfig(cfg)
	}
	b.mu.Lock()
	b.lastLayer = ""
	b.mu.Unlock()
}

// ActiveKeymapLayer reports the currently active keymap hold layer ("nav", "hyper", or "").
func (b *darwinBridge) ActiveKeymapLayer() string {
	if b.keymapEngine == nil {
		return ""
	}
	return b.keymapEngine.ActiveLayer()
}

// notifyLayerIfChanged records layer as the last observed keymap layer and invokes onLayer
// asynchronously (never on the event-tap thread) whenever it changed.
func (b *darwinBridge) notifyLayerIfChanged(layer string) {
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

// UpdateIMEConfig updates IME auto-switch configuration and rules dynamically.
func (b *darwinBridge) UpdateIMEConfig(imeCfg config.IMEConfig, rules config.IMERulesConfig) {
	b.mu.Lock()
	b.imeCfg = imeCfg
	b.mu.Unlock()
	b.imeEngine.UpdateRules(imeCfg.Enabled, rules)
}

func (b *darwinBridge) setCachedBundleID(bundleID string) {
	if bundleID == "" {
		return
	}
	b.lastBundleIDMu.Lock()
	b.lastBundleID = bundleID
	b.lastBundleIDMu.Unlock()
}

func (b *darwinBridge) cachedFrontmostBundleID() string {
	b.lastBundleIDMu.RLock()
	id := b.lastBundleID
	b.lastBundleIDMu.RUnlock()
	return id
}

// TapRecoveryCount returns how many times CGEventTap was automatically re-enabled after timeout/disable.
func (b *darwinBridge) TapRecoveryCount() int64 {
	return b.tapRecoveries.Load()
}

func (b *darwinBridge) Capabilities() host.Capabilities {
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

func (b *darwinBridge) Permissions(ctx context.Context) (host.PermissionStatus, error) {
	if err := ctx.Err(); err != nil {
		return host.PermissionStatus{}, err
	}
	if err := initDarwin(); err != nil {
		return host.PermissionStatus{}, err
	}
	ax := axIsProcessTrusted()
	im := cgPreflightListenEventAccess()
	return host.PermissionStatus{
		Accessibility:   ax,
		InputMonitoring: im,
		AppleEvents:     true,
		Granted:         ax && im,
		Promptable:      !(ax && im),
		Details: map[string]string{
			"secureInput": strconv.FormatBool(isSecureEventInputEnabled()),
		},
	}, nil
}

func (b *darwinBridge) RefreshPermissions(ctx context.Context) (host.PermissionStatus, error) {
	return b.Permissions(ctx)
}

func (b *darwinBridge) OpenPermissionSettings(kind string) error {
	var p permissionflow.Permission
	var anchor string
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "accessibility", "ax":
		p = permissionflow.Accessibility
		anchor = "Privacy_Accessibility"
	case "input_monitoring", "input-monitoring", "inputmonitoring":
		p = permissionflow.InputMonitoring
		anchor = "Privacy_ListenEvent"
	case "apple_events", "apple-events", "automation":
		anchor = "Privacy_Automation"
	default:
		p = permissionflow.Accessibility
		anchor = "Privacy_Accessibility"
	}
	if p != "" && mygo.App.IsReady() {
		return permissionflow.OpenSettings(p)
	}
	url := "x-apple.systempreferences:com.apple.preference.security?" + anchor
	return exec.Command("/usr/bin/open", url).Run()
}

func (b *darwinBridge) PromptPermission(kind string) (host.PermissionStatus, error) {
	if err := initDarwin(); err != nil {
		return host.PermissionStatus{}, err
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "accessibility", "ax", "":
		key := kAXTrustedCheckOptionPrompt
		val := kCFBooleanTrue
		dict := cfDictionaryCreate(0, &key, &val, 1, kCFTypeDictionaryKeyCallbacks, kCFTypeDictionaryValueCallbacks)
		if dict != 0 {
			defer cfRelease(dict)
		}
		_ = axIsProcessTrustedWithOptions(dict)
	case "input_monitoring", "input-monitoring", "inputmonitoring":
		_ = cgRequestListenEventAccess()
	}
	return b.Permissions(context.Background())
}

func (b *darwinBridge) Start(ctx context.Context, onTrigger func(host.TriggerEvent)) error {
	if err := initDarwin(); err != nil {
		return err
	}
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
	b.tapDone = make(chan struct{})
	b.imeDone = make(chan struct{})
	b.triggerQueue = triggerQueue
	b.triggerDone = triggerDone
	b.mu.Unlock()

	go b.runTriggerWorker(runCtx, triggerQueue, triggerDone, onTrigger)

	tapReady := make(chan error, 1)
	go b.runEventTap(runCtx, tapReady)
	if err := <-tapReady; err != nil {
		cancel()
		<-b.tapDone
		close(b.imeDone)
		<-triggerDone
		b.mu.Lock()
		b.started = false
		b.triggerQueue = nil
		b.mu.Unlock()
		return err
	}

	go b.runIMEObserver(runCtx)
	return nil
}

func (b *darwinBridge) Stop() error {
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		return nil
	}
	cancel := b.stopCancel
	tapPort := b.tapPort
	tapRL := b.tapRunLoop
	tapDone := b.tapDone
	imeDone := b.imeDone
	triggerDone := b.triggerDone
	b.triggerQueue = nil
	b.lastLayer = ""
	b.started = false
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if tapPort != 0 {
		cgEventTapEnable(tapPort, false)
	}
	if tapRL != 0 {
		cfRunLoopStop(tapRL)
	}
	if tapDone != nil {
		<-tapDone
	}
	if imeDone != nil {
		<-imeDone
	}
	if triggerDone != nil {
		<-triggerDone
	}
	return nil
}

func (b *darwinBridge) runEventTap(ctx context.Context, ready chan<- error) {
	defer close(b.tapDone)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	b.mu.Lock()
	if b.tapCallback == 0 {
		b.tapCallback = purego.NewCallback(func(proxy uintptr, eventType uint32, event uintptr, userInfo uintptr) uintptr {
			return b.handleCGEvent(eventType, event)
		})
	}
	cb := b.tapCallback
	b.mu.Unlock()

	mask := uint64(1<<kCGEventKeyDown) | uint64(1<<kCGEventKeyUp) | uint64(1<<kCGEventFlagsChanged) |
		uint64(1<<kCGEventLeftMouseDown) | uint64(1<<kCGEventLeftMouseUp) | uint64(1<<kCGEventRightMouseDown)
	port := cgEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault, mask, cb, 0)
	if port == 0 {
		ready <- errors.New("darwin: CGEventTapCreate failed (check Input Monitoring / Accessibility permission)")
		return
	}
	source := cfMachPortCreateRunLoopSource(0, port, 0)
	if source == 0 {
		cfRelease(port)
		ready <- errors.New("darwin: CFMachPortCreateRunLoopSource failed")
		return
	}
	rl := cfRunLoopGetCurrent()
	cfRunLoopAddSource(rl, source, kCFRunLoopCommonModes)
	cgEventTapEnable(port, true)

	b.mu.Lock()
	b.tapPort = port
	b.tapSource = source
	b.tapRunLoop = rl
	b.mu.Unlock()

	stopWatch := context.AfterFunc(ctx, func() {
		cgEventTapEnable(port, false)
		cfRunLoopStop(rl)
	})
	defer stopWatch()

	ready <- nil
	cfRunLoopRun()

	cfRunLoopRemoveSource(rl, source, kCFRunLoopCommonModes)
	cfRelease(source)
	cfRelease(port)

	b.mu.Lock()
	b.tapPort = 0
	b.tapSource = 0
	b.tapRunLoop = 0
	b.mu.Unlock()
}

func (b *darwinBridge) handleCGEvent(eventType uint32, event uintptr) uintptr {
	if eventType == kCGEventTapDisabledByTimeout || eventType == kCGEventTapDisabledByUserInput {
		b.mu.Lock()
		port := b.tapPort
		b.mu.Unlock()
		if port != 0 {
			cgEventTapEnable(port, true)
			b.tapRecoveries.Add(1)
		}
		return event
	}
	if event == 0 {
		return event
	}

	if cgEventGetIntegerValueField(event, kCGEventSourceUserData) == SyntheticEventTag {
		return event
	}

	nowMs := time.Now().UnixMilli()
	flags := cgEventGetFlags(event)
	keyCode := uint16(cgEventGetIntegerValueField(event, kCGKeyboardEventKeycode))

	switch eventType {
	case kCGEventLeftMouseDown, kCGEventRightMouseDown:
		// A click moves the insertion point / focus: drop any pending double-tap and snippet buffer.
		b.router.leaderDetector.Reset()
		b.router.doubleTapDetector.Reset()
		if eventType == kCGEventLeftMouseDown && b.triggers != nil {
			b.triggers.autoSelectMouseDown()
		}
		b.emitTriggerEvents([]host.TriggerEvent{{Kind: host.TriggerSnippetReset, TimestampMs: nowMs}})
		return event
	case kCGEventLeftMouseUp:
		if b.triggers != nil && b.triggers.autoSelectMouseUp() {
			if enabled, mode := b.triggers.selectionMode(); enabled && mode == "auto_select" {
				// AX is cross-process IPC; never perform it on the CGEventTap callback.
				go func(ts int64) {
					if snap, err := b.CaptureEditableField(context.Background()); err == nil && strings.TrimSpace(snap.SelectedText) != "" {
						b.emitTriggerEvents([]host.TriggerEvent{selectionEvent(ts)})
					}
				}(nowMs)
			}
		}
		return event

	case kCGEventFlagsChanged:
		modName, isDown, otherDown := darwinModifierEvent(keyCode, flags)
		if modName == "shift" {
			if b.triggers != nil && b.triggers.doubleShift(modName, isDown, otherDown, nowMs, false) {
				// A completed double tap belongs to the selection bar: cancel the
				// deferred ASCII toggle queued by the first tap.
				b.cancelShiftToggle()
				b.emitTriggerEvents([]host.TriggerEvent{selectionEvent(nowMs)})
				b.shiftDown = false
				return 0
			}
			if isDown {
				b.shiftDown = true
				b.shiftHadKey = false
			}
			if !isDown && b.shiftDown && !b.shiftHadKey && b.inlineEnabled.Load() && !isSecureEventInputEnabled() && b.inlineIMEGate() {
				b.shiftDown = false
				// With double-shift selection armed, the first tap must wait out the
				// double-tap window before it flips 中/英; otherwise one gesture would
				// both switch the input mode and open the selection bar.
				if armed, _ := b.triggers.selectionDoubleShiftArmed(); armed {
					b.deferShiftToggle(host.InlineKeyEvent{KeyName: "shift_tap", KeyCode: keyCode, IsDown: true, TimestampMs: nowMs})
				} else {
					_ = b.callInline(host.InlineKeyEvent{KeyName: "shift_tap", KeyCode: keyCode, IsDown: true, TimestampMs: nowMs})
				}
				return 0
			}
			if !isDown {
				b.shiftDown = false
			}
		}
		if modName == "" {
			b.router.leaderDetector.Reset()
			b.router.doubleTapDetector.Reset()
			return event
		}
		if b.keymapEngine != nil && b.keymapEngine.IsEnabled() {
			mods := darwinFlagsToModifiers(flags)
			outcome := b.keymapEngine.HandleKeyEvent(modName, isDown, mods, b.cachedFrontmostBundleID(), nowMs)
			b.notifyLayerIfChanged(outcome.LayerActive)
			for _, act := range outcome.EmitKeys {
				postDarwinKeyAction(act)
			}
			if outcome.Consume {
				return 0
			}
		} else {
			b.notifyLayerIfChanged("")
		}
		events := b.router.HandleModifier(modName, isDown, otherDown, nowMs)
		b.emitTriggerEvents(events)
		return event

	case kCGEventKeyDown:
		if b.shiftDown {
			b.shiftHadKey = true
		}
		keyName := darwinKeyCodeToName(keyCode)
		mods := darwinFlagsToModifiers(flags)
		if b.triggers != nil && b.triggers.shortcutMatches(keyName, mods) && !isSecureEventInputEnabled() {
			b.emitTriggerEvents([]host.TriggerEvent{optimizeEvent(nowMs)})
			return 0
		}
		if b.keymapEngine != nil && b.keymapEngine.IsEnabled() && keyName != "" {
			outcome := b.keymapEngine.HandleKeyEvent(keyName, true, mods, b.cachedFrontmostBundleID(), nowMs)
			b.notifyLayerIfChanged(outcome.LayerActive)
			for _, act := range outcome.EmitKeys {
				postDarwinKeyAction(act)
			}
			if outcome.Consume {
				b.router.leaderDetector.Reset()
				b.router.doubleTapDetector.Reset()
				return 0
			}
		} else {
			b.notifyLayerIfChanged("")
		}

		unicodeStr := darwinEventUnicodeString(event)
		isBackspace := keyCode == kVK_Delete
		secure := isSecureEventInputEnabled()

		// IME preedit detection is AX-bound and comparatively slow: only probe it for plain
		// (non-shortcut) keystrokes, which are the only ones that feed the snippet buffer.
		// AX marked-text queries are cross-process IPC. Avoid them on the typing hot path;
		// only query when a snippet-enabled trigger could actually complete.
		imeComposing := false
		if !hasNonShiftModifier(mods) && b.router.mayNeedMarkedText(unicodeStr) {
			imeComposing = darwinHasMarkedText()
		}
		if b.inlineEnabled.Load() && !isSecureEventInputEnabled() && b.inlineIMEGate() {
			if b.callInline(host.InlineKeyEvent{KeyName: keyName, Rune: unicodeStr, KeyCode: keyCode, Modifiers: mods, IsDown: true, TimestampMs: nowMs}) {
				return 0
			}
		}

		events, consume := b.router.HandleKeyDownWithIME(keyName, unicodeStr, mods, isBackspace, secure, imeComposing, nowMs)
		b.emitTriggerEvents(events)
		if consume {
			return 0
		}
		return event

	case kCGEventKeyUp:
		keyName := darwinKeyCodeToName(keyCode)
		if b.keymapEngine != nil && b.keymapEngine.IsEnabled() && keyName != "" {
			mods := darwinFlagsToModifiers(flags)
			outcome := b.keymapEngine.HandleKeyEvent(keyName, false, mods, b.cachedFrontmostBundleID(), nowMs)
			b.notifyLayerIfChanged(outcome.LayerActive)
			for _, act := range outcome.EmitKeys {
				postDarwinKeyAction(act)
			}
			if outcome.Consume {
				return 0
			}
		} else {
			b.notifyLayerIfChanged("")
		}
		return event
	}

	return event
}

func (b *darwinBridge) emitTriggerEvents(events []host.TriggerEvent) {
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

func (b *darwinBridge) runTriggerWorker(ctx context.Context, queue <-chan host.TriggerEvent, done chan<- struct{}, cb func(host.TriggerEvent)) {
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

func (b *darwinBridge) runIMEObserver(ctx context.Context) {
	defer close(b.imeDone)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var (
		prevApp     AppIdentity
		havePrevApp bool
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

			app := darwinFrontmostAppIdentity()
			if app.BundleID != "" {
				b.setCachedBundleID(app.BundleID)
			}
			if app.BundleID == "" && app.ProcessName == "" {
				continue
			}

			// An app activation moves the insertion point: snippet buffers typed in the
			// previous app must never leak into the newly focused one.
			if havePrevApp && appIdentityChanged(prevApp, app) && !isSelfClavisApp(app) {
				b.emitTriggerEvents([]host.TriggerEvent{{
					Kind:        host.TriggerSnippetReset,
					TimestampMs: time.Now().UnixMilli(),
				}})
			}
			prevApp = app
			havePrevApp = true

			if !enabled {
				continue
			}
			cur, err := b.CurrentInputSource(ctx)
			if err != nil {
				continue
			}
			decision := b.imeEngine.EvaluateAppActivation(app, cur.ID)
			if !decision.ShouldSwitch {
				continue
			}

			// Honor the configured settle delay so fast cmd-tab chains do not flap the input source.
			delayMs = min(max(delayMs, 0), 1000)
			if delayMs > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(delayMs) * time.Millisecond):
				}
				if latestApp := darwinFrontmostAppIdentity(); appIdentityChanged(latestApp, app) {
					continue
				}
			}

			if !b.applyIMEDecision(ctx, decision) {
				continue
			}
			if onIME != nil {
				if updated, err := b.CurrentInputSource(ctx); err == nil {
					onIME(updated)
				}
			}
		}
	}
}

// applyIMEDecision applies a switch decision, preferring an explicit input source and falling
// back to a mode-only switch. It reports whether any switch was applied successfully.
func (b *darwinBridge) applyIMEDecision(ctx context.Context, decision IMEDecision) bool {
	if decision.TargetInputSourceID != "" {
		if err := b.SelectInputSource(ctx, decision.TargetInputSourceID); err == nil {
			return true
		}
		if decision.TargetMode == "" {
			return false
		}
	}
	if decision.TargetMode == "" {
		return false
	}
	return b.SetIMEMode(ctx, decision.TargetMode) == nil
}

// darwinHasMarkedText reports whether the system-wide focused UI element currently holds IME
// marked (preedit/composition) text. Requires Accessibility trust; returns false without it.
func darwinHasMarkedText() bool {
	if initDarwin() != nil || axIsProcessTrusted == nil || !axIsProcessTrusted() {
		return false
	}
	sysWide := axUIElementCreateSystemWide()
	if sysWide == 0 {
		return false
	}
	defer cfRelease(sysWide)

	var focused uintptr
	if axUIElementCopyAttributeValue(sysWide, axAttr("AXFocusedUIElement"), &focused) != 0 || focused == 0 {
		return false
	}
	defer cfRelease(focused)

	var rangeVal uintptr
	if axUIElementCopyAttributeValue(focused, axAttr("AXMarkedTextRange"), &rangeVal) != 0 || rangeVal == 0 {
		return false
	}
	defer cfRelease(rangeVal)

	var r cfRange
	if !axValueGetValue(rangeVal, kAXValueCFRangeType, unsafe.Pointer(&r)) {
		return false
	}
	return r.Length > 0
}

func darwinFrontmostAppIdentity() AppIdentity {
	var app AppIdentity
	var pid int32
	withAutoreleasePool(func() {
		ws := cls("NSWorkspace").Send(sel("sharedWorkspace"))
		if ws == 0 {
			return
		}
		front := ws.Send(sel("frontmostApplication"))
		if front == 0 {
			return
		}
		pid = objc.Send[int32](front, sel("processIdentifier"))
		app.BundleID = goStringFromNS(front.Send(sel("bundleIdentifier")))
		execURL := front.Send(sel("executableURL"))
		if execURL != 0 {
			app.ProcessName = goStringFromNS(execURL.Send(sel("lastPathComponent")))
		}
		if app.ProcessName == "" {
			app.ProcessName = goStringFromNS(front.Send(sel("localizedName")))
		}
	})
	if pid > 0 && axIsProcessTrusted != nil && axIsProcessTrusted() {
		app.WindowTitle, app.ActiveURL = darwinInspectAXWindowContext(pid)
	}
	return app
}

func darwinInspectAXWindowContext(pid int32) (windowTitle string, activeURL string) {
	if pid <= 0 {
		return "", ""
	}
	appEl := axUIElementCreateApplication(pid)
	if appEl == 0 {
		return "", ""
	}
	defer cfRelease(appEl)

	var win uintptr
	if axUIElementCopyAttributeValue(appEl, axAttr("AXFocusedWindow"), &win) != 0 || win == 0 {
		if axUIElementCopyAttributeValue(appEl, axAttr("AXMainWindow"), &win) != 0 || win == 0 {
			return "", ""
		}
	}
	defer cfRelease(win)

	windowTitle = copyAXStringAttr(win, "AXTitle", 512)
	activeURL = extractAXWindowURL(win)
	return windowTitle, activeURL
}

func extractAXWindowURL(win uintptr) string {
	if doc := copyAXStringAttr(win, "AXDocument", 2048); doc != "" {
		return doc
	}
	if u := copyAXStringAttr(win, "AXURL", 2048); u != "" {
		return u
	}

	// Bounded shallow BFS walk over AXChildren for browser address bars / web areas (Safari, Chrome, Arc, Edge).
	type queueItem struct {
		el    uintptr
		depth int
	}
	cfRetain(win)
	queue := []queueItem{{el: win, depth: 0}}
	defer func() {
		for _, item := range queue {
			if item.el != 0 {
				cfRelease(item.el)
			}
		}
	}()

	const (
		maxDepth   = 5
		maxVisited = 64
	)
	visited := 0

	for len(queue) > 0 && visited < maxVisited {
		cur := queue[0]
		queue = queue[1:]
		visited++

		if cur.depth > 0 {
			if u := copyAXStringAttr(cur.el, "AXURL", 2048); isLikelyBrowserURL(u) {
				cfRelease(cur.el)
				return u
			}
			if doc := copyAXStringAttr(cur.el, "AXDocument", 2048); isLikelyBrowserURL(doc) {
				cfRelease(cur.el)
				return doc
			}
			role := copyAXStringAttr(cur.el, "AXRole", 64)
			if role == "AXTextField" || role == "AXComboBox" || role == "AXStaticText" || role == "AXWebArea" {
				desc := strings.ToLower(copyAXStringAttr(cur.el, "AXDescription", 128) + " " +
					copyAXStringAttr(cur.el, "AXTitle", 128) + " " +
					copyAXStringAttr(cur.el, "AXIdentifier", 128))
				if strings.Contains(desc, "address") || strings.Contains(desc, "url") || strings.Contains(desc, "omnibox") || strings.Contains(desc, "location") || strings.Contains(desc, "地址") {
					if val := copyAXStringAttr(cur.el, "AXValue", 2048); isLikelyBrowserURL(val) {
						cfRelease(cur.el)
						return val
					}
				}
			}
		}

		if cur.depth < maxDepth {
			var children uintptr
			if axUIElementCopyAttributeValue(cur.el, axAttr("AXChildren"), &children) == 0 && children != 0 {
				cnt := cfArrayGetCount(children)
				for i := 0; i < cnt && len(queue)+visited < maxVisited; i++ {
					child := cfArrayGetValueAtIndex(children, i)
					if child != 0 {
						cfRetain(child)
						queue = append(queue, queueItem{el: child, depth: cur.depth + 1})
					}
				}
				cfRelease(children)
			}
		}
		cfRelease(cur.el)
	}

	return ""
}

func isLikelyBrowserURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "file://") {
		return true
	}
	if strings.Contains(lower, ".") && !strings.HasPrefix(lower, ".") && !strings.HasSuffix(lower, ".") {
		return true
	}
	return false
}

func (b *darwinBridge) SnapshotContext(ctx context.Context) (host.ContextSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return host.ContextSnapshot{}, err
	}
	if err := initDarwin(); err != nil {
		return host.ContextSnapshot{}, err
	}

	var snap host.ContextSnapshot
	var pid int32
	withAutoreleasePool(func() {
		ws := cls("NSWorkspace").Send(sel("sharedWorkspace"))
		if ws != 0 {
			front := ws.Send(sel("frontmostApplication"))
			if front != 0 {
				pid = objc.Send[int32](front, sel("processIdentifier"))
				snap.BundleID = goStringFromNS(front.Send(sel("bundleIdentifier")))
				snap.AppName = goStringFromNS(front.Send(sel("localizedName")))
				if execURL := front.Send(sel("executableURL")); execURL != 0 {
					snap.ProcessName = goStringFromNS(execURL.Send(sel("lastPathComponent")))
				}
			}
		}
		pb := cls("NSPasteboard").Send(sel("generalPasteboard"))
		if pb != 0 && nsPasteboardTypeString != 0 {
			preview := goStringFromNS(pb.Send(sel("stringForType:"), nsPasteboardTypeString))
			snap.ClipboardPreview = truncateUTF8(preview, 512)
		}
	})
	if snap.BundleID != "" {
		b.setCachedBundleID(snap.BundleID)
	}

	if axIsProcessTrusted() {
		if pid > 0 {
			snap.WindowTitle, snap.ActiveURL = darwinInspectAXWindowContext(pid)
		}
		sysWide := axUIElementCreateSystemWide()
		if sysWide != 0 {
			defer cfRelease(sysWide)
			var focused uintptr
			if axUIElementCopyAttributeValue(sysWide, axAttr("AXFocusedUIElement"), &focused) == 0 && focused != 0 {
				defer cfRelease(focused)

				// An app that answers AXSelectedText (even with "") has told
				// us its selection; one that cannot leaves it to a copy probe,
				// unless the focus is on something that holds no text, as
				// Finder's file list, where a probe would only cost time.
				snap.SelectedText, snap.SelectionKnown = axStringAttr(focused, "AXSelectedText", 4096)
				if !snap.SelectionKnown && axRoleHoldsNoText(copyAXStringAttr(focused, "AXRole", 64)) {
					snap.SelectionKnown = true
				}
				snap.SurroundingText = copyAXStringAttr(focused, "AXValue", 4096)
				if snap.WindowTitle == "" || snap.ActiveURL == "" {
					var win uintptr
					if axUIElementCopyAttributeValue(focused, axAttr("AXWindow"), &win) == 0 && win != 0 {
						if snap.WindowTitle == "" {
							snap.WindowTitle = copyAXStringAttr(win, "AXTitle", 512)
						}
						if snap.ActiveURL == "" {
							snap.ActiveURL = extractAXWindowURL(win)
						}
						cfRelease(win)
					}
				}
				if anchor, ok := resolveAXCaretAnchor(focused); ok {
					snap.CursorAnchor = &anchor
				} else if anchor, ok := resolveAXWindowAnchor(focused); ok {
					snap.CursorAnchor = &anchor
				}
			}
		}
	}

	if snap.CursorAnchor == nil || !snap.CursorAnchor.Valid {
		if anchor, ok := resolveMouseAnchor(); ok {
			snap.CursorAnchor = &anchor
		}
	}

	if curIME, err := b.CurrentInputSource(ctx); err == nil && curIME.ID != "" {
		snap.ActiveInputSource = &curIME
	}

	return snap, nil
}

func copyAXStringAttr(el uintptr, attrName string, maxLen int) string {
	s, _ := axStringAttr(el, attrName, maxLen)
	return s
}

// axStringAttr reads a string attribute and reports whether the element
// answered: ok with "" means an empty value, not an unsupported attribute.
func axStringAttr(el uintptr, attrName string, maxLen int) (string, bool) {
	var val uintptr
	if axUIElementCopyAttributeValue(el, axAttr(attrName), &val) != 0 {
		return "", false
	}
	if val == 0 {
		return "", true
	}
	defer cfRelease(val)
	s := goStringFromCF(val)
	if maxLen > 0 {
		s = truncateUTF8(s, maxLen)
	}
	return s, true
}

func resolveAXCaretAnchor(focused uintptr) (host.CursorAnchor, bool) {
	var rangeVal uintptr
	if axUIElementCopyAttributeValue(focused, axAttr("AXSelectedTextRange"), &rangeVal) != 0 || rangeVal == 0 {
		return host.CursorAnchor{}, false
	}
	defer cfRelease(rangeVal)

	var r cfRange
	if !axValueGetValue(rangeVal, kAXValueCFRangeType, unsafe.Pointer(&r)) {
		return host.CursorAnchor{}, false
	}
	zeroRange := cfRange{Location: r.Location, Length: 0}
	param := axValueCreate(kAXValueCFRangeType, unsafe.Pointer(&zeroRange))
	if param == 0 {
		return host.CursorAnchor{}, false
	}
	defer cfRelease(param)

	var boundsVal uintptr
	if axUIElementCopyParameterizedAttr(focused, axAttr("AXBoundsForRange"), param, &boundsVal) != 0 || boundsVal == 0 {
		return host.CursorAnchor{}, false
	}
	defer cfRelease(boundsVal)

	var rect cgRect
	if !axValueGetValue(boundsVal, kAXValueCGRectType, unsafe.Pointer(&rect)) {
		return host.CursorAnchor{}, false
	}
	if rect.Size.Width == 0 && rect.Size.Height == 0 && rect.Origin.X == 0 && rect.Origin.Y == 0 {
		return host.CursorAnchor{}, false
	}
	return host.CursorAnchor{
		X:      rect.Origin.X,
		Y:      rect.Origin.Y,
		Width:  rect.Size.Width,
		Height: rect.Size.Height,
		Valid:  true,
	}, true
}

func resolveAXWindowAnchor(focused uintptr) (host.CursorAnchor, bool) {
	var win uintptr
	if axUIElementCopyAttributeValue(focused, axAttr("AXWindow"), &win) != 0 || win == 0 {
		return host.CursorAnchor{}, false
	}
	defer cfRelease(win)
	r, ok := getAXWindowRect(win)
	if !ok {
		return host.CursorAnchor{}, false
	}
	return host.CursorAnchor{
		X:      r.X + r.Width/2,
		Y:      r.Y + r.Height/3,
		Width:  1,
		Height: 18,
		Valid:  true,
	}, true
}

func resolveMouseAnchor() (host.CursorAnchor, bool) {
	ev := cgEventCreate(0)
	if ev == 0 {
		return host.CursorAnchor{}, false
	}
	defer cfRelease(ev)
	pt := cgEventGetLocation(ev)
	return host.CursorAnchor{
		X:      pt.X,
		Y:      pt.Y,
		Width:  1,
		Height: 18,
		Valid:  true,
	}, true
}

func getAXWindowRect(win uintptr) (Rect, bool) {
	var posVal, sizeVal uintptr
	if axUIElementCopyAttributeValue(win, axAttr("AXPosition"), &posVal) != 0 || posVal == 0 {
		return Rect{}, false
	}
	defer cfRelease(posVal)
	if axUIElementCopyAttributeValue(win, axAttr("AXSize"), &sizeVal) != 0 || sizeVal == 0 {
		return Rect{}, false
	}
	defer cfRelease(sizeVal)

	var pt cgPoint
	var sz cgSize
	if !axValueGetValue(posVal, kAXValueCGPointType, unsafe.Pointer(&pt)) {
		return Rect{}, false
	}
	if !axValueGetValue(sizeVal, kAXValueCGSizeType, unsafe.Pointer(&sz)) {
		return Rect{}, false
	}
	return Rect{X: pt.X, Y: pt.Y, Width: sz.Width, Height: sz.Height}, true
}

func setAXWindowRect(win uintptr, r Rect) error {
	pt := cgPoint{X: r.X, Y: r.Y}
	sz := cgSize{Width: r.Width, Height: r.Height}
	posVal := axValueCreate(kAXValueCGPointType, unsafe.Pointer(&pt))
	if posVal == 0 {
		return errors.New("darwin: AXValueCreate position failed")
	}
	defer cfRelease(posVal)

	sizeVal := axValueCreate(kAXValueCGSizeType, unsafe.Pointer(&sz))
	if sizeVal == 0 {
		return errors.New("darwin: AXValueCreate size failed")
	}
	defer cfRelease(sizeVal)

	if rc := axUIElementSetAttributeValue(win, axAttr("AXPosition"), posVal); rc != 0 {
		return fmt.Errorf("darwin: AXPosition set failed (%d)", rc)
	}
	if rc := axUIElementSetAttributeValue(win, axAttr("AXSize"), sizeVal); rc != 0 {
		return fmt.Errorf("darwin: AXSize set failed (%d)", rc)
	}
	return nil
}

func (b *darwinBridge) ExecuteWindowCommand(ctx context.Context, cmd host.WindowCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := initDarwin(); err != nil {
		return err
	}
	if !axIsProcessTrusted() {
		return errors.New("darwin: Accessibility permission required for window management")
	}

	var pid int32
	withAutoreleasePool(func() {
		ws := cls("NSWorkspace").Send(sel("sharedWorkspace"))
		if ws != 0 {
			front := ws.Send(sel("frontmostApplication"))
			if front != 0 {
				pid = objc.Send[int32](front, sel("processIdentifier"))
			}
		}
	})
	if pid <= 0 {
		return errors.New("darwin: no frontmost application")
	}

	appEl := axUIElementCreateApplication(pid)
	if appEl == 0 {
		return errors.New("darwin: AXUIElementCreateApplication failed")
	}
	defer cfRelease(appEl)

	var win uintptr
	if rc := axUIElementCopyAttributeValue(appEl, axAttr("AXFocusedWindow"), &win); rc != 0 || win == 0 {
		return fmt.Errorf("darwin: no focused window (%d)", rc)
	}
	defer cfRelease(win)

	curRect, ok := getAXWindowRect(win)
	if !ok {
		return errors.New("darwin: failed to read focused window bounds")
	}

	displays := darwinDisplays()
	winTitle := copyAXStringAttr(win, "AXTitle", 128)
	winID := fmt.Sprintf("pid:%d:%s", pid, winTitle)

	targetRect, applied, err := b.windowCalc.ComputeTargetRect(winID, curRect, cmd, displays)
	if err != nil || !applied {
		return err
	}
	return setAXWindowRect(win, targetRect)
}

func darwinDisplays() []DisplayInfo {
	var displays []DisplayInfo
	withAutoreleasePool(func() {
		screens := cls("NSScreen").Send(sel("screens"))
		if screens == 0 {
			return
		}
		count := objc.Send[uint](screens, sel("count"))
		if count == 0 {
			return
		}
		var primaryHeight float64
		for i := uint(0); i < count; i++ {
			scr := screens.Send(sel("objectAtIndex:"), i)
			frame := objc.Send[cgRect](scr, sel("frame"))
			visible := objc.Send[cgRect](scr, sel("visibleFrame"))
			if i == 0 {
				primaryHeight = frame.Size.Height
			}
			bounds := AppKitToQuartzRect(Rect{X: frame.Origin.X, Y: frame.Origin.Y, Width: frame.Size.Width, Height: frame.Size.Height}, primaryHeight)
			work := AppKitToQuartzRect(Rect{X: visible.Origin.X, Y: visible.Origin.Y, Width: visible.Size.Width, Height: visible.Size.Height}, primaryHeight)
			displays = append(displays, DisplayInfo{
				ID:       int64(i + 1),
				Bounds:   bounds,
				WorkArea: work,
				Primary:  i == 0,
			})
		}
	})
	if len(displays) > 0 {
		return displays
	}

	var ids [16]uint32
	var cnt uint32
	if cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &cnt) == 0 && cnt > 0 {
		mainID := cgMainDisplayID()
		for i := uint32(0); i < cnt; i++ {
			r := cgDisplayBounds(ids[i])
			rect := Rect{X: r.Origin.X, Y: r.Origin.Y, Width: r.Size.Width, Height: r.Size.Height}
			displays = append(displays, DisplayInfo{
				ID:       int64(ids[i]),
				Bounds:   rect,
				WorkArea: rect,
				Primary:  ids[i] == mainID,
			})
		}
	}
	return displays
}

// PasteClipboard implements MediaPaster by sending the platform paste shortcut
// after the caller has populated NSPasteboard. Accessibility is checked here so
// callers can distinguish an unavailable optional paste capability from a
// successful clipboard write.
func (b *darwinBridge) PasteClipboard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := initDarwin(); err != nil {
		return err
	}
	if !axIsProcessTrusted() {
		return errors.New("darwin: Accessibility permission required for clipboard paste")
	}
	b.IgnoreSelfPasteboardChange(b.ClipboardSequence())
	postTaggedKey(kVK_ANSI_V, kCGEventFlagMaskCommand)
	return nil
}

func (b *darwinBridge) InjectText(ctx context.Context, text string, deleteCount int, cursorBackOffset int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := initDarwin(); err != nil {
		return err
	}
	if !axIsProcessTrusted() {
		return errors.New("darwin: Accessibility permission required for text injection")
	}
	if utf8.RuneCountInString(text) <= 256 && !strings.ContainsAny(text, "\r\n\t") {
		for i := 0; i < deleteCount; i++ {
			postTaggedKey(kVK_Delete, 0)
		}
		units := utf16.Encode([]rune(text))
		for len(units) > 0 {
			n := len(units)
			if n > 20 {
				n = 20
			}
			postTaggedUnicode(units[:n])
			units = units[n:]
		}
		for i := 0; i < cursorBackOffset; i++ {
			postTaggedKey(kVK_LeftArrow, 0)
		}
		return nil
	}

	snap, snapErr := b.pbRestorer.Capture()

	for i := 0; i < deleteCount; i++ {
		postTaggedKey(kVK_Delete, 0)
	}

	var writtenChangeCount int64
	if text != "" {
		prevCC := snap.ChangeCount
		if snapErr != nil {
			prevCC = (&darwinPasteboardBackend{bridge: b}).ChangeCount()
		}
		b.IgnoreSelfPasteboardChange(prevCC + 1)
		var ok bool
		writtenChangeCount, ok = writeDarwinPasteboardString(text)
		if !ok {
			return errors.New("darwin: failed to write NSPasteboard for text injection")
		}
		b.IgnoreSelfPasteboardChange(writtenChangeCount)
		postTaggedKey(kVK_ANSI_V, kCGEventFlagMaskCommand)
		time.Sleep(45 * time.Millisecond)
	}

	for i := 0; i < cursorBackOffset; i++ {
		postTaggedKey(kVK_LeftArrow, 0)
	}

	if snapErr == nil && text != "" {
		_, _ = b.pbRestorer.RestoreIfUnmodified(snap, writtenChangeCount)
	}
	return nil
}

func trySetAXSelectedText(text string) bool {
	sysWide := axUIElementCreateSystemWide()
	if sysWide == 0 {
		return false
	}
	defer cfRelease(sysWide)

	var focused uintptr
	if axUIElementCopyAttributeValue(sysWide, axAttr("AXFocusedUIElement"), &focused) != 0 || focused == 0 {
		return false
	}
	defer cfRelease(focused)

	var settable bool
	if axUIElementIsAttributeSettable(focused, axAttr("AXSelectedText"), &settable) != 0 || !settable {
		return false
	}
	val := cfString(text)
	if val == 0 {
		return false
	}
	defer cfRelease(val)

	return axUIElementSetAttributeValue(focused, axAttr("AXSelectedText"), val) == 0
}

func postTaggedUnicode(units []uint16) {
	if len(units) == 0 || cgEventKeyboardSetUnicodeStr == nil {
		return
	}
	down := cgEventCreateKeyboardEvent(0, 0, true)
	if down != 0 {
		cgEventSetIntegerValueField(down, kCGEventSourceUserData, SyntheticEventTag)
		cgEventKeyboardSetUnicodeStr(down, uint(len(units)), &units[0])
		cgEventPost(kCGHIDEventTap, down)
		cfRelease(down)
	}
	up := cgEventCreateKeyboardEvent(0, 0, false)
	if up != 0 {
		cgEventSetIntegerValueField(up, kCGEventSourceUserData, SyntheticEventTag)
		cgEventKeyboardSetUnicodeStr(up, uint(len(units)), &units[0])
		cgEventPost(kCGHIDEventTap, up)
		cfRelease(up)
	}
}

func postTaggedKey(keyCode uint16, flags uint64) {
	down := cgEventCreateKeyboardEvent(0, keyCode, true)
	if down != 0 {
		cgEventSetIntegerValueField(down, kCGEventSourceUserData, SyntheticEventTag)
		if flags != 0 {
			cgEventSetFlags(down, flags)
		}
		cgEventPost(kCGHIDEventTap, down)
		cfRelease(down)
	}
	up := cgEventCreateKeyboardEvent(0, keyCode, false)
	if up != 0 {
		cgEventSetIntegerValueField(up, kCGEventSourceUserData, SyntheticEventTag)
		if flags != 0 {
			cgEventSetFlags(up, flags)
		}
		cgEventPost(kCGHIDEventTap, up)
		cfRelease(up)
	}
}

func postDarwinKeyAction(act KeyAction) {
	kc, ok := darwinNameToKeyCode(act.KeyName)
	if !ok {
		return
	}
	flags := darwinModifiersToFlags(act.Modifiers)
	postTaggedKey(kc, flags)
}

func darwinModifiersToFlags(mods []string) uint64 {
	var flags uint64
	for _, m := range mods {
		norm, ok := NormalizeShortcutModifier(m)
		if !ok {
			norm = strings.ToLower(strings.TrimSpace(m))
		}
		switch norm {
		case "command":
			flags |= kCGEventFlagMaskCommand
		case "control":
			flags |= kCGEventFlagMaskControl
		case "option":
			flags |= kCGEventFlagMaskAlternate
		case "shift":
			flags |= kCGEventFlagMaskShift
		case "fn":
			flags |= kCGEventFlagMaskSecondary
		}
	}
	return flags
}

func writeDarwinPasteboardString(text string) (int64, bool) {
	var cc int64
	var ok bool
	withAutoreleasePool(func() {
		pb := cls("NSPasteboard").Send(sel("generalPasteboard"))
		if pb == 0 || nsPasteboardTypeString == 0 {
			return
		}
		pb.Send(sel("clearContents"))
		ok = objc.Send[bool](pb, sel("setString:forType:"), nsString(text), nsPasteboardTypeString)
		cc = int64(objc.Send[int](pb, sel("changeCount")))
	})
	return cc, ok
}

func (b *darwinBridge) OpenApp(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return errors.New("darwin: empty app target")
	}
	if strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "~") || strings.HasSuffix(trimmed, ".app") {
		return exec.CommandContext(ctx, "/usr/bin/open", trimmed).Run()
	}
	if strings.Contains(trimmed, ".") {
		return exec.CommandContext(ctx, "/usr/bin/open", "-b", trimmed).Run()
	}
	return exec.CommandContext(ctx, "/usr/bin/open", "-a", trimmed).Run()
}

func (b *darwinBridge) InputSources(ctx context.Context) ([]host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := initDarwin(); err != nil {
		return nil, err
	}

	var out []host.InputSourceInfo
	var err error
	if mainErr := onMainThread(ctx, func() {
		list := tisCreateInputSourceList(0, false)
		if list == 0 {
			err = errors.New("darwin: TISCreateInputSourceList returned nil")
			return
		}
		defer cfRelease(list)

		n := cfArrayGetCount(list)
		out = make([]host.InputSourceInfo, 0, n)
		expectedCat := goStringFromCF(kTISCategoryKeyboardInputSource)

		for i := 0; i < n; i++ {
			src := cfArrayGetValueAtIndex(list, i)
			if src == 0 {
				continue
			}
			if kTISPropertyInputSourceCategory != 0 && expectedCat != "" {
				cat := goStringFromCF(tisGetInputSourceProperty(src, kTISPropertyInputSourceCategory))
				if cat != expectedCat {
					continue
				}
			}
			if kTISPropertyInputSourceIsSelect != 0 {
				selCap := tisGetInputSourceProperty(src, kTISPropertyInputSourceIsSelect)
				if selCap != 0 && !cfBooleanGetValue(selCap) {
					continue
				}
			}
			info := tisInputSourceToInfo(src)
			if info.ID != "" {
				out = append(out, info)
			}
		}
	}); mainErr != nil {
		return nil, mainErr
	}
	return out, err
}

func (b *darwinBridge) CurrentInputSource(ctx context.Context) (host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return host.InputSourceInfo{}, err
	}
	if err := initDarwin(); err != nil {
		return host.InputSourceInfo{}, err
	}

	var info host.InputSourceInfo
	found := false
	if err := onMainThread(ctx, func() {
		src := tisCopyCurrentKeyboardInputSource()
		if src == 0 {
			return
		}
		defer cfRelease(src)
		info, found = tisInputSourceToInfo(src), true
	}); err != nil {
		return host.InputSourceInfo{}, err
	}
	if !found {
		return host.InputSourceInfo{}, errors.New("darwin: TISCopyCurrentKeyboardInputSource returned nil")
	}
	return info, nil
}

func (b *darwinBridge) SelectInputSource(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := initDarwin(); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.New("darwin: empty input source id")
	}

	key := kTISPropertyInputSourceID
	val := cfString(trimmed)
	if val == 0 {
		return errors.New("darwin: CFStringCreateWithCString failed")
	}
	defer cfRelease(val)

	filter := cfDictionaryCreate(0, &key, &val, 1, kCFTypeDictionaryKeyCallbacks, kCFTypeDictionaryValueCallbacks)
	if filter == 0 {
		return errors.New("darwin: CFDictionaryCreate failed")
	}
	defer cfRelease(filter)

	var err error
	if mainErr := onMainThread(ctx, func() {
		list := tisCreateInputSourceList(filter, false)
		if list == 0 || cfArrayGetCount(list) == 0 {
			if list != 0 {
				cfRelease(list)
			}
			err = fmt.Errorf("darwin: input source %q not found", trimmed)
			return
		}
		defer cfRelease(list)

		src := cfArrayGetValueAtIndex(list, 0)
		if rc := tisSelectInputSource(src); rc != 0 {
			err = fmt.Errorf("darwin: TISSelectInputSource(%q) failed (%d)", trimmed, rc)
		}
	}); mainErr != nil {
		return mainErr
	}
	return err
}

// SetIMEMode switches to an ASCII-capable input source for IMEModeASCII,
// or a non-ASCII (Chinese/IME) input source for IMEModeNative.
func (b *darwinBridge) SetIMEMode(ctx context.Context, mode config.IMEMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode == config.IMEModeKeep || mode == "" {
		return nil
	}
	if mode != config.IMEModeASCII && mode != config.IMEModeNative {
		return fmt.Errorf("darwin: unsupported IME mode %q", mode)
	}

	cur, _ := b.CurrentInputSource(ctx)
	sources, err := b.InputSources(ctx)
	if err != nil {
		return err
	}

	switch mode {
	case config.IMEModeASCII:
		if cur.ID != "" && cur.IsASCII && !strings.Contains(strings.ToLower(cur.ID), "inputmethod") {
			return nil
		}
		b.mu.Lock()
		defaultID := strings.TrimSpace(b.imeCfg.DefaultInputSourceID)
		b.mu.Unlock()
		if defaultID != "" {
			for _, s := range sources {
				if s.ID == defaultID {
					return b.SelectInputSource(ctx, s.ID)
				}
			}
		}
		for _, s := range sources {
			if s.IsASCII && !strings.Contains(strings.ToLower(s.ID), "inputmethod") {
				return b.SelectInputSource(ctx, s.ID)
			}
		}
		for _, s := range sources {
			if s.IsASCII {
				return b.SelectInputSource(ctx, s.ID)
			}
		}
		return errors.New("darwin: no ASCII input source available")

	case config.IMEModeNative:
		if cur.ID != "" && (!cur.IsASCII || strings.Contains(strings.ToLower(cur.ID), "inputmethod")) {
			return nil
		}
		for _, s := range sources {
			if !s.IsASCII {
				return b.SelectInputSource(ctx, s.ID)
			}
		}
		for _, s := range sources {
			lowerID := strings.ToLower(s.ID)
			lowerLang := strings.ToLower(s.Language)
			if strings.Contains(lowerID, "inputmethod") || strings.HasPrefix(lowerLang, "zh") || strings.HasPrefix(lowerLang, "ja") || strings.HasPrefix(lowerLang, "ko") {
				return b.SelectInputSource(ctx, s.ID)
			}
		}
		return errors.New("darwin: no native IME input source available")
	}

	return nil
}

// asciiInputSourceIDLocked picks the ASCII input source to lock to during a Leader/Capsule
// session: an explicitly configured default when installed, else the first non-inputmethod
// ASCII layout, else any ASCII-capable source. Returns "" when no ASCII source exists.
func (b *darwinBridge) asciiInputSourceIDLocked(ctx context.Context) string {
	b.mu.Lock()
	defaultID := strings.TrimSpace(b.imeCfg.DefaultInputSourceID)
	b.mu.Unlock()

	sources, err := b.InputSources(ctx)
	if err != nil {
		// Without the installed list, the configured default is still the best guess.
		return defaultID
	}
	if defaultID != "" {
		for _, s := range sources {
			if s.ID == defaultID {
				return s.ID
			}
		}
	}
	for _, s := range sources {
		if s.IsASCII && !strings.Contains(strings.ToLower(s.ID), "inputmethod") {
			return s.ID
		}
	}
	for _, s := range sources {
		if s.IsASCII {
			return s.ID
		}
	}
	return ""
}

// LockTemporaryASCII temporarily switches to the ASCII input source for the duration of a
// Leader/Capsule session, remembering the pre-lock source so RestoreTemporaryIME can undo it.
// The app-rule restore stack is left untouched.
func (b *darwinBridge) LockTemporaryASCII(ctx context.Context) (bool, host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return false, host.InputSourceInfo{}, err
	}
	cur, err := b.CurrentInputSource(ctx)
	if err != nil {
		return false, host.InputSourceInfo{}, err
	}

	asciiID := b.asciiInputSourceIDLocked(ctx)
	wasNative := !cur.IsASCII || strings.Contains(strings.ToLower(cur.ID), "inputmethod")
	dec := b.imeEngine.TemporaryLockASCIIWithState(asciiID, cur.ID, wasNative)
	if !dec.ShouldSwitch {
		return false, cur, nil
	}

	if !b.applyIMEDecision(ctx, dec) {
		return false, cur, nil
	}

	if updated, err := b.CurrentInputSource(ctx); err == nil && updated.ID != "" {
		return true, updated, nil
	}
	return true, cur, nil
}

// RestoreTemporaryIME restores the input source (and native conversion mode) that was active
// before the most recent LockTemporaryASCII call.
func (b *darwinBridge) RestoreTemporaryIME(ctx context.Context) (bool, host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return false, host.InputSourceInfo{}, err
	}
	cur, _ := b.CurrentInputSource(ctx)
	dec := b.imeEngine.RestoreTemporaryLock(cur.ID)
	if !dec.ShouldSwitch {
		return false, cur, nil
	}

	if !b.applyIMEDecision(ctx, dec) {
		return false, cur, nil
	}

	if updated, err := b.CurrentInputSource(ctx); err == nil && updated.ID != "" {
		return true, updated, nil
	}
	return true, cur, nil
}

// ToggleInputSource cycles to the next selectable keyboard input source.
func (b *darwinBridge) ToggleInputSource(ctx context.Context) (host.InputSourceInfo, error) {
	sources, err := b.InputSources(ctx)
	if err != nil {
		return host.InputSourceInfo{}, err
	}
	if len(sources) == 0 {
		return host.InputSourceInfo{}, errors.New("darwin: no selectable input sources")
	}
	cur, err := b.CurrentInputSource(ctx)
	if err != nil {
		return host.InputSourceInfo{}, err
	}
	nextIdx := 0
	for i, s := range sources {
		if s.ID == cur.ID {
			nextIdx = (i + 1) % len(sources)
			break
		}
	}
	target := sources[nextIdx]
	if err := b.SelectInputSource(ctx, target.ID); err != nil {
		return host.InputSourceInfo{}, err
	}
	return target, nil
}

// onMainThread runs fn on the main thread and waits for it, or for ctx to
// end. The Text Input Sources API (TIS*) requires the main queue: on
// current macOS a call from another thread hits _dispatch_assert_queue_fail
// and the trap kills Clavis, as runIMEObserver's first poll did once the
// event tap started. On the main thread, and before the app runs (tests,
// the smoke check), fn runs at once. Elsewhere a canceled ctx stops the
// wait, and fn is skipped if the main thread has not started it: Stop
// cancels runIMEObserver's ctx and then waits for it on the main thread.
// When it returns an error fn may not have run, so its results must not be
// read. Callers must not hold a lock the main thread may wait for.
func onMainThread(ctx context.Context, fn func()) error {
	if !mygo.App.IsReady() || pthreadMainNP() != 0 {
		fn()
		return nil
	}
	done := make(chan struct{})
	go mygo.RunOnMain(func() {
		defer close(done)
		if ctx.Err() == nil {
			fn()
		}
	})
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tisInputSourceToInfo reads an input source's properties; main thread
// only (onMainThread).
func tisInputSourceToInfo(src uintptr) host.InputSourceInfo {
	idStr := goStringFromCF(tisGetInputSourceProperty(src, kTISPropertyInputSourceID))
	nameStr := goStringFromCF(tisGetInputSourceProperty(src, kTISPropertyLocalizedName))

	var lang string
	if langs := tisGetInputSourceProperty(src, kTISPropertyInputSourceLanguages); langs != 0 && cfArrayGetCount(langs) > 0 {
		lang = goStringFromCF(cfArrayGetValueAtIndex(langs, 0))
	}

	var isASCII bool
	if asciiProp := tisGetInputSourceProperty(src, kTISPropertyInputSourceIsASCIICap); asciiProp != 0 {
		isASCII = cfBooleanGetValue(asciiProp)
	}

	return host.InputSourceInfo{
		ID:       idStr,
		Name:     nameStr,
		Language: lang,
		IsASCII:  isASCII,
	}
}

func (b *darwinBridge) IgnoreSelfPasteboardChange(changeCount int64) {
	b.pbTracker.Ignore(changeCount)
}

// ClipboardSequence implements ClipboardSequencer so the poller can verify that an image or
// file-list read still belongs to the candidate returned by PollClipboardCandidate.
func (b *darwinBridge) ClipboardSequence() int64 {
	if initDarwin() != nil {
		return 0
	}
	var changeCount int64
	withAutoreleasePool(func() {
		pb := cls("NSPasteboard").Send(sel("generalPasteboard"))
		if pb != 0 {
			changeCount = int64(objc.Send[int](pb, sel("changeCount")))
		}
	})
	return changeCount
}

// classifyDarwinClipboard applies the clipboard media priority used by the poller: non-empty
// text wins over file URLs and images, then file URLs win over image representations. It only
// examines representation identifiers; payload bytes and file paths are deliberately not read.
func classifyDarwinClipboard(text string, types []string) clipboard.Kind {
	if text != "" {
		return clipboard.KindText
	}
	for _, raw := range types {
		t := strings.ToLower(strings.TrimSpace(raw))
		switch t {
		case "public.file-url", "nsfilenamespboardtype", "com.apple.pasteboard.promised-file-url":
			return clipboard.KindFile
		}
	}
	for _, raw := range types {
		t := strings.ToLower(strings.TrimSpace(raw))
		switch t {
		case "public.png", "public.tiff", "public.jpeg", "public.jpg", "public.gif", "public.heic", "public.heif", "public.bmp", "public.image", "nspasteboardtypepng", "nstiffpboardtype", "nspictpboardtype", "com.apple.tiff":
			return clipboard.KindImage
		}
		if strings.HasPrefix(t, "image/") {
			return clipboard.KindImage
		}
	}
	return ""
}

func (b *darwinBridge) PollClipboardCandidate(ctx context.Context) (*clipboard.CaptureCandidate, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if err := initDarwin(); err != nil {
		return nil, 0, err
	}

	var (
		changeCount int64
		text        string
		types       []string
	)
	withAutoreleasePool(func() {
		pb := cls("NSPasteboard").Send(sel("generalPasteboard"))
		if pb == 0 {
			return
		}
		changeCount = int64(objc.Send[int](pb, sel("changeCount")))
		if !b.pbTracker.ShouldProcess(changeCount) {
			return
		}
		if nsPasteboardTypeString != 0 {
			text = goStringFromNS(pb.Send(sel("stringForType:"), nsPasteboardTypeString))
		}
		items := pb.Send(sel("pasteboardItems"))
		if items != 0 {
			ic := objc.Send[uint](items, sel("count"))
			for i := uint(0); i < ic; i++ {
				item := items.Send(sel("objectAtIndex:"), i)
				tArr := item.Send(sel("types"))
				if tArr == 0 {
					continue
				}
				tc := objc.Send[uint](tArr, sel("count"))
				for j := uint(0); j < tc; j++ {
					tStr := goStringFromNS(tArr.Send(sel("objectAtIndex:"), j))
					if tStr != "" {
						types = append(types, tStr)
					}
				}
			}
		}
	})

	kind := classifyDarwinClipboard(text, types)
	if kind == "" {
		return nil, changeCount, nil
	}

	app := darwinFrontmostAppIdentity()
	var media clipboard.Media
	if kind != clipboard.KindText {
		media.Kind = kind
	}
	return &clipboard.CaptureCandidate{
		Content:         text,
		Media:           media,
		SourceBundleID:  app.BundleID,
		SourceAppName:   app.ProcessName,
		PasteboardTypes: types,
		Timestamp:       time.Now().UTC(),
	}, changeCount, nil
}

// darwinPasteboardBackend implements PasteboardBackend over NSPasteboard.generalPasteboard.
type darwinPasteboardBackend struct {
	bridge *darwinBridge
}

func (p *darwinPasteboardBackend) ChangeCount() int64 {
	if initDarwin() != nil {
		return 0
	}
	var cc int64
	withAutoreleasePool(func() {
		pb := cls("NSPasteboard").Send(sel("generalPasteboard"))
		if pb != 0 {
			cc = int64(objc.Send[int](pb, sel("changeCount")))
		}
	})
	return cc
}

func (p *darwinPasteboardBackend) Capture() (PasteboardSnapshot, error) {
	if err := initDarwin(); err != nil {
		return PasteboardSnapshot{}, err
	}
	var snap PasteboardSnapshot
	withAutoreleasePool(func() {
		pb := cls("NSPasteboard").Send(sel("generalPasteboard"))
		if pb == 0 {
			return
		}
		snap.ChangeCount = int64(objc.Send[int](pb, sel("changeCount")))
		items := pb.Send(sel("pasteboardItems"))
		if items == 0 {
			return
		}
		ic := objc.Send[uint](items, sel("count"))
		for i := uint(0); i < ic; i++ {
			item := items.Send(sel("objectAtIndex:"), i)
			tArr := item.Send(sel("types"))
			if tArr == 0 {
				continue
			}
			var itemSnap PasteboardItemSnapshot
			tc := objc.Send[uint](tArr, sel("count"))
			for j := uint(0); j < tc; j++ {
				tID := tArr.Send(sel("objectAtIndex:"), j)
				tStr := goStringFromNS(tID)
				if tStr == "" {
					continue
				}
				nsData := item.Send(sel("dataForType:"), tID)
				if nsData == 0 {
					continue
				}
				length := objc.Send[uint](nsData, sel("length"))
				var raw []byte
				if length > 0 {
					ptr := objc.Send[unsafe.Pointer](nsData, sel("bytes"))
					if ptr != nil {
						raw = make([]byte, length)
						copy(raw, unsafe.Slice((*byte)(ptr), length))
					}
				}
				itemSnap.Representations = append(itemSnap.Representations, PasteboardRepresentation{
					Type: tStr,
					Data: raw,
				})
			}
			if len(itemSnap.Representations) > 0 {
				snap.Items = append(snap.Items, itemSnap)
			}
		}
	})
	return snap, nil
}

func (p *darwinPasteboardBackend) Restore(snap PasteboardSnapshot) error {
	if err := initDarwin(); err != nil {
		return err
	}
	withAutoreleasePool(func() {
		pb := cls("NSPasteboard").Send(sel("generalPasteboard"))
		if pb == 0 {
			return
		}
		pb.Send(sel("clearContents"))
		if len(snap.Items) == 0 {
			cc := int64(objc.Send[int](pb, sel("changeCount")))
			p.bridge.IgnoreSelfPasteboardChange(cc)
			return
		}

		arr := cls("NSMutableArray").Send(sel("arrayWithCapacity:"), uint(len(snap.Items)))
		for _, item := range snap.Items {
			pbItem := cls("NSPasteboardItem").Send(sel("alloc")).Send(sel("init"))
			for _, rep := range item.Representations {
				var nsData objc.ID
				if len(rep.Data) > 0 {
					nsData = cls("NSData").Send(sel("dataWithBytes:length:"), unsafe.Pointer(&rep.Data[0]), uint(len(rep.Data)))
				} else {
					nsData = cls("NSData").Send(sel("data"))
				}
				pbItem.Send(sel("setData:forType:"), nsData, nsString(rep.Type))
			}
			arr.Send(sel("addObject:"), pbItem)
			pbItem.Send(sel("release"))
		}
		_ = objc.Send[bool](pb, sel("writeObjects:"), arr)
		cc := int64(objc.Send[int](pb, sel("changeCount")))
		p.bridge.IgnoreSelfPasteboardChange(cc)
	})
	return nil
}

func darwinModifierEvent(keyCode uint16, flags uint64) (modName string, isDown bool, otherDown bool) {
	switch keyCode {
	case kVK_RightOption:
		isDown = flags&nxDeviceRAltMask != 0
		otherDown = (flags & (kCGEventFlagMaskCommand | kCGEventFlagMaskShift | kCGEventFlagMaskControl | nxDeviceLAltMask)) != 0
		return "right_option", isDown, otherDown
	case kVK_Option:
		isDown = flags&nxDeviceLAltMask != 0
		otherDown = (flags & (kCGEventFlagMaskCommand | kCGEventFlagMaskShift | kCGEventFlagMaskControl | nxDeviceRAltMask)) != 0
		return "left_option", isDown, otherDown
	case kVK_RightCommand:
		isDown = flags&nxDeviceRCmdMask != 0
		otherDown = (flags & (kCGEventFlagMaskAlternate | kCGEventFlagMaskShift | kCGEventFlagMaskControl | nxDeviceLCmdMask)) != 0
		return "right_command", isDown, otherDown
	case kVK_Command:
		isDown = flags&nxDeviceLCmdMask != 0
		otherDown = (flags & (kCGEventFlagMaskAlternate | kCGEventFlagMaskShift | kCGEventFlagMaskControl | nxDeviceRCmdMask)) != 0
		return "left_command", isDown, otherDown
	case kVK_RightShift:
		isDown = flags&nxDeviceRShiftMask != 0
		otherDown = (flags & (kCGEventFlagMaskAlternate | kCGEventFlagMaskCommand | kCGEventFlagMaskControl | nxDeviceLShiftMask)) != 0
		return "right_shift", isDown, otherDown
	case kVK_Shift:
		isDown = flags&nxDeviceLShiftMask != 0
		otherDown = (flags & (kCGEventFlagMaskAlternate | kCGEventFlagMaskCommand | kCGEventFlagMaskControl | nxDeviceRShiftMask)) != 0
		return "left_shift", isDown, otherDown
	case kVK_RightControl:
		isDown = flags&nxDeviceRCtlMask != 0
		otherDown = (flags & (kCGEventFlagMaskAlternate | kCGEventFlagMaskCommand | kCGEventFlagMaskShift | nxDeviceLCtlMask)) != 0
		return "right_control", isDown, otherDown
	case kVK_Control:
		isDown = flags&nxDeviceLCtlMask != 0
		otherDown = (flags & (kCGEventFlagMaskAlternate | kCGEventFlagMaskCommand | kCGEventFlagMaskShift | nxDeviceRCtlMask)) != 0
		return "left_control", isDown, otherDown
	case kVK_CapsLock:
		isDown = flags&kCGEventFlagMaskAlphaShift != 0
		otherDown = (flags & (kCGEventFlagMaskAlternate | kCGEventFlagMaskCommand | kCGEventFlagMaskShift | kCGEventFlagMaskControl)) != 0
		return "caps_lock", isDown, otherDown
	default:
		return "", false, true
	}
}

func darwinFlagsToModifiers(flags uint64) []string {
	var mods []string
	if flags&kCGEventFlagMaskCommand != 0 {
		mods = append(mods, "command")
	}
	if flags&kCGEventFlagMaskAlternate != 0 {
		mods = append(mods, "option")
	}
	if flags&kCGEventFlagMaskShift != 0 {
		mods = append(mods, "shift")
	}
	if flags&kCGEventFlagMaskControl != 0 {
		mods = append(mods, "control")
	}
	if flags&kCGEventFlagMaskSecondary != 0 {
		mods = append(mods, "fn")
	}
	return mods
}

func darwinEventUnicodeString(event uintptr) string {
	var buf [8]uint16
	var actual uint
	cgEventKeyboardGetUnicodeStr(event, uint(len(buf)), &actual, &buf[0])
	if actual == 0 {
		return ""
	}
	if actual > uint(len(buf)) {
		actual = uint(len(buf))
	}
	var b strings.Builder
	for _, r := range utf16.Decode(buf[:actual]) {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func darwinNameToKeyCode(name string) (uint16, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "a":
		return kVK_ANSI_A, true
	case "b":
		return kVK_ANSI_B, true
	case "c":
		return kVK_ANSI_C, true
	case "d":
		return kVK_ANSI_D, true
	case "e":
		return kVK_ANSI_E, true
	case "f":
		return kVK_ANSI_F, true
	case "g":
		return kVK_ANSI_G, true
	case "h":
		return kVK_ANSI_H, true
	case "i":
		return kVK_ANSI_I, true
	case "j":
		return kVK_ANSI_J, true
	case "k":
		return kVK_ANSI_K, true
	case "l":
		return kVK_ANSI_L, true
	case "m":
		return kVK_ANSI_M, true
	case "n":
		return kVK_ANSI_N, true
	case "o":
		return kVK_ANSI_O, true
	case "p":
		return kVK_ANSI_P, true
	case "q":
		return kVK_ANSI_Q, true
	case "r":
		return kVK_ANSI_R, true
	case "s":
		return kVK_ANSI_S, true
	case "t":
		return kVK_ANSI_T, true
	case "u":
		return kVK_ANSI_U, true
	case "v":
		return kVK_ANSI_V, true
	case "w":
		return kVK_ANSI_W, true
	case "x":
		return kVK_ANSI_X, true
	case "y":
		return kVK_ANSI_Y, true
	case "z":
		return kVK_ANSI_Z, true
	case "0":
		return kVK_ANSI_0, true
	case "1":
		return kVK_ANSI_1, true
	case "2":
		return kVK_ANSI_2, true
	case "3":
		return kVK_ANSI_3, true
	case "4":
		return kVK_ANSI_4, true
	case "5":
		return kVK_ANSI_5, true
	case "6":
		return kVK_ANSI_6, true
	case "7":
		return kVK_ANSI_7, true
	case "8":
		return kVK_ANSI_8, true
	case "9":
		return kVK_ANSI_9, true
	case "space":
		return kVK_Space, true
	case "return", "enter":
		return kVK_Return, true
	case "tab":
		return kVK_Tab, true
	case "escape", "esc":
		return kVK_Escape, true
	case "backspace":
		return kVK_Delete, true
	case "delete", "forwarddelete":
		return kVK_ForwardDelete, true
	case "left":
		return kVK_LeftArrow, true
	case "right":
		return kVK_RightArrow, true
	case "up":
		return kVK_UpArrow, true
	case "down":
		return kVK_DownArrow, true
	case "home":
		return kVK_Home, true
	case "end":
		return kVK_End, true
	case "pageup":
		return kVK_PageUp, true
	case "pagedown":
		return kVK_PageDown, true
	case "minus", "-":
		return kVK_ANSI_Minus, true
	case "equal", "=":
		return kVK_ANSI_Equal, true
	case "leftbracket", "[":
		return kVK_ANSI_LeftBrk, true
	case "rightbracket", "]":
		return kVK_ANSI_RightBrk, true
	case "semicolon", ";":
		return kVK_ANSI_Semi, true
	case "quote", "'":
		return kVK_ANSI_Quote, true
	case "comma", ",":
		return kVK_ANSI_Comma, true
	case "period", ".":
		return kVK_ANSI_Period, true
	case "slash", "/":
		return kVK_ANSI_Slash, true
	case "backslash", `\`:
		return kVK_ANSI_Backsl, true
	case "grave", "backtick", "`":
		return kVK_ANSI_Grave, true
	case "f1":
		return kVK_F1, true
	case "f2":
		return kVK_F2, true
	case "f3":
		return kVK_F3, true
	case "f4":
		return kVK_F4, true
	case "f5":
		return kVK_F5, true
	case "f6":
		return kVK_F6, true
	case "f7":
		return kVK_F7, true
	case "f8":
		return kVK_F8, true
	case "f9":
		return kVK_F9, true
	case "f10":
		return kVK_F10, true
	case "f11":
		return kVK_F11, true
	case "f12":
		return kVK_F12, true
	default:
		return 0, false
	}
}

func darwinKeyCodeToName(code uint16) string {
	switch code {
	case kVK_ANSI_A:
		return "a"
	case kVK_ANSI_B:
		return "b"
	case kVK_ANSI_C:
		return "c"
	case kVK_ANSI_D:
		return "d"
	case kVK_ANSI_E:
		return "e"
	case kVK_ANSI_F:
		return "f"
	case kVK_ANSI_G:
		return "g"
	case kVK_ANSI_H:
		return "h"
	case kVK_ANSI_I:
		return "i"
	case kVK_ANSI_J:
		return "j"
	case kVK_ANSI_K:
		return "k"
	case kVK_ANSI_L:
		return "l"
	case kVK_ANSI_M:
		return "m"
	case kVK_ANSI_N:
		return "n"
	case kVK_ANSI_O:
		return "o"
	case kVK_ANSI_P:
		return "p"
	case kVK_ANSI_Q:
		return "q"
	case kVK_ANSI_R:
		return "r"
	case kVK_ANSI_S:
		return "s"
	case kVK_ANSI_T:
		return "t"
	case kVK_ANSI_U:
		return "u"
	case kVK_ANSI_V:
		return "v"
	case kVK_ANSI_W:
		return "w"
	case kVK_ANSI_X:
		return "x"
	case kVK_ANSI_Y:
		return "y"
	case kVK_ANSI_Z:
		return "z"
	case kVK_ANSI_0:
		return "0"
	case kVK_ANSI_1:
		return "1"
	case kVK_ANSI_2:
		return "2"
	case kVK_ANSI_3:
		return "3"
	case kVK_ANSI_4:
		return "4"
	case kVK_ANSI_5:
		return "5"
	case kVK_ANSI_6:
		return "6"
	case kVK_ANSI_7:
		return "7"
	case kVK_ANSI_8:
		return "8"
	case kVK_ANSI_9:
		return "9"
	case kVK_Space:
		return "space"
	case kVK_Return:
		return "return"
	case kVK_Tab:
		return "tab"
	case kVK_Escape:
		return "escape"
	case kVK_Delete:
		return "backspace"
	case kVK_ForwardDelete:
		return "forwarddelete"
	case kVK_LeftArrow:
		return "left"
	case kVK_RightArrow:
		return "right"
	case kVK_UpArrow:
		return "up"
	case kVK_DownArrow:
		return "down"
	case kVK_Home:
		return "home"
	case kVK_End:
		return "end"
	case kVK_PageUp:
		return "pageup"
	case kVK_PageDown:
		return "pagedown"
	case kVK_ANSI_Minus:
		return "minus"
	case kVK_ANSI_Equal:
		return "equal"
	case kVK_ANSI_LeftBrk:
		return "leftbracket"
	case kVK_ANSI_RightBrk:
		return "rightbracket"
	case kVK_ANSI_Semi:
		return "semicolon"
	case kVK_ANSI_Quote:
		return "quote"
	case kVK_ANSI_Comma:
		return "comma"
	case kVK_ANSI_Period:
		return "period"
	case kVK_ANSI_Slash:
		return "slash"
	case kVK_ANSI_Backsl:
		return "backslash"
	case kVK_ANSI_Grave:
		return "grave"
	case kVK_F1:
		return "f1"
	case kVK_F2:
		return "f2"
	case kVK_F3:
		return "f3"
	case kVK_F4:
		return "f4"
	case kVK_F5:
		return "f5"
	case kVK_F6:
		return "f6"
	case kVK_F7:
		return "f7"
	case kVK_F8:
		return "f8"
	case kVK_F9:
		return "f9"
	case kVK_F10:
		return "f10"
	case kVK_F11:
		return "f11"
	case kVK_F12:
		return "f12"
	default:
		return ""
	}
}

// cancelShiftToggle drops a deferred bare-Shift ASCII toggle.
func (b *darwinBridge) cancelShiftToggle() {
	b.shiftMu.Lock()
	if b.shiftToggle != nil {
		b.shiftToggle.Stop()
		b.shiftToggle = nil
	}
	b.shiftMu.Unlock()
}

// deferShiftToggle queues a bare-Shift ASCII toggle until the selection bar's
// double-tap window elapses. Completing the double tap cancels it, so exactly
// one of the two behaviours runs per gesture.
func (b *darwinBridge) deferShiftToggle(ev host.InlineKeyEvent) {
	armed, windowMs := b.triggers.selectionDoubleShiftArmed()
	if !armed || windowMs <= 0 {
		_ = b.callInline(ev)
		return
	}
	b.shiftMu.Lock()
	if b.shiftToggle != nil {
		b.shiftToggle.Stop()
	}
	b.shiftToggle = time.AfterFunc(time.Duration(windowMs)*time.Millisecond, func() {
		b.shiftMu.Lock()
		b.shiftToggle = nil
		b.shiftMu.Unlock()
		if b.inlineEnabled.Load() && !isSecureEventInputEnabled() && b.inlineIMEGate() {
			_ = b.callInline(ev)
		}
	})
	b.shiftMu.Unlock()
}
