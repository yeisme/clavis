package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
)

// CapsuleContext captures active application and text selection context when the Capsule opens.
type CapsuleContext struct {
	BundleID         string `json:"bundleId,omitempty"`
	ProcessName      string `json:"processName,omitempty"`
	AppName          string `json:"appName,omitempty"`
	WindowTitle      string `json:"windowTitle,omitempty"`
	ActiveURL        string `json:"activeUrl,omitempty"`
	SelectedText     string `json:"selectedText,omitempty"`
	SurroundingText  string `json:"surroundingText,omitempty"`
	ClipboardPreview string `json:"clipboardPreview,omitempty"`
}

// OmniboxClipboardEntry represents a clipboard item shown in the Clipboard tab.
type OmniboxClipboardEntry struct {
	ID            string `json:"id"`
	Content       string `json:"content"`
	SourceAppName string `json:"sourceAppName,omitempty"`
	IsPinned      bool   `json:"isPinned"`
}

// InstalledAppEntry represents a discovered OS application for the launcher.
type InstalledAppEntry struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId,omitempty"`
	Path     string `json:"path,omitempty"`
}

// ChatTurn represents a single message turn in a multi-turn AI session.
type ChatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OmniboxItemKind identifies the variant of an OmniboxItem.
type OmniboxItemKind string

const (
	ItemLeaderAction   OmniboxItemKind = "leader_action"
	ItemSnippet        OmniboxItemKind = "snippet"
	ItemSwitchTab      OmniboxItemKind = "switch_tab"
	ItemClipboardEntry OmniboxItemKind = "clipboard_entry"
	ItemGlossaryTerm   OmniboxItemKind = "glossary_term"
	ItemAIMode         OmniboxItemKind = "ai_mode"
	ItemSetting        OmniboxItemKind = "setting"
	ItemUtility        OmniboxItemKind = "utility"
	ItemInstalledApp   OmniboxItemKind = "installed_app"
	ItemEmoji          OmniboxItemKind = "emoji"
	ItemAppIMERule     OmniboxItemKind = "app_ime_rule"
)

// OmniboxActionSpec describes a contextual primary, secondary, or palette action for an OmniboxItem.
type OmniboxActionSpec struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Shortcut string `json:"shortcut,omitempty"`
}

// OmniboxItem represents a single selectable row in the Omnibox.
type OmniboxItem struct {
	ID               string               `json:"id"`
	Title            string               `json:"title"`
	Subtitle         string               `json:"subtitle"`
	KeyHint          string               `json:"keyHint,omitempty"`
	Icon             string               `json:"icon,omitempty"`
	Keywords         []string             `json:"keywords,omitempty"`
	MatchedIndices   []int                `json:"matchedIndices,omitempty"`
	Score            int                  `json:"score"`
	Kind             OmniboxItemKind      `json:"kind"`
	LeaderAction     *config.LeaderAction `json:"leaderAction,omitempty"`
	Snippet          *config.SnippetItem  `json:"snippet,omitempty"`
	SwitchTab        config.CapsuleTab    `json:"switchTab,omitempty"`
	ClipboardID      string               `json:"clipboardId,omitempty"`
	ClipboardContent string               `json:"clipboardContent,omitempty"`
	GlossaryTerm     *config.GlossaryTerm `json:"glossaryTerm,omitempty"`
	AIMode           string               `json:"aiMode,omitempty"`
	AIPrompt         string               `json:"aiPrompt,omitempty"`
	SettingID        string               `json:"settingId,omitempty"`
	UtilityValue     string               `json:"utilityValue,omitempty"`
	AppBundleID      string               `json:"appBundleId,omitempty"`
	AppPath          string               `json:"appPath,omitempty"`
	IMEMode          config.IMEMode       `json:"imeMode,omitempty"`
}

// CapsulePhase identifies the current Capsule UI phase.
type CapsulePhase string

const (
	PhaseHidden      CapsulePhase = "hidden"
	PhaseLeader      CapsulePhase = "leader"
	PhaseOmnibox     CapsulePhase = "omnibox"
	PhaseAIStreaming CapsulePhase = "ai_streaming"
	PhaseHUD         CapsulePhase = "hud"
)

// State is the immutable snapshot of the Capsule state machine.
type State struct {
	Phase             CapsulePhase            `json:"phase"`
	LeaderPath        []string                `json:"leaderPath,omitempty"`
	LeaderBreadcrumbs []string                `json:"leaderBreadcrumbs,omitempty"`
	LeaderNodes       []config.LeaderNode     `json:"leaderNodes,omitempty"`
	OmniboxTab        config.CapsuleTab       `json:"omniboxTab,omitempty"`
	OmniboxQuery      string                  `json:"omniboxQuery,omitempty"`
	OmniboxItems      []OmniboxItem           `json:"omniboxItems,omitempty"`
	SelectedIndex     int                     `json:"selectedIndex"`
	AIMode            string                  `json:"aiMode,omitempty"`
	AIPrompt          string                  `json:"aiPrompt,omitempty"`
	AISourceText      string                  `json:"aiSourceText,omitempty"`
	AIOutput          string                  `json:"aiOutput,omitempty"`
	AIComplete        bool                    `json:"aiComplete,omitempty"`
	AIError           string                  `json:"aiError,omitempty"`
	AIHistory         []ChatTurn              `json:"aiHistory,omitempty"`
	ActiveAIProfileID string                  `json:"activeAiProfileId,omitempty"`
	HUDMessage        string                  `json:"hudMessage,omitempty"`
	Context           CapsuleContext          `json:"context"`
	ClipboardEntries  []OmniboxClipboardEntry `json:"clipboardEntries,omitempty"`
	InstalledApps     []InstalledAppEntry     `json:"installedApps,omitempty"`
}

// NewInitialState returns a hidden initial State.
func NewInitialState() State {
	return State{Phase: PhaseHidden}
}

// EventType identifies an input event for CapsuleStateMachine.
type EventType string

const (
	EventOpenLeader                     EventType = "open_leader"
	EventOpenOmnibox                    EventType = "open_omnibox"
	EventUpdateContext                  EventType = "update_context"
	EventSetClipboardEntries            EventType = "set_clipboard_entries"
	EventSetInstalledApps               EventType = "set_installed_apps"
	EventLeaderKeyPress                 EventType = "leader_key_press"
	EventUpdateQuery                    EventType = "update_query"
	EventSwitchTab                      EventType = "switch_tab"
	EventSelectNext                     EventType = "select_next"
	EventSelectPrevious                 EventType = "select_previous"
	EventConfirmSelection               EventType = "confirm_selection"
	EventConfirmSecondarySelection      EventType = "confirm_secondary_selection"
	EventSelectAndConfirmIndex          EventType = "select_and_confirm_index"
	EventExecuteItemAction              EventType = "execute_item_action"
	EventTogglePinSelectedClipboardItem EventType = "toggle_pin_selected_clipboard_item"
	EventDeleteSelectedClipboardItem    EventType = "delete_selected_clipboard_item"
	EventBackspace                      EventType = "backspace"
	EventEscape                         EventType = "escape"
	EventAIChunk                        EventType = "ai_chunk"
	EventAICompleted                    EventType = "ai_completed"
	EventAIFailed                       EventType = "ai_failed"
	EventFollowUpAI                     EventType = "follow_up_ai"
	EventSelectAIProfile                EventType = "select_ai_profile"
	EventAcceptAIOutput                 EventType = "accept_ai_output"
	EventCopyAIOutput                   EventType = "copy_ai_output"
	EventShowHUD                        EventType = "show_hud"
	EventDismiss                        EventType = "dismiss"
)

