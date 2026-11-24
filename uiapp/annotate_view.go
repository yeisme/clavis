package uiapp

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/annotate"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var (
	annotateOTFOnce sync.Once
	annotateOTFFont *opentype.Font
)

func init() {
	annotate.TextRenderer = renderAnnotateOTFText
}

func renderAnnotateOTFText(dst *image.RGBA, pos annotate.Point, txt string, fontSize float64, c annotate.ColorRGBA) {
	txt = strings.TrimSpace(txt)
	if txt == "" || dst == nil {
		return
	}
	if fontSize <= 0 {
		fontSize = 20
	}
	annotateOTFOnce.Do(func() {
		if f, err := opentype.Parse(goregular.TTF); err == nil {
			annotateOTFFont = f
		}
	})
	if annotateOTFFont == nil {
		return
	}
	face, err := opentype.NewFace(annotateOTFFont, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return
	}
	defer face.Close()

	x := int(math.Round(pos.X))
	y := int(math.Round(pos.Y + fontSize))
	shadow := font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.RGBA{R: 0, G: 0, B: 0, A: 140}),
		Face: face,
		Dot:  fixed.P(x+1, y+1),
	}
	shadow.DrawString(txt)

	drawer := font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	drawer.DrawString(txt)
}

type annotateControllerState struct {
	mu          sync.Mutex
	open        bool
	doc         *annotate.Document
	tool        annotate.ToolKind
	colorIdx    int
	strokeWidth float64
	dragging    bool
	dragStart   annotate.Point
	dragCur     annotate.Point
	textEditing bool
	textAnchor  annotate.Point
	textDraft   string
	canvasW     float32
	canvasH     float32
	canvasHdl   ui.Handle
	bmpVersion  uint64
	cachedBmp   *ui.Bitmap
	statusMsg   string
}

var annotateStateStore = struct {
	mu    sync.Mutex
	byCtl map[*AppController]*annotateControllerState
}{
	byCtl: make(map[*AppController]*annotateControllerState),
}

func annotateStateFor(c *AppController) *annotateControllerState {
	annotateStateStore.mu.Lock()
	defer annotateStateStore.mu.Unlock()
	st := annotateStateStore.byCtl[c]
	if st == nil {
		st = &annotateControllerState{
			tool:        annotate.ToolRect,
			strokeWidth: 4,
		}
		annotateStateStore.byCtl[c] = st
	}
	return st
}

// AnnotateOpen reports whether an annotation document session is currently active.
func (c *AppController) AnnotateOpen() bool {
	if c == nil {
		return false
	}
	st := annotateStateFor(c)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.open && st.doc != nil
}

// CaptureAndAnnotate captures an interactive region and opens the Retina vector annotator.
func (c *AppController) CaptureAndAnnotate() {
	if c == nil || c.capturer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res, err := c.capturer.CaptureInteractive(ctx)
	if err != nil {
		c.ShowToastOrHUD(err.Error())
		return
	}
	if res.Cancelled || len(res.PNG) == 0 {
		return
	}
	if err := c.OpenAnnotateImage(res.PNG); err != nil {
		c.ShowToastOrHUD(err.Error())
	}
}

// CaptureAndOCR captures an interactive region and sends it straight to AI Vision OCR.
func (c *AppController) CaptureAndOCR() {
	if c == nil || c.capturer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res, err := c.capturer.CaptureInteractive(ctx)
	if err != nil {
		c.ShowToastOrHUD(err.Error())
		return
	}
	if res.Cancelled || len(res.PNG) == 0 {
		return
	}
	if err := c.StartImageOCR(res.PNG, "image/png", ""); err != nil {
		c.ShowToastOrHUD(err.Error())
		return
	}
	c.mu.Lock()
	present := c.OnPresentPhase
	c.mu.Unlock()
	if present != nil {
		present(engine.PhaseAIStreaming)
	}
}

// OpenAnnotateMedia opens an existing Clipboard media image file in the vector annotator.
func (c *AppController) OpenAnnotateMedia(m engine.ClipboardMedia) error {
	if !m.IsMedia() || strings.TrimSpace(m.Path) == "" {
		return fmt.Errorf("annotate: no media path")
	}
	data, err := os.ReadFile(m.Path)
	if err != nil {
		return err
	}
	return c.OpenAnnotateImage(data)
}

