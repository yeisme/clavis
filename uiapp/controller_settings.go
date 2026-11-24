package uiapp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// SettingsTab identifies one of the 5 native Settings tabs.
type SettingsTab int

const (
	SettingsTabGeneral SettingsTab = iota
	SettingsTabAI
	SettingsTabIMEKeymap
	SettingsTabPermissions
	SettingsTabConfigs
)

// SettingsTabNames returns the ordered display names for the 5 Settings tabs.
func SettingsTabNames() []string {
	return []string{
		i18n.T("settings.tab.general"),
		i18n.T("settings.tab.ai"),
		i18n.T("settings.tab.ime"),
		i18n.T("settings.tab.permissions"),
		i18n.T("settings.tab.configs"),
	}
}

// imeSwitchTimeout bounds the cross-process work done when Clavis moves the system input
// source. It is deliberately short: this runs off the input path but still in response to a
// user gesture, and a hung TIS call must not leave the app waiting.
const imeSwitchTimeout = 2 * time.Second

// IMEState is the single source of truth for what the input method is doing.
// The system input source, Clavis takeover, and engine ASCII mode collapse into
// four externally meaningful states for the pill and other UI surfaces.
type IMEState int

const (
	IMEStateASCII IMEState = iota
	IMEStateSystemCJK
	IMEStateTakeoverCJK
	IMEStateTakeoverASCII
)

func (s IMEState) Label() string {
	switch s {
	case IMEStateSystemCJK:
		return i18n.T("ime.state.system_cjk")
	case IMEStateTakeoverCJK:
		return i18n.T("ime.state.takeover_cjk")
	case IMEStateTakeoverASCII:
		return i18n.T("ime.state.takeover_ascii")
	default:
		return i18n.T("ime.state.ascii")
	}
}

// IMEState returns the cached externally visible IME state. It never asks the
// bridge for the current source, keeping source IPC out of repaint and hook paths.
func (c *AppController) IMEState() IMEState {
	c.mu.Lock()
	defer c.mu.Unlock()
	sourceASCII := c.activeInputSource.IsASCII
	forced := false
	if br, ok := c.Bridge.(bridge.InlineIMEForcer); ok && br != nil {
		forced = br.InlineIMEForced()
	}
	if forced {
		if c.inputEngineASCII {
			return IMEStateTakeoverASCII
		}
		return IMEStateTakeoverCJK
	}
	if sourceASCII {
		return IMEStateASCII
	}
	return IMEStateSystemCJK
}

// UpdateInputEngineMode caches the latest engine view mode for IMEState.
func (c *AppController) UpdateInputEngineMode(ascii bool) {
	c.mu.Lock()
	c.inputEngineASCII = ascii
	c.mu.Unlock()
}

func (c *AppController) showIMEPillState(anchor *host.CursorAnchor) {
	c.mu.Lock()
	cb := c.OnShowIMEPill
	c.mu.Unlock()
	if cb != nil {
		cb(c.IMEState().Label(), anchor)
	}
}

func (c *AppController) hasPillSink() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.OnShowIMEPill != nil
}

func (c *AppController) showPillNotice(message string, anchor *host.CursorAnchor) {
	c.mu.Lock()
	cb := c.OnShowIMEPill
	c.mu.Unlock()
	if cb != nil {
		cb(message, anchor)
	}
}

// bindAppIMERule persists (or removes) the per-app IME auto-switch rule requested by the Omnibox.
func (c *AppController) bindAppIMERule(eff engine.Effect, state engine.State) {
	appCtx := state.Context
	if eff.Context != nil {
		appCtx = *eff.Context
	}
	bundleID := strings.TrimSpace(appCtx.BundleID)
	processName := strings.TrimSpace(appCtx.ProcessName)
	appName := strings.TrimSpace(appCtx.AppName)
	if bundleID == "" && processName == "" {
		c.ShowToastOrHUD(i18n.T("hud.no_active_app"))
		return
	}
	if appName == "" {
		if bundleID != "" {
			appName = bundleID
		} else {
			appName = processName
		}
	}
	label := appName
	if trimmed := strings.TrimSpace(appCtx.AppName); trimmed != "" {
		label = trimmed
	}

	cfg := c.ConfigStore.Current()
	mode := eff.IMEMode
	if isClavisProcess(bundleID, processName) {
		c.ShowToastOrHUD(i18n.T("hud.no_active_app"))
		return
	}
	kept := buildAppIMERules(cfg.IMERules.Rules, bundleID, processName, appName, mode, cfg.Main.IME.DefaultInputSourceID)

	rulesCfg := config.IMERulesConfig{Rules: kept}
	if err := c.ConfigStore.SaveIMERules(rulesCfg); err != nil {
		c.ShowToastOrHUD(fmt.Sprintf("Failed to save IME rule: %v", err))
		return
	}

	var notice string
	if mode == "" {
		notice = i18n.Tf("hud.ime_unbound", label)
	} else {
		notice = i18n.Tf("hud.ime_bound", label, strings.ToUpper(string(mode)))
	}

	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = notice
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
		if mode != "" {
			if err := br.SetIMEMode(c.lifeCtx(), mode); err != nil && !errors.Is(err, errors.ErrUnsupported) {
				c.NotifyIMESwitchFailed(err)
			}
		}
	}
	c.ShowToastOrHUD(notice)
	if onRepaint != nil {
		onRepaint()
	}
}