// Event represents an input event to the CapsuleStateMachine.
type Event struct {
	Type             EventType               `json:"type"`
	Context          *CapsuleContext         `json:"context,omitempty"`
	Tab              config.CapsuleTab       `json:"tab,omitempty"`
	Query            string                  `json:"query,omitempty"`
	Prompt           string                  `json:"prompt,omitempty"`
	ProfileID        string                  `json:"profileId,omitempty"`
	Key              string                  `json:"key,omitempty"`
	Index            int                     `json:"index,omitempty"`
	Secondary        bool                    `json:"secondary,omitempty"`
	ActionID         string                  `json:"actionId,omitempty"`
	Chunk            string                  `json:"chunk,omitempty"`
	Error            string                  `json:"error,omitempty"`
	Message          string                  `json:"message,omitempty"`
	ClipboardEntries []OmniboxClipboardEntry `json:"clipboardEntries,omitempty"`
	InstalledApps    []InstalledAppEntry     `json:"installedApps,omitempty"`
}

// EffectType identifies a side effect emitted by Reduce.
type EffectType string

const (
	EffectPresentCapsule     EffectType = "present_capsule"
	EffectClose              EffectType = "close"
	EffectRunAction          EffectType = "run_action"
	EffectStartAI            EffectType = "start_ai"
	EffectCancelAI           EffectType = "cancel_ai"
	EffectApplyAIOutput      EffectType = "apply_ai_output"
	EffectCopyText           EffectType = "copy_text"
	EffectPasteClipboardItem EffectType = "paste_clipboard_item"
	EffectPinClipboardItem   EffectType = "pin_clipboard_item"
	EffectDeleteClipboard    EffectType = "delete_clipboard_item"
	EffectBindAppIMERule     EffectType = "bind_app_ime_rule"
)

// Effect describes a side effect that the host/coordinator should perform.
type Effect struct {
	Type             EffectType           `json:"type"`
	Action           *config.LeaderAction `json:"action,omitempty"`
	Context          *CapsuleContext      `json:"context,omitempty"`
	Mode             string               `json:"mode,omitempty"`
	Prompt           string               `json:"prompt,omitempty"`
	SourceText       string               `json:"sourceText,omitempty"`
	History          []ChatTurn           `json:"history,omitempty"`
	ProfileID        string               `json:"profileId,omitempty"`
	Text             string               `json:"text,omitempty"`
	ClipboardID      string               `json:"clipboardId,omitempty"`
	ClipboardContent string               `json:"clipboardContent,omitempty"`
	IsPinned         bool                 `json:"isPinned,omitempty"`
	IMEMode          config.IMEMode       `json:"imeMode,omitempty"`
}

// StateMachine wraps State + ClavisConfig with a stateful Handle method.
type StateMachine struct {
	config config.ClavisConfig
	state  State
}

// NewStateMachine constructs a StateMachine with the given config.
func NewStateMachine(cfg config.ClavisConfig) *StateMachine {
	st := NewInitialState()
	st.ActiveAIProfileID = cfg.Main.AI.ActiveProfileID
	return &StateMachine{
		config: cfg,
		state:  st,
	}
}

// State returns a copy of the current State.
func (m *StateMachine) State() State {
	return cloneState(m.state)
}

// SetConfig updates the config used for subsequent transitions.
func (m *StateMachine) SetConfig(cfg config.ClavisConfig) {
	m.config = cfg
	if m.state.ActiveAIProfileID == "" {
		m.state.ActiveAIProfileID = cfg.Main.AI.ActiveProfileID
	}
}

// Handle reduces an event and updates internal state, returning emitted effects.
func (m *StateMachine) Handle(ev Event) []Effect {
	next, effects := Reduce(m.state, ev, m.config)
	m.state = next
	return effects
}

func effectiveProfileID(state State, cfg config.ClavisConfig) string {
	if strings.TrimSpace(state.ActiveAIProfileID) != "" {
		return strings.TrimSpace(state.ActiveAIProfileID)
	}
	return strings.TrimSpace(cfg.Main.AI.ActiveProfileID)
}

