package uiapp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// ---------------------------------------------------------------------------
// Workflows master-detail editor
//
// A workflow is a persisted, declarative automation (workflows.toml). The
// editor mirrors the snippets/glossary master-detail pattern: the list on the
// left selects a workflow, the cards on the right edit a deep-cloned draft.
// Nothing reaches ConfigStore.SaveWorkflows until the user saves, so an invalid
// draft shows its validation error while the last valid config stays intact.
// ---------------------------------------------------------------------------

// workflowSourceOptions lists the input sources the workflow runtime supports.
// "prompt" stays out of the picker: the runner rejects it.
func workflowSourceOptions() []settingOption {
	return []settingOption{
		{string(config.WorkflowSourceClipboard), i18n.T("settings.workflows.src.clipboard")},
		{string(config.WorkflowSourceSelection), i18n.T("settings.workflows.src.selection")},
		{string(config.WorkflowSourceText), i18n.T("settings.workflows.src.text")},
	}
}

// workflowOutputOptions lists every output mode a workflow may declare.
func workflowOutputOptions() []settingOption {
	return []settingOption{
		{string(config.WorkflowOutputReplace), i18n.T("settings.workflows.out.replace")},
		{string(config.WorkflowOutputInsert), i18n.T("settings.workflows.out.insert")},
		{string(config.WorkflowOutputClipboard), i18n.T("settings.workflows.out.clipboard")},
		{string(config.WorkflowOutputNotify), i18n.T("settings.workflows.out.notify")},
		{string(config.WorkflowOutputNone), i18n.T("settings.workflows.out.none")},
	}
}

// workflowStepOutputOptions narrows the outputs to those the step runner can
// apply: a step may feed the next step, notify, or discard its result.
func workflowStepOutputOptions() []settingOption {
	return []settingOption{
		{string(config.WorkflowOutputReplace), i18n.T("settings.workflows.out.replace")},
		{string(config.WorkflowOutputNotify), i18n.T("settings.workflows.out.notify")},
		{string(config.WorkflowOutputNone), i18n.T("settings.workflows.out.none")},
	}
}

// workflowFailureOptions lists the failure modes the runner implements.
func workflowFailureOptions() []settingOption {
	return []settingOption{
		{string(config.WorkflowFailureStop), i18n.T("settings.workflows.fail.stop")},
		{string(config.WorkflowFailureContinue), i18n.T("settings.workflows.fail.continue")},
	}
}

// workflowExecutionOptions lists the execution modes the runner implements.
// Parallel/concurrent values stay decodable but validation rejects them.
func workflowExecutionOptions() []settingOption {
	return []settingOption{
		{string(config.WorkflowExecutionSequential), i18n.T("settings.workflows.exec.sequential")},
	}
}

// cloneWorkflow deep-clones a workflow, including the slices a shallow copy
// would share with the ConfigStore's last valid config.
func cloneWorkflow(w config.Workflow) config.Workflow {
	out := w
	if w.Inputs != nil {
		out.Inputs = append([]config.WorkflowInput(nil), w.Inputs...)
	}
	if w.Steps != nil {
		out.Steps = make([]config.WorkflowStep, len(w.Steps))
		for i, step := range w.Steps {
			if step.DependsOn != nil {
				step.DependsOn = append([]string(nil), step.DependsOn...)
			}
			out.Steps[i] = step
		}
	}
	return out
}

// cloneWorkflowsConfig deep-clones a workflows config.
func cloneWorkflowsConfig(c config.WorkflowsConfig) config.WorkflowsConfig {
	out := config.WorkflowsConfig{}
	if c.Workflows != nil {
		out.Workflows = make([]config.Workflow, len(c.Workflows))
		for i, w := range c.Workflows {
			out.Workflows[i] = cloneWorkflow(w)
		}
	}
	return out
}

