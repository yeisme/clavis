package uiapp

import "github.com/egoist/mygo/ui"

// stageRowList adapts the callback-oriented ui.List builder to the stage's
// row transform. The ListState parameter is necessary because the Omnibox
// keeps selection and scroll state outside the list; the callback builds the
// complete row first and then transforms that same element. This keeps the
// row's fill, text, icon and hit geometry together rather than rotating a
// child layer independently.
func stageRowList(c *ui.Context, state *ui.ListState, plan stagePlan, count int, selected int, buildRow func(i int) ui.Element) ui.Element {
	return ui.List(c, state, count, func(i int) {
		if buildRow == nil {
			return
		}
		stageRowTransform(c, buildRow(i), plan, i, selected)
	})
}

// stageRowTransform applies the fan pose to a complete result row. Frozen,
// planar and disabled plans intentionally leave the element untouched: this
// is both the reduced-motion path and the IME-composition readability path.
// The selected row receives only the camera perspective, with no rotation or
// Z translation, so its glyphs remain crisp and its transform is identity in
// the row-pose dimensions.
func stageRowTransform(c *ui.Context, el ui.Element, plan stagePlan, index, selected int) ui.Element {
	// A zero RowSkew is the resolved representation of ObliqueRows=false.
	if plan.Frozen || !plan.Enabled3D || plan.RowSkew == 0 {
		return el
	}

	if index == selected {
		return el
	}
	skew, z := stageRowPose(plan, index, selected, c, el)
	return el.Perspective(plan.Perspective).RotateY(skew).TranslateZ(z)
}
