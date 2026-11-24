package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestRunSelfCheckReadOnlyOnMissingAndEmptyConfigDir(t *testing.T) {
	t.Run("missing config dir stays absent", func(t *testing.T) {
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("USERPROFILE", fakeHome)

		var out bytes.Buffer
		if err := runSelfCheck(&out); err != nil {
			t.Fatalf("runSelfCheck error: %v", err)
		}
		text := out.String()
		if !strings.Contains(text, "config: defaults") || !strings.Contains(text, "self-check ok") {
			t.Fatalf("unexpected self-check output:\n%s", text)
		}
		cfgDir := filepath.Join(fakeHome, ".config", "clavis")
		if _, err := os.Stat(cfgDir); !os.IsNotExist(err) {
			t.Fatalf("self-check must not create missing config dir %s, stat err=%v", cfgDir, err)
		}
	})

	t.Run("empty config dir stays empty", func(t *testing.T) {
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("USERPROFILE", fakeHome)

		cfgDir := filepath.Join(fakeHome, ".config", "clavis")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		var out bytes.Buffer
		if err := runSelfCheck(&out); err != nil {
			t.Fatalf("runSelfCheck error: %v", err)
		}
		text := out.String()
		if !strings.Contains(text, "config: defaults") || !strings.Contains(text, "self-check ok") {
			t.Fatalf("unexpected self-check output:\n%s", text)
		}
		entries, err := os.ReadDir(cfgDir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) != 0 {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			t.Fatalf("self-check must remain read-only on an empty config dir, created files: %v", names)
		}
	})

	t.Run("partial config dir is loaded read-only without creating missing files", func(t *testing.T) {
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("USERPROFILE", fakeHome)

		cfgDir := filepath.Join(fakeHome, ".config", "clavis")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		mainCfg := config.DefaultMainConfig()
		mainCfg.InputEngine.Enabled = true
		mainCfg.InputEngine.Scheme = "xiaohe"
		if err := os.WriteFile(filepath.Join(cfgDir, string(config.FileMain)), []byte(mainCfg.Encode()), 0o644); err != nil {
			t.Fatalf("WriteFile config.toml: %v", err)
		}

		var out bytes.Buffer
		if err := runSelfCheck(&out); err != nil {
			t.Fatalf("runSelfCheck error: %v", err)
		}
		text := out.String()
		if !strings.Contains(text, "config: loaded") {
			t.Fatalf("expected loaded partial config, got:\n%s", text)
		}
		if !strings.Contains(text, `scheme="xiaohe"`) {
			t.Fatalf("expected xiaohe scheme in output, got:\n%s", text)
		}
		if !strings.Contains(text, `typing "nihc" -> `) || !strings.Contains(text, "你好") {
			t.Fatalf("expected xiaohe probe to produce 你好, got:\n%s", text)
		}
		entries, err := os.ReadDir(cfgDir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != string(config.FileMain) {
			t.Fatalf("self-check must not create missing sibling files, entries=%v", entries)
		}
	})
}

func TestProbeInputEngineSupportsAllSchemes(t *testing.T) {
	dict, _ := inputengine.LoadUserDictionary()
	schemes := []struct {
		scheme  string
		wantSeq string
	}{
		{"", "nihao"},
		{"quanpin", "nihao"},
		{"xiaohe", "nihc"},
		{"ziranma", "nihc"},
		{"microsoft", "nihk"},
		{"sogou", "nihk"},
		{"pinyin_plus", "nihk"},
	}
	for _, tc := range schemes {
		t.Run("scheme_"+tc.scheme, func(t *testing.T) {
			ie := config.DefaultInputEngineConfig()
			ie.Scheme = tc.scheme
			var out bytes.Buffer
			probeInputEngine(&out, ie, dict)
			got := out.String()
			if !strings.Contains(got, `typing "`+tc.wantSeq+`" -> `) {
				t.Fatalf("probe output = %q, want sequence %q", got, tc.wantSeq)
			}
			if !strings.Contains(got, "你好") {
				t.Fatalf("scheme %q probe failed to produce 你好: %q", tc.scheme, got)
			}
		})
	}
}
