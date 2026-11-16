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
	face := ui.Box(c).MinWidth(space18).Height(space18).Padding(space1-space1, space4).Radius(radiusKeycap).Center()
	fg := t.TextMuted
	switch tone {
	case keyToneAccent:
		fg = accentInk(t, surfaceBase(t))
		face.Background(t.Accent.Alpha(space2/space8)).Border(space1, t.Accent.Alpha(space4/space8))
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

func kitSegmentedPicker(c *ui.Context, t *ui.Theme, options []string, current string) (string, bool) {
	if len(options) == 0 {
		return current, false
	}
	selected := 0
	for i, option := range options {
		if option == current {
			selected = i
			break
		}
	}
	parts := ui.SegmentedBase(c, &selected, len(options))
	track := parts.Track.Radius(radiusChip).Border(space1, hairline(t)).Background(tileFill(t))
	_ = track
	for i, option := range options {
		segment := parts.Segment(i).Padding(space4, space8).Radius(radiusChip)
		if i == selected {
			segment.Background(t.Accent).TextColor(t.AccentText)
		} else {
			segment.TextColor(t.TextMuted)
		}
		segment.Children(func() { ui.Text(c, option).FontSize(typeLabel).SingleLine() })
	}
	return options[selected], parts.Track.Changed()
}
