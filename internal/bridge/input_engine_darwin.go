//go:build darwin

package bridge

import (
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// UpdateInputEngineLearning implements InputEngineLearningHost.
func (b *darwinBridge) UpdateInputEngineLearning(userFreq inputengine.UserFrequencyProvider, onCommit func(InputEngineCommit)) {
	b.ime.learnMu.Lock()
	b.ime.userFreq, b.ime.onCommit = userFreq, onCommit
	b.ime.learnMu.Unlock()
}

func (b *darwinBridge) UpdateInputEngineBlockedWords(words []string) { b.ime.setBlockedWords(words) }

// ConfigureInputEngine installs the pure-Go engine behind the existing CGEventTap. The tap
// itself checks secure input and the native Clavis IMK source before calling this handler.
func (b *darwinBridge) ConfigureInputEngine(cfg config.InputEngineConfig) {
	b.SetInlineIMEForced(cfg.ForceTakeover)
	b.configureInputEngine(cfg, false)
}

func (b *darwinBridge) ReloadInputEngine(cfg config.InputEngineConfig) {
	b.SetInlineIMEForced(cfg.ForceTakeover)
	b.configureInputEngine(cfg, true)
}

func (b *darwinBridge) configureInputEngine(cfg config.InputEngineConfig, force bool) {
	st := &b.ime
	changed, eng := st.configureWithForce(cfg, force)
	if !changed {
		return
	}
	if eng == nil {
		b.SetInlineIMEEnabled(false)
		st.setEngine(nil)
		b.notifyInputEngineView()
		return
	}
	st.setEngine(eng)
	b.SetInlineIMEHandler(b.inputEngineHandleInline)
	b.SetInlineIMEEnabled(true)
	st.startWorker()
	b.notifyInputEngineView()
}

func (b *darwinBridge) SetInputEngineViewHandler(fn func(InputEngineView)) {
	b.ime.viewMu.Lock()
	b.ime.viewFn = fn
	b.ime.viewMu.Unlock()
}

func (b *darwinBridge) SetInputEngineAICandidates(raw string, words []string) {
	if eng := b.ime.eng.Load(); eng != nil && eng.SetAICandidates(raw, words) {
		b.notifyInputEngineView()
	}
}

func (b *darwinBridge) resetInputEngine() {
	if b.ime.reset() {
		b.notifyInputEngineView()
	}
}

func (b *darwinBridge) notifyInputEngineView() { b.ime.notifyView() }

// emitDarwinCommit is deliberately synchronous: the tap must finish posting one commit before
// the next physical key is allowed through, otherwise a fast typer can reorder text.
func emitDarwinCommit(s string) {
	emitInputEngineCommit(s, postTaggedUnicode)
}

func (b *darwinBridge) inputEngineHandleInline(ev host.InlineKeyEvent) (consume bool) {
	eng := b.ime.eng.Load()
	if eng == nil || !ev.IsDown {
		return false
	}
	if b.isSelfFrontmost() {
		b.resetInputEngine()
		return false
	}
	defer func() {
		if recover() != nil {
			b.ime.commits.resetPrev()
			eng.Reset()
			consume = false
		}
	}()

	var res inputengine.Result
	keys, ok := inputEngineKeysForInline(ev)
	if !ok {
		return false
	}
	for _, key := range keys {
		res = b.ime.commits.handleKey(eng, key, b.ime.userLearning.Load())
	}
	if res.Commit != "" {
		emitDarwinCommit(res.Commit)
	}
	if res.Changed || b.ime.commits.hasPending() {
		b.notifyInputEngineView()
	}
	return res.Consume
}