// normalizeWorkflow clones and fills the conservative defaults the decoder
// applies, so drafts written by the editor stay readable after a reload.
func normalizeWorkflow(w config.Workflow) config.Workflow {
	out := cloneWorkflow(w)
	out.ID = strings.TrimSpace(out.ID)
	out.Name = strings.TrimSpace(out.Name)
	if out.Source == "" {
		out.Source = config.DefaultWorkflowSource
	}
	if out.Output == "" {
		out.Output = config.DefaultWorkflowOutput
	}
	if out.Failure == "" {
		out.Failure = config.DefaultWorkflowFailure
	}
	if out.Execution == "" {
		out.Execution = config.DefaultWorkflowExecution
	}
	if out.Inputs == nil {
		out.Inputs = []config.WorkflowInput{}
	}
	if out.Steps == nil {
		out.Steps = []config.WorkflowStep{}
	}
	for i := range out.Steps {
		step := &out.Steps[i]
		step.ID = strings.TrimSpace(step.ID)
		step.Action = strings.TrimSpace(step.Action)
		if step.Source == "" {
			step.Source = config.DefaultWorkflowSource
		}
		if step.Output == "" {
			step.Output = config.DefaultWorkflowOutput
		}
		if step.Failure == "" {
			step.Failure = config.DefaultWorkflowFailure
			step.FailureInherited = true
		}
		if step.Execution == "" {
			step.Execution = config.DefaultWorkflowExecution
		}
		if step.DependsOn == nil {
			step.DependsOn = []string{}
		}
	}
	return out
}

// firstWorkflowStepIndex is the step selected after a load: the first one, or
// none for a workflow without steps.
func firstWorkflowStepIndex(steps []config.WorkflowStep) int {
	if len(steps) == 0 {
		return -1
	}
	return 0
}

// syncWorkflowSelection keeps the draft state in step with the stored list.
// The draft is (re)loaded only when the selection changes or after a save or
// delete, never on every frame, so in-progress edits survive a repaint.
func (a *AppController) syncWorkflowSelection(se *settingsEditorState, workflows []config.Workflow) {
	if seGet(se, func(s *settingsEditorState) bool { return s.workflowIsNew }) {
		return
	}
	selected := seGet(se, func(s *settingsEditorState) string { return s.workflowSelected })
	if selected == "" {
		if len(workflows) == 0 {
			seSet(se, func(s *settingsEditorState) {
				s.workflowOriginalID = ""
				s.workflowLoaded = false
				s.workflowStepIndex = -1
				s.workflowConfirmDelete = false
			})
			return
		}
		selected = workflows[0].ID
		seSet(se, func(s *settingsEditorState) {
			s.workflowSelected = selected
			s.workflowOriginalID = selected
			s.workflowLoaded = false
		})
	}
	index := -1
	for i := range workflows {
		if workflows[i].ID == selected {
			index = i
			break
		}
	}
	if index < 0 {
		seSet(se, func(s *settingsEditorState) {
			s.workflowSelected = ""
			s.workflowOriginalID = ""
			s.workflowLoaded = false
			s.workflowStepIndex = -1
			s.workflowDraft = config.Workflow{}
			s.workflowConfirmDelete = false
		})
		return
	}
	if seGet(se, func(s *settingsEditorState) bool { return s.workflowLoaded }) {
		return
	}
	draft := normalizeWorkflow(workflows[index])
	seSet(se, func(s *settingsEditorState) {
		s.workflowDraft = draft
		s.workflowLoaded = true
		s.workflowStepIndex = firstWorkflowStepIndex(draft.Steps)
	})
}

// newWorkflowDraft starts an unsaved workflow with the conservative defaults
// the decoder would apply.
func (a *AppController) newWorkflowDraft() {
	se := a.settingsEditor()
	draft := normalizeWorkflow(config.Workflow{Enabled: true})
	seSet(se, func(s *settingsEditorState) {
		s.workflowIsNew = true
		s.workflowSelected = ""
		s.workflowOriginalID = ""
		s.workflowLoaded = true
		s.workflowDraft = draft
		s.workflowStepIndex = -1
		s.workflowError = ""
		s.workflowConfirmDelete = false
	})
}

// selectWorkflowDraft loads the stored workflow into the editor as a clone.
func (a *AppController) selectWorkflowDraft(item config.Workflow) {
	se := a.settingsEditor()
	draft := normalizeWorkflow(item)
	seSet(se, func(s *settingsEditorState) {
		s.workflowIsNew = false
		s.workflowSelected = item.ID
		s.workflowOriginalID = item.ID
		s.workflowLoaded = true
		s.workflowDraft = draft
		s.workflowStepIndex = firstWorkflowStepIndex(draft.Steps)
		s.workflowError = ""
		s.workflowConfirmDelete = false
	})
}

