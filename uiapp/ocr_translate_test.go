package uiapp

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
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
