package config

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/toml"
)

// TriggerConfig configures global hotkeys and snippet expansion triggers.
type TriggerConfig struct {
	LeaderKey           string `json:"leaderKey"`
	DoubleTapKey        string `json:"doubleTapKey"`
	DoubleTapIntervalMs int    `json:"doubleTapIntervalMs"`
	CapsuleShortcut     string `json:"capsuleShortcut"`
	ClipboardShortcut   string `json:"clipboardShortcut"`
	// InputEngineToggleShortcut flips the own input engine between forced Chinese
	// takeover and auto mode. Empty disables the hotkey.
	InputEngineToggleShortcut string `json:"inputEngineToggleShortcut"`
	SnippetExpansionEnabled   bool   `json:"snippetExpansionEnabled"`
	// SnippetBackspaceUndo reverts an expansion when Backspace is the very
	// next key, restoring the typed trigger (as espanso's undo_backspace).
	SnippetBackspaceUndo bool `json:"snippetBackspaceUndo"`
	// LeaderRevealDelayMs keeps the Leader menu invisible (but listening)
	// this long after activation: a fast key sequence runs without the menu
	// ever flashing, a pause reveals it (which-key style). 0 shows at once.
	LeaderRevealDelayMs int `json:"leaderRevealDelayMs"`
}

func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		LeaderKey:           "right_option",
		DoubleTapKey:        "right_command",
		DoubleTapIntervalMs: 300,
		CapsuleShortcut:     "option+space",
		ClipboardShortcut:   "alt+shift+c",
		// Empty by default: the input-mode toggle now has dedicated gestures (double-tap
		// right shift, or the candidate pill), so it no longer takes a chord away from the
		// user's shortcut budget. Set a value here to opt back in to a hotkey.
		InputEngineToggleShortcut: "",
		SnippetExpansionEnabled:   true,
		SnippetBackspaceUndo:      true,
		LeaderRevealDelayMs:       DefaultLeaderRevealDelayMs,
	}
}

// Leader reveal delay bounds: the default suits fast typists without making
// newcomers wait; beyond a second the menu would feel broken.
const (
	DefaultLeaderRevealDelayMs = 220
	MaxLeaderRevealDelayMs     = 1000
)

func decodeTriggerConfig(tbl *toml.Table, path string) (TriggerConfig, error) {
	cfg := DefaultTriggerConfig()
	if tbl == nil {
		return cfg, nil
	}
	if s, ok, err := optionalString(tbl, "leader_key", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.LeaderKey = s
	}
	if s, ok, err := optionalString(tbl, "double_tap_key", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.DoubleTapKey = s
	}
	if n, ok, err := optionalInt(tbl, "double_tap_interval_ms", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.DoubleTapIntervalMs = n
	}
	if s, ok, err := optionalString(tbl, "capsule_shortcut", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.CapsuleShortcut = s
	}
	if s, ok, err := optionalString(tbl, "clipboard_shortcut", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ClipboardShortcut = s
	}
	if s, ok, err := optionalString(tbl, "input_engine_toggle_shortcut", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.InputEngineToggleShortcut = s
	}
	if b, ok, err := optionalBool(tbl, "snippet_expansion_enabled", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.SnippetExpansionEnabled = b
	}
	if b, ok, err := optionalBool(tbl, "snippet_backspace_undo", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.SnippetBackspaceUndo = b
	}
	if n, ok, err := optionalInt(tbl, "leader_reveal_delay_ms", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.LeaderRevealDelayMs = n
	}
	return cfg, nil
}

func (c TriggerConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("leader_key", toml.StringVal(c.LeaderKey))
	t.Set("double_tap_key", toml.StringVal(c.DoubleTapKey))
	t.Set("double_tap_interval_ms", toml.IntVal(int64(c.DoubleTapIntervalMs)))
	t.Set("capsule_shortcut", toml.StringVal(c.CapsuleShortcut))
	t.Set("clipboard_shortcut", toml.StringVal(c.ClipboardShortcut))
	if c.InputEngineToggleShortcut != "" {
		t.Set("input_engine_toggle_shortcut", toml.StringVal(c.InputEngineToggleShortcut))
	}
	t.Set("snippet_expansion_enabled", toml.BoolVal(c.SnippetExpansionEnabled))
	t.Set("snippet_backspace_undo", toml.BoolVal(c.SnippetBackspaceUndo))
	t.Set("leader_reveal_delay_ms", toml.IntVal(int64(c.LeaderRevealDelayMs)))
	return t
}

// AppearanceConfig configures Capsule UI visuals and timings.
type AppearanceConfig struct {
	Theme                   string  `json:"theme"`
	Language                string  `json:"language"`
	Animations              string  `json:"animations"`
	CapsuleWidth            float64 `json:"capsuleWidth"`
	MaxVisibleResults       int     `json:"maxVisibleResults"`
	CornerRadius            float64 `json:"cornerRadius"`
	ShowKeyHints            bool    `json:"showKeyHints"`
	ShowPreviewPane         bool    `json:"showPreviewPane"`
	HUDDurationMs           int     `json:"hudDurationMs"`
	VisualPreset            string  `json:"visualPreset"`
	Enable3DEffects         bool    `json:"enable3DEffects"`
	EnableShaders           bool    `json:"enableShaders"`
	GlassClarity            float64 `json:"glassClarity"`
	CandidateBarOrientation string  `json:"candidateBarOrientation"`
	// StageIntensity scales the stage's yaw, pitch, row skew, parallax and
	// specular from 0 (flat geometry, same materials) to 1 (full spatial look).
	StageIntensity float64 `json:"stageIntensity"`
	// ObliqueRows turns the per-row fan of the result list on or off.
	ObliqueRows bool `json:"obliqueRows"`
}

func DefaultAppearanceConfig() AppearanceConfig {
	return AppearanceConfig{
		Theme:                   "system",
		Language:                "auto",
		Animations:              "auto",
		CapsuleWidth:            680.0,
		MaxVisibleResults:       8,
		CornerRadius:            16.0,
		ShowKeyHints:            true,
		ShowPreviewPane:         true,
		HUDDurationMs:           1400,
		VisualPreset:            "spatial_3d",
		Enable3DEffects:         true,
		EnableShaders:           true,
		GlassClarity:            0.18,
		CandidateBarOrientation: "horizontal",
		StageIntensity:          0.7,
		ObliqueRows:             true,
	}
}

func decodeAppearanceConfig(tbl *toml.Table, path string) (AppearanceConfig, error) {
	cfg := DefaultAppearanceConfig()
	if tbl == nil {
		return cfg, nil
	}
	if s, ok, err := optionalString(tbl, "theme", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Theme = s
	}
	if s, ok, err := optionalString(tbl, "language", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Language = s
	}
	if s, ok, err := optionalString(tbl, "animations", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Animations = s
	}
	if f, ok, err := optionalFloat(tbl, "capsule_width", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.CapsuleWidth = f
	}
	if n, ok, err := optionalInt(tbl, "max_visible_results", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.MaxVisibleResults = n
	}
	if f, ok, err := optionalFloat(tbl, "corner_radius", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.CornerRadius = f
	}
	if b, ok, err := optionalBool(tbl, "show_key_hints", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ShowKeyHints = b
	}
	if b, ok, err := optionalBool(tbl, "show_preview_pane", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ShowPreviewPane = b
	}
	if n, ok, err := optionalInt(tbl, "hud_duration_ms", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.HUDDurationMs = n
	}
	if s, ok, err := optionalString(tbl, "visual_preset", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.VisualPreset = s
	}
	if b, ok, err := optionalBool(tbl, "enable_3d_effects", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Enable3DEffects = b
	}
	if b, ok, err := optionalBool(tbl, "enable_shaders", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.EnableShaders = b
	}
	if f, ok, err := optionalFloat(tbl, "glass_clarity", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.GlassClarity = f
	}
	if s, ok, err := optionalString(tbl, "candidate_bar_orientation", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.CandidateBarOrientation = s
	}
	if f, ok, err := optionalFloat(tbl, "stage_intensity", path); err != nil {
		return cfg, err
	} else if ok {
		// Keep the raw value: ValidateMain warns about an out-of-range one, and
		// ConfigStore.Reload clamps it into [0,1] after validation. Clamping
		// here would hide the warning from the only path that ever reads it.
		cfg.StageIntensity = f
	}
	if b, ok, err := optionalBool(tbl, "oblique_rows", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ObliqueRows = b
	}
	return cfg, nil
}

