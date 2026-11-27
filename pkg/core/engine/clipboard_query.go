package engine

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const clipboardQuerySniffBytes = 2048

// ClipboardQuery represents a parsed structured clipboard search query.
type ClipboardQuery struct {
	Raw          string
	Types        []string
	Apps         []string
	PinnedFilter *bool
	SinceTime    time.Time
	ExactPhrases []string
	Regexes      []*regexp.Regexp
	RegexErr     string
	Terms        []string
	FreeText     string
}

// HasFilters reports whether any structured predicate or regex/phrase constraint is present.
func (q ClipboardQuery) HasFilters() bool {
	return len(q.Types) > 0 ||
		len(q.Apps) > 0 ||
		q.PinnedFilter != nil ||
		!q.SinceTime.IsZero() ||
		len(q.ExactPhrases) > 0 ||
		len(q.Regexes) > 0 ||
		q.RegexErr != ""
}

// ParseClipboardQuery parses Maccy/Raycast-style tokens from a clipboard search box.
// Supported tokens:
//   - type:<kind> / kind:<kind> (text, image, file, link, color, code, video, audio, document, archive)
//   - app:<name> (including app:"Google Chrome")
//   - is:pinned / is:unpinned / pin:true / pin:false
//   - today / since:<N><m|h|d|w>
//   - "exact phrase"
//   - /regex/ (RE2 compiled without panicking; invalid syntax sets RegexErr)
func ParseClipboardQuery(raw string, now time.Time) ClipboardQuery {
	if now.IsZero() {
		now = time.Now()
	}
	q := ClipboardQuery{Raw: strings.TrimSpace(raw)}
	if q.Raw == "" {
		return q
	}

	tokens := splitClipboardQueryTokens(q.Raw)
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		// Exact phrase: "..."
		if len(tok) >= 2 && strings.HasPrefix(tok, `"`) && strings.HasSuffix(tok, `"`) {
			inner := tok[1 : len(tok)-1]
			if inner != "" {
				q.ExactPhrases = append(q.ExactPhrases, inner)
			}
			continue
		}
		// Regex token: /pattern/ (or malformed /pattern[ without closing slash)
		if pat, isRegex := parseRegexToken(tok); isRegex {
			re, err := regexp.Compile("(?i)" + pat)
			if err != nil {
				q.RegexErr = err.Error()
			} else {
				q.Regexes = append(q.Regexes, re)
			}
			continue
		}
		lower := strings.ToLower(tok)
		if lower == "today" {
			loc := now.In(time.Local)
			q.SinceTime = time.Date(loc.Year(), loc.Month(), loc.Day(), 0, 0, 0, 0, time.Local)
			continue
		}
		if key, val, ok := strings.Cut(tok, ":"); ok {
			lkey := strings.ToLower(strings.TrimSpace(key))
			val = strings.TrimSpace(val)
			if len(val) >= 2 && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
				val = val[1 : len(val)-1]
			}
			switch lkey {
			case "type", "kind":
				if norm := normalizeClipboardTypeToken(val); norm != "" {
					q.Types = append(q.Types, norm)
					continue
				}
			case "app":
				if val != "" {
					q.Apps = append(q.Apps, strings.ToLower(val))
					continue
				}
			case "is", "pin", "pinned":
				switch strings.ToLower(val) {
				case "pinned", "true", "1", "yes":
					v := true
					q.PinnedFilter = &v
					continue
				case "unpinned", "false", "0", "no":
					v := false
					q.PinnedFilter = &v
					continue
				}
			case "since":
				if t, parsed := parseSinceDuration(val, now); parsed {
					q.SinceTime = t
					continue
				}
			}
		}
		q.Terms = append(q.Terms, tok)
	}
	q.FreeText = strings.Join(q.Terms, " ")
	return q
}

