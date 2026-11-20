package uiapp

import (
	"maps"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/prompt"
)

type promptComposerUIState struct {
	rootHandle    ui.Handle
	slotHandles   []ui.Handle
	needFocusSync bool
	isComposing   bool
}

// PreferredPromptComposerPanelSize computes comfortable dimensions for the standalone
// prompt slot composer floating panel while honoring PreferredPromptComposerSize width bounds.
func PreferredPromptComposerPanelSize(slotCount int) (width, height int) {
	w, h := PreferredPromptComposerSize(slotCount, true)
	if w < 620 {
		w = 620
	}
	if slotCount < 1 {
		slotCount = 1
	}
	if slotCount > 6 {
		slotCount = 6
	}
	neededH := 280 + slotCount*56
	if neededH > h {
		h = neededH
	}
	if h > 560 {
		h = 560
	}
	return w, h
}

// OpenPromptComposer initializes a SlotComposerSession for tpl and triggers OnOpenPromptComposer.
func (c *AppController) OpenPromptComposer(tpl config.PromptTemplate, capCtx engine.CapsuleContext) {
	c.mu.Lock()
	glossary := c.Glossary
	c.mu.Unlock()

	var glossaryBlock string
	if tpl.InjectGlossary {
		terms := glossary.RelevantTerms(capCtx.SelectedText+"\n"+capCtx.WindowTitle+"\n"+tpl.Template, "", 0)
		glossaryBlock = glossary.FormatPromptBlock(terms)
	}

	renderCtx := prompt.RenderContext{
		Selection:        capCtx.SelectedText,
		Clipboard:        capCtx.ClipboardPreview,
		App:              capCtx.AppName,
		URL:              capCtx.ActiveURL,
		WindowTitle:      capCtx.WindowTitle,
		Glossary:         glossaryBlock,
		SelectedText:     capCtx.SelectedText,
		ClipboardPreview: capCtx.ClipboardPreview,
		AppName:          capCtx.AppName,
		ActiveURL:        capCtx.ActiveURL,
	}

	sess := prompt.NewSlotComposerSession(tpl, renderCtx)

	c.mu.Lock()
	c.promptSession = sess
	c.promptContext = capCtx
	c.promptValidationError = ""
	c.promptUI.slotHandles = make([]ui.Handle, len(sess.Slots))
	c.promptUI.needFocusSync = true
	c.promptUI.isComposing = false
	onOpen := c.OnOpenPromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onOpen != nil {
		onOpen()
	}
	if onRepaint != nil {
		onRepaint()
	}
}

// ClosePromptComposer clears any active slot composer session and fires OnClosePromptComposer.
func (c *AppController) ClosePromptComposer() {
	c.mu.Lock()
	c.promptSession = nil
	c.promptValidationError = ""
	c.promptUI.slotHandles = nil
	c.promptUI.needFocusSync = false
	c.promptUI.isComposing = false
	onClose := c.OnClosePromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onClose != nil {
		onClose()
	}
	if onRepaint != nil {
		onRepaint()
	}
}

// PromptComposerOpen reports whether a slot composer session is active.
func (c *AppController) PromptComposerOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.promptSession != nil
}

// PromptComposerSlotCount returns the number of slots in the active session.
func (c *AppController) PromptComposerSlotCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.promptSession == nil {
		return 0
	}
	return len(c.promptSession.Slots)
}

// PromptComposerActiveSlotIndex returns the active slot index in the active session.
func (c *AppController) PromptComposerActiveSlotIndex() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.promptSession == nil {
		return 0
	}
	return c.promptSession.ActiveSlotIndex
}

// PromptComposerValidationError returns the current validation error message, if any.
func (c *AppController) PromptComposerValidationError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.promptValidationError
}

// PromptComposerPreview returns the live-rendered preview of the active session.
func (c *AppController) PromptComposerPreview() prompt.PromptRenderResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.promptSession == nil {
		return prompt.PromptRenderResult{}
	}
	return c.promptSession.Preview()
}

// SetPromptSlotValue sets the value for the named slot and clears prior validation errors.
func (c *AppController) SetPromptSlotValue(name, value string) {
	c.mu.Lock()
	if c.promptSession == nil {
		c.mu.Unlock()
		return
	}
	if c.promptSession.Values == nil {
		c.promptSession.Values = map[string]string{}
	}
	c.promptSession.Values[name] = value
	c.promptValidationError = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onRepaint != nil {
		onRepaint()
	}
}

