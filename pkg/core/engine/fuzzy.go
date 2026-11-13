package engine

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// FuzzyMatchResult holds the match score and matched rune indices in the label.
type FuzzyMatchResult struct {
	Score               int   `json:"score"`
	MatchedLabelIndices []int `json:"matchedLabelIndices"`
}

// Match runs fuzzy matching of query against target label.
func Match(query, target string) (FuzzyMatchResult, bool) {
	return MatchFull(query, target, "", nil)
}

// MatchFull runs fuzzy matching of query against label, key hint, and keywords,
// including zero-dependency Hanzi Pinyin/initialism matching and full-width normalization.
func MatchFull(query, label, key string, keywords []string) (FuzzyMatchResult, bool) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return FuzzyMatchResult{Score: 0, MatchedLabelIndices: nil}, true
	}

	var best FuzzyMatchResult
	hasBest := false

	evaluateCandidate := func(q string) {
		if direct, ok := scoreSingleQuery(q, label, key, keywords); ok {
			if !hasBest || direct.Score > best.Score {
				best = direct
				hasBest = true
			}
		}
		tokens := strings.Fields(q)
		if len(tokens) > 1 {
			if multi, ok := scoreMultiToken(tokens, label, key, keywords); ok {
				if !hasBest || multi.Score > best.Score {
					best = multi
					hasBest = true
				}
			}
		}
	}

	evaluateCandidate(trimmed)
	if norm := strings.TrimSpace(NormalizeFullWidth(trimmed)); norm != "" && norm != trimmed {
		evaluateCandidate(norm)
	}

	return best, hasBest
}

func scoreMultiToken(tokens []string, label, key string, keywords []string) (FuzzyMatchResult, bool) {
	totalScore := 0
	idxSet := make(map[int]struct{})
	for _, tok := range tokens {
		res, ok := scoreSingleQuery(tok, label, key, keywords)
		if !ok {
			return FuzzyMatchResult{}, false
		}
		totalScore += res.Score
		for _, idx := range res.MatchedLabelIndices {
			idxSet[idx] = struct{}{}
		}
	}
	indices := make([]int, 0, len(idxSet))
	for idx := range idxSet {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	return FuzzyMatchResult{
		Score:               totalScore + 20,
		MatchedLabelIndices: indices,
	}, true
}

func scoreSingleQuery(query, label, key string, keywords []string) (FuzzyMatchResult, bool) {
	labelMatch, labelOK := scoreLabel(query, label)
	if pyMatch, pyOK := MatchPinyinLabel(query, label); pyOK {
		if !labelOK || pyMatch.Score > labelMatch.Score {
			labelMatch = pyMatch
			labelOK = true
		}
	}
	keyBonus := scoreKey(query, key)
	keywordBonus := scoreKeywords(query, keywords)

	if !labelOK && keyBonus == 0 && keywordBonus == 0 {
		return FuzzyMatchResult{}, false
	}

	baseScore := 0
	var matched []int
	if labelOK {
		baseScore = labelMatch.Score
		matched = labelMatch.MatchedLabelIndices
	}
	return FuzzyMatchResult{
		Score:               baseScore + keyBonus + keywordBonus,
		MatchedLabelIndices: matched,
	}, true
}

func scoreKey(query, key string) int {
	if key == "" {
		return 0
	}
	lowerQ := strings.ToLower(query)
	lowerK := strings.ToLower(key)
	if lowerQ == lowerK {
		if query == key {
			return 320
		}
		return 300
	}
	if strings.HasPrefix(lowerK, lowerQ) {
		return 150
	}
	return 0
}

func scoreKeywords(query string, keywords []string) int {
	if len(keywords) == 0 {
		return 0
	}
	lowerQ := strings.ToLower(query)
	qRunes := []rune(lowerQ)
	best := 0

	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		lowerKW := strings.ToLower(kw)
		if lowerKW == lowerQ {
			if 180 > best {
				best = 180
			}
		} else if strings.HasPrefix(lowerKW, lowerQ) {
			if 110 > best {
				best = 110
			}
		} else if strings.Contains(lowerKW, lowerQ) {
			pts := 12
			if len(qRunes) >= 2 {
				pts = 50
			}
			if pts > best {
				best = pts
			}
		} else if isSubsequenceRunes(qRunes, []rune(lowerKW)) {
			pts := 8
			if len(qRunes) >= 2 {
				pts = 25
			}
			if pts > best {
				best = pts
			}
		}
		if ContainsHanzi(kw) {
			if pyPts := MatchPinyinKeyword(query, kw); pyPts > best {
				best = pyPts
			}
		}
	}
	return best
}

