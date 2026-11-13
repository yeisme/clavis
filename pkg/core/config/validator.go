package config

import (
	"fmt"
	"math"
	"net/url"
	"strings"
)

// ValidationSeverity indicates error vs warning.
type ValidationSeverity string

const (
	SeverityError   ValidationSeverity = "error"
	SeverityWarning ValidationSeverity = "warning"
)

// ValidationIssue describes one config validation finding.
type ValidationIssue struct {
	File     string             `json:"file"`
	KeyPath  string             `json:"keyPath"`
	Severity ValidationSeverity `json:"severity"`
	Message  string             `json:"message"`
}

// ConfigValidator validates all Clavis configuration files.
type ConfigValidator struct{}

// Validate runs all config validators against cfg.
func Validate(cfg ClavisConfig) []ValidationIssue {
	var issues []ValidationIssue
	issues = append(issues, ValidateMain(cfg.Main)...)
	knownSnippetIDs := make(map[string]struct{}, len(cfg.Snippets.Items))
	for _, s := range cfg.Snippets.Items {
		knownSnippetIDs[s.ID] = struct{}{}
	}
	issues = append(issues, ValidateLeader(cfg.Leader, knownSnippetIDs)...)
	issues = append(issues, ValidateSnippets(cfg.Snippets)...)
	issues = append(issues, ValidateGlossary(cfg.Glossary)...)
	issues = append(issues, ValidateIMERules(cfg.IMERules)...)
	return issues
}

// ValidateMain validates MainConfig (config.toml).
func ValidateMain(main MainConfig) []ValidationIssue {
	var issues []ValidationIssue
	file := string(FileMain)

	leaderKey := strings.TrimSpace(main.Trigger.LeaderKey)
	if leaderKey == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.leader_key", Severity: SeverityError,
			Message: "Trigger leader_key cannot be empty",
		})
	} else if !isSupportedTriggerModifierKey(leaderKey) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.leader_key", Severity: SeverityError,
			Message: fmt.Sprintf("Unsupported trigger leader_key '%s'", main.Trigger.LeaderKey),
		})
	}

	doubleTapKey := strings.TrimSpace(main.Trigger.DoubleTapKey)
	if doubleTapKey == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.double_tap_key", Severity: SeverityError,
			Message: "Trigger double_tap_key cannot be empty",
		})
	} else if !isSupportedTriggerModifierKey(doubleTapKey) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.double_tap_key", Severity: SeverityError,
			Message: fmt.Sprintf("Unsupported trigger double_tap_key '%s'", main.Trigger.DoubleTapKey),
		})
	}

	shortcut := strings.TrimSpace(main.Trigger.CapsuleShortcut)
	if shortcut == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.capsule_shortcut", Severity: SeverityError,
			Message: "Trigger capsule_shortcut cannot be empty",
		})
	} else if !isValidCapsuleShortcut(shortcut) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.capsule_shortcut", Severity: SeverityError,
			Message: fmt.Sprintf("Invalid trigger capsule_shortcut '%s'", main.Trigger.CapsuleShortcut),
		})
	}

	if main.Trigger.DoubleTapIntervalMs < 100 || main.Trigger.DoubleTapIntervalMs > 800 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.double_tap_interval_ms", Severity: SeverityError,
			Message: fmt.Sprintf("double_tap_interval_ms must be within 100..800 (got %d)", main.Trigger.DoubleTapIntervalMs),
		})
	}

	theme := strings.ToLower(strings.TrimSpace(main.Appearance.Theme))
	if theme == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.theme", Severity: SeverityError,
			Message: "appearance.theme cannot be empty",
		})
	} else if theme != "system" && theme != "light" && theme != "dark" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.theme", Severity: SeverityWarning,
			Message: fmt.Sprintf("Unknown appearance theme '%s'; expected 'system', 'light', or 'dark'", main.Appearance.Theme),
		})
	}

	if main.Appearance.CapsuleWidth <= 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.capsule_width", Severity: SeverityError,
			Message: "capsule_width must be positive",
		})
	}
	if main.Appearance.MaxVisibleResults <= 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.max_visible_results", Severity: SeverityError,
			Message: "max_visible_results must be positive",
		})
	}
	if math.IsNaN(main.Appearance.CornerRadius) || math.IsInf(main.Appearance.CornerRadius, 0) || main.Appearance.CornerRadius < 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.corner_radius", Severity: SeverityError,
			Message: "appearance.corner_radius must be non-negative",
		})
	}

	if strings.TrimSpace(main.AI.Provider) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.provider", Severity: SeverityError,
			Message: "AI provider cannot be empty",
		})
	}
	if !isValidHTTPURL(main.AI.BaseURL) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.base_url", Severity: SeverityError,
			Message: fmt.Sprintf("AI base_url '%s' must be a valid http or https URL with a host", main.AI.BaseURL),
		})
	}
	if strings.TrimSpace(main.AI.Model) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.model", Severity: SeverityError,
			Message: "AI model cannot be empty",
		})
	}
	if strings.TrimSpace(main.AI.KeychainAccount) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.keychain_account", Severity: SeverityError,
			Message: "AI keychain_account cannot be empty",
		})
	}
	if main.AI.Temperature < 0.0 || main.AI.Temperature > 2.0 || math.IsNaN(main.AI.Temperature) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.temperature", Severity: SeverityWarning,
			Message: fmt.Sprintf("AI temperature %v is outside recommended 0.0..2.0 range", main.AI.Temperature),
		})
	}
	if main.AI.TimeoutSeconds <= 0 || math.IsNaN(main.AI.TimeoutSeconds) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.timeout_seconds", Severity: SeverityError,
			Message: "AI timeout_seconds must be positive",
		})
	}

	if main.Clipboard.MaxHistoryItems <= 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "clipboard.max_history_items", Severity: SeverityError,
			Message: "clipboard.max_history_items must be positive",
		})
	}
	if main.Clipboard.RetentionDays < 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "clipboard.retention_days", Severity: SeverityError,
			Message: "clipboard.retention_days cannot be negative",
		})
	}

	if main.IME.Enabled && strings.TrimSpace(main.IME.DefaultInputSourceID) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ime.default_input_source_id", Severity: SeverityError,
			Message: "ime.default_input_source_id cannot be empty when IME is enabled",
		})
	}

	return issues
}

