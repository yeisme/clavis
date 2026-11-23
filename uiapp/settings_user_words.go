package uiapp

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// renderUserWordsCard draws the "my words" editor: words pinned to the top of the candidate list
// for their pinyin.
//
// This exists because a name like 叶树根 is in no general dictionary — it is the user's own.
// Before this card, adding one meant hand-writing a Rime dictionary file and pointing
// custom_dict_paths at it, which nobody does. The storage and ranking already existed
// (ime_user_lexicon.pinned, loaded before everything else); what was missing was a place to type
// the word.
func (a *AppController) renderUserWordsCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	ie := cfg.Main.InputEngine
	st := a.settingsEditor()
	settingsSourceCard(c, theme, i18n.T("settings.card.user_words"), "user_words", func() {
		settingsHint(c, theme, i18n.T("settings.user_words.help"))

		if len(ie.UserWords) == 0 {
			settingsHint(c, theme, i18n.T("settings.user_words.empty"))
		} else {
			ui.Column(c).FillWidth().Gap(space4).Children(func() {
				for _, w := range ie.UserWords {
					word := w
					ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, word).FontSize(typeBody).TextColor(theme.Text).Grow(1).Shrink(1).SingleLine()
						// Show the readings: a polyphone should be visible before it surprises
						// anyone with the wrong character.
						if readings := strings.Join(inputengine.ReadingsFor(word), " / "); readings != "" {
							ui.Text(c, readings).FontSize(typeMeta).TextColor(theme.TextMuted).Shrink(1).SingleLine()
						}
						if ui.Button(c, i18n.T("common.delete")).
							Key("del-user-word:" + word).
							Padding(space2, space10).
							Shrink(0).
							Clicked() {
							a.removeUserWord(word)
						}
					})
				}
			})
		}

		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &st.myWordDraft).FillWidth().Grow(1)
			if ui.Button(c, i18n.T("settings.user_words.add")).Key("add-user-word").Shrink(0).Clicked() {
				a.addUserWordDraft()
			}
		})
		if msg := strings.TrimSpace(st.myWordError); msg != "" {
			kitErrorBanner(c, theme, msg)
		}
	})
}

// addUserWordDraft appends the typed word, rejecting input that cannot work.
//
// Single characters are refused on purpose: pinning one puts that character above every other
// candidate for its syllable, which breaks that syllable for everything else. A word with no
// known reading is refused too — it could never be reached by typing.
func (a *AppController) addUserWordDraft() {
	st := a.settingsEditor()
	word := strings.TrimSpace(st.myWordDraft)
	st.myWordError = ""
	if word == "" {
		return
	}
	if len([]rune(word)) < 2 {
		st.myWordError = i18n.T("settings.user_words.too_short")
		return
	}
	if len(inputengine.ReadingsFor(word)) == 0 {
		st.myWordError = i18n.T("settings.user_words.unknown_reading")
		return
	}
	for _, existing := range a.Config().Main.InputEngine.UserWords {
		if existing == word {
			st.myWordError = i18n.T("settings.user_words.duplicate")
			return
		}
	}
	if err := a.UpdateMainConfig(func(m *config.MainConfig) {
		m.InputEngine.UserWords = append(append([]string(nil), m.InputEngine.UserWords...), word)
	}); err != nil {
		st.myWordError = err.Error()
		return
	}
	st.myWordDraft = ""
	// The desktopApp re-injects configured words from applySavedConfig (which runs after every
	// config save), so the addition reaches the engine without this layer owning the store.
}

// removeUserWord drops the word from the config and unpins it, so the change applies without a
// restart.
func (a *AppController) removeUserWord(word string) {
	_ = a.UpdateMainConfig(func(m *config.MainConfig) {
		kept := make([]string, 0, len(m.InputEngine.UserWords))
		for _, w := range m.InputEngine.UserWords {
			if w != word {
				kept = append(kept, w)
			}
		}
		m.InputEngine.UserWords = kept
	})
}