func (c AppearanceConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("theme", toml.StringVal(c.Theme))
	t.Set("language", toml.StringVal(c.Language))
	t.Set("animations", toml.StringVal(c.Animations))
	t.Set("capsule_width", toml.FloatVal(c.CapsuleWidth))
	t.Set("max_visible_results", toml.IntVal(int64(c.MaxVisibleResults)))
	t.Set("corner_radius", toml.FloatVal(c.CornerRadius))
	t.Set("show_key_hints", toml.BoolVal(c.ShowKeyHints))
	t.Set("show_preview_pane", toml.BoolVal(c.ShowPreviewPane))
	t.Set("hud_duration_ms", toml.IntVal(int64(c.HUDDurationMs)))
	t.Set("visual_preset", toml.StringVal(c.VisualPreset))
	t.Set("enable_3d_effects", toml.BoolVal(c.Enable3DEffects))
	t.Set("enable_shaders", toml.BoolVal(c.EnableShaders))
	t.Set("glass_clarity", toml.FloatVal(c.GlassClarity))
	t.Set("candidate_bar_orientation", toml.StringVal(c.CandidateBarOrientation))
	t.Set("stage_intensity", toml.FloatVal(c.StageIntensity))
	t.Set("oblique_rows", toml.BoolVal(c.ObliqueRows))
	return t
}

// AIProtocol specifies the wire format protocol used by an AI profile.
type AIProtocol string

const (
	ProtocolOpenAICompatible AIProtocol = "openai_compatible"
	ProtocolAnthropic        AIProtocol = "anthropic"
	ProtocolGemini           AIProtocol = "gemini"
)

// ModelCapabilities declares features a provider/model profile may safely use.
// False and zero values are conservative: callers must not assume a capability.
type ModelCapabilities struct {
	StructuredOutput bool  `json:"structuredOutput"`
	ToolCalling      bool  `json:"toolCalling"`
	Vision           bool  `json:"vision"`
	ReasoningEffort  bool  `json:"reasoningEffort"`
	ContextWindow    int64 `json:"contextWindow,omitempty"`
	Streaming        bool  `json:"streaming"`
}

// AllAIProtocols returns all supported AI wire protocols in deterministic order.
func AllAIProtocols() []AIProtocol {
	return []AIProtocol{
		ProtocolOpenAICompatible,
		ProtocolAnthropic,
		ProtocolGemini,
	}
}

func isValidAIProtocol(p AIProtocol) bool {
	for _, item := range AllAIProtocols() {
		if item == p {
			return true
		}
	}
	return false
}

const defaultAISystemPrompt = "You are a concise macOS writing and productivity assistant. Return only the requested output unless asked otherwise."

// AIProfile defines one named AI provider/model profile. ReasoningEffort,
// optional, is sent as "reasoning_effort" by the OpenAI-compatible
// protocol for reasoning models ("low" keeps DeepSeek V4.1 Flash quick);
// empty sends nothing.
type AIProfile struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Provider        string            `json:"provider"`
	Protocol        AIProtocol        `json:"protocol"`
	BaseURL         string            `json:"baseUrl"`
	Model           string            `json:"model"`
	Temperature     float64           `json:"temperature"`
	TimeoutSeconds  float64           `json:"timeoutSeconds"`
	SystemPrompt    string            `json:"systemPrompt"`
	KeychainAccount string            `json:"keychainAccount"`
	APIKeyEnvVar    string            `json:"apiKeyEnvVar,omitempty"`
	ReasoningEffort string            `json:"reasoningEffort,omitempty"`
	Capabilities    ModelCapabilities `json:"capabilities"`
}

// DefaultAIProfiles returns the built-in multi-provider AI profiles (DeepSeek, Ollama, Claude, Gemini).
func DefaultAIProfiles() []AIProfile {
	return []AIProfile{
		{
			ID:              "deepseek",
			Name:            "DeepSeek",
			Provider:        "deepseek",
			Protocol:        ProtocolOpenAICompatible,
			BaseURL:         "https://api.deepseek.com/v1",
			Model:           "deepseek-chat",
			Temperature:     0.2,
			TimeoutSeconds:  30.0,
			SystemPrompt:    defaultAISystemPrompt,
			KeychainAccount: "clavis.ai.deepseek",
			APIKeyEnvVar:    "DEEPSEEK_API_KEY",
		},
		{
			ID:              "ollama",
			Name:            "Ollama (Local)",
			Provider:        "ollama",
			Protocol:        ProtocolOpenAICompatible,
			BaseURL:         "http://127.0.0.1:11434/v1",
			Model:           "qwen2.5:7b",
			Temperature:     0.2,
			TimeoutSeconds:  30.0,
			SystemPrompt:    defaultAISystemPrompt,
			KeychainAccount: "clavis.ai.ollama",
			APIKeyEnvVar:    "",
		},
		{
			ID:              "claude",
			Name:            "Claude",
			Provider:        "anthropic",
			Protocol:        ProtocolAnthropic,
			BaseURL:         "https://api.anthropic.com/v1",
			Model:           "claude-3-7-sonnet-latest",
			Temperature:     0.2,
			TimeoutSeconds:  30.0,
			SystemPrompt:    defaultAISystemPrompt,
			KeychainAccount: "clavis.ai.anthropic",
			APIKeyEnvVar:    "ANTHROPIC_API_KEY",
		},
		{
			ID:              "gemini",
			Name:            "Gemini",
			Provider:        "gemini",
			Protocol:        ProtocolGemini,
			BaseURL:         "https://generativelanguage.googleapis.com/v1beta",
			Model:           "gemini-2.5-flash",
			Temperature:     0.2,
			TimeoutSeconds:  30.0,
			SystemPrompt:    defaultAISystemPrompt,
			KeychainAccount: "clavis.ai.gemini",
			APIKeyEnvVar:    "GEMINI_API_KEY",
		},
	}
}

// AIConfig configures the AI subsystem, supporting both named multi-provider profiles
// and flat fallback fields for backward compatibility.
type AIConfig struct {
	ActiveProfileID string      `json:"activeProfileId"`
	Provider        string      `json:"provider"`
	Protocol        AIProtocol  `json:"protocol"`
	BaseURL         string      `json:"baseUrl"`
	Model           string      `json:"model"`
	Temperature     float64     `json:"temperature"`
	TimeoutSeconds  float64     `json:"timeoutSeconds"`
	SystemPrompt    string      `json:"systemPrompt"`
	KeychainAccount string      `json:"keychainAccount"`
	APIKeyEnvVar    string      `json:"apiKeyEnvVar,omitempty"`
	Profiles        []AIProfile `json:"profiles"`
}

