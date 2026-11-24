package uiapp

import (
	"context"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/capture"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/annotate"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestAnnotateCJKTextRendersNonZeroPixels(t *testing.T) {
	dst := image.NewRGBA(image.Rect(0, 0, 320, 80))
	renderAnnotateOTFText(dst, annotate.Point{X: 12, Y: 12}, "中文标注测试 Hello 123", 24, annotate.ColorRGBA{R: 255, G: 60, B: 48, A: 255})

	nonZero := 0
	redPixels := 0
	for y := 0; y < dst.Bounds().Dy(); y++ {
		for x := 0; x < dst.Bounds().Dx(); x++ {
			c := dst.RGBAAt(x, y)
			if c.A > 0 {
				nonZero++
			}
			if c.R > 200 && c.A > 128 {
				redPixels++
			}
		}
	}
	if nonZero < 50 || redPixels < 30 {
		t.Fatalf("expected crisp CJK+ASCII glyph pixels, got nonZero=%d redPixels=%d", nonZero, redPixels)
	}

	// Verify Flatten also routes through renderAnnotateOTFText without panic.
	doc, err := annotate.NewDocumentFromPNG(makeSolidPNG(240, 80))
	if err != nil {
		t.Fatalf("NewDocumentFromPNG: %v", err)
	}
	doc.AddShape(annotate.Shape{
		Tool:     annotate.ToolText,
		P1:       annotate.Point{X: 10, Y: 10},
		Color:    annotate.ColorRGBA{R: 255, G: 255, B: 255, A: 255},
		FontSize: 22,
		Text:     "截图标注",
	})
	outPNG, flatImg, err := annotate.Flatten(doc)
	if err != nil || len(outPNG) == 0 || flatImg == nil {
		t.Fatalf("Flatten with CJK text failed: len=%d err=%v", len(outPNG), err)
	}

	for _, goos := range []string{"darwin", "windows", "linux"} {
		if len(annotateCJKFontCandidates(goos)) == 0 {
			t.Fatalf("expected CJK font candidates for %s", goos)
		}
	}
}

func TestMyGoJSONHasScreenCaptureUsageDescription(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "mygo.json"))
	if err != nil {
		t.Fatalf("read mygo.json: %v", err)
	}
	var doc struct {
		MacOS struct {
			InfoPlist map[string]any `json:"infoPlist"`
		} `json:"macos"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse mygo.json: %v", err)
	}
	val, _ := doc.MacOS.InfoPlist["NSScreenCaptureUsageDescription"].(string)
	if strings.TrimSpace(val) == "" {
		t.Fatal("mygo.json missing macos.infoPlist.NSScreenCaptureUsageDescription")
	}
}

type pasteboardSeqBridge struct {
	*mockBridge
	mu           sync.Mutex
	seq          int64
	ignoredSeqs  []int64
	ignoreBefore bool
	writeDone    bool
}

var _ bridge.HostService = (*pasteboardSeqBridge)(nil)
var _ bridge.ClipboardSequencer = (*pasteboardSeqBridge)(nil)

func (b *pasteboardSeqBridge) ClipboardSequence() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

func (b *pasteboardSeqBridge) IgnoreSelfPasteboardChange(changeCount int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ignoredSeqs = append(b.ignoredSeqs, changeCount)
	if !b.writeDone {
		b.ignoreBefore = true
	}
}

func TestAnnotateCopyUsesClipboardMediaDirAndIgnoresSelfPasteboard(t *testing.T) {
	cfgDir := t.TempDir()
	cfgStore := config.NewConfigStore(cfgDir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	clipStore := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	br := &pasteboardSeqBridge{
		mockBridge: &mockBridge{},
		seq:        41,
	}

	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		ClipboardStore: clipStore,
		Bridge:         br,
		HerdrExecutor:  disabledHerdrExecutor{},
		AnnotateCopyWriter: func(b []byte) error {
			br.mu.Lock()
			br.writeDone = true
			br.seq = 42
			br.mu.Unlock()
			return nil
		},
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	if err := ctrl.OpenAnnotateImage(makeSolidPNG(80, 60)); err != nil {
		t.Fatalf("OpenAnnotateImage: %v", err)
	}
	if err := ctrl.CopyAnnotatePNG(); err != nil {
		t.Fatalf("CopyAnnotatePNG: %v", err)
	}

	// Verify PNG stored in <cfgDir>/clipboard-media (NOT <cfgDir>/media).
	clipMediaDir := filepath.Join(cfgDir, "clipboard-media")
	entries, err := os.ReadDir(clipMediaDir)
	if err != nil {
		t.Fatalf("expected clipboard-media directory at %s: %v", clipMediaDir, err)
	}
	foundPNG := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".png") {
			foundPNG = true
			break
		}
	}
	if !foundPNG {
		t.Fatalf("expected PNG saved under %s, entries=%v", clipMediaDir, entries)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "media")); !os.IsNotExist(err) {
		t.Fatalf("unexpected legacy media dir created at %s", filepath.Join(cfgDir, "media"))
	}

	// Verify bridge ignored self pasteboard change before and after clipboard write.
	br.mu.Lock()
	ignoreBefore := br.ignoreBefore
	ignored := append([]int64(nil), br.ignoredSeqs...)
	br.mu.Unlock()
	if !ignoreBefore {
		t.Fatal("expected IgnoreSelfPasteboardChange to be called before clipboard write")
	}
	found42 := false
	for _, seq := range ignored {
		if seq == 42 {
			found42 = true
		}
	}
	if !found42 {
		t.Fatalf("expected IgnoreSelfPasteboardChange(42), got %v", ignored)
	}
}

type blockingCapturer struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (b *blockingCapturer) CaptureInteractive(ctx context.Context) (capture.Result, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case b.cancelled <- struct{}{}:
	default:
	}
	return capture.Result{}, ctx.Err()
}

func (b *blockingCapturer) CaptureFull(ctx context.Context) (capture.Result, error) {
	return b.CaptureInteractive(ctx)
}

func TestAnnotateCaptureRegisteredWithRuntimeCancellation(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	capMock := &blockingCapturer{
		started:   make(chan struct{}, 2),
		cancelled: make(chan struct{}, 2),
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     secrets.NewMemoryAPIKeyStore(),
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		Capturer:        capMock,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		ctrl.CaptureAndAnnotate()
	}()
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		ctrl.CaptureAndOCR()
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-capMock.started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for capture calls to start")
		}
	}

	report := ctrl.StopAll("test")
	if len(report.Operations) < 2 {
		t.Fatalf("expected both capture operations registered with runtime, got %+v", report.Operations)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-capMock.cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for capture context cancellation")
		}
	}
	<-done1
	<-done2
}

func TestAnnotateOCRPresentsAIStreamingExactlyOnce(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.anthropic", "sk-ant-test")
	mockHTTP := &sseMockDoer{
		body: "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"OCR OK\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	var presents atomic.Int32
	rawPNG := makeSolidPNG(120, 80)

	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		SecretStore:    sec,
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:  disabledHerdrExecutor{},
		HTTPDoer:       mockHTTP,
		Capturer:       fakeCapturer{res: capture.Result{PNG: rawPNG, Width: 120, Height: 80}},
		OnPresentPhase: func(p engine.CapsulePhase) {
			if p == engine.PhaseAIStreaming {
				presents.Add(1)
			}
		},
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	ctrl.CaptureAndOCR()
	waitUntil(t, 2*time.Second, func() bool {
		return ctrl.State().AIComplete
	}, "CaptureAndOCR stream finishes")
	if got := presents.Load(); got != 1 {
		t.Fatalf("CaptureAndOCR called OnPresentPhase(PhaseAIStreaming) %d times, want 1", got)
	}

	presents.Store(0)
	if err := ctrl.OpenAnnotateImage(rawPNG); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.SendAnnotateToAI(""); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return ctrl.State().AIComplete
	}, "SendAnnotateToAI stream finishes")
	if got := presents.Load(); got != 1 {
		t.Fatalf("SendAnnotateToAI called OnPresentPhase(PhaseAIStreaming) %d times, want 1", got)
	}
}

func TestAnnotateViewShortcutsAndTextEnterCommit(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	var copied, saved int
	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:  disabledHerdrExecutor{},
		AnnotateCopyWriter: func([]byte) error {
			copied++
			return nil
		},
		SaveCapturePath: func(b []byte) (string, error) {
			saved++
			p := filepath.Join(dir, "saved.png")
			return p, os.WriteFile(p, b, 0o644)
		},
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	rawPNG := makeSolidPNG(200, 120)
	if err := ctrl.OpenAnnotateImage(rawPNG); err != nil {
		t.Fatal(err)
	}

	tt := ui.NewTester(ctrl.AnnotateView, 900, 620)
	tt.Frame()

	// Text annotation input auto-focuses and commits on Enter.
	st := annotateStateFor(ctrl)
	st.mu.Lock()
	st.tool = annotate.ToolText
	st.textAnchor = annotate.Point{X: 20, Y: 20}
	st.textEditing = true
	st.textDraft = ""
	st.mu.Unlock()

	tt.Frame()
	tt.Frame()
	tt.Type("Hello 标注")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	st.mu.Lock()
	stillEditing := st.textEditing
	shapes, _, _, _ := st.doc.Snapshot()
	st.mu.Unlock()
	if stillEditing {
		t.Fatal("expected Enter to commit and close text annotation input")
	}
	if len(shapes) != 1 || shapes[0].Tool != annotate.ToolText || shapes[0].Text != "Hello 标注" {
		t.Fatalf("expected committed ToolText shape, got %+v", shapes)
	}

	// Undo via Cmd/Ctrl+Z and Redo via Cmd/Ctrl+Shift+Z.
	tt.Key(primaryMod(), ui.KeyZ)
	tt.Frame()
	shapes, _, canRedo, _ := st.doc.Snapshot()
	if len(shapes) != 0 || !canRedo {
		t.Fatalf("expected Undo via shortcut, shapes=%d canRedo=%v", len(shapes), canRedo)
	}
	tt.Key(primaryMod()|ui.Shift, ui.KeyZ)
	tt.Frame()
	shapes, _, _, _ = st.doc.Snapshot()
	if len(shapes) != 1 {
		t.Fatalf("expected Redo via Cmd/Ctrl+Shift+Z, shapes=%d", len(shapes))
	}

	// Save via Cmd/Ctrl+S.
	tt.Key(primaryMod(), ui.KeyS)
	tt.Frame()
	if saved != 1 || ctrl.AnnotateOpen() {
		t.Fatalf("expected Cmd/Ctrl+S to save and close, saved=%d open=%v", saved, ctrl.AnnotateOpen())
	}

	// Re-open and Copy via Cmd/Ctrl+C.
	if err := ctrl.OpenAnnotateImage(rawPNG); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(primaryMod(), ui.KeyC)
	tt.Frame()
	if copied != 1 || ctrl.AnnotateOpen() {
		t.Fatalf("expected Cmd/Ctrl+C to copy and close, copied=%d open=%v", copied, ctrl.AnnotateOpen())
	}
}
