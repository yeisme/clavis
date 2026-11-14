package uiapp

import (
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// cjkFontFamily is the CJK fallback chain the text engine walks when the UI
// language is Chinese: the platform UI font first (so Latin and digits keep
// the native look), then the platform's Han face — Microsoft YaHei UI on
// Windows, PingFang SC on macOS, Noto Sans CJK SC on Linux — before the
// system's own choice. The first family present draws; the others supply
// what it lacks.
const cjkFontFamily = "system-ui, Microsoft YaHei UI, PingFang SC, Noto Sans CJK SC"

// applyTextLocale localizes the typography of a view root: under Chinese it
// selects the CJK font fallback chain and tags the text "zh" so the shaper
// picks Simplified-Chinese Han glyph variants and breaks lines correctly.
// Font family and language inherit, so setting them once at the root styles
// the whole tree. It returns the same element for chaining.
func applyTextLocale(el ui.Element) ui.Element {
	if !i18n.IsChinese() {
		return el
	}
	return el.Font(cjkFontFamily).Lang("zh")
}

// cjkProseLineHeight is the extra leading Chinese prose reads best at; the
// toolkit's default suits Latin UI text, so only prose areas (AI output,
// snippet content, validation text) opt in.
const cjkProseLineHeight = 1.6

// applyProseLineHeight gives prose text (AI output, preview lines,
// diagnostics) the wider Chinese leading; single-line labels keep the
// default. It returns the same element for chaining. Both helpers live in
// package uiapp alongside the views that use them.
func applyProseLineHeight(el ui.Element) ui.Element {
	if !i18n.IsChinese() {
		return el
	}
	return el.LineHeight(cjkProseLineHeight)
}
