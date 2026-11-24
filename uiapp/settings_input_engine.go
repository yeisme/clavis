package uiapp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	internalstorage "github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

type userBlocklistStore interface {
	Words() []string
	Block(ctx context.Context, word string) error
	Unblock(ctx context.Context, word string) error
}

type inputEngineDictLoader func(customPaths []string) (inputengine.DictionaryStatus, []inputengine.DictionaryDiagnostic, error)

type inputEngineUIState struct {
	mu sync.Mutex

	dictLoaded   bool
	dictLoading  bool
	dictPathsKey string
	dictStatus   inputengine.DictionaryStatus
	dictDiags    []inputengine.DictionaryDiagnostic
	dictErr      string
	dictLoader   inputEngineDictLoader

	blocklistStore         userBlocklistStore
	blocklistInitAttempted bool
	blocklistInitLoading   bool
	blocklistUnavailable   bool
	blocklistWords         []string
	blocklistDraft         string
	blocklistErr           string
}

func (a *AppController) inputEngineUI() *inputEngineUIState {
	return a.inputEngineUIStateFor()
}

func (a *AppController) setInputEngineDictLoaderForTest(loader inputEngineDictLoader) {
	st := a.inputEngineUI()
	st.mu.Lock()
	st.dictLoader = loader
	st.dictLoaded = false
	st.dictLoading = false
	st.dictPathsKey = ""
	st.dictStatus = inputengine.DictionaryStatus{}
	st.dictDiags = nil
	st.dictErr = ""
	st.mu.Unlock()
}

func (a *AppController) setUserBlocklistStore(store userBlocklistStore) {
	st := a.inputEngineUI()
	st.mu.Lock()
	st.blocklistStore = store
	st.blocklistInitAttempted = true
	st.blocklistInitLoading = false
	st.blocklistUnavailable = store == nil
	st.blocklistErr = ""
	if store != nil {
		st.blocklistWords = append([]string(nil), store.Words()...)
	} else {
		st.blocklistWords = nil
	}
	st.mu.Unlock()
}

func defaultInputEngineDictLoader(customPaths []string) (inputengine.DictionaryStatus, []inputengine.DictionaryDiagnostic, error) {
	_, status := inputengine.LoadUserDictionary()
	var diags []inputengine.DictionaryDiagnostic
	_, err := inputengine.LoadDictionaryWithDiagnostics(customPaths, "", func(d inputengine.DictionaryDiagnostic) {
		diags = append(diags, d)
	})
	return status, diags, err
}

func (a *AppController) triggerInputEngineDictRefresh(svc ui.Services, customPaths []string, force bool) {
	if a.isClosed() {
		return
	}
	pathsCopy := append([]string(nil), customPaths...)
	pathsKey := strings.Join(pathsCopy, "\x00")
	st := a.inputEngineUI()
	st.mu.Lock()
	if st.dictLoading || (!force && st.dictLoaded && st.dictPathsKey == pathsKey) {
		st.mu.Unlock()
		return
	}
	st.dictLoading = true
	st.dictPathsKey = pathsKey
	loader := st.dictLoader
	st.mu.Unlock()
	if loader == nil {
		loader = defaultInputEngineDictLoader
	}
	lifeCtx := a.lifeCtx()
	go func() {
		status, diags, err := loader(pathsCopy)
		if lifeCtx.Err() != nil {
			st.mu.Lock()
			st.dictLoading = false
			st.mu.Unlock()
			return
		}
		st.mu.Lock()
		st.dictStatus = status
		st.dictDiags = append([]inputengine.DictionaryDiagnostic(nil), diags...)
		st.dictLoaded = true
		st.dictLoading = false
		if err != nil {
			st.dictErr = err.Error()
		} else {
			st.dictErr = ""
		}
		st.mu.Unlock()
		if a.isClosed() {
			return
		}
		svc.Invalidate()
		if a != nil {
			a.mu.Lock()
			repaint := a.OnRepaint
			a.mu.Unlock()
			if repaint != nil {
				repaint()
			}
		}
	}()
}

