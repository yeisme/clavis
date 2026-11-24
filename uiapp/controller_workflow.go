package uiapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/actions"
	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
)

// CancelWorkflow cancels the active workflow, if any. It is safe to call from
// UI actions such as Escape and returns whether a run was canceled.
func (c *AppController) CancelWorkflow() bool {
	c.workflowLifecycleMu.Lock()
	defer c.workflowLifecycleMu.Unlock()
	if c.workflowCancel == nil {
		return false
	}
	c.workflowCancel()
	c.workflowCancel = nil
	if c.workflowDone != nil {
		c.workflowDone()
		c.workflowDone = nil
	}
	c.workflowRunID++
	return true
}

// ExecuteWorkflow validates and plans a configured workflow, reports the plan via
// the normal HUD event, and starts its provider-independent runner in the
// background. Only actions already represented by the safe CLI/script adapters
// are exposed to workflows; workflow strings are never passed through a shell.
func (c *AppController) ExecuteWorkflow(workflowID string, actionCtx engine.CapsuleContext) error {
	return c.executeWorkflowContext(c.lifeCtx(), workflowID, actionCtx)
}

// RunWorkflow is an explicit-context alias for ExecuteWorkflow. It is useful to
// callers which use Run as the vocabulary for other asynchronous controller work.
func (c *AppController) RunWorkflow(ctx context.Context, workflowID string, actionCtx engine.CapsuleContext) error {
	if ctx == nil {
		ctx = c.lifeCtx()
	}
	return c.executeWorkflowContext(ctx, workflowID, actionCtx)
}

type workflowAction struct {
	leader *config.LeaderAction
	script *config.ScriptComponentSpec
}

type workflowActionRegistry struct {
	actions     map[string]workflowAction
	descriptors automation.MapActionRegistry
}

func (r workflowActionRegistry) GetAction(id string) (automation.ActionDescriptor, bool) {
	d, ok := r.descriptors[id]
	return d, ok
}

func (c *AppController) executeWorkflowContext(parent context.Context, workflowID string, actionCtx engine.CapsuleContext) error {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return errors.New("workflow ID cannot be empty")
	}
	cfg := c.ConfigStore.Current()
	var workflow config.Workflow
	found := false
	for _, candidate := range cfg.Workflows.Workflows {
		if candidate.ID == workflowID {
			workflow = candidate
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("workflow %q not found", workflowID)
	}
	if !workflow.Enabled {
		return fmt.Errorf("workflow %q is disabled", workflowID)
	}

	if err := validateWorkflowAdapter(workflow); err != nil {
		c.workflowHUD(fmt.Sprintf("Workflow %s failed to plan: %v", workflowID, err))
		return err
	}
	registry := c.workflowActionRegistry(cfg)
	if c.workflowRegistryOverride != nil {
		registry = c.workflowRegistryOverride(cfg)
	}
	definition := workflowDefinition(workflow)
	plan, err := automation.BuildExecutionPlanWithRegistry(definition, registry)
	if err != nil {
		c.workflowHUD(fmt.Sprintf("Workflow %s failed to plan: %v", workflowID, err))
		return err
	}
	inputs, inputErr := workflowInputs(workflow, actionCtx)
	if inputErr != nil {
		c.workflowHUD(fmt.Sprintf("Workflow %s failed: %v", workflowID, inputErr))
		return inputErr
	}

	if parent == nil {
		parent = c.lifeCtx()
	}
	runtimeRegistry := c.ensureRuntime()
	c.workflowLifecycleMu.Lock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		c.workflowLifecycleMu.Unlock()
		return errors.New("controller is closed")
	}
	// There is one controller-owned run slot. A subsequent run supersedes and
	// cancels the previous one, avoiding completion races between runs.
	if c.workflowCancel != nil {
		c.workflowCancel()
		if c.workflowDone != nil {
			c.workflowDone()
			c.workflowDone = nil
		}
	}
	runCtx, cancel := context.WithCancel(parent)
	workflowDone := runtimeRegistry.Register(runtimecoord.WorkflowRun, cancel, "Workflow "+workflowID)
	c.workflowCancel = cancel
	c.workflowDone = workflowDone
	c.workflowRunID++
	runID := c.workflowRunID
	c.mu.Lock()
	targetGen := c.workflowTargetGen
	c.mu.Unlock()
	c.workflowLifecycleMu.Unlock()

	c.workflowHUD(fmt.Sprintf("Workflow %s planned (%d steps)", workflowID, len(plan)))
	actionCtxCopy := actionCtx
	go func() {
		provider := workflowProvider{controller: c, actions: registry.actions, actionCtx: actionCtxCopy, workflowInputs: inputs}
		result, runErr := automation.NewRunner(provider, automation.WithActionRegistry(registry.descriptors)).Run(runCtx, plan, inputs)
		workflowDone()
		c.reportWorkflow(runID, targetGen, workflow, actionCtxCopy, result, runErr)
	}()
	return nil
}

