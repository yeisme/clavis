package uiapp

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type sseMockDoer struct {
	mu       sync.Mutex
	requests []string
	body     string
	ctype    string
}

func (s *sseMockDoer) Do(req *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(req.Body)
	s.mu.Lock()
	s.requests = append(s.requests, string(raw))
	s.mu.Unlock()
	ct := s.ctype
	if ct == "" {
		ct = "text/event-stream"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{ct}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func makeSolidPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 40, G: 120, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestPrepareOCRImageDownscalesOversizedImage(t *testing.T) {
	bigPNG := makeSolidPNG(2600, 1300)
	att, err := PrepareOCRImage(bigPNG, "image/png")
	if err != nil {
		t.Fatalf("PrepareOCRImage: %v", err)
	}
	if att.MIMEType != "image/png" {
		t.Fatalf("MIMEType = %q, want image/png", att.MIMEType)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(att.Data))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if cfg.Width != 2048 || cfg.Height != 1024 {
		t.Fatalf("scaled dimensions = %dx%d, want 2048x1024", cfg.Width, cfg.Height)
	}
}

func TestOCRImageStreamWithVisionRoutingAndNoVisionGuidance(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.anthropic", "sk-ant-test")
	_ = sec.SaveKey("clavis.ai.gemini", "gm-test")

	mockHTTP := &sseMockDoer{
		body: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"# OCR Result\\n- Hello\"}]}}]}\n\n",
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     sec,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		HTTPDoer:        mockHTTP,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	// Explicitly pick gemini as VisionProfileID even while active is deepseek.
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.AI.VisionProfileID = "gemini"
	}); err != nil {
		t.Fatal(err)
	}

	pngBytes := makeSolidPNG(120, 80)
	if err := ctrl.StartImageOCR(pngBytes, "image/png", ""); err != nil {
		t.Fatalf("StartImageOCR: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return st.AIComplete && strings.Contains(st.AIOutput, "OCR Result")
	}, "OCR stream completes with markdown output")

	mockHTTP.mu.Lock()
	reqCount := len(mockHTTP.requests)
	lastReq := ""
	if reqCount > 0 {
		lastReq = mockHTTP.requests[reqCount-1]
	}
	mockHTTP.mu.Unlock()
	if reqCount == 0 || !strings.Contains(lastReq, `"inline_data"`) {
		t.Fatalf("expected Gemini multimodal inline_data payload, got %q", lastReq)
	}

	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("OCR · Gemini") {
		t.Fatalf("expected OCR Vision bar, got %v", tt.Texts())
	}

	// Disable Vision on all profiles -> clear error + Settings navigation CTA.
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.AI.VisionProfileID = ""
		for i := range m.AI.Profiles {
			m.AI.Profiles[i].Capabilities.Vision = false
		}
	}); err != nil {
		t.Fatal(err)
	}
	var openedTab SettingsTab = -1
	ctrl.mu.Lock()
	ctrl.OnOpenSettings = func(tab SettingsTab) { openedTab = tab }
	ctrl.mu.Unlock()

	if err := ctrl.StartImageOCR(pngBytes, "image/png", ""); err != nil {
		t.Fatalf("StartImageOCR without vision: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(ctrl.State().AIError, "vision")
	}, "vision_unsupported failure message")
	tt.Frame()
	if _, ok := tt.Find("Configure Vision"); ok {
		_ = tt.Click("Configure Vision")
	} else if _, ok := tt.Find("配置 Vision 模型"); ok {
		_ = tt.Click("配置 Vision 模型")
	} else {
		t.Fatalf("missing vision configuration CTA button: %v", tt.Texts())
	}
	tt.Frame()
	if openedTab != SettingsTabAI {
		t.Fatalf("expected SettingsTabAI, got %v", openedTab)
	}
}

func TestTranslationDirectionAndPWUsesSensitiveTTL(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.deepseek", "sk-ds-test")

	mockHTTP := &sseMockDoer{
		body: "data: {\"choices\":[{\"delta\":{\"content\":\"你好，世界\"}}]}\n\ndata: [DONE]\n\n",
	}
	var written []string
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     sec,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		HTTPDoer:        mockHTTP,
		ClipboardWriter: func(s string) { written = append(written, s) },
	})
	t.Cleanup(ctrl.Close)

	ctrl.StartTranslation("Hello, world", "")
	waitUntil(t, 2*time.Second, func() bool {
		return ctrl.State().AIComplete && strings.Contains(ctrl.State().AIOutput, "你好，世界")
	}, "translation stream completes")

	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("ZH → EN") && !tt.HasText("中 → 英") {
		t.Fatalf("expected translation control bar, got %v", tt.Texts())
	}

	// Verify `pw 16` copies via User-Only TTL sensitive path and masks preview.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "pw 16"})
	tt.Frame()
	items := ctrl.State().OmniboxItems
	if len(items) == 0 || !items[0].UtilitySensitive {
		t.Fatalf("expected sensitive pw item, got %+v", items)
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, items[0].UtilityValue) {
			t.Fatalf("plaintext password leaked in UI frame: %q", txt)
		}
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	snap := ctrl.SensitiveClipboardState()
	if !snap.Active || snap.Label == "" {
		t.Fatalf("expected active sensitive TTL snapshot after pw confirm, got %+v", snap)
	}
	_ = ai.MaxImageAttachmentBytes
	_ = written
}

