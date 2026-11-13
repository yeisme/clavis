package engine_test

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestFuzzyMatcherScoringAndUnicode(t *testing.T) {
	exact, ok := engine.Match("Polish", "Polish")
	if !ok {
		t.Fatal("expected exact match")
	}
	prefix, ok := engine.Match("Pol", "Polish")
	if !ok {
		t.Fatal("expected prefix match")
	}
	wordBoundary, ok := engine.Match("lh", "Left Half")
	if !ok {
		t.Fatal("expected word boundary match")
	}
	nonBoundary, ok := engine.Match("lh", "Alphabet")
	if !ok {
		t.Fatal("expected non-boundary match")
	}

	if exact.Score <= prefix.Score {
		t.Fatalf("expected exact (%d) > prefix (%d)", exact.Score, prefix.Score)
	}
	if wordBoundary.Score <= nonBoundary.Score {
		t.Fatalf("expected wordBoundary (%d) > nonBoundary (%d)", wordBoundary.Score, nonBoundary.Score)
	}
	if !reflect.DeepEqual(wordBoundary.MatchedLabelIndices, []int{0, 5}) {
		t.Fatalf("expected [0, 5], got %v", wordBoundary.MatchedLabelIndices)
	}

	appMatch, ok := engine.Match("app", "A nice app")
	if !ok || !reflect.DeepEqual(appMatch.MatchedLabelIndices, []int{7, 8, 9}) {
		t.Fatalf("expected [7, 8, 9], got (%v, %v)", appMatch, ok)
	}

	keyMatch, ok := engine.MatchFull("wh", "Left Half", "wh", []string{"window"})
	if !ok || keyMatch.Score <= 0 {
		t.Fatalf("expected key match, got (%v, %v)", keyMatch, ok)
	}

	kwMatch, ok := engine.MatchFull("clipboard", "History", "c", []string{"clipboard"})
	if !ok || len(kwMatch.MatchedLabelIndices) != 0 {
		t.Fatalf("expected keyword match with empty label indices, got (%v, %v)", kwMatch, ok)
	}

	multiToken, ok := engine.MatchFull("win left", "Left Half", "h", []string{"window"})
	if !ok || multiToken.Score <= 0 {
		t.Fatal("expected multi-token match")
	}

	if _, ok := engine.Match("zzz", "Left Half"); ok {
		t.Fatal("expected non-match for zzz")
	}

	// Unicode rune-safety checks
	ligature, ok := engine.Match("ﬁle", "ﬁle")
	if !ok || !reflect.DeepEqual(ligature.MatchedLabelIndices, []int{0, 1, 2}) {
		t.Fatalf("ligature mismatch: (%v, %v)", ligature, ok)
	}
	if maxRuneCount := utf8.RuneCountInString("ﬁle"); ligature.MatchedLabelIndices[len(ligature.MatchedLabelIndices)-1] >= maxRuneCount {
		t.Fatalf("index out of rune range")
	}

	sharpS, ok := engine.Match("ß", "Straße")
	if !ok || !reflect.DeepEqual(sharpS.MatchedLabelIndices, []int{4}) {
		t.Fatalf("sharp-s mismatch: (%v, %v)", sharpS, ok)
	}

	cafe, ok := engine.Match("café", "Café")
	if !ok || !reflect.DeepEqual(cafe.MatchedLabelIndices, []int{0, 1, 2, 3}) {
		t.Fatalf("cafe mismatch: (%v, %v)", cafe, ok)
	}

	istanbul, ok := engine.Match("İ", "İstanbul")
	if !ok || !reflect.DeepEqual(istanbul.MatchedLabelIndices, []int{0}) {
		t.Fatalf("istanbul mismatch: (%v, %v)", istanbul, ok)
	}

	if _, ok := engine.Match("ﬁle", "x"); ok {
		t.Fatal("expected nil for shorter target")
	}
	if _, ok := engine.Match("ß", "a"); ok {
		t.Fatal("expected nil for ß in a")
	}
}

