package uiapp

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

const (
	fieldSavedDuration     = 1500 * time.Millisecond
	fieldSavedFadeDuration = 250 * time.Millisecond
	maxFailureBanners      = 2
)

type settingsNotice struct {
	Text string
	Tone chipTone
}

type settingsFailure struct {
	Key      string
	PageKey  string
	File     string
	FullPath string
	Field    string
	Reason   string
	Retry    func()
}

func settingsFieldID(pageKey, key string) string {
	if pageKey == "" {
		return key
	}
	return pageKey + "." + key
}

func (a *AppController) currentSettingsPageKey() string {
	a.mu.Lock()
	page := a.uiState.settingsPage
	a.mu.Unlock()
	return normalizeSettingsPageKey(page)
}

func (a *AppController) setPageNotice(pageKey, text string, tone chipTone) {
	if a == nil || strings.TrimSpace(pageKey) == "" {
		return
	}
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		if s.notices == nil {
			s.notices = make(map[string]settingsNotice)
		}
		if strings.TrimSpace(text) == "" {
			delete(s.notices, pageKey)
		} else {
			s.notices[pageKey] = settingsNotice{Text: text, Tone: tone}
		}
	})
	a.requestRepaint()
}

func (a *AppController) clearPageNotice(pageKey string) {
	if a == nil || pageKey == "" {
		return
	}
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		delete(s.notices, pageKey)
	})
}

func (a *AppController) pageNotice(pageKey string) (settingsNotice, bool) {
	if a == nil || pageKey == "" {
		return settingsNotice{}, false
	}
	se := a.settingsEditor()
	se.mu.Lock()
	defer se.mu.Unlock()
	if se.notices == nil {
		return settingsNotice{}, false
	}
	n, ok := se.notices[pageKey]
	return n, ok
}

func (a *AppController) requestRepaint() {
	if a == nil {
		return
	}
	a.mu.Lock()
	onRepaint := a.OnRepaint
	a.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

func formatFailureReason(err error, fullPath, file string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if fullPath != "" && file != "" {
		msg = strings.ReplaceAll(msg, fullPath, file)
	}
	return msg
}

func (a *AppController) recordSaveFailure(
	pageKey string,
	file config.ConfigFileKind,
	fieldKey string,
	fieldLabel string,
	err error,
	retry func(),
) {
	if a == nil || err == nil {
		return
	}
	if strings.TrimSpace(pageKey) == "" {
		pageKey = a.currentSettingsPageKey()
	}
	fileName := string(file)
	if fileName == "" {
		fileName = string(config.FileMain)
	}
	dir := ""
	if a.ConfigStore != nil {
		dir = strings.TrimSpace(a.ConfigStore.Directory())
	}
	fullPath := fileName
	if dir != "" {
		fullPath = filepath.Join(dir, fileName)
	}
	if strings.TrimSpace(fieldLabel) == "" {
		fieldLabel = fieldKey
	}
	if strings.TrimSpace(fieldLabel) == "" {
		fieldLabel = fileName
	}
	entryKey := fileName + "|" + strings.TrimSpace(fieldKey)
	failure := settingsFailure{
		Key:      entryKey,
		PageKey:  pageKey,
		File:     fileName,
		FullPath: fullPath,
		Field:    fieldLabel,
		Reason:   formatFailureReason(err, fullPath, fileName),
		Retry:    retry,
	}
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		if s.failures == nil {
			s.failures = make(map[string]settingsFailure)
		}
		if _, exists := s.failures[entryKey]; !exists {
			s.failureKeys = append(s.failureKeys, entryKey)
		}
		s.failures[entryKey] = failure
	})
	a.requestRepaint()
}

// clearSaveFailure removes a failure entry matching file and fieldKey, or all
// entries for file when fieldKey == "". Other failed fields remain visible.
func (a *AppController) clearSaveFailure(file config.ConfigFileKind, fieldKey string) {
	if a == nil {
		return
	}
	fileName := string(file)
	trimmedField := strings.TrimSpace(fieldKey)
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		if len(s.failures) == 0 {
			return
		}
		if trimmedField != "" {
			key := fileName + "|" + trimmedField
			if _, ok := s.failures[key]; ok {
				delete(s.failures, key)
				s.failureKeys = removeFailureKey(s.failureKeys, key)
			}
			return
		}
		kept := s.failureKeys[:0]
		for _, k := range s.failureKeys {
			if f, ok := s.failures[k]; ok && f.File == fileName {
				delete(s.failures, k)
				continue
			}
			kept = append(kept, k)
		}
		s.failureKeys = kept
	})
}

