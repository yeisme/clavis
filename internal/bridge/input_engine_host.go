package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// inputEngineState contains the platform-independent own-IME state. The hook only touches
// the engine and commit tracker; view rendering, anchor lookup, and learning run off-hook.
type inputEngineState struct {
	eng          atomic.Pointer[inputengine.Engine]
	viewMu       sync.Mutex
	viewFn       func(InputEngineView)
	learnMu      sync.RWMutex
	userFreq     inputengine.UserFrequencyProvider
	blockedWords []string
	onCommit     func(InputEngineCommit)
	userLearning atomic.Bool
	commits      inputEngineCommitTracker
	dirty        chan struct{}
	anchor       atomic.Pointer[host.CursorAnchor]
	started      atomic.Bool
	cacheDir     string
	anchorFn     func(context.Context) (host.CursorAnchor, bool)

	cfgMu      sync.Mutex
	lastCfg    config.InputEngineConfig
	cfgApplied bool
}

func (st *inputEngineState) buildEngine(cfg config.InputEngineConfig) *inputengine.Engine {
	st.learnMu.RLock()
	uf := st.userFreq
	blocked := append([]string(nil), st.blockedWords...)
	st.learnMu.RUnlock()
	st.userLearning.Store(cfg.UserLearning)
	eng := newInputEngineFromConfig(cfg, st.cacheDir, uf)
	if eng != nil {
		eng.SetBlockedWords(blocked)
	}
	return eng
}

func (st *inputEngineState) setBlockedWords(words []string) {
	st.learnMu.Lock()
	st.blockedWords = append(st.blockedWords[:0], words...)
	eng := st.eng.Load()
	st.learnMu.Unlock()
	if eng != nil {
		eng.SetBlockedWords(words)
		st.notifyView()
	}
}

func (st *inputEngineState) notifyView() {
	select {
	case st.dirty <- struct{}{}:
	default:
	}
}

func (st *inputEngineState) runViewWorker() {
	var commitBuf []InputEngineCommit
	for range st.dirty {
		commitBuf = st.commits.drain(commitBuf[:0])
		if len(commitBuf) > 0 {
			st.learnMu.RLock()
			onCommit := st.onCommit
			st.learnMu.RUnlock()
			if onCommit != nil {
				for _, c := range commitBuf {
					onCommit(c)
				}
			}
		}
		eng := st.eng.Load()
		var v InputEngineView
		if eng != nil {
			v.View, v.Raw = eng.View(), eng.Raw()
		}
		if v.View.Composing {
			if st.anchor.Load() == nil && st.anchorFn != nil {
				if a, ok := st.anchorFn(context.Background()); ok {
					st.anchor.Store(&a)
				}
			}
			v.Anchor = st.anchor.Load()
		} else {
			st.anchor.Store(nil)
		}
		st.viewMu.Lock()
		fn := st.viewFn
		st.viewMu.Unlock()
		if fn != nil {
			fn(v)
		}
	}
}

func (st *inputEngineState) reset() bool {
	if eng := st.eng.Load(); eng != nil {
		st.commits.resetPrev()
		return eng.Reset()
	}
	return false
}

func (st *inputEngineState) configure(cfg config.InputEngineConfig) (changed bool, eng *inputengine.Engine) {
	return st.configureWithForce(cfg, false)
}

func (st *inputEngineState) configureWithForce(cfg config.InputEngineConfig, force bool) (changed bool, eng *inputengine.Engine) {
	st.cfgMu.Lock()
	if !force && st.cfgApplied && inputEngineConfigEqual(st.lastCfg, cfg) {
		st.cfgMu.Unlock()
		return false, st.eng.Load()
	}
	st.lastCfg, st.cfgApplied = cfg, true
	st.cfgMu.Unlock()
	st.commits.resetPrev()
	eng = st.buildEngine(cfg)
	return true, eng
}

func (st *inputEngineState) setEngine(eng *inputengine.Engine) bool {
	if old := st.eng.Swap(eng); old != nil {
		old.Reset()
		return true
	}
	return false
}

func (st *inputEngineState) startWorker() bool {
	if st.started.CompareAndSwap(false, true) {
		go st.runViewWorker()
		return true
	}
	return false
}