func (a *AppController) ensureUserBlocklistStoreAsync(svc ui.Services) {
	if a.isClosed() {
		return
	}
	st := a.inputEngineUI()
	st.mu.Lock()
	if st.blocklistStore != nil || st.blocklistInitAttempted || st.blocklistInitLoading {
		st.mu.Unlock()
		return
	}
	var sqlStore *internalstorage.SQLiteClipboardStore
	if a != nil {
		sqlStore, _ = a.ClipboardStore.(*internalstorage.SQLiteClipboardStore)
	}
	if sqlStore == nil || sqlStore.DB() == nil {
		st.blocklistInitAttempted = true
		st.blocklistUnavailable = true
		st.mu.Unlock()
		return
	}
	st.blocklistInitLoading = true
	db := sqlStore.DB()
	st.mu.Unlock()

	lifeCtx := a.lifeCtx()
	go func() {
		store, err := internalstorage.NewUserBlocklistStore(db)
		if lifeCtx.Err() != nil {
			st.mu.Lock()
			st.blocklistInitLoading = false
			st.mu.Unlock()
			return
		}
		st.mu.Lock()
		st.blocklistInitAttempted = true
		st.blocklistInitLoading = false
		if err != nil || store == nil {
			st.blocklistUnavailable = true
			if err != nil {
				st.blocklistErr = err.Error()
			}
		} else {
			st.blocklistStore = store
			st.blocklistUnavailable = false
			st.blocklistWords = append([]string(nil), store.Words()...)
		}
		st.mu.Unlock()
		if a.isClosed() {
			return
		}
		svc.Invalidate()
		if a != nil {
			a.mu.Lock()
			repaint := a.OnRepaint
			a.mu.Unlock()
			if repaint != nil {
				repaint()
			}
		}
	}()
}

func (a *AppController) blockCandidateWordDraft() {
	st := a.inputEngineUI()
	st.mu.Lock()
	word := strings.TrimSpace(st.blocklistDraft)
	store := st.blocklistStore
	st.blocklistErr = ""
	st.mu.Unlock()
	if word == "" || store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.lifeCtx(), time.Second)
	defer cancel()
	if err := store.Block(ctx, word); err != nil {
		st.mu.Lock()
		st.blocklistErr = err.Error()
		st.mu.Unlock()
		return
	}
	words := append([]string(nil), store.Words()...)
	st.mu.Lock()
	st.blocklistWords = words
	st.blocklistDraft = ""
	st.blocklistErr = ""
	st.mu.Unlock()
}

func (a *AppController) unblockCandidateWord(word string) {
	st := a.inputEngineUI()
	st.mu.Lock()
	store := st.blocklistStore
	st.mu.Unlock()
	if store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.lifeCtx(), time.Second)
	defer cancel()
	if err := store.Unblock(ctx, word); err != nil {
		st.mu.Lock()
		st.blocklistErr = err.Error()
		st.mu.Unlock()
		return
	}
	words := append([]string(nil), store.Words()...)
	st.mu.Lock()
	st.blocklistWords = words
	st.blocklistErr = ""
	st.mu.Unlock()
}

// Settings of Clavis's own input engine ([input_engine]). Only options the
// engine reads are offered; mode depends on platform parts (IMK), so it
// stays in the TOML file. The config watcher applies a change at once.

// inputSchemes are the pinyin schemes the engine accepts.
var inputSchemes = []string{"quanpin", "xiaohe", "ziranma", "microsoft", "sogou", "pinyin_plus"}

// fuzzyPinyinPresets are common fuzzy pairs, initials then finals; the
// engine applies each both ways.
var fuzzyPinyinPresets = [][2]string{
	{"z", "zh"}, {"c", "ch"}, {"s", "sh"}, {"n", "l"}, {"f", "h"}, {"r", "l"},
	{"an", "ang"}, {"en", "eng"}, {"in", "ing"}, {"ian", "iang"}, {"uan", "uang"},
}

