package config

import (
	"fmt"
	"strings"
)

// ClipboardActionKind identifies a clipboard copy or paste capability managed
// by the Clipboard Key Manager.
type ClipboardActionKind string

const (
	ClipboardActionCopyContent   ClipboardActionKind = "copy_content"
	ClipboardActionPaste         ClipboardActionKind = "paste"
	ClipboardActionCopyID        ClipboardActionKind = "copy_id"
	ClipboardActionCopyPath      ClipboardActionKind = "copy_path"
	ClipboardActionCopyMetadata  ClipboardActionKind = "copy_metadata"
	ClipboardActionCopySensitive ClipboardActionKind = "copy_sensitive"
)

// Sensitive copy auto-clear TTL bounds (in seconds). Sensitive copies always
// carry a finite auto-clear deadline and never persist indefinitely.
const (
	DefaultSensitiveCopyTTLSeconds = 30
	MinSensitiveCopyTTLSeconds     = 5
	MaxSensitiveCopyTTLSeconds     = 300
)

// ClipboardActionBinding maps one clipboard capability to its chord and
// exposure policy.
type ClipboardActionBinding struct {
	Action     ClipboardActionKind `json:"action"`
	Shortcut   string              `json:"shortcut"`
	UserOnly   bool                `json:"userOnly"`
	AgentAllow bool                `json:"agentAllow"`
}

// ClipboardKeyManagerPolicy holds runtime policy for clipboard shortcut
// bindings, sensitive copy TTL, and history isolation.
type ClipboardKeyManagerPolicy struct {
	SensitiveTTLSeconds    int                      `json:"sensitiveTtlSeconds"`
	ExcludePasswordHistory bool                     `json:"excludePasswordHistory"`
	Bindings               []ClipboardActionBinding `json:"bindings"`
}

// DefaultClipboardKeyManagerPolicy returns the conservative default policy:
// finite 30s TTL for sensitive copies, password/secret history exclusion on,
// and sensitive copy strictly user-only (never exposed to Agent tools).
func DefaultClipboardKeyManagerPolicy() ClipboardKeyManagerPolicy {
	return ClipboardKeyManagerPolicy{
		SensitiveTTLSeconds:    DefaultSensitiveCopyTTLSeconds,
		ExcludePasswordHistory: true,
		Bindings: []ClipboardActionBinding{
			{Action: ClipboardActionCopyContent, Shortcut: "cmd+c", UserOnly: false, AgentAllow: false},
			{Action: ClipboardActionPaste, Shortcut: "enter", UserOnly: true, AgentAllow: false},
			{Action: ClipboardActionCopyID, Shortcut: "cmd+shift+i", UserOnly: false, AgentAllow: false},
			{Action: ClipboardActionCopyPath, Shortcut: "cmd+shift+p", UserOnly: false, AgentAllow: false},
			{Action: ClipboardActionCopyMetadata, Shortcut: "cmd+shift+m", UserOnly: false, AgentAllow: false},
			{Action: ClipboardActionCopySensitive, Shortcut: "cmd+alt+shift+c", UserOnly: true, AgentAllow: false},
		},
	}
}

// NormalizeSensitiveTTLSeconds clamps a sensitive copy auto-clear TTL to safe
// finite bounds. Zero or negative values fall back to DefaultSensitiveCopyTTLSeconds.
func NormalizeSensitiveTTLSeconds(sec int) int {
	if sec <= 0 {
		return DefaultSensitiveCopyTTLSeconds
	}
	if sec < MinSensitiveCopyTTLSeconds {
		return MinSensitiveCopyTTLSeconds
	}
	if sec > MaxSensitiveCopyTTLSeconds {
		return MaxSensitiveCopyTTLSeconds
	}
	return sec
}

// ClipboardShortcutConflict describes a shortcut collision involving the global
// clipboard hotkey or a clipboard action shortcut.
type ClipboardShortcutConflict struct {
	Source   string             `json:"source"`
	Target   string             `json:"target"`
	Chord    string             `json:"chord"`
	Severity ValidationSeverity `json:"severity"`
	Message  string             `json:"message"`
}

