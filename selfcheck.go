package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/daemon"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/ime"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// hasSelfCheckFlag reports whether --self-check is present before any "--" terminator.
func hasSelfCheckFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--self-check" {
			return true
		}
	}
	return false
}

// runSelfCheck prints a human-readable diagnostic of everything that silently
// fails on a real machine: config parsing, the own input engine's dictionary
// and composition, permissions, daemon runtime health, and inline-hook mode.
//
// It is read-only: no config writes, no engine installation, no persistence.
// mustLaunchAgentPath returns the keep-alive plist path, or "" when it cannot be resolved.
func mustLaunchAgentPath() string {
	p, err := bridge.LaunchAgentPath()
	if err != nil {
		return ""
	}
	return p
}

func runSelfCheck(w io.Writer) error {
	cfgDir := resolveConfigDir()
	fmt.Fprintf(w, "Clavis self-check (%s/%s)\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(w, "config dir: %s\n", cfgDir)

	// --- configuration -------------------------------------------------------
	cfg, loadedConfig, cfgErrors, cfgWarnings := loadConfigReadOnly(cfgDir)
	switch {
	case loadedConfig:
		fmt.Fprintln(w, "config: loaded")
	case len(cfgErrors) > 0:
		fmt.Fprintln(w, "config: defaults (failed to load existing config; see errors below)")
	default:
		fmt.Fprintln(w, "config: defaults (no config files on disk)")
	}
	for _, e := range cfgErrors {
		fmt.Fprintf(w, "config error: %v\n", e)
	}
	for _, warn := range cfgWarnings {
		fmt.Fprintf(w, "config warning: %s: %s\n", warn.KeyPath, warn.Message)
	}

	// --- own input engine ----------------------------------------------------
	ie := cfg.Main.InputEngine
	fmt.Fprintf(w, "input engine: enabled=%v mode=%q scheme=%q candidates=%d learning=%v\n",
		ie.Enabled, ie.Mode, ie.Scheme, ie.CandidateCount, ie.UserLearning)
	if len(ie.CustomDictPaths) > 0 {
		fmt.Fprintf(w, "input engine dicts: %s\n", strings.Join(ie.CustomDictPaths, ", "))
	}
	if !ie.Enabled {
		fmt.Fprintln(w, "input engine: OFF (set [input_engine] enabled = true to take over typing)")
	}
	userDict, dictStatus := inputengine.LoadUserDictionary()
	activeDict := userDict
	var dictionaryDiagnostics []inputengine.DictionaryDiagnostic
	var dictErr error
	if hasCustomDictPaths(ie.CustomDictPaths) {
		var customDict inputengine.Dictionary
		customDict, dictErr = inputengine.LoadDictionaryWithDiagnostics(ie.CustomDictPaths, "", func(d inputengine.DictionaryDiagnostic) {
			dictionaryDiagnostics = append(dictionaryDiagnostics, d)
		})
		if customDict != nil {
			activeDict = customDict
		}
	}
	fmt.Fprintf(w, "dictionary: %s (%s) · %d\n", dictStatus.Source, dictStatus.Path, dictStatus.Count)
	for _, diagnostic := range dictStatus.Diagnostics {
		fmt.Fprintf(w, "dictionary diagnostic: %s\n", diagnostic)
	}
	for _, diagnostic := range dictionaryDiagnostics {
		fmt.Fprintf(w, "dictionary diagnostic: %s: %s\n", diagnostic.Path, diagnostic.Message)
	}
	if dictErr != nil {
		fmt.Fprintf(w, "dictionary error: %v\n", dictErr)
	}
	probeInputEngine(w, ie, activeDict)

	// --- permissions ---------------------------------------------------------
	ctx := context.Background()
	br := bridge.NewHostService(
		bridge.WithTriggerConfig(cfg.Main.Trigger),
		bridge.WithKeymapConfig(cfg.Main.Keymap),
		bridge.WithIMEConfig(cfg.Main.IME, cfg.IMERules),
	)
	caps := br.Capabilities()
	fmt.Fprintf(w, "capabilities: ax=%v input=%v hotkeys=%v ime=%v window=%v\n",
		caps.Accessibility, caps.InputMonitoring, caps.GlobalHotkeys, caps.IMEControl, caps.WindowManagement)
	if perm, err := br.Permissions(ctx); err == nil {
		fmt.Fprintf(w, "permissions: accessibility=%v input-monitoring=%v apple-events=%v granted=%v\n",
			perm.Accessibility, perm.InputMonitoring, perm.AppleEvents, perm.Granted)
		if !perm.Granted {
			fmt.Fprintln(w, "permissions: NOT granted — grant Accessibility + Input Monitoring in System Settings")
		}
	} else {
		fmt.Fprintf(w, "permissions: query failed: %v\n", err)
	}

	// --- operational state ---------------------------------------------------
	// These two lines answer the questions that cost the most time when they are missing:
	// "is the app that should be handling my typing actually running?" and "where does the AI
	// key come from?". Both failures look like unrelated bugs from the outside — the user just
	// sees the system IME's candidate window and an unexplained authorisation dialog.
	if runtime.GOOS == "darwin" {
		fmt.Fprintf(w, "keep-alive agent: installed=%v path=%q\n", bridge.LaunchAgentInstalled(), mustLaunchAgentPath())
	}
	keysPath := secrets.DefaultKeysPath(cfgDir)
	if info, err := os.Stat(keysPath); err == nil {
		src := secrets.NewFileStore(keysPath)
		// Probe the profile the user actually configured, not the default one.
		probe := cfg.Main.AI.ActiveProfile()
		if srcName, srcErr := src.ResolveAPIKeySource(probe.KeychainAccount); srcErr == nil && srcName != secrets.KeySourceNone {
			fmt.Fprintf(w, "ai key source: %s (%s, mode %04o, account %q)\n",
				srcName, keysPath, info.Mode().Perm(), probe.KeychainAccount)
		} else {
			fmt.Fprintf(w, "ai key source: file present but no key for %q (%s)\n", probe.KeychainAccount, keysPath)
		}
	} else {
		fmt.Fprintf(w, "ai key source: none (%s missing)\n", keysPath)
	}

	// --- daemon runtime health (read-only) ----------------------------------
	writeDaemonSelfCheck(w, cfg.Main.Daemon)

	// --- input method integration -------------------------------------------
	// Clavis operates as a second-IME inline-hook engine alongside the OS IME
	// rather than replacing it with a system InputMethodKit bundle.
	if runtime.GOOS == "darwin" {
		st := bridge.IMKInstallStatus()
		fmt.Fprintf(w, "input method mode: inline-hook (second-IME; optional legacy IMK bundle installed=%v path=%q)\n", st.Installed, st.Path)
	} else {
		fmt.Fprintln(w, "input method mode: inline-hook (second-IME)")
	}

	// --- features that are easy to misconfigure -----------------------------
	sb := cfg.Main.SelectionBar
	fmt.Fprintf(w, "selection bar: enabled=%v trigger=%q actions=%d\n", sb.Enabled, sb.TriggerMode, len(sb.Actions))
	fmt.Fprintf(w, "in-place optimize shortcut: %q\n", cfg.Main.Daemon.InPlaceOptimizeShortcut)
	fmt.Fprintf(w, "prompts: %d templates, optimizer preset %q, inline prefix %q\n",
		len(cfg.Prompts.Items), cfg.Prompts.DefaultOptimizerPreset, cfg.Prompts.InlineOptimizePrefix)
	fmt.Fprintf(w, "components: %d widgets, %d sources, %d scripts\n",
		len(cfg.Components.Widgets), len(cfg.Components.Sources), len(cfg.Components.Scripts))

	fmt.Fprintln(w, "self-check ok")
	return nil
}

// loadConfigReadOnly reads existing config files from cfgDir without ever creating or
// mutating files on disk. Missing config dirs or empty dirs fall back to in-memory defaults.
func loadConfigReadOnly(cfgDir string) (config.ClavisConfig, bool, []config.LoadError, []config.ValidationIssue) {
	cfg := config.DefaultConfig()
	if _, err := os.Stat(cfgDir); err != nil {
		return cfg, false, nil, nil
	}

	anyPresent := false
	for _, kind := range config.AllConfigFileKinds() {
		if _, err := os.Stat(filepath.Join(cfgDir, string(kind))); err == nil {
			anyPresent = true
			break
		}
	}
	if !anyPresent {
		return cfg, false, nil, nil
	}

	required := []config.ConfigFileKind{
		config.FileMain,
		config.FileLeader,
		config.FileSnippets,
		config.FileGlossary,
		config.FileIMERules,
		config.FilePrompts,
		config.FileComponents,
	}
	allRequiredPresent := true
	for _, kind := range required {
		if _, err := os.Stat(filepath.Join(cfgDir, string(kind))); err != nil {
			allRequiredPresent = false
			break
		}
	}
	if allRequiredPresent {
		store := config.NewConfigStore(cfgDir)
		if store.Reload() {
			return store.Current(), true, nil, store.LastWarnings()
		}
		return cfg, false, store.LastErrors(), store.LastWarnings()
	}

	// Partial config directory: decode whichever TOML files exist on disk, keeping
	// in-memory defaults for absent files without writing anything to disk.
	var errs []config.LoadError
	readOptional := func(kind config.ConfigFileKind, apply func(string) error) {
		path := filepath.Join(cfgDir, string(kind))
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			errs = append(errs, config.LoadError{File: string(kind), Message: err.Error()})
			return
		}
		if err := apply(string(data)); err != nil {
			errs = append(errs, config.LoadError{File: string(kind), Message: err.Error()})
		}
	}
	readOptional(config.FileMain, func(s string) error {
		v, err := config.DecodeMainConfig(s)
		if err == nil {
			cfg.Main = v
		}
		return err
	})
	readOptional(config.FileLeader, func(s string) error {
		v, err := config.DecodeLeaderConfig(s)
		if err == nil {
			cfg.Leader = v
		}
		return err
	})
	readOptional(config.FileSnippets, func(s string) error {
		v, err := config.DecodeSnippetsConfig(s)
		if err == nil {
			cfg.Snippets = v
		}
		return err
	})
	readOptional(config.FileGlossary, func(s string) error {
		v, err := config.DecodeGlossaryConfig(s)
		if err == nil {
			cfg.Glossary = v
		}
		return err
	})
	readOptional(config.FileIMERules, func(s string) error {
		v, err := config.DecodeIMERulesConfig(s)
		if err == nil {
			cfg.IMERules = v
		}
		return err
	})
	readOptional(config.FilePrompts, func(s string) error {
		v, err := config.DecodePromptsConfig(s)
		if err == nil {
			cfg.Prompts = v
		}
		return err
	})
	readOptional(config.FileComponents, func(s string) error {
		v, err := config.DecodeComponentsConfig(s)
		if err == nil {
			cfg.Components = v
		}
		return err
	})
	readOptional(config.FileWorkflows, func(s string) error {
		v, err := config.DecodeWorkflowsConfig(s)
		if err == nil {
			cfg.Workflows = v
		}
		return err
	})
	readOptional(config.FileQuickJump, func(s string) error {
		v, err := config.DecodeQuickJumpConfig(s)
		if err == nil {
			cfg.QuickJump = v
		}
		return err
	})

	var warnings []config.ValidationIssue
	for _, issue := range config.Validate(cfg) {
		if issue.Severity == config.SeverityError {
			errs = append(errs, config.LoadError{
				File:    issue.File,
				KeyPath: issue.KeyPath,
				Message: issue.Message,
			})
		} else {
			warnings = append(warnings, issue)
		}
	}
	if len(errs) > 0 {
		return config.DefaultConfig(), false, errs, warnings
	}
	return cfg, true, nil, warnings
}

