package uiapp

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// MediaZoomMode is deliberately small and serialisation-free: it is transient
// UI state, never clipboard data. Fit tracks the available canvas while the
// other modes use an explicit percentage.
type MediaZoomMode uint8

const (
	MediaZoomFit MediaZoomMode = iota
	MediaZoomOriginal
	MediaZoomCustom
)

const (
	mediaZoomMin  float32 = 0.25
	mediaZoomMax  float32 = 8
	mediaZoomStep float32 = 0.25
)

type mediaPreviewState struct {
	itemID       string
	path         string
	mode         MediaZoomMode
	zoom         float32
	panX         float32
	panY         float32
	dragging     bool
	lastX, lastY float32
	focused      bool
	loading      bool
	failed       bool
	generation   uint64
}

func clampMediaZoom(v float32) float32 {
	if v < mediaZoomMin {
		return mediaZoomMin
	}
	if v > mediaZoomMax {
		return mediaZoomMax
	}
	return v
}

func mediaZoomPercent(mode MediaZoomMode, custom float32) int {
	if mode == MediaZoomFit {
		return 0
	}
	if mode == MediaZoomOriginal {
		return 100
	}
	return int(math.Round(float64(clampMediaZoom(custom) * 100)))
}

func mediaZoomLabel(mode MediaZoomMode, custom float32) string {
	if mode == MediaZoomFit {
		return i18n.T("preview.media.fit")
	}
	return fmt.Sprintf("%d%%", mediaZoomPercent(mode, custom))
}

func (a *AppController) resetMediaPreview(item engine.OmniboxItem) {
	m := item.ClipboardMedia
	path := ""
	if m != nil {
		path = m.Path
	}
	if a.uiState.mediaPreview.itemID == item.ID && a.uiState.mediaPreview.path == path {
		return
	}
	a.uiState.mediaPreview = mediaPreviewState{
		itemID:     item.ID,
		path:       path,
		mode:       MediaZoomFit,
		zoom:       1,
		generation: a.uiState.mediaPreview.generation + 1,
	}
}

func (a *AppController) setMediaZoom(mode MediaZoomMode, zoom float32) {
	p := &a.uiState.mediaPreview
	p.mode = mode
	if mode == MediaZoomCustom {
		p.zoom = clampMediaZoom(zoom)
	} else if mode == MediaZoomOriginal {
		p.zoom = 1
	} else {
		p.zoom = 1
	}
	p.panX, p.panY = 0, 0
}

func (a *AppController) adjustMediaZoom(delta float32) {
	p := &a.uiState.mediaPreview
	base := p.zoom
	if p.mode == MediaZoomFit {
		base = 1
	}
	a.setMediaZoom(MediaZoomCustom, base+delta)
}

func mediaPreviewCanvasHeight(m engine.ClipboardMedia) float32 {
	// A taller token-sized canvas gives small images room to grow while still
	// leaving metadata and actions visible in the Omnibox. The source aspect
	// ratio only influences the image itself, so transparent pixels are never
	// cropped.
	if m.Width > 0 && m.Height > 0 && m.Height > m.Width {
		return mediaCanvasPortraitHeight
	}
	return mediaCanvasHeight
}

func (a *AppController) handleMediaPreviewInput(ev ui.InputEvent) bool {
	p := &a.uiState.mediaPreview
	if ev.Kind == ui.InputKeyDown && (ev.Mods == 0 || (ev.Mods == ui.Shift && ev.Key == ui.KeyEqual)) && p.focused {
		switch ev.Key {
		case ui.Key0:
			a.setMediaZoom(MediaZoomFit, 1)
			return true
		case ui.Key1:
			a.setMediaZoom(MediaZoomOriginal, 1)
			return true
		case ui.KeyMinus:
			a.adjustMediaZoom(-mediaZoomStep)
			return true
		case ui.KeyEqual:
			a.adjustMediaZoom(mediaZoomStep)
			return true
		}
	}
	switch ev.Kind {
	case ui.InputZoom:
		a.setMediaZoom(MediaZoomCustom, p.zoom*ev.Scale)
		return true
	case ui.InputScroll:
		// Scroll events are scoped to the canvas, so hover-wheel zoom does not
		// steal keyboard navigation from the Omnibox list.
		if ev.Mods == 0 && ev.DY != 0 {
			a.adjustMediaZoom(float32(-math.Copysign(float64(mediaZoomStep), float64(ev.DY))))
			return true
		}
	case ui.InputPointerDown:
		p.focused = true
		if ev.Clicks >= 2 {
			if p.mode == MediaZoomFit {
				a.setMediaZoom(MediaZoomOriginal, 1)
			} else {
				a.setMediaZoom(MediaZoomFit, 1)
			}
			return true
		}
		if ev.Button == 0 && (p.mode != MediaZoomFit || p.zoom > 1) {
			p.dragging = true
			p.lastX, p.lastY = ev.X, ev.Y
			return true
		}
	case ui.InputPointerMove:
		if p.dragging && ev.Button == 0 {
			p.panX += ev.X - p.lastX
			p.panY += ev.Y - p.lastY
			p.lastX, p.lastY = ev.X, ev.Y
			return true
		}
	case ui.InputPointerUp:
		p.dragging = false
		return p.focused
	}
	return false
}

