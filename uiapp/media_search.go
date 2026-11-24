package uiapp

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// MediaProbeStatus tracks the lifecycle of an asynchronous file inspection.
type MediaProbeStatus uint8

const (
	MediaProbeIdle MediaProbeStatus = iota
	MediaProbeLoading
	MediaProbeReady
	MediaProbeUnavailable
)

// MediaProbeSnapshot holds real, non-fabricated file inspection facts cached
// asynchronously off the UI render thread.
type MediaProbeSnapshot struct {
	Path              string
	Category          MediaCategory
	Status            MediaProbeStatus
	Available         bool
	UnavailableReason string
	ByteSize          int64
	ModTime           time.Time
	IsDir             bool
	Extension         string
	Mime              string
	Width             int
	Height            int
	DurationMs        int64
	// Real UTF-8 excerpt for small readable documents (secret-guarded).
	TextExcerptLines []string
	ExcerptRedacted  bool
	// Real archive member summary (e.g. ZIP directory listing, never extracted).
	ArchiveEntryCount int
	ArchiveTotalBytes int64
	ArchiveEntries    []string
	// Real directory children when IsDir is true.
	DirEntries []string
	ProbedAt   time.Time
}

type mediaSearchControllerState struct {
	mu          sync.Mutex
	query       string
	category    MediaCategory
	selectedIdx int
	isComposing bool
	statusMsg   string
	customItems []engine.OmniboxItem
	probes      map[string]MediaProbeSnapshot
	inFlight    map[string]bool
}

var mediaSearchStore = struct {
	mu    sync.Mutex
	byCtl map[*AppController]*mediaSearchControllerState
}{
	byCtl: make(map[*AppController]*mediaSearchControllerState),
}

func (a *AppController) mediaSearchState() *mediaSearchControllerState {
	mediaSearchStore.mu.Lock()
	defer mediaSearchStore.mu.Unlock()
	st, ok := mediaSearchStore.byCtl[a]
	if !ok {
		st = &mediaSearchControllerState{
			category: MediaCategoryAll,
			probes:   make(map[string]MediaProbeSnapshot),
			inFlight: make(map[string]bool),
		}
		mediaSearchStore.byCtl[a] = st
	}
	return st
}

// SetMediaSearchItems injects a media item slice for MediaSearchView (used by
// dedicated media hub / tests; when empty, falls back to State.OmniboxItems).
func (a *AppController) SetMediaSearchItems(items []engine.OmniboxItem) {
	st := a.mediaSearchState()
	st.mu.Lock()
	st.customItems = append([]engine.OmniboxItem(nil), items...)
	st.selectedIdx = 0
	st.mu.Unlock()
}

// MediaSearchCategory returns the currently selected media filter category.
func (a *AppController) MediaSearchCategory() MediaCategory {
	st := a.mediaSearchState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.category == "" {
		return MediaCategoryAll
	}
	return st.category
}

// SetMediaSearchCategory selects the active media filter and resets selection.
func (a *AppController) SetMediaSearchCategory(cat MediaCategory) {
	if cat == "" {
		cat = MediaCategoryAll
	}
	st := a.mediaSearchState()
	st.mu.Lock()
	st.category = cat
	st.selectedIdx = 0
	st.mu.Unlock()
}

// MediaSearchQuery returns the active media search query.
func (a *AppController) MediaSearchQuery() string {
	st := a.mediaSearchState()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.query
}

// SetMediaSearchQuery updates the active media search query.
func (a *AppController) SetMediaSearchQuery(q string) {
	st := a.mediaSearchState()
	st.mu.Lock()
	st.query = q
	st.selectedIdx = 0
	st.mu.Unlock()
}

// MediaSearchSelectedIndex returns the current keyboard selection index.
func (a *AppController) MediaSearchSelectedIndex() int {
	st := a.mediaSearchState()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.selectedIdx
}

// SetMediaSearchSelectedIndex clamps and sets the current keyboard selection.
func (a *AppController) SetMediaSearchSelectedIndex(idx, total int) {
	st := a.mediaSearchState()
	st.mu.Lock()
	if total <= 0 {
		st.selectedIdx = 0
	} else {
		if idx < 0 {
			idx = 0
		}
		if idx >= total {
			idx = total - 1
		}
		st.selectedIdx = idx
	}
	st.mu.Unlock()
}

