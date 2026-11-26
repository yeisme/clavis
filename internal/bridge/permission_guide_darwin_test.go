//go:build darwin

package bridge

import (
	"testing"

	"github.com/egoist/mygo/plugins/permissionflow"
)

// TestDarwinGuidePermissionKinds: the guide takes the kinds the settings
// page and the first-run guide pass; others fall back to opening the page.
func TestDarwinGuidePermissionKinds(t *testing.T) {
	for kind, want := range map[string]permissionflow.Permission{
		"accessibility":    permissionflow.Accessibility,
		" AX ":             permissionflow.Accessibility,
		"input_monitoring": permissionflow.InputMonitoring,
		"input-monitoring": permissionflow.InputMonitoring,
		"InputMonitoring":  permissionflow.InputMonitoring,
		"screen_recording": permissionflow.ScreenRecording,
		"screen-recording": permissionflow.ScreenRecording,
	} {
		if got, ok := darwinGuidePermission(kind); !ok || got != want {
			t.Errorf("darwinGuidePermission(%q) = %q, %v; want %q", kind, got, ok, want)
		}
	}
	for _, kind := range []string{"", "automation", "unknown"} {
		if got, ok := darwinGuidePermission(kind); ok {
			t.Errorf("darwinGuidePermission(%q) = %q; want no guide", kind, got)
		}
	}
}
