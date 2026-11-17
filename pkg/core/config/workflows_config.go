package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// The mode types are strings on purpose: this keeps the public configuration
// model forward compatible while ValidateWorkflows rejects values the current
// runner cannot execute.
type WorkflowSource string
type WorkflowOutput string
type WorkflowFailureMode string
type WorkflowExecutionMode string

const (
	WorkflowSourceClipboard      WorkflowSource = "clipboard"
	WorkflowSourceSelection      WorkflowSource = "selection"
	WorkflowSourceText           WorkflowSource = "text"
	WorkflowSourcePrompt         WorkflowSource = "prompt"
	WorkflowSourceClipboardInput                = WorkflowSourceClipboard
	WorkflowSourceSelectedText                  = WorkflowSourceSelection

	WorkflowOutputReplace   WorkflowOutput = "replace"
	WorkflowOutputInsert    WorkflowOutput = "insert"
	WorkflowOutputClipboard WorkflowOutput = "clipboard"
	WorkflowOutputNotify    WorkflowOutput = "notify"
	WorkflowOutputNone      WorkflowOutput = "none"
	WorkflowOutputDiscard                  = WorkflowOutputNone

	WorkflowFailureStop     WorkflowFailureMode = "stop"
	WorkflowFailureContinue WorkflowFailureMode = "continue"
	WorkflowFailureNotify   WorkflowFailureMode = "notify"
	WorkflowFailureAbort                        = WorkflowFailureStop

	WorkflowExecutionSequential WorkflowExecutionMode = "sequential"
	WorkflowExecutionParallel   WorkflowExecutionMode = "parallel"
	WorkflowExecutionConcurrent                       = WorkflowExecutionParallel
)

const (
	DefaultWorkflowSource    = WorkflowSourceClipboard
	DefaultWorkflowOutput    = WorkflowOutputReplace
	DefaultWorkflowFailure   = WorkflowFailureStop
	DefaultWorkflowExecution = WorkflowExecutionSequential
)

// WorkflowInput declares a named value that can be supplied to a workflow.
type WorkflowInput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Default  string `json:"default"`
	Required bool   `json:"required"`
}

// WorkflowStep is one executable unit in a workflow. Dependencies name other
// steps in the same workflow and are evaluated before the step is run.
type WorkflowStep struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Action    string                `json:"action"`
	Command   string                `json:"command"`
	Prompt    string                `json:"prompt"`
	Input     string                `json:"input"`
	DependsOn []string              `json:"dependsOn"`
	Enabled   bool                  `json:"enabled"`
	Source    WorkflowSource        `json:"source"`
	Output    WorkflowOutput        `json:"output"`
	Failure   WorkflowFailureMode   `json:"failure"`
	Execution WorkflowExecutionMode `json:"execution"`
}

// Workflow describes a persisted, declarative workflow.
type Workflow struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Enabled     bool                  `json:"enabled"`
	Source      WorkflowSource        `json:"source"`
	Output      WorkflowOutput        `json:"output"`
	Failure     WorkflowFailureMode   `json:"failure"`
	Execution   WorkflowExecutionMode `json:"execution"`
	Inputs      []WorkflowInput       `json:"inputs"`
	Steps       []WorkflowStep        `json:"steps"`
}

// WorkflowsConfig is the root of workflows.toml.
type WorkflowsConfig struct {
	Workflows []Workflow `json:"workflows"`
}

// DefaultWorkflowsConfig returns a deliberately empty configuration. A
// workflow cannot run unless the user explicitly declares it; defaults on a
// declared workflow are conservative (sequential execution and stop on
// failure).
func DefaultWorkflowsConfig() WorkflowsConfig {
	return WorkflowsConfig{Workflows: nil}
}

func defaultWorkflow() Workflow {
	return Workflow{
		Enabled:   true,
		Source:    DefaultWorkflowSource,
		Output:    DefaultWorkflowOutput,
		Failure:   DefaultWorkflowFailure,
		Execution: DefaultWorkflowExecution,
	}
}

func defaultWorkflowStep() WorkflowStep {
	return WorkflowStep{
		Enabled:   true,
		Source:    DefaultWorkflowSource,
		Output:    DefaultWorkflowOutput,
		Failure:   DefaultWorkflowFailure,
		Execution: DefaultWorkflowExecution,
	}
}