// OpenAnnotateImage loads pngBytes into the standalone annotation editor and shows its window.
func (c *AppController) OpenAnnotateImage(pngBytes []byte) error {
	doc, err := annotate.NewDocumentFromPNG(pngBytes)
	if err != nil {
		return err
	}
	bmp, _ := ui.DecodeBitmap(pngBytes)

	st := annotateStateFor(c)
	st.mu.Lock()
	st.doc = doc
	st.open = true
	st.dragging = false
	st.textEditing = false
	st.textDraft = ""
	st.statusMsg = ""
	st.bmpVersion = doc.Version
	st.cachedBmp = bmp
	if st.strokeWidth <= 0 {
		st.strokeWidth = 4
	}
	st.mu.Unlock()

	c.mu.Lock()
	onOpen := c.OnShowAnnotateWindow
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onOpen != nil {
		onOpen()
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// CloseAnnotateWindow closes the screenshot annotation session.
func (c *AppController) CloseAnnotateWindow() {
	if c == nil {
		return
	}
	st := annotateStateFor(c)
	st.mu.Lock()
	st.open = false
	st.dragging = false
	st.textEditing = false
	st.textDraft = ""
	st.mu.Unlock()

	c.mu.Lock()
	onClose := c.OnCloseAnnotateWindow
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	if onRepaint != nil {
		onRepaint()
	}
}

func (c *AppController) refreshAnnotateBitmapLocked(st *annotateControllerState) {
	if st.doc == nil {
		return
	}
	_, _, _, ver := st.doc.Snapshot()
	if st.cachedBmp != nil && st.bmpVersion == ver {
		return
	}
	outPNG, _, err := annotate.Flatten(st.doc)
	if err != nil {
		return
	}
	if bmp, err := ui.DecodeBitmap(outPNG); err == nil {
		st.cachedBmp = bmp
		st.bmpVersion = ver
	}
}

func (c *AppController) SetAnnotateTool(tool annotate.ToolKind) {
	st := annotateStateFor(c)
	st.mu.Lock()
	st.tool = tool
	if tool != annotate.ToolText {
		st.textEditing = false
	}
	st.mu.Unlock()
}

func (c *AppController) SetAnnotateColorIndex(idx int) {
	pal := annotate.DefaultPalette()
	if idx < 0 || idx >= len(pal) {
		return
	}
	st := annotateStateFor(c)
	st.mu.Lock()
	st.colorIdx = idx
	st.mu.Unlock()
}

func (c *AppController) SetAnnotateStrokeWidth(w float64) {
	if w <= 0 {
		return
	}
	st := annotateStateFor(c)
	st.mu.Lock()
	st.strokeWidth = w
	st.mu.Unlock()
}

func (c *AppController) AddAnnotateShape(s annotate.Shape) {
	st := annotateStateFor(c)
	st.mu.Lock()
	if st.doc != nil {
		st.doc.AddShape(s)
		c.refreshAnnotateBitmapLocked(st)
	}
	st.mu.Unlock()
}

func (c *AppController) AnnotateUndo() bool {
	st := annotateStateFor(c)
	st.mu.Lock()
	ok := st.doc != nil && st.doc.Undo()
	if ok {
		c.refreshAnnotateBitmapLocked(st)
	}
	st.mu.Unlock()
	return ok
}

func (c *AppController) AnnotateRedo() bool {
	st := annotateStateFor(c)
	st.mu.Lock()
	ok := st.doc != nil && st.doc.Redo()
	if ok {
		c.refreshAnnotateBitmapLocked(st)
	}
	st.mu.Unlock()
	return ok
}

func (c *AppController) FlattenAnnotatePNG() ([]byte, error) {
	st := annotateStateFor(c)
	st.mu.Lock()
	doc := st.doc
	st.mu.Unlock()
	if doc == nil {
		return nil, fmt.Errorf("no active annotation document")
	}
	outPNG, _, err := annotate.Flatten(doc)
	return outPNG, err
}

func (c *AppController) recordPNGToMediaAndClipboard(pngBytes []byte) error {
	cfgDir := ""
	if c.ConfigStore != nil {
		cfgDir = c.ConfigStore.Directory()
	}
	if cfgDir != "" && c.ClipboardStore != nil {
		ms := clipboard.NewMediaStore(filepath.Join(cfgDir, "media"))
		if media, err := ms.SaveImage(pngBytes); err == nil {
			_, _, _ = c.ClipboardStore.Record(context.Background(), clipboard.CaptureCandidate{
				Content:       fmt.Sprintf("Screenshot %dx%d", media.Width, media.Height),
				SourceAppName: "Clavis Annotate",
				Timestamp:     time.Now(),
				Media:         media,
			})
			c.syncClipboardEntries(context.Background())
		}
	}
	if c.AnnotateCopyWriter != nil {
		return c.AnnotateCopyWriter(pngBytes)
	}
	if mygo.App.IsReady() {
		return mygo.Clipboard.Write(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, pngBytes))))
	}
	return nil
}

