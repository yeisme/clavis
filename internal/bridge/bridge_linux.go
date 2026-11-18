//go:build !darwin && !windows

package bridge

import (
	"context"
	"errors"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

type linuxBridge struct {
	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc

	pbTracker   *selfPasteboardTracker
	lastText    string
	lastFormats string
	seq         atomic.Int64
	triggers    *triggerRuntime
}

func newPlatformBridge(Options) HostService {
	return &linuxBridge{
		pbTracker: newSelfPasteboardTracker(),
		triggers:  newTriggerRuntime(),
	}
}

func (b *linuxBridge) UpdateTriggerConfig(config.TriggerConfig)                {}
func (b *linuxBridge) UpdateKeymapConfig(config.KeymapConfig)                  {}
func (b *linuxBridge) UpdateIMEConfig(config.IMEConfig, config.IMERulesConfig) {}
func (b *linuxBridge) SetIMEMode(ctx context.Context, _ config.IMEMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.ErrUnsupported
}

// LockTemporaryASCII is unsupported on Linux (no IME control backend).
func (b *linuxBridge) LockTemporaryASCII(ctx context.Context) (bool, host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return false, host.InputSourceInfo{}, err
	}
	return false, host.InputSourceInfo{}, errors.ErrUnsupported
}

// RestoreTemporaryIME is unsupported on Linux (no IME control backend).
func (b *linuxBridge) RestoreTemporaryIME(ctx context.Context) (bool, host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return false, host.InputSourceInfo{}, err
	}
	return false, host.InputSourceInfo{}, errors.ErrUnsupported
}

// ActiveKeymapLayer always reports no active layer on Linux (no global key hook).
func (b *linuxBridge) ActiveKeymapLayer() string { return "" }

func (b *linuxBridge) Capabilities() host.Capabilities {
	return host.Capabilities{
		Accessibility:    false,
		InputMonitoring:  false,
		GlobalHotkeys:    false,
		WindowManagement: false,
		IMEControl:       false,
		ClipboardHistory: mygo.App.IsReady(),
		Keychain:         true,
	}
}

func (b *linuxBridge) Permissions(ctx context.Context) (host.PermissionStatus, error) {
	if err := ctx.Err(); err != nil {
		return host.PermissionStatus{}, err
	}
	return host.PermissionStatus{
		Granted:    true,
		Promptable: false,
		Details: map[string]string{
			"platform": "linux",
		},
	}, nil
}

func (b *linuxBridge) RefreshPermissions(ctx context.Context) (host.PermissionStatus, error) {
	return b.Permissions(ctx)
}

func (b *linuxBridge) OpenPermissionSettings(string) error {
	return errors.ErrUnsupported
}

func (b *linuxBridge) PromptPermission(string) (host.PermissionStatus, error) {
	return host.PermissionStatus{}, errors.ErrUnsupported
}

func (b *linuxBridge) Start(ctx context.Context, _ func(host.TriggerEvent)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return errors.New("bridge: already started")
	}
	_, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	b.started = true
	return nil
}

func (b *linuxBridge) Stop() error {
	b.mu.Lock()
	cancel := b.cancel
	b.cancel = nil
	b.started = false
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (b *linuxBridge) SnapshotContext(ctx context.Context) (host.ContextSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return host.ContextSnapshot{}, err
	}
	var snap host.ContextSnapshot
	if mygo.App.IsReady() {
		preview := mygo.Clipboard.ReadText()
		snap.ClipboardPreview = truncateUTF8(preview, 512)
	}
	return snap, nil
}

func (b *linuxBridge) ExecuteWindowCommand(ctx context.Context, _ host.WindowCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.ErrUnsupported
}

func (b *linuxBridge) InjectText(ctx context.Context, _ string, _ int, _ int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.ErrUnsupported
}

// PasteClipboard uses the conventional xdotool backend when available. Unlike
// the clipboard write, sending a shortcut is optional on Linux, so absence of
// a supported backend is reported honestly rather than treated as success.
func (b *linuxBridge) PasteClipboard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path, err := exec.LookPath("xdotool"); err == nil {
		return exec.CommandContext(ctx, path, "key", "ctrl+v").Run()
	}
	if path, err := exec.LookPath("wtype"); err == nil {
		return exec.CommandContext(ctx, path, "-M", "ctrl", "-k", "v", "-m", "ctrl").Run()
	}
	return errors.ErrUnsupported
}

func (b *linuxBridge) OpenApp(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return errors.New("linux: empty target")
	}
	if mygo.App.IsReady() {
		if strings.Contains(trimmed, "://") {
			return mygo.Shell.OpenExternal(trimmed)
		}
		return mygo.Shell.OpenPath(trimmed)
	}
	return exec.CommandContext(ctx, "xdg-open", trimmed).Run()
}

