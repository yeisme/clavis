package uiapp

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// settingsEditorState holds the draft/editor state of the Settings
// configuration center. It lives in this file keyed by *AppController so that
// controller.go keeps its own state untouched, and it is safe to read from any
// goroutine the UI uses.
type settingsEditorState struct {
	mu             sync.Mutex
	railQuery      string
	pendingSection int
	leavePrompt    bool

	// recordingShortcut names the trigger field ("capsule_shortcut" or
	// "clipboard_shortcut") whose recorder waits for the next modifier+key chord.
	recordingShortcut string

	// "my words" editor: the draft being typed and the last validation message. They live in
	// the shared settings editor state so a repaint cannot drop what the user typed.
	myWordDraft string
	myWordError string

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

	// workflows master-detail editor. The draft is always a deep clone of the
	// stored workflow, so editing it never mutates the last valid config.
	workflowSelected   string // stored ID of the edited workflow
	workflowOriginalID string // ID to replace on save (draft.ID may differ)
	workflowIsNew      bool
	workflowLoaded     bool
	workflowDraft      config.Workflow
	workflowStepIndex  int
	workflowError      string
	// workflowConfirmDelete gates the destructive Delete Workflow action: the
	// first click only asks, and Cancel leaves the stored config untouched.
	workflowConfirmDelete bool

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
	ui.Column(c).FillWidth().Gap(space2).Children(func() {
		ui.Text(c, label).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
		body()
	})
}

// settingsFieldLabelWidth is the label column of the item editors (AI
// profile, Leader node, snippet, glossary term).
const settingsFieldLabelWidth float32 = 84

// settingsInlineNumber renders a number with its unit and a stepper named
// label, for the item editors, and reports a change.
func settingsInlineNumber(c *ui.Context, theme *ui.Theme, label string, value, lo, hi, step float64, unit string) (float64, bool) {
	next, changed := value, false
	ui.Row(c).Gap(space6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, formatSettingNumber(value, step, unit)).FontSize(typeBody).TextColor(theme.Text).SingleLine()
		v := value
		if ui.Stepper(c, &v, lo, hi, step).Label(label).Changed() {
			next, changed = v, true
		}
	})
	return next, changed
}

// settingsInlineRow renders a muted caption left of body, width wide.
func settingsInlineRow(c *ui.Context, theme *ui.Theme, label string, width float32, body func()) {
	ui.Row(c).FillWidth().Height(controlHeight).Gap(space8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(typeLabel).TextColor(theme.TextMuted).Width(width).SingleLine()
		body()
	})
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
	applyProseLineHeight(ui.Text(c, text)).FontSize(typeMeta).TextColor(theme.TextMuted)
}