func (c *AppController) workflowActionRegistry(cfg config.ClavisConfig) workflowActionRegistry {
	registry := workflowActionRegistry{
		actions:     make(map[string]workflowAction),
		descriptors: make(automation.MapActionRegistry),
	}
	register := func(id string, action workflowAction, provider automation.ProviderType, name string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := registry.actions[id]; exists {
			return
		}
		registry.actions[id] = action
		registry.descriptors[id] = automation.ActionDescriptor{ID: id, Name: name, Provider: provider, Risk: automation.RiskMedium}
	}
	var visit func([]config.LeaderNode, string)
	visit = func(nodes []config.LeaderNode, prefix string) {
		for _, node := range nodes {
			id := strings.TrimSpace(prefix + node.Key)
			if node.Action.Type == config.ActionGroup {
				visit(node.Action.Children, id+".")
				continue
			}
			action := node.Action
			if action.Type != config.ActionCLI || strings.TrimSpace(action.Command) == "" {
				continue
			}
			register(id, workflowAction{leader: &action}, automation.ProviderCLI, node.Label)
			// Short IDs are convenient in workflows and are only aliases when they
			// cannot shadow another action.
			register(strings.TrimPrefix(id, "leader."), workflowAction{leader: &action}, automation.ProviderCLI, node.Label)
		}
	}
	visit(cfg.Leader.Nodes, "leader.")
	for i := range cfg.Components.Scripts {
		script := cfg.Components.Scripts[i]
		// Disabled and malformed scripts are not executable actions. Omitting
		// them at planning time prevents a skipped provider result from being
		// mistaken for successful workflow completion.
		if !script.Enabled || strings.TrimSpace(script.Command) == "" {
			continue
		}
		action := workflowAction{script: &script}
		register(script.ID, action, automation.ProviderScript, script.Title)
		register("script."+script.ID, action, automation.ProviderScript, script.Title)
	}
	return registry
}

// validateWorkflowAdapter rejects configuration values that the controller
// cannot represent in automation.WorkflowDefinition. Config validation runs for
// persisted files, but this boundary is also used by in-memory callers and
// therefore must not rely on a prior config validation pass.
func validateWorkflowAdapter(workflow config.Workflow) error {
	if workflow.Execution != "" && workflow.Execution != config.WorkflowExecutionSequential {
		return fmt.Errorf("workflow %q has unsupported execution mode %q (only %q is supported)", workflow.ID, workflow.Execution, config.WorkflowExecutionSequential)
	}
	if workflow.Failure != "" && workflow.Failure != config.WorkflowFailureStop && workflow.Failure != config.WorkflowFailureContinue {
		return fmt.Errorf("workflow %q has unsupported failure mode %q", workflow.ID, workflow.Failure)
	}
	switch workflow.Output {
	case "", config.WorkflowOutputReplace, config.WorkflowOutputInsert, config.WorkflowOutputClipboard, config.WorkflowOutputNotify, config.WorkflowOutputNone:
	default:
		return fmt.Errorf("workflow %q has unsupported output mode %q", workflow.ID, workflow.Output)
	}
	if workflow.Source != "" && workflow.Source != config.WorkflowSourceClipboard && workflow.Source != config.WorkflowSourceSelection && workflow.Source != config.WorkflowSourceText {
		return fmt.Errorf("workflow %q has unsupported source %q", workflow.ID, workflow.Source)
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
		if step.Source != "" && step.Source != config.WorkflowSourceClipboard && step.Source != config.WorkflowSourceSelection && step.Source != config.WorkflowSourceText {
			return fmt.Errorf("workflow %q step %q has unsupported source %q", workflow.ID, step.ID, step.Source)
		}
		switch step.Output {
		case "", config.WorkflowOutputReplace, config.WorkflowOutputNotify, config.WorkflowOutputNone:
		default:
			return fmt.Errorf("workflow %q step %q has unsupported output mode %q", workflow.ID, step.ID, step.Output)
		}
		if !step.Enabled {
			continue
		}
		for _, dependency := range step.DependsOn {
			if enabled, exists := stepEnabled[dependency]; exists && !enabled {
				return fmt.Errorf("workflow %q step %q depends on disabled step %q", workflow.ID, step.ID, dependency)
			}
		}
	}
	return nil
}

