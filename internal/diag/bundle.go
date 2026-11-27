package diag

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// DoctorOptions controls read-only system & config inspection.
type DoctorOptions struct {
	ConfigDir        string
	DataDir          string
	LogDir           string
	ProbePermissions bool
	PermissionsProbe func(ctx context.Context) (host.PermissionStatus, error)
}

// DBSummary describes one SQLite database file without exposing any row payloads.
type DBSummary struct {
	Name          string         `json:"name"`
	Path          string         `json:"path"`
	Exists        bool           `json:"exists"`
	SizeBytes     int64          `json:"sizeBytes,omitempty"`
	SchemaVersion int            `json:"schemaVersion,omitempty"`
	TableCounts   map[string]int `json:"tableCounts,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// PermissionSummary captures host permission state.
type PermissionSummary struct {
	Probed          bool   `json:"probed"`
	Accessibility   bool   `json:"accessibility"`
	InputMonitoring bool   `json:"inputMonitoring"`
	ScreenRecording bool   `json:"screenRecording"`
	Granted         bool   `json:"granted"`
	Error           string `json:"error,omitempty"`
}

// LaunchAgentSummary captures macOS LaunchAgent installation state.
type LaunchAgentSummary struct {
	Supported bool   `json:"supported"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

// AISummary captures non-secret active AI profile and credential origin metadata.
type AISummary struct {
	ActiveProfile   string   `json:"activeProfile"`
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	KeySource       string   `json:"keySource"`
	ProfileCount    int      `json:"profileCount"`
	ConfiguredHosts []string `json:"configuredHosts,omitempty"`
}

// LogSummary summarizes main log and latest crash log presence.
type LogSummary struct {
	LogDir          string `json:"logDir"`
	MainLogExists   bool   `json:"mainLogExists"`
	MainLogBytes    int64  `json:"mainLogBytes,omitempty"`
	RecentCrash     bool   `json:"recentCrash"`
	CrashLogPath    string `json:"crashLogPath,omitempty"`
	CrashModifiedAt string `json:"crashModifiedAt,omitempty"`
}

// DoctorReport is the structured, secret-free diagnostic report.
type DoctorReport struct {
	GeneratedAt    string             `json:"generatedAt"`
	OS             string             `json:"os"`
	Arch           string             `json:"arch"`
	GoVersion      string             `json:"goVersion"`
	ConfigDir      string             `json:"configDir"`
	DataDir        string             `json:"dataDir"`
	ConfigLoaded   bool               `json:"configLoaded"`
	ConfigErrors   []string           `json:"configErrors,omitempty"`
	ConfigWarnings []string           `json:"configWarnings,omitempty"`
	Permissions    PermissionSummary  `json:"permissions"`
	LaunchAgent    LaunchAgentSummary `json:"launchAgent"`
	AI             AISummary          `json:"ai"`
	Logs           LogSummary         `json:"logs"`
	Databases      []DBSummary        `json:"databases"`
	Healthy        bool               `json:"healthy"`
}

// BuildDoctorReport runs a strictly read-only inspection without mutating config or DB dirs.
func BuildDoctorReport(ctx context.Context, opts DoctorOptions) DoctorReport {
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
	logDir := strings.TrimSpace(opts.LogDir)
	if logDir == "" {
		logDir = DefaultLogDir()
	}

	cfg, loaded, cfgErrs, cfgWarns := loadConfigReadOnly(cfgDir)
	report := DoctorReport{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		GoVersion:    runtime.Version(),
		ConfigDir:    RedactPaths(cfgDir),
		DataDir:      RedactPaths(dataDir),
		ConfigLoaded: loaded,
		AI:           buildAISummary(cfg, cfgDir),
		Logs:         buildLogSummary(logDir),
	}
	for _, e := range cfgErrs {
		report.ConfigErrors = append(report.ConfigErrors, RedactPaths(RedactText(e.Error())))
	}
	for _, w := range cfgWarns {
		report.ConfigWarnings = append(report.ConfigWarnings, RedactPaths(RedactText(fmt.Sprintf("%s:%s: %s", w.File, w.KeyPath, w.Message))))
	}

	if runtime.GOOS == "darwin" {
		laPath, _ := bridge.LaunchAgentPath()
		report.LaunchAgent = LaunchAgentSummary{
			Supported: true,
			Installed: bridge.LaunchAgentInstalled(),
			Path:      RedactPaths(laPath),
		}
	}

	if opts.PermissionsProbe != nil {
		report.Permissions.Probed = true
		st, err := opts.PermissionsProbe(ctx)
		if err != nil {
			report.Permissions.Error = RedactPaths(RedactText(err.Error()))
		} else {
			report.Permissions.Accessibility = st.Accessibility
			report.Permissions.InputMonitoring = st.InputMonitoring
			report.Permissions.ScreenRecording = st.ScreenRecording
			report.Permissions.Granted = st.Granted
		}
	} else if opts.ProbePermissions {
		report.Permissions.Probed = true
		br := bridge.NewHostService(
			bridge.WithTriggerConfig(cfg.Main.Trigger),
			bridge.WithKeymapConfig(cfg.Main.Keymap),
			bridge.WithIMEConfig(cfg.Main.IME, cfg.IMERules),
		)
		st, err := br.Permissions(ctx)
		if err != nil {
			report.Permissions.Error = RedactPaths(RedactText(err.Error()))
		} else {
			report.Permissions.Accessibility = st.Accessibility
			report.Permissions.InputMonitoring = st.InputMonitoring
			report.Permissions.ScreenRecording = st.ScreenRecording
			report.Permissions.Granted = st.Granted
		}
	}

	type dbTarget struct {
		name string
		path string
	}
	targets := []dbTarget{
		{name: "clipboard.db", path: filepath.Join(dataDir, "clipboard.db")},
		{name: "device_state.db", path: filepath.Join(dataDir, "device_state.db")},
		{name: "sync-state.db", path: filepath.Join(cfgDir, "sync-state.db")},
	}
	for _, tgt := range targets {
		report.Databases = append(report.Databases, inspectSQLiteSummaryReadOnly(ctx, tgt.name, tgt.path))
	}

	healthy := len(report.ConfigErrors) == 0 && !report.Logs.RecentCrash
	for _, db := range report.Databases {
		if db.Error != "" {
			healthy = false
			break
		}
	}
	report.Healthy = healthy
	return report
}

func buildAISummary(cfg config.ClavisConfig, cfgDir string) AISummary {
	active := cfg.Main.AI.ActiveProfile()
	keySrc := secrets.KeySourceNone
	keysPath := secrets.DefaultKeysPath(cfgDir)
	if _, err := os.Stat(keysPath); err == nil {
		if resolved, resolveErr := secrets.NewFileStore(keysPath).ResolveAPIKeySource(active.KeychainAccount); resolveErr == nil {
			keySrc = resolved
		}
	}
	hosts := make([]string, 0, len(cfg.Main.AI.Profiles))
	seen := map[string]bool{}
	for _, p := range cfg.Main.AI.Profiles {
		if u, err := url.Parse(strings.TrimSpace(p.BaseURL)); err == nil && u.Host != "" && !seen[u.Host] {
			seen[u.Host] = true
			hosts = append(hosts, u.Host)
		}
	}
	return AISummary{
		ActiveProfile:   active.ID,
		Provider:        active.Provider,
		Model:           active.Model,
		KeySource:       string(keySrc),
		ProfileCount:    len(cfg.Main.AI.Profiles),
		ConfiguredHosts: hosts,
	}
}

func buildLogSummary(logDir string) LogSummary {
	sum := LogSummary{LogDir: RedactPaths(logDir)}
	if info, err := os.Stat(MainLogPath(logDir)); err == nil && !info.IsDir() {
		sum.MainLogExists = true
		sum.MainLogBytes = info.Size()
	}
	if crashPath, modAt, ok := HasRecentCrash(logDir); ok {
		sum.RecentCrash = true
		sum.CrashLogPath = RedactPaths(crashPath)
		sum.CrashModifiedAt = modAt.UTC().Format(time.RFC3339)
	}
	return sum
}

func inspectSQLiteSummaryReadOnly(ctx context.Context, name, dbPath string) DBSummary {
	sum := DBSummary{Name: name, Path: RedactPaths(dbPath)}
	info, err := os.Stat(dbPath)
	if err != nil {
		return sum
	}
	sum.Exists = true
	sum.SizeBytes = info.Size()

	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		sum.Error = RedactPaths(RedactText(err.Error()))
		return sum
	}
	defer db.Close()

	if rows, qErr := db.Query(ctx, `PRAGMA user_version`); qErr == nil && len(rows.Values) > 0 && len(rows.Values[0]) > 0 {
		switch v := rows.Values[0][0].(type) {
		case int64:
			sum.SchemaVersion = int(v)
		case int:
			sum.SchemaVersion = v
		}
	}
	tblRows, err := db.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		sum.Error = RedactPaths(RedactText(err.Error()))
		return sum
	}
	counts := make(map[string]int)
	for _, r := range tblRows.Values {
		if len(r) == 0 {
			continue
		}
		tbl, ok := r[0].(string)
		if !ok || !isSafeIdentifier(tbl) {
			continue
		}
		// Skip FTS5 internal shadow tables and only read COUNT(*) never row contents.
		if strings.Contains(tbl, "_fts_") {
			continue
		}
		cntRows, cntErr := db.Query(ctx, `SELECT COUNT(*) FROM "`+tbl+`"`)
		if cntErr != nil {
			continue
		}
		if len(cntRows.Values) > 0 && len(cntRows.Values[0]) > 0 {
			if n, ok := cntRows.Values[0][0].(int64); ok {
				counts[tbl] = int(n)
			}
		}
	}
	sum.TableCounts = counts
	return sum
}

func isSafeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

// BundleOptions configures scrubbed diagnostic ZIP bundle generation.
type BundleOptions struct {
	ConfigDir        string
	DataDir          string
	LogDir           string
	OutputPath       string
	MaxLogLines      int
	SelfCheckText    string
	ProbePermissions bool
	PermissionsProbe func(ctx context.Context) (host.PermissionStatus, error)
}

// BundleResult describes the generated diagnostic ZIP archive.
type BundleResult struct {
	OutputPath string       `json:"outputPath"`
	Entries    []string     `json:"entries"`
	SizeBytes  int64        `json:"sizeBytes"`
	Report     DoctorReport `json:"report"`
}

type configSummary struct {
	Language       string    `json:"language"`
	Theme          string    `json:"theme"`
	VisualPreset   string    `json:"visualPreset"`
	LeaderKey      string    `json:"leaderKey"`
	InputEngineOn  bool      `json:"inputEngineEnabled"`
	InputScheme    string    `json:"inputScheme"`
	ClipboardOn    bool      `json:"clipboardEnabled"`
	MediaCapture   bool      `json:"mediaCapture"`
	LeaderNodes    int       `json:"leaderNodes"`
	SnippetsCount  int       `json:"snippetsCount"`
	GlossaryCount  int       `json:"glossaryCount"`
	IMERulesCount  int       `json:"imeRulesCount"`
	WorkflowsCount int       `json:"workflowsCount"`
	PromptsCount   int       `json:"promptsCount"`
	QuickJumpCount int       `json:"quickJumpCount"`
	AI             AISummary `json:"ai"`
}

