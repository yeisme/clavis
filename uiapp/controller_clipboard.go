package uiapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/yeisme/clavis/internal/bridge"
	internalstorage "github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
)

// ClipboardSearcher narrows clipboard querying to Search so AppController can
// route Clipboard tab queries to SQLite FTS5 (or MemoryStore fallback) without
// coupling pkg/core/engine to storage.
type ClipboardSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]clipboard.ClipboardItem, error)
}

type clipboardStatsReader interface {
	ClipboardDaily(context.Context, int) ([]internalstorage.ClipboardDay, error)
	ClipboardKindStats(context.Context) ([]internalstorage.ClipboardKindStat, error)
}

const (
	defaultClipboardMirrorLimit    = 50
	defaultClipboardSearchDebounce = 50 * time.Millisecond
)

type clipTask struct {
	gen       uint64
	syncAfter bool
	fn        func(ctx context.Context) error
}

func (c *AppController) runClipboardWorker() {
	defer close(c.clipDone)
	for {
		c.clipMu.Lock()
		for len(c.clipQueue) == 0 && !c.clipClosed {
			c.clipCond.Wait()
		}
		if c.clipClosed && len(c.clipQueue) == 0 {
			c.clipMu.Unlock()
			return
		}
		task := c.clipQueue[0]
		c.clipQueue = c.clipQueue[1:]
		c.clipMu.Unlock()

		ctx := context.Background()
		if task.fn != nil && (c.ClipboardStore != nil || !task.syncAfter) {
			_ = task.fn(ctx)
		}
		if task.syncAfter && c.ClipboardStore != nil && c.clipGen.Load() == task.gen {
			c.syncClipboardEntries(ctx)
			c.refreshActiveClipboardSearch(ctx)
		}
	}
}

func (c *AppController) enqueueClipboardTask(fn func(ctx context.Context) error) uint64 {
	return c.enqueueClipboardTaskWithGeneration(func(ctx context.Context, _ uint64) error {
		return fn(ctx)
	})
}

// enqueueClipboardTaskWithGeneration gives an operation the exact queue token
// assigned to it. This is important for multi-step clipboard effects: reading
// clipGen from inside a worker would accidentally accept a newer queued action.
func (c *AppController) enqueueClipboardTaskWithGeneration(fn func(ctx context.Context, generation uint64) error) uint64 {
	gen := c.clipGen.Add(1)
	c.clipMu.Lock()
	defer c.clipMu.Unlock()
	if !c.clipClosed {
		c.clipQueue = append(c.clipQueue, clipTask{gen: gen, syncAfter: true, fn: func(ctx context.Context) error {
			return fn(ctx, gen)
		}})
		if c.clipCond != nil {
			c.clipCond.Signal()
		}
	}
	return gen
}

func (c *AppController) enqueueClipboardSearchTask(fn func(ctx context.Context) error) {
	c.clipMu.Lock()
	if !c.clipClosed {
		c.clipQueue = append(c.clipQueue, clipTask{syncAfter: false, fn: fn})
		if c.clipCond != nil {
			c.clipCond.Signal()
		}
	}
	c.clipMu.Unlock()
}

func (c *AppController) clipboardMirrorLimit() int {
	limit := c.ConfigStore.Current().Main.Clipboard.MaxHistoryItems
	if limit <= 0 {
		limit = 200
	}
	if limit > defaultClipboardMirrorLimit {
		limit = defaultClipboardMirrorLimit
	}
	return limit
}

