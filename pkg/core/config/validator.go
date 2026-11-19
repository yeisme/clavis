package config

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/yeisme/clavis/pkg/core/i18n"
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
	knownWorkflowIDs := make(map[string]struct{}, len(cfg.Workflows.Workflows))
	for _, workflow := range cfg.Workflows.Workflows {
		knownWorkflowIDs[workflow.ID] = struct{}{}
	}
	issues = append(issues, ValidateLeader(cfg.Leader, knownSnippetIDs, knownWorkflowIDs)...)
	issues = append(issues, validateCLIActionProfiles(cfg.Leader, cfg.Main.CLI)...)
	issues = append(issues, ValidateSnippets(cfg.Snippets)...)
	issues = append(issues, ValidateGlossary(cfg.Glossary)...)
	issues = append(issues, ValidateIMERules(cfg.IMERules)...)
	issues = append(issues, ValidatePrompts(cfg.Prompts)...)
	issues = append(issues, ValidateComponents(cfg.Components)...)
	issues = append(issues, ValidateWorkflows(cfg.Workflows)...)
	return issues
}

// ValidateMain validates MainConfig (config.toml).
func ValidateMain(main MainConfig) []ValidationIssue {
	var issues []ValidationIssue
	issues = append(issues, validateCLIConfig(main.CLI)...)
	issues = append(issues, ValidateMCP(main.MCP)...)
	file := string(FileMain)

	leaderKey := strings.TrimSpace(main.Trigger.LeaderKey)
	if leaderKey == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.leader_key", Severity: SeverityError,
			Message: i18n.T("diag.trigger.leader_key.empty"),
		})
	} else if !isSupportedTriggerModifierKey(leaderKey) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.leader_key", Severity: SeverityError,
			Message: i18n.Tf("diag.trigger.leader_key.unsupported", main.Trigger.LeaderKey),
		})
	}

	doubleTapKey := strings.TrimSpace(main.Trigger.DoubleTapKey)
	if doubleTapKey == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.double_tap_key", Severity: SeverityError,
			Message: i18n.T("diag.trigger.double_tap_key.empty"),
		})
	} else if !isSupportedTriggerModifierKey(doubleTapKey) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.double_tap_key", Severity: SeverityError,
			Message: i18n.Tf("diag.trigger.double_tap_key.unsupported", main.Trigger.DoubleTapKey),
		})
	}

	shortcut := strings.TrimSpace(main.Trigger.CapsuleShortcut)
	if shortcut == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.capsule_shortcut", Severity: SeverityError,
			Message: i18n.T("diag.trigger.capsule_shortcut.empty"),
		})
	} else if !isValidCapsuleShortcut(shortcut) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.capsule_shortcut", Severity: SeverityError,
			Message: i18n.Tf("diag.trigger.capsule_shortcut.invalid", main.Trigger.CapsuleShortcut),
		})
	}

	// clipboard_shortcut is optional: empty disables the dedicated Clipboard History hotkey.
	if clip := strings.TrimSpace(main.Trigger.ClipboardShortcut); clip != "" && !isValidCapsuleShortcut(clip) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.clipboard_shortcut", Severity: SeverityError,
			Message: i18n.Tf("diag.trigger.clipboard_shortcut.invalid", main.Trigger.ClipboardShortcut),
		})
	}

	if main.Trigger.LeaderRevealDelayMs < 0 || main.Trigger.LeaderRevealDelayMs > MaxLeaderRevealDelayMs {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.leader_reveal_delay_ms", Severity: SeverityWarning,
			Message: i18n.Tf("diag.trigger.leader_reveal_delay.range", MaxLeaderRevealDelayMs, main.Trigger.LeaderRevealDelayMs),
		})
	}
	if main.Trigger.DoubleTapIntervalMs < 100 || main.Trigger.DoubleTapIntervalMs > 800 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "trigger.double_tap_interval_ms", Severity: SeverityError,
			Message: i18n.Tf("diag.trigger.double_tap_interval.range", main.Trigger.DoubleTapIntervalMs),
		})
	}

	// Validate [keymap] (only enforce threshold when TapHoldThresholdMs != 0 or Keymap is initialized)
	if main.Keymap.TapHoldThresholdMs != 0 && (main.Keymap.TapHoldThresholdMs < 80 || main.Keymap.TapHoldThresholdMs > 500) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "keymap.tap_hold_threshold_ms", Severity: SeverityError,
			Message: i18n.Tf("diag.keymap.tap_hold_threshold.range", main.Keymap.TapHoldThresholdMs),
		})
	}
	for i, r := range main.Keymap.Remaps {
		rPath := fmt.Sprintf("keymap.remaps[%d]", i)
		if strings.TrimSpace(r.From) == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: rPath + ".from", Severity: SeverityError,
				Message: i18n.T("diag.keymap.remap.from.empty"),
			})
		}
		if strings.TrimSpace(r.Tap) == "" && strings.TrimSpace(r.Hold) == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: rPath + ".tap", Severity: SeverityError,
				Message: i18n.T("diag.keymap.remap.need_tap_or_hold"),
			})
		}
	}

	theme := strings.ToLower(strings.TrimSpace(main.Appearance.Theme))
	if theme == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.theme", Severity: SeverityError,
			Message: i18n.T("diag.appearance.theme.empty"),
		})
	} else if theme != "system" && theme != "light" && theme != "dark" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.theme", Severity: SeverityWarning,
			Message: i18n.Tf("diag.appearance.theme.unknown", main.Appearance.Theme),
		})
	}

	language := strings.TrimSpace(main.Appearance.Language)
	if language != "" && language != "auto" && language != "zh-CN" && language != "en" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.language", Severity: SeverityWarning,
			Message: i18n.Tf("diag.appearance.language.unknown", main.Appearance.Language),
		})
	}

	animations := strings.TrimSpace(main.Appearance.Animations)
	if animations != "" && animations != "auto" && animations != "full" && animations != "reduced" && animations != "off" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.animations", Severity: SeverityWarning,
			Message: i18n.Tf("diag.appearance.animations.unknown", main.Appearance.Animations),
		})
	}

	if math.IsNaN(main.Appearance.CapsuleWidth) || math.IsInf(main.Appearance.CapsuleWidth, 0) || main.Appearance.CapsuleWidth <= 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.capsule_width", Severity: SeverityError,
			Message: i18n.T("diag.appearance.capsule_width.positive"),
		})
	}
	if main.Appearance.MaxVisibleResults <= 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.max_visible_results", Severity: SeverityError,
			Message: i18n.T("diag.appearance.max_visible.positive"),
		})
	}
	if math.IsNaN(main.Appearance.CornerRadius) || math.IsInf(main.Appearance.CornerRadius, 0) || main.Appearance.CornerRadius < 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.corner_radius", Severity: SeverityError,
			Message: i18n.T("diag.appearance.corner_radius.nonneg"),
		})
	}

	if strings.TrimSpace(main.AI.Provider) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.provider", Severity: SeverityError,
			Message: i18n.T("diag.ai.provider.empty"),
		})
	}
	if main.AI.Protocol != "" && !isValidAIProtocol(main.AI.Protocol) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.protocol", Severity: SeverityError,
			Message: i18n.Tf("diag.ai.protocol.unsupported", main.AI.Protocol),
		})
	}
	if !isValidHTTPURL(main.AI.BaseURL) {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.base_url", Severity: SeverityError,
			Message: i18n.Tf("diag.ai.base_url.invalid", main.AI.BaseURL),
		})
	}
	if strings.TrimSpace(main.AI.Model) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.model", Severity: SeverityError,
			Message: i18n.T("diag.ai.model.empty"),
		})
	}
	if strings.TrimSpace(main.AI.KeychainAccount) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.keychain_account", Severity: SeverityError,
			Message: i18n.T("diag.ai.keychain_account.empty"),
		})
	}
	if math.IsNaN(main.AI.Temperature) || math.IsInf(main.AI.Temperature, 0) || main.AI.Temperature < 0.0 || main.AI.Temperature > 2.0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.temperature", Severity: SeverityWarning,
			Message: i18n.Tf("diag.ai.temperature.range", main.AI.Temperature),
		})
	}
	if math.IsNaN(main.AI.TimeoutSeconds) || math.IsInf(main.AI.TimeoutSeconds, 0) || main.AI.TimeoutSeconds <= 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ai.timeout_seconds", Severity: SeverityError,
			Message: i18n.T("diag.ai.timeout.positive"),
		})
	}

	if len(main.AI.Profiles) > 0 {
		seenProfiles := make(map[string]struct{}, len(main.AI.Profiles))
		for i, prof := range main.AI.Profiles {
			pPath := fmt.Sprintf("ai.profiles[%d]", i)
			pid := strings.TrimSpace(prof.ID)
			if pid == "" {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".id", Severity: SeverityError,
					Message: i18n.T("diag.ai.profile.id.empty"),
				})
			} else if _, dup := seenProfiles[pid]; dup {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".id", Severity: SeverityError,
					Message: i18n.Tf("diag.ai.profile.id.duplicate", prof.ID),
				})
			} else {
				seenProfiles[pid] = struct{}{}
			}
			if strings.TrimSpace(prof.Provider) == "" {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".provider", Severity: SeverityError,
					Message: i18n.T("diag.ai.profile.provider.empty"),
				})
			}
			if prof.Protocol != "" && !isValidAIProtocol(prof.Protocol) {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".protocol", Severity: SeverityError,
					Message: i18n.Tf("diag.ai.profile.protocol.unsupported", prof.Protocol),
				})
			}
			if !isValidHTTPURL(prof.BaseURL) {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".base_url", Severity: SeverityError,
					Message: i18n.Tf("diag.ai.profile.base_url.invalid", prof.BaseURL),
				})
			}
			if strings.TrimSpace(prof.Model) == "" {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".model", Severity: SeverityError,
					Message: i18n.T("diag.ai.profile.model.empty"),
				})
			}
			if strings.TrimSpace(prof.KeychainAccount) == "" && !strings.EqualFold(strings.TrimSpace(prof.Provider), "ollama") {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".keychain_account", Severity: SeverityError,
					Message: i18n.T("diag.ai.profile.keychain_account.empty"),
				})
			}
			if math.IsNaN(prof.Temperature) || math.IsInf(prof.Temperature, 0) || prof.Temperature < 0.0 || prof.Temperature > 2.0 {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".temperature", Severity: SeverityWarning,
					Message: i18n.Tf("diag.ai.profile.temperature.range", prof.Temperature),
				})
			}
			if math.IsNaN(prof.TimeoutSeconds) || math.IsInf(prof.TimeoutSeconds, 0) || prof.TimeoutSeconds <= 0 {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: pPath + ".timeout_seconds", Severity: SeverityError,
					Message: i18n.T("diag.ai.profile.timeout.positive"),
				})
			}
		}
		if strings.TrimSpace(main.AI.ActiveProfileID) != "" {
			if _, ok := main.AI.ProfileByID(main.AI.ActiveProfileID); !ok {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: "ai.active_profile_id", Severity: SeverityError,
					Message: i18n.Tf("diag.ai.profile.active_missing", main.AI.ActiveProfileID),
				})
			}
		}
	}

	if main.Clipboard.MaxHistoryItems <= 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "clipboard.max_history_items", Severity: SeverityError,
			Message: i18n.T("diag.clipboard.max_history.positive"),
		})
	}
	if main.Clipboard.RetentionDays < 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "clipboard.retention_days", Severity: SeverityError,
			Message: i18n.T("diag.clipboard.retention_days.nonneg"),
		})
	}
	if main.Clipboard.MediaQuotaMB < 0 {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "clipboard.media_quota_mb", Severity: SeverityError,
			Message: i18n.T("diag.clipboard.media_quota.nonneg"),
		})
	}

	if main.Appearance.VisualPreset != "" && main.Appearance.VisualPreset != "spatial_3d" && main.Appearance.VisualPreset != "liquid_glass" && main.Appearance.VisualPreset != "classic" {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "appearance.visual_preset", Severity: SeverityError, Message: "unsupported visual preset"})
	}
	if main.Appearance.CandidateBarOrientation != "" && main.Appearance.CandidateBarOrientation != "horizontal" && main.Appearance.CandidateBarOrientation != "vertical" {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "appearance.candidate_bar_orientation", Severity: SeverityError, Message: "unsupported candidate bar orientation"})
	}
	if main.Appearance.GlassClarity < 0 || main.Appearance.GlassClarity > 1 || math.IsNaN(main.Appearance.GlassClarity) || math.IsInf(main.Appearance.GlassClarity, 0) {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "appearance.glass_clarity", Severity: SeverityError, Message: "glass_clarity must be within 0..1"})
	}
	// stage_intensity only scales visuals, so an out-of-range value is a
	// warning (like an unknown animations mode), not a load-blocking error.
	if clamped := NormalizeStageIntensity(main.Appearance.StageIntensity); clamped != main.Appearance.StageIntensity {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "appearance.stage_intensity", Severity: SeverityWarning,
			Message: fmt.Sprintf("stage_intensity %v is outside 0..1; clamped to %v", main.Appearance.StageIntensity, clamped),
		})
	}
	if main.InputEngine.Mode != "inline_hook" && main.InputEngine.Mode != "off" {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "input_engine.mode", Severity: SeverityError, Message: "unsupported input engine mode"})
	} else if main.InputEngine.legacyModeMigrated {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "input_engine.mode", Severity: SeverityWarning,
			Message: "input_engine.mode used legacy native_imk/dual; migrated to inline_hook",
		})
	}
	if strings.TrimSpace(main.InputEngine.IMKSocketPath) != "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "input_engine.imk_socket_path", Severity: SeverityWarning,
			Message: "input_engine.imk_socket_path is not wired; only inline_hook is effective",
		})
	}
	if main.InputEngine.Scheme != "" && main.InputEngine.Scheme != "quanpin" && main.InputEngine.Scheme != "xiaohe" && main.InputEngine.Scheme != "ziranma" && main.InputEngine.Scheme != "microsoft" && main.InputEngine.Scheme != "sogou" && main.InputEngine.Scheme != "pinyin_plus" {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "input_engine.scheme", Severity: SeverityError, Message: "unsupported input engine scheme"})
	}
	if main.InputEngine.CandidateCount < 3 || main.InputEngine.CandidateCount > 9 {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "input_engine.candidate_count", Severity: SeverityError, Message: "candidate_count must be within 3..9"})
	}
	if main.SelectionBar.TriggerMode != "" && main.SelectionBar.TriggerMode != "auto_select" && main.SelectionBar.TriggerMode != "double_shift" && main.SelectionBar.TriggerMode != "disabled" {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "selection_bar.trigger_mode", Severity: SeverityError, Message: "unsupported selection trigger mode"})
	}
	if main.Daemon.IdleCompactSeconds < 10 || main.Daemon.IdleCompactSeconds > 3600 {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "daemon.idle_compact_seconds", Severity: SeverityError, Message: "idle_compact_seconds must be within 10..3600"})
	}
	if main.Daemon.MaxMemoryMB < 24 || main.Daemon.MaxMemoryMB > 512 {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "daemon.max_memory_mb", Severity: SeverityError, Message: "max_memory_mb must be within 24..512"})
	}
	if main.Daemon.WatchdogIntervalMs < 500 || main.Daemon.WatchdogIntervalMs > 60000 {
		issues = append(issues, ValidationIssue{File: file, KeyPath: "daemon.watchdog_interval_ms", Severity: SeverityError, Message: "watchdog_interval_ms must be within 500..60000"})
	}

	if main.IME.Enabled && strings.TrimSpace(main.IME.DefaultInputSourceID) == "" {
		issues = append(issues, ValidationIssue{
			File: file, KeyPath: "ime.default_input_source_id", Severity: SeverityError,
			Message: i18n.T("diag.ime.default_input_source.required"),
		})
	}

	return issues
}