func (a *AppController) dismissSaveFailure(key string) {
	if a == nil || key == "" {
		return
	}
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		delete(s.failures, key)
		s.failureKeys = removeFailureKey(s.failureKeys, key)
	})
	a.requestRepaint()
}

func removeFailureKey(keys []string, target string) []string {
	out := keys[:0]
	for _, k := range keys {
		if k != target {
			out = append(out, k)
		}
	}
	return out
}

func (a *AppController) hasFailureForPage(pageKey string) bool {
	if a == nil || pageKey == "" {
		return false
	}
	se := a.settingsEditor()
	return seGet(se, func(s *settingsEditorState) bool {
		for _, k := range s.failureKeys {
			if f, ok := s.failures[k]; ok && f.PageKey == pageKey {
				return true
			}
		}
		return false
	})
}

func (a *AppController) failuresForPage(pageKey string) []settingsFailure {
	if a == nil || pageKey == "" {
		return nil
	}
	se := a.settingsEditor()
	return seGet(se, func(s *settingsEditorState) []settingsFailure {
		var out []settingsFailure
		for _, k := range s.failureKeys {
			if f, ok := s.failures[k]; ok && f.PageKey == pageKey {
				out = append(out, f)
			}
		}
		return out
	})
}

// RevealLocalPath reveals path in the OS file manager (Finder / Explorer).
func (a *AppController) RevealLocalPath(path string) error {
	cleanPath := filepath.Clean(path)
	if a != nil {
		a.mu.Lock()
		fn := a.RevealPath
		openFn := a.OpenPath
		a.mu.Unlock()
		if fn != nil {
			return fn(cleanPath)
		}
		if openFn != nil && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
			return openFn(filepath.Dir(cleanPath))
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", cleanPath).Start()
	case "windows":
		return exec.Command("explorer.exe", "/select,"+cleanPath).Start()
	default:
		return a.openLocalPath(filepath.Dir(cleanPath))
	}
}

func (a *AppController) markFieldSaved(c *ui.Context, fieldID string) {
	if a == nil || c == nil || strings.TrimSpace(fieldID) == "" {
		return
	}
	exp := c.Now().Add(fieldSavedDuration)
	se := a.settingsEditor()
	seSet(se, func(s *settingsEditorState) {
		if s.fieldSaved == nil {
			s.fieldSaved = make(map[string]time.Time)
		}
		s.fieldSaved[fieldID] = exp
	})
	c.After(fieldSavedDuration)
}

