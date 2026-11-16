package uiapp

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestVisualSnapshotPNGs is an opt-in postcondition for the snapshot harness.
// It catches an encoder or renderer that silently writes an empty or uniformly
// coloured surface while still producing a valid PNG.
func TestVisualSnapshotPNGs(t *testing.T) {
	dir := os.Getenv("CLAVIS_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set CLAVIS_SNAPSHOT_DIR to validate visual snapshots")
	}
	validateVisualSnapshotPNGs(t, dir)
}

func validateVisualSnapshotPNGs(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".png" {
			continue
		}
		count++
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", entry.Name())
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Errorf("%s is not a PNG: %v", entry.Name(), err)
			continue
		}
		bounds := img.Bounds()
		if bounds.Empty() {
			t.Errorf("%s has empty bounds", entry.Name())
			continue
		}
		first := color.RGBA{}
		var different bool
		for y := bounds.Min.Y; y < bounds.Max.Y && !different; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
				if x == bounds.Min.X && y == bounds.Min.Y {
					first = got
					continue
				}
				if got != first {
					different = true
					break
				}
			}
		}
		if !different {
			t.Errorf("%s is a single flat colour (%v)", entry.Name(), first)
		}
	}
	if count == 0 {
		t.Skip("snapshot directory contains no PNGs; run TestVisualSnapshots to generate them")
	}
	t.Logf("validated %d PNGs in %s", count, dir)
}
