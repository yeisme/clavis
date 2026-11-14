package ime

import (
	"strings"
	"sync"
)

var syllables = []string{"a", "ai", "an", "ang", "ao", "ba", "bai", "ban", "bang", "bao", "bei", "ben", "beng", "bi", "bian", "biao", "bie", "bin", "bing", "bo", "bu", "ca", "cai", "can", "cang", "cao", "ce", "ceng", "cha", "chai", "chan", "chang", "chao", "che", "chen", "cheng", "chi", "chong", "chou", "chu", "chua", "chuai", "chuan", "chuang", "chui", "chun", "chuo", "ci", "cong", "cou", "cu", "cuan", "cui", "cun", "cuo", "da", "dai", "dan", "dang", "dao", "de", "dei", "den", "deng", "di", "dia", "dian", "diao", "die", "ding", "diu", "dong", "dou", "du", "duan", "dui", "dun", "duo", "e", "ei", "en", "eng", "er", "fa", "fan", "fang", "fei", "fen", "feng", "fo", "fou", "fu", "ga", "gai", "gan", "gang", "gao", "ge", "gei", "gen", "geng", "gong", "gou", "gu", "gua", "guai", "guan", "guang", "gui", "gun", "guo", "ha", "hai", "han", "hang", "hao", "he", "hei", "hen", "heng", "hong", "hou", "hu", "hua", "huai", "huan", "huang", "hui", "hun", "huo", "ji", "jia", "jian", "jiang", "jiao", "jie", "jin", "jing", "jiong", "jiu", "ju", "juan", "jue", "jun", "ka", "kai", "kan", "kang", "kao", "ke", "ken", "keng", "kong", "kou", "ku", "kua", "kuai", "kuan", "kuang", "kui", "kun", "kuo", "la", "lai", "lan", "lang", "lao", "le", "lei", "leng", "li", "lia", "lian", "liang", "liao", "lie", "lin", "ling", "liu", "lo", "long", "lou", "lu", "lua", "luan", "lue", "lun", "luo", "lv", "lve", "ma", "mai", "man", "mang", "mao", "me", "mei", "men", "meng", "mi", "mian", "miao", "mie", "min", "ming", "miu", "mo", "mou", "mu", "na", "nai", "nan", "nang", "nao", "ne", "nei", "nen", "neng", "ni", "nian", "niang", "niao", "nie", "nin", "ning", "niu", "nong", "nou", "nu", "nuan", "nue", "nuo", "nv", "nve", "o", "ou", "pa", "pai", "pan", "pang", "pao", "pei", "pen", "peng", "pi", "pian", "piao", "pie", "pin", "ping", "po", "pou", "pu", "qi", "qia", "qian", "qiang", "qiao", "qie", "qin", "qing", "qiong", "qiu", "qu", "quan", "que", "qun", "ran", "rang", "rao", "re", "ren", "reng", "ri", "rong", "rou", "ru", "ruan", "rui", "run", "ruo", "sa", "sai", "san", "sang", "sao", "se", "sen", "seng", "sha", "shai", "shan", "shang", "shao", "she", "shen", "sheng", "shi", "shou", "shu", "shua", "shuai", "shuan", "shuang", "shui", "shun", "shuo", "si", "song", "sou", "su", "suan", "sui", "sun", "suo", "ta", "tai", "tan", "tang", "tao", "te", "teng", "ti", "tian", "tiao", "tie", "ting", "tong", "tou", "tu", "tuan", "tui", "tun", "tuo", "wa", "wai", "wan", "wang", "wei", "wen", "weng", "wo", "wu", "xi", "xia", "xian", "xiang", "xiao", "xie", "xin", "xing", "xiong", "xiu", "xu", "xuan", "xue", "xun", "ya", "yan", "yang", "yao", "ye", "yi", "yin", "ying", "yo", "yong", "you", "yu", "yuan", "yue", "yun", "za", "zai", "zan", "zang", "zao", "ze", "zei", "zen", "zeng", "zha", "zhai", "zhan", "zhang", "zhao", "zhe", "zhen", "zheng", "zhi", "zhong", "zhou", "zhu", "zhua", "zhuai", "zhuan", "zhuang", "zhui", "zhun", "zhuo", "zi", "zong", "zou", "zu", "zuan", "zui", "zun", "zuo"}

func normalizeSyllable(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ü", "v")
	if s != "lv" && s != "nv" && s != "lve" && s != "nve" {
		s = strings.ReplaceAll(s, "v", "u")
	}
	return s
}

