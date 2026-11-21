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
	"github.com/yeisme/clavis/pkg/core/i18n"
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
		engine.ItemInstalledApp,
		engine.ItemWorkflow:
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
		Padding(space6, space8).
		Gap(3).
		Radius(radiusCard).
		Background(theme.Background).
		Border(1, withAlpha(theme.Accent, alphaHalf)).
		Shadow(0, 10, 24, 0, ui.RGBA(0, 0, 0, 0.3))

	card.Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "command", 11, theme.Accent)
			ui.Text(c, i18n.T("omnibox.tab.actions")).FontSize(typeMeta).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)
			ui.Text(c, i18n.T("preview.menu_hint")).
				FontSize(typeMicro).
				TextColor(theme.TextMuted).
				SingleLine()
		})

		for i, spec := range specs {
			specIdx := i
			action := spec
			row := ui.ButtonBase(c).
				Key(fmt.Sprintf("action-menu:%s", action.ID)).
				FillWidth().
				Padding(space4, space8).
				Radius(radiusChip).
				FocusRing(false)
			if specIdx == idx {
				row.Background(withAlpha(theme.Accent, alphaSubtle)).
					Border(1, withAlpha(theme.Accent, alphaHalf))
			}
			row.Children(func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "chevron.right", 10, theme.TextMuted)
					ui.Text(c, action.Label).FontSize(typeLabel).TextColor(theme.Text).SingleLine().Grow(1)
					if action.Shortcut != "" {
						ui.Text(c, adaptActionShortcut(action.Shortcut)).
							FontSize(typeMicro).
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
	profile := appMotionProfile(c, a.Config().Main.Appearance)

	// Snippet-expansion flash: landing on a snippet row highlights its
	// preview for a beat (120ms) as the rendered content expands.
	flashRun := uint64(0)
	flashFrames := []ui.Keyframe{}
	if item.Kind == engine.ItemSnippet && item.Snippet != nil && profile.flash > 0 {
		if a.uiState.lastPreviewSnippet != item.Snippet.ID {
			a.uiState.lastPreviewSnippet = item.Snippet.ID
			a.uiState.snippetFlashRun++
		}
		flashRun = a.uiState.snippetFlashRun
		flashFrames = []ui.Keyframe{{At: 0, Value: 1}, {At: profile.flash, Value: 0, Ease: ui.EaseOut}}
	}

	card := kitCard(c, theme, cardElevated, func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, previewIcon(item), typeLabel, theme.Accent)
			ui.Text(c, i18n.T("preview.title")).FontSize(typeMeta).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)
			ui.Text(c, previewKindLabel(item)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		})
		kitDivider(c, theme)
		// Text scrolls inside the card. Media does not: its canvas gives up
		// height (down to mediaCanvasMinHeight) so the metadata and action
		// hints stay in view, instead of a fixed 300px canvas pushing the card
		// past the list and over the footer.
		if m := item.ClipboardMedia; m != nil && m.IsMedia() {
			ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(space4).Children(func() {
				a.renderOmniboxPreviewBody(c, theme, state, item)
				a.renderPreviewActionHints(c, theme, item)
			})
			return
		}
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(space4).Children(func() {
				a.renderOmniboxPreviewBody(c, theme, state, item)
				a.renderPreviewActionHints(c, theme, item)
			})
		})
	})
	// The card is bounded by its column (the list's height); without this it
	// takes its content's height and overflows onto the footer.
	card.Grow(1).MinHeight(0)
	// Preserve the snippet flash as a border animation on the elevated card.
	if len(flashFrames) == 2 {
		glow := card.Keyframes("snippet-flash", flashFrames, ui.KeyframeOptions{Run: flashRun, NonMotion: true})
		card.Border(space1, mixColor(theme.Accent.Alpha(alphaStrong), hairline(theme), 1-glow))
	}

}