// Reduce is a pure state-transition function: (state, event, config) -> (nextState, effects).
func Reduce(state State, event Event, cfg config.ClavisConfig) (State, []Effect) {
	next := cloneState(state)
	if next.Phase == "" {
		next.Phase = PhaseHidden
	}
	if next.ActiveAIProfileID == "" && cfg.Main.AI.ActiveProfileID != "" {
		next.ActiveAIProfileID = cfg.Main.AI.ActiveProfileID
	}

	switch event.Type {
	case EventOpenLeader:
		if event.Context != nil {
			next.Context = *event.Context
		}
		clearPhaseFields(&next)
		next.Phase = PhaseLeader
		next.LeaderNodes = copyLeaderNodes(cfg.Leader.Nodes)
		return next, []Effect{{Type: EffectPresentCapsule}}

	case EventOpenOmnibox:
		if event.Context != nil {
			next.Context = *event.Context
		}
		tab := event.Tab
		if tab == "" {
			tab = config.TabActions
		}
		items := buildOmniboxItems(tab, event.Query, next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseOmnibox
		next.OmniboxTab = tab
		next.OmniboxQuery = event.Query
		next.OmniboxItems = items
		next.SelectedIndex = 0
		return next, []Effect{{Type: EffectPresentCapsule}}

	case EventUpdateContext:
		if event.Context != nil {
			next.Context = *event.Context
		}
		return next, nil

	case EventSetClipboardEntries:
		var previousSelectedID string
		if next.Phase == PhaseOmnibox &&
			next.OmniboxTab == config.TabClipboard &&
			next.SelectedIndex >= 0 && next.SelectedIndex < len(next.OmniboxItems) {
			previousSelectedID = next.OmniboxItems[next.SelectedIndex].ID
		}

		entries := make([]OmniboxClipboardEntry, len(event.ClipboardEntries))
		copy(entries, event.ClipboardEntries)
		next.ClipboardEntries = entries

		if next.Phase == PhaseOmnibox && next.OmniboxTab == config.TabClipboard {
			items := buildOmniboxItems(config.TabClipboard, next.OmniboxQuery, next, cfg)
			preservedIdx := 0
			foundPrev := false
			if previousSelectedID != "" {
				for idx, it := range items {
					if it.ID == previousSelectedID {
						preservedIdx = idx
						foundPrev = true
						break
					}
				}
			}
			if !foundPrev {
				if len(items) == 0 {
					preservedIdx = 0
				} else if next.SelectedIndex < len(items) {
					preservedIdx = next.SelectedIndex
				} else {
					preservedIdx = len(items) - 1
				}
			}
			next.OmniboxItems = items
			next.SelectedIndex = preservedIdx
		}
		return next, nil

	case EventSetInstalledApps:
		apps := make([]InstalledAppEntry, len(event.InstalledApps))
		copy(apps, event.InstalledApps)
		next.InstalledApps = apps

		if next.Phase == PhaseOmnibox && next.OmniboxTab == config.TabActions {
			items := buildOmniboxItems(config.TabActions, next.OmniboxQuery, next, cfg)
			next.OmniboxItems = items
			if len(items) == 0 {
				next.SelectedIndex = 0
			} else if next.SelectedIndex >= len(items) {
				next.SelectedIndex = len(items) - 1
			}
		}
		return next, nil

	case EventSelectAIProfile:
		if id := strings.TrimSpace(event.ProfileID); id != "" {
			next.ActiveAIProfileID = id
		}
		return next, nil

	case EventLeaderKeyPress:
		if state.Phase != PhaseLeader || event.Key == "" {
			return next, nil
		}
		for _, node := range state.LeaderNodes {
			if node.Key == event.Key {
				return executeLeaderNode(node, state.LeaderPath, state.LeaderBreadcrumbs, next, cfg)
			}
		}
		// '/' (or full-width '／') jumps directly to a clean Actions Omnibox search bar.
		downgradeQuery := event.Key
		if event.Key == "/" || event.Key == "／" {
			downgradeQuery = ""
		}
		// Unmatched key automatically downgrades to Actions Omnibox.
		items := buildOmniboxItems(config.TabActions, downgradeQuery, next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseOmnibox
		next.OmniboxTab = config.TabActions
		next.OmniboxQuery = downgradeQuery
		next.OmniboxItems = items
		next.SelectedIndex = 0
		return next, nil

	case EventUpdateQuery:
		if state.Phase != PhaseOmnibox {
			return next, nil
		}
		items := buildOmniboxItems(state.OmniboxTab, event.Query, next, cfg)
		next.OmniboxQuery = event.Query
		next.OmniboxItems = items
		next.SelectedIndex = 0
		return next, nil

	case EventSwitchTab:
		query := ""
		if state.Phase == PhaseOmnibox {
			query = state.OmniboxQuery
		}
		tab := event.Tab
		if tab == "" {
			tab = config.TabActions
		}
		items := buildOmniboxItems(tab, query, next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseOmnibox
		next.OmniboxTab = tab
		next.OmniboxQuery = query
		next.OmniboxItems = items
		next.SelectedIndex = 0
		return next, nil

	case EventSelectNext:
		if state.Phase != PhaseOmnibox || len(state.OmniboxItems) == 0 {
			return next, nil
		}
		if state.SelectedIndex+1 < len(state.OmniboxItems) {
			next.SelectedIndex = state.SelectedIndex + 1
		}
		return next, nil

	case EventSelectPrevious:
		if state.Phase != PhaseOmnibox || len(state.OmniboxItems) == 0 {
			return next, nil
		}
		if state.SelectedIndex > 0 {
			next.SelectedIndex = state.SelectedIndex - 1
		}
		return next, nil

	case EventConfirmSelection:
		if state.Phase != PhaseOmnibox || state.SelectedIndex < 0 || state.SelectedIndex >= len(state.OmniboxItems) {
			return next, nil
		}
		return executeOmniboxItemWithAction(state.OmniboxItems[state.SelectedIndex], "primary", next, cfg)

	case EventConfirmSecondarySelection:
		if state.Phase != PhaseOmnibox || state.SelectedIndex < 0 || state.SelectedIndex >= len(state.OmniboxItems) {
			return next, nil
		}
		return executeOmniboxItemWithAction(state.OmniboxItems[state.SelectedIndex], "secondary", next, cfg)

	case EventSelectAndConfirmIndex:
		if state.Phase != PhaseOmnibox || event.Index < 0 || event.Index >= len(state.OmniboxItems) {
			return next, nil
		}
		next.SelectedIndex = event.Index
		actID := "primary"
		if event.Secondary {
			actID = "secondary"
		}
		return executeOmniboxItemWithAction(state.OmniboxItems[event.Index], actID, next, cfg)

	case EventExecuteItemAction:
		if state.Phase != PhaseOmnibox || state.SelectedIndex < 0 || state.SelectedIndex >= len(state.OmniboxItems) {
			return next, nil
		}
		actID := strings.TrimSpace(event.ActionID)
		if actID == "pin" {
			return Reduce(state, Event{Type: EventTogglePinSelectedClipboardItem}, cfg)
		}
		if actID == "delete" {
			return Reduce(state, Event{Type: EventDeleteSelectedClipboardItem}, cfg)
		}
		return executeOmniboxItemWithAction(state.OmniboxItems[state.SelectedIndex], actID, next, cfg)

	case EventTogglePinSelectedClipboardItem:
		if state.Phase != PhaseOmnibox ||
			state.SelectedIndex < 0 ||
			state.SelectedIndex >= len(state.OmniboxItems) {
			return next, nil
		}
		selected := state.OmniboxItems[state.SelectedIndex]
		if selected.Kind != ItemClipboardEntry {
			return next, nil
		}
		entryIdx := -1
		for i := range next.ClipboardEntries {
			if next.ClipboardEntries[i].ID == selected.ClipboardID {
				entryIdx = i
				break
			}
		}
		if entryIdx == -1 {
			return next, nil
		}
		next.ClipboardEntries[entryIdx].IsPinned = !next.ClipboardEntries[entryIdx].IsPinned
		newPinned := next.ClipboardEntries[entryIdx].IsPinned
		updatedItems := buildOmniboxItems(state.OmniboxTab, state.OmniboxQuery, next, cfg)
		clamped := 0
		if len(updatedItems) > 0 {
			if state.SelectedIndex < len(updatedItems) {
				clamped = state.SelectedIndex
			} else {
				clamped = len(updatedItems) - 1
			}
		}
		next.OmniboxItems = updatedItems
		next.SelectedIndex = clamped
		return next, []Effect{{
			Type:        EffectPinClipboardItem,
			ClipboardID: selected.ClipboardID,
			IsPinned:    newPinned,
		}}

	case EventDeleteSelectedClipboardItem:
		if state.Phase != PhaseOmnibox ||
			state.SelectedIndex < 0 ||
			state.SelectedIndex >= len(state.OmniboxItems) {
			return next, nil
		}
		selected := state.OmniboxItems[state.SelectedIndex]
		if selected.Kind != ItemClipboardEntry {
			return next, nil
		}
		filtered := make([]OmniboxClipboardEntry, 0, len(next.ClipboardEntries))
		for _, e := range next.ClipboardEntries {
			if e.ID != selected.ClipboardID {
				filtered = append(filtered, e)
			}
		}
		next.ClipboardEntries = filtered
		updatedItems := buildOmniboxItems(state.OmniboxTab, state.OmniboxQuery, next, cfg)
		clamped := 0
		if len(updatedItems) > 0 {
			if state.SelectedIndex < len(updatedItems) {
				clamped = state.SelectedIndex
			} else {
				clamped = len(updatedItems) - 1
			}
		}
		next.OmniboxItems = updatedItems
		next.SelectedIndex = clamped
		return next, []Effect{{
			Type:        EffectDeleteClipboard,
			ClipboardID: selected.ClipboardID,
		}}

	case EventBackspace:
		switch state.Phase {
		case PhaseLeader:
			if len(state.LeaderPath) == 0 {
				clearPhaseFields(&next)
				next.Phase = PhaseHidden
				return next, []Effect{{Type: EffectClose}}
			}
			parentPath := append([]string(nil), state.LeaderPath[:len(state.LeaderPath)-1]...)
			parentBreadcrumbs := append([]string(nil), state.LeaderBreadcrumbs[:len(state.LeaderBreadcrumbs)-1]...)
			parentNodes := resolveLeaderNodes(parentPath, cfg.Leader.Nodes)
			next.LeaderPath = parentPath
			next.LeaderBreadcrumbs = parentBreadcrumbs
			next.LeaderNodes = copyLeaderNodes(parentNodes)
			return next, nil

		case PhaseOmnibox:
			if state.OmniboxQuery == "" {
				return next, nil
			}
			qRunes := []rune(state.OmniboxQuery)
			updatedQuery := string(qRunes[:len(qRunes)-1])
			items := buildOmniboxItems(state.OmniboxTab, updatedQuery, next, cfg)
			next.OmniboxQuery = updatedQuery
			next.OmniboxItems = items
			next.SelectedIndex = 0
			return next, nil
		}
		return next, nil

	case EventEscape:
		switch state.Phase {
		case PhaseAIStreaming:
			wasComplete := state.AIComplete
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			if wasComplete {
				return next, []Effect{{Type: EffectClose}}
			}
			return next, []Effect{{Type: EffectCancelAI}, {Type: EffectClose}}

		case PhaseLeader:
			if len(state.LeaderPath) == 0 {
				clearPhaseFields(&next)
				next.Phase = PhaseHidden
				return next, []Effect{{Type: EffectClose}}
			}
			parentPath := append([]string(nil), state.LeaderPath[:len(state.LeaderPath)-1]...)
			parentBreadcrumbs := append([]string(nil), state.LeaderBreadcrumbs[:len(state.LeaderBreadcrumbs)-1]...)
			parentNodes := resolveLeaderNodes(parentPath, cfg.Leader.Nodes)
			next.LeaderPath = parentPath
			next.LeaderBreadcrumbs = parentBreadcrumbs
			next.LeaderNodes = copyLeaderNodes(parentNodes)
			return next, nil

		case PhaseHidden:
			return next, nil

		case PhaseOmnibox, PhaseHUD:
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{{Type: EffectClose}}
		}
		return next, nil

	case EventAIChunk:
		if state.Phase != PhaseAIStreaming || state.AIComplete {
			return next, nil
		}
		next.AIOutput += event.Chunk
		next.AIError = ""
		return next, nil

	case EventAICompleted:
		if state.Phase != PhaseAIStreaming {
			return next, nil
		}
		next.AIComplete = true
		next.AIError = ""
		return next, nil

	case EventAIFailed:
		if state.Phase != PhaseAIStreaming {
			return next, nil
		}
		next.AIComplete = true
		next.AIError = event.Error
		return next, nil

	case EventFollowUpAI:
		if state.Phase != PhaseAIStreaming {
			return next, nil
		}
		followUpPrompt := strings.TrimSpace(event.Prompt)
		if followUpPrompt == "" {
			followUpPrompt = strings.TrimSpace(event.Query)
		}
		if followUpPrompt == "" {
			return next, nil
		}
		if strings.TrimSpace(state.AIPrompt) != "" {
			userContent := state.AIPrompt
			if len(state.AIHistory) == 0 && strings.TrimSpace(state.AISourceText) != "" {
				userContent = state.AIPrompt + "\n\n" + state.AISourceText
			}
			next.AIHistory = append(next.AIHistory, ChatTurn{
				Role:    "user",
				Content: userContent,
			})
		}
		if strings.TrimSpace(state.AIOutput) != "" {
			next.AIHistory = append(next.AIHistory, ChatTurn{
				Role:    "assistant",
				Content: state.AIOutput,
			})
		}
		next.AIPrompt = followUpPrompt
		next.AIOutput = ""
		next.AIComplete = false
		next.AIError = ""
		ctxCopy := next.Context
		histCopy := copyChatTurns(next.AIHistory)
		profID := effectiveProfileID(next, cfg)
		return next, []Effect{
			{Type: EffectCancelAI},
			{
				Type:       EffectStartAI,
				Mode:       next.AIMode,
				Prompt:     followUpPrompt,
				SourceText: next.AISourceText,
				Context:    &ctxCopy,
				History:    histCopy,
				ProfileID:  profID,
			},
		}

	case EventAcceptAIOutput:
		if state.Phase != PhaseAIStreaming || state.AIError != "" || state.AIOutput == "" {
			return next, nil
		}
		output := state.AIOutput
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectApplyAIOutput, Text: output},
			{Type: EffectClose},
		}

	case EventCopyAIOutput:
		if state.Phase != PhaseAIStreaming || state.AIOutput == "" {
			return next, nil
		}
		output := state.AIOutput
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectCopyText, Text: output},
			{Type: EffectClose},
		}

	case EventShowHUD:
		clearPhaseFields(&next)
		next.Phase = PhaseHUD
		next.HUDMessage = event.Message
		return next, []Effect{{Type: EffectPresentCapsule}}

	case EventDismiss:
		if state.Phase == PhaseHidden {
			return next, nil
		}
		if state.Phase == PhaseAIStreaming && !state.AIComplete {
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{{Type: EffectCancelAI}, {Type: EffectClose}}
		}
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{{Type: EffectClose}}
	}

	return next, nil
}

