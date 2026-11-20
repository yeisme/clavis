package uiapp

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func sampleCandidateView() inputengine.View {
	return inputengine.View{
		Composing: true,
		Preedit:   "ni'hao'shi'jie",
		Selected:  0,
		Page:      1,
		HasPrev:   true,
		HasNext:   true,
		Candidates: []inputengine.Candidate{
			{Text: "你好世界"},
			{Text: "你好时间", AI: true},
			{Text: "你号"},
			{Text: "拟好", AI: true},
			{Text: "泥嚎"},
		},
	}
}

func TestCandidatePanelBothOrientationsAndExactSizing(t *testing.T) {
	defer SetCandidateAppearance(config.DefaultAppearanceConfig())

	v := sampleCandidateView()

	horizCfg := config.DefaultAppearanceConfig()
	horizCfg.CandidateBarOrientation = "horizontal"
	hw, hh := PreferredCandidatePanelSizeFor(v, panelOf(horizCfg))
	if hh != candidatePanelHeight {
		t.Fatalf("horizontal height = %d, want %d", hh, candidatePanelHeight)
	}
	if hw <= hh || hw < candidatePanelMinWidth || hw > candidatePanelMaxWidth {
		t.Fatalf("horizontal size = %dx%d, want wide strip in [%d..%d]", hw, hh, candidatePanelMinWidth, candidatePanelMaxWidth)
	}

	vertCfg := config.DefaultAppearanceConfig()
	vertCfg.CandidateBarOrientation = "vertical"
	vw, vh := PreferredCandidatePanelSizeFor(v, panelOf(vertCfg))
	if vh <= hh || vw >= hw {
		t.Fatalf("vertical size = %dx%d vs horizontal %dx%d: expected taller, narrower list", vw, vh, hw, hh)
	}
	wantVertH := candVertPadY*2 + candVertPreeditHeight + candVertGap + candHairline + len(v.Candidates)*(candVertGap+candVertRowHeight) + candVertGap + candVertNavHeight
	if vh != wantVertH {
		t.Fatalf("vertical height = %d, want exact %d", vh, wantVertH)
	}

	// Verify SetCandidateAppearance drives the zero-config entry points used by input_engine_ui.go.
	SetCandidateAppearance(vertCfg)
	if gotW, gotH := PreferredCandidatePanelSize(v); gotW != vw || gotH != vh {
		t.Fatalf("PreferredCandidatePanelSize(vertical) = %dx%d, want %dx%d", gotW, gotH, vw, vh)
	}
	SetCandidateAppearance(horizCfg)
	if gotW, gotH := PreferredCandidatePanelSize(v); gotW != hw || gotH != hh {
		t.Fatalf("PreferredCandidatePanelSize(horizontal) = %dx%d, want %dx%d", gotW, gotH, hw, hh)
	}

	// Render horizontal panel at PreferredCandidatePanelSizeFor and verify geometry.
	th := ui.NewTester(func(c *ui.Context) {
		InputCandidateViewFor(c, v, panelOf(horizCfg))
	}, hw, hh)
	th.Frame()
	if !th.HasText(v.Preedit) || !th.HasText("✦") || !th.HasText("‹") || !th.HasText("›") {
		t.Fatalf("horizontal candidate panel missing expected markers; texts=%v", th.Texts())
	}
	r1h, ok1 := th.Find("你好世界")
	r2h, ok2 := th.Find("你好时间")
	if !ok1 || !ok2 {
		t.Fatalf("expected horizontal candidate texts, got texts=%v", th.Texts())
	}
	if r2h.X <= r1h.X || math.Abs(float64(r2h.Y-r1h.Y)) > 2 {
		t.Fatalf("expected horizontal layout (r2 right of r1, same Y): r1=%+v r2=%+v", r1h, r2h)
	}
	for idx, cand := range v.Candidates {
		badge := fmt.Sprintf("%d", idx+1)
		rb, okB := th.Find(badge)
		rc, okC := th.Find(cand.Text)
		if !okB || !okC {
			t.Fatalf("missing candidate %d (%s) in horizontal strip; texts=%v", idx+1, cand.Text, th.Texts())
		}
		assertRectWithin(t, "horiz-badge-"+badge, rb, hw, hh)
		assertRectWithin(t, "horiz-cand-"+cand.Text, rc, hw, hh)
	}

	// Render vertical panel at PreferredCandidatePanelSizeFor and verify geometry.
	tv := ui.NewTester(func(c *ui.Context) {
		InputCandidateViewFor(c, v, panelOf(vertCfg))
	}, vw, vh)
	tv.Frame()
	if !tv.HasText(v.Preedit) || !tv.HasText("✦") || !tv.HasText("‹") || !tv.HasText("›") {
		t.Fatalf("vertical candidate panel missing expected markers; texts=%v", tv.Texts())
	}
	r1v, ok1 := tv.Find("你好世界")
	r2v, ok2 := tv.Find("你好时间")
	if !ok1 || !ok2 {
		t.Fatalf("expected vertical candidate texts, got texts=%v", tv.Texts())
	}
	if r2v.Y <= r1v.Y || math.Abs(float64(r2v.X-r1v.X)) > 2 {
		t.Fatalf("expected vertical layout (r2 below r1, same X): r1=%+v r2=%+v", r1v, r2v)
	}
	rowPitch := float64(candVertRowHeight + candVertGap)
	if math.Abs(float64(r2v.Y-r1v.Y)-rowPitch) > 2 {
		t.Fatalf("vertical row pitch = %.1f, want %.1f", r2v.Y-r1v.Y, rowPitch)
	}
	for idx, cand := range v.Candidates {
		badge := fmt.Sprintf("%d", idx+1)
		rb, okB := tv.Find(badge)
		rc, okC := tv.Find(cand.Text)
		if !okB || !okC {
			t.Fatalf("missing candidate %d (%s) in vertical list; texts=%v", idx+1, cand.Text, tv.Texts())
		}
		assertRectWithin(t, "vert-badge-"+badge, rb, vw, vh)
		assertRectWithin(t, "vert-cand-"+cand.Text, rc, vw, vh)
	}
}

