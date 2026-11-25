package uiapp

import (
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Xcode syntax colors for every place Clavis shows code: fenced blocks in AI
// answers and Markdown previews (markdown.Theme.CodeColors) and the clipboard
// preview's monospace lines (renderPreviewLinesSlice).
//
// The palette is Xcode 12+'s "Default (Light)" and "Default (Dark)" source
// editor theme. Xcode colors by what a symbol resolves to (project function,
// SDK type, declaration), which a lexical tokenizer cannot tell, so the
// categories with no lexical counterpart (project/other type and function
// declarations, project class names, other functions) are left out and the
// nearest one carries each token kind: see xcodeCodeTheme.

// xcodePalette is the Xcode colors one appearance uses.
type xcodePalette struct {
	comments     ui.Color // Comments
	strings      ui.Color // Strings
	numbers      ui.Color // Characters, Numbers
	keywords     ui.Color // Keywords (semibold in Xcode)
	preprocessor ui.Color // Preprocessor Statements
	urls         ui.Color // URLs
	attributes   ui.Color // Attributes
	projectFuncs ui.Color // Project Function and Method Names, Constants, Instance Variables and Globals
	otherTypes   ui.Color // Other Type Names
	// added is not an Xcode syntax color (Xcode has no diff language): it is
	// Apple's accessible system green, so a diff's inserted lines read as they
	// do everywhere on macOS. Deleted lines use the strings red.
	added ui.Color
}

var (
	xcodeLight = xcodePalette{
		comments:     ui.Hex("#5D6C79"),
		strings:      ui.Hex("#C41A16"),
		numbers:      ui.Hex("#1C00CF"),
		keywords:     ui.Hex("#9B2393"),
		preprocessor: ui.Hex("#643820"),
		urls:         ui.Hex("#0E0EFF"),
		attributes:   ui.Hex("#815F03"),
		projectFuncs: ui.Hex("#326D74"),
		otherTypes:   ui.Hex("#3900A0"),
		added:        ui.Hex("#248A3D"),
	}
	xcodeDark = xcodePalette{
		comments:     ui.Hex("#6C7986"),
		strings:      ui.Hex("#FC6A5D"),
		numbers:      ui.Hex("#D0BF69"),
		keywords:     ui.Hex("#FC5FA3"),
		preprocessor: ui.Hex("#FD8F3F"),
		urls:         ui.Hex("#5482FF"),
		attributes:   ui.Hex("#BF8555"),
		projectFuncs: ui.Hex("#67B7A4"),
		otherTypes:   ui.Hex("#D0A8FF"),
		added:        ui.Hex("#30DB5B"),
	}
)

// codeWellFill is the face of every code well: an opaque step from the
// theme's body, never a tint over whatever is behind it. Glass belongs to
// floating shells only, and a translucent well lets the desktop (or the card
// under an AI answer) change what the syntax colors have to read on.
func codeWellFill(t *ui.Theme) ui.Color {
	well := surfaceBase(t)
	well.A = 255
	return well
}

// xcodeThemeKey is what xcodeCodeTheme's result depends on: the colors
// editor.DefaultTheme and the palette read off the window theme.
type xcodeThemeKey struct {
	dark                                             bool
	text, muted, background, surface, border, accent ui.Color
	selection                                        ui.Color
}

func xcodeKeyOf(t *ui.Theme) xcodeThemeKey {
	return xcodeThemeKey{
		dark: t.Dark, text: t.Text, muted: t.TextMuted, background: t.Background,
		surface: t.Surface, border: t.Border, accent: t.Accent, selection: t.Selection,
	}
}

// xcodeThemes memoizes the themes: markdown compares a theme's CodeColors by
// pointer to decide whether its highlighted blocks are stale, so a theme must
// come back as the same pointer for as long as its inputs hold.
var xcodeThemes struct {
	sync.Mutex
	byKey map[xcodeThemeKey]*editor.Theme
}

// xcodeCodeTheme returns the Xcode code theme for t. The result is shared:
// callers must not modify it.
//
// Token kinds map to Xcode's categories as: Keyword to keywords (semibold);
// Constant (true, nil, null) and Tag to the keywords color, not bold; Type to
// other type names; Function and Property to project function names and
// instance variables (one color in Xcode); Number and Escape to numbers and
// characters; String, Regexp and inline Code to strings; Comment to comments
// (Xcode does not italicize them); Meta (preprocessor lines, annotations) to
// preprocessor statements; Attribute to attributes; Link to URLs. Operators,
// punctuation, variables and plain text stay the theme's text color, as in
// Xcode.
//
// Every color is Xcode's own where it reads at minTextContrast on the code
// well. Where it does not, it is blended toward the theme's text color in the
// smallest 10% step that does, keeping its hue: Xcode's dark comment gray
// (#6C7986) is 3.8 to 4.0:1 on the near-black dark well, so it comes out a
// shade lighter than Xcode's. (Xcode itself reads it at 3.7:1 on its own #1F1F24
// editor; Clavis holds body text to 4.5:1.) The light palette needs no nudge.
func xcodeCodeTheme(t *ui.Theme) *editor.Theme {
	key := xcodeKeyOf(t)
	xcodeThemes.Lock()
	defer xcodeThemes.Unlock()
	if ct, ok := xcodeThemes.byKey[key]; ok {
		return ct
	}
	if xcodeThemes.byKey == nil || len(xcodeThemes.byKey) >= 8 {
		xcodeThemes.byKey = map[xcodeThemeKey]*editor.Theme{}
	}
	ct := buildXcodeCodeTheme(t)
	xcodeThemes.byKey[key] = ct
	return ct
}

func buildXcodeCodeTheme(t *ui.Theme) *editor.Theme {
	p := xcodeLight
	if t.Dark {
		p = xcodeDark
	}
	well := codeWellFill(t)
	ink := func(c ui.Color) ui.Color { return readableOn(c, t.Text, well, minTextContrast) }

	// The non-token colors (selection, cursor, guides, diagnostics) stay the
	// window theme's, so an editor drawn with this theme matches the app.
	ct := editor.DefaultTheme(t)
	ct.Background, ct.Text = well, t.Text
	for k := range ct.Tokens {
		ct.Tokens[k] = editor.TokenStyle{Color: t.Text}
	}
	for _, r := range []struct {
		kinds []editor.TokenKind
		color ui.Color
		bold  bool
	}{
		{[]editor.TokenKind{editor.TokenKeyword, editor.TokenHeading}, p.keywords, true},
		{[]editor.TokenKind{editor.TokenConstant, editor.TokenTag}, p.keywords, false},
		{[]editor.TokenKind{editor.TokenType}, p.otherTypes, false},
		{[]editor.TokenKind{editor.TokenFunction, editor.TokenProperty}, p.projectFuncs, false},
		{[]editor.TokenKind{editor.TokenNumber, editor.TokenEscape}, p.numbers, false},
		{[]editor.TokenKind{editor.TokenString, editor.TokenRegexp, editor.TokenCode, editor.TokenDeleted, editor.TokenInvalid}, p.strings, false},
		{[]editor.TokenKind{editor.TokenComment}, p.comments, false},
		{[]editor.TokenKind{editor.TokenMeta}, p.preprocessor, false},
		{[]editor.TokenKind{editor.TokenAttribute}, p.attributes, false},
		{[]editor.TokenKind{editor.TokenLink}, p.urls, false},
		{[]editor.TokenKind{editor.TokenInserted}, p.added, false},
	} {
		for _, k := range r.kinds {
			ct.Tokens[k] = editor.TokenStyle{Color: ink(r.color), Bold: r.bold}
		}
	}
	ct.Tokens[editor.TokenStrong].Bold = true
	ct.Tokens[editor.TokenEmphasis].Italic = true
	return ct
}

// Highlighting bounds. The preview is rebuilt every frame, so what is
// highlighted is cached (codeHighlightCache) and what would be slow to lay out
// is left plain: a visible text past previewCodeMaxBytes, and any line past
// previewCodeMaxLine, whose thousands of spans would cost more than they say.
const (
	previewCodeCacheMax = 16
	previewCodeMaxBytes = 32 << 10
	previewCodeMaxLine  = 2000
)

// codeTokenSpan is the span style of one token: Xcode's color, semibold for
// bold kinds and italic for italic ones.
func codeTokenSpan(ct *editor.Theme, kind editor.TokenKind) ui.Span {
	sp := ui.Span{Color: ct.TokenColor(kind)}
	if s := ct.Tokens[kind]; s.Bold {
		sp.Weight = 600
	} else if s.Italic {
		sp.Italic = true
	}
	return sp
}

// appendCodeLineSpans appends the spans of one tokenized line to dst, merging
// neighbors of the same style so a line costs as few spans as it has colors.
// The text of the appended spans is the line, whole and in order.
func appendCodeLineSpans(dst []ui.Span, ct *editor.Theme, line string, toks []editor.Token) []ui.Span {
	var run ui.Span
	runStart, pos := 0, 0
	open := false
	add := func(end int, style ui.Span) {
		if open && style.Color == run.Color && style.Weight == run.Weight && style.Italic == run.Italic {
			pos = end
			return
		}
		if open {
			run.Text = line[runStart:pos]
			dst = append(dst, run)
		}
		run, runStart, pos, open = style, pos, end, true
	}
	for _, t := range toks {
		if end := min(t.End, len(line)); end > pos {
			add(end, codeTokenSpan(ct, t.Kind))
		}
	}
	if pos < len(line) {
		add(len(line), codeTokenSpan(ct, editor.TokenText))
	}
	if open {
		run.Text = line[runStart:pos]
		dst = append(dst, run)
	}
	return dst
}

// highlightCodeLines tokenizes lines, consecutive lines of one text, with tok
// and returns each line's spans. A blank line has none. A line past
// previewCodeMaxLine is not tokenized (the state carries over it unchanged, as
// MyGo's editor does) and stays one plain span.
func highlightCodeLines(ct *editor.Theme, tok editor.Tokenizer, lines []string) [][]ui.Span {
	out := make([][]ui.Span, len(lines))
	var (
		state   uint64
		toks    []editor.Token
		scratch []ui.Span // a line's spans are built here, then copied exactly sized
	)
	for i, line := range lines {
		switch {
		case strings.TrimSpace(line) == "":
			toks, state = tok.Tokenize(line, state, toks[:0])
		case len(line) > previewCodeMaxLine:
			plain := codeTokenSpan(ct, editor.TokenText)
			plain.Text = line
			out[i] = []ui.Span{plain}
		default:
			toks, state = tok.Tokenize(line, state, toks[:0])
			scratch = appendCodeLineSpans(scratch[:0], ct, line, toks)
			out[i] = slices.Clone(scratch)
		}
	}
	return out
}

// codeHighlightKey identifies a highlighted text cheaply: the theme it was
// colored for and the text's shape. The lines themselves are compared on a hit
// (codeHighlightEntry.lines), so a key collision can never show other text.
type codeHighlightKey struct {
	theme       xcodeThemeKey
	lines, size int
	first, last string
}

// codeHighlightEntry is a highlighted text. spans is nil when the text is in no
// language Clavis can name: that answer is cached too, so a plain text is not
// sniffed again every frame.
type codeHighlightEntry struct {
	lines []string
	spans [][]ui.Span
}

// codeHighlightCache keeps the highlighted spans of recently drawn previews.
// The preview is rebuilt every frame, and tokenizing a screenful of code (let
// alone sniffing its language) each time would be most of the frame.
type codeHighlightCache struct {
	mu      sync.Mutex
	max     int
	entries map[codeHighlightKey]codeHighlightEntry
	hits    uint64
	misses  uint64
}

func newCodeHighlightCache(max int) *codeHighlightCache {
	if max <= 0 {
		max = previewCodeCacheMax
	}
	return &codeHighlightCache{max: max, entries: make(map[codeHighlightKey]codeHighlightEntry, max)}
}

// previewCodeCache is the one cache the preview draws through.
var previewCodeCache = newCodeHighlightCache(previewCodeCacheMax)

func (c *codeHighlightCache) stats() (hits, misses uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

func (c *codeHighlightCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// spans returns the highlighted spans of lines, one slice per line, or nil
// when they are not code in a known language or are too large to highlight.
func (c *codeHighlightCache) spans(t *ui.Theme, lines []string) [][]ui.Span {
	size := 0
	for _, l := range lines {
		size += len(l)
	}
	if len(lines) == 0 || size == 0 || size > previewCodeMaxBytes {
		return nil
	}
	key := codeHighlightKey{theme: xcodeKeyOf(t), lines: len(lines), size: size, first: lines[0], last: lines[len(lines)-1]}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && slices.Equal(e.lines, lines) {
		c.hits++
		c.mu.Unlock()
		return e.spans
	}
	c.mu.Unlock()

	// The entry owns its lines, and its key's: they are substrings of the
	// clipboard entry, and keeping them would keep all 256 KiB of it alive.
	owned := make([]string, len(lines))
	for i, l := range lines {
		owned[i] = strings.Clone(l)
	}
	key.first, key.last = owned[0], owned[len(owned)-1]
	e := codeHighlightEntry{lines: owned}
	if lang := editor.LanguageNamed(detectCodeLanguage(strings.Join(owned, "\n"))); lang != nil && lang.Tokenizer != nil {
		e.spans = highlightCodeLines(xcodeCodeTheme(t), lang.Tokenizer, owned)
	}

	c.mu.Lock()
	c.misses++
	if len(c.entries) >= c.max {
		clear(c.entries)
	}
	c.entries[key] = e
	c.mu.Unlock()
	return e.spans
}

// Language detection. The preview has no file name to go by, so a code text's
// language is guessed from its first lines. A wrong guess costs more than none
// (prose colored as Python), so every signal is a whole-line shape that prose
// does not take, and a text that no language scores on stays plain.

const (
	codeSniffLines    = 40 // lines of a text the guess reads
	codeSniffMinScore = 3  // a language needs one strong signal or a few weak ones
)

// codeLang indexes a scored language.
type codeLang uint8

const (
	langGo codeLang = iota
	langSwift
	langRust
	langPython
	langJS
	langTS
	langShell
	langCSS
	langC
	langCPP
	langJava
	codeLangCount
)

// codeLangNames are the names editor.LanguageNamed takes for each codeLang.
var codeLangNames = [codeLangCount]string{"go", "swift", "rust", "python", "javascript", "typescript", "shell", "css", "c", "c++", "java"}

// codeRule is a line shape that points to a language, with how strongly.
type codeRule struct {
	lang   codeLang
	weight int
	match  func(line string) bool
}

func hasPrefixAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// isCodeIdent reports whether s is one identifier.
func isCodeIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9' || i == 0) {
			return false
		}
	}
	return true
}

