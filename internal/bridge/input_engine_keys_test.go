package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/ime"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestEngineKeyForVK(t *testing.T) {
	cases := []struct {
		vk    uint16
		text  string
		chord bool
		kind  inputengine.KeyKind
		ok    bool
	}{
		{0x41, "a", false, inputengine.KeyLetter, true},
		{0x41, "A", false, inputengine.KeyOther, true}, // Shift/Caps: upper-case is literal
		{0x41, "a", true, inputengine.KeyLetter, true}, // Ctrl+A: letter but Chord set
		{0x31, "1", false, inputengine.KeyDigit, true},
		{0x31, "!", false, inputengine.KeyOther, true},
		{0xDE, "'", false, inputengine.KeyApostrophe, true},
		{0xBD, "-", false, inputengine.KeyPagePrev, true},
		{0xBB, "=", false, inputengine.KeyPageNext, true},
		{0xBC, ",", false, inputengine.KeyOther, true},
		{0x20, " ", false, inputengine.KeySpace, true},
		{0x0D, "\r", false, inputengine.KeyEnter, true},
		{0x08, "\b", false, inputengine.KeyBackspace, true},
		{0x1B, "\x1b", false, inputengine.KeyEscape, true},
		{0x25, "", false, inputengine.KeyNavigation, true}, // Left
		{0x26, "", false, inputengine.KeyNavigation, true}, // Up
		{0x28, "", false, inputengine.KeyNavigation, true}, // Down
		{0x09, "\t", false, inputengine.KeyNavigation, true},
		{0x74, "", false, inputengine.KeyNavigation, true}, // F5
		{0xDC, "", false, 0, false},                        // dead key / no text
	}
	for i, c := range cases {
		k, ok := engineKeyForVK(c.vk, c.text, c.chord)
		if ok != c.ok || (ok && k.Kind != c.kind) || k.Chord != c.chord {
			t.Fatalf("case %d (vk=%#x %q): got %+v ok=%v", i, c.vk, c.text, k, ok)
		}
	}
}

func TestInputEngineInlineRuneMapping(t *testing.T) {
	cases := []struct {
		name  string
		run   string
		kind  inputengine.KeyKind
		chord bool
		dir   inputengine.NavDirection
	}{
		{name: "quote", run: `"`, kind: inputengine.KeyOther},
		{name: "minus", run: "_", kind: inputengine.KeyOther},
		{name: ",", run: "'", kind: inputengine.KeyApostrophe},
		{name: "comma", run: ",", kind: inputengine.KeyOther},
		{name: "period", run: ".", kind: inputengine.KeyOther},
		{name: "equal", run: "=", kind: inputengine.KeyPageNext},
		{name: "space", run: " ", kind: inputengine.KeySpace},
		{name: "left", run: "", kind: inputengine.KeyNavigation},
		{name: "up", run: "", kind: inputengine.KeyNavigation, dir: inputengine.NavUp},
		{name: "down", run: "", kind: inputengine.KeyNavigation, dir: inputengine.NavDown},
		{name: "unknown", run: "", kind: inputengine.KeyOther},
		{name: "unknown chord", run: "", kind: inputengine.KeyNavigation, chord: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mods := []string(nil)
			if tc.chord {
				mods = []string{"cmd"}
			}
			k, ok := inputEngineKeyForInline(host.InlineKeyEvent{KeyName: tc.name, Rune: tc.run, Modifiers: mods})
			if !ok || k.Kind != tc.kind || k.NavDir != tc.dir || k.Chord != tc.chord {
				t.Fatalf("inline %s/%q chord=%v = %+v, ok=%v; want %v", tc.name, tc.run, tc.chord, k, ok, tc.kind)
			}
		})
	}
}

