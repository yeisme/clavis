package uiapp

import (
	"image"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// macOSTestTheme is the macOS grayscale palette the app is moving to, on
// MyGo's light and dark themes. The syntax colors are held to it as well as to
// MyGo's own zinc defaults, so the palette swap cannot break their contrast.
func macOSTestTheme(dark bool) *ui.Theme {
	if dark {
		th := ui.DarkTheme()
		th.Background, th.Surface = ui.Hex("#1C1C1E"), ui.Hex("#2C2C2E")
		th.Text, th.TextMuted = ui.Hex("#F5F5F7"), ui.Hex("#A1A1A6")
		th.Accent = ui.Hex("#0A84FF")
		return th
	}
	th := ui.LightTheme()
	th.Background, th.Surface = ui.Hex("#FFFFFF"), ui.Hex("#F5F5F7")
	th.Text, th.TextMuted = ui.Hex("#1D1D1F"), ui.Hex("#6E6E73")
	th.Accent = ui.Hex("#0A84FF")
	return th
}

func codeTestThemes() map[string]*ui.Theme {
	return map[string]*ui.Theme{
		"zinc-light":  ui.LightTheme(),
		"zinc-dark":   ui.DarkTheme(),
		"macos-light": macOSTestTheme(false),
		"macos-dark":  macOSTestTheme(true),
	}
}

// TestXcodeCodeThemeReadsOnTheWell holds every token color to the 4.5:1 body
// text contrast on the opaque code well it is drawn on, in each palette.
func TestXcodeCodeThemeReadsOnTheWell(t *testing.T) {
	for name, th := range codeTestThemes() {
		ct := xcodeCodeTheme(th)
		well := codeWellFill(th)
		if ct.Background != well {
			t.Errorf("%s: code theme background %v is not the code well %v", name, ct.Background, well)
		}
		for k := editor.TokenText; k <= editor.TokenInvalid; k++ {
			if got := contrastRatio(ct.TokenColor(k), well); got < minTextContrast {
				t.Errorf("%s: token kind %d reads at %.2f:1 on the code well, want >= %.1f", name, k, got, minTextContrast)
			}
		}
	}
}

// TestXcodeCodeThemeKeepsXcodeColors pins the palette to Xcode 12+'s Default
// themes: where a color already reads on the well it is Xcode's, to the digit.
// Xcode's dark comment gray does not read at 4.5:1 on the dark well, and is the
// only one blended toward the text color, in the smallest 10% step that makes
// it legible.
func TestXcodeCodeThemeKeepsXcodeColors(t *testing.T) {
	type role struct {
		kinds []editor.TokenKind
		hex   string
	}
	roles := func(dark bool) []role {
		pick := func(light, dk string) string {
			if dark {
				return dk
			}
			return light
		}
		return []role{
			{[]editor.TokenKind{editor.TokenComment}, pick("#5D6C79", "#6C7986")},
			{[]editor.TokenKind{editor.TokenString, editor.TokenRegexp, editor.TokenCode}, pick("#C41A16", "#FC6A5D")},
			{[]editor.TokenKind{editor.TokenNumber, editor.TokenEscape}, pick("#1C00CF", "#D0BF69")},
			{[]editor.TokenKind{editor.TokenKeyword, editor.TokenConstant, editor.TokenTag, editor.TokenHeading}, pick("#9B2393", "#FC5FA3")},
			{[]editor.TokenKind{editor.TokenMeta}, pick("#643820", "#FD8F3F")},
			{[]editor.TokenKind{editor.TokenLink}, pick("#0E0EFF", "#5482FF")},
			{[]editor.TokenKind{editor.TokenAttribute}, pick("#815F03", "#BF8555")},
			{[]editor.TokenKind{editor.TokenFunction, editor.TokenProperty}, pick("#326D74", "#67B7A4")},
			{[]editor.TokenKind{editor.TokenType}, pick("#3900A0", "#D0A8FF")},
		}
	}
	for _, dark := range []bool{false, true} {
		th := macOSTestTheme(dark)
		well := codeWellFill(th)
		ct := xcodeCodeTheme(th)
		for _, r := range roles(dark) {
			want := ui.Hex(r.hex)
			for _, k := range r.kinds {
				got := ct.TokenColor(k)
				if contrastRatio(want, well) >= minTextContrast {
					if got != want {
						t.Errorf("dark=%v kind %d = %v, want Xcode's %s", dark, k, got, r.hex)
					}
					continue
				}
				// Not legible as Xcode has it: the first 10% step toward the text
				// color that is, and none before it.
				step := -1
				for s := 1; s <= 10; s++ {
					if want.Mix(th.Text, float32(s)/10) == got {
						step = s
						break
					}
				}
				if step < 1 || step > 4 {
					t.Errorf("dark=%v kind %d = %v: not a small blend of %s toward the text color", dark, k, got, r.hex)
					continue
				}
				if contrastRatio(want.Mix(th.Text, float32(step-1)/10), well) >= minTextContrast {
					t.Errorf("dark=%v kind %d was blended %d0%%, but %d0%% already reads", dark, k, step, step-1)
				}
			}
		}
	}
}

// TestXcodeCodeThemeNudgesOnlyWhatFails documents which Xcode color needs the
// nudge on the macOS dark well, so a palette change that moves it is noticed:
// only the comment gray, about 3.8:1 on it (Xcode's own dark editor is a little
// lighter than this well, and reads it at 3.7:1 too).
func TestXcodeCodeThemeNudgesOnlyWhatFails(t *testing.T) {
	th := macOSTestTheme(true)
	well := codeWellFill(th)
	var failing []string
	for name, hex := range map[string]string{
		"comments": "#6C7986", "strings": "#FC6A5D", "numbers": "#D0BF69", "keywords": "#FC5FA3",
		"preprocessor": "#FD8F3F", "urls": "#5482FF", "attributes": "#BF8555", "functions": "#67B7A4", "types": "#D0A8FF",
	} {
		if contrastRatio(ui.Hex(hex), well) < minTextContrast {
			failing = append(failing, name)
		}
	}
	if len(failing) != 1 || failing[0] != "comments" {
		t.Fatalf("on the dark code well only Xcode's comment gray should be below %.1f:1, got %v", minTextContrast, failing)
	}
	// And the light well passes every Xcode color untouched.
	light := macOSTestTheme(false)
	lightWell := codeWellFill(light)
	for _, hex := range []string{"#5D6C79", "#C41A16", "#1C00CF", "#9B2393", "#643820", "#0E0EFF", "#815F03", "#326D74", "#3900A0"} {
		if got := contrastRatio(ui.Hex(hex), lightWell); got < minTextContrast {
			t.Errorf("Xcode's light %s reads at %.2f:1 on the light well", hex, got)
		}
	}
}

// TestXcodeCodeThemeStyles covers the token styles: semibold keywords and
// headings, constants and tags in the keyword color without the weight, no
// italic comments, and operators, punctuation and variables as plain text.
func TestXcodeCodeThemeStyles(t *testing.T) {
	for name, th := range codeTestThemes() {
		ct := xcodeCodeTheme(th)
		for _, k := range []editor.TokenKind{editor.TokenKeyword, editor.TokenHeading, editor.TokenStrong} {
			if !ct.Tokens[k].Bold {
				t.Errorf("%s: kind %d should be bold", name, k)
			}
		}
		for k := editor.TokenText; k <= editor.TokenInvalid; k++ {
			s := ct.Tokens[k]
			switch k {
			case editor.TokenKeyword, editor.TokenHeading, editor.TokenStrong:
			default:
				if s.Bold {
					t.Errorf("%s: kind %d should not be bold", name, k)
				}
			}
			if s.Italic != (k == editor.TokenEmphasis) {
				t.Errorf("%s: kind %d italic = %v", name, k, s.Italic)
			}
		}
		if ct.Tokens[editor.TokenConstant].Color != ct.Tokens[editor.TokenKeyword].Color {
			t.Errorf("%s: constants should take the keyword color", name)
		}
		for _, k := range []editor.TokenKind{editor.TokenText, editor.TokenVariable, editor.TokenOperator, editor.TokenPunctuation} {
			if got := ct.TokenColor(k); got != th.Text {
				t.Errorf("%s: kind %d = %v, want the theme text %v", name, k, got, th.Text)
			}
		}
	}
}

// TestXcodeCodeThemeIsMemoized: markdown compares a theme's CodeColors by
// pointer to decide whether its highlighted blocks are stale, so equal themes
// must share one result and different ones must not.
func TestXcodeCodeThemeIsMemoized(t *testing.T) {
	if xcodeCodeTheme(macOSTestTheme(false)) != xcodeCodeTheme(macOSTestTheme(false)) {
		t.Fatal("equal themes must share one code theme")
	}
	if xcodeCodeTheme(macOSTestTheme(false)) == xcodeCodeTheme(macOSTestTheme(true)) {
		t.Fatal("light and dark must not share a code theme")
	}
	other := macOSTestTheme(false)
	other.Text = ui.Hex("#000000")
	if xcodeCodeTheme(other) == xcodeCodeTheme(macOSTestTheme(false)) {
		t.Fatal("a different text color must not reuse a code theme")
	}
}

// TestCodeWellIsOpaque: the owner forbids semi-transparent body content, and
// the contrast checks only mean something against a fixed fill.
func TestCodeWellIsOpaque(t *testing.T) {
	for name, th := range codeTestThemes() {
		if got := codeWellFill(th); got.A != 255 {
			t.Errorf("%s: code well alpha = %d, want 255", name, got.A)
		}
		md := buildClavisMarkdownTheme(th)
		if md.CodeBlockBackground != codeWellFill(th) {
			t.Errorf("%s: markdown code blocks must sit on the code well, got %v", name, md.CodeBlockBackground)
		}
		if md.CodeColors != xcodeCodeTheme(th) {
			t.Errorf("%s: markdown code blocks must use the Xcode code colors", name)
		}
	}
}

func spanTexts(spans []ui.Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

func findSpan(t *testing.T, spans []ui.Span, text string) ui.Span {
	t.Helper()
	for _, s := range spans {
		if s.Text == text {
			return s
		}
	}
	t.Fatalf("no span %q in %q", text, spanTexts(spans))
	return ui.Span{}
}

func TestHighlightCodeLinesSQL(t *testing.T) {
	th := macOSTestTheme(false)
	ct := xcodeCodeTheme(th)
	lines := strings.Split(codeSnapSQL, "\n")
	spans := highlightCodeLines(ct, editor.LanguageNamed("sql").Tokenizer, lines)
	if len(spans) != len(lines) {
		t.Fatalf("got %d lines of spans for %d lines", len(spans), len(lines))
	}
	for i, line := range lines {
		if got := spanTexts(spans[i]); got != line {
			t.Errorf("line %d: spans read %q, want %q", i, got, line)
		}
		for j := 1; j < len(spans[i]); j++ {
			a, b := spans[i][j-1], spans[i][j]
			if a.Color == b.Color && a.Weight == b.Weight && a.Italic == b.Italic {
				t.Errorf("line %d: neighbors %q and %q share a style and should have merged", i, a.Text, b.Text)
			}
		}
	}
	kw := findSpan(t, spans[0], "SELECT")
	if kw.Color != ui.Hex("#9B2393") || kw.Weight != 600 {
		t.Errorf("SELECT = %v weight %d, want Xcode's keyword magenta, semibold", kw.Color, kw.Weight)
	}
	if str := findSpan(t, spans[2], "'image'"); str.Color != ui.Hex("#C41A16") || str.Weight != 0 {
		t.Errorf("'image' = %v weight %d, want Xcode's string red, regular", str.Color, str.Weight)
	}
	if num := findSpan(t, spans[4], "20"); num.Color != ui.Hex("#1C00CF") {
		t.Errorf("20 = %v, want Xcode's number blue", num.Color)
	}
	if plain := findSpan(t, spans[2], " kind = "); plain.Color != th.Text {
		t.Errorf("identifiers and operators = %v, want the theme text %v", plain.Color, th.Text)
	}
}

func TestHighlightCodeLinesGoUsesXcodeRoles(t *testing.T) {
	ct := xcodeCodeTheme(macOSTestTheme(false))
	lines := []string{"// note", "func Retry(n int) error {", "\treturn nil"}
	spans := highlightCodeLines(ct, editor.LanguageNamed("go").Tokenizer, lines)
	for _, c := range []struct {
		line int
		text string
		hex  string
		bold bool
	}{
		{0, "// note", "#5D6C79", false},
		{1, "func", "#9B2393", true},
		{1, "Retry", "#326D74", false},
		{1, "int", "#3900A0", false},
		{1, "error", "#3900A0", false},
		{2, "return", "#9B2393", true},
		{2, "nil", "#9B2393", false},
	} {
		sp := findSpan(t, spans[c.line], c.text)
		if sp.Color != ui.Hex(c.hex) || (sp.Weight == 600) != c.bold || sp.Italic {
			t.Errorf("line %d %q = %v weight %d italic %v, want %s bold=%v", c.line, c.text, sp.Color, sp.Weight, sp.Italic, c.hex, c.bold)
		}
	}
}

// TestHighlightCodeLinesCarriesStateAcrossLines: a block comment (and so a
// string) goes on over lines, blank ones included.
func TestHighlightCodeLinesCarriesStateAcrossLines(t *testing.T) {
	ct := xcodeCodeTheme(macOSTestTheme(false))
	lines := []string{"/* start", "middle", "", "end */ x := 1"}
	spans := highlightCodeLines(ct, editor.LanguageNamed("go").Tokenizer, lines)
	comment := ct.Tokens[editor.TokenComment].Color
	if len(spans[1]) != 1 || spans[1][0].Color != comment {
		t.Errorf("a line inside a block comment = %+v, want one comment span", spans[1])
	}
	if spans[2] != nil {
		t.Errorf("a blank line should have no spans, got %+v", spans[2])
	}
	if got := findSpan(t, spans[3], "end */"); got.Color != comment {
		t.Errorf("the comment's last line must start in the comment, got %v", got.Color)
	}
	if got := findSpan(t, spans[3], "1"); got.Color == comment {
		t.Errorf("code after the comment closes must not stay comment-colored")
	}
}

func TestHighlightCodeLinesLeavesLongLinesPlain(t *testing.T) {
	ct := xcodeCodeTheme(macOSTestTheme(false))
	long := strings.Repeat("x", previewCodeMaxLine+1)
	spans := highlightCodeLines(ct, editor.LanguageNamed("go").Tokenizer, []string{long, "func f() {}"})
	if len(spans[0]) != 1 || spans[0][0].Text != long || spans[0][0].Color != ct.Text {
		t.Fatalf("a line past %d bytes must be one plain span", previewCodeMaxLine)
	}
	if findSpan(t, spans[1], "func").Weight != 600 {
		t.Fatal("the line after a long one must still be highlighted")
	}
}

func TestCodeHighlightCache(t *testing.T) {
	cache := newCodeHighlightCache(3)
	light, dark := macOSTestTheme(false), macOSTestTheme(true)
	goLines := []string{"func main() {", "\treturn", "}"}

	first := cache.spans(light, goLines)
	if first == nil || len(first) != len(goLines) {
		t.Fatalf("Go lines must highlight, got %v", first)
	}
	again := cache.spans(light, append([]string(nil), goLines...))
	if hits, misses := cache.stats(); hits != 1 || misses != 1 {
		t.Fatalf("same text again: hits=%d misses=%d, want 1 and 1", hits, misses)
	}
	if &again[0][0] != &first[0][0] {
		t.Fatal("a hit must return the cached spans, not rebuild them")
	}

	// The colors are the theme's: dark is another entry with its own colors.
	darkSpans := cache.spans(dark, goLines)
	if hits, misses := cache.stats(); hits != 1 || misses != 2 {
		t.Fatalf("dark: hits=%d misses=%d, want 1 and 2", hits, misses)
	}
	if kw := findSpan(t, darkSpans[0], "func"); kw.Color != ui.Hex("#FC5FA3") {
		t.Fatalf("dark keyword = %v, want Xcode's dark pink", kw.Color)
	}

	// A text no language claims is remembered as plain: no sniffing per frame.
	prose := []string{"just some words", "and more words"}
	if cache.spans(light, prose) != nil {
		t.Fatal("prose must not highlight")
	}
	cache.spans(light, prose)
	if hits, misses := cache.stats(); hits != 2 || misses != 3 {
		t.Fatalf("prose twice: hits=%d misses=%d, want 2 and 3", hits, misses)
	}

	// The cache is bounded.
	cache.spans(light, []string{"SELECT 1;"})
	if cache.len() > 3 {
		t.Fatalf("cache grew to %d entries, bound is 3", cache.len())
	}
}

// TestCodeHighlightCacheNeverShowsOtherText: two texts of the same shape (line
// count, size, first and last line) share a cache key, and must not share spans.
func TestCodeHighlightCacheNeverShowsOtherText(t *testing.T) {
	cache := newCodeHighlightCache(4)
	th := macOSTestTheme(false)
	a := []string{"x := 1", "aaaa := 2", "z := 3"}
	b := []string{"x := 1", "bbbb := 2", "z := 3"}
	cache.spans(th, a)
	got := cache.spans(th, b)
	if spanTexts(got[1]) != "bbbb := 2" {
		t.Fatalf("a text of the same shape showed %q", spanTexts(got[1]))
	}
}

func TestCodeHighlightCacheSkipsWhatIsTooLarge(t *testing.T) {
	cache := newCodeHighlightCache(2)
	huge := []string{"func main() {", strings.Repeat("// ", previewCodeMaxBytes/3), "}"}
	if cache.spans(macOSTestTheme(false), huge) != nil {
		t.Fatal("a text past previewCodeMaxBytes must stay plain")
	}
	if cache.len() != 0 {
		t.Fatal("a text past previewCodeMaxBytes must not be cached or tokenized")
	}
	if cache.spans(macOSTestTheme(false), nil) != nil || cache.spans(macOSTestTheme(false), []string{""}) != nil {
		t.Fatal("empty text must stay plain")
	}
}

func TestDetectCodeLanguage(t *testing.T) {
	for _, c := range []struct{ name, text, want string }{
		{"sql", codeSnapSQL, "sql"},
		{"sql one line", "SELECT id, name FROM users WHERE id = 1;", "sql"},
		{"sql lower", "select * from users", "sql"},
		{"sql insert", "INSERT INTO t (a) VALUES (1);", "sql"},
		{"sql update", "UPDATE t SET a = 1 WHERE b = 2", "sql"},
		{"sql ddl", "CREATE TABLE users (id INTEGER PRIMARY KEY);", "sql"},
		{"sql cte", "WITH x AS (SELECT 1) SELECT * FROM x", "sql"},
		{"sql after comment", "-- all images\nSELECT id FROM clipboard_items", "sql"},
		{"go", codeSnapGo, "go"},
		{"go package", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}", "go"},
		{"go one line", "func main() {}", "go"},
		{"swift", "import SwiftUI\n\nstruct ContentView: View {\n    var body: some View {\n        Text(\"Hello\")\n    }\n}", "swift"},
		{"swift func", "func greet(name: String) -> String {\n    return \"Hello, \" + name\n}", "swift"},
		{"rust", "fn main() {\n    let mut x = 5;\n    println!(\"{}\", x);\n}", "rust"},
		{"python", "def add(a, b):\n    return a + b\n\nif __name__ == \"__main__\":\n    print(add(1, 2))", "python"},
		{"python imports", "import os\nimport sys\n", "python"},
		{"python from", "from collections import OrderedDict", "python"},
		{"javascript", "const add = (a, b) => a + b;\nconsole.log(add(1, 2));", "javascript"},
		{"javascript function", "function foo() {\n  return 1;\n}", "javascript"},
		{"javascript import", "import React from 'react';", "javascript"},
		{"typescript", "interface User {\n  id: number;\n  name: string;\n}", "typescript"},
		{"typescript const", "const x: number = 5;\nexport default x;", "typescript"},
		{"shell git", "git status", "shell"},
		{"shell chain", `git commit -m "feat: x" && git push origin main`, "shell"},
		{"shell prompt", "$ ls -la ~/Downloads", "shell"},
		{"shell export", "export PATH=/opt/homebrew/bin:$PATH", "shell"},
		{"shell shebang", "#!/bin/bash\necho hi", "shell"},
		{"shell go test", "go test ./uiapp/... -count=1", "shell"},
		{"shell npm", "npm install --save-dev typescript", "shell"},
		{"python shebang", "#!/usr/bin/env python3\nprint(1)", "python"},
		{"json", `{"a": 1, "b": [true, null]}`, "json"},
		{"json array", `[1, 2, 3]`, "json"},
		{"json cut off", "{\n  \"name\": \"clavis\",\n  \"version\": \"1.4.0\",\n  \"keywords\": [", "json"},
		{"yaml", codeSnapYAML, "yaml"},
		{"yaml short", "name: ci\non:\n  push:\n    branches: [main]", "yaml"},
		{"yaml list", "steps:\n  - uses: actions/checkout@v4\n  - run: go test ./...", "yaml"},
		{"sql lower with evidence", "select id from users where id = 1", "sql"},
		{"toml", "[package]\nname = \"clavis\"\nversion = \"1.0\"", "toml"},
		{"html", "<div class=\"a\">hi</div>", "html"},
		{"html inline", "<b>bold</b> text", "html"},
		{"html doc", "<!DOCTYPE html>\n<html></html>", "html"},
		{"xml", "<?xml version=\"1.0\"?>\n<a/>", "xml"},
		{"xml tags", "<note><to>x</to></note>", "xml"},
		{"css", ".btn {\n  color: red;\n  padding: 4px;\n}", "css"},
		{"diff", "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b", "diff"},
		{"c", "#include <stdio.h>\nint main(void) {\n  printf(\"hi\");\n}", "c"},
		{"c++", "#include <iostream>\nint main() {\n  std::cout << 1;\n}", "c++"},
		{"java", "public class Main {\n  public static void main(String[] args) {\n    System.out.println(\"hi\");\n  }\n}", "java"},

		// What is not code in a language, or only looks like it, stays plain.
		{"empty", "", ""},
		{"words", "hello world", ""},
		{"chinese", "微信支付回调签名校验失败，请检查商户证书与 APIv3 密钥是否匹配", ""},
		{"capitalized select", "Select an item from the list below", ""},
		{"lowercase select prose", "select an item from the list below", ""},
		{"lowercase select prose with a comma", "select the one you want from this list, then press enter", ""},
		{"bullet list", "- apples\n- oranges\n- pears", ""},
		{"label lines in a letter", "Dear John,\n\nPlease find attached the report: it is due on Monday.\n\nRegards,\nAlice", ""},
		{"python traceback", "Traceback (most recent call last):\n  File \"main.py\", line 3, in <module>\n    foo()\nNameError: name 'foo' is not defined", ""},
		{"go panic", "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:\nmain.main()\n\t/tmp/x.go:7 +0x1d", ""},
		{"csv", "a,b,c\n1,2,3\n4,5,6", ""},
		{"c-like statement alone", "if (a < b) { return c; }", ""},
		{"update prose", "Update the docs, then set the flag", ""},
		{"make prose", "make sure you save the file before closing it", ""},
		{"go prose", "go to the store and buy some milk today", ""},
		{"path", "/usr/local/bin/foo", ""},
		{"url", "https://example.com/a?b=c", ""},
		{"log line", "2026-11-21T10:00:00Z INFO request served path=/api/v1/items status=200 latency=12ms", ""},
		{"label", "Note: this is important", ""},
		{"lone import", "import os", ""},
		{"braces only", "{\n  something\n}", ""},
	} {
		if got := detectCodeLanguage(c.text); got != c.want {
			t.Errorf("%s: detectCodeLanguage(%q) = %q, want %q", c.name, c.text, got, c.want)
		}
	}
}

// TestDetectedLanguagesAreKnownToTheEditor: every name the detector can return
// must be one editor.LanguageNamed resolves, with a tokenizer to highlight by.
func TestDetectedLanguagesAreKnownToTheEditor(t *testing.T) {
	names := append([]string{"json", "diff", "html", "xml", "sql", "toml", "yaml"}, codeLangNames[:]...)
	for _, name := range names {
		l := editor.LanguageNamed(name)
		if l == nil || l.Tokenizer == nil {
			t.Errorf("detected language %q has no MyGo tokenizer", name)
		}
	}
}

func TestDetectCodeLanguageReadsOnlyTheHead(t *testing.T) {
	// SQL far past the sniff window is not looked for, and the call stays cheap.
	text := strings.Repeat("plain words here\n", clipboardSniffBytes) + "SELECT 1 FROM t;"
	if got := detectCodeLanguage(text); got != "" {
		t.Fatalf("detectCodeLanguage read past its window: %q", got)
	}
}

// TestClipboardCodeFenceNamesItsLanguage: a clipboard text the preview treats
// as code is fenced with the language it reads as, which is what colors it; a
// code text no language claims keeps its bare fence.
func TestClipboardCodeFenceNamesItsLanguage(t *testing.T) {
	defer i18n.SetLanguage(i18n.Current())
	i18n.SetLanguage(i18n.English)
	for _, c := range []struct{ name, text, fence string }{
		{"sql", codeSnapSQL, "```sql\n"},
		{"go", codeSnapGo, "```go\n"},
		{"shell", codeSnapShell, "```shell\n"},
		{"unknown", "{\n  something\n}", "```\n"},
	} {
		entry := computePreviewText(c.text, false, previewInitialLineLimit)
		if entry.contentKind != previewKindCode {
			t.Errorf("%s: kind = %v, want code", c.name, entry.contentKind)
			continue
		}
		if !strings.HasPrefix(entry.mdSource, c.fence) {
			t.Errorf("%s: markdown source %q does not open with %q", c.name, entry.mdSource, c.fence)
		}
	}
	// JSON keeps its own fence.
	if entry := computePreviewText(codeSnapJSON, false, previewInitialLineLimit); !strings.HasPrefix(entry.mdSource, "```json\n") {
		t.Errorf("json: markdown source %q", entry.mdSource)
	}
}

func countPixelsNear(img *image.RGBA, c ui.Color, tolerance int) int {
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := img.RGBAAt(x, y)
			if abs(int(p.R)-int(c.R))+abs(int(p.G)-int(c.G))+abs(int(p.B)-int(c.B)) <= tolerance {
				n++
			}
		}
	}
	return n
}

