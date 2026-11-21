package inputengine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yeisme/clavis/pkg/core/ime"
)

func TestShuangpinSchemesThroughHandleKey(t *testing.T) {
	for _, scheme := range []string{"xiaohe", "ziranma", "microsoft", "sogou", "pinyin_plus"} {
		e := New(Options{CandidateCount: 7, Scheme: scheme})
		typeLetters(e, "nihc")
		v := e.View()
		if len(v.Candidates) == 0 || v.Candidates[0].Text != "你好" {
			t.Fatalf("%s nihc candidates = %v (preedit=%q)", scheme, texts(v), v.Preedit)
		}
		if v.Preedit != "ni'hao" {
			t.Fatalf("%s nihc preedit = %q, want ni'hao", scheme, v.Preedit)
		}
		res := e.HandleKey(Key{Kind: KeySpace})
		if !res.Consume || res.Commit != "你好" || e.Raw() != "" {
			t.Fatalf("%s space commit = %+v raw=%q", scheme, res, e.Raw())
		}
	}

	// Xiaohe multi-word, ambiguous pairs, odd trailing initial, and partial commit.
	xh := New(Options{CandidateCount: 9, Scheme: "xiaohe"})
	typeLetters(xh, "v")
	if v := xh.View(); v.Preedit != "zh" || len(v.Candidates) == 0 {
		t.Fatalf("xiaohe trailing 'v' = preedit %q cands %v", v.Preedit, texts(v))
	}
	typeLetters(xh, "sgo") // "vsgo" -> zhong'guo
	if v := xh.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "中国" {
		t.Fatalf("xiaohe vsgo = %v (preedit=%q)", texts(v), v.Preedit)
	}
	xh.Reset()

	typeLetters(xh, "ulpn") // "ul" + "pn" -> shuang'pin
	if v := xh.View(); !slices.Contains(texts(v), "双拼") {
		t.Fatalf("xiaohe ulpn = %v (preedit=%q)", texts(v), v.Preedit)
	}
	xh.Reset()

	// Ambiguous 2-key pair "dc" covers both dao (到) and diao (掉).
	typeLetters(xh, "dc")
	dcCands := texts(xh.View())
	if !slices.Contains(dcCands, "到") || !slices.Contains(dcCands, "掉") {
		t.Fatalf("xiaohe dc expected both 到 and 掉, got %v", dcCands)
	}
	xh.Reset()

	// Partial commit in Shuangpin keeps the remaining Shuangpin pair intact.
	typeLetters(xh, "nihcvs") // ni'hao + zhong (no 3-syllable phrase)
	idx := slices.IndexFunc(xh.View().Candidates, func(c Candidate) bool { return c.Text == "你好" })
	if idx < 0 {
		t.Fatalf("xiaohe nihcvs missing prefix 你好: %v", texts(xh.View()))
	}
	res := xh.HandleKey(Key{Kind: KeyDigit, Rune: rune('1' + idx)})
	if res.Commit != "你好" || xh.Raw() != "vs" || xh.View().Preedit != "zhong" {
		t.Fatalf("xiaohe partial commit: res=%+v raw=%q preedit=%q", res, xh.Raw(), xh.View().Preedit)
	}

	// Microsoft Shuangpin native "ao" on 'k': nihk -> 你好.
	ms := New(Options{CandidateCount: 7, Scheme: "microsoft"})
	typeLetters(ms, "nihk")
	if v := ms.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "你好" {
		t.Fatalf("microsoft nihk = %v", texts(v))
	}
}