func (a *AppController) renderInputEngineCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	ie := cfg.Main.InputEngine
	settingsSourceCard(c, theme, i18n.T("settings.card.input_engine"), "[input_engine]", func() {
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.input_engine.enabled"), "enabled",
			i18n.T("settings.input_engine.enabled_help"), ie.Enabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.Enabled = v })
		}
		// The rest only matters with the engine on; it stays readable.
		ui.Column(c).FillWidth().Gap(space6).Disabled(!ie.Enabled).Children(func() {
			schemes := make([]settingOption, len(inputSchemes))
			for i, s := range inputSchemes {
				schemes[i] = settingOption{s, i18n.T("settings.input_engine.scheme." + s)}
			}
			if v, changed := settingSelectRow(c, theme, i18n.T("settings.input_engine.scheme"), "scheme", "",
				schemes, ie.Scheme, 150); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.Scheme = v })
			}
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.input_engine.candidate_count"), "candidate_count", "",
				float64(ie.CandidateCount), 3, 9, 1, ""); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.CandidateCount = int(v) })
			}
			orientation := cfg.Main.Appearance.CandidateBarOrientation
			if orientation == "" {
				orientation = "horizontal"
			}
			if v, changed := settingChoiceRow(c, theme, i18n.T("settings.input_engine.orientation"),
				"appearance.candidate_bar_orientation", "", []settingOption{
					{"horizontal", i18n.T("settings.option.horizontal")},
					{"vertical", i18n.T("settings.option.vertical")},
				}, orientation); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CandidateBarOrientation = v })
			}
			if v, changed := settingSwitchRow(c, theme, i18n.T("settings.input_engine.shift_toggle"), "shift_toggle_ascii", "",
				ie.ShiftToggleASCII); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.ShiftToggleASCII = v })
			}
			if v, changed := settingSwitchRow(c, theme, i18n.T("settings.input_engine.user_learning"), "user_learning",
				i18n.T("settings.input_engine.user_learning_help"), ie.UserLearning); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.InputEngine.UserLearning = v })
			}
			a.renderFuzzyPinyin(c, theme, ie.FuzzyPinyin)
			a.renderUserWordsCard(c, theme, cfg)
		})
		settingsCardDivider(c, theme)
		a.renderDictDiagnosticsCard(c, theme, cfg)
		a.renderUserBlocklistCard(c, theme)
	})
}