func (a *AppController) renderOmniboxPreviewBody(
	c *ui.Context,
	theme *ui.Theme,
	state engine.State,
	item engine.OmniboxItem,
) {
	switch item.Kind {
	case engine.ItemClipboardEntry:
		if m := item.ClipboardMedia; m != nil && m.IsMedia() {
			a.renderClipboardMediaPreview(c, theme, item, *m)
			return
		}
		content := item.ClipboardContent
		if content == "" {
			content = item.Title
		}
		source := strings.TrimSpace(item.Subtitle)
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "clipboard", source, false)
			if item.KeyHint == engine.KeyHintPinned {
				previewBadge(c, theme, "pin.fill", i18n.T("common.pinned"), true)
			}
		})
		ui.Text(c, i18n.Tf("preview.clipboard_stats", utf8.RuneCountInString(content), previewLineCount(content))).
			FontSize(typeMicro).
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
			bounds := i18n.T("preview.word_boundary_off")
			if snippet.WordBoundary {
				bounds = i18n.T("preview.word_boundary_on")
			}
			ui.Text(c, bounds).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		})
		scope := i18n.T("preview.any_app")
		if len(snippet.AppBundleIDs) > 0 {
			scope = strings.Join(snippet.AppBundleIDs, ", ")
		}
		kitInsetWell(c, theme, func() {
			ui.Text(c, i18n.Tf("preview.scope", scope)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, i18n.T("preview.template")).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			renderPreviewLines(c, theme, snippet.Content, 24)
			rendered := engine.RenderSnippet(snippet.Content, engine.RenderOptions{
				TriggerLength: utf8.RuneCountInString(snippet.Trigger),
				Now:           time.Now(),
				ClipboardText: state.Context.ClipboardPreview,
				AppName:       state.Context.AppName,
			})
			ui.Text(c, i18n.T("preview.rendered")).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			renderPreviewLines(c, theme, rendered.RenderedText, 24)
		})

	case engine.ItemGlossaryTerm:
		term := item.GlossaryTerm
		if term == nil {
			renderPreviewLines(c, theme, item.Title, 8)
			return
		}
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, term.Term).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).SingleLine()
			if term.Category != "" {
				previewBadge(c, theme, "tag", term.Category, true)
			}
		})
		aliases := i18n.T("common.none")
		if len(term.Aliases) > 0 {
			aliases = strings.Join(term.Aliases, ", ")
		}
		ui.Text(c, i18n.Tf("preview.aliases", aliases)).FontSize(typeMeta).TextColor(theme.Text).SingleLine()
		if term.Hint != "" {
			ui.Text(c, term.Hint).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}

	case engine.ItemAIMode:
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "sparkles", i18n.Tf("preview.ai_mode", aiModeLabel(item.AIMode)), true)
		})
		ui.Text(c, i18n.T("preview.prompt")).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		prompt := item.AIPrompt
		if prompt == "" {
			prompt = item.Title
		}
		renderPreviewLines(c, theme, prompt, 32)

	case engine.ItemUtility:
		ui.Text(c, i18n.T("preview.value")).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		renderPreviewLines(c, theme, item.UtilityValue, 16)

	case engine.ItemAppIMERule:
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "keyboard", i18n.Tf("preview.ime_mode", string(item.IMEMode)), true)
		})
		ui.Text(c, item.Title).FontSize(typeLabel).TextColor(theme.Text).SingleLine()
		if item.Subtitle != "" {
			ui.Text(c, item.Subtitle).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}

	case engine.ItemWorkflow:
		workflow := item.Workflow
		if workflow == nil {
			renderPreviewLines(c, theme, item.Subtitle, 8)
			return
		}
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "arrow.triangle.branch", "Safe workflow", true)
			previewBadge(c, theme, fmt.Sprintf("%d steps", len(workflow.Steps)), "", false)
		})
		if workflow.Description != "" {
			renderPreviewLines(c, theme, workflow.Description, 8)
		}
		kitInsetWell(c, theme, func() {
			ui.Text(c, "Execution plan").FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			for _, step := range workflow.Steps {
				if !step.Enabled {
					continue
				}
				label := step.Name
				if label == "" {
					label = step.ID
				}
				ui.Text(c, fmt.Sprintf("• %s  (%s)", label, step.Action)).FontSize(typeMeta).TextColor(theme.Text).Wrap()
			}
		})

	case engine.ItemInstalledApp:
		if bmp := a.AppIconBitmap(item.AppPath); bmp != nil {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Image(c, bmp).Size(44, 44).Radius(radiusTile)
			})
		}
		ui.Text(c, item.Title).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).SingleLine()
		if item.AppBundleID != "" {
			ui.Text(c, item.AppBundleID).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
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
	ui.Column(c).FillWidth().Gap(space4).Children(func() {
		for _, spec := range shown {
			keyHint(c, theme, adaptActionShortcut(spec.Shortcut), spec.Label, keyToneNeutral)
		}
	})
}

func previewBadge(c *ui.Context, theme *ui.Theme, iconName, label string, accent bool) {
	fg := theme.TextMuted
	bg := theme.Background
	border := withAlpha(theme.Border, alphaStrong)
	if accent {
		fg = theme.Accent
		bg = withAlpha(theme.Accent, alphaSubtle)
		border = withAlpha(theme.Accent, alphaHalf)
	}
	ui.Box(c).Padding(space1, space6).Radius(radius4).Background(bg).Border(space1, border).Children(func() {
		ui.Row(c).Gap(space2).AlignItems(ui.Center).Children(func() {
			if iconName != "" {
				renderSemanticIcon(c, iconName, typeMicro-1, fg)
			}
			ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
		})
	})
}

