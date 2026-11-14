package uiapp

import (
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

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
	lower := strings.ToLower(s)
	singleLine := !strings.ContainsAny(s, "\r\n")
	switch {
	case singleLine && (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.")):
		return "link"
	case singleLine && looksLikeEmail(s):
		return "envelope"
	case singleLine && looksLikePath(s):
		return "folder"
	case singleLine && looksLikeShellCommand(lower):
		return "terminal"
	case looksLikeCode(s):
		return "curlybraces"
	default:
		return "doc.text"
	}
}

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
