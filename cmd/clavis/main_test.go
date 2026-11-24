package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/internal/secrets"
	internalsync "github.com/yeisme/clavis/internal/sync"
	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

func TestParseGlobalFlagsAllowsFlagsAfterCommand(t *testing.T) {
	dir := t.TempDir()
	gotDir, jsonOutput, command, err := parseGlobalFlags([]string{"workflow", "list", "--config-dir", dir, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != dir || !jsonOutput || strings.Join(command, " ") != "workflow list" {
		t.Fatalf("got dir=%q json=%v command=%v", gotDir, jsonOutput, command)
	}
}

func TestRunWorkflowPlanPrintsDeterministicPlanWithoutExecuting(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	cfg := store.Current()
	cfg.Components.Scripts = append(cfg.Components.Scripts, config.ScriptComponentSpec{ID: "prepare", Command: "touch", Enabled: true})
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "build", Name: "Build", Enabled: true,
		Steps: []config.WorkflowStep{
			{ID: "second", Action: "prepare", DependsOn: []string{"first"}, Enabled: true},
			{ID: "first", Action: "prepare", Enabled: true},
			{ID: "disabled", Action: "prepare", Enabled: false},
		},
	}}
	if err := store.SaveComponents(cfg.Components); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkflows(cfg.Workflows); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"workflow", "plan", "build", "--config-dir", dir}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v; stderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "first\nsecond\n"; got != want {
		t.Fatalf("plan=%q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "workflows.toml")); err != nil {
		t.Fatal(err)
	}
}

func TestRunWorkflowPlanRejectsPromptSourceAndDisabledScript(t *testing.T) {
	cfg := config.ClavisConfig{
		Components: config.ComponentsConfig{Scripts: []config.ScriptComponentSpec{{ID: "disabled", Command: "echo", Enabled: false}}},
		Workflows:  config.WorkflowsConfig{Workflows: []config.Workflow{{ID: "prompt", Enabled: true, Source: config.WorkflowSourcePrompt, Steps: []config.WorkflowStep{{ID: "step", Action: "disabled", Enabled: true}}}}},
	}
	if err := validateWorkflowPlanConfig(cfg.Workflows.Workflows[0]); err == nil || !strings.Contains(err.Error(), "prompt source") {
		t.Fatalf("prompt source validation error = %v", err)
	}
	cfg.Workflows.Workflows[0].Source = config.WorkflowSourceClipboard
	plan, err := automation.BuildExecutionPlanWithRegistry(toAutomationWorkflow(cfg.Workflows.Workflows[0]), actionRegistry(cfg))
	if err == nil || !strings.Contains(err.Error(), "not registered") || plan != nil {
		t.Fatalf("disabled script planning error = %v plan=%v", err, plan)
	}
}

func TestRunWorkflowPlanRejectsUnsupportedModes(t *testing.T) {
	workflow := config.Workflow{ID: "invalid", Execution: config.WorkflowExecutionParallel}
	if err := validateWorkflowPlanConfig(workflow); err == nil || !strings.Contains(err.Error(), "unsupported execution mode") {
		t.Fatalf("validation error = %v", err)
	}
	workflow.Execution = ""
	workflow.Failure = config.WorkflowFailureMode("abort")
	if err := validateWorkflowPlanConfig(workflow); err == nil || !strings.Contains(err.Error(), "unsupported failure mode") {
		t.Fatalf("abort validation error = %v", err)
	}
}

