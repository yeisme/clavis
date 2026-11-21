package uiapp

import (
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// clipboardClock is "now" for the relative copy times on clipboard rows;
// tests and snapshots pin it so the meta line is deterministic.
var clipboardClock = time.Now

// clipboardRowMeta is the secondary line of a clipboard row, joined with
// " · ": the source app (omitted when unknown — "Clipboard" there was noise
// in the clipboard tab), how long ago it was copied, and for multi-line
// text the line count, because the title shows only the first line and a
// long block otherwise looks like a one-liner.
func clipboardRowMeta(item engine.OmniboxItem, now time.Time) string {
	parts := make([]string, 0, 3)
	if app := strings.TrimSpace(item.ClipboardSourceApp); app != "" {
		parts = append(parts, app)
	}
	if !item.ClipboardCopiedAt.IsZero() {
		parts = append(parts, relativeCopyTime(item.ClipboardCopiedAt, now))
	}
	if item.ClipboardMedia == nil {
		if n := clipboardLineCount(item.ClipboardContent); n > 1 {
			parts = append(parts, i18n.T("clipboard.row.lines", n))
		}
	}
	if len(parts) == 0 {
		return item.Subtitle
	}
	return strings.Join(parts, " · ")
}

// clipboardLineCount counts the lines of a text entry, ignoring leading
// and trailing blank lines (a copied line usually ends with a newline).
func clipboardLineCount(content string) int {
	s := strings.Trim(content, " \t\r\n")
	if s == "" {
		return 0
	}
	// Every CRLF holds exactly one LF, so counting LFs needs no normalized
	// copy of the entry (this runs per visible row per frame).
	return strings.Count(s, "\n") + 1
}

// relativeCopyTime says when an entry was copied the way a person would:
// just now, N minutes/hours ago, yesterday (calendar day, local time),
// N days ago within a week, then the date.
func relativeCopyTime(at, now time.Time) string {
	at, now = at.In(time.Local), now.In(time.Local)
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return i18n.T("clipboard.row.just_now")
	case d < time.Hour:
		return i18n.T("clipboard.row.minutes_ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return i18n.T("clipboard.row.hours_ago", int(d/time.Hour))
	}
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local) }
	days := int(day(now).Sub(day(at)).Hours()/24 + 0.5)
	switch {
	case days <= 1:
		return i18n.T("clipboard.row.yesterday")
	case days < 7:
		return i18n.T("clipboard.row.days_ago", days)
	case at.Year() == now.Year():
		return i18n.T("clipboard.row.date", int(at.Month()), at.Day())
	default:
		return i18n.T("clipboard.row.date_year", at.Year(), int(at.Month()), at.Day())
	}
}

// clipboardContentIcon picks the leading icon for a text clipboard entry
// from what the text looks like, so a list of history reads at a glance:
// links, e-mail addresses, file paths, shell commands, code, or prose.
// fallback is returned for empty content.
func clipboardContentIcon(content, fallback string) string {
	s := strings.TrimSpace(content)
	if s == "" {
		if fallback != "" {
			return fallback
		}
		return "doc.on.clipboard"
	}
	// Classify from the head only: this runs for every visible row on every
	// frame, and a 256 KiB log used to be lowercased whole and scanned once per
	// code marker each time. Links, addresses, paths and commands are short
	// single lines, so anything longer than the head is none of them; code
	// shows itself in its first lines.
	head, truncated := s, len(s) > clipboardSniffBytes
	if truncated {
		head = s[:clipboardSniffBytes]
	}
	lower := strings.ToLower(head)
	singleLine := !truncated && !strings.ContainsAny(head, "\r\n")
	switch {
	case singleLine && (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.")):
		return "link"
	case singleLine && looksLikeEmail(head):
		return "envelope"
	case singleLine && looksLikePath(head):
		return "folder"
	case singleLine && looksLikeShellCommand(lower):
		return "terminal"
	case looksLikeCode(head):
		return "curlybraces"
	default:
		return "doc.text"
	}
}

// clipboardSniffBytes bounds how much of an entry the per-row classifiers read.
const clipboardSniffBytes = 2048

func looksLikeEmail(s string) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-3 && strings.Contains(s[at+1:], ".") && strings.Count(s, "@") == 1
}

func looksLikePath(s string) bool {
	if strings.ContainsAny(s, "\t") || strings.Count(s, " ") > 2 {
		return false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `\\`) {
		return strings.Count(s, "/") >= 2 || strings.Count(s, `\`) >= 2
	}
	// Windows drive paths: C:\Users\...
	return len(s) > 3 && unicode.IsLetter(rune(s[0])) && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

var shellCommandWords = map[string]bool{
	"git": true, "go": true, "npm": true, "npx": true, "pnpm": true, "yarn": true, "bun": true,
	"cd": true, "ls": true, "cat": true, "rm": true, "mv": true, "cp": true, "mkdir": true,
	"docker": true, "kubectl": true, "brew": true, "curl": true, "wget": true, "ssh": true,
	"scp": true, "make": true, "python": true, "python3": true, "pip": true, "pip3": true,
	"cargo": true, "sudo": true, "chmod": true, "export": true, "echo": true, "grep": true,
	"rg": true, "find": true, "tar": true, "node": true, "deno": true, "winget": true, "choco": true,
}

func looksLikeShellCommand(lower string) bool {
	trimmed := strings.TrimPrefix(lower, "$ ")
	first, _, _ := strings.Cut(trimmed, " ")
	return shellCommandWords[first] && strings.Contains(trimmed, " ")
}

func looksLikeCode(s string) bool {
	markers := []string{"{\n", "};", "=>", "func ", "function ", "def ", "class ", "import ", "package ", "const ", "</", "#include", "SELECT ", "select ", "INSERT ", "UPDATE "}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	// Indented multi-line blocks read as code too.
	return strings.Contains(s, "\n\t") || strings.Contains(s, "\n    ")
}

// parseHexSwatch reports the color of a clipboard entry that is exactly one
// CSS hex color ("#3B82F6", "#fff", "#3b82f680"), so the row can show the
// color itself instead of a generic icon.
func parseHexSwatch(content string) (ui.Color, bool) {
	s := strings.TrimSpace(content)
	if len(s) < 4 || s[0] != '#' {
		return ui.Color{}, false
	}
	switch len(s) - 1 {
	case 3, 4, 6, 8:
	default:
		return ui.Color{}, false
	}
	for _, r := range s[1:] {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return ui.Color{}, false
		}
	}
	return ui.Hex(s), true
}
