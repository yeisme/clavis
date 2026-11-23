package uiapp

import (
	"math"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// Settings rows. Every setting reads as a name in the UI's language, with
// the config line it writes below it in muted text ("leader_key:
// right_option"), so the TOML files and the settings center explain each
// other. Choices show humanized labels (右 ⌥ Option, 深色); the caption
// keeps the raw value a config file would hold.

// settingOption is one choice of an enumerated setting.
type settingOption struct {
	value, label string
}

// settingCaption is the config line under a setting's name: the key and,
// when the control shows it in another form, the raw value.
func settingCaption(key, value string) string {
	if value == "" {
		return key
	}
	return key + ": " + value
}

// settingRow lays out one setting: its name and caption on the left, the
// control on the right. The name labels the control (LabelledBy), so a
// click on it toggles a switch or focuses the control, as HTML's <label
// for> does. help, when set, shows as the name's tooltip.
func settingRow(c *ui.Context, theme *ui.Theme, title, caption, help string, control func() ui.Element) {
	ui.Row(c).FillWidth().Height(controlHeight).Gap(space12).AlignItems(ui.Center).Padding(space2, 0).Children(func() {
		name := settingRowName(c, theme, title, caption, help)
		control().LabelledBy(name)
	})
}

// settingStackedRow puts a wide control (a grid of choices) below the
// setting's name instead of beside it.
func settingStackedRow(c *ui.Context, theme *ui.Theme, title, caption, help string, control func() ui.Element) {
	ui.Column(c).FillWidth().Gap(space6).Padding(space2, 0).Children(func() {
		name := settingRowName(c, theme, title, caption, help)
		control().LabelledBy(name)
	})
}

func settingRowName(c *ui.Context, theme *ui.Theme, title, caption, help string) ui.Element {
	var name ui.Element
	ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
		name = ui.Text(c, title).FontSize(typeBody).FontWeight(500).TextColor(theme.Text).SingleLine()
		if help != "" {
			name.Tooltip(help)
		}
		if caption != "" {
			ui.Text(c, caption).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		}
	})
	return name
}

// settingSwitchRow is a named switch; the caption is the config key.
func settingSwitchRow(c *ui.Context, theme *ui.Theme, title, key, help string, on bool) (bool, bool) {
	value, changed := on, false
	settingRow(c, theme, title, key, help, func() ui.Element {
		sw := ui.Switch(c, &value)
		changed = sw.Changed()
		return sw
	})
	return value, changed
}

// settingNumberRow is a named number with its unit and a stepper. The
// caption repeats the value as the config file writes it.
func settingNumberRow(
	c *ui.Context, theme *ui.Theme,
	title, key, help string,
	value, lo, hi, step float64, unit string,
) (float64, bool) {
	next, changed := value, false
	shown := formatSettingNumber(value, step, unit)
	settingRow(c, theme, title, settingCaption(key, shown), help, func() ui.Element {
		var st ui.Element
		ui.Row(c).Gap(space6).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, shown).FontSize(typeBody).TextColor(theme.Text).SingleLine()
			v := value
			st = ui.Stepper(c, &v, lo, hi, step)
			if st.Changed() {
				next, changed = v, true
			}
		})
		return st
	})
	return next, changed
}

// settingSliderRow is a named stepped slider. The caption repeats the value
// as a percentage so the current visual setting remains visible while the
// thumb is adjusted.
func settingSliderRow(
	c *ui.Context, theme *ui.Theme,
	title, key, help string,
	value, lo, hi, step float64,
) (float64, bool) {
	next, changed := value, false
	shown := formatSettingNumber(value*100, step*100, "%")
	settingRow(c, theme, title, settingCaption(key, shown), help, func() ui.Element {
		v := value
		slider := ui.StepSlider(c, &v, lo, hi, step).MinWidth(space32 * 4).Grow(0)
		if slider.Changed() {
			// The slider is bounded by lo/hi, but a drag can still report a value
			// a hair outside; clamp to the declared range rather than to one
			// field's rule, so stage intensity and glass clarity share the
			// control without inheriting each other's semantics.
			next, changed = clampSetting(v, lo, hi), true
		}
		return slider
	})
	return next, changed
}

