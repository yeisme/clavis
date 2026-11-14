package storage

import (
	"github.com/yeisme/clavis/pkg/core/ime"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// UserFrequencyAdapter adapts LearningStore's pure-RAM mirrors to inputengine.UserFrequencyProvider.
// Lookups perform zero SQL/disk I/O and hold only brief read locks.
type UserFrequencyAdapter struct {
	store *LearningStore
}

var _ inputengine.UserFrequencyProvider = (*UserFrequencyAdapter)(nil)

// NewUserFrequencyAdapter wraps store as an inputengine.UserFrequencyProvider.
// Returns an untyped nil interface when store is nil so disabled learning is zero-overhead.
func NewUserFrequencyAdapter(store *LearningStore) inputengine.UserFrequencyProvider {
	if store == nil {
		return nil
	}
	return &UserFrequencyAdapter{store: store}
}

// LookupBoost returns the RAM-mirror unigram/bigram/pin boost for (pinyinKey, word, prevWord).
func (a *UserFrequencyAdapter) LookupBoost(pinyinKey, word, prevWord string) int32 {
	if a == nil || a.store == nil {
		return 0
	}
	return a.store.LookupBoost(pinyinKey, word, prevWord)
}

// CustomWordsForPrefix returns learned words whose pinyin starts with prefix, sorted by frequency.
func (a *UserFrequencyAdapter) CustomWordsForPrefix(prefix string) []ime.UserCandidate {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.customUserCandidatesForPrefix(prefix)
}
