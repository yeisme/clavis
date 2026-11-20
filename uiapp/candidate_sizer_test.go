package uiapp

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func composing(preedit string, words ...string) inputengine.View {
	v := inputengine.View{Composing: true, Preedit: preedit}
	for _, w := range words {
		v.Candidates = append(v.Candidates, inputengine.Candidate{Text: w})
	}
	return v
}

// TestCandidatePanelSizerOnlyGrowsWithinComposition pins the anti-jitter
// rule: typing more pinyin may shorten the candidates, but the panel keeps
// its widest size (in 32px buckets) until the composition ends.
func TestCandidateModeLabel(t *testing.T) {
	if got := candidateModeLabel(false); got != "中" {
		t.Fatalf("non-ASCII mode label = %q, want 中", got)
	}
	if got := candidateModeLabel(true); got != "英" {
		t.Fatalf("ASCII mode label = %q, want 英", got)
	}

	for _, orientation := range []string{"horizontal", "vertical"} {
		cfg := config.DefaultAppearanceConfig()
		cfg.CandidateBarOrientation = orientation
		v := composing("ni", "你", "呢")
		wCN, hCN := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
		v.ASCII = true
		wEN, hEN := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
		if wCN != wEN || hCN != hEN {
			t.Fatalf("%s mode indicator changed panel geometry: CN=%dx%d EN=%dx%d", orientation, wCN, hCN, wEN, hEN)
		}
		for ascii, want := range map[bool]string{false: "中", true: "英"} {
			v.ASCII = ascii
			tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, panelOf(cfg)) }, wCN, hCN)
			tt.Frame()
			if !tt.HasText(want) {
				t.Fatalf("%s ASCII=%v frame missing mode label %q: texts=%v", orientation, ascii, want, tt.Texts())
			}
		}
	}
}

func TestCandidatePanelSizerOnlyGrowsWithinComposition(t *testing.T) {
	cfg := config.DefaultAppearanceConfig()
	cfg.CandidateBarOrientation = "horizontal"
	var s CandidatePanelSizer

	w1, h1 := s.Next(composing("shu", "输", "书", "数", "树", "熟"), panelOf(cfg))
	if w1%candidateWidthStep != 0 && w1 != candidatePanelMaxWidth {
		t.Fatalf("width %d is not on a %dpx step", w1, candidateWidthStep)
	}
	wide, _ := s.Next(composing("shu'ru'fa", "输入法", "书如法", "输入", "舒", "输", "输入法候选窗"), panelOf(cfg))
	if wide < w1 {
		t.Fatalf("width shrank while growing content: %d -> %d", w1, wide)
	}
	narrow, h3 := s.Next(composing("shu'ru'fa'h", "输入法"), panelOf(cfg))
	if narrow != wide {
		t.Fatalf("width shrank within one composition: %d -> %d", wide, narrow)
	}
	if h3 != h1 {
		t.Fatalf("horizontal height changed: %d -> %d", h1, h3)
	}

	// Composition over: the next one starts from its own content size.
	s.Next(inputengine.View{}, panelOf(cfg))
	fresh, _ := s.Next(composing("a", "啊"), panelOf(cfg))
	if fresh >= wide {
		t.Fatalf("a new composition kept the old width %d (got %d)", wide, fresh)
	}
}

func TestCandidatePanelSizerVerticalAndCaps(t *testing.T) {
	cfg := config.DefaultAppearanceConfig()
	cfg.CandidateBarOrientation = "vertical"
	var s CandidatePanelSizer

	_, tall := s.Next(composing("ni", "你", "呢", "泥", "尼", "拟", "逆", "腻"), panelOf(cfg))
	_, short := s.Next(composing("nih", "你好"), panelOf(cfg))
	if short != tall {
		t.Fatalf("vertical height shrank within a composition: %d -> %d", tall, short)
	}

	s.Reset()
	long := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		long = append(long, "很长很长很长很长很长很长很长的候选")
	}
	w, _ := s.Next(composing("hen", long...), panelOf(cfg))
	if w > candVertMaxWidth {
		t.Fatalf("width %d exceeds the vertical cap %d", w, candVertMaxWidth)
	}
	if got := roundUpTo(97, 32); got != 128 {
		t.Fatalf("roundUpTo(97,32) = %d", got)
	}
	if got := roundUpTo(128, 32); got != 128 {
		t.Fatalf("roundUpTo(128,32) = %d", got)
	}
}