func TestCandidatePanelAccessibilityAndPresetDegradation(t *testing.T) {
	defer SetCandidateAppearance(config.DefaultAppearanceConfig())
	v := sampleCandidateView()

	cases := []struct {
		name        string
		preset      string
		animations  string
		shaders     bool
		orientation string
		prefs       ui.Preferences
	}{
		{"spatial3d-reduce-motion", "spatial_3d", "auto", true, "horizontal", ui.Preferences{ReduceMotion: true}},
		{"spatial3d-reduce-transparency", "spatial_3d", "full", true, "vertical", ui.Preferences{ReduceTransparency: true}},
		{"spatial3d-animations-off", "spatial_3d", "off", true, "horizontal", ui.Preferences{}},
		{"liquid-glass-reduced", "liquid_glass", "reduced", true, "vertical", ui.Preferences{}},
		{"classic-preset", "classic", "full", false, "horizontal", ui.Preferences{}},
		{"shaders-disabled", "spatial_3d", "full", false, "vertical", ui.Preferences{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultAppearanceConfig()
			cfg.VisualPreset = tc.preset
			cfg.Animations = tc.animations
			cfg.EnableShaders = tc.shaders
			cfg.CandidateBarOrientation = tc.orientation

			cur := v
			w, h := PreferredCandidatePanelSizeFor(cur, panelOf(cfg))
			tt := ui.NewTesterWith(func(c *ui.Context) {
				InputCandidateViewFor(c, cur, panelOf(cfg))
			}, w, h, ui.TesterOptions{Preferences: tc.prefs})
			tt.Frame()
			if tt.Image() == nil {
				t.Fatal("candidate frame image is nil")
			}

			// Move selection to candidate 2 (an AI candidate) and re-render. The selection
			// moves on every arrow key, so it switches at once in every preset: no keycap
			// spring or other frames left pending.
			cur.Selected = 1
			tt.Frame()
			if tt.FramePending() {
				t.Fatalf("%s left frames pending after a selection change", tc.name)
			}
			if tc.prefs.ReduceTransparency {
				// Center pixel of opaque fallback surface must be fully opaque.
				px := tt.Image().RGBAAt(w/2, h/2)
				if px.A != 255 {
					t.Fatalf("ReduceTransparency surface center alpha = %d, want 255", px.A)
				}
			}
		})
	}
}

