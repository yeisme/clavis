package uiapp

import (
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
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
			suffix := l.tag + "-" + mode
			t.Run(suffix, func(t *testing.T) {
				defer i18n.SetLanguage(i18n.English)
				i18n.SetLanguage(l.lang)
				renderSnapshotMatrix(t, dir, suffix, dark)
			})
		}
	}
}

func renderSnapshotMatrix(t *testing.T, dir, suffix string, dark bool) {
	doer := mockHTTPDoer{fn: func(req *http.Request) (*http.Response, error) {
		return sseResponse("这段代码的作用是：", "\\n\\n1. 读取配置文件；", "\\n2. 校验字段并返回错误；", "\\n3. 原子写回磁盘。"), nil
	}}
	f := newInteractionFixture(t, doer)
	f.record(t,
		"https://github.com/yeisme/clavis/pull/12",
		"git commit -m \"feat: 输入法候选窗\"",
		"微信支付回调签名校验失败，请检查商户证书与 APIv3 密钥是否匹配，然后重新发起退款请求。",
		"#3B82F6",
		"SELECT id, content, kind FROM clipboard_items WHERE kind = 'image' ORDER BY updated_at DESC LIMIT 20;",
	)
	appearance := f.ctrl.Config().Main.Appearance
	ctx := &engine.CapsuleContext{AppName: "Visual Studio Code", SelectedText: "func main() {}"}

	shoot := func(name string, view func(c *ui.Context), w, h int, scale float32, drive func(tt *ui.Tester)) {
		t.Helper()
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

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?解释这段代码")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI completion")
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("06-ai", f.ctrl.CapsuleView, w, h, 2, nil)

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
		cw, ch := PreferredCandidatePanelSizeFor(view, app)
		shoot("11-candidates-"+orientation, func(c *ui.Context) { InputCandidateViewFor(c, view, app) }, cw, ch, 2, nil)
	}

	f.ctrl.OpenSettings(SettingsTabGeneral)
	shoot("09-settings-general", f.ctrl.SettingsView, 960, 640, 1.5, nil)
	f.ctrl.OpenSettings(SettingsTabIMEKeymap)
	shoot("10-settings-ime", f.ctrl.SettingsView, 960, 640, 1.5, nil)
}
