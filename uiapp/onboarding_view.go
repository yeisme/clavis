package uiapp

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// The first-run guide. A new user starts with nothing but a tray icon; this
// one page takes them to a working Leader key: the permissions the platform
// asks for, with their live status; a check that waits for the Leader key;
// and the few shortcuts worth knowing. The host shows it in its own window
// on a machine's first launch and from the tray, and remembers per machine
// (not in config.toml, which may sync between machines) that it was done.

// OnboardingHooks are the host's side of the first-run guide.
type OnboardingHooks struct {
	// Finish closes the guide; the host remembers it was done.
	Finish func()
	// Relaunch restarts Clavis. macOS gives Input Monitoring to processes
	// started after it was granted, so the keyboard monitor may need one.
	Relaunch func()
}

// onboardingState is the guide's live state, under AppController.mu.
type onboardingState struct {
	hooks OnboardingHooks
	// leaderSeen is set once a Leader (or double-tap) trigger arrived.
	leaderSeen bool
	// relaunchNeeded: every permission is granted, yet the keyboard monitor
	// failed to start; a new process would start it.
	relaunchNeeded bool
}

// onboardingGuideTimeout bounds how long a permission guide waits for the
// user in System Settings.
const onboardingGuideTimeout = 10 * time.Minute

// onboardingLeaderPreview is how many top-level Leader entries the guide
// shows as examples.
const onboardingLeaderPreview = 4

// onboardingKeysWidth is the column of keycaps in the shortcut list, wide
// enough for Alt Shift C.
const onboardingKeysWidth = 104

// SetOnboardingHooks installs the host's side of the first-run guide.
func (a *AppController) SetOnboardingHooks(h OnboardingHooks) {
	a.mu.Lock()
	a.onboarding.hooks = h
	a.mu.Unlock()
}

// SetRelaunchNeeded tells the guide whether the keyboard monitor needs
// Clavis restarted to start with the permissions now granted.
func (a *AppController) SetRelaunchNeeded(v bool) {
	a.mu.Lock()
	changed := a.onboarding.relaunchNeeded != v
	a.onboarding.relaunchNeeded = v
	onRepaint := a.OnRepaint
	a.mu.Unlock()
	if changed && onRepaint != nil {
		onRepaint()
	}
}

// noteOnboardingLeader passes the guide's Leader check. The Leader dispatch
// that follows repaints.
func (a *AppController) noteOnboardingLeader() {
	a.mu.Lock()
	a.onboarding.leaderSeen = true
	a.mu.Unlock()
}

// GuidePermission takes the user to the system settings page that grants
// kind ("accessibility", "input_monitoring"). A bridge with a guide (macOS:
// a panel under System Settings with the app to drag into the list) waits
// there until the permission is granted or the user leaves; others only
// open the page. The permission status is refreshed afterwards.
func (a *AppController) GuidePermission(ctx context.Context, kind string) (host.PermissionStatus, error) {
	a.mu.Lock()
	br := a.Bridge
	a.mu.Unlock()
	var err error
	switch g := br.(type) {
	case nil:
		return host.PermissionStatus{}, nil
	case bridge.PermissionGuide:
		_, err = g.GuidePermission(ctx, kind)
	default:
		err = br.OpenPermissionSettings(kind)
	}
	st, refreshErr := a.RefreshPermissions(context.Background())
	if err == nil || ctx.Err() != nil {
		err = refreshErr
	}
	return st, err
}

// onboardingAsksPermissions reports whether goos makes the user grant
// Clavis permissions: macOS asks for Accessibility and Input Monitoring;
// Windows gives a desktop app its keyboard hook without asking.
func onboardingAsksPermissions(goos string) bool { return goos == "darwin" }

// OnboardingView renders the first-run guide.
func (a *AppController) OnboardingView(c *ui.Context) {
	a.renderOnboarding(c, runtime.GOOS)
}

func (a *AppController) renderOnboarding(c *ui.Context, goos string) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)
	diag := a.Diagnostics()
	a.mu.Lock()
	st := a.onboarding
	a.mu.Unlock()

	perms := diag.Permissions
	granted := !onboardingAsksPermissions(goos) || perms.Accessibility && perms.InputMonitoring
	ready := granted && diag.MonitorRunning

	applyTextLocale(ui.Column(c).Fill().Background(theme.Background)).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(space16+4, space16+8).Gap(space12).Children(func() {
				renderOnboardingHeader(c, theme)
				a.renderOnboardingPermissions(c, theme, goos, diag, st, ready)
				a.renderOnboardingLeader(c, theme, cfg, goos, st.leaderSeen, granted, diag.MonitorRunning)
				renderOnboardingShortcuts(c, theme, cfg, goos)
			})
		})
		renderOnboardingFooter(c, theme, a, st.hooks, ready)
	})
}