// settingsBanner renders a colored note with a border of the same color.
func settingsBanner(c *ui.Context, color ui.Color, text string) {
	_ = color
	kitErrorBanner(c, c.Theme(), text)
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
		return "backspace", true
	case ui.KeyDelete:
		return "forwarddelete", true
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
	case ui.KeyPageUp:
		return "pageup", true
	case ui.KeyPageDown:
		return "pagedown", true
	case ui.KeyF1:
		return "f1", true
	case ui.KeyF2:
		return "f2", true
	case ui.KeyF3:
		return "f3", true
	case ui.KeyF4:
		return "f4", true
	case ui.KeyF5:
		return "f5", true
	case ui.KeyF6:
		return "f6", true
	case ui.KeyF7:
		return "f7", true
	case ui.KeyF8:
		return "f8", true
	case ui.KeyF9:
		return "f9", true
	case ui.KeyF10:
		return "f10", true
	case ui.KeyF11:
		return "f11", true
	case ui.KeyF12:
		return "f12", true
	case ui.KeyMinus:
		return "minus", true
	case ui.KeyEqual:
		return "equal", true
	case ui.KeyBracketLeft:
		return "leftbracket", true
	case ui.KeyBracketRight:
		return "rightbracket", true
	case ui.KeySemicolon:
		return "semicolon", true
	case ui.KeyQuote:
		return "quote", true
	case ui.KeyComma:
		return "comma", true
	case ui.KeyPeriod:
		return "period", true
	case ui.KeySlash:
		return "slash", true
	case ui.KeyBackslash:
		return "backslash", true
	case ui.KeyBackquote:
		return "grave", true
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

	settingsSourceCard(c, theme, i18n.T("settings.card.keymap"), "[keymap]", func() {
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.keymap.enabled"), "enabled", "", keymap.Enabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.Enabled = v })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.keymap.nav_layer"), "nav_layer_enabled", "", keymap.NavLayerEnabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.NavLayerEnabled = v })
		}
		if v, changed := settingNumberRow(c, theme, i18n.T("settings.keymap.tap_hold_threshold"), "tap_hold_threshold_ms",
			i18n.T("settings.keymap.tap_hold_threshold_help"), float64(keymap.TapHoldThresholdMs), 80, 500, 10, "ms"); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.TapHoldThresholdMs = int(v) })
		}
		capsSelectW := float32(104)
		if settingsNarrowCard(c) {
			capsSelectW = 0
		}
		if v, changed := settingSelectRow(c, theme, i18n.T("settings.keymap.caps_lock_tap"), "caps_lock_tap", "",
			capsLockActionOptions(), keymap.CapsLockTap, capsSelectW); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.CapsLockTap = v })
		}
		if v, changed := settingSelectRow(c, theme, i18n.T("settings.keymap.caps_lock_hold"), "caps_lock_hold", "",
			capsLockActionOptions(), keymap.CapsLockHold, capsSelectW); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Keymap.CapsLockHold = v })
		}

		settingsCardDivider(c, theme)
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.keymap.custom_remaps")).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
			ui.Text(c, "[[keymap.remaps]]").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		})

		remapDrafts := seGet(se, func(s *settingsEditorState) [4]string {
			return [4]string{s.remapFrom, s.remapTap, s.remapHold, s.remapApps}
		})
		from, tap, hold, apps := remapDrafts[0], remapDrafts[1], remapDrafts[2], remapDrafts[3]
		remapCols := 2
		if settingsNarrowCard(c) {
			remapCols = 1
		}
		ui.Grid(c).Columns(remapCols).Gap(6).FillWidth().Children(func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.keymap.remap_from"), i18n.T("settings.keymap.remap_from")+": "+"caps_lock", from); changed {
				seSet(se, func(s *settingsEditorState) { s.remapFrom = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.keymap.remap_tap"), i18n.T("settings.keymap.remap_tap")+": "+"escape", tap); changed {
				seSet(se, func(s *settingsEditorState) { s.remapTap = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.keymap.remap_hold"), i18n.T("settings.keymap.remap_hold")+": "+"nav", hold); changed {
				seSet(se, func(s *settingsEditorState) { s.remapHold = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.keymap.remap_apps"), i18n.T("settings.keymap.remap_apps")+": "+"com.apple.Safari, com.apple.Terminal", apps); changed {
				seSet(se, func(s *settingsEditorState) { s.remapApps = v })
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			newFrom := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.remapFrom }))
			if ui.PrimaryButton(c, i18n.T("settings.keymap.add_remap")).Disabled(newFrom == "").Clicked() {
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
			settingsHint(c, theme, i18n.Tf("settings.keymap.remap_count", len(keymap.Remaps)))
		})

		if len(keymap.Remaps) == 0 {
			settingsHint(c, theme, i18n.T("settings.keymap.no_remaps"))
			return
		}
		for idx, rule := range keymap.Remaps {
			i, r := idx, rule
			summary := fmt.Sprintf("%s → tap=%s hold=%s", r.From, r.Tap, r.Hold)
			if len(r.AppBundleIDs) > 0 {
				summary += " (" + strings.Join(r.AppBundleIDs, ", ") + ")"
			}
			ui.Row(c).Key(fmt.Sprintf("remap:%d:%s", i, r.From)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, summary).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1)
				if ui.Button(c, i18n.T("common.delete")).Key(fmt.Sprintf("del-remap:%d", i)).Padding(space2, space10).Shrink(0).Clicked() {
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

// renderIMEConfig draws the global IME settings: switches, the switch
// delay, the current and the default input source.
func (a *AppController) renderIMEConfig(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	imeCfg := cfg.Main.IME

	settingsSourceCard(c, theme, i18n.T("settings.card.ime"), "[ime]", func() {
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.ime.enabled"), "enabled", "", imeCfg.Enabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.Enabled = v })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.ime.lock_ascii"), "lock_ascii_on_leader",
			i18n.T("settings.ime.lock_ascii_help"), imeCfg.LockASCIIOnLeader); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.LockASCIIOnLeader = v })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.ime.floating_pill"), "show_floating_pill", "",
			imeCfg.ShowFloatingPill); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.ShowFloatingPill = v })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.ime.indicator_hud"), "show_indicator_hud", "",
			imeCfg.ShowIndicatorHUD); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.ShowIndicatorHUD = v })
		}
		if v, changed := settingNumberRow(c, theme, i18n.T("settings.ime.switch_delay"), "switch_delay_ms",
			i18n.T("settings.ime.switch_delay_help"), float64(imeCfg.SwitchDelayMs), 0, 500, 10, "ms"); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.SwitchDelayMs = int(v) })
		}

		settingsCardDivider(c, theme)
		ctx := context.Background()
		if cur, err := a.CurrentInputSource(ctx); err == nil {
			settingsKeyValueRow(c, theme, i18n.T("settings.ime.current_source"), FormatInputSourceStatus(cur))
		}
		if st, ok := a.ForegroundIMEStatus(); ok {
			for _, row := range foregroundIMERows(st) {
				settingsKeyValueRow(c, theme, row[0], row[1])
			}
		}
		labels, ids := inputSourceOptions(a.AvailableInputSources(ctx), imeCfg.DefaultInputSourceID)
		options := make([]settingOption, len(ids))
		for i := range ids {
			options[i] = settingOption{ids[i], labels[i]}
			if ids[i] == "" {
				options[i].label = i18n.T("settings.option.none")
			}
		}
		if v, changed := settingSelectRow(c, theme, i18n.T("settings.ime.default_source"), "default_input_source_id", "",
			options, imeCfg.DefaultInputSourceID, 0); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.IME.DefaultInputSourceID = v })
		}
	})
}