func TestFuzzyPinyinThroughHandleKey(t *testing.T) {
	e := New(Options{
		CandidateCount: 9,
		Scheme:         "quanpin",
		FuzzyPinyin:    []string{"z=zh", "n=l", "s=sh", "an=ang"},
	})

	// z -> zh ("zongguo" -> 中国)
	typeLetters(e, "zongguo")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "中国" {
		t.Fatalf("fuzzy zongguo = %v", texts(v))
	}
	if r := e.HandleKey(Key{Kind: KeySpace}); r.Commit != "中国" {
		t.Fatalf("fuzzy zongguo commit = %+v", r)
	}

	// Bidirectional zh -> z ("zhaijian" -> 再见)
	typeLetters(e, "zhaijian")
	if !slices.Contains(texts(e.View()), "再见") {
		t.Fatalf("fuzzy zhaijian = %v", texts(e.View()))
	}
	e.Reset()

	// Exact zh ("zhongguo") must not be corrupted by z=zh.
	typeLetters(e, "zhongguo")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "中国" {
		t.Fatalf("exact zhongguo under fuzzy = %v", texts(v))
	}
	e.Reset()

	// l -> n ("lihao" -> 你好)
	typeLetters(e, "lihao")
	if !slices.Contains(texts(e.View()), "你好") {
		t.Fatalf("fuzzy lihao = %v", texts(e.View()))
	}
	e.Reset()

	// Final fuzzy an <-> ang ("shan" surfaces both 山 and 上 on page 1).
	typeLetters(e, "shan")
	shanCands := texts(e.View())
	if len(shanCands) == 0 || shanCands[0] != "山" || !slices.Contains(shanCands, "上") {
		t.Fatalf("fuzzy shan (an=ang) = %v", shanCands)
	}
}

