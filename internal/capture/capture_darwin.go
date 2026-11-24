//go:build darwin

package capture

import (
	"context"
	"os"
)

const macOSCaptureBin = "/usr/sbin/screencapture"

type darwinCapturer struct {
	runner CommandRunner
}

// NewCapturer constructs the platform screenshot capturer.
func NewCapturer() Capturer {
	return &darwinCapturer{runner: osCommandRunner{}}
}

func NewCapturerWithRunner(r CommandRunner) Capturer {
	if r == nil {
		r = osCommandRunner{}
	}
	return &darwinCapturer{runner: r}
}

func (c *darwinCapturer) resolveBinary() (string, error) {
	if _, err := os.Stat(macOSCaptureBin); err == nil {
		return macOSCaptureBin, nil
	}
	if c.runner != nil {
		if p, err := c.runner.LookPath("screencapture"); err == nil && p != "" {
			return p, nil
		}
	}
	return "", ErrCaptureUnavailable
}

func (c *darwinCapturer) CaptureInteractive(ctx context.Context) (Result, error) {
	return c.run(ctx, "-i", "-r", "-x")
}

func (c *darwinCapturer) CaptureFull(ctx context.Context) (Result, error) {
	return c.run(ctx, "-r", "-x")
}

func (c *darwinCapturer) run(ctx context.Context, flags ...string) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bin, err := c.resolveBinary()
	if err != nil {
		return Result{}, err
	}
	tmpPath, err := createTempCaptureFile(".png")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(tmpPath)

	args := append(append([]string(nil), flags...), tmpPath)
	runErr := c.runner.Run(ctx, bin, args...)
	if _, statErr := os.Stat(tmpPath); statErr != nil {
		// User pressed Esc to dismiss interactive region selection.
		if runErr == nil || ctx.Err() == nil {
			return Result{Cancelled: true}, nil
		}
		return Result{}, runErr
	}
	if runErr != nil {
		return Result{}, runErr
	}
	return readCapturedPNG(tmpPath)
}
