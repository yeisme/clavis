package uiapp

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

const composerHistoryLimit = 30

// composerChip represents a real, removable context attachment in the AI
// composer (selection, clipboard preview, active app, glossary, or agent session).
type composerChip struct {
	ID      string
	Kind    string // "selection", "clipboard", "app", "agent", "glossary"
	Icon    string
	Label   string
	Detail  string
	Payload string
}

type composerMenuKind uint8

const (
	composerMenuNone composerMenuKind = iota
	composerMenuMention
	composerMenuSlash
)

type composerCompletionItem struct {
	ID       string
	Trigger  string
	Title    string
	Subtitle string
	Icon     string
	Chip     *composerChip
	ExpandTo string
}

type composerState struct {
	mu               sync.Mutex
	chips            []composerChip
	history          []string
	historyIdx       int // -1 when not browsing history
	historyDraft     string
	menuSelected     int
	dismissedToken   string
	pendingSelection bool
}

func composerFor(a *AppController) *composerState {
	return a.composerStateFor()
}

func resetComposerState(a *AppController) {
	if a == nil {
		return
	}
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	// Preserve sent input history across turns in the same session,
	// while clearing ephemeral menu/selection/chips on Capsule close.
	st.chips = nil
	st.historyIdx = -1
	st.historyDraft = ""
	st.menuSelected = 0
	st.dismissedToken = ""
	st.pendingSelection = false
}

func (a *AppController) composerChips() []composerChip {
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.chips) == 0 {
		return nil
	}
	return append([]composerChip(nil), st.chips...)
}

func (a *AppController) addComposerChip(chip composerChip) {
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, existing := range st.chips {
		if existing.ID == chip.ID {
			return
		}
	}
	st.chips = append(st.chips, chip)
}

func (a *AppController) removeComposerChip(id string) {
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.chips) == 0 {
		return
	}
	kept := st.chips[:0]
	for _, ch := range st.chips {
		if ch.ID != id {
			kept = append(kept, ch)
		}
	}
	st.chips = kept
}

func (a *AppController) popLastComposerChip() bool {
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.chips) == 0 {
		return false
	}
	st.chips = st.chips[:len(st.chips)-1]
	return true
}

func (a *AppController) recordComposerHistory(prompt string) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return
	}
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.history) == 0 || st.history[len(st.history)-1] != trimmed {
		st.history = append(st.history, trimmed)
		if len(st.history) > composerHistoryLimit {
			st.history = st.history[len(st.history)-composerHistoryLimit:]
		}
	}
	st.historyIdx = -1
	st.historyDraft = ""
}

func (a *AppController) stepComposerHistory(delta int, currentText string) (string, bool) {
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.history) == 0 {
		return currentText, false
	}
	if delta < 0 {
		// Step backward into older prompts.
		if st.historyIdx == -1 {
			st.historyDraft = currentText
			st.historyIdx = len(st.history) - 1
		} else if st.historyIdx > 0 {
			st.historyIdx--
		} else {
			return st.history[0], true
		}
		return st.history[st.historyIdx], true
	}
	// Step forward toward newer prompts / saved draft.
	if st.historyIdx == -1 {
		return currentText, false
	}
	if st.historyIdx < len(st.history)-1 {
		st.historyIdx++
		return st.history[st.historyIdx], true
	}
	st.historyIdx = -1
	draft := st.historyDraft
	st.historyDraft = ""
	return draft, true
}

func (a *AppController) composerHistoryPosition() (idx, total int) {
	st := a.composerStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.historyIdx < 0 || len(st.history) == 0 {
		return -1, 0
	}
	return st.historyIdx + 1, len(st.history)
}

