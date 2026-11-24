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
	"slices"
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

type mockOutputRunner struct {
	mockRunner
	outputFn func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (m *mockOutputRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if m.outputFn != nil {
		return m.outputFn(ctx, name, args...)
	}
	return nil, nil
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

func TestCapturerHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cap := NewCapturerWithRunner(&mockRunner{
		lookPath: map[string]string{
			"screencapture": "/usr/sbin/screencapture",
			"grim":          "/usr/bin/grim",
			"slurp":         "/usr/bin/slurp",
		},
		runFn: func(c context.Context, _ string, _ ...string) error {
			return c.Err()
		},
	})
	if _, err := cap.CaptureInteractive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CaptureInteractive(cancelled) err = %v, want context.Canceled", err)
	}
	if _, err := cap.CaptureFull(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CaptureFull(cancelled) err = %v, want context.Canceled", err)
	}
}

func TestLinuxCapturerRoutesSlurpThroughRunner(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux grim+slurp mock test runs on linux")
	}
	data := samplePNG(32, 18)
	var grimArgs []string
	slurpCalled := false

	runner := &mockOutputRunner{
		mockRunner: mockRunner{
			lookPath: map[string]string{
				"grim":  "/usr/bin/grim",
				"slurp": "/usr/bin/slurp",
			},
			runFn: func(_ context.Context, name string, args ...string) error {
				if name != "/usr/bin/grim" {
					t.Fatalf("unexpected Run binary %q", name)
				}
				grimArgs = append([]string(nil), args...)
				return os.WriteFile(args[len(args)-1], data, 0o600)
			},
		},
		outputFn: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name != "/usr/bin/slurp" {
				t.Fatalf("unexpected Output binary %q", name)
			}
			slurpCalled = true
			return []byte("10,20 32x18\n"), nil
		},
	}

	res, err := NewCapturerWithRunner(runner).CaptureInteractive(context.Background())
	if err != nil {
		t.Fatalf("CaptureInteractive: %v", err)
	}
	if !slurpCalled {
		t.Fatal("slurp was not routed through runner")
	}
	if len(grimArgs) != 3 || grimArgs[0] != "-g" || grimArgs[1] != "10,20 32x18" {
		t.Fatalf("unexpected grim args: %v", grimArgs)
	}
	if res.Cancelled || res.Width != 32 || res.Height != 18 {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Slurp cancellation returns Cancelled=true without calling grim.
	grimCalled := false
	cancelRunner := &mockOutputRunner{
		mockRunner: mockRunner{
			lookPath: map[string]string{"grim": "/usr/bin/grim", "slurp": "/usr/bin/slurp"},
			runFn: func(context.Context, string, ...string) error {
				grimCalled = true
				return nil
			},
		},
		outputFn: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("cancelled")
		},
	}
	resCancel, err := NewCapturerWithRunner(cancelRunner).CaptureInteractive(context.Background())
	if err != nil || !resCancel.Cancelled || grimCalled {
		t.Fatalf("slurp cancel = (%+v, %v, grimCalled=%v)", resCancel, err, grimCalled)
	}

	// A plain CommandRunner without Output must still route through runner.Run
	// instead of executing a real OS slurp binary.
	var plainCalls []string
	plainRunner := &mockRunner{
		lookPath: map[string]string{"grim": "/usr/bin/grim", "slurp": "/usr/bin/slurp"},
		runFn: func(_ context.Context, name string, _ ...string) error {
			plainCalls = append(plainCalls, name)
			return errors.New("mock cancelled")
		},
	}
	resPlain, err := NewCapturerWithRunner(plainRunner).CaptureInteractive(context.Background())
	if err != nil || !resPlain.Cancelled {
		t.Fatalf("plain runner cancel = (%+v, %v)", resPlain, err)
	}
	if !slices.Equal(plainCalls, []string{"/usr/bin/slurp"}) {
		t.Fatalf("plain runner calls = %v, want [/usr/bin/slurp]", plainCalls)
	}
}

func TestWindowsCapturerInteractiveCancelAndFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows mock capturer test runs on windows")
	}

	// Interactive snip succeeds.
	snipData := samplePNG(64, 36)
	capOK := NewCapturerWithRunner(&mockRunner{
		runFn: func(_ context.Context, name string, args ...string) error {
			if name != "ms-screenclip:" {
				t.Fatalf("expected ms-screenclip:, got %q", name)
			}
			return os.WriteFile(args[len(args)-1], snipData, 0o600)
		},
	})
	res, err := capOK.CaptureInteractive(context.Background())
	if err != nil || res.Cancelled || res.Width != 64 || res.Height != 36 {
		t.Fatalf("interactive capture = (%+v, %v)", res, err)
	}

	// User cancels snip (no PNG produced).
	capCancel := NewCapturerWithRunner(&mockRunner{
		runFn: func(context.Context, string, ...string) error { return nil },
	})
	resCancel, err := capCancel.CaptureInteractive(context.Background())
	if err != nil || !resCancel.Cancelled {
		t.Fatalf("cancelled snip = (%+v, %v), want Cancelled=true", resCancel, err)
	}

	// ms-screenclip: unavailable -> falls back cleanly to CaptureFull.
	fullData := samplePNG(120, 80)
	var calls []string
	capFallback := NewCapturerWithRunner(&mockRunner{
		runFn: func(_ context.Context, name string, args ...string) error {
			calls = append(calls, name)
			if name == "ms-screenclip:" {
				return ErrCaptureUnavailable
			}
			return os.WriteFile(args[len(args)-1], fullData, 0o600)
		},
	})
	resFull, err := capFallback.CaptureInteractive(context.Background())
	if err != nil || resFull.Cancelled || resFull.Width != 120 || resFull.Height != 80 {
		t.Fatalf("fallback capture = (%+v, %v)", resFull, err)
	}
	if !slices.Equal(calls, []string{"ms-screenclip:", "gdi-capture"}) {
		t.Fatalf("fallback calls = %v, want [ms-screenclip: gdi-capture]", calls)
	}
}