func renderFieldSavedBadge(c *ui.Context, theme *ui.Theme, se *settingsEditorState, fieldID string) {
	if c == nil || se == nil || fieldID == "" {
		return
	}
	now := c.Now()
	se.mu.Lock()
	var exp time.Time
	var ok bool
	if se.fieldSaved != nil {
		exp, ok = se.fieldSaved[fieldID]
	}
	se.mu.Unlock()
	if !ok {
		return
	}
	remaining := exp.Sub(now)
	if remaining <= 0 {
		seSet(se, func(s *settingsEditorState) {
			delete(s.fieldSaved, fieldID)
		})
		return
	}
	alpha := float32(1)
	if !c.Preferences().ReduceMotion {
		if remaining > fieldSavedFadeDuration {
			c.After(remaining - fieldSavedFadeDuration)
		} else {
			alpha = float32(remaining) / float32(fieldSavedFadeDuration)
			if alpha < 0 {
				alpha = 0
			}
			c.After(16 * time.Millisecond)
		}
	} else {
		c.After(remaining)
	}
	row := ui.Row(c).Key("settings-saved:" + fieldID).Gap(space2).AlignItems(ui.Center).Shrink(0)
	if alpha < 1 {
		row.Opacity(alpha)
	}
	row.Children(func() {
		renderSemanticIcon(c, "checkmark", typeMeta, theme.TextMuted)
		ui.Text(c, i18n.T("settings.field.saved")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
	})
}

func (a *AppController) updateMainConfigField(
	c *ui.Context,
	pageKey string,
	fieldKey string,
	fieldLabel string,
	mutate func(m *config.MainConfig),
) error {
	err := a.updateMainConfigInner(mutate, pageKey, fieldKey, fieldLabel, false)
	if err != nil {
		var retryFn func()
		retryFn = func() {
			if retryErr := a.updateMainConfigField(nil, pageKey, fieldKey, fieldLabel, mutate); retryErr == nil {
				a.clearSaveFailure(config.FileMain, fieldKey)
			}
		}
		a.recordSaveFailure(pageKey, config.FileMain, fieldKey, fieldLabel, err, retryFn)
		return err
	}
	a.clearSaveFailure(config.FileMain, fieldKey)
	if c != nil {
		a.markFieldSaved(c, settingsFieldID(pageKey, fieldKey))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Immediate setting row wrappers with inline saved feedback & failure capture
// ---------------------------------------------------------------------------

func (a *AppController) settingRowWithFeedback(
	c *ui.Context,
	theme *ui.Theme,
	pageKey, fieldKey, title, caption, help string,
	control func() ui.Element,
) {
	fieldID := settingsFieldID(pageKey, fieldKey)
	se := a.settingsEditor()
	row := ui.Row(c).Key("settings-field:"+pageKey+":"+fieldKey).FillWidth().MinHeight(settingRowHeight).Gap(space12).AlignItems(ui.Center).Padding(space4, 0)
	row.Children(func() {
		name := settingRowName(c, theme, title, caption, help)
		ui.Row(c).Gap(space6).AlignItems(ui.Center).Shrink(0).Children(func() {
			renderFieldSavedBadge(c, theme, se, fieldID)
			ctrl := control()
			ctrl.LabelledBy(name)
		})
	})
	a.applyFieldAnchor(c, theme, a.Config(), row, pageKey, fieldKey)
}

func (a *AppController) settingStackedRowWithFeedback(
	c *ui.Context,
	theme *ui.Theme,
	pageKey, fieldKey, title, caption, help string,
	control func() ui.Element,
) {
	fieldID := settingsFieldID(pageKey, fieldKey)
	se := a.settingsEditor()
	row := ui.Column(c).Key("settings-field:"+pageKey+":"+fieldKey).FillWidth().Gap(space6).Padding(space2, 0)
	row.Children(func() {
		var name ui.Element
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			name = settingRowName(c, theme, title, caption, help)
			renderFieldSavedBadge(c, theme, se, fieldID)
		})
		ctrl := control()
		ctrl.LabelledBy(name)
	})
	a.applyFieldAnchor(c, theme, a.Config(), row, pageKey, fieldKey)
}

func (a *AppController) immediateSwitchRow(
	c *ui.Context,
	theme *ui.Theme,
	pageKey, title, key, help string,
	on bool,
	mutate func(m *config.MainConfig, v bool),
) {
	value, changed := on, false
	a.settingRowWithFeedback(c, theme, pageKey, key, title, key, help, func() ui.Element {
		sw := ui.Switch(c, &value)
		changed = sw.Changed()
		return sw
	})
	if changed {
		chosen := value
		_ = a.updateMainConfigField(c, pageKey, key, title, func(m *config.MainConfig) {
			mutate(m, chosen)
		})
	}
}

func (a *AppController) immediateNumberRow(
	c *ui.Context,
	theme *ui.Theme,
	pageKey, title, key, help string,
	value, lo, hi, step float64,
	unit string,
	mutate func(m *config.MainConfig, v float64),
) bool {
	next, changed := value, false
	shown := formatSettingNumber(value, step, unit)
	a.settingRowWithFeedback(c, theme, pageKey, key, title, settingCaption(key, shown), help, func() ui.Element {
		var st ui.Element
		ui.Row(c).Gap(space6).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, shown).FontSize(typeBody).TextColor(theme.Text).SingleLine()
			v := value
			st = ui.Stepper(c, &v, lo, hi, step)
			if st.Changed() {
				next, changed = v, true
			}
		})
		return st
	})
	if changed {
		return a.updateMainConfigField(c, pageKey, key, title, func(m *config.MainConfig) {
			mutate(m, next)
		}) == nil
	}
	return false
}

