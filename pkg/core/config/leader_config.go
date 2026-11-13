package config

import (
	"fmt"
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// WindowCommand represents a supported window layout action.
type WindowCommand string

const (
	WindowLeftHalf        WindowCommand = "left_half"
	WindowRightHalf       WindowCommand = "right_half"
	WindowTopHalf         WindowCommand = "top_half"
	WindowBottomHalf      WindowCommand = "bottom_half"
	WindowMaximize        WindowCommand = "maximize"
	WindowCenter          WindowCommand = "center"
	WindowNextDisplay     WindowCommand = "next_display"
	WindowPreviousDisplay WindowCommand = "previous_display"
	WindowRestore         WindowCommand = "restore"
)

func AllWindowCommands() []WindowCommand {
	return []WindowCommand{
		WindowLeftHalf,
		WindowRightHalf,
		WindowTopHalf,
		WindowBottomHalf,
		WindowMaximize,
		WindowCenter,
		WindowNextDisplay,
		WindowPreviousDisplay,
		WindowRestore,
	}
}

func isValidWindowCommand(cmd WindowCommand) bool {
	for _, c := range AllWindowCommands() {
		if c == cmd {
			return true
		}
	}
	return false
}

// CapsuleTab identifies a tab inside the Capsule Omnibox.
type CapsuleTab string

const (
	TabActions   CapsuleTab = "actions"
	TabClipboard CapsuleTab = "clipboard"
	TabSnippets  CapsuleTab = "snippets"
	TabAI        CapsuleTab = "ai"
	TabGlossary  CapsuleTab = "glossary"
	TabSettings  CapsuleTab = "settings"
)

func AllCapsuleTabs() []CapsuleTab {
	return []CapsuleTab{
		TabActions,
		TabClipboard,
		TabSnippets,
		TabAI,
		TabGlossary,
		TabSettings,
	}
}

func isValidCapsuleTab(tab CapsuleTab) bool {
	for _, t := range AllCapsuleTabs() {
		if t == tab {
			return true
		}
	}
	return false
}

// LeaderActionType identifies a LeaderAction variant.
type LeaderActionType string

const (
	ActionGroup             LeaderActionType = "group"
	ActionAIRewrite         LeaderActionType = "ai_rewrite"
	ActionWindow            LeaderActionType = "window"
	ActionCLI               LeaderActionType = "cli"
	ActionOpenApp           LeaderActionType = "open_app"
	ActionSwitchTab         LeaderActionType = "switch_tab"
	ActionSnippet           LeaderActionType = "snippet"
	ActionToggleIME         LeaderActionType = "toggle_ime"
	ActionNormalizeGlossary LeaderActionType = "normalize_glossary"
)

// LeaderAction is a tagged union describing the action of a LeaderNode.
type LeaderAction struct {
	Type          LeaderActionType `json:"type"`
	Children      []LeaderNode     `json:"children,omitempty"`
	Mode          string           `json:"mode,omitempty"`
	Prompt        string           `json:"prompt,omitempty"`
	WindowCommand WindowCommand    `json:"windowCommand,omitempty"`
	Command       string           `json:"command,omitempty"`
	Args          []string         `json:"args,omitempty"`
	Target        string           `json:"target,omitempty"`
	Tab           CapsuleTab       `json:"tab,omitempty"`
	SnippetID     string           `json:"snippetId,omitempty"`
}

func GroupAction(children ...LeaderNode) LeaderAction {
	return LeaderAction{Type: ActionGroup, Children: children}
}

func AIRewriteAction(mode, prompt string) LeaderAction {
	return LeaderAction{Type: ActionAIRewrite, Mode: mode, Prompt: prompt}
}

func WindowAction(cmd WindowCommand) LeaderAction {
	return LeaderAction{Type: ActionWindow, WindowCommand: cmd}
}

func CLIAction(command string, args ...string) LeaderAction {
	return LeaderAction{Type: ActionCLI, Command: command, Args: args}
}

func OpenAppAction(target string) LeaderAction {
	return LeaderAction{Type: ActionOpenApp, Target: target}
}

func SwitchTabAction(tab CapsuleTab) LeaderAction {
	return LeaderAction{Type: ActionSwitchTab, Tab: tab}
}

func SnippetAction(id string) LeaderAction {
	return LeaderAction{Type: ActionSnippet, SnippetID: id}
}

func ToggleIMEAction() LeaderAction {
	return LeaderAction{Type: ActionToggleIME}
}

func NormalizeGlossaryAction() LeaderAction {
	return LeaderAction{Type: ActionNormalizeGlossary}
}

// LeaderNode represents one node in the recursive leader key tree.
type LeaderNode struct {
	Key    string       `json:"key"`
	Label  string       `json:"label"`
	Icon   string       `json:"icon,omitempty"`
	Action LeaderAction `json:"action"`
}

func decodeLeaderNode(tbl *toml.Table, path string) (LeaderNode, error) {
	key, err := requiredString(tbl, "key", path)
	if err != nil {
		return LeaderNode{}, err
	}
	label, err := requiredString(tbl, "label", path)
	if err != nil {
		return LeaderNode{}, err
	}
	icon, _, err := optionalString(tbl, "icon", path)
	if err != nil {
		return LeaderNode{}, err
	}

	var actionType string
	if s, ok, err := optionalString(tbl, "action", path); err != nil {
		return LeaderNode{}, err
	} else if ok {
		actionType = s
	} else if s, ok, err := optionalString(tbl, "type", path); err != nil {
		return LeaderNode{}, err
	} else if ok {
		actionType = s
	} else {
		return LeaderNode{}, &DecodingError{
			KeyPath: joinPath(path, "action"),
			Message: "Missing required 'action' type",
		}
	}

	var action LeaderAction
	switch LeaderActionType(actionType) {
	case ActionGroup:
		childTables, _, err := optionalTableArray(tbl, "children", path)
		if err != nil {
			return LeaderNode{}, err
		}
		children := make([]LeaderNode, 0, len(childTables))
		for i, childTbl := range childTables {
			childPath := fmt.Sprintf("%s[%d]", joinPath(path, "children"), i)
			child, err := decodeLeaderNode(childTbl, childPath)
			if err != nil {
				return LeaderNode{}, err
			}
			children = append(children, child)
		}
		action = LeaderAction{Type: ActionGroup, Children: children}

	case ActionAIRewrite:
		mode, err := requiredString(tbl, "mode", path)
		if err != nil {
			return LeaderNode{}, err
		}
		prompt, err := requiredString(tbl, "prompt", path)
		if err != nil {
			return LeaderNode{}, err
		}
		action = AIRewriteAction(mode, prompt)

	case ActionWindow:
		rawCmd, err := requiredString(tbl, "command", path)
		if err != nil {
			return LeaderNode{}, err
		}
		cmd := WindowCommand(rawCmd)
		if !isValidWindowCommand(cmd) {
			allowed := make([]string, len(AllWindowCommands()))
			for i, c := range AllWindowCommands() {
				allowed[i] = string(c)
			}
			return LeaderNode{}, &DecodingError{
				KeyPath: joinPath(path, "command"),
				Message: fmt.Sprintf("Unknown window command '%s'; expected one of: %s", rawCmd, strings.Join(allowed, ", ")),
			}
		}
		action = WindowAction(cmd)

	case ActionCLI:
		command, err := requiredString(tbl, "command", path)
		if err != nil {
			return LeaderNode{}, err
		}
		args, _, err := optionalStringArray(tbl, "args", path)
		if err != nil {
			return LeaderNode{}, err
		}
		action = CLIAction(command, args...)

	case ActionOpenApp:
		target, err := requiredString(tbl, "target", path)
		if err != nil {
			return LeaderNode{}, err
		}
		action = OpenAppAction(target)

	case ActionSwitchTab:
		rawTab, err := requiredString(tbl, "tab", path)
		if err != nil {
			return LeaderNode{}, err
		}
		tab := CapsuleTab(rawTab)
		if !isValidCapsuleTab(tab) {
			allowed := make([]string, len(AllCapsuleTabs()))
			for i, t := range AllCapsuleTabs() {
				allowed[i] = string(t)
			}
			return LeaderNode{}, &DecodingError{
				KeyPath: joinPath(path, "tab"),
				Message: fmt.Sprintf("Unknown capsule tab '%s'; expected one of: %s", rawTab, strings.Join(allowed, ", ")),
			}
		}
		action = SwitchTabAction(tab)

	case ActionSnippet:
		snippetID, ok, err := optionalString(tbl, "snippet_id", path)
		if err != nil {
			return LeaderNode{}, err
		}
		if !ok {
			snippetID, err = requiredString(tbl, "id", path)
			if err != nil {
				return LeaderNode{}, err
			}
		}
		action = SnippetAction(snippetID)

	case ActionToggleIME:
		action = ToggleIMEAction()

	case ActionNormalizeGlossary:
		action = NormalizeGlossaryAction()

	default:
		return LeaderNode{}, &DecodingError{
			KeyPath: joinPath(path, "action"),
			Message: fmt.Sprintf("Unknown leader action '%s'", actionType),
		}
	}

	return LeaderNode{
		Key:    key,
		Label:  label,
		Icon:   icon,
		Action: action,
	}, nil
}

func (n LeaderNode) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("key", toml.StringVal(n.Key))
	t.Set("label", toml.StringVal(n.Label))
	if n.Icon != "" {
		t.Set("icon", toml.StringVal(n.Icon))
	}
	t.Set("action", toml.StringVal(string(n.Action.Type)))

	switch n.Action.Type {
	case ActionGroup:
		vals := make([]toml.Value, len(n.Action.Children))
		for i, c := range n.Action.Children {
			vals[i] = toml.TableVal(c.toTOML())
		}
		t.Set("children", toml.ArrayVal(vals))
	case ActionAIRewrite:
		t.Set("mode", toml.StringVal(n.Action.Mode))
		t.Set("prompt", toml.StringVal(n.Action.Prompt))
	case ActionWindow:
		t.Set("command", toml.StringVal(string(n.Action.WindowCommand)))
	case ActionCLI:
		t.Set("command", toml.StringVal(n.Action.Command))
		t.Set("args", stringsToTOMLArray(n.Action.Args))
	case ActionOpenApp:
		t.Set("target", toml.StringVal(n.Action.Target))
	case ActionSwitchTab:
		t.Set("tab", toml.StringVal(string(n.Action.Tab)))
	case ActionSnippet:
		t.Set("snippet_id", toml.StringVal(n.Action.SnippetID))
	case ActionToggleIME, ActionNormalizeGlossary:
		// no extra fields
	}
	return t
}

