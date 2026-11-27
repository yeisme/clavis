package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeisme/clavis/internal/diag"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
)

func TestCLIDoctorDiagBackupRestoreAndUninstall(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "missing-cfg")
	dataDir := filepath.Join(base, "missing-data")
	logDir := filepath.Join(base, "logs")
	t.Setenv(diag.EnvLogDir, logDir)
	t.Setenv(diag.EnvDataDir, dataDir)

	// 1. doctor --json stays strictly read-only on missing configDir/dataDir.
	var out, errBuf bytes.Buffer
	if err := Run([]string{"doctor", "--config-dir", cfgDir, "--data-dir", dataDir, "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("doctor --json failed: %v stderr=%s", err, errBuf.String())
	}
	var report diag.DoctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal doctor report: %v\n%s", err, out.String())
	}
	if _, statErr := os.Stat(cfgDir); !os.IsNotExist(statErr) {
		t.Fatal("doctor created missing config dir")
	}

	// 2. Seed config + secret and run diag export -o <zip>.
	store := config.NewConfigStore(cfgDir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	apiSecret := "sk-cli-diag-secret-998877"
	if err := secrets.NewFileStore(secrets.DefaultKeysPath(cfgDir)).SaveKey(store.Current().Main.AI.ActiveProfile().KeychainAccount, apiSecret); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(base, "diag.zip")
	out.Reset()
	if err := Run([]string{"diag", "export", "-o", zipPath, "--config-dir", cfgDir, "--data-dir", dataDir, "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("diag export failed: %v", err)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, openErr := f.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		raw, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.Contains(string(raw), apiSecret) {
			_ = zr.Close()
			t.Fatalf("diag zip leaked api secret in %s", f.Name)
		}
	}
	_ = zr.Close()

	// 3. backup & restore fail-closed on tamper & clean restore.
	backupDir := filepath.Join(base, "backup")
	out.Reset()
	if err := Run([]string{"backup", "--out", backupDir, "--config-dir", cfgDir, "--data-dir", dataDir, "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	dstCfg := filepath.Join(base, "dst-cfg")
	dstData := filepath.Join(base, "dst-data")
	mainTOML := filepath.Join(backupDir, "config", "config.toml")
	origTOML, err := os.ReadFile(mainTOML)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainTOML, append(append([]byte(nil), origTOML...), []byte("\n# tampered\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run([]string{"restore", "--from", backupDir, "--config-dir", dstCfg, "--data-dir", dstData}, &out, &errBuf); err == nil {
		t.Fatal("restore accepted tampered config.toml")
	}
	if err := os.WriteFile(mainTOML, origTOML, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run([]string{"restore", "--from", backupDir, "--config-dir", dstCfg, "--data-dir", dstData, "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstCfg, "config.toml")); err != nil {
		t.Fatalf("restored config.toml missing: %v", err)
	}

	// 4. uninstall --dry-run preserves dstCfg.
	prevLA := uninstallLaunchAgentFn
	uninstallLaunchAgentFn = func() error { return nil }
	t.Cleanup(func() { uninstallLaunchAgentFn = prevLA })
	out.Reset()
	if err := Run([]string{"uninstall", "--purge-data", "--dry-run", "--config-dir", dstCfg, "--data-dir", dstData, "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("uninstall --dry-run failed: %v", err)
	}
	if _, err := os.Stat(dstCfg); err != nil {
		t.Fatalf("uninstall --dry-run removed config dir: %v", err)
	}
}