// ValidateLeader validates LeaderConfig (leader.toml).
// Pass nil for knownSnippetIDs or knownWorkflowIDs to skip cross-file existence warnings.
func ValidateLeader(leader LeaderConfig, knownSnippetIDs map[string]struct{}, workflowIDs ...map[string]struct{}) []ValidationIssue {
	knownWorkflowIDs := make(map[string]struct{})
	if len(workflowIDs) > 0 {
		knownWorkflowIDs = workflowIDs[0]
	} else {
		knownWorkflowIDs = nil
	}
	var issues []ValidationIssue
	validateLeaderLevel(leader.Nodes, "nodes", knownSnippetIDs, knownWorkflowIDs, &issues)
	return issues
}

func validateLeaderLevel(nodes []LeaderNode, basePath string, knownSnippetIDs, knownWorkflowIDs map[string]struct{}, issues *[]ValidationIssue) {
	file := string(FileLeader)
	seenKeys := make(map[string]int, len(nodes))

	for i, node := range nodes {
		nodePath := fmt.Sprintf("%s[%d]", basePath, i)
		trimmedKey := strings.TrimSpace(node.Key)
		if trimmedKey == "" {
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".key", Severity: SeverityError,
				Message: i18n.T("diag.leader.node.key.empty"),
			})
		} else if firstIdx, dup := seenKeys[node.Key]; dup {
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".key", Severity: SeverityError,
				Message: i18n.Tf("diag.leader.key.duplicate", node.Key, firstIdx),
			})
		} else {
			seenKeys[node.Key] = i
		}

		if strings.TrimSpace(node.Label) == "" {
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".label", Severity: SeverityError,
				Message: i18n.T("diag.leader.node.label.empty"),
			})
		}

		switch node.Action.Type {
		case ActionGroup:
			if len(node.Action.Children) == 0 {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".children", Severity: SeverityWarning,
					Message: i18n.Tf("diag.leader.group.empty", node.Label),
				})
			} else {
				validateLeaderLevel(node.Action.Children, nodePath+".children", knownSnippetIDs, knownWorkflowIDs, issues)
			}
		case ActionAIRewrite:
			if strings.TrimSpace(node.Action.Mode) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".mode", Severity: SeverityError,
					Message: i18n.T("diag.leader.ai_rewrite.mode.empty"),
				})
			}
			if strings.TrimSpace(node.Action.Prompt) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".prompt", Severity: SeverityError,
					Message: i18n.T("diag.leader.ai_rewrite.prompt.empty"),
				})
			}
		case ActionCLI:
			if strings.TrimSpace(node.Action.Command) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".command", Severity: SeverityError,
					Message: i18n.T("diag.leader.cli.command.empty"),
				})
			}
		case ActionOpenApp:
			if strings.TrimSpace(node.Action.Target) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".target", Severity: SeverityError,
					Message: i18n.T("diag.leader.open_app.target.empty"),
				})
			}
		case ActionSnippet:
			if strings.TrimSpace(node.Action.SnippetID) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".snippet_id", Severity: SeverityError,
					Message: i18n.T("diag.leader.snippet.id.empty"),
				})
			} else if knownSnippetIDs != nil {
				if _, ok := knownSnippetIDs[node.Action.SnippetID]; !ok {
					*issues = append(*issues, ValidationIssue{
						File: file, KeyPath: nodePath + ".snippet_id", Severity: SeverityWarning,
						Message: i18n.Tf("diag.leader.snippet.missing", node.Action.SnippetID),
					})
				}
			}
		case ActionWindow:
			if !isValidWindowCommand(node.Action.WindowCommand) {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".command", Severity: SeverityError,
					Message: i18n.Tf("diag.leader.window.unknown", node.Action.WindowCommand),
				})
			}
		case ActionSwitchTab:
			if !isValidCapsuleTab(node.Action.Tab) {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".tab", Severity: SeverityError,
					Message: i18n.Tf("diag.leader.tab.unknown", node.Action.Tab),
				})
			}
		case ActionRunWorkflow:
			if strings.TrimSpace(node.Action.WorkflowID) == "" {
				*issues = append(*issues, ValidationIssue{
					File: file, KeyPath: nodePath + ".workflow_id", Severity: SeverityError,
					Message: i18n.T("diag.leader.workflow.id.empty"),
				})
			} else if knownWorkflowIDs != nil {
				if _, ok := knownWorkflowIDs[node.Action.WorkflowID]; !ok {
					*issues = append(*issues, ValidationIssue{
						File: file, KeyPath: nodePath + ".workflow_id", Severity: SeverityWarning,
						Message: i18n.Tf("diag.leader.workflow.missing", node.Action.WorkflowID),
					})
				}
			}
		case ActionToggleIME, ActionNormalizeGlossary:
			// valid without extra fields
		default:
			*issues = append(*issues, ValidationIssue{
				File: file, KeyPath: nodePath + ".action", Severity: SeverityError,
				Message: i18n.Tf("diag.leader.action.unknown", node.Action.Type),
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
				Message: i18n.T("diag.snippet.id.empty"),
			})
		} else if _, dup := seenIDs[item.ID]; dup {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".id", Severity: SeverityError,
				Message: i18n.Tf("diag.snippet.id.duplicate", item.ID),
			})
		} else {
			seenIDs[item.ID] = struct{}{}
		}

		if strings.TrimSpace(item.Trigger) == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".trigger", Severity: SeverityError,
				Message: i18n.T("diag.snippet.trigger.empty"),
			})
		} else if _, dup := seenTriggers[item.Trigger]; dup {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".trigger", Severity: SeverityError,
				Message: i18n.Tf("diag.snippet.trigger.duplicate", item.Trigger),
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
				Message: i18n.T("diag.glossary.term.empty"),
			})
		} else {
			lower := strings.ToLower(trimmedTerm)
			if _, dup := seenTerms[lower]; dup {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: path + ".term", Severity: SeverityWarning,
					Message: i18n.Tf("diag.glossary.term.duplicate", entry.Term),
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
		urlPat := strings.TrimSpace(rule.URLPattern)
		titlePat := strings.TrimSpace(rule.TitlePattern)

		if bundleID == "" && procName == "" {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".bundle_id", Severity: SeverityError,
				Message: i18n.T("diag.ime_rule.target.required"),
			})
		}
		if bundleID != "" {
			key := bundleID + "\x00" + urlPat + "\x00" + titlePat
			if _, dup := seenBundleIDs[key]; dup {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: path + ".bundle_id", Severity: SeverityError,
					Message: i18n.Tf("diag.ime_rule.bundle.duplicate", rule.BundleID),
				})
			} else {
				seenBundleIDs[key] = struct{}{}
			}
		}
		if procName != "" {
			lowerProc := strings.ToLower(procName) + "\x00" + urlPat + "\x00" + titlePat
			if _, dup := seenProcessNames[lowerProc]; dup {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: path + ".process_name", Severity: SeverityError,
					Message: i18n.Tf("diag.ime_rule.process.duplicate", rule.ProcessName),
				})
			} else {
				seenProcessNames[lowerProc] = struct{}{}
			}
		}
		if titlePat != "" {
			if _, err := regexp.Compile(titlePat); err != nil {
				issues = append(issues, ValidationIssue{
					File: file, KeyPath: path + ".title_pattern", Severity: SeverityError,
					Message: i18n.Tf("diag.ime_rule.title_pattern.invalid", rule.TitlePattern, err),
				})
			}
		}
		if rule.PreferredMode != "" && !isValidIMEMode(rule.PreferredMode) {
			allowed := make([]string, len(AllIMEModes()))
			for idx, m := range AllIMEModes() {
				allowed[idx] = string(m)
			}
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".preferred_mode", Severity: SeverityError,
				Message: i18n.Tf("diag.ime_rule.mode.unknown", rule.PreferredMode, strings.Join(allowed, ", ")),
			})
		}
		if strings.TrimSpace(rule.InputSourceID) == "" && (rule.PreferredMode == "" || !isValidIMEMode(rule.PreferredMode)) {
			issues = append(issues, ValidationIssue{
				File: file, KeyPath: path + ".input_source_id", Severity: SeverityError,
				Message: i18n.T("diag.ime_rule.source.required"),
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