// detectComposerTrigger inspects the current composer text and reports whether
// an active @context mention or /command token is being typed at the end.
func detectComposerTrigger(text string) (kind composerMenuKind, tokenStart int, needle string) {
	if text == "" {
		return composerMenuNone, -1, ""
	}
	// Find the start of the last whitespace-delimited token (also respects newlines).
	lastSep := strings.LastIndexAny(text, " \t\n\r")
	start := lastSep + 1
	token := text[start:]
	if len(token) == 0 {
		return composerMenuNone, -1, ""
	}
	switch token[0] {
	case '@':
		return composerMenuMention, start, token[1:]
	case '/':
		// Slash command triggers only at the start of the input or start of a line
		// so paths/URLs like "foo/bar" never open the slash menu.
		if start == 0 || text[start-1] == '\n' || text[start-1] == '\r' {
			return composerMenuSlash, start, token[1:]
		}
	}
	return composerMenuNone, -1, ""
}

func (a *AppController) composerMentionCandidates(state engine.State, cfg config.ClavisConfig, needle string) []composerCompletionItem {
	zh := i18n.IsChinese()
	q := strings.ToLower(strings.TrimSpace(needle))
	items := make([]composerCompletionItem, 0, 8)

	addIfMatch := func(item composerCompletionItem) {
		if q != "" {
			hay := strings.ToLower(item.Trigger + " " + item.Title + " " + item.Subtitle)
			if !strings.Contains(hay, q) {
				return
			}
		}
		items = append(items, item)
	}

	// 1. Real selected text / source text
	sel := strings.TrimSpace(state.AISourceText)
	if sel == "" {
		sel = strings.TrimSpace(state.Context.SelectedText)
	}
	if sel != "" {
		runes := utf8.RuneCountInString(sel)
		title := "Selection"
		sub := fmt.Sprintf("%d chars from %s", runes, nonEmptyOr(state.Context.AppName, "active app"))
		if zh {
			title = "选中文本"
			sub = fmt.Sprintf("%d 字 · %s", runes, nonEmptyOr(state.Context.AppName, "当前应用"))
		}
		chip := composerChip{
			ID:      "selection",
			Kind:    "selection",
			Icon:    "text.badge.plus",
			Label:   title,
			Detail:  fmt.Sprintf("%d", runes),
			Payload: sel,
		}
		addIfMatch(composerCompletionItem{
			ID: "mention:selection", Trigger: "@selection", Title: title, Subtitle: sub,
			Icon: chip.Icon, Chip: &chip,
		})
	}

	// 2. Real clipboard preview
	clip := strings.TrimSpace(state.Context.ClipboardPreview)
	if clip != "" {
		title := "Clipboard"
		if zh {
			title = "剪贴板"
		}
		snippet := agentsession.SanitizeDiagnosticText(clip)
		chip := composerChip{
			ID:      "clipboard",
			Kind:    "clipboard",
			Icon:    "doc.on.clipboard",
			Label:   title,
			Detail:  truncateRunes(snippet, 18),
			Payload: clip,
		}
		addIfMatch(composerCompletionItem{
			ID: "mention:clipboard", Trigger: "@clipboard", Title: title, Subtitle: snippet,
			Icon: chip.Icon, Chip: &chip,
		})
	}

	// 3. Active app context
	if app := strings.TrimSpace(state.Context.AppName); app != "" {
		title := "Active App"
		if zh {
			title = "当前应用"
		}
		chip := composerChip{
			ID:      "app:" + app,
			Kind:    "app",
			Icon:    "macwindow",
			Label:   app,
			Payload: "Active application: " + app,
		}
		addIfMatch(composerCompletionItem{
			ID: "mention:app", Trigger: "@app", Title: title, Subtitle: app,
			Icon: chip.Icon, Chip: &chip,
		})
	}

	// 4. Configured glossary terms
	if len(cfg.Glossary.Terms) > 0 {
		title := "Glossary"
		sub := fmt.Sprintf("%d configured terms", len(cfg.Glossary.Terms))
		if zh {
			title = "术语表"
			sub = fmt.Sprintf("已配置 %d 条术语", len(cfg.Glossary.Terms))
		}
		names := make([]string, 0, min(8, len(cfg.Glossary.Terms)))
		for i, term := range cfg.Glossary.Terms {
			if i >= 8 {
				break
			}
			names = append(names, term.Term)
		}
		chip := composerChip{
			ID:      "glossary",
			Kind:    "glossary",
			Icon:    "character.book.closed",
			Label:   title,
			Detail:  fmt.Sprintf("%d", len(cfg.Glossary.Terms)),
			Payload: "Glossary terms: " + strings.Join(names, ", "),
		}
		addIfMatch(composerCompletionItem{
			ID: "mention:glossary", Trigger: "@glossary", Title: title, Subtitle: sub,
			Icon: chip.Icon, Chip: &chip,
		})
	}

	// 5. Real cached Agent sessions (redacted!)
	sessions, _ := a.cachedAgentData()
	for i, s := range sessions {
		if i >= 3 {
			break
		}
		provider := agentProviderLabel(s.Provider)
		summary := agentOneLineConclusion(s)
		chip := composerChip{
			ID:      "agent:" + s.ID,
			Kind:    "agent",
			Icon:    "person.crop.circle",
			Label:   provider + ": " + truncateRunes(s.Title, 16),
			Payload: fmt.Sprintf("Agent (%s · %s): %s", provider, s.Title, summary),
		}
		addIfMatch(composerCompletionItem{
			ID: "mention:agent:" + s.ID, Trigger: "@" + strings.ToLower(provider),
			Title: provider + " · " + s.Title, Subtitle: summary,
			Icon: chip.Icon, Chip: &chip,
		})
	}

	return items
}

