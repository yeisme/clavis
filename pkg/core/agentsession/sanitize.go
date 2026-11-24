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
	secretRE    = regexp.MustCompile(`(?i)(bearer\s+|sk-[A-Za-z0-9_-]{8,}|gh[pous]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[bp]-[A-Za-z0-9_-]{8,}|(?:api_?key|secret_?key|access_?token|password|passwd|token)\s*[=:]\s*)[^\s"']+`)
	ansiCSI     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	ansiOSC     = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiSingle  = regexp.MustCompile(`\x1b[@-Z\\-_]`)
)

// SanitizeMultilineOutput strips ANSI escapes and redacts credentials and
// absolute paths while preserving multiline Markdown formatting (headings,
// lists, code fences, and paragraphs) within a bounded line/byte budget.
func SanitizeMultilineOutput(s string, maxLines, maxBytes int) (string, bool) {
	if s == "" {
		return "", false
	}
	if maxLines <= 0 {
		maxLines = 180
	}
	if maxBytes <= 0 {
		maxBytes = 20 << 10
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = ansiOSC.ReplaceAllString(s, "")
	s = ansiCSI.ReplaceAllString(s, "")
	s = ansiSingle.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = secretRE.ReplaceAllString(s, "[redacted]")
	s = pathRE.ReplaceAllString(s, "[path]")

	truncated := false
	if len(s) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
		truncated = true
	}

	rawLines := strings.Split(s, "\n")
	if len(rawLines) > maxLines {
		rawLines = rawLines[len(rawLines)-maxLines:]
		truncated = true
	}
	// Trim leading/trailing completely blank lines without destroying inner structure.
	for len(rawLines) > 0 && strings.TrimSpace(rawLines[0]) == "" {
		rawLines = rawLines[1:]
	}
	for len(rawLines) > 0 && strings.TrimSpace(rawLines[len(rawLines)-1]) == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	out := strings.Join(rawLines, "\n")
	if strings.Count(out, "```")%2 != 0 {
		out += "\n```"
	}
	return out, truncated
}

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
