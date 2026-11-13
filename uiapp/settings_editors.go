package uiapp

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// settingsEditorState holds the draft/editor state of the Settings
// configuration center. It lives in this file keyed by *AppController so that
// controller.go keeps its own state untouched, and it is safe to read from any
// goroutine the UI uses.
type settingsEditorState struct {
	mu sync.Mutex

	// recordingShortcut is set while the capsule shortcut recorder waits for
	// the next modifier+key chord.
	recordingShortcut bool

	// leader tree editor.
	leaderSelected []string
	leaderDraft    config.LeaderNode
	leaderLoaded   bool
	leaderError    string
	leaderNewKey   string
	leaderNewLabel string
	leaderChildKey string
	leaderChildLbl string

	// snippets master-detail editor.
	snippetQuery    string
	snippetSelected string
	snippetIsNew    bool
	snippetDraft    config.SnippetItem

	// glossary master-detail editor.
	glossarySelected string
	glossaryIsNew    bool
	glossaryDraft    config.GlossaryTerm

	// AI profile editor.
	aiDraftProfile config.AIProfile

	// keymap remap form.
	remapFrom string
	remapTap  string
	remapHold string
	remapApps string

	// clipboard exclusions form.
	clipExcludeDraft string
}

var settingsEditorStates sync.Map // map[*AppController]*settingsEditorState

// settingsEditor returns the editor state of ctrl, creating it on first use.
func (a *AppController) settingsEditor() *settingsEditorState {
	if v, ok := settingsEditorStates.Load(a); ok {
		return v.(*settingsEditorState)
	}
	fresh := &settingsEditorState{}
	actual, _ := settingsEditorStates.LoadOrStore(a, fresh)
	return actual.(*settingsEditorState)
}

// seGet reads the editor state under its lock.
func seGet[T any](se *settingsEditorState, fn func(s *settingsEditorState) T) T {
	se.mu.Lock()
	defer se.mu.Unlock()
	return fn(se)
}

// seSet mutates the editor state under its lock.
func seSet(se *settingsEditorState, fn func(s *settingsEditorState)) {
	se.mu.Lock()
	defer se.mu.Unlock()
	fn(se)
}

// ---------------------------------------------------------------------------
// Shared Settings widgets
// ---------------------------------------------------------------------------

// settingsLabelled renders a muted caption above body.
func settingsLabelled(c *ui.Context, theme *ui.Theme, label string, body func()) {
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(theme.TextMuted).SingleLine()
		body()
	})
}

// settingsInlineRow renders a muted caption left of body, width wide.
func settingsInlineRow(c *ui.Context, theme *ui.Theme, label string, width float32, body func()) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(theme.TextMuted).Width(width).SingleLine()
		body()
	})
}

// settingsPills renders a row of choice pills and reports the chosen value.
func settingsPills(c *ui.Context, theme *ui.Theme, key string, options []string, current string) (string, bool) {
	chosen, changed := current, false
	ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
		for _, opt := range options {
			o := opt
			active := strings.EqualFold(o, current)
			pill := ui.ButtonBase(c).Key(key+":"+o).Padding(3, 8).Radius(6).FocusRing(false)
			if active {
				pill.Background(theme.Accent)
			} else {
				pill.Background(theme.Background).Border(1, theme.Border)
			}
			pill.Children(func() {
				txt := ui.Text(c, o).FontSize(11).SingleLine()
				if active {
					txt.TextColor(theme.AccentText).FontWeight(600)
				} else {
					txt.TextColor(theme.TextMuted)
				}
			})
			if pill.Clicked() {
				chosen, changed = o, true
			}
		}
	})
	return chosen, changed
}

// settingsPillGrid renders choice pills in a grid of columns columns.
func settingsPillGrid(c *ui.Context, theme *ui.Theme, key string, columns int, options []string, current string) (string, bool) {
	chosen, changed := current, false
	ui.Grid(c).Columns(columns).Gap(4).FillWidth().Children(func() {
		for _, opt := range options {
			o := opt
			active := strings.EqualFold(o, current)
			pill := ui.ButtonBase(c).Key(key+":"+o).Padding(3, 6).Radius(6).FocusRing(false).Center()
			if active {
				pill.Background(theme.Accent)
			} else {
				pill.Background(theme.Background).Border(1, theme.Border)
			}
			pill.Children(func() {
				txt := ui.Text(c, o).FontSize(11).SingleLine()
				if active {
					txt.TextColor(theme.AccentText).FontWeight(600)
				} else {
					txt.TextColor(theme.TextMuted)
				}
			})
			if pill.Clicked() {
				chosen, changed = o, true
			}
		}
	})
	return chosen, changed
}

// settingsSwitch renders one labelled switch row and reports a change.
func settingsSwitch(c *ui.Context, theme *ui.Theme, caption, label string, on bool) (bool, bool) {
	value, changed := on, false
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, caption).FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
		sw := ui.Switch(c, &value).Label(label)
		if sw.Changed() {
			changed = true
		}
	})
	return value, changed
}

// settingsStepper renders a number stepper with a caption and reports a change.
func settingsStepper(
	c *ui.Context,
	theme *ui.Theme,
	caption string,
	value float64,
	lo, hi, step float64,
	suffix string,
) (float64, bool) {
	next, changed := value, false
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		text := caption
		if suffix != "" {
			text = fmt.Sprintf("%s: %.0f %s", caption, value, suffix)
		}
		ui.Text(c, text).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)
		v := value
		st := ui.Stepper(c, &v, lo, hi, step)
		if st.Changed() {
			next, changed = v, true
		}
	})
	return next, changed
}

// settingsSelect renders a pop-up selector and reports a change.
func settingsSelect(c *ui.Context, options []string, current string) (string, bool) {
	value := current
	if value == "" && len(options) > 0 {
		value = options[0]
	}
	sel := ui.Select(c, &value, options)
	return value, sel.Changed()
}

// settingsTextInput renders a labelled single-line text field.
func settingsTextInput(c *ui.Context, label, placeholder, value string) (string, bool) {
	in := ui.TextInput(c, &value).Label(label).Placeholder(placeholder).FillWidth()
	return value, in.Changed()
}

// settingsTextArea renders a labelled multi-line text field.
func settingsTextArea(c *ui.Context, label, placeholder, value string, lines int) (string, bool) {
	ta := ui.TextArea(c, &value).Label(label).Placeholder(placeholder).Lines(lines, lines+6).FillWidth()
	return value, ta.Changed()
}

// settingsHint renders a muted line of help text.
func settingsHint(c *ui.Context, theme *ui.Theme, text string) {
	ui.Text(c, text).FontSize(11).TextColor(theme.TextMuted).SingleLine()
}

// settingsBanner renders a colored note with a border of the same color.
func settingsBanner(c *ui.Context, color ui.Color, text string) {
	ui.Box(c).
		FillWidth().
		Padding(4, 8).
		Radius(6).
		Background(withAlpha(color, 0.12)).
		Border(1, withAlpha(color, 0.45)).
		Children(func() {
			ui.Text(c, text).FontSize(11).TextColor(color).SingleLine()
		})
}

// settingsDivider inserts a divider inside a card.
func settingsDivider(c *ui.Context) { ui.Divider(c) }

// ---------------------------------------------------------------------------
// Triggers & Appearance
// ---------------------------------------------------------------------------

// triggerPresets lists the supported leader/double-tap key presets.
func triggerPresets() []string {
	return []string{"right_option", "right_command", "left_option", "left_command", "right_control", "caps_lock"}
}

// shortcutFromKey maps a key chord to the canonical TOML text form, as
// "option+space" or "command+shift+k".
func shortcutFromKey(key ui.Key, mods ui.Modifiers) (string, bool) {
	name, ok := shortcutKeyName(key)
	if !ok {
		return "", false
	}
	var parts []string
	if mods&ui.Super != 0 {
		parts = append(parts, "command")
	}
	if mods&ui.Ctrl != 0 {
		parts = append(parts, "control")
	}
	if mods&ui.Alt != 0 {
		parts = append(parts, "option")
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "shift")
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(append(parts, name), "+"), true
}

// shortcutKeyName returns the canonical name of a shortcut's key.
func shortcutKeyName(key ui.Key) (string, bool) {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return string(rune('a' + (key - ui.KeyA))), true
	case key >= ui.Key0 && key <= ui.Key9:
		return string(rune('0' + (key - ui.Key0))), true
	}
	switch key {
	case ui.KeySpace:
		return "space", true
	case ui.KeyEnter:
		return "return", true
	case ui.KeyTab:
		return "tab", true
	case ui.KeyEscape:
		return "escape", true
	case ui.KeyBackspace:
		return "delete", true
	case ui.KeyUp:
		return "up", true
	case ui.KeyDown:
		return "down", true
	case ui.KeyLeft:
		return "left", true
	case ui.KeyRight:
		return "right", true
	case ui.KeyHome:
		return "home", true
	case ui.KeyEnd:
		return "end", true
	}
	return "", false
}

