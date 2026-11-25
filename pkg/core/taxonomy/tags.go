package taxonomy

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// MaxTagRunes caps the maximum rune length of a normalized tag.
	MaxTagRunes = 24
	// MaxTagsPerEntity caps the number of tags stored per entity.
	MaxTagsPerEntity = 8

	NamespaceEnv  = "env"
	NamespaceProj = "proj"
	NamespaceType = "type"
)

// ErrUnsafeMetadataTag indicates a tag or metadata value matched a credential
// or high-entropy secret pattern and was rejected fail-closed (D4).
var ErrUnsafeMetadataTag = errors.New("taxonomy: unsafe metadata tag")

// TagCount pairs a normalized tag with its occurrence count.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

var (
	secretSubstrings = []string{
		"password",
		"passwd",
		"secret",
		"token",
		"api_key",
		"apikey",
		"private_key",
		"access_key",
		"begin private key",
		"begin rsa private key",
		"begin openssh private key",
		"begin ec private key",
		"begin pgp private key",
	}

	secretPrefixPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[^a-z0-9])sk-[a-z0-9_-]{4,}`),
		regexp.MustCompile(`(?:^|[^a-z0-9])gh[pousr]_[a-z0-9_]{4,}`),
		regexp.MustCompile(`(?:^|[^a-z0-9])github_pat_[a-z0-9_]{4,}`),
		regexp.MustCompile(`(?:^|[^a-z0-9])xox[bp]-[a-z0-9_-]{4,}`),
		regexp.MustCompile(`(?:^|[^a-z0-9])akia[0-9a-z]{8,}`),
		regexp.MustCompile(`(?:^|[^a-z0-9])aiza[0-9a-z_-]{8,}`),
		regexp.MustCompile(`(?:^|\s)bearer\s+[a-z0-9._-]{4,}`),
	}
)

// NormalizeTag normalizes a single tag by trimming surrounding whitespace and
// leading '#' characters, collapsing internal whitespace runs to a single space,
// lowercasing ASCII letters while preserving UTF-8/CJK runes, and truncating to
// MaxTagRunes.
func NormalizeTag(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimLeft(s, "#")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	s = toLowerASCII(s)
	if utf8.RuneCountInString(s) > MaxTagRunes {
		var b strings.Builder
		b.Grow(len(s))
		count := 0
		for _, r := range s {
			if count >= MaxTagRunes {
				break
			}
			b.WriteRune(r)
			count++
		}
		s = strings.TrimSpace(b.String())
	}
	return s
}

// NormalizeTags normalizes each tag, drops empties, deduplicates preserving
// order, and caps the result at MaxTagsPerEntity.
func NormalizeTags(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, min(len(raw), MaxTagsPerEntity))
	for _, item := range raw {
		norm := NormalizeTag(item)
		if norm == "" {
			continue
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
		if len(out) >= MaxTagsPerEntity {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseTagNamespace parses controlled namespaces ("env:", "proj:", "type:").
// If matched with a non-empty value, it returns (ns, value, true); otherwise it
// returns ("", normalizedTag, false).
func ParseTagNamespace(tag string) (ns, value string, ok bool) {
	norm := NormalizeTag(tag)
	if norm == "" {
		return "", "", false
	}
	idx := strings.IndexByte(norm, ':')
	if idx <= 0 {
		return "", norm, false
	}
	prefix := norm[:idx]
	rest := strings.TrimSpace(norm[idx+1:])
	if rest == "" {
		return "", norm, false
	}
	switch prefix {
	case NamespaceEnv, NamespaceProj, NamespaceType:
		return prefix, rest, true
	default:
		return "", norm, false
	}
}

// FormatNamespacedTag formats a namespace and value into a normalized tag.
func FormatNamespacedTag(ns, value string) string {
	nsNorm := toLowerASCII(strings.TrimSpace(ns))
	valNorm := NormalizeTag(value)
	if valNorm == "" {
		return ""
	}
	if nsNorm == "" {
		return valNorm
	}
	return NormalizeTag(nsNorm + ":" + valNorm)
}

// ValidateSafeMetadataTag inspects raw (before 24-rune truncation) and its
// normalized form, failing closed with ErrUnsafeMetadataTag if any credential,
// secret keyword, private key header, or high-entropy token is detected.
func ValidateSafeMetadataTag(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	stripped := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	if stripped == "" {
		return nil
	}

	candidates := []string{trimmed, stripped, NormalizeTag(raw)}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		lower := strings.ToLower(c)
		for _, kw := range secretSubstrings {
			if strings.Contains(lower, kw) {
				return ErrUnsafeMetadataTag
			}
		}
		for _, re := range secretPrefixPatterns {
			if re.MatchString(lower) {
				return ErrUnsafeMetadataTag
			}
		}
	}

	// High-entropy token inspection on raw before truncation.
	for _, field := range strings.Fields(stripped) {
		token := strings.TrimLeft(field, "#")
		if looksLikeHighEntropySecret(token) {
			return ErrUnsafeMetadataTag
		}
		if idx := strings.IndexByte(token, ':'); idx >= 0 && idx+1 < len(token) {
			if looksLikeHighEntropySecret(token[idx+1:]) {
				return ErrUnsafeMetadataTag
			}
		}
		for _, part := range strings.Split(token, ".") {
			if looksLikeHighEntropySecret(part) {
				return ErrUnsafeMetadataTag
			}
		}
	}
	return nil
}

func looksLikeHighEntropySecret(tok string) bool {
	if len(tok) < 16 {
		return false
	}
	var upperCount, lowerCount, digitCount, symCount int
	var hasHyphen bool
	for i := 0; i < len(tok); i++ {
		b := tok[i]
		switch {
		case b >= 'A' && b <= 'Z':
			upperCount++
		case b >= 'a' && b <= 'z':
			lowerCount++
		case b >= '0' && b <= '9':
			digitCount++
		case b == '-':
			hasHyphen = true
		case b == '_' || b == '+' || b == '/' || b == '=':
			symCount++
		default:
			// Non-ASCII (e.g. CJK) or punctuation outside token alphabet.
			return false
		}
	}
	classes := 0
	if upperCount > 0 {
		classes++
	}
	if lowerCount > 0 {
		classes++
	}
	if digitCount > 0 {
		classes++
	}
	if symCount > 0 {
		classes++
	}
	if classes < 2 {
		return false
	}
	entropy := shannonEntropy(tok)
	// Hyphenated human-readable tags (e.g. "release-2026-11-25") have hyphens
	// as word separators; allow benign 2-class hyphenated words unless entropy
	// and class mix look strongly random.
	if hasHyphen {
		return classes >= 3 && entropy >= 3.8 && (digitCount*5 >= len(tok) || upperCount*4 >= len(tok))
	}
	// Avoid false positives on benign CamelCase phrases with at most minor digits
	// (e.g. "VeryLongTagNameThatExceeds24Runes"): require symbols, >= 20% digits,
	// or >= 25% uppercase alongside high Shannon entropy.
	if symCount == 0 && digitCount*5 < len(tok) && upperCount*4 < len(tok) {
		return false
	}
	if len(tok) >= 20 {
		return entropy >= 3.5
	}
	return entropy >= 3.6
}

func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

func toLowerASCII(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b.Len() == 0 {
				b.Grow(len(s))
				b.WriteString(s[:i])
			}
			b.WriteByte(c + ('a' - 'A'))
		} else if b.Len() > 0 {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return s
	}
	return b.String()
}