func workflowFailureMode(mode config.WorkflowFailureMode) automation.FailureMode {
	if mode == config.WorkflowFailureContinue {
		return automation.FailureContinue
	}
	return automation.FailureStop
}

func workflowDefinition(workflow config.Workflow) automation.WorkflowDefinition {
	definition := automation.WorkflowDefinition{ID: workflow.ID, Name: workflow.Name, Description: workflow.Description, FailureMode: workflowFailureMode(workflow.Failure)}
	for _, input := range workflow.Inputs {
		definition.Inputs = append(definition.Inputs, automation.WorkflowInput{ID: input.ID, Name: input.Name, Type: input.Type, Required: input.Required, Default: input.Default})
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
		// Keep the persisted source on the invocation. The provider uses it to
		// expose one deterministic source alias instead of making text,
		// selection, and clipboard interchangeable.
		input["source"] = string(step.Source)
		input["text"] = fmt.Sprintf("{{input.%s}}", workflowStepSourceInputID(step))
		definition.Steps = append(definition.Steps, automation.WorkflowStep{ID: step.ID, ActionID: step.Action, Input: input, DependsOn: append([]string(nil), step.DependsOn...), FailureMode: failure, OutputMode: output})
	}
	return definition
}

func workflowInputs(workflow config.Workflow, actionCtx engine.CapsuleContext) (map[string]any, error) {
	if workflow.Source != "" && workflow.Source != config.WorkflowSourceClipboard && workflow.Source != config.WorkflowSourceSelection && workflow.Source != config.WorkflowSourceText && workflow.Source != config.WorkflowSourcePrompt {
		return nil, fmt.Errorf("workflow %q has unsupported source %q", workflow.ID, workflow.Source)
	}
	values := workflowContextInputs(workflow.Source, actionCtx)
	if workflow.Source == config.WorkflowSourcePrompt {
		return nil, fmt.Errorf("workflow %q uses unsupported prompt source: prompt input is not available in controller workflows", workflow.ID)
	}
	for _, step := range workflow.Steps {
		if step.Enabled {
			if step.Source == config.WorkflowSourcePrompt {
				return nil, fmt.Errorf("workflow step %q uses unsupported prompt source: prompt input is not available in controller workflows", step.ID)
			}
			value, err := workflowSourceValue(step.Source, actionCtx)
			if err != nil && step.Source != config.WorkflowSourcePrompt {
				return nil, fmt.Errorf("workflow step %q: %w", step.ID, err)
			}
			values[workflowStepSourceInputID(step)] = value
		}
	}
	for _, input := range workflow.Inputs {
		value, ok := values[input.ID]
		if !ok || isEmptyWorkflowInput(value) {
			if input.Default != "" {
				value, ok = input.Default, true
				values[input.ID] = value
			}
			if input.Required && (!ok || isEmptyWorkflowInput(value)) {
				return nil, fmt.Errorf("required workflow input %q is not supplied", input.ID)
			}
		}
		// Persisted workflows commonly use the display name in templates while
		// the runner receives the stable input ID. Expose both aliases.
		if ok && input.Name != "" {
			values[input.Name] = value
		}
	}
	return values, nil
}