func hasCustomDictPaths(paths []string) bool {
	for _, p := range paths {
		if strings.TrimSpace(p) != "" {
			return true
		}
	}
	return false
}

// probeInputSequence returns the key sequence that encodes "ni" + "hao" under scheme
// (e.g. "nihao" for quanpin, "nihc" for xiaohe/ziranma, "nihk" for microsoft/sogou/pinyin_plus).
func probeInputSequence(scheme string) string {
	norm := strings.ToLower(strings.TrimSpace(scheme))
	return ime.ShuangpinCode(norm, "ni") + ime.ShuangpinCode(norm, "hao")
}

// probeInputEngine composes a scheme-appropriate pinyin string using the already-loaded
// dictionary and reports the candidates, proving the dictionary layers resolved and the engine runs.
func probeInputEngine(w io.Writer, ie config.InputEngineConfig, dict inputengine.Dictionary) {
	eng := inputengine.New(inputengine.Options{
		Scheme:           ie.Scheme,
		CandidateCount:   ie.CandidateCount,
		ShiftToggleASCII: ie.ShiftToggleASCII,
		FuzzyPinyin:      ie.FuzzyPinyin,
		Dictionary:       dict,
	})
	seq := probeInputSequence(ie.Scheme)
	for _, r := range seq {
		eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	view := eng.View()
	if len(view.Candidates) == 0 {
		fmt.Fprintf(w, "input engine probe: typing %q produced NO candidates (dictionary failed to load)\n", seq)
		return
	}
	names := make([]string, 0, 3)
	for i, c := range view.Candidates {
		if i >= 3 {
			break
		}
		names = append(names, c.Text)
	}
	fmt.Fprintf(w, "input engine probe: typing %q -> %s\n", seq, strings.Join(names, " / "))
}

// writeDaemonSelfCheck prints a read-only snapshot of resident supervisor
// configuration defaults and process telemetry without mutating disk or
// starting background goroutines.
func writeDaemonSelfCheck(w io.Writer, dcfg config.DaemonConfig) {
	sup := daemon.NewResidentSupervisor(dcfg)
	snap := sup.Snapshot()
	fmt.Fprintf(
		w,
		"daemon runtime: status=%s watchdog=%s idle_compact=%ds max_mem=%dMB heal_cooldown=%s alloc=%.1fMB goroutines=%d\n",
		snap.HealthStatus,
		daemon.WatchdogInterval(dcfg),
		dcfg.IdleCompactSeconds,
		dcfg.MaxMemoryMB,
		daemon.DefaultBridgeHealCooldown,
		snap.AllocMB,
		snap.NumGoroutine,
	)
}
