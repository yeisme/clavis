package engine

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// DiffOp is one kind of change in a TextDiff.
type DiffOp int

const (
	DiffEqual DiffOp = iota
	DiffDelete
	DiffInsert
)

// DiffSpan is a run of text with one DiffOp.
type DiffSpan struct {
	Op   DiffOp
	Text string
}

// TextDiff is the change set from a source text to a rewritten one.
type TextDiff struct {
	Spans []DiffSpan
	// Similarity is 2·equal / (source + result) over tokens: 1 for
	// identical texts, near 0 for a translation.
	Similarity float64
	// Deleted and Inserted count changed tokens (CJK runes or words).
	Deleted, Inserted int
}

// maxDiffCells caps the LCS table (tokens × tokens after trimming the common
// prefix and suffix): about 1400 × 1400 changed tokens, a few MB, a few ms.
const maxDiffCells = 2_000_000

// DiffText compares a source text with its AI rewrite for review before the
// result replaces the selection. Chinese, Japanese and Korean compare per
// character (a word boundary is invisible there), other scripts per word,
// with whitespace and punctuation as their own tokens. ok is false when the
// texts are too large to diff interactively.
func DiffText(source, result string) (d TextDiff, ok bool) {
	a, b := diffTokens(source), diffTokens(result)

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	midA, midB := a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]
	if len(midA)*len(midB) > maxDiffCells {
		return TextDiff{}, false
	}

	var spans []DiffSpan
	add := func(op DiffOp, tok string) {
		if n := len(spans); n > 0 && spans[n-1].Op == op {
			spans[n-1].Text += tok
			return
		}
		spans = append(spans, DiffSpan{Op: op, Text: tok})
	}
	equal := prefix + suffix
	for _, t := range a[:prefix] {
		add(DiffEqual, t)
	}

	// LCS over the differing middle; walking it forward emits each changed
	// region as its deletions followed by its insertions.
	n, m := len(midA), len(midB)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if midA[i] == midB[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var pendingDel, pendingIns []string
	flush := func() {
		for _, t := range pendingDel {
			add(DiffDelete, t)
		}
		for _, t := range pendingIns {
			add(DiffInsert, t)
		}
		d.Deleted += countWordTokens(pendingDel)
		d.Inserted += countWordTokens(pendingIns)
		pendingDel, pendingIns = pendingDel[:0], pendingIns[:0]
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case midA[i] == midB[j]:
			flush()
			add(DiffEqual, midA[i])
			equal++
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			pendingDel = append(pendingDel, midA[i])
			i++
		default:
			pendingIns = append(pendingIns, midB[j])
			j++
		}
	}
	pendingDel = append(pendingDel, midA[i:]...)
	pendingIns = append(pendingIns, midB[j:]...)
	flush()

	for _, t := range a[len(a)-suffix:] {
		add(DiffEqual, t)
	}
	d.Spans = mergeDiffIslands(spans)
	if total := len(a) + len(b); total > 0 {
		d.Similarity = 2 * float64(equal) / float64(total)
	} else {
		d.Similarity = 1
	}
	return d, true
}

// diffTokens splits text into CJK runes, Latin words (letters, digits,
// apostrophes inside words), whitespace runs and single punctuation runes.
func diffTokens(s string) []string {
	var toks []string
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		start := i
		switch {
		case isCJKRune(r):
			i += size
		case unicode.IsSpace(r):
			for i < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[i:])
				if !unicode.IsSpace(r2) {
					break
				}
				i += sz
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			for i < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[i:])
				if isCJKRune(r2) || !(unicode.IsLetter(r2) || unicode.IsDigit(r2) || r2 == '\'' || r2 == '_') {
					break
				}
				i += sz
			}
		default:
			i += size
		}
		toks = append(toks, s[start:i])
	}
	return toks
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		(r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

// countWordTokens counts the tokens that carry text (not whitespace).
func countWordTokens(toks []string) int {
	n := 0
	for _, t := range toks {
		if strings.TrimSpace(t) != "" {
			n++
		}
	}
	return n
}

// maxIslandRunes is the longest non-blank shared run folded into the edits
// around it. One rune between two edits (的, 了, a comma) is usually
// coincidence; two CJK runes are usually a word the rewrite kept (校验,
// 字段), which the review should show as kept.
const maxIslandRunes = 1

// mergeDiffIslands folds short unchanged runs sandwiched between two edits
// into both edits, like diff-match-patch's semantic cleanup: a lone shared
// character, or the space between two replaced words, splits one rewritten
// phrase into fragments. An island folds when it is whitespace or at most
// maxIslandRunes runes, and no longer than the edits on either side. Both
// texts still rebuild exactly.
func mergeDiffIslands(spans []DiffSpan) []DiffSpan {
	type group struct {
		equal        bool
		eq, del, ins string
	}
	var gs []group
	for _, s := range spans {
		if s.Op == DiffEqual {
			gs = append(gs, group{equal: true, eq: s.Text})
			continue
		}
		if n := len(gs); n == 0 || gs[n-1].equal {
			gs = append(gs, group{})
		}
		if s.Op == DiffDelete {
			gs[len(gs)-1].del += s.Text
		} else {
			gs[len(gs)-1].ins += s.Text
		}
	}
	size := func(g group) int {
		return max(utf8.RuneCountInString(g.del), utf8.RuneCountInString(g.ins))
	}
	for merged := true; merged; {
		merged = false
		for i := 1; i+1 < len(gs); i++ {
			island, before, after := gs[i], gs[i-1], gs[i+1]
			if !island.equal || before.equal || after.equal {
				continue
			}
			n := utf8.RuneCountInString(island.eq)
			if strings.TrimSpace(island.eq) != "" && n > maxIslandRunes {
				continue
			}
			if n > size(before) || n > size(after) {
				continue
			}
			gs[i-1] = group{del: before.del + island.eq + after.del, ins: before.ins + island.eq + after.ins}
			gs = append(gs[:i], gs[i+2:]...)
			merged = true
			break
		}
	}
	out := make([]DiffSpan, 0, len(gs)*2)
	for _, g := range gs {
		switch {
		case g.equal:
			out = append(out, DiffSpan{Op: DiffEqual, Text: g.eq})
		default:
			if g.del != "" {
				out = append(out, DiffSpan{Op: DiffDelete, Text: g.del})
			}
			if g.ins != "" {
				out = append(out, DiffSpan{Op: DiffInsert, Text: g.ins})
			}
		}
	}
	return out
}