// imeRuleTargetsApp reports whether rule is a plain per-app rule (no URL/title pattern)
// for the given bundle ID (preferred) or process name.
func imeRuleTargetsApp(rule config.IMERule, bundleID, processName string) bool {
	if strings.TrimSpace(rule.URLPattern) != "" || strings.TrimSpace(rule.TitlePattern) != "" {
		return false
	}
	if bundleID != "" {
		return strings.TrimSpace(rule.BundleID) == bundleID
	}
	if processName != "" {
		return strings.TrimSpace(rule.BundleID) == "" && normalizeRuleProcess(rule.ProcessName) == normalizeRuleProcess(processName)
	}
	return false
}

// normalizeRuleProcess compares Windows process names case-insensitively with ".exe" optional,
// matching the bridge's rule matcher so re-binding "code.exe" replaces an existing "Code.exe" rule.
func normalizeRuleProcess(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(n, ".exe")
}

func isClavisProcess(bundleID, processName string) bool {
	return strings.EqualFold(strings.TrimSpace(bundleID), "com.yeisme.clavis") || normalizeRuleProcess(processName) == "clavis"
}

// buildAppIMERules replaces any app-scoped rule for (bundleID|processName) with a new rule for
// mode, or only removes it when mode is empty. On Windows bundleID is empty, so the stored rule
// targets process_name (e.g. "Code.exe"), which the bridge matches against the foreground exe.
func buildAppIMERules(rules []config.IMERule, bundleID, processName, appName string, mode config.IMEMode, asciiSourceID string) []config.IMERule {
	kept := make([]config.IMERule, 0, len(rules)+1)
	for _, rule := range rules {
		if imeRuleTargetsApp(rule, bundleID, processName) {
			continue
		}
		kept = append(kept, rule)
	}
	if mode == "" {
		return kept
	}
	newRule := config.IMERule{
		BundleID:       bundleID,
		ProcessName:    processName,
		AppName:        appName,
		PreferredMode:  mode,
		RestoreOnLeave: true,
	}
	if mode == config.IMEModeASCII {
		newRule.InputSourceID = strings.TrimSpace(asciiSourceID)
	}
	return append(kept, newRule)
}

// FormatIMEPillLabel formats a short floating pill label ("EN", "中", or input source name).
func FormatIMEPillLabel(info host.InputSourceInfo) string {
	// A shared CJK IME (Windows Microsoft Pinyin) reports its live 中/英 conversion mode.
	switch info.Mode {
	case host.InputModeASCII:
		return "EN"
	case host.InputModeNative:
		if lang := strings.ToLower(strings.TrimSpace(info.Language)); strings.HasPrefix(lang, "ja") {
			return "あ"
		} else if strings.HasPrefix(lang, "ko") {
			return "한"
		}
		return "中"
	}
	if info.IsASCII {
		return "EN"
	}
	lowerID := strings.ToLower(strings.TrimSpace(info.ID))
	lowerName := strings.ToLower(strings.TrimSpace(info.Name))
	lowerLang := strings.ToLower(strings.TrimSpace(info.Language))
	if strings.Contains(lowerID, "scim") ||
		strings.Contains(lowerID, "pinyin") ||
		strings.Contains(lowerID, "rime") ||
		strings.Contains(lowerID, "sogou") ||
		strings.Contains(lowerName, "pinyin") ||
		strings.Contains(info.Name, "拼") ||
		strings.Contains(info.Name, "中") ||
		strings.HasPrefix(lowerLang, "zh") {
		return "中"
	}
	if strings.TrimSpace(info.Name) != "" {
		return strings.TrimSpace(info.Name)
	}
	if strings.TrimSpace(info.ID) != "" {
		return strings.TrimSpace(info.ID)
	}
	return "EN"
}