func (c *AppController) syncClipboardEntries(ctx context.Context) {
	if c.ClipboardStore == nil {
		return
	}
	limit := c.clipboardMirrorLimit()

	var gen uint64
	var hasGen bool
	if gt, ok := c.ClipboardStore.(clipboard.GenerationTracker); ok {
		gen = gt.Generation()
		hasGen = true
		c.clipSyncMu.Lock()
		unchanged := c.clipSynced && c.clipSyncedHasGen && c.clipSyncedGen == gen && c.clipSyncedLimit == limit
		c.clipSyncMu.Unlock()
		if unchanged {
			return
		}
	}

	items, err := c.ClipboardStore.Recent(ctx, limit)
	if err != nil {
		return
	}

	c.clipSyncMu.Lock()
	c.clipSynced = true
	c.clipSyncedHasGen = hasGen
	c.clipSyncedGen = gen
	c.clipSyncedLimit = limit
	c.clipSyncMu.Unlock()

	c.invalidateClipboardStats()
	c.Dispatch(engine.Event{
		Type:             engine.EventSetClipboardEntries,
		ClipboardEntries: clipboardItemsToEntries(items),
	})
}

func clipboardItemsToEntries(items []clipboard.ClipboardItem) []engine.OmniboxClipboardEntry {
	entries := make([]engine.OmniboxClipboardEntry, len(items))
	for i, it := range items {
		media := engineClipboardMedia(it.Media)
		preview := engine.ClipboardEntryPreview{
			Summary:   it.Content,
			Text:      it.Content,
			ThumbPath: media.ThumbPath,
			Decodable: !media.IsMedia() || media.ThumbPath != "" || media.Kind != "image",
		}
		if media.IsMedia() {
			preview.Summary = engine.ClipboardMediaTitle(media, it.Content)
			preview.Text = ""
		}
		entries[i] = engine.OmniboxClipboardEntry{
			ID: it.ID, Content: it.Content, SourceAppName: it.SourceAppName, IsPinned: it.IsPinned,
			CopiedAt: clipboardCopiedAt(it),
			Media:    media, Preview: preview,
			Actions: engine.ClipboardEntryActions{CanPin: !it.IsPinned, CanUnpin: it.IsPinned, CanCopy: true, CanPaste: true, CanDelete: true},
		}
	}
	return entries
}

// clipboardCopiedAt is when an entry was last copied: a re-copy of the same
// content refreshes UpdatedAt (and moves the entry up), so that wins over
// the first capture time.
func clipboardCopiedAt(it clipboard.ClipboardItem) time.Time {
	if !it.UpdatedAt.IsZero() {
		return it.UpdatedAt
	}
	return it.CreatedAt
}

func (c *AppController) cancelClipboardSearch(seq uint64) {
	c.clipSearchMu.Lock()
	if seq < c.clipSearchSeq.Load() {
		c.clipSearchMu.Unlock()
		return
	}
	if c.clipSearchTimer != nil {
		c.clipSearchTimer.Stop()
		c.clipSearchTimer = nil
	}
	if c.clipSearchCancel != nil {
		c.clipSearchCancel()
		c.clipSearchCancel = nil
	}
	c.clipSearchMu.Unlock()
}

func (c *AppController) scheduleClipboardSearch(seq uint64, query string, immediate bool) {
	c.clipSearchMu.Lock()
	if seq < c.clipSearchSeq.Load() {
		c.clipSearchMu.Unlock()
		return
	}
	if c.clipSearchTimer != nil {
		c.clipSearchTimer.Stop()
		c.clipSearchTimer = nil
	}
	if c.clipSearchCancel != nil {
		c.clipSearchCancel()
		c.clipSearchCancel = nil
	}
	c.clipSearchTimerSeq = seq
	debounce := c.clipSearchDebounce
	if immediate || debounce <= 0 {
		baseCtx := c.clipBaseCtx
		if baseCtx == nil {
			baseCtx = c.lifeCtx()
		}
		searchCtx, cancel := context.WithCancel(baseCtx)
		c.clipSearchCancel = cancel
		c.clipSearchMu.Unlock()
		c.enqueueClipboardSearchTask(func(context.Context) error {
			c.executeClipboardSearch(searchCtx, seq, query)
			return nil
		})
		return
	}

	c.clipSearchTimer = time.AfterFunc(debounce, func() {
		c.clipSearchMu.Lock()
		if c.clipSearchSeq.Load() != seq || c.clipSearchTimerSeq != seq {
			c.clipSearchMu.Unlock()
			return
		}
		c.clipSearchTimer = nil
		baseCtx := c.clipBaseCtx
		if baseCtx == nil {
			baseCtx = c.lifeCtx()
		}
		searchCtx, cancel := context.WithCancel(baseCtx)
		c.clipSearchCancel = cancel
		c.clipSearchMu.Unlock()

		c.enqueueClipboardSearchTask(func(context.Context) error {
			c.executeClipboardSearch(searchCtx, seq, query)
			return nil
		})
	})
	c.clipSearchMu.Unlock()
}