func TestInputEngineRuneKindsAgreeAcrossPlatforms(t *testing.T) {
	cases := []struct {
		name string
		run  string
		vk   uint16
	}{
		{name: "quote", run: `"`, vk: 0xDE},
		{name: "minus", run: "_", vk: 0xBD},
		{name: "apostrophe", run: "'", vk: 0xDE},
		{name: "comma", run: ",", vk: 0xBC},
		{name: "period", run: ".", vk: 0xBE},
		{name: "equal", run: "=", vk: 0xBB},
		{name: "space", run: " ", vk: ieVKSpace},
		{name: "left", run: "", vk: 0x25},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inline, inlineOK := inputEngineKeyForInline(host.InlineKeyEvent{KeyName: tc.name, Rune: tc.run})
			windows, windowsOK := engineKeyForVK(tc.vk, tc.run, false)
			if !inlineOK || !windowsOK || inline.Kind != windows.Kind {
				t.Fatalf("rune %q: inline=%+v/%v windows=%+v/%v", tc.run, inline, inlineOK, windows, windowsOK)
			}
		})
	}
}

func TestUTF16UnitsAndEligibility(t *testing.T) {
	if got := utf16Units("你a😀"); len(got) != 4 || got[0] != 0x4F60 || got[2] != 0xD83D || got[3] != 0xDE00 {
		t.Fatalf("utf16 = %x", got)
	}
	if !inputEngineEligible(false, false, false) {
		t.Fatal("plain ASCII field must be eligible")
	}
	for _, c := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if inputEngineEligible(c[0], c[1], c[2]) {
			t.Fatalf("must not take over: native=%v secure=%v self=%v", c[0], c[1], c[2])
		}
	}
}

