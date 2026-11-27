package diag

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestRollingLoggerRotationAndRedaction(t *testing.T) {
	logDir := t.TempDir()
	logger, err := NewRollingLogger(logDir, 180, 3)
	if err != nil {
		t.Fatalf("NewRollingLogger: %v", err)
	}
	defer logger.Close()

	home, _ := os.UserHomeDir()
	secret := "sk-proj-diagtestsecret123456"
	for i := 0; i < 12; i++ {
		if err := logger.Logf("info", "msg=%d api_key=%s home=%s/file.txt", i, secret, home); err != nil {
			t.Fatalf("Logf %d: %v", i, err)
		}
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(MainLogPath(logDir) + ".1"); err != nil {
		t.Fatalf("expected rotated .1 log file: %v", err)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(logDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret leaked in log file %s", e.Name())
		}
		if home != "" && strings.Contains(string(raw), home) {
			t.Fatalf("raw home path leaked in log file %s", e.Name())
		}
	}
}

func TestRecordPanicAndRecoverPanicScrubbed(t *testing.T) {
	logDir := t.TempDir()
	home, _ := os.UserHomeDir()
	secret := "sk-ant-panictest-987654321"
	var gotCrashPath string
	func() {
		defer RecoverPanic(logDir, "1.0.0", "unit-test", func(crashPath string) {
			gotCrashPath = crashPath
		})
		panic(fmt.Sprintf("boom bearer=Bearer %s dir=%s/Work", secret, home))
	}()

	if gotCrashPath == "" {
		t.Fatal("RecoverPanic callback did not run")
	}
	raw, err := os.ReadFile(gotCrashPath)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(raw)
	if strings.Contains(txt, secret) || (home != "" && strings.Contains(txt, home)) {
		t.Fatalf("crash log leaked secret or home path:\n%s", txt)
	}
	if !strings.Contains(txt, "component: unit-test") || !strings.Contains(txt, redactedMarker) {
		t.Fatalf("unexpected crash log format:\n%s", txt)
	}
}

func TestExportDiagnosticBundleZeroSecretBytes(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "cfg")
	dataDir := filepath.Join(base, "data")
	logDir := filepath.Join(base, "logs")
	store := config.NewConfigStore(cfgDir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}

	// Inject synthetic high-entropy secrets in every sensitive surface.
	apiSecret := "sk-bundle-neverleak-9988776655"
	vaultSecret := "vlt-bundle-neverleak-4433221100"
	snippetSecret := "SNIP_SECRET_BODY_DO_NOT_EXPORT_12345"
	clipSecret := "CLIPBOARD_SECRET_PAYLOAD_DO_NOT_EXPORT_67890"
	logSecret := "AIzaSyBundleLogSecret998877665544"

	cfg := store.Current()
	cfg.Snippets.Items = append(cfg.Snippets.Items, config.SnippetItem{
		ID: "sec-snip", Trigger: ";sec", Content: snippetSecret, Description: "sec",
	})
	if err := store.SaveSnippets(cfg.Snippets); err != nil {
		t.Fatal(err)
	}
	if err := secrets.NewFileStore(secrets.DefaultKeysPath(cfgDir)).SaveKey(cfg.Main.AI.ActiveProfile().KeychainAccount, apiSecret); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "vault_secrets.enc"), []byte(vaultSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "vault.key"), []byte("RAW_VAULT_MASTER_KEY_BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}

	clipStore, err := storage.NewSQLiteClipboardStore(context.Background(), filepath.Join(dataDir, "clipboard.db"), clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := clipStore.Record(context.Background(), clipboard.CaptureCandidate{
		Content:       clipSecret,
		SourceAppName: "Notes",
	}); err != nil {
		t.Fatal(err)
	}
	if err := clipStore.Close(); err != nil {
		t.Fatal(err)
	}

	logger, err := NewRollingLogger(logDir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = logger.Logf("error", "request failed with key %s", logSecret)
	_ = logger.Close()

	outZip := filepath.Join(base, "bundle.zip")
	res, err := ExportDiagnosticBundle(context.Background(), BundleOptions{
		ConfigDir:  cfgDir,
		DataDir:    dataDir,
		LogDir:     logDir,
		OutputPath: outZip,
		PermissionsProbe: func(context.Context) (host.PermissionStatus, error) {
			return host.PermissionStatus{Accessibility: true, InputMonitoring: true, ScreenRecording: true, Granted: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("ExportDiagnosticBundle: %v", err)
	}
	if !res.Report.Permissions.ScreenRecording || len(res.Entries) < 3 {
		t.Fatalf("unexpected bundle result: %+v", res)
	}

	zr, err := zip.OpenReader(outZip)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if IsSensitiveFileName(f.Name) {
			t.Fatalf("sensitive file included in bundle: %s", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range []string{apiSecret, vaultSecret, snippetSecret, clipSecret, logSecret, "RAW_VAULT_MASTER_KEY_BYTES"} {
			if strings.Contains(string(body), needle) {
				t.Fatalf("secret %q leaked in bundle entry %s", needle, f.Name)
			}
		}
	}
}

func TestBackupRestoreTamperFailClosedAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "src-cfg")
	dataDir := filepath.Join(base, "src-data")
	backupDir := filepath.Join(base, "backup")

	store := config.NewConfigStore(cfgDir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	if err := secrets.NewFileStore(secrets.DefaultKeysPath(cfgDir)).SaveKey("default", "sk-excluded-secret-123456"); err != nil {
		t.Fatal(err)
	}
	clipStore, err := storage.NewSQLiteClipboardStore(ctx, filepath.Join(dataDir, "clipboard.db"), clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "hello backup"}); err != nil {
		t.Fatal(err)
	}
	_ = clipStore.Close()

	bRes, err := CreateBackup(ctx, BackupOptions{ConfigDir: cfgDir, DataDir: dataDir, OutDir: backupDir})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if len(bRes.Manifest.ExcludedSecrets) != 1 || bRes.Manifest.ExcludedSecrets[0] != "ai-keys.toml" {
		t.Fatalf("expected ai-keys.toml excluded by default, got %+v", bRes.Manifest.ExcludedSecrets)
	}

	// Tamper backed-up clipboard.db and verify RestoreBackup fails closed without touching dst.
	dstCfg := filepath.Join(base, "dst-cfg")
	dstData := filepath.Join(base, "dst-data")
	backedUpDB := filepath.Join(backupDir, "data", "clipboard.db")
	origDB, err := os.ReadFile(backedUpDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backedUpDB, append(append([]byte(nil), origDB...), []byte("CORRUPT")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(ctx, RestoreOptions{BackupDir: backupDir, ConfigDir: dstCfg, DataDir: dstData}); err == nil {
		t.Fatal("expected RestoreBackup to reject tampered backup")
	}
	if _, statErr := os.Stat(dstCfg); !os.IsNotExist(statErr) {
		t.Fatal("failed restore created destination cfg dir")
	}

	// Restore untampered backup.
	if err := os.WriteFile(backedUpDB, origDB, 0o600); err != nil {
		t.Fatal(err)
	}
	rRes, err := RestoreBackup(ctx, RestoreOptions{BackupDir: backupDir, ConfigDir: dstCfg, DataDir: dstData})
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if rRes.VerifiedFiles == 0 || len(rRes.RestoredPaths) == 0 {
		t.Fatalf("unexpected RestoreResult: %+v", rRes)
	}
	reopened, err := storage.NewSQLiteClipboardStore(ctx, filepath.Join(dstData, "clipboard.db"), clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatalf("reopen restored clipboard.db: %v", err)
	}
	items, err := reopened.Recent(ctx, 10)
	_ = reopened.Close()
	if err != nil || len(items) != 1 || items[0].Content != "hello backup" {
		t.Fatalf("restored clipboard items = %+v err=%v", items, err)
	}
}

func TestRunUninstallDryRunAndPurgeWithBackup(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "cfg")
	dataDir := filepath.Join(base, "data")
	logDir := filepath.Join(base, "logs")
	cacheDir := filepath.Join(base, "cache")
	backupDir := filepath.Join(base, "uninstall-backup")

	store := config.NewConfigStore(cfgDir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dataDir, logDir, cacheDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "marker.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	laCalls := 0
	dryRes, err := RunUninstall(ctx, UninstallOptions{
		ConfigDir:            cfgDir,
		DataDir:              dataDir,
		LogDir:               logDir,
		CacheDir:             cacheDir,
		PurgeData:            true,
		BackupDir:            backupDir,
		DryRun:               true,
		UninstallLaunchAgent: func() error { laCalls++; return nil },
	})
	if err != nil {
		t.Fatalf("dry-run uninstall: %v", err)
	}
	if laCalls != 0 || len(dryRes.TCCGuidance) == 0 {
		t.Fatalf("unexpected dry-run result: calls=%d res=%+v", laCalls, dryRes)
	}
	if _, err := os.Stat(cfgDir); err != nil {
		t.Fatalf("dry-run deleted cfgDir: %v", err)
	}

	res, err := RunUninstall(ctx, UninstallOptions{
		ConfigDir:            cfgDir,
		DataDir:              dataDir,
		LogDir:               logDir,
		CacheDir:             cacheDir,
		PurgeData:            true,
		BackupDir:            backupDir,
		DryRun:               false,
		UninstallLaunchAgent: func() error { laCalls++; return nil },
	})
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if laCalls != 1 || res.BackupDir != backupDir {
		t.Fatalf("unexpected uninstall result: laCalls=%d res=%+v", laCalls, res)
	}
	for _, d := range []string{cfgDir, dataDir, logDir, cacheDir} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("expected %s purged, stat err=%v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(backupDir, backupManifestFile)); err != nil {
		t.Fatalf("pre-purge backup manifest missing: %v", err)
	}
}

func TestBuildDoctorReportDetectsEnvAPIKeyWithoutKeysFile(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "cfg")
	dataDir := filepath.Join(base, "data")
	logDir := filepath.Join(base, "logs")
	t.Setenv("DEEPSEEK_API_KEY", "sk-diag-env-dummy-key")
	report := BuildDoctorReport(context.Background(), DoctorOptions{
		ConfigDir: cfgDir,
		DataDir:   dataDir,
		LogDir:    logDir,
		PermissionsProbe: func(context.Context) (host.PermissionStatus, error) {
			return host.PermissionStatus{Accessibility: true, InputMonitoring: true, ScreenRecording: true, Granted: true}, nil
		},
	})
	if report.AI.KeySource != string(secrets.KeySourceEnv) {
		t.Fatalf("KeySource = %q, want %q", report.AI.KeySource, secrets.KeySourceEnv)
	}
	if _, err := os.Stat(cfgDir); !os.IsNotExist(err) {
		t.Fatalf("BuildDoctorReport must stay read-only, cfgDir stat err=%v", err)
	}
}
