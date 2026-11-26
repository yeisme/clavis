//go:build darwin

package bridge

import (
	"context"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/permissionflow"

	"github.com/yeisme/clavis/pkg/core/host"
)

var _ PermissionGuide = (*darwinBridge)(nil)

// GuidePermission opens the Privacy & Security page for kind with
// permissionflow's guide: a panel under System Settings holding Clavis to
// drag into the list, which an app that never asked is missing from. It
// returns when the permission is granted, the user closes the panel or
// System Settings, or ctx is done. Before the app runs (tests) it only opens
// the page.
func (b *darwinBridge) GuidePermission(ctx context.Context, kind string) (host.PermissionStatus, error) {
	p, ok := darwinGuidePermission(kind)
	if !ok || !mygo.App.IsReady() {
		if err := b.OpenPermissionSettings(kind); err != nil {
			return host.PermissionStatus{}, err
		}
		return b.Permissions(context.Background())
	}
	_, err := permissionflow.Guide(ctx, p, permissionflow.GuideOptions{})
	st, permErr := b.Permissions(context.Background())
	if err != nil && ctx.Err() == nil {
		return st, err
	}
	return st, permErr
}

// darwinGuidePermission maps a permission kind to the guide's permission;
// the kinds are those of OpenPermissionSettings.
func darwinGuidePermission(kind string) (permissionflow.Permission, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "accessibility", "ax":
		return permissionflow.Accessibility, true
	case "input_monitoring", "input-monitoring", "inputmonitoring":
		return permissionflow.InputMonitoring, true
	case "screen_recording", "screen-recording", "screencapture":
		return permissionflow.ScreenRecording, true
	}
	return "", false
}
