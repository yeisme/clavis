package uiapp

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// Settings of Clavis's own input engine ([input_engine]). Only options the
// engine reads are offered; mode depends on platform parts (IMK), so it
// stays in the TOML file. The config watcher applies a change at once.

// inputSchemes are the pinyin schemes the engine accepts.
var inputSchemes = []string{"quanpin", "xiaohe", "ziranma", "microsoft", "sogou", "pinyin_plus"}

// fuzzyPinyinPresets are common fuzzy pairs, initials then finals; the
// engine applies each both ways.
var fuzzyPinyinPresets = [][2]string{
	{"z", "zh"}, {"c", "ch"}, {"s", "sh"}, {"n", "l"}, {"f", "h"}, {"r", "l"},
	{"an", "ang"}, {"en", "eng"}, {"in", "ing"}, {"ian", "iang"}, {"uan", "uang"},
}

func (a *AppController) renderInputEngineCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	ie := cfg.Main.InputEngine
	settingsSourceCard(c, theme, i18n.T("settings.card.input_engine"), "[input_engine]", func() {
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.input_engine.enabled"), "enabled",
			i18n.T("settings.input_engine.enabled_help"), ie.Enabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.Enabled = v })
		}
		// The rest only matters with the engine on; it stays readable.
		ui.Column(c).FillWidth().Gap(space6).Disabled(!ie.Enabled).Children(func() {
			schemes := make([]settingOption, len(inputSchemes))
			for i, s := range inputSchemes {
				schemes[i] = settingOption{s, i18n.T("settings.input_engine.scheme." + s)}
			}
			if v, changed := settingSelectRow(c, theme, i18n.T("settings.input_engine.scheme"), "scheme", "",
				schemes, ie.Scheme, 150); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.Scheme = v })
			}
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.input_engine.candidate_count"), "candidate_count", "",
				float64(ie.CandidateCount), 3, 9, 1, ""); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.CandidateCount = int(v) })
			}
			orientation := cfg.Main.Appearance.CandidateBarOrientation
			if orientation == "" {
				orientation = "horizontal"
			}
			if v, changed := settingChoiceRow(c, theme, i18n.T("settings.input_engine.orientation"),
				"appearance.candidate_bar_orientation", "", []settingOption{
					{"horizontal", i18n.T("settings.option.horizontal")},
					{"vertical", i18n.T("settings.option.vertical")},
				}, orientation); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CandidateBarOrientation = v })
			}
			if v, changed := settingSwitchRow(c, theme, i18n.T("settings.input_engine.shift_toggle"), "shift_toggle_ascii", "",
				ie.ShiftToggleASCII); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.ShiftToggleASCII = v })
			}
			if v, changed := settingSwitchRow(c, theme, i18n.T("settings.input_engine.user_learning"), "user_learning",
				i18n.T("settings.input_engine.user_learning_help"), ie.UserLearning); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.UserLearning = v })
			}
			a.renderFuzzyPinyin(c, theme, ie.FuzzyPinyin)
			a.renderUserWordsCard(c, theme, cfg)
		})
	})
}

// renderFuzzyPinyin draws the fuzzy pairs as toggle chips. Rules the
// presets don't cover (written by hand in the TOML) are kept as they are.
func (a *AppController) renderFuzzyPinyin(c *ui.Context, theme *ui.Theme, rules []string) {
	caption := "fuzzy_pinyin"
	if len(rules) > 0 {
		caption = settingCaption("fuzzy_pinyin", strings.Join(rules, ", "))
	}
	settingStackedRow(c, theme, i18n.T("settings.input_engine.fuzzy"), caption, i18n.T("settings.input_engine.fuzzy_help"), func() ui.Element {
		row := ui.Row(c).FillWidth().Wrap().Gap(space4)
		row.Children(func() {
			for _, pair := range fuzzyPinyinPresets {
				p := pair
				on := slices.ContainsFunc(rules, func(r string) bool { return fuzzyRuleIs(r, p) })
				pill := ui.ButtonBase(c).Key("fuzzy:"+p[0]+"="+p[1]).Padding(space2, space8).
					Radius(radiusChip).FocusRing(false).Role(ui.RoleCheckBox).Checked(on)
				if on {
					pill.Background(selectionFill(theme)).Border(1, selectionEdge(theme))
				} else {
					pill.Background(theme.Background).Border(1, hairline(theme))
				}
				pill.Children(func() {
					fg := theme.TextMuted
					if on {
						fg = accentInk(theme, theme.Surface)
					}
					ui.Text(c, p[0]+" ⇄ "+p[1]).FontSize(typeMeta).FontWeight(600).TextColor(fg).SingleLine()
				})
				if pill.Clicked() {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) {
						m.InputEngine.FuzzyPinyin = toggleFuzzyRule(m.InputEngine.FuzzyPinyin, p)
					})
				}
			}
		})
		return row
	})
}

// fuzzyRuleIs reports whether rule is pair, written either way round and
// with "=", "_" or "-" between, as the engine reads it.
func fuzzyRuleIs(rule string, pair [2]string) bool {
	r := strings.ToLower(strings.TrimSpace(rule))
	a, b, ok := strings.Cut(r, "=")
	if !ok {
		if a, b, ok = strings.Cut(r, "_"); !ok {
			a, b, ok = strings.Cut(r, "-")
		}
	}
	if !ok {
		return false
	}
	return a == pair[0] && b == pair[1] || a == pair[1] && b == pair[0]
}

// toggleFuzzyRule removes pair from rules when present (in any spelling),
// else appends it as "a=b". Other rules keep their order.
func toggleFuzzyRule(rules []string, pair [2]string) []string {
	out := make([]string, 0, len(rules)+1)
	removed := false
	for _, r := range rules {
		if fuzzyRuleIs(r, pair) {
			removed = true
			continue
		}
		out = append(out, r)
	}
	if !removed {
		out = append(out, pair[0]+"="+pair[1])
	}
	return out
}