// TestPreviewLinesDrawInXcodeColors renders a monospace preview and finds the
// Xcode keyword, string and number colors in its pixels, in both appearances.
func TestPreviewLinesDrawInXcodeColors(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th := ui.LightTheme()
		want := map[string]ui.Color{"keyword": ui.Hex("#9B2393"), "string": ui.Hex("#C41A16"), "number": ui.Hex("#1C00CF")}
		if dark {
			th = ui.DarkTheme()
			want = map[string]ui.Color{"keyword": ui.Hex("#FC5FA3"), "string": ui.Hex("#FC6A5D"), "number": ui.Hex("#D0BF69")}
		}
		view := func(c *ui.Context) {
			ui.Column(c).Fill().Padding(space8).Children(func() {
				renderPreviewLines(c, c.Theme(), codeSnapSQL, previewInitialLineLimit)
			})
		}
		tt := ui.NewTester(view, 420, 200)
		tt.SetDark(dark)
		tt.SetScale(2)
		tt.Frame()
		for name, color := range want {
			if n := countPixelsNear(tt.Image(), color, 24); n < 12 {
				t.Errorf("dark=%v: only %d pixels of Xcode's %s color %v in the preview", dark, n, name, color)
			}
		}
		if !tt.HasText("SELECT") {
			t.Errorf("dark=%v: the keyword text must still be drawn and findable", dark)
		}
		_ = th
	}
}

