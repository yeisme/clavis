package uiapp

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
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
	aiStepsExpanded   map[*AppController]bool
	aiReviseOpen      map[*AppController]bool
}

var capsuleUI = &capsuleEphemeralStore{
	previewVisible:    make(map[*AppController]bool),
	actionMenuOpen:    make(map[*AppController]bool),
	actionMenuIdx:     make(map[*AppController]int),
	followUpComposing: make(map[*AppController]bool),
	aiStepsExpanded:   make(map[*AppController]bool),
	aiReviseOpen:      make(map[*AppController]bool),
}

func (s *capsuleEphemeralStore) reset(ctrl *AppController) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.previewVisible, ctrl)
	delete(s.actionMenuOpen, ctrl)
	delete(s.actionMenuIdx, ctrl)
	delete(s.followUpComposing, ctrl)
	delete(s.aiStepsExpanded, ctrl)
	delete(s.aiReviseOpen, ctrl)
}

func (s *capsuleEphemeralStore) stepsExpanded(ctrl *AppController) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aiStepsExpanded[ctrl]
}

func (s *capsuleEphemeralStore) toggleStepsExpanded(ctrl *AppController) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := !s.aiStepsExpanded[ctrl]
	if next {
		s.aiStepsExpanded[ctrl] = true
	} else {
		delete(s.aiStepsExpanded, ctrl)
	}
	return next
}

func (s *capsuleEphemeralStore) reviseOpen(ctrl *AppController) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aiReviseOpen[ctrl]
}

