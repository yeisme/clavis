package uiapp

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestRelativeCopyTime(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	now := time.Date(2026, 11, 20, 15, 4, 0, 0, time.Local)
	midnight := time.Date(2026, 11, 20, 0, 10, 0, 0, time.Local)
	cases := []struct {
		at, now time.Time
		zh, en  string
	}{
		{now.Add(-20 * time.Second), now, "刚刚", "just now"},
		{now.Add(-5 * time.Minute), now, "5 分钟前", "5 min ago"},
		{now.Add(-3 * time.Hour), now, "3 小时前", "3 h ago"},
		// Twenty minutes ago is "20 minutes ago" even across midnight.
		{midnight.Add(-20 * time.Minute), midnight, "20 分钟前", "20 min ago"},
		{now.Add(-26 * time.Hour), now, "昨天", "yesterday"},
		{now.AddDate(0, 0, -4), now, "4 天前", "4 days ago"},
		{time.Date(2026, 9, 3, 9, 0, 0, 0, time.Local), now, "9月3日", "9/3"},
		{time.Date(2025, 9, 3, 9, 0, 0, 0, time.Local), now, "2025年9月3日", "9/3/2025"},
	}
	for _, tc := range cases {
		i18n.SetLanguage(i18n.Chinese)
		if got := relativeCopyTime(tc.at, tc.now); got != tc.zh {
			t.Errorf("zh relativeCopyTime(%v, %v) = %q, want %q", tc.at, tc.now, got, tc.zh)
		}
		i18n.SetLanguage(i18n.English)
		if got := relativeCopyTime(tc.at, tc.now); got != tc.en {
			t.Errorf("en relativeCopyTime(%v, %v) = %q, want %q", tc.at, tc.now, got, tc.en)
		}
	}
}

