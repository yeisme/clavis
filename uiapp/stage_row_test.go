package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestStageRowFanGeometry(t *testing.T) {
	plan := stagePlan{Enabled3D: true, Perspective: 1000, RowSkew: 5}
	const selected = 3
	selectedSkew, selectedZ := stageRowPose(plan, selected, selected, nil, ui.Element{})
	if selectedSkew != 0 || selectedZ != 0 {
		t.Fatalf("selected pose = (%v, %v), want (0, 0)", selectedSkew, selectedZ)
	}
	for distance := 1; distance <= 3; distance++ {
		left, _ := stageRowPose(plan, selected-distance, selected, nil, ui.Element{})
		right, _ := stageRowPose(plan, selected+distance, selected, nil, ui.Element{})
		if left != -right {
			t.Fatalf("distance %d skew = %v/%v, want symmetric values", distance, left, right)
		}
		if left < -plan.RowSkew || left > plan.RowSkew || right < -plan.RowSkew || right > plan.RowSkew {
			t.Fatalf("distance %d skew exceeds clamp: %v/%v", distance, left, right)
		}
	}
	flat := plan
	flat.RowSkew = 0
	for i := 0; i < 7; i++ {
		skew, _ := stageRowPose(flat, i, selected, nil, ui.Element{})
		if skew != 0 {
			t.Fatalf("zero-skew row %d = %v, want 0", i, skew)
		}
	}
}

func TestStageRowFrozenIsFlat(t *testing.T) {
	plan := stagePlan{Enabled3D: true, Perspective: 1000, RowSkew: 5, Frozen: true}
	for i := 0; i < 6; i++ {
		got := stageRowTransform(nil, ui.Element{}, plan, i, 2)
		if got != (ui.Element{}) {
			t.Fatalf("frozen row %d was transformed", i)
		}
	}
	if stageRequestsAnimationFrames(plan) {
		t.Fatal("frozen row requested animation frames")
	}
}

func TestStageRowSelectedTransformIsIdentity(t *testing.T) {
	plan := stagePlan{Enabled3D: true, Perspective: 1000, RowSkew: 5}
	selected, _ := stageRowPose(plan, 2, 2, nil, ui.Element{})
	if selected != 0 {
		t.Fatalf("selected row skew = %v, want exactly 0", selected)
	}
	if _, z := stageRowPose(plan, 2, 2, nil, ui.Element{}); z != 0 {
		t.Fatalf("selected row z = %v, want exactly 0", z)
	}
}

func TestStageRowListRendersSpatialAndComposing(t *testing.T) {
	planCases := []struct {
		name string
		plan stagePlan
		dark bool
	}{
		{name: "light spatial", plan: stagePlan{Enabled3D: true, Perspective: 1000, RowSkew: 5}},
		{name: "dark spatial", plan: stagePlan{Enabled3D: true, Perspective: 1000, RowSkew: 5}, dark: true},
		{name: "composing flat", plan: stagePlan{Enabled3D: true, Perspective: 1000, RowSkew: 5, Frozen: true}},
	}
	for _, tc := range planCases {
		t.Run(tc.name, func(t *testing.T) {
			tester := ui.NewTester(func(c *ui.Context) {
				list := stageRowList(c, nil, tc.plan, 6, 2, func(i int) ui.Element {
					row := ui.Row(c).FillWidth().Height(36).Padding(4, 8).Background(c.Theme().Surface)
					row.Children(func() { ui.Text(c, "Result "+string(rune('0'+i))) })
					return row
				})
				list.FillWidth().Grow(1)
			}, 360, 300)
			tester.SetDark(tc.dark)
			tester.Frame()
			img := tester.Image()
			if img == nil || img.Bounds().Empty() {
				t.Fatal("stage row list produced an empty image")
			}
		})
	}
}