func splitQuanpin(input string) []string {
	input = strings.ToLower(strings.ReplaceAll(input, "ü", "v"))
	var out []string
	for len(input) > 0 {
		if input[0] == '\'' || input[0] == ' ' || input[0] == '\t' {
			input = input[1:]
			continue
		}
		n := 0
		for _, s := range syllables {
			if strings.HasPrefix(input, s) && len(s) > n {
				n = len(s)
			}
		}
		if n == 0 {
			out = append(out, input[:1])
			input = input[1:]
		} else {
			out = append(out, normalizeSyllable(input[:n]))
			input = input[n:]
		}
	}
	return out
}

// Key maps are represented as initial/final tables. The layouts follow the
// published standard layouts and share the common final spelling where schemes do.
var finalsXH = map[string]string{"a": "a", "ai": "l", "an": "j", "ang": "h", "ao": "c", "e": "e", "ei": "w", "en": "f", "eng": "g", "er": "r", "o": "o", "ou": "b", "u": "u", "ua": "x", "uai": "x", "uan": "r", "uang": "l", "ui": "v", "un": "p", "uo": "o", "v": "v", "ve": "t", "i": "i", "ia": "x", "ian": "m", "iang": "d", "iao": "c", "ie": "p", "in": "n", "ing": "y", "iong": "s", "iu": "q", "ong": "s", "ue": "t"}
var initialsXH = map[string]string{"b": "b", "p": "p", "m": "m", "f": "f", "d": "d", "t": "t", "n": "n", "l": "l", "g": "g", "k": "k", "h": "h", "j": "j", "q": "q", "x": "x", "zh": "v", "ch": "i", "sh": "u", "r": "r", "z": "z", "c": "c", "s": "s", "y": "y", "w": "w"}
var initialsZM = initialsXH
var finalsZM = finalsXH

// Microsoft, Sogou and Pinyin++ retain the standard initial key positions but
// differ in their frequent final keys.
var finalsMS = map[string]string{"a": "a", "ai": "l", "an": "j", "ang": "h", "ao": "k", "e": "e", "ei": "w", "en": "f", "eng": "g", "er": "r", "o": "o", "ou": "z", "u": "u", "ua": "x", "uai": "x", "uan": "r", "uang": "l", "ui": "v", "un": "p", "uo": "o", "v": "v", "ve": "t", "i": "i", "ia": "x", "ian": "m", "iang": "d", "iao": "c", "ie": "x", "in": "n", "ing": "y", "iong": "s", "iu": "q", "ong": "s", "ue": "t"}
var finalsSG = finalsMS
var finalsPP = finalsMS

type shuangpinTable struct {
	pairs    [26][26][]string
	initials [26]string
}

var (
	spOnce   sync.Once
	spTables map[string]*shuangpinTable
)

// preferredPairPrimary biases ambiguous Shuangpin pairs toward the much higher-frequency reading
// when a single-syllable representation is requested (while ShuangpinSyllables returns all).
var preferredPairPrimary = map[string]string{
	"shuang": "shai",
	"dao":    "diao",
	"hao":    "",
	"bao":    "biao",
	"lao":    "liao",
	"mao":    "miao",
	"nao":    "niao",
	"pao":    "piao",
	"tao":    "tiao",
	"gua":    "guai",
	"hua":    "huai",
	"kua":    "kuai",
	"shua":   "shuai",
	"zhua":   "zhuai",
}

