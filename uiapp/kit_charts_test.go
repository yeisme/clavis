package uiapp

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func tickLabels(ticks []chart.Tick) []string {
	out := make([]string, len(ticks))
	for i, tick := range ticks {
		out[i] = tick.Label
	}
	return out
}

func TestChartShortDay(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2026-11-19", "11/19"},
		{"2026-01-05", "1/5"}, // no zero padding, like clipboard.row.date
		{"2026-11-1", "11/1"}, // tolerant of unpadded rows
		{" 2026-12-31 ", "12/31"},
		{"7", "7"}, // not a date: the row still labels its column
		{"2026-13-02", "2026-13-02"},
		{"", ""},
	} {
		if got := chartShortDay(tc.in); got != tc.want {
			t.Errorf("chartShortDay(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestChartThinTicksKeepsTheNewestLabel(t *testing.T) {
	ticks := make([]chart.Tick, 7)
	for i := range ticks {
		ticks[i] = chart.Tick{Label: fmtInt(i)}
	}
	for _, tc := range []struct {
		every int
		want  []string
	}{
		{0, []string{"0", "1", "2", "3", "4", "5", "6"}},
		{1, []string{"0", "1", "2", "3", "4", "5", "6"}},
		{2, []string{"0", "2", "4", "6"}},
		{3, []string{"0", "3", "6"}},
		{4, []string{"2", "6"}},
		{9, []string{"6"}},
	} {
		if got := tickLabels(chartThinTicks(ticks, tc.every)); !slices.Equal(got, tc.want) {
			t.Errorf("every %d kept %v, want %v", tc.every, got, tc.want)
		}
	}
	if got := chartThinTicks(nil, 3); len(got) != 0 {
		t.Errorf("no ticks stay none, got %v", got)
	}
}

func TestChartTokenScaleHasThreeToFourRoundTicks(t *testing.T) {
	for _, tc := range []struct {
		max    int
		domain float64
		labels []string
	}{
		{91200, 100000, []string{"0", "50k", "100k"}},
		{8700, 10000, []string{"0", "5k", "10k"}},
		{14000, 15000, []string{"0", "5k", "10k", "15k"}},
		{3, 3, []string{"0", "1", "2", "3"}},
		{2300000, 3000000, []string{"0", "1M", "2M", "3M"}},
	} {
		scale, ticks := chartTokenScale(tc.max)
		if scale.Lo != 0 || scale.Hi != tc.domain {
			t.Errorf("max %d: domain [%v, %v], want [0, %v]", tc.max, scale.Lo, scale.Hi, tc.domain)
		}
		if got := tickLabels(ticks); !slices.Equal(got, tc.labels) {
			t.Errorf("max %d: ticks %v, want %v", tc.max, got, tc.labels)
		}
		if n := len(ticks); n < 3 || n > 4 {
			t.Errorf("max %d: %d ticks, want 3-4", tc.max, n)
		}
		// The tallest stack always fits under the top tick.
		if scale.At(float64(tc.max)) > 1 {
			t.Errorf("max %d overflows its own scale", tc.max)
		}
		if first, last := ticks[0], ticks[len(ticks)-1]; first.At != 0 || last.At != 1 {
			t.Errorf("max %d: ticks span %v..%v, want 0..1", tc.max, first.At, last.At)
		}
	}
}

func TestChartTokenScaleWithNoTokensKeepsABaseline(t *testing.T) {
	scale, ticks := chartTokenScale(0)
	if scale.Hi <= scale.Lo {
		t.Fatalf("an empty scale must still map: [%v, %v]", scale.Lo, scale.Hi)
	}
	if got := tickLabels(ticks); !slices.Equal(got, []string{"0"}) {
		t.Fatalf("ticks = %v, want just the baseline", got)
	}
	if ticks[0].At != 0 {
		t.Fatalf("baseline tick at %v, want 0", ticks[0].At)
	}
}

func TestChartCountScaleUsesWholeNumbers(t *testing.T) {
	for _, tc := range []struct {
		max    int
		labels []string
	}{
		{44, []string{"0", "20", "40", "60"}},
		{4, []string{"0", "4"}},
		{3, []string{"0", "3"}},
		{1, []string{"0", "2"}}, // a count of 1 never produces 0.5 steps
		{0, []string{"0"}},
	} {
		_, ticks := chartCountScale(tc.max)
		if got := tickLabels(ticks); !slices.Equal(got, tc.labels) {
			t.Errorf("max %d: ticks %v, want %v", tc.max, got, tc.labels)
		}
	}
}

func TestChartPercentTicks(t *testing.T) {
	ticks := chartPercentTicks()
	if got := tickLabels(ticks); !slices.Equal(got, []string{"0%", "50%", "100%"}) {
		t.Fatalf("ticks = %v", got)
	}
	for i, want := range []float32{0, 0.5, 1} {
		if ticks[i].At != want {
			t.Errorf("tick %d at %v, want %v", i, ticks[i].At, want)
		}
	}
}

func TestChartBandSlotsTileThePlot(t *testing.T) {
	x := chart.NewBand("a", "b", "c", "d", "e", "f", "g").Padding(chartColumnInner, chartColumnOuter)
	prev := chart.Span{}
	for i := 0; i < 7; i++ {
		slot := chartBandSlot(x, i)
		if math.Abs(float64(slot.Mid()-x.At(i))) > 1e-5 {
			t.Errorf("slot %d is centred at %v, its bar at %v", i, slot.Mid(), x.At(i))
		}
		if bar := x.Band(i); slot.Lo > bar.Lo || slot.Hi < bar.Hi {
			t.Errorf("slot %d %v does not contain its bar %v", i, slot, bar)
		}
		if i > 0 && math.Abs(float64(slot.Lo-prev.Hi)) > 1e-5 {
			t.Errorf("slots %d and %d leave a hole: %v then %v", i-1, i, prev, slot)
		}
		prev = slot
	}
}

func TestChartPercentsAddUpToOneHundred(t *testing.T) {
	for _, values := range [][]float64{
		{1, 1, 1},
		{214, 38, 12, 3},
		{2, 1, 4},
		{1, 2, 3, 4, 5, 6, 7},
		{0.5, 0.25, 0.25},
		{7},
	} {
		sum := 0
		for _, p := range chartPercents(values) {
			sum += p
		}
		if sum != 100 {
			t.Errorf("%v rounds to %v, which adds up to %d", values, chartPercents(values), sum)
		}
	}
	if got := chartPercents([]float64{1, 1, 1}); !slices.Equal(got, []int{34, 33, 33}) {
		t.Errorf("three equal parts = %v, want [34 33 33] (largest remainder, first wins ties)", got)
	}
	if got := chartPercents([]float64{2, 1, 4}); !slices.Equal(got, []int{29, 14, 57}) {
		t.Errorf("2:1:4 = %v, want [29 14 57]", got)
	}
	if got := chartPercents([]float64{0, -3, 6}); !slices.Equal(got, []int{0, 0, 100}) {
		t.Errorf("non-positive values must take no share, got %v", got)
	}
	if got := chartPercents(nil); len(got) != 0 {
		t.Errorf("nil in, nil out, got %v", got)
	}
	if got := chartPercents([]float64{0, 0}); !slices.Equal(got, []int{0, 0}) {
		t.Errorf("all-zero = %v", got)
	}
}

func TestChartSegmentWidths(t *testing.T) {
	sum := func(ws []float32) (total float32) {
		for _, w := range ws {
			total += w
		}
		return total
	}
	near := func(a, b float32) bool { return math.Abs(float64(a-b)) < 0.01 }

	// Proportional, with the gaps taken out of the width, not added to it.
	ws := chartSegmentWidths([]float64{2, 1, 4}, 300, 2, 4)
	if !near(sum(ws)+2*2, 300) {
		t.Errorf("2:1:4 widths %v plus 2 gaps = %v, want 300", ws, sum(ws)+4)
	}
	if !near(ws[0]*2, ws[2]) || !near(ws[1]*4, ws[2]) {
		t.Errorf("2:1:4 widths %v are not proportional", ws)
	}

	// A sliver is held at the minimum and the rest share what is left.
	ws = chartSegmentWidths([]float64{214, 38, 12, 3}, 240, 2, 4)
	if ws[3] != 4 {
		t.Errorf("the 3-item sliver is %v wide, want the 4dp minimum", ws[3])
	}
	if !near(sum(ws)+3*2, 240) {
		t.Errorf("widths %v plus 3 gaps = %v, want 240", ws, sum(ws)+6)
	}
	if !near(ws[0]/ws[1], 214.0/38.0) {
		t.Errorf("the big parts must keep their proportion: %v", ws)
	}

	// Parts at zero (or negative) draw nothing, and take no gap.
	ws = chartSegmentWidths([]float64{0, 5, -1, 5}, 102, 2, 4)
	if ws[0] != 0 || ws[2] != 0 || !near(ws[1], 50) || !near(ws[3], 50) {
		t.Errorf("zero parts must vanish: %v", ws)
	}

	// Degenerate inputs never panic and never invent width.
	for _, tc := range []struct {
		name   string
		values []float64
		width  float32
	}{
		{"empty", nil, 100},
		{"all zero", []float64{0, 0}, 100},
		{"no width", []float64{1, 2}, 0},
		{"width below the gaps", []float64{1, 1, 1}, 3},
	} {
		for _, w := range chartSegmentWidths(tc.values, tc.width, 2, 4) {
			if w != 0 {
				t.Errorf("%s: got width %v, want none", tc.name, w)
			}
		}
	}
}

func TestChartInlineFitsPrintsOnlyWhatFits(t *testing.T) {
	// Two big blocks print their count; a sliver leaves it to the legend.
	fits := chartInlineFits([]float64{100, 100, 1}, []string{"100", "100", "1"}, typeMeta)
	if !fits[0] || !fits[1] {
		t.Errorf("wide blocks must print: %v", fits)
	}
	if fits[2] {
		t.Errorf("a sliver of one percent must not print its count: %v", fits)
	}
	// A part with no text, or no size, never "fits".
	fits = chartInlineFits([]float64{5, 0}, []string{"", "7"}, typeMeta)
	if fits[0] || fits[1] {
		t.Errorf("empty text or zero size must not fit: %v", fits)
	}
	// The estimate is judged at the narrowest bar, so a label that fits there
	// fits at every width the hubs lay out.
	narrow := chartSegmentWidths([]float64{2, 1, 4}, chartRefBarWidth, chartGapSize, chartMinBlock)
	for i, text := range []string{"2", "1", "4"} {
		if narrow[i] < chartTextWidth(text, typeMeta)+2*chartInlinePad {
			t.Errorf("block %d (%q) is %vdp at the reference width: too narrow for its label", i, text, narrow[i])
		}
	}
}

func TestChartLatestIndexSkipsGaps(t *testing.T) {
	nan := math.NaN()
	for _, tc := range []struct {
		in   []float64
		want int
	}{
		{[]float64{0.5, 0.6, 0.7}, 2},
		{[]float64{0.5, nan, nan}, 0},
		{[]float64{nan, nan}, -1},
		{nil, -1},
		{[]float64{0, nan, 0}, 2}, // a real 0% is an observation, a gap is not
	} {
		if got := chartLatestIndex(tc.in); got != tc.want {
			t.Errorf("chartLatestIndex(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestChartInkOnPicksWhiteOnBlueAndDarkOnLight(t *testing.T) {
	light, dark := ui.LightTheme(), ui.DarkTheme()
	pal := newChartPalette(light)
	for _, tc := range []struct {
		name string
		fill ui.Color
		want ui.Color
	}{
		{"accent blue", chartAccent, chartInkWhite},
		{"system orange (light)", kitStateWarningColor(light), chartInkNear},
		{"system orange (dark)", kitStateWarningColor(dark), chartInkNear},
		{"strong gray (light)", pal.strong, chartInkNear},
		{"soft gray (light)", pal.soft, chartInkNear},
		{"near-black card", ui.Hex("#1C1C1E"), chartInkWhite},
	} {
		if got := chartInkOn(tc.fill); got != tc.want {
			t.Errorf("%s: ink %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestChartPaletteIsGrayscalePlusOneAccent(t *testing.T) {
	for _, theme := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		pal := newChartPalette(theme)
		if pal.accent != ui.Hex("#0A84FF") {
			t.Errorf("accent = %v, want #0A84FF", pal.accent)
		}
		// History marks are neutral: no channel stands out.
		for name, c := range map[string]ui.Color{"strong": pal.strong, "soft": pal.soft} {
			spread := max(c.R, c.G, c.B) - min(c.R, c.G, c.B)
			if spread > 12 {
				t.Errorf("dark=%v %s step %v is not neutral (spread %d)", theme.Dark, name, c, spread)
			}
		}
		// Two steps must be distinguishable from each other and from the card.
		if contrastRatio(pal.strong, pal.surface) < 2.5 {
			t.Errorf("dark=%v strong step has only %.2f:1 against the card", theme.Dark, contrastRatio(pal.strong, pal.surface))
		}
		if pal.strong == pal.soft || pal.soft == pal.surface {
			t.Errorf("dark=%v steps collapse: strong %v soft %v surface %v", theme.Dark, pal.strong, pal.soft, pal.surface)
		}
		// The stronger step is the more prominent one in both modes.
		if contrastRatio(pal.strong, pal.surface) <= contrastRatio(pal.soft, pal.surface) {
			t.Errorf("dark=%v the strong step must out-contrast the soft one", theme.Dark)
		}
		// Today's secondary step is blue too, and lighter than the accent in prominence.
		if pal.accentSoft.B <= pal.accentSoft.R {
			t.Errorf("dark=%v accentSoft %v is not blue", theme.Dark, pal.accentSoft)
		}
		if contrastRatio(pal.accent, pal.surface) <= contrastRatio(pal.accentSoft, pal.surface) {
			t.Errorf("dark=%v the accent must out-contrast its soft step", theme.Dark)
		}
		// The surfaces gaps and tooltips are cut from are opaque.
		if pal.surface.A != 255 || pal.tooltip.A != 255 || pal.baseline.A != 255 {
			t.Errorf("dark=%v opaque surfaces expected: surface %d tooltip %d baseline %d", theme.Dark, pal.surface.A, pal.tooltip.A, pal.baseline.A)
		}
		// Gridlines are recessive: lighter than the baseline.
		if pal.grid.A >= pal.baseline.A {
			t.Errorf("dark=%v gridlines (alpha %d) must be fainter than the baseline (alpha %d)", theme.Dark, pal.grid.A, pal.baseline.A)
		}
	}
}

// ---- AI usage -------------------------------------------------------------

func TestAIUsageColumnsStackOutputUnderInput(t *testing.T) {
	days := hubFixtureAIDays()
	cols := aiUsageColumns(days)
	if len(cols) != 7 {
		t.Fatalf("columns = %d, want 7", len(cols))
	}
	series := [][]float64{make([]float64, 7), make([]float64, 7)}
	for i, col := range cols {
		series[0][i], series[1][i] = col.Values[0], col.Values[1]
		if want := chartShortDay(days[i].Day); col.Label != want {
			t.Errorf("column %d label %q, want %q", i, col.Label, want)
		}
	}
	stacked := chart.Stack(series, chart.StackStandard)
	for i, day := range days {
		out, in := stacked[aiSeriesOutput][i], stacked[aiSeriesInput][i]
		if out.Lo != 0 || out.Hi != float64(day.OutputTokens) {
			t.Errorf("day %d output segment %+v, want 0..%d (grows from the baseline)", i, out, day.OutputTokens)
		}
		if in.Lo != out.Hi || in.Hi != float64(aiUsageTokens(day)) {
			t.Errorf("day %d input segment %+v, want %d..%d (piled on the output)", i, in, day.OutputTokens, aiUsageTokens(day))
		}
	}
}

func TestAIUsageHubHelpers(t *testing.T) {
	days := hubFixtureAIDays()
	if got := aiUsageMaxTokens(days); got != 66300+24900 {
		t.Errorf("aiUsageMaxTokens = %d, want %d", got, 66300+24900)
	}
	if got := aiUsageLastDays(append(testAIUsageDays(1, 2, 3), days...), 7); len(got) != 7 || got[0].Day != days[0].Day {
		t.Errorf("aiUsageLastDays must keep the newest 7, got %d starting %q", len(got), got[0].Day)
	}
	if got := aiUsageLastDays(days[:3], 7); len(got) != 3 {
		t.Errorf("a short window is kept whole, got %d", len(got))
	}
	pal := newChartPalette(ui.LightTheme())
	if got := aiUsageDayFill(pal, aiSeriesOutput, true); got != pal.accent {
		t.Errorf("today's output = %v, want the accent", got)
	}
	if got := aiUsageDayFill(pal, aiSeriesInput, true); got != pal.accentSoft {
		t.Errorf("today's input = %v, want the lighter blue step", got)
	}
	if got := aiUsageDayFill(pal, aiSeriesOutput, false); got != pal.strong {
		t.Errorf("history output = %v, want the stronger gray", got)
	}
	if got := aiUsageDayFill(pal, aiSeriesInput, false); got != pal.soft {
		t.Errorf("history input = %v, want the lighter gray", got)
	}
	if got := aiUsageDescribe(days[6]); got != "11/20: Input 27.4k, Result 9.8k, 12 req" {
		t.Errorf("aiUsageDescribe = %q", got)
	}
	in, out := aiUsageSeriesNames()
	if in != "Input" || out != "Result" {
		t.Errorf("series names = %q/%q", in, out)
	}
}

func TestAIUsageSeriesNamesAreBilingual(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	for _, tc := range []struct {
		lang      i18n.Language
		in, out   string
		describes string
	}{
		{i18n.English, "Input", "Result", "11/20: Input 27.4k, Result 9.8k, 12 req"},
		{i18n.Chinese, "输入", "结果", "11/20: 输入 27.4k, 结果 9.8k, 12 req"},
	} {
		i18n.SetLanguage(tc.lang)
		in, out := aiUsageSeriesNames()
		if in != tc.in || out != tc.out {
			t.Errorf("%s names = %q/%q, want %q/%q", tc.lang, in, out, tc.in, tc.out)
		}
		// The Chinese legend must never read "输出方式" (output mode).
		if strings.Contains(out, "方式") {
			t.Errorf("%s output series name %q is the workflow setting's, not the model's result", tc.lang, out)
		}
		if got := aiUsageDescribe(hubFixtureAIDays()[6]); got != tc.describes {
			t.Errorf("%s describe = %q, want %q", tc.lang, got, tc.describes)
		}
	}
}

// ---- clipboard ------------------------------------------------------------

func TestClipboardKindsOrderedFollowTheFilterOrder(t *testing.T) {
	got := clipboardKindsOrdered([]storage.ClipboardKindStat{
		{Kind: "video", Count: 3}, {Kind: "text", Count: 214}, {Kind: "file", Count: 0},
		{Kind: "image", Count: 38}, {Kind: "link", Count: 5}, {Kind: "file", Count: 12},
	})
	var kinds []string
	for _, k := range got {
		kinds = append(kinds, k.Kind)
	}
	if want := []string{"image", "text", "file", "link", "video"}; !slices.Equal(kinds, want) {
		t.Errorf("order = %v, want %v (zero rows dropped, clipboardKindOrder first)", kinds, want)
	}
}

func TestClipboardKindIconsMatchTheListRows(t *testing.T) {
	for kind, want := range map[string]string{
		"image": "photo", "video": "film", "file": "doc", "link": "link", "text": "doc.text", "Image": "photo", "": "doc.text",
	} {
		if got := clipboardKindIcon(kind); got != want {
			t.Errorf("clipboardKindIcon(%q) = %q, want %q", kind, got, want)
		}
		// Every icon must exist in the icon set (no silent question-mark fallback).
		if _, ok := semanticIconBodies[clipboardKindIcon(kind)]; !ok {
			t.Errorf("icon %q for kind %q is not in the icon set", clipboardKindIcon(kind), kind)
		}
	}
	if got := clipboardKindLabel("video"); got != MediaCategoryLabel(MediaCategoryVideo) {
		t.Errorf("video label = %q, want the media vocabulary's %q", got, MediaCategoryLabel(MediaCategoryVideo))
	}
}

func TestClipboardCaptureColumns(t *testing.T) {
	days := hubFixtureClipboardState("").days
	cols := clipboardCaptureColumns(append([]storage.ClipboardDay{{Day: "2026-11-01", Count: 99}}, days...))
	if len(cols) != 7 {
		t.Fatalf("columns = %d, want the newest 7", len(cols))
	}
	for i, col := range cols {
		if len(col.Values) != 1 || col.Values[0] != float64(days[i].Count) {
			t.Errorf("column %d = %+v, want a single series of %d", i, col, days[i].Count)
		}
	}
	if clipboardCapturesMax(days) != 44 {
		t.Errorf("clipboardCapturesMax = %d, want 44", clipboardCapturesMax(days))
	}
}

// ---- lexicon --------------------------------------------------------------

func TestLexiconHitSeriesLeavesGapsNotZeros(t *testing.T) {
	days := hubFixtureLexiconDays()
	series := lexiconHitSeries(days)
	if len(series) != 14 {
		t.Fatalf("series = %d points, want 14", len(series))
	}
	for i, day := range days {
		quiet := day.Hits+day.Misses == 0
		if math.IsNaN(series[i]) != quiet {
			t.Errorf("day %d (%+v): NaN=%v, want %v (a day with no events is a gap)", i, day, math.IsNaN(series[i]), quiet)
		}
		if !quiet {
			if want := float64(day.Hits) / float64(day.Hits+day.Misses); math.Abs(series[i]-want) > 1e-6 {
				t.Errorf("day %d rate %v, want %v", i, series[i], want)
			}
		}
	}
	// A genuine 0% day (all misses) is an observation, not a gap.
	if got := lexiconHitSeries([]LexiconHitDay{{Hits: 0, Misses: 4}}); math.IsNaN(got[0]) || got[0] != 0 {
		t.Errorf("all misses = %v, want a real 0", got)
	}
	if latest := chartLatestIndex(series); latest != 13 {
		t.Errorf("latest observation = %d, want 13", latest)
	}
}

func TestLexiconRanks(t *testing.T) {
	rows := lexiconRanks([]LexiconTopWord{
		{Word: "alpha", UseCount: 40}, {Word: "  ", UseCount: 9}, {Word: "beta", UseCount: 0},
		{Word: " gamma ", UseCount: 10}, {Word: "delta", UseCount: 4},
	}, 5)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want the 3 real words", rows)
	}
	if rows[1].Label != "gamma" || rows[0].Frac != 1 || rows[1].Frac != 0.25 || rows[2].Frac != 0.1 {
		t.Errorf("rows = %+v", rows)
	}
	if got := lexiconRanks(hubFixtureTopWords(), 3); len(got) != 3 {
		t.Errorf("limit 3 gave %d rows", len(got))
	}
	if got := lexiconRanks(nil, 5); len(got) != 0 {
		t.Errorf("no words, no rows: %v", got)
	}
}

func TestLexiconTopCaptionIsBilingualWithoutItsColon(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.English)
	if got := lexiconTopCaption(); got != "Top" {
		t.Errorf("EN caption = %q, want Top", got)
	}
	i18n.SetLanguage(i18n.Chinese)
	if got := lexiconTopCaption(); got != "高频词" {
		t.Errorf("ZH caption = %q, want 高频词", got)
	}
}

// ---- agent ----------------------------------------------------------------

func TestAgentLanePartsAddUpToTheSessionTotal(t *testing.T) {
	counts := countAgentLanes(hubFixtureAgentSessions())
	parts := agentLaneParts(counts)
	total := 0
	var kinds []agentLaneKind
	for _, part := range parts {
		total += part.Count
		kinds = append(kinds, part.Kind)
	}
	if total != counts.Total {
		t.Errorf("blocks add up to %d, the card headlines %d", total, counts.Total)
	}
	if want := []agentLaneKind{agentLaneRunning, agentLaneApproval, agentLaneDone}; !slices.Equal(kinds, want) {
		t.Errorf("lanes = %v, want %v", kinds, want)
	}

	// Idle sessions (not running, awaiting nothing, not finished) still count.
	withIdle := append(hubFixtureAgentSessions(), agentsession.Session{ID: "w1", Status: agentsession.StatusWaiting})
	idleCounts := countAgentLanes(withIdle)
	idle := agentLaneParts(idleCounts)
	sum := 0
	for _, part := range idle {
		sum += part.Count
	}
	if sum != idleCounts.Total || idle[len(idle)-1].Kind != agentLaneWaiting {
		t.Errorf("idle lane missing: %+v (total %d)", idle, idleCounts.Total)
	}

	if got := agentLaneParts(agentLaneCounts{}); len(got) != 0 {
		t.Errorf("no sessions, no blocks: %v", got)
	}
}

func TestAgentLaneColorsAndIcons(t *testing.T) {
	light, dark := ui.LightTheme(), ui.DarkTheme()
	for _, theme := range []*ui.Theme{light, dark} {
		pal := newChartPalette(theme)
		if got := agentLaneFill(theme, pal, agentLaneRunning); got != ui.Hex("#0A84FF") {
			t.Errorf("running = %v, want the accent", got)
		}
		if got := agentLaneFill(theme, pal, agentLaneDone); got != pal.strong {
			t.Errorf("done = %v, want neutral gray", got)
		}
	}
	if got := agentLaneFill(light, newChartPalette(light), agentLaneApproval); got != ui.Hex("#FF9500") {
		t.Errorf("approval (light) = %v, want #FF9500", got)
	}
	if got := agentLaneFill(dark, newChartPalette(dark), agentLaneApproval); got != ui.Hex("#FF9F0A") {
		t.Errorf("approval (dark) = %v, want #FF9F0A", got)
	}
	seen := map[string]agentLaneKind{}
	for _, kind := range []agentLaneKind{agentLaneRunning, agentLaneApproval, agentLaneDone, agentLaneWaiting} {
		icon := agentLaneIcon(kind)
		if _, ok := semanticIconBodies[icon]; !ok {
			t.Errorf("lane %q icon %q is not in the icon set", kind, icon)
		}
		if other, dup := seen[icon]; dup {
			t.Errorf("lanes %q and %q share the icon %q: a lane must be identifiable without its color", other, kind, icon)
		}
		seen[icon] = kind
	}
}

func TestAgentLaneNamesMatchTheAgentTab(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.English)
	for kind, want := range map[agentLaneKind]string{
		agentLaneRunning: "Running", agentLaneApproval: "Approval", agentLaneDone: "Done", agentLaneWaiting: "Waiting",
	} {
		if got := agentLaneName(kind); got != want {
			t.Errorf("EN %q = %q, want %q", kind, got, want)
		}
	}
	i18n.SetLanguage(i18n.Chinese)
	for kind, want := range map[agentLaneKind]string{
		agentLaneRunning: "运行中", agentLaneApproval: "待审批", agentLaneDone: "已完成", agentLaneWaiting: "等待中",
	} {
		if got := agentLaneName(kind); got != want {
			t.Errorf("ZH %q = %q, want %q", kind, got, want)
		}
	}
	for _, name := range []string{agentLaneName(agentLaneRunning), agentLaneName(agentLaneApproval)} {
		if strings.ContainsAny(name, "●▲✓○ ") {
			t.Errorf("lane name %q still carries its symbol", name)
		}
	}
}

// ---- rendered Stats hub ---------------------------------------------------

// statsHub is a Capsule opened on the Stats hub with deterministic snapshots
// injected through the controller's own cache seams.
type statsHub struct {
	ctrl  *AppController
	tt    *ui.Tester
	theme *ui.Theme
	w, h  int
}

// newStatsHub renders the hub at the real Omnibox-sized Capsule (680x536, or
// 720x536 in Chinese). It waits for the startup Agent discovery to settle first
// so a background refresh cannot overwrite the seeded sessions mid-test.
func newStatsHub(t *testing.T, dark bool, seed func(*AppController), mutate ...func(*config.MainConfig)) *statsHub {
	t.Helper()
	f := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) { return sseResponse("ok"), nil }})
	// Generous on purpose: waitUntil returns the moment discovery settles, and the
	// race detector slows the startup scan by an order of magnitude.
	waitUntil(t, agentSettleTimeout, func() bool {
		r := f.ctrl.agentRecordedOperationResult()
		return !f.ctrl.agentRefreshing() && r.State != "" && r.State != host.StateRefreshing
	}, "startup agent refresh settle")
	for _, fn := range mutate {
		if err := f.ctrl.UpdateMainConfig(fn); err != nil {
			t.Fatal(err)
		}
	}
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: &engine.CapsuleContext{AppName: "Notes"}})
	seed(f.ctrl)
	f.ctrl.SetHubSurface(HubSurfaceStats)
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	hub := &statsHub{ctrl: f.ctrl, w: w, h: h}
	hub.tt = ui.NewTester(func(c *ui.Context) {
		f.ctrl.CapsuleView(c)
		hub.theme = c.Theme()
	}, w, h)
	hub.tt.SetDark(dark)
	hub.tt.Frame()
	hub.tt.Advance(2 * time.Second) // let the Capsule's enter transition settle
	hub.tt.Frame()
	return hub
}