func typedView(raw string) inputengine.View {
	e := inputengine.New(inputengine.Options{})
	for _, r := range raw {
		e.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	return e.View()
}

// The body is a short line of text: real first pages sit in the 360–480 band (they were
// 608–640 wide with the 760 cap), and nothing on the page can push past the cap.
func TestCandidatePanelWidthBand(t *testing.T) {
	if candidatePanelMaxWidth < 360 || candidatePanelMaxWidth > 480 || candidatePanelMaxWidth%candidateWidthStep != 0 {
		t.Fatalf("horizontal cap %d: want a %dpx step within 360..480", candidatePanelMaxWidth, candidateWidthStep)
	}
	cfg := config.DefaultAppearanceConfig()
	cfg.CandidateBarOrientation = "horizontal"
	for _, tc := range []struct {
		raw          string
		natural, win int
	}{
		// A1 promotes the whole-input sentence onto page 1, so the widest cell is now a
		// sentence ("也数根" / "我是也数根") instead of a single character. The page grows a
		// little but stays inside the 360..480 band; before A1 these were 354 (both inputs).
		// The page indicator ("› 1/9") is now measured (31px) instead of an 18px box it
		// overflowed, so the natural widths are 13px wider; the windows did not change.
		// Widths moved again when the common dictionary landed: yeshugen now offers 页数/也说
		// (two-character words) instead of only single characters, so its natural width grew
		// by a cell. All four still sit inside the 360..480 band and the 448 cap.
		{"yeshugen", 390, 416},
		{"nihao", 390, 416},
		{"woshiyeshugen", 420, 448},
		{"zhonghuarenmingongheguo", 448, 448},
	} {
		v := typedView(tc.raw)
		var s CandidatePanelSizer
		w, h := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
		sw, sh := s.Next(v, panelOf(cfg))
		if w != tc.natural || sw != tc.win || h != candidatePanelHeight || sh != candidatePanelHeight {
			t.Errorf("%s: preferred %dx%d, window %dx%d; want %dx%d and %dx%d",
				tc.raw, w, h, sw, sh, tc.natural, candidatePanelHeight, tc.win, candidatePanelHeight)
		}
	}
}

// However long the pinyin gets, the panel keeps the width its candidates need: the preedit
// is cut in the middle and the 中/英 badge stays whole and inside.
func TestCandidatePanelLongPreeditDoesNotWiden(t *testing.T) {
	long := strings.Repeat("wo'shi'ye'shu'gen'", 6) + "a"
	for _, orientation := range []string{"horizontal", "vertical"} {
		cfg := config.DefaultAppearanceConfig()
		cfg.CandidateBarOrientation = orientation
		ws, hs := PreferredCandidatePanelSizeFor(composing("a", "啊", "阿"), panelOf(cfg))
		v := composing(long, "啊", "阿")
		w, h := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
		if w != ws || h != hs {
			t.Fatalf("%s: long preedit resized the panel %dx%d -> %dx%d", orientation, ws, hs, w, h)
		}
		tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, panelOf(cfg)) }, w, h)
		tt.Frame()
		pre, ok := tt.Find(long)
		badge, okBadge := tt.Find("中")
		if !ok || !okBadge {
			t.Fatalf("%s: preedit or badge missing: %v", orientation, tt.Texts())
		}
		if pre.X+pre.W > badge.X+0.5 || badge.X+badge.W > float32(w) {
			t.Fatalf("%s: preedit %+v runs into badge %+v (panel width %d)", orientation, pre, badge, w)
		}
	}
}

