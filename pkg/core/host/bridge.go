package host

import (
	"context"

	"github.com/yeisme/clavis/pkg/core/config"
)

// Capabilities describes which OS host features are available on the current platform.
type Capabilities struct {
	Accessibility    bool `json:"accessibility"`
	InputMonitoring  bool `json:"inputMonitoring"`
	GlobalHotkeys    bool `json:"globalHotkeys"`
	WindowManagement bool `json:"windowManagement"`
	IMEControl       bool `json:"imeControl"`
	ClipboardHistory bool `json:"clipboardHistory"`
	Keychain         bool `json:"keychain"`
}

// CursorAnchor represents a screen-coordinate anchor near the text caret or pointer.
type CursorAnchor struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Valid  bool    `json:"valid"`
}

// InputSourceInfo describes an OS keyboard layout / input method source.
type InputSourceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Language string `json:"language,omitempty"`
	IsASCII  bool   `json:"isAscii"`
	// Mode is the live conversion mode of an IME that keeps one input source for both
	// scripts (Windows Microsoft Pinyin 中/英): InputModeNative or InputModeASCII.
	// Empty when the platform reports script by input source alone (macOS).
	Mode string `json:"mode,omitempty"`
}

const (
	InputModeNative = "native"
	InputModeASCII  = "ascii"
)

// ContextSnapshot captures the active frontmost application and text context.
type ContextSnapshot struct {
	BundleID          string           `json:"bundleId,omitempty"`
	ProcessName       string           `json:"processName,omitempty"`
	AppName           string           `json:"appName,omitempty"`
	WindowTitle       string           `json:"windowTitle,omitempty"`
	ActiveURL         string           `json:"activeUrl,omitempty"`
	SelectedText      string           `json:"selectedText,omitempty"`
	SurroundingText   string           `json:"surroundingText,omitempty"`
	ClipboardPreview  string           `json:"clipboardPreview,omitempty"`
	CursorAnchor      *CursorAnchor    `json:"cursorAnchor,omitempty"`
	ActiveInputSource *InputSourceInfo `json:"activeInputSource,omitempty"`
}

// WindowCommand aliases config.WindowCommand for host-layer callers.
type WindowCommand = config.WindowCommand

// PermissionStatus summarizes OS privacy/automation permissions.
type PermissionStatus struct {
	Accessibility   bool              `json:"accessibility"`
	InputMonitoring bool              `json:"inputMonitoring"`
	AppleEvents     bool              `json:"appleEvents"`
	Granted         bool              `json:"granted"`
	Promptable      bool              `json:"promptable"`
	Details         map[string]string `json:"details,omitempty"`
}

// TriggerKind classifies global keyboard trigger events.
type TriggerKind string

const (
	TriggerLeaderKey     TriggerKind = "leader_key"
	TriggerDoubleTap     TriggerKind = "double_tap"
	TriggerCapsuleToggle TriggerKind = "capsule_toggle"
	// TriggerClipboardToggle opens (or closes) Capsule directly on the Clipboard History tab.
	TriggerClipboardToggle TriggerKind = "clipboard_toggle"
	TriggerSnippetKey      TriggerKind = "snippet_key"
	TriggerSnippetBack     TriggerKind = "snippet_backspace"
	TriggerSnippetReset    TriggerKind = "snippet_reset"
)

// TriggerEvent describes a global hotkey or keystroke event captured by the host.
type TriggerEvent struct {
	Kind        TriggerKind      `json:"kind"`
	Key         string           `json:"key,omitempty"`
	Rune        string           `json:"rune,omitempty"`
	Modifiers   []string         `json:"modifiers,omitempty"`
	TimestampMs int64            `json:"timestampMs"`
	Context     *ContextSnapshot `json:"context,omitempty"`
}

// Bridge is the platform-neutral host integration interface implemented per OS.
type Bridge interface {
	Capabilities() Capabilities
	SnapshotContext(ctx context.Context) (ContextSnapshot, error)
	ExecuteWindowCommand(ctx context.Context, cmd WindowCommand) error
	InjectText(ctx context.Context, text string, deleteCount int, cursorBackOffset int) error
	OpenApp(ctx context.Context, target string) error
	InputSources(ctx context.Context) ([]InputSourceInfo, error)
	CurrentInputSource(ctx context.Context) (InputSourceInfo, error)
	SelectInputSource(ctx context.Context, id string) error
	Permissions(ctx context.Context) (PermissionStatus, error)
}