func sseAIStreamDoer(chunks ...string) ai.HTTPDoer {
	return mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			return sseResponse(chunks...), nil
		},
	}
}

func startAIFromOmnibox(f *interactionFixture, prompt string) {
	f.ctrl.OpenOmnibox(config.TabAI, prompt)
	items := f.ctrl.State().OmniboxItems
	for i := f.ctrl.State().SelectedIndex; i < len(items)-1; i++ {
		f.ctrl.Dispatch(engine.Event{Type: engine.EventSelectNext})
	}
	f.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
}

func TestCapsulePhaseTransitionsAndPresetsHeadless(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	defer SetCandidateAppearance(config.DefaultAppearanceConfig())

	modes := []struct {
		name       string
		preset     string
		animations string
		shaders    bool
		prefs      ui.Preferences
		width      float64
	}{
		{"spatial3d-default", "spatial_3d", "full", true, ui.Preferences{}, 680},
		{"spatial3d-narrow", "spatial_3d", "full", true, ui.Preferences{}, 420},
		{"spatial3d-wide", "spatial_3d", "full", true, ui.Preferences{}, 960},
		{"liquid-glass", "liquid_glass", "full", true, ui.Preferences{}, 680},
		{"classic", "classic", "full", true, ui.Preferences{}, 680},
		{"animations-off", "spatial_3d", "off", true, ui.Preferences{}, 680},
		{"animations-reduced", "spatial_3d", "reduced", true, ui.Preferences{}, 680},
		{"reduce-motion-transparency", "spatial_3d", "auto", false, ui.Preferences{ReduceMotion: true, ReduceTransparency: true}, 680},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			f := newInteractionFixture(t, sseAIStreamDoer("Spatial ", "glass ", "verified!"))
			f.record(t, "clip-one", "clip-two")
			if err := f.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
				m.Appearance.VisualPreset = tc.preset
				m.Appearance.Animations = tc.animations
				m.Appearance.EnableShaders = tc.shaders
				m.Appearance.CapsuleWidth = tc.width
			}); err != nil {
				t.Fatalf("UpdateMainConfig: %v", err)
			}

			w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, f.ctrl.Config().Main.Appearance)
			tt := ui.NewTesterWith(f.ctrl.CapsuleView, w, h, ui.TesterOptions{
				Preferences: tc.prefs,
			})
			tt.Frame() // PhaseHidden baseline

			// 1. Open Leader phase.
			f.ctrl.Dispatch(engine.Event{
				Type: engine.EventOpenLeader,
				Context: &engine.CapsuleContext{
					AppName:      "Notes",
					SelectedText: "draft paragraph",
				},
			})
			tt.Frame()
			if tc.animations == "off" && tt.FramePending() {
				t.Fatal("animations=off must not request frames on Leader open")
			}
			tt.Advance(2 * time.Second)
			tt.Frame()
			if tt.FramePending() {
				t.Fatal("Leader open animation did not settle")
			}
			if !tt.HasText("Leader") {
				t.Fatalf("Leader phase missing breadcrumb; texts=%v", tt.Texts())
			}

			// 2. Press '/' to transition Leader -> Omnibox.
			tt.Key(0, ui.KeySlash)
			tt.Frame()
			if f.ctrl.State().Phase != engine.PhaseOmnibox {
				t.Fatalf("expected PhaseOmnibox after '/', got %s", f.ctrl.State().Phase)
			}
			if tc.animations == "off" && tt.FramePending() {
				t.Fatal("animations=off must not request frames on Leader->Omnibox transition")
			}
			if tc.preset == "spatial_3d" && tc.animations == "full" && !tc.prefs.ReduceMotion && !tt.FramePending() {
				t.Fatal("expected StageContainer3D spring frames on Leader->Omnibox transition")
			}
			tt.Advance(2 * time.Second)
			tt.Frame()
			if tt.FramePending() {
				t.Fatal("Leader->Omnibox stage transition did not settle")
			}

			// 3. Transition Omnibox -> AIStreaming and wait for stream completion.
			startAIFromOmnibox(f, "Polish draft paragraph")
			waitUntil(t, 2*time.Second, func() bool {
				st := f.ctrl.State()
				return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIOutput == "Spatial glass verified!"
			}, "AI stream completion")

			tt.Frame()
			tt.Advance(2 * time.Second)
			tt.Frame()
			if tt.FramePending() {
				t.Fatal("Omnibox->AIStreaming transition + glow did not settle once AIComplete")
			}
			replaceLabel := i18n.T("ai.replace")
			if !tt.HasText(replaceLabel) {
				t.Fatalf("AIStreaming missing %q button; texts=%v", replaceLabel, tt.Texts())
			}
			if tc.prefs.ReduceTransparency {
				px := tt.Image().RGBAAt(w/2, 22)
				if px.A != 255 {
					t.Fatalf("ReduceTransparency Capsule header alpha = %d, want 255", px.A)
				}
			}

			// Verify Replace button remains clickable even under 3D stage transform and narrow/wide widths.
			if err := tt.Click(replaceLabel); err != nil {
				t.Fatalf("click %q in %s: %v", replaceLabel, tc.name, err)
			}
			waitUntil(t, 2*time.Second, func() bool {
				return len(f.bridge.getInjected()) >= 1
			}, "AI Replace text injection")
		})
	}
}