func DefaultAIConfig() AIConfig {
	profiles := DefaultAIProfiles()
	active := profiles[0]
	return AIConfig{
		ActiveProfileID: active.ID,
		Provider:        active.Provider,
		Protocol:        active.Protocol,
		BaseURL:         active.BaseURL,
		Model:           active.Model,
		Temperature:     active.Temperature,
		TimeoutSeconds:  active.TimeoutSeconds,
		SystemPrompt:    active.SystemPrompt,
		KeychainAccount: active.KeychainAccount,
		APIKeyEnvVar:    active.APIKeyEnvVar,
		Profiles:        profiles,
	}
}

// ProfileByID looks up a configured AIProfile by ID.
func (c AIConfig) ProfileByID(id string) (AIProfile, bool) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return AIProfile{}, false
	}
	for _, p := range c.Profiles {
		if p.ID == trimmed {
			return p, true
		}
	}
	for _, p := range c.Profiles {
		if strings.EqualFold(p.ID, trimmed) {
			return p, true
		}
	}
	return AIProfile{}, false
}

// ActiveProfile resolves the currently active AIProfile, falling back to flat AIConfig fields.
func (c AIConfig) ActiveProfile() AIProfile {
	def := DefaultAIProfiles()[0]
	if p, ok := c.ProfileByID(c.ActiveProfileID); ok {
		// If the active profile is the default profile and caller mutated flat fields directly,
		// overlay the customized flat fields onto the returned profile.
		if p.ID == def.ID {
			if c.Provider != "" && c.Provider != def.Provider && p.Provider == def.Provider {
				p.Provider = c.Provider
			}
			if c.Protocol != "" && c.Protocol != def.Protocol && p.Protocol == def.Protocol {
				p.Protocol = c.Protocol
			}
			if c.BaseURL != "" && c.BaseURL != def.BaseURL && p.BaseURL == def.BaseURL {
				p.BaseURL = c.BaseURL
			}
			if c.Model != "" && c.Model != def.Model && p.Model == def.Model {
				p.Model = c.Model
			}
			if c.Temperature != 0 && c.Temperature != def.Temperature && p.Temperature == def.Temperature {
				p.Temperature = c.Temperature
			}
			if c.TimeoutSeconds != 0 && c.TimeoutSeconds != def.TimeoutSeconds && p.TimeoutSeconds == def.TimeoutSeconds {
				p.TimeoutSeconds = c.TimeoutSeconds
			}
			if c.SystemPrompt != "" && c.SystemPrompt != def.SystemPrompt && p.SystemPrompt == def.SystemPrompt {
				p.SystemPrompt = c.SystemPrompt
			}
			if c.KeychainAccount != "" && c.KeychainAccount != def.KeychainAccount && p.KeychainAccount == def.KeychainAccount {
				p.KeychainAccount = c.KeychainAccount
			}
			if c.APIKeyEnvVar != "" && c.APIKeyEnvVar != def.APIKeyEnvVar && p.APIKeyEnvVar == def.APIKeyEnvVar {
				p.APIKeyEnvVar = c.APIKeyEnvVar
			}
		}
		if p.Protocol == "" {
			p.Protocol = ProtocolOpenAICompatible
		}
		return p
	}
	if len(c.Profiles) > 0 && c.ActiveProfileID == "" {
		p := c.Profiles[0]
		if p.Protocol == "" {
			p.Protocol = ProtocolOpenAICompatible
		}
		return p
	}
	proto := c.Protocol
	if proto == "" {
		proto = ProtocolOpenAICompatible
	}
	id := c.ActiveProfileID
	if id == "" {
		id = c.Provider
	}
	if id == "" {
		id = "default"
	}
	return AIProfile{
		ID:              id,
		Name:            c.Provider,
		Provider:        c.Provider,
		Protocol:        proto,
		BaseURL:         c.BaseURL,
		Model:           c.Model,
		Temperature:     c.Temperature,
		TimeoutSeconds:  c.TimeoutSeconds,
		SystemPrompt:    c.SystemPrompt,
		KeychainAccount: c.KeychainAccount,
		APIKeyEnvVar:    c.APIKeyEnvVar,
	}
}

func checkForbiddenAPIKeys(tbl *toml.Table, path string) error {
	for _, forbiddenKey := range []string{"api_key", "apiKey", "secret_key", "secretKey"} {
		if _, exists := tbl.Get(forbiddenKey); exists {
			return &DecodingError{
				KeyPath: joinPath(path, forbiddenKey),
				Message: i18n.T("decode.ai.raw_key_forbidden"),
			}
		}
	}
	return nil
}

func decodeAIProfile(tbl *toml.Table, path string) (AIProfile, error) {
	if err := checkForbiddenAPIKeys(tbl, path); err != nil {
		return AIProfile{}, err
	}
	id, err := requiredString(tbl, "id", path)
	if err != nil {
		return AIProfile{}, err
	}
	name, _, err := optionalString(tbl, "name", path)
	if err != nil {
		return AIProfile{}, err
	}
	provider, _, err := optionalString(tbl, "provider", path)
	if err != nil {
		return AIProfile{}, err
	}
	if provider == "" {
		provider = id
	}
	if name == "" {
		name = provider
	}
	rawProto := string(ProtocolOpenAICompatible)
	if s, ok, err := optionalString(tbl, "protocol", path); err != nil {
		return AIProfile{}, err
	} else if ok && s != "" {
		rawProto = s
	}
	baseURL, err := requiredString(tbl, "base_url", path)
	if err != nil {
		return AIProfile{}, err
	}
	model, err := requiredString(tbl, "model", path)
	if err != nil {
		return AIProfile{}, err
	}
	temp := 0.2
	if f, ok, err := optionalFloat(tbl, "temperature", path); err != nil {
		return AIProfile{}, err
	} else if ok {
		temp = f
	}
	timeout := 30.0
	if f, ok, err := optionalFloat(tbl, "timeout_seconds", path); err != nil {
		return AIProfile{}, err
	} else if ok {
		timeout = f
	}
	sysPrompt := defaultAISystemPrompt
	if s, ok, err := optionalString(tbl, "system_prompt", path); err != nil {
		return AIProfile{}, err
	} else if ok {
		sysPrompt = s
	}
	keychainAcc := "clavis.ai." + id
	if s, ok, err := optionalString(tbl, "keychain_account", path); err != nil {
		return AIProfile{}, err
	} else if ok {
		keychainAcc = s
	}
	envVar, _, err := optionalString(tbl, "api_key_env_var", path)
	if err != nil {
		return AIProfile{}, err
	}
	effort, _, err := optionalString(tbl, "reasoning_effort", path)
	if err != nil {
		return AIProfile{}, err
	}
	capabilities := ModelCapabilities{}
	if caps, ok, err := optionalTable(tbl, "capabilities", path); err != nil {
		return AIProfile{}, err
	} else if ok {
		var e error
		if capabilities.StructuredOutput, _, e = optionalBool(caps, "structured_output", joinPath(path, "capabilities")); e != nil {
			return AIProfile{}, e
		}
		if capabilities.ToolCalling, _, e = optionalBool(caps, "tool_calling", joinPath(path, "capabilities")); e != nil {
			return AIProfile{}, e
		}
		if capabilities.Vision, _, e = optionalBool(caps, "vision", joinPath(path, "capabilities")); e != nil {
			return AIProfile{}, e
		}
		if capabilities.ReasoningEffort, _, e = optionalBool(caps, "reasoning_effort", joinPath(path, "capabilities")); e != nil {
			return AIProfile{}, e
		}
		if capabilities.Streaming, _, e = optionalBool(caps, "streaming", joinPath(path, "capabilities")); e != nil {
			return AIProfile{}, e
		}
		if v, _, e2 := optionalInt(caps, "context_window", joinPath(path, "capabilities")); e2 != nil {
			return AIProfile{}, e2
		} else {
			capabilities.ContextWindow = int64(v)
		}
	}

	return AIProfile{
		ID:              id,
		Name:            name,
		Provider:        provider,
		Protocol:        AIProtocol(rawProto),
		BaseURL:         baseURL,
		Model:           model,
		Temperature:     temp,
		TimeoutSeconds:  timeout,
		SystemPrompt:    sysPrompt,
		KeychainAccount: keychainAcc,
		APIKeyEnvVar:    envVar,
		ReasoningEffort: strings.TrimSpace(effort),
		Capabilities:    capabilities,
	}, nil
}

