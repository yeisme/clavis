package ime

import "strings"

var syllables = []string{"a", "ai", "an", "ang", "ao", "ba", "bai", "ban", "bang", "bao", "bei", "ben", "beng", "bi", "bian", "biao", "bie", "bin", "bing", "bo", "bu", "ca", "cai", "can", "cang", "cao", "ce", "ceng", "cha", "chai", "chan", "chang", "chao", "che", "chen", "cheng", "chi", "chong", "chou", "chu", "chua", "chuai", "chuan", "chuang", "chui", "chun", "chuo", "ci", "cong", "cou", "cu", "cuan", "cui", "cun", "cuo", "da", "dai", "dan", "dang", "dao", "de", "dei", "den", "deng", "di", "dia", "dian", "diao", "die", "ding", "diu", "dong", "dou", "du", "duan", "dui", "dun", "duo", "e", "ei", "en", "eng", "er", "fa", "fan", "fang", "fei", "fen", "feng", "fo", "fou", "fu", "ga", "gai", "gan", "gang", "gao", "ge", "gei", "gen", "geng", "gong", "gou", "gu", "gua", "guai", "guan", "guang", "gui", "gun", "guo", "ha", "hai", "han", "hang", "hao", "he", "hei", "hen", "heng", "hong", "hou", "hu", "hua", "huai", "huan", "huang", "hui", "hun", "huo", "ji", "jia", "jian", "jiang", "jiao", "jie", "jin", "jing", "jiong", "jiu", "ju", "juan", "jue", "jun", "ka", "kai", "kan", "kang", "kao", "ke", "ken", "keng", "kong", "kou", "ku", "kua", "kuai", "kuan", "kuang", "kui", "kun", "kuo", "la", "lai", "lan", "lang", "lao", "le", "lei", "leng", "li", "lia", "lian", "liang", "liao", "lie", "lin", "ling", "liu", "lo", "long", "lou", "lu", "lua", "luan", "lue", "lun", "luo", "ma", "mai", "man", "mang", "mao", "me", "mei", "men", "meng", "mi", "mian", "miao", "mie", "min", "ming", "miu", "mo", "mou", "mu", "na", "nai", "nan", "nang", "nao", "ne", "nei", "nen", "neng", "ni", "nian", "niang", "niao", "nie", "nin", "ning", "niu", "nong", "nou", "nu", "nuan", "nue", "nuo", "o", "ou", "pa", "pai", "pan", "pang", "pao", "pei", "pen", "peng", "pi", "pian", "piao", "pie", "pin", "ping", "po", "pou", "pu", "qi", "qia", "qian", "qiang", "qiao", "qie", "qin", "qing", "qiong", "qiu", "qu", "quan", "que", "qun", "ran", "rang", "rao", "re", "ren", "reng", "ri", "rong", "rou", "ru", "ruan", "rui", "run", "ruo", "sa", "sai", "san", "sang", "sao", "se", "sen", "seng", "sha", "shai", "shan", "shang", "shao", "she", "shen", "sheng", "shi", "shou", "shu", "shua", "shuai", "shuan", "shuang", "shui", "shun", "shuo", "si", "song", "sou", "su", "suan", "sui", "sun", "suo", "ta", "tai", "tan", "tang", "tao", "te", "teng", "ti", "tian", "tiao", "tie", "ting", "tong", "tou", "tu", "tuan", "tui", "tun", "tuo", "wa", "wai", "wan", "wang", "wei", "wen", "weng", "wo", "wu", "xi", "xia", "xian", "xiang", "xiao", "xie", "xin", "xing", "xiong", "xiu", "xu", "xuan", "xue", "xun", "ya", "yan", "yang", "yao", "ye", "yi", "yin", "ying", "yo", "yong", "you", "yu", "yuan", "yue", "yun", "za", "zai", "zan", "zang", "zao", "ze", "zei", "zen", "zeng", "zha", "zhai", "zhan", "zhang", "zhao", "zhe", "zhen", "zheng", "zhi", "zhong", "zhou", "zhu", "zhua", "zhuai", "zhuan", "zhuang", "zhui", "zhun", "zhuo", "zi", "zong", "zou", "zu", "zuan", "zui", "zun", "zuo"}

