// Command clavis provides a non-interactive view of the declarative Clavis
// configuration. It deliberately only validates and plans workflows: no
// provider, plugin, MCP, or external command is executed by this command.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yeisme/clavis/internal/diag"
	"github.com/yeisme/clavis/internal/secrets"
	internalsync "github.com/yeisme/clavis/internal/sync"
	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/ime"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

const (
	exitCodeOK        = 0
	exitCodeUsage     = 2
	exitCodeNotReady  = 3
	exitCodeTransport = 4
	exitCodeProtocol  = 5
)

type cliExitError struct {
	code int
	err  error
}

func (e *cliExitError) Error() string { return e.err.Error() }
func (e *cliExitError) Unwrap() error { return e.err }

// ExitCode maps a Run error to a deterministic CLI exit status:
//
//	0: success
//	2: usage / configuration error
//	3: not initialized / unauthorized / recovery code unconfirmed
//	4: retryable transport or storage error
//	5: protocol rejection (signature/version/class) or unresolved conflict
func ExitCode(err error) int {
	if err == nil {
		return exitCodeOK
	}
	var ce *cliExitError
	if errors.As(err, &ce) && ce.code > 0 {
		return ce.code
	}
	return exitCodeUsage
}

// Run executes the command using args and streams output to stdout/stderr.
// It is exported so callers and tests can use the CLI without starting a
// process. The command grammar is:
//
//	clavis action list [--config-dir DIR] [--json]
//	clavis workflow list [--config-dir DIR] [--json]
//	clavis workflow plan WORKFLOW [--config-dir DIR] [--json]
func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageError(stderr)
	}
	dataDir, strippedArgs, err := extractDataDirFlag(args)
	if err != nil {
		return err
	}
	configDir, jsonOutput, commandArgs, err := parseGlobalFlags(strippedArgs)
	if err != nil {
		return err
	}
	if len(commandArgs) > 0 && commandArgs[0] == "compile-dict" {
		return compileDict(stdout, stderr, commandArgs[1:])
	}
	if len(commandArgs) > 0 && commandArgs[0] == "sync" {
		return runSyncCommand(stdout, stderr, configDir, jsonOutput, commandArgs[1:])
	}
	if len(commandArgs) > 0 {
		switch commandArgs[0] {
		case "doctor":
			return runDoctorCommand(stdout, stderr, configDir, dataDir, jsonOutput, commandArgs[1:])
		case "diag":
			return runDiagCommand(stdout, stderr, configDir, dataDir, jsonOutput, commandArgs[1:])
		case "backup":
			return runBackupCommand(stdout, stderr, configDir, dataDir, jsonOutput, commandArgs[1:])
		case "restore":
			return runRestoreCommand(stdout, stderr, configDir, dataDir, jsonOutput, commandArgs[1:])
		case "uninstall":
			return runUninstallCommand(stdout, stderr, configDir, dataDir, jsonOutput, commandArgs[1:])
		}
	}
	if len(commandArgs) < 2 {
		return usageError(stderr)
	}

	store := config.NewConfigStore(configDir)
	cfg, err := store.LoadOrInitialize()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	switch commandArgs[0] {
	case "action":
		if commandArgs[1] != "list" || len(commandArgs) != 2 {
			return usageError(stderr)
		}
		return writeActions(stdout, cfg, jsonOutput)
	case "workflow":
		switch commandArgs[1] {
		case "list":
			if len(commandArgs) != 2 {
				return usageError(stderr)
			}
			return writeWorkflows(stdout, cfg, jsonOutput)
		case "plan":
			if len(commandArgs) != 3 || strings.TrimSpace(commandArgs[2]) == "" {
				return usageError(stderr)
			}
			return writeWorkflowPlan(stdout, cfg, commandArgs[2], jsonOutput)
		default:
			return usageError(stderr)
		}
	default:
		return usageError(stderr)
	}
}