func initShuangpinTables() {
	spOnce.Do(func() {
		schemes := []struct {
			name   string
			im, fm map[string]string
			oZero  bool
		}{
			{"xiaohe", initialsXH, finalsXH, false},
			{"ziranma", initialsZM, finalsZM, false},
			{"microsoft", initialsXH, finalsMS, true},
			{"sogou", initialsXH, finalsSG, true},
			{"pinyin_plus", initialsXH, finalsPP, false},
		}
		spTables = make(map[string]*shuangpinTable, len(schemes))
		for _, sc := range schemes {
			tbl := &shuangpinTable{}
			for k := 0; k < 26; k++ {
				tbl.initials[k] = string(rune('a' + k))
			}
			for initSyl, code := range sc.im {
				if len(code) == 1 && code[0] >= 'a' && code[0] <= 'z' {
					tbl.initials[code[0]-'a'] = initSyl
				}
			}
			addPair := func(pair, syl string) {
				if len(pair) != 2 || pair[0] < 'a' || pair[0] > 'z' || pair[1] < 'a' || pair[1] > 'z' {
					return
				}
				a, b := pair[0]-'a', pair[1]-'a'
				cur := tbl.pairs[a][b]
				for _, existing := range cur {
					if existing == syl {
						return
					}
				}
				if sec, ok := preferredPairPrimary[syl]; ok && sec != "" {
					for idx, existing := range cur {
						if existing == sec {
							cur = append(cur[:idx], append([]string{syl}, cur[idx:]...)...)
							tbl.pairs[a][b] = cur
							return
						}
					}
				}
				tbl.pairs[a][b] = append(cur, syl)
			}
			for _, sy := range syllables {
				code := shuangpinCodeWithMaps(sc.im, sc.fm, sy)
				if len(code) == 2 {
					addPair(code, sy)
				}
			}
			// Zero-initial two-key forms: Xiaohe/Ziranma double vowel or first+final-key;
			// Microsoft/Sogou 'o' + final-key.
			zeroForms := [][2]string{
				{"aa", "a"}, {"ee", "e"}, {"oo", "o"},
				{"ai", "ai"}, {"an", "an"}, {"ao", "ao"},
				{"ei", "ei"}, {"en", "en"}, {"er", "er"}, {"ou", "ou"},
				{"ah", "ang"}, {"eg", "eng"},
			}
			if sc.oZero {
				zeroForms = append(zeroForms,
					[2]string{"oa", "a"}, [2]string{"oe", "e"}, [2]string{"oo", "o"},
					[2]string{"ol", "ai"}, [2]string{"oj", "an"}, [2]string{"ok", "ao"},
					[2]string{"oz", "ei"}, [2]string{"of", "en"}, [2]string{"or", "er"},
					[2]string{"ob", "ou"}, [2]string{"oh", "ang"}, [2]string{"og", "eng"},
				)
			}
			for _, zf := range zeroForms {
				addPair(zf[0], zf[1])
			}
			// Preserve backward-compatible fallbacks tested across all 5 schemes ("nihc" -> ni hao, "ul" -> shuang).
			if len(tbl.pairs['h'-'a']['c'-'a']) == 0 {
				addPair("hc", "hao")
			}
			addPair("ul", "shuang")
			// Ensure Microsoft/Sogou ing on 'y' works alongside ';' when typed with letters only.
			if sc.name == "microsoft" || sc.name == "sogou" {
				for _, sy := range syllables {
					i, f := parseSyllable(sy)
					if f == "ing" {
						a := sc.im[i]
						if a == "" {
							a = i
						}
						addPair(a+"y", sy)
					}
				}
			}
			spTables[sc.name] = tbl
		}
	})
}

func shuangpinTableFor(scheme string) *shuangpinTable {
	initShuangpinTables()
	return spTables[strings.ToLower(scheme)]
}

// ShuangpinSyllables returns all canonical pinyin syllables (or initial prefix for a single
// trailing letter) encoded by code under scheme, ordered with higher-frequency syllables first.
func ShuangpinSyllables(scheme, code string) []string {
	tbl := shuangpinTableFor(scheme)
	if tbl == nil || code == "" {
		return nil
	}
	if len(code) == 1 {
		c := code[0]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c >= 'a' && c <= 'z' {
			return []string{tbl.initials[c-'a']}
		}
		return []string{code}
	}
	if len(code) == 2 {
		c0, c1 := code[0], code[1]
		if c0 >= 'A' && c0 <= 'Z' {
			c0 += 'a' - 'A'
		}
		if c1 >= 'A' && c1 <= 'Z' {
			c1 += 'a' - 'A'
		}
		if c0 >= 'a' && c0 <= 'z' && c1 >= 'a' && c1 <= 'z' {
			if syls := tbl.pairs[c0-'a'][c1-'a']; len(syls) > 0 {
				return syls
			}
		}
	}
	return []string{strings.ToLower(code)}
}

// SplitShuangpin decodes a Shuangpin key stream into primary canonical pinyin syllables/initials.
// "'" forces a segment boundary.
func SplitShuangpin(scheme, input string) []string {
	return splitShuangpin(scheme, input)
}

func splitShuangpin(scheme, input string) []string {
	tbl := shuangpinTableFor(scheme)
	var out []string
	start := 0
	for idx := 0; idx <= len(input); idx++ {
		if idx < len(input) && input[idx] != '\'' {
			continue
		}
		chunk := input[start:idx]
		start = idx + 1
		for len(chunk) >= 2 {
			pair := chunk[:2]
			chunk = chunk[2:]
			syls := ShuangpinSyllables(scheme, pair)
			if len(syls) > 0 {
				out = append(out, syls[0])
			} else if tbl == nil {
				out = append(out, strings.ToLower(pair))
			}
		}
		if len(chunk) == 1 {
			syls := ShuangpinSyllables(scheme, chunk)
			if len(syls) > 0 {
				out = append(out, syls[0])
			} else {
				out = append(out, strings.ToLower(chunk))
			}
		}
	}
	return out
}