func TestClipboardRowMeta(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)
	now := time.Date(2026, 11, 20, 15, 4, 0, 0, time.Local)
	clip := func(app, content string, ago time.Duration) engine.OmniboxItem {
		return engine.OmniboxItem{
			Kind: engine.ItemClipboardEntry, Subtitle: "剪贴板",
			ClipboardSourceApp: app, ClipboardContent: content, ClipboardCopiedAt: now.Add(-ago),
		}
	}
	cases := []struct {
		name string
		item engine.OmniboxItem
		want string
	}{
		{"source and time", clip("Notes", "hello", 2*time.Minute), "Notes · 2 分钟前"},
		{"unknown source drops the Clipboard fallback", clip("", "hello", 18*time.Minute), "18 分钟前"},
		{"multi-line text says how much is hidden", clip("Terminal", "SELECT 1\nFROM t\nLIMIT 2;\n", 10*time.Second), "Terminal · 刚刚 · 3 行"},
		{"CRLF and blank edges", clip("", "\r\n a\r\nb \r\n", time.Hour), "1 小时前 · 2 行"},
		{"no metadata keeps the subtitle", engine.OmniboxItem{Kind: engine.ItemClipboardEntry, Subtitle: "剪贴板", ClipboardContent: "x"}, "剪贴板"},
	}
	media := clip("Photos", "img\nlines", 26*time.Hour)
	media.ClipboardMedia = &engine.ClipboardMedia{Kind: "image"}
	cases = append(cases, struct {
		name string
		item engine.OmniboxItem
		want string
	}{"media never counts lines", media, "Photos · 昨天"})
	for _, tc := range cases {
		if got := clipboardRowMeta(tc.item, now); got != tc.want {
			t.Errorf("%s: clipboardRowMeta = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestOmniboxSelectionIsEdgeBarNotFlood renders the clipboard tab for real
// and reads pixels: the chosen row carries a ~2px accent bar at its leading
// edge over a faint tint, resting rows carry none, the tint stays subtle,
// and title and secondary line keep 4.5:1 on the composited row.
func TestOmniboxSelectionIsEdgeBarNotFlood(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		theme := ui.LightTheme()
		if dark {
			name, theme = "dark", ui.DarkTheme()
		}
		t.Run(name, func(t *testing.T) {
			f := newInteractionFixture(t, nil)
			f.record(t, "first entry", "second entry", "third entry")
			f.ctrl.OpenOmnibox(config.TabClipboard, "")
			waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 3 }, "clipboard items")
			tt := f.omniboxTester(t)
			tt.SetDark(dark)
			// Let the capsule enter transition settle before measuring the
			// selection switch. The following frame must still move the bar
			// immediately with the keyboard selection.
			tt.Advance(300 * time.Millisecond)
			tt.Frame()
			tt.Key(0, ui.KeyDown) // newest first: "second entry" is chosen
			tt.Frame()
			img := tt.Image()

			// The list labels each row with its title, so Find resolves to the
			// row's box (≈rowHeightOmnibox tall), not to the title glyphs.
			chosen, ok := tt.Find("second entry")
			if !ok || chosen.H < 30 {
				t.Fatalf("chosen row not found as a row box: %+v ok=%v; texts: %q", chosen, ok, tt.Texts())
			}
			resting, ok := tt.Find("third entry")
			if !ok || resting.H < 30 {
				t.Fatalf("resting row not found as a row box: %+v", resting)
			}
			wantBar := rowSelectionBar(theme)
			rowX := int(chosen.X)
			barY := int(chosen.Y + chosen.H/2)
			barX, barW := findColorRun(img, barY, max(rowX-4, 0), rowX+12, wantBar)
			if barW == 0 {
				var seen []ui.Color
				for x := max(rowX-4, 0); x < rowX+12; x++ {
					seen = append(seen, rgbaAt(img, x, barY))
				}
				t.Fatalf("no selection bar (%v) at the chosen row's leading edge y=%d (selected=%d, row=%+v); pixels: %v",
					wantBar, barY, f.ctrl.State().SelectedIndex, chosen, seen)
			}
			if barW > 3 {
				t.Fatalf("selection bar is %dpx wide, want ~%vpx (a bar, not a fill)", barW, rowSelectionBarWidth)
			}
			if barX > rowX+2 {
				t.Fatalf("bar starts at x=%d, want flush with the row edge x≈%d", barX, rowX)
			}
			if _, w := findColorRun(img, int(resting.Y+resting.H/2), max(rowX-4, 0), rowX+12, wantBar); w != 0 {
				t.Fatalf("resting row also shows a selection bar")
			}
			// The bar is inset from the row's top and bottom: near the edges
			// the leading column is tint, not bar.
			for _, y := range []int{int(chosen.Y) + 3, int(chosen.Y+chosen.H) - 4} {
				if px := rgbaAt(img, barX, y); colorClose(px, wantBar) {
					t.Fatalf("bar reaches the row's edge (pixel %v at y=%d)", px, y)
				}
			}
			// Old style stroked the whole row with an accent hairline: the top
			// edge mid-row must now be plain tint. Sample the first pixel row
			// fully inside the box (its top may fall between pixels).
			topY := int(math.Ceil(float64(chosen.Y)))
			if top, mid := rgbaAt(img, rowX+int(chosen.W/2), topY), rgbaAt(img, rowX+int(chosen.W/2), topY+3); !colorClose(top, mid) {
				t.Fatalf("chosen row has an outline: top edge %v vs inside %v", top, mid)
			}

			tint := rgbaAt(img, barX+barW+3, barY)
			rest := rgbaAt(img, barX+barW+3, int(resting.Y+resting.H/2))
			t.Logf("bar x=%d w=%d color=%v; tint=%v resting=%v", barX, barW, rgbaAt(img, barX, barY), tint, rest)
			if r := contrastRatio(tint, rest); r > 1.2 {
				t.Fatalf("chosen-row tint %v vs resting %v differs %.2f:1 — a flood, not a faint tint", tint, rest, r)
			}
			if tint == rest {
				t.Fatalf("chosen row has no tint at all (%v)", tint)
			}
			for _, ck := range []struct {
				what string
				fg   ui.Color
				bg   ui.Color
			}{
				{"title on the composited chosen row", theme.Text, tint},
				{"secondary line on the composited chosen row", secondaryInk(theme), tint},
				{"secondary line on a composited resting row", secondaryInk(theme), rest},
			} {
				if r := contrastRatio(ck.fg, ck.bg); r < minTextContrast {
					t.Fatalf("%s: %.2f:1 < %.1f", ck.what, r, minTextContrast)
				} else {
					t.Logf("%s: %.2f:1", ck.what, r)
				}
			}
		})
	}
}

// findColorRun scans row y from x0 to x1 for the first run of pixels within
// a small tolerance of want and returns where it starts and how wide it is.
func findColorRun(img *image.RGBA, y, x0, x1 int, want ui.Color) (int, int) {
	start, width := -1, 0
	for x := x0; x < x1; x++ {
		if colorClose(rgbaAt(img, x, y), want) {
			if start < 0 {
				start = x
			}
			width++
		} else if start >= 0 {
			break
		}
	}
	return start, width
}

func rgbaAt(img *image.RGBA, x, y int) ui.Color {
	c := img.RGBAAt(x, y)
	return ui.RGBA(c.R, c.G, c.B, 1)
}

func colorClose(a, b ui.Color) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R)+d(a.G, b.G)+d(a.B, b.B) <= 24
}