func (a *AppController) immediateSliderRow(
	c *ui.Context,
	theme *ui.Theme,
	pageKey, title, key, help string,
	value, lo, hi, step float64,
	mutate func(m *config.MainConfig, v float64),
) {
	next, changed := value, false
	shown := formatSettingNumber(value*100, step*100, "%")
	a.settingRowWithFeedback(c, theme, pageKey, key, title, settingCaption(key, shown), help, func() ui.Element {
		v := value
		slider := ui.StepSlider(c, &v, lo, hi, step).MinWidth(space32 * 4).Grow(0)
		if slider.Changed() {
			next, changed = clampSetting(v, lo, hi), true
		}
		return slider
	})
	if changed {
		_ = a.updateMainConfigField(c, pageKey, key, title, func(m *config.MainConfig) {
			mutate(m, next)
		})
	}
}

func (a *AppController) immediateChoiceRow(
	c *ui.Context,
	theme *ui.Theme,
	pageKey, title, key, help string,
	options []settingOption,
	current string,
	mutate func(m *config.MainConfig, v string),
) {
	chosen, changed := current, false
	a.settingRowWithFeedback(c, theme, pageKey, key, title, settingCaption(key, current), help, func() ui.Element {
		labels := make([]string, len(options))
		for i, o := range options {
			labels[i] = o.label
		}
		if len(options) <= 6 {
			picked := current
			control, value, ok := kitSegmentedPicker(c, theme, labels, labelsForValue(options, current))
			if ok {
				for _, o := range options {
					if o.label == value {
						picked = o.value
						break
					}
				}
				chosen, changed = picked, true
			}
			return control.Shrink(0)
		}
		sel := -1
		for i, o := range options {
			if strings.EqualFold(o.value, current) {
				sel = i
			}
		}
		seg := ui.Segmented(c, &sel, labels...).Shrink(0)
		if seg.Changed() && sel >= 0 && sel < len(options) && sel != -1 {
			chosen, changed = options[sel].value, true
		}
		return seg
	})
	if changed {
		_ = a.updateMainConfigField(c, pageKey, key, title, func(m *config.MainConfig) {
			mutate(m, chosen)
		})
	}
}

func (a *AppController) immediateSelectRow(
	c *ui.Context,
	theme *ui.Theme,
	pageKey, title, key, help string,
	options []settingOption,
	current string,
	width float32,
	mutate func(m *config.MainConfig, v string),
) {
	chosen, changed := current, false
	layout := a.settingRowWithFeedback
	if width <= 0 {
		layout = a.settingStackedRowWithFeedback
	}
	layout(c, theme, pageKey, key, title, settingCaption(key, current), help, func() ui.Element {
		labels := make([]string, 0, len(options)+1)
		curLabel := ""
		for _, o := range options {
			labels = append(labels, o.label)
			if o.value == current {
				curLabel = o.label
			}
		}
		if curLabel == "" {
			curLabel = current
			labels = append(labels, current)
		}
		v := curLabel
		sel := ui.Select(c, &v, labels)
		if width > 0 {
			sel.Width(width).Shrink(0)
		} else {
			sel.FillWidth()
		}
		if sel.Changed() && v != curLabel {
			for _, o := range options {
				if o.label == v {
					chosen, changed = o.value, true
				}
			}
		}
		return sel
	})
	if changed {
		_ = a.updateMainConfigField(c, pageKey, key, title, func(m *config.MainConfig) {
			mutate(m, chosen)
		})
	}
}

// ---------------------------------------------------------------------------
// Persistent failure banners, delete confirmation, unsaved switch & empty states
// ---------------------------------------------------------------------------

