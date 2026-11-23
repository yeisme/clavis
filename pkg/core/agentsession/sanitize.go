package agentsession

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	codeBlockRE = regexp.MustCompile("(?s)```.*?```")
	pathRE      = regexp.MustCompile(`(?:/[A-Za-z0-9._-]+){2,}`)
	secretRE    = regexp.MustCompile(`(?i)(bearer\s+|sk-[A-Za-z0-9_-]{8,}|token\s*[=:]\s*)[^\s]+`)
)

func sanitizeMessage(s string) string {
	s = codeBlockRE.ReplaceAllString(s, "[code]")
	s = secretRE.ReplaceAllString(s, "[redacted]")
	s = pathRE.ReplaceAllString(s, "[path]")
	s = strings.Join(strings.Fields(s), " ")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	runes := []rune(s)
	if len(runes) > 120 {
		runes = runes[:119]
		s = string(runes) + "…"
	}
	return s
}

// SanitizeDiagnosticText removes secrets, absolute paths, code and verbose
// payloads before diagnostic text crosses into the user-facing UI.
func SanitizeDiagnosticText(s string) string {
	return sanitizeMessage(s)
}

func basename(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(path))
}

func statusFromActivity(now, at time.Time, explicit string, pending bool) Status {
	explicit = strings.ToLower(strings.TrimSpace(explicit))
	switch explicit {
	case "stopped", "completed", "closed", "done":
		return StatusStopped
	case "unknown":
		return StatusUnknown
	}
	if pending {
		return StatusWaiting
	}
	switch explicit {
	case "running", "active", "in_progress":
		return StatusRunning
	case "waiting", "idle", "paused", "pending", "awaiting_human", "needs_confirmation":
		return StatusWaiting
	}
	if at.IsZero() {
		return StatusUnknown
	}
	if at.After(now) {
		return StatusUnknown
	}
	if now.Sub(at) <= 2*time.Minute {
		return StatusRunning
	}
	return StatusWaiting
}
