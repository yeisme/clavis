package annotate

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
)

// TextRenderer draws text annotations into dst. UI/host packages may register
// a TrueType/OpenType renderer at startup; when nil, Flatten uses a pure-stdlib
// scalable bitmap glyph renderer so pkg/core remains zero-dependency.
var TextRenderer func(dst *image.RGBA, pos Point, txt string, fontSize float64, c ColorRGBA)

// Flatten composites doc.Base and all committed Shapes into an RGBA image and lossless PNG bytes.
func Flatten(doc *Document) ([]byte, *image.RGBA, error) {
	if doc == nil || doc.Base == nil {
		return nil, nil, fmt.Errorf("annotate: empty document")
	}
	shapes, _, _, _ := doc.Snapshot()
	bounds := doc.Base.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), doc.Base, bounds.Min, draw.Src)

	for _, s := range shapes {
		switch s.Tool {
		case ToolMosaic:
			applyMosaicRect(dst, s.P1, s.P2, s.MosaicBlock)
		case ToolRect:
			drawRectOutline(dst, s.P1, s.P2, s.StrokeWidth, s.Color)
		case ToolArrow:
			drawArrow(dst, s.P1, s.P2, s.StrokeWidth, s.Color)
		case ToolText:
			if TextRenderer != nil {
				TextRenderer(dst, s.P1, s.Text, s.FontSize, s.Color)
			} else {
				drawTextBitmap(dst, s.P1, s.Text, s.FontSize, s.Color)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, nil, fmt.Errorf("annotate: encode png: %w", err)
	}
	return buf.Bytes(), dst, nil
}

func clampBounds(p1, p2 Point, w, h int) (x0, y0, x1, y1 int) {
	fx0, fx1 := math.Min(p1.X, p2.X), math.Max(p1.X, p2.X)
	fy0, fy1 := math.Min(p1.Y, p2.Y), math.Max(p1.Y, p2.Y)
	x0 = max(0, int(math.Floor(fx0)))
	y0 = max(0, int(math.Floor(fy0)))
	x1 = min(w, int(math.Ceil(fx1)))
	y1 = min(h, int(math.Ceil(fy1)))
	return x0, y0, x1, y1
}

func applyMosaicRect(dst *image.RGBA, p1, p2 Point, block int) {
	b := dst.Bounds()
	x0, y0, x1, y1 := clampBounds(p1, p2, b.Dx(), b.Dy())
	if x1 <= x0 || y1 <= y0 {
		return
	}
	if block < 4 {
		block = 12
	}
	for by := y0; by < y1; by += block {
		byEnd := min(y1, by+block)
		for bx := x0; bx < x1; bx += block {
			bxEnd := min(x1, bx+block)
			var sr, sg, sb, sa, count int64
			for y := by; y < byEnd; y++ {
				for x := bx; x < bxEnd; x++ {
					off := dst.PixOffset(x, y)
					sr += int64(dst.Pix[off+0])
					sg += int64(dst.Pix[off+1])
					sb += int64(dst.Pix[off+2])
					sa += int64(dst.Pix[off+3])
					count++
				}
			}
			if count == 0 {
				continue
			}
			avg := color.RGBA{
				R: uint8(sr / count),
				G: uint8(sg / count),
				B: uint8(sb / count),
				A: uint8(sa / count),
			}
			for y := by; y < byEnd; y++ {
				for x := bx; x < bxEnd; x++ {
					dst.SetRGBA(x, y, avg)
				}
			}
		}
	}
}

func blendPixel(dst *image.RGBA, x, y int, c ColorRGBA) {
	if !image.Pt(x, y).In(dst.Bounds()) || c.A == 0 {
		return
	}
	if c.A == 255 {
		dst.SetRGBA(x, y, color.RGBA{R: c.R, G: c.G, B: c.B, A: 255})
		return
	}
	off := dst.PixOffset(x, y)
	alpha := float64(c.A) / 255.0
	inv := 1.0 - alpha
	dst.Pix[off+0] = uint8(float64(c.R)*alpha + float64(dst.Pix[off+0])*inv)
	dst.Pix[off+1] = uint8(float64(c.G)*alpha + float64(dst.Pix[off+1])*inv)
	dst.Pix[off+2] = uint8(float64(c.B)*alpha + float64(dst.Pix[off+2])*inv)
	dst.Pix[off+3] = 255
}

func drawRectOutline(dst *image.RGBA, p1, p2 Point, width float64, c ColorRGBA) {
	b := dst.Bounds()
	x0, y0, x1, y1 := clampBounds(p1, p2, b.Dx(), b.Dy())
	if x1 <= x0 || y1 <= y0 {
		return
	}
	sw := max(1, int(math.Round(width)))
	for t := 0; t < sw; t++ {
		for x := x0; x < x1; x++ {
			if y0+t < y1 {
				blendPixel(dst, x, y0+t, c)
			}
			if y1-1-t >= y0 {
				blendPixel(dst, x, y1-1-t, c)
			}
		}
		for y := y0; y < y1; y++ {
			if x0+t < x1 {
				blendPixel(dst, x0+t, y, c)
			}
			if x1-1-t >= x0 {
				blendPixel(dst, x1-1-t, y, c)
			}
		}
	}
}

func drawDisc(dst *image.RGBA, cx, cy, r float64, c ColorRGBA) {
	if r < 0.5 {
		r = 0.5
	}
	minX := int(math.Floor(cx - r))
	maxX := int(math.Ceil(cx + r))
	minY := int(math.Floor(cy - r))
	maxY := int(math.Ceil(cy + r))
	r2 := r * r
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			if dx*dx+dy*dy <= r2 {
				blendPixel(dst, x, y, c)
			}
		}
	}
}