func (p AIProfile) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(p.ID))
	t.Set("name", toml.StringVal(p.Name))
	t.Set("provider", toml.StringVal(p.Provider))
	proto := p.Protocol
	if proto == "" {
		proto = ProtocolOpenAICompatible
	}
	t.Set("protocol", toml.StringVal(string(proto)))
	t.Set("base_url", toml.StringVal(p.BaseURL))
	t.Set("model", toml.StringVal(p.Model))
	t.Set("temperature", toml.FloatVal(p.Temperature))
	t.Set("timeout_seconds", toml.FloatVal(p.TimeoutSeconds))
	t.Set("system_prompt", toml.StringVal(p.SystemPrompt))
	t.Set("keychain_account", toml.StringVal(p.KeychainAccount))
	t.Set("api_key_env_var", toml.StringVal(p.APIKeyEnvVar))
	if p.ReasoningEffort != "" {
		t.Set("reasoning_effort", toml.StringVal(p.ReasoningEffort))
	}
	if p.Capabilities != (ModelCapabilities{}) {
		caps := toml.NewTable()
		caps.Set("structured_output", toml.BoolVal(p.Capabilities.StructuredOutput))
		caps.Set("tool_calling", toml.BoolVal(p.Capabilities.ToolCalling))
		caps.Set("vision", toml.BoolVal(p.Capabilities.Vision))
		caps.Set("reasoning_effort", toml.BoolVal(p.Capabilities.ReasoningEffort))
		caps.Set("context_window", toml.IntVal(p.Capabilities.ContextWindow))
		caps.Set("streaming", toml.BoolVal(p.Capabilities.Streaming))
		t.Set("capabilities", toml.TableVal(caps))
	}
	return t
}

func decodeAIConfig(tbl *toml.Table, path string) (AIConfig, error) {
	cfg := DefaultAIConfig()
	if tbl == nil {
		return cfg, nil
	}
	if err := checkForbiddenAPIKeys(tbl, path); err != nil {
		return cfg, err
	}

	hasFlatField := false
	hasActiveID := false

	if s, ok, err := optionalString(tbl, "active_profile_id", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ActiveProfileID = s
		hasActiveID = true
	}
	if s, ok, err := optionalString(tbl, "provider", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Provider = s
		hasFlatField = true
	}
	if s, ok, err := optionalString(tbl, "protocol", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Protocol = AIProtocol(s)
		hasFlatField = true
	}
	if s, ok, err := optionalString(tbl, "base_url", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.BaseURL = s
		hasFlatField = true
	}
	if s, ok, err := optionalString(tbl, "model", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Model = s
		hasFlatField = true
	}
	if f, ok, err := optionalFloat(tbl, "temperature", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Temperature = f
		hasFlatField = true
	}
	if f, ok, err := optionalFloat(tbl, "timeout_seconds", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.TimeoutSeconds = f
		hasFlatField = true
	}
	if s, ok, err := optionalString(tbl, "system_prompt", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.SystemPrompt = s
		hasFlatField = true
	}
	if s, ok, err := optionalString(tbl, "keychain_account", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.KeychainAccount = s
		hasFlatField = true
	}
	if s, ok, err := optionalString(tbl, "api_key_env_var", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.APIKeyEnvVar = s
		hasFlatField = true
	}

	profileTables, hasProfiles, err := optionalTableArray(tbl, "profiles", path)
	if err != nil {
		return cfg, err
	}
	if hasProfiles {
		profiles := make([]AIProfile, 0, len(profileTables))
		for i, pt := range profileTables {
			prof, err := decodeAIProfile(pt, fmt.Sprintf("%s.profiles[%d]", path, i))
			if err != nil {
				return cfg, err
			}
			profiles = append(profiles, prof)
		}
		cfg.Profiles = profiles
		if !hasActiveID && len(profiles) > 0 {
			cfg.ActiveProfileID = profiles[0].ID
		}
		if !hasFlatField {
			if active, ok := cfg.ProfileByID(cfg.ActiveProfileID); ok {
				cfg.Provider = active.Provider
				cfg.Protocol = active.Protocol
				cfg.BaseURL = active.BaseURL
				cfg.Model = active.Model
				cfg.Temperature = active.Temperature
				cfg.TimeoutSeconds = active.TimeoutSeconds
				cfg.SystemPrompt = active.SystemPrompt
				cfg.KeychainAccount = active.KeychainAccount
				cfg.APIKeyEnvVar = active.APIKeyEnvVar
			}
		}
	} else if hasFlatField && !hasActiveID {
		// Legacy [ai] table without [[ai.profiles]]: synthesize a single profile if customized.
		def := DefaultAIProfiles()[0]
		isCustom := cfg.Provider != def.Provider ||
			cfg.Protocol != def.Protocol ||
			cfg.BaseURL != def.BaseURL ||
			cfg.Model != def.Model ||
			cfg.Temperature != def.Temperature ||
			cfg.TimeoutSeconds != def.TimeoutSeconds ||
			cfg.SystemPrompt != def.SystemPrompt ||
			cfg.KeychainAccount != def.KeychainAccount ||
			cfg.APIKeyEnvVar != def.APIKeyEnvVar
		if isCustom {
			id := strings.TrimSpace(cfg.Provider)
			if id == "" {
				id = "custom"
			}
			proto := cfg.Protocol
			if proto == "" {
				proto = ProtocolOpenAICompatible
			}
			synthesized := AIProfile{
				ID:              id,
				Name:            id,
				Provider:        cfg.Provider,
				Protocol:        proto,
				BaseURL:         cfg.BaseURL,
				Model:           cfg.Model,
				Temperature:     cfg.Temperature,
				TimeoutSeconds:  cfg.TimeoutSeconds,
				SystemPrompt:    cfg.SystemPrompt,
				KeychainAccount: cfg.KeychainAccount,
				APIKeyEnvVar:    cfg.APIKeyEnvVar,
			}
			cfg.ActiveProfileID = id
			cfg.Profiles = []AIProfile{synthesized}
		}
	} else if hasActiveID && !hasFlatField {
		if active, ok := cfg.ProfileByID(cfg.ActiveProfileID); ok {
			cfg.Provider = active.Provider
			cfg.Protocol = active.Protocol
			cfg.BaseURL = active.BaseURL
			cfg.Model = active.Model
			cfg.Temperature = active.Temperature
			cfg.TimeoutSeconds = active.TimeoutSeconds
			cfg.SystemPrompt = active.SystemPrompt
			cfg.KeychainAccount = active.KeychainAccount
			cfg.APIKeyEnvVar = active.APIKeyEnvVar
		}
	}

	return cfg, nil
}