// normalizeShortcutText lower-cases a hand-typed shortcut and maps alt to option.
func normalizeShortcutText(text string) string {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(text)), func(r rune) bool {
		return r == '+' || r == ' ' || r == '-'
	})
	if len(fields) == 0 {
		return ""
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		switch f {
		case "alt":
			f = "option"
		case "cmd", "meta", "super":
			f = "command"
		case "ctrl":
			f = "control"
		}
		out = append(out, f)
	}
	return strings.Join(out, "+")
}

// ---------------------------------------------------------------------------
// Keymap remaps
// ---------------------------------------------------------------------------

// renderKeymapControls draws the keymap card, including custom remap CRUD.
func (a *AppController) renderKeymapControls(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	keymap := cfg.Main.Keymap

	settingsSectionCard(c, theme, "Keymap & Key Remaps", func() {
		if v, changed := settingsSwitch(c, theme, "Keymap Enabled (keymap.enabled)", "Keymap Enabled", keymap.Enabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.Enabled = v })
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Nav Layer (nav_layer_enabled)").FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
			navLayer := keymap.NavLayerEnabled
			sw := ui.Switch(c, &navLayer).Label("Nav Layer")
			if sw.Changed() {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.NavLayerEnabled = navLayer })
			}
		})

		if v, changed := settingsStepper(
			c, theme, "tap_hold_threshold_ms", float64(keymap.TapHoldThresholdMs), 80, 500, 10, "ms",
		); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.TapHoldThresholdMs = int(v) })
		}

		settingsInlineRow(c, theme, "caps_lock_tap", 110, func() {
			if v, changed := settingsSelect(c, []string{"escape", "nav", "hyper", "control", "none"}, keymap.CapsLockTap); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.CapsLockTap = v })
			}
		})
		settingsInlineRow(c, theme, "caps_lock_hold", 110, func() {
			if v, changed := settingsSelect(c, []string{"escape", "nav", "hyper", "control", "none"}, keymap.CapsLockHold); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.CapsLockHold = v })
			}
		})

		settingsDivider(c)
		ui.Text(c, "Custom remaps ([[keymap.remaps]])").FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()

		remapDrafts := seGet(se, func(s *settingsEditorState) [4]string {
			return [4]string{s.remapFrom, s.remapTap, s.remapHold, s.remapApps}
		})
		from, tap, hold, apps := remapDrafts[0], remapDrafts[1], remapDrafts[2], remapDrafts[3]
		ui.Grid(c).Columns(2).Gap(6).FillWidth().Children(func() {
			if v, changed := settingsTextInput(c, "Remap From", "caps_lock", from); changed {
				seSet(se, func(s *settingsEditorState) { s.remapFrom = v })
			}
			if v, changed := settingsTextInput(c, "Remap Tap", "escape", tap); changed {
				seSet(se, func(s *settingsEditorState) { s.remapTap = v })
			}
			if v, changed := settingsTextInput(c, "Remap Hold", "nav", hold); changed {
				seSet(se, func(s *settingsEditorState) { s.remapHold = v })
			}
			if v, changed := settingsTextInput(c, "Remap Apps", "com.apple.Safari, com.apple.Terminal", apps); changed {
				seSet(se, func(s *settingsEditorState) { s.remapApps = v })
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			newFrom := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.remapFrom }))
			if ui.PrimaryButton(c, "Add Remap").Disabled(newFrom == "").Clicked() {
				rule := config.KeyRemapRule{
					From:         strings.ToLower(newFrom),
					Tap:          strings.ToLower(strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.remapTap }))),
					Hold:         strings.ToLower(strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.remapHold }))),
					AppBundleIDs: splitTokens(seGet(se, func(s *settingsEditorState) string { return s.remapApps })),
				}
				_ = a.UpdateMainConfig(func(m *config.MainConfig) {
					m.Keymap.Remaps = append(append([]config.KeyRemapRule(nil), m.Keymap.Remaps...), rule)
				})
				seSet(se, func(s *settingsEditorState) {
					s.remapFrom, s.remapTap, s.remapHold, s.remapApps = "", "", "", ""
				})
			}
			settingsHint(c, theme, fmt.Sprintf("%d remap(s)", len(keymap.Remaps)))
		})

		if len(keymap.Remaps) == 0 {
			settingsHint(c, theme, "No custom remaps configured.")
			return
		}
		for idx, rule := range keymap.Remaps {
			i, r := idx, rule
			summary := fmt.Sprintf("%s → tap=%s hold=%s", r.From, r.Tap, r.Hold)
			if len(r.AppBundleIDs) > 0 {
				summary += " (" + strings.Join(r.AppBundleIDs, ", ") + ")"
			}
			ui.Row(c).Key(fmt.Sprintf("remap:%d:%s", i, r.From)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, summary).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)
				if ui.Button(c, "Delete").Key(fmt.Sprintf("del-remap:%d", i)).Clicked() {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) {
						remaps := make([]config.KeyRemapRule, 0, len(m.Keymap.Remaps))
						for j, item := range m.Keymap.Remaps {
							if j != i {
								remaps = append(remaps, item)
							}
						}
						m.Keymap.Remaps = remaps
					})
				}
			})
		}
	})
}