func TestRimeDictImportCachingAndAdapter(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	dictPath := filepath.Join(dir, "custom.dict.yaml")

	rimeYAML := strings.Join([]string{
		"---",
		"name: clavis_custom",
		"version: \"1.0\"",
		"sort: by_weight",
		"...",
		"# Custom phrases",
		"克莱维斯\tke lai wei si\t999999",
		"雾凇自定义词\twu song zi ding yi\t888888",
		"罕见单音词\tzhe\t777777",
	}, "\n")
	if err := os.WriteFile(dictPath, []byte(rimeYAML), 0644); err != nil {
		t.Fatal(err)
	}

	e := New(Options{
		CandidateCount:  7,
		CustomDictPaths: []string{dictPath},
		DictCacheDir:    cacheDir,
	})

	// Full pinyin hits custom Rime phrase at index 0.
	typeLetters(e, "kelaiweisi")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "克莱维斯" {
		t.Fatalf("custom phrase full pinyin = %v", texts(v))
	}
	if r := e.HandleKey(Key{Kind: KeySpace}); r.Commit != "克莱维斯" {
		t.Fatalf("commit custom phrase = %+v", r)
	}

	// Jianpin ("klws") also hits the custom Rime phrase.
	typeLetters(e, "klws")
	if !slices.Contains(texts(e.View()), "克莱维斯") {
		t.Fatalf("custom phrase jianpin = %v", texts(e.View()))
	}
	e.Reset()

	// 1-syllable custom entry surfaces ahead of default single chars, while Unihan chars remain available.
	typeLetters(e, "zhe")
	zheCands := texts(e.View())
	if len(zheCands) < 2 || zheCands[0] != "罕见单音词" || !slices.Contains(zheCands, "这") {
		t.Fatalf("1-syl custom + Unihan fallback = %v", zheCands)
	}
	e.Reset()

	// Built-in Starter + Unihan fallback still work alongside custom dict.
	typeLetters(e, "juede")
	if !slices.Contains(texts(e.View()), "觉得") {
		t.Fatalf("fallback Unihan phrase missing: %v", texts(e.View()))
	}

	// Verify .clvdict cache was written and invalidated when source dict changes.
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected cached .clvdict in %s, got err=%v entries=%v", cacheDir, err, entries)
	}

	updatedYAML := rimeYAML + "\n新热词测试\txin re ci ce shi\t950000\n"
	if err := os.WriteFile(dictPath, []byte(updatedYAML), 0644); err != nil {
		t.Fatal(err)
	}
	e2 := New(Options{
		CandidateCount:  7,
		CustomDictPaths: []string{dictPath},
		DictCacheDir:    cacheDir,
	})
	typeLetters(e2, "xinreciceshi")
	if v := e2.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "新热词测试" {
		t.Fatalf("cache invalidation failed: %v", texts(v))
	}

	// Verify direct NewIMEDictionary adapter from .clvdict bytes.
	compiled, err := ime.CompileRimeDict(strings.NewReader("---\nname: mem\n...\n字节词典\tzi jie ci dian\t500\n"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ime.LoadClvDict(compiled.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	e3 := New(Options{
		CandidateCount: 5,
		Dictionary:     ChainDictionaries(NewIMEDictionary(loaded), UnihanDictionary()),
	})
	typeLetters(e3, "zijiecidian")
	if v := e3.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "字节词典" {
		t.Fatalf("clvdict adapter = %v", texts(v))
	}
}

func TestMissingOrInvalidDictDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	corruptPath := filepath.Join(dir, "corrupt.clvdict")
	if err := os.WriteFile(corruptPath, []byte("CLVD-corrupt-payload"), 0644); err != nil {
		t.Fatal(err)
	}
	emptyDict := filepath.Join(dir, "empty.dict.yaml")
	if err := os.WriteFile(emptyDict, []byte("---\nname: empty\n...\n# no words\n"), 0644); err != nil {
		t.Fatal(err)
	}

	badConfigs := [][]string{
		{filepath.Join(dir, "does_not_exist.dict.yaml")},
		{dir}, // directory instead of file
		{corruptPath},
		{emptyDict},
	}
	for _, paths := range badConfigs {
		e := New(Options{
			CandidateCount:  7,
			CustomDictPaths: paths,
			DictCacheDir:    filepath.Join(dir, "cache"),
		})
		typeLetters(e, "nihao")
		if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "你好" {
			t.Fatalf("degrade %v nihao = %v", paths, texts(v))
		}
		e.Reset()
		typeLetters(e, "shuangpin")
		if !slices.Contains(texts(e.View()), "双拼") {
			t.Fatalf("degrade %v starter phrase missing = %v", paths, texts(e.View()))
		}
		e.Reset()
		typeLetters(e, "juede")
		if !slices.Contains(texts(e.View()), "觉得") {
			t.Fatalf("degrade %v unihan phrase missing = %v", paths, texts(e.View()))
		}
	}
}

func TestCandidateCountClamping(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, DefaultCandidateCount},
		{-3, MinCandidateCount},
		{1, MinCandidateCount},
		{2, MinCandidateCount},
		{3, 3},
		{5, 5},
		{7, 7},
		{9, 9},
		{15, MaxCandidateCount},
	}
	for _, tc := range cases {
		e := New(Options{CandidateCount: tc.in})
		if got := e.CandidateCount(); got != tc.want {
			t.Fatalf("CandidateCount(%d) = %d, want %d", tc.in, got, tc.want)
		}
		typeLetters(e, "shi")
		v := e.View()
		if len(v.Candidates) != tc.want {
			t.Fatalf("CandidateCount(%d) view len = %d, want %d", tc.in, len(v.Candidates), tc.want)
		}
		if tc.want < 9 {
			r := e.HandleKey(Key{Kind: KeyDigit, Rune: rune('0' + tc.want + 1)})
			if r.Consume || r.Commit != "" {
				t.Fatalf("CandidateCount(%d) out-of-page digit consumed or committed: %+v", tc.in, r)
			}
		}
	}
}

type mockUserFreq struct {
	mu      sync.Mutex
	seenKey []string
	seenPrv []string
}

func (m *mockUserFreq) LookupBoost(pinyinKey, word, prevWord string) int32 {
	m.mu.Lock()
	m.seenKey = append(m.seenKey, pinyinKey)
	m.seenPrv = append(m.seenPrv, prevWord)
	m.mu.Unlock()
	if prevWord == "这" && word == "事" {
		return 500
	}
	if prevWord == "" && word == "时" {
		return 200
	}
	return 0
}