func decodeWorkflowInput(t *toml.Table, path string) (WorkflowInput, error) {
	id, err := requiredString(t, "id", path)
	if err != nil {
		return WorkflowInput{}, err
	}
	name, _, err := optionalString(t, "name", path)
	if err != nil {
		return WorkflowInput{}, err
	}
	typ, _, err := optionalString(t, "type", path)
	if err != nil {
		return WorkflowInput{}, err
	}
	def, _, err := optionalString(t, "default", path)
	if err != nil {
		return WorkflowInput{}, err
	}
	required := false
	if value, ok, e := optionalBool(t, "required", path); e != nil {
		return WorkflowInput{}, e
	} else if ok {
		required = value
	}
	return WorkflowInput{ID: id, Name: name, Type: typ, Default: def, Required: required}, nil
}

func (i WorkflowInput) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(i.ID))
	t.Set("name", toml.StringVal(i.Name))
	t.Set("type", toml.StringVal(i.Type))
	t.Set("default", toml.StringVal(i.Default))
	t.Set("required", toml.BoolVal(i.Required))
	return t
}

func decodeWorkflowStep(t *toml.Table, path string) (WorkflowStep, error) {
	id, err := requiredString(t, "id", path)
	if err != nil {
		return WorkflowStep{}, err
	}
	step := defaultWorkflowStep()
	step.ID = id
	for key, target := range map[string]*string{
		"name": &step.Name, "action": &step.Action, "command": &step.Command,
		"prompt": &step.Prompt, "input": &step.Input,
	} {
		value, _, e := optionalString(t, key, path)
		if e != nil {
			return WorkflowStep{}, e
		}
		*target = value
	}
	if value, ok, e := optionalString(t, "source", path); e != nil {
		return WorkflowStep{}, e
	} else if ok {
		step.Source = WorkflowSource(value)
	}
	if value, ok, e := optionalString(t, "output", path); e != nil {
		return WorkflowStep{}, e
	} else if ok {
		step.Output = WorkflowOutput(value)
	}
	if value, ok, e := optionalString(t, "failure", path); e != nil {
		return WorkflowStep{}, e
	} else if ok {
		step.Failure = WorkflowFailureMode(value)
	}
	if value, ok, e := optionalString(t, "execution", path); e != nil {
		return WorkflowStep{}, e
	} else if ok {
		step.Execution = WorkflowExecutionMode(value)
	}
	if value, ok, e := optionalBool(t, "enabled", path); e != nil {
		return WorkflowStep{}, e
	} else if ok {
		step.Enabled = value
	}
	deps, _, err := optionalStringArray(t, "depends_on", path)
	if err != nil {
		return WorkflowStep{}, err
	}
	step.DependsOn = deps
	return step, nil
}

func (s WorkflowStep) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(s.ID))
	t.Set("name", toml.StringVal(s.Name))
	t.Set("action", toml.StringVal(s.Action))
	t.Set("command", toml.StringVal(s.Command))
	t.Set("prompt", toml.StringVal(s.Prompt))
	t.Set("input", toml.StringVal(s.Input))
	t.Set("depends_on", stringsToTOMLArray(s.DependsOn))
	t.Set("enabled", toml.BoolVal(s.Enabled))
	t.Set("source", toml.StringVal(string(s.Source)))
	t.Set("output", toml.StringVal(string(s.Output)))
	t.Set("failure", toml.StringVal(string(s.Failure)))
	t.Set("execution", toml.StringVal(string(s.Execution)))
	return t
}

func decodeWorkflow(t *toml.Table, path string) (Workflow, error) {
	id, err := requiredString(t, "id", path)
	if err != nil {
		return Workflow{}, err
	}
	workflow := defaultWorkflow()
	workflow.ID = id
	if value, _, e := optionalString(t, "name", path); e != nil {
		return Workflow{}, e
	} else {
		workflow.Name = value
	}
	if value, _, e := optionalString(t, "description", path); e != nil {
		return Workflow{}, e
	} else {
		workflow.Description = value
	}
	if value, ok, e := optionalBool(t, "enabled", path); e != nil {
		return Workflow{}, e
	} else if ok {
		workflow.Enabled = value
	}
	if value, ok, e := optionalString(t, "source", path); e != nil {
		return Workflow{}, e
	} else if ok {
		workflow.Source = WorkflowSource(value)
	}
	if value, ok, e := optionalString(t, "output", path); e != nil {
		return Workflow{}, e
	} else if ok {
		workflow.Output = WorkflowOutput(value)
	}
	if value, ok, e := optionalString(t, "failure", path); e != nil {
		return Workflow{}, e
	} else if ok {
		workflow.Failure = WorkflowFailureMode(value)
	}
	if value, ok, e := optionalString(t, "execution", path); e != nil {
		return Workflow{}, e
	} else if ok {
		workflow.Execution = WorkflowExecutionMode(value)
	}

	inputs, _, e := optionalTableArray(t, "inputs", path)
	if e != nil {
		return Workflow{}, e
	}
	if len(inputs) > 0 {
		workflow.Inputs = make([]WorkflowInput, 0, len(inputs))
		for i, input := range inputs {
			value, e := decodeWorkflowInput(input, fmt.Sprintf("%s.inputs[%d]", path, i))
			if e != nil {
				return Workflow{}, e
			}
			workflow.Inputs = append(workflow.Inputs, value)
		}
	}
	steps, _, e := optionalTableArray(t, "steps", path)
	if e != nil {
		return Workflow{}, e
	}
	if len(steps) > 0 {
		workflow.Steps = make([]WorkflowStep, 0, len(steps))
		for i, step := range steps {
			value, e := decodeWorkflowStep(step, fmt.Sprintf("%s.steps[%d]", path, i))
			if e != nil {
				return Workflow{}, e
			}
			workflow.Steps = append(workflow.Steps, value)
		}
	}
	return workflow, nil
}

