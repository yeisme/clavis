package host_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestOperationResultUsesStableJSONFieldNames(t *testing.T) {
	encoded, err := json.Marshal(host.OperationResult{State: host.StateUnknown, Code: "state_unavailable", Retryable: false})
	if err != nil {
		t.Fatalf("marshal operation result: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode operation result JSON: %v", err)
	}
	if got["state"] != "unknown" || got["code"] != "state_unavailable" || got["retryable"] != false {
		t.Fatalf("operation result JSON=%s", encoded)
	}
	if _, ok := got["State"]; ok {
		t.Fatalf("operation result used Go field names: %s", encoded)
	}
}

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