func main() {
	if err := Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(ExitCode(err))
	}
}

func parseGlobalFlags(args []string) (configDir string, jsonOutput bool, commandArgs []string, err error) {
	configDir = defaultConfigDir()
	// Keep parsing intentionally small and deterministic. Flags are accepted
	// before or after the command, which is convenient for shell scripts.
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOutput = true
		case arg == "--config-dir":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", false, nil, errors.New("--config-dir requires a directory")
			}
			configDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--config-dir="):
			configDir = strings.TrimPrefix(arg, "--config-dir=")
			if strings.TrimSpace(configDir) == "" {
				return "", false, nil, errors.New("--config-dir requires a directory")
			}
		case arg == "-h" || arg == "--help":
			return "", false, nil, usageError(nil)
		default:
			commandArgs = append(commandArgs, arg)
		}
	}
	return configDir, jsonOutput, commandArgs, nil
}

func compileDict(stdout, stderr io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("compile-dict requires an input .dict.yaml path")
	}
	input := args[0]
	output := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-o", "--output":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return errors.New("compile-dict: -o/--output requires a path")
			}
			output = args[i+1]
			i++
		case "--output=":
			output = strings.TrimPrefix(args[i], "--output=")
		default:
			if strings.HasPrefix(args[i], "--output=") {
				output = strings.TrimPrefix(args[i], "--output=")
			} else {
				return fmt.Errorf("compile-dict: unexpected argument %q", args[i])
			}
		}
	}
	if strings.TrimSpace(output) == "" {
		return errors.New("compile-dict requires -o/--output")
	}
	lex, err := ime.CompileRimeDictFiles([]string{input}, output)
	if err != nil {
		return fmt.Errorf("compile-dict: %w", err)
	}
	if lex == nil {
		return errors.New("compile-dict: compiler returned no lexicon")
	}
	_, err = fmt.Fprintf(stdout, "words=%d bytes=%d output=%s\n", lex.Len(), len(lex.Bytes()), output)
	return err
}

func extractDataDirFlag(args []string) (string, []string, error) {
	dataDir := diag.DefaultDataDir()
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--data-dir":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", nil, errors.New("--data-dir requires a directory")
			}
			dataDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--data-dir="):
			dataDir = strings.TrimPrefix(arg, "--data-dir=")
			if strings.TrimSpace(dataDir) == "" {
				return "", nil, errors.New("--data-dir requires a directory")
			}
		default:
			rest = append(rest, arg)
		}
	}
	return dataDir, rest, nil
}

func defaultConfigDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "clavis")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "clavis")
	}
	return ".clavis"
}

func usageError(w io.Writer) error {
	err := errors.New("usage: clavis [--config-dir DIR] [--data-dir DIR] [--json] action list | workflow list | workflow plan WORKFLOW | compile-dict INPUT -o OUTPUT | sync status|plan|run | doctor | diag export [-o FILE] | backup [--out DIR] [--include-secrets] | restore --from DIR [--dry-run] | uninstall [--purge-data] [--backup-dir DIR] [--dry-run]")
	if w != nil {
		fmt.Fprintln(w, err)
	}
	return &cliExitError{code: exitCodeUsage, err: err}
}

type syncStatusOutput struct {
	Code              string               `json:"code"`
	Configured        bool                 `json:"configured"`
	Enabled           bool                 `json:"enabled"`
	RecoveryReady     bool                 `json:"recoveryReady"`
	Account           string               `json:"account,omitempty"`
	DeviceID          string               `json:"deviceId,omitempty"`
	Watermark         uint64               `json:"watermark"`
	PendingUploads    int                  `json:"pendingUploads"`
	PendingTombstones int                  `json:"pendingTombstones"`
	Conflicts         int                  `json:"conflicts"`
	Devices           int                  `json:"devices"`
	SyncDir           string               `json:"syncDir,omitempty"`
	DirExists         bool                 `json:"dirExists"`
	EnvelopeFiles     int                  `json:"envelopeFiles"`
	TombstoneFiles    int                  `json:"tombstoneFiles"`
	Allowlist         []coresync.DataClass `json:"allowlist"`
	BlockedClasses    []coresync.DataClass `json:"blockedClasses"`
}