// splitTokens splits a comma or whitespace separated token list.
func splitTokens(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// IME configuration
// ---------------------------------------------------------------------------

// renderIMEConfig draws the IME settings card: switches, delay, default source,
// an installed-app quick picker and an inline regex validation indicator.
func (a *AppController) renderIMEConfig(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	imeCfg := cfg.Main.IME

	settingsSectionCard(c, theme, "IME Configuration (ime.*)", func() {
		ui.Grid(c).Columns(2).Gap(6).FillWidth().Children(func() {
			if v, changed := settingsSwitch(c, theme, "IME Auto-Switch (ime.enabled)", "IME Enabled", imeCfg.Enabled); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.Enabled = v })
			}
			if v, changed := settingsSwitch(c, theme, "Lock ASCII on Leader (lock_ascii_on_leader)", "Lock ASCII on Leader", imeCfg.LockASCIIOnLeader); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.LockASCIIOnLeader = v })
			}
			if v, changed := settingsSwitch(c, theme, "Show Floating Pill (show_floating_pill)", "Show Floating Pill", imeCfg.ShowFloatingPill); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.ShowFloatingPill = v })
			}
			if v, changed := settingsSwitch(c, theme, "Show Indicator HUD (show_indicator_hud)", "Show Indicator HUD", imeCfg.ShowIndicatorHUD); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.ShowIndicatorHUD = v })
			}
		})

		if v, changed := settingsStepper(c, theme, "switch_delay_ms", float64(imeCfg.SwitchDelayMs), 0, 500, 10, "ms"); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.SwitchDelayMs = int(v) })
		}

		sources := a.AvailableInputSources(context.Background())
		ids := make([]string, 0, len(sources)+1)
		ids = append(ids, "")
		for _, src := range sources {
			if strings.TrimSpace(src.ID) != "" {
				ids = append(ids, src.ID)
			}
		}
		settingsInlineRow(c, theme, "default_input_source_id", 130, func() {
			current := imeCfg.DefaultInputSourceID
			if !slices.Contains(ids, current) {
				ids = append(ids, current)
			}
			if v, changed := settingsSelect(c, ids, current); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.DefaultInputSourceID = v })
			}
		})

		settingsDivider(c)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Installed App Quick-Picker").FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
			settingsHint(c, theme, fmt.Sprintf("%d app(s)", len(a.State().InstalledApps)))
		})
		picked := ""
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			apps := a.State().InstalledApps
			if len(apps) == 0 {
				settingsHint(c, theme, "No installed apps detected yet.")
			}
			for i, app := range apps {
				if i >= 6 {
					break
				}
				entry := app
				label := entry.Name
				if label == "" {
					label = entry.BundleID
				}
				if ui.Button(c, label).Key("ime-app:" + entry.BundleID).Clicked() {
					picked = entry.BundleID
				}
			}
		})
		if picked != "" {
			a.mu.Lock()
			if target := strings.TrimSpace(picked); target != "" {
				a.uiState.imeRuleTargetDraft = target
			}
			a.mu.Unlock()
		}

		settingsDivider(c)
		ui.Text(c, "New IME Rule").FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()

		a.mu.Lock()
		targetDraft := a.uiState.imeRuleTargetDraft
		urlDraft := a.uiState.imeRuleURLDraft
		titleDraft := a.uiState.imeRuleTitleDraft
		sourceDraft := a.uiState.imeRuleSourceDraft
		modeDraft := a.uiState.imeRuleModeDraft
		a.mu.Unlock()
		if modeDraft == "" {
			modeDraft = config.IMEModeASCII
		}

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Grow(2).Children(func() {
				tIn := ui.TextInput(c, &targetDraft).
					Placeholder("Bundle ID or Process Name (e.g. com.apple.Safari)...").
					Label("Rule Target").
					FillWidth()
				if tIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleTargetDraft = targetDraft
					a.mu.Unlock()
				}
			})
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				uIn := ui.TextInput(c, &urlDraft).
					Placeholder("URL Pattern (optional)...").
					Label("URL Pattern").
					FillWidth()
				if uIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleURLDraft = urlDraft
					a.mu.Unlock()
				}
			})
		})

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				tiIn := ui.TextInput(c, &titleDraft).
					Placeholder("Title Pattern (optional)...").
					Label("Title Pattern").
					FillWidth()
				if tiIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleTitleDraft = titleDraft
					a.mu.Unlock()
				}
			})
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				sIn := ui.TextInput(c, &sourceDraft).
					Placeholder("Input Source ID (default: com.apple.keylayout.ABC)...").
					Label("Input Source ID").
					FillWidth()
				if sIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleSourceDraft = sourceDraft
					a.mu.Unlock()
				}
			})
		})

		if msg := regexIssue(titleDraft); msg != "" {
			settingsBanner(c, theme.Danger, "Title Pattern: invalid regex ("+msg+")")
		} else if strings.TrimSpace(titleDraft) != "" {
			settingsHint(c, theme, "Title Pattern: valid regex ✓")
		}
		if msg := regexIssue(urlDraft); msg != "" {
			settingsBanner(c, theme.Danger, "URL Pattern: invalid regex ("+msg+")")
		}

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			for _, m := range config.AllIMEModes() {
				modeVal := m
				activeMode := modeDraft == modeVal
				mBtn := ui.ButtonBase(c).
					Key("ime-mode:"+string(modeVal)).
					Padding(4, 8).
					Radius(5).
					FocusRing(false)
				if activeMode {
					mBtn.Background(theme.Accent)
				} else {
					mBtn.Background(theme.Background).Border(1, theme.Border)
				}
				mBtn.Children(func() {
					txt := ui.Text(c, string(modeVal)).FontSize(11).SingleLine()
					if activeMode {
						txt.TextColor(theme.AccentText).FontWeight(600)
					} else {
						txt.TextColor(theme.TextMuted)
					}
				})
				if mBtn.Clicked() {
					a.mu.Lock()
					a.uiState.imeRuleModeDraft = modeVal
					a.mu.Unlock()
				}
			}

			ui.Spacer(c)
		})

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			a.mu.Lock()
			curTarget := strings.TrimSpace(a.uiState.imeRuleTargetDraft)
			curURL := strings.TrimSpace(a.uiState.imeRuleURLDraft)
			curTitle := strings.TrimSpace(a.uiState.imeRuleTitleDraft)
			curSource := strings.TrimSpace(a.uiState.imeRuleSourceDraft)
			curMode := a.uiState.imeRuleModeDraft
			a.mu.Unlock()
			if curMode == "" {
				curMode = config.IMEModeASCII
			}

			if ui.PrimaryButton(c, "Add Rule").Disabled(curTarget == "").Clicked() {
				newRule := config.IMERule{
					AppName:        curTarget,
					URLPattern:     curURL,
					TitlePattern:   curTitle,
					InputSourceID:  curSource,
					PreferredMode:  curMode,
					RestoreOnLeave: true,
				}
				if strings.Contains(curTarget, ".") {
					newRule.BundleID = curTarget
				} else {
					newRule.ProcessName = curTarget
				}
				_ = a.AddIMERule(newRule)
			}
		})
	})
}

// regexIssue returns a description of why pattern is not a valid regex.
func regexIssue(pattern string) string {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return ""
	}
	if _, err := regexp.Compile(trimmed); err != nil {
		return err.Error()
	}
	return ""
}

// renderIMEKeymapSection draws the IME & Keymap section.
func (a *AppController) renderIMEKeymapSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	ui.Grid(c).Columns(2).Gap(10).FillWidth().Children(func() {
		a.renderKeymapControls(c, theme, cfg)
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			a.renderIMEConfig(c, theme, cfg)
			a.renderIMERulesCard(c, theme, cfg)
		})
	})
}

// renderIMERulesCard lists the configured IME rules with delete buttons,
// newest first.
func (a *AppController) renderIMERulesCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	settingsSectionCard(c, theme, "IME Auto-Switch Rules (ime-rules.toml)", func() {
		rules := cfg.IMERules.Rules
		if len(rules) == 0 {
			ui.Text(c, "No IME rules configured.").FontSize(12).TextColor(theme.TextMuted)
			return
		}
		for i := len(rules) - 1; i >= 0; i-- {
			idx := i
			rule := rules[idx]
			targetLabel := rule.BundleID
			if targetLabel == "" {
				targetLabel = rule.ProcessName
			}
			if rule.AppName != "" && !strings.EqualFold(rule.AppName, targetLabel) {
				targetLabel = fmt.Sprintf("%s (%s)", rule.AppName, targetLabel)
			}

			var details []string
			details = append(details, fmt.Sprintf("mode=%s", rule.PreferredMode))
			details = append(details, fmt.Sprintf("source=%s", rule.InputSourceID))
			if rule.URLPattern != "" {
				details = append(details, fmt.Sprintf("url=%s", rule.URLPattern))
			}
			if rule.TitlePattern != "" {
				details = append(details, fmt.Sprintf("title=%s", rule.TitlePattern))
			}
			details = append(details, fmt.Sprintf("restore=%v", rule.RestoreOnLeave))

			ui.Column(c).Key(fmt.Sprintf("ime-rule:%d:%s", idx, targetLabel)).FillWidth().Gap(1).Children(func() {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, targetLabel).FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
					if ui.Button(c, "Delete").Key(fmt.Sprintf("del-ime-rule:%d", idx)).Clicked() {
						_ = a.DeleteIMERule(idx)
					}
				})
				ui.Text(c, strings.Join(details, " • ")).FontSize(10).TextColor(theme.TextMuted).SingleLine()
			})
		}
	})
}

// ---------------------------------------------------------------------------
// AI profile editor
// ---------------------------------------------------------------------------

// renderAIProfileEditor draws the editor of the active AI profile.
func (a *AppController) renderAIProfileEditor(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	active := a.ActiveAIProfile()

	settingsSectionCard(c, theme, "Active Profile Configuration", func() {
		settingsHint(c, theme, "Editing AI profile: "+active.ID)
		ui.Grid(c).Columns(2).GapX(8).GapY(2).FillWidth().Children(func() {
			settingsInlineRow(c, theme, "name", 58, func() {
				if v, changed := settingsTextInput(c, "Profile Name", active.Name, active.Name); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Name = v })
				}
			})
			settingsInlineRow(c, theme, "provider", 58, func() {
				if v, changed := settingsTextInput(c, "Profile Provider", "deepseek", active.Provider); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Provider = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, "protocol", 58, func() {
				opts := make([]string, 0, 3)
				for _, p := range config.AllAIProtocols() {
					opts = append(opts, string(p))
				}
				current := string(active.Protocol)
				if current == "" {
					current = string(config.ProtocolOpenAICompatible)
				}
				if v, changed := settingsSelect(c, opts, current); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Protocol = config.AIProtocol(v) })
				}
			})
			settingsInlineRow(c, theme, "model", 58, func() {
				if v, changed := settingsTextInput(c, "Profile Model", "model-name", active.Model); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Model = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, "temperature", 58, func() {
				v := active.Temperature
				st := ui.Stepper(c, &v, 0, 2, 0.1)
				if st.Changed() {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Temperature = v })
				}
			})
			settingsInlineRow(c, theme, "timeout_s", 58, func() {
				v := active.TimeoutSeconds
				st := ui.Stepper(c, &v, 5, 300, 5)
				if st.Changed() {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.TimeoutSeconds = v })
				}
			})
		})
		settingsInlineRow(c, theme, "base_url", 58, func() {
			if v, changed := settingsTextInput(c, "Profile Base URL", "https://api.example.com/v1", active.BaseURL); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.BaseURL = strings.TrimSpace(v) })
			}
		})
		settingsInlineRow(c, theme, "keychain", 58, func() {
			if v, changed := settingsTextInput(c, "Profile Keychain Account", "clavis.ai.profile", active.KeychainAccount); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.KeychainAccount = strings.TrimSpace(v) })
			}
		})
		settingsLabelled(c, theme, "system_prompt", func() {
			if v, changed := settingsTextArea(c, "System Prompt", "You are a concise assistant...", active.SystemPrompt, 2); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.SystemPrompt = v })
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "Delete Profile").Disabled(len(cfg.Main.AI.Profiles) <= 1).Clicked() {
				a.deleteAIProfile(active.ID)
			}
			settingsHint(c, theme, fmt.Sprintf("%d profile(s) configured", len(cfg.Main.AI.Profiles)))
		})
	})
}