// canonicalModifierOrder pins deterministic ordering for shortcut comparison.
var canonicalModifierOrder = []string{"ctrl", "alt", "shift", "cmd", "fn"}

// reservedSystemClipboardChords lists OS-level shortcuts that must not be
// claimed as a global clipboard history trigger.
var reservedSystemClipboardChords = map[string]string{
	"cmd+c":   "OS Copy",
	"cmd+v":   "OS Paste",
	"cmd+x":   "OS Cut",
	"cmd+q":   "OS Quit",
	"cmd+w":   "OS Close Window",
	"cmd+tab": "OS App Switcher",
	"ctrl+c":  "OS/Terminal Copy or Interrupt",
	"ctrl+v":  "OS Paste",
	"alt+tab": "OS Window Switcher",
}

// NormalizeClipboardShortcut canonicalizes modifier aliases and ordering so
// equivalent chords ("Option+Shift+C" and "shift+alt+c") compare equal.
// Empty input returns "" (explicitly disabled).
func NormalizeClipboardShortcut(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "+")
	mods := map[string]bool{}
	key := ""
	for _, part := range parts {
		tok := strings.ToLower(strings.TrimSpace(part))
		if tok == "" {
			return strings.ToLower(trimmed)
		}
		switch tok {
		case "control", "ctrl":
			mods["ctrl"] = true
		case "option", "opt", "alt":
			mods["alt"] = true
		case "shift":
			mods["shift"] = true
		case "command", "cmd", "super", "win", "windows", "meta":
			mods["cmd"] = true
		case "fn", "function":
			mods["fn"] = true
		case "return":
			key = "enter"
		case "esc":
			key = "escape"
		default:
			if key != "" {
				// Multiple non-modifier keys: leave raw normalized so validation rejects it.
				return strings.ToLower(trimmed)
			}
			key = tok
		}
	}
	if key == "" {
		return strings.ToLower(trimmed)
	}
	out := make([]string, 0, len(mods)+1)
	for _, m := range canonicalModifierOrder {
		if mods[m] {
			out = append(out, m)
		}
	}
	out = append(out, key)
	return strings.Join(out, "+")
}

// ValidateClipboardShortcut checks whether raw is empty (disabled) or a valid
// modifier+key shortcut chord suitable for the global clipboard trigger.
func ValidateClipboardShortcut(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if !isValidCapsuleShortcut(trimmed) {
		return fmt.Errorf("invalid clipboard shortcut %q", raw)
	}
	norm := NormalizeClipboardShortcut(trimmed)
	if !strings.Contains(norm, "+") {
		return fmt.Errorf("global clipboard shortcut %q must include at least one modifier", raw)
	}
	return nil
}

