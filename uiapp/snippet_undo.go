package uiapp

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/i18n"
)

// snippetUndoWindow is how long Backspace still reverts the last expansion.
const snippetUndoWindow = 5 * time.Second

// snippetUndoDelay lets the user's real Backspace reach the target app
// before the revert is injected behind it (the hook passes the key through
// as it reports it, and the injection must land after it).
const snippetUndoDelay = 20 * time.Millisecond

// snippetUndoState remembers the last inline expansion until the next key.
type snippetUndoState struct {
	armed    bool
	typed    string // what the user typed for the trigger
	expanded string // what replaced it
	at       time.Time
}

// armSnippetUndo remembers an expansion so that an immediate Backspace can
// revert it, and announces it with a hint pill. Expansions that moved the
// caret ({{cursor}}) or contain multi-codepoint graphemes are not armed: the
// deletion count would no longer match what Backspace removes.
func (c *AppController) armSnippetUndo(typed, expanded string, cursorBackOffset int) {
	cfg := c.ConfigStore.Current()
	if !cfg.Main.Trigger.SnippetBackspaceUndo || cursorBackOffset != 0 ||
		typed == "" || expanded == "" || !undoSafeExpansion(expanded) {
		c.disarmSnippetUndo()
		return
	}
	c.mu.Lock()
	c.snippetUndo = snippetUndoState{armed: true, typed: typed, expanded: expanded, at: time.Now()}
	pillCb := c.OnShowIMEPill
	c.mu.Unlock()
	if pillCb != nil && cfg.Main.IME.ShowFloatingPill {
		pillCb(i18n.Tf("snippet.expanded_undo_hint", typed), nil)
	}
}

func (c *AppController) disarmSnippetUndo() {
	c.mu.Lock()
	c.snippetUndo = snippetUndoState{}
	c.mu.Unlock()
}

// tryUndoSnippet reverts the last expansion when Backspace comes right after
// it. The user's Backspace has already removed the expansion's last rune, so
// the revert deletes the rest and types the original trigger back. The
// restored trigger is not fed to the snippet engine (injected text is
// filtered by the hook), so it doesn't expand again on the next boundary.
func (c *AppController) tryUndoSnippet() bool {
	c.mu.Lock()
	u := c.snippetUndo
	c.snippetUndo = snippetUndoState{}
	c.mu.Unlock()
	if !u.armed || time.Since(u.at) > snippetUndoWindow {
		return false
	}
	del := max(utf8.RuneCountInString(u.expanded)-1, 0)
	time.AfterFunc(snippetUndoDelay, func() {
		c.injectTextAsync(u.typed, del, 0)
	})
	return true
}

// undoSafeExpansion reports whether one Backspace per rune removes text
// exactly: emoji ZWJ sequences, variation selectors, skin tones, flags and
// combining marks are single graphemes spanning several runes, so the count
// would over-delete.
func undoSafeExpansion(s string) bool {
	if strings.ContainsAny(s, "‍️︎") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 0x1F3FB && r <= 0x1F3FF, // skin tone modifiers
			r >= 0x1F1E6 && r <= 0x1F1FF, // regional indicators (flags)
			unicode.Is(unicode.Mn, r),    // combining marks
			r == '\r':                    // CRLF reads as one Backspace
			return false
		}
	}
	return true
}
