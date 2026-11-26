package uiapp

import (
	"context"
	"image"
	"image/color"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// These two tests cover the guarantees the Settings rework promised but did not
// initially test: that the rail still selects every section, and that all eight
// sections render at the window's minimum size in both themes.

// settingsSectionIndex returns the section the controller currently shows.
func settingsSectionIndex(ctrl *AppController) int {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	return normalizeSettingsSection(ctrl.uiState.settingsTabIdx)
}

// TestSettingsRailSelectsEverySection clicks each rail entry in turn and checks
// that the controller follows and the pane header names the new section. The
// rail was rewritten from the stock ui.Sidebar onto kit primitives, so this is
// the compatibility contract for every caller that drives sections by key.
func TestSettingsRailSelectsEverySection(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()

	sections := settingsSections()
	if len(sections) != 9 {
		t.Fatalf("settings exposes %d sections, want the documented 9", len(sections))
	}
	// Guard the assertion below: if HasText matched anything, every check would
	// pass vacuously.
	if tt.HasText("§ no such settings section §") {
		t.Fatal("HasText matched nonsense, so the header assertions would be vacuous")
	}
	// Start somewhere that is not the first entry so every click is a change.
	ctrl.setSettingsSection(sections[len(sections)-1].Index)
	tt.Frame()

	for _, meta := range sections {
		openSettingsSection(t, tt, meta.Label)

		if got := settingsSectionIndex(ctrl); got != meta.Index {
			t.Fatalf("clicking %q selected section %d, want %d", meta.Label, got, meta.Index)
		}
		// The header renders "<label> — <hint>"; assert the pane really swapped
		// rather than just the stored index changing.
		if !tt.HasText(meta.Hint) {
			t.Fatalf("pane header for %q does not show its hint %q", meta.Label, meta.Hint)
		}
	}
}

// TestSettingsSectionsRenderAtMinimumSize renders every section at the window's
// minimum size (760x500) in both themes. A card-based layout that overflows or
// collapses shows up as a uniform or empty image, so this catches the two
// failure modes the rework could plausibly introduce without asserting pixels.
func TestSettingsSectionsRenderAtMinimumSize(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)

	for _, dark := range []bool{false, true} {
		mode := "light"
		if dark {
			mode = "dark"
		}
		t.Run(mode, func(t *testing.T) {
			for _, meta := range settingsSections() {
				ctrl.setSettingsSection(meta.Index)
				tt := ui.NewTester(ctrl.SettingsView, 760, 500)
				tt.SetDark(dark)
				tt.Frame()
				tt.Frame()

				img := tt.Image()
				if img == nil || img.Bounds().Empty() {
					t.Fatalf("%s: section %q rendered no image", mode, meta.Label)
				}
				// A real pane draws a rail, cards, dividers and text; a collapsed
				// or clipped-to-nothing pane draws one flat colour.
				if shades := distinctShades(img); shades < 8 {
					t.Fatalf("%s: section %q rendered only %d distinct colours — pane looks empty",
						mode, meta.Label, shades)
				}
				if !tt.HasText(meta.Hint) {
					t.Fatalf("%s: section %q lost its pane header at minimum size", mode, meta.Label)
				}
			}
		})
	}
}

// distinctShades counts the distinct RGB values in an image, sampled on a grid
// so a large surface stays cheap to inspect.
func distinctShades(img *image.RGBA) int {
	seen := map[color.RGBA]struct{}{}
	b := img.Bounds()
	for y := 0; y < b.Max.Y; y += 4 {
		for x := 0; x < b.Max.X; x += 4 {
			seen[img.RGBAAt(x, y)] = struct{}{}
		}
	}
	return len(seen)
}

