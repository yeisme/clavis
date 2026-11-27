package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/diag"
)

var uninstallLaunchAgentFn = bridge.UninstallLaunchAgent

func runDoctorCommand(stdout, stderr io.Writer, configDir, dataDir string, jsonOutput bool, args []string) error {
	if len(args) != 0 {
		return usageError(stderr)
	}
	report := diag.BuildDoctorReport(context.Background(), diag.DoctorOptions{
		ConfigDir:        configDir,
		DataDir:          dataDir,
		LogDir:           diag.DefaultLogDir(),
		ProbePermissions: true,
	})
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintf(stdout, "Clavis doctor (%s/%s)\n", report.OS, report.Arch)
	fmt.Fprintf(stdout, "config dir: %s (loaded=%v errors=%d warnings=%d)\n",
		report.ConfigDir, report.ConfigLoaded, len(report.ConfigErrors), len(report.ConfigWarnings))
	fmt.Fprintf(stdout, "data dir: %s\n", report.DataDir)
	fmt.Fprintf(stdout, "permissions: ax=%v input=%v screen=%v granted=%v\n",
		report.Permissions.Accessibility, report.Permissions.InputMonitoring,
		report.Permissions.ScreenRecording, report.Permissions.Granted)
	fmt.Fprintf(stdout, "ai profile: %s (%s / %s) key_source=%s\n",
		report.AI.ActiveProfile, report.AI.Provider, report.AI.Model, report.AI.KeySource)
	fmt.Fprintf(stdout, "logs: dir=%s recent_crash=%v\n", report.Logs.LogDir, report.Logs.RecentCrash)
	for _, db := range report.Databases {
		fmt.Fprintf(stdout, "db: %s exists=%v schema=%d err=%q\n", db.Name, db.Exists, db.SchemaVersion, db.Error)
	}
	return nil
}

func runDiagCommand(stdout, stderr io.Writer, configDir, dataDir string, jsonOutput bool, args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return usageError(stderr)
	}
	var outPath string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "-o" || arg == "--output":
			if i+1 >= len(rest) || strings.TrimSpace(rest[i+1]) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("diag export: -o/--output requires a file path")}
			}
			outPath = rest[i+1]
			i++
		case strings.HasPrefix(arg, "--output="):
			outPath = strings.TrimPrefix(arg, "--output=")
			if strings.TrimSpace(outPath) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("diag export: --output requires a file path")}
			}
		default:
			return &cliExitError{code: exitCodeUsage, err: fmt.Errorf("diag export: unexpected argument %q", arg)}
		}
	}
	res, err := diag.ExportDiagnosticBundle(context.Background(), diag.BundleOptions{
		ConfigDir:        configDir,
		DataDir:          dataDir,
		LogDir:           diag.DefaultLogDir(),
		OutputPath:       outPath,
		ProbePermissions: true,
	})
	if err != nil {
		return &cliExitError{code: exitCodeTransport, err: err}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	_, err = fmt.Fprintf(stdout, "diagnostic bundle written: %s (%d bytes)\n", res.OutputPath, res.SizeBytes)
	return err
}

func runBackupCommand(stdout, _ io.Writer, configDir, dataDir string, jsonOutput bool, args []string) error {
	var outDir string
	includeSecrets := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--out":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("backup: --out requires a directory")}
			}
			outDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--out="):
			outDir = strings.TrimPrefix(arg, "--out=")
			if strings.TrimSpace(outDir) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("backup: --out requires a directory")}
			}
		case arg == "--include-secrets":
			includeSecrets = true
		default:
			return &cliExitError{code: exitCodeUsage, err: fmt.Errorf("backup: unexpected argument %q", arg)}
		}
	}
	res, err := diag.CreateBackup(context.Background(), diag.BackupOptions{
		ConfigDir:      configDir,
		DataDir:        dataDir,
		OutDir:         outDir,
		IncludeSecrets: includeSecrets,
	})
	if err != nil {
		return &cliExitError{code: exitCodeTransport, err: err}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	_, err = fmt.Fprintf(stdout, "backup written: %s (files=%d excluded_secrets=%d)\n",
		res.BackupDir, len(res.Manifest.Files), len(res.Manifest.ExcludedSecrets))
	return err
}

func runRestoreCommand(stdout, _ io.Writer, configDir, dataDir string, jsonOutput bool, args []string) error {
	var fromDir string
	dryRun := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--from":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("restore: --from requires a backup directory")}
			}
			fromDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--from="):
			fromDir = strings.TrimPrefix(arg, "--from=")
			if strings.TrimSpace(fromDir) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("restore: --from requires a backup directory")}
			}
		case arg == "--dry-run":
			dryRun = true
		default:
			return &cliExitError{code: exitCodeUsage, err: fmt.Errorf("restore: unexpected argument %q", arg)}
		}
	}
	if strings.TrimSpace(fromDir) == "" {
		return &cliExitError{code: exitCodeUsage, err: errors.New("restore requires --from DIR")}
	}
	res, err := diag.RestoreBackup(context.Background(), diag.RestoreOptions{
		BackupDir: fromDir,
		ConfigDir: configDir,
		DataDir:   dataDir,
		DryRun:    dryRun,
	})
	if err != nil {
		return &cliExitError{code: exitCodeProtocol, err: err}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	_, err = fmt.Fprintf(stdout, "restore ok (dry_run=%v verified_files=%d restored=%d)\n",
		res.DryRun, res.VerifiedFiles, len(res.RestoredPaths))
	return err
}

func runUninstallCommand(stdout, _ io.Writer, configDir, dataDir string, jsonOutput bool, args []string) error {
	purgeData := false
	dryRun := false
	var backupDir string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--purge-data":
			purgeData = true
		case arg == "--dry-run":
			dryRun = true
		case arg == "--backup-dir":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("uninstall: --backup-dir requires a directory")}
			}
			backupDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--backup-dir="):
			backupDir = strings.TrimPrefix(arg, "--backup-dir=")
			if strings.TrimSpace(backupDir) == "" {
				return &cliExitError{code: exitCodeUsage, err: errors.New("uninstall: --backup-dir requires a directory")}
			}
		default:
			return &cliExitError{code: exitCodeUsage, err: fmt.Errorf("uninstall: unexpected argument %q", arg)}
		}
	}
	res, err := diag.RunUninstall(context.Background(), diag.UninstallOptions{
		ConfigDir:            configDir,
		DataDir:              dataDir,
		LogDir:               diag.DefaultLogDir(),
		CacheDir:             diag.DefaultCacheDir(),
		PurgeData:            purgeData,
		BackupDir:            backupDir,
		DryRun:               dryRun,
		UninstallLaunchAgent: uninstallLaunchAgentFn,
	})
	if err != nil {
		return &cliExitError{code: exitCodeTransport, err: err}
	}
	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	for _, act := range res.Actions {
		fmt.Fprintln(stdout, act)
	}
	for _, guide := range res.TCCGuidance {
		fmt.Fprintln(stdout, guide)
	}
	return nil
}