var (
	// tsAnnotation matches a type annotation of a builtin type: id: string.
	tsAnnotation = regexp.MustCompile(`[\w)\]]\??:\s*(string|number|boolean|any|void|unknown|never|object)\b`)

	swiftFrameworks = map[string]bool{"SwiftUI": true, "Foundation": true, "UIKit": true, "AppKit": true, "Cocoa": true,
		"Combine": true, "XCTest": true, "CoreData": true, "SwiftData": true, "CoreGraphics": true, "MapKit": true,
		"AVFoundation": true, "OSLog": true, "Observation": true}

	cssProps = map[string]bool{"color": true, "background": true, "background-color": true, "margin": true, "padding": true,
		"border": true, "border-radius": true, "font-size": true, "font-family": true, "font-weight": true, "display": true,
		"width": true, "height": true, "position": true, "flex": true, "grid": true, "text-align": true, "line-height": true,
		"opacity": true, "overflow": true, "z-index": true, "cursor": true, "transition": true, "transform": true,
		"box-shadow": true, "gap": true, "align-items": true, "justify-content": true, "max-width": true, "min-height": true}

	// codeKeywordWords start lines that end in { without being a CSS selector.
	codeKeywordWords = map[string]bool{"if": true, "for": true, "while": true, "else": true, "switch": true, "func": true,
		"function": true, "class": true, "struct": true, "interface": true, "enum": true, "try": true, "do": true,
		"catch": true, "finally": true, "namespace": true, "impl": true, "fn": true, "type": true, "trait": true,
		"mod": true, "def": true, "package": true, "import": true, "return": true, "extension": true, "protocol": true}

	htmlTags = map[string]bool{"html": true, "head": true, "body": true, "div": true, "span": true, "p": true, "a": true,
		"ul": true, "ol": true, "li": true, "script": true, "style": true, "img": true, "input": true, "button": true,
		"form": true, "table": true, "tr": true, "td": true, "h1": true, "h2": true, "h3": true, "h4": true, "section": true,
		"nav": true, "header": true, "footer": true, "main": true, "label": true, "select": true, "textarea": true,
		"b": true, "i": true, "u": true, "em": true, "strong": true, "small": true, "code": true, "pre": true,
		"br": true, "hr": true, "blockquote": true}
)