func TestCapsuleLeaderAndAutoDowngrade(t *testing.T) {
	sm := engine.NewStateMachine(config.DefaultConfig())
	ctx := engine.CapsuleContext{
		BundleID:     "com.apple.TextEdit",
		AppName:      "TextEdit",
		SelectedText: "hello world",
	}

	openEff := sm.Handle(engine.Event{Type: engine.EventOpenLeader, Context: &ctx})
	if len(openEff) != 1 || openEff[0].Type != engine.EffectPresentCapsule {
		t.Fatalf("unexpected open effects: %+v", openEff)
	}
	st := sm.State()
	if st.Phase != engine.PhaseLeader || len(st.LeaderPath) != 0 || len(st.LeaderNodes) != len(config.DefaultLeaderConfig().Nodes) {
		t.Fatalf("unexpected root leader state: %+v", st)
	}

	// Drill into Window ("w")
	if eff := sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "w"}); len(eff) != 0 {
		t.Fatalf("expected no effects on drill-down, got %+v", eff)
	}
	st = sm.State()
	if !reflect.DeepEqual(st.LeaderPath, []string{"w"}) || !reflect.DeepEqual(st.LeaderBreadcrumbs, []string{"Window"}) {
		t.Fatalf("unexpected sub leader state: %+v", st)
	}

	// Backspace steps up
	_ = sm.Handle(engine.Event{Type: engine.EventBackspace})
	if len(sm.State().LeaderPath) != 0 {
		t.Fatal("expected root leader after backspace")
	}

	// Drill and Escape steps up; Escape at root closes
	_ = sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "w"})
	if eff := sm.Handle(engine.Event{Type: engine.EventEscape}); len(eff) != 0 || len(sm.State().LeaderPath) != 0 {
		t.Fatalf("expected nested escape to step up")
	}
	if eff := sm.Handle(engine.Event{Type: engine.EventEscape}); len(eff) != 1 || eff[0].Type != engine.EffectClose || sm.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected root escape to close")
	}

	// Leaf execution: w -> h (Left Half)
	_ = sm.Handle(engine.Event{Type: engine.EventOpenLeader, Context: &ctx})
	_ = sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "w"})
	leafEff := sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "h"})
	if len(leafEff) != 2 || leafEff[0].Type != engine.EffectRunAction || leafEff[0].Action.WindowCommand != config.WindowLeftHalf || leafEff[1].Type != engine.EffectClose {
		t.Fatalf("unexpected leaf effects: %+v", leafEff)
	}

	// Unmatched key 'z' downgrades to Actions Omnibox
	_ = sm.Handle(engine.Event{Type: engine.EventOpenLeader, Context: &ctx})
	_ = sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "z"})
	st = sm.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabActions || st.OmniboxQuery != "z" || len(st.OmniboxItems) == 0 {
		t.Fatalf("expected downgrade to actions omnibox, got %+v", st)
	}
	if st.OmniboxItems[0].Title != "Translate EN/ZH" {
		t.Fatalf("expected Translate EN/ZH first, got %s", st.OmniboxItems[0].Title)
	}
}

