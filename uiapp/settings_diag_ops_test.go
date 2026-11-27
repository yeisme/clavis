package uiapp

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/diag"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestSettingsDiagSelfOpsActionsAndSecretFreeBundle(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)

	for _, lang := range []i18n.Language{i18n.English, i18n.Chinese} {
		i18n.SetLanguage(lang)
		ctrl, _, _, _ := newTestController(t, nil)
		base := t.TempDir()
		dataDir := filepath.Join(base, "data")
		logDir := filepath.Join(base, "logs")
		ctrl.mu.Lock()
		ctrl.dataDir = dataDir
		ctrl.logDir = logDir
		ctrl.mu.Unlock()

		var openedDirs []string
		var revealed []string
		ctrl.OpenPath = func(p string) error { openedDirs = append(openedDirs, p); return nil }
		ctrl.RevealPath = func(p string) error { revealed = append(revealed, p); return nil }

		secret := "sk-ui-diag-secret-778899"
		if err := ctrl.SaveAPIKey(secret); err != nil {
			t.Fatalf("SaveAPIKey: %v", err)
		}
		if err := os.MkdirAll(logDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(diag.LatestCrashLogPath(logDir), []byte("panic: sample\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		ctrl.setSettingsPage("permissions")
		tt := ui.NewTester(ctrl.SettingsView, 960, 760)
		tt.Frame()

		if !tt.HasText(i18n.T("settings.diag.recent_crash")) {
			t.Fatalf("lang=%s expected recent crash row in diagnostics card", lang)
		}
		for _, btnKey := range []string{"settings.diag.export_bundle", "settings.diag.open_logs", "settings.diag.backup_now"} {
			if !tt.HasText(i18n.T(btnKey)) {
				t.Fatalf("lang=%s missing diagnostic button %q", lang, i18n.T(btnKey))
			}
		}

		if err := tt.Click(i18n.T("settings.diag.open_logs")); err != nil {
			t.Fatalf("click open logs: %v", err)
		}
		if len(openedDirs) != 1 || openedDirs[0] != logDir {
			t.Fatalf("openedDirs = %v, want [%s]", openedDirs, logDir)
		}

		// Click Export Diagnostic Bundle (async goroutine) and verify ZIP has zero secrets.
		if err := tt.Click(i18n.T("settings.diag.export_bundle")); err != nil {
			t.Fatalf("click export bundle: %v", err)
		}
		var zipPath string
		waitUntil(t, 2*time.Second, func() bool {
			entries, _ := filepath.Glob(filepath.Join(logDir, "clavis-diag-*.zip"))
			if len(entries) > 0 {
				zipPath = entries[0]
				return true
			}
			return false
		}, "diagnostic bundle written")

		zr, err := zip.OpenReader(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			rc, _ := f.Open()
			raw, _ := io.ReadAll(rc)
			_ = rc.Close()
			if strings.Contains(string(raw), secret) {
				_ = zr.Close()
				t.Fatalf("bundle leaked API key in %s", f.Name)
			}
		}
		_ = zr.Close()

		// Click Back Up Config & Data and verify manifest.json created.
		if err := tt.Click(i18n.T("settings.diag.backup_now")); err != nil {
			t.Fatalf("click backup now: %v", err)
		}
		waitUntil(t, 2*time.Second, func() bool {
			entries, _ := filepath.Glob(filepath.Join(dataDir, "backups", "backup-*", "manifest.json"))
			return len(entries) > 0
		}, "backup manifest written")

		tt.Frame()
		if len(revealed) < 2 {
			t.Fatalf("expected RevealLocalPath called for bundle & backup, got %v", revealed)
		}
	}
}
