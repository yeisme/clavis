package uiapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestClipboardCompactRowTruncationAndSelectionStyles(t *testing.T) {
	collapsed := collapseClipboardLine("line one\r\n   line\t\ttwo\nline three")
	if collapsed != "line one ↵ line two ↵ line three" {
		t.Fatalf("collapseClipboardLine = %q", collapsed)
	}

	// Bounded scan on huge 256 KiB blob must stay short & clean.
	huge := strings.Repeat("abcdefghij ", 30000)
	if got := collapseClipboardLine(huge); len(got) > clipboardSniffBytes {
		t.Fatalf("collapseClipboardLine exceeded sniff bound: %d > %d", len(got), clipboardSniffBytes)
	}

	// URL semantic middle truncation preserves scheme/host and tail.
	longURL := "https://github.com/yeisme/clavis/blob/main/pkg/core/engine/clipboard_query_long_filename.go"
	truncURL := clipboardCompactTitle(longURL, "", 48)
	if !strings.HasPrefix(truncURL, "https://github.com") || !strings.Contains(truncURL, "…") || !strings.HasSuffix(truncURL, ".go") {
		t.Fatalf("URL semantic truncation = %q", truncURL)
	}

	// File path truncation preserves root + filename.
	longPath := "/Users/example/Documents/workspace/project/src/components/ClipboardPreviewCard.tsx"
	truncPath := clipboardCompactTitle(longPath, "", 44)
	if !strings.HasPrefix(truncPath, "/Users/") || !strings.Contains(truncPath, "…") || !strings.HasSuffix(truncPath, "Card.tsx") {
		t.Fatalf("path semantic truncation = %q", truncPath)
	}

	// Long unbroken token preserves head + tail.
	longToken := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	truncToken := clipboardCompactTitle(longToken, "", 44)
	if !strings.HasPrefix(truncToken, "eyJhbGciOiJIUzI1NiIs") || !strings.Contains(truncToken, "…") || !strings.HasSuffix(truncToken, "Qssw5c") {
		t.Fatalf("token truncation = %q", truncToken)
	}

	// Search match past rune 40 centers the window around the match.
	deepText := strings.Repeat("padding text words ", 6) + "NEEDLE_TARGET trailing words"
	centered := clipboardCompactTitle(deepText, "NEEDLE_TARGET", 48)
	if !strings.Contains(centered, "NEEDLE_TARGET") || !strings.HasPrefix(centered, "…") {
		t.Fatalf("search-hit centering = %q", centered)
	}

	// B2 Maccy Solid Blue style paints selected row with #0A84FF.
	f := newInteractionFixture(t, nil)
	_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Clipboard.SelectionStyle = "maccy_solid"
		m.Clipboard.RowDensity = "compact"
	})
	f.record(t, "alpha entry", "beta entry")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 2 }, "clipboard items")
	tt := f.omniboxTester(t)
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	chosen, ok := tt.Find("beta entry")
	if !ok {
		t.Fatalf("selected row missing: %v", tt.Texts())
	}
	solidBlue := ui.RGB(10, 132, 255)
	px := rgbaAt(tt.Image(), int(chosen.X+chosen.W/2), int(chosen.Y+chosen.H-3))
	if !colorClose(px, solidBlue) {
		t.Fatalf("maccy_solid row background pixel=%v, want %v", px, solidBlue)
	}
}

