//go:build !darwin && !windows

package capture

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// CommandOutputRunner is an optional extension to CommandRunner allowing unit
// tests to supply command stdout (e.g. Wayland slurp geometry) without invoking
// real OS binaries.
type CommandOutputRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

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

func (c *linuxCapturer) runOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if or, ok := c.runner.(CommandOutputRunner); ok {
		return or.Output(ctx, name, args...)
	}
	if _, isOS := c.runner.(osCommandRunner); isOS || c.runner == nil {
		return exec.CommandContext(ctx, name, args...).Output()
	}
	// Custom CommandRunner without Output: still route through c.runner.Run so
	// mocked unit tests never leak out to a real OS executable.
	if err := c.runner.Run(ctx, name, args...); err != nil {
		return nil, err
	}
	return nil, nil
}

func (c *linuxCapturer) CaptureInteractive(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	tmpPath, err := createTempCaptureFile(".png")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(tmpPath)

	// Prefer Wayland grim + slurp when available.
	if grim, errG := c.runner.LookPath("grim"); errG == nil {
		if slurp, errS := c.runner.LookPath("slurp"); errS == nil {
			out, errSel := c.runOutput(ctx, slurp)
			geom := strings.TrimSpace(string(out))
			if errSel != nil || geom == "" {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
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
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
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

	return Result{}, ErrCaptureUnavailable
}

func (c *linuxCapturer) CaptureFull(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
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