type syncPlanOutput struct {
	Code           string               `json:"code"`
	Transport      string               `json:"transport"`
	Allowlist      []coresync.DataClass `json:"allowlist"`
	BlockedClasses []coresync.DataClass `json:"blockedClasses"`
	Items          []coresync.SyncItem  `json:"items"`
}

type syncRunOutput struct {
	Code               string               `json:"code"`
	SyncDir            string               `json:"syncDir"`
	IngestedEnvelopes  int                  `json:"ingestedEnvelopes"`
	IngestedTombstones int                  `json:"ingestedTombstones"`
	PulledObjects      int                  `json:"pulledObjects"`
	Conflicts          int                  `json:"conflicts"`
	Watermark          uint64               `json:"watermark"`
	Allowlist          []coresync.DataClass `json:"allowlist"`
	Error              string               `json:"error,omitempty"`
}

func runSyncCommand(stdout, stderr io.Writer, configDir string, jsonOutput bool, args []string) error {
	if len(args) == 0 {
		return usageError(stderr)
	}
	switch args[0] {
	case "status":
		return runSyncStatus(stdout, stderr, configDir, jsonOutput, args[1:])
	case "plan":
		if len(args) != 1 {
			return usageError(stderr)
		}
		return runSyncPlan(stdout, jsonOutput)
	case "run":
		return runSyncDir(stdout, stderr, configDir, jsonOutput, args[1:])
	default:
		return usageError(stderr)
	}
}

func parseDirFlag(args []string, required bool) (string, error) {
	var dir string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dir":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", &cliExitError{code: exitCodeUsage, err: errors.New("sync: --dir requires a directory path")}
			}
			dir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--dir="):
			dir = strings.TrimPrefix(arg, "--dir=")
			if strings.TrimSpace(dir) == "" {
				return "", &cliExitError{code: exitCodeUsage, err: errors.New("sync: --dir requires a directory path")}
			}
		default:
			return "", &cliExitError{code: exitCodeUsage, err: fmt.Errorf("sync: unexpected argument %q", arg)}
		}
	}
	if required && strings.TrimSpace(dir) == "" {
		return "", &cliExitError{code: exitCodeUsage, err: errors.New("sync run requires --dir DIR")}
	}
	return dir, nil
}

