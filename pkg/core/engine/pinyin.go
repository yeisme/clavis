package engine

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"slices"
	"strings"
	"sync"
	"unicode"
)

var (
	pinyinInitOnce sync.Once
	cjkPinyinIndex [cjkUnifiedCount]uint16
)

// Common Mandarin polyphones relevant to application names, commands, and productivity terms.
var hanziPolyphones = map[rune][]string{
	'行': {"xing", "hang"},
	'重': {"zhong", "chong"},
	'调': {"diao", "tiao"},
	'长': {"zhang", "chang"},
	'弹': {"dan", "tan"},
	'乐': {"le", "yue"},
	'藏': {"cang", "zang"},
	'差': {"cha", "chai", "ci"},
	'系': {"xi", "ji"},
	'折': {"zhe", "she"},
	'省': {"sheng", "xing"},
	'率': {"lv", "lu", "shuai"},
	'单': {"dan", "shan"},
	'解': {"jie", "xie"},
	'校': {"xiao", "jiao"},
	'奇': {"qi", "ji"},
	'传': {"chuan", "zhuan"},
	'便': {"bian", "pian"},
	'秘': {"mi", "bi"},
	'强': {"qiang", "jiang"},
	'参': {"can", "shen"},
	'模': {"mo", "mu"},
	'壳': {"ke", "qiao"},
	'号': {"hao"},
	'度': {"du", "duo"},
	'角': {"jiao", "jue"},
	'卡': {"ka", "qia"},
	'区': {"qu", "ou"},
	'盛': {"sheng", "cheng"},
	'扒': {"ba", "pa"},
}

func initPinyinTable() {
	pinyinInitOnce.Do(func() {
		comp, err := base64.StdEncoding.DecodeString(compressedPinyinIndicesB64)
		if err != nil {
			return
		}
		zr, err := zlib.NewReader(bytes.NewReader(comp))
		if err != nil {
			return
		}
		defer zr.Close()

		raw, err := io.ReadAll(zr)
		if err != nil || len(raw) < cjkUnifiedCount*2 {
			return
		}
		for i := range cjkUnifiedCount {
			lo := uint16(raw[i*2])
			hi := uint16(raw[i*2+1])
			cjkPinyinIndex[i] = lo | (hi << 8)
		}
	})
}

// IsHanzi reports whether r is inside the CJK Unified Ideographs block (U+4E00..U+9FA5).
func IsHanzi(r rune) bool {
	return r >= cjkUnifiedStart && r <= cjkUnifiedEnd
}

// ContainsHanzi reports whether s contains at least one CJK Unified Ideograph.
func ContainsHanzi(s string) bool {
	for _, r := range s {
		if IsHanzi(r) {
			return true
		}
	}
	return false
}

// HanziSyllables returns the toneless lowercase Pinyin syllables for r (including common
// polyphones and both 'v' and 'u' spellings for ü syllables like lv/lu, nv/nu).
func HanziSyllables(r rune) []string {
	if !IsHanzi(r) {
		return nil
	}
	initPinyinTable()

	var out []string
	addUnique := func(s string) {
		if s == "" || slices.Contains(out, s) {
			return
		}
		out = append(out, s)
		if strings.ContainsRune(s, 'v') {
			uForm := strings.ReplaceAll(s, "v", "u")
			if !slices.Contains(out, uForm) {
				out = append(out, uForm)
			}
		}
	}

	idx := int(cjkPinyinIndex[r-cjkUnifiedStart])
	if idx > 0 && idx < len(pinyinSyllableDict) {
		addUnique(pinyinSyllableDict[idx])
	}
	if extras, ok := hanziPolyphones[r]; ok {
		for _, p := range extras {
			addUnique(p)
		}
	}
	return out
}