// renderAIAddProfileCard draws the form that appends a custom AI profile.
func (a *AppController) renderAIAddProfileCard(c *ui.Context, theme *ui.Theme) {
	se := a.settingsEditor()
	settingsSectionCard(c, theme, "Add Custom AI Profile", func() {
		draft := seGet(se, func(s *settingsEditorState) config.AIProfile { return s.aiDraftProfile })
		if draft.ID == "" && draft.Provider == "" && draft.BaseURL == "" && draft.Model == "" {
			draft = config.AIProfile{Protocol: config.ProtocolOpenAICompatible, Temperature: 0.2, TimeoutSeconds: 30}
		}
		ui.Grid(c).Columns(2).GapX(8).GapY(2).FillWidth().Children(func() {
			if v, changed := settingsTextInput(c, "New Profile ID", "my-profile", draft.ID); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.ID = v })
			}
			if v, changed := settingsTextInput(c, "New Profile Provider", "openai", draft.Provider); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.Provider = v })
			}
			if v, changed := settingsTextInput(c, "New Profile Base URL", "https://api.example.com/v1", draft.BaseURL); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.BaseURL = v })
			}
			if v, changed := settingsTextInput(c, "New Profile Model", "model-name", draft.Model); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.Model = v })
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, "Add Profile").Clicked() {
				a.addAIProfileDraft()
			}
			settingsHint(c, theme, "New profiles persist to config.toml")
		})
	})
}

// updateAIProfile applies mutate to the profile with id and persists it,
// keeping the flat [ai] fields of the active profile in sync.
func (a *AppController) updateAIProfile(id string, mutate func(p *config.AIProfile)) {
	_ = a.UpdateMainConfig(func(m *config.MainConfig) {
		for i := range m.AI.Profiles {
			if m.AI.Profiles[i].ID != id {
				continue
			}
			mutate(&m.AI.Profiles[i])
			if m.AI.ActiveProfileID == id {
				syncActiveAIFlatFields(&m.AI, m.AI.Profiles[i])
			}
			return
		}
	})
}

// syncActiveAIFlatFields mirrors a profile into the flat [ai] fields.
func syncActiveAIFlatFields(aiCfg *config.AIConfig, p config.AIProfile) {
	aiCfg.Provider = p.Provider
	aiCfg.Protocol = p.Protocol
	aiCfg.BaseURL = p.BaseURL
	aiCfg.Model = p.Model
	aiCfg.Temperature = p.Temperature
	aiCfg.TimeoutSeconds = p.TimeoutSeconds
	aiCfg.SystemPrompt = p.SystemPrompt
	aiCfg.KeychainAccount = p.KeychainAccount
	aiCfg.APIKeyEnvVar = p.APIKeyEnvVar
}

// addAIProfileDraft appends the profile drafted in the editor.
func (a *AppController) addAIProfileDraft() {
	se := a.settingsEditor()
	draft := seGet(se, func(s *settingsEditorState) config.AIProfile { return s.aiDraftProfile })
	draft.ID = strings.TrimSpace(draft.ID)
	if draft.ID == "" {
		return
	}
	draft.Provider = strings.TrimSpace(draft.Provider)
	if draft.Provider == "" {
		draft.Provider = draft.ID
	}
	draft.Name = strings.TrimSpace(draft.Name)
	if draft.Name == "" {
		draft.Name = draft.Provider
	}
	if draft.Protocol == "" {
		draft.Protocol = config.ProtocolOpenAICompatible
	}
	if draft.Temperature == 0 {
		draft.Temperature = 0.2
	}
	if draft.TimeoutSeconds == 0 {
		draft.TimeoutSeconds = 30
	}
	if draft.KeychainAccount == "" {
		draft.KeychainAccount = "clavis.ai." + draft.ID
	}
	added := false
	_ = a.UpdateMainConfig(func(m *config.MainConfig) {
		if _, exists := m.AI.ProfileByID(draft.ID); exists {
			return
		}
		m.AI.Profiles = append(append([]config.AIProfile(nil), m.AI.Profiles...), draft)
		m.AI.ActiveProfileID = draft.ID
		syncActiveAIFlatFields(&m.AI, draft)
		added = true
	})
	if added {
		seSet(se, func(s *settingsEditorState) { s.aiDraftProfile = config.AIProfile{} })
	}
}

