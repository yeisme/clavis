package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/config"
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
// and composition, permissions, and the macOS native input-method bundle.
//
// It is read-only: no config writes, no engine installation, no persistence.
func runSelfCheck(w io.Writer) error {
	cfgDir := resolveConfigDir()
	fmt.Fprintf(w, "Clavis self-check (%s/%s)\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(w, "config dir: %s\n", cfgDir)

	// --- configuration -------------------------------------------------------
	cfg := config.DefaultConfig()
	store := config.NewConfigStore(cfgDir)
	usingDefaults := true
	if _, err := os.Stat(cfgDir); err == nil {
		if loaded, loadErr := store.LoadOrInitialize(); loadErr == nil {
			cfg, usingDefaults = loaded, false
		}
	}
	if usingDefaults {
		fmt.Fprintln(w, "config: defaults (no config dir on disk)")
	} else {
		fmt.Fprintln(w, "config: loaded")
	}
	for _, e := range store.LastErrors() {
		fmt.Fprintf(w, "config error: %v\n", e)
	}
	for _, warn := range store.LastWarnings() {
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
	probeInputEngine(w, ie)

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

	// --- macOS native input method ------------------------------------------
	if runtime.GOOS == "darwin" {
		st := bridge.IMKInstallStatus()
		fmt.Fprintf(w, "native input method: installed=%v path=%q\n", st.Installed, st.Path)
		if !st.Installed {
			fmt.Fprintln(w, "native input method: not installed (inline hook mode still works)")
		}
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

// probeInputEngine composes a known pinyin string and reports the candidates,
// which proves the dictionary layers actually resolved and the engine runs.
func probeInputEngine(w io.Writer, ie config.InputEngineConfig) {
	eng := inputengine.New(inputengine.Options{
		Scheme:           ie.Scheme,
		CandidateCount:   ie.CandidateCount,
		ShiftToggleASCII: ie.ShiftToggleASCII,
		FuzzyPinyin:      ie.FuzzyPinyin,
		CustomDictPaths:  ie.CustomDictPaths,
	})
	for _, r := range "nihao" {
		eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	view := eng.View()
	if len(view.Candidates) == 0 {
		fmt.Fprintf(w, "input engine probe: typing \"nihao\" produced NO candidates (dictionary failed to load)\n")
		return
	}
	names := make([]string, 0, 3)
	for i, c := range view.Candidates {
		if i >= 3 {
			break
		}
		names = append(names, c.Text)
	}
	fmt.Fprintf(w, "input engine probe: typing \"nihao\" -> %s\n", strings.Join(names, " / "))
}