// NormalizeFullWidth converts full-width ASCII characters (！..～, 　) and common Chinese IME
// punctuation/math operators (；：？！（）【】＋－×÷＝＠＞＜／、) into their half-width ASCII equivalents.
func NormalizeFullWidth(s string) string {
	if s == "" {
		return ""
	}
	needsNorm := false
	for _, r := range s {
		if r > 127 {
			needsNorm = true
			break
		}
	}
	if !needsNorm {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(NormalizeFullWidthRune(r))
	}
	return b.String()
}

// NormalizeFullWidthRune maps a single full-width ASCII or CJK punctuation/operator rune to ASCII.
func NormalizeFullWidthRune(r rune) rune {
	if r == 0x3000 {
		return ' '
	}
	if r >= 0xFF01 && r <= 0xFF5E {
		return r - 0xFEE0
	}
	switch r {
	case '；':
		return ';'
	case '：':
		return ':'
	case '？':
		return '?'
	case '！':
		return '!'
	case '（':
		return '('
	case '）':
		return ')'
	case '【', '［':
		return '['
	case '】', '］':
		return ']'
	case '＋':
		return '+'
	case '－', '—', '–':
		return '-'
	case '×', '＊':
		return '*'
	case '÷':
		return '/'
	case '＝':
		return '='
	case '＠':
		return '@'
	case '＞', '》':
		return '>'
	case '＜', '《':
		return '<'
	case '／', '、':
		return '/'
	case '。':
		return '.'
	case '，':
		return ','
	case '“', '”':
		return '"'
	case '‘', '’':
		return '\''
	default:
		return r
	}
}

