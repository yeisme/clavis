package uiapp

import (
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// CapsuleHubSurface identifies a dedicated operational hub opened inside the
// Capsule Omnibox without disturbing the 6 core search/workbench tabs (⌘1–⌘6)
// or breaking Cmd+Left/Right tab cycling.
type CapsuleHubSurface string

const (
	HubSurfaceNone   CapsuleHubSurface = ""
	HubSurfaceStats  CapsuleHubSurface = "stats"
	HubSurfaceManage CapsuleHubSurface = "manage"
)

// capsuleHubEntry describes a registered operational hub surface.
type capsuleHubEntry struct {
	Surface   CapsuleHubSurface
	Key       ui.Key
	KeyDigit  string
	Icon      string
	LabelFunc func() string
}

var capsuleHubEntries = []capsuleHubEntry{
	{
		Surface:  HubSurfaceStats,
		Key:      ui.Key7,
		KeyDigit: "7",
		Icon:     "square.3.layers.3d",
		LabelFunc: func() string {
			if i18n.IsChinese() {
				return "统计"
			}
			return "Stats"
		},
	},
	{
		Surface:  HubSurfaceManage,
		Key:      ui.Key8,
		KeyDigit: "8",
		Icon:     "gearshape",
		LabelFunc: func() string {
			if i18n.IsChinese() {
				return "管理"
			}
			return "Manage"
		},
	},
}

var hubSurfaceStore = struct {
	mu     sync.Mutex
	active map[*AppController]CapsuleHubSurface
}{
	active: make(map[*AppController]CapsuleHubSurface),
}

// ActiveHubSurface reports which operational hub (Stats / Management) is
// currently shown in the Capsule Omnibox, or HubSurfaceNone for the normal list.
func (a *AppController) ActiveHubSurface() CapsuleHubSurface {
	hubSurfaceStore.mu.Lock()
	defer hubSurfaceStore.mu.Unlock()
	return hubSurfaceStore.active[a]
}

// SetHubSurface activates or clears an operational hub inside the Capsule.
func (a *AppController) SetHubSurface(surface CapsuleHubSurface) {
	hubSurfaceStore.mu.Lock()
	if surface == HubSurfaceNone {
		delete(hubSurfaceStore.active, a)
	} else {
		hubSurfaceStore.active[a] = surface
	}
	hubSurfaceStore.mu.Unlock()
	a.mu.Lock()
	repaint := a.OnRepaint
	a.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

// ToggleHubSurface toggles the specified operational hub on or off.
func (a *AppController) ToggleHubSurface(surface CapsuleHubSurface) CapsuleHubSurface {
	hubSurfaceStore.mu.Lock()
	next := surface
	if hubSurfaceStore.active[a] == surface {
		next = HubSurfaceNone
		delete(hubSurfaceStore.active, a)
	} else {
		hubSurfaceStore.active[a] = surface
	}
	hubSurfaceStore.mu.Unlock()
	a.mu.Lock()
	repaint := a.OnRepaint
	a.mu.Unlock()
	if repaint != nil {
		repaint()
	}
	return next
}

// OpenStatsView opens the Capsule directly on the aggregated Stats hub.
func (a *AppController) OpenStatsView() {
	if a.State().Phase != engine.PhaseOmnibox {
		a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	}
	a.SetHubSurface(HubSurfaceStats)
}

// OpenManagementView opens the Capsule directly on the operational Management hub.
func (a *AppController) OpenManagementView() {
	if a.State().Phase != engine.PhaseOmnibox {
		a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	}
	a.SetHubSurface(HubSurfaceManage)
}

// CloseHubSurface returns to the normal Omnibox tab content.
func (a *AppController) CloseHubSurface() {
	a.SetHubSurface(HubSurfaceNone)
}

func registeredHubEntries() []capsuleHubEntry {
	return capsuleHubEntries
}

func (a *AppController) handleHubShortcutKey(key ui.Key, mods ui.Modifiers) bool {
	if !isPrimaryMod(mods) {
		return false
	}
	for _, entry := range capsuleHubEntries {
		if key == entry.Key {
			capsuleUI.setActionMenuShown(a, false)
			a.ToggleHubSurface(entry.Surface)
			return true
		}
	}
	if key == ui.KeyR && a.ActiveHubSurface() != HubSurfaceNone {
		a.refreshActiveHubSources()
		return true
	}
	return false
}

func (a *AppController) refreshActiveHubSources() {
	switch a.ActiveHubSurface() {
	case HubSurfaceStats:
		a.retryAIUsage()
		a.retryClipboardStats()
		a.retryLexiconHits()
		a.retryLexiconTopWords()
		a.AgentRefresh()
	case HubSurfaceManage:
		a.retryManageSyncStatus()
		a.retryClipboardStats()
		a.retryLexiconHits()
		a.AgentRefresh()
	}
}

func (a *AppController) retryLexiconTopWords() {
	a.lexiconTopWordsMu.Lock()
	a.lexiconTopWordsLoaded = false
	a.lexiconTopWordsErr = nil
	a.lexiconTopWordsMu.Unlock()
	a.ensureLexiconTopWordsRefresh()
}

func (a *AppController) renderOmniboxHubToggles(c *ui.Context, theme *ui.Theme) {
	prefix := modPrefix()
	active := a.ActiveHubSurface()
	for _, entry := range capsuleHubEntries {
		e := entry
		lbl := prefix + e.KeyDigit + " " + e.LabelFunc()
		a.renderOmniboxFooterFrameToggle(c, theme, e.Icon, lbl, active == e.Surface, func() {
			capsuleUI.setActionMenuShown(a, false)
			a.ToggleHubSurface(e.Surface)
		})
	}
}
