package config

import (
	"os"
	"path/filepath"
)

// DefaultConfigGenerator creates default commented TOML config files if missing.
type DefaultConfigGenerator struct{}

// GenerateIfMissing creates directory if needed and writes any missing config files idempotently.
// Returns the list of file paths created.
func (DefaultConfigGenerator) GenerateIfMissing(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var created []string
	for _, kind := range AllConfigFileKinds() {
		filePath := filepath.Join(dir, string(kind))
		if _, err := os.Stat(filePath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return created, err
		}
		content := DefaultContent(kind)
		if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
			return created, err
		}
		created = append(created, filePath)
	}
	return created, nil
}

// GenerateIfMissing is a package-level helper for DefaultConfigGenerator{}.GenerateIfMissing(dir).
func GenerateIfMissing(dir string) ([]string, error) {
	return DefaultConfigGenerator{}.GenerateIfMissing(dir)
}

// DefaultContent returns the default commented TOML content for a given ConfigFileKind.
func DefaultContent(kind ConfigFileKind) string {
	switch kind {
	case FileMain:
		return defaultMainTOML
	case FileLeader:
		return defaultLeaderTOML
	case FileSnippets:
		return defaultSnippetsTOML
	case FileGlossary:
		return defaultGlossaryTOML
	case FileIMERules:
		return defaultIMERulesTOML
	case FilePrompts:
		return DefaultPromptsConfig().Encode()
	case FileComponents:
		return DefaultComponentsConfig().Encode()
	default:
		return ""
	}
}

const defaultMainTOML = `# Clavis main configuration

[trigger]
# Primary leader key trigger (e.g. "right_option", "right_alt", "right_command", "right_win", "caps_lock")
leader_key = "right_option"
# Key used for double-tap quick activation
double_tap_key = "right_command"
# Allowed range: 100..800 ms
double_tap_interval_ms = 300
# Shortcut to toggle the Capsule launcher
capsule_shortcut = "option+space"
snippet_expansion_enabled = true

[keymap]
# Dual-role Tap/Hold key remapping & CapsLock Vim navigation layer
enabled = true
# Allowed range: 80..500 ms
tap_hold_threshold_ms = 200
caps_lock_tap = "escape"
caps_lock_hold = "nav"
nav_layer_enabled = true

[appearance]
# Theme: "system", "light", or "dark"
theme = "system"
# Language: "auto" (follow the system UI language), "zh-CN", or "en"
language = "auto"
capsule_width = 680.0
max_visible_results = 8
corner_radius = 16.0
show_key_hints = true
show_preview_pane = true
hud_duration_ms = 1400
visual_preset = "spatial_3d"
enable_3d_effects = true
enable_shaders = true
glass_clarity = 0.18
show_bento_shelf = true
candidate_bar_orientation = "horizontal"

[ai]
# API keys are never stored in config.toml; store them in OS Keychain / Credential Manager under keychain_account
active_profile_id = "deepseek"
provider = "deepseek"
protocol = "openai_compatible"
base_url = "https://api.deepseek.com/v1"
model = "deepseek-chat"
temperature = 0.2
timeout_seconds = 30.0
system_prompt = "You are a concise macOS writing and productivity assistant. Return only the requested output unless asked otherwise."
keychain_account = "clavis.ai.deepseek"
api_key_env_var = "DEEPSEEK_API_KEY"

[[ai.profiles]]
id = "deepseek"
name = "DeepSeek"
provider = "deepseek"
protocol = "openai_compatible"
base_url = "https://api.deepseek.com/v1"
model = "deepseek-chat"
temperature = 0.2
timeout_seconds = 30.0
system_prompt = "You are a concise macOS writing and productivity assistant. Return only the requested output unless asked otherwise."
keychain_account = "clavis.ai.deepseek"
api_key_env_var = "DEEPSEEK_API_KEY"

[[ai.profiles]]
id = "ollama"
name = "Ollama (Local)"
provider = "ollama"
protocol = "openai_compatible"
base_url = "http://127.0.0.1:11434/v1"
model = "qwen2.5:7b"
temperature = 0.2
timeout_seconds = 30.0
system_prompt = "You are a concise macOS writing and productivity assistant. Return only the requested output unless asked otherwise."
keychain_account = "clavis.ai.ollama"
api_key_env_var = ""

[[ai.profiles]]
id = "claude"
name = "Claude"
provider = "anthropic"
protocol = "anthropic"
base_url = "https://api.anthropic.com/v1"
model = "claude-3-7-sonnet-latest"
temperature = 0.2
timeout_seconds = 30.0
system_prompt = "You are a concise macOS writing and productivity assistant. Return only the requested output unless asked otherwise."
keychain_account = "clavis.ai.anthropic"
api_key_env_var = "ANTHROPIC_API_KEY"

[[ai.profiles]]
id = "gemini"
name = "Gemini"
provider = "gemini"
protocol = "gemini"
base_url = "https://generativelanguage.googleapis.com/v1beta"
model = "gemini-2.5-flash"
temperature = 0.2
timeout_seconds = 30.0
system_prompt = "You are a concise macOS writing and productivity assistant. Return only the requested output unless asked otherwise."
keychain_account = "clavis.ai.gemini"
api_key_env_var = "GEMINI_API_KEY"

[clipboard]
enabled = true
max_history_items = 200
retention_days = 30
ignore_concealed_types = true
excluded_bundle_ids = ["com.1password.1password", "com.apple.keychainaccess"]

[ime]
enabled = true
default_input_source_id = "com.apple.keylayout.ABC"
show_indicator_hud = true
switch_delay_ms = 50
lock_ascii_on_leader = true
show_floating_pill = true

[input_engine]
enabled = false
mode = "inline_hook"
scheme = "quanpin"
candidate_count = 7
mixed_english = true
auto_pair_punctuation = true
shift_toggle_ascii = true
fuzzy_pinyin = []
custom_dict_paths = []
user_learning = true
imk_socket_path = ""

[selection_bar]
enabled = true
trigger_mode = "double_shift"
actions = ["optimize_prompt", "explain", "translate", "polish", "copy"]

[daemon]
idle_compact_seconds = 60
max_memory_mb = 64
watchdog_interval_ms = 5000
in_place_optimize_shortcut = "option+tab"
`