func TestClipboardFocusModeHidesTabsAndCmdDigitsPaste(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "first focus row", "second focus row")

	f.ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerClipboardToggle})
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 2 }, "focus mode clipboard")
	if !f.ctrl.clipboardFocusMode() {
		t.Fatal("expected Focus Mode active after TriggerClipboardToggle")
	}

	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	for _, tabKey := range []string{"omnibox.tab.snippets", "omnibox.tab.glossary", "omnibox.tab.agent"} {
		if _, hasTab := tt.Find(i18n.T(tabKey)); hasTab {
			t.Fatalf("Focus Mode should hide 6-tab bar (%s), texts=%v", tabKey, tt.Texts())
		}
	}

	// ⌘2 directly pastes row 1 ("first focus row") in Focus Mode.
	tt.Key(primaryMod(), ui.Key2)
	waitUntil(t, 2*time.Second, func() bool { return f.bridge.getPasteClipboardCalls() == 1 }, "⌘2 direct paste")
	if got := f.copiedTexts(); len(got) == 0 || got[len(got)-1] != "first focus row" {
		t.Fatalf("expected ⌘2 to write first focus row, copied=%v", got)
	}
}

func TestClipboardFocusModeArrowSwitchesOutOfFocusMode(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "focus item")
	f.ctrl.OpenClipboardFocusMode("")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 1 }, "focus open")
	tt := f.omniboxTester(t)
	tt.Frame()
	tt.Key(primaryMod(), ui.KeyRight)
	if f.ctrl.clipboardFocusMode() {
		t.Fatal("⌘→ should exit Focus Mode when switching tab")
	}
	if st := f.ctrl.State(); st.OmniboxTab == config.TabClipboard {
		t.Fatalf("⌘→ did not leave Clipboard tab: %+v", st.OmniboxTab)
	}
}

func TestClipboardPasteNoAXFallbackAndKeepOpen(t *testing.T) {
	// Case 1: Missing Accessibility auto-downgrades to Copy + friendly HUD + dismiss.
	fNoAX := newInteractionFixture(t, nil)
	fNoAX.bridge.mu.Lock()
	fNoAX.bridge.perms.Accessibility = false
	fNoAX.bridge.mu.Unlock()
	fNoAX.record(t, "no-ax copy fallback")
	fNoAX.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(fNoAX.ctrl.State().OmniboxItems) >= 1 }, "no-ax items")

	fNoAX.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, 2*time.Second, func() bool { return fNoAX.ctrl.State().Phase == engine.PhaseHidden }, "no-ax dismiss")
	if calls := fNoAX.bridge.getPasteClipboardCalls(); calls != 0 {
		t.Fatalf("no-AX fallback must not invoke CGEvent PasteClipboard, got %d", calls)
	}
	if got := fNoAX.copiedTexts(); len(got) == 0 || got[len(got)-1] != "no-ax copy fallback" {
		t.Fatalf("no-AX fallback did not copy text: %v", got)
	}

	// Case 2: ⌘↵ (KeepOpen) pastes without dismissing the Capsule.
	fKeep := newInteractionFixture(t, nil)
	fKeep.record(t, "multi paste entry")
	fKeep.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(fKeep.ctrl.State().OmniboxItems) >= 1 }, "keep-open items")
	fKeep.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSecondarySelection})
	waitUntil(t, 2*time.Second, func() bool { return fKeep.bridge.getPasteClipboardCalls() == 1 }, "⌘↵ paste")
	time.Sleep(30 * time.Millisecond)
	if st := fKeep.ctrl.State(); st.Phase != engine.PhaseOmnibox {
		t.Fatalf("⌘↵ keep-open closed Capsule: phase=%s", st.Phase)
	}
}

func TestClipboardSoftDelete5sUndoAndImmediateFlush(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.SetClipboardDeleteUndoWindowForTest(80 * time.Millisecond)
	f.record(t, "keep me", "delete candidate")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) == 2 }, "two items")

	first := f.ctrl.State().OmniboxItems[0]
	f.ctrl.RequestSoftDeleteClipboardItem(first)
	if !f.ctrl.HasPendingClipboardDelete() {
		t.Fatal("expected pending soft delete")
	}
	vis := f.ctrl.visibleOmniboxItems(f.ctrl.State())
	if len(vis.items) != 1 || vis.items[0].ClipboardContent != "keep me" {
		t.Fatalf("soft delete should hide item immediately, got %+v", vis.items)
	}

	// Undo restores immediately and cancels DB delete.
	if !f.ctrl.UndoSoftDeleteClipboardItem() {
		t.Fatal("UndoSoftDeleteClipboardItem returned false")
	}
	time.Sleep(110 * time.Millisecond)
	items, _ := f.clips.Recent(context.Background(), 10)
	if len(items) != 2 {
		t.Fatalf("undone item was still deleted from store: %d", len(items))
	}

	// Second soft-delete commits to store once timer expires.
	f.ctrl.RequestSoftDeleteClipboardItem(first)
	waitUntil(t, 2*time.Second, func() bool {
		cur, _ := f.clips.Recent(context.Background(), 10)
		return len(cur) == 1
	}, "soft delete commit after window")
}