func executeLeaderNode(
	node config.LeaderNode,
	path []string,
	breadcrumbs []string,
	next State,
	cfg config.ClavisConfig,
) (State, []Effect) {
	switch node.Action.Type {
	case config.ActionGroup:
		clearPhaseFields(&next)
		next.Phase = PhaseLeader
		next.LeaderPath = append(append([]string(nil), path...), node.Key)
		next.LeaderBreadcrumbs = append(append([]string(nil), breadcrumbs...), node.Label)
		next.LeaderNodes = copyLeaderNodes(node.Action.Children)
		return next, nil

	case config.ActionAIRewrite:
		src := resolveRewriteSourceText(next.Context)
		ctxCopy := next.Context
		profID := effectiveProfileID(next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseAIStreaming
		next.AIMode = node.Action.Mode
		next.AIPrompt = node.Action.Prompt
		next.AISourceText = src
		return next, []Effect{{
			Type:       EffectStartAI,
			Mode:       node.Action.Mode,
			Prompt:     node.Action.Prompt,
			SourceText: src,
			Context:    &ctxCopy,
			ProfileID:  profID,
		}}

	case config.ActionSwitchTab:
		items := buildOmniboxItems(node.Action.Tab, "", next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseOmnibox
		next.OmniboxTab = node.Action.Tab
		next.OmniboxItems = items
		next.SelectedIndex = 0
		return next, nil

	default:
		act := node.Action
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectRunAction, Action: &act, Context: &ctxCopy},
			{Type: EffectClose},
		}
	}
}

// AvailableItemActions returns the ordered contextual actions for item (Primary first, Secondary second).
func AvailableItemActions(item OmniboxItem) []OmniboxActionSpec {
	switch item.Kind {
	case ItemClipboardEntry:
		pinLabel := "Pin Item"
		if item.KeyHint == "Pinned" {
			pinLabel = "Unpin Item"
		}
		return []OmniboxActionSpec{
			{ID: "paste", Label: "Paste to Active App", Shortcut: "↵"},
			{ID: "copy", Label: "Copy to Clipboard", Shortcut: "⌘↵"},
			{ID: "pin", Label: pinLabel, Shortcut: "⌘P"},
			{ID: "delete", Label: "Delete History Item", Shortcut: "⌘⌫"},
		}
	case ItemSnippet:
		return []OmniboxActionSpec{
			{ID: "expand", Label: "Insert Snippet", Shortcut: "↵"},
			{ID: "copy", Label: "Copy Rendered Snippet", Shortcut: "⌘↵"},
		}
	case ItemUtility:
		return []OmniboxActionSpec{
			{ID: "copy", Label: "Copy Result", Shortcut: "↵"},
			{ID: "paste", Label: "Insert Result at Cursor", Shortcut: "⌘↵"},
		}
	case ItemGlossaryTerm:
		return []OmniboxActionSpec{
			{ID: "copy", Label: "Copy Canonical Term", Shortcut: "↵"},
			{ID: "paste", Label: "Insert Term at Cursor", Shortcut: "⌘↵"},
		}
	case ItemEmoji:
		return []OmniboxActionSpec{
			{ID: "paste", Label: "Insert Emoji", Shortcut: "↵"},
			{ID: "copy", Label: "Copy Emoji", Shortcut: "⌘↵"},
		}
	case ItemInstalledApp:
		return []OmniboxActionSpec{
			{ID: "open", Label: "Open Application", Shortcut: "↵"},
			{ID: "copy_id", Label: "Copy Bundle ID / Path", Shortcut: "⌘↵"},
		}
	case ItemAIMode:
		return []OmniboxActionSpec{
			{ID: "run_ai", Label: "Run AI with Context", Shortcut: "↵"},
			{ID: "ask_only", Label: "Ask AI Without Selection", Shortcut: "⌘↵"},
		}
	case ItemAppIMERule:
		return []OmniboxActionSpec{
			{ID: "apply_rule", Label: "Apply App IME Rule", Shortcut: "↵"},
			{ID: "copy_title", Label: "Copy Rule Title", Shortcut: "⌘↵"},
		}
	default:
		return []OmniboxActionSpec{
			{ID: "primary", Label: "Run Action", Shortcut: "↵"},
			{ID: "copy_title", Label: "Copy Title", Shortcut: "⌘↵"},
		}
	}
}