func seedLoadedHub(c *AppController) { hubSeedLoaded(c, "") }

func (s *statsHub) rect(t *testing.T, name string) ui.Rect {
	t.Helper()
	r, ok := s.tt.Find(name)
	if !ok {
		t.Fatalf("%q not found in the hub; texts: %v", name, s.tt.Texts())
	}
	return r
}

func (s *statsHub) has(name string) bool {
	_, ok := s.tt.Find(name)
	return ok
}

// px is the rendered color at a point, at scale 1 so DIPs are pixels.
func (s *statsHub) px(x, y float32) ui.Color { return rgbaAt(s.tt.Image(), int(x), int(y)) }

func (s *statsHub) center(t *testing.T, name string) ui.Color {
	t.Helper()
	r := s.rect(t, name)
	return s.px(r.X+r.W/2, r.Y+r.H/2)
}

// chartPixelNear compares two colors channel by channel.
func chartPixelNear(a, b ui.Color, tol int) bool {
	d := func(x, y uint8) int { return int(math.Abs(float64(x) - float64(y))) }
	return d(a.R, b.R) <= tol && d(a.G, b.G) <= tol && d(a.B, b.B) <= tol
}

func within(inner, outer ui.Rect, slack float32) bool {
	return inner.X >= outer.X-slack && inner.Y >= outer.Y-slack &&
		inner.X+inner.W <= outer.X+outer.W+slack && inner.Y+inner.H <= outer.Y+outer.H+slack
}