// MediaSearchItems returns all media items currently visible to the media
// search browser (custom items if explicitly seeded, else media entries from
// State.OmniboxItems).
func (a *AppController) MediaSearchItems() []engine.OmniboxItem {
	st := a.mediaSearchState()
	st.mu.Lock()
	if len(st.customItems) > 0 {
		out := append([]engine.OmniboxItem(nil), st.customItems...)
		st.mu.Unlock()
		return out
	}
	st.mu.Unlock()

	state := a.State()
	var out []engine.OmniboxItem
	for _, it := range state.OmniboxItems {
		if it.ClipboardMedia != nil && (it.ClipboardMedia.Path != "" || len(it.ClipboardMedia.FilePaths) > 0 || it.ClipboardMedia.Kind != "") {
			out = append(out, it)
		}
	}
	return out
}

// FilterMediaItems filters media items by category and free-text query across
// title, path, filename, extension, MIME, source app, and category label.
func FilterMediaItems(items []engine.OmniboxItem, category MediaCategory, query string) []engine.OmniboxItem {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]engine.OmniboxItem, 0, len(items))
	for _, it := range items {
		if it.ClipboardMedia == nil {
			continue
		}
		cat := ClassifyClipboardMedia(*it.ClipboardMedia)
		if category != "" && category != MediaCategoryAll && cat != category {
			continue
		}
		if q != "" {
			path := mediaPrimaryPath(*it.ClipboardMedia)
			hay := strings.ToLower(strings.Join([]string{
				it.Title,
				it.Subtitle,
				it.ClipboardSourceApp,
				path,
				filepath.Base(path),
				mediaPathExt(path),
				it.ClipboardMedia.Mime,
				string(cat),
				MediaCategoryLabel(cat),
			}, " "))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

// MediaCategoryCounts computes item counts per category for the filter strip.
func MediaCategoryCounts(items []engine.OmniboxItem) map[MediaCategory]int {
	counts := map[MediaCategory]int{
		MediaCategoryAll:      0,
		MediaCategoryImage:    0,
		MediaCategoryVideo:    0,
		MediaCategoryAudio:    0,
		MediaCategoryDocument: 0,
		MediaCategoryArchive:  0,
		MediaCategoryFile:     0,
	}
	for _, it := range items {
		if it.ClipboardMedia == nil {
			continue
		}
		cat := ClassifyClipboardMedia(*it.ClipboardMedia)
		counts[MediaCategoryAll]++
		counts[cat]++
	}
	return counts
}

// MediaProbe returns the cached probe snapshot for m without blocking the UI
// thread, scheduling an asynchronous background probe on cache miss.
func (a *AppController) MediaProbe(m engine.ClipboardMedia) MediaProbeSnapshot {
	path := mediaPrimaryPath(m)
	cat := ClassifyClipboardMedia(m)
	if path == "" {
		return MediaProbeSnapshot{
			Category:          cat,
			Status:            MediaProbeUnavailable,
			Available:         false,
			UnavailableReason: pickLang(i18n.IsChinese(), "缺少文件路径", "missing file path"),
			ByteSize:          m.ByteSize,
			Mime:              m.Mime,
			Width:             m.Width,
			Height:            m.Height,
			DurationMs:        m.DurationMs,
		}
	}

	st := a.mediaSearchState()
	st.mu.Lock()
	if snap, ok := st.probes[path]; ok {
		st.mu.Unlock()
		return snap
	}
	if !st.inFlight[path] {
		st.inFlight[path] = true
		go a.runMediaProbeAsync(path, m)
	}
	st.mu.Unlock()

	return MediaProbeSnapshot{
		Path:       path,
		Category:   cat,
		Status:     MediaProbeLoading,
		Extension:  mediaPathExt(path),
		Mime:       m.Mime,
		ByteSize:   m.ByteSize,
		Width:      m.Width,
		Height:     m.Height,
		DurationMs: m.DurationMs,
	}
}

// RetryMediaProbe clears the cached probe for path and triggers a fresh async
// filesystem check (⌘R or "Re-check").
func (a *AppController) RetryMediaProbe(m engine.ClipboardMedia) {
	path := mediaPrimaryPath(m)
	if path == "" {
		return
	}
	st := a.mediaSearchState()
	st.mu.Lock()
	delete(st.probes, path)
	if !st.inFlight[path] {
		st.inFlight[path] = true
		go a.runMediaProbeAsync(path, m)
	}
	st.mu.Unlock()
}

func (a *AppController) runMediaProbeAsync(path string, m engine.ClipboardMedia) {
	snap := probeMediaFile(path, m)
	st := a.mediaSearchState()
	st.mu.Lock()
	if len(st.probes) >= 128 {
		clear(st.probes)
	}
	st.probes[path] = snap
	delete(st.inFlight, path)
	st.mu.Unlock()

	a.mu.Lock()
	repaint := a.OnRepaint
	a.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

// probeMediaFile inspects real on-disk metadata without ever fabricating media
// content or exposing secret-shaped text.
func probeMediaFile(path string, m engine.ClipboardMedia) MediaProbeSnapshot {
	cat := ClassifyClipboardMedia(m)
	snap := MediaProbeSnapshot{
		Path:       path,
		Category:   cat,
		Extension:  mediaPathExt(path),
		Mime:       strings.TrimSpace(m.Mime),
		ByteSize:   m.ByteSize,
		Width:      m.Width,
		Height:     m.Height,
		DurationMs: m.DurationMs,
		ProbedAt:   time.Now(),
	}

	info, err := os.Stat(path)
	if err != nil {
		snap.Status = MediaProbeUnavailable
		snap.Available = false
		if os.IsNotExist(err) {
			snap.UnavailableReason = pickLang(i18n.IsChinese(), "原文件已移动或删除", "source file missing or moved")
		} else if os.IsPermission(err) {
			snap.UnavailableReason = pickLang(i18n.IsChinese(), "无权访问该文件", "permission denied")
		} else {
			snap.UnavailableReason = err.Error()
		}
		return snap
	}

	snap.Status = MediaProbeReady
	snap.Available = true
	snap.ModTime = info.ModTime()
	snap.IsDir = info.IsDir()
	if !info.IsDir() && info.Size() >= 0 {
		snap.ByteSize = info.Size()
	}

	if info.IsDir() {
		if entries, err := os.ReadDir(path); err == nil {
			limit := len(entries)
			if limit > 8 {
				limit = 8
			}
			for i := 0; i < limit; i++ {
				name := entries[i].Name()
				if entries[i].IsDir() {
					name += "/"
				}
				snap.DirEntries = append(snap.DirEntries, name)
			}
		}
		return snap
	}

	switch cat {
	case MediaCategoryImage:
		if info.Size() <= clipboard.MaxImageBytes {
			if data, err := os.ReadFile(path); err == nil {
				if imgCfg, format, err := clipboard.CheckImage(data); err == nil {
					if snap.Width == 0 {
						snap.Width = imgCfg.Width
					}
					if snap.Height == 0 {
						snap.Height = imgCfg.Height
					}
					if snap.Mime == "" && format != "" {
						snap.Mime = "image/" + format
					}
				}
			}
		}
	case MediaCategoryArchive:
		if strings.EqualFold(snap.Extension, ".zip") {
			if zr, err := zip.OpenReader(path); err == nil {
				defer zr.Close()
				snap.ArchiveEntryCount = len(zr.File)
				for i, f := range zr.File {
					snap.ArchiveTotalBytes += int64(f.UncompressedSize64)
					if i < 6 {
						snap.ArchiveEntries = append(snap.ArchiveEntries, f.Name)
					}
				}
			}
		}
	case MediaCategoryDocument:
		if isReadableTextDocExt(snap.Extension, snap.Mime) && info.Size() <= 256<<10 {
			if f, err := os.Open(path); err == nil {
				defer f.Close()
				buf, _ := io.ReadAll(io.LimitReader(f, 1536))
				if len(buf) > 0 && utf8.Valid(buf) {
					raw := strings.ReplaceAll(string(buf), "\r\n", "\n")
					if IsPasswordOrSecretContent(raw) {
						snap.ExcerptRedacted = true
					} else {
						lines := strings.Split(raw, "\n")
						if len(lines) > 6 {
							lines = lines[:6]
						}
						for _, ln := range lines {
							snap.TextExcerptLines = append(snap.TextExcerptLines, strings.TrimRight(ln, " \t"))
						}
					}
				}
			}
		}
	}

	return snap
}

func isReadableTextDocExt(ext, mime string) bool {
	if strings.HasPrefix(strings.ToLower(mime), "text/") || mime == "application/json" || mime == "application/xml" {
		return true
	}
	switch strings.ToLower(ext) {
	case ".md", ".markdown", ".txt", ".csv", ".tsv", ".json", ".yaml", ".yml", ".toml", ".xml", ".html", ".htm", ".log":
		return true
	}
	return false
}

// HandleMediaSearchKey implements keyboard-first navigation, category filter
// switching, re-probe, copy path, paste, and reset while respecting IME
// composition priority.
func (a *AppController) HandleMediaSearchKey(key ui.Key, mods ui.Modifiers, visible []engine.OmniboxItem) bool {
	st := a.mediaSearchState()
	st.mu.Lock()
	composing := st.isComposing
	curIdx := st.selectedIdx
	curCat := st.category
	curQuery := st.query
	st.mu.Unlock()

	if composing && imeOwnsKey(key, mods) {
		return false
	}

	// ⌘1..⌘7 jumps directly to a registered media filter category.
	if isPrimaryMod(mods) {
		if cat, ok := MediaFilterByKey(key); ok {
			a.SetMediaSearchCategory(cat)
			return true
		}
		switch key {
		case ui.KeyR:
			if curIdx >= 0 && curIdx < len(visible) && visible[curIdx].ClipboardMedia != nil {
				a.RetryMediaProbe(*visible[curIdx].ClipboardMedia)
				return true
			}
		case ui.KeyC:
			if curIdx >= 0 && curIdx < len(visible) && visible[curIdx].ClipboardMedia != nil {
				a.copySelectedMediaItem(visible[curIdx])
				return true
			}
		case ui.KeyLeft:
			a.cycleMediaSearchCategory(curCat, -1)
			return true
		case ui.KeyRight:
			a.cycleMediaSearchCategory(curCat, 1)
			return true
		}
	}

	switch key {
	case ui.KeyUp:
		if len(visible) > 0 {
			next := curIdx - 1
			if next < 0 {
				next = len(visible) - 1
			}
			a.SetMediaSearchSelectedIndex(next, len(visible))
			return true
		}
	case ui.KeyDown:
		if len(visible) > 0 {
			next := (curIdx + 1) % len(visible)
			a.SetMediaSearchSelectedIndex(next, len(visible))
			return true
		}
	case ui.KeyP:
		if mods == ui.Ctrl && len(visible) > 0 {
			next := curIdx - 1
			if next < 0 {
				next = len(visible) - 1
			}
			a.SetMediaSearchSelectedIndex(next, len(visible))
			return true
		}
	case ui.KeyN:
		if mods == ui.Ctrl && len(visible) > 0 {
			next := (curIdx + 1) % len(visible)
			a.SetMediaSearchSelectedIndex(next, len(visible))
			return true
		}
	case ui.KeyTab:
		if mods&ui.Shift != 0 {
			a.cycleMediaSearchCategory(curCat, -1)
		} else {
			a.cycleMediaSearchCategory(curCat, 1)
		}
		return true
	case ui.KeyEscape:
		if strings.TrimSpace(curQuery) != "" {
			a.SetMediaSearchQuery("")
			return true
		}
		if curCat != "" && curCat != MediaCategoryAll {
			a.SetMediaSearchCategory(MediaCategoryAll)
			return true
		}
	case ui.KeyEnter:
		if curIdx >= 0 && curIdx < len(visible) && visible[curIdx].ClipboardMedia != nil {
			a.pasteSelectedMediaItem(visible[curIdx])
			return true
		}
	}
	return false
}

func (a *AppController) cycleMediaSearchCategory(current MediaCategory, delta int) {
	filters := RegisteredMediaFilters()
	idx := 0
	for i, f := range filters {
		if f.Category == current {
			idx = i
			break
		}
	}
	next := (idx + delta + len(filters)) % len(filters)
	a.SetMediaSearchCategory(filters[next].Category)
}

func (a *AppController) copySelectedMediaItem(item engine.OmniboxItem) {
	if item.ClipboardMedia == nil {
		return
	}
	path := mediaPrimaryPath(*item.ClipboardMedia)
	if path == "" {
		return
	}
	res, err := a.ExecuteClipboardAction(ClipboardCopyRequest{
		Origin: ClipboardCopyOriginUser,
		Action: config.ClipboardActionCopyPath,
		Item: clipboard.ClipboardItem{
			ID: item.ClipboardID,
			Media: clipboard.Media{
				Kind:      clipboard.Kind(item.ClipboardMedia.Kind),
				MediaPath: path,
			},
		},
	})
	st := a.mediaSearchState()
	st.mu.Lock()
	if err != nil {
		st.statusMsg = err.Error()
	} else {
		st.statusMsg = res.Summary
	}
	st.mu.Unlock()
}

func (a *AppController) pasteSelectedMediaItem(item engine.OmniboxItem) {
	if item.ClipboardMedia == nil {
		return
	}
	snap := a.MediaProbe(*item.ClipboardMedia)
	if snap.Status == MediaProbeUnavailable || !snap.Available {
		st := a.mediaSearchState()
		st.mu.Lock()
		st.statusMsg = pickLang(i18n.IsChinese(), "原文件不可用，无法粘贴", "File unavailable — cannot paste")
		st.mu.Unlock()
		return
	}
	a.pasteClipboardMediaAsync(*item.ClipboardMedia)
}