func TestClipboardClearAllConfirmAndPreservesPinned(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "pinned-keep", "unpinned-one", "unpinned-two")
	items, _ := f.clips.Recent(context.Background(), 10)
	for _, it := range items {
		if it.Content == "pinned-keep" {
			_ = f.clips.Pin(context.Background(), it.ID, true)
		}
	}
	f.ctrl.syncClipboardEntries(context.Background())
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)
	tt.Frame()

	// ⌥⇧⌘⌫ opens footer confirmation; Esc cancels without deleting.
	tt.Key(primaryMod()|ui.Alt|ui.Shift, ui.KeyBackspace)
	tt.Frame()
	if !f.ctrl.clipboardClearConfirm() {
		t.Fatal("⌥⇧⌘⌫ did not open inline clear-all confirmation")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if f.ctrl.clipboardClearConfirm() {
		t.Fatal("Esc did not dismiss clear-all confirmation")
	}
	if cur, _ := f.clips.Recent(context.Background(), 10); len(cur) != 3 {
		t.Fatalf("Esc clear confirmation unexpectedly deleted items: %d", len(cur))
	}

	// Trigger again & click Confirm button.
	tt.Key(primaryMod()|ui.Alt|ui.Shift, ui.KeyBackspace)
	tt.Frame()
	btn, ok := tt.Find(i18n.T("clipboard.clear.confirm_btn"))
	if !ok {
		t.Fatalf("missing clear confirm button: %v", tt.Texts())
	}
	tt.ClickAt(btn.X+btn.W/2, btn.Y+btn.H/2)
	tt.Frame()
	cur, _ := f.clips.Recent(context.Background(), 10)
	if len(cur) != 1 || cur[0].Content != "pinned-keep" || !cur[0].IsPinned {
		t.Fatalf("ClearUnpinned failed to preserve only pinned entry: %+v", cur)
	}
}

func TestClipboardFloatingPopoverNoListShiftAndSearchFilter(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "https://clavis.dev", "second row")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 2 }, "popover items")
	tt := f.omniboxTester(t)
	tt.Frame()

	before, ok := tt.Find("https://clavis.dev")
	if !ok {
		t.Fatalf("first row missing: %v", tt.Texts())
	}
	tt.Key(primaryMod(), ui.KeyK)
	tt.Frame()
	if !f.ctrl.omniboxActionMenuOpen(f.ctrl.State()) {
		t.Fatal("⌘K did not open action popover")
	}
	after, ok := tt.Find("https://clavis.dev")
	if !ok || after.Y != before.Y {
		t.Fatalf("floating ⌘K popover shifted list row Y from %.1f to %.1f", before.Y, after.Y)
	}
	// Filtering actions narrows menu items without moving the list.
	capsuleUI.setActionMenuQuery(f.ctrl, "snippet")
	tt.Frame()
	specs := f.ctrl.filteredActionMenuSpecs(f.ctrl.State().OmniboxItems[0])
	if len(specs) != 1 || specs[0].ID != "save_snippet" {
		t.Fatalf("expected save_snippet after filter, got %+v", specs)
	}
}

