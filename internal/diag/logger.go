package diag

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxLogBytes caps a single log file before rolling (5 MiB).
	DefaultMaxLogBytes int64 = 5 * 1024 * 1024
	// DefaultMaxLogBackups keeps the 3 most recent rotated log files.
	DefaultMaxLogBackups = 3
)

// RollingLogger writes scrubbed timestamped log entries to clavis.log with bounded size rotation.
type RollingLogger struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
	closed     bool
}

// NewRollingLogger opens or creates MainLogPath(logDir) with owner-only permissions.
func NewRollingLogger(logDir string, maxBytes int64, maxBackups int) (*RollingLogger, error) {
	if strings.TrimSpace(logDir) == "" {
		logDir = DefaultLogDir()
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLogBytes
	}
	if maxBackups <= 0 {
		maxBackups = DefaultMaxLogBackups
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	p := MainLogPath(logDir)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	var sz int64
	if info, statErr := f.Stat(); statErr == nil {
		sz = info.Size()
	}
	return &RollingLogger{
		path:       p,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
		file:       f,
		size:       sz,
	}, nil
}

// Path returns the active clavis.log path.
func (l *RollingLogger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Logf formats, scrubs secrets and user home paths, and appends one log line.
func (l *RollingLogger) Logf(level, format string, args ...any) error {
	if l == nil {
		return nil
	}
	msg := fmt.Sprintf(format, args...)
	msg = RedactPaths(RedactText(msg))
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().UTC().Format(time.RFC3339Nano), strings.ToUpper(strings.TrimSpace(level)), msg)

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return nil
	}
	if l.size+int64(len(line)) > l.maxBytes && l.size > 0 {
		if err := l.rotateLocked(); err != nil {
			return err
		}
	}
	n, err := l.file.WriteString(line)
	l.size += int64(n)
	return err
}

func (l *RollingLogger) rotateLocked() error {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	oldest := fmt.Sprintf("%s.%d", l.path, l.maxBackups)
	_ = os.Remove(oldest)
	for i := l.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", l.path, i)
		dst := fmt.Sprintf("%s.%d", l.path, i+1)
		_ = os.Rename(src, dst)
	}
	_ = os.Rename(l.path, l.path+".1")
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	l.file = f
	l.size = 0
	return nil
}

// Close closes the underlying log file.
func (l *RollingLogger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// RecordPanic atomically writes a scrubbed crash report to crash-latest.log.
func RecordPanic(logDir, version, component string, recovered any, stack []byte) (string, error) {
	if strings.TrimSpace(logDir) == "" {
		logDir = DefaultLogDir()
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return "", err
	}
	if len(stack) == 0 {
		stack = debug.Stack()
	}
	if strings.TrimSpace(version) == "" {
		version = "1.0.0"
	}
	if strings.TrimSpace(component) == "" {
		component = "app"
	}
	panicStr := RedactPaths(RedactText(fmt.Sprintf("%v", recovered)))
	stackStr := RedactPaths(RedactText(string(stack)))

	var b strings.Builder
	fmt.Fprintf(&b, "timestamp: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "version: %s\n", version)
	fmt.Fprintf(&b, "os: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "go: %s\n", runtime.Version())
	fmt.Fprintf(&b, "component: %s\n", component)
	fmt.Fprintf(&b, "panic: %s\n\n%s\n", panicStr, stackStr)

	dst := LatestCrashLogPath(logDir)
	if err := writeFileAtomic(dst, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

// RecoverPanic should be called with defer to persist unexpected panics cleanly.
func RecoverPanic(logDir, version, component string, onRecovered func(crashPath string)) {
	r := recover()
	if r == nil {
		return
	}
	p, _ := RecordPanic(logDir, version, component, r, debug.Stack())
	if onRecovered != nil {
		onRecovered(p)
	}
}

// ReadTailLines returns up to the last maxLines lines from path with secrets and
// user home paths redacted. Missing files return (nil, nil).
func ReadTailLines(path string, maxLines int) ([]string, error) {
	if maxLines <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxReadBytes int64 = 128 * 1024
	offset := int64(0)
	if info.Size() > maxReadBytes {
		offset = info.Size() - maxReadBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	text := string(raw)
	if offset > 0 {
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[idx+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = RedactPaths(RedactText(line))
	}
	return out, nil
}

func writeFileAtomic(dst string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