// ExportDiagnosticBundle writes a scrubbed, secret-free diagnostic ZIP archive atomically.
func ExportDiagnosticBundle(ctx context.Context, opts BundleOptions) (BundleResult, error) {
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
	logDir := strings.TrimSpace(opts.LogDir)
	if logDir == "" {
		logDir = DefaultLogDir()
	}
	outPath := strings.TrimSpace(opts.OutputPath)
	if outPath == "" {
		outPath = filepath.Join(logDir, fmt.Sprintf("clavis-diag-%s.zip", time.Now().UTC().Format("20060102-150405")))
	}
	maxLines := opts.MaxLogLines
	if maxLines <= 0 {
		maxLines = 200
	}

	report := BuildDoctorReport(ctx, DoctorOptions{
		ConfigDir:        cfgDir,
		DataDir:          dataDir,
		LogDir:           logDir,
		ProbePermissions: opts.ProbePermissions,
		PermissionsProbe: opts.PermissionsProbe,
	})
	cfg, _, _, _ := loadConfigReadOnly(cfgDir)
	summary := configSummary{
		Language:       cfg.Main.Appearance.Language,
		Theme:          cfg.Main.Appearance.Theme,
		VisualPreset:   cfg.Main.Appearance.VisualPreset,
		LeaderKey:      cfg.Main.Trigger.LeaderKey,
		InputEngineOn:  cfg.Main.InputEngine.Enabled,
		InputScheme:    cfg.Main.InputEngine.Scheme,
		ClipboardOn:    cfg.Main.Clipboard.Enabled,
		MediaCapture:   cfg.Main.Clipboard.MediaCapture,
		LeaderNodes:    len(cfg.Leader.Nodes),
		SnippetsCount:  len(cfg.Snippets.Items),
		GlossaryCount:  len(cfg.Glossary.Terms),
		IMERulesCount:  len(cfg.IMERules.Rules),
		WorkflowsCount: len(cfg.Workflows.Workflows),
		PromptsCount:   len(cfg.Prompts.Items),
		QuickJumpCount: len(cfg.QuickJump.Bookmarks),
		AI:             report.AI,
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var entries []string
	writeEntry := func(name string, content []byte) error {
		if IsSensitiveFileName(name) {
			return fmt.Errorf("refusing to include sensitive file %q in diagnostic bundle", name)
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(content); err != nil {
			return err
		}
		entries = append(entries, name)
		return nil
	}

	doctorJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return BundleResult{}, err
	}
	if err := writeEntry("doctor.json", []byte(RedactPaths(RedactText(string(doctorJSON)))+"\n")); err != nil {
		return BundleResult{}, err
	}
	summaryJSON, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return BundleResult{}, err
	}
	if err := writeEntry("config-summary.json", []byte(RedactPaths(RedactText(string(summaryJSON)))+"\n")); err != nil {
		return BundleResult{}, err
	}

	if lines, err := ReadTailLines(MainLogPath(logDir), maxLines); err == nil && len(lines) > 0 {
		if err := writeEntry("logs/clavis-tail.log", []byte(strings.Join(lines, "\n")+"\n")); err != nil {
			return BundleResult{}, err
		}
	}
	if crashLines, err := ReadTailLines(LatestCrashLogPath(logDir), maxLines); err == nil && len(crashLines) > 0 {
		if err := writeEntry("logs/crash-latest.log", []byte(strings.Join(crashLines, "\n")+"\n")); err != nil {
			return BundleResult{}, err
		}
	}
	if strings.TrimSpace(opts.SelfCheckText) != "" {
		if err := writeEntry("selfcheck.txt", []byte(RedactPaths(RedactText(opts.SelfCheckText))+"\n")); err != nil {
			return BundleResult{}, err
		}
	}

	if err := zw.Close(); err != nil {
		return BundleResult{}, err
	}
	zipBytes := buf.Bytes()
	if err := writeFileAtomic(outPath, zipBytes, 0o600); err != nil {
		return BundleResult{}, err
	}
	return BundleResult{
		OutputPath: outPath,
		Entries:    entries,
		SizeBytes:  int64(len(zipBytes)),
		Report:     report,
	}, nil
}

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
	var errs []config.LoadError
	readOptional := func(kind config.ConfigFileKind, apply func(string) error) {
		raw, err := os.ReadFile(filepath.Join(cfgDir, string(kind)))
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, config.LoadError{File: string(kind), Message: err.Error()})
			}
			return
		}
		if err := apply(string(raw)); err != nil {
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
	readOptional(config.FileIdentities, func(s string) error {
		v, err := config.DecodeIdentitiesConfig(s)
		if err == nil {
			cfg.Identities = v
		}
		return err
	})
	var warns []config.ValidationIssue
	for _, issue := range config.Validate(cfg) {
		if issue.Severity == config.SeverityError {
			errs = append(errs, config.LoadError{File: issue.File, KeyPath: issue.KeyPath, Message: issue.Message})
		} else {
			warns = append(warns, issue)
		}
	}
	if len(errs) > 0 {
		return config.DefaultConfig(), false, errs, warns
	}
	return cfg, true, nil, warns
}
