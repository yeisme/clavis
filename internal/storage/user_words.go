package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/engine"
)

const maxPinnedWordReadings = 4096

// InjectUserWords writes configured words into the pinned lexicon. Single-character
// entries are rejected because pinning them would distort ordinary character ranking.
// A word is also rejected when its readings cannot all be persisted.
func InjectUserWords(ctx context.Context, store *LearningStore, words []string) (rejected []string) {
	if store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, raw := range words {
		word := strings.TrimSpace(raw)
		if word == "" {
			continue
		}
		if !validPinnedWord(word) {
			rejected = append(rejected, word)
			continue
		}
		readings := wordReadings(word)
		if len(readings) == 0 || store.recordPinnedReadings(ctx, word, readings) != nil {
			rejected = append(rejected, word)
		}
	}
	return rejected
}

func (s *LearningStore) recordPinnedReadings(ctx context.Context, word string, readings []string) error {
	if s.db == nil {
		return fmt.Errorf("pin user word: database unavailable")
	}
	now := time.Now().UTC().UnixNano()
	statements := make([]sqlite.Statement, 0, len(readings))
	for _, reading := range readings {
		statements = append(statements, imeWordCommitStatement(reading, word, true, now))
	}
	if _, err := s.db.Transaction(ctx, statements); err != nil {
		return err
	}
	for _, reading := range readings {
		s.mirrorIMECommit(reading, word, "", true, now)
	}
	return nil
}

func wordReadings(word string) []string {
	readings := []string{""}
	for _, r := range word {
		syllables := engine.HanziSyllables(r)
		if len(syllables) == 0 || len(readings) > maxPinnedWordReadings/len(syllables) {
			return nil
		}
		next := make([]string, 0, len(readings)*len(syllables))
		for _, prefix := range readings {
			for _, syllable := range syllables {
				if len(prefix)+len(syllable) > MaxMirrorKeyBytes {
					return nil
				}
				next = append(next, prefix+syllable)
			}
		}
		readings = next
	}
	return readings
}

func validPinnedWord(word string) bool {
	if len(word) > MaxMirrorKeyBytes {
		return false
	}
	count := 0
	for _, r := range word {
		if !engine.IsHanzi(r) {
			return false
		}
		count++
	}
	return count >= 2
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
