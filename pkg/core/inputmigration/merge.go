package inputmigration

import (
	"math"
	"sort"
)

const (
	// maxMergedEntries mirrors the portable learning contract's lexicon
	// capacity without importing internal/storage into this package.
	maxMergedEntries   = 4096
	maxMergedFrequency = uint64(4096)
)

type learningKey struct {
	keyDigest  string
	wordDigest string
}

type bigramKey struct {
	previousDigest string
	wordDigest     string
}

// MergeLearning returns the deterministic union of two learning snapshots.
// Word keys are (KeyDigest, WordDigest); bigram keys are
// (PreviousDigest, WordDigest). Pinned is merged with logical OR. The result
// contains no duplicate key and is sorted by its stable digest key.
//
// Unpinned frequencies are compressed after summing the input frequencies.
// Pinned frequencies retain their checked sum and are never removed by the
// capacity trim. Metadata is merged independently of the entry collections.
func MergeLearning(a, b LearningData) LearningData {
	out := LearningData{
		Enabled:           a.Enabled || b.Enabled,
		SummaryOnly:       a.SummaryOnly && b.SummaryOnly,
		RecoveryObjectRef: sharedRecoveryObjectRef(a.RecoveryObjectRef, b.RecoveryObjectRef),
		Words:             mergeLearningEntries(a.Words, b.Words),
		Bigrams:           mergeBigramEntries(a.Bigrams, b.Bigrams),
		Aggregates:        mergeLearningEntries(a.Aggregates, b.Aggregates),
	}
	return out
}

func sharedRecoveryObjectRef(a, b string) string {
	if a != "" && a == b {
		return a
	}
	return ""
}

func mergeLearningEntries(a, b []LearningEntry) []LearningEntry {
	merged := make(map[learningKey]LearningEntry, len(a)+len(b))
	for _, entry := range append(append([]LearningEntry(nil), a...), b...) {
		key := learningKey{keyDigest: entry.KeyDigest, wordDigest: entry.WordDigest}
		current, exists := merged[key]
		if !exists {
			current = entry
			current.Frequency = 0
			current.Pinned = false
			current.LastUsedNS = 0
		}
		current.Frequency = addFrequency(current.Frequency, entry.Frequency)
		current.Pinned = current.Pinned || entry.Pinned
		if entry.LastUsedNS > current.LastUsedNS {
			current.LastUsedNS = entry.LastUsedNS
		}
		merged[key] = current
	}

	entries := make([]LearningEntry, 0, len(merged))
	for _, entry := range merged {
		if !entry.Pinned {
			entry.Frequency = compressFrequency(entry.Frequency)
		}
		entries = append(entries, entry)
	}
	entries = retainTopLearningEntries(entries)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].KeyDigest != entries[j].KeyDigest {
			return entries[i].KeyDigest < entries[j].KeyDigest
		}
		return entries[i].WordDigest < entries[j].WordDigest
	})
	return entries
}

func mergeBigramEntries(a, b []BigramEntry) []BigramEntry {
	merged := make(map[bigramKey]BigramEntry, len(a)+len(b))
	for _, entry := range append(append([]BigramEntry(nil), a...), b...) {
		key := bigramKey{previousDigest: entry.PreviousDigest, wordDigest: entry.WordDigest}
		current, exists := merged[key]
		if !exists {
			current = entry
			current.Frequency = 0
			current.LastUsedNS = 0
		}
		current.Frequency = addFrequency(current.Frequency, entry.Frequency)
		if entry.LastUsedNS > current.LastUsedNS {
			current.LastUsedNS = entry.LastUsedNS
		}
		merged[key] = current
	}

	entries := make([]BigramEntry, 0, len(merged))
	for _, entry := range merged {
		entry.Frequency = compressFrequency(entry.Frequency)
		entries = append(entries, entry)
	}
	if len(entries) > maxMergedEntries {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Frequency != entries[j].Frequency {
				return entries[i].Frequency > entries[j].Frequency
			}
			if entries[i].LastUsedNS != entries[j].LastUsedNS {
				return entries[i].LastUsedNS > entries[j].LastUsedNS
			}
			if entries[i].PreviousDigest != entries[j].PreviousDigest {
				return entries[i].PreviousDigest < entries[j].PreviousDigest
			}
			return entries[i].WordDigest < entries[j].WordDigest
		})
		entries = entries[:maxMergedEntries]
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].PreviousDigest != entries[j].PreviousDigest {
			return entries[i].PreviousDigest < entries[j].PreviousDigest
		}
		return entries[i].WordDigest < entries[j].WordDigest
	})
	return entries
}

func retainTopLearningEntries(entries []LearningEntry) []LearningEntry {
	pinned := 0
	for _, entry := range entries {
		if entry.Pinned {
			pinned++
		}
	}
	if len(entries) <= maxMergedEntries {
		return entries
	}
	if pinned >= maxMergedEntries {
		kept := make([]LearningEntry, 0, pinned)
		for _, entry := range entries {
			if entry.Pinned {
				kept = append(kept, entry)
			}
		}
		return kept
	}
	keepUnpinned := maxMergedEntries - pinned
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Pinned != entries[j].Pinned {
			return entries[i].Pinned
		}
		if entries[i].Frequency != entries[j].Frequency {
			return entries[i].Frequency > entries[j].Frequency
		}
		if entries[i].LastUsedNS != entries[j].LastUsedNS {
			return entries[i].LastUsedNS > entries[j].LastUsedNS
		}
		if entries[i].KeyDigest != entries[j].KeyDigest {
			return entries[i].KeyDigest < entries[j].KeyDigest
		}
		return entries[i].WordDigest < entries[j].WordDigest
	})
	kept := make([]LearningEntry, 0, maxMergedEntries)
	for _, entry := range entries {
		if entry.Pinned || keepUnpinned > 0 {
			kept = append(kept, entry)
			if !entry.Pinned {
				keepUnpinned--
			}
		}
	}
	return kept
}

func addFrequency(a, b uint64) uint64 {
	if ^uint64(0)-a < b {
		return ^uint64(0)
	}
	return a + b
}

func compressFrequency(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	if n > uint64(math.MaxInt64) {
		return maxMergedFrequency
	}
	compressed := math.Round(1024 * math.Log1p(float64(n)))
	if compressed >= float64(maxMergedFrequency) {
		return maxMergedFrequency
	}
	return uint64(compressed)
}
