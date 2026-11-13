package host_test

import (
	"context"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

type stubBridge struct {
	caps host.Capabilities
}

func (s stubBridge) Capabilities() host.Capabilities { return s.caps }
func (stubBridge) SnapshotContext(context.Context) (host.ContextSnapshot, error) {
	return host.ContextSnapshot{
		BundleID: "com.apple.Notes",
		AppName:  "Notes",
		CursorAnchor: &host.CursorAnchor{
			X: 100, Y: 200, Width: 2, Height: 18, Valid: true,
		},
	}, nil
}
func (stubBridge) ExecuteWindowCommand(context.Context, host.WindowCommand) error { return nil }
func (stubBridge) InjectText(context.Context, string, int, int) error             { return nil }
func (stubBridge) OpenApp(context.Context, string) error                          { return nil }
func (stubBridge) InputSources(context.Context) ([]host.InputSourceInfo, error) {
	return []host.InputSourceInfo{{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true}}, nil
}
func (stubBridge) CurrentInputSource(context.Context) (host.InputSourceInfo, error) {
	return host.InputSourceInfo{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true}, nil
}
func (stubBridge) SelectInputSource(context.Context, string) error { return nil }
func (stubBridge) Permissions(context.Context) (host.PermissionStatus, error) {
	return host.PermissionStatus{Accessibility: true, InputMonitoring: true, Granted: true}, nil
}

func TestBridgeInterfaceSatisfaction(t *testing.T) {
	var b host.Bridge = stubBridge{
		caps: host.Capabilities{
			Accessibility:    true,
			InputMonitoring:  true,
			GlobalHotkeys:    true,
			WindowManagement: true,
			IMEControl:       true,
			ClipboardHistory: true,
			Keychain:         true,
		},
	}
	if !b.Capabilities().WindowManagement {
		t.Fatal("expected WindowManagement capability")
	}
	snap, err := b.SnapshotContext(context.Background())
	if err != nil || snap.AppName != "Notes" || !snap.CursorAnchor.Valid {
		t.Fatalf("unexpected snapshot: %+v (%v)", snap, err)
	}
	if err := b.ExecuteWindowCommand(context.Background(), config.WindowMaximize); err != nil {
		t.Fatalf("unexpected window command error: %v", err)
	}
}