func workflowContextInputs(source config.WorkflowSource, actionCtx engine.CapsuleContext) map[string]any {
	selection := actionCtx.SelectedText
	clipboard := actionCtx.ClipboardPreview
	text := selection
	switch source {
	case "", config.WorkflowSourceClipboard:
		text = clipboard
	case config.WorkflowSourceText:
		if actionCtx.SurroundingText != "" {
			text = actionCtx.SurroundingText
		}
	case config.WorkflowSourcePrompt:
		text = ""
	}
	values := map[string]any{
		"selection": selection, "selected_text": selection,
		"clipboard": clipboard, "clipboard_text": clipboard,
		"text": text,
		"app":  actionCtx.AppName, "app_name": actionCtx.AppName, "bundle_id": actionCtx.BundleID,
		"url": actionCtx.ActiveURL, "window_title": actionCtx.WindowTitle,
	}
	if source != config.WorkflowSourcePrompt {
		values["prompt"] = text
	}
	return values
}

// workflowStepSourceInputID returns an internal input key for a step's source
// text. Step IDs allow both hyphens and underscores, so replacing punctuation
// with underscores is not injective (for example, a-b and a_b collide). Encode
// every byte outside the unambiguous alphanumeric set as a fixed-width hex
// escape; the resulting key is deterministic, valid for template lookup, and
// preserves a one-to-one mapping from valid step IDs without exposing values.
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

func workflowSourceValue(source config.WorkflowSource, actionCtx engine.CapsuleContext) (string, error) {
	switch source {
	case config.WorkflowSourceSelection:
		return actionCtx.SelectedText, nil
	case "", config.WorkflowSourceClipboard:
		return actionCtx.ClipboardPreview, nil
	case config.WorkflowSourceText:
		if actionCtx.SurroundingText != "" {
			return actionCtx.SurroundingText, nil
		}
		return actionCtx.SelectedText, nil
	case config.WorkflowSourcePrompt:
		return "", fmt.Errorf("workflow prompt source requires explicit prompt input")
	default:
		return "", fmt.Errorf("unsupported workflow source %q", source)
	}
}

func workflowTargetMatchesSnapshot(target engine.CapsuleContext, current host.ContextSnapshot) bool {
	// Empty target identity is not verifiable. Refuse injection rather than
	// guessing, while retaining compatibility with contexts that only carry text.
	if strings.TrimSpace(target.BundleID) == "" && strings.TrimSpace(target.ProcessName) == "" && strings.TrimSpace(target.AppName) == "" {
		return false
	}
	if target.BundleID != "" && current.BundleID != target.BundleID {
		return false
	}
	if target.ProcessName != "" && current.ProcessName != target.ProcessName {
		return false
	}
	if target.AppName != "" && current.AppName != target.AppName {
		return false
	}
	return true
}

func isEmptyWorkflowInput(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

type workflowProvider struct {
	controller     *AppController
	actions        map[string]workflowAction
	actionCtx      engine.CapsuleContext
	workflowInputs map[string]any
}

func (p workflowProvider) Invoke(ctx context.Context, invocation automation.ActionInvocation) (automation.ActionResult, error) {
	action, ok := p.actions[invocation.ActionID]
	if !ok {
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: "action is not available"}, nil
	}
	// Workflow inputs provide the defaults for an action invocation. The
	// runner's step-specific bindings must be applied last: a step may select a
	// different source than the workflow (for example, selection in a clipboard
	// workflow), and its text/source aliases must win while resolving action
	// templates. This also keeps named workflow inputs and internal step-output
	// bindings available without letting them overwrite the current step.
	bindings := mergeWorkflowInputs(workflowContextInputs("", p.actionCtx), automation.ActionInvocation{Input: p.workflowInputs})
	bindings = mergeWorkflowInputs(bindings, invocation)
	if action.script != nil {
		if !action.script.Enabled {
			return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: "script is disabled and cannot complete workflow"}, nil
		}
		if strings.TrimSpace(action.script.Command) == "" {
			return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: "script command is empty"}, nil
		}
		// Substitute into each argv element rather than a shell command. This keeps
		// input and previous-step output observable without changing command safety.
		spec := *action.script
		var err error
		spec.Args, err = substituteWorkflowArgs(spec.Args, bindings)
		if err != nil {
			return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: err.Error()}, nil
		}
		result, err := actions.ExecuteScriptComponent(ctx, spec, p.actionCtx)
		return cliActionResult(result, err), nil
	}
	if action.leader == nil || action.leader.Type != config.ActionCLI {
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: "workflow action is not a CLI or script action"}, nil
	}
	args, err := substituteWorkflowArgs(action.leader.Args, bindings)
	if err != nil {
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: err.Error()}, nil
	}
	return p.invokeCLIProfile(ctx, action.leader.Profile, action.leader.Command, args, 0)
}

