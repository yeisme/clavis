package uiapp

import (
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// Adding a word must go through the same guards the pinning layer relies on, because a bad
// entry here would break a whole syllable: a single character outranks every other candidate for
// that sound, and a word with no reading can never be typed at all.
func TestAddUserWordDraftRejectsUnusableInput(t *testing.T) {
	cases := []struct {
		name    string
		word    string
		wantErr bool
	}{
		{"single character is refused", "是", true},
		{"empty is ignored", "   ", false},
		{"real name is accepted", "叶树根", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := NewAppController(Options{ConfigStore: config.NewConfigStore(t.TempDir())})
			t.Cleanup(ctrl.Close)
			st := ctrl.settingsEditor()
			st.myWordDraft = tc.word
			st.myWordError = ""
			ctrl.addUserWordDraft()
			if got := strings.TrimSpace(st.myWordError) != ""; got != tc.wantErr {
				t.Fatalf("word=%q error=%q wantErr=%v", tc.word, st.myWordError, tc.wantErr)
			}
		})
	}
}

// Every string the card renders must resolve, otherwise the panel shows raw keys.
func TestUserWordsI18nKeysResolve(t *testing.T) {
	for _, lang := range []i18n.Language{i18n.Chinese, i18n.English} {
		i18n.SetLanguage(lang)
		for _, key := range []string{
			"settings.card.user_words", "settings.user_words.help",
			"settings.user_words.empty", "settings.user_words.add",
			"settings.user_words.remove", "settings.user_words.too_short",
			"settings.user_words.unknown_reading", "settings.user_words.duplicate",
		} {
			if got := i18n.T(key); got == "" || got == key {
				t.Fatalf("%v: key %q did not resolve", lang, key)
			}
		}
	}
}