func disjoint(a, b ui.Rect) bool {
	return a.X+a.W <= b.X+0.5 || b.X+b.W <= a.X+0.5 || a.Y+a.H <= b.Y+0.5 || b.Y+b.H <= a.Y+0.5
}

func TestStatsHubCardsAreEqualAndWholeAtTheCapsuleSize(t *testing.T) {
	cardNames := []string{"stats-card-ai", "stats-card-clipboard", "stats-card-lexicon", "stats-card-agent"}
	for _, tc := range []struct {
		name string
		lang i18n.Language
		dark bool
		wide int
	}{
		{"en light", i18n.English, false, 680},
		{"en dark", i18n.English, true, 680},
		{"zh light", i18n.Chinese, false, 720},
		{"zh dark", i18n.Chinese, true, 720},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer i18n.SetLanguage(i18n.English)
			i18n.SetLanguage(tc.lang)
			s := newStatsHub(t, tc.dark, seedLoadedHub)
			if s.w != tc.wide {
				t.Fatalf("capsule width = %d, want %d", s.w, tc.wide)
			}
			// The headline figures are plain text (never labelled over), found by it.
			count, bytes := clipboardStatsTotals(hubFixtureClipboardState("").kinds)
			rate, _ := lexiconOverallHitRate(hubFixtureLexiconDays())
			kpis := map[string]string{
				"stats-card-ai":        aiUsageTodayLabel(hubFixtureAIDays()),
				"stats-card-clipboard": clipboardStatsCountLabel(count, bytes),
				"stats-card-lexicon":   i18n.Tf("stats.card.lexicon.hit_rate", rate),
				"stats-card-agent":     i18n.Tf("stats.card.agent.sessions", len(hubFixtureAgentSessions())),
			}
			inside := map[string][]string{
				"stats-card-ai":        {kpis["stats-card-ai"], "stats-ai-legend", "stats-ai-chart"},
				"stats-card-clipboard": {kpis["stats-card-clipboard"], "stats-clip-bar", "stats-clip-legend", "stats-clip-chart"},
				"stats-card-lexicon":   {kpis["stats-card-lexicon"], "stats-lex-chart", "stats-lex-top"},
				"stats-card-agent":     {kpis["stats-card-agent"], "stats-agent-bar", "stats-agent-legend"},
			}
			scroll := s.rect(t, "stats-scroll")
			cards := make([]ui.Rect, len(cardNames))
			for i, name := range cardNames {
				cards[i] = s.rect(t, name)
				if cards[i].H != statsCardHeight {
					t.Errorf("%s is %.1f tall, want every card %v", name, cards[i].H, statsCardHeight)
				}
				if !within(cards[i], ui.Rect{W: float32(s.w), H: float32(s.h)}, 0.5) {
					t.Errorf("%s %+v leaves the %dx%d window", name, cards[i], s.w, s.h)
				}
				for _, id := range inside[name] {
					if r := s.rect(t, id); !within(r, cards[i], 0.5) {
						t.Errorf("%q %+v hangs out of %s %+v", id, r, name, cards[i])
					}
				}
			}
			// 2x2, nothing overlapping.
			for i := range cards {
				for j := i + 1; j < len(cards); j++ {
					if !disjoint(cards[i], cards[j]) {
						t.Errorf("%s and %s overlap: %+v %+v", cardNames[i], cardNames[j], cards[i], cards[j])
					}
				}
			}
			if cards[1].Y != cards[0].Y || cards[3].Y != cards[2].Y || cards[2].Y < cards[0].Y+cards[0].H {
				t.Errorf("cards are not a 2x2 grid: %+v", cards)
			}
			// The whole second row is visible: the grid fits the viewport, so
			// nothing is clipped and nothing needs a scroll.
			if bottom := cards[3].Y + cards[3].H; bottom > scroll.Y+scroll.H-1 {
				t.Errorf("the second row ends at %.1f, too near (or below) the viewport bottom %.1f", bottom, scroll.Y+scroll.H)
			}
			// Text and marks that share a row never collide.
			ai, today, legend := s.rect(t, "stats-ai-chart-area"), s.rect(t, kpis["stats-card-ai"]), s.rect(t, "stats-ai-legend")
			if legend.X < today.X+today.W || legend.Y+legend.H > ai.Y {
				t.Errorf("AI legend %+v collides with the KPI %+v or the plot %+v", legend, today, ai)
			}
			bar, key, chartR := s.rect(t, "stats-clip-bar"), s.rect(t, "stats-clip-legend"), s.rect(t, "stats-clip-chart")
			if key.X+key.W > chartR.X || bar.Y+bar.H > key.Y {
				t.Errorf("clipboard legend %+v collides with the chart %+v or the bar %+v", key, chartR, bar)
			}
			line, top := s.rect(t, "stats-lex-chart"), s.rect(t, "stats-lex-top")
			if line.X+line.W > top.X {
				t.Errorf("lexicon line %+v collides with the ranked list %+v", line, top)
			}
			if lane, rows := s.rect(t, "stats-agent-bar"), s.rect(t, "stats-agent-legend"); lane.Y+lane.H > rows.Y {
				t.Errorf("agent bar %+v collides with its legend %+v", lane, rows)
			}
		})
	}
}

