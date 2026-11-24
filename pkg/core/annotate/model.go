package annotate

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sync"
)

// ToolKind identifies the vector/pixel annotation primitive.
type ToolKind string

const (
	ToolRect   ToolKind = "rect"
	ToolArrow  ToolKind = "arrow"
	ToolMosaic ToolKind = "mosaic"
	ToolText   ToolKind = "text"
)

// Point specifies coordinates in source image pixel space ([0..Width], [0..Height]).
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ColorRGBA represents an 8-bit sRGBA stroke or fill color.
type ColorRGBA struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
	A uint8 `json:"a"`
}

// PalettePreset names a curated high-contrast annotation swatch.
type PalettePreset struct {
	Name  string
	Color ColorRGBA
}

// DefaultPalette returns curated annotation colors matching desktop visual contrast.
func DefaultPalette() []PalettePreset {
	return []PalettePreset{
		{Name: "Red", Color: ColorRGBA{R: 255, G: 59, B: 48, A: 255}},
		{Name: "Blue", Color: ColorRGBA{R: 10, G: 132, B: 255, A: 255}},
		{Name: "Green", Color: ColorRGBA{R: 48, G: 209, B: 88, A: 255}},
		{Name: "Amber", Color: ColorRGBA{R: 255, G: 204, B: 0, A: 255}},
		{Name: "White", Color: ColorRGBA{R: 255, G: 255, B: 255, A: 255}},
	}
}

// Shape describes one committed vector/mosaic annotation primitive.
type Shape struct {
	ID          string    `json:"id"`
	Tool        ToolKind  `json:"tool"`
	P1          Point     `json:"p1"`
	P2          Point     `json:"p2"`
	Color       ColorRGBA `json:"color"`
	StrokeWidth float64   `json:"strokeWidth"`
	MosaicBlock int       `json:"mosaicBlock,omitempty"`
	Text        string    `json:"text,omitempty"`
	FontSize    float64   `json:"fontSize,omitempty"`
}

// Document holds a base screenshot image and an undoable stack of annotations.
type Document struct {
	mu        sync.RWMutex
	Width     int
	Height    int
	BasePNG   []byte
	Base      image.Image
	Shapes    []Shape
	redoStack []Shape
	Version   uint64
}

// NewDocumentFromPNG decodes PNG/raster bytes into an editable annotation Document.
func NewDocumentFromPNG(data []byte) (*Document, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("annotate: empty image data")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("annotate: decode image: %w", err)
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("annotate: invalid image dimensions %dx%d", b.Dx(), b.Dy())
	}
	return &Document{
		Width:   b.Dx(),
		Height:  b.Dy(),
		BasePNG: append([]byte(nil), data...),
		Base:    img,
		Version: 1,
	}, nil
}

// NewDocumentFromImage wraps an already-decoded image.Image.
func NewDocumentFromImage(img image.Image) (*Document, error) {
	if img == nil {
		return nil, fmt.Errorf("annotate: nil base image")
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("annotate: invalid image dimensions %dx%d", b.Dx(), b.Dy())
	}
	return &Document{
		Width:   b.Dx(),
		Height:  b.Dy(),
		Base:    img,
		Version: 1,
	}, nil
}

// AddShape commits s onto the document and clears any stale redo branch.
func (d *Document) AddShape(s Shape) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s.ID == "" {
		s.ID = fmt.Sprintf("shape-%d", len(d.Shapes)+1)
	}
	if s.StrokeWidth <= 0 {
		s.StrokeWidth = 3
	}
	if s.Color.A == 0 {
		s.Color = ColorRGBA{R: 255, G: 59, B: 48, A: 255}
	}
	if s.Tool == ToolMosaic && s.MosaicBlock <= 0 {
		s.MosaicBlock = 14
	}
	if s.Tool == ToolText && s.FontSize <= 0 {
		s.FontSize = 20
	}
	d.Shapes = append(d.Shapes, s)
	d.redoStack = nil
	d.Version++
}

// Undo pops the most recent shape onto the redo stack.
func (d *Document) Undo() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.Shapes) == 0 {
		return false
	}
	last := d.Shapes[len(d.Shapes)-1]
	d.Shapes = d.Shapes[:len(d.Shapes)-1]
	d.redoStack = append(d.redoStack, last)
	d.Version++
	return true
}

// Redo restores the most recently undone shape.
func (d *Document) Redo() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.redoStack) == 0 {
		return false
	}
	last := d.redoStack[len(d.redoStack)-1]
	d.redoStack = d.redoStack[:len(d.redoStack)-1]
	d.Shapes = append(d.Shapes, last)
	d.Version++
	return true
}

// Snapshot returns a concurrent-safe copy of committed shapes and version counter.
func (d *Document) Snapshot() (shapes []Shape, canUndo bool, canRedo bool, version uint64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	shapes = append([]Shape(nil), d.Shapes...)
	return shapes, len(d.Shapes) > 0, len(d.redoStack) > 0, d.Version
}
