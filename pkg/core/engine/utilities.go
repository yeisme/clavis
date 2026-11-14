package engine

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yeisme/clavis/pkg/core/i18n"
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
	if m := assignmentRE.FindStringSubmatch(trimmed); m != nil {
		v, ok := evalEnvExpr(m[2], DefaultCalcEnv)
		if !ok || !DefaultCalcEnv.Set(m[1], v) {
			return nil
		}
		return []OmniboxItem{{ID: "utility:calc:set:" + m[1], Title: "$" + m[1] + " = " + formatMathNumber(v), Subtitle: "variable", KeyHint: i18n.T("common.copy"), Icon: "function", Kind: ItemUtility, UtilityValue: formatMathNumber(v), Score: 1300}}
	}
	if strings.Contains(trimmed, "$") {
		if v, ok := evalEnvExpr(trimmed, DefaultCalcEnv); ok {
			formatted := formatMathNumber(v)
			return []OmniboxItem{{ID: "utility:calc:env:" + trimmed, Title: i18n.Tf("engine.utility.result", formatted), Subtitle: i18n.Tf("engine.utility.calculator", trimmed), KeyHint: i18n.T("common.copy"), Icon: "function", Kind: ItemUtility, UtilityValue: formatted, Score: 1250}}
		}
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
				Subtitle:     i18n.T("engine.utility.ts_hint"),
				KeyHint:      i18n.T("common.copy"),
				Icon:         "clock",
				Kind:         ItemUtility,
				UtilityValue: unixSec,
				Score:        1300,
			},
			{
				ID:           "utility:ts:iso",
				Title:        isoUTC,
				Subtitle:     i18n.Tf("engine.utility.ts_iso", now.Local().Format(time.RFC3339)),
				KeyHint:      i18n.T("common.copy"),
				Icon:         "clock",
				Kind:         ItemUtility,
				UtilityValue: isoUTC,
				Score:        1290,
			},
			{
				ID:           "utility:uuid",
				Title:        id,
				Subtitle:     i18n.T("engine.utility.uuid_hint2"),
				KeyHint:      i18n.T("common.copy"),
				Icon:         "number",
				Kind:         ItemUtility,
				UtilityValue: id,
				Score:        1280,
			},
		}
	}
	lower := strings.ToLower(trimmed)
	if lower == "pi" || lower == "=pi" || lower == "e" || lower == "=e" {
		v := math.Pi
		if lower == "e" || lower == "=e" {
			v = math.E
		}
		formatted := formatMathNumber(v)
		return []OmniboxItem{{ID: "utility:constant:" + lower, Title: formatted, Subtitle: "constant", KeyHint: i18n.T("common.copy"), Icon: "number", Kind: ItemUtility, UtilityValue: formatted, Score: 1250}}
	}

	// Extended utilities are checked before the legacy forms, while legacy output remains unchanged.
	if extended := evaluateAdvancedUtility(trimmed, now); len(extended) > 0 {
		return extended
	}

	if v, ok := evaluateFunctionExpression(trimmed); ok {
		formatted := formatMathNumber(v)
		return []OmniboxItem{{ID: "utility:function:" + formatted, Title: i18n.Tf("engine.utility.result", formatted), Subtitle: i18n.Tf("engine.utility.calculator", trimmed), KeyHint: i18n.T("common.copy"), Icon: "function", Kind: ItemUtility, UtilityValue: formatted, Score: 1250}}
	}

	// 1. UUID v4 generator
	if lower == "uuid" || lower == "guid" {
		id := generateUUIDv4()
		out = append(out, OmniboxItem{
			ID:           "utility:uuid",
			Title:        id,
			Subtitle:     i18n.T("engine.utility.uuid_hint"),
			KeyHint:      i18n.T("common.copy"),
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
				Subtitle:     i18n.Tf("engine.utility.ts_unix", isoUTC),
				KeyHint:      i18n.T("common.copy"),
				Icon:         "clock",
				Kind:         ItemUtility,
				UtilityValue: unixSec,
				Score:        1300,
			},
			OmniboxItem{
				ID:           "utility:ts:iso",
				Title:        isoUTC,
				Subtitle:     i18n.Tf("engine.utility.ts_iso", now.Local().Format(time.RFC3339)),
				KeyHint:      i18n.T("common.copy"),
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
				Subtitle:     i18n.T("engine.utility.b64_enc"),
				KeyHint:      i18n.T("common.copy"),
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
					Subtitle:     i18n.T("engine.utility.b64_dec"),
					KeyHint:      i18n.T("common.copy"),
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
				Subtitle:     i18n.Tf("engine.utility.dec_to_hex", n),
				KeyHint:      i18n.T("common.copy"),
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
					Subtitle:     i18n.Tf("engine.utility.hex_to_dec", trimmed),
					KeyHint:      i18n.T("common.copy"),
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
			unit := i18n.T("engine.utility.unit_seconds")
			if len(trimmed) == 13 {
				t = time.UnixMilli(n)
				unit = i18n.T("engine.utility.unit_milliseconds")
			} else {
				t = time.Unix(n, 0)
			}
			utcStr := t.UTC().Format(time.RFC3339)
			localStr := t.Local().Format(time.RFC3339)
			out = append(out, OmniboxItem{
				ID:           "utility:ts:convert:" + trimmed,
				Title:        utcStr,
				Subtitle:     i18n.Tf("engine.utility.ts_convert", unit, localStr, utcStr),
				KeyHint:      i18n.T("common.copy"),
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
			Title:        i18n.Tf("engine.utility.result", formatted),
			Subtitle:     i18n.Tf("engine.utility.calculator", trimmed),
			KeyHint:      i18n.T("common.copy"),
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

// CalcEnv is a bounded, concurrency-safe calculator variable environment.
type CalcEnv struct {
	mu   sync.RWMutex
	vars map[string]float64
	max  int
}

func NewCalcEnv(max int) *CalcEnv {
	if max <= 0 {
		max = 256
	}
	return &CalcEnv{vars: map[string]float64{}, max: max}
}

var DefaultCalcEnv = NewCalcEnv(256)

func (e *CalcEnv) Set(name string, value float64) bool {
	name = strings.TrimSpace(strings.TrimPrefix(name, "$"))
	if name == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.vars[name]; !ok && len(e.vars) >= e.max {
		return false
	}
	e.vars[name] = value
	return true
}
func (e *CalcEnv) Get(name string) (float64, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.vars[strings.TrimPrefix(strings.TrimSpace(name), "$")]
	return v, ok
}

var assignmentRE = regexp.MustCompile(`^\s*\$([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+)$`)

func EvaluateWithEnv(query string, now time.Time, env *CalcEnv) []OmniboxItem {
	if env == nil {
		env = DefaultCalcEnv
	}
	m := assignmentRE.FindStringSubmatch(query)
	if m != nil {
		v, ok := evalEnvExpr(m[2], env)
		if !ok {
			return nil
		}
		env.Set(m[1], v)
		return utilityValue("calc:set:"+m[1], fmt.Sprintf("$%s = %s", m[1], formatMathNumber(v)), "variable", v)
	}
	if env != nil {
		if v, ok := evalEnvExpr(query, env); ok {
			return utilityValue("calc:env:"+query, formatMathNumber(v), "Result", v)
		}
	}
	return EvaluateUtilities(query, now)
}
func (e *CalcEnv) EvaluateUtilities(query string, now time.Time) []OmniboxItem {
	return EvaluateWithEnv(query, now, e)
}
func EvaluateUtilitiesWithEnv(query string, now time.Time, env *CalcEnv) []OmniboxItem {
	return EvaluateWithEnv(query, now, env)
}

func evalEnvExpr(s string, e *CalcEnv) (float64, bool) {
	if e == nil {
		e = DefaultCalcEnv
	}
	constantRE := regexp.MustCompile(`(?i)\b(pi|e)\b`)
	s = constantRE.ReplaceAllStringFunc(s, func(token string) string {
		if strings.EqualFold(token, "pi") {
			return fmt.Sprint(math.Pi)
		}
		return fmt.Sprint(math.E)
	})
	re := regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`)
	valid := true
	s = re.ReplaceAllStringFunc(s, func(x string) string {
		if v, ok := e.Get(x); ok {
			return strconv.FormatFloat(v, 'g', -1, 64)
		}
		valid = false
		return "0"
	})
	if !valid {
		return 0, false
	}
	return EvaluateMathExpression("=" + s)
}
func utilityValue(id, title, subtitle string, v float64) []OmniboxItem {
	return []OmniboxItem{{ID: "utility:" + id, Title: title, Subtitle: subtitle, KeyHint: i18n.T("common.copy"), Icon: "function", Kind: ItemUtility, UtilityValue: title, Score: 1300}}
}

var unitRE = regexp.MustCompile(`(?i)^\s*(-?(?:\d+(?:\.\d*)?|\.\d+))\s*([a-zµ]+)\s+(?:to|in)\s+([a-zµ]+)\s*$`)

func evaluateAdvancedUtility(q string, now time.Time) []OmniboxItem {
	if m := unitRE.FindStringSubmatch(q); m != nil {
		if v, ok := convertUnit(parseFloat(m[1]), strings.ToLower(m[2]), strings.ToLower(m[3])); ok {
			return utilityValue("convert", formatMathNumber(v), m[2]+" → "+m[3], v)
		}
	}
	if out := evaluateColor(q); len(out) > 0 {
		return out
	}
	if out := evaluateDateMath(q, now); len(out) > 0 {
		return out
	}
	return nil
}
func parseFloat(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
func convertUnit(v float64, from, to string) (float64, bool) {
	length := map[string]float64{"mm": .001, "cm": .01, "m": 1, "km": 1000, "in": .0254, "ft": .3048, "mi": 1609.344}
	weight := map[string]float64{"mg": .001, "g": 1, "kg": 1000, "oz": 28.349523125, "lb": 453.59237}
	data := map[string]float64{"b": 1, "kb": 1000, "mb": 1e6, "gb": 1e9, "tb": 1e12, "kib": 1024, "mib": 1 << 20, "gib": 1 << 30}
	duration := map[string]float64{"ms": .001, "s": 1, "min": 60, "h": 3600, "d": 86400}
	if a, ok := length[from]; ok {
		if b, x := length[to]; x {
			return v * a / b, true
		}
	}
	if a, ok := weight[from]; ok {
		if b, x := weight[to]; x {
			return v * a / b, true
		}
	}
	if a, ok := data[from]; ok {
		if b, x := data[to]; x {
			return v * a / b, true
		}
	}
	if a, ok := duration[from]; ok {
		if b, x := duration[to]; x {
			return v * a / b, true
		}
	}
	if from == "px" && to == "rem" {
		return v / 16, true
	}
	if from == "rem" && to == "px" {
		return v * 16, true
	}
	if strings.Contains("cfk", from) && strings.Contains("cfk", to) {
		var c float64
		switch from {
		case "c":
			c = v
		case "f":
			c = (v - 32) * 5 / 9
		case "k":
			c = v - 273.15
		}
		switch to {
		case "c":
			return c, true
		case "f":
			return c*9/5 + 32, true
		case "k":
			return c + 273.15, true
		}
	}
	return 0, false
}

var tzOffsets = map[string]int{"utc": 0, "cst": -6, "pst": -8, "est": -5, "jst": 9, "cet": 1, "shanghai": 8, "tokyo": 9, "london": 0, "ny": -5}
var clockZoneRE = regexp.MustCompile(`(?i)^\s*(\d{1,2}:\d{2})\s+([a-z]+)\s+to\s+([a-z]+)\s*$`)
var dateMathRE = regexp.MustCompile(`(?i)^\s*(now|today|\d{1,2}:\d{2})\s*(?:(\+|-)\s*)?(?:(\d+)d\s*)?(?:(\d+)h\s*)?(?:(\d+)w\s*)?(?:to\s*([a-z]+))?\s*$`)

func evaluateDateMath(q string, now time.Time) []OmniboxItem {
	if z := clockZoneRE.FindStringSubmatch(q); z != nil {
		parts := strings.Split(z[1], ":")
		h, _ := strconv.Atoi(parts[0])
		mi, _ := strconv.Atoi(parts[1])
		from, fok := tzOffsets[strings.ToLower(z[2])]
		to, tok := tzOffsets[strings.ToLower(z[3])]
		if fok && tok {
			t := time.Date(now.Year(), now.Month(), now.Day(), h, mi, 0, 0, time.UTC).Add(time.Duration(to-from) * time.Hour)
			return []OmniboxItem{{ID: "utility:date", Title: t.Format("15:04"), Subtitle: z[1] + " " + strings.ToUpper(z[2]) + " → " + strings.ToUpper(z[3]), Icon: "calendar.badge.clock", Kind: ItemUtility, UtilityValue: t.Format("15:04"), Score: 1300}}
		}
	}
	m := dateMathRE.FindStringSubmatch(q)
	if m == nil {
		return nil
	}
	t := now
	if strings.EqualFold(m[1], "today") {
		t = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
	sign := 1
	if m[2] == "-" {
		sign = -1
	}
	days := parseInt(m[3])*sign + parseInt(m[5])*7*sign
	t = t.AddDate(0, 0, days).Add(time.Duration(sign*parseInt(m[4])) * time.Hour)
	if m[1] != "now" && m[1] != "today" {
		parts := strings.Split(m[1], ":")
		h, _ := strconv.Atoi(parts[0])
		mi, _ := strconv.Atoi(parts[1])
		t = time.Date(now.Year(), now.Month(), now.Day(), h, mi, 0, 0, time.UTC)
	}
	if zone := strings.ToLower(m[6]); zone != "" {
		if off, ok := tzOffsets[zone]; ok {
			t = t.Add(time.Duration(off) * time.Hour)
		}
	}
	return []OmniboxItem{{ID: "utility:date", Title: t.Format(time.RFC3339), Subtitle: "date/time", Icon: "calendar.badge.clock", Kind: ItemUtility, UtilityValue: t.Format(time.RFC3339), Score: 1300}}
}
func parseInt(s string) int { n, _ := strconv.Atoi(s); return n }

func evaluateColor(q string) []OmniboxItem {
	c, ok := parseColor(strings.TrimSpace(q))
	if !ok {
		return nil
	}
	hex := fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2])
	rgb := fmt.Sprintf("rgb(%d, %d, %d)", c[0], c[1], c[2])
	hsl := "hsl(" + colorHSL(c) + ")"
	items := []OmniboxItem{
		{ID: "utility:color:hex:" + hex, Title: hex, Subtitle: "HEX", Icon: "swatch:" + hex, Kind: ItemUtility, UtilityValue: hex, Score: 1300},
		{ID: "utility:color:rgb:" + hex, Title: rgb, Subtitle: "RGB", Icon: "swatch:" + hex, Kind: ItemUtility, UtilityValue: rgb, Score: 1290},
		{ID: "utility:color:hsl:" + hex, Title: hsl, Subtitle: "HSL", Icon: "swatch:" + hex, Kind: ItemUtility, UtilityValue: hsl, Score: 1280},
		{ID: "utility:color:oklch:" + hex, Title: "oklch(" + colorOKLCH(c) + ")", Subtitle: "OKLCH", Icon: "swatch:" + hex, Kind: ItemUtility, UtilityValue: "oklch(" + colorOKLCH(c) + ")", Score: 1270},
	}
	return items
}
func parseColor(s string) ([3]int, bool) {
	var c [3]int
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasPrefix(s, "oklch(") {
		var l, chroma, hue float64
		if _, err := fmt.Sscanf(s, "oklch(%f %f %f)", &l, &chroma, &hue); err == nil {
			// Approximate OKLCH -> sRGB conversion, clamped for display.
			angle := hue * math.Pi / 180
			a, bb := chroma*math.Cos(angle), chroma*math.Sin(angle)
			ll := l
			L := ll + 0.3963377774*a + 0.2158037573*bb
			M := ll - 0.1055613458*a - 0.0638541728*bb
			S := ll - 0.0894841775*a - 1.291485548*bb
			l3, m3, s3 := L*L*L, M*M*M, S*S*S
			r := 4.0767416621*l3 - 3.3077115913*m3 + 0.2309699292*s3
			g := -1.2684380046*l3 + 2.6097574011*m3 - 0.3413193965*s3
			b := -0.0041960863*l3 - 0.7034186147*m3 + 1.707614701*s3
			clamp := func(x float64) int {
				if x < 0 {
					x = 0
				}
				if x > 1 {
					x = 1
				}
				return int(x*255 + .5)
			}
			return [3]int{clamp(r), clamp(g), clamp(b)}, true
		}
	}

	if strings.HasPrefix(s, "#") {
		h := strings.TrimPrefix(s, "#")
		if len(h) == 3 {
			h = "" + string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) != 6 && len(h) != 8 {
			return c, false
		}
		for i := 0; i < 3; i++ {
			n, e := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
			if e != nil {
				return c, false
			}
			c[i] = int(n)
		}
		return c, true
	}
	if strings.HasPrefix(s, "rgb(") {
		var a, b, d int
		if _, e := fmt.Sscanf(s, "rgb(%d,%d,%d)", &a, &b, &d); e == nil {
			c = [3]int{a, b, d}
			return c, true
		}
	}
	if strings.HasPrefix(s, "hsl(") {
		var h, sp, l float64
		if _, e := fmt.Sscanf(s, "hsl(%f,%f%%,%f%%)", &h, &sp, &l); e == nil {
			return hslRGB(h, sp, l), true
		}
	}
	return c, false
}
func hslRGB(h, s, l float64) [3]int {
	h = math.Mod(h, 360) / 360
	s /= 100
	l /= 100
	var r, g, b float64
	if s == 0 {
		r = l
		g = l
		b = l
	} else {
		f := func(p, q, t float64) float64 {
			if t < 0 {
				t++
			}
			if t > 1 {
				t--
			}
			if t < 1.0/6 {
				return p + (q-p)*6*t
			}
			if t < .5 {
				return q
			}
			if t < 2.0/3 {
				return p + (q-p)*(2.0/3-t)*6
			}
			return p
		}
		q := l * (1 + s)
		if l >= .5 {
			q = l + s - l*s
		}
		p := 2*l - q
		r = f(p, q, h+1.0/3)
		g = f(p, q, h)
		b = f(p, q, h-1.0/3)
	}
	return [3]int{int(r*255 + .5), int(g*255 + .5), int(b*255 + .5)}
}
func colorOKLCH(c [3]int) string {
	r, g, b := float64(c[0])/255, float64(c[1])/255, float64(c[2])/255
	l := 0.5 * (r + g + b)
	return fmt.Sprintf("%.3f %.3f %.1f", l, 0.0, 0.0)
}

func colorHSL(c [3]int) string {
	r, g, b := float64(c[0])/255, float64(c[1])/255, float64(c[2])/255
	max, min := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l := (max + min) / 2
	if max == min {
		return fmt.Sprintf("0°, 0%%, %d%%", int(l*100+.5))
	}
	d := max - min
	s := d / (1 - math.Abs(2*l-1))
	var h float64
	switch max {
	case r:
		h = 60 * math.Mod((g-b)/d, 6)
	case g:
		h = 60 * ((b-r)/d + 2)
	case b:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return fmt.Sprintf("%d°, %d%%, %d%%", int(h+.5), int(s*100+.5), int(l*100+.5))
}

var functionRE = regexp.MustCompile(`(?i)^\s*(sqrt|sin|cos|abs|round|log|pow)\s*\(([^()]*)\)\s*$`)

func evaluateFunctionExpression(s string) (float64, bool) {
	m := functionRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	parts := strings.Split(m[2], ",")
	args := make([]float64, len(parts))
	for i, p := range parts {
		v, ok := EvaluateMathExpression("=" + strings.TrimSpace(p))
		if !ok {
			return 0, false
		}
		args[i] = v
	}
	switch strings.ToLower(m[1]) {
	case "sqrt":
		if len(args) == 1 && args[0] >= 0 {
			return math.Sqrt(args[0]), true
		}
	case "sin":
		if len(args) == 1 {
			return math.Sin(args[0]), true
		}
	case "cos":
		if len(args) == 1 {
			return math.Cos(args[0]), true
		}
	case "abs":
		if len(args) == 1 {
			return math.Abs(args[0]), true
		}
	case "round":
		if len(args) == 1 {
			return math.Round(args[0]), true
		}
	case "log":
		if len(args) == 1 && args[0] > 0 {
			return math.Log(args[0]), true
		}
	case "pow":
		if len(args) == 2 {
			return math.Pow(args[0], args[1]), true
		}
	}
	return 0, false
}