func composerSlashCandidates(cfg config.ClavisConfig, needle string) []composerCompletionItem {
	zh := i18n.IsChinese()
	q := strings.ToLower(strings.TrimSpace(needle))
	builtins := []composerCompletionItem{
		{
			ID: "slash:concise", Trigger: "/concise", Icon: "wand.and.stars",
			Title:    pickLang(zh, "精简润色", "Make concise"),
			Subtitle: pickLang(zh, "保留原意，删去冗余表达", "Keep meaning, tighten wording"),
			ExpandTo: pickLang(zh, "请将上述内容改写得更精简清晰：", "Rewrite concisely while keeping the exact meaning: "),
		},
		{
			ID: "slash:bullets", Trigger: "/bullets", Icon: "text.badge.checkmark",
			Title:    pickLang(zh, "提炼三点摘要", "3-bullet summary"),
			Subtitle: pickLang(zh, "用三条要点归纳核心结论", "Distill into three crisp bullet points"),
			ExpandTo: pickLang(zh, "请用 3 条要点总结核心结论：", "Summarize the key points in 3 bullets: "),
		},
		{
			ID: "slash:explain", Trigger: "/explain", Icon: "sparkles",
			Title:    pickLang(zh, "逐步解释", "Explain step by step"),
			Subtitle: pickLang(zh, "拆解逻辑、边界条件与原因", "Break down logic, edge cases, and rationale"),
			ExpandTo: pickLang(zh, "请分步骤解释以下内容的实现逻辑：", "Explain this step by step: "),
		},
		{
			ID: "slash:translate", Trigger: "/translate", Icon: "globe",
			Title:    pickLang(zh, "中英互译", "Translate"),
			Subtitle: pickLang(zh, "自然地道地翻译并保持术语一致", "Translate naturally preserving technical terms"),
			ExpandTo: pickLang(zh, "请将以下内容自然翻译为英文：", "Translate naturally into English: "),
		},
		{
			ID: "slash:fix", Trigger: "/fix", Icon: "checkmark",
			Title:    pickLang(zh, "修正语法与错字", "Fix grammar & typos"),
			Subtitle: pickLang(zh, "只做最小必要修改，不改变语气", "Minimal fixes without changing tone"),
			ExpandTo: pickLang(zh, "请修正语法与错别字，保持原有语气：", "Fix grammar and typos with minimal edits: "),
		},
		{
			ID: "slash:review", Trigger: "/review", Icon: "terminal",
			Title:    pickLang(zh, "审查边界与风险", "Review risks & edge cases"),
			Subtitle: pickLang(zh, "指出潜在 bug、竞态或安全隐患", "Flag bugs, races, and edge cases"),
			ExpandTo: pickLang(zh, "请审查以下代码的边界条件与潜在风险：", "Review for bugs, races, and edge cases: "),
		},
	}

	items := make([]composerCompletionItem, 0, len(builtins)+len(cfg.Prompts.Items))
	match := func(it composerCompletionItem) bool {
		if q == "" {
			return true
		}
		hay := strings.ToLower(it.Trigger + " " + it.Title + " " + it.Subtitle)
		return strings.Contains(hay, q)
	}
	for _, it := range builtins {
		if match(it) {
			items = append(items, it)
		}
	}
	for _, tpl := range cfg.Prompts.Items {
		trig := strings.TrimSpace(tpl.Trigger)
		if trig == "" {
			continue
		}
		if !strings.HasPrefix(trig, "/") {
			trig = "/" + trig
		}
		it := composerCompletionItem{
			ID:       "slash:tpl:" + tpl.ID,
			Trigger:  trig,
			Title:    nonEmptyOr(tpl.Title, tpl.ID),
			Subtitle: nonEmptyOr(tpl.Description, truncateRunes(strings.TrimSpace(tpl.Template), 48)),
			Icon:     "wand.and.stars",
			ExpandTo: strings.TrimSpace(tpl.Template),
		}
		if match(it) {
			items = append(items, it)
		}
	}
	if len(items) > 6 {
		items = items[:6]
	}
	return items
}