// shellLine reports whether a line is a shell command: it starts with a
// command word (the same list the clipboard row uses for its terminal icon)
// and is either short or carries what commands carry (flags, pipes, paths).
func shellLine(line string) bool {
	s := strings.TrimPrefix(line, "$ ")
	if !looksLikeShellCommand(strings.ToLower(s)) {
		return false
	}
	return len(strings.Fields(s)) <= 4 || containsAny(s, " -", "|", "&&", ">", "$", "./", "~/", "=")
}

// shellExport reports whether a line is export NAME=value.
func shellExport(line string) bool {
	rest, ok := strings.CutPrefix(line, "export ")
	if !ok {
		return false
	}
	name, _, ok := strings.Cut(rest, "=")
	return ok && isCodeIdent(name) && name == strings.ToUpper(name)
}

// cssPropertyLine reports whether a line is `property: value;` of a property
// CSS is known for (TypeScript members take the same shape).
func cssPropertyLine(line string) bool {
	name, _, ok := strings.Cut(line, ":")
	return ok && strings.HasSuffix(line, ";") && cssProps[strings.TrimSpace(name)]
}

// cssSelectorLine reports whether a line opens a CSS rule: a selector ending in
// { that is not the head of a statement of another language.
func cssSelectorLine(line string) bool {
	if !strings.HasSuffix(line, "{") || containsAny(line, "(", "=", "\"") {
		return false
	}
	word, _, _ := strings.Cut(line, " ")
	if codeKeywordWords[strings.TrimSuffix(word, "{")] {
		return false
	}
	return hasPrefixAny(line, ".", "#", ":", "*", "[", "@") || line[0] >= 'a' && line[0] <= 'z'
}

