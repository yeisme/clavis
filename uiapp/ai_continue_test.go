package uiapp

import (
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/host"
)

// Models add numbering, quotes and commentary even when told not to; the parser must clean
// those rather than pass them through into the user's document.
func TestParseAIContinuationCleansModelNoise(t *testing.T) {
	reply := "1. 不过预算部分需要再确认\n" +
		"2) \"整体方向没问题\"\n" +
		"- 我今晚整理一份风险清单\n" +
		"以上是三个选项\n"
	got := ParseAIContinuation(reply)
	if len(got) != 3 {
		t.Fatalf("got %d options: %v", len(got), got)
	}
	for _, want := range []string{"不过预算部分需要再确认", "整体方向没问题", "我今晚整理一份风险清单"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
}

func TestParseAIContinuationDropsDuplicatesAndEmpty(t *testing.T) {
	got := ParseAIContinuation("\n\n重复一句\n重复一句\n\n")
	if len(got) != 1 || got[0] != "重复一句" {
		t.Fatalf("got %v", got)
	}
}

func TestParseAIContinuationCapsAtThree(t *testing.T) {
	got := ParseAIContinuation("一\n二\n三\n四\n五\n")
	if len(got) != aiContinueOptions {
		t.Fatalf("got %d, want %d", len(got), aiContinueOptions)
	}
}

// Only text before the caret matters, and only the tail: continuation is about what comes next.
func TestTailBeforeCaretUsesTailOnly(t *testing.T) {
	long := strings.Repeat("字", 500)
	snap := host.EditableFieldSnapshot{Value: long + "后面的内容", SelectionEnd: len(long)}
	got := tailBeforeCaret(snap)
	if n := len([]rune(got)); n > aiContinueContextChars {
		t.Fatalf("context is %d runes, want <= %d", n, aiContinueContextChars)
	}
	if strings.Contains(got, "后面的内容") {
		t.Fatal("text after the caret must not be sent")
	}
}

func TestAIContinuePromptAsksForNoNumbering(t *testing.T) {
	p := AIContinuePrompt("这个方案我看了，")
	if !strings.Contains(p, "no numbering") || !strings.Contains(p, "这个方案我看了，") {
		t.Fatalf("prompt missing constraints or context:\n%s", p)
	}
}