func TestCapsuleFreeformAIAndClipboardAndGlossary(t *testing.T) {
	sm := engine.NewStateMachine(config.DefaultConfig())

	// Ask AI without selection (should not pull clipboardPreview)
	askCtx := engine.CapsuleContext{ClipboardPreview: "unrelated"}
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAI, Query: "pol", Context: &askCtx})
	st := sm.State()
	last := st.OmniboxItems[len(st.OmniboxItems)-1]
	if last.ID != "ai:adhoc:pol" || last.AIMode != "ask" || last.Title != `Ask AI: "pol"` {
		t.Fatalf("unexpected adhoc ask item: %+v", last)
	}
	for i := 0; i < len(st.OmniboxItems)-1; i++ {
		_ = sm.Handle(engine.Event{Type: engine.EventSelectNext})
	}
	eff := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(eff) != 1 || eff[0].Type != engine.EffectStartAI || eff[0].Mode != "ask" || eff[0].SourceText != "" {
		t.Fatalf("unexpected ask startAI effect: %+v", eff)
	}

	// Escape while streaming cancels AI
	cancelEff := sm.Handle(engine.Event{Type: engine.EventEscape})
	if len(cancelEff) != 2 || cancelEff[0].Type != engine.EffectCancelAI || cancelEff[1].Type != engine.EffectClose {
		t.Fatalf("unexpected cancel effects: %+v", cancelEff)
	}

	// Custom rewrite in Actions tab with selected text
	selCtx := engine.CapsuleContext{SelectedText: "draft paragraph"}
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "make shorter", Context: &selCtx})
	st = sm.State()
	adhoc := st.OmniboxItems[len(st.OmniboxItems)-1]
	if adhoc.AIMode != "custom" || adhoc.Title != `Rewrite with AI: "make shorter"` {
		t.Fatalf("unexpected custom rewrite item: %+v", adhoc)
	}
	eff = sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(eff) != 1 || eff[0].Type != engine.EffectStartAI || eff[0].Mode != "custom" || eff[0].SourceText != "draft paragraph" {
		t.Fatalf("unexpected custom startAI effect: %+v", eff)
	}
	_ = sm.Handle(engine.Event{Type: engine.EventAIChunk, Chunk: "Shorter."})
	_ = sm.Handle(engine.Event{Type: engine.EventAICompleted})
	applyEff := sm.Handle(engine.Event{Type: engine.EventAcceptAIOutput})
	if len(applyEff) != 2 || applyEff[0].Type != engine.EffectApplyAIOutput || applyEff[0].Text != "Shorter." {
		t.Fatalf("unexpected acceptAI effect: %+v", applyEff)
	}

	// Clipboard pin, delete, and SetClipboardEntries ID preservation across sort reorder
	entries := []engine.OmniboxClipboardEntry{
		{ID: "a", Content: "Alpha", IsPinned: false},
		{ID: "b", Content: "Beta", IsPinned: false},
		{ID: "c", Content: "Gamma", IsPinned: false},
	}
	_ = sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: entries})
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	_ = sm.Handle(engine.Event{Type: engine.EventSelectNext}) // select "b"
	pinEff := sm.Handle(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem})
	if len(pinEff) != 1 || pinEff[0].Type != engine.EffectPinClipboardItem || pinEff[0].ClipboardID != "b" || !pinEff[0].IsPinned {
		t.Fatalf("unexpected pin effect: %+v", pinEff)
	}

	// Simulate DB re-sort placing pinned "b" first
	reordered := []engine.OmniboxClipboardEntry{
		{ID: "b", Content: "Beta", IsPinned: true},
		{ID: "a", Content: "Alpha", IsPinned: false},
		{ID: "c", Content: "Gamma", IsPinned: false},
	}
	_ = sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: reordered})
	st = sm.State()
	if st.SelectedIndex != 0 || st.OmniboxItems[st.SelectedIndex].ID != "clipboard:b" {
		t.Fatalf("expected selection ID preserved at index 0, got idx=%d id=%s", st.SelectedIndex, st.OmniboxItems[st.SelectedIndex].ID)
	}

	delEff := sm.Handle(engine.Event{Type: engine.EventDeleteSelectedClipboardItem})
	if len(delEff) != 1 || delEff[0].Type != engine.EffectDeleteClipboard || delEff[0].ClipboardID != "b" {
		t.Fatalf("unexpected delete effect: %+v", delEff)
	}

	// Built-in normalize_glossary in Glossary & Actions tabs
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "normalize", Context: &selCtx})
	st = sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].ID != "action:normalize-glossary" {
		t.Fatalf("expected normalize-glossary first, got %+v", st.OmniboxItems)
	}
}