// deleteAIProfile removes the profile with id.
func (a *AppController) deleteAIProfile(id string) {
	_ = a.UpdateMainConfig(func(m *config.MainConfig) {
		kept := make([]config.AIProfile, 0, len(m.AI.Profiles))
		for _, p := range m.AI.Profiles {
			if p.ID != id {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 || len(kept) == len(m.AI.Profiles) {
			return
		}
		m.AI.Profiles = kept
		m.AI.ActiveProfileID = kept[0].ID
		syncActiveAIFlatFields(&m.AI, kept[0])
	})
}

// ---------------------------------------------------------------------------
// Leader tree editor
// ---------------------------------------------------------------------------

// leaderActionTypes lists the supported LeaderAction types.
func leaderActionTypes() []string {
	return []string{
		string(config.ActionGroup),
		string(config.ActionWindow),
		string(config.ActionAIRewrite),
		string(config.ActionSnippet),
		string(config.ActionOpenApp),
		string(config.ActionSwitchTab),
		string(config.ActionCLI),
		string(config.ActionToggleIME),
		string(config.ActionNormalizeGlossary),
	}
}

// cloneLeaderAction copies an action, cloning its children.
func cloneLeaderAction(action config.LeaderAction) config.LeaderAction {
	out := action
	out.Children = cloneLeaderNodes(action.Children)
	out.Args = append([]string(nil), action.Args...)
	return out
}

// cloneLeaderNodes copies a leader node slice deeply enough to edit it.
func cloneLeaderNodes(nodes []config.LeaderNode) []config.LeaderNode {
	if nodes == nil {
		return nil
	}
	out := make([]config.LeaderNode, len(nodes))
	for i, n := range nodes {
		out[i] = n
		out[i].Action = cloneLeaderAction(n.Action)
	}
	return out
}

// leaderNodesAt returns the sibling slice and index addressed by path.
func leaderNodesAt(nodes []config.LeaderNode, path []string) ([]config.LeaderNode, int, []config.LeaderNode, bool) {
	if len(path) == 0 {
		return nil, -1, nodes, false
	}
	targetKey := path[0]
	for i := range nodes {
		if nodes[i].Key != targetKey {
			continue
		}
		if len(path) == 1 {
			return nodes, i, nil, true
		}
		return leaderNodesAt(nodes[i].Action.Children, path[1:])
	}
	return nil, -1, nil, false
}

// findLeaderNode resolves path to a node in nodes.
func findLeaderNode(nodes []config.LeaderNode, path []string) (config.LeaderNode, bool) {
	if len(path) == 0 {
		return config.LeaderNode{}, false
	}
	for i := range nodes {
		if nodes[i].Key != path[0] {
			continue
		}
		if len(path) == 1 {
			return nodes[i], true
		}
		return findLeaderNode(nodes[i].Action.Children, path[1:])
	}
	return config.LeaderNode{}, false
}

// replaceLeaderNode replaces the node at path, returning the new root slice.
func replaceLeaderNode(nodes []config.LeaderNode, path []string, replacement config.LeaderNode) []config.LeaderNode {
	out := cloneLeaderNodes(nodes)
	if len(path) == 0 {
		return out
	}
	for i := range out {
		if out[i].Key != path[0] {
			continue
		}
		if len(path) == 1 {
			out[i] = replacement
			return out
		}
		out[i].Action.Children = replaceLeaderNode(out[i].Action.Children, path[1:], replacement)
		return out
	}
	return out
}

// deleteLeaderNode removes the node at path, returning the new root slice.
func deleteLeaderNode(nodes []config.LeaderNode, path []string) []config.LeaderNode {
	if len(path) == 0 {
		return cloneLeaderNodes(nodes)
	}
	out := make([]config.LeaderNode, 0, len(nodes))
	for i := range nodes {
		node := nodes[i]
		node.Action = cloneLeaderAction(node.Action)
		if node.Key == path[0] {
			if len(path) == 1 {
				continue
			}
			node.Action.Children = deleteLeaderNode(node.Action.Children, path[1:])
		}
		out = append(out, node)
	}
	return out
}

// appendLeaderNode appends child under the node at parentPath (the root when
// parentPath is empty), returning the new root slice.
func appendLeaderNode(nodes []config.LeaderNode, parentPath []string, child config.LeaderNode) []config.LeaderNode {
	out := cloneLeaderNodes(nodes)
	if len(parentPath) == 0 {
		return append(out, child)
	}
	for i := range out {
		if out[i].Key != parentPath[0] {
			continue
		}
		if len(parentPath) == 1 {
			out[i].Action.Children = append(cloneLeaderNodes(out[i].Action.Children), child)
			return out
		}
		out[i].Action.Children = appendLeaderNode(out[i].Action.Children, parentPath[1:], child)
		return out
	}
	return out
}

// moveLeaderNode swaps the node at path with its sibling delta positions away.
func moveLeaderNode(nodes []config.LeaderNode, path []string, delta int) []config.LeaderNode {
	out := cloneLeaderNodes(nodes)
	if len(path) == 0 {
		return out
	}
	for i := range out {
		if out[i].Key != path[0] {
			continue
		}
		if len(path) == 1 {
			target := i + delta
			if target < 0 || target >= len(out) {
				return out
			}
			out[i], out[target] = out[target], out[i]
			return out
		}
		out[i].Action.Children = moveLeaderNode(out[i].Action.Children, path[1:], delta)
		return out
	}
	return out
}

// leaderKeyTaken reports whether key is already used by a sibling of the node
// at path, the node itself excluded.
func leaderKeyTaken(nodes []config.LeaderNode, path []string, key string) bool {
	siblings, idx, _, ok := leaderNodesAt(nodes, path)
	if !ok {
		return false
	}
	for i := range siblings {
		if i == idx {
			continue
		}
		if siblings[i].Key == key {
			return true
		}
	}
	return false
}

// leaderChildKeyTaken reports whether key is already used by a child of the
// node at parentPath, or by a root node when parentPath is empty.
func leaderChildKeyTaken(nodes []config.LeaderNode, parentPath []string, key string) bool {
	children := nodes
	if len(parentPath) > 0 {
		parent, ok := findLeaderNode(nodes, parentPath)
		if !ok {
			return false
		}
		children = parent.Action.Children
	}
	for i := range children {
		if children[i].Key == key {
			return true
		}
	}
	return false
}

// defaultLeaderAction builds an action of the requested type.
func defaultLeaderAction(kind string) config.LeaderAction {
	switch config.LeaderActionType(kind) {
	case config.ActionGroup:
		return config.GroupAction()
	case config.ActionWindow:
		return config.WindowAction(config.WindowLeftHalf)
	case config.ActionAIRewrite:
		return config.AIRewriteAction("polish", "Improve clarity and flow.")
	case config.ActionSnippet:
		return config.SnippetAction("")
	case config.ActionOpenApp:
		return config.OpenAppAction("")
	case config.ActionSwitchTab:
		return config.SwitchTabAction(config.TabActions)
	case config.ActionCLI:
		return config.CLIAction("")
	case config.ActionToggleIME:
		return config.ToggleIMEAction()
	case config.ActionNormalizeGlossary:
		return config.NormalizeGlossaryAction()
	default:
		return config.GroupAction()
	}
}

// renderLeaderSection draws the recursive Leader tree editor.
func (a *AppController) renderLeaderSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	selectedPath := seGet(se, func(s *settingsEditorState) []string { return append([]string(nil), s.leaderSelected...) })
	if len(selectedPath) > 0 {
		if _, ok := findLeaderNode(cfg.Leader.Nodes, selectedPath); !ok {
			seSet(se, func(s *settingsEditorState) { s.leaderSelected = nil })
			selectedPath = nil
		}
	}
	if len(selectedPath) > 0 {
		node, _ := findLeaderNode(cfg.Leader.Nodes, selectedPath)
		if !seGet(se, func(s *settingsEditorState) bool { return s.leaderLoaded }) {
			seSet(se, func(s *settingsEditorState) {
				s.leaderDraft = node
				s.leaderDraft.Action = cloneLeaderAction(node.Action)
				s.leaderLoaded = true
			})
		}
	}

	ui.Grid(c).Columns(2).Gap(10).FillWidth().Children(func() {
		settingsSectionCard(c, theme, fmt.Sprintf("Leader Tree (leader.toml) · %d root node(s)", len(cfg.Leader.Nodes)), func() {
			a.renderLeaderTree(c, theme, cfg.Leader.Nodes, nil, selectedPath)

			settingsDivider(c)
			ui.Text(c, "New Root Node").FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
			newKey := seGet(se, func(s *settingsEditorState) string { return s.leaderNewKey })
			newLabel := seGet(se, func(s *settingsEditorState) string { return s.leaderNewLabel })
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
				ui.Column(c).Gap(2).Width(60).Children(func() {
					if v, changed := settingsTextInput(c, "New Node Key", "z", newKey); changed {
						seSet(se, func(s *settingsEditorState) { s.leaderNewKey = v })
					}
				})
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					if v, changed := settingsTextInput(c, "New Node Label", "My Action", newLabel); changed {
						seSet(se, func(s *settingsEditorState) { s.leaderNewLabel = v })
					}
				})
			})
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, "Add Root Node").Clicked() {
					a.addLeaderNode(nil)
				}
				settingsHint(c, theme, "Keys must be unique among siblings.")
			})
			if msg := seGet(se, func(s *settingsEditorState) string { return s.leaderError }); msg != "" {
				settingsBanner(c, theme.Danger, msg)
			}
		})

		settingsSectionCard(c, theme, "Node Editor", func() {
			if len(selectedPath) == 0 {
				settingsHint(c, theme, "Select a node in the tree to edit it.")
				return
			}
			a.renderLeaderNodeEditor(c, theme, cfg, selectedPath)
		})
	})
}

// renderLeaderTree draws nodes recursively, indenting children.
func (a *AppController) renderLeaderTree(
	c *ui.Context,
	theme *ui.Theme,
	nodes []config.LeaderNode,
	prefix []string,
	selected []string,
) {
	se := a.settingsEditor()
	for i := range nodes {
		node := nodes[i]
		path := append(append([]string(nil), prefix...), node.Key)
		isSelected := slices.Equal(path, selected)
		row := ui.ButtonBase(c).
			Key("leader-node:"+strings.Join(path, "/")).
			FillWidth().
			Padding(2, 4).
			Radius(5).
			FocusRing(false)
		if isSelected {
			row.Background(withAlpha(theme.Accent, 0.16))
		}
		depth := len(prefix)
		row.Children(func() {
			if depth > 0 {
				ui.Spacer(c).Width(float32(depth) * 10)
			}
			ui.Box(c).
				Padding(1, 5).
				Radius(4).
				Background(withAlpha(theme.Accent, 0.18)).
				Children(func() {
					ui.Text(c, node.Key).FontSize(11).FontWeight(700).TextColor(theme.Accent).SingleLine()
				})
			ui.Text(c, node.Label).FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
			ui.Text(c, string(node.Action.Type)).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		})
		if row.Clicked() {
			seSet(se, func(s *settingsEditorState) {
				s.leaderSelected = path
				s.leaderDraft = node
				s.leaderDraft.Action = cloneLeaderAction(node.Action)
				s.leaderLoaded = true
				s.leaderError = ""
			})
		}
		if node.Action.Type == config.ActionGroup {
			a.renderLeaderTree(c, theme, node.Action.Children, path, selected)
		}
	}
}

