package diag

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// EnvLogDir overrides the log directory (primarily for isolated tests).
	EnvLogDir = "CLAVIS_LOG_DIR"
	// EnvDataDir overrides the user data directory (primarily for isolated tests).
	EnvDataDir = "CLAVIS_DATA_DIR"

	mainLogFileName        = "clavis.log"
	latestCrashLogFileName = "crash-latest.log"
)

// DefaultLogDir returns the per-machine directory for rolling runtime & crash logs.
func DefaultLogDir() string {
	if override := strings.TrimSpace(os.Getenv(EnvLogDir)); override != "" {
		return filepath.Clean(override)
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			return filepath.Join(home, "Library", "Logs", "Clavis")
		}
	case "windows":
		if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
			return filepath.Join(cacheDir, "Clavis", "Logs")
		}
	default:
		if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
			return filepath.Join(cacheDir, "clavis", "logs")
		}
	}
	if home != "" {
		return filepath.Join(home, ".clavis", "logs")
	}
	return filepath.Join(os.TempDir(), "clavis-logs")
}

// DefaultDataDir returns the per-machine application data directory matching mygo.PathUserData.
func DefaultDataDir() string {
	if override := strings.TrimSpace(os.Getenv(EnvDataDir)); override != "" {
		return filepath.Clean(override)
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" && home != "" {
		return filepath.Join(home, "Library", "Application Support", "Clavis")
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
		return filepath.Join(cfgDir, "Clavis")
	}
	if home != "" {
		return filepath.Join(home, ".local", "share", "Clavis")
	}
	return filepath.Join(os.TempDir(), "clavis-data")
}

// DefaultConfigDir returns Clavis's declarative TOML configuration directory.
func DefaultConfigDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "clavis")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "clavis")
	}
	return ".clavis"
}

// DefaultCacheDir returns the per-machine cache directory for Clavis.
func DefaultCacheDir() string {
	if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
		return filepath.Join(cacheDir, "Clavis")
	}
	return filepath.Join(os.TempDir(), "clavis-cache")
}

// MainLogPath returns the primary log file path under logDir.
func MainLogPath(logDir string) string {
	if strings.TrimSpace(logDir) == "" {
		logDir = DefaultLogDir()
	}
	return filepath.Join(logDir, mainLogFileName)
}

// LatestCrashLogPath returns the crash log file path under logDir.
func LatestCrashLogPath(logDir string) string {
	if strings.TrimSpace(logDir) == "" {
		logDir = DefaultLogDir()
	}
	return filepath.Join(logDir, latestCrashLogFileName)
}

// HasRecentCrash checks strictly read-only whether crash-latest.log exists under logDir.
func HasRecentCrash(logDir string) (string, time.Time, bool) {
	p := LatestCrashLogPath(logDir)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return "", time.Time{}, false
	}
	return p, info.ModTime(), true
}