func splitClipboardQueryTokens(s string) []string {
	var tokens []string
	var buf strings.Builder
	inQuote := false
	inRegex := false
	escaped := false

	flush := func() {
		if buf.Len() > 0 {
			tokens = append(tokens, buf.String())
			buf.Reset()
		}
	}

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			buf.WriteRune(r)
			escaped = false
			continue
		}
		if inRegex {
			if r == '\\' {
				buf.WriteRune(r)
				escaped = true
				continue
			}
			buf.WriteRune(r)
			if r == '/' {
				inRegex = false
				flush()
			}
			continue
		}
		if inQuote {
			if r == '"' {
				buf.WriteRune(r)
				inQuote = false
				continue
			}
			buf.WriteRune(r)
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		if r == '"' {
			inQuote = true
			buf.WriteRune(r)
			continue
		}
		if r == '/' && buf.Len() == 0 {
			// Check whether this slash starts a regex token (/.../ or /[unclosed...) vs plain path (/Users/foo/bar).
			rest := string(runes[i+1:])
			hasClosing := hasUnescapedSlash(rest)
			looksLikeRegexSyntax := strings.ContainsAny(rest, "[(*+?^$\\")
			looksLikePath := strings.HasPrefix(strings.ToLower(rest), "users/") ||
				strings.HasPrefix(strings.ToLower(rest), "tmp/") ||
				strings.HasPrefix(strings.ToLower(rest), "var/") ||
				strings.HasPrefix(strings.ToLower(rest), "etc/") ||
				strings.HasPrefix(strings.ToLower(rest), "library/") ||
				strings.Count(rest, "/") > 1
			if (hasClosing && !looksLikePath) || (looksLikeRegexSyntax && !strings.Contains(rest, "/")) {
				inRegex = true
			}
		}
		buf.WriteRune(r)
	}
	flush()
	return tokens
}

func hasUnescapedSlash(s string) bool {
	esc := false
	for _, r := range s {
		if esc {
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		if r == '/' {
			return true
		}
	}
	return false
}

func parseRegexToken(tok string) (string, bool) {
	if !strings.HasPrefix(tok, "/") {
		return "", false
	}
	if len(tok) >= 2 && strings.HasSuffix(tok, "/") {
		inner := tok[1 : len(tok)-1]
		if inner == "" {
			return "", false
		}
		return strings.ReplaceAll(inner, `\/`, `/`), true
	}
	// Handle unclosed regex syntax such as `/[abc` so UI surfaces an invalid regex indicator.
	if strings.ContainsAny(tok[1:], "[(*+?\\") && !strings.Contains(tok[1:], "/") {
		return tok[1:], true
	}
	return "", false
}

func normalizeClipboardTypeToken(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "text", "texts", "plain":
		return "text"
	case "image", "images", "img", "photo":
		return "image"
	case "file", "files", "dir", "folder":
		return "file"
	case "link", "links", "url", "urls":
		return "link"
	case "color", "colors", "hex":
		return "color"
	case "code":
		return "code"
	case "video", "videos":
		return "video"
	case "audio":
		return "audio"
	case "doc", "document", "documents":
		return "document"
	case "archive", "zip":
		return "archive"
	default:
		return ""
	}
}