var (
	onboardingIconOnce sync.Once
	onboardingIcon     *ui.Bitmap
)

// onboardingAppIcon is the app icon the guide greets with, drawn once.
func onboardingAppIcon() *ui.Bitmap {
	onboardingIconOnce.Do(func() {
		if bmp, err := ui.DecodeBitmap(GenerateAppIconPNG(128)); err == nil {
			onboardingIcon = bmp
		}
	})
	return onboardingIcon
}

func renderOnboardingHeader(c *ui.Context, theme *ui.Theme) {
	ui.Row(c).FillWidth().Gap(space14).AlignItems(ui.Center).Children(func() {
		if icon := onboardingAppIcon(); icon != nil {
			ui.Image(c, icon).Size(52, 52).Shrink(0)
		}
		ui.Column(c).Grow(1).Shrink(1).Gap(space4).Children(func() {
			ui.Text(c, i18n.T("onboarding.title")).FontSize(20).FontWeight(700).TextColor(theme.Text).SingleLine()
			applyProseLineHeight(ui.Text(c, i18n.T("onboarding.subtitle"))).FontSize(typeBody).TextColor(theme.TextMuted)
		})
	})
}

// onboardingStep frames one numbered step: its number, or a check once it
// is done, beside the title and the body.
func onboardingStep(c *ui.Context, theme *ui.Theme, n int, title string, done bool, body func()) {
	ui.Row(c).Key(fmt.Sprintf("onboarding-step:%d", n)).FillWidth().Gap(space12).AlignItems(ui.Start).
		Padding(space12, space14).Radius(radiusCard).Background(theme.Surface).Border(1, theme.Border).
		Children(func() {
			badge := ui.Box(c).Size(24, 24).Radius(radiusPill).Center().Shrink(0)
			if done {
				badge.Background(theme.Success.Alpha(0.16)).Children(func() {
					renderSemanticIcon(c, "checkmark", 12, readableOn(theme.Success, theme.Text, theme.Surface, minTextContrast))
				})
			} else {
				badge.Background(theme.Accent.Alpha(0.14)).Children(func() {
					ui.Text(c, strconv.Itoa(n)).FontSize(typeLabel).FontWeight(700).TextColor(accentInk(theme, theme.Surface)).SingleLine()
				})
			}
			ui.Column(c).Grow(1).Shrink(1).Gap(space8).Children(func() {
				ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(theme.Text).SingleLine()
				body()
			})
		})
}

// onboardingNote is a status line: an icon and a sentence in one color.
func onboardingNote(c *ui.Context, theme *ui.Theme, icon string, color ui.Color, text string) {
	ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
		renderSemanticIcon(c, icon, 12, color)
		applyProseLineHeight(ui.Text(c, text)).FontSize(typeLabel).TextColor(color).Grow(1).Shrink(1)
	})
}

// renderOnboardingPermissions is step 1: each permission with what it is
// for and its live status; once granted, whether the keyboard monitor runs.
func (a *AppController) renderOnboardingPermissions(c *ui.Context, theme *ui.Theme, goos string, diag DiagnosticsSnapshot, st onboardingState, done bool) {
	onboardingStep(c, theme, 1, i18n.T("onboarding.step.permissions"), done, func() {
		if !onboardingAsksPermissions(goos) {
			if diag.MonitorRunning {
				onboardingNote(c, theme, "checkmark", theme.Success, i18n.T("onboarding.perm.none_needed"))
			} else {
				onboardingNote(c, theme, "exclamationmark.triangle", theme.Warning, i18n.T("onboarding.perm.monitor_stopped"))
			}
			return
		}
		perms := diag.Permissions
		// The one accent button is the next permission to grant.
		a.onboardingPermRow(c, theme, "accessibility", i18n.T("settings.perm.accessibility"),
			i18n.T("onboarding.perm.why_accessibility"), perms.Accessibility, true)
		a.onboardingPermRow(c, theme, "input_monitoring", i18n.T("settings.perm.input_monitoring"),
			i18n.T("onboarding.perm.why_input_monitoring"), perms.InputMonitoring, perms.Accessibility)
		if !perms.Accessibility || !perms.InputMonitoring || diag.MonitorRunning {
			return
		}
		if !st.relaunchNeeded {
			onboardingNote(c, theme, "clock", theme.TextMuted, i18n.T("onboarding.perm.starting"))
			return
		}
		ui.Row(c).FillWidth().Gap(space10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1).Shrink(1).Children(func() {
				onboardingNote(c, theme, "arrow.clockwise", theme.Warning, i18n.T("onboarding.perm.relaunch_hint"))
			})
			if ui.PrimaryButton(c, i18n.T("onboarding.perm.relaunch")).Key("onboarding-relaunch").Clicked() && st.hooks.Relaunch != nil {
				st.hooks.Relaunch()
			}
		})
	})
}