var codeRules = []codeRule{
	// Go.
	{langGo, 5, func(l string) bool { return strings.HasPrefix(l, "package ") && isCodeIdent(l[len("package "):]) }},
	{langGo, 3, func(l string) bool {
		return strings.HasPrefix(l, "func ") && strings.Contains(l, "(") && !strings.Contains(l, "->")
	}},
	{langGo, 2, func(l string) bool { return strings.Contains(l, ":=") }},
	{langGo, 5, func(l string) bool {
		return strings.HasPrefix(l, "type ") && containsAny(l, " struct", " interface")
	}},
	{langGo, 3, func(l string) bool { return l == "import (" || strings.HasPrefix(l, `import "`) }},
	{langGo, 3, func(l string) bool {
		return containsAny(l, "fmt.", "err != nil", "err == nil", "errors.New(") || hasPrefixAny(l, "defer ", "go func(")
	}},

	// Swift.
	{langSwift, 5, func(l string) bool {
		name, ok := strings.CutPrefix(l, "import ")
		return ok && swiftFrameworks[name]
	}},
	{langSwift, 3, func(l string) bool { return strings.Contains(l, "func ") && strings.Contains(l, "->") }},
	{langSwift, 5, func(l string) bool { return containsAny(l, "some View", ": View {", ": App {") }},
	{langSwift, 4, func(l string) bool {
		return hasPrefixAny(l, "@State", "@Binding", "@Published", "@main", "@Environment", "@ObservedObject", "@StateObject", "@IBOutlet", "@IBAction")
	}},
	{langSwift, 4, func(l string) bool {
		return strings.HasPrefix(l, "guard ") && strings.Contains(l, " else") || hasPrefixAny(l, "if let ", "guard let ")
	}},

	// Rust.
	{langRust, 4, func(l string) bool { return hasPrefixAny(l, "fn ", "pub fn ", "async fn ", "pub async fn ") }},
	{langRust, 5, func(l string) bool { return hasPrefixAny(l, "use std::", "use crate::", "use super::") }},
	{langRust, 4, func(l string) bool { return strings.HasPrefix(l, "let mut ") }},
	{langRust, 3, func(l string) bool { return hasPrefixAny(l, "impl ", "impl<") }},
	{langRust, 4, func(l string) bool { return hasPrefixAny(l, "pub struct ", "pub enum ", "pub trait ", "pub mod ") }},
	{langRust, 4, func(l string) bool {
		return containsAny(l, "println!(", "format!(", "vec![", "panic!(", "assert!(", ".unwrap()", ".expect(")
	}},
	{langRust, 3, func(l string) bool { return containsAny(l, "&str", "&mut ", "-> Result<", "-> Option<") }},
	{langRust, 5, func(l string) bool { return hasPrefixAny(l, "#[derive(", "#![", "#[") }},

	// Python.
	{langPython, 5, func(l string) bool {
		return hasPrefixAny(l, "def ", "async def ") && strings.Contains(l, "(") && strings.Contains(l, ":")
	}},
	{langPython, 5, func(l string) bool { return strings.HasPrefix(l, "class ") && strings.HasSuffix(l, ":") }},
	{langPython, 6, func(l string) bool { return strings.HasPrefix(l, "if __name__") }},
	{langPython, 5, func(l string) bool { return strings.HasPrefix(l, "from ") && strings.Contains(l, " import ") }},
	{langPython, 2, func(l string) bool {
		return strings.HasPrefix(l, "import ") && !containsAny(l, "\"", "'", ";", "{", " from ")
	}},
	{langPython, 4, func(l string) bool {
		return strings.HasPrefix(l, "elif ") || l == "try:" || l == "else:" || l == "finally:" ||
			hasPrefixAny(l, "except ", "with ") && strings.HasSuffix(l, ":")
	}},
	{langPython, 4, func(l string) bool { return strings.Contains(l, "__init__") }},
	{langPython, 2, func(l string) bool { return strings.Contains(l, "self.") || strings.Contains(l, "lambda ") }},
	{langPython, 3, func(l string) bool { return hasPrefixAny(l, "for ", "while ", "if ") && strings.HasSuffix(l, ":") }},

	// JavaScript.
	{langJS, 4, func(l string) bool {
		return hasPrefixAny(l, "function ", "async function ") || containsAny(l, " function(", "= function")
	}},
	{langJS, 3, func(l string) bool {
		return hasPrefixAny(l, "const ", "let ", "var ") && strings.Contains(l, " = ") && strings.HasSuffix(l, ";")
	}},
	{langJS, 3, func(l string) bool { return strings.Contains(l, "=>") }},
	{langJS, 5, func(l string) bool {
		return strings.HasPrefix(l, "import ") && strings.Contains(l, " from ") && containsAny(l, "'", "\"")
	}},
	{langJS, 5, func(l string) bool {
		return hasPrefixAny(l, "export default ", "export const ", "export function ", "export class ", "export async ", "export {", "export interface ", "export type ")
	}},
	{langJS, 5, func(l string) bool { return strings.Contains(l, "console.") }},
	{langJS, 5, func(l string) bool { return containsAny(l, "require(", "module.exports") }},
	{langJS, 3, func(l string) bool { return containsAny(l, "document.", "window.", "JSON.stringify(", "JSON.parse(") }},
	{langJS, 3, func(l string) bool {
		return containsAny(l, ".then(", ".catch(", "new Promise") || hasPrefixAny(l, "await ") || strings.Contains(l, " await ")
	}},
	{langJS, 4, func(l string) bool { return containsAny(l, "===", "!==") }},

	// TypeScript (its own signals; the JavaScript ones count toward it too).
	{langTS, 4, func(l string) bool { return strings.HasPrefix(l, "interface ") && strings.HasSuffix(l, "{") }},
	{langTS, 5, func(l string) bool { return strings.HasPrefix(l, "type ") && strings.Contains(l, " = ") }},
	{langTS, 4, func(l string) bool { return tsAnnotation.MatchString(l) }},
	{langTS, 3, func(l string) bool { return containsAny(l, "Promise<", "Record<", "Partial<", "Array<", "<T>", "<T,") }},
	{langTS, 3, func(l string) bool {
		return containsAny(l, " implements ", " as const", " as any", " as string", " readonly ") || strings.HasPrefix(l, "readonly ")
	}},

	// Shell.
	{langShell, 4, shellLine},
	{langShell, 3, func(l string) bool { return strings.HasPrefix(l, "$ ") }},
	{langShell, 4, shellExport},
	{langShell, 4, func(l string) bool {
		return hasPrefixAny(l, "if [", "if [[") || l == "then" || l == "fi" || l == "done" || l == "esac" || containsAny(l, "; then", "; do")
	}},

	// CSS.
	{langCSS, 2, cssSelectorLine},
	{langCSS, 3, cssPropertyLine},
	{langCSS, 4, func(l string) bool {
		return hasPrefixAny(l, "@media ", "@import ", "@font-face", "@keyframes ", ":root")
	}},

	// C and C++.
	{langC, 6, func(l string) bool { return strings.HasPrefix(l, "#include ") }},
	{langC, 4, func(l string) bool {
		return hasPrefixAny(l, "#define ", "#ifdef ", "#ifndef ", "#pragma ") || l == "#endif"
	}},
	{langC, 5, func(l string) bool { return hasPrefixAny(l, "int main(", "void main(") }},
	{langC, 3, func(l string) bool { return strings.HasPrefix(l, "typedef ") || strings.Contains(l, "printf(") }},
	{langCPP, 4, func(l string) bool {
		return containsAny(l, "std::", "cout <<", "cin >>", "nullptr", "<iostream>", "<vector>") ||
			hasPrefixAny(l, "using namespace ", "template<", "template <", "namespace ")
	}},
	{langCPP, 5, func(l string) bool { return strings.HasPrefix(l, "#import ") }},

	// Java.
	{langJava, 5, func(l string) bool {
		return hasPrefixAny(l, "public class ", "public static void main", "import java.", "import javax.") ||
			strings.Contains(l, "System.out.print") || strings.HasPrefix(l, "package ") && strings.HasSuffix(l, ";")
	}},
	{langJava, 4, func(l string) bool {
		return hasPrefixAny(l, "@Override", "@Test", "@Autowired", "@SpringBootApplication")
	}},
	{langJava, 2, func(l string) bool {
		return hasPrefixAny(l, "private ", "public ", "protected ") && (strings.Contains(l, "(") || strings.HasSuffix(l, ";"))
	}},
}

