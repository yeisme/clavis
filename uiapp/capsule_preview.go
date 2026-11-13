package uiapp

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// capsuleEphemeralStore keeps per-controller ephemeral UI state for the
// CapsuleView upgrades (preview pane override, contextual action menu,
// follow-up IME composition flag) without widening AppController.
//
// Everything here is accessed from the UI build thread, but the runtime may
// repaint from another goroutine, so all access is guarded by mu.
type capsuleEphemeralStore struct {
	mu sync.Mutex

	previewVisible    map[*AppController]bool // presence = explicit user override
	actionMenuOpen    map[*AppController]bool
	actionMenuIdx     map[*AppController]int
	followUpComposing map[*AppController]bool
}

var capsuleUI = &capsuleEphemeralStore{
	previewVisible:    make(map[*AppController]bool),
	actionMenuOpen:    make(map[*AppController]bool),
	actionMenuIdx:     make(map[*AppController]int),
	followUpComposing: make(map[*AppController]bool),
}

func (s *capsuleEphemeralStore) reset(ctrl *AppController) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.previewVisible, ctrl)
	delete(s.actionMenuOpen, ctrl)
	delete(s.actionMenuIdx, ctrl)
	delete(s.followUpComposing, ctrl)
}

func (s *capsuleEphemeralStore) previewFor(ctrl *AppController, configured bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.previewVisible[ctrl]; ok {
		return v
	}
	return configured
}

func (s *capsuleEphemeralStore) setPreviewVisible(ctrl *AppController, visible bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previewVisible[ctrl] = visible
}

func (s *capsuleEphemeralStore) togglePreview(ctrl *AppController, configured bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := true
	if v, ok := s.previewVisible[ctrl]; ok {
		next = !v
	} else {
		next = !configured
	}
	s.previewVisible[ctrl] = next
	return next
}

func (s *capsuleEphemeralStore) actionMenuShown(ctrl *AppController) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actionMenuOpen[ctrl]
}

func (s *capsuleEphemeralStore) setActionMenuShown(ctrl *AppController, open bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if open {
		s.actionMenuOpen[ctrl] = true
		return
	}
	delete(s.actionMenuOpen, ctrl)
	delete(s.actionMenuIdx, ctrl)
}

func (s *capsuleEphemeralStore) actionMenuSelection(ctrl *AppController) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actionMenuIdx[ctrl]
}

func (s *capsuleEphemeralStore) setActionMenuSelection(ctrl *AppController, idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actionMenuIdx[ctrl] = idx
}

func (s *capsuleEphemeralStore) moveActionMenuSelection(ctrl *AppController, delta, count int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if count <= 0 {
		s.actionMenuIdx[ctrl] = 0
		return 0
	}
	idx := s.actionMenuIdx[ctrl] + delta
	for idx < 0 {
		idx += count
	}
	idx %= count
	s.actionMenuIdx[ctrl] = idx
	return idx
}

func (s *capsuleEphemeralStore) composingFollowUp(ctrl *AppController) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.followUpComposing[ctrl]
}

func (s *capsuleEphemeralStore) setFollowUpComposing(ctrl *AppController, composing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if composing {
		s.followUpComposing[ctrl] = true
		return
	}
	delete(s.followUpComposing, ctrl)
}

// followUpComposing reports whether the AI follow-up input is mid IME composition.
func (a *AppController) followUpComposing() bool {
	return capsuleUI.composingFollowUp(a)
}

// previewPaneVisible reports the effective split preview pane visibility.
func (a *AppController) previewPaneVisible(cfg config.ClavisConfig) bool {
	return capsuleUI.previewFor(a, cfg.Main.Appearance.ShowPreviewPane)
}

// omniboxActionMenuOpen reports whether the contextual ⌘K action menu shows.
func (a *AppController) omniboxActionMenuOpen(state engine.State) bool {
	if len(state.OmniboxItems) == 0 {
		return false
	}
	return capsuleUI.actionMenuShown(a)
}

// toggleOmniboxActionMenu flips the contextual action menu for the selected item.
func (a *AppController) toggleOmniboxActionMenu() {
	open := capsuleUI.actionMenuShown(a)
	capsuleUI.setActionMenuShown(a, !open)
	if !open {
		capsuleUI.setActionMenuSelection(a, 0)
	}
}

