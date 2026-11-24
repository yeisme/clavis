package uiapp

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/prompt"
	"github.com/yeisme/clavis/pkg/core/template"
)

const (
	templatesSubTabLocal   = 0
	templatesSubTabCatalog = 1
)

// templatesUIState holds per-controller state for Settings -> Templates & Prompts.
// It is owned by AppController.subStates (controller_substates.go), never a package-global map.
type templatesUIState struct {
	mu sync.Mutex

	activeSubTab int

	// Section A: Local Prompts (prompts.toml) visual editor
	promptQuery      string
	promptSelectedID string
	promptOriginalID string
	promptIsNew      bool
	promptLoaded     bool
	promptDraft      config.PromptTemplate
	promptError      string
	prefixDraft      string
	prefixLoaded     bool

	// Section B: Catalog Template Library (pkg/core/template.Library)
	catalogDir       string
	libraryDir       string
	catalogLoaded    bool
	lastRefreshAt    time.Time
	catalogSnap      *template.Catalog
	importedItems    []template.Item
	diagnostics      []template.Diagnostic
	selectedKey      string
	overlayDraft     string
	overlayLoadedKey string
	overlayError     string
	libraryStatus    string
	libraryError     string
}

func (a *AppController) templatesUI() *templatesUIState {
	return a.templatesUIStateFor()
}

// SetTemplatesSettingsSubTab switches between Local Prompts (0) and Catalog Library (1).
func (a *AppController) SetTemplatesSettingsSubTab(tab int) {
	if tab != templatesSubTabCatalog {
		tab = templatesSubTabLocal
	}
	st := a.templatesUI()
	st.mu.Lock()
	st.activeSubTab = tab
	st.mu.Unlock()
	if tab == templatesSubTabCatalog {
		st.mu.Lock()
		loaded := st.catalogLoaded
		st.mu.Unlock()
		if !loaded {
			_ = a.ReloadTemplateLibrary()
		}
	}
}

// SetTemplateLibraryDir overrides the on-disk CAS/imports/overlays directory used by
// the Templates Settings page and contract body resolution.
func (a *AppController) SetTemplateLibraryDir(dir string) {
	st := a.templatesUI()
	st.mu.Lock()
	st.libraryDir = strings.TrimSpace(dir)
	st.catalogLoaded = false
	st.mu.Unlock()
}

// TemplateLibraryDir returns the resolved on-disk template library root directory.
func (a *AppController) TemplateLibraryDir() string {
	st := a.templatesUI()
	st.mu.Lock()
	override := strings.TrimSpace(st.libraryDir)
	st.mu.Unlock()
	if override != "" {
		return override
	}
	if a != nil && a.ConfigStore != nil && strings.TrimSpace(a.ConfigStore.Directory()) != "" {
		return filepath.Join(a.ConfigStore.Directory(), "templates")
	}
	return filepath.Join(defaultConfigDir(), "templates")
}

// SetTemplateCatalogDir sets the local promptrepo catalog source directory path.
func (a *AppController) SetTemplateCatalogDir(dir string) {
	st := a.templatesUI()
	st.mu.Lock()
	st.catalogDir = strings.TrimSpace(dir)
	st.mu.Unlock()
}

// TemplateLibrarySnapshot returns a copy of the cached catalog snapshot, imported items,
// and diagnostics held by the controller's Templates UI state.
func (a *AppController) TemplateLibrarySnapshot() (*template.Catalog, []template.Item, []template.Diagnostic) {
	st := a.templatesUI()
	st.mu.Lock()
	defer st.mu.Unlock()
	var snapCopy *template.Catalog
	if st.catalogSnap != nil {
		cp := *st.catalogSnap
		snapCopy = &cp
	}
	items := append([]template.Item(nil), st.importedItems...)
	diags := append([]template.Diagnostic(nil), st.diagnostics...)
	return snapCopy, items, diags
}

func (a *AppController) openTemplateLibrary() (*template.Library, error) {
	dir := a.TemplateLibraryDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return template.OpenLibrary(dir)
}

// RefreshTemplateCatalog verifies a local promptrepo catalog directory into the
// library snapshot and reloads imported CAS entries & diagnostics.
func (a *AppController) RefreshTemplateCatalog(catalogDir string) []template.Diagnostic {
	dir := strings.TrimSpace(catalogDir)
	st := a.templatesUI()
	if dir == "" {
		st.mu.Lock()
		dir = strings.TrimSpace(st.catalogDir)
		st.mu.Unlock()
	}
	if dir == "" {
		msg := i18n.T("settings.templates.catalog.dir_required")
		st.mu.Lock()
		st.libraryError = msg
		st.libraryStatus = ""
		st.mu.Unlock()
		a.setSettingsStatus(msg)
		return nil
	}

	lib, err := a.openTemplateLibrary()
	if err != nil {
		msg := i18n.Tf("settings.templates.catalog.open_failed", err)
		st.mu.Lock()
		st.libraryError = msg
		st.libraryStatus = ""
		st.mu.Unlock()
		a.setSettingsStatus(msg)
		return nil
	}
	defer lib.Close()

	refreshDiags := lib.Refresh(dir)
	snap, snapDiags := lib.SnapshotCatalog()
	items, viewDiags := lib.View(nil)

	allDiags := make([]template.Diagnostic, 0, len(refreshDiags)+len(snapDiags)+len(viewDiags))
	allDiags = append(allDiags, refreshDiags...)
	if snap == nil {
		allDiags = append(allDiags, snapDiags...)
	}
	allDiags = append(allDiags, viewDiags...)

	status := ""
	errMsg := ""
	if snap != nil && len(refreshDiags) == 0 {
		status = i18n.Tf("settings.templates.catalog.refreshed", len(template.DefaultSolutions(*snap)))
	} else if len(refreshDiags) > 0 {
		errMsg = template.RenderDiagnosticString(refreshDiags[0])
	}

	st.mu.Lock()
	st.catalogDir = dir
	st.catalogLoaded = true
	st.lastRefreshAt = time.Now()
	st.catalogSnap = snap
	st.importedItems = items
	st.diagnostics = allDiags
	st.libraryStatus = status
	st.libraryError = errMsg
	st.overlayLoadedKey = ""
	st.mu.Unlock()

	if errMsg != "" {
		a.setSettingsStatus(errMsg)
	} else if status != "" {
		a.setSettingsStatus(status)
	}
	return allDiags
}

