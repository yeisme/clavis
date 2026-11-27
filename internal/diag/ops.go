package diag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/config"
)

const backupManifestFile = "manifest.json"

// BackupFileEntry describes one file recorded in backup manifest.json.
type BackupFileEntry struct {
	TargetKind    string `json:"targetKind"` // "config" or "data"
	RelativePath  string `json:"relativePath"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
	IsSQLiteDB    bool   `json:"isSqliteDb,omitempty"`
	SchemaVersion int    `json:"schemaVersion,omitempty"`
}

// BackupManifest is the integrity-verified inventory written to manifest.json.
type BackupManifest struct {
	Version          int               `json:"version"`
	CreatedAt        string            `json:"createdAt"`
	OS               string            `json:"os"`
	Arch             string            `json:"arch"`
	AppSchemaVersion int               `json:"appSchemaVersion"`
	IncludeSecrets   bool              `json:"includeSecrets"`
	ExcludedSecrets  []string          `json:"excludedSecrets,omitempty"`
	Files            []BackupFileEntry `json:"files"`
}

// BackupOptions configures backup creation.
type BackupOptions struct {
	ConfigDir      string
	DataDir        string
	OutDir         string
	IncludeSecrets bool
}

// BackupResult summarizes a completed backup.
type BackupResult struct {
	BackupDir    string         `json:"backupDir"`
	ManifestPath string         `json:"manifestPath"`
	Manifest     BackupManifest `json:"manifest"`
}

// RestoreOptions configures restore verification and execution.
type RestoreOptions struct {
	BackupDir string
	ConfigDir string
	DataDir   string
	DryRun    bool
}

// RestoreResult summarizes restore validation and application.
type RestoreResult struct {
	BackupDir     string   `json:"backupDir"`
	DryRun        bool     `json:"dryRun"`
	VerifiedFiles int      `json:"verifiedFiles"`
	RestoredPaths []string `json:"restoredPaths"`
}

// UninstallOptions configures uninstall cleanup.
type UninstallOptions struct {
	ConfigDir            string
	DataDir              string
	LogDir               string
	CacheDir             string
	PurgeData            bool
	BackupDir            string
	DryRun               bool
	UninstallLaunchAgent func() error
}

// UninstallResult reports what uninstall did (or would do under DryRun).
type UninstallResult struct {
	DryRun       bool     `json:"dryRun"`
	PurgeData    bool     `json:"purgeData"`
	BackupDir    string   `json:"backupDir,omitempty"`
	Actions      []string `json:"actions"`
	RemovedPaths []string `json:"removedPaths,omitempty"`
	TCCGuidance  []string `json:"tccGuidance"`
}

// CreateBackup checkpoints SQLite WAL files and writes a SHA-256 verified backup directory.
func CreateBackup(ctx context.Context, opts BackupOptions) (BackupResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfgDir := strings.TrimSpace(opts.ConfigDir)
	if cfgDir == "" {
		cfgDir = DefaultConfigDir()
	}
	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	outDir := strings.TrimSpace(opts.OutDir)
	if outDir == "" {
		outDir = filepath.Join(dataDir, "backups", "backup-"+time.Now().UTC().Format("20060102-150405"))
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return BackupResult{}, fmt.Errorf("create backup dir: %w", err)
	}

	manifest := BackupManifest{
		Version:          1,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		AppSchemaVersion: storage.AppSchemaVersion,
		IncludeSecrets:   opts.IncludeSecrets,
	}

	copyRegular := func(kind, srcPath, rel string) error {
		info, err := os.Stat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		dstPath := filepath.Join(outDir, kind, rel)
		sum, n, err := copyFileAtomicSHA256(srcPath, dstPath, 0o600)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, BackupFileEntry{
			TargetKind:   kind,
			RelativePath: filepath.ToSlash(rel),
			SHA256:       sum,
			SizeBytes:    n,
		})
		return nil
	}

	for _, kind := range config.AllConfigFileKinds() {
		if err := copyRegular("config", filepath.Join(cfgDir, string(kind)), string(kind)); err != nil {
			return BackupResult{}, err
		}
	}
	if err := copyRegular("config", filepath.Join(cfgDir, "vault_refs.json"), "vault_refs.json"); err != nil {
		return BackupResult{}, err
	}

	// Secret files are opt-in only.
	secretTargets := []struct {
		kind string
		dir  string
		name string
	}{
		{"config", cfgDir, "ai-keys.toml"},
		{"config", cfgDir, "vault_secrets.enc"},
		{"config", cfgDir, "vault.key"},
	}
	for _, st := range secretTargets {
		srcPath := filepath.Join(st.dir, st.name)
		if _, err := os.Stat(srcPath); err != nil {
			continue
		}
		if !opts.IncludeSecrets {
			manifest.ExcludedSecrets = append(manifest.ExcludedSecrets, st.name)
			continue
		}
		if err := copyRegular(st.kind, srcPath, st.name); err != nil {
			return BackupResult{}, err
		}
	}

	// SQLite databases (WAL-checkpointed first).
	dbTargets := []struct {
		kind string
		dir  string
		name string
	}{
		{"data", dataDir, "clipboard.db"},
		{"data", dataDir, "device_state.db"},
		{"config", cfgDir, "sync-state.db"},
	}
	for _, dt := range dbTargets {
		srcPath := filepath.Join(dt.dir, dt.name)
		if info, err := os.Stat(srcPath); err != nil || info.IsDir() {
			continue
		}
		ver, err := checkpointSQLiteDB(ctx, srcPath)
		if err != nil {
			return BackupResult{}, fmt.Errorf("checkpoint %s: %w", dt.name, err)
		}
		dstPath := filepath.Join(outDir, dt.kind, dt.name)
		sum, n, err := copyFileAtomicSHA256(srcPath, dstPath, 0o600)
		if err != nil {
			return BackupResult{}, err
		}
		manifest.Files = append(manifest.Files, BackupFileEntry{
			TargetKind:    dt.kind,
			RelativePath:  dt.name,
			SHA256:        sum,
			SizeBytes:     n,
			IsSQLiteDB:    true,
			SchemaVersion: ver,
		})
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupResult{}, err
	}
	manifestPath := filepath.Join(outDir, backupManifestFile)
	if err := writeFileAtomic(manifestPath, append(manifestBytes, '\n'), 0o600); err != nil {
		return BackupResult{}, fmt.Errorf("write backup manifest: %w", err)
	}
	return BackupResult{
		BackupDir:    outDir,
		ManifestPath: manifestPath,
		Manifest:     manifest,
	}, nil
}

// RestoreBackup verifies SHA-256 checksums and SQLite integrity/schema fail-closed before restoring files.
func RestoreBackup(ctx context.Context, opts RestoreOptions) (RestoreResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	backupDir := strings.TrimSpace(opts.BackupDir)
	if backupDir == "" {
		return RestoreResult{}, errors.New("restore requires a backup directory")
	}
	cfgDir := strings.TrimSpace(opts.ConfigDir)
	if cfgDir == "" {
		cfgDir = DefaultConfigDir()
	}
	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}

	manifestBytes, err := os.ReadFile(filepath.Join(backupDir, backupManifestFile))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return RestoreResult{}, fmt.Errorf("parse backup manifest: %w", err)
	}
	if manifest.Version != 1 {
		return RestoreResult{}, fmt.Errorf("unsupported backup manifest version %d", manifest.Version)
	}
	if manifest.AppSchemaVersion > storage.AppSchemaVersion {
		return RestoreResult{}, fmt.Errorf("backup schema version %d exceeds supported %d", manifest.AppSchemaVersion, storage.AppSchemaVersion)
	}

	type plannedCopy struct {
		entry   BackupFileEntry
		srcPath string
		dstPath string
	}
	plans := make([]plannedCopy, 0, len(manifest.Files))
	for _, f := range manifest.Files {
		cleanRel := filepath.Clean(filepath.FromSlash(f.RelativePath))
		if cleanRel == "." || filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, "..") {
			return RestoreResult{}, fmt.Errorf("invalid backup relative path %q", f.RelativePath)
		}
		var dstBase string
		switch f.TargetKind {
		case "config":
			dstBase = cfgDir
		case "data":
			dstBase = dataDir
		default:
			return RestoreResult{}, fmt.Errorf("invalid backup target kind %q", f.TargetKind)
		}
		srcPath := filepath.Join(backupDir, f.TargetKind, cleanRel)
		dstPath := filepath.Join(dstBase, cleanRel)

		actualSum, actualSize, sumErr := fileSHA256(srcPath)
		if sumErr != nil {
			return RestoreResult{}, fmt.Errorf("verify %s: %w", f.RelativePath, sumErr)
		}
		if actualSum != f.SHA256 || actualSize != f.SizeBytes {
			return RestoreResult{}, fmt.Errorf("checksum mismatch for %s", f.RelativePath)
		}
		if f.IsSQLiteDB {
			if err := verifySQLiteFile(ctx, srcPath, f.RelativePath == "clipboard.db"); err != nil {
				return RestoreResult{}, fmt.Errorf("sqlite verification failed for %s: %w", f.RelativePath, err)
			}
		}
		plans = append(plans, plannedCopy{entry: f, srcPath: srcPath, dstPath: dstPath})
	}

	res := RestoreResult{
		BackupDir:     backupDir,
		DryRun:        opts.DryRun,
		VerifiedFiles: len(plans),
	}
	if opts.DryRun {
		for _, p := range plans {
			res.RestoredPaths = append(res.RestoredPaths, p.dstPath)
		}
		return res, nil
	}

	for _, p := range plans {
		if _, _, err := copyFileAtomicSHA256(p.srcPath, p.dstPath, 0o600); err != nil {
			return RestoreResult{}, fmt.Errorf("restore %s: %w", p.entry.RelativePath, err)
		}
		if p.entry.IsSQLiteDB {
			_ = os.Remove(p.dstPath + "-wal")
			_ = os.Remove(p.dstPath + "-shm")
		}
		res.RestoredPaths = append(res.RestoredPaths, p.dstPath)
	}
	return res, nil
}

// RunUninstall removes LaunchAgent and caches, optionally backs up and purges local data,
// and returns explicit TCC cleanup guidance for the user.
func RunUninstall(ctx context.Context, opts UninstallOptions) (UninstallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfgDir := nonEmpty(opts.ConfigDir, DefaultConfigDir())
	dataDir := nonEmpty(opts.DataDir, DefaultDataDir())
	logDir := nonEmpty(opts.LogDir, DefaultLogDir())
	cacheDir := nonEmpty(opts.CacheDir, DefaultCacheDir())
	uninstallLA := opts.UninstallLaunchAgent
	if uninstallLA == nil {
		uninstallLA = bridge.UninstallLaunchAgent
	}

	res := UninstallResult{
		DryRun:    opts.DryRun,
		PurgeData: opts.PurgeData,
		TCCGuidance: []string{
			"Open System Settings -> Privacy & Security and remove Clavis from Accessibility, Input Monitoring, and Screen Recording.",
			"Optional CLI reset: tccutil reset All com.yeisme.clavis",
		},
	}

	if runtime.GOOS == "darwin" || opts.UninstallLaunchAgent != nil {
		if opts.DryRun {
			res.Actions = append(res.Actions, "dry-run: uninstall LaunchAgent ("+bridge.LaunchAgentLabel+")")
		} else {
			if err := uninstallLA(); err != nil && !errors.Is(err, errors.ErrUnsupported) {
				return res, fmt.Errorf("uninstall LaunchAgent: %w", err)
			}
			res.Actions = append(res.Actions, "uninstalled LaunchAgent ("+bridge.LaunchAgentLabel+")")
		}
	}

	removeDir := func(label, dir string) error {
		if strings.TrimSpace(dir) == "" {
			return nil
		}
		if _, err := os.Stat(dir); err != nil {
			return nil
		}
		if opts.DryRun {
			res.Actions = append(res.Actions, fmt.Sprintf("dry-run: remove %s (%s)", label, RedactPaths(dir)))
			return nil
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", label, err)
		}
		res.RemovedPaths = append(res.RemovedPaths, dir)
		res.Actions = append(res.Actions, fmt.Sprintf("removed %s", label))
		return nil
	}

	if err := removeDir("cache directory", cacheDir); err != nil {
		return res, err
	}
	if opts.PurgeData {
		if strings.TrimSpace(opts.BackupDir) != "" {
			if opts.DryRun {
				res.BackupDir = opts.BackupDir
				res.Actions = append(res.Actions, "dry-run: create backup before purge at "+RedactPaths(opts.BackupDir))
			} else {
				bRes, err := CreateBackup(ctx, BackupOptions{
					ConfigDir: cfgDir,
					DataDir:   dataDir,
					OutDir:    opts.BackupDir,
				})
				if err != nil {
					return res, fmt.Errorf("pre-purge backup: %w", err)
				}
				res.BackupDir = bRes.BackupDir
				res.Actions = append(res.Actions, "created pre-purge backup")
			}
		}
		for _, target := range []struct{ label, dir string }{
			{"config directory", cfgDir},
			{"data directory", dataDir},
			{"logs directory", logDir},
		} {
			if err := removeDir(target.label, target.dir); err != nil {
				return res, err
			}
		}
	} else {
		res.Actions = append(res.Actions, "retained config, data, and logs (pass --purge-data to remove)")
	}
	return res, nil
}

func checkpointSQLiteDB(ctx context.Context, dbPath string) (int, error) {
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		return 0, err
	}
	defer db.Close()

	if rows, qErr := db.Query(ctx, `PRAGMA journal_mode`); qErr == nil && len(rows.Values) > 0 && len(rows.Values[0]) > 0 {
		if mode, ok := rows.Values[0][0].(string); ok && strings.EqualFold(mode, "wal") {
			if cpRows, cpErr := db.Query(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); cpErr != nil {
				return 0, cpErr
			} else if len(cpRows.Values) > 0 && len(cpRows.Values[0]) > 0 {
				if busy, ok := cpRows.Values[0][0].(int64); ok && busy != 0 {
					return 0, errors.New("wal_checkpoint busy")
				}
			}
		}
	}
	ver := 0
	if rows, qErr := db.Query(ctx, `PRAGMA user_version`); qErr == nil && len(rows.Values) > 0 && len(rows.Values[0]) > 0 {
		if v, ok := rows.Values[0][0].(int64); ok {
			ver = int(v)
		}
	}
	return ver, nil
}

func verifySQLiteFile(ctx context.Context, dbPath string, checkAppSchema bool) error {
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	if len(rows.Values) == 0 || len(rows.Values[0]) == 0 {
		return errors.New("integrity_check returned no rows")
	}
	if status, ok := rows.Values[0][0].(string); !ok || !strings.EqualFold(status, "ok") {
		return fmt.Errorf("integrity_check failed: %v", rows.Values[0][0])
	}
	if checkAppSchema {
		vRows, err := db.Query(ctx, `PRAGMA user_version`)
		if err != nil {
			return err
		}
		if len(vRows.Values) > 0 && len(vRows.Values[0]) > 0 {
			if v, ok := vRows.Values[0][0].(int64); ok && int(v) > storage.AppSchemaVersion {
				return fmt.Errorf("schema version %d exceeds supported %d", v, storage.AppSchemaVersion)
			}
		}
	}
	return nil
}

func copyFileAtomicSHA256(srcPath, dstPath string, perm os.FileMode) (string, int64, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", 0, err
	}
	defer src.Close()

	dir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dstPath)+".tmp.*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err != nil {
		cleanup()
		return "", 0, err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}