func (m *mockUserFreq) CustomWordsForPrefix(prefix string) []UserCandidate {
	if prefix == "nihao" {
		return []UserCandidate{{Word: "泥嚎", Frequency: 1000}}
	}
	return nil
}

func (m *mockUserFreq) BigramWordsForPrefix(prevWord, prefix string) []UserCandidate {
	if prevWord == "我" && prefix == "men" {
		return []UserCandidate{{PinyinKey: "women", Word: "我们", Frequency: 8}}
	}
	return nil
}

func TestBigramGenerationAndColdStartSnapshot(t *testing.T) {
	withBigram := &mockUserFreq{}
	e := New(Options{CandidateCount: 7, UserFrequency: withBigram})
	typeLetters(e, "wo")
	wo := e.View()
	idx := slices.IndexFunc(wo.Candidates, func(c Candidate) bool { return c.Text == "我" })
	if idx < 0 {
		t.Fatalf("previous-word candidate missing: %v", texts(wo))
	}
	if got := e.HandleKey(Key{Kind: KeyDigit, Rune: rune('1' + idx)}); got.Commit != "我" {
		t.Fatalf("commit previous word: %+v", got)
	}
	typeLetters(e, "men")
	withBigramCandidates := texts(e.View())
	if !slices.Contains(withBigramCandidates, "我们") {
		t.Fatalf("bigram candidate missing: %v", withBigramCandidates)
	}
	if slices.Index(withBigramCandidates, "我们") > 2 {
		t.Fatalf("bigram candidate did not surface early: %v", withBigramCandidates)
	}

	cold := New(Options{CandidateCount: 7})
	typeLetters(cold, "men")
	want := texts(cold.View())
	if slices.Contains(want, "我们") {
		t.Fatalf("cold start unexpectedly contains learned bigram: %v", want)
	}
	withEmptyMirror := New(Options{CandidateCount: 7, UserFrequency: &mockUserFreq{}})
	typeLetters(withEmptyMirror, "men")
	if got := texts(withEmptyMirror.View()); !slices.Equal(want, got) {
		t.Fatalf("cold-start candidate snapshot changed: got=%v want=%v", got, want)
	}
}

func TestUserFrequencyHookAndBigramContext(t *testing.T) {
	uf := &mockUserFreq{}
	e := New(Options{CandidateCount: 7, UserFrequency: uf})

	// Unigram boost lifts "时" above "是" when prevWord is empty.
	typeLetters(e, "shi")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "时" {
		t.Fatalf("unigram boost: %v", texts(v))
	}
	e.Reset()

	// Commit "这", then type "shi": bigram boost with prevWord=="这" lifts "事" to #0.
	typeLetters(e, "zhe")
	if r := e.HandleKey(Key{Kind: KeySpace}); r.Commit != "这" {
		t.Fatalf("commit 这: %+v", r)
	}
	typeLetters(e, "shi")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "事" {
		t.Fatalf("bigram boost after 这: %v", texts(v))
	}

	// Reset clears prevWord context so "时" leads again.
	e.Reset()
	typeLetters(e, "shi")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "时" {
		t.Fatalf("after Reset boost: %v", texts(v))
	}
	e.Reset()

	// CustomWordsForPrefix surfaces learned user word at #0.
	typeLetters(e, "nihao")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "泥嚎" {
		t.Fatalf("custom user word: %v", texts(v))
	}
}

func TestEngineConcurrentAccess(t *testing.T) {
	e := New(Options{CandidateCount: 7, Scheme: "xiaohe", FuzzyPinyin: []string{"z=zh"}, UserFrequency: &mockUserFreq{}})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				typeLetters(e, "nihc")
				_ = e.View()
				_ = e.Raw()
				e.SetAICandidates("nihc", []string{"你好呀"})
				e.HandleKey(Key{Kind: KeySpace})
				e.Reset()
			}
		}()
	}
	wg.Wait()
}
