package uiapp

import (
	"bytes"
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/capture"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/annotate"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type fakeCapturer struct {
	res capture.Result
	err error
}

func (f fakeCapturer) CaptureInteractive(context.Context) (capture.Result, error) { return f.res, f.err }
func (f fakeCapturer) CaptureFull(context.Context) (capture.Result, error)        { return f.res, f.err }

func TestAnnotateViewVectorToolsCopySaveAndOCR(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.anthropic", "sk-ant-test")

	mockHTTP := &sseMockDoer{
		body: "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Annotated OCR OK\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	rawPNG := makeSolidPNG(160, 100)
	var openedAnnotate, closedAnnotate, presentedAI int
	var copiedPNG []byte
	var savedFile string

	ctrl := NewAppController(Options{
		ConfigStore:           cfgStore,
		SecretStore:           sec,
		ClipboardStore:        clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:         disabledHerdrExecutor{},
		HTTPDoer:              mockHTTP,
		Capturer:              fakeCapturer{res: capture.Result{PNG: rawPNG, Width: 160, Height: 100}},
		OnShowAnnotateWindow:  func() { openedAnnotate++ },
		OnCloseAnnotateWindow: func() { closedAnnotate++ },
		OnPresentPhase: func(p engine.CapsulePhase) {
			if p == engine.PhaseAIStreaming {
				presentedAI++
			}
		},
		AnnotateCopyWriter: func(b []byte) error { copiedPNG = append([]byte(nil), b...); return nil },
		SaveCapturePath: func(b []byte) (string, error) {
			savedFile = filepath.Join(dir, "captured.png")
			return savedFile, os.WriteFile(savedFile, b, 0o644)
		},
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	// Built-in capture action item appears in > prefix search.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: ">截图"})
	items := ctrl.State().OmniboxItems
	if len(items) == 0 || !strings.Contains(items[0].ID, "clavis-capture") {
		t.Fatalf("expected built-in capture action in > prefix search, got %+v", items)
	}

	// CaptureAndAnnotate opens AnnotateView cleanly.
	ctrl.CaptureAndAnnotate()
	if !ctrl.AnnotateOpen() || openedAnnotate != 1 {
		t.Fatalf("expected annotate window open, open=%v opened=%d", ctrl.AnnotateOpen(), openedAnnotate)
	}

	// Render AnnotateView with Tester, draw shapes + Undo/Redo.
	tt := ui.NewTester(ctrl.AnnotateView, 900, 620)
	tt.Frame()
	if !tt.HasText("160x100 px") {
		t.Fatalf("expected image dimension readout in AnnotateView: %v", tt.Texts())
	}

	ctrl.AddAnnotateShape(annotate.Shape{Tool: annotate.ToolRect, P1: annotate.Point{X: 10, Y: 10}, P2: annotate.Point{X: 80, Y: 50}})
	ctrl.AddAnnotateShape(annotate.Shape{Tool: annotate.ToolMosaic, P1: annotate.Point{X: 90, Y: 20}, P2: annotate.Point{X: 140, Y: 70}})
	tt.Frame()
	if _, ok := tt.Find("Undo"); !ok {
		if _, zhOK := tt.Find("撤销"); !zhOK {
			t.Fatalf("expected Undo button visible after adding shape: %v", tt.Texts())
		}
	}

	// Copy PNG -> writes to AnnotateCopyWriter + MediaStore + closes window.
	if err := ctrl.CopyAnnotatePNG(); err != nil {
		t.Fatalf("CopyAnnotatePNG: %v", err)
	}
	if len(copiedPNG) == 0 || closedAnnotate != 1 || ctrl.AnnotateOpen() {
		t.Fatalf("CopyAnnotatePNG did not copy/close properly: bytes=%d closed=%d open=%v", len(copiedPNG), closedAnnotate, ctrl.AnnotateOpen())
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(copiedPNG)); err != nil || cfg.Width != 160 || cfg.Height != 100 {
		t.Fatalf("unexpected flattened PNG dimensions: %+v err=%v", cfg, err)
	}

	// Save PNG -> writes file cleanly.
	_ = ctrl.OpenAnnotateImage(rawPNG)
	path, err := ctrl.SaveAnnotatePNG()
	if err != nil || path != savedFile {
		t.Fatalf("SaveAnnotatePNG path=%q err=%v", path, err)
	}

	// SendAnnotateToAI -> triggers Vision OCR stream.
	_ = ctrl.OpenAnnotateImage(rawPNG)
	if err := ctrl.SendAnnotateToAI(""); err != nil {
		t.Fatalf("SendAnnotateToAI: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return ctrl.State().AIComplete && strings.Contains(ctrl.State().AIOutput, "Annotated OCR OK")
	}, "annotated screenshot routed to OCR")
	if presentedAI == 0 {
		t.Fatal("expected OnPresentPhase(PhaseAIStreaming) after SendAnnotateToAI")
	}
}
