package uiapp

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// Frame benchmarks for the transient surfaces: one forced rebuild of the
// Capsule view per iteration. They keep the cost of visual polish (keycaps,
// tiles, contrast-derived colors) visible next to the 16ms frame budget:
//
//	go test -run '^$' -bench BenchmarkFrame ./uiapp/
func benchController(b testing.TB) *AppController {
	b.Helper()
	cfgStore := config.NewConfigStore(b.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		b.Fatal(err)
	}
	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		Bridge:         newMockBridge(),
	})
	b.Cleanup(ctrl.Close)
	return ctrl
}

func benchFrames(b *testing.B, ctrl *AppController) {
	w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}

func BenchmarkFrameHUD(b *testing.B) {
	ctrl := benchController(b)
	ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: "Copied to clipboard"})
	benchFrames(b, ctrl)
}

func BenchmarkFrameOmnibox(b *testing.B) {
	ctrl := benchController(b)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	benchFrames(b, ctrl)
}

// BenchmarkFrameOmniboxSpatial measures the spatial preset against the
// 16ms/frame budget; a sustained result above 16ms is a visual frame regression.
func BenchmarkFrameOmniboxSpatial(b *testing.B) {
	ctrl := benchController(b)
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Appearance.VisualPreset = "spatial_3d"
		m.Appearance.Animations = "full"
	}); err != nil {
		b.Fatal(err)
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	benchFrames(b, ctrl)
}

// BenchmarkFrameOmniboxClassic measures the flat classic baseline against the
// 16ms/frame budget; compare it with BenchmarkFrameOmniboxSpatial for regressions.
func BenchmarkFrameOmniboxClassic(b *testing.B) {
	ctrl := benchController(b)
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Appearance.VisualPreset = "classic"
		m.Appearance.Animations = "reduced"
	}); err != nil {
		b.Fatal(err)
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	benchFrames(b, ctrl)
}

func BenchmarkFrameLeader(b *testing.B) {
	ctrl := benchController(b)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader})
	benchFrames(b, ctrl)
}

// frameCost is the per-frame cost of tt: the best of several batches after a warm-up, so a
// stray scheduler hiccup cannot make a frame look slow.
func frameCost(tt *ui.Tester) time.Duration {
	const warm, batches, perBatch = 10, 7, 30
	for range warm {
		tt.Frame()
	}
	costs := make([]time.Duration, 0, batches)
	for range batches {
		start := time.Now()
		for range perBatch {
			tt.Frame()
		}
		costs = append(costs, time.Since(start)/perBatch)
	}
	sort.Slice(costs, func(i, j int) bool { return costs[i] < costs[j] })
	return costs[0]
}

// mainFrameCost is one frame of the main interface: the Capsule with the Omnibox open.
func mainFrameCost(tb testing.TB) time.Duration {
	ctrl := benchController(tb)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
	return frameCost(ui.NewTester(ctrl.CapsuleView, w, h))
}

func candidateFrameTester(v inputengine.View, cfg CandidatePanelConfig) *ui.Tester {
	w, h := PreferredCandidatePanelSizeFor(v, cfg)
	return ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, cfg) }, w, h)
}

// The candidate window repaints on every keystroke, so a style may not make it heavier than
// the main interface: one frame, for every style, in both orientations, on the longest
// input, stays within 1.2x one Omnibox frame. (Nothing heavier — a web view, a 3D scene —
// may ever go into this window.)
func TestCandidateFrameWithinMainFrameBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("frame timing")
	}
	main := mainFrameCost(t)
	budget := main * 12 / 10
	v := typedViewWith("zhonghuarenmingongheguo", inputengine.MaxCandidateCount)
	for name, style := range candidateStyles() {
		for _, orientation := range []string{"horizontal", "vertical"} {
			app := config.DefaultAppearanceConfig()
			app.CandidateBarOrientation = orientation
			cost := frameCost(candidateFrameTester(v, CandidatePanelConfig{Appearance: app, Style: style}))
			t.Logf("%-15s %-10s %8v / main %v (%.2fx)", name, orientation, cost, main, float64(cost)/float64(main))
			if cost > budget {
				t.Errorf("%s/%s: candidate frame %v > 1.2 x main frame %v", name, orientation, cost, main)
			}
		}
	}
}

// BenchmarkFrameCandidate is one candidate-window frame per style, next to
// BenchmarkFrameOmnibox:
//
//	go test -run '^$' -bench 'BenchmarkFrame(Candidate|Omnibox)$' ./uiapp/
func BenchmarkFrameCandidate(b *testing.B) {
	v := typedViewWith("zhonghuarenmingongheguo", inputengine.MaxCandidateCount)
	for _, name := range []string{"native", "glass", "flat", "custom"} {
		style := candidateStyles()[name]
		b.Run(fmt.Sprint(name), func(b *testing.B) {
			tt := candidateFrameTester(v, CandidatePanelConfig{Appearance: config.DefaultAppearanceConfig(), Style: style})
			tt.Frame()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tt.Frame()
			}
		})
	}
}
