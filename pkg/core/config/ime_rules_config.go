package config

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/toml"
)

// IMEMode specifies preferred input method behavior for an application.
type IMEMode string

const (
	IMEModeASCII  IMEMode = "ascii"
	IMEModeNative IMEMode = "native"
	IMEModeKeep   IMEMode = "keep"
)

func AllIMEModes() []IMEMode {
	return []IMEMode{IMEModeASCII, IMEModeNative, IMEModeKeep}
}

func isValidIMEMode(m IMEMode) bool {
	for _, mode := range AllIMEModes() {
		if mode == m {
			return true
		}
	}
	return false
}

// IMERule defines a per-application (and optional URL/title pattern) IME auto-switching rule.
type IMERule struct {
	BundleID       string  `json:"bundleId"`
	ProcessName    string  `json:"processName,omitempty"`
	AppName        string  `json:"appName"`
	URLPattern     string  `json:"urlPattern,omitempty"`
	TitlePattern   string  `json:"titlePattern,omitempty"`
	InputSourceID  string  `json:"inputSourceId"`
	PreferredMode  IMEMode `json:"preferredMode"`
	RestoreOnLeave bool    `json:"restoreOnLeave"`
}

func decodeIMERule(tbl *toml.Table, path string) (IMERule, error) {
	bundleID, hasBundle, err := optionalString(tbl, "bundle_id", path)
	if err != nil {
		return IMERule{}, err
	}
	processName, hasProc, err := optionalString(tbl, "process_name", path)
	if err != nil {
		return IMERule{}, err
	}
	if !hasBundle && !hasProc {
		return IMERule{}, &DecodingError{
			KeyPath: joinPath(path, "bundle_id"),
			Message: i18n.T("decode.ime_rule.target_missing"),
		}
	}

	appName, _, err := optionalString(tbl, "app_name", path)
	if err != nil {
		return IMERule{}, err
	}
	urlPattern, _, err := optionalString(tbl, "url_pattern", path)
	if err != nil {
		return IMERule{}, err
	}
	titlePattern, _, err := optionalString(tbl, "title_pattern", path)
	if err != nil {
		return IMERule{}, err
	}
	inputSourceID, _, err := optionalString(tbl, "input_source_id", path)
	if err != nil {
		return IMERule{}, err
	}
	rawMode := string(IMEModeASCII)
	if s, ok, err := optionalString(tbl, "preferred_mode", path); err != nil {
		return IMERule{}, err
	} else if ok {
		rawMode = s
	}
	mode := IMEMode(rawMode)
	if !isValidIMEMode(mode) {
		allowed := make([]string, len(AllIMEModes()))
		for i, m := range AllIMEModes() {
			allowed[i] = string(m)
		}
		return IMERule{}, &DecodingError{
			KeyPath: joinPath(path, "preferred_mode"),
			Message: i18n.Tf("decode.ime_rule.mode.unknown", rawMode, strings.Join(allowed, ", ")),
		}
	}

	restoreOnLeave := true
	if b, ok, err := optionalBool(tbl, "restore_on_leave", path); err != nil {
		return IMERule{}, err
	} else if ok {
		restoreOnLeave = b
	}

	return IMERule{
		BundleID:       bundleID,
		ProcessName:    processName,
		AppName:        appName,
		URLPattern:     urlPattern,
		TitlePattern:   titlePattern,
		InputSourceID:  inputSourceID,
		PreferredMode:  mode,
		RestoreOnLeave: restoreOnLeave,
	}, nil
}

func (r IMERule) toTOML() *toml.Table {
	t := toml.NewTable()
	if r.BundleID != "" {
		t.Set("bundle_id", toml.StringVal(r.BundleID))
	}
	if r.ProcessName != "" {
		t.Set("process_name", toml.StringVal(r.ProcessName))
	}
	t.Set("app_name", toml.StringVal(r.AppName))
	if r.URLPattern != "" {
		t.Set("url_pattern", toml.StringVal(r.URLPattern))
	}
	if r.TitlePattern != "" {
		t.Set("title_pattern", toml.StringVal(r.TitlePattern))
	}
	if r.InputSourceID != "" {
		t.Set("input_source_id", toml.StringVal(r.InputSourceID))
	}
	t.Set("preferred_mode", toml.StringVal(string(r.PreferredMode)))
	t.Set("restore_on_leave", toml.BoolVal(r.RestoreOnLeave))
	return t
}

// IMERulesConfig represents ime-rules.toml.
type IMERulesConfig struct {
	Rules []IMERule `json:"rules"`
}

func DefaultIMERulesConfig() IMERulesConfig {
	return DefaultIMERulesConfigFor(runtime.GOOS)
}

// DefaultIMERulesConfigFor returns the default per-app IME rules for goos. Windows has no
// bundle IDs, so its defaults target process names and use mode-only switching (no HKL), which
// works with a single shared IME such as Microsoft Pinyin.
func DefaultIMERulesConfigFor(goos string) IMERulesConfig {
	if goos == "windows" {
		return IMERulesConfig{Rules: []IMERule{
			{ProcessName: "WindowsTerminal.exe", AppName: "Windows Terminal", PreferredMode: IMEModeASCII, RestoreOnLeave: true},
			{ProcessName: "Code.exe", AppName: "Visual Studio Code", PreferredMode: IMEModeASCII, RestoreOnLeave: true},
		}}
	}
	return IMERulesConfig{
		Rules: []IMERule{
			{
				BundleID:       "com.apple.Terminal",
				AppName:        "Terminal",
				InputSourceID:  "com.apple.keylayout.ABC",
				PreferredMode:  IMEModeASCII,
				RestoreOnLeave: true,
			},
			{
				BundleID:       "com.googlecode.iterm2",
				AppName:        "iTerm2",
				InputSourceID:  "com.apple.keylayout.ABC",
				PreferredMode:  IMEModeASCII,
				RestoreOnLeave: true,
			},
		},
	}
}

func DecodeIMERulesConfig(input string) (IMERulesConfig, error) {
	tbl, err := toml.Parse(input)
	if err != nil {
		return IMERulesConfig{}, err
	}
	return IMERulesConfigFromTOML(tbl)
}

func IMERulesConfigFromTOML(tbl *toml.Table) (IMERulesConfig, error) {
	tables, _, err := optionalTableArray(tbl, "rules", "")
	if err != nil {
		return IMERulesConfig{}, err
	}
	rules := make([]IMERule, 0, len(tables))
	for i, itemTbl := range tables {
		rule, err := decodeIMERule(itemTbl, fmt.Sprintf("rules[%d]", i))
		if err != nil {
			return IMERulesConfig{}, err
		}
		rules = append(rules, rule)
	}
	return IMERulesConfig{Rules: rules}, nil
}

func (c IMERulesConfig) Encode() string {
	root := toml.NewTable()
	vals := make([]toml.Value, len(c.Rules))
	for i, rule := range c.Rules {
		vals[i] = toml.TableVal(rule.toTOML())
	}
	root.Set("rules", toml.ArrayVal(vals))
	return toml.Encode(root)
}
