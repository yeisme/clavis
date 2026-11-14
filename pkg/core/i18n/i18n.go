// Package i18n is Clavis' lightweight localization layer: a pure-Go string
// table with placeholder interpolation for the languages Clavis ships
// (English and Simplified Chinese).
//
// It has no dependencies beyond the standard library so that every layer of
// the app — the engine, the config validators and the UI — can format
// user-visible text through one table. The active language is process-wide
// and resolved from the [appearance] section of config.toml ("auto" follows
// the operating system's preferred languages).
//
// Keys are dot-separated identifiers ("omnibox.tab.actions"). The English
// table is the source of truth: a key missing from the Chinese table falls
// back to English, and a key missing from both is shown as the key itself so
// gaps are visible in review rather than silently blank. A test walks the
// sources for i18n.T / i18n.Tf call sites and asserts that every key used
// exists in both tables.
package i18n

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
)

// Language is one of the languages Clavis ships. The zero value behaves
// like Auto.
type Language string

const (
	// Auto follows the operating system's preferred UI language.
	Auto Language = "auto"
	// English is the "en" table and the fallback for missing keys.
	English Language = "en"
	// Chinese is the "zh-CN" (Simplified Chinese, Han script) table.
	Chinese Language = "zh-CN"
)

// AllLanguages lists the accepted [appearance].language values, in the order
// the settings UI shows them.
func AllLanguages() []string {
	return []string{string(Auto), string(Chinese), string(English)}
}

// NormalizeLanguage maps a config value, locale tag or environment variable
// to one of the shipped languages. Unknown values fall back to English;
// the empty string means Auto (the config default).
func NormalizeLanguage(s string) Language {
	t := strings.TrimSpace(s)
	if t == "" {
		return Auto
	}
	t = strings.ToLower(t)
	// Strip encoding suffixes as an environment locale would carry
	// ("zh_CN.UTF-8", "en_US.UTF-8").
	if i := strings.IndexAny(t, ".@"); i >= 0 {
		t = t[:i]
	}
	t = strings.ReplaceAll(t, "_", "-")
	switch {
	case t == "auto":
		return Auto
	case strings.HasPrefix(t, "zh"): // zh, zh-cn, zh-hans, zh-sg, zh_cn …
		return Chinese
	case strings.HasPrefix(t, "en"):
		return English
	default:
		return English
	}
}

// Tag returns the BCP 47 tag of the language, "auto" included, as the UI
// toolkit expects it.
func (l Language) Tag() string {
	switch l {
	case Chinese:
		return "zh-Hans"
	case English, Auto:
		return "en"
	}
	return string(l)
}

// current holds the resolved Language (never Auto: Auto is resolved by
// ResolveLanguage before storing). English until something resolves
// otherwise, which keeps headless runs and the default config English.
var current atomic.Value // Language

func init() { current.Store(English) }

// Current returns the active language.
func Current() Language {
	if l, ok := current.Load().(Language); ok {
		return l
	}
	return English
}

// IsChinese reports whether the active language is Chinese. Layout code
// uses it to pick CJK-aware widths, fonts and line heights.
func IsChinese() bool { return Current() == Chinese }

// DisplayLabel returns the active language's display text for a stock label
// that ships in the generated defaults (leader.toml node labels and the
// default AI prompts), so a zh-CN user sees 「左半屏」 for the default
// "Left Half" node. Config files keep their English values; labels the user
// wrote themselves have no "label." table entry and come back unchanged.
func DisplayLabel(label string) string {
	if Current() != Chinese {
		return label
	}
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return label
	}
	if s, ok := zhTable["label."+trimmed]; ok && s != "" {
		return s
	}
	return label
}

// preferred supplies the operating system's preferred UI languages for
// Auto. The desktop shell injects the toolkit's probe; without one (tests,
// CLI runs) the LANG family of environment variables is read instead.
var preferred func() []string

// SetPreferredLanguagesProvider installs the source of the system's
// preferred UI languages, strongest first. Passing nil restores the
// built-in environment probe.
func SetPreferredLanguagesProvider(fn func() []string) { preferred = fn }

// PreferredLanguages returns the system's preferred UI languages,
// strongest first: the injected provider's answer, else the LANG/LC_*  environment.
func PreferredLanguages() []string {
	if preferred != nil {
		if langs := preferred(); len(langs) > 0 {
			return langs
		}
	}
	return preferredFromEnv()
}

// preferredFromEnv reads the POSIX locale environment as a pure-Go
// fallback for platforms without an injected probe.
func preferredFromEnv() []string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.TrimSpace(getenv(key)); v != "" && v != "C" && v != "POSIX" {
			return []string{v}
		}
	}
	return nil
}

// getenv is os.Getenv, split out for tests.
var getenv = os.Getenv

// ResolveLanguage resolves a configured language ("auto", "zh-CN", "en", a
// locale tag) against the system's preferred languages and returns a
// concrete language (never Auto).
func ResolveLanguage(configured string, preferred []string) Language {
	if l := NormalizeLanguage(configured); l != Auto {
		return l
	}
	for _, tag := range preferred {
		if l := NormalizeLanguage(tag); l != Auto {
			return l
		}
	}
	return English
}

// SetLanguage sets the active language. Auto resolves against the
// system's preferred languages.
func SetLanguage(l Language) {
	if l == Auto {
		l = ResolveLanguage(string(Auto), PreferredLanguages())
	}
	current.Store(l)
}

// SyncConfigLanguage applies the [appearance].language value of a loaded
// config. An explicit value ("zh-CN", "en") becomes the active language
// immediately; "auto" (and the empty string) keeps the current language;
// resolving "auto" against the operating system is the app shell's job,
// which knows the toolkit's locale probe — the config store only reacts to
// explicit choices so headless runs stay on the default language.
// The config store calls this after a successful decode, so that the
// diagnostics of the same load validate in the language the user asked for.
func SyncConfigLanguage(configured string) {
	if l := NormalizeLanguage(configured); l != Auto {
		current.Store(l)
	}
}

// T returns the text for key in the active language, falling back to
// English, then to the key itself. Arguments see Tf.
func T(key string, args ...any) string { return tf(Current(), key, args...) }

// Tf formats the text for key in the active language with fmt.Sprintf
// semantics: the table entry is the format string and args its arguments.
// The Chinese entries may reorder arguments with %[2]s-style indexes.
func Tf(key string, args ...any) string { return tf(Current(), key, args...) }

func tf(l Language, key string, args ...any) string {
	if l == Chinese {
		if s, ok := zhTable[key]; ok && s != "" {
			return format(s, args)
		}
	}
	if s, ok := enTable[key]; ok && s != "" {
		return format(s, args)
	}
	if len(args) > 0 {
		return format(key, args)
	}
	return key
}

func format(template string, args []any) string {
	if len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}

// MissingKeys returns the keys present in one table but not the other, for
// the parity test.
func MissingKeys() []string {
	var missing []string
	for k := range enTable {
		if _, ok := zhTable[k]; !ok {
			missing = append(missing, "zh-CN missing: "+k)
		}
	}
	for k := range zhTable {
		if _, ok := enTable[k]; !ok {
			missing = append(missing, "en missing: "+k)
		}
	}
	sort.Strings(missing)
	return missing
}

// HasKey reports whether the key exists in a table, for the call-site
// scan test.
func HasKey(key string) bool {
	_, en := enTable[key]
	_, zh := zhTable[key]
	return en && zh
}