// ValidateLeader validates LeaderConfig (leader.toml).
// Pass nil for knownSnippetIDs to skip cross-file snippet existence warning.
func ValidateLeader(leader LeaderConfig, knownSnippetIDs map[string]struct{}) []ValidationIssue {
	var issues []ValidationIssue
	validateLeaderLevel(leader.Nodes, "nodes", knownSnippetIDs, &issues)
	return issues
}

func validateLeaderLevel(nodes []LeaderNode, basePath string, knownSnippetIDs map[string]struct{}, issues *[]ValidationIssue) {
	file := string(FileLeader)
	seenKeys := make(map[string]int, len(nodes))

	for i, node := range nodes {
		nodePath := fmt.Sprintf("%s[%d]", basePath, i)
		trimmedKey := strings.TrimSpace(node.Key)
		if trimmedKey == "" {
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".key", Severity: SeverityError,
				Message: "Leader node key cannot be empty",
			})
		} else if firstIdx, dup := seenKeys[node.Key]; dup {
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".key", Severity: SeverityError,
				Message: fmt.Sprintf("Duplicate leader key '%s' at the same level (first seen at index %d)", node.Key, firstIdx),
			})
		} else {
			seenKeys[node.Key] = i
		}

		if strings.TrimSpace(node.Label) == "" {
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".label", Severity: SeverityError,
				Message: "Leader node label cannot be empty",
			})
		}

		switch node.Action.Type {
		case ActionGroup:
			if len(node.Action.Children) == 0 {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".children", Severity: SeverityWarning,
					Message: fmt.Sprintf("Leader group '%s' has no child nodes", node.Label),
				})
			} else {
				validateLeaderLevel(node.Action.Children, nodePath+".children", knownSnippetIDs, issues)
			}
		case ActionAIRewrite:
			if strings.TrimSpace(node.Action.Mode) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".mode", Severity: SeverityError,
					Message: "AI rewrite mode cannot be empty",
				})
			}
			if strings.TrimSpace(node.Action.Prompt) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".prompt", Severity: SeverityError,
					Message: "AI rewrite prompt cannot be empty",
				})
			}
		case ActionCLI:
			if strings.TrimSpace(node.Action.Command) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".command", Severity: SeverityError,
					Message: "CLI action command cannot be empty",
				})
			}
		case ActionOpenApp:
			if strings.TrimSpace(node.Action.Target) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".target", Severity: SeverityError,
					Message: "Open app target cannot be empty",
				})
			}
		case ActionSnippet:
			if strings.TrimSpace(node.Action.SnippetID) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".snippet_id", Severity: SeverityError,
					Message: "Snippet action id cannot be empty",
				})
			} else if knownSnippetIDs != nil {
				if _, ok := knownSnippetIDs[node.Action.SnippetID]; !ok {
					*issues = append(*issues, ValidationIssue{
						File: file, KeyPath: nodePath + ".snippet_id", Severity: SeverityWarning,
						Message: fmt.Sprintf("Referenced snippet id '%s' does not exist in snippets.toml", node.Action.SnippetID),
					})
				}
			}
		case ActionWindow:
			if !isValidWindowCommand(node.Action.WindowCommand) {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".command", Severity: SeverityError,
					Message: fmt.Sprintf("Unknown window command '%s'", node.Action.WindowCommand),
				})
			}
		case ActionSwitchTab:
			if !isValidCapsuleTab(node.Action.Tab) {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".tab", Severity: SeverityError,
					Message: fmt.Sprintf("Unknown capsule tab '%s'", node.Action.Tab),
				})
			}
		case ActionToggleIME, ActionNormalizeGlossary:
			// valid without extra fields
		default:
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".action", Severity: SeverityError,
				Message: fmt.Sprintf("Unknown leader action '%s'", node.Action.Type),
			})
		}
	}
}

