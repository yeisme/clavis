package bridge

import (
	"context"

	"github.com/yeisme/clavis/pkg/core/host"
)

// PermissionGuide is implemented by bridges that can walk the user through
// granting a permission in the system's settings, beyond opening the page
// (OpenPermissionSettings). GuidePermission returns once kind
// ("accessibility", "input_monitoring") is granted, the user leaves the
// settings, or ctx is done, with the permissions as they are then.
type PermissionGuide interface {
	GuidePermission(ctx context.Context, kind string) (host.PermissionStatus, error)
}