func (a *AppController) renderSettingsFailureBanners(c *ui.Context, theme *ui.Theme, pageKey string) {
	failures := a.failuresForPage(pageKey)
	if len(failures) == 0 {
		return
	}
	visible := failures
	extra := 0
	if len(visible) > maxFailureBanners {
		extra = len(visible) - maxFailureBanners
		visible = visible[:maxFailureBanners]
	}
	background := theme.Danger.Alpha(alphaSubtle)
	foreground := readableOn(theme.Danger, theme.Text, background.Over(theme.Surface), minTextContrast)
	ui.Column(c).Key("settings-failure-banners:" + pageKey).FillWidth().Gap(space6).Children(func() {
		for _, f := range visible {
			item := f
			ui.Row(c).Key("failure-banner:"+item.Key).FillWidth().Padding(space8, space10).Radius(radius8).Gap(space8).
				AlignItems(ui.Center).Background(background).Border(space1, theme.Danger.Alpha(alphaHalf)).Children(func() {
				renderSemanticIcon(c, "xmark", IconSize16, foreground)
				ui.Column(c).Grow(1).MinWidth(0).Gap(space2).Children(func() {
					ui.Text(c, i18n.Tf("settings.failure.title", item.Field, item.Reason)).
						FontSize(typeBody).FontWeight(500).TextColor(foreground).Wrap()
					ui.Text(c, item.FullPath).FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
				})
				ui.Row(c).Gap(space6).AlignItems(ui.Center).Shrink(0).Children(func() {
					if item.Retry != nil {
						if ui.Button(c, i18n.T("settings.failure.retry")).Key("failure-retry:"+item.Key).Padding(space2, space8).Clicked() {
							item.Retry()
						}
					}
					if ui.Button(c, i18n.T("settings.failure.reveal_finder")).Key("failure-reveal:"+item.Key).Padding(space2, space8).Clicked() {
						_ = a.RevealLocalPath(item.FullPath)
					}
					if ui.Button(c, "×").Key("failure-dismiss:"+item.Key).Label(i18n.T("settings.failure.dismiss")).Padding(space2, space6).Clicked() {
						a.dismissSaveFailure(item.Key)
					}
				})
			})
		}
		if extra > 0 {
			ui.Text(c, i18n.Tf("settings.failure.more", extra)).FontSize(typeMeta).TextColor(foreground).Wrap()
		}
	})
}

func kitDeleteConfirmRow(
	c *ui.Context,
	theme *ui.Theme,
	key string,
	prompt string,
	confirmLabel string,
	cancelLabel string,
	onCancel func(),
	onConfirm func(),
) {
	if confirmLabel == "" {
		confirmLabel = i18n.T("settings.delete.confirm_action")
	}
	if cancelLabel == "" {
		cancelLabel = i18n.T("settings.delete.cancel")
	}
	well := kitInsetWell(c, theme, func() {
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "exclamationmark.triangle", IconSize16, kitStateWarningColor(theme))
			ui.Text(c, prompt).FontSize(typeBody).TextColor(theme.Text).Wrap().Grow(1).MinWidth(0)
			ui.Row(c).Gap(space6).AlignItems(ui.Center).Shrink(0).Children(func() {
				cancelBtn := ui.Button(c, cancelLabel).Key(key + "-cancel").AutoFocus()
				cancelBtn.HandleInput(func(ev ui.InputEvent) bool {
					if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape && onCancel != nil {
						onCancel()
						return true
					}
					return false
				})
				if cancelBtn.Clicked() && onCancel != nil {
					onCancel()
				}
				if ui.Button(c, confirmLabel).Key(key+"-confirm").Clicked() && onConfirm != nil {
					onConfirm()
				}
			})
		})
	})
	well.Key(key).Label(key).HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape && onCancel != nil {
			onCancel()
			return true
		}
		return false
	})
}

func kitUnsavedSwitchBanner(
	c *ui.Context,
	theme *ui.Theme,
	key string,
	itemName string,
	onSave func(),
	onDiscard func(),
	onKeep func(),
) {
	well := kitInsetWell(c, theme, func() {
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Wrap().Children(func() {
			kitStatusDot(c, theme.Accent, false)
			ui.Text(c, i18n.Tf("settings.draft.unsaved_named", itemName)).
				FontSize(typeBody).FontWeight(500).TextColor(theme.Text).Wrap().Grow(1).MinWidth(0)
			ui.Row(c).Gap(space6).AlignItems(ui.Center).Shrink(0).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.draft.save")).Key(key+"-save").Clicked() && onSave != nil {
					onSave()
				}
				if ui.Button(c, i18n.T("settings.draft.discard_short")).Key(key+"-discard").Clicked() && onDiscard != nil {
					onDiscard()
				}
				keepBtn := ui.Button(c, i18n.T("settings.draft.keep_editing")).Key(key + "-keep").AutoFocus()
				keepBtn.HandleInput(func(ev ui.InputEvent) bool {
					if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape && onKeep != nil {
						onKeep()
						return true
					}
					return false
				})
				if keepBtn.Clicked() && onKeep != nil {
					onKeep()
				}
			})
		})
	})
	well.Key(key).Label(key).HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape && onKeep != nil {
			onKeep()
			return true
		}
		return false
	})
}