func executeOmniboxItem(item OmniboxItem, next State, cfg config.ClavisConfig) (State, []Effect) {
	return executeOmniboxItemWithAction(item, "primary", next, cfg)
}

func executeOmniboxItemWithAction(item OmniboxItem, actionID string, next State, cfg config.ClavisConfig) (State, []Effect) {
	actID := strings.ToLower(strings.TrimSpace(actionID))
	if actID == "" {
		actID = "primary"
	}

	// Resolve "secondary" alias to the item's canonical secondary action ID.
	if actID == "secondary" {
		specs := AvailableItemActions(item)
		if len(specs) >= 2 {
			actID = specs[1].ID
		} else {
			actID = "primary"
		}
	}

	// Handle explicit cross-item secondary actions before falling through to primary execution.
	switch actID {
	case "copy_title":
		txt := item.Title
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectCopyText, Text: txt},
			{Type: EffectClose},
		}
	case "copy_id":
		idTxt := strings.TrimSpace(item.AppBundleID)
		if idTxt == "" {
			idTxt = strings.TrimSpace(item.AppPath)
		}
		if idTxt == "" {
			idTxt = item.Title
		}
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectCopyText, Text: idTxt},
			{Type: EffectClose},
		}
	case "ask_only":
		ctxCopy := next.Context
		profID := effectiveProfileID(next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseAIStreaming
		next.AIMode = "ask"
		next.AIPrompt = item.AIPrompt
		next.AISourceText = ""
		return next, []Effect{{
			Type:       EffectStartAI,
			Mode:       "ask",
			Prompt:     item.AIPrompt,
			SourceText: "",
			Context:    &ctxCopy,
			ProfileID:  profID,
		}}
	}

	switch item.Kind {
	case ItemClipboardEntry:
		if actID == "copy" {
			txt := item.ClipboardContent
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectCopyText, Text: txt},
				{Type: EffectClose},
			}
		}
	case ItemSnippet:
		if actID == "copy" && item.Snippet != nil {
			rendered := RenderSnippet(item.Snippet.Content, RenderOptions{
				Now:           time.Now(),
				ClipboardText: next.Context.ClipboardPreview,
				AppName:       next.Context.AppName,
			})
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectCopyText, Text: rendered.RenderedText},
				{Type: EffectClose},
			}
		}
	case ItemUtility:
		if actID == "paste" {
			val := item.UtilityValue
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectApplyAIOutput, Text: val},
				{Type: EffectClose},
			}
		}
	case ItemGlossaryTerm:
		if actID == "paste" && item.GlossaryTerm != nil {
			termText := item.GlossaryTerm.Term
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectApplyAIOutput, Text: termText},
				{Type: EffectClose},
			}
		}
	case ItemEmoji:
		if actID == "copy" {
			val := item.UtilityValue
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectCopyText, Text: val},
				{Type: EffectClose},
			}
		}
	case ItemAppIMERule:
		ctxCopy := next.Context
		mode := item.IMEMode
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectBindAppIMERule, IMEMode: mode, Context: &ctxCopy},
			{Type: EffectClose},
		}
	}

	switch item.Kind {
	case ItemLeaderAction:
		if item.LeaderAction == nil {
			return next, nil
		}
		act := *item.LeaderAction
		switch act.Type {
		case config.ActionGroup:
			clearPhaseFields(&next)
			next.Phase = PhaseLeader
			next.LeaderBreadcrumbs = []string{item.Title}
			next.LeaderNodes = copyLeaderNodes(act.Children)
			return next, nil

		case config.ActionAIRewrite:
			src := resolveRewriteSourceText(next.Context)
			ctxCopy := next.Context
			profID := effectiveProfileID(next, cfg)
			clearPhaseFields(&next)
			next.Phase = PhaseAIStreaming
			next.AIMode = act.Mode
			next.AIPrompt = act.Prompt
			next.AISourceText = src
			return next, []Effect{{
				Type:       EffectStartAI,
				Mode:       act.Mode,
				Prompt:     act.Prompt,
				SourceText: src,
				Context:    &ctxCopy,
				ProfileID:  profID,
			}}

		case config.ActionSwitchTab:
			items := buildOmniboxItems(act.Tab, "", next, cfg)
			clearPhaseFields(&next)
			next.Phase = PhaseOmnibox
			next.OmniboxTab = act.Tab
			next.OmniboxItems = items
			next.SelectedIndex = 0
			return next, nil

		default:
			ctxCopy := next.Context
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectRunAction, Action: &act, Context: &ctxCopy},
				{Type: EffectClose},
			}
		}

	case ItemSnippet:
		if item.Snippet == nil {
			return next, nil
		}
		act := config.SnippetAction(item.Snippet.ID)
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectRunAction, Action: &act, Context: &ctxCopy},
			{Type: EffectClose},
		}

	case ItemSwitchTab:
		items := buildOmniboxItems(item.SwitchTab, "", next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseOmnibox
		next.OmniboxTab = item.SwitchTab
		next.OmniboxItems = items
		next.SelectedIndex = 0
		return next, nil

	case ItemClipboardEntry:
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectPasteClipboardItem, ClipboardID: item.ClipboardID, ClipboardContent: item.ClipboardContent},
			{Type: EffectClose},
		}

	case ItemGlossaryTerm:
		if item.GlossaryTerm == nil {
			return next, nil
		}
		termText := item.GlossaryTerm.Term
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectCopyText, Text: termText},
			{Type: EffectClose},
		}

	case ItemAIMode:
		var src string
		if item.AIMode == "ask" {
			if strings.TrimSpace(next.Context.SelectedText) != "" {
				src = next.Context.SelectedText
			}
		} else {
			src = resolveRewriteSourceText(next.Context)
		}
		ctxCopy := next.Context
		profID := effectiveProfileID(next, cfg)
		clearPhaseFields(&next)
		next.Phase = PhaseAIStreaming
		next.AIMode = item.AIMode
		next.AIPrompt = item.AIPrompt
		next.AISourceText = src
		return next, []Effect{{
			Type:       EffectStartAI,
			Mode:       item.AIMode,
			Prompt:     item.AIPrompt,
			SourceText: src,
			Context:    &ctxCopy,
			ProfileID:  profID,
		}}

	case ItemSetting:
		act := config.CLIAction("clavis-setting", item.SettingID)
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectRunAction, Action: &act, Context: &ctxCopy},
			{Type: EffectClose},
		}

	case ItemUtility:
		val := item.UtilityValue
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectCopyText, Text: val},
			{Type: EffectClose},
		}

	case ItemEmoji:
		val := item.UtilityValue
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectApplyAIOutput, Text: val},
			{Type: EffectClose},
		}

	case ItemInstalledApp:
		target := strings.TrimSpace(item.AppBundleID)
		if target == "" {
			target = strings.TrimSpace(item.AppPath)
		}
		if target == "" {
			target = item.Title
		}
		act := config.OpenAppAction(target)
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectRunAction, Action: &act, Context: &ctxCopy},
			{Type: EffectClose},
		}
	}
	return next, nil
}