// saveWorkflowsConfig persists workflows.toml and refreshes the state machine,
// so the Omnibox and the workflow runner see the new configuration at once.
func (a *AppController) saveWorkflowsConfig(workflowsCfg config.WorkflowsConfig) error {
	if err := a.ConfigStore.SaveWorkflows(workflowsCfg); err != nil {
		a.setSettingsStatus(i18n.Tf("settings.status.save_workflows_failed", err))
		return err
	}
	updated := a.ConfigStore.Current()
	a.mu.Lock()
	if a.StateMachine != nil {
		a.StateMachine.SetConfig(updated)
	}
	a.settingsStatus = i18n.Tf("settings.status.workflows_saved", len(updated.Workflows.Workflows))
	onRepaint := a.OnRepaint
	a.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// validateWorkflowStepActions rejects drafts whose steps could not run. It
// asks the same executable action registry the controller plans with (leader
// CLI nodes and enabled scripts), so an empty or misspelled action ID fails in
// the editor with an inline message instead of persisting a workflow that can
// only fail at execution time.
func (a *AppController) validateWorkflowStepActions(workflow config.Workflow) error {
	registry := a.workflowActionRegistry(a.ConfigStore.Current())
	for i, step := range workflow.Steps {
		if !step.Enabled {
			continue
		}
		title := workflowStepTitle(step, i)
		actionID := strings.TrimSpace(step.Action)
		if actionID == "" {
			return errors.New(i18n.Tf("settings.workflows.err_action", title))
		}
		if _, ok := registry.GetAction(actionID); !ok {
			return errors.New(i18n.Tf("settings.workflows.unknown_action", title, actionID))
		}
	}
	return nil
}

// saveWorkflowDraft persists the drafted workflow. A validation error is shown
// in the editor and leaves the last valid config untouched.
func (a *AppController) saveWorkflowDraft() {
	se := a.settingsEditor()
	draft := normalizeWorkflow(seGet(se, func(s *settingsEditorState) config.Workflow { return s.workflowDraft }))
	if draft.ID == "" {
		msg := i18n.T("settings.workflows.err_id_required")
		seSet(se, func(s *settingsEditorState) { s.workflowError = msg })
		a.setSettingsStatus(msg)
		return
	}
	if err := a.validateWorkflowStepActions(draft); err != nil {
		msg := err.Error()
		seSet(se, func(s *settingsEditorState) { s.workflowError = msg })
		a.setSettingsStatus(msg)
		return
	}
	original := seGet(se, func(s *settingsEditorState) string { return s.workflowOriginalID })
	isNew := seGet(se, func(s *settingsEditorState) bool { return s.workflowIsNew })
	next := cloneWorkflowsConfig(a.ConfigStore.Current().Workflows)
	replaced := false
	if !isNew {
		for i := range next.Workflows {
			if next.Workflows[i].ID == original {
				next.Workflows[i] = draft
				replaced = true
				break
			}
		}
	}
	if !replaced {
		next.Workflows = append(next.Workflows, draft)
	}
	if err := a.saveWorkflowsConfig(next); err != nil {
		msg := err.Error()
		seSet(se, func(s *settingsEditorState) { s.workflowError = msg })
		return
	}
	// Keep editing a private clone: the store now owns the saved slices.
	stored := cloneWorkflow(draft)
	seSet(se, func(s *settingsEditorState) {
		s.workflowError = ""
		s.workflowIsNew = false
		s.workflowSelected = draft.ID
		s.workflowOriginalID = draft.ID
		s.workflowDraft = stored
		s.workflowLoaded = true
		s.workflowConfirmDelete = false
	})
}

// requestDeleteWorkflow arms the inline confirmation instead of deleting the
// stored workflow immediately.
func (a *AppController) requestDeleteWorkflow() {
	se := a.settingsEditor()
	if seGet(se, func(s *settingsEditorState) bool { return s.workflowIsNew }) {
		return
	}
	if seGet(se, func(s *settingsEditorState) string { return s.workflowSelected }) == "" {
		return
	}
	seSet(se, func(s *settingsEditorState) {
		s.workflowConfirmDelete = true
		s.workflowError = ""
	})
}

// cancelDeleteWorkflow closes the confirmation without touching the store.
func (a *AppController) cancelDeleteWorkflow() {
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) { s.workflowConfirmDelete = false })
}

