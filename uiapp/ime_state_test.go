package uiapp

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestIMEStateMatrix(t *testing.T) {
	previousLanguage := i18n.Current()
	t.Cleanup(func() { i18n.SetLanguage(previousLanguage) })
	i18n.SetLanguage(i18n.Chinese)
	c, br, _, _ := newTestController(t, nil)
	cases := []struct {
		name   string
		forced bool
		ascii  bool
		source bool
		want   IMEState
		label  string
	}{
		{"ascii", false, false, true, IMEStateASCII, "ABC"},
		{"system-cjk", false, false, false, IMEStateSystemCJK, "拼音 · 未接管"},
		{"takeover-cjk", true, false, true, IMEStateTakeoverCJK, "中式输入"},
		{"takeover-ascii", true, true, false, IMEStateTakeoverASCII, "中式输入 · 英"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			br.mu.Lock()
			br.inlineForced = tc.forced
			br.currentIME = host.InputSourceInfo{ID: tc.name, Name: tc.name, IsASCII: tc.source}
			br.mu.Unlock()
			c.UpdateInputEngineMode(tc.ascii)
			c.NotifyIMESwitch(host.InputSourceInfo{ID: tc.name, Name: tc.name, IsASCII: tc.source}, nil)
			if got := c.IMEState(); got != tc.want {
				t.Fatalf("IMEState = %v, want %v", got, tc.want)
			}
			if got := c.IMEState().Label(); got != tc.label {
				t.Fatalf("Label = %q, want %q", got, tc.label)
			}
		})
	}
}

func TestPillLabelFollowsIMEState(t *testing.T) {
	c, br, _, _ := newTestController(t, nil)
	var got string
	c.OnShowIMEPill = func(label string, _ *host.CursorAnchor) { got = label }
	br.mu.Lock()
	br.inlineForced = true
	br.currentIME = host.InputSourceInfo{ID: "pinyin", IsASCII: false}
	br.mu.Unlock()
	c.UpdateInputEngineMode(false)
	c.NotifyIMESwitch(host.InputSourceInfo{ID: "pinyin", IsASCII: false}, nil)
	if got != c.IMEState().Label() {
		t.Fatalf("pill label = %q, IMEState label = %q", got, c.IMEState().Label())
	}
	br.mu.Lock()
	br.inlineForced = false
	br.currentIME = host.InputSourceInfo{ID: "abc", IsASCII: true}
	br.mu.Unlock()
	c.UpdateInputEngineMode(true)
	c.NotifyIMESwitch(host.InputSourceInfo{ID: "abc", IsASCII: true}, nil)
	if got != c.IMEState().Label() {
		t.Fatalf("pill label after state change = %q, IMEState label = %q", got, c.IMEState().Label())
	}
}
