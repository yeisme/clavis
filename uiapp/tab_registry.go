package uiapp

import (
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
	HubSurfaceMedia  CapsuleHubSurface = "media"
	HubSurfaceAssets CapsuleHubSurface = HubSurfaceMedia
)

// MediaFilterEntry registers one keyboard-first media filter chip in the
// Clipboard / Media search bar (⌘1–⌘7).
type MediaFilterEntry struct {
	Category      MediaCategory
	Key           ui.Key
	ShortcutDigit string
	Icon          string
	LabelFunc     func() string
}

var registeredMediaFilterEntries = []MediaFilterEntry{
	{Category: MediaCategoryAll, Key: ui.Key1, ShortcutDigit: "1", Icon: "square.3.layers.3d", LabelFunc: func() string { return MediaCategoryLabel(MediaCategoryAll) }},
	{Category: MediaCategoryImage, Key: ui.Key2, ShortcutDigit: "2", Icon: "photo", LabelFunc: func() string { return MediaCategoryLabel(MediaCategoryImage) }},
	{Category: MediaCategoryVideo, Key: ui.Key3, ShortcutDigit: "3", Icon: "film", LabelFunc: func() string { return MediaCategoryLabel(MediaCategoryVideo) }},
	{Category: MediaCategoryAudio, Key: ui.Key4, ShortcutDigit: "4", Icon: "clock", LabelFunc: func() string { return MediaCategoryLabel(MediaCategoryAudio) }},
	{Category: MediaCategoryDocument, Key: ui.Key5, ShortcutDigit: "5", Icon: "doc.text", LabelFunc: func() string { return MediaCategoryLabel(MediaCategoryDocument) }},
	{Category: MediaCategoryArchive, Key: ui.Key6, ShortcutDigit: "6", Icon: "doc.on.doc", LabelFunc: func() string { return MediaCategoryLabel(MediaCategoryArchive) }},
	{Category: MediaCategoryFile, Key: ui.Key7, ShortcutDigit: "7", Icon: "folder", LabelFunc: func() string { return MediaCategoryLabel(MediaCategoryFile) }},
}

// RegisteredMediaFilters returns the ordered filter registry for the six media
// categories plus "all".
func RegisteredMediaFilters() []MediaFilterEntry {
	return append([]MediaFilterEntry(nil), registeredMediaFilterEntries...)
}

// MediaFilterByKey resolves a ⌘1–⌘7 key press to its registered MediaCategory.
func MediaFilterByKey(key ui.Key) (MediaCategory, bool) {
	for _, entry := range registeredMediaFilterEntries {
		if entry.Key == key {
			return entry.Category, true
		}
	}
	return MediaCategoryAll, false
}

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
	{
		Surface:  HubSurfaceAssets,
		Key:      ui.Key9,
		KeyDigit: "9",
		Icon:     "photo",
		LabelFunc: func() string {
			if i18n.IsChinese() {
				return "资产"
			}
			return "Assets"
		},
	},
}

// ActiveHubSurface reports which operational hub (Stats / Management) is
// currently shown in the Capsule Omnibox, or HubSurfaceNone for the normal list.
func (a *AppController) ActiveHubSurface() CapsuleHubSurface {
	return a.hubSurfaceState()
}

// resetHubSurfaceSilent clears the active hub surface without invoking OnRepaint.
func (a *AppController) resetHubSurfaceSilent() {
	a.setHubSurfaceState(HubSurfaceNone)
}

// SetHubSurface activates or clears an operational hub inside the Capsule.
func (a *AppController) SetHubSurface(surface CapsuleHubSurface) {
	a.setHubSurfaceState(surface)
	if a == nil {
		return
	}
	a.mu.Lock()
	repaint := a.OnRepaint
	a.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

// ToggleHubSurface toggles the specified operational hub on or off.
func (a *AppController) ToggleHubSurface(surface CapsuleHubSurface) CapsuleHubSurface {
	next := a.toggleHubSurfaceState(surface)
	if a == nil {
		return next
	}
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
	case HubSurfaceAssets:
		a.retryClipboardStats()
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