// codeSniffSample returns the first codeSniffLines non-blank lines of head,
// trimmed.
func codeSniffSample(head string) []string {
	var lines []string
	for rest := head; rest != "" && len(lines) < codeSniffLines; {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// detectCodeLanguage names the language of a code or command text, in the
// spelling editor.LanguageNamed takes ("go", "sql", "shell"), or returns ""
// when it cannot say. It reads only the head of the text (clipboardSniffBytes),
// and is meant to be called once per text and cached.
func detectCodeLanguage(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	head := trimmed
	if len(head) > clipboardSniffBytes {
		head = head[:clipboardSniffBytes]
	}
	// A #! line names the interpreter outright.
	if l := editor.LanguageForText("", head); l != nil {
		return strings.ToLower(l.Name)
	}
	lines := codeSniffSample(head)
	switch {
	case looksLikeJSONText(trimmed, lines):
		return "json"
	case looksLikeDiff(lines):
		return "diff"
	}
	if lang := markupLanguage(head); lang != "" {
		return lang
	}
	if looksLikeSQL(lines) {
		return "sql"
	}
	if lang := scoredLanguage(lines); lang != "" {
		return lang
	}
	switch {
	case looksLikeTOML(lines):
		return "toml"
	case looksLikeYAML(lines):
		return "yaml"
	}
	return ""
}

// scoredLanguage returns the language whose line shapes score highest over
// lines, or "" when none reaches codeSniffMinScore.
func scoredLanguage(lines []string) string {
	var scores [codeLangCount]int
	for _, l := range lines {
		for _, r := range codeRules {
			if r.match(l) {
				scores[r.lang] += r.weight
			}
		}
	}
	// TypeScript is JavaScript with annotations, C++ is C with more: the
	// superset takes the score of the base once its own signals show.
	if scores[langTS] >= codeSniffMinScore {
		scores[langTS] += scores[langJS]
		scores[langJS] = 0
	}
	if scores[langCPP] >= codeSniffMinScore {
		scores[langCPP] += scores[langC]
		scores[langC] = 0
	}
	best, bestScore := codeLangCount, 0
	for lang := codeLang(0); lang < codeLangCount; lang++ {
		if scores[lang] > bestScore {
			best, bestScore = lang, scores[lang]
		}
	}
	if best == codeLangCount || bestScore < codeSniffMinScore {
		return ""
	}
	return codeLangNames[best]
}

// looksLikeJSONText reports whether the text is JSON, whole (as the preview
// classifies it) or cut off: an object or array whose lines are "key": pairs.
func looksLikeJSONText(trimmed string, lines []string) bool {
	if isLikelyJSON(trimmed) {
		return true
	}
	if len(lines) == 0 {
		return false
	}
	if first := lines[0]; first != "{" && first != "[" && !strings.HasPrefix(first, `{"`) && !strings.HasPrefix(first, `["`) {
		return false
	}
	keys := 0
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimPrefix(l, "{"), `"`) && strings.Contains(l, `":`) {
			keys++
		}
	}
	return keys >= 2 || len(lines) == 1 && keys == 1
}

