package inputengine

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Async AI candidates (design §5): UI layer waits AIDebounce after the last raw change, then
// asks the active [ai] profile with AITimeout; any failure degrades silently to local results.

const (
	AIDebounce      = 250 * time.Millisecond
	AITimeout       = 1500 * time.Millisecond
	MaxAICandidates = 5
	minAIRawLen     = 2
)

// ShouldQueryAI reports whether raw is worth a remote suggestion (≥2 letters, no stray chars).
func ShouldQueryAI(raw string) bool {
	letters := strings.ReplaceAll(raw, "'", "")
	if len(letters) < minAIRawLen || len(letters) > maxRawLen {
		return false
	}
	for _, r := range letters {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// AIPrompt builds the request. Only the pinyin and the app name are sent — never clipboard or
// surrounding text.
func AIPrompt(raw, appName string) string {
	var b strings.Builder
	b.WriteString("You are a Chinese pinyin input method. Convert the pinyin into the most likely Simplified Chinese words or phrases.\n")
	b.WriteString("Reply with at most 5 candidates, one per line, Chinese text only, no numbering, no pinyin, no explanation.\n")
	if appName = strings.TrimSpace(appName); appName != "" {
		b.WriteString("Application: ")
		b.WriteString(truncate(appName, 64))
		b.WriteString("\n")
	}
	b.WriteString("Pinyin: ")
	b.WriteString(raw)
	return b.String()
}

// ParseAICandidates extracts up to MaxAICandidates clean candidates from a model reply,
// stripping numbering/bullets/quotes and rejecting lines without Han characters.
func ParseAICandidates(reply string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.FieldsFunc(reply, func(r rune) bool { return r == '\n' || r == '、' || r == ',' || r == '，' || r == ';' }) {
		line = strings.TrimSpace(line)
		line = strings.TrimLeftFunc(line, func(r rune) bool {
			return unicode.IsDigit(r) || unicode.IsSpace(r) || strings.ContainsRune(".)-*•:：。．）", r)
		})
		line = strings.Trim(line, " \t\"'`“”‘’「」")
		if line == "" || utf8.RuneCountInString(line) > 16 || seen[line] || !hasHan(line) {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if len(out) == MaxAICandidates {
			break
		}
	}
	return out
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
