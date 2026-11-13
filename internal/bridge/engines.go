package bridge

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yeisme/clavis/pkg/core/config"
)

// Popover placement constants (in points/DIPs).
const (
	PopoverScreenMargin = 12.0
	PopoverAnchorGap    = 6.0
)

// Rect represents a 2D rectangle in unified top-left screen coordinates
// (X increases right, Y increases downward), unless explicitly converted to AppKit.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (r Rect) MaxX() float64 { return r.X + r.Width }
func (r Rect) MaxY() float64 { return r.Y + r.Height }
func (r Rect) Center() Point {
	return Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
}
func (r Rect) IsZero() bool {
	return r.Width <= 0 || r.Height <= 0
}

// Point represents a 2D coordinate.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Size represents 2D dimensions.
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// DisplayInfo describes a monitor's full bounds and usable work area (excluding dock/menu bar/taskbar).
type DisplayInfo struct {
	ID       int64 `json:"id"`
	Bounds   Rect  `json:"bounds"`
	WorkArea Rect  `json:"workArea"`
	Primary  bool  `json:"primary"`
}

func (d DisplayInfo) effectiveWorkArea() Rect {
	if !d.WorkArea.IsZero() {
		return d.WorkArea
	}
	return d.Bounds
}

// ---- DoubleTapDetector ----

type doubleTapState int

const (
	dtIdle doubleTapState = iota
	dtFirstDown
	dtFirstUp
	dtSecondDown
)

// DoubleTapDetector is a pure state machine detecting rapid modifier double-taps
// while rejecting intervening keys, hold-too-long presses, and opposite-side modifiers.
type DoubleTapDetector struct {
	mu           sync.Mutex
	target       string
	intervalMs   int64
	maxHoldMs    int64
	state        doubleTapState
	firstSide    string
	firstDownMs  int64
	firstUpMs    int64
	secondDownMs int64
}

// NewDoubleTapDetector constructs a DoubleTapDetector for targetKey and intervalMs.
func NewDoubleTapDetector(targetKey string, intervalMs int) *DoubleTapDetector {
	d := &DoubleTapDetector{}
	d.SetConfig(targetKey, intervalMs)
	return d
}

// SetConfig updates the target modifier key and interval, resetting pending state.
func (d *DoubleTapDetector) SetConfig(targetKey string, intervalMs int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if intervalMs <= 0 {
		intervalMs = 300
	}
	d.target = NormalizeTriggerModifier(targetKey)
	d.intervalMs = int64(intervalMs)
	d.maxHoldMs = int64(intervalMs)
	d.resetLocked()
}

// SetMaxHoldMs overrides the maximum duration a single tap may be held down.
func (d *DoubleTapDetector) SetMaxHoldMs(maxHoldMs int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if maxHoldMs > 0 {
		d.maxHoldMs = int64(maxHoldMs)
	}
}

// Reset clears any in-progress double-tap state.
func (d *DoubleTapDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resetLocked()
}

func (d *DoubleTapDetector) resetLocked() {
	d.state = dtIdle
	d.firstSide = ""
	d.firstDownMs = 0
	d.firstUpMs = 0
	d.secondDownMs = 0
}

// HandleInterveningKey resets the state machine when any non-modifier key is pressed.
func (d *DoubleTapDetector) HandleInterveningKey() {
	d.Reset()
}

// HandleModifier processes a modifier press (isDown=true) or release (isDown=false) at nowMs.
// otherModifiersDown must be true if any other modifier key is simultaneously held.
// Returns true on completion of a valid double-tap.
func (d *DoubleTapDetector) HandleModifier(mod string, isDown bool, otherModifiersDown bool, nowMs int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.target == "" || otherModifiersDown {
		d.resetLocked()
		return false
	}

	norm := NormalizeTriggerModifier(mod)
	if !modifierMatchesTarget(norm, d.target) {
		// Opposite-side or unrelated modifier cancels any pending tap.
		d.resetLocked()
		return false
	}

	switch d.state {
	case dtIdle:
		if isDown {
			d.state = dtFirstDown
			d.firstSide = norm
			d.firstDownMs = nowMs
		}
		return false

	case dtFirstDown:
		if isDown {
			if d.firstSide != norm {
				d.resetLocked()
			}
			return false
		}
		if d.firstSide != norm {
			d.resetLocked()
			return false
		}
		hold := nowMs - d.firstDownMs
		if hold < 0 || hold > d.maxHoldMs {
			d.resetLocked()
			return false
		}
		d.state = dtFirstUp
		d.firstUpMs = nowMs
		return false

	case dtFirstUp:
		if !isDown {
			return false
		}
		gap := nowMs - d.firstUpMs
		if gap < 0 || gap > d.intervalMs {
			// Gap expired; treat this press as the start of a fresh first tap.
			d.state = dtFirstDown
			d.firstSide = norm
			d.firstDownMs = nowMs
			return false
		}
		if d.firstSide != "" && norm != d.firstSide {
			d.resetLocked()
			return false
		}
		d.state = dtSecondDown
		d.secondDownMs = nowMs
		return false

	case dtSecondDown:
		if isDown {
			if d.firstSide != norm {
				d.resetLocked()
			}
			return false
		}
		if d.firstSide != norm {
			d.resetLocked()
			return false
		}
		hold := nowMs - d.secondDownMs
		d.resetLocked()
		return hold >= 0 && hold <= d.maxHoldMs
	}

	return false
}

// NormalizeTriggerModifier normalizes trigger modifier aliases across macOS/Windows/Linux.
func NormalizeTriggerModifier(raw string) string {
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "-", "_")
	switch s {
	case "right_option", "right_alt", "roption", "ralt", "right_opt":
		return "right_option"
	case "left_option", "left_alt", "loption", "lalt", "left_opt":
		return "left_option"
	case "option", "alt", "opt":
		return "option"
	case "right_command", "right_cmd", "rcommand", "rcmd", "right_win", "rwin", "right_meta", "rmeta", "right_super":
		return "right_command"
	case "left_command", "left_cmd", "lcommand", "lcmd", "left_win", "lwin", "left_meta", "lmeta", "left_super":
		return "left_command"
	case "command", "cmd", "super", "win", "windows", "meta":
		return "command"
	case "right_shift", "rshift":
		return "right_shift"
	case "left_shift", "lshift":
		return "left_shift"
	case "shift":
		return "shift"
	case "right_control", "right_ctrl", "rcontrol", "rctrl":
		return "right_control"
	case "left_control", "left_ctrl", "lcontrol", "lctrl":
		return "left_control"
	case "control", "ctrl":
		return "control"
	case "caps_lock", "capslock", "caps":
		return "caps_lock"
	default:
		return s
	}
}