func runSyncStatus(stdout, _ io.Writer, configDir string, jsonOutput bool, args []string) error {
	dirFlag, err := parseDirFlag(args, false)
	if err != nil {
		return err
	}
	ctx := context.Background()
	statePath := filepath.Join(configDir, "sync-state.db")
	watermark, recoveryReady, meta, exists, err := internalsync.InspectStateFileReadOnly(ctx, statePath)
	if err != nil {
		return &cliExitError{code: exitCodeTransport, err: fmt.Errorf("sync status: read local state: %w", err)}
	}
	effectiveDir := strings.TrimSpace(dirFlag)
	if effectiveDir == "" {
		effectiveDir = strings.TrimSpace(meta.SyncDir)
	}
	if effectiveDir == "" {
		candidate := filepath.Join(configDir, "sync-dir")
		if info, statErr := os.Lstat(candidate); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			effectiveDir = candidate
		}
	}
	inspection, inspErr := internalsync.InspectDirReadOnly(effectiveDir)
	if inspErr != nil {
		return &cliExitError{code: exitCodeUsage, err: fmt.Errorf("sync status: inspect dir: %w", inspErr)}
	}
	allowlist := meta.AllowedClasses
	if len(allowlist) == 0 {
		allowlist = coresync.Phase1SyncPlan().AllowedClasses()
	}
	configured := exists && (strings.TrimSpace(meta.AccountID) != "" || recoveryReady || strings.TrimSpace(meta.SyncDir) != "")
	code := "not_configured"
	if configured {
		code = "ok"
	}
	out := syncStatusOutput{
		Code:              code,
		Configured:        configured,
		Enabled:           configured && recoveryReady,
		RecoveryReady:     recoveryReady,
		Account:           coresync.RedactIdentifier(meta.AccountID),
		DeviceID:          coresync.RedactIdentifier(meta.DeviceID),
		Watermark:         watermark,
		PendingUploads:    meta.PendingUploads,
		PendingTombstones: meta.PendingTombstones,
		Conflicts:         meta.Conflicts,
		Devices:           meta.Devices,
		SyncDir:           effectiveDir,
		DirExists:         inspection.Exists,
		EnvelopeFiles:     inspection.EnvelopeFiles,
		TombstoneFiles:    inspection.TombstoneFiles,
		Allowlist:         allowlist,
		BlockedClasses:    []coresync.DataClass{coresync.Passwords, coresync.KeyManager},
	}
	if jsonOutput {
		return encodeJSON(stdout, out)
	}
	_, err = fmt.Fprintf(
		stdout,
		"configured=%t\tenabled=%t\trecovery_ready=%t\twatermark=%d\tconflicts=%d\tallowlist=%s\n",
		out.Configured,
		out.Enabled,
		out.RecoveryReady,
		out.Watermark,
		out.Conflicts,
		joinClasses(out.Allowlist),
	)
	return err
}

func runSyncPlan(stdout io.Writer, jsonOutput bool) error {
	plan := coresync.Phase1SyncPlan()
	out := syncPlanOutput{
		Code:           "ok",
		Transport:      "dir",
		Allowlist:      plan.AllowedClasses(),
		BlockedClasses: []coresync.DataClass{coresync.Passwords, coresync.KeyManager},
		Items:          plan.Items(),
	}
	if jsonOutput {
		return encodeJSON(stdout, out)
	}
	for _, item := range out.Items {
		status := "disabled"
		if item.Class == coresync.Passwords || item.Class == coresync.KeyManager {
			status = "forbidden"
		} else if item.Allowed {
			status = "allowed"
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\n", item.Class, status); err != nil {
			return err
		}
	}
	return nil
}