// ReloadTemplateLibrary reloads the stored snapshot, imported CAS items, and diagnostics
// without modifying the catalog snapshot on disk.
func (a *AppController) ReloadTemplateLibrary() []template.Diagnostic {
	st := a.templatesUI()
	lib, err := a.openTemplateLibrary()
	if err != nil {
		msg := i18n.Tf("settings.templates.catalog.open_failed", err)
		st.mu.Lock()
		st.libraryError = msg
		st.mu.Unlock()
		return nil
	}
	defer lib.Close()

	snap, snapDiags := lib.SnapshotCatalog()
	items, viewDiags := lib.View(nil)
	var allDiags []template.Diagnostic
	if snap == nil && len(items) > 0 {
		allDiags = append(allDiags, snapDiags...)
	}
	allDiags = append(allDiags, viewDiags...)

	st.mu.Lock()
	st.catalogLoaded = true
	st.catalogSnap = snap
	st.importedItems = items
	st.diagnostics = allDiags
	st.overlayLoadedKey = ""
	st.mu.Unlock()
	return allDiags
}

// ImportTemplateVariant imports (or updates) one canonical catalog variant into CAS.
func (a *AppController) ImportTemplateVariant(packageID, solutionID, role, locale string) []template.Diagnostic {
	st := a.templatesUI()
	st.mu.Lock()
	dir := strings.TrimSpace(st.catalogDir)
	st.mu.Unlock()
	if dir == "" {
		msg := i18n.T("settings.templates.catalog.dir_required")
		st.mu.Lock()
		st.libraryError = msg
		st.mu.Unlock()
		a.setSettingsStatus(msg)
		return nil
	}

	lib, err := a.openTemplateLibrary()
	if err != nil {
		msg := i18n.Tf("settings.templates.catalog.open_failed", err)
		st.mu.Lock()
		st.libraryError = msg
		st.mu.Unlock()
		a.setSettingsStatus(msg)
		return nil
	}
	defer lib.Close()

	importDiags := lib.Import(dir, packageID, solutionID, role, locale, template.SourceFile)
	snap, _ := lib.SnapshotCatalog()
	items, viewDiags := lib.View(nil)

	allDiags := append(append([]template.Diagnostic(nil), importDiags...), viewDiags...)
	stem := solutionID + "." + role + "." + locale

	status := ""
	errMsg := ""
	if len(importDiags) == 0 {
		status = i18n.Tf("settings.templates.catalog.imported_ok", stem)
	} else {
		errMsg = template.RenderDiagnosticString(importDiags[0])
	}

	st.mu.Lock()
	st.catalogLoaded = true
	st.catalogSnap = snap
	st.importedItems = items
	st.diagnostics = allDiags
	st.selectedKey = stem
	st.overlayLoadedKey = ""
	st.libraryStatus = status
	st.libraryError = errMsg
	st.mu.Unlock()

	if errMsg != "" {
		a.setSettingsStatus(errMsg)
	} else if status != "" {
		a.setSettingsStatus(status)
	}
	return allDiags
}

// RemoveTemplateImport removes an imported variant record and refreshes the library view.
func (a *AppController) RemoveTemplateImport(solutionID, role, locale string) []template.Diagnostic {
	st := a.templatesUI()
	lib, err := a.openTemplateLibrary()
	if err != nil {
		msg := i18n.Tf("settings.templates.catalog.open_failed", err)
		st.mu.Lock()
		st.libraryError = msg
		st.mu.Unlock()
		a.setSettingsStatus(msg)
		return nil
	}
	defer lib.Close()

	remDiags := lib.RemoveImport(solutionID, role, locale)
	snap, _ := lib.SnapshotCatalog()
	items, viewDiags := lib.View(nil)
	allDiags := append(append([]template.Diagnostic(nil), remDiags...), viewDiags...)
	stem := solutionID + "." + role + "." + locale

	status := i18n.Tf("settings.templates.catalog.removed_ok", stem)
	errMsg := ""
	if len(remDiags) > 0 {
		errMsg = template.RenderDiagnosticString(remDiags[0])
		status = ""
	}

	st.mu.Lock()
	st.catalogSnap = snap
	st.importedItems = items
	st.diagnostics = allDiags
	st.overlayLoadedKey = ""
	st.overlayDraft = ""
	st.libraryStatus = status
	st.libraryError = errMsg
	st.mu.Unlock()

	if errMsg != "" {
		a.setSettingsStatus(errMsg)
	} else if status != "" {
		a.setSettingsStatus(status)
	}
	return allDiags
}

// SaveTemplateOverlay validates and persists a local TOML overlay for an imported variant.
func (a *AppController) SaveTemplateOverlay(solutionID, role, locale, tomlBody string) []template.Diagnostic {
	st := a.templatesUI()
	lib, err := a.openTemplateLibrary()
	if err != nil {
		msg := i18n.Tf("settings.templates.catalog.open_failed", err)
		st.mu.Lock()
		st.overlayError = msg
		st.mu.Unlock()
		return nil
	}
	defer lib.Close()

	saveDiags := lib.SaveOverlay(solutionID, role, locale, []byte(tomlBody))
	snap, _ := lib.SnapshotCatalog()
	items, viewDiags := lib.View(nil)
	allDiags := append(append([]template.Diagnostic(nil), saveDiags...), viewDiags...)

	errMsg := ""
	status := ""
	if len(saveDiags) > 0 {
		errMsg = template.RenderDiagnosticString(saveDiags[0])
	} else {
		status = i18n.T("settings.templates.overlay.saved")
	}

	st.mu.Lock()
	st.catalogSnap = snap
	st.importedItems = items
	st.diagnostics = allDiags
	st.overlayDraft = tomlBody
	st.overlayError = errMsg
	st.libraryStatus = status
	st.mu.Unlock()

	if errMsg != "" {
		a.setSettingsStatus(errMsg)
	} else if status != "" {
		a.setSettingsStatus(status)
	}
	return saveDiags
}