// FormatInputSourceStatus renders a readable "<name> · 中/EN" line for settings and diagnostics.
func FormatInputSourceStatus(info host.InputSourceInfo) string {
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = strings.TrimSpace(info.ID)
	}
	if name == "" {
		return i18n.T("settings.diag.unknown")
	}
	label := FormatIMEPillLabel(info)
	if label == name {
		return name
	}
	return name + " · " + label
}

// toggleInlineIMEForced flips the own input engine between forced Chinese takeover and
// auto mode. Forced mode exists because the engine deliberately stands down whenever a
// non-ASCII system input source (Apple Pinyin, Sogou, Squirrel …) is active; without an
// override the engine would be unreachable on a Mac whose system IME is Chinese.
//
// The hotkey consumes the key, so the active system IME never sees it: no double preedit.
// Feedback goes through the same IME pill the keymap layer uses, because the candidate
// panel only exists while composing.
func (c *AppController) toggleInlineIMEForced() {
	br, ok := c.Bridge.(bridge.InlineIMEForcer)
	if !ok || br == nil {
		return
	}
	next := !br.InlineIMEForced()
	br.SetInlineIMEForced(next)

	c.mu.Lock()
	onRepaint := c.OnRepaint
	cfg := c.ConfigStore.Current()
	c.mu.Unlock()

	// Persist the choice: the gesture is easy to forget, and losing it on every relaunch is
	// why the engine felt dead after a restart. A failed write must not block the toggle.
	if next != cfg.Main.InputEngine.ForceTakeover {
		_ = c.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.ForceTakeover = next })
	}

	if cfg.Main.IME.ShowFloatingPill {
		c.showIMEPillState(nil)
	}
	if onRepaint != nil {
		onRepaint()
	}
}

// NotifyIMESwitchFailed records an IME switch the bridge could not apply and surfaces it in the
// HUD/pill and the settings diagnostics card, so failures are never silent.
func (c *AppController) NotifyIMESwitchFailed(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.lastIMEError = err.Error()
	c.mu.Unlock()
	c.ShowToastOrHUD(i18n.Tf("hud.ime_switch_failed", err.Error()))
}

// NotifyIMESwitch updates the active input source and triggers OnShowIMEPill when ShowFloatingPill is enabled.
func (c *AppController) NotifyIMESwitch(info host.InputSourceInfo, anchor *host.CursorAnchor) {
	cfg := c.ConfigStore.Current()
	c.mu.Lock()
	if info.ID != "" || info.Name != "" {
		c.activeInputSource = info
	}
	wasTakeover := false
	if f, ok := c.Bridge.(bridge.InlineIMEForcer); ok && f != nil {
		wasTakeover = f.InlineIMEForced()
	}
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	// The user just switched the system input source by hand. Two behaviours keep the two IMEs
	// from fighting:
	//   1. auto_takeover: a non-ASCII source means "I want Chinese", so the engine engages
	//      without the user having to discover the double-tap.
	//   2. lock_system_source: once the engine owns input, push the system source back to ASCII
	//      so the system IME stops drawing its own candidate window on top of ours.
	if f, ok := c.Bridge.(bridge.InlineIMEForcer); ok && f != nil && cfg.Main.InputEngine.Enabled {
		switch {
		case cfg.Main.InputEngine.AutoTakeover && !info.IsASCII && !wasTakeover:
			// Engine takes over, then the source is locked below on the next event.
			f.SetInlineIMEForced(true)
			_ = c.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.ForceTakeover = true })
		case cfg.Main.InputEngine.LockSystemSource && wasTakeover && !info.IsASCII:
			go c.lockSystemSourceToASCII()
		}
	}

	if cfg.Main.IME.ShowFloatingPill {
		c.showIMEPillState(anchor)
	}
	if onRepaint != nil {
		onRepaint()
	}
}

// NotifyKeymapLayer records the current keymap hold layer and surfaces a transient pill notice
// when entering a non-empty layer (e.g. "nav", "hyper").
func (c *AppController) NotifyKeymapLayer(layer string) {
	trimmed := strings.TrimSpace(layer)
	c.mu.Lock()
	c.activeKeymapLayer = trimmed
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if trimmed != "" && c.hasPillSink() && c.ConfigStore.Current().Main.IME.ShowFloatingPill {
		c.showPillNotice("LAYER • "+strings.ToUpper(trimmed), nil)
	}
	if onRepaint != nil {
		onRepaint()
	}
}