// renderDictDiagnosticsCard shows an async-cached summary of loaded user/embedded
// dictionaries and any custom-dictionary diagnostics without blocking render frames.
func (a *AppController) renderDictDiagnosticsCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	zh := i18n.IsChinese()
	ie := cfg.Main.InputEngine
	a.triggerInputEngineDictRefresh(c.Services(), ie.CustomDictPaths, false)

	st := a.inputEngineUI()
	st.mu.Lock()
	loaded := st.dictLoaded
	loading := st.dictLoading
	status := st.dictStatus
	diags := append([]inputengine.DictionaryDiagnostic(nil), st.dictDiags...)
	errMsg := st.dictErr
	st.mu.Unlock()

	settingsSourceCard(c, theme,
		pickLang(zh, "词典状态与诊断", "Dictionary Status & Diagnostics"),
		"dicts/*.clvdict",
		func() {
			sourceLabel := pickLang(zh, "内置词典", "Embedded")
			sourceTone := chipNeutral
			switch {
			case loading && !loaded:
				sourceLabel = pickLang(zh, "检查中…", "Checking…")
				sourceTone = chipAccent
			case status.Source == "user":
				sourceLabel = pickLang(zh, "用户词典已加载", "User Dict Loaded")
				sourceTone = chipSuccess
			case len(status.Diagnostics) > 0 || len(diags) > 0 || errMsg != "":
				sourceLabel = pickLang(zh, "内置回退（有诊断）", "Embedded (Warnings)")
				sourceTone = chipWarning
			}

			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				kitStatusPill(c, theme, sourceLabel, sourceTone)
				ui.Spacer(c)
				refreshLbl := pickLang(zh, "刷新诊断", "Refresh")
				if loading {
					refreshLbl = pickLang(zh, "刷新中…", "Refreshing…")
				}
				if ui.Button(c, refreshLbl).Key("input-engine-dict-refresh").Disabled(loading).Padding(space2, space8).Shrink(0).Clicked() {
					a.triggerInputEngineDictRefresh(c.Services(), ie.CustomDictPaths, true)
				}
			})

			if loaded {
				settingsKeyValueRow(c, theme, pickLang(zh, "已加载词条数", "Loaded Entries"), fmt.Sprintf("%d", status.Count))
				settingsKeyValueRow(c, theme, pickLang(zh, "自定义词典路径", "Custom Dict Paths"), fmt.Sprintf("%d", len(ie.CustomDictPaths)))
				if status.Source == "user" && strings.TrimSpace(status.Path) != "" {
					settingsKeyValueRow(c, theme, pickLang(zh, "用户词典文件", "User Dict Files"), status.Path)
				}
			} else {
				settingsHint(c, theme, pickLang(zh, "正在后台检查词典状态，不阻塞界面渲染。", "Checking dictionary status asynchronously without blocking render."))
			}

			if errMsg != "" {
				kitWarningBanner(c, theme, errMsg)
			}

			const maxShownDiags = 5
			shown := 0
			total := len(status.Diagnostics) + len(diags)
			for _, msg := range status.Diagnostics {
				if shown >= maxShownDiags {
					break
				}
				kitWarningBanner(c, theme, msg)
				shown++
			}
			for _, d := range diags {
				if shown >= maxShownDiags {
					break
				}
				name := filepath.Base(strings.TrimSpace(d.Path))
				if name == "" || name == "." {
					name = d.Path
				}
				line := fmt.Sprintf("[%s] %s: %s", d.Kind, name, d.Message)
				if d.Fallback != "" {
					line += " (" + d.Fallback + ")"
				}
				kitWarningBanner(c, theme, line)
				shown++
			}
			if total > maxShownDiags {
				settingsHint(c, theme, pickLang(zh,
					fmt.Sprintf("另有 %d 条词典诊断未展开。", total-maxShownDiags),
					fmt.Sprintf("%d more dictionary diagnostic(s) omitted.", total-maxShownDiags)))
			}
		})
}

// renderUserBlocklistCard draws the candidate word blocklist backed by
// storage.UserBlocklistStore (or an injected test store) without performing
// SQLite I/O during render frames.
func (a *AppController) renderUserBlocklistCard(c *ui.Context, theme *ui.Theme) {
	zh := i18n.IsChinese()
	a.ensureUserBlocklistStoreAsync(c.Services())

	st := a.inputEngineUI()
	st.mu.Lock()
	unavailable := st.blocklistUnavailable
	loading := st.blocklistInitLoading
	words := append([]string(nil), st.blocklistWords...)
	draft := st.blocklistDraft
	errMsg := st.blocklistErr
	st.mu.Unlock()

	settingsSourceCard(c, theme,
		pickLang(zh, "候选用词屏蔽列表", "Candidate Blocklist"),
		fmt.Sprintf("%d", len(words)),
		func() {
			settingsHint(c, theme, pickLang(zh,
				"精确隐藏不希望出现在候选栏中的词条。",
				"Hide exact words from appearing in the input candidate list."))
			switch {
			case loading:
				settingsHint(c, theme, pickLang(zh, "正在加载屏蔽列表…", "Loading blocklist…"))
				return
			case unavailable:
				settingsHint(c, theme, pickLang(zh, "屏蔽词库需要本地 SQLite 存储。", "Candidate blocklist requires local SQLite storage."))
				return
			}

			if len(words) == 0 {
				settingsHint(c, theme, pickLang(zh, "尚未屏蔽任何候选词。", "No blocked candidate words yet."))
			} else {
				ui.Column(c).FillWidth().Gap(space4).Children(func() {
					for _, w := range words {
						word := w
						ui.Row(c).Key("blocklist-row:"+word).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, word).FontSize(typeBody).TextColor(theme.Text).Grow(1).Shrink(1).SingleLine()
							if ui.Button(c, pickLang(zh, "移除", "Unblock")).
								Key("unblock-word:" + word).
								Padding(space2, space10).
								Shrink(0).
								Clicked() {
								a.unblockCandidateWord(word)
							}
						})
					}
				})
			}

			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				in := ui.TextInput(c.Key("blocklist-word-input"), &draft).
					Label(pickLang(zh, "屏蔽词", "Blocklist Word")).
					Placeholder(pickLang(zh, "输入要屏蔽的候选词", "Word to hide from candidates")).
					FillWidth().
					Grow(1)
				if in.Changed() {
					st.mu.Lock()
					st.blocklistDraft = draft
					st.mu.Unlock()
				}
				if ui.Button(c, pickLang(zh, "屏蔽", "Block")).
					Key("add-blocklist-word").
					Disabled(strings.TrimSpace(draft) == "").
					Padding(space2, space10).
					Shrink(0).
					Clicked() {
					a.blockCandidateWordDraft()
				}
			})
			if errMsg != "" {
				kitErrorBanner(c, theme, errMsg)
			}
		})
}