func TestCapsuleAIActiveStreamingHeaderGlowDegradesCleanly(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	defer SetCandidateAppearance(config.DefaultAppearanceConfig())

	release := make(chan struct{})
	firstChunkSent := make(chan struct{})
	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			pr, pw := io.Pipe()
			go func() {
				_, _ = fmt.Fprint(pw, "data: {\"choices\":[{\"delta\":{\"content\":\"Streaming...\"}}]}\n\n")
				close(firstChunkSent)
				<-release
				_, _ = fmt.Fprint(pw, "data: [DONE]\n\n")
				_ = pw.Close()
			}()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr,
			}, nil
		},
	}

	f := newInteractionFixture(t, doer)
	startAIFromOmnibox(f, "Stream test")
	<-firstChunkSent
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && !st.AIComplete && st.AIOutput == "Streaming..."
	}, "mid-stream AI output")

	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, f.ctrl.Config().Main.Appearance)
	for _, shadersOn := range []bool{true, false} {
		_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
			m.Appearance.EnableShaders = shadersOn
			m.Appearance.VisualPreset = "spatial_3d"
		})
		tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
		tt.Frame()
		if tt.Image() == nil || !tt.HasText("Streaming...") {
			t.Fatalf("mid-stream frame failed with EnableShaders=%v; texts=%v", shadersOn, tt.Texts())
		}
	}
	close(release)
	waitUntil(t, 2*time.Second, func() bool { return f.ctrl.State().AIComplete }, "stream finish")
}

func assertRectWithin(t *testing.T, label string, r ui.Rect, w, h int) {
	t.Helper()
	if r.W <= 0 || r.H <= 0 {
		t.Fatalf("%s has empty rect %+v", label, r)
	}
	if r.X < -1 || r.Y < -1 || r.X+r.W > float32(w)+1 || r.Y+r.H > float32(h)+1 {
		t.Fatalf("%s rect %+v overflows panel %dx%d", label, r, w, h)
	}
}