func TestSettingsSidebarSearchPerSectionAccuracy(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)
	ctrl, _, _, _ := newTestController(t, nil)
	cfg := ctrl.Config()
	sections := settingsNavigationSections()

	matchingKeys := func(q string) []string {
		var out []string
		for _, meta := range sections {
			if settingsSectionMatches(meta, q, cfg) {
				out = append(out, meta.Key)
			}
		}
		return out
	}

	// Previously fmt.Sprintf("%+v", cfg) made every query match all 13 sections.
	if got := matchingKeys("maxhistoryitems"); len(got) != 1 || got[0] != "clipboard" {
		t.Fatalf("query 'maxhistoryitems' matched %v, want only [clipboard]", got)
	}
	if got := matchingKeys("screenshot_shortcut"); len(got) != 1 || got[0] != "triggers" {
		t.Fatalf("query 'screenshot_shortcut' matched %v, want only [triggers]", got)
	}
	if got := matchingKeys("截图快捷键"); len(got) != 1 || got[0] != "triggers" {
		t.Fatalf("query '截图快捷键' matched %v, want only [triggers]", got)
	}
	if got := matchingKeys("quickjump"); len(got) != 1 || got[0] != "configs" {
		t.Fatalf("query 'quickjump' matched %v, want only [configs]", got)
	}
	if got := matchingKeys("vault"); len(got) != 1 || got[0] != "security" {
		t.Fatalf("query 'vault' matched %v, want only [security]", got)
	}
	if got := matchingKeys("同步"); len(got) != 1 || got[0] != "about" {
		t.Fatalf("query '同步' matched %v, want only [about]", got)
	}
	if got := matchingKeys("promptrepo"); len(got) != 1 || got[0] != "templates" {
		t.Fatalf("query 'promptrepo' matched %v, want only [templates]", got)
	}
	if got := matchingKeys("模板库"); len(got) != 1 || got[0] != "templates" {
		t.Fatalf("query '模板库' matched %v, want only [templates]", got)
	}
}

func TestSettingsScreenshotShortcutQuickJumpAndAllSectionsRender(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()

	// General & Triggers renders the screenshot_shortcut field and persists edits.
	if !tt.HasText("Screenshot Shortcut") || !tt.HasText("screenshot_shortcut") {
		t.Fatalf("expected Screenshot Shortcut editor on General & Triggers, got %v", tt.Texts())
	}
	if err := tt.Click("Screenshot Shortcut"); err != nil {
		t.Fatalf("click Screenshot Shortcut label: %v", err)
	}
	tt.Frame()
	tt.Type("option+shift+s")
	tt.Frame()
	if got := ctrl.Config().Main.Trigger.ScreenshotShortcut; got != "option+shift+s" {
		t.Fatalf("Trigger.ScreenshotShortcut = %q, want option+shift+s", got)
	}
	if body := readConfigFile(t, ctrl, config.FileMain); !strings.Contains(body, `screenshot_shortcut = "option+shift+s"`) {
		t.Fatalf("config.toml missing screenshot_shortcut:\n%s", body)
	}

	// QuickJump config file has bilingual non-empty descriptions.
	i18n.SetLanguage(i18n.English)
	if desc := configFileDescription(config.FileQuickJump); strings.TrimSpace(desc) == "" {
		t.Fatal("expected non-empty EN description for FileQuickJump")
	}
	i18n.SetLanguage(i18n.Chinese)
	if desc := configFileDescription(config.FileQuickJump); strings.TrimSpace(desc) == "" {
		t.Fatal("expected non-empty ZH description for FileQuickJump")
	}
	i18n.SetLanguage(i18n.English)

	// All 13 navigation sections (including Security & About) render cleanly at MinHeight (500) and default (640).
	for _, size := range [][2]int{{760, 500}, {960, 640}} {
		for _, meta := range settingsNavigationSections() {
			ctrl.setSettingsSection(meta.Index)
			st := ui.NewTester(ctrl.SettingsView, size[0], size[1])
			st.Frame()
			st.Frame()
			if !st.HasText(meta.Hint) {
				t.Fatalf("section %q lost pane header at %dx%d", meta.Key, size[0], size[1])
			}
		}
	}
}