// deleteWorkflowDraft removes the selected stored workflow.
func (a *AppController) deleteWorkflowDraft() {
	se := a.settingsEditor()
	if seGet(se, func(s *settingsEditorState) bool { return s.workflowIsNew }) {
		return
	}
	target := seGet(se, func(s *settingsEditorState) string { return s.workflowSelected })
	if target == "" {
		return
	}
	next := config.WorkflowsConfig{Workflows: []config.Workflow{}}
	for _, item := range a.ConfigStore.Current().Workflows.Workflows {
		if item.ID == target {
			continue
		}
		next.Workflows = append(next.Workflows, cloneWorkflow(item))
	}
	if err := a.saveWorkflowsConfig(next); err != nil {
		seSet(se, func(s *settingsEditorState) { s.workflowError = err.Error() })
		return
	}
	selection := ""
	if len(next.Workflows) > 0 {
		selection = next.Workflows[0].ID
	}
	seSet(se, func(s *settingsEditorState) {
		s.workflowError = ""
		s.workflowIsNew = false
		s.workflowSelected = selection
		s.workflowOriginalID = selection
		s.workflowLoaded = false
		s.workflowStepIndex = -1
		s.workflowDraft = config.Workflow{}
		s.workflowConfirmDelete = false
	})
}

// addWorkflowStep appends a draft step (persisted with the workflow) and
// selects it.
func (a *AppController) addWorkflowStep() {
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		step := config.WorkflowStep{
			Enabled:          true,
			Source:           config.DefaultWorkflowSource,
			Output:           config.DefaultWorkflowOutput,
			Failure:          config.DefaultWorkflowFailure,
			FailureInherited: true,
			Execution:        config.DefaultWorkflowExecution,
			DependsOn:        []string{},
		}
		steps := make([]config.WorkflowStep, len(s.workflowDraft.Steps)+1)
		copy(steps, s.workflowDraft.Steps)
		steps[len(s.workflowDraft.Steps)] = step
		s.workflowDraft.Steps = steps
		s.workflowStepIndex = len(steps) - 1
	})
}

// deleteWorkflowStep removes the selected step from the draft.
func (a *AppController) deleteWorkflowStep() {
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		index := s.workflowStepIndex
		if index < 0 || index >= len(s.workflowDraft.Steps) {
			return
		}
		steps := make([]config.WorkflowStep, 0, len(s.workflowDraft.Steps)-1)
		steps = append(steps, s.workflowDraft.Steps[:index]...)
		steps = append(steps, s.workflowDraft.Steps[index+1:]...)
		if index >= len(steps) {
			index = len(steps) - 1
		}
		s.workflowDraft.Steps = steps
		s.workflowStepIndex = index
	})
}

// setWorkflowStep mutates one step of the draft under the editor lock.
func setWorkflowStep(se *settingsEditorState, index int, mutate func(step *config.WorkflowStep)) {
	seSet(se, func(s *settingsEditorState) {
		if index < 0 || index >= len(s.workflowDraft.Steps) {
			return
		}
		mutate(&s.workflowDraft.Steps[index])
	})
}

// workflowStepTitle names a step row: its ID, or a positional placeholder while
// the user has not typed one yet.
func workflowStepTitle(step config.WorkflowStep, index int) string {
	if id := strings.TrimSpace(step.ID); id != "" {
		return id
	}
	return i18n.Tf("settings.workflows.step_placeholder", index+1)
}

// settingsInlineSwitch is a label with a switch pushed to the row's end, the
// shape the item editors use for a boolean field.
func settingsInlineSwitch(c *ui.Context, theme *ui.Theme, label string, on bool) (bool, bool) {
	value, changed := on, false
	ui.Row(c).FillWidth().Height(controlHeight).Gap(space12).AlignItems(ui.Center).Padding(space2, 0).Children(func() {
		name := ui.Text(c, label).FontSize(typeBody).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
		sw := ui.Switch(c, &value)
		if sw.LabelledBy(name).Changed() {
			changed = true
		}
	})
	return value, changed
}