// MatchPinyinLabel matches an ASCII/bilingual query against a label containing Hanzi characters,
// supporting full Pinyin ("weixin"), initialisms ("wx"), partial trailing syllables ("weix"),
// polyphones ("chonggou"), and mixed Hanzi+English labels ("打开 VSCode" via "dkvsc").
// MatchedLabelIndices contains exact rune indices in label for highlighting.
func MatchPinyinLabel(query, label string) (FuzzyMatchResult, bool) {
	if query == "" || label == "" || !ContainsHanzi(label) {
		return FuzzyMatchResult{}, false
	}

	normQ := NormalizeFullWidth(strings.TrimSpace(query))
	if normQ == "" {
		return FuzzyMatchResult{}, false
	}

	// Build compact lowercase query runes, stripping Pinyin syllable apostrophes.
	qRunes := make([]rune, 0, len(normQ))
	hasASCIILetter := false
	for _, r := range normQ {
		if r == '\'' || unicode.IsSpace(r) {
			continue
		}
		lr := foldRune(r)
		if lr >= 'a' && lr <= 'z' {
			hasASCIILetter = true
		}
		qRunes = append(qRunes, lr)
	}
	if !hasASCIILetter || len(qRunes) == 0 {
		return FuzzyMatchResult{}, false
	}

	lRunes := []rune(label)
	m := len(qRunes)
	n := len(lRunes)
	if m > n*6 {
		return FuzzyMatchResult{}, false
	}

	const negInf = -1 << 28

	type dpCell struct {
		score   int
		prevJ   int
		prevQi  int
		matched bool
		wasAdj  bool
	}

	dp := make([][]dpCell, n+1)
	for j := 0; j <= n; j++ {
		dp[j] = make([]dpCell, m+1)
		for qi := 0; qi <= m; qi++ {
			dp[j][qi] = dpCell{score: negInf, prevJ: -1, prevQi: -1}
		}
	}
	dp[0][0] = dpCell{score: 0}

	update := func(nj, nqi, candScore, pj, pqi int, matched bool) {
		if candScore > dp[nj][nqi].score {
			dp[nj][nqi] = dpCell{
				score:   candScore,
				prevJ:   pj,
				prevQi:  pqi,
				matched: matched,
				wasAdj:  matched,
			}
		}
	}

	qStr := string(qRunes)
	// Precompute byte offsets in qStr per rune index for fast HasPrefix checks.
	qBytePos := make([]int, m+1)
	byteOff := 0
	for i, r := range qRunes {
		qBytePos[i] = byteOff
		byteOff += len(string(r))
	}
	qBytePos[m] = byteOff

	for j := 0; j < n; j++ {
		lr := lRunes[j]
		syls := HanziSyllables(lr)
		foldedL := foldRune(lr)

		for qi := 0; qi <= m; qi++ {
			cur := dp[j][qi]
			if cur.score <= negInf/2 {
				continue
			}

			// 1. Skip lRunes[j]
			skipPenalty := 0
			if qi > 0 && qi < m {
				if isWordSeparator(lr) {
					skipPenalty = 1
				} else {
					skipPenalty = 4
				}
			}
			update(j+1, qi, cur.score-skipPenalty, j, qi, false)

			if qi == m {
				continue
			}

			adjBonus := 0
			if cur.wasAdj {
				adjBonus = 36
			} else if j == 0 || (j > 0 && isWordSeparator(lRunes[j-1])) {
				adjBonus = 24
			}

			// 2. Direct rune match (for digits, ASCII letters, or Hanzi literals in mixed queries)
			if foldedL == qRunes[qi] {
				update(j+1, qi+1, cur.score+42+adjBonus, j, qi, true)
			}

			// 3. Hanzi Pinyin syllable / initial / trailing-prefix match
			if len(syls) > 0 {
				remSlice := qStr[qBytePos[qi]:]
				remRunes := m - qi
				for _, syl := range syls {
					if syl == "" {
						continue
					}
					// Initial letter match (e.g. 'w' for "wei")
					if rune(syl[0]) == qRunes[qi] {
						update(j+1, qi+1, cur.score+38+adjBonus, j, qi, true)
					}
					// Full syllable match (e.g. "wei" for '微')
					sLen := len(syl) // syllables are pure ASCII a-z
					if sLen > 1 && remRunes >= sLen && strings.HasPrefix(remSlice, syl) {
						fullBonus := 62 + sLen*12 + adjBonus
						update(j+1, qi+sLen, cur.score+fullBonus, j, qi, true)
					} else if sLen > 2 && remRunes >= 2 && remRunes < sLen && strings.HasPrefix(syl, remSlice) {
						// Trailing partial syllable while user is mid-typing (e.g. "weix" -> "wei" + "x"[in])
						partBonus := 44 + remRunes*8 + adjBonus
						update(j+1, m, cur.score+partBonus, j, qi, true)
					}
				}
			}
		}
	}

	end := dp[n][m]
	if end.score <= negInf/2 {
		return FuzzyMatchResult{}, false
	}

	var revIndices []int
	cj, cqi := n, m
	for cj > 0 {
		cell := dp[cj][cqi]
		if cell.matched {
			revIndices = append(revIndices, cj-1)
		}
		cj, cqi = cell.prevJ, cell.prevQi
		if cj < 0 || cqi < 0 {
			break
		}
	}
	if len(revIndices) == 0 {
		return FuzzyMatchResult{}, false
	}

	indices := make([]int, len(revIndices))
	for i := range revIndices {
		indices[i] = revIndices[len(revIndices)-1-i]
	}

	finalScore := end.score
	if indices[0] == 0 {
		finalScore += 45
	}
	if len(indices) == n {
		finalScore += 180
	}
	finalScore -= n / 4

	return FuzzyMatchResult{
		Score:               finalScore,
		MatchedLabelIndices: indices,
	}, true
}

// MatchPinyinKeyword scores an ASCII/Pinyin query against a keyword string containing Hanzi.
// Returns 0 when not matched.
func MatchPinyinKeyword(query, keyword string) int {
	res, ok := MatchPinyinLabel(query, keyword)
	if !ok {
		return 0
	}
	kwRunes := []rune(keyword)
	if len(res.MatchedLabelIndices) == len(kwRunes) {
		return 155
	}
	if len(res.MatchedLabelIndices) > 0 && res.MatchedLabelIndices[0] == 0 {
		return 95
	}
	return 48
}