func TestStatsHubCardsKeepTheirHeightAndScrollInASmallerCapsule(t *testing.T) {
	// MaxVisibleResults 3 is the smallest Capsule a user can ask for. The grid
	// must keep its 2x2 layout and scroll, never squeeze the rows into a clip.
	s := newStatsHub(t, false, seedLoadedHub, func(m *config.MainConfig) { m.Appearance.MaxVisibleResults = 3 })
	if s.h >= 536 {
		t.Fatalf("capsule height = %d, expected a smaller one", s.h)
	}
	scroll := s.rect(t, "stats-scroll")
	if scroll.H >= 2*statsCardHeight {
		t.Fatalf("the %.1f viewport is not smaller than the grid, nothing to prove", scroll.H)
	}
	top, second := s.rect(t, "stats-card-ai"), s.rect(t, "stats-card-lexicon")
	// Layout positions are not clipped: the second row starts one full card
	// and one gap below the first, however short the viewport is.
	if got, want := second.Y-top.Y, statsCardHeight+space10; got != want {
		t.Errorf("the second row starts %.1f below the first, want %.1f (rows must not be squeezed)", got, want)
	}
	// And the viewport really scrolls to it: wheel down, the last card shows.
	if r := s.rect(t, "stats-card-agent"); r.H > 0 && r.Y+r.H <= scroll.Y+scroll.H && r.H == statsCardHeight {
		t.Fatalf("the last card is whole before any scrolling in a %.1f viewport: %+v", scroll.H, r)
	}
	s.tt.Scroll(scroll.X+scroll.W/2, scroll.Y+scroll.H/2, 0, 400)
	s.tt.Frame()
	last := s.rect(t, "stats-card-agent")
	if last.H < 100 || last.Y+last.H > scroll.Y+scroll.H+0.5 {
		t.Errorf("scrolling must bring the last card into the viewport: %+v in %+v", last, scroll)
	}
}