func (c AIConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("active_profile_id", toml.StringVal(c.ActiveProfileID))
	t.Set("provider", toml.StringVal(c.Provider))
	proto := c.Protocol
	if proto == "" {
		proto = ProtocolOpenAICompatible
	}
	t.Set("protocol", toml.StringVal(string(proto)))
	t.Set("base_url", toml.StringVal(c.BaseURL))
	t.Set("model", toml.StringVal(c.Model))
	t.Set("temperature", toml.FloatVal(c.Temperature))
	t.Set("timeout_seconds", toml.FloatVal(c.TimeoutSeconds))
	t.Set("system_prompt", toml.StringVal(c.SystemPrompt))
	t.Set("keychain_account", toml.StringVal(c.KeychainAccount))
	t.Set("api_key_env_var", toml.StringVal(c.APIKeyEnvVar))
	if len(c.Profiles) > 0 {
		vals := make([]toml.Value, len(c.Profiles))
		for i, p := range c.Profiles {
			vals[i] = toml.TableVal(p.toTOML())
		}
		t.Set("profiles", toml.ArrayVal(vals))
	}
	return t
}

// KeyRemapRule defines a low-level tap/hold or direct key remap rule.
type KeyRemapRule struct {
	From         string   `json:"from"`
	Tap          string   `json:"tap,omitempty"`
	Hold         string   `json:"hold,omitempty"`
	AppBundleIDs []string `json:"appBundleIds,omitempty"`
}

func decodeKeyRemapRule(tbl *toml.Table, path string) (KeyRemapRule, error) {
	from, err := requiredString(tbl, "from", path)
	if err != nil {
		return KeyRemapRule{}, err
	}
	tap, _, err := optionalString(tbl, "tap", path)
	if err != nil {
		return KeyRemapRule{}, err
	}
	hold, _, err := optionalString(tbl, "hold", path)
	if err != nil {
		return KeyRemapRule{}, err
	}
	apps, hasApps, err := optionalStringArray(tbl, "app_bundle_ids", path)
	if err != nil {
		return KeyRemapRule{}, err
	}
	if !hasApps {
		apps = []string{}
	}
	return KeyRemapRule{
		From:         from,
		Tap:          tap,
		Hold:         hold,
		AppBundleIDs: apps,
	}, nil
}

func (r KeyRemapRule) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("from", toml.StringVal(r.From))
	if r.Tap != "" {
		t.Set("tap", toml.StringVal(r.Tap))
	}
	if r.Hold != "" {
		t.Set("hold", toml.StringVal(r.Hold))
	}
	t.Set("app_bundle_ids", stringsToTOMLArray(r.AppBundleIDs))
	return t
}

// KeymapConfig configures dual-role tap/hold keys, CapsLock remapping, and Vim navigation layer.
type KeymapConfig struct {
	Enabled            bool           `json:"enabled"`
	TapHoldThresholdMs int            `json:"tapHoldThresholdMs"`
	CapsLockTap        string         `json:"capsLockTap"`
	CapsLockHold       string         `json:"capsLockHold"`
	NavLayerEnabled    bool           `json:"navLayerEnabled"`
	Remaps             []KeyRemapRule `json:"remaps"`
}

func DefaultKeymapConfig() KeymapConfig {
	return KeymapConfig{
		Enabled:            true,
		TapHoldThresholdMs: 200,
		CapsLockTap:        "escape",
		CapsLockHold:       "nav",
		NavLayerEnabled:    true,
		Remaps:             []KeyRemapRule{},
	}
}

func decodeKeymapConfig(tbl *toml.Table, path string) (KeymapConfig, error) {
	cfg := DefaultKeymapConfig()
	if tbl == nil {
		return cfg, nil
	}
	if b, ok, err := optionalBool(tbl, "enabled", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Enabled = b
	}
	if n, ok, err := optionalInt(tbl, "tap_hold_threshold_ms", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.TapHoldThresholdMs = n
	}
	if s, ok, err := optionalString(tbl, "caps_lock_tap", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.CapsLockTap = s
	}
	if s, ok, err := optionalString(tbl, "caps_lock_hold", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.CapsLockHold = s
	}
	if b, ok, err := optionalBool(tbl, "nav_layer_enabled", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.NavLayerEnabled = b
	}
	remapTables, hasRemaps, err := optionalTableArray(tbl, "remaps", path)
	if err != nil {
		return cfg, err
	}
	if hasRemaps {
		remaps := make([]KeyRemapRule, 0, len(remapTables))
		for i, rt := range remapTables {
			rule, err := decodeKeyRemapRule(rt, fmt.Sprintf("%s.remaps[%d]", path, i))
			if err != nil {
				return cfg, err
			}
			remaps = append(remaps, rule)
		}
		cfg.Remaps = remaps
	}
	return cfg, nil
}

func (c KeymapConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	t.Set("tap_hold_threshold_ms", toml.IntVal(int64(c.TapHoldThresholdMs)))
	t.Set("caps_lock_tap", toml.StringVal(c.CapsLockTap))
	t.Set("caps_lock_hold", toml.StringVal(c.CapsLockHold))
	t.Set("nav_layer_enabled", toml.BoolVal(c.NavLayerEnabled))
	if len(c.Remaps) > 0 {
		vals := make([]toml.Value, len(c.Remaps))
		for i, r := range c.Remaps {
			vals[i] = toml.TableVal(r.toTOML())
		}
		t.Set("remaps", toml.ArrayVal(vals))
	}
	return t
}

// ClipboardConfig configures clipboard history capture and retention.
type ClipboardConfig struct {
	Enabled              bool     `json:"enabled"`
	MaxHistoryItems      int      `json:"maxHistoryItems"`
	RetentionDays        int      `json:"retentionDays"`
	IgnoreConcealedTypes bool     `json:"ignoreConcealedTypes"`
	ExcludedBundleIDs    []string `json:"excludedBundleIds"`
	// MediaCapture records images (and, on Windows, copied files/videos) in history.
	MediaCapture bool `json:"mediaCapture"`
	// MediaQuotaMB caps on-disk image blobs; oldest unpinned media rows are evicted first.
	MediaQuotaMB int `json:"mediaQuotaMb"`
}

func DefaultClipboardConfig() ClipboardConfig {
	return ClipboardConfig{
		Enabled:              true,
		MaxHistoryItems:      200,
		RetentionDays:        30,
		IgnoreConcealedTypes: true,
		ExcludedBundleIDs: []string{
			"com.1password.1password",
			"com.apple.keychainaccess",
		},
		MediaCapture: true,
		MediaQuotaMB: 512,
	}
}

func decodeClipboardConfig(tbl *toml.Table, path string) (ClipboardConfig, error) {
	cfg := DefaultClipboardConfig()
	if tbl == nil {
		return cfg, nil
	}
	if b, ok, err := optionalBool(tbl, "enabled", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Enabled = b
	}
	if n, ok, err := optionalInt(tbl, "max_history_items", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.MaxHistoryItems = n
	}
	if n, ok, err := optionalInt(tbl, "retention_days", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.RetentionDays = n
	}
	if b, ok, err := optionalBool(tbl, "ignore_concealed_types", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.IgnoreConcealedTypes = b
	}
	if arr, ok, err := optionalStringArray(tbl, "excluded_bundle_ids", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ExcludedBundleIDs = arr
	}
	if b, ok, err := optionalBool(tbl, "media_capture", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.MediaCapture = b
	}
	if n, ok, err := optionalInt(tbl, "media_quota_mb", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.MediaQuotaMB = n
	}
	return cfg, nil
}