func modifierMatchesTarget(normMod, target string) bool {
	if normMod == "" || target == "" {
		return false
	}
	if normMod == target {
		return true
	}
	switch target {
	case "option":
		return normMod == "left_option" || normMod == "right_option"
	case "command":
		return normMod == "left_command" || normMod == "right_command"
	case "shift":
		return normMod == "left_shift" || normMod == "right_shift"
	case "control":
		return normMod == "left_control" || normMod == "right_control"
	default:
		return false
	}
}

// ---- HotkeyMatcher ----

// Hotkey represents a parsed global shortcut (modifiers + primary key).
type Hotkey struct {
	Raw       string
	Modifiers []string
	Key       string
	modSet    map[string]struct{}
}

// ParseHotkey parses shortcut strings such as "option+space", "alt+shift+space",
// "command+shift+k", or "ctrl+alt+p".
func ParseHotkey(shortcut string) (Hotkey, error) {
	trimmed := strings.TrimSpace(shortcut)
	if trimmed == "" {
		return Hotkey{}, errors.New("hotkey: empty shortcut")
	}

	parts := strings.Split(trimmed, "+")
	modSet := make(map[string]struct{})
	var key string

	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			return Hotkey{}, fmt.Errorf("hotkey: empty token in %q", shortcut)
		}
		if mod, ok := NormalizeShortcutModifier(token); ok {
			modSet[mod] = struct{}{}
			continue
		}
		normKey := NormalizeShortcutKey(token)
		if normKey == "" {
			return Hotkey{}, fmt.Errorf("hotkey: unsupported token %q in %q", token, shortcut)
		}
		if key != "" {
			return Hotkey{}, fmt.Errorf("hotkey: multiple primary keys in %q", shortcut)
		}
		key = normKey
	}

	if key == "" {
		return Hotkey{}, fmt.Errorf("hotkey: missing primary key in %q", shortcut)
	}

	mods := make([]string, 0, len(modSet))
	for m := range modSet {
		mods = append(mods, m)
	}
	sort.Strings(mods)

	return Hotkey{
		Raw:       shortcut,
		Modifiers: mods,
		Key:       key,
		modSet:    modSet,
	}, nil
}

// Matches reports whether the normalized key and active modifier set exactly match h.
func (h Hotkey) Matches(key string, modifiers []string) bool {
	if h.Key == "" || NormalizeShortcutKey(key) != h.Key {
		return false
	}
	active := make(map[string]struct{}, len(modifiers))
	for _, m := range modifiers {
		norm, ok := NormalizeShortcutModifier(m)
		if !ok {
			return false
		}
		active[norm] = struct{}{}
	}
	if len(active) != len(h.modSet) {
		return false
	}
	for m := range h.modSet {
		if _, ok := active[m]; !ok {
			return false
		}
	}
	return true
}

// NormalizeShortcutModifier normalizes shortcut modifier aliases to canonical names.
func NormalizeShortcutModifier(raw string) (string, bool) {
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "-", "_")
	switch s {
	case "option", "opt", "alt", "left_option", "right_option", "left_alt", "right_alt":
		return "option", true
	case "command", "cmd", "super", "win", "windows", "meta", "left_command", "right_command", "left_win", "right_win", "left_meta", "right_meta":
		return "command", true
	case "shift", "left_shift", "right_shift":
		return "shift", true
	case "control", "ctrl", "left_control", "right_control", "left_ctrl", "right_ctrl":
		return "control", true
	case "fn", "function":
		return "fn", true
	default:
		return "", false
	}
}

