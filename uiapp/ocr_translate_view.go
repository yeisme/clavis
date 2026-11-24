package uiapp

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const ocrMaxDimension = 2048

// TranslateAuto resets any manual direction override and delegates to script detection.
const TranslateAuto engine.TranslateDirection = ""

type ocrTranslateControllerState struct {
	mu             sync.Mutex
	translateDir   engine.TranslateDirection
	lastSourceHash [32]byte
	hasLastSource  bool
	pendingImgs    []ai.ImageAttachment
	lastOCRImg     *ai.ImageAttachment
}

var ocrTranslateStore = struct {
	mu    sync.Mutex
	byCtl map[*AppController]*ocrTranslateControllerState
}{
	byCtl: make(map[*AppController]*ocrTranslateControllerState),
}

func ocrTranslateStateFor(c *AppController) *ocrTranslateControllerState {
	ocrTranslateStore.mu.Lock()
	defer ocrTranslateStore.mu.Unlock()
	st := ocrTranslateStore.byCtl[c]
	if st == nil {
		st = &ocrTranslateControllerState{}
		ocrTranslateStore.byCtl[c] = st
	}
	return st
}

func (c *AppController) setTranslateDirection(dir engine.TranslateDirection) {
	st := ocrTranslateStateFor(c)
	st.mu.Lock()
	st.translateDir = dir
	st.mu.Unlock()
}

func (c *AppController) setTranslateDirectionFor(text string, dir engine.TranslateDirection) {
	h := sha256.Sum256([]byte(text))
	st := ocrTranslateStateFor(c)
	st.mu.Lock()
	st.translateDir = dir
	st.lastSourceHash = h
	st.hasLastSource = true
	st.mu.Unlock()
}

func (c *AppController) translateDirectionFor(text string) engine.TranslateDirection {
	h := sha256.Sum256([]byte(text))
	st := ocrTranslateStateFor(c)
	st.mu.Lock()
	if st.hasLastSource && st.lastSourceHash != h {
		st.translateDir = TranslateAuto
	}
	st.lastSourceHash = h
	st.hasLastSource = true
	dir := st.translateDir
	st.mu.Unlock()
	if dir == TranslateAuto {
		return engine.DetectTranslateDirection(text)
	}
	return dir
}

func (c *AppController) stageOCRImages(img ai.ImageAttachment) {
	st := ocrTranslateStateFor(c)
	st.mu.Lock()
	copyImg := ai.ImageAttachment{
		MIMEType: img.MIMEType,
		Data:     append([]byte(nil), img.Data...),
		Detail:   img.Detail,
	}
	st.lastOCRImg = &copyImg
	st.pendingImgs = []ai.ImageAttachment{copyImg}
	st.mu.Unlock()
}

func (c *AppController) consumeOCRImages() []ai.ImageAttachment {
	st := ocrTranslateStateFor(c)
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.pendingImgs) > 0 {
		out := append([]ai.ImageAttachment(nil), st.pendingImgs...)
		st.pendingImgs = nil
		return out
	}
	if st.lastOCRImg != nil {
		return []ai.ImageAttachment{*st.lastOCRImg}
	}
	return nil
}