func (c *AppController) maybeLockASCIIForLeader() {
	cfg := c.ConfigStore.Current()
	if !cfg.Main.IME.Enabled || !cfg.Main.IME.LockASCIIOnLeader {
		return
	}

	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return
	}

	ctx := c.lifeCtx()
	var prevID string
	if curBefore, curErr := br.CurrentInputSource(ctx); curErr == nil && !curBefore.IsASCII {
		prevID = strings.TrimSpace(curBefore.ID)
	}
	changed, asciiInfo, err := br.LockTemporaryASCII(ctx)
	if err != nil {
		// Bridges without temporary-lock support fall back to the configured default source.
		changed, asciiInfo = c.legacyLockASCII(cfg, br)
	}
	if !changed {
		return
	}

	// Suspend engine takeover for the duration of the panel. Locking the system source alone
	// does not stop the engine: it would keep consuming the key events the panel's search box
	// needs, which is the most confusing of the two-IME failure modes.
	takeoverSuspended := false
	if f, ok := br.(bridge.InlineIMEForcer); ok && f != nil && f.InlineIMEForced() {
		f.SetInlineIMEForced(false)
		takeoverSuspended = true
	}

	c.mu.Lock()
	if prevID != "" && c.preLeaderIMESourceID == "" {
		c.preLeaderIMESourceID = prevID
	}
	c.imeLockedForCapsule = true
	c.preLeaderIMETakeover = takeoverSuspended
	c.skipIMERestore = false
	c.activeInputSource = asciiInfo
	c.mu.Unlock()

	if cfg.Main.IME.ShowFloatingPill {
		c.showIMEPillState(nil)
	}
}

// lockSystemSourceToASCII pushes the system input source to ASCII while the engine owns
// Chinese input. Two IMEs both drawing candidates for the same keystrokes is the worst of the
// conflict modes; the engine cannot suppress the system IME's window by itself, so the source
// is moved out of the way. LockTemporaryASCII remembers the previous source, so the user's
// choice is restorable rather than overwritten.
func (c *AppController) lockSystemSourceToASCII() {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil || c.isClosed() {
		return
	}
	ctx, cancel := context.WithTimeout(c.lifeCtx(), imeSwitchTimeout)
	defer cancel()
	if _, _, err := br.LockTemporaryASCII(ctx); err != nil {
		return // bridges without temporary-lock support simply keep the source as-is
	}
}

// legacyLockASCII selects cfg.Main.IME.DefaultInputSourceID directly and remembers the
// previous non-ASCII source so a legacy restore path can still work.
func (c *AppController) legacyLockASCII(cfg config.ClavisConfig, br bridge.HostService) (bool, host.InputSourceInfo) {
	defaultASCII := strings.TrimSpace(cfg.Main.IME.DefaultInputSourceID)
	if defaultASCII == "" {
		return false, host.InputSourceInfo{}
	}
	ctx := c.lifeCtx()
	cur, err := br.CurrentInputSource(ctx)
	if err != nil || cur.IsASCII || strings.TrimSpace(cur.ID) == "" || cur.ID == defaultASCII {
		return false, cur
	}
	if err := br.SelectInputSource(ctx, defaultASCII); err != nil {
		return false, cur
	}

	asciiInfo := host.InputSourceInfo{
		ID:      defaultASCII,
		Name:    "ABC",
		IsASCII: true,
	}
	if curAfter, err := br.CurrentInputSource(ctx); err == nil && curAfter.ID != "" {
		asciiInfo = curAfter
	}

	c.mu.Lock()
	c.preLeaderIMESourceID = cur.ID
	c.mu.Unlock()
	return true, asciiInfo
}

func (c *AppController) maybeRestoreLockedIME() {
	c.mu.Lock()
	locked := c.imeLockedForCapsule
	skip := c.skipIMERestore
	restoreID := strings.TrimSpace(c.preLeaderIMESourceID)
	restoreTakeover := c.preLeaderIMETakeover
	br := c.Bridge
	c.imeLockedForCapsule = false
	c.skipIMERestore = false
	c.preLeaderIMESourceID = ""
	c.preLeaderIMETakeover = false
	c.mu.Unlock()

	if !locked || br == nil {
		return
	}

	// Restore engine takeover that was suspended when the panel opened, so closing the panel
	// returns the user to the mode they were in rather than silently dropping to "no engine".
	// This runs before the skip check: skip only means "do not put the system source back"
	// (the user already changed it by hand), and leaving the engine off forever would be a
	// much worse outcome than a stale input source.
	if restoreTakeover {
		if f, ok := br.(bridge.InlineIMEForcer); ok && f != nil && !f.InlineIMEForced() {
			f.SetInlineIMEForced(true)
		}
	}
	if skip {
		return
	}

	ctx := c.lifeCtx()
	restored, info, err := br.RestoreTemporaryIME(ctx)
	if (err != nil || !restored) && restoreID != "" {
		// Fallback to the remembered pre-Leader non-ASCII source ID if needed.
		if selErr := br.SelectInputSource(ctx, restoreID); selErr == nil {
			if cur, curErr := br.CurrentInputSource(ctx); curErr == nil && cur.ID != "" {
				info, restored = cur, true
			}
		}
	}
	if restored && info.ID != "" {
		c.mu.Lock()
		c.activeInputSource = info
		c.mu.Unlock()
	}
}