// looksLikeDiff reports whether the lines are a unified diff.
func looksLikeDiff(lines []string) bool {
	for i, l := range lines {
		if strings.HasPrefix(l, "diff --git ") || strings.HasPrefix(l, "@@ -") && strings.Contains(l, " @@") {
			return true
		}
		if strings.HasPrefix(l, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
			return true
		}
	}
	return false
}

// markupLanguage names the markup language of a text that opens with a tag:
// "html", "xml", or "" when it is not markup.
func markupLanguage(head string) string {
	if !strings.HasPrefix(head, "<") || !strings.Contains(head, ">") {
		return ""
	}
	lower := strings.ToLower(head)
	switch {
	case hasPrefixAny(lower, "<?xml", "<svg", "<plist"):
		return "xml"
	case hasPrefixAny(lower, "<!doctype html", "<html"):
		return "html"
	}
	if !containsAny(head, "</", "/>") {
		return ""
	}
	name := lower[1:]
	if i := strings.IndexAny(name, " \t\r\n>/"); i >= 0 {
		name = name[:i]
	}
	if htmlTags[name] {
		return "html"
	}
	if isCodeIdent(strings.NewReplacer("-", "_", ":", "_", ".", "_").Replace(name)) {
		return "xml"
	}
	return ""
}

// looksLikeSQL reports whether the lines are an SQL statement: the first one
// opens with a statement word in one case (SELECT or select, not Select), and
// the statement's own second word or clause follows.
func looksLikeSQL(lines []string) bool {
	first := ""
	for _, l := range lines {
		if !hasPrefixAny(l, "--", "/*", "*") {
			first = l
			break
		}
	}
	word, rest, _ := strings.Cut(first, " ")
	word = strings.TrimRight(word, "(;*")
	lower := strings.ToLower(word)
	// SELECT or select, never Select: a capitalized word opens a sentence.
	if word == "" || word != strings.ToUpper(word) && word != lower {
		return false
	}
	flat := " " + strings.ToLower(strings.Join(strings.Fields(strings.Join(lines, " ")), " ")) + " "
	// SQL in capitals is evidence enough; in lower case it needs what prose
	// lacks: a star, a call, a comparison or a semicolon (a comma does not
	// count: "select the one you want from the list, then press enter").
	if word != strings.ToUpper(word) && !containsAny(flat, "*", "(", "=", ";") {
		return false
	}
	next, _, _ := strings.Cut(strings.ToLower(rest), " ")
	switch lower {
	case "select":
		return strings.Contains(flat, " from ") || strings.HasSuffix(strings.TrimSpace(flat), ";")
	case "insert", "merge":
		return strings.Contains(flat, " into ")
	case "update":
		return strings.Contains(flat, " set ")
	case "delete":
		return strings.Contains(flat, " from ")
	case "create", "alter", "drop":
		switch next {
		case "table", "index", "view", "database", "schema", "trigger", "function", "procedure", "unique", "or", "temporary", "temp", "type", "sequence", "extension":
			return true
		}
	case "with":
		return containsAny(flat, " as (", " as select")
	case "truncate":
		return strings.Contains(flat, " table ")
	case "explain":
		return containsAny(flat, " select ", " analyze ")
	case "pragma":
		return containsAny(flat, "=", "(")
	case "grant", "revoke":
		return strings.Contains(flat, " on ")
	}
	return false
}