func TestStatsHubCardsAreOpaqueSurfaces(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s := newStatsHub(t, dark, seedLoadedHub)
		want := chartOpaque(s.theme.Surface)
		for _, name := range []string{"stats-card-ai", "stats-card-clipboard", "stats-card-lexicon", "stats-card-agent"} {
			r := s.rect(t, name)
			// The bottom-right corner of the padding holds no content.
			if got := s.px(r.X+r.W-8, r.Y+r.H-6); !chartPixelNear(got, want, 2) {
				t.Errorf("dark=%v %s paints %v at its corner, want the opaque surface %v (no glass on chart cards)", dark, name, got, want)
			}
		}
	}
}

func TestStatsHubAIColumnsStackAlignedOnOneBaseline(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s := newStatsHub(t, dark, seedLoadedHub)
		pal := newChartPalette(s.theme)
		days := hubFixtureAIDays()
		scale, _ := chartTokenScale(aiUsageMaxTokens(days))
		area := s.rect(t, "stats-ai-chart-area")
		baseline := area.Y + area.H
		for i, day := range days {
			outID, inID := "stats-ai-chart-bar-"+fmtInt(i)+"-0", "stats-ai-chart-bar-"+fmtInt(i)+"-1"
			if aiUsageTokens(day) == 0 {
				if s.has(outID) || s.has(inID) {
					t.Errorf("dark=%v day %d has no tokens and must draw no bar", dark, i)
				}
				continue
			}
			out, in := s.rect(t, outID), s.rect(t, inID)
			// Heights are the data: tokens as a fraction of the nice scale.
			wantOut := area.H * scale.At(float64(day.OutputTokens))
			wantIn := area.H * (scale.At(float64(aiUsageTokens(day))) - scale.At(float64(day.OutputTokens)))
			if math.Abs(float64(out.H-wantOut)) > 0.75 || math.Abs(float64(in.H-wantIn)) > 0.75 {
				t.Errorf("dark=%v day %d: output %.2f input %.2f, want %.2f / %.2f", dark, i, out.H, in.H, wantOut, wantIn)
			}
			// One shared baseline, and the segments touch (the gap is a surface
			// rule drawn over their seam, never extra height).
			if math.Abs(float64(out.Y+out.H-baseline)) > 0.5 {
				t.Errorf("dark=%v day %d output ends at %.2f, the baseline is %.2f", dark, i, out.Y+out.H, baseline)
			}
			if math.Abs(float64(in.Y+in.H-out.Y)) > 0.5 {
				t.Errorf("dark=%v day %d input does not sit on its output: %+v over %+v", dark, i, in, out)
			}
			if in.W != out.W || in.X != out.X {
				t.Errorf("dark=%v day %d segments differ in x/width: %+v vs %+v", dark, i, in, out)
			}
			if out.W > 24 {
				t.Errorf("dark=%v day %d is %.1fdp thick; a column must stay <= 24dp", dark, i, out.W)
			}
		}

		img := s.tt.Image()
		sample := func(r ui.Rect) ui.Color { return rgbaAt(img, int(r.X+r.W/2), int(r.Y+r.H/2)) }
		contrast := func(c ui.Color) float64 { return contrastRatio(c, pal.surface) }
		for i, day := range days {
			if aiUsageTokens(day) == 0 {
				continue
			}
			out, in := s.rect(t, "stats-ai-chart-bar-"+fmtInt(i)+"-0"), s.rect(t, "stats-ai-chart-bar-"+fmtInt(i)+"-1")
			gotOut, gotIn := sample(out), sample(in)
			if i == len(days)-1 {
				if !chartPixelNear(gotOut, pal.accent, 3) {
					t.Errorf("dark=%v today's output is %v, want #0A84FF", dark, gotOut)
				}
				if !chartPixelNear(gotIn, pal.accentSoft, 3) {
					t.Errorf("dark=%v today's input is %v, want the lighter blue %v", dark, gotIn, pal.accentSoft)
				}
				continue
			}
			if !chartPixelNear(gotOut, pal.strong, 3) || !chartPixelNear(gotIn, pal.soft, 3) {
				t.Errorf("dark=%v day %d history = %v / %v, want neutral %v / %v", dark, i, gotOut, gotIn, pal.strong, pal.soft)
			}
			if int(gotOut.B)-int(gotOut.R) > 24 || int(gotIn.B)-int(gotIn.R) > 24 {
				t.Errorf("dark=%v day %d history is tinted blue: %v / %v", dark, i, gotOut, gotIn)
			}
			if contrast(gotOut) <= contrast(gotIn) {
				t.Errorf("dark=%v day %d: output must be the more prominent step", dark, i)
			}
		}

		// A 2dp gap in the surface color separates the stacked segments.
		out3, in3 := s.rect(t, "stats-ai-chart-bar-3-0"), s.rect(t, "stats-ai-chart-bar-3-1")
		seam := int(math.Round(float64(out3.Y)))
		cx := out3.X + out3.W/2
		if !chartPixelNear(s.px(cx, float32(seam)), pal.surface, 10) && !chartPixelNear(s.px(cx, float32(seam-1)), pal.surface, 10) {
			t.Errorf("dark=%v no surface gap at the seam y=%d: %v / %v", dark, seam, s.px(cx, float32(seam)), s.px(cx, float32(seam-1)))
		}
		// Rounded data end, square baseline: the top corner of the stack is
		// cut away, the bottom corner is filled.
		if got := s.px(in3.X+1, in3.Y); chartPixelNear(got, pal.soft, 6) {
			t.Errorf("dark=%v the top corner of the stack is square (%v); the data end must be rounded", dark, got)
		}
		if got := s.px(out3.X+1, out3.Y+out3.H-1); !chartPixelNear(got, pal.strong, 6) {
			t.Errorf("dark=%v the baseline corner is %v; the baseline end must be square and filled", dark, got)
		}
	}
}