// ResetTemplateOverlay deletes the local TOML overlay for an imported variant.
func (a *AppController) ResetTemplateOverlay(solutionID, role, locale string) []template.Diagnostic {
	st := a.templatesUI()
	lib, err := a.openTemplateLibrary()
	if err != nil {
		msg := i18n.Tf("settings.templates.catalog.open_failed", err)
		st.mu.Lock()
		st.overlayError = msg
		st.mu.Unlock()
		return nil
	}
	defer lib.Close()

	delDiags := lib.DeleteOverlay(solutionID, role, locale)
	snap, _ := lib.SnapshotCatalog()
	items, viewDiags := lib.View(nil)

	errMsg := ""
	status := ""
	if len(delDiags) > 0 {
		errMsg = template.RenderDiagnosticString(delDiags[0])
	} else {
		status = i18n.T("settings.templates.overlay.reset_done")
	}

	st.mu.Lock()
	st.catalogSnap = snap
	st.importedItems = items
	st.diagnostics = append(append([]template.Diagnostic(nil), delDiags...), viewDiags...)
	st.overlayDraft = ""
	st.overlayError = errMsg
	st.libraryStatus = status
	st.mu.Unlock()

	if status != "" {
		a.setSettingsStatus(status)
	}
	return delDiags
}

// renderSettingsTemplatesSection renders Settings -> Templates & Prompts.
func (a *AppController) renderSettingsTemplatesSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	st := a.templatesUI()
	st.mu.Lock()
	activeTab := st.activeSubTab
	st.mu.Unlock()

	// Top sub-tab bar switching between Local Prompts (prompts.toml) and Catalog Library.
	ui.Row(c).Key("templates-subtabs").FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
		renderTab := func(key, label string, target int) {
			active := activeTab == target
			btn := ui.ButtonBase(c).Key(key).Label(label).Padding(space4, space10).Radius(radiusChip).FocusRing(false)
			if active {
				btn.Background(theme.Accent.Alpha(alphaSubtle)).Border(space1, theme.Accent.Alpha(alphaHalf))
			} else {
				btn.Background(tileFill(theme)).Border(space1, hairline(theme))
			}
			fg := secondaryInk(theme)
			if active {
				fg = accentInk(theme, surfaceBase(theme))
			}
			btn.Children(func() {
				ui.Text(c, label).FontSize(typeLabel).FontWeight(600).TextColor(fg).SingleLine()
			})
			if btn.Clicked() {
				a.SetTemplatesSettingsSubTab(target)
			}
		}
		renderTab("templates-tab-local", i18n.T("settings.templates.tab.local"), templatesSubTabLocal)
		renderTab("templates-tab-catalog", i18n.T("settings.templates.tab.catalog"), templatesSubTabCatalog)
	})

	if activeTab == templatesSubTabCatalog {
		a.renderCatalogTemplateLibraryTab(c, theme)
		return
	}
	a.renderLocalPromptsTab(c, theme, cfg)
}

func optimizerPresetSettingOptions() []settingOption {
	presets := prompt.BuiltInPresets()
	out := make([]settingOption, 0, len(presets))
	for _, p := range presets {
		key := "optimizer.preset." + p.ID
		label := i18n.T(key)
		if label == "" || label == key {
			label = p.Name
		}
		out = append(out, settingOption{value: p.ID, label: label})
	}
	return out
}

func clonePromptTemplate(tpl config.PromptTemplate) config.PromptTemplate {
	cp := tpl
	cp.AppBundleIDs = append([]string(nil), tpl.AppBundleIDs...)
	if tpl.Slots != nil {
		cp.Slots = make([]config.PromptSlot, len(tpl.Slots))
		for i, sl := range tpl.Slots {
			slCopy := sl
			slCopy.Options = append([]string(nil), sl.Options...)
			cp.Slots[i] = slCopy
		}
	}
	return cp
}

func promptPlaceholders() []string {
	return []string{"{{selection}}", "{{clipboard}}", "{{app}}", "{{cursor}}"}
}