func pickLang(zh bool, zhText, enText string) string {
	if zh {
		return zhText
	}
	return enText
}

func nonEmptyOr(v, fallback string) string {
	if t := strings.TrimSpace(v); t != "" {
		return t
	}
	return fallback
}

func truncateRunes(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen || maxLen <= 1 {
		return s
	}
	return string(r[:maxLen-1]) + "…"
}

// composePromptWithChips combines attached context chips with the typed prompt
// so EventFollowUpAI carries the real selected context without fabricating data.
func (a *AppController) composePromptWithChips(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	chips := a.composerChips()
	if len(chips) == 0 {
		return trimmed
	}
	ctxLines := make([]string, 0, len(chips))
	for _, ch := range chips {
		if p := strings.TrimSpace(ch.Payload); p != "" {
			ctxLines = append(ctxLines, fmt.Sprintf("[%s] %s", ch.Label, p))
		}
	}
	if len(ctxLines) == 0 {
		return trimmed
	}
	if trimmed == "" {
		return strings.Join(ctxLines, "\n")
	}
	return strings.Join(ctxLines, "\n") + "\n\n" + trimmed
}

func (a *AppController) applyComposerCompletion(text string, tokenStart int, item composerCompletionItem) string {
	prefix := ""
	if tokenStart > 0 && tokenStart <= len(text) {
		prefix = text[:tokenStart]
	}
	if item.Chip != nil {
		a.addComposerChip(*item.Chip)
		return strings.TrimRight(prefix, " ")
	}
	return prefix + item.ExpandTo
}

func (a *AppController) composerActiveMenu(cfg config.ClavisConfig, state engine.State) (composerMenuKind, int, []composerCompletionItem) {
	if a.followUpComposing() {
		return composerMenuNone, -1, nil
	}
	text := a.uiState.followUpQuery
	menuKind, tokenStart, needle := detectComposerTrigger(text)
	if menuKind == composerMenuNone {
		return composerMenuNone, -1, nil
	}
	st := composerFor(a)
	if tokenStart >= 0 && tokenStart < len(text) && text[tokenStart:] == st.dismissedToken {
		return composerMenuNone, -1, nil
	}
	var items []composerCompletionItem
	switch menuKind {
	case composerMenuMention:
		items = a.composerMentionCandidates(state, cfg, needle)
	case composerMenuSlash:
		items = composerSlashCandidates(cfg, needle)
	}
	if len(items) == 0 {
		return composerMenuNone, -1, nil
	}
	return menuKind, tokenStart, items
}