func TestSettingsAIProfilesShortcutHintAndSingleImpactCard(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsSection(settingsSectionAI)
	tt := ui.NewTester(ctrl.SettingsView, 980, 1400)
	tt.Frame()

	// Shortcut hint must match ai_center_view.go (⌘⇧M / Ctrl+⇧M), never ⇧P.
	joined := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(joined, "⇧M") {
		t.Fatalf("AI profile switcher missing ⇧M cycle hint: %v", tt.Texts())
	}
	if strings.Contains(joined, "⇧P") {
		t.Fatalf("AI profile switcher still contains stale ⇧P hint: %v", tt.Texts())
	}

	// Opening switch impact preview must render Confirm Switch ONCE (no duplicate card/key).
	ctrl.setAIProfilePreviewTarget("ollama")
	tt.Frame()
	confirmCount := 0
	for _, txt := range tt.Texts() {
		if txt == "Confirm Switch" {
			confirmCount++
		}
	}
	if confirmCount != 1 {
		t.Fatalf("Confirm Switch rendered %d times, want 1; texts=%v", confirmCount, tt.Texts())
	}
}

type countingSlowIMEBridge struct {
	*mockBridge
	gate        chan struct{}
	curCalls    atomic.Int32
	sourceCalls atomic.Int32
}

func (b *countingSlowIMEBridge) CurrentInputSource(ctx context.Context) (host.InputSourceInfo, error) {
	if b.curCalls.Add(1) == 1 && b.gate != nil {
		select {
		case <-b.gate:
		case <-ctx.Done():
			return host.InputSourceInfo{}, ctx.Err()
		}
	}
	return b.mockBridge.CurrentInputSource(ctx)
}

func (b *countingSlowIMEBridge) InputSources(ctx context.Context) ([]host.InputSourceInfo, error) {
	b.sourceCalls.Add(1)
	return b.mockBridge.InputSources(ctx)
}

func TestSettingsIMEConfigCachesSourcesAsyncAndRefreshes(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	gate := make(chan struct{})
	br := &countingSlowIMEBridge{mockBridge: newMockBridge(), gate: gate}
	ctrl := NewAppController(Options{
		ConfigStore: config.NewConfigStore(t.TempDir()),
		Bridge:      br,
	})
	t.Cleanup(ctrl.Close)
	tt := ui.NewTester(func(c *ui.Context) {
		ctrl.renderIMEConfig(c, ui.LightTheme(), ctrl.Config())
	}, 520, 480)

	// Frame 1 must not block even while OS TIS IPC is blocked on gate.
	start := time.Now()
	tt.Frame()
	if elapsed := time.Since(start); elapsed > 80*time.Millisecond {
		t.Fatalf("renderIMEConfig blocked render frame for %v", elapsed)
	}
	close(gate)

	waitUntil(t, time.Second, func() bool {
		se := ctrl.settingsEditor()
		se.mu.Lock()
		defer se.mu.Unlock()
		return !se.imeInFlight && !se.imeFetchedAt.IsZero()
	}, "IME config async refresh completed")

	// Rapid subsequent frames hit the 2s cache without calling OS IPC again.
	for range 6 {
		tt.Frame()
	}
	if got := br.curCalls.Load(); got != 1 {
		t.Fatalf("CurrentInputSource called %d times within TTL, want 1", got)
	}
	if got := br.sourceCalls.Load(); got != 1 {
		t.Fatalf("InputSources called %d times within TTL, want 1", got)
	}
	if !tt.HasText("ABC · EN") {
		t.Fatalf("cached current input source not rendered: %v", tt.Texts())
	}

	// Clicking Refresh Input Sources explicitly forces an async refresh.
	if err := tt.Click("Refresh Input Sources"); err != nil {
		t.Fatalf("click Refresh Input Sources: %v", err)
	}
	waitUntil(t, time.Second, func() bool {
		return br.curCalls.Load() >= 2 && br.sourceCalls.Load() >= 2
	}, "explicit Refresh Input Sources triggered async fetch")
}