func resolveRewriteSourceText(ctx CapsuleContext) string {
	if ctx.SelectedText != "" {
		return ctx.SelectedText
	}
	return ctx.ClipboardPreview
}

func resolveLeaderNodes(path []string, root []config.LeaderNode) []config.LeaderNode {
	current := root
	for _, key := range path {
		found := false
		for _, node := range current {
			if node.Key == key && node.Action.Type == config.ActionGroup {
				current = node.Action.Children
				found = true
				break
			}
		}
		if !found {
			return root
		}
	}
	return current
}

// ParseOmniboxPrefix inspects rawQuery for a leading quick-route prefix (>, ;, @, ?, :, =),
// normalizing full-width prefix equivalents (＞, ；, ＠, ？, ：, ＝) typed under Chinese IME.
func ParseOmniboxPrefix(rawQuery string) (prefix rune, rest string, routed bool) {
	trimmedLeft := strings.TrimLeft(rawQuery, " \t\u3000")
	if trimmedLeft == "" {
		return 0, "", false
	}
	firstRune, size := utf8.DecodeRuneInString(trimmedLeft)
	normFirst := NormalizeFullWidthRune(firstRune)
	switch normFirst {
	case '>', ';', '@', '?', ':', '=':
		return normFirst, strings.TrimSpace(trimmedLeft[size:]), true
	}
	lower := strings.ToLower(trimmedLeft)
	if strings.HasPrefix(lower, "cb ") {
		return '@', strings.TrimSpace(trimmedLeft[3:]), true
	}
	return 0, "", false
}

func makeClipboardOmniboxItems(entries []OmniboxClipboardEntry) []OmniboxItem {
	candidates := make([]OmniboxItem, 0, len(entries))
	for _, entry := range entries {
		firstLine := firstNonEmptyLine(entry.Content)
		sub := entry.SourceAppName
		if sub == "" {
			sub = "Clipboard"
		}
		keyHint := ""
		icon := "doc.on.clipboard"
		if entry.IsPinned {
			keyHint = "Pinned"
			icon = "pin.fill"
		}
		var kws []string
		if entry.Content != "" {
			kws = append(kws, entry.Content)
		}
		if entry.SourceAppName != "" {
			kws = append(kws, entry.SourceAppName)
		}
		candidates = append(candidates, OmniboxItem{
			ID:               "clipboard:" + entry.ID,
			Title:            firstLine,
			Subtitle:         sub,
			KeyHint:          keyHint,
			Icon:             icon,
			Keywords:         kws,
			Kind:             ItemClipboardEntry,
			ClipboardID:      entry.ID,
			ClipboardContent: entry.Content,
		})
	}
	return candidates
}

func makeSettingsOmniboxItems() []OmniboxItem {
	return []OmniboxItem{
		{
			ID:        "setting:reload-config",
			Title:     "Reload Configuration",
			Subtitle:  "Reload TOML configuration files",
			Icon:      "arrow.clockwise",
			Keywords:  []string{"reload", "config", "toml", "settings", "重载配置"},
			Kind:      ItemSetting,
			SettingID: "reload-config",
		},
		{
			ID:        "setting:open-config-dir",
			Title:     "Open Configuration Folder",
			Subtitle:  "~/.config/clavis",
			Icon:      "folder",
			Keywords:  []string{"folder", "open", "config", "settings", "配置目录"},
			Kind:      ItemSetting,
			SettingID: "open-config-dir",
		},
	}
}

func makeAppIMERuleItems(ctx CapsuleContext) []OmniboxItem {
	targetLabel := strings.TrimSpace(ctx.AppName)
	if targetLabel == "" {
		targetLabel = strings.TrimSpace(ctx.ProcessName)
	}
	if targetLabel == "" {
		targetLabel = strings.TrimSpace(ctx.BundleID)
	}
	if targetLabel == "" {
		targetLabel = "Current App"
	}
	return []OmniboxItem{
		{
			ID:       "action:ime-bind-ascii",
			Title:    "Bind Current App to English (ASCII) IME",
			Subtitle: fmt.Sprintf("Auto-switch %s to ASCII • 绑定当前应用为英文输入法", targetLabel),
			KeyHint:  "IME",
			Icon:     "keyboard",
			Keywords: []string{"ime", "ascii", "english", "bind", "rule", "绑定", "英文", "输入法", "当前应用"},
			Kind:     ItemAppIMERule,
			IMEMode:  config.IMEModeASCII,
		},
		{
			ID:       "action:ime-bind-native",
			Title:    "Bind Current App to Native (Chinese) IME",
			Subtitle: fmt.Sprintf("Auto-switch %s to Native IME • 绑定当前应用为中文输入法", targetLabel),
			KeyHint:  "IME",
			Icon:     "keyboard.badge.ellipsis",
			Keywords: []string{"ime", "native", "chinese", "pinyin", "bind", "rule", "绑定", "中文", "拼音", "输入法", "当前应用"},
			Kind:     ItemAppIMERule,
			IMEMode:  config.IMEModeNative,
		},
		{
			ID:       "action:ime-unbind-app",
			Title:    "Unbind Current App IME Rule",
			Subtitle: fmt.Sprintf("Remove auto-switch rule for %s • 解除当前应用输入法规则", targetLabel),
			KeyHint:  "IME",
			Icon:     "keyboard.chevron.compact.down",
			Keywords: []string{"ime", "unbind", "remove", "clear", "rule", "解绑", "移除", "输入法规则"},
			Kind:     ItemAppIMERule,
			IMEMode:  "",
		},
	}
}

func scoreCandidates(candidates []OmniboxItem, query string) []OmniboxItem {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return candidates
	}
	type scoredItem struct {
		origIdx int
		item    OmniboxItem
	}
	var scored []scoredItem
	for i, cand := range candidates {
		keyForScoring := strings.ReplaceAll(cand.KeyHint, " ", "")
		kws := make([]string, 0, len(cand.Keywords)+1)
		kws = append(kws, cand.Keywords...)
		if cand.Subtitle != "" {
			kws = append(kws, cand.Subtitle)
		}
		if res, ok := MatchFull(trimmed, cand.Title, keyForScoring, kws); ok {
			updated := cand
			updated.Score = res.Score
			updated.MatchedIndices = res.MatchedLabelIndices
			scored = append(scored, scoredItem{origIdx: i, item: updated})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].item.Score != scored[j].item.Score {
			return scored[i].item.Score > scored[j].item.Score
		}
		return scored[i].origIdx < scored[j].origIdx
	})
	out := make([]OmniboxItem, len(scored))
	for i, s := range scored {
		out[i] = s.item
	}
	return out
}