func TestInputEngineConfigPlumbing(t *testing.T) {
	// Default config has Enabled=false -> nil engine.
	def := config.DefaultInputEngineConfig()
	if def.Enabled {
		t.Fatal("default [input_engine] must stay disabled")
	}
	if eng := newInputEngineFromConfig(def, t.TempDir(), nil); eng != nil {
		t.Fatal("disabled config must not instantiate engine")
	}
	for _, mode := range []string{"native_imk", "off"} {
		cfg := def
		cfg.Enabled = true
		cfg.Mode = mode
		if eng := newInputEngineFromConfig(cfg, t.TempDir(), nil); eng != nil {
			t.Fatalf("mode %q must not instantiate inline hook engine", mode)
		}
	}

	dir := t.TempDir()
	dictPath := filepath.Join(dir, "bridge_custom.dict.yaml")
	if err := os.WriteFile(dictPath, []byte("---\nname: bridge\n...\n桥接热词\tqiao jie re ci\t999999\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := def
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	cfg.Scheme = "xiaohe"
	cfg.CandidateCount = 1 // clamped to 3
	cfg.FuzzyPinyin = []string{"z=zh"}
	cfg.CustomDictPaths = []string{dictPath}

	eng := newInputEngineFromConfig(cfg, filepath.Join(dir, "cache"), nil)
	if eng == nil {
		t.Fatal("enabled inline_hook config returned nil engine")
	}
	if eng.CandidateCount() != inputengine.MinCandidateCount {
		t.Fatalf("clamped CandidateCount = %d, want %d", eng.CandidateCount(), inputengine.MinCandidateCount)
	}
	// Shuangpin ("nihc" -> 你好) works through the wired engine.
	for _, r := range "nihc" {
		eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	if v := eng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "你好" {
		t.Fatalf("wired xiaohe nihc = %+v", v.Candidates)
	}
	eng.Reset()

	// Custom Rime dict ("qcjpreci" -> qiao'jie're'ci -> 桥接热词) works through the wired engine.
	for _, r := range "qcjpreci" {
		eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	if v := eng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "桥接热词" {
		t.Fatalf("wired custom dict in xiaohe = %+v (preedit=%q)", v.Candidates, v.Preedit)
	}
}

type bridgeTestFreq struct {
	mu     sync.RWMutex
	counts map[[2]string]int32
	bigram map[[2]string]int32
}

func newBridgeTestFreq() *bridgeTestFreq {
	return &bridgeTestFreq{
		counts: make(map[[2]string]int32),
		bigram: make(map[[2]string]int32),
	}
}

func (f *bridgeTestFreq) record(c InputEngineCommit) {
	f.mu.Lock()
	f.counts[[2]string{c.PinyinKey, c.Word}]++
	if c.PrevWord != "" {
		f.bigram[[2]string{c.PrevWord, c.Word}]++
	}
	f.mu.Unlock()
}

func (f *bridgeTestFreq) LookupBoost(pinyinKey, word, prevWord string) int32 {
	f.mu.RLock()
	b := f.counts[[2]string{pinyinKey, word}]
	if prevWord != "" {
		b += f.bigram[[2]string{prevWord, word}] * 2
	}
	f.mu.RUnlock()
	return b
}

func (f *bridgeTestFreq) CustomWordsForPrefix(prefix string) []ime.UserCandidate {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []ime.UserCandidate
	for k, v := range f.counts {
		if strings.HasPrefix(k[0], prefix) {
			out = append(out, ime.UserCandidate{Word: k[1], Frequency: v})
		}
	}
	return out
}
func (f *bridgeTestFreq) BigramWordsForPrefix(string, string) []ime.UserCandidate { return nil }

func TestInputEngineUserLearningConfigAndCommitTracker(t *testing.T) {
	freq := newBridgeTestFreq()
	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"

	// user_learning=false forces UserFrequency=nil even when a provider is passed.
	cfgOff := cfg
	cfgOff.UserLearning = false
	optsOff, ok := inputEngineOptionsFromConfig(cfgOff, t.TempDir(), freq)
	if !ok || optsOff.UserFrequency != nil {
		t.Fatalf("user_learning=false must force UserFrequency=nil, got ok=%v uf=%v", ok, optsOff.UserFrequency)
	}

	// user_learning=true wires UserFrequency into inputengine.Options.
	optsOn, ok := inputEngineOptionsFromConfig(cfg, t.TempDir(), freq)
	if !ok || optsOn.UserFrequency == nil {
		t.Fatalf("user_learning=true must wire UserFrequency, got ok=%v uf=%v", ok, optsOn.UserFrequency)
	}

	eng := newInputEngineFromConfig(cfg, t.TempDir(), freq)
	tracker := &inputEngineCommitTracker{}
	typeStr := func(s string) {
		for _, r := range s {
			tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyLetter, Rune: r}, true)
		}
	}

	// Baseline "shi" -> index 0 is "是"; find "时" and select it by digit.
	typeStr("shi")
	v := eng.View()
	shiIdx := -1
	for i, c := range v.Candidates {
		if c.Text == "时" {
			shiIdx = i
			break
		}
	}
	if shiIdx <= 0 {
		t.Fatalf("expected 时 after index 0 in baseline, got %+v", v.Candidates)
	}
	res := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyDigit, Rune: rune('1' + shiIdx)}, true)
	if res.Commit != "时" {
		t.Fatalf("expected commit 时, got %+v", res)
	}

	// Simulate partial commit ("nihaozhongguo" -> Space commits "你好", then Space commits "中国").
	tracker.resetPrev()
	typeStr("nihaozhongguo")
	res1 := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeySpace}, true)
	res2 := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeySpace}, true)
	if res1.Commit != "你好" || res2.Commit != "中国" {
		t.Fatalf("partial commit mismatch: %+v / %+v", res1, res2)
	}

	drained := tracker.drain(nil)
	if len(drained) != 3 {
		t.Fatalf("expected 3 commits, got %+v", drained)
	}
	// LatencyNs is measured on the hook thread, so compare the learning fields only.
	type learnFields struct{ PinyinKey, Word, PrevWord string }
	fields := func(c InputEngineCommit) learnFields {
		return learnFields{c.PinyinKey, c.Word, c.PrevWord}
	}
	if got, want := fields(drained[0]), (learnFields{"shi", "时", ""}); got != want {
		t.Fatalf("commit[0] = %+v, want %+v (raw %+v)", got, want, drained[0])
	}
	if got, want := fields(drained[1]), (learnFields{"nihao", "你好", ""}); got != want {
		t.Fatalf("commit[1] = %+v, want %+v", got, want)
	}
	if got, want := fields(drained[2]), (learnFields{"zhongguo", "中国", "你好"}); got != want {
		t.Fatalf("commit[2] = %+v, want %+v", got, want)
	}
	// Telemetry must be a plausible positive latency for every commit.
	for i, c := range drained {
		if c.LatencyNs <= 0 {
			t.Fatalf("commit[%d] has non-positive LatencyNs: %d", i, c.LatencyNs)
		}
	}

	// Persist drained commits into freq -> "时" now jumps to #0 on next composition.
	for _, c := range drained {
		freq.record(c)
	}
	typeStr("shi")
	if got := eng.View().Candidates; len(got) == 0 || got[0].Text != "时" {
		t.Fatalf("expected 时 lifted to #0 after commit learning, got %+v", got)
	}
	tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyEscape}, true)

	// learn=false records zero commits.
	typeStr("nihao")
	_ = tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeySpace}, false)
	if rem := tracker.drain(nil); len(rem) != 0 {
		t.Fatalf("learn=false must record no commits, got %+v", rem)
	}
}