func mergeWorkflowInputs(workflowInputs map[string]any, invocation automation.ActionInvocation) map[string]any {
	bindings := make(map[string]any, len(workflowInputs)+len(invocation.Input)+len(invocation.Inputs))
	for key, value := range workflowInputs {
		bindings[key] = value
	}
	for key, value := range invocation.Input {
		bindings[key] = value
	}
	for key, value := range invocation.Inputs {
		bindings[key] = value
	}
	return bindings
}

func substituteWorkflowArgs(args []string, bindings map[string]any) ([]string, error) {
	resolved := make([]string, len(args))
	for i, arg := range args {
		value, err := substituteWorkflowArg(arg, bindings)
		if err != nil {
			return nil, err
		}
		resolved[i] = value
	}
	return resolved, nil
}

func substituteWorkflowArg(arg string, bindings map[string]any) (string, error) {
	var firstErr error
	resolved := workflowTemplatePattern.ReplaceAllStringFunc(arg, func(match string) string {
		expression := strings.TrimSpace(match[2 : len(match)-2])
		value, ok := resolveWorkflowBinding(expression, bindings)
		if !ok {
			firstErr = fmt.Errorf("workflow: unresolved template %q", expression)
			return match
		}
		if encoded, ok := value.(map[string]any); ok {
			data, err := json.Marshal(encoded)
			if err == nil {
				return string(data)
			}
		}
		valueType := reflect.TypeOf(value)
		if valueType != nil && (valueType.Kind() == reflect.Map || valueType.Kind() == reflect.Slice || valueType.Kind() == reflect.Array) {
			if data, err := json.Marshal(value); err == nil {
				return string(data)
			}
		}
		return fmt.Sprint(value)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return resolved, nil
}

func resolveWorkflowBinding(expression string, bindings map[string]any) (any, bool) {
	parts := strings.Split(expression, ".")
	if len(parts) == 0 {
		return nil, false
	}
	if value, ok := bindings[parts[0]]; ok {
		for _, part := range parts[1:] {
			var found bool
			value, found = lookupWorkflowBinding(value, part)
			if !found {
				return nil, false
			}
		}
		return value, true
	}
	// {{input}} and {{inputs}} are useful for providers that receive a map
	// without a named field. A named input field still wins when present.
	if (expression == "input" || expression == "inputs") && len(bindings) > 0 {
		return bindings, true
	}
	return nil, false
}

func lookupWorkflowBinding(value any, key string) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	item, found := object[key]
	return item, found
}

var workflowTemplatePattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)

func (p workflowProvider) invokeCLI(ctx context.Context, command string, args []string, timeout time.Duration) (automation.ActionResult, error) {
	return p.invokeCLIProfile(ctx, "", command, args, timeout)
}

