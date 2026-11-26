package uiapp

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	internalstorage "github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestFuzzyRuleSpellingsAndToggle(t *testing.T) {
	zzh := [2]string{"z", "zh"}
	for _, r := range []string{"z=zh", "zh=z", "z_zh", " Z-ZH "} {
		if !fuzzyRuleIs(r, zzh) {
			t.Errorf("fuzzyRuleIs(%q, z/zh) = false", r)
		}
	}
	for _, r := range []string{"z=z", "zh", "an=ang", "s=sh"} {
		if fuzzyRuleIs(r, zzh) {
			t.Errorf("fuzzyRuleIs(%q, z/zh) = true", r)
		}
	}

	// Toggling off drops every spelling of the pair and keeps the rest.
	got := toggleFuzzyRule([]string{"zh=z", "k=g", "z_zh"}, zzh)
	if !slices.Equal(got, []string{"k=g"}) {
		t.Fatalf("toggle off = %q", got)
	}
	// Toggling on appends the canonical spelling.
	got = toggleFuzzyRule(got, [2]string{"an", "ang"})
	if !slices.Equal(got, []string{"k=g", "an=ang"}) {
		t.Fatalf("toggle on = %q", got)
	}
}

// TestInputEngineCardPersistsAndGates: the engine's options are disabled
// while it is off; once on, a fuzzy chip writes fuzzy_pinyin to
// config.toml and a second click removes it again.
func TestInputEngineCardPersistsAndGates(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 1600)
	ctrl.setSettingsSection(settingsSectionIMEKeymap)
	tt.Frame()
	if ctrl.Config().Main.InputEngine.Enabled {
		t.Skip("the default config enables the input engine; the gate test needs it off")
	}

	chip := "z ⇄ zh"
	r, ok := tt.Find(chip)
	if !ok {
		t.Fatalf("no fuzzy chip %q: %v", chip, tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if rules := ctrl.Config().Main.InputEngine.FuzzyPinyin; len(rules) != 0 {
		t.Fatalf("a disabled chip changed fuzzy_pinyin to %q", rules)
	}

	if err := tt.Click("Use Clavis Input Engine"); err != nil {
		t.Fatalf("click engine switch: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !ctrl.Config().Main.InputEngine.Enabled {
		t.Fatal("the engine switch did not enable input_engine")
	}

	if err := tt.Click(chip); err != nil {
		t.Fatalf("click %s: %v", chip, err)
	}
	tt.Frame()
	if rules := ctrl.Config().Main.InputEngine.FuzzyPinyin; !slices.Equal(rules, []string{"z=zh"}) {
		t.Fatalf("fuzzy_pinyin = %q, want [z=zh]", rules)
	}
	if body := readConfigFile(t, ctrl, config.FileMain); !strings.Contains(body, `"z=zh"`) {
		t.Fatalf("config.toml does not persist fuzzy_pinyin:\n%s", body)
	}
	if !tt.HasText("fuzzy_pinyin: z=zh") {
		t.Fatalf("caption does not show the rule: %v", tt.Texts())
	}

	if err := tt.Click(chip); err != nil {
		t.Fatalf("click %s again: %v", chip, err)
	}
	tt.Frame()
	if rules := ctrl.Config().Main.InputEngine.FuzzyPinyin; len(rules) != 0 {
		t.Fatalf("second click left fuzzy_pinyin = %q", rules)
	}
}

// TestCandidateAppearanceFollowsSettings: the candidate window reads its
// layout from a package global, which a settings change must update even
// while the Capsule (which also publishes it) never renders.
func TestCandidateAppearanceFollowsSettings(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CandidateBarOrientation = "vertical" }); err != nil {
		t.Fatalf("UpdateMainConfig: %v", err)
	}
	if got := CandidateAppearance().CandidateBarOrientation; got != "vertical" {
		t.Fatalf("candidate orientation = %q after a settings change, want vertical", got)
	}
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CandidateBarOrientation = "horizontal" }); err != nil {
		t.Fatalf("UpdateMainConfig: %v", err)
	}
	if got := CandidateAppearance().CandidateBarOrientation; got != "horizontal" {
		t.Fatalf("candidate orientation = %q, want horizontal", got)
	}
}

type fakeBlocklistStore struct {
	mu    sync.Mutex
	words map[string]struct{}
}

func newFakeBlocklistStore(initial ...string) *fakeBlocklistStore {
	s := &fakeBlocklistStore{words: make(map[string]struct{}, len(initial))}
	for _, w := range initial {
		s.words[w] = struct{}{}
	}
	return s
}