func TestSnippetEngineAndRenderer(t *testing.T) {
	snippets := []config.SnippetItem{
		{ID: "sig", Trigger: ";sig", Content: "Best regards,", WordBoundary: true},
		{ID: "mail-only", Trigger: ";mail", Content: "Sent from Mail", WordBoundary: true, AppBundleIDs: []string{"com.apple.mail"}},
		{ID: "proc-only", Trigger: ";term", Content: "pwsh", WordBoundary: true, ProcessNames: []string{"WindowsTerminal.exe"}},
		{ID: "arrow", Trigger: "->", Content: "→", WordBoundary: false},
	}

	eng := engine.NewSnippetEngine(snippets, 16)

	// Word boundary blocks "hello;sig"
	for _, r := range "hello;sig" {
		if m, ok := eng.Feed(r, engine.FeedOptions{}); ok {
			t.Fatalf("unexpected match inside word: %+v", m)
		}
	}

	// Wraparound > capacity + word boundary allows match after space
	eng.Reset()
	for _, r := range "abcdefghijklmnopqrstuvwxyz " {
		if _, ok := eng.Feed(r, engine.FeedOptions{}); ok {
			t.Fatal("unexpected match")
		}
	}
	var matched *engine.SnippetMatch
	var ok bool
	for _, r := range ";sig" {
		matched, ok = eng.Feed(r, engine.FeedOptions{})
	}
	if !ok || matched.Snippet.ID != "sig" || matched.RenderedText != "Best regards," || matched.DeleteCount != 4 || matched.CursorBackOffset != 0 {
		t.Fatalf("unexpected sig match: (%+v, %v)", matched, ok)
	}

	// Backspace recovery
	for _, r := range ";six" {
		_, _ = eng.Feed(r, engine.FeedOptions{})
	}
	eng.Backspace()
	matched, ok = eng.Feed('g', engine.FeedOptions{})
	if !ok || matched.Snippet.ID != "sig" {
		t.Fatalf("expected match after backspace")
	}

	// Bundle ID & process name filtering
	eng.Reset()
	for _, r := range ";mail" {
		if _, ok := eng.Feed(r, engine.FeedOptions{BundleID: "com.apple.TextEdit"}); ok {
			t.Fatal("should not match wrong bundleID")
		}
	}
	eng.Reset()
	for _, r := range ";mail" {
		matched, ok = eng.Feed(r, engine.FeedOptions{BundleID: "com.apple.mail"})
	}
	if !ok || matched.Snippet.ID != "mail-only" {
		t.Fatal("expected mail-only match")
	}

	eng.Reset()
	for _, r := range ";term" {
		matched, ok = eng.Feed(r, engine.FeedOptions{ProcessName: "windowsterminal.exe"})
	}
	if !ok || matched.Snippet.ID != "proc-only" {
		t.Fatal("expected proc-only case-insensitive process_name match")
	}

	// Non-word-boundary
	eng.Reset()
	_, _ = eng.Feed('a', engine.FeedOptions{})
	_, _ = eng.Feed('-', engine.FeedOptions{})
	matched, ok = eng.Feed('>', engine.FeedOptions{})
	if !ok || matched.RenderedText != "→" || matched.DeleteCount != 2 {
		t.Fatalf("expected arrow match, got (%+v, %v)", matched, ok)
	}

	// Renderer placeholders + exact rune cursor offset
	fixedTime := time.Date(2026, 4, 19, 14, 35, 9, 0, time.UTC)
	res := engine.RenderSnippet(
		"Date={{date}} Custom={{date:yyyy/MM/dd}} Time={{time}} TimeS={{time:HH:mm:ss}} DT={{datetime}} App={{app}} Clip={{clipboard}} End",
		engine.RenderOptions{
			TriggerLength: 5,
			Now:           fixedTime,
			ClipboardText: "{{cursor}}raw_clip",
			AppName:       "Xcode",
			Location:      time.UTC,
		},
	)
	want := "Date=2026-04-19 Custom=2026/04/19 Time=14:35 TimeS=14:35:09 DT=2026-04-19 14:35 App=Xcode Clip={{cursor}}raw_clip End"
	if res.RenderedText != want || res.CursorBackOffset != 0 || res.DeleteCount != 5 {
		t.Fatalf("unexpected render result: %+v\nwant: %s", res, want)
	}

	cursorRes := engine.RenderSnippet(`func {{cursor}}(x: String) -> "👍中"`, engine.RenderOptions{TriggerLength: 3})
	wantSuffixRunes := utf8.RuneCountInString(`(x: String) -> "👍中"`)
	if cursorRes.RenderedText != `func (x: String) -> "👍中"` || cursorRes.CursorBackOffset != wantSuffixRunes {
		t.Fatalf("unexpected rune cursor offset: %+v (want %d)", cursorRes, wantSuffixRunes)
	}
}

