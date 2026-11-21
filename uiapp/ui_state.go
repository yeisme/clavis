package uiapp

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"
)

// The Omnibox body splits into the result list and the preview pane at a
// divider the user drags (MyGo's SplitPercent: arrow keys move it once it
// has the focus, a double click puts it back). The percentage is the
// list's share; the limits keep both panes usable.
const (
	defaultPreviewListPercent float32 = 58 // the preview takes 42%
	minPreviewListPercent     float32 = 35 // the list keeps at least 35%
	maxPreviewListPercent     float32 = 75 // the preview keeps at least 25%
)

// clampPreviewListPercent bounds a split percentage; zero, NaN or anything
// unset falls back to the default.
func clampPreviewListPercent(p float32) float32 {
	if math.IsNaN(float64(p)) || p <= 0 {
		return defaultPreviewListPercent
	}
	return min(max(p, minPreviewListPercent), maxPreviewListPercent)
}

// uiStateFileName holds view state the user sets by hand (the preview
// split). It is not configuration: it lives beside config.toml in a small
// file the app owns, so dragging a divider never rewrites the user's config.
const uiStateFileName = "ui-state.json"

// uiStateSaveDelay is how long the divider must rest before the file is
// written, so a drag writes once instead of on every pointer move.
const uiStateSaveDelay = 500 * time.Millisecond

type persistedUIState struct {
	PreviewListPercent float32 `json:"previewListPercent,omitempty"`
}

type uiPersistState struct {
	loaded bool
	state  persistedUIState
	timer  *time.Timer
}

// uiStatePathLocked is ui-state.json in the config directory, or "" when
// the controller has no config store. c.mu must be held.
func (c *AppController) uiStatePathLocked() string {
	if c.ConfigStore == nil {
		return ""
	}
	dir := c.ConfigStore.Directory()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, uiStateFileName)
}

// previewListPercent is the list's share of the split, read from
// ui-state.json the first time it is asked for.
func (c *AppController) previewListPercent() float32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.uiPersist.loaded {
		c.uiPersist.loaded = true
		if path := c.uiStatePathLocked(); path != "" {
			if data, err := os.ReadFile(path); err == nil {
				var st persistedUIState
				if json.Unmarshal(data, &st) == nil {
					c.uiPersist.state = st
				}
			}
		}
	}
	return clampPreviewListPercent(c.uiPersist.state.PreviewListPercent)
}

// setPreviewListPercent records a move of the divider and schedules the
// write for when it has rested uiStateSaveDelay.
func (c *AppController) setPreviewListPercent(p float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.uiPersist.loaded = true
	c.uiPersist.state.PreviewListPercent = clampPreviewListPercent(p)
	if c.uiPersist.timer != nil {
		c.uiPersist.timer.Stop()
	}
	c.uiPersist.timer = time.AfterFunc(uiStateSaveDelay, c.saveUIState)
}

// saveUIState writes ui-state.json atomically (temp file and rename).
func (c *AppController) saveUIState() {
	c.mu.Lock()
	st, path := c.uiPersist.state, c.uiStatePathLocked()
	c.uiPersist.timer = nil
	c.mu.Unlock()
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// flushUIState writes a pending change now (on Close), so a divider moved
// just before quitting is not lost.
func (c *AppController) flushUIState() {
	c.mu.Lock()
	pending := c.uiPersist.timer != nil && c.uiPersist.timer.Stop()
	c.mu.Unlock()
	if pending {
		c.saveUIState()
	}
}