func (a *AppController) composerTryAcceptCompletion(cfg config.ClavisConfig, state engine.State) bool {
	_, tokenStart, items := a.composerActiveMenu(cfg, state)
	if len(items) == 0 {
		return false
	}
	st := composerFor(a)
	idx := st.menuSelected
	if idx < 0 || idx >= len(items) {
		idx = 0
	}
	updated := a.applyComposerCompletion(a.uiState.followUpQuery, tokenStart, items[idx])
	a.uiState.followUpQuery = updated
	st.menuSelected = 0
	st.dismissedToken = ""
	return true
}

func (a *AppController) composerDismissActiveMenu(cfg config.ClavisConfig, state engine.State) bool {
	_, tokenStart, items := a.composerActiveMenu(cfg, state)
	if len(items) == 0 {
		return false
	}
	st := composerFor(a)
	text := a.uiState.followUpQuery
	if tokenStart >= 0 && tokenStart < len(text) {
		st.dismissedToken = text[tokenStart:]
	}
	st.menuSelected = 0
	return true
}

// renderInputComposer renders the upgraded multi-line AI Input Composer with:
//   - Elevated @context and /command completion popups (keyboard + click)
//   - Removable context chips strip
//   - Auto-growing multi-line TextArea (Enter send, Shift+Enter newline)
//   - Input history navigation (Up/Down)
//   - Strict IME preedit priority (never hijacks composition keys)
//   - Clean locked/running state with active indicator & Stop button
func (a *AppController) renderInputComposer(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, state engine.State, phase aiInteractionPhase) {
	zh := i18n.IsChinese()
	st := composerFor(a)
	chips := a.composerChips()

	// Running state: keep the composer frame visible and calm with attached chips,
	// active blue dot, and an inline Stop trigger so layout stays steady.
	if phase == aiPhaseRunning {
		ui.Column(c).Key("input-composer-running").Label("input-composer-running").FillWidth().
			Padding(space8, space10).Gap(space6).Radius(radius8).
			Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
			if len(chips) > 0 {
				a.renderComposerChipsRow(c, theme, chips, false)
			}
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				kitStatusDot(c, kitStateLoadingColor(theme), true)
				runningText := pickLang(zh, "模型正在流式生成中… 按 ⌘. 可随时停止并编辑提问", "Model is streaming… Press ⌘. to stop and edit prompt")
				ui.Text(c, runningText).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
				stopLabel := pickLang(zh, "停止", "Stop")
				if ui.Button(c, stopLabel).Key("composer-stop-btn").Label(stopLabel).Shrink(0).Clicked() {
					a.stopActiveAIStream()
				}
			})
		})
		return
	}

	followUp := a.uiState.followUpQuery
	menuKind, tokenStart, needle := detectComposerTrigger(followUp)
	var menuItems []composerCompletionItem
	if menuKind != composerMenuNone && !a.followUpComposing() {
		rawToken := followUp[tokenStart:]
		if rawToken != st.dismissedToken {
			switch menuKind {
			case composerMenuMention:
				menuItems = a.composerMentionCandidates(state, cfg, needle)
			case composerMenuSlash:
				menuItems = composerSlashCandidates(cfg, needle)
			}
		}
	} else if menuKind == composerMenuNone {
		st.dismissedToken = ""
	}
	menuOpen := len(menuItems) > 0
	if menuOpen {
		if st.menuSelected < 0 || st.menuSelected >= len(menuItems) {
			st.menuSelected = 0
		}
	} else {
		st.menuSelected = 0
	}

	placeholder := i18n.T("ai.followup_placeholder")
	sendLabel := pickLang(zh, "发送", "Send")
	switch phase {
	case aiPhaseFailed:
		placeholder = pickLang(zh, "编辑问题重试（@ 上下文 · / 命令 · ⇧Enter 换行），或按 ⌘R 原样重试…", "Edit prompt to retry (@ context · / commands · ⇧Enter newline), or ⌘R…")
		sendLabel = pickLang(zh, "重试", "Retry")
	case aiPhasePending:
		placeholder = pickLang(zh, "输入提问（@ 引用上下文 · / 命令 · ⇧Enter 换行 · Enter 发送）…", "Ask anything (@ context · / commands · ⇧Enter newline · Enter send)…")
	case aiPhaseAwaitingHuman:
		placeholder = pickLang(zh, "输入修改意见继续追问（@ 引用上下文 · / 命令 · ⇧Enter 换行）…", "Enter revision notes (@ context · / commands · ⇧Enter newline)…")
	}

	acceptMenuItem := func(idx int, k *ui.TextKey) {
		if idx < 0 || idx >= len(menuItems) {
			return
		}
		updated := a.applyComposerCompletion(a.uiState.followUpQuery, tokenStart, menuItems[idx])
		a.uiState.followUpQuery = updated
		st.menuSelected = 0
		st.dismissedToken = ""
		if k != nil {
			k.SetText(updated)
		}
		a.uiState.followUpHandle.RequestFocus(c)
	}

	submitComposer := func(text string, k *ui.TextKey) {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			a.recordComposerHistory(trimmed)
			payload := a.composePromptWithChips(trimmed)
			if k != nil {
				k.SetText("")
			}
			a.uiState.followUpQuery = ""
			a.submitAIFollowUpOrRetry(state, payload)
			return
		}
		if phase == aiPhaseFailed {
			a.submitAIFollowUpOrRetry(state, a.composePromptWithChips(""))
			return
		}
		if phase == aiPhaseDone || phase == aiPhaseAwaitingHuman {
			a.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
		}
	}

	ui.Column(c).Key("ai-center-composer").Label("ai-center-composer").FillWidth().Gap(space6).Children(func() {
		// 1. Elevated @context / /command completion popup above the composer.
		if menuOpen {
			a.renderComposerCompletionMenu(c, theme, menuKind, menuItems, st.menuSelected, func(idx int) {
				acceptMenuItem(idx, nil)
			})
		}

		// 2. Composer surface: context chips + multi-line TextArea + toolbar.
		ui.Column(c).FillWidth().Padding(space6, space8).Gap(space6).Radius(radius8).
			Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
			if len(chips) > 0 {
				a.renderComposerChipsRow(c, theme, chips, true)
			}

			lines := previewLineCount(followUp)
			minLines := 2
			if lines > 2 {
				minLines = min(lines, 5)
			}

			in := ui.TextArea(c, &followUp).
				Bind(&a.uiState.followUpHandle).
				Lines(minLines, 5).
				Placeholder(placeholder).
				Label(i18n.T("ai.followup_label")).
				FillWidth()

			// Track IME composition so candidate selection never fires menu/history/send/close.
			in.ObserveInput(func(ev ui.InputEvent) {
				switch ev.Kind {
				case ui.InputCompose:
					capsuleUI.setFollowUpComposing(a, ev.Text != "")
				case ui.InputText, ui.InputCommand:
					capsuleUI.setFollowUpComposing(a, false)
				}
			})
			if !in.Focused() {
				capsuleUI.setFollowUpComposing(a, false)
			} else {
				capsuleUI.setFollowUpComposing(a, in.Composing())
			}

			in.OnTextKey(func(k ui.TextKey) bool {
				// Priority 1: IME preedit ALWAYS owns unmodified candidate navigation & commit keys.
				if a.followUpComposing() && k.Mods == 0 {
					switch k.Key {
					case ui.KeyEnter, ui.KeyEscape, ui.KeyUp, ui.KeyDown, ui.KeyTab:
						return true
					default:
						return false
					}
				}

				// Priority 2: Active @ / completion popup navigation.
				if menuOpen && k.Mods == 0 {
					switch k.Key {
					case ui.KeyUp:
						st.menuSelected = (st.menuSelected - 1 + len(menuItems)) % len(menuItems)
						return true
					case ui.KeyDown:
						st.menuSelected = (st.menuSelected + 1) % len(menuItems)
						return true
					case ui.KeyTab, ui.KeyEnter:
						a.uiState.followUpQuery = k.Text()
						acceptMenuItem(st.menuSelected, &k)
						return true
					case ui.KeyEscape:
						if tokenStart >= 0 && tokenStart < len(followUp) {
							st.dismissedToken = followUp[tokenStart:]
						}
						return true
					}
				}

				// Priority 3: Composer shortcuts (history, chip backspace, Escape, Enter submit).
				switch {
				case k.Key == ui.KeyBackspace && k.Mods == 0 && k.Text() == "" && len(a.composerChips()) > 0:
					return a.popLastComposerChip()
				case k.Key == ui.KeyUp && k.Mods == 0 && !strings.Contains(k.Text(), "\n"):
					if recalled, ok := a.stepComposerHistory(-1, k.Text()); ok {
						k.SetText(recalled)
						a.uiState.followUpQuery = recalled
						return true
					}
				case k.Key == ui.KeyDown && k.Mods == 0 && !strings.Contains(k.Text(), "\n"):
					if recalled, ok := a.stepComposerHistory(1, k.Text()); ok {
						k.SetText(recalled)
						a.uiState.followUpQuery = recalled
						return true
					}
				case k.Key == ui.KeyTab && k.Mods == 0:
					return true
				case k.Key == ui.KeyEscape && k.Mods == 0:
					a.Dispatch(engine.Event{Type: engine.EventEscape})
					return true
				case k.Key == ui.KeyEnter && k.Mods == 0:
					text := k.Text()
					if strings.TrimSpace(text) == "" {
						text = a.uiState.followUpQuery
					}
					submitComposer(text, &k)
					return true
				}
				return false
			})

			if in.Changed() {
				a.uiState.followUpQuery = followUp
			}

			// Compact composer footer bar: @ / quick buttons, line/history indicator, and Send CTA.
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				atBtn := ui.ButtonBase(c).Key("composer-insert-at").Label("@").
					Padding(space1, space6).Radius(radius4).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
				atBtn.Children(func() {
					ui.Text(c, "@").FontSize(typeMicro).FontWeight(700).TextColor(secondaryInk(theme)).SingleLine()
				})
				if atBtn.Clicked() {
					st.dismissedToken = ""
					if a.uiState.followUpQuery != "" && !strings.HasSuffix(a.uiState.followUpQuery, " ") && !strings.HasSuffix(a.uiState.followUpQuery, "\n") {
						a.uiState.followUpQuery += " @"
					} else {
						a.uiState.followUpQuery += "@"
					}
					a.uiState.followUpHandle.RequestFocus(c)
				}

				slashBtn := ui.ButtonBase(c).Key("composer-insert-slash").Label("/").
					Padding(space1, space6).Radius(radius4).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
				slashBtn.Children(func() {
					ui.Text(c, "/").FontSize(typeMicro).FontWeight(700).TextColor(secondaryInk(theme)).SingleLine()
				})
				if slashBtn.Clicked() {
					st.dismissedToken = ""
					if strings.TrimSpace(a.uiState.followUpQuery) == "" {
						a.uiState.followUpQuery = "/"
					} else if !strings.HasSuffix(a.uiState.followUpQuery, "\n") {
						a.uiState.followUpQuery += "\n/"
					} else {
						a.uiState.followUpQuery += "/"
					}
					a.uiState.followUpHandle.RequestFocus(c)
				}

				if histIdx, histTotal := a.composerHistoryPosition(); histIdx > 0 {
					histText := fmt.Sprintf("History %d/%d", histIdx, histTotal)
					if zh {
						histText = fmt.Sprintf("历史 %d/%d", histIdx, histTotal)
					}
					ui.Text(c, histText).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
				} else if lines > 1 {
					metaText := fmt.Sprintf("%d lines · %d chars", lines, utf8.RuneCountInString(followUp))
					if zh {
						metaText = fmt.Sprintf("%d 行 · %d 字", lines, utf8.RuneCountInString(followUp))
					}
					ui.Text(c, metaText).Key("composer-multiline-meta").FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
				} else {
					hint := pickLang(zh, "⇧Enter 换行 · ↑↓ 历史", "⇧Enter newline · ↑↓ history")
					ui.Text(c, hint).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
				}

				ui.Spacer(c)

				canSend := strings.TrimSpace(a.uiState.followUpQuery) != "" || len(chips) > 0 || (phase == aiPhaseFailed && aiRetryPrompt(state) != "")
				sendBtn := ui.Button(c, sendLabel).Key("ai-center-send").Label(sendLabel).Disabled(!canSend).Shrink(0)
				if sendBtn.Clicked() && canSend {
					submitComposer(a.uiState.followUpQuery, nil)
				}
			})
		})
	})
}