// TestCaptureSpatialWireScreenshots writes composite PNGs over a checkerboard/wallpaper
// backdrop when CLAVIS_CAPTURE_SCREENSHOTS=<dir> is set for visual verification.
func TestCaptureSpatialWireScreenshots(t *testing.T) {
	outDir := strings.TrimSpace(os.Getenv("CLAVIS_CAPTURE_SCREENSHOTS"))
	if outDir == "" {
		return
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	defer i18n.SetLanguage(i18n.English)
	defer SetCandidateAppearance(config.DefaultAppearanceConfig())

	savePNG := func(name string, img *image.RGBA, dark bool) {
		t.Helper()
		b := img.Bounds()
		bg := image.NewRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if dark {
					bg.SetRGBA(x, y, color.RGBA{R: 18 + uint8((x*24)/max(b.Dx(), 1)), G: 22 + uint8((y*20)/max(b.Dy(), 1)), B: 36, A: 255})
				} else {
					bg.SetRGBA(x, y, color.RGBA{R: 232, G: 237, B: 246, A: 255})
				}
			}
		}
		draw.Draw(bg, b, img, b.Min, draw.Over)
		fp := filepath.Join(outDir, name+".png")
		f, err := os.Create(fp)
		if err != nil {
			t.Fatalf("create %s: %v", fp, err)
		}
		defer f.Close()
		if err := png.Encode(f, bg); err != nil {
			t.Fatalf("encode %s: %v", fp, err)
		}
	}

	v := sampleCandidateView()
	for _, spec := range []struct {
		name, orient, preset, theme string
		dark                        bool
		prefs                       ui.Preferences
	}{
		{"cand_horiz_spatial3d_dark", "horizontal", "spatial_3d", "dark", true, ui.Preferences{}},
		{"cand_horiz_spatial3d_light", "horizontal", "spatial_3d", "light", false, ui.Preferences{}},
		{"cand_vert_spatial3d_dark", "vertical", "spatial_3d", "dark", true, ui.Preferences{}},
		{"cand_vert_opaque_light", "vertical", "spatial_3d", "light", false, ui.Preferences{ReduceTransparency: true, ReduceMotion: true}},
		{"cand_horiz_classic_dark", "horizontal", "classic", "dark", true, ui.Preferences{}},
	} {
		cfg := config.DefaultAppearanceConfig()
		cfg.CandidateBarOrientation = spec.orient
		cfg.VisualPreset = spec.preset
		cfg.Theme = spec.theme
		w, h := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
		tt := ui.NewTesterWith(func(c *ui.Context) {
			InputCandidateViewFor(c, v, panelOf(cfg))
		}, w, h, ui.TesterOptions{Dark: spec.dark, Preferences: spec.prefs})
		tt.Frame()
		tt.Advance(500 * time.Millisecond)
		tt.Frame()
		savePNG(spec.name, tt.Image(), spec.dark)
	}

	// Capture Capsule across Leader, Omnibox (narrow 420 & wide 960), and AIStreaming (spatial_3d, liquid_glass, classic).
	for _, spec := range []struct {
		name, preset, phase string
		width               float64
		dark                bool
		prefs               ui.Preferences
	}{
		{"capsule_leader_spatial3d_dark", "spatial_3d", "leader", 680, true, ui.Preferences{}},
		{"capsule_omnibox_narrow_420", "spatial_3d", "omnibox", 420, true, ui.Preferences{}},
		{"capsule_omnibox_wide_960", "spatial_3d", "omnibox", 960, true, ui.Preferences{}},
		{"capsule_ai_spatial3d_dark", "spatial_3d", "ai", 680, true, ui.Preferences{}},
		{"capsule_ai_liquid_glass_light", "liquid_glass", "ai", 680, false, ui.Preferences{}},
		{"capsule_ai_classic_dark", "classic", "ai", 680, true, ui.Preferences{}},
	} {
		f := newInteractionFixture(t, sseAIStreamDoer("**Polished Output:** ", "Clavis Spatial 3D & Liquid Glass active."))
		f.record(t, "https://github.com/yeisme/clavis", "go test -count=1 ./...")
		themeName := "light"
		if spec.dark {
			themeName = "dark"
		}
		_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
			m.Appearance.VisualPreset = spec.preset
			m.Appearance.CapsuleWidth = spec.width
			m.Appearance.Theme = themeName
		})

		var phase engine.CapsulePhase
		switch spec.phase {
		case "leader":
			phase = engine.PhaseLeader
			f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Notes"}})
		case "omnibox":
			phase = engine.PhaseOmnibox
			f.ctrl.OpenOmnibox(config.TabClipboard, "")
		case "ai":
			phase = engine.PhaseAIStreaming
			startAIFromOmnibox(f, "Polish raw note")
			waitUntil(t, 2*time.Second, func() bool { return f.ctrl.State().AIComplete }, "ai complete")
		}
		w, h := PreferredCapsuleSize(phase, f.ctrl.Config().Main.Appearance)
		tt := ui.NewTesterWith(f.ctrl.CapsuleView, w, h, ui.TesterOptions{Dark: spec.dark, Preferences: spec.prefs})
		tt.Frame()
		tt.Advance(2 * time.Second)
		tt.Frame()
		savePNG(spec.name, tt.Image(), spec.dark)
	}
}
