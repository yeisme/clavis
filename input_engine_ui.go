package main

import (
	"context"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/inputengine"
	"github.com/yeisme/clavis/uiapp"
)

// Own input engine UI glue (docs/design/own-input-engine.md, route B): candidate panel shown
// with ShowInactive under the caret, plus the debounced async AI suggestion loop. Active when
// [input_engine] enabled=true and the bridge implements bridge.InputEngineHost (both the
// darwin and the Windows bridge do). The panel's look is [input_engine.style].

const candidatePanelGap = 4.0

type inputEngineUI struct {
	host  bridge.InputEngineHost
	panel *mygo.Window
	view  atomic.Pointer[bridge.InputEngineView]
	ai    aiDebouncer
	// sizer keeps the panel from resizing on every keystroke: within one
	// composition it only grows, in steps.
	sizer uiapp.CandidatePanelSizer
}

func (a *desktopApp) setupInputEngine(ctx context.Context, br bridge.HostService, cfg config.InputEngineConfig, suggest suggestFunc) *inputEngineUI {
	if !cfg.Enabled {
		return nil
	}
	h, ok := br.(bridge.InputEngineHost)
	if !ok {
		return nil
	}
	u := &inputEngineUI{host: h}
	u.ai = newInputEngineAIDebouncer(br, inputengine.AIDebounce, suggest, h.SetInputEngineAICandidates)
	w, hgt := uiapp.PreferredCandidatePanelSize(inputengine.View{})
	u.panel = mygo.NewWindow(mygo.WindowOptions{
		Title: "Clavis Input", Width: w, Height: hgt,
		Panel: true, Hidden: true, Frameless: true, Transparent: true,
		// The window mask is fixed at creation; the content follows [input_engine.style]
		// live, so a changed radius needs a restart to reach the native corners.
		Vibrancy: mygo.VibrancyGlass, CornerRadius: uiapp.CandidatePanelCornerRadius(), DisableResize: true,
		AlwaysOnTop: true, SkipTaskbar: true,
		Content: ui.View(func(c *ui.Context) {
			if v := u.view.Load(); v != nil {
				uiapp.InputCandidateView(c, v.View)
			}
		}),
	})
	u.panel.OnClose(a.closeHides(u.panel.Hide))
	h.SetInputEngineViewHandler(func(v bridge.InputEngineView) { u.onView(ctx, v) })
	h.ConfigureInputEngine(cfg)
	return u
}

func (u *inputEngineUI) onView(ctx context.Context, v bridge.InputEngineView) {
	u.view.Store(&v)
	u.ai.Changed(ctx, v.Raw, v.View.Composing)
	mygo.RunOnMain(func() {
		cur := u.view.Load()
		if cur == nil || !cur.View.Composing || len(cur.View.Candidates) == 0 && cur.View.Preedit == "" {
			u.sizer.Reset()
			u.panel.Hide()
			return
		}
		pw, ph := u.sizer.Next(cur.View, uiapp.CurrentCandidatePanelConfig())
		bounds := computeCandidatePanelBounds(cur.Anchor, mygo.Screen.CursorScreenPoint(),
			bridge.Size{Width: float64(pw), Height: float64(ph)}, mygo.Screen.Displays())
		u.panel.SetBounds(bounds)
		u.panel.ShowInactive()
		u.panel.Invalidate()
	})
}

// computeCandidatePanelBounds puts the panel just below the caret, flipping above when it
// would leave the work area, then clamps horizontally.
func computeCandidatePanelBounds(anchor *host.CursorAnchor, cursor mygo.Point, size bridge.Size, displays []mygo.Display) mygo.Rectangle {
	r := resolveAnchorRect(anchor, cursor)
	x := r.X
	y := r.MaxY() + candidatePanelGap
	if ds := toBridgeDisplays(displays); len(ds) > 0 {
		d := selectBridgeDisplayForPoint(r.Center(), ds)
		wa := d.WorkArea
		if wa.IsZero() {
			wa = d.Bounds
		}
		if y+size.Height > wa.MaxY()-bridge.PopoverScreenMargin {
			y = r.Y - size.Height - candidatePanelGap
		}
		x = clampScreenAxis(x, size.Width, wa.X, wa.Width, bridge.PopoverScreenMargin)
		y = clampScreenAxis(y, size.Height, wa.Y, wa.Height, bridge.PopoverScreenMargin)
	}
	return mygo.Rectangle{X: int(math.Round(x)), Y: int(math.Round(y)), Width: int(size.Width), Height: int(size.Height)}
}

type suggestFunc func(ctx context.Context, raw, appName string) []string

// aiDebouncer fires suggest once raw has been stable for delay; any newer raw cancels the
// in-flight request and bumps gen so late replies are dropped (design §5).
type aiDebouncer struct {
	delay      time.Duration
	suggest    suggestFunc
	deliver    func(raw string, words []string)
	resolveApp func(ctx context.Context) string

	mu     sync.Mutex
	last   string
	gen    uint64
	timer  *time.Timer
	cancel context.CancelFunc
}

func newInputEngineAIDebouncer(br bridge.HostService, delay time.Duration, suggest suggestFunc, deliver func(raw string, words []string)) aiDebouncer {
	return aiDebouncer{
		delay:   delay,
		suggest: suggest,
		deliver: deliver,
		resolveApp: func(ctx context.Context) string {
			return resolveInputEngineAppName(ctx, br)
		},
	}
}

func resolveInputEngineAppName(ctx context.Context, br bridge.HostService) string {
	if br == nil {
		return ""
	}
	if rep, ok := br.(bridge.ForegroundIMEReporter); ok {
		if st, ok := rep.ForegroundIMEStatus(); ok {
			if name := firstNonEmptyAppLabel(st.App.ProcessName, st.App.BundleID); name != "" {
				return name
			}
		}
	}
	if snap, err := br.SnapshotContext(ctx); err == nil {
		return firstNonEmptyAppLabel(snap.AppName, snap.ProcessName, snap.BundleID)
	}
	return ""
}

func firstNonEmptyAppLabel(candidates ...string) string {
	for _, c := range candidates {
		if t := strings.TrimSpace(c); t != "" {
			return t
		}
	}
	return ""
}

func (d *aiDebouncer) Changed(ctx context.Context, raw string, composing bool) {
	if d.suggest == nil || d.deliver == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if raw == d.last && composing {
		return // view-only change (paging, AI merge)
	}
	d.last = raw
	d.gen++
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	if !composing || !inputengine.ShouldQueryAI(raw) {
		return
	}
	gen := d.gen
	d.timer = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		if d.gen != gen {
			d.mu.Unlock()
			return
		}
		rctx, cancel := context.WithCancel(ctx)
		d.cancel = cancel
		d.mu.Unlock()
		var appName string
		if d.resolveApp != nil {
			appName = d.resolveApp(rctx)
		}
		words := d.suggest(rctx, raw, appName)
		cancel()
		d.mu.Lock()
		fresh := d.gen == gen
		d.mu.Unlock()
		if fresh && len(words) > 0 {
			d.deliver(raw, words)
		}
	})
}