// NormalizeShortcutKey normalizes key names and aliases.
func NormalizeShortcutKey(raw string) string {
	if raw == " " {
		return "space"
	}
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "space":
		return "space"
	case "enter", "return":
		return "return"
	case "esc", "escape":
		return "escape"
	case "backtick", "grave", "`":
		return "grave"
	case "minus", "-":
		return "minus"
	case "equal", "=":
		return "equal"
	case "leftbracket", "[":
		return "leftbracket"
	case "rightbracket", "]":
		return "rightbracket"
	case "semicolon", ";":
		return "semicolon"
	case "quote", "'":
		return "quote"
	case "comma", ",":
		return "comma"
	case "period", ".":
		return "period"
	case "slash", "/":
		return "slash"
	case "backslash", `\`:
		return "backslash"
	case "tab", "backspace", "delete", "forwarddelete",
		"left", "right", "up", "down", "home", "end", "pageup", "pagedown",
		"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		return s
	}
	if len(s) == 1 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= '0' && s[0] <= '9')) {
		return s
	}
	return ""
}

// ---- KeymapLayerEngine ----

// KeyAction represents a synthesized keystroke (and optional modifier chord) to emit.
type KeyAction struct {
	KeyName   string   `json:"keyName"`
	Modifiers []string `json:"modifiers,omitempty"`
}

// KeymapOutcome describes how the host bridge should handle a raw key event after KeymapLayerEngine evaluation.
type KeymapOutcome struct {
	Consume     bool        `json:"consume"`
	EmitKeys    []KeyAction `json:"emitKeys,omitempty"`
	LayerActive string      `json:"layerActive,omitempty"`
}

var navLayerKeyMap = map[string]string{
	"h":         "left",
	"j":         "down",
	"k":         "up",
	"l":         "right",
	"u":         "pageup",
	"d":         "pagedown",
	"0":         "home",
	"4":         "end",
	"backspace": "delete",
}

var hyperModifiers = []string{"command", "control", "option", "shift"}

// KeymapLayerEngine evaluates dual-role Tap/Hold keys (such as CapsLock -> Escape on tap,
// Vim navigation layer or Hyper modifier on hold) and custom KeyRemapRules.
type KeymapLayerEngine struct {
	mu               sync.Mutex
	cfg              config.KeymapConfig
	thresholdMs      int64
	heldKey          string
	heldRule         config.KeyRemapRule
	heldDownMs       int64
	comboUsed        bool
	activeLayer      string
	consumedDownKeys map[string]struct{}
}

// NewKeymapLayerEngine constructs a KeymapLayerEngine from cfg.
func NewKeymapLayerEngine(cfg config.KeymapConfig) *KeymapLayerEngine {
	e := &KeymapLayerEngine{
		consumedDownKeys: make(map[string]struct{}),
	}
	e.UpdateConfig(cfg)
	return e
}

// UpdateConfig replaces the active KeymapConfig and resets transient hold state.
func (e *KeymapLayerEngine) UpdateConfig(cfg config.KeymapConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = cfg
	thresh := cfg.TapHoldThresholdMs
	if thresh <= 0 {
		thresh = 200
	}
	e.thresholdMs = int64(thresh)
	e.resetLocked()
}

// IsEnabled reports whether the keymap engine is enabled.
func (e *KeymapLayerEngine) IsEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.Enabled
}

// ActiveLayer returns the currently active hold layer name (e.g. "nav", "hyper", or "").
func (e *KeymapLayerEngine) ActiveLayer() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeLayer
}

// Reset clears any held key or active layer state.
func (e *KeymapLayerEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetLocked()
}

func (e *KeymapLayerEngine) resetLocked() {
	e.heldKey = ""
	e.heldRule = config.KeyRemapRule{}
	e.heldDownMs = 0
	e.comboUsed = false
	e.activeLayer = ""
	clear(e.consumedDownKeys)
}

// HandleKey is a convenience wrapper around HandleKeyEvent with no extra modifiers or app bundle filter.
func (e *KeymapLayerEngine) HandleKey(key string, isDown bool, nowMs int64) KeymapOutcome {
	return e.HandleKeyEvent(key, isDown, nil, "", nowMs)
}

// HandleKeyEvent processes a key/modifier press (isDown=true) or release (isDown=false).
func (e *KeymapLayerEngine) HandleKeyEvent(key string, isDown bool, modifiers []string, appBundleID string, nowMs int64) KeymapOutcome {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.cfg.Enabled {
		return KeymapOutcome{}
	}

	normKey := normalizeKeymapKeyName(key)
	if normKey == "" {
		return KeymapOutcome{LayerActive: e.activeLayer}
	}

	// 1. If the currently held trigger key is being released or repeated:
	if e.heldKey != "" && normKey == e.heldKey {
		if isDown {
			// Auto-repeat while holding the layer/trigger key
			return KeymapOutcome{
				Consume:     true,
				LayerActive: e.activeLayer,
			}
		}
		// Release of held trigger key
		duration := nowMs - e.heldDownMs
		isTap := !e.comboUsed && duration >= 0 && duration <= e.thresholdMs
		tapSpec := strings.TrimSpace(e.heldRule.Tap)

		e.heldKey = ""
		e.heldRule = config.KeyRemapRule{}
		e.heldDownMs = 0
		e.comboUsed = false
		e.activeLayer = ""

		if isTap && tapSpec != "" {
			if act, ok := parseKeyActionSpec(tapSpec, modifiers); ok {
				return KeymapOutcome{
					Consume:     true,
					EmitKeys:    []KeyAction{act},
					LayerActive: "",
				}
			}
		}
		return KeymapOutcome{
			Consume:     true,
			LayerActive: "",
		}
	}

	// 2. If a layer/hold key is currently held and another key is pressed or released:
	if e.heldKey != "" {
		if !isDown {
			delete(e.consumedDownKeys, normKey)
			return KeymapOutcome{
				Consume:     true,
				LayerActive: e.activeLayer,
			}
		}

		// Another key pressed while trigger key is held -> cancels tap emission on release!
		e.comboUsed = true
		e.consumedDownKeys[normKey] = struct{}{}

		holdMode := strings.ToLower(strings.TrimSpace(e.heldRule.Hold))
		switch holdMode {
		case "nav":
			if e.cfg.NavLayerEnabled {
				if navTarget, ok := navLayerKeyMap[normKey]; ok {
					return KeymapOutcome{
						Consume: true,
						EmitKeys: []KeyAction{{
							KeyName:   navTarget,
							Modifiers: cleanActionModifiers(modifiers),
						}},
						LayerActive: "nav",
					}
				}
			}
			return KeymapOutcome{
				Consume:     true,
				LayerActive: e.activeLayer,
			}

		case "hyper":
			return KeymapOutcome{
				Consume: true,
				EmitKeys: []KeyAction{{
					KeyName:   normKey,
					Modifiers: append([]string(nil), hyperModifiers...),
				}},
				LayerActive: "hyper",
			}

		default:
			if holdMod, ok := NormalizeShortcutModifier(holdMode); ok {
				mods := mergeModifiers(cleanActionModifiers(modifiers), holdMod)
				return KeymapOutcome{
					Consume: true,
					EmitKeys: []KeyAction{{
						KeyName:   normKey,
						Modifiers: mods,
					}},
					LayerActive: holdMod,
				}
			}
			if holdMode != "" {
				if act, ok := parseKeyActionSpec(holdMode, modifiers); ok {
					return KeymapOutcome{
						Consume:     true,
						EmitKeys:    []KeyAction{act},
						LayerActive: e.activeLayer,
					}
				}
			}
			return KeymapOutcome{
				Consume:     true,
				LayerActive: e.activeLayer,
			}
		}
	}

	// 3. No trigger key is currently held. If this is a key release of a previously consumed combo key, consume it.
	if !isDown {
		if _, wasConsumed := e.consumedDownKeys[normKey]; wasConsumed {
			delete(e.consumedDownKeys, normKey)
			return KeymapOutcome{Consume: true}
		}
		return KeymapOutcome{}
	}

	// 4. Check if normKey matches a configured remap rule or the built-in CapsLock rule.
	if rule, ok := e.findMatchingRemapRule(normKey, appBundleID); ok {
		e.heldKey = normKey
		e.heldRule = rule
		e.heldDownMs = nowMs
		e.comboUsed = false
		e.activeLayer = e.resolveActiveLayerName(rule)
		return KeymapOutcome{
			Consume:     true,
			LayerActive: e.activeLayer,
		}
	}

	return KeymapOutcome{}
}

func (e *KeymapLayerEngine) findMatchingRemapRule(normKey string, appBundleID string) (config.KeyRemapRule, bool) {
	for _, r := range e.cfg.Remaps {
		if normalizeKeymapKeyName(r.From) != normKey {
			continue
		}
		if !remapRuleMatchesApp(r.AppBundleIDs, appBundleID) {
			continue
		}
		return r, true
	}

	if normKey == "caps_lock" {
		tap := strings.TrimSpace(e.cfg.CapsLockTap)
		hold := strings.TrimSpace(e.cfg.CapsLockHold)
		if strings.EqualFold(hold, "nav") && !e.cfg.NavLayerEnabled {
			hold = ""
		}
		if tap != "" || hold != "" {
			return config.KeyRemapRule{
				From: "caps_lock",
				Tap:  tap,
				Hold: hold,
			}, true
		}
	}

	return config.KeyRemapRule{}, false
}

func (e *KeymapLayerEngine) resolveActiveLayerName(rule config.KeyRemapRule) string {
	hold := strings.ToLower(strings.TrimSpace(rule.Hold))
	switch hold {
	case "":
		return ""
	case "nav":
		if e.cfg.NavLayerEnabled {
			return "nav"
		}
		return ""
	case "hyper":
		return "hyper"
	default:
		if mod, ok := NormalizeShortcutModifier(hold); ok {
			return mod
		}
		return hold
	}
}

func remapRuleMatchesApp(allowedBundleIDs []string, appBundleID string) bool {
	if len(allowedBundleIDs) == 0 {
		return true
	}
	cur := strings.TrimSpace(appBundleID)
	if cur == "" {
		return false
	}
	normCur := normalizeProcessName(cur)
	for _, id := range allowedBundleIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "*" || strings.EqualFold(trimmed, cur) || (normCur != "" && normalizeProcessName(trimmed) == normCur) {
			return true
		}
	}
	return false
}

func normalizeKeymapKeyName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	mod := NormalizeTriggerModifier(trimmed)
	switch mod {
	case "caps_lock",
		"right_option", "left_option", "option",
		"right_command", "left_command", "command",
		"right_shift", "left_shift", "shift",
		"right_control", "left_control", "control":
		return mod
	}
	if k := NormalizeShortcutKey(trimmed); k != "" {
		return k
	}
	return strings.ToLower(trimmed)
}

func parseKeyActionSpec(spec string, extraModifiers []string) (KeyAction, bool) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return KeyAction{}, false
	}
	if strings.Contains(trimmed, "+") {
		if hk, err := ParseHotkey(trimmed); err == nil {
			return KeyAction{
				KeyName:   hk.Key,
				Modifiers: append([]string(nil), hk.Modifiers...),
			}, true
		}
	}
	norm := NormalizeShortcutKey(trimmed)
	if norm == "" {
		norm = strings.ToLower(trimmed)
	}
	return KeyAction{
		KeyName:   norm,
		Modifiers: cleanActionModifiers(extraModifiers),
	}, true
}

func cleanActionModifiers(mods []string) []string {
	if len(mods) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{}, len(mods))
	for _, m := range mods {
		if norm, ok := NormalizeShortcutModifier(m); ok {
			if _, exists := seen[norm]; !exists {
				seen[norm] = struct{}{}
				out = append(out, norm)
			}
		}
	}
	return out
}

func mergeModifiers(existing []string, extra string) []string {
	out := append([]string(nil), existing...)
	for _, m := range out {
		if m == extra {
			return out
		}
	}
	return append(out, extra)
}

// ---- ScreenGeometryClamp ----

// QuartzToAppKitRect converts a Quartz top-left rect to an AppKit bottom-left rect.
func QuartzToAppKitRect(r Rect, primaryDisplayHeight float64) Rect {
	return Rect{
		X:      r.X,
		Y:      primaryDisplayHeight - r.Y - r.Height,
		Width:  r.Width,
		Height: r.Height,
	}
}

// AppKitToQuartzRect converts an AppKit bottom-left rect to a Quartz top-left rect.
func AppKitToQuartzRect(r Rect, primaryDisplayHeight float64) Rect {
	return Rect{
		X:      r.X,
		Y:      primaryDisplayHeight - r.Y - r.Height,
		Width:  r.Width,
		Height: r.Height,
	}
}

// QuartzToAppKitPoint converts a Quartz top-left point to an AppKit bottom-left point.
func QuartzToAppKitPoint(p Point, primaryDisplayHeight float64) Point {
	return Point{X: p.X, Y: primaryDisplayHeight - p.Y}
}

// AppKitToQuartzPoint converts an AppKit bottom-left point to a Quartz top-left point.
func AppKitToQuartzPoint(p Point, primaryDisplayHeight float64) Point {
	return Point{X: p.X, Y: primaryDisplayHeight - p.Y}
}

// PlacePopover computes a popover frame in unified top-left screen coordinates:
// it places the popover below anchor by default, flips above anchor when the bottom
// would overflow the display's work area (with a 12pt margin), and clamps X/Y to stay
// inside the target display's work area with a 12pt margin.
func PlacePopover(anchor Rect, popoverSize Size, displays []DisplayInfo) Rect {
	if len(displays) == 0 {
		return Rect{
			X:      anchor.X,
			Y:      anchor.Y + anchor.Height + PopoverAnchorGap,
			Width:  popoverSize.Width,
			Height: popoverSize.Height,
		}
	}

	target := selectDisplayForPoint(anchor.Center(), displays)
	wa := target.effectiveWorkArea()

	x := anchor.X
	y := anchor.Y + anchor.Height + PopoverAnchorGap

	maxAllowedBottom := wa.MaxY() - PopoverScreenMargin
	if y+popoverSize.Height > maxAllowedBottom {
		aboveY := anchor.Y - popoverSize.Height - PopoverAnchorGap
		if aboveY >= wa.Y+PopoverScreenMargin {
			y = aboveY
		}
	}

	x = clampAxis(x, popoverSize.Width, wa.X, wa.Width, PopoverScreenMargin)
	y = clampAxis(y, popoverSize.Height, wa.Y, wa.Height, PopoverScreenMargin)

	return Rect{
		X:      x,
		Y:      y,
		Width:  popoverSize.Width,
		Height: popoverSize.Height,
	}
}

func clampAxis(pos, size, start, span, margin float64) float64 {
	minPos := start + margin
	maxPos := start + span - size - margin
	if maxPos < minPos {
		return minPos
	}
	if pos < minPos {
		return minPos
	}
	if pos > maxPos {
		return maxPos
	}
	return pos
}

func selectDisplayForPoint(p Point, displays []DisplayInfo) DisplayInfo {
	for _, d := range displays {
		if pointInRect(p, d.Bounds) {
			return d
		}
	}
	// Nearest display by distance to bounds
	bestIdx := -1
	bestDist := math.Inf(1)
	for i, d := range displays {
		b := d.Bounds
		dx := max(max(b.X-p.X, 0), p.X-b.MaxX())
		dy := max(max(b.Y-p.Y, 0), p.Y-b.MaxY())
		dist := dx*dx + dy*dy
		if dist < bestDist {
			bestDist = dist
			bestIdx = i
		}
	}
	if bestIdx >= 0 {
		return displays[bestIdx]
	}
	for _, d := range displays {
		if d.Primary {
			return d
		}
	}
	return displays[0]
}

func selectDisplayForRect(r Rect, displays []DisplayInfo) int {
	if len(displays) == 0 {
		return -1
	}
	bestIdx := 0
	bestArea := -1.0
	for i, d := range displays {
		b := d.Bounds
		overlapW := min(r.MaxX(), b.MaxX()) - max(r.X, b.X)
		overlapH := min(r.MaxY(), b.MaxY()) - max(r.Y, b.Y)
		area := max(overlapW, 0) * max(overlapH, 0)
		if area > bestArea {
			bestArea = area
			bestIdx = i
		}
	}
	if bestArea > 0 {
		return bestIdx
	}
	center := r.Center()
	best := selectDisplayForPoint(center, displays)
	for i, d := range displays {
		if d.ID == best.ID && d.Bounds == best.Bounds {
			return i
		}
	}
	return 0
}

func pointInRect(p Point, r Rect) bool {
	return p.X >= r.X && p.X <= r.MaxX() && p.Y >= r.Y && p.Y <= r.MaxY()
}

// ---- IMESwitchDecisionEngine ----

// AppIdentity identifies the currently focused application and window/URL context across macOS/Windows/Linux.
type AppIdentity struct {
	BundleID    string `json:"bundleId,omitempty"`
	ProcessName string `json:"processName,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
	ActiveURL   string `json:"activeUrl,omitempty"`
}

