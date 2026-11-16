package uiapp

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// This file guards the design tokens in tokens.go. The views used to inline
// raw style numbers (.Radius(999), .FontSize(12), .Padding(8, 10)) next to
// real tokens, which is how the Capsule header drifted away from the Leader
// menu. The guard has two halves:
//
//   - token scale tests, which pin the ramp every view reads from;
//   - a raw-literal scan with a ratchet baseline, which lets the existing debt
//     stay put while forbidding any new literal from sneaking in.
//
// The baseline shrinks as each surface migrates to the kit; the ratchet only
// fails when a file grows past its recorded debt, so progress is never blocked
// by a stale entry.

type styleKind int

const (
	styleRadius styleKind = iota
	styleFontSize
	stylePadding
	styleKindCount
)

// styleKindNames labels each kind in failure output.
var styleKindNames = [styleKindCount]string{"radius", "fontsize", "padding"}

// stylePatterns match a literal argument to the three styling calls that were
// routinely inlined. A token argument (e.g. .FontSize(typeBody)) does not
// match, because only digits are accepted. The leading dot is deliberately not
// required: a chained call often starts its own line ("\t\tRadius(6)."),
// leaving the dot at the end of the previous line.
var stylePatterns = [styleKindCount]*regexp.Regexp{
	styleRadius:   regexp.MustCompile(`\bRadius\((\d+(?:\.\d+)?)\)`),
	styleFontSize: regexp.MustCompile(`\bFontSize\((\d+(?:\.\d+)?)\)`),
	stylePadding:  regexp.MustCompile(`\bPadding\((\d+)`),
}

// styleDebt counts the raw style literals of one file.
type styleDebt [styleKindCount]int

// styleDebtBaseline is the raw-literal debt recorded when the guard landed.
// Later tasks (T5, T6, T7) migrate the surfaces and lower these numbers; T14
// empties the map, which makes the guard a zero-tolerance check.
var styleDebtBaseline = map[string]styleDebt{
	"capsule_preview.go":      {0, 0, 0},
	"capsule_view.go":         {0, 0, 0},
	"components.go":           {0, 0, 1},
	"input_candidate_view.go": {3, 6, 5},
	"onboarding_view.go":      {0, 1, 0},
	"pill_view.go":            {0, 0, 0},
	"prompt_composer.go":      {0, 0, 0},
	"selection_bar_view.go":   {0, 0, 0},
	"settings_editors.go":     {0, 0, 0},
	"settings_view.go":        {0, 0, 0},
	"spatial3d.go":            {1, 2, 1},
	"theme_glass.go":          {0, 2, 1},
}

// countStyleLiterals counts the raw literals in one Go source text.
func countStyleLiterals(src string) styleDebt {
	var d styleDebt
	for kind, re := range stylePatterns {
		d[kind] = len(re.FindAllString(src, -1))
	}
	return d
}

// styleDebtFiles scans the package's non-test sources. tokens.go is skipped: it
// is where the literal values are defined.
func styleDebtFiles(t *testing.T) map[string]styleDebt {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	out := map[string]styleDebt{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "tokens.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if d := countStyleLiterals(string(src)); d != (styleDebt{}) {
			out[name] = d
		}
	}
	return out
}