func buildOmniboxItems(tab config.CapsuleTab, query string, state State, cfg config.ClavisConfig) []OmniboxItem {
	// Prefix quick-routing in Actions tab: >, ;, @ (or "cb "), ?, :, =
	if tab == config.TabActions {
		if prefix, rest, ok := ParseOmniboxPrefix(query); ok {
			switch prefix {
			case '>':
				var cmds []OmniboxItem
				cmds = append(cmds, flattenLeaderNodes(cfg.Leader.Nodes, nil, nil)...)
				cmds = append(cmds, makeNormalizeGlossaryItem())
				cmds = append(cmds, makeAppIMERuleItems(state.Context)...)
				cmds = append(cmds, makeInstalledAppItems(state.InstalledApps)...)
				cmds = append(cmds, makeSettingsOmniboxItems()...)
				return scoreCandidates(cmds, rest)
			case ';':
				snips := makeSnippetItems(cfg.Snippets.Items)
				if rest == "" {
					return snips
				}
				withSemi := scoreCandidates(snips, ";"+rest)
				withoutSemi := scoreCandidates(snips, rest)
				if len(withSemi) >= len(withoutSemi) && len(withSemi) > 0 {
					return withSemi
				}
				return withoutSemi
			case '@':
				clips := makeClipboardOmniboxItems(state.ClipboardEntries)
				return scoreCandidates(clips, rest)
			case '?':
				aiModes := scoreCandidates(flattenAIModes(cfg.Leader.Nodes, nil, nil), rest)
				if rest != "" {
					out := make([]OmniboxItem, 0, 1+len(aiModes))
					adhoc := makeAdhocAIItem(rest, state.Context)
					adhoc.Score = 2000
					out = append(out, adhoc)
					out = append(out, aiModes...)
					return out
				}
				return aiModes
			case ':':
				return SearchEmojis(":" + rest)
			case '=':
				return EvaluateUtilities("="+rest, time.Now())
			}
		}
	}

	var candidates []OmniboxItem

	switch tab {
	case config.TabActions:
		candidates = append(candidates, flattenLeaderNodes(cfg.Leader.Nodes, nil, nil)...)
		candidates = append(candidates, makeNormalizeGlossaryItem())
		candidates = append(candidates, makeAppIMERuleItems(state.Context)...)
		candidates = append(candidates, makeSnippetItems(cfg.Snippets.Items)...)
		candidates = append(candidates, makeInstalledAppItems(state.InstalledApps)...)
		candidates = append(candidates, makeTabSwitchItems(config.TabActions)...)

	case config.TabSnippets:
		candidates = makeSnippetItems(cfg.Snippets.Items)

	case config.TabClipboard:
		candidates = makeClipboardOmniboxItems(state.ClipboardEntries)

	case config.TabAI:
		candidates = flattenAIModes(cfg.Leader.Nodes, nil, nil)

	case config.TabGlossary:
		candidates = append(candidates, makeNormalizeGlossaryItem())
		for i, term := range cfg.Glossary.Terms {
			tCopy := term
			sub := term.Hint
			if sub == "" {
				sub = term.Category
			} else if term.Category != "" {
				sub = term.Category + " • " + term.Hint
			}
			kws := make([]string, 0, len(term.Aliases)+2)
			kws = append(kws, term.Aliases...)
			kws = append(kws, term.Category, term.Hint)
			candidates = append(candidates, OmniboxItem{
				ID:           fmt.Sprintf("glossary:%d:%s", i, term.Term),
				Title:        term.Term,
				Subtitle:     sub,
				KeyHint:      term.Category,
				Icon:         "character.book.closed",
				Keywords:     kws,
				Kind:         ItemGlossaryTerm,
				GlossaryTerm: &tCopy,
			})
		}

	case config.TabSettings:
		candidates = makeSettingsOmniboxItems()
	}

	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return candidates
	}

	scoredItems := scoreCandidates(candidates, trimmed)

	var results []OmniboxItem
	if tab == config.TabActions {
		utilItems := EvaluateUtilities(trimmed, time.Now())
		results = append(results, utilItems...)

		emojiItems := SearchEmojis(trimmed)
		normTrimmed := NormalizeFullWidth(trimmed)
		lowerTrimmed := strings.ToLower(normTrimmed)
		if strings.HasPrefix(normTrimmed, ":") || lowerTrimmed == "emoji" || strings.HasPrefix(lowerTrimmed, "emoji ") {
			results = append(results, emojiItems...)
		} else {
			results = append(results, scoredItems...)
			results = append(results, emojiItems...)
			goto appendAdhocAI
		}
	}

	results = append(results, scoredItems...)

appendAdhocAI:
	if tab == config.TabAI || (tab == config.TabActions && (len(results) == 0 || utf8.RuneCountInString(trimmed) >= 3)) {
		results = append(results, makeAdhocAIItem(trimmed, state.Context))
	}

	return results
}

func makeInstalledAppItems(apps []InstalledAppEntry) []OmniboxItem {
	if len(apps) == 0 {
		return nil
	}
	items := make([]OmniboxItem, 0, len(apps))
	for i, app := range apps {
		idKey := app.BundleID
		if idKey == "" {
			idKey = app.Path
		}
		if idKey == "" {
			idKey = fmt.Sprintf("%d:%s", i, app.Name)
		}
		sub := "Application"
		if app.BundleID != "" {
			sub = "Application • " + app.BundleID
		} else if app.Path != "" {
			sub = "Application • " + app.Path
		}
		kws := []string{"app", "application", "launch", "open", app.Name}
		if app.BundleID != "" {
			kws = append(kws, app.BundleID)
		}
		items = append(items, OmniboxItem{
			ID:          "app:" + idKey,
			Title:       app.Name,
			Subtitle:    sub,
			KeyHint:     "App",
			Icon:        "app.badge",
			Keywords:    kws,
			Kind:        ItemInstalledApp,
			AppBundleID: app.BundleID,
			AppPath:     app.Path,
		})
	}
	return items
}

func makeAdhocAIItem(query string, ctx CapsuleContext) OmniboxItem {
	hasSelection := strings.TrimSpace(ctx.SelectedText) != ""
	mode := "ask"
	title := fmt.Sprintf("Ask AI: %q", query)
	subtitle := "Send freeform prompt to AI"
	if hasSelection {
		mode = "custom"
		title = fmt.Sprintf("Rewrite with AI: %q", query)
		subtitle = "Use selected text as context"
	}
	return OmniboxItem{
		ID:       "ai:adhoc:" + query,
		Title:    title,
		Subtitle: subtitle,
		Icon:     "sparkles",
		Kind:     ItemAIMode,
		AIMode:   mode,
		AIPrompt: query,
	}
}

func makeNormalizeGlossaryItem() OmniboxItem {
	act := config.NormalizeGlossaryAction()
	return OmniboxItem{
		ID:           "action:normalize-glossary",
		Title:        "Normalize Terms (Glossary)",
		Subtitle:     "Replace glossary aliases with canonical terms",
		Icon:         "text.badge.checkmark",
		Keywords:     []string{"glossary", "normalize", "terms", "alias", "fix", "typo"},
		Kind:         ItemLeaderAction,
		LeaderAction: &act,
	}
}

