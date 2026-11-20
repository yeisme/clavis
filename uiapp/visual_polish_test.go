package uiapp

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// testThemes are the stock light/dark themes plus variants with system
// accents that are hard to read (yellow, graphite, pink): the tokens must
// keep text and glyphs legible whatever accent macOS hands us.
func testThemes() map[string]*ui.Theme {
	out := map[string]*ui.Theme{"light": ui.LightTheme(), "dark": ui.DarkTheme()}
	for name, accent := range map[string]ui.Color{
		"yellow":   ui.Hex("#ffcc00"),
		"graphite": ui.Hex("#8e8e93"),
		"pink":     ui.Hex("#ff2d55"),
	} {
		for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
			t := *base
			t.Accent = accent
			mode := "light"
			if t.Dark {
				mode = "dark"
			}
			out[name+"-"+mode] = &t
		}
	}
	return out
}

func TestTokenContrast(t *testing.T) {
	for _, alpha := range []float32{alphaFaint, alphaSubtle, alphaSoft, alphaHalf, alphaStrong} {
		if alpha <= 0 || alpha > 1 {
			t.Errorf("opacity token %v outside (0,1]", alpha)
		}
	}
	for name, theme := range testThemes() {
		bg := surfaceBase(theme)
		selected := selectionFill(theme).Over(bg)
		marked := matchMark(theme).Over(selected)

		type contrastCheck struct {
			what string
			fg   ui.Color
			bg   ui.Color
			min  float64
		}
		checks := []contrastCheck{
			{"accent ink on surface", accentInk(theme, bg), bg, minTextContrast},
			{"text on reconstructed card", theme.Text, surfaceHighlight(theme).Over(theme.Surface), minTextContrast},
			{"accent glyph on surface", accentGlyph(theme, bg), bg, minGlyphContrast},
			{"title on chosen row", theme.Text, selected, minTextContrast},
			{"subtitle on chosen row", theme.TextMuted, selected, minGlyphContrast},
			{"matched runes on chosen row", theme.Text, marked, minTextContrast},
			{"matched runes on resting row", theme.Text, matchMark(theme).Over(bg), minTextContrast},
			{"tile icon on chosen row", selectedTileGlyph(theme), selectedTileFill(theme).Over(selected), minGlyphContrast},
			// Omnibox rows: faint tint + 2px bar. Secondary lines (source · time)
			// are held to the body-text 4.5:1, on the resting and chosen row.
			{"title on chosen omnibox row", theme.Text, rowSelectionFill(theme).Over(bg), minTextContrast},
			{"secondary line on resting row", secondaryInk(theme), bg, minTextContrast},
			{"secondary line on chosen omnibox row", secondaryInk(theme), rowSelectionFill(theme).Over(bg), minTextContrast},
			{"matched runes on chosen omnibox row", theme.Text, matchMark(theme).Over(rowSelectionFill(theme).Over(bg)), minTextContrast},
			{"selection bar on chosen omnibox row", rowSelectionBar(theme), rowSelectionFill(theme).Over(bg), minGlyphContrast},
			// Dialog skeleton: meta chips and the required tag are neutral
			// tiles; their secondary text still clears 4.5:1.
			{"secondary text on a meta chip", secondaryInk(theme), tileFill(theme).Over(bg), minTextContrast},
			{"secondary text on a meta chip over the glass body", secondaryInk(theme), tileFill(theme).Over(glassBodyTint(theme).Over(bg)), minTextContrast},
			{"pill EN segment", theme.Background, theme.Text.Alpha(0.88).Over(bg), minTextContrast},
			{"diff insertion", theme.Text, diffInsertMark(theme).Over(theme.Surface), minTextContrast},
			{"diff deletion", diffDeleteInk(theme, theme.Surface), diffDeleteMark(theme).Over(theme.Surface), minTextContrast},
		}
		for level := elev1; level <= elev4; level++ {
			_, _, _, _, shadow := elevationShadow(theme, level)
			checks = append(checks, contrastCheck{fmt.Sprintf("text on elevation %d", level), theme.Text, shadow.Over(theme.Background), minTextContrast})
		}
		for _, ck := range checks {
			if got := contrastRatio(ck.fg, ck.bg); got < ck.min {
				t.Errorf("%s: %s contrast %.2f < %.1f", name, ck.what, got, ck.min)
			}
		}
	}
}