func TestRunWorkflowPlanJSON(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	cfg := store.Current()
	cfg.Components.Scripts = append(cfg.Components.Scripts, config.ScriptComponentSpec{ID: "echo", Command: "echo", Enabled: true})
	cfg.Workflows.Workflows = []config.Workflow{{ID: "one", Enabled: true, Steps: []config.WorkflowStep{{ID: "step", Action: "echo", Enabled: true}}}}
	if err := store.SaveComponents(cfg.Components); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkflows(cfg.Workflows); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"workflow", "plan", "one", "--json", "--config-dir", dir}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v; stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "step"`) || strings.Contains(stdout.String(), `"command"`) {
		t.Fatalf("unexpected plan JSON: %s", stdout.String())
	}
}

func TestToAutomationWorkflowInheritsWorkflowFailurePolicy(t *testing.T) {
	workflow := config.Workflow{ID: "continue", Failure: config.WorkflowFailureContinue, Steps: []config.WorkflowStep{{ID: "first", Action: "run", Enabled: true}}}
	definition := toAutomationWorkflow(workflow)
	plan, err := automation.BuildExecutionPlan(definition)
	if err != nil || definition.FailureMode != automation.FailureContinue || len(plan) != 1 || plan[0].FailureMode != automation.FailureContinue {
		t.Fatalf("definition=%+v plan=%+v err=%v", definition, plan, err)
	}
}

func TestToAutomationWorkflowNormalizesSourceBindingsAndTextShorthand(t *testing.T) {
	workflow := config.Workflow{
		ID:     "source-bindings",
		Inputs: []config.WorkflowInput{{ID: "tone", Name: "Tone", Default: "classified"}},
		Steps: []config.WorkflowStep{
			{ID: "first-step", Action: "first", Enabled: true, Source: config.WorkflowSourceSelection, Input: "{{text}}|{{Tone}}|{{input.raw}}", Prompt: "{{selection}}"},
			{ID: "second_step", Action: "second", Enabled: true, Source: config.WorkflowSourceClipboard, Input: "{{text}}"},
		},
	}

	definition := toAutomationWorkflow(workflow)
	plan, err := automation.BuildExecutionPlan(definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan length = %d, want 2", len(plan))
	}

	first := plan[0].Input
	if got, want := first["source"], string(config.WorkflowSourceSelection); got != want {
		t.Fatalf("first source = %v, want %v", got, want)
	}
	if got, want := first["text"], "{{input.__workflow_step_first_x2d_step_text}}"; got != want {
		t.Fatalf("first text binding = %v, want %q", got, want)
	}
	if got, want := first["input"], "{{input.__workflow_step_first_x2d_step_text}}|{{input.tone}}|{{input.raw}}"; got != want {
		t.Fatalf("first normalized input = %v, want %q", got, want)
	}
	if got, want := first["prompt"], "{{input.selection}}"; got != want {
		t.Fatalf("first normalized prompt = %v, want %q", got, want)
	}

	second := plan[1].Input
	if got, want := second["source"], string(config.WorkflowSourceClipboard); got != want {
		t.Fatalf("second source = %v, want %v", got, want)
	}
	if got, want := second["text"], "{{input.__workflow_step_second_x5f_step_text}}"; got != want {
		t.Fatalf("second text binding = %v, want %q", got, want)
	}
	if got, want := second["input"], "{{input.__workflow_step_second_x5f_step_text}}"; got != want {
		t.Fatalf("second normalized input = %v, want %q", got, want)
	}
}

func TestRunWorkflowPlanSourceMetadataDoesNotExposeSourceValues(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	cfg := store.Current()
	cfg.Components.Scripts = append(cfg.Components.Scripts, config.ScriptComponentSpec{ID: "echo", Command: "echo", Enabled: true})
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "safe", Enabled: true,
		Steps: []config.WorkflowStep{{ID: "step", Action: "echo", Enabled: true, Source: config.WorkflowSourceSelection, Input: "{{text}}"}},
	}}
	if err := store.SaveComponents(cfg.Components); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkflows(cfg.Workflows); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"workflow", "plan", "safe", "--json", "--config-dir", dir}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v; stderr=%s", err, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, `"source": "selection"`) || !strings.Contains(output, `{{input.__workflow_step_step_text}}`) {
		t.Fatalf("source metadata missing from plan JSON: %s", output)
	}
	// A plan has no action context. Selection/clipboard contents must never be
	// sampled or serialized while producing metadata-only CLI output.
	for _, sensitive := range []string{"selected-value", "clipboard-value", "secret-value"} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("plan JSON leaked source value %q: %s", sensitive, output)
		}
	}
}

func TestSyncCLIStatusOfflineAndPlan(t *testing.T) {
	cfgDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--config-dir", cfgDir, "--json", "sync", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("sync status offline err = %v (stderr=%s)", err, stderr.String())
	}
	var status syncStatusOutput
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status JSON: %v\nraw=%s", err, stdout.String())
	}
	if status.Configured || status.Enabled || status.RecoveryReady || status.Code != "not_configured" {
		t.Fatalf("unexpected unconfigured status: %+v", status)
	}
	if len(status.Allowlist) != 2 || status.Allowlist[0] != coresync.InputMethod || status.Allowlist[1] != coresync.LearningData {
		t.Fatalf("unexpected allowlist: %+v", status.Allowlist)
	}
	// Verify offline status created zero files inside cfgDir!
	if entries, err := os.ReadDir(cfgDir); err != nil || len(entries) != 0 {
		t.Fatalf("sync status mutated empty configDir: entries=%v err=%v", entries, err)
	}

	// Human status output works cleanly too.
	stdout.Reset()
	if err := Run([]string{"sync", "status", "--config-dir", cfgDir}, &stdout, &stderr); err != nil {
		t.Fatalf("sync status human err = %v", err)
	}
	if !strings.Contains(stdout.String(), "configured=false") || !strings.Contains(stdout.String(), "allowlist=input_method,learning_data") {
		t.Fatalf("unexpected human status: %q", stdout.String())
	}

	// sync plan JSON and human output.
	stdout.Reset()
	if err := Run([]string{"sync", "plan", "--json", "--config-dir", cfgDir}, &stdout, &stderr); err != nil {
		t.Fatalf("sync plan JSON err = %v", err)
	}
	var plan syncPlanOutput
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal plan JSON: %v", err)
	}
	if plan.Transport != "dir" || len(plan.Allowlist) != 2 || len(plan.BlockedClasses) != 2 {
		t.Fatalf("unexpected plan output: %+v", plan)
	}
	stdout.Reset()
	if err := Run([]string{"sync", "plan", "--config-dir", cfgDir}, &stdout, &stderr); err != nil {
		t.Fatalf("sync plan human err = %v", err)
	}
	if !strings.Contains(stdout.String(), "input_method\tallowed") || !strings.Contains(stdout.String(), "passwords\tforbidden") {
		t.Fatalf("unexpected human plan: %q", stdout.String())
	}
}

func TestSyncCLIRunExitCodesAndEndToEndDirSync(t *testing.T) {
	cfgDir := t.TempDir()
	syncDir := filepath.Join(t.TempDir(), "shared")
	var stdout, stderr bytes.Buffer

	// Missing --dir returns exit code 2 (usage error).
	err := Run([]string{"sync", "run", "--config-dir", cfgDir}, &stdout, &stderr)
	if ExitCode(err) != exitCodeUsage {
		t.Fatalf("missing --dir exit code = %d (err=%v), want %d", ExitCode(err), err, exitCodeUsage)
	}

	// Uninitialized configDir returns exit code 3 (not initialized), never fake success!
	stdout.Reset()
	err = Run([]string{"sync", "run", "--dir", syncDir, "--config-dir", cfgDir, "--json"}, &stdout, &stderr)
	if ExitCode(err) != exitCodeNotReady {
		t.Fatalf("uninitialized sync run exit code = %d (err=%v), want %d", ExitCode(err), err, exitCodeNotReady)
	}
	if !strings.Contains(stdout.String(), `"code": "not_initialized"`) {
		t.Fatalf("uninitialized JSON output = %s", stdout.String())
	}

	// Bootstrap Device A and Device B, upload on A, then execute `clavis sync run --dir <shared> --config-dir <B> --json`!
	privA := filepath.Join(t.TempDir(), "dev-a-cfg")
	privB := filepath.Join(t.TempDir(), "dev-b-cfg")
	ctx := context.Background()

	txA, err := internalsync.OpenDirTransport(ctx, internalsync.DirTransportOptions{
		SyncDir:     syncDir,
		PrivateDir:  privA,
		AccountID:   "cli-acct",
		DeviceID:    "cli-dev-a",
		SecretStore: secrets.NewFileStore(secrets.DefaultKeysPath(privA)),
	})
	if err != nil {
		t.Fatal(err)
	}
	code, err := txA.BootstrapAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txB, err := internalsync.OpenDirTransport(ctx, internalsync.DirTransportOptions{
		SyncDir:     syncDir,
		PrivateDir:  privB,
		AccountID:   "cli-acct",
		DeviceID:    "cli-dev-b",
		SecretStore: secrets.NewFileStore(secrets.DefaultKeysPath(privB)),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bundle, err := txA.CreatePairingBundle(ctx, "cli-dev-b", txB.DevicePublicKey(), txB.X25519PublicKey(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := txB.AcceptPairingTicket(ctx, bundle.Ticket, bundle.ShortCode, coresync.PairingFingerprint(txA.DevicePublicKey()), code, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := txA.Upload(ctx, coresync.LearningData, "cli-lexicon", 1, []byte("secret-learning-payload")); err != nil {
		t.Fatal(err)
	}
	// Close both transports so CLI opens Device B's local SQLite cleanly.
	if err := txA.Close(); err != nil {
		t.Fatal(err)
	}
	if err := txB.Close(); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"--config-dir", privB, "--json", "sync", "run", "--dir", syncDir}, &stdout, &stderr); err != nil {
		t.Fatalf("clavis sync run failed: %v (stderr=%s stdout=%s)", err, stderr.String(), stdout.String())
	}
	var runOut syncRunOutput
	if err := json.Unmarshal(stdout.Bytes(), &runOut); err != nil {
		t.Fatalf("unmarshal run JSON: %v\nraw=%s", err, stdout.String())
	}
	if runOut.Code != "ok" || runOut.IngestedEnvelopes != 1 || runOut.PulledObjects != 1 || runOut.Conflicts != 0 {
		t.Fatalf("unexpected sync run output: %+v", runOut)
	}
	if strings.Contains(stdout.String(), "secret-learning-payload") {
		t.Fatalf("sync run leaked plaintext payload: %s", stdout.String())
	}

	// Now verify `clavis sync status --config-dir <privB> --json` shows configured=true, enabled=true, envelopeFiles=1.
	stdout.Reset()
	if err := Run([]string{"sync", "status", "--config-dir", privB, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("sync status after run err = %v", err)
	}
	var after syncStatusOutput
	if err := json.Unmarshal(stdout.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !after.Configured || !after.Enabled || !after.RecoveryReady || after.EnvelopeFiles != 1 {
		t.Fatalf("unexpected status after run: %+v", after)
	}
}