func TestStatsHubAIChartAxesLegendAndNoCurrency(t *testing.T) {
	s := newStatsHub(t, false, seedLoadedHub)
	for _, want := range []string{
		"37.2k tok", "7d: 335k tok · 120 calls", // today + the existing summary key
		"0", "50k", "100k", // round y ticks through formatTokenCount
		"11/14", "11/15", "11/16", "11/17", "11/18", "11/19", "11/20", // a short day under every column
		"Input", "Result", // the legend of the two series
	} {
		if !s.tt.HasText(want) {
			t.Errorf("AI chart is missing %q; texts: %v", want, s.tt.Texts())
		}
	}
	// Tokens are the spend measure: cost is never stored, so no currency may appear.
	for _, text := range s.tt.Texts() {
		lower := strings.ToLower(text)
		if strings.ContainsAny(text, "$¥￥€£") || strings.Contains(lower, "usd") || strings.Contains(lower, "cny") ||
			strings.Contains(lower, "rmb") || strings.Contains(lower, "cost") || strings.Contains(text, "费用") {
			t.Errorf("a currency or cost figure appeared on the Stats hub: %q", text)
		}
	}
}

func TestStatsHubAISelectionByPointerAndKeyboard(t *testing.T) {
	s := newStatsHub(t, false, seedLoadedHub)
	days := hubFixtureAIDays()
	area := s.rect(t, "stats-ai-chart-area")
	labels := make([]string, len(days))
	for i, d := range days {
		labels[i] = chartShortDay(d.Day)
	}
	x := chart.NewBand(labels...).Padding(chartColumnInner, chartColumnOuter)

	if s.tt.HasText("66.3k") {
		t.Fatal("a tooltip is showing before anything is hovered")
	}
	s.tt.Move(area.X+area.W*x.At(3), area.Y+area.H*0.5)
	s.tt.Frame()
	for _, want := range []string{"11/17 · 33 req", "66.3k", "24.9k"} {
		if !s.tt.HasText(want) {
			t.Errorf("hovering 11/17 must show %q in the tooltip; texts: %v", want, s.tt.Texts())
		}
	}
	s.tt.Move(float32(s.w)+40, float32(s.h)+40) // out of the plot, out of the window
	s.tt.Frame()
	if s.tt.HasText("66.3k") {
		t.Error("the tooltip must go away when the pointer leaves the plot")
	}

	// The keyboard does the same: Tab to the plot, Home / arrows move the choice.
	focused := false
	for i := 0; i < 40 && !focused; i++ {
		s.tt.Key(0, ui.KeyTab)
		s.tt.Frame()
		focused = s.tt.Focused("stats-ai-chart-area")
	}
	if !focused {
		t.Fatal("Tab never reaches the AI chart")
	}
	s.tt.Key(0, ui.KeyHome)
	s.tt.Key(0, ui.KeyRight)
	s.tt.Frame()
	announced := s.tt.Announcements()
	want := []string{aiUsageDescribe(days[0]), aiUsageDescribe(days[1])}
	if n := len(announced); n < 2 || !slices.Equal(announced[n-2:], want) {
		t.Errorf("announcements = %q, want to end with %q", announced, want)
	}
	if !s.tt.HasText("42.5k") || !s.tt.HasText("11/15 · 21 req") {
		t.Errorf("the keyboard choice must show the same tooltip as the pointer; texts: %v", s.tt.Texts())
	}
}