func kitEmptyStateWithAction(
	c *ui.Context,
	theme *ui.Theme,
	icon string,
	title string,
	body string,
	actionLabel string,
	actionKey string,
	onAction func(),
) {
	ui.Column(c).FillWidth().Padding(space24).Gap(space8).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		renderSemanticIcon(c, icon, IconSize24, theme.TextMuted)
		ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(theme.Text).SingleLine()
		if body != "" {
			ui.Text(c, body).FontSize(typeBody).TextColor(theme.TextMuted).TextAlign(ui.Center).Wrap()
		}
		if actionLabel != "" && onAction != nil {
			if ui.PrimaryButton(c, actionLabel).Key(actionKey).Clicked() {
				onAction()
			}
		}
	})
}

func settingsEditorCardWithDirty(c *ui.Context, theme *ui.Theme, title string, dirty bool, key string, body func()) {
	kitCard(c, theme, cardFlat, func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
			if dirty {
				ui.Box(c).Key(key).Label(key).Children(func() {
					kitStatusDot(c, theme.Accent, false)
				})
			}
		})
		kitDivider(c, theme)
		body()
	})
}

// ---------------------------------------------------------------------------
// Dirty detection across all draft-based Settings editors & window state
// ---------------------------------------------------------------------------

func normalizeStringsSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return in
}

func normalizeSnippetForCompare(s config.SnippetItem) config.SnippetItem {
	out := s
	out.AppBundleIDs = normalizeStringsSlice(s.AppBundleIDs)
	out.ProcessNames = normalizeStringsSlice(s.ProcessNames)
	return out
}

func normalizeGlossaryForCompare(g config.GlossaryTerm) config.GlossaryTerm {
	out := g
	out.Aliases = normalizeStringsSlice(g.Aliases)
	return out
}

func normalizeLeaderNodeForCompare(n config.LeaderNode) config.LeaderNode {
	out := n
	out.Action = normalizeLeaderActionForCompare(n.Action)
	return out
}

func normalizeLeaderActionForCompare(a config.LeaderAction) config.LeaderAction {
	out := a
	if len(a.Args) == 0 {
		out.Args = nil
	} else {
		out.Args = append([]string(nil), a.Args...)
	}
	if len(a.Children) == 0 {
		out.Children = nil
	} else {
		out.Children = make([]config.LeaderNode, len(a.Children))
		for i, ch := range a.Children {
			out.Children[i] = normalizeLeaderNodeForCompare(ch)
		}
	}
	return out
}

func normalizePromptForCompare(p config.PromptTemplate) config.PromptTemplate {
	out := clonePromptTemplate(p)
	if len(out.Slots) == 0 {
		out.Slots = nil
	}
	if len(out.AppBundleIDs) == 0 {
		out.AppBundleIDs = nil
	}
	return out
}

func (a *AppController) snippetPageDirty(cfg config.ClavisConfig) bool {
	st := seGet(a.settingsEditor(), func(s *settingsEditorState) struct {
		isNew    bool
		selected string
		draft    config.SnippetItem
	} {
		return struct {
			isNew    bool
			selected string
			draft    config.SnippetItem
		}{s.snippetIsNew, s.snippetSelected, s.snippetDraft}
	})
	if st.isNew {
		return st.draft.ID != "" ||
			st.draft.Trigger != "" ||
			st.draft.Description != "" ||
			st.draft.Content != "" ||
			!st.draft.WordBoundary ||
			len(st.draft.AppBundleIDs) > 0 ||
			len(st.draft.ProcessNames) > 0
	}
	if st.selected == "" {
		return false
	}
	for _, item := range cfg.Snippets.Items {
		if item.ID == st.selected {
			return !reflect.DeepEqual(normalizeSnippetForCompare(item), normalizeSnippetForCompare(st.draft))
		}
	}
	return true
}

func (a *AppController) glossaryPageDirty(cfg config.ClavisConfig) bool {
	st := seGet(a.settingsEditor(), func(s *settingsEditorState) struct {
		isNew    bool
		selected string
		draft    config.GlossaryTerm
	} {
		return struct {
			isNew    bool
			selected string
			draft    config.GlossaryTerm
		}{s.glossaryIsNew, s.glossarySelected, s.glossaryDraft}
	})
	if st.isNew {
		return st.draft.Term != "" ||
			st.draft.Category != "" ||
			st.draft.Hint != "" ||
			len(st.draft.Aliases) > 0
	}
	if st.selected == "" {
		return false
	}
	for _, term := range cfg.Glossary.Terms {
		if term.Term == st.selected {
			return !reflect.DeepEqual(normalizeGlossaryForCompare(term), normalizeGlossaryForCompare(st.draft))
		}
	}
	return true
}