// IMEDecision describes whether the input source should switch upon an app activation or temporary lock.
type IMEDecision struct {
	ShouldSwitch        bool           `json:"shouldSwitch"`
	TargetInputSourceID string         `json:"targetInputSourceId,omitempty"`
	TargetMode          config.IMEMode `json:"targetMode,omitempty"`
	Reason              string         `json:"reason,omitempty"`
	Restored            bool           `json:"restored,omitempty"`
}

// IMESwitchDecisionEngine evaluates per-app (and URL/Title context) IME auto-switch rules,
// tracks a restore_on_leave stack across consecutive rule-matched apps, and supports
// independent temporary ASCII locking for Leader/Capsule sessions.
type IMESwitchDecisionEngine struct {
	mu                   sync.Mutex
	enabled              bool
	rules                []config.IMERule
	lastApp              AppIdentity
	hasLastApp           bool
	inRuleSession        bool
	savedInputSourceID   string
	savedWasNative       bool
	shouldRestoreOnLeave bool
	tempLockActive       bool
	tempSavedInputSource string
	tempSavedWasNative   bool
}

// NewIMESwitchDecisionEngine creates an IMESwitchDecisionEngine.
func NewIMESwitchDecisionEngine(enabled bool, rules config.IMERulesConfig) *IMESwitchDecisionEngine {
	e := &IMESwitchDecisionEngine{}
	e.UpdateRules(enabled, rules)
	return e
}