// TestSelectedRowKeepsContentVisible pins the fix for accent-on-accent rows:
// the list's own chosen-row fill is transparent (the row draws a soft tint),
// and matched runes keep the text color on a marker background instead of
// turning accent-colored.
func TestSelectedRowKeepsContentVisible(t *testing.T) {
	theme := ui.LightTheme()
	lt := rowSelectionTheme(theme)
	if lt.Accent != ui.Transparent || lt.InactiveAccent != ui.Transparent {
		t.Fatalf("list theme must not paint a solid chosen row: accent=%v inactive=%v", lt.Accent, lt.InactiveAccent)
	}
	if lt.AccentText != theme.Text {
		t.Fatalf("chosen-row text = %v, want theme text %v", lt.AccentText, theme.Text)
	}
	if theme.Accent == ui.Transparent {
		t.Fatal("rowSelectionTheme must copy, not mutate, the window theme")
	}

	mark := matchMark(theme)
	spans := buildHighlightedSpans("规范术语", []int{1, 3}, mark, theme.Text)
	if len(spans) != 4 {
		t.Fatalf("spans = %+v, want 4 runs", spans)
	}
	for i, sp := range spans {
		if sp.Color != theme.Text {
			t.Fatalf("span %d %q color = %v, want text color", i, sp.Text, sp.Color)
		}
		matched := i == 1 || i == 3
		if matched && (sp.Background != mark || sp.Weight != 700) {
			t.Fatalf("matched span %q = %+v, want marker background + bold", sp.Text, sp)
		}
		if !matched && sp.Background != (ui.Color{}) {
			t.Fatalf("unmatched span %q has background %v", sp.Text, sp.Background)
		}
	}
}

func TestSplitShortcut(t *testing.T) {
	cases := map[string][]string{
		"⌘↵":           {"⌘", "↵"},
		"⌘K":           {"⌘", "K"},
		"⌥1":           {"⌥", "1"},
		"⌥1–9":         {"⌥", "1–9"},
		"Ctrl+Shift+M": {"Ctrl", "Shift", "M"},
		"Alt+1–9":      {"Alt", "1–9"},
		"w h":          {"w", "h"},
		"esc":          {"esc"},
		"/cr":          {"/cr"},
		"":             nil,
	}
	for in, want := range cases {
		got := splitShortcut(in)
		if len(got) != len(want) {
			t.Fatalf("splitShortcut(%q) = %q, want %q", in, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("splitShortcut(%q) = %q, want %q", in, got, want)
			}
		}
	}
}