func flattenLeaderNodes(nodes []config.LeaderNode, keyPrefix, breadcrumbs []string) []OmniboxItem {
	var result []OmniboxItem
	for idx, node := range nodes {
		nextKeys := append(append([]string(nil), keyPrefix...), node.Key)
		if node.Action.Type == config.ActionGroup {
			nextBC := append(append([]string(nil), breadcrumbs...), node.Label)
			result = append(result, flattenLeaderNodes(node.Action.Children, nextKeys, nextBC)...)
			continue
		}
		keyHint := strings.Join(nextKeys, " ")
		subtitle := "Action"
		if len(breadcrumbs) > 0 {
			subtitle = strings.Join(breadcrumbs, " › ")
		}
		kws := make([]string, 0, len(breadcrumbs)+4)
		kws = append(kws, breadcrumbs...)
		kws = append(kws, node.Key, strings.Join(nextKeys, ""), "action")
		kws = append(kws, keywordsForAction(node.Action)...)
		actCopy := node.Action
		result = append(result, OmniboxItem{
			ID:           fmt.Sprintf("leader:%s#%d", strings.Join(nextKeys, "."), idx),
			Title:        node.Label,
			Subtitle:     subtitle,
			KeyHint:      keyHint,
			Icon:         node.Icon,
			Keywords:     kws,
			Kind:         ItemLeaderAction,
			LeaderAction: &actCopy,
		})
	}
	return result
}

func flattenAIModes(nodes []config.LeaderNode, keyPrefix, breadcrumbs []string) []OmniboxItem {
	var result []OmniboxItem
	for idx, node := range nodes {
		nextKeys := append(append([]string(nil), keyPrefix...), node.Key)
		switch node.Action.Type {
		case config.ActionGroup:
			nextBC := append(append([]string(nil), breadcrumbs...), node.Label)
			result = append(result, flattenAIModes(node.Action.Children, nextKeys, nextBC)...)
		case config.ActionAIRewrite:
			icon := node.Icon
			if icon == "" {
				icon = "sparkles"
			}
			kws := make([]string, 0, len(breadcrumbs)+3)
			kws = append(kws, breadcrumbs...)
			kws = append(kws, node.Action.Mode, "ai", node.Key)
			result = append(result, OmniboxItem{
				ID:       fmt.Sprintf("ai:%s:%d", node.Action.Mode, idx),
				Title:    node.Label,
				Subtitle: node.Action.Prompt,
				KeyHint:  strings.Join(nextKeys, " "),
				Icon:     icon,
				Keywords: kws,
				Kind:     ItemAIMode,
				AIMode:   node.Action.Mode,
				AIPrompt: node.Action.Prompt,
			})
		}
	}
	return result
}

func makeSnippetItems(snippets []config.SnippetItem) []OmniboxItem {
	items := make([]OmniboxItem, 0, len(snippets))
	for _, s := range snippets {
		sCopy := s
		title := s.Description
		if title == "" {
			title = s.Trigger
		}
		items = append(items, OmniboxItem{
			ID:       "snippet:" + s.ID,
			Title:    title,
			Subtitle: "Snippet • " + s.Trigger,
			KeyHint:  s.Trigger,
			Icon:     "text.badge.plus",
			Keywords: []string{s.ID, s.Trigger, s.Description, "snippet"},
			Kind:     ItemSnippet,
			Snippet:  &sCopy,
		})
	}
	return items
}

func makeTabSwitchItems(excluded config.CapsuleTab) []OmniboxItem {
	var items []OmniboxItem
	for _, tab := range config.AllCapsuleTabs() {
		if tab == excluded {
			continue
		}
		display := tabDisplayName(tab)
		items = append(items, OmniboxItem{
			ID:        "tab:" + string(tab),
			Title:     display,
			Subtitle:  "Switch Tab",
			KeyHint:   string(tab),
			Icon:      tabIcon(tab),
			Keywords:  []string{"tab", "switch", string(tab), display},
			Kind:      ItemSwitchTab,
			SwitchTab: tab,
		})
	}
	return items
}

func tabDisplayName(tab config.CapsuleTab) string {
	switch tab {
	case config.TabActions:
		return "Actions"
	case config.TabClipboard:
		return "Clipboard"
	case config.TabSnippets:
		return "Snippets"
	case config.TabAI:
		return "AI"
	case config.TabGlossary:
		return "Glossary"
	case config.TabSettings:
		return "Settings"
	default:
		return string(tab)
	}
}

func tabIcon(tab config.CapsuleTab) string {
	switch tab {
	case config.TabActions:
		return "bolt"
	case config.TabClipboard:
		return "doc.on.clipboard"
	case config.TabSnippets:
		return "text.badge.plus"
	case config.TabAI:
		return "sparkles"
	case config.TabGlossary:
		return "character.book.closed"
	case config.TabSettings:
		return "gearshape"
	default:
		return "circle"
	}
}

func keywordsForAction(act config.LeaderAction) []string {
	switch act.Type {
	case config.ActionGroup:
		return []string{"group"}
	case config.ActionAIRewrite:
		return []string{"ai", "rewrite", act.Mode}
	case config.ActionWindow:
		raw := string(act.WindowCommand)
		return []string{"window", raw, strings.ReplaceAll(raw, "_", " ")}
	case config.ActionCLI:
		out := []string{"cli", act.Command}
		return append(out, act.Args...)
	case config.ActionOpenApp:
		return []string{"app", "open", act.Target}
	case config.ActionSwitchTab:
		return []string{"tab", string(act.Tab)}
	case config.ActionSnippet:
		return []string{"snippet", act.SnippetID}
	case config.ActionToggleIME:
		return []string{"ime", "input", "keyboard", "toggle"}
	case config.ActionNormalizeGlossary:
		return []string{"glossary", "normalize", "terms", "alias", "fix", "typo"}
	default:
		return nil
	}
}

func firstNonEmptyLine(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return s
	}
	for _, line := range strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n") {
		if line != "" {
			return line
		}
	}
	return s
}

func clearPhaseFields(s *State) {
	s.LeaderPath = nil
	s.LeaderBreadcrumbs = nil
	s.LeaderNodes = nil
	s.OmniboxTab = ""
	s.OmniboxQuery = ""
	s.OmniboxItems = nil
	s.SelectedIndex = 0
	s.AIMode = ""
	s.AIPrompt = ""
	s.AISourceText = ""
	s.AIOutput = ""
	s.AIComplete = false
	s.AIError = ""
	s.AIHistory = nil
	s.HUDMessage = ""
}

func cloneState(s State) State {
	out := s
	if s.LeaderPath != nil {
		out.LeaderPath = append([]string(nil), s.LeaderPath...)
	}
	if s.LeaderBreadcrumbs != nil {
		out.LeaderBreadcrumbs = append([]string(nil), s.LeaderBreadcrumbs...)
	}
	if s.LeaderNodes != nil {
		out.LeaderNodes = copyLeaderNodes(s.LeaderNodes)
	}
	if s.OmniboxItems != nil {
		out.OmniboxItems = append([]OmniboxItem(nil), s.OmniboxItems...)
	}
	if s.ClipboardEntries != nil {
		out.ClipboardEntries = append([]OmniboxClipboardEntry(nil), s.ClipboardEntries...)
	}
	if s.InstalledApps != nil {
		out.InstalledApps = append([]InstalledAppEntry(nil), s.InstalledApps...)
	}
	if s.AIHistory != nil {
		out.AIHistory = copyChatTurns(s.AIHistory)
	}
	return out
}

func copyChatTurns(turns []ChatTurn) []ChatTurn {
	if turns == nil {
		return nil
	}
	out := make([]ChatTurn, len(turns))
	copy(out, turns)
	return out
}

func copyLeaderNodes(nodes []config.LeaderNode) []config.LeaderNode {
	if nodes == nil {
		return nil
	}
	out := make([]config.LeaderNode, len(nodes))
	copy(out, nodes)
	return out
}