func (a *AppController) renderLocalPromptsTab(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	st := a.templatesUI()
	items := cfg.Prompts.Items

	// 1. Prompt optimizer defaults card (DefaultOptimizerPreset & InlineOptimizePrefix).
	settingsSourceCard(c, theme, i18n.T("settings.templates.defaults.card"), "prompts.toml", func() {
		currentPreset := strings.TrimSpace(cfg.Prompts.DefaultOptimizerPreset)
		if currentPreset == "" {
			currentPreset = prompt.PresetCoding
		}
		if v, changed := settingSelectRow(
			c, theme,
			i18n.T("settings.templates.defaults.preset"),
			"default_optimizer_preset",
			i18n.T("settings.templates.defaults.preset_help"),
			optimizerPresetSettingOptions(),
			currentPreset,
			180,
		); changed {
			updated := cfg.Prompts
			updated.DefaultOptimizerPreset = v
			_ = a.SavePromptsConfig(updated)
		}

		st.mu.Lock()
		if !st.prefixLoaded {
			st.prefixDraft = cfg.Prompts.InlineOptimizePrefix
			st.prefixLoaded = true
		}
		prefixDraft := st.prefixDraft
		st.mu.Unlock()

		ui.Row(c).Key("prompts-inline-prefix-row").FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			name := settingRowName(c, theme, i18n.T("settings.templates.defaults.prefix"), settingCaption("inline_optimize_prefix", cfg.Prompts.InlineOptimizePrefix), "")
			val := prefixDraft
			in := ui.TextInput(c.Key("prompts-inline-prefix-input"), &val).
				Label(i18n.T("settings.templates.defaults.prefix")).
				Placeholder(i18n.T("settings.templates.defaults.prefix_ph")).
				Width(100).
				Shrink(0)
			in.LabelledBy(name)
			if in.Changed() {
				st.mu.Lock()
				st.prefixDraft = val
				st.mu.Unlock()
				prefixDraft = val
			}
			if ui.Button(c, i18n.T("settings.templates.defaults.save_prefix")).Key("prompts-apply-prefix").Shrink(0).Clicked() {
				next := strings.TrimSpace(prefixDraft)
				if next == "" {
					next = "//"
				}
				updated := cfg.Prompts
				updated.InlineOptimizePrefix = next
				if err := a.SavePromptsConfig(updated); err == nil {
					st.mu.Lock()
					st.prefixDraft = next
					st.mu.Unlock()
				}
			}
		})
	})

	// Reconcile local prompt selection.
	st.mu.Lock()
	selectedID := st.promptSelectedID
	isNew := st.promptIsNew
	loaded := st.promptLoaded
	st.mu.Unlock()

	if !isNew {
		if selectedID == "" && len(items) > 0 {
			selectedID = items[0].ID
			st.mu.Lock()
			st.promptSelectedID = selectedID
			st.promptOriginalID = selectedID
			st.promptDraft = clonePromptTemplate(items[0])
			st.promptLoaded = true
			st.promptError = ""
			st.mu.Unlock()
			loaded = true
		} else if selectedID != "" {
			idx := slices.IndexFunc(items, func(p config.PromptTemplate) bool { return p.ID == selectedID })
			if idx < 0 {
				selectedID = ""
				st.mu.Lock()
				st.promptSelectedID = ""
				st.promptOriginalID = ""
				st.promptLoaded = false
				st.mu.Unlock()
				loaded = false
			} else if !loaded {
				st.mu.Lock()
				st.promptOriginalID = items[idx].ID
				st.promptDraft = clonePromptTemplate(items[idx])
				st.promptLoaded = true
				st.mu.Unlock()
				loaded = true
			}
		}
	}

	// 2. Master-detail Local Prompts CRUD.
	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(space10).FillWidth().Children(func() {
		// Left card: Master prompt list.
		settingsEditorCard(c, theme, i18n.Tf("settings.templates.local.list_title", len(items)), func() {
			st.mu.Lock()
			query := st.promptQuery
			st.mu.Unlock()
			search := ui.SearchField(c.Key("local-prompts-search"), &query).
				Label(i18n.T("settings.templates.local.search_ph")).
				Placeholder(i18n.T("settings.templates.local.search_ph")).
				FillWidth()
			if search.Changed() {
				st.mu.Lock()
				st.promptQuery = query
				st.mu.Unlock()
			}
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.templates.local.new")).Key("local-prompt-new").Clicked() {
					st.mu.Lock()
					st.promptIsNew = true
					st.promptSelectedID = ""
					st.promptOriginalID = ""
					st.promptLoaded = true
					st.promptError = ""
					st.promptDraft = config.PromptTemplate{
						Category:        "general",
						OptimizerPreset: cfg.Prompts.DefaultOptimizerPreset,
					}
					st.mu.Unlock()
				}
				settingsHint(c, theme, i18n.T("settings.templates.local.select_hint"))
			})
			settingsDivider(c)
			qLower := strings.ToLower(strings.TrimSpace(query))
			matches := 0
			for i := range items {
				item := items[i]
				if qLower != "" {
					hay := strings.ToLower(item.ID + " " + item.Title + " " + item.Trigger + " " + item.Category + " " + item.Description)
					if !strings.Contains(hay, qLower) {
						continue
					}
				}
				matches++
				row := settingsListRow(c, theme, "local-prompt:"+item.ID, !isNew && item.ID == selectedID)
				row.Children(func() {
					ui.Text(c, nonEmptyOr(item.Trigger, item.ID)).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Gap(space1).Children(func() {
						ui.Text(c, nonEmptyOr(item.Title, item.ID)).FontSize(typeLabel).TextColor(theme.Text).SingleLine()
						if item.Category != "" {
							ui.Text(c, item.Category).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						}
					})
					slotCount := len(prompt.ParseTemplate(item.Template).Slots(item))
					ui.Text(c, i18n.Tf("settings.templates.prompt.slots_count", slotCount)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().Shrink(0)
				})
				if row.Clicked() {
					st.mu.Lock()
					st.promptIsNew = false
					st.promptSelectedID = item.ID
					st.promptOriginalID = item.ID
					st.promptDraft = clonePromptTemplate(item)
					st.promptLoaded = true
					st.promptError = ""
					st.mu.Unlock()
				}
			}
			if matches == 0 {
				kitEmptyState(c, theme, "doc.text", i18n.T("settings.templates.local.empty"), "")
			}
		})

		// Right card: Detail editor + {{slot}} live preview.
		settingsEditorCard(c, theme, i18n.T("settings.templates.local.editor_title"), func() {
			st.mu.Lock()
			draft := clonePromptTemplate(st.promptDraft)
			draftIsNew := st.promptIsNew
			promptErr := st.promptError
			hasDraft := st.promptLoaded || st.promptIsNew
			st.mu.Unlock()

			if !hasDraft {
				settingsHint(c, theme, i18n.T("settings.templates.local.select_hint"))
				return
			}

			settingsInlineRow(c, theme, i18n.T("settings.field.id"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.templates.prompt.id"), i18n.T("settings.templates.prompt.id_ph"), draft.ID); changed {
					st.mu.Lock()
					st.promptDraft.ID = v
					st.promptError = ""
					st.mu.Unlock()
					draft.ID = v
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.name"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.templates.prompt.name"), i18n.T("settings.templates.prompt.name_ph"), draft.Title); changed {
					st.mu.Lock()
					st.promptDraft.Title = v
					st.promptError = ""
					st.mu.Unlock()
					draft.Title = v
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.trigger"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.templates.prompt.trigger"), i18n.T("settings.templates.prompt.trigger_ph"), draft.Trigger); changed {
					st.mu.Lock()
					st.promptDraft.Trigger = v
					st.promptError = ""
					st.mu.Unlock()
					draft.Trigger = v
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.category"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.templates.prompt.category"), i18n.T("settings.templates.prompt.category_ph"), draft.Category); changed {
					st.mu.Lock()
					st.promptDraft.Category = v
					st.mu.Unlock()
					draft.Category = v
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.description"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.templates.prompt.description"), i18n.T("settings.templates.prompt.description_ph"), draft.Description); changed {
					st.mu.Lock()
					st.promptDraft.Description = v
					st.mu.Unlock()
					draft.Description = v
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.templates.prompt.preset"), settingsFieldLabelWidth, func() {
				presetOpts := []string{"", prompt.PresetCoding, prompt.PresetConcise, prompt.PresetDeepResearch, prompt.PresetEnglishPro, prompt.PresetPolite}
				if v, changed := settingsSelect(c, presetOpts, draft.OptimizerPreset); changed {
					st.mu.Lock()
					st.promptDraft.OptimizerPreset = v
					st.mu.Unlock()
					draft.OptimizerPreset = v
				}
			})
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				name := ui.Text(c, i18n.T("settings.templates.prompt.inject_glossary")).
					FontSize(typeMeta).TextColor(theme.TextMuted).Width(settingsFieldLabelWidth).SingleLine()
				inj := draft.InjectGlossary
				if ui.Switch(c, &inj).LabelledBy(name).Changed() {
					st.mu.Lock()
					st.promptDraft.InjectGlossary = inj
					st.mu.Unlock()
					draft.InjectGlossary = inj
				}
			})

			content := draft.Template
			area := ui.TextArea(c.Key("local-prompt-content"), &content).
				Label(i18n.T("settings.templates.prompt.content")).
				Placeholder(i18n.T("settings.templates.prompt.content_ph")).
				Lines(3, 8).
				FillWidth()
			if area.Changed() {
				st.mu.Lock()
				st.promptDraft.Template = content
				st.mu.Unlock()
				draft.Template = content
			}

			// Built-in placeholder chips.
			ui.Row(c).FillWidth().Wrap().Gap(space4).Children(func() {
				for _, chip := range promptPlaceholders() {
					ph := chip
					if ui.Button(c, ph).Key("prompt-chip:"+ph).FontSize(typeMicro).Padding(space2, space6).Clicked() {
						st.mu.Lock()
						st.promptDraft.Template += ph
						draft.Template = st.promptDraft.Template
						st.mu.Unlock()
					}
				}
			})

			// Detected {{slot}} preview.
			parsed := prompt.ParseTemplate(draft.Template)
			detectedSlots := parsed.Slots(draft)
			ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
				if len(detectedSlots) == 0 {
					ui.Text(c, i18n.T("settings.templates.prompt.no_slots")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
				} else {
					ui.Text(c, i18n.Tf("settings.templates.prompt.slots_detected", len(detectedSlots))).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
					for _, sl := range detectedSlots {
						kitMetaChip(c, theme, "{{"+sl.Name+"}}")
					}
				}
			})

			sampleVals := make(map[string]string, len(detectedSlots))
			for _, sl := range detectedSlots {
				if sl.Default != "" {
					sampleVals[sl.Name] = sl.Default
				} else {
					sampleVals[sl.Name] = "<" + sl.Name + ">"
				}
			}
			rendered := prompt.RenderPrompt(draft, sampleVals, prompt.RenderContext{
				App:       "Preview",
				Selection: "selection",
				Clipboard: "clipboard",
			})
			ui.Box(c).
				FillWidth().
				Padding(space6, space8).
				Radius(radiusChip).
				Background(theme.Background).
				Border(space1, hairline(theme)).
				Children(func() {
					ui.Column(c).FillWidth().Gap(space2).Children(func() {
						ui.Text(c, i18n.T("settings.templates.prompt.preview")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						ui.Text(c, rendered.RenderedText).FontSize(typeLabel).TextColor(theme.Text).Wrap()
					})
				})

			if promptErr != "" {
				kitErrorBanner(c, theme, promptErr)
			}

			ui.Row(c).FillWidth().Wrap().Gap(space8).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.templates.prompt.save")).Key("local-prompt-save").Clicked() {
					a.saveLocalPromptDraft()
				}
				if ui.Button(c, i18n.T("settings.templates.prompt.open_composer")).Key("local-prompt-compose").Disabled(strings.TrimSpace(draft.Template) == "").Clicked() {
					a.OpenPromptComposer(draft, engine.CapsuleContext{})
				}
				if ui.Button(c, i18n.T("settings.templates.prompt.delete")).Key("local-prompt-delete").Disabled(draftIsNew).Clicked() {
					a.deleteLocalPromptDraft()
				}
			})
		})
	})
}