func TestClipboardContentIcon(t *testing.T) {
	cases := map[string]string{
		"https://github.com/yeisme/clavis/pull/12": "link",
		"www.example.com":                          "link",
		"dev@example.com":                          "envelope",
		"/Users/me/Projects/clavis/main.go":        "folder",
		`C:\Users\me\clavis\main.go`:               "folder",
		"git commit -m \"feat: candidates\"":       "terminal",
		"$ go test ./...":                          "terminal",
		"SELECT id FROM t WHERE x = 1;":            "curlybraces",
		"func main() {\n\tprintln(1)\n}":           "curlybraces",
		"微信支付回调签名校验失败，请检查商户证书":                     "doc.text",
		"git": "doc.text", // a bare word is not a command
		"":    "doc.on.clipboard",
	}
	for in, want := range cases {
		if got := clipboardContentIcon(in, ""); got != want {
			t.Errorf("clipboardContentIcon(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseHexSwatch(t *testing.T) {
	for _, ok := range []string{"#3B82F6", "#fff", "#3b82f680", " #ABCD "} {
		if _, got := parseHexSwatch(ok); !got {
			t.Errorf("parseHexSwatch(%q) = false, want a swatch", ok)
		}
	}
	for _, bad := range []string{"3B82F6", "#12", "#12345", "#ggg", "#3B82F6 is blue", ""} {
		if _, got := parseHexSwatch(bad); got {
			t.Errorf("parseHexSwatch(%q) = true, want none", bad)
		}
	}
	if c, _ := parseHexSwatch("#3B82F6"); c != ui.Hex("#3B82F6") {
		t.Fatalf("swatch color = %v", c)
	}
}

func TestClassifyPill(t *testing.T) {
	cases := []struct {
		label  string
		kind   pillKind
		native string
		ascii  bool
		text   string
	}{
		{"中", pillIMEState, "中", false, ""},
		{"EN", pillIMEState, "中", true, ""},
		{"", pillIMEState, "中", true, ""},
		{"あ", pillIMEState, "あ", false, ""},
		{"한", pillIMEState, "한", false, ""},
		{"LAYER • NAV", pillLayer, "", false, "NAV"},
		{"已绑定 Notes → 英文", pillMessage, "", false, "已绑定 Notes → 英文"},
	}
	for _, tc := range cases {
		got := classifyPill(tc.label)
		if got.kind != tc.kind || got.native != tc.native || got.ascii != tc.ascii || got.text != tc.text {
			t.Errorf("classifyPill(%q) = %+v, want kind=%d native=%q ascii=%v text=%q", tc.label, got, tc.kind, tc.native, tc.ascii, tc.text)
		}
	}
	if d := PillDisplayDuration("中"); d >= PillDisplayDuration("已绑定 Notes → 英文") {
		t.Errorf("a mode badge (%v) should leave sooner than a sentence", d)
	}
	failure := i18n.T("common.error") + ": timeout"
	if PillDisplayDuration(failure) <= PillDisplayDuration("已绑定 Notes → 英文") {
		t.Errorf("a failure should stay longer than an info notice")
	}
	if d := PillDisplayDuration("EN"); d < 600*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("mode badge duration %v outside a glanceable 0.6–1.5s", d)
	}
}

func TestIMEPillRendersBothModes(t *testing.T) {
	for _, label := range []string{"中", "EN"} {
		w, h := PreferredPillSizeForLabel(label)
		tt := ui.NewTester(func(c *ui.Context) { IMEPillView(c, label) }, w, h)
		tt.Frame()
		if !tt.HasText("中") || !tt.HasText("EN") {
			t.Fatalf("pill %q should show both segments, got %v", label, tt.Texts())
		}
	}
	tt := ui.NewTester(func(c *ui.Context) { IMEPillView(c, "LAYER • NAV") }, 160, 32)
	tt.Frame()
	if !tt.HasText(i18n.Tf("pill.layer", "NAV")) {
		t.Fatalf("layer pill text missing, got %v", tt.Texts())
	}
}

func TestLeaderHeightFitsCards(t *testing.T) {
	prev := 0
	for n := 0; n <= 30; n++ {
		h := leaderHeightFor(n)
		if h < leaderMinHeight || h > leaderMaxHeight {
			t.Fatalf("leaderHeightFor(%d) = %d outside [%d,%d]", n, h, leaderMinHeight, leaderMaxHeight)
		}
		if h < prev {
			t.Fatalf("leaderHeightFor(%d) = %d shrank from %d", n, h, prev)
		}
		prev = h
	}
	// One action per row now, so the pinned values shifted up: 4 actions no longer share two
	// rows. Pinned so a row-height change has to be a deliberate edit.
	chrome := leaderChromeHeight
	oneRow := leaderRowHeight
	for nodes, want := range map[int]int{
		4:  min(chrome+4*oneRow, leaderMaxHeight),
		12: min(chrome+12*oneRow, leaderMaxHeight),
		24: leaderMaxHeight,
	} {
		if got := leaderHeightFor(nodes); got != want {
			t.Fatalf("leaderHeightFor(%d) = %d, want %d", nodes, got, want)
		}
	}
}

func TestDisplayLabelLocalizesStockLabelsOnly(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)
	if got := i18n.DisplayLabel("Left Half"); got != "左半屏" {
		t.Fatalf("DisplayLabel(Left Half) = %q", got)
	}
	if got := i18n.DisplayLabel("My Custom Thing"); got != "My Custom Thing" {
		t.Fatalf("user label changed to %q", got)
	}
	i18n.SetLanguage(i18n.English)
	if got := i18n.DisplayLabel("Left Half"); got != "Left Half" {
		t.Fatalf("English DisplayLabel = %q", got)
	}
}

func TestAIModeLabel(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)
	if got := aiModeLabel("custom"); got != "自由提问" {
		t.Fatalf("aiModeLabel(custom) = %q", got)
	}
	if got := aiModeLabel("my-mode"); got != "my-mode" {
		t.Fatalf("user mode = %q", got)
	}
}
