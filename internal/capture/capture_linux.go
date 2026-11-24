//go:build !darwin && !windows

package capture

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
)

type linuxCapturer struct {
	runner CommandRunner
}

// NewCapturer constructs the Linux Wayland/X11 screenshot capturer.
func NewCapturer() Capturer {
	return &linuxCapturer{runner: osCommandRunner{}}
}

func NewCapturerWithRunner(r CommandRunner) Capturer {
	if r == nil {
		r = osCommandRunner{}
	}
	return &linuxCapturer{runner: r}
}

func (c *linuxCapturer) CaptureInteractive(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tmpPath, err := createTempCaptureFile(".png")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(tmpPath)

	// Prefer Wayland grim + slurp when available.
	if grim, errG := c.runner.LookPath("grim"); errG == nil {
		if slurp, errS := c.runner.LookPath("slurp"); errS == nil {
			out, errSel := exec.CommandContext(ctx, slurp).Output()
			geom := strings.TrimSpace(string(out))
			if errSel != nil || geom == "" {
				return Result{Cancelled: true}, nil
			}
			if err := c.runner.Run(ctx, grim, "-g", geom, tmpPath); err != nil {
				return Result{}, err
			}
			return readCapturedPNG(tmpPath)
		}
		if err := c.runner.Run(ctx, grim, tmpPath); err != nil {
			return Result{}, err
		}
		return readCapturedPNG(tmpPath)
	}

	// Fallback X11 scrot / import.
	if scrot, errSc := c.runner.LookPath("scrot"); errSc == nil {
		if err := c.runner.Run(ctx, scrot, "-s", tmpPath); err != nil {
			if _, stErr := os.Stat(tmpPath); stErr != nil {
				return Result{Cancelled: true}, nil
			}
			return Result{}, err
		}
		return readCapturedPNG(tmpPath)
	}
	if imp, errIm := c.runner.LookPath("import"); errIm == nil {
		if err := c.runner.Run(ctx, imp, tmpPath); err != nil {
			return Result{}, err
		}
		return readCapturedPNG(tmpPath)
	}

	_ = bytes.Buffer{}
	return Result{}, ErrCaptureUnavailable
}

func (c *linuxCapturer) CaptureFull(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tmpPath, err := createTempCaptureFile(".png")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(tmpPath)

	if grim, errG := c.runner.LookPath("grim"); errG == nil {
		if err := c.runner.Run(ctx, grim, tmpPath); err != nil {
			return Result{}, err
		}
		return readCapturedPNG(tmpPath)
	}
	if scrot, errSc := c.runner.LookPath("scrot"); errSc == nil {
		if err := c.runner.Run(ctx, scrot, tmpPath); err != nil {
			return Result{}, err
		}
		return readCapturedPNG(tmpPath)
	}
	return Result{}, ErrCaptureUnavailable
}