func TestGlossaryEngine(t *testing.T) {
	ge := engine.NewGlossaryEngine([]config.GlossaryTerm{
		{Term: "Clavis", Aliases: []string{"clavis", "clavis app"}, Hint: "Keep capitalized.", Category: "product"},
		{Term: "MyGo", Aliases: []string{"mygo"}, Category: "framework"},
	})

	rel := ge.RelevantTerms("Use clavis app with mygo.", "", 0)
	if len(rel) != 2 {
		t.Fatalf("expected 2 relevant terms, got %+v", rel)
	}

	block := ge.FormatPromptBlock(rel)
	if !strings.HasPrefix(block, "<glossary>\n") || !strings.HasSuffix(block, "\n</glossary>") {
		t.Fatalf("unexpected glossary block:\n%s", block)
	}
	if !strings.Contains(block, "- Clavis [product] (aliases: clavis, clavis app): Keep capitalized.") {
		t.Fatalf("missing Clavis line in block:\n%s", block)
	}

	injected := ge.InjectIntoSystemPrompt("Base prompt.", "Use clavis.", "", 0)
	if !strings.HasPrefix(injected, "Base prompt.\n\n<glossary>") {
		t.Fatalf("unexpected injected prompt:\n%s", injected)
	}

	// Longest-first alias normalization with word boundaries
	norm := ge.NormalizeAliases("Use clavis app and clavis, not clavisx, with mygo.", true, true)
	if norm != "Use Clavis and Clavis, not clavisx, with MyGo." {
		t.Fatalf("unexpected normalization: %q", norm)
	}
}

func TestUtilitiesEmojiAppLauncherAndMultiTurnAI(t *testing.T) {
	fixedNow := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	// 1. Math evaluator
	mathItems := engine.EvaluateUtilities("(2 + 3) * 4^2 - 10 % 3", fixedNow)
	if len(mathItems) != 1 || mathItems[0].Kind != engine.ItemUtility || mathItems[0].UtilityValue != "79" || !strings.Contains(mathItems[0].Title, "Result = 79") {
		t.Fatalf("unexpected math utility items: %+v", mathItems)
	}

	// 2. Converters: uuid, ts, 10-digit timestamp, b64/b64d, hex/0x
	uuidItems := engine.EvaluateUtilities("uuid", fixedNow)
	if len(uuidItems) != 1 || len(uuidItems[0].UtilityValue) != 36 {
		t.Fatalf("unexpected uuid items: %+v", uuidItems)
	}
	tsItems := engine.EvaluateUtilities("ts", fixedNow)
	if len(tsItems) != 2 || tsItems[0].UtilityValue != "1776599999" && tsItems[0].UtilityValue != "1776600000" {
		t.Fatalf("unexpected ts items: %+v", tsItems)
	}
	convTs := engine.EvaluateUtilities("1700000000", fixedNow)
	if len(convTs) != 1 || convTs[0].UtilityValue != "2023-11-14T22:13:20Z" {
		t.Fatalf("unexpected 10-digit timestamp conversion: %+v", convTs)
	}
	b64Enc := engine.EvaluateUtilities("b64 hello clavis", fixedNow)
	if len(b64Enc) != 1 || b64Enc[0].UtilityValue != "aGVsbG8gY2xhdmlz" {
		t.Fatalf("unexpected b64 encode: %+v", b64Enc)
	}
	b64Dec := engine.EvaluateUtilities("b64d aGVsbG8gY2xhdmlz", fixedNow)
	if len(b64Dec) != 1 || b64Dec[0].UtilityValue != "hello clavis" {
		t.Fatalf("unexpected b64 decode: %+v", b64Dec)
	}
	hexEnc := engine.EvaluateUtilities("hex 255", fixedNow)
	if len(hexEnc) != 1 || hexEnc[0].UtilityValue != "0xFF" {
		t.Fatalf("unexpected hex encode: %+v", hexEnc)
	}
	hexDec := engine.EvaluateUtilities("0xFF", fixedNow)
	if len(hexDec) != 1 || hexDec[0].UtilityValue != "255" {
		t.Fatalf("unexpected 0xFF decode: %+v", hexDec)
	}

	// 3. Emoji search (English + Chinese)
	enEmoji := engine.SearchEmojis(":rocket")
	if len(enEmoji) == 0 || enEmoji[0].UtilityValue != "🚀" {
		t.Fatalf("expected 🚀 for :rocket, got %+v", enEmoji)
	}
	zhEmoji := engine.SearchEmojis("火箭")
	if len(zhEmoji) == 0 || zhEmoji[0].UtilityValue != "🚀" {
		t.Fatalf("expected 🚀 for 火箭, got %+v", zhEmoji)
	}

	// 4. Capsule integration: InstalledApps, Emoji confirm, Utility confirm, Multi-turn AI follow-up
	sm := engine.NewStateMachine(config.DefaultConfig())
	_ = sm.Handle(engine.Event{
		Type: engine.EventSetInstalledApps,
		InstalledApps: []engine.InstalledAppEntry{
			{Name: "Visual Studio Code", BundleID: "com.microsoft.VSCode", Path: "/Applications/Visual Studio Code.app"},
		},
	})
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "VSCode"})
	st := sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].Kind != engine.ItemInstalledApp {
		t.Fatalf("expected installed app first for VSCode, got %+v", st.OmniboxItems)
	}
	appEff := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(appEff) != 2 || appEff[0].Type != engine.EffectRunAction || appEff[0].Action.Target != "com.microsoft.VSCode" {
		t.Fatalf("unexpected installed app confirm effects: %+v", appEff)
	}

	// Select AI profile + multi-turn follow-up
	_ = sm.Handle(engine.Event{Type: engine.EventSelectAIProfile, ProfileID: "claude"})
	_ = sm.Handle(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{SelectedText: "hello world"}})
	_ = sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "a"})
	startEff := sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "p"})
	if len(startEff) != 1 || startEff[0].Type != engine.EffectStartAI || startEff[0].ProfileID != "claude" {
		t.Fatalf("expected EffectStartAI with profile claude, got %+v", startEff)
	}
	_ = sm.Handle(engine.Event{Type: engine.EventAIChunk, Chunk: "Polished v1"})
	_ = sm.Handle(engine.Event{Type: engine.EventAICompleted})

	followEff := sm.Handle(engine.Event{Type: engine.EventFollowUpAI, Prompt: "Make it more formal"})
	if len(followEff) != 2 || followEff[0].Type != engine.EffectCancelAI || followEff[1].Type != engine.EffectStartAI {
		t.Fatalf("unexpected follow-up effects: %+v", followEff)
	}
	if len(followEff[1].History) != 2 || followEff[1].History[1].Content != "Polished v1" || followEff[1].ProfileID != "claude" {
		t.Fatalf("expected 2 history turns and claude profile in follow-up EffectStartAI, got %+v", followEff[1])
	}
	st = sm.State()
	if st.Phase != engine.PhaseAIStreaming || st.AIComplete || st.AIOutput != "" || st.AIPrompt != "Make it more formal" {
		t.Fatalf("unexpected state after EventFollowUpAI: %+v", st)
	}
}