func (c ClipboardConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	t.Set("max_history_items", toml.IntVal(int64(c.MaxHistoryItems)))
	t.Set("retention_days", toml.IntVal(int64(c.RetentionDays)))
	t.Set("ignore_concealed_types", toml.BoolVal(c.IgnoreConcealedTypes))
	t.Set("excluded_bundle_ids", stringsToTOMLArray(c.ExcludedBundleIDs))
	t.Set("media_capture", toml.BoolVal(c.MediaCapture))
	t.Set("media_quota_mb", toml.IntVal(int64(c.MediaQuotaMB)))
	return t
}

// IMEConfig configures per-app IME auto-switching.
type IMEConfig struct {
	Enabled              bool   `json:"enabled"`
	DefaultInputSourceID string `json:"defaultInputSourceId"`
	ShowIndicatorHUD     bool   `json:"showIndicatorHud"`
	SwitchDelayMs        int    `json:"switchDelayMs"`
	LockASCIIOnLeader    bool   `json:"lockAsciiOnLeader"`
	ShowFloatingPill     bool   `json:"showFloatingPill"`
}

// WindowsDefaultInputSourceID is the en-US keyboard layout HKL, formatted like
// the Windows bridge reports input source IDs.
const WindowsDefaultInputSourceID = "0x04090409"

// DefaultInputSourceIDFor returns the platform default ASCII input source ID.
func DefaultInputSourceIDFor(goos string) string {
	if goos == "windows" {
		return WindowsDefaultInputSourceID
	}
	return "com.apple.keylayout.ABC"
}

// NormalizeInputSourceIDFor maps input source IDs that cannot exist on goos
// (e.g. macOS "com.apple.*" layouts copied to Windows) to the platform default.
func NormalizeInputSourceIDFor(goos, id string) string {
	if goos == "windows" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "com.apple.") {
		return WindowsDefaultInputSourceID
	}
	return id
}

func DefaultIMEConfig() IMEConfig {
	return DefaultIMEConfigFor(runtime.GOOS)
}

// DefaultIMEConfigFor returns IME defaults for the given GOOS.
func DefaultIMEConfigFor(goos string) IMEConfig {
	return IMEConfig{
		Enabled:              true,
		DefaultInputSourceID: DefaultInputSourceIDFor(goos),
		ShowIndicatorHUD:     true,
		SwitchDelayMs:        50,
		LockASCIIOnLeader:    true,
		ShowFloatingPill:     true,
	}
}

func decodeIMEConfig(tbl *toml.Table, path string) (IMEConfig, error) {
	cfg := DefaultIMEConfig()
	if tbl == nil {
		return cfg, nil
	}
	if b, ok, err := optionalBool(tbl, "enabled", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Enabled = b
	}
	if s, ok, err := optionalString(tbl, "default_input_source_id", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.DefaultInputSourceID = NormalizeInputSourceIDFor(runtime.GOOS, s)
	}
	if b, ok, err := optionalBool(tbl, "show_indicator_hud", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ShowIndicatorHUD = b
	}
	if n, ok, err := optionalInt(tbl, "switch_delay_ms", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.SwitchDelayMs = n
	}
	if b, ok, err := optionalBool(tbl, "lock_ascii_on_leader", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.LockASCIIOnLeader = b
	}
	if b, ok, err := optionalBool(tbl, "show_floating_pill", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ShowFloatingPill = b
	}
	return cfg, nil
}

func (c IMEConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	t.Set("default_input_source_id", toml.StringVal(c.DefaultInputSourceID))
	t.Set("show_indicator_hud", toml.BoolVal(c.ShowIndicatorHUD))
	t.Set("switch_delay_ms", toml.IntVal(int64(c.SwitchDelayMs)))
	t.Set("lock_ascii_on_leader", toml.BoolVal(c.LockASCIIOnLeader))
	t.Set("show_floating_pill", toml.BoolVal(c.ShowFloatingPill))
	return t
}

// InputEngineConfig configures Clavis' built-in input engine.
//
// MixedEnglish and AutoPairPunctuation are retained for parse compatibility
// only. They have no effect and are not read from or written to config.toml
// (docs/design/own-input-engine.md, §11).
type InputEngineConfig struct {
	Enabled             bool     `json:"enabled"`
	Mode                string   `json:"mode"`
	Scheme              string   `json:"scheme"`
	CandidateCount      int      `json:"candidateCount"`
	MixedEnglish        bool     `json:"mixedEnglish"`        // Parse compatibility only; no effect.
	AutoPairPunctuation bool     `json:"autoPairPunctuation"` // Parse compatibility only; no effect.
	ShiftToggleASCII    bool     `json:"shiftToggleASCII"`
	FuzzyPinyin         []string `json:"fuzzyPinyin"`
	CustomDictPaths     []string `json:"customDictPaths"`
	UserWords           []string `json:"userWords"`
	AutoSeedFullName    bool     `json:"autoSeedFullName"`
	SeededFullName      bool     `json:"seededFullName"`
	UserLearning        bool     `json:"userLearning"`
	IMKSocketPath       string   `json:"imkSocketPath"`
	// Style is [input_engine.style], the candidate window's look.
	Style InputEngineStyleConfig `json:"style"`

	// legacyModeMigrated is decoder provenance for old native IMK/dual values.
	// It is intentionally not serialized: effective mode is always inline_hook.
	legacyModeMigrated bool
}

func DefaultInputEngineConfig() InputEngineConfig {
	return InputEngineConfig{Mode: "inline_hook", Scheme: "quanpin", CandidateCount: 7, MixedEnglish: true, AutoPairPunctuation: true, ShiftToggleASCII: true, FuzzyPinyin: []string{}, CustomDictPaths: []string{}, UserWords: []string{}, AutoSeedFullName: true, UserLearning: true, Style: DefaultInputEngineStyleConfig()}
}

// Candidate window style presets ([input_engine.style] preset).
const (
	CandidateStyleNative = "native" // system material, solid accent selection
	CandidateStyleGlass  = "glass"  // Liquid Glass, tinted selection
	CandidateStyleFlat   = "flat"   // opaque, borderless, underlined selection
	CandidateStyleCustom = "custom" // plain surface built from the fields
)

// Selection looks ([input_engine.style] selected_style). All three keep the cell's size.
const (
	CandidateSelectedFill      = "fill"
	CandidateSelectedTint      = "tint"
	CandidateSelectedUnderline = "underline"
)

// InputEngineStyleConfig is [input_engine.style]: how the candidate window looks. Only the
// candidate window reads it. [appearance] keeps the light/dark mode and the orientation
// (a layout choice); its visual preset no longer restyles the candidate window.
//
// preset picks a whole look; every other field overrides the preset when set, and 0 or ""
// means "the preset's value". The style only feeds parameters (padding, font size, page
// indicator) into the panel's size formula, so the window still sizes to fit under any
// combination.
type InputEngineStyleConfig struct {
	Preset            string  `json:"preset"`            // native | glass | flat | custom
	Radius            float64 `json:"radius"`            // window corner radius, DIPs (2..24)
	Padding           float64 `json:"padding"`           // inner padding, DIPs (2..16)
	FontSize          float64 `json:"fontSize"`          // candidate text size, points (12..22)
	Opacity           float64 `json:"opacity"`           // surface fill opacity (0..1]; text stays opaque
	Accent            string  `json:"accent"`            // "#RRGGBB"; "" follows the system accent
	SelectedStyle     string  `json:"selectedStyle"`     // fill | tint | underline
	ShowPageIndicator bool    `json:"showPageIndicator"` // "‹ › 2/9" at the end of the strip
}