func (c *AppController) toggleIME() {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil || c.isClosed() {
		return
	}
	lifeCtx := c.lifeCtx()
	go func() {
		if lifeCtx.Err() != nil {
			return
		}
		sources, err := br.InputSources(lifeCtx)
		if lifeCtx.Err() != nil {
			return
		}
		if err != nil || len(sources) == 0 {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.no_input_sources")})
			return
		}
		current, err := br.CurrentInputSource(lifeCtx)
		next := sources[0]
		if err == nil {
			// Prefer toggling between ASCII and non-ASCII when available.
			foundAlt := false
			for _, s := range sources {
				if s.ID != current.ID && s.IsASCII != current.IsASCII {
					next = s
					foundAlt = true
					break
				}
			}
			if !foundAlt {
				for i, s := range sources {
					if s.ID == current.ID {
						next = sources[(i+1)%len(sources)]
						break
					}
				}
			}
		}
		if err := br.SelectInputSource(lifeCtx, next.ID); err != nil {
			if lifeCtx.Err() == nil {
				c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.Tf("hud.ime_switch_failed", err)})
			}
			return
		}
		if lifeCtx.Err() != nil {
			return
		}
		c.NotifyIMESwitch(next, nil)
		name := next.Name
		if name == "" {
			name = next.ID
		}
		c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.Tf("hud.ime_switched", name)})
	}()
}

func (c *AppController) normalizeGlossary(actionCtx engine.CapsuleContext) {
	c.mu.Lock()
	glossary := c.Glossary
	c.mu.Unlock()

	if strings.TrimSpace(actionCtx.SelectedText) != "" {
		normalized := glossary.NormalizeAliases(actionCtx.SelectedText, true, true)
		c.injectTextAsync(normalized, 0, 0)
		c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.normalized_glossary")})
		return
	}
	if strings.TrimSpace(actionCtx.ClipboardPreview) != "" {
		normalized := glossary.NormalizeAliases(actionCtx.ClipboardPreview, true, true)
		c.writeClipboardText(normalized)
		c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.normalized_clipboard")})
		return
	}
	c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.no_selection")})
}

func (c *AppController) handleInternalSettingCommand(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "reload-config":
		if c.ReloadConfig() {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.config_reloaded")})
		} else {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.config_reload_failed")})
		}
	case "open-config-dir":
		if err := c.OpenConfigDirectory(); err != nil {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.Tf("hud.open_folder_failed", err)})
		}
	case "ai":
		c.OpenSettings(SettingsTabAI)
	case "ime", "keymap", "ime-keymap":
		c.OpenSettings(SettingsTabIMEKeymap)
	case "permissions":
		c.OpenSettings(SettingsTabPermissions)
	case "configs":
		c.OpenSettings(SettingsTabConfigs)
	case "security", "vault":
		c.OpenSettingsSection(settingsSectionSecurity)
	default:
		c.OpenSettings(SettingsTabGeneral)
	}
}

// OpenSettings selects the requested settings tab and invokes OnOpenSettings.
func (c *AppController) OpenSettings(tab SettingsTab) {
	c.OpenSettingsSection(int(tab))
}

// OpenSettingsSection opens the settings center directly on a specific navigation rail section.
func (c *AppController) OpenSettingsSection(section int) {
	target := normalizeSettingsSection(section)
	meta := settingsSectionMetaFor(target)
	c.mu.Lock()
	c.uiState.settingsTabIdx = target
	fn := c.OnOpenSettings
	onRepaint := c.OnRepaint
	closed := c.closed
	c.mu.Unlock()
	if fn != nil {
		fn(meta.Settings)
	}
	if onRepaint != nil {
		onRepaint()
	}
	if closed {
		return
	}
	// The key may have changed outside Clavis: look again, off the main
	// thread, where reading the keychain needs no nested event loop.
	lifeCtx := c.lifeCtx()
	go func() {
		if lifeCtx.Err() != nil {
			return
		}
		if c.refreshAPIKeySource() && lifeCtx.Err() == nil && onRepaint != nil {
			onRepaint()
		}
	}()
}