func (s *capsuleEphemeralStore) setReviseOpen(ctrl *AppController, open bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if open {
		s.aiReviseOpen[ctrl] = true
		return
	}
	delete(s.aiReviseOpen, ctrl)
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

// previewableOmniboxItem reports whether a given row has rich previewable content.
func previewableOmniboxItem(item engine.OmniboxItem) (engine.OmniboxItem, bool) {
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

// omniboxPreviewItem reports the selected row when it has rich previewable content.
func omniboxPreviewItem(state engine.State) (engine.OmniboxItem, bool) {
	item, ok := selectedOmniboxItem(state)
	if !ok {
		return engine.OmniboxItem{}, false
	}
	return previewableOmniboxItem(item)
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

	card := ui.Column(c).FillWidth().Padding(space0, space0).Gap(space6).Background(ui.Transparent).Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			// Neutral: the card's title icon is a label, not a selection.
			renderSemanticIcon(c, previewIcon(item), typeLabel, secondaryInk(theme))
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
		card.Background(mixColor(theme.Accent.Alpha(alphaStrong), theme.Background, 1-glow))
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
		source := strings.TrimSpace(item.ClipboardSourceApp)
		if source != "" || item.KeyHint == engine.KeyHintPinned {
			ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
				if source != "" {
					previewBadge(c, theme, "macwindow", source, false)
				}
				if item.KeyHint == engine.KeyHintPinned {
					previewBadge(c, theme, "pin.fill", i18n.T("common.pinned"), false)
				}
			})
		}
		if col, ok := parseHexSwatch(content); ok {
			renderColorPreviewCard(c, theme, content, col)
			return
		}
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
		if isAgentItem(item) {
			a.renderAgentPreviewBody(c, theme, item)
			return
		}
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

// renderPreviewActionHints lists the primary and secondary contextual actions
// on one compact row so the preview keeps its vertical budget for content.
func (a *AppController) renderPreviewActionHints(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem) {
	if isAgentItem(item) {
		hints := agentItemFooterHints(item, a.agentRefreshRetryable())
		if len(hints) == 0 {
			return
		}
		ui.Divider(c)
		ui.Row(c).FillWidth().Wrap().Gap(space12).AlignItems(ui.Center).Children(func() {
			for _, h := range hints {
				keyHint(c, theme, h.keys, h.caption, keyToneNeutral)
			}
		})
		return
	}
	specs := engine.AvailableItemActions(item)
	if len(specs) == 0 {
		return
	}
	shown := specs
	if len(shown) > 2 {
		shown = shown[:2]
	}
	ui.Divider(c)
	ui.Row(c).FillWidth().Wrap().Gap(space12).AlignItems(ui.Center).Children(func() {
		for _, spec := range shown {
			keyHint(c, theme, adaptActionShortcut(spec.Shortcut), spec.Label, keyToneNeutral)
		}
	})
}

func (a *AppController) renderAgentPreviewBody(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem) {
	sessions, _ := a.cachedAgentData()
	if session, ok := agentSessionForItem(item, sessions); ok {
		ui.Text(c, session.Title).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).Wrap()
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			if provider := agentProviderLabel(session.Provider); provider != "" {
				previewBadge(c, theme, "person.crop.circle", provider, false)
			}
			statusColor := kitStateEmptyColor(theme)
			running := session.Status == agentsession.StatusRunning
			if running {
				statusColor = kitStateLoadingColor(theme)
			}
			ui.Box(c).Padding(space1, space6).Radius(radius4).Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					kitStatusDot(c, statusColor, running)
					ui.Text(c, agentStatusLabel(session.Status)).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
				})
			})
			if session.PendingReview {
				pendingLabel := "Pending review"
				if i18n.IsChinese() {
					pendingLabel = "待验收"
				}
				previewBadge(c, theme, "checkmark", pendingLabel, false)
			}
			previewBadge(c, theme, "", agentRelativeTime(session.LastActivityAt, time.Now()), false)
		})
		if ws := strings.TrimSpace(session.Workspace); ws != "" {
			wsLabel := "Workspace"
			if i18n.IsChinese() {
				wsLabel = "工作区"
			}
			ui.Text(c, wsLabel).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, agentsession.SanitizeDiagnosticText(ws)).Font("monospace").FontSize(typeMicro).TextColor(theme.Text).Wrap()
		}
		if id := strings.TrimSpace(session.ID); id != "" {
			ui.Text(c, "ID: "+id).Font("monospace").FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
		if msg := strings.TrimSpace(agentsession.SanitizeDiagnosticText(session.LastMessage)); msg != "" {
			msgLabel := "Latest message"
			if i18n.IsChinese() {
				msgLabel = "最近消息"
			}
			ui.Text(c, msgLabel).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			kitInsetWell(c, theme, func() {
				renderPreviewLines(c, theme, msg, 12)
			})
		}
		return
	}

	if strings.HasPrefix(item.UtilityValue, "agent-command:") {
		cmdLabel := "Command"
		if i18n.IsChinese() {
			cmdLabel = "快捷命令"
		}
		ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "command", cmdLabel, false)
		})
		ui.Text(c, item.Title).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).Wrap()
		if item.Subtitle != "" {
			kitInsetWell(c, theme, func() {
				renderPreviewLines(c, theme, item.Title+"\n"+item.Subtitle, 8)
			})
		}
		return
	}

	if item.UtilityValue == "agent-refresh-status" {
		res := a.agentRecordedOperationResult()
		statusBadge := "Status"
		if i18n.IsChinese() {
			statusBadge = "同步状态"
		}
		ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "info.circle", statusBadge, false)
		})
		ui.Text(c, item.Title).FontSize(typeBody).FontWeight(600).TextColor(theme.Text).Wrap()
		if item.Subtitle != "" {
			ui.Text(c, item.Subtitle).FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
		}
		if res.State == host.StateFailed && res.Message != "" {
			kitInsetWell(c, theme, func() {
				renderPreviewLines(c, theme, agentsession.SanitizeDiagnosticText(res.Message), 8)
			})
		}
		return
	}

	diagBadge := "Diagnostic"
	if i18n.IsChinese() {
		diagBadge = "数据源诊断"
	}
	ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
		previewBadge(c, theme, "info.circle", diagBadge, false)
	})
	ui.Text(c, item.Title).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).SingleLine()
	if item.Subtitle != "" {
		kitInsetWell(c, theme, func() {
			renderPreviewLines(c, theme, item.Subtitle, 8)
		})
	}
}