// CopyAnnotatePNG flattens the Retina screenshot, writes PNG bytes to clipboard + MediaStore, and closes.
func (c *AppController) CopyAnnotatePNG() error {
	outPNG, err := c.FlattenAnnotatePNG()
	if err != nil {
		return err
	}
	if err := c.recordPNGToMediaAndClipboard(outPNG); err != nil {
		return err
	}
	c.CloseAnnotateWindow()
	c.ShowToastOrHUD(pickLang(i18n.IsChinese(), "已复制标注截图", "Annotated screenshot copied"))
	return nil
}

// SaveAnnotatePNG saves the flattened PNG to ~/Pictures/Clavis (or SaveCapturePath hook) and closes.
func (c *AppController) SaveAnnotatePNG() (string, error) {
	outPNG, err := c.FlattenAnnotatePNG()
	if err != nil {
		return "", err
	}
	var savedPath string
	if c.SaveCapturePath != nil {
		savedPath, err = c.SaveCapturePath(outPNG)
		if err != nil {
			return "", err
		}
	} else {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, "Pictures", "Clavis")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		savedPath = filepath.Join(dir, "clavis-"+time.Now().Format("20060102-150405")+".png")
		if err := os.WriteFile(savedPath, outPNG, 0o644); err != nil {
			return "", err
		}
	}
	c.CloseAnnotateWindow()
	c.ShowToastOrHUD(savedPath)
	return savedPath, nil
}

// SendAnnotateToAI flattens the annotated screenshot and routes it to AI Vision (OCR or Ask).
func (c *AppController) SendAnnotateToAI(prompt string) error {
	outPNG, err := c.FlattenAnnotatePNG()
	if err != nil {
		return err
	}
	c.CloseAnnotateWindow()
	if err := c.StartImageOCR(outPNG, "image/png", prompt); err != nil {
		c.ShowToastOrHUD(err.Error())
		return err
	}
	c.mu.Lock()
	present := c.OnPresentPhase
	c.mu.Unlock()
	if present != nil {
		present(engine.PhaseAIStreaming)
	}
	return nil
}

