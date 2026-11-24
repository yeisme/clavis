package uiapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
)

// AI continuation ("续写"): the user finishes a thought, presses a configured chord, and gets
// the sentence they most likely type next, inserted at the caret.
//
// Deliberately explicit, never automatic. Each request costs seconds and uploads whatever the
// user has typed so far — a feature that fired on its own would eventually upload a password
// typed into a field that merely looked like a draft. An explicit chord means the user decided
// to send that text.
//
// Scope note: this inserts the single best suggestion. Listing three and letting the user pick
// needs a chooser surface; that is tracked as follow-up work rather than faked here.

const (
	// aiContinueContextChars is how much text before the caret is sent: two or three sentences
	// is enough for a plausible continuation and keeps the payload small.
	aiContinueContextChars = 200
	// aiContinueTimeout is longer than the per-keystroke candidate timeout. This is a
	// deliberate request, so a few seconds is acceptable where a keystroke budget would not be.
	aiContinueTimeout = 8 * time.Second
	// aiContinueOptions is how many alternatives the model is asked for. Only the first is
	// inserted today; asking for several keeps the prompt honest and allows a chooser later.
	aiContinueOptions = 3
)

// AIContinuePrompt builds the request. Separate so it can be tested without a client.
func AIContinuePrompt(contextText string) string {
	var b strings.Builder
	b.WriteString("Continue the text below with the sentence the writer most likely types next. ")
	fmt.Fprintf(&b, "Reply with at most %d options, one per line, no numbering, ", aiContinueOptions)
	b.WriteString("no quotes, no explanation. Match the language and tone of the text. ")
	b.WriteString("Each option must be one sentence and must read as a natural continuation.\n\n")
	b.WriteString("Text so far:\n")
	b.WriteString(contextText)
	return b.String()
}

// ParseAIContinuation extracts usable lines from a model reply. List markers and wrapping
// quotes are stripped rather than the line being dropped: models add them even when told not to.
func ParseAIContinuation(reply string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(reply, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "-*•"))
		// "1. foo" / "1) foo"
		if len(line) > 2 && line[1] == '.' || len(line) > 2 && line[1] == ')' {
			if line[0] >= '1' && line[0] <= '9' {
				line = strings.TrimSpace(line[2:])
			}
		}
		line = strings.Trim(line, "\"'“”「」")
		line = strings.TrimSpace(line)
		if line == "" || seen[line] || !containsHanOrLetter(line) {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if len(out) >= aiContinueOptions {
			break
		}
	}
	return out
}