// renderLeaderNodeEditor draws the form of the selected node.
func (a *AppController) renderLeaderNodeEditor(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	path []string,
) {
	se := a.settingsEditor()
	draft := seGet(se, func(s *settingsEditorState) config.LeaderNode { return s.leaderDraft })

	settingsInlineRow(c, theme, "key", 76, func() {
		if v, changed := settingsTextInput(c, "Node Key", "k", draft.Key); changed {
			seSet(se, func(s *settingsEditorState) { s.leaderDraft.Key = strings.TrimSpace(v) })
		}
	})
	settingsInlineRow(c, theme, "label", 76, func() {
		if v, changed := settingsTextInput(c, "Node Label", "Label", draft.Label); changed {
			seSet(se, func(s *settingsEditorState) { s.leaderDraft.Label = v })
		}
	})
	settingsInlineRow(c, theme, "icon", 76, func() {
		if v, changed := settingsTextInput(c, "Node Icon", "sparkles", draft.Icon); changed {
			seSet(se, func(s *settingsEditorState) { s.leaderDraft.Icon = strings.TrimSpace(v) })
		}
	})
	settingsInlineRow(c, theme, "action", 76, func() {
		if v, changed := settingsSelect(c, leaderActionTypes(), string(draft.Action.Type)); changed {
			seSet(se, func(s *settingsEditorState) {
				if string(s.leaderDraft.Action.Type) != v {
					s.leaderDraft.Action = defaultLeaderAction(v)
				}
			})
		}
	})

	draft = seGet(se, func(s *settingsEditorState) config.LeaderNode { return s.leaderDraft })
	switch draft.Action.Type {
	case config.ActionGroup:
		settingsHint(c, theme, fmt.Sprintf("Group with %d child node(s).", len(draft.Action.Children)))
		settingsDivider(c)
		ui.Text(c, "New Child Node").FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
		childKey := seGet(se, func(s *settingsEditorState) string { return s.leaderChildKey })
		childLabel := seGet(se, func(s *settingsEditorState) string { return s.leaderChildLbl })
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Width(60).Children(func() {
				if v, changed := settingsTextInput(c, "Child Node Key", "x", childKey); changed {
					seSet(se, func(s *settingsEditorState) { s.leaderChildKey = v })
				}
			})
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				if v, changed := settingsTextInput(c, "Child Node Label", "Child", childLabel); changed {
					seSet(se, func(s *settingsEditorState) { s.leaderChildLbl = v })
				}
			})
		})
		if ui.Button(c, "Add Child Node").Clicked() {
			a.addLeaderNode(path)
		}
	case config.ActionWindow:
		settingsInlineRow(c, theme, "command", 76, func() {
			cmds := make([]string, 0, len(config.AllWindowCommands()))
			for _, cmd := range config.AllWindowCommands() {
				cmds = append(cmds, string(cmd))
			}
			if v, changed := settingsSelect(c, cmds, string(draft.Action.WindowCommand)); changed {
				seSet(se, func(s *settingsEditorState) {
					s.leaderDraft.Action = config.WindowAction(config.WindowCommand(v))
				})
			}
		})
	case config.ActionAIRewrite:
		settingsInlineRow(c, theme, "mode", 76, func() {
			if v, changed := settingsTextInput(c, "Rewrite Mode", "polish", draft.Action.Mode); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Mode = v })
			}
		})
		settingsLabelled(c, theme, "prompt", func() {
			if v, changed := settingsTextArea(c, "Rewrite Prompt", "Improve clarity...", draft.Action.Prompt, 2); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Prompt = v })
			}
		})
	case config.ActionSnippet:
		settingsInlineRow(c, theme, "snippet_id", 76, func() {
			ids := make([]string, 0, len(cfg.Snippets.Items))
			for _, item := range cfg.Snippets.Items {
				ids = append(ids, item.ID)
			}
			current := draft.Action.SnippetID
			if current == "" && len(ids) > 0 {
				current = ids[0]
			}
			if v, changed := settingsSelect(c, ids, current); changed {
				seSet(se, func(s *settingsEditorState) {
					s.leaderDraft.Action = config.SnippetAction(v)
				})
			}
		})
	case config.ActionOpenApp:
		settingsInlineRow(c, theme, "target", 76, func() {
			if v, changed := settingsTextInput(c, "App Target", "com.apple.Safari", draft.Action.Target); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Target = v })
			}
		})
	case config.ActionSwitchTab:
		settingsInlineRow(c, theme, "tab", 76, func() {
			tabs := make([]string, 0, len(config.AllCapsuleTabs()))
			for _, tab := range config.AllCapsuleTabs() {
				tabs = append(tabs, string(tab))
			}
			current := string(draft.Action.Tab)
			if current == "" {
				current = string(config.TabActions)
			}
			if v, changed := settingsSelect(c, tabs, current); changed {
				seSet(se, func(s *settingsEditorState) {
					s.leaderDraft.Action = config.SwitchTabAction(config.CapsuleTab(v))
				})
			}
		})
	case config.ActionCLI:
		settingsInlineRow(c, theme, "command", 76, func() {
			if v, changed := settingsTextInput(c, "CLI Command", "open", draft.Action.Command); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Command = v })
			}
		})
		settingsInlineRow(c, theme, "args", 76, func() {
			if v, changed := settingsTextInput(c, "CLI Args", "-a Safari", strings.Join(draft.Action.Args, " ")); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Args = splitTokens(v) })
			}
		})
	}

	settingsDivider(c)
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, "Save Node").Clicked() {
			a.saveLeaderNode(path)
		}
		if ui.Button(c, "Move Up").Clicked() {
			a.moveLeaderSelection(-1)
		}
		if ui.Button(c, "Move Down").Clicked() {
			a.moveLeaderSelection(1)
		}
		ui.Spacer(c)
		if ui.Button(c, "Delete Node").Clicked() {
			a.deleteLeaderSelection()
		}
	})
}

// addLeaderNode appends the drafted node under parentPath and persists it.
func (a *AppController) addLeaderNode(parentPath []string) {
	se := a.settingsEditor()
	key := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.leaderNewKey }))
	label := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.leaderNewLabel }))
	childKey := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.leaderChildKey }))
	childLabel := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.leaderChildLbl }))
	if len(parentPath) > 0 {
		key, label = childKey, childLabel
	}
	if key == "" {
		seSet(se, func(s *settingsEditorState) { s.leaderError = "A node key is required." })
		return
	}
	if label == "" {
		label = key
	}
	cfg := a.ConfigStore.Current()
	if leaderChildKeyTaken(cfg.Leader.Nodes, parentPath, key) {
		seSet(se, func(s *settingsEditorState) {
			s.leaderError = fmt.Sprintf("Duplicate key %q among sibling nodes.", key)
		})
		return
	}
	child := config.LeaderNode{Key: key, Label: label, Action: config.GroupAction()}
	nodes := appendLeaderNode(cfg.Leader.Nodes, parentPath, child)
	if err := a.SaveLeaderConfig(config.LeaderConfig{Nodes: nodes}); err != nil {
		return
	}
	newPath := append(append([]string(nil), parentPath...), key)
	seSet(se, func(s *settingsEditorState) {
		s.leaderError = ""
		s.leaderSelected = newPath
		s.leaderDraft = child
		s.leaderLoaded = true
		if len(parentPath) > 0 {
			s.leaderChildKey, s.leaderChildLbl = "", ""
		} else {
			s.leaderNewKey, s.leaderNewLabel = "", ""
		}
	})
}

// saveLeaderNode validates and persists the drafted node at path.
func (a *AppController) saveLeaderNode(path []string) {
	se := a.settingsEditor()
	draft := seGet(se, func(s *settingsEditorState) config.LeaderNode { return s.leaderDraft })
	draft.Key = strings.TrimSpace(draft.Key)
	if draft.Key == "" {
		seSet(se, func(s *settingsEditorState) { s.leaderError = "A node key is required." })
		return
	}
	cfg := a.ConfigStore.Current()
	parentPath := path[:max(len(path)-1, 0)]
	if leaderKeyTaken(cfg.Leader.Nodes, path, draft.Key) {
		seSet(se, func(s *settingsEditorState) {
			s.leaderError = fmt.Sprintf("Duplicate key %q among sibling nodes.", draft.Key)
		})
		return
	}
	nodes := replaceLeaderNode(cfg.Leader.Nodes, path, draft)
	if err := a.SaveLeaderConfig(config.LeaderConfig{Nodes: nodes}); err != nil {
		return
	}
	newPath := append(append([]string(nil), parentPath...), draft.Key)
	seSet(se, func(s *settingsEditorState) {
		s.leaderError = ""
		s.leaderSelected = newPath
		s.leaderLoaded = false
	})
}

// moveLeaderSelection moves the selected node one sibling up or down.
func (a *AppController) moveLeaderSelection(delta int) {
	se := a.settingsEditor()
	path := seGet(se, func(s *settingsEditorState) []string { return append([]string(nil), s.leaderSelected...) })
	if len(path) == 0 {
		return
	}
	cfg := a.ConfigStore.Current()
	nodes := moveLeaderNode(cfg.Leader.Nodes, path, delta)
	if err := a.SaveLeaderConfig(config.LeaderConfig{Nodes: nodes}); err != nil {
		return
	}
	seSet(se, func(s *settingsEditorState) { s.leaderLoaded = false })
}