func (a *AppController) saveLocalPromptDraft() {
	st := a.templatesUI()
	st.mu.Lock()
	draft := clonePromptTemplate(st.promptDraft)
	isNew := st.promptIsNew
	originalID := st.promptOriginalID
	st.mu.Unlock()

	draft.ID = strings.TrimSpace(draft.ID)
	draft.Title = strings.TrimSpace(draft.Title)
	draft.Trigger = strings.TrimSpace(draft.Trigger)
	draft.Category = strings.TrimSpace(draft.Category)
	draft.Description = strings.TrimSpace(draft.Description)

	if draft.ID == "" || draft.Title == "" || draft.Trigger == "" {
		msg := i18n.T("settings.templates.prompt.err_required")
		st.mu.Lock()
		st.promptError = msg
		st.mu.Unlock()
		return
	}

	// Recompute declared slots from placeholders while preserving existing slot metadata.
	draft.Slots = prompt.ParseTemplate(draft.Template).Slots(draft)
	if draft.AppBundleIDs == nil {
		draft.AppBundleIDs = []string{}
	}

	cfg := a.ConfigStore.Current()
	promptsCfg := cfg.Prompts
	nextItems := make([]config.PromptTemplate, 0, len(promptsCfg.Items)+1)
	replaced := false
	for _, existing := range promptsCfg.Items {
		if !isNew && existing.ID == originalID {
			nextItems = append(nextItems, draft)
			replaced = true
			continue
		}
		if existing.ID == draft.ID {
			msg := i18n.Tf("settings.templates.prompt.err_duplicate_id", draft.ID)
			st.mu.Lock()
			st.promptError = msg
			st.mu.Unlock()
			return
		}
		nextItems = append(nextItems, existing)
	}
	if !replaced {
		nextItems = append(nextItems, draft)
	}
	promptsCfg.Items = nextItems
	if err := a.SavePromptsConfig(promptsCfg); err != nil {
		st.mu.Lock()
		st.promptError = err.Error()
		st.mu.Unlock()
		return
	}

	st.mu.Lock()
	st.promptIsNew = false
	st.promptSelectedID = draft.ID
	st.promptOriginalID = draft.ID
	st.promptDraft = clonePromptTemplate(draft)
	st.promptLoaded = true
	st.promptError = ""
	st.mu.Unlock()
}