func runSyncDir(stdout, _ io.Writer, configDir string, jsonOutput bool, args []string) error {
	dir, err := parseDirFlag(args, true)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(dir); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return &cliExitError{code: exitCodeUsage, err: fmt.Errorf("sync run: %s is not a safe directory", dir)}
		}
	} else if !os.IsNotExist(statErr) {
		return &cliExitError{code: exitCodeTransport, err: fmt.Errorf("sync run: stat %s: %w", dir, statErr)}
	}

	ctx := context.Background()
	allowlist := coresync.Phase1SyncPlan().AllowedClasses()
	statePath := filepath.Join(configDir, "sync-state.db")
	_, recoveryReady, meta, exists, err := internalsync.InspectStateFileReadOnly(ctx, statePath)
	if err != nil {
		return &cliExitError{code: exitCodeTransport, err: fmt.Errorf("sync run: read local state: %w", err)}
	}
	if !exists || strings.TrimSpace(meta.AccountID) == "" || strings.TrimSpace(meta.DeviceID) == "" {
		out := syncRunOutput{Code: "not_initialized", SyncDir: dir, Allowlist: allowlist}
		if jsonOutput {
			_ = encodeJSON(stdout, out)
		}
		return &cliExitError{code: exitCodeNotReady, err: errors.New("sync run: local sync state is not initialized")}
	}
	if !recoveryReady {
		out := syncRunOutput{Code: "recovery_unconfirmed", SyncDir: dir, Allowlist: allowlist}
		if jsonOutput {
			_ = encodeJSON(stdout, out)
		}
		return &cliExitError{code: exitCodeNotReady, err: errors.New("sync run: recovery code is not confirmed")}
	}

	keyStore := secrets.NewFileStore(secrets.DefaultKeysPath(configDir))
	tx, err := internalsync.OpenDirTransport(ctx, internalsync.DirTransportOptions{
		SyncDir:     dir,
		PrivateDir:  configDir,
		AccountID:   meta.AccountID,
		DeviceID:    meta.DeviceID,
		SecretStore: keyStore,
	})
	if err != nil {
		return &cliExitError{code: exitCodeTransport, err: fmt.Errorf("sync run: open dir transport: %w", err)}
	}
	defer tx.Close()

	report, err := tx.SyncOnce(ctx)
	if err != nil {
		code := exitCodeTransport
		statusCode := "transport_error"
		if isSyncProtocolError(err) {
			code = exitCodeProtocol
			statusCode = "protocol_rejected"
		}
		out := syncRunOutput{Code: statusCode, SyncDir: dir, Allowlist: allowlist, Watermark: report.Watermark, Conflicts: report.ConflictCount}
		if jsonOutput {
			_ = encodeJSON(stdout, out)
		}
		return &cliExitError{code: code, err: fmt.Errorf("sync run: %w", err)}
	}

	out := syncRunOutput{
		Code:               "ok",
		SyncDir:            dir,
		IngestedEnvelopes:  report.IngestedEnvelopes,
		IngestedTombstones: report.IngestedTombstones,
		PulledObjects:      report.PulledCount,
		Conflicts:          report.ConflictCount,
		Watermark:          report.Watermark,
		Allowlist:          report.AllowedClasses,
	}
	if report.ConflictCount > 0 {
		out.Code = "conflict_detected"
	}
	if jsonOutput {
		if encErr := encodeJSON(stdout, out); encErr != nil {
			return encErr
		}
	} else {
		if _, fErr := fmt.Fprintf(
			stdout,
			"code=%s\tenvelopes=%d\ttombstones=%d\tpulled=%d\tconflicts=%d\twatermark=%d\n",
			out.Code,
			out.IngestedEnvelopes,
			out.IngestedTombstones,
			out.PulledObjects,
			out.Conflicts,
			out.Watermark,
		); fErr != nil {
			return fErr
		}
	}
	if report.ConflictCount > 0 {
		return &cliExitError{code: exitCodeProtocol, err: fmt.Errorf("sync run: %d conflict(s) require explicit resolution", report.ConflictCount)}
	}
	return nil
}

func isSyncProtocolError(err error) bool {
	if errors.Is(err, coresync.ErrClassForbidden) ||
		errors.Is(err, coresync.ErrClassNotAllowed) ||
		errors.Is(err, coresync.ErrStoreDeviceRevoked) ||
		errors.Is(err, coresync.ErrStoreConflict) ||
		errors.Is(err, coresync.ErrStoreVersion) ||
		errors.Is(err, coresync.ErrStorePlaintext) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "signature") || strings.Contains(msg, "invalid envelope") || strings.Contains(msg, "tombstone")
}

func joinClasses(classes []coresync.DataClass) string {
	parts := make([]string, 0, len(classes))
	for _, c := range classes {
		parts = append(parts, string(c))
	}
	return strings.Join(parts, ",")
}

// actionRecord is a deliberately stable, command-oriented projection of the
// provider-neutral automation descriptor.
type actionRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider"`
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
}

func writeActions(w io.Writer, cfg config.ClavisConfig, jsonOutput bool) error {
	actions := collectActions(cfg)
	if jsonOutput {
		return encodeJSON(w, actions)
	}
	for _, action := range actions {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", action.ID, action.Provider); err != nil {
			return err
		}
	}
	return nil
}