func (c *AppController) setSettingsStatus(msg string) {
	c.mu.Lock()
	c.settingsStatus = msg
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

// UpdateMainConfig mutates and persists config.toml via ConfigStore.SaveMain,
// then propagates keymap and IME updates to the host bridge.
func (c *AppController) UpdateMainConfig(mutate func(main *config.MainConfig)) error {
	if mutate == nil {
		return nil
	}
	cfg := c.ConfigStore.Current()
	mainCfg := cfg.Main
	mutate(&mainCfg)

	if err := c.ConfigStore.SaveMain(mainCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_failed", err))
		return err
	}

	updated := c.ConfigStore.Current()
	SetCandidateAppearance(updated.Main.Appearance)
	SetCandidateStyle(updated.Main.InputEngine.Style)
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	if c.AIClient != nil {
		c.AIClient.Config = updated.Main.AI
	}
	c.settingsStatus = i18n.T("settings.status.saved")
	br := c.Bridge
	onRepaint := c.OnRepaint
	onLanguageChange := c.OnLanguageChange
	onConfigSaved := c.OnConfigSaved
	langChanged := syncAppearanceLanguage(updated)
	c.mu.Unlock()

	c.refreshAPIKeySource()
	if br != nil {
		br.UpdateKeymapConfig(updated.Main.Keymap)
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onConfigSaved != nil {
		onConfigSaved()
	}
	if langChanged && onLanguageChange != nil {
		onLanguageChange()
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// ExitHideDelay reports how long the shell should keep the capsule window
// visible after a dismiss so the exit animation lands; 0 hides at once.
// The value is published by the last CapsuleView frame (fresh config and
// accessibility preferences).
func (c *AppController) ExitHideDelay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.uiState.lastExitHideDelay
}

// syncAppearanceLanguage applies [appearance].language of cfg, resolving
// "auto" against the system preference, and reports whether the language
// changed. It runs on config save/reload so views, tray and diagnostics
// speak one language.
func syncAppearanceLanguage(cfg config.ClavisConfig) bool {
	prev := i18n.Current()
	i18n.SetLanguage(i18n.ResolveLanguage(cfg.Main.Appearance.Language, i18n.PreferredLanguages()))
	return i18n.Current() != prev
}

// AddIMERule validates and appends an IME auto-switch rule to ime-rules.toml,
// then updates the host bridge.
func (c *AppController) AddIMERule(rule config.IMERule) error {
	cfg := c.ConfigStore.Current()
	rule.BundleID = strings.TrimSpace(rule.BundleID)
	rule.ProcessName = strings.TrimSpace(rule.ProcessName)
	rule.AppName = strings.TrimSpace(rule.AppName)
	rule.URLPattern = strings.TrimSpace(rule.URLPattern)
	rule.TitlePattern = strings.TrimSpace(rule.TitlePattern)
	rule.InputSourceID = strings.TrimSpace(rule.InputSourceID)

	if rule.InputSourceID == "" {
		rule.InputSourceID = strings.TrimSpace(cfg.Main.IME.DefaultInputSourceID)
		if rule.InputSourceID == "" {
			rule.InputSourceID = "com.apple.keylayout.ABC"
		}
	}
	if rule.PreferredMode == "" {
		rule.PreferredMode = config.IMEModeASCII
	}
	if rule.AppName == "" {
		if rule.BundleID != "" {
			rule.AppName = rule.BundleID
		} else {
			rule.AppName = rule.ProcessName
		}
	}

	rulesCfg := cfg.IMERules
	rulesCfg.Rules = append(append([]config.IMERule(nil), rulesCfg.Rules...), rule)

	if err := c.ConfigStore.SaveIMERules(rulesCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.add_ime_rule_failed", err))
		return err
	}

	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.uiState.imeRuleTargetDraft = ""
	c.uiState.imeRuleURLDraft = ""
	c.uiState.imeRuleTitleDraft = ""
	c.uiState.imeRuleSourceDraft = ""
	c.settingsStatus = i18n.T("settings.status.ime_rule_added")
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// DeleteIMERule removes the IME rule at index from ime-rules.toml and updates the host bridge.
func (c *AppController) DeleteIMERule(index int) error {
	cfg := c.ConfigStore.Current()
	if index < 0 || index >= len(cfg.IMERules.Rules) {
		return fmt.Errorf("IME rule index %d out of range", index)
	}

	rules := make([]config.IMERule, 0, len(cfg.IMERules.Rules)-1)
	for i, r := range cfg.IMERules.Rules {
		if i != index {
			rules = append(rules, r)
		}
	}
	rulesCfg := config.IMERulesConfig{Rules: rules}

	if err := c.ConfigStore.SaveIMERules(rulesCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.delete_ime_rule_failed", err))
		return err
	}

	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = i18n.T("settings.status.ime_rule_deleted")
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveLeaderConfig persists leader.toml, refreshes the state machine, and repaints.
func (c *AppController) SaveLeaderConfig(leaderCfg config.LeaderConfig) error {
	if err := c.ConfigStore.SaveLeader(leaderCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_leader_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = i18n.Tf("settings.status.leader_saved", len(updated.Leader.Nodes))
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveSnippetsConfig persists snippets.toml, hot-reloads the snippet engine, and repaints.
func (c *AppController) SaveSnippetsConfig(snippetsCfg config.SnippetsConfig) error {
	if err := c.ConfigStore.SaveSnippets(snippetsCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_snippets_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	if c.Snippets != nil {
		c.Snippets.UpdateSnippets(updated.Snippets.Items)
	}
	c.settingsStatus = i18n.Tf("settings.status.snippets_saved", len(updated.Snippets.Items))
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveGlossaryConfig persists glossary.toml, hot-reloads the glossary and AI client engines, and repaints.
func (c *AppController) SaveGlossaryConfig(glossaryCfg config.GlossaryConfig) error {
	if err := c.ConfigStore.SaveGlossary(glossaryCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_glossary_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.Glossary = engine.NewGlossaryEngineFromConfig(updated.Glossary)
	if c.AIClient != nil {
		c.AIClient.GlossaryEngine = c.Glossary
	}
	c.settingsStatus = i18n.Tf("settings.status.glossary_saved", len(updated.Glossary.Terms))
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveIMERulesConfig persists ime-rules.toml, refreshes the state machine, and updates the host bridge.
func (c *AppController) SaveIMERulesConfig(imeRulesCfg config.IMERulesConfig) error {
	if err := c.ConfigStore.SaveIMERules(imeRulesCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_ime_rules_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = i18n.Tf("settings.status.ime_rules_saved", len(updated.IMERules.Rules))
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// CurrentInputSource returns the live input source from the bridge (falling back to the last
// observed one), used by the Settings IME card.
func (c *AppController) CurrentInputSource(ctx context.Context) (host.InputSourceInfo, error) {
	if ctx == nil {
		ctx = c.lifeCtx()
	}
	c.mu.Lock()
	br := c.Bridge
	last := c.activeInputSource
	c.mu.Unlock()
	if br == nil {
		return last, nil
	}
	cur, err := br.CurrentInputSource(ctx)
	if err != nil {
		return last, err
	}
	return cur, nil
}

// AvailableInputSources returns the OS input sources for Settings pickers (empty on error/unavailable).
func (c *AppController) AvailableInputSources(ctx context.Context) []host.InputSourceInfo {
	if ctx == nil {
		ctx = c.lifeCtx()
	}
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return nil
	}
	sources, err := br.InputSources(ctx)
	if err != nil {
		return nil
	}
	return sources
}

// ReloadConfig reloads all 5 TOML config files from ConfigStore and updates engines.
func (c *AppController) ReloadConfig() bool {
	ok := c.ConfigStore.Reload()
	cfg := c.ConfigStore.Current()

	c.mu.Lock()
	var br bridge.HostService
	if ok {
		c.StateMachine.SetConfig(cfg)
		SetCandidateAppearance(cfg.Main.Appearance)
		SetCandidateStyle(cfg.Main.InputEngine.Style)
		if c.Snippets != nil {
			c.Snippets.UpdateSnippets(cfg.Snippets.Items)
		}
		c.Glossary = engine.NewGlossaryEngineFromConfig(cfg.Glossary)
		c.AIClient = ai.NewOpenAICompatibleClient(
			cfg.Main.AI,
			c.Glossary,
			c.httpDoer,
			c.resolveAPIKey,
		)
		c.AIClient.UsageRecorder = c.aiUsageRecorder
		c.settingsStatus = i18n.T("hud.config_reloaded")
		br = c.Bridge
	} else {
		errs := c.ConfigStore.LastErrors()
		c.settingsStatus = i18n.Tf("settings.status.reload_failed", len(errs))
	}
	onRepaint := c.OnRepaint
	onLanguageChange := c.OnLanguageChange
	langChanged := syncAppearanceLanguage(cfg)
	c.mu.Unlock()

	if ok {
		c.refreshAPIKeySource()
	}
	if ok && br != nil {
		br.UpdateKeymapConfig(cfg.Main.Keymap)
		br.UpdateIMEConfig(cfg.Main.IME, cfg.IMERules)
	}
	if langChanged && onLanguageChange != nil {
		onLanguageChange()
	}
	if onRepaint != nil {
		onRepaint()
	}
	return ok
}

// RefreshPermissions queries the host bridge for current OS permissions and active IME.
func (c *AppController) RefreshPermissions(ctx context.Context) (host.PermissionStatus, error) {
	if ctx == nil {
		ctx = c.lifeCtx()
	}
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return host.PermissionStatus{}, nil
	}
	status, err := br.Permissions(ctx)
	var activeIME host.InputSourceInfo
	if ime, imeErr := br.CurrentInputSource(ctx); imeErr == nil {
		activeIME = ime
	}
	rec := int(br.TapRecoveryCount())
	c.mu.Lock()
	if err == nil {
		c.permissionStatus = status
	}
	if activeIME.ID != "" {
		c.activeInputSource = activeIME
	}
	if rec > c.recoveryCount {
		c.recoveryCount = rec
	}
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return status, err
}

// PromptPermission requests OS permission (accessibility / input_monitoring) via host bridge.
func (c *AppController) PromptPermission(kind string) (host.PermissionStatus, error) {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return host.PermissionStatus{}, nil
	}
	status, err := br.PromptPermission(kind)
	c.mu.Lock()
	if err == nil {
		c.permissionStatus = status
	}
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return status, err
}

// OpenPermissionSettings opens OS privacy/permission settings for kind.
func (c *AppController) OpenPermissionSettings(kind string) error {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return nil
	}
	return br.OpenPermissionSettings(kind)
}

// OpenConfigDirectory opens the Clavis configuration directory in the OS file manager.
func (c *AppController) OpenConfigDirectory() error {
	return c.openLocalPath(c.ConfigStore.Directory())
}

// OpenConfigFile opens one of the 5 TOML configuration files.
func (c *AppController) OpenConfigFile(kind config.ConfigFileKind) error {
	return c.openLocalPath(filepath.Join(c.ConfigStore.Directory(), string(kind)))
}

func (c *AppController) openLocalPath(path string) error {
	cleanPath := filepath.Clean(path)
	c.mu.Lock()
	fn := c.OpenPath
	c.mu.Unlock()
	if fn != nil {
		return fn(cleanPath)
	}
	if mygo.App.IsReady() {
		return mygo.Shell.OpenPath(cleanPath)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", cleanPath).Start()
	case "windows":
		return exec.Command("explorer.exe", cleanPath).Start()
	default:
		return exec.Command("xdg-open", cleanPath).Start()
	}
}

// RecordMonitorRecovery increments the monitor recovery counter (for diagnostics UI).
func (c *AppController) RecordMonitorRecovery() {
	c.mu.Lock()
	c.recoveryCount++
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

// DiagnosticsSnapshot captures permission and runtime diagnostic state for UI rendering.
type DiagnosticsSnapshot struct {
	Permissions       host.PermissionStatus
	MonitorRunning    bool
	RecoveryCount     int
	ActiveInputSource host.InputSourceInfo
	ActiveKeymapLayer string
	// LastIMEError is the most recent IME switch failure reported by the bridge.
	LastIMEError string
}

// Diagnostics returns a thread-safe snapshot of permission and monitor diagnostics.
func (c *AppController) Diagnostics() DiagnosticsSnapshot {
	c.mu.Lock()
	br := c.Bridge
	snap := DiagnosticsSnapshot{
		Permissions:       c.permissionStatus,
		MonitorRunning:    c.monitorRunning,
		RecoveryCount:     c.recoveryCount,
		ActiveInputSource: c.activeInputSource,
		ActiveKeymapLayer: c.activeKeymapLayer,
		LastIMEError:      c.lastIMEError,
	}
	c.mu.Unlock()
	if br != nil {
		if brRec := int(br.TapRecoveryCount()); brRec > snap.RecoveryCount {
			snap.RecoveryCount = brRec
		}
		if layer := strings.TrimSpace(br.ActiveKeymapLayer()); layer != "" {
			snap.ActiveKeymapLayer = layer
		}
	}
	return snap
}

// ForegroundIMEStatus returns the IME state of the last non-Clavis foreground app when the
// platform bridge tracks it (Windows). ok is false elsewhere or before the first observation.
func (c *AppController) ForegroundIMEStatus() (bridge.ForegroundIMEStatus, bool) {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	rep, ok := br.(bridge.ForegroundIMEReporter)
	if !ok || rep == nil {
		return bridge.ForegroundIMEStatus{}, false
	}
	return rep.ForegroundIMEStatus()
}
