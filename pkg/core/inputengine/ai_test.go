package inputengine

import (
	"reflect"
	"strings"
	"testing"
)

func TestShouldQueryAI(t *testing.T) {
	for raw, want := range map[string]bool{"n": false, "ni": true, "ni'hao": true, "nI": false, "": false} {
		if got := ShouldQueryAI(raw); got != want {
			t.Fatalf("%q: %v", raw, got)
		}
	}
}

func TestAIPromptPrivacy(t *testing.T) {
	p := AIPrompt("nihao", "WeChat.exe")
	if !strings.Contains(p, "Pinyin: nihao") || !strings.Contains(p, "WeChat.exe") {
		t.Fatal(p)
	}
	if strings.Contains(AIPrompt("nihao", ""), "Application") {
		t.Fatal("empty app name leaked a field")
	}
}

func TestParseAICandidates(t *testing.T) {
	reply := "1. 你好\n2) “你号”\n- 拟好\nnihao\n你好\n• 泥嚎、你好吗\n6. 多余一\n7. 多余二"
	got := ParseAICandidates(reply)
	want := []string{"你好", "你号", "拟好", "泥嚎", "你好吗"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if len(ParseAICandidates("")) != 0 || len(ParseAICandidates("sorry, I can't")) != 0 {
		t.Fatal("garbage parsed")
	}
}