// DetectClipboardShortcutConflicts inspects main.Trigger.ClipboardShortcut and
// optional action bindings for collisions with Capsule triggers, keymap
// bindings, OS-reserved global chords, and duplicate action bindings.
func DetectClipboardShortcutConflicts(main MainConfig, bindings []ClipboardActionBinding) []ClipboardShortcutConflict {
	var conflicts []ClipboardShortcutConflict

	clipRaw := strings.TrimSpace(main.Trigger.ClipboardShortcut)
	clipChord := NormalizeClipboardShortcut(clipRaw)

	if clipChord != "" {
		if err := ValidateClipboardShortcut(clipRaw); err != nil {
			conflicts = append(conflicts, ClipboardShortcutConflict{
				Source:   "trigger.clipboard_shortcut",
				Target:   "syntax",
				Chord:    clipChord,
				Severity: SeverityError,
				Message:  err.Error(),
			})
		}

		if capChord := NormalizeClipboardShortcut(main.Trigger.CapsuleShortcut); capChord != "" && capChord == clipChord {
			conflicts = append(conflicts, ClipboardShortcutConflict{
				Source:   "trigger.clipboard_shortcut",
				Target:   "trigger.capsule_shortcut",
				Chord:    clipChord,
				Severity: SeverityError,
				Message:  fmt.Sprintf("clipboard shortcut %q conflicts with trigger.capsule_shortcut", clipChord),
			})
		}

		if imeChord := NormalizeClipboardShortcut(main.Trigger.InputEngineToggleShortcut); imeChord != "" && imeChord == clipChord {
			conflicts = append(conflicts, ClipboardShortcutConflict{
				Source:   "trigger.clipboard_shortcut",
				Target:   "trigger.input_engine_toggle_shortcut",
				Chord:    clipChord,
				Severity: SeverityError,
				Message:  fmt.Sprintf("clipboard shortcut %q conflicts with trigger.input_engine_toggle_shortcut", clipChord),
			})
		}

		if reservedName, reserved := reservedSystemClipboardChords[clipChord]; reserved {
			conflicts = append(conflicts, ClipboardShortcutConflict{
				Source:   "trigger.clipboard_shortcut",
				Target:   "system:" + reservedName,
				Chord:    clipChord,
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("clipboard shortcut %q overlaps reserved system shortcut (%s)", clipChord, reservedName),
			})
		}

		for i, kb := range main.Keymap.Bindings {
			for _, rawKey := range kb.Keys {
				if NormalizeClipboardShortcut(rawKey) == clipChord {
					target := fmt.Sprintf("keymap.bindings[%d]", i)
					if strings.TrimSpace(kb.Action) != "" {
						target += " (" + strings.TrimSpace(kb.Action) + ")"
					}
					conflicts = append(conflicts, ClipboardShortcutConflict{
						Source:   "trigger.clipboard_shortcut",
						Target:   target,
						Chord:    clipChord,
						Severity: SeverityError,
						Message:  fmt.Sprintf("clipboard shortcut %q conflicts with %s", clipChord, target),
					})
				}
			}
		}
	}

	// Check duplicate chords inside clipboard action bindings and sensitive-action agent exposure.
	seenActionChord := make(map[string]ClipboardActionKind, len(bindings))
	for _, b := range bindings {
		if b.Action == ClipboardActionCopySensitive && (b.AgentAllow || !b.UserOnly) {
			conflicts = append(conflicts, ClipboardShortcutConflict{
				Source:   "clipboard.actions." + string(b.Action),
				Target:   "policy.user_only",
				Chord:    NormalizeClipboardShortcut(b.Shortcut),
				Severity: SeverityError,
				Message:  "sensitive clipboard copy must remain user-only and cannot be exposed to agent tools",
			})
		}
		chord := NormalizeClipboardShortcut(b.Shortcut)
		if chord == "" {
			continue
		}
		if prev, dup := seenActionChord[chord]; dup {
			conflicts = append(conflicts, ClipboardShortcutConflict{
				Source:   "clipboard.actions." + string(b.Action),
				Target:   "clipboard.actions." + string(prev),
				Chord:    chord,
				Severity: SeverityError,
				Message:  fmt.Sprintf("clipboard action %q shares shortcut %q with %q", b.Action, chord, prev),
			})
			continue
		}
		seenActionChord[chord] = b.Action
	}

	return conflicts
}

// ValidateClipboardShortcutConflicts converts detected shortcut conflicts into
// standard ValidationIssue records for config diagnostics.
func ValidateClipboardShortcutConflicts(main MainConfig, bindings []ClipboardActionBinding) []ValidationIssue {
	conflicts := DetectClipboardShortcutConflicts(main, bindings)
	if len(conflicts) == 0 {
		return nil
	}
	issues := make([]ValidationIssue, 0, len(conflicts))
	for _, c := range conflicts {
		issues = append(issues, ValidationIssue{
			File:     string(FileMain),
			KeyPath:  c.Source,
			Severity: c.Severity,
			Message:  c.Message,
		})
	}
	return issues
}