// renderFuzzyPinyin draws the fuzzy pairs as toggle chips. Rules the
// presets don't cover (written by hand in the TOML) are kept as they are.
func (a *AppController) renderFuzzyPinyin(c *ui.Context, theme *ui.Theme, rules []string) {
	caption := "fuzzy_pinyin"
	if len(rules) > 0 {
		caption = settingCaption("fuzzy_pinyin", strings.Join(rules, ", "))
	}
	settingStackedRow(c, theme, i18n.T("settings.input_engine.fuzzy"), caption, i18n.T("settings.input_engine.fuzzy_help"), func() ui.Element {
		row := ui.Row(c).FillWidth().Wrap().Gap(space4)
		row.Children(func() {
			for _, pair := range fuzzyPinyinPresets {
				p := pair
				on := slices.ContainsFunc(rules, func(r string) bool { return fuzzyRuleIs(r, p) })
				pill := ui.ButtonBase(c).Key("fuzzy:"+p[0]+"="+p[1]).Padding(space2, space8).
					Radius(radiusChip).FocusRing(false).Role(ui.RoleCheckBox).Checked(on)
				if on {
					pill.Background(selectionFill(theme)).Border(1, selectionEdge(theme))
				} else {
					pill.Background(theme.Background).Border(1, hairline(theme))
				}
				pill.Children(func() {
					fg := theme.TextMuted
					if on {
						fg = accentInk(theme, theme.Surface)
					}
					ui.Text(c, p[0]+" ⇄ "+p[1]).FontSize(typeMeta).FontWeight(600).TextColor(fg).SingleLine()
				})
				if pill.Clicked() {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) {
						m.InputEngine.FuzzyPinyin = toggleFuzzyRule(m.InputEngine.FuzzyPinyin, p)
					})
				}
			}
		})
		return row
	})
}

// fuzzyRuleIs reports whether rule is pair, written either way round and
// with "=", "_" or "-" between, as the engine reads it.
func fuzzyRuleIs(rule string, pair [2]string) bool {
	r := strings.ToLower(strings.TrimSpace(rule))
	a, b, ok := strings.Cut(r, "=")
	if !ok {
		if a, b, ok = strings.Cut(r, "_"); !ok {
			a, b, ok = strings.Cut(r, "-")
		}
	}
	if !ok {
		return false
	}
	return a == pair[0] && b == pair[1] || a == pair[1] && b == pair[0]
}

// toggleFuzzyRule removes pair from rules when present (in any spelling),
// else appends it as "a=b". Other rules keep their order.
func toggleFuzzyRule(rules []string, pair [2]string) []string {
	out := make([]string, 0, len(rules)+1)
	removed := false
	for _, r := range rules {
		if fuzzyRuleIs(r, pair) {
			removed = true
			continue
		}
		out = append(out, r)
	}
	if !removed {
		out = append(out, pair[0]+"="+pair[1])
	}
	return out
}