// renderWorkflowsSection draws the workflow list, the workflow editor and the
// steps of the workflow being edited.
func (a *AppController) renderWorkflowsSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	workflows := cfg.Workflows.Workflows
	a.syncWorkflowSelection(se, workflows)

	selected := seGet(se, func(s *settingsEditorState) string { return s.workflowSelected })
	isNew := seGet(se, func(s *settingsEditorState) bool { return s.workflowIsNew })
	active := isNew || selected != ""

	// The editor needs room for its segmented pickers: below a wide window the
	// list stacks above the editor instead of squeezing it into a narrow column.
	columns := 2
	if width, _ := c.Size(); width < 1080 {
		columns = 1
	}
	ui.Grid(c).Columns(columns).Gap(10).FillWidth().Children(func() {
		settingsEditorCard(c, theme, i18n.Tf("settings.workflows.card_title", len(workflows)), func() {
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.workflows.new_workflow")).Clicked() {
					a.newWorkflowDraft()
				}
				settingsHint(c, theme, i18n.T("settings.workflows.select_workflow"))
			})
			settingsDivider(c)
			if len(workflows) == 0 {
				kitEmptyState(c, theme, "wand.and.stars", i18n.T("settings.workflows.empty"), "")
			}
			for i := range workflows {
				item := workflows[i]
				row := settingsListRow(c, theme, "workflow:"+item.ID, !isNew && item.ID == selected)
				row.Children(func() {
					ui.Text(c, item.ID).FontSize(typeLabel).FontWeight(600).TextColor(accentInk(theme, theme.Surface)).SingleLine().Shrink(0)
					if item.Name != "" {
						ui.Text(c, item.Name).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
					} else {
						ui.Spacer(c)
					}
					if !item.Enabled {
						chip(c, theme, "", i18n.T("settings.workflows.disabled"), chipNeutral).Shrink(0)
					}
					ui.Text(c, fmt.Sprintf("%d", len(item.Steps))).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().Shrink(0)
				})
				if row.Clicked() {
					a.selectWorkflowDraft(item)
				}
			}
		})

		settingsEditorCard(c, theme, i18n.T("settings.workflows.editor"), func() {
			if !active {
				settingsHint(c, theme, i18n.T("settings.workflows.select_workflow"))
				return
			}
			a.renderWorkflowEditor(c, theme)
		})
	})

	if active {
		a.renderWorkflowStepsCard(c, theme)
	}
}