func DefaultInputEngineStyleConfig() InputEngineStyleConfig {
	return InputEngineStyleConfig{Preset: CandidateStyleNative, ShowPageIndicator: true}
}

func decodeInputEngineStyleConfig(tbl *toml.Table, path string) (InputEngineStyleConfig, error) {
	c := DefaultInputEngineStyleConfig()
	if tbl == nil {
		return c, nil
	}
	// Ordered, so the first bad key reported is always the same one.
	for _, f := range []struct {
		key string
		dst *string
	}{{"preset", &c.Preset}, {"accent", &c.Accent}, {"selected_style", &c.SelectedStyle}} {
		if s, ok, e := optionalString(tbl, f.key, path); e != nil {
			return c, e
		} else if ok {
			*f.dst = strings.TrimSpace(s)
		}
	}
	for _, f := range []struct {
		key string
		dst *float64
	}{{"radius", &c.Radius}, {"padding", &c.Padding}, {"font_size", &c.FontSize}, {"opacity", &c.Opacity}} {
		if v, ok, e := optionalFloat(tbl, f.key, path); e != nil {
			return c, e
		} else if ok {
			*f.dst = v
		}
	}
	if b, ok, e := optionalBool(tbl, "show_page_indicator", path); e != nil {
		return c, e
	} else if ok {
		c.ShowPageIndicator = b
	}
	return c, nil
}

func (c InputEngineStyleConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("preset", toml.StringVal(c.Preset))
	t.Set("radius", toml.FloatVal(c.Radius))
	t.Set("padding", toml.FloatVal(c.Padding))
	t.Set("font_size", toml.FloatVal(c.FontSize))
	t.Set("opacity", toml.FloatVal(c.Opacity))
	t.Set("accent", toml.StringVal(c.Accent))
	t.Set("selected_style", toml.StringVal(c.SelectedStyle))
	t.Set("show_page_indicator", toml.BoolVal(c.ShowPageIndicator))
	return t
}
func decodeInputEngineConfig(tbl *toml.Table, path string) (InputEngineConfig, error) {
	c := DefaultInputEngineConfig()
	if tbl == nil {
		return c, nil
	}
	if b, ok, e := optionalBool(tbl, "enabled", path); e != nil {
		return c, e
	} else if ok {
		c.Enabled = b
	}
	if s, ok, e := optionalString(tbl, "mode", path); e != nil {
		return c, e
	} else if ok {
		switch s {
		case "native_imk", "dual":
			// These modes were persisted by older versions, but the native IMK
			// host is not implemented. Keep loading the config with the only
			// effective implementation and retain provenance for a warning.
			c.Mode = "inline_hook"
			c.legacyModeMigrated = true
		default:
			c.Mode = s
		}
	}
	if s, ok, e := optionalString(tbl, "scheme", path); e != nil {
		return c, e
	} else if ok {
		c.Scheme = s
	}
	if n, ok, e := optionalInt(tbl, "candidate_count", path); e != nil {
		return c, e
	} else if ok {
		c.CandidateCount = n
	}
	if b, ok, e := optionalBool(tbl, "shift_toggle_ascii", path); e != nil {
		return c, e
	} else if ok {
		c.ShiftToggleASCII = b
	}
	if a, ok, e := optionalStringArray(tbl, "fuzzy_pinyin", path); e != nil {
		return c, e
	} else if ok {
		c.FuzzyPinyin = a
	}
	if a, ok, e := optionalStringArray(tbl, "custom_dict_paths", path); e != nil {
		return c, e
	} else if ok {
		c.CustomDictPaths = a
	}
	if a, ok, e := optionalStringArray(tbl, "user_words", path); e != nil {
		return c, e
	} else if ok {
		c.UserWords = a
	}
	if b, ok, e := optionalBool(tbl, "auto_seed_full_name", path); e != nil {
		return c, e
	} else if ok {
		c.AutoSeedFullName = b
	}
	if b, ok, e := optionalBool(tbl, "seeded_full_name", path); e != nil {
		return c, e
	} else if ok {
		c.SeededFullName = b
	}
	if b, ok, e := optionalBool(tbl, "user_learning", path); e != nil {
		return c, e
	} else if ok {
		c.UserLearning = b
	}
	if s, ok, e := optionalString(tbl, "imk_socket_path", path); e != nil {
		return c, e
	} else if ok {
		c.IMKSocketPath = s
	}
	styleTbl, _, e := optionalTable(tbl, "style", path)
	if e != nil {
		return c, e
	}
	if c.Style, e = decodeInputEngineStyleConfig(styleTbl, joinPath(path, "style")); e != nil {
		return c, e
	}
	return c, nil
}
func (c InputEngineConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	t.Set("mode", toml.StringVal(c.Mode))
	t.Set("scheme", toml.StringVal(c.Scheme))
	t.Set("candidate_count", toml.IntVal(int64(c.CandidateCount)))
	t.Set("shift_toggle_ascii", toml.BoolVal(c.ShiftToggleASCII))
	t.Set("fuzzy_pinyin", stringsToTOMLArray(c.FuzzyPinyin))
	t.Set("custom_dict_paths", stringsToTOMLArray(c.CustomDictPaths))
	if len(c.UserWords) > 0 {
		t.Set("user_words", stringsToTOMLArray(c.UserWords))
	}
	if !c.AutoSeedFullName {
		t.Set("auto_seed_full_name", toml.BoolVal(false))
	}
	if c.SeededFullName {
		t.Set("seeded_full_name", toml.BoolVal(true))
	}
	t.Set("user_learning", toml.BoolVal(c.UserLearning))
	t.Set("imk_socket_path", toml.StringVal(c.IMKSocketPath))
	t.Set("style", toml.TableVal(c.Style.toTOML()))
	return t
}

// SelectionBarConfig configures text-selection actions.
type SelectionBarConfig struct {
	Enabled     bool     `json:"enabled"`
	TriggerMode string   `json:"triggerMode"`
	Actions     []string `json:"actions"`
}

func DefaultSelectionBarConfig() SelectionBarConfig {
	return SelectionBarConfig{Enabled: true, TriggerMode: "double_shift", Actions: []string{"optimize_prompt", "explain", "translate", "polish", "copy"}}
}
func decodeSelectionBarConfig(tbl *toml.Table, path string) (SelectionBarConfig, error) {
	c := DefaultSelectionBarConfig()
	if tbl == nil {
		return c, nil
	}
	if b, o, e := optionalBool(tbl, "enabled", path); e != nil {
		return c, e
	} else if o {
		c.Enabled = b
	}
	if s, o, e := optionalString(tbl, "trigger_mode", path); e != nil {
		return c, e
	} else if o {
		c.TriggerMode = s
	}
	if a, o, e := optionalStringArray(tbl, "actions", path); e != nil {
		return c, e
	} else if o {
		c.Actions = a
	}
	return c, nil
}
func (c SelectionBarConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	t.Set("trigger_mode", toml.StringVal(c.TriggerMode))
	t.Set("actions", stringsToTOMLArray(c.Actions))
	return t
}

// DaemonConfig controls resident process lifecycle behavior.
type DaemonConfig struct {
	IdleCompactSeconds      int    `json:"idleCompactSeconds"`
	MaxMemoryMB             int    `json:"maxMemoryMB"`
	WatchdogIntervalMs      int    `json:"watchdogIntervalMs"`
	InPlaceOptimizeShortcut string `json:"inPlaceOptimizeShortcut"`
}