func (a *AppController) deleteLocalPromptDraft() {
	st := a.templatesUI()
	st.mu.Lock()
	targetID := st.promptOriginalID
	if targetID == "" {
		targetID = st.promptSelectedID
	}
	st.mu.Unlock()
	if strings.TrimSpace(targetID) == "" {
		return
	}

	cfg := a.ConfigStore.Current()
	promptsCfg := cfg.Prompts
	nextItems := make([]config.PromptTemplate, 0, len(promptsCfg.Items))
	for _, existing := range promptsCfg.Items {
		if existing.ID != targetID {
			nextItems = append(nextItems, existing)
		}
	}
	promptsCfg.Items = nextItems
	if err := a.SavePromptsConfig(promptsCfg); err != nil {
		st.mu.Lock()
		st.promptError = err.Error()
		st.mu.Unlock()
		return
	}

	st.mu.Lock()
	st.promptIsNew = false
	st.promptSelectedID = ""
	st.promptOriginalID = ""
	st.promptLoaded = false
	st.promptDraft = config.PromptTemplate{}
	st.promptError = ""
	st.mu.Unlock()
}

type catalogVariantEntry struct {
	key        string
	solution   template.Solution
	tmpl       template.Template
	imported   *template.Item
	title      string
	summary    string
	updateable bool
}

