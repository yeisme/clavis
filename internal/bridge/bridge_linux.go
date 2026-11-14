//go:build !darwin && !windows

package bridge

import (
	"context"
	"errors"
	"os/exec"
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

	pbTracker *selfPasteboardTracker
	lastText  string
	seq       atomic.Int64
	triggers  *triggerRuntime
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

func (b *linuxBridge) PollClipboardCandidate(ctx context.Context) (*clipboard.CaptureCandidate, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if !mygo.App.IsReady() {
		return nil, b.seq.Load(), nil
	}
	text := mygo.Clipboard.ReadText()
	b.mu.Lock()
	changed := text != b.lastText
	if changed {
		b.lastText = text
		b.seq.Add(1)
	}
	seq := b.seq.Load()
	b.mu.Unlock()

	if !changed || !b.pbTracker.ShouldProcess(seq) || text == "" {
		return nil, seq, nil
	}
	return &clipboard.CaptureCandidate{
		Content:   text,
		Timestamp: time.Now().UTC(),
	}, seq, nil
}
