package engine

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// EvaluateUtilities evaluates instant developer & productivity utilities for query:
//   - Math expressions (+, -, *, /, %, ^, parentheses, unary +/-) -> "Result = <value>"
//   - "uuid" -> RFC 4122 v4 UUID
//   - "ts" / "now" -> Unix seconds & ISO-8601 UTC
//   - 10-digit / 13-digit numeric timestamp -> UTC & Local RFC3339
//   - "b64 <text>" / "b64d <base64>" -> Base64 encode / decode
//   - "hex <int>" / "0x<hex>" -> Hex / Decimal conversion
func EvaluateUtilities(query string, now time.Time) []OmniboxItem {
	trimmed := strings.TrimSpace(NormalizeFullWidth(query))
	if trimmed == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}

	var out []OmniboxItem
	if trimmed == "=" {
		id := generateUUIDv4()
		unixSec := strconv.FormatInt(now.Unix(), 10)
		isoUTC := now.UTC().Format(time.RFC3339)
		return []OmniboxItem{
			{
				ID:           "utility:ts:unix",
				Title:        unixSec,
				Subtitle:     "Unix Timestamp (seconds) • Type math after '=' (e.g. =(2+3)*4)",
				KeyHint:      "Copy",
				Icon:         "clock",
				Kind:         ItemUtility,
				UtilityValue: unixSec,
				Score:        1300,
			},
			{
				ID:           "utility:ts:iso",
				Title:        isoUTC,
				Subtitle:     "ISO-8601 UTC Timestamp • Local: " + now.Local().Format(time.RFC3339),
				KeyHint:      "Copy",
				Icon:         "clock",
				Kind:         ItemUtility,
				UtilityValue: isoUTC,
				Score:        1290,
			},
			{
				ID:           "utility:uuid",
				Title:        id,
				Subtitle:     "UUID v4 • Press Enter to copy or Secondary to paste",
				KeyHint:      "Copy",
				Icon:         "number",
				Kind:         ItemUtility,
				UtilityValue: id,
				Score:        1280,
			},
		}
	}
	lower := strings.ToLower(trimmed)

	// 1. UUID v4 generator
	if lower == "uuid" || lower == "guid" {
		id := generateUUIDv4()
		out = append(out, OmniboxItem{
			ID:           "utility:uuid",
			Title:        id,
			Subtitle:     "UUID v4 • Press Enter to copy",
			KeyHint:      "Copy",
			Icon:         "number",
			Kind:         ItemUtility,
			UtilityValue: id,
			Score:        1300,
		})
		return out
	}

	// 2. Current timestamp ("ts" / "now")
	if lower == "ts" || lower == "now" || lower == "timestamp" {
		unixSec := strconv.FormatInt(now.Unix(), 10)
		isoUTC := now.UTC().Format(time.RFC3339)
		out = append(out,
			OmniboxItem{
				ID:           "utility:ts:unix",
				Title:        unixSec,
				Subtitle:     "Unix Timestamp (seconds) • " + isoUTC,
				KeyHint:      "Copy",
				Icon:         "clock",
				Kind:         ItemUtility,
				UtilityValue: unixSec,
				Score:        1300,
			},
			OmniboxItem{
				ID:           "utility:ts:iso",
				Title:        isoUTC,
				Subtitle:     "ISO-8601 UTC Timestamp • Local: " + now.Local().Format(time.RFC3339),
				KeyHint:      "Copy",
				Icon:         "clock",
				Kind:         ItemUtility,
				UtilityValue: isoUTC,
				Score:        1290,
			},
		)
		return out
	}

	// 3. Base64 encode / decode
	if strings.HasPrefix(lower, "b64 ") || strings.HasPrefix(lower, "base64 ") {
		idx := strings.IndexByte(trimmed, ' ')
		rawText := strings.TrimSpace(trimmed[idx+1:])
		if rawText != "" {
			encoded := base64.StdEncoding.EncodeToString([]byte(rawText))
			out = append(out, OmniboxItem{
				ID:           "utility:b64:encode",
				Title:        encoded,
				Subtitle:     "Base64 Encode • Press Enter to copy",
				KeyHint:      "Copy",
				Icon:         "lock.rotation",
				Kind:         ItemUtility,
				UtilityValue: encoded,
				Score:        1300,
			})
			return out
		}
	}
	if strings.HasPrefix(lower, "b64d ") || strings.HasPrefix(lower, "base64d ") {
		idx := strings.IndexByte(trimmed, ' ')
		rawB64 := strings.TrimSpace(trimmed[idx+1:])
		if rawB64 != "" {
			if decoded, ok := tryBase64Decode(rawB64); ok {
				out = append(out, OmniboxItem{
					ID:           "utility:b64:decode",
					Title:        decoded,
					Subtitle:     "Base64 Decode • Press Enter to copy",
					KeyHint:      "Copy",
					Icon:         "lock.open",
					Kind:         ItemUtility,
					UtilityValue: decoded,
					Score:        1300,
				})
				return out
			}
		}
	}

	// 4. Decimal -> Hex ("hex <int>")
	if strings.HasPrefix(lower, "hex ") {
		arg := strings.TrimSpace(trimmed[4:])
		if n, err := strconv.ParseInt(arg, 10, 64); err == nil {
			hexVal := fmt.Sprintf("0x%X", n)
			out = append(out, OmniboxItem{
				ID:           "utility:hex:fromdec",
				Title:        hexVal,
				Subtitle:     fmt.Sprintf("Decimal %d → Hex • Press Enter to copy", n),
				KeyHint:      "Copy",
				Icon:         "number",
				Kind:         ItemUtility,
				UtilityValue: hexVal,
				Score:        1300,
			})
			return out
		}
	}

	// 5. Hex -> Decimal ("0x<hex>")
	if strings.HasPrefix(lower, "0x") && len(trimmed) > 2 {
		hexPart := trimmed[2:]
		if isAllHexDigits(hexPart) {
			if n, err := strconv.ParseInt(hexPart, 16, 64); err == nil {
				decStr := strconv.FormatInt(n, 10)
				out = append(out, OmniboxItem{
					ID:           "utility:hex:todec",
					Title:        decStr,
					Subtitle:     fmt.Sprintf("Hex %s → Decimal • Press Enter to copy", trimmed),
					KeyHint:      "Copy",
					Icon:         "number",
					Kind:         ItemUtility,
					UtilityValue: decStr,
					Score:        1300,
				})
				return out
			}
		}
	}

	// 6. 10-digit or 13-digit numeric Unix timestamp converter
	if (len(trimmed) == 10 || len(trimmed) == 13) && isAllASCIIDigits(trimmed) {
		if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			var t time.Time
			unit := "seconds"
			if len(trimmed) == 13 {
				t = time.UnixMilli(n)
				unit = "milliseconds"
			} else {
				t = time.Unix(n, 0)
			}
			utcStr := t.UTC().Format(time.RFC3339)
			localStr := t.Local().Format(time.RFC3339)
			out = append(out, OmniboxItem{
				ID:           "utility:ts:convert:" + trimmed,
				Title:        utcStr,
				Subtitle:     fmt.Sprintf("Timestamp (%s) • Local: %s • UTC: %s", unit, localStr, utcStr),
				KeyHint:      "Copy",
				Icon:         "calendar.badge.clock",
				Kind:         ItemUtility,
				UtilityValue: utcStr,
				Score:        1300,
			})
			return out
		}
	}

	// 7. Recursive-descent math evaluator
	if val, ok := EvaluateMathExpression(trimmed); ok {
		formatted := formatMathNumber(val)
		out = append(out, OmniboxItem{
			ID:           "utility:math:" + formatted,
			Title:        fmt.Sprintf("Result = %s", formatted),
			Subtitle:     fmt.Sprintf("Calculator • %s — Press Enter to copy", trimmed),
			KeyHint:      "Copy",
			Icon:         "function",
			Kind:         ItemUtility,
			UtilityValue: formatted,
			Score:        1250,
		})
	}

	return out
}