func (w Workflow) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(w.ID))
	t.Set("name", toml.StringVal(w.Name))
	t.Set("description", toml.StringVal(w.Description))
	t.Set("enabled", toml.BoolVal(w.Enabled))
	t.Set("source", toml.StringVal(string(w.Source)))
	t.Set("output", toml.StringVal(string(w.Output)))
	t.Set("failure", toml.StringVal(string(w.Failure)))
	t.Set("execution", toml.StringVal(string(w.Execution)))
	inputs := make([]toml.Value, len(w.Inputs))
	for i := range w.Inputs {
		inputs[i] = toml.TableVal(w.Inputs[i].toTOML())
	}
	t.Set("inputs", toml.ArrayVal(inputs))
	steps := make([]toml.Value, len(w.Steps))
	for i := range w.Steps {
		steps[i] = toml.TableVal(w.Steps[i].toTOML())
	}
	t.Set("steps", toml.ArrayVal(steps))
	return t
}

// DecodeWorkflowsConfig parses workflows.toml text.
func DecodeWorkflowsConfig(input string) (WorkflowsConfig, error) {
	t, err := toml.Parse(input)
	if err != nil {
		return WorkflowsConfig{}, err
	}
	return WorkflowsConfigFromTOML(t)
}

// WorkflowsConfigFromTOML decodes a parsed workflows.toml table.
func WorkflowsConfigFromTOML(t *toml.Table) (WorkflowsConfig, error) {
	cfg := DefaultWorkflowsConfig()
	items, _, err := optionalTableArray(t, "workflows", "")
	if err != nil {
		return cfg, err
	}
	if len(items) == 0 {
		return cfg, nil
	}
	cfg.Workflows = make([]Workflow, 0, len(items))
	for i, item := range items {
		workflow, err := decodeWorkflow(item, fmt.Sprintf("workflows[%d]", i))
		if err != nil {
			return WorkflowsConfig{}, err
		}
		cfg.Workflows = append(cfg.Workflows, workflow)
	}
	return cfg, nil
}

// Encode serializes the config deterministically with the repository TOML API.
func (c WorkflowsConfig) Encode() string {
	root := toml.NewTable()
	items := make([]toml.Value, len(c.Workflows))
	for i := range c.Workflows {
		items[i] = toml.TableVal(c.Workflows[i].toTOML())
	}
	root.Set("workflows", toml.ArrayVal(items))
	return toml.Encode(root)
}

var workflowIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func validateWorkflowID(id string) bool { return workflowIDPattern.MatchString(strings.TrimSpace(id)) }

func appendWorkflowIssue(out *[]ValidationIssue, path, message string) {
	*out = append(*out, ValidationIssue{File: string(FileWorkflows), KeyPath: path, Severity: SeverityError, Message: message})
}

func validateMode(out *[]ValidationIssue, path, value string, supported map[string]struct{}, label string) {
	if value == "" {
		return
	}
	if _, ok := supported[value]; ok {
		return
	}
	supportedModes := make([]string, 0, len(supported))
	for mode := range supported {
		supportedModes = append(supportedModes, mode)
	}
	sort.Strings(supportedModes)
	appendWorkflowIssue(out, path, fmt.Sprintf("unsupported workflow %s mode %q (supported: %s)", label, value, strings.Join(supportedModes, ", ")))
}

var supportedWorkflowSources = map[string]struct{}{"clipboard": {}, "selection": {}, "text": {}, "prompt": {}}
var supportedWorkflowOutputs = map[string]struct{}{"replace": {}, "insert": {}, "clipboard": {}, "notify": {}, "none": {}, "discard": {}}

// The workflow runner currently implements only stop/continue failure handling.
// Keep the public string model forward compatible, but reject modes that would
// otherwise be silently treated as stop or continue.
var supportedWorkflowFailures = map[string]struct{}{"stop": {}, "continue": {}, "abort": {}}