// looksLikeTOML reports whether the lines are a TOML or INI file: [sections]
// and `key = value` pairs, nothing else.
func looksLikeTOML(lines []string) bool {
	sections, pairs := 0, 0
	for _, l := range lines {
		switch name, _, pair := strings.Cut(l, " = "); {
		case hasPrefixAny(l, "#", ";"):
		case strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") && !strings.Contains(l, ","):
			sections++
		case pair && isCodeIdent(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(name))):
			pairs++
		default:
			return false
		}
	}
	return sections >= 1 && pairs >= 1
}

// yamlKeyLine reports whether a line is a mapping entry, `key:` or `key: value`
// or `- key: value`, the way YAML spells one.
func yamlKeyLine(line string) bool {
	line = strings.TrimPrefix(line, "- ")
	i := strings.IndexByte(line, ':')
	if i <= 0 || i+1 < len(line) && line[i+1] != ' ' {
		return false
	}
	return isCodeIdent(strings.NewReplacer("-", "_", ".", "_", "/", "_").Replace(strings.Trim(line[:i], `"'`)))
}

// looksLikeYAML reports whether the lines are YAML: at least two mapping
// entries, and at most as many other lines as there are entries (a block
// scalar's text, say). Comments, `---` and bare list items are neutral: a list
// alone is not a mapping.
func looksLikeYAML(lines []string) bool {
	keys, other := 0, 0
	for _, l := range lines {
		switch {
		case hasPrefixAny(l, "#") || l == "---" || l == "..." || l == "-" || strings.HasPrefix(l, "- ") && !yamlKeyLine(l):
		case yamlKeyLine(l):
			keys++
		default:
			other++
		}
	}
	return keys >= 2 && other <= keys
}