func (p workflowProvider) invokeCLIProfile(ctx context.Context, profileID, command string, args []string, timeout time.Duration) (automation.ActionResult, error) {
	if strings.TrimSpace(profileID) == "" {
		return automation.ActionResult{Status: automation.ResultFailed, Error: "workflow CLI action requires a trusted profile"}, nil
	}
	var executor actions.Executor = actions.NewCLIExecutor()
	var profile *config.CLITrustedProfile
	if p.controller != nil {
		p.controller.mu.Lock()
		executor = p.controller.CLIExecutor
		cfg := p.controller.ConfigStore.Current()
		p.controller.mu.Unlock()
		if !cfg.Main.CLI.Enabled {
			return automation.ActionResult{Status: automation.ResultFailed, Error: "CLI execution is disabled; configure a trusted profile"}, nil
		}
		if profileID != "" {
			for i := range cfg.Main.CLI.TrustedProfiles {
				if cfg.Main.CLI.TrustedProfiles[i].ID == profileID {
					profile = &cfg.Main.CLI.TrustedProfiles[i]
					break
				}
			}
			if profile == nil {
				return automation.ActionResult{Status: automation.ResultFailed, Error: fmt.Sprintf("CLI profile %q was not found", profileID)}, nil
			}
			if !profile.Enabled {
				return automation.ActionResult{Status: automation.ResultFailed, Error: fmt.Sprintf("CLI profile %q is disabled", profileID)}, nil
			}
			if profile.DryRun {
				return automation.ActionResult{Status: automation.ResultFailed, Error: "CLI profile is in dry-run mode"}, nil
			}
		}
	}
	result := executor.ExecuteRequest(ctx, actions.CommandRequest{Profile: profile, Command: command, Args: args, Timeout: timeout, ProfileID: profileID})
	return commandActionResult(result), nil
}

func commandActionResult(result actions.CommandResult) automation.ActionResult {
	output := result.Stdout
	if output == "" {
		output = result.Stderr
	}
	actionResult := automation.ActionResult{Status: automation.ResultSucceeded, Output: output, Metadata: map[string]any{"exitCode": result.ExitCode, "stderr": result.Stderr, "timedOut": result.TimedOut}}
	if result.Err != nil {
		actionResult.Status = automation.ResultFailed
		actionResult.Error = result.Err.Error()
		return actionResult
	}
	if result.TimedOut || result.Canceled || result.ExitCode != 0 {
		actionResult.Status = automation.ResultFailed
	}
	return actionResult
}

func cliActionResult(result actions.CLIExecutionResult, err error) automation.ActionResult {
	output := result.Stdout
	if output == "" {
		output = result.Stderr
	}
	actionResult := automation.ActionResult{Status: automation.ResultSucceeded, Output: output, Metadata: map[string]any{"exitCode": result.ExitCode, "stderr": result.Stderr, "timedOut": result.TimedOut}}
	if err != nil {
		actionResult.Status = automation.ResultFailed
		actionResult.Error = err.Error()
		return actionResult
	}
	if result.TimedOut || result.ExitCode != 0 {
		actionResult.Status = automation.ResultFailed
		if result.TimedOut {
			actionResult.Error = "command timed out"
		} else {
			actionResult.Error = fmt.Sprintf("command exited with code %d", result.ExitCode)
		}
	}
	return actionResult
}

func (c *AppController) reportWorkflow(runID, targetGen uint64, workflow config.Workflow, actionCtx engine.CapsuleContext, result automation.ExecutionResult, runErr error) {
	c.workflowLifecycleMu.Lock()
	currentRun := runID == c.workflowRunID
	c.mu.Lock()
	closed := c.closed
	hook := c.OnWorkflowResult
	c.mu.Unlock()
	if closed || !currentRun {
		c.workflowLifecycleMu.Unlock()
		return
	}
	// Cancellation is intentionally silent. In particular, a canceled command
	// must not turn Escape or shutdown into a late failure HUD.
	canceled := result.Canceled || errors.Is(runErr, context.Canceled)
	if c.workflowCancel != nil && runID == c.workflowRunID {
		// Finalize the run before doing any HUD, output, or callback work. Those
		// operations may synchronously enter the lifecycle API themselves.
		c.workflowCancel = nil
		if c.workflowDone != nil {
			c.workflowDone()
			c.workflowDone = nil
		}
	}
	c.workflowLifecycleMu.Unlock()
	if canceled {
		return
	}
	if runErr != nil {
		c.workflowHUD(fmt.Sprintf("Workflow %s failed: %v", workflow.ID, runErr))
	} else if result.Stopped {
		message := "workflow failed"
		for _, step := range result.Steps {
			if step.Result.Status == automation.ResultFailed {
				message = fmt.Sprintf("step %s failed: %s", step.StepID, step.Result.Error)
				break
			}
		}
		c.workflowHUD(fmt.Sprintf("Workflow %s %s", workflow.ID, message))
	} else {
		c.workflowHUD(fmt.Sprintf("Workflow %s completed", workflow.ID))
		c.applyWorkflowOutputAtTarget(workflow.Output, actionCtx, result, targetGen)
	}
	if hook != nil {
		hook(workflow.ID, result, runErr)
	}
}