// TestPreviewLinesHighlightOncePerText: the preview is rebuilt every frame, and
// the highlight of a text is computed on the first one only.
func TestPreviewLinesHighlightOncePerText(t *testing.T) {
	text := "func syntaxCacheProbe() error {\n\treturn nil\n}\n// unique to this test"
	lines, hidden := previewLines(text, previewInitialLineLimit)
	// NewTester builds the first frame, so the counts are read before it.
	hits0, misses0 := previewCodeCache.stats()
	tt := ui.NewTester(func(c *ui.Context) {
		renderPreviewLinesSlice(c, c.Theme(), lines, true, hidden)
	}, 420, 160)
	for range 4 {
		tt.Frame()
	}
	hits1, misses1 := previewCodeCache.stats()
	if misses1-misses0 != 1 {
		t.Fatalf("the text was highlighted %d times over 4 frames, want once", misses1-misses0)
	}
	if hits1-hits0 < 3 {
		t.Fatalf("later frames must hit the cache: %d hits over 4 frames", hits1-hits0)
	}
}

// TestPreviewLinesWithoutALanguageStayPlain: a mono text no language claims is
// drawn exactly as before, one plain text per line.
func TestPreviewLinesWithoutALanguageStayPlain(t *testing.T) {
	lines := []string{"/usr/local/bin/clavis"}
	if previewCodeCache.spans(ui.LightTheme(), lines) != nil {
		t.Fatal("a path must not be highlighted")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		renderPreviewLinesSlice(c, c.Theme(), lines, true, 0)
	}, 300, 80)
	tt.Frame()
	if _, ok := tt.Find("/usr/local/bin/clavis"); !ok {
		t.Fatal("the path must still be drawn")
	}
}

