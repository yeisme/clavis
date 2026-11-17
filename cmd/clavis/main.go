// Command clavis provides a non-interactive view of the declarative Clavis
// configuration. It deliberately only validates and plans workflows: no
// provider, plugin, MCP, or external command is executed by this command.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
)

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
	configDir, jsonOutput, commandArgs, err := parseGlobalFlags(args)
	if err != nil {
		return err
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
		os.Exit(2)
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
	err := errors.New("usage: clavis [--config-dir DIR] [--json] action list | workflow list | workflow plan WORKFLOW")
	if w != nil {
		fmt.Fprintln(w, err)
	}
	return err
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
	if workflow.Failure != "" && workflow.Failure != config.WorkflowFailureStop && workflow.Failure != config.WorkflowFailureContinue && workflow.Failure != config.WorkflowFailureAbort {
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
		if step.Failure != "" && step.Failure != config.WorkflowFailureStop && step.Failure != config.WorkflowFailureContinue && step.Failure != config.WorkflowFailureAbort {
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

func toAutomationWorkflow(workflow config.Workflow) automation.WorkflowDefinition {
	result := automation.WorkflowDefinition{ID: workflow.ID, Name: workflow.Name, Description: workflow.Description}
	for _, input := range workflow.Inputs {
		result.Inputs = append(result.Inputs, automation.WorkflowInput{ID: input.ID, Name: input.Name, Type: input.Type, Required: input.Required, Default: input.Default})
	}
	for _, step := range workflow.Steps {
		if !step.Enabled {
			continue
		}
		failure := automation.FailureStop
		if step.Failure == config.WorkflowFailureContinue {
			failure = automation.FailureContinue
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
			input["input"] = step.Input
		}
		if step.Prompt != "" {
			input["prompt"] = step.Prompt
		}
		result.Steps = append(result.Steps, automation.WorkflowStep{ID: step.ID, ActionID: step.Action, Input: input, DependsOn: append([]string(nil), step.DependsOn...), FailureMode: failure, OutputMode: output})
	}
	return result
}

func encodeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
