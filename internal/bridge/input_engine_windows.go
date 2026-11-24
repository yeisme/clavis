//go:build windows

package bridge

import (
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

// UpdateInputEngineLearning implements InputEngineLearningHost.
func (b *windowsBridge) UpdateInputEngineLearning(userFreq inputengine.UserFrequencyProvider, onCommit func(InputEngineCommit)) {
	b.ime.learnMu.Lock()
	b.ime.userFreq = userFreq
	b.ime.onCommit = onCommit
	b.ime.learnMu.Unlock()
}

func (b *windowsBridge) UpdateInputEngineBlockedWords(words []string) { b.ime.setBlockedWords(words) }

// ConfigureInputEngine implements InputEngineHost.
func (b *windowsBridge) ConfigureInputEngine(cfg config.InputEngineConfig) {
	b.SetInlineIMEForced(cfg.ForceTakeover)
	b.configureInputEngine(cfg, false)
}

func (b *windowsBridge) ReloadInputEngine(cfg config.InputEngineConfig) {
	b.SetInlineIMEForced(cfg.ForceTakeover)
	b.configureInputEngine(cfg, true)
}

func (b *windowsBridge) configureInputEngine(cfg config.InputEngineConfig, force bool) {
	st := &b.ime
	changed, eng := st.configureWithForce(cfg, force)
	if !changed {
		return
	}
	if eng == nil {
		if old := st.eng.Swap(nil); old != nil {
			old.Reset()
			b.notifyInputEngineView()
		}
		return
	}
	st.setEngine(eng)
	st.startWorker()
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
	b.shiftTracker.Reset()
	if b.ime.reset() {
		b.notifyInputEngineView()
	}
}
func (b *windowsBridge) notifyInputEngineView() { b.ime.notifyView() }

func (b *windowsBridge) inputEngineEligibleAt(now time.Time, app AppIdentity) bool {
	return winInputEngineEligible(
		b.inlineForced.Load(),
		b.hookIMEComposing(now),
		b.hookSecureInput(now),
		isSelfClavisApp(app),
	)
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
			b.shiftTracker.Reset()
			b.ime.commits.resetPrev()
			eng.Reset()
			consume = false
		}
	}()

	isShift := isWinShiftVK(vk)
	if !isShift {
		if _, isMod := winVKToModifier(vk); isMod {
			b.shiftTracker.OnNonShiftModifier()
			return false
		}
		if !isDown {
			return false
		}
		b.shiftTracker.OnInterveningKey()
	}

	app := AppIdentity{ProcessName: b.cachedFrontmostProcessName()}
	if !b.inputEngineEligibleAt(now, app) {
		b.shiftTracker.Reset()
		b.ime.commits.resetPrev()
		if eng.Reset() {
			b.notifyInputEngineView()
		}
		return false
	}

	if isShift {
		otherDown := winOtherModifiersDown(vk)
		if isDown {
			b.shiftTracker.OnShiftDown(vk, otherDown)
			return false
		}
		if !b.shiftTracker.OnShiftUp(vk, otherDown) {
			return false
		}
		b.ime.commits.handleKey(eng, inputengine.Key{Kind: inputengine.KeyShiftDown}, b.ime.userLearning.Load())
		if res := b.ime.commits.handleKey(eng, inputengine.Key{Kind: inputengine.KeyShiftUp}, b.ime.userLearning.Load()); res.Changed || res.Commit != "" {
			if res.Commit != "" {
				sendWinUnicodeText(res.Commit)
			}
			b.notifyInputEngineView()
		}
		return false // Shift always reaches the router (leader / double-tap unaffected)
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
	switch vk {
	case vkUp:
		key.NavDir = inputengine.NavUp
	case vkDown:
		key.NavDir = inputengine.NavDown
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