// UpdateRules replaces the active IME configuration and rules.
func (e *IMESwitchDecisionEngine) UpdateRules(enabled bool, rules config.IMERulesConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = enabled
	e.rules = append([]config.IMERule(nil), rules.Rules...)
}

// Reset clears active session, restore stack, and temporary lock state.
func (e *IMESwitchDecisionEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastApp = AppIdentity{}
	e.hasLastApp = false
	e.inRuleSession = false
	e.savedInputSourceID = ""
	e.savedWasNative = false
	e.shouldRestoreOnLeave = false
	e.tempLockActive = false
	e.tempSavedInputSource = ""
	e.tempSavedWasNative = false
}

// IsTemporaryLocked reports whether a temporary ASCII lock is currently active.
func (e *IMESwitchDecisionEngine) IsTemporaryLocked() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tempLockActive
}

// TemporaryLockASCII records currentInputSourceID in an independent temporary lock slot
// and switches to asciiInputSourceID (for Leader/Capsule sessions) without disturbing the app-rule stack.
func (e *IMESwitchDecisionEngine) TemporaryLockASCII(asciiInputSourceID string, currentInputSourceID string) IMEDecision {
	return e.TemporaryLockASCIIWithState(asciiInputSourceID, currentInputSourceID, false)
}

// TemporaryLockASCIIWithState records both currentInputSourceID and whether a native/non-ASCII conversion mode
// was active (e.g. Windows IMM32 Chinese mode under a shared HKL) before locking to ASCII.
func (e *IMESwitchDecisionEngine) TemporaryLockASCIIWithState(asciiInputSourceID string, currentInputSourceID string, currentWasNative bool) IMEDecision {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := strings.TrimSpace(asciiInputSourceID)
	cur := strings.TrimSpace(currentInputSourceID)
	if !e.tempLockActive {
		e.tempLockActive = true
		e.tempSavedInputSource = cur
		e.tempSavedWasNative = currentWasNative
	}
	if target != "" {
		if target != cur {
			return IMEDecision{
				ShouldSwitch:        true,
				TargetInputSourceID: target,
				TargetMode:          config.IMEModeASCII,
				Reason:              "temporary_lock_ascii",
			}
		}
		// Same input source (e.g. shared Windows HKL): only the conversion mode needs clearing.
		if currentWasNative {
			return IMEDecision{
				ShouldSwitch: true,
				TargetMode:   config.IMEModeASCII,
				Reason:       "temporary_lock_ascii",
			}
		}
		return IMEDecision{}
	}
	if currentWasNative {
		return IMEDecision{
			ShouldSwitch: true,
			TargetMode:   config.IMEModeASCII,
			Reason:       "temporary_lock_ascii",
		}
	}
	return IMEDecision{}
}

