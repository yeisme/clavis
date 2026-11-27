package bridge

import (
	"context"
	"sync"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// SyntheticEventTag ("CLVS" = 0x434C5653) tags keyboard events synthesized by Clavis
// so global keyboard hooks/taps ignore self-injected keystrokes and avoid feedback loops.
const SyntheticEventTag int64 = 0x434C5653

// HostService extends pkg/core/host.Bridge with lifecycle, permission flows,
// keymap/IME runtime configuration updates, and clipboard monitoring required by the desktop runtime.
type HostService interface {
	host.Bridge
	Start(ctx context.Context, onTrigger func(host.TriggerEvent)) error
	Stop() error
	UpdateKeymapConfig(cfg config.KeymapConfig)
	UpdateIMEConfig(imeCfg config.IMEConfig, rules config.IMERulesConfig)
	SetIMEMode(ctx context.Context, mode config.IMEMode) error
	LockTemporaryASCII(ctx context.Context) (bool, host.InputSourceInfo, error)
	RestoreTemporaryIME(ctx context.Context) (bool, host.InputSourceInfo, error)
	ActiveKeymapLayer() string
	RefreshPermissions(ctx context.Context) (host.PermissionStatus, error)
	OpenPermissionSettings(kind string) error
	PromptPermission(kind string) (host.PermissionStatus, error)
	PollClipboardCandidate(ctx context.Context) (*clipboard.CaptureCandidate, int64, error)
	IgnoreSelfPasteboardChange(changeCount int64)
	TapRecoveryCount() int64
}

// ExtendedHostService contains optional low-latency and process-management capabilities.
// It is intentionally separate so existing embedders of HostService remain source-compatible.
// InlineIMEForcer is the optional capability for platforms whose own input engine can be
// forced to take over Chinese typing while another IME stays active. It is a separate,
// narrow interface on purpose: adding methods to ExtendedHostService silently breaks every
// embedder and mock that satisfies it by assertion.
type InlineIMEForcer interface {
	SetInlineIMEForced(forced bool)
	InlineIMEForced() bool
}

// ExternalAppActivator tracks the most recent non-Clavis foreground application
// and activates a running application by bundle ID (macOS SF-11/SF-13).
type ExternalAppActivator interface {
	LastExternalApp() AppIdentity
	CurrentFrontmostApp() AppIdentity
	ActivateAppByBundleID(ctx context.Context, bundleID string) error
}

type ExtendedHostService interface {
	HostService
	CommitText(ctx context.Context, text string, deleteCount int, cursorBackOffset int) error
	SetInlineIMEHandler(fn func(host.InlineKeyEvent) bool)
	SetInlineIMEEnabled(enabled bool)
	FastCaretAnchor(ctx context.Context) (host.CursorAnchor, error)
	CaptureEditableField(ctx context.Context) (host.EditableFieldSnapshot, error)
	ReplaceEditableField(ctx context.Context, replacement string, onlySelection bool) error
	UpdateDaemonShortcut(inPlaceShortcut string)
	UpdateSelectionBarConfig(cfg config.SelectionBarConfig)
	ListListeningPorts(ctx context.Context) ([]host.ListeningPort, error)
	KillProcess(ctx context.Context, pid int, force bool) error
}

// Bridge aliases HostService for callers referencing bridge.Bridge.
type Bridge = HostService

// Options configures a platform HostService created by NewBridge / NewHostService.
type Options struct {
	TriggerConfig        config.TriggerConfig
	KeymapConfig         config.KeymapConfig
	IMEConfig            config.IMEConfig
	IMERules             config.IMERulesConfig
	OnInputSourceChanged func(host.InputSourceInfo)
	OnKeymapLayerChanged func(layer string)
	// OnIMESwitchFailed reports an IME auto-switch that the platform could not apply, so the UI
	// can surface it instead of failing silently. Currently emitted by the Windows bridge.
	OnIMESwitchFailed func(err error)
}

// Option mutates bridge Options.
type Option func(*Options)

// WithTriggerConfig sets the initial TriggerConfig for global hotkeys and double-tap detection.
func WithTriggerConfig(cfg config.TriggerConfig) Option {
	return func(o *Options) {
		o.TriggerConfig = cfg
	}
}

// WithKeymapConfig sets the initial KeymapConfig for tap/hold remapping and navigation layers.
func WithKeymapConfig(cfg config.KeymapConfig) Option {
	return func(o *Options) {
		o.KeymapConfig = cfg
	}
}

// WithIMEConfig sets the initial IMEConfig and IMERulesConfig for per-app IME auto-switching.
func WithIMEConfig(imeCfg config.IMEConfig, rules config.IMERulesConfig) Option {
	return func(o *Options) {
		o.IMEConfig = imeCfg
		o.IMERules = rules
	}
}

// WithOnInputSourceChanged registers a callback invoked when auto-IME switching changes the input source.
func WithOnInputSourceChanged(fn func(host.InputSourceInfo)) Option {
	return func(o *Options) {
		o.OnInputSourceChanged = fn
	}
}

// WithOnIMESwitchFailed registers a callback for IME switches that could not be applied.
func WithOnIMESwitchFailed(fn func(err error)) Option {
	return func(o *Options) {
		o.OnIMESwitchFailed = fn
	}
}

// WithOnKeymapLayerChanged registers a callback invoked when KeymapLayerEngine enters or exits a hold layer (e.g. "nav", "hyper", "").
func WithOnKeymapLayerChanged(fn func(layer string)) Option {
	return func(o *Options) {
		o.OnKeymapLayerChanged = fn
	}
}

func defaultOptions(opts []Option) Options {
	o := Options{
		TriggerConfig: config.DefaultTriggerConfig(),
		KeymapConfig:  config.DefaultKeymapConfig(),
		IMEConfig:     config.DefaultIMEConfig(),
		IMERules:      config.DefaultIMERulesConfig(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// NewHostService constructs the platform-appropriate HostService implementation.
func NewHostService(opts ...Option) HostService {
	return newPlatformBridge(defaultOptions(opts))
}

// NewBridge is an alias for NewHostService.
func NewBridge(opts ...Option) HostService {
	return NewHostService(opts...)
}

// triggerRouter coordinates DoubleTapDetector, HotkeyMatcher, and snippet key events.
type triggerRouter struct {
	mu                 sync.RWMutex
	cfg                config.TriggerConfig
	leaderDetector     *DoubleTapDetector
	doubleTapDetector  *DoubleTapDetector
	capsuleHotkey      Hotkey
	hasCapsuleHotkey   bool
	clipboardHotkey    Hotkey
	hasClipboardHotkey bool
	imeToggleHotkey    Hotkey
	hasIMEToggleHotkey bool
}

func newTriggerRouter(cfg config.TriggerConfig) *triggerRouter {
	r := &triggerRouter{}
	r.UpdateConfig(cfg)
	return r
}

func (r *triggerRouter) UpdateConfig(cfg config.TriggerConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
	if r.leaderDetector == nil {
		r.leaderDetector = NewDoubleTapDetector(cfg.LeaderKey, cfg.DoubleTapIntervalMs)
	} else {
		r.leaderDetector.SetConfig(cfg.LeaderKey, cfg.DoubleTapIntervalMs)
	}
	if r.doubleTapDetector == nil {
		r.doubleTapDetector = NewDoubleTapDetector(cfg.DoubleTapKey, cfg.DoubleTapIntervalMs)
	} else {
		r.doubleTapDetector.SetConfig(cfg.DoubleTapKey, cfg.DoubleTapIntervalMs)
	}
	if hk, err := ParseHotkey(cfg.CapsuleShortcut); err == nil {
		r.capsuleHotkey = hk
		r.hasCapsuleHotkey = true
	} else {
		r.capsuleHotkey = Hotkey{}
		r.hasCapsuleHotkey = false
	}
	if hk, err := ParseHotkey(cfg.ClipboardShortcut); err == nil {
		r.clipboardHotkey = hk
		r.hasClipboardHotkey = true
	} else {
		r.clipboardHotkey = Hotkey{}
		r.hasClipboardHotkey = false
	}
	if hk, err := ParseHotkey(cfg.InputEngineToggleShortcut); err == nil {
		r.imeToggleHotkey = hk
		r.hasIMEToggleHotkey = true
	} else {
		r.imeToggleHotkey = Hotkey{}
		r.hasIMEToggleHotkey = false
	}
}

func (r *triggerRouter) HandleModifier(mod string, isDown bool, otherModifiersDown bool, nowMs int64) []host.TriggerEvent {
	r.mu.RLock()
	leaderDet := r.leaderDetector
	doubleDet := r.doubleTapDetector
	leaderKey := NormalizeTriggerModifier(r.cfg.LeaderKey)
	doubleKey := NormalizeTriggerModifier(r.cfg.DoubleTapKey)
	r.mu.RUnlock()

	var events []host.TriggerEvent
	if leaderDet.HandleModifier(mod, isDown, otherModifiersDown, nowMs) {
		events = append(events, host.TriggerEvent{
			Kind:        host.TriggerLeaderKey,
			Key:         leaderKey,
			TimestampMs: nowMs,
		})
	}
	if doubleKey != "" && doubleKey != leaderKey {
		if doubleDet.HandleModifier(mod, isDown, otherModifiersDown, nowMs) {
			events = append(events, host.TriggerEvent{
				Kind:        host.TriggerDoubleTap,
				Key:         doubleKey,
				TimestampMs: nowMs,
			})
		}
	} else if !isDown && otherModifiersDown {
		doubleDet.Reset()
	}
	return events
}

func (r *triggerRouter) snippetExpansionEnabled() bool {
	r.mu.RLock()
	enabled := r.cfg.SnippetExpansionEnabled
	r.mu.RUnlock()
	return enabled
}

// HandleKeyDown evaluates a keyDown event against CapsuleShortcut and inline snippet triggers.
// Returns (eventsToEmit, consumeKeyEvent).
func (r *triggerRouter) HandleKeyDown(key string, unicodeStr string, modifiers []string, isBackspace bool, secureInputActive bool, nowMs int64) ([]host.TriggerEvent, bool) {
	return r.HandleKeyDownWithIME(key, unicodeStr, modifiers, isBackspace, secureInputActive, false, nowMs)
}

// HandleKeyDownWithIME evaluates a keyDown event while accounting for active IME preedit/composition
// and cursor/context-resetting navigation keys.
func (r *triggerRouter) HandleKeyDownWithIME(
	key string,
	unicodeStr string,
	modifiers []string,
	isBackspace bool,
	secureInputActive bool,
	imeComposing bool,
	nowMs int64,
) ([]host.TriggerEvent, bool) {
	r.mu.RLock()
	leaderDet := r.leaderDetector
	doubleDet := r.doubleTapDetector
	hk := r.capsuleHotkey
	hasHK := r.hasCapsuleHotkey
	clipHK := r.clipboardHotkey
	hasClipHK := r.hasClipboardHotkey
	imeHK := r.imeToggleHotkey
	hasIMEHK := r.hasIMEToggleHotkey
	snippetEnabled := r.cfg.SnippetExpansionEnabled
	r.mu.RUnlock()

	leaderDet.HandleInterveningKey()
	doubleDet.HandleInterveningKey()

	if hasHK && hk.Matches(key, modifiers) {
		return []host.TriggerEvent{{
			Kind:        host.TriggerCapsuleToggle,
			Key:         hk.Key,
			Modifiers:   append([]string(nil), modifiers...),
			TimestampMs: nowMs,
		}}, true
	}

	if hasClipHK && clipHK.Matches(key, modifiers) {
		return []host.TriggerEvent{{
			Kind:        host.TriggerClipboardToggle,
			Key:         clipHK.Key,
			Modifiers:   append([]string(nil), modifiers...),
			TimestampMs: nowMs,
		}}, true
	}

	// Checked before the snippet branch: the toggle must work even while a secure
	// field is focused, so it never depends on snippet state.
	if hasIMEHK && imeHK.Matches(key, modifiers) {
		return []host.TriggerEvent{{
			Kind:        host.TriggerInputEngineToggle,
			Key:         imeHK.Key,
			Modifiers:   append([]string(nil), modifiers...),
			TimestampMs: nowMs,
		}}, true
	}

	if !snippetEnabled || secureInputActive {
		return nil, false
	}

	// Non-shift shortcut modifiers (Cmd/Ctrl/Alt/Fn) move selection, paste, or switch focus -> reset snippet buffer.
	if hasNonShiftModifier(modifiers) {
		return []host.TriggerEvent{{
			Kind:        host.TriggerSnippetReset,
			Key:         key,
			Modifiers:   append([]string(nil), modifiers...),
			TimestampMs: nowMs,
		}}, false
	}

	// During active IME preedit/composition, raw keystrokes belong to the IME candidate buffer rather than committed text.
	if imeComposing {
		return []host.TriggerEvent{{
			Kind:        host.TriggerSnippetReset,
			Key:         key,
			TimestampMs: nowMs,
		}}, false
	}

	// Backspace rewinds the snippet buffer; keep it ahead of the general
	// context-reset check because NormalizeShortcutKey accepts delete as its
	// compatibility alias.
	if isBackspace {
		return []host.TriggerEvent{{
			Kind:        host.TriggerSnippetBack,
			Key:         "backspace",
			Modifiers:   append([]string(nil), modifiers...),
			TimestampMs: nowMs,
		}}, false
	}

	// Cursor navigation, Enter, Escape, Tab, and ForwardDelete break contiguous typing -> reset snippet buffer.
	if isSnippetContextResetKey(key) {
		return []host.TriggerEvent{{
			Kind:        host.TriggerSnippetReset,
			Key:         key,
			TimestampMs: nowMs,
		}}, false
	}

	if unicodeStr != "" {
		return []host.TriggerEvent{{
			Kind:        host.TriggerSnippetKey,
			Key:         key,
			Rune:        unicodeStr,
			Modifiers:   append([]string(nil), modifiers...),
			TimestampMs: nowMs,
		}}, false
	}

	return nil, false
}

func isSnippetContextResetKey(key string) bool {
	switch NormalizeShortcutKey(key) {
	case "left", "right", "up", "down",
		"home", "end", "pageup", "pagedown",
		"return", "escape", "tab",
		"backspace", "delete", "forwarddelete":
		return true
	default:
		return false
	}
}

func hasNonShiftModifier(modifiers []string) bool {
	for _, m := range modifiers {
		norm, ok := NormalizeShortcutModifier(m)
		if !ok {
			continue
		}
		if norm != "shift" {
			return true
		}
	}
	return false
}

// appIdentityChanged reports whether b differs from a in bundle ID or (normalized) process name.
// App activations invalidate inline snippet buffers and IME restore stacks.
func appIdentityChanged(a, b AppIdentity) bool {
	return a.BundleID != b.BundleID || normalizeProcessName(a.ProcessName) != normalizeProcessName(b.ProcessName)
}

// selfPasteboardTracker filters out self-originated clipboard change counts.
type selfPasteboardTracker struct {
	mu          sync.Mutex
	initialized bool
	lastSeen    int64
	ignored     map[int64]struct{}
}

func newSelfPasteboardTracker() *selfPasteboardTracker {
	return &selfPasteboardTracker{
		ignored: make(map[int64]struct{}),
	}
}

func (t *selfPasteboardTracker) Ignore(changeCount int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ignored[changeCount] = struct{}{}
}

func (t *selfPasteboardTracker) ShouldProcess(changeCount int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.initialized {
		t.initialized = true
		t.lastSeen = changeCount
		delete(t.ignored, changeCount)
		return false
	}

	if changeCount == t.lastSeen {
		return false
	}
	t.lastSeen = changeCount

	if _, ign := t.ignored[changeCount]; ign {
		delete(t.ignored, changeCount)
		return false
	}
	// Prune stale ignored entries older than current changeCount
	for k := range t.ignored {
		if k < changeCount {
			delete(t.ignored, k)
		}
	}
	return true
}

// truncateUTF8 truncates s to at most maxBytes bytes without splitting a multi-byte UTF-8 rune.
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
