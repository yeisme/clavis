package uiapp

import (
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// searchFieldState is the view state of the Leader's and the Omnibox's
// search fields (uiViewState.search).
type searchFieldState struct {
	// caretEnd puts the caret of the Omnibox field after its text in the
	// next frame: TextKey.SetText, which accepts a completion, leaves the
	// caret where it was, inside the completed word.
	caretEnd bool
}

// searchKeyRole is what one of the arbitrated keys of a search field does.
// The order of the arbitration is docs/design/interaction-spec.md §3.1:
// IME preedit > inline completion > tab navigation. Tab never both completes
// and switches tabs: it completes, or it does nothing.
type searchKeyRole int

const (
	// searchKeyOther is a key outside the arbitration (Enter, the letters…).
	searchKeyOther searchKeyRole = iota
	// searchKeyIME: an input method is composing, the key is its own.
	searchKeyIME
	// searchKeyAccept: Tab with an inline completion takes it.
	searchKeyAccept
	// searchKeyInert: Tab with nothing to complete, or Shift+Tab while a
	// completion shows. Taken, so that focus does not leave the field, and
	// nothing else happens.
	searchKeyInert
	// searchKeyNextTab: Shift+Tab with nothing to complete, or Cmd+→.
	searchKeyNextTab
	// searchKeyPrevTab: Cmd+←.
	searchKeyPrevTab
)

// arbitrateSearchKey decides one key of a search field: Tab, Shift+Tab,
// Cmd+← and Cmd+→ (Ctrl outside macOS). composing is an IME preedit in the
// field; completing is an inline completion it shows.
func arbitrateSearchKey(key ui.Key, mods ui.Modifiers, composing, completing bool) searchKeyRole {
	tab := key == ui.KeyTab && mods == 0
	shiftTab := key == ui.KeyTab && mods == ui.Shift
	arrow := (key == ui.KeyLeft || key == ui.KeyRight) && isPrimaryMod(mods)
	switch {
	case !tab && !shiftTab && !arrow:
		return searchKeyOther
	case composing:
		return searchKeyIME
	case tab && completing:
		return searchKeyAccept
	case tab:
		return searchKeyInert
	case shiftTab && completing:
		return searchKeyInert
	case shiftTab, key == ui.KeyRight:
		return searchKeyNextTab
	default:
		return searchKeyPrevTab
	}
}

// imeOwnsKey reports whether an IME preedit keeps the key away from the
// panel: every unmodified key and Shift ones (Enter, the arrows, Tab, Esc
// pick, page and cancel candidates), and the arbitrated chords Cmd+←/→.
// Other Cmd/Ctrl chords stay the panel's (spec §2.2).
func imeOwnsKey(key ui.Key, mods ui.Modifiers) bool {
	if mods == 0 || mods == ui.Shift {
		return true
	}
	return (key == ui.KeyLeft || key == ui.KeyRight) && isPrimaryMod(mods)
}

// inlineCompletion completes typed to title when title starts with it,
// ignoring case: what was typed stays as typed, the rest of title follows.
func inlineCompletion(typed, title string) (string, bool) {
	if typed == "" {
		return "", false
	}
	rest := title
	for _, r := range typed {
		t, size := utf8.DecodeRuneInString(rest)
		if size == 0 || !strings.EqualFold(string(r), string(t)) {
			return "", false
		}
		rest = rest[size:]
	}
	if rest == "" {
		return "", false
	}
	return typed + rest, true
}

// omniboxCompletionTitle is the text the Omnibox query completes toward:
// the chosen row's title. Clipboard rows are not completed, their titles
// are what was copied; neither is a routed query (> ; @ ? : = /), whose
// rows are titled without the prefix.
func omniboxCompletionTitle(state engine.State) (string, bool) {
	if state.Phase != engine.PhaseOmnibox || state.OmniboxTab == config.TabClipboard {
		return "", false
	}
	if _, _, routed := engine.ParseOmniboxPrefix(state.OmniboxQuery); routed {
		return "", false
	}
	item, ok := selectedOmniboxItem(state)
	if !ok || item.Kind == engine.ItemClipboardEntry || item.Title == "" {
		return "", false
	}
	return item.Title, true
}

// omniboxCompletion is the query the Omnibox shows completed, if any.
func omniboxCompletion(state engine.State) (string, bool) {
	title, ok := omniboxCompletionTitle(state)
	if !ok {
		return "", false
	}
	return inlineCompletion(state.OmniboxQuery, title)
}

// shiftTabLabel is the printable Shift+Tab chord.
func shiftTabLabel() string {
	if runtime.GOOS == "darwin" {
		return "⇧Tab"
	}
	return "Shift+Tab"
}

// uiText picks the Chinese or the English text of a string the search
// field owns. The shared tables live in pkg/core/i18n, which this change
// does not touch; Chinese and English are the only languages they ship.
func uiText(zh, en string) string {
	if i18n.IsChinese() {
		return zh
	}
	return en
}

// searchTabHint is the footer hint the Tab arbitration leaves for the field:
// "Tab Complete" while there is a completion, "⇧Tab Switch tab" otherwise.
func searchTabHint(completing bool) footerHint {
	if completing {
		return footerHint{keys: "Tab", caption: uiText("补全", "Complete")}
	}
	return footerHint{keys: shiftTabLabel(), caption: i18n.T("omnibox.footer.switch_tab")}
}

// footerHint is one keyHint of a footer row. drop orders what gives way when
// the row runs out of room: the highest goes first.
type footerHint struct {
	keys, caption string
	tone          keyTone
	drop          int
}

// keyHintWidth is the width keyHint lays shortcut and caption out in: each
// key a kitKeycap (at least space18 square, space4 side padding, 1px
// border), space2 between the caps, space4 before the caption.
func keyHintWidth(c *ui.Context, shortcut, caption string) float32 {
	var w float32
	for i, k := range splitShortcut(shortcut) {
		size := typeMicro
		if utf8.RuneCountInString(k) == 1 {
			size = typeMeta
		}
		lw, _ := c.MeasureText(0, ui.Span{Text: k, Size: size, Weight: 600})
		w += max(space18, lw+2*space4+2*space1)
		if i > 0 {
			w += space2
		}
	}
	if caption != "" {
		cw, _ := c.MeasureText(0, ui.Span{Text: caption, Size: typeMeta})
		w += space4 + cw
	}
	return w
}

// fitFooterHints drops hints, highest drop first, until the row of them, a
// space1 divider and two space8 gaps between neighbours, fits room. The
// first-ranked hint always stays.
func fitFooterHints(c *ui.Context, hints []footerHint, room float32) []footerHint {
	width := func(hs []footerHint) float32 {
		var w float32
		for i, h := range hs {
			w += keyHintWidth(c, h.keys, h.caption)
			if i > 0 {
				w += space1 + 2*space8
			}
		}
		return w
	}
	for len(hints) > 1 && width(hints) > room {
		worst := 0
		for i, h := range hints {
			if h.drop > hints[worst].drop {
				worst = i
			}
		}
		hints = append(hints[:worst:worst], hints[worst+1:]...)
	}
	return hints
}

// renderFooterHints lays hints out as the footer does: keyHints between
// hairline dividers.
func renderFooterHints(c *ui.Context, theme *ui.Theme, hints []footerHint) {
	ui.Row(c).Gap(space8).AlignItems(ui.Center).Children(func() {
		for i, h := range hints {
			if i > 0 {
				ui.Box(c).Width(space1).Height(space12).Background(hairline(theme))
			}
			keyHint(c, theme, h.keys, h.caption, h.tone)
		}
	})
}

// switchOmniboxTab moves the Omnibox to tab, keeping its query, and closes
// the action menu that belonged to the old tab's row.
func (a *AppController) switchOmniboxTab(tab config.CapsuleTab) {
	capsuleUI.setActionMenuShown(a, false)
	a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: tab})
}

// drawCompletionGhost paints the rest of an inline completion after what
// was typed in a single-line search field, muted like its placeholder, the
// way a shell or an address bar shows what Tab takes. The typed text is
// measured as the field draws it (size, weight 500 from its TextRanges)
// from the field's own inset, its padding Space(2.5) and 1px border. A rest
// that would not fit is left out: the field scrolls its text then, and the
// footer still says "Tab Complete".
func drawCompletionGhost(in ui.Element, theme *ui.Theme, typed, rest string, size float32) {
	if typed == "" || rest == "" {
		return
	}
	in.AutoComplete(ui.AutoCompleteInline)
	in.DrawOver(func(p *ui.Painter, r ui.Rect) {
		inset := theme.Space(2.5) + 1
		typedW, _ := p.MeasureText(0, ui.Span{Text: typed, Size: size, Weight: 500})
		ghost := ui.Span{Text: rest, Size: size, Color: theme.TextMuted}
		restW, restH := p.MeasureText(0, ghost)
		if inset+typedW+restW > r.W-inset {
			return
		}
		p.RichText(r.X+inset+typedW, r.Y+(r.H-restH)/2, 0, ghost)
	})
}