// TestStyleLiteralGuard holds the raw-literal debt to its baseline. It fails
// when a file introduces new literals, and its failure message is the exact Go
// literal to paste when a migration lowers a count.
func TestStyleLiteralGuard(t *testing.T) {
	if os.Getenv("CLAVIS_STYLE_BASELINE") != "" {
		debt := styleDebtFiles(t)
		t.Logf("var styleDebtBaseline = map[string]styleDebt{")
		for _, name := range sortedDebtKeys(debt) {
			d := debt[name]
			t.Logf("\t%q: {%d, %d, %d}, // radius, fontsize, padding", name, d[styleRadius], d[styleFontSize], d[stylePadding])
		}
		t.Logf("}")
		return
	}

	actual := styleDebtFiles(t)
	names := map[string]struct{}{}
	for name := range actual {
		names[name] = struct{}{}
	}
	for name := range styleDebtBaseline {
		names[name] = struct{}{}
	}

	var problems []string
	for _, name := range sortedStringKeys(names) {
		got, want := actual[name], styleDebtBaseline[name]
		for kind := styleKind(0); kind < styleKindCount; kind++ {
			if got[kind] > want[kind] {
				problems = append(problems, fmt.Sprintf(
					"%s: raw %s literals %d > baseline %d — use a tokens.go constant",
					name, styleKindNames[kind], got[kind], want[kind]))
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("raw style literals crept in:\n  %s", strings.Join(problems, "\n  "))
	}
}

// sortedDebtKeys returns the map's keys in a stable order.
func sortedDebtKeys(m map[string]styleDebt) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedStringKeys returns a set's keys in a stable order.
func sortedStringKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestStyleLiteralGuardCatchesLiterals proves the scan is not silently broken:
// a synthetic source with all three shapes is counted, and token arguments are
// not.
func TestStyleLiteralGuardCatchesLiterals(t *testing.T) {
	got := countStyleLiterals(`a.Radius(999).FontSize(12).Padding(8, 10)`)
	want := styleDebt{styleRadius: 1, styleFontSize: 1, stylePadding: 1}
	if got != want {
		t.Fatalf("countStyleLiterals = %v, want %v", got, want)
	}

	tokens := countStyleLiterals(`a.Radius(radiusCard).FontSize(typeBody).Padding(space8, space10)`)
	if tokens != (styleDebt{}) {
		t.Fatalf("token arguments must not count as literals: %v", tokens)
	}
}

// TestSpacingScaleIsMonotonic pins the spacing ramp so a future edit cannot
// reorder it. Every step is used by a view, so the order is load-bearing.
func TestSpacingScaleIsMonotonic(t *testing.T) {
	scale := []float32{space1, space2, space4, space6, space8, space10, space12, space14, space16, space18, space20, space24, space32}
	for i := 1; i < len(scale); i++ {
		if scale[i] <= scale[i-1] {
			t.Fatalf("spacing scale is not increasing at %d: %v", i, scale)
		}
	}
}

// TestRadiusScaleIsMonotonic pins the corner radii. radiusPill is excluded: it
// is a sentinel that the renderer clamps to half the box height.
func TestRadiusScaleIsMonotonic(t *testing.T) {
	scale := []float32{radius4, radiusKeycap, radiusChip, radiusTile, radius8, radiusRow, radiusCard, radiusSegment, radius12}
	for i := 1; i < len(scale); i++ {
		if scale[i] <= scale[i-1] {
			t.Fatalf("radius scale is not increasing at %d: %v", i, scale)
		}
	}
	if radiusPill <= radius12 {
		t.Fatalf("radiusPill must stay the largest corner: %v <= %v", radiusPill, radius12)
	}
}

// TestTypeScaleIsMonotonic pins the type ramp, including the two sizes the
// geometry and hero titles needed.
func TestTypeScaleIsMonotonic(t *testing.T) {
	scale := []float32{typeMicro, typeMeta, typeLabel, typeBody, typeTitle, typeSearch, typeGlyph, typeHero}
	for i := 1; i < len(scale); i++ {
		if scale[i] <= scale[i-1] {
			t.Fatalf("type scale is not increasing at %d: %v", i, scale)
		}
	}
	if lineTight >= lineProse {
		t.Fatalf("line heights must be ordered: tight %v >= prose %v", lineTight, lineProse)
	}
}

// TestElevationScale pins the shadow ramp: it must grow monotonically, stay
// darker in dark mode at equal level, and clamp out-of-range levels instead of
// returning a zero shadow by accident.
func TestElevationScale(t *testing.T) {
	light, dark := ui.LightTheme(), ui.DarkTheme()
	for level := elev1; level <= elev4; level++ {
		lx, ly, lBlur, _, lColor := elevationShadow(light, level)
		dx, dy, dBlur, _, dColor := elevationShadow(dark, level)

		if lx != dx || ly != dy || lBlur != dBlur {
			t.Fatalf("level %d geometry differs by theme: light %v dark %v", level, lBlur, dBlur)
		}
		if level > elev1 && lBlur <= elevationStep(level-1).Blur {
			t.Fatalf("level %d blur %v does not exceed level %d", level, lBlur, level-1)
		}
		if dColor.A <= lColor.A {
			t.Fatalf("level %d dark alpha %v must exceed light alpha %v", level, dColor.A, lColor.A)
		}
		if lColor.A == 0 || dColor.A == 0 {
			t.Fatalf("level %d must not be invisible", level)
		}
	}

	if _, _, _, _, c := elevationShadow(light, elevNone); c.A != 0 {
		t.Fatalf("elevNone must be transparent, got %v", c)
	}
	if _, _, _, _, c := elevationShadow(light, 99); c.A == 0 {
		t.Fatal("out-of-range level must clamp to elev4, not vanish")
	}
	if _, _, _, _, c := elevationShadow(nil, elev2); c.A != 0 {
		t.Fatal("a nil theme must resolve to no shadow")
	}
	if surfaceHighlight(light).A <= surfaceHighlight(dark).A {
		t.Fatal("the light-mode card highlight must be stronger than the dark one")
	}
}

// TestDensityMetrics pins the numbers layout.go's Preferred* formulas build on,
// so a view cannot quietly change the capsule's rhythm.
func TestDensityMetrics(t *testing.T) {
	cases := []struct {
		name string
		got  float32
		want float32
	}{
		{"headerHeight", headerHeight, 40},
		{"searchRowHeight", searchRowHeight, 52},
		{"rowHeightOmnibox", rowHeightOmnibox, 44},
		{"footerHeight", footerHeight, 44},
		{"controlHeight", controlHeight, 28},
		{"cardPadX", cardPadX, 12},
		{"cardPadY", cardPadY, 10},
		{"cardGap", cardGap, 10},
		{"railWidth", railWidth, 200},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestOpacityIsNotExpressedAsSpacing bans the "spaceN/spaceM" idiom for alpha
// and mix ratios. It hides an opacity inside the spacing ramp, so a spacing
// change silently retints the UI, and it reads as geometry when it is not.
func TestOpacityIsNotExpressedAsSpacing(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	ratio := regexp.MustCompile(`(?:Alpha\(\s*space\d+\s*/|Mix\([^)]*,\s*space\d+\s*/)`)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "tokens.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if loc := ratio.FindIndex(src); loc != nil {
			t.Fatalf("%s expresses opacity as a spacing ratio (%q); use an alpha* token from tokens.go",
				name, src[loc[0]:loc[1]])
		}
	}
}