// selectedOmniboxItem returns the currently selected Omnibox row, if any.
func selectedOmniboxItem(state engine.State) (engine.OmniboxItem, bool) {
	idx := state.SelectedIndex
	if idx < 0 || idx >= len(state.OmniboxItems) {
		if len(state.OmniboxItems) == 0 {
			return engine.OmniboxItem{}, false
		}
		idx = 0
	}
	return state.OmniboxItems[idx], true
}

// omniboxPreviewItem reports the selected row when it has rich previewable content.
func omniboxPreviewItem(state engine.State) (engine.OmniboxItem, bool) {
	item, ok := selectedOmniboxItem(state)
	if !ok {
		return engine.OmniboxItem{}, false
	}
	switch item.Kind {
	case engine.ItemClipboardEntry,
		engine.ItemSnippet,
		engine.ItemGlossaryTerm,
		engine.ItemAIMode,
		engine.ItemUtility,
		engine.ItemAppIMERule,
		engine.ItemInstalledApp:
		return item, true
	default:
		return engine.OmniboxItem{}, false
	}
}

// adaptActionShortcut rewrites macOS shortcut glyphs for other platforms.
func adaptActionShortcut(shortcut string) string {
	if runtime.GOOS == "darwin" || shortcut == "" {
		return shortcut
	}
	return strings.ReplaceAll(shortcut, "⌘", "Ctrl+")
}

// handleActionMenuKey intercepts keys while the contextual Action Menu is open.
// It returns true when the key belongs to the menu. Escape closes only the
// menu, never the Capsule.
func (a *AppController) handleActionMenuKey(key ui.Key, mods ui.Modifiers, state engine.State) bool {
	if mods != 0 || !a.omniboxActionMenuOpen(state) {
		return false
	}
	item, ok := selectedOmniboxItem(state)
	if !ok {
		return false
	}
	specs := engine.AvailableItemActions(item)
	if len(specs) == 0 {
		return false
	}
	switch key {
	case ui.KeyEscape:
		capsuleUI.setActionMenuShown(a, false)
		return true
	case ui.KeyUp:
		capsuleUI.moveActionMenuSelection(a, -1, len(specs))
		return true
	case ui.KeyDown:
		capsuleUI.moveActionMenuSelection(a, 1, len(specs))
		return true
	case ui.KeyEnter:
		idx := capsuleUI.actionMenuSelection(a)
		if idx < 0 || idx >= len(specs) {
			idx = 0
		}
		spec := specs[idx]
		capsuleUI.setActionMenuShown(a, false)
		a.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: spec.ID})
		return true
	default:
		return false
	}
}

// renderOmniboxActionMenu renders the elevated contextual action palette for
// the selected Omnibox row (toggled with ⌘K / Ctrl+K or the footer button).
func (a *AppController) renderOmniboxActionMenu(c *ui.Context, theme *ui.Theme, state engine.State) {
	item, ok := selectedOmniboxItem(state)
	if !ok {
		capsuleUI.setActionMenuShown(a, false)
		return
	}
	specs := engine.AvailableItemActions(item)
	if len(specs) == 0 {
		capsuleUI.setActionMenuShown(a, false)
		return
	}
	idx := capsuleUI.actionMenuSelection(a)
	if idx < 0 || idx >= len(specs) {
		idx = 0
		capsuleUI.setActionMenuSelection(a, idx)
	}

	card := ui.Column(c).
		FillWidth().
		Padding(7, 9).
		Gap(3).
		Radius(10).
		Background(theme.Background).
		Border(1, withAlpha(theme.Accent, 0.5)).
		Shadow(0, 10, 24, 0, ui.RGBA(0, 0, 0, 0.3))

	card.Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "command", 11, theme.Accent)
			ui.Text(c, "Actions").FontSize(11).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "↑↓ Select • ↵ Run • Esc Close").
				FontSize(10).
				TextColor(theme.TextMuted).
				SingleLine()
		})

		for i, spec := range specs {
			specIdx := i
			action := spec
			row := ui.ButtonBase(c).
				Key(fmt.Sprintf("action-menu:%s", action.ID)).
				FillWidth().
				Padding(5, 8).
				Radius(6).
				FocusRing(false)
			if specIdx == idx {
				row.Background(withAlpha(theme.Accent, 0.18)).
					Border(1, withAlpha(theme.Accent, 0.4))
			}
			row.Children(func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "chevron.right", 10, theme.TextMuted)
					ui.Text(c, action.Label).FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
					if action.Shortcut != "" {
						ui.Text(c, adaptActionShortcut(action.Shortcut)).
							FontSize(10).
							FontWeight(600).
							TextColor(theme.TextMuted).
							SingleLine()
					}
				})
			})
			if row.Clicked() {
				capsuleUI.setActionMenuShown(a, false)
				a.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: action.ID})
			}
		}
	})
}