// LeaderConfig represents leader.toml.
type LeaderConfig struct {
	Nodes []LeaderNode `json:"nodes"`
}

func DefaultLeaderConfig() LeaderConfig {
	return LeaderConfig{
		Nodes: []LeaderNode{
			{
				Key:   "w",
				Label: "Window",
				Icon:  "macwindow",
				Action: GroupAction(
					LeaderNode{Key: "h", Label: "Left Half", Icon: "rectangle.lefthalf.filled", Action: WindowAction(WindowLeftHalf)},
					LeaderNode{Key: "l", Label: "Right Half", Icon: "rectangle.righthalf.filled", Action: WindowAction(WindowRightHalf)},
					LeaderNode{Key: "m", Label: "Maximize", Icon: "arrow.up.left.and.arrow.down.right", Action: WindowAction(WindowMaximize)},
					LeaderNode{Key: "c", Label: "Center", Icon: "rectangle.center.inset.filled", Action: WindowAction(WindowCenter)},
				),
			},
			{
				Key:   "a",
				Label: "AI Rewrite",
				Icon:  "sparkles",
				Action: GroupAction(
					LeaderNode{
						Key:    "p",
						Label:  "Polish",
						Icon:   "wand.and.stars",
						Action: AIRewriteAction("polish", "Improve clarity, grammar, and flow while preserving the original meaning and tone."),
					},
					LeaderNode{
						Key:    "t",
						Label:  "Translate EN/ZH",
						Icon:   "globe",
						Action: AIRewriteAction("translate", "Translate Chinese to natural English or English to natural Chinese."),
					},
				),
			},
			{
				Key:    "c",
				Label:  "Clipboard",
				Icon:   "doc.on.clipboard",
				Action: SwitchTabAction(TabClipboard),
			},
			{
				Key:    "i",
				Label:  "Toggle IME",
				Icon:   "keyboard",
				Action: ToggleIMEAction(),
			},
			{
				Key:    "t",
				Label:  "Terminal",
				Icon:   "terminal",
				Action: OpenAppAction("com.apple.Terminal"),
			},
		},
	}
}

// DecodeLeaderConfig parses and decodes leader.toml.
func DecodeLeaderConfig(input string) (LeaderConfig, error) {
	tbl, err := toml.Parse(input)
	if err != nil {
		return LeaderConfig{}, err
	}
	return LeaderConfigFromTOML(tbl)
}

func LeaderConfigFromTOML(tbl *toml.Table) (LeaderConfig, error) {
	nodeTables, _, err := optionalTableArray(tbl, "nodes", "")
	if err != nil {
		return LeaderConfig{}, err
	}
	nodes := make([]LeaderNode, 0, len(nodeTables))
	for i, nt := range nodeTables {
		node, err := decodeLeaderNode(nt, fmt.Sprintf("nodes[%d]", i))
		if err != nil {
			return LeaderConfig{}, err
		}
		nodes = append(nodes, node)
	}
	return LeaderConfig{Nodes: nodes}, nil
}

func (c LeaderConfig) Encode() string {
	root := toml.NewTable()
	vals := make([]toml.Value, len(c.Nodes))
	for i, n := range c.Nodes {
		vals[i] = toml.TableVal(n.toTOML())
	}
	root.Set("nodes", toml.ArrayVal(vals))
	return toml.Encode(root)
}
