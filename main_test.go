package main

import (
	"bytes"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/uiapp"
)

func TestHasSmokeCheckFlag(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--help"}, false},
		{[]string{"--smoke-check"}, true},
		{[]string{"--foo", "--smoke-check"}, true},
		{[]string{"--", "--smoke-check"}, false},
	}
	for _, tc := range cases {
		if got := hasSmokeCheckFlag(tc.args); got != tc.want {
			t.Errorf("hasSmokeCheckFlag(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestRunSmokeCheck_OutputAndNoUserConfigMutation(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	var out bytes.Buffer
	if err := runSmokeCheck(&out); err != nil {
		t.Fatalf("runSmokeCheck returned error: %v", err)
	}

	got := strings.TrimSpace(out.String())
	wantPrefix := "Clavis smoke-check ok (leader=right_option, os=" + runtime.GOOS + ", arch=" + runtime.GOARCH + ")"
	if got != wantPrefix {
		t.Fatalf("smoke-check output = %q, want %q", got, wantPrefix)
	}

	userCfgDir := filepath.Join(fakeHome, ".config", "clavis")
	if _, err := os.Stat(userCfgDir); !os.IsNotExist(err) {
		t.Fatalf("smoke-check should not create %s, stat err=%v", userCfgDir, err)
	}
}

func TestComputeCapsuleBounds_PlacementAndWorkAreaClamping(t *testing.T) {
	displays := []mygo.Display{
		{
			ID:       1,
			Primary:  true,
			Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900},
			WorkArea: mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 825}, // 25pt menu bar, 50pt dock
		},
		{
			ID:       2,
			Primary:  false,
			Bounds:   mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1080},
			WorkArea: mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1040},
		},
	}
	capsuleSize := bridge.Size{Width: 680, Height: 340}

	t.Run("places below valid caret anchor", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 200, Y: 120, Width: 2, Height: 20, Valid: true}
		got := computeCapsuleBounds(anchor, mygo.Point{X: 10, Y: 10}, capsuleSize, displays)

		if got.X != 200 {
			t.Fatalf("X = %d, want 200", got.X)
		}
		wantY := int(120 + 20 + bridge.PopoverAnchorGap)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want %d", got.Y, wantY)
		}
		if got.Width != 680 || got.Height != 340 {
			t.Fatalf("size = (%d,%d), want (680,340)", got.Width, got.Height)
		}
	})

	t.Run("flips above caret near bottom of work area", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 300, Y: 780, Width: 2, Height: 20, Valid: true}
		got := computeCapsuleBounds(anchor, mygo.Point{}, capsuleSize, displays)

		wantY := int(780 - 340 - bridge.PopoverAnchorGap)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want %d (flipped above anchor)", got.Y, wantY)
		}
	})

	t.Run("clamps horizontally and vertically inside work area with margin", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 1350, Y: 20, Width: 2, Height: 16, Valid: true}
		got := computeCapsuleBounds(anchor, mygo.Point{}, capsuleSize, displays)

		maxAllowedX := 1440 - 680 - int(bridge.PopoverScreenMargin)
		if got.X != maxAllowedX {
			t.Fatalf("X = %d, want %d", got.X, maxAllowedX)
		}
		minAllowedY := 25 + int(bridge.PopoverScreenMargin)
		if got.Y < minAllowedY {
			t.Fatalf("Y = %d, want >= %d", got.Y, minAllowedY)
		}
	})

	t.Run("uses cursor fallback when anchor is nil or invalid and targets secondary display", func(t *testing.T) {
		invalid := &host.CursorAnchor{X: 0, Y: 0, Valid: false}
		cursor := mygo.Point{X: 1800, Y: 200}
		got := computeCapsuleBounds(invalid, cursor, capsuleSize, displays)

		if got.X != 1800 {
			t.Fatalf("X = %d, want 1800 on secondary display", got.X)
		}
		wantY := int(200 + 18 + bridge.PopoverAnchorGap)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want %d", got.Y, wantY)
		}
	})
}