// deleteLeaderSelection removes the selected node.
func (a *AppController) deleteLeaderSelection() {
	se := a.settingsEditor()
	path := seGet(se, func(s *settingsEditorState) []string { return append([]string(nil), s.leaderSelected...) })
	if len(path) == 0 {
		return
	}
	cfg := a.ConfigStore.Current()
	nodes := deleteLeaderNode(cfg.Leader.Nodes, path)
	if err := a.SaveLeaderConfig(config.LeaderConfig{Nodes: nodes}); err != nil {
		return
	}
	seSet(se, func(s *settingsEditorState) {
		s.leaderSelected = nil
		s.leaderLoaded = false
		s.leaderError = ""
	})
}

// ---------------------------------------------------------------------------
// Snippets master-detail editor
// ---------------------------------------------------------------------------

// snippetPlaceholders lists the placeholder chips the snippet editor inserts.
func snippetPlaceholders() []string {
	return []string{"{{cursor}}", "{{clipboard}}", "{{date}}", "{{time}}", "{{datetime}}", "{{app}}"}
}

// renderSnippetsSection draws the snippets master-detail editor.
func (a *AppController) renderSnippetsSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	items := cfg.Snippets.Items

	selected := seGet(se, func(s *settingsEditorState) string { return s.snippetSelected })
	isNew := seGet(se, func(s *settingsEditorState) bool { return s.snippetIsNew })
	if !isNew {
		if selected == "" && len(items) > 0 {
			selected = items[0].ID
			seSet(se, func(s *settingsEditorState) {
				s.snippetSelected = selected
				s.snippetDraft = items[0]
			})
		}
		if selected != "" && !slices.ContainsFunc(items, func(i config.SnippetItem) bool { return i.ID == selected }) {
			seSet(se, func(s *settingsEditorState) { s.snippetSelected = "" })
			selected = ""
		}
	}

	ui.Grid(c).Columns(2).Gap(10).FillWidth().Children(func() {
		settingsSectionCard(c, theme, fmt.Sprintf("Snippets (snippets.toml) · %d item(s)", len(items)), func() {
			query := seGet(se, func(s *settingsEditorState) string { return s.snippetQuery })
			search := ui.SearchField(c, &query).Label("Search Snippets").Placeholder("Search snippets...").FillWidth()
			if search.Changed() {
				seSet(se, func(s *settingsEditorState) { s.snippetQuery = query })
			}
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, "New Snippet").Clicked() {
					seSet(se, func(s *settingsEditorState) {
						s.snippetIsNew = true
						s.snippetSelected = ""
						s.snippetDraft = config.SnippetItem{WordBoundary: true, AppBundleIDs: []string{}, ProcessNames: []string{}}
					})
				}
				settingsHint(c, theme, "Select an item to edit it.")
			})
			settingsDivider(c)
			matches := 0
			for i := range items {
				item := items[i]
				if query != "" && !strings.Contains(strings.ToLower(item.ID+" "+item.Trigger+" "+item.Description), strings.ToLower(query)) {
					continue
				}
				matches++
				row := ui.ButtonBase(c).Key("snippet:"+item.ID).FillWidth().Padding(2, 4).Radius(5).FocusRing(false)
				if item.ID == selected {
					row.Background(withAlpha(theme.Accent, 0.16))
				}
				row.Children(func() {
					ui.Text(c, item.Trigger).FontSize(12).FontWeight(600).TextColor(theme.Accent).SingleLine()
					ui.Text(c, item.Description).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)
				})
				if row.Clicked() {
					seSet(se, func(s *settingsEditorState) {
						s.snippetIsNew = false
						s.snippetSelected = item.ID
						s.snippetDraft = item
					})
				}
			}
			if matches == 0 {
				settingsHint(c, theme, "No snippets match.")
			}
		})

		settingsSectionCard(c, theme, "Snippet Editor", func() {
			draft := seGet(se, func(s *settingsEditorState) config.SnippetItem { return s.snippetDraft })
			settingsInlineRow(c, theme, "id", 74, func() {
				if v, changed := settingsTextInput(c, "Snippet ID", "my-snippet", draft.ID); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.ID = v })
				}
			})
			settingsInlineRow(c, theme, "trigger", 74, func() {
				if v, changed := settingsTextInput(c, "Snippet Trigger", ";sig", draft.Trigger); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.Trigger = v })
				}
			})
			settingsInlineRow(c, theme, "description", 74, func() {
				if v, changed := settingsTextInput(c, "Snippet Description", "What it expands to", draft.Description); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.Description = v })
				}
			})
			if v, changed := settingsSwitch(c, theme, "word_boundary", "Word Boundary", draft.WordBoundary); changed {
				seSet(se, func(s *settingsEditorState) { s.snippetDraft.WordBoundary = v })
			}
			settingsInlineRow(c, theme, "app_bundle_ids", 74, func() {
				if v, changed := settingsTextInput(c, "Snippet App Bundle IDs", "com.apple.Safari", strings.Join(draft.AppBundleIDs, ", ")); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.AppBundleIDs = splitTokens(v) })
				}
			})
			settingsInlineRow(c, theme, "process_names", 74, func() {
				if v, changed := settingsTextInput(c, "Snippet Process Names", "Safari", strings.Join(draft.ProcessNames, ", ")); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.ProcessNames = splitTokens(v) })
				}
			})

			draft = seGet(se, func(s *settingsEditorState) config.SnippetItem { return s.snippetDraft })
			content := draft.Content
			area := ui.TextArea(c, &content).Label("Snippet Content").Placeholder("Expanded text...").Lines(2, 6).FillWidth()
			if area.Changed() {
				seSet(se, func(s *settingsEditorState) { s.snippetDraft.Content = content })
			}
			ui.Grid(c).Columns(3).GapX(4).GapY(3).FillWidth().Children(func() {
				for _, chip := range snippetPlaceholders() {
					ph := chip
					if ui.Button(c, ph).Key("snippet-chip:" + ph).FontSize(10).Clicked() {
						seSet(se, func(s *settingsEditorState) { s.snippetDraft.Content += ph })
					}
				}
			})

			rendered := engine.RenderSnippet(draft.Content, engine.RenderOptions{
				TriggerLength: len(draft.Trigger),
				AppName:       "Preview",
				ClipboardText: "clipboard",
			})
			ui.Box(c).
				FillWidth().
				Padding(4, 8).
				Radius(6).
				Background(theme.Background).
				Border(1, theme.Border).
				Children(func() {
					ui.Text(c, "Live Preview").FontSize(10).TextColor(theme.TextMuted).SingleLine()
					ui.Text(c, rendered.RenderedText).FontSize(12).TextColor(theme.Text)
				})

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, "Save Snippet").Clicked() {
					a.saveSnippetDraft()
				}
				if ui.Button(c, "Delete Snippet").Disabled(seGet(se, func(s *settingsEditorState) bool { return s.snippetIsNew })).Clicked() {
					a.deleteSnippetDraft()
				}
			})
		})
	})
}

// saveSnippetDraft persists the drafted snippet into snippets.toml.
func (a *AppController) saveSnippetDraft() {
	se := a.settingsEditor()
	draft := seGet(se, func(s *settingsEditorState) config.SnippetItem { return s.snippetDraft })
	draft.ID = strings.TrimSpace(draft.ID)
	draft.Trigger = strings.TrimSpace(draft.Trigger)
	if draft.ID == "" || draft.Trigger == "" {
		a.setSettingsStatus("Snippet id and trigger are required")
		return
	}
	if draft.AppBundleIDs == nil {
		draft.AppBundleIDs = []string{}
	}
	if draft.ProcessNames == nil {
		draft.ProcessNames = []string{}
	}
	cfg := a.ConfigStore.Current()
	items := make([]config.SnippetItem, 0, len(cfg.Snippets.Items)+1)
	replaced := false
	for _, item := range cfg.Snippets.Items {
		if item.ID == draft.ID {
			items = append(items, draft)
			replaced = true
			continue
		}
		items = append(items, item)
	}
	if !replaced {
		items = append(items, draft)
	}
	if err := a.SaveSnippetsConfig(config.SnippetsConfig{Items: items}); err != nil {
		return
	}
	seSet(se, func(s *settingsEditorState) {
		s.snippetIsNew = false
		s.snippetSelected = draft.ID
		s.snippetDraft = draft
	})
}

