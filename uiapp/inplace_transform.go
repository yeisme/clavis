package uiapp

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

// SelectionBarPayload is the data a native shell needs to render the selection bar.
type SelectionBarPayload struct {
	Anchor  host.CursorAnchor
	Text    string
	Actions []string
}

type inplaceSession struct {
	original      string
	onlySelection bool
	mode          string
}

const inplaceCaptureTimeout = 750 * time.Millisecond
const inplaceMaxRunes = 4000

func (c *AppController) extendedBridge() bridge.ExtendedHostService {
	c.mu.Lock()
	b := c.Bridge
	c.mu.Unlock()
	if x, ok := b.(bridge.ExtendedHostService); ok {
		return x
	}
	return nil
}

func cappedRunes(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= inplaceMaxRunes {
		return s
	}
	return string([]rune(s)[:inplaceMaxRunes])
}

func (c *AppController) captureEditable() (host.EditableFieldSnapshot, error) {
	b := c.extendedBridge()
	if b == nil {
		return host.EditableFieldSnapshot{}, fmt.Errorf("focused text field is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), inplaceCaptureTimeout)
	defer cancel()
	return b.CaptureEditableField(ctx)
}

func (c *AppController) showSelectionQuickBar() {
	cfg := c.ConfigStore.Current()
	if !cfg.Main.SelectionBar.Enabled || strings.EqualFold(strings.TrimSpace(cfg.Main.SelectionBar.TriggerMode), "disabled") {
		return
	}
	snap, err := c.captureEditable()
	if err != nil {
		c.ShowToastOrHUD("Could not read the focused text field")
		return
	}
	text := strings.TrimSpace(snap.SelectedText)
	if text == "" && snap.SelectionEnd > snap.SelectionStart {
		text = fieldSelection(snap)
	}
	if text == "" {
		c.ShowToastOrHUD("Select text first")
		return
	}
	anchor := host.CursorAnchor{}
	if b := c.extendedBridge(); b != nil {
		ctx, cancel := context.WithTimeout(context.Background(), inplaceCaptureTimeout)
		anchor, _ = b.FastCaretAnchor(ctx)
		cancel()
		if !anchor.Valid {
			ctx, cancel = context.WithTimeout(context.Background(), inplaceCaptureTimeout)
			if s, e := b.SnapshotContext(ctx); e == nil && s.CursorAnchor != nil {
				anchor = *s.CursorAnchor
			}
			cancel()
		}
	}
	payload := SelectionBarPayload{Anchor: anchor, Text: text, Actions: append([]string(nil), cfg.Main.SelectionBar.Actions...)}
	c.mu.Lock()
	fn := c.OnShowSelectionBar
	c.mu.Unlock()
	if fn != nil {
		fn(payload)
	}
}

func fieldSelection(s host.EditableFieldSnapshot) string {
	if s.SelectionStart < 0 || s.SelectionEnd < s.SelectionStart {
		return ""
	}
	r := []rune(s.Value)
	if s.SelectionStart > len(r) {
		return ""
	}
	end := s.SelectionEnd
	if end > len(r) {
		end = len(r)
	}
	return string(r[s.SelectionStart:end])
}

// StartInPlaceTransform starts the normal Capsule AI surface after reading the focused field.
func (c *AppController) startInPlaceTransform(onlySelection bool, mode string) {
	snap, err := c.captureEditable()
	if err != nil {
		c.ShowToastOrHUD("Could not read the focused text field")
		return
	}
	if strings.TrimSpace(mode) == "" {
		mode = "optimize"
	}
	selected := strings.TrimSpace(snap.SelectedText)
	if selected == "" && snap.SelectionEnd > snap.SelectionStart {
		selected = fieldSelection(snap)
	}
	selectedExists := selected != ""
	if mode != "optimize" {
		// Rewrite/explain actions insert their answer at the caret; the selected text is
		// their source, rather than a range to replace.
		onlySelection = false
	} else if !onlySelection && selectedExists {
		onlySelection = true
	}
	working := snap.Value
	if selectedExists && (onlySelection || mode != "optimize") {
		working = selected
	}
	working = cappedRunes(working)
	if working == "" {
		c.ShowToastOrHUD("There is no text to transform")
		return
	}
	c.beginTransform(working, snap.Value, onlySelection, mode)
}

func (c *AppController) beginTransform(working, original string, onlySelection bool, mode string) {
	c.mu.Lock()
	c.transform = &inplaceSession{original: original, onlySelection: onlySelection, mode: mode}
	c.mu.Unlock()
	ctx := engine.CapsuleContext{SelectedText: working}
	c.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAI, Query: working, Context: &ctx})
	// Build an AI surface directly; this avoids relying on configurable leader actions.
	c.Dispatch(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: len(c.State().OmniboxItems) - 1})
	c.mu.Lock()
	if c.transform != nil {
		c.transform.mode = mode
		c.transform.original = original
		c.transform.onlySelection = onlySelection
	}
	c.mu.Unlock()
}

// HandleSelectionBarAction executes an action selected by the native selection-bar shell.
func (c *AppController) HandleSelectionBarAction(action, text string) {
	c.startSelectionAction(action, text)
}

func (c *AppController) startSelectionAction(action, text string) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "copy":
		c.writeClipboardText(text)
		c.ShowToastOrHUD("Copied")
	case "optimize_prompt":
		c.startInPlaceTransform(true, "optimize")
	case "explain", "translate", "polish":
		c.startInPlaceTransform(true, strings.ToLower(strings.TrimSpace(action)))
	}
}

func (c *AppController) applyInPlaceResult(ts *inplaceSession, result string) {
	// Non-optimizer selection-bar actions use the normal caret insertion behavior.
	if ts.mode != "optimize" {
		c.mu.Lock()
		if c.transform == ts {
			c.transform = nil
		}
		c.mu.Unlock()
		c.injectTextAsync(result, 0, 0)
		c.recordPromptVersion("in_place_transform", "", ts.original, result, ts.mode)
		return
	}
	// A session is single-use, including stale/failure paths. This prevents a later
	// unrelated AI acceptance from attempting to write an old transformation.
	c.mu.Lock()
	if c.transform == ts {
		c.transform = nil
	}
	c.mu.Unlock()
	b := c.extendedBridge()
	if b == nil {
		c.ShowToastOrHUD("Focused text field is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), inplaceCaptureTimeout)
	current, err := b.CaptureEditableField(ctx)
	cancel()
	if err != nil {
		c.writeClipboardText(result)
		c.ShowToastOrHUD("Could not verify the field; result copied instead")
		return
	}
	if current.Value != ts.original {
		c.writeClipboardText(result)
		c.ShowToastOrHUD("The field changed while transforming; result copied instead")
		return
	}
	ctx, cancel = context.WithTimeout(context.Background(), inplaceCaptureTimeout)
	err = b.ReplaceEditableField(ctx, result, ts.onlySelection)
	cancel()
	if err != nil {
		c.writeClipboardText(result)
		c.ShowToastOrHUD("Could not replace the field; result copied instead")
		return
	}
	c.recordPromptVersion("in_place_transform", "", ts.original, result, ts.mode)
}