func (a *AppController) renderClipboardMediaToolbar(c *ui.Context, theme *ui.Theme) {
	p := a.uiState.mediaPreview
	ui.Row(c).FillWidth().Gap(space2).AlignItems(ui.Center).Children(func() {
		// The preview rail is intentionally compact (especially at the minimum
		// Capsule width), so visible text labels stay readable without relying on
		// clipped icon+label pills. These are still real buttons with localized
		// labels and keyboard-focus behavior.
		button := func(label string, active bool, fn func()) {
			b := ui.ButtonBase(c).Height(controlHeight).Padding(space2, space4).Radius(radiusPill).FocusRing(false)
			if active {
				b.Background(theme.Accent.Alpha(alphaSubtle)).Border(space1, theme.Accent.Alpha(alphaHalf))
			} else {
				b.Background(tileFill(theme)).Border(space1, hairline(theme))
			}
			color := theme.TextMuted
			if active {
				color = accentInk(theme, surfaceBase(theme))
			}
			b.Children(func() {
				ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(color).SingleLine()
			})
			if b.Clicked() {
				fn()
			}
		}
		button(i18n.T("preview.media.fit"), p.mode == MediaZoomFit, func() { a.setMediaZoom(MediaZoomFit, 1) })
		button("−", false, func() { a.adjustMediaZoom(-mediaZoomStep) })
		ui.Text(c, mediaZoomLabel(p.mode, p.zoom)).FontSize(typeMicro).FontWeight(700).TextColor(theme.Text).SingleLine()
		button("+", false, func() { a.adjustMediaZoom(mediaZoomStep) })
		button(i18n.T("preview.media.original"), p.mode == MediaZoomOriginal, func() { a.setMediaZoom(MediaZoomOriginal, 1) })
	})
}

func (a *AppController) renderClipboardMediaCanvas(c *ui.Context, theme *ui.Theme, m engine.ClipboardMedia) {
	p := &a.uiState.mediaPreview
	canvasHeight := mediaPreviewCanvasHeight(m)
	canvas := ui.Box(c).FillWidth().Height(canvasHeight).Clip().Focusable().Bind(&a.uiState.mediaPreviewHandle).FocusRing(false)
	canvas.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputPointerDown {
			a.uiState.mediaPreviewHandle.RequestFocus(c)
		}
		return a.handleMediaPreviewInput(ev)
	})
	canvas.Children(func() {
		bmp := a.ThumbBitmap(m.ThumbPath)
		if m.Kind == "image" && m.Path != "" {
			// Keep the thumbnail visible while the <=8 MiB original is decoded;
			// a missing/corrupt original therefore has a useful visual fallback.
			if original := a.requestOriginalBitmap(m.Path, p); original != nil {
				bmp = original
			}
		}
		if bmp == nil {
			if p.loading {
				ui.Text(c, i18n.T("preview.media.loading")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			} else {
				ui.Text(c, i18n.T("preview.media.unavailable")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			}
			return
		}
		if p.mode == MediaZoomFit || p.loading || p.failed {
			ui.Image(c, bmp).FillWidth().FillHeight().Fit(ui.Contain).Radius(radiusChip)
			return
		}
		w, h := bmp.Size()
		if w <= 0 || h <= 0 {
			return
		}
		zoom := p.zoom
		// Use the decoded image's native dimensions for Original, and a bounded
		// canvas-relative base for Custom so 25% is still useful on huge images.
		base := float32(w)
		if p.mode == MediaZoomCustom && base > mediaImageBase {
			base = mediaImageBase
		}
		width := base * zoom
		height := float32(h) / float32(w) * width
		ui.Image(c, bmp).Size(width, height).Translate(p.panX, p.panY).Radius(radiusChip)
	})
	if p.focused {
		ui.Text(c, i18n.T("preview.media.focus_hint")).Absolute().Bottom(space4).Left(space6).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
	}
}

func (a *AppController) requestOriginalBitmap(path string, p *mediaPreviewState) *ui.Bitmap {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	bmp, ok := a.cachedOriginalBitmap(path)
	if ok {
		p.loading = false
		p.failed = bmp == nil
		return bmp
	}
	p.loading = true
	a.scheduleOriginalBitmap(path)
	return nil
}
