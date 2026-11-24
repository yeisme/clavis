package engine

import (
	"fmt"
	"strings"
	"unicode"
)

// TranslateDirection describes auto-detected bilingual translation flow.
type TranslateDirection string

const (
	TranslateZhToEn   TranslateDirection = "zh->en"
	TranslateEnToZh   TranslateDirection = "en->zh"
	TranslateAutoToZh TranslateDirection = "auto->zh"
)

// DetectTranslateDirection inspects Unicode scripts to choose zh->en vs en->zh.
func DetectTranslateDirection(text string) TranslateDirection {
	hasHan := false
	hasLatin := false
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			hasHan = true
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLatin = true
		}
	}
	if hasHan {
		return TranslateZhToEn
	}
	if hasLatin {
		return TranslateEnToZh
	}
	return TranslateAutoToZh
}

// NormalizeTranslateTarget canonicalizes user language tags like "en", "zh", "中文".
func NormalizeTranslateTarget(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "en", "eng", "english", "英", "英文", "英语":
		return "English"
	case "zh", "zh-cn", "cn", "chinese", "中", "中文", "简体中文":
		return "Simplified Chinese"
	case "zh-tw", "繁体", "繁体中文":
		return "Traditional Chinese"
	case "ja", "jp", "japanese", "日", "日文", "日语":
		return "Japanese"
	case "ko", "kr", "korean", "韩", "韩文", "韩语":
		return "Korean"
	default:
		return strings.TrimSpace(raw)
	}
}

// BuildTranslatePrompt returns strict bilingual translation system & user instructions
// that preserve code, identifiers, URLs, and Markdown formatting.
func BuildTranslatePrompt(text string, dir TranslateDirection) (systemPrompt, instruction string) {
	if dir == "" {
		dir = DetectTranslateDirection(text)
	}
	targetLang := "Simplified Chinese"
	switch dir {
	case TranslateZhToEn:
		targetLang = "natural, idiomatic English"
	case TranslateEnToZh:
		targetLang = "natural, fluent Simplified Chinese"
	default:
		targetLang = "Simplified Chinese (or English if input is already Chinese)"
	}
	systemPrompt = fmt.Sprintf(
		"You are a precise technical bilingual translator. Translate the user's text into %s. "+
			"Preserve code blocks, inline `code`, symbol identifiers, file paths, URLs, numbers, and Markdown structure verbatim. "+
			"Return the translated Markdown directly without conversational filler.",
		targetLang,
	)
	instruction = "Translate into " + targetLang
	return systemPrompt, instruction
}

// ParseTranslateQuery detects quick translation triggers: `tr `, `fy `, `翻译 `, or `translate `.
func ParseTranslateQuery(query string) (text string, matched bool) {
	trimmed := strings.TrimSpace(NormalizeFullWidth(query))
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	for _, exact := range []string{"tr", "fy", "翻译", "translate"} {
		if lower == exact {
			return "", true
		}
	}
	for _, prefix := range []string{"tr ", "tr:", "fy ", "fy:", "翻译 ", "翻译:", "translate ", "translate:"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):]), true
		}
	}
	return "", false
}

// MakeTranslateOmniboxItem builds a bilingual translation AI item when query matches.
func MakeTranslateOmniboxItem(query string, ctx CapsuleContext) (OmniboxItem, bool) {
	text, matched := ParseTranslateQuery(query)
	if !matched {
		return OmniboxItem{}, false
	}
	if text == "" {
		text = strings.TrimSpace(ctx.SelectedText)
	}
	if text == "" {
		text = strings.TrimSpace(ctx.ClipboardPreview)
	}
	dir := DetectTranslateDirection(text)
	_, instruction := BuildTranslatePrompt(text, dir)
	dirLabel := string(dir)
	switch dir {
	case TranslateZhToEn:
		dirLabel = "中文 → English"
	case TranslateEnToZh:
		dirLabel = "English → 中文"
	default:
		dirLabel = "双语互译"
	}
	title := "Translate (" + dirLabel + ")"
	if text != "" {
		preview := text
		if len([]rune(preview)) > 40 {
			preview = string([]rune(preview)[:40]) + "…"
		}
		title = fmt.Sprintf("翻译 (%s): %s", dirLabel, preview)
	}
	return OmniboxItem{
		ID:           "ai:translate:" + string(dir),
		Title:        title,
		Subtitle:     "保留代码与 Markdown 格式，由已配置 AI Profile 翻译",
		KeyHint:      "↵",
		Icon:         "character.book.closed",
		Keywords:     []string{"translate", "tr", "fy", "翻译", "双语", "en", "zh"},
		Score:        1350,
		Kind:         ItemAIMode,
		AIMode:       "translate",
		AIPrompt:     instruction,
		AISourceText: text,
	}, true
}