// RestoreTemporaryLock restores the input source (or native conversion mode) that was active prior to TemporaryLockASCII.
func (e *IMESwitchDecisionEngine) RestoreTemporaryLock(currentInputSourceID string) IMEDecision {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.tempLockActive {
		return IMEDecision{}
	}
	saved := e.tempSavedInputSource
	wasNative := e.tempSavedWasNative
	e.tempLockActive = false
	e.tempSavedInputSource = ""
	e.tempSavedWasNative = false

	cur := strings.TrimSpace(currentInputSourceID)
	if saved != "" && saved != cur {
		var mode config.IMEMode
		if wasNative {
			mode = config.IMEModeNative
		}
		return IMEDecision{
			ShouldSwitch:        true,
			TargetInputSourceID: saved,
			TargetMode:          mode,
			Reason:              "restore_temporary_lock",
			Restored:            true,
		}
	}
	if wasNative {
		return IMEDecision{
			ShouldSwitch: true,
			TargetMode:   config.IMEModeNative,
			Reason:       "restore_temporary_lock",
			Restored:     true,
		}
	}
	return IMEDecision{}
}

func isSelfClavisApp(app AppIdentity) bool {
	if strings.EqualFold(strings.TrimSpace(app.BundleID), "com.yeisme.clavis") {
		return true
	}
	return normalizeProcessName(app.ProcessName) == "clavis"
}

// EvaluateAppActivation determines whether switching to app should change the input source.
func (e *IMESwitchDecisionEngine) EvaluateAppActivation(app AppIdentity, currentInputSourceID string) IMEDecision {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.enabled || isSelfClavisApp(app) {
		return IMEDecision{}
	}

	if e.hasLastApp && sameAppIdentity(e.lastApp, app) {
		return IMEDecision{}
	}
	e.lastApp = app
	e.hasLastApp = true

	// If a temporary Leader ASCII lock is currently active, the user's effective pre-lock source is tempSavedInputSource.
	effectiveCurrent := strings.TrimSpace(currentInputSourceID)
	if e.tempLockActive && e.tempSavedInputSource != "" {
		effectiveCurrent = e.tempSavedInputSource
	}

	rule, matched := e.matchRule(app)
	if matched {
		if !e.inRuleSession {
			e.inRuleSession = true
			if rule.RestoreOnLeave && (effectiveCurrent != "" || e.tempSavedWasNative) {
				e.savedInputSourceID = effectiveCurrent
				e.savedWasNative = e.tempSavedWasNative
				e.shouldRestoreOnLeave = true
			} else {
				e.savedInputSourceID = ""
				e.savedWasNative = false
				e.shouldRestoreOnLeave = false
			}
		} else if !rule.RestoreOnLeave {
			e.savedInputSourceID = ""
			e.savedWasNative = false
			e.shouldRestoreOnLeave = false
		}

		if rule.PreferredMode == config.IMEModeKeep {
			return IMEDecision{}
		}
		targetID := strings.TrimSpace(rule.InputSourceID)
		if targetID != "" {
			if targetID == currentInputSourceID {
				return IMEDecision{}
			}
			return IMEDecision{
				ShouldSwitch:        true,
				TargetInputSourceID: targetID,
				TargetMode:          rule.PreferredMode,
				Reason:              "rule_match",
			}
		}
		if rule.PreferredMode == config.IMEModeASCII || rule.PreferredMode == config.IMEModeNative {
			return IMEDecision{
				ShouldSwitch: true,
				TargetMode:   rule.PreferredMode,
				Reason:       "rule_mode_match",
			}
		}
		return IMEDecision{}
	}

	if e.inRuleSession {
		saved := e.savedInputSourceID
		wasNative := e.savedWasNative
		restore := e.shouldRestoreOnLeave
		e.inRuleSession = false
		e.savedInputSourceID = ""
		e.savedWasNative = false
		e.shouldRestoreOnLeave = false

		if restore {
			if saved != "" && saved != currentInputSourceID {
				return IMEDecision{
					ShouldSwitch:        true,
					TargetInputSourceID: saved,
					Reason:              "restore_on_leave",
					Restored:            true,
				}
			}
			if wasNative {
				return IMEDecision{
					ShouldSwitch: true,
					TargetMode:   config.IMEModeNative,
					Reason:       "restore_on_leave",
					Restored:     true,
				}
			}
		}
	}

	return IMEDecision{}
}

// matchRule evaluates rules in specificity order:
// 1. Rule with URLPattern matching app.ActiveURL (+ BundleID/ProcessName if non-empty);
// 2. Rule with TitlePattern matching app.WindowTitle (+ BundleID/ProcessName if non-empty);
// 3. Rule with BundleID (without URL/Title constraint);
// 4. Rule with ProcessName (without URL/Title constraint).
func (e *IMESwitchDecisionEngine) matchRule(app AppIdentity) (config.IMERule, bool) {
	// 1. URLPattern rules
	if strings.TrimSpace(app.ActiveURL) != "" {
		for _, r := range e.rules {
			urlPat := strings.TrimSpace(r.URLPattern)
			if urlPat == "" {
				continue
			}
			if !ruleMatchesAppConstraint(r, app) {
				continue
			}
			if !matchURLPattern(urlPat, app.ActiveURL) {
				continue
			}
			if titlePat := strings.TrimSpace(r.TitlePattern); titlePat != "" && !matchTitlePattern(titlePat, app.WindowTitle) {
				continue
			}
			return r, true
		}
	}

	// 2. TitlePattern rules
	if strings.TrimSpace(app.WindowTitle) != "" {
		for _, r := range e.rules {
			if strings.TrimSpace(r.URLPattern) != "" {
				continue
			}
			titlePat := strings.TrimSpace(r.TitlePattern)
			if titlePat == "" {
				continue
			}
			if !ruleMatchesAppConstraint(r, app) {
				continue
			}
			if matchTitlePattern(titlePat, app.WindowTitle) {
				return r, true
			}
		}
	}

	// 3. BundleID-only rules
	if app.BundleID != "" {
		for _, r := range e.rules {
			if strings.TrimSpace(r.URLPattern) != "" || strings.TrimSpace(r.TitlePattern) != "" {
				continue
			}
			if r.BundleID != "" && r.BundleID == app.BundleID {
				return r, true
			}
		}
	}

	// 4. ProcessName-only rules
	normProc := normalizeProcessName(app.ProcessName)
	if normProc != "" {
		for _, r := range e.rules {
			if strings.TrimSpace(r.URLPattern) != "" || strings.TrimSpace(r.TitlePattern) != "" {
				continue
			}
			if r.ProcessName != "" && normalizeProcessName(r.ProcessName) == normProc {
				return r, true
			}
		}
	}

	return config.IMERule{}, false
}