func DefaultDaemonConfig() DaemonConfig {
	return DaemonConfig{IdleCompactSeconds: 60, MaxMemoryMB: 64, WatchdogIntervalMs: 5000, InPlaceOptimizeShortcut: "option+tab"}
}
func decodeDaemonConfig(tbl *toml.Table, path string) (DaemonConfig, error) {
	c := DefaultDaemonConfig()
	if tbl == nil {
		return c, nil
	}
	if n, o, e := optionalInt(tbl, "idle_compact_seconds", path); e != nil {
		return c, e
	} else if o {
		c.IdleCompactSeconds = n
	}
	if n, o, e := optionalInt(tbl, "max_memory_mb", path); e != nil {
		return c, e
	} else if o {
		c.MaxMemoryMB = n
	}
	if n, o, e := optionalInt(tbl, "watchdog_interval_ms", path); e != nil {
		return c, e
	} else if o {
		c.WatchdogIntervalMs = n
	}
	if s, o, e := optionalString(tbl, "in_place_optimize_shortcut", path); e != nil {
		return c, e
	} else if o {
		c.InPlaceOptimizeShortcut = s
	}
	return c, nil
}
func (c DaemonConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("idle_compact_seconds", toml.IntVal(int64(c.IdleCompactSeconds)))
	t.Set("max_memory_mb", toml.IntVal(int64(c.MaxMemoryMB)))
	t.Set("watchdog_interval_ms", toml.IntVal(int64(c.WatchdogIntervalMs)))
	t.Set("in_place_optimize_shortcut", toml.StringVal(c.InPlaceOptimizeShortcut))
	return t
}

// MainConfig represents config.toml.
type MainConfig struct {
	Trigger      TriggerConfig      `json:"trigger"`
	Keymap       KeymapConfig       `json:"keymap"`
	Appearance   AppearanceConfig   `json:"appearance"`
	AI           AIConfig           `json:"ai"`
	Clipboard    ClipboardConfig    `json:"clipboard"`
	IME          IMEConfig          `json:"ime"`
	InputEngine  InputEngineConfig  `json:"inputEngine"`
	SelectionBar SelectionBarConfig `json:"selectionBar"`
	Daemon       DaemonConfig       `json:"daemon"`
	CLI          CLIConfig          `json:"cli"`
	MCP          MCPConfig          `json:"mcp"`
}

func DefaultMainConfig() MainConfig {
	return MainConfig{
		Trigger:      DefaultTriggerConfig(),
		Keymap:       DefaultKeymapConfig(),
		Appearance:   DefaultAppearanceConfig(),
		AI:           DefaultAIConfig(),
		Clipboard:    DefaultClipboardConfig(),
		IME:          DefaultIMEConfig(),
		InputEngine:  DefaultInputEngineConfig(),
		SelectionBar: DefaultSelectionBarConfig(),
		Daemon:       DefaultDaemonConfig(),
		CLI:          DefaultCLIConfig(),
		MCP:          DefaultMCPConfig(),
	}
}

// DecodeMainConfig parses and decodes config.toml.
func DecodeMainConfig(input string) (MainConfig, error) {
	tbl, err := toml.Parse(input)
	if err != nil {
		return MainConfig{}, err
	}
	return MainConfigFromTOML(tbl)
}

// MainConfigFromTOML decodes MainConfig from a parsed TOML table.
func MainConfigFromTOML(tbl *toml.Table) (MainConfig, error) {
	triggerTbl, _, err := optionalTable(tbl, "trigger", "")
	if err != nil {
		return MainConfig{}, err
	}
	keymapTbl, _, err := optionalTable(tbl, "keymap", "")
	if err != nil {
		return MainConfig{}, err
	}
	appTbl, _, err := optionalTable(tbl, "appearance", "")
	if err != nil {
		return MainConfig{}, err
	}
	aiTbl, _, err := optionalTable(tbl, "ai", "")
	if err != nil {
		return MainConfig{}, err
	}
	clipTbl, _, err := optionalTable(tbl, "clipboard", "")
	if err != nil {
		return MainConfig{}, err
	}
	imeTbl, _, err := optionalTable(tbl, "ime", "")
	if err != nil {
		return MainConfig{}, err
	}
	inputTbl, _, err := optionalTable(tbl, "input_engine", "")
	if err != nil {
		return MainConfig{}, err
	}
	selectionTbl, _, err := optionalTable(tbl, "selection_bar", "")
	if err != nil {
		return MainConfig{}, err
	}
	daemonTbl, _, err := optionalTable(tbl, "daemon", "")
	if err != nil {
		return MainConfig{}, err
	}
	cliTbl, _, err := optionalTable(tbl, "cli", "")
	if err != nil {
		return MainConfig{}, err
	}
	mcpTbl, _, err := optionalTable(tbl, "mcp", "")
	if err != nil {
		return MainConfig{}, err
	}

	trigger, err := decodeTriggerConfig(triggerTbl, "trigger")
	if err != nil {
		return MainConfig{}, err
	}
	keymap, err := decodeKeymapConfig(keymapTbl, "keymap")
	if err != nil {
		return MainConfig{}, err
	}
	appearance, err := decodeAppearanceConfig(appTbl, "appearance")
	if err != nil {
		return MainConfig{}, err
	}
	aiCfg, err := decodeAIConfig(aiTbl, "ai")
	if err != nil {
		return MainConfig{}, err
	}
	clipboard, err := decodeClipboardConfig(clipTbl, "clipboard")
	if err != nil {
		return MainConfig{}, err
	}
	ime, err := decodeIMEConfig(imeTbl, "ime")
	if err != nil {
		return MainConfig{}, err
	}
	inputEngine, err := decodeInputEngineConfig(inputTbl, "input_engine")
	if err != nil {
		return MainConfig{}, err
	}
	selectionBar, err := decodeSelectionBarConfig(selectionTbl, "selection_bar")
	if err != nil {
		return MainConfig{}, err
	}
	daemon, err := decodeDaemonConfig(daemonTbl, "daemon")
	if err != nil {
		return MainConfig{}, err
	}
	cli, err := decodeCLIConfig(cliTbl, "cli")
	if err != nil {
		return MainConfig{}, err
	}
	mcp, err := decodeMCPConfig(mcpTbl, "mcp")
	if err != nil {
		return MainConfig{}, err
	}
	return MainConfig{Trigger: trigger, Keymap: keymap, Appearance: appearance, AI: aiCfg, Clipboard: clipboard, IME: ime, InputEngine: inputEngine, SelectionBar: selectionBar, Daemon: daemon, CLI: cli, MCP: mcp}, nil
}

// Encode serializes MainConfig to deterministic TOML.
func (c MainConfig) Encode() string {
	root := toml.NewTable()
	root.Set("trigger", toml.TableVal(c.Trigger.toTOML()))
	root.Set("keymap", toml.TableVal(c.Keymap.toTOML()))
	root.Set("appearance", toml.TableVal(c.Appearance.toTOML()))
	root.Set("ai", toml.TableVal(c.AI.toTOML()))
	root.Set("clipboard", toml.TableVal(c.Clipboard.toTOML()))
	root.Set("ime", toml.TableVal(c.IME.toTOML()))
	root.Set("input_engine", toml.TableVal(c.InputEngine.toTOML()))
	root.Set("selection_bar", toml.TableVal(c.SelectionBar.toTOML()))
	root.Set("daemon", toml.TableVal(c.Daemon.toTOML()))
	root.Set("cli", toml.TableVal(c.CLI.toTOML()))
	root.Set("mcp", toml.TableVal(c.MCP.toTOML()))
	return toml.Encode(root)
}