func (c *AppController) workflowHUD(message string) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: message})
}

func (c *AppController) applyWorkflowOutput(mode config.WorkflowOutput, actionCtx engine.CapsuleContext, result automation.ExecutionResult) {
	c.mu.Lock()
	targetGen := c.workflowTargetGen
	c.mu.Unlock()
	c.applyWorkflowOutputAtTarget(mode, actionCtx, result, targetGen)
}

func (c *AppController) applyWorkflowOutputAtTarget(mode config.WorkflowOutput, actionCtx engine.CapsuleContext, result automation.ExecutionResult, targetGen uint64) {
	if mode == config.WorkflowOutputNone || mode == config.WorkflowOutputDiscard {
		return
	}
	var output any
	for _, step := range result.Steps {
		if step.Result.Status != automation.ResultSucceeded {
			continue
		}
		// ExecutionResult.Outputs is deliberately limited to non-discarded step
		// outputs. Never fall back to StepResult.Result.Output here: it may contain
		// a secret from an OutputDiscard step.
		if value, ok := result.Outputs[step.StepID]; ok {
			output = value
		}
	}
	if output == nil {
		return
	}
	text := strings.TrimSpace(fmt.Sprint(output))
	if text == "" {
		return
	}
	switch mode {
	case config.WorkflowOutputReplace, config.WorkflowOutputInsert:
		c.mu.Lock()
		stale := c.workflowTargetGen != targetGen || c.closed
		c.mu.Unlock()
		if stale {
			// The selected text belongs to an older foreground target. Never
			// inject into an unrelated field; a notification preserves the result.
			c.workflowHUD(text)
			return
		}
		if mode == config.WorkflowOutputReplace {
			c.injectWorkflowTextAsync(text, len([]rune(actionCtx.SelectedText)), 0, targetGen, actionCtx)
		} else {
			c.injectWorkflowTextAsync(text, 0, 0, targetGen, actionCtx)
		}
	case config.WorkflowOutputClipboard:
		c.writeClipboardText(text)
	case config.WorkflowOutputNotify:
		c.workflowHUD(text)
	}
}