// renderIMERuleForm draws the new-rule form of the per-app rules card: a
// quick pick of installed apps, the target and patterns, the mode, and the
// regex checks of the patterns as they are typed.
func (a *AppController) renderIMERuleForm(c *ui.Context, theme *ui.Theme) {
	apps := a.State().InstalledApps
	picked := ""
	if len(apps) > 0 {
		ui.Text(c, i18n.T("settings.ime.app_picker")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
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
					picked = imeRuleTargetForApp(entry, runtime.GOOS)
				}
			}
		})
	}
	if picked != "" {
		a.mu.Lock()
		if target := strings.TrimSpace(picked); target != "" {
			a.uiState.imeRuleTargetDraft = target
		}
		a.mu.Unlock()
	}

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

	draftInput := func(value *string, label, placeholder string, grow float32, store func(string)) {
		in := ui.TextInput(c, value).Placeholder(placeholder).Label(label).Grow(grow).MinWidth(0)
		if in.Changed() {
			v := *value
			a.mu.Lock()
			store(v)
			a.mu.Unlock()
		}
	}
	if settingsNarrowCard(c) {
		ui.Column(c).FillWidth().Gap(space6).Children(func() {
			draftInput(&targetDraft, i18n.T("settings.ime.rule_target"), i18n.T("settings.ime.rule_target_ph"), 1,
				func(v string) { a.uiState.imeRuleTargetDraft = v })
			draftInput(&urlDraft, i18n.T("settings.ime.url_pattern"), i18n.T("settings.ime.url_pattern_ph"), 1,
				func(v string) { a.uiState.imeRuleURLDraft = v })
			draftInput(&titleDraft, i18n.T("settings.ime.title_pattern"), i18n.T("settings.ime.title_pattern_ph"), 1,
				func(v string) { a.uiState.imeRuleTitleDraft = v })
			draftInput(&sourceDraft, i18n.T("settings.ime.source_id"), i18n.T("settings.ime.source_id_ph"), 1,
				func(v string) { a.uiState.imeRuleSourceDraft = v })
		})
	} else {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			draftInput(&targetDraft, i18n.T("settings.ime.rule_target"), i18n.T("settings.ime.rule_target_ph"), 3,
				func(v string) { a.uiState.imeRuleTargetDraft = v })
			draftInput(&urlDraft, i18n.T("settings.ime.url_pattern"), i18n.T("settings.ime.url_pattern_ph"), 2,
				func(v string) { a.uiState.imeRuleURLDraft = v })
		})
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			draftInput(&titleDraft, i18n.T("settings.ime.title_pattern"), i18n.T("settings.ime.title_pattern_ph"), 2,
				func(v string) { a.uiState.imeRuleTitleDraft = v })
			draftInput(&sourceDraft, i18n.T("settings.ime.source_id"), i18n.T("settings.ime.source_id_ph"), 3,
				func(v string) { a.uiState.imeRuleSourceDraft = v })
		})
	}

	if msg := regexIssue(titleDraft); msg != "" {
		settingsBanner(c, theme.Danger, i18n.Tf("settings.ime.title_regex_bad", msg))
	} else if strings.TrimSpace(titleDraft) != "" {
		settingsHint(c, theme, i18n.T("settings.ime.title_regex_ok"))
	}
	if msg := regexIssue(urlDraft); msg != "" {
		settingsBanner(c, theme.Danger, i18n.Tf("settings.ime.url_regex_bad", msg))
	}

	ui.Row(c).FillWidth().Children(func() {
		modes := config.AllIMEModes()
		labels := make([]string, len(modes))
		sel := 0
		for i, m := range modes {
			labels[i] = imeModeLabel(m)
			if m == modeDraft {
				sel = i
			}
		}
		if ui.Segmented(c, &sel, labels...).FillEqually().Label(i18n.T("settings.ime.rule_mode")).Changed() && sel < len(modes) {
			a.mu.Lock()
			a.uiState.imeRuleModeDraft = modes[sel]
			a.mu.Unlock()
		}
	})
	ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c)

		curTarget := strings.TrimSpace(targetDraft)
		if ui.PrimaryButton(c, i18n.T("settings.ime.add_rule")).Disabled(curTarget == "").Clicked() {
			newRule := config.IMERule{
				AppName:        curTarget,
				URLPattern:     strings.TrimSpace(urlDraft),
				TitlePattern:   strings.TrimSpace(titleDraft),
				InputSourceID:  strings.TrimSpace(sourceDraft),
				PreferredMode:  modeDraft,
				RestoreOnLeave: true,
			}
			newRule.BundleID, newRule.ProcessName = imeRuleTargetFields(curTarget, runtime.GOOS)
			_ = a.AddIMERule(newRule)
		}
	})
}

