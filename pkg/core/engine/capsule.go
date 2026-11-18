package engine

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
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

// ClipboardEntryPreview is the safe, eagerly mappable part of an entry used by
// list rows and the preview pane. It deliberately contains no system clipboard
// handle: the UI can render this data without reading the pasteboard again.
type ClipboardEntryPreview struct {
	Summary   string `json:"summary,omitempty"`
	Text      string `json:"text,omitempty"`
	ThumbPath string `json:"thumbPath,omitempty"`
	Decodable bool   `json:"decodable"`
}

// ClipboardEntryActions is the capability state advertised to the UI. Keeping
// this alongside the entry prevents action menus from guessing capabilities
// from a partially decoded media payload.
type ClipboardEntryActions struct {
	CanPin    bool `json:"canPin"`
	CanUnpin  bool `json:"canUnpin"`
	CanCopy   bool `json:"canCopy"`
	CanPaste  bool `json:"canPaste"`
	CanDelete bool `json:"canDelete"`
}

// OmniboxClipboardEntry represents a clipboard item shown in the Clipboard tab.
// It is the complete entry mapping contract: identity, pin state, media type,
// preview data, and available actions are all explicit.
type OmniboxClipboardEntry struct {
	ID            string `json:"id"`
	Content       string `json:"content"`
	SourceAppName string `json:"sourceAppName,omitempty"`
	IsPinned      bool   `json:"isPinned"`
	// Media is set for image/file/video entries (empty Kind = text).
	Media   ClipboardMedia        `json:"media,omitzero"`
	Preview ClipboardEntryPreview `json:"preview,omitzero"`
	Actions ClipboardEntryActions `json:"actions"`
}

// ClipboardMedia mirrors clipboard.Media for the engine/UI layer (engine must not import storage).
type ClipboardMedia struct {
	Kind       string   `json:"kind,omitempty"` // "image" | "file" | "video"
	Path       string   `json:"path,omitempty"`
	Mime       string   `json:"mime,omitempty"`
	ThumbPath  string   `json:"thumbPath,omitempty"`
	Width      int      `json:"width,omitempty"`
	Height     int      `json:"height,omitempty"`
	ByteSize   int64    `json:"byteSize,omitempty"`
	DurationMs int64    `json:"durationMs,omitempty"`
	FilePaths  []string `json:"filePaths,omitempty"`
}

// IsMedia reports whether the entry is not plain text.
func (m ClipboardMedia) IsMedia() bool { return m.Kind != "" && m.Kind != "text" }

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
	ItemPromptTemplate OmniboxItemKind = "prompt_template"
	ItemScriptCommand  OmniboxItemKind = "script_command"
	ItemWorkflow       OmniboxItemKind = "workflow"
	ItemPortProcess    OmniboxItemKind = "port_process"
)