func (a *AppController) onboardingPermRow(c *ui.Context, theme *ui.Theme, kind, label, purpose string, granted, primary bool) {
	ui.Row(c).Key("onboarding-perm:" + kind).FillWidth().Gap(space10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(space2).Children(func() {
			ui.Text(c, label).FontSize(typeBody).FontWeight(600).TextColor(theme.Text).SingleLine()
			applyProseLineHeight(ui.Text(c, purpose)).FontSize(typeMeta).TextColor(theme.TextMuted)
		})
		if granted {
			chip(c, theme, "checkmark", i18n.T("settings.perm.granted"), chipSuccess)
			return
		}
		btn := ui.Button
		if primary {
			btn = ui.PrimaryButton
		}
		if btn(c, i18n.T("onboarding.perm.grant")).Key("onboarding-grant:" + kind).Clicked() {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), onboardingGuideTimeout)
				defer cancel()
				_, _ = a.GuidePermission(ctx, kind)
			}()
		}
	})
}

// renderOnboardingLeader is step 2: double-tap the Leader key (both trigger
// keys fire on a double tap, see bridge.DoubleTapDetector). It shows the key
// as configured and the first entries of the user's own Leader menu, and
// turns green when the trigger arrives. Until the keyboard monitor runs it
// says what it waits for: the permissions, or the monitor itself.
func (a *AppController) renderOnboardingLeader(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, goos string, seen, granted, listening bool) {
	trig := cfg.Main.Trigger
	onboardingStep(c, theme, 2, i18n.T("onboarding.step.leader"), seen, func() {
		ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("onboarding.leader.press_before")).FontSize(typeBody).TextColor(theme.Text).SingleLine()
			onboardingKeycap(c, theme, triggerKeyLabel(trig.LeaderKey, goos))
			ui.Text(c, i18n.T("onboarding.leader.press_after")).FontSize(typeBody).TextColor(theme.Text).SingleLine()
		})
		if nodes := cfg.Leader.Nodes; len(nodes) > 0 {
			ui.Grid(c).Columns(2).GapX(space12).GapY(space6).FillWidth().Children(func() {
				for i, n := range nodes {
					if i == onboardingLeaderPreview {
						break
					}
					ui.Row(c).Key("onboarding-leader:" + n.Key).Gap(space8).AlignItems(ui.Center).Children(func() {
						leaderKeycap(c, theme, n.Key)
						ui.Text(c, i18n.DisplayLabel(n.Label)).FontSize(typeLabel).TextColor(theme.Text).SingleLine().Shrink(1)
					})
				}
			})
		}
		if dt := trig.DoubleTapKey; dt != "" && dt != trig.LeaderKey {
			ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, i18n.T("onboarding.leader.double_tap_before")).FontSize(typeLabel).TextColor(theme.TextMuted).SingleLine()
				keycap(c, theme, triggerKeyLabel(dt, goos), keyToneNeutral)
				ui.Text(c, i18n.T("onboarding.leader.double_tap_after")).FontSize(typeLabel).TextColor(theme.TextMuted).SingleLine()
			})
		}
		ui.Row(c).FillWidth().Gap(space10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1).Shrink(1).Children(func() {
				switch {
				case seen:
					onboardingNote(c, theme, "checkmark", theme.Success, i18n.T("onboarding.leader.seen"))
				case !granted:
					onboardingNote(c, theme, "lock.fill", theme.TextMuted, i18n.T("onboarding.leader.blocked"))
				case !listening:
					onboardingNote(c, theme, "clock", theme.TextMuted, i18n.T("onboarding.leader.blocked_monitor"))
				default:
					onboardingNote(c, theme, "keyboard", accentInk(theme, theme.Surface), i18n.T("onboarding.leader.waiting"))
				}
			})
			if ui.Button(c, i18n.T("onboarding.leader.change")).Key("onboarding-change-key").Clicked() {
				a.OpenSettings(SettingsTabGeneral)
			}
		})
	})
}

// onboardingKeycap is the Leader key in the sentence that asks for it:
// a keycap at body size.
func onboardingKeycap(c *ui.Context, theme *ui.Theme, label string) {
	ui.Box(c).Padding(space4, space10).Radius(radiusChip).Shrink(0).
		Background(keycapFill(theme)).Border(1, keycapEdge(theme)).Shadow(0, 1, 0, 0, keycapLip(theme)).
		Children(func() {
			ui.Text(c, label).FontSize(typeBody).FontWeight(600).TextColor(theme.Text).SingleLine()
		})
}