func (c *AppController) refreshActiveClipboardSearch(ctx context.Context) {
	st := c.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard {
		return
	}
	query := strings.TrimSpace(st.OmniboxQuery)
	if query == "" {
		return
	}
	seq := c.clipSearchSeq.Load()
	c.executeClipboardSearch(ctx, seq, query)
}

func (c *AppController) executeClipboardSearch(ctx context.Context, seq uint64, query string) {
	if c.clipSearchSeq.Load() != seq || ctx.Err() != nil {
		return
	}
	st := c.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard || strings.TrimSpace(st.OmniboxQuery) != query {
		return
	}
	c.mu.Lock()
	searcher := c.ClipboardSearcher
	c.mu.Unlock()
	if searcher == nil {
		return
	}

	var genBefore uint64
	var hasGen bool
	if gt, ok := c.ClipboardStore.(clipboard.GenerationTracker); ok {
		genBefore = gt.Generation()
		hasGen = true
	}

	items, err := searcher.Search(ctx, query, c.clipboardMirrorLimit())
	if err != nil {
		return
	}

	// Generation + query stale-result guards.
	if c.clipSearchSeq.Load() != seq || ctx.Err() != nil {
		return
	}
	if hasGen {
		if gt, ok := c.ClipboardStore.(clipboard.GenerationTracker); ok {
			if gt.Generation() != genBefore {
				return
			}
		}
	}
	stAfter := c.State()
	if stAfter.Phase != engine.PhaseOmnibox || stAfter.OmniboxTab != config.TabClipboard || strings.TrimSpace(stAfter.OmniboxQuery) != query {
		return
	}

	c.Dispatch(engine.Event{
		Type:             engine.EventSetClipboardSearchResults,
		Query:            query,
		ClipboardEntries: clipboardItemsToEntries(items),
	})
}