// ValidateSnippets validates SnippetsConfig (snippets.toml).
func ValidateSnippets(snippets SnippetsConfig) []ValidationIssue {
	var issues []ValidationIssue
	file := string(FileSnippets)
	seenIDs := make(map[string]struct{}, len(snippets.Items))
	seenTriggers := make(map[string]struct{}, len(snippets.Items))

	for i, item := range snippets.Items {
		path := fmt.Sprintf("snippets[%d]", i)
		if strings.TrimSpace(item.ID) == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".id", Severity: SeverityError,
				Message: "Snippet id cannot be empty",
			})
		} else if _, dup := seenIDs[item.ID]; dup {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".id", Severity: SeverityError,
				Message: fmt.Sprintf("Duplicate snippet id '%s'", item.ID),
			})
		} else {
			seenIDs[item.ID] = struct{}{}
		}

		if strings.TrimSpace(item.Trigger) == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".trigger", Severity: SeverityError,
				Message: "Snippet trigger cannot be empty",
			})
		} else if _, dup := seenTriggers[item.Trigger]; dup {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".trigger", Severity: SeverityError,
				Message: fmt.Sprintf("Duplicate snippet trigger '%s'", item.Trigger),
			})
		} else {
			seenTriggers[item.Trigger] = struct{}{}
		}
	}
	return issues
}

// ValidateGlossary validates GlossaryConfig (glossary.toml).
func ValidateGlossary(glossary GlossaryConfig) []ValidationIssue {
	var issues []ValidationIssue
	file := string(FileGlossary)
	seenTerms := make(map[string]struct{}, len(glossary.Terms))

	for i, entry := range glossary.Terms {
		path := fmt.Sprintf("terms[%d]", i)
		trimmedTerm := strings.TrimSpace(entry.Term)
		if trimmedTerm == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".term", Severity: SeverityError,
				Message: "Glossary term cannot be empty",
			})
		} else {
			lower := strings.ToLower(trimmedTerm)
			if _, dup := seenTerms[lower]; dup {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: path + ".term", Severity: SeverityWarning,
					Message: fmt.Sprintf("Duplicate glossary term '%s'", entry.Term),
				})
			} else {
				seenTerms[lower] = struct{}{}
			}
		}
	}
	return issues
}

// ValidateIMERules validates IMERulesConfig (ime-rules.toml).
func ValidateIMERules(imeRules IMERulesConfig) []ValidationIssue {
	var issues []ValidationIssue
	file := string(FileIMERules)
	seenBundleIDs := make(map[string]struct{}, len(imeRules.Rules))
	seenProcessNames := make(map[string]struct{}, len(imeRules.Rules))

	for i, rule := range imeRules.Rules {
		path := fmt.Sprintf("rules[%d]", i)
		bundleID := strings.TrimSpace(rule.BundleID)
		procName := strings.TrimSpace(rule.ProcessName)

		if bundleID == "" && procName == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".bundle_id", Severity: SeverityError,
				Message: "IME rule must specify bundle_id or process_name",
			})
		}
		if bundleID != "" {
			if _, dup := seenBundleIDs[bundleID]; dup {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: path + ".bundle_id", Severity: SeverityError,
					Message: fmt.Sprintf("Duplicate IME rule for bundle_id '%s'", rule.BundleID),
				})
			} else {
				seenBundleIDs[bundleID] = struct{}{}
			}
		}
		if procName != "" {
			lowerProc := strings.ToLower(procName)
			if _, dup := seenProcessNames[lowerProc]; dup {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: path + ".process_name", Severity: SeverityError,
					Message: fmt.Sprintf("Duplicate IME rule for process_name '%s'", rule.ProcessName),
				})
			} else {
				seenProcessNames[lowerProc] = struct{}{}
			}
		}
		if strings.TrimSpace(rule.InputSourceID) == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".input_source_id", Severity: SeverityError,
				Message: "IME rule input_source_id cannot be empty",
			})
		}
	}
	return issues
}