// Plans and runners execute steps serially. Parallel/concurrent values remain
// decodable for compatibility, but validation rejects them until implemented.
var supportedWorkflowExecutions = map[string]struct{}{"sequential": {}}

// ValidateWorkflows validates IDs, modes, dependencies, and the dependency DAG.
func ValidateWorkflows(c WorkflowsConfig) []ValidationIssue {
	var issues []ValidationIssue
	workflowIDs := make(map[string]struct{}, len(c.Workflows))
	for i, workflow := range c.Workflows {
		path := fmt.Sprintf("workflows[%d]", i)
		if !validateWorkflowID(workflow.ID) {
			appendWorkflowIssue(&issues, path+".id", "workflow id must be a non-empty identifier")
		} else if _, exists := workflowIDs[workflow.ID]; exists {
			appendWorkflowIssue(&issues, path+".id", "duplicate workflow id")
		} else {
			workflowIDs[workflow.ID] = struct{}{}
		}
		validateMode(&issues, path+".source", string(workflow.Source), supportedWorkflowSources, "source")
		validateMode(&issues, path+".output", string(workflow.Output), supportedWorkflowOutputs, "output")
		validateMode(&issues, path+".failure", string(workflow.Failure), supportedWorkflowFailures, "failure")
		validateMode(&issues, path+".execution", string(workflow.Execution), supportedWorkflowExecutions, "execution")

		inputIDs := map[string]struct{}{}
		for j, input := range workflow.Inputs {
			inputPath := fmt.Sprintf("%s.inputs[%d]", path, j)
			if !validateWorkflowID(input.ID) {
				appendWorkflowIssue(&issues, inputPath+".id", "workflow input id must be a non-empty identifier")
			} else if _, exists := inputIDs[input.ID]; exists {
				appendWorkflowIssue(&issues, inputPath+".id", "duplicate workflow input id")
			} else {
				inputIDs[input.ID] = struct{}{}
			}
		}
		stepIDs := map[string]int{}
		for j, step := range workflow.Steps {
			stepPath := fmt.Sprintf("%s.steps[%d]", path, j)
			if !validateWorkflowID(step.ID) {
				appendWorkflowIssue(&issues, stepPath+".id", "workflow step id must be a non-empty identifier")
			} else if previous, exists := stepIDs[step.ID]; exists {
				_ = previous
				appendWorkflowIssue(&issues, stepPath+".id", "duplicate workflow step id")
			} else {
				stepIDs[step.ID] = j
			}
			validateMode(&issues, stepPath+".source", string(step.Source), supportedWorkflowSources, "source")
			validateMode(&issues, stepPath+".output", string(step.Output), supportedWorkflowOutputs, "output")
			validateMode(&issues, stepPath+".failure", string(step.Failure), supportedWorkflowFailures, "failure")
			validateMode(&issues, stepPath+".execution", string(step.Execution), supportedWorkflowExecutions, "execution")
		}
		validateWorkflowDependencies(&issues, path, workflow.Steps, stepIDs)
	}
	return issues
}

func validateWorkflowDependencies(issues *[]ValidationIssue, workflowPath string, steps []WorkflowStep, stepIDs map[string]int) {
	adjacency := make(map[string][]string, len(stepIDs))
	for i, step := range steps {
		stepPath := fmt.Sprintf("%s.steps[%d]", workflowPath, i)
		for j, dependency := range step.DependsOn {
			dependencyPath := fmt.Sprintf("%s.depends_on[%d]", stepPath, j)
			dependencyIndex, ok := stepIDs[dependency]
			if !ok {
				appendWorkflowIssue(issues, dependencyPath, "workflow step dependency does not exist")
				continue
			}
			if dependency == step.ID {
				appendWorkflowIssue(issues, dependencyPath, "workflow step cannot depend on itself")
			}
			if step.Enabled && !steps[dependencyIndex].Enabled {
				appendWorkflowIssue(issues, dependencyPath, fmt.Sprintf("enabled workflow step %q depends on disabled workflow step %q", step.ID, dependency))
			}
			adjacency[step.ID] = append(adjacency[step.ID], dependency)
		}
	}
	state := make(map[string]uint8, len(stepIDs))
	var visit func(string)
	visit = func(id string) {
		if state[id] == 1 {
			appendWorkflowIssue(issues, workflowPath+".steps", "workflow step dependencies contain a cycle")
			return
		}
		if state[id] == 2 {
			return
		}
		state[id] = 1
		for _, dependency := range adjacency[id] {
			visit(dependency)
		}
		state[id] = 2
	}
	for id := range stepIDs {
		visit(id)
	}
}
