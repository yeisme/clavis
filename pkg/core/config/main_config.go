package config

import (
	"github.com/yeisme/clavis/pkg/core/toml"
)

// TriggerConfig configures global hotkeys and snippet expansion triggers.
type TriggerConfig struct {
	LeaderKey               string `json:"leaderKey"`
	DoubleTapKey            string `json:"doubleTapKey"`
	DoubleTapIntervalMs     int    `json:"doubleTapIntervalMs"`
	CapsuleShortcut         string `json:"capsuleShortcut"`
	SnippetExpansionEnabled bool   `json:"snippetExpansionEnabled"`
}

func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		LeaderKey:               "right_option",
		DoubleTapKey:            "right_command",
		DoubleTapIntervalMs:     300,
		CapsuleShortcut:         "option+space",
		SnippetExpansionEnabled: true,
	}
}

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
	if b, ok, err := optionalBool(tbl, "snippet_expansion_enabled", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.SnippetExpansionEnabled = b
	}
	return cfg, nil
}

func (c TriggerConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("leader_key", toml.StringVal(c.LeaderKey))
	t.Set("double_tap_key", toml.StringVal(c.DoubleTapKey))
	t.Set("double_tap_interval_ms", toml.IntVal(int64(c.DoubleTapIntervalMs)))
	t.Set("capsule_shortcut", toml.StringVal(c.CapsuleShortcut))
	t.Set("snippet_expansion_enabled", toml.BoolVal(c.SnippetExpansionEnabled))
	return t
}

// AppearanceConfig configures Capsule UI visuals and timings.
type AppearanceConfig struct {
	Theme             string  `json:"theme"`
	CapsuleWidth      float64 `json:"capsuleWidth"`
	MaxVisibleResults int     `json:"maxVisibleResults"`
	CornerRadius      float64 `json:"cornerRadius"`
	ShowKeyHints      bool    `json:"showKeyHints"`
	HUDDurationMs     int     `json:"hudDurationMs"`
}

