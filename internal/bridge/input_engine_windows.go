//go:build windows

package bridge

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// Own input engine host, route B (docs/design/own-input-engine.md): the LL keyboard hook feeds
// keys to the pure inputengine, commits are SendInput(KEYEVENTF_UNICODE) on the hook thread
// before it returns, and views are pushed to the UI's candidate panel off-thread.

const keyeventfUnicodeFlag = 0x0004

type inputEngineState struct {
	eng     atomic.Pointer[inputengine.Engine]
	viewMu  sync.Mutex
	viewFn  func(InputEngineView)
	dirty   chan struct{}
	anchor  atomic.Pointer[host.CursorAnchor]
	started atomic.Bool
}

// ConfigureInputEngine implements InputEngineHost.
func (b *windowsBridge) ConfigureInputEngine(cfg config.InputEngineConfig) {
	st := &b.ime
	if !cfg.Enabled || (cfg.Mode != "" && cfg.Mode != "inline_hook" && cfg.Mode != "dual") {
		if old := st.eng.Swap(nil); old != nil {
			old.Reset()
			b.notifyInputEngineView()
		}
		return
	}
	eng := inputengine.New(inputengine.Options{CandidateCount: cfg.CandidateCount, ShiftToggleASCII: cfg.ShiftToggleASCII})
	if old := st.eng.Swap(eng); old != nil {
		old.Reset()
	}
	if st.started.CompareAndSwap(false, true) {
		go b.runInputEngineViewWorker()
	}
	b.notifyInputEngineView()
}

// SetInputEngineViewHandler implements InputEngineHost.
func (b *windowsBridge) SetInputEngineViewHandler(fn func(InputEngineView)) {
	b.ime.viewMu.Lock()
	b.ime.viewFn = fn
	b.ime.viewMu.Unlock()
}

// SetInputEngineAICandidates implements InputEngineHost.
func (b *windowsBridge) SetInputEngineAICandidates(raw string, words []string) {
	if eng := b.ime.eng.Load(); eng != nil && eng.SetAICandidates(raw, words) {
		b.notifyInputEngineView()
	}
}

func (b *windowsBridge) resetInputEngine() {
	if eng := b.ime.eng.Load(); eng != nil && eng.Reset() {
		b.notifyInputEngineView()
	}
}

// notifyInputEngineView is non-blocking (safe on the hook thread).
func (b *windowsBridge) notifyInputEngineView() {
	select {
	case b.ime.dirty <- struct{}{}:
	default:
	}
}

// runInputEngineViewWorker coalesces view updates and resolves the caret anchor once per
// composition (GetGUIThreadInfo is a cross-thread call and must stay off the hook thread).
func (b *windowsBridge) runInputEngineViewWorker() {
	for range b.ime.dirty {
		eng := b.ime.eng.Load()
		var v InputEngineView
		if eng != nil {
			v.View = eng.View()
			v.Raw = eng.Raw()
		}
		if v.View.Composing {
			if b.ime.anchor.Load() == nil {
				_, hwnd := winForegroundApp()
				if a, ok := resolveWinCaretAnchor(hwnd); ok {
					b.ime.anchor.Store(&a)
				} else if a, ok := resolveWinMouseAnchor(); ok {
					b.ime.anchor.Store(&a)
				}
			}
			v.Anchor = b.ime.anchor.Load()
		} else {
			b.ime.anchor.Store(nil)
		}
		b.ime.viewMu.Lock()
		fn := b.ime.viewFn
		b.ime.viewMu.Unlock()
		if fn != nil {
			fn(v)
		}
	}
}

// inputEngineHandleKey runs inside the LL hook. Returns consume=true to swallow the key.
// Any panic resets the engine and passes the key through (never eat keys on a bug).
func (b *windowsBridge) inputEngineHandleKey(vk uint16, scan uint32, isDown bool, now time.Time) (consume bool) {
	eng := b.ime.eng.Load()
	if eng == nil {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			eng.Reset()
			consume = false
		}
	}()

	if vk == vkShift || vk == vkLShift || vk == vkRShift {
		kind := inputengine.KeyShiftUp
		if isDown {
			kind = inputengine.KeyShiftDown
		}
		if res := eng.HandleKey(inputengine.Key{Kind: kind}); res.Changed || res.Commit != "" {
			sendWinUnicodeText(res.Commit)
			b.notifyInputEngineView()
		}
		return false // Shift always reaches the router (leader / double-tap unaffected)
	}
	if !isDown {
		return false
	}
	if _, isMod := winVKToModifier(vk); isMod {
		return false
	}

	app := AppIdentity{ProcessName: b.cachedFrontmostProcessName()}
	if !inputEngineEligible(b.hookIMEComposing(now), b.hookSecureInput(now), isSelfClavisApp(app)) {
		if eng.Reset() {
			b.notifyInputEngineView()
		}
		return false
	}

	chord := winVKDown(vkLControl) || winVKDown(vkRControl) || winVKDown(vkLMenu) || winVKDown(vkRMenu) ||
		winVKDown(vkLWin) || winVKDown(vkRWin)
	text := ""
	if !chord {
		text = winVKToUnicode(vk, scan)
	}
	key, ok := engineKeyForVK(vk, text, chord)
	if !ok {
		return false
	}
	res := eng.HandleKey(key)
	if res.Commit != "" {
		sendWinUnicodeText(res.Commit)
	}
	if res.Changed {
		b.notifyInputEngineView()
	}
	if res.Consume {
		// Engine-owned keys never feed the snippet buffer (design §6 rule 4).
		b.emitTriggerEvents([]host.TriggerEvent{{Kind: host.TriggerSnippetReset, Key: "input_engine", TimestampMs: now.UnixMilli()}})
	}
	return res.Consume
}

// sendWinUnicodeText injects s as Unicode key events tagged with SyntheticEventTag, so the
// hook passes them straight through (no re-entry into the engine or snippets).
func sendWinUnicodeText(s string) {
	units := utf16Units(s)
	if len(units) == 0 {
		return
	}
	inputs := make([]winInput, 0, len(units)*2)
	for _, u := range units {
		inputs = append(inputs,
			winInput{Typ: inputKeyboard, Ki: keybdInput{WScan: u, DwFlags: keyeventfUnicodeFlag, DwExtraInfo: uintptr(SyntheticEventTag)}},
			winInput{Typ: inputKeyboard, Ki: keybdInput{WScan: u, DwFlags: keyeventfUnicodeFlag | keyeventfKeyUp, DwExtraInfo: uintptr(SyntheticEventTag)}},
		)
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
}