// AnnotateView renders the streamlined vector screenshot annotation window.
func (a *AppController) AnnotateView(c *ui.Context) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)
	zh := i18n.IsChinese()

	st := annotateStateFor(a)
	st.mu.Lock()
	a.refreshAnnotateBitmapLocked(st)
	doc := st.doc
	curTool := st.tool
	curColorIdx := st.colorIdx
	curStroke := st.strokeWidth
	textEditing := st.textEditing
	textAnchor := st.textAnchor
	textDraft := st.textDraft
	bmp := st.cachedBmp
	dragging := st.dragging
	dragStart := st.dragStart
	dragCur := st.dragCur
	statusMsg := st.statusMsg
	st.mu.Unlock()

	pal := annotate.DefaultPalette()
	if curColorIdx < 0 || curColorIdx >= len(pal) {
		curColorIdx = 0
	}
	curColorRGBA := pal[curColorIdx].Color

	var canUndo, canRedo bool
	var docW, docH int
	if doc != nil {
		_, canUndo, canRedo, _ = doc.Snapshot()
		docW, docH = doc.Width, doc.Height
	}

	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		a.CloseAnnotateWindow()
	})
	c.OnWindowShortcut(primaryMod(), ui.KeyZ, func() {
		a.AnnotateUndo()
	})

	applyTextLocale(ui.Column(c).Fill().Background(theme.Background).Padding(space10, space12).Gap(space8)).Children(func() {
		// Top Toolbar: 4 tools, color swatches, stroke sizes, Undo/Redo.
		kitCard(c, theme, cardInset, func() {
			ui.Row(c).FillWidth().Padding(space4, space8).Gap(space6).AlignItems(ui.Center).Children(func() {
				toolBtn := func(key, label string, tool annotate.ToolKind) {
					active := curTool == tool
					b := ui.ButtonBase(c).Key(key).Label(label).Padding(space2, space8).Radius(radiusChip).FocusRing(false)
					if active {
						b.Background(theme.Accent.Alpha(alphaSubtle)).Border(space1, theme.Accent.Alpha(alphaHalf))
					} else {
						b.Background(theme.Surface).Border(space1, hairline(theme))
					}
					fg := secondaryInk(theme)
					if active {
						fg = accentInk(theme, surfaceBase(theme))
					}
					b.Children(func() {
						ui.Text(c, label).FontSize(typeMeta).FontWeight(600).TextColor(fg).SingleLine()
					})
					if b.Clicked() {
						a.SetAnnotateTool(tool)
					}
				}
				toolBtn("ann-tool-rect", pickLang(zh, "矩形", "Rect"), annotate.ToolRect)
				toolBtn("ann-tool-arrow", pickLang(zh, "箭头", "Arrow"), annotate.ToolArrow)
				toolBtn("ann-tool-mosaic", pickLang(zh, "马赛克", "Mosaic"), annotate.ToolMosaic)
				toolBtn("ann-tool-text", pickLang(zh, "文字", "Text"), annotate.ToolText)

				kitDivider(c, theme)

				// 5 color swatches.
				for i, p := range pal {
					idx := i
					swColor := ui.RGBA(p.Color.R, p.Color.G, p.Color.B, float32(p.Color.A)/255)
					b := ui.ButtonBase(c).Key("ann-color-"+p.Name).Width(space18).Height(space18).Radius(radiusPill).Background(swColor).FocusRing(false)
					if curColorIdx == idx {
						b.Border(space2, theme.Text)
					} else {
						b.Border(space1, hairline(theme))
					}
					if b.Clicked() {
						a.SetAnnotateColorIndex(idx)
					}
				}

				// Stroke width S / M / L.
				for _, sw := range []struct {
					label string
					val   float64
				}{
					{"S", 2},
					{"M", 4},
					{"L", 7},
				} {
					s := sw
					active := math.Abs(curStroke-s.val) < 0.5
					b := ui.ButtonBase(c).Key("ann-stroke-"+s.label).Label(s.label).Padding(space2, space6).Radius(radius4).FocusRing(false)
					if active {
						b.Background(theme.Accent.Alpha(alphaSubtle)).Border(space1, theme.Accent.Alpha(alphaHalf))
					} else {
						b.Background(theme.Surface).Border(space1, hairline(theme))
					}
					fg := secondaryInk(theme)
					if active {
						fg = accentInk(theme, surfaceBase(theme))
					}
					b.Children(func() {
						ui.Text(c, s.label).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
					})
					if b.Clicked() {
						a.SetAnnotateStrokeWidth(s.val)
					}
				}

				ui.Spacer(c)

				if canUndo {
					if ui.Button(c, pickLang(zh, "撤销", "Undo")).Key("ann-undo").Padding(space2, space8).Clicked() {
						a.AnnotateUndo()
					}
				}
				if canRedo {
					if ui.Button(c, pickLang(zh, "重做", "Redo")).Key("ann-redo").Padding(space2, space8).Clicked() {
						a.AnnotateRedo()
					}
				}
			})
		})

		if textEditing {
			kitCard(c, theme, cardInset, func() {
				ui.Row(c).FillWidth().Padding(space4, space8).Gap(space6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, pickLang(zh, "添加标注文字：", "Text:")).FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
					tin := ui.TextInput(c.Key("ann-text-input"), &textDraft).
						Placeholder(pickLang(zh, "输入文字并点击确定", "Type annotation text")).Grow(1)
					if tin.Changed() {
						st.mu.Lock()
						st.textDraft = textDraft
						st.mu.Unlock()
					}
					commitLbl := pickLang(zh, "确定", "Add")
					if ui.Button(c, commitLbl).Key("ann-text-commit").Label(commitLbl).Padding(space2, space8).Clicked() {
						if strings.TrimSpace(textDraft) != "" {
							a.AddAnnotateShape(annotate.Shape{
								Tool:     annotate.ToolText,
								P1:       textAnchor,
								Color:    curColorRGBA,
								FontSize: math.Max(16, curStroke*5),
								Text:     textDraft,
							})
						}
						st.mu.Lock()
						st.textEditing = false
						st.textDraft = ""
						st.mu.Unlock()
					}
				})
			})
		}

		// Interactive Retina Annotation Canvas.
		canvas := ui.Box(c).Key("ann-canvas").FillWidth().Grow(1).MinHeight(space0).
			Radius(radius8).Background(theme.Surface.Mix(theme.Text, alphaFaint)).Border(space1, hairline(theme)).
			Clip().Focusable().Bind(&st.canvasHdl).FocusRing(false)

		canvas.HandleInput(func(ev ui.InputEvent) bool {
			if docW <= 0 || docH <= 0 {
				return false
			}
			st.mu.Lock()
			cw, ch := st.canvasW, st.canvasH
			st.mu.Unlock()
			fit := computeAnnotateFitRect(cw, ch, docW, docH)
			mapPt := func(lx, ly float32) annotate.Point {
				if fit.W <= 0 || fit.H <= 0 {
					return annotate.Point{}
				}
				px := (float64(lx-fit.X) / float64(fit.W)) * float64(docW)
				py := (float64(ly-fit.Y) / float64(fit.H)) * float64(docH)
				px = math.Max(0, math.Min(float64(docW), px))
				py = math.Max(0, math.Min(float64(docH), py))
				return annotate.Point{X: px, Y: py}
			}

			switch ev.Kind {
			case ui.InputPointerDown:
				if ev.Button != 0 {
					return false
				}
				st.canvasHdl.RequestFocus(c)
				pt := mapPt(ev.X, ev.Y)
				st.mu.Lock()
				if st.tool == annotate.ToolText {
					st.textAnchor = pt
					st.textEditing = true
					st.dragging = false
				} else {
					st.dragStart = pt
					st.dragCur = pt
					st.dragging = true
				}
				st.mu.Unlock()
				return true

			case ui.InputPointerMove:
				st.mu.Lock()
				if st.dragging {
					st.dragCur = mapPt(ev.X, ev.Y)
					st.mu.Unlock()
					return true
				}
				st.mu.Unlock()

			case ui.InputPointerUp:
				st.mu.Lock()
				wasDragging := st.dragging
				startPt := st.dragStart
				endPt := mapPt(ev.X, ev.Y)
				tool := st.tool
				col := pal[st.colorIdx%len(pal)].Color
				sw := st.strokeWidth
				st.dragging = false
				st.mu.Unlock()
				if wasDragging && !ev.Canceled && math.Hypot(endPt.X-startPt.X, endPt.Y-startPt.Y) >= 4 {
					a.AddAnnotateShape(annotate.Shape{
						Tool:        tool,
						P1:          startPt,
						P2:          endPt,
						Color:       col,
						StrokeWidth: sw,
						MosaicBlock: 14,
					})
				}
				return wasDragging
			}
			return false
		})

		canvas.Draw(func(p *ui.Painter, r ui.Rect) {
			st.mu.Lock()
			st.canvasW, st.canvasH = r.W, r.H
			st.mu.Unlock()
			if docW <= 0 || docH <= 0 || bmp == nil {
				return
			}
			fit := computeAnnotateFitRect(r.W, r.H, docW, docH)
			dst := ui.Rect{X: r.X + fit.X, Y: r.Y + fit.Y, W: fit.W, H: fit.H}
			p.Image(bmp, dst, ui.FillBox)

			// 60fps live vector preview while dragging before committing onto Bitmap.
			if dragging && fit.W > 0 && fit.H > 0 {
				sx := fit.W / float32(docW)
				sy := fit.H / float32(docH)
				x0 := dst.X + float32(dragStart.X)*sx
				y0 := dst.Y + float32(dragStart.Y)*sy
				x1 := dst.X + float32(dragCur.X)*sx
				y1 := dst.Y + float32(dragCur.Y)*sy
				col := ui.RGBA(curColorRGBA.R, curColorRGBA.G, curColorRGBA.B, float32(curColorRGBA.A)/255)
				rx, ry := min(x0, x1), min(y0, y1)
				rw, rh := float32(math.Abs(float64(x1-x0))), float32(math.Abs(float64(y1-y0)))
				switch curTool {
				case annotate.ToolRect:
					p.Stroke(ui.Rect{X: rx, Y: ry, W: rw, H: rh}, col, 0, float32(curStroke)*sx)
				case annotate.ToolMosaic:
					p.Fill(ui.Rect{X: rx, Y: ry, W: rw, H: rh}, theme.Text.Alpha(alphaSubtle), 0)
					p.StrokeDashed(ui.Rect{X: rx, Y: ry, W: rw, H: rh}, theme.TextMuted, 0, space1)
				case annotate.ToolArrow:
					p.Line(x0, y0, x1, y1, float32(curStroke)*sx, col)
				}
			}
		})

		if doc == nil {
			canvas.Children(func() {
				kitStateView(c, theme, kitStateEmpty, "photo",
					pickLang(zh, "未载入截图", "No Screenshot Loaded"),
					pickLang(zh, "从动作面板运行「截图并标注」或从剪贴板图片预览点击「标注」。", "Trigger Capture & Annotate or click Annotate on a clipboard image."),
					nil)
			})
		}

		if statusMsg != "" {
			ui.Text(c, statusMsg).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}

		// Bottom Action Bar: Copy PNG, Save PNG, AI OCR, Ask AI, Cancel.
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			if docW > 0 && docH > 0 {
				ui.Text(c, fmt.Sprintf("%dx%d px", docW, docH)).
					FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			}
			ui.Spacer(c)

			cancelLbl := pickLang(zh, "取消", "Cancel")
			if ui.Button(c, cancelLbl).Key("ann-cancel").Label(cancelLbl).Padding(space2, space10).Clicked() {
				a.CloseAnnotateWindow()
			}
			if doc != nil {
				askLbl := "Ask AI"
				if ui.Button(c, askLbl).Key("ann-ask-ai").Label(askLbl).Padding(space2, space10).Clicked() {
					_ = a.SendAnnotateToAI("Inspect this annotated screenshot carefully and explain or diagnose what it shows.")
				}
				ocrLbl := "AI OCR"
				if ui.Button(c, ocrLbl).Key("ann-ai-ocr").Label(ocrLbl).Padding(space2, space10).Clicked() {
					_ = a.SendAnnotateToAI("")
				}
				saveLbl := pickLang(zh, "保存 PNG", "Save PNG")
				if ui.Button(c, saveLbl).Key("ann-save").Label(saveLbl).Padding(space2, space10).Clicked() {
					if _, err := a.SaveAnnotatePNG(); err != nil {
						st.mu.Lock()
						st.statusMsg = err.Error()
						st.mu.Unlock()
					}
				}
				copyLbl := pickLang(zh, "复制", "Copy")
				if ui.PrimaryButton(c, copyLbl).Key("ann-copy").Label(copyLbl).Padding(space2, space12).Clicked() {
					if err := a.CopyAnnotatePNG(); err != nil {
						st.mu.Lock()
						st.statusMsg = err.Error()
						st.mu.Unlock()
					}
				}
			}
		})
	})
}

func computeAnnotateFitRect(canvasW, canvasH float32, imgW, imgH int) ui.Rect {
	if canvasW <= 0 || canvasH <= 0 || imgW <= 0 || imgH <= 0 {
		return ui.Rect{}
	}
	scale := math.Min(float64(canvasW)/float64(imgW), float64(canvasH)/float64(imgH))
	if scale > 1 {
		scale = 1
	}
	w := float32(float64(imgW) * scale)
	h := float32(float64(imgH) * scale)
	return ui.Rect{
		X: (canvasW - w) / 2,
		Y: (canvasH - h) / 2,
		W: w,
		H: h,
	}
}
