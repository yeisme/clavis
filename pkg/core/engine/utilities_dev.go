package engine

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func evaluateDevUtility(trimmed string, _ time.Time) []OmniboxItem {
	lower := strings.ToLower(trimmed)

	// 1. Cryptographic password generator (`pw`, `pw <len>`, `pass [len]`) — always sensitive TTL.
	if lower == "pw" || lower == "pass" || strings.HasPrefix(lower, "pw ") || strings.HasPrefix(lower, "pass ") {
		if items := evalPasswordUtility(trimmed); len(items) > 0 {
			return items
		}
	}

	// 2. URL encode / decode (`url <text>`, `urld <text>`).
	if strings.HasPrefix(lower, "urld ") {
		return evalURLDecodeUtility(strings.TrimSpace(trimmed[len("urld "):]))
	}
	if strings.HasPrefix(lower, "url ") {
		return evalURLEncodeUtility(strings.TrimSpace(trimmed[len("url "):]))
	}

	// 3. Hash digests (`hash <text>`, `sha256 <text>`, `md5 <text>`).
	if strings.HasPrefix(lower, "hash ") {
		return evalHashUtility(strings.TrimSpace(trimmed[len("hash "):]), true, true)
	}
	if strings.HasPrefix(lower, "sha256 ") {
		return evalHashUtility(strings.TrimSpace(trimmed[len("sha256 "):]), true, false)
	}
	if strings.HasPrefix(lower, "md5 ") {
		return evalHashUtility(strings.TrimSpace(trimmed[len("md5 "):]), false, true)
	}

	// 4. Case conversions (`case <text>`).
	if strings.HasPrefix(lower, "case ") {
		return evalCaseUtility(strings.TrimSpace(trimmed[len("case "):]))
	}

	// 5. Linear-time RE2 regexp tester (`re /pattern/[i] text`).
	if strings.HasPrefix(lower, "re ") {
		return evalRegexUtility(strings.TrimSpace(trimmed[len("re "):]))
	}

	// 6. JWT payload decoder (`jwt <token>` or bare 3-part `eyJ...` token).
	if strings.HasPrefix(lower, "jwt ") {
		return evalJWTUtility(strings.TrimSpace(trimmed[len("jwt "):]))
	}
	if strings.HasPrefix(trimmed, "eyJ") && strings.Count(trimmed, ".") == 2 {
		if items := evalJWTUtility(trimmed); len(items) > 0 {
			return items
		}
	}

	// 7. JSON pretty / minify (`json <payload>` or bare valid JSON object/array).
	if strings.HasPrefix(lower, "json ") {
		return evalJSONUtility(strings.TrimSpace(trimmed[len("json "):]))
	}
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		if len(trimmed) > 2 && json.Valid([]byte(trimmed)) {
			return evalJSONUtility(trimmed)
		}
	}

	return nil
}

func evalPasswordUtility(trimmed string) []OmniboxItem {
	fields := strings.Fields(trimmed)
	length := 16
	if len(fields) >= 2 {
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil
		}
		if n < 8 {
			n = 8
		} else if n > 64 {
			n = 64
		}
		length = n
	}
	pw, err := generateSecurePassword(length)
	if err != nil {
		return nil
	}
	mask := strings.Repeat("•", min(length, 16))
	return []OmniboxItem{{
		ID:               fmt.Sprintf("utility:pw:%d", length),
		Title:            fmt.Sprintf("Password (%d chars): %s", length, mask),
		Subtitle:         "User-Only TTL Copy · Not recorded in clipboard history",
		KeyHint:          "↵",
		Icon:             "lock.rotation",
		Kind:             ItemUtility,
		UtilityValue:     pw,
		UtilitySensitive: true,
		Score:            1350,
	}}
}