func agentItemFooterHints(item engine.OmniboxItem, retryable bool) []footerHint {
	zh := i18n.IsChinese()
	refreshCaption := "Refresh"
	if zh {
		refreshCaption = "刷新"
	}
	var hints []footerHint
	switch {
	case strings.HasPrefix(item.UtilityValue, "agent-session:"):
		cap := "Focus / Copy ID"
		if zh {
			cap = "跳转 / 复制 ID"
		}
		hints = append(hints, footerHint{keys: "↵", caption: cap, drop: 0})
	case strings.HasPrefix(item.UtilityValue, "agent-command:"):
		cap := "Copy prompt"
		if zh {
			cap = "复制命令"
		}
		hints = append(hints, footerHint{keys: "↵", caption: cap, drop: 0})
	case item.UtilityValue == "agent-refresh-status" && retryable:
		cap := "Retry"
		if zh {
			cap = "重试"
		}
		hints = append(hints, footerHint{keys: "↵", caption: cap, drop: 0})
	}
	hints = append(hints, footerHint{keys: modPrefix() + "R", caption: refreshCaption, drop: 2})
	return hints
}

func previewBadge(c *ui.Context, theme *ui.Theme, iconName, label string, _ bool) {
	// Passive metadata badges stay strictly neutral gray (clavis-visual-spec.md §0:
	// system blue is reserved for selection, focus, actionable feedback, and running state).
	fg := secondaryInk(theme)
	bg := tileFill(theme)
	border := hairline(theme)
	ui.Box(c).Padding(space1, space6).Radius(radius4).Background(bg).Border(space1, border).Children(func() {
		ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
			if iconName != "" {
				renderSemanticIcon(c, iconName, typeMicro-1, fg)
			}
			ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
		})
	})
}

func renderColorPreviewCard(c *ui.Context, theme *ui.Theme, raw string, col ui.Color) {
	ui.Box(c).FillWidth().Height(88).Radius(radiusCard).Background(col).Border(space1, hairline(theme))
	hexStr := strings.ToUpper(strings.TrimSpace(raw))
	rgbStr := fmt.Sprintf("rgb(%d, %d, %d)", col.R, col.G, col.B)
	if col.A < 255 {
		rgbStr = fmt.Sprintf("rgba(%d, %d, %d, %.2f)", col.R, col.G, col.B, float64(col.A)/255.0)
	}
	h, s, l := colorToHSL(col)
	hslStr := fmt.Sprintf("hsl(%d°, %d%%, %d%%)", h, s, l)
	kitInsetWell(c, theme, func() {
		colorFormatRow := func(label, value string) {
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).Width(36).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
				ui.Text(c, value).Font("monospace").FontSize(typeMeta).TextColor(theme.Text).SingleLine().Grow(1)
			})
		}
		colorFormatRow("HEX", hexStr)
		colorFormatRow("RGB", rgbStr)
		colorFormatRow("HSL", hslStr)
	})
}

func colorToHSL(col ui.Color) (h, s, l int) {
	r := float64(col.R) / 255.0
	g := float64(col.G) / 255.0
	b := float64(col.B) / 255.0
	maxV := math.Max(r, math.Max(g, b))
	minV := math.Min(r, math.Min(g, b))
	lf := (maxV + minV) / 2.0
	var hf, sf float64
	if maxV != minV {
		d := maxV - minV
		if lf > 0.5 {
			sf = d / (2.0 - maxV - minV)
		} else {
			sf = d / (maxV + minV)
		}
		switch maxV {
		case r:
			hf = (g - b) / d
			if g < b {
				hf += 6
			}
		case g:
			hf = (b-r)/d + 2
		case b:
			hf = (r-g)/d + 4
		}
		hf *= 60
	}
	return int(math.Round(hf)), int(math.Round(sf * 100)), int(math.Round(lf * 100))
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
	mono := previewIsMono(text)
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
	// Cut only the lines that are shown and count the rest: the preview is
	// rebuilt every frame, and splitting a 256 KiB log into all its lines
	// (after copying it to normalize CRLF) was most of the frame.
	rest := text
	for strings.HasSuffix(rest, "\n") {
		rest = strings.TrimSuffix(strings.TrimSuffix(rest, "\n"), "\r")
	}
	for limit <= 0 || len(lines) < limit {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			lines = append(lines, rest)
			return lines, 0
		}
		lines = append(lines, strings.TrimSuffix(rest[:i], "\r"))
		rest = rest[i+1:]
	}
	return lines, strings.Count(rest, "\n") + 1
}