// EvaluateMathExpression safely parses and evaluates a mathematical expression
// supporting +, -, *, /, %, ^, parentheses, and unary +/-.
func EvaluateMathExpression(raw string) (float64, bool) {
	expr := strings.TrimSpace(NormalizeFullWidth(raw))
	hasExplicitEquals := false
	if strings.HasPrefix(expr, "=") {
		hasExplicitEquals = true
		expr = strings.TrimSpace(expr[1:])
	}
	if expr == "" {
		return 0, false
	}

	hasOperatorOrParen := hasExplicitEquals
	hasDigit := false
	for _, r := range expr {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+' || r == '-' || r == '*' || r == '/' || r == '%' || r == '^' || r == '(' || r == ')':
			hasOperatorOrParen = true
		case r == '.' || unicode.IsSpace(r):
		default:
			return 0, false
		}
	}
	if !hasDigit || !hasOperatorOrParen {
		return 0, false
	}

	p := mathParser{input: expr}
	val, ok := p.parseExpr()
	p.skipSpaces()
	if !ok || p.pos != len(p.input) || math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, false
	}
	// If there was no explicit '=' and the input was just a signed literal (e.g. "-42"),
	// require at least one binary op or parentheses so plain numbers don't trigger math mode.
	if !hasExplicitEquals && p.opCount == 0 && !p.hadParens {
		return 0, false
	}
	return val, true
}