func scoreLabel(query, label string) (FuzzyMatchResult, bool) {
	qRunes := []rune(query)
	lRunes := []rune(label)
	m := len(qRunes)
	n := len(lRunes)
	if m == 0 || m > n {
		return FuzzyMatchResult{}, false
	}

	qLower := make([]rune, m)
	for i, r := range qRunes {
		qLower[i] = foldRune(r)
	}
	lLower := make([]rune, n)
	for j, r := range lRunes {
		lLower[j] = foldRune(r)
	}

	// Quick subsequence precheck
	qi := 0
	for li := 0; li < n && qi < m; li++ {
		if lLower[li] == qLower[qi] {
			qi++
		}
	}
	if qi != m {
		return FuzzyMatchResult{}, false
	}

	isWordStart := make([]bool, n)
	for i := 0; i < n; i++ {
		if i == 0 {
			isWordStart[i] = true
		} else {
			prev := lRunes[i-1]
			curr := lRunes[i]
			if isWordSeparator(prev) || (unicode.IsLower(prev) && unicode.IsUpper(curr)) {
				isWordStart[i] = true
			}
		}
	}

	const negInf = math.MinInt / 4
	dp := make([][]int, m)
	parent := make([][]int, m)
	for i := 0; i < m; i++ {
		dp[i] = make([]int, n)
		parent[i] = make([]int, n)
		for j := 0; j < n; j++ {
			dp[i][j] = negInf
			parent[i][j] = -1
		}
	}

	for j := 0; j < n; j++ {
		if lLower[j] == qLower[0] {
			s := 10
			if j == 0 {
				s += 15
			}
			if isWordStart[j] {
				s += 35
			}
			if qRunes[0] == lRunes[j] {
				s += 2
			}
			s -= j
			dp[0][j] = s
		}
	}

	if m > 1 {
		for i := 1; i < m; i++ {
			bestNonAdjVal := negInf
			bestNonAdjIdx := -1

			for j := i; j < n; j++ {
				if j >= 2 {
					k := j - 2
					if dp[i-1][k] > negInf {
						cand := dp[i-1][k] + 4*k
						if cand > bestNonAdjVal {
							bestNonAdjVal = cand
							bestNonAdjIdx = k
						}
					}
				}

				if lLower[j] != qLower[i] {
					continue
				}

				bestPrevScore := negInf
				bestPrevIdx := -1

				if dp[i-1][j-1] > negInf {
					bestPrevScore = dp[i-1][j-1] + 45
					bestPrevIdx = j - 1
				}

				if bestNonAdjIdx != -1 {
					gapPenalty := 4 * (j - 1)
					nonAdjScore := bestNonAdjVal - gapPenalty
					if nonAdjScore > bestPrevScore {
						bestPrevScore = nonAdjScore
						bestPrevIdx = bestNonAdjIdx
					}
				}

				if bestPrevIdx == -1 {
					continue
				}

				score := bestPrevScore + 10
				if isWordStart[j] {
					score += 35
				}
				if qRunes[i] == lRunes[j] {
					score += 2
				}
				dp[i][j] = score
				parent[i][j] = bestPrevIdx
			}
		}
	}

	bestEndScore := negInf
	bestEndIdx := -1
	for j := m - 1; j < n; j++ {
		if dp[m-1][j] > bestEndScore {
			bestEndScore = dp[m-1][j]
			bestEndIdx = j
		}
	}
	if bestEndIdx == -1 {
		return FuzzyMatchResult{}, false
	}

	indices := make([]int, m)
	currIdx := bestEndIdx
	for i := m - 1; i >= 0; i-- {
		indices[i] = currIdx
		currIdx = parent[i][currIdx]
	}

	lowerQStr := string(qLower)
	lowerLStr := string(lLower)
	finalScore := bestEndScore

	if lowerQStr == lowerLStr {
		finalScore += 500
		if query == label {
			finalScore += 20
		}
	} else if strings.HasPrefix(lowerLStr, lowerQStr) {
		finalScore += 250
	} else if strings.Contains(lowerLStr, lowerQStr) {
		finalScore += 120
	}

	finalScore -= n / 4

	return FuzzyMatchResult{
		Score:               finalScore,
		MatchedLabelIndices: indices,
	}, true
}

func foldRune(r rune) rune {
	// Normalize Turkish capital I with dot (\u0130) and lowercase via unicode.ToLower
	return unicode.ToLower(r)
}

func isWordSeparator(r rune) bool {
	return r == ' ' || r == '-' || r == '_' || r == '/' || r == '.' || r == ':'
}

func isSubsequenceRunes(needle, haystack []rune) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	ni := 0
	for _, hc := range haystack {
		if ni < len(needle) && hc == needle[ni] {
			ni++
		}
	}
	return ni == len(needle)
}