func generateSecurePassword(length int) (string, error) {
	const (
		lower  = "abcdefghijklmnopqrstuvwxyz"
		upper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		digits = "0123456789"
		syms   = "!@#$%^&*-_+="
		all    = lower + upper + digits + syms
	)
	classes := []string{lower, upper, digits, syms}
	buf := make([]byte, length)
	for i := 0; i < len(classes) && i < length; i++ {
		ch, err := randChar(classes[i])
		if err != nil {
			return "", err
		}
		buf[i] = ch
	}
	for i := len(classes); i < length; i++ {
		ch, err := randChar(all)
		if err != nil {
			return "", err
		}
		buf[i] = ch
	}
	// Fisher-Yates shuffle with crypto/rand.
	for i := length - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		j := int(n.Int64())
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf), nil
}

func randChar(charset string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
	if err != nil {
		return 0, err
	}
	return charset[int(n.Int64())], nil
}

func evalJSONUtility(raw string) []OmniboxItem {
	if raw == "" {
		return nil
	}
	data := []byte(raw)
	if !json.Valid(data) {
		return []OmniboxItem{{
			ID:           "utility:json:invalid",
			Title:        "Invalid JSON",
			Subtitle:     "Check syntax (quotes, commas, brackets)",
			Icon:         "info.circle",
			Kind:         ItemUtility,
			UtilityValue: raw,
			Score:        1200,
		}}
	}
	var pretty, compact bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") != nil {
		return nil
	}
	prettyStr := pretty.String()
	items := []OmniboxItem{{
		ID:           "utility:json:pretty",
		Title:        "Pretty JSON (" + strconv.Itoa(strings.Count(prettyStr, "\n")+1) + " lines)",
		Subtitle:     previewSnippet(prettyStr, 72),
		KeyHint:      "↵",
		Icon:         "curlybraces",
		Kind:         ItemUtility,
		UtilityValue: prettyStr,
		Score:        1310,
	}}
	if json.Compact(&compact, data) == nil {
		minStr := compact.String()
		items = append(items, OmniboxItem{
			ID:           "utility:json:compact",
			Title:        previewSnippet(minStr, 72),
			Subtitle:     fmt.Sprintf("Minified JSON (%d bytes)", len(minStr)),
			KeyHint:      "↵",
			Icon:         "curlybraces",
			Kind:         ItemUtility,
			UtilityValue: minStr,
			Score:        1300,
		})
	}
	return items
}

func evalURLEncodeUtility(raw string) []OmniboxItem {
	if raw == "" {
		return nil
	}
	q := url.QueryEscape(raw)
	p := url.PathEscape(raw)
	items := []OmniboxItem{{
		ID:           "utility:url:query",
		Title:        q,
		Subtitle:     "URL Query Encode",
		KeyHint:      "↵",
		Icon:         "link",
		Kind:         ItemUtility,
		UtilityValue: q,
		Score:        1300,
	}}
	if p != q {
		items = append(items, OmniboxItem{
			ID:           "utility:url:path",
			Title:        p,
			Subtitle:     "URL Path Encode (%20 spaces)",
			KeyHint:      "↵",
			Icon:         "link",
			Kind:         ItemUtility,
			UtilityValue: p,
			Score:        1290,
		})
	}
	return items
}

func evalURLDecodeUtility(raw string) []OmniboxItem {
	if raw == "" {
		return nil
	}
	dec, err := url.QueryUnescape(raw)
	if err != nil {
		dec, err = url.PathUnescape(raw)
	}
	if err != nil {
		return []OmniboxItem{{
			ID:           "utility:urld:invalid",
			Title:        "Invalid URL encoding",
			Subtitle:     err.Error(),
			Icon:         "info.circle",
			Kind:         ItemUtility,
			UtilityValue: raw,
			Score:        1200,
		}}
	}
	return []OmniboxItem{{
		ID:           "utility:urld:ok",
		Title:        dec,
		Subtitle:     "URL Decoded",
		KeyHint:      "↵",
		Icon:         "link",
		Kind:         ItemUtility,
		UtilityValue: dec,
		Score:        1300,
	}}
}