func parseSinceDuration(v string, now time.Time) (time.Time, bool) {
	s := strings.ToLower(strings.TrimSpace(v))
	if len(s) < 2 {
		return time.Time{}, false
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	switch unit {
	case 'm':
		return now.Add(-time.Duration(n) * time.Minute), true
	case 'h':
		return now.Add(-time.Duration(n) * time.Hour), true
	case 'd':
		return now.Add(-time.Duration(n) * 24 * time.Hour), true
	case 'w':
		return now.Add(-time.Duration(n) * 7 * 24 * time.Hour), true
	default:
		return time.Time{}, false
	}
}

// ClipboardEntryCategory classifies an OmniboxClipboardEntry for filtering & UI chips.
func ClipboardEntryCategory(e OmniboxClipboardEntry) string {
	if e.Media.IsMedia() {
		kind := strings.ToLower(strings.TrimSpace(e.Media.Kind))
		if kind == "" {
			return "file"
		}
		return kind
	}
	return classifyClipboardTextCategory(e.Content)
}

// ClipboardItemCategory classifies an OmniboxItem clipboard entry for UI filtering.
func ClipboardItemCategory(item OmniboxItem) string {
	if item.ClipboardMedia != nil && item.ClipboardMedia.IsMedia() {
		kind := strings.ToLower(strings.TrimSpace(item.ClipboardMedia.Kind))
		if kind == "" {
			return "file"
		}
		return kind
	}
	return classifyClipboardTextCategory(item.ClipboardContent)
}

func classifyClipboardTextCategory(content string) string {
	s := strings.TrimSpace(content)
	if s == "" {
		return "text"
	}
	if len(s) > clipboardQuerySniffBytes {
		s = s[:clipboardQuerySniffBytes]
	}
	if !strings.ContainsAny(s, "\r\n") {
		if IsHTTPURL(s) {
			return "link"
		}
		if isSimpleHexColor(s) {
			return "color"
		}
	}
	if looksLikeClipboardCode(s) {
		return "code"
	}
	return "text"
}

// IsHTTPURL reports whether raw is a single-line http:// or https:// URL with a host.
func IsHTTPURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func isSimpleHexColor(s string) bool {
	if len(s) < 4 || s[0] != '#' {
		return false
	}
	switch len(s) - 1 {
	case 3, 4, 6, 8:
	default:
		return false
	}
	for _, r := range s[1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func looksLikeClipboardCode(s string) bool {
	for _, marker := range []string{"{\n", "};", "=>", "func ", "function ", "def ", "class ", "import ", "package ", "const ", "</", "#include", "SELECT ", "select "} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return strings.Contains(s, "\n\t") || strings.Contains(s, "\n    ")
}

// ClipboardCategoryMatches reports whether entry category matches a requested filter kind.
func ClipboardCategoryMatches(entryCategory string, isMedia bool, filterKind string) bool {
	f := strings.ToLower(strings.TrimSpace(filterKind))
	if f == "" || f == "all" {
		return true
	}
	if f == "text" {
		return !isMedia
	}
	if f == "file" {
		return isMedia && entryCategory != "image"
	}
	return entryCategory == f
}

// FilterClipboardEntries filters entries by parsed ClipboardQuery.
func FilterClipboardEntries(entries []OmniboxClipboardEntry, q ClipboardQuery, now time.Time) []OmniboxClipboardEntry {
	if q.RegexErr != "" {
		return nil
	}
	out := make([]OmniboxClipboardEntry, 0, len(entries))
	for _, e := range entries {
		if !clipboardEntryMatchesQuery(e, q) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func clipboardEntryMatchesQuery(e OmniboxClipboardEntry, q ClipboardQuery) bool {
	if q.PinnedFilter != nil && e.IsPinned != *q.PinnedFilter {
		return false
	}
	if !q.SinceTime.IsZero() {
		ts := e.CopiedAt
		if ts.IsZero() {
			ts = e.FirstCopiedAt
		}
		if ts.IsZero() || ts.Before(q.SinceTime) {
			return false
		}
	}
	if len(q.Types) > 0 {
		cat := ClipboardEntryCategory(e)
		isMedia := e.Media.IsMedia()
		matchedType := false
		for _, want := range q.Types {
			if ClipboardCategoryMatches(cat, isMedia, want) {
				matchedType = true
				break
			}
		}
		if !matchedType {
			return false
		}
	}
	if len(q.Apps) > 0 {
		appName := strings.ToLower(e.SourceAppName)
		bundleID := strings.ToLower(e.SourceBundleID)
		matchedApp := false
		for _, want := range q.Apps {
			if (appName != "" && strings.Contains(appName, want)) || (bundleID != "" && strings.Contains(bundleID, want)) {
				matchedApp = true
				break
			}
		}
		if !matchedApp {
			return false
		}
	}
	for _, phrase := range q.ExactPhrases {
		if !strings.Contains(e.Content, phrase) && !strings.Contains(strings.ToLower(e.Content), strings.ToLower(phrase)) {
			return false
		}
	}
	for _, re := range q.Regexes {
		if !re.MatchString(e.Content) && !re.MatchString(e.SourceAppName) {
			return false
		}
	}
	for _, term := range q.Terms {
		lt := strings.ToLower(term)
		if !strings.Contains(strings.ToLower(e.Content), lt) &&
			!strings.Contains(strings.ToLower(e.SourceAppName), lt) &&
			!strings.Contains(strings.ToLower(e.SourceBundleID), lt) {
			return false
		}
	}
	return true
}

// SortClipboardOmniboxItems orders clipboard items according to pinPlacement ("top" | "bottom" | "inline").
func SortClipboardOmniboxItems(items []OmniboxItem, pinPlacement string) []OmniboxItem {
	if len(items) <= 1 {
		return items
	}
	out := make([]OmniboxItem, len(items))
	copy(out, items)
	switch strings.ToLower(strings.TrimSpace(pinPlacement)) {
	case "bottom":
		sort.SliceStable(out, func(i, j int) bool {
			pi := out[i].KeyHint == KeyHintPinned
			pj := out[j].KeyHint == KeyHintPinned
			if pi != pj {
				return !pi
			}
			return false
		})
	case "inline":
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].ClipboardCopiedAt.After(out[j].ClipboardCopiedAt)
		})
	default:
		sort.SliceStable(out, func(i, j int) bool {
			pi := out[i].KeyHint == KeyHintPinned
			pj := out[j].KeyHint == KeyHintPinned
			if pi != pj {
				return pi
			}
			return false
		})
	}
	return out
}

// NormalizePlainTextForPaste strips Windows/control line breaks and invisible formatting artifacts.
func NormalizePlainTextForPaste(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimFunc(s, func(r rune) bool {
		return r == '\uFEFF' || r == '\u200B' || r == '\u200C' || r == '\u200D'
	})
}
