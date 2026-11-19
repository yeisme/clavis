package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yeisme/clavis/pkg/core/host"
)

func InstallMacOSInputMethod(ctx context.Context, helperBinaryPath string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// There is no implemented InputMethodKit host/controller to install. Do
	// not create a misleading app bundle or plist that macOS cannot load.
	return "", errors.New("IMK host is not implemented")
}
func IMKInstallStatus() host.IMKStatus {
	if runtime.GOOS != "darwin" {
		return host.IMKStatus{}
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return host.IMKStatus{Error: e.Error()}
	}
	p := filepath.Join(home, "Library", "Input Methods", "ClavisInput.app")
	if _, e = os.Stat(p); e != nil {
		return host.IMKStatus{Path: p}
	}
	return host.IMKStatus{Installed: true, Path: p}
}