// PromptComposerHandleKey routes Tab, Shift+Tab, Left, and Right into SlotComposerSession.
func (c *AppController) PromptComposerHandleKey(key string) {
	c.mu.Lock()
	if c.promptSession == nil {
		c.mu.Unlock()
		return
	}
	prevIdx := c.promptSession.ActiveSlotIndex
	c.promptSession.HandleKey(key)
	if c.promptSession.ActiveSlotIndex != prevIdx {
		c.promptUI.needFocusSync = true
	}
	c.promptValidationError = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onRepaint != nil {
		onRepaint()
	}
}

// CommitPromptComposer validates required slots, and on success closes the composer and
// injects the rendered prompt at the caret into the frontmost app.
func (c *AppController) CommitPromptComposer() bool {
	c.mu.Lock()
	sess := c.promptSession
	if sess == nil {
		c.mu.Unlock()
		return false
	}
	res := sess.Preview()
	if len(res.MissingRequired) > 0 {
		msg := i18n.Tf("prompts.missing_required", strings.Join(res.MissingRequired, ", "))
		c.promptValidationError = msg
		onRepaint := c.OnRepaint
		c.mu.Unlock()
		c.ShowToastOrHUD(msg)
		if onRepaint != nil {
			onRepaint()
		}
		return false
	}

	rendered := res.RenderedText
	backOffset := res.CursorBackOffset
	tpl := sess.Template
	c.promptSession = nil
	c.promptValidationError = ""
	c.promptUI.slotHandles = nil
	c.promptUI.needFocusSync = false
	c.promptUI.isComposing = false
	onClose := c.OnClosePromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onClose != nil {
		onClose()
	}
	c.injectTextAsync(rendered, 0, backOffset)
	promptID := strings.TrimSpace(tpl.ID)
	if promptID == "" {
		promptID = strings.TrimSpace(tpl.Title)
	}
	c.recordPromptVersion(promptID, tpl.Trigger, tpl.Template, rendered, tpl.OptimizerPreset)
	if onRepaint != nil {
		onRepaint()
	}
	return true
}

// CopyPromptComposer validates required slots, and on success closes the composer and
// copies the rendered prompt to the clipboard.
func (c *AppController) CopyPromptComposer() bool {
	c.mu.Lock()
	sess := c.promptSession
	if sess == nil {
		c.mu.Unlock()
		return false
	}
	res := sess.Preview()
	if len(res.MissingRequired) > 0 {
		msg := i18n.Tf("prompts.missing_required", strings.Join(res.MissingRequired, ", "))
		c.promptValidationError = msg
		onRepaint := c.OnRepaint
		c.mu.Unlock()
		c.ShowToastOrHUD(msg)
		if onRepaint != nil {
			onRepaint()
		}
		return false
	}

	rendered := res.RenderedText
	c.promptSession = nil
	c.promptValidationError = ""
	c.promptUI.slotHandles = nil
	c.promptUI.needFocusSync = false
	c.promptUI.isComposing = false
	onClose := c.OnClosePromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onClose != nil {
		onClose()
	}
	c.writeClipboardText(rendered)
	c.ShowToastOrHUD(i18n.T("hud.copied"))
	if onRepaint != nil {
		onRepaint()
	}
	return true
}