func TestComputeIMEPillBounds_PlacementAndWorkAreaClamping(t *testing.T) {
	displays := []mygo.Display{
		{
			ID:       1,
			Primary:  true,
			Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900},
			WorkArea: mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 825},
		},
		{
			ID:       2,
			Primary:  false,
			Bounds:   mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1080},
			WorkArea: mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1040},
		},
	}
	pw, ph := uiapp.PreferredIMEPillSize()
	pillSize := bridge.Size{Width: float64(pw), Height: float64(ph)}

	t.Run("places compactly above and right of valid caret anchor without covering typing line", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 200, Y: 120, Width: 2, Height: 20, Valid: true}
		got := computeIMEPillBounds(anchor, mygo.Point{X: 10, Y: 10}, pillSize, displays)

		wantX := int(200 + 2 + imePillOffsetX)
		wantY := int(120 - float64(ph) - imePillOffsetY)
		if got.X != wantX || got.Y != wantY {
			t.Fatalf("pos = (%d,%d), want (%d,%d)", got.X, got.Y, wantX, wantY)
		}
		if got.Width != pw || got.Height != ph {
			t.Fatalf("size = (%d,%d), want (%d,%d)", got.Width, got.Height, pw, ph)
		}
		if got.Y+got.Height > int(anchor.Y) {
			t.Fatalf("pill bottom %d overlaps typing line starting at %d", got.Y+got.Height, int(anchor.Y))
		}
	})

	t.Run("flips below typing line near top of work area and clamps right edge", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 1420, Y: 30, Width: 2, Height: 20, Valid: true}
		got := computeIMEPillBounds(anchor, mygo.Point{}, pillSize, displays)

		maxAllowedX := 1440 - pw - int(bridge.PopoverScreenMargin)
		if got.X != maxAllowedX {
			t.Fatalf("X = %d, want clamped %d", got.X, maxAllowedX)
		}
		wantY := int(30 + 20 + imePillOffsetY)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want flipped below typing line %d", got.Y, wantY)
		}
	})

	t.Run("uses cursor fallback when anchor is nil or invalid on secondary display", func(t *testing.T) {
		invalid := &host.CursorAnchor{X: 0, Y: 0, Valid: false}
		cursor := mygo.Point{X: 1800, Y: 200}
		got := computeIMEPillBounds(invalid, cursor, bridge.Size{}, displays)

		wantX := int(1800 + 1 + imePillOffsetX)
		wantY := int(200 - float64(ph) - imePillOffsetY)
		if got.X != wantX || got.Y != wantY {
			t.Fatalf("pos = (%d,%d), want (%d,%d) on secondary display", got.X, got.Y, wantX, wantY)
		}
		if got.Width != pw || got.Height != ph {
			t.Fatalf("size = (%d,%d), want (%d,%d)", got.Width, got.Height, pw, ph)
		}
	})

	t.Run("sizes status/layer pills wider while clamping to the work area", func(t *testing.T) {
		labelW, labelH := uiapp.PreferredPillSizeForLabel("LAYER • NAV")
		if labelW <= pw || labelH <= 0 {
			t.Fatalf("PreferredPillSizeForLabel('LAYER • NAV') = (%d,%d), want wider than (%d,%d)", labelW, labelH, pw, ph)
		}
		anchor := &host.CursorAnchor{X: 1400, Y: 120, Width: 2, Height: 20, Valid: true}
		got := computeIMEPillBounds(anchor, mygo.Point{}, bridge.Size{Width: float64(labelW), Height: float64(labelH)}, displays)

		if got.Width != labelW || got.Height != labelH {
			t.Fatalf("size = (%d,%d), want (%d,%d)", got.Width, got.Height, labelW, labelH)
		}
		maxAllowedX := 1440 - labelW - int(bridge.PopoverScreenMargin)
		if got.X != maxAllowedX {
			t.Fatalf("X = %d, want clamped %d", got.X, maxAllowedX)
		}
	})
}

func TestGenerateTrayIconPNG(t *testing.T) {
	data := uiapp.GenerateTrayIconPNG(32)
	if len(data) == 0 {
		t.Fatal("uiapp.GenerateTrayIconPNG(32) returned empty bytes")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode failed: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("tray icon size = %dx%d, want 32x32", b.Dx(), b.Dy())
	}

	var opaquePixels, transparentPixels int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A == 0 {
				transparentPixels++
				continue
			}
			opaquePixels++
			if c.R != 0 || c.G != 0 || c.B != 0 {
				t.Fatalf("template icon pixel (%d,%d) has non-black RGB (%d,%d,%d)", x, y, c.R, c.G, c.B)
			}
		}
	}
	if opaquePixels == 0 || transparentPixels == 0 {
		t.Fatalf("expected both drawn and transparent pixels, got opaque=%d transparent=%d", opaquePixels, transparentPixels)
	}
}

func TestToMyGoAccelerator(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"option+space", "Alt+Space"},
		{"command+shift+k", "Super+Shift+K"},
		{"ctrl+alt+p", "Ctrl+Alt+P"},
		{"cmd+grave", "Super+`"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := toMyGoAccelerator(tc.in); got != tc.want {
			t.Errorf("toMyGoAccelerator(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatPermissionsMenuLabel(t *testing.T) {
	if got := formatPermissionsMenuLabel(host.PermissionStatus{Accessibility: true, InputMonitoring: true}, nil, host.Capabilities{Accessibility: true, InputMonitoring: true}); got != "Permissions: All Granted" {
		t.Fatalf("got %q", got)
	}
	if got := formatPermissionsMenuLabel(host.PermissionStatus{Accessibility: false, InputMonitoring: true}, nil, host.Capabilities{Accessibility: true, InputMonitoring: true}); got != "Permissions: Grant Accessibility" {
		t.Fatalf("got %q", got)
	}
	if got := formatPermissionsMenuLabel(host.PermissionStatus{}, nil, host.Capabilities{}); got != "Permissions: Standard Mode" {
		t.Fatalf("got %q", got)
	}
}
