package uiapp

import (
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
		wCN, hCN := PreferredCandidatePanelSizeFor(v, cfg)
		v.ASCII = true
		wEN, hEN := PreferredCandidatePanelSizeFor(v, cfg)
		if wCN != wEN || hCN != hEN {
			t.Fatalf("%s mode indicator changed panel geometry: CN=%dx%d EN=%dx%d", orientation, wCN, hCN, wEN, hEN)
		}
		for ascii, want := range map[bool]string{false: "中", true: "英"} {
			v.ASCII = ascii
			tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, cfg) }, wCN, hCN)
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

	w1, h1 := s.Next(composing("shu", "输", "书", "数", "树", "熟"), cfg)
	if w1%candidateWidthStep != 0 && w1 != candidatePanelMaxWidth {
		t.Fatalf("width %d is not on a %dpx step", w1, candidateWidthStep)
	}
	wide, _ := s.Next(composing("shu'ru'fa", "输入法", "书如法", "输入", "舒", "输", "输入法候选窗"), cfg)
	if wide < w1 {
		t.Fatalf("width shrank while growing content: %d -> %d", w1, wide)
	}
	narrow, h3 := s.Next(composing("shu'ru'fa'h", "输入法"), cfg)
	if narrow != wide {
		t.Fatalf("width shrank within one composition: %d -> %d", wide, narrow)
	}
	if h3 != h1 {
		t.Fatalf("horizontal height changed: %d -> %d", h1, h3)
	}

	// Composition over: the next one starts from its own content size.
	s.Next(inputengine.View{}, cfg)
	fresh, _ := s.Next(composing("a", "啊"), cfg)
	if fresh >= wide {
		t.Fatalf("a new composition kept the old width %d (got %d)", wide, fresh)
	}
}

func TestCandidatePanelSizerVerticalAndCaps(t *testing.T) {
	cfg := config.DefaultAppearanceConfig()
	cfg.CandidateBarOrientation = "vertical"
	var s CandidatePanelSizer

	_, tall := s.Next(composing("ni", "你", "呢", "泥", "尼", "拟", "逆", "腻"), cfg)
	_, short := s.Next(composing("nih", "你好"), cfg)
	if short != tall {
		t.Fatalf("vertical height shrank within a composition: %d -> %d", tall, short)
	}

	s.Reset()
	long := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		long = append(long, "很长很长很长很长很长很长很长的候选")
	}
	w, _ := s.Next(composing("hen", long...), cfg)
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
