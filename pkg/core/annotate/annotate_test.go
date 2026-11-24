package annotate

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func buildPatternPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			if (x/2+y/2)%2 == 0 {
				c = color.RGBA{R: 20, G: 40, B: 200, A: 255}
			} else {
				c = color.RGBA{R: 220, G: 220, B: 220, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestDocumentUndoRedoAndFlattenMosaicRectArrowText(t *testing.T) {
	rawPNG := buildPatternPNG(160, 120)
	doc, err := NewDocumentFromPNG(rawPNG)
	if err != nil {
		t.Fatalf("NewDocumentFromPNG: %v", err)
	}
	if doc.Width != 160 || doc.Height != 120 {
		t.Fatalf("dims = %dx%d, want 160x120", doc.Width, doc.Height)
	}

	red := ColorRGBA{R: 255, G: 40, B: 40, A: 255}
	blue := ColorRGBA{R: 10, G: 132, B: 255, A: 255}
	white := ColorRGBA{R: 255, G: 255, B: 255, A: 255}

	doc.AddShape(Shape{Tool: ToolMosaic, P1: Point{10, 10}, P2: Point{50, 50}, MosaicBlock: 10})
	doc.AddShape(Shape{Tool: ToolRect, P1: Point{60, 10}, P2: Point{120, 50}, Color: red, StrokeWidth: 3})
	doc.AddShape(Shape{Tool: ToolArrow, P1: Point{20, 90}, P2: Point{100, 90}, Color: blue, StrokeWidth: 4})
	doc.AddShape(Shape{Tool: ToolText, P1: Point{15, 65}, Text: "BUG", Color: white, FontSize: 22})

	if _, canUndo, canRedo, _ := doc.Snapshot(); !canUndo || canRedo {
		t.Fatalf("expected canUndo=true, canRedo=false")
	}
	if !doc.Undo() {
		t.Fatal("Undo returned false")
	}
	if shapes, _, canRedo, _ := doc.Snapshot(); len(shapes) != 3 || !canRedo {
		t.Fatalf("after Undo len=%d canRedo=%v", len(shapes), canRedo)
	}
	if !doc.Redo() {
		t.Fatal("Redo returned false")
	}

	outPNG, outRGBA, err := Flatten(doc)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(outPNG))
	if err != nil || cfg.Width != 160 || cfg.Height != 120 {
		t.Fatalf("decoded output PNG %+v err=%v", cfg, err)
	}

	// Verify Mosaic block [10..20)x[10..20) collapsed checkerboard pixels to uniform color.
	firstMosaic := outRGBA.RGBAAt(11, 11)
	if outRGBA.RGBAAt(13, 13) != firstMosaic || outRGBA.RGBAAt(15, 15) != firstMosaic {
		t.Fatalf("mosaic block pixels not uniform: (11,11)=%v (13,13)=%v", firstMosaic, outRGBA.RGBAAt(13, 13))
	}

	// Verify Rect top border pixel is red.
	borderPx := outRGBA.RGBAAt(80, 11)
	if borderPx.R < 220 || borderPx.G > 80 {
		t.Fatalf("expected red rect border pixel at (80,11), got %v", borderPx)
	}

	// Verify Arrow tip area is blue.
	tipPx := outRGBA.RGBAAt(95, 90)
	if tipPx.B < 220 || tipPx.R > 60 {
		t.Fatalf("expected blue arrow tip pixel at (95,90), got %v", tipPx)
	}
}
