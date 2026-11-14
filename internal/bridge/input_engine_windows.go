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
	eng          atomic.Pointer[inputengine.Engine]
	viewMu       sync.Mutex
	viewFn       func(InputEngineView)
	learnMu      sync.RWMutex
	userFreq     inputengine.UserFrequencyProvider
	onCommit     func(InputEngineCommit)
	userLearning atomic.Bool
	commits      inputEngineCommitTracker
	dirty        chan struct{}
	anchor       atomic.Pointer[host.CursorAnchor]
	started      atomic.Bool
	cacheDir     string

	// cfgMu guards lastCfg/cfgApplied: the [input_engine] config last built into
	// eng. Rebuilding is expensive (dictionary load, possibly a Rime YAML
	// compile) and drops any in-flight composition, so a reload that leaves the
	// section untouched must be a no-op.
	cfgMu      sync.Mutex
	lastCfg    config.InputEngineConfig
	cfgApplied bool
}

func (st *inputEngineState) buildEngine(cfg config.InputEngineConfig) *inputengine.Engine {
	st.learnMu.RLock()
	uf := st.userFreq
	st.learnMu.RUnlock()
	st.userLearning.Store(cfg.UserLearning)
	return newInputEngineFromConfig(cfg, st.cacheDir, uf)
}

// UpdateInputEngineLearning implements InputEngineLearningHost.
func (b *windowsBridge) UpdateInputEngineLearning(userFreq inputengine.UserFrequencyProvider, onCommit func(InputEngineCommit)) {
	b.ime.learnMu.Lock()
	b.ime.userFreq = userFreq
	b.ime.onCommit = onCommit
	b.ime.learnMu.Unlock()
}

// ConfigureInputEngine implements InputEngineHost.
func (b *windowsBridge) ConfigureInputEngine(cfg config.InputEngineConfig) {
	st := &b.ime
	// A reload that does not change [input_engine] must not rebuild the engine:
	// the build loads the lexicon (and may compile a Rime dictionary) and
	// Reset() would discard whatever the user is currently composing.
	st.cfgMu.Lock()
	if st.cfgApplied && inputEngineConfigEqual(st.lastCfg, cfg) {
		st.cfgMu.Unlock()
		return
	}
	st.lastCfg, st.cfgApplied = cfg, true
	st.cfgMu.Unlock()

	eng := st.buildEngine(cfg)
	st.commits.resetPrev()
	if eng == nil {
		if old := st.eng.Swap(nil); old != nil {
			old.Reset()
			b.notifyInputEngineView()
		}
		return
	}
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
	if eng := b.ime.eng.Load(); eng != nil {
		b.ime.commits.resetPrev()
		if eng.Reset() {
			b.notifyInputEngineView()
		}
	}
}

// notifyInputEngineView is non-blocking (safe on the hook thread).
func (b *windowsBridge) notifyInputEngineView() {
	select {
	case b.ime.dirty <- struct{}{}:
	default:
	}
}

// runInputEngineViewWorker coalesces view updates, resolves the caret anchor once per
// composition, and persists queued IME commits off the keyboard hook thread.
func (b *windowsBridge) runInputEngineViewWorker() {
	var commitBuf []InputEngineCommit
	for range b.ime.dirty {
		commitBuf = b.ime.commits.drain(commitBuf[:0])
		if len(commitBuf) > 0 {
			b.ime.learnMu.RLock()
			onCommit := b.ime.onCommit
			b.ime.learnMu.RUnlock()
			if onCommit != nil {
				for _, c := range commitBuf {
					onCommit(c)
				}
			}
		}
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
			b.ime.commits.resetPrev()
			eng.Reset()
			consume = false
		}
	}()

	if vk == vkShift || vk == vkLShift || vk == vkRShift {
		kind := inputengine.KeyShiftUp
		if isDown {
			kind = inputengine.KeyShiftDown
		}
		if res := b.ime.commits.handleKey(eng, inputengine.Key{Kind: kind}, b.ime.userLearning.Load()); res.Changed || res.Commit != "" {
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
		b.ime.commits.resetPrev()
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
	res := b.ime.commits.handleKey(eng, key, b.ime.userLearning.Load())
	if res.Commit != "" {
		sendWinUnicodeText(res.Commit)
	}
	if res.Changed || b.ime.commits.hasPending() {
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