func drawThickLine(dst *image.RGBA, x0, y0, x1, y1, width float64, c ColorRGBA) {
	dx, dy := x1-x0, y1-y0
	dist := math.Hypot(dx, dy)
	steps := max(1, int(math.Ceil(dist*1.5)))
	radius := math.Max(1, width/2)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		drawDisc(dst, x0+dx*t, y0+dy*t, radius, c)
	}
}

func drawArrow(dst *image.RGBA, p1, p2 Point, width float64, c ColorRGBA) {
	dx := p2.X - p1.X
	dy := p2.Y - p1.Y
	dist := math.Hypot(dx, dy)
	if dist < 2 {
		return
	}
	if width <= 0 {
		width = 3
	}
	headLen := math.Min(dist*0.45, math.Max(14, width*4.5))
	headHalfW := math.Max(7, width*2.4)
	ux, uy := dx/dist, dy/dist
	baseX := p2.X - ux*headLen
	baseY := p2.Y - uy*headLen
	nx, ny := -uy, ux

	shaftEndX := p2.X - ux*(headLen*0.65)
	shaftEndY := p2.Y - uy*(headLen*0.65)
	drawThickLine(dst, p1.X, p1.Y, shaftEndX, shaftEndY, width, c)

	left := Point{X: baseX + nx*headHalfW, Y: baseY + ny*headHalfW}
	right := Point{X: baseX - nx*headHalfW, Y: baseY - ny*headHalfW}
	fillTriangle(dst, p2, left, right, c)
}

func fillTriangle(dst *image.RGBA, a, bPt, cPt Point, col ColorRGBA) {
	minX := max(0, int(math.Floor(math.Min(a.X, math.Min(bPt.X, cPt.X)))))
	maxX := min(dst.Bounds().Dx()-1, int(math.Ceil(math.Max(a.X, math.Max(bPt.X, cPt.X)))))
	minY := max(0, int(math.Floor(math.Min(a.Y, math.Min(bPt.Y, cPt.Y)))))
	maxY := min(dst.Bounds().Dy()-1, int(math.Ceil(math.Max(a.Y, math.Max(bPt.Y, cPt.Y)))))

	edge := func(p, q, r Point) float64 {
		return (r.X-p.X)*(q.Y-p.Y) - (r.Y-p.Y)*(q.X-p.X)
	}
	area := edge(a, bPt, cPt)
	if math.Abs(area) < 1e-6 {
		return
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			pt := Point{X: float64(x) + 0.5, Y: float64(y) + 0.5}
			w0 := edge(bPt, cPt, pt)
			w1 := edge(cPt, a, pt)
			w2 := edge(a, bPt, pt)
			if area > 0 {
				if w0 >= 0 && w1 >= 0 && w2 >= 0 {
					blendPixel(dst, x, y, col)
				}
			} else if w0 <= 0 && w1 <= 0 && w2 <= 0 {
				blendPixel(dst, x, y, col)
			}
		}
	}
}

// Compact 5x7 pure-stdlib bitmap glyphs for headless/mobile flatten fallback.
var stdFont5x7 = map[rune][7]uint8{
	' ': {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	'-': {0x00, 0x00, 0x00, 0x1F, 0x00, 0x00, 0x00},
	'.': {0x00, 0x00, 0x00, 0x00, 0x00, 0x0C, 0x0C},
	':': {0x00, 0x0C, 0x0C, 0x00, 0x0C, 0x0C, 0x00},
	'0': {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	'1': {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'2': {0x0E, 0x11, 0x01, 0x06, 0x08, 0x10, 0x1F},
	'3': {0x1E, 0x01, 0x01, 0x0E, 0x01, 0x01, 0x1E},
	'4': {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	'5': {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	'6': {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	'7': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	'9': {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
	'A': {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'B': {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E},
	'C': {0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E},
	'D': {0x1C, 0x12, 0x11, 0x11, 0x11, 0x12, 0x1C},
	'E': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F},
	'F': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10},
	'G': {0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0F},
	'H': {0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'I': {0x0E, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'J': {0x07, 0x02, 0x02, 0x02, 0x12, 0x12, 0x0C},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F},
	'M': {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O': {0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'P': {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
	'Q': {0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D},
	'R': {0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11},
	'S': {0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E},
	'T': {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x1B, 0x11},
	'X': {0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0A, 0x04, 0x04, 0x04, 0x04},
	'Z': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F},
}

func drawTextBitmap(dst *image.RGBA, pos Point, txt string, fontSize float64, c ColorRGBA) {
	txt = strings.TrimSpace(txt)
	if txt == "" {
		return
	}
	scale := max(1, int(math.Round(fontSize/9)))
	ox := int(math.Round(pos.X))
	oy := int(math.Round(pos.Y))
	shadow := ColorRGBA{R: 0, G: 0, B: 0, A: 140}
	for idx, r := range []rune(txt) {
		up := r
		if up >= 'a' && up <= 'z' {
			up = up - 'a' + 'A'
		}
		glyph, ok := stdFont5x7[up]
		if !ok {
			glyph = [7]uint8{0x1F, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1F}
		}
		gx := ox + idx*6*scale
		for row := 0; row < 7; row++ {
			bits := glyph[row]
			for col := 0; col < 5; col++ {
				if bits&(1<<(4-col)) != 0 {
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							blendPixel(dst, gx+col*scale+dx+1, oy+row*scale+dy+1, shadow)
							blendPixel(dst, gx+col*scale+dx, oy+row*scale+dy, c)
						}
					}
				}
			}
		}
	}
}