// deleteSnippetDraft removes the selected snippet from snippets.toml.
func (a *AppController) deleteSnippetDraft() {
	se := a.settingsEditor()
	target := seGet(se, func(s *settingsEditorState) string { return s.snippetSelected })
	cfg := a.ConfigStore.Current()
	items := make([]config.SnippetItem, 0, len(cfg.Snippets.Items))
	for _, item := range cfg.Snippets.Items {
		if item.ID != target {
			items = append(items, item)
		}
	}
	if err := a.SaveSnippetsConfig(config.SnippetsConfig{Items: items}); err != nil {
		return
	}
	seSet(se, func(s *settingsEditorState) {
		s.snippetIsNew = false
		s.snippetSelected = ""
		s.snippetDraft = config.SnippetItem{}
	})
}

// ---------------------------------------------------------------------------
// Glossary & Clipboard
// ---------------------------------------------------------------------------

// renderGlossarySection draws glossary CRUD beside the clipboard controls.
func (a *AppController) renderGlossarySection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	terms := cfg.Glossary.Terms
	selected := seGet(se, func(s *settingsEditorState) string { return s.glossarySelected })
	isNew := seGet(se, func(s *settingsEditorState) bool { return s.glossaryIsNew })
	if !isNew && selected == "" && len(terms) > 0 {
		selected = terms[0].Term
		seSet(se, func(s *settingsEditorState) {
			s.glossarySelected = selected
			s.glossaryDraft = terms[0]
		})
	}

	ui.Grid(c).Columns(2).Gap(10).FillWidth().Children(func() {
		settingsSectionCard(c, theme, fmt.Sprintf("Glossary (glossary.toml) · %d term(s)", len(terms)), func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, "New Term").Clicked() {
					seSet(se, func(s *settingsEditorState) {
						s.glossaryIsNew = true
						s.glossarySelected = ""
						s.glossaryDraft = config.GlossaryTerm{Aliases: []string{}}
					})
				}
				settingsHint(c, theme, "Select a term to edit it.")
			})
			settingsDivider(c)
			for _, term := range terms {
				t := term
				row := ui.ButtonBase(c).Key("glossary:"+t.Term).FillWidth().Padding(2, 4).Radius(5).FocusRing(false)
				if t.Term == selected {
					row.Background(withAlpha(theme.Accent, 0.16))
				}
				row.Children(func() {
					ui.Text(c, t.Term).FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
					ui.Text(c, strings.Join(t.Aliases, ", ")).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)
					ui.Text(c, t.Category).FontSize(10).TextColor(theme.TextMuted).SingleLine()
				})
				if row.Clicked() {
					seSet(se, func(s *settingsEditorState) {
						s.glossaryIsNew = false
						s.glossarySelected = t.Term
						s.glossaryDraft = t
					})
				}
			}

			settingsDivider(c)
			draft := seGet(se, func(s *settingsEditorState) config.GlossaryTerm { return s.glossaryDraft })
			settingsInlineRow(c, theme, "term", 76, func() {
				if v, changed := settingsTextInput(c, "Glossary Term", "Kubernetes", draft.Term); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Term = v })
				}
			})
			settingsInlineRow(c, theme, "aliases", 76, func() {
				if v, changed := settingsTextInput(c, "Glossary Aliases", "k8s, kube", strings.Join(draft.Aliases, ", ")); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Aliases = splitTokens(v) })
				}
			})
			settingsInlineRow(c, theme, "category", 76, func() {
				if v, changed := settingsTextInput(c, "Glossary Category", "product", draft.Category); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Category = v })
				}
			})
			settingsInlineRow(c, theme, "hint", 76, func() {
				if v, changed := settingsTextInput(c, "Glossary Hint", "Keep this spelling", draft.Hint); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Hint = v })
				}
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, "Save Term").Clicked() {
					a.saveGlossaryDraft()
				}
				if ui.Button(c, "Delete Term").Disabled(seGet(se, func(s *settingsEditorState) bool { return s.glossaryIsNew })).Clicked() {
					a.deleteGlossaryDraft()
				}
			})
		})

		a.renderClipboardCard(c, theme, cfg)
	})
}

// saveGlossaryDraft persists the drafted glossary term.
func (a *AppController) saveGlossaryDraft() {
	se := a.settingsEditor()
	draft := seGet(se, func(s *settingsEditorState) config.GlossaryTerm { return s.glossaryDraft })
	draft.Term = strings.TrimSpace(draft.Term)
	if draft.Term == "" {
		a.setSettingsStatus("A glossary term is required")
		return
	}
	if draft.Category == "" {
		draft.Category = "general"
	}
	if draft.Aliases == nil {
		draft.Aliases = []string{}
	}
	cfg := a.ConfigStore.Current()
	terms := make([]config.GlossaryTerm, 0, len(cfg.Glossary.Terms)+1)
	replaced := false
	for _, term := range cfg.Glossary.Terms {
		if term.Term == draft.Term {
			terms = append(terms, draft)
			replaced = true
			continue
		}
		terms = append(terms, term)
	}
	if !replaced {
		terms = append(terms, draft)
	}
	if err := a.SaveGlossaryConfig(config.GlossaryConfig{Terms: terms}); err != nil {
		return
	}
	seSet(se, func(s *settingsEditorState) {
		s.glossaryIsNew = false
		s.glossarySelected = draft.Term
		s.glossaryDraft = draft
	})
}

// deleteGlossaryDraft removes the selected glossary term.
func (a *AppController) deleteGlossaryDraft() {
	se := a.settingsEditor()
	target := seGet(se, func(s *settingsEditorState) string { return s.glossarySelected })
	cfg := a.ConfigStore.Current()
	terms := make([]config.GlossaryTerm, 0, len(cfg.Glossary.Terms))
	for _, term := range cfg.Glossary.Terms {
		if term.Term != target {
			terms = append(terms, term)
		}
	}
	if err := a.SaveGlossaryConfig(config.GlossaryConfig{Terms: terms}); err != nil {
		return
	}
	seSet(se, func(s *settingsEditorState) {
		s.glossaryIsNew = false
		s.glossarySelected = ""
		s.glossaryDraft = config.GlossaryTerm{}
	})
}

// renderClipboardCard draws the [clipboard] controls.
func (a *AppController) renderClipboardCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	clip := cfg.Main.Clipboard

	settingsSectionCard(c, theme, "Clipboard History (clipboard.*)", func() {
		if v, changed := settingsSwitch(c, theme, "Clipboard History (clipboard.enabled)", "Clipboard Enabled", clip.Enabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.Enabled = v })
		}
		if v, changed := settingsSwitch(c, theme, "Ignore Concealed Types (ignore_concealed_types)", "Ignore Concealed Types", clip.IgnoreConcealedTypes); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.IgnoreConcealedTypes = v })
		}
		if v, changed := settingsStepper(c, theme, "max_history_items", float64(clip.MaxHistoryItems), 10, 2000, 10, ""); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.MaxHistoryItems = int(v) })
		}
		if v, changed := settingsStepper(c, theme, "retention_days", float64(clip.RetentionDays), 1, 365, 1, "days"); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.RetentionDays = int(v) })
		}

		settingsDivider(c)
		ui.Text(c, "Excluded Apps (excluded_bundle_ids)").FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				if v, changed := settingsTextInput(c, "Excluded Bundle ID", "com.example.app", seGet(se, func(s *settingsEditorState) string { return s.clipExcludeDraft })); changed {
					seSet(se, func(s *settingsEditorState) { s.clipExcludeDraft = v })
				}
			})
			pending := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.clipExcludeDraft }))
			if ui.Button(c, "Add Exclusion").Disabled(pending == "").Clicked() {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) {
					if slices.Contains(m.Clipboard.ExcludedBundleIDs, pending) {
						return
					}
					m.Clipboard.ExcludedBundleIDs = append(append([]string(nil), m.Clipboard.ExcludedBundleIDs...), pending)
				})
				seSet(se, func(s *settingsEditorState) { s.clipExcludeDraft = "" })
			}
		})
		for i, bundleID := range clip.ExcludedBundleIDs {
			idx, id := i, bundleID
			ui.Row(c).Key(fmt.Sprintf("clip-excl:%d:%s", idx, id)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, id).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)
				if ui.Button(c, "Remove").Key(fmt.Sprintf("clip-excl-remove:%d", idx)).Clicked() {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) {
						kept := make([]string, 0, len(m.Clipboard.ExcludedBundleIDs))
						for j, item := range m.Clipboard.ExcludedBundleIDs {
							if j != idx {
								kept = append(kept, item)
							}
						}
						m.Clipboard.ExcludedBundleIDs = kept
					})
				}
			})
		}
	})
}