func TestInputEngineCommitTrackerPreservesBigramOnArrowNavigation(t *testing.T) {
	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	eng := newInputEngineFromConfig(cfg, t.TempDir(), nil)
	tracker := &inputEngineCommitTracker{}
	typeStr := func(s string) {
		for _, r := range s {
			tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyLetter, Rune: r}, true)
		}
	}

	// 1. Commit first word "我" via Space (top-1 choice).
	typeStr("wo")
	res1 := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeySpace}, true)
	if res1.Commit == "" {
		t.Fatalf("expected first commit for wo, got %+v", res1)
	}
	firstWord := res1.Commit

	// 2. Compose "shi", navigate Down -> Up -> Down to select candidate #1, then Space.
	typeStr("shi")
	v := eng.View()
	if len(v.Candidates) < 2 {
		t.Fatalf("expected at least 2 candidates for shi, got %+v", v.Candidates)
	}
	wantSecond := v.Candidates[1].Text
	if nav := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyNavigation, NavDir: inputengine.NavDown}, true); !nav.Consume || !nav.Changed {
		t.Fatalf("Down navigation during composition = %+v", nav)
	}
	if nav := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyNavigation, NavDir: inputengine.NavUp}, true); !nav.Consume || !nav.Changed {
		t.Fatalf("Up navigation during composition = %+v", nav)
	}
	if nav := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyNavigation, NavDir: inputengine.NavDown}, true); !nav.Consume || !nav.Changed {
		t.Fatalf("second Down navigation during composition = %+v", nav)
	}
	res2 := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeySpace}, true)
	if res2.Commit != wantSecond {
		t.Fatalf("commit after Down+Space = %q, want %q", res2.Commit, wantSecond)
	}

	// 3. Navigation outside composition must reset prevWord so cursor jumps break bigrams.
	tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyNavigation}, true)
	typeStr("hao")
	res3 := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeySpace}, true)
	if res3.Commit == "" {
		t.Fatalf("expected third commit for hao, got %+v", res3)
	}

	// 4. Aborting active composition with non-directional navigation (e.g. Left/Home) also resets prevWord.
	typeStr("ni")
	tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyNavigation}, true)
	if eng.Raw() != "" {
		t.Fatalf("non-directional navigation must abort composition, raw=%q", eng.Raw())
	}
	typeStr("ma")
	res4 := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeySpace}, true)
	if res4.Commit == "" {
		t.Fatalf("expected fourth commit for ma, got %+v", res4)
	}

	drained := tracker.drain(nil)
	if len(drained) != 4 {
		t.Fatalf("expected 4 commits, got %+v", drained)
	}
	if !drained[0].FirstChoice {
		t.Fatalf("commit[0] should be FirstChoice=true, got %+v", drained[0])
	}
	if drained[1].PrevWord != firstWord {
		t.Fatalf("commit[1].PrevWord = %q, want %q (arrow navigation must not wipe bigram context)", drained[1].PrevWord, firstWord)
	}
	if drained[1].FirstChoice {
		t.Fatalf("commit[1] selected index 1 so FirstChoice must be false, got %+v", drained[1])
	}
	if drained[2].PrevWord != "" {
		t.Fatalf("commit[2].PrevWord = %q, want empty after out-of-composition navigation", drained[2].PrevWord)
	}
	if drained[3].PrevWord != "" {
		t.Fatalf("commit[3].PrevWord = %q, want empty after navigation-aborted composition", drained[3].PrevWord)
	}
}

