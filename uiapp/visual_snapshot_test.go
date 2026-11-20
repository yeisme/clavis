package uiapp

import (
	"context"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// TestVisualSnapshots renders every transient surface (Leader, Omnibox,
// Clipboard + preview, AI, HUD, floating pills) and two Settings pages to PNG
// for visual review. It is opt-in so the regular suite stays fast and does not
// depend on platform fonts:
//
//	CLAVIS_SNAPSHOT_DIR=/tmp/clavis-shots go test -run TestVisualSnapshots ./uiapp/
//
// The headless Tester rasterizes real pixels, so before/after comparisons of a
// visual change need neither the running app nor its OS permissions.
func TestVisualSnapshots(t *testing.T) {
	dir := os.Getenv("CLAVIS_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set CLAVIS_SNAPSHOT_DIR to write visual snapshots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	langs := []struct {
		tag  string
		lang i18n.Language
	}{{"zh", i18n.Chinese}, {"en", i18n.English}}

	for _, l := range langs {
		for _, dark := range []bool{false, true} {
			mode := "light"
			if dark {
				mode = "dark"
			}
			for _, preset := range []string{"classic", "liquid_glass", "spatial_3d"} {
				for _, motion := range []string{"full", "reduced"} {
					suffix := l.tag + "-" + mode + "-" + preset + "-" + motion
					t.Run(suffix, func(t *testing.T) {
						defer i18n.SetLanguage(i18n.English)
						i18n.SetLanguage(l.lang)
						renderSnapshotMatrix(t, dir, suffix, dark, preset, motion)
					})
				}
			}
		}
	}
	validateVisualSnapshotPNGs(t, dir)
}

func renderSnapshotMatrix(t *testing.T, dir, suffix string, dark bool, preset, motion string) {
	core := preset == "spatial_3d" && motion == "full"
	coreFrame := func(name string) bool {
		return name == "01-leader" || name == "02-omnibox" || (len(name) >= 2 && name[:2] == "06") || (len(name) >= 2 && name[:2] == "09") || (len(name) >= 2 && name[:2] == "18")
	}
	if core {
		t.Log("snapshot coverage: full matrix for core frames; non-core frames are narrowed to spatial_3d/full")
	} else {
		t.Log("snapshot coverage narrowed: Leader, Omnibox, AI, Settings General, and all onboarding frames only; non-core frames omitted outside spatial_3d/full")
	}
	doer := mockHTTPDoer{fn: func(req *http.Request) (*http.Response, error) {
		return sseResponse("这段代码的作用是：", "\\n\\n1. 读取配置文件；", "\\n2. 校验字段并返回错误；", "\\n3. 原子写回磁盘。"), nil
	}}
	f := newInteractionFixture(t, doer)
	// Clipboard rows show "source · relative time · N lines": a fixed clock and
	// back-dated captures keep that meta line identical from run to run.
	snapNow := time.Date(2026, 11, 20, 15, 4, 0, 0, time.Local)
	restoreClock := clipboardClock
	clipboardClock = func() time.Time { return snapNow }
	defer func() { clipboardClock = restoreClock }()
	ctxRecord := context.Background()
	mediaPath := filepath.Join(t.TempDir(), "preview.png")
	if err := os.WriteFile(mediaPath, testPNG16(t), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, clip := range []clipboard.CaptureCandidate{
		{Content: "https://github.com/yeisme/clavis/pull/12", SourceAppName: "Safari", Timestamp: snapNow.AddDate(0, 0, -4)},
		{Content: "preview-media-deck", SourceAppName: "Photos", Timestamp: snapNow.Add(-26 * time.Hour),
			Media: clipboard.Media{Kind: clipboard.KindImage, BlobSHA: "deck-sha", MediaPath: mediaPath, ThumbPath: mediaPath, Width: 1920, Height: 1080, ByteSize: 2400000}},
		{Content: "git commit -m \"feat: 输入法候选窗\"", SourceAppName: "Terminal", Timestamp: snapNow.Add(-3 * time.Hour)},
		{Content: "微信支付回调签名校验失败，请检查商户证书与 APIv3 密钥是否匹配，然后重新发起退款请求。", Timestamp: snapNow.Add(-18 * time.Minute)},
		{Content: "#3B82F6", SourceAppName: "Figma", Timestamp: snapNow.Add(-2 * time.Minute)},
		{Content: "SELECT id, content, kind\nFROM clipboard_items\nWHERE kind = 'image'\nORDER BY updated_at DESC\nLIMIT 20;", SourceAppName: "Notes", Timestamp: snapNow.Add(-20 * time.Second)},
	} {
		if _, _, err := f.clips.Record(ctxRecord, clip); err != nil {
			t.Fatal(err)
		}
	}
	lang := i18n.Current()
	_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Appearance.VisualPreset = preset
		m.Appearance.Animations = motion
		m.Appearance.ObliqueRows = true
		// Saving the config re-resolves [appearance].language; pin it to the
		// subtest's language or every "zh" frame silently renders English.
		m.Appearance.Language = string(lang)
	})
	appearance := f.ctrl.Config().Main.Appearance
	ctx := &engine.CapsuleContext{AppName: "Visual Studio Code", SelectedText: "func main() {}"}

	shoot := func(name string, view func(c *ui.Context), w, h int, scale float32, drive func(tt *ui.Tester)) {
		t.Helper()
		if !core && !coreFrame(name) {
			return
		}
		tt := ui.NewTester(view, w, h)
		tt.SetDark(dark)
		tt.SetScale(scale)
		tt.Frame()
		if drive != nil {
			drive(tt)
		}
		tt.Advance(2 * time.Second)
		tt.Frame()
		out, err := os.Create(filepath.Join(dir, name+"-"+suffix+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		if err := png.Encode(out, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: ctx})
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("01-leader", f.ctrl.CapsuleView, w, h, 2, nil)

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("02-omnibox", f.ctrl.CapsuleView, w, h, 2, nil)

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	shoot("03-omnibox-query", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Type("fy")
		tt.Frame()
	})

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	shoot("03b-omnibox-hover", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Move(float32(w/2), float32(h/2+20))
		tt.Frame()
	})
	shoot("03c-omnibox-focused", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Key(0, ui.KeyDown)
		tt.Frame()
	})
	shoot("04-omnibox-alt-held", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.HoldModifiers(ui.Alt)
		tt.Frame()
	})

	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 5 }, "clipboard items")
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("05-clipboard", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Key(0, ui.KeyDown)
		tt.Key(0, ui.KeyDown)
		tt.Frame()
	})
	f.ctrl.OpenOmnibox(config.TabClipboard, "#3B82F6")
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("05b-hex-colour", f.ctrl.CapsuleView, w, h, 2, nil)
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	for idx, candidate := range f.ctrl.State().OmniboxItems {
		if candidate.ClipboardMedia != nil && candidate.ClipboardMedia.IsMedia() {
			f.ctrl.SelectIndex(idx)
			break
		}
	}
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("05c-preview-media-deck", f.ctrl.CapsuleView, w, h, 2, nil)

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?解释这段代码")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	// Capture the active orb as soon as the AI phase opens.
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("06a-ai-streaming", f.ctrl.CapsuleView, w, h, 2, nil)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI completion")
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("06-ai", f.ctrl.CapsuleView, w, h, 2, nil)

	// AI rewrite review: a close edit opens on the changes view.
	rf := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("这段代码负责读取配置文件，并校验所有字段后原子写回磁盘。"), nil
	}})
	rf.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: &engine.CapsuleContext{
		AppName: "Notes", SelectedText: "这段代码的作用是读取配置文件，然后校验字段并写回磁盘。",
	}})
	rt := ui.NewTester(rf.ctrl.CapsuleView, w, h)
	rt.Frame()
	rt.Type("?润色")
	rt.Frame()
	rt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := rf.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI review completion")
	w, h = PreferredCapsuleSizeForState(rf.ctrl.State(), appearance)
	shoot("06b-ai-review", rf.ctrl.CapsuleView, w, h, 2, nil)

	f.ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.copied")})
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("07-hud", f.ctrl.CapsuleView, w, h, 2, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})

	for i, label := range []string{"中", "EN", "LAYER • NAV", "已绑定 Notes → 英文"} {
		lbl := label
		pw, ph := PreferredPillSizeForLabel(lbl)
		shoot("08-pill-"+string(rune('a'+i)), func(c *ui.Context) { IMEPillView(c, lbl) }, pw, ph, 3, nil)
	}

	// Own input engine candidate window: a mid-composition page with the
	// selection on the first cell and an AI suggestion, in both orientations.
	view := inputengine.View{
		Composing: true,
		Preedit:   "shu'ru'fa",
		Candidates: []inputengine.Candidate{
			{Text: "输入法"}, {Text: "书如法"}, {Text: "输入"}, {Text: "舒"}, {Text: "输"},
			{Text: "输入法候选窗", AI: true},
		},
		Selected: 0,
		Page:     0,
		HasNext:  true,
	}
	for _, orientation := range []string{"horizontal", "vertical"} {
		app := appearance
		app.CandidateBarOrientation = orientation
		cw, ch := PreferredCandidatePanelSizeFor(view, panelOf(app))
		shoot("11-candidates-"+orientation, func(c *ui.Context) { InputCandidateViewFor(c, view, panelOf(app)) }, cw, ch, 2, nil)
	}

	f.ctrl.OpenSettings(SettingsTabGeneral)
	shoot("09-settings-general", f.ctrl.SettingsView, 960, 640, 1.5, nil)
	shoot("09b-settings-general-full", f.ctrl.SettingsView, 960, 1000, 1.5, nil)
	f.ctrl.OpenSettings(SettingsTabIMEKeymap)
	shoot("10-settings-ime", f.ctrl.SettingsView, 960, 640, 1.5, nil)
	shoot("10b-settings-ime-full", f.ctrl.SettingsView, 960, 1500, 1.5, nil)
	// The window's minimum size: two narrow columns.
	shoot("10c-settings-ime-min", f.ctrl.SettingsView, 760, 500, 1.5, nil)
	// A declared workflow so the Workflows editor snapshot shows its fields
	// rather than the empty state.
	if err := f.ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID:          "summarize-selection",
		Name:        "Summarize Selection",
		Description: "Rewrite the selection into bullet points.",
		Enabled:     true,
		Source:      config.WorkflowSourceSelection,
		Output:      config.WorkflowOutputReplace,
		Failure:     config.WorkflowFailureStop,
		Execution:   config.WorkflowExecutionSequential,
		Steps: []config.WorkflowStep{{
			ID: "draft", Name: "Draft", Action: "script.echo", Input: "text",
			Enabled: true, Source: config.WorkflowSourceSelection, Output: config.WorkflowOutputReplace,
			Failure: config.WorkflowFailureStop, Execution: config.WorkflowExecutionSequential,
		}},
	}}}); err != nil {
		t.Fatalf("seed workflows: %v", err)
	}
	for _, sec := range []struct {
		name  string
		index int
	}{
		{"16-settings-permissions-telemetry", settingsSectionPermissions},
		{"12-settings-glossary-clipboard", settingsSectionGlossary},
		{"13-settings-leader", settingsSectionLeader},
		{"14-settings-snippets", settingsSectionSnippets},
		{"15-settings-ai", settingsSectionAI},
		{"17-settings-configs", settingsSectionConfigs},
		{"19-settings-workflows", settingsSectionWorkflows},
	} {
		f.ctrl.setSettingsSection(sec.index)
		shoot(sec.name, f.ctrl.SettingsView, 960, 900, 1.5, nil)
	}

	// The first-run guide, from a fresh Mac to a working Leader key, its
	// smallest window, and Windows.
	guide := func(goos string) func(c *ui.Context) {
		return func(c *ui.Context) { f.ctrl.renderOnboarding(c, goos) }
	}
	_ = f.ctrl.Stop()
	f.bridge.mu.Lock()
	f.bridge.perms = host.PermissionStatus{}
	f.bridge.mu.Unlock()
	_, _ = f.ctrl.RefreshPermissions(context.Background())
	shoot("18-onboarding-fresh", guide("darwin"), 520, 520, 1.5, nil)
	shoot("18b-onboarding-min", guide("darwin"), 520, 520, 1.5, nil)
	f.bridge.mu.Lock()
	f.bridge.perms = host.PermissionStatus{Accessibility: true, InputMonitoring: true, Granted: true}
	f.bridge.mu.Unlock()
	_, _ = f.ctrl.RefreshPermissions(context.Background())
	f.ctrl.SetRelaunchNeeded(true)
	shoot("18c-onboarding-relaunch", guide("darwin"), 520, 520, 1.5, nil)
	_ = f.ctrl.Start(context.Background())
	shoot("18d-onboarding-waiting", guide("darwin"), 520, 520, 1.5, nil)
	f.ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	f.ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
	shoot("18e-onboarding-ready", guide("darwin"), 520, 520, 1.5, nil)
	shoot("18f-onboarding-windows", guide("windows"), 520, 520, 1.5, nil)
}
