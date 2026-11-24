package uiapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type previewContentKind uint8

const (
	previewKindPlain previewContentKind = iota
	previewKindMarkdown
	previewKindJSON
	previewKindCode
)

const (
	defaultMarkdownMaxBytes = 24 << 10
	defaultMarkdownMaxLines = 300
	maxJSONIndentBytes      = 16 << 10
)

type markdownThemeKey struct {
	dark   bool
	accent ui.Color
	text   ui.Color
	bg     ui.Color
}

// detectPreviewContentKind classifies text in O(1) using only the leading
// clipboardSniffBytes window plus optional filename extension hint.
func detectPreviewContentKind(text string, extHint string) previewContentKind {
	ext := strings.ToLower(strings.TrimSpace(extHint))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = filepath.Ext(ext)
	}
	switch ext {
	case ".md", ".markdown":
		return previewKindMarkdown
	case ".json", ".jsonl":
		if isLikelyJSON(text) {
			return previewKindJSON
		}
		return previewKindCode
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".sh", ".bash", ".zsh", ".sql", ".yaml", ".yml", ".toml", ".xml", ".html", ".css":
		return previewKindCode
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return previewKindPlain
	}
	head := trimmed
	if len(head) > clipboardSniffBytes {
		head = head[:clipboardSniffBytes]
	}

	if isLikelyJSON(trimmed) {
		return previewKindJSON
	}
	if looksLikeMarkdownHead(head) {
		return previewKindMarkdown
	}
	if previewIsMono(head) {
		return previewKindCode
	}
	return previewKindPlain
}

func isLikelyJSON(trimmed string) bool {
	if len(trimmed) < 2 || len(trimmed) > maxJSONIndentBytes {
		return false
	}
	first := trimmed[0]
	if first != '{' && first != '[' {
		return false
	}
	return json.Valid([]byte(trimmed))
}

func looksLikeMarkdownHead(head string) bool {
	if strings.Contains(head, "```") {
		return true
	}
	lines := strings.Split(head, "\n")
	signals := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "#### "):
			signals += 2
		case strings.HasPrefix(line, "- [ ] ") || strings.HasPrefix(line, "- [x] ") || strings.HasPrefix(line, "- [X] "):
			signals += 2
		case strings.HasPrefix(line, "> "):
			signals++
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			signals++
		case strings.HasPrefix(line, "|") && strings.Contains(line[1:], "|"):
			signals++
		case strings.Contains(line, "](") && strings.Contains(line, "["):
			signals++
		case strings.Contains(line, "**") && strings.Count(line, "**") >= 2:
			signals++
		}
		if signals >= 2 {
			return true
		}
	}
	return false
}

// formatMarkdownPreviewSource converts JSON/code inputs into bounded Markdown
// fences and applies byte/line caps so huge blobs never blow the 16ms frame
// budget.
func formatMarkdownPreviewSource(raw string, kind previewContentKind, extHint string, maxBytes, maxLines int) (src string, truncated bool, omittedLines int) {
	text := raw
	switch kind {
	case previewKindJSON:
		trimmed := strings.TrimSpace(raw)
		if len(trimmed) <= maxJSONIndentBytes {
			var buf bytes.Buffer
			if json.Indent(&buf, []byte(trimmed), "", "  ") == nil {
				text = "```json\n" + buf.String() + "\n```"
			} else {
				text = "```json\n" + trimmed + "\n```"
			}
		} else {
			text = "```json\n" + trimmed + "\n```"
		}
	case previewKindCode:
		lang := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(extHint))), ".")
		if lang == "yml" {
			lang = "yaml"
		}
		text = "```" + lang + "\n" + strings.TrimRight(raw, "\r\n") + "\n```"
	}
	return prepareMarkdownBudget(text, maxBytes, maxLines)
}

// prepareMarkdownBudget trims raw Markdown cleanly to maxBytes and maxLines,
// closing any unclosed triple-backtick fence so downstream parsing stays valid.
func prepareMarkdownBudget(raw string, maxBytes, maxLines int) (body string, truncated bool, omittedLines int) {
	if maxBytes <= 0 {
		maxBytes = defaultMarkdownMaxBytes
	}
	if maxLines <= 0 {
		maxLines = defaultMarkdownMaxLines
	}
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	if len(s) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		rest := s[cut:]
		omittedLines += strings.Count(rest, "\n") + 1
		s = s[:cut]
		truncated = true
	}

	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		omittedLines += len(lines) - maxLines
		lines = lines[:maxLines]
		truncated = true
	}
	out := strings.Join(lines, "\n")
	if strings.Count(out, "```")%2 != 0 {
		out += "\n```"
	}
	return out, truncated, omittedLines
}

func isSafeExternalURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return u.Host != "" || strings.EqualFold(u.Scheme, "mailto")
	default:
		return false
	}
}

func buildClavisMarkdownTheme(t *ui.Theme) *markdown.Theme {
	linkColor := accentInk(t, surfaceBase(t))
	return &markdown.Theme{
		FontSize:            typeLabel,
		CodeFont:            "monospace",
		LineHeight:          lineProse,
		CodeLineHeight:      lineProse,
		Text:                t.Text,
		Muted:               secondaryInk(t),
		Link:                linkColor,
		Strong:              t.Text,
		StrongWeight:        600,
		Code:                t.Text,
		CodeBackground:      tileFill(t),
		QuoteBar:            hairline(t),
		QuoteText:           secondaryInk(t),
		Rule:                hairline(t),
		CodeBlockBackground: tileFill(t),
		CodeBlockBorder:     hairline(t),
		CodeBlockRadius:     radiusChip,
		TableBorder:         hairline(t),
		TableHeader:         tileFill(t),
		Checkbox:            linkColor,
	}
}

func (a *AppController) ensureMarkdownOptions(theme *ui.Theme) *markdown.Options {
	key := markdownThemeKey{
		dark:   theme.Dark,
		accent: theme.Accent,
		text:   theme.Text,
		bg:     theme.Background,
	}
	if a.uiState.mdThemeKey != key || a.uiState.mdOpts.Theme == nil {
		a.uiState.mdThemeKey = key
		a.uiState.mdOpts.Theme = buildClavisMarkdownTheme(theme)
	}
	a.uiState.mdOpts.WrapCode = true
	a.uiState.mdOpts.OnLink = func(raw string) {
		if isSafeExternalURL(raw) {
			a.openExternalURL(raw)
		}
	}
	a.uiState.mdOpts.CodeBlock = func(c *ui.Context, b *markdown.CodeBlockView) {
		renderClavisCodeBlock(c, theme, b, func(code string) {
			if err := a.writeClipboardText(code); err != nil {
				a.ShowToastOrHUD(err.Error())
				return
			}
			a.ShowToastOrHUD(i18n.T("hud.copied"))
		})
	}
	return &a.uiState.mdOpts
}

func renderClavisCodeBlock(c *ui.Context, theme *ui.Theme, b *markdown.CodeBlockView, onCopy func(string)) {
	zh := i18n.IsChinese()
	lang := strings.TrimSpace(b.Language)
	if lang == "" {
		lang = "text"
	}
	copyLbl := pickLang(zh, "复制", "Copy")

	ui.Column(c).FillWidth().Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).Clip().Children(func() {
		ui.Row(c).FillWidth().Padding(space4, space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, lang).Font("monospace").FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			ui.Spacer(c)
			btn := ui.ButtonBase(c).Key(fmt.Sprintf("md-code-copy:%d", b.Index)).Label(copyLbl).
				Padding(space1, space6).Radius(radius4).Background(theme.Surface).Border(space1, hairline(theme)).FocusRing(false)
			btn.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "doc.on.doc", typeMicro, secondaryInk(theme))
					ui.Text(c, copyLbl).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
				})
			})
			if btn.Clicked() && onCopy != nil {
				onCopy(b.Code())
			}
		})
		ui.Box(c).FillWidth().Height(space1).Background(hairline(theme))
		ui.Box(c).FillWidth().Padding(space4, space8).Children(func() {
			b.Body(c)
		})
	})
}

func (a *AppController) renderClavisMarkdown(c *ui.Context, theme *ui.Theme, raw string, streaming bool) {
	opts := a.ensureMarkdownOptions(theme)
	src, truncated, omitted := prepareMarkdownBudget(raw, defaultMarkdownMaxBytes, defaultMarkdownMaxLines)
	if strings.TrimSpace(src) == "" {
		ui.Text(c, "—").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		return
	}
	if streaming {
		markdown.Stream(c, src, opts)
	} else {
		markdown.View(c, src, opts)
	}
	if truncated && omitted > 0 {
		ui.Text(c, i18n.Tf("preview.more_lines", omitted)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
	}
}

func (a *AppController) openExternalURL(raw string) {
	if !isSafeExternalURL(raw) {
		return
	}
	if a.OpenExternal != nil {
		_ = a.OpenExternal(raw)
		return
	}
	_ = mygo.Shell.OpenExternal(raw)
}
