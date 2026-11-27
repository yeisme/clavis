package uiapp

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	case singleLine && (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.") || looksLikeDomain(head)):
		return "link"
	case singleLine && isColorText(head):
		return "eyedropper"
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

func looksLikeDomain(s string) bool {
	if strings.ContainsAny(s, " 	/@") || !strings.Contains(s, ".") {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
				return false
			}
		}
	}
	return len(parts[len(parts)-1]) >= 2
}

func isColorText(s string) bool {
	_, ok := parseHexSwatch(s)
	return ok
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

// collapseClipboardLine flattens a bounded prefix (<= clipboardSniffBytes) onto one
// dense display line, replacing newlines with the visible " ↵ " marker and collapsing
// runs of whitespace so 256 KiB clipboard blobs never allocate linearly per frame.
func collapseClipboardLine(content string) string {
	if content == "" {
		return ""
	}
	if len(content) > clipboardSniffBytes {
		content = content[:clipboardSniffBytes]
		for len(content) > 0 && !utf8.ValidString(content) {
			content = content[:len(content)-1]
		}
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(min(len(trimmed), 256))
	pendingSpace := false
	pendingNL := false
	for _, r := range trimmed {
		switch r {
		case '\r':
			continue
		case '\n':
			pendingNL = true
			pendingSpace = false
		case ' ', '\t':
			if !pendingNL {
				pendingSpace = true
			}
		default:
			if pendingNL {
				if b.Len() > 0 {
					b.WriteString(" ↵ ")
				}
				pendingNL = false
				pendingSpace = false
			} else if pendingSpace {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				pendingSpace = false
			}
			b.WriteRune(r)
			if b.Len() >= clipboardSniffBytes {
				return b.String()
			}
		}
	}
	return b.String()
}

// clipboardCompactTitle produces a single-line title with semantic middle truncation:
// - active search match past rune 40 centers a window around the hit;
// - HTTP URLs keep scheme+host + "…" + tail path;
// - file paths keep root prefix + "…" + filename;
// - unbroken tokens >= 36 runes keep head 18 + "…" + tail 12;
// - general text truncates cleanly at maxRunes.
func clipboardCompactTitle(content, query string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 64
	}
	collapsed := collapseClipboardLine(content)
	if collapsed == "" {
		return ""
	}
	runes := []rune(collapsed)
	n := len(runes)
	if n <= maxRunes {
		return collapsed
	}

	// Center around deep search match (> rune 40).
	if q := strings.TrimSpace(query); q != "" {
		parsed := engine.ParseClipboardQuery(q, time.Time{})
		needle := strings.ToLower(strings.TrimSpace(parsed.FreeText))
		if needle == "" && len(parsed.ExactPhrases) > 0 {
			needle = strings.ToLower(parsed.ExactPhrases[0])
		}
		if needle != "" {
			if byteIdx := strings.Index(strings.ToLower(collapsed), needle); byteIdx > 0 {
				runeHit := utf8.RuneCountInString(collapsed[:byteIdx])
				if runeHit > 40 {
					half := maxRunes / 2
					start := max(0, runeHit-half/2)
					end := min(n, start+maxRunes-2)
					if end-start < maxRunes-2 {
						start = max(0, end-(maxRunes-2))
					}
					prefix := ""
					if start > 0 {
						prefix = "…"
					}
					suffix := ""
					if end < n {
						suffix = "…"
					}
					return prefix + string(runes[start:end]) + suffix
				}
			}
		}
	}

	// Semantic middle truncation for URLs.
	lower := strings.ToLower(collapsed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if schemeEnd := strings.Index(collapsed, "://"); schemeEnd >= 0 {
			rest := collapsed[schemeEnd+3:]
			if slash := strings.IndexByte(rest, '/'); slash >= 0 {
				hostEnd := utf8.RuneCountInString(collapsed[:schemeEnd+3+slash])
				headLen := min(hostEnd+1, maxRunes-16)
				tailLen := max(12, maxRunes-headLen-1)
				if headLen >= 10 && headLen+tailLen+1 < n {
					return string(runes[:headLen]) + "…" + string(runes[n-tailLen:])
				}
			}
		}
	}

	// Semantic middle truncation for file paths.
	if looksLikePath(collapsed) {
		headLen := min(18, maxRunes/2-4)
		tailLen := max(14, maxRunes-headLen-1)
		if headLen >= 8 && headLen+tailLen+1 < n {
			return string(runes[:headLen]) + "…" + string(runes[n-tailLen:])
		}
	}

	// Long unbroken single token (e.g. JWT, base64, SHA digest).
	if !strings.ContainsRune(collapsed, ' ') && n >= 36 {
		headLen := min(20, maxRunes-13)
		tailLen := 12
		if headLen >= 8 && headLen+tailLen+1 < n {
			return string(runes[:headLen]) + "…" + string(runes[n-tailLen:])
		}
	}

	return string(runes[:maxRunes-1]) + "…"
}

func clipboardCompactRightMeta(item engine.OmniboxItem, now time.Time) string {
	if item.ClipboardMedia != nil && item.ClipboardMedia.IsMedia() {
		var parts []string
		if item.ClipboardMedia.Width > 0 && item.ClipboardMedia.Height > 0 {
			parts = append(parts, fmt.Sprintf("%d×%d", item.ClipboardMedia.Width, item.ClipboardMedia.Height))
		}
		if item.ClipboardMedia.ByteSize > 0 {
			parts = append(parts, formatClipboardBytes(item.ClipboardMedia.ByteSize))
		}
		if len(parts) > 0 {
			return strings.Join(parts, " · ")
		}
	}
	var parts [2]string
	p := parts[:0]
	if app := strings.TrimSpace(item.ClipboardSourceApp); app != "" {
		p = append(p, app)
	}
	if !item.ClipboardCopiedAt.IsZero() {
		p = append(p, relativeCopyTime(item.ClipboardCopiedAt, now))
	}
	if len(p) > 0 {
		return strings.Join(p, " · ")
	}
	return ""
}