func (a *AppController) renderComposerChipsRow(c *ui.Context, theme *ui.Theme, chips []composerChip, removable bool) {
	ui.Row(c).Key("composer-chips-row").Label("composer-chips-row").FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
		for _, ch := range chips {
			chip := ch
			ui.Row(c).Key("composer-chip:"+chip.ID).Padding(space2, space6).Radius(radiusChip).Gap(space4).AlignItems(ui.Center).
				Background(tileFill(theme)).Border(space1, hairline(theme)).Shrink(0).Children(func() {
				renderSemanticIcon(c, chip.Icon, typeMicro, secondaryInk(theme))
				label := chip.Label
				if chip.Detail != "" {
					label = label + " · " + chip.Detail
				}
				ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(theme.Text).SingleLine()
				if removable {
					rm := ui.ButtonBase(c).Key("composer-chip-rm:"+chip.ID).Label("Remove "+chip.Label).
						Padding(space0, space2).Radius(radius2).FocusRing(false)
					rm.Children(func() {
						renderSemanticIcon(c, "xmark", typeMicro, theme.TextMuted)
					})
					if rm.Clicked() {
						a.removeComposerChip(chip.ID)
					}
				}
			})
		}
	})
}

func (a *AppController) renderComposerCompletionMenu(
	c *ui.Context,
	theme *ui.Theme,
	kind composerMenuKind,
	items []composerCompletionItem,
	selected int,
	onPick func(int),
) {
	zh := i18n.IsChinese()
	menuKey := "composer-mention-menu"
	headerTitle := pickLang(zh, "@ 引用上下文", "@ Attach context")
	headerIcon := "doc.on.clipboard"
	if kind == composerMenuSlash {
		menuKey = "composer-slash-menu"
		headerTitle = pickLang(zh, "/ 快捷命令", "/ Slash commands")
		headerIcon = "command"
	}

	const maxVisible = 4
	start := 0
	if selected >= maxVisible {
		start = selected - maxVisible + 1
	}
	end := min(len(items), start+maxVisible)

	ui.Column(c).Key(menuKey).Label(menuKey).FillWidth().Padding(space6, space8).Gap(space2).
		Radius(radius8).Background(theme.Background).Border(space1, hairline(theme)).Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, headerIcon, typeMicro, secondaryInk(theme))
			ui.Text(c, headerTitle).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, pickLang(zh, "↑↓ 选择 · ↵/Tab 确认 · Esc 关闭", "↑↓ Navigate · ↵/Tab Insert · Esc Dismiss")).
				FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		})
		for i := start; i < end; i++ {
			idx := i
			item := items[i]
			isSel := idx == selected
			row := ui.ButtonBase(c).Key("composer-menu-item:" + item.ID).Label(item.Title).
				FillWidth().Padding(space4, space8).Radius(radiusChip).FocusRing(false)
			if isSel {
				row.Background(rowSelectionFill(theme)).Border(space1, ui.Transparent).Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(rowSelectionBarRect(r), rowSelectionBar(theme), rowSelectionBarWidth/2)
				})
			} else {
				row.Border(space1, ui.Transparent)
			}
			row.Children(func() {
				ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, item.Icon, typeMeta, secondaryInk(theme))
					ui.Text(c, item.Trigger).Font("monospace").FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine().Shrink(0)
					ui.Text(c, item.Title).FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).SingleLine().Shrink(0)
					if item.Subtitle != "" {
						ui.Text(c, item.Subtitle).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
					}
				})
			})
			if row.Clicked() {
				onPick(idx)
			}
		}
	})
}