func collectCatalogVariants(snap *template.Catalog, imported []template.Item) ([]catalogVariantEntry, map[string]*template.Item) {
	byID := make(map[string]*template.Item, len(imported))
	for i := range imported {
		cp := imported[i]
		byID[cp.ID] = &cp
	}
	if snap == nil {
		return nil, byID
	}
	var out []catalogVariantEntry
	for _, s := range template.DefaultSolutions(*snap) {
		for _, tmpl := range s.Templates {
			stem := s.ID + "." + tmpl.Role + "." + tmpl.Locale
			imp := byID[stem]
			title := s.ID
			summary := ""
			if loc, ok := s.Locales[tmpl.Locale]; ok && strings.TrimSpace(loc.Title) != "" {
				title = loc.Title
				summary = loc.Summary
			} else if loc, ok := s.Locales["en"]; ok && strings.TrimSpace(loc.Title) != "" {
				title = loc.Title
				summary = loc.Summary
			}
			updateable := imp != nil && imp.Provenance != nil && (imp.Provenance.Digest != tmpl.Digest || imp.Provenance.Version != s.Version)
			out = append(out, catalogVariantEntry{
				key:        stem,
				solution:   s,
				tmpl:       tmpl,
				imported:   imp,
				title:      title,
				summary:    summary,
				updateable: updateable,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out, byID
}

func shortDigest(digest string) string {
	trimmed := strings.TrimSpace(digest)
	if trimmed == "" {
		return "-"
	}
	if strings.HasPrefix(trimmed, "sha256:") && len(trimmed) > 19 {
		return trimmed[:19] + "…"
	}
	if len(trimmed) > 16 {
		return trimmed[:16] + "…"
	}
	return trimmed
}

func contractStatusPill(status string) (string, chipTone) {
	switch status {
	case "validated":
		return i18n.T("settings.templates.catalog.status_validated"), chipSuccess
	case "contract_invalid":
		return i18n.T("settings.templates.catalog.status_invalid"), chipDanger
	case "header_only_unvalidated":
		return i18n.T("settings.templates.catalog.status_header_only"), chipWarning
	default:
		return i18n.T("settings.templates.catalog.status_no_contract"), chipWarning
	}
}

func (a *AppController) renderCatalogTemplateLibraryTab(c *ui.Context, theme *ui.Theme) {
	st := a.templatesUI()
	st.mu.Lock()
	if !st.catalogLoaded {
		st.mu.Unlock()
		_ = a.ReloadTemplateLibrary()
		st.mu.Lock()
	}
	catalogDir := st.catalogDir
	snap := st.catalogSnap
	imported := append([]template.Item(nil), st.importedItems...)
	diags := append([]template.Diagnostic(nil), st.diagnostics...)
	selectedKey := st.selectedKey
	libStatus := st.libraryStatus
	libErr := st.libraryError
	st.mu.Unlock()

	variants, importedByID := collectCatalogVariants(snap, imported)
	variantByKey := make(map[string]catalogVariantEntry, len(variants))
	for _, v := range variants {
		variantByKey[v.key] = v
	}
	if selectedKey == "" {
		if len(imported) > 0 {
			selectedKey = imported[0].ID
		} else if len(variants) > 0 {
			selectedKey = variants[0].key
		}
		if selectedKey != "" {
			st.mu.Lock()
			st.selectedKey = selectedKey
			st.mu.Unlock()
		}
	}

	// 1. Catalog Source & Snapshot card.
	settingsSourceCard(c, theme, i18n.T("settings.templates.catalog.source_card"), "catalog.snapshot.json", func() {
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			dirVal := catalogDir
			in := ui.TextInput(c.Key("catalog-dir-input"), &dirVal).
				Label(i18n.T("settings.templates.catalog.dir_label")).
				Placeholder(i18n.T("settings.templates.catalog.dir_ph")).
				FillWidth().
				Grow(1)
			if in.Changed() {
				st.mu.Lock()
				st.catalogDir = dirVal
				st.mu.Unlock()
				catalogDir = dirVal
			}
			if ui.PrimaryButton(c, i18n.T("settings.templates.catalog.refresh")).Key("catalog-refresh-btn").Shrink(0).Clicked() {
				_ = a.RefreshTemplateCatalog(catalogDir)
			}
			if ui.Button(c, i18n.T("settings.templates.catalog.reload_library")).Key("catalog-reload-btn").Shrink(0).Clicked() {
				_ = a.ReloadTemplateLibrary()
			}
		})

		if snap == nil {
			settingsHint(c, theme, i18n.T("settings.templates.catalog.no_snapshot"))
		} else {
			repoName := nonEmptyOr(snap.Repository.Name, snap.Repository.ID)
			metaText := i18n.Tf(
				"settings.templates.catalog.snapshot_meta",
				repoName,
				shortDigest(snap.Digest),
				len(template.DefaultSolutions(*snap)),
				len(imported),
			)
			ui.Text(c, metaText).FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
		}
		if libErr != "" {
			kitErrorBanner(c, theme, libErr)
		} else if libStatus != "" {
			kitStatusPill(c, theme, libStatus, chipSuccess)
		}
	})

	// 2. Master-detail Catalog & Imported Templates + Inspector / Overlay Editor.
	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(space10).FillWidth().Children(func() {
		// Left card: Imported + Catalog variants list.
		settingsEditorCard(c, theme, i18n.T("settings.templates.catalog.list_title"), func() {
			if len(imported) == 0 && len(variants) == 0 {
				kitEmptyState(c, theme, "doc.text", i18n.T("settings.templates.catalog.empty_list"), "")
				return
			}
			if len(imported) > 0 {
				ui.Text(c, i18n.Tf("settings.templates.catalog.section_imported", len(imported))).
					FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
				for i := range imported {
					it := imported[i]
					row := settingsListRow(c, theme, "tpl-imported:"+it.ID, selectedKey == it.ID)
					row.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(space1).Children(func() {
							ui.Text(c, nonEmptyOr(it.Title, it.ID)).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
							sub := it.ID
							if it.Provenance != nil {
								sub = it.Provenance.Locale + " · " + shortDigest(it.Provenance.Digest)
							}
							ui.Text(c, sub).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						})
						lbl, tone := contractStatusPill(it.ContractStatus)
						kitStatusPill(c, theme, lbl, tone)
					})
					if row.Clicked() {
						st.mu.Lock()
						st.selectedKey = it.ID
						st.overlayLoadedKey = ""
						st.overlayError = ""
						st.mu.Unlock()
					}
				}
			}

			if len(variants) > 0 {
				if len(imported) > 0 {
					settingsDivider(c)
				}
				ui.Text(c, i18n.Tf("settings.templates.catalog.section_available", len(variants))).
					FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
				for _, v := range variants {
					entry := v
					row := settingsListRow(c, theme, "tpl-catalog:"+entry.key, selectedKey == entry.key)
					row.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(space1).Children(func() {
							ui.Text(c, entry.title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
							ui.Text(c, entry.tmpl.Role+" · "+entry.tmpl.Locale+" · v"+entry.solution.Version).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						})
						switch {
						case entry.updateable:
							kitStatusPill(c, theme, i18n.T("settings.templates.catalog.status_update"), chipWarning)
						case entry.imported != nil:
							kitStatusPill(c, theme, i18n.T("settings.templates.catalog.status_imported"), chipSuccess)
						default:
							kitStatusPill(c, theme, i18n.T("settings.templates.catalog.status_available"), chipNeutral)
						}
					})
					if row.Clicked() {
						st.mu.Lock()
						st.selectedKey = entry.key
						st.overlayLoadedKey = ""
						st.overlayError = ""
						st.mu.Unlock()
					}
				}
			}
		})

		// Right card: Inspector, Contract details, Import/Composer actions, Overlay editor.
		settingsEditorCard(c, theme, i18n.T("settings.templates.catalog.detail_title"), func() {
			imp := importedByID[selectedKey]
			entry, hasVariant := variantByKey[selectedKey]
			if imp == nil && !hasVariant {
				settingsHint(c, theme, i18n.T("settings.templates.catalog.select_hint"))
				return
			}

			// Header badges.
			ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
				title := selectedKey
				if imp != nil {
					title = nonEmptyOr(imp.Title, imp.ID)
				} else if hasVariant {
					title = entry.title
				}
				ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
				if imp != nil {
					lbl, tone := contractStatusPill(imp.ContractStatus)
					kitStatusPill(c, theme, lbl, tone)
					if imp.Orphaned {
						kitStatusPill(c, theme, i18n.T("settings.templates.catalog.status_orphaned"), chipDanger)
					} else if imp.UpdateStatus == "update_available" {
						kitStatusPill(c, theme, i18n.T("settings.templates.catalog.status_update"), chipWarning)
					}
				}
			})

			// Metadata rows.
			var pkgID, solID, version, role, locale, digestStr, contractDigestStr, rights, maturity string
			if imp != nil && imp.Provenance != nil {
				pkgID = imp.Provenance.PackageID
				solID = imp.Provenance.SolutionID
				version = imp.Provenance.Version
				role = imp.Provenance.Role
				locale = imp.Provenance.Locale
				digestStr = imp.Provenance.Digest
				contractDigestStr = imp.Provenance.ContractDigest
				maturity = imp.Maturity
			}
			if hasVariant {
				if pkgID == "" {
					pkgID = entry.solution.PackageID
				}
				if solID == "" {
					solID = entry.solution.ID
				}
				if version == "" {
					version = entry.solution.Version
				}
				if role == "" {
					role = entry.tmpl.Role
				}
				if locale == "" {
					locale = entry.tmpl.Locale
				}
				if digestStr == "" {
					digestStr = entry.tmpl.Digest
				}
				rights = entry.solution.Rights
				if maturity == "" {
					maturity = entry.solution.Maturity
				}
			}

			settingsKeyValueRow(c, theme, i18n.T("settings.templates.catalog.field_ref"), pkgID+"/"+solID+" ("+role+")")
			settingsKeyValueRow(c, theme, i18n.T("settings.templates.catalog.field_version"), version)
			settingsKeyValueRow(c, theme, i18n.T("settings.templates.catalog.field_locale"), locale)
			settingsKeyValueRow(c, theme, i18n.T("settings.templates.catalog.field_digest"), shortDigest(digestStr))
			if contractDigestStr != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.templates.catalog.field_contract_digest"), shortDigest(contractDigestStr))
			}
			if rights != "" || maturity != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.templates.catalog.field_rights"), nonEmptyOr(rights, "-")+" / "+nonEmptyOr(maturity, "-"))
			}

			// Validated Contract summary (read-only).
			if imp != nil && imp.Contract != nil {
				settingsCardDivider(c, theme)
				perms := "-"
				if len(imp.Contract.Permissions) > 0 {
					perms = strings.Join(imp.Contract.Permissions, ", ")
				}
				settingsKeyValueRow(c, theme, i18n.T("settings.templates.catalog.field_permissions"), perms)
				if len(imp.Contract.Inputs) == 0 {
					settingsHint(c, theme, i18n.T("settings.templates.catalog.no_inputs"))
				} else {
					ui.Text(c, i18n.Tf("settings.templates.catalog.contract_inputs", len(imp.Contract.Inputs))).
						FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
					for _, in := range imp.Contract.Inputs {
						desc := in.Type
						if in.Required {
							desc += " · " + i18n.T("prompts.required_badge")
						}
						if len(in.Enum) > 0 {
							desc += " [" + strings.Join(in.Enum, "|") + "]"
						}
						settingsKeyValueRow(c, theme, in.LocalizedLabel(imp.Contract.Locale)+" ("+in.Name+")", desc)
					}
				}
			}

			// Item-specific contract diagnostics.
			if imp != nil && len(imp.ContractDiagnostics) > 0 {
				for _, d := range imp.ContractDiagnostics {
					kitWarningBanner(c, theme, template.RenderDiagnosticString(d))
				}
			}

			// Action bar.
			ui.Row(c).FillWidth().Wrap().Gap(space8).AlignItems(ui.Center).Children(func() {
				if hasVariant {
					importLabel := i18n.T("settings.templates.catalog.action_import")
					if entry.updateable {
						importLabel = i18n.T("settings.templates.catalog.action_update")
					}
					if ui.PrimaryButton(c, importLabel).Key("catalog-import-btn").Clicked() {
						_ = a.ImportTemplateVariant(entry.solution.PackageID, entry.solution.ID, entry.tmpl.Role, entry.tmpl.Locale)
					}
				}
				if imp != nil {
					canOpen := imp.ContractStatus != "contract_invalid"
					if ui.Button(c, i18n.T("settings.templates.catalog.action_composer")).Key("catalog-open-composer-btn").Disabled(!canOpen).Clicked() {
						if imp.Contract != nil {
							a.OpenContractTemplateWithDefaults(*imp.Contract, imp.Body, nonEmptyOr(imp.Title, imp.ID), imp.SlotDefaults, engine.CapsuleContext{})
						} else {
							a.OpenPromptComposer(config.PromptTemplate{
								ID:       imp.ID,
								Trigger:  imp.Trigger,
								Title:    nonEmptyOr(imp.Title, imp.ID),
								Category: imp.Category,
								Template: imp.Body,
							}, engine.CapsuleContext{})
						}
					}
					if imp.Provenance != nil {
						if ui.Button(c, i18n.T("settings.templates.catalog.action_remove")).Key("catalog-remove-btn").Clicked() {
							_ = a.RemoveTemplateImport(imp.Provenance.SolutionID, imp.Provenance.Role, imp.Provenance.Locale)
						}
					}
				}
			})

			// Local TOML Overlay editor for imported variants.
			if imp != nil && imp.Provenance != nil {
				settingsCardDivider(c, theme)
				ui.Text(c, i18n.T("settings.templates.overlay.title")).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
				settingsHint(c, theme, i18n.T("settings.templates.overlay.hint"))

				st.mu.Lock()
				needsLoad := st.overlayLoadedKey != imp.ID
				st.mu.Unlock()
				if needsLoad {
					loadedText := ""
					if lib, err := a.openTemplateLibrary(); err == nil {
						if raw, exists, _ := lib.ReadOverlay(imp.Provenance.SolutionID, imp.Provenance.Role, imp.Provenance.Locale); exists {
							loadedText = string(raw)
						}
						lib.Close()
					}
					st.mu.Lock()
					st.overlayDraft = loadedText
					st.overlayLoadedKey = imp.ID
					st.mu.Unlock()
				}

				st.mu.Lock()
				ovDraft := st.overlayDraft
				ovErr := st.overlayError
				st.mu.Unlock()

				area := ui.TextArea(c.Key("catalog-overlay-editor"), &ovDraft).
					Label(i18n.T("settings.templates.overlay.input_label")).
					Placeholder("title = \"...\"\ntrigger = \"/...\"\nenabled = true").
					Lines(3, 7).
					FillWidth()
				if area.Changed() {
					st.mu.Lock()
					st.overlayDraft = ovDraft
					st.overlayError = ""
					st.mu.Unlock()
				}
				if ovErr != "" {
					kitErrorBanner(c, theme, ovErr)
				}
				ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c, i18n.T("settings.templates.overlay.save")).Key("catalog-overlay-save").Clicked() {
						_ = a.SaveTemplateOverlay(imp.Provenance.SolutionID, imp.Provenance.Role, imp.Provenance.Locale, ovDraft)
					}
					if ui.Button(c, i18n.T("settings.templates.overlay.reset")).Key("catalog-overlay-reset").Clicked() {
						_ = a.ResetTemplateOverlay(imp.Provenance.SolutionID, imp.Provenance.Role, imp.Provenance.Locale)
					}
				})
			}
		})
	})

	// 3. Validation Diagnostics panel.
	settingsEditorCard(c, theme, i18n.Tf("settings.templates.diag.card", len(diags)), func() {
		if len(diags) == 0 {
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				renderSemanticIcon(c, "checkmark", typeLabel, theme.Success)
				ui.Text(c, i18n.T("settings.templates.diag.clean")).FontSize(typeLabel).TextColor(theme.Success).Wrap().Grow(1)
			})
			return
		}
		for i, d := range diags {
			line := fmt.Sprintf("[%d] %s", i+1, template.RenderDiagnosticString(d))
			if d.Code == template.DegradedNoContract || d.Code == template.Orphaned || d.Code == template.BuiltinConflict {
				kitWarningBanner(c, theme, line)
			} else {
				kitErrorBanner(c, theme, line)
			}
		}
	})
}