// PrepareOCRImage validates a raster image, scales images larger than 2048px
// on the longest side using pure-Go bilinear interpolation, and encodes to PNG
// when resized or exceeding ai.MaxImageAttachmentBytes.
func PrepareOCRImage(raw []byte, mimeType string) (ai.ImageAttachment, error) {
	if len(raw) == 0 {
		return ai.ImageAttachment{}, fmt.Errorf("empty image payload")
	}
	normMIME, ok := ai.NormalizeImageMIMEType(mimeType)
	srcImg, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return ai.ImageAttachment{}, fmt.Errorf("decode image: %w", err)
	}
	if !ok {
		switch format {
		case "png":
			normMIME = "image/png"
		case "jpeg":
			normMIME = "image/jpeg"
		case "gif":
			normMIME = "image/gif"
		case "webp":
			normMIME = "image/webp"
		default:
			normMIME = "image/png"
		}
	}
	bounds := srcImg.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= ocrMaxDimension && h <= ocrMaxDimension && len(raw) <= ai.MaxImageAttachmentBytes && normMIME != "" {
		return ai.ImageAttachment{
			MIMEType: normMIME,
			Data:     append([]byte(nil), raw...),
			Detail:   "high",
		}, nil
	}
	newW, newH := w, h
	if w >= h && w > ocrMaxDimension {
		newW = ocrMaxDimension
		newH = max(1, h*ocrMaxDimension/w)
	} else if h > w && h > ocrMaxDimension {
		newH = ocrMaxDimension
		newW = max(1, w*ocrMaxDimension/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.BiLinear.Scale(dst, dst.Bounds(), srcImg, bounds, draw.Over, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return ai.ImageAttachment{}, fmt.Errorf("encode scaled png: %w", err)
	}
	if buf.Len() > ai.MaxImageAttachmentBytes {
		return ai.ImageAttachment{}, fmt.Errorf("scaled PNG still exceeds %d bytes", ai.MaxImageAttachmentBytes)
	}
	return ai.ImageAttachment{
		MIMEType: "image/png",
		Data:     buf.Bytes(),
		Detail:   "high",
	}, nil
}

// StartImageOCR validates and prepares imgBytes and streams AI Vision OCR.
func (c *AppController) StartImageOCR(imgBytes []byte, mimeType, prompt string) error {
	if c == nil {
		return fmt.Errorf("controller is nil")
	}
	att, err := PrepareOCRImage(imgBytes, mimeType)
	if err != nil {
		return err
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultOCRPrompt(false)
	}
	c.stageOCRImages(att)
	cfg := c.Config()
	visProf, visErr := ai.ResolveVisionProfile(cfg.Main.AI)
	profID := ""
	if visErr == nil {
		profID = visProf.ID
	}
	effects := c.Dispatch(engine.Event{
		Type:      engine.EventStartAI,
		Mode:      "ocr",
		Prompt:    prompt,
		ProfileID: profID,
	})
	presented := false
	for _, eff := range effects {
		if eff.Type == engine.EffectPresentCapsule {
			presented = true
			break
		}
	}
	if !presented {
		c.handlePresentCapsule(engine.PhaseAIStreaming)
	}
	return nil
}

// stageClipboardMediaForOCR reads a clipboard image file, prepares/downscales it
// for Vision inference, and stages it on this controller without emitting events.
func (c *AppController) stageClipboardMediaForOCR(m engine.ClipboardMedia) error {
	if c == nil || !m.IsMedia() || strings.TrimSpace(m.Path) == "" {
		return fmt.Errorf("no image path available")
	}
	data, err := os.ReadFile(m.Path)
	if err != nil {
		return err
	}
	mimeType := strings.TrimSpace(m.Mime)
	if mimeType == "" {
		switch strings.ToLower(filepath.Ext(m.Path)) {
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		case ".webp":
			mimeType = "image/webp"
		case ".gif":
			mimeType = "image/gif"
		default:
			mimeType = "image/png"
		}
	}
	att, err := PrepareOCRImage(data, mimeType)
	if err != nil {
		return err
	}
	c.stageOCRImages(att)
	return nil
}

// StartClipboardImageOCR reads a stored clipboard image and starts AI Vision OCR.
func (c *AppController) StartClipboardImageOCR(m engine.ClipboardMedia) error {
	if c == nil || !m.IsMedia() || strings.TrimSpace(m.Path) == "" {
		return fmt.Errorf("no image path available")
	}
	if err := c.stageClipboardMediaForOCR(m); err != nil {
		c.ShowToastOrHUD(err.Error())
		return err
	}
	cfg := c.Config()
	visProf, visErr := ai.ResolveVisionProfile(cfg.Main.AI)
	profID := ""
	if visErr == nil {
		profID = visProf.ID
	}
	c.Dispatch(engine.Event{
		Type:      engine.EventStartAI,
		Mode:      "ocr",
		Prompt:    defaultOCRPrompt(false),
		ProfileID: profID,
	})
	return nil
}

// StartTranslation enters bilingual AI translation mode for text with optional direction ("zh->en", "en->zh", or "").
func (c *AppController) StartTranslation(text, direction string) {
	if c == nil {
		return
	}
	dir := engine.TranslateDirection(strings.TrimSpace(direction))
	c.setTranslateDirectionFor(text, dir)
	if dir == TranslateAuto {
		dir = engine.DetectTranslateDirection(text)
	}
	_, instr := engine.BuildTranslatePrompt(text, dir)
	c.Dispatch(engine.Event{
		Type:       engine.EventStartAI,
		Mode:       "translate",
		Prompt:     instr,
		SourceText: text,
	})
}

func defaultOCRPrompt(tableMode bool) string {
	if tableMode {
		return "Extract all text and convert tables or structured lists from this image into clean Markdown tables. Return Markdown only."
	}
	return "Extract all readable text, code blocks, and headings from this image accurately, preserving line breaks and formatting. Return the extracted Markdown only."
}

func (a *AppController) renderOCRTranslateBar(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, state engine.State) {
	if state.AIMode != "translate" && state.AIMode != "ocr" {
		return
	}
	zh := i18n.IsChinese()

	if state.AIMode == "translate" {
		curDir := a.translateDirectionFor(state.AISourceText)
		ui.Row(c).Key("ai-translate-bar").FillWidth().Padding(space4, space8).Gap(space6).AlignItems(ui.Center).
			Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
			renderSemanticIcon(c, "character.book.closed", typeMicro, secondaryInk(theme))
			ui.Text(c, pickLang(zh, "双语翻译", "Translate")).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()

			dirBtn := func(key, label string, dir engine.TranslateDirection) {
				active := curDir == dir
				b := ui.ButtonBase(c).Key(key).Label(label).Padding(space2, space8).Radius(radius4).FocusRing(false)
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
					ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
				})
				if b.Clicked() {
					a.StartTranslation(state.AISourceText, string(dir))
				}
			}
			dirBtn("ai-tr-zhtoEn", pickLang(zh, "中 → 英", "ZH → EN"), engine.TranslateZhToEn)
			dirBtn("ai-tr-entoZh", pickLang(zh, "英 → 中", "EN → ZH"), engine.TranslateEnToZh)
			ui.Spacer(c)
		})
		return
	}

	// state.AIMode == "ocr"
	visProf, visErr := ai.ResolveVisionProfile(cfg.Main.AI)
	hasVision := visErr == nil && visProf.Capabilities.Vision
	ui.Row(c).Key("ai-ocr-bar").FillWidth().Padding(space4, space8).Gap(space6).AlignItems(ui.Center).
		Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
		renderSemanticIcon(c, "sparkles", typeMicro, secondaryInk(theme))
		if hasVision {
			ui.Text(c, "OCR · "+visProf.Name).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		} else {
			ui.Text(c, pickLang(zh, "未配置 Vision 模型", "No Vision profile")).FontSize(typeMicro).FontWeight(600).TextColor(kitStateWarningColor(theme)).SingleLine()
		}
		ui.Spacer(c)
		ocrBtn := func(key, label string, onClick func()) {
			b := ui.ButtonBase(c).Key(key).Label(label).Padding(space2, space8).Radius(radius4).
				Background(theme.Surface).Border(space1, hairline(theme)).FocusRing(false)
			b.Children(func() {
				ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			})
			if b.Clicked() {
				onClick()
			}
		}
		if hasVision {
			ocrBtn("ai-ocr-text", pickLang(zh, "纯文本", "Plain Text"), func() {
				a.Dispatch(engine.Event{Type: engine.EventStartAI, Mode: "ocr", Prompt: defaultOCRPrompt(false), ProfileID: visProf.ID})
			})
			ocrBtn("ai-ocr-table", pickLang(zh, "Markdown 表格", "MD Table"), func() {
				a.Dispatch(engine.Event{Type: engine.EventStartAI, Mode: "ocr", Prompt: defaultOCRPrompt(true), ProfileID: visProf.ID})
			})
		} else {
			ocrBtn("ai-ocr-settings", pickLang(zh, "配置 Vision 模型", "Configure Vision"), func() {
				a.OpenSettings(SettingsTabAI)
			})
		}
	})
}