func (c *AppController) runAction(act config.LeaderAction, actionCtx engine.CapsuleContext) {
	switch act.Type {
	case config.ActionWindow:
		c.mu.Lock()
		br := c.Bridge
		c.mu.Unlock()
		if br != nil && !c.isClosed() {
			lifeCtx := c.lifeCtx()
			go func() {
				if lifeCtx.Err() != nil {
					return
				}
				_ = br.ExecuteWindowCommand(lifeCtx, act.WindowCommand)
			}()
		}

	case config.ActionCLI:
		if act.Command == "clavis-setting" {
			c.handleInternalSettingCommand(act.Args)
			return
		}
		if act.Command == "clavis-capture" {
			mode := ""
			if len(act.Args) > 0 {
				mode = act.Args[0]
			}
			if mode == "ocr" {
				go c.CaptureAndOCR()
			} else {
				go c.CaptureAndAnnotate()
			}
			return
		}
		cfg := c.ConfigStore.Current()
		if !cfg.Main.CLI.Enabled {
			c.ShowToastOrHUD("CLI execution is disabled")
			return
		}
		var profile *config.CLITrustedProfile
		for i := range cfg.Main.CLI.TrustedProfiles {
			if cfg.Main.CLI.TrustedProfiles[i].ID == act.Profile {
				profile = &cfg.Main.CLI.TrustedProfiles[i]
				break
			}
		}
		if profile == nil {
			c.ShowToastOrHUD("CLI action requires a trusted profile")
			return
		}
		if !profile.Enabled {
			c.ShowToastOrHUD(fmt.Sprintf("CLI profile %q is disabled", profile.ID))
			return
		}
		if _, err := actions.ResolveTrusted(*profile, act.Command); err != nil {
			c.ShowToastOrHUD(fmt.Sprintf("CLI command refused: %v", err))
			return
		}
		if profile.DryRun {
			c.ShowToastOrHUD(fmt.Sprintf("CLI dry-run: %s %s", act.Command, strings.Join(act.Args, " ")))
			return
		}
		c.mu.Lock()
		execer := c.CLIExecutor
		c.mu.Unlock()
		ctx, cancel := context.WithCancel(c.lifeCtx())
		c.mu.Lock()
		if c.cliCancel != nil {
			c.cliCancel()
		}
		c.cliGeneration++
		generation := c.cliGeneration
		c.cliCancel = cancel
		c.mu.Unlock()
		go func() {
			defer func() {
				c.mu.Lock()
				if c.cliGeneration == generation {
					c.cliCancel = nil
				}
				c.mu.Unlock()
			}()
			res := execer.ExecuteRequest(ctx, actions.CommandRequest{Profile: profile, Command: act.Command, Args: act.Args, Dir: profile.DefaultDir, ProfileID: profile.ID})
			status := "succeeded"
			category := ""
			if res.Err != nil || res.ExitCode != 0 || res.TimedOut || res.Canceled {
				status = "failed"
				category = automation.ErrorCategory(res.Err)
				if res.TimedOut {
					category = "timeout"
				}
				if res.Canceled {
					category = "cancelled"
				}
			}
			c.mu.Lock()
			audit := c.AuditLog
			c.mu.Unlock()
			if audit != nil {
				audit.Append(automation.AuditEvent{Profile: profile.ID, Target: act.Command, InputFingerprint: automation.FingerprintInput(strings.Join(append([]string{act.Command}, act.Args...), "\x00")), Status: status, ErrorCategory: category})
			}
			if res.Err != nil {
				c.ShowToastOrHUD(fmt.Sprintf("CLI command failed: %v", res.Err))
				return
			}
			if res.Canceled {
				c.ShowToastOrHUD("CLI command cancelled")
				return
			}
			if res.TimedOut {
				c.ShowToastOrHUD(i18n.T("hud.command_timeout"))
				return
			}
			if res.ExitCode != 0 {
				c.ShowToastOrHUD(i18n.Tf("hud.command_exit_code", res.ExitCode))
			}
		}()

	case config.ActionOpenApp:
		c.mu.Lock()
		br := c.Bridge
		c.mu.Unlock()
		if br != nil && !c.isClosed() {
			lifeCtx := c.lifeCtx()
			go func() {
				if lifeCtx.Err() != nil {
					return
				}
				_ = br.OpenApp(lifeCtx, act.Target)
			}()
		}

	case config.ActionSnippet:
		cfg := c.ConfigStore.Current()
		for _, item := range cfg.Snippets.Items {
			if item.ID == act.SnippetID {
				rendered := engine.RenderSnippet(item.Content, engine.RenderOptions{
					Now:           time.Now(),
					ClipboardText: actionCtx.ClipboardPreview,
					AppName:       actionCtx.AppName,
				})
				c.injectTextAsync(rendered.RenderedText, 0, rendered.CursorBackOffset)
				return
			}
		}
		c.Dispatch(engine.Event{
			Type:    engine.EventShowHUD,
			Message: i18n.Tf("hud.snippet_not_found", act.SnippetID),
		})

	case config.ActionToggleIME:
		c.mu.Lock()
		c.skipIMERestore = true
		c.imeLockedForCapsule = false
		c.preLeaderIMESourceID = ""
		c.mu.Unlock()
		c.toggleIME()

	case config.ActionNormalizeGlossary:
		c.normalizeGlossary(actionCtx)
	}
}