func (a *AppController) workflowPageDirty(cfg config.ClavisConfig) bool {
	st := seGet(a.settingsEditor(), func(s *settingsEditorState) struct {
		loaded     bool
		isNew      bool
		selected   string
		originalID string
		draft      config.Workflow
	} {
		return struct {
			loaded     bool
			isNew      bool
			selected   string
			originalID string
			draft      config.Workflow
		}{s.workflowLoaded, s.workflowIsNew, s.workflowSelected, s.workflowOriginalID, cloneWorkflow(s.workflowDraft)}
	})
	if st.isNew {
		emptyDefault := normalizeWorkflow(config.Workflow{Enabled: true})
		return !reflect.DeepEqual(emptyDefault, normalizeWorkflow(st.draft))
	}
	if !st.loaded || st.selected == "" {
		return false
	}
	targetID := st.originalID
	if targetID == "" {
		targetID = st.selected
	}
	for _, item := range cfg.Workflows.Workflows {
		if item.ID == targetID {
			return !reflect.DeepEqual(normalizeWorkflow(cloneWorkflow(item)), normalizeWorkflow(st.draft))
		}
	}
	return true
}

func (a *AppController) leaderNodeDirty(cfg config.ClavisConfig) bool {
	st := seGet(a.settingsEditor(), func(s *settingsEditorState) struct {
		loaded   bool
		selected []string
		draft    config.LeaderNode
	} {
		return struct {
			loaded   bool
			selected []string
			draft    config.LeaderNode
		}{s.leaderLoaded, append([]string(nil), s.leaderSelected...), s.leaderDraft}
	})
	if !st.loaded || len(st.selected) == 0 {
		return false
	}
	saved, ok := findLeaderNode(cfg.Leader.Nodes, st.selected)
	if !ok {
		return true
	}
	return !reflect.DeepEqual(normalizeLeaderNodeForCompare(saved), normalizeLeaderNodeForCompare(st.draft))
}

func (a *AppController) leaderPageDirty(cfg config.ClavisConfig) bool {
	hasPendingForms := seGet(a.settingsEditor(), func(s *settingsEditorState) bool {
		return s.leaderNewKey != "" || s.leaderNewLabel != "" || s.leaderChildKey != "" || s.leaderChildLbl != ""
	})
	return hasPendingForms || a.leaderNodeDirty(cfg)
}

func (a *AppController) templatesPageDirty(cfg config.ClavisConfig) bool {
	ts := a.templatesUI()
	ts.mu.Lock()
	loaded := ts.promptLoaded
	isNew := ts.promptIsNew
	origID := ts.promptOriginalID
	selID := ts.promptSelectedID
	draft := clonePromptTemplate(ts.promptDraft)
	ts.mu.Unlock()
	if isNew {
		return draft.ID != "" ||
			draft.Title != "" ||
			draft.Trigger != "" ||
			draft.Description != "" ||
			draft.Template != "" ||
			draft.InjectGlossary ||
			len(draft.AppBundleIDs) > 0 ||
			(draft.Category != "" && draft.Category != "general") ||
			draft.OptimizerPreset != cfg.Prompts.DefaultOptimizerPreset
	}
	if !loaded || selID == "" {
		return false
	}
	lookup := origID
	if lookup == "" {
		lookup = selID
	}
	for _, item := range cfg.Prompts.Items {
		if item.ID == lookup {
			return !reflect.DeepEqual(normalizePromptForCompare(item), normalizePromptForCompare(draft))
		}
	}
	return true
}

func (a *AppController) aiPageDirty() bool {
	return seGet(a.settingsEditor(), func(s *settingsEditorState) bool {
		p := s.aiDraftProfile
		return strings.TrimSpace(p.ID) != "" ||
			strings.TrimSpace(p.Provider) != "" ||
			strings.TrimSpace(p.BaseURL) != "" ||
			strings.TrimSpace(p.Model) != "" ||
			strings.TrimSpace(p.Name) != ""
	})
}