// PromptComposerView renders the standalone slot-composer panel for the active prompt template.
func (a *AppController) PromptComposerView(c *ui.Context) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)
	prefs := c.Preferences()
	radius := NormalizeCornerRadius(cfg.Main.Appearance)
	profile := appMotionProfile(c, cfg.Main.Appearance)
	DisableAnimationsFor(c, profile)

	a.mu.Lock()
	sess := a.promptSession
	if sess == nil {
		a.mu.Unlock()
		ui.Box(c).Fill()
		return
	}
	tpl := sess.Template
	slots := append([]config.PromptSlot(nil), sess.Slots...)
	activeSlotIdx := sess.ActiveSlotIndex
	slotValues := maps.Clone(sess.Values)
	preview := sess.Preview()
	appCtx := a.promptContext
	validationErr := a.promptValidationError
	needFocusSync := a.promptUI.needFocusSync
	if len(a.promptUI.slotHandles) != len(slots) {
		a.promptUI.slotHandles = make([]ui.Handle, len(slots))
	}
	a.mu.Unlock()

	root := c.Root()
	if !prefs.ReduceTransparency {
		root.Radius(radius)
	}

	card := kitFloatingShell(c, theme, applyTextLocale(ui.Column(c).Fill().Radius(radius).Clip()), cfg.Main.Appearance)

	// Window-level primary+Enter commits from anywhere; Escape closes.
	c.OnWindowShortcut(primaryMod(), ui.KeyEnter, func() {
		a.CommitPromptComposer()
	})

	card.Children(func() {
		// Header: the shared dialog title bar. The template is the title, the
		// composer kind its secondary line; trigger and app are neutral meta.
		kitDialogHeader(c, theme, "wand.and.stars", tpl.Title, i18n.T("prompts.composer_title"), func() {
			if tpl.Trigger != "" {
				kitMetaChip(c, theme, tpl.Trigger)
			}
			if appCtx.AppName != "" {
				kitMetaChip(c, theme, appCtx.AppName)
			}
		})

		body := kitDialogBody(c)
		if len(slots) == 0 {
			body.Bind(&a.promptUI.rootHandle).Focusable().AutoFocus().FocusRing(false)
			if needFocusSync {
				a.promptUI.rootHandle.RequestFocus(c)
				a.mu.Lock()
				a.promptUI.needFocusSync = false
				a.mu.Unlock()
			}
		}
		body.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputKeyDown || a.promptUI.isComposing {
				return false
			}
			switch {
			case ev.Key == ui.KeyEscape && ev.Mods == 0:
				a.ClosePromptComposer()
				return true
			case ev.Key == ui.KeyEnter && ev.Mods == 0:
				a.CommitPromptComposer()
				return true
			case ev.Key == ui.KeyTab && ev.Mods == 0:
				a.PromptComposerHandleKey("Tab")
				return true
			case ev.Key == ui.KeyTab && ev.Mods == ui.Shift:
				a.PromptComposerHandleKey("Shift+Tab")
				return true
			case ev.Key == ui.KeyLeft && ev.Mods == 0:
				a.PromptComposerHandleKey("Left")
				return true
			case ev.Key == ui.KeyRight && ev.Mods == 0:
				a.PromptComposerHandleKey("Right")
				return true
			}
			return false
		})

		body.Children(func() {
			if len(slots) == 0 {
				ui.Box(c).
					FillWidth().
					Padding(space10, space12).
					Radius(radius8).
					Background(theme.Surface).
					Border(1, withAlpha(theme.Border, alphaStrong)).
					Children(func() {
						ui.Text(c, i18n.T("prompts.no_slots")).FontSize(typeLabel).TextColor(theme.TextMuted)
					})
			} else {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					for i, slot := range slots {
						slotIdx := i
						sl := slot
						label := strings.TrimSpace(sl.Label)
						if label == "" {
							label = sl.Name
						}
						isActive := slotIdx == activeSlotIdx

						ui.Column(c).Key("prompt-slot:" + sl.Name).FillWidth().Gap(space4).Children(func() {
							// The active slot shows through its field's focus ring or
							// accent edge; the label row carries only the neutral
							// "required" tag (a lone accent tick there read as a caret).
							kitControlRow(c, theme, label, func() {
								if sl.Required {
									ui.Box(c).Padding(space1, space4).Radius(radius4).
										Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
										ui.Text(c, i18n.T("prompts.required_badge")).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
									})
								}
							})

							if sl.Kind == "select" && len(sl.Options) > 0 {
								selRow := ui.Row(c).
									Bind(&a.promptUI.slotHandles[slotIdx]).
									Focusable().
									FocusRing(false).
									FillWidth().
									Gap(6).
									Padding(space4, space6).
									Radius(radius8).
									Background(theme.Surface)
								if isActive {
									selRow.Border(1, withAlpha(theme.Accent, alphaStrong))
								} else {
									selRow.Border(1, withAlpha(theme.Border, alphaStrong))
								}
								selRow.HandleInput(func(ev ui.InputEvent) bool {
									if ev.Kind != ui.InputKeyDown {
										return false
									}
									switch {
									case ev.Key == ui.KeyLeft && ev.Mods == 0:
										a.PromptComposerHandleKey("Left")
										return true
									case ev.Key == ui.KeyRight && ev.Mods == 0:
										a.PromptComposerHandleKey("Right")
										return true
									case ev.Key == ui.KeyTab && ev.Mods == 0:
										a.PromptComposerHandleKey("Tab")
										return true
									case ev.Key == ui.KeyTab && ev.Mods == ui.Shift:
										a.PromptComposerHandleKey("Shift+Tab")
										return true
									case ev.Key == ui.KeyEnter && ev.Mods == 0:
										a.CommitPromptComposer()
										return true
									case ev.Key == ui.KeyEscape && ev.Mods == 0:
										a.ClosePromptComposer()
										return true
									}
									return false
								})
								if !needFocusSync && selRow.Focused() && activeSlotIdx != slotIdx {
									a.mu.Lock()
									if a.promptSession != nil {
										a.promptSession.ActiveSlotIndex = slotIdx
									}
									a.mu.Unlock()
								}
								selRow.Children(func() {
									curVal := slotValues[sl.Name]
									for _, opt := range sl.Options {
										optVal := opt
										chosen := optVal == curVal
										pill := ui.ButtonBase(c).
											Key("opt:"+sl.Name+":"+optVal).
											Padding(space4, space10).
											Radius(radiusChip).
											FocusRing(false)
										if chosen {
											pill.Background(theme.Accent)
										} else {
											pill.Background(theme.Background).Border(1, withAlpha(theme.Border, alphaStrong))
										}
										pill.Children(func() {
											t := ui.Text(c, optVal).FontSize(typeLabel).SingleLine()
											if chosen {
												t.TextColor(theme.AccentText).FontWeight(600)
											} else {
												t.TextColor(theme.Text)
											}
										})
										if pill.Clicked() {
											a.mu.Lock()
											if a.promptSession != nil {
												a.promptSession.ActiveSlotIndex = slotIdx
											}
											a.mu.Unlock()
											a.SetPromptSlotValue(sl.Name, optVal)
											a.promptUI.slotHandles[slotIdx].RequestFocus(c)
										}
									}
								})
							} else {
								val := slotValues[sl.Name]
								in := ui.TextInput(c, &val).
									Bind(&a.promptUI.slotHandles[slotIdx]).
									Placeholder(i18n.Tf("prompts.slot_placeholder", label)).
									Label(label).
									FillWidth()
								if slotIdx == 0 {
									in.AutoFocus()
								}
								in.ObserveInput(func(ev ui.InputEvent) {
									switch ev.Kind {
									case ui.InputCompose:
										a.promptUI.isComposing = ev.Text != ""
									case ui.InputText, ui.InputCommand:
										a.promptUI.isComposing = false
									}
								})
								if !in.Focused() {
									a.promptUI.isComposing = false
								} else {
									a.promptUI.isComposing = in.Composing()
									if !needFocusSync && activeSlotIdx != slotIdx {
										a.mu.Lock()
										if a.promptSession != nil {
											a.promptSession.ActiveSlotIndex = slotIdx
										}
										a.mu.Unlock()
									}
								}
								in.OnTextKey(func(k ui.TextKey) bool {
									if a.promptUI.isComposing {
										switch k.Key {
										case ui.KeyEnter, ui.KeyTab, ui.KeyEscape:
											return true
										default:
											return false
										}
									}
									switch {
									case k.Key == ui.KeyTab && k.Mods == 0:
										a.PromptComposerHandleKey("Tab")
										return true
									case k.Key == ui.KeyTab && k.Mods == ui.Shift:
										a.PromptComposerHandleKey("Shift+Tab")
										return true
									case k.Key == ui.KeyEnter && k.Mods == 0:
										a.CommitPromptComposer()
										return true
									case k.Key == ui.KeyEscape && k.Mods == 0:
										a.ClosePromptComposer()
										return true
									}
									return false
								})
								if in.Changed() && val != slotValues[sl.Name] {
									a.SetPromptSlotValue(sl.Name, val)
								}
							}
						})
					}

					if needFocusSync {
						if activeSlotIdx >= 0 && activeSlotIdx < len(a.promptUI.slotHandles) {
							a.promptUI.slotHandles[activeSlotIdx].RequestFocus(c)
						}
						a.mu.Lock()
						a.promptUI.needFocusSync = false
						a.mu.Unlock()
					}
				})
			}

			if validationErr != "" {
				ui.Box(c).
					FillWidth().
					Padding(space8, space10).
					Radius(radius8).
					Background(withAlpha(theme.Danger, alphaFaint)).
					Border(1, withAlpha(theme.Danger, alphaHalf)).
					Children(func() {
						ui.Text(c, validationErr).FontSize(typeLabel).FontWeight(600).TextColor(theme.Danger)
					})
			}

			// Live-rendered preview is an inset kit card so the composer reads as
			// one elevated surface with a clearly separated output well.
			kitCard(c, theme, cardInset, func() {
				ui.Text(c, i18n.T("prompts.preview")).FontSize(typeMeta).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
				ui.Scroll(c).FillWidth().Grow(1).Children(func() {
					renderPreviewLines(c, theme, preview.RenderedText, 40)
				})
			})
		})

		kitDialogFooter(c, theme, i18n.T("prompts.footer.hint"),
			dialogAction{Label: i18n.T("common.copy"), OnClick: func() { a.CopyPromptComposer() }},
			dialogAction{Label: i18n.T("prompts.cancel"), OnClick: a.ClosePromptComposer},
			dialogAction{Label: i18n.T("prompts.insert"), Primary: true, OnClick: func() { a.CommitPromptComposer() }},
		)
	})
}
