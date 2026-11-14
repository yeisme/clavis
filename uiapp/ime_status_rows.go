package uiapp

import (
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// foregroundIMERows renders the IME card's "last external app" diagnostics as key/value rows:
// the app, its input source, the zh/en conversion state, and the rule that currently matches.
func foregroundIMERows(st bridge.ForegroundIMEStatus) [][2]string {
	app := st.App.ProcessName
	if app == "" {
		app = st.App.BundleID
	}
	if app == "" {
		app = i18n.T("settings.diag.unknown")
	}
	mode := i18n.T("settings.ime.mode_unknown")
	switch st.Source.Mode {
	case host.InputModeNative:
		mode = i18n.T("settings.ime.mode_native")
	case host.InputModeASCII:
		mode = i18n.T("settings.ime.mode_ascii")
	}
	rule := i18n.T("settings.ime.rule_none")
	if st.RuleMatched {
		target := bridge.DescribeIMERuleTarget(st.Rule)
		pref := string(st.Rule.PreferredMode)
		if pref == "" {
			pref = string(config.IMEModeKeep)
		}
		rule = i18n.Tf("settings.ime.rule_matched", target, pref)
		if !st.AutoSwitch {
			rule += " · " + i18n.T("settings.ime.auto_switch_off")
		}
	}
	return [][2]string{
		{i18n.T("settings.ime.fg_app"), app},
		{i18n.T("settings.ime.fg_source"), FormatInputSourceStatus(st.Source)},
		{i18n.T("settings.ime.fg_mode"), mode},
		{i18n.T("settings.ime.fg_rule"), rule},
	}
}