func (f *fakeBlocklistStore) Words() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.words))
	for w := range f.words {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

func (f *fakeBlocklistStore) Block(_ context.Context, word string) error {
	f.mu.Lock()
	f.words[strings.TrimSpace(word)] = struct{}{}
	f.mu.Unlock()
	return nil
}

func (f *fakeBlocklistStore) Unblock(_ context.Context, word string) error {
	f.mu.Lock()
	delete(f.words, strings.TrimSpace(word))
	f.mu.Unlock()
	return nil
}

func TestInputEngineDictDiagnosticsCardAsyncAndRefresh(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	gate := make(chan struct{})
	var calls atomic.Int32
	ctrl.setInputEngineDictLoaderForTest(func(_ []string) (inputengine.DictionaryStatus, []inputengine.DictionaryDiagnostic, error) {
		n := calls.Add(1)
		if n == 1 {
			<-gate
		}
		return inputengine.DictionaryStatus{
			Source: "user",
			Path:   "custom.clvdict",
			Count:  4200 + int(n),
		}, []inputengine.DictionaryDiagnostic{{
			Path:     "/tmp/bad.dict.yaml",
			Kind:     "malformed",
			Message:  "missing tab separator",
			Fallback: "starter+builtin",
		}}, nil
	})

	tt := ui.NewTester(func(c *ui.Context) {
		ctrl.renderDictDiagnosticsCard(c, ui.LightTheme(), ctrl.Config())
	}, 520, 360)

	// Frame 1 must return immediately without blocking on the gated loader.
	start := time.Now()
	tt.Frame()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("renderDictDiagnosticsCard blocked render thread for %v", elapsed)
	}
	if !tt.HasText("Checking…") {
		t.Fatalf("expected Checking… pill while loading asynchronously, got %v", tt.Texts())
	}

	// Unblock loader and wait for cached status + diagnostic banner.
	close(gate)
	waitUntil(t, time.Second, func() bool {
		st := ctrl.inputEngineUI()
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.dictLoaded && !st.dictLoading
	}, "dict diagnostics async load finished")

	// Rapid subsequent renders use cached snapshot (no extra loader invocations).
	for range 5 {
		tt.Frame()
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader called %d times across cached frames, want 1", got)
	}
	if !tt.HasText("4201") || !tt.HasText("bad.dict.yaml") || !tt.HasText("User Dict Loaded") {
		t.Fatalf("dict diagnostics card missing loaded stats/diagnostic: %v", tt.Texts())
	}

	// Clicking Refresh triggers a fresh async reload.
	if err := tt.Click("Refresh"); err != nil {
		t.Fatalf("click Refresh: %v", err)
	}
	waitUntil(t, time.Second, func() bool {
		return calls.Load() >= 2
	}, "dict diagnostics refresh triggered")
	waitUntil(t, time.Second, func() bool {
		st := ctrl.inputEngineUI()
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.dictLoading && st.dictStatus.Count == 4202
	}, "dict diagnostics refresh completed")
	tt.Frame()
	if !tt.HasText("4202") {
		t.Fatalf("expected refreshed count 4202, got %v", tt.Texts())
	}
}

func TestInputEngineUserBlocklistViewerAndEditor(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	t.Run("fake store add and unblock via UI", func(t *testing.T) {
		ctrl, _, _, _ := newTestController(t, nil)
		store := newFakeBlocklistStore("噪声词")
		ctrl.setUserBlocklistStore(store)

		tt := ui.NewTester(func(c *ui.Context) {
			ctrl.renderUserBlocklistCard(c, ui.LightTheme())
		}, 520, 320)
		tt.Frame()
		if !tt.HasText("噪声词") {
			t.Fatalf("existing blocked word missing: %v", tt.Texts())
		}

		// Type and block a new word.
		if err := tt.Click("Blocklist Word"); err != nil {
			t.Fatalf("click Blocklist Word input: %v", err)
		}
		tt.Type("错别字")
		tt.Frame()
		if err := tt.Click("Block"); err != nil {
			t.Fatalf("click Block button: %v", err)
		}
		tt.Frame()
		if got := store.Words(); !slices.Equal(got, []string{"噪声词", "错别字"}) {
			t.Fatalf("blocklist store words = %v, want [噪声词 错别字]", got)
		}
		if !tt.HasText("错别字") {
			t.Fatalf("newly blocked word not rendered: %v", tt.Texts())
		}

		// Unblock the first listed word.
		if err := tt.Click("Unblock"); err != nil {
			t.Fatalf("click Unblock: %v", err)
		}
		tt.Frame()
		if got := store.Words(); len(got) != 1 {
			t.Fatalf("blocklist store words after unblock = %v, want len 1", got)
		}
	})

	t.Run("real SQLite UserBlocklistStore auto-wired from ClipboardStore", func(t *testing.T) {
		sqlStore, err := internalstorage.NewSQLiteClipboardStore(context.Background(), ":memory:", clipboard.NewPrivacyFilter())
		if err != nil {
			t.Fatalf("open memory sqlite store: %v", err)
		}
		defer sqlStore.Close()
		bl, err := internalstorage.NewUserBlocklistStore(sqlStore.DB())
		if err != nil {
			t.Fatalf("NewUserBlocklistStore: %v", err)
		}
		if err := bl.Block(context.Background(), "屏蔽样本"); err != nil {
			t.Fatalf("seed blocklist: %v", err)
		}

		ctrl := NewAppController(Options{
			ConfigStore:    config.NewConfigStore(t.TempDir()),
			ClipboardStore: sqlStore,
		})
		t.Cleanup(ctrl.Close)
		tt := ui.NewTester(func(c *ui.Context) {
			ctrl.renderUserBlocklistCard(c, ui.LightTheme())
		}, 520, 320)
		tt.Frame()
		waitUntil(t, time.Second, func() bool {
			st := ctrl.inputEngineUI()
			st.mu.Lock()
			defer st.mu.Unlock()
			return st.blocklistStore != nil && !st.blocklistInitLoading
		}, "SQLite UserBlocklistStore async init")
		tt.Frame()
		if !tt.HasText("屏蔽样本") {
			t.Fatalf("SQLite-backed blocklist word not shown: %v", tt.Texts())
		}
	})
}