// renderWorkflowEditor draws the fields of the drafted workflow.
func (a *AppController) renderWorkflowEditor(c *ui.Context, theme *ui.Theme) {
	se := a.settingsEditor()
	draft := seGet(se, func(s *settingsEditorState) config.Workflow { return s.workflowDraft })

	settingsInlineRow(c, theme, i18n.T("settings.field.id"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.workflows.id"), "summarize-selection", draft.ID); changed {
			seSet(se, func(s *settingsEditorState) { s.workflowDraft.ID = strings.TrimSpace(v) })
		}
	})
	settingsInlineRow(c, theme, i18n.T("settings.field.name"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.workflows.name"), i18n.T("settings.workflows.name_ph"), draft.Name); changed {
			seSet(se, func(s *settingsEditorState) { s.workflowDraft.Name = v })
		}
	})
	settingsLabelled(c, theme, i18n.T("settings.field.description"), func() {
		if v, changed := settingsTextArea(c, i18n.T("settings.workflows.description"), i18n.T("settings.workflows.description_ph"), draft.Description, 2); changed {
			seSet(se, func(s *settingsEditorState) { s.workflowDraft.Description = v })
		}
	})

	ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
		if enabled, changed := settingsInlineSwitch(c, theme, i18n.T("settings.workflows.enabled"), draft.Enabled); changed {
			seSet(se, func(s *settingsEditorState) { s.workflowDraft.Enabled = enabled })
		}
	})

	draft = seGet(se, func(s *settingsEditorState) config.Workflow { return s.workflowDraft })
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.source"), "source", "",
		workflowSourceOptions(), string(draft.Source)); changed {
		seSet(se, func(s *settingsEditorState) { s.workflowDraft.Source = config.WorkflowSource(v) })
	}
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.output"), "output", "",
		workflowOutputOptions(), string(draft.Output)); changed {
		seSet(se, func(s *settingsEditorState) { s.workflowDraft.Output = config.WorkflowOutput(v) })
	}
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.failure"), "failure", "",
		workflowFailureOptions(), string(draft.Failure)); changed {
		seSet(se, func(s *settingsEditorState) { s.workflowDraft.Failure = config.WorkflowFailureMode(v) })
	}
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.execution"), "execution", "",
		workflowExecutionOptions(), string(draft.Execution)); changed {
		seSet(se, func(s *settingsEditorState) { s.workflowDraft.Execution = config.WorkflowExecutionMode(v) })
	}

	// A non-sensitive preview: only counts and modes, never commands, prompts
	// or clipboard contents.
	enabledSteps := 0
	for _, step := range draft.Steps {
		if step.Enabled {
			enabledSteps++
		}
	}
	ui.Box(c).
		FillWidth().
		Padding(space4, space8).
		Radius(radiusChip).
		Background(theme.Background).
		Border(1, theme.Border).
		Children(func() {
			ui.Text(c, i18n.T("settings.workflows.preview")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, i18n.Tf("settings.workflows.preview_summary", len(draft.Steps), enabledSteps)).
				FontSize(typeLabel).
				TextColor(theme.Text).
				SingleLine()
		})

	if msg := seGet(se, func(s *settingsEditorState) string { return s.workflowError }); msg != "" {
		kitErrorBanner(c, theme, msg)
	}

	confirming := seGet(se, func(s *settingsEditorState) bool { return s.workflowConfirmDelete })
	if confirming {
		target := seGet(se, func(s *settingsEditorState) string { return s.workflowSelected })
		if target == "" {
			target = draft.ID
		}
		kitErrorBanner(c, theme, i18n.Tf("settings.workflows.delete_confirm", target))
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, i18n.T("settings.workflows.confirm_delete")).Clicked() {
				a.deleteWorkflowDraft()
			}
			if ui.Button(c, i18n.T("settings.workflows.delete_cancel")).Clicked() {
				a.cancelDeleteWorkflow()
			}
		})
	}

	settingsDivider(c)
	ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, i18n.T("settings.workflows.save")).Clicked() {
			a.saveWorkflowDraft()
		}
		if !confirming && ui.Button(c, i18n.T("settings.workflows.delete")).
			Disabled(seGet(se, func(s *settingsEditorState) bool { return s.workflowIsNew })).
			Clicked() {
			a.requestDeleteWorkflow()
		}
	})
}

// renderWorkflowStepsCard draws the step list of the drafted workflow and the
// editor of the selected step. Steps persist with Save Workflow.
func (a *AppController) renderWorkflowStepsCard(c *ui.Context, theme *ui.Theme) {
	se := a.settingsEditor()
	draft := seGet(se, func(s *settingsEditorState) config.Workflow { return s.workflowDraft })
	steps := draft.Steps
	index := seGet(se, func(s *settingsEditorState) int { return s.workflowStepIndex })
	if len(steps) == 0 {
		index = -1
		if seGet(se, func(s *settingsEditorState) int { return s.workflowStepIndex }) != -1 {
			seSet(se, func(s *settingsEditorState) { s.workflowStepIndex = -1 })
		}
	} else if index < 0 || index >= len(steps) {
		index = 0
		seSet(se, func(s *settingsEditorState) { s.workflowStepIndex = index })
	}

	settingsEditorCard(c, theme, i18n.Tf("settings.workflows.steps_title", len(steps)), func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, i18n.T("settings.workflows.add_step")).Clicked() {
				a.addWorkflowStep()
			}
			settingsHint(c, theme, i18n.T("settings.workflows.steps_hint"))
		})
		settingsDivider(c)
		if len(steps) == 0 {
			kitEmptyState(c, theme, "text.badge.checkmark", i18n.T("settings.workflows.empty_steps"), "")
			return
		}
		for i := range steps {
			step := steps[i]
			row := settingsListRow(c, theme, fmt.Sprintf("workflow-step:%d", i), i == index)
			row.Children(func() {
				ui.Text(c, workflowStepTitle(step, i)).FontSize(typeLabel).FontWeight(600).TextColor(accentInk(theme, theme.Surface)).SingleLine().Shrink(0)
				if step.Action != "" {
					ui.Text(c, step.Action).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
				} else {
					ui.Spacer(c)
				}
				if !step.Enabled {
					chip(c, theme, "", i18n.T("settings.workflows.disabled"), chipNeutral).Shrink(0)
				}
			})
			if row.Clicked() {
				seSet(se, func(s *settingsEditorState) { s.workflowStepIndex = i })
			}
		}
		settingsDivider(c)
		if index >= 0 && index < len(steps) {
			a.renderWorkflowStepEditor(c, theme, index)
		}
	})
}