func TestClipboardBlockAppSaveSnippetColorCopyAndSourceFilter(t *testing.T) {
	f := newInteractionFixture(t, nil)
	_, _, _ = f.clips.Record(context.Background(), clipboard.CaptureCandidate{
		Content:        "#0A84FF",
		SourceBundleID: "com.figma.Desktop",
		SourceAppName:  "Figma",
		Timestamp:      time.Now().Add(-time.Minute),
	})
	_, _, _ = f.clips.Record(context.Background(), clipboard.CaptureCandidate{
		Content:        "snippet candidate text",
		SourceBundleID: "com.apple.Notes",
		SourceAppName:  "Notes",
		Timestamp:      time.Now(),
	})
	f.ctrl.syncClipboardEntries(context.Background())
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 2 }, "entries loaded")

	// Block source app persists to ExcludedBundleIDs.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "block_app"})
	excluded := f.ctrl.Config().Main.Clipboard.ExcludedBundleIDs
	foundNotes := false
	for _, b := range excluded {
		if b == "com.apple.Notes" {
			foundNotes = true
		}
	}
	if !foundNotes {
		t.Fatalf("block_app did not persist com.apple.Notes into ExcludedBundleIDs: %v", excluded)
	}

	// Save as snippet opens Snippets section with draft prefilled.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "save_snippet"})
	if seGet(f.ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.snippetDraft.Content }) != "snippet candidate text" {
		t.Fatal("save_snippet did not prefill snippet draft")
	}

	// Re-open on color entry (#0A84FF) and click color copy chip + Source App filter.
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	f.ctrl.Dispatch(engine.Event{Type: engine.EventSelectIndex, Index: 1})
	tt := f.omniboxTester(t)
	tt.Frame()
	if rgbBtn, ok := tt.Find("RGB:rgb(10, 132, 255)"); ok {
		tt.ClickAt(rgbBtn.X+rgbBtn.W/2, rgbBtn.Y+rgbBtn.H/2)
		if got := f.copiedTexts(); len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "rgb(") {
			t.Fatalf("clicking RGB color chip did not copy rgb(...): %v", got)
		}
	} else {
		t.Fatalf("preview-color-rgb button missing in preview pane: %v", tt.Texts())
	}

	if appChip, ok := tt.Find("app:Figma"); ok {
		tt.ClickAt(appChip.X+appChip.W/2, appChip.Y+appChip.H/2)
		tt.Frame()
		if st := f.ctrl.State(); !strings.Contains(st.OmniboxQuery, "app:Figma") {
			t.Fatalf("clicking source app chip did not set app:Figma query, got %q", st.OmniboxQuery)
		}
	} else {
		t.Fatalf("preview-meta-source-app chip missing in metadata footer: %v", tt.Texts())
	}
}

func TestClipboard5LevelEscapeSequence(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "esc item")
	f.ctrl.OpenOmnibox(config.TabClipboard, "esc")
	f.ctrl.clipboardStatsMu.Lock()
	f.ctrl.clipboardStats.filter = "text"
	f.ctrl.clipboardStatsMu.Unlock()
	capsuleUI.setActionMenuShown(f.ctrl, true)

	tt := f.omniboxTester(t)
	tt.Frame()

	// Level 1 (with no IME preedit): closes ⌘K action menu.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if f.ctrl.omniboxActionMenuOpen(f.ctrl.State()) {
		t.Fatal("Esc #1 should close action menu")
	}
	// Level 2: clears non-empty query.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if f.ctrl.State().OmniboxQuery != "" {
		t.Fatalf("Esc #2 should clear query, still %q", f.ctrl.State().OmniboxQuery)
	}
	// Level 3: clears active clipboard filter.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if f.ctrl.clipboardStatsFilter() != "" {
		t.Fatalf("Esc #3 should reset filter, still %q", f.ctrl.clipboardStatsFilter())
	}
	// Level 4: dismisses clean Capsule.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if f.ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("Esc #4 should dismiss Capsule, phase=%s", f.ctrl.State().Phase)
	}
}