// renderPreviewLines renders text line by line so long, multi-line content can
// scroll inside the preview pane.
func renderPreviewLines(c *ui.Context, theme *ui.Theme, text string, limit int) {
	lines, hidden := previewLines(text, limit)
	if len(lines) == 0 {
		ui.Text(c, "—").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		return
	}
	// Code, commands and paths keep their shape in the monospace face on a
	// faint well; prose wraps with the wider CJK leading.
	trimmed := strings.TrimSpace(text)
	mono := looksLikeCode(text) || looksLikeShellCommand(strings.ToLower(trimmed)) || looksLikePath(trimmed)
	body := ui.Column(c).FillWidth().Gap(space2)
	if mono {
		body.Padding(space6, space8).Radius(radiusChip).Background(tileFill(theme)).Border(1, hairline(theme))
	}
	body.Children(func() {
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				if mono {
					ui.Box(c).Height(space6)
				}
				continue
			}
			// Lines wrap at the pane width: a long URL or SQL statement reads
			// in full instead of being cut to its first 40 characters.
			t := ui.Text(c, line).TextColor(theme.Text).Wrap()
			if mono {
				t.Font("monospace").FontSize(typeMeta)
			} else {
				applyProseLineHeight(t).FontSize(typeLabel)
			}
		}
		if hidden > 0 {
			ui.Text(c, i18n.Tf("preview.more_lines", hidden)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
	})
}

// previewLines splits text into at most limit lines (limit <= 0: all) and
// reports how many further lines were left out.
func previewLines(text string, limit int) (lines []string, hidden int) {
	if strings.TrimSpace(text) == "" {
		return nil, 0
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines = strings.Split(strings.TrimRight(normalized, "\n"), "\n")
	if limit > 0 && len(lines) > limit {
		hidden = len(lines) - limit
		lines = lines[:limit:limit]
	}
	return lines, hidden
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
		return i18n.T("preview.kind.clipboard")
	case engine.ItemSnippet:
		return i18n.T("preview.kind.snippet")
	case engine.ItemGlossaryTerm:
		return i18n.T("preview.kind.glossary")
	case engine.ItemAIMode:
		return i18n.T("preview.kind.ai")
	case engine.ItemUtility:
		return i18n.T("preview.kind.utility")
	case engine.ItemAppIMERule:
		return i18n.T("preview.kind.ime_rule")
	case engine.ItemInstalledApp:
		return i18n.T("preview.kind.app")
	case engine.ItemWorkflow:
		return "Workflow"
	default:
		return i18n.T("preview.kind.item")
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
	case engine.ItemWorkflow:
		return "arrow.triangle.branch"
	default:
		return "bolt"
	}
}

// renderClipboardMediaPreview shows the thumbnail and metadata for image/file/video entries.
func (a *AppController) renderClipboardMediaPreview(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem, m engine.ClipboardMedia) {
	a.resetMediaPreview(item)
	source := strings.TrimSpace(item.Subtitle)
	ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
		previewBadge(c, theme, item.Icon, source, false)
		if item.KeyHint == engine.KeyHintPinned {
			previewBadge(c, theme, "pin.fill", i18n.T("common.pinned"), true)
		}
	})
	if m.Kind == "image" || m.Kind == "video" {
		a.renderClipboardMediaToolbar(c, theme)
	}
	kitInsetWell(c, theme, func() {
		bmp := a.ThumbBitmap(m.ThumbPath)
		if (m.Kind == "image" || m.Kind == "video") && len(m.FilePaths) <= 1 {
			a.renderClipboardMediaCanvas(c, theme, m)
		}
		if len(m.FilePaths) > 1 {
			CoverFlowDeck3D(c, len(m.FilePaths), 0, effectiveSpatialAppearance(c, a.Config().Main.Appearance), func(i int, active bool) {
				path := m.FilePaths[i]
				image := a.ThumbBitmap(path)
				if image == nil && i == 0 {
					image = bmp
				}
				if image != nil {
					ui.Image(c, image).Fill().Fit(ui.Contain).Radius(radiusChip)
				} else {
					ui.Text(c, path).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
				}
			})
		} else if bmp != nil && m.Kind != "image" && m.Kind != "video" {
			ui.Image(c, bmp).FillWidth().Height(mediaPreviewCanvasHeight(m)).MinHeight(mediaCanvasMinHeight).Fit(ui.Contain).Radius(radiusChip)
		} else if bmp == nil && m.Kind != "image" && m.Kind != "video" {
			ui.Text(c, i18n.T("preview.media.unavailable")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		}
		for _, row := range clipboardMediaMetaRows(m) {
			ui.Text(c, row).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
		if len(m.FilePaths) > 0 {
			renderPreviewLines(c, theme, strings.Join(m.FilePaths, "\n"), 20)
		}
	}).Grow(1).MinHeight(0)
}