// inputSourceOptions returns parallel select labels and IDs for the default input source picker.
// Labels are readable ("中文(简体，中国) (0x08040804)") and always unique; current is kept even
// when it is not installed so the select never drops the configured value.
func inputSourceOptions(sources []host.InputSourceInfo, current string) (labels, ids []string) {
	labels = []string{""}
	ids = []string{""}
	for _, src := range sources {
		id := strings.TrimSpace(src.ID)
		if id == "" || slices.Contains(ids, id) {
			continue
		}
		label := id
		if name := strings.TrimSpace(src.Name); name != "" && name != id {
			label = name + " (" + id + ")"
		}
		labels = append(labels, label)
		ids = append(ids, id)
	}
	if !slices.Contains(ids, current) {
		labels = append(labels, current)
		ids = append(ids, current)
	}
	return labels, ids
}

// imeRuleTargetFields maps a typed rule target to (bundle_id, process_name). Windows has no bundle
// IDs, so "Code.exe" must become process_name there; elsewhere a dotted target is a bundle ID.
func imeRuleTargetFields(target, goos string) (bundleID, processName string) {
	target = strings.TrimSpace(target)
	if goos == "windows" || !strings.Contains(target, ".") {
		return "", target
	}
	return target, ""
}

// imeRuleTargetForApp picks the rule target for an installed app: its bundle ID on macOS, its
// executable name on Windows (mygo reports .lnk paths / AUMIDs as IDs there).
func imeRuleTargetForApp(app engine.InstalledAppEntry, goos string) string {
	if goos != "windows" {
		return app.BundleID
	}
	for _, p := range []string{app.Path, app.BundleID} {
		p = strings.TrimSpace(p)
		if strings.EqualFold(filepath.Ext(p), ".exe") {
			return filepath.Base(strings.ReplaceAll(p, "\\", "/"))
		}
	}
	if name := strings.TrimSpace(app.Name); name != "" {
		return name + ".exe"
	}
	return app.BundleID
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
	// Global behavior on the left, per-app rules on the right: the rule form
	// and the newest rules stay in view without scrolling.
	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(10).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			a.renderKeymapControls(c, theme, cfg)
			a.renderIMEConfig(c, theme, cfg)
			a.renderInputEngineCard(c, theme, cfg)
		})
		a.renderIMERulesCard(c, theme, cfg)
	})
}

// renderIMERulesCard draws the per-app rules: the new-rule form, then the
// configured rules with delete buttons, newest first.
func (a *AppController) renderIMERulesCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	settingsSourceCard(c, theme, i18n.T("settings.card.ime_rules"), "ime-rules.toml", func() {
		a.renderIMERuleForm(c, theme)
		settingsCardDivider(c, theme)
		rules := cfg.IMERules.Rules
		if len(rules) == 0 {
			kitEmptyState(c, theme, "keyboard", i18n.T("settings.ime.no_rules"), "")
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
			if rule.InputSourceID != "" {
				details = append(details, rule.InputSourceID)
			}
			if rule.URLPattern != "" {
				details = append(details, "url="+rule.URLPattern)
			}
			if rule.TitlePattern != "" {
				details = append(details, "title="+rule.TitlePattern)
			}
			if rule.RestoreOnLeave {
				details = append(details, i18n.T("settings.ime.restore_on_leave"))
			}
			tone := chipNeutral

			ui.Row(c).Key(fmt.Sprintf("ime-rule:%d:%s", idx, targetLabel)).FillWidth().Gap(space8).AlignItems(ui.Center).Padding(space2, 0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(space2).Children(func() {
					ui.Text(c, targetLabel).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
					ui.Row(c).Gap(space6).AlignItems(ui.Center).Children(func() {
						chip(c, theme, "", imeModeLabel(rule.PreferredMode), tone).Shrink(0)
						ui.Text(c, strings.Join(details, " · ")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().MinWidth(0)
					})
				})
				if ui.Button(c, i18n.T("common.delete")).Key(fmt.Sprintf("del-ime-rule:%d", idx)).
					Padding(space2, space10).Shrink(0).Clicked() {
					_ = a.DeleteIMERule(idx)
				}
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

	settingsEditorCard(c, theme, i18n.T("settings.card.profile_config"), func() {
		settingsHint(c, theme, i18n.Tf("settings.ai.editing_profile", active.ID))
		ui.Column(c).FillWidth().Gap(space4).Children(func() {
			settingsInlineRow(c, theme, i18n.T("settings.field.name"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_name"), active.Name, active.Name); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Name = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.provider"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_provider"), "deepseek", active.Provider); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Provider = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.protocol"), settingsFieldLabelWidth, func() {
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
			settingsInlineRow(c, theme, i18n.T("settings.field.model"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_model"), "model-name", active.Model); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Model = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.temperature"), settingsFieldLabelWidth, func() {
				if v, changed := settingsInlineNumber(c, theme, i18n.T("settings.field.temperature"), active.Temperature, 0, 2, 0.1, ""); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Temperature = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.timeout_s"), settingsFieldLabelWidth, func() {
				if v, changed := settingsInlineNumber(c, theme, i18n.T("settings.field.timeout_s"), active.TimeoutSeconds, 5, 300, 5, "s"); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.TimeoutSeconds = v })
				}
			})
		})
		settingsInlineRow(c, theme, i18n.T("settings.field.base_url"), settingsFieldLabelWidth, func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_base_url"), "https://api.example.com/v1", active.BaseURL); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.BaseURL = strings.TrimSpace(v) })
			}
		})
		settingsInlineRow(c, theme, i18n.T("settings.field.keychain"), settingsFieldLabelWidth, func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_keychain"), "clavis.ai.profile", active.KeychainAccount); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.KeychainAccount = strings.TrimSpace(v) })
			}
		})
		settingsLabelled(c, theme, i18n.T("settings.field.system_prompt"), func() {
			if v, changed := settingsTextArea(c, i18n.T("settings.ai.system_prompt"), i18n.T("settings.ai.system_prompt_ph"), active.SystemPrompt, 2); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.SystemPrompt = v })
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, i18n.T("settings.ai.delete_profile")).Disabled(len(cfg.Main.AI.Profiles) <= 1).Clicked() {
				a.deleteAIProfile(active.ID)
			}
			settingsHint(c, theme, i18n.Tf("settings.ai.profile_count", len(cfg.Main.AI.Profiles)))
		})
	})
}