func ruleMatchesAppConstraint(r config.IMERule, app AppIdentity) bool {
	hasBundle := strings.TrimSpace(r.BundleID) != "" && strings.TrimSpace(r.BundleID) != "*"
	hasProc := strings.TrimSpace(r.ProcessName) != "" && strings.TrimSpace(r.ProcessName) != "*"
	if !hasBundle && !hasProc {
		return true
	}
	if hasBundle && app.BundleID != "" && strings.EqualFold(strings.TrimSpace(r.BundleID), strings.TrimSpace(app.BundleID)) {
		return true
	}
	if hasProc && app.ProcessName != "" && normalizeProcessName(r.ProcessName) == normalizeProcessName(app.ProcessName) {
		return true
	}
	return false
}

func matchURLPattern(pattern string, activeURL string) bool {
	pat := strings.ToLower(strings.TrimSpace(pattern))
	rawURL := strings.ToLower(strings.TrimSpace(activeURL))
	if pat == "" || rawURL == "" {
		return false
	}

	var host, hostAndPath string
	parseTarget := rawURL
	if !strings.Contains(parseTarget, "://") {
		parseTarget = "https://" + parseTarget
	}
	if u, err := url.Parse(parseTarget); err == nil && u.Hostname() != "" {
		host = strings.ToLower(u.Hostname())
		hostAndPath = host + u.Path
	}

	// Leading subdomain wildcard such as "*.docs.rs"
	if strings.HasPrefix(pat, "*.") && !strings.Contains(pat[2:], "*") && !strings.Contains(pat, "/") {
		domain := pat[2:]
		if domain != "" && host != "" {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return true
			}
		}
	}

	// General wildcard pattern containing '*' or '?'
	if strings.ContainsAny(pat, "*?") {
		if wildcardMatch(pat, rawURL) {
			return true
		}
		if host != "" && wildcardMatch(pat, host) {
			return true
		}
		if hostAndPath != "" && wildcardMatch(pat, hostAndPath) {
			return true
		}
		return false
	}

	// Host or subdomain match (e.g. "github.com" matches "github.com" and "gist.github.com")
	if host != "" && (host == pat || strings.HasSuffix(host, "."+pat)) {
		return true
	}

	// Substring match
	return strings.Contains(rawURL, pat)
}

func wildcardMatch(pattern, str string) bool {
	pIdx, sIdx := 0, 0
	starIdx, matchIdx := -1, 0
	for sIdx < len(str) {
		if pIdx < len(pattern) && (pattern[pIdx] == '?' || pattern[pIdx] == str[sIdx]) {
			pIdx++
			sIdx++
		} else if pIdx < len(pattern) && pattern[pIdx] == '*' {
			starIdx = pIdx
			matchIdx = sIdx
			pIdx++
		} else if starIdx != -1 {
			pIdx = starIdx + 1
			matchIdx++
			sIdx = matchIdx
		} else {
			return false
		}
	}
	for pIdx < len(pattern) && pattern[pIdx] == '*' {
		pIdx++
	}
	return pIdx == len(pattern)
}

func matchTitlePattern(pattern string, windowTitle string) bool {
	pat := strings.TrimSpace(pattern)
	if pat == "" || windowTitle == "" {
		return false
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return false
	}
	return re.MatchString(windowTitle)
}

func sameAppIdentity(a, b AppIdentity) bool {
	if a.WindowTitle != b.WindowTitle || a.ActiveURL != b.ActiveURL {
		return false
	}
	if a.BundleID != "" || b.BundleID != "" {
		return a.BundleID == b.BundleID && normalizeProcessName(a.ProcessName) == normalizeProcessName(b.ProcessName)
	}
	return normalizeProcessName(a.ProcessName) == normalizeProcessName(b.ProcessName)
}

func normalizeProcessName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimSuffix(s, ".exe")
	return s
}

// ---- WindowLayoutCalculator ----

// WindowLayoutCalculator computes target window frames for WindowCommands and
// tracks per-window pre-tiling frames for WindowRestore.
type WindowLayoutCalculator struct {
	mu          sync.Mutex
	savedFrames map[string]Rect
}

// NewWindowLayoutCalculator constructs a WindowLayoutCalculator.
func NewWindowLayoutCalculator() *WindowLayoutCalculator {
	return &WindowLayoutCalculator{
		savedFrames: make(map[string]Rect),
	}
}

// ClearSavedFrame removes any stored restore frame for windowID.
func (c *WindowLayoutCalculator) ClearSavedFrame(windowID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.savedFrames, windowID)
}

// HasSavedFrame reports whether a pre-tiling frame is currently saved for windowID.
func (c *WindowLayoutCalculator) HasSavedFrame(windowID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.savedFrames[windowID]
	return ok
}