const defaultLeaderTOML = `# Clavis leader key tree
# Actions: "group", "ai_rewrite", "window", "cli", "open_app", "switch_tab", "snippet", "toggle_ime", "normalize_glossary"

[[nodes]]
key = "w"
label = "Window"
icon = "macwindow"
action = "group"

[[nodes.children]]
key = "h"
label = "Left Half"
icon = "rectangle.lefthalf.filled"
action = "window"
command = "left_half"

[[nodes.children]]
key = "l"
label = "Right Half"
icon = "rectangle.righthalf.filled"
action = "window"
command = "right_half"

[[nodes.children]]
key = "m"
label = "Maximize"
icon = "arrow.up.left.and.arrow.down.right"
action = "window"
command = "maximize"

[[nodes.children]]
key = "c"
label = "Center"
icon = "rectangle.center.inset.filled"
action = "window"
command = "center"

[[nodes]]
key = "a"
label = "AI Rewrite"
icon = "sparkles"
action = "group"

[[nodes.children]]
key = "p"
label = "Polish"
icon = "wand.and.stars"
action = "ai_rewrite"
mode = "polish"
prompt = "Improve clarity, grammar, and flow while preserving the original meaning and tone."

[[nodes.children]]
key = "t"
label = "Translate EN/ZH"
icon = "globe"
action = "ai_rewrite"
mode = "translate"
prompt = "Translate Chinese to natural English or English to natural Chinese."

[[nodes]]
key = "c"
label = "Clipboard"
icon = "doc.on.clipboard"
action = "switch_tab"
tab = "clipboard"

[[nodes]]
key = "i"
label = "Toggle IME"
icon = "keyboard"
action = "toggle_ime"

[[nodes]]
key = "t"
label = "Terminal"
icon = "terminal"
action = "open_app"
target = "com.apple.Terminal"
`

const defaultSnippetsTOML = `# Clavis text expansion snippets

[[snippets]]
id = "email-signoff"
trigger = ";sig"
content = "Best regards,"
description = "Standard email sign-off"
word_boundary = true
app_bundle_ids = []

[[snippets]]
id = "shrug"
trigger = ";shrug"
content = "¯\\_(ツ)_/¯"
description = "Shrug emoticon"
word_boundary = true
app_bundle_ids = []
`

const defaultGlossaryTOML = `# Clavis domain glossary for AI rewriting and terminology consistency

[[terms]]
term = "Clavis"
aliases = ["clavis"]
hint = "Keep capitalized as the product name."
category = "product"
`

const defaultIMERulesTOML = `# Clavis per-application input method rules
# preferred_mode: "ascii", "native", or "keep"
# Optional context matchers: url_pattern = "*github.com*", title_pattern = ".*VIM.*"

[[rules]]
bundle_id = "com.apple.Terminal"
app_name = "Terminal"
input_source_id = "com.apple.keylayout.ABC"
preferred_mode = "ascii"
restore_on_leave = true

[[rules]]
bundle_id = "com.googlecode.iterm2"
app_name = "iTerm2"
input_source_id = "com.apple.keylayout.ABC"
preferred_mode = "ascii"
restore_on_leave = true
`
