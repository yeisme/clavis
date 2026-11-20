package storage

import (
	"context"
	"strings"
	"unicode"

	"github.com/yeisme/clavis/pkg/core/engine"
)

// InjectUserWords writes configured words into the pinned lexicon. Single-character
// entries are skipped because pinning them would distort ordinary character ranking.
func InjectUserWords(ctx context.Context, store *LearningStore, words []string) (rejected []string) {
	if store == nil {
		return nil
	}
	for _, raw := range words {
		word := strings.TrimSpace(raw)
		if !validPinnedWord(word) {
			if word != "" {
				rejected = append(rejected, word)
			}
			continue
		}
		for _, reading := range wordReadings(word) {
			key := compactReading(reading)
			if key != "" {
				_ = store.RecordIMECommit(ctx, key, word, "", true)
			}
		}
	}
	return rejected
}

func wordReadings(word string) []string {
	choices := make([][]string, 0, len([]rune(word)))
	for _, r := range word {
		syllables := engine.HanziSyllables(r)
		if len(syllables) == 0 {
			return nil
		}
		choices = append(choices, syllables)
	}
	readings := []string{""}
	for _, options := range choices {
		next := make([]string, 0, len(readings)*len(options))
		for _, prefix := range readings {
			for _, syllable := range options {
				if prefix == "" {
					next = append(next, syllable)
				} else {
					next = append(next, prefix+" "+syllable)
				}
			}
		}
		readings = next
	}
	return readings
}

func validPinnedWord(word string) bool {
	runes := []rune(word)
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if !engine.IsHanzi(r) {
			return false
		}
	}
	return true
}

func compactReading(reading string) string {
	var b strings.Builder
	for _, syl := range strings.Fields(reading) {
		b.WriteString(syl)
	}
	return b.String()
}

// HasHanzi reports whether name contains at least one Chinese character.
func HasHanzi(name string) bool {
	for _, r := range name {
		if engine.IsHanzi(r) || unicode.In(r, unicode.Han) {
			return true
		}
	}
	return false
}