// ComputeTargetRect calculates the target Rect for cmd.
// Returns (targetRect, applied, error). For WindowRestore when no frame is saved,
// returns (current, false, nil).
func (c *WindowLayoutCalculator) ComputeTargetRect(windowID string, current Rect, cmd config.WindowCommand, displays []DisplayInfo) (Rect, bool, error) {
	if len(displays) == 0 {
		return Rect{}, false, errors.New("window: no displays available")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if cmd == config.WindowRestore {
		saved, ok := c.savedFrames[windowID]
		if !ok {
			return current, false, nil
		}
		delete(c.savedFrames, windowID)
		return saved, true, nil
	}

	if windowID != "" {
		if _, exists := c.savedFrames[windowID]; !exists && !current.IsZero() {
			c.savedFrames[windowID] = current
		}
	}

	idx := selectDisplayForRect(current, displays)
	wa := displays[idx].effectiveWorkArea()

	switch cmd {
	case config.WindowLeftHalf:
		return Rect{X: wa.X, Y: wa.Y, Width: math.Floor(wa.Width / 2), Height: wa.Height}, true, nil
	case config.WindowRightHalf:
		half := math.Floor(wa.Width / 2)
		return Rect{X: wa.X + half, Y: wa.Y, Width: wa.Width - half, Height: wa.Height}, true, nil
	case config.WindowTopHalf:
		return Rect{X: wa.X, Y: wa.Y, Width: wa.Width, Height: math.Floor(wa.Height / 2)}, true, nil
	case config.WindowBottomHalf:
		half := math.Floor(wa.Height / 2)
		return Rect{X: wa.X, Y: wa.Y + half, Width: wa.Width, Height: wa.Height - half}, true, nil
	case config.WindowMaximize:
		return wa, true, nil
	case config.WindowCenter:
		w := current.Width
		h := current.Height
		if w <= 0 {
			w = math.Round(wa.Width * 0.7)
		}
		if h <= 0 {
			h = math.Round(wa.Height * 0.7)
		}
		w = min(w, wa.Width)
		h = min(h, wa.Height)
		return Rect{
			X:      wa.X + math.Round((wa.Width-w)/2),
			Y:      wa.Y + math.Round((wa.Height-h)/2),
			Width:  w,
			Height: h,
		}, true, nil
	case config.WindowNextDisplay:
		if len(displays) <= 1 {
			return current, false, nil
		}
		nextIdx := (idx + 1) % len(displays)
		return moveRectToDisplay(current, wa, displays[nextIdx].effectiveWorkArea()), true, nil
	case config.WindowPreviousDisplay:
		if len(displays) <= 1 {
			return current, false, nil
		}
		prevIdx := (idx - 1 + len(displays)) % len(displays)
		return moveRectToDisplay(current, wa, displays[prevIdx].effectiveWorkArea()), true, nil
	default:
		return Rect{}, false, fmt.Errorf("window: unknown command %q", cmd)
	}
}

func moveRectToDisplay(current, srcWA, dstWA Rect) Rect {
	if current.IsZero() {
		return dstWA
	}
	// If the window filled the source work area, fill the destination work area.
	if math.Abs(current.Width-srcWA.Width) <= 2 && math.Abs(current.Height-srcWA.Height) <= 2 {
		return dstWA
	}
	w := min(current.Width, dstWA.Width)
	h := min(current.Height, dstWA.Height)

	relX := 0.5
	if srcWA.Width > current.Width {
		relX = (current.X - srcWA.X) / (srcWA.Width - current.Width)
		relX = max(0, min(1, relX))
	}
	relY := 0.5
	if srcWA.Height > current.Height {
		relY = (current.Y - srcWA.Y) / (srcWA.Height - current.Height)
		relY = max(0, min(1, relY))
	}

	x := dstWA.X + math.Round(relX*(dstWA.Width-w))
	y := dstWA.Y + math.Round(relY*(dstWA.Height-h))
	return Rect{X: x, Y: y, Width: w, Height: h}
}

// ---- PasteboardSnapshotRestorer ----

// PasteboardRepresentation holds one data representation for a pasteboard item.
type PasteboardRepresentation struct {
	Type string
	Data []byte
}

// PasteboardItemSnapshot holds all representations for one pasteboard item.
type PasteboardItemSnapshot struct {
	Representations []PasteboardRepresentation
}

// PasteboardSnapshot captures multi-item, multi-format pasteboard contents and its change count.
type PasteboardSnapshot struct {
	ChangeCount int64
	Items       []PasteboardItemSnapshot
}

// Clone returns a deep copy of the snapshot so external slice mutations cannot alter it.
func (s PasteboardSnapshot) Clone() PasteboardSnapshot {
	items := make([]PasteboardItemSnapshot, len(s.Items))
	for i, item := range s.Items {
		reps := make([]PasteboardRepresentation, len(item.Representations))
		for j, rep := range item.Representations {
			copied := make([]byte, len(rep.Data))
			copy(copied, rep.Data)
			reps[j] = PasteboardRepresentation{Type: rep.Type, Data: copied}
		}
		items[i] = PasteboardItemSnapshot{Representations: reps}
	}
	return PasteboardSnapshot{ChangeCount: s.ChangeCount, Items: items}
}

// PasteboardBackend abstracts OS pasteboard operations for PasteboardSnapshotRestorer.
type PasteboardBackend interface {
	ChangeCount() int64
	Capture() (PasteboardSnapshot, error)
	Restore(snapshot PasteboardSnapshot) error
}

// PasteboardSnapshotRestorer preserves and conditionally restores pasteboard snapshots.
type PasteboardSnapshotRestorer struct {
	backend PasteboardBackend
}

// NewPasteboardSnapshotRestorer constructs a PasteboardSnapshotRestorer for backend.
func NewPasteboardSnapshotRestorer(backend PasteboardBackend) *PasteboardSnapshotRestorer {
	return &PasteboardSnapshotRestorer{backend: backend}
}

// Capture reads a deep-copied snapshot from the backend.
func (r *PasteboardSnapshotRestorer) Capture() (PasteboardSnapshot, error) {
	if r.backend == nil {
		return PasteboardSnapshot{}, errors.New("pasteboard: nil backend")
	}
	snap, err := r.backend.Capture()
	if err != nil {
		return PasteboardSnapshot{}, err
	}
	return snap.Clone(), nil
}

// RestoreIfUnmodified restores snapshot only if the current pasteboard changeCount still equals
// expectedChangeCount. If an external app changed the pasteboard in the meantime, it skips
// restoration and returns (false, nil).
func (r *PasteboardSnapshotRestorer) RestoreIfUnmodified(snapshot PasteboardSnapshot, expectedChangeCount int64) (bool, error) {
	if r.backend == nil {
		return false, errors.New("pasteboard: nil backend")
	}
	if r.backend.ChangeCount() != expectedChangeCount {
		return false, nil
	}
	if err := r.backend.Restore(snapshot.Clone()); err != nil {
		return false, err
	}
	return true, nil
}
