package uiapp

import (
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

type interactionState struct {
	Hovered  bool
	Pressed  bool
	Focused  bool
	Selected bool
}

func kitFocusRing(t *ui.Theme) ui.Color {
	return readableOn(t.Accent, t.Text, t.Surface, minGlyphContrast)
}

func kitInteractive(c *ui.Context, t *ui.Theme, el ui.Element, st interactionState, p motionProfile) ui.Element {
	_ = c
	if st.Hovered {
		el.Background(selectionFill(t))
	}
	if st.Selected {
		el.Background(selectionFill(t)).Border(space1, selectionEdge(t))
	}
	if st.Focused {
		el.Border(space2, kitFocusRing(t))
	}
	if st.Pressed && p.MotionEnabled() {
		el.Scale(0.985, 0.985)
	}
	return el
}

func kitRow(c *ui.Context, t *ui.Theme, st interactionState, p motionProfile, body func()) ui.Element {
	row := ui.Row(c).Padding(space6, space8).Radius(radiusRow).Gap(space10).
		AlignItems(ui.Center).FillWidth()
	row = kitInteractive(c, t, row, st, p)
	row.Children(body)
	return row
}

func kitControlRow(c *ui.Context, t *ui.Theme, label string, body func()) {
	ui.Row(c).FillWidth().Height(controlHeight).AlignItems(ui.Center).Gap(space8).Children(func() {
		ui.Text(c, label).FontSize(typeLabel).TextColor(t.TextMuted).SingleLine().Grow(1)
		if body != nil {
			body()
		}
	})
}

func kitKeycap(c *ui.Context, t *ui.Theme, label string, tone keyTone) ui.Element {
	// Alpha values are opacity, not spacing: expressing them as spaceN/spaceM
	// would make a keycap's tint change if the spacing ramp ever moved.
	const (
		keycapAccentFill   float32 = 0.16
		keycapAccentBorder float32 = alphaHalf
	)
	face := ui.Box(c).MinWidth(space18).Height(space18).Padding(space0, space4).Radius(radiusKeycap).Center()
	fg := t.TextMuted
	switch tone {
	case keyToneAccent:
		fg = accentInk(t, surfaceBase(t))
		face.Background(t.Accent.Alpha(keycapAccentFill)).Border(space1, t.Accent.Alpha(keycapAccentBorder))
	case keyToneQuiet:
		face.Background(tileFill(t))
	default:
		face.Background(keycapFill(t)).Border(space1, keycapEdge(t)).Shadow(0, space1, 0, 0, keycapLip(t))
	}
	size := typeMicro
	if utf8.RuneCountInString(label) == 1 {
		size = typeMeta
	}
	face.Children(func() {
		ui.Text(c, label).FontSize(size).FontWeight(600).TextColor(fg).SingleLine()
	})
	return face
}

func kitIconTile(c *ui.Context, t *ui.Theme, icon string, selected bool) ui.Element {
	tile := ui.Box(c).Size(rowIconTile, rowIconTile).Radius(radiusTile).Center()
	color := t.TextMuted
	if selected {
		tile.Background(selectedTileFill(t))
		color = selectedTileGlyph(t)
	} else {
		tile.Background(tileFill(t))
	}
	tile.Children(func() { renderSemanticIcon(c, icon, rowIconSize, color) })
	return tile
}

// kitSegmentedPicker renders a segmented choice and returns the element so the
// caller can place it in a layout slot. Returning only the value forced callers
// to discard the element and substitute a placeholder, which is how the control
// ended up floating outside its right-aligned slot.
func kitSegmentedPicker(c *ui.Context, t *ui.Theme, options []string, current string) (ui.Element, string, bool) {
	if len(options) == 0 {
		return ui.Box(c).Width(space1).Height(space1), current, false
	}
	selected := 0
	for i, option := range options {
		if option == current {
			selected = i
			break
		}
	}
	parts := ui.SegmentedBase(c, &selected, len(options))
	// Segments are built *in* the track (see ui.SegmentedBase's contract).
	// Building them as siblings of the track let them float outside its well,
	// which is why the control read as loose text with one filled word.
	track := parts.Track.Padding(space2).Radius(radiusChip).
		Border(space1, hairline(t)).Background(t.Surface)
	track.Children(func() {
		for i, option := range options {
			segment := parts.Segment(i).Padding(space4, space8).Radius(radiusChip)
			if i == selected {
				segment.Background(t.Accent).TextColor(t.AccentText)
			} else {
				segment.TextColor(t.TextMuted)
			}
			segment.Children(func() { ui.Text(c, option).FontSize(typeLabel).SingleLine() })
		}
	})
	return track, options[selected], parts.Track.Changed()
}
