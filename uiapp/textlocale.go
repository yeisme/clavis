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

// mixColor linearly blends color a to b by t (0 keeps a, 1 is b), alpha
// included; used by the snippet flash highlight.
func mixColor(a, b ui.Color, t float32) ui.Color {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return ui.Color{
		R: uint8(float32(a.R) + (float32(b.R)-float32(a.R))*t),
		G: uint8(float32(a.G) + (float32(b.G)-float32(a.G))*t),
		B: uint8(float32(a.B) + (float32(b.B)-float32(a.B))*t),
		A: uint8(float32(a.A) + (float32(b.A)-float32(a.A))*t),
	}
}