// renderOnboardingShortcuts is step 3: the shortcuts that open Clavis
// without the Leader key, and a snippet to type, all as configured.
func renderOnboardingShortcuts(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, goos string) {
	trig := cfg.Main.Trigger
	onboardingStep(c, theme, 3, i18n.T("onboarding.step.shortcuts"), false, func() {
		shortcut := func(key, keys, caption string) {
			labels := shortcutKeyLabels(keys, goos)
			if len(labels) == 0 {
				return
			}
			ui.Row(c).Key("onboarding-shortcut:" + key).FillWidth().Gap(space10).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Width(onboardingKeysWidth).Shrink(0).Gap(space2).AlignItems(ui.Center).Children(func() {
					for _, l := range labels {
						keycap(c, theme, l, keyToneNeutral)
					}
				})
				ui.Text(c, caption).FontSize(typeLabel).TextColor(theme.Text).SingleLine().Shrink(1)
			})
		}
		shortcut("capsule", trig.CapsuleShortcut, i18n.T("onboarding.shortcut.capsule"))
		shortcut("clipboard", trig.ClipboardShortcut, i18n.T("onboarding.shortcut.clipboard"))
		if trigger := firstSnippetTrigger(cfg); trig.SnippetExpansionEnabled && trigger != "" {
			ui.Row(c).Key("onboarding-shortcut:snippet").FillWidth().Gap(space10).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Width(onboardingKeysWidth).Shrink(0).Children(func() {
					chip(c, theme, "", trigger, chipNeutral)
				})
				ui.Text(c, i18n.T("onboarding.shortcut.snippet")).FontSize(typeLabel).TextColor(theme.Text).SingleLine().Shrink(1)
			})
		}
	})
}

// firstSnippetTrigger is a snippet trigger to show as an example.
func firstSnippetTrigger(cfg config.ClavisConfig) string {
	for _, s := range cfg.Snippets.Items {
		if t := strings.TrimSpace(s.Trigger); t != "" {
			return t
		}
	}
	return ""
}

func renderOnboardingFooter(c *ui.Context, theme *ui.Theme, a *AppController, hooks OnboardingHooks, ready bool) {
	ui.Box(c).FillWidth().Height(1).Background(theme.Border)
	ui.Row(c).FillWidth().Padding(space12, space16+8).Gap(space10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("onboarding.footer.reopen")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).Shrink(1)
		if ui.Button(c, i18n.T("onboarding.footer.settings")).Key("onboarding-settings").Clicked() {
			a.OpenSettings(SettingsTabGeneral)
		}
		// Until the permissions are in place the accent stays on step 1.
		btn := ui.Button
		if ready {
			btn = ui.PrimaryButton
		}
		if btn(c, i18n.T("onboarding.footer.start")).Key("onboarding-start").Clicked() && hooks.Finish != nil {
			hooks.Finish()
		}
	})
}

// shortcutKeyLabels names the keys of a shortcut as the keyboard in front of
// the user labels them: "option+space" is ⌥ Space on a Mac, Alt Space on a
// PC. Modifiers come in the platform's order (⌃⌥⇧⌘ on a Mac, Win Ctrl Alt
// Shift on a PC). An unparsable shortcut has no labels.
func shortcutKeyLabels(shortcut, goos string) []string {
	hk, err := bridge.ParseHotkey(shortcut)
	if err != nil {
		return nil
	}
	mac := goos == "darwin"
	order := []string{"command", "control", "option", "shift", "fn"}
	if mac {
		order = []string{"control", "option", "shift", "command", "fn"}
	}
	names := map[string][2]string{ // mod → {mac, pc}
		"control": {"⌃", "Ctrl"},
		"option":  {"⌥", "Alt"},
		"shift":   {"⇧", "Shift"},
		"command": {"⌘", "Win"},
		"fn":      {"fn", "Fn"},
	}
	var labels []string
	for _, mod := range order {
		if slices.Contains(hk.Modifiers, mod) {
			labels = append(labels, byPlatform(mac, names[mod][0], names[mod][1]))
		}
	}
	return append(labels, hotkeyKeyLabel(hk.Key, mac))
}

// hotkeyKeyLabel names a normalized key (bridge.NormalizeShortcutKey).
func hotkeyKeyLabel(key string, mac bool) string {
	switch key {
	case "space":
		return "Space"
	case "return":
		return byPlatform(mac, "↵", "Enter")
	case "escape":
		return "Esc"
	case "tab":
		return "Tab"
	case "delete", "backspace":
		return byPlatform(mac, "⌫", "Backspace")
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "grave":
		return "`"
	case "minus":
		return "-"
	case "equal":
		return "="
	case "leftbracket":
		return "["
	case "rightbracket":
		return "]"
	case "semicolon":
		return ";"
	case "quote":
		return "'"
	case "comma":
		return ","
	case "period":
		return "."
	case "slash":
		return "/"
	case "backslash":
		return "\\"
	}
	if utf8.RuneCountInString(key) == 1 {
		return strings.ToUpper(key)
	}
	return strings.ToUpper(key[:1]) + key[1:]
}