func TestStatsHubLexiconLineBreaksAtQuietDaysAndMarksTheLatestPoint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s := newStatsHub(t, dark, seedLoadedHub)
		pal := newChartPalette(s.theme)
		days := hubFixtureLexiconDays()
		// Expectations come straight from the raw hit/miss counts, never from the
		// helper under test, so a regression cannot agree with itself.
		observed := func(i int) bool { return days[i].Hits+days[i].Misses > 0 }
		rate13 := float32(days[13].Hits) / float32(days[13].Hits+days[13].Misses)
		area := s.rect(t, "stats-lex-chart-area")
		labels := make([]string, len(days))
		for i, d := range days {
			labels[i] = chartShortDay(d.Day)
		}
		x := chart.NewBand(labels...).Padding(chartLineInner, chartLineOuter)

		// The latest observation carries the only accent dot, >= 8dp.
		dot := s.rect(t, "stats-lex-chart-latest")
		if dot.W < 8 || dot.H < 8 {
			t.Errorf("dark=%v the end dot is %.1fx%.1f, marks must be at least 8dp", dark, dot.W, dot.H)
		}
		wantX, wantY := area.X+area.W*x.At(13), area.Y+area.H*(1-rate13)
		if math.Abs(float64(dot.X+dot.W/2-wantX)) > 1 || math.Abs(float64(dot.Y+dot.H/2-wantY)) > 1 {
			t.Errorf("dark=%v the dot is centred at (%.1f, %.1f), the latest point is (%.1f, %.1f)", dark, dot.X+dot.W/2, dot.Y+dot.H/2, wantX, wantY)
		}
		if got := s.center(t, "stats-lex-chart-latest"); !chartPixelNear(got, pal.accent, 3) {
			t.Errorf("dark=%v the end dot is %v, want #0A84FF", dark, got)
		}
		img := s.tt.Image()
		blue := 0
		for py := int(area.Y); py < int(area.Y+area.H); py++ {
			for px := int(area.X); px < int(area.X+area.W); px++ {
				if inDot := float32(px) >= dot.X-4 && float32(px) <= dot.X+dot.W+4 && float32(py) >= dot.Y-4 && float32(py) <= dot.Y+dot.H+4; inDot {
					continue
				}
				if c := rgbaAt(img, px, py); chartPixelNear(c, pal.accent, 40) {
					blue++
				}
			}
		}
		if blue != 0 {
			t.Errorf("dark=%v %d accent pixels in the plot besides the latest dot: blue marks only the latest point", dark, blue)
		}

		// Observed days sit on the line; quiet days are gaps, not zeros.
		lineAt := func(i int) (found bool) {
			cx := int(area.X + area.W*x.At(i))
			// Scan the plot and a few dp below its baseline: a quiet day wrongly
			// plotted as zero would draw its stroke straddling the baseline.
			for py := int(area.Y) - 2; py < int(area.Y+area.H)+4; py++ {
				for px := cx - 2; px <= cx+2; px++ {
					if chartPixelNear(rgbaAt(img, px, py), pal.strong, 6) {
						return true
					}
				}
			}
			return false
		}
		for i := range days {
			if i == 13 {
				continue // the latest point is the accent dot, which sits over the line's end
			}
			if got, want := lineAt(i), observed(i); got != want {
				t.Errorf("dark=%v day %d (%+v): line present=%v, want %v (a day with no events is a gap)", dark, i, days[i], got, want)
			}
		}
		for _, want := range []string{"100%", "50%", "0%", "11/20", i18n.Tf("stats.card.lexicon.hit_rate", 85)} {
			if !s.tt.HasText(want) {
				t.Errorf("dark=%v lexicon chart is missing %q; texts: %v", dark, want, s.tt.Texts())
			}
		}
	}
}

func TestStatsHubLexiconTopWordsAreRankedBarsWithValueLabels(t *testing.T) {
	s := newStatsHub(t, false, seedLoadedHub)
	ranks := lexiconRanks(hubFixtureTopWords(), 5)
	first := s.rect(t, "stats-lex-top-row-0-bar")
	for i, rank := range ranks {
		bar := s.rect(t, "stats-lex-top-row-"+fmtInt(i)+"-bar")
		if bar.X != first.X {
			t.Errorf("bar %d starts at %.1f, the shared baseline is %.1f", i, bar.X, first.X)
		}
		if bar.H > 24 {
			t.Errorf("bar %d is %.1fdp thick; bars stay thin", i, bar.H)
		}
		if got := float64(bar.W / first.W); math.Abs(got-rank.Frac) > 0.01 {
			t.Errorf("bar %d is %.3f of the longest, its value is %.3f of it (%d)", i, got, rank.Frac, rank.Value)
		}
		for _, want := range []string{rank.Label, fmtInt(rank.Value)} {
			if !s.tt.HasText(want) {
				t.Errorf("row %d is missing %q", i, want)
			}
		}
		if i > 0 && bar.W >= s.rect(t, "stats-lex-top-row-"+fmtInt(i-1)+"-bar").W {
			t.Errorf("bar %d is not shorter than the bar above it: ranked, longest first", i)
		}
	}
	if !s.tt.HasText("Top") {
		t.Errorf("the list has no caption: %v", s.tt.Texts())
	}
}

func TestStatsHubClipboardCompositionAndLegend(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s := newStatsHub(t, dark, seedLoadedHub)
		pal := newChartPalette(s.theme)
		bar := s.rect(t, "stats-clip-bar")
		if bar.H != chartThinBarSize {
			t.Errorf("dark=%v the composition bar is %.1f tall, want %v", dark, bar.H, chartThinBarSize)
		}
		image, text := s.rect(t, "stats-clip-seg-image"), s.rect(t, "stats-clip-seg-text")
		if got, want := float64(text.W/image.W), 214.0/38.0; math.Abs(got/want-1) > 0.01 {
			t.Errorf("dark=%v text/image widths are %.3f, the counts are %.3f", dark, got, want)
		}
		if image.X >= text.X || text.X >= s.rect(t, "stats-clip-seg-file").X || s.rect(t, "stats-clip-seg-file").X >= s.rect(t, "stats-clip-seg-video").X {
			t.Errorf("dark=%v the segments are not in clipboardKindOrder order", dark)
		}
		// The sliver (3 of 267) is held at the minimum width, not dropped.
		if w := s.rect(t, "stats-clip-seg-video").W; w < chartMinBlock-0.01 {
			t.Errorf("dark=%v the video segment is %.2f wide, minimum %v", dark, w, chartMinBlock)
		}
		// Gaps: the 100% bar is the full width, minus 3 gaps.
		total := image.W + text.W + s.rect(t, "stats-clip-seg-file").W + s.rect(t, "stats-clip-seg-video").W + 3*chartGapSize
		if math.Abs(float64(total-bar.W)) > 0.5 {
			t.Errorf("dark=%v segments and gaps add up to %.1f, the bar is %.1f", dark, total, bar.W)
		}
		// Neutral steps, none of them blue when no filter is active.
		for i, kind := range []string{"image", "text", "file", "video"} {
			got := s.center(t, "stats-clip-seg-"+kind)
			want := clipboardStatColor(s.theme, i, false).Over(pal.surface)
			if !chartPixelNear(got, want, 4) || chartPixelNear(got, pal.accent, 40) {
				t.Errorf("dark=%v %s segment = %v, want neutral %v", dark, kind, got, want)
			}
		}
		// A legend row per kind in the same order, with count and size.
		var ys []float32
		for _, kind := range []string{"image", "text", "file", "video"} {
			ys = append(ys, s.rect(t, "stats-clip-key-"+kind).Y)
		}
		if !slices.IsSorted(ys) || ys[0] == ys[1] {
			t.Errorf("dark=%v legend rows are not stacked in order: %v", dark, ys)
		}
		for _, want := range []string{"267 items · 101.7 MB", "Images", "Text", "Files", "Video", "38", "214", "61.4 MB", "1.2 MB", "9.0 MB", "30.0 MB"} {
			if !s.tt.HasText(want) {
				t.Errorf("dark=%v clipboard card is missing %q; texts: %v", dark, want, s.tt.Texts())
			}
		}
	}
}