// renderAIAddProfileCard draws the form that appends a custom AI profile.
func (a *AppController) renderAIAddProfileCard(c *ui.Context, theme *ui.Theme) {
	se := a.settingsEditor()
	settingsEditorCard(c, theme, i18n.T("settings.card.add_profile"), func() {
		draft := seGet(se, func(s *settingsEditorState) config.AIProfile { return s.aiDraftProfile })
		if draft.ID == "" && draft.Provider == "" && draft.BaseURL == "" && draft.Model == "" {
			draft = config.AIProfile{Protocol: config.ProtocolOpenAICompatible, Temperature: 0.2, TimeoutSeconds: 30}
		}
		ui.Grid(c).Columns(2).GapX(8).GapY(2).FillWidth().Children(func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_id"), "my-profile", draft.ID); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.ID = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_provider"), "openai", draft.Provider); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.Provider = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_base_url"), "https://api.example.com/v1", draft.BaseURL); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.BaseURL = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_model"), "model-name", draft.Model); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.Model = v })
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, i18n.T("settings.ai.add_profile")).Clicked() {
				a.addAIProfileDraft()
			}
			settingsHint(c, theme, i18n.T("settings.ai.new_profile_hint"))
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
		string(config.ActionRunWorkflow),
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

// leaderWorkflowIDs returns configured workflow IDs in declaration order.
func leaderWorkflowIDs(cfg config.ClavisConfig) []string {
	ids := make([]string, 0, len(cfg.Workflows.Workflows))
	for _, workflow := range cfg.Workflows.Workflows {
		if strings.TrimSpace(workflow.ID) != "" {
			ids = append(ids, workflow.ID)
		}
	}
	return ids
}

// leaderWorkflowOptions keeps a stale selected ID visible so the editor can
// report it as invalid instead of silently changing the saved action.
func leaderWorkflowOptions(ids []string, current string) ([]string, bool) {
	valid := slices.Contains(ids, current)
	if !valid && current != "" {
		return append([]string{current}, ids...), false
	}
	return ids, valid
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
	case config.ActionRunWorkflow:
		return config.RunWorkflowAction("")
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

	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(10).FillWidth().Children(func() {
		settingsEditorCard(c, theme, i18n.Tf("settings.leader.card_title", len(cfg.Leader.Nodes)), func() {
			a.renderLeaderTree(c, theme, cfg.Leader.Nodes, nil, selectedPath)

			settingsDivider(c)
			ui.Text(c, i18n.T("settings.leader.new_root")).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
			newKey := seGet(se, func(s *settingsEditorState) string { return s.leaderNewKey })
			newLabel := seGet(se, func(s *settingsEditorState) string { return s.leaderNewLabel })
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
				ui.Column(c).Gap(2).Width(60).Children(func() {
					if v, changed := settingsTextInput(c, i18n.T("settings.leader.new_key"), "z", newKey); changed {
						seSet(se, func(s *settingsEditorState) { s.leaderNewKey = v })
					}
				})
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					if v, changed := settingsTextInput(c, i18n.T("settings.leader.new_label"), i18n.T("settings.leader.new_label_ph"), newLabel); changed {
						seSet(se, func(s *settingsEditorState) { s.leaderNewLabel = v })
					}
				})
			})
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.leader.add_root")).Clicked() {
					a.addLeaderNode(nil)
				}
				settingsHint(c, theme, i18n.T("settings.leader.keys_unique"))
			})
			if msg := seGet(se, func(s *settingsEditorState) string { return s.leaderError }); msg != "" {
				settingsBanner(c, theme.Danger, msg)
			}
		})

		settingsEditorCard(c, theme, i18n.T("settings.leader.node_editor"), func() {
			if len(selectedPath) == 0 {
				settingsHint(c, theme, i18n.T("settings.leader.select_node"))
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
		row := settingsListRow(c, theme, "leader-node:"+strings.Join(path, "/"), isSelected)
		depth := len(prefix)
		row.Children(func() {
			if depth > 0 {
				// A fixed indent: a Spacer would grow into the free width.
				ui.Box(c).Width(float32(depth) * 18).Shrink(0)
			}
			keycap(c, theme, node.Key, keyToneNeutral)
			ui.Text(c, node.Label).FontSize(typeLabel).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
			ui.Text(c, string(node.Action.Type)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
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

	settingsInlineRow(c, theme, i18n.T("settings.field.key"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.leader.node_key"), "k", draft.Key); changed {
			seSet(se, func(s *settingsEditorState) { s.leaderDraft.Key = strings.TrimSpace(v) })
		}
	})
	settingsInlineRow(c, theme, i18n.T("settings.field.label"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.leader.node_label"), i18n.T("settings.leader.node_label_ph"), draft.Label); changed {
			seSet(se, func(s *settingsEditorState) { s.leaderDraft.Label = v })
		}
	})
	settingsInlineRow(c, theme, i18n.T("settings.field.icon"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.leader.node_icon"), "sparkles", draft.Icon); changed {
			seSet(se, func(s *settingsEditorState) { s.leaderDraft.Icon = strings.TrimSpace(v) })
		}
	})
	settingsInlineRow(c, theme, i18n.T("settings.field.action"), settingsFieldLabelWidth, func() {
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
		settingsHint(c, theme, i18n.Tf("settings.leader.group_children", len(draft.Action.Children)))
		settingsDivider(c)
		ui.Text(c, i18n.T("settings.leader.new_child")).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
		childKey := seGet(se, func(s *settingsEditorState) string { return s.leaderChildKey })
		childLabel := seGet(se, func(s *settingsEditorState) string { return s.leaderChildLbl })
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Width(60).Children(func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.leader.child_key"), "x", childKey); changed {
					seSet(se, func(s *settingsEditorState) { s.leaderChildKey = v })
				}
			})
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.leader.child_label"), i18n.T("settings.leader.child_label_ph"), childLabel); changed {
					seSet(se, func(s *settingsEditorState) { s.leaderChildLbl = v })
				}
			})
		})
		if ui.Button(c, i18n.T("settings.leader.add_child")).Clicked() {
			a.addLeaderNode(path)
		}
	case config.ActionWindow:
		settingsInlineRow(c, theme, i18n.T("settings.field.command"), settingsFieldLabelWidth, func() {
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
		settingsInlineRow(c, theme, i18n.T("settings.field.mode"), settingsFieldLabelWidth, func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.leader.rewrite_mode"), "polish", draft.Action.Mode); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Mode = v })
			}
		})
		settingsLabelled(c, theme, i18n.T("settings.field.prompt"), func() {
			if v, changed := settingsTextArea(c, i18n.T("settings.leader.rewrite_prompt"), i18n.T("settings.leader.rewrite_prompt_ph"), draft.Action.Prompt, 2); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Prompt = v })
			}
		})
	case config.ActionSnippet:
		settingsInlineRow(c, theme, i18n.T("settings.field.snippet_id"), settingsFieldLabelWidth, func() {
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
		settingsInlineRow(c, theme, i18n.T("settings.field.target"), settingsFieldLabelWidth, func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.leader.app_target"), "com.apple.Safari", draft.Action.Target); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Target = v })
			}
		})
	case config.ActionSwitchTab:
		settingsInlineRow(c, theme, i18n.T("settings.field.tab"), settingsFieldLabelWidth, func() {
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
	case config.ActionRunWorkflow:
		settingsInlineRow(c, theme, i18n.T("settings.field.workflow_id"), settingsFieldLabelWidth, func() {
			ids := leaderWorkflowIDs(cfg)
			current := draft.Action.WorkflowID
			options, valid := leaderWorkflowOptions(ids, current)
			if current == "" && len(ids) > 0 {
				current = ids[0]
				valid = true
				options = ids
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.WorkflowID = current })
			}
			if len(ids) > 0 {
				if v, changed := settingsSelect(c, options, current); changed {
					seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action = config.RunWorkflowAction(v) })
				}
			} else {
				settingsHint(c, theme, i18n.T("settings.leader.workflow_none"))
			}
			if !valid && current != "" {
				settingsBanner(c, theme.Danger, fmt.Sprintf("Workflow %q is invalid or no longer exists.", current))
			}
		})
	case config.ActionCLI:
		settingsInlineRow(c, theme, i18n.T("settings.field.command"), settingsFieldLabelWidth, func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.leader.cli_command"), "open", draft.Action.Command); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Command = v })
			}
		})
		settingsInlineRow(c, theme, i18n.T("settings.field.args"), settingsFieldLabelWidth, func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.leader.cli_args"), "-a Safari", strings.Join(draft.Action.Args, " ")); changed {
				seSet(se, func(s *settingsEditorState) { s.leaderDraft.Action.Args = splitTokens(v) })
			}
		})
	}

	settingsDivider(c)
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, i18n.T("settings.leader.save_node")).Clicked() {
			a.saveLeaderNode(path)
		}
		if ui.Button(c, i18n.T("settings.leader.move_up")).Clicked() {
			a.moveLeaderSelection(-1)
		}
		if ui.Button(c, i18n.T("settings.leader.move_down")).Clicked() {
			a.moveLeaderSelection(1)
		}
		ui.Spacer(c)
		if ui.Button(c, i18n.T("settings.leader.delete_node")).Clicked() {
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
		seSet(se, func(s *settingsEditorState) { s.leaderError = i18n.T("settings.leader.err_key_required") })
		return
	}
	if label == "" {
		label = key
	}
	cfg := a.ConfigStore.Current()
	if leaderChildKeyTaken(cfg.Leader.Nodes, parentPath, key) {
		seSet(se, func(s *settingsEditorState) {
			s.leaderError = i18n.Tf("settings.leader.err_duplicate_key", key)
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
			s.leaderError = i18n.Tf("settings.leader.err_duplicate_key", draft.Key)
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

	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(10).FillWidth().Children(func() {
		settingsEditorCard(c, theme, i18n.Tf("settings.snippets.card_title", len(items)), func() {
			query := seGet(se, func(s *settingsEditorState) string { return s.snippetQuery })
			search := ui.SearchField(c, &query).Label(i18n.T("omnibox.placeholder.snippets")).Placeholder(i18n.T("omnibox.placeholder.snippets")).FillWidth()
			if search.Changed() {
				seSet(se, func(s *settingsEditorState) { s.snippetQuery = query })
			}
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.snippets.new_snippet")).Clicked() {
					seSet(se, func(s *settingsEditorState) {
						s.snippetIsNew = true
						s.snippetSelected = ""
						s.snippetDraft = config.SnippetItem{WordBoundary: true, AppBundleIDs: []string{}, ProcessNames: []string{}}
					})
				}
				settingsHint(c, theme, i18n.T("settings.snippets.select_item"))
			})
			settingsDivider(c)
			matches := 0
			for i := range items {
				item := items[i]
				if query != "" && !strings.Contains(strings.ToLower(item.ID+" "+item.Trigger+" "+item.Description), strings.ToLower(query)) {
					continue
				}
				matches++
				row := settingsListRow(c, theme, "snippet:"+item.ID, item.ID == selected)
				row.Children(func() {
					ui.Text(c, item.Trigger).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Shrink(0)
					ui.Text(c, item.Description).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
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
				kitEmptyState(c, theme, "text.badge.plus", i18n.T("omnibox.empty.snippets"), "")
			}
		})

		settingsEditorCard(c, theme, i18n.T("settings.snippets.editor"), func() {
			draft := seGet(se, func(s *settingsEditorState) config.SnippetItem { return s.snippetDraft })
			settingsInlineRow(c, theme, i18n.T("settings.field.id"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.snippets.id"), "my-snippet", draft.ID); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.ID = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.trigger"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.snippets.trigger"), ";sig", draft.Trigger); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.Trigger = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.description"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.snippets.description"), i18n.T("settings.snippets.description_ph"), draft.Description); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.Description = v })
				}
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				name := ui.Text(c, i18n.T("settings.snippets.word_boundary")).FontSize(typeMeta).TextColor(theme.TextMuted).
					Width(settingsFieldLabelWidth).SingleLine()
				wb := draft.WordBoundary
				if ui.Switch(c, &wb).LabelledBy(name).Changed() {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.WordBoundary = wb })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.app_bundle_ids"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.snippets.bundle_ids"), "com.apple.Safari", strings.Join(draft.AppBundleIDs, ", ")); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.AppBundleIDs = splitTokens(v) })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.process_names"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.snippets.process_names"), "Safari", strings.Join(draft.ProcessNames, ", ")); changed {
					seSet(se, func(s *settingsEditorState) { s.snippetDraft.ProcessNames = splitTokens(v) })
				}
			})

			draft = seGet(se, func(s *settingsEditorState) config.SnippetItem { return s.snippetDraft })
			content := draft.Content
			area := ui.TextArea(c, &content).Label(i18n.T("settings.snippets.content")).Placeholder(i18n.T("settings.snippets.content_ph")).Lines(2, 6).FillWidth()
			if area.Changed() {
				seSet(se, func(s *settingsEditorState) { s.snippetDraft.Content = content })
			}
			ui.Grid(c).Columns(3).GapX(4).GapY(3).FillWidth().Children(func() {
				for _, chip := range snippetPlaceholders() {
					ph := chip
					if ui.Button(c, ph).Key("snippet-chip:" + ph).FontSize(typeMicro).Clicked() {
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
				Padding(space4, space8).
				Radius(radiusChip).
				Background(theme.Background).
				Border(1, theme.Border).
				Children(func() {
					ui.Text(c, i18n.T("settings.snippets.live_preview")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
					ui.Text(c, rendered.RenderedText).FontSize(typeLabel).TextColor(theme.Text)
				})

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.snippets.save")).Clicked() {
					a.saveSnippetDraft()
				}
				if ui.Button(c, i18n.T("settings.snippets.delete")).Disabled(seGet(se, func(s *settingsEditorState) bool { return s.snippetIsNew })).Clicked() {
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
		a.setSettingsStatus(i18n.T("settings.snippets.err_required"))
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

	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(10).FillWidth().Children(func() {
		settingsEditorCard(c, theme, i18n.Tf("settings.glossary.card_title", len(terms)), func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.glossary.new_term")).Clicked() {
					seSet(se, func(s *settingsEditorState) {
						s.glossaryIsNew = true
						s.glossarySelected = ""
						s.glossaryDraft = config.GlossaryTerm{Aliases: []string{}}
					})
				}
				settingsHint(c, theme, i18n.T("settings.glossary.select_term"))
			})
			settingsDivider(c)
			if len(terms) == 0 {
				kitEmptyState(c, theme, "character.book.closed", i18n.T("omnibox.empty.glossary"), "")
			}
			for _, term := range terms {
				t := term
				row := settingsListRow(c, theme, "glossary:"+t.Term, t.Term == selected)
				row.Children(func() {
					ui.Text(c, t.Term).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Shrink(0)
					ui.Text(c, strings.Join(t.Aliases, ", ")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
					if t.Category != "" {
						chip(c, theme, "", t.Category, chipNeutral).Shrink(0)
					}
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
			settingsInlineRow(c, theme, i18n.T("settings.field.term"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.glossary.term"), "Kubernetes", draft.Term); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Term = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.aliases"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.glossary.aliases"), "k8s, kube", strings.Join(draft.Aliases, ", ")); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Aliases = splitTokens(v) })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.category"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.glossary.category"), "product", draft.Category); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Category = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.hint"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.glossary.hint"), i18n.T("settings.glossary.hint_ph"), draft.Hint); changed {
					seSet(se, func(s *settingsEditorState) { s.glossaryDraft.Hint = v })
				}
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.glossary.save")).Clicked() {
					a.saveGlossaryDraft()
				}
				if ui.Button(c, i18n.T("settings.glossary.delete")).Disabled(seGet(se, func(s *settingsEditorState) bool { return s.glossaryIsNew })).Clicked() {
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
		a.setSettingsStatus(i18n.T("settings.glossary.err_required"))
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

	settingsSourceCard(c, theme, i18n.T("settings.card.clipboard"), "[clipboard]", func() {
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.clipboard.enabled"), "enabled", "", clip.Enabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.Enabled = v })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.clipboard.ignore_concealed"), "ignore_concealed_types",
			i18n.T("settings.clipboard.ignore_concealed_help"), clip.IgnoreConcealedTypes); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.IgnoreConcealedTypes = v })
		}
		if v, changed := settingNumberRow(c, theme, i18n.T("settings.clipboard.max_items"), "max_history_items", "",
			float64(clip.MaxHistoryItems), 10, 2000, 10, ""); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.MaxHistoryItems = int(v) })
		}
		if v, changed := settingNumberRow(c, theme, i18n.T("settings.clipboard.retention"), "retention_days", "",
			float64(clip.RetentionDays), 1, 365, 1, i18n.T("common.days")); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.RetentionDays = int(v) })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.clipboard.media_capture"), "media_capture",
			i18n.T("settings.clipboard.media_capture_help"), clip.MediaCapture); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.MediaCapture = v })
		}
		if v, changed := settingNumberRow(c, theme, i18n.T("settings.clipboard.media_quota"), "media_quota_mb",
			i18n.T("settings.clipboard.media_quota_help"), float64(clip.MediaQuotaMB), 64, 4096, 64, "MB"); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.MediaQuotaMB = int(v) })
		}

		settingsCardDivider(c, theme)
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.clipboard.excluded_apps")).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
			ui.Text(c, "excluded_bundle_ids").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		})
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.clipboard.excluded_id"), "com.example.app", seGet(se, func(s *settingsEditorState) string { return s.clipExcludeDraft })); changed {
					seSet(se, func(s *settingsEditorState) { s.clipExcludeDraft = v })
				}
			})
			pending := strings.TrimSpace(seGet(se, func(s *settingsEditorState) string { return s.clipExcludeDraft }))
			if ui.Button(c, i18n.T("settings.clipboard.add_exclusion")).Disabled(pending == "").Clicked() {
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
				ui.Text(c, id).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1)
				if ui.Button(c, i18n.T("settings.clipboard.remove")).Key(fmt.Sprintf("clip-excl-remove:%d", idx)).Padding(space2, space10).Shrink(0).Clicked() {
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