// clampSetting bounds v to [lo, hi], mapping NaN to lo.
func clampSetting(v, lo, hi float64) float64 {
	if math.IsNaN(v) || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// formatSettingNumber shows value with the decimals of step ("0.7" for a
// step of 0.1, "300" for 10) and its unit.
func formatSettingNumber(value, step float64, unit string) string {
	decimals := 0
	for d := step; decimals < 3 && d > 0 && d < 1-1e-9; d *= 10 {
		decimals++
	}
	shown := strconv.FormatFloat(value, 'f', decimals, 64)
	if unit != "" {
		shown += " " + unit
	}
	return shown
}

// settingChoiceRow is a named segmented choice among a few options.
func settingChoiceRow(
	c *ui.Context, theme *ui.Theme,
	title, key, help string,
	options []settingOption, current string,
) (string, bool) {
	chosen, changed := current, false
	settingRow(c, theme, title, settingCaption(key, current), help, func() ui.Element {
		labels := make([]string, len(options))
		for i, o := range options {
			labels[i] = o.label
		}
		if len(options) <= 6 {
			picked := current
			control, value, ok := kitSegmentedPicker(c, theme, labels, labelsForValue(options, current))
			if ok {
				for _, o := range options {
					if o.label == value {
						picked = o.value
						break
					}
				}
				chosen, changed = picked, true
			}
			return control.Shrink(0)
		}
		sel := -1
		for i, o := range options {
			if strings.EqualFold(o.value, current) {
				sel = i
			}
		}
		seg := ui.Segmented(c, &sel, labels...).Shrink(0)
		if seg.Changed() && sel >= 0 && sel < len(options) && sel != -1 {
			chosen, changed = options[sel].value, true
		}
		return seg
	})
	return chosen, changed
}

func labelsForValue(options []settingOption, current string) string {
	for _, o := range options {
		if strings.EqualFold(o.value, current) {
			return o.label
		}
	}
	return current
}

// settingSelectRow is a named drop-down, for longer lists of options,
// width wide beside the name; a width of 0 stacks it under the name, as
// wide as the row, for long option labels.
func settingSelectRow(
	c *ui.Context, theme *ui.Theme,
	title, key, help string,
	options []settingOption, current string, width float32,
) (string, bool) {
	chosen, changed := current, false
	layout := settingRow
	if width <= 0 {
		layout = settingStackedRow
	}
	layout(c, theme, title, settingCaption(key, current), help, func() ui.Element {
		labels := make([]string, 0, len(options)+1)
		curLabel := ""
		for _, o := range options {
			labels = append(labels, o.label)
			if o.value == current {
				curLabel = o.label
			}
		}
		if curLabel == "" { // a value the list doesn't know stays selectable
			curLabel = current
			labels = append(labels, current)
		}
		v := curLabel
		sel := ui.Select(c, &v, labels)
		if width > 0 {
			sel.Width(width).Shrink(0)
		} else {
			sel.FillWidth()
		}
		if sel.Changed() && v != curLabel {
			for _, o := range options {
				if o.label == v {
					chosen, changed = o.value, true
				}
			}
		}
		return sel
	})
	return chosen, changed
}

// settingsTwoColumns keeps the standard two-column Settings layout at the
// supported 760px minimum width and collapses only when narrower than that.
func settingsTwoColumns(c *ui.Context) int {
	if w, _ := c.Size(); w > 0 && w < 720 {
		return 1
	}
	return 2
}

// settingsNarrowCard reports whether a two-column Settings card is narrow
// enough (e.g. at the 760px minimum window width) that nested two-column input
// rows inside the card should stack to a single readable column.
func settingsNarrowCard(c *ui.Context) bool {
	w, _ := c.Size()
	return w > 0 && w < 860
}

// settingsListRow is one row of a master list (Leader tree, snippets,
// glossary, workflows): a button, the chosen row tinted as the Omnibox's is.
// The row keeps its natural keyboard focus ring so a keyboard user can see
// which row Enter would activate; selection stays a fill, focus stays a ring,
// so a focused row that is not chosen still reads as focused.
func settingsListRow(c *ui.Context, theme *ui.Theme, key string, selected bool) ui.Element {
	row := ui.ButtonBase(c).Key(key).FillWidth().Padding(space4, space8).Radius(radiusChip).
		Gap(space8).AlignItems(ui.Center).FocusRing(true).FocusRingColor(kitFocusRing(theme))
	if selected {
		row.Background(selectionFill(theme)).Border(1, selectionEdge(theme))
	} else {
		row.Border(1, ui.Transparent)
	}
	return row
}

// ---------------------------------------------------------------------------
// Humanized options
// ---------------------------------------------------------------------------

// triggerKeyOptions are the trigger keys a picker offers, named for goos.
func triggerKeyOptions(goos string) []settingOption {
	presets := triggerPresets()
	out := make([]settingOption, len(presets))
	for i, p := range presets {
		out[i] = settingOption{p, triggerKeyLabel(p, goos)}
	}
	return out
}

// triggerKeyLabel names a trigger key token: with its glyph on macOS
// ("右 ⌥ Option"), by its PC name elsewhere ("右 Alt"), as the keyboard in
// front of the user labels it. Unknown tokens show as they are.
func triggerKeyLabel(token, goos string) string {
	if token == "caps_lock" {
		return i18n.T("settings.key.caps_lock")
	}
	side, mod, ok := strings.Cut(token, "_")
	if !ok || (side != "left" && side != "right") {
		return token
	}
	var name string
	mac := goos == "darwin"
	switch mod {
	case "option", "alt":
		name = byPlatform(mac, "⌥ Option", "Alt")
	case "command", "cmd", "win", "super":
		name = byPlatform(mac, "⌘ Command", "Win")
	case "control", "ctrl":
		name = byPlatform(mac, "⌃ Control", "Ctrl")
	case "shift":
		name = byPlatform(mac, "⇧ Shift", "Shift")
	default:
		return token
	}
	return i18n.Tf("settings.key."+side, name)
}

// byPlatform picks the macOS or the PC name of a key.
func byPlatform(mac bool, macLabel, pcLabel string) string {
	if mac {
		return macLabel
	}
	return pcLabel
}

func themeOptions() []settingOption {
	return []settingOption{
		{"system", i18n.T("settings.option.system")},
		{"light", i18n.T("settings.option.light")},
		{"dark", i18n.T("settings.option.dark")},
	}
}

// languageOptions name each language in itself, as language pickers do.
func languageOptions() []settingOption {
	out := make([]settingOption, 0, 3)
	for _, l := range i18n.AllLanguages() {
		label := l
		switch i18n.Language(l) {
		case i18n.Auto:
			label = i18n.T("settings.option.system")
		case i18n.Chinese:
			label = "简体中文"
		case i18n.English:
			label = "English"
		}
		out = append(out, settingOption{l, label})
	}
	return out
}

func animationOptions() []settingOption {
	out := make([]settingOption, 0, 4)
	for _, m := range AllAnimationModes() {
		label := m
		switch AnimationMode(m) {
		case AnimationAuto:
			label = i18n.T("settings.option.system")
		case AnimationFull:
			label = i18n.T("settings.option.motion_full")
		case AnimationReduced:
			label = i18n.T("settings.option.motion_reduced")
		case AnimationOff:
			label = i18n.T("settings.option.off")
		}
		out = append(out, settingOption{m, label})
	}
	return out
}

func visualPresetOptions() []settingOption {
	return []settingOption{
		{"classic", i18n.T("settings.option.visual_preset_classic")},
		{"liquid_glass", i18n.T("settings.option.visual_preset_liquid_glass")},
		{"spatial_3d", i18n.T("settings.option.visual_preset_spatial_3d")},
	}
}

// capsLockActionOptions are what a tap or hold of Caps Lock can do.
func capsLockActionOptions() []settingOption {
	return []settingOption{
		{"escape", "Esc"},
		{"nav", i18n.T("settings.keymap.nav_layer")},
		{"hyper", "Hyper"},
		{"control", byPlatform(runtime.GOOS == "darwin", "⌃ Control", "Ctrl")},
		{"none", i18n.T("settings.option.none")},
	}
}

// imeModeLabel names an IME rule mode, short enough for a segment of the
// narrow rules column.
func imeModeLabel(m config.IMEMode) string {
	switch m {
	case config.IMEModeASCII:
		return i18n.T("settings.ime.rule_mode_ascii")
	case config.IMEModeNative:
		return i18n.T("settings.ime.rule_mode_native")
	case config.IMEModeKeep:
		return i18n.T("settings.ime.rule_mode_keep")
	}
	return string(m)
}