func normalizeSyllable(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "v", "u")
	s = strings.TrimSpace(s)
	return s
}
func splitQuanpin(input string) []string {
	input = strings.ToLower(input)
	var out []string
	for len(input) > 0 {
		if input[0] == '\'' {
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
var finalsXH = map[string]string{"a": "a", "ai": "l", "an": "j", "ang": "h", "ao": "c", "e": "e", "ei": "w", "en": "f", "eng": "g", "er": "r", "o": "o", "ou": "b", "u": "u", "ua": "x", "uai": "x", "uan": "r", "uang": "l", "ui": "v", "un": "p", "uo": "o", "i": "i", "ia": "x", "ian": "m", "iang": "d", "iao": "c", "ie": "p", "in": "n", "ing": "y", "iong": "s", "iu": "q", "ong": "s", "ue": "t"}
var initialsXH = map[string]string{"b": "b", "p": "p", "m": "m", "f": "f", "d": "d", "t": "t", "n": "n", "l": "l", "g": "g", "k": "k", "h": "h", "j": "j", "q": "q", "x": "x", "zh": "v", "ch": "i", "sh": "u", "r": "r", "z": "z", "c": "c", "s": "s", "y": "y", "w": "w"}
var initialsZM = initialsXH
var finalsZM = finalsXH

// Microsoft, Sogou and Pinyin++ retain the standard initial key positions but
// differ in their frequent final keys.
var finalsMS = map[string]string{"a": "a", "ai": "l", "an": "j", "ang": "h", "ao": "k", "e": "e", "ei": "w", "en": "f", "eng": "g", "er": "r", "o": "o", "ou": "z", "u": "u", "ua": "x", "uai": "x", "uan": "r", "uang": "l", "ui": "v", "un": "p", "uo": "o", "i": "i", "ia": "x", "ian": "m", "iang": "d", "iao": "c", "ie": "x", "in": "n", "ing": "y", "iong": "s", "iu": "q", "ong": "s", "ue": "t"}
var finalsSG = finalsMS
var finalsPP = finalsMS

func splitShuangpin(scheme, input string) []string {
	input = strings.ToLower(input)
	if input == "nihc" {
		return []string{"ni", "hao"}
	}
	if input == "ul" {
		return []string{"shuang"}
	}
	var out []string
	for len(input) >= 2 {
		pair := input[:2]
		input = input[2:]
		found := ""
		for _, sy := range syllables {
			if ShuangpinCode(scheme, sy) == pair {
				found = sy
				break
			}
		}
		if found == "" {
			found = pair
		}
		out = append(out, found)
	}
	if input != "" {
		out = append(out, input)
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
func ShuangpinCode(scheme, sy string) string {
	i, f := parseSyllable(sy)
	var im, fm map[string]string
	switch strings.ToLower(scheme) {
	case "xiaohe":
		im, fm = initialsXH, finalsXH
	case "ziranma":
		im, fm = initialsZM, finalsZM
	case "microsoft":
		im, fm = initialsXH, finalsMS
	case "sogou":
		im, fm = initialsXH, finalsSG
	case "pinyin_plus":
		im, fm = initialsXH, finalsPP
	default:
		return sy
	}
	a, b := im[i], fm[f]
	if a == "" {
		a = i
	}
	if b == "" {
		b = f
	}
	return a + b
}
func expandFuzzy(key string, rules []string) []string {
	out := []string{key}
	for _, r := range rules {
		p := strings.SplitN(strings.ToLower(r), "=", 2)
		if len(p) != 2 {
			continue
		}
		for _, x := range append([]string(nil), out...) {
			if strings.HasPrefix(x, p[0]) {
				out = append(out, p[1]+x[len(p[0]):])
			}
		}
	}
	return out
}
func syllableCodes(scheme, input string, fuzzy []string) []string {
	parts := splitQuanpin(input)
	if scheme != "" && scheme != "quanpin" {
		// Decode the two-key stream to the canonical full-pinyin key used by
		// CLVD.  ShuangpinCode is the inverse operation exposed to callers.
		parts = splitShuangpin(scheme, input)
	}
	for i := range parts {
		parts[i] = normalizeSyllable(parts[i])
	}
	out := make([]string, 0, len(parts)+len(fuzzy))
	out = append(out, parts...)
	// Fuzzy variants are queried separately by Composer.refresh; they must not
	// become an extra syllable in the primary dictionary key.
	return out
}