func TestPrepareOCRImageErrorsFormatsAndTallScale(t *testing.T) {
	if _, err := PrepareOCRImage(nil, "image/png"); err == nil {
		t.Fatal("expected error for empty OCR payload")
	}
	if _, err := PrepareOCRImage([]byte("not-a-valid-image"), "image/png"); err == nil {
		t.Fatal("expected error for corrupted image payload")
	}

	// Tall image (h > w, > 2048px) scales down proportionally on height and encodes PNG.
	tallPNG := makeSolidPNG(1000, 4000)
	tallAtt, err := PrepareOCRImage(tallPNG, "image/png")
	if err != nil {
		t.Fatalf("PrepareOCRImage tall PNG: %v", err)
	}
	tallCfg, _, err := image.DecodeConfig(bytes.NewReader(tallAtt.Data))
	if err != nil || tallCfg.Width != 512 || tallCfg.Height != 2048 {
		t.Fatalf("expected 512x2048 scaled PNG, got %dx%d err=%v", tallCfg.Width, tallCfg.Height, err)
	}

	// Unlisted/empty MIME infers decoded format ("image/jpeg") while keeping <=2048 dimensions intact.
	var jpgBuf bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 64, 48))
	if err := jpeg.Encode(&jpgBuf, src, nil); err != nil {
		t.Fatal(err)
	}
	jpgAtt, err := PrepareOCRImage(jpgBuf.Bytes(), "image/x-unknown")
	if err != nil {
		t.Fatalf("PrepareOCRImage jpeg format fallback: %v", err)
	}
	if jpgAtt.MIMEType != "image/jpeg" || jpgAtt.Detail != "high" {
		t.Fatalf("unexpected jpeg attachment metadata: %+v", jpgAtt)
	}
}

func TestClipboardMediaOCRStagingErrorsAndRetry(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.gemini", "gm-test")

	mockHTTP := &sseMockDoer{
		body: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"table-ocr-ok\"}]}}]}\n\n",
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     sec,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		HTTPDoer:        mockHTTP,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.AI.VisionProfileID = "gemini"
	}); err != nil {
		t.Fatal(err)
	}

	// Nil controller & invalid media / missing file paths reject cleanly.
	var nilCtrl *AppController
	if err := nilCtrl.StartImageOCR([]byte{1}, "image/png", ""); err == nil {
		t.Fatal("nil controller StartImageOCR should error")
	}
	if err := ctrl.StartClipboardImageOCR(engine.ClipboardMedia{Kind: "text"}); err == nil {
		t.Fatal("non-media ClipboardMedia should fail StartClipboardImageOCR")
	}
	missingPath := filepath.Join(t.TempDir(), "missing.jpg")
	if err := ctrl.StartClipboardImageOCR(engine.ClipboardMedia{Kind: "image", Path: missingPath}); err == nil {
		t.Fatal("missing image file should fail StartClipboardImageOCR")
	}

	// Real temp JPEG file with empty Mime infers image/jpeg from .jpg extension and triggers Vision OCR.
	var jpgBuf bytes.Buffer
	_ = jpeg.Encode(&jpgBuf, image.NewRGBA(image.Rect(0, 0, 40, 30)), nil)
	jpgPath := filepath.Join(t.TempDir(), "clip-ocr.jpg")
	if err := os.WriteFile(jpgPath, jpgBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.StartClipboardImageOCR(engine.ClipboardMedia{Kind: "image", Path: jpgPath}); err != nil {
		t.Fatalf("StartClipboardImageOCR failed: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return st.AIComplete && strings.Contains(st.AIOutput, "table-ocr-ok")
	}, "clipboard media OCR completed")

	// consumeOCRImages retains lastOCRImg so MD Table re-run works without re-reading file.
	reused := ctrl.consumeOCRImages()
	if len(reused) != 1 || reused[0].MIMEType != "image/jpeg" {
		t.Fatalf("expected retained lastOCRImg for retry, got %+v", reused)
	}
	if !strings.Contains(defaultOCRPrompt(true), "Markdown tables") || strings.Contains(defaultOCRPrompt(false), "Markdown tables") {
		t.Fatal("defaultOCRPrompt tableMode toggle mismatch")
	}
	ctrl.StartTranslation("测试方向重置", string(engine.TranslateEnToZh))
	if got := ctrl.translateDirectionFor("测试方向重置"); got != engine.TranslateEnToZh {
		t.Fatalf("expected explicit override %q, got %q", engine.TranslateEnToZh, got)
	}
}