var supportedTriggerModifierKeys = map[string]struct{}{
	"right_option": {}, "left_option": {}, "option": {}, "alt": {}, "opt": {},
	"right_alt": {}, "left_alt": {}, "roption": {}, "loption": {}, "ralt": {}, "lalt": {},
	"right_command": {}, "left_command": {}, "command": {}, "cmd": {}, "super": {},
	"right_cmd": {}, "left_cmd": {}, "rcommand": {}, "lcommand": {}, "rcmd": {}, "lcmd": {},
	"right_win": {}, "left_win": {}, "win": {}, "windows": {}, "rwin": {}, "lwin": {},
	"right_meta": {}, "left_meta": {}, "meta": {}, "rmeta": {}, "lmeta": {},
	"right_shift": {}, "left_shift": {}, "shift": {}, "rshift": {}, "lshift": {},
	"right_control": {}, "left_control": {}, "control": {}, "ctrl": {}, "rctrl": {}, "lctrl": {},
	"caps_lock": {}, "capslock": {}, "caps": {},
}

var supportedShortcutModifiers = map[string]struct{}{
	"command": {}, "cmd": {}, "super": {}, "win": {}, "windows": {}, "meta": {},
	"option": {}, "opt": {}, "alt": {},
	"shift":   {},
	"control": {}, "ctrl": {},
	"fn": {}, "function": {},
}

var supportedShortcutKeys = map[string]struct{}{
	"a": {}, "b": {}, "c": {}, "d": {}, "e": {}, "f": {}, "g": {}, "h": {}, "i": {}, "j": {}, "k": {}, "l": {}, "m": {},
	"n": {}, "o": {}, "p": {}, "q": {}, "r": {}, "s": {}, "t": {}, "u": {}, "v": {}, "w": {}, "x": {}, "y": {}, "z": {},
	"0": {}, "1": {}, "2": {}, "3": {}, "4": {}, "5": {}, "6": {}, "7": {}, "8": {}, "9": {},
	"space": {}, "return": {}, "enter": {}, "tab": {}, "escape": {}, "esc": {},
	"backspace": {}, "delete": {}, "forwarddelete": {},
	"left": {}, "right": {}, "up": {}, "down": {}, "home": {}, "end": {}, "pageup": {}, "pagedown": {},
	"minus": {}, "-": {}, "equal": {}, "=": {}, "leftbracket": {}, "[": {}, "rightbracket": {}, "]": {},
	"semicolon": {}, ";": {}, "quote": {}, "'": {}, "comma": {}, ",": {}, "period": {}, ".": {},
	"slash": {}, "/": {}, "backslash": {}, "\\": {}, "grave": {}, "backtick": {}, "`": {},
	"f1": {}, "f2": {}, "f3": {}, "f4": {}, "f5": {}, "f6": {}, "f7": {}, "f8": {}, "f9": {}, "f10": {}, "f11": {}, "f12": {},
}

func isSupportedTriggerModifierKey(raw string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "-", "_")
	_, ok := supportedTriggerModifierKeys[normalized]
	return ok
}

func isValidCapsuleShortcut(shortcut string) bool {
	parts := strings.Split(shortcut, "+")
	if len(parts) == 0 {
		return false
	}
	keyCount := 0
	for _, part := range parts {
		token := strings.ToLower(strings.TrimSpace(part))
		if token == "" {
			return false
		}
		if _, isMod := supportedShortcutModifiers[token]; isMod {
			continue
		}
		if _, isKey := supportedShortcutKeys[token]; isKey {
			keyCount++
			if keyCount > 1 {
				return false
			}
			continue
		}
		return false
	}
	return keyCount == 1
}

func isValidHTTPURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}