type mathParser struct {
	input     string
	pos       int
	opCount   int
	hadParens bool
}

func (p *mathParser) skipSpaces() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

func (p *mathParser) parseExpr() (float64, bool) {
	left, ok := p.parseTerm()
	if !ok {
		return 0, false
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			break
		}
		ch := p.input[p.pos]
		if ch != '+' && ch != '-' {
			break
		}
		p.pos++
		p.opCount++
		right, ok := p.parseTerm()
		if !ok {
			return 0, false
		}
		if ch == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, true
}

func (p *mathParser) parseTerm() (float64, bool) {
	left, ok := p.parsePower()
	if !ok {
		return 0, false
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			break
		}
		ch := p.input[p.pos]
		if ch != '*' && ch != '/' && ch != '%' {
			break
		}
		p.pos++
		p.opCount++
		right, ok := p.parsePower()
		if !ok {
			return 0, false
		}
		switch ch {
		case '*':
			left *= right
		case '/':
			if right == 0 {
				return 0, false
			}
			left /= right
		case '%':
			if right == 0 {
				return 0, false
			}
			left = math.Mod(left, right)
		}
	}
	return left, true
}

func (p *mathParser) parsePower() (float64, bool) {
	base, ok := p.parseUnary()
	if !ok {
		return 0, false
	}
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '^' {
		p.pos++
		p.opCount++
		exp, ok := p.parsePower()
		if !ok {
			return 0, false
		}
		return math.Pow(base, exp), true
	}
	return base, true
}

func (p *mathParser) parseUnary() (float64, bool) {
	p.skipSpaces()
	if p.pos < len(p.input) {
		switch p.input[p.pos] {
		case '+':
			p.pos++
			return p.parseUnary()
		case '-':
			p.pos++
			v, ok := p.parseUnary()
			return -v, ok
		}
	}
	return p.parsePrimary()
}

func (p *mathParser) parsePrimary() (float64, bool) {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0, false
	}
	if p.input[p.pos] == '(' {
		p.pos++
		p.hadParens = true
		val, ok := p.parseExpr()
		if !ok {
			return 0, false
		}
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, false
		}
		p.pos++
		return val, true
	}

	start := p.pos
	dotCount := 0
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch >= '0' && ch <= '9' {
			p.pos++
		} else if ch == '.' {
			dotCount++
			if dotCount > 1 {
				return 0, false
			}
			p.pos++
		} else {
			break
		}
	}
	if p.pos == start || (p.pos-start == 1 && p.input[start] == '.') {
		return 0, false
	}
	val, err := strconv.ParseFloat(p.input[start:p.pos], 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func formatMathNumber(val float64) string {
	if val == 0 {
		return "0"
	}
	rounded := math.Round(val)
	if math.Abs(val-rounded) < 1e-9 && math.Abs(rounded) < 1e15 {
		return strconv.FormatInt(int64(rounded), 10)
	}
	return strconv.FormatFloat(val, 'f', -1, 64)
}

func generateUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> ((i % 8) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func tryBase64Decode(s string) (string, bool) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		if raw, err := enc.DecodeString(s); err == nil && len(raw) > 0 {
			return string(raw), true
		}
	}
	return "", false
}

func isAllASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isAllHexDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isDigit := c >= '0' && c <= '9'
		isLower := c >= 'a' && c <= 'f'
		isUpper := c >= 'A' && c <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return true
}