func containsHanOrLetter(s string) bool {
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// tailBeforeCaret returns at most aiContinueContextChars runes of text immediately before
// the caret. Continuation is about what comes next, so only the preceding text matters.
func tailBeforeCaret(snap host.EditableFieldSnapshot) string {
	runes := []rune(snap.Value)
	caret := snap.SelectionEnd
	if caret < 0 || caret > len(runes) {
		caret = len(runes)
	}
	runes = runes[:caret]
	if len(runes) > aiContinueContextChars {
		runes = runes[len(runes)-aiContinueContextChars:]
	}
	return strings.TrimSpace(string(runes))
}

// startAIContinue captures context, asks for a continuation and inserts it. Every early return
// tells the user why: a chord that silently does nothing is indistinguishable from a chord that
// is not bound at all.
func (c *AppController) startAIContinue() {
	cfg := c.ConfigStore.Current()
	if !cfg.Main.AI.ContinueEnabled {
		c.ShowToastOrHUD(i18n.T("ai.continue.disabled"))
		return
	}
	if c.isSecureInputEnabled() {
		c.ShowToastOrHUD(i18n.T("ai.continue.secure_input_blocked"))
		return
	}
	snap, err := c.captureEditable()
	if err != nil {
		c.ShowToastOrHUD(i18n.T("ai.continue.read_field_failed"))
		return
	}
	contextText := tailBeforeCaret(snap)
	if utf8.RuneCountInString(contextText) < 2 {
		c.ShowToastOrHUD(i18n.T("ai.continue.too_short"))
		return
	}

	c.mu.Lock()
	clientPtr := c.AIClient
	profile := c.activeAIProfileLocked()
	secStore := c.SecretStore
	c.mu.Unlock()
	if clientPtr == nil {
		c.ShowToastOrHUD(i18n.T("ai.continue.not_configured"))
		return
	}

	// One-time disclosure. Continuation uploads the user's own unfinished text, which is more
	// sensitive than a pinyin string, so say so before the first send rather than after.
	if !cfg.Main.AI.ContinueDisclosed {
		preview := contextText
		if utf8.RuneCountInString(preview) > 80 {
			preview = string([]rune(preview)[:80]) + "…"
		}
		c.ShowToastOrHUD(i18n.Tf("ai.continue.disclosure", preview))
		_ = c.UpdateMainConfig(func(m *config.MainConfig) { m.AI.ContinueDisclosed = true })
	}

	regCtx, done := c.ensureRuntime().Begin(runtimecoord.AIStream, "AI continue")
	ctx, cancel := context.WithTimeout(regCtx, aiContinueTimeout)

	go func() {
		defer done()
		defer cancel()

		client := *clientPtr
		client.Config = cfg.Main.AI
		client.APIKeyProvider = func(context.Context) (string, error) {
			return secrets.ResolveProfileAPIKey(secStore, profile)
		}
		var reply strings.Builder
		err := client.StreamCompletion(ctx, ai.CompletionRequest{
			Mode:    "continue",
			Prompt:  AIContinuePrompt(contextText),
			Profile: &profile,
		}, func(delta string) {
			if reply.Len() < 4096 {
				reply.WriteString(delta)
			}
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
				return
			}
			c.ShowToastOrHUD(i18n.Tf("ai.continue.request_failed", err))
			return
		}
		if ctx.Err() != nil {
			return
		}
		options := ParseAIContinuation(reply.String())
		if len(options) == 0 {
			c.ShowToastOrHUD(i18n.T("ai.continue.no_suggestion"))
			return
		}
		c.insertContinuation(options)
	}()
}

// insertAtCaret types text into the focused field. deleteCount is 0: a continuation adds to
// what is there rather than replacing it.
func (c *AppController) insertAtCaret(text string) error {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return fmt.Errorf("no host bridge")
	}
	ctx, cancel := context.WithTimeout(context.Background(), imeSwitchTimeout)
	defer cancel()
	return br.InjectText(ctx, text, 0, 0)
}

// armContinuationUndo arms the 5-second Backspace undo state for inserted AI continuation
// text. Unlike snippet triggers (where armSnippetUndo requires a non-empty typed trigger to
// restore), continuation replaces nothing, so typed stays empty and Backspace deletes only
// the inserted runes.
func (c *AppController) armContinuationUndo(inserted string) bool {
	if c.ConfigStore != nil && !c.ConfigStore.Current().Main.Trigger.SnippetBackspaceUndo {
		c.disarmSnippetUndo()
		return false
	}
	if inserted == "" || !undoSafeExpansion(inserted) {
		c.disarmSnippetUndo()
		return false
	}
	c.mu.Lock()
	c.snippetUndo = snippetUndoState{
		armed:    true,
		typed:    "",
		expanded: inserted,
		at:       time.Now(),
	}
	c.mu.Unlock()
	return true
}

// insertContinuation writes the best suggestion at the caret and tells the user what else was
// offered, so a poor first pick is recoverable by typing rather than by re-asking.
func (c *AppController) insertContinuation(options []string) {
	best := options[0]
	if err := c.insertAtCaret(best); err != nil {
		c.ShowToastOrHUD(i18n.Tf("ai.continue.insert_failed", err))
		return
	}
	armed := c.armContinuationUndo(best)
	note := i18n.T("ai.continue.inserted")
	if len(options) > 1 {
		note = i18n.Tf("ai.continue.inserted_more", len(options)-1)
	}
	if armed {
		note += " · " + i18n.T("ai.continue.undo_hint")
	}
	c.ShowToastOrHUD(note)
}