func evalJWTUtility(token string) []OmniboxItem {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil
	}
	headerBytes, errH := decodeJWTPart(parts[0])
	payloadBytes, errP := decodeJWTPart(parts[1])
	if errH != nil || errP != nil || !json.Valid(headerBytes) || !json.Valid(payloadBytes) {
		return nil
	}
	var prettyHeader, prettyPayload bytes.Buffer
	_ = json.Indent(&prettyHeader, headerBytes, "", "  ")
	_ = json.Indent(&prettyPayload, payloadBytes, "", "  ")

	var claims map[string]any
	_ = json.Unmarshal(payloadBytes, &claims)
	sub := "JWT Claims (signature unverified)"
	if exp, ok := claims["exp"].(float64); ok && exp > 0 {
		expAt := time.Unix(int64(exp), 0).UTC()
		state := "valid"
		if expAt.Before(time.Now().UTC()) {
			state = "expired"
		}
		sub = fmt.Sprintf("JWT Claims (unverified · exp %s · %s)", expAt.Format("2006-01-02 15:04Z"), state)
	}
	combined := prettyPayload.String()
	return []OmniboxItem{
		{
			ID:           "utility:jwt:payload",
			Title:        previewSnippet(combined, 72),
			Subtitle:     sub,
			KeyHint:      "↵",
			Icon:         "key.horizontal",
			Kind:         ItemUtility,
			UtilityValue: combined,
			Score:        1320,
		},
		{
			ID:           "utility:jwt:header",
			Title:        previewSnippet(prettyHeader.String(), 72),
			Subtitle:     "JWT Header",
			KeyHint:      "↵",
			Icon:         "key.horizontal",
			Kind:         ItemUtility,
			UtilityValue: prettyHeader.String(),
			Score:        1310,
		},
	}
}

func decodeJWTPart(seg string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(seg); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(seg)
}

func evalHashUtility(raw string, wantSHA256, wantMD5 bool) []OmniboxItem {
	if raw == "" {
		return nil
	}
	var out []OmniboxItem
	if wantSHA256 {
		sum := sha256.Sum256([]byte(raw))
		hexStr := hex.EncodeToString(sum[:])
		out = append(out, OmniboxItem{
			ID:           "utility:hash:sha256",
			Title:        hexStr,
			Subtitle:     "SHA-256",
			KeyHint:      "↵",
			Icon:         "number",
			Kind:         ItemUtility,
			UtilityValue: hexStr,
			Score:        1310,
		})
	}
	if wantMD5 {
		sum := md5.Sum([]byte(raw))
		hexStr := hex.EncodeToString(sum[:])
		out = append(out, OmniboxItem{
			ID:           "utility:hash:md5",
			Title:        hexStr,
			Subtitle:     "MD5",
			KeyHint:      "↵",
			Icon:         "number",
			Kind:         ItemUtility,
			UtilityValue: hexStr,
			Score:        1300,
		})
	}
	return out
}

func evalCaseUtility(raw string) []OmniboxItem {
	words := splitIdentifierWords(raw)
	if len(words) == 0 {
		return nil
	}
	lowerWords := make([]string, len(words))
	titleWords := make([]string, len(words))
	upperWords := make([]string, len(words))
	for i, w := range words {
		lw := strings.ToLower(w)
		lowerWords[i] = lw
		upperWords[i] = strings.ToUpper(w)
		r := []rune(lw)
		r[0] = unicode.ToUpper(r[0])
		titleWords[i] = string(r)
	}
	camel := lowerWords[0] + strings.Join(titleWords[1:], "")
	pascal := strings.Join(titleWords, "")
	snake := strings.Join(lowerWords, "_")
	kebab := strings.Join(lowerWords, "-")
	upperSnake := strings.Join(upperWords, "_")
	return []OmniboxItem{
		{ID: "utility:case:camel", Title: camel, Subtitle: "camelCase", KeyHint: "↵", Icon: "textformat", Kind: ItemUtility, UtilityValue: camel, Score: 1320},
		{ID: "utility:case:pascal", Title: pascal, Subtitle: "PascalCase", KeyHint: "↵", Icon: "textformat", Kind: ItemUtility, UtilityValue: pascal, Score: 1310},
		{ID: "utility:case:snake", Title: snake, Subtitle: "snake_case", KeyHint: "↵", Icon: "textformat", Kind: ItemUtility, UtilityValue: snake, Score: 1300},
		{ID: "utility:case:kebab", Title: kebab, Subtitle: "kebab-case", KeyHint: "↵", Icon: "textformat", Kind: ItemUtility, UtilityValue: kebab, Score: 1290},
		{ID: "utility:case:const", Title: upperSnake, Subtitle: "UPPER_SNAKE", KeyHint: "↵", Icon: "textformat", Kind: ItemUtility, UtilityValue: upperSnake, Score: 1280},
	}
}

func splitIdentifierWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		if r == '_' || r == '-' || r == '.' || r == '/' || unicode.IsSpace(r) {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) && len(cur) > 0 {
			prev := cur[len(cur)-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

func evalRegexUtility(rest string) []OmniboxItem {
	if !strings.HasPrefix(rest, "/") {
		return nil
	}
	// Find unescaped closing '/'.
	end := -1
	escaped := false
	for i := 1; i < len(rest); i++ {
		if escaped {
			escaped = false
			continue
		}
		if rest[i] == '\\' {
			escaped = true
			continue
		}
		if rest[i] == '/' {
			end = i
			break
		}
	}
	if end <= 1 {
		return nil
	}
	pattern := rest[1:end]
	tail := rest[end+1:]
	flags := ""
	if sp := strings.IndexAny(tail, " \t"); sp >= 0 {
		flags = tail[:sp]
		tail = strings.TrimSpace(tail[sp+1:])
	} else {
		flags = tail
		tail = ""
	}
	if strings.Contains(flags, "i") {
		pattern = "(?i)" + pattern
	}
	if strings.Contains(flags, "m") {
		pattern = "(?m)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return []OmniboxItem{{
			ID:           "utility:re:error",
			Title:        "Invalid RE2 Regexp",
			Subtitle:     err.Error(),
			Icon:         "info.circle",
			Kind:         ItemUtility,
			UtilityValue: pattern,
			Score:        1200,
		}}
	}
	if tail == "" {
		return []OmniboxItem{{
			ID:           "utility:re:ready",
			Title:        "RE2 pattern compiled — type input text after closing /",
			Subtitle:     "/" + rest[1:end] + "/" + flags,
			Icon:         "magnifyingglass",
			Kind:         ItemUtility,
			UtilityValue: pattern,
			Score:        1250,
		}}
	}
	matches := re.FindAllStringSubmatch(tail, 50)
	if len(matches) == 0 {
		return []OmniboxItem{{
			ID:           "utility:re:nomatch",
			Title:        "0 matches",
			Subtitle:     "/" + rest[1:end] + "/" + flags,
			Icon:         "magnifyingglass",
			Kind:         ItemUtility,
			UtilityValue: "",
			Score:        1280,
		}}
	}
	first := matches[0][0]
	var flat []string
	for _, m := range matches {
		flat = append(flat, m[0])
	}
	allJoined := strings.Join(flat, "\n")
	sub := fmt.Sprintf("%d match(es) · %d group(s)", len(matches), len(matches[0])-1)
	if len(matches[0]) > 1 {
		sub += " · group[1]=" + matches[0][1]
	}
	return []OmniboxItem{{
		ID:           "utility:re:match",
		Title:        first,
		Subtitle:     sub,
		KeyHint:      "↵",
		Icon:         "magnifyingglass",
		Kind:         ItemUtility,
		UtilityValue: allJoined,
		Score:        1320,
	}}
}

func previewSnippet(s string, maxRunes int) string {
	oneLine := strings.Join(strings.Fields(s), " ")
	r := []rune(oneLine)
	if len(r) <= maxRunes {
		return oneLine
	}
	return string(r[:maxRunes]) + "…"
}