func (b *linuxBridge) InputSources(ctx context.Context) ([]host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.ErrUnsupported
}

func (b *linuxBridge) CurrentInputSource(ctx context.Context) (host.InputSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return host.InputSourceInfo{}, err
	}
	return host.InputSourceInfo{}, errors.ErrUnsupported
}

func (b *linuxBridge) SelectInputSource(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.ErrUnsupported
}

func (b *linuxBridge) IgnoreSelfPasteboardChange(changeCount int64) {
	b.pbTracker.Ignore(changeCount)
}

func (b *linuxBridge) TapRecoveryCount() int64 {
	return 0
}

// ClipboardSequence exposes the bridge-local target signature sequence. Linux has no
// portable native sequence number through MyGo, so the sequence advances only when the
// advertised target set or text snapshot changes.
func (b *linuxBridge) ClipboardSequence() int64 { return b.seq.Load() }

func linuxClipboardHasTextFormat(formats []string) bool {
	for _, format := range formats {
		switch strings.ToLower(strings.TrimSpace(format)) {
		case "text/plain", "utf8_string", "string", "text/plain;charset=utf-8":
			return true
		}
	}
	return false
}

// linuxClipboardKind classifies advertised clipboard formats without requesting their
// payloads. Text deliberately wins: applications commonly advertise both text and a
// preview image, and the existing text capture behavior must remain unchanged.
func linuxClipboardKind(formats []string, text string) clipboard.Kind {
	textAvailable := text != ""
	fileAvailable := false
	imageAvailable := false
	for _, format := range formats {
		format = strings.ToLower(strings.TrimSpace(format))
		switch format {
		case "text/plain", "utf8_string", "string", "text/plain;charset=utf-8":
			textAvailable = textAvailable || text != ""
		case "text/uri-list", "application/x-mygo-file-list", "application/x-gnome-copied-files", "x-special/gnome-copied-files":
			fileAvailable = true
		default:
			if strings.HasPrefix(format, "image/") {
				imageAvailable = true
			}
		}
	}
	if textAvailable {
		return clipboard.KindText
	}
	if fileAvailable {
		return clipboard.KindFile
	}
	if imageAvailable {
		return clipboard.KindImage
	}
	return ""
}

// linuxClipboardFormatSignature is stable for one advertised format set and does not
// materialize clipboard data. Sorting makes target enumeration order irrelevant.
func linuxClipboardFormatSignature(formats []string) string {
	canonical := make([]string, 0, len(formats))
	seen := make(map[string]struct{}, len(formats))
	for _, format := range formats {
		format = strings.ToLower(strings.TrimSpace(format))
		if format == "" {
			continue
		}
		if _, ok := seen[format]; ok {
			continue
		}
		seen[format] = struct{}{}
		canonical = append(canonical, format)
	}
	sort.Strings(canonical)
	return strings.Join(canonical, "\x00")
}

func (b *linuxBridge) PollClipboardCandidate(ctx context.Context) (*clipboard.CaptureCandidate, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if !mygo.App.IsReady() {
		return nil, b.seq.Load(), nil
	}

	// Formats() only asks GTK for TARGETS; unlike Read/ReadFiles/ReadFormat it does
	// not request any representation bytes. Keep this before media enrichment so the
	// privacy filter in the shared poller runs before the payload is read.
	formats := mygo.Clipboard.AvailableFormats()
	// Read text only when a text target is advertised. This preserves text priority
	// while avoiding any representation read for file/image-only clipboard owners.
	text := ""
	if len(formats) == 0 || linuxClipboardHasTextFormat(formats) {
		text = mygo.Clipboard.ReadText()
	}
	kind := linuxClipboardKind(formats, text)
	formatSignature := linuxClipboardFormatSignature(formats)
	b.mu.Lock()
	changed := text != b.lastText || formatSignature != b.lastFormats
	if changed {
		b.lastText = text
		b.lastFormats = formatSignature
		b.seq.Add(1)
	}
	seq := b.seq.Load()
	b.mu.Unlock()

	if !changed || !b.pbTracker.ShouldProcess(seq) {
		return nil, seq, nil
	}
	if kind == "" {
		return nil, seq, nil
	}
	candidate := &clipboard.CaptureCandidate{
		PasteboardTypes: append([]string(nil), formats...),
		Timestamp:       time.Now().UTC(),
	}
	if kind == clipboard.KindText {
		// Preserve the prior behavior exactly: empty text is not a candidate, and
		// only text is read into Content. Media candidates intentionally carry no
		// payload here.
		if text == "" {
			return nil, seq, nil
		}
		candidate.Content = text
	} else {
		candidate.Kind = kind
	}
	return candidate, seq, nil
}
