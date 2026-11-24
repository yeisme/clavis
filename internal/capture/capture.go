package capture

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Result holds captured screenshot image bytes and metadata.
type Result struct {
	PNG        []byte
	Width      int
	Height     int
	CapturedAt time.Time
	Cancelled  bool
}

// ErrCaptureUnavailable indicates the host OS lacks a supported capture tool.
var ErrCaptureUnavailable = errors.New("screenshot capture tool unavailable")

// Capturer abstracts OS screen capture.
type Capturer interface {
	CaptureInteractive(ctx context.Context) (Result, error)
	CaptureFull(ctx context.Context) (Result, error)
}

// CommandRunner abstracts external capture utilities for pure unit tests.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) error
}

type osCommandRunner struct{}

func (osCommandRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (osCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

func readCapturedPNG(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	if len(data) == 0 {
		return Result{Cancelled: true}, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, err
	}
	return Result{
		PNG:        data,
		Width:      cfg.Width,
		Height:     cfg.Height,
		CapturedAt: time.Now(),
	}, nil
}

func createTempCaptureFile(ext string) (string, error) {
	f, err := os.CreateTemp("", "clavis-capture-*"+ext)
	if err != nil {
		return "", err
	}
	name := f.Name()
	_ = f.Close()
	// Remove pre-created empty file so tools like screencapture/grim create it only on confirm.
	_ = os.Remove(name)
	return filepath.Clean(name), nil
}