func collectActions(cfg config.ClavisConfig) []actionRecord {
	registry := automation.NewRegistry()
	var visit func([]config.LeaderNode, string)
	visit = func(nodes []config.LeaderNode, prefix string) {
		for _, node := range nodes {
			id := prefix + node.Key
			if node.Action.Type == config.ActionGroup {
				visit(node.Action.Children, id+".")
				continue
			}
			_ = registry.RegisterBuiltin(id, node.Action)
		}
	}
	visit(cfg.Leader.Nodes, "leader.")
	for _, script := range cfg.Components.Scripts {
		_ = registry.RegisterScript(script)
	}
	out := make([]actionRecord, 0, len(registry.List()))
	for _, descriptor := range registry.List() {
		out = append(out, actionRecord{
			ID: descriptor.ID, Name: descriptor.Name, Description: descriptor.Description,
			Provider: descriptor.Provider, Kind: string(descriptor.Kind), Enabled: descriptor.Script == nil || descriptor.Script.Enabled,
		})
	}
	return out
}

func writeWorkflows(w io.Writer, cfg config.ClavisConfig, jsonOutput bool) error {
	workflows := append([]config.Workflow(nil), cfg.Workflows.Workflows...)
	sort.Slice(workflows, func(i, j int) bool { return workflows[i].ID < workflows[j].ID })
	if jsonOutput {
		return encodeJSON(w, workflows)
	}
	for _, workflow := range workflows {
		name := workflow.Name
		if name == "" {
			name = workflow.ID
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\n", workflow.ID, name); err != nil {
			return err
		}
	}
	return nil
}

func writeWorkflowPlan(w io.Writer, cfg config.ClavisConfig, id string, jsonOutput bool) error {
	var workflow *config.Workflow
	for i := range cfg.Workflows.Workflows {
		if cfg.Workflows.Workflows[i].ID == id {
			workflow = &cfg.Workflows.Workflows[i]
			break
		}
	}
	if workflow == nil {
		return fmt.Errorf("workflow %q not found", id)
	}
	if !workflow.Enabled {
		return fmt.Errorf("workflow %q is disabled", id)
	}
	if err := validateWorkflowPlanConfig(*workflow); err != nil {
		return err
	}
	model := toAutomationWorkflow(*workflow)
	registry := actionRegistry(cfg)
	plan, err := automation.BuildExecutionPlanWithRegistry(model, registry)
	if err != nil {
		return err
	}
	if jsonOutput {
		return encodeJSON(w, plan)
	}
	for _, step := range plan {
		if _, err := fmt.Fprintln(w, step.ID); err != nil {
			return err
		}
	}
	return nil
}

func actionRegistry(cfg config.ClavisConfig) automation.MapActionRegistry {
	registry := make(automation.MapActionRegistry)
	var visit func([]config.LeaderNode, string)
	visit = func(nodes []config.LeaderNode, prefix string) {
		for _, node := range nodes {
			id := strings.TrimSpace(prefix + node.Key)
			if node.Action.Type == config.ActionGroup {
				visit(node.Action.Children, id+".")
				continue
			}
			// CLI is the only executable leader action supported by the workflow
			// runtime. Do not let planning advertise prompt/UI-only actions.
			if node.Action.Type != config.ActionCLI || strings.TrimSpace(node.Action.Command) == "" {
				continue
			}
			registry[id] = automation.ActionDescriptor{ID: id, Name: node.Label, Provider: automation.ProviderCLI, Risk: automation.RiskLow}
			short := strings.TrimPrefix(id, "leader.")
			if _, exists := registry[short]; !exists {
				registry[short] = automation.ActionDescriptor{ID: short, Name: node.Label, Provider: automation.ProviderCLI, Risk: automation.RiskLow}
			}
		}
	}
	visit(cfg.Leader.Nodes, "leader.")
	for _, script := range cfg.Components.Scripts {
		if !script.Enabled || strings.TrimSpace(script.Command) == "" {
			continue
		}
		for _, id := range []string{script.ID, "script." + script.ID} {
			if strings.TrimSpace(id) != "" {
				registry[id] = automation.ActionDescriptor{ID: id, Name: script.Title, Provider: automation.ProviderScript, Risk: automation.RiskLow}
			}
		}
	}
	return registry
}