// renderOmniboxPreviewCard renders the right-hand split preview pane for the
// selected Omnibox row.
func (a *AppController) renderOmniboxPreviewCard(
	c *ui.Context,
	theme *ui.Theme,
	state engine.State,
	item engine.OmniboxItem,
) {
	card := ui.Column(c).
		Fill().
		Padding(9, 10).
		Gap(6).
		Radius(10).
		Background(theme.Surface).
		Border(1, withAlpha(theme.Border, 0.85))

	card.Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, previewIcon(item), 12, theme.Accent)
			ui.Text(c, "Preview").FontSize(11).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)
			ui.Text(c, previewKindLabel(item)).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		})
		ui.Divider(c)

		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(5).Children(func() {
				a.renderOmniboxPreviewBody(c, theme, state, item)
				a.renderPreviewActionHints(c, theme, item)
			})
		})
	})
}

func (a *AppController) renderOmniboxPreviewBody(
	c *ui.Context,
	theme *ui.Theme,
	state engine.State,
	item engine.OmniboxItem,
) {
	switch item.Kind {
	case engine.ItemClipboardEntry:
		content := item.ClipboardContent
		if content == "" {
			content = item.Title
		}
		source := strings.TrimSpace(item.Subtitle)
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "clipboard", source, false)
			if item.KeyHint == "Pinned" {
				previewBadge(c, theme, "pin.fill", "Pinned", true)
			}
		})
		ui.Text(c, fmt.Sprintf("%d chars • %d lines",
			utf8.RuneCountInString(content), previewLineCount(content))).
			FontSize(10).
			TextColor(theme.TextMuted).
			SingleLine()
		renderPreviewLines(c, theme, content, 200)

	case engine.ItemSnippet:
		snippet := item.Snippet
		if snippet == nil {
			renderPreviewLines(c, theme, item.Title, 8)
			return
		}
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "text.badge.plus", snippet.Trigger, true)
			bounds := "Word boundary off"
			if snippet.WordBoundary {
				bounds = "Word boundary on"
			}
			ui.Text(c, bounds).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		})
		scope := "Any app"
		if len(snippet.AppBundleIDs) > 0 {
			scope = strings.Join(snippet.AppBundleIDs, ", ")
		}
		ui.Text(c, "Scope: "+scope).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		ui.Text(c, "Template").FontSize(10).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		renderPreviewLines(c, theme, snippet.Content, 24)
		rendered := engine.RenderSnippet(snippet.Content, engine.RenderOptions{
			TriggerLength: utf8.RuneCountInString(snippet.Trigger),
			Now:           time.Now(),
			ClipboardText: state.Context.ClipboardPreview,
			AppName:       state.Context.AppName,
		})
		ui.Text(c, "Rendered").FontSize(10).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		renderPreviewLines(c, theme, rendered.RenderedText, 24)

	case engine.ItemGlossaryTerm:
		term := item.GlossaryTerm
		if term == nil {
			renderPreviewLines(c, theme, item.Title, 8)
			return
		}
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, term.Term).FontSize(13).FontWeight(700).TextColor(theme.Text).SingleLine()
			if term.Category != "" {
				previewBadge(c, theme, "tag", term.Category, true)
			}
		})
		aliases := "None"
		if len(term.Aliases) > 0 {
			aliases = strings.Join(term.Aliases, ", ")
		}
		ui.Text(c, "Aliases: "+aliases).FontSize(11).TextColor(theme.Text).SingleLine()
		if term.Hint != "" {
			ui.Text(c, term.Hint).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		}

	case engine.ItemAIMode:
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			mode := item.AIMode
			if mode == "" {
				mode = "ask"
			}
			previewBadge(c, theme, "sparkles", "mode: "+mode, true)
		})
		ui.Text(c, "Prompt").FontSize(10).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		prompt := item.AIPrompt
		if prompt == "" {
			prompt = item.Title
		}
		renderPreviewLines(c, theme, prompt, 32)

	case engine.ItemUtility:
		ui.Text(c, "Value").FontSize(10).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		renderPreviewLines(c, theme, item.UtilityValue, 16)

	case engine.ItemAppIMERule:
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "keyboard", "IME "+string(item.IMEMode), true)
		})
		ui.Text(c, item.Title).FontSize(12).TextColor(theme.Text).SingleLine()
		if item.Subtitle != "" {
			ui.Text(c, item.Subtitle).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		}

	case engine.ItemInstalledApp:
		if bmp := a.AppIconBitmap(item.AppPath); bmp != nil {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Image(c, bmp).Size(44, 44).Radius(9)
			})
		}
		ui.Text(c, item.Title).FontSize(13).FontWeight(700).TextColor(theme.Text).SingleLine()
		if item.AppBundleID != "" {
			ui.Text(c, item.AppBundleID).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		}
		if item.AppPath != "" {
			renderPreviewLines(c, theme, item.AppPath, 4)
		}

	default:
		renderPreviewLines(c, theme, item.Subtitle, 8)
	}
}

