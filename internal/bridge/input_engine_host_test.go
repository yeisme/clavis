package bridge

import (
	"reflect"
	"testing"
	"unicode/utf16"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestInputEngineInlineMapping(t *testing.T) {
	cases := []struct {
		name  string
		ev    host.InlineKeyEvent
		kind  inputengine.KeyKind
		chord bool
	}{
		{"letter", host.InlineKeyEvent{KeyName: "n", Rune: "n", IsDown: true}, inputengine.KeyLetter, false},
		{"space", host.InlineKeyEvent{KeyName: "space", IsDown: true}, inputengine.KeySpace, false},
		{"apostrophe", host.InlineKeyEvent{KeyName: "quote", Rune: "'", IsDown: true}, inputengine.KeyApostrophe, false},
		{"chord", host.InlineKeyEvent{KeyName: "a", Rune: "a", Modifiers: []string{"cmd"}, IsDown: true}, inputengine.KeyLetter, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ok := inputEngineKeyForInline(tc.ev)
			if !ok || k.Kind != tc.kind || k.Chord != tc.chord {
				t.Fatalf("got %#v, ok=%v", k, ok)
			}
		})
	}
}

func TestInputEngineShiftTapMapping(t *testing.T) {
	keys, ok := inputEngineKeysForInline(host.InlineKeyEvent{KeyName: "shift_tap", IsDown: true})
	if !ok || len(keys) != 2 || keys[0].Kind != inputengine.KeyShiftDown || keys[1].Kind != inputengine.KeyShiftUp {
		t.Fatalf("got %#v, ok=%v", keys, ok)
	}
}

func TestInputEngineCommitChunking(t *testing.T) {
	var got [][]uint16
	input := "你好" + "世界" + "🙂"
	emitInputEngineCommit(input, func(units []uint16) { got = append(got, append([]uint16(nil), units...)) })
	if len(got) != 1 || string(utf16.Decode(got[0])) != input {
		t.Fatalf("unexpected chunks: %#v", got)
	}
	long := "界界界界界界界界界界界界界界界界界界界界界界"
	got = nil
	emitInputEngineCommit(long, func(units []uint16) { got = append(got, append([]uint16(nil), units...)) })
	for i, c := range got {
		if len(c) > 20 {
			t.Fatalf("chunk %d has %d units", i, len(c))
		}
	}
}

func TestInputEngineSharedStateChineseComposition(t *testing.T) {
	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled, cfg.Mode = true, "inline_hook"
	st := &inputEngineState{dirty: make(chan struct{}, 1)}
	changed, eng := st.configure(cfg)
	if !changed || eng == nil {
		t.Fatal("engine was not configured")
	}
	st.setEngine(eng)
	for _, r := range "nihao" {
		k, ok := inputEngineKeyForInline(host.InlineKeyEvent{KeyName: string(r), Rune: string(r), IsDown: true})
		if !ok {
			t.Fatalf("mapping %q", r)
		}
		res := st.commits.handleKey(eng, k, false)
		if !res.Consume {
			t.Fatalf("letter %q not consumed", r)
		}
	}
	k, _ := inputEngineKeyForInline(host.InlineKeyEvent{KeyName: "space", IsDown: true})
	res := st.commits.handleKey(eng, k, false)
	if res.Commit != "你好" {
		t.Fatalf("commit = %q", res.Commit)
	}
	var posted [][]uint16
	emitInputEngineCommit(res.Commit, func(units []uint16) { posted = append(posted, append([]uint16(nil), units...)) })
	if len(posted) != 1 || string(utf16.Decode(posted[0])) != "你好" {
		t.Fatalf("posted commit = %#v", posted)
	}
}

func TestInputEngineConfigStateDedupes(t *testing.T) {
	cfg := config.DefaultInputEngineConfig()
	st := &inputEngineState{dirty: make(chan struct{}, 1)}
	changed, _ := st.configure(cfg)
	if !changed {
		t.Fatal("first configure should change")
	}
	changed, _ = st.configure(cfg)
	if changed {
		t.Fatal("identical configure rebuilt state")
	}
	if !reflect.DeepEqual(st.lastCfg, cfg) {
		t.Fatal("config not retained")
	}
}