func TestPinyinFullWidthPrefixRoutingAndDualActions(t *testing.T) {
	// 1. Pinyin full, initialism, partial trailing syllable, polyphones, and ü (v/u) with exact Hanzi rune indices
	pyFull, ok := engine.Match("weixin", "微信 (WeChat)")
	if !ok || !reflect.DeepEqual(pyFull.MatchedLabelIndices, []int{0, 1}) {
		t.Fatalf("expected [0, 1] for weixin -> 微信 (WeChat), got (%+v, %v)", pyFull, ok)
	}
	pyInit, ok := engine.Match("wx", "微信 (WeChat)")
	if !ok || !reflect.DeepEqual(pyInit.MatchedLabelIndices, []int{0, 1}) {
		t.Fatalf("expected [0, 1] for wx -> 微信 (WeChat), got (%+v, %v)", pyInit, ok)
	}
	if pyFull.Score <= pyInit.Score {
		t.Fatalf("expected full pinyin score (%d) > initialism score (%d)", pyFull.Score, pyInit.Score)
	}
	pyPartial, ok := engine.Match("weix", "微信")
	if !ok || !reflect.DeepEqual(pyPartial.MatchedLabelIndices, []int{0, 1}) {
		t.Fatalf("expected [0, 1] for trailing partial weix -> 微信, got (%+v, %v)", pyPartial, ok)
	}
	pyPoly, ok := engine.Match("chonggou", "重构代码")
	if !ok || !reflect.DeepEqual(pyPoly.MatchedLabelIndices, []int{0, 1}) {
		t.Fatalf("expected polyphone chonggou -> 重构代码 [0, 1], got (%+v, %v)", pyPoly, ok)
	}
	pyV, ok := engine.Match("lvse", "绿色主题")
	if !ok || !reflect.DeepEqual(pyV.MatchedLabelIndices, []int{0, 1}) {
		t.Fatalf("expected v-spelling lvse -> 绿色主题 [0, 1], got (%+v, %v)", pyV, ok)
	}
	pyMixed, ok := engine.Match("dkvsc", "打开 VSCode")
	if !ok || len(pyMixed.MatchedLabelIndices) != 5 {
		t.Fatalf("expected mixed Hanzi+ASCII match dkvsc -> 打开 VSCode, got (%+v, %v)", pyMixed, ok)
	}

	// 2. Full-width operators, hex, and emoji Pinyin search
	fixedNow := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	fwMath := engine.EvaluateUtilities("＝（２＋３）×４^2", fixedNow)
	if len(fwMath) != 1 || fwMath[0].UtilityValue != "80" {
		t.Fatalf("expected 80 for full-width math expression, got %+v", fwMath)
	}
	fwHex := engine.EvaluateUtilities("０ｘＦＦ", fixedNow)
	if len(fwHex) != 1 || fwHex[0].UtilityValue != "255" {
		t.Fatalf("expected 255 for full-width 0xFF, got %+v", fwHex)
	}
	fwEmoji := engine.SearchEmojis("：huojian")
	if len(fwEmoji) == 0 || fwEmoji[0].UtilityValue != "🚀" {
		t.Fatalf("expected 🚀 for full-width Pinyin ：huojian, got %+v", fwEmoji)
	}

	// 3. Leader '/' & '／' instant clean search switch
	sm := engine.NewStateMachine(config.DefaultConfig())
	appCtx := engine.CapsuleContext{
		BundleID:         "com.tencent.xinWeChat",
		AppName:          "微信",
		SelectedText:     "selected sample",
		ClipboardPreview: "clip-val",
	}
	_ = sm.Handle(engine.Event{Type: engine.EventOpenLeader, Context: &appCtx})
	_ = sm.Handle(engine.Event{Type: engine.EventLeaderKeyPress, Key: "/"})
	st := sm.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabActions || st.OmniboxQuery != "" {
		t.Fatalf("expected '/' in Leader to open clean Actions Omnibox, got %+v", st)
	}

	// 4. Omnibox Prefix Quick-Routing: >, ;, @, ?, :, = (including full-width)
	_ = sm.Handle(engine.Event{
		Type: engine.EventSetClipboardEntries,
		ClipboardEntries: []engine.OmniboxClipboardEntry{
			{ID: "c1", Content: "alpha clipboard line", SourceAppName: "Notes"},
			{ID: "c2", Content: "beta clipboard line", SourceAppName: "Safari"},
		},
	})

	// ';' & '；' route to Snippets
	_ = sm.Handle(engine.Event{Type: engine.EventUpdateQuery, Query: "；sig"})
	st = sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].Kind != engine.ItemSnippet || st.OmniboxItems[0].Snippet.ID != "email-signoff" {
		t.Fatalf("expected ；sig to route to email-signoff snippet, got %+v", st.OmniboxItems)
	}
	// Secondary confirm on snippet copies rendered text instead of injecting
	secSnipEff := sm.Handle(engine.Event{Type: engine.EventConfirmSecondarySelection})
	if len(secSnipEff) != 2 || secSnipEff[0].Type != engine.EffectCopyText || secSnipEff[0].Text != "Best regards," {
		t.Fatalf("expected secondary confirm on snippet to emit EffectCopyText, got %+v", secSnipEff)
	}

	// '@' routes inline to Clipboard entries & supports Pin/Secondary Copy
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "@beta", Context: &appCtx})
	st = sm.State()
	if len(st.OmniboxItems) != 1 || st.OmniboxItems[0].ClipboardID != "c2" {
		t.Fatalf("expected @beta to filter clipboard c2, got %+v", st.OmniboxItems)
	}
	pinEff := sm.Handle(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "pin"})
	if len(pinEff) != 1 || pinEff[0].Type != engine.EffectPinClipboardItem || !pinEff[0].IsPinned {
		t.Fatalf("expected pin action in @ prefix view, got %+v", pinEff)
	}
	secClipEff := sm.Handle(engine.Event{Type: engine.EventConfirmSecondarySelection})
	if len(secClipEff) != 2 || secClipEff[0].Type != engine.EffectCopyText || secClipEff[0].Text != "beta clipboard line" {
		t.Fatalf("expected secondary confirm on clipboard item to copy, got %+v", secClipEff)
	}

	// '?' routes to AI with adhoc prompt first; secondary asks without selection
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "？explain golang", Context: &appCtx})
	st = sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].Kind != engine.ItemAIMode {
		t.Fatalf("expected ？query to route to AI items first, got %+v", st.OmniboxItems)
	}
	secAIEff := sm.Handle(engine.Event{Type: engine.EventConfirmSecondarySelection})
	if len(secAIEff) != 1 || secAIEff[0].Type != engine.EffectStartAI || secAIEff[0].Mode != "ask" || secAIEff[0].SourceText != "" {
		t.Fatalf("expected secondary AI action to force ask without selection, got %+v", secAIEff)
	}

	// '=' routes to Utilities & Secondary confirm injects directly at cursor
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "=6*7", Context: &appCtx})
	st = sm.State()
	if len(st.OmniboxItems) != 1 || st.OmniboxItems[0].UtilityValue != "42" {
		t.Fatalf("expected =6*7 utility result 42, got %+v", st.OmniboxItems)
	}
	specs := engine.AvailableItemActions(st.OmniboxItems[0])
	if len(specs) < 2 || specs[0].ID != "copy" || specs[1].ID != "paste" {
		t.Fatalf("unexpected AvailableItemActions for utility: %+v", specs)
	}
	secUtilEff := sm.Handle(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: 0, Secondary: true})
	if len(secUtilEff) != 2 || secUtilEff[0].Type != engine.EffectApplyAIOutput || secUtilEff[0].Text != "42" {
		t.Fatalf("expected secondary SelectAndConfirmIndex on utility to inject 42, got %+v", secUtilEff)
	}

	// '>' routes to Commands + One-Click Bind Current App IME Rule (matched via Chinese Pinyin "bdywsrf" / "绑定当前应用为英文输入法")
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: ">英文输入法", Context: &appCtx})
	st = sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].Kind != engine.ItemAppIMERule || st.OmniboxItems[0].IMEMode != config.IMEModeASCII {
		t.Fatalf("expected >英文输入法 to surface Bind Current App to ASCII IME, got %+v", st.OmniboxItems)
	}
	bindEff := sm.Handle(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: 0})
	if len(bindEff) != 2 || bindEff[0].Type != engine.EffectBindAppIMERule || bindEff[0].IMEMode != config.IMEModeASCII || bindEff[0].Context.BundleID != "com.tencent.xinWeChat" {
		t.Fatalf("expected EffectBindAppIMERule(ascii) for WeChat, got %+v", bindEff)
	}
}

