package capture

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"testing"
)

type mockRunner struct {
	lookPath map[string]string
	runFn    func(ctx context.Context, name string, args ...string) error
}

func (m *mockRunner) LookPath(file string) (string, error) {
	if p, ok := m.lookPath[file]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) error {
	if m.runFn != nil {
		return m.runFn(ctx, name, args...)
	}
	return nil
}

func samplePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.SetRGBA(0, 0, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestCapturerWithMockRunner(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("mock darwin screencapture test runs on darwin")
	}
	data := samplePNG(40, 24)
	capOK := NewCapturerWithRunner(&mockRunner{
		runFn: func(_ context.Context, _ string, args ...string) error {
			outPath := args[len(args)-1]
			return os.WriteFile(outPath, data, 0o600)
		},
	})
	res, err := capOK.CaptureInteractive(context.Background())
	if err != nil {
		t.Fatalf("CaptureInteractive: %v", err)
	}
	if res.Cancelled || res.Width != 40 || res.Height != 24 || len(res.PNG) == 0 {
		t.Fatalf("unexpected capture result: %+v", res)
	}

	// Simulate user pressing Esc (no file written).
	capCancel := NewCapturerWithRunner(&mockRunner{
		runFn: func(context.Context, string, ...string) error {
			return errors.New("exit status 1")
		},
	})
	resCancel, err := capCancel.CaptureInteractive(context.Background())
	if err != nil {
		t.Fatalf("cancelled capture returned err: %v", err)
	}
	if !resCancel.Cancelled {
		t.Fatalf("expected Cancelled=true when file missing")
	}
}