func (a *AppController) settingsPageDirty(pageKey string) bool {
	if a == nil || a.ConfigStore == nil {
		return false
	}
	cfg := a.ConfigStore.Current()
	switch pageKey {
	case "leader":
		return a.leaderPageDirty(cfg)
	case "snippets":
		return a.snippetPageDirty(cfg)
	case "glossary":
		return a.glossaryPageDirty(cfg)
	case "workflows":
		return a.workflowPageDirty(cfg)
	case "templates":
		return a.templatesPageDirty(cfg)
	case "ai":
		return a.aiPageDirty()
	default:
		return false
	}
}

func (a *AppController) anySettingsPageDirty() bool {
	if a == nil || a.ConfigStore == nil {
		return false
	}
	cfg := a.ConfigStore.Current()
	return a.snippetPageDirty(cfg) ||
		a.glossaryPageDirty(cfg) ||
		a.workflowPageDirty(cfg) ||
		a.leaderPageDirty(cfg) ||
		a.templatesPageDirty(cfg) ||
		a.aiPageDirty()
}

func (a *AppController) syncDocumentEdited() {
	if a == nil {
		return
	}
	dirty := a.anySettingsPageDirty()
	a.mu.Lock()
	changed := dirty != a.lastDocumentEdited
	if changed {
		a.lastDocumentEdited = dirty
	}
	cb := a.OnDocumentEdited
	a.mu.Unlock()
	if changed && cb != nil {
		cb(dirty)
	}
}

func (a *AppController) handleSettingsRootEscape() bool {
	se := a.settingsEditor()
	ts := a.templatesUI()

	if seGet(se, func(s *settingsEditorState) bool { return s.recordingShortcut != "" }) {
		seSet(se, func(s *settingsEditorState) { s.recordingShortcut = "" })
		return true
	}

	canceledDelete := seGet(se, func(s *settingsEditorState) bool {
		return s.leaderConfirmDelete ||
			s.snippetConfirmDelete ||
			s.glossaryConfirmDelete ||
			s.workflowConfirmDelete ||
			s.aiProfileConfirmDelete != "" ||
			s.apiKeyConfirmDelete
	})
	if canceledDelete {
		seSet(se, func(s *settingsEditorState) {
			s.leaderConfirmDelete = false
			s.snippetConfirmDelete = false
			s.glossaryConfirmDelete = false
			s.workflowConfirmDelete = false
			s.aiProfileConfirmDelete = ""
			s.apiKeyConfirmDelete = false
		})
		return true
	}

	canceledSwitch := seGet(se, func(s *settingsEditorState) bool {
		return s.leaderPendingSwitch ||
			s.snippetPendingSwitch ||
			s.glossaryPendingSwitch ||
			s.workflowPendingSwitch ||
			s.promptPendingSwitch
	})
	if canceledSwitch {
		seSet(se, func(s *settingsEditorState) {
			s.leaderPendingSwitch = false
			s.leaderPendingPath = nil
			s.snippetPendingSwitch = false
			s.snippetPendingID = ""
			s.snippetPendingNew = false
			s.glossaryPendingSwitch = false
			s.glossaryPendingTerm = ""
			s.glossaryPendingNew = false
			s.workflowPendingSwitch = false
			s.workflowPendingID = ""
			s.workflowPendingNew = false
			s.promptPendingSwitch = false
			s.promptPendingID = ""
			s.promptPendingNew = false
		})
		return true
	}

	_ = ts
	return false
}

// editorEntityName formats a readable label for prompts/confirmations.
func editorEntityName(primary, fallback string) string {
	if s := strings.TrimSpace(primary); s != "" {
		return s
	}
	return strings.TrimSpace(fallback)
}

func (a *AppController) settingsPageLabel(pageKey string) string {
	if meta, ok := settingsPageByKey(pageKey); ok {
		return meta.Label
	}
	return pageKey
}

func (a *AppController) activeConfirmAny() bool {
	return seGet(a.settingsEditor(), func(s *settingsEditorState) bool {
		return s.leaderConfirmDelete ||
			s.snippetConfirmDelete ||
			s.glossaryConfirmDelete ||
			s.workflowConfirmDelete ||
			s.aiProfileConfirmDelete != "" ||
			s.apiKeyConfirmDelete
	})
}

func settingsConfirmPromptForFile(name string, file config.ConfigFileKind) string {
	return i18n.Tf("settings.delete.confirm_file", name, string(file))
}

func formatSettingsErrorLabel(pageKey, label string) string {
	if strings.TrimSpace(label) != "" {
		return label
	}
	return pageKey
}