// A page wider than the cap cuts its longest candidates to one common width; the short
// ones (a two-character word, single characters) keep every character.
func TestCandidatePanelOverflowCutsLongestFirst(t *testing.T) {
	cfg := config.DefaultAppearanceConfig()
	cfg.CandidateBarOrientation = "horizontal"
	v := composing("zhong'hua", "中华人民共和国万岁", "中华人民共和国", "中华人民", "中华", "中", "种", "重")
	v.HasNext = true
	v.ASCII = true // badge 英, so the candidate 中 is the only 中 on screen
	w, h := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
	if w != candidatePanelMaxWidth {
		t.Fatalf("overflowing page width = %d, want the cap %d", w, candidatePanelMaxWidth)
	}
	// The 9-, 7- and 4-character words are cut to one width; 中华 and the singles are not.
	textCap := candidateTextCap(v)
	if textCap < candidateTextWidth("中华") || textCap >= candidateTextWidth("中华人民") {
		t.Fatalf("text cap = %d, want within [%d, %d)", textCap, candidateTextWidth("中华"), candidateTextWidth("中华人民"))
	}
	tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, panelOf(cfg)) }, w, h)
	tt.Frame()
	for _, cand := range v.Candidates {
		r, ok := tt.Find(cand.Text)
		if !ok {
			t.Fatalf("candidate %q missing: %v", cand.Text, tt.Texts())
		}
		assertRectWithin(t, cand.Text, r, w, h)
		chars := float32(len([]rune(cand.Text)))
		if full := chars * 14; candidateTextWidth(cand.Text) <= textCap && r.W < full {
			t.Fatalf("%q was cut to %.1f although it fits the cap %d", cand.Text, r.W, textCap)
		}
		if r.W > float32(textCap)+0.5 {
			t.Fatalf("%q is %.1f wide, over the cap %d", cand.Text, r.W, textCap)
		}
	}
	// An AI suggestion that overflows is the only one cut.
	ai := composing("shu'ru'fa", "输入法", "书如法", "输入", "舒", "输")
	ai.Candidates = append(ai.Candidates, inputengine.Candidate{Text: "输入法候选窗", AI: true})
	ai.HasPrev, ai.HasNext = true, true
	if got := candidateTextCap(ai); got < candidateTextWidth("书如法") || got >= candidateTextWidth("输入法候选窗") {
		t.Fatalf("AI page text cap = %d, want only 输入法候选窗 cut", got)
	}
}

// The chosen candidate is a solid accent fill (not a faint tint), in both orientations;
// the others have none.
func TestCandidateSelectionIsSolidAccent(t *testing.T) {
	for _, orientation := range []string{"horizontal", "vertical"} {
		cfg := config.DefaultAppearanceConfig()
		cfg.CandidateBarOrientation = orientation
		v := composing("ni'hao", "你好", "你", "尼", "泥")
		v.Selected = 2
		w, h := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
		var accent ui.Color
		tt := ui.NewTester(func(c *ui.Context) {
			InputCandidateViewFor(c, v, panelOf(cfg))
			accent = c.Theme().Accent
		}, w, h)
		tt.Frame()
		img := tt.Image()
		beside := func(digit string) (r, g, b uint8) {
			rect, ok := tt.Find(digit)
			if !ok {
				t.Fatalf("%s: digit %s missing: %v", orientation, digit, tt.Texts())
			}
			px := img.RGBAAt(int(rect.X)-4, int(rect.Y+rect.H/2)) // the cell's own padding
			return px.R, px.G, px.B
		}
		near := func(a, b uint8) bool { d := int(a) - int(b); return d >= -2 && d <= 2 }
		if r, g, b := beside("3"); !near(r, accent.R) || !near(g, accent.G) || !near(b, accent.B) {
			t.Fatalf("%s: chosen cell = %d,%d,%d, want the accent %d,%d,%d", orientation, r, g, b, accent.R, accent.G, accent.B)
		}
		if r, g, b := beside("1"); near(r, accent.R) && near(g, accent.G) && near(b, accent.B) {
			t.Fatalf("%s: an unchosen cell is filled with the accent", orientation)
		}
	}
}