func (c *AppController) injectTextAsync(text string, deleteCount, cursorBackOffset int) {
	c.mu.Lock()
	br := c.Bridge
	beforeInject := c.BeforeInject
	closed := c.closed
	c.mu.Unlock()
	if br == nil || closed {
		return
	}
	ctx := c.lifeCtx()
	go func() {
		if ctx.Err() != nil {
			return
		}
		if beforeInject != nil {
			beforeInject(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		_ = br.InjectText(ctx, text, deleteCount, cursorBackOffset)
	}()
}

// injectWorkflowTextAsync rechecks controller shutdown and the foreground
// target immediately before injection. The check is intentionally performed in
// the goroutine: workflow output can finish long after the capsule was opened.
func (c *AppController) injectWorkflowTextAsync(text string, deleteCount, cursorBackOffset int, targetGen uint64, target engine.CapsuleContext) {
	lifeCtx := c.lifeCtx()
	go func() {
		if lifeCtx.Err() != nil {
			return
		}
		c.workflowLifecycleMu.Lock()
		defer c.workflowLifecycleMu.Unlock()
		c.mu.Lock()
		if c.closed || c.workflowTargetGen != targetGen || c.Bridge == nil {
			c.mu.Unlock()
			return
		}
		br := c.Bridge
		beforeInject := c.BeforeInject
		c.mu.Unlock()
		ctx := lifeCtx
		// A foreground-app switch does not necessarily open another Clavis
		// capsule. Verify the target again immediately before injection so a
		// completed workflow cannot write into the newly focused application.
		snapshot, err := br.SnapshotContext(ctx)
		if err != nil || !workflowTargetMatchesSnapshot(target, snapshot) {
			return
		}
		if beforeInject != nil {
			beforeInject(ctx)
		}
		// BeforeInject may synchronously open UI or otherwise allow the user to
		// change the foreground application. Take a second identity snapshot
		// immediately before writing, and refuse the injection if it changed.
		snapshot, err = br.SnapshotContext(ctx)
		if err != nil || !workflowTargetMatchesSnapshot(target, snapshot) {
			return
		}
		_ = br.InjectText(ctx, text, deleteCount, cursorBackOffset)
	}()
}

func (c *AppController) writeClipboardText(text string) error {
	c.mu.Lock()
	writer := c.ClipboardWriter
	writerWithError := c.ClipboardWriterWithError
	c.mu.Unlock()
	if writerWithError != nil {
		return writerWithError(text)
	}
	if writer != nil {
		writer(text)
		return nil
	}
	if mygo.App.IsReady() {
		// WriteText intentionally discards backend errors. Use Write directly so
		// a failed system clipboard write cannot be mistaken for a successful paste.
		return mygo.Clipboard.Write(transfer.TextData(text))
	}
	return errors.New("app not ready")
}

// pasteClipboardTextAsync writes the selected text to the system clipboard and
// then asks the optional platform bridge to send Cmd+V/Ctrl+V. The callback is
// invoked only after both operations succeed, so failed writes or unsupported
// paste capabilities cannot be recorded as successful item use.
func (c *AppController) pasteClipboardTextAsync(text string, onPasted func()) {
	if err := c.writeClipboardText(text); err != nil {
		c.ShowToastOrHUD(err.Error())
		return
	}
	c.startClipboardPaste(onPasted)
}

// startClipboardPaste serializes delayed paste shortcuts and invalidates them when
// a new Capsule target or controller lifecycle supersedes the operation.
func (c *AppController) startClipboardPaste(onPasted func()) {
	regCtx, done := c.ensureRuntime().Begin(runtimecoord.ClipboardPaste, "Clipboard paste")
	ctx, cancel := context.WithCancel(regCtx)
	c.mu.Lock()
	br := c.Bridge
	beforeInject := c.BeforeInject
	if c.clipboardPasteCancel != nil {
		c.clipboardPasteCancel()
		if c.clipboardPasteDone != nil {
			c.clipboardPasteDone()
			c.clipboardPasteDone = nil
		}
	}
	c.clipboardPasteDone = done
	c.clipboardPasteCancel = cancel
	c.clipboardPasteGen++
	generation := c.clipboardPasteGen
	c.mu.Unlock()
	paster, ok := br.(bridge.MediaPaster)
	if !ok || paster == nil {
		cancel()
		done()
		c.ShowToastOrHUD(i18n.T("clipboard.media.paste_unsupported"))
		return
	}
	go func() {
		defer done()
		if beforeInject != nil {
			beforeInject(ctx)
		}
		if err := ctx.Err(); err != nil || !c.clipboardPasteCurrent(generation) {
			return
		}
		if err := paster.PasteClipboard(ctx); err != nil {
			if ctx.Err() == nil {
				c.ShowToastOrHUD(err.Error())
			}
			return
		}
		if !c.clipboardPasteCurrent(generation) {
			return
		}
		if onPasted != nil {
			onPasted()
		}
		c.Dispatch(engine.Event{Type: engine.EventDismiss})
	}()
}

func (c *AppController) clipboardPasteCurrent(generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.clipboardPasteGen == generation
}

// activateClipboardItem is the compatibility entry point for old shells. A
// click is a paste, never a pin or preview mutation; explicit pinning remains
// available through the action menu and Cmd/Ctrl+P.
func (c *AppController) activateClipboardItem(op engine.ClipboardActivation) {
	if op.Media != nil && op.Media.IsMedia() {
		c.pasteClipboardMediaAsync(*op.Media, func() {
			if op.ID != "" {
				c.recordItemUse("clipboard:" + op.ID)
			}
		})
		return
	}
	c.pasteClipboardTextAsync(op.Content, func() {
		if op.ID != "" {
			c.recordItemUse("clipboard:" + op.ID)
		}
	})
}