func validateWorkflowPlanConfig(workflow config.Workflow) error {
	if workflow.Execution != "" && workflow.Execution != config.WorkflowExecutionSequential {
		return fmt.Errorf("workflow %q has unsupported execution mode %q (only %q is supported)", workflow.ID, workflow.Execution, config.WorkflowExecutionSequential)
	}
	if workflow.Failure != "" && workflow.Failure != config.WorkflowFailureStop && workflow.Failure != config.WorkflowFailureContinue {
		return fmt.Errorf("workflow %q has unsupported failure mode %q", workflow.ID, workflow.Failure)
	}
	if workflow.Source == config.WorkflowSourcePrompt {
		return fmt.Errorf("workflow %q uses unsupported prompt source: CLI workflow planning requires explicit input", workflow.ID)
	}
	if workflow.Source != "" && workflow.Source != config.WorkflowSourceClipboard && workflow.Source != config.WorkflowSourceSelection && workflow.Source != config.WorkflowSourceText {
		return fmt.Errorf("workflow %q has unsupported source %q", workflow.ID, workflow.Source)
	}
	switch workflow.Output {
	case "", config.WorkflowOutputReplace, config.WorkflowOutputInsert, config.WorkflowOutputClipboard, config.WorkflowOutputNotify, config.WorkflowOutputNone:
	default:
		return fmt.Errorf("workflow %q has unsupported output mode %q", workflow.ID, workflow.Output)
	}
	stepEnabled := make(map[string]bool, len(workflow.Steps))
	for _, step := range workflow.Steps {
		stepEnabled[step.ID] = step.Enabled
	}
	for _, step := range workflow.Steps {
		if step.Execution != "" && step.Execution != config.WorkflowExecutionSequential {
			return fmt.Errorf("workflow %q step %q has unsupported execution mode %q (only %q is supported)", workflow.ID, step.ID, step.Execution, config.WorkflowExecutionSequential)
		}
		if step.Failure != "" && step.Failure != config.WorkflowFailureStop && step.Failure != config.WorkflowFailureContinue {
			return fmt.Errorf("workflow %q step %q has unsupported failure mode %q", workflow.ID, step.ID, step.Failure)
		}
		if step.Source == config.WorkflowSourcePrompt {
			return fmt.Errorf("workflow %q step %q uses unsupported prompt source: CLI workflow planning requires explicit input", workflow.ID, step.ID)
		}
		if step.Source != "" && step.Source != config.WorkflowSourceClipboard && step.Source != config.WorkflowSourceSelection && step.Source != config.WorkflowSourceText {
			return fmt.Errorf("workflow %q step %q has unsupported source %q", workflow.ID, step.ID, step.Source)
		}
		switch step.Output {
		case "", config.WorkflowOutputReplace, config.WorkflowOutputNotify, config.WorkflowOutputNone:
		default:
			return fmt.Errorf("workflow %q step %q has unsupported output mode %q", workflow.ID, step.ID, step.Output)
		}
		if step.Enabled {
			for _, dependency := range step.DependsOn {
				if enabled, exists := stepEnabled[dependency]; exists && !enabled {
					return fmt.Errorf("workflow %q step %q depends on disabled step %q", workflow.ID, step.ID, dependency)
				}
			}
		}
	}
	if issues := config.ValidateWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{workflow}}); len(issues) != 0 {
		return fmt.Errorf("invalid workflow %q: %s", workflow.ID, formatWorkflowIssues(issues))
	}
	return nil
}

func formatWorkflowIssues(issues []config.ValidationIssue) string {
	parts := make([]string, len(issues))
	for i, issue := range issues {
		parts[i] = issue.KeyPath + ": " + issue.Message
	}
	return strings.Join(parts, "; ")
}

