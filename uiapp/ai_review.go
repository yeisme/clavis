package uiapp

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// AI rewrite review: before "Replace" overwrites the selection, the result
// can be read as changes against the source (deletions struck through,
// insertions highlighted). Close edits (polish, grammar fixes) open on the
// changes view; rewrites that share little with the source (translations)
// don't offer it.
const (
	// aiReviewMinSimilarity: below this the result is a new text, not an
	// edit, and a diff would be all red and green.
	aiReviewMinSimilarity = 0.25
	// aiReviewAutoSimilarity: at or above this the changes view is the
	// default, as the edits are what the user needs to check.
	aiReviewAutoSimilarity = 0.6
)

// aiReviewChoice is the user's pick between the result and the changes.
type aiReviewChoice int

const (
	aiReviewAuto aiReviewChoice = iota
	aiReviewResult
	aiReviewChanges
)

// aiReviewState caches one (source, result) diff: computing it walks an
// LCS table, which must not run every frame.
type aiReviewState struct {
	source, result string
	diff           engine.TextDiff
	ok             bool
	choice         aiReviewChoice
}

// aiReview returns the diff for a finished rewrite, or ok=false when there
// is nothing to review (no source, still streaming, an error, a text too
// different or too large). A new result resets the user's choice.
func (a *AppController) aiReview(state engine.State) (engine.TextDiff, bool) {
	if state.AISourceText == "" || state.AIOutput == "" || !state.AIComplete || state.AIError != "" {
		return engine.TextDiff{}, false
	}
	r := &a.uiState.aiReview
	if r.source != state.AISourceText || r.result != state.AIOutput {
		r.source, r.result = state.AISourceText, state.AIOutput
		r.diff, r.ok = engine.DiffText(state.AISourceText, state.AIOutput)
		r.choice = aiReviewAuto
	}
	if !r.ok || r.diff.Similarity < aiReviewMinSimilarity {
		return engine.TextDiff{}, false
	}
	return r.diff, true
}

// aiReviewShowsChanges reports whether the changes view is active for d.
func (a *AppController) aiReviewShowsChanges(d engine.TextDiff) bool {
	switch a.uiState.aiReview.choice {
	case aiReviewResult:
		return false
	case aiReviewChanges:
		return true
	}
	return d.Similarity >= aiReviewAutoSimilarity
}

func (a *AppController) toggleAIReview(d engine.TextDiff) {
	if a.aiReviewShowsChanges(d) {
		a.uiState.aiReview.choice = aiReviewResult
	} else {
		a.uiState.aiReview.choice = aiReviewChanges
	}
}

// renderAIReviewToggle draws [Result | Changes −n +m] with the ⌘D hint.
func (a *AppController) renderAIReviewToggle(c *ui.Context, theme *ui.Theme, d engine.TextDiff) {
	changes := a.aiReviewShowsChanges(d)
	ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
		segment := func(label string, active bool, extra func()) {
			btn := ui.ButtonBase(c).Padding(space2, space10).Radius(radiusChip).FocusRing(false)
			fg := theme.TextMuted
			if active {
				btn.Background(selectionFill(theme)).Border(1, selectionEdge(theme))
				fg = accentInk(theme, surfaceBase(theme))
			} else {
				btn.Background(tileFill(theme)).Border(1, hairline(theme))
			}
			btn.Children(func() {
				ui.Row(c).Gap(space6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).FontSize(typeLabel).FontWeight(600).TextColor(fg).SingleLine()
					if extra != nil {
						extra()
					}
				})
			})
			if btn.Clicked() && !active {
				a.toggleAIReview(d)
			}
		}
		segment(i18n.T("ai.view.result"), !changes, nil)
		segment(i18n.T("ai.view.changes"), changes, func() {
			bg := surfaceBase(theme)
			if d.Deleted > 0 {
				ui.Text(c, "−"+strconv.Itoa(d.Deleted)).FontSize(typeMeta).FontWeight(600).
					TextColor(readableOn(theme.Danger, theme.Text, bg, minTextContrast)).SingleLine()
			}
			if d.Inserted > 0 {
				ui.Text(c, "+"+strconv.Itoa(d.Inserted)).FontSize(typeMeta).FontWeight(600).
					TextColor(readableOn(theme.Success, theme.Text, bg, minTextContrast)).SingleLine()
			}
		})
		ui.Spacer(c)
		keyHint(c, theme, modPrefix()+"D", "", keyToneQuiet)
	})
}

// renderAIReviewChanges draws the result as changes against the source:
// deletions struck through on a red tint, insertions on a green tint, both
// in colors that keep 4.5:1 on the output well.
func renderAIReviewChanges(c *ui.Context, theme *ui.Theme, d engine.TextDiff) {
	well := theme.Surface
	spans := make([]ui.Span, 0, len(d.Spans))
	for _, s := range d.Spans {
		switch s.Op {
		case engine.DiffDelete:
			spans = append(spans, ui.Span{
				Text: s.Text, Color: diffDeleteInk(theme, well), Strikethrough: true,
				Background: diffDeleteMark(theme), Radius: 3,
			})
		case engine.DiffInsert:
			spans = append(spans, ui.Span{
				Text: s.Text, Color: theme.Text, Weight: 600,
				Background: diffInsertMark(theme), Radius: 3,
			})
		default:
			spans = append(spans, ui.Span{Text: s.Text, Color: theme.Text})
		}
	}
	applyProseLineHeight(ui.RichText(c, spans...).FontSize(typeBody).Wrap())
}

// Diff marks: tints behind changed runs and the struck-through ink.
func diffDeleteMark(t *ui.Theme) ui.Color { return t.Danger.Alpha(0.14) }
func diffInsertMark(t *ui.Theme) ui.Color { return t.Success.Alpha(0.22) }
func diffDeleteInk(t *ui.Theme, well ui.Color) ui.Color {
	return readableOn(t.Danger, t.Text, diffDeleteMark(t).Over(well), minTextContrast)
}