func TestCommittedCandidateWordPunctuationAutoCommit(t *testing.T) {
	// 1. Leading AI candidate is skipped; first full-syllable local candidate is matched.
	viewWithAI := inputengine.View{
		Composing: true,
		Preedit:   "ni'hao",
		Candidates: []inputengine.Candidate{
			{Text: "AI预测", Consumed: 2, AI: true},
			{Text: "你好", Consumed: 2, AI: false},
		},
	}
	word, firstChoice, ok := committedCandidateWord(viewWithAI, inputengine.Key{Kind: inputengine.KeyOther, Rune: '，'}, "你好，", true)
	if !ok || word != "你好" || firstChoice {
		t.Fatalf("with leading AI: got (%q, %v, %v), want (你好, false, true)", word, firstChoice, ok)
	}

	// 2. Leading partial-syllable candidate is skipped; first full-syllable local candidate is matched.
	viewWithPartial := inputengine.View{
		Composing: true,
		Preedit:   "ni'hao",
		Candidates: []inputengine.Candidate{
			{Text: "你", Consumed: 1, AI: false},
			{Text: "你好", Consumed: 2, AI: false},
		},
	}
	word, firstChoice, ok = committedCandidateWord(viewWithPartial, inputengine.Key{Kind: inputengine.KeyOther, Rune: '.'}, "你好.", true)
	if !ok || word != "你好" || firstChoice {
		t.Fatalf("with leading partial: got (%q, %v, %v), want (你好, false, true)", word, firstChoice, ok)
	}

	// 3. No full-syllable non-AI candidate -> raw fallback is NOT misattributed as a candidate commit.
	viewNoFull := inputengine.View{
		Composing: true,
		Preedit:   "ni'hao",
		Candidates: []inputengine.Candidate{
			{Text: "AI预测", Consumed: 2, AI: true},
			{Text: "你", Consumed: 1, AI: false},
		},
	}
	if w, _, matched := committedCandidateWord(viewNoFull, inputengine.Key{Kind: inputengine.KeyOther, Rune: ','}, "nihao,", true); matched || w != "" {
		t.Fatalf("raw fallback must not match candidate, got (%q, %v)", w, matched)
	}

	// 4. End-to-end with Engine: AI candidate sits at index 0 for single-syllable "shi",
	// punctuation auto-commits local index 1 ("是") and tracker extracts it for learning.
	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	eng := newInputEngineFromConfig(cfg, t.TempDir(), nil)
	tracker := &inputEngineCommitTracker{}
	for _, r := range "shi" {
		tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyLetter, Rune: r}, true)
	}
	if !eng.SetAICandidates("shi", []string{"人工智能预测"}) {
		t.Fatal("SetAICandidates returned false")
	}
	pre := eng.View()
	if len(pre.Candidates) < 2 || !pre.Candidates[0].AI {
		t.Fatalf("expected AI candidate at index 0, got %+v", pre.Candidates)
	}
	wantLocal := pre.Candidates[1].Text
	res := tracker.handleKey(eng, inputengine.Key{Kind: inputengine.KeyOther, Rune: '，'}, true)
	if res.Commit != wantLocal+"，" {
		t.Fatalf("punctuation commit = %q, want %q", res.Commit, wantLocal+"，")
	}
	drained := tracker.drain(nil)
	if len(drained) != 1 || drained[0].PinyinKey != "shi" || drained[0].Word != wantLocal {
		t.Fatalf("drained commits = %+v, want single commit for %q", drained, wantLocal)
	}
}