func workflowFailureMode(mode config.WorkflowFailureMode) automation.FailureMode {
	if mode == config.WorkflowFailureContinue {
		return automation.FailureContinue
	}
	return automation.FailureStop
}

var workflowTemplatePattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)

// workflowStepSourceInputID mirrors the controller's private source binding
// key. The key is metadata only: it identifies the source value that a
// provider would receive at execution time without putting that value in a
// CLI plan.
func workflowStepSourceInputID(step config.WorkflowStep) string {
	id := strings.TrimSpace(step.ID)
	var encoded strings.Builder
	encoded.Grow(len(id))
	for i := 0; i < len(id); i++ {
		ch := id[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			encoded.WriteByte(ch)
			continue
		}
		fmt.Fprintf(&encoded, "_x%02x_", ch)
	}
	return "__workflow_step_" + encoded.String() + "_text"
}

// normalizeWorkflowTemplates mirrors the controller's documented template
// shorthand. In particular, {{text}} means this step's configured source,
// rather than a shared workflow-level value.
func normalizeWorkflowTemplates(value string, workflow config.Workflow, step config.WorkflowStep) string {
	return workflowTemplatePattern.ReplaceAllStringFunc(value, func(match string) string {
		expression := strings.TrimSpace(match[2 : len(match)-2])
		if strings.HasPrefix(expression, "input.") || strings.HasPrefix(expression, "step.") {
			return match
		}
		switch expression {
		case "text":
			return "{{input." + workflowStepSourceInputID(step) + "}}"
		case "selection", "selected_text", "clipboard", "clipboard_text", "prompt", "app", "app_name", "bundle_id", "url", "window_title":
			return "{{input." + expression + "}}"
		}
		for _, input := range workflow.Inputs {
			if expression == input.ID || (input.Name != "" && expression == input.Name) {
				return "{{input." + input.ID + "}}"
			}
		}
		return match
	})
}

func toAutomationWorkflow(workflow config.Workflow) automation.WorkflowDefinition {
	result := automation.WorkflowDefinition{ID: workflow.ID, Name: workflow.Name, Description: workflow.Description, FailureMode: workflowFailureMode(workflow.Failure)}
	for _, input := range workflow.Inputs {
		result.Inputs = append(result.Inputs, automation.WorkflowInput{ID: input.ID, Name: input.Name, Type: input.Type, Required: input.Required, Default: input.Default})
	}
	for _, step := range workflow.Steps {
		if !step.Enabled {
			continue
		}
		failure := automation.FailureMode("")
		if step.Failure == config.WorkflowFailureContinue {
			failure = automation.FailureContinue
		} else if step.Failure == config.WorkflowFailureStop && !step.FailureInherited {
			failure = automation.FailureStop
		}
		output := automation.OutputCapture
		switch step.Output {
		case config.WorkflowOutputNone:
			output = automation.OutputDiscard
		case config.WorkflowOutputReplace:
			output = automation.OutputReplace
		case config.WorkflowOutputNotify:
			output = automation.OutputNotify
		}
		input := map[string]any{}
		if step.Input != "" {
			input["input"] = normalizeWorkflowTemplates(step.Input, workflow, step)
		}
		if step.Prompt != "" {
			input["prompt"] = normalizeWorkflowTemplates(step.Prompt, workflow, step)
		}
		// Keep the source binding as an alias, not a captured value. Planning
		// must remain metadata-only and must never inspect or execute providers.
		input["source"] = string(step.Source)
		input["text"] = "{{input." + workflowStepSourceInputID(step) + "}}"
		result.Steps = append(result.Steps, automation.WorkflowStep{ID: step.ID, ActionID: step.Action, Input: input, DependsOn: append([]string(nil), step.DependsOn...), FailureMode: failure, OutputMode: output})
	}
	return result
}

func encodeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