// TestMarkdownCodeBlocksDrawInXcodeColors takes the Markdown route: a fenced
// block in an AI answer, through ensureMarkdownOptions and the code block hook.
func TestMarkdownCodeBlocksDrawInXcodeColors(t *testing.T) {
	f := newInteractionFixture(t, nil)
	for _, dark := range []bool{false, true} {
		keyword := ui.Hex("#9B2393")
		if dark {
			keyword = ui.Hex("#FC5FA3")
		}
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Padding(space8).Children(func() {
				f.ctrl.renderClavisMarkdown(c, c.Theme(), "```go\nfunc main() {\n\treturn\n}\n```", false)
			})
		}, 420, 220)
		tt.SetDark(dark)
		tt.SetScale(2)
		tt.Frame()
		if n := countPixelsNear(tt.Image(), keyword, 24); n < 12 {
			t.Errorf("dark=%v: only %d pixels of Xcode's keyword color in the fenced block", dark, n)
		}
	}
}

// TestCodeRecolorsWhenAppearanceChanges flips one running window from light to
// dark and back: neither the Markdown block (whose highlighted chunks markdown
// keeps until its theme changes) nor the preview's cached spans may keep the
// other appearance's colors.
func TestCodeRecolorsWhenAppearanceChanges(t *testing.T) {
	f := newInteractionFixture(t, nil)
	lightKeyword, darkKeyword := ui.Hex("#9B2393"), ui.Hex("#FC5FA3")
	for _, c := range []struct {
		name string
		draw func(c *ui.Context)
	}{
		{"markdown", func(c *ui.Context) {
			f.ctrl.renderClavisMarkdown(c, c.Theme(), "```go\nfunc main() {\n\treturn\n}\n```", false)
		}},
		{"preview lines", func(c *ui.Context) {
			renderPreviewLines(c, c.Theme(), "func main() {\n\treturn\n}", previewInitialLineLimit)
		}},
	} {
		tt := ui.NewTester(func(ctx *ui.Context) {
			ui.Column(ctx).Fill().Padding(space8).Children(func() { c.draw(ctx) })
		}, 420, 220)
		tt.SetScale(2)
		for _, step := range []struct {
			dark      bool
			want, not ui.Color
		}{{false, lightKeyword, darkKeyword}, {true, darkKeyword, lightKeyword}, {false, lightKeyword, darkKeyword}} {
			tt.SetDark(step.dark)
			tt.Frame()
			if n := countPixelsNear(tt.Image(), step.want, 24); n < 12 {
				t.Errorf("%s dark=%v: only %d pixels of the keyword color it should have", c.name, step.dark, n)
			}
			if n := countPixelsNear(tt.Image(), step.not, 24); n != 0 {
				t.Errorf("%s dark=%v: %d pixels still in the other appearance's keyword color", c.name, step.dark, n)
			}
		}
	}
}

// BenchmarkPreviewCodeLines measures a frame of a 60-line monospace preview:
// highlighted (the cache hit every frame after the first), and the same lines
// with a quote mark in front, which no language claims, as plain text.
//
//	go test -run '^$' -bench BenchmarkPreviewCodeLines -benchmem ./uiapp/
func BenchmarkPreviewCodeLines(b *testing.B) {
	code := strings.Repeat("func handler(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(200) // done\n\treturn\n}\n", 15)
	plain := "> " + strings.ReplaceAll(strings.TrimSuffix(code, "\n"), "\n", "\n> ")
	for _, tc := range []struct {
		name, text string
		colored    bool
	}{{"highlighted", code, true}, {"plain", plain, false}} {
		b.Run(tc.name, func(b *testing.B) {
			lines, hidden := previewLines(tc.text, previewInitialLineLimit)
			if got := previewCodeCache.spans(ui.LightTheme(), lines) != nil; got != tc.colored {
				b.Fatalf("highlighted = %v, want %v", got, tc.colored)
			}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Scroll(c).Fill().Children(func() {
					renderPreviewLinesSlice(c, c.Theme(), lines, true, hidden)
				})
			}, 520, 700)
			tt.Frame()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				tt.Frame()
			}
		})
	}
}
