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
	if helperBinaryPath == "" {
		return "", errors.New("imk: helper path is empty")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	app := filepath.Join(home, "Library", "Input Methods", "ClavisInput.app")
	contents := filepath.Join(app, "Contents")
	if e = os.MkdirAll(filepath.Join(contents, "MacOS"), 0755); e != nil {
		return "", e
	}
	dst := filepath.Join(contents, "MacOS", "clavis-imk")
	data, e := os.ReadFile(helperBinaryPath)
	if e != nil {
		return "", e
	}
	if e = os.WriteFile(dst, data, 0755); e != nil {
		return "", e
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.yeisme.clavis.inputmethod</string><key>CFBundleExecutable</key><string>clavis-imk</string><key>InputMethodConnectionName</key><string>ClavisInput</string><key>InputMethodServerControllerClass</key><string>ClavisIMKController</string><key>tsInputMethodIconFileKey</key><string>icon</string></dict></plist>`
	if e = os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0644); e != nil {
		return "", e
	}
	return app, nil
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