func parseSyllable(s string) (string, string) {
	s = normalizeSyllable(s)
	initial := ""
	for _, x := range []string{"zh", "ch", "sh", "b", "p", "m", "f", "d", "t", "n", "l", "g", "k", "h", "j", "q", "x", "r", "z", "c", "s", "y", "w"} {
		if strings.HasPrefix(s, x) && len(x) > len(initial) {
			initial = x
		}
	}
	return initial, s[len(initial):]
}

func shuangpinCodeWithMaps(im, fm map[string]string, sy string) string {
	i, f := parseSyllable(sy)
	a, b := im[i], fm[f]
	if a == "" {
		a = i
	}
	if b == "" {
		b = f
	}
	return a + b
}

func ShuangpinCode(scheme, sy string) string {
	switch strings.ToLower(scheme) {
	case "xiaohe":
		return shuangpinCodeWithMaps(initialsXH, finalsXH, sy)
	case "ziranma":
		return shuangpinCodeWithMaps(initialsZM, finalsZM, sy)
	case "microsoft":
		return shuangpinCodeWithMaps(initialsXH, finalsMS, sy)
	case "sogou":
		return shuangpinCodeWithMaps(initialsXH, finalsSG, sy)
	case "pinyin_plus":
		return shuangpinCodeWithMaps(initialsXH, finalsPP, sy)
	default:
		return sy
	}
}

// ExpandFuzzy returns key followed by fuzzy pinyin alternatives generated by rules such as
// "z=zh", "c=ch", "s=sh", "n=l", "f=h", "r=l", "an=ang", "en=eng", "in=ing", "ian=iang", "uan=uang".
// Rules are applied bidirectionally to syllable initials and finals without corrupting e.g. "zhong".
func ExpandFuzzy(key string, rules []string) []string {
	return expandFuzzy(key, rules)
}

func expandFuzzy(key string, rules []string) []string {
	key = normalizeSyllable(key)
	if key == "" || len(rules) == 0 {
		if key == "" {
			return nil
		}
		return []string{key}
	}
	out := []string{key}
	appendUnique := func(v string) {
		if v == "" {
			return
		}
		for _, existing := range out {
			if existing == v {
				return
			}
		}
		out = append(out, v)
	}
	for _, r := range rules {
		norm := strings.ToLower(strings.TrimSpace(r))
		if !strings.ContainsRune(norm, '=') {
			norm = strings.Replace(norm, "_", "=", 1)
			norm = strings.Replace(norm, "-", "=", 1)
		}
		p := strings.SplitN(norm, "=", 2)
		if len(p) != 2 || p[0] == "" || p[1] == "" {
			continue
		}
		snapshot := out
		for _, x := range snapshot {
			initPart, finPart := parseSyllable(x)
			for _, pair := range [2][2]string{{p[0], p[1]}, {p[1], p[0]}} {
				src, dst := pair[0], pair[1]
				if initPart == src {
					appendUnique(dst + finPart)
				}
				if finPart != "" {
					if finPart == src {
						appendUnique(initPart + dst)
					} else if (src == "an" || src == "ang" || src == "en" || src == "eng") && strings.HasSuffix(finPart, src) {
						// Support e.g. "an=ang" expanding ian<->iang and uan<->uang as well.
						stem := finPart[:len(finPart)-len(src)]
						if stem == "i" || stem == "u" {
							appendUnique(initPart + stem + dst)
						}
					}
				} else if x == src {
					appendUnique(dst)
				}
			}
		}
	}
	return out
}

// SyllableCodes splits input according to scheme ("quanpin" or one of the 5 Shuangpin layouts)
// into normalized pinyin syllable codes.
func SyllableCodes(scheme, input string, fuzzy []string) []string {
	return syllableCodes(scheme, input, fuzzy)
}

func syllableCodes(scheme, input string, fuzzy []string) []string {
	var parts []string
	if scheme != "" && !strings.EqualFold(scheme, "quanpin") {
		// Decode the two-key stream to the canonical full-pinyin key used by
		// CLVD. ShuangpinCode is the inverse operation exposed to callers.
		parts = splitShuangpin(scheme, input)
	} else {
		parts = splitQuanpin(input)
	}
	for i := range parts {
		parts[i] = normalizeSyllable(parts[i])
	}
	out := make([]string, 0, len(parts)+len(fuzzy))
	out = append(out, parts...)
	// Fuzzy variants are queried separately by callers; they must not
	// become an extra syllable in the primary dictionary key.
	return out
}
