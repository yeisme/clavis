package bridge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LaunchAgentLabel is the launchd job that keeps Clavis alive.
const LaunchAgentLabel = "com.yeisme.clavis"

// launchAgentPlist renders the job. KeepAlive uses SuccessfulExit=false, which means launchd
// restarts the app only when it died badly. A deliberate Quit therefore stays quit — an input
// method that resurrects itself after "Quit" would be hostile.
func launchAgentPlist(execPath, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key><false/>
  </dict>
  <key>ProcessType</key><string>Interactive</string>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardErrorPath</key><string>%s</string>
  <key>StandardOutPath</key><string>%s</string>
</dict>
</plist>
`, LaunchAgentLabel, execPath, logPath, logPath)
}

// LaunchAgentDir is ~/Library/LaunchAgents.
func LaunchAgentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("bridge: cannot resolve home directory")
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// LaunchAgentPath is the plist path for the current user.
func LaunchAgentPath() (string, error) {
	dir, err := LaunchAgentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, LaunchAgentLabel+".plist"), nil
}

// InstallLaunchAgent writes the keep-alive job and loads it.
//
// Why this exists: an input method that macOS may terminate at will is not an input method.
// Without a keeper the process disappears (TAL treats an accessory app with no windows as
// idle), and the user silently falls back to the system IME — which looks exactly like "the
// candidate window turned into a multi-line list".
func InstallLaunchAgent(execPath string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.ErrUnsupported
	}
	execPath = strings.TrimSpace(execPath)
	if execPath == "" {
		return "", errors.New("bridge: empty executable path")
	}
	dir, err := LaunchAgentDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	logPath := filepath.Join(os.TempDir(), LaunchAgentLabel+".log")
	path, err := LaunchAgentPath()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(launchAgentPlist(execPath, logPath)), 0o644); err != nil {
		return "", err
	}
	// bootout first so a changed plist takes effect; ignore "not loaded".
	_ = runLaunchctl("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchAgentLabel))
	if err := runLaunchctl("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path); err != nil {
		return path, err
	}
	return path, nil
}

// UninstallLaunchAgent removes the job and its plist.
func UninstallLaunchAgent() error {
	if runtime.GOOS != "darwin" {
		return errors.ErrUnsupported
	}
	_ = runLaunchctl("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchAgentLabel))
	path, err := LaunchAgentPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// LaunchAgentInstalled reports whether the plist exists.
func LaunchAgentInstalled() bool {
	path, err := LaunchAgentPath()
	if err != nil {
		return false
	}
	_, statErr := os.Stat(path)
	return statErr == nil
}

func runLaunchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