// previewIsMono reports whether a preview reads as code, a command or a path.
// Like clipboardContentIcon it reads only the head: it runs every frame, and
// lowercasing a whole 256 KiB entry here was the largest allocation of a
// selection change (BenchmarkClipboardSwitch/large).
func previewIsMono(text string) bool {
	trimmed := strings.TrimSpace(text)
	head, truncated := trimmed, len(trimmed) > clipboardSniffBytes
	if truncated {
		head = trimmed[:clipboardSniffBytes]
	}
	return looksLikeCode(head) || looksLikeShellCommand(strings.ToLower(head)) || (!truncated && looksLikePath(head))
}

// previewLineCount counts lines as a split on LF would (a trailing newline
// starts an empty last line); CRLF holds one LF, so no normalized copy.
func previewLineCount(text string) int {
	return strings.Count(text, "\n") + 1
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
		if isAgentItem(item) {
			if strings.HasPrefix(item.UtilityValue, "agent-command:") {
				if i18n.IsChinese() {
					return "Agent 命令"
				}
				return "Agent Command"
			}
			if strings.HasPrefix(item.UtilityValue, "agent-diagnostic") {
				if i18n.IsChinese() {
					return "Agent 诊断"
				}
				return "Agent Diagnostic"
			}
			return "Agent"
		}
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
		return fallbackActionIcon(item.Icon, item.Title, item.Subtitle)
	}
}

func fallbackActionIcon(explicit, title, subtitle string) string {
	if name := strings.ToLower(strings.TrimSpace(explicit)); name != "" {
		if _, ok := semanticIconBodies[name]; ok {
			return name
		}
	}
	text := strings.ToLower(strings.TrimSpace(title + " " + subtitle))
	switch {
	case strings.Contains(text, "window") || strings.Contains(text, "窗口"):
		return "macwindow"
	case strings.Contains(text, "ai") || strings.Contains(text, "rewrite") || strings.Contains(text, "改写"):
		return "sparkles"
	case strings.Contains(text, "clipboard") || strings.Contains(text, "剪贴板"):
		return "doc.on.clipboard"
	case strings.Contains(text, "snippet") || strings.Contains(text, "片段"):
		return "curlybraces"
	case strings.Contains(text, "ime") || strings.Contains(text, "输入法") || strings.Contains(text, "词库"):
		return "character.book.closed"
	case strings.Contains(text, "setting") || strings.Contains(text, "设置"):
		return "gearshape"
	case strings.Contains(text, "http") || strings.Contains(text, "url") || strings.Contains(text, "链接"):
		return "globe"
	case strings.Contains(text, "search") || strings.Contains(text, "搜索"):
		return "search"
	case strings.Contains(text, "copy") || strings.Contains(text, "复制"):
		return "doc.on.doc"
	case strings.Contains(text, "translat") || strings.Contains(text, "翻译"):
		return "globe"
	case strings.Contains(text, "delete") || strings.Contains(text, "删除"):
		return "trash"
	case strings.Contains(text, "terminal") || strings.Contains(text, "shell"):
		return "terminal"
	default:
		return "command"
	}
}

// renderClipboardMediaPreview shows the thumbnail and metadata for image/file/video entries.
func (a *AppController) renderClipboardMediaPreview(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem, m engine.ClipboardMedia) {
	a.resetMediaPreview(item)
	source := strings.TrimSpace(item.ClipboardSourceApp)
	if source == "" {
		source = strings.TrimSpace(item.Subtitle)
	}
	meta := clipboardMediaMetaRows(m)
	ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
		if source != "" {
			previewBadge(c, theme, "macwindow", source, false)
		}
		if item.KeyHint == engine.KeyHintPinned {
			previewBadge(c, theme, "pin.fill", i18n.T("common.pinned"), false)
		}
		for _, row := range meta {
			previewBadge(c, theme, "", row, false)
		}
	})
	if m.Kind == "image" || m.Kind == "video" {
		a.renderClipboardMediaToolbar(c, theme, m)
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
		if len(m.FilePaths) > 0 {
			renderPreviewLines(c, theme, strings.Join(m.FilePaths, "\n"), 20)
		}
	}).Grow(1).MinHeight(0)
}