// renderPreviewActionHints lists the primary and secondary contextual actions.
func (a *AppController) renderPreviewActionHints(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem) {
	specs := engine.AvailableItemActions(item)
	if len(specs) == 0 {
		return
	}
	shown := specs
	if len(shown) > 2 {
		shown = shown[:2]
	}
	ui.Divider(c)
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		for _, spec := range shown {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, adaptActionShortcut(spec.Shortcut)).
					FontSize(10).
					FontWeight(700).
					TextColor(theme.Accent).
					SingleLine()
				ui.Text(c, spec.Label).FontSize(10).TextColor(theme.TextMuted).SingleLine().Grow(1)
			})
		}
	})
}

func previewBadge(c *ui.Context, theme *ui.Theme, iconName, label string, accent bool) {
	fg := theme.TextMuted
	bg := theme.Background
	border := withAlpha(theme.Border, 0.8)
	if accent {
		fg = theme.Accent
		bg = withAlpha(theme.Accent, 0.14)
		border = withAlpha(theme.Accent, 0.4)
	}
	ui.Box(c).Padding(1, 6).Radius(4).Background(bg).Border(1, border).Children(func() {
		ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
			if iconName != "" {
				renderSemanticIcon(c, iconName, 9, fg)
			}
			ui.Text(c, label).FontSize(10).FontWeight(600).TextColor(fg).SingleLine()
		})
	})
}

// renderPreviewLines renders text line by line so long, multi-line content can
// scroll inside the preview pane.
func renderPreviewLines(c *ui.Context, theme *ui.Theme, text string, limit int) {
	lines := previewLines(text, limit)
	if len(lines) == 0 {
		ui.Text(c, "—").FontSize(11).TextColor(theme.TextMuted).SingleLine()
		return
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		ui.Text(c, line).FontSize(11).TextColor(theme.Text).SingleLine()
	}
}

func previewLines(text string, limit int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if limit > 0 && len(lines) > limit {
		lines = append(lines[:limit:limit], "…")
	}
	return lines
}

func previewLineCount(text string) int {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func previewKindLabel(item engine.OmniboxItem) string {
	switch item.Kind {
	case engine.ItemClipboardEntry:
		return "Clipboard"
	case engine.ItemSnippet:
		return "Snippet"
	case engine.ItemGlossaryTerm:
		return "Glossary"
	case engine.ItemAIMode:
		return "AI Mode"
	case engine.ItemUtility:
		return "Utility"
	case engine.ItemAppIMERule:
		return "IME Rule"
	case engine.ItemInstalledApp:
		return "App"
	default:
		return "Item"
	}
}

func previewIcon(item engine.OmniboxItem) string {
	if item.Icon != "" {
		return item.Icon
	}
	switch item.Kind {
	case engine.ItemClipboardEntry:
		return "doc.on.clipboard"
	case engine.ItemSnippet:
		return "text.badge.plus"
	case engine.ItemGlossaryTerm:
		return "character.book.closed"
	case engine.ItemAIMode:
		return "sparkles"
	case engine.ItemInstalledApp:
		return "app.badge"
	case engine.ItemAppIMERule:
		return "keyboard"
	default:
		return "bolt"
	}
}