func TestSnippetEngine_FullWidthPunctuationTriggers(t *testing.T) {
	eng := engine.NewSnippetEngine([]config.SnippetItem{
		{ID: "sig", Trigger: ";sig", Content: "Best regards,", WordBoundary: true},
		{ID: "fw-colon", Trigger: "：note", Content: "NOTE", WordBoundary: true},
	}, 32)

	// A CJK IME commits '；' (U+FF1B) instead of ';' (U+003B); the ASCII trigger must still fire.
	eng.Reset()
	var (
		matched *engine.SnippetMatch
		ok      bool
	)
	for _, r := range "；sig" {
		matched, ok = eng.Feed(r, engine.FeedOptions{})
	}
	if !ok || matched.Snippet.ID != "sig" {
		t.Fatalf("expected full-width '；sig' to match ';sig', got (%+v, %v)", matched, ok)
	}
	// DeleteCount counts typed runes, so the host deletes '；sig' (4 runes, not 4 bytes).
	if matched.DeleteCount != 4 || matched.RenderedText != "Best regards," {
		t.Fatalf("unexpected delete/render for full-width trigger: %+v", matched)
	}

	// Reverse direction: a full-width trigger still matches plain ASCII typing.
	eng.Reset()
	for _, r := range ":note" {
		matched, ok = eng.Feed(r, engine.FeedOptions{})
	}
	if !ok || matched.Snippet.ID != "fw-colon" {
		t.Fatalf("expected ':note' to match '：note', got (%+v, %v)", matched, ok)
	}

	// Full-width letters are not punctuation: they must never alias ASCII word characters.
	eng.Reset()
	for _, r := range "；ｓｉｇ" {
		if _, ok := eng.Feed(r, engine.FeedOptions{}); ok {
			t.Fatal("expected full-width letters not to match an ASCII word trigger")
		}
	}

	// Word boundary still applies to full-width triggers typed inside a word.
	eng.Reset()
	for _, r := range "hi；sig" {
		if _, ok := eng.Feed(r, engine.FeedOptions{}); ok {
			t.Fatal("expected word boundary to block 'hi；sig'")
		}
	}
}
