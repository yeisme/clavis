package uiapp

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/terminal"
)

const defaultFileSearchDebounce = 120 * time.Millisecond

func (c *AppController) cancelFileSearch(seq uint64) {
	c.fileSearchMu.Lock()
	if seq < c.fileSearchSeq.Load() {
		c.fileSearchMu.Unlock()
		return
	}
	if c.fileSearchTimer != nil {
		c.fileSearchTimer.Stop()
		c.fileSearchTimer = nil
	}
	if c.fileSearchCancel != nil {
		c.fileSearchCancel()
		c.fileSearchCancel = nil
	}
	c.fileSearchMu.Unlock()
}

func (c *AppController) scheduleFileSearch(seq uint64, query string, immediate bool) {
	c.fileSearchMu.Lock()
	if seq < c.fileSearchSeq.Load() {
		c.fileSearchMu.Unlock()
		return
	}
	if c.fileSearchTimer != nil {
		c.fileSearchTimer.Stop()
		c.fileSearchTimer = nil
	}
	if c.fileSearchCancel != nil {
		c.fileSearchCancel()
		c.fileSearchCancel = nil
	}
	c.fileSearchTimerSeq = seq
	debounce := c.fileSearchDebounce
	if debounce <= 0 {
		debounce = defaultFileSearchDebounce
	}
	if immediate {
		c.fileSearchMu.Unlock()
		c.runFileSearch(seq, query)
		return
	}
	c.fileSearchTimer = time.AfterFunc(debounce, func() {
		c.fileSearchMu.Lock()
		if c.fileSearchTimerSeq != seq || seq != c.fileSearchSeq.Load() || c.closed {
			c.fileSearchMu.Unlock()
			return
		}
		c.fileSearchTimer = nil
		c.fileSearchMu.Unlock()
		c.runFileSearch(seq, query)
	})
	c.fileSearchMu.Unlock()
}

func (c *AppController) runFileSearch(seq uint64, query string) {
	if seq != c.fileSearchSeq.Load() {
		return
	}
	baseCtx := c.fileBaseCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	searchCtx, cancel := context.WithCancel(baseCtx)
	c.fileSearchMu.Lock()
	if seq != c.fileSearchSeq.Load() || c.closed {
		c.fileSearchMu.Unlock()
		cancel()
		return
	}
	if c.fileSearchCancel != nil {
		c.fileSearchCancel()
	}
	c.fileSearchCancel = cancel
	searcher := c.FileSearcher
	c.fileSearchMu.Unlock()
	if searcher == nil {
		cancel()
		return
	}

	c.startBackground(func() {
		defer cancel()
		matches, err := searcher(searchCtx, query)
		if err != nil || searchCtx.Err() != nil || seq != c.fileSearchSeq.Load() {
			return
		}
		c.Dispatch(engine.Event{
			Type:        engine.EventSetFileSearchResults,
			Query:       query,
			FileMatches: matches,
		})
	})
}

func (c *AppController) openDirectoryInTerminal(dir string) {
	termCfg := config.DefaultTerminalConfig()
	if c != nil && c.ConfigStore != nil {
		termCfg = c.Config().Main.Terminal
	}
	dir = strings.TrimSpace(dir)
	if dir != "" && !filepath.IsAbs(dir) {
		dir = ""
	}
	agentTerminalResolverMu.RLock()
	factory := agentTerminalResolverFactory
	agentTerminalResolverMu.RUnlock()
	if factory == nil {
		factory = terminal.NewResolver
	}
	resolver := factory(termCfg)
	if resolver == nil {
		return
	}
	plan, _, err := resolver.Open(dir)
	if err != nil && plan.Profile.BundleID != "" && c != nil {
		c.mu.Lock()
		bridge := c.Bridge
		c.mu.Unlock()
		if bridge != nil {
			_ = bridge.OpenApp(c.lifeCtx(), plan.Profile.BundleID)
		}
	}
}