type PortEntry struct {
	Port    int    `json:"port"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
	Command string `json:"command,omitempty"`
}
type CandidateCorpus struct {
	Items      []OmniboxItem `json:"items,omitempty"`
	Generation uint64        `json:"generation"`
}

// OmniboxActionSpec describes a contextual primary, secondary, or palette action for an OmniboxItem.
type OmniboxActionSpec struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Shortcut string `json:"shortcut,omitempty"`
}

// OmniboxItem represents a single selectable row in the Omnibox.
type OmniboxItem struct {
	ID               string                 `json:"id"`
	Title            string                 `json:"title"`
	Subtitle         string                 `json:"subtitle"`
	KeyHint          string                 `json:"keyHint,omitempty"`
	Icon             string                 `json:"icon,omitempty"`
	Keywords         []string               `json:"keywords,omitempty"`
	MatchedIndices   []int                  `json:"matchedIndices,omitempty"`
	Score            int                    `json:"score"`
	Kind             OmniboxItemKind        `json:"kind"`
	LeaderAction     *config.LeaderAction   `json:"leaderAction,omitempty"`
	Snippet          *config.SnippetItem    `json:"snippet,omitempty"`
	SwitchTab        config.CapsuleTab      `json:"switchTab,omitempty"`
	ClipboardID      string                 `json:"clipboardId,omitempty"`
	ClipboardContent string                 `json:"clipboardContent,omitempty"`
	ClipboardMedia   *ClipboardMedia        `json:"clipboardMedia,omitempty"`
	GlossaryTerm     *config.GlossaryTerm   `json:"glossaryTerm,omitempty"`
	AIMode           string                 `json:"aiMode,omitempty"`
	AIPrompt         string                 `json:"aiPrompt,omitempty"`
	SettingID        string                 `json:"settingId,omitempty"`
	UtilityValue     string                 `json:"utilityValue,omitempty"`
	AppBundleID      string                 `json:"appBundleId,omitempty"`
	AppPath          string                 `json:"appPath,omitempty"`
	IMEMode          config.IMEMode         `json:"imeMode,omitempty"`
	PromptTemplate   *config.PromptTemplate `json:"promptTemplate,omitempty"`
	OptimizerPreset  string                 `json:"optimizerPreset,omitempty"`
	ScriptID         string                 `json:"scriptId,omitempty"`
	Workflow         *config.Workflow       `json:"workflow,omitempty"`
	Port             int                    `json:"port,omitempty"`
	PortPID          int                    `json:"portPid,omitempty"`
	PortProcess      string                 `json:"portProcess,omitempty"`
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
	Phase                   CapsulePhase            `json:"phase"`
	LeaderPath              []string                `json:"leaderPath,omitempty"`
	LeaderBreadcrumbs       []string                `json:"leaderBreadcrumbs,omitempty"`
	LeaderNodes             []config.LeaderNode     `json:"leaderNodes,omitempty"`
	OmniboxTab              config.CapsuleTab       `json:"omniboxTab,omitempty"`
	OmniboxQuery            string                  `json:"omniboxQuery,omitempty"`
	OmniboxItems            []OmniboxItem           `json:"omniboxItems,omitempty"`
	SelectedIndex           int                     `json:"selectedIndex"`
	AIMode                  string                  `json:"aiMode,omitempty"`
	AIPrompt                string                  `json:"aiPrompt,omitempty"`
	AISourceText            string                  `json:"aiSourceText,omitempty"`
	AIOutput                string                  `json:"aiOutput,omitempty"`
	AIComplete              bool                    `json:"aiComplete,omitempty"`
	AIError                 string                  `json:"aiError,omitempty"`
	AIHistory               []ChatTurn              `json:"aiHistory,omitempty"`
	AIOptimizerPreset       string                  `json:"aiOptimizerPreset,omitempty"`
	ActiveAIProfileID       string                  `json:"activeAiProfileId,omitempty"`
	HUDMessage              string                  `json:"hudMessage,omitempty"`
	Context                 CapsuleContext          `json:"context"`
	ClipboardEntries        []OmniboxClipboardEntry `json:"clipboardEntries,omitempty"`
	ClipboardSearchExternal bool                    `json:"clipboardSearchExternal,omitempty"`
	ClipboardSearchQuery    string                  `json:"clipboardSearchQuery,omitempty"`
	ClipboardSearchEntries  []OmniboxClipboardEntry `json:"clipboardSearchEntries,omitempty"`
	InstalledApps           []InstalledAppEntry     `json:"installedApps,omitempty"`
	FrecencyScores          map[string]int          `json:"frecencyScores,omitempty"`
	ListeningPorts          []PortEntry             `json:"listeningPorts,omitempty"`
}

// NewInitialState returns a hidden initial State.
func NewInitialState() State {
	return State{Phase: PhaseHidden}
}

// EventType identifies an input event for CapsuleStateMachine.
type EventType string

const (
	EventActivateClipboardItem          EventType = "activate_clipboard_item"
	EventOpenLeader                     EventType = "open_leader"
	EventOpenOmnibox                    EventType = "open_omnibox"
	EventUpdateContext                  EventType = "update_context"
	EventSetClipboardEntries            EventType = "set_clipboard_entries"
	EventSetClipboardSearchResults      EventType = "set_clipboard_search_results"
	EventSetInstalledApps               EventType = "set_installed_apps"
	EventSetFrecencyScores              EventType = "set_frecency_scores"
	EventSetListeningPorts              EventType = "set_listening_ports"
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
	FrecencyScores   map[string]int          `json:"frecencyScores,omitempty"`
	ListeningPorts   []PortEntry             `json:"listeningPorts,omitempty"`
}

// EffectType identifies a side effect emitted by Reduce.
type EffectType string

const (
	EffectPresentCapsule EffectType = "present_capsule"
	// EffectActivateClipboardItem is retained for compatibility with older
	// integrations. Clipboard activation is now a pure paste operation and
	// never changes pin state.
	EffectActivateClipboardItem EffectType = "activate_clipboard_item"
	EffectClose                 EffectType = "close"
	EffectRunAction             EffectType = "run_action"
	EffectRunWorkflow           EffectType = "run_workflow"
	EffectStartAI               EffectType = "start_ai"
	EffectCancelAI              EffectType = "cancel_ai"
	EffectApplyAIOutput         EffectType = "apply_ai_output"
	EffectCopyText              EffectType = "copy_text"
	EffectPasteClipboardItem    EffectType = "paste_clipboard_item"
	EffectPinClipboardItem      EffectType = "pin_clipboard_item"
	EffectDeleteClipboard       EffectType = "delete_clipboard_item"
	EffectBindAppIMERule        EffectType = "bind_app_ime_rule"
	EffectOpenPromptComposer    EffectType = "open_prompt_composer"
)

// Effect describes a side effect that the host/coordinator should perform.
type Effect struct {
	Type                EffectType             `json:"type"`
	Action              *config.LeaderAction   `json:"action,omitempty"`
	WorkflowID          string                 `json:"workflowId,omitempty"`
	Context             *CapsuleContext        `json:"context,omitempty"`
	Mode                string                 `json:"mode,omitempty"`
	Prompt              string                 `json:"prompt,omitempty"`
	SourceText          string                 `json:"sourceText,omitempty"`
	History             []ChatTurn             `json:"history,omitempty"`
	ProfileID           string                 `json:"profileId,omitempty"`
	Text                string                 `json:"text,omitempty"`
	ClipboardID         string                 `json:"clipboardId,omitempty"`
	ClipboardContent    string                 `json:"clipboardContent,omitempty"`
	ClipboardMedia      *ClipboardMedia        `json:"clipboardMedia,omitempty"`
	IsPinned            bool                   `json:"isPinned,omitempty"`
	IMEMode             config.IMEMode         `json:"imeMode,omitempty"`
	PromptTemplate      *config.PromptTemplate `json:"promptTemplate,omitempty"`
	OptimizerPreset     string                 `json:"optimizerPreset,omitempty"`
	ClipboardActivation *ClipboardActivation   `json:"clipboardActivation,omitempty"`
}

// ClipboardActivation is the portable effect contract for a clipboard
// activation. It is a pure paste operation: activation never changes pin
// state. Pin changes are explicit through the pin action or Cmd/Ctrl+P.
// ClipboardActivationStep names the ordered effects of an activation.
type ClipboardActivationStep string

const (
	ClipboardStepWrite ClipboardActivationStep = "write_system_clipboard"
	ClipboardStepPaste ClipboardActivationStep = "paste_to_active_app"
)

type ClipboardActivation struct {
	ID      string                    `json:"id"`
	Content string                    `json:"content,omitempty"`
	Media   *ClipboardMedia           `json:"media,omitempty"`
	Steps   []ClipboardActivationStep `json:"steps"`
	// PinBeforeWrite is deprecated and always false. It remains serialized for
	// compatibility with older clients.
	PinBeforeWrite bool   `json:"pinBeforeWrite"`
	PresentPreview bool   `json:"presentPreview"`
	Generation     uint64 `json:"generation,omitempty"`
}

// StateMachine wraps State + ClavisConfig with a stateful Handle method.
type StateMachine struct {
	config           config.ClavisConfig
	state            State
	CandidateCorpus  CandidateCorpus
	corpusGeneration uint64
}

// NewStateMachine constructs a StateMachine with the given config.
func NewStateMachine(cfg config.ClavisConfig) *StateMachine {
	st := NewInitialState()
	st.ActiveAIProfileID = cfg.Main.AI.ActiveProfileID
	m := &StateMachine{config: cfg, state: st}
	m.rebuildCandidateCorpus()
	return m
}

// State returns a copy of the current State.
func (m *StateMachine) State() State {
	return cloneState(m.state)
}

// SetConfig updates the config used for subsequent transitions.
func (m *StateMachine) SetConfig(cfg config.ClavisConfig) {
	m.config = cfg
	m.corpusGeneration++
	m.rebuildCandidateCorpus()
	if m.state.ActiveAIProfileID == "" {
		m.state.ActiveAIProfileID = cfg.Main.AI.ActiveProfileID
	}
}

// SetExternalClipboardSearch enables or disables external (e.g. SQLite FTS5)
// search resolution for non-empty Clipboard tab queries.
func (m *StateMachine) SetExternalClipboardSearch(enabled bool) {
	m.state.ClipboardSearchExternal = enabled
}

// Handle reduces an event and updates internal state, returning emitted effects.
func (m *StateMachine) Handle(ev Event) []Effect {
	next, effects := Reduce(m.state, ev, m.config)
	m.state = next
	if ev.Type == EventSetClipboardEntries || ev.Type == EventSetInstalledApps {
		m.corpusGeneration++
		m.rebuildCandidateCorpus()
	}
	return effects
}
func (m *StateMachine) rebuildCandidateCorpus() {
	items := append([]OmniboxItem{}, flattenLeaderNodes(m.config.Leader.Nodes, nil, nil)...)
	items = append(items, makeSnippetItems(m.config.Snippets.Items)...)
	items = append(items, makeInstalledAppItems(m.state.InstalledApps)...)
	for _, p := range m.config.Prompts.Items {
		x := p
		items = append(items, OmniboxItem{ID: "prompt:" + p.ID, Title: p.Title, Subtitle: p.Description, KeyHint: p.Trigger, Icon: "wand.and.stars", Keywords: []string{p.Trigger, p.Category}, Kind: ItemPromptTemplate, PromptTemplate: &x})
	}
	for _, x := range m.config.Components.Scripts {
		if x.Enabled {
			items = append(items, OmniboxItem{ID: "script:" + x.ID, Title: x.Title, Subtitle: x.Subtitle, KeyHint: x.Trigger, Kind: ItemScriptCommand, ScriptID: x.ID})
		}
	}
	items = append(items, makeWorkflowItems(m.config.Workflows.Workflows)...)
	m.CandidateCorpus = CandidateCorpus{Items: items, Generation: m.corpusGeneration}
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
		clearPhaseFields(&next)
		items := buildOmniboxItems(tab, event.Query, next, cfg)
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
			if next.ClipboardSearchExternal && strings.TrimSpace(next.OmniboxQuery) != "" {
				return next, nil
			}
			items := buildOmniboxItems(config.TabClipboard, next.OmniboxQuery, next, cfg)
			next.OmniboxItems = items
			next.SelectedIndex = preserveClipboardSelectionIndex(items, previousSelectedID, next.SelectedIndex)
		}
		return next, nil

	case EventSetClipboardSearchResults:
		if next.Phase != PhaseOmnibox || next.OmniboxTab != config.TabClipboard {
			return next, nil
		}
		trimmedQuery := strings.TrimSpace(event.Query)
		if trimmedQuery == "" || strings.TrimSpace(next.OmniboxQuery) != trimmedQuery {
			return next, nil
		}
		var previousSelectedID string
		if next.SelectedIndex >= 0 && next.SelectedIndex < len(next.OmniboxItems) {
			previousSelectedID = next.OmniboxItems[next.SelectedIndex].ID
		}
		entries := make([]OmniboxClipboardEntry, len(event.ClipboardEntries))
		copy(entries, event.ClipboardEntries)
		next.ClipboardSearchQuery = trimmedQuery
		next.ClipboardSearchEntries = entries

		items := makeClipboardOmniboxItems(entries)
		next.OmniboxItems = items
		next.SelectedIndex = preserveClipboardSelectionIndex(items, previousSelectedID, 0)
		return next, nil

	case EventSetFrecencyScores:
		next.FrecencyScores = make(map[string]int, len(event.FrecencyScores))
		for id, score := range event.FrecencyScores {
			next.FrecencyScores[id] = score
		}
		if next.Phase == PhaseOmnibox {
			next.OmniboxItems = buildOmniboxItems(next.OmniboxTab, next.OmniboxQuery, next, cfg)
		}
		return next, nil

	case EventSetListeningPorts:
		next.ListeningPorts = append([]PortEntry(nil), event.ListeningPorts...)
		if next.Phase == PhaseOmnibox {
			next.OmniboxItems = buildOmniboxItems(next.OmniboxTab, next.OmniboxQuery, next, cfg)
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
		if state.OmniboxTab == config.TabClipboard && next.ClipboardSearchExternal {
			next.OmniboxQuery = event.Query
			trimmed := strings.TrimSpace(event.Query)
			if trimmed == "" {
				next.ClipboardSearchQuery = ""
				next.ClipboardSearchEntries = nil
				next.OmniboxItems = makeClipboardOmniboxItems(next.ClipboardEntries)
				next.SelectedIndex = 0
				return next, nil
			}
			if next.ClipboardSearchQuery == trimmed {
				next.OmniboxItems = makeClipboardOmniboxItems(next.ClipboardSearchEntries)
				next.SelectedIndex = 0
				return next, nil
			}
			if len(next.OmniboxItems) == 0 {
				next.SelectedIndex = 0
			} else if next.SelectedIndex >= len(next.OmniboxItems) {
				next.SelectedIndex = len(next.OmniboxItems) - 1
			}
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
		clearPhaseFields(&next)
		items := buildOmniboxItems(tab, query, next, cfg)
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

	case EventActivateClipboardItem:
		if state.Phase != PhaseOmnibox || state.SelectedIndex < 0 || state.SelectedIndex >= len(state.OmniboxItems) {
			return next, nil
		}
		selected := state.OmniboxItems[state.SelectedIndex]
		if selected.Kind != ItemClipboardEntry || strings.TrimSpace(selected.ClipboardID) == "" {
			return next, nil
		}
		return next, []Effect{{
			Type: EffectActivateClipboardItem,
			ClipboardActivation: &ClipboardActivation{
				ID: selected.ClipboardID, Content: selected.ClipboardContent,
				Media:          selected.ClipboardMedia,
				Steps:          []ClipboardActivationStep{ClipboardStepWrite, ClipboardStepPaste},
				PinBeforeWrite: false, PresentPreview: false,
			},
		}}

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
		searchIdx := -1
		for i := range next.ClipboardSearchEntries {
			if next.ClipboardSearchEntries[i].ID == selected.ClipboardID {
				searchIdx = i
				break
			}
		}
		if entryIdx == -1 && searchIdx == -1 && (!next.ClipboardSearchExternal || selected.ClipboardID == "") {
			return next, nil
		}
		wasPinned := selected.KeyHint == KeyHintPinned
		if entryIdx >= 0 {
			wasPinned = next.ClipboardEntries[entryIdx].IsPinned
		} else if searchIdx >= 0 {
			wasPinned = next.ClipboardSearchEntries[searchIdx].IsPinned
		}
		newPinned := !wasPinned
		if entryIdx >= 0 {
			next.ClipboardEntries[entryIdx].IsPinned = newPinned
		}
		if searchIdx >= 0 {
			next.ClipboardSearchEntries[searchIdx].IsPinned = newPinned
		}
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
		if len(next.ClipboardSearchEntries) > 0 {
			filteredSearch := make([]OmniboxClipboardEntry, 0, len(next.ClipboardSearchEntries))
			for _, e := range next.ClipboardSearchEntries {
				if e.ID != selected.ClipboardID {
					filteredSearch = append(filteredSearch, e)
				}
			}
			next.ClipboardSearchEntries = filteredSearch
		}
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
			if state.OmniboxTab == config.TabClipboard && next.ClipboardSearchExternal {
				next.OmniboxQuery = updatedQuery
				trimmed := strings.TrimSpace(updatedQuery)
				if trimmed == "" {
					next.ClipboardSearchQuery = ""
					next.ClipboardSearchEntries = nil
					next.OmniboxItems = makeClipboardOmniboxItems(next.ClipboardEntries)
					next.SelectedIndex = 0
					return next, nil
				}
				if next.ClipboardSearchQuery == trimmed {
					next.OmniboxItems = makeClipboardOmniboxItems(next.ClipboardSearchEntries)
					next.SelectedIndex = 0
					return next, nil
				}
				if len(next.OmniboxItems) == 0 {
					next.SelectedIndex = 0
				} else if next.SelectedIndex >= len(next.OmniboxItems) {
					next.SelectedIndex = len(next.OmniboxItems) - 1
				}
				return next, nil
			}
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
				Type:            EffectStartAI,
				Mode:            next.AIMode,
				Prompt:          followUpPrompt,
				SourceText:      next.AISourceText,
				Context:         &ctxCopy,
				History:         histCopy,
				ProfileID:       profID,
				OptimizerPreset: next.AIOptimizerPreset,
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
		next.LeaderBreadcrumbs = append(append([]string(nil), breadcrumbs...), i18n.DisplayLabel(node.Label))
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

	case config.ActionRunWorkflow:
		if strings.TrimSpace(node.Action.WorkflowID) == "" {
			return next, nil
		}
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{{Type: EffectRunWorkflow, WorkflowID: node.Action.WorkflowID, Context: &ctxCopy}, {Type: EffectClose}}

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
// KeyHintPinned is the sentinel KeyHint marking a pinned clipboard entry.
// It is compared, not shown: render sites translate it with i18n.
const KeyHintPinned = "Pinned"

// modKey names the primary modifier in shortcut hints: ⌘ on macOS,
// Ctrl+ elsewhere. Both languages use the platform glyph.
func modKey() string {
	if runtime.GOOS == "darwin" {
		return "⌘"
	}
	return "Ctrl+"
}

func AvailableItemActions(item OmniboxItem) []OmniboxActionSpec {
	switch item.Kind {
	case ItemClipboardEntry:
		pinLabel := i18n.T("engine.action.pin")
		if item.KeyHint == KeyHintPinned {
			pinLabel = i18n.T("engine.action.unpin")
		}
		return []OmniboxActionSpec{
			{ID: "copy", Label: i18n.T("engine.action.copy_clipboard"), Shortcut: "↵"},
			{ID: "paste", Label: i18n.T("engine.action.paste"), Shortcut: modKey() + "↵"},
			{ID: "pin", Label: pinLabel, Shortcut: modKey() + "P"},
			{ID: "delete", Label: i18n.T("engine.action.delete_history"), Shortcut: modKey() + "⌫"},
		}
	case ItemSnippet:
		return []OmniboxActionSpec{
			{ID: "expand", Label: i18n.T("engine.action.insert_snippet"), Shortcut: "↵"},
			{ID: "copy", Label: i18n.T("engine.action.copy_snippet"), Shortcut: modKey() + "↵"},
		}
	case ItemUtility:
		return []OmniboxActionSpec{
			{ID: "copy", Label: i18n.T("engine.action.copy_result"), Shortcut: "↵"},
			{ID: "paste", Label: i18n.T("engine.action.insert_result"), Shortcut: modKey() + "↵"},
		}
	case ItemGlossaryTerm:
		return []OmniboxActionSpec{
			{ID: "copy", Label: i18n.T("engine.action.copy_term"), Shortcut: "↵"},
			{ID: "paste", Label: i18n.T("engine.action.insert_term"), Shortcut: modKey() + "↵"},
		}
	case ItemEmoji:
		return []OmniboxActionSpec{
			{ID: "paste", Label: i18n.T("engine.action.insert_emoji"), Shortcut: "↵"},
			{ID: "copy", Label: i18n.T("engine.action.copy_emoji"), Shortcut: modKey() + "↵"},
		}
	case ItemInstalledApp:
		return []OmniboxActionSpec{
			{ID: "open", Label: i18n.T("engine.action.open_app"), Shortcut: "↵"},
			{ID: "copy_id", Label: i18n.T("engine.action.copy_bundle"), Shortcut: modKey() + "↵"},
		}
	case ItemAIMode:
		return []OmniboxActionSpec{
			{ID: "run_ai", Label: i18n.T("engine.action.run_ai"), Shortcut: "↵"},
			{ID: "ask_only", Label: i18n.T("engine.action.ask_only"), Shortcut: modKey() + "↵"},
		}
	case ItemAppIMERule:
		return []OmniboxActionSpec{
			{ID: "apply_rule", Label: i18n.T("engine.action.apply_rule"), Shortcut: "↵"},
			{ID: "copy_title", Label: i18n.T("engine.action.copy_title"), Shortcut: modKey() + "↵"},
		}
	case ItemPromptTemplate:
		return []OmniboxActionSpec{
			{ID: "compose", Label: i18n.T("engine.action.compose_prompt"), Shortcut: "↵"},
			{ID: "copy_template", Label: i18n.T("engine.action.copy_template"), Shortcut: modKey() + "↵"},
		}
	case ItemWorkflow:
		return []OmniboxActionSpec{
			{ID: "run_workflow", Label: "Run Workflow", Shortcut: "↵"},
			{ID: "copy_title", Label: i18n.T("engine.action.copy_item_title"), Shortcut: modKey() + "↵"},
		}
	default:
		return []OmniboxActionSpec{
			{ID: "primary", Label: i18n.T("engine.action.run"), Shortcut: "↵"},
			{ID: "copy_title", Label: i18n.T("engine.action.copy_item_title"), Shortcut: modKey() + "↵"},
		}
	}
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
	// Clipboard history follows Maccy semantics: the primary action pastes the
	// selected item into the active app. Copy remains an explicit action-menu item.

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
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectCopyText, Text: item.ClipboardContent, ClipboardID: item.ClipboardID, ClipboardMedia: item.ClipboardMedia},
				{Type: EffectClose},
			}
		}
		if actID == "paste" {
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectPasteClipboardItem, ClipboardID: item.ClipboardID, ClipboardContent: item.ClipboardContent, ClipboardMedia: item.ClipboardMedia},
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

		case config.ActionRunWorkflow:
			if strings.TrimSpace(act.WorkflowID) == "" {
				return next, nil
			}
			ctxCopy := next.Context
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{{Type: EffectRunWorkflow, WorkflowID: act.WorkflowID, Context: &ctxCopy}, {Type: EffectClose}}

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
		// Keep the Capsule open until the asynchronous write and platform paste
		// both succeed; the controller dismisses it from the success callback.
		return next, []Effect{{Type: EffectPasteClipboardItem, ClipboardID: item.ClipboardID, ClipboardContent: item.ClipboardContent, ClipboardMedia: item.ClipboardMedia}}

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
		if item.AIMode == "optimize" {
			draft := strings.TrimSpace(item.AIPrompt)
			src := ""
			if draft == "" {
				draft = strings.TrimSpace(next.Context.SelectedText)
			} else if strings.TrimSpace(next.Context.SelectedText) != "" {
				src = next.Context.SelectedText
			}
			if draft == "" {
				return next, nil
			}
			preset := strings.TrimSpace(item.OptimizerPreset)
			if preset == "" {
				preset = strings.TrimSpace(cfg.Prompts.DefaultOptimizerPreset)
			}
			if preset == "" {
				preset = "coding"
			}
			ctxCopy := next.Context
			profID := effectiveProfileID(next, cfg)
			clearPhaseFields(&next)
			next.Phase = PhaseAIStreaming
			next.AIMode = "optimize"
			next.AIPrompt = draft
			next.AISourceText = src
			next.AIOptimizerPreset = preset
			return next, []Effect{{
				Type:            EffectStartAI,
				Mode:            "optimize",
				Prompt:          draft,
				SourceText:      src,
				Context:         &ctxCopy,
				ProfileID:       profID,
				OptimizerPreset: preset,
			}}
		}
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

	case ItemWorkflow:
		if item.Workflow == nil || strings.TrimSpace(item.Workflow.ID) == "" {
			return next, nil
		}
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{{Type: EffectRunWorkflow, WorkflowID: item.Workflow.ID, Context: &ctxCopy}, {Type: EffectClose}}

	case ItemPromptTemplate:
		if item.PromptTemplate == nil {
			return next, nil
		}
		if actID == "copy_template" {
			tplText := item.PromptTemplate.Template
			clearPhaseFields(&next)
			next.Phase = PhaseHidden
			return next, []Effect{
				{Type: EffectCopyText, Text: tplText},
				{Type: EffectClose},
			}
		}
		tpl := *item.PromptTemplate
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{
			{Type: EffectOpenPromptComposer, PromptTemplate: &tpl, Context: &ctxCopy},
			{Type: EffectClose},
		}

	case ItemScriptCommand:
		for _, spec := range cfg.Components.Scripts {
			if spec.ID == item.ScriptID {
				act := config.CLIAction(spec.Command, spec.Args...)
				ctxCopy := next.Context
				clearPhaseFields(&next)
				next.Phase = PhaseHidden
				return next, []Effect{{Type: EffectRunAction, Action: &act, Context: &ctxCopy}, {Type: EffectClose}}
			}
		}

	case ItemPortProcess:
		act := config.CLIAction("kill", "-TERM", strconv.Itoa(item.PortPID))
		ctxCopy := next.Context
		clearPhaseFields(&next)
		next.Phase = PhaseHidden
		return next, []Effect{{Type: EffectRunAction, Action: &act, Context: &ctxCopy}, {Type: EffectClose}}

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
	case '>', ';', '@', '?', ':', '=', '/':
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
		if entry.Media.IsMedia() {
			firstLine = ClipboardMediaTitle(entry.Media, entry.Content)
		}
		sub := entry.SourceAppName
		if sub == "" {
			sub = i18n.T("omnibox.tab.clipboard")
		}
		keyHint := ""
		icon := "doc.on.clipboard"
		if entry.IsPinned {
			keyHint = KeyHintPinned
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
			ClipboardMedia:   clipboardMediaPtr(entry.Media),
		})
		if entry.Media.IsMedia() {
			candidates[len(candidates)-1].Icon = clipboardMediaIcon(entry.Media)
			if entry.IsPinned {
				candidates[len(candidates)-1].Icon = "pin.fill"
			}
		}
	}
	return candidates
}

func clipboardMediaPtr(m ClipboardMedia) *ClipboardMedia {
	if !m.IsMedia() {
		return nil
	}
	c := m
	return &c
}

func clipboardMediaIcon(m ClipboardMedia) string {
	switch m.Kind {
	case "image":
		return "photo"
	case "video":
		return "film"
	}
	return "doc"
}

// ClipboardMediaTitle is the list title for a media entry: "Image 1920×1080" or the file name(s).
func ClipboardMediaTitle(m ClipboardMedia, content string) string {
	switch m.Kind {
	case "image":
		if m.Width > 0 && m.Height > 0 {
			return i18n.Tf("clipboard.media.image_dims", m.Width, m.Height)
		}
		return i18n.T("clipboard.media.image")
	default:
		if t := firstNonEmptyLine(content); t != "" {
			if n := len(m.FilePaths); n > 1 {
				return i18n.Tf("clipboard.media.files_more", t, n-1)
			}
			return t
		}
		return i18n.T("clipboard.media.file")
	}
}

func makeSettingsOmniboxItems() []OmniboxItem {
	return []OmniboxItem{
		{
			ID:        "setting:reload-config",
			Title:     i18n.T("common.reload"),
			Subtitle:  i18n.T("engine.settings.reload.subtitle"),
			Icon:      "arrow.clockwise",
			Keywords:  []string{"reload", "config", "toml", "settings", "重载配置"},
			Kind:      ItemSetting,
			SettingID: "reload-config",
		},
		{
			ID:        "setting:open-config-dir",
			Title:     i18n.T("engine.settings.open_dir"),
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
		targetLabel = i18n.T("engine.ime.current_app")
	}
	return []OmniboxItem{
		{
			ID:       "action:ime-bind-ascii",
			Title:    i18n.T("engine.ime.bind_ascii"),
			Subtitle: i18n.Tf("engine.ime.bind_ascii.subtitle", targetLabel),
			KeyHint:  "IME",
			Icon:     "keyboard",
			Keywords: []string{"ime", "ascii", "english", "bind", "rule", "绑定", "英文", "输入法", "当前应用", "绑定当前应用为英文输入法"},
			Kind:     ItemAppIMERule,
			IMEMode:  config.IMEModeASCII,
		},
		{
			ID:       "action:ime-bind-native",
			Title:    i18n.T("engine.ime.bind_native"),
			Subtitle: i18n.Tf("engine.ime.bind_native.subtitle", targetLabel),
			KeyHint:  "IME",
			Icon:     "keyboard.badge.ellipsis",
			Keywords: []string{"ime", "native", "chinese", "pinyin", "bind", "rule", "绑定", "中文", "拼音", "输入法", "当前应用", "绑定当前应用为中文输入法"},
			Kind:     ItemAppIMERule,
			IMEMode:  config.IMEModeNative,
		},
		{
			ID:       "action:ime-unbind-app",
			Title:    i18n.T("engine.ime.unbind"),
			Subtitle: i18n.Tf("engine.ime.unbind.subtitle", targetLabel),
			KeyHint:  "IME",
			Icon:     "keyboard.chevron.compact.down",
			Keywords: []string{"ime", "unbind", "remove", "clear", "rule", "解绑", "移除", "输入法规则", "解除当前应用输入法规则"},
			Kind:     ItemAppIMERule,
			IMEMode:  "",
		},
	}
}

func scoreCandidates(candidates []OmniboxItem, query string) []OmniboxItem {
	return scoreCandidatesWithFrecency(candidates, query, nil)
}
func scoreCandidatesWithFrecency(candidates []OmniboxItem, query string, frecency map[string]int) []OmniboxItem {
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
			updated.Score = res.Score + frecency[cand.ID]
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
	// Inline prompt optimizer prefix (e.g. "//draft") takes precedence over single-'/' prompts routing.
	if tab == config.TabActions || tab == config.TabAI {
		if draft, ok := matchInlineOptimizePrefix(query, cfg); ok {
			return makeOptimizePromptItems(draft, state.Context, cfg)
		}
	}
	// Configured source prefixes take precedence over built-in routes.
	if tab == config.TabActions {
		for _, src := range cfg.Components.Sources {
			if !src.Enabled || strings.TrimSpace(src.Prefix) == "" || !strings.HasPrefix(query, src.Prefix) {
				continue
			}
			rest := strings.TrimSpace(strings.TrimPrefix(query, src.Prefix))
			switch src.ID {
			case "prompts":
				return matchPromptItems(cfgPromptItems(cfg, state.Context), query, rest, src.Prefix, state.FrecencyScores)
			case "scripts":
				var scripts []OmniboxItem
				for _, x := range cfg.Components.Scripts {
					if x.Enabled {
						scripts = append(scripts, OmniboxItem{ID: "script:" + x.ID, Title: x.Title, Subtitle: x.Subtitle, KeyHint: x.Trigger, Kind: ItemScriptCommand, ScriptID: x.ID})
					}
				}
				return scoreCandidatesWithFrecency(scripts, rest, state.FrecencyScores)
			case "workflows":
				return scoreCandidatesWithFrecency(makeWorkflowItems(cfg.Workflows.Workflows), rest, state.FrecencyScores)
			}
		}
	}
	// Prefix quick-routing in Actions tab: >, ;, @ (or "cb "), ?, :, =
	if tab == config.TabActions {
		if prefix, rest, ok := ParseOmniboxPrefix(query); ok {
			switch prefix {
			case '>':
				var cmds []OmniboxItem
				for _, port := range state.ListeningPorts {
					cmds = append(cmds, OmniboxItem{ID: fmt.Sprintf("port:%d", port.Port), Title: fmt.Sprintf(":%d %s", port.Port, port.Process), Subtitle: port.Command, Keywords: []string{"kill", "port", strconv.Itoa(port.Port)}, Kind: ItemPortProcess, Port: port.Port, PortPID: port.PID, PortProcess: port.Process, Score: 1000})
				}
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
				for _, port := range state.ListeningPorts {
					if rest == strconv.Itoa(port.Port) || strings.Contains(strings.ToLower(port.Process), strings.ToLower(rest)) {
						return []OmniboxItem{{ID: fmt.Sprintf("port:%d", port.Port), Title: fmt.Sprintf("Kill %s on :%d", port.Process, port.Port), Subtitle: port.Command, Kind: ItemPortProcess, Port: port.Port, PortPID: port.PID, PortProcess: port.Process, Score: 1300}}
					}
				}
				return SearchEmojis(":" + rest)
			case '=':
				return EvaluateUtilities("="+rest, time.Now())
			case '/':
				if sourceEnabled(cfg.Components.Sources, "prompts") {
					return matchPromptItems(cfgPromptItems(cfg, state.Context), query, rest, "/", state.FrecencyScores)
				}
			}
		}
	}

	var candidates []OmniboxItem

	switch tab {
	case config.TabActions:
		candidates = append(candidates, flattenLeaderNodes(cfg.Leader.Nodes, nil, nil)...)
		candidates = append(candidates, makeNormalizeGlossaryItem())
		if sourceEnabled(cfg.Components.Sources, "prompts") {
			candidates = append(candidates, cfgPromptItems(cfg, state.Context)...)
		}
		if sourceEnabled(cfg.Components.Sources, "scripts") {
			for _, s := range cfg.Components.Scripts {
				if s.Enabled {
					candidates = append(candidates, OmniboxItem{ID: "script:" + s.ID, Title: s.Title, Subtitle: s.Subtitle, KeyHint: s.Trigger, Kind: ItemScriptCommand, ScriptID: s.ID, Score: 1000})
				}
			}
		}
		candidates = append(candidates, makeWorkflowItems(cfg.Workflows.Workflows)...)
		for _, p := range state.ListeningPorts {
			candidates = append(candidates, OmniboxItem{ID: fmt.Sprintf("port:%d", p.Port), Title: fmt.Sprintf(":%d %s", p.Port, p.Process), Subtitle: p.Command, Keywords: []string{"port", "kill", strconv.Itoa(p.Port)}, Kind: ItemPortProcess, Port: p.Port, PortPID: p.PID, PortProcess: p.Process, Score: 1000})
		}

		candidates = append(candidates, makeAppIMERuleItems(state.Context)...)
		candidates = append(candidates, makeSnippetItems(cfg.Snippets.Items)...)
		candidates = append(candidates, makeInstalledAppItems(state.InstalledApps)...)
		candidates = append(candidates, makeTabSwitchItems(config.TabActions)...)
	case config.TabSnippets:
		candidates = makeSnippetItems(cfg.Snippets.Items)

	case config.TabClipboard:
		if state.ClipboardSearchExternal {
			trimmed := strings.TrimSpace(query)
			if trimmed == "" {
				return makeClipboardOmniboxItems(state.ClipboardEntries)
			}
			if state.ClipboardSearchQuery == trimmed {
				return makeClipboardOmniboxItems(state.ClipboardSearchEntries)
			}
			return nil
		}
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

	scoredItems := scoreCandidatesWithFrecency(candidates, trimmed, state.FrecencyScores)

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

func makeWorkflowItems(workflows []config.Workflow) []OmniboxItem {
	items := make([]OmniboxItem, 0, len(workflows))
	for _, workflow := range workflows {
		if !workflow.Enabled || strings.TrimSpace(workflow.ID) == "" {
			continue
		}
		w := workflow
		title := strings.TrimSpace(workflow.Name)
		if title == "" {
			title = workflow.ID
		}
		subtitle := strings.TrimSpace(workflow.Description)
		if subtitle == "" {
			subtitle = fmt.Sprintf("%d steps • safe CLI/script actions", len(workflow.Steps))
		}
		items = append(items, OmniboxItem{
			ID: "workflow:" + workflow.ID, Title: title, Subtitle: subtitle,
			Keywords: []string{"workflow", workflow.ID, workflow.Name, workflow.Description},
			Icon:     "arrow.triangle.branch", Kind: ItemWorkflow, Workflow: &w, Score: 1000,
		})
	}
	return items
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
		sub := i18n.T("engine.app.kind")
		if app.BundleID != "" {
			sub = i18n.Tf("engine.app.kind_id", app.BundleID)
		} else if app.Path != "" {
			sub = i18n.Tf("engine.app.kind_id", app.Path)
		}
		kws := []string{"app", "application", "launch", "open", app.Name}
		if app.BundleID != "" {
			kws = append(kws, app.BundleID)
		}
		items = append(items, OmniboxItem{
			ID:          "app:" + idKey,
			Title:       app.Name,
			Subtitle:    sub,
			KeyHint:     i18n.T("engine.app.hint"),
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
	title := i18n.Tf("engine.ai.ask_title", query)
	subtitle := i18n.T("engine.ai.ask_subtitle")
	if hasSelection {
		mode = "custom"
		title = i18n.Tf("engine.ai.rewrite_title", query)
		subtitle = i18n.T("engine.ai.rewrite_subtitle")
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
		Title:        i18n.T("engine.glossary.normalize"),
		Subtitle:     i18n.T("engine.glossary.normalize.subtitle"),
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
			nextBC := append(append([]string(nil), breadcrumbs...), i18n.DisplayLabel(node.Label))
			result = append(result, flattenLeaderNodes(node.Action.Children, nextKeys, nextBC)...)
			continue
		}
		keyHint := strings.Join(nextKeys, " ")
		subtitle := i18n.T("engine.leader.action")
		if len(breadcrumbs) > 0 {
			subtitle = strings.Join(breadcrumbs, " › ")
		}
		kws := make([]string, 0, len(breadcrumbs)+4)
		kws = append(kws, breadcrumbs...)
		kws = append(kws, node.Key, strings.Join(nextKeys, ""), "action")
		kws = append(kws, keywordsForAction(node.Action)...)
		title := i18n.DisplayLabel(node.Label)
		if title != node.Label {
			// Localized stock label: the original stays searchable.
			kws = append(kws, node.Label)
		}
		actCopy := node.Action
		result = append(result, OmniboxItem{
			ID:           fmt.Sprintf("leader:%s#%d", strings.Join(nextKeys, "."), idx),
			Title:        title,
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
			nextBC := append(append([]string(nil), breadcrumbs...), i18n.DisplayLabel(node.Label))
			result = append(result, flattenAIModes(node.Action.Children, nextKeys, nextBC)...)
		case config.ActionAIRewrite:
			icon := node.Icon
			if icon == "" {
				icon = "sparkles"
			}
			kws := make([]string, 0, len(breadcrumbs)+3)
			kws = append(kws, breadcrumbs...)
			kws = append(kws, node.Action.Mode, "ai", node.Key)
			title := i18n.DisplayLabel(node.Label)
			if title != node.Label {
				kws = append(kws, node.Label)
			}
			result = append(result, OmniboxItem{
				ID:       fmt.Sprintf("ai:%s:%d", node.Action.Mode, idx),
				Title:    title,
				Subtitle: i18n.DisplayLabel(node.Action.Prompt),
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
			Subtitle: i18n.Tf("engine.snippet.subtitle", s.Trigger),
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
			Subtitle:  i18n.T("engine.tab.switch"),
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
		return i18n.T("omnibox.tab.actions")
	case config.TabClipboard:
		return i18n.T("omnibox.tab.clipboard")
	case config.TabSnippets:
		return i18n.T("omnibox.tab.snippets")
	case config.TabAI:
		return i18n.T("omnibox.tab.ai")
	case config.TabGlossary:
		return i18n.T("omnibox.tab.glossary")
	case config.TabSettings:
		return i18n.T("common.settings")
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
	start := 0
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	if start >= len(s) {
		return s
	}
	rest := s[start:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return strings.TrimRightFunc(rest, unicode.IsSpace)
	}
	line := rest[:nl]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}

func preserveClipboardSelectionIndex(items []OmniboxItem, previousSelectedID string, fallbackIdx int) int {
	if len(items) == 0 {
		return 0
	}
	if previousSelectedID != "" {
		for idx, it := range items {
			if it.ID == previousSelectedID {
				return idx
			}
		}
	}
	if fallbackIdx < 0 {
		return 0
	}
	if fallbackIdx < len(items) {
		return fallbackIdx
	}
	return len(items) - 1
}

func clearPhaseFields(s *State) {
	s.LeaderPath = nil
	s.LeaderBreadcrumbs = nil
	s.LeaderNodes = nil
	s.OmniboxTab = ""
	s.OmniboxQuery = ""
	s.OmniboxItems = nil
	s.SelectedIndex = 0
	s.ClipboardSearchQuery = ""
	s.ClipboardSearchEntries = nil
	s.AIMode = ""
	s.AIPrompt = ""
	s.AISourceText = ""
	s.AIOutput = ""
	s.AIComplete = false
	s.AIError = ""
	s.AIHistory = nil
	s.AIOptimizerPreset = ""
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
	if s.ClipboardSearchEntries != nil {
		out.ClipboardSearchEntries = append([]OmniboxClipboardEntry(nil), s.ClipboardSearchEntries...)
	}
	if s.InstalledApps != nil {
		out.InstalledApps = append([]InstalledAppEntry(nil), s.InstalledApps...)
	}
	if s.FrecencyScores != nil {
		out.FrecencyScores = make(map[string]int, len(s.FrecencyScores))
		for k, v := range s.FrecencyScores {
			out.FrecencyScores[k] = v
		}
	}
	if s.ListeningPorts != nil {
		out.ListeningPorts = append([]PortEntry(nil), s.ListeningPorts...)
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

func sourceEnabled(sources []config.SourceModuleSpec, id string) bool {
	if len(sources) == 0 {
		return true
	}
	for _, s := range sources {
		if s.ID == id {
			return s.Enabled
		}
	}
	return true
}

func promptMatchesApp(p config.PromptTemplate, ctx CapsuleContext) bool {
	if len(p.AppBundleIDs) == 0 {
		return true
	}
	bundleID := strings.TrimSpace(ctx.BundleID)
	procName := strings.TrimSpace(ctx.ProcessName)
	for _, id := range p.AppBundleIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if bundleID != "" && strings.EqualFold(trimmed, bundleID) {
			return true
		}
		if bundleID == "" && procName != "" && strings.EqualFold(trimmed, procName) {
			return true
		}
	}
	return false
}

func cfgPromptItems(cfg config.ClavisConfig, ctx CapsuleContext) []OmniboxItem {
	items := make([]OmniboxItem, 0, len(cfg.Prompts.Items))
	for _, p := range cfg.Prompts.Items {
		if !promptMatchesApp(p, ctx) {
			continue
		}
		x := p
		kws := []string{p.Trigger, p.Category, p.ID}
		if bare := strings.TrimLeft(strings.TrimSpace(p.Trigger), "/!"); bare != "" && bare != p.Trigger {
			kws = append(kws, bare)
		}
		items = append(items, OmniboxItem{
			ID:             "prompt:" + p.ID,
			Title:          p.Title,
			Subtitle:       p.Description,
			KeyHint:        p.Trigger,
			Icon:           "wand.and.stars",
			Keywords:       kws,
			Kind:           ItemPromptTemplate,
			PromptTemplate: &x,
			Score:          1000,
		})
	}
	return items
}

func matchPromptItems(items []OmniboxItem, fullQuery, rest, prefix string, frecency map[string]int) []OmniboxItem {
	if strings.TrimSpace(rest) == "" {
		return items
	}
	scored := scoreCandidatesWithFrecency(items, rest, frecency)
	trimmedFull := strings.TrimSpace(fullQuery)
	lowerRest := strings.ToLower(strings.TrimSpace(rest))
	for i := range scored {
		if scored[i].PromptTemplate == nil {
			continue
		}
		trig := strings.TrimSpace(scored[i].PromptTemplate.Trigger)
		bare := strings.TrimSpace(strings.TrimPrefix(trig, prefix))
		if strings.EqualFold(trig, trimmedFull) || (bare != "" && strings.EqualFold(bare, lowerRest)) {
			scored[i].Score += 600
		} else if bare != "" && strings.HasPrefix(strings.ToLower(bare), lowerRest) {
			scored[i].Score += 250
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored
}

func inlineOptimizePrefix(cfg config.ClavisConfig) string {
	p := strings.TrimSpace(cfg.Prompts.InlineOptimizePrefix)
	if p == "" {
		return "//"
	}
	return p
}

func matchInlineOptimizePrefix(query string, cfg config.ClavisConfig) (string, bool) {
	prefix := inlineOptimizePrefix(cfg)
	if prefix == "" {
		return "", false
	}
	trimmedLeft := strings.TrimLeft(query, " \t\u3000")
	if strings.HasPrefix(trimmedLeft, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(trimmedLeft, prefix)), true
	}
	normLeft := NormalizeFullWidth(trimmedLeft)
	if strings.HasPrefix(normLeft, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(normLeft, prefix)), true
	}
	return "", false
}

var builtInOptimizerPresetOrder = []string{
	"coding",
	"concise",
	"deep_research",
	"english_pro",
	"polite",
}

func optimizerPresetLabel(id string) string {
	switch id {
	case "coding":
		return i18n.T("optimizer.preset.coding")
	case "concise":
		return i18n.T("optimizer.preset.concise")
	case "deep_research":
		return i18n.T("optimizer.preset.deep_research")
	case "english_pro":
		return i18n.T("optimizer.preset.english_pro")
	case "polite":
		return i18n.T("optimizer.preset.polite")
	default:
		return i18n.T("optimizer.preset.coding")
	}
}

func makeOptimizePromptItems(draft string, ctx CapsuleContext, cfg config.ClavisConfig) []OmniboxItem {
	defPreset := strings.ToLower(strings.TrimSpace(cfg.Prompts.DefaultOptimizerPreset))
	validDef := false
	for _, id := range builtInOptimizerPresetOrder {
		if id == defPreset {
			validDef = true
			break
		}
	}
	if !validDef {
		defPreset = "coding"
	}
	ordered := make([]string, 0, len(builtInOptimizerPresetOrder))
	ordered = append(ordered, defPreset)
	for _, id := range builtInOptimizerPresetOrder {
		if id != defPreset {
			ordered = append(ordered, id)
		}
	}

	hasSelection := strings.TrimSpace(ctx.SelectedText) != ""
	items := make([]OmniboxItem, 0, len(ordered))
	for i, presetID := range ordered {
		label := optimizerPresetLabel(presetID)
		var title, subtitle string
		switch {
		case draft != "":
			title = i18n.Tf("engine.prompt.optimize_title", label, draft)
			subtitle = i18n.T("engine.prompt.optimize_subtitle")
		case hasSelection:
			title = i18n.Tf("engine.prompt.optimize_selection_title", label)
			subtitle = i18n.T("engine.prompt.optimize_subtitle")
		default:
			title = i18n.Tf("engine.prompt.optimize_empty_title", label)
			subtitle = i18n.T("engine.prompt.optimize_empty_subtitle")
		}
		items = append(items, OmniboxItem{
			ID:              "optimize:" + presetID,
			Title:           title,
			Subtitle:        subtitle,
			KeyHint:         label,
			Icon:            "wand.and.stars",
			Keywords:        []string{"optimize", "prompt", presetID, label},
			Kind:            ItemAIMode,
			AIMode:          "optimize",
			AIPrompt:        draft,
			OptimizerPreset: presetID,
			Score:           2000 - i*10,
		})
	}
	return items
}