// renderWorkflowStepEditor draws the fields of one drafted step.
func (a *AppController) renderWorkflowStepEditor(c *ui.Context, theme *ui.Theme, index int) {
	se := a.settingsEditor()
	step := seGet(se, func(s *settingsEditorState) config.WorkflowStep {
		if index < len(s.workflowDraft.Steps) {
			return s.workflowDraft.Steps[index]
		}
		return config.WorkflowStep{}
	})

	ui.Text(c, i18n.T("settings.workflows.step_editor")).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()

	settingsInlineRow(c, theme, i18n.T("settings.field.id"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.workflows.step_id"), "step-a", step.ID); changed {
			setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.ID = strings.TrimSpace(v) })
		}
	})
	settingsInlineRow(c, theme, i18n.T("settings.field.name"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.workflows.step_name"), i18n.T("settings.workflows.step_name_ph"), step.Name); changed {
			setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.Name = v })
		}
	})
	settingsInlineRow(c, theme, i18n.T("settings.field.action"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.workflows.step_action"), i18n.T("settings.workflows.step_action_ph"), step.Action); changed {
			setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.Action = strings.TrimSpace(v) })
		}
	})
	settingsInlineRow(c, theme, i18n.T("settings.field.input"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.workflows.step_input"), i18n.T("settings.workflows.step_input_ph"), step.Input); changed {
			setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.Input = v })
		}
	})
	ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
		if enabled, changed := settingsInlineSwitch(c, theme, i18n.T("settings.workflows.step_enabled"), step.Enabled); changed {
			setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.Enabled = enabled })
		}
	})

	step = seGet(se, func(s *settingsEditorState) config.WorkflowStep {
		if index < len(s.workflowDraft.Steps) {
			return s.workflowDraft.Steps[index]
		}
		return config.WorkflowStep{}
	})
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.step_source"), "step.source", "",
		workflowSourceOptions(), string(step.Source)); changed {
		setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.Source = config.WorkflowSource(v) })
	}
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.step_output"), "step.output", "",
		workflowStepOutputOptions(), string(step.Output)); changed {
		setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.Output = config.WorkflowOutput(v) })
	}
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.step_failure"), "step.failure", "",
		workflowFailureOptions(), string(step.Failure)); changed {
		setWorkflowStep(se, index, func(s *config.WorkflowStep) {
			s.Failure = config.WorkflowFailureMode(v)
			s.FailureInherited = false
		})
	}
	if v, changed := settingChoiceRow(c, theme, i18n.T("settings.workflows.step_execution"), "step.execution", "",
		workflowExecutionOptions(), string(step.Execution)); changed {
		setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.Execution = config.WorkflowExecutionMode(v) })
	}
	settingsInlineRow(c, theme, i18n.T("settings.field.depends_on"), settingsFieldLabelWidth, func() {
		if v, changed := settingsTextInput(c, i18n.T("settings.workflows.step_depends_on"), i18n.T("settings.workflows.step_depends_ph"), strings.Join(step.DependsOn, ", ")); changed {
			setWorkflowStep(se, index, func(s *config.WorkflowStep) { s.DependsOn = splitTokens(v) })
		}
	})

	settingsDivider(c)
	ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, i18n.T("settings.workflows.step_delete")).Clicked() {
			a.deleteWorkflowStep()
		}
		settingsHint(c, theme, i18n.T("settings.workflows.steps_hint"))
	})
}