func DefaultAppearanceConfig() AppearanceConfig {
	return AppearanceConfig{
		Theme:             "system",
		CapsuleWidth:      680.0,
		MaxVisibleResults: 8,
		CornerRadius:      16.0,
		ShowKeyHints:      true,
		HUDDurationMs:     1400,
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
	if n, ok, err := optionalInt(tbl, "hud_duration_ms", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.HUDDurationMs = n
	}
	return cfg, nil
}

func (c AppearanceConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("theme", toml.StringVal(c.Theme))
	t.Set("capsule_width", toml.FloatVal(c.CapsuleWidth))
	t.Set("max_visible_results", toml.IntVal(int64(c.MaxVisibleResults)))
	t.Set("corner_radius", toml.FloatVal(c.CornerRadius))
	t.Set("show_key_hints", toml.BoolVal(c.ShowKeyHints))
	t.Set("hud_duration_ms", toml.IntVal(int64(c.HUDDurationMs)))
	return t
}

// AIConfig configures the OpenAI-compatible AI provider.
type AIConfig struct {
	Provider        string  `json:"provider"`
	BaseURL         string  `json:"baseUrl"`
	Model           string  `json:"model"`
	Temperature     float64 `json:"temperature"`
	TimeoutSeconds  float64 `json:"timeoutSeconds"`
	SystemPrompt    string  `json:"systemPrompt"`
	KeychainAccount string  `json:"keychainAccount"`
}

func DefaultAIConfig() AIConfig {
	return AIConfig{
		Provider:        "openai",
		BaseURL:         "https://api.openai.com/v1",
		Model:           "gpt-4o-mini",
		Temperature:     0.2,
		TimeoutSeconds:  30.0,
		SystemPrompt:    "You are a concise macOS writing and productivity assistant. Return only the requested output unless asked otherwise.",
		KeychainAccount: "clavis.ai.default",
	}
}

func decodeAIConfig(tbl *toml.Table, path string) (AIConfig, error) {
	cfg := DefaultAIConfig()
	if tbl == nil {
		return cfg, nil
	}
	for _, forbiddenKey := range []string{"api_key", "apiKey", "secret_key", "secretKey"} {
		if _, exists := tbl.Get(forbiddenKey); exists {
			return cfg, &DecodingError{
				KeyPath: joinPath(path, forbiddenKey),
				Message: "Raw API keys must never be stored in config.toml; use 'keychain_account' instead",
			}
		}
	}
	if s, ok, err := optionalString(tbl, "provider", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Provider = s
	}
	if s, ok, err := optionalString(tbl, "base_url", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.BaseURL = s
	}
	if s, ok, err := optionalString(tbl, "model", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Model = s
	}
	if f, ok, err := optionalFloat(tbl, "temperature", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Temperature = f
	}
	if f, ok, err := optionalFloat(tbl, "timeout_seconds", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.TimeoutSeconds = f
	}
	if s, ok, err := optionalString(tbl, "system_prompt", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.SystemPrompt = s
	}
	if s, ok, err := optionalString(tbl, "keychain_account", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.KeychainAccount = s
	}
	return cfg, nil
}

func (c AIConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("provider", toml.StringVal(c.Provider))
	t.Set("base_url", toml.StringVal(c.BaseURL))
	t.Set("model", toml.StringVal(c.Model))
	t.Set("temperature", toml.FloatVal(c.Temperature))
	t.Set("timeout_seconds", toml.FloatVal(c.TimeoutSeconds))
	t.Set("system_prompt", toml.StringVal(c.SystemPrompt))
	t.Set("keychain_account", toml.StringVal(c.KeychainAccount))
	return t
}

// ClipboardConfig configures clipboard history capture and retention.
type ClipboardConfig struct {
	Enabled              bool     `json:"enabled"`
	MaxHistoryItems      int      `json:"maxHistoryItems"`
	RetentionDays        int      `json:"retentionDays"`
	IgnoreConcealedTypes bool     `json:"ignoreConcealedTypes"`
	ExcludedBundleIDs    []string `json:"excludedBundleIds"`
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
	return cfg, nil
}

func (c ClipboardConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	t.Set("max_history_items", toml.IntVal(int64(c.MaxHistoryItems)))
	t.Set("retention_days", toml.IntVal(int64(c.RetentionDays)))
	t.Set("ignore_concealed_types", toml.BoolVal(c.IgnoreConcealedTypes))
	t.Set("excluded_bundle_ids", stringsToTOMLArray(c.ExcludedBundleIDs))
	return t
}

// IMEConfig configures per-app IME auto-switching.
type IMEConfig struct {
	Enabled              bool   `json:"enabled"`
	DefaultInputSourceID string `json:"defaultInputSourceId"`
	ShowIndicatorHUD     bool   `json:"showIndicatorHud"`
	SwitchDelayMs        int    `json:"switchDelayMs"`
}

func DefaultIMEConfig() IMEConfig {
	return IMEConfig{
		Enabled:              true,
		DefaultInputSourceID: "com.apple.keylayout.ABC",
		ShowIndicatorHUD:     true,
		SwitchDelayMs:        50,
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
		cfg.DefaultInputSourceID = s
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
	return cfg, nil
}

func (c IMEConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	t.Set("default_input_source_id", toml.StringVal(c.DefaultInputSourceID))
	t.Set("show_indicator_hud", toml.BoolVal(c.ShowIndicatorHUD))
	t.Set("switch_delay_ms", toml.IntVal(int64(c.SwitchDelayMs)))
	return t
}

// MainConfig represents config.toml.
type MainConfig struct {
	Trigger    TriggerConfig    `json:"trigger"`
	Appearance AppearanceConfig `json:"appearance"`
	AI         AIConfig         `json:"ai"`
	Clipboard  ClipboardConfig  `json:"clipboard"`
	IME        IMEConfig        `json:"ime"`
}

func DefaultMainConfig() MainConfig {
	return MainConfig{
		Trigger:    DefaultTriggerConfig(),
		Appearance: DefaultAppearanceConfig(),
		AI:         DefaultAIConfig(),
		Clipboard:  DefaultClipboardConfig(),
		IME:        DefaultIMEConfig(),
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

	trigger, err := decodeTriggerConfig(triggerTbl, "trigger")
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

	return MainConfig{
		Trigger:    trigger,
		Appearance: appearance,
		AI:         aiCfg,
		Clipboard:  clipboard,
		IME:        ime,
	}, nil
}

// Encode serializes MainConfig to deterministic TOML.
func (c MainConfig) Encode() string {
	root := toml.NewTable()
	root.Set("trigger", toml.TableVal(c.Trigger.toTOML()))
	root.Set("appearance", toml.TableVal(c.Appearance.toTOML()))
	root.Set("ai", toml.TableVal(c.AI.toTOML()))
	root.Set("clipboard", toml.TableVal(c.Clipboard.toTOML()))
	root.Set("ime", toml.TableVal(c.IME.toTOML()))
	return toml.Encode(root)
}