func TestStatsHubClipboardFilterMakesItsSegmentBlue(t *testing.T) {
	s := newStatsHub(t, false, func(c *AppController) { hubSeedLoaded(c, "image") })
	pal := newChartPalette(s.theme)
	if got := s.center(t, "stats-clip-seg-image"); !chartPixelNear(got, pal.accent, 3) {
		t.Errorf("the segment matching the active filter is %v, want #0A84FF", got)
	}
	for _, kind := range []string{"text", "file", "video"} {
		if got := s.center(t, "stats-clip-seg-"+kind); chartPixelNear(got, pal.accent, 40) {
			t.Errorf("%s does not match the filter but is blue: %v", kind, got)
		}
	}
}

func TestStatsHubClipboardCapturesAreColumnsWithTodayInBlue(t *testing.T) {
	s := newStatsHub(t, false, seedLoadedHub)
	pal := newChartPalette(s.theme)
	counts := []int{12, 31, 18, 44, 27, 9, 23}
	scale, _ := chartCountScale(44)
	area := s.rect(t, "stats-clip-chart-area")
	for i, n := range counts {
		bar := s.rect(t, "stats-clip-chart-bar-"+fmtInt(i)+"-0")
		if want := area.H * scale.At(float64(n)); math.Abs(float64(bar.H-want)) > 0.75 {
			t.Errorf("day %d: %.2f tall for %d captures, want %.2f", i, bar.H, n, want)
		}
		if math.Abs(float64(bar.Y+bar.H-(area.Y+area.H))) > 0.5 {
			t.Errorf("day %d does not stand on the baseline", i)
		}
		got, want := s.center(t, "stats-clip-chart-bar-"+fmtInt(i)+"-0"), pal.strong
		if i == len(counts)-1 {
			want = pal.accent
		}
		if !chartPixelNear(got, want, 3) {
			t.Errorf("day %d = %v, want %v", i, got, want)
		}
	}
	for _, want := range []string{"23", "11/14", "11/17", "11/20", "60", "20"} {
		if !s.tt.HasText(want) {
			t.Errorf("captures chart is missing %q; texts: %v", want, s.tt.Texts())
		}
	}
}

func TestStatsHubAgentLaneBarAndLegend(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s := newStatsHub(t, dark, seedLoadedHub)
		pal := newChartPalette(s.theme)
		run, appr, done := s.rect(t, "stats-agent-lane-running"), s.rect(t, "stats-agent-lane-approval"), s.rect(t, "stats-agent-lane-done")
		for _, r := range []ui.Rect{run, appr, done} {
			if r.H != chartTallBarSize || r.H > 28 {
				t.Errorf("dark=%v lane block is %.1f tall; a tall bar is %v and never more than 28", dark, r.H, chartTallBarSize)
			}
		}
		if math.Abs(float64(run.W/done.W)-0.5) > 0.01 || math.Abs(float64(appr.W/done.W)-0.25) > 0.01 {
			t.Errorf("dark=%v lane widths %.1f / %.1f / %.1f are not 2:1:4", dark, run.W, appr.W, done.W)
		}
		if !(run.X < appr.X && appr.X < done.X) {
			t.Errorf("dark=%v lanes are not in running, approval, done order", dark)
		}
		// Sample the left edge of each block, clear of the count printed at its centre.
		edge := func(r ui.Rect) ui.Color { return s.px(r.X+3, r.Y+r.H/2) }
		if got := edge(run); !chartPixelNear(got, pal.accent, 6) {
			t.Errorf("dark=%v a running lane is %v, want the accent", dark, got)
		}
		if got, want := edge(appr), kitStateWarningColor(s.theme); !chartPixelNear(got, want, 6) {
			t.Errorf("dark=%v the approval lane is %v, want system orange %v", dark, got, want)
		}
		if got := edge(done); !chartPixelNear(got, pal.strong, 6) {
			t.Errorf("dark=%v the done lane is %v, want neutral gray %v", dark, got, pal.strong)
		}
		for _, want := range []string{"7 sessions", "Running", "Approval", "Done", "29%", "14%", "57%"} {
			if !s.tt.HasText(want) {
				t.Errorf("dark=%v agent card is missing %q; texts: %v", dark, want, s.tt.Texts())
			}
		}
		// Each lane has its own icon row, in the same order as the bar.
		var ys []float32
		for _, kind := range []string{"running", "approval", "done"} {
			ys = append(ys, s.rect(t, "stats-agent-key-"+kind).Y)
		}
		if !slices.IsSorted(ys) || ys[0] == ys[1] {
			t.Errorf("dark=%v legend rows are not in lane order: %v", dark, ys)
		}
	}
}

func TestStatsHubKeepsItsLoadingErrorAndEmptyStates(t *testing.T) {
	s := newStatsHub(t, false, hubSeedStates)
	for _, name := range []string{"stats-ai-chart", "stats-clip-chart", "stats-lex-chart", "stats-agent-bar"} {
		if s.has(name) {
			t.Errorf("%s is drawn with no data behind it: a chart must never be a placeholder", name)
		}
	}
	// The same notices the cards showed before they had charts: loading (the
	// blue dot), error with its Retry action, empty text, and no sessions.
	for _, want := range []string{
		"Loading AI usage…", "Clipboard stats unavailable", "Retry", "No records", "No sessions",
		i18n.Tf("stats.card.agent.sessions", 0),
		i18n.Tf("stats.card.lexicon.terms_count", 1),
	} {
		if !s.tt.HasText(want) {
			t.Errorf("missing %q in the states view; texts: %v", want, s.tt.Texts())
		}
	}
	for _, name := range []string{"stats-card-ai", "stats-card-clipboard", "stats-card-lexicon", "stats-card-agent"} {
		if r := s.rect(t, name); r.H != statsCardHeight {
			t.Errorf("%s is %.1f tall in a non-happy state, want %v: cards stay equal", name, r.H, statsCardHeight)
		}
	}
	// The error's Retry is a real action: pressing it asks the reader again.
	if err := s.tt.Click("Retry"); err != nil {
		t.Fatalf("Retry is not clickable: %v", err)
	}
}

func TestStatsHubEmptyAIAndClipboardKeepTheirNotices(t *testing.T) {
	s := newStatsHub(t, false, func(c *AppController) {
		hubSeedLoaded(c, "")
		c.aiUsageMu.Lock()
		c.aiUsageDays = testAIUsageDays(0, 0, 0, 0, 0, 0, 0)
		c.aiUsageMu.Unlock()
		c.clipboardStatsMu.Lock()
		c.clipboardStats = clipboardStatsState{loaded: true}
		c.clipboardStatsMu.Unlock()
	})
	for _, want := range []string{"No AI calls recorded in last 7 days", "No clipboard entries recorded"} {
		if !s.tt.HasText(want) {
			t.Errorf("missing %q; texts: %v", want, s.tt.Texts())
		}
	}
	for _, name := range []string{"stats-ai-chart", "stats-clip-chart"} {
		if s.has(name) {
			t.Errorf("%s must not draw without data", name)
		}
	}
}
